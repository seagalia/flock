//go:build e2e

package e2e_test

import (
	"bytes"
	"io"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
)

const (
	secretValue  = "e2e-very-secret"
	secretKey    = "token"
	sqliteDbPath = "/tmp/flock.db"
)

var secretType = helpers.TypeUrl[*secret.Secret]()

// Sqlite because the spec reads the store file out of the container. The users are the OIDC ones so that a second
// user without any role exists. Zones and instances are admin only, so the admin builds the chain.
var _ = Describe("Secret", Ordered, func() {
	var (
		g                 *env.Controller
		admin, alice, bob *env.Api

		secretUuid  string
		backendUuid string
		backendName string
		reference   string
	)

	BeforeAll(func(ctx SpecContext) {
		provider, err := e2eEnv.StartOauth2(ctx)
		Expect(err).NotTo(HaveOccurred())
		g = startController(ctx, env.ControllerOptions{
			Name: "controller-secret", Store: env.StoreSqlite, Oidc: provider, Instantiators: env.SubscriberInstantiators(),
		})
		admin = loginAdmin(ctx, g)
		alice = loginOidc(ctx, g, provider, "alice")
		bob = loginOidc(ctx, g, provider, "bob")
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(g)
	})

	It("keeps the values out of read and list answers", func(ctx SpecContext) {
		created, err := alice.Secret.CreateSecret(ctx, &svcsecret.CreateSecretRequest{Secret: &secret.Secret{
			Name: env.Unique("secret"),
			Data: map[string]string{secretKey: secretValue},
		}})
		Expect(err).NotTo(HaveOccurred())
		secretUuid = created.GetSecret().GetUUID()
		reference = secret.Ref{SecretUUID: secretUuid, Key: secretKey}.String()
		Expect(created.GetSecret().GetData()).To(Equal(map[string]string{secretKey: ""}))

		read, err := alice.Secret.ReadSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
		Expect(err).NotTo(HaveOccurred())
		Expect(read.GetSecret().GetData()).To(Equal(map[string]string{secretKey: ""}))

		listed, err := alice.Secret.ListSecret(ctx, &svcsecret.ListSecretRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.GetSecrets()).To(ContainElement(HaveField("UUID", secretUuid)))
		for _, listedSecret := range listed.GetSecrets() {
			Expect(listedSecret.GetData()).To(HaveEach(BeEmpty()))
		}
	}, NodeTimeout(time.Minute))

	It("reveals the values to the owner only", func(ctx SpecContext) {
		revealed, err := alice.Secret.RevealSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
		Expect(err).NotTo(HaveOccurred())
		Expect(revealed.GetSecret().GetData()).To(Equal(map[string]string{secretKey: secretValue}))

		_, err = bob.Secret.RevealSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
	}, NodeTimeout(time.Minute))

	It("lets a reader of the secret point a backend at it, and only a reader", func(ctx SpecContext) {
		bobBackend := env.NewBackend(env.Unique("backend"))
		bobBackend.Metadata = map[string]string{"auth": reference}
		_, err := bob.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: bobBackend})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		grant(ctx, alice, secretType, secretUuid, permission.Role_READER, user("bob"))
		created, err := bob.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: bobBackend})
		Expect(err).NotTo(HaveOccurred())
		Expect(created.GetBackend().GetMetadata()).To(HaveKeyWithValue("auth", reference))

		_, err = bob.Secret.RevealSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		_, err = bob.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: created.GetBackend().GetUUID()})
		Expect(err).NotTo(HaveOccurred())
	}, NodeTimeout(time.Minute))

	It("sends the secret to the agent of an instance whose backend points at it", func(ctx SpecContext) {
		backendWithRef := env.NewBackend(env.Unique("backend"))
		backendWithRef.Metadata = map[string]string{"auth": reference}
		createdBackend, err := admin.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: backendWithRef})
		Expect(err).NotTo(HaveOccurred())
		backendUuid = createdBackend.GetBackend().GetUUID()
		backendName = backendWithRef.GetName()

		vsResp, err := admin.VS.CreateVS(ctx, &svcvs.CreateVSRequest{Vs: env.NewTcpVs(env.Unique("vs"), 8001, backendUuid)})
		Expect(err).NotTo(HaveOccurred())
		zone := subscriberZone(ctx, admin)
		instanceUuid, _ := createInstance(ctx, admin, zone, vsResp.GetVs().GetUUID())

		subscriber := connectSubscriber(ctx, g, instanceUuid)
		Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(backendUuid))
		// the subscriber asks for the secret type url on its own, the agent SDK does it for the secrets it needs
		Expect(subscriber.Connect(ctx, g.Addr(ctx), append(fakeagent.SubscriberTypeUrls, secretType)...)).To(Succeed())
		Eventually(heldUuids(subscriber, secretType)).Should(ConsistOf(secretUuid))
		held := &secret.Secret{}
		Expect(subscriber.State(secretType)[secretUuid].ToMessage(held)).To(Succeed())
		Expect(held.GetData()).To(Equal(map[string]string{secretKey: secretValue}))
	}, NodeTimeout(2*time.Minute))

	It("stores the value encrypted", func(ctx SpecContext) {
		for _, file := range []string{sqliteDbPath, sqliteDbPath + "-wal"} {
			reader, err := g.Container.CopyFileFromContainer(ctx, file)
			if err != nil {
				continue
			}
			content, err := io.ReadAll(reader)
			Expect(err).NotTo(HaveOccurred())
			reader.Close()
			Expect(content).NotTo(BeEmpty())
			Expect(bytes.Contains(content, []byte(secretValue))).To(BeFalse(), "%s holds the secret value in clear", file)
		}
		// the main file must have been read while the secret is stored, else the check above proves nothing
		reader, err := g.Container.CopyFileFromContainer(ctx, sqliteDbPath)
		Expect(err).NotTo(HaveOccurred())
		defer reader.Close()
		content, err := io.ReadAll(reader)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Contains(content, []byte("CREATE TABLE"))).To(BeTrue())
	}, NodeTimeout(time.Minute))
	It("refuses to delete a referenced secret, then deletes it once nothing points at it", func(ctx SpecContext) {
		_, err := alice.Secret.DeleteSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)
		Expect(err.Error()).To(ContainSubstring(backendName))

		stored, err := admin.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(err).NotTo(HaveOccurred())
		updated := stored.GetBackend()
		updated.Metadata = nil
		_, err = admin.Backend.UpdateBackend(ctx, &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: updated})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			_, err := alice.Secret.DeleteSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
			return err
		}).Should(Succeed())
	}, NodeTimeout(time.Minute))
})
