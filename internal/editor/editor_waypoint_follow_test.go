package editor

// editor_waypoint_follow_test.go — previewing a waypoint animation on an object
// that is not a brush.
//
// A brush is posed through a placement the level file never holds; every other
// kind is posed by writing the fields it does hold. So on top of what
// editor_waypoint_test.go checks for a brush, the promise here is that nothing
// that writes the level down — an undo snapshot, a save — ever sees an object
// where the preview had it.

import (
	"testing"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// spritePreviewLevel is waypointEditorLevel plus a sprite "torch" at {1 1 1} —
// where slideAnimation's path sets off — and the slide animation itself.
func spritePreviewLevel(t *testing.T) (*world.Level, waypointTarget) {
	t.Helper()
	lvl := waypointEditorLevel(t)
	Editor.Active = true
	lvl.Sprites = []world.Sprite{world.NewSprite("torch", mgl32.Vec3{1, 1, 1})}
	lvl.WaypointAnimations = []world.WaypointAnimation{slideAnimation()}
	return lvl, waypointTarget{Kind: hooks.WaypointSprite, ID: "torch"}
}

func TestAPreviewMovesASpriteAndStoppingPutsItBack(t *testing.T) {
	lvl, torch := spritePreviewLevel(t)
	if !editorWaypointPreviewStart("slide", torch) {
		t.Fatal("the preview refused to start on a sprite")
	}
	editorWaypointPreviewTick(1)
	if got := lvl.Sprites[0].Position; !vecNear(got, mgl32.Vec3{1, 1, 3}, 1e-4) {
		t.Errorf("a second into the preview the sprite is at %v, want {1 1 3}", got)
	}

	editorWaypointPreviewStop()
	if s := lvl.Sprites[0]; s.Position != (mgl32.Vec3{1, 1, 1}) || s.Waypoints != nil {
		t.Errorf("stopping the preview left the sprite at %v (following %v)", s.Position, s.Waypoints)
	}
}

// The preview writes the sprite's own Position, so a snapshot taken mid-preview
// has to be handed the authored one — and the preview has to keep holding the
// sprite where it was afterwards.
func TestWritingTheLevelDownMidPreviewSeesTheObjectAtRest(t *testing.T) {
	lvl, torch := spritePreviewLevel(t)
	editorWaypointPreviewStart("slide", torch)
	editorWaypointPreviewTick(1)

	raw, err := editorLevelToRaw()
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if p := raw.Sprites[0].Position; p != (world.RawVec3{X: 1, Y: 1, Z: 1}) {
		t.Errorf("the snapshot holds the sprite at %+v, want where it was authored, {1 1 1}", p)
	}
	if got := lvl.Sprites[0].Position; !vecNear(got, mgl32.Vec3{1, 1, 3}, 1e-4) {
		t.Errorf("after the snapshot the sprite is at %v; the preview should still hold it at {1 1 3}", got)
	}
	editorWaypointPreviewStop()
}

func TestUndoingMidPreviewLeavesTheObjectWhereItWasAuthored(t *testing.T) {
	lvl, torch := spritePreviewLevel(t)
	withSpawnMarkerTexture(t)
	editorWaypointPreviewStart("slide", torch)
	editorWaypointPreviewTick(1)

	EditorPushUndo()
	EditorUndo()
	if got := lvl.Sprites[0].Position; got != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("after an undo mid-preview the sprite is at %v, want {1 1 1}", got)
	}
	if Editor.WaypointPreview.Posed {
		t.Error("the preview still believes it holds a sprite the undo rebuilt")
	}
}

// A directional light is posed through its angles, which a direction does not
// give back exactly; stopping has to.
func TestAPreviewOnADirectionalLightGivesItsAnglesBack(t *testing.T) {
	lvl, _ := spritePreviewLevel(t)
	lvl.Lights.Directional = []world.DirectionalLight{{ID: "sun", Yaw: 270, Pitch: -30}}
	lvl.WaypointAnimations[0].Points[0].Rotation = world.DirLightEuler(0, -60)
	sun := &lvl.Lights.Directional[0]
	sun.SyncDirection()

	if !editorWaypointPreviewStart("slide", waypointTarget{Kind: hooks.WaypointLight, ID: "sun"}) {
		t.Fatal("the preview refused to start on a directional light")
	}
	editorWaypointPreviewTick(2.5)
	if sun.Yaw == 270 && sun.Pitch == -30 {
		t.Fatal("the preview did not turn the sun")
	}
	editorWaypointPreviewStop()
	if sun.Yaw != 270 || sun.Pitch != -30 {
		t.Errorf("after Stop the sun is at yaw %v pitch %v, want exactly 270 and -30", sun.Yaw, sun.Pitch)
	}
}

func TestAPreviewRefusesAVehicle(t *testing.T) {
	lvl, _ := spritePreviewLevel(t)
	lvl.Entities = []world.Entity{{ID: "ship", Vehicle: &world.VehicleSpec{}}}
	if editorWaypointPreviewStart("slide", waypointTarget{Kind: hooks.WaypointEntity, ID: "ship"}) {
		t.Error("the preview played on a vehicle, whose drive model owns its motion")
	}
	for _, tg := range editorWaypointTargets(lvl) {
		if tg.ID == "ship" {
			t.Error("a vehicle is offered as something to preview on")
		}
	}
}

func TestPreviewTargetsListEveryKindButStaticBrushes(t *testing.T) {
	lvl, torch := spritePreviewLevel(t)
	lvl.Lights.Points = []world.PointLight{{ID: "lamp"}}
	lvl.Chunks[0].Brushes[1].Dynamic = true // "b"; "a" stays static

	got := map[waypointTarget]bool{}
	for _, tg := range editorWaypointTargets(lvl) {
		got[tg] = true
	}
	for _, want := range []waypointTarget{torch, {Kind: hooks.WaypointLight, ID: "lamp"}, {Kind: hooks.WaypointBrush, ID: "b"}} {
		if !got[want] {
			t.Errorf("%s is not offered to preview on", want.label())
		}
	}
	if got[waypointTarget{Kind: hooks.WaypointBrush, ID: "a"}] {
		t.Error("a static brush is offered to preview on")
	}
}
