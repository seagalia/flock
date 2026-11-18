package devenv

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDevenv(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Devenv Suite")
}
