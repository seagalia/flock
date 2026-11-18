package devenv

import (
	"bytes"
	"context"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("confirmPrivileged", func() {
	var out *bytes.Buffer

	BeforeEach(func() { out = &bytes.Buffer{} })

	It("should agree with yes and read nothing", func() {
		Expect(confirmPrivileged(context.Background(), nil, false, out, true)).To(Succeed())
	})

	It("should ask for --yes with no terminal", func() {
		err := confirmPrivileged(context.Background(), strings.NewReader("y\n"), false, out, false)
		Expect(err).To(MatchError(ContainSubstring("--yes")))
	})

	It("should agree with y or yes whatever the case", func() {
		for _, answer := range []string{"y\n", "YES\n"} {
			err := confirmPrivileged(context.Background(), strings.NewReader(answer), true, out, false)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("should decline anything else", func() {
		err := confirmPrivileged(context.Background(), strings.NewReader("n\n"), true, out, false)
		Expect(err).To(MatchError("privileged mode declined"))
	})

	It("should list what changes on the machine", func() {
		Expect(confirmPrivileged(context.Background(), strings.NewReader("y\n"), true, out, false)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("inet flock"))
		Expect(out.String()).To(ContainSubstring("flock0"))
	})

	It("should return when stopped while reading", func() {
		pr, pw := io.Pipe()
		defer pw.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := confirmPrivileged(ctx, pr, true, out, false)

		Expect(err).To(MatchError(context.Canceled))
	})
})

var _ = Describe("parseCapEff", func() {
	It("should read the effective capabilities", func() {
		status := "Name:\tflock\nCapEff:\t000001ffffffffff\nCapBnd:\t000001ffffffffff\n"
		caps, err := parseCapEff(strings.NewReader(status))
		Expect(err).NotTo(HaveOccurred())
		Expect(caps & (1 << 12)).NotTo(BeZero())
	})

	It("should fail with no CapEff line", func() {
		_, err := parseCapEff(strings.NewReader("Name:\tflock\n"))
		Expect(err).To(HaveOccurred())
	})
})
