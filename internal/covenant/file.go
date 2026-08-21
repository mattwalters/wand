package covenant

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// FileName is the covenant file's name, at the root of the repo.
const FileName = "wand.toml"

// File is the parsed covenant file: a checked-in wand.toml that
// parameterizes the stock covenant. TOML, so the rationale for a value
// survives as a comment next to the value it justifies. Fields left unset in
// the file keep the stock covenant's values; Covenant() applies the set ones.
//
// The file carries parameters only — status *names* over fixed semantics,
// caps, the estimate scale, toggles, the three pluggable commands, ticket
// templates — never topology. The state graph is wand's opinion; the schema
// version field lets wand ship topology upgrades centrally, for everyone.
//
// The covenant/config split: the file must never contain a secret, a machine
// path, or a harness name. Those are machine config, not covenant. The test
// is whether two clones could legitimately differ — if they could, it is
// config and belongs elsewhere; if a difference means two different
// processes, it is covenant and belongs here.
type File struct {
	Schema    int               `toml:"schema"`
	Team      FileTeam          `toml:"team"`
	Statuses  FileStatuses      `toml:"statuses"`
	Caps      FileCaps          `toml:"caps"`
	Estimates FileEstimates     `toml:"estimates"`
	Toggles   FileToggles       `toml:"toggles"`
	Commands  FileCommands      `toml:"commands"`
	Templates map[string]string `toml:"templates"`
}

// FileTeam binds the repo to one Linear team. It is an identity, not a
// process parameter — two clones of the same repo cannot legitimately bind
// to different teams, so it rides its own section rather than living among
// statuses/caps/toggles.
type FileTeam struct {
	Key string `toml:"key"`
}

// FileStatuses maps display names over the fixed status semantics. The keys
// are the semantic identities (Status.Key); only the names are the user's.
type FileStatuses struct {
	Triage     string `toml:"triage"`
	Backlog    string `toml:"backlog"`
	ToPlan     string `toml:"to_plan"`
	InPlanning string `toml:"in_planning"`
	PlanReview string `toml:"plan_review"`
	Todo       string `toml:"todo"`
	NeedsInput string `toml:"needs_input"`
	InProgress string `toml:"in_progress"`
	InReview   string `toml:"in_review"`
	Done       string `toml:"done"`
	Canceled   string `toml:"canceled"`
	Duplicate  string `toml:"duplicate"`
}

// overrides returns the set display names by status key.
func (s FileStatuses) overrides() map[string]string {
	all := map[string]string{
		"triage":      s.Triage,
		"backlog":     s.Backlog,
		"to_plan":     s.ToPlan,
		"in_planning": s.InPlanning,
		"plan_review": s.PlanReview,
		"todo":        s.Todo,
		"needs_input": s.NeedsInput,
		"in_progress": s.InProgress,
		"in_review":   s.InReview,
		"done":        s.Done,
		"canceled":    s.Canceled,
		"duplicate":   s.Duplicate,
	}
	for key, name := range all {
		if name == "" {
			delete(all, key)
		}
	}
	return all
}

// FileCaps are the run-loop limits. Zero means unset; an explicit zero or a
// negative is refused, because a cap of nothing is a request to loop forever.
//
// WorkerRetries is the one exception, and a pointer for the same reason
// FileToggles are: it bounds *extra* attempts on top of the one spawn every
// phase gets, so zero is not "loop forever" but "never retry" — a coherent
// answer, and the behavior wand had before retries existed. A pointer is
// what keeps that answer distinguishable from not mentioning the key.
//
// PhaseTimeoutMinutes is a map rather than a struct because unlike every
// other cap it is keyed by a name, not a fixed field — and because a map
// absorbs any key at decode time, Parse's strict-unknown-key check
// (Undecoded) cannot see a misspelled phase inside it; validateCaps checks
// its keys against knownPhases explicitly for that reason.
type FileCaps struct {
	ReviewRounds         int            `toml:"review_rounds"`
	CIAttempts           int            `toml:"ci_attempts"`
	WorkerTimeoutMinutes int            `toml:"worker_timeout_minutes"`
	PhaseTimeoutMinutes  map[string]int `toml:"phase_timeout_minutes"`
	Lanes                int            `toml:"lanes"`
	WorkerRetries        *int           `toml:"worker_retries"`
}

// knownPhases are the phase names caps.phase_timeout_minutes may key: the
// union of internal/run's phases (implement, fix-ci, review, revise) and
// internal/scope's (scout, critic, revise). One set rather than two, since
// scope's own reuse of a "revise" override costs nothing and a wrong phase
// name is refused the same way regardless of which package would read it.
var knownPhases = map[string]bool{
	"implement": true,
	"fix-ci":    true,
	"review":    true,
	"revise":    true,
	"scout":     true,
	"critic":    true,
}

// FileEstimates carries the estimate scale, in Linear's own vocabulary.
type FileEstimates struct {
	Scale string `toml:"scale"`
}

