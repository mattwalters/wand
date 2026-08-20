//go:build unix

package dispatch

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own session: detached from this process's
// controlling terminal, so a SIGINT or SIGHUP delivered to the terminal's
// foreground process group — the watcher's own Ctrl-C — does not reach it.
// Deliberately not [worker.SetupProcessGroup]: that helper wires
// cmd.Cancel to kill the child when its context is done, which is exactly
// the opposite of surviving the watcher, and this cmd is started without a
// context in the first place (exec.Command, not exec.CommandContext).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
