// Package rpctest helps testing connect servers and the code reading their CallInfo.
package rpctest

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"

	"github.com/seagalia/flock/sdk/rpcserver"
)

// ServerContext gives the context a connect handler gets for a call with the request headers header,
// connect only lets connect.Server.Call attach the server CallInfo.
func ServerContext(ctx context.Context, header map[string]string) context.Context {
	info := &connect.CallInfo{}
	for key, value := range header {
		info.RequestHeader().Set(key, value)
	}
	return ServerContextWithInfo(ctx, info)
}

// ServerContextWithInfo gives the context a connect handler gets for a call described by info
// (request headers, peer address, protocol).
func ServerContextWithInfo(ctx context.Context, info *connect.CallInfo) context.Context {
	const procedure = "/rpctest.Capture/Context"
	var handlerCtx context.Context
	server := connect.NewServer()
	server.Register(connect.Method{
		Spec: connect.Spec{Procedure: procedure, StreamType: connect.StreamTypeUnary},
		Handler: func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
			handlerCtx = ctx
			return nil
		},
	})
	_ = server.Call(ctx, procedure, info, nil) // nolint:errcheck the handler never fails
	return handlerCtx
}

// Serve serves server, with the health and reflection services of rpcserver.Mount, on a random port of 127.0.0.1,
// over TLS with tlsConf or in cleartext HTTP/2 (h2c) when it is nil. It gives the address and a function stopping
// the server.
func Serve(server *connect.Server, tlsConf *tls.Config) (string, func()) {
	mux := http.NewServeMux()
	rpcserver.Mount(mux, server, grpchealth.NewStaticChecker())
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(tlsConf == nil)
	httpServer := &http.Server{Handler: mux, Protocols: protocols, TLSConfig: tlsConf, HTTP2: rpcserver.HTTP2Config()}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		if tlsConf == nil {
			_ = httpServer.Serve(listener) // nolint:errcheck
			return
		}
		_ = httpServer.ServeTLS(listener, "", "") // nolint:errcheck
	}()
	return listener.Addr().String(), func() { _ = httpServer.Close() } // nolint:errcheck
}
