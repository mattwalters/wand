// Package verbs implements the single-writer lifecycle actions an
// interactive session performs: claim, handback, abandon, file.
//
// Each verb encodes an ordering rule the reference system carried as prose —
// and prose ordering is remembered ordering, which is to say sometimes
// ordering. Here the sequence is the code:
//
//   - claim moves the status before anything touches the filesystem, because
//     the status move is the cheapest place to lose a race: two sessions that
//     both branch and then both claim have each done work one must throw
//     away, while two that claim first collide before either has anything.
//   - handback posts the question before setting Needs Input, so a failure
//     between the two never leaves a Needs Input ticket with no question on
//     it — that ticket parks forever, because the human it waits on was
//     never asked anything.
//   - abandon posts the evidence first, then corrects the description,
//     moves to Backlog and unassigns in one write, so the body stops
//     asserting the false premise in the same act that demotes the ticket.
//   - file searches for near-duplicates before filing, and files into
//     Triage with the agent-filed label and no priority. An agent never
//     promotes what it filed.
//
// Every status this package writes passes through guard.CheckState first —
// the same verdict function behind the hook — so wand's own write path
// cannot drift from what the guard promises.
//
// The package is deliberately thin over its inputs: decisions live here,
// I/O lives behind the Linear interface, and the CLI commands are wiring.
package verbs

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/guard"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
)

// AgentFiledLabel marks issues an agent filed into Triage. Like the
// human-only label, it is covenant topology, not a parameter — no covenant
// file renames it (see covenant.Default).
const AgentFiledLabel = "agent-filed"

// Linear is the slice of the Linear client the verbs use. An interface so
// tests can hold a fake that records call order — the ordering rules above
// are the point of this package, and a test must be able to see them.
type Linear interface {
	IssueByIdentifier(ctx context.Context, identifier string) (linear.Issue, error)
	TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error)
	Viewer(ctx context.Context) (linear.User, error)
	CreateComment(ctx context.Context, issueID, body string) error
	UpdateIssue(ctx context.Context, issueID string, u linear.IssueUpdate) error
	TeamByKey(ctx context.Context, key string) (linear.Team, error)
	LabelByName(ctx context.Context, name string) (linear.Label, bool, error)
	CreateIssue(ctx context.Context, in linear.IssueCreate) (linear.Issue, error)
	SearchIssues(ctx context.Context, teamKey, term string) ([]linear.Issue, error)
}

// resolveState turns a covenant status key into the team's workflow state
// UUID, passing the display name through the guard on the way. The guard
// check is belt over braces — no verb here writes a forbidden status — but
// it keeps the promise that every wand write path calls the one verdict
// function, covenant renames included.
func resolveState(ctx context.Context, cl Linear, cov covenant.Covenant, teamID, key string) (string, error) {
	name := cov.StatusName(key)
	if name == "" {
		return "", fmt.Errorf("the covenant has no %q status; this is a wand bug", key)
	}
	if reason, blocked := guard.CheckState(name); blocked {
		return "", fmt.Errorf("refusing to set status %q:\n\n%s", name, reason)
	}
	states, err := cl.TeamStates(ctx, teamID)
	if err != nil {
		return "", err
	}
	if id, ok := linear.StateIDByName(states, name); ok {
		return id, nil
	}
	return "", fmt.Errorf(
		"the team has no status named %q; the board has drifted from the covenant — run `wand doctor`, and `wand init` to repair", name)
}

// refuseIfClosed rejects verbs against a closed ticket. The guard judges only
// the destination status, so without this gate an abandon or handback against
// a Done or Canceled ticket would silently reopen it — undoing a close, which
// is as much a human's call as making one.
func refuseIfClosed(issue linear.Issue, verb string) error {
	switch issue.State.Type {
	case "completed", "canceled":
		return fmt.Errorf(
			"%s is closed (%q), and reopening a closed ticket is a human's call — %s would silently undo that close. If the close was wrong, say so in a comment and leave the status to a person",
			issue.Identifier, issue.State.Name, verb)
	}
	return nil
}

// Claimed is what a successful claim reports: the issue as it stood, and
// who now holds it.
type Claimed struct {
	Issue    linear.Issue // as read before the transition; BranchName is the branch to start from
	Assignee string       // the viewer's display name
}

