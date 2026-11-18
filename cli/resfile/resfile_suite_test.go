package resfile_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestResfile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Resfile Suite")
}
