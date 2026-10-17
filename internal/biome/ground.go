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
// cheaper: the highest brush surface below a point, and its normal. (And,
// since the procedural terrain, the highest mesh-collision scenery surface:
// see ground_scenery.go.)
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
//
// Every brush in the level, that is, which is placed in `frame` — the frame the
// point itself is in ("" for the world; see Volume.Frame). A chunk carried by
// an entity is that entity's geometry, expressed in its coordinates: to a
// world-space volume it is a ship hovering overhead, not ground, and taking it
// as ground planted a field of grass on the floor of whatever flew over. It is
// tested in placement space for the same reason — the point is in the frame,
// so the geometry has to be carried only as far as the frame, not out to the
// world.
func SurfaceUnder(lvl *world.Level, frame string, wx, topY, wz, maxDrop float32) Surface {
	return NewGroundQuery(lvl, frame).SurfaceUnder(wx, topY, wz, maxDrop)
}

// GroundQuery is SurfaceUnder with the per-chunk and per-brush work done once,
// for a caller about to ask thousands of columns in a row — a scatter volume's
// height bake. It captures the level as it is when made, so it is for one bake
// and not to be kept across frames: an edit afterwards is not seen.
type GroundQuery struct {
	chunks []groundChunk
	// scenery is the mesh-collision placements, for a world-frame query
	// (ground_scenery.go).
	scenery []groundScenery
}

type groundChunk struct {
	ch     *world.Chunk
	lo, hi mgl32.Vec3 // placement bounds
	model  mgl32.Mat4
	inv    mgl32.Mat4
	moved  bool // model is not the identity
	// brushes are the solid brushes, each with the box around its drawn mesh
	// in chunk space.
	brushes []groundBrush
	// grid bins brushes by their boxes' XZ extent (chunk space) when there
	// are enough of them to be worth it; nil tests every brush.
	grid *groundGrid
}

// groundGridMin is how many brushes a chunk needs before its column test
// goes through a grid: below it, the per-brush box test is cheaper than the
// binning.
const groundGridMin = 32

// groundGrid is a uniform XZ grid over a chunk's brush boxes. Cell c's
// brushes are idx[start[c]:start[c+1]], in ascending brush order — the
// order the column visits them in without a grid — so a column meets the
// same brushes in the same order and ties between equally near faces break
// the same way.
type groundGrid struct {
	x0, z0     float32
	invX, invZ float32
	nx, nz     int
	start, idx []int32
}

// cellX is the grid column holding chunk-space x, clamped into the grid.
// Monotonic in x, so a box [lo, hi] binned by cellX(lo)..cellX(hi) is found
// from every x inside it.
func (g *groundGrid) cellX(x float32) int {
	return min(max(int(math.Floor(float64((x-g.x0)*g.invX))), 0), g.nx-1)
}

func (g *groundGrid) cellZ(z float32) int {
	return min(max(int(math.Floor(float64((z-g.z0)*g.invZ))), 0), g.nz-1)
}

