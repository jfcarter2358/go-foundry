package editor

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// gizmoSegments drains the queued gizmo lines into (start, end, colour)
// triples. GizmoLine packs each end as position + colour, so one segment is 12
// floats.
func gizmoSegments() [][3]mgl32.Vec3 {
	var out [][3]mgl32.Vec3
	for i := 0; i+11 < len(gizmo.Buf); i += 12 {
		out = append(out, [3]mgl32.Vec3{
			{gizmo.Buf[i+0], gizmo.Buf[i+1], gizmo.Buf[i+2]},
			{gizmo.Buf[i+6], gizmo.Buf[i+7], gizmo.Buf[i+8]},
			{gizmo.Buf[i+3], gizmo.Buf[i+4], gizmo.Buf[i+5]},
		})
	}
	gizmo.Buf = gizmo.Buf[:0]
	return out
}

// outlineLevel is one 4x4x3 box brush with its lower-left-near corner at the
// origin, so a face's corners are round numbers.
func outlineLevel() *world.Level {
	return &world.Level{Chunks: []world.Chunk{{
		ID: "c0",
		Brushes: []world.Brush{
			world.NewBoxBrush("b0", mgl32.Vec3{2, 1.5, 2}, mgl32.Vec3{2, 1.5, 2}, "concrete"),
		},
	}}}
}

// outlineOf selects one face of outlineLevel and returns what the outline pass
// queued for it.
func outlineOf(faceIdx int) [][3]mgl32.Vec3 {
	Editor.ActiveChunkIdx = 0
	setFaceSelection([]faceSel{brushFaceSel(0, 0, faceIdx)})
	gizmo.Buf = gizmo.Buf[:0]
	drawSelectedFaceOutline()
	return gizmoSegments()
}

// segmentBetween reports whether any queued segment joins a and b, either way
// round — the outline is a closed loop, so winding is not the point.
func segmentBetween(segs [][3]mgl32.Vec3, a, b mgl32.Vec3) bool {
	for _, s := range segs {
		if (nearlyVec(s[0], a, 1e-4) && nearlyVec(s[1], b, 1e-4)) ||
			(nearlyVec(s[0], b, 1e-4) && nearlyVec(s[1], a, 1e-4)) {
			return true
		}
	}
	return false
}

// stubFrom finds the one segment starting at the face centre — the normal stub
// — and returns its direction and length as a vector.
func stubFrom(t *testing.T, segs [][3]mgl32.Vec3, center mgl32.Vec3) mgl32.Vec3 {
	t.Helper()
	for _, s := range segs {
		if nearlyVec(s[0], center, 1e-4) {
			return s[1].Sub(s[0])
		}
	}
	t.Fatalf("no normal stub starting at the face centre %v", center)
	return mgl32.Vec3{}
}

// A selected face gets its corner loop outlined in the model editor's face
// colour, with a stub pointing the way it faces.
func TestSelectedFaceIsOutlined(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	segs := outlineOf(boxFaceBottom)
	if len(segs) != 5 {
		t.Fatalf("queued %d segments, want 4 face edges + 1 normal stub", len(segs))
	}
	corners := []mgl32.Vec3{{0, 0, 0}, {4, 0, 0}, {4, 0, 4}, {0, 0, 4}}
	for i, a := range corners {
		b := corners[(i+1)%len(corners)]
		if !segmentBetween(segs, a, b) {
			t.Errorf("no outline segment between %v and %v", a, b)
		}
	}
	for _, s := range segs {
		if s[2] != modelFaceSelColor {
			t.Fatalf("segment %v drawn in %v, want the model editor's face colour %v",
				s[0], s[2], modelFaceSelColor)
		}
	}
}

