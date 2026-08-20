//go:build unix

package worker

import (
	"os"
	"os/exec"
	"syscall"
)

// SetupProcessGroup places the child in its own process group and makes
// context cancellation kill the whole group, not just the direct child. A
// harness spawns helpers of its own (shells, MCP transports); a survivor of
// a single-process kill would keep running in the worktree — and could
// rewrite the handoff path after the runner has collected and deleted it.
// Exported for the orchestrator's own subprocesses (verify, git, gh), which
// face the same hazard.
func SetupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			// The group is already gone; report it the way os/exec
			// treats a clean race, not as a cancellation failure.
			return os.ErrProcessDone
		}
		return err
	}
}
