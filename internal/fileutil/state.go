package fileutil

// state.go — where the game *writes*, as opposed to where it reads.
//
// Every asset path in the engine resolves against ResourcesPath, the install's
// resources/ folder, which is exactly right for content: the install directory
// is read-only as far as the game is concerned, and a level and its textures
// ship together. Mutable state is the other half of that sentence and had been
// sharing the same answer — foundry.log and audio.yaml were both written beside
// the executable, which works in dist/ and fails the moment the game is
// installed under Program Files or /opt, where the user running it cannot
// write.
//
// So writes go through StatePath instead. UserDataRoot is empty in a
// development build, which makes StatePath identical to what it replaced and
// leaves the editor's save-beside-the-exe workflow untouched; a release build
// points it at the per-user data directory during startup. Reads go through
// ReadState, which falls back to resources/ so a shipped default (an
// audio.yaml a project wants to ship at a particular mix, say) is still picked
// up on a first run when nothing has been written yet.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// UserDataRoot is the directory per-user mutable state is written to. Empty
// means "write in the project" (beside the executable, in a checkout), which is
// the development default.
//
// Set through UseUserDataDir rather than directly, so the directory is known to
// exist by the time anything tries to write into it.
var UserDataRoot string

// StateDir is the directory writes land in.
func StateDir() string {
	if UserDataRoot != "" {
		return UserDataRoot
	}
	if ProjectPath != "" {
		return ProjectPath
	}
	return ExecutablePath
}

// StatePath resolves the name of a state file to somewhere writable.
func StatePath(name string) string {
	return filepath.Join(StateDir(), filepath.FromSlash(name))
}

// ReadState reads a state file, falling back to a copy shipped in resources/
// when the user has none of their own yet.
//
// The fallback is what makes a shipped default work: a project that wants to
// launch at a particular volume, or with a particular set of detail presets,
// puts the file in its resources/ — which is what a pack ships — and the first
// write moves the user's own copy to the data directory without ever touching
// the original.
func ReadState(name string) ([]byte, error) {
	data, err := os.ReadFile(StatePath(name))
	if err == nil || UserDataRoot == "" {
		return data, err
	}
	return os.ReadFile(filepath.Join(ResourcesPath, filepath.FromSlash(name)))
}

// UseUserDataDir points state writes at the per-user data directory for app,
// creating it if necessary, and returns the directory chosen.
//
// A failure is returned rather than fatal: a game that cannot find a home for
// its settings should still run, writing beside the executable exactly as a
// development build does. The caller logs it.
func UseUserDataDir(app string) (string, error) {
	dir, err := UserDataDir(app)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	UserDataRoot = dir
	return dir, nil
}

// UserDataDir returns the conventional per-user data directory for app on this
// platform, without creating it.
//
// Keyed off runtime.GOOS rather than build tags because there is nothing
// platform-specific in the code itself — only in the answer.
func UserDataDir(app string) (string, error) {
	name := SanitizeDirName(app)
	if name == "" {
		name = "Foundry"
	}

	switch runtime.GOOS {
	case "windows":
		// APPDATA is the roaming profile, which is where a game's settings and
		// saves are expected to live and what a backup or a roaming profile
		// actually carries with the user.
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, name), nil
		}
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", name), nil
	default:
		if base := os.Getenv("XDG_DATA_HOME"); base != "" {
			return filepath.Join(base, name), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", name), nil
	}

	// Windows with no APPDATA, which happens under a stripped service account.
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, name), nil
}

// SanitizeDirName turns a human title into something safe to use as a single
// directory name on every platform the engine builds for.
//
// A game's title is author-supplied and goes straight into a path, so the
// separators and the Windows-reserved characters have to come out — otherwise
// a title with a slash in it silently creates a nested directory, and one with
// a colon fails outright on Windows for reasons the author would have no way
// to guess at.
func SanitizeDirName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r < 0x20, r == 0x7f:
			// Control characters: dropped.
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	// Trailing dots and spaces are legal to write and impossible to open on
	// Windows, so they come off rather than being replaced.
	return strings.TrimRight(b.String(), ". ")
}
