package world

// brush_dynamic.go — a brush a script moves, and the placement that makes it
// move.
//
// # Why a placement and not just moving the verts
//
// A brush's Verts are its authored shape, in chunk coordinates. Moving a door
// by rewriting them would work exactly once: it would rebuild the face meshes
// every frame it moved, it would make the level file's contents depend on where
// the door happened to stop, and leaving play would have nothing to restore to.
//
// So a moving brush keeps its authored corners and carries a TRANSFORM on top:
// Offset, Rotation and Scale, with Velocity and AngularVelocity the rates that
// carry the first two, all runtime-only. Nothing here is serialized,
// nothing survives a reload, and the geometry on disk is always the brush at
// rest. That is the same bargain Chunk.Origin strikes for a whole chunk, one
// level down.
//
// # What the transform reaches
//
//   - Drawing: the face meshes are uploaded once, in authored coordinates, and
//     the draw multiplies Placement into the chunk's model matrix. Moving a
//     brush uploads nothing (internal/app and render/shadow.go). The world
//     shader carries normals through the inverse transpose, so a brush scaled
//     unevenly still lights correctly.
//   - Collision: the convex hull the resolver pushes bodies out of is the same
//     vertex list with the same transform applied — collision.ConvexHull.Xform
//     — so what a body hits is where the brush is drawn, which is the whole
//     point of the feature. The hull's support function holds for any linear
//     map, so a scaled brush is as solid as a turned one.
//   - Extent: LocalAABB reports where the brush IS, so the chunk's bounds, the
//     spatial hash and every frustum cull follow it. AdvanceBrushes re-buckets
//     the hash after integrating, and so does the `position` property's setter.
//   - Compiling: a dynamic brush is left out of the static bake entirely (see
//     compile.BakesBrush) — it is not clipped, it does not clip its neighbours,
//     it is not a solid leaf of the BSP and it does not help seal the level.
//     It stays a live brush that a compiled level draws and collides the
//     ordinary way.
//
// # What it does NOT do
//
// Nothing rides a moving brush. A body standing on a lift is not carried by it
// — the brush moves out from under the body, and the body falls or is pushed by
// the next frame's resolve. Carrying riders is a chunk-scale feature
// (Entity.AttachedChunk) and a brush has no counterpart yet.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// AtRest reports whether the brush is where it was authored — no offset, no
// rotation, no scale. The fast path every consumer of Placement takes first,
// since all but a handful of brushes in a level are at rest for the whole of it.
func (b *Brush) AtRest() bool {
	return b.Offset == mgl32.Vec3{} && b.Rotation == mgl32.Vec3{} && b.unscaled()
}

// unscaled reports whether Scale leaves the brush its authored size. Both the
// zero value and an explicit {1,1,1} do: the first is what every brush nobody
// has scaled holds, the second what one scaled back to normal holds.
func (b *Brush) unscaled() bool {
	return b.Scale == (mgl32.Vec3{}) || b.Scale == (mgl32.Vec3{1, 1, 1})
}

// EffectiveScale is Scale with its zero value read as no scale at all.
func (b *Brush) EffectiveScale() mgl32.Vec3 { return effectiveScale3(b.Scale) }

// Pivot is the point Rotation turns about, Scale grows about, and `position`
// reports: the average of the brush's authored corners, in chunk coordinates.
//
// The average rather than the centre of the bounding box, because it is the
// same choice collision.ConvexHull.Center makes about the same vertex list, and
// two answers to "where is this brush" that disagreed by a few centimetres
// would be a bug waiting for a level to lean on it.
func (b *Brush) Pivot() mgl32.Vec3 {
	if len(b.Verts) == 0 {
		return mgl32.Vec3{}
	}
	var sum mgl32.Vec3
	for _, v := range b.Verts {
		sum = sum.Add(v)
	}
	return sum.Mul(1 / float32(len(b.Verts)))
}

// Placement is the transform from the brush's authored corners to where it
// currently is, in the chunk's own coordinates. The identity for a brush at
// rest.
//
// Rotation and Scale are applied about Pivot rather than about the chunk
// origin, so a door authored twenty metres out turns on its own centre rather
// than swinging round the room, and a crate grown to twice its size grows in
// place rather than away from the origin. Scale is applied first, in the
// brush's own authored axes, so a slab stretched along X and then turned stays
// a stretched slab rather than shearing.
func (b *Brush) Placement() mgl32.Mat4 {
	if b.AtRest() {
		return mgl32.Ident4()
	}
	m := mgl32.Translate3D(b.Offset.X(), b.Offset.Y(), b.Offset.Z())
	scaled := !b.unscaled()
	if b.Rotation != (mgl32.Vec3{}) || scaled {
		p := b.Pivot()
		m = m.Mul4(mgl32.Translate3D(p.X(), p.Y(), p.Z()))
		if b.Rotation != (mgl32.Vec3{}) {
			m = m.Mul4(EulerMatrixYXZ(b.Rotation))
		}
		if scaled {
			m = m.Mul4(mgl32.Scale3D(b.Scale.X(), b.Scale.Y(), b.Scale.Z()))
		}
		m = m.Mul4(mgl32.Translate3D(-p.X(), -p.Y(), -p.Z()))
	}
	return m
}

// Position is where the brush is now, in chunk coordinates: its pivot carried
// through Placement. Rotation and scale about the pivot leave the pivot alone,
// so this is Pivot plus Offset — written out the long way so that it stays true
// of whatever Placement grows into.
func (b *Brush) Position() mgl32.Vec3 {
	if b.AtRest() {
		return b.Pivot()
	}
	return mulPoint(b.Placement(), b.Pivot())
}

