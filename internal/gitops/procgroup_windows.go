//go:build windows

package gitops

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// configureProcessGroup starts cmd in its own process group so context
// cancellation can terminate git plus anything it spawns, not just the direct
// git process. Windows has no equivalent of POSIX's kill(-pid, SIGKILL), so a
// cancelled command's whole tree is torn down via taskkill /T instead.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		// Cancel fires after cmd's own context is already done, so the kill
		// itself needs an independent, short-lived context to actually run
		// rather than one that would abort it before taskkill can execute.
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		// #nosec G204 -- the PID argument is our own tracked child process, not
		// externally-controlled input.
		return exec.CommandContext(killCtx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	// Bound how long Wait() blocks after Cancel runs: if taskkill somehow fails
	// to reap every process promptly, fall back to closing the I/O pipes and
	// killing the direct process so the caller is never stuck waiting.
	cmd.WaitDelay = 5 * time.Second
}
