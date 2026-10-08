package collision

// scenery_mesh.go — bodies, rays and particles against a cooked mesh's own
// collision, in the hulls and mesh modes (docs/nanite/PHASE_2b.md).
//
// A prop in the box mode is resolved as it always was: an immovable proxy
// entity, pushed apart from bodies after they move (scenery_collision.go).
// That is fine for a crate and wrong for a cliff — a box round a cliff is a
// wall where its slope should be — so a cooked mesh offers two better
// answers, and both join the resolve chain a walking body already runs
// (MovePlayer, resolveEntityWorld) rather than being a pass after it. That
// is what gives them step-up, floor and ceiling credit, and the sweep's
// protection against a fast body skipping through, for nothing: a rock is a
// solid exactly as a brush is.
//
//   - hulls: each hull is a ConvexHull shape placed by the placement's
//     matrix (support = the farthest transformed vertex), and the body is
//     pushed out of each in turn by GJK/EPA — pushOutOfChunkBrushes' loop,
//     for the same reasons, over a Compound of the placement's hulls.
//   - mesh: the cut's triangles answer surface.go's probes — walls, floor,
//     ceiling by short rays from inside the body — through their tree in
//     mesh-local space. The ray is carried into the mesh by the placement's
//     inverse and its hit's normal carried back out by the inverse's
//     transpose, so a turned, tilted or unevenly scaled rock is a rock.
//
// Only bodies in world space take part, as with the box mode: a placement
// is in world coordinates and a rider in a ship describes itself in the
// ship's.

import (
	"math"

	"foundry/internal/meshcollide"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// pushOutOfScenery resolves a cylindrical body — centre pos, radius,
// vertical half-height, in world space — against every hulls- and mesh-mode
// placement its box reaches. Box-mode placements are not here: they are
// resolved by ResolveSceneryCollisions.
func pushOutOfScenery(lvl *world.Level, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	ix := sceneryIndexFor(lvl)
	if ix == nil || radius <= 0 || halfHeight <= 0 {
		return
	}
	start := profStart()
	defer profStop(start, &sceneryStats.resolveTime)

	const margin = float32(0.05)
	half := mgl32.Vec3{radius + margin, halfHeight + margin, radius + margin}
	var near [16]*sceneryEntry
	cand := near[:0]
	ix.overlapping(out.Sub(half), out.Add(half), func(e *sceneryEntry) bool {
		if e.mode == meshcollide.ModeHulls || e.mode == meshcollide.ModeMesh {
			cand = append(cand, e)
		}
		return true
	})
	for _, e := range cand {
		var f, c bool
		switch e.mode {
		case meshcollide.ModeHulls:
			out, f, c = pushOutOfHulls(e, out, radius, halfHeight)
		case meshcollide.ModeMesh:
			out, f, c = pushOutOfSurface(meshSurface{e: e, r: residentFor(e.data)}, out, radius, halfHeight)
		}
		hitFloor = hitFloor || f
		hitCeiling = hitCeiling || c
	}
	return
}

// hullsOf is a hulls-mode placement as a Compound of placed ConvexHulls.
func hullsOf(e *sceneryEntry) Compound {
	parts := make([]Shape, len(e.data.Hulls))
	for i := range e.data.Hulls {
		parts[i] = ConvexHull{Pts: e.data.Hulls[i].Pts, Xform: e.m}
	}
	return Compound{Parts: parts}
}

// pushOutOfHulls is pushOutOfChunkBrushes' loop over one placement's hulls:
// each hull the body's box reaches, GJK/EPA, push out past the reported
// depth, and floor/ceiling credit by the push's slope.
func pushOutOfHulls(e *sceneryEntry, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	body := Cylinder{C: out, Half: mgl32.Vec3{radius, halfHeight, radius}, Rot: mgl32.Ident4()}
	bestFloorCos := float32(0)
	for hi := range e.data.Hulls {
		if !e.hullBoxes[hi].touches(body.C, body.Half) {
			continue
		}
		hull := ConvexHull{Pts: e.data.Hulls[hi].Pts, Xform: e.m}
		n, depth, ok := Penetration(body, hull)
		if !ok || depth <= 0 {
			continue
		}
		const separationEpsilon = float32(0.001)
		push := n.Mul(-(depth + separationEpsilon))
		out = out.Add(push)
		body = Translate(body, push).(Cylinder)
		mag := push.Len()
		if mag < 1e-9 {
			continue
		}
		cos := push.Y() / mag
		switch {
		case cos > 0:
			if cos >= MaxSlopeCos && cos > bestFloorCos {
				bestFloorCos = cos
				hitFloor = true
			}
		case cos < 0:
			if -cos >= MaxSlopeCos {
				hitCeiling = true
			}
		}
	}
	return
}

// meshSurface is a mesh-mode placement as a probeSurface, in world space.
type meshSurface struct {
	e *sceneryEntry
	r *meshcollide.Resident
}

func (s meshSurface) probe(o, d mgl32.Vec3, maxT float32) (float32, mgl32.Vec3, bool) {
	lo, ld := s.e.local(o, d)
	t, tri, ok := s.r.Raycast(lo, ld, maxT)
	if !ok {
		return 0, mgl32.Vec3{}, false
	}
	n := s.e.normalOut(s.r.Normals[tri])
	// Facing back along the ray: the sheet is two-sided, and the side a body
	// meets it from is the side it is pushed out to.
	if n.Dot(d) > 0 {
		n = n.Mul(-1)
	}
	return t, n, true
}

func (s meshSurface) near(lo, hi mgl32.Vec3) bool {
	if !boxTouches(s.e.box, lo, hi) {
		return false
	}
	l := aabb{lo: lo, hi: hi}.transformed(s.e.inv)
	return s.r.NearBox(l.lo, l.hi)
}

// local carries a world ray into the placement's mesh space. The direction
// is carried as it is, not renormalised, so the hit's t along it is the
// world distance.
func (e *sceneryEntry) local(o, d mgl32.Vec3) (mgl32.Vec3, mgl32.Vec3) {
	o4 := e.inv.Mul4x1(o.Vec4(1))
	d4 := e.inv.Mul4x1(d.Vec4(0))
	return mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}, mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}
}

