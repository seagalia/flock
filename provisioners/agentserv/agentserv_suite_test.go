package agentserv_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAgentserv(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Provisioner Agent Server Suite")
}
