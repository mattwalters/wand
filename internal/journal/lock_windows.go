//go:build windows

package journal

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// CI runs Linux and macOS, so this file is compile-checked there and
// exercised only on a Windows machine. The behaviour it has to match is
// stated in lock_unix.go and pinned by the crash tests.

// tryLock takes an exclusive lock on the first byte of f without blocking.
// LockFileEx is Windows' equivalent of the flock story in lock_unix.go: the
// lock is released by the kernel when the holding process exits, which is
// what makes a dead holder provably dead rather than merely quiet.
func tryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped),
	)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return false, nil
	default:
		return false, err
	}
}

func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
