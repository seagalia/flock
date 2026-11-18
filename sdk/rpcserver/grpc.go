package rpcserver

import (
	"context"
	"errors"
	"runtime/debug"

	"connectrpc.com/connect/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GrpcStreamInterceptor runs the chain of NewServer (panic recovery, call log, authFunc, metrics) on the streams of
// a grpc-go server. The agent keeps one to serve envoy's xDS, go-control-plane only works with grpc-go.
func GrpcStreamInterceptor(authFunc AuthFunc) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		streamType := grpcStreamType(info)
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			logger := loggerFor(streamType)
			logger.Error().
				Str("procedure", info.FullMethod).
				Str("stack", string(debug.Stack())).
				Msgf("panic triggered: %v", recovered)
			err = status.Errorf(codes.Internal, "panic triggered: %v", recovered)
		}()

		client, _ := PeerFromContext(stream.Context())
		call := startCallLog(info.FullMethod, streamType, client.Addr, connect.ProtocolNameGRPC)
		defer func() { call.finish(err) }()

		authCtx, err := authFunc(stream.Context(), info.FullMethod)
		if err != nil {
			return toGrpcStatus(err)
		}

		labels := metrics.start(info.FullMethod, streamType)
		err = handler(srv, &countingGrpcStream{ServerStream: stream, ctx: authCtx, labels: labels})
		metrics.finish(labels, err)
		return err
	}
}

// InitializeGrpcMetrics creates the metrics of every stream of server, so they are exposed before their first call
func InitializeGrpcMetrics(server *grpc.Server) {
	for service, serviceInfo := range server.GetServiceInfo() {
		for _, method := range serviceInfo.Methods {
			streamInfo := &grpc.StreamServerInfo{IsClientStream: method.IsClientStream, IsServerStream: method.IsServerStream}
			metrics.initialize("/"+service+"/"+method.Name, grpcStreamType(streamInfo))
		}
	}
}

func grpcStreamType(info *grpc.StreamServerInfo) connect.StreamType {
	streamType := connect.StreamTypeUnary
	if info.IsClientStream {
		streamType |= connect.StreamTypeClient
	}
	if info.IsServerStream {
		streamType |= connect.StreamTypeServer
	}
	return streamType
}

// toGrpcStatus turns the connect errors of an AuthFunc into the grpc-go status with the same code
func toGrpcStatus(err error) error {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return err
	}
	return status.Error(codes.Code(connectErr.Code()), connectErr.Message())
}

type countingGrpcStream struct {
	grpc.ServerStream
	ctx    context.Context
	labels callLabels
}

func (s *countingGrpcStream) Context() context.Context {
	return s.ctx
}

func (s *countingGrpcStream) RecvMsg(msg any) error {
	err := s.ServerStream.RecvMsg(msg)
	if err == nil {
		metrics.onReceived(s.labels)
	}
	return err
}

func (s *countingGrpcStream) SendMsg(msg any) error {
	err := s.ServerStream.SendMsg(msg)
	if err == nil {
		metrics.onSent(s.labels)
	}
	return err
}
