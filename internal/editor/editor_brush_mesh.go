package editor

// editor_brush_mesh.go — reshaping a brush's own mesh.
//
// A sector's face was flat by construction, so shaping one meant editing a
// parametric SurfaceMesh riding on top of it: a second geometry model, in
// (u, v, depth) coordinates relative to the face it hung off, with its own
// selection unit and its own editing sub-mode. A brush has no base surface to
// hang anything off — its verts are free 3D points — and it does not need one,
// because a brush *is* a mesh. The ordinary point, edge and face selection
// modes already address exactly what these operations act on.
//
// So the shaping sub-mode is gone and what is left is three operations on the
// current face selection, each bound to the key the model editor binds it to:
//
//	E  extrude — move the face along its own normal, growing the solid
//	I  inset   — shrink the face within its plane, drawing the sides in after it
//	B  subdivide — split the face at its edge midpoints, adding no volume
//
// # Why these three and not the model editor's whole vocabulary
//
// A brush must stay convex (world/brush.go), and that rules out most of what
// makes sense on a model. Each of these three preserves convexity outright:
//
//   - Extruding a face along its own normal slides one supporting plane
//     outward or inward. Every other plane is untouched and the face stays
//     within its own, so the intersection of half-spaces is still a convex
//     solid. (Extruding along anything *else* — a bump on a wall — does not,
//     which is why there is no free-direction extrude here. A bump is a second
//     brush, the way it is in every id-lineage editor.)
//   - Insetting draws a face's corners toward its centre in its own plane,
//     turning the solid into a frustum of itself. Still convex.
//   - Subdividing changes the face list, not the point set, so the hull is
//     exactly what it was.
//
// A free vertex drag is the one edit that can break convexity, and it is
// checked rather than refused — see warnIfSelectionBrokeConvexity.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	// brushExtrudeStep is how far one press of E moves a face when there is no
	// grid to snap to. A quarter unit — small enough to shape with, large
	// enough to see.
	brushExtrudeStep float32 = 0.25

	// brushInsetFraction is how far one press of I pulls a face's corners
	// toward its centre, as a fraction of the distance to that centre. A fifth
	// per press, so four presses still leave a face rather than collapsing it
	// to a point.
	brushInsetFraction float32 = 0.2

	// brushMinFaceExtent is how small a face is allowed to get. Below this an
	// inset would collapse the face into a degenerate loop, whose normal is
	// undefined and whose triangulation is empty.
	brushMinFaceExtent float32 = 1e-3
)

// brushMeshHandleKey runs the mesh operations bound to E, I and B.
//
// Reported as consumed only when an operation actually ran, so the keys stay
// available to whatever else wants them when no face is selected. Caller has
// already checked that ImGui does not want the keyboard.
func brushMeshHandleKey(sc sdl.Scancode, shift bool) bool {
	if !ws().editing() || len(ws().SelectedFaces) == 0 {
		return false
	}
	switch sc {
	case sdl.SCANCODE_E:
		// Shift reverses it, the way shift reverses the brush ghost's rotate
		// and resize keys: pulling a face back in is as common as pushing it
		// out, and a modifier is cheaper than a second binding.
		d := brushExtrudeDefault()
		if shift {
			d = -d
		}
		brushExtrudeSelectedFaces(d)
		return true
	case sdl.SCANCODE_I:
		f := brushInsetFraction
		if shift {
			f = -f
		}
		brushInsetSelectedFaces(f)
		return true
	case sdl.SCANCODE_B:
		brushSubdivideSelectedFaces()
		return true
	}
	return false
}

// brushExtrudeDefault is how far one extrude moves a face: the grid quantum
// when snapping is on, so a shaped brush stays on the grid the rest of the
// level is built to, and a fixed step otherwise.
func brushExtrudeDefault() float32 {
	if ws().SnapToGrid && ws().StepSize > 0 {
		return ws().StepSize
	}
	return brushExtrudeStep
}

