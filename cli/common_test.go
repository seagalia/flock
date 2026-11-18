package cli

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func writeTempFile(name, content string) string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), name)
	Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
	return path
}

var _ = Describe("Common", func() {
	Describe("stringFilterToFilterMeta", func() {
		DescribeTable("parses metadata filters",
			func(filter, wantKey, wantValue string, wantOperator svccore.ListFilter_Operator) {
				Expect(stringFilterToFilterMeta(filter)).To(Equal(&svccore.ListFilter{
					Key:        wantKey,
					Value:      wantValue,
					Operator:   wantOperator,
					IsMetadata: true,
				}))
			},
			Entry("equal", "env == prod", "env", "prod", svccore.ListFilter_EQUAL),
			Entry("not equal", "env!=prod", "env", "prod", svccore.ListFilter_NOT_EQUAL),
			Entry("like", "team ~= net", "team", "net", svccore.ListFilter_LIKE),
			Entry("not like", "team !~ net", "team", "net", svccore.ListFilter_NOT_LIKE),
			Entry("splits on the first operator only", "url==http://a==b", "url", "http://a==b", svccore.ListFilter_EQUAL),
		)

		DescribeTable("ignores invalid filters",
			func(filter string) {
				Expect(stringFilterToFilterMeta(filter)).To(BeNil())
			},
			Entry("empty", ""),
			Entry("no operator", "env"),
			Entry("single equal", "env=prod"),
		)
	})

	Describe("stringFiltersToFiltersMeta", func() {
		It("returns nil without filters", func() {
			Expect(stringFiltersToFiltersMeta(nil)).To(BeNil())
		})

		It("skips invalid filters and keeps the order", func() {
			got := stringFiltersToFiltersMeta([]string{"env==prod", "invalid", "team~=net"})

			Expect(got).To(HaveLen(2))
			Expect(got[0].GetKey()).To(Equal("env"))
			Expect(got[1].GetKey()).To(Equal("team"))
		})
	})

	Describe("TypeFromShort", func() {
		It("resolves a short name case insensitively", func() {
			Expect(TypeFromShort("Backend")).To(Equal(helpers.TypeUrl[*backend.Backend]()))
		})

		It("returns empty for an unknown short name", func() {
			Expect(TypeFromShort("unknown")).To(BeEmpty())
		})
	})

	Describe("FileToProto", func() {
		It("returns an empty message, not loaded, for a missing file", func() {
			cl, loaded, err := FileToProto[*backend.Backend](filepath.Join(GinkgoT().TempDir(), "backend.yml"))

			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(BeFalse())
			Expect(cl).NotTo(BeNil())
		})

		DescribeTable("treats empty content as not loaded",
			func(content string) {
				_, loaded, err := FileToProto[*backend.Backend](writeTempFile("backend.json", content))

				Expect(err).NotTo(HaveOccurred())
				Expect(loaded).To(BeFalse())
			},
			Entry("empty", ""),
			Entry("blank", "   \n"),
			Entry("null", "null"),
			Entry("empty object", "{ }"),
			Entry("empty object with newline", "{}\n"),
		)

		DescribeTable("reads yaml and json files",
			func(name, content string) {
				cl, loaded, err := FileToProto[*backend.Backend](writeTempFile(name, content))

				Expect(err).NotTo(HaveOccurred())
				Expect(loaded).To(BeTrue())
				Expect(cl.GetName()).To(Equal("backend"))
				Expect(cl.GetSni()).To(Equal("backend.local"))
			},
			Entry("yml", "backend.yml", "name: backend\nsni: backend.local\n"),
			Entry("yaml", "backend.yaml", "name: backend\nsni: backend.local\n"),
			Entry("json", "backend.json", `{"name": "backend", "sni": "backend.local"}`),
		)

		It("fails on content that does not match the message", func() {
			_, _, err := FileToProto[*backend.Backend](writeTempFile("backend.json", `{"unknown_field": 1}`))

			Expect(err).To(MatchError(ContainSubstring("failed to unmarshal")))
		})
	})

	Describe("ProtoSkeletonToFile", func() {
		DescribeTable("writes a file FileToProto can read back",
			func(name string) {
				path := filepath.Join(GinkgoT().TempDir(), name)

				Expect(ProtoSkeletonToFile(&vip.VIP{Name: "public", Ip: "10.0.0.1"}, path)).To(Succeed())
				fromFile, loaded, err := FileToProto[*vip.VIP](path)

				Expect(err).NotTo(HaveOccurred())
				Expect(loaded).To(BeTrue())
				Expect(fromFile.GetName()).To(Equal("public"))
				Expect(fromFile.GetIp()).To(Equal("10.0.0.1"))
			},
			Entry("yaml", "vip.yml"),
			Entry("json", "vip.json"),
		)

		It("writes yaml for a yaml extension", func() {
			path := filepath.Join(GinkgoT().TempDir(), "vip.yaml")

			Expect(ProtoSkeletonToFile(&vip.VIP{Name: "public"}, path)).To(Succeed())

			content, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(content))).NotTo(HavePrefix("{"))
			Expect(string(content)).To(ContainSubstring("name: public"))
		})
	})

	Describe("ProtoToYaml", func() {
		It("uses proto names and emits unpopulated fields", func() {
			out, err := ProtoToYaml(&backend.Backend{Name: "backend"})

			Expect(err).NotTo(HaveOccurred())
			Expect(string(out)).To(ContainSubstring("name: backend"))
			Expect(string(out)).To(ContainSubstring("enable_tls: false"))
		})
	})

	Describe("NameFromAny", func() {
		It("reads the name of the packed message", func() {
			anyMsg, err := anypb.New(&backend.Backend{Name: "backend"})
			Expect(err).NotTo(HaveOccurred())

			Expect(NameFromAny(anyMsg)).To(Equal("backend"))
		})
	})

	Describe("ProtoDiffContent", func() {
		It("shows old and new values of a changed field", func() {
			from := &backend.Backend{UUID: "c1", Name: "backend", Sni: "old.local"}
			dest := proto.Clone(from).(*backend.Backend)
			dest.Sni = "new.local"

			diff, err := ProtoDiffContent(from, dest)

			Expect(err).NotTo(HaveOccurred())
			Expect(diff).To(ContainSubstring("old.local"))
			Expect(diff).To(ContainSubstring("new.local"))
		})

		It("reports nothing when messages are equal", func() {
			cl := &backend.Backend{UUID: "c1", Name: "backend"}

			diff, err := ProtoDiffContent(cl, proto.Clone(cl))

			Expect(err).NotTo(HaveOccurred())
			Expect(diff).NotTo(ContainSubstring("backend"))
		})
	})
})
