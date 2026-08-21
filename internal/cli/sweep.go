package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/sweep"
)

func newSweepCmd() *cobra.Command {
	var teamKey string

	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Act on one thing left over after a run ended",
		Long: "sweep is everything that happens after wand run or wand plan exits.\n" +
			"It ranks and vets four conditions the same way dispatch picks a\n" +
			"winner, and acts on at most one, first-refused-skipped: a lease whose\n" +
			"owner is provably dead (the run's journal is parked and the ticket\n" +
			"handed back), a re-review label (a human asked for another review\n" +
			"cycle) or a re-plan label (the planning-side twin, handed back into\n" +
			"In Planning rather than Needs Input), or an unresolved review thread\n" +
			"on a ready-for-human PR — necessarily left after convergence, since\n" +
			"wand run's own check catches one standing before that.\n\n" +
			"It also reports, read-only, tickets sitting In Progress with no run\n" +
			"behind them at all — not even a dead one. There is nothing there to\n" +
			"act on, only something that looks stuck for a person to judge.\n\n" +
			"Requires LINEAR_API_KEY and an authenticated gh. Run it from inside\n" +
			"the repository, which is also where the team key comes from: [team]\n" +
			"key in the nearest wand.toml, unless --team-key names another team.",
		Args: cobra.NoArgs,
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
			repo, err := repoRoot("sweep")
			if err != nil {
				return err
			}
			store, err := journal.Default()
			if err != nil {
				return err
			}

			ctx, stop := journal.Interruptible(cmd.Context())
			defer stop()

			res, err := sweep.Execute(ctx, sweep.Deps{
				Board:   cl,
				Hub:     run.ExecHub{},
				Runs:    store,
				Cov:     cov,
				TeamKey: resolvedTeamKey,
				Repo:    repo,
				Out:     cmd.OutOrStdout(),
			}, store)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if res.Acted == sweep.ActedNothing {
				fmt.Fprintln(out, "sweep: nothing to act on")
			} else {
				fmt.Fprintf(out, "sweep: %s %s (%s): %s\n",
					res.Acted, res.Candidate.Ticket, res.Candidate.Kind, res.Candidate.Reason)
			}
			if len(res.Zombies) > 0 {
				fmt.Fprintf(out, "sweep: %d ticket(s) In Progress with no run behind them:\n", len(res.Zombies))
				for _, z := range res.Zombies {
					fmt.Fprintf(out, "  %s  %s\n", z.Ticket, z.Title)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND (falls back to [team] key in wand.toml)")
	return cmd
}
