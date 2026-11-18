package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

type Reloader interface {
	Reload()
}

// Content is a screen of the app, the app owns the terminal: alternate screen, mouse and quit keys
type Content interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View() string
}

// InputCapturer is a content with text inputs, while it captures input q is typed text and only ctrl+c quits
type InputCapturer interface {
	CapturesInput() bool
}

type App struct {
	content     Content
	enableMouse bool
}

func NewApp(content Content) *App {
	// components mark their zones even when the mouse is not reported, zone.Mark panics without a manager
	zone.NewGlobal()
	return &App{content: content}
}

// EnableMouse reports the mouse to the content, which locates clicks with bubblezone marks
func (a *App) EnableMouse() {
	a.enableMouse = true
}

func (a *App) Init() tea.Cmd {
	return a.content.Init()
}

func (a *App) Update(tMsg tea.Msg) (tea.Model, tea.Cmd) {
	switch tMsgRef := tMsg.(type) {
	case tea.KeyPressMsg:
		capturer, hasInputs := a.content.(InputCapturer)
		typingText := hasInputs && capturer.CapturesInput()
		quitKey := tMsgRef.String() == "ctrl+c" || (tMsgRef.String() == "q" && !typingText)
		if quitKey {
			return a, tea.Quit
		}
	}
	if errorContent != "" {
		return a, tea.Quit
	}
	return a, a.content.Update(tMsg)
}

func (a *App) View() tea.View {
	// Scan records the zones and removes their marks from the output
	content := zone.Scan(lipgloss.NewStyle().Render(debugContent + "\n" + a.content.View()))
	view := tea.NewView(content)
	view.AltScreen = true
	if a.enableMouse {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}

func (a *App) Error() error {
	if errorContent == "" {
		return nil
	}
	return errors.New(errorContent)
}
