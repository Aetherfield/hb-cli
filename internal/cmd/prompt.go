package cmd

import (
	"errors"
	"os"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// ErrNotInteractive is returned when a prompt is needed but stdin/stdout is
// not a terminal; callers should tell the user which flag to pass instead.
var ErrNotInteractive = errors.New("not an interactive terminal; pass the choice as a flag")

func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

type choice struct{ Label, Value string }

// selectOne shows an arrow-key menu (huh, a bubbletea form) and returns the
// chosen value.
func selectOne(title string, opts []choice) (string, error) {
	if !interactive() {
		return "", ErrNotInteractive
	}
	hopts := make([]huh.Option[string], len(opts))
	for i, o := range opts {
		hopts[i] = huh.NewOption(o.Label, o.Value)
	}
	var v string
	err := huh.NewSelect[string]().Title(title).Options(hopts...).Value(&v).Run()
	return v, err
}

// inputText asks for a line of text; secret masks what is typed.
func inputText(title, desc string, secret bool) (string, error) {
	if !interactive() {
		return "", ErrNotInteractive
	}
	in := huh.NewInput().Title(title).Description(desc)
	if secret {
		in = in.EchoMode(huh.EchoModePassword)
	}
	var v string
	err := in.Value(&v).Run()
	return v, err
}
