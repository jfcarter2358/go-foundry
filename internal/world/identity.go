package world

// identity.go — minting IDs that a script can address an object by.
//
// Chunks and sectors have carried string IDs since the level format existed, but
// nothing ever checked them for collisions: the editor minted sector IDs from
// `len(ch.Sectors)`, which repeats the moment a sector is deleted and another
// created, and a sector split appended "_split" to a name that might already have
// one. That was harmless while an ID was only ever a label. It stops being
// harmless the moment `@id` has to resolve to exactly one object.
//
// The rule these enforce is the same one UniqueEntityID (entity.go) has always
// enforced for entities, applied to the rest of the world:
//
//   - Chunk IDs are unique within a level.
//   - Sector IDs are unique within a level, not merely within their chunk.
//     Chunk.Clone already assumed as much when it rewrote a clone's sector IDs
//     with a suffix, and Portal.ToSector is a bare sector ID with no chunk
//     qualifier, so anything narrower would already have been ambiguous.
//   - Light IDs are unique within a level's LightSet.
//   - Edge IDs are unique within their sector (see sector_edge_id.go), because
//     an edge is always addressed through the sector that owns it.

import (
	"fmt"
	"log"
)

// sectorIDTakenIn reports whether any sector in ch other than the one at
// exceptIdx already uses id. Pass -1 to check every sector.
func sectorIDTakenIn(ch *Chunk, id string, exceptIdx int) bool {
	for si := range ch.Sectors {
		if si == exceptIdx {
			continue
		}
		if ch.Sectors[si].ID == id {
			return true
		}
	}
	return false
}

// UniqueSectorID returns base, or base with a numeric suffix that no sector in
// ch is using. Chunk-scoped because that is what the editor has in hand at a
// mint site; uniqueSectorIDInLevel is the level-wide version the loader uses.
func UniqueSectorID(ch *Chunk, base string) string {
	if base == "" {
		base = "sector"
	}
	if !sectorIDTakenIn(ch, base, -1) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !sectorIDTakenIn(ch, id, -1) {
			return id
		}
	}
}

// sectorIDTakenInLevel reports whether any sector anywhere in chunks uses id,
// ignoring the one at (exceptChunk, exceptSector).
func sectorIDTakenInLevel(chunks []Chunk, id string, exceptChunk, exceptSector int) bool {
	for ci := range chunks {
		for si := range chunks[ci].Sectors {
			if ci == exceptChunk && si == exceptSector {
				continue
			}
			if chunks[ci].Sectors[si].ID == id {
				return true
			}
		}
	}
	return false
}

// uniqueSectorIDInLevel returns base, or base with a suffix no sector anywhere
// in chunks is using.
func uniqueSectorIDInLevel(chunks []Chunk, base string, exceptChunk, exceptSector int) string {
	if base == "" {
		base = "sector"
	}
	if !sectorIDTakenInLevel(chunks, base, exceptChunk, exceptSector) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !sectorIDTakenInLevel(chunks, id, exceptChunk, exceptSector) {
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

// ResolveLevelIDCollisions makes every chunk and sector ID in chunks distinct,
// renaming duplicates and rewriting any Portal.ToSector that pointed at a
// renamed sector.
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

	// Sector renames have to be applied before portals are rewritten, because a
	// portal in one chunk can target a sector in another.
	renamed := map[string]string{}
	seenSector := map[string]bool{}
	for ci := range chunks {
		for si := range chunks[ci].Sectors {
			id := chunks[ci].Sectors[si].ID
			if id == "" {
				id = fmt.Sprintf("sector_%d", si)
			}
			if seenSector[id] {
				fresh := uniqueSectorIDInLevel(chunks, id, ci, si)
				log.Printf("level: sector ID %q is used more than once; renaming one to %q", id, fresh)
				renamed[id] = fresh
				id = fresh
			}
			seenSector[id] = true
			chunks[ci].Sectors[si].ID = id
		}
	}

	if len(renamed) == 0 {
		return
	}
	// A portal names its target by bare sector ID. Only the *second* claimant of
	// a name was renamed, so a portal that pointed at the first still resolves —
	// but one that meant the second is now dangling, and there is no way to tell
	// the two apart. Log it rather than guess.
	for ci := range chunks {
		for si := range chunks[ci].Sectors {
			for pi := range chunks[ci].Sectors[si].Portals {
				to := chunks[ci].Sectors[si].Portals[pi].ToSector
				if _, was := renamed[to]; was {
					log.Printf("level: portal in sector %q targets %q, which was ambiguous before renaming; verify it points where you meant",
						chunks[ci].Sectors[si].ID, to)
				}
			}
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
// volume, a sector, a chunk, a light or a biome sector — or is reserved.
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
	if lvl == nil {
		return false
	}
	for i := range lvl.Entities {
		if lvl.Entities[i].ID == id {
			return true
		}
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].ID == id {
			return true
		}
	}
	for i := range lvl.Sprites {
		if lvl.Sprites[i].ID == id {
			return true
		}
	}
	for i := range lvl.Emitters {
		if lvl.Emitters[i].ID == id {
			return true
		}
	}
	for i := range lvl.Sounds {
		if lvl.Sounds[i].ID == id {
			return true
		}
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.ID == id {
			return true
		}
		for si := range ch.Sectors {
			if ch.Sectors[si].ID == id {
				return true
			}
		}
		for bi := range ch.BiomeSectors {
			if ch.BiomeSectors[bi].ID == id {
				return true
			}
		}
	}
	return lightIDTaken(&lvl.Lights, id)
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
