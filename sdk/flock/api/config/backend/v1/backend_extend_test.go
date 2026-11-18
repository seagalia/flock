package backend_test

import (
	"errors"
	"time"

	"buf.build/go/protovalidate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	. "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Backend", func() {
	var aBackend *Backend

	BeforeEach(func() {
		aBackend = &Backend{UUID: "backend-1", Name: "backend-1"}
	})

	It("refuses an eds url whose scheme isn't http, https, grpc or grpcs", func() {
		edsOf := func(scheme string) *EDS {
			return &EDS{Url: &core.URL{Scheme: scheme, Host: "vigie", Port: 8080}, ServiceName: "web"}
		}

		var validationErr *protovalidate.ValidationError
		Expect(errors.As(helpers.Validate(edsOf("ftp")), &validationErr)).To(BeTrue())
		Expect(validationErr.Violations).To(HaveLen(1))
		Expect(validationErr.Violations[0].Proto.GetRuleId()).To(Equal("eds.url.scheme"))
		for _, scheme := range []string{"http", "https", "grpc", "grpcs"} {
			Expect(helpers.Validate(edsOf(scheme))).To(Succeed(), scheme)
		}
	})

	Context("GetStarlingHttpProtocolOptions", func() {
		It("should give nothing when http protocol options are not set", func() {
			options, err := aBackend.GetStarlingHttpProtocolOptions()
			Expect(err).ToNot(HaveOccurred())
			Expect(options).To(BeNil())
		})

		It("should give the http protocol options set", func() {
			expected := &starling_commopts.HttpProtocolOptions{
				UpstreamHttpProtocolOptions: &starling_commopts.UpstreamHttpProtocolOptions{AutoSni: true},
			}
			packed, err := anypb.New(expected)
			Expect(err).ToNot(HaveOccurred())
			aBackend.HttpProtocolOptions = packed

			options, err := aBackend.GetStarlingHttpProtocolOptions()
			Expect(err).ToNot(HaveOccurred())
			Expect(options).To(BeComparableTo(expected, protocmp.Transform()))
		})

		It("should fail when http protocol options are of another type", func() {
			packed, err := anypb.New(wrapperspb.String("not options"))
			Expect(err).ToNot(HaveOccurred())
			aBackend.HttpProtocolOptions = packed

			_, err = aBackend.GetStarlingHttpProtocolOptions()
			Expect(err).To(HaveOccurred())
		})
	})

	Context("GetStarlingOptions", func() {
		It("should give empty options when typed options are not set", func() {
			options, err := aBackend.GetStarlingOptions()
			Expect(err).ToNot(HaveOccurred())
			Expect(options).ToNot(BeNil())
		})

		It("should fail when typed options are of another type", func() {
			packed, err := anypb.New(wrapperspb.String("not options"))
			Expect(err).ToNot(HaveOccurred())
			aBackend.TypedOptions = packed

			_, err = aBackend.GetStarlingOptions()
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Metadata", func() {
		It("should add and delete metadata even when no metadata exists", func() {
			Expect(aBackend.DeleteMetadata("key")).To(BeNil())
			aBackend.AddMetadata("key", "value")
			Expect(aBackend.GetMetadata()).To(Equal(map[string]string{"key": "value"}))
			Expect(aBackend.DeleteMetadata("key")).To(BeEmpty())
		})
	})

	Context("ProvDefault", func() {
		It("should set a refresh rate and a health check when missing", func() {
			aBackend.ProvDefault()
			Expect(aBackend.GetRefreshRate().AsDuration()).To(Equal(5 * time.Second))
			Expect(aBackend.GetHealthCheck()).ToNot(BeNil())
		})

		It("should keep the refresh rate already set", func() {
			aBackend.RefreshRate = durationpb.New(time.Second)
			aBackend.ProvDefault()
			Expect(aBackend.GetRefreshRate().AsDuration()).To(Equal(time.Second))
		})

		It("should not panic on a nil backend", func() {
			var nilBackend *Backend
			Expect(nilBackend.ProvDefault).ToNot(Panic())
		})
	})
})
