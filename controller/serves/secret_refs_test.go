package serves_test

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// storedKind creates and updates one of the six kinds that can point at a secret, as an admin so only the reference
// checks can refuse the call
type storedKind struct {
	name   string
	create func(metadata map[string]string) (uuid string, err error)
	update func(uuid string, metadata map[string]string) error
}

var _ = Describe("Secret references in metadata", func() {
	var server *testServer
	var secretUuid string
	adminCtx := func() context.Context { return userCtx(adminUser) }

	BeforeEach(func() {
		server = newTestServer(instantiaters.NewNull())
		resp, err := server.CreateSecret(userCtx("bob"), &svcsecret.CreateSecretRequest{
			Secret: &secret.Secret{Name: "secret-1", Data: map[string]string{"token": "s3cr3t-token"}},
		})
		Expect(err).ToNot(HaveOccurred())
		secretUuid = resp.GetSecret().GetUUID()
	})
	calls := 0
	// uniqueName keeps names apart when a spec creates the same kind twice
	uniqueName := func(prefix string) string {
		calls++
		return fmt.Sprintf("%s-%d", prefix, calls)
	}
	reference := func(key string) map[string]string {
		return map[string]string{"auth": secret.RefPrefix + secretUuid + "/" + key}
	}

	kinds := []storedKind{
		{
			name: "backend",
			create: func(md map[string]string) (string, error) {
				created := newBackend(uniqueName("backend"))
				created.Metadata = md
				resp, err := server.CreateBackend(adminCtx(), &svcbackend.CreateBackendRequest{Backend: created})
				return resp.GetBackend().GetUUID(), err
			},
			update: func(uuid string, md map[string]string) error {
				stored, err := dbStoreMan.Backends().Read(context.Background(), uuid)
				Expect(err).ToNot(HaveOccurred())
				stored.Metadata = md
				_, err = server.UpdateBackend(adminCtx(), &svcbackend.UpdateBackendRequest{UUID: uuid, Backend: stored})
				return err
			},
		},
		{
			name: "virtual server",
			create: func(md map[string]string) (string, error) {
				backendResp, err := server.CreateBackend(adminCtx(), &svcbackend.CreateBackendRequest{Backend: newBackend(uniqueName("backend"))})
				Expect(err).ToNot(HaveOccurred())
				created := newVs(uniqueName("vs"), 8080, backendResp.GetBackend().GetUUID())
				created.Metadata = md
				resp, err := server.CreateVS(adminCtx(), &svcvs.CreateVSRequest{Vs: created})
				return resp.GetVs().GetUUID(), err
			},
			update: func(uuid string, md map[string]string) error {
				stored, err := dbStoreMan.VirtualServers().Read(context.Background(), uuid)
				Expect(err).ToNot(HaveOccurred())
				stored.Metadata = md
				_, err = server.UpdateVS(adminCtx(), &svcvs.UpdateVSRequest{UUID: uuid, Vs: stored})
				return err
			},
		},
		{
			name: "vip",
			create: func(md map[string]string) (string, error) {
				created := newVip(uniqueName("vip"))
				created.Metadata = md
				resp, err := server.CreateVIP(adminCtx(), &svcvip.CreateVIPRequest{Vip: created})
				return resp.GetVip().GetUUID(), err
			},
			update: func(uuid string, md map[string]string) error {
				stored, err := dbStoreMan.VIPs().Read(context.Background(), uuid)
				Expect(err).ToNot(HaveOccurred())
				stored.Metadata = md
				_, err = server.UpdateVIP(adminCtx(), &svcvip.UpdateVIPRequest{UUID: uuid, Vip: stored})
				return err
			},
		},
		{
			name: "certificate",
			create: func(md map[string]string) (string, error) {
				certPem, keyPem := generateCertPair()
				resp, err := server.CreateCertificate(adminCtx(), &svccert.CreateCertificateRequest{
					Certificate: &cert.Certificate{Name: uniqueName("cert"), CertChain: certPem, PrivateKey: keyPem, Ca: certPem, Metadata: md},
				})
				return resp.GetCertificate().GetUUID(), err
			},
			update: func(uuid string, md map[string]string) error {
				stored, err := dbStoreMan.Certificates().Read(context.Background(), uuid)
				Expect(err).ToNot(HaveOccurred())
				stored.Metadata = md
				_, err = server.UpdateCertificate(adminCtx(), &svccert.UpdateCertificateRequest{UUID: uuid, Certificate: stored})
				return err
			},
		},
		{
			name: "zone",
			create: func(md map[string]string) (string, error) {
				created := newZone(uniqueName("zone"))
				created.Metadata = md
				resp, err := server.CreateZone(adminCtx(), &svczone.CreateZoneRequest{Zone: created})
				return resp.GetZone().GetUUID(), err
			},
			update: func(uuid string, md map[string]string) error {
				stored, err := dbStoreMan.Zones().Read(context.Background(), uuid)
				Expect(err).ToNot(HaveOccurred())
				stored.Metadata = md
				_, err = server.UpdateZone(adminCtx(), &svczone.UpdateZoneRequest{UUID: uuid, Zone: stored})
				return err
			},
		},
		{
			name: "instance",
			create: func(md map[string]string) (string, error) {
				created := newInstance(uniqueName("inst"), server.createZone(uniqueName("zone")))
				created.Metadata = md
				resp, err := server.CreateInstance(adminCtx(), &svcinstance.CreateInstanceRequest{Instance: created})
				return resp.GetInstance().GetUUID(), err
			},
			update: func(uuid string, md map[string]string) error {
				stored, err := dbStoreMan.Instances().Read(context.Background(), uuid)
				Expect(err).ToNot(HaveOccurred())
				stored.Metadata = md
				_, err = server.UpdateInstance(adminCtx(), &svcinstance.UpdateInstanceRequest{UUID: uuid, Instance: stored})
				return err
			},
		},
	}
	DescribeTable("create", func(kind storedKind) {
		_, err := kind.create(reference("token"))
		Expect(err).ToNot(HaveOccurred())

		_, err = kind.create(reference("missing"))
		expectCode(err, connect.CodeInvalidArgument)
		Expect(err.Error()).To(ContainSubstring(`"auth"`))
	},
		Entry("backend", kinds[0]), Entry("virtual server", kinds[1]), Entry("vip", kinds[2]),
		Entry("certificate", kinds[3]), Entry("zone", kinds[4]), Entry("instance", kinds[5]),
	)

	DescribeTable("update", func(kind storedKind) {
		uuid, err := kind.create(nil)
		Expect(err).ToNot(HaveOccurred())

		err = kind.update(uuid, reference("missing"))
		expectCode(err, connect.CodeInvalidArgument)
		Expect(err.Error()).To(ContainSubstring(`"auth"`))

		Expect(kind.update(uuid, reference("token"))).To(Succeed())
	},
		Entry("backend", kinds[0]), Entry("virtual server", kinds[1]), Entry("vip", kinds[2]),
		Entry("certificate", kinds[3]), Entry("zone", kinds[4]), Entry("instance", kinds[5]),
	)

	Context("on a backend, as a user who owns it", func() {
		createBackend := func(user string, md map[string]string) (string, error) {
			created := newBackend(uniqueName("backend"))
			created.Metadata = md
			resp, err := server.CreateBackend(userCtx(user), &svcbackend.CreateBackendRequest{Backend: created})
			return resp.GetBackend().GetUUID(), err
		}
		updateBackend := func(user, uuid string, change func(*backend.Backend)) error {
			stored, err := dbStoreMan.Backends().Read(context.Background(), uuid)
			Expect(err).ToNot(HaveOccurred())
			change(stored)
			_, err = server.UpdateBackend(userCtx(user), &svcbackend.UpdateBackendRequest{UUID: uuid, Backend: stored})
			return err
		}

		It("refuses a malformed reference, naming the metadata key", func() {
			_, err := createBackend("alice", map[string]string{"auth": secret.RefPrefix + "u1"})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err.Error()).To(ContainSubstring(`"auth"`))
		})
		It("refuses an unknown secret, naming the metadata key", func() {
			_, err := createBackend(adminUser, map[string]string{"auth": secret.RefPrefix + "unknown/token"})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err.Error()).To(ContainSubstring(`metadata["auth"]`))
		})
		It("tells a caller with no role nothing about an unknown secret", func() {
			_, err := createBackend("alice", map[string]string{"auth": secret.RefPrefix + "unknown/token"})

			expectCode(err, connect.CodePermissionDenied)
			Expect(err.Error()).To(ContainSubstring("needs the reader role on it"))
		})
		It("refuses a caller with no role on the secret", func() {
			_, err := createBackend("alice", reference("token"))

			expectCode(err, connect.CodePermissionDenied)
			Expect(err.Error()).To(ContainSubstring(
				`metadata["auth"]: pointing at secret ` + secretUuid + " needs the reader role on it",
			))
		})
		It("accepts a reader of the secret", func() {
			err := server.perm.AddPermission(context.Background(), helpers.TypeUrl[*secret.Secret](), secretUuid, permission.Role_READER, permission.MemberType_USER, "alice")
			Expect(err).ToNot(HaveOccurred())

			_, err = createBackend("alice", reference("token"))

			Expect(err).ToNot(HaveOccurred())
		})
		It("accepts the owner of the secret", func() {
			_, err := createBackend("bob", reference("token"))

			Expect(err).ToNot(HaveOccurred())
		})
		It("lets a writer of the backend change another field and keeps the reference unchecked", func() {
			uuid, err := createBackend("bob", reference("token"))
			Expect(err).ToNot(HaveOccurred())
			Expect(server.perm.AddPermission(context.Background(),
				helpers.TypeUrl[*backend.Backend](), uuid, permission.Role_WRITER, permission.MemberType_USER, "alice",
			)).To(Succeed())

			err = updateBackend("alice", uuid, func(b *backend.Backend) { b.Sni = "changed.example.com" })

			Expect(err).ToNot(HaveOccurred())
		})
		It("checks a reference moved to another metadata key", func() {
			uuid, err := createBackend("bob", reference("token"))
			Expect(err).ToNot(HaveOccurred())
			Expect(server.perm.AddPermission(context.Background(),
				helpers.TypeUrl[*backend.Backend](), uuid, permission.Role_WRITER, permission.MemberType_USER, "alice",
			)).To(Succeed())

			err = updateBackend("alice", uuid, func(b *backend.Backend) {
				b.Metadata = map[string]string{"other": b.Metadata["auth"]}
			})

			expectCode(err, connect.CodePermissionDenied)
			Expect(err.Error()).To(ContainSubstring(`metadata["other"]`))
		})
	})
	Context("in the typed fields of a backend, as a user who owns it", func() {
		keyRef := func(key string) *flocktype.SecretKeyRef {
			return &flocktype.SecretKeyRef{SecretUuid: secretUuid, Key: key}
		}
		secretHeader := func(name, key string) *corev1.HeaderValue {
			return &corev1.HeaderValue{Key: name, ValueSource: &corev1.HeaderValue_ValueFromSecret{ValueFromSecret: keyRef(key)}}
		}
		edsBackend := func(headers []*corev1.HeaderValue, password *flocktype.SecretKeyRef) *backend.Backend {
			created := newBackend(uniqueName("backend"))
			userInfo := &corev1.URL_UserInfo{User: "u"}
			if password != nil {
				userInfo.PasswordSource = &corev1.URL_UserInfo_PasswordFromSecret{PasswordFromSecret: password}
			}
			created.Endpoint = &backend.Backend_Eds{Eds: &backend.EDS{
				Url:     &corev1.URL{Scheme: "http", Host: "vigie", UserInfo: userInfo},
				Headers: headers,
			}}
			return created
		}
		create := func(user string, created *backend.Backend) (string, error) {
			resp, err := server.CreateBackend(userCtx(user), &svcbackend.CreateBackendRequest{Backend: created})
			return resp.GetBackend().GetUUID(), err
		}
		updateEds := func(user, uuid string, change func(*backend.EDS)) error {
			stored, err := dbStoreMan.Backends().Read(context.Background(), uuid)
			Expect(err).ToNot(HaveOccurred())
			change(stored.GetEds())
			_, err = server.UpdateBackend(userCtx(user), &svcbackend.UpdateBackendRequest{UUID: uuid, Backend: stored})
			return err
		}
		giveWriter := func(uuid string) {
			Expect(server.perm.AddPermission(context.Background(),
				helpers.TypeUrl[*backend.Backend](), uuid, permission.Role_WRITER, permission.MemberType_USER, "alice",
			)).To(Succeed())
		}

		It("refuses a header pointing at a secret the caller can't read, naming the header", func() {
			_, err := create("alice", edsBackend([]*corev1.HeaderValue{secretHeader("x-token", "token")}, nil))

			expectCode(err, connect.CodePermissionDenied)
			Expect(err.Error()).To(ContainSubstring("eds.headers[0]: pointing at secret " + secretUuid))
		})
		It("refuses a password pointing at a missing key, naming the url", func() {
			_, err := create("bob", edsBackend(nil, keyRef("k")))

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err.Error()).To(ContainSubstring(fmt.Sprintf(`eds.url.user_info: secret %s has no key "k"`, secretUuid)))
		})
		It("accepts a header and a password from a secret the caller owns", func() {
			_, err := create("bob", edsBackend([]*corev1.HeaderValue{secretHeader("x-token", "token")}, keyRef("token")))

			Expect(err).ToNot(HaveOccurred())
		})
		It("lets a writer of the backend keep an unchanged header reference", func() {
			uuid, err := create("bob", edsBackend([]*corev1.HeaderValue{secretHeader("x-token", "token")}, nil))
			Expect(err).ToNot(HaveOccurred())
			giveWriter(uuid)

			err = updateEds("alice", uuid, func(eds *backend.EDS) { eds.ServiceName = "changed" })

			Expect(err).ToNot(HaveOccurred())
		})
		It("checks a header reference moved to another header", func() {
			uuid, err := create("bob", edsBackend([]*corev1.HeaderValue{secretHeader("x-token", "token")}, nil))
			Expect(err).ToNot(HaveOccurred())
			giveWriter(uuid)

			err = updateEds("alice", uuid, func(eds *backend.EDS) {
				plain := &corev1.HeaderValue{Key: "x-plain", ValueSource: &corev1.HeaderValue_Value{Value: "p"}}
				eds.Headers = []*corev1.HeaderValue{plain, eds.Headers[0]}
			})

			expectCode(err, connect.CodePermissionDenied)
			Expect(err.Error()).To(ContainSubstring("eds.headers[1]"))
		})
	})
})
