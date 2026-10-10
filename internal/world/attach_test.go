package world

// attach_test.go — resolving the "ride this" binding.
//
// Four cases, cheapest first, and the two that matter most are the ones where
// carriers compose: an entity that is itself riding a frame, and a bone inside
// that entity's model. Getting either wrong puts a torch at the right numbers
// in the wrong space, which looks like a placement mistake rather than a bug.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// attachLevel builds a level with a ship (a frame carrier) and an NPC aboard it.
func attachLevel(t *testing.T) *Level {
	t.Helper()
	lvl := &Level{
		// A frame exists because a chunk names its owner — a vehicle is an
		// entity with geometry parented to it, not merely an entity with a
		// VehicleSpec. See BuildFrames.
		Chunks: []Chunk{{ID: "hull", FrameOwner: "ship"}},
		Entities: []Entity{
			{
				ID: "ship", Position: mgl32.Vec3{100, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
				Vehicle: &VehicleSpec{},
			},
			{
				// Aboard the ship, ten units forward of its origin.
				ID: "npc", Position: mgl32.Vec3{10, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
				Frame: "ship",
			},
		},
	}
	fs, err := BuildFrames(lvl)
	if err != nil {
		t.Fatalf("BuildFrames: %v", err)
	}
	lvl.Frames = fs
	RefreshFrames(lvl, lvl.Frames)
	return lvl
}

// translationOf pulls the position out of a transform.
func translationOf(m mgl32.Mat4) mgl32.Vec3 { return mgl32.Vec3{m[12], m[13], m[14]} }

func TestAttachNothingIsIdentity(t *testing.T) {
	lvl := attachLevel(t)
	if got := (Attach{}).Matrix(lvl); got != mgl32.Ident4() {
		t.Errorf("an empty Attach resolved to %v, want the identity", got)
	}
	if !(Attach{}).IsZero() {
		t.Error("an empty Attach does not report itself as zero")
	}
}

func TestAttachToAFrame(t *testing.T) {
	lvl := attachLevel(t)
	got := translationOf(Attach{Frame: "ship"}.Matrix(lvl))
	if got != (mgl32.Vec3{100, 0, 0}) {
		t.Errorf("frame attach resolved to %v, want the ship at {100 0 0}", got)
	}
}

// TestAttachToAnEntityComposesItsFrame is the case that is easy to get wrong.
// The NPC's own Position is expressed in the ship's space, so resolving it
// without composing the ship would put a torch at {10 0 0} in the world —
// nowhere near the hand holding it.
func TestAttachToAnEntityComposesItsFrame(t *testing.T) {
	lvl := attachLevel(t)
	got := translationOf(Attach{Entity: "npc"}.Matrix(lvl))
	want := mgl32.Vec3{110, 0, 0} // the ship at 100, plus the NPC's 10 aboard it
	if got != want {
		t.Errorf("entity attach resolved to %v, want %v — the carrier's own frame was not composed", got, want)
	}
}

// TestAttachSurvivesAMissingCarrier — deleting the entity a sprite was riding
// should leave the sprite somewhere, not fail.
func TestAttachSurvivesAMissingCarrier(t *testing.T) {
	lvl := attachLevel(t)
	for _, a := range []Attach{
		{Entity: "gone"},
		{Frame: "gone"},
	} {
		if got := a.Matrix(lvl); got != mgl32.Ident4() {
			t.Errorf("%+v resolved to %v; a missing carrier should read as the world", a, got)
		}
	}
	// A missing BONE is different: the entity carrying it is still there, so
	// the binding falls back to the entity rather than all the way to the
	// world. A torch on a renamed bone should end up on the NPC, not at the
	// origin.
	fallback := Attach{Entity: "npc", Model: "gone", Bone: "gone"}.Matrix(lvl)
	if got := translationOf(fallback); got != (mgl32.Vec3{110, 0, 0}) {
		t.Errorf("a missing bone resolved to %v; it should fall back to the entity at {110 0 0}", got)
	}
	if (Attach{Entity: "gone"}).Matrix(nil) != mgl32.Ident4() {
		t.Error("a nil level should resolve to the identity rather than panic")
	}
}

// TestAttachMovesWithItsCarrier — the whole point of the binding.
func TestAttachMovesWithItsCarrier(t *testing.T) {
	lvl := attachLevel(t)
	before := translationOf(Attach{Entity: "npc"}.Matrix(lvl))

	// Sail the ship.
	FindEntity(lvl, "ship").Position = mgl32.Vec3{200, 0, 0}
	RefreshFrames(lvl, lvl.Frames)

	after := translationOf(Attach{Entity: "npc"}.Matrix(lvl))
	if after.Sub(before) != (mgl32.Vec3{100, 0, 0}) {
		t.Errorf("moving the ship 100 along X moved the attachment by %v", after.Sub(before))
	}
}

// TestAttachDescribeNamesTheBinding — the panel and the Explorer row both read
// this, so it has to be right for every shape of binding.
func TestAttachDescribe(t *testing.T) {
	for _, c := range []struct {
		a    Attach
		want string
	}{
		{Attach{}, ""},
		{Attach{Frame: "ship"}, "frame ship"},
		{Attach{Entity: "npc"}, "npc"},
		{Attach{Entity: "npc", Model: "body", Bone: "hand_r"}, "npc.body.hand_r"},
	} {
		if got := c.a.Describe(); got != c.want {
			t.Errorf("%+v described as %q, want %q", c.a, got, c.want)
		}
	}
}

// TestSpriteWorldPositionGoesThroughItsAttachment — the sprite's authored
// position is local when it is attached, and the renderer and the picker both
// read it through here.
func TestSpriteWorldPositionGoesThroughItsAttachment(t *testing.T) {
	lvl := attachLevel(t)
	s := NewSprite("torch", mgl32.Vec3{0, 2, 0})
	s.Attach = Attach{Entity: "npc"}

	got := s.WorldPosition(lvl)
	want := mgl32.Vec3{110, 2, 0}
	if got != want {
		t.Errorf("attached sprite is at %v, want %v", got, want)
	}

	// Unattached, the authored position is world-space untouched.
	s.Attach = Attach{}
	if got := s.WorldPosition(lvl); got != (mgl32.Vec3{0, 2, 0}) {
		t.Errorf("unattached sprite moved to %v", got)
	}
}

// TestEmitterOriginGoesThroughItsAttachment, and carries its own rotation.
func TestEmitterOriginGoesThroughItsAttachment(t *testing.T) {
	lvl := attachLevel(t)
	e := NewEmitter("exhaust", mgl32.Vec3{0, 1, 0})
	e.Attach = Attach{Frame: "ship"}

	got := translationOf(e.OriginMatrix(lvl))
	want := mgl32.Vec3{100, 1, 0}
	if got != want {
		t.Errorf("attached emitter origin is at %v, want %v", got, want)
	}
}
