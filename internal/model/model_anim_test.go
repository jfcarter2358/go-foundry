package model

import (
	"foundry/internal/gfx"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// clipChain returns a three-bone chain plus a one-second clip that swings the
// root from rest to a quarter turn.
func clipChain(t *testing.T) (*ModelDoc, [3]int, *ModelClip) {
	t.Helper()
	m, idx := boneChain(t)
	ci := m.AddClip("walk")
	c := m.Clip(ci)
	c.SetKey(idx[0], 0, IdentBonePose())
	c.SetKey(idx[0], 1, ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(90), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})
	return m, idx, c
}

func TestClipSampleHoldsTheEndKeys(t *testing.T) {
	m, idx, c := clipChain(t)
	pose := NewModelPose(len(m.Bones))

	// Before the first key and after the last, the track holds rather than
	// extrapolating — a clip sampled a hair past its end settles on its final
	// pose instead of swinging on.
	c.SampleInto(&pose, len(m.Bones), -5)
	if !pose.Bones[idx[0]].IsIdentity() {
		t.Error("sampling before the first key did not hold it")
	}
	c.SampleInto(&pose, len(m.Bones), 99)
	end := pose.Bones[idx[0]]
	c.SampleInto(&pose, len(m.Bones), 1)
	if !vec3Close(end.Rot.V, pose.Bones[idx[0]].Rot.V, 1e-5) {
		t.Error("sampling past the last key did not hold it")
	}
}

func TestClipSampleHitsKeysExactly(t *testing.T) {
	m, idx, c := clipChain(t)
	pose := NewModelPose(len(m.Bones))

	c.SampleInto(&pose, len(m.Bones), 0)
	if !pose.Bones[idx[0]].IsIdentity() {
		t.Errorf("t=0 gave %+v, want the rest pose", pose.Bones[idx[0]])
	}
	c.SampleInto(&pose, len(m.Bones), 1)
	want := mgl32.QuatRotate(mgl32.DegToRad(90), mgl32.Vec3{1, 0, 0})
	if !vec3Close(pose.Bones[idx[0]].Rot.V, want.V, 1e-5) {
		t.Errorf("t=1 rotation = %v, want %v", pose.Bones[idx[0]].Rot.V, want.V)
	}
}

func TestClipSampleSlerpsHalfway(t *testing.T) {
	m, idx, c := clipChain(t)
	pose := NewModelPose(len(m.Bones))
	c.SampleInto(&pose, len(m.Bones), 0.5)

	got := pose.Bones[idx[0]].EffectiveRot()
	if l := got.Len(); mgl32.Abs(l-1) > 1e-5 {
		t.Errorf("midpoint quaternion has length %v, wanted unit — a non-unit rotation scales the bone", l)
	}
	// Halfway through a 90-degree swing is 45 degrees, not the average of the
	// two endpoints. This is the check that says slerp, not lerp.
	want := mgl32.QuatRotate(mgl32.DegToRad(45), mgl32.Vec3{1, 0, 0})
	if !vec3Close(got.V, want.V, 1e-4) || mgl32.Abs(got.W-want.W) > 1e-4 {
		t.Errorf("midpoint rotation = %v/%v, want %v/%v", got.V, got.W, want.V, want.W)
	}

	// And the bone must not have shortened on the way, which is what lerping
	// posed endpoints instead of rotations would do.
	posed := m.PosedBones(m.PoseDeltas(pose))
	if l := posed[idx[0]].Tail.Sub(posed[idx[0]].Head).Len(); mgl32.Abs(l-1) > 1e-5 {
		t.Errorf("bone length mid-swing = %v, want 1", l)
	}
}

func TestClipSampleLeavesUnkeyedBonesAtRest(t *testing.T) {
	m, idx, c := clipChain(t)
	pose := NewModelPose(len(m.Bones))
	// Dirty the pose first, so a sample that forgot to reset would show it.
	pose.Bones[idx[2]].Trans = mgl32.Vec3{9, 9, 9}
	c.SampleInto(&pose, len(m.Bones), 0.5)
	if !pose.Bones[idx[2]].IsIdentity() {
		t.Errorf("an unkeyed bone kept a stale pose: %+v", pose.Bones[idx[2]])
	}
}

