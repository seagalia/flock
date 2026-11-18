package bgp

import (
	"net"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/logging"
)

func TestBgp(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Bgp Suite")
}

func freeTcpPort() uint16 {
	GinkgoHelper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	defer listener.Close()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func loopbackInterface() string {
	GinkgoHelper()
	ifaces, err := net.Interfaces()
	Expect(err).ToNot(HaveOccurred())
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface.Name
		}
	}
	Fail("no loopback interface found")
	return ""
}
