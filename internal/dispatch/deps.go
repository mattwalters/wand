package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/plan"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/worker"
)

// Board is the slice of the Linear client dispatch reads through: enough to
// read Todo and To Plan itself, plus everything run.Execute and
// plan.Execute need once a winner is chosen — the same client value is
// handed to both, and Go's structural typing is what lets one interface
// satisfy three.
type Board interface {
	run.Board
	plan.Board
	TeamIssuesByState(ctx context.Context, teamKey, stateName string) ([]linear.Issue, error)
}

// Runs is the slice of the journal store dispatch reads to count lane
// occupancy — the same narrow slice [home.Runs] uses, for the same
// reason: a walk of the store plus a liveness check per run, nothing else.
type Runs interface {
	List() ([]string, error)
	Inspect(id string) (journal.Report, error)
}

// Workers spawns one cold worker and collects its handoff. Both run.Workers
// and plan.Workers have this exact shape, so one value passed to each
// Deps satisfies both.
type Workers interface {
	Run(ctx context.Context, spec worker.Spec) (worker.Result, error)
}

// Deps is everything one dispatch pass acts through: a Board and Runs to
// read, plus every interface run.Execute and plan.Execute need to run the
// winner's loop — dispatch does not reimplement either orchestrator, it
// wires the same Deps they already take.
type Deps struct {
	Board Board
	Cov   covenant.Covenant
	Runs  Runs

	Git     run.Git
	Hub     run.Hub
	Shell   run.Shell
	Tree    plan.Tree
	Workers Workers

	TeamKey string
	Repo    string
	Harness string
	Model   string
	Effort  string

	Out io.Writer
}

func (d Deps) validate() error {
	switch {
	case d.Board == nil, d.Runs == nil, d.Git == nil, d.Hub == nil, d.Shell == nil, d.Tree == nil, d.Workers == nil:
		return errors.New("dispatch: Deps is missing an interface; this is a wand bug")
	case d.TeamKey == "":
		return errors.New("dispatch: Deps.TeamKey is required")
	case d.Repo == "":
		return errors.New("dispatch: Deps.Repo is required")
	case !filepath.IsAbs(d.Repo):
		return fmt.Errorf("dispatch: Deps.Repo must be absolute, got %q", d.Repo)
	case d.Cov.Caps.Lanes < 1:
		return errors.New("dispatch: the covenant's lane cap is unset; this is a wand bug — covenant.Default and the file loader both guarantee it")
	}
	return nil
}