func TestSetKeyReplacesWithinEpsilonAndStaysSorted(t *testing.T) {
	m, idx, c := clipChain(t)
	tr := c.TrackFor(idx[0])
	if len(tr.Keys) != 2 {
		t.Fatalf("track has %d keys, want 2", len(tr.Keys))
	}

	// Re-keying the frame the playhead is parked on replaces rather than stacks.
	c.SetKey(idx[0], ModelKeyEpsilon/2, ModelBonePose{Trans: mgl32.Vec3{1, 0, 0}, Scale: mgl32.Vec3{1, 1, 1}})
	tr = c.TrackFor(idx[0])
	if len(tr.Keys) != 2 {
		t.Errorf("re-keying within epsilon stacked a key: %d keys", len(tr.Keys))
	}

	c.SetKey(idx[0], 0.5, IdentBonePose())
	tr = c.TrackFor(idx[0])
	if len(tr.Keys) != 3 {
		t.Fatalf("insert gave %d keys, want 3", len(tr.Keys))
	}
	for i := 1; i < len(tr.Keys); i++ {
		if tr.Keys[i].Time < tr.Keys[i-1].Time {
			t.Errorf("keys out of order at %d: %v after %v", i, tr.Keys[i].Time, tr.Keys[i-1].Time)
		}
	}
	_ = m
}

func TestDeleteKeyDropsAnEmptiedTrack(t *testing.T) {
	_, idx, c := clipChain(t)
	if !c.DeleteKey(idx[0], 0) || !c.DeleteKey(idx[0], 1) {
		t.Fatal("DeleteKey refused a key that exists")
	}
	if c.TrackFor(idx[0]) != nil {
		t.Error("an emptied track was left behind; it would show as a blank timeline lane")
	}
	if c.DeleteKey(idx[0], 0) {
		t.Error("DeleteKey reported success on a track that is gone")
	}
}

// TestDeleteBonesRemapsAnimationTracks is the invariant model_anim.go's header
// calls out: a track left pointing at a deleted bone drives the wrong limb.
func TestDeleteBonesRemapsAnimationTracks(t *testing.T) {
	m, idx := boneChain(t)
	ci := m.AddClip("walk")
	c := m.Clip(ci)
	c.SetKey(idx[1], 0, IdentBonePose())
	c.SetKey(idx[2], 0, IdentBonePose())
	tailName := m.Bones[idx[2]].Name

	// Delete the root; the other two shuffle down by one.
	m.DeleteBones([]int{idx[0]})

	c = m.Clip(ci)
	want := m.BoneIndexByName(tailName)
	if want < 0 {
		t.Fatal("the tail bone did not survive the delete")
	}
	found := false
	for _, tr := range c.Tracks {
		if tr.Bone < 0 || tr.Bone >= len(m.Bones) {
			t.Fatalf("track points at bone %d, but only %d remain", tr.Bone, len(m.Bones))
		}
		if tr.Bone == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the tail bone's track was not reindexed to %d", want)
	}
}

func TestDeleteBonesDropsTracksForDeletedBones(t *testing.T) {
	m, idx := boneChain(t)
	c := m.Clip(m.AddClip("walk"))
	c.SetKey(idx[0], 0, IdentBonePose())

	m.DeleteBones([]int{idx[0]})
	if n := len(m.Clip(0).Tracks); n != 0 {
		t.Errorf("a track survived its bone's deletion: %d tracks left", n)
	}
}

func TestMergeShiftsAnimationTracks(t *testing.T) {
	dst, _ := boneChain(t)
	src, sidx := boneChain(t)
	c := src.Clip(src.AddClip("walk"))
	c.SetKey(sidx[0], 0, IdentBonePose())

	boneBase := len(dst.Bones)
	dst.Merge(src, mgl32.Vec3{5, 0, 0})

	if len(dst.Animations) != 1 {
		t.Fatalf("merged document has %d clips, want 1", len(dst.Animations))
	}
	tr := dst.Animations[0].Tracks
	if len(tr) != 1 {
		t.Fatalf("merged clip has %d tracks, want 1", len(tr))
	}
	if want := boneBase + sidx[0]; tr[0].Bone != want {
		t.Errorf("merged track drives bone %d, want %d — it would animate the wrong armature", tr[0].Bone, want)
	}
}

func TestClipNamesAreUniquified(t *testing.T) {
	m := NewCubeDoc()
	m.AddClip("walk")
	if i := m.AddClip("walk"); m.Animations[i].Name == "walk" {
		t.Error("a second clip took the same name; the level file addresses clips by name")
	}
}

// ---------- serialization ----------

