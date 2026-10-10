package release

// config.go — what a release is built for, and where it goes.
//
// Kept in release.yaml in the project directory, beside resources/ rather than
// in it. resources/game.yaml says what the game IS and ships inside it;
// release.yaml says how this project
// is built and never ships (internal/pack keeps it out by name). The Release
// window writes it every time it starts a build, so `foundry build-release`
// from a terminal builds what the window last did.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v2"
)

// ConfigFileName is release.yaml's name in the project directory.
const ConfigFileName = "release.yaml"

// Platform is a target a release can be built for.
type Platform struct {
	Name   string // what release.yaml and the command line call it
	Label  string // what the Release window calls it
	GOOS   string
	GOARCH string
}

// Platforms is every target the engine ships to, in display order.
var Platforms = []Platform{
	{Name: "linux", Label: "Linux", GOOS: "linux", GOARCH: "amd64"},
	{Name: "windows", Label: "Windows", GOOS: "windows", GOARCH: "amd64"},
}

// PlatformByName finds a platform by its release.yaml name.
func PlatformByName(name string) (Platform, bool) {
	for _, p := range Platforms {
		if p.Name == strings.ToLower(strings.TrimSpace(name)) {
			return p, true
		}
	}
	return Platform{}, false
}

// String is the platform as a GOOS-GOARCH pair, which is how every file built
// for it is suffixed.
func (p Platform) String() string { return p.GOOS + "-" + p.GOARCH }

// ExeExt is what an executable for the platform ends in.
func (p Platform) ExeExt() string {
	if p.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func platformNames() string {
	names := make([]string, len(Platforms))
	for i, p := range Platforms {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

// Config is release.yaml.
type Config struct {
	// Platforms is which targets to build, by name.
	Platforms []string `yaml:"platforms"`
	// Output is where the build goes, relative to the project directory unless
	// absolute.
	Output string `yaml:"output"`
	// Prune ships only the assets the game can reach from its manifest.
	Prune bool `yaml:"prune"`
	// Installers is no longer read: a release always builds its installers.
	// Kept so a release.yaml written when they were optional still loads.
	Installers bool `yaml:"installers,omitempty"`
	// AllowLeaks ships a level that leaks rather than failing the build.
	AllowLeaks bool `yaml:"allow_leaks"`
	// Source is a Foundry checkout to build the game binary from, instead of
	// using the prebuilt one in runtimes/. See player.go.
	Source string `yaml:"source,omitempty"`
}

// DefaultConfig is a release for the machine the editor is running on.
func DefaultConfig() Config {
	host := "linux"
	if runtime.GOOS == "windows" {
		host = "windows"
	}
	return Config{Platforms: []string{host}, Output: "build", Prune: true}
}

// LoadConfig reads a project's release.yaml. A project with none gets the
// defaults, which is not an error: most projects will never have written one.
func LoadConfig(root string) (Config, error) {
	c := DefaultConfig()
	data, err := os.ReadFile(filepath.Join(root, ConfigFileName))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	// Onto the defaults, so a key the file leaves out keeps its default.
	if err := yaml.UnmarshalStrict(data, &c); err != nil {
		return DefaultConfig(), fmt.Errorf("%s: %w", ConfigFileName, err)
	}
	return c, nil
}

// SaveConfig writes a project's release.yaml.
func SaveConfig(root string, c Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := "# release.yaml — how the editor's Release window builds this project.\n" +
		"# Not shipped. See docs/guide/20-shipping.md.\n"
	// A plain write rather than fileutil.WriteFileAtomic: this is rewritten on
	// every build, and a .bak of it beside the editor would be clutter in the
	// project for no file anybody edits by hand.
	return os.WriteFile(filepath.Join(root, ConfigFileName), append([]byte(header), data...), 0644)
}

// Targets resolves the configured platform names, in display order.
func (c Config) Targets() ([]Platform, error) {
	if len(c.Platforms) == 0 {
		return nil, errors.New("no platforms chosen")
	}
	want := map[string]bool{}
	for _, name := range c.Platforms {
		p, ok := PlatformByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown platform %q (want %s)", name, platformNames())
		}
		want[p.Name] = true
	}
	var out []Platform
	for _, p := range Platforms {
		if want[p.Name] {
			out = append(out, p)
		}
	}
	return out, nil
}

// OutputDir is where the build goes, as an absolute path under root unless
// Output already is one.
func (c Config) OutputDir(root string) string {
	if filepath.IsAbs(c.Output) {
		return c.Output
	}
	return filepath.Join(root, filepath.FromSlash(c.Output))
}

// Validate reports what would stop a build before it starts.
func (c Config) Validate() error {
	if _, err := c.Targets(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Output) == "" {
		return errors.New("no output directory")
	}
	return nil
}
