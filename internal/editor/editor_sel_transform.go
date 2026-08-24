package editor

// editor_sel_transform.go — moving a multi-vertex selection as one body.
//
// Translation needs nothing but each vertex's own start value: every one takes
// the same offset. Rotation and scale are defined against the selection's
// centre instead, so a vertex cannot be transformed without knowing where its
// neighbours are — which is what makes a multi-selection turn as a rigid body
// rather than each point spinning uselessly about itself.
//
// The awkward part is the write-back. A sector vertex is not stored as a
// position: its XZ is a chunk point shared with every other sector (and every
// other selected handle) that uses that corner, and its Y is a per-vertex
// offset from the sector's floor or ceiling height. So a transform that hands
// back world positions has to be folded into those two stores, and the shared
// XZ has to be reconciled when several selected handles land on the same point
// — the floor and ceiling handles of one corner are two selections of one
// coordinate. They are averaged rather than written in selection order, so the
// result does not depend on which handle was clicked first.

import (
	"foundry/internal/biome"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// selectedVerticesWorld snapshots the world position of every selected vertex,
// in selection order. Entries whose indices no longer resolve are left at the
// origin so the slice stays parallel with Editor.SelectedVertices — the drags
// index into it by selection position.
func selectedVerticesWorld() []mgl32.Vec3 {
	out := make([]mgl32.Vec3, len(Editor.SelectedVertices))
	for i, sel := range Editor.SelectedVertices {
		if pos, ok := vertexSelPos(sel); ok {
			out[i] = pos
		}
	}
	return out
}

// vertexWriteback records what writeSelectedVertexWorld changed, so the rebuild
// that follows knows exactly which chunk points moved and which sectors need
// reloading — and so a test can inspect the write without a GL context, which
// rebuilding geometry needs and headless code has not got.
type vertexWriteback struct {
	moves          map[int][]biome.PointMove // per chunk: the points that moved, and from where
	movedPoints    map[[2]int]struct{}       // chunk+point index of each moved point
	touchedSectors map[[2]int]struct{}       // chunk+sector index of each height change
}

// empty reports whether nothing at all changed, in which case there is nothing
// to rebuild.
func (w vertexWriteback) empty() bool {
	return len(w.moves) == 0 && len(w.touchedSectors) == 0
}

// setSelectedVertexWorld rewrites every selected vertex's world position
// through fn, then rebuilds whatever moved.
//
// fn is handed the vertex's current position for convenience; drags that must
// stay exact over a long gesture ignore it and work from their own mouse-down
// snapshot instead, so a hundred motion events do not accumulate a hundred
// roundings.
func setSelectedVertexWorld(fn func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3) {
	rebuildAfterVertexWrite(writeSelectedVertexWorld(fn))
}

// writeSelectedVertexWorld is setSelectedVertexWorld's write half: it folds the
// positions fn returns into the two stores a sector vertex actually has, and
// reports what it changed. It touches no geometry, so nothing here needs a GL
// context.
func writeSelectedVertexWorld(fn func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3) vertexWriteback {
	out := vertexWriteback{
		moves:          map[int][]biome.PointMove{},
		movedPoints:    map[[2]int]struct{}{},
		touchedSectors: map[[2]int]struct{}{},
	}
	lvl := Editor.level
	if lvl == nil {
		return out
	}

	// Pass one: resolve the target position of every selection, and sum the XZ
	// per shared chunk point so the reconciliation below can average it.
	type xzAccum struct {
		sum mgl32.Vec2
		N   int
	}
	targets := make([]mgl32.Vec3, len(Editor.SelectedVertices))
	resolved := make([]bool, len(Editor.SelectedVertices))
	accum := map[[2]int]*xzAccum{}
	for i, sel := range Editor.SelectedVertices {
		cur, ok := vertexSelPos(sel)
		if !ok {
			continue
		}
		p := fn(i, sel, cur)
		targets[i] = p
		resolved[i] = true
		key := [2]int{sel.chunkIdx, sel.pointIdx}
		a := accum[key]
		if a == nil {
			a = &xzAccum{}
			accum[key] = a
		}
		a.sum = a.sum.Add(mgl32.Vec2{p.X(), p.Z()})
		a.N++
	}

	// Pass two: the shared XZ. Only points that actually moved are recorded, so
	// a pure height drag does not drag the biome layer along with it.
	for key, a := range accum {
		if key[0] < 0 || key[0] >= len(lvl.Chunks) {
			continue
		}
		ch := &lvl.Chunks[key[0]]
		if key[1] < 0 || key[1] >= len(ch.Points) {
			continue
		}
		want := a.sum.Mul(1 / float32(a.N))
		was := ch.Points[key[1]]
		// Exact, not approximate: every drag is applied absolutely from its
		// mouse-down snapshot, so a still cursor recomputes the identical value
		// and skips the rebuild, while a real move of any size gets through.
		if was == want {
			continue
		}
		ch.Points[key[1]] = want
		out.moves[key[0]] = append(out.moves[key[0]], biome.PointMove{PointIdx: key[1], From: was})
		out.movedPoints[key] = struct{}{}
	}

	// Pass three: the per-vertex height offset, which is the only per-vertex
	// height control a sector has.
	for i, sel := range Editor.SelectedVertices {
		if !resolved[i] {
			continue
		}
		_, s := sectorOf(sel)
		if s == nil {
			continue
		}
		base := s.Ceiling.Height
		if sel.isFloor {
			base = s.Floor.Height
		}
		want := targets[i].Y() - base
		if vertexOffset(s, sel) == want {
			continue
		}
		setVertexOffset(s, sel, want)
		out.touchedSectors[[2]int{sel.chunkIdx, sel.sectorIdx}] = struct{}{}
	}
	return out
}

// rebuildAfterVertexWrite regenerates the geometry a write invalidated.
//
// Points first and all at once, so a rotation warps its sectors in one step
// rather than once per corner; then the sectors whose heights changed but whose
// outline did not, which the point pass never visited.
func rebuildAfterVertexWrite(w vertexWriteback) {
	lvl := Editor.level
	if lvl == nil || w.empty() {
		return
	}
	moves, movedPoints, touchedSectors := w.moves, w.movedPoints, w.touchedSectors

	for ci, ms := range moves {
		biome.CarryBiomesWithPoints(&lvl.Chunks[ci], ms)
	}
	// Edge points are re-derived for every sector of a touched chunk, not only
	// the selected ones: a moved chunk point may well be a corner of a sector
	// nothing in the selection belongs to.
	for ci := range moves {
		ch := &lvl.Chunks[ci]
		for si := range ch.Sectors {
			rebuildSectorEdgePoints(ch, &ch.Sectors[si])
		}
	}
	rebuilt := map[[2]int]struct{}{}
	for key := range movedPoints {
		ch := &lvl.Chunks[key[0]]
		for si := range ch.Sectors {
			if sectorVertexPos(&ch.Sectors[si], key[1]) < 0 {
				continue
			}
			if _, done := rebuilt[[2]int{key[0], si}]; done {
				continue
			}
			rebuilt[[2]int{key[0], si}] = struct{}{}
			_ = world.ReloadSector(ch, &ch.Sectors[si])
		}
	}
	for key := range touchedSectors {
		if _, done := rebuilt[key]; done {
			continue
		}
		ch := &lvl.Chunks[key[0]]
		if key[1] < 0 || key[1] >= len(ch.Sectors) {
			continue
		}
		rebuilt[key] = struct{}{}
		_ = world.ReloadSector(ch, &ch.Sectors[key[1]])
	}

	touchedChunks := map[int]struct{}{}
	for key := range rebuilt {
		touchedChunks[key[0]] = struct{}{}
	}
	for ci := range moves {
		touchedChunks[ci] = struct{}{}
	}
	for ci := range touchedChunks {
		lvl.Chunks[ci].RebuildGaps()
	}
}

// ---------- the transforms themselves ----------

// rotatePointAbout turns p by angle radians about the line through center along
// axis.
func rotatePointAbout(p, center, axis mgl32.Vec3, angle float32) mgl32.Vec3 {
	if axis.Len() < 1e-6 {
		return p
	}
	rot := mgl32.HomogRotate3D(angle, axis.Normalize()).Mat3()
	return center.Add(rot.Mul3x1(p.Sub(center)))
}

// scalePointAbout moves p toward or away from center by a per-axis factor.
//
// This is what the scale tool does to a selection, and it is deliberately *not*
// a resize: sector vertices have no size to change, so spreading them about
// their shared centre is the whole of what "scale the selection" can mean here.
func scalePointAbout(p, center, factors mgl32.Vec3) mgl32.Vec3 {
	d := p.Sub(center)
	return center.Add(mgl32.Vec3{d.X() * factors.X(), d.Y() * factors.Y(), d.Z() * factors.Z()})
}

// rotateXZAbout turns the XZ of p about center's XZ, leaving Y alone — the
// top-down map's only rotation, since a turn about any other axis would move
// points out of the plane the map draws.
func rotateXZAbout(p, center mgl32.Vec2, angle float32) mgl32.Vec2 {
	sin, cos := float32(math.Sin(float64(angle))), float32(math.Cos(float64(angle)))
	d := p.Sub(center)
	return center.Add(mgl32.Vec2{d.X()*cos - d.Y()*sin, d.X()*sin + d.Y()*cos})
}

// selectionCenterXZ returns the XZ centre of whatever the 2D map's gizmo acts
// on: the selected vertices, the selected entity, or the spawn marker.
func selectionCenterXZ() (mgl32.Vec2, bool) {
	if len(Editor.SelectedVertices) > 0 {
		if c, ok := selectedVerticesCenter(); ok {
			return mgl32.Vec2{c.X(), c.Z()}, true
		}
		return mgl32.Vec2{}, false
	}
	if e := activeGizmoEntity(); e != nil {
		return mgl32.Vec2{e.Position.X(), e.Position.Z()}, true
	}
	if s := activeGizmoSpawn(); s != nil {
		return mgl32.Vec2{s.Position.X(), s.Position.Z()}, true
	}
	return mgl32.Vec2{}, false
}
