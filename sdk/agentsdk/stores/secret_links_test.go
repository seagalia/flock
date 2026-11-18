package stores_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("SecretLinks", func() {
	var links *SecretLinks
	vsType := helpers.TypeUrl[*vs.VirtualServer]()
	backendType := helpers.TypeUrl[*backend.Backend]()
	found := func(path, secretUuid, key string) secret.FoundRef {
		return secret.FoundRef{Path: path, Ref: secret.Ref{SecretUUID: secretUuid, Key: key}}
	}
	refs := func(found ...secret.FoundRef) []secret.FoundRef { return found }

	BeforeEach(func() {
		links = NewSecretLinks()
	})

	It("replaces the links of a resource", func() {
		links.Set(vsType, "vs-1", refs(found(`metadata["a"]`, "s1", "k")))
		links.Set(vsType, "vs-1", refs(found(`metadata["b"]`, "s2", "k")))

		Expect(links.LinkedTo("s1")).To(BeEmpty())
		Expect(links.LinkedTo("s2")).To(Equal([]ResourceKey{{TypeUrl: vsType, UUID: "vs-1"}}))
		Expect(links.Refs(vsType, "vs-1")).To(Equal(refs(found(`metadata["b"]`, "s2", "k"))))
	})

	It("lists each resource linked to a secret once", func() {
		links.Set(vsType, "vs-1", refs(found(`metadata["a"]`, "s1", "k"), found(`metadata["b"]`, "s1", "other")))
		links.Set(backendType, "backend-1", refs(found(`metadata["a"]`, "s1", "k")))

		Expect(links.LinkedTo("s1")).To(Equal([]ResourceKey{
			{TypeUrl: backendType, UUID: "backend-1"},
			{TypeUrl: vsType, UUID: "vs-1"},
		}))
	})

	It("forgets the links of a deleted resource", func() {
		links.Set(vsType, "vs-1", refs(found(`metadata["a"]`, "s1", "k")))
		links.Delete(vsType, "vs-1")

		Expect(links.LinkedTo("s1")).To(BeEmpty())
		Expect(links.Refs(vsType, "vs-1")).To(BeEmpty())
	})

	It("puts the references back in a clone", func() {
		resolved := &vs.VirtualServer{UUID: "vs-1", Metadata: map[string]string{"a": "value", "plain": "x"}}
		links.Set(vsType, "vs-1", refs(found(`metadata["a"]`, "s1", "k")))

		unresolved := links.Unresolved(resolved)

		Expect(helpers.GetMetadata(unresolved)).To(Equal(map[string]string{"a": "secret://s1/k", "plain": "x"}))
		Expect(resolved.GetMetadata()).To(HaveKeyWithValue("a", "value"))
	})

	It("puts back a typed reference along with a metadata one", func() {
		fromSecret := &flocktype.SecretKeyRef{SecretUuid: "s1", Key: "k"}
		received := &backend.Backend{UUID: "backend-1", Metadata: map[string]string{"a": "secret://s1/k"},
			Endpoint: &backend.Backend_Eds{Eds: &backend.EDS{Headers: []*core.HeaderValue{
				{Key: "x-token", ValueSource: &core.HeaderValue_ValueFromSecret{ValueFromSecret: fromSecret}},
			}}}}
		resolved := &backend.Backend{UUID: "backend-1", Metadata: map[string]string{"a": "v"},
			Endpoint: &backend.Backend_Eds{Eds: &backend.EDS{Headers: []*core.HeaderValue{
				{Key: "x-token", ValueSource: &core.HeaderValue_Value{Value: "v"}},
			}}}}
		links.Set(backendType, "backend-1", SecretRefsOf(received))

		Expect(proto.Equal(links.Unresolved(resolved), received)).To(BeTrue())
	})

	It("blanks a secret", func() {
		unresolved := links.Unresolved(&secret.Secret{UUID: "s1", Data: map[string]string{"k": "v"}})

		Expect(unresolved.(*secret.Secret).GetData()).To(Equal(map[string]string{"k": ""}))
	})

	It("skips malformed references", func() {
		aBackend := &backend.Backend{Metadata: map[string]string{"ok": "secret://s1/k", "bad": "secret://s1"}}

		Expect(SecretRefsOf(aBackend)).To(Equal(refs(found(`metadata["ok"]`, "s1", "k"))))
	})

	It("resolves held secrets, keeps the others and empties missing keys", func() {
		secrets := storers.NewMemoryManager().Secrets()
		Expect(secrets.Create(context.Background(), &secret.Secret{UUID: "s1", Data: map[string]string{"k": "v"}})).To(Succeed())
		aBackend := &backend.Backend{Metadata: map[string]string{
			"a": "secret://s1/k", "b": "secret://s1/missing", "c": "secret://s2/k",
		}}

		ResolveSecretRefs(context.Background(), aBackend, SecretRefsOf(aBackend), secrets)

		Expect(aBackend.GetMetadata()).To(Equal(map[string]string{"a": "v", "b": "", "c": "secret://s2/k"}))
	})
})
