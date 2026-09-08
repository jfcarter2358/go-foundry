package editor

// editor_3d_face_select.go — what a selected face looks like.
//
// Face mode's click used to leave no mark: the side panel opened on whatever it
// landed on and the viewport said nothing, so which of two coincident faces you
// actually got — this brush's top or the one stacked above it — was a guess
// until you dragged something.
//
// This is the model editor's answer (drawModelSelection, editor_model_pick.go)
// drawn against the level editor's geometry: the face's boundary outlined, plus
// a short stub along its normal, because which way "out" is decides which way
// an extrusion goes.
//
// The colour is modelFaceSelColor itself rather than a copy of its value. The
// same feedback in two viewports should not be two slightly different oranges
// — the same rule the translate gizmo already follows by reusing the 3D view's
// arm colours instead of defining its own.

import (
	"foundry/internal/gizmo"

	"github.com/go-gl/mathgl/mgl32"
)

// editorFaceNormalStubPx is the on-screen length of the normal stub.
//
// Pixel-scaled where the model editor's is a fixed 0.25 world units, because
// the two editors work at different scales: a model spans a few units and a
// level spans hundreds, so one fixed length is either invisible across a room
// or enormous with your nose against a wall.
const editorFaceNormalStubPx = float32(34)

// editorFaceSelSecondaryColor is what a selected face that is not the primary
// is outlined in: the same hue, dimmed.
//
// The distinction is worth drawing because the primary is not merely first in a
// list — it is the face the side panel edits, and the one a toggle takes its
// "before" value from. A dozen identically lit walls would say nothing about
// which of them the panel in the corner is describing.
var editorFaceSelSecondaryColor = modelFaceSelColor.Mul(0.55)

// drawSelectedFaceOutline queues the outline of every selected face.
//
// Caller has already turned depth testing off, so the outline shows through the
// geometry in front of it — the same terms the vertex and edge handles are
// drawn on, and for the same reason: it is feedback about what an edit will
// touch, not something that has to be clickable where it appears.
func drawSelectedFaceOutline() {
	if Editor.level == nil {
		return
	}
	for i, sel := range Editor.SelectedFaces {
		col := modelFaceSelColor
		if i > 0 {
			col = editorFaceSelSecondaryColor
		}
		editorOutlineFace(sel, col)
	}
}

// editorOutlineFace outlines one selected brush face: its corner loop, closed,
// plus a stub along its outward normal.
//
// A sector needed six branches here, one per kind of face, because each was a
// different shape derived a different way — a floor from the polygon at its
// per-vertex heights, a wall from an upright quad between two of them, a slab
// from a surface offset by its thickness. A brush face is a loop of corners, so
// there is one path and it is the honest one: draw the corners.
func editorOutlineFace(sel faceSel, col mgl32.Vec3) {
	ch, b := faceSelBrush(sel)
	f := faceSelFace(sel)
	if ch == nil || b == nil || f == nil {
		return
	}
	pts := b.FacePoints(f)
	n := len(pts)
	if n < 2 {
		return
	}
	var sum mgl32.Vec3
	for i := 0; i < n; i++ {
		a := ch.LocalToWorld(pts[i])
		gizmo.GizmoLine(a, ch.LocalToWorld(pts[(i+1)%n]), col)
		sum = sum.Add(a)
	}
	// The normal is computed in the brush's own space, like the face's plane
	// is, then carried out — LocalToWorldDirection is what makes a rotated or
	// scaled chunk's faces point the right way.
	normal := ch.LocalToWorldDirection(b.FaceNormal(f))
	editorFaceNormalStub(sum.Mul(1/float32(n)), normal, col)
}

// editorFaceNormalStub draws the short line out of a face's middle that says
// which side is the front.
func editorFaceNormalStub(center, normal, col mgl32.Vec3) {
	if normal.Len() < 1e-6 {
		return
	}
	length := worldSizeForPixels(center, editorFaceNormalStubPx)
	gizmo.GizmoLine(center, center.Add(normal.Normalize().Mul(length)), col)
}
