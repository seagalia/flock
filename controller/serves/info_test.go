package serves_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("Info", func() {
	It("should list resource types managed by controller", func() {
		server := newTestServer(instantiaters.NewNull())

		resp, err := server.ListResourceTypes(userCtx(adminUser), &emptypb.Empty{})

		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetResourceTypes()).To(ConsistOf(
			helpers.TypeUrl[*backend.Backend](),
			helpers.TypeUrl[*instance.Instance](),
			helpers.TypeUrl[*vip.VIP](),
			helpers.TypeUrl[*vs.VirtualServer](),
			helpers.TypeUrl[*cert.Certificate](),
		))
	})

	Context("GetControllerFeatures", func() {
		It("should report the ipam as enabled when one is configured", func() {
			server := newTestServerWithIpam(instantiaters.NewNull(), dbIpam)

			resp, err := server.GetControllerFeatures(userCtx("bob"), &emptypb.Empty{})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetIpamEnabled()).To(BeTrue())
		})
		It("should report the ipam as disabled without one", func() {
			server := newTestServer(instantiaters.NewNull())

			resp, err := server.GetControllerFeatures(userCtx("bob"), &emptypb.Empty{})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetIpamEnabled()).To(BeFalse())
		})
	})
})
