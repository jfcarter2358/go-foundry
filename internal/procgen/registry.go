package procgen

// registry.go — the stage kinds, and the document templates.
//
// A StageKind is everything the engine and the editor know about one kind of
// stage: its schema and its hooks. Adding a stage to the engine is one file
// that builds a StageKind and calls RegisterStage from init — no switch
// statement anywhere else grows a case:
//
//   - Run produces the stage's outputs (files and placements) for a real run;
//   - Preview produces a fast, in-memory PreviewData for the editor's
//     viewports, and is optional;
//   - Load rebuilds the stage's in-memory product from its files, so a
//     downstream stage can read an upstream one the run engine skipped as
//     fresh. Optional; a stage whose consumers read its files directly needs
//     none;
//   - Validate adds the kind's own checks to Doc.Validate. Optional.
//
// Templates are the same idea for whole documents: File > New in the editor
// and `foundry gen new -template <name>` offer every registered one.

import (
	"fmt"
	"sort"
	"sync"
)

// StageKind is one kind of stage.
type StageKind struct {
	// Name is what a document's `stage:` key says.
	Name string
	// Label and Doc are the editor's heading and blurb, and the docs'.
	Label string
	Doc   string
	// Category groups kinds in the editor's Add menu ("Terrain", "Debug").
	Category string
	// Version is folded into every stage hash: bump it when Run's output
	// changes for the same parameters, and every document reruns the stage.
	Version int
	// Params is the schema, in the order the editor lays it out.
	Params []ParamDef

	// Run executes the stage for real. Required.
	Run func(rc *RunContext) error
	// Preview is the fast in-memory version for the editor. Optional.
	Preview func(pc *PreviewContext) (*PreviewData, error)
	// Load rebuilds the stage's in-memory product (StageResult.Artifact) from
	// its files, for downstream stages of a run that skipped it. Optional.
	Load func(res *StageResult) (any, error)
	// Validate reports problems Doc.Validate cannot know about — a size that
	// is not a multiple of the tile size, say. p is resolved. Optional.
	Validate func(d *Doc, s *Stage, p Params) []string

	// Regional says how the kind takes part in a level generation doc's
	// environment regions (regional.go): RegionalNone (the default — a
	// region's preset stage of this kind is skipped, with a log line),
	// RegionalPerRegion (expanded once per region as r_<region>_<stage>) or
	// RegionalShared (one stage serves every region; never expanded). Not
	// hashed: setting it changes no existing document's hashes.
	Regional RegionalMode
	// OnDemand, with RegionalPerRegion, expands a preset stage of this kind
	// only when another expanded stage of the same region refers to it
	// (directly or through others): an asset stage — rocks, Poly Haven
	// imports — that no placement of the region reads would only download
	// and cook for nothing (regional.go). Not hashed.
	OnDemand bool
	// SharedFor names the preset kinds a Shared stage stands in for: an
	// expanded stage's ref to a preset stage of one of these kinds is
	// rewritten to the level doc's stage of this kind (region_terrain for a
	// preset's terrain). Optional.
	SharedFor []string
	// HashExtra, when set, is folded into the stage's hash (after its refs'
	// hashes): for a stage whose output depends on more than its params and
	// its upstream stages — region_terrain reads each region's environment
	// preset, a file of its own, so editing a preset must rerun it. p is
	// resolved. An error refuses the run (and the preview hashes without
	// it). Optional; a kind without it — or whose HashExtra returns "" —
	// hashes exactly as before it existed.
	HashExtra func(d *Doc, s *Stage, p Params) (string, error)
	// Expand, on the kind that DEFINES the regions (the `regions` stage),
	// lists each region with the preset document it grows, at plan time.
	// p is resolved. Optional; see regional.go.
	Expand func(d *Doc, s *Stage, p Params) ([]RegionExpansion, error)
	// Warn reports things an author should hear about that do not stop a
	// run — the regions stage's forbidden adjacent environments. p is
	// resolved. Run logs them (and returns them in Result.Warnings);
	// Warnings collects them for `foundry gen check` and the editor.
	// Optional; may be slow-ish (a coarse raster), so it is not called per
	// frame.
	Warn func(d *Doc, s *Stage, p Params) []string
	// Incremental marks a kind that manages its own files across runs
	// (incremental.go): when it reruns, the engine keeps its previous
	// outputs instead of deleting them first, and hands it the previous
	// manifest entry (RunContext.Previous, with per-tile hashes) so it can
	// reuse what is still fresh and prune the rest. Not hashed.
	Incremental bool
	// RectKeyed: the stage's artifact may implement RectKeyer, and every
	// stage downstream of it folds the artifact's key of a piece's
	// rectangle into that piece's paint key (paint_incremental.go): a
	// ground_edit's heights follow paint non-locally (a pond's level is its
	// whole outline's), so a downstream piece must key on the heights it
	// reads, not only on the paint near it. Not hashed.
	RectKeyed bool
}

