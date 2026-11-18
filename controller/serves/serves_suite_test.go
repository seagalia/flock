package serves_test

import (
	"testing"

	"github.com/seagalia/flock/controller/auth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDiscoverers(t *testing.T) {
	auth.SetTestMode(true)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Serves Suite")
}

//go:generate go tool mockgen -destination=mock_ads_drs_test.go -package=serves_test github.com/seagalia/flock/controller/serves DiscoveryStream
