package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/ledger"
)

func newStatsCmd() *cobra.Command {
	var since time.Duration

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Aggregate reports over the run journal",
		Long: "stats reads every run in the journal store and prints four reports:\n" +
			"token velocity by day, a per-harness x per-phase breakdown (tokens,\n" +
			"wall-clock, rounds), per-harness x per-verb convergence, and per-ticket\n" +
			"totals.\n\n" +
			"It reads only the local journal store — no LINEAR_API_KEY, no network.\n" +
			"A run whose journal fails to read or replay is skipped and named at\n" +
			"the end, rather than failing the whole report.",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := journal.Default()
			if err != nil {
				return err
			}
			result, err := ledger.Walk(store)
			if err != nil {
				return err
			}
			var cutoff time.Time
			if since > 0 {
				cutoff = time.Now().Add(-since)
			}
			fmt.Fprint(cmd.OutOrStdout(), statsReport(result, cutoff))
			return nil
		},
	}

	cmd.Flags().DurationVar(&since, "since", 0, "only include velocity buckets from this far back, e.g. 168h (default: whole history)")
	return cmd
}

// statsReport renders every report in one pass. Pure so a test can hold it
// against a synthetic [ledger.WalkResult], the way version_test.go tests
// versionString, rather than exec'ing the binary.
func statsReport(result ledger.WalkResult, since time.Time) string {
	var b strings.Builder

	b.WriteString("token velocity\n")
	b.WriteString(ledger.RenderVelocity(ledger.Velocity(result.Runs, since)))

	b.WriteString("\nharness x phase\n")
	b.WriteString(ledger.RenderHarnessPhase(ledger.HarnessPhase(result.Runs)))

	b.WriteString("\nconvergence (harness x verb)\n")
	b.WriteString(ledger.RenderConvergence(ledger.Convergence(result.Runs)))

	b.WriteString("\nper-ticket totals\n")
	b.WriteString(ledger.RenderTickets(ledger.Tickets(result.Runs)))

	if len(result.Skipped) > 0 {
		fmt.Fprintf(&b, "\nskipped (could not read or replay): %s\n", strings.Join(result.Skipped, ", "))
	}
	return b.String()
}
