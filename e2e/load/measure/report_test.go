package measure_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/load/measure"
)

const mib = 1024 * 1024

var _ = Describe("Report", func() {
	It("renders a markdown table per side", func() {
		before := measure.Sample{HeapInuse: 10 * mib, HeapAlloc: 8 * mib, StackInuse: 2 * mib, Sys: 30 * mib, RSS: 25 * mib, ContainerMemory: 40 * mib, Goroutines: 40}
		after := measure.Sample{HeapInuse: 11 * mib, HeapAlloc: 7 * mib, StackInuse: 3 * mib, Sys: 30 * mib, RSS: 25 * mib, ContainerMemory: 40 * mib, Goroutines: 41}
		unsettled := before
		unsettled.Unsettled = true
		report := measure.Report{
			Date:   time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
			Commit: "abc1234",
			Scale:  "2",
			Tables: []measure.Table{{
				Title:         "controller",
				ContainerNote: "container = controller process + sqlite page cache",
				Rows: []measure.Row{
					{Phase: "seeded", Sample: before},
					{Phase: "api clients", Sample: unsettled},
					measure.Delta("churn delta", before, after),
				},
			}},
		}

		out := report.Render()

		Expect(out).To(ContainSubstring("# flock load report"))
		Expect(out).To(ContainSubstring("2026-10-25 12:00 UTC · abc1234 · LOAD_SCALE=2"))
		Expect(out).To(ContainSubstring("## controller"))
		Expect(out).To(ContainSubstring("container = controller process + sqlite page cache"))
		Expect(out).To(ContainSubstring("| phase | heap inuse | heap alloc | stack | sys | rss | container | goroutines |"))
		Expect(out).To(ContainSubstring("| seeded | 10.0 | 8.0 | 2.0 | 30.0 | 25.0 | 40.0 | 40 |"))
		Expect(out).To(ContainSubstring("| api clients (unsettled) |"))
		Expect(out).To(ContainSubstring("| churn delta | +1.0 | -1.0 | +1.0 | +0.0 | +0.0 | +0.0 | +1 |"))
	})

	It("marks a delta unsettled when one of its samples is", func() {
		before := measure.Sample{HeapInuse: 10 * mib}
		after := measure.Sample{HeapInuse: 11 * mib, Unsettled: true}
		report := measure.Report{Tables: []measure.Table{{Rows: []measure.Row{measure.Delta("churn delta", before, after)}}}}

		Expect(report.Render()).To(ContainSubstring("| churn delta (unsettled) | +1.0 |"))
	})
})
