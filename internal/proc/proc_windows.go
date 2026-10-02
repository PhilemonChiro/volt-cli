package proc

import "os/exec"

func setGroup(*exec.Cmd) {}

func terminate(cmd *exec.Cmd) { cmd.Process.Kill() }