func TestFmdlRoundTripsAnimations(t *testing.T) {
	m, idx, _ := clipChain(t)
	m.Animations[0].Loop = true
	m.Animations[0].FPS = 30
	m.AssignVertsToBone(modelAllVerts(m), idx[0], 1)

	path := filepath.Join(t.TempDir(), "anim"+ModelExt)
	if err := SaveModel(m, path); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}
	got, err := LoadModel(path)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	if len(got.Animations) != 1 {
		t.Fatalf("loaded %d clips, want 1", len(got.Animations))
	}
	c := &got.Animations[0]
	if c.Name != "walk" || c.FPS != 30 || !c.Loop {
		t.Errorf("clip metadata lost: %+v", ModelClip{Name: c.Name, Duration: c.Duration, FPS: c.FPS, Loop: c.Loop})
	}
	if c.EffectiveDuration() != 1 {
		t.Errorf("duration = %v, want 1", c.EffectiveDuration())
	}

	// The track must have come back bound to the same bone, resolved by name.
	tr := c.TrackFor(got.BoneIndexByName(m.Bones[idx[0]].Name))
	if tr == nil {
		t.Fatal("the track did not survive the round trip")
	}
	if len(tr.Keys) != 2 {
		t.Fatalf("loaded %d keys, want 2", len(tr.Keys))
	}

	// And sampling the loaded clip must agree with the original.
	before, after := NewModelPose(len(m.Bones)), NewModelPose(len(got.Bones))
	m.Animations[0].SampleInto(&before, len(m.Bones), 0.5)
	c.SampleInto(&after, len(got.Bones), 0.5)
	a, b := before.Bones[idx[0]].EffectiveRot(), after.Bones[idx[0]].EffectiveRot()
	if !vec3Close(a.V, b.V, 1e-5) || mgl32.Abs(a.W-b.W) > 1e-5 {
		t.Errorf("sampled rotation changed across the round trip: %v/%v -> %v/%v", a.V, a.W, b.V, b.W)
	}
}

// TestFmdlOmitsRestChannels keeps the format readable: a clip that only rotates
// should not write a vector of zeroes and a vector of ones on every key.
func TestFmdlOmitsRestChannels(t *testing.T) {
	m, _, _ := clipChain(t)
	path := filepath.Join(t.TempDir(), "anim"+ModelExt)
	if err := SaveModel(m, path); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		field := strings.TrimSpace(line)
		if field == "p:" || strings.HasPrefix(field, "p: ") {
			t.Errorf("saved file writes a rest translation:\n%s", body)
		}
		if field == "s:" || strings.HasPrefix(field, "s: ") {
			t.Errorf("saved file writes a rest scale:\n%s", body)
		}
	}
	// The rotation is the one channel this clip actually uses, so it must be
	// there — otherwise the checks above would pass on an empty file.
	if !strings.Contains(string(body), "r:") {
		t.Errorf("the rotation channel was omitted too:\n%s", body)
	}
}

func TestLoadRejectsATrackNamingAMissingBone(t *testing.T) {
	m, _, _ := clipChain(t)
	raw := modelDocToRaw(m)
	raw.Animations[0].Tracks[0].Bone = "NoSuchBone"
	if err := validateRawModel(raw); err == nil {
		t.Error("validation accepted a track driving a bone the armature does not contain")
	}
}

func TestLoadRejectsDuplicateClipNames(t *testing.T) {
	m, _, _ := clipChain(t)
	raw := modelDocToRaw(m)
	raw.Animations = append(raw.Animations, raw.Animations[0])
	if err := validateRawModel(raw); err == nil {
		t.Error("validation accepted two clips with the same name")
	}
}

func TestLoadRejectsAnArmatureTooBigForTheShader(t *testing.T) {
	m := NewCubeDoc()
	for i := 0; i <= gfx.MaxSkinBones; i++ {
		y := float32(i)
		if m.AddBone(mgl32.Vec3{0, y, 0}, mgl32.Vec3{0, y + 1, 0}) < 0 {
			t.Fatalf("AddBone refused bone %d", i)
		}
	}
	c := m.Clip(m.AddClip("walk"))
	c.SetKey(0, 0, IdentBonePose())

	if err := validateRawModel(modelDocToRaw(m)); err == nil {
		t.Errorf("validation accepted %d bones; the shader palette holds %d", len(m.Bones), gfx.MaxSkinBones)
	}
}

func TestUnanimatedModelStillSavesWithoutAnAnimationsBlock(t *testing.T) {
	m, _ := boneChain(t)
	path := filepath.Join(t.TempDir(), "plain"+ModelExt)
	if err := SaveModel(m, path); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(body), "animations:") {
		t.Errorf("a model with no clips wrote an animations block:\n%s", body)
	}
}
