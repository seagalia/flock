package eds_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEds(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Docker Provisioner EDS Suite")
}
