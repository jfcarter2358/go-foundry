//go:build !windows

package claude

import (
	"os"
	"os/exec"
	"syscall"
)

// isolateProcessTree puts the CLI in a process group of its own, so a session
// that will not stop can be taken down with everything it started — the tools it
// ran, and any MCP server it spawned.
func isolateProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func processEnv() []string { return os.Environ() }
