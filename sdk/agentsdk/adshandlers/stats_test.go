package adshandlers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// the metric lives as long as the process, every spec starts with it empty like the stores
var _ = BeforeEach(func() {
	stats.resourceInfo.Reset()
})

var _ = Describe("resource info metric", func() {
	signaturesOf := func(uuid string) []string {
		collected := make(chan prometheus.Metric, 1000)
		stats.resourceInfo.Collect(collected)
		close(collected)

		var signatures []string
		for metric := range collected {
			written := &dto.Metric{}
			Expect(metric.Write(written)).To(Succeed())
			labels := map[string]string{}
			for _, label := range written.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["uuid"] == uuid {
				signatures = append(signatures, labels["signature"])
			}
		}
		return signatures
	}

	It("keeps the signature of a resource", func() {
		stats.AddResource(helpers.TypeUrl[*backend.Backend](), "backend-uuid", "name", "sig-1")

		Expect(signaturesOf("backend-uuid")).To(ConsistOf("sig-1"))
	})

	It("leaves out the signature of a secret, a hash of its values", func() {
		stats.AddResource(helpers.TypeUrl[*secret.Secret](), "secret-uuid", "name", "sig-1")

		Expect(signaturesOf("secret-uuid")).To(ConsistOf(""))
	})

	It("leaves out the signature of a certificate, a hash of its private key", func() {
		stats.AddResource(helpers.TypeUrl[*cert.Certificate](), "cert-uuid", "name", "sig-1")

		Expect(signaturesOf("cert-uuid")).To(ConsistOf(""))
	})
})
