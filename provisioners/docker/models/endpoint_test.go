package models_test

import (
	"net/netip"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/docker/models"
)

func onNetworks(networks map[string]string) *container.NetworkSettingsSummary {
	settings := &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{}}
	for name, ip := range networks {
		settings.Networks[name] = &network.EndpointSettings{IPAddress: netip.MustParseAddr(ip)}
	}
	return settings
}

func exposing(portType string, ports ...uint16) []container.PortSummary {
	summaries := make([]container.PortSummary, 0, len(ports))
	for _, port := range ports {
		summaries = append(summaries, container.PortSummary{PrivatePort: port, Type: portType})
	}
	return summaries
}

var _ = Describe("Endpoint", func() {
	var summary container.Summary

	BeforeEach(func() {
		summary = container.Summary{
			ID:              "c1",
			Labels:          map[string]string{"com.docker.compose.project": "shop"},
			Ports:           exposing("tcp", 80),
			NetworkSettings: onNetworks(map[string]string{"shop_default": "172.18.0.2", "shop_lb": "172.19.0.2"}),
		}
		summary.HostConfig.NetworkMode = "shop_lb"
	})

	endpoint := func(labels map[string]string) (string, uint32, uint32, error) {
		base := decode("web", labels)
		ep, err := models.Endpoint(summary, base)
		return ep.GetIp(), ep.GetPort(), ep.GetLoadBalancingWeight(), err
	}

	Describe("network", func() {
		It("takes the network mode by default", func() {
			ip, _, _, err := endpoint(map[string]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("172.19.0.2"))
		})

		It("takes the first network by name when the network mode is none of them", func() {
			summary.HostConfig.NetworkMode = "bridge"
			ip, _, _, err := endpoint(map[string]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("172.18.0.2"))
		})

		It("takes the network of the label, by its name or the compose one", func() {
			ip, _, _, err := endpoint(map[string]string{"io.seagalia.flock.docker_network": "shop_default"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("172.18.0.2"))

			ip, _, _, err = endpoint(map[string]string{"io.seagalia.flock.docker_network": "default"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("172.18.0.2"))
		})

		It("refuses a network the container is not on, or no network", func() {
			_, _, _, err := endpoint(map[string]string{"io.seagalia.flock.docker_network": "backoffice"})
			Expect(err).To(MatchError(ContainSubstring("not on network backoffice")))

			summary.NetworkSettings = nil
			_, _, _, err = endpoint(map[string]string{})
			Expect(err).To(HaveOccurred())
		})

		It("falls back to the ipv6 address", func() {
			summary.NetworkSettings = &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{
				"shop_lb": {GlobalIPv6Address: netip.MustParseAddr("fd00::2")},
			}}
			ip, _, _, err := endpoint(map[string]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("fd00::2"))
		})
	})

	Describe("port", func() {
		It("takes the single exposed port, listed once per address family when published", func() {
			summary.Ports = []container.PortSummary{
				{PrivatePort: 8080, PublicPort: 8080, IP: netip.MustParseAddr("0.0.0.0"), Type: "tcp"},
				{PrivatePort: 8080, PublicPort: 8080, IP: netip.MustParseAddr("::"), Type: "tcp"},
				{PrivatePort: 53, Type: "udp"},
			}
			_, port, _, err := endpoint(map[string]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(port).To(Equal(uint32(8080)))

			_, port, _, err = endpoint(map[string]string{"io.seagalia.flock.protocol": "udp"})
			Expect(err).NotTo(HaveOccurred())
			Expect(port).To(Equal(uint32(53)))
		})

		It("takes the label over the exposed ports", func() {
			summary.Ports = exposing("tcp", 80, 443)
			_, port, _, err := endpoint(map[string]string{"io.seagalia.flock.backend.port": "443"})
			Expect(err).NotTo(HaveOccurred())
			Expect(port).To(Equal(uint32(443)))
		})

		It("asks for the label with no exposed port or several", func() {
			summary.Ports = exposing("tcp", 80, 443)
			_, _, _, err := endpoint(map[string]string{})
			Expect(err).To(MatchError(ContainSubstring("io.seagalia.flock.backend.port")))

			summary.Ports = nil
			_, _, _, err = endpoint(map[string]string{})
			Expect(err).To(HaveOccurred())
		})
	})

	It("weighs 1 unless the label says otherwise", func() {
		_, _, weight, err := endpoint(map[string]string{})
		Expect(err).NotTo(HaveOccurred())
		Expect(weight).To(Equal(uint32(1)))

		_, _, weight, err = endpoint(map[string]string{"io.seagalia.flock.backend.weight": "5"})
		Expect(err).NotTo(HaveOccurred())
		Expect(weight).To(Equal(uint32(5)))
	})
})
