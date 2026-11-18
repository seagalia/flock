package devenv

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("loginCommand", func() {
	It("should single-quote a plain password", func() {
		Expect(loginCommand("myhost", "s3cret")).To(Equal("flock login -t myhost:8443 -u admin -p 's3cret' -k"))
	})

	It("should keep a space inside the quotes", func() {
		Expect(loginCommand("myhost", "a b$!&")).To(Equal("flock login -t myhost:8443 -u admin -p 'a b$!&' -k"))
	})

	It("should escape a single quote for a POSIX shell", func() {
		Expect(loginCommand("myhost", "it's")).To(Equal(`flock login -t myhost:8443 -u admin -p 'it'\''s' -k`))
	})

	It("should use the long form for a password starting with a dash, kong would read it as a flag", func() {
		Expect(loginCommand("myhost", "-x")).To(Equal("flock login -t myhost:8443 -u admin --password='-x' -k"))
	})
})

var _ = Describe("controllerNames", func() {
	It("should trim the hostnames, FLOCK_DEV_HOSTNAMES=\"a, b\" leaves a space before the second", func() {
		names, err := controllerNames([]string{"a", " b"}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(names).To(Equal([]string{"a", "b"}))
	})
})

var _ = Describe("checkPortFree", func() {
	It("should refuse an address something already answers on", func(ctx SpecContext) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(ln.Close)

		err = checkPortFree(ctx, ln.Addr().String())
		Expect(err).To(MatchError(ContainSubstring("something already answers on " + ln.Addr().String())))
	})

	It("should accept an address nothing answers on", func(ctx SpecContext) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		addr := ln.Addr().String()
		Expect(ln.Close()).To(Succeed())

		Expect(checkPortFree(ctx, addr)).To(Succeed())
	})
})