// newGroundGrid bins brushes into about two per cell.
func newGroundGrid(brushes []groundBrush) *groundGrid {
	lo, hi := brushes[0].lo, brushes[0].hi
	for _, b := range brushes[1:] {
		lo, hi = geom.VecMin(lo, b.lo), geom.VecMax(hi, b.hi)
	}
	n := min(max(int(math.Sqrt(float64(len(brushes))/2)), 1), 128)
	g := &groundGrid{x0: lo.X(), z0: lo.Z(), nx: n, nz: n}
	if w := hi.X() - lo.X(); w > 0 {
		g.invX = float32(n) / w
	} else {
		g.nx = 1
	}
	if d := hi.Z() - lo.Z(); d > 0 {
		g.invZ = float32(n) / d
	} else {
		g.nz = 1
	}
	g.start = make([]int32, g.nx*g.nz+1)
	for _, b := range brushes {
		for cz := g.cellZ(b.lo.Z()); cz <= g.cellZ(b.hi.Z()); cz++ {
			for cx := g.cellX(b.lo.X()); cx <= g.cellX(b.hi.X()); cx++ {
				g.start[cz*g.nx+cx+1]++
			}
		}
	}
	for c := 1; c < len(g.start); c++ {
		g.start[c] += g.start[c-1]
	}
	g.idx = make([]int32, g.start[len(g.start)-1])
	fill := append([]int32(nil), g.start[:len(g.start)-1]...)
	for bi, b := range brushes {
		for cz := g.cellZ(b.lo.Z()); cz <= g.cellZ(b.hi.Z()); cz++ {
			for cx := g.cellX(b.lo.X()); cx <= g.cellX(b.hi.X()); cx++ {
				c := cz*g.nx + cx
				g.idx[fill[c]] = int32(bi)
				fill[c]++
			}
		}
	}
	return g
}

type groundBrush struct {
	b      *world.Brush
	lo, hi mgl32.Vec3
}

// NewGroundQuery gathers what SurfaceUnder needs from lvl for points in frame.
func NewGroundQuery(lvl *world.Level, frame string) *GroundQuery {
	q := &GroundQuery{}
	if lvl == nil {
		return q
	}
	frame = world.FrameID(frame)
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue // not drawn and not solid, so nothing to stand on
		}
		if world.FrameID(ch.FrameOwner) != frame {
			continue // another frame's geometry — see SurfaceUnder
		}
		gc := groundChunk{ch: ch, model: ch.LocalPlacementMatrix()}
		gc.lo, gc.hi = ch.PlacementBounds()
		if gc.model != mgl32.Ident4() {
			gc.inv = gc.model.Inv()
			if gc.inv == (mgl32.Mat4{}) {
				continue
			}
			gc.moved = true
		}
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.Solid {
				// Decorative geometry is not ground. A body walks through it,
				// and grass growing on it would be growing on nothing.
				continue
			}
			// The DRAWN mesh: what grass grows on is the surface an author can
			// see, and its faces index that mesh's own vertex pool.
			verts := b.DrawMesh().Verts
			if len(verts) == 0 {
				continue
			}
			gb := groundBrush{b: b, lo: verts[0], hi: verts[0]}
			for _, v := range verts[1:] {
				gb.lo, gb.hi = geom.VecMin(gb.lo, v), geom.VecMax(gb.hi, v)
			}
			gc.brushes = append(gc.brushes, gb)
		}
		if len(gc.brushes) >= groundGridMin {
			gc.grid = newGroundGrid(gc.brushes)
		}
		q.chunks = append(q.chunks, gc)
	}
	if frame == "" {
		q.addScenery(lvl)
	}
	return q
}

