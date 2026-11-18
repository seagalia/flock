package eds_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/moby/moby/api/types/container"
	dclient "github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/docker/eds"
	edsv1 "github.com/seagalia/vigie/sdk/vigie/eds/v1"
)

// listResult reads the list of a compose project where whoami-1 runs two replicas on the default network and
// whoami-2 one on the lb network, beside the flock container which didn't opt in.
func listResult() dclient.ContainerListResult {
	GinkgoHelper()
	raw, err := os.ReadFile("testdata/list.json")
	Expect(err).NotTo(HaveOccurred())
	result := dclient.ContainerListResult{}
	Expect(json.Unmarshal(raw, &result)).To(Succeed())
	return result
}

func addresses(endpoints []*edsv1.Endpoint) []string {
	ips := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		ips = append(ips, endpoint.GetIp())
	}
	return ips
}

var _ = Describe("Server", func() {
	var server *eds.Server

	BeforeEach(func() {
		server = eds.NewServer()
	})

	getEndpoints := func(serviceName string) ([]*edsv1.Endpoint, error) {
		response, err := server.GetEndpoints(context.Background(), &edsv1.GetEndpointsRequest{ServiceName: serviceName})
		return response.GetEndpoints(), err
	}
	ipsOf := func(serviceName string) []string {
		GinkgoHelper()
		endpoints, err := getEndpoints(serviceName)
		Expect(err).NotTo(HaveOccurred())
		return addresses(endpoints)
	}
	isServed := func(serviceName string) bool {
		_, err := getEndpoints(serviceName)
		return connect.CodeOf(err) != connect.CodeNotFound
	}

	It("gives one endpoint per replica of each service", func() {
		Expect(server.Replace(listResult().Items)).To(Succeed())

		Expect(ipsOf("flock-dev-privileged-whoami-1")).To(Equal([]string{"172.18.0.2", "172.18.0.3"}))
		Expect(ipsOf("flock-dev-privileged-whoami-2")).To(Equal([]string{"172.30.90.22"}))
		endpoints, err := getEndpoints("flock-dev-privileged-whoami-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(endpoints[0].GetPort()).To(Equal(uint32(80)))
		Expect(isServed("flock-dev-privileged-flock")).To(BeFalse())
	})

	It("leaves out the containers docker doesn't see healthy", func() {
		result := listResult()
		result.Items[0].Health = &container.HealthSummary{Status: container.Unhealthy}
		result.Items[1].Health = &container.HealthSummary{Status: container.Starting}
		result.Items[2].Health = &container.HealthSummary{Status: container.Healthy}

		Expect(server.Replace(result.Items)).To(Succeed())
		Expect(isServed("flock-dev-privileged-whoami-1")).To(BeFalse())
		Expect(isServed("flock-dev-privileged-whoami-2")).To(BeTrue())
	})

	It("keeps the previous list when a container is in error", func() {
		Expect(server.Replace(withoutService(listResult().Items, "whoami-2"))).To(Succeed())
		result := listResult()
		result.Items[2].Labels["io.seagalia.flock.docker_network"] = "backoffice"

		Expect(server.Replace(result.Items)).To(MatchError(ContainSubstring("backoffice")))
		Expect(isServed("flock-dev-privileged-whoami-1")).To(BeTrue())
		Expect(isServed("flock-dev-privileged-whoami-2")).To(BeFalse())
	})

	It("answers only the last list it was given", func() {
		Expect(server.Replace(listResult().Items)).To(Succeed())
		Expect(server.Replace(withoutService(listResult().Items, "whoami-2"))).To(Succeed())

		Expect(isServed("flock-dev-privileged-whoami-1")).To(BeTrue())
		Expect(isServed("flock-dev-privileged-whoami-2")).To(BeFalse())
	})

	It("ends with a whole list when writers race", func() {
		older := withoutService(listResult().Items, "whoami-2")
		newer := listResult().Items
		var writers sync.WaitGroup
		for range 50 {
			writers.Go(func() { _ = server.Replace(older) })
			writers.Go(func() { _ = server.Replace(newer) })
			writers.Go(func() { _ = isServed("flock-dev-privileged-whoami-1") })
		}
		writers.Wait()
		Expect(server.Replace(newer)).To(Succeed())

		Expect(ipsOf("flock-dev-privileged-whoami-2")).To(Equal([]string{"172.30.90.22"}))
	})
})

func withoutService(containers []container.Summary, service string) []container.Summary {
	return slices.DeleteFunc(slices.Clone(containers), func(summary container.Summary) bool {
		return summary.Labels["com.docker.compose.service"] == service
	})
}
