package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// lastStatus keeps the latest status hold reported
type lastStatus struct {
	mutex  sync.Mutex
	status status
}

func (l *lastStatus) report(s status) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.status = s
}

func (l *lastStatus) get() status {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.status
}

func addrOf(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

var _ = Describe("hold", func() {
	It("holds every connection and sends requests", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
		DeferCleanup(server.Close)
		last := &lastStatus{}

		hold(ctx, []string{addrOf(server)}, 50, 300*time.Millisecond, 50*time.Millisecond, last.report)

		Expect(last.get()).To(And(
			HaveField("Established", 50),
			HaveField("Failed", 0),
			HaveField("Requests", BeNumerically(">=", 50)),
			HaveField("Done", true),
		))
	})

	It("counts refused connections as failed", func(ctx SpecContext) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		closedAddr := lis.Addr().String()
		Expect(lis.Close()).To(Succeed())
		last := &lastStatus{}

		hold(ctx, []string{closedAddr}, 10, 200*time.Millisecond, 50*time.Millisecond, last.report)

		Expect(last.get()).To(And(
			HaveField("Established", 0), HaveField("Failed", 10), HaveField("Done", true),
			HaveField("LastError", ContainSubstring("refused")),
		))
	})

	It("counts a connection closed by the server as failed", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Connection", "close")
			_, _ = w.Write([]byte("bye"))
		}))
		DeferCleanup(server.Close)
		last := &lastStatus{}

		hold(ctx, []string{addrOf(server)}, 5, 300*time.Millisecond, 50*time.Millisecond, last.report)

		Expect(last.get()).To(And(HaveField("Established", 0), HaveField("Failed", 5), HaveField("Done", true)))
	})
})
