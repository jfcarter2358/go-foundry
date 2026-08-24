package main

// editor_model_anim_panel.go — the animation section and its timeline.
//
// One collapsing section like every other panel block (modelBoneSection,
// modelUVSection), plus a custom-drawn timeline. The timeline has to be
// hand-drawn — imgui has no widget for "lanes of draggable diamonds" — and
// follows the canvas pattern the texture editor already uses: reserve a
// rectangle with InvisibleButton, then paint into the window's draw list.
//
// The whole panel routes edits through the same four calls the gizmos do
// (modelPoseSetTime, modelKeyBones, modelPoseEvaluate, ModelPushUndo), so a key
// dropped from a button and one dropped from the keyboard cannot disagree.

import (
	"fmt"
	"foundry/internal/imguibackend"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// modelAnimRenameBuf backs the clip rename field, and modelAnimRenameFor is the
// clip it was filled from. Tracking which clip it belongs to is what makes the
// field follow the combo: keyed on emptiness alone it would keep showing the
// previous clip's name after a switch, and refill itself the moment the user
// cleared it to type a new one.
var (
	modelAnimRenameBuf string
	modelAnimRenameFor = -1
)

// modelAnimSection is the panel block. Drawn after the bone section, since a
// clip is meaningless without an armature to drive.
func modelAnimSection() {
	if modelDoc == nil {
		return
	}
	if !imgui.CollapsingHeaderV("Animation##modelanim", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	if len(modelDoc.Bones) == 0 {
		imgui.Text("Add bones before animating.")
		return
	}

	modelPoseModeToggle()
	imgui.Separator()
	modelAnimClipControls()
	if modelActiveClip() == nil {
		return
	}
	imgui.Separator()
	modelAnimTransport()
	imgui.Separator()
	modelAnimKeyControls()
}

// modelPoseModeToggle is the mode switch, styled like the toolbar's active
// buttons so it reads as a mode rather than an action.
func modelPoseModeToggle() {
	// Through the helper, so the mode is latched once: clicking the button flips
	// modelPoseMode, and a push/pop pair that re-tested the live value would
	// underflow ImGui's style stack (editorActiveToggleButton, editor_ui.go).
	active := modelPoseMode
	label := "Enter pose mode [P]"
	if active {
		label = "Leave pose mode [P]"
	}
	if editorActiveToggleButton(label+"##modelposemode", active) {
		modelSetPoseMode(!active)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Pose mode deforms the mesh without editing it.\n" +
			"The document keeps its rest shape, so saving is unaffected.")
	}
	if modelPoseMode {
		imgui.Text(fmt.Sprintf("Posed bones: %d", len(modelPosedBoneIndices())))
	}
}

// modelAnimClipControls is the clip picker and its metadata.
func modelAnimClipControls() {
	name := "(none)"
	if c := modelActiveClip(); c != nil {
		name = c.Name
	}
	if imgui.BeginCombo("Clip##modelanimclip", name) {
		for i := range modelDoc.Animations {
			if imgui.SelectableV(modelDoc.Animations[i].Name, i == modelPoseClip, 0, imgui.Vec2{}) {
				modelPoseSetClip(i)
			}
		}
		imgui.EndCombo()
	}

	if imgui.Button("New##modelanimnew") {
		ModelPushUndo()
		modelPoseSetClip(modelDoc.AddClip("Clip"))
		modelSetStatus("New clip %q", modelDoc.Animations[modelPoseClip].Name)
	}
	c := modelActiveClip()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(c == nil)
	if imgui.Button("Duplicate##modelanimdup") && c != nil {
		ModelPushUndo()
		if i := modelDoc.DuplicateClip(modelPoseClip); i >= 0 {
			modelPoseSetClip(i)
		}
	}
	imgui.SameLine()
	if imgui.Button("Delete##modelanimdel") && c != nil {
		ModelPushUndo()
		modelDoc.DeleteClip(modelPoseClip)
		if modelPoseClip >= len(modelDoc.Animations) {
			modelPoseClip = len(modelDoc.Animations) - 1
		}
		modelClearKeySelection()
		modelPoseSetTime(0)
	}
	imguibackend.EditorPopDisabled()

	// Re-fetch: both buttons above mutate modelDoc.Animations, and the pointer
	// taken before them does not survive it. Duplicate appends, which can move
	// the backing array, so writes through the old pointer would land in a
	// freed one; Delete can leave modelPoseClip at -1 while the stale pointer is
	// still non-nil, and the rename read-back below would index it.
	c = modelActiveClip()
	if c == nil {
		imgui.Text("No clips yet — New starts one.")
		return
	}

	// Committed on Enter, so a rename is one undo entry rather than one per
	// keystroke — the rule modelBoneProperties follows for bone names.
	if modelAnimRenameFor != modelPoseClip {
		modelAnimRenameBuf = c.Name
		modelAnimRenameFor = modelPoseClip
	}
	if imgui.InputTextV("Name##modelanimname", &modelAnimRenameBuf,
		imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
		ModelPushUndo()
		modelDoc.RenameClip(modelPoseClip, modelAnimRenameBuf)
		// Read back, because RenameClip uniquifies — otherwise the field would
		// show a name the clip does not have.
		modelAnimRenameBuf = modelDoc.Animations[modelPoseClip].Name
	}

	dur := c.duration()
	if editorDragFloat("Duration (s)##modelanimdur", &dur, 0.05, 600, "%.2f") {
		ModelPushUndo()
		c.Duration = dur
		modelPoseSetTime(modelPoseTime) // re-clamp the playhead into the new range
	}
	fps := c.fps()
	if editorDragFloat("FPS##modelanimfps", &fps, 1, 120, "%.0f") {
		ModelPushUndo()
		c.FPS = fps
	}
	loop := c.Loop
	if imgui.Checkbox("Loop##modelanimloop", &loop) {
		ModelPushUndo()
		c.Loop = loop
	}
}

// modelAnimTransport is the panel's copy of the playback row. The timeline strip
// along the bottom of the viewport (editor_model_timeline.go) carries the same
// controls next to the keys they move through; this one is what remains reachable
// when the window is too narrow for the strip.
func modelAnimTransport() {
	c := modelActiveClip()
	if c == nil {
		return
	}
	if imgui.Button("|<##modelanimstart") {
		modelPosePlaying = false
		modelPoseSetFrame(0)
	}
	imgui.SameLine()
	if imgui.Button("<##modelanimprev") {
		modelPosePlaying = false
		modelPoseStepFrame(-1)
	}
	imgui.SameLine()
	play := "Play"
	if modelPosePlaying {
		play = "Pause"
	}
	if imgui.Button(play + "##modelanimplay") {
		modelTogglePlayback()
	}
	imgui.SameLine()
	if imgui.Button(">##modelanimnext") {
		modelPosePlaying = false
		modelPoseStepFrame(1)
	}
	imgui.SameLine()
	if imgui.Button(">|##modelanimend") {
		modelPosePlaying = false
		modelPoseSetFrame(c.frameCount())
	}

	frame := int32(modelPoseFrame())
	if imgui.SliderInt("Frame##modelanimframe", &frame, 0, int32(c.frameCount())) {
		modelPosePlaying = false
		modelPoseSetFrame(int(frame))
	}
	imgui.Text(fmt.Sprintf("%.3fs of %.3fs", modelPoseTime, c.duration()))
}

// modelTogglePlayback starts or stops the playhead, entering pose mode first —
// pressing play outside it would run a clip nothing was showing.
func modelTogglePlayback() {
	if modelActiveClip() == nil {
		return
	}
	if !modelPoseMode {
		modelSetPoseMode(true)
	}
	modelPosePlaying = !modelPosePlaying
}

// modelAnimKeyControls is the keying row.
func modelAnimKeyControls() {
	c := modelActiveClip()
	if c == nil {
		return
	}
	posed := modelPosedBoneIndices()
	imguibackend.EditorPushDisabled(!modelPoseMode)
	if imgui.Button("Key pose [I]##modelanimkeypose") {
		modelKeyPose()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Keys every bone that has moved off its rest placement.\n" +
			"Bones you have not touched are left unkeyed, so the clip stays editable.")
	}
	imgui.SameLine()
	if imgui.Button("Key selected##modelanimkeysel") {
		ModelPushUndo()
		n := modelKeyBones(modelSelection.Bones)
		modelSetStatus("Keyed %d bone(s) at frame %d", n, modelPoseFrame())
	}
	imguibackend.EditorPopDisabled()

	if imgui.Button("Delete key##modelanimdelkey") {
		// Selected keys first, playhead as the fallback — the same order the
		// Delete key follows (modelPoseHandleKey).
		modelDeleteSelectedKeys()
	}
	imgui.SameLine()
	if imgui.Button("Reset pose##modelanimreset") {
		ModelPushUndo()
		modelPoseReset()
		modelSetStatus("Pose reset to rest")
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(len(modelSelection.Bones) == 0)
	if imgui.Button("Reset bone##modelanimresetbone") {
		ModelPushUndo()
		modelPoseResetBones(modelSelection.Bones)
	}
	imguibackend.EditorPopDisabled()

	imgui.Text(fmt.Sprintf("Posed: %d   Keyed bones: %d", len(posed), len(modelKeyedBoneIndices())))
}

// modelKeyPose keys every bone that has been moved off rest.
func modelKeyPose() {
	posed := modelPosedBoneIndices()
	if len(posed) == 0 {
		modelSetStatus("Nothing to key — no bone has moved off its rest placement")
		return
	}
	ModelPushUndo()
	n := modelKeyBones(posed)
	modelSetStatus("Keyed %d bone(s) at frame %d", n, modelPoseFrame())
}

// modelDeleteKeyAtPlayhead removes the playhead's keys — the selected bones', or
// every bone's when nothing is selected.
func modelDeleteKeyAtPlayhead() {
	c := modelActiveClip()
	if c == nil {
		return
	}
	t := float32(modelPoseFrame()) / c.fps()
	targets := modelSelection.Bones
	if len(targets) == 0 {
		targets = modelKeyedBoneIndices()
	}
	removed := 0
	ModelPushUndo()
	for _, bi := range targets {
		if c.DeleteKey(bi, t) {
			removed++
		}
	}
	if removed == 0 {
		// Nothing changed, so the undo entry would be a no-op the user has to
		// press Ctrl+Z through — the same guard modelAddBone uses.
		modelUndoStack.pop()
		modelSetStatus("No key at frame %d", modelPoseFrame())
		return
	}
	modelPoseSampleClip()
	modelSetStatus("Removed %d key(s) at frame %d", removed, modelPoseFrame())
}
