package adshandlers_test

import (
	"context"

	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/logging"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/storers"
)

func TestAdsHandlers(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "AdsHandlers Suite")
}

// the state of the agent app the specs run, made again before each spec so no spec depends on what another one left
var (
	store       storers.StoreManager
	prevStore   storers.StoreManager
	states      *stores.StateStore
	secretLinks *stores.SecretLinks
	rt          *runtime.Runtime
)

var _ = BeforeEach(func() {
	rt = runtime.New()
	store = rt.Store
	prevStore = rt.PrevStore
	states = rt.States
	secretLinks = rt.SecretLinks
})

func makeBackend(uuid string) *backend.Backend {
	return &backend.Backend{UUID: uuid, Name: uuid}
}

func makeResource(message proto.Message, uuid string) *svcdiscovery.Resource {
	packed, err := anypb.New(message)
	Expect(err).ToNot(HaveOccurred())
	return &svcdiscovery.Resource{Uuid: uuid, Signature: "sig-" + uuid, Resource: packed}
}

// handlers receive unmarshalled copies, so messages are compared by content
type protoEq struct {
	expected proto.Message
}

func ProtoEq(expected proto.Message) gomock.Matcher {
	return protoEq{expected: expected}
}

func (m protoEq) Matches(x any) bool {
	actual, ok := x.(proto.Message)
	return ok && proto.Equal(m.expected, actual)
}

func (m protoEq) String() string {
	return fmt.Sprintf("is proto equal to %v", m.expected)
}

type failingReadManager struct {
	*storers.MemoryManager
	readErr error
}

func (m failingReadManager) Backends() storers.Storer[*backend.Backend] {
	return failingReadStorer{Storer: m.MemoryManager.Backends(), readErr: m.readErr}
}

type failingReadStorer struct {
	storers.Storer[*backend.Backend]
	readErr error
}

func (s failingReadStorer) Read(context.Context, string) (*backend.Backend, error) {
	return nil, s.readErr
}
