package core_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Core Suite")
}

func durationpbOf(d time.Duration) *durationpb.Duration {
	return durationpb.New(d)
}
