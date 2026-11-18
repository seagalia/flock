package dbstore_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
	. "github.com/seagalia/flock/controller/dbstore"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

type namedMessage interface {
	GetName() string
}

func names[T namedMessage](messages []T) []string {
	result := make([]string, len(messages))
	for i, m := range messages {
		result[i] = m.GetName()
	}
	return result
}

var _ = Describe("DbStore", func() {
	Context("CRUD", func() {
		var backends storers.Storer[*backend.Backend]
		BeforeEach(func() {
			backends = storeMan.Backends()
			err := backends.Create(context.Background(), &backend.Backend{UUID: "uuid-1", Name: "backend-1", Sni: "sni-1"})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should read created message", func() {
			read, err := backends.Read(context.Background(), "uuid-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetName()).To(Equal("backend-1"))
			Expect(read.GetSni()).To(Equal("sni-1"))
		})
		It("should tell if message exists", func() {
			Expect(backends.Exists(context.Background(), "uuid-1")).To(BeTrue())
			Expect(backends.Exists(context.Background(), "unknown")).To(BeFalse())
		})
		It("should find message by name", func() {
			found, err := backends.(storers.StorerFindName[*backend.Backend]).FindByName(context.Background(), "backend-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(found.GetUUID()).To(Equal("uuid-1"))
		})
		It("should refuse to create message with same uuid or name", func() {
			err := backends.Create(context.Background(), &backend.Backend{UUID: "uuid-1", Name: "other"})
			Expect(errors.IsErrAlreadyExists(err)).To(BeTrue())
			err = backends.Create(context.Background(), &backend.Backend{UUID: "uuid-2", Name: "backend-1"})
			Expect(errors.IsErrAlreadyExists(err)).To(BeTrue())
		})
		It("should update message", func() {
			err := backends.Update(context.Background(), "uuid-1", &backend.Backend{Name: "renamed", Sni: "sni-2"})
			Expect(err).ToNot(HaveOccurred())

			read, err := backends.Read(context.Background(), "uuid-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetUUID()).To(Equal("uuid-1"))
			Expect(read.GetName()).To(Equal("renamed"))
			Expect(read.GetSni()).To(Equal("sni-2"))
		})
		It("should delete message", func() {
			Expect(backends.Delete(context.Background(), "uuid-1")).To(Succeed())
			Expect(backends.Exists(context.Background(), "uuid-1")).To(BeFalse())
		})
		Context("with a cancelled ctx", func() {
			var cancelled context.Context
			BeforeEach(func() {
				var cancel context.CancelFunc
				cancelled, cancel = context.WithCancel(context.Background())
				cancel()
			})

			It("should stop a read", func() {
				_, err := backends.Read(cancelled, "uuid-1")
				Expect(err).To(MatchError(context.Canceled))
				Expect(errors.IsErrNotFound(err)).To(BeFalse())
			})
			It("should stop a list", func() {
				_, err := backends.List(cancelled, nil)
				Expect(err).To(MatchError(context.Canceled))
			})
			It("should stop a create and store nothing", func() {
				err := backends.Create(cancelled, &backend.Backend{UUID: "uuid-2", Name: "backend-2"})
				Expect(err).To(MatchError(context.Canceled))
				Expect(backends.Exists(context.Background(), "uuid-2")).To(BeFalse())
			})
		})
		It("should give not found errors on unknown message", func() {
			_, err := backends.Read(context.Background(), "unknown")
			Expect(errors.IsErrNotFound(err)).To(BeTrue())

			_, err = backends.(storers.StorerFindName[*backend.Backend]).FindByName(context.Background(), "unknown")
			Expect(errors.IsErrNotFound(err)).To(BeTrue())

			err = backends.Update(context.Background(), "unknown", &backend.Backend{Name: "unknown"})
			Expect(errors.IsErrNotFound(err)).To(BeTrue())
		})
		It("should list light information", func() {
			light, err := backends.(LightStorer).LightList(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(light).To(Equal(map[string]string{"uuid-1": "backend-1"}))
		})
	})

	Context("List", func() {
		var backends storers.Storer[*backend.Backend]
		BeforeEach(func() {
			backends = storeMan.Backends()
			for _, c := range []*backend.Backend{
				{UUID: "uuid-a", Name: "alpha", Metadata: map[string]string{"env": "prod", "team": "net"}},
				{UUID: "uuid-b", Name: "beta", Metadata: map[string]string{"env": "dev", "team": "net"}},
				{UUID: "uuid-c", Name: "gamma", Metadata: map[string]string{"env": "prod"}},
			} {
				Expect(backends.Create(context.Background(), c)).To(Succeed())
			}
		})

		It("should list all messages", func() {
			list, err := backends.List(context.Background(), nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha", "beta", "gamma"))
		})
		It("should paginate", func() {
			list, err := backends.List(context.Background(), &storers.ListOptions{Limit: 2})
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(HaveLen(2))

			list, err = backends.List(context.Background(), &storers.ListOptions{Limit: 2, Offset: 2})
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(HaveLen(1))
		})
		DescribeTable("should filter",
			func(filter *svccore.ListFilter, expectedNames ...string) {
				list, err := backends.List(context.Background(), nil, filter)
				Expect(err).ToNot(HaveOccurred())
				Expect(names(list)).To(ConsistOf(expectedNames))
			},
			Entry("name equal", &svccore.ListFilter{Key: "name", Operator: svccore.ListFilter_EQUAL, Value: "beta"}, "beta"),
			Entry("name not equal", &svccore.ListFilter{Key: "name", Operator: svccore.ListFilter_NOT_EQUAL, Value: "beta"}, "alpha", "gamma"),
			Entry("name like", &svccore.ListFilter{Key: "name", Operator: svccore.ListFilter_LIKE, Value: "mm"}, "gamma"),
			Entry("name not like", &svccore.ListFilter{Key: "name", Operator: svccore.ListFilter_NOT_LIKE, Value: "a"}),
			Entry("uuid equal", &svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-a"}, "alpha"),
			Entry("uuid not equal", &svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_NOT_EQUAL, Value: "uuid-a"}, "beta", "gamma"),
			Entry("uuid like", &svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_LIKE, Value: "-c"}, "gamma"),
			Entry("uuid not like", &svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_NOT_LIKE, Value: "-c"}, "alpha", "beta"),
			Entry("metadata equal", &svccore.ListFilter{IsMetadata: true, Key: "env", Operator: svccore.ListFilter_EQUAL, Value: "prod"}, "alpha", "gamma"),
			Entry("metadata not equal", &svccore.ListFilter{IsMetadata: true, Key: "env", Operator: svccore.ListFilter_NOT_EQUAL, Value: "prod"}, "beta"),
			Entry("metadata like", &svccore.ListFilter{IsMetadata: true, Key: "env", Operator: svccore.ListFilter_LIKE, Value: "de"}, "beta"),
			Entry("metadata not like", &svccore.ListFilter{IsMetadata: true, Key: "env", Operator: svccore.ListFilter_NOT_LIKE, Value: "de"}, "alpha", "gamma"),
		)
		It("should match all metadata filters", func() {
			list, err := backends.List(context.Background(), nil,
				&svccore.ListFilter{IsMetadata: true, Key: "env", Operator: svccore.ListFilter_EQUAL, Value: "prod"},
				&svccore.ListFilter{IsMetadata: true, Key: "team", Operator: svccore.ListFilter_EQUAL, Value: "net"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha"))
		})
		It("should match any of several uuid filters", func() {
			list, err := backends.List(context.Background(), nil,
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-a"},
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-c"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha", "gamma"))
		})
		It("should match any of several name and uuid filters", func() {
			list, err := backends.List(context.Background(), nil,
				&svccore.ListFilter{Key: "name", Operator: svccore.ListFilter_EQUAL, Value: "alpha"},
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-b"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha", "beta"))
		})
		It("should match name and uuid filters along with every metadata filter", func() {
			list, err := backends.List(context.Background(), nil,
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-a"},
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-b"},
				&svccore.ListFilter{IsMetadata: true, Key: "env", Operator: svccore.ListFilter_EQUAL, Value: "prod"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha"))
		})
		It("should keep the permissions of the user over several uuid filters", func() {
			backendStore := backends.(*DbStore[*backend.Backend])
			Expect(backendStore.AddPermission(context.Background(), "uuid-a", permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())

			list, err := backends.List(context.Background(), &storers.ListOptions{UserName: "bob"},
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-a"},
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "uuid-b"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha"))
		})
		It("should follow metadata changes on update and delete", func() {
			err := backends.Update(context.Background(), "uuid-a", &backend.Backend{Name: "alpha", Metadata: map[string]string{"env": "dev"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(backends.Delete(context.Background(), "uuid-b")).To(Succeed())

			list, err := backends.List(context.Background(), nil, &svccore.ListFilter{IsMetadata: true, Key: "env", Operator: svccore.ListFilter_EQUAL, Value: "dev"})
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha"))

			list, err = backends.List(context.Background(), nil, &svccore.ListFilter{IsMetadata: true, Key: "team", Operator: svccore.ListFilter_EQUAL, Value: "net"})
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(BeEmpty())
		})
		It("should only list messages user or its groups can read", func() {
			backendStore := backends.(*DbStore[*backend.Backend])
			Expect(backendStore.AddPermission(context.Background(), "uuid-a", permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())
			Expect(backendStore.AddPermission(context.Background(), "uuid-b", permission.Role_OWNER, permission.MemberType_GROUP, "team")).To(Succeed())

			list, err := backends.List(context.Background(), &storers.ListOptions{UserName: "bob", UserGroups: []string{"team"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("alpha", "beta"))

			list, err = backends.List(context.Background(), &storers.ListOptions{UserName: "alice", UserGroups: []string{"other"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(BeEmpty())
		})
	})

	Context("Foreign resources", func() {
		var vips storers.Storer[*vip.VIP]
		vsTypeUrl := helpers.TypeUrl[*vs.VirtualServer]()
		BeforeEach(func() {
			vips = storeMan.VIPs()
			Expect(vips.Create(context.Background(), &vip.VIP{UUID: "vip-1", Name: "vip-1", Ip: "10.0.0.1", VirtualServersUuid: []string{"vs-1", "vs-2"}})).To(Succeed())
			Expect(vips.Create(context.Background(), &vip.VIP{UUID: "vip-2", Name: "vip-2", Ip: "10.0.0.2", VirtualServersUuid: []string{"vs-2"}})).To(Succeed())
		})

		It("should find foreign resources of a source", func() {
			found, err := vips.FindForeignBySrc(context.Background(), "vip-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(HaveKey(vsTypeUrl))
			Expect(found[vsTypeUrl]).To(ConsistOf("vs-1", "vs-2"))
		})
		It("should find sources using a foreign resource", func() {
			found, err := vips.FindSrcWithForeignResource(context.Background(), vsTypeUrl, "vs-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(ConsistOf("vip-1", "vip-2"))
		})
		It("should find all foreign resources of a type by source", func() {
			found, err := vips.FindForeignResources(context.Background(), vsTypeUrl)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(HaveLen(2))
			Expect(found["vip-1"]).To(ConsistOf("vs-1", "vs-2"))
			Expect(found["vip-2"]).To(ConsistOf("vs-2"))
		})
		It("should follow foreign resources changes on update", func() {
			err := vips.Update(context.Background(), "vip-1", &vip.VIP{Name: "vip-1", Ip: "10.0.0.1", VirtualServersUuid: []string{"vs-2", "vs-3"}})
			Expect(err).ToNot(HaveOccurred())

			found, err := vips.FindForeignBySrc(context.Background(), "vip-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(found[vsTypeUrl]).To(ConsistOf("vs-2", "vs-3"))
		})
		It("should remove foreign resources on delete", func() {
			Expect(vips.Delete(context.Background(), "vip-1")).To(Succeed())

			found, err := vips.FindSrcWithForeignResource(context.Background(), vsTypeUrl, "vs-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(ConsistOf("vip-2"))
		})
		It("should filter list by foreign resource in both directions", func() {
			Expect(storeMan.Instances().Create(context.Background(), &instance.Instance{UUID: "inst-1", Name: "inst-1", VipsUuid: []string{"vip-2"}})).To(Succeed())

			list, err := vips.List(context.Background(), nil, &svccore.ListFilter{Key: vsTypeUrl, Operator: svccore.ListFilter_EQUAL, Value: "vs-1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("vip-1"))

			list, err = vips.List(context.Background(), nil, &svccore.ListFilter{Key: helpers.TypeUrl[*instance.Instance](), Operator: svccore.ListFilter_EQUAL, Value: "inst-1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("vip-2"))
		})
		It("should match uuid filters along with a foreign resource filter", func() {
			list, err := vips.List(context.Background(), nil,
				&svccore.ListFilter{Key: vsTypeUrl, Operator: svccore.ListFilter_EQUAL, Value: "vs-2"},
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "vip-1"},
				&svccore.ListFilter{Key: "uuid", Operator: svccore.ListFilter_EQUAL, Value: "vip-3"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(names(list)).To(ConsistOf("vip-1"))
		})
		It("should give no foreign resources for type without foreign", func() {
			Expect(storeMan.Backends().Create(context.Background(), &backend.Backend{UUID: "c-1", Name: "c-1"})).To(Succeed())

			found, err := storeMan.Backends().FindForeignBySrc(context.Background(), "c-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeEmpty())

			srcs, err := storeMan.Backends().FindSrcWithForeignResource(context.Background(), vsTypeUrl, "vs-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(srcs).To(BeEmpty())

			all, err := storeMan.Backends().FindForeignResources(context.Background(), vsTypeUrl)
			Expect(err).ToNot(HaveOccurred())
			Expect(all).To(BeEmpty())
		})
	})

	Context("Encrypted store", func() {
		It("should store certificates encrypted and read them in clear", func() {
			certificate := &cert.Certificate{UUID: "cert-1", Name: "cert-1", PrivateKey: "private-key"}
			Expect(storeMan.Certificates().Create(context.Background(), certificate)).To(Succeed())

			var rawData []byte
			err := dbMan.QueryRow(context.Background(), dbs.MakeDbQuery("SELECT data FROM Certificate WHERE uuid = ?", []any{"cert-1"})).Scan(&rawData)
			Expect(err).ToNot(HaveOccurred())
			clearData, err := proto.Marshal(certificate)
			Expect(err).ToNot(HaveOccurred())
			Expect(rawData).ToNot(Equal(clearData))

			read, err := storeMan.Certificates().Read(context.Background(), "cert-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetPrivateKey()).To(Equal("private-key"))

			list, err := storeMan.Certificates().List(context.Background(), nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].GetPrivateKey()).To(Equal("private-key"))
		})

		It("should store secrets encrypted", func() {
			Expect(storeMan.Secrets().Create(context.Background(), &secret.Secret{
				UUID: "secret-1", Name: "secret-1", Data: map[string]string{"token": "s3cr3t-value"},
			})).To(Succeed())

			var rawData []byte
			err := dbMan.QueryRow(context.Background(), dbs.MakeDbQuery("SELECT data FROM Secret WHERE uuid = ?", []any{"secret-1"})).Scan(&rawData)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(rawData)).ToNot(ContainSubstring("s3cr3t-value"))

			read, err := storeMan.Secrets().Read(context.Background(), "secret-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetData()).To(HaveKeyWithValue("token", "s3cr3t-value"))
		})
		It("should sign an encrypted resource with its encrypted bytes", func() {
			stored := &secret.Secret{UUID: "secret-1", Name: "secret-1", Data: map[string]string{"token": "s3cr3t-value"}}
			Expect(storeMan.Secrets().Create(context.Background(), stored)).To(Succeed())
			signatureOf := func() string {
				var signature string
				err := dbMan.QueryRow(context.Background(), dbs.MakeDbQuery("SELECT signature FROM Secret WHERE uuid = ?", []any{"secret-1"})).Scan(&signature)
				Expect(err).ToNot(HaveOccurred())
				return signature
			}
			plainSignature, err := helpers.MessageSignature(stored)
			Expect(err).ToNot(HaveOccurred())
			createdSignature := signatureOf()
			Expect(createdSignature).ToNot(Equal(plainSignature))

			Expect(storeMan.Secrets().Update(context.Background(), "secret-1", stored)).To(Succeed())

			Expect(signatureOf()).ToNot(Equal(createdSignature))
		})
	})

	Context("Secret references", func() {
		secretTypeUrl := helpers.TypeUrl[*secret.Secret]()

		It("should link a resource to the secrets its metadata points at", func() {
			backends := storeMan.Backends()
			Expect(backends.Create(context.Background(), &backend.Backend{
				UUID: "backend-1", Name: "backend-1", Metadata: map[string]string{"tok": "secret://secret-1/token"},
			})).To(Succeed())

			srcs, err := backends.FindSrcWithForeignResource(context.Background(), secretTypeUrl, "secret-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(srcs).To(ConsistOf("backend-1"))

			Expect(backends.Update(context.Background(), "backend-1", &backend.Backend{
				Name: "backend-1", Metadata: map[string]string{"other": "plain"},
			})).To(Succeed())

			srcs, err = backends.FindSrcWithForeignResource(context.Background(), secretTypeUrl, "secret-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(srcs).To(BeEmpty())
		})
		It("should move the link when the reference changes", func() {
			backends := storeMan.Backends()
			Expect(backends.Create(context.Background(), &backend.Backend{
				UUID: "backend-1", Name: "backend-1", Metadata: map[string]string{"tok": "secret://secret-1/token"},
			})).To(Succeed())

			Expect(backends.Update(context.Background(), "backend-1", &backend.Backend{
				Name: "backend-1", Metadata: map[string]string{"tok": "secret://secret-2/token"},
			})).To(Succeed())

			srcs, err := backends.FindSrcWithForeignResource(context.Background(), secretTypeUrl, "secret-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(srcs).To(BeEmpty())
			srcs, err = backends.FindSrcWithForeignResource(context.Background(), secretTypeUrl, "secret-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(srcs).To(ConsistOf("backend-1"))
		})
		It("should link a certificate and a zone too", func() {
			metadata := map[string]string{"tok": "secret://secret-1/token"}
			Expect(storeMan.Certificates().Create(context.Background(), &cert.Certificate{UUID: "cert-1", Name: "cert-1", Metadata: metadata})).To(Succeed())
			Expect(storeMan.Zones().Create(context.Background(), &zone.Zone{UUID: "zone-1", Name: "zone-1", Metadata: metadata})).To(Succeed())

			srcs, err := storeMan.Certificates().FindSrcWithForeignResource(context.Background(), secretTypeUrl, "secret-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(srcs).To(ConsistOf("cert-1"))
			srcs, err = storeMan.Zones().FindSrcWithForeignResource(context.Background(), secretTypeUrl, "secret-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(srcs).To(ConsistOf("zone-1"))
		})
	})

	Context("Permissions", func() {
		var backendStore *DbStore[*backend.Backend]
		BeforeEach(func() {
			backendStore = storeMan.Backends().(*DbStore[*backend.Backend])
			Expect(backendStore.Create(context.Background(), &backend.Backend{UUID: "uuid-1", Name: "backend-1"})).To(Succeed())
		})

		It("should grant lower roles to member having a higher role", func() {
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_WRITER, permission.MemberType_USER, "bob")).To(Succeed())

			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_READER, "bob", nil)).To(BeTrue())
			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_WRITER, "bob", nil)).To(BeTrue())
			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_OWNER, "bob", nil)).To(BeFalse())
		})
		It("should give no permission on an unknown resource, with no error", func() {
			Expect(backendStore.HasPermission(context.Background(), "unknown", permission.Role_READER, "bob", nil)).To(BeFalse())
		})
		It("should grant permission through groups", func() {
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_OWNER, permission.MemberType_GROUP, "team")).To(Succeed())

			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_OWNER, "", []string{"other", "team"})).To(BeTrue())
			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_OWNER, "team", nil)).To(BeFalse())
		})
		It("should replace previous role of member", func() {
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_OWNER, permission.MemberType_USER, "bob")).To(Succeed())
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())

			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_OWNER, "bob", nil)).To(BeFalse())
			perms, err := backendStore.ListMemberPermissions(context.Background(), "uuid-1", -1)
			Expect(err).ToNot(HaveOccurred())
			Expect(perms).To(HaveLen(1))
		})
		It("should delete permission", func() {
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_OWNER, permission.MemberType_USER, "bob")).To(Succeed())

			Expect(backendStore.DeletePermission(context.Background(), "uuid-1", permission.MemberType_USER, "bob")).To(Succeed())

			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_READER, "bob", nil)).To(BeFalse())
		})
		It("should list member permissions by role ordered by role", func() {
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_OWNER, permission.MemberType_USER, "bob")).To(Succeed())
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_READER, permission.MemberType_GROUP, "team")).To(Succeed())

			perms, err := backendStore.ListMemberPermissions(context.Background(), "uuid-1", permission.Role_OWNER)
			Expect(err).ToNot(HaveOccurred())
			Expect(perms).To(HaveLen(1))
			Expect(perms[0].GetMember().GetMemberName()).To(Equal("bob"))

			perms, err = backendStore.ListMemberPermissions(context.Background(), "uuid-1", -1)
			Expect(err).ToNot(HaveOccurred())
			Expect(perms).To(HaveLen(2))
			Expect(perms[0].GetRole()).To(Equal(permission.Role_READER))
			Expect(perms[0].GetMember().GetMemberType()).To(Equal(permission.MemberType_GROUP))
			Expect(perms[1].GetRole()).To(Equal(permission.Role_OWNER))
		})
		It("should allow anyone to read when allow all read is set", func() {
			Expect(backendStore.IsAllowAllRead(context.Background(), "uuid-1")).To(BeFalse())

			Expect(backendStore.SetAllowAllRead(context.Background(), "uuid-1", true)).To(Succeed())

			Expect(backendStore.IsAllowAllRead(context.Background(), "uuid-1")).To(BeTrue())
			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_READER, "anyone", nil)).To(BeTrue())
			Expect(backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_WRITER, "anyone", nil)).To(BeFalse())
		})
		It("should require username or groups", func() {
			_, err := backendStore.HasPermission(context.Background(), "uuid-1", permission.Role_READER, "", nil)
			Expect(err).To(MatchError(ContainSubstring("username or/and groups must be set")))
		})
		It("should remove permissions on delete", func() {
			Expect(backendStore.AddPermission(context.Background(), "uuid-1", permission.Role_OWNER, permission.MemberType_USER, "bob")).To(Succeed())

			Expect(backendStore.Delete(context.Background(), "uuid-1")).To(Succeed())

			perms, err := backendStore.ListMemberPermissions(context.Background(), "uuid-1", -1)
			Expect(err).ToNot(HaveOccurred())
			Expect(perms).To(BeEmpty())
		})
	})
})
