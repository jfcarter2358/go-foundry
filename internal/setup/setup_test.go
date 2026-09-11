package setup

// The installer's whole behaviour, against a fake machine: where a game goes,
// what an upgrade replaces, what is never touched, and what an uninstall takes
// with it.

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost is a Windows user profile in a temporary directory.
type fakeHost struct {
	root    string
	ask     func(text string) bool // nil answers yes to everything
	asked   []string
	told    []string
	busy    int  // how many more InUse checks report the game running
	retry   bool // what Retry answers
	retried int
	reg     map[string]map[string]RegistryValue
	links   map[string]string
	launch  string
}

func newHost(t *testing.T) *fakeHost {
	return &fakeHost{root: t.TempDir(), reg: map[string]map[string]RegistryValue{}, links: map[string]string{}}
}

func (f *fakeHost) Ask(text string, defaultNo bool) bool {
	f.asked = append(f.asked, text)
	return f.ask == nil || f.ask(text)
}
func (f *fakeHost) Tell(text string, isError bool) { f.told = append(f.told, text) }
func (f *fakeHost) Retry(text string) bool         { f.retried++; return f.retry }
func (f *fakeHost) LocalAppData() string           { return filepath.Join(f.root, "Local") }
func (f *fakeHost) AppData() string                { return filepath.Join(f.root, "Roaming") }

func (f *fakeHost) Folder(which KnownFolder) (string, error) {
	if which == FolderDesktop {
		return filepath.Join(f.root, "Desktop"), nil
	}
	return filepath.Join(f.root, "Start Menu", "Programs"), nil
}

func (f *fakeHost) RegistryString(key, name string) (string, bool) {
	v, ok := f.reg[key][name]
	return v.Str, ok && !v.IsDWORD
}

func (f *fakeHost) SetRegistry(key string, values ...RegistryValue) error {
	if f.reg[key] == nil {
		f.reg[key] = map[string]RegistryValue{}
	}
	for _, v := range values {
		f.reg[key][v.Name] = v
	}
	return nil
}

func (f *fakeHost) DeleteRegistryKey(key string) error { delete(f.reg, key); return nil }

func (f *fakeHost) CreateShortcut(link, target, workDir string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		return err
	}
	f.links[link] = target
	return os.WriteFile(link, []byte(target), 0644)
}

func (f *fakeHost) InUse(path string) bool {
	if f.busy > 0 {
		f.busy--
		return true
	}
	return false
}

func (f *fakeHost) Launch(exe, dir string) error { f.launch = exe; return nil }

const stubBytes = "MZ this stands in for the installer program"

