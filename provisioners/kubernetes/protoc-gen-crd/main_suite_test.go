package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProtocGenCrd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "protoc-gen-crd Suite")
}