// FileToggles switch optional lifecycle stages. Pointers, because "off" and
// "not mentioned" are different answers.
type FileToggles struct {
	PlanInterview *bool `toml:"plan_interview"`
	PlanCritic    *bool `toml:"plan_critic"`
}

// FileCommands are the three pluggable commands.
type FileCommands struct {
	Verify    string `toml:"verify"`
	Provision string `toml:"provision"`
	RunAgent  string `toml:"run_agent"`
}

// Parse decodes and validates a covenant file. It refuses unknown keys
// loudly: a misspelled key silently defaulting is the failure mode.
func Parse(data []byte) (File, error) {
	var f File
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return File{}, err
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = fmt.Sprintf("%q", k.String())
		}
		return File{}, fmt.Errorf("unknown key %s — every key is checked so a misspelling cannot silently default", strings.Join(keys, ", "))
	}

	if !md.IsDefined("schema") {
		return File{}, fmt.Errorf("schema is required (this wand speaks schema %d)", SchemaVersion)
	}
	if f.Schema < 1 {
		return File{}, fmt.Errorf("schema %d is not a covenant schema; the first is 1", f.Schema)
	}
	// An older schema is read as-is rather than refused or upgraded in
	// place: schema bumps only add optional keys, so an older file means
	// exactly what it says under the current number. It still gets the
	// current *topology* — that is central, not the file's (see
	// SchemaVersion) — which is what surfaces an unmigrated team as drift
	// instead of hiding it behind a version number.
	if f.Schema > SchemaVersion {
		return File{}, fmt.Errorf("schema %d is newer than this wand speaks (%d); upgrade wand", f.Schema, SchemaVersion)
	}

	if err := validateTeam(md, f.Team); err != nil {
		return File{}, err
	}
	if err := validateStatuses(md, f.Statuses); err != nil {
		return File{}, err
	}
	if err := validateCaps(md, f.Caps); err != nil {
		return File{}, err
	}
	if _, known := estimateScales[f.Estimates.Scale]; md.IsDefined("estimates", "scale") && !known {
		return File{}, fmt.Errorf("estimates.scale %q is not one Linear speaks (notUsed, exponential, fibonacci, linear, tShirt)", f.Estimates.Scale)
	}
	if err := validateCommands(md, f.Commands); err != nil {
		return File{}, err
	}
	for name, body := range f.Templates {
		if strings.TrimSpace(body) == "" {
			return File{}, fmt.Errorf("templates.%s is empty; delete the key or give it a body", name)
		}
	}
	return f, nil
}

