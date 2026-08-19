//go:build unix

package journal

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive advisory lock on f without blocking, and
// reports whether it got it.
//
// flock is the mechanism because of what it does when a process dies: the
// kernel drops the lock, whatever killed the process — SIGKILL, a panic, a
// power cut, an OOM. That is what turns "is this run still alive?" from a
// heuristic over timestamps and pids into a fact. A heartbeat can only say
// "nothing recently", and a pid can be reused by an unrelated process
// within seconds.
//
// The lock is tied to the open file description, so a second open in this
// same process is refused exactly as another process would be — a run
// cannot mistake itself for a dead holder.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return false, err
	}
}

func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
