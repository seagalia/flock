package devenv

import (
	"context"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"google.golang.org/protobuf/types/known/emptypb"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeController keeps the zones, vips and instances in memory and implements what seed calls.
type fakeController struct {
	svczoneconnect.UnimplementedZoneHandler
	svcvipconnect.UnimplementedVIPHandler
	svcinstanceconnect.UnimplementedInstanceHandler

	zones     []*zone.Zone
	vips      []*vip.VIP
	instances []*instance.Instance

	vipUpdates  []*svcvip.UpdateVIPRequest
	vipAttaches []*svcinstance.AddVipToInstanceRequest
}

func (f *fakeController) ListZone(context.Context, *svczone.ListZoneRequest) (*svczone.ListZoneResponse, error) {
	return &svczone.ListZoneResponse{Zones: f.zones}, nil
}

func (f *fakeController) CreateZone(_ context.Context, req *svczone.CreateZoneRequest) (*svczone.ZoneResponse, error) {
	req.GetZone().UUID = "zone-1"
	f.zones = append(f.zones, req.GetZone())
	return &svczone.ZoneResponse{Zone: req.GetZone()}, nil
}

func (f *fakeController) ListVIP(context.Context, *svcvip.ListVIPRequest) (*svcvip.ListVIPResponse, error) {
	return &svcvip.ListVIPResponse{Vip: f.vips}, nil
}

func (f *fakeController) CreateVIP(_ context.Context, req *svcvip.CreateVIPRequest) (*svcvip.VIPResponse, error) {
	req.GetVip().UUID = "vip-1"
	f.vips = append(f.vips, req.GetVip())
	return &svcvip.VIPResponse{Vip: req.GetVip()}, nil
}

func (f *fakeController) UpdateVIP(_ context.Context, req *svcvip.UpdateVIPRequest) (*svcvip.VIPResponse, error) {
	f.vipUpdates = append(f.vipUpdates, req)
	return &svcvip.VIPResponse{Vip: req.GetVip()}, nil
}

func (f *fakeController) ListInstance(context.Context, *svcinstance.ListInstanceRequest) (*svcinstance.ListInstanceResponse, error) {
	return &svcinstance.ListInstanceResponse{Instances: f.instances}, nil
}

func (f *fakeController) CreateInstance(_ context.Context, req *svcinstance.CreateInstanceRequest) (*svcinstance.InstanceResponse, error) {
	req.GetInstance().UUID = "instance-1"
	f.instances = append(f.instances, req.GetInstance())
	return &svcinstance.InstanceResponse{Instance: req.GetInstance()}, nil
}

func (f *fakeController) AddVipToInstance(_ context.Context, req *svcinstance.AddVipToInstanceRequest) (*emptypb.Empty, error) {
	f.vipAttaches = append(f.vipAttaches, req)
	return &emptypb.Empty{}, nil
}

var _ = Describe("seed", func() {
	var (
		fake   *fakeController
		client *clients.Client
	)

	BeforeEach(func() {
		fake = &fakeController{}
		server := connect.NewServer()
		svczoneconnect.RegisterZoneHandler(server, fake)
		svcvipconnect.RegisterVIPHandler(server, fake)
		svcinstanceconnect.RegisterInstanceHandler(server, fake)
		client = clients.New(connect.NewClient(connectinprocess.New(server)))
	})

	It("should create the VIP local on the wildcard IP", func() {
		instanceUuid, err := seed(context.Background(), client)

		Expect(err).ToNot(HaveOccurred())
		Expect(instanceUuid).To(Equal("instance-1"))
		Expect(fake.vips).To(HaveLen(1))
		Expect(fake.vips[0].GetName()).To(Equal("local"))
		Expect(fake.vips[0].GetIp()).To(Equal("0.0.0.0"))
		Expect(fake.instances[0].GetVipsUuid()).To(Equal([]string{"vip-1"}))
	})

	It("should set an existing VIP local back to the wildcard IP and keep its virtual servers", func() {
		fake.zones = []*zone.Zone{{UUID: "zone-1", Name: zoneName}}
		fake.vips = []*vip.VIP{{UUID: "vip-1", Name: "local", Ip: "172.17.0.2", VirtualServersUuid: []string{"vs-1"}}}
		fake.instances = []*instance.Instance{{UUID: "instance-1", Name: instanceName, VipsUuid: []string{"vip-1"}}}

		_, err := seed(context.Background(), client)

		Expect(err).ToNot(HaveOccurred())
		Expect(fake.vipUpdates).To(HaveLen(1))
		Expect(fake.vipUpdates[0].GetUUID()).To(Equal("vip-1"))
		Expect(fake.vipUpdates[0].GetVip().GetIp()).To(Equal("0.0.0.0"))
		Expect(fake.vipUpdates[0].GetVip().GetVirtualServersUuid()).To(Equal([]string{"vs-1"}))
	})

	It("should attach the VIP to an existing instance that lacks it", func() {
		fake.zones = []*zone.Zone{{UUID: "zone-1", Name: zoneName}}
		fake.vips = []*vip.VIP{{UUID: "vip-1", Name: "local", Ip: "0.0.0.0"}}
		fake.instances = []*instance.Instance{{UUID: "instance-1", Name: instanceName}}

		_, err := seed(context.Background(), client)

		Expect(err).ToNot(HaveOccurred())
		Expect(fake.vipUpdates).To(BeEmpty())
		Expect(fake.vipAttaches).To(HaveLen(1))
		Expect(fake.vipAttaches[0].GetUUID()).To(Equal("instance-1"))
		Expect(fake.vipAttaches[0].GetVipUuid()).To(Equal("vip-1"))
	})
})