// quote is pack's shellQuote, which the stamp is written with.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// makeInstaller builds a Setup.exe for a game with the given files.
func makeInstaller(t *testing.T, title, version string, files map[string]string) string {
	t.Helper()
	stage := t.TempDir()
	files[StampName] = "# test\nTITLE=" + quote(title) + "\nVERSION=" + quote(version) +
		"\nEXE='test-game.exe'\nDATA_DIR=" + quote(title) + "\n"
	for name, body := range files {
		p := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	stub := filepath.Join(t.TempDir(), "stub.exe")
	if err := os.WriteFile(stub, []byte(stubBytes), 0755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "Setup.exe")
	if err := Write(stub, stage, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func openInstaller(t *testing.T, path string) *Installer {
	t.Helper()
	in, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	return in
}

func gameFiles(level string) map[string]string {
	return map[string]string{"test-game.exe": "the game", "levels/" + level: "a level"}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAnInstallerCarriesItsTreeAndItsProgram(t *testing.T) {
	path := makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp"))
	in := openInstaller(t, path)

	if in.StubSize != int64(len(stubBytes)) {
		t.Errorf("StubSize = %d, want %d", in.StubSize, len(stubBytes))
	}
	st, err := in.Stamp()
	if err != nil || st.Title != "Test Game" || st.Version != "1.0.0" || st.Exe != "test-game.exe" {
		t.Errorf("Stamp = %+v, %v", st, err)
	}

	uninstaller := filepath.Join(t.TempDir(), "uninstall.exe")
	if err := in.WriteUninstaller(uninstaller); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, uninstaller); got != stubBytes {
		t.Errorf("the uninstaller is %q, want the program alone", got)
	}
	if _, err := Open(uninstaller); !errors.Is(err, ErrNoPayload) {
		t.Errorf("opening the uninstaller: %v, want ErrNoPayload", err)
	}
}

func TestADamagedInstallerIsRefused(t *testing.T) {
	path := makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Lop off the middle of the zip, leaving the trailer claiming it is all there.
	cut := append(append([]byte(nil), data[:len(stubBytes)+10]...), data[len(data)-trailerSize:]...)
	damaged := filepath.Join(t.TempDir(), "damaged.exe")
	if err := os.WriteFile(damaged, cut, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(damaged); err == nil || errors.Is(err, ErrNoPayload) {
		t.Errorf("a truncated installer opened: %v", err)
	}
}

// A payload is only written by Write, but an installer is a downloaded file,
// and one that could write outside its own folder is not a risk worth taking.
func TestAPayloadCannotWriteOutsideTheInstall(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(stubBytes)
	start := int64(buf.Len())
	cw := &countingWriter{w: &buf}
	zw := zip.NewWriter(cw)
	w, _ := zw.Create("../escaped.txt")
	w.Write([]byte("nope"))
	zw.Close()
	writeTrailer(&buf, start, cw.n)

	path := filepath.Join(t.TempDir(), "evil.exe")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	in := openInstaller(t, path)
	dir := filepath.Join(t.TempDir(), "install")
	if _, err := in.Extract(dir); err == nil {
		t.Error("an entry named ../escaped.txt was extracted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped.txt")); err == nil {
		t.Error("a file was written outside the install folder")
	}
}

func TestAFreshInstall(t *testing.T) {
	h := newHost(t)
	in := openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp")))
	if err := Install(h, in, Options{}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(h.LocalAppData(), "Programs", "Test Game")
	if got := readFile(t, filepath.Join(dir, "levels", "intro.fbsp")); got != "a level" {
		t.Errorf("installed level = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, UninstallerName)); got != stubBytes {
		t.Error("the uninstaller is not the program")
	}
	app := h.reg[`Software\Test Game`]
	if app["InstallDir"].Str != dir || app["Version"].Str != "1.0.0" {
		t.Errorf("app key = %+v", app)
	}
	un := h.reg[`Software\Microsoft\Windows\CurrentVersion\Uninstall\Test Game`]
	if un["DisplayName"].Str != "Test Game" || un["UninstallString"].Str != `"`+filepath.Join(dir, UninstallerName)+`"` ||
		!un["NoModify"].IsDWORD || un["DisplayIcon"].Str != filepath.Join(dir, "test-game.exe") {
		t.Errorf("uninstall key = %+v", un)
	}
	for _, link := range []string{
		filepath.Join(h.root, "Start Menu", "Programs", "Test Game", "Test Game.lnk"),
		filepath.Join(h.root, "Start Menu", "Programs", "Test Game", "Uninstall Test Game.lnk"),
		filepath.Join(h.root, "Desktop", "Test Game.lnk"),
	} {
		if _, ok := h.links[link]; !ok {
			t.Errorf("no shortcut %s (have %v)", link, h.links)
		}
	}
	if h.launch != filepath.Join(dir, "test-game.exe") {
		t.Errorf("launched %q after installing", h.launch)
	}
}

// An upgrade goes where the old install is, however it got there, and
// replaces it whole: a file the old version had and the new one does not must
// not survive to be found by the game.
func TestAnUpgradeReplacesTheOldInstallWhereItIs(t *testing.T) {
	h := newHost(t)
	custom := filepath.Join(h.root, "Games", "TG")
	first := openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("old.fbsp")))
	if err := Install(h, first, Options{Silent: true, Dir: custom}); err != nil {
		t.Fatal(err)
	}

	second := openInstaller(t, makeInstaller(t, "Test Game", "2.0.0", gameFiles("new.fbsp")))
	h.asked = nil
	if err := Install(h, second, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(h.asked) == 0 || !strings.Contains(h.asked[0], "from version 1.0.0 to version 2.0.0") {
		t.Errorf("the first question was %q", h.asked)
	}
	for _, q := range h.asked {
		if strings.Contains(q, "desktop") {
			t.Error("an upgrade asked about the desktop shortcut again")
		}
	}
	if _, err := os.Stat(filepath.Join(custom, "levels", "new.fbsp")); err != nil {
		t.Error("the upgrade did not go where the old install was")
	}
	if _, err := os.Stat(filepath.Join(custom, "levels", "old.fbsp")); err == nil {
		t.Error("a file only the old version had survived the upgrade")
	}
	if v := h.reg[`Software\Test Game`]["Version"].Str; v != "2.0.0" {
		t.Errorf("recorded version = %q", v)
	}
}

func TestSetupWillNotInstallOverSomeoneElsesFiles(t *testing.T) {
	h := newHost(t)
	dir := filepath.Join(h.LocalAppData(), "Programs", "Test Game")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thesis.docx"), []byte("years of work"), 0644); err != nil {
		t.Fatal(err)
	}
	in := openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp")))
	if err := Install(h, in, Options{Silent: true}); err == nil {
		t.Error("installed into a folder of somebody else's files")
	}
	if got := readFile(t, filepath.Join(dir, "thesis.docx")); got != "years of work" {
		t.Error("the files already in the folder were touched")
	}
}

func TestARunningGameIsWaitedForOrRefused(t *testing.T) {
	h := newHost(t)
	in := openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp")))
	if err := Install(h, in, Options{Silent: true}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.LocalAppData(), "Programs", "Test Game")

	h.busy = 1
	if err := Install(h, in, Options{Silent: true}); !errors.Is(err, ErrRunning) {
		t.Errorf("a silent install over a running game: %v, want ErrRunning", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-game.exe")); err != nil {
		t.Error("a refused install still removed the running game")
	}

	h.busy, h.retry = 2, true
	if err := Install(h, in, Options{}); err != nil {
		t.Errorf("an install that waited for the game: %v", err)
	}
	if h.retried != 2 {
		t.Errorf("asked to retry %d times, want 2", h.retried)
	}

	h.busy, h.retry = 1, false
	if err := Install(h, in, Options{}); !errors.Is(err, ErrCancelled) {
		t.Errorf("cancelling the wait: %v, want ErrCancelled", err)
	}
}

func TestSayingNoInstallsNothing(t *testing.T) {
	h := newHost(t)
	h.ask = func(string) bool { return false }
	in := openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp")))
	if err := Install(h, in, Options{}); !errors.Is(err, ErrCancelled) {
		t.Errorf("Install = %v, want ErrCancelled", err)
	}
	if _, err := os.Stat(filepath.Join(h.LocalAppData(), "Programs", "Test Game")); err == nil {
		t.Error("a cancelled install made its folder")
	}
	if len(h.reg) != 0 {
		t.Errorf("a cancelled install wrote the registry: %v", h.reg)
	}
}

func TestUninstallTakesEverythingButTheSaves(t *testing.T) {
	h := newHost(t)
	in := openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp")))
	saves := filepath.Join(h.AppData(), "Test Game")
	install := func() string {
		if err := Install(h, in, Options{Silent: true, Desktop: true}); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(saves, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(saves, "save1.dat"), []byte("progress"), 0644); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(h.LocalAppData(), "Programs", "Test Game")
	}

	dir := install()
	h.ask = func(q string) bool { return !strings.Contains(q, "saves") }
	if err := Uninstall(h, dir, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		dir,
		filepath.Join(h.root, "Start Menu", "Programs", "Test Game"),
		filepath.Join(h.root, "Desktop", "Test Game.lnk"),
	} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s survived the uninstall", gone)
		}
	}
	if len(h.reg) != 0 {
		t.Errorf("registry after uninstalling: %v", h.reg)
	}
	if _, err := os.Stat(filepath.Join(saves, "save1.dat")); err != nil {
		t.Error("the saves were deleted though the player said no")
	}

	dir = install()
	if err := Uninstall(h, dir, Options{Silent: true, DeleteSaves: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(saves); err == nil {
		t.Error("-delete-saves kept the saves")
	}
}

func TestUninstallRefusesAFolderSetupDidNotMake(t *testing.T) {
	h := newHost(t)
	dir := filepath.Join(h.root, "Documents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "letter.txt"), []byte("dear"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(h, dir, Options{Silent: true}); err == nil {
		t.Error("uninstalled a folder with no stamp")
	}
	if _, err := os.Stat(filepath.Join(dir, "letter.txt")); err != nil {
		t.Error("files in a folder Setup did not make were deleted")
	}
}

func TestParseStamp(t *testing.T) {
	st, err := ParseStamp([]byte("# Written by foundrypack.\nTITLE='Bob'\\''s Quest'\nVERSION='1.0'\nEXE='bobs-quest.exe'\nDATA_DIR='Bob'\\''s Quest'\n"))
	if err != nil || st.Title != "Bob's Quest" || st.DataDir != "Bob's Quest" || st.Exe != "bobs-quest.exe" {
		t.Errorf("ParseStamp = %+v, %v", st, err)
	}
	for _, bad := range []string{
		"TITLE='x'\nEXE='..\\evil.exe'\n",
		"TITLE='x'\nEXE='game.exe'\nDATA_DIR='..'\n",
		"TITLE='x'\nEXE='game.exe'\nDATA_DIR='a/b'\n",
		"TITLE='x'\n",
		"TITLE='x\nEXE='game.exe'\n",
	} {
		if _, err := ParseStamp([]byte(bad)); err == nil {
			t.Errorf("ParseStamp(%q) accepted it", bad)
		}
	}
}
