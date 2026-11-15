// Package cmd wires the cobra command tree.
package cmd

import (
	"github.com/spf13/cobra"

	"hb/internal/i18n"
)

// Version is set by scripts/install.ps1 and scripts/install.sh:
// <VERSION file>+<UTC build date>.<commit count>.g<short sha>, e.g.
// 0.1.0+20261115.12.g1a2b3c4 (-ldflags "-X hb/internal/cmd.Version=...").
// Left empty, a plain `go build` or `go run` reports dev+g<sha> from the
// build info Go embeds (see version.go).
var Version = ""

// Global flags.
var (
	flagJSON    bool
	flagProfile string
)

func NewRoot() *cobra.Command {
	cobra.EnableCommandSorting = false // the order below is the order of the help
	root := &cobra.Command{
		Use:           "hb",
		Short:         i18n.T("root.short"),
		Long:          i18n.T("root.short") + "\n" + i18n.T("root.long"),
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON (keys always English, no secrets)")
	root.PersistentFlags().StringVar(&flagProfile, "profile", "", "profile name (default: config default_profile)")
	root.AddCommand(newLoginCmd(), newLogoutCmd(), newWhoamiCmd(), newPlayCmd(), newKddbCmd(), newAssetsCmd(), newHintsCmd(), newUnsnowCmd(), newDoctorCmd())
	polish(root)
	return root
}
