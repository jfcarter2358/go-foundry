package main

import (
	"foundry/internal/geom"
	"foundry/internal/model"
	"foundry/internal/modeltest"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// withPoseEditor stands up a rigged document in pose mode and restores the
// editor globals afterwards — the shape editor_model_pick_test.go uses.
func withPoseEditor(t *testing.T) [3]int {
	t.Helper()
	prevDoc, prevPose, prevMode := modelDoc, modelPose, modelPoseMode
	prevClip, prevTime, prevSel := modelPoseClip, modelPoseTime, modelSelection
	prevUndo, prevRedo := modelUndoStack, modelRedoStack
	t.Cleanup(func() {
		modelDoc, modelPose, modelPoseMode = prevDoc, prevPose, prevMode
		modelPoseClip, modelPoseTime, modelSelection = prevClip, prevTime, prevSel
		modelUndoStack, modelRedoStack = prevUndo, prevRedo
		modelPosePlaying = false
	})

	m, idx := modeltest.BoneChain(t)
	m.AutoWeightVerts(modeltest.AllVerts(m))
	modelDoc = m
	modelPoseMode = false
	modelPoseClip = -1
	modelPoseTime = 0
	modelUndoStack, modelRedoStack = boundedStack[modelSnapshot]{}, boundedStack[modelSnapshot]{}
	modelSetPoseMode(true)
	return idx
}

// TestPoseModeLeavesTheDocumentAtRest is the design's central claim: the
// viewport shows a deformed mesh while modelDoc still holds the shape that gets
// saved.
func TestPoseModeLeavesTheDocumentAtRest(t *testing.T) {
	idx := withPoseEditor(t)
	restVerts := append([]mgl32.Vec3(nil), modelDoc.Verts...)
	restBones := model.CloneBones(modelDoc.Bones)

	modelPoseSet(idx[0], model.ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(45), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})

	for i := range modelDoc.Verts {
		if !vec3Close(modelDoc.Verts[i], restVerts[i], 1e-6) {
			t.Fatalf("posing wrote vertex %d into the document: %v -> %v", i, restVerts[i], modelDoc.Verts[i])
		}
	}
	for i := range modelDoc.Bones {
		if !vec3Close(modelDoc.Bones[i].Head, restBones[i].Head, 1e-6) ||
			!vec3Close(modelDoc.Bones[i].Tail, restBones[i].Tail, 1e-6) {
			t.Fatalf("posing moved bone %d in the document", i)
		}
	}

	// ...and the display arrays did move.
	moved := false
	for i, p := range modelDisplayVerts() {
		if !vec3Close(p, restVerts[i], 1e-6) {
			moved = true
			break
		}
	}
	if !moved {
		t.Error("the displayed mesh did not deform")
	}
	if vec3Close(modelDisplayBones()[idx[0]].Tail, restBones[idx[0]].Tail, 1e-6) {
		t.Error("the displayed armature did not move")
	}
}

func TestLeavingPoseModeShowsTheRestMeshAgain(t *testing.T) {
	idx := withPoseEditor(t)
	modelPoseSet(idx[0], model.ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(45), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})
	modelSetPoseMode(false)

	for i, p := range modelDisplayVerts() {
		if !vec3Close(p, modelDoc.Verts[i], 1e-6) {
			t.Fatalf("vertex %d still shows the pose after leaving pose mode", i)
		}
	}
	for i, b := range modelDisplayBones() {
		if !vec3Close(b.Tail, modelDoc.Bones[i].Tail, 1e-6) {
			t.Fatalf("bone %d still shows the pose after leaving pose mode", i)
		}
	}
}

func TestPoseModeForcesBoneSelection(t *testing.T) {
	prev := modelSelection.Mode
	t.Cleanup(func() { modelSelection.Mode = prev })
	modelSelection.Mode = modelSelFace
	withPoseEditor(t)
	if modelSelection.Mode != modelSelBone {
		t.Errorf("pose mode left the selection in %v; mesh tools edit the rest document", modelSelection.Mode)
	}
}

