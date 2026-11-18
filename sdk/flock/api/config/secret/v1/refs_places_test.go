package secret_test

import (
	"os"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// FindRefs knows by name the places of a secret value, a new field holding a header value or a url password must be
// taught to it. This spec reads every proto of the api to tell when one shows up.
var _ = Describe("Places of a secret value", func() {
	It("lists every field leading to a header value or a url password", func() {
		content, err := os.ReadFile("../../../../../../cli/protoset/protoset.bin")
		Expect(err).ToNot(HaveOccurred())
		set := &descriptorpb.FileDescriptorSet{}
		Expect(proto.Unmarshal(content, set)).To(Succeed())
		files, err := protodesc.NewFiles(set)
		Expect(err).ToNot(HaveOccurred())

		var messages []protoreflect.MessageDescriptor
		var addMessages func(protoreflect.MessageDescriptors)
		addMessages = func(descriptors protoreflect.MessageDescriptors) {
			for i := range descriptors.Len() {
				messages = append(messages, descriptors.Get(i))
				addMessages(descriptors.Get(i).Messages())
			}
		}
		files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
			isResourceOrExtension := strings.HasPrefix(string(file.Package()), "flock.api.config.") ||
				strings.HasPrefix(string(file.Package()), "flock.extensions.")
			if isResourceOrExtension {
				addMessages(file.Messages())
			}
			return true
		})

		// a message leads to a secret value when one of its fields does, until no message is added
		leading := map[protoreflect.FullName]bool{
			"flock.api.config.core.v1.HeaderValue":  true,
			"flock.api.config.core.v1.URL.UserInfo": true,
		}
		var places []string
		for added := true; added; {
			added = false
			places = nil
			for _, message := range messages {
				fields := message.Fields()
				for i := range fields.Len() {
					field := fields.Get(i)
					valueType := field.Message()
					if field.IsMap() {
						valueType = field.MapValue().Message()
					}
					if valueType == nil || !leading[valueType.FullName()] {
						continue
					}
					places = append(places, string(field.FullName()))
					added = added || !leading[message.FullName()]
					leading[message.FullName()] = true
				}
			}
		}
		slices.Sort(places)

		Expect(places).To(Equal([]string{
			"flock.api.config.backend.v1.Backend.eds",
			"flock.api.config.backend.v1.Backend.health_check",
			"flock.api.config.backend.v1.EDS.headers",
			"flock.api.config.backend.v1.EDS.url",
			"flock.api.config.core.v1.HeaderValueOption.header",
			"flock.api.config.core.v1.HealthCheck.http_health_check",
			"flock.api.config.core.v1.HttpHealthCheck.request_headers_to_add",
			"flock.api.config.core.v1.URL.user_info",
			"flock.extensions.vs.starling_options.v1.HttpOptions.request_headers_to_add",
			"flock.extensions.vs.starling_options.v1.HttpOptions.response_headers_to_add",
		}), "a field above is new to FindRefs: teach secretFieldsOf in refs.go its place, then add it here")
	})
})
