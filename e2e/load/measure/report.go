package measure

import (
	"fmt"
	"strings"
	"time"
)

// DeltaValues is a signed difference between two samples
type DeltaValues struct {
	HeapInuse, HeapAlloc, StackInuse, Sys, RSS, ContainerMemory, Goroutines int64
	// Unsettled is set when one of the two samples was, the difference is then not a leak to trust
	Unsettled bool
}

// Row is a phase: Render prints Delta when set, Sample otherwise
type Row struct {
	Phase  string
	Sample Sample
	Delta  *DeltaValues
}

type Table struct {
	Title string
	// ContainerNote says what the container memory holds besides the flock process
	ContainerNote string
	Rows          []Row
}

type Report struct {
	Date   time.Time
	Commit string
	Scale  string
	Tables []Table
}

// Delta is the row of after - before, to read a leak at a glance
func Delta(phase string, before, after Sample) Row {
	diff := func(a, b uint64) int64 { return int64(a) - int64(b) }
	return Row{Phase: phase, Delta: &DeltaValues{
		HeapInuse:       diff(after.HeapInuse, before.HeapInuse),
		HeapAlloc:       diff(after.HeapAlloc, before.HeapAlloc),
		StackInuse:      diff(after.StackInuse, before.StackInuse),
		Sys:             diff(after.Sys, before.Sys),
		RSS:             diff(after.RSS, before.RSS),
		ContainerMemory: diff(after.ContainerMemory, before.ContainerMemory),
		Goroutines:      int64(after.Goroutines - before.Goroutines),
		Unsettled:       before.Unsettled || after.Unsettled,
	}}
}

// Render gives the report in markdown, bytes in MiB
func (r Report) Render() string {
	var out strings.Builder
	out.WriteString("# flock load report\n\n")
	fmt.Fprintf(&out, "%s · %s · LOAD_SCALE=%s\n", r.Date.UTC().Format("2006-01-02 15:04 UTC"), r.Commit, r.Scale)
	for _, table := range r.Tables {
		fmt.Fprintf(&out, "\n## %s\n\n%s\n\n", table.Title, table.ContainerNote)
		out.WriteString("| phase | heap inuse | heap alloc | stack | sys | rss | container | goroutines |\n")
		out.WriteString("|---|---|---|---|---|---|---|---|\n")
		for _, row := range table.Rows {
			out.WriteString(renderRow(row))
		}
	}
	return out.String()
}

func renderRow(row Row) string {
	phase := row.Phase
	if row.Delta != nil {
		d := row.Delta
		if d.Unsettled {
			phase += " (unsettled)"
		}
		signed := func(bytes int64) string { return fmt.Sprintf("%+.1f", float64(bytes)/mib) }
		return fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %+d |\n", phase,
			signed(d.HeapInuse), signed(d.HeapAlloc), signed(d.StackInuse), signed(d.Sys), signed(d.RSS), signed(d.ContainerMemory), d.Goroutines)
	}
	s := row.Sample
	if s.Unsettled {
		phase += " (unsettled)"
	}
	unsigned := func(bytes uint64) string { return fmt.Sprintf("%.1f", float64(bytes)/mib) }
	return fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %d |\n", phase,
		unsigned(s.HeapInuse), unsigned(s.HeapAlloc), unsigned(s.StackInuse), unsigned(s.Sys), unsigned(s.RSS), unsigned(s.ContainerMemory), s.Goroutines)
}

const mib = 1024 * 1024
