package tabs

import (
	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func pressKey(tabs *Tabs, key tea.KeyPressMsg) tea.Msg {
	GinkgoHelper()
	_, cmd := tabs.Update(key)
	Expect(cmd).NotTo(BeNil())
	return cmd()
}

var _ = Describe("Tabs", func() {
	var tabs *Tabs

	BeforeEach(func() {
		tabs = New([]*Elem{
			{Key: "first", Value: "First"},
			{Key: "second", Value: "Second"},
			{Key: "third", Value: "Third"},
		})
	})

	It("moves to the next tab on tab and reports the active tab", func() {
		msg := pressKey(tabs, tea.KeyPressMsg{Code: tea.KeyTab})

		Expect(tabs.GetActiveTab().Key).To(Equal("second"))
		Expect(msg).To(Equal(ActiveTabMsg(1)))
	})

	It("wraps around to the first tab", func() {
		pressKey(tabs, tea.KeyPressMsg{Code: tea.KeyTab})
		pressKey(tabs, tea.KeyPressMsg{Code: tea.KeyTab})
		pressKey(tabs, tea.KeyPressMsg{Code: tea.KeyTab})

		Expect(tabs.GetActiveTab().Key).To(Equal("first"))
	})

	It("wraps around to the last tab on shift+tab", func() {
		pressKey(tabs, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})

		Expect(tabs.GetActiveTab().Key).To(Equal("third"))
	})

	It("selects a tab from SelectTabCmd", func() {
		tabs.Update(SelectTabCmd(2)())

		Expect(tabs.GetActiveTab().Key).To(Equal("third"))
	})

	It("ignores an out of range selection", func() {
		tabs.Update(SelectTabMsg(2))

		tabs.Update(SelectTabMsg(10))
		tabs.Update(SelectTabMsg(-1))

		Expect(tabs.GetActiveTab().Key).To(Equal("third"))
	})

	It("resets the active tab when new tabs make it out of range", func() {
		tabs.Update(SelectTabMsg(2))

		tabs.SetTabs([]*Elem{{Key: "only", Value: "Only"}})

		Expect(tabs.GetActiveTab().Key).To(Equal("only"))
	})

	It("selects the first tab on Init", func() {
		tabs.Update(SelectTabMsg(1))

		tabs.Init()

		Expect(tabs.GetActiveTab().Key).To(Equal("first"))
	})
})
