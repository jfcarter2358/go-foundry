package editor

// editor_sprite_test.go — the two things about sprites that are easy to get
// wrong and invisible when you do.
//
// One: the picker's quad and the shader's quad have to agree. They are separate
// implementations of one rule (the shader cannot be asked where it put
// something), so nothing but a test keeps them together — and the symptom of
// drift is a sprite that has to be clicked slightly beside itself, which reads
// as a fiddly editor rather than as a bug.
//
// Two: selection is an index into a slice that undo replaces wholesale, so
// every path that shortens the list has to leave the index somewhere valid.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// spriteTestLevel installs a level holding one sprite and returns it.
func spriteTestLevel(t *testing.T, s world.Sprite) *world.Level {
	t.Helper()
	lvl := &world.Level{Sprites: []world.Sprite{s}}
	prev := Editor.level
	Editor.level = lvl
	t.Cleanup(func() {
		Editor.level = prev
		editorClearSpriteSelection()
	})
	return lvl
}

// TestYAxisSpriteStaysUprightWhateverTheCamera is the property that makes
// BillboardYAxis worth having: a bush must not lean back as the camera rises.
func TestYAxisSpriteStaysUpright(t *testing.T) {
	lvl := spriteTestLevel(t, world.Sprite{
		Position: mgl32.Vec3{}, Width: 2, Height: 4,
		Billboard: gfx.BillboardYAxis, Visible: true, Texture: "default",
	})
	s := &lvl.Sprites[0]

	for _, cam := range []mgl32.Vec3{
		{0, 0, 10}, {10, 0, 0}, {7, 7, 7}, {0, 20, 0.001}, {-3, -8, 2},
	} {
		c := spriteQuadCorners(lvl, s, cam)
		// The two bottom corners must share a height, and the top pair must sit
		// exactly Height above them: that is what "up is world up" means.
		if math.Abs(float64(c[0].Y()-c[1].Y())) > 1e-4 {
			t.Errorf("cam %v: bottom edge is not level (%v vs %v)", cam, c[0].Y(), c[1].Y())
		}
		if got := c[3].Y() - c[0].Y(); math.Abs(float64(got-s.Height)) > 1e-4 {
			t.Errorf("cam %v: quad is %v tall, want %v", cam, got, s.Height)
		}
	}
}

// TestFullBillboardFacesTheCamera checks the other mode's defining property.
func TestFullBillboardFacesTheCamera(t *testing.T) {
	lvl := spriteTestLevel(t, world.Sprite{
		Position: mgl32.Vec3{}, Width: 2, Height: 2,
		Billboard: gfx.BillboardFull, Visible: true, Texture: "default",
	})
	s := &lvl.Sprites[0]

	for _, cam := range []mgl32.Vec3{{0, 0, 5}, {5, 5, 5}, {-2, 9, 1}} {
		c := spriteQuadCorners(lvl, s, cam)
		n := c[1].Sub(c[0]).Cross(c[3].Sub(c[0]))
		if n.LenSqr() < 1e-8 {
			t.Fatalf("cam %v: degenerate quad", cam)
		}
		// The quad's normal should point straight at the camera.
		if got := n.Normalize().Dot(cam.Normalize()); math.Abs(float64(got)) < 0.999 {
			t.Errorf("cam %v: quad normal is %v off the view axis (dot %v)", cam, n.Normalize(), got)
		}
	}
}

// TestPickHitsTheQuadItDraws is the picker-vs-shader agreement, checked the
// only way it can be from the CPU side: a ray fired at the sprite's centre must
// hit at the distance the sprite actually is, and a ray fired past its edge
// must miss.
func TestPickHitsTheQuadItDraws(t *testing.T) {
	lvl := spriteTestLevel(t, world.Sprite{
		Position: mgl32.Vec3{0, 0, -10}, Width: 2, Height: 2,
		Billboard: gfx.BillboardFull, Visible: true, Texture: "default",
	})
	s := &lvl.Sprites[0]
	cam := mgl32.Vec3{0, 0, 0}

	got := pickSpriteRayT(cam, mgl32.Vec3{0, 0, -1}, lvl, s, cam)
	if math.Abs(float64(got-10)) > 1e-3 {
		t.Errorf("centre shot: got t=%v, want 10", got)
	}

	// Just outside the half-width, at the sprite's depth.
	miss := mgl32.Vec3{1.01, 0, -10}.Normalize()
	if got := pickSpriteRayT(cam, miss, lvl, s, cam); got < float32(math.MaxFloat32) {
		t.Errorf("shot past the edge hit at t=%v; it should miss", got)
	}

	// Just inside it.
	hit := mgl32.Vec3{0.99, 0, -10}.Normalize()
	if got := pickSpriteRayT(cam, hit, lvl, s, cam); got >= float32(math.MaxFloat32) {
		t.Error("shot inside the edge missed")
	}
}

