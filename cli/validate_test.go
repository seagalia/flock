package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validate", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("accepts a valid file without calling the controller", func() {
		file := writeTempFile("vs.yml", "name: web\nport: 80\ntcp_route_config:\n  backend_uuid: "+resourceUuid)

		out, err := runner.run("validate", "vs", "-f", file)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("is valid"))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("lists every violation with its field path", func() {
		file := writeTempFile("vs.yml", "port: 0\ntcp_route_config:\n  backend_uuid: ''")

		out, err := runner.run("validate", "vs", "-f", file)

		Expect(err).To(MatchError(ContainSubstring("3 violation(s)")))
		Expect(out).To(ContainSubstring("name: must be at least 1 characters"))
		Expect(out).To(ContainSubstring("port: must be greater than 0"))
		Expect(out).To(ContainSubstring("tcp_route_config.backend_uuid: must be at least 1 characters"))
	})

	It("validates the typed_options packed in the resource", func() {
		file := writeTempFile("vs.yml", `
name: web
port: 80
http_routes_config:
  virtual_hosts:
  - routes:
    - backend_uuid: `+resourceUuid+`
  typed_options:
    "@type": type.googleapis.com/flock.extensions.vs.starling_options.v1.HttpOptions
    request_headers_to_remove: [""]
`)

		out, err := runner.run("validate", "vs", "-f", file)

		Expect(err).To(MatchError(ContainSubstring("is not valid")))
		Expect(out).To(ContainSubstring("http_routes_config.typed_options.request_headers_to_remove[0]: must be at least 1 characters"))
	})

	It("reports an unknown field", func() {
		file := writeTempFile("vs.yml", "name: web\nprot: 80")

		_, err := runner.run("validate", "vs", "-f", file)

		Expect(err).To(MatchError(ContainSubstring(`unknown field "prot"`)))
	})

	It("fails on a missing file", func() {
		_, err := runner.run("validate", "backend", "-f", "/does/not/exist.yml")

		Expect(err).To(MatchError(ContainSubstring("empty or missing")))
	})
})
