package stages

// terrain_far.go — the terrain stage's far backdrop: the map's country
// continued past its edge to the horizon, as a ring of coarse cooked tiles,
// and the stitch that joins it to the map (docs/canyon/PHASE_7.md, "The map
// edge"; the height function is internal/procgen/terrain/far.go).
//
// Off unless `far` is set. With it the run adds:
//
//   - far_height.png: the backdrop's heights on a `far_cell` lattice over the
//     whole far square, for terrain_material's far weights;
//   - THE RING: the far square less the map (and a one-cell strip round it)
//     cut into `far_tile` rectangles on the lattice, each a closed
//     HeightGrid (skirts and a bottom: a crack between two ring tiles shows
//     a skirt, not the sky), its rim locked through the cook so neighbours
//     meet at every LOD, placed `far_<i>_<j>` with no collision and the
//     `far_material`;
//   - THE STITCH: between the map's edge and the ring's inner row a strip one
//     lattice cell wide, cut per edge tile (`strip_<side>_<k>`). Its inner
//     edge is that edge tile's own rim vertices — the same samples, in the
//     same tile-local frame, cooked on the same quantisation step — and its
//     outer edge the ring's lattice points, so the map runs into the ring
//     with no step and no gap, and the edge tiles' skirts, which used to
//     stand as walls round the map, are under ground. The two edges are
//     zipped together (more inner vertices than outer), and a quad fills each
//     corner. A strip wears the terrain's own material: the splat's weights
//     clamp at the map's edge, which over one cell is the edge's colour.
//
// The ring is cheap: a 3 km reach at 16 m is ~50 tiles of a few thousand
// triangles, seen only from far off, with no collision (nothing walks out
// there; the player can still step off the stitch — the map's edge is the
// playable edge).

import (
	"fmt"
	"math"
	"time"

	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
)

// farParams are the terrain stage's backdrop parameters (all Added: a
// terrain from before them keeps its hash).
func farParams() []procgen.ParamDef {
	f := procgen.Float
	return []procgen.ParamDef{
		{Name: "far", Added: true, Type: procgen.ParamFloat, Label: "Far backdrop (m)", Default: f(0), Min: 0, Max: 50000, Step: 10,
			Doc: "How far past the map's edge a coarse backdrop continues its mesas and canyons, toward the horizon; 0 for none. " +
				"Raise the level's view distance (the environment stage's) to see it."},
		{Name: "far_cell", Added: true, Type: procgen.ParamFloat, Label: "Far cell (m)", Default: f(16), Min: 1, Max: 256, Step: 1,
			Doc: "The backdrop's sample spacing. Must divide Size and Tile size."},
		{Name: "far_blend", Added: true, Type: procgen.ParamFloat, Label: "Far blend (m)", Default: f(400), Min: 0, Max: 10000, Step: 10,
			Doc: "Over this distance past the edge the map's edge eases into the backdrop's own landforms."},
		{Name: "far_tile", Added: true, Type: procgen.ParamFloat, Label: "Far tile (m)", Default: f(1024), Min: 64, Max: 8192, Step: 16,
			Doc: "The backdrop tiles' largest side."},
		{Name: "far_material", Added: true, Type: procgen.ParamString, Default: procgen.String(""),
			Doc: "The .fmat the backdrop tiles wear: a terrain_material stage's far material, gen_<generator>_<its id>_far. " +
				"Empty: the tiles' texture."},
	}
}

func validateFar(p procgen.Params) []string {
	if p.Float("far") <= 0 {
		return nil
	}
	var out []string
	c := p.Float("far_cell")
	if _, ok := whole(p.Float("size"), c); !ok {
		out = append(out, fmt.Sprintf("Size (%g m) must be a whole number of far cells (%g m)", p.Float("size"), c))
	}
	if _, ok := whole(p.Float("tile_size"), c); !ok {
		out = append(out, fmt.Sprintf("Tile size (%g m) must be a whole number of far cells (%g m)", p.Float("tile_size"), c))
	}
	return out
}

// farLattice is the backdrop's samples.
type farLattice struct {
	cell     float64
	r        float64 // the far square's half side
	o        float64 // the stitch's outer half side: the map's half + one cell
	n        int     // samples a side
	h        []float32
	lo, hi   float32
	minX     float64
	segments []float64 // the tile boundaries along an axis, -r..r
}

