package tabs

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTabs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tabs Suite")
}
