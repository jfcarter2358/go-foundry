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
// Three properties the packing keeps deliberately:
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
// What it is not: a conformal or angle-preserving unwrap. Each island is a flat
// projection, so a strongly curved island stretches towards its edges, and one
// that folds back on itself can overlap. AngleLimit is the control for both —
// lower it and the mesh is cut into more, flatter islands.

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

// buildUVIslands groups faces into islands: two faces join when they share an
// edge — the same two vertex indices — and the angle between their normals is
// within the limit.
//
// Sharing an edge means sharing INDICES, not positions. Two faces that merely
// touch in space, each with its own copy of the corner, are separate islands;
// that is what keeps two brushes that meet at a wall from being flattened into
// one sheet.
//
// The grouping is a union-find over the faces rather than a flood fill, because
// an island is defined by an equivalence relation ("reachable through
// shallow-enough edges") and union-find expresses that directly — a flood fill
// would have to re-derive the same answer with a visited set and a queue.
func buildUVIslands(verts []mgl32.Vec3, faces [][]int, opts AtlasOptions) []uvIsland {
	// Only faces that can actually be projected take part. Dropping the
	// degenerates here rather than mid-projection means every later step can
	// assume a usable loop.
	var list []int
	for fi, loop := range faces {
		if usableAtlasFace(verts, loop) {
			list = append(list, fi)
		}
	}
	if len(list) == 0 {
		return nil
	}

	normals := make([]mgl32.Vec3, len(list))
	for i, fi := range list {
		normals[i] = PolygonNormal3D(loopPoints(verts, faces[fi]))
	}

	parent := make([]int, len(list))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]] // path halving
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		if ra, rb := find(a), find(b); ra != rb {
			parent[rb] = ra
		}
	}

	// Which faces (by slot in list) sit on each undirected edge.
	edgeFaces := make(map[[2]int][]int)
	for slot, fi := range list {
		loop := faces[fi]
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

	// The map is walked in no particular order, which is fine: the partition
	// union-find arrives at does not depend on the order the unions came in.
	cosLimit := float32(math.Cos(float64(opts.AngleLimit)))
	for _, touching := range edgeFaces {
		for a := 0; a < len(touching); a++ {
			for b := a + 1; b < len(touching); b++ {
				// A degenerate face has a zero normal, so this dot is 0 and it
				// joins nothing above a 90° limit — it becomes its own island,
				// which is the honest answer for a face with no plane.
				if normals[touching[a]].Dot(normals[touching[b]]) >= cosLimit {
					union(touching[a], touching[b])
				}
			}
		}
	}

	// Collect in first-seen order over the face list, so islands come out
	// ordered by their lowest face index — stable across runs, unlike a walk
	// over the group map.
	groups := make(map[int][]int, len(list))
	var roots []int
	for slot, fi := range list {
		r := find(slot)
		if _, seen := groups[r]; !seen {
			roots = append(roots, r)
		}
		groups[r] = append(groups[r], fi)
	}

	out := make([]uvIsland, 0, len(roots))
	for _, r := range roots {
		out = append(out, projectIsland(verts, faces, groups[r], opts.VDown))
	}
	return out
}

// projectIsland flattens one island onto its best-fit plane.
//
// The plane's normal is the sum of the faces' *area vectors*, not of their unit
// normals: a chart is usually a few large faces plus a scattering of small
// ones, and weighting a sliver the same as the slab it sits on would tilt the
// projection towards the sliver and stretch everything else.
func projectIsland(verts []mgl32.Vec3, faces [][]int, group []int, vDown bool) uvIsland {
	var n mgl32.Vec3
	for _, fi := range group {
		n = n.Add(loopAreaVector(verts, faces[fi]))
	}
	if n.Len() < 1e-9 {
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
	if n.Len() < 1e-9 {
		n = mgl32.Vec3{0, 1, 0}
	}
	n = n.Normalize()

	// The same basis a model's per-face planar unwrap builds, so a face
	// unwrapped either way comes out with its texture the same way up rather
	// than mirrored. PerpAxis puts U along the horizontal and V up the slope,
	// the right-and-up of someone facing the surface.
	u := PerpAxis(n)
	if u.Len() < 1e-6 {
		u = mgl32.Vec3{1, 0, 0}
	}
	u = u.Normalize()
	v := n.Cross(u).Normalize()
	if vDown {
		v = v.Mul(-1)
	}

	is := uvIsland{Faces: group, Coords: make(map[int]mgl32.Vec2)}
	origin := verts[faces[group[0]][0]]
	minU, minV := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxU, maxV := -float32(math.MaxFloat32), -float32(math.MaxFloat32)
	for _, fi := range group {
		for _, vi := range faces[fi] {
			if _, done := is.Coords[vi]; done {
				continue
			}
			d := verts[vi].Sub(origin)
			c := mgl32.Vec2{d.Dot(u), d.Dot(v)}
			is.Coords[vi] = c
			minU, maxU = min(minU, c.X()), max(maxU, c.X())
			minV, maxV = min(minV, c.Y()), max(maxV, c.Y())
		}
	}
	is.Min = mgl32.Vec2{minU, minV}
	is.Size = mgl32.Vec2{maxU - minU, maxV - minV}
	return is
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
