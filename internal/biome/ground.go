package biome

// ground.go — finding the ground under a scatter volume.
//
// A scatter volume says "grow grass in this region" (world/volume_scatter.go).
// What it does NOT say is where the ground is: the instances have to land on
// whatever geometry happens to be underneath, and the author is not going to
// trace the floor by hand.
//
// The painted model did not have this problem. A biome polygon was drawn ON a
// sector's floor, so the floor under any point was a lookup: the sector it was
// painted on, at that XZ. A volume is placed in space and the ground under it
// is whatever brush face a ray finds, so this file casts one.
//
// # Why a downward ray and not the collision resolver
//
// internal/collision has a general Raycast, and this could call it. It does not,
// for two reasons. It would put a dependency on the collision resolver into the
// render path, where the only question being asked is a geometric one; and the
// general cast reports entities, volumes, scenery and blocking rules that a
// blade of grass has no opinion about. What scatter needs is narrower and
// cheaper: the highest brush surface below a point, and its normal.
//
// # The surface IS the plane
//
// A brush face is flat. It has no displacement layer riding on top of it — the
// per-face procedural detail that used to put one there is gone, and organic
// shape comes from subdividing the brush's own geometry instead — so the
// triangles this ray meets are the drawn surface, and there is nothing to
// correct the hit against afterwards.

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// SlopeFeatherDegrees is how wide the slope gate's transition is, in degrees
// either side of the authored limit. A hard cut-off makes a visible line
// across a hillside where the grass stops; feathering it thins the instances
// out over a few degrees instead.
const SlopeFeatherDegrees = float32(6)

// Surface is what the ground under one point turns out to be.
type Surface struct {
	// Height is the world Y of the drawn surface — the face's own displaced
	// height where it carries displacement detail, its flat plane otherwise.
	Height float32
	// Normal is the unit normal of that surface at this point, which is what
	// the slope gate is measured against.
	Normal mgl32.Vec3
	// Found is false when the ray met nothing at all, which is a scatter
	// volume floating in the air with no floor under part of it. The caller
	// grows nothing there rather than falling back to y=0, because a field of
	// grass hanging at the world origin under a raised platform is a worse
	// answer than a bare patch.
	Found bool
}

// SurfaceUnder casts a ray straight down from (wx, topY, wz) and reports the
// highest solid surface it meets within maxDrop.
//
// Every brush in the level is a candidate, not only those in one chunk: a
// volume is placed in world space and routinely spans a chunk boundary, which
// is the whole reason it is level-scoped in the first place.
func SurfaceUnder(lvl *world.Level, wx, topY, wz, maxDrop float32) Surface {
	if lvl == nil || maxDrop <= 0 {
		return Surface{}
	}
	origin := mgl32.Vec3{wx, topY, wz}
	best := Surface{}
	bestT := maxDrop

	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue // not drawn and not solid, so nothing to stand on
		}
		// A quick reject on the chunk's own bounds before any triangle work.
		// The column is a vertical segment, so only the XZ overlap and the
		// height range matter.
		lo, hi := ch.WorldBounds()
		if wx < lo.X() || wx > hi.X() || wz < lo.Z() || wz > hi.Z() {
			continue
		}
		if hi.Y() < topY-maxDrop || lo.Y() > topY {
			continue
		}
		model := ch.ModelMatrix()

		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.Solid {
				// Decorative geometry is not ground. A body walks through it,
				// and grass growing on it would be growing on nothing.
				continue
			}
			// The DRAWN mesh: what grass grows on is the surface an author can
			// see, and its faces index that mesh's own vertex pool — asking the
			// brush for a detail face's corners would resolve them against the
			// hull's.
			drawn := b.DrawMesh()
			faces := drawn.Faces
			for fi := range faces {
				f := &faces[fi]
				if f.NoDraw || f.NoCollide {
					continue
				}
				// Only faces that could be walked on. A wall's triangles are
				// on the column too — every enclosed room has four of them
				// around it — and landing an instance on one would grow grass
				// sideways out of it.
				n := ch.LocalToWorldDirection(drawn.FaceNormal(f))
				if n.Y() <= 0 {
					continue
				}
				t, ok := columnHitsFace(origin, maxDrop, f.Geometry.Vertices, model)
				if !ok || t >= bestT {
					continue
				}
				bestT = t
				best = Surface{Height: topY - t, Normal: n.Normalize(), Found: true}
			}
		}
	}
	return best
}

