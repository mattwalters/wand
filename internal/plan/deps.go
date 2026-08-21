package plan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/verbs"
	"github.com/mattwalters/wand/internal/worker"
)

// Board is the slice of the Linear client the orchestrator writes through.
// It extends the verbs' slice because the premise hand-back is
// verbs.Handback itself — the comment-before-status ordering is reused, not
// re-derived here.
type Board interface {
	verbs.Linear
	IssueComments(ctx context.Context, issueID string) ([]linear.Comment, error)
	// AddLabel marks the ticket. The scout's own writes never need it —
	// it is here so a parked plan run can be reported with verbs.ReportPark,
	// which labels rather than moving a status the scout does not own.
	AddLabel(ctx context.Context, issueID, labelID string) error
	// UpsertSection writes one marker-fenced region of the description,
	// leaving every byte outside it alone.
	UpsertSection(ctx context.Context, issueID, description, id, markdown string) (next string, changed bool, err error)
}

// Workers spawns one cold worker and collects its handoff; the production
// implementation is worker.Run behind an adapter.
type Workers interface {
	Run(ctx context.Context, spec worker.Spec) (worker.Result, error)
}

// Tree reports what is uncommitted in a directory, which is how the
// read-only promise is checked rather than trusted. Behind an interface for
// the same reason everything else here is: the decisions are testable
// without a repository.
type Tree interface {
	// Status returns the repository's uncommitted state as a stable
	// string — the same tree twice gives the same answer, a changed tree a
	// different one.
	Status(ctx context.Context, dir string) (string, error)
}

// Deps is everything the plan run acts through.
type Deps struct {
	Board   Board
	Cov     covenant.Covenant
	Workers Workers
	Tree    Tree

	// Repo is the absolute path of the repository the scout reads. There
	// is no worktree in a plan run: the scout reads the checkout the
	// command was run from, which is why it may not write to it.
	Repo string
	// Harness names the worker adapter, for the journal and the
	// provenance footer.
	Harness string
	// Model and Effort pass through to every worker; empty means the
	// harness's default.
	Model  string
	Effort string

	// Interactive runs the draft-then-grill interview between validation
	// and the first Linear write. It needs a human on the other end of
	// In, and the caller is the one that knows whether there is one.
	Interactive bool
	// In is where the interview's answers come from; required when
	// Interactive.
	In io.Reader

	// Out receives progress narration and the interview itself.
	Out io.Writer
}

func (d Deps) validate() error {
	switch {
	case d.Board == nil, d.Workers == nil, d.Tree == nil:
		return errors.New("plan: Deps is missing an interface; this is a wand bug")
	case d.Repo == "":
		return errors.New("plan: Deps.Repo is required")
	case !filepath.IsAbs(d.Repo):
		return fmt.Errorf("plan: Deps.Repo must be absolute, got %q", d.Repo)
	case d.Cov.Caps.WorkerTimeout <= 0:
		return errors.New("plan: the covenant's worker timeout is unset; this is a wand bug — covenant.Default and the file loader both guarantee it")
	case d.Cov.StatusName("to_plan") == "":
		return errors.New("plan: the covenant has no to_plan status; this is a wand bug")
	}
	if d.Interactive {
		// The toggle and the flag answer different questions: the covenant
		// says whether this repo's lifecycle has an interview at all, and
		// the flag says whether this invocation has a human to hold one
		// with. Passing the flag against a covenant that turned the stage
		// off is a contradiction worth refusing rather than resolving.
		if !d.Cov.Toggles.PlanInterview {
			return errors.New("plan: this repo's covenant turns the plan interview off (toggles.plan_interview); drop --interactive, or turn the stage back on in wand.toml")
		}
		if d.In == nil {
			return errors.New("plan: an interactive plan run needs somewhere to read answers from; this is a wand bug")
		}
	}
	return nil
}
