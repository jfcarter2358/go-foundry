package geom

// uv_atlas.go — flattening a mesh into one texture's worth of UVs.
//
// A per-face planar projection gives every face the whole image. That is the
// right answer for re-aiming a tiling material at one face and exactly the
// wrong one for *painting* a surface, because no part of the image belongs to
// any particular face — every face is everywhere, so a brush stroke lands on
// all of them at once.
//
// An atlas unwrap gives each face its own region of one shared image instead.
// The mesh is cut into islands of connected faces that point roughly the same
// way, each island is flattened onto its own best-fit plane, and the flattened
// islands are packed side by side into the unit square. The result is a layout
// you can paint on: a texel belongs to one face, so painting it changes that
// face and nothing else.
//
// It works on a bare vertex pool and a list of index loops rather than on any
// one document, because two documents need it: a model (model/model_uv_atlas.go)
// and the brushes a level is built from (world/brush_uv.go). Both had better lay
// the same mesh out the same way, and one implementation is how.
//
// Four properties the layout keeps deliberately:
//
//   - Uniform texel density. Every island is scaled by the *same* factor, so a
//     face twice the size of another gets twice the texture area. Normalising
//     per island would give a doorknob and a wall the same number of texels.
//   - A gutter between islands. Bilinear filtering and mipmapping both sample
//     outside a triangle's footprint, so islands packed flush against their
//     neighbours bleed into them — as coloured fringes along every seam, worst
//     at distance where the mip levels are coarsest. PaddingUV holds them
//     apart.
//   - Determinism. Islands are built and ordered from face order, never from
//     map iteration order, so unwrapping the same mesh twice produces the same
//     layout. Otherwise every re-unwrap would scramble the texture that had been
//     painted against the previous one.
//
//   - No overlap, inside an island as well as between them. Every face is
//     checked as it joins an island, flattened, against the faces already
//     there; one that would fold over or land on another starts a new island
//     instead. No texel belongs to two places on the surface, so every part
//     can be painted on its own.
//
// What it is not: a conformal or angle-preserving unwrap. Each island is a flat
// projection, so a strongly curved island stretches towards its edges.
// AngleLimit is the control for that — lower it and the mesh is cut into more,
// flatter islands.

import (
	"fmt"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// defaultAtlasAngleLimit is how far two adjacent faces' normals may differ
	// and still share an island. 60° keeps a cube's six sides apart (their
	// normals differ by 90°) while holding a subdivided curve together.
	defaultAtlasAngleLimit = float32(math.Pi / 3)

	// defaultAtlasPaddingUV is the gutter left around every island, in UV
	// units — 4 texels on a 512² atlas. Two texels would survive bilinear
	// filtering but not the mip chain, where each level halves the gutter.
	defaultAtlasPaddingUV = float32(4.0 / 512.0)
)

// AtlasOptions tunes how the mesh is cut and packed. The zero value is usable:
// every field falls back to a default.
type AtlasOptions struct {
	// AngleLimit is the largest angle, in radians, between two adjacent faces'
	// normals that still leaves them in one island. Lower cuts more seams and
	// flattens with less distortion; higher keeps bigger, easier-to-paint
	// islands. <= 0 means defaultAtlasAngleLimit.
	AngleLimit float32
	// PaddingUV is the gutter left around each island, in UV units. < 0 is
	// clamped to 0; 0 means defaultAtlasPaddingUV. Pass a deliberate 0 by
	// passing a negative value.
	PaddingUV float32
	// Aspect is the target texture's width divided by its height. Island
	// heights are scaled by it so a texel stays square on a non-square atlas.
	// <= 0 means 1.
	Aspect float32
	// VDown turns each island's V axis round, so V grows DOWN the island rather
	// than up it.
	//
	// The texture editor draws row 0 at the top, and the sampler agrees, so a
	// projection whose V runs up a wall shows that wall upside down on the
	// canvas: grass painted along the bottom of the island ends up along the top
	// of the wall. A level's surfaces have an up and an author paints to it, so
	// brushes set this. The model editor does not, and lays islands out the way
	// it always has.
	VDown bool
}

