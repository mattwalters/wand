package pm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/worker"
)

// Outcome is how a propose run ended, for the caller and the scheduler.
type Outcome struct {
	// Kind reuses the journal's vocabulary: converged, handed_back, parked.
	Kind journal.Outcome
	// Reason is the human sentence explaining Kind.
	Reason string
	// RunID is the journal run this propose happened under.
	RunID string
	// ProposalPath is where the validated proposal was written — set only
	// when Kind is journal.Converged.
	ProposalPath string
}

// Exit codes, the same contract style scope publishes: 1 is deliberately
// absent, meaning the run never started (bad flags, no journal, an empty
// brief) — fang's generic failure exit.
const (
	// ExitProposed: the proposal landed in a file, and its path was printed.
	ExitProposed = 0
	// ExitHandedBack: the scout found the brief's premise wrong; its
	// account is on stdout/journal and no proposal was written.
	ExitHandedBack = 2
	// ExitParked: the run stopped without deciding. The journal says why.
	ExitParked = 3
)

// ExitCode maps the outcome onto the contract.
func (o Outcome) ExitCode() int {
	switch o.Kind {
	case journal.Converged:
		return ExitProposed
	case journal.HandedBack:
		return ExitHandedBack
	default:
		return ExitParked
	}
}

// Execute proposes a set of tickets from one brief.
//
// An error return means no run happened — Deps was invalid, the brief was
// empty, or the journal would not open — and nothing was written anywhere.
// Once a run exists, every path ends in exactly one Outcome, including
// interrupts, which park with the signal as the reason. No Linear write is
// ever made on this path: Board (deps.go) carries no write method at all,
// so the property holds by the type the orchestrator was handed, not by
// discipline inside it.
func Execute(ctx context.Context, d Deps, store *journal.Store, brief string) (Outcome, error) {
	if err := d.validate(); err != nil {
		return Outcome{}, err
	}
	if d.Out == nil {
		d.Out = io.Discard
	}
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return Outcome{}, fmt.Errorf("pm: the brief is empty")
	}

	r, err := store.Create(journal.Meta{
		Ticket:  syntheticID("pm", brief),
		Verb:    "pm",
		Repo:    d.Repo,
		Harness: d.Harness,
	})
	if err != nil {
		return Outcome{}, err
	}
	defer r.Close()
	fmt.Fprintf(d.Out, "proposing from a brief (%d bytes), journaling to %s\n", len(brief), r.Dir())

	p := &proposing{d: d, r: r, brief: brief}
	out := p.run(ctx)
	out.RunID = r.ID()
	fmt.Fprintf(d.Out, "run %s ended: %s — %s\n", r.ID(), out.Kind, out.Reason)
	return out, nil
}

// syntheticID derives a stable, filesystem-safe run identifier for input
// that names no Linear ticket. journal.Meta.Ticket is required and non-empty
// (journal.go), and the run directory name is built from it — a brief has
// no such identifier of its own, so one is derived from its content, which
// also makes two runs over the same unchanged brief produce comparable ids.
func syntheticID(prefix, text string) string {
	sum := sha256.Sum256([]byte(text))
	return prefix + "-" + hex.EncodeToString(sum[:])[:12]
}

// proposing is one run's working state.
type proposing struct {
	d     Deps
	r     *journal.Run
	brief string
}

