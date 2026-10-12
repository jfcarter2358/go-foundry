package world

// identity.go — minting IDs that a script can address an object by.
//
// Chunks and brushes carry string IDs. The rule these enforce is the same one
// UniqueEntityID (entity.go) has always enforced for entities, applied to the
// rest of the world:
//
//   - Chunk IDs are unique within a level.
//   - Brush IDs are unique within a level, not merely within their chunk.
//     Chunk.Clone already assumes as much when it rewrites a clone's brush
//     IDs with a suffix.
//   - Light IDs are unique within a level's LightSet.

import (
	"fmt"
	"log"
)

// brushIDTakenIn reports whether any brush in ch other than the one at
// exceptIdx already uses id. Pass -1 to check every brush.
func brushIDTakenIn(ch *Chunk, id string, exceptIdx int) bool {
	for bi := range ch.Brushes {
		if bi == exceptIdx {
			continue
		}
		if ch.Brushes[bi].ID == id {
			return true
		}
	}
	return false
}

// UniqueBrushID returns base, or base with a numeric suffix that no brush in
// ch is using. Chunk-scoped because that is what the editor has in hand at a
// mint site; uniqueBrushIDInLevel is the level-wide version the loader uses.
func UniqueBrushID(ch *Chunk, base string) string {
	if base == "" {
		base = "brush"
	}
	if !brushIDTakenIn(ch, base, -1) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !brushIDTakenIn(ch, id, -1) {
			return id
		}
	}
}

// brushIDTakenInLevel reports whether any brush anywhere in chunks uses id,
// ignoring the one at (exceptChunk, exceptBrush).
func brushIDTakenInLevel(chunks []Chunk, id string, exceptChunk, exceptBrush int) bool {
	for ci := range chunks {
		for bi := range chunks[ci].Brushes {
			if ci == exceptChunk && bi == exceptBrush {
				continue
			}
			if chunks[ci].Brushes[bi].ID == id {
				return true
			}
		}
	}
	return false
}

// uniqueBrushIDInLevel returns base, or base with a suffix no brush anywhere
// in chunks is using.
func uniqueBrushIDInLevel(chunks []Chunk, base string, exceptChunk, exceptBrush int) string {
	if base == "" {
		base = "brush"
	}
	if !brushIDTakenInLevel(chunks, base, exceptChunk, exceptBrush) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !brushIDTakenInLevel(chunks, id, exceptChunk, exceptBrush) {
			return id
		}
	}
}

// chunkIDTaken reports whether any chunk other than the one at exceptIdx uses id.
func chunkIDTaken(chunks []Chunk, id string, exceptIdx int) bool {
	for ci := range chunks {
		if ci == exceptIdx {
			continue
		}
		if chunks[ci].ID == id {
			return true
		}
	}
	return false
}

// uniqueChunkID returns base, or base with a suffix no chunk is using.
func uniqueChunkID(chunks []Chunk, base string, exceptIdx int) string {
	if base == "" {
		base = "chunk"
	}
	if !chunkIDTaken(chunks, base, exceptIdx) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !chunkIDTaken(chunks, id, exceptIdx) {
			return id
		}
	}
}

// lightIDTaken reports whether any light in ls uses id.
func lightIDTaken(ls *LightSet, id string) bool {
	for i := range ls.Directional {
		if ls.Directional[i].ID == id {
			return true
		}
	}
	for i := range ls.Points {
		if ls.Points[i].ID == id {
			return true
		}
	}
	return false
}

// uniqueLightID returns base, or base with a suffix no light in ls is using.
func uniqueLightID(ls *LightSet, base string) string {
	if base == "" {
		base = "light"
	}
	if !lightIDTaken(ls, base) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !lightIDTaken(ls, id) {
			return id
		}
	}
}

// AssignLightIDs fills in any light that loaded without an ID and resolves
// collisions between the ones that did. Lights predate having identity at all,
// so every existing level takes this path.
func AssignLightIDs(ls *LightSet) {
	for i := range ls.Directional {
		id := ls.Directional[i].ID
		if id == "" || lightIDTakenElsewhereDir(ls, id, i) {
			base := id
			if base == "" {
				base = fmt.Sprintf("dir_light_%d", i)
			}
			ls.Directional[i].ID = uniqueLightIDExcludingDir(ls, base, i)
		}
	}
	for i := range ls.Points {
		id := ls.Points[i].ID
		if id == "" || lightIDTakenElsewherePoint(ls, id, i) {
			base := id
			if base == "" {
				base = fmt.Sprintf("point_light_%d", i)
			}
			ls.Points[i].ID = uniqueLightIDExcludingPoint(ls, base, i)
		}
	}
}

func lightIDTakenElsewhereDir(ls *LightSet, id string, exceptIdx int) bool {
	for i := range ls.Directional {
		if i != exceptIdx && ls.Directional[i].ID == id {
			return true
		}
	}
	for i := range ls.Points {
		if ls.Points[i].ID == id {
			return true
		}
	}
	return false
}

func lightIDTakenElsewherePoint(ls *LightSet, id string, exceptIdx int) bool {
	for i := range ls.Points {
		if i != exceptIdx && ls.Points[i].ID == id {
			return true
		}
	}
	for i := range ls.Directional {
		if ls.Directional[i].ID == id {
			return true
		}
	}
	return false
}

