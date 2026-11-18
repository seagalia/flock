// Package fakeconnect provides an in-process connect server for unit tests,
// so code using the flock sdk clients can be tested without a controller.
package fakeconnect

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type Handler func(req proto.Message) (proto.Message, error)

// StreamHandler answers a server streaming call with all the messages to send, in order.
type StreamHandler func(req proto.Message) ([]proto.Message, error)

// Conn serves the handlers registered by procedure (e.g. svcvsconnect.VirtualServerReadVSProcedure)
// to the clients made by Client.
type Conn struct {
	mu             sync.Mutex
	handlers       map[string]Handler
	streamHandlers map[string]StreamHandler
	openStreams    map[string]bool
	calls          []string
	requests       map[string][]proto.Message
	headers        map[string][]http.Header
	server         *connect.Server
}

func New() *Conn {
	c := &Conn{
		handlers:       map[string]Handler{},
		streamHandlers: map[string]StreamHandler{},
		openStreams:    map[string]bool{},
		requests:       map[string][]proto.Message{},
		headers:        map[string][]http.Header{},
		server:         connect.NewServer(),
	}
	// Handlers are registered by procedure name only, so there is no generated
	// connect.Method to register: everything goes through the unknown handler.
	c.server.SetUnknownHandler(c.serve)
	return c
}

func (c *Conn) Handle(procedure string, handler Handler) *Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[procedure] = handler
	return c
}

func (c *Conn) HandleStream(procedure string, handler StreamHandler) *Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streamHandlers[procedure] = handler
	return c
}

// KeepStreamOpen makes the stream of the procedure wait, once its messages are sent, until the call is cancelled
// instead of ending. It is how a followed stream (logs) looks while the server has nothing more to say.
func (c *Conn) KeepStreamOpen(procedure string) *Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openStreams[procedure] = true
	return c
}

// Client gives a connect client for the generated service clients (e.g. svcvsconnect.NewVirtualServerClient).
func (c *Conn) Client(interceptors ...connect.ClientInterceptor) *connect.Client {
	return connect.NewClient(connectinprocess.New(c.server), interceptors...)
}

// Calls gives the called procedures in call order.
func (c *Conn) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.calls...)
}

func (c *Conn) Requests(procedure string) []proto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]proto.Message{}, c.requests[procedure]...)
}

// RequestHeaders gives the headers of each call of the procedure.
func (c *Conn) RequestHeaders(procedure string) []http.Header {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]http.Header{}, c.headers[procedure]...)
}

func (c *Conn) serve(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
	procedure := spec.Procedure
	c.mu.Lock()
	handler, isUnary := c.handlers[procedure]
	streamHandler, isStream := c.streamHandlers[procedure]
	keepOpen := c.openStreams[procedure]
	c.mu.Unlock()
	method, err := findMethod(procedure)
	if err != nil {
		return err
	}
	req, err := newRequest(method)
	if err != nil {
		return err
	}
	if err := stream.Receive(req); err != nil {
		return err
	}

	if !isUnary && !isStream {
		// Same as a grpc connection: an unregistered stream is refused before
		// the request is sent, an unregistered unary call is recorded first.
		if !method.IsStreamingServer() {
			c.record(ctx, procedure, req)
		}
		return connect.Errorf(connect.CodeUnimplemented, "fakeconnect: no handler for %s", procedure)
	}
	c.record(ctx, procedure, req)

	if isUnary {
		resp, err := handler(req)
		if err != nil {
			return err
		}
		return stream.Send(resp)
	}

	messages, err := streamHandler(req)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if err := stream.Send(message); err != nil {
			return err
		}
	}
	if keepOpen {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (c *Conn) record(ctx context.Context, procedure string, req proto.Message) {
	headers := http.Header{}
	if callInfo, ok := connect.CallInfoForServerContext(ctx); ok {
		for key, values := range callInfo.RequestHeader().All() {
			headers[key] = append([]string{}, values...)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, procedure)
	c.requests[procedure] = append(c.requests[procedure], req)
	c.headers[procedure] = append(c.headers[procedure], headers)
}

// findMethod resolves the procedure ("/pkg.Service/Method") because the unknown handler gets no schema.
func findMethod(procedure string) (protoreflect.MethodDescriptor, error) {
	fullName := strings.ReplaceAll(strings.TrimPrefix(procedure, "/"), "/", ".")
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(fullName))
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "fakeconnect: unknown procedure %s: %v", procedure, err)
	}
	method, isMethod := descriptor.(protoreflect.MethodDescriptor)
	if !isMethod {
		return nil, connect.Errorf(connect.CodeInternal, "fakeconnect: %s is not a method", procedure)
	}
	return method, nil
}

// newRequest gives the generated type, handlers type-assert their request.
func newRequest(method protoreflect.MethodDescriptor) (proto.Message, error) {
	messageType, err := protoregistry.GlobalTypes.FindMessageByName(method.Input().FullName())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "fakeconnect: request type of %s: %v", method.FullName(), err)
	}
	return messageType.New().Interface(), nil
}
