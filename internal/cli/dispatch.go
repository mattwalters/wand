package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/dispatch"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/plan"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/worker"
)

func newDispatchCmd() *cobra.Command {
	var teamKey, harness, model, effort string
	var watch bool
	var interval time.Duration

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Pick the one ticket to run next, and run it",
		Long: "dispatch is the selector over the loop: a thin, read-mostly pass that\n" +
			"picks the highest-ranked, vetted Todo issue — through wand run — or,\n" +
			"when no lane is free or Todo has nothing startable, the highest-ranked,\n" +
			"vetted Scoping issue — through wand plan, which needs no lane, so\n" +
			"research is never starved by full lane occupancy. One ticket per pass.\n\n" +
			"The Todo gate lives here, deliberately, and not in run or plan\n" +
			"themselves: a human typing an identifier has already made that\n" +
			"decision; an unattended selector has not.\n\n" +
			"A repository dispatches from one process at a time — a directory and a\n" +
			"pid, reclaimed once its holder is provably dead, never assumed. Pass\n" +
			"--watch to poll instead of running one pass and exiting: each tick\n" +
			"that finds capacity spawns the winner as a detached process that\n" +
			"survives the watcher, so several lanes can be in flight at once.\n\n" +
			"Exit codes are a contract a scheduler can read: 0 converged, 1 refused\n" +
			"(nothing started, or a claim raced and lost), 2 handed back or parked\n" +
			"(the journal has the detail), 3 locked (another dispatch already runs\n" +
			"here), 4 nothing to do, 5 Linear unreachable. --watch runs until\n" +
			"interrupted and does not use this contract itself.\n\n" +
			"Requires LINEAR_API_KEY, an authenticated gh, and commands.verify in\n" +
			"wand.toml. Run it from inside the repository, which is also where the\n" +
			"team key comes from: [team] key in the nearest wand.toml, unless\n" +
			"--team-key names another team.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if watch {
				if err := runDispatchWatch(cmd, teamKey, harness, model, effort, interval); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					os.Exit(1)
				}
				return
			}
			if code := runDispatchOnce(cmd, teamKey, harness, model, effort); code != dispatch.ExitConverged {
				os.Exit(code)
			}
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND (falls back to [team] key in wand.toml)")
	cmd.Flags().StringVar(&harness, "harness", "claude-code", "worker harness: claude-code or codex")
	cmd.Flags().StringVar(&model, "model", "", "model for every worker (default: the harness's default)")
	cmd.Flags().StringVar(&effort, "effort", "", "reasoning effort for every worker (default: the harness's default)")
	cmd.Flags().BoolVar(&watch, "watch", false, "poll and dispatch continuously, spawning detached children that survive this process")
	cmd.Flags().DurationVar(&interval, "interval", time.Minute, "how often --watch polls")
	return cmd
}

// dispatchDeps builds what one pass, or one watch, needs — the same
// wiring runRun and runPlan already assemble, shared here because
// dispatch hands the same interfaces to run.Execute and plan.Execute
// rather than reimplementing either.
func dispatchDeps(cmd *cobra.Command, teamKey, harness, model, effort string) (dispatch.Deps, *journal.Store, error) {
	var zero dispatch.Deps
	cov, fileTeamKey, _, err := covenantFromCwd()
	if err != nil {
		return zero, nil, err
	}
	resolvedTeamKey, err := resolveTeamKey(teamKey, fileTeamKey)
	if err != nil {
		return zero, nil, err
	}
	cl, err := linearFromEnv()
	if err != nil {
		return zero, nil, err
	}
	adapter, err := worker.AdapterFor(harness)
	if err != nil {
		return zero, nil, err
	}
	repo, err := repoRoot("dispatch")
	if err != nil {
		return zero, nil, err
	}
	store, err := journal.Default()
	if err != nil {
		return zero, nil, err
	}

	return dispatch.Deps{
		Board:   cl,
		Cov:     cov,
		Runs:    store,
		Git:     run.ExecGit{RunsRoot: store.Root},
		Hub:     run.ExecHub{},
		Shell:   run.ExecShell{},
		Tree:    plan.ExecTree{},
		Workers: run.AdapterWorkers{Adapter: adapter},
		TeamKey: resolvedTeamKey,
		Repo:    repo,
		Harness: adapter.Name(),
		Model:   model,
		Effort:  effort,
		Out:     cmd.OutOrStdout(),
	}, store, nil
}

func runDispatchOnce(cmd *cobra.Command, teamKey, harness, model, effort string) int {
	errOut := cmd.ErrOrStderr()
	fail := func(err error) int {
		fmt.Fprintln(errOut, err)
		return dispatch.ExitRefused
	}

	d, store, err := dispatchDeps(cmd, teamKey, harness, model, effort)
	if err != nil {
		return fail(err)
	}

	ctx, stop := journal.Interruptible(cmd.Context())
	defer stop()

	res, err := dispatch.Execute(ctx, d, store)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "dispatch: %s — %s\n", res.Kind, res.Reason)
	return res.ExitCode()
}

func runDispatchWatch(cmd *cobra.Command, teamKey, harness, model, effort string, interval time.Duration) error {
	d, store, err := dispatchDeps(cmd, teamKey, harness, model, effort)
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving the wand binary to spawn winners through: %w", err)
	}

	ctx, stop := journal.Interruptible(cmd.Context())
	defer stop()

	return dispatch.Watch(ctx, dispatch.WatchDeps{Deps: d, Bin: bin, Interval: interval}, store)
}
