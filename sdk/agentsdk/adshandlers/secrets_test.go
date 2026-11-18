package adshandlers_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	. "github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("Secret references", func() {
	var (
		mockHandler *testhelpers.MockMessageHandler[*backend.Backend]
		backends    *AdsHandler[*backend.Backend]
		secrets     *AdsHandler[*secret.Secret]
		ctx         = context.Background()
		backendType = helpers.TypeUrl[*backend.Backend]()
		secretType  = helpers.TypeUrl[*secret.Secret]()
	)

	withMetadata := func(metadata map[string]string) *backend.Backend {
		aBackend := makeBackend("backend-1")
		aBackend.Metadata = metadata
		return aBackend
	}
	addSecret := func(data map[string]string) {
		aSecret := &secret.Secret{UUID: "s1", Name: "s1", Data: data}
		Expect(secrets.AddResource(ctx, makeResource(aSecret, "s1"))).To(Succeed())
	}
	addBackend := func(metadata map[string]string) {
		Expect(backends.AddResource(ctx, makeResource(withMetadata(metadata), "backend-1"))).To(Succeed())
	}
	pointing := map[string]string{"a": "secret://s1/k"}

	BeforeEach(func() {
		mockCtrl := gomock.NewController(GinkgoT())
		mockAggr := testhelpers.NewMockAggregater(mockCtrl)
		mockHandler = testhelpers.NewMockMessageHandler[*backend.Backend](mockCtrl)
		mockHandler.EXPECT().Name().Return("backend").AnyTimes()
		backends = NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
		secrets = NewAdsHandler[*secret.Secret](rt, mockAggr, 1, NewSecretRefresher(rt))
	})

	It("gives the handlers and the store the resolved metadata", func() {
		addSecret(map[string]string{"k": "v"})
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withMetadata(map[string]string{"a": "v"}))).
			Return(nil)

		addBackend(pointing)

		stored, err := store.Backends().Read(context.Background(), "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetMetadata()).To(Equal(map[string]string{"a": "v"}))
	})

	It("resolves a resource received before its secret once the secret arrives", func() {
		gomock.InOrder(
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withMetadata(pointing))).Return(nil),
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withMetadata(map[string]string{"a": "v"}))).
				Return(nil),
		)
		addBackend(pointing)

		addSecret(map[string]string{"k": "v"})
	})

	It("empties a value whose key a secret update dropped", func() {
		addSecret(map[string]string{"k": "v"})
		gomock.InOrder(
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withMetadata(map[string]string{"a": "v"}))).
				Return(nil),
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withMetadata(map[string]string{"a": ""}))).
				Return(nil),
		)
		addBackend(pointing)

		addSecret(map[string]string{"other": "v"})
	})

	It("applies the reruns of many linked resources in parallel inside one window", func() {
		var finished atomic.Int32
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, aBackend *backend.Backend) error {
				if aBackend.GetMetadata()["a"] != "v" {
					return nil
				}
				select {
				case <-time.After(1200 * time.Millisecond):
					finished.Add(1)
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}).Times(6)
		for _, uuid := range []string{"backend-1", "backend-2", "backend-3"} {
			aBackend := makeBackend(uuid)
			aBackend.Metadata = pointing
			Expect(backends.AddResource(ctx, makeResource(aBackend, uuid))).To(Succeed())
		}

		// one by one the three reruns would need 3.6s
		window, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		aSecret := &secret.Secret{UUID: "s1", Name: "s1", Data: map[string]string{"k": "v"}}
		Expect(secrets.AddResource(window, makeResource(aSecret, "s1"))).To(Succeed())

		Expect(finished.Load()).To(BeEquivalentTo(3))
	})

	It("runs nothing when a secret is removed", func() {
		addSecret(map[string]string{"k": "v"})
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(1)
		addBackend(pointing)

		Expect(secrets.RemoveResource(ctx, "s1")).To(Succeed())
	})

	It("no longer runs a resource that stopped pointing at the secret", func() {
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		addBackend(pointing)
		addBackend(map[string]string{"a": "plain"})

		addSecret(map[string]string{"k": "v"})
	})

	It("keeps the version the controller sent through a re-run", func() {
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		addBackend(pointing)

		addSecret(map[string]string{"k": "v"})

		Expect(states.Version(backendType, "backend-1")).To(Equal("sig-backend-1"))
	})

	It("marks the resource errored, not the secret, when a re-run fails", func() {
		gomock.InOrder(
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil),
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(fmt.Errorf("failed")),
		)
		addBackend(pointing)

		addSecret(map[string]string{"k": "v"})

		_, backendState, _ := states.Get(backendType, "backend-1")
		Expect(backendState).To(Equal(agentstatus.StatusResponse_ERRORED))
		_, secretState, _ := states.Get(secretType, "s1")
		Expect(secretState).To(Equal(agentstatus.StatusResponse_OK))
	})

	It("keeps the references in the prev store, which the status answers show", func() {
		addSecret(map[string]string{"k": "v"})
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(3)
		addBackend(pointing)
		prevOfNew, err := prevStore.Backends().Read(context.Background(), "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(prevOfNew.GetMetadata()).To(Equal(pointing))

		addBackend(map[string]string{"a": "plain"})
		prevOfDropped, err := prevStore.Backends().Read(context.Background(), "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(prevOfDropped.GetMetadata()).To(Equal(pointing))

		addBackend(map[string]string{"a": "plain"})
		prevOfPlain, err := prevStore.Backends().Read(context.Background(), "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(prevOfPlain.GetMetadata()).To(Equal(map[string]string{"a": "plain"}))
	})

	It("drops the links of a removed resource", func() {
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil)
		addBackend(pointing)
		mockHandler.EXPECT().OnMessageRemove(gomock.Any(), gomock.Any()).Return(nil)

		Expect(backends.RemoveResource(ctx, "backend-1")).To(Succeed())

		Expect(secretLinks.LinkedTo("s1")).To(BeEmpty())
	})

	It("resolves a resource whose secret arrives while it is being added", func() {
		mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		for i := range 5000 {
			secretUuid := fmt.Sprintf("race-secret-%d", i)
			backendUuid := fmt.Sprintf("race-backend-%d", i)
			aBackend := makeBackend(backendUuid)
			aBackend.Metadata = map[string]string{"a": "secret://" + secretUuid + "/k"}
			aSecret := &secret.Secret{UUID: secretUuid, Name: secretUuid, Data: map[string]string{"k": "v"}}

			done := make(chan error, 1)
			go func() { done <- secrets.AddResource(ctx, makeResource(aSecret, secretUuid)) }()
			Expect(backends.AddResource(ctx, makeResource(aBackend, backendUuid))).To(Succeed())
			Expect(<-done).To(Succeed())

			stored, err := store.Backends().Read(context.Background(), backendUuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetMetadata()).To(HaveKeyWithValue("a", "v"), "attempt %d", i)
		}
	})

	Context("in typed fields", func() {
		fromSecret := &flocktype.SecretKeyRef{SecretUuid: "s1", Key: "k"}
		withEds := func(header *core.HeaderValue, password *core.URL_UserInfo) *backend.Backend {
			aBackend := makeBackend("backend-1")
			aBackend.Endpoint = &backend.Backend_Eds{Eds: &backend.EDS{
				Url:     &core.URL{Scheme: "http", Host: "vigie", UserInfo: password},
				Headers: []*core.HeaderValue{header},
			}}
			return aBackend
		}
		headerFrom := func(source *core.HeaderValue) *backend.Backend { return withEds(source, nil) }
		secretHeader := &core.HeaderValue{Key: "x-token", ValueSource: &core.HeaderValue_ValueFromSecret{ValueFromSecret: fromSecret}}
		plainHeader := func(value string) *core.HeaderValue {
			return &core.HeaderValue{Key: "x-token", ValueSource: &core.HeaderValue_Value{Value: value}}
		}
		secretPassword := &core.URL_UserInfo{User: "u", PasswordSource: &core.URL_UserInfo_PasswordFromSecret{PasswordFromSecret: fromSecret}}
		plainPassword := func(value string) *core.URL_UserInfo {
			return &core.URL_UserInfo{User: "u", PasswordSource: &core.URL_UserInfo_Password{Password: value}}
		}
		addTyped := func(aBackend *backend.Backend) {
			Expect(backends.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(Succeed())
		}

		It("gives the handlers the header value of the secret", func() {
			addSecret(map[string]string{"k": "v"})
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(headerFrom(plainHeader("v")))).Return(nil)

			addTyped(headerFrom(secretHeader))
		})

		It("sends an empty header value until the secret arrives, then runs the backend again", func() {
			gomock.InOrder(
				mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(headerFrom(plainHeader("")))).Return(nil),
				mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(headerFrom(plainHeader("v")))).Return(nil),
			)
			addTyped(headerFrom(secretHeader))

			addSecret(map[string]string{"k": "v"})
		})

		It("runs the backend again with the new value when the secret is updated", func() {
			addSecret(map[string]string{"k": "v"})
			gomock.InOrder(
				mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(headerFrom(plainHeader("v")))).Return(nil),
				mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(headerFrom(plainHeader("rotated")))).Return(nil),
			)
			addTyped(headerFrom(secretHeader))

			addSecret(map[string]string{"k": "rotated"})
		})

		It("keeps the typed reference in the prev store", func() {
			addSecret(map[string]string{"k": "v"})
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(2)
			addTyped(headerFrom(secretHeader))
			addTyped(headerFrom(secretHeader))

			prev, err := prevStore.Backends().Read(context.Background(), "backend-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(prev, headerFrom(secretHeader))).To(BeTrue())
		})

		It("gives the handlers the password of the secret, empty until it arrives", func() {
			gomock.InOrder(
				mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withEds(plainHeader("p"), plainPassword("")))).Return(nil),
				mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withEds(plainHeader("p"), plainPassword("v")))).Return(nil),
				mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(withEds(plainHeader("p"), plainPassword("rotated")))).Return(nil),
			)
			addTyped(withEds(plainHeader("p"), secretPassword))

			addSecret(map[string]string{"k": "v"})
			addSecret(map[string]string{"k": "rotated"})
		})

		It("keeps the password reference in the prev store", func() {
			addSecret(map[string]string{"k": "v"})
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil).Times(2)
			addTyped(withEds(plainHeader("p"), secretPassword))
			addTyped(withEds(plainHeader("p"), secretPassword))

			prev, err := prevStore.Backends().Read(context.Background(), "backend-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(prev, withEds(plainHeader("p"), secretPassword))).To(BeTrue())
		})
	})
})
