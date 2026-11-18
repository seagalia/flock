package storers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	. "github.com/seagalia/flock/sdk/storers"
)

func makeVip(uuid, name string, vsUuids ...string) *vip.VIP {
	return &vip.VIP{UUID: uuid, Name: name, Ip: "10.0.0.1", VirtualServersUuid: vsUuids}
}

func names(vips []*vip.VIP) []string {
	result := make([]string, 0, len(vips))
	for _, aVip := range vips {
		result = append(result, aVip.GetName())
	}
	return result
}

func itBehavesLikeAStorer(newVipStore func() Storer[*vip.VIP], newBackendStore func() Storer[*backend.Backend]) {
	var store Storer[*vip.VIP]

	BeforeEach(func() {
		store = newVipStore()
	})

	It("should keep stored messages apart from what callers change", func() {
		created := makeVip("vip-1", "first", "vs-1")
		Expect(store.Create(context.Background(), created)).To(Succeed())
		created.Name = "changed after create"

		read, err := store.Read(context.Background(), "vip-1")
		Expect(err).ToNot(HaveOccurred())
		read.VirtualServersUuid = append(read.VirtualServersUuid, "vs-2")
		listed, err := store.List(context.Background(), nil)
		Expect(err).ToNot(HaveOccurred())
		listed[0].Name = "changed after list"

		again, err := store.Read(context.Background(), "vip-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(again.GetName()).To(Equal("first"))
		Expect(again.GetVirtualServersUuid()).To(Equal([]string{"vs-1"}))
	})

	It("should read, update and delete created messages", func() {
		Expect(store.Create(context.Background(), makeVip("vip-1", "first"))).To(Succeed())
		read, err := store.Read(context.Background(), "vip-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(read.GetName()).To(Equal("first"))
		Expect(store.Exists(context.Background(), "vip-1")).To(BeTrue())

		Expect(store.Update(context.Background(), "vip-1", makeVip("vip-1", "updated"))).To(Succeed())
		read, err = store.Read(context.Background(), "vip-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(read.GetName()).To(Equal("updated"))

		Expect(store.Delete(context.Background(), "vip-1")).To(Succeed())
		Expect(store.Exists(context.Background(), "vip-1")).To(BeFalse())
	})

	It("should give a not found error for unknown messages", func() {
		_, err := store.Read(context.Background(), "unknown")
		Expect(errors.IsErrNotFound(err)).To(BeTrue())
	})

	It("should list messages filtered by name", func() {
		Expect(store.Create(context.Background(), makeVip("vip-1", "front-web"))).To(Succeed())
		Expect(store.Create(context.Background(), makeVip("vip-2", "front-api"))).To(Succeed())
		Expect(store.Create(context.Background(), makeVip("vip-3", "back"))).To(Succeed())

		all, err := store.List(context.Background(), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(names(all)).To(ConsistOf("front-web", "front-api", "back"))

		filter := func(operator svccore.ListFilter_Operator, value string) []string {
			found, err := store.List(context.Background(), nil, &svccore.ListFilter{Key: FilterNameKey, Operator: operator, Value: value})
			Expect(err).ToNot(HaveOccurred())
			return names(found)
		}
		Expect(filter(svccore.ListFilter_EQUAL, "back")).To(ConsistOf("back"))
		Expect(filter(svccore.ListFilter_NOT_EQUAL, "back")).To(ConsistOf("front-web", "front-api"))
		Expect(filter(svccore.ListFilter_LIKE, "front")).To(ConsistOf("front-web", "front-api"))
		Expect(filter(svccore.ListFilter_NOT_LIKE, "front")).To(ConsistOf("back"))

		ignored, err := store.List(context.Background(), nil, &svccore.ListFilter{Key: "other", Value: "back"})
		Expect(err).ToNot(HaveOccurred())
		Expect(ignored).To(HaveLen(3))
	})

	It("should list messages matching any of several name and uuid filters", func() {
		Expect(store.Create(context.Background(), makeVip("vip-1", "front-web"))).To(Succeed())
		Expect(store.Create(context.Background(), makeVip("vip-2", "front-api"))).To(Succeed())
		Expect(store.Create(context.Background(), makeVip("vip-3", "back"))).To(Succeed())

		byUuids, err := store.List(context.Background(), nil,
			&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "vip-1"},
			&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "vip-3"},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(names(byUuids)).To(ConsistOf("front-web", "back"))

		byNameOrUuid, err := store.List(context.Background(), nil,
			&svccore.ListFilter{Key: FilterNameKey, Operator: svccore.ListFilter_EQUAL, Value: "front-api"},
			&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_LIKE, Value: "-3"},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(names(byNameOrUuid)).To(ConsistOf("front-api", "back"))

		metadataName, err := store.List(context.Background(), nil,
			&svccore.ListFilter{IsMetadata: true, Key: FilterNameKey, Operator: svccore.ListFilter_EQUAL, Value: "back"},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(metadataName).To(HaveLen(3))
	})

	It("should list pages of messages with limit and offset", func() {
		Expect(store.Create(context.Background(), makeVip("vip-3", "third"))).To(Succeed())
		Expect(store.Create(context.Background(), makeVip("vip-1", "first"))).To(Succeed())
		Expect(store.Create(context.Background(), makeVip("vip-2", "second"))).To(Succeed())

		firstPage, err := store.List(context.Background(), &ListOptions{Limit: 2})
		Expect(err).ToNot(HaveOccurred())
		Expect(names(firstPage)).To(Equal([]string{"first", "second"}))

		secondPage, err := store.List(context.Background(), &ListOptions{Limit: 2, Offset: 2})
		Expect(err).ToNot(HaveOccurred())
		Expect(names(secondPage)).To(Equal([]string{"third"}))

		// the cli lists until it gets an empty page
		pastLastPage, err := store.List(context.Background(), &ListOptions{Limit: 2, Offset: 4})
		Expect(err).ToNot(HaveOccurred())
		Expect(pastLastPage).To(BeEmpty())
	})

	It("should find foreign resources of messages", func() {
		vsTypeUrl := helpers.TypeUrl[*vs.VirtualServer]()
		Expect(store.Create(context.Background(), makeVip("vip-1", "first", "vs-1", "vs-2"))).To(Succeed())
		Expect(store.Create(context.Background(), makeVip("vip-2", "second", "vs-2"))).To(Succeed())

		foreign, err := store.FindForeignResources(context.Background(), vsTypeUrl)
		Expect(err).ToNot(HaveOccurred())
		Expect(foreign).To(Equal(map[string][]string{"vip-1": {"vs-1", "vs-2"}, "vip-2": {"vs-2"}}))

		bySrc, err := store.FindForeignBySrc(context.Background(), "vip-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(bySrc).To(Equal(map[string][]string{vsTypeUrl: {"vs-1", "vs-2"}}))
		bySrc, err = store.FindForeignBySrc(context.Background(), "unknown")
		Expect(err).ToNot(HaveOccurred())
		Expect(bySrc).To(BeEmpty())

		sources, err := store.FindSrcWithForeignResource(context.Background(), vsTypeUrl, "vs-2")
		Expect(err).ToNot(HaveOccurred())
		Expect(sources).To(ConsistOf("vip-1", "vip-2"))
	})

	It("should find no foreign resources for messages without foreign resources", func() {
		backendStore := newBackendStore()
		Expect(backendStore.Create(context.Background(), &backend.Backend{UUID: "backend-1"})).To(Succeed())
		foreign, err := backendStore.FindForeignResources(context.Background(), helpers.TypeUrl[*vs.VirtualServer]())
		Expect(err).ToNot(HaveOccurred())
		Expect(foreign).To(BeEmpty())
		bySrc, err := backendStore.FindForeignBySrc(context.Background(), "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(bySrc).To(BeEmpty())
	})

	It("should be found by name", func() {
		Expect(store.Create(context.Background(), makeVip("vip-1", "first"))).To(Succeed())
		found, err := FindByName[*vip.VIP](context.Background(), "first", store, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(found.GetUUID()).To(Equal("vip-1"))

		_, err = FindByName[*vip.VIP](context.Background(), "unknown", store, nil)
		Expect(errors.IsErrNotFound(err)).To(BeTrue())
	})
}

var _ = Describe("Memory storer", func() {
	itBehavesLikeAStorer(
		func() Storer[*vip.VIP] { return NewMemory[*vip.VIP]() },
		func() Storer[*backend.Backend] { return NewMemory[*backend.Backend]() },
	)
})

var _ = Describe("StoreBy", func() {
	It("should give the store of a message type from a store manager", func() {
		manager := NewMemoryManager()
		Expect(StoreBy[*vip.VIP](manager)).To(BeIdenticalTo(manager.VIPs()))
		Expect(StoreBy[*backend.Backend](manager)).To(BeIdenticalTo(manager.Backends()))
		Expect(StoreBy[*vs.VirtualServer](manager)).To(BeIdenticalTo(manager.VirtualServers()))
		Expect(StoreBy[*zone.Zone](manager)).To(BeIdenticalTo(manager.Zones()))
		Expect(manager.Instances()).ToNot(BeNil())
		Expect(manager.Certificates()).ToNot(BeNil())
		Expect(StoreBy[*secret.Secret](manager)).To(BeIdenticalTo(manager.Secrets()))
	})

	It("should fail for a type without store", func() {
		_, err := StoreBy[*svccore.ListFilter](NewMemoryManager())
		Expect(err).To(MatchError(ContainSubstring("no store found")))
	})
})
