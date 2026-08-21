package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/guard"
)

func newGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Judge a pending tool call against the covenant's authorization rules",
		Long: "guard is the harness hook: it reads a pending tool call as JSON on stdin\n" +
			"(the Claude Code PreToolUse protocol) and blocks — exit 2, reason on\n" +
			"stderr — any Linear save_issue that sets a status only a human may set:\n" +
			"Todo, To Plan, Done, Canceled or Duplicate, by name or by state type.\n" +
			"Everything else passes, and input that does not parse passes too: a\n" +
			"broken guard must never wedge a session.\n\n" +
			"wand init installs the hook entry that routes save_issue calls here.",
		// Run, not RunE: the hook protocol's block is exit code 2 with bare
		// reason text on stderr. fang exits 1 on any RunE error and decorates
		// it, so the command exits itself instead of returning.
		Run: func(cmd *cobra.Command, args []string) {
			if code := guard.Run(cmd.InOrStdin(), cmd.ErrOrStderr()); code != 0 {
				os.Exit(code)
			}
		},
	}
}
