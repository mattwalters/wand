package pm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/pm"
)

// writeBoard is a fake WriteBoard, recording every write in the order it
// happened — the property this whole test file is for, the same way
// scope's loop_test.go pins its own write order.
type writeBoard struct {
	*readOnlyBoard

	team   linear.Team
	states []linear.WorkflowState
	label  linear.Label

	calls        []string
	nextIssueSeq int

	failCreateProject bool
	failCreateIssue   string // fails the ticket with this exact title
	failCreateBlocker bool
}

func newWriteBoard() *writeBoard {
	return &writeBoard{
		readOnlyBoard: &readOnlyBoard{},
		team:          linear.Team{ID: "team-1", Key: "WND"},
		states:        []linear.WorkflowState{{ID: "state-triage", Name: "Triage", Type: "triage"}},
		label:         linear.Label{ID: "label-agent-filed", Name: "agent-filed"},
	}
}

func (b *writeBoard) TeamByKey(ctx context.Context, key string) (linear.Team, error) {
	return b.team, nil
}

func (b *writeBoard) TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error) {
	return b.states, nil
}

func (b *writeBoard) LabelByName(ctx context.Context, name string) (linear.Label, bool, error) {
	return b.label, true, nil
}

func (b *writeBoard) CreateProject(ctx context.Context, teamID, name, description string) (linear.Project, error) {
	if b.failCreateProject {
		return linear.Project{}, fmt.Errorf("linear is down")
	}
	b.calls = append(b.calls, "project="+name)
	return linear.Project{ID: "proj-" + name, Name: name, Description: description}, nil
}

func (b *writeBoard) CreateIssue(ctx context.Context, in linear.IssueCreate) (linear.Issue, error) {
	if b.failCreateIssue != "" && in.Title == b.failCreateIssue {
		return linear.Issue{}, fmt.Errorf("linear is down")
	}
	b.nextIssueSeq++
	b.calls = append(b.calls, "issue="+in.Title)
	return linear.Issue{ID: "id-" + in.Title, Identifier: fmt.Sprintf("WND-%d", 100+b.nextIssueSeq), Title: in.Title}, nil
}

func (b *writeBoard) CreateBlocker(ctx context.Context, blockedIssueID, blockerIssueID string) error {
	if b.failCreateBlocker {
		return fmt.Errorf("linear is down")
	}
	b.calls = append(b.calls, fmt.Sprintf("blocker=%s<-%s", blockedIssueID, blockerIssueID))
	return nil
}

func blessHarness(t *testing.T) (*writeBoard, pm.BlessDeps, *journal.Store, *strings.Builder) {
	t.Helper()
	b := newWriteBoard()
	store := journal.New(t.TempDir())
	out := &strings.Builder{}
	deps := pm.BlessDeps{
		Board:   b,
		Cov:     covenant.Default(),
		TeamKey: "WND",
		Repo:    t.TempDir(),
		Out:     out,
	}
	return b, deps, store, out
}