// resolve fills in the defaults, so the packing code below never re-checks.
func (o AtlasOptions) resolve() AtlasOptions {
	if o.AngleLimit <= 0 {
		o.AngleLimit = defaultAtlasAngleLimit
	}
	switch {
	case o.PaddingUV < 0:
		o.PaddingUV = 0
	case o.PaddingUV == 0:
		o.PaddingUV = defaultAtlasPaddingUV
	}
	if o.Aspect <= 0 {
		o.Aspect = 1
	}
	return o
}

// AtlasLayout is the result of an unwrap.
type AtlasLayout struct {
	// UVs is index-parallel to the faces the unwrap was given: UVs[i] holds one
	// coordinate per corner of face i, or is nil for a face that took no part
	// (fewer than three corners, or a corner outside the vertex pool).
	UVs [][]mgl32.Vec2
	// Islands is how many islands the mesh was cut into.
	Islands int
	// Scale is how many UV units one unit of the vertex pool's space became,
	// along U. On a W-texel-wide atlas that is Scale*W texels per unit — the
	// number that says whether the atlas is big enough for what it covers.
	Scale float32
}

// uvIsland is one connected group of faces, flattened onto a shared plane.
//
// Coords is keyed by *vertex index*, not by face corner, so two faces meeting
// inside the island agree on where their shared vertex went — which is what
// keeps a brush stroke continuous across an interior edge. Vertices on the
// island's boundary are also in some other island's Coords, at a different
// place; that is the seam, and it is intended.
type uvIsland struct {
	Faces  []int // indices into the faces slice, ascending
	Coords map[int]mgl32.Vec2
	Min    mgl32.Vec2 // lower corner of Coords' bounding box
	Size   mgl32.Vec2 // its extent, in the vertex pool's units
}

// AtlasUnwrap lays the given faces out so that together they tile one texture
// without overlapping. faces holds each face's corners as indices into verts,
// in winding order.
//
// Nothing is written anywhere: the caller owns the faces and decides where the
// coordinates go. If no face can be projected an error is returned — a silent
// empty layout would look exactly like a successful unwrap onto a blank
// texture.
func AtlasUnwrap(verts []mgl32.Vec3, faces [][]int, opts AtlasOptions) (AtlasLayout, error) {
	opts = opts.resolve()

	islands := buildUVIslands(verts, faces, opts)
	if len(islands) == 0 {
		return AtlasLayout{}, fmt.Errorf("nothing to unwrap: no face has three or more usable corners")
	}

	// Heights are pre-multiplied by the aspect ratio so the packer works in one
	// space — UV units of the target texture — rather than juggling world units
	// on one axis and texture units on the other.
	sizes := make([][2]float32, len(islands))
	for i, is := range islands {
		sizes[i] = [2]float32{is.Size.X(), is.Size.Y() * opts.Aspect}
	}

	// Shelf packing wants the tallest first, or short islands open a shelf that
	// tall ones cannot then use. SliceStable keeps equal-height islands in
	// mesh order, which is what makes the layout reproducible.
	order := make([]int, len(islands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return sizes[order[a]][1] > sizes[order[b]][1]
	})

	origins, scale, ok := atlasFit(sizes, order, opts.PaddingUV)
	if !ok {
		return AtlasLayout{}, fmt.Errorf("cannot fit %d UV islands in one texture at %.4f padding: "+
			"raise the angle limit to cut fewer, larger islands, or lower the padding",
			len(islands), opts.PaddingUV)
	}

	out := AtlasLayout{
		UVs:     make([][]mgl32.Vec2, len(faces)),
		Islands: len(islands),
		Scale:   scale,
	}
	for i, is := range islands {
		ox, oy := origins[i][0], origins[i][1]
		for _, fi := range is.Faces {
			loop := faces[fi]
			uvs := make([]mgl32.Vec2, len(loop))
			for c, vi := range loop {
				p := is.Coords[vi].Sub(is.Min)
				uvs[c] = mgl32.Vec2{
					ox + p.X()*scale,
					oy + p.Y()*opts.Aspect*scale,
				}
			}
			out.UVs[fi] = uvs
		}
	}
	return out, nil
}

// ---------- cutting the mesh into islands ----------

// usableAtlasFace reports whether a loop can be projected: three corners at
// least, every one of them inside the vertex pool.
func usableAtlasFace(verts []mgl32.Vec3, loop []int) bool {
	if len(loop) < 3 {
		return false
	}
	for _, vi := range loop {
		if vi < 0 || vi >= len(verts) {
			return false
		}
	}
	return true
}

