package cplters

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCplters(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cplters Suite")
}
