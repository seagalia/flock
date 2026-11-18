package models_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"

	. "github.com/seagalia/flock/sdk/models"
)

var _ = Describe("Duration", func() {
	DescribeTable("should parse and format durations",
		func(raw string, expected time.Duration) {
			parsed, err := ParseDuration(raw)
			Expect(err).ToNot(HaveOccurred())
			Expect(time.Duration(parsed)).To(Equal(expected))
			Expect(parsed.String()).To(Equal(raw))
		},
		Entry("milliseconds", "150ms", 150*time.Millisecond),
		Entry("compound", "1h30m", 90*time.Minute),
		Entry("days", "2d", 48*time.Hour),
		Entry("weeks", "1w", 7*24*time.Hour),
		Entry("years", "1y", 365*24*time.Hour),
	)

	It("should accept 0 without unit", func() {
		parsed, err := ParseDuration("0")
		Expect(err).ToNot(HaveOccurred())
		Expect(parsed.String()).To(Equal("0s"))
	})

	DescribeTable("should reject invalid durations",
		func(raw string) {
			_, err := ParseDuration(raw)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("unknown unit", "10x"),
		Entry("wrong order", "1m1h"),
		Entry("overflow", "300y"),
	)

	It("should be read and written in yaml", func() {
		var holder struct {
			Timeout Duration `yaml:"timeout"`
		}
		Expect(yaml.Unmarshal([]byte("timeout: 1m30s"), &holder)).To(Succeed())
		Expect(time.Duration(holder.Timeout)).To(Equal(90 * time.Second))

		out, err := yaml.Marshal(holder)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("timeout: 1m30s\n"))

		Expect(yaml.Unmarshal([]byte("timeout: nope"), &holder)).ToNot(Succeed())
	})
})