// columnHitsFace intersects a straight-down ray with one face's triangles and
// returns the nearest distance along it.
//
// The ray is rewritten in the chunk's own space rather than the triangles being
// carried into the world, exactly as the editor's picker does: the inverse
// transform scales origin and direction together, so the returned distance is
// still the world-space one and is directly comparable across chunks.
func columnHitsFace(origin mgl32.Vec3, maxDrop float32, verts []float32, model mgl32.Mat4) (float32, bool) {
	if len(verts) < 15 {
		return 0, false
	}
	dir := mgl32.Vec3{0, -1, 0}
	if model != mgl32.Ident4() {
		inv := model.Inv()
		if inv == (mgl32.Mat4{}) {
			return 0, false
		}
		o4 := inv.Mul4x1(origin.Vec4(1))
		d4 := inv.Mul4x1(dir.Vec4(0))
		origin = mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}
		dir = mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}
	}

	best := float32(math.MaxFloat32)
	found := false
	for i := 0; i+14 < len(verts); i += 15 {
		a := mgl32.Vec3{verts[i+0], verts[i+1], verts[i+2]}
		b := mgl32.Vec3{verts[i+5], verts[i+6], verts[i+7]}
		c := mgl32.Vec3{verts[i+10], verts[i+11], verts[i+12]}
		t, ok := rayTriangle(origin, dir, a, b, c)
		if !ok || t < 0 || t > maxDrop || t >= best {
			continue
		}
		best, found = t, true
	}
	return best, found
}

// rayTriangle is Möller–Trumbore, two-sided: a face is ground whichever way its
// triangles happen to be wound, since the caller has already decided which way
// the face itself points.
func rayTriangle(origin, dir, a, b, c mgl32.Vec3) (float32, bool) {
	const eps = 1e-7
	e1, e2 := b.Sub(a), c.Sub(a)
	p := dir.Cross(e2)
	det := e1.Dot(p)
	if det > -eps && det < eps {
		return 0, false // parallel to the triangle's plane
	}
	inv := 1 / det
	tv := origin.Sub(a)
	u := tv.Dot(p) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	q := tv.Cross(e1)
	v := dir.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return 0, false
	}
	return e2.Dot(q) * inv, true
}

// SlopeCoverage is how much of an instance survives at a surface with this
// normal, in [0,1]: 1 inside the authored slope range, 0 outside it, feathered
// over SlopeFeatherDegrees at each end.
//
// The normal is normalised here rather than trusted. A face normal built from a
// cross product has a length that scales with the triangle's area — a 16-unit
// floor quad yields something like (0, 256, -128) — and reading .Y off that
// and clamping into acos's domain pins every such normal at 0 degrees, so the
// gate silently believes every sloped surface in the level is perfectly flat.
func SlopeCoverage(s *world.VolumeScatter, n mgl32.Vec3) float32 {
	minA := world.ResolvedMinSlope(s)
	maxA := world.ResolvedMaxSlope(s)

	l := n.Len()
	if l < 1e-8 {
		return 1 // degenerate normal: no basis to exclude anything on
	}
	y := n.Y() / l
	if y > 1 {
		y = 1
	} else if y < -1 {
		y = -1
	}
	angle := float32(math.Acos(float64(y))) * 180 / math.Pi

	lo := float32(1)
	if minA > 0 {
		lo = geom.Smoothstep32(minA-SlopeFeatherDegrees, minA+SlopeFeatherDegrees, angle)
	}
	hi := float32(1)
	if maxA < 90 {
		hi = 1 - geom.Smoothstep32(maxA-SlopeFeatherDegrees, maxA+SlopeFeatherDegrees, angle)
	}
	return geom.Clamp01(lo * hi)
}
