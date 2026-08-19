package journal_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// fixture returns a store with a frozen clock and a fixed host, plus a
// repository path outside it. Tests then break the one thing they are about.
func fixture(t *testing.T) (*journal.Store, journal.Meta) {
	t.Helper()
	s := journal.New(t.TempDir())
	s.Now = func() time.Time { return epoch }
	s.Host = "test-host"
	return s, journal.Meta{Ticket: "WND-7", Verb: "run", Repo: t.TempDir()}
}

func TestCreateLaysOutTheRun(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	if want := "WND-7-20260819T120000Z"; r.ID() != want {
		t.Errorf("ID = %q, want %q", r.ID(), want)
	}
	if fi, err := os.Stat(r.ScratchDir()); err != nil || !fi.IsDir() {
		t.Errorf("scratch directory is not there: %v", err)
	}
	// PW-175: the worker's write grant must not point into the worktree.
	if strings.HasPrefix(r.ScratchDir(), meta.Repo) {
		t.Errorf("scratch %s is inside the repository %s", r.ScratchDir(), meta.Repo)
	}

	state, err := s.State(r.ID())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Meta.ID != r.ID() || state.Meta.Ticket != "WND-7" || state.Meta.Verb != "run" {
		t.Errorf("opening record = %+v, want the run's metadata", state.Meta)
	}
	if state.Ended() {
		t.Error("a freshly created run already reports an ending")
	}
}

// The store root inside the repository would hand every worker a scratch
// directory inside the worktree — the PW-175 failure, where a debug dump
// dirties the tree and the run parks on noise it made itself.
func TestCreateRefusesAStoreInsideTheRepository(t *testing.T) {
	repo := t.TempDir()
	s := journal.New(filepath.Join(repo, ".wand", "state"))
	_, err := s.Create(journal.Meta{Ticket: "WND-7", Verb: "run", Repo: repo})
	if err == nil {
		t.Fatal("Create accepted a store rooted inside the repository")
	}
	if !strings.Contains(err.Error(), "outside the worktree") {
		t.Errorf("error %q does not explain the scratch rule", err)
	}
}

func TestCreateValidatesMeta(t *testing.T) {
	cases := map[string]func(*journal.Meta){
		"Ticket": func(m *journal.Meta) { m.Ticket = "" },
		"Verb":   func(m *journal.Meta) { m.Verb = "" },
		"Repo":   func(m *journal.Meta) { m.Repo = "" },
		// A relative repo path resolves against whichever process reads
		// the journal later, which is never the one that wrote it.
		"relative Repo": func(m *journal.Meta) { m.Repo = "some/repo" },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			s, meta := fixture(t)
			breakIt(&meta)
			if _, err := s.Create(meta); err == nil {
				t.Fatalf("Create accepted a Meta with a broken %s", name)
			}
		})
	}
}

// Two runs of one ticket in the same second must not share a directory:
// the second would append into the first's journal, and the replay of the
// interleaved stream would refuse — stranding both.
func TestCreateDoesNotCollide(t *testing.T) {
	s, meta := fixture(t)
	first, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer first.Close()
	second, err := s.Create(meta)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	defer second.Close()

	if first.ID() == second.ID() {
		t.Fatalf("both runs took the id %q", first.ID())
	}
	ids, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 || ids[0] != first.ID() || ids[1] != second.ID() {
		t.Errorf("List = %v, want the two runs oldest first", ids)
	}
}

func TestLeaseNamesTheHolderAndThePhase(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()
	if err := r.StartPhase("implement", 2); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}

	var lease journal.Lease
	raw, err := os.ReadFile(filepath.Join(s.Dir(r.ID()), "lease.json"))
	if err != nil {
		t.Fatalf("reading the lease: %v", err)
	}
	if err := json.Unmarshal(raw, &lease); err != nil {
		t.Fatalf("decoding the lease: %v", err)
	}
	if lease.RunID != r.ID() || lease.Ticket != "WND-7" {
		t.Errorf("lease = %+v, want the run's identity", lease)
	}
	if lease.Host != "test-host" || lease.PID != os.Getpid() {
		t.Errorf("lease holder = %s/%d, want test-host/%d", lease.Host, lease.PID, os.Getpid())
	}
	if lease.Phase != "implement" || lease.Round != 2 {
		t.Errorf("lease position = %q round %d, want implement round 2", lease.Phase, lease.Round)
	}
	if !lease.Held() {
		t.Error("Held = false for a lease with a pid on it")
	}
}

