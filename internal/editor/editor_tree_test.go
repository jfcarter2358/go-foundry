package editor

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Planting, turning, sizing, duplicating and deleting a tree, each an undo
// step that puts the trees back as they were — which is also the proof that a
// tree survives the level file, since undo snapshots go through it.
func TestTreesArePlantedEditedAndUndone(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	withSpawnMarkerTexture(t)

	editorArmTreePlacement(world.FoliageRedcedar)
	Editor.NewTreeVariant = 1
	if !placeTreeAt(mgl32.Vec3{4, 0, -6}) {
		t.Fatal("placing a tree failed")
	}
	lvl := Editor.level
	if len(lvl.Trees) != 1 || activeTree() == nil || Editor.GizmoKind != gizmoKindTree {
		t.Fatalf("planted %d trees, selection %v, gizmo %q", len(lvl.Trees), activeTree(), Editor.GizmoKind)
	}
	tr := activeTree()
	if tr.Species != world.FoliageRedcedar || tr.Variant != 1 || tr.Position != (mgl32.Vec3{4, 0, -6}) || !tr.Solid {
		t.Fatalf("planted %+v", *tr)
	}
	if !Editor.TreePlacementMode {
		t.Error("placement did not stay armed for the next tree")
	}

	// The gizmos: a yaw turn and a uniform size, from one drag start.
	if !captureGizmoDragStart() {
		t.Fatal("the gizmo did not take the tree")
	}
	applyRotationToSelection(mgl32.Vec3{0, 1, 0}, tr.Position, 0.5)
	applyScaleToSelection(tr.Position, mgl32.Vec3{1, 1.5, 1})
	if d := tr.Yaw - (Editor.GizmoDragStartRot.Y() + 0.5); d > 1e-5 || d < -1e-5 {
		t.Errorf("the yaw ring turned it to %v", tr.Yaw)
	}
	turned := tr.Yaw
	applyRotationToSelection(mgl32.Vec3{1, 0, 0}, tr.Position, 0.5)
	if tr.Yaw != turned {
		t.Errorf("a tilt ring turned it: yaw %v, was %v", tr.Yaw, turned)
	}
	if tr.Scale != 1.5 {
		t.Errorf("sized to %v, want 1.5", tr.Scale)
	}

	duplicateActiveTree()
	if len(lvl.Trees) != 2 || lvl.Trees[1].ID == lvl.Trees[0].ID || lvl.Trees[1].Species != world.FoliageRedcedar {
		t.Fatalf("duplicate: %+v", lvl.Trees)
	}
	EditorDeleteSelected()
	if len(lvl.Trees) != 1 {
		t.Fatalf("delete left %d trees", len(lvl.Trees))
	}

	EditorUndo() // the delete
	if n := len(Editor.level.Trees); n != 2 {
		t.Fatalf("undoing the delete left %d trees", n)
	}
	EditorUndo() // the duplicate
	EditorUndo() // the gizmo drag
	got := Editor.level.Trees
	if len(got) != 1 || got[0].Scale > 1.0001 || got[0].Species != world.FoliageRedcedar || got[0].Variant != 1 {
		t.Fatalf("after undoing back to the planting: %+v", got)
	}
	EditorUndo() // the planting
	if n := len(Editor.level.Trees); n != 0 {
		t.Fatalf("undoing the planting left %d trees", n)
	}
}

// A click on a tree's crown picks it, and nothing else in its column of air.
func TestATreeIsPickedByItsCrown(t *testing.T) {
	tr := world.NewTree("spruce", world.FoliageSitkaSpruce, mgl32.Vec3{10, 0, 0})
	if d := pickTreeRayT(mgl32.Vec3{-30, 20, 0}, mgl32.Vec3{1, 0, 0}, &tr); d > 40 || d < 30 {
		t.Errorf("a ray through the crown hit at %v", d)
	}
	if d := pickTreeRayT(mgl32.Vec3{-30, 80, 0}, mgl32.Vec3{1, 0, 0}, &tr); d < 1e9 {
		t.Errorf("a ray over the top hit at %v", d)
	}
}
