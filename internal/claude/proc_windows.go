//go:build windows

package claude

import (
	"os"
	"os/exec"
	"strconv"
)

// isolateProcessTree does nothing: taskkill finds the tree by walking it.
func isolateProcessTree(cmd *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}

func processEnv() []string { return os.Environ() }
