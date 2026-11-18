package lockers_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLockers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Lockers Suite")
}
