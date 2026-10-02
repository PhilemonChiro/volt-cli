//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
	"time"
)

// setGroup puts cmd in its own process group so the whole tree can be
// signalled; the terminal's Ctrl-C then reaches it only through volt.
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *exec.Cmd) {
	pgid := cmd.Process.Pid
	syscall.Kill(-pgid, syscall.SIGTERM)
	time.AfterFunc(2*time.Second, func() { syscall.Kill(-pgid, syscall.SIGKILL) })
}
