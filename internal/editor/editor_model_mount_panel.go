package editor

// editor_model_mount_panel.go — the "Mount on" rows of a model attachment.
//
// One widget for both places an attachment is edited: the entity editor, which
// edits a type, and the level editor's entity panel, which edits one placed
// entity. They differ only in where the bone names come from (the entity
// editor's preview has read the .fmdl; a placed entity has loaded its own) and
// in which undo stack an edit goes on, so both are handed in.

import (
	"foundry/internal/imguibackend"
	"foundry/internal/world"
)

// modelMountHost is what the mount rows need from the editor they are in.
type modelMountHost struct {
	// Models are every attachment on the entity, the one being edited among
	// them.
	Models []world.EntityModel
	// Bones lists the bones of the named attachment, or nil when it has none or
	// has not loaded.
	Bones func(model string) []string
	// PushUndo is called once, before the first write of an edit.
	PushUndo func()
}

// editorModelMountUI draws the Parent and Bone rows for attachment m, and
// reports whether either changed. Candidates that would make m ride itself are
// left out of the Parent list rather than offered and refused.
func editorModelMountUI(h modelMountHost, m *world.EntityModel, idSuffix string) bool {
	holder := &world.Entity{Models: h.Models}
	parents := make([]string, 0, len(h.Models))
	for i := range h.Models {
		name := h.Models[i].Name
		if name == m.Name || holder.ModelMountCycle(m.Name, name) {
			continue
		}
		parents = append(parents, name)
	}

	changed := false
	parent := m.Parent
	if editorAttachCombo("Mount on##mmountp"+idSuffix, &parent, parents,
		"Another of this entity's models for this one to ride —\n"+
			"a sword on a character, a turret on a hull. (none) rides\n"+
			"the entity itself. Offset and Rotation are then local to it.") {
		h.PushUndo()
		m.Parent = parent
		// A bone belongs to the model it was picked from; carried over to
		// another it would name nothing, or the wrong joint.
		m.Bone = ""
		changed = true
	}

	if m.Parent == "" {
		return changed
	}
	bones := h.Bones(m.Parent)
	imguibackend.EditorPushDisabled(len(bones) == 0 && m.Bone == "")
	bone := m.Bone
	if editorAttachCombo("Bone##mmountb"+idSuffix, &bone, bones,
		"A bone in that model — the socket. This model sits at its\n"+
			"head, +Y along the bone, and follows it through every\n"+
			"animation. (none) rides the model's own origin.") {
		h.PushUndo()
		m.Bone = bone
		changed = true
	}
	imguibackend.EditorPopDisabled()
	if len(bones) == 0 {
		editorTextMutedWrapped("That model has no bones, or has not loaded — this one rides its origin.")
	}
	return changed
}

// editorModelBoneNamesIn lists the bones of one attachment in models, for a host
// whose attachments are loaded.
func editorModelBoneNamesIn(models []world.EntityModel, name string) []string {
	for i := range models {
		if models[i].Name == name {
			return editorModelBoneNames(&models[i])
		}
	}
	return nil
}
