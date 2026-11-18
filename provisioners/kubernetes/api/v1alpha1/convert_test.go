package v1alpha1_test

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/provisioners/kubernetes/api/v1alpha1"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	zonev1 "github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	starlingoptionsv1 "github.com/seagalia/flock/sdk/flock/extensions/backend/starling_options/v1"
	starlinglimitv1 "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// fakeResolver gives the uuid `<kind>-<name>`, and fails on the names in missing.
type fakeResolver struct {
	missing map[string]bool
}

func (r fakeResolver) UUID(_ context.Context, kind v1alpha1.Kind, ref v1alpha1.ObjectRef) (string, error) {
	if r.missing[ref.Name] {
		return "", fmt.Errorf("%s %s not found", kind, ref.Name)
	}
	return fmt.Sprintf("%s-%s", kind, ref.Name), nil
}

// valid tells whether the controller would take the message: it defaults then validates every received message. The
// specs set the name first, the provisioner takes it from the object.
func valid(message proto.Message) error {
	helpers.ProvDefault(message)
	return helpers.Validate(message)
}

var _ = Describe("ToProto", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("converts a backend the controller takes once defaulted", func() {
		spec := &v1alpha1.BackendSpec{
			EnableTLS:               new(true),
			SNI:                     "app.example.com",
			LBPolicy:                v1alpha1.BackendLBPolicyLeastRequest,
			RefreshRateMilliseconds: 30000,
			EDS: &v1alpha1.EDS{
				URL:         &v1alpha1.URL{Scheme: "https", Host: "vigie.example.com"},
				ServiceName: "app",
				Headers: []v1alpha1.HeaderValue{{
					Key:             "authorization",
					ValueFromSecret: &v1alpha1.SecretKeyRef{SecretRef: v1alpha1.ObjectRef{Name: "vigie"}, Key: "token"},
				}},
			},
			HealthCheck:  v1alpha1.HealthCheck{TCPHealthCheck: &v1alpha1.TCPHealthCheck{}},
			TypedOptions: &v1alpha1.BackendTypedOptions{Starling: &v1alpha1.StarlingBackendOptions{}},
		}

		backend, err := spec.ToProto(ctx, fakeResolver{})

		Expect(err).NotTo(HaveOccurred())
		Expect(backend.GetEnableTls()).To(BeTrue())
		Expect(backend.GetSni()).To(Equal("app.example.com"))
		Expect(backend.GetLbPolicy()).To(Equal(backendv1.Backend_LEAST_REQUEST))
		Expect(backend.GetRefreshRate().AsDuration()).To(Equal(30 * time.Second))
		Expect(backend.GetHealthCheck().GetTcpHealthCheck()).NotTo(BeNil())
		Expect(backend.GetEds().GetUrl().GetHost()).To(Equal("vigie.example.com"))
		Expect(backend.GetEds().GetHeaders()[0].GetValueFromSecret().GetSecretUuid()).To(Equal("Secret-vigie"))
		Expect(backend.GetTypedOptions().MessageIs(&starlingoptionsv1.BackendOptions{})).To(BeTrue())
		backend.Name = "app"
		Expect(valid(backend)).To(Succeed())
	})

	It("leaves an absent duration to the default of the controller", func() {
		spec := &v1alpha1.BackendSpec{
			DNS:         &v1alpha1.DNS{Host: "app.example.com", Port: 80},
			HealthCheck: v1alpha1.HealthCheck{NoHealthCheck: &v1alpha1.NoHealthCheck{}},
		}

		backend, err := spec.ToProto(ctx, fakeResolver{})

		Expect(err).NotTo(HaveOccurred())
		Expect(backend.GetRefreshRate()).To(BeNil())
		backend.Name = "app"
		Expect(valid(backend)).To(Succeed())
	})

	It("names the path of a reference it can't resolve", func() {
		spec := &v1alpha1.BackendSpec{EDS: &v1alpha1.EDS{
			URL: &v1alpha1.URL{Scheme: "https", Host: "vigie.example.com"},
			Headers: []v1alpha1.HeaderValue{
				{Key: "x-team", Value: new("lb")},
				{Key: "authorization", ValueFromSecret: &v1alpha1.SecretKeyRef{
					SecretRef: v1alpha1.ObjectRef{Name: "gone"}, Key: "token",
				}},
			},
		}}

		_, err := spec.ToProto(ctx, fakeResolver{missing: map[string]bool{"gone": true}})

		Expect(err).To(MatchError("eds: headers[1]: valueFromSecret: secretRef: Secret gone not found"))
	})

	It("refuses an enum value the type doesn't define", func() {
		spec := &v1alpha1.BackendSpec{LBPolicy: "Fastest"}

		_, err := spec.ToProto(ctx, fakeResolver{})

		Expect(err).To(MatchError(`lbPolicy: unknown value "Fastest"`))
	})

	It("converts a zone with its pools", func() {
		spec := &v1alpha1.ZoneSpec{
			Zone: "eu-west-1a",
			Pools: []v1alpha1.IPPool{{
				Name:     "internal",
				Privacy:  v1alpha1.IPPoolPrivacyPrivate,
				Subnets:  []v1alpha1.Subnet{{IP: "10.0.0.0", PrefixLength: 24}},
				Metadata: map[string]string{"team": "lb"},
			}},
		}

		zone, err := spec.ToProto(ctx, fakeResolver{})

		Expect(err).NotTo(HaveOccurred())
		pool := zone.GetPools()[0]
		Expect(pool.GetPrivacy()).To(Equal(zonev1.IpPool_PRIVATE))
		Expect(pool.GetSubnets()[0].GetPrefixLength()).To(BeEquivalentTo(24))
		Expect(pool.GetMetadata()).To(HaveKeyWithValue("team", "lb"))
		zone.Name = "paris"
		Expect(valid(zone)).To(Succeed())
	})

	It("converts a certificate without its secret, which the reconciler copies", func() {
		spec := &v1alpha1.CertificateSpec{
			DomainNames: []string{"app.example.com"},
			SecretRef:   v1alpha1.ObjectRef{Name: "app-tls"},
		}

		certificate, err := spec.ToProto(ctx, fakeResolver{missing: map[string]bool{"app-tls": true}})

		Expect(err).NotTo(HaveOccurred())
		Expect(certificate.GetDomainNames()).To(Equal([]string{"app.example.com"}))
		Expect(certificate.GetCertChain()).To(BeEmpty())
	})

	It("resolves the references of an instance", func() {
		spec := &v1alpha1.InstanceSpec{
			ZoneRef:      v1alpha1.ObjectRef{Name: "paris"},
			ResourceType: "starling",
			VIPRefs:      []v1alpha1.ObjectRef{{Name: "front"}, {Name: "back"}},
		}

		instance, err := spec.ToProto(ctx, fakeResolver{})

		Expect(err).NotTo(HaveOccurred())
		Expect(instance.GetZoneUuid()).To(Equal("Zone-paris"))
		Expect(instance.GetVipsUuid()).To(Equal([]string{"VIP-front", "VIP-back"}))
		instance.Name = "lb-1"
		Expect(valid(instance)).To(Succeed())
	})

	It("converts a virtual server down to the last level of its rate limit descriptors", func() {
		descriptor := v1alpha1.StarlingDescriptor{
			Key: "remote_address",
			Descriptors: []v1alpha1.StarlingDescriptorLevel2{{
				Key: "path",
				Descriptors: []v1alpha1.StarlingDescriptorLevel3{{
					Key: "method",
					Descriptors: []v1alpha1.StarlingDescriptorLevel4{{
						Key:    "user",
						Policy: &v1alpha1.StarlingPolicy{Unit: v1alpha1.TimeUnitMinute, RequestsPerUnit: 10},
					}},
				}},
			}},
		}
		spec := &v1alpha1.VirtualServerSpec{
			Port: 443,
			HTTPRoutesConfig: &v1alpha1.HTTPRoutesConfig{
				VirtualHosts: []v1alpha1.VirtualHost{{
					Domains: []string{"app.example.com"},
					Routes: []v1alpha1.Route{{
						Name:       "all",
						Match:      v1alpha1.RouteMatch{Prefix: new("/")},
						BackendRef: &v1alpha1.ObjectRef{Name: "app"},
					}},
				}},
			},
			LimitConfig: []v1alpha1.LimitConfig{{
				TypedLimit: &v1alpha1.LimitConfigTypedLimit{
					Starling: &v1alpha1.StarlingRateLimit{Descriptors: []v1alpha1.StarlingDescriptor{descriptor}},
				},
			}},
		}

		virtualServer, err := spec.ToProto(ctx, fakeResolver{})

		Expect(err).NotTo(HaveOccurred())
		route := virtualServer.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0]
		Expect(route.GetBackendUuid()).To(Equal("Backend-app"))
		rateLimit := &starlinglimitv1.StarlingRateLimit{}
		Expect(virtualServer.GetLimitConfig()[0].GetTypedLimit().UnmarshalTo(rateLimit)).To(Succeed())
		deepest := rateLimit.GetDescriptors()[0].GetDescriptors()[0].GetDescriptors()[0].GetDescriptors()[0]
		Expect(deepest.GetKey()).To(Equal("user"))
		Expect(deepest.GetPolicy().GetRequestsPerUnit()).To(BeEquivalentTo(10))
		virtualServer.Name = "app"
		Expect(valid(virtualServer)).To(Succeed())
	})
})