// ---------- extrude ----------

// brushExtrudeSelectedFaces moves every selected face dist along its own
// outward normal. Positive grows the solid; negative pulls the face in.
//
// The corners are moved rather than duplicated: a brush face slides its
// supporting plane, it does not spawn a new one. Duplicating would be the
// model editor's extrude — the one that leaves a ring of side faces behind —
// and on a convex solid that ring is exactly what would make it concave.
//
// Faces are handled one at a time and each is re-read first, because two
// selected faces of one brush share corners: extruding a box's top and its
// side both move the two verts they have in common, and the second operation
// has to see what the first did.
func brushExtrudeSelectedFaces(dist float32) {
	if len(ws().SelectedFaces) == 0 || dist == 0 {
		return
	}
	ws().pushUndo()
	n := 0
	for _, sel := range ws().SelectedFaces {
		if sel.detail {
			// Edit mode's tools are the convexity-preserving ones and they act
			// on the SOLID. A detail face has its own, in detail mode; running
			// these on one would slide a plane the hull does not have.
			continue
		}
		_, b := faceSelBrush(sel)
		f := faceSelFace(sel)
		if b == nil || f == nil {
			continue
		}
		offset := b.FaceNormal(f).Mul(dist)
		if offset.Len() < 1e-9 {
			continue
		}
		for _, vi := range f.Indices {
			if vi >= 0 && vi < len(b.Verts) {
				b.Verts[vi] = b.Verts[vi].Add(offset)
			}
		}
		n++
	}
	rebuildSelectedFaceBrushes()
	warnIfSelectionBrokeConvexity()
	ws().setStatus("Extruded %d face(s) by %.3f", n, dist)
}

// ---------- inset ----------

// brushInsetSelectedFaces pulls every selected face's corners toward that
// face's centre by the given fraction of their distance to it, leaving the
// face in its own plane. A negative fraction pushes them out instead.
//
// The side faces follow for free: they are quads on the corners this moved, so
// they simply slope. That is what turns a box into a frustum, and it is why
// inset is the first half of "taper this pillar" rather than of "cut a hole" —
// a hole in a convex solid is not a thing a convex solid can have.
//
// A face whose corners would end up closer together than brushMinFaceExtent is
// left alone rather than collapsed: a degenerate loop has no normal, so the
// next operation on it would have nothing to work from.
func brushInsetSelectedFaces(fraction float32) {
	if len(ws().SelectedFaces) == 0 || fraction == 0 {
		return
	}
	ws().pushUndo()
	n, skipped := 0, 0
	for _, sel := range ws().SelectedFaces {
		if sel.detail {
			continue // the solid's tools, on the solid's faces only
		}
		_, b := faceSelBrush(sel)
		f := faceSelFace(sel)
		if b == nil || f == nil || len(f.Indices) < 3 {
			continue
		}
		center, ok := faceCenter(b, f)
		if !ok {
			continue
		}
		if fraction > 0 && faceMaxRadius(b, f, center)*(1-fraction) < brushMinFaceExtent {
			skipped++
			continue
		}
		for _, vi := range f.Indices {
			if vi < 0 || vi >= len(b.Verts) {
				continue
			}
			v := b.Verts[vi]
			b.Verts[vi] = v.Sub(v.Sub(center).Mul(fraction))
		}
		n++
	}
	rebuildSelectedFaceBrushes()
	warnIfSelectionBrokeConvexity()
	if skipped > 0 {
		ws().setStatus("Inset %d face(s); %d too small to inset further", n, skipped)
		return
	}
	ws().setStatus("Inset %d face(s)", n)
}

// faceCenter is the average of a face's corners — its centroid for a regular
// loop, and close enough to one for any convex n-gon that inset reads as
// even. ok is false for a face with no resolvable corners.
func faceCenter(b *world.Brush, f *world.BrushFace) (mgl32.Vec3, bool) {
	var sum mgl32.Vec3
	n := 0
	for _, vi := range f.Indices {
		if vi >= 0 && vi < len(b.Verts) {
			sum = sum.Add(b.Verts[vi])
			n++
		}
	}
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	return sum.Mul(1 / float32(n)), true
}

