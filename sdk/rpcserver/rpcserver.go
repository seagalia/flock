// Package rpcserver holds what the controller and agent servers share: a connect server with recovery, call logs,
// authentication and prometheus metrics on every call, and the same chain for the grpc-go server the agent keeps
// for envoy's xDS.
package rpcserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"connectrpc.com/grpcreflect/v2"
	"connectrpc.com/validate"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/seagalia/flock/sdk/helpers"
)

// AuthFunc authenticates a call to procedure ("/package.Service/Method") from its context, the returned context
// is the one given to the handler, an error refuses the call.
type AuthFunc func(ctx context.Context, procedure string) (context.Context, error)

// Peer is the client of a call.
type Peer struct {
	Addr string
	// TLS is nil when the client did not connect with TLS
	TLS *tls.ConnectionState
}

func (p Peer) HasClientCertificate() bool {
	return p.TLS != nil && len(p.TLS.PeerCertificates) > 0
}

// PeerFromContext gives the client of the call served with ctx, by the connect server or by the grpc-go one.
func PeerFromContext(ctx context.Context) (Peer, bool) {
	if callInfo, isConnectCall := connect.CallInfoForServerContext(ctx); isConnectCall {
		// nil when the call is not served over HTTP (in-process), TLS() is nil-safe
		httpInfo, _ := connecthttp.ServerInfoForContext(ctx)
		return Peer{Addr: callInfo.PeerAddr, TLS: httpInfo.TLS()}, true
	}
	grpcPeer, isGrpcCall := peer.FromContext(ctx)
	if !isGrpcCall {
		return Peer{}, false
	}
	result := Peer{Addr: grpcPeer.Addr.String()}
	if tlsInfo, isTLS := grpcPeer.AuthInfo.(credentials.TLSInfo); isTLS {
		result.TLS = &tlsInfo.State
	}
	return result, true
}

// NewServer gives a connect server running, around each call: panic recovery, one log line when the call ends,
// authFunc, then prometheus metrics (calls refused by authFunc are logged but not counted). Every received message
// gets its defaults then is checked against its protovalidate rules, an invalid one fails the call with
// InvalidArgument and the violations as error details.
func NewServer(authFunc AuthFunc) *connect.Server {
	return connect.NewServer(
		recoverPanics,
		logCalls,
		authenticate(authFunc),
		countCalls,
		keepErrorMessages,
		codeCutCalls,
		applyDefaults,
		validate.NewServerInterceptor(),
	)
}

// HealthPath is the plain HTTP route answering the health of the server, see Mount.
const HealthPath = "/healthz"

// Mount registers the gRPC health service reporting what checker says and the reflection service on server, creates
// the metrics of every method so they are exposed before their first call, then routes every method on mux.
// Services must be registered before. checker also answers GET HealthPath, unauthenticated and over HTTP/1.1 as
// well: load balancers and orchestrators that can't make a gRPC call probe it.
func Mount(mux connecthttp.ServeMux, server *connect.Server, checker grpchealth.Checker) {
	grpchealth.Register(server, checker)
	grpcreflect.Register(server)
	for spec := range server.Specs() {
		metrics.initialize(spec.Procedure, spec.StreamType)
	}
	connecthttp.Mount(mux, server)
	mux.Handle("GET "+HealthPath, healthHandler(checker))
}

func healthHandler(checker grpchealth.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := grpchealth.StatusUnknown
		resp, err := checker.Check(r.Context(), &grpchealth.CheckRequest{})
		if err == nil {
			status = resp.Status
		}
		httpCode := http.StatusServiceUnavailable
		if status == grpchealth.StatusServing {
			httpCode = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpCode)
		_, _ = fmt.Fprintf(w, `{"status":%q}`+"\n", status.String()) // nolint:errcheck the client went away
	}
}

func recoverPanics(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			logger := loggerFor(spec.StreamType)
			logger.Error().
				Str("procedure", spec.Procedure).
				Str("stack", string(debug.Stack())).
				Msgf("panic triggered: %v", recovered)
			err = connect.Errorf(connect.CodeInternal, "panic triggered: %v", recovered)
		}()
		return next(ctx, spec, stream)
	}
}

func logCalls(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		client, _ := PeerFromContext(ctx)
		protocol := ""
		if callInfo, ok := connect.CallInfoForServerContext(ctx); ok {
			protocol = callInfo.Protocol
		}
		call := startCallLog(spec.Procedure, spec.StreamType, client.Addr, protocol)
		err := next(ctx, spec, stream)
		call.finish(err)
		return err
	}
}

