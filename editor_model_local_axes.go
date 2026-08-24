package main

// editor_model_local_axes.go — the selection's own frame, and the switch that
// points the scale gizmo at it.
//
// Global axes are the right default: they are the frame the grid, the snapping
// and every coordinate readout are in. They are also useless on anything built
// at an angle — a face on a roof pitch has no world axis running across it, so
// "make this a bit wider" cannot be expressed as an X, Y or Z scale at all.
// Pressing C again, once the scale tool is already up, swaps the arms onto a
// frame derived from what is selected, and pressing it again swaps back.

import "github.com/go-gl/mathgl/mgl32"

// modelScaleLocal is the switch itself: false is the world frame, true the
// selection's own. It is a mode rather than per-selection state, because it
// answers a question about how the user wants to work, not about one face.
var modelScaleLocal bool

// modelLocalBasis is the selection's own orthonormal frame, or false when the
// selection does not imply one.
//
// The three cases are the three things a selection can have a direction from:
//   - faces: the surface. Third axis is the normal, the other two lie in the
//     plane, so arm 2 is "thickness" and arms 0/1 are "across the surface".
//   - edges: the edge itself, as arm 0. The other two are an arbitrary pair
//     perpendicular to it — there is nothing else to derive them from, and the
//     one the user is aiming at is the edge.
//   - bones: the bone's own direction as arm 0, which is what "longer" means
//     for a limb.
//
// A vertex selection has no direction at all — a point is a point — so it has
// no local frame and keeps the world one.
func modelLocalBasis() ([3]mgl32.Vec3, bool) {
	if modelDoc == nil {
		return [3]mgl32.Vec3{}, false
	}
	switch modelSelection.Mode {
	case modelSelFace:
		return modelFaceBasis(modelSelection.Faces)
	case modelSelEdge:
		return modelEdgeBasis(modelSelection.Edges)
	case modelSelBone:
		return modelBoneBasis(modelSelection.Bones)
	}
	return [3]mgl32.Vec3{}, false
}

// modelFaceBasis builds a frame from a face selection: the averaged normal,
// and an in-plane pair taken from the first face's first edge, so the arms line
// up with the geometry rather than sitting at some arbitrary roll around it.
func modelFaceBasis(faces []int) ([3]mgl32.Vec3, bool) {
	n, ok := modelFacesNormal(faces)
	if !ok {
		return [3]mgl32.Vec3{}, false
	}
	var t mgl32.Vec3
	if len(faces) > 0 && faces[0] >= 0 && faces[0] < len(modelDoc.Faces) {
		f := &modelDoc.Faces[faces[0]]
		if len(f.Verts) >= 2 {
			// Gram-Schmidt: the edge, less whatever part of it points along
			// the normal. On a planar face that part is zero; on one that is
			// slightly bowed it is what keeps the frame orthonormal.
			e := modelDoc.Verts[f.Verts[1]].Sub(modelDoc.Verts[f.Verts[0]])
			t = e.Sub(n.Mul(e.Dot(n)))
		}
	}
	return basisAround(n, t, 2), true
}

// modelEdgeBasis builds a frame from the first selected edge.
func modelEdgeBasis(edges []edgeKey) ([3]mgl32.Vec3, bool) {
	for _, e := range edges {
		if e.A < 0 || e.A >= len(modelDoc.Verts) || e.B < 0 || e.B >= len(modelDoc.Verts) {
			continue
		}
		d := modelDoc.Verts[e.B].Sub(modelDoc.Verts[e.A])
		if d.Len() < 1e-6 {
			continue
		}
		return basisAround(d.Normalize(), mgl32.Vec3{}, 0), true
	}
	return [3]mgl32.Vec3{}, false
}

