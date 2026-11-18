package cli

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("explain", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("lists the resources and the extensions without calling the controller", func() {
		out, err := runner.run("explain")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("vs           flock.api.config.vs.v1.VirtualServer"))
		Expect(out).To(ContainSubstring("flock.extensions.vs.starling_options.v1.HttpOptions"))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("explains the fields of a resource with their comments, rules and oneof", func() {
		out, err := runner.run("explain", "vs")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("port: uint32\n    Port the VirtualServer listens on, on each of its VIPs.\n    rules: {\"uint32\":{\"lte\":65535,\"gt\":0}}"))
		Expect(out).To(ContainSubstring("oneof: route_config (set exactly one)"))
		Expect(out).To(ContainSubstring("→ flock explain vs http_routes_config"))
	})

	It("explains the message of a field path", func() {
		out, err := runner.run("explain", "vs", "http_routes_config")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("flock.api.config.vs.v1.HttpRoutesConfig"))
		Expect(out).To(ContainSubstring("- flock.extensions.vs.starling_options.v1.HttpOptions"))
		Expect(out).To(ContainSubstring("→ flock explain vs http_routes_config.virtual_hosts"))
	})

	It("explains an extension in json", func() {
		out, err := runner.run("explain", "flock.extensions.vs.starling_options.v1.HttpOptions", "--json")

		Expect(err).NotTo(HaveOccurred())
		var explanation messageExplanation
		Expect(json.Unmarshal([]byte(out), &explanation)).To(Succeed())
		Expect(explanation.Fields[0].Name).To(Equal("codec_type"))
		Expect(explanation.Fields[0].EnumValues).To(Equal([]string{"AUTO", "HTTP1", "HTTP2"}))
		Expect(explanation.Fields[1].Comment).To(Equal("RequestHeadersToAdd are added to each request before it is sent to a backend, at most 1000."))
	})

	It("lists the fields when the path doesn't exist", func() {
		_, err := runner.run("explain", "vs", "nope")

		Expect(err).To(MatchError(ContainSubstring("has no field nope, fields are: UUID, name, port")))
	})

	It("refuses a path through a scalar", func() {
		_, err := runner.run("explain", "vs", "port")

		Expect(err).To(MatchError(ContainSubstring("is a uint32, not a message")))
	})
})
