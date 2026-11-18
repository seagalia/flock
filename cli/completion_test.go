package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/posener/complete"
	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Completion", func() {
	var conn *fakeconnect.Conn

	BeforeEach(func() {
		conn = fakeconnect.New().
			Handle(svcvipconnect.VIPListVIPProcedure, reply(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{Name: "public"}, {Name: "public-6"}}})).
			Handle(svcbackendconnect.BackendListBackendProcedure, reply(&svcbackend.ListBackendResponse{Backends: []*backend.Backend{{Name: "backend"}}}))
		previous := completionConnect
		completionConnect = func() (*connect.Client, error) { return conn.Client(), nil }
		DeferCleanup(func() { completionConnect = previous })
	})

	It("completes resource names matching what is typed", func() {
		names := predictResource(helpers.TypeUrl[*vip.VIP]()).Predict(complete.Args{Last: "pub"})

		Expect(names).To(Equal([]string{"public", "public-6"}))
		filter := lastRequest[*svcvip.ListVIPRequest](conn, svcvipconnect.VIPListVIPProcedure).GetFilters()[0]
		Expect(filter.GetValue()).To(Equal("pub"))
		Expect(filter.GetOperator()).To(Equal(svccore.ListFilter_LIKE))
	})

	It("completes names of the type given with -r", func() {
		args := complete.Args{All: []string{"add-permission", "-r", "backend", "ba"}, Last: "ba"}

		Expect(predictByResourceType(args)).To(Equal([]string{"backend"}))
	})

	It("completes nothing before the resource type is known", func() {
		args := complete.Args{All: []string{"add-permission", "ba"}, Last: "ba"}

		Expect(predictByResourceType(args)).To(BeEmpty())
		Expect(conn.Calls()).To(BeEmpty())
	})

	It("shows the connection error as the only proposal", func() {
		completionConnect = func() (*connect.Client, error) { return nil, errors.New("please login first") }

		names := predictResource(helpers.TypeUrl[*vip.VIP]()).Predict(complete.Args{Last: "pub"})

		Expect(names).To(Equal([]string{"Error: please login first"}))
	})

	It("proposes nothing when the controller is too slow to answer", func() {
		previous := completionTimeout
		completionTimeout = 200 * time.Millisecond
		DeferCleanup(func() { completionTimeout = previous })
		stalled := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		stalled.EnableHTTP2 = true
		stalled.StartTLS()
		DeferCleanup(stalled.Close)
		GinkgoT().Setenv("FLOCK_TOKEN", "tok")
		GinkgoT().Setenv("FLOCK_SKIP_SSL_VALIDATION", "true")
		GinkgoT().Setenv("FLOCK_HOST", stalled.Listener.Addr().String())
		GinkgoT().Setenv("FLOCK_CLI_CONFIG", filepath.Join(GinkgoT().TempDir(), "config.json"))
		completionConnect = CompletionClient

		start := time.Now()
		names := predictResource(helpers.TypeUrl[*vip.VIP]()).Predict(complete.Args{Last: "pub"})

		Expect(names).To(BeEmpty())
		Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
	})

	It("completes the pools of the zone given before the pool", func() {
		conn.Handle(svczoneconnect.ZoneListZoneProcedure, reply(&svczone.ListZoneResponse{Zones: []*zone.Zone{{Name: "production", Pools: []*zone.IpPool{{Name: "ns-a"}, {Name: "ns-b"}, {Name: "other"}}}}}))
		args := complete.Args{All: []string{"delete-pool-from-zone", "production", "ns"}, Last: "ns"}

		Expect(predictZonePool(args)).To(Equal([]string{"ns-a", "ns-b"}))
	})

	It("completes no pool before the zone is given", func() {
		args := complete.Args{All: []string{"delete-pool-from-zone", "pro"}, Last: "pro"}

		Expect(predictZonePool(args)).To(BeEmpty())
		Expect(conn.Calls()).To(BeEmpty())
	})
})
