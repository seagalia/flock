//go:build !linux

package l3logs_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/agent/starling/l3logs"
)

var _ = Describe("L3Logger on non linux platform", func() {
	It("should be a no-op calling after init", func() {
		called := false
		Expect(NewL3Logger(nil).Run(context.Background(), func() { called = true })).To(Succeed())
		Expect(called).To(BeTrue())
	})
})
