package certmanagers_test

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/cespare/xxhash/v2"
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
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/acme/params"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const (
	challengeSecretUuid = "challenge-1"
	challengeRef        = "secret://challenge-1/marker"
)

// fakeCertController holds the controller's copy of one certificate, which the writes of the manager replace
type fakeCertController struct {
	mutex  sync.Mutex
	copy   *cert.Certificate
	reads  int
	writes int
	// editOnReread is merged into the copy at the second read and stays, a user edit made during the order
	editOnReread map[string]string
}

func (f *fakeCertController) read() *cert.Certificate {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.reads++
	if f.reads == 2 {
		maps.Copy(f.copy.Metadata, f.editOnReread)
	}
	return proto.Clone(f.copy).(*cert.Certificate)
}

func (f *fakeCertController) write(crt *cert.Certificate) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.copy = proto.Clone(crt).(*cert.Certificate)
	f.writes++
}

func (f *fakeCertController) writeCount() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.writes
}

var _ = Describe("CertManager challenge secret", func() {
	var (
		ctrl         *gomock.Controller
		obtainer     *mocks.MockObtainer
		certClient   *mocks.MockCertificateClient
		secretClient *mocks.MockSecretClient
		manager      *certmanagers.CertManager
		states       *stores.StateStore
		ctx          context.Context
		cancel       context.CancelFunc
		crt          *cert.Certificate
		controller   *fakeCertController
		updates      chan *svccert.UpdateCertificateRequest
	)

	challengeSecretName := func(crtUuid string) string {
		return fmt.Sprintf("flock-acme-challenges-%x", xxhash.Sum64([]byte(crtUuid)))
	}

	BeforeEach(func() {
		ctrl = newMockController()
		obtainer = mocks.NewMockObtainer(ctrl)
		certClient = mocks.NewMockCertificateClient(ctrl)
		secretClient = mocks.NewMockSecretClient(ctrl)
		states = stores.NewStateStore()
		identity := newFakeIdentity()
		identity.give(agentUuid)
		manager = certmanagers.NewCertManager(
			obtainer, certClient, secretClient, leases.NewClient(grantingLease(ctrl), instanceUuid), identity, states, "",
		)
		manager.SetRetry(2, time.Millisecond)
		manager.SetTickRenew(time.Hour)
		manager.SetTickCleanUp(time.Hour)
		manager.SetLeaseTTL(300 * time.Millisecond)
		manager.SetChallenges(challenges.NewTable())
		updates = make(chan *svccert.UpdateCertificateRequest, 10)

		crt = &cert.Certificate{
			UUID:        uuid.NewString(),
			Name:        "shop",
			DomainNames: []string{"shop.example.com"},
			Metadata:    map[string]string{"flock.provisioner.acme": "true", "flock.acme.email": "ops@example.com"},
		}
		controller = &fakeCertController{copy: proto.Clone(crt).(*cert.Certificate)}
		ctx, cancel = context.WithCancel(context.Background())
	})

	JustBeforeEach(func() {
		runDone := make(chan struct{})
		go func() {
			defer close(runDone)
			manager.Run(ctx)
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(runDone).Should(BeClosed())
		})
	})

	expectController := func() {
		certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *svccore.UuidRequest) (*svccert.CertificateResponse, error) {
				return &svccert.CertificateResponse{Certificate: controller.read()}, nil
			}).AnyTimes()
		certClient.EXPECT().UpdateCertificate(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
				controller.write(req.GetCertificate())
				updates <- req
				return &svccert.CertificateResponse{Certificate: req.GetCertificate()}, nil
			}).AnyTimes()
	}

	// expectSecretCreates answers the creates of the account secret, and of the challenge secret with createChallenge
	expectSecretCreates := func(
		createChallenge func(*svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error),
	) chan *svcsecret.CreateSecretRequest {
		creates := make(chan *svcsecret.CreateSecretRequest, 10)
		secretClient.EXPECT().CreateSecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
				creates <- req
				if strings.HasPrefix(req.GetSecret().GetName(), "flock-acme-challenges-") {
					return createChallenge(req)
				}
				return &svcsecret.SecretResponse{Secret: &secret.Secret{UUID: "secret-1"}}, nil
			}).AnyTimes()
		return creates
	}
	challengeCreated := func(req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
		created := proto.Clone(req.GetSecret()).(*secret.Secret)
		created.UUID = challengeSecretUuid
		return &svcsecret.SecretResponse{Secret: created}, nil
	}

	expectSecretList := func(found ...*secret.Secret) chan svccore.ListFilters {
		filters := make(chan svccore.ListFilters, 10)
		secretClient.EXPECT().ListSecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svcsecret.ListSecretRequest) (*svcsecret.ListSecretResponse, error) {
				filters <- req.GetFilters()
				return &svcsecret.ListSecretResponse{Secrets: found}, nil
			}).AnyTimes()
		return filters
	}

	existingChallengeSecret := func(crtUuid string) *secret.Secret {
		return &secret.Secret{
			UUID: challengeSecretUuid,
			Name: challengeSecretName(crtUuid),
			Metadata: map[string]string{
				challenges.MetadataKey:       "true",
				"flock.acme.cert.ref":        crtUuid,
				"flock.acme.secret.attached": "true",
			},
		}
	}

	// expectOrder tells the solver each Obtain received, and how many certificate writes came before it
	expectOrder := func() (solvers chan challenge.Provider, writesBefore chan int) {
		solvers = make(chan challenge.Provider, 10)
		writesBefore = make(chan int, 10)
		certPEM, keyPEM := freshLeafPEM("shop.example.com")
		obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *params.CertRequest, _ legocert.ObtainRequest, solver challenge.Provider) (*legocert.Resource, error) {
				solvers <- solver
				writesBefore <- controller.writeCount()
				req.Registration, req.KeyPem = account()
				return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
			})
		return solvers, writesBefore
	}

	It("creates the challenge secret and references it before the first order", func() {
		controller.editOnReread = map[string]string{"team": "payments"}
		expectController()
		filters := expectSecretList()
		creates := expectSecretCreates(challengeCreated)
		solvers, writesBefore := expectOrder()

		Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

		var listed svccore.ListFilters
		Eventually(filters).Should(Receive(&listed))
		Expect(listed).To(ConsistOf(
			HaveField("Key", "flock.acme.cert.ref"),
			HaveField("Key", challenges.MetadataKey),
		))
		for _, filter := range listed {
			Expect(filter.GetIsMetadata()).To(BeTrue())
			Expect(filter.GetOperator()).To(Equal(svccore.ListFilter_EQUAL))
		}
		Expect(listed).To(ContainElement(HaveField("Value", crt.GetUUID())))
		Expect(listed).To(ContainElement(HaveField("Value", "true")))

		var created *svcsecret.CreateSecretRequest
		Eventually(creates).Should(Receive(&created))
		Expect(created.GetSecret().GetName()).To(Equal(challengeSecretName(crt.GetUUID())))
		Expect(created.GetSecret().GetData()).To(Equal(challenges.EncodeData(nil)))
		Expect(created.GetSecret().GetMetadata()).To(Equal(map[string]string{
			challenges.MetadataKey:       "true",
			helpers.MetadataLockKey:      "true",
			helpers.MetadataProvisionBy:  agentUuid,
			"flock.provisioner.acme":     "true",
			"flock.acme.cert.ref":        crt.GetUUID(),
			"flock.acme.secret.attached": "true",
		}))

		var referenced *svccert.UpdateCertificateRequest
		Eventually(updates).Should(Receive(&referenced))
		Expect(referenced.GetCertificate().GetMetadata()).To(HaveKeyWithValue(challenges.RefMetadataKey, challengeRef))
		Expect(referenced.GetCertificate().GetMetadata()).To(HaveKeyWithValue("team", "payments"))
		Expect(referenced.GetCertificate().GetCertChain()).To(BeEmpty())

		var solver challenge.Provider
		Eventually(solvers).Should(Receive(&solver))
		Expect(solver).To(BeAssignableToTypeOf(&challenges.Provider{}))
		Expect(<-writesBefore).To(Equal(1))

		var written *svccert.UpdateCertificateRequest
		Eventually(updates).Should(Receive(&written))
		Expect(written.GetCertificate().GetCertChain()).NotTo(BeEmpty())
		Expect(written.GetCertificate().GetMetadata()).To(HaveKeyWithValue(challenges.RefMetadataKey, challengeRef))
	})

	It("reuses the challenge secret of an earlier order", func() {
		controller.copy.Metadata[challenges.RefMetadataKey] = challengeRef
		// the ads handler resolved the reference to the marker of the secret
		crt.Metadata[challenges.RefMetadataKey] = "challenges"
		expectController()
		expectSecretList(existingChallengeSecret(crt.GetUUID()))
		creates := expectSecretCreates(func(req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
			return nil, fmt.Errorf("challenge secret %s created again", req.GetSecret().GetName())
		})
		solvers, writesBefore := expectOrder()

		Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

		Eventually(solvers).Should(Receive(BeAssignableToTypeOf(&challenges.Provider{})))
		Expect(<-writesBefore).To(Equal(0))
		var written *svccert.UpdateCertificateRequest
		Eventually(updates).Should(Receive(&written))
		Expect(written.GetCertificate().GetCertChain()).NotTo(BeEmpty())
		Expect(written.GetCertificate().GetMetadata()).To(HaveKeyWithValue(challenges.RefMetadataKey, challengeRef))
		Expect(creates).To(HaveLen(1))
		Expect((<-creates).GetSecret().GetName()).NotTo(HavePrefix("flock-acme-challenges-"))
	})

	It("attaches again the challenge secret a removal detached before the order uses it", func() {
		controller.copy.Metadata[challenges.RefMetadataKey] = challengeRef
		crt.Metadata[challenges.RefMetadataKey] = "challenges"
		expectController()
		detached := existingChallengeSecret(crt.GetUUID())
		detached.Metadata["flock.acme.secret.attached"] = "false"
		detached.Metadata[helpers.MetadataProvisionBy] = "agent-2"
		expectSecretList(detached)
		attached := make(chan *svcsecret.UpdateSecretRequest, 10)
		secretClient.EXPECT().UpdateSecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svcsecret.UpdateSecretRequest) (*svcsecret.SecretResponse, error) {
				attached <- req
				return &svcsecret.SecretResponse{Secret: req.GetSecret()}, nil
			})
		expectSecretCreates(challengeCreated)
		solvers, _ := expectOrder()

		Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

		var update *svcsecret.UpdateSecretRequest
		Eventually(attached).Should(Receive(&update))
		Expect(update.GetUUID()).To(Equal(challengeSecretUuid))
		Expect(update.GetSecret().GetMetadata()).To(HaveKeyWithValue("flock.acme.secret.attached", "true"))
		Expect(update.GetSecret().GetMetadata()).To(HaveKeyWithValue(helpers.MetadataProvisionBy, agentUuid))
		Expect(update.GetSecret().GetMetadata()).To(HaveKeyWithValue(challenges.MetadataKey, "true"))
		Eventually(solvers).Should(Receive(BeAssignableToTypeOf(&challenges.Provider{})))
		Eventually(updates).Should(Receive(HaveField("Certificate.CertChain", Not(BeEmpty()))))
	})

	It("takes out of the challenge secret what the order left there", func() {
		table := challenges.NewTable()
		manager.SetChallenges(table)
		controller.copy.Metadata[challenges.RefMetadataKey] = challengeRef
		crt.Metadata[challenges.RefMetadataKey] = "challenges"
		expectController()
		expectSecretList(existingChallengeSecret(crt.GetUUID()))
		expectSecretCreates(challengeCreated)
		challengeWrites := make(chan []challenges.Challenge, 10)
		secretClient.EXPECT().UpdateSecret(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *svcsecret.UpdateSecretRequest) (*svcsecret.SecretResponse, error) {
				if req.GetUUID() == challengeSecretUuid {
					written := challenges.DecodeData(req.GetSecret().GetData())
					challengeWrites <- written
					go table.Replace(req.GetUUID(), written)
				}
				return &svcsecret.SecretResponse{Secret: req.GetSecret()}, nil
			}).AnyTimes()
		certPEM, keyPEM := freshLeafPEM("shop.example.com")
		obtainer.EXPECT().Obtain(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *params.CertRequest, _ legocert.ObtainRequest, solver challenge.Provider) (*legocert.Resource, error) {
				// an order whose CleanUp never reached the secret
				err := solver.Present(ctx, "shop.example.com", "tok-1", "ka-1")
				if err != nil {
					return nil, err
				}
				req.Registration, req.KeyPem = account()
				return &legocert.Resource{Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM)}, nil
			})

		Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

		Eventually(challengeWrites).Should(Receive(HaveLen(1)))
		Eventually(challengeWrites).Should(Receive(BeEmpty()))
		Eventually(updates).Should(Receive(HaveField("Certificate.CertChain", Not(BeEmpty()))))
	})

	Context("when the table is not set", func() {
		BeforeEach(func() {
			manager.SetChallenges(nil)
		})

		It("writes nothing for challenges", func() {
			expectController()
			creates := expectSecretCreates(func(req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
				return nil, fmt.Errorf("challenge secret %s created with no table", req.GetSecret().GetName())
			})
			solvers, writesBefore := expectOrder()

			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

			Eventually(solvers).Should(Receive(BeNil()))
			Expect(<-writesBefore).To(Equal(0))
			var written *svccert.UpdateCertificateRequest
			Eventually(updates).Should(Receive(&written))
			Expect(written.GetCertificate().GetMetadata()).NotTo(HaveKey(challenges.RefMetadataKey))
			Expect(creates).To(HaveLen(1))
		})
	})

	It("retries later without back-off when the challenge secret can't be made", func() {
		manager.SetRetry(2, 50*time.Millisecond)
		expectController()
		expectSecretList()
		var challengeCreates atomic.Int32
		expectSecretCreates(func(*svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
			challengeCreates.Add(1)
			return nil, connect.NewError(connect.CodeUnavailable, "controller down")
		})

		Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

		Eventually(challengeCreates.Load).Should(BeNumerically(">=", 2))
		Expect(updates).To(BeEmpty())
		Eventually(func() error {
			_, _, err := states.Get(helpers.TypeUrl[*cert.Certificate](), crt.GetUUID())
			return err
		}).Should(MatchError(ContainSubstring("controller down")))
	})

	It("fails on a secret holding the name of the challenge secret that is not one of ours, ordering nothing", func() {
		manager.SetRetry(2, 50*time.Millisecond)
		expectController()
		expectSecretList()
		expectSecretCreates(func(*svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
			return nil, connect.NewError(connect.CodeAlreadyExists, "name taken")
		})

		Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

		Eventually(func() error {
			_, _, err := states.Get(helpers.TypeUrl[*cert.Certificate](), crt.GetUUID())
			return err
		}).Should(MatchError(ContainSubstring(challengeSecretName(crt.GetUUID()))))
		Consistently(updates, 100*time.Millisecond).ShouldNot(Receive())
	})

	It("orders nothing when the certificate left the provisioner before the reference is written", func() {
		controller.editOnReread = map[string]string{"flock.provisioner.acme": "false"}
		expectController()
		expectSecretList()
		expectSecretCreates(challengeCreated)

		Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())

		Consistently(updates, 200*time.Millisecond).ShouldNot(Receive())
		// no retry is kept, the ADS update of the change orders it again or removes it
		Expect(manager.HasActivity(crt.GetUUID())).To(BeFalse())
	})

	Context("removed from the provisioner", func() {
		BeforeEach(func() {
			certPEM, keyPEM := freshLeafPEM("shop.example.com")
			crt.CertChain = certPEM
			crt.PrivateKey = keyPEM
			crt.Metadata["flock.acme.secret.ref"] = "secret-1"
			crt.Metadata[challenges.RefMetadataKey] = "challenges"
			secretClient.EXPECT().ReadSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).
				Return(&svcsecret.SecretResponse{Secret: &secret.Secret{
					UUID:     "secret-1",
					Metadata: map[string]string{"flock.acme.cert.ref": crt.GetUUID()},
				}}, nil)
		})

		JustBeforeEach(func() {
			Expect(manager.OnMessageAdded(ctx, crt)).To(Succeed())
		})

		It("detaches the challenge secret of a certificate that left acme", func() {
			certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
				Return(&svccert.CertificateResponse{Certificate: crt}, nil)
			expectSecretList(existingChallengeSecret(crt.GetUUID()))
			detached := map[string]*secret.Secret{}
			secretClient.EXPECT().UpdateSecret(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *svcsecret.UpdateSecretRequest) (*svcsecret.SecretResponse, error) {
					detached[req.GetUUID()] = req.GetSecret()
					return &svcsecret.SecretResponse{Secret: req.GetSecret()}, nil
				}).Times(2)

			Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())

			Expect(detached).To(HaveKey("secret-1"))
			Expect(detached).To(HaveKey(challengeSecretUuid))
			challengeMetadata := detached[challengeSecretUuid].GetMetadata()
			Expect(challengeMetadata).To(HaveKeyWithValue("flock.acme.secret.attached", "false"))
			Expect(challengeMetadata).To(HaveKeyWithValue(challenges.MetadataKey, "true"))
		})

		It("deletes the challenge secret of a certificate gone, counting one already deleted as cleaned", func() {
			certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
				Return(nil, connect.NewError(connect.CodeNotFound, "not found"))
			expectSecretList(existingChallengeSecret(crt.GetUUID()))
			secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).
				Return(&emptypb.Empty{}, nil)
			secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: challengeSecretUuid}).
				Return(nil, connect.NewError(connect.CodeNotFound, "not found"))

			Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())
		})

		Context("after http-01 and tls-alpn-01 were turned off", func() {
			BeforeEach(func() {
				manager.SetChallenges(nil)
			})

			It("still deletes the challenge secret of a certificate gone", func() {
				certClient.EXPECT().ReadCertificate(gomock.Any(), gomock.Any()).
					Return(nil, connect.NewError(connect.CodeNotFound, "not found"))
				expectSecretList(existingChallengeSecret(crt.GetUUID()))
				secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: "secret-1"}).
					Return(&emptypb.Empty{}, nil)
				secretClient.EXPECT().DeleteSecret(gomock.Any(), &svccore.UuidRequest{UUID: challengeSecretUuid}).
					Return(&emptypb.Empty{}, nil)

				Expect(manager.OnMessageRemove(ctx, crt)).To(Succeed())
			})
		})
	})
})
