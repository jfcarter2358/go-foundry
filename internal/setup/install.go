package setup

// install.go — installing and uninstalling.
//
// The layout is the NSIS installer's (packaging/windows/installer.nsi) key for
// key, so a game installed by either is found, upgraded and removed by the
// other:
//
//	%LOCALAPPDATA%\Programs\<name>                    the install tree
//	HKCU\Software\<name>                              InstallDir, Version
//	HKCU\...\CurrentVersion\Uninstall\<name>          Add/Remove Programs
//	<Start Menu>\Programs\<name>\<name>.lnk           and "Uninstall <name>.lnk"
//	<Desktop>\<name>.lnk                              if the player asked for one
//
// And the rules are NSIS's too. An install per user, needing no administrator.
// An upgrade goes where the old install is and replaces it whole — the game
// never writes into its install tree, so there is nothing there to keep — but
// only a folder carrying the .foundrypack stamp is ever deleted. A running game
// is waited for rather than half-overwritten. Saves, in %APPDATA%\<data dir>,
// are only deleted when the player says so.
//
// Everything here goes through Host, so the whole of it runs in a test on any
// machine; cmd/foundrysetup is the Windows Host.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
)

// Host is what installing needs from the machine it runs on.
type Host interface {
	// Ask puts a yes/no question to the player. defaultNo makes No the button
	// Enter presses. Never called by a silent install.
	Ask(text string, defaultNo bool) bool
	// Tell shows the player a message.
	Tell(text string, isError bool)
	// Retry asks whether to try something again.
	Retry(text string) bool

	LocalAppData() string
	AppData() string
	Folder(which KnownFolder) (string, error)

	// RegistryString reads a string value under HKEY_CURRENT_USER.
	RegistryString(key, name string) (string, bool)
	// SetRegistry creates a key under HKEY_CURRENT_USER and sets values in it.
	SetRegistry(key string, values ...RegistryValue) error
	// DeleteRegistryKey removes a key and everything under it; one that is
	// not there is not an error.
	DeleteRegistryKey(key string) error

	CreateShortcut(link, target, workDir string) error
	// InUse reports whether a file is held open by a running program.
	InUse(path string) bool
	Launch(exe, dir string) error
}

// KnownFolder names one of the per-user folders a shortcut goes in.
type KnownFolder int

const (
	FolderPrograms KnownFolder = iota // the Start Menu's Programs folder
	FolderDesktop
)

// RegistryValue is one value to write.
type RegistryValue struct {
	Name    string
	Str     string
	DWORD   uint32
	IsDWORD bool
}

// Str is a REG_SZ value.
func Str(name, value string) RegistryValue { return RegistryValue{Name: name, Str: value} }

// DWORD is a REG_DWORD value.
func DWORD(name string, value uint32) RegistryValue {
	return RegistryValue{Name: name, DWORD: value, IsDWORD: true}
}

// Options is how an install or uninstall was asked for.
type Options struct {
	// Silent asks nothing: an install goes to the usual place (or the old
	// install) and an uninstall keeps the saves, unless the flags below say
	// otherwise.
	Silent bool
	// Desktop is a silent install's answer to "a desktop shortcut?".
	Desktop bool
	// Dir is where a fresh install goes instead of
	// %LOCALAPPDATA%\Programs\<name>. An upgrade always goes where the old
	// install is.
	Dir string
	// DeleteSaves is a silent uninstall's answer to "delete the saves?".
	DeleteSaves bool
}

var (
	// ErrCancelled is the player saying no.
	ErrCancelled = errors.New("cancelled")
	// ErrRunning is a silent install or uninstall finding the game running.
	ErrRunning = errors.New("the game is running; close it and run this again")
)

// UninstallerName is the uninstaller's name in an install tree, as NSIS named
// it.
const UninstallerName = "uninstall.exe"

func appKey(name string) string { return `Software\` + name }

func uninstallKey(name string) string {
	return `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + name
}

