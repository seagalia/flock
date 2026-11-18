package models_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/provisioners/docker/models"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("TLS over tcp", func() {
	var converter *models.Converter

	BeforeEach(func() {
		var err error
		converter, err = models.NewConverter(&config.Config{Docker: config.DockerConfig{
			Eds: config.EdsConfig{AdvertiseUrl: "grpc://provisioner:18003"},
		}}, "agent-1")
		Expect(err).NotTo(HaveOccurred())
	})

	tlsTcp := func(service string, labels map[string]string) *models.Base {
		GinkgoHelper()
		labels["io.seagalia.flock.protocol"] = "tcp"
		labels["io.seagalia.flock.listen_port"] = "5432"
		return decode(service, labels)
	}

	It("terminates TLS for the domains with the certificate the acme provisioner orders", func() {
		base := tlsTcp("db", map[string]string{
			"io.seagalia.flock.tls.enable": "true",
			"io.seagalia.flock.domains":    "db.example.com",
		})
		Expect(base.IsTlsTcp()).To(BeTrue())
		certificate, err := converter.Certificate(base)
		Expect(err).NotTo(HaveOccurred())
		Expect(certificate.GetDomainNames()).To(Equal([]string{"db.example.com"}))

		tlsRoute, err := converter.TlsTcpRoute(base, "backend-db", "cert-db")
		Expect(err).NotTo(HaveOccurred())
		Expect(tlsRoute.GetTls().GetServerNames()).To(Equal([]string{"db.example.com"}))
		Expect(tlsRoute.GetTls().GetCertificatesUuid()).To(Equal([]string{"cert-db"}))

		ref, err := converter.TlsTcpVirtualServerRef(base)
		Expect(err).NotTo(HaveOccurred())
		Expect(ref).To(Equal("io.seagalia.flock.docker/tls-5432"))
		virtualServer, err := converter.DefaultTlsTcpVirtualServer(base, tlsRoute)
		Expect(err).NotTo(HaveOccurred())
		Expect(helpers.Validate(virtualServer)).To(Succeed())
		Expect(virtualServer.GetName()).To(Equal(ref))
		Expect(virtualServer.GetPort()).To(Equal(uint32(5432)))

		_, err = converter.VirtualServer(base, "backend-db")
		Expect(err).To(HaveOccurred())
	})

	It("passes the connections through with no certificate", func() {
		base := tlsTcp("db", map[string]string{
			"io.seagalia.flock.tls.passthrough": "true",
			"io.seagalia.flock.domains":         "db.example.com",
		})
		certificate, err := converter.Certificate(base)
		Expect(err).NotTo(HaveOccurred())
		Expect(certificate).To(BeNil())

		tlsRoute, err := converter.TlsTcpRoute(base, "backend-db", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tlsRoute.GetPassthrough().GetServerNames()).To(Equal([]string{"db.example.com"}))
	})

	It("routes on the name of the service by default, and every connection with the wildcard domain", func() {
		byService, err := converter.TlsTcpRoute(tlsTcp("db", map[string]string{"io.seagalia.flock.tls.passthrough": "true"}), "backend-db", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(byService.GetPassthrough().GetServerNames()).To(Equal([]string{"db"}))

		catchAll, err := converter.TlsTcpRoute(tlsTcp("db", map[string]string{
			"io.seagalia.flock.tls.passthrough": "true",
			"io.seagalia.flock.domains":         "*",
		}), "backend-db", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(catchAll.GetPassthrough().GetServerNames()).To(BeEmpty())
	})

	It("refuses passthrough on http and termination with no certificate", func() {
		_, err := converter.VirtualHost(decode("web", map[string]string{"io.seagalia.flock.tls.passthrough": "true"}), "b", "")
		Expect(err).To(HaveOccurred())

		base := tlsTcp("db", map[string]string{"io.seagalia.flock.tls.enable": "true"})
		_, err = converter.TlsTcpRoute(base, "backend-db", "")
		Expect(err).To(HaveOccurred())
	})

	It("never creates a TLS virtual server named by label", func() {
		base := tlsTcp("db", map[string]string{
			"io.seagalia.flock.tls.passthrough": "true",
			"io.seagalia.flock.virtual_server":  "edge",
		})
		ref, err := converter.TlsTcpVirtualServerRef(base)
		Expect(err).NotTo(HaveOccurred())
		Expect(ref).To(Equal("edge"))
		_, err = converter.DefaultTlsTcpVirtualServer(base, &vs.TlsTcpRouteConfig{})
		Expect(err).To(HaveOccurred())
	})

	It("replaces the route of the same backend and removes the routes of a backend", func() {
		db := tlsTcp("db", map[string]string{"io.seagalia.flock.tls.passthrough": "true", "io.seagalia.flock.domains": "db.example.com"})
		cache := tlsTcp("cache", map[string]string{"io.seagalia.flock.tls.passthrough": "true", "io.seagalia.flock.domains": "cache.example.com"})
		dbRoute, err := converter.TlsTcpRoute(db, "backend-db", "")
		Expect(err).NotTo(HaveOccurred())
		cacheRoute, err := converter.TlsTcpRoute(cache, "backend-cache", "")
		Expect(err).NotTo(HaveOccurred())

		shared, err := converter.DefaultTlsTcpVirtualServer(db, dbRoute)
		Expect(err).NotTo(HaveOccurred())
		shared, err = models.JoinTlsTcpRoute(shared, cacheRoute)
		Expect(err).NotTo(HaveOccurred())
		shared, err = models.JoinTlsTcpRoute(shared, dbRoute)
		Expect(err).NotTo(HaveOccurred())
		Expect(helpers.Validate(shared)).To(Succeed())
		Expect(shared.GetTlsTcpRoutesConfig().GetRoutes()).To(HaveLen(2))

		cleaned := models.RemoveTlsTcpRoutes(shared, "backend-db")
		Expect(cleaned.GetTlsTcpRoutesConfig().GetRoutes()).To(HaveLen(1))
		Expect(cleaned.GetTlsTcpRoutesConfig().GetRoutes()[0].GetBackendUuid()).To(Equal("backend-cache"))
	})
})
