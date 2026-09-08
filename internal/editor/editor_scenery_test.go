package editor

// editor_scenery_test.go — the brush's behaviour, which is where it differs
// from every other placement tool in this editor.
//
// Three properties, and all three are easy to regress into "one-shot placement
// that happens to be called a brush":
//
//   - a stamp leaves the brush armed, so the next click places another copy;
//   - a stamp does not select what it made, because a selection would fight the
//     brush for that next click;
//   - the ghost's transform is what a stamp inherits, so what lands is what was
//     being previewed.
//
// Plus the selection bookkeeping every index-into-a-slice selection in this
// package needs: a delete must not leave the index pointing at a neighbour.

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// sceneryTestLevel installs an empty level and puts the editor back afterwards.
func sceneryTestLevel(t *testing.T, props ...world.Scenery) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	lvl := &world.Level{Scenery: props}
	prev := Editor.level
	Editor.level = lvl
	t.Cleanup(func() {
		Editor.level = prev
		editorClearScenerySelection()
		Editor.BrushActive = false
		Editor.BrushModel = ""
		Editor.BrushRotation = mgl32.Vec3{}
		Editor.BrushScale = 0
		Editor.BrushRandomYaw = false
	})
	return lvl
}

// armTestBrush points the brush at a model that does not exist. The stamp still
// lands — a placement whose mesh will not load is real level content, which is
// the rule editor_scenery.go and the level loader both hold — and nothing here
// needs a GL context to prove what it is proving.
func armTestBrush(t *testing.T) {
	t.Helper()
	Editor.BrushModel = "models/rock.fmdl"
	Editor.BrushActive = true
	Editor.BrushSolid = true
	Editor.BrushScale = 1
}

func TestBrushStaysArmedAfterPlacing(t *testing.T) {
	lvl := sceneryTestLevel(t)
	armTestBrush(t)

	for i := 1; i <= 3; i++ {
		if !brushStamp(mgl32.Vec3{float32(i), 0, 0}) {
			t.Fatalf("stamp %d was refused", i)
		}
		if !Editor.BrushActive {
			t.Fatalf("the brush disarmed itself after stamp %d", i)
		}
	}
	if len(lvl.Scenery) != 3 {
		t.Fatalf("three clicks placed %d props", len(lvl.Scenery))
	}
}

// Selecting what was just stamped would give the next click two meanings. It
// must not happen.
func TestBrushDoesNotSelectWhatItPlaces(t *testing.T) {
	sceneryTestLevel(t)
	armTestBrush(t)

	brushStamp(mgl32.Vec3{1, 0, 0})
	if Editor.ActiveSceneryIdx != -1 && activeScenery() != nil {
		t.Errorf("a stamp selected its own copy (idx %d)", Editor.ActiveSceneryIdx)
	}
	if Editor.GizmoKind == gizmoKindScenery {
		t.Error("a stamp attached the gizmo to its own copy")
	}
}

// Every copy is addressable and named after the mesh, so a level of forty rocks
// reads as rock, rock_2, rock_3 rather than forty things called "scenery".
func TestStampedPropsGetUniqueIDsFromTheModelName(t *testing.T) {
	lvl := sceneryTestLevel(t)
	armTestBrush(t)

	for i := 0; i < 3; i++ {
		brushStamp(mgl32.Vec3{float32(i), 0, 0})
	}
	seen := map[string]bool{}
	for _, s := range lvl.Scenery {
		if s.ID == "" {
			t.Fatal("a stamp produced a prop with no ID")
		}
		if seen[s.ID] {
			t.Fatalf("two props share the ID %q", s.ID)
		}
		seen[s.ID] = true
	}
	if !seen["rock"] {
		t.Errorf("the first copy was not named after its model: %v", seen)
	}
}

// The ghost carries the transform; the stamp inherits it. If these ever come
// apart, what lands is not what was previewed.
func TestStampInheritsTheGhostsTransform(t *testing.T) {
	lvl := sceneryTestLevel(t)
	armTestBrush(t)
	Editor.BrushRotation = mgl32.Vec3{0, 1.25, 0}
	Editor.BrushScale = 2.5
	Editor.BrushSolid = false

	brushStamp(mgl32.Vec3{4, 1, -2})
	s := lvl.Scenery[0]
	if s.Position != (mgl32.Vec3{4, 1, -2}) {
		t.Errorf("position: got %v", s.Position)
	}
	if s.Rotation != (mgl32.Vec3{0, 1.25, 0}) {
		t.Errorf("rotation: got %v, want the brush's", s.Rotation)
	}
	if s.Scale != (mgl32.Vec3{2.5, 2.5, 2.5}) {
		t.Errorf("scale: got %v, want 2.5 uniform", s.Scale)
	}
	if s.Solid {
		t.Error("the brush's solid flag did not reach the copy")
	}
}

