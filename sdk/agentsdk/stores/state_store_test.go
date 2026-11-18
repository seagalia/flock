package stores_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("StateStore notes", func() {
	var stateStore *StateStore
	typeUrl := helpers.TypeUrl[*vip.VIP]()

	BeforeEach(func() {
		stateStore = NewStateStore()
	})

	It("keeps notes when the state is cleared", func() {
		stateStore.SetNotes(typeUrl, "u1", "src", []*flocktype.Note{{Code: "c", Message: "w"}})
		stateStore.SetState(&vip.VIP{UUID: "u1"}, agentstatus.StatusResponse_ADDING)
		stateStore.AddError(&vip.VIP{UUID: "u1"}, nil)
		stateStore.ClearState(typeUrl, "u1")

		Expect(stateStore.Notes(typeUrl, "u1")).To(ConsistOf(HaveField("Message", "w")))
	})

	It("drops the notes of a source set to empty", func() {
		stateStore.SetNotes(typeUrl, "u1", "src", []*flocktype.Note{{Code: "c"}})
		stateStore.SetNotes(typeUrl, "u1", "src", nil)

		Expect(stateStore.Notes(typeUrl, "u1")).To(BeEmpty())
	})

	It("keeps the notes of each source apart", func() {
		stateStore.SetNotes(typeUrl, "u1", "b", []*flocktype.Note{{Message: "from b"}})
		stateStore.SetNotes(typeUrl, "u1", "a", []*flocktype.Note{{Message: "from a"}})
		stateStore.SetNotes(typeUrl, "u1", "b", []*flocktype.Note{{Message: "from b again"}})

		Expect(stateStore.Notes(typeUrl, "u1")).To(HaveExactElements(
			HaveField("Message", "from a"), HaveField("Message", "from b again")))
	})

	It("gives no notes for an unknown resource", func() {
		Expect(stateStore.Notes(typeUrl, "unknown")).To(BeEmpty())
	})
})