func TestInspectSeesItsOwnRunAsAlive(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rep, err := s.Inspect(r.ID())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// The lock lives on the open file description, so this process is as
	// much a stranger to its own held lock as another process would be.
	if rep.Live != journal.Alive {
		t.Errorf("Live = %s while the run is held, want alive", rep.Live)
	}
	if rep.Zombie() {
		t.Error("a live run reported as a zombie")
	}

	if err := r.Converged("shipped"); err != nil {
		t.Fatalf("Converged: %v", err)
	}
	rep, err = s.Inspect(r.ID())
	if err != nil {
		t.Fatalf("Inspect after ending: %v", err)
	}
	if rep.Live != journal.Dead {
		t.Errorf("Live = %s after the run released, want dead", rep.Live)
	}
	if rep.Lease.Held() {
		t.Error("a lease survived the run that held it")
	}
	if rep.Zombie() {
		t.Error("a run that recorded its ending reported as a zombie")
	}
}

// A lease from another machine is one this process cannot judge. Reporting
// it dead would let a sweeper act against a healthy run on another host,
// and two writers on one ticket is the failure nothing recovers from.
func TestInspectWithholdsAVerdictOnAnotherHost(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	leasePath := filepath.Join(s.Dir(r.ID()), "lease.json")
	raw, err := os.ReadFile(leasePath)
	if err != nil {
		t.Fatalf("reading the lease: %v", err)
	}
	var lease journal.Lease
	if err := json.Unmarshal(raw, &lease); err != nil {
		t.Fatalf("decoding the lease: %v", err)
	}
	lease.Host = "some-other-builder"
	rewrite(t, leasePath, lease)

	rep, err := s.Inspect(r.ID())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if rep.Live != journal.Unknown {
		t.Errorf("Live = %s for a lease from another host, want unknown", rep.Live)
	}
	if rep.Zombie() {
		t.Error("an unknown holder was reported as a zombie; only provable death may be swept")
	}
}

func rewrite(t *testing.T, path string, lease journal.Lease) {
	t.Helper()
	raw, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("encoding the lease: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the lease: %v", err)
	}
}

// A crash mid-append leaves a partial final line. That is the case this
// file format exists for, so reading drops it — and only it.
func TestRecordsDropATornFinalLine(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.StartPhase("implement", 1); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	// Simulate the crash: the process died with half a record written.
	path := filepath.Join(s.Dir(r.ID()), "journal.jsonl")
	appendRaw(t, path, `{"seq":3,"kind":"phase.en`)

	recs, err := s.Records(r.ID())
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("read %d records, want the 2 whole ones", len(recs))
	}
	if _, err := journal.Replay(recs); err != nil {
		t.Errorf("the surviving stream does not replay: %v", err)
	}
}

// A line that will not parse anywhere but the end is loss, not
// interruption. Skipping it could drop the record saying the run already
// ended, and resuming an ended run is two writers on one ticket.
func TestRecordsRefuseACorruptedLineMidStream(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()
	path := filepath.Join(s.Dir(r.ID()), "journal.jsonl")
	appendRaw(t, path, "{\"seq\":2,\"kind\":\"phase.st\n")
	appendRaw(t, path, "{\"seq\":3,\"kind\":\"note\"}\n")

	if _, err := s.Records(r.ID()); err == nil {
		t.Fatal("Records accepted a stream with a hole in the middle of it")
	}
}

func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("opening the journal: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("appending: %v", err)
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv("WAND_STATE_DIR", "/var/lib/wand")
	got, err := journal.DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	if got != "/var/lib/wand" {
		t.Errorf("DefaultRoot = %q, want the WAND_STATE_DIR override", got)
	}

	// A relative override would put the journal wherever the orchestrator
	// happened to be started from, which is not the same place twice.
	t.Setenv("WAND_STATE_DIR", "state")
	if _, err := journal.DefaultRoot(); err == nil {
		t.Error("DefaultRoot accepted a relative WAND_STATE_DIR")
	}
}

func TestStoreRootMustBeAbsolute(t *testing.T) {
	s := journal.New("runs")
	if _, err := s.Create(journal.Meta{Ticket: "WND-7", Verb: "run", Repo: t.TempDir()}); err == nil {
		t.Fatal("Create accepted a relative store root")
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List accepted a relative store root")
	}
}
