package procgen

// environments.go — environment presets: the documents a region grows.
//
// An environment ("desert_canyon", "savannah") is an ordinary .fgen document
// under resources/environments/<name>.fgen. Placed in a level generation
// doc's region (docs/regions/SPEC.md), its terrain stage is the region's
// field provider (its size ignored: the region supplies bounds), its
// terrain_material and polyhaven_textures stages the region's ground
// material, and its placement stages are expanded per region (regional.go).
//
// Built-in presets are registered in Go, beside the templates, and WRITTEN to
// a project only when no file of that name exists there (WriteEnvironment):
// once written, the file is the user's, and a newer build never overwrites
// their edits. Every reader goes through LoadEnvironment, which prefers the
// file and falls back to the registered document — so a project that never
// asked for the files still runs, with the build's own presets.
//
// Each environment also has an overlay colour: the 2D biome overlay of the
// regions stage and the level editor's region outlines paint it.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/procgen/noise"
)

// EnvironmentsDirName is where environment presets live under resources/.
const EnvironmentsDirName = "environments"

// Environment is a registered preset.
type Environment struct {
	// Name is the file's basename and what a region's `environment:` says.
	Name  string
	Label string
	Doc   string
	// Color is the overlay colour, 0..1 RGB.
	Color [3]float64
	// New builds the preset document (named Name).
	New func() *Doc
}

var environments = map[string]*Environment{}

// RegisterEnvironment adds a preset; a duplicate or unnamed one panics.
func RegisterEnvironment(e Environment) {
	if e.Name == "" || !ValidDocName(e.Name) || e.New == nil {
		panic("procgen: an environment needs a valid Name and New")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := environments[e.Name]; dup {
		panic("procgen: environment " + e.Name + " registered twice")
	}
	ee := e
	environments[e.Name] = &ee
}

// EnvironmentByName is a registered preset, or nil.
func EnvironmentByName(name string) *Environment {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return environments[name]
}

// Environments is every registered preset, by name.
func Environments() []*Environment {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Environment, 0, len(environments))
	for _, e := range environments {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EnvironmentsDir is resources/environments.
func EnvironmentsDir() string { return filepath.Join(fileutil.ResourcesPath, EnvironmentsDirName) }

// EnvironmentPath is a preset's file by name.
func EnvironmentPath(name string) string { return filepath.Join(EnvironmentsDir(), name+Ext) }

// EnvironmentNames is every preset there is: the registered ones and the
// files in resources/environments, sorted, without repeats.
func EnvironmentNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range Environments() {
		seen[e.Name] = true
		out = append(out, e.Name)
	}
	if ents, err := os.ReadDir(EnvironmentsDir()); err == nil {
		for _, ent := range ents {
			n := ent.Name()
			if ent.IsDir() || !strings.HasSuffix(n, Ext) {
				continue
			}
			n = strings.TrimSuffix(n, Ext)
			if ValidDocName(n) && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// LoadEnvironment is the named preset's document: the project's file when
// there is one, else the registered preset's. from says which ("environments/
// savannah.fgen" or "built-in savannah").
func LoadEnvironment(name string) (d *Doc, from string, err error) {
	if !ValidDocName(name) {
		return nil, "", fmt.Errorf("environment %q: a preset's name is lower-case letters, digits and underscores", name)
	}
	p := EnvironmentPath(name)
	if fileExists(p) {
		d, err := ReadDoc(p)
		if err != nil {
			return nil, "", err
		}
		return d, EnvironmentsDirName + "/" + name + Ext, nil
	}
	if e := EnvironmentByName(name); e != nil {
		d := e.New()
		d.Name = name
		return d, "built-in " + name, nil
	}
	return nil, "", fmt.Errorf("no environment %q (there are: %s)", name, strings.Join(EnvironmentNames(), ", "))
}

// LoadGenerator reads a region's generator override: a bare preset name
// (LoadEnvironment), or a resources-relative or absolute path to a .fgen.
func LoadGenerator(ref string) (*Doc, string, error) {
	ref = strings.TrimSpace(ref)
	if ValidDocName(ref) {
		return LoadEnvironment(ref)
	}
	p := ref
	if !filepath.IsAbs(p) {
		p = filepath.Join(fileutil.ResourcesPath, filepath.FromSlash(ref))
	}
	if !strings.HasSuffix(p, Ext) {
		p += Ext
	}
	d, err := ReadDoc(p)
	if err != nil {
		return nil, "", err
	}
	return d, ref, nil
}

// WriteEnvironment writes the registered preset to the project's
// resources/environments when — and only when — no file of that name is
// there. wrote is false for a file already present (left untouched).
func WriteEnvironment(name string) (path string, wrote bool, err error) {
	e := EnvironmentByName(name)
	if e == nil {
		return "", false, fmt.Errorf("no built-in environment %q", name)
	}
	path = EnvironmentPath(name)
	if fileExists(path) {
		return path, false, nil
	}
	d := e.New()
	d.Name = name
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, err
	}
	if err := WriteDoc(path, d); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// WriteEnvironments writes every registered preset that has no file yet,
// returning the paths it wrote.
func WriteEnvironments() ([]string, error) {
	var out []string
	for _, e := range Environments() {
		p, wrote, err := WriteEnvironment(e.Name)
		if err != nil {
			return out, err
		}
		if wrote {
			out = append(out, p)
		}
	}
	return out, nil
}

// EnvironmentColor is the preset's overlay colour; an unregistered name (a
// project's own preset) gets a stable colour hashed from its name.
func EnvironmentColor(name string) [3]float64 {
	if e := EnvironmentByName(name); e != nil {
		return e.Color
	}
	h := noise.HashString(name)
	hue := float64(h%3600) / 3600
	return hsv(hue, 0.55, 0.85)
}

// hsv is an HSV colour as RGB, all 0..1.
func hsv(h, s, v float64) [3]float64 {
	h = (h - math.Floor(h)) * 6
	i := math.Floor(h)
	f := h - i
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	switch int(i) {
	case 0:
		return [3]float64{v, t, p}
	case 1:
		return [3]float64{q, v, p}
	case 2:
		return [3]float64{p, v, t}
	case 3:
		return [3]float64{p, q, v}
	case 4:
		return [3]float64{t, p, v}
	}
	return [3]float64{v, p, q}
}
