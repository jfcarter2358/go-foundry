package stages

// ground_edit_river_mesh.go — the rivers' water surfaces: one ribbon mesh
// per terrain tile and water material, cooked like the pond tiles
// (docs/terrain-paint/PHASE_W3.md, "Ribbon").
//
// ON THE GRID, NOT ALONG THE SPLINE. A river's surface slopes, so the
// ponds' flat per-cell sheet (waterCells/waterTileMesh) does not fit; but a
// ribbon swept along the centreline and cut at tile edges would have to
// meet its neighbour tile's cut, and another river's ribbon at a
// confluence, vertex for vertex. Instead the surface lives on the terrain's
// own samples (terrain.RiverSample.Surface: every river's surface merged,
// each sample owned by the river whose banks it is deepest inside), and a
// cell is water when one of its corners is wet and all four carry a
// surface; one ring more is added under the banks, so the shoreline is where
// the ground rises through the water, as at a pond. Two triangles per cell
// over shared corner vertices: tiles meet on the same samples with the same
// heights (no crack), a confluence is one sheet, and a cell the pond
// water stands in (its wet samples) is left to the pond, and the pond's
// skirt under its bank gives way to the river (writeGroundWater drops the
// river's cells from it): no dry gap and no doubled sheet where a river
// enters a pond, whose level it ends at.
//
// EACH VERTEX carries what the shader needs (water.glsl):
//
//   - UV: u across the river (m, + left of the heading), v along it (m
//     from the source). The water shader samples its detail maps in world
//     space, so the UVs are there for a material that wants them; at a
//     confluence they jump from one river's frame to the other's;
//   - COLOUR: red the foam (W1's hook: it adds to the shore foam), green
//     and blue the current along X and Z as 0.5 + 0.5·v/RiverFlowMax (4
//     m/s), alpha 1 (fragment.glsl reads alpha as cavity). A material with
//     `flow_map` reads green/blue as the flow map: two-phase flow-mapped
//     normals and foam advected downstream.
//
// The cook locks the tile's rectangle and the surface's open border (as
// the pond tiles), its colour weight keeps the vertices where the flow or
// foam changes, and the placement is the pond's: collision none, not
// solid, wearing the river's material. A key in the file name makes a
// changed ribbon a new model path (the editor's live splice reloads it);
// a ribbon whose cells and corner values match last run's is reused.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
)

// groundRiverVersion changes when a ribbon changes for the same cells.
// 2: the vertex normal follows the surface's slope (fixes after W5).
const groundRiverVersion = "ground-river-2"

// riverTileName is a ribbon's placement ID.
func riverTileName(ti, tj int, mat string) string { return fmt.Sprintf("river_%d_%d_%s", ti, tj, mat) }

// pondCoreCells is the cells (index cj·N + ci, NaN-free = covered) with a
// corner in a pond's water — the ponds' own surface without its skirt —
// or nil without ponds.
func pondCoreCells(g *terrain.Grid, ws *groundWater) []float32 {
	if !ws.wet() {
		return nil
	}
	s, n := g.S(), g.N
	nan := float32(math.NaN())
	out := make([]float32, n*n)
	for cj := 0; cj < n; cj++ {
		for ci := 0; ci < n; ci++ {
			out[cj*n+ci] = nan
			for _, k := range [4]int{cj*s + ci, cj*s + ci + 1, (cj+1)*s + ci, (cj+1)*s + ci + 1} {
				if ws.water.Body[k] >= 0 {
					out[cj*n+ci] = ws.water.Level[k]
					break
				}
			}
		}
	}
	return out
}

