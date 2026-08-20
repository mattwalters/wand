package doctor

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/bootstrap"
	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
)

// covenantTeam is a team that satisfies the stock covenant, the state a
// completed `wand init` leaves behind.
func covenantTeam() (linear.Team, bootstrap.Current) {
	team := linear.Team{ID: "team-1", Name: "Wand", Key: "WND", TriageEnabled: true, IssueEstimationType: "fibonacci"}
	var current bootstrap.Current
	for _, s := range covenant.Default().Statuses {
		current.States = append(current.States, linear.WorkflowState{ID: "st-" + s.Key, Name: s.Name, Type: s.Type, Position: s.Position})
	}
	for _, l := range covenant.Default().Labels {
		current.Labels = append(current.Labels, linear.Label{ID: "l-" + l.Name, Name: l.Name})
	}
	for _, a := range covenant.Default().Automations {
		current.Automations = append(current.Automations, automation("ga-"+a.Event, a.Event, a.Status))
	}
	return team, current
}

// schema1Team is what a team bootstrapped under WND-32's original covenant —
// before Scoped existed — looks like today: every status the pre-Scoped
// covenant created, Scoped absent. It mirrors bootstrap_test.go's
// existingSchema1Team, duplicated here because that fixture is unexported in
// another package's _test.go file.
func schema1Team() (linear.Team, bootstrap.Current) {
	team := linear.Team{ID: "team-1", Name: "Wand", Key: "WND", TriageEnabled: true, IssueEstimationType: "fibonacci"}
	var current bootstrap.Current
	for _, s := range []struct {
		name, typ string
		pos       float64
	}{
		{"Triage", "triage", 0}, {"Backlog", "backlog", 0},
		{"Scoping", "unstarted", -50}, {"Todo", "unstarted", 1},
		{"Needs Input", "unstarted", 50}, {"In Progress", "started", 2},
		{"In Review", "started", 3}, {"Done", "completed", 4},
		{"Canceled", "canceled", 5}, {"Duplicate", "duplicate", 6},
	} {
		current.States = append(current.States, linear.WorkflowState{ID: "st-" + s.name, Name: s.name, Type: s.typ, Position: s.pos})
	}
	for _, l := range []string{"human-only", "agent-filed", "ready-for-human", "re-review"} {
		current.Labels = append(current.Labels, linear.Label{ID: "l-" + l, Name: l})
	}
	for _, a := range []struct{ event, state string }{
		{"draft", "In Progress"}, {"start", "In Progress"},
		{"review", "In Review"}, {"merge", "Done"},
	} {
		current.Automations = append(current.Automations, automation("ga-"+a.event, a.event, a.state))
	}
	return team, current
}

func automation(id, event, state string) linear.GitAutomation {
	a := linear.GitAutomation{ID: id, Event: event}
	if state != "" {
		a.State = &struct {
			Name string `json:"name"`
		}{Name: state}
	}
	return a
}

func TestDiagnoseCleanTeam(t *testing.T) {
	team, current := covenantTeam()
	if findings := Diagnose(covenant.Default(), team, current); len(findings) != 0 {
		t.Errorf("a team at the covenant reports drift:\n%s", strings.Join(findings, "\n"))
	}
}

func TestDiagnoseMissingScopedStatus(t *testing.T) {
	// A team bootstrapped before Scoped existed (WND-32's original covenant)
	// predates WND-38's addition of Scoped to covenant.Default(). Diagnose
	// should surface that gap the same way it surfaces any other missing
	// status: as drift, with a remedy, not an accusation.
	team, current := schema1Team()
	findings := Diagnose(covenant.Default(), team, current)
	want := `status "Scoped" (unstarted) is missing`
	if len(findings) != 1 || findings[0] != want {
		t.Errorf("findings:\n got %s\nwant %s", strings.Join(findings, "\n     "), want)
	}
}