// faceMaxRadius is how far the furthest corner of a face is from center — the
// measure inset checks before it shrinks one.
func faceMaxRadius(b *world.Brush, f *world.BrushFace, center mgl32.Vec3) float32 {
	best := float32(0)
	for _, vi := range f.Indices {
		if vi < 0 || vi >= len(b.Verts) {
			continue
		}
		if d := b.Verts[vi].Sub(center).Len(); d > best {
			best = d
		}
	}
	return best
}

// ---------- subdivide ----------

// brushSubdivideSelectedFaces splits every selected face into one quad per
// corner: a new vertex at each edge's midpoint, one at the face's centre, and
// four-sided pieces between them.
//
// No volume changes: every new vertex lies in the face's own plane, on the hull
// it was already on, so the solid is exactly the solid it was. What changes is
// how finely the face can be shaped afterwards — a subdivided face's corners
// can be dragged individually, and *that* is what breaks convexity, which is
// why the warning belongs on the drag rather than here.
//
// The pieces are QUADRANTS, not a fan of triangles to the centre. A fan is
// cheaper to write — it needs no cooperation from the neighbouring faces — but
// what it produces is a face crossed by diagonals, which is not what anyone
// means by "subdivide this wall": you cannot select the top-left quarter of it
// afterwards, and a dragged corner pulls a triangle rather than a panel. The
// midpoint split is the model editor's, and it is the same code
// (editor_detail_mesh.go), so a face splits the same way whichever mesh it
// belongs to.
//
// The T-junction the fan was avoiding is handled rather than dodged: splitting
// a face puts a vertex on an edge it SHARES with its neighbour, and
// model.SubdivideFaces inserts that vertex into the neighbour's own loop as it
// goes. The neighbour keeps its shape and gains a corner, which is what stops
// the seam between them opening up under interpolated light.
func brushSubdivideSelectedFaces() {
	if len(ws().SelectedFaces) == 0 {
		return
	}
	ws().pushUndo()

	// Grouped per brush: the split renumbers a brush's faces, so each brush is
	// rewritten in one pass and the selection is rebuilt from what came back.
	perBrush := map[[2]int][]int{}
	order := [][2]int{}
	for _, sel := range ws().SelectedFaces {
		if sel.detail || faceSelFace(sel) == nil {
			continue // the solid's tools, on the solid's faces only
		}
		key := [2]int{sel.chunkIdx, sel.brushIdx}
		if _, seen := perBrush[key]; !seen {
			order = append(order, key)
		}
		perBrush[key] = append(perBrush[key], sel.faceIdx)
	}

	var newSels []faceSel
	n := 0
	for _, key := range order {
		_, b := faceSelBrush(faceSel{chunkIdx: key[0], brushIdx: key[1]})
		if b == nil {
			continue
		}
		// The brush's own mesh, through the shared operation. Every new vertex
		// lands on an edge or in the plane of the face it came from, both of
		// which are already on the hull, so the solid is exactly the solid it
		// was and convexity cannot break here. What changes is how finely the
		// face can be shaped afterwards — and dragging one of those new corners
		// IS what breaks convexity, which is why the warning belongs there.
		pieces := meshSubdivideFaces(&b.BrushMesh, perBrush[key])
		if len(pieces) == 0 {
			continue
		}
		n += len(perBrush[key])
		for _, fi := range pieces {
			newSels = append(newSels, brushFaceSel(key[0], key[1], fi))
		}
		_ = b.Load()
	}
	if len(newSels) > 0 {
		setFaceSelection(newSels)
	}
	ws().setStatus("Subdivided %d face(s) into %d", n, len(newSels))
}