func authenticate(authFunc AuthFunc) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			authCtx, err := authFunc(ctx, spec.Procedure)
			if err != nil {
				return err
			}
			return next(authCtx, spec, stream)
		}
	}
}

func applyDefaults(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		return next(ctx, spec, &defaultingStream{ServerStream: stream})
	}
}

type defaultingStream struct {
	connect.ServerStream
}

// Receive gives the defaults to the message and to the resources it carries (CreateBackendRequest.backend), the
// rules of a resource may require a field its defaults fill
func (s *defaultingStream) Receive(msg any) error {
	err := s.ServerStream.Receive(msg)
	if err != nil {
		return err
	}
	message, isProto := msg.(proto.Message)
	if !isProto {
		return nil
	}
	helpers.ProvDefault(message)
	message.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		isSingleMessage := field.Message() != nil && !field.IsList() && !field.IsMap()
		if isSingleMessage {
			helpers.ProvDefault(value.Message().Interface())
		}
		return true
	})
	return nil
}

func countCalls(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		labels := metrics.start(spec.Procedure, spec.StreamType)
		err := next(ctx, spec, &countingStream{ServerStream: stream, labels: labels})
		metrics.finish(labels, err)
		return err
	}
}

type countingStream struct {
	connect.ServerStream
	labels callLabels
}

func (s *countingStream) Receive(msg any) error {
	err := s.ServerStream.Receive(msg)
	if err == nil {
		metrics.onReceived(s.labels)
	}
	return err
}

func (s *countingStream) Send(msg any) error {
	err := s.ServerStream.Send(msg)
	if err == nil {
		metrics.onSent(s.labels)
	}
	return err
}

// keepErrorMessages gives clients the message of every error, users need it to fix their requests. connect only
// sends errors authored with connect.NewError/Errorf: any other error would reach the client as a bare Unknown code,
// and an error received from another server (an agent called by the controller) as a bare Internal code.
func keepErrorMessages(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		return publicError(next(ctx, spec, stream))
	}
}

// codeCutCalls answers the code of a ctx error that a handler wrapped as a fault of the server, since a store call or a
// lock wait cut by the client's cancel or deadline is no fault of the server. It reads the error chain and not the
// call's ctx, because a handler finishing a change on a detached ctx after the client left can still fail for real.
func codeCutCalls(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		err := next(ctx, spec, stream)
		answered := connect.CodeOf(err)
		isServerFault := answered == connect.CodeInternal || answered == connect.CodeUnknown || answered == connect.CodeUnavailable
		if err == nil || !isServerFault {
			return err
		}
		var code connect.Code
		switch {
		case errors.Is(err, context.Canceled):
			code = connect.CodeCanceled
		case errors.Is(err, context.DeadlineExceeded):
			code = connect.CodeDeadlineExceeded
		default:
			return err
		}
		message := err.Error()
		if connectErr, ok := errors.AsType[*connect.Error](err); ok {
			message = connectErr.Message()
		}
		return connect.NewError(code, message).WithCause(err)
	}
}

func publicError(err error) error {
	if err == nil {
		return nil
	}
	connectErr, isConnectErr := err.(*connect.Error)
	isAuthoredHere := isConnectErr && !connectErr.IsRemote()
	if isAuthoredHere {
		return err
	}
	message := err.Error()
	if isConnectErr {
		// Error() would prefix the message with the code a second time
		message = connectErr.Message()
	}
	return connect.NewError(codeOf(err), message).WithCause(err)
}

func codeOf(err error) connect.Code {
	if errors.Is(err, context.Canceled) {
		return connect.CodeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.CodeDeadlineExceeded
	}
	return connect.CodeOf(err)
}

const (
	maxConcurrentStreams = 1000000
	keepaliveTime        = 30 * time.Second
	keepaliveTimeout     = 5 * time.Second
)

// HTTP2Config is the HTTP/2 configuration of the http.Server serving the connect server: agents keep long lived
// streams, pings detect the dead ones.
func HTTP2Config() *http.HTTP2Config {
	return &http.HTTP2Config{
		MaxConcurrentStreams: maxConcurrentStreams,
		SendPingTimeout:      keepaliveTime,
		PingTimeout:          keepaliveTimeout,
	}
}
