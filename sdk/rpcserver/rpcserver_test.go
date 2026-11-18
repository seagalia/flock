package rpcserver_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"connectrpc.com/grpchealth/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/types/known/emptypb"

	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

type userKey struct{}

// syncBuffer is written by the server goroutines and read by the specs
type syncBuffer struct {
	mutex sync.Mutex
	buf   bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) lines() []map[string]any {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	result := make([]map[string]any, 0)
	scanner := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for scanner.Scan() {
		line := map[string]any{}
		Expect(json.Unmarshal(scanner.Bytes(), &line)).To(Succeed())
		result = append(result, line)
	}
	return result
}

var _ = Describe("NewServer", func() {
	const service = "rpcserver.test.Echo"
	var (
		handler func(ctx context.Context) error
		clients *helpers.GrpcClients
		addr    string
		logs    *syncBuffer
	)

	// counterValue reads a grpc_server_* counter of a method of the test service, -1 when it is not exposed
	counterValue := func(metricName, method string, extraLabels ...string) float64 {
		families, err := prometheus.DefaultGatherer.Gather()
		Expect(err).ToNot(HaveOccurred())
		for _, family := range families {
			if family.GetName() != metricName {
				continue
			}
			for _, metric := range family.GetMetric() {
				labels := map[string]string{}
				for _, label := range metric.GetLabel() {
					labels[label.GetName()] = label.GetValue()
				}
				isMatching := labels["grpc_service"] == service && labels["grpc_method"] == method
				for i := 0; i+1 < len(extraLabels); i += 2 {
					isMatching = isMatching && labels[extraLabels[i]] == extraLabels[i+1]
				}
				if isMatching {
					return metric.GetCounter().GetValue()
				}
			}
		}
		return -1
	}

	call := func(method string, header map[string]string) error {
		spec := connect.Spec{Procedure: "/" + service + "/" + method, StreamType: connect.StreamTypeUnary}
		ctx, callInfo := connect.NewClientContext(context.Background())
		for key, value := range header {
			callInfo.RequestHeader().Set(key, value)
		}
		return clients.Client(addr).CallUnary(ctx, spec, &emptypb.Empty{}, &emptypb.Empty{})
	}

	BeforeEach(func() {
		logs = &syncBuffer{}
		Expect(logging.Setup(logging.Options{Console: logs, InJson: true, Level: "debug"})).To(Succeed())
		DeferCleanup(func() {
			Expect(logging.Setup(logging.Options{Console: GinkgoWriter})).To(Succeed())
		})

		handler = func(context.Context) error { return nil }
		authFunc := func(ctx context.Context, procedure string) (context.Context, error) {
			callInfo, _ := connect.CallInfoForServerContext(ctx)
			user := callInfo.RequestHeader().Get("x-user")
			if user == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, "no user for "+procedure)
			}
			if callInfo.RequestHeader().Get("x-cut") != "" {
				// stands for a call whose ctx ends while the handler runs: the client left or its deadline passed
				cut, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cut
			}
			return context.WithValue(ctx, userKey{}, user), nil
		}
		server := rpcserver.NewServer(authFunc)
		for _, method := range []string{"Ok", "Refused", "Plain", "Remote", "Panic", "User", "Cut"} {
			server.Register(connect.Method{
				Spec: connect.Spec{Procedure: "/" + service + "/" + method, StreamType: connect.StreamTypeUnary},
				Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
					if err := stream.Receive(&emptypb.Empty{}); err != nil {
						return err
					}
					if err := handler(ctx); err != nil {
						return err
					}
					return stream.Send(&emptypb.Empty{})
				},
			})
		}
		serverTLS, clientTLS := rpctest.NewTLS("client")
		var stop func()
		addr, stop = rpctest.Serve(server, serverTLS)
		DeferCleanup(stop)
		clients = helpers.NewGrpcClients(clientTLS)
		DeferCleanup(clients.Close)
	})

	Context("authentication", func() {
		It("refuses the call when the auth func fails, without running the handler", func() {
			handlerRan := false
			handler = func(context.Context) error { //nolint:unparam // matches the func type it stands in for
				handlerRan = true
				return nil
			}
			err := call("Refused", nil)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated))
			Expect(err).To(MatchError(ContainSubstring("no user for /rpcserver.test.Echo/Refused")))
			Expect(handlerRan).To(BeFalse())
		})

		It("gives the handler the context of the auth func", func() {
			var user any
			handler = func(ctx context.Context) error { //nolint:unparam // matches the func type it stands in for
				user = ctx.Value(userKey{})
				return nil
			}
			Expect(call("User", map[string]string{"x-user": "bob"})).To(Succeed())
			Expect(user).To(Equal("bob"))
		})
	})

	It("refuses an invalid request with the violations as error details, before the handler", func(ctx SpecContext) {
		serverTLS, clientTLS := rpctest.NewTLS("client")
		server := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) { return ctx, nil })
		spec := connect.Spec{Procedure: "/" + service + "/Read", StreamType: connect.StreamTypeUnary}
		handlerRan := false
		server.Register(connect.Method{
			Spec: spec,
			Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
				if err := stream.Receive(&svccore.UuidRequest{}); err != nil {
					return err
				}
				handlerRan = true
				return stream.Send(&emptypb.Empty{})
			},
		})
		validateAddr, stop := rpctest.Serve(server, serverTLS)
		DeferCleanup(stop)
		validateClients := helpers.NewGrpcClients(clientTLS)
		DeferCleanup(validateClients.Close)

		err := validateClients.Client(validateAddr).CallUnary(ctx, spec, &svccore.UuidRequest{}, &emptypb.Empty{})

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("UUID")))
		var connectErr *connect.Error
		Expect(errors.As(err, &connectErr)).To(BeTrue())
		Expect(connectErr.Details()).ToNot(BeEmpty())
		detail, err := connectproto.UnmarshalErrorDetail(connectErr.Details()[0])
		Expect(err).ToNot(HaveOccurred())
		Expect(detail).To(BeAssignableToTypeOf(&validatepb.Violations{}))
		Expect(detail.(*validatepb.Violations).GetViolations()).ToNot(BeEmpty())
		Expect(handlerRan).To(BeFalse())
	})

	Context("errors", func() {
		It("keeps the message of a plain error", func() {
			handler = func(context.Context) error { return fmt.Errorf("boom") }
			err := call("Plain", map[string]string{"x-user": "bob"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnknown))
			Expect(err).To(MatchError(ContainSubstring("boom")))
		})

		It("forwards the code and message of an error received from another server", func() {
			handler = func(context.Context) error {
				return connect.NewError(connect.CodeNotFound, "agent says no").WithRemote()
			}
			err := call("Remote", map[string]string{"x-user": "bob"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
			Expect(err).To(MatchError(ContainSubstring("agent says no")))
		})

		It("answers and counts Canceled for an internal error of a call whose ctx ended", func() {
			handler = func(ctx context.Context) error {
				return connect.Errorf(connect.CodeInternal, "read store: %v", ctx.Err()).WithCause(ctx.Err())
			}
			canceledBefore := counterValue("grpc_server_handled_total", "Cut", "grpc_code", "Canceled")
			err := call("Cut", map[string]string{"x-user": "bob", "x-cut": "1"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeCanceled))
			Expect(err).To(MatchError(ContainSubstring("read store: context canceled")))
			Expect(counterValue("grpc_server_handled_total", "Cut", "grpc_code", "Canceled")).To(Equal(canceledBefore + 1))
		})

		It("answers Canceled for an unavailable error caused by the end of the ctx, as a lock wait the client gave up", func() {
			handler = func(ctx context.Context) error {
				return connect.Errorf(connect.CodeUnavailable, "could not lock: %v", ctx.Err()).WithCause(ctx.Err())
			}
			err := call("Cut", map[string]string{"x-user": "bob", "x-cut": "1"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeCanceled))
		})

		It("keeps an internal error not caused by the end of the ctx, as one of work detached from the call", func() {
			handler = func(context.Context) error {
				return connect.Errorf(connect.CodeInternal, "write store: disk full").WithCause(errors.New("disk full"))
			}
			err := call("Cut", map[string]string{"x-user": "bob", "x-cut": "1"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInternal))
		})

		It("recovers a panic as an internal error", func() {
			handler = func(context.Context) error { panic("kaboom") }
			err := call("Panic", map[string]string{"x-user": "bob"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInternal))
			Expect(err).To(MatchError(ContainSubstring("panic triggered: kaboom")))
		})
	})

	Context("metrics", func() {
		It("counts the calls the auth func accepted, with their code", func() {
			Expect(counterValue("grpc_server_handled_total", "Ok", "grpc_code", "NotFound")).To(BeZero(), "initialized at zero")
			startedBefore := counterValue("grpc_server_started_total", "Ok")
			handledBefore := counterValue("grpc_server_handled_total", "Ok", "grpc_code", "OK")
			Expect(call("Ok", map[string]string{"x-user": "bob"})).To(Succeed())
			Expect(counterValue("grpc_server_started_total", "Ok")).To(Equal(startedBefore + 1))
			Expect(counterValue("grpc_server_handled_total", "Ok", "grpc_code", "OK")).To(Equal(handledBefore + 1))
			Expect(counterValue("grpc_server_msg_received_total", "Ok")).To(BeNumerically(">=", 1))
			Expect(counterValue("grpc_server_msg_sent_total", "Ok")).To(BeNumerically(">=", 1))
		})

		It("does not count refused calls", func() {
			startedBefore := counterValue("grpc_server_started_total", "Refused")
			Expect(call("Refused", nil)).ToNot(Succeed())
			Expect(counterValue("grpc_server_started_total", "Refused")).To(Equal(startedBefore))
		})
	})

	Context("logs", func() {
		finishedCalls := func() []map[string]any {
			result := make([]map[string]any, 0)
			for _, line := range logs.lines() {
				if line["message"] == "finished call" {
					result = append(result, line)
				}
			}
			return result
		}

		It("logs each call when it ends, refused ones included", func() {
			Expect(call("Ok", map[string]string{"x-user": "bob"})).To(Succeed())
			Expect(call("Refused", nil)).ToNot(Succeed())

			Eventually(finishedCalls).Should(HaveLen(2))
			ok, refused := finishedCalls()[0], finishedCalls()[1]
			Expect(ok).To(HaveKeyWithValue("component", "grpc_unary_unary"))
			Expect(ok).To(HaveKeyWithValue("grpc.service", service))
			Expect(ok).To(HaveKeyWithValue("grpc.method", "Ok"))
			Expect(ok).To(HaveKeyWithValue("grpc.code", "OK"))
			Expect(ok).To(HaveKeyWithValue("protocol", "grpc"))
			Expect(ok).To(HaveKeyWithValue("level", "info"))
			Expect(refused).To(HaveKeyWithValue("grpc.code", "Unauthenticated"))
			Expect(refused).To(HaveKeyWithValue("grpc.error", ContainSubstring("no user")))
		})

		It("skips successful health checks", func() {
			healthClient := grpchealth.NewClient(clients.Client(addr, func(next connect.ClientFunc) connect.ClientFunc {
				return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
					callInfo, _ := connect.CallInfoForClientContext(ctx)
					callInfo.RequestHeader().Set("x-user", "bob")
					return next(ctx, spec)
				}
			}))
			resp, err := healthClient.Check(context.Background(), &grpchealth.CheckRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.Status).To(Equal(grpchealth.StatusServing))

			Expect(call("Ok", map[string]string{"x-user": "bob"})).To(Succeed())
			Eventually(finishedCalls).Should(HaveLen(1))
			Expect(finishedCalls()[0]).To(HaveKeyWithValue("grpc.method", "Ok"))
		})
	})
})

