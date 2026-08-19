package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/verbs"
)

// verbSetup is the preamble every lifecycle verb shares: a client from the
// environment, the covenant from the repo, a bounded context.
func verbSetup(cmd *cobra.Command) (*linear.Client, covenant.Covenant, context.Context, context.CancelFunc, error) {
	cl, err := linearFromEnv()
	if err != nil {
		return nil, covenant.Covenant{}, nil, nil, err
	}
	cov, err := covenantFromCwd()
	if err != nil {
		return nil, covenant.Covenant{}, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), apiTimeout)
	return cl, cov, ctx, cancel, nil
}

// covenantFromCwd finds the nearest wand.toml walking up from the working
// directory — the file lives at the repo root, and a verb run from a
// subdirectory must see the same covenant as one run at the root. A
// cwd-only lookup would silently vet a claim against the stock covenant on
// a board whose columns the file renames. No file anywhere up means the
// stock covenant, same as covenant.Load.
func covenantFromCwd() (covenant.Covenant, error) {
	dir, err := os.Getwd()
	if err != nil {
		return covenant.Covenant{}, err
	}
	for {
		cov, fromFile, err := covenant.Load(filepath.Join(dir, covenant.FileName))
		if err != nil {
			return covenant.Covenant{}, err
		}
		if fromFile {
			return cov, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return covenant.Default(), nil
		}
		dir = parent
	}
}

// messageOrStdin resolves a comment body: the -m flag when given, stdin
// otherwise — long markdown reads better from a heredoc than from a quoted
// flag. Only a flag the user actually passed counts as given (a blank -m is
// an error the verb reports, not a cue to read stdin), and an interactive
// terminal refuses rather than blocking on a read the user cannot see.
func messageOrStdin(cmd *cobra.Command, flagSet bool, flagValue string) (string, error) {
	if flagSet {
		return flagValue, nil
	}
	if f, ok := cmd.InOrStdin().(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return "", fmt.Errorf("no message: pass -m, or pipe the message on stdin (a heredoc works well for long markdown)")
		}
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("reading the message from stdin: %w", err)
	}
	return string(data), nil
}

func newClaimCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "claim <identifier>",
		Short: "Take one blessed issue: In Progress, assigned, before any work",
		Long: "claim vets the issue exactly as queue does — it must sit in Todo, not be\n" +
			"labeled human-only, and have no unresolved blockers — then moves it to\n" +
			"In Progress and assigns it to the key holder in a single write.\n\n" +
			"Claim before anything touches the filesystem: the status move is the\n" +
			"cheapest place to lose a race. Two sessions that both branch and then\n" +
			"claim have each done work one must throw away; two that claim first\n" +
			"collide before either has anything.\n\n" +
			"Requires LINEAR_API_KEY in the environment.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, cov, ctx, cancel, err := verbSetup(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			claimed, err := verbs.Claim(ctx, cl, cov, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "claimed %s: %s, assigned to %s\n",
				claimed.Issue.Identifier, cov.StatusName("in_progress"), claimed.Assignee)
			if claimed.Issue.BranchName != "" {
				fmt.Fprintf(out, "branch:  %s\n", claimed.Issue.BranchName)
			}
			return nil
		},
	}
}

func newHandbackCmd() *cobra.Command {
	var message string

	cmd := &cobra.Command{
		Use:   "handback <identifier>",
		Short: "Park one issue on a human: the question first, Needs Input second",
		Long: "handback posts your question as a comment — what you need to know, the\n" +
			"options you see, which you would pick — and then moves the issue to\n" +
			"Needs Input. In that order, always: a failure between the two leaves an\n" +
			"In Progress ticket carrying its question, never a Needs Input ticket\n" +
			"that asks nothing and so parks forever.\n\n" +
			"The question comes from -m, or from stdin when -m is absent.\n\n" +
			"Requires LINEAR_API_KEY in the environment.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question, err := messageOrStdin(cmd, cmd.Flags().Changed("message"), message)
			if err != nil {
				return err
			}
			cl, cov, ctx, cancel, err := verbSetup(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			issue, err := verbs.Handback(ctx, cl, cov, args[0], question)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "handed back %s: question posted, now %s\n",
				issue.Identifier, cov.StatusName("needs_input"))
			return nil
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "the question: what you need, the options, your pick")
	return cmd
}

