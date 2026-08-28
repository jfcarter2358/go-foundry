package editor

// editor_attach_panel.go — the "ride this" binding widget.
//
// Shared, because a sprite and (later) a particle emitter bind to a carrier the
// same way and there is no reason for two widgets that must agree. The four
// rows cascade: a bone is meaningless without the model attachment it belongs
// to, which is meaningless without the entity that carries it, so each row is
// disabled until the one above it is answered rather than being offered and
// then quietly ignored.
//
// Every list is built from the level as it currently stands, so a carrier that
// has been deleted shows as "(missing)" rather than silently reading as
// unattached — the same courtesy textureCombo pays a texture that has gone.

import (
	"foundry/internal/imguibackend"
	"foundry/internal/world"
	"sort"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// attachNone is the combo entry meaning "not bound at this level".
const attachNone = "(none)"

// editorAttachUI draws the four rows and reports whether anything changed.
// idSuffix keeps two of these distinct when a panel ever draws both.
func editorAttachUI(lvl *world.Level, a *world.Attach, idSuffix string) bool {
	changed := false

	if editorAttachCombo("Frame##attfr"+idSuffix, &a.Frame, editorFrameNames(lvl),
		"A moving coordinate system - in practice a vehicle.\nThe sprite rides it, and Position becomes its local space.") {
		changed = true
	}

	if editorAttachCombo("Entity##atten"+idSuffix, &a.Entity, editorEntityIDs(lvl),
		"An entity to ride. Position becomes an offset from its\norigin, in its rotated space.") {
		// Dropping the entity drops what hangs off it, or the panel would keep
		// offering a bone on a model that is no longer reachable.
		if a.Entity == "" {
			a.Model, a.Bone = "", ""
		}
		changed = true
	}

	e := world.FindEntity(lvl, a.Entity)
	imguibackend.EditorPushDisabled(e == nil)
	if editorAttachCombo("Model##attmd"+idSuffix, &a.Model, editorEntityModelNames(e),
		"Which of the entity's model attachments the bone\nbelongs to.") {
		if a.Model == "" {
			a.Bone = ""
		}
		changed = true
	}
	imguibackend.EditorPopDisabled()

	var m *world.EntityModel
	if e != nil && a.Model != "" {
		m = e.Model(a.Model)
	}
	imguibackend.EditorPushDisabled(m == nil)
	if editorAttachCombo("Bone##attbn"+idSuffix, &a.Bone, editorModelBoneNames(m),
		"A bone within that model. The sprite follows it through\nevery animation the entity plays.") {
		changed = true
	}
	imguibackend.EditorPopDisabled()

	return changed
}

// editorAttachCombo draws one row: a combo over names, with "(none)" first and
// an unknown current value kept and flagged rather than dropped.
func editorAttachCombo(label string, value *string, names []string, tooltip string) bool {
	preview := *value
	if preview == "" {
		preview = attachNone
	} else if !containsString(names, *value) {
		preview = *value + " (missing)"
	}

	changed := false
	if imgui.BeginCombo(label, preview) {
		if imgui.SelectableV(attachNone, *value == "", 0, imgui.Vec2{}) && *value != "" {
			*value = ""
			changed = true
		}
		for _, n := range names {
			if imgui.SelectableV(n, n == *value, 0, imgui.Vec2{}) && n != *value {
				*value = n
				changed = true
			}
		}
		imgui.EndCombo()
	}
	if tooltip != "" && imgui.IsItemHovered() {
		imgui.SetTooltip(tooltip)
	}
	return changed
}

// editorFrameNames lists the level's moving coordinate systems.
//
// Taken from the FrameSet rather than from "entities with a VehicleSpec": a
// frame exists because a chunk names its owner, so an entity can carry a spec
// and no frame (nothing is parented to it yet), and geometry can be parented to
// an entity that is not a vehicle at all. The set is the authority on which
// names Attach.Matrix will actually resolve.
func editorFrameNames(lvl *world.Level) []string {
	if lvl == nil || lvl.Frames == nil {
		return nil
	}
	frames := lvl.Frames.Order()
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		if f.OwnerID != "" {
			out = append(out, f.OwnerID)
		}
	}
	sort.Strings(out)
	return out
}

// editorEntityIDs lists every entity in the level.
func editorEntityIDs(lvl *world.Level) []string {
	if lvl == nil {
		return nil
	}
	out := make([]string, 0, len(lvl.Entities))
	for i := range lvl.Entities {
		out = append(out, lvl.Entities[i].ID)
	}
	return out
}

// editorEntityModelNames lists one entity's model attachments by name.
func editorEntityModelNames(e *world.Entity) []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.Models))
	for i := range e.Models {
		out = append(out, e.Models[i].Name)
	}
	return out
}

// editorModelBoneNames lists one attachment's bones. Empty for an unrigged
// model, which is what disables the row above it having already been answered.
func editorModelBoneNames(m *world.EntityModel) []string {
	if m == nil || m.Doc == nil {
		return nil
	}
	out := make([]string, 0, len(m.Doc.Bones))
	for i := range m.Doc.Bones {
		out = append(out, m.Doc.Bones[i].Name)
	}
	return out
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
