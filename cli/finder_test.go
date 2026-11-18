package cli

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

const backendUuid = "0f8fad5b-d9cb-469f-a165-70867728950e"

var _ = Describe("Finder", func() {
	var (
		conn            *fakeconnect.Conn
		client          *clients.Client
		listedBackends  []*backend.Backend
		receivedFilters []*svccore.ListFilter
	)

	BeforeEach(func() {
		listedBackends = nil
		receivedFilters = nil
		conn = fakeconnect.New().
			Handle(svcbackendconnect.BackendListBackendProcedure, func(req proto.Message) (proto.Message, error) {
				receivedFilters = req.(*svcbackend.ListBackendRequest).GetFilters()
				return &svcbackend.ListBackendResponse{Backends: listedBackends}, nil
			}).
			Handle(svcbackendconnect.BackendReadBackendProcedure, func(req proto.Message) (proto.Message, error) {
				uuid := req.(*svccore.UuidRequest).GetUUID()
				return &svcbackend.BackendResponse{Backend: &backend.Backend{UUID: uuid, Name: "read-by-uuid"}}, nil
			})
		client = clients.New(conn.Client())
	})

	Describe("FindTypedByName", func() {
		It("lists with an equal filter on name", func() {
			listedBackends = []*backend.Backend{{Name: "backend"}}

			elems, err := FindTypedByName(context.Background(), client, helpers.TypeUrl[*backend.Backend](), "backend")

			Expect(err).NotTo(HaveOccurred())
			Expect(elems).To(HaveLen(1))
			Expect(helpers.GetName(elems[0])).To(Equal("backend"))
			Expect(receivedFilters).To(HaveLen(1))
			Expect(receivedFilters[0].GetKey()).To(Equal("name"))
			Expect(receivedFilters[0].GetValue()).To(Equal("backend"))
			Expect(receivedFilters[0].GetOperator()).To(Equal(svccore.ListFilter_EQUAL))
		})

		It("uses the given operator", func() {
			_, err := FindTypedByName(context.Background(), client, helpers.TypeUrl[*backend.Backend](), "back", svccore.ListFilter_LIKE)

			Expect(err).NotTo(HaveOccurred())
			Expect(receivedFilters[0].GetOperator()).To(Equal(svccore.ListFilter_LIKE))
		})

		It("lists everything without filter when name is empty", func() {
			listedBackends = []*backend.Backend{{Name: "a"}, {Name: "b"}}

			elems, err := FindTypedByName(context.Background(), client, helpers.TypeUrl[*backend.Backend](), "")

			Expect(err).NotTo(HaveOccurred())
			Expect(receivedFilters).To(BeEmpty())
			Expect(elems).To(HaveLen(2))
		})

		It("fails on an unknown type", func() {
			_, err := FindTypedByName(context.Background(), client, "type.googleapis.com/unknown", "x")

			Expect(err).To(MatchError(ContainSubstring("not found")))
		})
	})

	Describe("FindFirstByName", func() {
		It("fails when nothing matches", func() {
			_, err := FindFirstByName[*backend.Backend](context.Background(), client, "missing")

			Expect(err).To(MatchError(ContainSubstring("missing not found")))
		})
	})

	Describe("FindBackend", func() {
		It("lists by name when the identifier is not a uuid", func() {
			listedBackends = []*backend.Backend{{Name: "backend"}}

			found, err := FindBackend(context.Background(), client, &NameUuid{identifier: "backend"})

			Expect(err).NotTo(HaveOccurred())
			Expect(found.GetName()).To(Equal("backend"))
			Expect(conn.Calls()).To(Equal([]string{svcbackendconnect.BackendListBackendProcedure}))
		})

		It("reads by uuid when the identifier is a uuid", func() {
			found, err := FindBackend(context.Background(), client, &NameUuid{identifier: backendUuid})

			Expect(err).NotTo(HaveOccurred())
			Expect(found.GetUUID()).To(Equal(backendUuid))
			Expect(found.GetName()).To(Equal("read-by-uuid"))
			Expect(conn.Calls()).To(Equal([]string{svcbackendconnect.BackendReadBackendProcedure}))
		})
	})

	Describe("FindZone", func() {
		It("reads the zone given by uuid", func() {
			var requested string
			conn.Handle(svczoneconnect.ZoneReadZoneProcedure, func(req proto.Message) (proto.Message, error) {
				requested = req.(*svccore.UuidRequest).GetUUID()
				return &svczone.ZoneResponse{Zone: &zone.Zone{UUID: backendUuid, Name: "production"}}, nil
			})

			found, err := FindZone(context.Background(), client, &NameUuid{identifier: backendUuid})

			Expect(err).NotTo(HaveOccurred())
			Expect(requested).To(Equal(backendUuid))
			Expect(found.GetName()).To(Equal("production"))
		})

		It("finds the zone given by name", func() {
			conn.Handle(svczoneconnect.ZoneListZoneProcedure, func(req proto.Message) (proto.Message, error) {
				Expect(req.(*svczone.ListZoneRequest).GetFilters()[0].GetValue()).To(Equal("production"))
				return &svczone.ListZoneResponse{Zones: []*zone.Zone{{UUID: backendUuid, Name: "production"}}}, nil
			})

			found, err := FindZone(context.Background(), client, &NameUuid{identifier: "production"})

			Expect(err).NotTo(HaveOccurred())
			Expect(found.GetUUID()).To(Equal(backendUuid))
			Expect(conn.Calls()).To(Equal([]string{svczoneconnect.ZoneListZoneProcedure}))
		})
	})

})
