package pm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/verbs"
)

// WriteBoard is what the bless stage writes through: Board's reads, for the
// re-check against a board that may have moved since the file was written,
// plus every write the batched file needs.
type WriteBoard interface {
	Board
	TeamByKey(ctx context.Context, key string) (linear.Team, error)
	TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error)
	LabelByName(ctx context.Context, name string) (linear.Label, bool, error)
	CreateProject(ctx context.Context, teamID, name, description string) (linear.Project, error)
	CreateIssue(ctx context.Context, in linear.IssueCreate) (linear.Issue, error)
	// CreateBlocker records blockedIssueID as blocked by blockerIssueID.
	// Both issues must already exist; Bless calls this only after every
	// ticket in the batch has been created.
	CreateBlocker(ctx context.Context, blockedIssueID, blockerIssueID string) error
}

// BlessDeps is everything the bless stage acts through.
type BlessDeps struct {
	Board   WriteBoard
	Cov     covenant.Covenant
	TeamKey string
	// Repo is the absolute path the journal's Meta.Repo names.
	Repo string
	Out  io.Writer
}

func (d BlessDeps) validate() error {
	switch {
	case d.Board == nil:
		return errors.New("pm: BlessDeps.Board is required")
	case d.TeamKey == "":
		return errors.New("pm: BlessDeps.TeamKey is required")
	case d.Repo == "":
		return errors.New("pm: BlessDeps.Repo is required")
	case !filepath.IsAbs(d.Repo):
		return fmt.Errorf("pm: BlessDeps.Repo must be absolute, got %q", d.Repo)
	case d.Cov.StatusName("triage") == "":
		return errors.New("pm: the covenant has no triage status; this is a wand bug")
	}
	return nil
}

// BlessOutcome is how a bless run ended.
type BlessOutcome struct {
	Kind   journal.Outcome
	Reason string
	RunID  string
	// Filed is every ticket actually created, title to Linear identifier —
	// populated even on a park, so a partial batch reports exactly what
	// landed rather than leaving that to be discovered in Linear later.
	Filed map[string]string
	// FiledProjects is every project actually created, name to ID.
	FiledProjects map[string]string
}

// ExitFiled is bless's success code: every project and ticket in the
// proposal landed, and every blocked_by relation was wired.
//
// The rest of the contract reuses propose's constants: ExitParked (a write
// failed partway through the batch; BlessOutcome.Filed / FiledProjects say
// exactly what landed) and, implicitly, exit 1 for a refusal before
// anything was written — a proposal file that fails validation, or one
// whose board has drifted since it was drafted. That "1" is the same
// "the run never started" meaning plan and dispatch give it, so bless
// returns a plain error for it rather than a BlessOutcome, the same way
// Execute does for propose's own never-started cases.
const ExitFiled = 0

// ExitCode maps the outcome onto the contract.
func (o BlessOutcome) ExitCode() int {
	if o.Kind == journal.Converged {
		return ExitFiled
	}
	return ExitParked
}

// Bless reads a proposal file, re-validates it, re-checks it against the
// live board, and performs the batched write as the single writer: any new
// projects first, then every ticket in blocked_by-dependency order, then
// every blocked_by relation last, once every ticket in the batch exists.
//
// A refusal before any write returns a plain error and writes nothing —
// the file did not parse, its schema is newer than this binary speaks, or
// the board has moved under it (a project or a title the proposal named no
// longer resolves the way it did when the file was written). Past that
// point, a journal run is opened and every further failure parks, naming
// exactly what had already landed — a batch stops rather than retries,
// because a bless that guessed at what to skip re-filing could as easily
// double-file it.
func Bless(ctx context.Context, d BlessDeps, store *journal.Store, path string) (BlessOutcome, error) {
	if err := d.validate(); err != nil {
		return BlessOutcome{}, err
	}
	if d.Out == nil {
		d.Out = io.Discard
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return BlessOutcome{}, fmt.Errorf("pm: reading %s: %w", path, err)
	}
	draft, err := ParseProposal(raw)
	if err != nil {
		return BlessOutcome{}, fmt.Errorf("pm: %s is not a usable proposal: %w", path, err)
	}
	if draft.Premise == PremiseWrong {
		return BlessOutcome{}, fmt.Errorf("pm: this proposal reports the brief's premise as wrong: %s — nothing to bless", firstLine(draft.Reason))
	}

	team, err := d.Board.TeamByKey(ctx, d.TeamKey)
	if err != nil {
		return BlessOutcome{}, err
	}
	if team.ID == "" {
		return BlessOutcome{}, fmt.Errorf("pm: no team with key %q", d.TeamKey)
	}
	existingProjects, err := d.Board.Projects(ctx, d.TeamKey)
	if err != nil {
		return BlessOutcome{}, err
	}
	if err := checkProjectRefs(draft, existingProjects); err != nil {
		return BlessOutcome{}, fmt.Errorf("pm: the board has drifted since this proposal was written: %w", err)
	}
	if err := checkTitleCollisions(ctx, d.Board, d.TeamKey, d.Cov, draft); err != nil {
		return BlessOutcome{}, fmt.Errorf("pm: the board has drifted since this proposal was written: %w", err)
	}

	r, err := store.Create(journal.Meta{
		Ticket: syntheticID("pm-bless", string(raw)),
		Verb:   "pm-bless",
		Repo:   d.Repo,
	})
	if err != nil {
		return BlessOutcome{}, err
	}
	defer r.Close()
	fmt.Fprintf(d.Out, "blessing %s, journaling to %s\n", path, r.Dir())

	b := &blessing{
		d: d, r: r, team: team, draft: draft,
		projectIDs:      map[string]string{},
		createdProjects: map[string]string{},
		filed:           map[string]string{},
		filedIDs:        map[string]string{},
	}
	for _, p := range existingProjects {
		b.projectIDs[strings.ToLower(strings.TrimSpace(p.Name))] = p.ID
	}
	out := b.write(ctx)
	out.RunID = r.ID()
	fmt.Fprintf(d.Out, "run %s ended: %s — %s\n", r.ID(), out.Kind, out.Reason)
	return out, nil
}

