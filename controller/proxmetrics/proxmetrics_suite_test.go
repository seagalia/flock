package proxmetrics_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProxmetrics(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Proxmetrics Suite")
}