// riverCells is every water cell (index cj·N + ci) and its owning river
// (file comment). pond is the ponds' own cells (pondCoreCells), or nil.
func riverCells(g *terrain.Grid, rv *groundRivers, pond []float32) map[int32]int32 {
	s, n := g.S(), g.N
	smp := rv.res.Samples
	hasSurf := func(k int) bool {
		v, ok := smp[int32(k)]
		return ok && v.Surface == v.Surface
	}
	covered := func(c int) bool { return pond != nil && pond[c] == pond[c] }
	corners := func(ci, cj int) [4]int {
		return [4]int{cj*s + ci, cj*s + ci + 1, (cj+1)*s + ci, (cj+1)*s + ci + 1}
	}
	allSurf := func(ci, cj int) bool {
		for _, k := range corners(ci, cj) {
			if !hasSurf(k) {
				return false
			}
		}
		return true
	}
	// Candidates: the cells round every sample with a surface.
	cand := map[int32]bool{}
	for k, v := range smp {
		if v.Surface != v.Surface {
			continue
		}
		i, j := int(k)%s, int(k)/s
		for _, d := range [4][2]int{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}} {
			ci, cj := i+d[0], j+d[1]
			if ci >= 0 && cj >= 0 && ci < n && cj < n {
				cand[int32(cj*n+ci)] = true
			}
		}
	}
	keys := make([]int32, 0, len(cand))
	for c := range cand {
		keys = append(keys, c)
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
	cells := map[int32]int32{}
	for _, c := range keys {
		ci, cj := int(c)%n, int(c)/n
		if covered(int(c)) || !allSurf(ci, cj) {
			continue
		}
		owner := int32(-1)
		for _, k := range corners(ci, cj) {
			if v := smp[int32(k)]; v.Wet {
				owner = v.Owner
				break
			}
		}
		if owner >= 0 {
			cells[c] = owner
		}
	}
	// One ring more under the banks.
	grown := map[int32]int32{}
	for _, c := range keys {
		if _, in := cells[c]; in {
			continue
		}
		ci, cj := int(c)%n, int(c)/n
		if covered(int(c)) || !allSurf(ci, cj) {
			continue
		}
		for _, d := range [8][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-1, -1}, {1, -1}, {-1, 1}, {1, 1}} {
			ni, nj := ci+d[0], cj+d[1]
			if ni < 0 || nj < 0 || ni >= n || nj >= n {
				continue
			}
			if o, ok := cells[int32(nj*n+ni)]; ok {
				grown[c] = o
				break
			}
		}
	}
	for c, o := range grown {
		cells[c] = o
	}
	return cells
}

// writeGroundRivers meshes, cooks and places the ribbons (file comment).
func writeGroundRivers(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, rv *groundRivers, cells map[int32]int32) error {
	if !rv.any() {
		return nil
	}
	// Per tile and material, the cells, sorted.
	type tileMat struct {
		ti, tj int
		mat    string
	}
	groups := map[tileMat][]int32{}
	n := g.N
	for c, o := range cells {
		ci, cj := int(c)%n, int(c)/n
		ti, tj := min(ci/tp.per, tp.count-1), min(cj/tp.per, tp.count-1)
		key := tileMat{ti, tj, rv.mats[o]}
		groups[key] = append(groups[key], c)
	}
	keys := make([]tileMat, 0, len(groups))
	for k, cs := range groups {
		sort.Slice(cs, func(a, b int) bool { return cs[a] < cs[b] })
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].tj != keys[b].tj {
			return keys[a].tj < keys[b].tj
		}
		if keys[a].ti != keys[b].ti {
			return keys[a].ti < keys[b].ti
		}
		return keys[a].mat < keys[b].mat
	})
	no := false
	out := make([]*procgen.Placement, len(keys))
	reused := make([]bool, len(keys))
	if err := rc.Parallel(len(keys), func(x int) (ferr error) {
		k := keys[x]
		cs := groups[k]
		name := riverTileName(k.ti, k.tj, safeName(k.mat, "water"))
		key := groundRiverKey(rc, g, tp, rv, cs, k.ti, k.tj, k.mat)
		file := fmt.Sprintf("%s_%s.fnm", name, key[:10])
		pl := &procgen.Placement{ID: name, Model: rc.AssetPath(file), Position: tileCentre(g, tp, k.ti, k.tj), Collision: "none",
			Solid: &no, Chunk: procgen.ChunkAuto, Material: k.mat}
		if rc.ReuseTile(name, key, file) {
			out[x], reused[x] = pl, true
			return nil
		}
		m := riverTileMesh(g, tp, rv, cs, k.ti, k.tj)
		rc.BeginTile(name)
		defer func() {
			if ferr == nil {
				rc.SetTile(name, key)
			}
		}()
		ts := float32(tp.size)
		rect := [4]float32{-ts / 2, -ts / 2, ts / 2, ts / 2}
		if _, err := meshout.Cook(m, rc.Path(file), meshout.CookOptions{LockRect: &rect, LockBorder: true, CollisionMesh: true}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		out[x] = pl
		return nil
	}); err != nil {
		return err
	}
	var placed []procgen.Placement
	nr := 0
	for x, pl := range out {
		if pl != nil {
			placed = append(placed, *pl)
			if reused[x] {
				nr++
			}
		}
	}
	rc.Place(placed...)
	rc.Logf("river surfaces: %d cells in %d meshes (%d reused)", len(cells), len(placed), nr)
	return nil
}

