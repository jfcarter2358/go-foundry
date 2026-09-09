package levelio

// entity_geometry.go — turning an entity's brushes into level geometry.
//
// A definition says "this type is these six brushes" (world/entity_brush.go).
// This is what makes that true in a level: one runtime Chunk per entity,
// uploaded, bound to the entity's frame, and tagged with the entity it came
// from so saving knows not to write it back out.
//
// It lives here rather than in internal/world because it uploads geometry and
// resolves textures, and internal/world must not depend on the loader — the
// level format is levelio's half of the split. What world owns is the
// description; what this owns is making it real.
//
// The two ends that matter:
//
//   - The chunk's FrameOwner is the entity's ID, so BuildFrames gives the
//     entity a frame and every downstream pass — render, shadows, streaming,
//     the player walking around inside it — works with no further change. That
//     machinery already existed for vehicle interiors; what changed is only
//     where the brushes come from.
//   - The chunk's FromEntity is that same ID, and is what LevelToRaw checks
//     before writing a chunk out. A materialised chunk is the *entity's*
//     content; a level that saved a copy of it would write the same geometry
//     twice and the two would drift.
//
// # One chunk, not one per attachment
//
// The chunk attachment this replaces let a type carry several named decks,
// because each was a separate file. An entity's brushes are one pool, so there
// is one chunk — and nothing is lost, because a "deck" was only ever a grouping
// of brushes and the brushes are still individually addressable, individually
// dynamic, and individually switchable through their own `enabled`.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"path/filepath"
	"strings"
)

// ChunksDirName is the directory under the install root holding standalone
// chunk files, and chunkFileExt the extension they carry.
//
// Chunk files are still a thing a level can import geometry from and export
// geometry to — what stopped being a thing is an entity *referencing* one. The
// entity editor's import reads one of these and copies the brushes in.
const (
	ChunksDirName = "chunks"
	chunkFileExt  = ".yaml"
)

// ChunksDir is that directory's absolute path.
func ChunksDir() string { return filepath.Join(fileutil.ExecutablePath, ChunksDirName) }

// ChunkFilePath resolves a chunk name — with or without its extension — to a
// path under chunks/.
func ChunkFilePath(name string) string {
	if !strings.EqualFold(filepath.Ext(name), chunkFileExt) {
		name += chunkFileExt
	}
	return filepath.Join(ChunksDir(), name)
}

// ScanChunkNames lists the chunk files available to import, extension trimmed
// and sorted — what the entity editor's import dialog offers.
func ScanChunkNames() []string {
	files := fileutil.ScanFilesByExt(ChunksDir(), chunkFileExt)
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, strings.TrimSuffix(f, chunkFileExt))
	}
	return names
}

// BuildChunkFromRaw converts a raw chunk into a runtime one, uploading its
// geometry, and reports the textures it needs.
//
// The exported door onto loadRawChunk, for the callers outside a level load
// that need exactly the same conversion: the prefab stamper, and the entity
// editor's import.
func BuildChunkFromRaw(rc world.RawChunk) (world.Chunk, map[string]bool, error) {
	return loadRawChunk(rc)
}

