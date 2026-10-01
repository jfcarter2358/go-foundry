package compile

// light.go — baking the lighting into the geometry.
//
// A static surface lit by static lights computes the same colour every frame
// for the rest of the level's life. The compiler does it once instead, stores
// the answer on the vertices, and the release renderer interpolates it — which
// removes the per-fragment light loop, the shadow-map lookups and the cascade
// selection from every pixel of the world, in every pass.
//
// Per-vertex rather than lightmap atlases, which was settled up front: no
// second UV set, no atlas packer, no extra texture bind, and the result is
// three floats on a vertex the renderer was already sending.
//
// # Which means the vertices have to be dense enough to say anything
//
// That is the catch, and it is why SubdivideFaces exists. A twenty-unit floor
// is four vertices; baking a shadow onto it stores four numbers and
// interpolates a wash. So faces are cut on a world-aligned grid first, and only
// then lit. The grid is world-aligned rather than per-face precisely so that
// two faces meeting at an edge are cut at the SAME places along it: cut each
// face on its own bounds instead and the shared edge ends up with vertices on
// one side that the other has no counterpart for, and every one of those is a
// visible seam once the two sides interpolate different values.
//
// # The lighting model is the shader's, exactly
//
// Not an approximation of it. The whole point is that a baked surface and a
// live one look the same, so this reproduces fragment.glsl term for term:
// hemispheric ambient (GlobalLight.AmbientAt), then each directional light by
// wrapped Lambert (GlobalLight.LightWrap), then each point light by the same
// with a quadratic falloff that cuts off hard at the light's radius. Both are
// one-sided — a surface facing away from a light gets none of it — so a
// double-sided face is baked once per side, and a face that shades smooth
// (Face.VertexNormals) once more with those normals. The one thing done
// differently is the shadow, which is a ray against the drawn faces rather
// than a sampled shadow map, and is therefore sharper than the live version
// rather than softer.
//
// # Shadow rays test faces, not the tree
//
// The tree can answer "is this segment blocked" on its own, by whether it
// enters a solid cell, and the bake used to ask it that. It gets it wrong in
// both directions on real levels. A cell's solidity is the side of the last
// splitter it was built behind, which is right for a clean closed shell and
// wrong wherever CSG has cut one open — level1's AA hall baked in full sun under
// an intact roof. And a vertex where a cliff slab meets the terrain it overlaps
// starts its ray a bias's width inside the neighbour's solid cell, so it read
// as fully shadowed: about one sunlit vertex in eight in level1's pocket, each
// one a dark triangle fan in the middle of lit ground.
//
// So a shadow ray asks the same tree of drawn, opaque faces the bounce uses
// (faceBVH.occluded): blocked by the front of anything, or either side of a
// double-sided face. It sets out from open air (airstart.go), where a solid is
// always met front first; a detail face is its own surface, so it needs no help
// getting out of its hull and shadows its own recess.
//
// That is the direct light. On top of it goes the bounce (bounce.go), light
// carried on from one surface to the next — the one term the live shader has
// no counterpart for, and so the one way a compiled level deliberately looks
// different from the editor. It is why the direct answer is kept per exclusion
// group until the end (groupLights) rather than summed as it is sampled.
//
// # Only the static lights
//
// A light marked Dynamic is skipped here entirely. It is the one a script
// moves, dims or sends along a path, and a value frozen into the vertices
// cannot follow it — so fragment.glsl sums it live on top of the baked answer
// instead, shadow maps and all. A static light marked NoBake is skipped the same
// way: it belongs to geometry the compile leaves out, and baked it would stay on
// whatever it shone on after that geometry had gone. The global ambient has no
// such switch and is always baked.
//
// BakeExclude is the same idea aimed at one chunk rather than the whole bake: a
// light that should still be baked everywhere else, just not onto a chunk with
// no lasting reason to carry it (a hangar the level disables for good once the
// ship has left it, lit for the moment by the same sun the ground outside
// bakes normally). sample is handed the sampled face's source chunk for
// exactly this check.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/world"
)

