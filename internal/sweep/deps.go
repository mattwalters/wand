package sweep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/verbs"
)

// ReReviewLabel marks a converged ticket a human wants another cycle over.
// Covenant topology like wand's other fixed-semantic labels, not a
// parameter (see covenant.Default) — sweep is what watches for it.
const ReReviewLabel = "re-review"

// Board is the slice of the Linear client sweep reads and writes through.
// It extends the verbs' slice because a sweep hand-back is verbs.Handback
// itself — the comment-before-status ordering is reused, not re-derived.
type Board interface {
	verbs.Linear
	TeamIssuesByLabel(ctx context.Context, teamKey, label string) ([]linear.Issue, error)
	TeamIssuesByState(ctx context.Context, teamKey, stateName string) ([]linear.Issue, error)
}

// Hub is the narrow slice of the GitHub surface sweep needs: enough to find
// a ticket's PR and read its unresolved threads. run.ExecHub already
// implements this, and every other Hub method it declares.
type Hub interface {
	PRForBranch(ctx context.Context, dir, branch string) (run.PR, bool, error)
	UnresolvedThreads(ctx context.Context, dir string, number int) (int, error)
}

// Runs is the slice of the journal store sweep reads to find dead-lease
// runs and issues sitting In Progress with nothing behind them at all.
type Runs interface {
	List() ([]string, error)
	Inspect(id string) (journal.Report, error)
}

// Deps is everything one sweep pass acts through.
type Deps struct {
	Board Board
	Hub   Hub
	Runs  Runs
	Cov   covenant.Covenant

	TeamKey string
	// Repo is the absolute path of the repository sweep reads PRs for —
	// the checkout it is run from, the same as `wand scope`. There is no
	// worktree at this point in the lifecycle: a converged run already
	// removed its own.
	Repo string

	Out io.Writer
}

func (d Deps) validate() error {
	switch {
	case d.Board == nil, d.Hub == nil, d.Runs == nil:
		return errors.New("sweep: Deps is missing an interface; this is a wand bug")
	case d.TeamKey == "":
		return errors.New("sweep: Deps.TeamKey is required")
	case d.Repo == "":
		return errors.New("sweep: Deps.Repo is required")
	case !filepath.IsAbs(d.Repo):
		return fmt.Errorf("sweep: Deps.Repo must be absolute, got %q", d.Repo)
	}
	return nil
}
