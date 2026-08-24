package main

// model_uv_atlas.go — flattening a whole model into one texture's worth of UVs.
//
// ModelPlanarUnwrap (model_uv.go) is a per-face repair tool: every face gets
// its own projection, and "fit to 0..1" makes every face cover the entire
// image. That is the right answer for re-aiming a tiling material at one face
// and exactly the wrong one for *painting* a model, because no part of the
// image belongs to any particular face — every face is everywhere, so a brush
// stroke lands on all of them at once.
//
// An atlas unwrap gives each face its own region of one shared image instead.
// The mesh is cut into islands of connected faces that point roughly the same
// way, each island is flattened onto its own best-fit plane, and the flattened
// islands are packed side by side into the unit square. The result is a layout
// you can paint on: a texel belongs to one face, so painting it changes that
// face and nothing else.
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
//   - Determinism. Islands are built and ordered from sorted face indices,
//     never from map iteration order, so unwrapping the same mesh twice
//     produces the same layout. Otherwise every re-unwrap would scramble the
//     texture that had been painted against the previous one.
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

// AtlasUnwrapOptions tunes how the mesh is cut and packed. The zero value is
// usable: every field falls back to a default.
type AtlasUnwrapOptions struct {
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
}

// resolve fills in the defaults, so the packing code below never re-checks.
func (o AtlasUnwrapOptions) resolve() AtlasUnwrapOptions {
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

// uvIsland is one connected group of faces, flattened onto a shared plane.
//
// Coords is keyed by *document vertex index*, not by face corner, so two faces
// meeting inside the island agree on where their shared vertex went — which is
// what keeps a brush stroke continuous across an interior edge. Vertices on the
// island's boundary are also in some other island's Coords, at a different
// place; that is the seam, and it is intended.
type uvIsland struct {
	Faces  []int // ascending
	Coords map[int]mgl32.Vec2
	Min    mgl32.Vec2 // lower corner of Coords' bounding box
	Size   mgl32.Vec2 // its extent, in world units
}

// AtlasUnwrap rewrites the given faces' UVs so that together they tile one
// texture without overlapping, and returns the number of islands the mesh was
// cut into.
//
// Faces with fewer than three vertices are skipped: they have no plane to
// project onto and no area to give texels to. If that leaves nothing, the
// document is untouched and an error is returned — a silent no-op here would
// look exactly like a successful unwrap onto a blank texture.
func (m *ModelDoc) AtlasUnwrap(faces []int, opts AtlasUnwrapOptions) (int, error) {
	opts = opts.resolve()

	islands := m.buildUVIslands(faces, opts.AngleLimit)
	if len(islands) == 0 {
		return 0, fmt.Errorf("nothing to unwrap: no selected face has three or more vertices")
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
		return 0, fmt.Errorf("cannot fit %d UV islands in one texture at %.4f padding: "+
			"raise the angle limit to cut fewer, larger islands, or lower the padding",
			len(islands), opts.PaddingUV)
	}

	for i, is := range islands {
		ox, oy := origins[i][0], origins[i][1]
		for _, fi := range is.Faces {
			f := &m.Faces[fi]
			uvs := make([]mgl32.Vec2, len(f.Verts))
			for c, vi := range f.Verts {
				p := is.Coords[vi].Sub(is.Min)
				uvs[c] = mgl32.Vec2{
					ox + p.X()*scale,
					oy + p.Y()*opts.Aspect*scale,
				}
			}
			f.UVs = uvs
		}
	}
	m.touch()
	return len(islands), nil
}

// ---------- cutting the mesh into islands ----------

// buildUVIslands groups faces into islands: two faces join when they share an
// edge and the angle between their normals is within angleLimit.
//
// The grouping is a union-find over the selection rather than a flood fill,
// because an island is defined by an equivalence relation ("reachable through
// shallow-enough edges") and union-find expresses that directly — a flood fill
// would have to re-derive the same answer with a visited set and a queue.
func (m *ModelDoc) buildUVIslands(faces []int, angleLimit float32) []uvIsland {
	// Only faces that can actually be projected take part. Dropping the
	// degenerates here rather than mid-projection means every later step can
	// assume len(Verts) >= 3.
	set := m.faceSet(faces)
	list := make([]int, 0, len(set))
	for _, fi := range sortedKeys(set) {
		if len(m.Faces[fi].Verts) >= 3 {
			list = append(list, fi)
		}
	}
	if len(list) == 0 {
		return nil
	}

	slot := make(map[int]int, len(list))
	normals := make([]mgl32.Vec3, len(list))
	for i, fi := range list {
		slot[fi] = i
		normals[i] = m.faceNormal(&m.Faces[fi])
	}

	parent := make([]int, len(list))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
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

	cosLimit := float32(math.Cos(float64(angleLimit)))
	for _, e := range m.Edges() {
		touching := m.FacesAtEdge(e)
		for a := 0; a < len(touching); a++ {
			ia, ok := slot[touching[a]]
			if !ok {
				continue
			}
			for b := a + 1; b < len(touching); b++ {
				ib, ok := slot[touching[b]]
				if !ok {
					continue
				}
				// A degenerate face has a zero normal, so this dot is 0 and it
				// joins nothing above a 90° limit — it becomes its own island,
				// which is the honest answer for a face with no plane.
				if normals[ia].Dot(normals[ib]) >= cosLimit {
					union(ia, ib)
				}
			}
		}
	}

	// Collect in first-seen order over the sorted face list, so islands come
	// out ordered by their lowest face index — stable across runs, unlike a
	// walk over the group map.
	groups := make(map[int][]int, len(list))
	var roots []int
	for i, fi := range list {
		r := find(i)
		if _, seen := groups[r]; !seen {
			roots = append(roots, r)
		}
		groups[r] = append(groups[r], fi)
	}

	out := make([]uvIsland, 0, len(roots))
	for _, r := range roots {
		out = append(out, m.projectIsland(groups[r]))
	}
	return out
}

// projectIsland flattens one island onto its best-fit plane.
//
// The plane's normal is the sum of the faces' *area vectors*, not of their unit
// normals: a chart is usually a few large faces plus a scattering of small
// ones, and weighting a sliver the same as the slab it sits on would tilt the
// projection towards the sliver and stretch everything else.
func (m *ModelDoc) projectIsland(faces []int) uvIsland {
	var n mgl32.Vec3
	for _, fi := range faces {
		n = n.Add(m.faceAreaVector(&m.Faces[fi]))
	}
	if n.Len() < 1e-9 {
		// The area vectors cancelled — a zero-area island, or two coincident
		// faces wound against each other. Fall back to any face that does have
		// a normal, then to +Y, so the island still lands somewhere finite
		// instead of producing NaN UVs.
		for _, fi := range faces {
			if fn := m.faceNormal(&m.Faces[fi]); fn.Len() > 0.5 {
				n = fn
				break
			}
		}
	}
	if n.Len() < 1e-9 {
		n = mgl32.Vec3{0, 1, 0}
	}
	n = n.Normalize()

	// The same basis ModelPlanarUnwrap builds, so a face unwrapped either way
	// comes out with its texture the same way up rather than mirrored.
	u := perpAxis(n)
	if u.Len() < 1e-6 {
		u = mgl32.Vec3{1, 0, 0}
	}
	u = u.Normalize()
	v := n.Cross(u).Normalize()

	is := uvIsland{Faces: faces, Coords: make(map[int]mgl32.Vec2)}
	origin := m.Verts[m.Faces[faces[0]].Verts[0]]
	minU, minV := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxU, maxV := -float32(math.MaxFloat32), -float32(math.MaxFloat32)
	for _, fi := range faces {
		for _, vi := range m.Faces[fi].Verts {
			if _, done := is.Coords[vi]; done {
				continue
			}
			d := m.Verts[vi].Sub(origin)
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

// faceAreaVector returns the face's Newell vector: a normal whose length is
// twice the face's area. faceNormal is this, normalized — the un-normalized
// form is what area-weighted averaging needs.
func (m *ModelDoc) faceAreaVector(f *ModelFace) mgl32.Vec3 {
	var n mgl32.Vec3
	cnt := len(f.Verts)
	if cnt < 3 {
		return n
	}
	for i := 0; i < cnt; i++ {
		a := m.Verts[f.Verts[i]]
		b := m.Verts[f.Verts[(i+1)%cnt]]
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
