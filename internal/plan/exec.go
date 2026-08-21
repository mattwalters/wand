package plan

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mattwalters/wand/internal/worker"
)

// The production implementations of Tree and Workers: thin wrappers around
// git and the worker runner. Thin on purpose — the decisions live in the
// orchestrator, and these turn them into processes.

// ExecTree is Tree over the git binary.
type ExecTree struct{}

// Status returns the repository's uncommitted state, untracked files
// included. Porcelain v1 is the format to compare because it is documented
// as stable across git versions; the human-readable one is explicitly not.
//
// Untracked files count. The most likely thing a worker told to read leaves
// behind is a note-to-self or a scratch script, and a check that ignored
// them would miss exactly the case it is for.
func (ExecTree) Status(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain", "--untracked-files=all")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git status in %s: %w\n%s", dir, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// AdapterWorkers is Workers over one harness adapter.
type AdapterWorkers struct {
	Adapter worker.Adapter
}

func (a AdapterWorkers) Run(ctx context.Context, spec worker.Spec) (worker.Result, error) {
	return worker.Run(ctx, a.Adapter, spec)
}
