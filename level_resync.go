package main

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
// reloadSector is the single place editor code rebuilds a sector after such an
// edit, so it is also where the biomes over that sector are marked for a
// rebuild of their own.

import "github.com/go-gl/mathgl/mgl32"

// reloadSector rebuilds s's geometry from the current chunk points and flags
// every biome sector over it dirty. Editor code should call this rather than
// s.Load directly, so nothing painted on top of the sector is left behind.
func reloadSector(ch *Chunk, s *Sector) error {
	// The edit can move or shrink the sector, so the biomes over where it
	// *was* are exactly as stale as the ones over where it now is: both
	// footprints get marked. (Sector.Load recomputes MinBound/MaxBound, so
	// reading them before and after brackets the change.)
	oldMin, oldMax := s.MinBound, s.MaxBound
	err := s.Load(SectorPoints(ch, s))
	lo := mgl32.Vec2{min(oldMin.X(), s.MinBound.X()), min(oldMin.Y(), s.MinBound.Y())}
	hi := mgl32.Vec2{max(oldMax.X(), s.MaxBound.X()), max(oldMax.Y(), s.MaxBound.Y())}
	markBiomesDirtyOverBox(ch, lo, hi)
	return err
}

// markBiomesDirtyOverBox flags every biome sector whose polygon overlaps the XZ
// box [lo,hi], so its floor-derived meshes are rebuilt on the next frame.
func markBiomesDirtyOverBox(ch *Chunk, lo, hi mgl32.Vec2) {
	forEachBiomeSector(ch, func(bs *BiomeSector) {
		bmin, bmax := bs.Bounds()
		if bmax.X() < lo.X() || bmin.X() > hi.X() || bmax.Y() < lo.Y() || bmin.Y() > hi.Y() {
			return
		}
		// Both flags: a floor that moved changes the height texture the
		// scatter drops onto and the ground patch mesh (and water plane)
		// alike. See BiomeSector's field docs for why they are separate.
		bs.HeightDirty = true
		bs.GroundDirty = true
	})
}

// forEachBiomeSector runs fn over every biome sector in the edited level.
//
// The whole level, not just ch: biome polygons are world-space and live on
// whichever chunk they were painted in, which need not be the chunk holding the
// floor they sit over. Outside the editor there is no level to scan and the
// edited chunk is all there is to go on — nothing edits geometry there anyway.
//
// The level arrives through CurrentLevel (level_context.go) rather than by
// parameter: every caller in the rebuild chain would otherwise have to thread a
// *Level it does not itself use.
func forEachBiomeSector(ch *Chunk, fn func(bs *BiomeSector)) {
	if lvl := CurrentLevel(); lvl != nil {
		for ci := range lvl.Chunks {
			c := &lvl.Chunks[ci]
			for i := range c.BiomeSectors {
				fn(&c.BiomeSectors[i])
			}
		}
		return
	}
	if ch == nil {
		return
	}
	for i := range ch.BiomeSectors {
		fn(&ch.BiomeSectors[i])
	}
}
