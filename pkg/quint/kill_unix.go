//go:build unix

package quint

import (
	"os/exec"
	"syscall"
)

// killGroup starts cmd in its own process group and makes cancellation
// kill the whole group: quint verify runs Java child processes that would
// otherwise outlive it.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