// loopPoints resolves a usable loop to its positions.
func loopPoints(verts []mgl32.Vec3, loop []int) []mgl32.Vec3 {
	pts := make([]mgl32.Vec3, len(loop))
	for i, vi := range loop {
		pts[i] = verts[vi]
	}
	return pts
}

// atlasFace is one usable face, resolved once for the whole unwrap.
type atlasFace struct {
	fi     int        // index into the faces slice
	normal mgl32.Vec3 // unit, or zero for a face with no plane
	tris   [][3]int   // its triangulation, as indices into verts
	nbrs   []int      // slots of the faces across its edges, ascending
}

// buildUVIslands cuts the faces into islands, each of which flattens onto one
// plane without any two of its faces landing on each other.
//
// An island grows outwards from a seed face across shared edges — the same two
// vertex INDICES, not positions. Two faces that merely touch in space, each with
// its own copy of the corner, are separate islands; that is what keeps two
// brushes that meet at a wall from being flattened into one sheet. A neighbour
// joins only if:
//
//   - its normal is within the angle limit of the face it is reached from, so a
//     hard edge is a seam;
//   - its normal is within the limit of the SEED's, so a gentle curve cannot
//     creep all the way round. Checking neighbours alone is what used to let a
//     cylinder's whole barrel, or a limb, or a sphere, join into one island —
//     every step 20°, the sum 360° — whose back then flattened onto its front;
//   - flattened onto the seed's plane it keeps its winding, so it has not
//     folded over, and it covers none of the faces already placed.
//
// The last test is what makes the layout paintable: every texel inside an
// island belongs to one face. It is checked directly rather than trusted to the
// angle tests, because a large angle limit, a non-planar face or a mesh that
// spirals (a ramp, a helix, an overhang) can overlap while every angle passes.
// A face turned away is not lost: it seeds, or joins, a later island.
//
// Seeds are taken in face order and the growth is breadth-first over sorted
// neighbour lists, so the same mesh always cuts the same way.
func buildUVIslands(verts []mgl32.Vec3, faces [][]int, opts AtlasOptions) []uvIsland {
	// Only faces that can actually be projected take part. Dropping the
	// degenerates here rather than mid-projection means every later step can
	// assume a usable loop.
	var list []atlasFace
	for fi, loop := range faces {
		if !usableAtlasFace(verts, loop) {
			continue
		}
		pts := loopPoints(verts, loop)
		af := atlasFace{fi: fi, normal: PolygonNormal3D(pts)}
		for _, t := range TriangulatePolygon3D(pts) {
			af.tris = append(af.tris, [3]int{loop[t[0]], loop[t[1]], loop[t[2]]})
		}
		list = append(list, af)
	}
	if len(list) == 0 {
		return nil
	}

	// Which faces (by slot in list) sit on each undirected edge.
	edgeFaces := make(map[[2]int][]int)
	for slot, af := range list {
		loop := faces[af.fi]
		for c := range loop {
			a, b := loop[c], loop[(c+1)%len(loop)]
			if a == b {
				continue
			}
			if a > b {
				a, b = b, a
			}
			k := [2]int{a, b}
			if s := edgeFaces[k]; len(s) > 0 && s[len(s)-1] == slot {
				continue // a loop that runs along one edge twice
			}
			edgeFaces[k] = append(edgeFaces[k], slot)
		}
	}
	// The map is walked in no particular order; sorting each neighbour list
	// afterwards is what takes that order back out of the result.
	for _, touching := range edgeFaces {
		for _, a := range touching {
			for _, b := range touching {
				if a != b {
					list[a].nbrs = append(list[a].nbrs, b)
				}
			}
		}
	}
	for i := range list {
		list[i].nbrs = sortedUnique(list[i].nbrs)
	}

	scale := atlasLengthScale(verts, faces, list)
	cosLimit := float32(math.Cos(float64(opts.AngleLimit)))
	assigned := make([]bool, len(list))
	var out []uvIsland
	for seed := range list {
		if assigned[seed] {
			continue
		}
		group := growUVIsland(verts, list, seed, assigned, cosLimit, scale)

		// The seed's plane is the one the growth proved flat, but the island's
		// area-weighted plane stretches it less. Use that one when it is also
		// flat — it usually is — and fall back to the seed's when it is not.
		n := islandAreaNormal(verts, faces, list, group)
		if !islandFlatOn(verts, list, group, n, scale) {
			n = list[seed].normal
		}
		fis := make([]int, len(group))
		for i, slot := range group {
			fis[i] = list[slot].fi
		}
		sort.Ints(fis)
		out = append(out, projectIsland(verts, faces, fis, n, opts.VDown))
	}
	return out
}

