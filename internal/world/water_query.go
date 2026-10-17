package world

// water_query.go — "is this point under water, how deep, and which way is
// it flowing?" (docs/terrain-paint/PHASE_W5.md, item 1).
//
// Water in this engine is not a volume. A generated pond or lake is a flat
// cooked mesh per tile wearing a `water` material, a generated river is a
// sloping ribbon whose vertex colours carry its flow map (green and blue:
// the current along X and Z, terrain.RiverFlowMax m/s at the ends), and a
// hand-built pool is a brush face wearing water. All of them are surfaces
// with nothing underneath, placed as non-solid scenery or faces — so the
// physics never knew a body was there, and a script had no way to ask.
//
// The query is built from exactly what the renderer draws: every triangle of
// every placement slot (and every upward brush face) whose material carries
// the water effect, in world space. That makes it right for every kind of
// water at once — a release build ships the meshes, not the generator's
// terrain.yaml, so building from the generator's own lists would have
// answered only in the editor — and it can never disagree with the picture.
// A waterfall's falling sheet (the `falls` param) and any steep triangle
// (waterMinFacing: the ribbon's step under a fall) are left out: they are
// not a surface anything stands in.
//
// The triangles go into a uniform grid over X/Z (waterCell metres a cell,
// each cell listing the triangles whose box touches it), so a query is one
// cell's handful of point-in-triangle tests. Where surfaces overlap in plan
// (a pond in a cave under a lake) the answer is the surface just ABOVE the
// point, or failing that the highest one below it, so a body wading in the
// lower pond is in the lower pond.
//
// A surface has no bed, so "under water" means below a surface by at most
// WaterQueryMaxDepth: a cave forty metres under a lake is not in the lake.
//
// The grid is rebuilt lazily when what it was built from may have changed:
// another level, a different number of placements or chunks, or a brush
// mesh change (BrushMeshGeneration). InvalidateWaterQuery forces it (a
// generator splice that swaps meshes without changing the count).
//
// Safe to call from any goroutine: the build is under a lock and the built
// grid is never modified, only replaced.

