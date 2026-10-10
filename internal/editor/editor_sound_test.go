package editor

// editor_sound_test.go — the sound editor's testable half.
//
// Two things worth asserting here, both of which are silent when broken.
//
// The first is that duplicating a sound gives the copy its own state. A copy
// that shares the original's ScriptData shares the *map*, so tagging one tags
// both; a copy that shares the original's mixer voice means stopping one
// silences both, and the survivor then never restarts because it still
// believes it is playing. Neither shows up in a screenshot.
//
// The second is that selecting a sound clears whatever else was selected. The
// properties panel draws the selected object's fields and only those, so two
// live selections means one of them is being edited invisibly.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// withSoundLevel installs a level holding the given sounds and selects none.
// Editor is a package global, so it gets the with-helper treatment.
func withSoundLevel(t *testing.T, sounds ...world.Sound) *world.Level {
	t.Helper()
	prevLevel := Editor.level
	prevSound := Editor.ActiveSoundIdx
	prevEmitter := Editor.ActiveEmitterIdx
	prevGizmo := Editor.GizmoKind
	prevFocus := Editor.PanelFocus
	t.Cleanup(func() {
		Editor.level = prevLevel
		Editor.ActiveSoundIdx = prevSound
		Editor.ActiveEmitterIdx = prevEmitter
		Editor.GizmoKind = prevGizmo
		Editor.PanelFocus = prevFocus
	})

	lvl := &world.Level{Sounds: sounds}
	Editor.level = lvl
	Editor.ActiveSoundIdx = -1
	Editor.ActiveEmitterIdx = -1
	Editor.GizmoKind = gizmoKindNone
	return lvl
}

func testSound(id string) world.Sound {
	s := world.NewSound(id, mgl32.Vec3{1, 2, 3})
	s.Clip = "hum"
	return s
}

func TestDuplicatingASoundGivesTheCopyItsOwnTags(t *testing.T) {
	src := testSound("hum")
	src.Tags = []string{"ambient"}
	src.Attributes = world.AttrSet{"level": world.IntAttr(3)}
	lvl := withSoundLevel(t, src)

	editorSelectSound(0)
	duplicateActiveSound()

	if len(lvl.Sounds) != 2 {
		t.Fatalf("%d sounds after duplicating one", len(lvl.Sounds))
	}
	cp := &lvl.Sounds[1]
	cp.Attributes["level"] = world.IntAttr(99)
	if lvl.Sounds[0].Attributes["level"] == world.IntAttr(99) {
		t.Error("the copy shares the original's attribute map; editing one edits both")
	}
	cp.Tags[0] = "changed"
	if lvl.Sounds[0].Tags[0] == "changed" {
		t.Error("the copy shares the original's tag slice")
	}
}

func TestDuplicatingASoundDoesNotShareItsVoice(t *testing.T) {
	// Sharing the handle would mean stopping one silenced both, and the
	// survivor would then never start again because it still believes it is
	// playing.
	src := testSound("hum")
	src.Voice = 12345
	lvl := withSoundLevel(t, src)

	editorSelectSound(0)
	duplicateActiveSound()

	if !lvl.Sounds[1].Voice.IsZero() {
		t.Errorf("the copy inherited voice %d; it must start with none", lvl.Sounds[1].Voice)
	}
}

func TestDuplicatingASoundGivesItAFreeID(t *testing.T) {
	lvl := withSoundLevel(t, testSound("hum"))
	editorSelectSound(0)
	duplicateActiveSound()

	if lvl.Sounds[1].ID == lvl.Sounds[0].ID {
		t.Errorf("both sounds are called %q; `@hum` would be ambiguous", lvl.Sounds[0].ID)
	}
	if world.LevelIDTaken(nil, lvl.Sounds[1].ID) {
		t.Errorf("the copy took a reserved ID %q", lvl.Sounds[1].ID)
	}
}

