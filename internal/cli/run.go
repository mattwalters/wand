package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/run"
)

func newRunCmd() *cobra.Command {
	var harness, model, effort string

	cmd := &cobra.Command{
		Use:   "run <identifier>",
		Short: "Own one ticket from claim to a terminal state: implement, CI, review, revise",
		Long: "run claims one blessed ticket and drives it through the loop — a cold\n" +
			"worker per phase (implement, fix-CI, review, revise), phases and caps\n" +
			"from the covenant. Workers commit in a run-private worktree and are\n" +
			"mute; the orchestrator makes every Linear and GitHub write: it pushes,\n" +
			"opens and titles the PR, and moves the ticket.\n\n" +
			"Every run ends in exactly one terminal state, journaled by the run\n" +
			"journal: converged (In Review, ready-for-human, PR open), handed back\n" +
			"(Needs Input, with the reason as a comment posted first), or parked\n" +
			"(journal-only, with the worktree preserved). Convergence happens only\n" +
			"on a reviewer's positive evidence — a cap running out is a hand-back\n" +
			"that says so, never a quiet pass.\n\n" +
			"Exit codes are a contract a scheduler can read: 0 converged,\n" +
			"2 handed back, 3 parked, 1 the run never started (refused claim,\n" +
			"missing configuration).\n\n" +
			"Requires LINEAR_API_KEY, an authenticated gh, and commands.verify in\n" +
			"wand.toml. Run it from inside the repository.",
		Args: cobra.ExactArgs(1),
		// Run, not RunE: the exit code is the contract, and fang exits 1 on
		// any RunE error, which would collapse handed-back and parked into
		// generic failure. The command exits itself instead.
		Run: func(cmd *cobra.Command, args []string) {
			if code := runRun(cmd, args[0], harness, model, effort); code != run.ExitConverged {
				os.Exit(code)
			}
		},
	}

	cmd.Flags().StringVar(&harness, "harness", "claude-code", "worker harness: claude-code or codex")
	cmd.Flags().StringVar(&model, "model", "", "model for every worker (default: the harness's default)")
	cmd.Flags().StringVar(&effort, "effort", "", "reasoning effort for every worker (default: the harness's default)")
	return cmd
}

func runRun(cmd *cobra.Command, identifier, harness, model, effort string) int {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	fail := func(err error) int {
		fmt.Fprintln(errOut, err)
		return 1
	}

	cl, err := linearFromEnv()
	if err != nil {
		return fail(err)
	}
	cov, err := covenantFromCwd()
	if err != nil {
		return fail(err)
	}
	adapter, err := run.AdapterFor(harness)
	if err != nil {
		return fail(err)
	}
	repo, err := repoRoot()
	if err != nil {
		return fail(err)
	}
	store, err := journal.Default()
	if err != nil {
		return fail(err)
	}

	// The whole run is interruptible, and an interrupt is a reason: the
	// run parks quoting the signal, not an anonymous "context canceled".
	ctx, stop := journal.Interruptible(cmd.Context())
	defer stop()

	outcome, err := run.Execute(ctx, run.Deps{
		Board: cl,
		Cov:   cov,
		// The store root is what tells resume which preserved worktrees
		// are wand's to reclaim and which belong to a human.
		Git:     run.ExecGit{RunsRoot: store.Root},
		Hub:     run.ExecHub{},
		Workers: run.AdapterWorkers{Adapter: adapter},
		Shell:   run.ExecShell{},
		Repo:    repo,
		Harness: adapter.Name(),
		Model:   model,
		Effort:  effort,
		Out:     out,
	}, store, identifier)
	if err != nil {
		return fail(err)
	}
	return outcome.ExitCode()
}

// repoRoot resolves the repository the run acts on from the working
// directory — the worktree, the journal's Meta.Repo, and every git
// operation all hang off it.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: wand run acts on the repo it is run from")
	}
	return strings.TrimSpace(string(out)), nil
}
