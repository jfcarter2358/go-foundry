//go:build !windows

package release

import (
	"os/exec"
	"syscall"
)

// isolateProcessTree puts the build in a process group of its own, so that
// killProcessTree can take everything under it down together.
func isolateProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree kills the build's process group: the build, and any go build
// or packaging tool it is waiting on.
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