// installName is the folder, key and shortcut name a game installs under: its
// title, with whatever a folder name cannot hold taken out.
func installName(st Stamp) (string, error) {
	name := fileutil.SanitizeDirName(st.Title)
	if name == "" || !singleName(name) {
		return "", fmt.Errorf("the title %q makes no usable folder name", st.Title)
	}
	return name, nil
}

// Install installs a game from an opened installer.
func Install(h Host, in *Installer, o Options) error {
	st, err := in.Stamp()
	if err != nil {
		return err
	}
	name, err := installName(st)
	if err != nil {
		return err
	}

	// Where: the old install if there is one — the registry naming a folder
	// that still carries the stamp — and otherwise the usual place. A registry
	// entry whose folder has since been deleted is not an upgrade, just a fresh
	// install that knows where the last one was.
	dir := filepath.Join(h.LocalAppData(), "Programs", name)
	upgrading, prevVersion := false, ""
	if prev, ok := h.RegistryString(appKey(name), "InstallDir"); ok && hasStamp(prev) {
		dir, upgrading = prev, true
		prevVersion, _ = h.RegistryString(appKey(name), "Version")
	} else if o.Dir != "" {
		dir = o.Dir
	}

	desktopLink := ""
	if desktop, err := h.Folder(FolderDesktop); err == nil {
		desktopLink = filepath.Join(desktop, name+".lnk")
	}
	wantDesktop := o.Desktop
	if upgrading {
		// Kept as the player left it last time.
		wantDesktop = wantDesktop || fileExists(desktopLink)
	}

	if !o.Silent {
		if !h.Ask(installQuestion(st, dir, upgrading, prevVersion), false) {
			return ErrCancelled
		}
		if !upgrading && desktopLink != "" {
			wantDesktop = h.Ask("Put a shortcut to "+st.Title+" on the desktop?", true)
		}
	}

	// A folder that already holds something and is not an install of ours is
	// left alone entirely. Nothing is ever deleted without the stamp, so the
	// alternative would be installing over someone's files and merging with
	// them.
	if !upgrading && !hasStamp(dir) && hasEntries(dir) {
		return fmt.Errorf("%s already has files in it that are not an installation of %s, so Setup will not install over them", dir, st.Title)
	}

	if err := waitNotRunning(h, filepath.Join(dir, st.Exe), st.Title, o.Silent); err != nil {
		return err
	}

	// Replaced whole rather than merged: a level or texture renamed between
	// releases would otherwise stay behind for ever, and the game would keep
	// finding it.
	if hasStamp(dir) {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing the previous installation: %w", err)
		}
	}
	size, err := in.Extract(dir)
	if err != nil {
		return err
	}
	uninstaller := filepath.Join(dir, UninstallerName)
	if err := in.WriteUninstaller(uninstaller); err != nil {
		return err
	}

	exe := filepath.Join(dir, st.Exe)
	if err := h.SetRegistry(appKey(name), Str("InstallDir", dir), Str("Version", st.Version)); err != nil {
		return err
	}
	if err := h.SetRegistry(uninstallKey(name),
		Str("DisplayName", st.Title),
		Str("DisplayVersion", st.Version),
		Str("DisplayIcon", exe),
		Str("InstallLocation", dir),
		Str("UninstallString", `"`+uninstaller+`"`),
		Str("QuietUninstallString", `"`+uninstaller+`" -silent`),
		DWORD("NoModify", 1),
		DWORD("NoRepair", 1),
		DWORD("EstimatedSize", uint32(size/1024)),
	); err != nil {
		return err
	}

	// Shortcuts are the one part that can fail without the game failing to
	// install, so a problem with one is reported rather than undoing the rest.
	var problems []string
	if programs, err := h.Folder(FolderPrograms); err != nil {
		problems = append(problems, err.Error())
	} else {
		folder := filepath.Join(programs, name)
		if err := os.MkdirAll(folder, 0755); err != nil {
			problems = append(problems, err.Error())
		} else {
			if err := h.CreateShortcut(filepath.Join(folder, name+".lnk"), exe, dir); err != nil {
				problems = append(problems, err.Error())
			}
			if err := h.CreateShortcut(filepath.Join(folder, "Uninstall "+name+".lnk"), uninstaller, dir); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	if wantDesktop && desktopLink != "" {
		if err := h.CreateShortcut(desktopLink, exe, dir); err != nil {
			problems = append(problems, err.Error())
		}
	}

	if o.Silent {
		return nil
	}
	msg := st.Title + " is installed."
	if len(problems) > 0 {
		msg += "\n\nSome shortcuts could not be made:\n" + strings.Join(problems, "\n")
	}
	if h.Ask(msg+"\n\nPlay it now?", false) {
		if err := h.Launch(exe, dir); err != nil {
			h.Tell("Could not start "+st.Title+": "+err.Error(), true)
		}
	}
	return nil
}

func installQuestion(st Stamp, dir string, upgrading bool, prevVersion string) string {
	switch {
	case upgrading && prevVersion != "":
		return fmt.Sprintf("Update %s in\n%s\nfrom version %s to version %s?\n\nYour saved games and settings are kept.",
			st.Title, dir, prevVersion, st.Version)
	case upgrading:
		return fmt.Sprintf("Update the copy of %s installed in\n%s\nto version %s?\n\nYour saved games and settings are kept.",
			st.Title, dir, st.Version)
	default:
		return fmt.Sprintf("Install %s %s?\n\nIt will be installed in\n%s", st.Title, st.Version, dir)
	}
}

// Uninstall removes the installation in dir.
func Uninstall(h Host, dir string, o Options) error {
	data, err := os.ReadFile(filepath.Join(dir, StampName))
	if err != nil {
		return fmt.Errorf("%s is not an installation Setup made, so nothing was removed", dir)
	}
	st, err := ParseStamp(data)
	if err != nil {
		return err
	}
	name, err := installName(st)
	if err != nil {
		return err
	}

	if !o.Silent && !h.Ask(fmt.Sprintf("Remove %s from\n%s?", st.Title, dir), false) {
		return ErrCancelled
	}
	if err := waitNotRunning(h, filepath.Join(dir, st.Exe), st.Title, o.Silent); err != nil {
		return err
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing %s: %w", dir, err)
	}
	if programs, err := h.Folder(FolderPrograms); err == nil {
		os.RemoveAll(filepath.Join(programs, name))
	}
	if desktop, err := h.Folder(FolderDesktop); err == nil {
		os.Remove(filepath.Join(desktop, name+".lnk"))
	}
	if err := h.DeleteRegistryKey(uninstallKey(name)); err != nil {
		return err
	}
	if err := h.DeleteRegistryKey(appKey(name)); err != nil {
		return err
	}

	// Saves are kept unless the player says otherwise: an uninstall is very
	// often a reinstall, and a lost save is not recoverable.
	if st.DataDir != "" {
		saves := filepath.Join(h.AppData(), st.DataDir)
		if hasEntries(saves) {
			del := o.DeleteSaves
			if !o.Silent {
				del = h.Ask(fmt.Sprintf("Also delete your %s saves and settings?\n\n%s", st.Title, saves), true)
			}
			if del {
				os.RemoveAll(saves)
			}
		}
	}

	if !o.Silent {
		h.Tell(st.Title+" has been removed.", false)
	}
	return nil
}

func waitNotRunning(h Host, exe, title string, silent bool) error {
	for h.InUse(exe) {
		if silent {
			return ErrRunning
		}
		if !h.Retry(title + " is still running.\n\nClose it, then click Retry.") {
			return ErrCancelled
		}
	}
	return nil
}

func hasStamp(dir string) bool {
	return dir != "" && fileExists(filepath.Join(dir, StampName))
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func hasEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}
