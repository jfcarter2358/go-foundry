package editor

// editor_model_visibility.go — which points and edges the camera can actually
// see, and therefore which of them can be clicked.
//
// In solid shading the mesh hides its own far side, so drawing a handle on
// every vertex is a lie: half of them sit behind the surface, and clicking one
// grabs geometry the user cannot see. Wireframe is the opposite contract —
// nothing is hidden, so everything is fair game. modelWireframe switches
// between the two, and this file answers the question the solid mode needs.
//
// Visibility is a real occlusion test — a ray from the eye to the vertex,
// blocked or not — rather than the cheaper "is any face it touches turned
// towards me". The cheap test is exact on a convex solid and wrong on
// everything else: the far side of a torus, or a limb behind a torso, would
// keep offering handles through the surface in front of them.
//
// The result is cached against the eye position and the document's revision,
// which is what makes the exact test affordable. Note the key does not include
// the view *direction*: what a point eye can see past an opaque mesh depends
// only on where that eye is, so turning on the spot — or flying a mouse-look —
// costs nothing to recheck.

import (
	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// modelWireframe draws the mesh as lines only and makes every point and edge
// selectable. Off, the mesh is solid and only what can be seen can be picked.
var modelWireframe bool

// modelVisTriBudget caps how much work one visibility pass may do, measured in
// vertex-triangle tests. Past it the pass falls back to the front-facing
// approximation described above.
//
// The exact test is O(verts x triangles), which grows quadratically under
// repeated subdivision: measured here, a cube is 96 tests and three levels of
// Catmull-Clark is 296,000 of them — 8.7ms, which is half a frame. The cache
// means that price is only paid when the eye actually moves, and the budget is
// set so that when it is paid it stays around a millisecond. Past it a heavy
// mesh gets slightly optimistic handles instead of a stuttering viewport.
const modelVisTriBudget = 60000

// modelVis caches the last pass. verts is index-parallel to ModelDoc.Verts.
var modelVis struct {
	verts []bool
	exact bool // false when the budget forced the front-facing fallback

	// The state the cache was computed for. doc is compared by pointer as well
	// as revision because undo swaps in a different document whose own counter
	// is unrelated to the one it replaced.
	valid     bool
	doc       *model.ModelDoc
	rev       uint64
	eye       mgl32.Vec3
	wireframe bool
}

// modelInvalidateVisibility forces the next pass to recompute. Called when the
// shading mode changes, which alters the answer without moving the camera.
func modelInvalidateVisibility() { modelVis.valid = false }

// modelVertexVisible reports whether vertex vi can be seen from the camera.
// Always true in wireframe, where nothing occludes anything.
func modelVertexVisible(vi int) bool {
	if modelWireframe {
		return true
	}
	if vi < 0 || vi >= len(modelVis.verts) {
		// No pass has run yet (a test harness, or a click before the first
		// frame). Erring towards selectable beats a dead viewport.
		return true
	}
	return modelVis.verts[vi]
}

// modelEdgeVisible reports whether edge e can be seen. An edge counts as
// visible when either endpoint is: an edge running behind an occluder but
// emerging at one end is still something the user can see and aim at.
func modelEdgeVisible(e model.EdgeKey) bool {
	return modelWireframe || modelVertexVisible(e.A) || modelVertexVisible(e.B)
}

// modelTri is one triangle of the mesh in world space, with the vertex indices
// it was built from so a vertex can skip the triangles it belongs to.
type modelTri struct {
	a, b, c mgl32.Vec3
	verts   [3]int
}

// modelCollectTris triangulates the whole mesh once. faceTriangles ear-clips on
// every call, so calling it inside the per-vertex loop would re-triangulate the
// model once per vertex.
func modelCollectTris() []modelTri {
	if modelDoc == nil {
		return nil
	}
	out := make([]modelTri, 0, len(modelDoc.Faces)*2)
	for fi := range modelDoc.Faces {
		f := &modelDoc.Faces[fi]
		for _, t := range modelDoc.FaceTriangles(fi) {
			va, vb, vc := f.Verts[t[0]], f.Verts[t[1]], f.Verts[t[2]]
			out = append(out, modelTri{
				a:     modelDoc.Verts[va],
				b:     modelDoc.Verts[vb],
				c:     modelDoc.Verts[vc],
				verts: [3]int{va, vb, vc},
			})
		}
	}
	return out
}

// modelUpdateVisibility brings the visibility cache up to date. Cheap to call
// repeatedly: it returns immediately unless the eye or the mesh has moved, so
// the draw path and every picking entry point can each just call it.
func modelUpdateVisibility() {
	if modelDoc == nil {
		modelVis.verts = nil
		modelVis.valid = false
		return
	}
	eye := modelCam.position()
	if modelVis.valid &&
		modelVis.doc == modelDoc &&
		modelVis.rev == modelDoc.Rev() &&
		modelVis.eye == eye &&
		modelVis.wireframe == modelWireframe &&
		len(modelVis.verts) == len(modelDoc.Verts) {
		return
	}
	modelVis.valid = true
	modelVis.doc = modelDoc
	modelVis.rev = modelDoc.Rev()
	modelVis.eye = eye
	modelVis.wireframe = modelWireframe
	if cap(modelVis.verts) >= len(modelDoc.Verts) {
		modelVis.verts = modelVis.verts[:len(modelDoc.Verts)]
	} else {
		modelVis.verts = make([]bool, len(modelDoc.Verts))
	}
	// Wireframe shows everything, so the pass is pure cost there.
	if modelWireframe {
		for i := range modelVis.verts {
			modelVis.verts[i] = true
		}
		modelVis.exact = false
		return
	}

	tris := modelCollectTris()
	if len(modelDoc.Verts)*len(tris) > modelVisTriBudget {
		modelVisibilityByFacing(eye)
		return
	}
	modelVis.exact = true

	for vi, p := range modelDoc.Verts {
		modelVis.verts[vi] = !modelPointOccluded(eye, p, vi, tris)
	}
}

// modelPointOccluded reports whether anything sits between the eye and p.
//
// Triangles that use the vertex itself are skipped: they meet the ray exactly
// at its far end, and floating-point noise there would have every vertex
// occlude itself.
func modelPointOccluded(eye, p mgl32.Vec3, vi int, tris []modelTri) bool {
	seg := p.Sub(eye)
	dist := seg.Len()
	if dist < 1e-6 {
		return false
	}
	dir := seg.Mul(1 / dist)

	// A surface grazing the ray just behind the vertex must not count, or a
	// vertex would be hidden by the very face it sits on.
	const slack = 1e-3
	limit := dist - slack
	for i := range tris {
		t := &tris[i]
		if t.verts[0] == vi || t.verts[1] == vi || t.verts[2] == vi {
			continue
		}
		hit, dt, _ := rayTriangleIntersect(eye, dir, t.a, t.b, t.c)
		if hit && dt > slack && dt < limit {
			return true
		}
	}
	return false
}

// modelVisibilityByFacing is the fallback for meshes too dense for the exact
// test: a vertex counts as visible when any face touching it is turned towards
// the camera. Exact on a convex solid, optimistic on everything else — it can
// offer a handle that is really hidden behind another part of the model, which
// is the trade the budget buys.
func modelVisibilityByFacing(eye mgl32.Vec3) {
	modelVis.exact = false
	for vi := range modelDoc.Verts {
		faces := modelDoc.FacesAtVertex(vi)
		if len(faces) == 0 {
			// A loose vertex belongs to no surface, so nothing of its own can
			// hide it.
			modelVis.verts[vi] = true
			continue
		}
		visible := false
		for _, fi := range faces {
			toEye := eye.Sub(modelDoc.FaceCenter(fi))
			if modelDoc.FaceNormal(fi).Dot(toEye) > 0 {
				visible = true
				break
			}
		}
		modelVis.verts[vi] = visible
	}
}
