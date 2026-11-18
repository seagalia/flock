package protoset

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProtoset(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Protoset Suite")
}
