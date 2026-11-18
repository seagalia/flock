package rpcserver_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRpcserver(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rpcserver Suite")
}