// growUVIsland grows one island breadth-first from seed, marks its faces
// assigned, and returns their slots. See buildUVIslands for the rules.
func growUVIsland(verts []mgl32.Vec3, list []atlasFace, seed int, assigned []bool,
	cosLimit float32, scale atlasScale) []int {
	assigned[seed] = true
	group := []int{seed}
	n := list[seed].normal
	if n.Len() < 0.5 {
		// No plane to flatten anything else onto: the face stands alone,
		// which is the honest answer for a face with no area.
		return group
	}
	u, v := islandBasis(n, false)
	origin := verts[list[seed].tris[0][0]]
	flat := func(vi int) mgl32.Vec2 { return planeCoord(verts[vi], origin, u, v) }

	grid := newTriGrid(scale.cell)
	for _, t := range list[seed].tris {
		grid.add(seed, flat(t[0]), flat(t[1]), flat(t[2]), scale.tol)
	}

	// Faces already turned down for this island for a reason that cannot
	// change as it grows: outside the cone, folded, or overlapping. (Being too
	// sharp an edge away from ONE neighbour is not such a reason — another
	// neighbour may reach it at a shallower angle.)
	refused := make(map[int]bool)
	for q := 0; q < len(group); q++ {
		cur := group[q]
		for _, nb := range list[cur].nbrs {
			if assigned[nb] || refused[nb] {
				continue
			}
			if list[nb].normal.Dot(list[cur].normal) < cosLimit {
				continue
			}
			if list[nb].normal.Dot(n) < cosLimit || !fitsFlat(list[nb], nb, grid, flat, scale.tol) {
				refused[nb] = true
				continue
			}
			for _, t := range list[nb].tris {
				grid.add(nb, flat(t[0]), flat(t[1]), flat(t[2]), scale.tol)
			}
			assigned[nb] = true
			group = append(group, nb)
		}
	}
	return group
}

// fitsFlat reports whether a face, flattened, keeps its winding and covers no
// triangle already in the grid.
func fitsFlat(af atlasFace, slot int, grid *triGrid, flat func(int) mgl32.Vec2, tol float32) bool {
	for _, t := range af.tris {
		a, b, c := flat(t[0]), flat(t[1]), flat(t[2])
		if triCross(a, b, c) < -tol*tol {
			return false // folded over: seen from the plane it faces away
		}
		if grid.overlaps(slot, a, b, c, tol) {
			return false
		}
	}
	return true
}

// islandFlatOn reports whether a group of faces flattens onto the plane with
// normal n with none folded and none overlapping another.
func islandFlatOn(verts []mgl32.Vec3, list []atlasFace, group []int, n mgl32.Vec3,
	scale atlasScale) bool {
	if n.Len() < 0.5 {
		return false
	}
	u, v := islandBasis(n, false)
	origin := verts[list[group[0]].tris[0][0]]
	flat := func(vi int) mgl32.Vec2 { return planeCoord(verts[vi], origin, u, v) }
	grid := newTriGrid(scale.cell)
	for _, slot := range group {
		if !fitsFlat(list[slot], slot, grid, flat, scale.tol) {
			return false
		}
		for _, t := range list[slot].tris {
			grid.add(slot, flat(t[0]), flat(t[1]), flat(t[2]), scale.tol)
		}
	}
	return true
}

// islandAreaNormal is the island's best-fit plane normal: the sum of its faces'
// *area vectors*, not of their unit normals. A chart is usually a few large
// faces plus a scattering of small ones, and weighting a sliver the same as the
// slab it sits on would tilt the projection towards the sliver and stretch
// everything else. Zero if the area vectors cancel.
func islandAreaNormal(verts []mgl32.Vec3, faces [][]int, list []atlasFace, group []int) mgl32.Vec3 {
	var n mgl32.Vec3
	for _, slot := range group {
		n = n.Add(loopAreaVector(verts, faces[list[slot].fi]))
	}
	if n.Len() < 1e-9 {
		return mgl32.Vec3{}
	}
	return n.Normalize()
}