// normalOut carries a mesh-space normal into world space (by the inverse's
// transpose, which keeps it perpendicular under uneven scale), unit length.
func (e *sceneryEntry) normalOut(n mgl32.Vec3) mgl32.Vec3 {
	w := transposeMulDir(e.inv, n)
	if l := w.Len(); l > 1e-12 {
		return w.Mul(1 / l)
	}
	return w
}

// ---------- rays ----------

// sceneryRay casts a world ray against one placement in its mode, and
// returns the distance to the hit and the surface's world normal.
func sceneryRay(lvl *world.Level, e *sceneryEntry, origin, dir mgl32.Vec3, tMax float32) (float32, mgl32.Vec3, bool) {
	switch e.mode {
	case meshcollide.ModeBox:
		p := &e.proxy
		m := mgl32.Translate3D(p.Position.X(), p.Position.Y(), p.Position.Z()).Mul4(world.EulerMatrixYXZ(p.Rotation))
		return rayBoxNormal(origin, dir, m, p.BoundHalfExtents(), tMax)
	case meshcollide.ModeHulls:
		lo, ld := e.local(origin, dir)
		best, bestN, found := tMax, mgl32.Vec3{}, false
		for hi := range e.data.Hulls {
			if _, ok := newRayDir(origin, dir).enterBox(e.hullBoxes[hi], best); !ok {
				continue
			}
			if t, n, ok := rayConvex(lo, ld, e.data.Hulls[hi].Planes, best); ok && t <= best {
				best, bestN, found = t, n, true
			}
		}
		if !found {
			return 0, mgl32.Vec3{}, false
		}
		return best, e.normalOut(bestN), true
	case meshcollide.ModeMesh:
		r := residentFor(e.data)
		lo, ld := e.local(origin, dir)
		t, tri, ok := r.Raycast(lo, ld, tMax)
		if !ok {
			return 0, mgl32.Vec3{}, false
		}
		n := e.normalOut(r.Normals[tri])
		if n.Dot(dir) > 0 {
			n = n.Mul(-1)
		}
		return t, n, true
	}
	return 0, mgl32.Vec3{}, false
}

