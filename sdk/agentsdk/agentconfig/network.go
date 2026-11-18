package agentconfig

import (
	"fmt"
	"net"

	"errors"
)

func FirstAddrOfInterface(iface string) (string, error) {
	ifaceObj, err := net.InterfaceByName(iface)
	if err != nil {
		return "", fmt.Errorf("failed to get interface: %w", err)
	}
	addrs, err := ifaceObj.Addrs()
	if err != nil {
		return "", fmt.Errorf("failed to get interface addresses: %w", err)
	}
	if len(addrs) == 0 {
		return "", errors.New("no addresses on interface")
	}
	switch v := addrs[0].(type) {
	case *net.IPNet:
		return v.IP.String(), nil
	case *net.IPAddr:
		return v.IP.String(), nil
	}
	return "", errors.New("failed to get interface address")
}

// OutboundIP gives the local address the host uses to reach the internet. No packet is sent: dialing udp only
// picks the route.
func OutboundIP() (net.IP, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil, fmt.Errorf("failed get outbound IP: %w", err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP, nil
}