func TestDiagnoseToleratesStrangers(t *testing.T) {
	// Extra statuses outside the machine's path, extra labels, and
	// automations on events the covenant does not mention are invisible to
	// wand, not drift.
	team, current := covenantTeam()
	current.States = append(current.States, linear.WorkflowState{ID: "st-design", Name: "Design", Type: "unstarted", Position: -100})
	current.Labels = append(current.Labels, linear.Label{ID: "l-bug", Name: "Bug"})
	current.Automations = append(current.Automations, automation("ga-mergeable", "mergeable", "In Review"))

	if findings := Diagnose(covenant.Default(), team, current); len(findings) != 0 {
		t.Errorf("strangers reported as drift:\n%s", strings.Join(findings, "\n"))
	}
}

func TestDiagnoseDrift(t *testing.T) {
	team, clean := covenantTeam()

	tests := []struct {
		name   string
		mutate func(*linear.Team, *bootstrap.Current)
		want   string
	}{
		{
			name: "renamed status is missing under its covenant name",
			mutate: func(_ *linear.Team, c *bootstrap.Current) {
				for i := range c.States {
					if c.States[i].Name == "Todo" {
						c.States[i].Name = "Ready"
					}
				}
			},
			want: `status "Todo" (unstarted) is missing`,
		},
		{
			name: "missing label",
			mutate: func(_ *linear.Team, c *bootstrap.Current) {
				labels := c.Labels[:0]
				for _, l := range c.Labels {
					if l.Name != "human-only" {
						labels = append(labels, l)
					}
				}
				c.Labels = labels
			},
			want: `label "human-only" is missing`,
		},
		{
			name: "missing automation",
			mutate: func(_ *linear.Team, c *bootstrap.Current) {
				autos := c.Automations[:0]
				for _, a := range c.Automations {
					if a.Event != "draft" {
						autos = append(autos, a)
					}
				}
				c.Automations = autos
			},
			want: `PR automation on draft is missing; the covenant points it at "In Progress"`,
		},
		{
			name: "repointed automation",
			mutate: func(_ *linear.Team, c *bootstrap.Current) {
				for i := range c.Automations {
					if c.Automations[i].Event == "merge" {
						c.Automations[i] = automation(c.Automations[i].ID, "merge", "Canceled")
					}
				}
			},
			want: `PR automation on merge points at "Canceled"; the covenant points it at "Done"`,
		},
		{
			name: "automation pointing at nothing",
			mutate: func(_ *linear.Team, c *bootstrap.Current) {
				for i := range c.Automations {
					if c.Automations[i].Event == "merge" {
						c.Automations[i] = automation(c.Automations[i].ID, "merge", "")
					}
				}
			},
			want: `PR automation on merge points at nothing; the covenant points it at "Done"`,
		},
		{
			name: "covenant name over the wrong state type",
			mutate: func(_ *linear.Team, c *bootstrap.Current) {
				for i := range c.States {
					if c.States[i].Name == "Needs Input" {
						c.States[i].Type = "started"
					}
				}
			},
			want: `status "Needs Input" has type started; the covenant says unstarted`,
		},
		{
			name: "triage disabled",
			mutate: func(tm *linear.Team, _ *bootstrap.Current) {
				tm.TriageEnabled = false
			},
			want: `triage is disabled; the covenant enables it`,
		},
		{
			name: "estimate scale changed",
			mutate: func(tm *linear.Team, _ *bootstrap.Current) {
				tm.IssueEstimationType = "tShirt"
			},
			want: `issue estimation is "tShirt"; the covenant says "fibonacci"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := team
			current := bootstrap.Current{
				States:      append([]linear.WorkflowState(nil), clean.States...),
				Labels:      append([]linear.Label(nil), clean.Labels...),
				Automations: append([]linear.GitAutomation(nil), clean.Automations...),
			}
			tt.mutate(&tm, &current)

			findings := Diagnose(covenant.Default(), tm, current)
			if len(findings) != 1 || findings[0] != tt.want {
				t.Errorf("findings:\n got %s\nwant %s", strings.Join(findings, "\n     "), tt.want)
			}
		})
	}
}

func TestDiagnoseRenamedCovenant(t *testing.T) {
	// A covenant file renaming a status moves the requirement: the stock name
	// on the board no longer satisfies it, and automations follow the rename.
	file, err := covenant.Parse([]byte("schema = 1\n[statuses]\ntodo = \"Ready\"\nin_review = \"Reviewing\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	team, current := covenantTeam()

	findings := Diagnose(file.Covenant(), team, current)
	want := []string{
		`status "Ready" (unstarted) is missing`,
		`status "Reviewing" (started) is missing`,
		`PR automation on review points at "In Review"; the covenant points it at "Reviewing"`,
	}
	if strings.Join(findings, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n got %s\nwant %s", strings.Join(findings, "; "), strings.Join(want, "; "))
	}
}

// fakeClient serves canned team state, or fails at a chosen call.
type fakeClient struct {
	team    linear.Team
	current bootstrap.Current

	teamErr, readErr error
}

func (f *fakeClient) TeamByKey(_ context.Context, key string) (linear.Team, error) {
	if f.teamErr != nil {
		return linear.Team{}, f.teamErr
	}
	if !strings.EqualFold(key, f.team.Key) {
		return linear.Team{}, nil
	}
	return f.team, nil
}

func (f *fakeClient) TeamStates(context.Context, string) ([]linear.WorkflowState, error) {
	return f.current.States, f.readErr
}

func (f *fakeClient) Labels(context.Context) ([]linear.Label, error) {
	return f.current.Labels, f.readErr
}

func (f *fakeClient) GitAutomations(context.Context, string) ([]linear.GitAutomation, error) {
	return f.current.Automations, f.readErr
}

func TestRunExitContract(t *testing.T) {
	team, clean := covenantTeam()

	t.Run("clean team exits 0", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), &fakeClient{team: team, current: clean}, &out, &errOut, "WND", covenant.Default())
		if code != ExitClean {
			t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, ExitClean, errOut.String())
		}
		if !strings.Contains(out.String(), "team WND satisfies the covenant") {
			t.Errorf("output does not say the team is clean:\n%s", out.String())
		}
	})

	t.Run("drift exits 1", func(t *testing.T) {
		current := clean
		current.Labels = nil
		var out, errOut bytes.Buffer
		code := Run(context.Background(), &fakeClient{team: team, current: current}, &out, &errOut, "WND", covenant.Default())
		if code != ExitDrift {
			t.Fatalf("exit code = %d, want %d", code, ExitDrift)
		}
		if !strings.Contains(out.String(), `drift: label "human-only" is missing`) {
			t.Errorf("output does not report the drift:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "4 drifts from the covenant") {
			t.Errorf("output does not count the drift:\n%s", out.String())
		}
	})

	t.Run("team predating Scoped exits 1", func(t *testing.T) {
		// A team bootstrapped before WND-38 added Scoped to the covenant is
		// drift, not silence, and the report reads as a remedy: init adds
		// the missing status, nobody removed it.
		schemaTeam, schemaCurrent := schema1Team()
		var out, errOut bytes.Buffer
		code := Run(context.Background(), &fakeClient{team: schemaTeam, current: schemaCurrent}, &out, &errOut, "WND", covenant.Default())
		if code != ExitDrift {
			t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, ExitDrift, errOut.String())
		}
		if !strings.Contains(out.String(), `drift: status "Scoped" (unstarted) is missing`) {
			t.Errorf("output does not report the missing status:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "1 drift from the covenant") {
			t.Errorf("output does not count the drift:\n%s", out.String())
		}
	})

	t.Run("unknown team exits 2", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), &fakeClient{team: team, current: clean}, &out, &errOut, "NOPE", covenant.Default())
		if code != ExitError {
			t.Fatalf("exit code = %d, want %d", code, ExitError)
		}
		if !strings.Contains(errOut.String(), `no team has the key "NOPE"`) {
			t.Errorf("stderr does not explain itself:\n%s", errOut.String())
		}
	})

	t.Run("API failure exits 2, not 0", func(t *testing.T) {
		// Could-not-check must never masquerade as health.
		for name, cl := range map[string]*fakeClient{
			"team lookup fails": {team: team, teamErr: errors.New("boom")},
			"team read fails":   {team: team, current: clean, readErr: errors.New("boom")},
		} {
			var out, errOut bytes.Buffer
			if code := Run(context.Background(), cl, &out, &errOut, "WND", covenant.Default()); code != ExitError {
				t.Errorf("%s: exit code = %d, want %d", name, code, ExitError)
			}
		}
	})
}
