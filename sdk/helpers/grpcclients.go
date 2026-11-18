package helpers

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
)

const (
	grpcDialTimeout      = 10 * time.Second
	grpcKeepaliveTime    = 45 * time.Second
	grpcKeepaliveTimeout = 5 * time.Second
)

// GrpcClients makes connect clients calling servers with the gRPC protocol. They share one pool of HTTP/2
// connections, over TLS with tlsConf or in cleartext (h2c) when it is nil: gRPC needs HTTP/2.
type GrpcClients struct {
	httpClient *http.Client
	scheme     string
}

func NewGrpcClients(tlsConf *tls.Config) *GrpcClients {
	protocols := new(http.Protocols)
	scheme := "https"
	if tlsConf != nil {
		protocols.SetHTTP2(true)
	} else {
		protocols.SetUnencryptedHTTP2(true)
		scheme = "http"
	}
	dialer := &net.Dialer{Timeout: grpcDialTimeout}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         dialer.DialContext,
		TLSClientConfig:     tlsConf,
		TLSHandshakeTimeout: grpcDialTimeout,
		Protocols:           protocols,
		// pings detect a dead server on a long lived stream
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: grpcKeepaliveTime,
			PingTimeout:     grpcKeepaliveTimeout,
		},
	}
	return &GrpcClients{
		httpClient: &http.Client{Transport: transport},
		scheme:     scheme,
	}
}

// Client gives a client for the server at addr (host:port), to pass to the generated New<Service>Client
func (g *GrpcClients) Client(addr string, interceptors ...connect.ClientInterceptor) *connect.Client {
	baseURL := g.scheme + "://" + addr
	return connect.NewClient(connecthttp.NewTransport(g.httpClient, baseURL, connecthttp.WithGRPC()), interceptors...)
}

// IsReachable tells if the server at addr answers its gRPC health service. Any answer counts, a refusal (no
// credentials for the health service) or an unimplemented health service included: callers only want to skip
// servers they can't reach (connection, TLS, timeout).
func (g *GrpcClients) IsReachable(ctx context.Context, addr string) bool {
	_, err := grpchealth.NewClient(g.Client(addr)).Check(ctx, &grpchealth.CheckRequest{})
	if err == nil {
		return true
	}
	code := connect.CodeOf(err)
	isUnreachable := code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded || code == connect.CodeCanceled
	return !isUnreachable
}

// Close closes the idle connections of the pool, the ones in use are closed when their call ends
func (g *GrpcClients) Close() {
	g.httpClient.CloseIdleConnections()
}