// Claim vets one issue and takes it: In Progress, assigned to the key
// holder, in a single write, before anything touches the filesystem. The
// vetting is the queue's own (human-only, unresolved blockers) plus the
// status gate — claim starts blessed work, so only Todo is claimable.
func Claim(ctx context.Context, cl Linear, cov covenant.Covenant, identifier string) (Claimed, error) {
	issue, err := cl.IssueByIdentifier(ctx, identifier)
	if err != nil {
		return Claimed{}, err
	}
	todo := cov.StatusName("todo")
	if !strings.EqualFold(issue.State.Name, todo) {
		return Claimed{}, fmt.Errorf(
			"%s is in %q, not %q: claim starts blessed work, and blessing is a human act — an issue outside %q is not yours to start",
			issue.Identifier, issue.State.Name, todo, todo)
	}
	if reason := queue.Vet(issue); reason != "" {
		return Claimed{}, fmt.Errorf("%s may not be started: %s", issue.Identifier, reason)
	}

	viewer, err := cl.Viewer(ctx)
	if err != nil {
		return Claimed{}, err
	}
	stateID, err := resolveState(ctx, cl, cov, issue.TeamID, "in_progress")
	if err != nil {
		return Claimed{}, err
	}
	// One write: a claim that set the status and then failed to assign would
	// look owned by nobody while reading as taken.
	err = cl.UpdateIssue(ctx, issue.ID, linear.IssueUpdate{
		StateID:    stateID,
		AssigneeID: viewer.ID,
	})
	if err != nil {
		return Claimed{}, err
	}
	return Claimed{Issue: issue, Assignee: viewer.Name}, nil
}

// Handback parks one issue on a human: the question as a comment first,
// Needs Input second. The order is the contract — if the status write fails,
// the ticket stays In Progress carrying its question, which a later pass can
// finish; the reverse failure leaves a Needs Input ticket that asks nothing,
// and that ticket parks forever. A ticket a human already closed refuses:
// reopening a close is their call, not the verb's.
func Handback(ctx context.Context, cl Linear, cov covenant.Covenant, identifier, question string) (linear.Issue, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return linear.Issue{}, fmt.Errorf(
			"handback needs the question: what you need to know, the options you see, and which you would pick — a Needs Input ticket with no question on it parks forever")
	}
	issue, err := cl.IssueByIdentifier(ctx, identifier)
	if err != nil {
		return linear.Issue{}, err
	}
	if err := refuseIfClosed(issue, "handback"); err != nil {
		return linear.Issue{}, err
	}
	// Resolve the target before the comment: resolveState is a pure read, so
	// running it first means a drifted board or a guard refusal stops the
	// verb with nothing written — a failure after the comment would make a
	// repaired re-run post the question twice.
	stateID, err := resolveState(ctx, cl, cov, issue.TeamID, "needs_input")
	if err != nil {
		return linear.Issue{}, err
	}
	if err := cl.CreateComment(ctx, issue.ID, question); err != nil {
		return linear.Issue{}, err
	}
	if err := cl.UpdateIssue(ctx, issue.ID, linear.IssueUpdate{StateID: stateID}); err != nil {
		return linear.Issue{}, err
	}
	return issue, nil
}

// Correction is one anchored edit to the description: the exact wording the
// evidence disproved, and what should stand in its place. New may be empty
// to delete the wording outright.
type Correction struct {
	Old string
	New string
}

// note renders the correction into the evidence comment, quoting the old
// wording. The quote is the record: Linear surfaces description history
// poorly, so the comment is where the disproven claim survives for anyone
// auditing why the ticket came back.
func (c Correction) note() string {
	var b strings.Builder
	b.WriteString("The description asserted:\n\n")
	b.WriteString(blockquote(c.Old))
	if strings.TrimSpace(c.New) == "" {
		b.WriteString("\n\nThat wording is removed in the same action as this comment.")
	} else {
		b.WriteString("\n\nCorrected in the same action as this comment to:\n\n")
		b.WriteString(blockquote(c.New))
	}
	return b.String()
}

