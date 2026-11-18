package logging_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	. "github.com/seagalia/flock/sdk/logging"
)

func jsonLines(buf *bytes.Buffer) []map[string]any {
	decoded := make([]map[string]any, 0)
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		fields := map[string]any{}
		Expect(json.Unmarshal([]byte(line), &fields)).To(Succeed(), line)
		decoded = append(decoded, fields)
	}
	return decoded
}

var _ = Describe("Setup", func() {
	AfterEach(func() {
		Expect(Setup(Options{Console: GinkgoWriter})).To(Succeed())
	})

	Context("filter_components", func() {
		setup := func(rules ...string) *bytes.Buffer {
			buf := &bytes.Buffer{}
			Expect(Setup(Options{Level: "trace", InJson: true, FilterComponents: rules, Console: buf})).To(Succeed())
			return buf
		}
		// logAt writes one line of component at level into buf and tells if it was shown
		logAt := func(buf *bytes.Buffer, component string, level zerolog.Level) bool {
			buf.Reset()
			logger := Component(component)
			logger.WithLevel(level).Msg("hello")
			return len(jsonLines(buf)) == 1
		}

		It("shows lines without component in allow-list mode", func() {
			buf := setup("!other")
			log.Info().Msg("hello")
			Expect(jsonLines(buf)).To(HaveLen(1))
		})

		It("hides a filtered component from the given level", func() {
			buf := setup("aggregator=info")
			Expect(logAt(buf, "aggregator", zerolog.InfoLevel)).To(BeFalse())
			Expect(logAt(buf, "aggregator", zerolog.DebugLevel)).To(BeFalse())
			Expect(logAt(buf, "aggregator", zerolog.WarnLevel)).To(BeTrue())
			Expect(logAt(buf, "other", zerolog.DebugLevel)).To(BeTrue())
		})

		It("hides trace lines of a component filtered without level", func() {
			buf := setup("aggregator")
			Expect(logAt(buf, "aggregator", zerolog.TraceLevel)).To(BeFalse())
			Expect(logAt(buf, "aggregator", zerolog.DebugLevel)).To(BeTrue())
		})

		It("only shows components selected with '!'", func() {
			buf := setup("!aggregator")
			Expect(logAt(buf, "aggregator", zerolog.DebugLevel)).To(BeTrue())
			Expect(logAt(buf, "other", zerolog.ErrorLevel)).To(BeFalse())
		})

		It("only shows the given level of a component selected with '!' and a level", func() {
			buf := setup("!aggregator=debug")
			Expect(logAt(buf, "aggregator", zerolog.DebugLevel)).To(BeTrue())
			Expect(logAt(buf, "aggregator", zerolog.InfoLevel)).To(BeFalse())
		})

		It("rejects an unknown level", func() {
			err := Setup(Options{FilterComponents: []string{"aggregator=loud"}, Console: io.Discard})
			Expect(err).To(MatchError(ContainSubstring("aggregator")))
		})
	})

	Context("level", func() {
		It("accepts warning as warn", func() {
			Expect(ParseLevel("warning")).To(Equal(zerolog.WarnLevel))
			_, err := ParseLevel("loud")
			Expect(err).To(HaveOccurred())
		})

		It("uses info when level is empty", func() {
			buf := &bytes.Buffer{}
			Expect(Setup(Options{InJson: true, Console: buf})).To(Succeed())
			log.Debug().Msg("hidden")
			log.Info().Msg("shown")
			Expect(jsonLines(buf)).To(ConsistOf(HaveKeyWithValue("message", "shown")))
		})
	})

	Context("text output", func() {
		textOf := func(opts Options) string {
			buf := &bytes.Buffer{}
			opts.Console = buf
			Expect(Setup(opts)).To(Succeed())
			logger := Component("app")
			logger.Info().Msg("hello")
			return buf.String()
		}

		It("writes colorless text when no_color", func() {
			out := textOf(Options{NoColor: true})
			Expect(out).To(ContainSubstring("INF"))
			Expect(out).To(ContainSubstring("hello"))
			Expect(out).To(ContainSubstring("component=app"))
			Expect(out).ToNot(ContainSubstring("\x1b["))
		})

		It("writes colored text when force_colors", func() {
			Expect(textOf(Options{ForceColors: true})).To(ContainSubstring("\x1b["))
		})

		It("no_color wins over force_colors", func() {
			Expect(textOf(Options{ForceColors: true, NoColor: true})).ToNot(ContainSubstring("\x1b["))
		})

		It("writes no color to a non terminal by default", func() {
			Expect(textOf(Options{})).ToNot(ContainSubstring("\x1b["))
		})
	})

	It("logger created before Setup follows it", func() {
		early := Component("early")
		buf := &bytes.Buffer{}
		Expect(Setup(Options{InJson: true, Console: buf})).To(Succeed())
		early.Info().Msg("hello")
		Expect(jsonLines(buf)).To(ConsistOf(HaveKeyWithValue("component", "early")))
	})

	It("sends filtered colored text to the listener and every line to syslog", func() {
		console, listener, syslog := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
		Expect(Setup(Options{
			NoColor:          true,
			InJson:           true,
			FilterComponents: []string{"aggregator=info"},
			Console:          console,
			Listener:         listener,
			Syslog:           syslog,
		})).To(Succeed())

		aggregator := Component("aggregator")
		aggregator.Info().Msg("hidden")
		Expect(console.String()).To(BeEmpty())
		Expect(listener.String()).To(BeEmpty())
		Expect(jsonLines(syslog)).To(ConsistOf(HaveKeyWithValue("component", "aggregator")))

		aggregator.Warn().Msg("shown")
		Expect(listener.String()).To(ContainSubstring("shown"))
		Expect(listener.String()).To(ContainSubstring("\x1b["))
		Expect(jsonLines(console)).To(HaveLen(1))
	})

	It("does not allocate for a disabled level", func() {
		Expect(Setup(Options{Console: io.Discard})).To(Succeed())
		logger := Component("bench")
		allocs := testing.AllocsPerRun(100, func() {
			logger.Debug().Str("vip", "10.0.0.1").Msg("adding vip")
		})
		Expect(allocs).To(BeZero())
	})
})
