//go:build release

package app

// project_release.go — a release build's project is its install: the
// resources/ beside the executable. Nothing to choose and nothing to reopen.

func chooseProject(exeDir string, args []string) (string, string) {
	return exeDir, "the install"
}

func projectOpened(dir string) {}

func relaunchInto(dir string) {}
