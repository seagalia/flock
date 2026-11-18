package bigip_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBigip(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BIG-IP Suite")
}
