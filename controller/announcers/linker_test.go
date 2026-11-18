package announcers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	. "github.com/seagalia/flock/controller/announcers"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/storers"
	"sync/atomic"
)

var _ = Describe("Link", func() {
	var sm storers.StoreManager
	var m *Link
	BeforeEach(func() {
		sm = storers.NewMemoryManager()
		m = NewLink(context.Background(), sm)
		err := sm.Instances().Create(context.Background(), &instance.Instance{
			UUID:     "instance-uuid",
			Name:     "instance-uuid",
			VipsUuid: []string{},
		})
		Expect(err).ToNot(HaveOccurred())
		err = sm.Instances().Create(context.Background(), &instance.Instance{
			UUID:     "instance-uuid2",
			Name:     "instance-uuid2",
			VipsUuid: []string{},
		})
		Expect(err).ToNot(HaveOccurred())
	})
	AfterEach(func() {
		observe.OffAll()
	})
	Context("Connect", func() {
		It("should connect to announcer", func() {
			err := m.Connect("instance-uuid", "type-url", &AnnouncerListenerTest{}, nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			err = m.Connect("instance-uuid2", "type-url", &AnnouncerListenerTest{}, nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())

			conns := m.ListConnections()

			Expect(conns).To(HaveLen(2))
			Expect(conns).To(HaveKey("instance-uuid"))
			Expect(conns).To(HaveKey("instance-uuid2"))
			Expect(conns).To(HaveKeyWithValue("instance-uuid", 1))
			Expect(conns).To(HaveKeyWithValue("instance-uuid2", 1))
		})
		When("init function is given", func() {
			It("should connect to announcer and run init", func() {
				var ops int64
				err := m.Connect(
					"instance-uuid", "type-url", &AnnouncerListenerTest{},
					nil, nil, func(changes []*svcdiscovery.Resource, removes []string) {
						atomic.AddInt64(&ops, 1)
					},
				)
				Expect(err).ToNot(HaveOccurred())

				conns := m.ListConnections()

				Expect(conns).To(HaveLen(1))
				Expect(conns).To(HaveKey("instance-uuid"))
				Expect(conns).To(HaveKeyWithValue("instance-uuid", 1))
				EventuallyAtomic(&ops).Should(Equal(1))
			})
		})
		When("connections give different filters", func() {
			It("should snap the resources matching the filters of each connection", func() {
				Expect(sm.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-web", Name: "vip-web", Metadata: map[string]string{"team": "web"}})).To(Succeed())
				Expect(sm.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-db", Name: "vip-db", Metadata: map[string]string{"team": "db"}})).To(Succeed())
				Expect(sm.Instances().Update(context.Background(), "instance-uuid", &instance.Instance{
					UUID: "instance-uuid", Name: "instance-uuid", VipsUuid: []string{"vip-web", "vip-db"},
				})).To(Succeed())

				snapped := make(chan []string, 2)
				connect := func(team string) {
					err := m.Connect("instance-uuid", helpers.TypeUrl[*vip.VIP](), &AnnouncerListenerTest{}, nil,
						svccore.ListFilters{{Key: "team", Value: team, IsMetadata: true}},
						func(changes []*svcdiscovery.Resource, removes []string) {
							snapped <- lo.Map(changes, func(res *svcdiscovery.Resource, _ int) string { return res.GetUuid() })
						},
					)
					Expect(err).ToNot(HaveOccurred())
				}
				connect("web")
				Eventually(snapped).Should(Receive(Equal([]string{"vip-web"})))
				connect("db")
				Eventually(snapped).Should(Receive(Equal([]string{"vip-db"})))
			})
		})
		When("announcer already exists", func() {
			It("should connect to current announcer", func() {
				err := m.Connect("instance-uuid", "type-url", &AnnouncerListenerTest{}, nil, nil, nil)
				Expect(err).ToNot(HaveOccurred())
				err = m.Connect("instance-uuid", "type-url", &AnnouncerListenerTest{}, nil, nil, nil)
				Expect(err).ToNot(HaveOccurred())

				conns := m.ListConnections()

				Expect(conns).To(HaveLen(1))
				Expect(conns).To(HaveKey("instance-uuid"))
				Expect(conns).To(HaveKeyWithValue("instance-uuid", 2))
			})
		})
	})
	Context("Disconnect", func() {
		var list1, list2 *AnnouncerListenerTest
		BeforeEach(func() {
			list1 = &AnnouncerListenerTest{}
			list2 = &AnnouncerListenerTest{}
			err := m.Connect("instance-uuid", "type-url", list1, nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			err = m.Connect("instance-uuid", "type-url", list2, nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			conns := m.ListConnections()
			Expect(conns).To(HaveLen(1))
			Expect(conns).To(HaveKeyWithValue("instance-uuid", 2))
		})
		It("should disconnect from announcer", func() {
			m.Disconnect("instance-uuid", "type-url", list1)

			conns := m.ListConnections()
			Expect(conns).To(HaveLen(1))
			Expect(conns).To(HaveKey("instance-uuid"))
			Expect(conns).To(HaveKeyWithValue("instance-uuid", 1))
		})
		When("all listeners have disconnected", func() {
			It("should remove announcer", func() {
				m.Disconnect("instance-uuid", "type-url", list1)
				m.Disconnect("instance-uuid", "type-url", list2)

				conns := m.ListConnections()
				Expect(conns).To(BeEmpty())
			})
		})
	})
	Context("DisconnectAll", func() {
		var list1, list2 *AnnouncerListenerTest
		BeforeEach(func() {
			list1 = &AnnouncerListenerTest{}
			list2 = &AnnouncerListenerTest{}
			err := m.Connect("instance-uuid", "type-url", list1, nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			err = m.Connect("instance-uuid", "type-url", list2, nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			err = m.Connect("instance-uuid2", "type-url", list2, nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			conns := m.ListConnections()
			Expect(conns).To(HaveLen(2))
			Expect(conns).To(HaveKeyWithValue("instance-uuid", 2))
			Expect(conns).To(HaveKeyWithValue("instance-uuid2", 1))
		})

		It("should disconnect all listeners for instance uuid", func() {
			m.DisconnectAll("instance-uuid")

			conns := m.ListConnections()
			Expect(conns).To(HaveLen(1))
			Expect(conns).To(HaveKey("instance-uuid2"))
			Expect(conns).To(HaveKeyWithValue("instance-uuid2", 1))
		})
	})

})
