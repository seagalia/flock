package main

import (
	"os"
	"strings"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// the descriptor set make generate writes, the only one at hand that keeps the comments the Any fields are read from
const protosetPath = "../../../cli/protoset/protoset.bin"

func readProtoset() *descriptorpb.FileDescriptorSet {
	content, err := os.ReadFile(protosetPath)
	Expect(err).NotTo(HaveOccurred())
	set := &descriptorpb.FileDescriptorSet{}
	Expect(proto.Unmarshal(content, set)).To(Succeed())
	return set
}

func newPlugin(set *descriptorpb.FileDescriptorSet) *protogen.Plugin {
	request := &pluginpb.CodeGeneratorRequest{ProtoFile: set.GetFile()}
	for _, file := range set.GetFile() {
		request.FileToGenerate = append(request.FileToGenerate, file.GetName())
	}
	plugin, err := protogen.Options{}.New(request)
	Expect(err).NotTo(HaveOccurred())
	return plugin
}

// generatedContent joins the generated files whose name ends with suffix.
func generatedContent(plugin *protogen.Plugin, suffix string) string {
	var content strings.Builder
	for _, file := range plugin.Response().GetFile() {
		if strings.HasSuffix(file.GetName(), suffix) {
			content.WriteString(file.GetContent())
		}
	}
	return content.String()
}

func findField(set *descriptorpb.FileDescriptorSet, message, field string) *descriptorpb.FieldDescriptorProto {
	for _, file := range set.GetFile() {
		for _, candidate := range file.GetMessageType() {
			if file.GetPackage()+"."+candidate.GetName() != message {
				continue
			}
			for _, fieldProto := range candidate.GetField() {
				if fieldProto.GetName() == field {
					return fieldProto
				}
			}
		}
	}
	Fail("field " + message + "." + field + " not found")
	return nil
}

var _ = Describe("generate", func() {
	var (
		set   *descriptorpb.FileDescriptorSet
		roots []protoreflect.FullName
	)

	BeforeEach(func() {
		set = readProtoset()
		roots = []protoreflect.FullName{
			"flock.api.config.zone.v1.Zone", "flock.api.config.instance.v1.Instance", "flock.api.config.cert.v1.Certificate",
			"flock.api.config.backend.v1.Backend", "flock.api.config.vip.v1.VIP", "flock.api.config.vs.v1.VirtualServer",
		}
	})

	It("writes the specs and their conversion for the roots", func() {
		plugin := newPlugin(set)

		Expect(generate(plugin, roots, defaultedFields())).To(Succeed())

		spec := generatedContent(plugin, ".spec.go")
		Expect(spec).To(ContainSubstring("type BackendSpec struct"))
		Expect(spec).To(ContainSubstring("type ZoneSpec struct"))
		Expect(spec).To(ContainSubstring("Starling *StarlingBackendOptions `json:\"starling,omitempty\"`"))
		Expect(spec).To(ContainSubstring("// +listType=map\n\t// +listMapKey=name\n\tPools []IPPool"))
		Expect(generatedContent(plugin, ".convert.go")).To(ContainSubstring(
			"func (in *BackendSpec) ToProto(ctx context.Context, refs Resolver) (*backendv1.Backend, error)"))
	})

	It("makes a required field optional when ProvDefault fills it", func() {
		withDefaults := newPlugin(set)
		withoutDefaults := newPlugin(readProtoset())

		Expect(generate(withDefaults, roots, defaultedFields())).To(Succeed())
		Expect(generate(withoutDefaults, roots, map[protoreflect.FullName]bool{})).To(Succeed())

		refreshRate := "// +kubebuilder:validation:Minimum=1\n\t// +kubebuilder:validation:ExclusiveMinimum=true\n\tRefreshRateMilliseconds"
		Expect(generatedContent(withDefaults, ".spec.go")).To(ContainSubstring("// +optional\n\t" + refreshRate))
		Expect(generatedContent(withoutDefaults, ".spec.go")).To(ContainSubstring("// +required\n\t" + refreshRate))
	})

	It("unrolls a message holding itself down to its depth", func() {
		plugin := newPlugin(set)

		Expect(generate(plugin, roots, defaultedFields())).To(Succeed())

		spec := generatedContent(plugin, ".spec.go")
		Expect(spec).To(ContainSubstring("Descriptors []StarlingDescriptorLevel4"))
		Expect(spec).NotTo(ContainSubstring("StarlingDescriptorLevel5"))
		Expect(spec).To(ContainSubstring("The CRD stops descriptors at this depth (4)"))
	})

	It("refuses a message holding itself with no depth", func() {
		depths := recursionDepths
		DeferCleanup(func() { recursionDepths = depths })
		recursionDepths = map[protoreflect.FullName]int{}
		plugin := newPlugin(set)

		err := generate(plugin, roots, defaultedFields())

		Expect(err).To(MatchError(ContainSubstring(
			"flock.extensions.limit.starling_limit.v1.Descriptor holds itself, add it to recursionDepths")))
	})

	It("checks the items of a list of strings", func() {
		plugin := newPlugin(set)

		Expect(generate(plugin, roots, defaultedFields())).To(Succeed())

		spec := generatedContent(plugin, ".spec.go")
		Expect(spec).To(ContainSubstring("// +kubebuilder:validation:items:MaxLength=256\n" +
			"\t// +kubebuilder:validation:items:Pattern=" + `"^:?[0-9a-zA-Z!#$%&'*+.\\-^_|~\\x60]+$"`))
		Expect(spec).To(ContainSubstring(`// +kubebuilder:validation:items:XValidation:rule="!(self in ['host'`))
	})

	It("adds the references of an override, which the conversion leaves out", func() {
		plugin := newPlugin(set)

		Expect(generate(plugin, roots, defaultedFields())).To(Succeed())

		Expect(generatedContent(plugin, ".spec.go")).To(MatchRegexp(
			"type CertificateSpec struct {(?s:.)*SecretRef ObjectRef `json:\"secretRef,omitzero\"`"))
		Expect(generatedContent(plugin, ".spec.go")).NotTo(ContainSubstring("CertChain"))
		convert := generatedContent(plugin, ".convert.go")
		certificateStart := strings.Index(convert, "func (in *CertificateSpec) ToProto")
		certificateLength := strings.Index(convert[certificateStart:], "\n}\n")
		Expect(convert[certificateStart : certificateStart+certificateLength]).NotTo(ContainSubstring("SecretRef"))
	})

	It("writes the types of a kind in its own files and the shared ones in the common files", func() {
		plugin := newPlugin(set)

		Expect(generate(plugin, roots, defaultedFields())).To(Succeed())

		Expect(generatedContent(plugin, "zz_generated.backend.spec.go")).To(ContainSubstring("type BackendSpec struct"))
		Expect(generatedContent(plugin, "zz_generated.virtualserver.spec.go")).To(
			ContainSubstring("type StarlingDescriptor struct"))
		Expect(generatedContent(plugin, "zz_generated.common.spec.go")).To(ContainSubstring("type HeaderValue struct"))
		Expect(generatedContent(plugin, "zz_generated.common.convert.go")).To(
			ContainSubstring("func (in *HeaderValue) ToProto"))
		Expect(generatedContent(plugin, "zz_generated.backend.spec.go")).NotTo(ContainSubstring("type HeaderValue struct"))
	})

	It("refuses a root it can't find", func() {
		plugin := newPlugin(set)

		err := generate(plugin, []protoreflect.FullName{"flock.api.config.nope.v1.Nope"}, defaultedFields())

		Expect(err).To(MatchError(ContainSubstring("flock.api.config.nope.v1.Nope: root message not found")))
	})

	It("refuses a string with no maximum", func() {
		findField(set, "flock.api.config.zone.v1.Zone", "zone").Options = &descriptorpb.FieldOptions{}
		plugin := newPlugin(set)

		err := generate(plugin, roots, defaultedFields())

		Expect(err).To(MatchError(ContainSubstring("flock.api.config.zone.v1.Zone.zone: no maximum")))
	})

	It("refuses a rule it doesn't know", func() {
		options := &descriptorpb.FieldOptions{}
		proto.SetExtension(options, validate.E_Field, validate.FieldRules_builder{
			String: validate.StringRules_builder{MaxLen: proto.Uint64(253), Email: new(true)}.Build(),
		}.Build())
		findField(set, "flock.api.config.zone.v1.Zone", "zone").Options = options
		plugin := newPlugin(set)

		err := generate(plugin, roots, defaultedFields())

		Expect(err).To(MatchError(ContainSubstring("flock.api.config.zone.v1.Zone.zone: well known string rule email is not supported")))
	})
})
