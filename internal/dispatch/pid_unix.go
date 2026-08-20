//go:build unix

package dispatch

import "syscall"

// processAlive probes a pid with signal 0: no signal is delivered, but the
// kernel still validates that the pid exists and reports permission the way
// it would for a real signal. ESRCH means gone; EPERM means it exists and
// this process merely lacks the right to signal it — alive either way.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
