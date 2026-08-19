// Package cli assembles wand's command tree.
package cli

import "github.com/spf13/cobra"

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
	}

	root.AddCommand(newUICmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newGuardCmd())
	root.AddCommand(newQueueCmd())
	root.AddCommand(newTicketCmd())
	root.AddCommand(newVersionCmd())
	return root
}
