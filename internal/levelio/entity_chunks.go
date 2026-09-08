package levelio

// entity_chunks.go — turning an entity's chunk attachments into level geometry.
//
// A definition says "this type carries the chunk `shuttle_hull`, one metre
// forward of its origin" (world/entity_chunk.go). This is what makes that true
// in a level: one runtime Chunk per attachment, uploaded, bound to the entity's
// frame, and tagged with the entity it came from so saving knows not to write
// it back out.
//
// It lives here rather than in internal/world because it reads files, and
// internal/world must not depend on the loader — the level format is levelio's
// half of the split. What world owns is the description; what this owns is
// making it real.
//
// The two ends that matter:
//
//   - The chunk's FrameOwner is the entity's ID, so BuildFrames gives the
//     entity a frame and every downstream pass — render, collision, the player
//     walking around inside it — works with no further change. That machinery
//     already existed for vehicle interiors; all that was missing was a way to
//     author the binding on a type.
//   - The chunk's FromEntity is that same ID, and is what LevelToRaw checks
//     before writing a chunk out. A materialised chunk is the *definition's*
//     content; a level that saved a copy of it would stop tracking the file.

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

// ScanChunkNames lists the chunk files available to attach, extension trimmed
// and sorted — what the entity editor's picker offers.
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
// that need exactly the same conversion: the entity editor's preview, and the
// materialiser below.
func BuildChunkFromRaw(rc world.RawChunk) (world.Chunk, map[string]bool, error) {
	return loadRawChunk(rc)
}

// LoadChunkForEntity reads one attachment's chunk file and builds it, already
// placed and bound to the entity.
//
// entityID may be empty, which is what the entity editor's preview passes: the
// chunk is then built at the attachment's own offset with no frame, since the
// preview *is* the entity's local space.
func LoadChunkForEntity(entityID string, a *world.EntityChunk) (world.Chunk, map[string]bool, error) {
	if a == nil || a.File == "" {
		return world.Chunk{}, nil, fmt.Errorf("chunk attachment has no file")
	}
	rc, err := LoadChunkFromFile(ChunkFilePath(a.File))
	if err != nil {
		return world.Chunk{}, nil, fmt.Errorf("chunk attachment %q: %w", a.Name, err)
	}

	// The file's own ID, origin and placement describe where it sat in whatever
	// level exported it, which has no meaning here — the attachment's placement
	// replaces all of it, exactly as ImportChunkIntoLevel replaces the origin.
	rc.ID = world.EntityChunkID(entityID, a.Name)
	rc.Origin = world.RawChunkOrigin{X: a.Offset.X(), Y: a.Offset.Y(), Z: a.Offset.Z()}
	rc.Rotation = a.Rotation
	rc.Scale = a.EffectiveScale()
	rc.Frame = entityID
	// A definition may ship a deck switched off; an attachment never switches
	// one back on that the file itself authored disabled.
	rc.Disabled = rc.Disabled || a.Disabled

	// Sector IDs have to be unique across the level, and two shuttles placed
	// from one definition would otherwise both carry a sector called "hold".
	// Prefixing by the chunk's own ID is the same shape ImportChunkIntoLevel's
	// suffixing has, and keeps the qualified name readable in the Explorer.
	if entityID != "" {
		prefixSectorIDs(&rc, rc.ID+".")
	}

	ch, tex, err := BuildChunkFromRaw(rc)
	if err != nil {
		return world.Chunk{}, nil, fmt.Errorf("chunk attachment %q: %w", a.Name, err)
	}
	ch.FromEntity = entityID
	return ch, tex, nil
}

// prefixSectorIDs renames every sector in the chunk, carrying in-chunk portal
// references along with them. A portal naming a sector outside this chunk is
// left alone — it was not one of the names being rewritten.
func prefixSectorIDs(rc *world.RawChunk, prefix string) {
	if prefix == "" {
		return
	}
	remap := make(map[string]string, len(rc.Sectors))
	for i := range rc.Sectors {
		remap[rc.Sectors[i].ID] = prefix + rc.Sectors[i].ID
	}
	for i := range rc.Sectors {
		rc.Sectors[i].ID = remap[rc.Sectors[i].ID]
		for j := range rc.Sectors[i].Edges {
			if to, ok := remap[rc.Sectors[i].Edges[j].ToSector]; ok {
				rc.Sectors[i].Edges[j].ToSector = to
			}
		}
	}
}

// MaterializeEntityChunks builds every chunk attachment on e and appends the
// results to lvl, adding whatever textures they need to texNeeded.
//
// An attachment that will not load costs the entity that deck, not its
// existence: the error is returned for the caller to report and the rest are
// still built. That is the rule a missing model attachment follows, and for the
// same reason — a level that refuses to open over one missing chunk file is
// worse than one that opens with a hole in it and says so.
func MaterializeEntityChunks(lvl *world.Level, e *world.Entity, texNeeded map[string]bool) []error {
	if lvl == nil || e == nil || len(e.Chunks) == 0 {
		return nil
	}
	var errs []error
	for i := range e.Chunks {
		a := &e.Chunks[i]
		if a.File == "" {
			continue // an attachment with nothing in it yet
		}
		ch, tex, err := LoadChunkForEntity(e.ID, a)
		if err != nil {
			errs = append(errs, fmt.Errorf("entity %q: %w", e.ID, err))
			continue
		}
		ch.ID = uniqueChunkID(lvl, ch.ID)
		for t := range tex {
			if texNeeded != nil {
				texNeeded[t] = true
			}
		}
		lvl.Chunks = append(lvl.Chunks, ch)
	}
	return errs
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

// DropEntityChunks removes and releases every chunk materialised from the named
// entity, and reports how many went.
//
// Called when the entity is deleted, renamed, or re-stamped from a definition
// that no longer carries the same decks. Releasing the geometry matters: these
// chunks hold GL buffers like any other, and dropping the slice entry alone
// would leak them once per edit.
func DropEntityChunks(lvl *world.Level, entityID string) int {
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
