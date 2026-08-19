package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/bootstrap"
	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/shim"
)

// settingsPath is where the harness shim lives, relative to the repo root
// init runs in.
const settingsPath = ".claude/settings.json"

// installShim ensures the repo's harness settings route Linear issue writes
// through wand guard. The shim is a build artifact of the covenant:
// regenerated here, never hand-edited.
func installShim(out io.Writer, dryRun bool) error {
	existing, err := os.ReadFile(settingsPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	ensured, changed, err := shim.Ensure(existing)
	if err != nil {
		return fmt.Errorf("%s: %w", settingsPath, err)
	}
	if !changed {
		fmt.Fprintln(out, "guard hook already installed")
		return nil
	}
	if dryRun {
		fmt.Fprintf(out, "would install PreToolUse guard hook in %s (%s → wand guard)\n", settingsPath, shim.Matcher)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, ensured, 0o644); err != nil {
		return err
	}
	// Read back and verify, so "installed" means observed rather than assumed.
	written, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	if _, again, err := shim.Ensure(written); err != nil || again {
		return fmt.Errorf("guard hook still not installed after writing %s", settingsPath)
	}
	fmt.Fprintf(out, "install PreToolUse guard hook in %s (%s → wand guard)\n", settingsPath, shim.Matcher)
	return nil
}

func newInitCmd() *cobra.Command {
	var (
		teamKey  string
		teamName string
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a Linear team to the covenant",
		Long: "init creates the repo's Linear team (or adopts an existing one by key)\n" +
			"and brings it to the covenant: the workflow statuses, the labels, and\n" +
			"the PR automations. It is idempotent — a team already at the covenant\n" +
			"plans no writes.\n\n" +
			"Requires LINEAR_API_KEY in the environment, with full access.",
		RunE: func(cmd *cobra.Command, args []string) error {
			apiKey := os.Getenv("LINEAR_API_KEY")
			if apiKey == "" {
				return fmt.Errorf("LINEAR_API_KEY is not set; create a full-access key in Linear's security settings")
			}
			if teamKey == "" {
				return fmt.Errorf("--team-key is required (e.g. --team-key WAND)")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()

			cl := &linear.Client{APIKey: apiKey}
			cov := covenant.Default()
			out := cmd.OutOrStdout()

			// The harness shim first: it is purely local, and from here on
			// this very repo's sessions are under the guard.
			if err := installShim(out, dryRun); err != nil {
				return err
			}

			team, err := cl.TeamByKey(ctx, teamKey)
			if err != nil {
				return err
			}
			created := false
			if team.ID == "" {
				if teamName == "" {
					teamName = teamKey
				}
				if dryRun {
					fmt.Fprintf(out, "would create team %q (key %s)\n", teamName, teamKey)
					// Plan against an empty team; the real default statuses and
					// automations only exist once the team does, so this
					// overstates the work rather than understating it.
					for _, a := range bootstrap.Plan(cov, bootstrap.Current{}) {
						fmt.Fprintf(out, "would %s\n", a)
					}
					return nil
				}
				team, err = cl.CreateTeam(ctx, teamName, teamKey, linear.TeamSettings{
					TriageEnabled:       cov.TriageEnabled,
					IssueEstimationType: cov.IssueEstimationType,
				})
				if err != nil {
					return err
				}
				created = true
				fmt.Fprintf(out, "created team %s (%s)\n", team.Key, team.Name)
			} else {
				fmt.Fprintf(out, "adopting existing team %s (%s)\n", team.Key, team.Name)
			}

			current, err := bootstrap.Read(ctx, cl, team.ID)
			if err != nil {
				return err
			}
			actions := bootstrap.Plan(cov, current)
			if len(actions) == 0 {
				fmt.Fprintln(out, "team already satisfies the covenant; nothing to do")
				return nil
			}
			for _, a := range actions {
				if dryRun {
					fmt.Fprintf(out, "would %s\n", a)
				} else {
					fmt.Fprintf(out, "%s\n", a)
				}
			}
			if dryRun {
				return nil
			}
			if err := bootstrap.Apply(ctx, cl, team.ID, actions); err != nil {
				return err
			}

			// Read back and verify, so "done" means observed rather than assumed.
			verify, err := bootstrap.Read(ctx, cl, team.ID)
			if err != nil {
				return err
			}
			if remaining := bootstrap.Plan(cov, verify); len(remaining) > 0 {
				return fmt.Errorf("after apply, %d actions still planned — first: %s", len(remaining), remaining[0])
			}
			if created {
				fmt.Fprintf(out, "team %s bootstrapped to the covenant\n", team.Key)
			} else {
				fmt.Fprintf(out, "team %s brought to the covenant\n", team.Key)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WAND")
	cmd.Flags().StringVar(&teamName, "team-name", "", "team name when creating (defaults to the key)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and write nothing")
	return cmd
}
