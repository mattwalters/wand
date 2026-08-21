package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/pm"
	"github.com/mattwalters/wand/internal/worker"
)

func newPMCmd() *cobra.Command {
	var teamKey, harness, model, effort string

	cmd := &cobra.Command{
		Use:   "pm [brief-file]",
		Short: "Propose a set of tickets from a decided product brief",
		Long: "pm sends a cold scout over a brief — a file, or stdin when no file is\n" +
			"given — and this team's board, and asks it to propose the tickets that\n" +
			"should exist: titles, bodies stating the goal and what is still open,\n" +
			"project assignments (including new projects), and blocking order between\n" +
			"the tickets it proposes.\n\n" +
			"The proposal is validated exactly as strictly as wand plan validates a\n" +
			"plan, then written to a file under this run's journal directory — as JSON\n" +
			"and as a human-readable rendering beside it. Nothing is written to\n" +
			"Linear: read the file, edit it if you want to, then run\n" +
			"`wand pm bless` on it to file the approved set.\n\n" +
			"Exit codes are a contract a scheduler can read: 0 proposed, 2 handed back\n" +
			"(the scout judged the brief's premise wrong — already done, or already\n" +
			"tracked), 3 parked, 1 the run never started.\n\n" +
			"Requires LINEAR_API_KEY, and the harness on PATH. Run it inside a repo.",
		Args: cobra.MaximumNArgs(1),
		// Run, not RunE: the exit code is the contract, the same reason
		// plan's own command exits itself rather than returning to fang.
		Run: func(cmd *cobra.Command, args []string) {
			if code := runPM(cmd, args, teamKey, harness, model, effort); code != pm.ExitProposed {
				os.Exit(code)
			}
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND (falls back to [team] key in wand.toml)")
	cmd.Flags().StringVar(&harness, "harness", "claude-code", "worker harness: claude-code or codex")
	cmd.Flags().StringVar(&model, "model", "", "model for the scout (default: the harness's default)")
	cmd.Flags().StringVar(&effort, "effort", "", "reasoning effort for the scout (default: the harness's default)")
	cmd.AddCommand(newPMBlessCmd())
	return cmd
}

func runPM(cmd *cobra.Command, args []string, teamKey, harness, model, effort string) int {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	fail := func(err error) int {
		fmt.Fprintln(errOut, err)
		return 1
	}

	brief, err := readBrief(cmd, args)
	if err != nil {
		return fail(err)
	}
	cov, fileTeamKey, _, err := covenantFromCwd()
	if err != nil {
		return fail(err)
	}
	resolvedTeamKey, err := resolveTeamKey(teamKey, fileTeamKey)
	if err != nil {
		return fail(err)
	}
	cl, err := linearFromEnv()
	if err != nil {
		return fail(err)
	}
	adapter, err := worker.AdapterFor(harness)
	if err != nil {
		return fail(err)
	}
	repo, err := repoRoot("pm")
	if err != nil {
		return fail(err)
	}
	store, err := journal.Default()
	if err != nil {
		return fail(err)
	}

	ctx, stop := journal.Interruptible(cmd.Context())
	defer stop()

	outcome, err := pm.Execute(ctx, pm.Deps{
		Board:   cl,
		Cov:     cov,
		Workers: pm.AdapterWorkers{Adapter: adapter},
		TeamKey: resolvedTeamKey,
		Repo:    repo,
		Harness: adapter.Name(),
		Model:   model,
		Effort:  effort,
		Out:     out,
	}, store, brief)
	if err != nil {
		return fail(err)
	}
	if outcome.ProposalPath != "" {
		fmt.Fprintf(out, "proposal: %s\n", outcome.ProposalPath)
	}
	return outcome.ExitCode()
}

// readBrief reads the brief from a file argument, or from stdin when none
// was given — the same "flag or stdin" contract messageOrStdin gives
// handback and abandon, minus the flag: a brief is the whole point of the
// command's one argument, not an option alongside it.
func readBrief(cmd *cobra.Command, args []string) (string, error) {
	if len(args) == 1 {
		data, err := os.ReadFile(args[0])
		if err != nil {
			// Any failure to open a brief-shaped argument supports the same
			// conclusion, not just os.IsNotExist: a long pasted brief just
			// as often trips the filesystem's own name-too-long error
			// before existence is ever checked.
			if looksLikeBrief(args[0]) {
				return "", fmt.Errorf("%q looks like brief text, not a path: pipe it on stdin instead (`echo \"...\" | wand pm`)", truncate(args[0], briefEchoLimit))
			}
			msg := err.Error()
			var pathErr *fs.PathError
			if errors.As(err, &pathErr) {
				msg = pathErr.Err.Error()
			}
			return "", fmt.Errorf("reading the brief from %s: %s", truncate(args[0], briefEchoLimit), msg)
		}
		return string(data), nil
	}
	if f, ok := cmd.InOrStdin().(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return "", fmt.Errorf("no brief: pass a file, or pipe the brief on stdin")
		}
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("reading the brief from stdin: %w", err)
	}
	return string(data), nil
}

// briefLikeLength is well past any plausible real file path, but well short
// of a brief worth writing: an argument this long that also fails to open
// as a file is prose pasted where a path belongs, not a mistyped path.
const briefLikeLength = 200

// briefEchoLimit bounds how much of a bad argument an error line ever
// echoes, so a brief pasted by mistake can't flood the terminal.
const briefEchoLimit = 80

// looksLikeBrief reports whether s reads as brief prose rather than a
// mistyped path: a newline can never appear in a path a shell would pass as
// a single argument's intent, and length past briefLikeLength is far beyond
// any real filename.
func looksLikeBrief(s string) bool {
	return strings.Contains(s, "\n") || len(s) > briefLikeLength
}

// truncate shortens s to at most n runes, marking the cut with an ellipsis
// so a truncated echo still reads as partial rather than complete.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func newPMBlessCmd() *cobra.Command {
	var teamKey string

	cmd := &cobra.Command{
		Use:   "bless <proposal-file>",
		Short: "File a proposed ticket set into Triage, wiring its blockers",
		Long: "bless re-reads and re-validates a proposal file — the one wand pm wrote,\n" +
			"possibly edited by hand since — against the same schema, re-checks it\n" +
			"against the live board (a project or title may have moved since the file\n" +
			"was written), and then performs the write as the single writer: any new\n" +
			"projects first, then every ticket into Triage with the agent-filed label,\n" +
			"in blocked_by-dependency order, then every blocked_by relation last, once\n" +
			"every ticket in the batch exists.\n\n" +
			"A failure partway through the batch stops rather than retries, and reports\n" +
			"exactly what had already landed — read the run's journal for the detail.\n\n" +
			"Nothing here promotes anything: every ticket lands in Triage, unranked and\n" +
			"unowned, exactly where wand file lands an agent's own finding. Blessing it\n" +
			"onward to Todo stays a human's act.\n\n" +
			"Exit codes: 0 filed, 1 the file did not validate or the board has\n" +
			"drifted (nothing written), 3 a write failed partway through the batch.\n\n" +
			"Requires LINEAR_API_KEY in the environment.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if code := runPMBless(cmd, args[0], teamKey); code != pm.ExitFiled {
				os.Exit(code)
			}
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND (falls back to [team] key in wand.toml)")
	return cmd
}

func runPMBless(cmd *cobra.Command, path, teamKey string) int {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	fail := func(err error) int {
		fmt.Fprintln(errOut, err)
		return 1
	}

	cov, fileTeamKey, _, err := covenantFromCwd()
	if err != nil {
		return fail(err)
	}
	resolvedTeamKey, err := resolveTeamKey(teamKey, fileTeamKey)
	if err != nil {
		return fail(err)
	}
	cl, err := linearFromEnv()
	if err != nil {
		return fail(err)
	}
	repo, err := repoRoot("pm bless")
	if err != nil {
		return fail(err)
	}
	store, err := journal.Default()
	if err != nil {
		return fail(err)
	}

	ctx, stop := journal.Interruptible(cmd.Context())
	defer stop()

	outcome, err := pm.Bless(ctx, pm.BlessDeps{
		Board:   cl,
		Cov:     cov,
		TeamKey: resolvedTeamKey,
		Repo:    repo,
		Out:     out,
	}, store, path)
	if err != nil {
		return fail(err)
	}
	return outcome.ExitCode()
}
