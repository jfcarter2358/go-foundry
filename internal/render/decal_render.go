package render

// decal_render.go — drawing the level's projected decals.
//
// A pass of its own, between the opaque world and the sky, and every part of
// that placement is load-bearing.
//
// AFTER the opaque world, because a decal blends with the surface under it and
// that surface has to have been drawn. BEFORE the sky and the sorted transparent
// pass, because a decal is part of an opaque surface rather than a translucent
// object floating in the level: it must not sort against glass and water, it must
// be under them, and it needs no sorting among itself beyond its authored Order.
//
// The depth state is the whole trick. A decal's triangles are the surface's own
// triangles, a couple of millimetres out, so they sit at almost exactly the depth
// already in the buffer:
//
//   - The test stays on, and that is what clips a decal against everything in
//     front of it. A bullet hole behind a crate is hidden by the crate for free,
//     which is the thing a screen-space decal has to work for.
//   - Depth WRITING is off. A decal adds no occlusion of its own, and letting it
//     write would make two overlapping decals fight over which one is nearer when
//     neither is.
//   - A polygon offset pulls them toward the viewer by a slope-scaled amount,
//     which is what separates the decal from its surface at every viewing angle
//     the flat push in the mesh (Decal.Push) cannot cover on its own.
//
// Culling is the decal's own box against the frustum, tested once for the whole
// decal rather than per piece: a decal is a few centimetres across, so its pieces
// are all in the same place and testing them apart would cost more than it saves.
//
// Pieces are drawn in RUNS. A firefight leaves a wall of bullet holes that are
// all one texture, one tint, one blend, and a decal's piece used to be a whole
// surface draw of its own — every uniform the world program's surface takes, a
// texture, a VAO. Consecutive pieces in the draw order whose draw state is the
// same, and whose vertices are already where they are drawn (a compiled
// surface's, or an unmoved chunk's: an identity matrix), are one draw out of a
// buffer holding all their triangles, kept from frame to frame and rebuilt only
// when the run's pieces change. Drawing them as one is drawing them in the
// same order with the same state, so nothing on screen changes; a piece that
// needs a matrix still draws alone, so no vertex is carried through a
// different sum than the GPU would have done.