// Random yaw is the difference between forty rocks and one rock forty times.
// The ghost keeps its dialled-in facing either way — a preview that spun on
// every mouse move would be useless for lining a crate up against a wall.
func TestRandomYawVariesTheCopiesAndNotTheGhost(t *testing.T) {
	lvl := sceneryTestLevel(t)
	armTestBrush(t)
	Editor.BrushRotation = mgl32.Vec3{0, 0.5, 0}
	Editor.BrushRandomYaw = true

	for i := 0; i < 16; i++ {
		brushStamp(mgl32.Vec3{float32(i), 0, 0})
	}
	distinct := map[float32]bool{}
	for _, s := range lvl.Scenery {
		distinct[s.Rotation.Y()] = true
		// Only the yaw is randomised: pitch and roll stay where the brush put
		// them, or a scattered field of upright trees would lie down.
		if s.Rotation.X() != 0 || s.Rotation.Z() != 0 {
			t.Fatalf("random yaw disturbed another axis: %v", s.Rotation)
		}
	}
	if len(distinct) < 8 {
		t.Errorf("16 copies took only %d distinct facings", len(distinct))
	}
	if Editor.BrushRotation.Y() != 0.5 {
		t.Errorf("the ghost's own yaw was overwritten: %v", Editor.BrushRotation.Y())
	}
}

// A brush with no model has nothing to place, and a click while it is armed
// must not append a prop with an empty mesh path.
func TestBrushWithNoModelPlacesNothing(t *testing.T) {
	lvl := sceneryTestLevel(t)
	Editor.BrushActive = true
	Editor.BrushModel = ""

	if brushStamp(mgl32.Vec3{1, 0, 0}) {
		t.Error("a brush with no model reported a placement")
	}
	if len(lvl.Scenery) != 0 {
		t.Fatalf("a brush with no model placed %d props", len(lvl.Scenery))
	}
}

// ---------- keys ----------

func TestBrushKeysTurnAndResizeTheGhost(t *testing.T) {
	sceneryTestLevel(t)
	armTestBrush(t)

	if !editorBrushHandleKey(sdl.SCANCODE_RIGHTBRACKET, false) {
		t.Fatal("] was not consumed while brushing")
	}
	if got := mgl32.RadToDeg(Editor.BrushRotation.Y()); !nearlyEqual(got, brushYawStep, 1e-3) {
		t.Errorf("] turned the ghost %v degrees, want %v", got, brushYawStep)
	}
	// Shift is the fine step, not a second coarse one.
	editorBrushHandleKey(sdl.SCANCODE_LEFTBRACKET, true)
	if got := mgl32.RadToDeg(Editor.BrushRotation.Y()); !nearlyEqual(got, brushYawStep-brushFineYawStep, 1e-3) {
		t.Errorf("Shift+[ turned the ghost to %v degrees", got)
	}

	before := editorBrushScale()
	editorBrushHandleKey(sdl.SCANCODE_EQUALS, false)
	if editorBrushScale() <= before {
		t.Error("= did not grow the ghost")
	}
	editorBrushHandleKey(sdl.SCANCODE_MINUS, false)
	if got := editorBrushScale(); !nearlyEqual(got, before, 1e-4) {
		t.Errorf("- did not undo =: %v, want %v", got, before)
	}
}

func TestEscapePutsTheBrushAway(t *testing.T) {
	sceneryTestLevel(t)
	armTestBrush(t)

	if !editorBrushHandleKey(sdl.SCANCODE_ESCAPE, false) {
		t.Fatal("Escape was not consumed while brushing")
	}
	if Editor.BrushActive {
		t.Error("Escape left the brush armed")
	}
}

// The keys belong to the brush and only to the brush: bare ] must still reach
// whatever else wants it when nothing is being scattered.
func TestBrushKeysAreInertWhenTheBrushIsOff(t *testing.T) {
	sceneryTestLevel(t)
	Editor.BrushActive = false

	for _, key := range []sdl.Scancode{
		sdl.SCANCODE_LEFTBRACKET, sdl.SCANCODE_RIGHTBRACKET,
		sdl.SCANCODE_MINUS, sdl.SCANCODE_EQUALS, sdl.SCANCODE_ESCAPE,
	} {
		if editorBrushHandleKey(key, false) {
			t.Errorf("scancode %v was swallowed with the brush off", key)
		}
	}
}

