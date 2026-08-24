package main

import (
	"foundry/internal/gfx"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// gizmoSegments drains the queued gizmo lines into (start, end, colour)
// triples. GizmoLine packs each end as position + colour, so one segment is 12
// floats.
func gizmoSegments() [][3]mgl32.Vec3 {
	var out [][3]mgl32.Vec3
	for i := 0; i+11 < len(gizmoBuf); i += 12 {
		out = append(out, [3]mgl32.Vec3{
			{gizmoBuf[i+0], gizmoBuf[i+1], gizmoBuf[i+2]},
			{gizmoBuf[i+6], gizmoBuf[i+7], gizmoBuf[i+8]},
			{gizmoBuf[i+3], gizmoBuf[i+4], gizmoBuf[i+5]},
		})
	}
	gizmoBuf = gizmoBuf[:0]
	return out
}

// outlineLevel is a 4x4 square sector with a floor at y=0, a ceiling at y=3 and
// one wall running along its first edge.
func outlineLevel() *Level {
	return &Level{Chunks: []Chunk{{
		ID:     "c0",
		Points: []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
		Sectors: []Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        Floor{Enabled: true, Height: 0},
			Ceiling:      Ceiling{Enabled: true, Height: 3},
			Walls: []Wall{{
				Visible:    true,
				StartPoint: mgl32.Vec2{0, 0},
				EndPoint:   mgl32.Vec2{4, 0},
			}},
		}},
	}}}
}

// outlineOf selects one face of outlineLevel and returns what the outline pass
// queued for it.
func outlineOf(surface string, edgeIdx int) [][3]mgl32.Vec3 {
	Editor.ActiveChunkIdx = 0
	Editor.ActiveBiomeSectorIdx = -1
	setFaceSelection([]faceSel{{chunkIdx: 0, sectorIdx: 0, kind: surface, idx: edgeIdx}})
	gizmoBuf = gizmoBuf[:0]
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

// A selected floor gets its polygon outlined at the floor's own heights, in the
// model editor's face colour, with a stub pointing the way it faces.
func TestSelectedFloorIsOutlined(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	segs := outlineOf("floor", -1)
	if len(segs) != 5 {
		t.Fatalf("queued %d segments, want 4 polygon edges + 1 normal stub", len(segs))
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

	stub := stubFrom(t, segs, mgl32.Vec3{2, 0, 2})
	if stub.Normalize() != (mgl32.Vec3{0, 1, 0}) {
		t.Errorf("floor stub points %v, want straight up", stub.Normalize())
	}
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

// A ceiling faces the other way, and getting that backwards would point every
// extrusion into the room above.
func TestSelectedCeilingStubPointsDown(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	segs := outlineOf("ceiling", -1)
	if !segmentBetween(segs, mgl32.Vec3{0, 3, 0}, mgl32.Vec3{4, 3, 0}) {
		t.Error("ceiling outlined at the wrong height")
	}
	stub := stubFrom(t, segs, mgl32.Vec3{2, 3, 2})
	if stub.Normalize() != (mgl32.Vec3{0, -1, 0}) {
		t.Errorf("ceiling stub points %v, want straight down", stub.Normalize())
	}
}

// A wall is outlined as the upright quad it occupies, floor to wall top at each
// end, with the outward normal WallFacingCamera reads it by.
func TestSelectedWallIsOutlined(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	segs := outlineOf("wall", 0)
	if len(segs) != 5 {
		t.Fatalf("queued %d segments, want 4 quad edges + 1 normal stub", len(segs))
	}
	corners := []mgl32.Vec3{{0, 0, 0}, {4, 0, 0}, {4, 3, 0}, {0, 3, 0}}
	for i, a := range corners {
		b := corners[(i+1)%len(corners)]
		if !segmentBetween(segs, a, b) {
			t.Errorf("no outline segment between %v and %v", a, b)
		}
	}
	stub := stubFrom(t, segs, mgl32.Vec3{2, 1.5, 0})
	if !nearlyVec(stub.Normalize(), mgl32.Vec3{0, 0, 1}, 1e-5) {
		t.Errorf("wall stub points %v, want the left-hand normal {0 0 1}", stub.Normalize())
	}
}

// The stub is sized in pixels, not world units, so it stays readable whether
// the camera is across the level or up against the surface.
func TestFaceNormalStubIsPixelSized(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	near := stubFrom(t, outlineOf("floor", -1), mgl32.Vec3{2, 0, 2}).Len()

	gfx.Cam.Position = mgl32.Vec3{2, 0, 60}
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, mgl32.Vec3{2, 0, 2}, mgl32.Vec3{0, 1, 0})
	far := stubFrom(t, outlineOf("floor", -1), mgl32.Vec3{2, 0, 2}).Len()

	if far <= near {
		t.Errorf("stub is %v up close and %v far away; it should grow with distance "+
			"to hold its on-screen length", near, far)
	}
}

// Nothing selected must queue nothing: an outline left over from a cleared
// selection is worse than none, because it points at geometry an edit will not
// touch.
func TestNoSelectionOutlinesNothing(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()

	if segs := outlineOf("", -1); len(segs) != 0 {
		t.Errorf("no surface selected queued %d segments", len(segs))
	}

	Editor.ActiveSurface = "wall"
	Editor.ActiveEdgeIdx = 7 // past the end of s0's walls
	gizmoBuf = gizmoBuf[:0]
	drawSelectedFaceOutline()
	if segs := gizmoSegments(); len(segs) != 0 {
		t.Errorf("an out-of-range wall index queued %d segments", len(segs))
	}
}

// Water is picked in face mode too, so it gets the same outline — drawn at the
// height its plane actually sits at rather than the flat polygon's.
func TestSelectedWaterIsOutlinedAtThePlaneHeight(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = outlineLevel()
	ch := &Editor.level.Chunks[0]
	ch.BiomeSectors = []BiomeSector{{
		ID:     "lake",
		Points: []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
		Brush:  BiomeBrush{Kind: BiomeWater},
	}}
	ch.WaterSurfaces = []WaterSurface{{ID: "lake", Position: mgl32.Vec3{2, -0.6, 2}}}

	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveSurface = ""
	Editor.ActiveBiomeSectorIdx = 0
	gizmoBuf = gizmoBuf[:0]
	drawSelectedFaceOutline()
	segs := gizmoSegments()

	if len(segs) != 5 {
		t.Fatalf("queued %d segments, want 4 polygon edges + 1 normal stub", len(segs))
	}
	if !segmentBetween(segs, mgl32.Vec3{0, -0.6, 0}, mgl32.Vec3{4, -0.6, 0}) {
		t.Error("water outlined at the polygon's height rather than the plane's")
	}

	// A sector painted but not yet synced into a plane has no height to draw at.
	ch.WaterSurfaces = nil
	gizmoBuf = gizmoBuf[:0]
	drawSelectedFaceOutline()
	if segs := gizmoSegments(); len(segs) != 0 {
		t.Errorf("a water sector with no plane yet queued %d segments", len(segs))
	}
}
