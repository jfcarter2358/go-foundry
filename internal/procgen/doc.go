// Package procgen is the procedural content generators' core: the .fgen
// generator document, the parameter schema its stages declare, the registry
// of stage kinds, and the run engine that executes a document's stages
// incrementally, in parallel where they allow it, with progress, a log and
// cancellation.
//
// A generator is a document the user edits, previews and runs inside the
// editor (the Generator window, internal/editor/editor_generator_*.go), and
// that `foundry gen run` runs headless with exactly the same code. A document
// is a seed, an output directory, an optional target level, and an ordered
// list of stages; each stage is an instance of a registered StageKind with
// its own parameters. The stages are where all the knowledge lives — terrain
// and erosion, SDF rocks, scatter, textures — and each one is a file that
// calls RegisterStage from init. Nothing in this package knows what any
// stage does.
//
// Layering: this package is pure CPU Go and sits below internal/world and
// internal/gfx (internal/layering_test.go holds it there). The pieces that
// need more live beside it:
//
//   - procgen/noise: deterministic noise and RNG, below everything.
//   - procgen/meshout: meshes out — an ImportMesh builder, the GLB write and
//     the in-memory Nanite cook (it reaches internal/nanitecook, and so gfx).
//   - procgen/genlevel: placements into a level, by ID prefix (internal/world).
//   - procgen/stages: the built-in stage kinds, registered by import.
//   - procgen/gencli: `foundry gen`.
package procgen

// doc.go — the generator document.
//
//	doc: Desert canyon, red sandstone
//	seed: 1234
//	output: generated/desert_canyon      # under resources/; default generated/<name>
//	level: desert_canyon                 # optional: the level that receives placements
//	stages:
//	  - stage: terrain                   # a registered StageKind
//	    id: ground                       # unique; names the outputs and the refs
//	    params: {size: 1024, resolution: 0.5}
//	  - stage: scatter
//	    id: talus
//	    enabled: false
//	    params: {terrain: ground}        # a stage ref names an upstream id
//
// The document's NAME is its file's basename, never a key inside it, so a
// renamed file is a renamed generator — the same rule .fmat follows. The name
// is what generated level entries are prefixed with (gen.<name>.<stage>.),
// so renaming a generator orphans its old placements; the editor says so.

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Ext is a generator document's extension.
const Ext = ".fgen"

// GeneratorsDirName is where generator documents live under resources/.
const GeneratorsDirName = "generators"

// DefaultOutputRoot is the folder under resources/ an output directory
// defaults into: generated/<name>.
const DefaultOutputRoot = "generated"

// Doc is a generator document.
type Doc struct {
	// Name is the file's basename without the extension. Not stored in the
	// file.
	Name string
	// Doc is a note for whoever opens the generator next.
	Doc string
	// Seed seeds every stage (see StageSeed).
	Seed int64
	// Output is the output directory under resources/, slash-separated; empty
	// is generated/<Name>.
	Output string
	// Level is the level whose scenery: (and volumes:) receive the
	// generator's placements, by name as load_level spells it; empty places
	// nothing.
	Level string
	// Stages run in this order. A stage may refer only to stages above it.
	Stages []Stage
}

// Stage is one stage of a document.
type Stage struct {
	// Kind names the StageKind.
	Kind string
	// ID is unique within the document: it names the stage's output folder,
	// its placements' ID prefix and what downstream stage refs say.
	ID string
	// Disabled stages are kept but not run, and their placements are not
	// spliced.
	Disabled bool
	// Params are the values the document sets. Missing ones read their
	// defaults (Resolve); the file writes only these.
	Params Params
	// Raw holds a stage of a kind this build does not know, verbatim, so
	// opening a document from a newer build and saving it loses nothing.
	// Validate reports such a stage and Run refuses the document.
	Raw map[string]interface{}
}

// Enabled reports whether the stage runs.
func (s *Stage) Enabled() bool { return !s.Disabled }

// OutputDir is the output directory under resources/, slash-separated.
func (d *Doc) OutputDir() string {
	if d.Output != "" {
		return strings.Trim(path.Clean(strings.ReplaceAll(d.Output, "\\", "/")), "/")
	}
	return DefaultOutputRoot + "/" + d.Name
}

// StageByID is the stage with the given ID, or nil.
func (d *Doc) StageByID(id string) *Stage {
	for i := range d.Stages {
		if d.Stages[i].ID == id {
			return &d.Stages[i]
		}
	}
	return nil
}

// StageIndex is the index of the stage with the given ID, or -1.
func (d *Doc) StageIndex(id string) int {
	for i := range d.Stages {
		if d.Stages[i].ID == id {
			return i
		}
	}
	return -1
}

// Clone is a deep copy, for undo snapshots and for handing a run its own
// document while the editor goes on editing.
func (d *Doc) Clone() *Doc {
	if d == nil {
		return nil
	}
	out := *d
	out.Stages = make([]Stage, len(d.Stages))
	for i, s := range d.Stages {
		s.Params = s.Params.Clone()
		if s.Raw != nil {
			s.Raw = cloneRaw(s.Raw).(map[string]interface{})
		}
		out.Stages[i] = s
	}
	return &out
}

func cloneRaw(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		m := make(map[string]interface{}, len(t))
		for k, x := range t {
			m[k] = cloneRaw(x)
		}
		return m
	case map[interface{}]interface{}:
		m := make(map[interface{}]interface{}, len(t))
		for k, x := range t {
			m[k] = cloneRaw(x)
		}
		return m
	case []interface{}:
		s := make([]interface{}, len(t))
		for i, x := range t {
			s[i] = cloneRaw(x)
		}
		return s
	}
	return v
}

// stageIDPattern is what a stage ID may be: it is a folder name and a
// segment of a level object's ID, so lower-case letters, digits and
// underscores, starting with a letter.
var stageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidStageID reports whether id is a legal stage ID.
func ValidStageID(id string) bool { return stageIDPattern.MatchString(id) }

// ValidDocName reports whether name is a legal generator name: the same rule
// as a stage ID, since it is a segment of every generated ID too.
func ValidDocName(name string) bool { return stageIDPattern.MatchString(name) }

// NewStage is a stage of kind k with its parameters at their defaults (and
// so absent from the file), under an ID that is unique in d: the kind's name,
// numbered if that is taken.
func (d *Doc) NewStage(kind string) Stage {
	return Stage{Kind: kind, ID: d.UniqueStageID(kind), Params: Params{}}
}

// UniqueStageID is base, or base_2, base_3... — the first no stage uses.
func (d *Doc) UniqueStageID(base string) string {
	base = strings.ToLower(base)
	if !ValidStageID(base) {
		base = "stage"
	}
	if d.StageByID(base) == nil {
		return base
	}
	for n := 2; ; n++ {
		id := base + "_" + strconv.Itoa(n)
		if d.StageByID(id) == nil {
			return id
		}
	}
}

// IDPrefix is the prefix every level entry the generator named name places
// carries: "gen.<name>.". A splice removes everything with it before adding
// the run's placements, so a rerun replaces exactly its own entries and
// leaves hand-made ones alone.
func IDPrefix(name string) string { return "gen." + name + "." }

// StagePrefix is IDPrefix narrowed to one stage: "gen.<name>.<stage>.".
func StagePrefix(name, stage string) string { return IDPrefix(name) + stage + "." }