// modelBoneBasis builds a frame from the first selected bone's direction.
func modelBoneBasis(bones []int) ([3]mgl32.Vec3, bool) {
	for _, bi := range bones {
		if bi < 0 || bi >= len(modelDoc.Bones) {
			continue
		}
		d := modelDoc.Bones[bi].Tail.Sub(modelDoc.Bones[bi].Head)
		if d.Len() < 1e-6 {
			continue
		}
		return basisAround(d.Normalize(), mgl32.Vec3{}, 0), true
	}
	return [3]mgl32.Vec3{}, false
}

// basisAround completes an orthonormal frame around the unit vector `main`,
// which becomes axis `slot`. `hint` is a preferred direction for the axis after
// it, used when the selection has one — an edge of the face, say — and ignored
// when it is degenerate or parallel to main.
func basisAround(main, hint mgl32.Vec3, slot int) [3]mgl32.Vec3 {
	var u, v mgl32.Vec3
	if hint.Len() > 1e-6 {
		u = hint.Normalize()
		v = main.Cross(u).Normalize()
	} else {
		u, v = modelRingBasis(main)
	}
	var out [3]mgl32.Vec3
	switch slot {
	case 0:
		out = [3]mgl32.Vec3{main, u, v}
	case 1:
		out = [3]mgl32.Vec3{v, main, u}
	default:
		out = [3]mgl32.Vec3{u, v, main}
	}
	return out
}

// worldBasis is the frame the gizmo uses when it is not using a local one.
func worldBasis() [3]mgl32.Vec3 {
	return [3]mgl32.Vec3{gizmoAxes[0], gizmoAxes[1], gizmoAxes[2]}
}

// modelScaleBasis is the frame the scale gizmo is working in right now, and
// whether it is the selection's own. Falls back to the world frame whenever the
// selection has no direction of its own, so the switch can be left on without
// the tool going missing on a vertex selection.
func modelScaleBasis() ([3]mgl32.Vec3, bool) {
	// Pose mode forces the bone's own frame, whatever the local-axes switch
	// says: a pose scale channel *is* expressed in bone space, so the Y arm has
	// to be the one along the bone. World axes would give three handles whose
	// meaning changed with whichever way the limb happened to be pointing.
	if b, ok := modelPoseScaleAxes(); ok {
		return b, true
	}
	if !modelScaleLocal {
		return worldBasis(), false
	}
	if b, ok := modelLocalBasis(); ok {
		return b, true
	}
	return worldBasis(), false
}

// modelScaleDragBasis is the frame a scale drag in progress is using — frozen
// at mouse-down, for the same reason StartOrigin is: the drag moves the very
// vertices the local frame is derived from, and re-deriving it mid-drag would
// let the axes creep along with the geometry they are supposed to be measuring.
func modelScaleDragBasis() [3]mgl32.Vec3 {
	if modelGizmo.Dragging && modelGizmo.Tool == modelToolScale {
		return modelGizmo.StartBasis
	}
	b, _ := modelScaleBasis()
	return b
}

// modelSetScaleLocal switches frames and says so — on an axis-aligned selection
// the two look identical, and without the message the keypress would look like
// it had done nothing.
func modelSetScaleLocal(on bool) {
	modelScaleLocal = on
	if !on {
		modelSetStatus("Scaling along the world axes")
		return
	}
	if _, ok := modelLocalBasis(); !ok {
		modelSetStatus("Scaling along the local axes — this selection has none, so the world axes stand in")
		return
	}
	modelSetStatus("Scaling along the selection's own axes")
}

// modelScaleToolKey is what C does: raise the scale tool, or — when it is
// already up — swap it between the world frame and the selection's own.
func modelScaleToolKey() {
	if modelGizmoTool != modelToolScale {
		modelSetGizmoTool(modelToolScale)
		return
	}
	modelSetScaleLocal(!modelScaleLocal)
}

// modelScaleLocalLabel marks the frame on the toolbar's scale button, since the
// switch is otherwise invisible on anything that happens to be axis-aligned.
func modelScaleLocalLabel() string {
	if modelScaleLocal {
		return "Local"
	}
	return "World"
}
