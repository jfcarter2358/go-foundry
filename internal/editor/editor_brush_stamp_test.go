package editor

// editor_brush_stamp_test.go — the armed brush stamp, which is where placing a
// solid differs from every other Add menu entry.
//
// Blocking out a room is a dozen boxes in a row, so the stamp is a mode rather
// than a one-shot: the properties below are the ones that turn back into "a
// menu item that places one brush" the moment somebody regresses them.
//
//   - a stamp leaves the tool armed, so the next click places another;
//   - Escape is the way out, and it is the only key the tool takes;
//   - arming it puts every other placement tool away, because they all answer
//     the same left-click;
//   - the ghost and the stamp agree about where the brush lands — the point of
//     casting one ray per frame and reading it from the click.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// armTestStamp arms the stamp and puts a ghost point under the cursor, which is
// what a click reads. The ghost itself is drawn from a GL context these tests
// do not have, so its two outputs are set directly.
func armTestStamp(t *testing.T, kind string, pos mgl32.Vec3) {
	t.Helper()
	editorArmBrushPlacement(kind)
	if !Editor.BrushPlaceActive {
		t.Fatalf("arming the %s stamp left it off", kind)
	}
	brushPlacePos = pos
	brushPlaceValid = true
}

// A stamped brush wears no texture. Naming one for it — "concrete", as this
// used to — puts a material into the level that nobody chose, and which the
// author has to notice before they can replace it; the renderer paints an
// untextured face from its own normal instead, so a blockout still reads.
func TestStampedBrushIsUntextured(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)

	armTestStamp(t, brushKindBox, mgl32.Vec3{8, 0, 0})
	if !editorBrushPlaceHandleClick() {
		t.Fatal("the click was not consumed by the stamp")
	}
	b := &ch.Brushes[len(ch.Brushes)-1]
	if len(b.Faces) == 0 {
		t.Fatalf("stamped brush %q has no faces", b.ID)
	}
	for i, f := range b.Faces {
		if f.Texture != "" {
			t.Errorf("face %d of a freshly stamped brush wears %q, want no texture", i, f.Texture)
		}
	}
}

func TestBrushStampStaysArmedAfterPlacing(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	before := len(ch.Brushes)

	for i := 1; i <= 3; i++ {
		armTestStamp(t, brushKindBox, mgl32.Vec3{float32(i * 4), 0, 0})
		if !editorBrushPlaceHandleClick() {
			t.Fatalf("click %d was not consumed by the stamp", i)
		}
		if !Editor.BrushPlaceActive {
			t.Fatalf("the stamp disarmed itself after click %d", i)
		}
	}
	if got := len(ch.Brushes) - before; got != 3 {
		t.Fatalf("three clicks placed %d brushes", got)
	}
}

// A click while the ghost is hidden — the cursor over the side panel — places
// nothing, but must not fall through to selecting either.
func TestBrushStampSwallowsClickWithoutGhost(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	before := len(ch.Brushes)

	armTestStamp(t, brushKindBox, mgl32.Vec3{})
	brushPlaceValid = false
	if !editorBrushPlaceHandleClick() {
		t.Fatal("a click while stamping fell through to selection")
	}
	if len(ch.Brushes) != before {
		t.Fatalf("a click with no ghost placed %d brushes", len(ch.Brushes)-before)
	}
}

// Nothing armed means the stamp never sees the click at all.
func TestBrushStampIgnoresClickWhenDisarmed(t *testing.T) {
	defer saveEditorState()()
	brushTestLevel(t)
	editorDisarmBrushPlacement()
	if editorBrushPlaceHandleClick() {
		t.Fatal("the disarmed stamp consumed a click")
	}
}

func TestBrushStampEscapeDisarms(t *testing.T) {
	defer saveEditorState()()
	brushTestLevel(t)
	armTestStamp(t, brushKindWedge, mgl32.Vec3{})

	if editorBrushPlaceHandleKey(sdl.SCANCODE_G) {
		t.Fatal("the stamp swallowed a key that is bound elsewhere")
	}
	if !editorBrushPlaceHandleKey(sdl.SCANCODE_ESCAPE) {
		t.Fatal("Escape was not consumed while the stamp was armed")
	}
	if Editor.BrushPlaceActive {
		t.Fatal("Escape left the stamp armed")
	}
	if editorBrushPlaceHandleKey(sdl.SCANCODE_ESCAPE) {
		t.Fatal("the disarmed stamp consumed Escape")
	}
}

// Two tools armed at once would make which one wins a question about the order
// of the checks in Editor3DHandleClick.
func TestArmingBrushStampDisarmsEverythingElse(t *testing.T) {
	defer saveEditorState()()
	brushTestLevel(t)
	Editor.EntityPlacementMode = true
	Editor.PrefabPlacementMode = true
	Editor.VolumePlacementMode = true
	Editor.SpritePlacementMode = true
	Editor.EmitterPlacementMode = true
	Editor.SoundPlacementMode = true
	Editor.SpawnPlacementMode = true
	Editor.BrushActive = true

	editorArmBrushPlacement(brushKindBox)

	for name, on := range map[string]bool{
		"entity":  Editor.EntityPlacementMode,
		"prefab":  Editor.PrefabPlacementMode,
		"volume":  Editor.VolumePlacementMode,
		"sprite":  Editor.SpritePlacementMode,
		"emitter": Editor.EmitterPlacementMode,
		"sound":   Editor.SoundPlacementMode,
		"spawn":   Editor.SpawnPlacementMode,
		"scenery": Editor.BrushActive,
	} {
		if on {
			t.Errorf("%s placement survived arming the brush stamp", name)
		}
	}
}

// The gizmo's arms are drawn over everything and sit right where a freshly
// stamped brush is, so they would swallow the click meant to place the next one.
func TestBrushStampSuppressesGizmo(t *testing.T) {
	defer saveEditorState()()
	brushTestLevel(t)
	editorArmBrushPlacement(brushKindBox)
	if !gizmoSuppressed() {
		t.Fatal("the gizmo is still live while the stamp is armed")
	}
}

// The ghost is drawn from the same two functions the stamp places through, so
// what lands is what was previewed. These are the pieces that decide that.
func TestBrushStampAnchorAndCentreMatchTheGhost(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)

	Editor.SnapToGrid = false
	if got := brushStampAnchor(mgl32.Vec3{1.3, 0, 2.7}); got != (mgl32.Vec3{1.3, 0, 2.7}) {
		t.Errorf("unsnapped anchor = %v, want the point itself", got)
	}
	Editor.SnapToGrid = true
	Editor.StepSize = 1
	if got := brushStampAnchor(mgl32.Vec3{1.3, 0, 2.7}); got != (mgl32.Vec3{1, 0, 3}) {
		t.Errorf("snapped anchor = %v, want (1,0,3)", got)
	}

	// A box sits *on* the click, so its centre is half a box above it; a wedge
	// and a cylinder straddle it.
	click := mgl32.Vec3{4, 0, 4}
	if got := brushStampLocalCenter(ch, brushKindBox, click); got != (mgl32.Vec3{4, defaultBrushHalf, 4}) {
		t.Errorf("box centre = %v, want the click lifted by %v", got, defaultBrushHalf)
	}
	for _, kind := range []string{brushKindWedge, brushKindCylinder} {
		if got := brushStampLocalCenter(ch, kind, click); got != click {
			t.Errorf("%s centre = %v, want the click itself", kind, got)
		}
	}
}
