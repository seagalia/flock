package highlight

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHighlight(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Highlight Suite")
}
