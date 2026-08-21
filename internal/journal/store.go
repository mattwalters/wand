package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Store is a directory of runs. One directory per run:
//
//	<root>/runs/<id>/journal.jsonl   the append-only record stream
//	<root>/runs/<id>/lease.json      who holds it, replaced atomically
//	<root>/runs/<id>/lock            the liveness proof, never deleted
//	<root>/runs/<id>/scratch/        the worker's write grant
//
// The store lives outside every repository it records runs for, and Create
// refuses if it does not. That is the PW-175 lesson made structural: a
// worker's scratch output — a CI watch log, a debug dump — written inside
// the worktree leaves the tree dirty, and a dirty tree forces a park. Parks
// have to stay reserved for work genuinely at risk; lanes parked by noise
// train people to stop reading parks at all.
//
// The lock guards one *run*, not one ticket. Two runs against the same
// ticket are two directories and two locks, and nothing here stops them —
// deciding that a ticket already has a run is normally the dispatcher's
// job, made on the board, before a run exists. This layer's promise is
// narrower and absolute: one writer per run, and a dead one provably dead.
//
// An orchestrator whose ticket never changes status while it works has no
// board move to lose the race on, and asks for ticket-level exclusion
// explicitly with [Store.LockTicket] (ticket.go) — machine-local, held for
// the life of the process, released by the kernel when it dies:
//
//	<root>/tickets/<ticket>.lock    one plan run at a time, per ticket
type Store struct {
	// Root is the directory runs live under. Absolute, and outside every
	// repository the store records runs for.
	Root string

	// Now is the clock. Nil means time.Now — tests set it so run ids,
	// records and leases are reproducible.
	Now func() time.Time

	// Host identifies this machine in leases. Empty means the system
	// hostname. A lease from a different Host reads as Unknown liveness,
	// which is what a test needs to reach that branch.
	Host string
}

// New returns a store rooted at dir.
func New(dir string) *Store { return &Store{Root: dir} }

// Default returns a store at [DefaultRoot].
func Default() (*Store, error) {
	root, err := DefaultRoot()
	if err != nil {
		return nil, err
	}
	return New(root), nil
}

// DefaultRoot resolves where runs live: WAND_STATE_DIR when set, then the
// platform's state directory.
//
// State, not cache and not config: a journal that a cleaner may delete is
// not a journal, and a journal that syncs between machines would carry a
// lease naming a pid on the wrong host. On Unix that is
// $XDG_STATE_HOME/wand, falling back to ~/.local/state/wand — including on
// macOS, where a developer tool's state is where a developer expects to
// find it rather than under Application Support.
func DefaultRoot() (string, error) {
	if dir := os.Getenv("WAND_STATE_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("journal: WAND_STATE_DIR must be an absolute path, got %q", dir)
		}
		return dir, nil
	}
	if runtime.GOOS == "windows" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("journal: locating the state directory: %w", err)
		}
		return filepath.Join(dir, "wand"), nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "wand"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("journal: locating the state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "wand"), nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) host() string {
	if s.Host != "" {
		return s.Host
	}
	return hostname()
}

// runsDir is the parent of every run directory.
func (s *Store) runsDir() string { return filepath.Join(s.Root, "runs") }

// Dir returns the directory of one run. It does not check that the run
// exists.
func (s *Store) Dir(id string) string { return filepath.Join(s.runsDir(), id) }

func (s *Store) check() error {
	if s.Root == "" {
		return errors.New("journal: Store.Root is required")
	}
	if !filepath.IsAbs(s.Root) {
		return fmt.Errorf("journal: Store.Root must be absolute, got %q — a relative root resolves against whichever process reads it", s.Root)
	}
	return nil
}

