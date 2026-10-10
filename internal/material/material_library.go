package material

// material_library.go — the loaded set of materials and how a name becomes one.
//
// Materials are resolved by NAME, the way a texture is: a face that says
// `material: hangar_strip` means resources/materials/hangar_strip.fmat, and a
// level file never carries a path. The library is the table that does it.
//
// It is loaded once at startup (LoadAll) and again whenever the material editor
// saves, and it is read from the draw path, so it is behind a mutex and hands
// out pointers that callers must not edit — Doc.Clone is there for callers who
// mean to.
//
// Generation is how everything downstream notices a change without being told.
// A resolved material is CACHED on whatever wears it (gfx.Geometry's surface
// state, a sprite's draw fields), because re-reading a document per draw call
// would be a map lookup and a dozen branches per batch; the cache is stamped
// with the Generation it was built at, and one integer compare per draw is what
// makes an edit in the material editor show up on every wall in the level on
// the next frame. It is the same trick world.BrushMeshGeneration plays for
// meshes, for the same reason.

import (
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	libMu sync.RWMutex
	lib   = map[string]*Doc{}
	// generation counts edits to the library. Never zero after the first load,
	// so a cache left at its zero value always reads as stale.
	generation = 1
)

// Generation is the current library generation. Anything caching a resolved
// material records it and re-resolves when it no longer matches.
func Generation() int {
	libMu.RLock()
	defer libMu.RUnlock()
	return generation
}

// bumpLocked invalidates every cached resolution. The caller holds libMu.
func bumpLocked() { generation++ }

// Get returns the named material, or nil. A nil result is the ordinary case for
// most of the engine — nearly everything names a texture rather than a
// material — so it is not an error and nothing logs it.
//
// The returned pointer is the library's own. Edit a Clone.
func Get(name string) *Doc {
	if name == "" {
		return nil
	}
	libMu.RLock()
	defer libMu.RUnlock()
	// The preview slot first: while the material editor is showing an unsaved
	// document, that document is what its own swatch must resolve to. It is
	// under a name no file can have, so nothing else can reach it.
	if previewDoc != nil && previewName == name {
		return previewDoc
	}
	return lib[name]
}

// Exists reports whether a material of that name is loaded — what a picker and
// the level checker ask, without pulling the document.
func Exists(name string) bool { return Get(name) != nil }

// Names lists every loaded material, sorted, for the pickers.
func Names() []string {
	libMu.RLock()
	defer libMu.RUnlock()
	out := make([]string, 0, len(lib))
	for n := range lib {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// NamesFor lists every loaded material of one domain, sorted — a surface
// picker has no business offering a sky.
func NamesFor(d Domain) []string {
	libMu.RLock()
	defer libMu.RUnlock()
	var out []string
	for n, doc := range lib {
		if doc.Domain == d {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Put installs a document under its own name and invalidates every cached
// resolution. The material editor's save path, and how a test stands a material
// up without a file.
func Put(d *Doc) {
	if d == nil || d.Name == "" {
		return
	}
	libMu.Lock()
	defer libMu.Unlock()
	lib[d.Name] = d
	bumpLocked()
}

// Remove drops a material — a deleted file, or one the editor renamed away
// from.
func Remove(name string) {
	libMu.Lock()
	defer libMu.Unlock()
	if _, ok := lib[name]; !ok {
		return
	}
	delete(lib, name)
	bumpLocked()
}

// Clear empties the library and the preview slot. For tests, and for a project switch: the editor
// can open a project in another directory, and the materials of the one it
// left have no business still resolving.
func Clear() {
	libMu.Lock()
	defer libMu.Unlock()
	lib = map[string]*Doc{}
	previewName, previewDoc, previewFP = "", nil, 0
	bumpLocked()
}

// LoadAll reads every .fmat in the materials directory into the library,
// replacing what was there. A document that fails to parse is logged and
// skipped rather than failing the load: one bad material should cost its own
// surfaces their effects, not stop the game starting.
//
// Returns how many loaded, which the editor's status line prints.
func LoadAll() int {
	docs := map[string]*Doc{}
	for _, name := range ScanMaterialFiles() {
		d, err := ReadMaterial(MaterialPath(name))
		if err != nil {
			log.Printf("material %s: %v", name, err)
			continue
		}
		for _, p := range d.Validate() {
			log.Printf("material %s: %s", name, p)
		}
		docs[name] = d
	}
	libMu.Lock()
	defer libMu.Unlock()
	lib = docs
	bumpLocked()
	return len(docs)
}

// Reload re-reads one material from disk. What the editor calls after a save,
// and what the Claude panel's reload routing calls when a material file is
// written from outside — the same treatment scripts, models and effects get.
// A material whose file has gone is removed.
func Reload(name string) error {
	d, err := ReadMaterial(MaterialPath(name))
	if err != nil {
		Remove(name)
		return err
	}
	Put(d)
	return nil
}

// NameFromPath is the material a file path refers to, and whether it is one at
// all. For the editor's file watcher and the release packer.
func NameFromPath(path string) (string, bool) {
	if !strings.EqualFold(filepath.Ext(path), MaterialExt) {
		return "", false
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), true
}

// ---------- the preview slot ----------
//
// The material editor has to resolve a document that is NOT in the library: one
// being edited, possibly never saved, which must not be findable by name from a
// level and must not appear in any picker. It also redraws sixty times a
// second, and a document installed through Put on every one of those frames
// would bump the generation on every one of them — which would make every
// generation-stamped cache in the engine permanently stale for as long as the
// editor is open.
//
// So a preview lives in a slot of its own: consulted by Get, never listed by
// Names or NamesFor, and only bumping the generation when what it holds
// actually changes.

var (
	previewName string
	previewDoc  *Doc
	previewFP   uint64
)

// SetPreview installs d as the preview document under its own name, replacing
// whatever was there. A nil d clears the slot.
//
// The generation moves only when the document's content differs from what the
// slot already held, so an editor redrawing an unchanged material costs one
// fingerprint a frame and invalidates nothing.
func SetPreview(d *Doc) {
	libMu.Lock()
	defer libMu.Unlock()
	if d == nil {
		if previewDoc == nil {
			return
		}
		previewName, previewDoc, previewFP = "", nil, 0
		bumpLocked()
		return
	}
	fp := Fingerprint(d)
	if previewDoc != nil && previewName == d.Name && fp == previewFP {
		return
	}
	previewName, previewDoc, previewFP = d.Name, d, fp
	bumpLocked()
}

// PreviewName reports which name the preview slot is holding, or "".
func PreviewName() string {
	libMu.RLock()
	defer libMu.RUnlock()
	return previewName
}
