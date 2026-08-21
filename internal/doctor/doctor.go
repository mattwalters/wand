// Package doctor diffs a live Linear team against the covenant and reports
// drift, without writing anything.
//
// bootstrap.Plan already is most of the diff — a non-empty plan is a drift
// report — so Diagnose is largely a reporting pass over it, plus the checks
// Plan cannot express: a status wearing a covenant name over the wrong state
// type, and the team-level settings (triage, the estimate scale).
//
// Tolerated strangers are part of the contract. Extra statuses outside the
// machine's path (a team's own "Design" column upstream of Backlog), extra
// labels, and automations on events the covenant does not mention are
// invisible to wand, not drift. Renamed or missing covenant statuses,
// repointed automations, and missing labels are drift.
//
// RepoCheck widens doctor past the Linear API for the one thing that lives
// only on disk: whether the harness shims wand init writes are committed.
// An untracked-but-matching shim protects only the checkout that generated
// it, which is drift by the same definition Diagnose uses for everything
// else — so Run folds RepoCheck's findings into the same exit-code
// contract, rather than leaving it a second, separate check.
package doctor

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mattwalters/wand/internal/bootstrap"
	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
)

// The exit-code contract, diff-style: silence is health, drift is a finding,
// and a check that could not run refuses to claim either.
const (
	ExitClean = 0 // the team satisfies the covenant
	ExitDrift = 1 // drift found
	ExitError = 2 // could not check
)

// Diagnose diffs one team against the covenant and returns the drift, one
// finding per line, in covenant order: statuses, labels, automations, team
// settings. An empty slice means the team satisfies the covenant.
func Diagnose(cov covenant.Covenant, team linear.Team, current bootstrap.Current) []string {
	var findings []string

	// The plan is the diff: every write init would make is drift doctor
	// reports. Automation actions are re-rendered against the observed rule
	// so the report says what the team does today, not just what it should.
	currentAutomation := map[string]linear.GitAutomation{}
	for _, a := range current.Automations {
		currentAutomation[a.Event] = a
	}
	for _, a := range bootstrap.Plan(cov, current) {
		switch a.Op {
		case bootstrap.CreateStatus:
			findings = append(findings, fmt.Sprintf("status %q (%s) is missing", a.Status.Name, a.Status.Type))
		case bootstrap.CreateLabel:
			findings = append(findings, fmt.Sprintf("label %q is missing", a.Label.Name))
		case bootstrap.CreateAutomation:
			findings = append(findings, fmt.Sprintf("PR automation on %s is missing; the covenant points it at %q", a.Event, a.StatusName))
		case bootstrap.UpdateAutomation:
			at := "nothing"
			if got, ok := currentAutomation[a.Event]; ok && got.State != nil {
				at = fmt.Sprintf("%q", got.State.Name)
			}
			findings = append(findings, fmt.Sprintf("PR automation on %s points at %s; the covenant points it at %q", a.Event, at, a.StatusName))
		}
	}

	// What Plan cannot express: a status can carry a covenant name over the
	// wrong state type — same column label, different machine.
	currentState := map[string]linear.WorkflowState{}
	for _, s := range current.States {
		currentState[strings.ToLower(s.Name)] = s
	}
	for _, want := range cov.Statuses {
		got, ok := currentState[strings.ToLower(want.Name)]
		if ok && got.Type != want.Type {
			findings = append(findings, fmt.Sprintf("status %q has type %s; the covenant says %s", got.Name, got.Type, want.Type))
		}
	}

	// Team-level settings, also outside Plan's vocabulary.
	if team.TriageEnabled != cov.TriageEnabled {
		if cov.TriageEnabled {
			findings = append(findings, "triage is disabled; the covenant enables it")
		} else {
			findings = append(findings, "triage is enabled; the covenant disables it")
		}
	}
	if team.IssueEstimationType != cov.IssueEstimationType {
		findings = append(findings, fmt.Sprintf("issue estimation is %q; the covenant says %q", team.IssueEstimationType, cov.IssueEstimationType))
	}

	return findings
}

// Client is the read-only slice of the Linear client doctor needs.
type Client interface {
	TeamByKey(ctx context.Context, key string) (linear.Team, error)
	bootstrap.Reader
}

// Run performs the whole check against a live team and returns the exit
// code, so the contract — clean, drift, could-not-check — is testable
// in-process; the cobra layer turns the return into the real exit. Findings
// go to out, failures to check go to errOut.
//
// repoFindings and repoErr are RepoCheck's result, computed by the caller
// (cli/doctor.go) and merged in here rather than in the cobra layer, so the
// precedence between the filesystem check and the Linear check is pinned by
// an in-process test rather than left to the compiled-binary e2e tier.
// repoErr takes precedence over any Linear-side outcome and is checked
// first: it is purely local and cheaper to diagnose than a Linear failure,
// the same "local step first" order init itself uses for installing the
// shim before ever calling the Linear API. A repo-check failure is reported
// as could-not-check, never as drift or as health, the same rule this
// function already applies to a Linear failure.
func Run(ctx context.Context, cl Client, out, errOut io.Writer, teamKey string, cov covenant.Covenant, repoFindings []string, repoErr error) int {
	if repoErr != nil {
		fmt.Fprintf(errOut, "could not check: %v\n", repoErr)
		return ExitError
	}

	team, err := cl.TeamByKey(ctx, teamKey)
	if err != nil {
		fmt.Fprintf(errOut, "could not check: %v\n", err)
		return ExitError
	}
	if team.ID == "" {
		fmt.Fprintf(errOut, "could not check: no team has the key %q\n", teamKey)
		return ExitError
	}
	current, err := bootstrap.Read(ctx, cl, team.ID)
	if err != nil {
		fmt.Fprintf(errOut, "could not check: %v\n", err)
		return ExitError
	}

	findings := Diagnose(cov, team, current)
	findings = append(findings, repoFindings...)
	if len(findings) == 0 {
		fmt.Fprintf(out, "team %s satisfies the covenant\n", team.Key)
		return ExitClean
	}
	for _, f := range findings {
		fmt.Fprintf(out, "drift: %s\n", f)
	}
	plural := "s"
	if len(findings) == 1 {
		plural = ""
	}
	fmt.Fprintf(out, "%d drift%s from the covenant\n", len(findings), plural)
	return ExitDrift
}
