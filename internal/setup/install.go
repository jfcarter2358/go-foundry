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
// only a folder carrying the .foundrypack stamp is ever deleted whole, and the
// stamp is always the last thing to go (removeInstall), so a removal that is
// interrupted can be finished by running either program again. A folder that
// lost its stamp to an interruption before that rule existed is recognised by
// holding nothing but files this installer would write itself (leftover). A
// running game is waited for rather than half-overwritten. Saves, in
// %APPDATA%\<data dir>, are only deleted when the player says so.
//
// Everything here goes through Host, so the whole of it runs in a test on any
// machine; cmd/foundrysetup is the Windows Host.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	// Record is what the uninstaller carries about the installation it
	// removes (ReadRecord): the stamp and the file list, for a folder whose
	// own stamp has gone.
	Record *Record
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
	//
	// A folder whose stamp has gone but which holds nothing this installer
	// would not write itself is the remains of an interrupted removal, and is
	// ours too — see leftover.
	ownFiles := installedNames(in.Names())
	dir := filepath.Join(h.LocalAppData(), "Programs", name)
	upgrading, prevVersion := false, ""
	if prev, ok := h.RegistryString(appKey(name), "InstallDir"); ok && (hasStamp(prev) || leftover(prev, ownFiles)) {
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
	// left alone entirely: the alternative would be installing over someone's
	// files and merging with them. When it looks like an old copy that is
	// merely missing its stamp, the message says which files are in the way.
	ours := hasStamp(dir) || leftover(dir, ownFiles)
	if !ours && hasEntries(dir) {
		msg := fmt.Sprintf("%s already has files in it that are not an installation of %s, so Setup will not install over them.", dir, st.Title)
		if strange := strangers(dir, ownFiles); len(strange) > 0 {
			msg += "\n\nIf this is an old copy of " + st.Title + ", delete these and run Setup again:\n" + listSome(strange)
		}
		return errors.New(msg)
	}

	if err := waitNotRunning(h, dir, st, o.Silent); err != nil {
		return err
	}

	// Replaced whole rather than merged: a level or texture renamed between
	// releases would otherwise stay behind for ever, and the game would keep
	// finding it.
	if ours {
		if err := removeInstall(dir, st.Title); err != nil {
			return err
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
	if err := register(h, name, st, dir, uninstaller, size); err != nil {
		return err
	}
	problems := makeShortcuts(h, name, exe, uninstaller, dir, wantDesktop, desktopLink)

	if o.Silent {
		return nil
	}
	msg := st.Title + " is installed."
	if len(problems) > 0 {
		msg += "\n\nSome shortcuts could not be made:\n" + strings.Join(problems, "\n")
	}
	launch := "\n\nPlay it now?"
	if st.IsEditor() {
		launch = "\n\nOpen it now?"
	}
	if h.Ask(msg+launch, false) {
		if err := h.Launch(exe, dir); err != nil {
			h.Tell("Could not start "+st.Title+": "+err.Error(), true)
		}
	}
	return nil
}

// register writes the registry keys an install is found and removed by.
func register(h Host, name string, st Stamp, dir, uninstaller string, size int64) error {
	exe := filepath.Join(dir, st.Exe)
	if err := h.SetRegistry(appKey(name), Str("InstallDir", dir), Str("Version", st.Version)); err != nil {
		return err
	}
	return h.SetRegistry(uninstallKey(name),
		Str("DisplayName", st.Title),
		Str("DisplayVersion", st.Version),
		Str("DisplayIcon", exe),
		Str("InstallLocation", dir),
		Str("UninstallString", `"`+uninstaller+`"`),
		Str("QuietUninstallString", `"`+uninstaller+`" -silent`),
		DWORD("NoModify", 1),
		DWORD("NoRepair", 1),
		DWORD("EstimatedSize", uint32(size/1024)),
	)
}

// makeShortcuts makes the Start Menu entries, and the desktop shortcut when one
// is wanted, and returns what went wrong. Shortcuts are the one part that can
// fail without the install failing, so a problem with one is reported rather
// than undoing the rest.
func makeShortcuts(h Host, name, exe, uninstaller, dir string, wantDesktop bool, desktopLink string) []string {
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
	return problems
}

// removeShortcutsAndKeys undoes register and makeShortcuts.
func removeShortcutsAndKeys(h Host, name string) error {
	if programs, err := h.Folder(FolderPrograms); err == nil {
		os.RemoveAll(filepath.Join(programs, name))
	}
	if desktop, err := h.Folder(FolderDesktop); err == nil {
		os.Remove(filepath.Join(desktop, name+".lnk"))
	}
	if err := h.DeleteRegistryKey(uninstallKey(name)); err != nil {
		return err
	}
	return h.DeleteRegistryKey(appKey(name))
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
//
// The folder's stamp says what it is. Without one, the uninstaller's own
// record (Options.Record) does, and then only the files that record lists are
// removed — the stamp is what makes deleting the whole folder safe, and a
// folder without it may have gained something that is not ours.
func Uninstall(h Host, dir string, o Options) error {
	data, err := os.ReadFile(filepath.Join(dir, StampName))
	stamped := err == nil
	if !stamped {
		if o.Record == nil {
			return fmt.Errorf("%s is not an installation Setup made, so nothing was removed", dir)
		}
		data = o.Record.Stamp
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
	if err := waitNotRunning(h, dir, st, o.Silent); err != nil {
		return err
	}

	var left []string
	if stamped {
		if err := removeInstall(dir, st.Title); err != nil {
			return err
		}
	} else {
		if left, err = removeListed(dir, st.Title, o.Record.Files); err != nil {
			return err
		}
	}
	// The registry and the shortcuts go even when something was left behind:
	// nothing of the program is there any more to run or to uninstall again.
	if err := removeShortcutsAndKeys(h, name); err != nil {
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
		msg := st.Title + " has been removed."
		if len(left) > 0 {
			msg += "\n\nThese were not part of it and were left in\n" + dir + ":\n" + listSome(left)
		}
		h.Tell(msg, false)
	}
	return nil
}

// waitNotRunning waits for every program in the installation to be closed —
// not only the main one. A program the editor started from its own folder (a
// packed game being tried out, say) holds its .exe and its DLLs just as surely,
// and a removal that met one part way used to leave a folder that was neither
// installed nor removable. The uninstaller is left out: the copy of it that is
// doing the removing started from it, and it is on its way out.
func waitNotRunning(h Host, dir string, st Stamp, silent bool) error {
	for {
		busy := runningIn(h, dir, st)
		if busy == "" {
			return nil
		}
		if silent {
			return ErrRunning
		}
		what := st.Title + " is still running."
		if busy != st.Exe {
			what = busy + ", from " + st.Title + "'s folder, is still in use."
		}
		if !h.Retry(what + "\n\nClose it, then click Retry.") {
			return ErrCancelled
		}
	}
}

// runningIn is the first program or library in the installation that is in
// use, by its path under dir, or "" when none is.
func runningIn(h Host, dir string, st Stamp) string {
	if h.InUse(filepath.Join(dir, st.Exe)) {
		return st.Exe
	}
	found := ""
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".exe" && ext != ".dll" {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.EqualFold(rel, UninstallerName) || strings.EqualFold(rel, st.Exe) {
			return nil
		}
		if h.InUse(p) {
			found = filepath.ToSlash(rel)
		}
		return nil
	})
	return found
}

// ---------- removing ----------

// removeAttempts and removeWait are how hard a removal tries against a file
// something has open for a moment — Windows Defender scanning an .exe that was
// only just written is the usual one, and it lets go in well under a second.
var (
	removeAttempts = 15
	removeWait     = 200 * time.Millisecond
)

// removeInstall deletes an installation, its stamp LAST. Everything else goes
// first, so a removal that fails part way — a file still open — leaves a folder
// that still says what it is, and running Setup or the uninstaller again
// finishes the job. The stamp used to go first (it sorts first), and a failure
// after it left a folder that both refused as not theirs.
func removeInstall(dir, title string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == StampName {
			continue
		}
		if err := removeRetrying(filepath.Join(dir, e.Name())); err != nil {
			return lockedError(title, err)
		}
	}
	if err := removeRetrying(dir); err != nil {
		return lockedError(title, err)
	}
	return nil
}

// removeListed removes the files an installation wrote and the uninstaller,
// then every folder that leaves empty, and dir itself if nothing else is in
// it. It returns what was left because it was not the installation's.
func removeListed(dir, title string, files []string) ([]string, error) {
	for _, name := range append(append([]string(nil), files...), UninstallerName) {
		p, err := within(dir, name)
		if err != nil {
			continue
		}
		if err := removeRetrying(p); err != nil {
			return nil, lockedError(title, err)
		}
	}
	// Deepest first, so a folder is empty by the time it is reached.
	var dirs []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		os.Remove(d) // fails, rightly, on a folder with something else in it
	}
	return strangers(dir, nil), nil
}

func removeRetrying(path string) error {
	var err error
	for i := 0; i < removeAttempts; i++ {
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
		time.Sleep(removeWait)
	}
	return err
}

func lockedError(title string, err error) error {
	return fmt.Errorf("%v\n\nSomething still has this open: %s itself, a program started from it, "+
		"or a virus scanner still checking it. Close it and run this again — it will finish the job", err, title)
}

// installedNames is the set of paths an installation of this payload holds:
// its files and its uninstaller.
func installedNames(files []string) map[string]bool {
	set := map[string]bool{strings.ToLower(UninstallerName): true}
	for _, f := range files {
		set[strings.ToLower(f)] = true
	}
	return set
}

// leftover reports whether dir is what an interrupted removal of an earlier
// version left: no stamp, but nothing in it that this installer would not
// write there itself. Replacing those is exactly what installing does anyway,
// so treating the folder as ours can never delete anything that was not.
func leftover(dir string, own map[string]bool) bool {
	return !hasStamp(dir) && hasEntries(dir) && len(strangers(dir, own)) == 0
}

// strangers are the files under dir that are not in own (nil: every file), by
// slash path. Windows paths are compared without case, as Windows does.
func strangers(dir string, own map[string]bool) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !own[strings.ToLower(rel)] {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// listSome is a list of paths for a message box, cut short when it is long.
func listSome(paths []string) string {
	const most = 12
	if len(paths) <= most {
		return strings.Join(paths, "\n")
	}
	return strings.Join(paths[:most], "\n") + fmt.Sprintf("\n…and %d more", len(paths)-most)
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
