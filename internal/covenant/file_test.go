package covenant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// full exercises every section the file can carry.
const full = `
schema = 1

[statuses]
todo = "Ready"
needs_input = "Waiting on Human"

[caps]
review_rounds = 5
ci_attempts = 2
worker_timeout_minutes = 45

[estimates]
scale = "linear"

[toggles]
scope_interview = false
scope_critic = true

[commands]
verify = "make check"
provision = "make worktree"
run_agent = "claude -p"

[templates]
feature = "## What\n\n## Why"
`

func TestParseFullFile(t *testing.T) {
	f, err := Parse([]byte(full))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cov := f.Covenant()

	byKey := map[string]string{}
	for _, s := range cov.Statuses {
		byKey[s.Key] = s.Name
	}
	if byKey["todo"] != "Ready" {
		t.Errorf("todo renamed to %q, want %q", byKey["todo"], "Ready")
	}
	if byKey["needs_input"] != "Waiting on Human" {
		t.Errorf("needs_input renamed to %q, want %q", byKey["needs_input"], "Waiting on Human")
	}
	if byKey["in_progress"] != "In Progress" {
		t.Errorf("in_progress = %q, want the stock name kept", byKey["in_progress"])
	}

	want := Caps{ReviewRounds: 5, CIAttempts: 2, WorkerTimeout: 45 * time.Minute}
	if cov.Caps != want {
		t.Errorf("Caps = %+v, want %+v", cov.Caps, want)
	}
	if cov.IssueEstimationType != "linear" {
		t.Errorf("IssueEstimationType = %q, want %q", cov.IssueEstimationType, "linear")
	}
	if cov.Toggles.ScopeInterview {
		t.Error("ScopeInterview = true, want the file's false to override the default true")
	}
	if !cov.Toggles.ScopeCritic {
		t.Error("ScopeCritic = false, want the file's true to override the default false")
	}
	if cov.Commands.Verify != "make check" || cov.Commands.Provision != "make worktree" || cov.Commands.RunAgent != "claude -p" {
		t.Errorf("Commands = %+v", cov.Commands)
	}
	if cov.Templates["feature"] == "" {
		t.Error("template \"feature\" not carried")
	}
}

// A renamed status must carry its automations with it: a rule still pointing
// at the old name would target a state that no longer exists.
func TestRenameFollowsAutomations(t *testing.T) {
	f, err := Parse([]byte("schema = 1\n[statuses]\ndone = \"Shipped\"\nin_review = \"Reviewing\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cov := f.Covenant()
	events := map[string]string{}
	for _, a := range cov.Automations {
		events[a.Event] = a.Status
	}
	if events["merge"] != "Shipped" {
		t.Errorf("merge automation targets %q, want %q", events["merge"], "Shipped")
	}
	if events["review"] != "Reviewing" {
		t.Errorf("review automation targets %q, want %q", events["review"], "Reviewing")
	}
	if events["start"] != "In Progress" {
		t.Errorf("start automation targets %q, want the stock name kept", events["start"])
	}
}

// A file that sets nothing but the schema is the stock covenant exactly.
func TestMinimalFileIsDefault(t *testing.T) {
	f, err := Parse([]byte("schema = 1\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cov := f.Covenant()
	def := Default()
	if cov.Caps != def.Caps || cov.Toggles != def.Toggles || cov.Commands != def.Commands {
		t.Errorf("minimal file diverged from Default: %+v vs %+v", cov, def)
	}
	for i, s := range cov.Statuses {
		if s != def.Statuses[i] {
			t.Errorf("status %d = %+v, want %+v", i, s, def.Statuses[i])
		}
	}
}

// The refusals. Each invalid file must fail, and fail saying why: a
// misspelled key that silently defaulted is the failure mode the whole
// validator exists to stop.
func TestParseRefuses(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		wantErr string // substring the error must carry
	}{
		{"unknown top-level key", "schema = 1\ncolour = true\n", `"colour"`},
		{"misspelled cap", "schema = 1\n[caps]\nreveiw_rounds = 3\n", `"caps.reveiw_rounds"`},
		{"unknown status key", "schema = 1\n[statuses]\nblocked = \"Blocked\"\n", `"statuses.blocked"`},
		{"unknown section", "schema = 1\n[machine]\npath = \"x\"\n", `"machine.path"`},
		{"missing schema", "[caps]\nci_attempts = 1\n", "schema is required"},
		{"schema zero", "schema = 0\n", "not a covenant schema"},
		{"schema from the future", "schema = 2\n", "upgrade wand"},
		{"unknown estimate scale", "schema = 1\n[estimates]\nscale = \"points\"\n", "estimates.scale"},
		{"cap of zero", "schema = 1\n[caps]\nreview_rounds = 0\n", "caps.review_rounds"},
		{"negative cap", "schema = 1\n[caps]\nworker_timeout_minutes = -5\n", "caps.worker_timeout_minutes"},
		{"empty status name", "schema = 1\n[statuses]\ntodo = \"\"\n", "statuses.todo"},
		{"colliding status names", "schema = 1\n[statuses]\ntodo = \"Backlog\"\n", `share the name "Backlog"`},
		{"empty command", "schema = 1\n[commands]\nverify = \"\"\n", "commands.verify"},
		{"empty template", "schema = 1\n[templates]\nfeature = \"\"\n", "templates.feature"},
		{"wrong type", "schema = \"one\"\n", "schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file))
			if err == nil {
				t.Fatalf("Parse accepted %q", tc.file)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadMissingFileIsStock(t *testing.T) {
	cov, fromFile, err := Load(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if fromFile {
		t.Error("fromFile = true for a missing file")
	}
	if cov.Caps != Default().Caps || len(cov.Statuses) != len(Default().Statuses) {
		t.Error("missing file did not yield the stock covenant")
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	cov, fromFile, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !fromFile {
		t.Error("fromFile = false for a present file")
	}
	if cov.Caps.ReviewRounds != 5 {
		t.Errorf("ReviewRounds = %d, want 5", cov.Caps.ReviewRounds)
	}
}

// A present-but-broken file must be loud, never quietly the defaults, and
// the error must say which file so the fix starts in the right place.
func TestLoadBrokenFileIsLoud(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("schema = 1\n[caps]\nreveiw_rounds = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted a misspelled cap")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file", err)
	}
}
