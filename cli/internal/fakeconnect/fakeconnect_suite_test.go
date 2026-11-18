package fakeconnect_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFakeconnect(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fakeconnect Suite")
}
