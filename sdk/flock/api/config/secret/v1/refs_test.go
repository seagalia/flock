package secret_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	. "github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
)

var _ = Describe("Secret references of a resource", func() {
	keyRef := func(secretUUID, key string) *flocktype.SecretKeyRef {
		return &flocktype.SecretKeyRef{SecretUuid: secretUUID, Key: key}
	}
	plainHeader := func(key, value string) *core.HeaderValue {
		return &core.HeaderValue{Key: key, ValueSource: &core.HeaderValue_Value{Value: value}}
	}
	secretHeader := func(key string, ref *flocktype.SecretKeyRef) *core.HeaderValue {
		return &core.HeaderValue{Key: key, ValueSource: &core.HeaderValue_ValueFromSecret{ValueFromSecret: ref}}
	}
	edsBackend := func() *backend.Backend {
		return &backend.Backend{
			UUID:     "b1",
			Metadata: map[string]string{"a": "secret://s1/k", "plain": "v"},
			Endpoint: &backend.Backend_Eds{Eds: &backend.EDS{
				Url: &core.URL{Scheme: "http", Host: "vigie", UserInfo: &core.URL_UserInfo{
					User:           "u",
					PasswordSource: &core.URL_UserInfo_PasswordFromSecret{PasswordFromSecret: keyRef("s1", "k")},
				}},
				Headers: []*core.HeaderValue{plainHeader("x-plain", "p"), secretHeader("x-token", keyRef("s1", "k"))},
			}},
		}
	}
	packedOptions := func() *anypb.Any {
		options, err := anypb.New(&starling_vsopts.HttpOptions{
			RequestHeadersToAdd:  []*core.HeaderValueOption{{Header: secretHeader("x-request", keyRef("s2", "token"))}},
			ResponseHeadersToAdd: []*core.HeaderValueOption{{Header: plainHeader("x-response", "p")}},
		})
		Expect(err).ToNot(HaveOccurred())
		return options
	}
	optionsHeadersVs := func() *vs.VirtualServer {
		return &vs.VirtualServer{UUID: "vs1", RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
			HttpRoutesConfig: &vs.HttpRoutesConfig{
				TypedOptions: packedOptions(),
				VirtualHosts: []*route.VirtualHost{{Routes: []*route.Route{{}}}},
			},
		}}
	}
	httpOptionsOf := func(aVs *vs.VirtualServer) *starling_vsopts.HttpOptions {
		options := &starling_vsopts.HttpOptions{}
		Expect(aVs.GetHttpRoutesConfig().GetTypedOptions().UnmarshalTo(options)).To(Succeed())
		return options
	}
	optionsHeaderPath := "http_routes_config.typed_options.request_headers_to_add[0].header"

	Describe("FindRefs", func() {
		It("finds the metadata references and the typed ones, sorted by location", func() {
			refs, err := FindRefs(edsBackend())

			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(Equal([]FoundRef{
				{Path: "eds.headers[1]", Ref: Ref{SecretUUID: "s1", Key: "k"}},
				{Path: "eds.url.user_info", Ref: Ref{SecretUUID: "s1", Key: "k"}},
				{Path: `metadata["a"]`, Ref: Ref{SecretUUID: "s1", Key: "k"}},
			}))
		})

		It("finds a header inside the starling HTTP options of a virtual server", func() {
			refs, err := FindRefs(optionsHeadersVs())

			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(Equal([]FoundRef{{Path: optionsHeaderPath, Ref: Ref{SecretUUID: "s2", Key: "token"}}}))
		})

		It("finds a header of the HTTP health check of a backend", func() {
			aBackend := &backend.Backend{HealthCheck: &core.HealthCheck{HealthChecker: &core.HealthCheck_HttpHealthCheck{
				HttpHealthCheck: &core.HttpHealthCheck{RequestHeadersToAdd: []*core.HeaderValueOption{
					{Header: plainHeader("x-plain", "p")},
					{Header: secretHeader("x-token", keyRef("s1", "k"))},
				}},
			}}}

			refs, err := FindRefs(aBackend)

			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(Equal([]FoundRef{{
				Path: "health_check.http_health_check.request_headers_to_add[1].header",
				Ref:  Ref{SecretUUID: "s1", Key: "k"},
			}}))
		})

		It("leaves out a header packed where no extension of the agents reads one", func() {
			aVs := &vs.VirtualServer{RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
				VirtualHosts: []*route.VirtualHost{{Routes: []*route.Route{{TypedOptions: packedOptions()}}}},
			}}}

			refs, err := FindRefs(aVs)

			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(BeEmpty())
		})

		It("gives one reference per location when two headers point at the same value", func() {
			aBackend := edsBackend()
			aBackend.GetEds().Headers = append(aBackend.GetEds().Headers, secretHeader("x-again", keyRef("s1", "k")))

			refs, err := FindRefs(aBackend)

			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(ContainElements(
				FoundRef{Path: "eds.headers[1]", Ref: Ref{SecretUUID: "s1", Key: "k"}},
				FoundRef{Path: "eds.headers[2]", Ref: Ref{SecretUUID: "s1", Key: "k"}},
			))
		})

		It("reports a malformed metadata reference and still gives the others", func() {
			aBackend := edsBackend()
			aBackend.Metadata["bad"] = "secret://s1"

			refs, err := FindRefs(aBackend)

			Expect(err).To(MatchError(ContainSubstring(`metadata key "bad"`)))
			Expect(refs).To(HaveLen(3))
		})

		It("gives nothing for a resource not there", func() {
			refs, err := FindRefs(nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(BeEmpty())

			var noBackend *backend.Backend
			refs, err = FindRefs(noBackend)
			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(BeEmpty())
		})

		It("gives nothing for a secret", func() {
			refs, err := FindRefs(&Secret{UUID: "s1", Metadata: map[string]string{"a": "secret://s2/k"}})

			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(BeEmpty())
		})
	})

	Describe("Resolve and Unresolve", func() {
		held := func(Ref) (string, bool) { return "v", true }
		notHeld := func(Ref) (string, bool) { return "", false }

		It("writes the values of the secrets in every location", func() {
			aBackend := edsBackend()
			refs, _ := FindRefs(aBackend)

			Resolve(aBackend, refs, held)

			Expect(aBackend.GetEds().GetHeaders()[1].GetValue()).To(Equal("v"))
			Expect(aBackend.GetEds().GetUrl().GetUserInfo().GetPassword()).To(Equal("v"))
			Expect(aBackend.GetMetadata()).To(Equal(map[string]string{"a": "v", "plain": "v"}))
		})

		It("empties the typed fields of a secret not held and keeps the metadata reference", func() {
			aBackend := edsBackend()
			refs, _ := FindRefs(aBackend)

			Resolve(aBackend, refs, notHeld)

			Expect(aBackend.GetEds().GetHeaders()[1].GetValueSource()).To(Equal(&core.HeaderValue_Value{Value: ""}))
			Expect(aBackend.GetEds().GetUrl().GetUserInfo().GetPasswordSource()).
				To(Equal(&core.URL_UserInfo_Password{Password: ""}))
			Expect(aBackend.GetMetadata()["a"]).To(Equal("secret://s1/k"))
		})

		It("puts the references back where they were found", func() {
			received := edsBackend()
			refs, _ := FindRefs(received)
			resolved := proto.Clone(received)
			Resolve(resolved, refs, held)

			Unresolve(resolved, refs)

			Expect(proto.Equal(resolved, received)).To(BeTrue())
		})

		It("resolves and puts back a header of the starling HTTP options", func() {
			received := optionsHeadersVs()
			refs, _ := FindRefs(received)
			resolved := proto.Clone(received).(*vs.VirtualServer)

			Resolve(resolved, refs, held)
			Expect(httpOptionsOf(resolved).GetRequestHeadersToAdd()[0].GetHeader().GetValue()).To(Equal("v"))

			Unresolve(resolved, refs)
			Expect(proto.Equal(httpOptionsOf(resolved), httpOptionsOf(received))).To(BeTrue())
		})
	})

	Describe("EachKeyRef", func() {
		It("lets a typed reference of the starling HTTP options be rewritten", func() {
			aVs := optionsHeadersVs()
			var paths []string

			err := EachKeyRef(aVs, func(path string, ref *flocktype.SecretKeyRef) error {
				paths = append(paths, path)
				ref.SecretUuid = "u1"
				return nil
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(paths).To(Equal([]string{optionsHeaderPath}))
			Expect(httpOptionsOf(aVs).GetRequestHeadersToAdd()[0].GetHeader().GetValueFromSecret().GetSecretUuid()).
				To(Equal("u1"))
		})
	})
})
