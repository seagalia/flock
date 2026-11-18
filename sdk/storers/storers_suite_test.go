package storers_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStorers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Storers Suite")
}