// MoveTo places the brush's pivot at p, in chunk coordinates. The inverse of
// Position, and the only way anything outside this file should be writing
// Offset.
func (b *Brush) MoveTo(p mgl32.Vec3) { b.Offset = p.Sub(b.Pivot()) }

// PlacedVerts returns the brush's corners where they currently are. Allocates,
// so it is for the callers that need a slice (a compile-time walk, a test);
// the per-frame paths transform as they go instead.
func (b *Brush) PlacedVerts() []mgl32.Vec3 {
	if b.AtRest() {
		return b.Verts
	}
	m := b.Placement()
	out := make([]mgl32.Vec3, len(b.Verts))
	for i, v := range b.Verts {
		out[i] = mulPoint(m, v)
	}
	return out
}

// PlacedFacePoints is FacePoints where the brush currently is — the authored
// corners of one face carried through Placement.
//
// FacePoints itself deliberately stays in authored coordinates: it is what the
// face meshes are built from, and those are uploaded once and then moved by the
// draw's model matrix rather than rebuilt. This is for the callers that need
// real positions instead — a particle testing what it landed on, a test.
func (b *Brush) PlacedFacePoints(f *BrushFace) []mgl32.Vec3 {
	pts := b.FacePoints(f)
	if b.AtRest() {
		return pts
	}
	m := b.Placement()
	for i := range pts {
		pts[i] = mulPoint(m, pts[i])
	}
	return pts
}

// PlacedFaceNormal is FaceNormal carried through the brush's current rotation
// and scale. A pure translation leaves a normal alone, so this is the identity
// for the common case of a brush that only slides.
//
// A normal is not carried the way a point is: the inverse transpose of R·S is
// R·S⁻¹, so a face that slopes across an axis being stretched tilts TOWARD the
// others rather than away. Dividing by the scale before turning is that.
func (b *Brush) PlacedFaceNormal(f *BrushFace) mgl32.Vec3 {
	n := b.FaceNormal(f)
	scaled := !b.unscaled()
	if b.Rotation == (mgl32.Vec3{}) && !scaled {
		return n
	}
	if scaled {
		s := b.Scale
		n = mgl32.Vec3{n.X() / s.X(), n.Y() / s.Y(), n.Z() / s.Z()}
	}
	r := EulerMatrixYXZ(b.Rotation).Mul4x1(n.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}.Normalize()
}

// mulPoint applies an affine matrix to a point.
func mulPoint(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// HasDynamicBrushes reports whether any of the chunk's brushes is one a script
// can move. The question a compiled chunk asks before walking its brush list at
// all: its static geometry is drawn out of the compiled surfaces, so unless it
// holds a dynamic brush there is nothing left in it to draw.
func (ch *Chunk) HasDynamicBrushes() bool {
	for bi := range ch.Brushes {
		if ch.Brushes[bi].Dynamic {
			return true
		}
	}
	return false
}

// AdvanceBrushes moves every moving brush in the level on by dt seconds and
// reports whether any of them moved: the ones following a waypoint animation
// along it (brush_waypoints.go), and the rest by their rates.
//
// The brush counterpart of AdvanceChunks, and it runs on the same tick for the
// same reason: a lift that slides on its own is authored state, not a script
// writing a position every frame. Velocity is in the chunk's own coordinates,
// the space Offset is measured in, and AngularVelocity is in the same Euler
// basis as Rotation — a turn about the brush's own pivot, not about anything
// the chunk does.
//
// A brush following waypoints is driven by them alone, and its rates wait. Two
// things writing one placement would have the door arrive somewhere no arrow
// is, and the animation is the more specific instruction of the two; the rates
// take over again from wherever the path left the brush.
//
// Rotation wraps into [-π, π] as it is integrated rather than growing without
// bound, for the reason Entity.Integrate gives: a door left spinning for an
// hour would otherwise reach an angle whose float32 resolution is coarser than
// a degree, and the spin would visibly quantise.
//
// A disabled chunk's brushes do not move. They are out of the world entirely,
// so integrating them would be simulating something that is not there — the
// rule AdvanceChunks states at greater length.
func AdvanceBrushes(lvl *Level, dt float32) bool {
	if lvl == nil || dt == 0 {
		return false
	}
	moved := false
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue
		}
		chunkMoved := false
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.Dynamic {
				continue
			}
			if b.Waypoints != nil {
				if advanceBrushWaypoints(lvl, ch, b, dt) {
					chunkMoved = true
				}
				continue
			}
			// A channel a script is easing is written by its tween alone, and
			// the rate that would move it waits (tween.go). Read before the
			// step, so a tween that arrives this tick does not also let its rate
			// run on top of the exact value it landed on.
			easingMove, easingTurn := b.Tweens.Easing(TweenMove), b.Tweens.Easing(TweenRotate)
			if b.Tweens.Active() && advanceBrushTweens(ch, b, dt) {
				chunkMoved = true
			}
			if b.Velocity != (mgl32.Vec3{}) && !easingMove {
				b.Offset = b.Offset.Add(b.Velocity.Mul(dt))
				chunkMoved = true
			}
			if b.AngularVelocity != (mgl32.Vec3{}) && !easingTurn {
				r := b.Rotation.Add(b.AngularVelocity.Mul(dt))
				b.Rotation = mgl32.Vec3{WrapAngle(r.X()), WrapAngle(r.Y()), WrapAngle(r.Z())}
				chunkMoved = true
			}
		}
		if chunkMoved {
			// The chunk's own extent is the union of its brushes', so a brush
			// that moved past it has to grow it before anything culls or
			// buckets against the old one.
			ch.RecomputeBounds()
			moved = true
		}
	}
	if moved {
		RebuildSpatialHash(lvl)
	}
	return moved
}