// TestUndoRestoresThePoseNotJustTheDocument covers why modelSnapshot exists: the
// pose is not inside ModelDoc, so a document-only snapshot would undo nothing.
func TestUndoRestoresThePoseNotJustTheDocument(t *testing.T) {
	idx := withPoseEditor(t)
	ModelPushUndo()
	modelPoseSet(idx[0], model.ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(60), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})
	if modelPose.Bone(idx[0]).IsIdentity() {
		t.Fatal("the pose edit did not take")
	}

	ModelUndo()
	if !modelPose.Bone(idx[0]).IsIdentity() {
		t.Error("undo left the bone posed")
	}
	ModelRedo()
	if modelPose.Bone(idx[0]).IsIdentity() {
		t.Error("redo did not restore the pose")
	}
}

func TestKeyPoseKeysOnlyPosedBones(t *testing.T) {
	idx := withPoseEditor(t)
	modelPoseClip = modelDoc.AddClip("walk")
	modelPoseSet(idx[1], model.ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(30), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})

	modelKeyPose()
	c := modelActiveClip()
	if c == nil {
		t.Fatal("no active clip")
	}
	if len(c.Tracks) != 1 {
		t.Fatalf("keyed %d tracks, want 1 — untouched bones should stay unkeyed", len(c.Tracks))
	}
	if c.Tracks[0].Bone != idx[1] {
		t.Errorf("keyed bone %d, want %d", c.Tracks[0].Bone, idx[1])
	}
}

// TestScrubbingAndPlaybackAgree checks the single-path claim: both move the
// playhead through modelPoseSampleClip, so a frame reached either way looks the
// same.
func TestScrubbingAndPlaybackAgree(t *testing.T) {
	idx := withPoseEditor(t)
	modelPoseClip = modelDoc.AddClip("walk")
	c := modelActiveClip()
	c.Duration = 1
	c.FPS = 10
	c.Loop = false
	c.SetKey(idx[0], 0, model.IdentBonePose())
	c.SetKey(idx[0], 1, model.ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(90), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})

	modelPoseSetTime(0.5)
	scrubbed := modelPose.Bone(idx[0]).EffectiveRot()

	modelPoseSetTime(0)
	modelPosePlaying = true
	for i := 0; i < 5; i++ {
		modelPoseTick(0.1)
	}
	played := modelPose.Bone(idx[0]).EffectiveRot()

	if !vec3Close(scrubbed.V, played.V, 1e-4) {
		t.Errorf("scrubbing and playback disagree at 0.5s: %v vs %v", scrubbed.V, played.V)
	}
}

func TestPlaybackStopsAtTheEndOfAOneShot(t *testing.T) {
	idx := withPoseEditor(t)
	modelPoseClip = modelDoc.AddClip("hit")
	c := modelActiveClip()
	c.Duration, c.Loop = 0.5, false
	c.SetKey(idx[0], 0, model.IdentBonePose())

	modelPosePlaying = true
	modelPoseTick(1)
	if modelPosePlaying {
		t.Error("a one-shot kept playing past its end")
	}
	if modelPoseTime != c.EffectiveDuration() {
		t.Errorf("playhead settled at %v, want the clip end %v", modelPoseTime, c.EffectiveDuration())
	}
}

func TestPlaybackLoops(t *testing.T) {
	idx := withPoseEditor(t)
	modelPoseClip = modelDoc.AddClip("walk")
	c := modelActiveClip()
	c.Duration, c.Loop = 0.5, true
	c.SetKey(idx[0], 0, model.IdentBonePose())

	modelPosePlaying = true
	modelPoseTick(0.6)
	if !modelPosePlaying {
		t.Error("a looping clip stopped at its end")
	}
	if modelPoseTime >= c.EffectiveDuration() {
		t.Errorf("playhead did not wrap: %v", modelPoseTime)
	}
}

// TestPoseTranslationSkipsConnectedBones covers the joint-welding rule: a
// connected bone's head *is* its parent's tail, so translating it would pull the
// chain apart.
func TestPoseTranslationSkipsConnectedBones(t *testing.T) {
	idx := withPoseEditor(t)
	if !modelDoc.Bones[idx[1]].Connected {
		t.Fatal("expected an extruded chain to be connected")
	}
	modelSelection.Bones = []int{idx[1]}
	modelPoseGizmoCapture()
	modelPoseMoveApply(0, 1)
	modelPoseGizmoRelease()

	if !modelPose.Bone(idx[1]).IsIdentity() {
		t.Error("a connected bone was translated; the joint would come apart")
	}

	// A root, by contrast, moves.
	modelSelection.Bones = []int{idx[0]}
	modelPoseGizmoCapture()
	modelPoseMoveApply(0, 1)
	modelPoseGizmoRelease()
	if modelPose.Bone(idx[0]).IsIdentity() {
		t.Error("the root bone did not translate")
	}
}

