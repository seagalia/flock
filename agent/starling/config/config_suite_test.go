package config_test

import (
	"net"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Suite")
}

// loopbackInterface is lo on linux and lo0 on darwin, it always exists
func loopbackInterface() string {
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