// The yaw wraps rather than creeping off to 3600 degrees over a session of
// scattering, which would make the panel's own field unreadable.
func TestBrushYawWraps(t *testing.T) {
	sceneryTestLevel(t)
	armTestBrush(t)
	for i := 0; i < 40; i++ {
		editorBrushYaw(1, false)
	}
	if got := mgl32.RadToDeg(Editor.BrushRotation.Y()); got > 180.001 || got < -180.001 {
		t.Errorf("yaw left its range: %v degrees", got)
	}
}

// ---------- selection ----------

// Delete has to leave the index somewhere valid, or the panel goes on showing
// the properties of whatever slid into the gap.
func TestDeletingAPropClearsTheSelection(t *testing.T) {
	lvl := sceneryTestLevel(t,
		world.NewScenery("a", "models/a.fmdl", mgl32.Vec3{}),
		world.NewScenery("b", "models/b.fmdl", mgl32.Vec3{1, 0, 0}),
	)
	editorSelectScenery(0)
	deleteActiveScenery()

	if len(lvl.Scenery) != 1 || lvl.Scenery[0].ID != "b" {
		t.Fatalf("delete removed the wrong prop: %+v", lvl.Scenery)
	}
	if activeScenery() != nil {
		t.Error("the selection survived the delete it was the target of")
	}
	if Editor.GizmoKind == gizmoKindScenery {
		t.Error("the gizmo stayed attached to a deleted prop")
	}
}

// A duplicate is offset rather than coincident — two props in the same place
// cannot be told apart, let alone clicked apart — and it is the copy that ends
// up selected.
func TestDuplicatingAPropOffsetsAndSelectsTheCopy(t *testing.T) {
	src := world.NewScenery("rock", "models/rock.fmdl", mgl32.Vec3{2, 0, 2})
	src.Rotation = mgl32.Vec3{0, 0.75, 0}
	src.Scale = mgl32.Vec3{1.5, 1.5, 1.5}
	src.Solid = false
	lvl := sceneryTestLevel(t, src)

	editorSelectScenery(0)
	duplicateActiveScenery()

	if len(lvl.Scenery) != 2 {
		t.Fatalf("duplicate produced %d props", len(lvl.Scenery))
	}
	cp := lvl.Scenery[1]
	if cp.ID == src.ID {
		t.Error("the copy took the original's ID")
	}
	if cp.Position == src.Position {
		t.Error("the copy landed exactly on the original")
	}
	if cp.Rotation != src.Rotation || cp.Scale != src.Scale || cp.Solid != src.Solid {
		t.Errorf("the copy did not inherit the original's placement: %+v", cp)
	}
	if Editor.ActiveSceneryIdx != 1 {
		t.Errorf("the copy was not selected: idx %d", Editor.ActiveSceneryIdx)
	}
}

// Arming the brush must put every one-shot placement mode away: both answer the
// next left click, and two armed at once makes which wins a question about the
// order of the checks in Editor3DHandleClick.
func TestArmingTheBrushDisarmsTheOneShotModes(t *testing.T) {
	sceneryTestLevel(t)
	Editor.EntityPlacementMode = true
	Editor.VolumePlacementMode = true
	Editor.SpritePlacementMode = true
	Editor.SpawnPlacementMode = true
	t.Cleanup(func() {
		Editor.EntityPlacementMode = false
		Editor.VolumePlacementMode = false
		Editor.SpritePlacementMode = false
		Editor.SpawnPlacementMode = false
	})

	editorArmBrush("models/rock.fmdl")

	if Editor.EntityPlacementMode || Editor.VolumePlacementMode ||
		Editor.SpritePlacementMode || Editor.SpawnPlacementMode {
		t.Error("arming the brush left a one-shot placement mode armed")
	}
	if !Editor.BrushActive {
		t.Error("the brush did not arm")
	}
}

// And a click while the brush is armed is never a selection, even on a frame
// where the ghost was not drawn — otherwise a click aimed at placing a rock
// would throw away whatever it happened to land on instead.
func TestBrushConsumesClicksEvenWithNoGhost(t *testing.T) {
	lvl := sceneryTestLevel(t)
	armTestBrush(t)
	brushGhostValid = false

	if !editorBrushHandleClick() {
		t.Error("a click while brushing fell through to selection")
	}
	if len(lvl.Scenery) != 0 {
		t.Error("a click with no ghost drawn still placed something")
	}
}