// TestPoseScaleUsesTheBonesOwnAxes pins the frame the scale gizmo works in.
func TestPoseScaleUsesTheBonesOwnAxes(t *testing.T) {
	idx := withPoseEditor(t)
	modelSelection.Bones = []int{idx[0]}
	axes, ok := modelPoseScaleAxes()
	if !ok {
		t.Fatal("pose mode did not pin the scale gizmo to the bone's frame")
	}
	// The chain runs up +Y, so the bone's Y axis is the world's.
	if !vec3Close(axes[1], mgl32.Vec3{0, 1, 0}, 1e-5) {
		t.Errorf("bone Y axis = %v, want the bone direction", axes[1])
	}
}

func TestClearChannelDropsOnlyThatChannel(t *testing.T) {
	idx := withPoseEditor(t)
	modelPoseSet(idx[0], model.ModelBonePose{
		Trans: mgl32.Vec3{1, 0, 0},
		Rot:   mgl32.QuatRotate(0.5, mgl32.Vec3{0, 0, 1}),
		Scale: mgl32.Vec3{1, 2, 1},
	})
	modelPoseClearChannel([]int{idx[0]}, false, true, false)

	bp := modelPose.Bone(idx[0])
	if mgl32.Abs(mgl32.Abs(bp.EffectiveRot().W)-1) > 1e-6 {
		t.Error("rotation was not cleared")
	}
	if bp.Trans.Len() < 0.5 {
		t.Error("clearing the rotation also dropped the translation")
	}
	if mgl32.Abs(bp.EffectiveScale().Y()-2) > 1e-6 {
		t.Error("clearing the rotation also dropped the scale")
	}
}

// TestDeletingTheLastClipLeavesNoDanglingIndex covers the panel's clip
// lifecycle. modelPoseClip goes to -1 when the last clip is deleted, and
// anything still holding a *ModelClip from before the delete is looking at a
// slot that no longer belongs to it — the panel re-fetches for exactly this
// reason.
func TestDeletingTheLastClipLeavesNoDanglingIndex(t *testing.T) {
	withPoseEditor(t)
	modelPoseClip = modelDoc.AddClip("walk")
	if modelActiveClip() == nil {
		t.Fatal("the new clip is not active")
	}

	modelDoc.DeleteClip(modelPoseClip)
	if modelPoseClip >= len(modelDoc.Animations) {
		modelPoseClip = len(modelDoc.Animations) - 1
	}
	if modelPoseClip != -1 {
		t.Fatalf("modelPoseClip = %d after deleting the only clip, want -1", modelPoseClip)
	}
	if modelActiveClip() != nil {
		t.Error("modelActiveClip returned a clip after the last one was deleted")
	}
	// The panel's guards all funnel through these two, so neither may panic.
	modelPoseSetTime(0.5)
	modelPoseSampleClip()
	if got := modelPoseFrame(); got != 0 {
		t.Errorf("frame = %d with no clip, want 0", got)
	}
}

// TestDuplicateClipInvalidatesEarlierPointers is the other half: append can move
// the backing array, so a *ModelClip taken before Duplicate must not be written
// through afterwards.
func TestDuplicateClipInvalidatesEarlierPointers(t *testing.T) {
	withPoseEditor(t)
	modelPoseClip = modelDoc.AddClip("walk")
	before := modelActiveClip()
	before.Duration = 3

	// Grow past the initial capacity so a reallocation is near-certain.
	for i := 0; i < 8; i++ {
		modelPoseClip = modelDoc.DuplicateClip(modelPoseClip)
	}

	// Writing through the stale pointer must not be how the panel behaves; the
	// live clip is the one re-fetched by index.
	live := modelActiveClip()
	if live == nil {
		t.Fatal("no active clip after duplicating")
	}
	live.Duration = 7
	if modelDoc.Animations[modelPoseClip].Duration != 7 {
		t.Error("a write through the re-fetched pointer did not reach the document")
	}
	if len(modelDoc.Animations) != 9 {
		t.Errorf("expected 9 clips, got %d", len(modelDoc.Animations))
	}
}