// Param is the named parameter's definition, or nil.
func (k *StageKind) Param(name string) *ParamDef { return findDef(k.Params, name) }

// DisplayLabel is Label, or Name.
func (k *StageKind) DisplayLabel() string {
	if k.Label != "" {
		return k.Label
	}
	return k.Name
}

var (
	registryMu sync.RWMutex
	stageKinds = map[string]*StageKind{}
	templates  = map[string]*Template{}
)

// RegisterStage adds a kind. A duplicate name, a missing Run or a schema
// whose defaults do not match their types is a programming error and panics:
// it runs from init, where a panic is a failed build of the tests rather than
// a stage that quietly reads zeros.
func RegisterStage(k StageKind) {
	if k.Name == "" || !ValidStageID(k.Name) {
		panic(fmt.Sprintf("procgen: stage kind name %q must be a lower-case word", k.Name))
	}
	if k.Run == nil {
		panic("procgen: stage kind " + k.Name + " has no Run")
	}
	checkDefs("stage kind "+k.Name, k.Params)
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := stageKinds[k.Name]; dup {
		panic("procgen: stage kind " + k.Name + " registered twice")
	}
	kk := k
	stageKinds[k.Name] = &kk
}

// UnregisterStage removes a kind. For tests that register a kind of their
// own.
func UnregisterStage(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(stageKinds, name)
}

// KindByName is a registered kind, or nil.
func KindByName(name string) *StageKind {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return stageKinds[name]
}

// Kinds is every registered kind, sorted by category then name, so menus and
// listings do not shuffle between runs.
func Kinds() []*StageKind {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*StageKind, 0, len(stageKinds))
	for _, k := range stageKinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Template is a starting document for File > New.
type Template struct {
	Name  string
	Label string
	Doc   string
	// New builds the document for a generator to be called name.
	New func(name string) *Doc
}

// RegisterTemplate adds a template; a duplicate name panics.
func RegisterTemplate(t Template) {
	if t.Name == "" || t.New == nil {
		panic("procgen: a template needs a Name and New")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := templates[t.Name]; dup {
		panic("procgen: template " + t.Name + " registered twice")
	}
	tt := t
	templates[t.Name] = &tt
}

// TemplateByName is a registered template, or nil.
func TemplateByName(name string) *Template {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return templates[name]
}

// Templates is every registered template, "blank" first and then by name.
func Templates() []*Template {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Template, 0, len(templates))
	for _, t := range templates {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == BlankTemplate) != (out[j].Name == BlankTemplate) {
			return out[i].Name == BlankTemplate
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// BlankTemplate is the template every build has: a seed and no stages.
const BlankTemplate = "blank"

func init() {
	RegisterTemplate(Template{
		Name:  BlankTemplate,
		Label: "Blank",
		Doc:   "A seed and no stages. Add stages from the stage list's Add menu.",
		New:   func(name string) *Doc { return &Doc{Name: name, Seed: 1} },
	})
}

// NewFromTemplate builds a document from the named template, or an error
// naming the ones there are.
func NewFromTemplate(template, name string) (*Doc, error) {
	t := TemplateByName(template)
	if t == nil {
		var names []string
		for _, t := range Templates() {
			names = append(names, t.Name)
		}
		return nil, fmt.Errorf("no template %q (there are: %v)", template, names)
	}
	d := t.New(name)
	d.Name = name
	return d, nil
}
