package dispatch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireThenRelease(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root, "/repo/a")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := os.Stat(lockDir(root, "/repo/a")); err != nil {
		t.Fatalf("lock directory not created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(lockDir(root, "/repo/a")); !os.IsNotExist(err) {
		t.Fatalf("lock directory still exists after Release: %v", err)
	}
}

func TestAcquireRefusesALiveHolder(t *testing.T) {
	root := t.TempDir()
	dir := lockDir(root, "/repo/a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// This test process's own pid is alive by construction.
	if err := writeHolder(dir, holder{PID: os.Getpid(), Host: hostname()}); err != nil {
		t.Fatal(err)
	}

	_, err := Acquire(root, "/repo/a")
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("Acquire = %v, want a *LockedError", err)
	}
	if locked.Holder.PID != os.Getpid() {
		t.Errorf("LockedError.Holder.PID = %d, want %d", locked.Holder.PID, os.Getpid())
	}
}

func TestAcquireReclaimsADeadHolder(t *testing.T) {
	root := t.TempDir()
	dir := lockDir(root, "/repo/a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A pid nothing on this machine holds. 1 is init/launchd, which this
	// unprivileged test cannot signal — the same "exists but not mine"
	// answer as EPERM for a real foreign process, so pick a pid outside any
	// real range instead: negative pids are never assigned.
	dead := -424242
	if err := writeHolder(dir, holder{PID: dead, Host: hostname()}); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(root, "/repo/a")
	if err != nil {
		t.Fatalf("Acquire over a dead holder: %v", err)
	}
	defer lock.Release()

	h, err := readHolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.PID != os.Getpid() {
		t.Errorf("holder.PID = %d, want this process's own pid %d", h.PID, os.Getpid())
	}
}

func TestAcquireTreatsAnotherHostAsUnreclaimable(t *testing.T) {
	root := t.TempDir()
	dir := lockDir(root, "/repo/a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A pid that would be dead on this host, but the holder claims a
	// different one — never reclaimed, the same honesty journal.Unknown
	// insists on for a lease this process cannot examine.
	if err := writeHolder(dir, holder{PID: -424242, Host: "some-other-host"}); err != nil {
		t.Fatal(err)
	}

	_, err := Acquire(root, "/repo/a")
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("Acquire = %v, want a *LockedError for an unverifiable foreign holder", err)
	}
}

func TestAcquireReclaimsAMissingHolderFile(t *testing.T) {
	root := t.TempDir()
	dir := lockDir(root, "/repo/a")
	// mkdir succeeded but the process died before writing holder.json.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(root, "/repo/a")
	if err != nil {
		t.Fatalf("Acquire over a holder-less directory: %v", err)
	}
	lock.Release()
}

func TestAcquireScopesLocksPerRepo(t *testing.T) {
	root := t.TempDir()
	a, err := Acquire(root, "/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()

	b, err := Acquire(root, "/repo/b")
	if err != nil {
		t.Fatalf("a different repo should not contend: %v", err)
	}
	defer b.Release()
}

func TestReleaseIsIdempotent(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root, "/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("second Release: %v", err)
	}
	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Errorf("Release on a nil Lock: %v", err)
	}
}

func TestReadHolderMissingIsIsNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := readHolder(filepath.Join(dir, "missing"))
	if !os.IsNotExist(err) {
		t.Fatalf("readHolder on a missing dir = %v, want IsNotExist", err)
	}
}
