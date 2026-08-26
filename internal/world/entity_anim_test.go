package world

// entity_anim_test.go — the crossfade SetClip runs between two clips.
//
// Both clips here are keyed with a single key at t=0, which — per
// ModelBoneTrack.sample — holds that value at every time, positive or
// negative: what is under test is the blend arithmetic, not clip sampling, so
// the fixture is built to make the target pose independent of the clock.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/model"
	"foundry/internal/modeltest"
)

// vec3Close reports whether a and b agree on every axis to within eps.
func vec3Close(a, b mgl32.Vec3, eps float32) bool {
	return mgl32.Abs(a.X()-b.X()) <= eps && mgl32.Abs(a.Y()-b.Y()) <= eps && mgl32.Abs(a.Z()-b.Z()) <= eps
}

// animTestDoc returns a skinned single-bone cube carrying three named clips,
// each translating the bone to a distinct, constant place.
func animTestDoc(t *testing.T) (*model.ModelDoc, int) {
	t.Helper()
	doc, bi := modeltest.SkinnedCube(t)
	keyTrans(doc, bi, "a", mgl32.Vec3{1, 0, 0})
	keyTrans(doc, bi, "b", mgl32.Vec3{0, 0, 5})
	keyTrans(doc, bi, "c", mgl32.Vec3{0, 10, 0})
	return doc, bi
}

func keyTrans(doc *model.ModelDoc, bi int, clip string, trans mgl32.Vec3) {
	c := doc.Clip(doc.AddClip(clip))
	c.SetKey(bi, 0, model.ModelBonePose{Trans: trans, Rot: mgl32.QuatIdent(), Scale: mgl32.Vec3{1, 1, 1}})
}

func TestSetClipDoesNotBlendFromRest(t *testing.T) {
	doc, bi := animTestDoc(t)
	a := NewEntityAnim(doc)
	if a == nil {
		t.Fatal("NewEntityAnim refused a rigged, skinned, keyed model")
	}
	// Nothing was playing, so even a positive blend has nothing to fade from —
	// the first clip on a fresh entity should appear immediately.
	if !a.SetClip("a", 0.2) {
		t.Fatal("SetClip(a) failed")
	}
	if a.blending {
		t.Error("the first clip on an unposed entity started a crossfade")
	}
	if got := a.pose.Bones[bi].Trans; !vec3Close(got, mgl32.Vec3{1, 0, 0}, 1e-5) {
		t.Errorf("pose = %v, want clip a's pose", got)
	}
}

func TestSetClipCrossfadesBetweenTwoClips(t *testing.T) {
	doc, bi := animTestDoc(t)
	a := NewEntityAnim(doc)
	a.SetClip("a", 0.2)

	if !a.SetClip("b", 0.2) {
		t.Fatal("SetClip(b) failed")
	}
	if !a.blending {
		t.Fatal("switching clips with a positive blend did not start a crossfade")
	}
	// At the instant of the switch the crossfade has run for none of its
	// duration, so what is on screen must still read as clip a.
	if got := a.blended.Bones[bi].Trans; !vec3Close(got, mgl32.Vec3{1, 0, 0}, 1e-5) {
		t.Errorf("pose at the switch = %v, want clip a's pose unchanged", got)
	}

	a.Update(0.1) // halfway through the 0.2s crossfade
	if !a.blending {
		t.Error("the crossfade ended before its duration")
	}
	if got := a.blended.Bones[bi].Trans; !vec3Close(got, mgl32.Vec3{0.5, 0, 2.5}, 1e-4) {
		t.Errorf("pose halfway through the crossfade = %v, want the midpoint of a and b", got)
	}

	a.Update(0.2) // past the end
	if a.blending {
		t.Error("the crossfade did not end after its duration elapsed")
	}
	if got := a.pose.Bones[bi].Trans; !vec3Close(got, mgl32.Vec3{0, 0, 5}, 1e-5) {
		t.Errorf("settled pose = %v, want clip b's pose", got)
	}
}

// TestSetClipInterruptingABlendDepartsFromTheShownPose is the point of
// snapshotting currentPose() rather than the outgoing clip's own sample:
// switching again mid-fade should bend toward the new target from wherever
// the eye actually is, not snap back to the pose the first switch started
// from.
func TestSetClipInterruptingABlendDepartsFromTheShownPose(t *testing.T) {
	doc, bi := animTestDoc(t)
	a := NewEntityAnim(doc)
	a.SetClip("a", 0.2)
	a.SetClip("b", 0.2)
	a.Update(0.1) // halfway: on screen right now is the midpoint of a and b
	shown := a.blended.Bones[bi].Trans

	if !a.SetClip("c", 0.2) {
		t.Fatal("SetClip(c) failed")
	}
	if got := a.blendFrom.Bones[bi].Trans; !vec3Close(got, shown, 1e-5) {
		t.Errorf("the new crossfade departed from %v, want the pose actually on screen, %v", got, shown)
	}
}

func TestSetClipWithZeroBlendIsAnInstantCut(t *testing.T) {
	doc, bi := animTestDoc(t)
	a := NewEntityAnim(doc)
	a.SetClip("a", 0.2)

	if !a.SetClip("b", 0) {
		t.Fatal("SetClip(b, 0) failed")
	}
	if a.blending {
		t.Error("an explicit zero blend still started a crossfade")
	}
	if got := a.pose.Bones[bi].Trans; !vec3Close(got, mgl32.Vec3{0, 0, 5}, 1e-5) {
		t.Errorf("pose after a hard cut = %v, want clip b's pose immediately", got)
	}
}

// TestSetClipStopBypassesTheCrossfade pins the documented exception: an empty
// name always cuts to rest, even mid-blend, because there is no clip left to
// keep sampling a "from" pose out of on every later frame.
func TestSetClipStopBypassesTheCrossfade(t *testing.T) {
	doc, _ := animTestDoc(t)
	a := NewEntityAnim(doc)
	a.SetClip("a", 0.2)
	a.SetClip("b", 0.2)

	if !a.SetClip("", 0.2) {
		t.Fatal("SetClip(\"\") failed")
	}
	if a.blending || a.Playing || a.Clip != -1 {
		t.Errorf("stop did not cut immediately: blending=%v playing=%v clip=%d", a.blending, a.Playing, a.Clip)
	}
	for i, m := range a.Palette {
		if m != mgl32.Ident4() {
			t.Errorf("bone %d palette entry after stop = %v, want identity", i, m)
		}
	}
}
