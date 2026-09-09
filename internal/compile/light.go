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
// ambient, then each directional light by abs(N·L) — the engine lights both
// sides of a surface — then each point light by the same with a quadratic
// falloff that cuts off hard at the light's radius. The one thing done
// differently is the shadow, which is a ray through the BSP rather than a
// sampled shadow map, and is therefore sharper than the live version rather
// than softer.

import (
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
	// ShadowBias is how far off a surface a shadow ray starts. Without it every
	// vertex is occluded by the face it is a corner of.
	ShadowBias float32
}

// DefaultLightOptions is a reasonable bake for a level authored at the scale
// this engine's player moves at — a body is about two units tall, so a
// four-unit grid puts several samples across a doorway.
func DefaultLightOptions() LightOptions {
	return LightOptions{GridStep: 4, ShadowBias: 0.05}
}

// BakeLight fills every compiled face's per-vertex lighting.
//
// The tree is modified: faces are cut on the grid first (SubdivideFaces), which
// renumbers them, so anything holding face indices from before the call is
// stale afterwards. That is why this runs after visibility, which cares about
// cells rather than about which face is which.
func BakeLight(t *BSPTree, lvl *world.Level, opt LightOptions) {
	if opt.GridStep > 0 {
		t.SubdivideFaces(opt.GridStep)
	}
	b := &baker{tree: t, opt: opt}
	if lvl != nil {
		b.lights = lvl.Lights
	}
	// The segment a shadow ray covers for a directional light, which has no
	// position to aim at: far enough to leave the world box from anywhere
	// inside it.
	b.reach = t.Hi.Sub(t.Lo).Len() + 1

	for fi := range t.Faces {
		f := &t.Faces[fi]
		f.Light = make([]mgl32.Vec3, len(f.Points))
		for i, p := range f.Points {
			f.Light[i] = b.sample(p, f.Plane.N)
		}
	}
}

type baker struct {
	tree   *BSPTree
	lights world.LightSet
	opt    LightOptions
	reach  float32
}

// sample is fragment.glsl's lighting loop, for one point on a surface.
func (b *baker) sample(p, n mgl32.Vec3) mgl32.Vec3 {
	g := b.lights.Global
	out := g.Color.Mul(g.EffectiveBrightness())
	origin := p.Add(n.Mul(b.opt.ShadowBias))

	for i := range b.lights.Directional {
		dl := &b.lights.Directional[i]
		bright := dl.EffectiveBrightness()
		if bright <= 0 {
			continue
		}
		d := dl.Direction
		if d.Len() < 1e-6 {
			continue
		}
		toLight := d.Normalize().Mul(-1)
		contrib := dl.Color.Mul(bright * bsp.Abs(n.Dot(toLight)))
		if dl.CastsShadows() && !b.tree.LineOfSight(origin, origin.Add(toLight.Mul(b.reach))) {
			contrib = contrib.Mul(1 - dl.ShadowStrength)
		}
		out = out.Add(contrib)
	}

	for i := range b.lights.Points {
		pl := &b.lights.Points[i]
		bright := pl.EffectiveBrightness()
		if bright <= 0 || pl.Radius <= 0 {
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
			out = out.Add(pl.Color.Mul(bright))
			continue
		}
		contrib := pl.Color.Mul(bright * bsp.Abs(n.Dot(toLight.Mul(1/dist))) * t * t)
		if pl.CastsShadows() && !b.tree.LineOfSight(origin, pl.Position) {
			contrib = contrib.Mul(1 - pl.ShadowStrength)
		}
		out = out.Add(contrib)
	}
	return out
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
	// Which faces survived the build. A node's list is the authority: every
	// face a leaf holds was attached from one.
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
			g.Light = nil
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
