package measure_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/load/measure"
)

const metricsFixture = `# TYPE go_memstats_heap_inuse_bytes gauge
go_memstats_heap_inuse_bytes{target="agent"} 1.048576e+07
# TYPE go_memstats_heap_alloc_bytes gauge
go_memstats_heap_alloc_bytes{target="agent"} 8e+06
# TYPE go_memstats_stack_inuse_bytes gauge
go_memstats_stack_inuse_bytes{target="agent"} 2.097152e+06
# TYPE go_memstats_sys_bytes gauge
go_memstats_sys_bytes{target="agent"} 3e+07
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes{target="agent"} 2.5e+07
# TYPE go_goroutines gauge
go_goroutines{target="agent"} 42
`

var _ = Describe("ParseSample", func() {
	It("reads the memory metrics", func() {
		sample, err := measure.ParseSample(metricsFixture)
		Expect(err).ToNot(HaveOccurred())
		Expect(sample).To(Equal(measure.Sample{
			HeapInuse:  10485760,
			HeapAlloc:  8000000,
			StackInuse: 2097152,
			Sys:        30000000,
			RSS:        25000000,
			Goroutines: 42,
		}))
	})

	It("fails on a missing metric instead of reading 0", func() {
		withoutRss := strings.ReplaceAll(metricsFixture, "process_resident_memory_bytes", "other_bytes")
		_, err := measure.ParseSample(withoutRss)
		Expect(err).To(MatchError(ContainSubstring("process_resident_memory_bytes")))
	})
})
