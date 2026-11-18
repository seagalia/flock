package config_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"

	. "github.com/seagalia/flock/sdk/config"
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
	)

	It("should reject invalid durations", func() {
		for _, raw := range []string{"", "10x", "300y"} {
			_, err := ParseDuration(raw)
			Expect(err).To(HaveOccurred(), raw)
		}
	})

	It("should be read and written in yaml, json, text and as flag", func() {
		var holder struct {
			Timeout Duration `yaml:"timeout" json:"timeout"`
		}
		Expect(yaml.Unmarshal([]byte("timeout: 2m"), &holder)).To(Succeed())
		Expect(time.Duration(holder.Timeout)).To(Equal(2 * time.Minute))

		Expect(json.Unmarshal([]byte(`{"timeout":"5s"}`), &holder)).To(Succeed())
		Expect(time.Duration(holder.Timeout)).To(Equal(5 * time.Second))
		out, err := json.Marshal(holder)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(MatchJSON(`{"timeout":"5s"}`))
		Expect(json.Unmarshal([]byte(`{"timeout":5}`), &holder)).ToNot(Succeed())

		var d Duration
		Expect(d.UnmarshalText([]byte("1h"))).To(Succeed())
		text, err := d.MarshalText()
		Expect(err).ToNot(HaveOccurred())
		Expect(string(text)).To(Equal("1h"))

		Expect(d.Set("3s")).To(Succeed())
		Expect(time.Duration(d)).To(Equal(3 * time.Second))
		Expect(d.Type()).To(Equal("duration"))
	})
})
