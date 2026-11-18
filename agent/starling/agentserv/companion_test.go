package agentserv_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"go.uber.org/mock/gomock"

	"github.com/seagalia/flock/agent/starling/agentlist"
	. "github.com/seagalia/flock/agent/starling/agentserv"
	"github.com/seagalia/flock/agent/starling/nm"
	"github.com/seagalia/flock/agent/starling/testhelpers"
)

func loopback() *net.Interface {
	ifaces, err := net.Interfaces()
	Expect(err).ToNot(HaveOccurred())
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return &iface
		}
	}
	Fail("no loopback interface found")
	return nil
}

func getIfacesInfo(handler http.HandlerFunc, query string) (int, []IfaceInfo) {
	GinkgoHelper()
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/companion/v1/ifaces"+query, nil))
	if recorder.Code != http.StatusOK {
		return recorder.Code, nil
	}
	Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))
	infos := make([]IfaceInfo, 0)
	Expect(json.Unmarshal(recorder.Body.Bytes(), &infos)).To(Succeed())
	return recorder.Code, infos
}

var _ = Describe("CompanionServer", func() {
	var (
		mockCtrl *gomock.Controller
		mockNm   *testhelpers.MockNetworkManager
		lo       *net.Interface
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockNm = testhelpers.NewMockNetworkManager(mockCtrl)
		lo = loopback()
	})

	Context("ServeInterfacesInfo", func() {
		It("should give an empty list without advertisement interfaces", func() {
			server := NewCompanionServer(nil, mockNm, nil)
			defer server.Stop()
			code, infos := getIfacesInfo(server.ServeInterfacesInfo, "")
			Expect(code).To(Equal(http.StatusOK))
			Expect(infos).To(BeEmpty())
		})

		It("should give addresses of advertisement interfaces and skip unknown ones", func() {
			server := NewCompanionServer(nil, mockNm, []string{lo.Name, "this-iface-does-not-exist"})
			defer server.Stop()
			code, infos := getIfacesInfo(server.ServeInterfacesInfo, "")
			Expect(code).To(Equal(http.StatusOK))
			Expect(infos).To(HaveLen(1))
			Expect(infos[0].Addrs).To(ContainElement(ContainSubstring("127.0.0.1")))
		})

		It("should only give interfaces routing to the requested destination", func() {
			server := NewCompanionServer(nil, mockNm, []string{lo.Name})
			defer server.Stop()
			mockNm.EXPECT().RouteGetAll(net.ParseIP("10.0.0.1")).Return([]*nm.Route{{Interface: lo}}, nil)
			code, infos := getIfacesInfo(server.ServeInterfacesInfo, "?for_dest=10.0.0.1")
			Expect(code).To(Equal(http.StatusOK))
			Expect(infos).To(HaveLen(1))

			mockNm.EXPECT().RouteGetAll(net.ParseIP("10.0.0.2")).Return([]*nm.Route{}, nil)
			_, infos = getIfacesInfo(server.ServeInterfacesInfo, "?for_dest=10.0.0.2")
			Expect(infos).To(BeEmpty())

			otherIface := &net.Interface{Name: "eth9", HardwareAddr: net.HardwareAddr{0, 1, 2, 3, 4, 5}}
			mockNm.EXPECT().RouteGetAll(net.ParseIP("10.0.0.3")).Return([]*nm.Route{{Interface: otherIface}}, nil)
			_, infos = getIfacesInfo(server.ServeInterfacesInfo, "?for_dest=10.0.0.3")
			Expect(infos).To(BeEmpty(), "routes through non advertisement interfaces must be ignored")
		})

		It("should give internal error when routes can't be retrieved", func() {
			server := NewCompanionServer(nil, mockNm, []string{lo.Name})
			defer server.Stop()
			mockNm.EXPECT().RouteGetAll(gomock.Any()).Return(nil, fmt.Errorf("netlink error"))
			code, _ := getIfacesInfo(server.ServeInterfacesInfo, "?for_dest=10.0.0.1")
			Expect(code).To(Equal(http.StatusInternalServerError))
		})
	})

	Context("ServeEvents", func() {
		It("should stream announced vips calculated by the placer", func() {
			placer := agentlist.NewVipPlacer(context.Background(), "node-1", nil, nil, rt.Store.VIPs())
			server := NewCompanionServer(placer, mockNm, nil)
			defer server.Stop()

			Expect(server.OnPlacerAddedVip(context.Background(), &vip.VIP{})).To(Succeed())
			Expect(server.OnPlacerRemoveVip(context.Background(), &vip.VIP{})).To(Succeed())
			Expect(server.OnAfterCalculate(context.Background(), []*vip.VIP{
				{Ip: "10.0.0.1"}, {Ip: "10.0.0.2", PrefixLength: 31},
			})).To(Succeed())

			httpServer := httptest.NewServer(http.HandlerFunc(server.ServeEvents))
			defer httpServer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"?stream="+AnnouncedVipsEvent, nil)
			Expect(err).ToNot(HaveOccurred())
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.Header.Get("Content-Type")).To(Equal("text/event-stream"))

			scanner := bufio.NewScanner(resp.Body)
			nextData := func() string {
				for scanner.Scan() {
					if after, ok := strings.CutPrefix(scanner.Text(), "data:"); ok {
						return strings.TrimSpace(after)
					}
				}
				return ""
			}
			// last event is replayed to new subscribers
			Expect(nextData()).To(Equal(`["10.0.0.1/32","10.0.0.2/31"]`))

			// then new calculations are streamed, published until received as subscription is asynchronous
			stopPublish := make(chan struct{})
			defer close(stopPublish)
			go func() {
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				for {
					_ = server.OnAfterCalculate(context.Background(), []*vip.VIP{{Ip: "10.0.0.3"}})
					select {
					case <-stopPublish:
						return
					case <-ticker.C:
					}
				}
			}()
			Expect(nextData()).To(Equal(`["10.0.0.3/32"]`))
		})
	})
})
