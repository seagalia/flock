package procmemory_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProcmemory(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Procmemory Suite")
}