// LightOptions tunes the bake.
type LightOptions struct {
	// GridStep is how far apart the baked samples are, in world units. Faces
	// are cut on a grid of this size before lighting, so it is the resolution
	// of every baked shadow edge in the level. Zero leaves faces uncut, which
	// is only sensible for a level already built out of small pieces.
	GridStep float32
	// MinGridStep is how fine the grid is made where a shadow edge crosses it
	// (refine.go): the step is halved there until it would go below this.
	// Zero, or anything not below GridStep, leaves the grid as it is.
	MinGridStep float32
	// ShadowBias is how far off a surface a shadow ray starts. Without it every
	// vertex is occluded by the face it is a corner of.
	ShadowBias float32
	// BounceSamples is how many rays each vertex gathers bounced light along,
	// per bounce (bounce.go). Zero means DefaultBounceSamples. How many bounces
	// there are is the level's own choice, GlobalLight.Bounces.
	BounceSamples int
	// MaxAlbedo caps how much of the light arriving at a surface it may send
	// on. A texture averaging to white would otherwise hand on everything it
	// received, and a closed white room would never stop brightening. Zero
	// means DefaultMaxAlbedo.
	MaxAlbedo float32
}

// DefaultBounceSamples and DefaultMaxAlbedo are LightOptions' zero-value
// meanings.
const (
	DefaultBounceSamples = 64
	DefaultMaxAlbedo     = float32(0.9)
)

// DefaultLightOptions is a reasonable bake for a level authored at the scale
// this engine's player moves at — a body is about two units tall, so a
// four-unit grid puts several samples across a doorway, and one unit is fine
// enough that a shadow edge reads as a line rather than a row of teeth.
func DefaultLightOptions() LightOptions {
	return LightOptions{GridStep: 4, MinGridStep: 1, ShadowBias: 0.05,
		BounceSamples: DefaultBounceSamples, MaxAlbedo: DefaultMaxAlbedo}
}

// BakeLight fills every compiled face's per-vertex lighting.
//
// The tree is modified: faces are cut on the grid first (SubdivideFaces), which
// renumbers them, so anything holding face indices from before the call is
// stale afterwards. That is why this runs after visibility, which cares about
// cells rather than about which face is which.
func BakeLight(t *BSPTree, lvl *world.Level, opt LightOptions) { bakeLight(t, lvl, opt, nil) }

// bakeLight is BakeLight, telling p how far it has got.
func bakeLight(t *BSPTree, lvl *world.Level, opt LightOptions, p Progress) {
	p.report("Light: cutting faces on the grid", 0)
	if opt.GridStep > 0 {
		t.SubdivideFaces(opt.GridStep)
	}
	b := &baker{tree: t, opt: opt, progress: p}
	if lvl != nil {
		b.lights = lvl.Lights
		b.seal = lvl.Seal
	}
	b.sun = b.lights.BakedSun()
	b.groupLights()
	b.bvh = buildFaceBVH(t, opt.GridStep > 0)
	p.report("Light: indexing shadow casters", 0)
	b.solids = newSolidIndex(lvl)
	// The segment a shadow ray covers for a directional light, which has no
	// position to aim at: far enough to leave the world box from anywhere
	// inside it.
	b.reach = t.Hi.Sub(t.Lo).Len() + 1
	b.softRadius = opt.GridStep
	if opt.GridStep > 0 && opt.MinGridStep > 0 && opt.MinGridStep < opt.GridStep {
		p.report("Light: refining shadow edges", 0)
		b.refineShadowEdges(opt.GridStep, opt.MinGridStep)
		b.bvh = buildFaceBVH(t, true)
		b.softRadius = opt.MinGridStep
	}

	// The direct light, kept per exclusion group (see lightGroups) until the
	// bounce has had its turn, because what a bounce may carry into a chunk
	// depends on which lights were allowed there in the first place.
	direct := make([]faceSamples, len(t.Faces))
	p.parallelFor(fmt.Sprintf("Light: direct (%d faces)", len(t.Faces)), len(t.Faces), func(fi int) {
		f := &t.Faces[fi]
		f.dropLight()
		direct[fi] = b.directFace(fi)
	})

	total := direct
	if bounces := min(b.lights.Global.Bounces, world.MaxBounces); bounces > 0 {
		total = b.bounce(direct, bounces)
	}
	p.report("Light: summing", 0)
	for fi := range t.Faces {
		f := &t.Faces[fi]
		fs := &total[fi]
		f.Light = fs.sum(kindFront, b.groups())
		f.BackLight = fs.sum(kindBack, b.groups())
		f.SmoothLight = fs.sum(kindSmoothFront, b.groups())
		f.SmoothBackLight = fs.sum(kindSmoothBack, b.groups())
		f.SunShare, f.BackSunShare = direct[fi].sun[0], direct[fi].sun[1]

		smooth := f.VertexNormals()
		f.Indirect = b.indirect(f, smooth, kindFront, f.Light, &direct[fi])
		f.BackIndirect = b.indirect(f, smooth, kindBack, f.BackLight, &direct[fi])
		f.SmoothIndirect = b.indirect(f, smooth, kindSmoothFront, f.SmoothLight, &direct[fi])
		f.SmoothBackIndirect = b.indirect(f, smooth, kindSmoothBack, f.SmoothBackLight, &direct[fi])
	}
}