// TestRenameReadBackSurvivesUniquifying: RenameClip uniquifies, so the panel's
// field has to be refilled from the document rather than from what was typed.
func TestRenameReadBackSurvivesUniquifying(t *testing.T) {
	withPoseEditor(t)
	modelDoc.AddClip("walk")
	modelPoseClip = modelDoc.AddClip("run")

	modelDoc.RenameClip(modelPoseClip, "walk")
	got := modelDoc.Animations[modelPoseClip].Name
	if got == "walk" {
		t.Error("two clips ended up named walk; the level file addresses them by name")
	}
	if got == "" {
		t.Error("the clip lost its name")
	}
}

// ---------- keyframe selection ----------

// keyedPoseEditor sets up a clip with two bones keyed at two times each.
func keyedPoseEditor(t *testing.T) ([3]int, *model.ModelClip) {
	t.Helper()
	idx := withPoseEditor(t)
	t.Cleanup(modelClearKeySelection)
	modelPoseClip = modelDoc.AddClip("walk")
	c := modelActiveClip()
	c.Duration, c.FPS = 1, 10
	for _, bi := range []int{idx[0], idx[1]} {
		c.SetKey(bi, 0, model.IdentBonePose())
		c.SetKey(bi, 0.5, model.ModelBonePose{
			Rot:   mgl32.QuatRotate(0.3, mgl32.Vec3{1, 0, 0}),
			Scale: mgl32.Vec3{1, 1, 1},
		})
	}
	modelClearKeySelection()
	return idx, c
}

func TestSelectKeyReplacesUnlessAdditive(t *testing.T) {
	idx, _ := keyedPoseEditor(t)

	modelSelectKey([]modelKeyRef{{Bone: idx[0], Time: 0}}, false)
	if len(modelSelectedKeys) != 1 || !modelKeySelected(idx[0], 0) {
		t.Fatalf("plain click did not select one key: %+v", modelSelectedKeys)
	}

	modelSelectKey([]modelKeyRef{{Bone: idx[1], Time: 0.5}}, false)
	if len(modelSelectedKeys) != 1 || !modelKeySelected(idx[1], 0.5) {
		t.Errorf("plain click did not replace the selection: %+v", modelSelectedKeys)
	}

	modelSelectKey([]modelKeyRef{{Bone: idx[0], Time: 0}}, true)
	if len(modelSelectedKeys) != 2 {
		t.Errorf("ctrl-click did not add: %+v", modelSelectedKeys)
	}
	// Ctrl-clicking a selected key removes it again.
	modelSelectKey([]modelKeyRef{{Bone: idx[0], Time: 0}}, true)
	if modelKeySelected(idx[0], 0) {
		t.Error("ctrl-click did not toggle the key off")
	}
}

func TestDeleteSelectedKeysRemovesExactlyThose(t *testing.T) {
	idx, c := keyedPoseEditor(t)
	modelSelectKey([]modelKeyRef{{Bone: idx[0], Time: 0.5}}, false)

	if n := modelDeleteSelectedKeys(); n != 1 {
		t.Fatalf("deleted %d keys, want 1", n)
	}
	if c.HasKeyAt(idx[0], 0.5) {
		t.Error("the selected key survived")
	}
	if !c.HasKeyAt(idx[0], 0) {
		t.Error("an unselected key on the same track was deleted")
	}
	if !c.HasKeyAt(idx[1], 0.5) {
		t.Error("an unselected key on another track was deleted")
	}
	if len(modelSelectedKeys) != 0 {
		t.Error("the selection was not cleared after deleting")
	}
}

func TestDeleteSelectedKeysIsUndoable(t *testing.T) {
	idx, _ := keyedPoseEditor(t)
	modelSelectKey([]modelKeyRef{{Bone: idx[0], Time: 0.5}}, false)
	modelDeleteSelectedKeys()

	ModelUndo()
	if c := modelActiveClip(); c == nil || !c.HasKeyAt(idx[0], 0.5) {
		t.Error("undo did not bring the deleted keyframe back")
	}
}

