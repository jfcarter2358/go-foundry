package collision

// step_anim_test.go — the clips that ride along with a step (entity_collision.go's
// triggerStepAnim, entity_update.go's noteLanding), as opposed to step_ease_test.go's
// StepOffset, which is purely visual and needs no clip at all.
//
// The fixtures here carry a synthetic "body" attachment with step_up/step_down
// clips baked from a one-bone, one-vertex rig — not human.fmdl, which another
// change is in the middle of teaching those clips to. EntityModel.Play returns
// false harmlessly against a model with no matching animation (entity_model.go),
// so these tests exercise the trigger logic on its own terms rather than
// depending on that content landing first.

import (
	"testing"

	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// stepAnimDoc is a minimal rigged, animated document: one bone, one vertex
// bound to it, and two one-shot clips named step_up and step_down — enough
// for NewEntityAnim to accept it and for SetClip to resolve either name.
func stepAnimDoc() *model.ModelDoc {
	oneShot := func(name string, dur float32) model.ModelClip {
		return model.ModelClip{
			Name: name, Duration: dur, FPS: 30,
			Tracks: []model.ModelBoneTrack{{
				Bone: 0,
				Keys: []model.ModelBoneKey{
					{Time: 0, ModelBonePose: model.IdentBonePose()},
					{Time: dur, ModelBonePose: model.IdentBonePose()},
				},
			}},
		}
	}
	return &model.ModelDoc{
		Bones:   []model.ModelBone{{Name: "root", Parent: model.ModelBoneNoParent}},
		Verts:   []mgl32.Vec3{{0, 0, 0}},
		Weights: [][]model.ModelVertexWeight{{{Bone: 0, Weight: 1}}},
		Animations: []model.ModelClip{
			oneShot("step_up", 0.4),
			oneShot("step_down", 0.3),
		},
	}
}

// stepAnimWalker is easeWalker with a "body" attachment carrying the step_up/
// step_down clips, so the trigger side (PlayAnimation) can be observed through
// EntityModel.Playing alongside StepOffset.
func stepAnimWalker(id string, feet mgl32.Vec3) world.Entity {
	e := easeWalker(id, feet)
	e.Models = []world.EntityModel{{
		Name: "body",
		Animations: []world.EntityAnimation{
			{Name: "step_up"},
			{Name: "step_down"},
		},
		Anim: world.NewEntityAnim(stepAnimDoc()),
	}}
	return e
}

// ---------- step_up ----------

// Walking onto a real sill — the same 0.5m riser step_ease_test.go eases —
// also plays step_up on the body attachment.
func TestSteppingOntoASillPlaysStepUp(t *testing.T) {
	lvl := stepRoom(4, block(0.5))
	lvl.Entities = []world.Entity{stepAnimWalker("w", mgl32.Vec3{0, 0.1, 0})}
	w := &lvl.Entities[0]
	for i := 0; i < 30; i++ {
		EntityUpdate(lvl, easeDT, true, nil)
	}

	played := false
	for i := 0; i < 120 && !played; i++ {
		w.Velocity[0] = 2
		EntityUpdate(lvl, easeDT, true, nil)
		if w.Model("body").Playing() == "step_up" {
			played = true
		}
	}
	if !played {
		t.Fatal("stepping onto a 0.5m sill never played step_up")
	}
}

// A curb well under stepAnimLiftThreshold still eases StepOffset (step_ease_test.go
// covers that) but must not spend a whole clip on something this small.
func TestATinyLiftDoesNotPlayStepUp(t *testing.T) {
	const curb = float32(0.08) // above stepLiftSlack, below stepAnimLiftThreshold
	lvl := stepRoom(4, block(curb))
	lvl.Entities = []world.Entity{stepAnimWalker("w", mgl32.Vec3{0, 0.1, 0})}
	w := &lvl.Entities[0]
	for i := 0; i < 30; i++ {
		EntityUpdate(lvl, easeDT, true, nil)
	}

	for i := 0; i < 120; i++ {
		w.Velocity[0] = 2
		EntityUpdate(lvl, easeDT, true, nil)
		if w.Model("body").Playing() == "step_up" {
			t.Fatalf("tick %d: a %vm curb played step_up (threshold is %v)", i, curb, stepAnimLiftThreshold)
		}
	}
}

// A second lift landing while step_up is still showing — a staircase taken
// tread by tread, each one its own NoteStepLift call — must not restart the
// clip from the top.
func TestStepUpAnimNotRetriggeredWhilePlaying(t *testing.T) {
	e := stepAnimWalker("w", mgl32.Vec3{})
	triggerStepAnim(&e, "step_up")
	m := e.Model("body")
	if m.Playing() != "step_up" {
		t.Fatalf("triggerStepAnim did not start step_up (playing %q)", m.Playing())
	}
	m.Anim.Update(0.1)
	if m.Anim.Time == 0 {
		t.Fatal("test setup: the clip did not advance")
	}
	elapsed := m.Anim.Time

	triggerStepAnim(&e, "step_up") // the next tread's lift, same stride
	if m.Anim.Time != elapsed {
		t.Errorf("a second trigger mid-clip restarted it: time %v -> %v", elapsed, m.Anim.Time)
	}
}

// ---------- step_down ----------

// A short drop — a low ledge, not a fall — plays step_down once it lands.
func TestASmallDropPlaysStepDown(t *testing.T) {
	lvl := stepRoom(4)
	lvl.Entities = []world.Entity{stepAnimWalker("w", mgl32.Vec3{0, 0.3, 0})}
	w := &lvl.Entities[0]

	played := false
	for i := 0; i < 120 && !played; i++ {
		EntityUpdate(lvl, easeDT, true, nil)
		if w.Model("body").Playing() == "step_down" {
			played = true
		}
	}
	if !played {
		t.Fatalf("a 0.3m drop never played step_down; grounded=%v pos=%v", w.Collision.OnGround, w.Position)
	}
}

// A real fall — well past stepDownMaxDrop — is left silent: it is not a
// stride down a ledge, and a future "hard landing" clip is out of scope here.
func TestALongFallDoesNotPlayStepDown(t *testing.T) {
	lvl := stepRoom(10)
	lvl.Entities = []world.Entity{stepAnimWalker("w", mgl32.Vec3{0, 6, 0})}
	w := &lvl.Entities[0]

	landed := false
	for i := 0; i < 200; i++ {
		EntityUpdate(lvl, easeDT, true, nil)
		if w.Model("body").Playing() == "step_down" {
			t.Fatalf("tick %d: a 6m fall played step_down", i)
		}
		if w.Collision.OnGround {
			landed = true
			break
		}
	}
	if !landed {
		t.Fatal("the walker never landed; the guard was not actually exercised")
	}
}

// noteLanding's own guards: a seated body (riding a vehicle or a seat of any
// kind) and an entity with no AI controller (not a walker this animation is
// for) must never play step_down, however far they fell.
func TestNoteLandingIgnoresSeatedAndNonWalkerEntities(t *testing.T) {
	const midBandSpeed = float32(6) // drop = 6^2/(2*36) = 0.5m, well inside the band

	seated := stepAnimWalker("w", mgl32.Vec3{})
	seated.Seat = "vehicle"
	noteLanding(&seated, midBandSpeed)
	if seated.Model("body").Playing() == "step_down" {
		t.Error("a seated entity played step_down on landing")
	}

	noAI := stepAnimWalker("w2", mgl32.Vec3{})
	noAI.AI = nil
	noteLanding(&noAI, midBandSpeed)
	if noAI.Model("body").Playing() == "step_down" {
		t.Error("a non-AI-driven entity played step_down on landing")
	}
}