// indirect is the part of answer k (total, already summed) that live GI
// replaces: the ambient term plus whatever the bounce added — everything but
// the static lights' own direct light. The ambient is worked out again rather
// than kept from the direct pass, where it is folded into group 0 with the
// lights that have no exclusions; it is the same AmbientAt of the same normal,
// so the subtraction leaves exactly the lights.
func (b *baker) indirect(f *Face, smooth []mgl32.Vec3, k int, total []mgl32.Vec3, direct *faceSamples) []mgl32.Vec3 {
	if total == nil {
		return nil
	}
	normals, side, ok := kindFrame(f, smooth, k)
	if !ok {
		return nil
	}
	lit := direct.sum(k, b.groups())
	out := make([]mgl32.Vec3, len(total))
	for i := range out {
		n := f.Plane.N
		if normals != nil {
			n = normals[i]
		}
		v := total[i].Add(b.lights.Global.AmbientAt(n.Mul(side)))
		if i < len(lit) {
			v = v.Sub(lit[i])
		}
		// The bounce never takes light away, so a negative is float noise.
		out[i] = mgl32.Vec3{max(v.X(), 0), max(v.Y(), 0), max(v.Z(), 0)}
	}
	return out
}

// The four answers a face can carry — Face.Light, SmoothLight, BackLight and
// SmoothBackLight — as indices into faceSamples.
const (
	kindFront = iota
	kindSmoothFront
	kindBack
	kindSmoothBack
	numKinds
)

// faceSamples is one face's light per answer it carries, per exclusion group:
// kinds[k][g*len(Points)+i] is group g's share at point i. A kind the face does
// not carry (no back on a single-sided face, no smooth on a flat one) is nil.
type faceSamples struct {
	kinds [numKinds][]mgl32.Vec3
	// sun is Face.SunShare and Face.BackSunShare, which the bounce leaves as
	// they are.
	sun [2][]float32
}

// sum is kind k with every group added up — what the renderer is handed.
func (fs *faceSamples) sum(k, groups int) []mgl32.Vec3 {
	v := fs.kinds[k]
	if v == nil {
		return nil
	}
	n := len(v) / groups
	out := make([]mgl32.Vec3, n)
	for g := 0; g < groups; g++ {
		for i := range out {
			out[i] = out[i].Add(v[g*n+i])
		}
	}
	return out
}

// kindFrame is how kind k of face f is sampled: the normal at each point (nil
// for the plane's), and which side of the face it is.
func kindFrame(f *Face, smooth []mgl32.Vec3, k int) (normals []mgl32.Vec3, side float32, ok bool) {
	switch k {
	case kindFront:
		return nil, 1, true
	case kindSmoothFront:
		return smooth, 1, smooth != nil
	case kindBack:
		return nil, -1, f.DoubleSided
	case kindSmoothBack:
		return smooth, -1, smooth != nil && f.DoubleSided
	}
	return nil, 0, false
}

