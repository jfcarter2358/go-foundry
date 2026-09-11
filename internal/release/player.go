package release

// player.go — the game binary a release wraps its content around.
//
// A release is a release-tagged build of the engine, with no editor in it, and
// the project's content beside it. The content is the project's; the binary is
// the engine's, and it is the same binary for every game — game.yaml, read at
// boot, is what makes it a particular one. So nothing about a release needs the
// binary to be compiled for this project, only for this engine and this
// platform, which is what lets it come from either of two places:
//
//   - runtimes/, in the project directory beside the editor. `make runtimes`
//     builds one per platform from the same checkout the editor was built
//     from. The normal case for an editor running from a game's own project,
//     which has no Foundry source in it and may have no Go toolchain — and on
//     Windows almost certainly has no C cross-compiler.
//   - A Foundry checkout, built on the spot with `go build -tags release`. Used
//     when one is configured, or when the project directory is inside one (the
//     engine's own repository, or dist/), which is what the Makefile does too.

import (
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

// RuntimesDirName is the folder prebuilt game binaries live in, in the project
// directory. Not a content directory, so nothing in it is ever packed.
const RuntimesDirName = "runtimes"

// PlayerFileName is what a prebuilt game binary for a platform is called in
// runtimes/, and what `make runtimes` names it.
func PlayerFileName(p Platform) string {
	return "foundry-release-" + p.String() + p.ExeExt()
}

// PrebuiltPlayer is where a platform's prebuilt game binary is expected.
func PrebuiltPlayer(root string, p Platform) string {
	return filepath.Join(root, RuntimesDirName, PlayerFileName(p))
}

// FindSource returns the Foundry checkout to build the game binary from, or ""
// to use the prebuilt ones.
//
// A configured directory has to be a checkout, and saying so beats quietly
// falling back to runtimes/ and shipping whatever happens to be in there. With
// nothing configured, the project directory and each directory above it is
// tried in turn.
func FindSource(root, configured string) (string, error) {
	if configured != "" {
		dir := configured
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		if !IsFoundrySource(dir) {
			return "", fmt.Errorf("%s is not a Foundry checkout: no go.mod for module foundry with cmd/foundry beside it", dir)
		}
		return dir, nil
	}
	dir, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	for {
		if IsFoundrySource(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// IsFoundrySource reports whether dir is the root of a Foundry checkout.
func IsFoundrySource(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil || modulePath(data) != "foundry" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "cmd", "foundry"))
	return err == nil && info.IsDir()
}

// modulePath is the module a go.mod declares.
func modulePath(gomod []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(gomod))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

// BuildPlayer compiles the release game binary for a platform from a Foundry
// checkout, with the flags the Makefile's release targets use.
func BuildPlayer(ctx context.Context, src string, p Platform, out string, w io.Writer) error {
	goTool, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("building the game binary from %s needs the Go toolchain on PATH (%v) — or leave the Foundry source empty and use runtimes/", src, err)
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0755); err != nil {
		return err
	}

	// -s -w drop the symbol table and DWARF. -H windowsgui keeps a console
	// window from opening behind the game; the engine logs to a file instead.
	ldflags := "-s -w"
	if p.GOOS == "windows" {
		ldflags += " -H windowsgui"
	}
	cmd := exec.CommandContext(ctx, goTool, "build", "-tags", "release", "-ldflags", ldflags, "-o", absOut, "./cmd/foundry")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOOS="+p.GOOS, "GOARCH="+p.GOARCH, "CGO_ENABLED=1")
	if p.GOOS == "windows" && runtime.GOOS != "windows" {
		// The mingw cross-compiler the Makefile names, unless the environment
		// already says otherwise.
		for _, kv := range [][2]string{{"CC", "x86_64-w64-mingw32-gcc"}, {"CXX", "x86_64-w64-mingw32-g++"}} {
			if os.Getenv(kv[0]) == "" {
				cmd.Env = append(cmd.Env, kv[0]+"="+kv[1])
			}
		}
	}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		if p.GOOS != runtime.GOOS {
			return fmt.Errorf("go build for %s: %w — the engine uses cgo, so cross-building needs a C compiler for %s; build it where there is one (make runtimes) and put it in runtimes/", p, err, p.GOOS)
		}
		return fmt.Errorf("go build for %s: %w", p, err)
	}
	return nil
}

