package multi

import (
	"bufio"
	"context"
	"net"
	"slices"
	"strings"

	"github.com/ArthurHlt/emitter"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/observe"
)

// startFakeNatsServer answers the handshake and the pings of the nats protocol and ignores everything else, enough
// for a client to subscribe, drain and close. It gives the url of the server.
func startFakeNatsServer() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(listener.Close)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveFakeNats(conn)
		}
	}()
	return "nats://" + listener.Addr().String()
}

func serveFakeNats(conn net.Conn) {
	defer conn.Close() // nolint:errcheck
	_, err := conn.Write([]byte(`INFO {"server_id":"fake","version":"2.10.0","proto":1,"max_payload":1048576}` + "\r\n"))
	if err != nil {
		return
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if !strings.HasPrefix(line, "PING") {
			continue
		}
		if _, err := conn.Write([]byte("PONG\r\n")); err != nil {
			return
		}
	}
}

var _ = Describe("Nats Run", func() {
	It("stops observing the bus then drains and closes the connection when its context ends", func() {
		conn, err := nats.Connect(startFakeNatsServer())
		Expect(err).ToNot(HaveOccurred())
		n := &Nats{entry: logging.Component("nats_multi"), conn: conn, instanceUuid: "test"}
		DeferCleanup(func() { observe.OffAll(n) })
		// compared by identity, gomega would read the live connection inside n to compare or print it
		isListening := func() bool {
			return slices.ContainsFunc(observe.Listeners(observer.EventType_UPDATE_SUBJECT), func(l emitter.Listener) bool {
				return l == n
			})
		}

		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- n.Run(ctx) }()
		Eventually(isListening).Should(BeTrue())

		cancel()

		Eventually(runDone).Should(Receive(BeNil()))
		Expect(isListening()).To(BeFalse())
		Expect(conn.IsClosed()).To(BeTrue())
	})
})
