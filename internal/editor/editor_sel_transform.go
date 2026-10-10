package editor

// editor_sel_transform.go — moving a multi-vertex selection as one body.
//
// Translation needs nothing but each vertex's own start value: every one takes
// the same offset. Rotation and scale are defined against the selection's
// centre instead, so a vertex cannot be transformed without knowing where its
// neighbours are — which is what makes a multi-selection turn as a rigid body
// rather than each point spinning uselessly about itself.
//
// The write-back used to be the awkward part. A *sector* vertex was not stored
// as a position: its XZ was a chunk point shared with every other sector that
// used that corner, and its Y was a per-vertex offset from the sector's floor
// or ceiling height — so a transform handing back world positions had to be
// folded into two stores, and shared points had to be reconciled by averaging.
//
// A brush vertex is a position. It is owned outright by one brush and shared
// with nothing, so the write is a write: carry the world target back into the
// chunk's local space and store it. All of that machinery went with the sector.
//
// What remains is the chunk transform. Everything above this layer — the
// gizmos, the selection centre — works in world space, and ch.LocalToWorld /
// ch.WorldToLocal are the only two places that know a chunk can be moved,
// rotated or scaled at all.

import (
	"math"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// selectedVerticesWorld snapshots the world position of every vertex handle the
// gizmo is dragging, in selection order. Entries whose indices no longer
// resolve are left at the origin so the slice stays parallel with
// gizmoVertexSels — the drags index into it by position.
func selectedVerticesWorld() []mgl32.Vec3 {
	sels := gizmoVertexSels()
	out := make([]mgl32.Vec3, len(sels))
	for i, sel := range sels {
		if pos, ok := vertexSelPos(sel); ok {
			out[i] = pos
		}
	}
	return out
}

// vertexWriteback records which brushes a write actually changed, so the
// rebuild that follows reloads exactly those — and so a test can inspect the
// write without a GL context, which rebuilding geometry needs and headless code
// has not got.
type vertexWriteback struct {
	touchedBrushes map[[2]int]struct{} // chunk+brush index of each moved brush
}

// empty reports whether nothing at all changed, in which case there is nothing
// to rebuild.
func (w vertexWriteback) empty() bool { return len(w.touchedBrushes) == 0 }

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

// writeSelectedVertexWorld is setSelectedVertexWorld's write half: it stores
// the positions fn returns and reports which brushes it touched. It touches no
// geometry, so nothing here needs a GL context.
func writeSelectedVertexWorld(fn func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3) vertexWriteback {
	out := vertexWriteback{touchedBrushes: map[[2]int]struct{}{}}
	if ws().level == nil {
		return out
	}
	// Whatever the gizmo is dragging: the vertex selection, or the handles a
	// face selection is built from (editor_face_gizmo.go). Both are the same
	// list of handles by the time they get here.
	for i, sel := range gizmoVertexSels() {
		ch, b := vertexSelBrush(sel)
		// Which of the brush's two meshes the index belongs to. A gizmo drag
		// in object mode carries handles from BOTH, so this is decided per
		// handle rather than once for the drag — see vertexSel.detail.
		m := selMesh(b, sel.detail)
		if m == nil || sel.vertIdx < 0 || sel.vertIdx >= len(m.Verts) {
			continue
		}
		cur, ok := vertexSelPos(sel)
		if !ok {
			continue
		}
		// fn works in world space, same as vertexSelPos read it; the store is
		// chunk-local.
		want := ch.WorldToLocal(fn(i, sel, cur))
		// Exact, not approximate: every drag is applied absolutely from its
		// mouse-down snapshot, so a still cursor recomputes the identical value
		// and skips the rebuild, while a real move of any size gets through.
		if m.Verts[sel.vertIdx] == want {
			continue
		}
		m.Verts[sel.vertIdx] = want
		out.touchedBrushes[[2]int{sel.chunkIdx, sel.brushIdx}] = struct{}{}
	}
	return out
}

// setSelectedVertexLocals is setSelectedVertexWorld in the chunk's own space,
// for the edits that are defined there rather than in the world: grid snapping
// wants to land on the chunk's grid, not on a world grid the chunk's own
// rotation would have skewed it off.
func setSelectedVertexLocals(fn func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3) {
	out := vertexWriteback{touchedBrushes: map[[2]int]struct{}{}}
	if ws().level != nil {
		for i, sel := range gizmoVertexSels() {
			_, b := vertexSelBrush(sel)
			m := selMesh(b, sel.detail)
			if m == nil || sel.vertIdx < 0 || sel.vertIdx >= len(m.Verts) {
				continue
			}
			want := fn(i, sel, m.Verts[sel.vertIdx])
			if m.Verts[sel.vertIdx] == want {
				continue
			}
			m.Verts[sel.vertIdx] = want
			out.touchedBrushes[[2]int{sel.chunkIdx, sel.brushIdx}] = struct{}{}
		}
	}
	rebuildAfterVertexWrite(out)
}

// rebuildAfterVertexWrite regenerates the geometry a write invalidated: each
// touched brush once, then its chunk's bounds, which a moved corner can have
// grown or shrunk.
func rebuildAfterVertexWrite(w vertexWriteback) {
	lvl := ws().level
	if lvl == nil || w.empty() {
		return
	}
	chunks := map[int]struct{}{}
	for key := range w.touchedBrushes {
		if key[0] < 0 || key[0] >= len(lvl.Chunks) {
			continue
		}
		ch := &lvl.Chunks[key[0]]
		if key[1] < 0 || key[1] >= len(ch.Brushes) {
			continue
		}
		_ = ch.Brushes[key[1]].Load()
		chunks[key[0]] = struct{}{}
	}
	for ci := range chunks {
		lvl.Chunks[ci].RecomputeBounds()
	}
	// Once for the whole write rather than once per chunk — see
	// editorChunkGeometryChanged. A vertex dragged past the chunk's old extent
	// is exactly the case that re-buckets it.
	world.RebuildSpatialHash(lvl)
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
// a resize: a vertex has no size to change, so spreading a group of them about
// their shared centre is the whole of what "scale the selection" can mean here.
// Scaling a whole brush is exactly this applied to all of its corners, which is
// why the tool needs no separate brush path.
func scalePointAbout(p, center, factors mgl32.Vec3) mgl32.Vec3 {
	d := p.Sub(center)
	return center.Add(mgl32.Vec3{d.X() * factors.X(), d.Y() * factors.Y(), d.Z() * factors.Z()})
}

// rotateXZAbout turns the XZ of p about center's XZ, leaving Y alone.
func rotateXZAbout(p, center mgl32.Vec2, angle float32) mgl32.Vec2 {
	sin, cos := float32(math.Sin(float64(angle))), float32(math.Cos(float64(angle)))
	d := p.Sub(center)
	return center.Add(mgl32.Vec2{d.X()*cos - d.Y()*sin, d.X()*sin + d.Y()*cos})
}

// selectionCenterXZ returns the XZ centre of whatever the gizmo acts on: the
// selected vertices, the selected entity, or the spawn marker.
func selectionCenterXZ() (mgl32.Vec2, bool) {
	if len(ws().SelectedVertices) > 0 {
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