// TestHiddenSpriteIsStillPickable — a script that hid a sprite must not thereby
// make it unreachable in the editor, the same courtesy a disabled volume gets.
func TestHiddenSpriteIsStillPickable(t *testing.T) {
	lvl := spriteTestLevel(t, world.Sprite{
		Position: mgl32.Vec3{0, 0, -5}, Width: 2, Height: 2,
		Billboard: gfx.BillboardFull, Visible: false, Texture: "default",
	})
	cam := mgl32.Vec3{}
	if got := pickSpriteRayT(cam, mgl32.Vec3{0, 0, -1}, lvl, &lvl.Sprites[0], cam); got >= float32(math.MaxFloat32) {
		t.Error("a hidden sprite could not be clicked")
	}
}

// TestDeleteClearsTheSelection guards the index-not-pointer rule: deleting the
// selected sprite must not leave the index pointing into a shorter slice, or
// the next panel draw reads off the end.
func TestDeleteClearsTheSelection(t *testing.T) {
	lvl := spriteTestLevel(t, world.Sprite{ID: "a", Width: 1, Height: 1, Visible: true})
	lvl.Sprites = append(lvl.Sprites, world.Sprite{ID: "b", Width: 1, Height: 1, Visible: true})

	editorSelectSprite(1)
	if activeSprite() == nil || activeSprite().ID != "b" {
		t.Fatal("select did not take")
	}
	deleteActiveSprite()

	if Editor.ActiveSpriteIdx != -1 {
		t.Errorf("after delete the index is %d, want -1", Editor.ActiveSpriteIdx)
	}
	if activeSprite() != nil {
		t.Error("something is still selected after deleting it")
	}
	if Editor.GizmoKind == gizmoKindSprite {
		t.Error("the gizmo is still holding a deleted sprite")
	}
	if len(lvl.Sprites) != 1 || lvl.Sprites[0].ID != "a" {
		t.Errorf("the wrong sprite was deleted: %+v", lvl.Sprites)
	}
}

// TestSelectingASpriteClearsOtherSelections — the selections are meant to be
// mutually exclusive, and a stale one left behind is what lets a gizmo drag
// something the panel is not showing.
func TestSelectingASpriteClearsOtherSelections(t *testing.T) {
	lvl := spriteTestLevel(t, world.Sprite{ID: "s", Width: 1, Height: 1, Visible: true})
	lvl.Volumes = []world.Volume{{ID: "v", HalfX: 1, HalfY: 1, HalfZ: 1, Enabled: true}}

	editorSelectVolume(0)
	editorSelectSprite(0)

	if activeVolume() != nil {
		t.Error("selecting a sprite left the volume selected")
	}
	if Editor.GizmoKind != gizmoKindSprite {
		t.Errorf("gizmo kind is %q, want %q", Editor.GizmoKind, gizmoKindSprite)
	}
}

// TestDuplicateOffsetsTheCopy — two quads in exactly the same place cannot be
// told apart or clicked apart, which is why the copy is moved.
func TestDuplicateOffsetsTheCopy(t *testing.T) {
	lvl := spriteTestLevel(t, world.Sprite{
		ID: "bush", Position: mgl32.Vec3{1, 2, 3}, Width: 2, Height: 2, Visible: true,
	})
	editorSelectSprite(0)
	duplicateActiveSprite()

	if len(lvl.Sprites) != 2 {
		t.Fatalf("got %d sprites, want 2", len(lvl.Sprites))
	}
	cp := lvl.Sprites[1]
	if cp.ID == "bush" {
		t.Error("the copy kept the original's ID; @bush would be ambiguous")
	}
	if cp.Position == lvl.Sprites[0].Position {
		t.Error("the copy landed exactly on the original")
	}
	if Editor.ActiveSpriteIdx != 1 {
		t.Errorf("the copy was not selected (idx %d)", Editor.ActiveSpriteIdx)
	}
}
