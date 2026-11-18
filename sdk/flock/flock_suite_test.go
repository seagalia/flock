package flock_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFlock(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Flock Suite")
}
