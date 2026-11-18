package multi

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMulti(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Multi Suite")
}