// TestKeySelectionIsPrunedWhenItsKeysGoAway guards the reference-by-time design:
// a stale reference must not resurrect on the next delete.
func TestKeySelectionIsPrunedWhenItsKeysGoAway(t *testing.T) {
	idx, c := keyedPoseEditor(t)
	modelSelectKey([]modelKeyRef{
		{Bone: idx[0], Time: 0},
		{Bone: idx[0], Time: 0.5},
	}, false)

	c.DeleteKey(idx[0], 0.5)
	modelPruneKeySelection()
	if len(modelSelectedKeys) != 1 || !modelKeySelected(idx[0], 0) {
		t.Errorf("pruning kept a reference to a deleted key: %+v", modelSelectedKeys)
	}

	modelPoseClip = -1
	modelPruneKeySelection()
	if len(modelSelectedKeys) != 0 {
		t.Error("the selection survived losing the clip")
	}
}

func TestSwitchingClipDropsTheKeySelection(t *testing.T) {
	idx, _ := keyedPoseEditor(t)
	modelSelectKey([]modelKeyRef{{Bone: idx[0], Time: 0}}, false)
	other := modelDoc.AddClip("run")

	modelPoseSetClip(other)
	if len(modelSelectedKeys) != 0 {
		t.Error("a keyframe from the previous clip stayed selected")
	}
}

// TestSummaryKeySelectionCoversEveryBone: clicking the "All bones" lane should
// select every key at that instant, so one Delete clears the whole column.
func TestSummaryKeySelectionCoversEveryBone(t *testing.T) {
	idx, c := keyedPoseEditor(t)
	var refs []modelKeyRef
	for i := range c.Tracks {
		for _, k := range c.Tracks[i].Keys {
			if geom.Abs32(k.Time-0.5) <= model.ModelKeyEpsilon {
				refs = append(refs, modelKeyRef{Bone: c.Tracks[i].Bone, Time: k.Time})
			}
		}
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 bones keyed at 0.5, found %d", len(refs))
	}
	modelSelectKey(refs, false)
	if !modelSummaryKeySelected(c, 0.5) {
		t.Error("the summary marker does not read as selected")
	}

	modelDeleteSelectedKeys()
	for _, bi := range []int{idx[0], idx[1]} {
		if c.HasKeyAt(bi, 0.5) {
			t.Errorf("bone %d kept its key at 0.5", bi)
		}
		if !c.HasKeyAt(bi, 0) {
			t.Errorf("bone %d lost its key at 0", bi)
		}
	}
}

// TestTimelinePickKeyFindsTheDiamondUnderTheCursor exercises the hit test
// against the same layout the drawing uses — the two sharing
// modelTimelineLayout is what makes a diamond clickable where it is painted.
func TestTimelinePickKeyFindsTheDiamondUnderTheCursor(t *testing.T) {
	idx, c := keyedPoseEditor(t)
	l := modelTimelineLayout{
		x0: 100, x1: 500, top: 0,
		lanes: modelKeyedBoneIndices(),
		dur:   c.EffectiveDuration(), fps: c.EffectiveFPS(),
	}
	l.span = l.x1 - l.x0

	// Dead on the second bone's key at t=0.5.
	lane := -1
	for i, bi := range l.lanes {
		if bi == idx[1] {
			lane = i
		}
	}
	if lane < 0 {
		t.Fatal("the second bone has no lane")
	}
	at := imgui.Vec2{X: l.timeToX(0.5), Y: l.laneY(lane)}
	refs, ok := modelTimelinePickKey(l, c, at)
	if !ok || len(refs) != 1 || refs[0].Bone != idx[1] || geom.Abs32(refs[0].Time-0.5) > model.ModelKeyEpsilon {
		t.Fatalf("hit test missed the diamond: ok=%v refs=%+v", ok, refs)
	}

	// Well away from any key on that lane: no hit, so the click scrubs instead.
	away := imgui.Vec2{X: l.timeToX(0.25), Y: l.laneY(lane)}
	if _, ok := modelTimelinePickKey(l, c, away); ok {
		t.Error("hit test claimed a key in empty space; clicks there should scrub")
	}

	// The summary lane returns every bone keyed at that instant.
	sum := imgui.Vec2{X: l.timeToX(0.5), Y: l.summaryY()}
	refs, ok = modelTimelinePickKey(l, c, sum)
	if !ok || len(refs) != 2 {
		t.Errorf("summary lane returned %d refs (ok=%v), want 2", len(refs), ok)
	}
}
