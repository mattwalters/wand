package dispatch

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The selector's own lock: one repository, one dispatch process at a time.
//
// A single-shot pass holds it for the whole invocation — selection through
// the loop it runs — and `--watch` holds it for the life of the poll loop,
// releasing it only on exit. Concurrent lanes come from `--watch` spawning
// detached children (watch.go), never from two dispatch processes racing
// each other's selection; the lock is what makes that a refusal instead of
// two winners claimed off the same read of Todo.
//
// mkdir, not flock: the run and ticket locks (internal/journal) use flock
// because the kernel drops it however the holder dies, and that is exactly
// what turns "is it still running?" into a fact instead of a heartbeat.
// flock's own reach is not the problem here — this package could use it —
// but flock has no shell-visible trace and nothing a person can `ls`: a
// directory holding a small pid file is inspectable and removable by hand
// the way a scheduler's own idiom (`flock(1)`, wrapping a cron job) is, and
// that command does not ship on macOS. mkdir is atomic the same way
// [journal.Store.mkdir] uses it to claim a run directory: two processes
// racing the same Mkdir cannot both succeed. What mkdir does not give back
// for free is the kernel's release-on-death, so this package proves death
// itself — a pid on this host that no longer answers a liveness probe — and
// never assumes it for a pid on another host, the same honesty
// [journal.Liveness] insists on.

// holder is what the lock directory carries about who holds it.
type holder struct {
	PID   int       `json:"pid"`
	Host  string    `json:"host"`
	Since time.Time `json:"since"`
}

// LockedError is returned when another process already holds the repo's
// dispatch lock. The CLI maps it to [ExitLocked] rather than a generic
// failure — a scheduler's whole view of a locked pass is a status and a
// log, and "locked" is a different fact than "refused".
type LockedError struct {
	Repo   string
	Holder holder
}

func (e *LockedError) Error() string {
	if e.Holder.Host == "" {
		return fmt.Sprintf("dispatch: %s is already being dispatched by another wand process on this machine", e.Repo)
	}
	return fmt.Sprintf(
		"dispatch: %s is already being dispatched by pid %d on %s (since %s); two selectors racing one Todo read is exactly what this lock exists to refuse",
		e.Repo, e.Holder.PID, e.Holder.Host, e.Holder.Since.UTC().Format(time.RFC3339))
}

// Lock is the held directory. Release is idempotent.
type Lock struct {
	dir string
}

// lockDir names the per-repo lock directory, scoped under the journal
// store's root so it lives beside the runs and ticket locks it coordinates
// with. The repo path is hashed rather than slugged: unlike a ticket
// identifier, a repo path is not something a human ever needs to read off
// the directory name, and a hash sidesteps every path-length and
// reserved-character question a slug would raise across platforms.
func lockDir(root, repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(root, "dispatch", fmt.Sprintf("%x.lock", sum[:8]))
}

// Acquire takes the dispatch lock for repo, without blocking. A directory
// left by a holder that has since died on this host is reclaimed and
// retried once; a directory held by a live holder, or one on a host this
// process cannot examine, refuses as [*LockedError].
func Acquire(root, repo string) (*Lock, error) {
	parent := filepath.Join(root, "dispatch")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("dispatch: creating the lock directory: %w", err)
	}
	dir := lockDir(root, repo)

	for attempt := 0; attempt < 2; attempt++ {
		if err := os.Mkdir(dir, 0o755); err == nil {
			h := holder{PID: os.Getpid(), Host: hostname(), Since: time.Now().UTC()}
			if err := writeHolder(dir, h); err != nil {
				os.RemoveAll(dir)
				return nil, fmt.Errorf("dispatch: taking the lock: %w", err)
			}
			return &Lock{dir: dir}, nil
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("dispatch: taking the lock: %w", err)
		}

		h, err := readHolder(dir)
		if os.IsNotExist(err) {
			// mkdir succeeded for some earlier process, which then died
			// before it could record who it was. Nobody to be alive:
			// reclaim and retry once.
			os.RemoveAll(dir)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("dispatch: the lock at %s is held but its holder is unreadable, so it is left for a person to clear: %w", dir, err)
		}
		if h.Host == hostname() && !processAlive(h.PID) {
			os.RemoveAll(dir)
			continue
		}
		// A live holder, or one on a host this process cannot examine — the
		// same honesty [journal.Unknown] insists on: never reclaim what
		// cannot be proven dead.
		return nil, &LockedError{Repo: repo, Holder: h}
	}
	return nil, fmt.Errorf("dispatch: could not take the lock for %s after reclaiming a dead holder", repo)
}

// Release drops the lock. Safe to call on a nil Lock.
func (l *Lock) Release() error {
	if l == nil || l.dir == "" {
		return nil
	}
	dir := l.dir
	l.dir = ""
	return os.RemoveAll(dir)
}

func writeHolder(dir string, h holder) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "holder.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "holder.json"))
}

func readHolder(dir string) (holder, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "holder.json"))
	if err != nil {
		return holder{}, err
	}
	var h holder
	if err := json.Unmarshal(raw, &h); err != nil {
		return holder{}, fmt.Errorf("parsing %s: %w", filepath.Join(dir, "holder.json"), err)
	}
	return h, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return h
}
