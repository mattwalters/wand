package pm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/worker"
)

// Board is the slice of Linear the propose stage reads. It carries no write
// method at all — not "a write method the propose path happens not to
// call", an interface with none on it — so "no Linear writes in this path"
// is a property the compiler holds, not a promise the orchestrator keeps by
// discipline alone.
type Board interface {
	// Projects lists a team's existing projects, so the scout proposes a
	// new one only when nothing on the board already covers it.
	Projects(ctx context.Context, teamKey string) ([]linear.Project, error)
	// TeamIssuesByState lists a team's issues in one status, titles read for
	// the board summary — Triage and Backlog, per the ticket's own
	// cost/quality call.
	TeamIssuesByState(ctx context.Context, teamKey, stateName string) ([]linear.Issue, error)
	// SearchIssues finds near-duplicates of one proposed title, the same
	// call `wand file` makes before filing — here it only surfaces
	// candidates into the rendered proposal for a human to weigh, since
	// propose never files anything itself.
	SearchIssues(ctx context.Context, teamKey, term string) ([]linear.Issue, error)
}

// Workers spawns one cold worker and collects its handoff; the production
// implementation is worker.Run behind AdapterWorkers.
type Workers interface {
	Run(ctx context.Context, spec worker.Spec) (worker.Result, error)
}

// Deps is everything the propose stage acts through.
type Deps struct {
	Board   Board
	Cov     covenant.Covenant
	Workers Workers

	// TeamKey is the Linear team whose board the scout reads.
	TeamKey string
	// Repo is the absolute path of the repository the run's journal is
	// opened against — pm has no worktree, the same as plan, but the
	// journal still needs a repository to name.
	Repo string
	// Harness names the worker adapter, for the journal and the rendered
	// provenance.
	Harness string
	// Model and Effort pass through to the worker; empty means the
	// harness's default.
	Model  string
	Effort string

	// Out receives progress narration.
	Out io.Writer
}

func (d Deps) validate() error {
	switch {
	case d.Board == nil, d.Workers == nil:
		return errors.New("pm: Deps is missing an interface; this is a wand bug")
	case d.TeamKey == "":
		return errors.New("pm: Deps.TeamKey is required")
	case d.Repo == "":
		return errors.New("pm: Deps.Repo is required")
	case !filepath.IsAbs(d.Repo):
		return fmt.Errorf("pm: Deps.Repo must be absolute, got %q", d.Repo)
	case d.Cov.Caps.WorkerTimeout <= 0:
		return errors.New("pm: the covenant's worker timeout is unset; this is a wand bug — covenant.Default and the file loader both guarantee it")
	case d.Cov.StatusName("triage") == "":
		return errors.New("pm: the covenant has no triage status; this is a wand bug")
	}
	return nil
}

// AdapterWorkers is Workers over one harness adapter — the same thin
// wrapper plan.AdapterWorkers is, kept as its own copy rather than shared
// so pm does not import plan for a five-line type.
type AdapterWorkers struct {
	Adapter worker.Adapter
}

func (a AdapterWorkers) Run(ctx context.Context, spec worker.Spec) (worker.Result, error) {
	return worker.Run(ctx, a.Adapter, spec)
}