// directFace is every answer face fi carries, lit by the static lights alone.
func (b *baker) directFace(fi int) faceSamples {
	f := &b.tree.Faces[fi]
	var fs faceSamples
	smooth := f.VertexNormals()
	groups := b.groups()
	// One probe per corner per side, shared by the flat and smooth answers:
	// where a shadow falls does not depend on which normal shades the point.
	var probes [2][]*shadowProbe
	for k := 0; k < numKinds; k++ {
		normals, side, ok := kindFrame(f, smooth, k)
		if !ok {
			continue
		}
		si := 0
		if side < 0 {
			si = 1
		}
		if probes[si] == nil {
			probes[si] = make([]*shadowProbe, len(f.Points))
			for i := range f.Points {
				probes[si][i] = b.probe(f, fi, i, f.Plane.N.Mul(side))
			}
			if b.sun >= 0 {
				fs.sun[si] = make([]float32, len(f.Points))
			}
		}
		out := make([]mgl32.Vec3, groups*len(f.Points))
		acc := make([]mgl32.Vec3, groups)
		for i, p := range f.Points {
			n := f.Plane.N
			if normals != nil {
				n = normals[i]
			}
			clear(acc)
			share := b.sample(p, n.Mul(side), probes[si][i], f.Chunk, acc)
			if normals == nil && fs.sun[si] != nil {
				fs.sun[si][i] = share
			}
			for g := range acc {
				out[g*len(f.Points)+i] = acc[g]
			}
		}
		fs.kinds[k] = out
	}
	return fs
}

type baker struct {
	// progress hears the bake's steps; nil for a bake nobody is watching.
	progress Progress
	tree     *BSPTree
	lights   world.LightSet
	opt      LightOptions
	reach    float32
	// bvh is the drawn, opaque faces every shadow ray is tested against —
	// see "Shadow rays test faces" in the file comment.
	bvh *faceBVH
	// solids is the level's brushes, for keeping a ray's start out of them
	// (airstart.go). nil for a tree baked without its level.
	solids *solidIndex
	// softRadius is how far a corner's soft-shadow patch reaches
	// (softshadow.go): the finest grid step, the one the faces a shadow edge
	// crosses are cut on (refine.go). Faces cut coarser have no edge to soften,
	// and a patch their own size would only smear the neighbours' edges onto
	// them.
	softRadius float32
	// sun is lights.BakedSun(): the light Face.SunShare is kept for, or -1.
	sun int
	// seal is the level's sealing box, where a sun's shadow ray stops (see
	// sealExit). nil, or switched off, for a level that seals itself.
	seal *world.Seal

	// The lights sorted into exclusion groups — see groupLights.
	groupExclude [][]string
	dirGroup     []int
	pointGroup   []int
}

// groupLights sorts the static lights by the chunks they may not bake into.
//
// Direct light only needs BakeExclude checked at the point being lit. Bounced
// light needs more: a sun kept off the hangar must not reach it second-hand
// either, off the sunlit ground outside the door. So the bake carries each set
// of lights with the same exclusions as its own group, and a bounce into a
// chunk only picks up the groups allowed there. Group 0 is the ambient and
// every light with no exclusions, which in most levels is all there is.
func (b *baker) groupLights() {
	index := map[string]int{"": 0}
	b.groupExclude = [][]string{nil}
	groupOf := func(excl []string) int {
		if len(excl) == 0 {
			return 0
		}
		sorted := slices.Clone(excl)
		slices.Sort(sorted)
		key := strings.Join(sorted, "\x00")
		if g, ok := index[key]; ok {
			return g
		}
		index[key] = len(b.groupExclude)
		b.groupExclude = append(b.groupExclude, sorted)
		return len(b.groupExclude) - 1
	}
	b.dirGroup = make([]int, len(b.lights.Directional))
	for i := range b.lights.Directional {
		b.dirGroup[i] = groupOf(b.lights.Directional[i].BakeExclude)
	}
	b.pointGroup = make([]int, len(b.lights.Points))
	for i := range b.lights.Points {
		b.pointGroup[i] = groupOf(b.lights.Points[i].BakeExclude)
	}
}

// groups is how many exclusion groups the bake carries (never fewer than 1).
func (b *baker) groups() int { return max(len(b.groupExclude), 1) }

// groupAllowedIn is whether group g's lights may light chunk.
func (b *baker) groupAllowedIn(g int, chunk string) bool {
	return g >= len(b.groupExclude) || !slices.Contains(b.groupExclude[g], chunk)
}

