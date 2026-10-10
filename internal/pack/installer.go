package pack

// installer.go — the Linux installers, from the scripts in packaging/.
//
// A .deb is dpkg-deb's job, driven by the same script the Taskfile runs; what
// is here is running it without a repository to find it in (it is embedded,
// see foundry/packaging) and saying plainly when the tool it needs is missing.
// The Windows installer is not a script at all: see internal/setup.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"foundry/packaging"
)

// LinuxInstallScriptName is what install.sh is called inside a Linux tree.
const LinuxInstallScriptName = "install.sh"

// WriteLinuxInstallScript puts install.sh into a staged Linux tree, beside the
// executable, where the tarball's reader will look for it.
func WriteLinuxInstallScript(stage string) error {
	return os.WriteFile(filepath.Join(stage, LinuxInstallScriptName), packaging.LinuxInstallScript, 0755)
}

// DebAvailable says why a .deb cannot be built on this machine, or nil.
func DebAvailable() error {
	if runtime.GOOS == "windows" {
		return errors.New("a .deb is built with dpkg-deb, which does not run on Windows")
	}
	for _, tool := range []string{"sh", "dpkg-deb", "dpkg"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s not found (apt install dpkg-dev)", tool)
		}
	}
	return nil
}

// Deb builds a .deb from a staged Linux tree.
func Deb(ctx context.Context, stage, out string, logw io.Writer) error {
	if err := DebAvailable(); err != nil {
		return err
	}
	script, cleanup, err := writeScript("make-deb.sh", packaging.DebScript)
	if err != nil {
		return err
	}
	defer cleanup()
	// Run through sh rather than executed, so the temporary copy needs no
	// executable bit.
	cmd := exec.CommandContext(ctx, "sh", script, stage, out)
	cmd.Stdout, cmd.Stderr = logw, logw
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make-deb.sh: %w", err)
	}
	return nil
}

// writeScript writes an embedded script to a temporary directory of its own,
// and returns its path and the function that removes it again.
func writeScript(name string, body []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "foundry-installer-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0644); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}
