package doctor

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/mattwalters/wand/internal/shim"
)

// ShimPath is one harness shim wand init writes: where it lives, and the
// function that says whether a given byte slice already carries the guard
// hook it should.
type ShimPath struct {
	Path   string
	Ensure func([]byte) ([]byte, bool, error)
}

// RepoCheck is doctor's one filesystem-touching check, alongside its usual
// Linear diff: a shim that already matches what init would write, present
// on disk, but not tracked by git protects only the checkout it was
// generated in — untracked-but-matching is drift by the same definition
// Diagnose uses everywhere else, and this is the WND-86 shape itself, caught
// here so it does not take forty commits and a second incident to notice
// again.
//
// A shim that is missing, or present but stale (Ensure would still rewrite
// it), is not this check's concern — init already regenerates it every run,
// and doctor's Linear-side checks are not widened to cover it here.
//
// readFile and tracked are injected so this function needs neither a real
// checkout nor a git binary to test; cli/doctor.go supplies the real ones.
// A non-nil error means the check itself could not run — no git, or no
// repository at the given path — which must not be read as either drift or
// health.
func RepoCheck(paths []ShimPath, readFile func(string) ([]byte, error), tracked func(string) shim.TrackStatus) (findings []string, err error) {
	for _, p := range paths {
		content, rerr := readFile(p.Path)
		if errors.Is(rerr, fs.ErrNotExist) {
			continue
		}
		if rerr != nil {
			return nil, fmt.Errorf("%s: %w", p.Path, rerr)
		}
		if _, changed, eerr := p.Ensure(content); eerr != nil || changed {
			continue
		}

		switch tracked(p.Path) {
		case shim.StatusUntracked:
			findings = append(findings, fmt.Sprintf("%s carries the guard hook but is not tracked by git; commit it, or only this checkout is protected", p.Path))
		case shim.StatusUnknown:
			return nil, fmt.Errorf("could not determine whether %s is tracked by git", p.Path)
		}
	}
	return findings, nil
}
