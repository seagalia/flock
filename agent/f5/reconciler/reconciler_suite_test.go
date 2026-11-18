package reconciler_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
)

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconciler Suite")
}

// rt is the state of the agent app the specs run, made again before each spec so no spec depends on what another one
// left
var rt *runtime.Runtime

var _ = BeforeEach(func() {
	rt = runtime.New()
})
