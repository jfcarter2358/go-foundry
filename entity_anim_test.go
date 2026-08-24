package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// animatedDoc returns a rigged, weighted, one-clip document — the shape a
// runtime entity is expected to load.
func animatedDoc(t *testing.T) (*ModelDoc, [3]int) {
	t.Helper()
	m, idx := boneChain(t)
	m.AssignVertsToBone(modelAllVerts(m), idx[0], 1)
	c := m.Clip(m.AddClip("walk"))
	c.Duration, c.FPS, c.Loop = 1, 24, true
	c.SetKey(idx[0], 0, identBonePose())
	c.SetKey(idx[0], 1, ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(90), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})
	return m, idx
}

func TestNewEntityAnimSkipsModelsWithNothingToPlay(t *testing.T) {
	if a := newEntityAnim(nil); a != nil {
		t.Error("a nil document produced playback state")
	}
	if a := newEntityAnim(NewCubeDoc()); a != nil {
		t.Error("an unrigged cube produced playback state")
	}

	// Bones and weights but no clips: still nothing to play.
	m, idx := boneChain(t)
	m.AssignVertsToBone(modelAllVerts(m), idx[0], 1)
	if a := newEntityAnim(m); a != nil {
		t.Error("a rigged but unanimated model produced playback state")
	}

	// A clip with no keys does not count either.
	m.AddClip("empty")
	if a := newEntityAnim(m); a != nil {
		t.Error("a keyless clip produced playback state")
	}
}

func TestEntityAnimStartsAtRest(t *testing.T) {
	doc, _ := animatedDoc(t)
	a := newEntityAnim(doc)
	if a == nil {
		t.Fatal("an animated model produced no playback state")
	}
	if len(a.Palette) != len(doc.Bones) {
		t.Fatalf("palette has %d entries for %d bones", len(a.Palette), len(doc.Bones))
	}
	for i, m := range a.Palette {
		if !mat4Close(m, mgl32.Ident4(), 1e-6) {
			t.Errorf("bone %d starts at %v, want the identity", i, m)
		}
	}
	if a.Playing {
		t.Error("playback started without a clip being selected")
	}
}

func TestEntityAnimSetClipAndAdvance(t *testing.T) {
	doc, idx := animatedDoc(t)
	a := newEntityAnim(doc)
	if !a.SetClip("walk") {
		t.Fatal("SetClip refused a clip the model has")
	}
	if a.SetClip("nope") {
		t.Error("SetClip accepted a clip the model does not have")
	}
	if !a.SetClip("walk") {
		t.Fatal("SetClip refused a clip the model has")
	}

	a.Update(0.5)
	if mat4Close(a.Palette[idx[0]], mgl32.Ident4(), 1e-5) {
		t.Error("the palette did not move half a second into the clip")
	}

	// And it must agree with what the editor would show at the same time.
	pose := newModelPose(len(doc.Bones))
	doc.Animations[0].SampleInto(&pose, len(doc.Bones), a.Time)
	want := doc.PoseDeltas(pose)
	for i := range want {
		if !mat4Close(a.Palette[i], want[i], 1e-5) {
			t.Fatalf("bone %d: runtime palette disagrees with the editor's pose\ngot  %v\nwant %v",
				i, a.Palette[i], want[i])
		}
	}
}

func TestEntityAnimLoops(t *testing.T) {
	doc, _ := animatedDoc(t)
	a := newEntityAnim(doc)
	a.SetClip("walk")
	a.Loop = true
	a.Update(2.5) // well past the one-second clip
	if !a.Playing {
		t.Error("a looping clip stopped")
	}
	if a.Time < 0 || a.Time >= 1 {
		t.Errorf("looping clock is %v, want it wrapped into [0, 1)", a.Time)
	}
}

func TestEntityAnimOneShotStops(t *testing.T) {
	doc, _ := animatedDoc(t)
	a := newEntityAnim(doc)
	a.SetClip("walk")
	a.Loop = false
	a.Update(5)
	if a.Playing {
		t.Error("a one-shot kept playing past its end")
	}
	if a.Time != doc.Animations[0].duration() {
		t.Errorf("one-shot settled at %v, want %v", a.Time, doc.Animations[0].duration())
	}
}

func TestEntityAnimSpeedScalesTheClock(t *testing.T) {
	doc, _ := animatedDoc(t)
	a := newEntityAnim(doc)
	a.SetClip("walk")
	a.Loop = false
	a.Speed = 2
	a.Update(0.25)
	if mgl32.Abs(a.Time-0.5) > 1e-5 {
		t.Errorf("at 2x, 0.25s of wall time advanced the clock to %v, want 0.5", a.Time)
	}
}

func TestSetClipToEmptyReturnsToRest(t *testing.T) {
	doc, _ := animatedDoc(t)
	a := newEntityAnim(doc)
	a.SetClip("walk")
	a.Update(0.5)
	if !a.SetClip("") {
		t.Fatal("clearing the clip failed")
	}
	if a.Playing {
		t.Error("clearing the clip left playback running")
	}
	for i, m := range a.Palette {
		if !mat4Close(m, mgl32.Ident4(), 1e-6) {
			t.Errorf("bone %d did not return to rest: %v", i, m)
		}
	}
}

// TestUpdateEntityAnimationsToleratesStaticEntities is the guard on the
// main-loop tick: most entities in a level have no Anim at all.
func TestUpdateEntityAnimationsToleratesStaticEntities(t *testing.T) {
	doc, _ := animatedDoc(t)
	lvl := &Level{Entities: []Entity{
		{ID: "static"},
		{ID: "animated", Anim: newEntityAnim(doc)},
	}}
	lvl.Entities[1].Anim.SetClip("walk")

	UpdateEntityAnimations(lvl, 0.1)
	UpdateEntityAnimations(nil, 0.1)

	if lvl.Entities[1].Anim.Time == 0 {
		t.Error("the animated entity did not advance")
	}
}
