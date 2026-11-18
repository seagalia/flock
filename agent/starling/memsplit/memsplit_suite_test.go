package memsplit_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMemsplit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Memsplit Suite")
}