// phaseDetail mirrors scope's: bounded so the journal stays readable, and
// what lets a crashed run be told from a converged one by reading alone.
type phaseDetail struct {
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Handoff    bool   `json:"handoff"`
	Error      string `json:"error,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
}

const journalTail = 4 << 10

func tailOf(s string) string {
	if len(s) <= journalTail {
		return s
	}
	return "[… clipped …]\n" + s[len(s)-journalTail:]
}

func (p *proposing) run(ctx context.Context) Outcome {
	summary, out := p.readBoard(ctx)
	if out != nil {
		return *out
	}

	draft, out := p.draft(ctx, summary)
	if out != nil {
		return *out
	}
	if draft.Premise == PremiseWrong {
		return p.handback(draft.Reason)
	}

	dupes := p.collisions(ctx, draft)
	return p.write(ctx, draft, dupes)
}

// readBoard fetches the projects and Triage/Backlog titles the scout is
// shown. A read failure parks: without a board, the scout would be
// proposing blind, and a proposal drafted blind is not one worth writing.
func (p *proposing) readBoard(ctx context.Context) (string, *Outcome) {
	if err := p.r.StartPhase("board-read", 1); err != nil {
		return "", p.park(fmt.Sprintf("could not journal the board-read phase: %v", err))
	}
	projects, err := p.d.Board.Projects(ctx, p.d.TeamKey)
	if err != nil {
		return "", p.park(fmt.Sprintf("could not read the team's projects: %v", err))
	}
	triageName := p.d.Cov.StatusName("triage")
	triage, err := p.d.Board.TeamIssuesByState(ctx, p.d.TeamKey, triageName)
	if err != nil {
		return "", p.park(fmt.Sprintf("could not read %s: %v", triageName, err))
	}
	backlogName := p.d.Cov.StatusName("backlog")
	backlog, err := p.d.Board.TeamIssuesByState(ctx, p.d.TeamKey, backlogName)
	if err != nil {
		return "", p.park(fmt.Sprintf("could not read %s: %v", backlogName, err))
	}
	if err := p.r.EndPhase(nil); err != nil {
		return "", p.park(fmt.Sprintf("could not journal the end of the board-read phase: %v", err))
	}
	fmt.Fprintf(p.d.Out, "board read: %d project(s), %d triage, %d backlog\n", len(projects), len(triage), len(backlog))
	return BoardSummary(projects, triage, backlog), nil
}

// draft spawns the scout and validates what it wrote.
func (p *proposing) draft(ctx context.Context, boardSummary string) (Draft, *Outcome) {
	fmt.Fprintf(p.d.Out, "phase scout: spawning a cold worker (%s)\n", p.d.Harness)
	if err := p.r.StartPhase("scout", 1); err != nil {
		return Draft{}, p.park(fmt.Sprintf("could not journal phase scout: %v", err))
	}
	spec := worker.Spec{
		Mode:        "pm (propose tickets from a brief; no worktree, no Linear writes)",
		Rules:       scoutRules(),
		Prompt:      scoutPrompt(p.brief, boardSummary, p.d.Cov),
		Dir:         p.d.Repo,
		ScratchDir:  p.r.ScratchDir(),
		HandoffPath: p.r.HandoffPath(),
		Timeout:     p.d.Cov.Caps.WorkerTimeout,
		Model:       p.d.Model,
		Effort:      p.d.Effort,
		Out:         p.d.Out,
		Label:       "scout",
	}
	res, err := p.d.Workers.Run(ctx, spec)
	detail := phaseDetail{ExitCode: res.ExitCode, TimedOut: res.TimedOut, Handoff: res.Handoff != nil}
	if err != nil {
		detail.Error = err.Error()
		detail.OutputTail = tailOf(res.Output)
	}
	if jerr := p.r.EndPhase(detail); jerr != nil {
		return Draft{}, p.park(fmt.Sprintf("could not journal the end of phase scout: %v", jerr))
	}
	if ctx.Err() != nil {
		return Draft{}, p.parkCtx(ctx)
	}
	if err != nil {
		return Draft{}, p.park(fmt.Sprintf("the scout worker failed: %v", err))
	}

	draft, perr := ParseDraft(res.Handoff)
	if perr != nil {
		return Draft{}, p.park(fmt.Sprintf("the scout's handoff is unusable, so no proposal was written: %v", perr))
	}
	if err := p.r.Note("scout handoff", draft); err != nil {
		fmt.Fprintf(p.d.Out, "journal: %v\n", err)
	}
	return draft, nil
}

// collisions runs the near-duplicate search the ticket's own board-read
// stage calls for, one search per proposed title, so possible duplicates
// surface in the rendered proposal before a human ever reads it. A search
// failure is logged and skipped rather than parking the run: the proposal
// itself is already sound, and losing the collision hint is a worse outcome
// only if it is silent.
func (p *proposing) collisions(ctx context.Context, d Draft) map[string][]linear.Issue {
	dupes := map[string][]linear.Issue{}
	for _, t := range d.Tickets {
		hits, err := p.d.Board.SearchIssues(ctx, p.d.TeamKey, t.Title)
		if err != nil {
			fmt.Fprintf(p.d.Out, "collision search for %q failed, continuing without it: %v\n", t.Title, err)
			continue
		}
		if len(hits) > 0 {
			dupes[strings.ToLower(strings.TrimSpace(t.Title))] = hits
		}
	}
	return dupes
}

// write renders the validated draft to a file — JSON and a human-readable
// Markdown rendering beside it — and converges. No Linear call happens
// here or anywhere else on this path.
func (p *proposing) write(ctx context.Context, draft Draft, dupes map[string][]linear.Issue) Outcome {
	draft.SchemaVersion = SchemaVersion
	jsonPath := filepath.Join(p.r.Dir(), "proposal.json")
	raw, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return *p.park(fmt.Sprintf("could not encode the proposal: %v", err))
	}
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		return *p.park(fmt.Sprintf("could not write the proposal file: %v", err))
	}

	mdPath := filepath.Join(p.r.Dir(), "proposal.md")
	if err := os.WriteFile(mdPath, []byte(ProposalMarkdown(draft, dupes)), 0o644); err != nil {
		return *p.park(fmt.Sprintf("the proposal JSON is at %s, but the readable rendering failed: %v", jsonPath, err))
	}

	fmt.Fprintf(p.d.Out, "wrote the proposal: %s (%s)\n", jsonPath, mdPath)
	reason := fmt.Sprintf("proposed %d ticket(s) and %d new project(s); written to %s", len(draft.Tickets), len(draft.Projects), jsonPath)
	if err := p.r.Converged(reason); err != nil {
		fmt.Fprintf(p.d.Out, "journal: %v\n", err)
	}
	return Outcome{Kind: journal.Converged, Reason: reason, ProposalPath: jsonPath}
}

// handback ends the run with the scout's own account, journal-only: there is
// no ticket in Linear to comment on. The account still reaches the human,
// through stdout and the journal note already written in draft().
func (p *proposing) handback(reason string) Outcome {
	reason = strings.TrimSpace(reason)
	fmt.Fprintf(p.d.Out, "the brief's premise was judged wrong:\n\n%s\n", reason)
	if err := p.r.HandedBack(reason); err != nil {
		fmt.Fprintf(p.d.Out, "journal: %v\n", err)
	}
	return Outcome{Kind: journal.HandedBack, Reason: reason}
}

// park ends the run without deciding, journal-only.
func (p *proposing) park(reason string) *Outcome {
	if err := p.r.Parked(reason); err != nil {
		fmt.Fprintf(p.d.Out, "journal: %v\n", err)
	}
	return &Outcome{Kind: journal.Parked, Reason: reason}
}

// parkCtx parks, preferring the interrupt's own sentence when the context is
// what killed the operation — "interrupted by SIGTERM" explains a run;
// "context canceled" does not.
func (p *proposing) parkCtx(ctx context.Context) *Outcome {
	if ctx.Err() != nil {
		return p.park(context.Cause(ctx).Error())
	}
	return p.park("interrupted")
}
