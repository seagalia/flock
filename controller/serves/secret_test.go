package serves_test

import (
	"context"

	"bytes"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/ArthurHlt/emitter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
)

// lockedBuffer lets the log hook of the server write while the spec reads
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var _ = Describe("Secret", func() {
	var server *testServer
	var logs *lockedBuffer
	BeforeEach(func() {
		logs = &lockedBuffer{}
		previous := log.Logger
		log.Logger = zerolog.New(logs)
		DeferCleanup(func() { log.Logger = previous })
		server = newTestServer(instantiaters.NewNull())
	})
	newSecret := func(name string) *secret.Secret {
		return &secret.Secret{
			Name: name,
			Data: map[string]string{"token": "s3cr3t-token", "password": "s3cr3t-password"},
		}
	}
	createSecret := func(owner string) string {
		GinkgoHelper()
		resp, err := server.CreateSecret(userCtx(owner), &svcsecret.CreateSecretRequest{Secret: newSecret("secret-1")})
		Expect(err).ToNot(HaveOccurred())
		return resp.GetSecret().GetUUID()
	}
	// pointingBackend stores a backend whose metadata points at key of the secret, as the handlers of the backends do
	pointingBackend := func(name, secretUuid, key string) string {
		GinkgoHelper()
		pointing := newBackend(name)
		pointing.UUID = name + "-uuid"
		pointing.Metadata = map[string]string{"auth": secret.RefPrefix + secretUuid + "/" + key}
		Expect(dbStoreMan.Backends().Create(context.Background(), pointing)).To(Succeed())
		return pointing.UUID
	}
	update := func(user, uuid string, data map[string]string) (*svcsecret.SecretResponse, error) {
		return server.UpdateSecret(userCtx(user), &svcsecret.UpdateSecretRequest{
			UUID: uuid, Secret: &secret.Secret{Name: "secret-1", Data: data},
		})
	}
	expectBlanked := func(data map[string]string) {
		GinkgoHelper()
		Expect(data).To(HaveLen(2))
		Expect(data).To(HaveKeyWithValue("token", ""))
		Expect(data).To(HaveKeyWithValue("password", ""))
	}

	Context("CreateSecret", func() {
		It("blanks the values it answers and keeps them in the store", func() {
			resp, err := server.CreateSecret(userCtx("bob"), &svcsecret.CreateSecretRequest{Secret: newSecret("secret-1")})
			Expect(err).ToNot(HaveOccurred())
			expectBlanked(resp.GetSecret().GetData())

			stored, err := dbStoreMan.Secrets().Read(context.Background(), resp.GetSecret().GetUUID())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetData()).To(HaveKeyWithValue("token", "s3cr3t-token"))
		})
		It("makes a caller who is not admin the owner", func() {
			uuid := createSecret("bob")

			Expect(server.perm.HasPermission(context.Background(), helpers.TypeUrl[*secret.Secret](), uuid, permission.Role_OWNER, "bob", nil)).To(BeTrue())
		})
		It("refuses a secret whose metadata points at a secret", func() {
			pointing := newSecret("secret-1")
			pointing.Metadata = map[string]string{"k": secret.RefPrefix + "uuid/key"}

			_, err := server.CreateSecret(userCtx("bob"), &svcsecret.CreateSecretRequest{Secret: pointing})

			expectCode(err, connect.CodeInvalidArgument)
		})
		It("answers AlreadyExists for a name already taken", func() {
			createSecret("bob")

			_, err := server.CreateSecret(userCtx("bob"), &svcsecret.CreateSecretRequest{Secret: newSecret("secret-1")})

			expectCode(err, connect.CodeAlreadyExists)
		})
	})

	Context("ReadSecret and ListSecret", func() {
		It("blank the values and keep the keys", func() {
			uuid := createSecret("bob")

			read, err := server.ReadSecret(userCtx("bob"), &svccore.UuidRequest{UUID: uuid})
			Expect(err).ToNot(HaveOccurred())
			expectBlanked(read.GetSecret().GetData())

			list, err := server.ListSecret(userCtx("bob"), &svcsecret.ListSecretRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(list.GetSecrets()).To(HaveLen(1))
			expectBlanked(list.GetSecrets()[0].GetData())
		})
		It("lets a reader read and refuses a user with no role", func() {
			uuid := createSecret("bob")
			_, err := server.AddPermission(userCtx("bob"), &svcperm.AddPermissionRequest{
				ResourceType: helpers.TypeUrl[*secret.Secret](), ResourceUuid: uuid, Role: permission.Role_READER, Member: user("alice"),
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.ReadSecret(userCtx("alice"), &svccore.UuidRequest{UUID: uuid})
			Expect(err).ToNot(HaveOccurred())
			_, err = server.ReadSecret(userCtx("eve"), &svccore.UuidRequest{UUID: uuid})
			expectCode(err, connect.CodePermissionDenied)
		})
	})

	Context("UpdateSecret", func() {
		It("keeps the stored value of a key sent empty and removes a key left out", func() {
			uuid := createSecret("bob")

			resp, err := update("bob", uuid, map[string]string{"token": "", "extra": "new-value"})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetSecret().GetData()).To(HaveKeyWithValue("token", ""))
			Expect(resp.GetSecret().GetData()).To(HaveKeyWithValue("extra", ""))

			stored, err := dbStoreMan.Secrets().Read(context.Background(), uuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetData()).To(Equal(map[string]string{"token": "s3cr3t-token", "extra": "new-value"}))
		})
		It("refuses a new key with an empty value", func() {
			uuid := createSecret("bob")

			_, err := update("bob", uuid, map[string]string{"token": "", "fresh": ""})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err.Error()).To(ContainSubstring(`key "fresh" needs a value`))
		})
		It("refuses to remove a key a backend points at, naming the backend", func() {
			uuid := createSecret("bob")
			pointingBackend("backend-1", uuid, "password")

			_, err := update("bob", uuid, map[string]string{"token": ""})

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(err.Error()).To(ContainSubstring(`key "password" of secret ` + uuid + " is used by backend backend-1"))
		})
		It("removes a key no resource points at while another key is used", func() {
			uuid := createSecret("bob")
			pointingBackend("backend-1", uuid, "token")

			_, err := update("bob", uuid, map[string]string{"token": ""})

			Expect(err).ToNot(HaveOccurred())
		})
		It("lets a writer update and asks the owner role for a name change", func() {
			uuid := createSecret("bob")
			_, err := server.AddPermission(userCtx("bob"), &svcperm.AddPermissionRequest{
				ResourceType: helpers.TypeUrl[*secret.Secret](), ResourceUuid: uuid, Role: permission.Role_WRITER, Member: user("alice"),
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = update("alice", uuid, map[string]string{"token": "", "password": ""})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.UpdateSecret(userCtx("alice"), &svcsecret.UpdateSecretRequest{
				UUID: uuid, Secret: &secret.Secret{Name: "renamed", Data: map[string]string{"token": ""}},
			})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("refuses a secret whose metadata points at a secret", func() {
			uuid := createSecret("bob")

			_, err := server.UpdateSecret(userCtx("bob"), &svcsecret.UpdateSecretRequest{
				UUID: uuid,
				Secret: &secret.Secret{
					Name: "secret-1", Data: map[string]string{"token": ""},
					Metadata: map[string]string{"k": secret.RefPrefix + uuid + "/token"},
				},
			})

			expectCode(err, connect.CodeInvalidArgument)
		})
	})

	Context("RevealSecret", func() {
		It("gives the values to the owner and logs the call without them", func() {
			uuid := createSecret("bob")

			resp, err := server.RevealSecret(userCtx("bob"), &svccore.UuidRequest{UUID: uuid})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetSecret().GetData()).To(Equal(map[string]string{"token": "s3cr3t-token", "password": "s3cr3t-password"}))
			Expect(logs.String()).To(ContainSubstring(`"message":"secret revealed"`))
			Expect(logs.String()).To(ContainSubstring(`"level":"info"`))
			Expect(logs.String()).To(ContainSubstring(`"username":"bob"`))
			Expect(logs.String()).To(ContainSubstring(`"secret_uuid":"` + uuid + `"`))
			Expect(logs.String()).To(ContainSubstring(`"secret_name":"secret-1"`))
			Expect(logs.String()).ToNot(ContainSubstring("s3cr3t-"))
		})
		It("is refused to a writer and logs nothing", func() {
			uuid := createSecret("bob")
			_, err := server.AddPermission(userCtx("bob"), &svcperm.AddPermissionRequest{
				ResourceType: helpers.TypeUrl[*secret.Secret](), ResourceUuid: uuid, Role: permission.Role_WRITER, Member: user("alice"),
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.RevealSecret(userCtx("alice"), &svccore.UuidRequest{UUID: uuid})

			expectCode(err, connect.CodePermissionDenied)
			Expect(logs.String()).ToNot(ContainSubstring("secret revealed"))
		})
	})

	Context("DeleteSecret", func() {
		It("is refused while a backend points at the secret, then works once the reference is gone", func() {
			uuid := createSecret("bob")
			backendUuid := pointingBackend("backend-1", uuid, "token")

			_, err := server.DeleteSecret(userCtx("bob"), &svccore.UuidRequest{UUID: uuid})
			expectCode(err, connect.CodeFailedPrecondition)
			Expect(err.Error()).To(ContainSubstring("secret " + uuid + " is used by backend backend-1"))

			unreferenced := newBackend("backend-1")
			Expect(dbStoreMan.Backends().Update(context.Background(), backendUuid, unreferenced)).To(Succeed())
			_, err = server.DeleteSecret(userCtx("bob"), &svccore.UuidRequest{UUID: uuid})
			Expect(err).ToNot(HaveOccurred())
			exists, err := dbStoreMan.Secrets().Exists(context.Background(), uuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeFalse())
		})
		It("is refused while the eds password of a backend points at the secret", func() {
			uuid := createSecret("bob")
			pointing := newBackend("backend-2")
			pointing.UUID = "backend-2-uuid"
			pointing.Endpoint = &backend.Backend_Eds{Eds: &backend.EDS{Url: &corev1.URL{
				Scheme: "http", Host: "vigie", UserInfo: &corev1.URL_UserInfo{
					User: "u",
					PasswordSource: &corev1.URL_UserInfo_PasswordFromSecret{
						PasswordFromSecret: &flocktype.SecretKeyRef{SecretUuid: uuid, Key: "token"},
					},
				},
			}}}
			Expect(dbStoreMan.Backends().Create(context.Background(), pointing)).To(Succeed())

			_, err := server.DeleteSecret(userCtx("bob"), &svccore.UuidRequest{UUID: uuid})

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(err.Error()).To(ContainSubstring("secret " + uuid + " is used by backend backend-2"))
		})
		It("is refused to a writer", func() {
			uuid := createSecret("bob")
			_, err := server.AddPermission(userCtx("bob"), &svcperm.AddPermissionRequest{
				ResourceType: helpers.TypeUrl[*secret.Secret](), ResourceUuid: uuid, Role: permission.Role_WRITER, Member: user("alice"),
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.DeleteSecret(userCtx("alice"), &svccore.UuidRequest{UUID: uuid})

			expectCode(err, connect.CodePermissionDenied)
		})
	})

	Context("observer events", func() {
		It("carry the signature of the blanked secret", func() {
			var mu sync.Mutex
			var subjects []*observer.Subject
			observe.OnAll(observe.ListenerFuncSubject(func(event *emitter.EventOf[*observer.Subject]) {
				subject := event.TypedSubject()
				mu.Lock()
				defer mu.Unlock()
				if subject.GetTypeUrl() == helpers.TypeUrl[*secret.Secret]() {
					subjects = append(subjects, subject)
				}
			}))
			uuid := createSecret("bob")
			_, err := update("bob", uuid, map[string]string{"token": "another-value", "password": ""})
			Expect(err).ToNot(HaveOccurred())

			stored, err := dbStoreMan.Secrets().Read(context.Background(), uuid)
			Expect(err).ToNot(HaveOccurred())
			expected, err := helpers.MessageSignature(stored.Blanked())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func() int {
				mu.Lock()
				defer mu.Unlock()
				return len(subjects)
			}).Should(Equal(2))
			mu.Lock()
			defer mu.Unlock()
			for _, subject := range subjects {
				Expect(subject.GetSignature()).To(Equal(expected))
			}
		})
	})

})