func writeProposal(t *testing.T, d map[string]any) string {
	t.Helper()
	d["schema_version"] = pm.SchemaVersion
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "proposal.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// The write lands in the fixed order the batch discipline promises:
// projects first (their ids feed project resolution), then every ticket in
// blocked_by order, then every relation last, once every ticket exists.
func TestBlessWritesInOrderProjectsThenBlockersThenBlockedThenRelations(t *testing.T) {
	b, deps, store, _ := blessHarness(t)
	path := writeProposal(t, goodDraft())

	out, err := pm.Bless(context.Background(), deps, store, path)
	if err != nil {
		t.Fatalf("Bless: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if out.ExitCode() != pm.ExitFiled {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), pm.ExitFiled)
	}

	want := []string{
		"project=Onboarding",
		"issue=Add signup form validation",
		"issue=Wire signup form to the API",
		"blocker=id-Wire signup form to the API<-id-Add signup form validation",
	}
	if strings.Join(b.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", b.calls, want)
	}
	if len(out.Filed) != 2 || len(out.FiledProjects) != 1 {
		t.Errorf("Filed = %v, FiledProjects = %v", out.Filed, out.FiledProjects)
	}
}

// A ticket referencing an existing board project reuses it rather than
// creating a second project under the same name.
func TestBlessReusesAnExistingProjectByName(t *testing.T) {
	b, deps, store, _ := blessHarness(t)
	b.projects = []linear.Project{{ID: "existing-proj", Name: "Onboarding", Description: "already here"}}
	draft := goodDraft()
	draft["projects"] = []any{} // nothing new to create; both tickets still reference "Onboarding"
	path := writeProposal(t, draft)

	out, err := pm.Bless(context.Background(), deps, store, path)
	if err != nil {
		t.Fatalf("Bless: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s)", out.Kind, out.Reason)
	}
	for _, c := range b.calls {
		if strings.HasPrefix(c, "project=") {
			t.Errorf("a new project was created despite one existing: %v", b.calls)
		}
	}
	if len(out.FiledProjects) != 0 {
		t.Errorf("FiledProjects = %v, want none — the project already existed", out.FiledProjects)
	}
}

// A proposed project whose name happens to already exist on the board is
// reused rather than duplicated, and — the case that actually exercises the
// distinction between "on the board" and "created this run" — it must not
// be reported as filed, even though its name is right there in the
// proposal's own Projects.
func TestBlessDoesNotReportAReusedProjectAsFiled(t *testing.T) {
	b, deps, store, _ := blessHarness(t)
	b.projects = []linear.Project{{ID: "existing-proj", Name: "Onboarding", Description: "already here"}}
	path := writeProposal(t, goodDraft()) // still proposes "Onboarding"

	out, err := pm.Bless(context.Background(), deps, store, path)
	if err != nil {
		t.Fatalf("Bless: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s)", out.Kind, out.Reason)
	}
	for _, c := range b.calls {
		if strings.HasPrefix(c, "project=") {
			t.Errorf("a new project was created despite one existing: %v", b.calls)
		}
	}
	if len(out.FiledProjects) != 0 {
		t.Errorf("FiledProjects = %v, want none — Onboarding already existed and was reused", out.FiledProjects)
	}
}

// A failure partway through the batch stops rather than retries, and
// reports exactly what had already landed.
func TestBlessAMidBatchFailureReportsWhatAlreadyLanded(t *testing.T) {
	b, deps, store, _ := blessHarness(t)
	b.failCreateIssue = "Wire signup form to the API"
	path := writeProposal(t, goodDraft())

	out, err := pm.Bless(context.Background(), deps, store, path)
	if err != nil {
		t.Fatalf("Bless: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if out.ExitCode() != pm.ExitParked {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), pm.ExitParked)
	}
	if len(out.FiledProjects) != 1 {
		t.Errorf("FiledProjects = %v, want the one project that landed before the failure", out.FiledProjects)
	}
	if len(out.Filed) != 1 {
		t.Errorf("Filed = %v, want the one ticket that landed before the failure", out.Filed)
	}
	for _, c := range b.calls {
		if strings.Contains(c, "blocker=") {
			t.Errorf("a relation was wired despite one ticket failing to file: %v", b.calls)
		}
	}
	// No retry: exactly one attempt at the failing title.
	var attempts int
	for _, c := range b.calls {
		if c == "issue=Wire signup form to the API" {
			attempts++
		}
	}
	if attempts != 0 {
		t.Errorf("the failing create is recorded as succeeding: %v", b.calls)
	}
}

func TestBlessFailingAProjectStopsBeforeAnyTicket(t *testing.T) {
	b, deps, store, _ := blessHarness(t)
	b.failCreateProject = true
	path := writeProposal(t, goodDraft())

	out, err := pm.Bless(context.Background(), deps, store, path)
	if err != nil {
		t.Fatalf("Bless: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s, want parked", out.Kind)
	}
	if len(b.calls) != 0 {
		t.Fatalf("writes happened despite the project failing: %v", b.calls)
	}
}

func TestBlessFailingARelationStillReportsEveryFiledTicket(t *testing.T) {
	b, deps, store, _ := blessHarness(t)
	b.failCreateBlocker = true
	path := writeProposal(t, goodDraft())

	out, err := pm.Bless(context.Background(), deps, store, path)
	if err != nil {
		t.Fatalf("Bless: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s, want parked", out.Kind)
	}
	if len(out.Filed) != 2 {
		t.Errorf("Filed = %v, want both tickets — they landed before the relation failed", out.Filed)
	}
}

// The one rule the whole file is built around: an invalid or drifted
// proposal is refused before a journal run even opens, and nothing is
// written.
func TestBlessRefusesAnUnparsableFile(t *testing.T) {
	_, deps, store, _ := blessHarness(t)
	path := filepath.Join(t.TempDir(), "proposal.json")
	if err := os.WriteFile(path, []byte(`{"premise":"sound"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := pm.Bless(context.Background(), deps, store, path); err == nil {
		t.Fatal("expected a refusal for a file with no schema_version")
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Errorf("a refused bless left a run behind: %v", ids)
	}
}

func TestBlessRefusesAWrongPremiseProposal(t *testing.T) {
	_, deps, store, _ := blessHarness(t)
	path := writeProposal(t, map[string]any{"premise": "wrong", "reason": "already tracked"})

	if _, err := pm.Bless(context.Background(), deps, store, path); err == nil {
		t.Fatal("expected a refusal for a wrong-premise proposal")
	}
}

// A proposal naming a project that resolves to neither the board nor its
// own proposed set is refused before any write — the board-drift case the
// ticket's own re-check exists for.
func TestBlessRefusesAProjectRefThatResolvesToNothing(t *testing.T) {
	_, deps, store, _ := blessHarness(t)
	draft := goodDraft()
	draft["projects"] = []any{}
	tickets := draft["tickets"].([]any)
	tickets[0].(map[string]any)["project"] = "Onboarding"
	path := writeProposal(t, draft)

	if _, err := pm.Bless(context.Background(), deps, store, path); err == nil {
		t.Fatal("expected a refusal: Onboarding is neither proposed nor on the board")
	}
}

// A title that now collides with something already on the board — filed by
// someone or something else since the proposal was drafted — is a board
// drift the human blessing a stale file could not have weighed.
func TestBlessRefusesATitleThatNowCollides(t *testing.T) {
	b, deps, store, _ := blessHarness(t)
	b.triage = []linear.Issue{{Identifier: "WND-1", Title: "Add signup form validation"}}
	path := writeProposal(t, goodDraft())

	if _, err := pm.Bless(context.Background(), deps, store, path); err == nil {
		t.Fatal("expected a refusal for a title that now collides")
	}
}
