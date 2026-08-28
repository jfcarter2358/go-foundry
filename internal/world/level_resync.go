package world

// level_resync.go — keeping caches derived from structural geometry in step
// with edits to that geometry.
//
// A biome sector is a world-space polygon that drapes over whatever floor
// happens to lie under it: its scatter height/coverage texture (grass.go), its
// ground patch mesh (biome_ground.go), and Water's companion plane
// (biome_water.go) are all baked from floor heights and then cached by sector
// ID. Nothing in that chain watches the floor, so an edit that moves the ground
// — dragging a vertex or an edge, typing a new floor height, extruding, moving
// a whole chunk — left the biome baked at the shape the floor had when it was
// last built: a patch of flat ground sitting through a slope that has since
// moved out from under it, until "Refresh Heights" was pressed by hand.
//
// ReloadSector is the single place editor code rebuilds a sector after such an
// edit, so it is also where the biomes over that sector are marked for a
// rebuild of their own.

import "github.com/go-gl/mathgl/mgl32"

// ReloadSector rebuilds s's geometry from the current chunk points and flags
// every biome sector over it dirty. Editor code should call this rather than
// s.Load directly, so nothing painted on top of the sector is left behind.
func ReloadSector(ch *Chunk, s *Sector) error {
	// The edit can move or shrink the sector, so the biomes over where it
	// *was* are exactly as stale as the ones over where it now is: both
	// footprints get marked. (Sector.Load recomputes MinBound/MaxBound, so
	// reading them before and after brackets the change.)
	oldMin, oldMax := s.MinBound, s.MaxBound
	err := s.Load(SectorPoints(ch, s))
	lo := mgl32.Vec2{min(oldMin.X(), s.MinBound.X()), min(oldMin.Y(), s.MinBound.Y())}
	hi := mgl32.Vec2{max(oldMax.X(), s.MaxBound.X()), max(oldMax.Y(), s.MaxBound.Y())}
	MarkBiomesDirtyOverSector(ch, s, lo, hi)
	return err
}

// MarkBiomesDirtyOverSector flags the biome sectors whose floor-derived meshes
// this edit to s invalidates, so they are rebuilt on the next frame. lo/hi
// bracket s's footprint before AND after the edit, in ch's own local space.
//
// Two filters, and BOTH are load-bearing.
//
// The XZ overlap test is done in WORLD space rather than by comparing the two
// boxes directly. Both sides are chunk-local, but to *different* chunks: lo/hi
// to ch, and each biome sector's own Bounds() to whichever chunk it was
// painted on. Comparing them raw treats two chunks' local frames as if they
// were one — and since chunks conventionally sit near their own local origin,
// nearly every painted sector then looks like it overlaps nearly every edit.
//
// Overlapping in XZ is not enough, though, because XZ says nothing about
// height: a chunk floating ABOVE painted ground overlaps it exactly. So the
// paint must also actually be DRAPED on s — the same question DrapeTarget
// answers for the renderer and for the warp gate in CarryBiomesWithPoints, and
// answered here the same way so the three cannot disagree.
//
// Getting either filter wrong is a performance cliff rather than a visual bug,
// and a steep one: a spuriously dirtied sector re-triangulates its ground patch
// from scratch on the next frame — measured at ~134ms and 44MB for one modest
// polygon — and a vertex drag runs this on every mouse-move.
func MarkBiomesDirtyOverSector(ch *Chunk, s *Sector, lo, hi mgl32.Vec2) {
	if ch == nil {
		return
	}
	editLo, editHi := transformAABB(ch.ModelMatrix(),
		mgl32.Vec3{lo.X(), 0, lo.Y()}, mgl32.Vec3{hi.X(), 0, hi.Y()})
	ForEachBiomeSector(ch, func(owner *Chunk, bs *BiomeSector) {
		bmin, bmax := bs.Bounds()
		paintLo, paintHi := transformAABB(owner.ModelMatrix(),
			mgl32.Vec3{bmin.X(), 0, bmin.Y()}, mgl32.Vec3{bmax.X(), 0, bmax.Y()})
		if paintHi.X() < editLo.X() || paintLo.X() > editHi.X() ||
			paintHi.Z() < editLo.Z() || paintLo.Z() > editHi.Z() {
			return
		}
		if s != nil && !paintDrapesOn(owner, bs, s) {
			return
		}
		// Both flags: a floor that moved changes the height texture the
		// scatter drops onto and the ground patch mesh (and water plane)
		// alike. See BiomeSector's field docs for why they are separate.
		bs.HeightDirty = true
		bs.GroundDirty = true
	})
}

// paintDrapesOn reports whether an edit to sector s can change where bs's
// paint sits — i.e. whether s is, was, or could become the surface bs drapes
// on. Conservative by design: a false positive costs one wasted rebuild, a
// false negative leaves the paint baked at a height that no longer exists.
//
// Three ways it can matter, and together they are exhaustive:
//
//  1. bs is BOUND to s. The binding outranks geometry, so s stays relevant
//     even after an edit that moves it out from under the paint entirely —
//     which is the one case containment alone would miss.
//  2. Some vertex resolves to s right now. Evaluated against post-edit
//     geometry (ReloadSector runs after the caller mutates the points), which
//     is what catches s having just moved UNDER the paint and become its new
//     surface.
//  3. Some vertex resolves to nothing — open ground. A sector moving under it
//     would newly cover it, and there is no earlier answer to compare against.
//
// The case that looks missing — paint that was on s, is not bound to s, and
// has since resolved elsewhere — cannot arise. Unbound paint resolves by
// "lowest floor wins", so it was never on the upper of two stacked surfaces to
// begin with; and if s was the lowest, moving s away means the paint now
// resolves to something that was already losing to s, which only happens when
// s stopped containing it — and then it is bound (1) or open ground (3).
func paintDrapesOn(owner *Chunk, bs *BiomeSector, s *Sector) bool {
	if bs.DrapeSector != "" && bs.DrapeSector == s.ID {
		return true
	}
	lvl := CurrentLevel()
	for _, v := range bs.Points {
		_, got := DrapeTarget(lvl, owner, bs.DrapeSector, v.X(), v.Y())
		if got == s || got == nil {
			return true
		}
	}
	return false
}

// ForEachBiomeSector runs fn over every biome sector in the edited level,
// handing each one the chunk that owns it.
//
// The whole level, not just ch: a biome polygon can drape over a floor in a
// chunk other than the one it was painted on (FloorHeightNormalUnderLevel
// samples every chunk), so an edit to one chunk's floor can invalidate paint
// belonging to another. The owning chunk is passed because a biome sector's
// Points are in THAT chunk's local space — anything comparing them against
// something from a different chunk has to carry both out to world space first
// (see MarkBiomesDirtyOverBox).
//
// Outside the editor there is no level to scan and the edited chunk is all
// there is to go on — nothing edits geometry there anyway.
//
// The level arrives through CurrentLevel (level_context.go) rather than by
// parameter: every caller in the rebuild chain would otherwise have to thread a
// *Level it does not itself use.
func ForEachBiomeSector(ch *Chunk, fn func(owner *Chunk, bs *BiomeSector)) {
	if lvl := CurrentLevel(); lvl != nil {
		for ci := range lvl.Chunks {
			c := &lvl.Chunks[ci]
			for i := range c.BiomeSectors {
				fn(c, &c.BiomeSectors[i])
			}
		}
		return
	}
	if ch == nil {
		return
	}
	for i := range ch.BiomeSectors {
		fn(ch, &ch.BiomeSectors[i])
	}
}