type statusChecker grpchealth.Status

func (c statusChecker) Check(context.Context, *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	return &grpchealth.CheckResponse{Status: grpchealth.Status(c)}, nil
}

var _ = Describe("Mount", func() {
	// httptest serves HTTP/1.1 only: what a probe that can't speak gRPC uses
	getHealth := func(checker grpchealth.Checker) (*http.Response, string) {
		mux := http.NewServeMux()
		rpcserver.Mount(mux, rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) {
			return nil, connect.Errorf(connect.CodeUnauthenticated, "no user")
		}), checker)
		httpServer := httptest.NewServer(mux)
		DeferCleanup(httpServer.Close)

		resp, err := http.Get(httpServer.URL + rpcserver.HealthPath)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.ProtoMajor).To(Equal(1))
		return resp, string(body)
	}

	It("answers the health over HTTP/1.1 without authentication", func() {
		resp, body := getHealth(statusChecker(grpchealth.StatusServing))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
		Expect(body).To(MatchJSON(`{"status":"serving"}`))
	})

	It("answers an error when the checker reports the server not serving", func() {
		resp, body := getHealth(statusChecker(grpchealth.StatusNotServing))
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
		Expect(body).To(MatchJSON(`{"status":"not_serving"}`))
	})

	// a browser on the plain http listener of the controller, or the cli in http1 mode, calls streams over HTTP/1.1
	It("serves a server stream over HTTP/1.1 with the Connect protocol", func(ctx SpecContext) {
		const service = "rpcserver.test.Echo"
		server := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) { return ctx, nil })
		spec := connect.Spec{Procedure: "/" + service + "/Follow", StreamType: connect.StreamTypeServer}
		server.Register(connect.Method{
			Spec: spec,
			Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
				if err := stream.Receive(&emptypb.Empty{}); err != nil {
					return err
				}
				return stream.Send(&emptypb.Empty{})
			},
		})
		mux := http.NewServeMux()
		rpcserver.Mount(mux, server, grpchealth.NewStaticChecker())
		httpServer := httptest.NewServer(mux)
		DeferCleanup(httpServer.Close)
		client := connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))

		stream, err := client.CallServerStream(ctx, spec, &emptypb.Empty{})
		Expect(err).ToNot(HaveOccurred())
		defer stream.Close() // nolint:errcheck

		Expect(stream.Receive(&emptypb.Empty{})).To(Succeed())
		Expect(stream.Receive(&emptypb.Empty{})).To(MatchError(io.EOF))
	})
})