// teamKeyPattern is a Linear team key's shape: uppercase letters and digits,
// starting with a letter. Not the last word on what Linear will accept —
// just enough to catch a pasted issue identifier or a lowercase typo before
// it reaches the API as a silent no-such-team.
var teamKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,9}$`)

func validateTeam(md toml.MetaData, t FileTeam) error {
	if !md.IsDefined("team", "key") {
		return nil
	}
	if strings.TrimSpace(t.Key) == "" {
		return fmt.Errorf("team.key is empty; delete the key or set it, e.g. team.key = \"WND\"")
	}
	if !teamKeyPattern.MatchString(t.Key) {
		return fmt.Errorf("team.key %q does not look like a Linear team key (uppercase letters and digits, e.g. \"WND\")", t.Key)
	}
	return nil
}

func validateStatuses(md toml.MetaData, s FileStatuses) error {
	// An explicitly empty name is refused rather than treated as unset:
	// whatever the author meant, it was not "quietly keep the default".
	for _, key := range []string{"triage", "backlog", "to_plan", "in_planning", "plan_review", "todo", "needs_input", "in_progress", "in_review", "done", "canceled", "duplicate"} {
		if md.IsDefined("statuses", key) && strings.TrimSpace(s.overrides()[key]) == "" {
			return fmt.Errorf("statuses.%s is empty; delete the key to keep the stock name", key)
		}
	}
	// Two semantics sharing one display name would collapse two states into
	// one on the board. Linear matches names case-insensitively, so we do too.
	byName := map[string]string{}
	for _, st := range fileCovenant(s).Statuses {
		name := strings.ToLower(st.Name)
		if other, taken := byName[name]; taken {
			return fmt.Errorf("statuses %s and %s would share the name %q; two semantics cannot share one state", other, st.Key, st.Name)
		}
		byName[name] = st.Key
	}
	return nil
}

func validateCaps(md toml.MetaData, c FileCaps) error {
	for key, v := range map[string]int{
		"review_rounds":          c.ReviewRounds,
		"ci_attempts":            c.CIAttempts,
		"worker_timeout_minutes": c.WorkerTimeoutMinutes,
		"lanes":                  c.Lanes,
	} {
		if md.IsDefined("caps", key) && v < 1 {
			return fmt.Errorf("caps.%s must be at least 1, got %d — a cap of nothing is a request to loop forever", key, v)
		}
	}
	// Not folded into the loop above: this cap counts retries rather than
	// attempts, so its floor is zero — "never retry" — and only a negative
	// is meaningless.
	if c.WorkerRetries != nil && *c.WorkerRetries < 0 {
		return fmt.Errorf("caps.worker_retries must be at least 0, got %d — 0 means never retry", *c.WorkerRetries)
	}
	// A map absorbs any key at decode time, so the misspelled-key check
	// above never sees these; each one gets checked against knownPhases
	// here instead, same rule as every other unknown key in the file.
	for phase, minutes := range c.PhaseTimeoutMinutes {
		if !knownPhases[phase] {
			return fmt.Errorf("caps.phase_timeout_minutes has an unknown phase %q — known phases are critic, fix-ci, implement, review, revise, scout", phase)
		}
		if minutes < 1 {
			return fmt.Errorf("caps.phase_timeout_minutes.%s must be at least 1, got %d — a cap of nothing is a request to loop forever", phase, minutes)
		}
	}
	return nil
}

func validateCommands(md toml.MetaData, c FileCommands) error {
	for key, v := range map[string]string{
		"verify":    c.Verify,
		"provision": c.Provision,
		"run_agent": c.RunAgent,
	} {
		if md.IsDefined("commands", key) && strings.TrimSpace(v) == "" {
			return fmt.Errorf("commands.%s is empty; delete the key to leave it unconfigured", key)
		}
	}
	return nil
}

// fileCovenant applies status renames alone — enough for duplicate-name
// validation, which runs before the rest of the file is known to be valid.
func fileCovenant(s FileStatuses) Covenant {
	cov := Default()
	names := s.overrides()
	rename := map[string]string{}
	for i, st := range cov.Statuses {
		if name, ok := names[st.Key]; ok {
			rename[st.Name] = name
			cov.Statuses[i].Name = name
		}
	}
	// Automations point at statuses by display name; follow the renames.
	for i, a := range cov.Automations {
		if name, ok := rename[a.Status]; ok {
			cov.Automations[i].Status = name
		}
	}
	return cov
}

// Covenant is the stock covenant parameterized by the file: renamed statuses
// (with automations following), and every set cap, toggle, command and
// template applied over the defaults.
func (f File) Covenant() Covenant {
	cov := fileCovenant(f.Statuses)
	if f.Estimates.Scale != "" {
		cov.IssueEstimationType = f.Estimates.Scale
	}
	if f.Caps.ReviewRounds > 0 {
		cov.Caps.ReviewRounds = f.Caps.ReviewRounds
	}
	if f.Caps.CIAttempts > 0 {
		cov.Caps.CIAttempts = f.Caps.CIAttempts
	}
	if f.Caps.WorkerTimeoutMinutes > 0 {
		cov.Caps.WorkerTimeout = time.Duration(f.Caps.WorkerTimeoutMinutes) * time.Minute
	}
	for phase, minutes := range f.Caps.PhaseTimeoutMinutes {
		if cov.Caps.PhaseTimeouts == nil {
			cov.Caps.PhaseTimeouts = make(map[string]time.Duration, len(f.Caps.PhaseTimeoutMinutes))
		}
		cov.Caps.PhaseTimeouts[phase] = time.Duration(minutes) * time.Minute
	}
	if f.Caps.Lanes > 0 {
		cov.Caps.Lanes = f.Caps.Lanes
	}
	if f.Caps.WorkerRetries != nil {
		cov.Caps.WorkerRetries = *f.Caps.WorkerRetries
	}
	if f.Toggles.PlanInterview != nil {
		cov.Toggles.PlanInterview = *f.Toggles.PlanInterview
	}
	if f.Toggles.PlanCritic != nil {
		cov.Toggles.PlanCritic = *f.Toggles.PlanCritic
	}
	if f.Commands.Verify != "" {
		cov.Commands.Verify = f.Commands.Verify
	}
	if f.Commands.Provision != "" {
		cov.Commands.Provision = f.Commands.Provision
	}
	if f.Commands.RunAgent != "" {
		cov.Commands.RunAgent = f.Commands.RunAgent
	}
	if len(f.Templates) > 0 {
		cov.Templates = make(map[string]string, len(f.Templates))
		for name, body := range f.Templates {
			cov.Templates[name] = body
		}
	}
	return cov
}

// Load reads the covenant file at path and returns the covenant it
// parameterizes, plus the team key it binds the repo to (empty when the file
// sets none). The team key rides alongside the Covenant rather than inside
// it: it is an identity, not something doctor should diff against live
// Linear state. A missing file is not an error — the stock covenant
// applies, and fromFile reports which happened. Any other failure is loud:
// a present-but-broken file must never quietly become the defaults.
func Load(path string) (cov Covenant, teamKey string, fromFile bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), "", false, nil
	}
	if err != nil {
		return Covenant{}, "", false, err
	}
	f, err := Parse(data)
	if err != nil {
		return Covenant{}, "", false, fmt.Errorf("%s: %w", path, err)
	}
	return f.Covenant(), f.Team.Key, true, nil
}
