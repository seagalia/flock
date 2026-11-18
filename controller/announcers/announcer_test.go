package announcers_test

import (
	"context"

	"fmt"
	"github.com/ArthurHlt/emitter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	"github.com/samber/lo"
	. "github.com/seagalia/flock/controller/announcers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/storers"
	"maps"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/types/known/anypb"
)

var _ = Describe("Announcer", func() {
	var sm storers.StoreManager
	var a *Announcer
	var instanceUuid string = "instance-uuid"
	httpRoutesTo := func(backendUuid string) *vs.VirtualServer_HttpRoutesConfig {
		return &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
			VirtualHosts: []*route.VirtualHost{{
				Domains: []string{"*"},
				Routes:  []*route.Route{{Action: &route.Route_BackendUuid{BackendUuid: backendUuid}}},
			}},
		}}
	}
	tlsRoutesTo := func(backendUuid, certUuid string) *vs.VirtualServer_TlsTcpRoutesConfig {
		return &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{
			Routes: []*vs.TlsTcpRouteConfig{{
				BackendUuid: backendUuid,
				Mode:        &vs.TlsTcpRouteConfig_Tls{Tls: &corev1.TLS{CertificatesUuid: []string{certUuid}}},
			}},
		}}
	}
	defaultDataBefore := func() {
		err := sm.Backends().Create(context.Background(), &backend.Backend{
			UUID: "backend-uuid",
			Name: "backend-uuid",
		})
		Expect(err).ToNot(HaveOccurred())

		err = sm.VirtualServers().Create(context.Background(), &vs.VirtualServer{
			UUID: "vs-uuid",
			Name: "vs-uuid",
			RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
				HttpRoutesConfig: &vs.HttpRoutesConfig{
					VirtualHosts: []*route.VirtualHost{{
						Domains: []string{"*"},
						Routes:  []*route.Route{{Action: &route.Route_BackendUuid{BackendUuid: "backend-uuid"}}},
					}},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		err = sm.VIPs().Create(context.Background(), &vip.VIP{
			UUID:               "vip-uuid",
			Name:               "vip-uuid",
			VirtualServersUuid: []string{"vs-uuid"},
		})
		Expect(err).ToNot(HaveOccurred())

		err = sm.Instances().Create(context.Background(), &instance.Instance{
			UUID:     instanceUuid,
			Name:     instanceUuid,
			VipsUuid: []string{"vip-uuid"},
		})
		Expect(err).ToNot(HaveOccurred())

		a.Init()
	}
	BeforeEach(func() {
		sm = storers.NewMemoryManager()
		a = NewAnnouncer(context.Background(), instanceUuid, sm)
		err := sm.Instances().Create(context.Background(), &instance.Instance{
			UUID:     instanceUuid,
			Name:     instanceUuid,
			VipsUuid: []string{},
		})
		Expect(err).ToNot(HaveOccurred())
	})
	AfterEach(func() {
		a.Close()
		observe.OffAll()
	})
	Context("LockReceive/UnlockReceive", func() {
		It("holds observe until unlocked", func() {
			var ops int64
			a.LockReceive()
			go func() {
				a.Observe(emitter.NewEvent("test", nil))
				atomic.AddInt64(&ops, 1)
			}()

			ConsistentlyAtomic(&ops).Should(Equal(0))
			a.Init()
			a.UnlockReceive()

			EventuallyAtomic(&ops).Should(Equal(1))
		})
	})
	When("is initialized", func() {
		BeforeEach(func() {
			a.Init()
		})
		Context("concurrent events on one subject", func() {
			BeforeEach(func() {
				defaultDataBefore()
			})

			// a delete drops the lock of its subject: an update waiting on that lock must still unlock what it locked
			It("should survive updates and deletes racing on the same virtual server", func() {
				vsSubject := &observer.Subject{TypeUrl: helpers.TypeUrl[*vs.VirtualServer](), Identifier: "vs-uuid"}
				var wg sync.WaitGroup
				for _, eventType := range []observer.EventType{observer.EventType_UPDATE_SUBJECT, observer.EventType_DELETE_SUBJECT} {
					for range 4 {
						wg.Go(func() {
							defer GinkgoRecover()
							for range 500 {
								a.Observe(emitter.NewEventOf(eventType.String(), vsSubject))
							}
						})
					}
				}
				wg.Wait()
			})

			// what a listener received must end as what the announcer holds, whatever the pace of the changes
			It("should give a listener the removal of a virtual server added and removed at once", func() {
				var mutex sync.Mutex
				held := make(map[string]bool)
				list := &AnnouncerListenerTest{}
				list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
					mutex.Lock()
					defer mutex.Unlock()
					for _, r := range res {
						if evName == ChangeEvent {
							held[r.GetUuid()] = true
							continue
						}
						delete(held, r.GetUuid())
					}
				})
				a.On(helpers.TypeUrl[*vs.VirtualServer](), list)
				event := func(eventType observer.EventType, typeUrl, uuid string) {
					a.Observe(emitter.NewEventOf(eventType.String(), &observer.Subject{TypeUrl: typeUrl, Identifier: uuid}))
				}
				vipType := helpers.TypeUrl[*vip.VIP]()
				setVipVs := func(vsUuids ...string) {
					Expect(sm.VIPs().Update(context.Background(), "vip-uuid", &vip.VIP{UUID: "vip-uuid", Name: "vip-uuid", VirtualServersUuid: vsUuids})).To(Succeed())
					event(observer.EventType_UPDATE_SUBJECT, vipType, "vip-uuid")
				}

				for n := range 300 {
					uuid := fmt.Sprintf("churn-vs-%d", n)
					Expect(sm.VirtualServers().Create(context.Background(), &vs.VirtualServer{
						UUID: uuid,
						Name: uuid,
						RouteConfig: &vs.VirtualServer_TcpRouteConfig{
							TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-uuid"},
						},
					})).To(Succeed())
					setVipVs("vs-uuid", uuid)
					setVipVs("vs-uuid")
					Expect(sm.VirtualServers().Delete(context.Background(), uuid)).To(Succeed())
					event(observer.EventType_DELETE_SUBJECT, helpers.TypeUrl[*vs.VirtualServer](), uuid)
				}

				heldNow := func() map[string]bool {
					mutex.Lock()
					defer mutex.Unlock()
					return maps.Clone(held)
				}
				Eventually(heldNow).Should(BeEmpty())
				Consistently(heldNow, "500ms").Should(BeEmpty())
			})
		})
		Context("SnapRessources", func() {
			BeforeEach(func() {
				defaultDataBefore()
			})
			When("there is no initial resource version", func() {
				It("should snap all resources", func() {
					initResVers := make(map[string]string)
					changesBackend, _ := a.SnapRessources(helpers.TypeUrl[*backend.Backend](), initResVers)
					changesVs, _ := a.SnapRessources(helpers.TypeUrl[*vs.VirtualServer](), initResVers)
					changesVips, _ := a.SnapRessources(helpers.TypeUrl[*vip.VIP](), initResVers)

					Expect(changesBackend).To(HaveLen(1))
					Expect(changesBackend[0].GetUuid()).To(Equal("backend-uuid"))

					Expect(changesVs).To(HaveLen(1))
					Expect(changesVs[0].GetUuid()).To(Equal("vs-uuid"))

					Expect(changesVips).To(HaveLen(1))
					Expect(changesVips[0].GetUuid()).To(Equal("vip-uuid"))
				})
			})
			When("there is an initial resource version", func() {
				It("should not snap any resources if signature match", func() {
					sig, err := helpers.MessageSignature(&backend.Backend{
						UUID: "backend-uuid",
						Name: "backend-uuid",
					})
					Expect(err).ToNot(HaveOccurred())

					initResVers := map[string]string{
						"backend-uuid": sig,
					}
					changesBackend, _ := a.SnapRessources(helpers.TypeUrl[*backend.Backend](), initResVers)

					Expect(changesBackend).To(BeEmpty())
				})
				It("should snap only needed resource", func() {
					sig, err := helpers.MessageSignature(&backend.Backend{
						UUID: "backend-uuid",
						Name: "backend-uuid-new-name",
					})
					Expect(err).ToNot(HaveOccurred())

					initResVers := map[string]string{
						"backend-uuid": sig,
					}
					changesBackend, _ := a.SnapRessources(helpers.TypeUrl[*backend.Backend](), initResVers)

					Expect(changesBackend).To(HaveLen(1))
					Expect(changesBackend[0].GetUuid()).To(Equal("backend-uuid"))
				})
				It("should mark as removed not found uuid", func() {
					sig, err := helpers.MessageSignature(&backend.Backend{
						UUID: "backend-uuid-not",
						Name: "backend-uuid-not",
					})
					Expect(err).ToNot(HaveOccurred())

					initResVers := map[string]string{
						"backend-uuid-not": sig,
					}
					changesBackend, removes := a.SnapRessources(helpers.TypeUrl[*backend.Backend](), initResVers)

					Expect(changesBackend).To(HaveLen(1))
					Expect(changesBackend[0].GetUuid()).To(Equal("backend-uuid"))
					Expect(removes).To(HaveLen(1))
					Expect(removes[0]).To(Equal("backend-uuid-not"))
				})
			})
		})
		Context("Off", func() {
			var list *AnnouncerListenerTest
			BeforeEach(func() {
				list = &AnnouncerListenerTest{}
				defaultDataBefore()
			})
			It("should not announce anything", func() {
				var ops int64
				var done int64
				list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
					atomic.AddInt64(&ops, 1)
				})
				a.On(helpers.TypeUrl[*backend.Backend](), list)
				observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
					a.Observe(e)
					atomic.AddInt64(&done, 1)
				}))

				err := sm.Backends().Update(context.Background(), "backend-uuid", &backend.Backend{
					UUID: "backend-uuid",
					Name: "backend-uuid-new-name",
				})
				Expect(err).ToNot(HaveOccurred())
				<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: "backend-uuid",
				})

				EventuallyAtomic(&ops).Should(Equal(1))
				a.Off(helpers.TypeUrl[*backend.Backend](), list)
				err = sm.Backends().Update(context.Background(), "backend-uuid", &backend.Backend{
					UUID: "backend-uuid",
					Name: "backend-uuid-new-name2",
				})
				Expect(err).ToNot(HaveOccurred())
				<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: "backend-uuid",
				})

				EventuallyAtomic(&ops).Should(Equal(1))
				EventuallyAtomic(&done).Should(Equal(2))
			})
		})
		Context("Close", func() {
			var list *AnnouncerListenerTest
			BeforeEach(func() {
				list = &AnnouncerListenerTest{}
				defaultDataBefore()
			})
			It("should not announce anything", func() {
				var ops int64
				var done int64
				list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
					atomic.AddInt64(&ops, 1)
				})
				a.On(helpers.TypeUrl[*backend.Backend](), list)
				observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
					a.Observe(e)
					atomic.AddInt64(&done, 1)
				}))

				err := sm.Backends().Update(context.Background(), "backend-uuid", &backend.Backend{
					UUID: "backend-uuid",
					Name: "backend-uuid-new-name",
				})
				Expect(err).ToNot(HaveOccurred())
				<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: "backend-uuid",
				})

				EventuallyAtomic(&ops).Should(Equal(1))
				a.Close()
				err = sm.Backends().Update(context.Background(), "backend-uuid", &backend.Backend{
					UUID: "backend-uuid",
					Name: "backend-uuid-new-name2",
				})
				Expect(err).ToNot(HaveOccurred())
				<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: "backend-uuid",
				})

				EventuallyAtomic(&ops).Should(Equal(1))
				EventuallyAtomic(&done).Should(Equal(2))
			})
		})
		Context("On", func() {
			var list *AnnouncerListenerTest
			BeforeEach(func() {
				defaultDataBefore()
			})
			It("should not announce resource not in instance", func() {
				list = &AnnouncerListenerTest{}
				err := sm.Backends().Create(context.Background(), &backend.Backend{
					UUID: "test-backend-1",
					Name: "test-backend-1",
				})
				Expect(err).ToNot(HaveOccurred())
				var ops int64
				var waitAnnounce int64
				list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
					defer GinkgoRecover()
					atomic.AddInt64(&waitAnnounce, 1)
				})
				a.On(helpers.TypeUrl[*backend.Backend](), list)
				observe.On(observer.EventType_CREATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
					a.Observe(e)
					atomic.AddInt64(&ops, 1)
				}))

				<-observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: "test-backend-1",
				})

				EventuallyAtomic(&ops).Should(Equal(1))
				EventuallyAtomic(&waitAnnounce).Should(Equal(0))
			})
			When("event on backend", func() {
				BeforeEach(func() {
					list = &AnnouncerListenerTest{}
				})
				It("should announce change if it change", func() {
					err := sm.Backends().Update(context.Background(), "backend-uuid", &backend.Backend{
						UUID: "backend-uuid",
						Name: "test-backend-1",
					})
					Expect(err).ToNot(HaveOccurred())

					var ops int64
					var waitAnnounce int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						Expect(evName).To(Equal(ChangeEvent))
						Expect(res[0].GetUuid()).To(Equal("backend-uuid"))
						atomic.AddInt64(&waitAnnounce, 1)
					})
					a.On(helpers.TypeUrl[*backend.Backend](), list)
					observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*backend.Backend](),
						Identifier: "backend-uuid",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounce).Should(Equal(1))
				})
				It("should announce remove if it was removed", func() {
					var ops int64
					var waitAnnounce int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						Expect(evName).To(Equal(RemoveEvent))
						Expect(res[0].GetUuid()).To(Equal("backend-uuid"))
						atomic.AddInt64(&waitAnnounce, 1)
					})
					a.On(helpers.TypeUrl[*backend.Backend](), list)
					observe.On(observer.EventType_DELETE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					<-observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*backend.Backend](),
						Identifier: "backend-uuid",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounce).Should(Equal(1))
				})
			})
			When("listeners give filters", func() {
				webTeam := svccore.ListFilters{{Key: "team", Value: "web", IsMetadata: true}}
				var webList *AnnouncerListenerTest
				setVipTeam := func(team string) {
					Expect(sm.VIPs().Update(context.Background(), "vip-uuid", &vip.VIP{
						UUID: "vip-uuid", Name: "vip-uuid", VirtualServersUuid: []string{"vs-uuid"},
						Metadata: map[string]string{"team": team},
					})).To(Succeed())
				}
				emitVipUpdate := func() {
					<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*vip.VIP](),
						Identifier: "vip-uuid",
					})
				}
				BeforeEach(func() {
					setVipTeam("web")
					a.Close()
					a = NewAnnouncer(context.Background(), instanceUuid, sm)
					a.Init()
					list = &AnnouncerListenerTest{}
					webList = &AnnouncerListenerTest{}
					observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
					}))
				})
				It("should only snap resources matching the filters of the listener", func() {
					vips, _ := a.SnapRessources(helpers.TypeUrl[*vip.VIP](), map[string]string{}, webTeam...)
					Expect(vips).To(HaveLen(1))
					dbVips, _ := a.SnapRessources(helpers.TypeUrl[*vip.VIP](), map[string]string{},
						&svccore.ListFilter{Key: "team", Value: "db", IsMetadata: true})
					Expect(dbVips).To(BeEmpty())

					backends, _ := a.SnapRessources(helpers.TypeUrl[*backend.Backend](), map[string]string{})
					Expect(backends).To(HaveLen(1), "the filters of the vip stream do not reach the backends")
				})
				It("should remove a resource from the listener it stops matching only", func() {
					var webRemoved, otherChanged int64
					webList.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						Expect(evName).To(Equal(RemoveEvent))
						Expect(res[0].GetUuid()).To(Equal("vip-uuid"))
						atomic.AddInt64(&webRemoved, 1)
					})
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						Expect(evName).To(Equal(ChangeEvent))
						atomic.AddInt64(&otherChanged, 1)
					})
					a.On(helpers.TypeUrl[*vip.VIP](), webList, webTeam...)
					a.On(helpers.TypeUrl[*vip.VIP](), list)

					setVipTeam("db")
					emitVipUpdate()

					EventuallyAtomic(&webRemoved).Should(Equal(1))
					EventuallyAtomic(&otherChanged).Should(Equal(1))
				})
				It("should send a resource again once it matches again", func() {
					var changed, removed int64
					webList.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						if evName == RemoveEvent {
							atomic.AddInt64(&removed, 1)
							return
						}
						atomic.AddInt64(&changed, 1)
					})
					setVipTeam("db")
					a.Close()
					a = NewAnnouncer(context.Background(), instanceUuid, sm)
					a.Init()
					a.On(helpers.TypeUrl[*vip.VIP](), webList, webTeam...)

					setVipTeam("other")
					emitVipUpdate()
					ConsistentlyAtomic(&removed).Should(Equal(0), "the listener never had the vip")

					setVipTeam("web")
					emitVipUpdate()
					EventuallyAtomic(&changed).Should(Equal(1))
				})
			})
			When("event on vip", func() {
				BeforeEach(func() {
					list = &AnnouncerListenerTest{}
				})
				It("should announce all changes even when virtual servers changed", func() {
					err := sm.VirtualServers().Create(context.Background(), &vs.VirtualServer{
						UUID: "vs-uuid2",
						Name: "vs-uuid2",
						RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
							HttpRoutesConfig: &vs.HttpRoutesConfig{
								VirtualHosts: []*route.VirtualHost{{
									Domains: []string{"*"},
									Routes:  []*route.Route{{Action: &route.Route_BackendUuid{BackendUuid: "backend-uuid"}}},
								}},
							},
						},
					})
					Expect(err).ToNot(HaveOccurred())
					err = sm.VIPs().Update(context.Background(), "vip-uuid", &vip.VIP{
						UUID:               "vip-uuid",
						Name:               "vip-uuid",
						VirtualServersUuid: []string{"vs-uuid2"},
					})
					Expect(err).ToNot(HaveOccurred())

					var ops int64
					var waitAnnounceChg int64
					var waitAnnounceRm int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						if evName == ChangeEvent {
							Expect(res).To(HaveLen(1))
							if res[0].GetResource().GetTypeUrl() == helpers.TypeUrl[*vip.VIP]() {
								Expect(res[0].GetUuid()).To(Equal("vip-uuid"))
								atomic.AddInt64(&waitAnnounceChg, 1)
							}
							if res[0].GetResource().GetTypeUrl() == helpers.TypeUrl[*vs.VirtualServer]() {
								Expect(res[0].GetUuid()).To(Equal("vs-uuid2"))
								atomic.AddInt64(&waitAnnounceChg, 1)
							}
							return
						}
						if evName == RemoveEvent {
							Expect(res).To(HaveLen(1))
							Expect(res[0].GetUuid()).To(Equal("vs-uuid"))
							atomic.AddInt64(&waitAnnounceRm, 1)
							return
						}
					})
					a.On(helpers.TypeUrl[*vip.VIP](), list)
					a.On(helpers.TypeUrl[*vs.VirtualServer](), list)
					observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*vip.VIP](),
						Identifier: "vip-uuid",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounceChg).Should(Equal(2))
					EventuallyAtomic(&waitAnnounceRm).Should(Equal(1))
				})
				It("should announce remove if it was removed", func() {
					var ops int64
					var waitAnnounce int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						Expect(evName).To(Equal(RemoveEvent))
						Expect(res[0].GetUuid()).To(
							Or(
								Equal("vip-uuid"),
								Equal("vs-uuid"),
								Equal("backend-uuid"),
							),
						)
						atomic.AddInt64(&waitAnnounce, 1)
					})
					a.On(helpers.TypeUrl[*vip.VIP](), list)
					a.On(helpers.TypeUrl[*vs.VirtualServer](), list)
					observe.On(observer.EventType_DELETE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					<-observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*vip.VIP](),
						Identifier: "vip-uuid",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounce).Should(Equal(2))
				})
			})
			When("event on virtual server", func() {
				BeforeEach(func() {
					list = &AnnouncerListenerTest{}
				})
				It("should announce all changes even when route config changed", func() {
					err := sm.VirtualServers().Update(context.Background(), "vs-uuid", &vs.VirtualServer{
						UUID: "vs-uuid",
						Name: "vs-uuid",
						RouteConfig: &vs.VirtualServer_TcpRouteConfig{
							TcpRouteConfig: &vs.TcpRouteConfig{
								BackendUuid: "backend-uuid",
							},
						},
					})
					Expect(err).ToNot(HaveOccurred())

					var ops int64
					var waitAnnounceChg int64
					var waitAnnounceRm int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						if evName == ChangeEvent {
							Expect(res).To(HaveLen(1))
							Expect(res[0].GetUuid()).To(Equal("vs-uuid"))
							atomic.AddInt64(&waitAnnounceChg, 1)
							return
						}
						if evName == RemoveEvent {
							atomic.AddInt64(&waitAnnounceRm, 1)
							return
						}
					})
					a.On(helpers.TypeUrl[*vs.VirtualServer](), list)
					a.On(helpers.TypeUrl[*backend.Backend](), list)
					observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*vs.VirtualServer](),
						Identifier: "vs-uuid",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounceChg).Should(Equal(1))
					// the tcp route still uses the backend of the http route
					ConsistentlyAtomic(&waitAnnounceRm).Should(Equal(0))
				})
				It("should not remove vs still used by others", func() {
					err := sm.VirtualServers().Create(context.Background(), &vs.VirtualServer{
						UUID: "vs-uuid2",
						Name: "vs-uuid2",
						RouteConfig: &vs.VirtualServer_TcpRouteConfig{
							TcpRouteConfig: &vs.TcpRouteConfig{
								BackendUuid: "backend-uuid",
							},
						},
					})
					Expect(err).ToNot(HaveOccurred())
					err = sm.VIPs().Create(context.Background(), &vip.VIP{
						UUID:               "vip1",
						Name:               "vip1",
						Ip:                 "127.0.0.1",
						VirtualServersUuid: []string{"vs-uuid"},
					})
					Expect(err).ToNot(HaveOccurred())
					err = sm.VIPs().Create(context.Background(), &vip.VIP{
						UUID:               "vip2",
						Name:               "vip2",
						Ip:                 "127.0.0.1",
						VirtualServersUuid: []string{"vs-uuid"},
					})
					Expect(err).ToNot(HaveOccurred())

					err = sm.Instances().Update(context.Background(), instanceUuid, &instance.Instance{
						UUID:     instanceUuid,
						Name:     instanceUuid,
						VipsUuid: []string{"vip1", "vip2"},
					})
					Expect(err).ToNot(HaveOccurred())

					a.Init()

					var ops int64
					var waitAnnounceChg int64

					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						if evName == ChangeEvent {
							Expect(res).To(HaveLen(1))
							Expect(res[0].GetUuid()).To(Or(Equal("vs-uuid2"), Equal("vip2")))
							atomic.AddInt64(&waitAnnounceChg, 1)
						}
					})
					a.On(helpers.TypeUrl[*vs.VirtualServer](), list)
					a.On(helpers.TypeUrl[*backend.Backend](), list)
					observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))
					observe.On(observer.EventType_DELETE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					err = sm.VIPs().Create(context.Background(), &vip.VIP{
						UUID:               "vip2",
						Name:               "vip2",
						Ip:                 "127.0.0.1",
						VirtualServersUuid: []string{"vs-uuid2"},
					})
					Expect(err).ToNot(HaveOccurred())

					<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*vip.VIP](),
						Identifier: "vip2",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounceChg).Should(Equal(1))

					changes, _ := a.SnapRessources(helpers.TypeUrl[*vs.VirtualServer](), map[string]string{})

					Expect(changes).To(HaveLen(2))
					Expect(changes).To(ContainElement(gstruct.PointTo(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
						"Uuid": Equal("vs-uuid"),
					}))))
					Expect(changes).To(ContainElement(gstruct.PointTo(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
						"Uuid": Equal("vs-uuid2"),
					}))))

				})
				It("should announce all changes even when route config changes is more complex", func() {
					err := sm.Backends().Create(context.Background(), &backend.Backend{
						UUID: "backend-uuid2",
						Name: "backend-uuid2",
					})
					Expect(err).ToNot(HaveOccurred())
					err = sm.VirtualServers().Update(context.Background(), "vs-uuid", &vs.VirtualServer{
						UUID: "vs-uuid",
						Name: "vs-uuid",
						RouteConfig: &vs.VirtualServer_TcpRouteConfig{
							TcpRouteConfig: &vs.TcpRouteConfig{
								BackendUuid: "backend-uuid2",
							},
						},
					})
					Expect(err).ToNot(HaveOccurred())

					var ops int64
					var waitAnnounceChg int64
					var waitAnnounceRm int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						if evName == ChangeEvent {
							Expect(res).To(HaveLen(1))
							if res[0].GetResource().GetTypeUrl() == helpers.TypeUrl[*backend.Backend]() {
								Expect(res[0].GetUuid()).To(Equal("backend-uuid2"))
								atomic.AddInt64(&waitAnnounceChg, 1)
							}
							if res[0].GetResource().GetTypeUrl() == helpers.TypeUrl[*vs.VirtualServer]() {
								Expect(res[0].GetUuid()).To(Equal("vs-uuid"))
								atomic.AddInt64(&waitAnnounceChg, 1)
							}
							return
						}
						if evName == RemoveEvent {
							atomic.AddInt64(&waitAnnounceRm, 1)
							Expect(res).To(HaveLen(1))
							Expect(res[0].GetUuid()).To(Equal("backend-uuid"))
						}
					})
					a.On(helpers.TypeUrl[*vs.VirtualServer](), list)
					a.On(helpers.TypeUrl[*backend.Backend](), list)
					observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*vs.VirtualServer](),
						Identifier: "vs-uuid",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounceChg).Should(Equal(2))
					EventuallyAtomic(&waitAnnounceRm).Should(Equal(1))
				})
				It("should announce remove if it was removed", func() {
					var ops int64
					var waitAnnounce int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						Expect(evName).To(Equal(RemoveEvent))
						Expect(res[0].GetUuid()).To(
							Or(
								Equal("vs-uuid"),
								Equal("backend-uuid"),
							),
						)
						atomic.AddInt64(&waitAnnounce, 1)
					})
					a.On(helpers.TypeUrl[*vs.VirtualServer](), list)
					a.On(helpers.TypeUrl[*backend.Backend](), list)
					observe.On(observer.EventType_DELETE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))
					<-observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*vs.VirtualServer](),
						Identifier: "vs-uuid",
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounce).Should(Equal(2))
				})
			})
			When("event on instance", func() {
				BeforeEach(func() {
					list = &AnnouncerListenerTest{}
				})
				It("should announce all changes even when virtual servers changed", func() {
					err := sm.VIPs().Create(context.Background(), &vip.VIP{
						UUID:               "vip-uuid2",
						Name:               "vip-uuid2",
						VirtualServersUuid: []string{"vs-uuid"},
					})
					Expect(err).ToNot(HaveOccurred())
					err = sm.Instances().Update(context.Background(), instanceUuid, &instance.Instance{
						UUID:     instanceUuid,
						Name:     instanceUuid,
						VipsUuid: []string{"vip-uuid2"},
					})
					Expect(err).ToNot(HaveOccurred())

					var ops int64
					var waitAnnounceChg int64
					var waitAnnounceRm int64
					list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
						defer GinkgoRecover()
						if evName == ChangeEvent {
							Expect(res).To(HaveLen(1))
							Expect(res[0].GetUuid()).To(Equal("vip-uuid2"))
							atomic.AddInt64(&waitAnnounceChg, 1)
							return
						}
						if evName == RemoveEvent {
							Expect(res).To(HaveLen(1))
							Expect(res[0].GetUuid()).To(Equal("vip-uuid"))
							atomic.AddInt64(&waitAnnounceRm, 1)
						}
					})
					a.On(helpers.TypeUrl[*vip.VIP](), list)
					observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
						a.Observe(e)
						atomic.AddInt64(&ops, 1)
					}))

					<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
						TypeUrl:    helpers.TypeUrl[*instance.Instance](),
						Identifier: instanceUuid,
					})

					EventuallyAtomic(&ops).Should(Equal(1))
					EventuallyAtomic(&waitAnnounceChg).Should(Equal(1))
					EventuallyAtomic(&waitAnnounceRm).Should(Equal(1))
				})
			})
		})
	})
	// regression: a tls virtual server dropped the reference to its certificates instead of adding it, so detaching
	// one of two virtual servers sharing a certificate removed it from the agent
	Context("certificate shared by two tls virtual servers", func() {
		certType := helpers.TypeUrl[*cert.Certificate]()
		snapCertUuids := func() []string {
			changes, _ := a.SnapRessources(certType, map[string]string{})
			uuids := make([]string, 0, len(changes))
			for _, res := range changes {
				uuids = append(uuids, res.GetUuid())
			}
			return uuids
		}
		setVipVirtualServers := func(vsUuids ...string) {
			err := sm.VIPs().Update(context.Background(), "vip-uuid", &vip.VIP{UUID: "vip-uuid", Name: "vip-uuid", VirtualServersUuid: vsUuids})
			Expect(err).ToNot(HaveOccurred())
			<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
				TypeUrl:    helpers.TypeUrl[*vip.VIP](),
				Identifier: "vip-uuid",
			})
		}
		BeforeEach(func() {
			Expect(sm.Backends().Create(context.Background(), &backend.Backend{UUID: "backend-uuid", Name: "backend-uuid"})).To(Succeed())
			Expect(sm.Certificates().Create(context.Background(), &cert.Certificate{UUID: "cert-uuid", Name: "cert-uuid"})).To(Succeed())
			for _, vsUuid := range []string{"vs-tls-1", "vs-tls-2"} {
				err := sm.VirtualServers().Create(context.Background(), &vs.VirtualServer{
					UUID: vsUuid,
					Name: vsUuid,
					RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{
						Routes: []*vs.TlsTcpRouteConfig{{
							BackendUuid: "backend-uuid",
							Mode:        &vs.TlsTcpRouteConfig_Tls{Tls: &corev1.TLS{CertificatesUuid: []string{"cert-uuid"}}},
						}},
					}},
				})
				Expect(err).ToNot(HaveOccurred())
			}
			err := sm.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-uuid", Name: "vip-uuid", VirtualServersUuid: []string{"vs-tls-1", "vs-tls-2"}})
			Expect(err).ToNot(HaveOccurred())
			err = sm.Instances().Update(context.Background(), instanceUuid, &instance.Instance{UUID: instanceUuid, Name: instanceUuid, VipsUuid: []string{"vip-uuid"}})
			Expect(err).ToNot(HaveOccurred())
			a.Init()
			observe.On(observer.EventType_UPDATE_SUBJECT, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
				a.Observe(e)
			}))
			Expect(snapCertUuids()).To(ConsistOf("cert-uuid"))
		})
		It("should keep the certificate while another virtual server uses it", func() {
			setVipVirtualServers("vs-tls-1")
			Eventually(func() []string {
				changes, _ := a.SnapRessources(helpers.TypeUrl[*vs.VirtualServer](), map[string]string{})
				return lo.Map(changes, func(res *svcdiscovery.Resource, _ int) string { return res.GetUuid() })
			}).Should(ConsistOf("vs-tls-1"))
			Expect(snapCertUuids()).To(ConsistOf("cert-uuid"))
		})
		It("should remove the certificate once no virtual server uses it", func() {
			setVipVirtualServers()
			Eventually(snapCertUuids).Should(BeEmpty())
		})
	})
	Context("secrets referenced by metadata", func() {
		secretType := helpers.TypeUrl[*secret.Secret]()
		var list *AnnouncerListenerTest
		var changes []*svcdiscovery.Resource
		var removes []*svcdiscovery.Resource
		var mu sync.Mutex
		changeUuids := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return lo.Map(changes, func(res *svcdiscovery.Resource, _ int) string { return res.GetUuid() })
		}
		removeUuids := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return lo.Map(removes, func(res *svcdiscovery.Resource, _ int) string { return res.GetUuid() })
		}
		emitUpdate := func(typeUrl, uuid string) {
			<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{TypeUrl: typeUrl, Identifier: uuid})
		}
		setBackendMetadata := func(uuid string, metadata map[string]string) {
			Expect(sm.Backends().Update(context.Background(), uuid, &backend.Backend{UUID: uuid, Name: uuid, Metadata: metadata})).To(Succeed())
			emitUpdate(helpers.TypeUrl[*backend.Backend](), uuid)
		}
		BeforeEach(func() {
			changes, removes = nil, nil
			list = &AnnouncerListenerTest{}
			list.SetOnAnnounce(func(evName EventName, res []*svcdiscovery.Resource) {
				mu.Lock()
				defer mu.Unlock()
				if evName == ChangeEvent {
					changes = append(changes, res...)
					return
				}
				removes = append(removes, res...)
			})
			defaultDataBefore()
			Expect(sm.Backends().Create(context.Background(), &backend.Backend{UUID: "backend-2", Name: "backend-2"})).To(Succeed())
			err := sm.VirtualServers().Update(context.Background(), "vs-uuid", &vs.VirtualServer{
				UUID: "vs-uuid",
				Name: "vs-uuid",
				RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
					VirtualHosts: []*route.VirtualHost{{
						Domains: []string{"*"},
						Routes: []*route.Route{
							{Action: &route.Route_BackendUuid{BackendUuid: "backend-uuid"}},
							{Action: &route.Route_BackendUuid{BackendUuid: "backend-2"}},
						},
					}},
				}},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(sm.Secrets().Create(context.Background(), &secret.Secret{
				UUID: "secret-uuid", Name: "secret-uuid", Data: map[string]string{"token": "s3cret"},
			})).To(Succeed())
			Expect(sm.Secrets().Create(context.Background(), &secret.Secret{UUID: "unused-uuid", Name: "unused-uuid"})).To(Succeed())
			a.On(secretType, list)
			// the listener keeps its own announcer, because a listener of a finished spec may still run when the next spec
			// replaces the shared variable
			announcer := a
			for _, eventType := range []observer.EventType{observer.EventType_UPDATE_SUBJECT, observer.EventType_DELETE_SUBJECT} {
				observe.On(eventType, emitter.ListenerFuncOf[*observer.Subject](func(e *emitter.EventOf[*observer.Subject]) {
					announcer.Observe(e)
				}))
			}
			emitUpdate(helpers.TypeUrl[*vs.VirtualServer](), "vs-uuid")
		})
		It("should send the secret with its values once a backend references it", func() {
			setBackendMetadata("backend-uuid", map[string]string{"auth": "secret://secret-uuid/token"})

			Eventually(changeUuids).Should(ConsistOf("secret-uuid"))
			mu.Lock()
			defer mu.Unlock()
			sent := &secret.Secret{}
			Expect(changes[0].ToMessage(sent)).To(Succeed())
			Expect(sent.GetData()).To(HaveKeyWithValue("token", "s3cret"))
		})
		It("should send a secret referenced by the instance, by two keys of one resource", func() {
			err := sm.Instances().Update(context.Background(), instanceUuid, &instance.Instance{
				UUID: instanceUuid, Name: instanceUuid, VipsUuid: []string{"vip-uuid"},
				Metadata: map[string]string{"a": "secret://secret-uuid/token", "b": "secret://secret-uuid/other"},
			})
			Expect(err).ToNot(HaveOccurred())
			emitUpdate(helpers.TypeUrl[*instance.Instance](), instanceUuid)

			Eventually(changeUuids).Should(ConsistOf("secret-uuid"))
		})
		It("should keep the secret while another backend references it", func() {
			setBackendMetadata("backend-uuid", map[string]string{"auth": "secret://secret-uuid/token"})
			setBackendMetadata("backend-2", map[string]string{"auth": "secret://secret-uuid/token"})
			Eventually(changeUuids).Should(ConsistOf("secret-uuid"))

			setBackendMetadata("backend-uuid", nil)
			Consistently(removeUuids).Should(BeEmpty())

			setBackendMetadata("backend-2", nil)
			Eventually(removeUuids).Should(ConsistOf("secret-uuid"))
		})
		It("should send the secret a header of the starling HTTP options points at, and remove it with the header", func() {
			setHttpOptions := func(options *starling_vsopts.HttpOptions) {
				typedOptions, err := anypb.New(options)
				Expect(err).ToNot(HaveOccurred())
				Expect(sm.VirtualServers().Update(context.Background(), "vs-uuid", &vs.VirtualServer{
					UUID: "vs-uuid",
					Name: "vs-uuid",
					RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
						TypedOptions: typedOptions,
						VirtualHosts: []*route.VirtualHost{{
							Domains: []string{"*"},
							Routes: []*route.Route{{
								Action: &route.Route_BackendUuid{BackendUuid: "backend-uuid"},
							}},
						}},
					}},
				})).To(Succeed())
				emitUpdate(helpers.TypeUrl[*vs.VirtualServer](), "vs-uuid")
			}
			fromSecret := &corev1.HeaderValue{Key: "x-token", ValueSource: &corev1.HeaderValue_ValueFromSecret{
				ValueFromSecret: &flocktype.SecretKeyRef{SecretUuid: "secret-uuid", Key: "token"},
			}}

			setHttpOptions(&starling_vsopts.HttpOptions{RequestHeadersToAdd: []*corev1.HeaderValueOption{{Header: fromSecret}}})
			Eventually(changeUuids).Should(ConsistOf("secret-uuid"))

			setHttpOptions(&starling_vsopts.HttpOptions{})
			Eventually(removeUuids).Should(ConsistOf("secret-uuid"))
		})
		It("should send a change when the secret is updated", func() {
			setBackendMetadata("backend-uuid", map[string]string{"auth": "secret://secret-uuid/token"})
			Eventually(changeUuids).Should(ConsistOf("secret-uuid"))

			Expect(sm.Secrets().Update(context.Background(), "secret-uuid", &secret.Secret{
				UUID: "secret-uuid", Name: "secret-uuid", Data: map[string]string{"token": "rotated"},
			})).To(Succeed())
			emitUpdate(secretType, "secret-uuid")

			Eventually(changeUuids).Should(ConsistOf("secret-uuid", "secret-uuid"))
		})
		DescribeTable("should send the secret referenced by a resource of the instance chain",
			func(reference func(metadata map[string]string)) {
				reference(map[string]string{"auth": "secret://secret-uuid/token"})

				Eventually(changeUuids).Should(ConsistOf("secret-uuid"))
			},
			Entry("a VIP", func(metadata map[string]string) {
				Expect(sm.VIPs().Update(context.Background(), "vip-uuid", &vip.VIP{
					UUID: "vip-uuid", Name: "vip-uuid", VirtualServersUuid: []string{"vs-uuid"}, Metadata: metadata,
				})).To(Succeed())
				emitUpdate(helpers.TypeUrl[*vip.VIP](), "vip-uuid")
			}),
			Entry("a virtual server", func(metadata map[string]string) {
				Expect(sm.VirtualServers().Update(context.Background(), "vs-uuid", &vs.VirtualServer{
					UUID: "vs-uuid", Name: "vs-uuid", Metadata: metadata, RouteConfig: httpRoutesTo("backend-uuid"),
				})).To(Succeed())
				emitUpdate(helpers.TypeUrl[*vs.VirtualServer](), "vs-uuid")
			}),
			Entry("a certificate", func(metadata map[string]string) {
				Expect(sm.Certificates().Create(context.Background(), &cert.Certificate{UUID: "cert-uuid", Name: "cert-uuid"})).To(Succeed())
				Expect(sm.VirtualServers().Update(context.Background(), "vs-uuid", &vs.VirtualServer{
					UUID: "vs-uuid", Name: "vs-uuid", RouteConfig: tlsRoutesTo("backend-uuid", "cert-uuid"),
				})).To(Succeed())
				emitUpdate(helpers.TypeUrl[*vs.VirtualServer](), "vs-uuid")
				Expect(sm.Certificates().Update(context.Background(), "cert-uuid", &cert.Certificate{
					UUID: "cert-uuid", Name: "cert-uuid", Metadata: metadata,
				})).To(Succeed())
				emitUpdate(helpers.TypeUrl[*cert.Certificate](), "cert-uuid")
			}),
		)
		It("should remove the secret when the backend referencing it is deleted", func() {
			setBackendMetadata("backend-uuid", map[string]string{"auth": "secret://secret-uuid/token"})
			Eventually(changeUuids).Should(ConsistOf("secret-uuid"))

			Expect(sm.Backends().Delete(context.Background(), "backend-uuid")).To(Succeed())
			<-observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
				TypeUrl: helpers.TypeUrl[*backend.Backend](), Identifier: "backend-uuid",
			})

			Eventually(removeUuids).Should(ConsistOf("secret-uuid"))
		})
		It("should remove the secret when the instance drops the VIP referencing it", func() {
			Expect(sm.VIPs().Update(context.Background(), "vip-uuid", &vip.VIP{
				UUID: "vip-uuid", Name: "vip-uuid", VirtualServersUuid: []string{"vs-uuid"},
				Metadata: map[string]string{"auth": "secret://secret-uuid/token"},
			})).To(Succeed())
			emitUpdate(helpers.TypeUrl[*vip.VIP](), "vip-uuid")
			Eventually(changeUuids).Should(ConsistOf("secret-uuid"))

			Expect(sm.Instances().Update(context.Background(), instanceUuid, &instance.Instance{
				UUID: instanceUuid, Name: instanceUuid, VipsUuid: []string{},
			})).To(Succeed())
			emitUpdate(helpers.TypeUrl[*instance.Instance](), instanceUuid)

			Eventually(removeUuids).Should(ConsistOf("secret-uuid"))
		})
		It("should send nothing for a secret referenced outside the instance chain", func() {
			Expect(sm.Backends().Create(context.Background(), &backend.Backend{
				UUID: "backend-other", Name: "backend-other",
				Metadata: map[string]string{"auth": "secret://secret-uuid/token"},
			})).To(Succeed())
			Expect(sm.VirtualServers().Create(context.Background(), &vs.VirtualServer{
				UUID: "vs-other", Name: "vs-other", RouteConfig: httpRoutesTo("backend-other"),
			})).To(Succeed())

			emitUpdate(helpers.TypeUrl[*backend.Backend](), "backend-other")
			emitUpdate(secretType, "secret-uuid")

			Consistently(changeUuids).Should(BeEmpty())
			changed, _ := a.SnapRessources(secretType, map[string]string{})
			Expect(changed).To(BeEmpty())
		})
	})
	Context("secret referenced before the announcer starts", func() {
		It("should include the secret in the first snapshot", func() {
			Expect(sm.Secrets().Create(context.Background(), &secret.Secret{
				UUID: "secret-uuid", Name: "secret-uuid", Data: map[string]string{"token": "s3cret"},
			})).To(Succeed())
			Expect(sm.Backends().Create(context.Background(), &backend.Backend{
				UUID: "backend-uuid", Name: "backend-uuid",
				Metadata: map[string]string{"auth": "secret://secret-uuid/token"},
			})).To(Succeed())
			Expect(sm.VirtualServers().Create(context.Background(), &vs.VirtualServer{
				UUID: "vs-uuid", Name: "vs-uuid", RouteConfig: httpRoutesTo("backend-uuid"),
			})).To(Succeed())
			Expect(sm.VIPs().Create(context.Background(), &vip.VIP{
				UUID: "vip-uuid", Name: "vip-uuid", VirtualServersUuid: []string{"vs-uuid"},
			})).To(Succeed())
			Expect(sm.Instances().Update(context.Background(), instanceUuid, &instance.Instance{
				UUID: instanceUuid, Name: instanceUuid, VipsUuid: []string{"vip-uuid"},
			})).To(Succeed())

			a.Init()

			changed, _ := a.SnapRessources(helpers.TypeUrl[*secret.Secret](), map[string]string{})
			Expect(lo.Map(changed, func(res *svcdiscovery.Resource, _ int) string { return res.GetUuid() })).
				To(ConsistOf("secret-uuid"))
		})
	})
})