// SurfaceUnder is the package-level SurfaceUnder against the gathered level.
func (q *GroundQuery) SurfaceUnder(wx, topY, wz, maxDrop float32) Surface {
	if q == nil || maxDrop <= 0 {
		return Surface{}
	}
	origin := mgl32.Vec3{wx, topY, wz}
	best := Surface{}
	bestT := maxDrop

	for ci := range q.chunks {
		gc := &q.chunks[ci]
		ch := gc.ch
		// A quick reject on the chunk's own bounds before any triangle work.
		// The column is a vertical segment, so only the XZ overlap and the
		// height range matter.
		if wx < gc.lo.X() || wx > gc.hi.X() || wz < gc.lo.Z() || wz > gc.hi.Z() {
			continue
		}
		if gc.hi.Y() < topY-maxDrop || gc.lo.Y() > topY {
			continue
		}

		// The column in the chunk's own space, once per chunk. A chunk's
		// placement is a Y rotation and a uniform scale, so the column stays
		// vertical in here; only its length changes with the scale.
		localOrigin, localDir := origin, mgl32.Vec3{0, -1, 0}
		if gc.moved {
			o4 := gc.inv.Mul4x1(origin.Vec4(1))
			d4 := gc.inv.Mul4x1(localDir.Vec4(0))
			localOrigin = mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}
			localDir = mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}
		}
		localBottom := localOrigin.Y() + localDir.Y()*maxDrop

		// Only the brushes binned where the column stands, when the chunk
		// has a grid; a column off the grid is off every brush's box.
		cand := []int32(nil)
		nb := len(gc.brushes)
		if g := gc.grid; g != nil {
			c := g.cellZ(localOrigin.Z())*g.nx + g.cellX(localOrigin.X())
			cand = g.idx[g.start[c]:g.start[c+1]]
			nb = len(cand)
		}
		for k := 0; k < nb; k++ {
			bi := k
			if cand != nil {
				bi = int(cand[k])
			}
			gb := &gc.brushes[bi]
			// Reject the brush on its own box before touching a face. The
			// chunk reject above is not enough on its own: a chunk is one box
			// around everything in it, so a big terrain chunk passes it for
			// every column, and every face of every brush got ray-tested — a
			// grass field over a 700-brush canyon took 13 s to bake, on the
			// main thread, on the frame the chunk was switched on. A point
			// exactly on a box's edge counts as inside, so a column down the
			// seam between two brushes still tests both.
			if localOrigin.X() < gb.lo.X() || localOrigin.X() > gb.hi.X() ||
				localOrigin.Z() < gb.lo.Z() || localOrigin.Z() > gb.hi.Z() ||
				gb.hi.Y() < localBottom || gb.lo.Y() > localOrigin.Y() {
				continue
			}
			drawn := gb.b.DrawMesh()
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
				n := ch.LocalToPlacementDirection(drawn.FaceNormal(f))
				if n.Y() <= 0 {
					continue
				}
				t, ok := columnHitsFace(localOrigin, localDir, maxDrop, f.Geometry.Vertices)
				if !ok || t >= bestT {
					continue
				}
				bestT = t
				best = Surface{Height: topY - t, Normal: n.Normalize(), Found: true}
			}
		}

		// The chunk's terrain answers the column with one ray down its own
		// surface. The column's parameter is carried through the placement's
		// scale exactly as the faces' is: localDir is the world's down, in
		// here. Only an upward-facing hit is ground, as for the faces above.
		// A cave roof's underside is passed through to the floor below it.
		if tr := ch.Terrain; tr != nil {
			o, gone := localOrigin, float32(0)
			for i := 0; i < 4; i++ {
				h, ok := tr.Raycast(o, localDir, min(maxDrop, bestT)-gone)
				if !ok {
					break
				}
				n := ch.LocalToPlacementDirection(h.Normal)
				if n.Y() > 0 {
					bestT = gone + h.T
					best = Surface{Height: topY - bestT, Normal: n.Normalize(), Found: true}
					break
				}
				const past = 1e-3
				gone += h.T + past
				o = o.Add(localDir.Mul(h.T + past))
			}
		}
	}
	// Mesh-collision scenery: generated terrain tiles, big rocks.
	if len(q.scenery) > 0 {
		if t, n, ok := q.sceneryUnder(origin, bestT); ok && t < bestT {
			best = Surface{Height: topY - t, Normal: n, Found: true}
		}
	}
	return best
}

// columnHitsFace intersects a straight-down ray with one face's triangles and
// returns the nearest distance along it.
//
// origin and dir are the column already rewritten in the chunk's own space
// (SurfaceUnder does that once per chunk) rather than the triangles being
// carried into the world, exactly as the editor's picker does: the inverse
// transform scales origin and direction together, so the returned distance is
// still the world-space one and is directly comparable across chunks.
func columnHitsFace(origin, dir mgl32.Vec3, maxDrop float32, verts []float32) (float32, bool) {
	if len(verts) < 15 {
		return 0, false
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
