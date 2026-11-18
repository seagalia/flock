package spinners

import (
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/seagalia/flock/cli/tui/tuicommon"
)

const (
	padding = 2
)

type Infinite struct {
	spinner spinner.Model
	running bool
	message string
}

func NewInfinite(message string) *Infinite {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(tuicommon.Blue)
	return &Infinite{
		spinner: s,
		message: message,
	}
}

func (m *Infinite) Start() {
	m.running = true
}

func (m *Infinite) Stop() {
	m.running = false
}

func (m *Infinite) Init() tea.Cmd {
	return nil
}

func (m *Infinite) Update(tMsg tea.Msg) (*Infinite, tea.Cmd) {
	cmds := make([]tea.Cmd, 0)
	switch tMsg.(type) {

	case tuicommon.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(m.spinner.Tick())
		return m, cmd
	}

	return m, tea.Batch(cmds...)
}

func (m *Infinite) View() string {
	if !m.running {
		return ""
	}
	pad := strings.Repeat(" ", padding)
	return "\n" +
		pad + m.spinner.View() + " " + m.message
}
