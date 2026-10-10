//go:build windows

package release

import (
	"os/exec"
	"strconv"
)

// isolateProcessTree does nothing: taskkill finds the tree by walking it.
func isolateProcessTree(cmd *exec.Cmd) {}

// killProcessTree kills the build and everything under it. taskkill /T takes
// the build's own children with it — a go build — which
// Process.Kill would leave running.
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
