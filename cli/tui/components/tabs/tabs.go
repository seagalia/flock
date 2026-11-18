package tabs

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
	"github.com/seagalia/flock/cli/tui/tuicommon"
)

// SelectTabMsg is a message that contains the index of the tab to select.
type SelectTabMsg int

// ActiveTabMsg is a message that contains the index of the current active tab.
type ActiveTabMsg int

// Tabs is bubbletea component that displays a list of tabs.
type Tabs struct {
	tabs        []*Elem
	content     string
	width       int
	height      int
	activeTab   int
	TabInactive lipgloss.Style
	TabActive   lipgloss.Style
	TabDot      lipgloss.Style
}

func tabBorderWithBottom(left, middle, right string) lipgloss.Border {
	border := lipgloss.RoundedBorder()
	border.BottomLeft = left
	border.Bottom = middle
	border.BottomRight = right
	return border
}

var (
	inactiveTabBorder = tabBorderWithBottom("┴", "─", "┴")
	activeTabBorder   = tabBorderWithBottom("┘", " ", "└")
	highlightColor    = tuicommon.Blue
	docStyle          = lipgloss.NewStyle().Padding(1, 2, 1, 2)
	windowStyle       = lipgloss.NewStyle().
				BorderForeground(highlightColor).
				Padding(2, 0).
				Align(lipgloss.Center).
				Border(lipgloss.NormalBorder()).
				UnsetBorderTop()
)

type Elem struct {
	Key   string
	Value string
}

// New creates a new Tabs component.
func New(tabs []*Elem) *Tabs {
	r := &Tabs{
		tabs:      tabs,
		activeTab: 0,
		TabInactive: lipgloss.NewStyle().
			Border(inactiveTabBorder, true).
			BorderForeground(highlightColor).
			Padding(0, 1),
		TabActive: lipgloss.NewStyle().
			Border(activeTabBorder, true).
			BorderForeground(highlightColor).
			Padding(0, 1),
	}
	return r
}

func (t *Tabs) SetTabs(tabs []*Elem) {
	t.tabs = tabs
	if t.activeTab >= len(t.tabs) {
		t.activeTab = 0
	}
}

// SetContent sets the content of the tabs.
func (t *Tabs) SetContent(content string) {
	t.content = content
}

// SetSize implements common.Component.
func (t *Tabs) SetSize(width, height int) {
	t.width = width
	t.height = height
}

// Init implements tea.Model.
func (t *Tabs) Init() tea.Cmd {
	t.activeTab = 0
	return nil
}

func (t *Tabs) GetActiveTab() *Elem {
	return t.tabs[t.activeTab]
}

// Update implements tea.Model.
func (t *Tabs) Update(tMsg tea.Msg) (*Tabs, tea.Cmd) {
	cmds := make([]tea.Cmd, 0)
	switch msg := tMsg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "tab":
			t.activeTab = (t.activeTab + 1) % len(t.tabs)
			cmds = append(cmds, t.activeTabCmd)
		case "shift+tab":
			t.activeTab = (t.activeTab - 1 + len(t.tabs)) % len(t.tabs)
			cmds = append(cmds, t.activeTabCmd)
		}
	case tea.MouseReleaseMsg:
		if msg.Button != tea.MouseLeft {
			break
		}

		for i, tab := range t.tabs {
			if zone.Get(tab.Key).InBounds(msg) {
				t.activeTab = i
				cmds = append(cmds, t.activeTabCmd)
			}
		}
	case SelectTabMsg:
		tab := int(msg)
		if tab >= 0 && tab < len(t.tabs) {
			t.activeTab = int(msg)
		}
	}
	return t, tea.Batch(cmds...)
}

// View implements tea.Model.
func (t *Tabs) View() string {
	doc := strings.Builder{}

	var row string
	isContentTooLarge := false
	sizeContent := lipgloss.Width(t.content)
	for i, tb := range t.tabs {
		var style lipgloss.Style
		isFirst, isLast, isActive := i == 0, i == len(t.tabs)-1, i == t.activeTab
		if isActive {
			style = t.TabActive
		} else {
			style = t.TabInactive
		}
		border, _, _, _, _ := style.GetBorder()
		if isFirst && isActive {
			border.BottomLeft = "│"
		} else if isFirst && !isActive {
			border.BottomLeft = "├"
		} else if isLast && isActive {
			border.BottomRight = "│"
		} else if isLast && !isActive {
			border.BottomRight = "┤"
		}
		if isLast && isContentTooLarge && !isActive {
			border.BottomRight = "┴"
		}
		if isLast && isContentTooLarge && isActive {
			border.BottomRight = "└"
		}
		style = style.Border(border)

		val := lipgloss.NewStyle().Width(15).Render(tb.Value)
		val = style.Render(val)
		val = zone.Mark(
			tb.Key,
			val,
		)
		row = lipgloss.JoinHorizontal(lipgloss.Top, row, val)
		if sizeContent > lipgloss.Width(row) {
			isContentTooLarge = true
		}

	}

	if isContentTooLarge {
		style := t.TabInactive
		border, _, _, _, _ := style.GetBorder()
		border.BottomRight = "┐"
		border.BottomLeft = "─"
		border.TopLeft = ""
		border.TopRight = ""
		border.Top = ""
		border.Left = ""
		border.Right = ""
		style = style.Border(border).Width(sizeContent-lipgloss.Width(row)).Padding(0, 0)

		row = lipgloss.JoinHorizontal(lipgloss.Top, row, style.Render(" "))
	}
	doc.WriteString(row)
	doc.WriteString("\n")
	doc.WriteString(windowStyle.Render(t.content))
	return docStyle.Render(doc.String())
}

func (t *Tabs) activeTabCmd() tea.Msg {
	return ActiveTabMsg(t.activeTab)
}

// SelectTabCmd is a bubbletea command that selects the tab at the given index.
func SelectTabCmd(tab int) tea.Cmd {
	return func() tea.Msg {
		return SelectTabMsg(tab)
	}
}