func (l *farLattice) at(i, j int) float32 { return l.h[j*l.n+i] }

// index is the lattice index of coordinate v (on the lattice).
func (l *farLattice) index(v float64) int { return int(math.Round((v - l.minX) / l.cell)) }

// splitSpan cuts [a, b] (a whole number of cells) into pieces of at most
// size, each a whole number of cells, as even as that allows.
func splitSpan(a, b, cell, size float64) []float64 {
	cells := int(math.Round((b - a) / cell))
	per := max(int(size/cell), 1)
	n := max((cells+per-1)/per, 1)
	out := []float64{a}
	for k := 1; k < n; k++ {
		out = append(out, a+float64(cells*k/n)*cell)
	}
	return append(out, b)
}

func buildFarLattice(rc *procgen.RunContext, ff *terrain.FarField, half, cell, reach, tile float64) (*farLattice, error) {
	o := half + cell
	k := int(math.Ceil(math.Max(reach-cell, cell) / cell))
	r := o + float64(k)*cell
	l := &farLattice{cell: cell, r: r, o: o, minX: -r}
	l.n = int(math.Round(2*r/cell)) + 1
	l.h = make([]float32, l.n*l.n)
	err := rc.Parallel(l.n, func(j int) error {
		if err := rc.Err(); err != nil {
			return err
		}
		z := -r + float64(j)*cell
		for i := 0; i < l.n; i++ {
			l.h[j*l.n+i] = float32(ff.Height(-r+float64(i)*cell, z))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	l.lo, l.hi = l.h[0], l.h[0]
	for _, v := range l.h {
		l.lo, l.hi = min(l.lo, v), max(l.hi, v)
	}
	segs := splitSpan(-r, -o, cell, tile)
	segs = append(segs, splitSpan(-o, o, cell, tile)[1:]...)
	l.segments = append(segs, splitSpan(o, r, cell, tile)[1:]...)
	return l, nil
}

// writeFar builds, cooks and places the backdrop; meta gains its record.
func writeFar(rc *procgen.RunContext, s *terrain.Settings, res *terrain.Result, meta *terrain.Meta, tp tilePlan, cook bool) error {
	p := rc.Params
	reach := p.Float("far")
	if reach <= 0 || !cook {
		return nil
	}
	t0 := time.Now()
	g := res.Grid
	half := g.Size() / 2
	cell := p.Float("far_cell")
	ff := terrain.NewFarField(s, res, cell, p.Float("far_blend"), reach)
	l, err := buildFarLattice(rc, ff, half, cell, reach, p.Float("far_tile"))
	if err != nil {
		return err
	}
	fm := &terrain.FarMeta{Cell: cell, Reach: reach, Blend: p.Float("far_blend"), Samples: l.n,
		Bounds: terrain.Bounds{MinX: -l.r, MinZ: -l.r, MaxX: l.r, MaxZ: l.r, MinY: l.lo, MaxY: l.hi},
		Height: terrain.Height{File: terrain.FarHeightFile, Lo: l.lo, Hi: l.hi, Encoding: "u16"}}
	png, err := terrain.EncodeGray16(l.n, l.n, l.h, l.lo, l.hi)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(terrain.FarHeightFile, png); err != nil {
		return err
	}
	tex := p.Str("texture")
	uvs := float32(p.Float("uv_scale"))
	base := min(l.lo, tp.base) - 20

	// The ring.
	type farTile struct {
		name           string
		x0, x1, z0, z1 float64
	}
	var tiles []farTile
	segs := l.segments
	inner := func(a, b float64) bool { return a >= -l.o-1e-6 && b <= l.o+1e-6 }
	for b := 0; b+1 < len(segs); b++ {
		for a := 0; a+1 < len(segs); a++ {
			x0, x1, z0, z1 := segs[a], segs[a+1], segs[b], segs[b+1]
			if inner(x0, x1) && inner(z0, z1) {
				continue
			}
			tiles = append(tiles, farTile{fmt.Sprintf("far_%d_%d", a, b), x0, x1, z0, z1})
		}
	}
	farMat := p.Str("far_material")
	err = rc.Parallel(len(tiles), func(k int) error {
		if err := rc.Err(); err != nil {
			return err
		}
		t := tiles[k]
		cx, cz := float32((t.x0+t.x1)/2), float32((t.z0+t.z1)/2)
		i0, j0 := l.index(t.x0), l.index(t.z0)
		hg := meshout.HeightGrid{
			X0: float32(t.x0) - cx, Z0: float32(t.z0) - cz, SizeX: float32(t.x1 - t.x0), SizeZ: float32(t.z1 - t.z0),
			NX: l.index(t.x1) - i0, NZ: l.index(t.z1) - j0,
			Closed: true, Base: base, UVScale: uvs, UVOffset: [2]float32{cx * uvs, cz * uvs},
			Height: func(i, j int) float32 { return l.at(i0+i, j0+j) },
		}
		var b meshout.Builder
		b.Slot("ground", tex)
		hg.Build(&b)
		rect := hg.Rect()
		if _, err := meshout.Cook(b.Mesh(), rc.Path(t.name+".fnm"), meshout.CookOptions{LockRect: &rect, CollisionMesh: true}); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
		rc.Place(procgen.Placement{ID: t.name, Model: rc.AssetPath(t.name + ".fnm"), Position: procgen.Point3{X: cx, Z: cz},
			Collision: "none", Material: farMat})
		return nil
	})
	if err != nil {
		return err
	}
	for _, t := range tiles {
		fm.Tiles = append(fm.Tiles, t.name+".fnm")
	}

	// The stitch, per edge tile.
	type strip struct {
		name   string
		side   byte // 's', 'n', 'w', 'e'
		ti, tj int
	}
	var strips []strip
	for k := 0; k < tp.count; k++ {
		strips = append(strips, strip{fmt.Sprintf("strip_s_%d", k), 's', k, 0}, strip{fmt.Sprintf("strip_n_%d", k), 'n', k, tp.count - 1},
			strip{fmt.Sprintf("strip_w_%d", k), 'w', 0, k}, strip{fmt.Sprintf("strip_e_%d", k), 'e', tp.count - 1, k})
	}
	mat := p.Str("material")
	err = rc.Parallel(len(strips), func(k int) error {
		if err := rc.Err(); err != nil {
			return err
		}
		st := strips[k]
		m, centre := stripMesh(g, tp, l, st.side, st.ti, st.tj, tex, uvs)
		if _, err := meshout.Cook(m, rc.Path(st.name+".fnm"), meshout.CookOptions{LockBorder: true, PositionStep: tp.quant,
			CollisionMesh: true}); err != nil {
			return fmt.Errorf("%s: %w", st.name, err)
		}
		rc.Place(procgen.Placement{ID: st.name, Model: rc.AssetPath(st.name + ".fnm"), Position: centre,
			Collision: "none", Material: mat})
		return nil
	})
	if err != nil {
		return err
	}
	for _, st := range strips {
		fm.Strips = append(fm.Strips, st.name+".fnm")
	}
	meta.Far = fm
	rc.Logf("far backdrop: %.0f m past the edge at %g m, %d ring tiles and %d stitch strips, heights %.0f .. %.0f m, in %s",
		reach, cell, len(tiles), len(strips), l.lo, l.hi, time.Since(t0).Round(time.Millisecond))
	return nil
}

// stripMesh is the stitch strip along the given side of edge tile (ti, tj),
// in that tile's local frame (and the tile's centre): its inner edge the
// tile's rim vertices, its outer edge the lattice one cell out, the corner
// quads with the corner tiles.
func stripMesh(g *terrain.Grid, tp tilePlan, l *farLattice, side byte, ti, tj int, tex string, uvs float32) (*model.ImportMesh, procgen.Point3) {
	_, centre := tileGridAt(g, tp, ti, tj, uvs, false)
	cx, cz := float64(centre.X), float64(centre.Z)
	var b meshout.Builder
	b.Slot("ground", tex)
	vert := func(x, y, z float64) uint32 {
		return b.Vertex(mgl32.Vec3{float32(x - cx), float32(y), float32(z - cz)}, mgl32.Vec3{},
			mgl32.Vec2{float32(x) * uvs, float32(z) * uvs})
	}
	tri := func(a, c, d uint32, pa, pc, pd [3]float64) {
		// Up-facing, whichever way the zip went round.
		e1 := [3]float64{pc[0] - pa[0], pc[1] - pa[1], pc[2] - pa[2]}
		e2 := [3]float64{pd[0] - pa[0], pd[1] - pa[1], pd[2] - pa[2]}
		if e1[2]*e2[0]-e1[0]*e2[2] < 0 {
			c, d = d, c
		}
		b.Tri(a, c, d)
	}
	n := tp.per / tp.step
	ts := tp.size
	x0 := g.MinX + float64(ti)*ts
	z0 := g.MinZ + float64(tj)*ts
	half := g.Size() / 2
	type pt struct {
		p [3]float64
		v uint32
	}
	mk := func(x, y, z float64) pt { return pt{[3]float64{x, y, z}, vert(x, y, z)} }
	// Inner (the tile's rim) and outer (the lattice) runs, both along the
	// side in increasing coordinate.
	var in, out []pt
	along := func(k int) (x, z float64, gi, gj int) {
		switch side {
		case 's', 'n':
			gi, gj = ti*tp.per+k*tp.step, 0
			if side == 'n' {
				gj = g.N
			}
		default:
			gi, gj = 0, tj*tp.per+k*tp.step
			if side == 'e' {
				gi = g.N
			}
		}
		return g.X(gi), g.Z(gj), gi, gj
	}
	for k := 0; k <= n; k++ {
		x, z, gi, gj := along(k)
		in = append(in, mk(x, float64(g.At(gi, gj)), z))
	}
	var fixed float64
	var a0, a1 float64
	switch side {
	case 's':
		fixed, a0, a1 = -l.o, x0, x0+ts
	case 'n':
		fixed, a0, a1 = l.o, x0, x0+ts
	case 'w':
		fixed, a0, a1 = -l.o, z0, z0+ts
	default:
		fixed, a0, a1 = l.o, z0, z0+ts
	}
	for a := a0; a <= a1+1e-6; a += l.cell {
		var x, z float64
		if side == 's' || side == 'n' {
			x, z = a, fixed
		} else {
			x, z = fixed, a
		}
		out = append(out, mk(x, float64(l.at(l.index(x), l.index(z))), z))
	}
	coord := func(q pt) float64 {
		if side == 's' || side == 'n' {
			return q.p[0]
		}
		return q.p[2]
	}
	i, j := 0, 0
	for i < len(in)-1 || j < len(out)-1 {
		if j == len(out)-1 || (i < len(in)-1 && coord(in[i+1]) <= coord(out[j+1])) {
			tri(in[i].v, in[i+1].v, out[j].v, in[i].p, in[i+1].p, out[j].p)
			i++
		} else {
			tri(in[i].v, out[j+1].v, out[j].v, in[i].p, out[j+1].p, out[j].p)
			j++
		}
	}
	// A corner: the map's corner, the two runs' ends one cell out, and the
	// lattice's corner. The south and north strips of the end tiles own them.
	corner := func(cxs, czs float64) {
		mx, mz := cxs*half, czs*half
		gi, gj := 0, 0
		if cxs > 0 {
			gi = g.N
		}
		if czs > 0 {
			gj = g.N
		}
		c0 := mk(mx, float64(g.At(gi, gj)), mz)
		ox, oz := cxs*l.o, czs*l.o
		c1 := mk(ox, float64(l.at(l.index(ox), l.index(mz))), mz)
		c2 := mk(ox, float64(l.at(l.index(ox), l.index(oz))), oz)
		c3 := mk(mx, float64(l.at(l.index(mx), l.index(oz))), oz)
		tri(c0.v, c1.v, c2.v, c0.p, c1.p, c2.p)
		tri(c0.v, c2.v, c3.v, c0.p, c2.p, c3.p)
	}
	if side == 's' || side == 'n' {
		zs := -1.0
		if side == 'n' {
			zs = 1
		}
		if ti == 0 {
			corner(-1, zs)
		}
		if ti == tp.count-1 {
			corner(1, zs)
		}
	}
	b.Weld(0, true)
	return b.Mesh(), centre
}
