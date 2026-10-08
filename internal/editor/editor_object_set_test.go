package editor

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// withTreeLevel installs a level holding trees at the given feet.
func withTreeLevel(t *testing.T, feet ...mgl32.Vec3) *world.Level {
	t.Helper()
	prevLevel, prevTree, prevGizmo := Editor.level, Editor.ActiveTreeIdx, Editor.GizmoKind
	prevClip := editorClipboard
	t.Cleanup(func() {
		Editor.level, Editor.ActiveTreeIdx, Editor.GizmoKind = prevLevel, prevTree, prevGizmo
		editorClipboard = prevClip
		objSet = nil
	})
	lvl := &world.Level{}
	for _, p := range feet {
		lvl.Trees = append(lvl.Trees, world.NewTree(world.UniqueTreeID(lvl, "tree"), world.FoliageSitkaSpruce, p))
	}
	Editor.level = lvl
	Editor.ActiveTreeIdx = -1
	Editor.GizmoKind = gizmoKindNone
	return lvl
}

func TestCtrlClickBuildsATreeSet(t *testing.T) {
	withTreeLevel(t, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{4, 0, 0}, mgl32.Vec3{8, 0, 0})

	editorSelectTree(0)
	editorToggleObjectSelection(objRef{pickTree, 2})
	if got := objectSet(); len(got) != 2 || got[0].idx != 0 || got[1].idx != 2 {
		t.Fatalf("set after ctrl+click = %v, want trees 0 and 2", got)
	}
	if Editor.GizmoKind != gizmoKindGroup || Editor.ActiveTreeIdx != 2 {
		t.Fatalf("gizmo %q active %d, want group on tree 2", Editor.GizmoKind, Editor.ActiveTreeIdx)
	}

	// Ctrl+clicking a member again takes it out, leaving a plain selection.
	editorToggleObjectSelection(objRef{pickTree, 2})
	if len(objectSet()) != 0 || Editor.ActiveTreeIdx != 0 || Editor.GizmoKind != gizmoKindTree {
		t.Fatalf("after removing: set %v active %d gizmo %q", objectSet(), Editor.ActiveTreeIdx, Editor.GizmoKind)
	}

	// A plain select drops the set.
	editorToggleObjectSelection(objRef{pickTree, 1})
	editorSelectTree(2)
	if len(objectSet()) != 0 {
		t.Fatalf("plain select kept the set: %v", objectSet())
	}
}

func TestGroupMoveKeepsLayout(t *testing.T) {
	lvl := withTreeLevel(t, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{4, 0, 0})
	editorSetObjectSelection([]objRef{{pickTree, 0}, {pickTree, 1}})
	if c, _ := objectSetCenter(); c != (mgl32.Vec3{2, 0, 0}) {
		t.Fatalf("centre %v", c)
	}
	if !captureObjectSetDragStart() {
		t.Fatal("drag did not start")
	}
	dragObjectSet(2, 3)
	if lvl.Trees[0].Position.Z() != lvl.Trees[1].Position.Z() || lvl.Trees[1].Position.X()-lvl.Trees[0].Position.X() != 4 {
		t.Fatalf("layout changed: %v %v", lvl.Trees[0].Position, lvl.Trees[1].Position)
	}
	if lvl.Trees[0].Position.Z() == 0 {
		t.Fatal("group did not move")
	}
}

func TestDeleteSetAndCopyPaste(t *testing.T) {
	lvl := withTreeLevel(t, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{4, 0, 0}, mgl32.Vec3{8, 1, 0})
	editorSetObjectSelection([]objRef{{pickTree, 1}, {pickTree, 2}})
	editorCopySelection()
	if editorClipboard == nil || len(editorClipboard.trees) != 2 {
		t.Fatalf("clipboard = %+v", editorClipboard)
	}
	if editorClipboard.anchor != (mgl32.Vec3{6, 0, 0}) {
		t.Fatalf("anchor %v, want centre XZ at lowest Y", editorClipboard.anchor)
	}

	deleteObjectSet()
	if len(lvl.Trees) != 1 || lvl.Trees[0].Position != (mgl32.Vec3{0, 0, 0}) {
		t.Fatalf("after delete: %+v", lvl.Trees)
	}

	editorPasteClipboardAt(mgl32.Vec3{100, 5, 0})
	if len(lvl.Trees) != 3 {
		t.Fatalf("after paste: %d trees", len(lvl.Trees))
	}
	if lvl.Trees[1].Position != (mgl32.Vec3{98, 5, 0}) || lvl.Trees[2].Position != (mgl32.Vec3{102, 6, 0}) {
		t.Fatalf("pasted at %v %v", lvl.Trees[1].Position, lvl.Trees[2].Position)
	}
	if lvl.Trees[1].ID == lvl.Trees[2].ID || lvl.Trees[1].ID == lvl.Trees[0].ID {
		t.Fatalf("pasted IDs collide: %s %s %s", lvl.Trees[0].ID, lvl.Trees[1].ID, lvl.Trees[2].ID)
	}
	if got := objectSet(); len(got) != 2 {
		t.Fatalf("paste did not select the copies: %v", got)
	}
}
