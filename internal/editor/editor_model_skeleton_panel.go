package editor

// editor_model_skeleton_panel.go — the Skeleton section of the model editor
// panel, and the file actions behind it.
//
// A skeleton is the armature and its clips saved as their own document, so
// several models can wear one rig (model_skeleton.go). The editing surface for
// it is deliberately the same one it always was: the bones are still dragged in
// this viewport, the clips are still keyed in this timeline. All this section
// adds is *where the result goes* — into a shared .fskl instead of into this
// model's .fmdl.
//
// That is why there is no separate skeleton editor window. A rig is built
// against a mesh; a window with no mesh in it would be a worse place to build
// one, and two places to edit bones would be two places for them to disagree.

import (
	"fmt"
	"foundry/internal/imguibackend"
	"foundry/internal/model"
	"path/filepath"
	"time"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	modelShowNewSkeletonDialog  bool
	modelShowLinkSkeletonDialog bool

	modelNewSkeletonName string
	modelSkeletonList    []string // cached .fskl listing; nil forces a rescan
)

// modelSkeletonSection is the panel block, drawn just above the bones it
// decides the home of.
func modelSkeletonSection() {
	if modelDoc == nil {
		return
	}
	if !imgui.CollapsingHeaderV("Skeleton##modelskel", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	if modelDoc.Skeleton == "" {
		imgui.Text("This model owns its own bones.")
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Bones and clips are saved inside the .fmdl,\n" +
				"and reach no other model.")
		}
		if imgui.Button("New Skeleton...##modelskelnew") {
			modelNewSkeletonName = modelDoc.ID
			modelShowNewSkeletonDialog = true
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Move this model's bones and clips into a shared\n" +
				"skeleton file, and link this model to it.")
		}
		imgui.SameLine()
		if imgui.Button("Link Skeleton...##modelskellink") {
			modelSkeletonList = nil
			modelShowLinkSkeletonDialog = true
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Replace this model's armature with an existing one.\n" +
				"Skin weights follow by bone name; a bone the skeleton\n" +
				"does not have loses its vertices.")
		}
		return
	}

	imgui.Text(fmt.Sprintf("Linked: %s%s", modelDoc.Skeleton, model.SkeletonExt))
	editorTextWrapped(fmt.Sprintf("%d bones, %d clips — shared. Editing them changes every model on this rig.",
		len(modelDoc.Bones), len(modelDoc.Animations)))
	if others := modelsSharingSkeleton(modelDoc.Skeleton, modelDoc.Path); len(others) > 0 {
		editorTextWrapped("Also worn by: " + joinUpTo(others, 4))
	}

	if imgui.Button("Change...##modelskelchange") {
		modelSkeletonList = nil
		modelShowLinkSkeletonDialog = true
	}
	imgui.SameLine()
	if imgui.Button("Unlink##modelskelunlink") {
		ModelPushUndo()
		name := modelDoc.Skeleton
		modelDoc.UnlinkSkeleton()
		modelSetStatus("Unlinked from %s — the bones are this model's own now", name)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Keep the bones and clips exactly as they are, but save them\n" +
			"inside this .fmdl again. Edits stop reaching the other models.")
	}
}