// checkProjectRefs re-checks every ticket's Project against the board read
// fresh: it must resolve to one of the draft's own proposed projects or to
// a project already on the board. ParseDraft cannot make this check — it
// has no board — so it happens here, at the one point Bless has read one.
func checkProjectRefs(d Draft, existingProjects []linear.Project) error {
	proposed := map[string]bool{}
	for _, p := range d.Projects {
		proposed[strings.ToLower(strings.TrimSpace(p.Name))] = true
	}
	existing := map[string]bool{}
	for _, p := range existingProjects {
		existing[strings.ToLower(strings.TrimSpace(p.Name))] = true
	}
	for _, t := range d.Tickets {
		ref := strings.TrimSpace(t.Project)
		if ref == "" {
			continue
		}
		key := strings.ToLower(ref)
		if !proposed[key] && !existing[key] {
			return fmt.Errorf("ticket %q names project %q, which is neither one of this proposal's own projects nor an existing project on the board", t.Title, ref)
		}
	}
	return nil
}

// checkTitleCollisions refuses a proposal whose title now exactly matches
// an issue already sitting in Triage or Backlog — the case a human blessing
// a stale file could not have weighed, because it did not exist when the
// proposal was drafted. A near-duplicate that already existed at propose
// time was surfaced in the rendered proposal for the human to judge; this
// checks only for what changed since.
func checkTitleCollisions(ctx context.Context, board Board, teamKey string, cov covenant.Covenant, d Draft) error {
	triage, err := board.TeamIssuesByState(ctx, teamKey, cov.StatusName("triage"))
	if err != nil {
		return err
	}
	backlog, err := board.TeamIssuesByState(ctx, teamKey, cov.StatusName("backlog"))
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, i := range triage {
		existing[strings.ToLower(strings.TrimSpace(i.Title))] = true
	}
	for _, i := range backlog {
		existing[strings.ToLower(strings.TrimSpace(i.Title))] = true
	}
	for _, t := range d.Tickets {
		if existing[strings.ToLower(strings.TrimSpace(t.Title))] {
			return fmt.Errorf("a ticket titled %q now already exists in %s or %s; re-run wand pm for a fresh proposal", t.Title, cov.StatusName("triage"), cov.StatusName("backlog"))
		}
	}
	return nil
}

// blessing is one bless run's working state.
type blessing struct {
	d     BlessDeps
	r     *journal.Run
	team  linear.Team
	draft Draft

	// projectIDs accumulates name (lowercased) to Linear project ID as the
	// batch proceeds, seeded from the live board before any write — so
	// project resolution (createTickets) sees existing and freshly-created
	// projects the same way, through one map.
	projectIDs map[string]string
	// createdProjects is the strict subset of projectIDs this run actually
	// created, name (as proposed, not lowercased) to ID — what
	// BlessOutcome.FiledProjects reports, kept separate from projectIDs so
	// a proposed project whose name happens to match one already on the
	// board is correctly reported as reused, not filed.
	createdProjects map[string]string
	// filed accumulates title (lowercased) to the created issue's
	// human-readable identifier — what a partial-failure park reports, so a
	// human reads exactly what already exists without re-deriving it from
	// Linear.
	filed map[string]string
	// filedIDs is the same set keyed to the internal Linear ID rather than
	// the identifier: CreateBlocker takes ids, and wireBlockers runs after
	// every ticket in the batch is in this map.
	filedIDs map[string]string
}

