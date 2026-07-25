//go:build !windows

package gitops

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcessGroup starts cmd in its own process group and arranges for
// context cancellation to kill that whole group — git plus anything it spawns
// (credential helpers, hooks, clean/smudge filters) — not just the direct git
// process. exec.CommandContext's default Cancel only kills the single process
// it started, so a subprocess git forks would survive cancellation, keep
// running, and could still hold file handles open on the very tree the caller
// believes it just aborted.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Bound how long Wait() blocks after Cancel runs: if the group kill somehow
	// fails to reap every process promptly, fall back to closing the I/O pipes
	// and killing the direct process so the caller is never stuck waiting.
	cmd.WaitDelay = 5 * time.Second
}
