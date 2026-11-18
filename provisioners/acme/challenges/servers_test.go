package challenges_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/provisioners/acme/challenges"
)

const acmeIdentifierOID = "1.3.6.1.5.5.7.1.31"

// flakyListener fails its first Accept like a process out of file descriptors would.
type flakyListener struct {
	net.Listener
	failed atomic.Bool
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.failed.CompareAndSwap(false, true) {
		return nil, errors.New("accept: too many open files")
	}
	return l.Listener.Accept()
}

var _ = Describe("servers", func() {
	var (
		table  *challenges.Table
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		table = challenges.NewTable()
		table.Replace("s1", []challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() { cancel() })

	listen := func() net.Listener {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		return listener
	}

	serve := func(fn func() error) <-chan error {
		done := make(chan error, 1)
		go func() { done <- fn() }()
		return done
	}

	get := func(addr, path string) (*http.Response, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		Expect(err).ToNot(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		return resp, string(body)
	}

	dial := func(addr string) (*tls.Conn, error) {
		dialer := &tls.Dialer{Config: &tls.Config{
			ServerName:         "a.test",
			NextProtos:         []string{"acme-tls/1"},
			InsecureSkipVerify: true, //nolint:gosec // the challenge certificate is self-signed
		}}
		dialCtx, cancelDial := context.WithTimeout(ctx, 2*time.Second)
		defer cancelDial()
		conn, err := dialer.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		return conn.(*tls.Conn), nil
	}

	It("answers the key authorization of a token", func() {
		listener := listen()
		serve(func() error { return challenges.ServeHTTP01(ctx, listener, table) })

		resp, body := get(listener.Addr().String(), "/.well-known/acme-challenge/tok-1")

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/plain"))
		Expect(body).To(Equal("ka-1"))
	})

	It("answers 404 for an unknown token and any other path", func() {
		listener := listen()
		serve(func() error { return challenges.ServeHTTP01(ctx, listener, table) })

		resp, _ := get(listener.Addr().String(), "/.well-known/acme-challenge/nope")
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))

		resp, _ = get(listener.Addr().String(), "/other")
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("hands the challenge certificate of the domain to an acme-tls/1 handshake", func() {
		listener := listen()
		serve(func() error { return challenges.ServeTLSALPN01(ctx, listener, table) })

		conn, err := dial(listener.Addr().String())
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()

		leaf := conn.ConnectionState().PeerCertificates[0]
		Expect(leaf.DNSNames).To(ContainElement("a.test"))
		oids := make([]string, 0, len(leaf.Extensions))
		for _, ext := range leaf.Extensions {
			oids = append(oids, ext.Id.String())
		}
		Expect(oids).To(ContainElement(acmeIdentifierOID))
	})

	It("fails the handshake for a domain two secrets answer differently", func() {
		table.Replace("s2", []challenges.Challenge{{Domain: "a.test", Token: "tok-2", KeyAuth: "ka-other"}})
		listener := listen()
		serve(func() error { return challenges.ServeTLSALPN01(ctx, listener, table) })

		_, err := dial(listener.Addr().String())

		Expect(err).To(HaveOccurred())
	})

	It("closes an idle keep-alive connection", func() {
		listener := listen()
		serve(func() error { return challenges.ServeHTTP01WithIdleTimeout(ctx, listener, table, 100*time.Millisecond) })
		conn, err := net.Dial("tcp", listener.Addr().String())
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()

		_, err = conn.Write([]byte("GET /.well-known/acme-challenge/tok-1 HTTP/1.1\r\nHost: a.test\r\n\r\n"))
		Expect(err).ToNot(HaveOccurred())
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, nil)
		Expect(err).ToNot(HaveOccurred())
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		Expect(resp.Close).To(BeFalse())

		Expect(conn.SetReadDeadline(time.Now().Add(2 * time.Second))).To(Succeed())
		_, err = reader.ReadByte()
		Expect(err).To(MatchError(io.EOF))
	})

	It("refuses a request whose headers pass 8 KiB", func() {
		listener := listen()
		serve(func() error { return challenges.ServeHTTP01(ctx, listener, table) })
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"http://"+listener.Addr().String()+"/.well-known/acme-challenge/tok-1", nil)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("X-Filler", strings.Repeat("x", 16<<10))

		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusRequestHeaderFieldsTooLarge))
	})

	It("keeps serving tls-alpn-01 after a failed Accept", func() {
		listener := &flakyListener{Listener: listen()}
		done := serve(func() error { return challenges.ServeTLSALPN01(ctx, listener, table) })

		conn, err := dial(listener.Addr().String())
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()

		Expect(conn.ConnectionState().PeerCertificates[0].DNSNames).To(ContainElement("a.test"))
		Consistently(done, 50*time.Millisecond).ShouldNot(Receive())
	})

	It("stops serving http-01 once ctx ends with a request in flight", func() {
		listener := listen()
		done := serve(func() error { return challenges.ServeHTTP01(ctx, listener, table) })
		conn, err := net.Dial("tcp", listener.Addr().String())
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()
		_, err = conn.Write([]byte("GET /.well-known/acme-challenge/tok-1 HTTP/1.1\r\n"))
		Expect(err).ToNot(HaveOccurred())
		time.Sleep(100 * time.Millisecond)

		cancel()

		Eventually(done, time.Second).Should(Receive(BeNil()))
	})

	It("stops serving tls-alpn-01 once ctx ends with a handshake in flight", func() {
		listener := listen()
		done := serve(func() error { return challenges.ServeTLSALPN01(ctx, listener, table) })
		conn, err := net.Dial("tcp", listener.Addr().String())
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()
		time.Sleep(100 * time.Millisecond)

		cancel()

		Eventually(done, time.Second).Should(Receive(BeNil()))
	})
})