// sample is fragment.glsl's lighting loop, for one point on a surface, over the
// static lights only (see the file comment), added into out by exclusion group
// (groupLights) — out has one slot per group, and the whole answer is their
// sum. n is the normal lit with; pr is the corner's shadow probe (softshadow.go),
// which also knows where its rays start. chunk is the face's source chunk
// (Face.Chunk), which is the one thing a light's BakeExclude is checked
// against — see DirectionalLight.BakeExclude.
//
// It returns the baked sun's share (Face.SunShare): what fraction of the sun's
// unshadowed light this point was given — 0 where it faces away — or -1 where
// the sun was left out of this chunk's bake (BakeExclude), which the renderer
// must not mistake for a point the sun's shadow covers.
func (b *baker) sample(p, n mgl32.Vec3, pr *shadowProbe, chunk string, out []mgl32.Vec3) (sunShare float32) {
	g := b.lights.Global
	out[0] = out[0].Add(g.AmbientAt(n))

	for i := range b.lights.Directional {
		dl := &b.lights.Directional[i]
		bright := dl.EffectiveBrightness()
		if bright <= 0 || dl.LitLive() || slices.Contains(dl.BakeExclude, chunk) {
			if i == b.sun {
				sunShare = -1
			}
			continue
		}
		d := dl.Direction
		if d.Len() < 1e-6 {
			continue
		}
		toLight := d.Normalize().Mul(-1)
		contrib := dl.Color.Mul(bright * g.LightWrap(n.Dot(toLight)))
		if contrib == (mgl32.Vec3{}) {
			continue // facing away: no ray to cast
		}
		kept := float32(1)
		if dl.CastsShadows() {
			lit := pr.lit(i, func(start mgl32.Vec3) (mgl32.Vec3, float32, bool) {
				reach := b.reach
				if e, ok := sealExit(b.seal, start, toLight); ok {
					reach = e.Sub(start).Len()
				}
				return toLight, reach, true
			})
			kept = 1 - dl.ShadowStrength*(1-lit)
		}
		if i == b.sun {
			sunShare = kept
		}
		out[b.dirGroup[i]] = out[b.dirGroup[i]].Add(contrib.Mul(kept))
	}

	for i := range b.lights.Points {
		pl := &b.lights.Points[i]
		bright := pl.EffectiveBrightness()
		// An attached light is dynamic by construction (PointLight.Attach); the
		// check is repeated so a level built in code cannot bake one at its
		// carrier-local offset.
		if bright <= 0 || pl.Radius <= 0 || pl.LitLive() || slices.Contains(pl.BakeExclude, chunk) {
			continue
		}
		toLight := pl.Position.Sub(p)
		dist := toLight.Len()
		if dist >= pl.Radius {
			continue
		}
		// The shader's falloff, kept exactly: linear in distance, squared.
		t := 1 - dist/pl.Radius
		if dist < 1e-6 {
			// On the light itself the direction is undefined; the surface faces
			// it as squarely as it can.
			out[b.pointGroup[i]] = out[b.pointGroup[i]].Add(pl.Color.Mul(bright))
			continue
		}
		contrib := pl.Color.Mul(bright * g.LightWrap(n.Dot(toLight.Mul(1/dist))) * t * t)
		if contrib == (mgl32.Vec3{}) {
			continue
		}
		if pl.CastsShadows() {
			pos := pl.Position
			lit := pr.lit(len(b.lights.Directional)+i, func(start mgl32.Vec3) (mgl32.Vec3, float32, bool) {
				ray := pos.Sub(start)
				l := ray.Len()
				if l <= 1e-6 {
					return mgl32.Vec3{}, 0, false
				}
				return ray.Mul(1 / l), l, true
			})
			contrib = contrib.Mul(1 - pl.ShadowStrength*(1-lit))
		}
		out[b.pointGroup[i]] = out[b.pointGroup[i]].Add(contrib)
	}
	return sunShare
}