func newAbandonCmd() *cobra.Command {
	var message, replace, with string

	cmd := &cobra.Command{
		Use:   "abandon <identifier>",
		Short: "Hand one issue back to Backlog, with the evidence that undoes it",
		Long: "abandon posts your evidence as a comment, then — in a single write —\n" +
			"corrects the description, moves the issue to Backlog and unassigns it.\n" +
			"Pass the exact wording the evidence disproved with --replace and what\n" +
			"should stand instead with --with; the old wording is quoted into the\n" +
			"comment, because Linear's description history is where corrections go\n" +
			"to be forgotten. The anchor must match exactly once — anything else\n" +
			"refuses before writing, rather than guess in someone else's prose.\n\n" +
			"Never Canceled, Done or Duplicate: closing is a human's call however\n" +
			"wrong the ticket turned out to be, and the guard enforces it.\n\n" +
			"The evidence comes from -m, or from stdin when -m is absent.\n\n" +
			"Requires LINEAR_API_KEY in the environment.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			replaceSet := cmd.Flags().Changed("replace")
			withSet := cmd.Flags().Changed("with")
			if replaceSet != withSet {
				return errors.New("the --replace and --with flags travel together: the wording to correct, and what stands in its place (--with \"\" deletes it)")
			}
			var corr *verbs.Correction
			if replaceSet {
				corr = &verbs.Correction{Old: replace, New: with}
			}

			evidence, err := messageOrStdin(cmd, cmd.Flags().Changed("message"), message)
			if err != nil {
				return err
			}
			cl, cov, ctx, cancel, err := verbSetup(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			issue, err := verbs.Abandon(ctx, cl, cov, args[0], evidence, corr)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "abandoned %s: evidence posted, now %s, unassigned\n",
				issue.Identifier, cov.StatusName("backlog"))
			if corr != nil {
				fmt.Fprintln(out, "description corrected in the same write")
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "the evidence: what you found and why it undoes the premise")
	cmd.Flags().StringVar(&replace, "replace", "", "exact description wording the evidence disproved")
	cmd.Flags().StringVar(&with, "with", "", "what the description should say instead (empty deletes)")
	return cmd
}

func newFileCmd() *cobra.Command {
	var teamKey, description string
	var force bool

	cmd := &cobra.Command{
		Use:   "file <title>",
		Short: "File a finding into Triage, after searching for duplicates",
		Long: "file searches the team for near-duplicates of the title first. If the\n" +
			"search finds candidates, nothing is filed: read them, and either add a\n" +
			"comment to the issue that already exists or re-run with --force. Past\n" +
			"the search, the issue lands in Triage with the agent-filed label, no\n" +
			"priority and no assignee — ranking and owning work are part of blessing\n" +
			"it, and an agent never promotes what it filed.\n\n" +
			"Requires LINEAR_API_KEY in the environment.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if teamKey == "" {
				return fmt.Errorf("--team-key is required (e.g. --team-key WND)")
			}
			cl, cov, ctx, cancel, err := verbSetup(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			res, err := verbs.File(ctx, cl, cov, verbs.FileRequest{
				TeamKey:     teamKey,
				Title:       args[0],
				Description: description,
				Force:       force,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if res.Created == nil {
				fmt.Fprintln(out, "the search found existing issues near that title:")
				idWidth := 0
				for _, d := range res.Duplicates {
					idWidth = max(idWidth, len(d.Identifier))
				}
				for _, d := range res.Duplicates {
					fmt.Fprintf(out, "  %-*s  %-12s %s\n", idWidth, d.Identifier, d.State.Name, d.Title)
				}
				return fmt.Errorf("not filed: rule these out first — comment on the existing issue, or re-run with --force if this is genuinely new work")
			}
			fmt.Fprintf(out, "filed %s into %s (%s): %s\n",
				res.Created.Identifier, cov.StatusName("triage"), verbs.AgentFiledLabel, res.Created.Title)
			if res.Created.URL != "" {
				fmt.Fprintf(out, "url:   %s\n", res.Created.URL)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND")
	cmd.Flags().StringVarP(&description, "description", "d", "", "the finding: what you saw, where, and why it matters")
	cmd.Flags().BoolVar(&force, "force", false, "file even though the search found candidates")
	return cmd
}