// modelSkeletonDialogs draws the two file pickers. Called from modelDialogs so
// every model-editor modal is queued in one place.
func modelSkeletonDialogs() {
	imguibackend.WithModalPopup("New Skeleton##modelskelnewdlg", &modelShowNewSkeletonDialog, func() {
		imgui.Text(fmt.Sprintf("Saved to %s", model.SkeletonsDir()))
		editorTextWrapped("This model's bones and animations move into the new file. " +
			"Other models can then link the same rig.")
		imgui.InputText("Name##modelskelnewname", &modelNewSkeletonName)
		imgui.Separator()
		if imgui.Button("Create##modelskelnewok") && modelNewSkeletonName != "" && modelDoc != nil {
			modelCreateSkeleton(modelNewSkeletonName)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##modelskelnewcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Link Skeleton##modelskellinkdlg", &modelShowLinkSkeletonDialog, func() {
		if modelSkeletonList == nil {
			modelSkeletonList = model.SkeletonNames()
		}
		if len(modelSkeletonList) == 0 {
			imgui.Text(fmt.Sprintf("No %s files in %s", model.SkeletonExt, model.SkeletonsDir()))
		}
		editorTextWrapped("Skin weights are carried across by bone name.")
		for _, name := range modelSkeletonList {
			if !imgui.Selectable(name) {
				continue
			}
			modelLinkSkeleton(name)
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##modelskellinkcancel") {
			imgui.CloseCurrentPopup()
		}
	})
}

// modelCreateSkeleton lifts the open document's armature into a new .fskl and
// links the document to it.
func modelCreateSkeleton(name string) {
	if modelDoc == nil {
		return
	}
	if len(modelDoc.Bones) == 0 {
		modelSetStatus("Add bones before making a skeleton")
		return
	}
	skel := model.ExtractSkeleton(modelDoc, name)
	path := model.SkeletonPath(name)
	if err := model.SaveSkeleton(skel, path); err != nil {
		modelSetStatus("New skeleton failed: %v", err)
		return
	}
	ModelPushUndo()
	// Through LinkSkeleton rather than by setting the field: the weights have to
	// be re-resolved through the saved skeleton's bone order, which is the order
	// every future load will see.
	modelDoc.LinkSkeleton(skel)
	modelSkeletonList = nil
	modelSetStatus("Created %s — %d bones, %d clips", filepath.Base(path), len(skel.Bones), len(skel.Animations))
}

// modelLinkSkeleton points the open document at an existing skeleton.
func modelLinkSkeleton(name string) {
	skel, err := model.LoadSkeletonNamed(modelDoc.Path, name)
	if err != nil {
		modelSetStatus("Link failed: %v", err)
		return
	}
	ModelPushUndo()
	dropped := modelDoc.LinkSkeleton(skel)
	modelClearSelection()
	modelUVInvalidate()
	if dropped > 0 {
		modelSetStatus("Linked %s — %d influence(s) had no bone in it and were dropped", name, dropped)
		return
	}
	modelSetStatus("Linked %s — %d bones, %d clips", name, len(skel.Bones), len(skel.Animations))
}

// modelsSharingSkeleton lists the other models on disk that link name, so the
// panel can say whose animation is about to change.
//
// The scan reads each .fmdl's first lines rather than parsing it
// (model.SkeletonLinkOf), but it is still a folder walk, and the panel asks
// every frame the section is open. So the answer is held for a moment — long
// enough that an open section is one scan rather than sixty a second, short
// enough that a model relinked in another window shows up without a restart.
// The same trade the save chip's fingerprint refresh makes.
var skeletonUsers struct {
	name    string
	exclude string
	at      time.Time
	list    []string
}

const skeletonUsersTTL = 2 * time.Second

func modelsSharingSkeleton(name, exclude string) []string {
	if name == "" {
		return nil
	}
	if skeletonUsers.name == name && skeletonUsers.exclude == exclude &&
		time.Since(skeletonUsers.at) < skeletonUsersTTL {
		return skeletonUsers.list
	}
	skeletonUsers.name, skeletonUsers.exclude = name, exclude
	skeletonUsers.at = time.Now()
	skeletonUsers.list = model.ModelsUsingSkeleton(name, exclude)
	return skeletonUsers.list
}

func joinUpTo(names []string, max int) string {
	if len(names) <= max {
		return joinComma(names)
	}
	return fmt.Sprintf("%s and %d more", joinComma(names[:max]), len(names)-max)
}

func joinComma(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// modelSharedRigNote is the one-line warning the Bones and Animation sections
// carry when the armature is not this model's own.
//
// It sits with the controls rather than only in the Skeleton section above,
// because by the time someone is dragging a joint or keying a frame the section
// that says where it goes may well be collapsed — and "I fixed the marine's
// walk and the mechanic started walking differently" is a surprise worth one
// line of screen space to avoid.
func modelSharedRigNote() {
	if modelDoc == nil || modelDoc.Skeleton == "" {
		return
	}
	others := modelsSharingSkeleton(modelDoc.Skeleton, modelDoc.Path)
	if len(others) == 0 {
		editorTextMutedWrapped(fmt.Sprintf("Shared rig %s — no other model wears it yet.", modelDoc.Skeleton))
		return
	}
	editorTextWrappedColored(modelSharedRigColor,
		fmt.Sprintf("Shared rig %s — edits here also change %s.", modelDoc.Skeleton, joinUpTo(others, 4)))
}

// modelSharedRigColor is the panel's warning amber, matching the AI panel's.
var modelSharedRigColor = imgui.Vec4{X: 0.95, Y: 0.78, Z: 0.35, W: 1}
