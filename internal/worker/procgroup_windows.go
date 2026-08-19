//go:build windows

package worker

import "os/exec"

// setupProcessGroup is a no-op on Windows: there is no POSIX process group
// to signal, so cancellation falls back to os/exec's default single-process
// kill, and cmd.WaitDelay bounds any descendant that survives it.
func setupProcessGroup(cmd *exec.Cmd) {}