// rayConvex is where the ray o + t·d enters the convex solid bounded by
// planes (n·p <= w inside), with the entering plane's normal. A ray that
// starts inside hits at 0, as raySlab's box does: a shot fired from inside
// a rock is stopped by it.
func rayConvex(o, d mgl32.Vec3, planes []mgl32.Vec4, tMax float32) (float32, mgl32.Vec3, bool) {
	t0, t1 := float32(0), tMax
	var n0 mgl32.Vec3
	for _, pl := range planes {
		n := mgl32.Vec3{pl[0], pl[1], pl[2]}
		denom := n.Dot(d)
		dist := pl[3] - n.Dot(o) // > 0: o is inside this plane
		if math.Abs(float64(denom)) < 1e-12 {
			if dist < 0 {
				return 0, mgl32.Vec3{}, false
			}
			continue
		}
		t := dist / denom
		if denom < 0 {
			// Entering.
			if t > t0 {
				t0, n0 = t, n
			}
		} else if t < t1 {
			t1 = t
		}
		if t0 > t1 {
			return 0, mgl32.Vec3{}, false
		}
	}
	if n0 == (mgl32.Vec3{}) {
		// Inside from the start: no entering plane, so the normal points
		// back along the ray.
		if l := d.Len(); l > 0 {
			n0 = d.Mul(-1 / l)
		}
	}
	return t0, n0, true
}

// rayBoxNormal is raySlab with the normal of the face the ray enters by, in
// world space, for a box of half-extents half placed by m (rigid).
func rayBoxNormal(origin, dir mgl32.Vec3, m mgl32.Mat4, half mgl32.Vec3, tMax float32) (float32, mgl32.Vec3, bool) {
	inv := m.Inv()
	o4 := inv.Mul4x1(origin.Vec4(1))
	d4 := inv.Mul4x1(dir.Vec4(0))
	o := mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}
	d := mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}
	planes := []mgl32.Vec4{
		{1, 0, 0, half[0]}, {-1, 0, 0, half[0]},
		{0, 1, 0, half[1]}, {0, -1, 0, half[1]},
		{0, 0, 1, half[2]}, {0, 0, -1, half[2]},
	}
	t, n, ok := rayConvex(o, d, planes, tMax)
	if !ok {
		return 0, mgl32.Vec3{}, false
	}
	return t, rotate(m, n), true
}

// sceneryRayNearest is the nearest solid placement along a ray within
// tMax, by the placement broadphase: its ID (a scenery's or a placed tree's),
// the distance and the world normal. any stops at the first hit found.
func sceneryRayNearest(lvl *world.Level, origin, dir mgl32.Vec3, tMax float32, ignore map[string]bool, any bool) (id string, t float32, n mgl32.Vec3, ok bool) {
	ix := sceneryIndexFor(lvl)
	if ix == nil {
		return "", 0, mgl32.Vec3{}, false
	}
	start := profStart()
	defer profStop(start, &sceneryStats.rayTime)
	best, bestID, found := tMax, "", false
	var bestN mgl32.Vec3
	ix.along(origin, dir, &best, func(e *sceneryEntry) bool {
		if len(ignore) > 0 && ignore[e.id] {
			return true
		}
		th, nn, hit := sceneryRay(lvl, e, origin, dir, best)
		if hit && th <= best {
			best, bestID, bestN, found = th, e.id, nn, true
			if any {
				return false
			}
		}
		return true
	})
	if !found {
		return "", 0, mgl32.Vec3{}, false
	}
	return bestID, best, bestN, true
}

// ---------- particles ----------

// probeScenery is ParticleCollide's vertical probe against the solid
// placements in the column through the destination: the first surface it
// meets, as a world point and unit normal. Every mode takes part — the box
// as the box a body stops at, the hulls and the mesh as themselves.
func probeScenery(lvl *world.Level, from, to mgl32.Vec3, descending bool) (point, normal mgl32.Vec3, ok bool) {
	ix := sceneryIndexFor(lvl)
	if ix == nil {
		return
	}
	start := profStart()
	defer profStop(start, &sceneryStats.particleTime)
	origin := mgl32.Vec3{to.X(), from.Y() + particleProbeDepth, to.Z()}
	dir := mgl32.Vec3{0, -1, 0}
	if !descending {
		origin[1] = from.Y() - particleProbeDepth
		dir = mgl32.Vec3{0, 1, 0}
	}
	reach := abs32(to.Y()-from.Y()) + 2*particleProbeDepth
	best := reach
	found := false
	var bestN mgl32.Vec3
	ix.along(origin, dir, &best, func(e *sceneryEntry) bool {
		if t, n, hit := sceneryRay(lvl, e, origin, dir, best); hit && t <= best {
			best, bestN, found = t, n, true
		}
		return true
	})
	if !found {
		return
	}
	return origin.Add(dir.Mul(best)), bestN, true
}
