package runtime_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func makeResource(message proto.Message, uuid string) *svcdiscovery.Resource {
	packed, err := anypb.New(message)
	Expect(err).ToNot(HaveOccurred())
	return &svcdiscovery.Resource{Uuid: uuid, Signature: "sig-" + uuid, Resource: packed}
}

// app is what an agent app builds over its runtime for certificates: flock dev runs the starling agent and the
// provisioner side by side, both subscribed to the certificates and their secrets.
type app struct {
	rt           *runtime.Runtime
	handler      *testhelpers.MockMessageHandler[*cert.Certificate]
	certificates *adshandlers.AdsHandler[*cert.Certificate]
	secrets      *adshandlers.AdsHandler[*secret.Secret]
}

func newApp(mockCtrl *gomock.Controller, name string) *app {
	rt := runtime.New()
	aggr := testhelpers.NewMockAggregater(mockCtrl)
	handler := testhelpers.NewMockMessageHandler[*cert.Certificate](mockCtrl)
	handler.EXPECT().Name().Return(name).AnyTimes()
	return &app{
		rt:           rt,
		handler:      handler,
		certificates: adshandlers.NewAdsHandler[*cert.Certificate](rt, aggr, 1, handler),
		secrets:      adshandlers.NewAdsHandler[*secret.Secret](rt, aggr, 1, adshandlers.NewSecretRefresher(rt)),
	}
}

var _ = Describe("Two apps in one process", func() {
	var (
		ctx         = context.Background()
		agent       *app
		provisioner *app
		pointing    = &cert.Certificate{UUID: "cert-1", Name: "cert-1", Metadata: map[string]string{"a": "secret://s1/k"}}
		aSecret     = &secret.Secret{UUID: "s1", Name: "s1", Data: map[string]string{"k": "v"}}
	)

	BeforeEach(func() {
		mockCtrl := gomock.NewController(GinkgoT())
		agent = newApp(mockCtrl, "agent")
		provisioner = newApp(mockCtrl, "provisioner")
	})

	It("keeps a resource received by one out of the stores and states of the other", func() {
		provisioner.handler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil)

		Expect(provisioner.certificates.AddResource(ctx, makeResource(pointing, "cert-1"))).To(Succeed())

		Expect(provisioner.rt.Store.Certificates().Exists(context.Background(), "cert-1")).To(BeTrue())
		Expect(agent.rt.Store.Certificates().Exists(context.Background(), "cert-1")).To(BeFalse())
		Expect(agent.rt.PrevStore.Certificates().Exists(context.Background(), "cert-1")).To(BeFalse())
		Expect(agent.rt.States.Version(helpers.TypeUrl[*cert.Certificate](), "cert-1")).To(BeEmpty())
		Expect(agent.rt.SecretLinks.LinkedTo("s1")).To(BeEmpty())
	})

	It("runs again the resources of both apps when their secret changes", func() {
		agent.handler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		provisioner.handler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		Expect(agent.certificates.AddResource(ctx, makeResource(pointing, "cert-1"))).To(Succeed())
		Expect(provisioner.certificates.AddResource(ctx, makeResource(pointing, "cert-1"))).To(Succeed())

		Expect(agent.secrets.AddResource(ctx, makeResource(aSecret, "s1"))).To(Succeed())
		Expect(provisioner.secrets.AddResource(ctx, makeResource(aSecret, "s1"))).To(Succeed())
	})
})

var _ = Describe("Handlers", func() {
	It("gives every handler in registration order, or the ones of a type", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		aggr := testhelpers.NewMockAggregater(mockCtrl)
		rt := runtime.New()
		firstCertificates := adshandlers.NewAdsHandler[*cert.Certificate](rt, aggr, 1)
		secrets := adshandlers.NewAdsHandler[*secret.Secret](rt, aggr, 1, adshandlers.NewSecretRefresher(rt))
		secondCertificates := adshandlers.NewAdsHandler[*cert.Certificate](rt, aggr, 1)

		Expect(rt.Handlers("")).To(HaveExactElements(
			BeIdenticalTo(firstCertificates), BeIdenticalTo(secrets), BeIdenticalTo(secondCertificates),
		))
		Expect(rt.Handlers(helpers.TypeUrl[*cert.Certificate]())).To(HaveExactElements(
			BeIdenticalTo(firstCertificates), BeIdenticalTo(secondCertificates),
		))
		Expect(rt.Handlers("type.googleapis.com/unknown")).To(BeEmpty())
	})
})