func blockquote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// Abandon hands one issue back to Backlog: the evidence as a comment first,
// then — in a single write — the description correction, the status move and
// the unassignment. The correction rides the same write as the demotion so
// the body stops asserting the false premise in the act that returns the
// ticket to the pool; a Backlog ticket still claiming the disproven premise
// would just be re-blessed on the strength of it.
//
// Never Canceled, Done or Duplicate: closing is a human's call however
// wrong the ticket turned out to be, and the guard enforces it. The same
// respect runs the other way — a ticket a human already closed refuses,
// because reopening a close is as much their call as making one.
func Abandon(ctx context.Context, cl Linear, cov covenant.Covenant, identifier, evidence string, corr *Correction) (linear.Issue, error) {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return linear.Issue{}, fmt.Errorf(
			"abandon needs the evidence: what you found and why it undoes the ticket's premise — a hand-back with no reasons on it reads as a bot giving up")
	}
	issue, err := cl.IssueByIdentifier(ctx, identifier)
	if err != nil {
		return linear.Issue{}, err
	}
	if err := refuseIfClosed(issue, "abandon"); err != nil {
		return linear.Issue{}, err
	}

	update := linear.IssueUpdate{Unassign: true}
	comment := evidence
	if corr != nil {
		// Compose the corrected body before any write: an anchor that does
		// not pin a unique location refuses here, with nothing yet changed.
		next, err := linear.WithReplacement(issue.Description, corr.Old, corr.New)
		if err != nil {
			return linear.Issue{}, err
		}
		update.Description = &next
		comment = evidence + "\n\n" + corr.note()
	}

	// Resolve the target before the comment: the comment promises the
	// correction lands "in the same action", so every failure that can be
	// checked without writing must come first — a comment followed by a
	// refusal would record a correction that never happened.
	stateID, err := resolveState(ctx, cl, cov, issue.TeamID, "backlog")
	if err != nil {
		return linear.Issue{}, err
	}
	update.StateID = stateID
	if err := cl.CreateComment(ctx, issue.ID, comment); err != nil {
		return linear.Issue{}, err
	}
	if err := cl.UpdateIssue(ctx, issue.ID, update); err != nil {
		return linear.Issue{}, err
	}
	return issue, nil
}

// FileRequest is what `wand file` needs: where, what, and whether the caller
// has already ruled the search hits out as duplicates.
type FileRequest struct {
	TeamKey     string
	Title       string
	Description string
	Force       bool // file even though the search found candidates
}

// FileResult reports a file attempt. Created is nil when the verb refused
// because Duplicates need ruling out first.
type FileResult struct {
	Created    *linear.Issue
	Duplicates []linear.Issue
}

// File searches the team for near-duplicates of the title, then files the
// issue into Triage with the agent-filed label, no priority and no assignee.
// When the search returns candidates and Force is not set, nothing is filed:
// the caller reads the candidates and either comments on the existing issue
// or re-runs with Force. Search-first is the rule because a duplicate filed
// is a duplicate a human must later notice, judge and close — the cheap
// moment to catch it is before it exists.
func File(ctx context.Context, cl Linear, cov covenant.Covenant, req FileRequest) (FileResult, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return FileResult{}, fmt.Errorf("file needs a title")
	}
	team, err := cl.TeamByKey(ctx, req.TeamKey)
	if err != nil {
		return FileResult{}, err
	}
	if team.ID == "" {
		return FileResult{}, fmt.Errorf("no team with key %q", req.TeamKey)
	}

	duplicates, err := cl.SearchIssues(ctx, req.TeamKey, title)
	if err != nil {
		return FileResult{}, err
	}
	if len(duplicates) > 0 && !req.Force {
		return FileResult{Duplicates: duplicates}, nil
	}

	labelID, err := agentFiledLabelID(ctx, cl)
	if err != nil {
		return FileResult{}, err
	}
	stateID, err := resolveState(ctx, cl, cov, team.ID, "triage")
	if err != nil {
		return FileResult{}, err
	}
	created, err := cl.CreateIssue(ctx, linear.IssueCreate{
		TeamID:      team.ID,
		Title:       title,
		Description: req.Description,
		StateID:     stateID,
		LabelIDs:    []string{labelID},
	})
	if err != nil {
		return FileResult{}, err
	}
	return FileResult{Created: &created, Duplicates: duplicates}, nil
}

func agentFiledLabelID(ctx context.Context, cl Linear) (string, error) {
	label, found, err := cl.LabelByName(ctx, AgentFiledLabel)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf(
			"no %q label anywhere in the workspace; run `wand init` to bring the team to the covenant", AgentFiledLabel)
	}
	return label.ID, nil
}
