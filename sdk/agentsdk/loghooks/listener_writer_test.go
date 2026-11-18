package loghooks_test

import (
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/loghooks"
)

type lineListener struct {
	lines chan string
}

func (l *lineListener) OnLogReceive(logLine string) {
	l.lines <- logLine
}

var _ = Describe("ListenerWriter", func() {
	var writer *ListenerWriter

	BeforeEach(func() {
		writer = NewListenerWriter()
	})

	write := func(line string) {
		_, err := writer.Write([]byte(line))
		Expect(err).ToNot(HaveOccurred())
	}

	It("gives lines to listeners until they are removed", func() {
		listener := &lineListener{lines: make(chan string, 10)}
		writer.AddListener(listener)
		write("first")
		Eventually(listener.lines).Should(Receive(Equal("first")))

		writer.RemoveListener(listener)
		write("second")
		Consistently(listener.lines, "100ms").ShouldNot(Receive())
	})

	It("keeps the last 500 lines", func() {
		Expect(writer.LatestLines()).To(BeEmpty())
		for i := range 510 {
			write(fmt.Sprint(i))
		}
		lines := writer.LatestLines()
		Expect(lines).To(HaveLen(500))
		Expect(lines[0]).To(Equal("10"))
		Expect(lines[499]).To(Equal("509"))
	})

	It("copies the line", func() {
		line := []byte("first")
		_, err := writer.Write(line)
		Expect(err).ToNot(HaveOccurred())
		copy(line, "xxxxx")
		Expect(writer.LatestLines()).To(Equal([]string{"first"}))
	})

	It("accepts concurrent writes", func() {
		wg := sync.WaitGroup{}
		for range 10 {
			wg.Go(func() {
				for range 100 {
					_, _ = writer.Write([]byte("line"))
				}
			})
		}
		wg.Wait()
		Expect(writer.LatestLines()).To(HaveLen(500))
	})
})
