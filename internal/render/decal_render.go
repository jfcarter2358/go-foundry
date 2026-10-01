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

import (
	"sort"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
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
	for _, i := range order {
		d := &lvl.Decals[i]
		if !set || d.Blend != blend {
			applyDecalBlend(d.Blend)
			blend, set = d.Blend, true
		}
		for pi := range d.Pieces {
			p := &d.Pieces[pi]
			m, ok := lvl.DecalPieceMatrix(p)
			if !ok {
				continue
			}
			p.Geometry.DrawWithTransform(m)
		}
		DebugStats.Decals++
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(0, 0)
	gl.DepthMask(true)
	gl.Disable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
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
