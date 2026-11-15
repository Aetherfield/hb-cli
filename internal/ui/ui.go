// Package ui styles hb's plain command output. One UI belongs to one output
// writer: its colours follow what that writer can show (none when it is a pipe
// or a file, or NO_COLOR is set), and nothing in the layout depends on colour:
// the plain text reads the same, widths included.
package ui

import (
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// UI renders styled strings for one writer.
type UI struct{ r *lipgloss.Renderer }

// For makes the UI of a writer.
func For(w io.Writer) *UI { return &UI{lipgloss.NewRenderer(w)} }

// WithColor is for tests: a UI that always draws its colours.
func WithColor(w io.Writer) *UI {
	u := For(w)
	u.r.SetColorProfile(termenv.ANSI256)
	return u
}

func (u *UI) st() lipgloss.Style { return u.r.NewStyle() }

// Title is a heading.
func (u *UI) Title(s string) string { return u.st().Bold(true).Render(s) }

// Section is a small heading over a group of lines.
func (u *UI) Section(s string) string {
	return u.st().Bold(true).Foreground(lipgloss.Color("6")).Render(s)
}

// Dim is for what is secondary: labels, ids, hints.
func (u *UI) Dim(s string) string { return u.st().Faint(true).Render(s) }

// Good, Bad and Warn colour an outcome.
func (u *UI) Good(s string) string { return u.st().Foreground(lipgloss.Color("2")).Render(s) }
func (u *UI) Bad(s string) string  { return u.st().Foreground(lipgloss.Color("1")).Render(s) }
func (u *UI) Warn(s string) string { return u.st().Foreground(lipgloss.Color("3")).Render(s) }

// Accent is for a name worth finding at a glance.
func (u *UI) Accent(s string) string { return u.st().Foreground(lipgloss.Color("6")).Render(s) }

// Link is an address to open.
func (u *UI) Link(s string) string {
	return u.st().Foreground(lipgloss.Color("6")).Underline(true).Render(s)
}

// Bold is emphasis.
func (u *UI) Bold(s string) string { return u.st().Bold(true).Render(s) }

// Mark is ✓ or ✗ in its colour.
func (u *UI) Mark(ok bool) string {
	if ok {
		return u.Good("✓")
	}
	return u.Bad("✗")
}

// Pad fills s with spaces on the right to a display width of w (Chinese
// characters are two columns wide), so columns line up in any terminal.
func Pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// Width is a string's display width, colour codes not counted.
func Width(s string) int { return lipgloss.Width(s) }