func (b *blessing) write(ctx context.Context) BlessOutcome {
	stateID, err := verbs.ResolveState(ctx, b.d.Board, b.d.Cov, b.team.ID, "triage")
	if err != nil {
		return b.park(fmt.Sprintf("could not resolve the %s status: %v", b.d.Cov.StatusName("triage"), err))
	}
	labelID, err := agentFiledLabelID(ctx, b.d.Board)
	if err != nil {
		return b.park(err.Error())
	}

	if out := b.createProjects(ctx); out != nil {
		return *out
	}
	if out := b.createTickets(ctx, stateID, labelID); out != nil {
		return *out
	}
	if out := b.wireBlockers(ctx); out != nil {
		return *out
	}

	reason := fmt.Sprintf("filed %d ticket(s) and %d project(s) into %s", len(b.filed), len(b.createdProjects), b.d.Cov.StatusName("triage"))
	if err := b.r.Converged(reason); err != nil {
		fmt.Fprintf(b.d.Out, "journal: %v\n", err)
	}
	return BlessOutcome{Kind: journal.Converged, Reason: reason, Filed: b.filed, FiledProjects: b.createdProjects}
}

func (b *blessing) createProjects(ctx context.Context) *BlessOutcome {
	for _, p := range b.draft.Projects {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		if _, exists := b.projectIDs[key]; exists {
			// Already on the board under this name; reuse it rather than
			// creating a second project with the same name.
			continue
		}
		created, err := b.d.Board.CreateProject(ctx, b.team.ID, p.Name, p.Description)
		if err != nil {
			out := b.park(fmt.Sprintf("creating project %q failed: %v", p.Name, err))
			return &out
		}
		b.projectIDs[key] = created.ID
		b.createdProjects[p.Name] = created.ID
		fmt.Fprintf(b.d.Out, "created project %s\n", p.Name)
		b.note("created project", created)
	}
	return nil
}

func (b *blessing) createTickets(ctx context.Context, stateID, labelID string) *BlessOutcome {
	for _, t := range TopoOrder(b.draft.Tickets) {
		description, err := TicketDescription(t)
		if err != nil {
			out := b.park(fmt.Sprintf("ticket %q: composing its description failed: %v", t.Title, err))
			return &out
		}
		var projectID string
		if ref := strings.TrimSpace(t.Project); ref != "" {
			projectID = b.projectIDs[strings.ToLower(ref)]
		}
		created, err := b.d.Board.CreateIssue(ctx, linear.IssueCreate{
			TeamID:      b.team.ID,
			Title:       t.Title,
			Description: description,
			StateID:     stateID,
			LabelIDs:    []string{labelID},
			ProjectID:   projectID,
		})
		if err != nil {
			out := b.park(fmt.Sprintf("filing ticket %q failed: %v", t.Title, err))
			return &out
		}
		key := strings.ToLower(strings.TrimSpace(t.Title))
		b.filed[key] = created.Identifier
		b.filedIDs[key] = created.ID
		fmt.Fprintf(b.d.Out, "filed %s: %s\n", created.Identifier, t.Title)
		b.note("filed ticket", created)
	}
	return nil
}

// wireBlockers runs last, once every ticket in the batch has been created,
// so CreateBlocker always has both ends to point at.
func (b *blessing) wireBlockers(ctx context.Context) *BlessOutcome {
	for _, t := range b.draft.Tickets {
		blockedID, ok := b.filedIDs[strings.ToLower(strings.TrimSpace(t.Title))]
		if !ok {
			continue // should not happen: createTickets files every ticket or this run has already parked
		}
		for _, dep := range t.BlockedBy {
			blockerID, ok := b.filedIDs[strings.ToLower(strings.TrimSpace(dep))]
			if !ok {
				continue
			}
			if err := b.d.Board.CreateBlocker(ctx, blockedID, blockerID); err != nil {
				out := b.park(fmt.Sprintf("every ticket is filed, but %s blocked by %s failed to wire: %v", t.Title, dep, err))
				return &out
			}
			fmt.Fprintf(b.d.Out, "wired %q blocked by %q\n", t.Title, dep)
		}
	}
	return nil
}

// agentFiledLabelID mirrors verbs.agentFiledLabelID, unexported there and
// needed here for the same reason `wand file` needs it: every ticket pm
// files carries the same "an agent proposed this" mark.
func agentFiledLabelID(ctx context.Context, cl WriteBoard) (string, error) {
	label, found, err := cl.LabelByName(ctx, verbs.AgentFiledLabel)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no %q label anywhere in the workspace; run `wand init` to bring the team to the covenant", verbs.AgentFiledLabel)
	}
	return label.ID, nil
}

// park ends the run naming exactly what had already landed — every project
// and every ticket filed so far — so a human reading the journal knows what
// to expect on the board without re-deriving it.
func (b *blessing) park(reason string) BlessOutcome {
	full := reason
	if len(b.filed) > 0 || len(b.createdProjects) > 0 {
		full = fmt.Sprintf("%s (already filed: %d project(s), %d ticket(s))", reason, len(b.createdProjects), len(b.filed))
	}
	if err := b.r.Parked(full); err != nil {
		fmt.Fprintf(b.d.Out, "journal: %v\n", err)
	}
	return BlessOutcome{Kind: journal.Parked, Reason: full, Filed: b.filed, FiledProjects: b.createdProjects}
}

func (b *blessing) note(message string, detail any) {
	if err := b.r.Note(message, detail); err != nil {
		fmt.Fprintf(b.d.Out, "journal: %v\n", err)
	}
}
