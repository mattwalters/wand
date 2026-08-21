// Package cli assembles wand's command tree.
package cli

import (
	"io"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/screen"
)

// Root returns the wand command tree.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "wand",
		Short: "An agent toolkit for repositories",
		Long: "wand sets up and maintains the agent machinery in a repository:\n" +
			"its covenant, and the blessing path work travels along.",
		// Errors are reported by fang; cobra should not also dump usage for
		// what are usually runtime failures rather than misuse.
		SilenceUsage:  true,
		SilenceErrors: true,
		// A bare `wand` takes no arguments; anything after it is either a
		// subcommand (handled before Args runs) or a typo, and a typo
		// should fail as an unknown command rather than silently opening
		// home.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A terminal is what makes home possible at all: Bubble
			// Tea needs one to draw into. Anything else — a pipe, a script,
			// CI — reads as "what is this", so it gets the same help a
			// bare `wand` printed before this command existed.
			if !isTTY(cmd.OutOrStdout()) {
				return cmd.Help()
			}
			// Bare `wand` never engages by itself — opening a dashboard
			// must not start spending money. Engage mode is a deliberate
			// key press, reachable from here exactly as it is from
			// `wand ui`, with the same default harness and poll interval
			// `wand ui`'s own flags default to.
			return runHome(cmd, "", "claude-code", "", "", screen.DefaultWidth, screen.DefaultHeight, time.Minute)
		},
	}

	root.AddCommand(newUICmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newGuardCmd())
	root.AddCommand(newQueueCmd())
	root.AddCommand(newTicketCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newClaimCmd())
	root.AddCommand(newHandbackCmd())
	root.AddCommand(newAbandonCmd())
	root.AddCommand(newFileCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newPMCmd())
	root.AddCommand(newDispatchCmd())
	root.AddCommand(newSweepCmd())
	root.AddCommand(newStatsCmd())
	root.AddCommand(newVersionCmd())
	return root
}

// isTTY reports whether w is a terminal a Bubble Tea program can draw into.
//
// It checks the writer cobra actually resolved rather than os.Stdout
// directly, so a test can stand in a non-*os.File writer (a bytes.Buffer,
// say) and get the same non-TTY path a pipe or a CI runner gets, with no
// package-level fake to wire up.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(f.Fd())
}