// Create opens a new run: its directory, its scratch directory, the lock
// that proves it alive, its opening record and its lease — in that order,
// so that nothing claims to hold a run before it can prove it.
//
// The returned Run holds the lock until Close. Pair it immediately:
//
//	r, err := store.Create(meta)
//	if err != nil { return err }
//	defer r.Close()
//
// Close is what makes "exactly one terminal record" true even when the
// process unwinds by a path nobody planned.
func (s *Store) Create(m Meta) (*Run, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	// The scratch directory is handed to a worker as its write grant. If
	// the store sits inside the repository, that grant points into the
	// worktree, and every run ends by parking on a tree the run itself
	// dirtied.
	if s.Root == m.Repo || within(m.Repo, s.Root) {
		return nil, fmt.Errorf(
			"journal: the store root %s is inside the repository %s; scratch must live outside the worktree, or every run parks on a tree it dirtied itself",
			s.Root, m.Repo)
	}
	if err := os.MkdirAll(s.runsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("journal: creating the run directory: %w", err)
	}

	now := s.now()
	id, dir, err := s.mkdir(m.Ticket, now)
	if err != nil {
		return nil, err
	}
	m.ID = id

	r, err := s.open(dir, m, now)
	if err != nil {
		// A half-built run is worse than none: it would list, replay as
		// an error, and pull a human into diagnosing a directory that
		// never held a process.
		os.RemoveAll(dir)
		return nil, err
	}
	if err := r.append(Record{Kind: KindStarted, Run: &m}); err != nil {
		r.release()
		os.RemoveAll(dir)
		return nil, err
	}
	if err := r.renew(); err != nil {
		r.release()
		os.RemoveAll(dir)
		return nil, err
	}
	return r, nil
}

// Reopen picks a run back up after the process holding it died. It is the
// engine behind resume, and it refuses in exactly two cases: a run that
// already recorded a terminal state (finished runs are read, not re-run),
// and a run whose lock is still held (single writer — the reason the lock
// exists).
//
// The lock is taken before the journal is read, and that order is
// load-bearing. Reading first would sample a stream the previous holder
// could still be appending to; if it then died, the truncation below would
// be computed from a stale offset and would cut away a record that had been
// written legitimately in between. Nothing may be concluded about a run
// until nothing else can be writing it.
func (s *Store) Reopen(id string) (*Run, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	dir := s.Dir(id)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("journal: no run %q under %s", id, s.runsDir())
	}
	// Take the lock first, on the strength of the id alone: a held lock
	// means a live holder, and the second writer is the one that must not
	// exist. The rest of the metadata comes out of the journal below.
	r, err := s.open(dir, Meta{ID: id}, s.now())
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Run, error) {
		r.release()
		return nil, err
	}

	recs, valid, err := readJournal(filepath.Join(dir, journalFile))
	if err != nil {
		return fail(err)
	}
	state, err := Replay(recs)
	if err != nil {
		return fail(err)
	}
	if ok, why := state.Resumable(); !ok {
		return fail(errors.New("journal: " + why))
	}
	prior, err := readLease(dir)
	if err != nil {
		return fail(err)
	}
	r.adopt(state)

	// A crash mid-append leaves a partial final line. Dropping it on read
	// is not enough once we are about to append: the next record would
	// land behind the fragment, and the fragment would then sit in the
	// middle of the stream, where it is loss rather than interruption.
	if err := os.Truncate(filepath.Join(dir, journalFile), valid); err != nil {
		return fail(fmt.Errorf("journal: truncating the torn tail of %s: %w", id, err))
	}
	if err := r.append(Record{Kind: KindResumed, Reason: resumeReason(prior, state)}); err != nil {
		return fail(err)
	}
	if err := r.renew(); err != nil {
		return fail(err)
	}
	return r, nil
}

// resumeReason states what was interrupted, in the record itself, so the
// journal explains its own gap to whoever reads it next.
func resumeReason(prior Lease, state State) string {
	var b strings.Builder
	b.WriteString("resumed")
	if prior.Held() {
		fmt.Fprintf(&b, "; previous holder was pid %d on %s", prior.PID, prior.Host)
	}
	switch {
	case state.Phase == "":
		b.WriteString("; no phase had started")
	case state.PhaseDone:
		fmt.Fprintf(&b, "; phase %q round %d had finished", state.Phase, state.Round)
	default:
		fmt.Fprintf(&b, "; phase %q round %d was left open and will be re-run", state.Phase, state.Round)
	}
	return b.String()
}