import (
	"sort"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// DecalRebuildBudget is how many decals SyncDecals may re-clip in one frame.
//
// Small on purpose. A rebuild walks every triangle whose brush overlaps the
// decal's box, so a level opening with two hundred authored decals would spend
// most of a second on them if they all went at once — and an author dragging one
// with the gizmo rebuilds that one per mouse event, which must not be starved by
// a queue behind it. Four a frame clears a hundred in under half a second, which
// is inside a level load's own budget and invisible during editing.
const DecalRebuildBudget = 4

// RenderDecals draws every visible decal in the level.
//
// It owns its GL state completely — blend, depth mask, polygon offset — and puts
// all of it back, so nothing after this pass inherits a decal's settings. That
// matters more here than in most passes: a depth mask left off would stop the sky
// and every transparent surface after it from depth-testing correctly.
func RenderDecals(lvl *world.Level, cull *Frustum) {
	DebugStats.Decals = 0
	if lvl == nil || len(lvl.Decals) == 0 {
		return
	}
	// Rebuild whatever went stale — a decal that moved, or one whose surface was
	// edited under it. Here rather than in the frame loop so that the pass that
	// needs the mesh is the pass that asks for it.
	world.SyncDecals(lvl, DecalRebuildBudget)

	order := decalDrawOrder(lvl, cull)
	if len(order) == 0 {
		return
	}

	gl.UseProgram(gfx.Program)
	gl.Enable(gl.BLEND)
	gl.DepthMask(false)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	// Negative: toward the viewer. The slope factor is what handles a surface
	// seen at an angle, where a constant offset in depth units is worth almost
	// nothing; the constant term covers a surface seen face-on, where the slope
	// is zero. One texel-ish of each is the smallest pair that stops the fighting
	// without floating the decal visibly off its wall.
	gl.PolygonOffset(-1, -1)

	// The blend function is changed only when the mode does, which for the common
	// level — every decal alpha-blended — means once. `set` rather than comparing
	// against a sentinel, because every DecalBlend value is a legitimate mode and
	// none of them can stand in for "not yet applied".
	var blend gfx.DecalBlend
	set := false
	runs := &decalRuns
	runs.begin()
	for _, i := range order {
		d := &lvl.Decals[i]
		if !set || d.Blend != blend {
			// A run never crosses a change of blend function.
			runs.flush()
			applyDecalBlend(d.Blend)
			blend, set = d.Blend, true
		}
		for pi := range d.Pieces {
			p := &d.Pieces[pi]
			m, ok := lvl.DecalPieceMatrix(p)
			if !ok {
				continue
			}
			runs.add(&p.Geometry, m)
		}
		DebugStats.Decals++
	}
	runs.flush()
	runs.end()

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(0, 0)
	gl.DepthMask(true)
	gl.Disable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
}

// decalBatching switches the runs off, for the test that checks they draw
// what the pieces drawn one at a time do.
var decalBatching = true

// decalRunPiece is what a run holds of one piece: the arrays its buffers were
// made from, kept so their memory cannot pass to another piece while the run
// remembers them — which is what lets the run know its pieces by the arrays'
// addresses alone.
type decalRunPiece struct {
	verts, normals, light []float32
}

// decalRun is one run's merged buffers and what they were made from.
type decalRun struct {
	pieces []decalRunPiece
	batch  gfx.Batch
	verts  []float32
}

// decalRunner gathers the frame's pieces into runs and draws them.
type decalRunner struct {
	// pending is the run being gathered: its first piece, whose draw state
	// the run draws with, that state, and the pieces.
	first   *gfx.Geometry
	firstM  mgl32.Mat4
	state   gfx.SurfaceState
	baked   int32
	pending []*gfx.Geometry
	// runs are the merged runs drawn this frame, in order, and kept for the
	// next; used is how many this frame has drawn.
	runs []decalRun
	used int
}

var decalRuns decalRunner

func (r *decalRunner) begin() { r.used, r.pending, r.first = 0, r.pending[:0], nil }

// end lets go of the runs this frame did not draw.
func (r *decalRunner) end() {
	for i := r.used; i < len(r.runs); i++ {
		r.runs[i].batch.Delete()
		r.runs[i] = decalRun{}
	}
	r.runs = r.runs[:r.used]
}

// add draws g under model, or holds it to be drawn with the pieces before it.
func (r *decalRunner) add(g *gfx.Geometry, model mgl32.Mat4) {
	if len(g.Vertices) == 0 {
		return
	}
	identity := model == mgl32.Ident4()
	mergeable := decalBatching && identity && g.Ebo == 0 && len(g.BonePalette) == 0 && !g.IsSkinned()
	if !mergeable {
		r.flush()
		g.DrawWithTransform(model)
		return
	}
	st := g.SurfaceState()
	baked := g.BakedLightFlag()
	if r.first != nil && (st != r.state || baked != r.baked ||
		(len(g.Normals) > 0) != (len(r.first.Normals) > 0)) {
		r.flush()
	}
	if r.first == nil {
		r.first, r.firstM, r.state, r.baked = g, model, st, baked
	}
	r.pending = append(r.pending, g)
}

// flush draws the run gathered so far: a single piece as it always was, more
// than one out of the run's merged buffers.
func (r *decalRunner) flush() {
	defer func() { r.pending, r.first = r.pending[:0], nil }()
	switch len(r.pending) {
	case 0:
		return
	case 1:
		r.first.DrawWithTransform(r.firstM)
		return
	}
	if r.used == len(r.runs) {
		r.runs = append(r.runs, decalRun{})
	}
	run := &r.runs[r.used]
	r.used++
	run.sync(r.pending, r.baked != 0)
	// The first piece's every uniform and texture, drawn from the run's
	// buffers: a copy of it pointed at them.
	g := *r.first
	g.Vao, g.Lbo, g.Ebo, g.Indices = run.batch.Vao, run.batch.Lbo, 0, nil
	g.Vertices = run.verts
	if g.Vao == 0 {
		return
	}
	g.DrawWithTransform(r.firstM)
}

// sync makes the run's buffers hold pieces' triangles, unless they already do.
func (run *decalRun) sync(pieces []*gfx.Geometry, baked bool) {
	same := len(run.pieces) == len(pieces)
	for i := 0; same && i < len(pieces); i++ {
		g, k := pieces[i], &run.pieces[i]
		same = sameFloats(k.verts, g.Vertices) && sameFloats(k.normals, g.Normals) &&
			(!baked || sameFloats(k.light, g.BakedLightData()))
	}
	if same && (run.batch.Vao != 0 || !gfx.GeometryUploadsEnabled) {
		return
	}
	run.pieces = run.pieces[:0]
	run.verts = run.verts[:0]
	var normals, light []float32
	for _, g := range pieces {
		k := decalRunPiece{verts: g.Vertices, normals: g.Normals}
		run.verts = append(run.verts, g.Vertices...)
		normals = append(normals, g.Normals...)
		if baked {
			k.light = g.BakedLightData()
			light = append(light, k.light...)
		}
		run.pieces = append(run.pieces, k)
	}
	run.batch.Upload(run.verts, normals, light)
}

// sameFloats is whether a and b are the same array, not merely equal.
func sameFloats(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

// applyDecalBlend sets the blend function for one decal blend mode.
//
// Multiply is the one worth a note. `DST_COLOR * src` means the fragment's colour
// SCALES what is already on the wall, so a white texel leaves the wall alone and
// a black one blacks it out — which is why one grey scorch texture reads correctly
// on a pale wall and a dark floor.
//
// Its source factor is DST_COLOR and its destination factor ZERO, with nothing
// for the fragment's ALPHA to do. That is deliberate: this equation can express
// `src * dst` and cannot express `dst * mix(1, src, a)`, so leaving the alpha to
// the blender would make a half-transparent WHITE texel come out at 1.5 times the
// wall's brightness instead of leaving it alone. The alpha is folded toward white
// in the shader instead (fragment.glsl, surfaceBlend == 2), which is what makes a
// mask with a soft edge — or one fading out at the end of its lifetime — fade
// rather than glow.
func applyDecalBlend(b gfx.DecalBlend) {
	switch b {
	case gfx.DecalBlendMultiply:
		gl.BlendFunc(gl.DST_COLOR, gl.ZERO)
	case gfx.DecalBlendAdditive:
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE)
	default:
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	}
}

// decalDrawOrder returns the indices of the decals to draw, in the order they
// should be drawn: by Order, then by level order, which is placement order.
//
// The scratch slice is kept between frames so a level with three hundred decals
// allocates nothing per frame. Sorted with a stable sort, because the tie-break
// IS the level order and a sort that reshuffled equal keys would make two
// overlapping decals swap which was on top from frame to frame.
func decalDrawOrder(lvl *world.Level, cull *Frustum) []int {
	decalOrderScratch = decalOrderScratch[:0]
	for i := range lvl.Decals {
		d := &lvl.Decals[i]
		if !d.Drawable() || !DecalInFrustum(d, cull) {
			continue
		}
		decalOrderScratch = append(decalOrderScratch, i)
	}
	sort.SliceStable(decalOrderScratch, func(a, b int) bool {
		return lvl.Decals[decalOrderScratch[a]].Order < lvl.Decals[decalOrderScratch[b]].Order
	})
	return decalOrderScratch
}

var decalOrderScratch []int

// DecalInFrustum reports whether a decal's projection box survives frustum
// culling. A decal with no box authored is never culled, for the reason a scenery
// placement with no bounds is not: it has nothing to cull by, and an empty box
// would make it invisible everywhere except dead centre of the view.
func DecalInFrustum(d *world.Decal, cull *Frustum) bool {
	if cull == nil {
		return true
	}
	min, max, ok := d.WorldAABB()
	if !ok {
		return true
	}
	return AABBInFrustum(*cull, min, max)
}
