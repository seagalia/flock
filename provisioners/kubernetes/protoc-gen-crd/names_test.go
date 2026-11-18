package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("names", func() {
	DescribeTable("typeName",
		func(protoName, expected string) {
			Expect(typeName(protoName)).To(Equal(expected))
		},
		Entry("an acronym written in title case", "HttpHealthCheck", "HTTPHealthCheck"),
		Entry("an uppercase run", "IPAddrGroup", "IPAddrGroup"),
		Entry("digits staying with their word", "Http2ProtocolOptions", "HTTP2ProtocolOptions"),
	)

	DescribeTable("fieldNames",
		func(protoName, goName, jsonName string) {
			gotGo, gotJSON := fieldNames(protoName)
			Expect(gotGo).To(Equal(goName))
			Expect(gotJSON).To(Equal(jsonName))
		},
		Entry("an acronym at the end", "enable_tls", "EnableTLS", "enableTLS"),
		Entry("an acronym at the start", "ip_addr_group", "IPAddrGroup", "ipAddrGroup"),
		Entry("a plain word", "metadata", "Metadata", "metadata"),
		Entry("a name in PascalCase", "PathPrefix", "PathPrefix", "pathPrefix"),
	)

	DescribeTable("enumValue",
		func(protoName, expected string) {
			Expect(enumValue(protoName)).To(Equal(expected))
		},
		Entry("words", "ROUND_ROBIN", "RoundRobin"),
		Entry("an HTTP method", "OPTIONS", "OPTIONS"),
		Entry("an acronym", "V4_ONLY", "V4Only"),
	)
})
