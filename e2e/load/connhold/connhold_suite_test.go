package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConnhold(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Connhold Suite")
}
