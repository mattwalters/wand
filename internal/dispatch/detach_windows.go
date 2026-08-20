//go:build windows

package dispatch

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own process group, detached from this process's
// console — CREATE_NEW_PROCESS_GROUP means a Ctrl+C event delivered to this
// console does not reach it. CI runs Linux and macOS, so this file is
// compile-checked there and exercised only on a Windows machine.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
