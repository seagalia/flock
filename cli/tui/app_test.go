package tui_test

import (
	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/cli/tui"
	"github.com/seagalia/flock/cli/tui/components/tabs"
)

// tabsContent shows tabs, which mark their zones for mouse clicks
type tabsContent struct {
	tabs     *tabs.Tabs
	received []tea.Msg
}

func (c *tabsContent) Init() tea.Cmd { return nil }

func (c *tabsContent) Update(msg tea.Msg) tea.Cmd {
	c.received = append(c.received, msg)
	return nil
}

func (c *tabsContent) View() string { return c.tabs.View() }

// typingContent has a focused text input
type typingContent struct {
	received []tea.Msg
}

func (c *typingContent) Init() tea.Cmd { return nil }

func (c *typingContent) Update(msg tea.Msg) tea.Cmd {
	c.received = append(c.received, msg)
	return nil
}

func (c *typingContent) View() string { return "" }

func (c *typingContent) CapturesInput() bool { return true }

var _ = Describe("App", func() {
	var content *tabsContent

	BeforeEach(func() {
		content = &tabsContent{tabs: tabs.New([]*tabs.Elem{{Key: "agent-1", Value: "Agent 1"}})}
	})

	It("shows the content on the alternate screen without mouse by default", func() {
		view := tui.NewApp(content).View()

		Expect(view.AltScreen).To(BeTrue())
		Expect(view.MouseMode).To(Equal(tea.MouseModeNone))
		Expect(view.Content).To(ContainSubstring("Agent 1"))
	})

	It("reports the mouse once enabled", func() {
		app := tui.NewApp(content)
		app.EnableMouse()

		view := app.View()

		Expect(view.MouseMode).To(Equal(tea.MouseModeCellMotion))
		Expect(view.Content).To(ContainSubstring("Agent 1"))
	})

	It("quits on q and forwards the other messages to the content", func() {
		app := tui.NewApp(content)

		_, cmd := app.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		Expect(cmd()).To(Equal(tea.QuitMsg{}))

		app.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
		Expect(content.received).To(ConsistOf(tea.KeyPressMsg{Code: 'r', Text: "r"}))
	})

	It("forwards q to a content capturing input and still quits on ctrl+c", func() {
		typing := &typingContent{}
		app := tui.NewApp(typing)

		_, cmd := app.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		Expect(cmd).To(BeNil())
		Expect(typing.received).To(ConsistOf(tea.KeyPressMsg{Code: 'q', Text: "q"}))

		_, cmd = app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		Expect(cmd()).To(Equal(tea.QuitMsg{}))
	})
})
