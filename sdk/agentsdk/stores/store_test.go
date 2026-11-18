package stores_test

import (
	"context"

	"fmt"

	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("Stores", func() {
	It("should give the store of a type in a store manager", func() {
		sm := storers.NewMemoryManager()
		Expect(StoreBy[*backend.Backend](sm)).To(BeIdenticalTo(sm.Backends()))
		Expect(StoreBy[*vip.VIP](sm)).To(BeIdenticalTo(sm.VIPs()))
	})

	It("should panic for a type without store", func() {
		Expect(func() { StoreBy[*svccore.ListFilter](storers.NewMemoryManager()) }).To(Panic())
	})

	It("should count the resources of the counted store", func() {
		DeferCleanup(func() { CountResourcesOf(nil) })
		Expect(LenStore[*backend.Backend]()).To(BeZero())

		sm := storers.NewMemoryManager()
		CountResourcesOf(sm)
		Expect(sm.Backends().Create(context.Background(), &backend.Backend{UUID: "backend-1"})).To(Succeed())
		Expect(sm.Instances().Create(context.Background(), &instance.Instance{UUID: "instance-1"})).To(Succeed())
		Expect(sm.Certificates().Create(context.Background(), &cert.Certificate{UUID: "cert-1"})).To(Succeed())
		Expect(LenStore[*backend.Backend]()).To(Equal(1))
	})
})

var _ = Describe("StateStore", func() {
	var stateStore *StateStore
	backendTypeUrl := helpers.TypeUrl[*backend.Backend]()

	BeforeEach(func() {
		stateStore = NewStateStore()
	})

	It("should keep the last error of a resource until deleted", func() {
		aBackend := &backend.Backend{UUID: "backend-1"}
		stateStore.AddError(aBackend, fmt.Errorf("first"))
		stateStore.AddError(aBackend, fmt.Errorf("second"))

		message, state, err := stateStore.Get(backendTypeUrl, "backend-1")
		Expect(err).To(MatchError("second"))
		Expect(state).To(Equal(agentstatus.StatusResponse_ERRORED))
		Expect(message).To(BeIdenticalTo(aBackend))

		stateStore.DeleteTyped(backendTypeUrl, "backend-1")
		message, state, err = stateStore.Get(backendTypeUrl, "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(state).To(Equal(agentstatus.StatusResponse_UNKNOWN))
		Expect(message).To(BeNil())
	})

	It("should replace an error by a state in progress", func() {
		aBackend := &backend.Backend{UUID: "backend-1"}
		stateStore.AddError(aBackend, fmt.Errorf("failed"))
		stateStore.SetState(aBackend, agentstatus.StatusResponse_REMOVING)

		_, state, err := stateStore.Get(backendTypeUrl, "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(state).To(Equal(agentstatus.StatusResponse_REMOVING))

		stateStore.SetStateTyped(backendTypeUrl, "backend-1", agentstatus.StatusResponse_ADDING)
		_, state, _ = stateStore.Get(backendTypeUrl, "backend-1")
		Expect(state).To(Equal(agentstatus.StatusResponse_ADDING))

		stateStore.DeleteTyped(backendTypeUrl, "backend-1")
		_, state, _ = stateStore.Get(backendTypeUrl, "backend-1")
		Expect(state).To(Equal(agentstatus.StatusResponse_UNKNOWN))
	})

	It("should keep the version through the changes of state and forget it with the resource", func() {
		aBackend := &backend.Backend{UUID: "backend-1"}
		stateStore.SetVersion(backendTypeUrl, "backend-1", "v1")
		stateStore.SetState(aBackend, agentstatus.StatusResponse_ADDING)
		stateStore.AddError(aBackend, fmt.Errorf("failed"))
		stateStore.ClearState(backendTypeUrl, "backend-1")

		message, state, err := stateStore.Get(backendTypeUrl, "backend-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(message).To(BeNil())
		Expect(state).To(Equal(agentstatus.StatusResponse_OK))
		Expect(stateStore.Version(backendTypeUrl, "backend-1")).To(Equal("v1"))

		stateStore.DeleteTyped(backendTypeUrl, "backend-1")
		Expect(stateStore.Version(backendTypeUrl, "backend-1")).To(BeEmpty())
	})

	It("should range over states of one type only", func() {
		stateStore.AddError(&backend.Backend{UUID: "backend-1"}, fmt.Errorf("backend error"))
		stateStore.AddError(&vip.VIP{UUID: "vip-1"}, fmt.Errorf("vip error"))

		found := make([]string, 0)
		stateStore.Range(backendTypeUrl, func(message proto.Message, state agentstatus.StatusResponse_State, err error) {
			found = append(found, helpers.GetIdentifier(message)+": "+state.String()+" "+err.Error())
		})
		Expect(found).To(Equal([]string{"backend-1: ERRORED backend error"}))
	})

})