func TestSelectingASoundClearsEveryOtherSelection(t *testing.T) {
	// The properties panel draws the selected object's fields and only those,
	// so two live selections means one is being edited invisibly.
	lvl := withSoundLevel(t, testSound("hum"))
	lvl.Emitters = []world.Emitter{world.NewEmitter("torch", mgl32.Vec3{})}

	editorSelectEmitter(0)
	if Editor.ActiveEmitterIdx != 0 {
		t.Fatal("the emitter did not select")
	}

	editorSelectSound(0)
	if Editor.ActiveEmitterIdx != -1 {
		t.Error("selecting a sound left the emitter selected too")
	}
	if Editor.GizmoKind != gizmoKindSound {
		t.Errorf("the gizmo is on %q, not the sound", Editor.GizmoKind)
	}
	if Editor.PanelFocus != panelFocusSound {
		t.Errorf("the panel focused %q, not the sound", Editor.PanelFocus)
	}
}

func TestClearingTheSoundSelectionReleasesTheGizmo(t *testing.T) {
	withSoundLevel(t, testSound("hum"))
	editorSelectSound(0)
	editorClearSoundSelection()

	if Editor.ActiveSoundIdx != -1 {
		t.Error("the selection index survived")
	}
	if Editor.GizmoKind == gizmoKindSound {
		t.Error("the gizmo is still attached to a sound that is no longer selected")
	}
	if activeGizmoSound() != nil {
		t.Error("activeGizmoSound still hands back a sound")
	}
}

func TestDeletingASoundClearsTheSelectionRatherThanLeavingItDangling(t *testing.T) {
	// Selection is an index because undo replaces the level wholesale. An
	// index left pointing past the end of a shortened slice is the bug that
	// arrangement exists to make impossible, and this is the guard.
	lvl := withSoundLevel(t, testSound("a"), testSound("b"))
	editorSelectSound(1)
	deleteActiveSound()

	if len(lvl.Sounds) != 1 {
		t.Fatalf("%d sounds after deleting one of two", len(lvl.Sounds))
	}
	if Editor.ActiveSoundIdx != -1 {
		t.Errorf("the selection is still %d, past the end of a one-element slice", Editor.ActiveSoundIdx)
	}
	if activeSound() != nil {
		t.Error("activeSound still returns something")
	}
}

func TestPickingTestsTheMarkerThatIsActuallyDrawn(t *testing.T) {
	// The marker is a camera-facing disc, so a ray straight at it must hit and
	// a ray past its edge must miss. Testing what is drawn is what makes a
	// click land where it looks like it should.
	lvl := withSoundLevel(t, testSound("hum"))
	s := &lvl.Sounds[0]
	s.Position = mgl32.Vec3{0, 0, 0}

	cam := mgl32.Vec3{0, 0, 10}
	hit := pickSoundRayT(cam, mgl32.Vec3{0, 0, -1}, lvl, s, cam)
	if hit > 1e6 {
		t.Error("a ray straight at the marker missed it")
	}

	// Offset by more than the marker radius, parallel to the first ray.
	off := cam.Add(mgl32.Vec3{soundMarkerRadius * 4, 0, 0})
	if miss := pickSoundRayT(off, mgl32.Vec3{0, 0, -1}, lvl, s, cam); miss < 1e6 {
		t.Error("a ray well past the marker's edge hit it anyway")
	}
}

func TestAnAttachedSoundIsPickedWhereItActuallyIs(t *testing.T) {
	// The marker follows the carrier, but the gizmo and the panel show the
	// authored position. Picking has to use the world one or an attached
	// sound becomes unclickable.
	lvl := withSoundLevel(t, testSound("hum"))
	s := &lvl.Sounds[0]
	s.Position = mgl32.Vec3{5, 0, 0}

	want := s.WorldPosition(lvl)
	cam := want.Add(mgl32.Vec3{0, 0, 10})
	if hit := pickSoundRayT(cam, mgl32.Vec3{0, 0, -1}, lvl, s, cam); hit > 1e6 {
		t.Errorf("the marker at %v was not where picking looked", want)
	}
}
