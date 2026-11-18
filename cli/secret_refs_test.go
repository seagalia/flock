package cli

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/sdk/clients"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	vsv1 "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

const secretBackendDoc = `kind: backend
name: web
metadata:
  tok: secret://name:s/token
ip_addr_group:
  ip_addresses:
  - ip: 10.0.0.10
    port: 8080
health_check:
  no_health_check: {}
`

const secretPasswordDoc = `kind: backend
name: web
eds:
  url:
    scheme: http
    host: vigie
    user_info:
      user: lb
      password_from_secret:
        secret_uuid: name:s
        key: token
health_check:
  no_health_check: {}
`

var _ = Describe("resolveSecretRefs", func() {
	var (
		conn   *fakeconnect.Conn
		client *clients.Client
		ctx    = context.Background()
	)

	BeforeEach(func() {
		conn = fakeconnect.New()
		client = clients.New(conn.Client())
		conn.Handle(svcsecretconnect.SecretListSecretProcedure, func(req proto.Message) (proto.Message, error) {
			resp := &svcsecret.ListSecretResponse{}
			isFirstPage := req.(*svcsecret.ListSecretRequest).GetOffset() == 0
			if isFirstPage && nameFilter(req) == "s" {
				resp.Secrets = []*secret.Secret{{UUID: "sec-1", Name: "s"}}
			}
			if isFirstPage && nameFilter(req) == "twice" {
				resp.Secrets = []*secret.Secret{{UUID: "sec-2", Name: "twice"}, {UUID: "sec-3", Name: "twice"}}
			}
			return resp, nil
		})
	})

	withMetadata := func(metadata map[string]string) *backendv1.Backend {
		return &backendv1.Backend{Metadata: metadata}
	}

	It("replaces a known name by its uuid", func() {
		metadata := map[string]string{"tok": "secret://name:s/token", "env": "prod"}

		Expect(resolveSecretRefs(ctx, client, withMetadata(metadata))).To(Succeed())

		Expect(metadata).To(Equal(map[string]string{"tok": "secret://sec-1/token", "env": "prod"}))
	})

	It("fails on an unknown name", func() {
		metadata := map[string]string{"tok": "secret://name:x/token"}

		err := resolveSecretRefs(ctx, client, withMetadata(metadata))

		Expect(err).To(MatchError(`metadata "tok": no secret named "x"`))
	})

	It("refuses a name several secrets share", func() {
		metadata := map[string]string{"tok": "secret://name:twice/token"}

		err := resolveSecretRefs(ctx, client, withMetadata(metadata))

		Expect(err).To(MatchError(`metadata "tok": 2 secrets are named "twice", use secret://<secret uuid>/token`))
	})

	It("leaves a uuid reference and a plain value alone, with no call", func() {
		metadata := map[string]string{"a": "secret://sec-9/token", "b": "plain"}

		Expect(resolveSecretRefs(ctx, client, withMetadata(metadata))).To(Succeed())

		Expect(metadata).To(Equal(map[string]string{"a": "secret://sec-9/token", "b": "plain"}))
		Expect(conn.Calls()).To(BeEmpty())
	})

	It("refuses a name reference without a key", func() {
		err := resolveSecretRefs(ctx, client, withMetadata(map[string]string{"tok": "secret://name:s"}))

		Expect(err).To(MatchError(ContainSubstring(`metadata "tok"`)))
	})

	Describe("in typed fields", func() {
		secretHeader := func(secretUuid string) *corev1.HeaderValue {
			return &corev1.HeaderValue{Key: "x-token", ValueSource: &corev1.HeaderValue_ValueFromSecret{
				ValueFromSecret: &flocktype.SecretKeyRef{SecretUuid: secretUuid, Key: "token"},
			}}
		}
		edsBackend := func(secretUuid string) *backendv1.Backend {
			return &backendv1.Backend{Endpoint: &backendv1.Backend_Eds{Eds: &backendv1.EDS{
				Headers: []*corev1.HeaderValue{secretHeader(secretUuid)},
			}}}
		}

		It("replaces a known name by its uuid in an eds header", func() {
			aBackend := edsBackend("name:s")

			Expect(resolveSecretRefs(ctx, client, aBackend)).To(Succeed())

			Expect(aBackend.GetEds().GetHeaders()[0].GetValueFromSecret().GetSecretUuid()).To(Equal("sec-1"))
		})

		It("replaces a name in a header of the starling HTTP options", func() {
			options, err := anypb.New(&starling_vsopts.HttpOptions{
				RequestHeadersToAdd: []*corev1.HeaderValueOption{{Header: secretHeader("name:s")}},
			})
			Expect(err).ToNot(HaveOccurred())
			aVs := &vsv1.VirtualServer{RouteConfig: &vsv1.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vsv1.HttpRoutesConfig{
				TypedOptions: options,
				VirtualHosts: []*route.VirtualHost{{Routes: []*route.Route{{}}}},
			}}}

			Expect(resolveSecretRefs(ctx, client, aVs)).To(Succeed())

			resolved := &starling_vsopts.HttpOptions{}
			Expect(aVs.GetHttpRoutesConfig().GetTypedOptions().UnmarshalTo(resolved)).To(Succeed())
			Expect(resolved.GetRequestHeadersToAdd()[0].GetHeader().GetValueFromSecret().GetSecretUuid()).To(Equal("sec-1"))
		})

		It("fails on an unknown name, naming the field", func() {
			err := resolveSecretRefs(ctx, client, edsBackend("name:x"))

			Expect(err).To(MatchError(`eds.headers[0]: no secret named "x"`))
		})

		It("leaves a uuid alone, with no call", func() {
			aBackend := edsBackend("sec-9")

			Expect(resolveSecretRefs(ctx, client, aBackend)).To(Succeed())

			Expect(aBackend.GetEds().GetHeaders()[0].GetValueFromSecret().GetSecretUuid()).To(Equal("sec-9"))
			Expect(conn.Calls()).To(BeEmpty())
		})
	})

	Describe("in apply", func() {
		BeforeEach(func() {
			conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{}))
		})

		It("shows the uuid form of a created backend", func() {
			conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{}))

			plan, err := planApply(ctx, client, parseDocs(secretBackendDoc))

			Expect(err).NotTo(HaveOccurred())
			created := plan.Changes[0].To.(*backendv1.Backend)
			Expect(created.GetMetadata()).To(Equal(map[string]string{"tok": "secret://sec-1/token"}))
		})

		It("shows the uuid of a password secret named in a manifest", func() {
			conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{}))

			plan, err := planApply(ctx, client, parseDocs(secretPasswordDoc))

			Expect(err).NotTo(HaveOccurred())
			created := plan.Changes[0].To.(*backendv1.Backend)
			Expect(created.GetEds().GetUrl().GetUserInfo().GetPasswordFromSecret().GetSecretUuid()).To(Equal("sec-1"))
		})

		It("changes nothing on a second run", func() {
			stored := parseDocs(secretBackendDoc)[0].Message.(*backendv1.Backend)
			stored.UUID = "b-1"
			stored.Metadata = map[string]string{"tok": "secret://sec-1/token"}
			conn.Handle(svcbackendconnect.BackendListBackendProcedure,
				firstPage(&svcbackend.ListBackendResponse{Backends: []*backendv1.Backend{stored}}))

			plan, err := planApply(ctx, client, parseDocs(secretBackendDoc))

			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Changes).To(BeEmpty())
			Expect(plan.Unchanged).To(Equal([]string{"backend web"}))
		})
	})
})