import (
	"math"
	"sync"

	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

// WaterQueryMaxDepth is how far below a surface a point still counts as in
// that water, metres (the file comment).
const WaterQueryMaxDepth = 40

// waterFlowMax is the speed a flow-map vertex colour's full range encodes,
// m/s: terrain.RiverFlowMax and water.glsl's WATER_FLOW_MAX (world may not
// import procgen).
const waterFlowMax = 4.0

// waterCell is the grid's cell size, metres.
const waterCell = 4.0

// waterMinFacing is the least upward facing (the normal's Y) a triangle
// needs to count as a surface: about 41° from level. No river runs that
// steep (MaxSlope keeps a surface under ~20°, rapids under ~30°); what is
// steeper is the strip of ribbon a fall's step leaves on the grid, pitched
// from the lip's level to the foot's over a cell or two. Counted, that strip
// stood over the plunge pool's air: a body by the pool (or the camera, which
// then drew the whole frame as under water) was "just under" the lip's
// surface. Fixed after phase W5 (docs/terrain-paint/PHASE_W5.md, "Fixes
// after W5"); it was 0.3 (73°).
const waterMinFacing = 0.75

// WaterSample is the answer for one point.
type WaterSample struct {
	// Found says a water surface lies over (or under) the point's X/Z at
	// all; every other field is zero without one.
	Found bool
	// In says the point is below that surface (by at most
	// WaterQueryMaxDepth).
	In bool
	// Surface is the surface's height at the point's X/Z (the still
	// surface: the shader's waves ride a few centimetres either side).
	Surface float32
	// Depth is Surface minus the point's height: positive under water,
	// negative above it.
	Depth float32
	// Flow is the current at the point, m/s along world X and Z: a river's
	// flow map, zero on still water.
	Flow mgl32.Vec2
	// Material is the .fmat the surface wears ("" for an inline effect).
	Material string
	// Water is that material's water look (absorption, colours, …), for
	// the underwater view's fog and caustics.
	Water material.Water
}

// waterTri is one surface triangle: its corners (world), each corner's flow
// (m/s, X/Z) and its body.
type waterTri struct {
	a, b, c    mgl32.Vec3
	fa, fb, fc mgl32.Vec2
	body       int32
}

// waterBody is a material's look, shared by its triangles.
type waterBody struct {
	material string
	water    material.Water
}

// waterGrid is the built query.
type waterGrid struct {
	tris   []waterTri
	bodies []waterBody
	// The grid: cells × cells over [x0, x0 + n·waterCell), cell (i, j)
	// listing tris[cellTris[cellStart[k]:cellStart[k+1]]].
	x0, z0    float32
	nx, nz    int
	cellStart []int32
	cellTris  []int32
}

// waterQueryKey is what a built grid was built from.
type waterQueryKey struct {
	lvl     *Level
	scenery int
	chunks  int
	brushes uint64
	gen     uint64
}

var waterQuery struct {
	sync.Mutex
	key  waterQueryKey
	grid *waterGrid
	gen  uint64
}

// InvalidateWaterQuery throws away the built query: the next one rebuilds.
func InvalidateWaterQuery() {
	waterQuery.Lock()
	waterQuery.gen++
	waterQuery.grid = nil
	waterQuery.Unlock()
}

// waterGridFor is lvl's grid, built if what it was built from changed.
func waterGridFor(lvl *Level) *waterGrid {
	if lvl == nil {
		return nil
	}
	waterQuery.Lock()
	defer waterQuery.Unlock()
	k := waterQueryKey{lvl: lvl, scenery: len(lvl.Scenery), chunks: len(lvl.Chunks),
		brushes: BrushMeshGeneration(), gen: waterQuery.gen}
	if waterQuery.grid != nil && waterQuery.key == k {
		return waterQuery.grid
	}
	waterQuery.key = k
	waterQuery.grid = buildWaterGrid(lvl)
	return waterQuery.grid
}

// LevelHasWater reports whether lvl has any water surface the query knows.
func LevelHasWater(lvl *Level) bool {
	g := waterGridFor(lvl)
	return g != nil && len(g.tris) > 0
}

// WaterAt answers the query for p (the file comment).
func WaterAt(lvl *Level, p mgl32.Vec3) WaterSample {
	g := waterGridFor(lvl)
	if g == nil || len(g.tris) == 0 {
		return WaterSample{}
	}
	i := int(math.Floor(float64((p.X() - g.x0) / waterCell)))
	j := int(math.Floor(float64((p.Z() - g.z0) / waterCell)))
	if i < 0 || j < 0 || i >= g.nx || j >= g.nz {
		return WaterSample{}
	}
	k := j*g.nx + i
	best := -1
	var bestH float32
	var bestW [3]float32
	above := false // best is a surface above p
	for _, ti := range g.cellTris[g.cellStart[k]:g.cellStart[k+1]] {
		t := &g.tris[ti]
		w, ok := baryXZ(t, p.X(), p.Z())
		if !ok {
			continue
		}
		h := w[0]*t.a.Y() + w[1]*t.b.Y() + w[2]*t.c.Y()
		isAbove := h >= p.Y()
		switch {
		case best < 0:
		case isAbove && !above:
		case isAbove && above && h < bestH:
		case !isAbove && !above && h > bestH:
		default:
			continue
		}
		best, bestH, bestW, above = int(ti), h, w, isAbove
	}
	if best < 0 {
		return WaterSample{}
	}
	t := &g.tris[best]
	b := &g.bodies[t.body]
	depth := bestH - p.Y()
	return WaterSample{
		Found:    true,
		In:       depth >= 0 && depth <= WaterQueryMaxDepth,
		Surface:  bestH,
		Depth:    depth,
		Flow:     t.fa.Mul(bestW[0]).Add(t.fb.Mul(bestW[1])).Add(t.fc.Mul(bestW[2])),
		Material: b.material,
		Water:    b.water,
	}
}

// WaterCrossing finds where the segment from a to b crosses a water surface
// from above — a shot, a thrown thing — and returns the crossing point. It
// steps the segment at half a cell, which is plenty for surfaces that are
// near-flat over a metre.
func WaterCrossing(lvl *Level, a, b mgl32.Vec3) (mgl32.Vec3, bool) {
	if !LevelHasWater(lvl) {
		return mgl32.Vec3{}, false
	}
	d := b.Sub(a)
	n := int(d.Len()/(waterCell*0.5)) + 1
	if n > 4000 {
		n = 4000
	}
	prev := WaterAt(lvl, a)
	if prev.In {
		return mgl32.Vec3{}, false // starts under water: no entry
	}
	pp := a
	for s := 1; s <= n; s++ {
		q := a.Add(d.Mul(float32(s) / float32(n)))
		w := WaterAt(lvl, q)
		if w.In && w.Found {
			// Between pp (above) and q (below): the surface is near-flat,
			// so interpolate on the height above it.
			hp := pp.Y() - w.Surface
			hq := q.Y() - w.Surface
			t := float32(1)
			if hp-hq > 1e-6 {
				t = hp / (hp - hq)
			}
			x := pp.Add(q.Sub(pp).Mul(t))
			x[1] = w.Surface
			return x, true
		}
		pp = q
	}
	return mgl32.Vec3{}, false
}

// baryXZ is p's barycentric weights in t projected onto X/Z, ok when p is
// inside (a hair of slack, so a point on a shared edge is in both).
func baryXZ(t *waterTri, x, z float32) ([3]float32, bool) {
	ax, az := t.a.X(), t.a.Z()
	v0x, v0z := t.b.X()-ax, t.b.Z()-az
	v1x, v1z := t.c.X()-ax, t.c.Z()-az
	v2x, v2z := x-ax, z-az
	den := v0x*v1z - v1x*v0z
	if den > -1e-9 && den < 1e-9 {
		return [3]float32{}, false
	}
	u := (v2x*v1z - v1x*v2z) / den
	v := (v0x*v2z - v2x*v0z) / den
	const eps = -1e-4
	if u < eps || v < eps || u+v > 1-eps {
		return [3]float32{}, false
	}
	return [3]float32{1 - u - v, u, v}, true
}

// buildWaterGrid gathers lvl's water triangles and grids them.
func buildWaterGrid(lvl *Level) *waterGrid {
	g := &waterGrid{}
	bodyOf := map[string]int32{}
	body := func(name string, w material.Water) int32 {
		if b, ok := bodyOf[name]; ok {
			return b
		}
		b := int32(len(g.bodies))
		g.bodies = append(g.bodies, waterBody{material: name, water: w})
		bodyOf[name] = b
		return b
	}
	// The surface materials, resolved once each.
	type resolved struct {
		ok bool
		w  material.Water
	}
	seen := map[string]resolved{}
	water := func(name string) (material.Water, bool) {
		if r, ok := seen[name]; ok {
			return r.w, r.ok
		}
		s, ok := material.ResolveSurface(name)
		r := resolved{ok: ok && s.HasWater && s.Water.Falls <= 0, w: s.Water}
		seen[name] = r
		return r.w, r.ok
	}
	add := func(a, b, c mgl32.Vec3, fa, fb, fc mgl32.Vec2, bi int32) {
		n := b.Sub(a).Cross(c.Sub(a))
		l := n.Len()
		if l < 1e-9 || math.Abs(float64(n.Y()/l)) < waterMinFacing {
			return // degenerate, or steep: not a surface to stand in
		}
		g.tris = append(g.tris, waterTri{a: a, b: b, c: c, fa: fa, fb: fb, fc: fc, body: bi})
	}

	for si := range lvl.Scenery {
		s := &lvl.Scenery[si]
		m := s.Nanite
		if m == nil {
			continue
		}
		var slots []int
		var looks []material.Water
		for k := range m.Slots {
			if w, ok := water(s.SlotMaterial(k)); ok {
				slots = append(slots, k)
				looks = append(looks, w)
			}
		}
		if len(slots) == 0 {
			continue
		}
		if err := m.Flat(); err != nil || len(m.Vertices) == 0 {
			continue
		}
		model := s.ModelMatrix()
		colors := m.HasColors && len(m.Colors) == len(m.Vertices)/5
		for n, k := range slots {
			w := looks[n]
			bi := body(s.SlotMaterial(k), w)
			flowOf := func(v uint32) mgl32.Vec2 {
				if !colors || w.FlowMap <= 0 {
					return mgl32.Vec2{}
				}
				c := m.Colors[v]
				fx := (float32(c>>8&0xff)/255*2 - 1) * waterFlowMax * w.FlowMap
				fz := (float32(c>>16&0xff)/255*2 - 1) * waterFlowMax * w.FlowMap
				// A placement turned about Y turns its current with it.
				d := model.Mul4x1(mgl32.Vec4{fx, 0, fz, 0})
				return mgl32.Vec2{d.X(), d.Z()}
			}
			pos := func(v uint32) mgl32.Vec3 {
				return model.Mul4x1(mgl32.Vec4{m.Vertices[5*v], m.Vertices[5*v+1], m.Vertices[5*v+2], 1}).Vec3()
			}
			first, count := m.SlotTriangles(k)
			idx := m.Indices[3*first : 3*(first+count)]
			for t := 0; t+2 < len(idx); t += 3 {
				i0, i1, i2 := idx[t], idx[t+1], idx[t+2]
				add(pos(i0), pos(i1), pos(i2), flowOf(i0), flowOf(i1), flowOf(i2), bi)
			}
		}
	}

	// Hand-built water: upward brush faces wearing it, fanned into
	// triangles in world space.
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		model := ch.ModelMatrix()
		for bi := range ch.Brushes {
			br := &ch.Brushes[bi]
			for fi := range br.Faces {
				f := &br.Faces[fi]
				if f.Material == "" {
					continue
				}
				w, ok := water(f.Material)
				if !ok {
					continue
				}
				pts := br.PlacedFacePoints(f)
				if len(pts) < 3 {
					continue
				}
				for k := range pts {
					pts[k] = model.Mul4x1(pts[k].Vec4(1)).Vec3()
				}
				b := body(f.Material, w)
				for k := 1; k+1 < len(pts); k++ {
					add(pts[0], pts[k], pts[k+1], mgl32.Vec2{}, mgl32.Vec2{}, mgl32.Vec2{}, b)
				}
			}
		}
	}
	if len(g.tris) == 0 {
		return g
	}

	// The grid over the triangles' box.
	lo := mgl32.Vec2{float32(math.Inf(1)), float32(math.Inf(1))}
	hi := mgl32.Vec2{float32(math.Inf(-1)), float32(math.Inf(-1))}
	for i := range g.tris {
		for _, p := range [3]mgl32.Vec3{g.tris[i].a, g.tris[i].b, g.tris[i].c} {
			lo = mgl32.Vec2{min(lo.X(), p.X()), min(lo.Y(), p.Z())}
			hi = mgl32.Vec2{max(hi.X(), p.X()), max(hi.Y(), p.Z())}
		}
	}
	g.x0, g.z0 = lo.X(), lo.Y()
	g.nx = int((hi.X()-lo.X())/waterCell) + 1
	g.nz = int((hi.Y()-lo.Y())/waterCell) + 1
	cellRange := func(t *waterTri) (i0, j0, i1, j1 int) {
		mnx := min(t.a.X(), t.b.X(), t.c.X())
		mxx := max(t.a.X(), t.b.X(), t.c.X())
		mnz := min(t.a.Z(), t.b.Z(), t.c.Z())
		mxz := max(t.a.Z(), t.b.Z(), t.c.Z())
		cl := func(v, n int) int { return max(0, min(v, n-1)) }
		return cl(int((mnx-g.x0)/waterCell), g.nx), cl(int((mnz-g.z0)/waterCell), g.nz),
			cl(int((mxx-g.x0)/waterCell), g.nx), cl(int((mxz-g.z0)/waterCell), g.nz)
	}
	counts := make([]int32, g.nx*g.nz+1)
	for i := range g.tris {
		i0, j0, i1, j1 := cellRange(&g.tris[i])
		for j := j0; j <= j1; j++ {
			for ii := i0; ii <= i1; ii++ {
				counts[j*g.nx+ii+1]++
			}
		}
	}
	for k := 1; k < len(counts); k++ {
		counts[k] += counts[k-1]
	}
	g.cellStart = counts
	g.cellTris = make([]int32, counts[len(counts)-1])
	fill := make([]int32, g.nx*g.nz)
	for i := range g.tris {
		i0, j0, i1, j1 := cellRange(&g.tris[i])
		for j := j0; j <= j1; j++ {
			for ii := i0; ii <= i1; ii++ {
				k := j*g.nx + ii
				g.cellTris[g.cellStart[k]+fill[k]] = int32(i)
				fill[k]++
			}
		}
	}
	return g
}
