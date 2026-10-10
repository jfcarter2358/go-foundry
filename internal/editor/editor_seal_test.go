package editor

// The seal box in the editor: that it can be reached without getting in the
// way, and that the gizmos size it the way they size a volume.

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestPickSealRayTOnlyFromOutside(t *testing.T) {
	miss := float32(math.MaxFloat32)
	s := &world.Seal{Half: mgl32.Vec3{10, 10, 10}}

	if got := pickSealRayT(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, -1}, s); got != miss {
		t.Errorf("a ray from inside the box hit it at %v", got)
	}
	if got := pickSealRayT(mgl32.Vec3{0, 0, 30}, mgl32.Vec3{0, 0, -1}, s); math.Abs(float64(got-20)) > 1e-4 {
		t.Errorf("a ray from outside hit at %v, want 20", got)
	}
	if got := pickSealRayT(mgl32.Vec3{0, 30, 30}, mgl32.Vec3{0, 0, -1}, s); got != miss {
		t.Errorf("a ray passing above the box hit it at %v", got)
	}
	if got := pickSealRayT(mgl32.Vec3{0, 0, 30}, mgl32.Vec3{0, 0, -1}, nil); got != miss {
		t.Errorf("a level with no seal was hit at %v", got)
	}
}

// From outside, the box stands in front of the whole level. A click on anything
// in it has to get that thing; only a click on nothing gets the box.
func TestPick3DGivesTheSealOnlyAClickThatHitNothingElse(t *testing.T) {
	defer saveEditorSelection()()
	sx, sy := setupPickCamera() // at the origin, looking down -Z

	brushGeomLevel(t, world.NewBoxBrush("wall", mgl32.Vec3{0, 0, -20}, mgl32.Vec3{2, 2, 1}, "concrete"))
	Editor.level.Seal = &world.Seal{Position: mgl32.Vec3{0, 0, -20}, Half: mgl32.Vec3{5, 5, 5}}
	if hit := pick3D(sx, sy); hit.kind != pickBrushFace {
		t.Errorf("clicking the wall inside the seal hit %q", hit.kind)
	}

	Editor.level.Chunks = nil
	if hit := pick3D(sx, sy); hit.kind != pickSeal {
		t.Errorf("clicking the seal with nothing in front of it hit %q", hit.kind)
	}

	Editor.level.Seal.Position = mgl32.Vec3{}
	if hit := pick3D(sx, sy); hit.kind == pickSeal {
		t.Error("a click from inside the seal picked it")
	}
}

func TestTheSealMovesAndScalesButDoesNotRotate(t *testing.T) {
	defer saveEditorSelection()()
	lvl := selectTestLevel(t)
	lvl.Seal = &world.Seal{Position: mgl32.Vec3{1, 2, 3}, Half: mgl32.Vec3{4, 4, 4}}
	Editor.Active = true
	Editor.Mode = Mode3D

	editorSelectSeal()
	if o, ok := translateGizmoOrigin(); !ok || o != lvl.Seal.Position {
		t.Errorf("move gizmo at %v, %v; want the seal's centre", o, ok)
	}
	if o, ok := scaleGizmoOrigin(); !ok || o != lvl.Seal.Position {
		t.Errorf("scale gizmo at %v, %v; want the seal's centre", o, ok)
	}
	if _, ok := rotateGizmoOrigin(); ok {
		t.Error("the seal has a rotate gizmo; the compiler's slabs are axis-aligned")
	}

	if !captureGizmoDragStart() {
		t.Fatal("a drag on the seal did not start")
	}
	applyScaleToSelection(lvl.Seal.Position, mgl32.Vec3{2, 1, 0})
	if want := (mgl32.Vec3{8, 4, world.SealMinHalf}); lvl.Seal.Half != want {
		t.Errorf("half-extents after scaling = %v, want %v (clamped, never zero)", lvl.Seal.Half, want)
	}
}

func TestFitToLevelKeepsTheSealSwitchedOff(t *testing.T) {
	defer saveEditorSelection()()
	lvl := selectTestLevel(t)
	lvl.Seal = &world.Seal{Half: mgl32.Vec3{1, 1, 1}, Disabled: true}

	editorFitSealToLevel()
	if !lvl.Seal.Disabled {
		t.Error("fitting switched the seal back on")
	}
	// The test level's two boxes span (0,0,0) to (4,2,2).
	for _, p := range []mgl32.Vec3{{0, 0, 0}, {4, 2, 2}} {
		if !lvl.Seal.Contains(p) {
			t.Errorf("the fitted seal does not hold %v", p)
		}
	}
}