// islandBasis is the (u, v) an island flattens onto for plane normal n.
//
// The same basis a model's per-face planar unwrap builds, so a face unwrapped
// either way comes out with its texture the same way up rather than mirrored.
// PerpAxis puts U along the horizontal and V up the slope, the right-and-up of
// someone facing the surface. v = n × u keeps a face wound CCW about n CCW in
// the plane, which is what fitsFlat's fold test relies on — so the growth and
// the flatness check always work unflipped, and vDown is applied only to the
// final projection. A mirror cannot make faces fold or overlap, so the answer
// is the same either way.
func islandBasis(n mgl32.Vec3, vDown bool) (u, v mgl32.Vec3) {
	u = PerpAxis(n)
	if u.Len() < 1e-6 {
		u = mgl32.Vec3{1, 0, 0}
	}
	u = u.Normalize()
	v = n.Cross(u).Normalize()
	if vDown {
		v = v.Mul(-1)
	}
	return u, v
}

func planeCoord(p, origin, u, v mgl32.Vec3) mgl32.Vec2 {
	d := p.Sub(origin)
	return mgl32.Vec2{d.Dot(u), d.Dot(v)}
}

// projectIsland flattens one island (face indices, ascending) onto the plane
// with normal n.
func projectIsland(verts []mgl32.Vec3, faces [][]int, group []int, n mgl32.Vec3, vDown bool) uvIsland {
	if n.Len() < 0.5 {
		// The area vectors cancelled — a zero-area island, or two coincident
		// faces wound against each other. Fall back to any face that does have
		// a normal, then to +Y, so the island still lands somewhere finite
		// instead of producing NaN UVs.
		for _, fi := range group {
			if fn := PolygonNormal3D(loopPoints(verts, faces[fi])); fn.Len() > 0.5 {
				n = fn
				break
			}
		}
	}
	if n.Len() < 0.5 {
		n = mgl32.Vec3{0, 1, 0}
	}
	u, v := islandBasis(n.Normalize(), vDown)

	is := uvIsland{Faces: group, Coords: make(map[int]mgl32.Vec2)}
	origin := verts[faces[group[0]][0]]
	minU, minV := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxU, maxV := -float32(math.MaxFloat32), -float32(math.MaxFloat32)
	for _, fi := range group {
		for _, vi := range faces[fi] {
			if _, done := is.Coords[vi]; done {
				continue
			}
			c := planeCoord(verts[vi], origin, u, v)
			is.Coords[vi] = c
			minU, maxU = min(minU, c.X()), max(maxU, c.X())
			minV, maxV = min(minV, c.Y()), max(maxV, c.Y())
		}
	}
	is.Min = mgl32.Vec2{minU, minV}
	is.Size = mgl32.Vec2{maxU - minU, maxV - minV}
	return is
}

// ---------- overlap testing in the flattened plane ----------

// atlasScale sizes the overlap tests to the mesh rather than to fixed units, so
// a centimetre-scale prop and a hundred-metre cliff are judged alike.
type atlasScale struct {
	tol  float32 // how far two triangles may interpenetrate and still count as touching
	cell float32 // triGrid cell size
}

// atlasLengthScale derives the tests' sizes from the mean edge length of the
// faces taking part.
func atlasLengthScale(verts []mgl32.Vec3, faces [][]int, list []atlasFace) atlasScale {
	var sum float64
	var n int
	for _, af := range list {
		loop := faces[af.fi]
		for c := range loop {
			sum += float64(verts[loop[c]].Sub(verts[loop[(c+1)%len(loop)]]).Len())
			n++
		}
	}
	mean := float32(sum / float64(max(n, 1)))
	if mean <= 0 || math.IsNaN(float64(mean)) || math.IsInf(float64(mean), 0) {
		mean = 1
	}
	return atlasScale{tol: mean * 1e-4, cell: mean * 2}
}

// triGrid buckets flattened triangles by the cells their bounding boxes cover,
// so testing a face against an island costs its neighbourhood rather than the
// whole island.
type triGrid struct {
	cell  float32
	tris  []gridTri
	cells map[[2]int32][]int
	// big holds triangles covering too many cells to bucket — a ground plane
	// among a thousand bolts — and is tested against everything.
	big []int
}