// riverVertexColor is a sample's vertex colour (file comment).
func riverVertexColor(s terrain.RiverSample) [4]uint8 {
	enc := func(v float32) uint8 {
		return uint8(math.Round(float64(min(max(v, 0), 1)) * 255))
	}
	return [4]uint8{enc(s.Foam), enc(0.5 + 0.5*s.FlowX/terrain.RiverFlowMax), enc(0.5 + 0.5*s.FlowZ/terrain.RiverFlowMax), 255}
}

// riverSurfaceNormal is the ribbon's facing at sample (i, j): the surface's
// slope from its neighbours (fixes after W5: the water shader tells which
// side of a sloping river the eye is on by it, docs/terrain-paint/
// PHASE_W5.md). Along each axis the gentler of the two one-sided slopes,
// so the step a fall leaves on the grid does not tilt the water beside it,
// and no slope at all past 0.6 (the step itself).
func riverSurfaceNormal(rv *groundRivers, s int, cell float32, i, j int) mgl32.Vec3 {
	at := func(i, j int) (float32, bool) {
		if i < 0 || j < 0 || i >= s || j >= s {
			return 0, false
		}
		v, ok := rv.res.Samples[int32(j*s+i)]
		return v.Surface, ok && v.Surface == v.Surface
	}
	h0, ok := at(i, j)
	if !ok {
		return mgl32.Vec3{0, 1, 0}
	}
	slope := func(di, dj int) float32 {
		var best float32
		found := false
		for _, sg := range [2]int{-1, 1} {
			h, ok := at(i+sg*di, j+sg*dj)
			if !ok {
				continue
			}
			d := (h - h0) / (float32(sg) * cell)
			if !found || abs32(d) < abs32(best) {
				best, found = d, true
			}
		}
		if abs32(best) > 0.6 {
			return 0
		}
		return best
	}
	return mgl32.Vec3{-slope(1, 0), 1, -slope(0, 1)}.Normalize()
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// riverTileMesh is a tile's ribbon over cells cs, in tile-local
// coordinates, with its vertex colours.
func riverTileMesh(g *terrain.Grid, tp tilePlan, rv *groundRivers, cs []int32, ti, tj int) *model.ImportMesh {
	s, n := g.S(), g.N
	ts := float32(tp.size)
	cell := float32(g.Cell)
	var b meshout.Builder
	b.Slot("water", "")
	index := map[int]uint32{}
	var colors [][4]uint8
	vert := func(i, j int) uint32 {
		k := j*s + i
		if v, ok := index[k]; ok {
			return v
		}
		smp := rv.res.Samples[int32(k)]
		x := float32(i-ti*tp.per)*cell - ts/2
		z := float32(j-tj*tp.per)*cell - ts/2
		v := b.Vertex(mgl32.Vec3{x, smp.Surface, z}, riverSurfaceNormal(rv, s, cell, i, j), mgl32.Vec2{smp.U, smp.V})
		colors = append(colors, riverVertexColor(smp))
		index[k] = v
		return v
	}
	for _, c := range cs {
		ci, cj := int(c)%n, int(c)/n
		// Counter-clockwise from above, as the terrain's top.
		a := vert(ci, cj)
		cc := vert(ci, cj+1)
		d := vert(ci+1, cj+1)
		e := vert(ci+1, cj)
		b.Tri(a, cc, d)
		b.Tri(a, d, e)
	}
	m := b.Mesh()
	m.Colors = colors
	return m
}

// groundRiverKey is a ribbon's piece key: its tile, material, cells and
// the corner values they read.
func groundRiverKey(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, rv *groundRivers, cs []int32, ti, tj int, mat string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d,%d|%d|%d|%g|%g|%g|%g|%s\n", ti, tj, tp.count, tp.per, tp.size, g.Cell, g.MinX, g.MinZ, mat)
	s, n := g.S(), g.N
	var b [4]byte
	put := func(v float32) {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
		h.Write(b[:])
	}
	seen := map[int]bool{}
	for _, c := range cs {
		binary.LittleEndian.PutUint32(b[:], uint32(c))
		h.Write(b[:])
		ci, cj := int(c)%n, int(c)/n
		for _, k := range [4]int{cj*s + ci, cj*s + ci + 1, (cj+1)*s + ci, (cj+1)*s + ci + 1} {
			if seen[k] {
				continue
			}
			seen[k] = true
			v := rv.res.Samples[int32(k)]
			put(v.Surface)
			col := riverVertexColor(v)
			h.Write(col[:])
			put(v.U)
			put(v.V)
		}
	}
	return groundPieceKey(rc, groundRiverVersion, fmt.Sprintf("%x", h.Sum(nil)))
}
