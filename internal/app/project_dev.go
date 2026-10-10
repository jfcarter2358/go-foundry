//go:build !release

package app

// project_dev.go — which project a development build opens (internal/project),
// and starting over on another one.

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"foundry/internal/fileutil"
	"foundry/internal/platform"
	"foundry/internal/project"
)

// editorDataDir is where the editor keeps its own per-user state: the recent
// project list. "" when there is nowhere to keep it.
func editorDataDir() string {
	dir, err := fileutil.UserDataDir(project.DataDirName)
	if err != nil {
		return ""
	}
	return dir
}

// chooseProject is the project to open, and why it was chosen.
func chooseProject(exeDir string, args []string) (string, string) {
	c, err := project.Resolve(exeDir, editorDataDir(), args)
	if err != nil {
		// A command line that does not parse is reported, not fatal: the
		// project it failed to name is the only thing lost.
		c, _ = project.Resolve(exeDir, editorDataDir(), nil)
		c.Reason += " (" + err.Error() + ")"
	}
	return c.Dir, c.Reason
}

// projectOpened records the project in the recent list and names the window
// after it, now that the log is open to say if that fails.
func projectOpened(dir string) {
	if err := project.Remember(editorDataDir(), dir); err != nil {
		log.Printf("WARNING: could not update the recent project list: %v", err)
	}
	platform.WinTitle = "Foundry — " + filepath.Base(dir)
}

// relaunchInto starts this program again on another project. Run as the last
// thing on the way out, once this process has let go of its window.
func relaunchInto(dir string) {
	if dir == "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("could not reopen on %s: %v", dir, err)
		return
	}
	cmd := exec.Command(exe, "-"+project.Flag, dir)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// Logged either way: this is the last line this process writes, and the
	// next one's own "project: ... (named on the command line)" is what says
	// the hand-over worked. Without the pair, a relaunch that opened the wrong
	// project looks exactly like a relaunch that never happened.
	log.Printf("reopening on %s: %s -%s %s", dir, exe, project.Flag, dir)
	if err := cmd.Start(); err != nil {
		log.Printf("could not reopen on %s: %v", dir, err)
	}
}