// Which way "out" is decides which way an extrude goes, so the stub has to
// follow the face's own winding — and a box's top and bottom face opposite
// ways.
func TestFaceStubsFollowTheOutwardNormal(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	for _, tc := range []struct {
		name    string
		faceIdx int
		center  mgl32.Vec3
		want    mgl32.Vec3
	}{
		{"bottom", boxFaceBottom, mgl32.Vec3{2, 0, 2}, mgl32.Vec3{0, -1, 0}},
		{"top", boxFaceTop, mgl32.Vec3{2, 3, 2}, mgl32.Vec3{0, 1, 0}},
		{"-Z side", boxFaceNegZ, mgl32.Vec3{2, 1.5, 0}, mgl32.Vec3{0, 0, -1}},
		{"+X side", boxFacePosX, mgl32.Vec3{4, 1.5, 2}, mgl32.Vec3{1, 0, 0}},
	} {
		stub := stubFrom(t, outlineOf(tc.faceIdx), tc.center)
		if !nearlyVec(stub.Normalize(), tc.want, 1e-5) {
			t.Errorf("%s stub points %v, want %v", tc.name, stub.Normalize(), tc.want)
		}
	}
}

// A secondary selection is dimmed. The primary is not merely first in a list —
// it is the face the side panel edits — so a dozen identically lit faces would
// say nothing about which one the panel is describing.
func TestSecondarySelectionIsDimmed(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()
	Editor.ActiveChunkIdx = 0

	setFaceSelection([]faceSel{
		brushFaceSel(0, 0, boxFaceBottom),
		brushFaceSel(0, 0, boxFaceTop),
	})
	gizmo.Buf = gizmo.Buf[:0]
	drawSelectedFaceOutline()
	segs := gizmoSegments()

	if len(segs) != 10 {
		t.Fatalf("queued %d segments, want two faces' 5 each", len(segs))
	}
	primary, secondary := 0, 0
	for _, s := range segs {
		switch s[2] {
		case modelFaceSelColor:
			primary++
		case editorFaceSelSecondaryColor:
			secondary++
		default:
			t.Fatalf("segment drawn in an unexpected colour %v", s[2])
		}
	}
	if primary != 5 || secondary != 5 {
		t.Errorf("got %d primary and %d secondary segments, want 5 of each", primary, secondary)
	}
}

// The stub is sized in pixels, not world units, so it stays readable whether
// the camera is across the level or up against the surface.
func TestFaceNormalStubIsPixelSized(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	near := stubFrom(t, outlineOf(boxFaceBottom), mgl32.Vec3{2, 0, 2}).Len()

	gfx.Cam.Position = mgl32.Vec3{2, 0, 60}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{2, 0, 2}, mgl32.Vec3{0, 1, 0})
	far := stubFrom(t, outlineOf(boxFaceBottom), mgl32.Vec3{2, 0, 2}).Len()

	if far <= near {
		t.Errorf("stub is %v up close and %v far away; it should grow with distance "+
			"to hold its on-screen length", near, far)
	}
}

// Nothing selected must queue nothing: an outline left over from a cleared
// selection is worse than none, because it points at geometry an edit will not
// touch. Nor must a stale index — undo can shorten a brush's face list.
func TestNoSelectionOutlinesNothing(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()
	Editor.ActiveChunkIdx = 0

	clearFaceSelection()
	gizmo.Buf = gizmo.Buf[:0]
	drawSelectedFaceOutline()
	if segs := gizmoSegments(); len(segs) != 0 {
		t.Errorf("no face selected queued %d segments", len(segs))
	}

	// Past the end of b0's faces. Assigned directly rather than through
	// setFaceSelection, which prunes what does not resolve — the point here is
	// what the draw pass does when a stale entry reaches it anyway.
	Editor.SelectedFaces = []faceSel{brushFaceSel(0, 0, 99)}
	gizmo.Buf = gizmo.Buf[:0]
	drawSelectedFaceOutline()
	if segs := gizmoSegments(); len(segs) != 0 {
		t.Errorf("an out-of-range face index queued %d segments", len(segs))
	}
}