func uniqueLightIDExcludingDir(ls *LightSet, base string, exceptIdx int) string {
	if !lightIDTakenElsewhereDir(ls, base, exceptIdx) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !lightIDTakenElsewhereDir(ls, id, exceptIdx) {
			return id
		}
	}
}

func uniqueLightIDExcludingPoint(ls *LightSet, base string, exceptIdx int) string {
	if !lightIDTakenElsewherePoint(ls, base, exceptIdx) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !lightIDTakenElsewherePoint(ls, id, exceptIdx) {
			return id
		}
	}
}

// ResolveLevelIDCollisions makes every chunk and brush ID in chunks distinct,
// renaming duplicates.
//
// It renames rather than rejects: a level authored before uniqueness was
// enforced should still open, with a warning telling the author what moved. A
// hard error here would lock someone out of their own level.
func ResolveLevelIDCollisions(chunks []Chunk) {
	seenChunk := map[string]bool{}
	for ci := range chunks {
		id := chunks[ci].ID
		if id == "" {
			id = fmt.Sprintf("chunk_%d", ci)
		}
		if seenChunk[id] {
			fresh := uniqueChunkID(chunks, id, ci)
			log.Printf("level: chunk ID %q is used more than once; renaming one to %q", id, fresh)
			id = fresh
		}
		seenChunk[id] = true
		chunks[ci].ID = id
	}

	seenBrush := map[string]bool{}
	for ci := range chunks {
		for bi := range chunks[ci].Brushes {
			id := chunks[ci].Brushes[bi].ID
			if id == "" {
				id = fmt.Sprintf("brush_%d", bi)
			}
			if seenBrush[id] {
				fresh := uniqueBrushIDInLevel(chunks, id, ci, bi)
				log.Printf("level: brush ID %q is used more than once; renaming one to %q", id, fresh)
				id = fresh
			}
			seenBrush[id] = true
			chunks[ci].Brushes[bi].ID = id
		}
	}
}

// ---------- the level-wide ID namespace ----------

// reservedLevelIDs are the names the engine answers to itself. A level that
// authored `id: player` would otherwise shadow the engine-owned player entity,
// and `@level` has to keep meaning the level.
var reservedLevelIDs = map[string]bool{
	"player": true,
	"level":  true,
	"self":   true,
	"spawn":  true,
}

// LevelIDTaken reports whether id already names something in lvl — an entity, a
// volume, a chunk, a brush or a light — or is reserved.
//
// SCRIPTING.md promises that `@id` never needs qualifying, which only holds if
// one name means one object across every kind. Checking per-kind would let an
// author give a volume the same name as a sector and get whichever the resolver
// happened to look at first.
func LevelIDTaken(lvl *Level, id string) bool {
	if id == "" {
		return true
	}
	if reservedLevelIDs[id] {
		return true
	}
	return eachLevelID(lvl, func(have string) bool { return have == id })
}

// eachLevelID calls fn with the ID of every addressable object in lvl — the
// one list of what shares the namespace, so LevelIDTaken and LevelIDIndex
// cannot disagree about it. fn returning true stops the walk, and
// eachLevelID then returns true.
func eachLevelID(lvl *Level, fn func(id string) bool) bool {
	if lvl == nil {
		return false
	}
	for i := range lvl.Entities {
		if fn(lvl.Entities[i].ID) {
			return true
		}
	}
	for i := range lvl.Volumes {
		if fn(lvl.Volumes[i].ID) {
			return true
		}
	}
	for i := range lvl.Sprites {
		if fn(lvl.Sprites[i].ID) {
			return true
		}
	}
	for i := range lvl.Emitters {
		if fn(lvl.Emitters[i].ID) {
			return true
		}
	}
	for i := range lvl.Sounds {
		if fn(lvl.Sounds[i].ID) {
			return true
		}
	}
	for i := range lvl.Effects {
		if fn(lvl.Effects[i].ID) {
			return true
		}
	}
	for i := range lvl.Scenery {
		if fn(lvl.Scenery[i].ID) {
			return true
		}
	}
	for i := range lvl.Trees {
		if fn(lvl.Trees[i].ID) {
			return true
		}
	}
	for i := range lvl.Decals {
		if fn(lvl.Decals[i].ID) {
			return true
		}
	}
	for i := range lvl.WaypointAnimations {
		if fn(lvl.WaypointAnimations[i].ID) {
			return true
		}
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if fn(ch.ID) {
			return true
		}
		for bi := range ch.Brushes {
			if fn(ch.Brushes[bi].ID) {
				return true
			}
		}
	}
	for i := range lvl.Lights.Directional {
		if fn(lvl.Lights.Directional[i].ID) {
			return true
		}
	}
	for i := range lvl.Lights.Points {
		if fn(lvl.Lights.Points[i].ID) {
			return true
		}
	}
	return false
}

// uniqueLevelID returns base, or base with a numeric suffix, such that no object
// of any kind in lvl is using it. Every mint site for an addressable object goes
// through here or through a kind-specific wrapper that consults it.
func uniqueLevelID(lvl *Level, base string) string {
	if base == "" {
		base = "object"
	}
	if !LevelIDTaken(lvl, base) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !LevelIDTaken(lvl, id) {
			return id
		}
	}
}