type gridTri struct {
	slot    int
	a, b, c mgl32.Vec2
}

// maxGridCellsPerTri bounds how many buckets one triangle is filed under.
const maxGridCellsPerTri = 256

func newTriGrid(cell float32) *triGrid {
	return &triGrid{cell: cell, cells: make(map[[2]int32][]int)}
}

// cellRange is the span of cells a triangle's bounding box covers, and whether
// it is small enough to bucket at all.
func (g *triGrid) cellRange(a, b, c mgl32.Vec2) (x0, y0, x1, y1 int32, ok bool) {
	lo := mgl32.Vec2{min(a.X(), b.X(), c.X()), min(a.Y(), b.Y(), c.Y())}
	hi := mgl32.Vec2{max(a.X(), b.X(), c.X()), max(a.Y(), b.Y(), c.Y())}
	fx0, fy0 := math.Floor(float64(lo.X()/g.cell)), math.Floor(float64(lo.Y()/g.cell))
	fx1, fy1 := math.Floor(float64(hi.X()/g.cell)), math.Floor(float64(hi.Y()/g.cell))
	if (fx1-fx0+1)*(fy1-fy0+1) > maxGridCellsPerTri || math.Abs(fx0) > 1e9 || math.Abs(fx1) > 1e9 ||
		math.Abs(fy0) > 1e9 || math.Abs(fy1) > 1e9 {
		return 0, 0, 0, 0, false
	}
	return int32(fx0), int32(fy0), int32(fx1), int32(fy1), true
}

// add files a triangle. One with no area covers nothing and is not kept.
func (g *triGrid) add(slot int, a, b, c mgl32.Vec2, tol float32) {
	if abs32(triCross(a, b, c)) <= tol*tol {
		return
	}
	idx := len(g.tris)
	g.tris = append(g.tris, gridTri{slot, a, b, c})
	x0, y0, x1, y1, ok := g.cellRange(a, b, c)
	if !ok {
		g.big = append(g.big, idx)
		return
	}
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			k := [2]int32{x, y}
			g.cells[k] = append(g.cells[k], idx)
		}
	}
}

// overlaps reports whether a triangle's interior meets that of any filed
// triangle from a different face. Triangles of the same face never count
// against each other: they share edges by construction.
func (g *triGrid) overlaps(slot int, a, b, c mgl32.Vec2, tol float32) bool {
	if abs32(triCross(a, b, c)) <= tol*tol {
		return false // no area, so no interior to overlap with
	}
	hit := func(idx int) bool {
		t := g.tris[idx]
		return t.slot != slot && trianglesOverlap2D(a, b, c, t.a, t.b, t.c, tol)
	}
	for _, idx := range g.big {
		if hit(idx) {
			return true
		}
	}
	x0, y0, x1, y1, ok := g.cellRange(a, b, c)
	if !ok {
		// Too big to look up by cell: test everything.
		for idx := range g.tris {
			if hit(idx) {
				return true
			}
		}
		return false
	}
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for _, idx := range g.cells[[2]int32{x, y}] {
				if hit(idx) {
					return true
				}
			}
		}
	}
	return false
}

// trianglesOverlap2D reports whether two triangles' interiors intersect by more
// than tol, by the separating axis theorem: two convex shapes are disjoint
// exactly when some edge normal of one of them separates them.
//
// The tolerance is what lets faces that share an edge or a corner — which is
// every pair of neighbours in an island — count as touching, not overlapping:
// along the shared edge's normal their projections meet at a single value.
func trianglesOverlap2D(a0, a1, a2, b0, b1, b2 mgl32.Vec2, tol float32) bool {
	ta := [3]mgl32.Vec2{a0, a1, a2}
	tb := [3]mgl32.Vec2{b0, b1, b2}
	for _, tri := range [2]*[3]mgl32.Vec2{&ta, &tb} {
		for i := 0; i < 3; i++ {
			e := tri[(i+1)%3].Sub(tri[i])
			l := e.Len()
			if l < 1e-12 {
				continue
			}
			axis := mgl32.Vec2{-e.Y() / l, e.X() / l}
			aLo, aHi := projectTri(ta, axis)
			bLo, bHi := projectTri(tb, axis)
			if aHi <= bLo+tol || bHi <= aLo+tol {
				return false
			}
		}
	}
	return true
}

