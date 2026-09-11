package game

// manifest.go — game.yaml, the file that turns the engine into a particular
// game.
//
// A development build boots into the 3D editor on a blank level, because that
// is what an engine with no project loaded should do. An installed build has
// no editor to boot into and no author to pick a level, so something in the
// install's resources/ has to say which level starts, what the window is called
// and where the player's settings go. That is this file.
//
// It is read by both builds and acted on by the release one (see
// internal/app/startup_release.go). Keeping the parse in a package of its own,
// below internal/app and importing nothing but fileutil, is what lets it be
// tested without a window — and what lets cmd/foundrypack read the same
// manifest the game will, rather than a second parser that agrees with this one
// until it doesn't.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"

	"gopkg.in/yaml.v2"
)

// FileName is the manifest's name in the resources directory.
const FileName = "game.yaml"

// Window is the manifest's window block: what the game opens as.
//
// Zero fields mean "leave the engine's default alone" rather than "zero", so a
// manifest that only names a start level does not open a 0x0 window.
type Window struct {
	Width      int32 `yaml:"width,omitempty"`
	Height     int32 `yaml:"height,omitempty"`
	Fullscreen bool  `yaml:"fullscreen,omitempty"`
}

// Manifest is game.yaml.
type Manifest struct {
	// Title names the window, the installer, and — unless DataDir overrides it
	// — the per-user directory settings and saves are written to.
	Title   string `yaml:"title"`
	Version string `yaml:"version,omitempty"`

	// DataDir overrides the per-user data directory's name. Worth setting when
	// the title contains punctuation, or when it may change between releases:
	// renaming the title should not silently orphan everyone's settings.
	DataDir string `yaml:"data_dir,omitempty"`

	// StartLevel is the level, by name, that the game boots into. Required —
	// there is nothing else for a release build to do.
	StartLevel string `yaml:"start_level"`

	Window Window `yaml:"window,omitempty"`
}

// Default is what a manifest field means when the file does not set it.
//
// Title is deliberately not among them. It is the game's name, and it reaches
// the window caption, the installer and the per-user data directory — shipping
// somebody's game under a placeholder because they left a line out is a worse
// outcome than refusing to boot and saying so.
func Default() Manifest {
	return Manifest{Version: "0.0.0"}
}

// Path is the manifest's location in the running engine's resources directory.
func Path() string { return filepath.Join(fileutil.ResourcesPath, FileName) }

// ProjectPath is the manifest's location in the project at root: the directory
// an editor runs from, which holds resources/ rather than being it.
func ProjectPath(root string) string {
	return filepath.Join(fileutil.ResourcesDir(root), FileName)
}

// Load reads and validates a manifest.
func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return Parse(data)
}

// Parse is Load without the file, which is what the tests and the packer use.
//
// Unmarshalling onto Default() rather than onto the zero value is what makes an
// omitted field mean "the default" — yaml.v2 leaves a key it did not see
// alone, so the defaults survive and only what the file actually says is
// overwritten.
func Parse(data []byte) (Manifest, error) {
	m := Default()
	if err := yaml.UnmarshalStrict(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", FileName, err)
	}
	m.normalize()
	return m, m.Validate()
}

// normalize trims the fields that end up in paths and window titles, so a
// trailing space in the YAML does not become a directory name nobody can find.
func (m *Manifest) normalize() {
	m.Title = strings.TrimSpace(m.Title)
	m.Version = strings.TrimSpace(m.Version)
	m.DataDir = strings.TrimSpace(m.DataDir)
	m.StartLevel = strings.TrimSpace(m.StartLevel)
}

// Validate reports what would make this manifest unusable at boot.
//
// Only the two things that cannot be defaulted: a game with no start level has
// nothing to show, and one with no title has no window caption and no data
// directory. Everything else has a sensible answer when it is missing, and a
// manifest should not be rejected for leaving it out.
func (m Manifest) Validate() error {
	if m.StartLevel == "" {
		return fmt.Errorf("%s: start_level is required", FileName)
	}
	if m.Title == "" {
		return fmt.Errorf("%s: title is required", FileName)
	}
	// A start level is a name, resolved under levels/ — not a path. Catching it
	// here says so, rather than letting it become a file-not-found on a path
	// the author never wrote.
	if strings.ContainsAny(m.StartLevel, `/\`) {
		return fmt.Errorf("%s: start_level %q must be a level name, not a path", FileName, m.StartLevel)
	}
	return nil
}

// DataDirName is the per-user directory this game's settings and saves belong
// in, as a single path element.
func (m Manifest) DataDirName() string {
	if m.DataDir != "" {
		return fileutil.SanitizeDirName(m.DataDir)
	}
	return fileutil.SanitizeDirName(m.Title)
}

// WindowTitle is what the window is captioned. The version is appended when
// there is one, because "which build am I looking at" is the first question
// asked of every bug report.
func (m Manifest) WindowTitle() string {
	if m.Version == "" {
		return m.Title
	}
	return m.Title + " " + m.Version
}
