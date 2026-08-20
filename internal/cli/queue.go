package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
)

// apiTimeout bounds each command's conversation with Linear, reads and
// writes alike.
const apiTimeout = time.Minute

// linearFromEnv builds a client from LINEAR_API_KEY, the same contract init
// documents: the key comes from the environment, never from a file.
func linearFromEnv() (*linear.Client, error) {
	apiKey := os.Getenv("LINEAR_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("LINEAR_API_KEY is not set; create a full-access key in Linear's security settings")
	}
	return &linear.Client{APIKey: apiKey}, nil
}

func newQueueCmd() *cobra.Command {
	var teamKey string

	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Print the ranked, vetted Todo queue",
		Long: "queue prints the team's Todo issues in start order: priority ascending\n" +
			"with \"No priority\" last, oldest first within a priority, identifier as\n" +
			"the final tiebreak so two racing readers agree.\n\n" +
			"Issues an agent may not start are vetted out and printed with the\n" +
			"reason — labeled human-only, or blocked by an issue not yet completed\n" +
			"or canceled (a started blocker still blocks). Nothing is dropped\n" +
			"silently: a queue that quietly comes up short reads as a queue in\n" +
			"order.\n\n" +
			"Requires LINEAR_API_KEY in the environment.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cov, fileTeamKey, _, err := covenantFromCwd()
			if err != nil {
				return err
			}
			resolvedTeamKey, err := resolveTeamKey(teamKey, fileTeamKey)
			if err != nil {
				return err
			}
			cl, err := linearFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), apiTimeout)
			defer cancel()

			issues, err := cl.TeamIssuesByState(ctx, resolvedTeamKey, cov.StatusName("todo"))
			if err != nil {
				return err
			}
			ready, skips := queue.Build(issues)
			fmt.Fprint(cmd.OutOrStdout(), queue.Render(ready, skips))
			return nil
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND (falls back to [team] key in wand.toml)")
	return cmd
}