// mkdir claims a run directory, and the claim is the mkdir itself: two
// processes creating a run for the same ticket in the same second cannot
// both succeed, so neither ends up appending to the other's journal.
func (s *Store) mkdir(ticket string, now time.Time) (id, dir string, err error) {
	base := fmt.Sprintf("%s-%s", slug(ticket), now.UTC().Format("20060102T150405Z"))
	for n := 1; n <= 100; n++ {
		id = base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		dir = s.Dir(id)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}
		if !os.IsExist(err) {
			return "", "", fmt.Errorf("journal: creating the run directory: %w", err)
		}
	}
	return "", "", fmt.Errorf("journal: could not claim a run directory for %s under %s", ticket, s.runsDir())
}

// slug makes a ticket identifier safe as a directory name without making it
// unreadable — the id is something a human types into `wand resume`.
func slug(ticket string) string {
	var b strings.Builder
	for _, r := range ticket {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "run"
	}
	return out
}

// List returns the ids of every run in the store, oldest first — run ids
// begin with the ticket and end with a sortable timestamp, so within a
// ticket the order is chronological.
func (s *Store) List() ([]string, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.runsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Records returns one run's record stream, with a torn final line dropped.
func (s *Store) Records(id string) ([]Record, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	recs, _, err := readJournal(filepath.Join(s.Dir(id), journalFile))
	return recs, err
}

// State replays one run.
func (s *Store) State(id string) (State, error) {
	recs, err := s.Records(id)
	if err != nil {
		return State{}, err
	}
	return Replay(recs)
}

// Report is everything a sweeper needs about one run: what the journal
// says, who the lease names, and whether that holder is still running.
type Report struct {
	State State
	Lease Lease
	Live  Liveness
}

// Zombie reports a run whose journal says it is still going and whose
// holder is provably gone — the ticket sitting In Progress with nothing
// behind it.
//
// [Unknown] is deliberately not a zombie. A run on another machine is one
// this process cannot see, and sweeping what it cannot see is how a healthy
// run acquires a second writer.
func (r Report) Zombie() bool { return !r.State.Ended() && r.Live == Dead }

// Inspect reads one run whole: state, lease and liveness.
func (s *Store) Inspect(id string) (Report, error) {
	state, err := s.State(id)
	if err != nil {
		return Report{}, err
	}
	dir := s.Dir(id)
	lease, err := readLease(dir)
	if err != nil {
		return Report{}, err
	}
	live, err := liveness(dir, lease, s.host())
	if err != nil {
		// The lock could not be examined, which is exactly what Unknown
		// is for. The report still stands; only the verdict is withheld.
		return Report{State: state, Lease: lease, Live: Unknown}, nil
	}
	return Report{State: state, Lease: lease, Live: live}, nil
}

// readJournal reads a record stream, tolerating a torn final line and
// nothing else.
//
// A torn final line is a crash caught mid-append — the case this file
// format exists for — so it is dropped, and the offset of the last whole
// record is returned so a resume can truncate it away before appending. A
// line that will not parse anywhere else in the stream is loss, not
// interruption: silently skipping it could drop the record that says the
// run already ended, and a resumed run that already ended is two writers.
func readJournal(path string) ([]Record, int64, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, 0, fmt.Errorf("journal: no journal at %s", path)
	}
	if err != nil {
		return nil, 0, err
	}
	var (
		recs   []Record
		offset int64
	)
	for len(raw) > 0 {
		i := bytes.IndexByte(raw, '\n')
		if i < 0 {
			// Unterminated tail: the write never finished.
			break
		}
		line, rest := raw[:i], raw[i+1:]
		if len(bytes.TrimSpace(line)) > 0 {
			var rec Record
			if err := json.Unmarshal(line, &rec); err != nil {
				return nil, 0, fmt.Errorf("journal: %s: record %d does not parse: %w", path, len(recs)+1, err)
			}
			recs = append(recs, rec)
		}
		offset += int64(i) + 1
		raw = rest
	}
	return recs, offset, nil
}

// within reports whether path lies strictly inside dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