// EntityGeometryChunk builds the one chunk an entity's brushes make, already
// placed and bound to the entity, and reports the textures it needs.
//
// entityID may be empty, which is what the entity editor's preview passes: the
// chunk is then built with no frame and unqualified brush IDs, since the
// preview *is* the entity's local space and there is no level for a name to
// collide in.
//
// Returns ok=false with no error for an entity with no brushes, which is the
// ordinary case for a barrel and must not be an error or a chunk.
func EntityGeometryChunk(entityID string, e *world.Entity) (ch world.Chunk, tex map[string]bool, ok bool, err error) {
	if e == nil || !e.HasBrushes() {
		return world.Chunk{}, nil, false, nil
	}

	rc := world.RawChunk{
		ID: world.EntityGeometryChunkID(entityID),
		// No origin, rotation or scale of its own: the entity's brushes are
		// authored in the entity's own space, and the frame below is what
		// carries them into the world. A second placement here would be a
		// transform with nothing to say and one more thing to keep in step
		// with the gizmo that drags the entity.
		Frame: entityID,
	}
	rc.Brushes = make([]world.RawBrush, 0, len(e.Brushes))
	for i := range e.Brushes {
		rb := world.BrushToRaw(&e.Brushes[i])
		// Brush IDs are level-unique, and every shuttle placed from one
		// definition calls its door "door". Qualifying by the entity is what
		// makes `@shuttle_2.door` name exactly one solid.
		rb.ID = world.EntityBrushID(entityID, rb.ID)
		rc.Brushes = append(rc.Brushes, rb)
	}

	built, tex, err := BuildChunkFromRaw(rc)
	if err != nil {
		return world.Chunk{}, nil, false, fmt.Errorf("entity %q geometry: %w", e.ID, err)
	}
	built.FromEntity = entityID
	return built, tex, true, nil
}

// MaterializeEntityGeometry builds e's brushes into a chunk and appends it to
// lvl, adding whatever textures it needs to texNeeded.
//
// An entity whose geometry will not build costs the level that entity's solid,
// not the level: the error is returned for the caller to report and the load
// carries on. That is the rule a missing model attachment follows, and for the
// same reason — a level that refuses to open over one bad brush is worse than
// one that opens with a hole in it and says so.
func MaterializeEntityGeometry(lvl *world.Level, e *world.Entity, texNeeded map[string]bool) error {
	if lvl == nil || e == nil || !e.HasBrushes() {
		return nil
	}
	ch, tex, ok, err := EntityGeometryChunk(e.ID, e)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	ch.ID = uniqueChunkID(lvl, ch.ID)
	for t := range tex {
		if texNeeded != nil {
			texNeeded[t] = true
		}
	}
	lvl.Chunks = append(lvl.Chunks, ch)
	return nil
}

// EnsureChunkTextures uploads the textures a just-materialised chunk needs.
//
// The level loader does not use it: it accumulates every chunk's textures and
// loads them in one stepped pass afterwards. This is for the editor, which
// materialises a single entity mid-session with no such pass to join.
func EnsureChunkTextures(texNeeded map[string]bool) error {
	for name := range texNeeded {
		if _, already := gfx.Textures[name]; already {
			continue
		}
		path, ok := gfx.TextureNames[name]
		if !ok {
			return fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := gfx.LoadTexture(name, filepath.Join(fileutil.ExecutablePath, path)); err != nil {
			return err
		}
	}
	return nil
}

// DropEntityGeometry removes and releases every chunk materialised from the
// named entity, and reports how many went.
//
// Called when the entity is deleted, renamed, or re-stamped from a definition
// whose geometry has changed. Releasing matters: these chunks hold GL buffers
// like any other, and dropping the slice entry alone would leak them once per
// edit. Written as a sweep rather than a single lookup because a rename leaves
// the old ID's chunk behind and this is what has to find it.
func DropEntityGeometry(lvl *world.Level, entityID string) int {
	if lvl == nil || entityID == "" {
		return 0
	}
	kept := lvl.Chunks[:0]
	dropped := 0
	for i := range lvl.Chunks {
		if lvl.Chunks[i].FromEntity == entityID {
			lvl.Chunks[i].ReleaseGeometry()
			dropped++
			continue
		}
		kept = append(kept, lvl.Chunks[i])
	}
	lvl.Chunks = kept
	return dropped
}

// uniqueChunkID returns base, or base with a numeric suffix no chunk in the
// level is using. A materialised ID is already qualified by its entity, so this
// only fires when a level file happens to author a chunk called the same thing.
func uniqueChunkID(lvl *world.Level, base string) string {
	taken := func(id string) bool {
		for i := range lvl.Chunks {
			if lvl.Chunks[i].ID == id {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !taken(id) {
			return id
		}
	}
}
