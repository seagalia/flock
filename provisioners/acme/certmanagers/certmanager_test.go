package certmanagers_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	legocert "github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/provisioners/acme/certmanagers"
	"github.com/seagalia/flock/provisioners/acme/certmanagers/mocks"
	"github.com/seagalia/flock/provisioners/acme/params"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	"github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("CertManager", func() {
	var (
		ctrl         *gomock.Controller
		obtainer     *mocks.MockObtainer
		certClient   *mocks.MockCertificateClient
		secretClient *mocks.MockSecretClient
		manager      *certmanagers.CertManager
		states       *stores.StateStore
		ctx          context.Context
		cancel       context.CancelFunc
		// runDone is closed once Run returned
		runDone  chan struct{}
		identity *fakeIdentity
		// the controller gives the uuid before the manager runs unless a spec holds it back
		givesUuidAtStart bool
		// the certificates the manager wrote back to the controller
		updates chan *svccert.UpdateCertificateRequest
	)

	newManager := func(leaseClient svcleaseconnect.LeaseClient) *certmanagers.CertManager {
		m := certmanagers.NewCertManager(
			obtainer, certClient, secretClient, leases.NewClient(leaseClient, instanceUuid), identity, states, "",
		)
		m.SetRetry(2, time.Millisecond)
		m.SetTickRenew(20 * time.Millisecond)
		m.SetTickCleanUp(time.Hour)
		m.SetLeaseTTL(300 * time.Millisecond)
		return m
	}

	BeforeEach(func() {
		ctrl = newMockController()
		obtainer = mocks.NewMockObtainer(ctrl)
		certClient = mocks.NewMockCertificateClient(ctrl)
		secretClient = mocks.NewMockSecretClient(ctrl)
		// a removal looks for the challenge secrets of the certificate, the specs of challenges.go give some
		secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Cond(isChallengeSecretList)).
			Return(&svcsecret.ListSecretResponse{}, nil).AnyTimes()
		identity = newFakeIdentity()
		givesUuidAtStart = true
		states = stores.NewStateStore()
		manager = newManager(grantingLease(ctrl))
		updates = make(chan *svccert.UpdateCertificateRequest, 10)

		ctx, cancel = context.WithCancel(context.Background())
	})

	// the specs set the ticks in BeforeEach, the manager reads them when it starts
	JustBeforeEach(func() {
		if givesUuidAtStart {
			identity.give(agentUuid)
		}
		runDone = make(chan struct{})
		go func() {
			defer close(runDone)
			manager.Run(ctx)
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(runDone).Should(BeClosed())
		})
		// a certificate the spec adds while the renewal of the start still runs would be ordered twice
		if givesUuidAtStart {
			Eventually(manager.FirstRenewDone()).Should(BeClosed())
		}
	})

	expectCertificateUpdates := func() {
		certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
				updates <- req
				return &svccert.CertificateResponse{Certificate: req.GetCertificate()}, nil
			}).AnyTimes()
	}

	// expectControllerCopy has the controller answer crt as the copy of the certificate it holds, read at each call
	expectControllerCopy := func(crt func() *cert.Certificate) {
		certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svccore.UuidRequest) (*svccert.CertificateResponse, error) {
				controllerCopy := proto.Clone(crt()).(*cert.Certificate)
				Expect(req.GetUUID()).To(Equal(controllerCopy.GetUUID()))
				return &svccert.CertificateResponse{Certificate: controllerCopy}, nil
			}).AnyTimes()
	}

	stateError := func(crtUuid string) func() error {
		return func() error {
			_, _, err := states.Get(helpers.TypeUrl[*cert.Certificate](), crtUuid)
			return err
		}
	}

	// managedCert is a certificate this provisioner already obtained, as the ads handler gives it: secret refs
	// resolved to their values
	managedCert := func(domains []string, notBefore, notAfter time.Time) *cert.Certificate {
		certPEM, keyPEM := leafPEMFor(domains, notBefore, notAfter)
		registrationJSON, accountKeyPEM := resolvedAccount()
		return &cert.Certificate{
			UUID:        uuid.NewString(),
			Name:        "shop",
			DomainNames: domains,
			CertChain:   certPEM,
			PrivateKey:  keyPEM,
			Metadata: map[string]string{
				"flock.provisioner.acme":     "true",
				"flock.acme.email":           "ops@example.com",
				"flock.acme.registration":    registrationJSON,
				"flock.acme.key_pem":         accountKeyPEM,
				"flock.acme.secret.ref":      "secret-1",
				"flock.acme.secret.attached": "true",
				helpers.MetadataProvisionBy:  agentUuid,
			},
		}
	}

	// expectSecretUpdate has the controller hold secret-1 as the secret of the certificate crtUuid
	expectSecretUpdate := func(crtUuid string) chan *svcsecret.UpdateSecretRequest {
		secretUpdates := make(chan *svcsecret.UpdateSecretRequest, 10)
		secretClient.EXPECT().ReadSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).
			Return(&svcsecret.SecretResponse{Secret: &secret.Secret{
				UUID:     "secret-1",
				Metadata: map[string]string{"flock.acme.cert.ref": crtUuid},
			}}, nil).
			AnyTimes()
		secretClient.EXPECT().UpdateSecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svcsecret.UpdateSecretRequest) (*svcsecret.SecretResponse, error) {
				secretUpdates <- req
				return &svcsecret.SecretResponse{Secret: req.GetSecret()}, nil
			}).AnyTimes()
		return secretUpdates
	}

	Context("a certificate never obtained", func() {
		var crt *cert.Certificate

		BeforeEach(func() {
			crt = &cert.Certificate{
				UUID:        uuid.NewString(),
				Name:        "shop",
				DomainNames: []string{"shop.example.com"},
				Metadata: map[string]string{
					"flock.provisioner.acme":         "true",
					"flock.acme.email":               "ops@example.com",
					"flock.acme.ovh.application_key": "ak",
				},
			}
			expectControllerCopy(func() *cert.Certificate { return crt })
			// a first order not written is due again at each renew tick, only the order queued may run here
			manager.SetTickRenew(time.Hour)
		})

		It("orders it, keeps the account in a locked secret and writes the chain back pointing at the secret", func() {
			certPEM, keyPEM := freshLeafPEM("shop.example.com")
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *params.CertRequest, order legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					Expect(order.Domains).To(Equal([]string{"shop.example.com"}))
					Expect(order.Bundle).To(BeTrue())
					Expect(req.GetEmail()).To(Equal("ops@example.com"))
					// what the lego factory does on a first order
					req.Registration, req.KeyPem = account()
					return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
				})
			var created *svcsecret.CreateSecretRequest
			secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
					created = req
					return &svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil
				})
			expectCertificateUpdates()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			var sent *svccert.UpdateCertificateRequest
			Eventually(updates).Should(Receive(&sent))
			Expect(created.GetSecret().GetData()).To(HaveKey("flock.acme.registration"))
			Expect(created.GetSecret().GetData()).To(HaveKey("flock.acme.key_pem"))
			Expect(created.GetSecret().GetMetadata()).To(HaveKeyWithValue(helpers.MetadataLockKey, "true"))
			Expect(created.GetSecret().GetMetadata()).To(HaveKeyWithValue(helpers.MetadataProvisionBy, agentUuid))
			Expect(created.GetSecret().GetMetadata()).To(HaveKeyWithValue("flock.acme.cert.ref", crt.GetUUID()))

			Expect(sent.GetUUID()).To(Equal(crt.GetUUID()))
			Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
			Expect(sent.GetCertificate().GetPrivateKey()).To(Equal(keyPEM))
			Expect(sent.GetCertificate().GetMetadata()).To(And(
				HaveKeyWithValue("flock.acme.registration", "secret://secret-1/flock.acme.registration"),
				HaveKeyWithValue("flock.acme.key_pem", "secret://secret-1/flock.acme.key_pem"),
				HaveKeyWithValue("flock.acme.secret.ref", "secret-1"),
				HaveKeyWithValue(helpers.MetadataProvisionBy, agentUuid),
				// the filter of the provisioner and the dns provider stay, the next renewal needs them
				HaveKeyWithValue("flock.provisioner.acme", "true"),
				HaveKeyWithValue("flock.acme.ovh.application_key", "ak"),
			))
		})

		Context("before the controller gave the uuid of the provisioner", func() {
			BeforeEach(func() {
				givesUuidAtStart = false
			})

			It("writes nothing until it has the uuid, then writes the certificate provisioned by it", func() {
				certPEM, keyPEM := freshLeafPEM("shop.example.com")
				obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
						req.Registration, req.KeyPem = account()
						return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
					})
				secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
					Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Consistently(updates, 300*time.Millisecond).ShouldNot(Receive())

				identity.give("agent-from-controller")

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue(helpers.MetadataProvisionBy, "agent-from-controller"))
			})
		})

		It("marks it applied in the state store once written back", func() {
			certPEM, keyPEM := freshLeafPEM("shop.example.com")
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					req.Registration, req.KeyPem = account()
					return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
				})
			secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
				Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
			expectCertificateUpdates()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(updates).Should(Receive())
			Eventually(func() agentstatus.StatusResponse_State {
				_, state, _ := states.Get(helpers.TypeUrl[*cert.Certificate](), crt.GetUUID())
				return state
			}).Should(Equal(agentstatus.StatusResponse_OK))
		})

		It("counts one success and exposes the expiry of the certificate obtained", func() {
			notAfter := time.Now().Add(90 * 24 * time.Hour)
			certPEM, keyPEM := leafPEM("shop.example.com", time.Now().Add(-time.Hour), notAfter)
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					req.Registration, req.KeyPem = account()
					return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
				})
			secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
				Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
			expectCertificateUpdates()
			successes := certmanagers.ObtainCount("success")

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(updates).Should(Receive())
			Eventually(func() float64 { return certmanagers.ObtainCount("success") }).Should(Equal(successes + 1))
			Eventually(func() float64 {
				seconds, _ := certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
				return seconds
			}).Should(BeNumerically("==", notAfter.Unix()))
		})

		It("counts one failure once the retries are spent, not one per attempt", func() {
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("ca down")).Times(2)
			expectCertificateUpdates()
			failures := certmanagers.ObtainCount("failure")

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(func() float64 { return certmanagers.ObtainCount("failure") }).Should(Equal(failures + 1))
			Consistently(func() float64 { return certmanagers.ObtainCount("failure") }, 100*time.Millisecond).
				Should(Equal(failures + 1))
		})

		It("records the failure in the state store once the retries are spent, and writes only the back-off", func() {
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("ca down")).Times(2)
			expectCertificateUpdates()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(stateError(crt.GetUUID())).Should(MatchError(And(
				ContainSubstring("failed to obtain certificate"),
				ContainSubstring("ca down"),
			)))
			var sent *svccert.UpdateCertificateRequest
			Eventually(updates).Should(Receive(&sent))
			Expect(sent.GetCertificate().GetCertChain()).To(BeEmpty())
			Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.failures", "1"))
			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		It("waits on stop for an obtain in flight, which still writes the certificate back", func() {
			certPEM, keyPEM := freshLeafPEM("shop.example.com")
			obtaining := make(chan struct{})
			release := make(chan struct{})
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					close(obtaining)
					<-release
					req.Registration, req.KeyPem = account()
					return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
				})
			secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
				DoAndReturn(func(callCtx context.Context, _ *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
					if callCtx.Err() != nil {
						return nil, callCtx.Err()
					}
					return &svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil
				})
			certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
				DoAndReturn(func(callCtx context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
					if callCtx.Err() != nil {
						return nil, callCtx.Err()
					}
					updates <- req
					return &svccert.CertificateResponse{Certificate: req.GetCertificate()}, nil
				})
			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
			Eventually(obtaining).Should(BeClosed())

			cancel()
			Consistently(runDone, 100*time.Millisecond).ShouldNot(BeClosed())
			close(release)

			Eventually(runDone).Should(BeClosed())
			var sent *svccert.UpdateCertificateRequest
			Expect(updates).To(Receive(&sent))
			Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
		})

		It("keeps the renew tick off it while an order for it is queued", func() {
			manager.SetRetry(1, time.Millisecond)
			// each failed order writes its back-off, which the controller copy read by the next one doesn't hold
			expectCertificateUpdates()
			obtaining := make(chan struct{}, 10)
			release := make(chan struct{})
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, *params.CertRequest, legocert.ObtainRequest, challenge.Provider) (*legocert.Resource, error) {
					obtaining <- struct{}{}
					<-release
					return nil, errors.New("ca down")
				}).Times(2)

			// the second copy, an ADS update during the first order, is queued behind it
			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(obtaining).Should(Receive())
			Expect(manager.DueUuids()).NotTo(ContainElement(crt.GetUUID()))
			release <- struct{}{}
			Eventually(obtaining).Should(Receive())
			Expect(manager.DueUuids()).NotTo(ContainElement(crt.GetUUID()))
			release <- struct{}{}
			Eventually(manager.DueUuids).Should(ContainElement(crt.GetUUID()))
		})

		It("refuses a certificate without domains without ordering, backing off until its domains change", func() {
			crt.DomainNames = nil
			expectCertificateUpdates()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(stateError(crt.GetUUID())).Should(MatchError(ContainSubstring("no domains")))
			Eventually(updates).Should(Receive(HaveField("Certificate.Metadata", HaveKeyWithValue("flock.acme.failures", "1"))))
		})
	})

	Context("a certificate this provisioner already obtained", func() {
		It("doesn't order it again while its domains stay the same", func() {
			crt := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		Context("with no renew tick racing the queued order", func() {
			// a tick ordering the new domains first would leave the queued order to order them a second time
			BeforeEach(func() {
				manager.SetTickRenew(time.Hour)
			})

			It("orders it again when its domains change, pointing the metadata at the secret instead of the values it received", func() {
				crt := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
				resolvedRegistration := crt.GetMetadata()["flock.acme.registration"]
				resolvedAccountKey := crt.GetMetadata()["flock.acme.key_pem"]
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				var changed *cert.Certificate
				// the controller has the new domains with the chain of the old ones, not due yet
				expectControllerCopy(func() *cert.Certificate {
					controllerCopy := proto.Clone(changed).(*cert.Certificate)
					controllerCopy.CertChain = crt.GetCertChain()
					return controllerCopy
				})

				certPEM, keyPEM := freshLeafPEM("shop.example.com")
				obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil)
				secretUpdates := expectSecretUpdate(crt.GetUUID())
				expectCertificateUpdates()

				changed = managedCert([]string{"shop.example.com", "www.example.com"}, time.Now(), time.Now().Add(time.Hour))
				changed.UUID = crt.GetUUID()
				changed.Metadata = crt.GetMetadata()
				// the controller sends the new domains with the chain of the old ones
				changed.CertChain = crt.GetCertChain()
				changed.PrivateKey = crt.GetPrivateKey()
				Expect(manager.OnMessageAdded(ctx, changed)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetMetadata()).To(And(
					HaveKeyWithValue("flock.acme.registration", "secret://secret-1/flock.acme.registration"),
					HaveKeyWithValue("flock.acme.key_pem", "secret://secret-1/flock.acme.key_pem"),
				))
				Expect(sent.GetCertificate().GetMetadata()).NotTo(ContainElement(resolvedRegistration))
				Expect(sent.GetCertificate().GetMetadata()).NotTo(ContainElement(resolvedAccountKey))
				var secretSent *svcsecret.UpdateSecretRequest
				Expect(secretUpdates).To(Receive(&secretSent))
				Expect(secretSent.GetUUID()).To(Equal("secret-1"))
			})
		})

		It("renews it once a third of its lifetime is left", func() {
			// 90 days of validity, 20 left
			crt := managedCert([]string{"shop.example.com"}, time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
			// the controller holds the same due chain
			expectControllerCopy(func() *cert.Certificate { return crt })
			certPEM, keyPEM := freshLeafPEM("shop.example.com")
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil)
			expectSecretUpdate(crt.GetUUID())
			expectCertificateUpdates()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			var sent *svccert.UpdateCertificateRequest
			Eventually(updates).Should(Receive(&sent))
			Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
			// renewed, the new chain has its whole lifetime left
			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		It("exposes the expiry of the certificate it holds", func() {
			notAfter := time.Now().Add(89 * 24 * time.Hour)
			crt := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), notAfter)

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			seconds, exposed := certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
			Expect(exposed).To(BeTrue())
			Expect(seconds).To(BeNumerically("==", notAfter.Unix()))
		})

		// the CA issues lowercase punycode SANs whatever the form of the domains ordered. No ReadCertificate is expected,
		// gomock fails the spec on an order from the renew tick.
		DescribeTable("doesn't order it again at the renew tick when its leaf holds the domains in the form the CA wrote",
			func(domain, san string) {
				crt := managedCert([]string{san}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
				crt.DomainNames = []string{domain}

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Consistently(manager.DueUuids, 200*time.Millisecond).ShouldNot(ContainElement(crt.GetUUID()))
			},
			Entry("an upper-case domain with its lowercase SAN", "Shop.Example.com", "shop.example.com"),
			Entry("a unicode domain with its punycode SAN", "café.example.com", "xn--caf-dma.example.com"),
			Entry("a wildcard with a trailing dot", "*.Example.com.", "*.example.com"),
		)

		It("leaves it alone while more than a third of its lifetime is left", func() {
			// 90 days of validity, 40 left
			crt := managedCert([]string{"shop.example.com"}, time.Now().Add(-50*24*time.Hour), time.Now().Add(40*24*time.Hour))

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		Context("then removed from the provisioner", func() {
			var crt *cert.Certificate

			BeforeEach(func() {
				crt = managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
			})

			JustBeforeEach(func() {
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
			})

			It("detaches its secret while the controller still has the certificate", func() {
				certClient.EXPECT().ReadCertificate(gomock.Any(), &svccore.UuidRequest{UUID: crt.GetUUID()}).
					Return(&svccert.CertificateResponse{Certificate: crt}, nil)
				secretUpdates := expectSecretUpdate(crt.GetUUID())

				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())

				var detached *svcsecret.UpdateSecretRequest
				Expect(secretUpdates).To(Receive(&detached))
				Expect(detached.GetUUID()).To(Equal("secret-1"))
				Expect(detached.GetSecret().GetMetadata()).To(HaveKeyWithValue("flock.acme.secret.attached", "false"))
			})

			// ownSecret has the controller hold secret-1 as the secret of the certificate crtUuid
			ownSecret := func(crtUuid string) {
				secretClient.EXPECT().ReadSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).
					Return(&svcsecret.SecretResponse{Secret: &secret.Secret{
						UUID:     "secret-1",
						Metadata: map[string]string{"flock.acme.cert.ref": crtUuid},
					}}, nil)
			}

			It("deletes its secret once the controller no longer has the certificate", func() {
				certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
					Return(nil, connect.NewError(connect.CodeNotFound, "not found"))
				ownSecret(crt.GetUUID())
				secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).Return(&emptypb.Empty{}, nil)
				_, exposed := certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
				Expect(exposed).To(BeTrue())

				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())

				_, exposed = certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
				Expect(exposed).To(BeFalse())
			})

			It("counts a secret already deleted as cleaned", func() {
				certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
					Return(nil, connect.NewError(connect.CodeNotFound, "not found"))
				ownSecret(crt.GetUUID())
				secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).
					Return(nil, connect.NewError(connect.CodeNotFound, "not found"))

				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())

				_, exposed := certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
				Expect(exposed).To(BeFalse())
			})

			Context("as a clone pointing at the secret of the certificate it was cloned from", func() {
				BeforeEach(func() {
					// no UpdateSecret nor DeleteSecret may touch the original's secret
					ownSecret("original-uuid")
				})

				It("leaves the secret alone while the controller still has the clone", func() {
					certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
						Return(&svccert.CertificateResponse{Certificate: crt}, nil)

					Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())
				})

				It("leaves the secret alone once the controller no longer has the clone", func() {
					certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
						Return(nil, connect.NewError(connect.CodeNotFound, "not found"))

					Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())
				})
			})

			It("keeps its secret when the controller can't be read", func() {
				certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
					Return(nil, connect.NewError(connect.CodeUnavailable, "controller down"))

				Expect(manager.OnMessageRemove(ctx, crt)).To(MatchError(ContainSubstring("controller down")))
			})
		})
	})

	Context("cleaning up the secrets", func() {
		var (
			deleted chan string
			filters chan svccore.ListFilters
			// deleteErr is the answer of the controller to DeleteSecret
			deleteErr error
		)

		BeforeEach(func() {
			manager.SetTickCleanUp(20 * time.Millisecond)
			deleted = make(chan string, 100)
			filters = make(chan svccore.ListFilters, 100)
			deleteErr = nil

			detached := []*secret.Secret{
				{UUID: "secret-gone", Metadata: map[string]string{"flock.acme.cert.ref": "cert-gone"}},
				{UUID: "secret-kept", Metadata: map[string]string{"flock.acme.cert.ref": "cert-kept"}},
				{UUID: "secret-unreadable", Metadata: map[string]string{"flock.acme.cert.ref": "cert-unreadable"}},
			}
			secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *svcsecret.ListSecretRequest) (*svcsecret.ListSecretResponse, error) {
					filters <- req.GetFilters()
					if req.GetOffset() > 0 {
						return &svcsecret.ListSecretResponse{}, nil
					}
					return &svcsecret.ListSecretResponse{Secrets: detached}, nil
				}).AnyTimes()
			certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *svccore.UuidRequest) (*svccert.CertificateResponse, error) {
					switch req.GetUUID() {
					case "cert-gone":
						return nil, connect.NewError(connect.CodeNotFound, "not found")
					case "cert-unreadable":
						return nil, connect.NewError(connect.CodeUnavailable, "controller down")
					}
					return &svccert.CertificateResponse{Certificate: &cert.Certificate{UUID: req.GetUUID()}}, nil
				}).AnyTimes()
			secretClient.EXPECT().DeleteSecret(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
					deleted <- req.GetUUID()
					return &emptypb.Empty{}, deleteErr
				}).AnyTimes()
		})

		It("deletes only the detached secrets whose certificate the controller no longer has", func() {
			Eventually(deleted).Should(Receive(Equal("secret-gone")))
			Consistently(deleted, 100*time.Millisecond).ShouldNot(Receive(Not(Equal("secret-gone"))))
		})

		It("cleans the detached secrets of any replica", func() {
			var sent svccore.ListFilters
			Eventually(filters).Should(Receive(&sent))
			Expect(sent).To(ContainElements(
				HaveField("Key", "flock.provisioner.acme"),
				And(HaveField("Key", "flock.acme.secret.attached"), HaveField("Value", "false")),
			))
			Expect(sent).NotTo(ContainElement(HaveField("Key", helpers.MetadataProvisionBy)))
		})

		Context("with a secret already deleted", func() {
			BeforeEach(func() {
				deleteErr = connect.NewError(connect.CodeNotFound, "not found")
			})

			It("counts a secret already deleted as cleaned", func() {
				crt := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
				crt.UUID = "cert-gone"
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Eventually(func() bool {
					_, exposed := certmanagers.NotAfterSeconds("cert-gone", "shop")
					return exposed
				}).Should(BeFalse())
			})
		})
	})

	Context("with a controller that never answers", func() {
		var (
			// listingStarted receives once per listing sent, listings once per listing given up
			listingStarted chan struct{}
			listings       chan error
		)

		BeforeEach(func() {
			certmanagers.SetControllerCallTimeout(50 * time.Millisecond)
			manager.SetTickCleanUp(20 * time.Millisecond)
			listingStarted = make(chan struct{}, 100)
			listings = make(chan error, 100)
			secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Any()).
				DoAndReturn(func(callCtx context.Context, _ *svcsecret.ListSecretRequest) (*svcsecret.ListSecretResponse, error) {
					listingStarted <- struct{}{}
					<-callCtx.Done()
					listings <- callCtx.Err()
					return nil, callCtx.Err()
				}).AnyTimes()
		})

		It("gives up the listing of the secrets and lists them again on a later tick", func() {
			Eventually(listings).Should(Receive(MatchError(context.DeadlineExceeded)))
			Eventually(listings).Should(Receive(MatchError(context.DeadlineExceeded)))
		})

		It("still obtains a certificate while a listing hangs", func() {
			certPEM, keyPEM := freshLeafPEM("shop.example.com")
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					req.Registration, req.KeyPem = account()
					return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
				})
			secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
				Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
			expectCertificateUpdates()
			crt := &cert.Certificate{
				UUID:        uuid.NewString(),
				Name:        "shop",
				DomainNames: []string{"shop.example.com"},
				Metadata:    map[string]string{"flock.provisioner.acme": "true", "flock.acme.email": "ops@example.com"},
			}
			expectControllerCopy(func() *cert.Certificate { return crt })
			Eventually(listingStarted).Should(Receive())

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(updates).Should(Receive())
		})
	})

	Context("ordering under a lease", func() {
		var (
			leaseClient *testhelpers.MockLeaseClient
			crt         *cert.Certificate
			certPEM     string
			keyPEM      string
			// the time of each Acquire the controller received
			acquires chan time.Time
		)

		BeforeEach(func() {
			leaseClient = testhelpers.NewMockLeaseClient(ctrl)
			manager = newManager(leaseClient)
			// a first order not written is due again at each renew tick, only the retry timers may order again here
			manager.SetTickRenew(time.Hour)
			acquires = make(chan time.Time, 10)
			crt = &cert.Certificate{
				UUID:        uuid.NewString(),
				Name:        "shop",
				DomainNames: []string{"shop.example.com"},
				Metadata:    map[string]string{"flock.provisioner.acme": "true", "flock.acme.email": "ops@example.com"},
			}
			certPEM, keyPEM = freshLeafPEM("shop.example.com")
		})

		acquireAnswers := func(answers ...func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error)) {
			for _, answer := range answers {
				leaseClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						acquires <- time.Now()
						Expect(req.GetTypeUrl()).To(Equal(helpers.TypeUrl[*cert.Certificate]()))
						Expect(req.GetUUID()).To(Equal(crt.GetUUID()))
						Expect(req.GetInstanceUuid()).To(Equal(instanceUuid))
						return answer(req)
					})
			}
		}
		granted := func(req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
			return grantedLease(req), nil
		}
		expectRenewals := func() {
			leaseClient.EXPECT().Renew(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *svclease.RenewRequest) (*svclease.RenewResponse, error) {
					return renewedLease(req), nil
				}).AnyTimes()
		}
		expectRelease := func() chan *svclease.ReleaseRequest {
			released := make(chan *svclease.ReleaseRequest, 10)
			leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).
				DoAndReturn(func(callCtx context.Context, req *svclease.ReleaseRequest) (*emptypb.Empty, error) {
					Expect(callCtx.Err()).NotTo(HaveOccurred())
					released <- req
					return &emptypb.Empty{}, nil
				})
			return released
		}
		expectOrder := func() {
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					req.Registration, req.KeyPem = account()
					return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
				})
		}
		expectWrite := func() {
			secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
				Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
			expectCertificateUpdates()
		}
		// orderingBlocked has Obtain wait for the end of its ctx, then tell its cause
		orderingBlocked := func() (obtaining chan struct{}, causes chan error) {
			obtaining = make(chan struct{})
			causes = make(chan error, 1)
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(orderCtx context.Context, _ *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					close(obtaining)
					<-orderCtx.Done()
					causes <- context.Cause(orderCtx)
					return nil, orderCtx.Err()
				})
			return obtaining, causes
		}

		Context("with a renew tick", func() {
			BeforeEach(func() {
				manager.SetTickRenew(time.Second)
			})

			It("schedules no retry when refused the lease of a renewal, the holder backs off and the renew tick takes over", func() {
				// 90 days of validity, 20 left
				due := managedCert([]string{"shop.example.com"}, time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
				due.UUID = crt.GetUUID()
				leaseClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
					DoAndReturn(func(context.Context, *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						acquires <- time.Now()
						return heldLease(time.Now().Add(200 * time.Millisecond)), nil
					}).AnyTimes()
				Expect(manager.OnMessageAdded(ctx, due)).To(Succeed())

				// the tick comes every second, a retry at the end of the lease would come 200ms after the refusal
				Eventually(acquires, 2*time.Second).Should(Receive())
				Consistently(acquires, 600*time.Millisecond).ShouldNot(Receive())
			})

			It("orders the new domains another replica left unordered once the tick sees them", func() {
				written := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
				written.UUID = crt.GetUUID()
				written.Metadata[helpers.MetadataProvisionBy] = "agent-2"
				withNewDomain := proto.Clone(written).(*cert.Certificate)
				withNewDomain.DomainNames = append(withNewDomain.DomainNames, "www.shop.example.com")
				crt = withNewDomain
				acquireAnswers(
					func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						return heldLease(time.Now().Add(time.Hour)), nil
					},
					granted,
				)
				expectControllerCopy(func() *cert.Certificate { return withNewDomain })
				expectRenewals()
				expectRelease()
				ordered := make(chan []string, 1)
				obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *params.CertRequest, request legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
						ordered <- request.Domains
						req.Registration, req.KeyPem = account()
						bothPEM, bothKeyPEM := leafPEMFor(request.Domains, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
						return &legocert.Resource{Certificate: []byte(bothPEM), PrivateKey: []byte(bothKeyPEM)}, nil
					})
				expectSecretUpdate(crt.GetUUID())
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, written)).To(Succeed())
				Expect(manager.OnMessageAdded(ctx, withNewDomain)).To(Succeed())

				Eventually(ordered, 3*time.Second).Should(Receive(ConsistOf("shop.example.com", "www.shop.example.com")))
			})
		})

		It("retries a domain change refused the lease once the lease it saw ends", func() {
			written := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
			written.UUID = crt.GetUUID()
			withNewDomain := proto.Clone(written).(*cert.Certificate)
			withNewDomain.DomainNames = append(withNewDomain.DomainNames, "www.shop.example.com")
			crt = withNewDomain
			expiresAt := time.Now().Add(200 * time.Millisecond)
			acquireAnswers(
				func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) { return heldLease(expiresAt), nil },
				granted,
			)
			expectControllerCopy(func() *cert.Certificate { return withNewDomain })
			expectRenewals()
			expectRelease()
			expectOrder()
			expectSecretUpdate(crt.GetUUID())
			expectCertificateUpdates()

			Expect(manager.OnMessageAdded(ctx, written)).To(Succeed())
			Expect(manager.OnMessageAdded(ctx, withNewDomain)).To(Succeed())

			Eventually(acquires).Should(Receive())
			var second time.Time
			Eventually(acquires).Should(Receive(&second))
			Expect(second).To(BeTemporally(">=", expiresAt))
			Eventually(updates).Should(Receive())
		})

		It("records the domains of a change another replica ordered", func() {
			written := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
			written.UUID = crt.GetUUID()
			written.Metadata[helpers.MetadataProvisionBy] = "agent-2"
			withNewDomain := proto.Clone(written).(*cert.Certificate)
			withNewDomain.DomainNames = append(withNewDomain.DomainNames, "www.shop.example.com")
			crt = withNewDomain
			acquireAnswers(func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
				return heldLease(time.Now().Add(time.Hour)), nil
			})

			Expect(manager.OnMessageAdded(ctx, written)).To(Succeed())
			Expect(manager.OnMessageAdded(ctx, withNewDomain)).To(Succeed())
			Eventually(acquires).Should(Receive())
			holderWrote := managedCert(withNewDomain.GetDomainNames(), time.Now().Add(-time.Minute), time.Now().Add(90*24*time.Hour))
			holderWrote.UUID = crt.GetUUID()
			holderWrote.Metadata[helpers.MetadataProvisionBy] = "agent-2"
			Expect(manager.OnMessageAdded(ctx, holderWrote)).To(Succeed())

			Eventually(func() []string { return manager.RecordedDomains(crt.GetUUID()) }).
				Should(ConsistOf("shop.example.com", "www.shop.example.com"))
			Expect(manager.DueUuids()).NotTo(ContainElement(crt.GetUUID()))
		})

		It("retries a first order refused the lease once the lease it saw ends", func() {
			expiresAt := time.Now().Add(200 * time.Millisecond)
			acquireAnswers(
				func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) { return heldLease(expiresAt), nil },
				granted,
			)
			expectControllerCopy(func() *cert.Certificate { return crt })
			expectRenewals()
			released := expectRelease()
			expectOrder()
			expectWrite()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(acquires).Should(Receive())
			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
			var second time.Time
			Eventually(acquires).Should(Receive(&second))
			Expect(second).To(BeTemporally(">=", expiresAt))
			Eventually(updates).Should(Receive())
			Eventually(released).Should(Receive(HaveField("LeaseId", leaseId)))
		})

		It("retries after the retry delay when the controller can't be reached", func() {
			retryDelay := 200 * time.Millisecond
			manager.SetRetry(2, retryDelay)
			acquireAnswers(
				func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					return nil, connect.NewError(connect.CodeUnavailable, "controller down")
				},
				granted,
			)
			expectControllerCopy(func() *cert.Certificate { return crt })
			expectRenewals()
			expectRelease()
			expectOrder()
			expectWrite()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			var first, second time.Time
			Eventually(acquires).Should(Receive(&first))
			Eventually(acquires).Should(Receive(&second))
			Expect(second.Sub(first)).To(BeNumerically(">=", retryDelay))
			Eventually(updates).Should(Receive())
		})

		It("skips a certificate another replica just wrote", func() {
			acquireAnswers(granted)
			written := proto.Clone(crt).(*cert.Certificate)
			written.CertChain, written.PrivateKey = freshLeafPEM("shop.example.com")
			certClient.EXPECT().ReadCertificate(gomock.Any(), &svccore.UuidRequest{UUID: crt.GetUUID()}).
				Return(&svccert.CertificateResponse{Certificate: written}, nil)
			released := expectRelease()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(released).Should(Receive(HaveField("LeaseId", leaseId)))
			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		It("skips when the domains of the controller's copy differ", func() {
			acquireAnswers(granted)
			newer := proto.Clone(crt).(*cert.Certificate)
			newer.DomainNames = []string{"shop.example.com", "www.example.com"}
			certClient.EXPECT().ReadCertificate(gomock.Any(), &svccore.UuidRequest{UUID: crt.GetUUID()}).
				Return(&svccert.CertificateResponse{Certificate: newer}, nil)
			released := expectRelease()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(released).Should(Receive(HaveField("LeaseId", leaseId)))
			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		It("cancels the order when the lease is lost", func() {
			acquireAnswers(granted)
			expectControllerCopy(func() *cert.Certificate { return crt })
			leaseClient.EXPECT().Renew(gomock.Any(), gomock.Any()).
				Return(nil, connect.NewError(connect.CodeNotFound, "lease not found"))
			obtaining, causes := orderingBlocked()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(obtaining).Should(BeClosed())
			// the first renewal comes after a third of the ttl, which the helper keeps at 10s at least
			Eventually(causes, 5*time.Second).Should(Receive(MatchError(leases.ErrLost)))
			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		It("drops the result when the lease is lost before the write", func() {
			acquireAnswers(granted)
			expectControllerCopy(func() *cert.Certificate { return crt })
			renewed := make(chan struct{})
			leaseClient.EXPECT().Renew(gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, *svclease.RenewRequest) (*svclease.RenewResponse, error) {
					close(renewed)
					return nil, connect.NewError(connect.CodeNotFound, "lease not found")
				})
			expectOrder()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(renewed).Should(BeClosed())
			Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
		})

		It("renews and writes an order finishing after the stop", func() {
			acquireAnswers(granted)
			expectControllerCopy(func() *cert.Certificate { return crt })
			obtaining := make(chan struct{})
			finish := make(chan struct{})
			obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
					close(obtaining)
					<-finish
					req.Registration, req.KeyPem = account()
					return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
				})
			gomock.InOrder(
				leaseClient.EXPECT().Renew(gomock.Any(), gomock.Any()).
					DoAndReturn(func(callCtx context.Context, req *svclease.RenewRequest) (*svclease.RenewResponse, error) {
						Expect(callCtx.Err()).NotTo(HaveOccurred())
						return renewedLease(req), nil
					}),
				secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
					Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil),
				certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
					DoAndReturn(func(callCtx context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
						Expect(callCtx.Err()).NotTo(HaveOccurred())
						updates <- req
						return &svccert.CertificateResponse{Certificate: req.GetCertificate()}, nil
					}),
				leaseClient.EXPECT().Release(gomock.Any(), &svclease.ReleaseRequest{LeaseId: leaseId}).
					Return(&emptypb.Empty{}, nil),
			)

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
			Eventually(obtaining).Should(BeClosed())

			cancel()
			close(finish)

			Eventually(runDone).Should(BeClosed())
			Expect(updates).To(Receive())
		})

		It("releases the leases of the orders in flight on stop", func() {
			acquireAnswers(granted)
			expectControllerCopy(func() *cert.Certificate { return crt })
			expectRenewals()
			released := expectRelease()
			obtaining, _ := orderingBlocked()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
			Eventually(obtaining).Should(BeClosed())

			cancel()

			Eventually(runDone).Should(BeClosed())
			Expect(released).To(Receive(HaveField("LeaseId", leaseId)))
		})

		It("starts no order from a retry timer once stopped", func() {
			acquireAnswers(func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
				return heldLease(time.Now().Add(100 * time.Millisecond)), nil
			})

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
			Eventually(acquires).Should(Receive())

			cancel()

			Eventually(runDone).Should(BeClosed())
			Consistently(acquires, 300*time.Millisecond).ShouldNot(Receive())
		})

		DescribeTable("gives up a certificate whose lease the controller refuses",
			func(code connect.Code) {
				acquireAnswers(func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					return nil, connect.NewError(code, "refused")
				})

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Eventually(acquires).Should(Receive())
				Consistently(acquires, 100*time.Millisecond).ShouldNot(Receive())
			},
			Entry("removed from the controller", connect.CodeNotFound),
			Entry("no longer allowed", connect.CodePermissionDenied),
		)

		It("writes the certificate obtained later, with no new order, when the lease can't be renewed before the write", func() {
			acquireAnswers(granted, granted)
			expectControllerCopy(func() *cert.Certificate { return crt })
			leaseClient.EXPECT().Renew(gomock.Any(), gomock.Any()).
				Return(nil, connect.NewError(connect.CodeUnavailable, "controller down"))
			expectRenewals()
			leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).Times(2)
			expectOrder()
			expectWrite()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(updates).Should(Receive())
			Expect(acquires).To(HaveLen(2))
		})

		Context("backing off after a failed order", func() {
			// controllerCopy is the copy the controller answers, read at each call: the order goroutines read it
			var controllerCopy atomic.Pointer[cert.Certificate]

			// withSecretRef is crt as the controller holds it, the secret ref the ads handler resolved in crt
			withSecretRef := func(adsCopy *cert.Certificate) *cert.Certificate {
				stored := proto.Clone(adsCopy).(*cert.Certificate)
				stored.Metadata["flock.acme.ovh.application_key"] = "secret://dns-secret/flock.acme.ovh.application_key"
				return stored
			}
			backingOff := func(crt *cert.Certificate, failures string, retryAfter time.Time) {
				crt.Metadata["flock.acme.failures"] = failures
				crt.Metadata["flock.acme.retry_after"] = retryAfter.Format(time.RFC3339Nano)
			}
			failingOrders := func() {
				obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("ca down")).MinTimes(1)
			}
			retryAfterOf := func(sent *svccert.UpdateCertificateRequest) time.Time {
				at, err := time.Parse(time.RFC3339, sent.GetCertificate().GetMetadata()["flock.acme.retry_after"])
				Expect(err).NotTo(HaveOccurred())
				return at
			}

			BeforeEach(func() {
				crt.Metadata["flock.acme.ovh.application_key"] = "ak-resolved"
				controllerCopy.Store(withSecretRef(crt))
				expectControllerCopy(controllerCopy.Load)
			})

			It("writes the back-off on the copy read from the controller", func() {
				acquireAnswers(granted)
				expectRenewals()
				failingOrders()
				gomock.InOrder(
					certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
						DoAndReturn(func(_ context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
							updates <- req
							return &svccert.CertificateResponse{Certificate: req.GetCertificate()}, nil
						}),
					leaseClient.EXPECT().Release(gomock.Any(), &svclease.ReleaseRequest{LeaseId: leaseId}).
						Return(&emptypb.Empty{}, nil),
				)

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetUUID()).To(Equal(crt.GetUUID()))
				Expect(sent.GetCertificate().GetMetadata()).To(And(
					HaveKeyWithValue("flock.acme.failures", "1"),
					HaveKeyWithValue("flock.acme.ovh.application_key", "secret://dns-secret/flock.acme.ovh.application_key"),
				))
				Expect(sent.GetCertificate().GetMetadata()).NotTo(ContainElement("ak-resolved"))
				Expect(retryAfterOf(sent)).To(BeTemporally("~", time.Now().Add(time.Hour), 5*time.Second))
				Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
			})

			It("doubles the delay at each failed order in a row", func() {
				stored := withSecretRef(crt)
				backingOff(stored, "2", time.Now().Add(-time.Minute))
				controllerCopy.Store(stored)
				acquireAnswers(granted)
				expectRenewals()
				expectRelease()
				failingOrders()
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.failures", "3"))
				Expect(retryAfterOf(sent)).To(BeTemporally("~", time.Now().Add(4*time.Hour), 5*time.Second))
			})

			It("waits for retry_after before ordering", func() {
				retryAt := time.Now().Add(300 * time.Millisecond)
				stored := withSecretRef(crt)
				backingOff(stored, "1", retryAt)
				controllerCopy.Store(stored)
				acquireAnswers(granted, func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					return heldLease(time.Now().Add(time.Hour)), nil
				})
				released := expectRelease()

				// no Obtain is expected: gomock fails the spec on it
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Eventually(released).Should(Receive(HaveField("LeaseId", leaseId)))
				Eventually(acquires).Should(Receive())
				var retried time.Time
				Eventually(acquires).Should(Receive(&retried))
				Expect(retried).To(BeTemporally(">=", retryAt))
			})

			It("keeps a certificate off the renew tick until its retry_after", func() {
				// 90 days of validity, 20 left
				due := managedCert([]string{"shop.example.com"}, time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
				backingOff(due, "1", time.Now().Add(time.Hour))

				Expect(manager.OnMessageAdded(ctx, due)).To(Succeed())

				_, isRecorded := certmanagers.NotAfterSeconds(due.GetUUID(), "shop")
				Expect(isRecorded).To(BeTrue())
				Expect(manager.DueUuids()).NotTo(ContainElement(due.GetUUID()))
			})

			It("orders at once when the domains changed, counting the failures from one again", func() {
				crt.DomainNames = []string{"a.test"}
				stored := withSecretRef(crt)
				backingOff(stored, "3", time.Now().Add(time.Hour))
				controllerCopy.Store(stored)
				acquireAnswers(granted, granted)
				expectRenewals()
				released := expectRelease()
				expectRelease()
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(released).Should(Receive())

				changed := proto.Clone(crt).(*cert.Certificate)
				changed.DomainNames = []string{"b.test"}
				backingOff(changed, "3", time.Now().Add(time.Hour))
				controllerCopy.Store(withSecretRef(changed))
				obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *params.CertRequest, order legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
						Expect(order.Domains).To(Equal([]string{"b.test"}))
						return nil, errors.New("ca down")
					}).MinTimes(1)
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, changed)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetDomainNames()).To(Equal([]string{"b.test"}))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.failures", "1"))
				Expect(retryAfterOf(sent)).To(BeTemporally("~", time.Now().Add(time.Hour), 5*time.Second))
			})

			It("removes the back-off after a successful order", func() {
				backingOff(crt, "2", time.Now().Add(-time.Minute))
				controllerCopy.Store(withSecretRef(crt))
				acquireAnswers(granted)
				expectRenewals()
				expectRelease()
				expectOrder()
				expectWrite()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(sent.GetCertificate().GetMetadata()).NotTo(HaveKey("flock.acme.failures"))
				Expect(sent.GetCertificate().GetMetadata()).NotTo(HaveKey("flock.acme.retry_after"))
			})

			It("writes no back-off for an order cut by the stop", func() {
				acquireAnswers(granted)
				expectRenewals()
				released := expectRelease()
				obtaining, _ := orderingBlocked()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(obtaining).Should(BeClosed())

				cancel()

				// no UpdateCertificate is expected: gomock fails the spec on it
				Eventually(runDone).Should(BeClosed())
				Expect(released).To(Receive(HaveField("LeaseId", leaseId)))
			})
		})

		Context("taking over the certificates of another replica", func() {
			BeforeEach(func() {
				manager.SetTickRenew(100 * time.Millisecond)
			})

			It("keeps a record of a certificate provisioned by another agent and renews it once due", func() {
				// 90 days of validity, 20 left
				crt = managedCert([]string{"shop.example.com"}, time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
				crt.Metadata[helpers.MetadataProvisionBy] = "agent-2"
				acquireAnswers(granted)
				expectControllerCopy(func() *cert.Certificate { return crt })
				expectRenewals()
				expectRelease()
				expectOrder()
				secretUpdates := expectSecretUpdate(crt.GetUUID())
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(acquires).To(HaveLen(1))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(sent.GetCertificate().GetMetadata()).To(And(
					HaveKeyWithValue(helpers.MetadataProvisionBy, agentUuid),
					HaveKeyWithValue("flock.acme.secret.ref", "secret-1"),
				))
				Expect(secretUpdates).To(Receive(HaveField("UUID", "secret-1")))
			})

			It("doesn't reorder a fresh certificate provisioned by another agent", func() {
				crt = managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
				crt.Metadata[helpers.MetadataProvisionBy] = "agent-2"

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Consistently(acquires, 300*time.Millisecond).ShouldNot(Receive())
				_, exposed := certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
				Expect(exposed).To(BeTrue())
			})

			Context("with no renew tick", func() {
				// a tick would order the certificate first seen with no chain, taking the lease meant for the domain change
				BeforeEach(func() {
					manager.SetTickRenew(time.Hour)
				})

				It("reuses the secret of the replica that wrote the certificate it first saw with no chain", func() {
					acquireAnswers(
						func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
							return heldLease(time.Now().Add(time.Hour)), nil
						},
						granted,
					)
					Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
					Eventually(acquires).Should(Receive())

					written := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
					written.UUID = crt.GetUUID()
					written.Metadata[helpers.MetadataProvisionBy] = "agent-2"
					Expect(manager.OnMessageAdded(ctx, written)).To(Succeed())

					withNewDomain := proto.Clone(written).(*cert.Certificate)
					withNewDomain.DomainNames = append(withNewDomain.DomainNames, "www.shop.example.com")
					expectControllerCopy(func() *cert.Certificate { return withNewDomain })
					expectRenewals()
					expectRelease()
					expectOrder()
					secretUpdates := expectSecretUpdate(crt.GetUUID())
					expectCertificateUpdates()

					Expect(manager.OnMessageAdded(ctx, withNewDomain)).To(Succeed())

					var sent *svccert.UpdateCertificateRequest
					Eventually(updates).Should(Receive(&sent))
					Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.secret.ref", "secret-1"))
					Expect(secretUpdates).To(Receive(HaveField("UUID", "secret-1")))
				})
			})

			It("deletes a secret holding the name of the certificate's secret and creates its own, writing nothing into it", func() {
				acquireAnswers(granted)
				expectControllerCopy(func() *cert.Certificate { return crt })
				expectRenewals()
				expectRelease()
				expectOrder()
				// left by a replica gone before writing the certificate, or planted by a user who can create secrets:
				// no UpdateSecret may write the account into it
				var secretName string
				gomock.InOrder(
					secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
						DoAndReturn(func(_ context.Context, req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
							secretName = req.GetSecret().GetName()
							return nil, connect.NewError(connect.CodeAlreadyExists, "secret already exists")
						}),
					secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Any()).
						DoAndReturn(func(_ context.Context, req *svcsecret.ListSecretRequest) (*svcsecret.ListSecretResponse, error) {
							Expect(req.GetFilters()).To(ConsistOf(And(HaveField("Key", "name"), HaveField("Value", secretName))))
							return &svcsecret.ListSecretResponse{Secrets: []*secret.Secret{{
								UUID:     "planted",
								Name:     secretName,
								Metadata: map[string]string{"flock.acme.cert.ref": crt.GetUUID()},
							}}}, nil
						}),
					secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: "planted"}).
						Return(&emptypb.Empty{}, nil),
					secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
						Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-2"}}, nil),
				)
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.secret.ref", "secret-2"))
			})

			// a planted secret referenced by a resource of the user, or the secret of an original a clone points at,
			// can't be deleted. An Obtain more than the one expected fails the spec.
			DescribeTable("creates its secret under a fresh name when the name can't be freed, and points the certificate at it",
				func(code connect.Code) {
					acquireAnswers(granted)
					expectControllerCopy(func() *cert.Certificate { return crt })
					expectRenewals()
					expectRelease()
					expectOrder()
					var secretName string
					gomock.InOrder(
						secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
							DoAndReturn(func(_ context.Context, req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
								secretName = req.GetSecret().GetName()
								return nil, connect.NewError(connect.CodeAlreadyExists, "secret already exists")
							}),
						secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Any()).
							DoAndReturn(func(context.Context, *svcsecret.ListSecretRequest) (*svcsecret.ListSecretResponse, error) {
								return &svcsecret.ListSecretResponse{Secrets: []*secret.Secret{{UUID: "planted", Name: secretName}}}, nil
							}),
						secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: "planted"}).
							Return(nil, connect.NewError(code, "can't delete")),
						secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
							DoAndReturn(func(_ context.Context, req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
								Expect(req.GetSecret().GetName()).To(HavePrefix(secretName + "-"))
								Expect(req.GetSecret().GetMetadata()).To(HaveKeyWithValue("flock.acme.cert.ref", crt.GetUUID()))
								return &svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-2"}}, nil
							}),
					)
					expectCertificateUpdates()

					Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

					var sent *svccert.UpdateCertificateRequest
					Eventually(updates).Should(Receive(&sent))
					Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.secret.ref", "secret-2"))
				},
				Entry("still referenced", connect.CodeFailedPrecondition),
				Entry("not allowed", connect.CodePermissionDenied),
			)

			It("creates its own secret for a clone pointing at the secret of another certificate", func() {
				clone := managedCert([]string{"shop.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(89*24*time.Hour))
				clone.CertChain = ""
				clone.PrivateKey = ""
				crt = clone
				acquireAnswers(granted)
				expectControllerCopy(func() *cert.Certificate { return crt })
				expectRenewals()
				expectRelease()
				expectOrder()
				// the clone kept the secret ref of the original, no UpdateSecret may touch the original's secret
				secretClient.EXPECT().ReadSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).
					Return(&svcsecret.SecretResponse{Secret: &secret.Secret{
						UUID:     "secret-1",
						Metadata: map[string]string{"flock.acme.cert.ref": "original-uuid"},
					}}, nil)
				secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
					Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-2"}}, nil)
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.secret.ref", "secret-2"))
			})

			It("retries a first order left by a dead replica", func() {
				acquireAnswers(
					func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						return heldLease(time.Now().Add(time.Hour)), nil
					},
					granted,
				)
				expectControllerCopy(func() *cert.Certificate { return crt })
				expectRenewals()
				expectRelease()
				expectOrder()
				expectWrite()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Eventually(updates).Should(Receive())
				Expect(acquires).To(HaveLen(2))
			})
		})

		Context("removed while it is ordered", func() {
			// removeAsync runs the removal as the ADS handler does, its error comes once it returned
			removeAsync := func() chan error {
				done := make(chan error, 1)
				go func() {
					done <- manager.OnMessageRemove(ctx, crt)
				}()
				return done
			}

			BeforeEach(func() {
				expectControllerCopy(func() *cert.Certificate { return crt })
			})

			It("cancels the order in flight and returns at once, writing nothing", func() {
				acquireAnswers(granted)
				expectRenewals()
				released := expectRelease()
				obtaining, causes := orderingBlocked()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(obtaining).Should(BeClosed())

				removed := removeAsync()

				Eventually(removed, 500*time.Millisecond).Should(Receive(Succeed()))
				Expect(causes).To(Receive(MatchError(certmanagers.ErrRemoved)))
				Expect(released).To(Receive(HaveField("LeaseId", leaseId)))
				Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
			})

			It("writes nothing from a retry timer of a removed certificate", func() {
				acquireAnswers(func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					return heldLease(time.Now().Add(100 * time.Millisecond)), nil
				})
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(acquires).Should(Receive())
				// the timer fires while the lock is held, its order waits for the lock as the removal does
				unlock := manager.LockCert(crt.GetUUID())
				Consistently(acquires, 200*time.Millisecond).ShouldNot(Receive())
				removed := removeAsync()
				Consistently(removed, 50*time.Millisecond).ShouldNot(Receive())

				unlock()

				Eventually(removed).Should(Receive(Succeed()))
				Consistently(acquires, 200*time.Millisecond).ShouldNot(Receive())
			})

			It("writes nothing from a queued order of a removed certificate", func() {
				unlock := manager.LockCert(crt.GetUUID())
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				removed := removeAsync()
				Consistently(removed, 50*time.Millisecond).ShouldNot(Receive())

				unlock()

				Eventually(removed).Should(Receive(Succeed()))
				Consistently(acquires, 200*time.Millisecond).ShouldNot(Receive())
			})

			It("writes nothing when the removal comes after the order and before the write", func() {
				acquireAnswers(granted)
				released := expectRelease()
				expectOrder()
				renewing := make(chan struct{})
				proceed := make(chan struct{})
				leaseClient.EXPECT().Renew(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *svclease.RenewRequest) (*svclease.RenewResponse, error) {
						close(renewing)
						<-proceed
						return renewedLease(req), nil
					})

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(renewing).Should(BeClosed())
				removed := removeAsync()
				// the removal waits for the lock the order holds, once it cancelled the order
				Consistently(removed, 50*time.Millisecond).ShouldNot(Receive())

				close(proceed)

				// no CreateSecret nor UpdateCertificate is expected: gomock fails the spec on either
				Eventually(removed).Should(Receive(Succeed()))
				Expect(released).To(Receive(HaveField("LeaseId", leaseId)))
				Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
			})

			It("orders a removed certificate again once it is added back", func() {
				acquireAnswers(
					func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						return nil, connect.NewError(connect.CodePermissionDenied, "refused")
					},
					granted,
				)
				expectRenewals()
				expectRelease()
				expectOrder()
				expectWrite()
				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(acquires).Should(Receive())
				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Eventually(updates).Should(Receive())
			})
		})

		Context("with a removal failing on the controller", func() {
			BeforeEach(func() {
				certmanagers.SetRemovalKeptFor(10 * time.Millisecond)
				manager.SetTickRenew(20 * time.Millisecond)
				manager.SetTickCleanUp(20 * time.Millisecond)
				secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Any()).
					Return(&svcsecret.ListSecretResponse{}, nil).AnyTimes()
			})

			It("never orders the certificate again, even once the removals are pruned", func() {
				leaseClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
					DoAndReturn(func(context.Context, *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						select {
						case acquires <- time.Now():
						default:
						}
						return nil, connect.NewError(connect.CodePermissionDenied, "refused")
					}).AnyTimes()
				certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
					Return(nil, connect.NewError(connect.CodeUnavailable, "controller down"))

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(acquires).Should(Receive())

				Expect(manager.OnMessageRemove(ctx, crt)).To(MatchError(ContainSubstring("controller down")))
				for len(acquires) > 0 {
					<-acquires
				}

				Consistently(acquires, 300*time.Millisecond).ShouldNot(Receive())
			})
		})

		Context("removed from the provisioner while the controller still has it", func() {
			BeforeEach(func() {
				manager.SetTickRenew(20 * time.Millisecond)
			})

			// the renew tick asks again for the lease of the certificate due until it is removed
			refusingAll := func() {
				leaseClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
					DoAndReturn(func(context.Context, *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						select {
						case acquires <- time.Now():
						default:
						}
						return nil, connect.NewError(connect.CodePermissionDenied, "refused")
					}).AnyTimes()
			}
			drain := func() {
				for len(acquires) > 0 {
					<-acquires
				}
			}

			// no ReadSecret, UpdateSecret nor UpdateCertificate is expected: gomock fails the spec on any
			It("drops a certificate with no chain without detaching a secret it doesn't have", func() {
				refusingAll()
				expectControllerCopy(func() *cert.Certificate { return crt })

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(acquires).Should(Receive())

				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())
				drain()

				Consistently(acquires, 200*time.Millisecond).ShouldNot(Receive())
				Expect(manager.DueUuids()).NotTo(ContainElement(crt.GetUUID()))
			})

			It("drops a certificate with a chain once its secret is detached", func() {
				// 90 days of validity, 20 left
				crt = managedCert([]string{"shop.example.com"}, time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
				refusingAll()
				expectControllerCopy(func() *cert.Certificate { return crt })
				secretUpdates := expectSecretUpdate(crt.GetUUID())

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(acquires).Should(Receive())

				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())

				Expect(secretUpdates).To(Receive(And(
					HaveField("UUID", "secret-1"),
					HaveField("Secret.Metadata", HaveKeyWithValue("flock.acme.secret.attached", "false")),
				)))
				drain()
				Consistently(acquires, 200*time.Millisecond).ShouldNot(Receive())
				_, exposed := certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
				Expect(exposed).To(BeFalse())
			})
		})

		Context("once its retry and its removal are over", func() {
			BeforeEach(func() {
				certmanagers.SetRemovalKeptFor(10 * time.Millisecond)
				manager.SetTickCleanUp(20 * time.Millisecond)
				secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Any()).
					Return(&svcsecret.ListSecretResponse{}, nil).AnyTimes()
				expectControllerCopy(func() *cert.Certificate { return crt })
			})

			It("keeps nothing for the certificate", func() {
				acquireAnswers(
					func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						return heldLease(time.Now().Add(100 * time.Millisecond)), nil
					},
					func(*svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						return nil, connect.NewError(connect.CodePermissionDenied, "refused")
					},
				)

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
				Eventually(acquires).Should(Receive())
				// the second Acquire is the retry scheduled at the end of the held lease
				Eventually(acquires).Should(Receive())
				Eventually(func() bool { return manager.HasActivity(crt.GetUUID()) }).Should(BeFalse())

				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())

				Eventually(func() bool { return manager.HasActivity(crt.GetUUID()) }).Should(BeFalse())
				Consistently(acquires, 100*time.Millisecond).ShouldNot(Receive())
			})
		})

		Context("with a write to the controller failing once", func() {
			expectFailingThenWorkingUpdate := func() {
				certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
					Return(nil, connect.NewError(connect.CodeUnavailable, "controller down"))
				expectCertificateUpdates()
			}

			Context("on a first order", func() {
				// an Obtain more than the one expected fails the spec: each order counts against the limits of the CA
				It("writes the certificate obtained again after the retry delay with no new order, reusing the secret the failed write created", func() {
					acquireAnswers(granted, granted)
					expectControllerCopy(func() *cert.Certificate { return crt })
					expectRenewals()
					leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).Times(2)
					expectOrder()
					secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
						Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
					secretUpdates := expectSecretUpdate(crt.GetUUID())
					expectFailingThenWorkingUpdate()

					Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

					var sent *svccert.UpdateCertificateRequest
					Eventually(updates).Should(Receive(&sent))
					Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
					Expect(sent.GetCertificate().GetPrivateKey()).To(Equal(keyPEM))
					Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.secret.ref", "secret-1"))
					Expect(secretUpdates).To(Receive(HaveField("UUID", "secret-1")))
					_, exposed := certmanagers.NotAfterSeconds(crt.GetUUID(), "shop")
					Expect(exposed).To(BeTrue())
				})

				It("orders the new domains instead of writing the certificate obtained for the old ones", func() {
					// only a domain change may start the second order
					manager.SetRetry(2, time.Hour)
					var controllerCopy atomic.Pointer[cert.Certificate]
					controllerCopy.Store(crt)
					expectControllerCopy(controllerCopy.Load)
					acquireAnswers(granted, granted)
					expectRenewals()
					leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).Times(2)
					ordered := make(chan []string, 2)
					obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
						DoAndReturn(func(_ context.Context, req *params.CertRequest, request legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
							ordered <- request.Domains
							req.Registration, req.KeyPem = account()
							chainPEM, chainKeyPEM := leafPEMFor(request.Domains, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
							return &legocert.Resource{Certificate: []byte(chainPEM), PrivateKey: []byte(chainKeyPEM)}, nil
						}).Times(2)
					secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
						Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
					expectSecretUpdate(crt.GetUUID())
					expectFailingThenWorkingUpdate()
					Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
					Eventually(ordered).Should(Receive(ConsistOf("shop.example.com")))

					changed := proto.Clone(crt).(*cert.Certificate)
					changed.DomainNames = []string{"shop.example.com", "www.shop.example.com"}
					controllerCopy.Store(changed)
					Expect(manager.OnMessageAdded(ctx, changed)).To(Succeed())

					Eventually(ordered).Should(Receive(ConsistOf("shop.example.com", "www.shop.example.com")))
					var sent *svccert.UpdateCertificateRequest
					Eventually(updates).Should(Receive(&sent))
					Expect(sent.GetCertificate().GetDomainNames()).To(ConsistOf("shop.example.com", "www.shop.example.com"))
					Expect(sent.GetCertificate().GetCertChain()).NotTo(Equal(certPEM))
				})
			})

			Context("on a renewal", func() {
				BeforeEach(func() {
					manager = newManager(grantingLease(ctrl))
					// only the renew tick may order again
					manager.SetRetry(2, time.Hour)
				})

				It("writes the renewal again on the next tick with no new order", func() {
					// 90 days of validity, 20 left
					due := managedCert([]string{"shop.example.com"}, time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
					expectControllerCopy(func() *cert.Certificate { return due })
					expectOrder()
					expectSecretUpdate(due.GetUUID())
					expectFailingThenWorkingUpdate()

					Expect(manager.OnMessageAdded(ctx, due)).To(Succeed())

					var sent *svccert.UpdateCertificateRequest
					Eventually(updates).Should(Receive(&sent))
					Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				})
			})
		})

		Context("edited by the user while the retry of its renewal waits", func() {
			var (
				due    *cert.Certificate
				edited *cert.Certificate
				// controllerCopy is the copy the controller answers, read at each call: the order goroutines read it
				controllerCopy atomic.Pointer[cert.Certificate]
				// dnsKeys gets the DNS provider key of each order
				dnsKeys chan any
			)

			withBackOff := func(crt *cert.Certificate, retryAfter time.Time) *cert.Certificate {
				backingOff := proto.Clone(crt).(*cert.Certificate)
				backingOff.Metadata["flock.acme.failures"] = "1"
				backingOff.Metadata["flock.acme.retry_after"] = retryAfter.Format(time.RFC3339Nano)
				return backingOff
			}

			BeforeEach(func() {
				// the renewal starts at the first renew tick, which waits for the uuid the spec gives once its mocks
				// are set
				givesUuidAtStart = false
				manager.SetRetry(2, 300*time.Millisecond)
				// 90 days of validity, 20 left
				due = managedCert([]string{"shop.example.com"}, time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
				due.UUID = crt.GetUUID()
				due.Metadata["flock.acme.ovh.application_key"] = "ak-old"
				edited = proto.Clone(due).(*cert.Certificate)
				edited.Metadata["flock.acme.ovh.application_key"] = "ak-fixed"
				controllerCopy.Store(due)
				expectControllerCopy(controllerCopy.Load)
				acquireAnswers(granted, granted)
				expectRenewals()
				expectSecretUpdate(due.GetUUID())
				dnsKeys = make(chan any, 2)
				obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
						dnsKeys <- req.DnsProvider["flock.acme.ovh.application_key"]
						return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
					})
			})

			It("orders after the back-off with the metadata of the edit and writes them", func() {
				retryAt := time.Now().Add(500 * time.Millisecond)
				controllerCopy.Store(withBackOff(due, retryAt))
				released := expectRelease()
				expectRelease()
				expectCertificateUpdates()
				Expect(manager.OnMessageAdded(ctx, due)).To(Succeed())
				identity.give(agentUuid)
				Eventually(released).Should(Receive())

				edited = withBackOff(edited, retryAt)
				controllerCopy.Store(edited)
				Expect(manager.OnMessageAdded(ctx, edited)).To(Succeed())

				Eventually(dnsKeys).Should(Receive(Equal("ak-fixed")))
				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.ovh.application_key", "ak-fixed"))
			})

			It("writes the renewal whose write failed with the metadata of the edit", func() {
				released := expectRelease()
				expectRelease()
				certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
					Return(nil, connect.NewError(connect.CodeUnavailable, "controller down"))
				expectCertificateUpdates()
				Expect(manager.OnMessageAdded(ctx, due)).To(Succeed())
				identity.give(agentUuid)
				Eventually(dnsKeys).Should(Receive(Equal("ak-old")))
				Eventually(released).Should(Receive())

				controllerCopy.Store(edited)
				Expect(manager.OnMessageAdded(ctx, edited)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("flock.acme.ovh.application_key", "ak-fixed"))
			})
		})

		Context("holding a secret ref the ads handler resolved", func() {
			const dnsKeyRef = "secret://dns-secret/flock.acme.ovh.application_key"

			BeforeEach(func() {
				crt.Metadata["flock.acme.ovh.application_key"] = "ak-resolved"
				stored := proto.Clone(crt).(*cert.Certificate)
				stored.Metadata["flock.acme.ovh.application_key"] = dnsKeyRef
				stored.Metadata["team"] = "payments"
				expectControllerCopy(func() *cert.Certificate { return stored })
				expectRenewals()
				expectOrder()
			})

			It("writes the certificate obtained with the ref, never the resolved value", func() {
				acquireAnswers(granted)
				expectRelease()
				expectWrite()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(sent.GetCertificate().GetPrivateKey()).To(Equal(keyPEM))
				Expect(sent.GetCertificate().GetMetadata()).To(And(
					HaveKeyWithValue("flock.acme.ovh.application_key", dnsKeyRef),
					HaveKeyWithValue("flock.acme.registration", "secret://secret-1/flock.acme.registration"),
					HaveKeyWithValue("flock.acme.key_pem", "secret://secret-1/flock.acme.key_pem"),
					HaveKeyWithValue("flock.acme.secret.ref", "secret-1"),
					HaveKeyWithValue(helpers.MetadataProvisionBy, agentUuid),
				))
				Expect(sent.GetCertificate().GetMetadata()).NotTo(ContainElement("ak-resolved"))
			})

			// the ADS copy the order started from is older than the copy read under the lease
			It("keeps a metadata edit the user made before the lease", func() {
				acquireAnswers(granted)
				expectRelease()
				expectWrite()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetMetadata()).To(HaveKeyWithValue("team", "payments"))
			})

			It("writes the ref again when it retries a failed write", func() {
				acquireAnswers(granted, granted)
				leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).Times(2)
				secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
					Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
				expectSecretUpdate(crt.GetUUID())
				failed := make(chan *svccert.UpdateCertificateRequest, 1)
				certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
						failed <- req
						return nil, connect.NewError(connect.CodeUnavailable, "controller down")
					})
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(sent.GetCertificate().GetMetadata()).To(And(
					HaveKeyWithValue("flock.acme.ovh.application_key", dnsKeyRef),
					HaveKeyWithValue("team", "payments"),
				))
				Expect(sent.GetCertificate().GetMetadata()).NotTo(ContainElement("ak-resolved"))
				Expect(failed).To(Receive(HaveField("Certificate.Metadata", Not(ContainElement("ak-resolved")))))
			})
		})

		// an order lasts up to ~30 minutes with its retries, and a write replaces the whole certificate
		Context("edited on the controller while it is ordered", func() {
			const (
				dnsKeyRef    = "secret://dns-secret/flock.acme.ovh.application_key"
				editedKeyRef = "secret://dns-secret-2/flock.acme.ovh.application_key"
			)
			var (
				// controllerCopy is the copy the controller answers, read at each call: the order goroutines read it
				controllerCopy atomic.Pointer[cert.Certificate]
				// edit is applied to the controller's copy while Obtain runs
				edit func(*cert.Certificate)
			)

			editDuringObtain := func(obtainErr error) {
				obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, _ challenge.Provider) (*legocert.Resource, error) {
						edited := proto.Clone(controllerCopy.Load()).(*cert.Certificate)
						edit(edited)
						controllerCopy.Store(edited)
						if obtainErr != nil {
							return nil, obtainErr
						}
						req.Registration, req.KeyPem = account()
						return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
					}).MinTimes(1)
			}
			editMetadata := func(edited *cert.Certificate) {
				edited.Metadata["flock.acme.ovh.application_key"] = editedKeyRef
				edited.Metadata["team"] = "payments"
			}
			editDomains := func(edited *cert.Certificate) {
				edited.DomainNames = []string{"shop.example.com", "www.example.com"}
			}
			removeProvisionerKey := func(edited *cert.Certificate) {
				delete(edited.Metadata, "flock.provisioner.acme")
			}

			BeforeEach(func() {
				crt.Metadata["flock.acme.ovh.application_key"] = "ak-resolved"
				stored := proto.Clone(crt).(*cert.Certificate)
				stored.Metadata["flock.acme.ovh.application_key"] = dnsKeyRef
				controllerCopy.Store(stored)
				expectControllerCopy(controllerCopy.Load)
				acquireAnswers(granted)
				expectRenewals()
				expectRelease()
			})

			It("writes the certificate obtained onto the edited copy", func() {
				edit = editMetadata
				editDuringObtain(nil)
				expectWrite()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(sent.GetCertificate().GetMetadata()).To(And(
					HaveKeyWithValue("flock.acme.ovh.application_key", editedKeyRef),
					HaveKeyWithValue("team", "payments"),
					HaveKeyWithValue("flock.acme.secret.ref", "secret-1"),
				))
			})

			It("writes the back-off onto the edited copy", func() {
				edit = editMetadata
				editDuringObtain(errors.New("ca down"))
				expectCertificateUpdates()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetMetadata()).To(And(
					HaveKeyWithValue("flock.acme.failures", "1"),
					HaveKeyWithValue("flock.acme.ovh.application_key", editedKeyRef),
					HaveKeyWithValue("team", "payments"),
				))
			})

			// the ADS update of the new domains orders them, a write would put the old domains back
			It("writes nothing when the domains changed", func() {
				edit = editDomains
				editDuringObtain(nil)

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Consistently(updates, 200*time.Millisecond).ShouldNot(Receive())
			})

			It("writes no back-off when the domains changed", func() {
				edit = editDomains
				editDuringObtain(errors.New("ca down"))

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Eventually(stateError(crt.GetUUID())).Should(MatchError(ContainSubstring("failed to obtain certificate")))
				Consistently(updates, 200*time.Millisecond).ShouldNot(Receive())
			})

			// the write would put back the key the user took off, and the provisioner would keep the certificate
			It("writes nothing when the certificate left the provisioner", func() {
				edit = removeProvisionerKey
				editDuringObtain(nil)

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Consistently(updates, 200*time.Millisecond).ShouldNot(Receive())
			})

			It("writes no back-off when the certificate left the provisioner", func() {
				edit = removeProvisionerKey
				editDuringObtain(errors.New("ca down"))

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				Eventually(stateError(crt.GetUUID())).Should(MatchError(ContainSubstring("failed to obtain certificate")))
				Consistently(updates, 200*time.Millisecond).ShouldNot(Receive())
			})
		})

		// OnMessageAdded records no certificate holding a chain and no key, the write of its order still needs a record
		It("orders and writes a certificate whose chain is due and was never provisioned", func() {
			crt.CertChain, _ = leafPEM("shop.example.com", time.Now().Add(-70*24*time.Hour), time.Now().Add(20*24*time.Hour))
			expectControllerCopy(func() *cert.Certificate { return crt })
			acquireAnswers(granted)
			expectRenewals()
			expectRelease()
			expectOrder()
			expectWrite()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			var sent *svccert.UpdateCertificateRequest
			Eventually(updates).Should(Receive(&sent))
			Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
			Expect(sent.GetCertificate().GetPrivateKey()).To(Equal(keyPEM))
		})

		Context("with the read before the write failing once", func() {
			// an Obtain more than the one expected fails the spec: each order counts against the limits of the CA
			It("writes the certificate obtained again after the retry delay with no new order", func() {
				stored := proto.Clone(crt).(*cert.Certificate)
				var reads atomic.Int32
				certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *svccore.UuidRequest) (*svccert.CertificateResponse, error) {
						// the first read is the one deciding the order
						if reads.Add(1) == 2 {
							return nil, connect.NewError(connect.CodeUnavailable, "controller down")
						}
						return &svccert.CertificateResponse{Certificate: proto.Clone(stored).(*cert.Certificate)}, nil
					}).AnyTimes()
				acquireAnswers(granted, granted)
				expectRenewals()
				leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).Times(2)
				expectOrder()
				expectWrite()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				var sent *svccert.UpdateCertificateRequest
				Eventually(updates).Should(Receive(&sent))
				Expect(sent.GetCertificate().GetCertChain()).To(Equal(certPEM))
				Expect(reads.Load()).To(BeNumerically(">=", 3))
			})
		})

		Context("with a write to the controller failing every time", func() {
			It("retries the write at the retry delay three times, then waits the back-off, never ordering again", func() {
				manager.SetRetry(2, 50*time.Millisecond)
				leaseClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
						acquires <- time.Now()
						return grantedLease(req), nil
					}).AnyTimes()
				expectRenewals()
				leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).AnyTimes()
				expectControllerCopy(func() *cert.Certificate { return crt })
				expectOrder()
				secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
					Return(&svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil)
				expectSecretUpdate(crt.GetUUID())
				writes := make(chan struct{}, 10)
				certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
					DoAndReturn(func(context.Context, *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
						writes <- struct{}{}
						return nil, connect.NewError(connect.CodeUnavailable, "controller down")
					}).AnyTimes()

				Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

				for range 3 {
					Eventually(writes).Should(Receive())
				}
				Expect(acquires).To(HaveLen(3))
				// the back-off after the third failure is an hour
				Consistently(acquires, 500*time.Millisecond).Should(HaveLen(3))
				Expect(writes).To(BeEmpty())
			})
		})
	})
})

var _ = DescribeTable("the back-off after failed orders in a row",
	func(failures int, delay time.Duration) {
		Expect(certmanagers.BackOffDelay(failures)).To(Equal(delay))
	},
	Entry("an hour after the first", 1, time.Hour),
	Entry("doubled after the second", 2, 2*time.Hour),
	Entry("doubled again after the third", 3, 4*time.Hour),
	Entry("capped at six hours", 4, 6*time.Hour),
	Entry("still capped long after", 10, 6*time.Hour),
)
