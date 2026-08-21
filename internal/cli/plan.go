package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/plan"
	"github.com/mattwalters/wand/internal/worker"
)

func newPlanCmd() *cobra.Command {
	var harness, model, effort string
	var interactive bool

	cmd := &cobra.Command{
		Use:   "plan <identifier>",
		Short: "Research one To Plan ticket into a plan a human can bless",
		Long: "plan claims one ticket into In Planning, sends a cold, read-only scout over\n" +
			"this repository to research it, validates what it hands back, and writes\n" +
			"the result: the plan into the ticket's description, the approaches and\n" +
			"their trade-offs as a comment, the estimate, and Plan Review last — so\n" +
			"every deliverable has landed before the status that advertises it.\n" +
			"Nothing is written unless the whole handoff passes validation.\n\n" +
			"The ticket must be in To Plan, or already In Planning carrying the\n" +
			"re-plan label wand sweep's own hand-back leaves — a re-plan, which\n" +
			"reads the comments left on the plan since and revises it in place\n" +
			"instead of drafting from nothing. Blessing research is a human act,\n" +
			"the same way blessing building is, and plan will not take a ticket\n" +
			"nobody blessed. It ends in Plan Review: promoting the plan to Todo is\n" +
			"yours — that promotion is what approves it.\n\n" +
			"There is no worktree, no branch and no CI: the scout reads the checkout you\n" +
			"run this from and may not change it. If it does, the run parks and leaves\n" +
			"the change for you to look at.\n\n" +
			"--interactive grills you over the draft before anything is written —\n" +
			"questions worst-consequence first, each quoting the draft — and hands your\n" +
			"answers to a second, fresh session to revise, because a session that has\n" +
			"just argued for an approach defends it. It needs a terminal; an unattended\n" +
			"caller must not pass it.\n\n" +
			"Exit codes are a contract a scheduler can read: 0 planned, 2 handed back\n" +
			"(the scout judged the ticket's premise wrong), 3 parked, 1 the run never\n" +
			"started.\n\n" +
			"Requires LINEAR_API_KEY, and the harness on PATH. Run it inside the repo.",
		Args: cobra.ExactArgs(1),
		// Run, not RunE: the exit code is the contract, and fang exits 1 on
		// any RunE error, which would collapse handed-back and parked into
		// generic failure. The command exits itself instead.
		Run: func(cmd *cobra.Command, args []string) {
			if code := runPlan(cmd, args[0], harness, model, effort, interactive); code != plan.ExitPlanReviewed {
				os.Exit(code)
			}
		},
	}

	cmd.Flags().StringVar(&harness, "harness", "claude-code", "worker harness: claude-code or codex")
	cmd.Flags().StringVar(&model, "model", "", "model for every worker (default: the harness's default)")
	cmd.Flags().StringVar(&effort, "effort", "", "reasoning effort for every worker (default: the harness's default)")
	cmd.Flags().BoolVar(&interactive, "interactive", false, "interview you over the draft before writing anything (needs a terminal)")
	return cmd
}

func runPlan(cmd *cobra.Command, identifier, harness, model, effort string, interactive bool) int {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	fail := func(err error) int {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if interactive {
		// Refuse before anything happens rather than block forever on a
		// read nobody is there to answer. An unattended caller that passes
		// the flag gets a refusal it can see in its logs, which is the
		// failure it can act on; a plan run hung on a pipe is not.
		if err := requireTTY(cmd); err != nil {
			return fail(err)
		}
	}

	cl, err := linearFromEnv()
	if err != nil {
		return fail(err)
	}
	cov, _, _, err := covenantFromCwd()
	if err != nil {
		return fail(err)
	}
	adapter, err := worker.AdapterFor(harness)
	if err != nil {
		return fail(err)
	}
	repo, err := repoRoot("plan")
	if err != nil {
		return fail(err)
	}
	store, err := journal.Default()
	if err != nil {
		return fail(err)
	}

	// The whole run is interruptible, and an interrupt is a reason: the run
	// parks quoting the signal, not an anonymous "context canceled".
	ctx, stop := journal.Interruptible(cmd.Context())
	defer stop()

	outcome, err := plan.Execute(ctx, plan.Deps{
		Board:       cl,
		Cov:         cov,
		Workers:     plan.AdapterWorkers{Adapter: adapter},
		Tree:        plan.ExecTree{},
		Repo:        repo,
		Harness:     adapter.Name(),
		Model:       model,
		Effort:      effort,
		Interactive: interactive,
		In:          cmd.InOrStdin(),
		Out:         out,
	}, store, identifier)
	if err != nil {
		return fail(err)
	}
	return outcome.ExitCode()
}

// requireTTY refuses an interview with nobody on the other end. The
// interview is the one place wand blocks on a human, so it is the one place
// that has to prove there is one.
func requireTTY(cmd *cobra.Command) error {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return fmt.Errorf("--interactive needs a terminal to read your answers from")
	}
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("--interactive needs a terminal to read your answers from: %w", err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("--interactive needs a terminal: stdin is a pipe or a file here, and an interview nobody can answer would hang the run. Drop the flag for an unattended plan run")
	}
	return nil
}
