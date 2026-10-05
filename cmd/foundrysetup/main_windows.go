//go:build windows

// Command foundrysetup is a game's Windows installer and uninstaller — and the
// editor's, for a Foundry release (task dist-editor).
//
// One program, two jobs. With an install tree appended to it (internal/setup)
// it is the game's Setup.exe; with nothing appended it is the game's
// uninstall.exe, which Setup writes into the install tree. The release build
// does the appending — nobody runs this program bare except as an uninstaller.
//
//	Setup.exe                       install, asking as it goes
//	Setup.exe -silent [-desktop]    install asking nothing (exit 2 if the game is running)
//	uninstall.exe [-silent] [-delete-saves]
//
// NSIS's /S is accepted for -silent, so a script written for the NSIS
// installer drives this one the same way.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"foundry/internal/setup"
)

func main() {
	// Setup running itself to ask the graphics driver what it offers; see
	// graphics_windows.go.
	if len(os.Args) == 2 && os.Args[1] == probeGraphicsFlag {
		runGraphicsProbe()
	}
	silent := flag.Bool("silent", false, "install or uninstall without asking anything")
	desktop := flag.Bool("desktop", false, "with -silent, also put a shortcut on the desktop")
	dir := flag.String("dir", "", `install a fresh copy here instead of in %LOCALAPPDATA%\Programs`)
	deleteSaves := flag.Bool("delete-saves", false, "with -silent, also delete the game's saves when uninstalling")
	removeDir := flag.String("uninstall-dir", "", "remove the installation in this directory (how the uninstaller runs itself, from a copy outside the install)")
	flag.Parse()
	for _, a := range flag.Args() {
		if strings.EqualFold(a, "/S") {
			*silent = true
		}
	}

	o := setup.Options{Silent: *silent, Desktop: *desktop, Dir: *dir, DeleteSaves: *deleteSaves}
	os.Exit(run(host{}, o, *removeDir))
}

func run(h host, o setup.Options, removeDir string) int {
	if removeDir != "" {
		// What this uninstaller was written to remove, carried in the program
		// itself, for an installation whose own stamp has gone.
		if exe, err := os.Executable(); err == nil {
			if r, ok := setup.ReadRecord(exe); ok {
				o.Record = &r
			}
		}
		return exitCode(h, o, setup.Uninstall(h, removeDir, o))
	}
	exe, err := os.Executable()
	if err != nil {
		return exitCode(h, o, err)
	}
	in, err := setup.Open(exe)
	if errors.Is(err, setup.ErrNoPayload) {
		return exitCode(h, o, relaunchUninstaller(exe, o))
	}
	if err != nil {
		return exitCode(h, o, err)
	}
	defer in.Close()
	return exitCode(h, o, setup.Install(h, in, o))
}

// relaunchUninstaller runs a copy of this uninstaller from the temporary folder,
// and returns.
//
// Windows will not delete a program while it runs, and this one lives in the
// folder it is about to delete. The copy runs from outside it, and is left in
// the temporary folder when it is done — NSIS's uninstallers do exactly the
// same, for the same reason.
func relaunchUninstaller(exe string, o setup.Options) error {
	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp("", "foundry-uninstall-*.exe")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	args := []string{"-uninstall-dir", filepath.Dir(exe)}
	if o.Silent {
		args = append(args, "-silent")
	}
	if o.DeleteSaves {
		args = append(args, "-delete-saves")
	}
	cmd := exec.Command(tmp.Name(), args...)
	// Not the install folder, which is where the Start Menu's Uninstall
	// shortcut starts this: a process's working directory cannot be deleted,
	// so the copy would empty the folder and then fail to remove it.
	cmd.Dir = filepath.Dir(tmp.Name())
	return cmd.Start()
}

// exitCode is the process's answer: 0 done, 1 cancelled, 2 the game is running,
// 3 anything else — which an interactive run also says in a message box, since
// a program with no console has nowhere else to say it.
func exitCode(h host, o setup.Options, err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, setup.ErrCancelled):
		return 1
	case errors.Is(err, setup.ErrRunning):
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if o.Silent {
		fmt.Fprintln(os.Stderr, err)
	} else {
		h.Tell(err.Error(), true)
	}
	return 3
}
