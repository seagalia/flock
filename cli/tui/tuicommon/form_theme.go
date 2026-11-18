package tuicommon

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// FormTheme is huh's Charm theme in the Flock colors. It ignores the isDark huh gives: huh only learns the background
// when the program sends a tea.BackgroundColorMsg, which no flock program does, while the palette already follows the
// terminal through compat. The colors ThemeCharm picks for false read on both backgrounds, except the options, left in
// the terminal's own color.
var FormTheme = huh.ThemeFunc(func(bool) *huh.Styles {
	t := huh.ThemeCharm(false)

	t.Focused.Title = t.Focused.Title.Foreground(Navy)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(Navy)
	t.Focused.Directory = t.Focused.Directory.Foreground(Navy)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(Red)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(Red)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(Green)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(Green)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(Green)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(Green)
	t.Focused.Option = t.Focused.Option.Foreground(lipgloss.NoColor{})
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(lipgloss.NoColor{})
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(Green)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(Green)
	// white on the light green of dark terminals is hard to read
	buttonText := adaptive("#ffffff", "#000000")
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(buttonText).Background(Green)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Green)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(Green)

	// ThemeCharm copied its focused styles into the blurred and group ones before the changes above
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
})
