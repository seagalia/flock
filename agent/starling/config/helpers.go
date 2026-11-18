package config

import (
	"fmt"
	"net"

	"errors"
)

func getDefaultInterface() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("failed get default interface: %w", err)
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("failed get default interface: %w", err)
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNetAddr, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNetAddr.Contains(localAddr.IP) {
				return iface.Name, nil
			}
		}
	}

	return "", errors.New("failed get default interface")
}
