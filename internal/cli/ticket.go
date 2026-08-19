package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/ticket"
)

func newTicketCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ticket <identifier>",
		Short: "Print one ticket whole, for a cold reader",
		Long: "ticket renders one issue for someone with no context — a worker being\n" +
			"prompted, or a human catching up. The description is passed whole;\n" +
			"comments follow oldest-first, every page of them, each held to a\n" +
			"per-comment budget so a long early comment cannot crowd out the short\n" +
			"answer a human left last.\n\n" +
			"Requires LINEAR_API_KEY in the environment.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := linearFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), readTimeout)
			defer cancel()

			issue, err := cl.IssueByIdentifier(ctx, args[0])
			if err != nil {
				return err
			}
			comments, err := cl.IssueComments(ctx, issue.ID)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), ticket.Render(issue, comments))
			return nil
		},
	}
}
