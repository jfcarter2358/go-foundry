package ai

// anim_displaced_test.go — a walking agent whose pose a script replaced gets
// its walk back; one a script is flinching lets the flinch finish.

import (
	"testing"

	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func displacedAnimEntity() *world.Entity {
	clip := func(name string, dur float32, loop bool) model.ModelClip {
		return model.ModelClip{
			Name: name, Duration: dur, FPS: 30, Loop: loop,
			Tracks: []model.ModelBoneTrack{{
				Bone: 0,
				Keys: []model.ModelBoneKey{
					{Time: 0, ModelBonePose: model.IdentBonePose()},
					{Time: dur, ModelBonePose: model.IdentBonePose()},
				},
			}},
		}
	}
	doc := &model.ModelDoc{
		Bones:   []model.ModelBone{{Name: "root", Parent: model.ModelBoneNoParent}},
		Verts:   []mgl32.Vec3{{0, 0, 0}},
		Weights: [][]model.ModelVertexWeight{{{Bone: 0, Weight: 1}}},
		Animations: []model.ModelClip{
			clip("walk", 1, true),
			clip("idle", 1, true),
			clip("hit", 0.3, false),
			clip("death", 0.5, false),
		},
	}
	return &world.Entity{ID: "a", Models: []world.EntityModel{{
		Name:       "body",
		Animations: []world.EntityAnimation{{Name: "walk"}, {Name: "idle"}, {Name: "hit"}, {Name: "death"}},
		Anim:       world.NewEntityAnim(doc),
	}}}
}

func TestWalkDisplacedByAScriptedPoseIsNoticed(t *testing.T) {
	e := displacedAnimEntity()
	e.PlayAnimation("", "walk")
	if animDisplaced(e, "walk") {
		t.Fatal("walk is playing; nothing displaced it")
	}
	// The combat script losing its target mid-follow.
	e.PlayAnimation("", "idle")
	if !animDisplaced(e, "walk") {
		t.Fatal("idle replaced the walk and was not noticed")
	}
}

func TestOneShotOverAWalkPlaysOut(t *testing.T) {
	e := displacedAnimEntity()
	e.PlayAnimation("", "walk")
	e.PlayAnimation("", "hit")
	if animDisplaced(e, "walk") {
		t.Fatal("a flinch in progress was cut short")
	}
	e.Models[0].Update(0.5, e.ID)
	if !animDisplaced(e, "walk") {
		t.Fatal("the flinch ended and the walk was not wanted back")
	}
}

// A script kills an agent that was walking: it switches the controller off and
// plays the death in the same breath. The controller's next tick must not stand
// the body back up in its idle — during the fall or once it lies still.
func TestSwitchingOffForADeathKeepsTheDeath(t *testing.T) {
	for _, settle := range []float32{0, 1} {
		e := displacedAnimEntity()
		e.AI = &world.AISpec{Enabled: false, IdleAnim: "idle", MoveAnim: "walk"}
		e.AIState = &world.AIState{Session: session, Mode: world.AIMoving, Anim: "walk"}
		e.PlayAnimation("", "walk")
		e.PlayAnimation("", "death")
		e.Models[0].Update(settle, e.ID)
		tick(&world.Level{}, e, 0, 1.0/60, nil)
		if !animShowing(e, "death") {
			t.Fatalf("settle %v: the death was replaced by %q", settle, e.Models[0].Playing())
		}
	}
}

// Switched off while nothing but its own walk was showing, it still stops the
// walk — that pose is the controller's to take back.
func TestSwitchingOffAWalkingAgentStandsItStill(t *testing.T) {
	e := displacedAnimEntity()
	e.AI = &world.AISpec{Enabled: false, IdleAnim: "idle", MoveAnim: "walk"}
	e.AIState = &world.AIState{Session: session, Mode: world.AIMoving, Anim: "walk"}
	e.PlayAnimation("", "walk")
	tick(&world.Level{}, e, 0, 1.0/60, nil)
	if got := e.Models[0].Playing(); got != "idle" {
		t.Fatalf("a switched-off walker shows %q, want idle", got)
	}
}
