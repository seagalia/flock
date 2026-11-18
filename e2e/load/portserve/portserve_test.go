package main

import (
	"context"
	"net"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("serve", func() {
	It("answers 200 on every listener", func(ctx SpecContext) {
		listeners := make([]net.Listener, 3)
		for n := range listeners {
			var err error
			listeners[n], err = net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
		}
		serveCtx, stop := context.WithCancel(ctx)
		served := make(chan error, 1)
		go func() { served <- serve(serveCtx, listeners) }()

		for _, listener := range listeners {
			resp, err := http.Get("http://" + listener.Addr().String() + "/health")
			Expect(err).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		}

		stop()
		Eventually(served).Should(Receive(BeNil()))
	})
})