// SubdivideFaces cuts every face on a world-aligned grid of the given size, and
// drops the faces nothing references.
//
// The two jobs are one pass because both renumber: the tree's face list still
// holds every intermediate a BSP split produced, and rebuilding it from what
// the nodes actually kept is the moment to replace each survivor with its
// pieces.
func (t *BSPTree) SubdivideFaces(step float32) {
	if step <= 0 {
		return
	}
	used := t.usedFaces()

	faces := make([]Face, 0, len(t.Faces))
	remap := make([][]int, len(t.Faces))
	for fi := range t.Faces {
		if !used[fi] || t.Faces[fi].NoDraw {
			// A structural face is never seen, so there is nothing to light
			// and no reason to carry it any further: the tree it shaped is
			// built, and visibility has already been computed over it.
			continue
		}
		for _, piece := range gridSubdivide(t.Faces[fi].Points, step) {
			g := t.Faces[fi]
			g.Points = piece
			g.dropLight()
			faces = append(faces, g)
			remap[fi] = append(remap[fi], len(faces)-1)
		}
	}
	t.Faces = faces

	for ni := range t.Nodes {
		t.Nodes[ni].Faces = remapIndices(t.Nodes[ni].Faces, remap)
	}
	for li := range t.Leaves {
		t.Leaves[li].Faces = remapIndices(t.Leaves[li].Faces, remap)
	}
}

// usedFaces is which of the tree's faces survived the build. A node's list is
// the authority: every face a leaf holds was attached from one.
func (t *BSPTree) usedFaces() []bool {
	used := make([]bool, len(t.Faces))
	for ni := range t.Nodes {
		for _, fi := range t.Nodes[ni].Faces {
			used[fi] = true
		}
	}
	// A detail face never reaches a node: it is inserted straight into the
	// cells the structural faces made (bsp.go), so for those the LEAVES are
	// the authority. Reading the nodes alone would drop every one of them
	// here, and a level would compile with its detail missing.
	for li := range t.Leaves {
		for _, fi := range t.Leaves[li].Faces {
			if t.Faces[fi].Detail {
				used[fi] = true
			}
		}
	}
	// A cover face is in neither: it is drawn past the tree altogether (see
	// Face.CoveredBy). Every one CSG produced is used.
	for fi := range t.Faces {
		if t.Faces[fi].CoveredBy != nil {
			used[fi] = true
		}
	}
	return used
}

func remapIndices(in []int, remap [][]int) []int {
	var out []int
	for _, fi := range in {
		out = append(out, remap[fi]...)
	}
	return out
}

// gridSubdivide cuts a convex polygon on the world grid until no piece spans
// more than step on any axis.
//
// Cut against absolute grid lines rather than against the polygon's own bounds,
// so that two faces meeting along an edge are cut at the same coordinates and
// their shared edge has matching vertices. See the file comment on why that
// matters more than it sounds like it should.
func gridSubdivide(pts []mgl32.Vec3, step float32) [][]mgl32.Vec3 {
	pieces := [][]mgl32.Vec3{pts}
	for axis := 0; axis < 3; axis++ {
		var next [][]mgl32.Vec3
		for _, p := range pieces {
			next = append(next, splitOnAxis(p, axis, step)...)
		}
		pieces = next
	}
	return pieces
}

func splitOnAxis(pts []mgl32.Vec3, axis int, step float32) [][]mgl32.Vec3 {
	lo, hi := bsp.Bounds(pts)
	if hi[axis]-lo[axis] <= step+bsp.OnPlaneEps {
		return [][]mgl32.Vec3{pts}
	}
	// The first grid line strictly inside the polygon's span. It is less than
	// one step above lo, so the piece below it never needs cutting again and
	// only the piece above recurses.
	k := float32(int(lo[axis]/step)) * step
	for k <= lo[axis]+bsp.OnPlaneEps {
		k += step
	}
	if k >= hi[axis]-bsp.OnPlaneEps {
		return [][]mgl32.Vec3{pts}
	}
	var n mgl32.Vec3
	n[axis] = 1
	front, back := bsp.SplitPolygon(pts, bsp.Plane{N: n, D: k})

	var out [][]mgl32.Vec3
	if len(back) >= 3 && bsp.PolygonArea(back) >= bsp.MinFragmentArea {
		out = append(out, back)
	}
	if len(front) >= 3 && bsp.PolygonArea(front) >= bsp.MinFragmentArea {
		out = append(out, splitOnAxis(front, axis, step)...)
	}
	if len(out) == 0 {
		// The cut produced nothing usable, which means the polygon was a sliver
		// lying along the grid line. Keep it whole rather than losing it.
		return [][]mgl32.Vec3{pts}
	}
	return out
}