// SetupStubFileName is the Windows installer program in runtimes/: the program
// a Setup.exe is, before a game is appended to it (internal/setup).
const SetupStubFileName = "foundry-setup-windows-amd64.exe"

// setupStubPackage is the main package the installer program is built from.
const setupStubPackage = "foundry/cmd/foundrysetup"

// PrebuiltSetupStub is where the prebuilt Windows installer program is
// expected.
func PrebuiltSetupStub(root string) string {
	return filepath.Join(root, RuntimesDirName, SetupStubFileName)
}

// BuildSetupStub compiles the Windows installer program from a Foundry
// checkout. Pure Go, so unlike the game binary it builds for Windows from any
// machine with no C compiler.
func BuildSetupStub(ctx context.Context, src, out string, w io.Writer) error {
	goTool, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("building the installer from %s needs the Go toolchain on PATH (%v)", src, err)
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0755); err != nil {
		return err
	}
	// -H windowsgui: an installer opens message boxes, not a console.
	cmd := exec.CommandContext(ctx, goTool, "build", "-ldflags", "-s -w -H windowsgui", "-o", absOut, "./cmd/foundrysetup")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build of the installer: %w", err)
	}
	return nil
}

// CheckSetupStub confirms a binary is the Windows installer program, read off
// its build info rather than trusted from its name: appending a game to the
// wrong program makes a Setup.exe that runs something else entirely.
func CheckSetupStub(path string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s is not a Go program: %w", path, err)
	}
	if info.Path != setupStubPackage {
		return fmt.Errorf("%s is %s, not the Foundry installer — rebuild runtimes/ with make runtimes", path, info.Path)
	}
	for _, s := range info.Settings {
		if s.Key == "GOOS" && s.Value != "windows" {
			return fmt.Errorf("%s is built for %s, not windows", path, s.Value)
		}
	}
	return nil
}

// CheckPlayer confirms a binary is fit to ship as the game for a platform, and
// returns a warning worth printing about it, if there is one.
//
// It has to be a release build. A development build would boot — and ship the
// editor, which is the one outcome the release tag exists to rule out (see
// internal/app/devtools_release.go) — so a development binary copied into
// runtimes/ by mistake is refused here rather than packed. Go records the build
// tags and target in every binary it links, so this is read off the file
// rather than trusted from its name.
func CheckPlayer(path string, p Platform) (warning string, err error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s is not a Go program: %w", path, err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if !hasBuildTag(settings["-tags"], "release") {
		return "", fmt.Errorf("%s is a development build, with the editor in it — build it with -tags release (make runtimes)", path)
	}
	if goos := settings["GOOS"]; goos != "" && goos != p.GOOS {
		return "", fmt.Errorf("%s is built for %s, not %s", path, goos, p.GOOS)
	}
	if goarch := settings["GOARCH"]; goarch != "" && goarch != p.GOARCH {
		return "", fmt.Errorf("%s is built for %s, not %s", path, goarch, p.GOARCH)
	}
	return revisionWarning(settings["vcs.revision"]), nil
}

// revisionWarning compares the commit a game binary was built from with the
// one this program was.
//
// A warning and not a refusal: the levels a release ships are compiled by this
// program, and the game binary refuses a level compiled by a different version
// of the compiler, with a message that says so — but most commits do not touch
// the format, and a dirty working tree reports the same revision on both sides
// anyway. So it is said, once, where the author will see it.
func revisionWarning(player string) string {
	own, ok := debug.ReadBuildInfo()
	if !ok || player == "" {
		return ""
	}
	var mine string
	for _, s := range own.Settings {
		if s.Key == "vcs.revision" {
			mine = s.Value
		}
	}
	if mine == "" || mine == player {
		return ""
	}
	return fmt.Sprintf("the game binary was built from Foundry %s and this editor from %s; if the level format changed in between, the game will refuse its levels — rebuild runtimes/ with make runtimes",
		shortRevision(player), shortRevision(mine))
}

func shortRevision(r string) string {
	if len(r) > 12 {
		return r[:12]
	}
	return r
}

// hasBuildTag reports whether a -tags setting names tag. Go writes the list
// comma-separated; older toolchains accepted spaces.
func hasBuildTag(tags, tag string) bool {
	for _, t := range strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ' ' }) {
		if t == tag {
			return true
		}
	}
	return false
}
