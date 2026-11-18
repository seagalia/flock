package challenges

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/challenge/tlsalpn01"
)

const (
	http01PathPrefix    = "/.well-known/acme-challenge/"
	alpnProtocol        = "acme-tls/1"
	headerTimeout       = 10 * time.Second
	httpWriteTimeout    = 10 * time.Second
	httpIdleTimeout     = 30 * time.Second
	maxHeaderBytes      = 8 << 10
	handshakeTimeout    = 10 * time.Second
	httpShutdownTimeout = 500 * time.Millisecond
	minAcceptRetryDelay = 5 * time.Millisecond
	maxAcceptRetryDelay = time.Second
)

// ServeHTTP01 answers the http-01 challenges of the table on listener until ctx ends, then returns nil.
func ServeHTTP01(ctx context.Context, listener net.Listener, table *Table) error {
	return serveHTTP01(ctx, listener, table, httpIdleTimeout)
}

func serveHTTP01(ctx context.Context, listener net.Listener, table *Table, idleTimeout time.Duration) error {
	// the port stays open to anyone for the life of the provisioner, no client may hold its connections forever
	server := &http.Server{
		Handler:           http01Handler(table),
		ReadHeaderTimeout: headerTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve http-01: %w", err)
	case <-ctx.Done():
	}

	// ctx is already done, the shutdown needs its own deadline
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http-01: %w", err)
	}
	return nil
}

func http01Handler(table *Table) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, isChallengePath := strings.CutPrefix(r.URL.Path, http01PathPrefix)
		if r.Method != http.MethodGet || !isChallengePath || token == "" {
			http.NotFound(w, r)
			return
		}
		keyAuth, found := table.KeyAuthByToken(token)
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(keyAuth))
	})
}

// ServeTLSALPN01 answers the tls-alpn-01 handshakes of the table on listener until ctx ends, then waits for the
// handshakes in flight and returns nil.
func ServeTLSALPN01(ctx context.Context, listener net.Listener, table *Table) error {
	tlsConfig := &tls.Config{
		NextProtos: []string{alpnProtocol},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if !slices.Contains(hello.SupportedProtos, alpnProtocol) {
				return nil, fmt.Errorf("client does not offer %s", alpnProtocol)
			}
			keyAuth, found := table.KeyAuthByDomain(hello.ServerName)
			if !found {
				return nil, fmt.Errorf("no single challenge answer for %q", hello.ServerName)
			}
			return tlsalpn01.ChallengeCert(hello.ServerName, keyAuth)
		},
	}

	// closing the listener is what ends Accept when ctx ends
	stopWatching := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopWatching()

	var inFlight sync.WaitGroup
	defer inFlight.Wait()
	// frees the port before the wait when Accept fails for good
	defer func() { _ = listener.Close() }()

	var retryDelay time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("accept tls-alpn-01: %w", err)
			}
			// like http.Server.Serve, an error such as running out of file descriptors passes once conns close
			retryDelay = min(max(2*retryDelay, minAcceptRetryDelay), maxAcceptRetryDelay)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(retryDelay):
			}
			continue
		}
		retryDelay = 0
		inFlight.Go(func() {
			handshakeAndClose(ctx, tls.Server(conn, tlsConfig))
		})
	}
}

// handshakeAndClose only needs the handshake: the CA reads the certificate and hangs up.
func handshakeAndClose(ctx context.Context, conn *tls.Conn) {
	defer conn.Close()

	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	// a failed handshake is the CA's answer to a refused domain, there is nothing to report
	_ = conn.HandshakeContext(handshakeCtx)
}
