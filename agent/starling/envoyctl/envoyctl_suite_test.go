package envoyctl

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestEnvoyctl(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Envoyctl Suite")
}

// rt is the state of the agent app the specs run, made again before each spec so no spec depends on what another one
// left
var rt *runtime.Runtime

var _ = BeforeEach(func() {
	rt = runtime.New()
})

// validatable is the same validation envoy does on resources received through xds
type validatable interface {
	ValidateAll() error
}

func expectValidEnvoyConfig(message validatable) {
	GinkgoHelper()
	Expect(message.ValidateAll()).To(Succeed())
}

func unmarshalAny[T proto.Message](anyMsg *anypb.Any, message T) T {
	GinkgoHelper()
	Expect(anyMsg).ToNot(BeNil())
	Expect(anyMsg.UnmarshalTo(message)).To(Succeed())
	if v, ok := any(message).(validatable); ok {
		expectValidEnvoyConfig(v)
	}
	return message
}

func anyOf(message proto.Message) (*anypb.Any, error) {
	return anypb.New(message)
}