func projectTri(t [3]mgl32.Vec2, axis mgl32.Vec2) (lo, hi float32) {
	lo, hi = t[0].Dot(axis), t[0].Dot(axis)
	for _, p := range t[1:] {
		d := p.Dot(axis)
		lo, hi = min(lo, d), max(hi, d)
	}
	return lo, hi
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// sortedUnique sorts s and drops repeats, in place.
func sortedUnique(s []int) []int {
	sort.Ints(s)
	out := s[:0]
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// loopAreaVector returns a usable loop's Newell vector: a normal whose length is
// twice the face's area. PolygonNormal3D is this, normalized — the
// un-normalized form is what area-weighted averaging needs.
func loopAreaVector(verts []mgl32.Vec3, loop []int) mgl32.Vec3 {
	var n mgl32.Vec3
	cnt := len(loop)
	for i := 0; i < cnt; i++ {
		a := verts[loop[i]]
		b := verts[loop[(i+1)%cnt]]
		n = n.Add(mgl32.Vec3{
			(a.Y() - b.Y()) * (a.Z() + b.Z()),
			(a.Z() - b.Z()) * (a.X() + b.X()),
			(a.X() - b.X()) * (a.Y() + b.Y()),
		})
	}
	return n
}

// ---------- packing islands into the unit square ----------

// atlasPack lays the islands out in shelves at a given scale and reports where
// each one's content starts. sizes is indexed by island; order is the sequence
// to place them in. The returned origins are indexed by island, not by order.
//
// Shelf packing — fill a row left to right, start a new row at the tallest
// item's height — rather than a tighter algorithm, because the input is
// pre-sorted by height, which is the case shelves handle nearly optimally, and
// because the result has to stay reproducible. Wasted atlas space costs texels;
// a layout that shifted between runs would cost the user their painting.
func atlasPack(sizes [][2]float32, order []int, scale, pad float32) (origins [][2]float32, height float32, ok bool) {
	origins = make([][2]float32, len(sizes))
	var x, y, shelfH float32
	for _, i := range order {
		// Each island reserves its scaled size plus a gutter on every side, so
		// two neighbours end up 2*pad apart and the atlas edge is pad away.
		w := sizes[i][0]*scale + 2*pad
		h := sizes[i][1]*scale + 2*pad
		if w > 1 || h > 1 {
			return nil, 0, false
		}
		if x+w > 1 {
			x, y, shelfH = 0, y+shelfH, 0
		}
		if y+h > 1 {
			return nil, 0, false
		}
		origins[i] = [2]float32{x + pad, y + pad}
		x += w
		if h > shelfH {
			shelfH = h
		}
	}
	return origins, y + shelfH, true
}

// atlasFit finds the largest scale at which every island still fits, by
// bisection on atlasPack.
//
// Bisection rather than a closed-form "total area / atlas area" estimate,
// because the gutters and the shelf breaks make the packed height a step
// function of the scale, not a smooth one — an analytic guess would have to be
// checked and corrected anyway. Fifty halvings settle a float32 completely.
func atlasFit(sizes [][2]float32, order []int, pad float32) (origins [][2]float32, scale float32, ok bool) {
	// At scale 0 every island is just its gutter. If even that does not fit,
	// no smaller scale will, and the bisection below would have no lower bound.
	if _, _, ok := atlasPack(sizes, order, 0, pad); !ok {
		return nil, 0, false
	}

	lo, hi := float32(0), float32(1)
	for i := 0; i < 40; i++ {
		if _, _, ok := atlasPack(sizes, order, hi, pad); !ok {
			break
		}
		lo, hi = hi, hi*2
	}
	for i := 0; i < 50; i++ {
		mid := lo + (hi-lo)/2
		if mid <= lo || mid >= hi {
			break // the two have met at adjacent floats
		}
		if _, _, ok := atlasPack(sizes, order, mid, pad); ok {
			lo = mid
		} else {
			hi = mid
		}
	}

	origins, _, ok = atlasPack(sizes, order, lo, pad)
	if !ok {
		return nil, 0, false
	}
	return origins, lo, true
}
