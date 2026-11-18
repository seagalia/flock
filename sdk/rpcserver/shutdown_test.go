package rpcserver_test

import (
	"context"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/sdk/rpcserver"
)

var _ = Describe("Shutdown", func() {
	// startServer serves handler with drainCtx as base context of its requests and calls it once in the background,
	// it gives the server and the error of the call
	startServer := func(handler http.HandlerFunc, drainCtx context.Context) (*http.Server, <-chan error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		server := &http.Server{
			Handler:     handler,
			BaseContext: func(net.Listener) context.Context { return drainCtx },
		}
		go server.Serve(listener) // nolint:errcheck

		callErr := make(chan error, 1)
		go func() {
			resp, err := http.Get("http://" + listener.Addr().String())
			if err == nil {
				resp.Body.Close() // nolint:errcheck
			}
			callErr <- err
		}()
		return server, callErr
	}

	It("cancels the requests once the grace has passed and returns when they end", func() {
		drainCtx, cancelDrain := context.WithCancel(context.Background())
		entered := make(chan struct{})
		server, callErr := startServer(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
		}, drainCtx)
		Eventually(entered).Should(BeClosed())

		start := time.Now()
		err := rpcserver.Shutdown([]*http.Server{server}, cancelDrain, 300*time.Millisecond, 5*time.Second)

		Expect(err).ToNot(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically(">=", 300*time.Millisecond))
		Expect(time.Since(start)).To(BeNumerically("<", 1300*time.Millisecond))
		Eventually(callErr).Should(Receive(BeNil()))
	})

	It("closes a server whose requests still run at the timeout", func() {
		drainCtx, cancelDrain := context.WithCancel(context.Background())
		entered := make(chan struct{})
		release := make(chan struct{})
		DeferCleanup(func() { close(release) })
		server, callErr := startServer(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
		}, drainCtx)
		Eventually(entered).Should(BeClosed())

		start := time.Now()
		err := rpcserver.Shutdown([]*http.Server{server}, cancelDrain, 100*time.Millisecond, 500*time.Millisecond)

		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(time.Since(start)).To(BeNumerically(">=", 500*time.Millisecond))
		Expect(time.Since(start)).To(BeNumerically("<", 1500*time.Millisecond))
		Eventually(callErr).Should(Receive(HaveOccurred()))
	})
})
