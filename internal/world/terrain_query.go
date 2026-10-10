package world

// terrain_query.go — asking a terrain's surface questions: where does a ray
// meet it, which triangles are in a box, is there ground under a point.
//
// Every question is answered against the surface actually built
// (terrain_mesh.go) — the blocks in block view, the surface net in smooth
// view — so what a body stands on, what a bullet hits and what is drawn are
// one surface.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// TerrainHit is where a ray met a terrain.
type TerrainHit struct {
	// T is the distance along the ray, in the ray direction's units.
	T float32
	// Normal is the hit triangle's unit normal, pointing into the air.
	Normal mgl32.Vec3
	// Face names the face hit: its block is the one a pick would mine, and
	// the block beyond it along Face's direction the one a pick would fill.
	Face int32
}

// Raycast intersects the chunk-space ray origin + t*dir (t in [0, maxT])
// with the surface and returns the nearest hit. It walks the ray brick by
// brick (a 3D DDA over terrainBrick-block bins), so a cast costs the bricks
// it crosses, not the terrain's size.
func (t *Terrain) Raycast(origin, dir mgl32.Vec3, maxT float32) (TerrainHit, bool) {
	c := t.ensureMesh()
	lo, hi := t.LocalBounds()
	t0, t1, ok := rayBoxSpan(origin, dir, lo, hi)
	if !ok || t1 < 0 || t0 > maxT {
		return TerrainHit{}, false
	}
	t0 = max(t0, 0)
	t1 = min(t1, maxT)

	bsz := t.BlockSize * terrainBrick
	start := origin.Add(dir.Mul(t0)).Sub(t.Origin).Mul(1 / bsz)
	maxB := [3]int{
		(t.SizeX+terrainBrick-1)/terrainBrick + 1,
		(t.SizeY+terrainBrick-1)/terrainBrick + 1,
		(t.SizeZ+terrainBrick-1)/terrainBrick + 1,
	}
	var cell, step [3]int
	var tMax, tDelta [3]float32
	for a := 0; a < 3; a++ {
		cell[a] = clampInt(int(math.Floor(float64(start[a]))), -1, maxB[a])
		switch {
		case dir[a] > 0:
			step[a] = 1
			tDelta[a] = bsz / dir[a]
			tMax[a] = t0 + (float32(cell[a]+1)-start[a])*bsz/dir[a]
		case dir[a] < 0:
			step[a] = -1
			tDelta[a] = -bsz / dir[a]
			tMax[a] = t0 + (float32(cell[a])-start[a])*bsz/dir[a]
		default:
			tMax[a] = float32(math.Inf(1))
			tDelta[a] = float32(math.Inf(1))
		}
	}

	best := TerrainHit{T: float32(math.Inf(1))}
	found := false
	for {
		exit := min(tMax[0], tMax[1], tMax[2])
		c.eachSectionForBrick(cell, func(s *TerrainSection, local int) {
			for _, ref := range s.brickTris[s.brickStart[local]:s.brickStart[local+1]] {
				f := &s.Faces[ref/2]
				tri := f.Triangle(int(ref % 2))
				if d, hit := rayTriangle(origin, dir, tri[0], tri[1], tri[2]); hit && d >= 0 && d <= maxT && d < best.T {
					best = TerrainHit{T: d, Face: f.Key, Normal: tri[1].Sub(tri[0]).Cross(tri[2].Sub(tri[0]))}
					found = true
				}
			}
		})
		if found && best.T <= exit+1e-5 {
			break
		}
		if exit > t1 {
			break
		}
		a := 0
		if tMax[1] < tMax[a] {
			a = 1
		}
		if tMax[2] < tMax[a] {
			a = 2
		}
		cell[a] += step[a]
		tMax[a] += tDelta[a]
		if cell[a] < -1 || cell[a] > maxB[a] {
			break
		}
	}
	if !found {
		return TerrainHit{}, false
	}
	if l := best.Normal.Len(); l > 1e-12 {
		best.Normal = best.Normal.Mul(1 / l)
	}
	return best, true
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// eachSectionForBrick calls fn with every section whose bins cover brick
// (absolute brick coordinates) and that brick's index in them.
func (c *terrainMeshCache) eachSectionForBrick(b [3]int, fn func(s *TerrainSection, local int)) {
	const B = terrainBricksSec
	var lo, hi [3]int
	n := [3]int{c.nx, c.ny, c.nz}
	for a := 0; a < 3; a++ {
		// Section s bins bricks [s*B-1, s*B+B].
		lo[a] = max(floorDiv(b[a]-B, B), 0)
		hi[a] = min(floorDiv(b[a]+1, B), n[a]-1)
	}
	for sz := lo[2]; sz <= hi[2]; sz++ {
		for sy := lo[1]; sy <= hi[1]; sy++ {
			for sx := lo[0]; sx <= hi[0]; sx++ {
				l := [3]int{b[0] - (sx*B - 1), b[1] - (sy*B - 1), b[2] - (sz*B - 1)}
				if l[0] < 0 || l[1] < 0 || l[2] < 0 || l[0] >= terrainBrickSpan || l[1] >= terrainBrickSpan || l[2] >= terrainBrickSpan {
					continue
				}
				s := c.sections[c.secIndex(sx, sy, sz)]
				if len(s.brickStart) == 0 {
					continue
				}
				fn(s, (l[2]*terrainBrickSpan+l[1])*terrainBrickSpan+l[0])
			}
		}
	}
}

// EachFaceIn calls fn for every face whose corners' box overlaps the
// chunk-space box [lo, hi].
func (t *Terrain) EachFaceIn(lo, hi mgl32.Vec3, fn func(f *TerrainFace)) {
	for _, s := range t.Sections() {
		if s.Empty() || !boxesOverlap(s.Lo, s.Hi, lo, hi) {
			continue
		}
		for fi := range s.Faces {
			f := &s.Faces[fi]
			flo, fhi := f.Corners[0], f.Corners[0]
			for _, p := range f.Corners[1:] {
				for k := 0; k < 3; k++ {
					flo[k] = min(flo[k], p[k])
					fhi[k] = max(fhi[k], p[k])
				}
			}
			if boxesOverlap(flo, fhi, lo, hi) {
				fn(f)
			}
		}
	}
}

// SolidNear reports whether any solid block overlaps the chunk-space box
// [lo, hi] grown by one block — the cheap test that lets a body nowhere near
// the ground skip every ray.
func (t *Terrain) SolidNear(lo, hi mgl32.Vec3) bool {
	x0, y0, z0 := t.BlockAt(lo)
	x1, y1, z1 := t.BlockAt(hi)
	x0, y0, z0 = max(x0-1, 0), max(y0-1, 0), max(z0-1, 0)
	x1, y1, z1 = min(x1+1, t.SizeX-1), min(y1+1, t.SizeY-1), min(z1+1, t.SizeZ-1)
	for z := z0; z <= z1; z++ {
		for y := y0; y <= y1; y++ {
			i := t.Index(x0, y, z)
			for x := x0; x <= x1; x++ {
				if t.Blocks[i] != 0 {
					return true
				}
				i++
			}
		}
	}
	return false
}

// GroundBelow casts straight down (chunk -Y) from p for at most maxDrop and
// returns the first surface facing upward: its height and unit normal.
func (t *Terrain) GroundBelow(p mgl32.Vec3, maxDrop float32) (y float32, n mgl32.Vec3, ok bool) {
	o := p
	for i := 0; i < 4; i++ {
		h, hit := t.Raycast(o, mgl32.Vec3{0, -1, 0}, maxDrop-(p.Y()-o.Y()))
		if !hit {
			return 0, mgl32.Vec3{}, false
		}
		if h.Normal.Y() > 0 {
			return o.Y() - h.T, h.Normal, true
		}
		// A ceiling's underside: carry on down from just past it.
		o[1] -= h.T + 1e-3
	}
	return 0, mgl32.Vec3{}, false
}

// rayBoxSpan is the slab test: the parameter range over which the ray is
// inside [lo, hi]. Tolerant of zero direction components.
func rayBoxSpan(o, d, lo, hi mgl32.Vec3) (t0, t1 float32, ok bool) {
	t0, t1 = float32(math.Inf(-1)), float32(math.Inf(1))
	for i := 0; i < 3; i++ {
		if d[i] == 0 {
			if o[i] < lo[i]-1e-4 || o[i] > hi[i]+1e-4 {
				return 0, 0, false
			}
			continue
		}
		a := (lo[i] - o[i]) / d[i]
		b := (hi[i] - o[i]) / d[i]
		if a > b {
			a, b = b, a
		}
		t0 = max(t0, a)
		t1 = min(t1, b)
	}
	return t0 - 1e-3, t1 + 1e-3, t0 <= t1+1e-3
}

// rayTriangle is Möller–Trumbore, two-sided.
func rayTriangle(o, d, a, b, c mgl32.Vec3) (float32, bool) {
	e1 := b.Sub(a)
	e2 := c.Sub(a)
	p := d.Cross(e2)
	det := e1.Dot(p)
	if det > -1e-12 && det < 1e-12 {
		return 0, false
	}
	inv := 1 / det
	s := o.Sub(a)
	u := s.Dot(p) * inv
	if u < -1e-5 || u > 1+1e-5 {
		return 0, false
	}
	q := s.Cross(e1)
	v := d.Dot(q) * inv
	if v < -1e-5 || u+v > 1+1e-5 {
		return 0, false
	}
	return e2.Dot(q) * inv, true
}
