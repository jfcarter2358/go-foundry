package claude

// locate.go — finding the Claude Code CLI, and checking it is new enough.
//
// The panel does not implement an agent. It runs the `claude` binary the same
// way the VS Code extension does, and talks to it over stdin/stdout. So the
// first thing that has to work is finding that binary, and it is in a different
// place on nearly every machine: the native installer puts it in ~/.claude/local,
// npm puts it on PATH, and someone who only ever used the VS Code extension has
// it buried in the extension folder and nowhere else — which is exactly the case
// on the machine this was written on. All of those are tried, in that order,
// before we tell anyone to go and install something.
//
// A version check comes with it. The control protocol the panel depends on —
// permission prompts over stdio — is not in old builds, and a CLI that silently
// denies every write is far harder to understand than one that says up front
// that it is too old.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// MinVersion is the oldest CLI the panel will drive. 2.0 is where
// `--permission-prompt-tool stdio` and the can_use_tool control request settled
// into the shape session.go expects.
var MinVersion = [3]int{2, 0, 0}

// versionTimeout bounds `claude --version`. It is a local process doing nothing
// but printing a string; if it has not answered by now it never will.
const versionTimeout = 10 * time.Second

// ErrNotFound is returned by Locate when no candidate exists.
type ErrNotFound struct {
	Tried []string
}

func (e *ErrNotFound) Error() string {
	return "the Claude Code CLI was not found. Install it from https://claude.com/claude-code, " +
		"or set its path in the panel's settings. Looked in: " + strings.Join(e.Tried, ", ")
}

// Locate finds the CLI. configured wins when it is set and exists, so a project
// pinned to a particular build never has the search second-guess it.
func Locate(configured string) (string, error) {
	var tried []string
	for _, c := range Candidates(configured) {
		tried = append(tried, c)
		if isExecutableFile(c) {
			return c, nil
		}
	}
	// Only after the fixed paths: PATH is the common case, but exec.LookPath is
	// the one lookup that can be slow (it stats every entry), and on Windows it
	// also has to try each PATHEXT.
	for _, name := range binaryNames() {
		tried = append(tried, name+" (on PATH)")
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", &ErrNotFound{Tried: tried}
}

// Candidates is every fixed path tried, in order. Exported so the panel can show
// the list when it finds nothing, and so a test can check the order without a
// binary anywhere.
func Candidates(configured string) []string {
	var out []string
	if configured != "" {
		out = append(out, configured)
	}
	if env := os.Getenv("FOUNDRY_CLAUDE_CLI"); env != "" {
		out = append(out, env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	for _, name := range binaryNames() {
		out = append(out,
			filepath.Join(home, ".claude", "local", name),
			filepath.Join(home, ".local", "bin", name),
		)
	}
	if runtime.GOOS == "windows" {
		out = append(out,
			filepath.Join(home, "AppData", "Local", "Programs", "claude", "claude.exe"),
			filepath.Join(home, "AppData", "Roaming", "npm", "claude.cmd"),
		)
	} else {
		out = append(out, "/usr/local/bin/claude", "/opt/homebrew/bin/claude")
	}
	out = append(out, vscodeBundled(home)...)
	return out
}

// vscodeBundled finds the copy the VS Code extension ships. The extension folder
// is versioned — anthropic.claude-code-2.1.278-linux-x64 — so this globs, and
// sorts so the newest sorts last and is tried first.
func vscodeBundled(home string) []string {
	var out []string
	roots := []string{
		filepath.Join(home, ".vscode-server", "extensions"),
		filepath.Join(home, ".vscode", "extensions"),
		filepath.Join(home, ".vscode-insiders", "extensions"),
		filepath.Join(home, ".cursor", "extensions"),
	}
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "anthropic.claude-code-*"))
		if err != nil {
			continue
		}
		// Glob returns them sorted, so the newest version is at the end.
		for i := len(matches) - 1; i >= 0; i-- {
			for _, name := range binaryNames() {
				out = append(out, filepath.Join(matches[i], "resources", "native-binary", name))
			}
		}
	}
	return out
}

func binaryNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"claude.exe", "claude.cmd"}
	}
	return []string{"claude"}
}

// isExecutableFile reports whether p is a file we could run. On Windows the
// permission bits mean nothing, so existing is enough.
func isExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

// Version runs `bin --version` and returns what it printed, trimmed.
func Version(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run %s --version: %w", bin, err)
	}
	// It prints "2.1.278 (Claude Code)"; the first line is all we want.
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	if sc.Scan() {
		return strings.TrimSpace(sc.Text()), nil
	}
	return "", fmt.Errorf("%s --version printed nothing", bin)
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion pulls the semver out of a --version line. The rest of the line is
// decoration and has changed before.
func ParseVersion(s string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// CheckVersion reports whether the CLI at bin is new enough, and what it said.
// An unparseable version is accepted: a build whose banner we do not recognise
// is far more likely to be newer than older, and refusing to run it would be a
// worse failure than letting it try.
func CheckVersion(bin string) (string, error) {
	line, err := Version(bin)
	if err != nil {
		return "", err
	}
	v, ok := ParseVersion(line)
	if !ok {
		return line, nil
	}
	if less(v, MinVersion) {
		return line, fmt.Errorf("Claude Code %s is too old for the editor panel — %d.%d.%d or newer is needed. Run `claude update`",
			line, MinVersion[0], MinVersion[1], MinVersion[2])
	}
	return line, nil
}

func less(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
