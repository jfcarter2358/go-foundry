package editor

// editor_emitter_panel.go — the Emitters section of the editor's side panel.
//
// Deliberately short. An emitter is a placement, not a definition: everything
// about how the effect looks lives in its .fpar and is edited in the particle
// editor, so this panel is a transform, the document it names, and the three
// knobs worth varying between two placements of the same effect.
//
// The Edit button is the seam between the two: it opens the particle editor on
// this emitter's document, which is where the forty parameters actually are.

import (
	"foundry/internal/imguibackend"
	"foundry/internal/particle"
	"foundry/internal/world"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorEmitterPropertiesSection draws the selected emitter's fields.
func editorEmitterPropertiesSection() {
	e := activeEmitter()
	if e == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusEmitter)
	if !imgui.CollapsingHeaderV("Emitter: "+e.ID+"##emhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusEmitter)
	editorEmitterProperties(e)
}

// editorEmitterAddMenu draws the emitter half of the Explorer's Add menu.
func editorEmitterAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newem", &Editor.NewEmitterID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"emitter\".\nMade unique on placement.")
	}
	imgui.SetNextItemWidth(160)
	particleFileCombo("Effect##newemfx", &Editor.NewEmitterEffect)
	imgui.Separator()
	if imgui.MenuItem("Place By Clicking") {
		editorArmEmitterPlacement()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A particle effect, placed where you click.\nAuthor the effect itself in the particle editor.")
	}
}

// editorEmitterProperties draws the selected emitter's editable state.
func editorEmitterProperties(e *world.Emitter) {
	editorEmitterCarrierNote(e)
	editorPropGroupOpen("Identity##emidgrp", func() { editorEmitterIdentity(e) })
	editorPropGroupOpen("Effect##emfxgrp", func() { editorEmitterEffect(e) })
	editorPropGroupOpen("Transform##emtfgrp", func() { editorEmitterTransform(e) })
	editorPropGroup("Attach##emattachgrp", 0, func() { editorEmitterAttach(e) })
	editorScriptDataUI(&e.ScriptData, "em", strconv.Quote(e.ID))
	editorPropGroup("Actions##emactions", 0, func() { editorEmitterActions(e) })
}

// editorEmitterCarrierNote says so when this emitter is an entity's rather than
// the level's, and offers the one button that leads anywhere.
//
// It has to be said, and said at the top. A materialised emitter is an ordinary
// one in every way that matters — it simulates, it draws, a script addresses it
// — so nothing else on screen distinguishes it from one the level placed, and
// an author who retunes it here would watch the change vanish the next time the
// level is opened. The fields below are left live rather than disabled: turning
// a knob to see what it does is exactly how an author decides what to write on
// the type, and the note is what stops them expecting it to keep.
func editorEmitterCarrierNote(e *world.Emitter) {
	if e.FromEntity == "" {
		return
	}
	editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.8, Z: 0.35, W: 1},
		"Carried by entity "+e.FromEntity+". This emitter belongs to that entity's "+
			"type, not to the level, so nothing changed here is saved — it is put "+
			"back from the type every time the level loads.")
	if ent := world.FindEntity(Editor.level, e.FromEntity); ent != nil && ent.Type != "" {
		if imgui.Button("Edit Type##emcarrier") {
			OpenEntityEditorOn(ent.Type)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Open " + ent.Type + " in the entity editor, where this\neffect is aimed and tuned for good.")
		}
	}
	imgui.Separator()
}

// editorEmitterIdentity draws the ID and the two switches.
//
// Enabled and Emitting are separate because they mean different things and an
// author needs both: Enabled stops the simulation dead, Emitting stops new
// particles while letting the ones in flight finish. Turning a fountain off
// with Enabled makes its spray vanish mid-air; with Emitting it drains.
func editorEmitterIdentity(e *world.Emitter) {
	id := e.ID
	if imgui.InputTextV("ID##emid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != e.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("An emitter needs an ID for a script to address it")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			e.ID = id
		}
	}

	if imgui.Checkbox("Enabled##emenabled", &e.Enabled) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off stops the simulation dead - every live particle\nvanishes at once.")
	}
	if imgui.Checkbox("Emitting##ememitting", &e.Emitting) {
		EditorPushUndo()
		e.SyncSystems()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off stops new particles but lets the ones in flight\nfinish. Bursts still fire. This is what stop_emit does.")
	}
}

// editorEmitterEffect draws the document picker and what it resolved to.
func editorEmitterEffect(e *world.Emitter) {
	if particleFileCombo("Effect##emfx", &e.Effect) {
		EditorPushUndo()
		if err := e.Load(); err != nil {
			editorSetStatus("Emitter %s: %v", e.ID, err)
		}
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(e.Effect == "")
	if imgui.Button("Edit...##emedit") {
		OpenParticleEditorOn(e.Effect)
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open this effect in the particle editor.")
	}

	if !e.Loaded() {
		if e.Effect == "" {
			editorTextMutedWrapped("No effect chosen: this emitter produces nothing.")
		} else {
			editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.5, Z: 0.3, W: 1},
				"Could not load "+e.Effect+ParticleExtLabel+" - the emitter is placed but produces nothing.")
		}
	} else {
		editorTextMuted(layerSummary(e))
	}

	if imgui.Button("Reload##emreload") {
		reloadActiveEmitter()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Re-read the effect from disk, discarding what is in flight.")
	}
	imgui.SameLine()
	if imgui.Button("Restart##emrestart") {
		e.Reset()
	}

	// Both push into the running systems: the rate scale and the tint are
	// folded into each layer on the way to it (Emitter.SetDoc), so without a
	// sync a drag would do nothing until the effect was reloaded — which reads
	// as a dead control rather than as a deferred one.
	if editorDragFloat("Rate scale##emrate", &e.RateScale, 0, 10, "%.2f") {
		e.SyncSystems()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies every layer's emission rate.\n1 is the effect's own.")
	}
	et := e.EffectiveTint()
	tint := [3]float32{et.X(), et.Y(), et.Z()}
	if imgui.ColorEdit3("Tint##emtint", &tint) {
		e.Tint = mgl32.Vec3{tint[0], tint[1], tint[2]}
		e.SyncSystems()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies every layer's own tint - one green torch\nwithout a second document.")
	}
}

// ParticleExtLabel is the extension shown in messages, so a status line reads
// as a filename rather than a bare name.
const ParticleExtLabel = particle.ParticleExt

// layerSummary names the effect's layers and how many particles are live, which
// is the answer to "is this thing running" without leaving the panel.
func layerSummary(e *world.Emitter) string {
	if e.Doc == nil {
		return ""
	}
	s := ""
	for i := range e.Doc.Layers {
		if i > 0 {
			s += ", "
		}
		s += e.Doc.Layers[i].Name
		if e.Doc.Layers[i].Muted {
			s += " (muted)"
		}
	}
	return s + "   live: " + strconv.Itoa(e.LiveCount())
}

// editorEmitterTransform draws position and rotation. Rotation matters here in
// a way it does not for a sphere emitter: it aims the cone.
func editorEmitterTransform(e *world.Emitter) {
	pos := [3]float32{e.Position.X(), e.Position.Y(), e.Position.Z()}
	if editorDragFloat3("Position##empos", &pos, 0, 0, "%.3f") {
		e.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if !e.Attach.IsZero() && imgui.IsItemHovered() {
		imgui.SetTooltip("Local to " + e.Attach.Describe() + ", not the world.")
	}

	rot := vec3ToDegrees(e.Rotation)
	if editorDragFloat3Step("Rotation (deg)##emrot", &rot, -360, 360, "%.1f", 1) {
		e.Rotation = vec3FromDegrees(rot)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Turns the emission shape - a cone fires wherever\nit points.")
	}

	if imgui.Checkbox("Show emission shape##emshapes", &Editor.ShowEmitterShapes) {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Outline the region particles are born in.\nThe only way to aim a cone by eye.")
	}
}

// editorEmitterAttach draws the carrier binding, shared with sprites.
func editorEmitterAttach(e *world.Emitter) {
	if editorAttachUI(Editor.level, &e.Attach, "em") {
		EditorPushUndo()
	}
	if e.Attach.IsZero() {
		editorTextMutedWrapped("Unattached: Position is world-space.")
	} else {
		editorTextMutedWrapped("Rides " + e.Attach.Describe() + "; Position is local to it.")
	}
}

// editorEmitterActions draws the whole-object buttons.
//
// Both are shut off for an emitter an entity carries: a duplicate would be an
// orphan the next re-materialisation drops, and a delete would come back on the
// next load. The note at the top of the panel says where the real edit is.
func editorEmitterActions(e *world.Emitter) {
	imguibackend.EditorPushDisabled(e.FromEntity != "")
	if imgui.Button("Duplicate##emdup") {
		duplicateActiveEmitter()
	}
	imgui.SameLine()
	if imgui.Button("Delete##emdel") {
		deleteActiveEmitter()
	}
	imguibackend.EditorPopDisabled()
	if e.FromEntity != "" && imgui.IsItemHovered() {
		imgui.SetTooltip("Entity " + e.FromEntity + " owns this emitter. Remove it from\n" +
			"that entity's type instead.")
	}
}

// particleFileCombo picks a .fpar by name. Beside modelFileCombo in spirit and
// in behaviour, including its rescan-while-open rule: one directory read per
// frame the dropdown is up, and always current.
func particleFileCombo(label string, effect *string) bool {
	preview := *effect
	if preview == "" {
		preview = "(none)"
	}
	changed := false
	if imgui.BeginCombo(label, preview) {
		if Editor.ParticleFileNames == nil {
			Editor.ParticleFileNames = particleEffectNames()
		}
		if imgui.SelectableV("(none)", *effect == "", 0, imgui.Vec2{}) && *effect != "" {
			*effect = ""
			changed = true
		}
		for _, n := range Editor.ParticleFileNames {
			if imgui.SelectableV(n, n == *effect, 0, imgui.Vec2{}) && n != *effect {
				*effect = n
				changed = true
			}
		}
		imgui.EndCombo()
	} else {
		// Dropped so the next open rescans; a new effect saved from the
		// particle editor has to appear without restarting the level editor.
		Editor.ParticleFileNames = nil
	}
	return changed
}

// particleEffectNames lists the .fpar documents, extension trimmed, since that
// is how an emitter names one.
func particleEffectNames() []string {
	files := particle.ScanParticleFiles()
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f[:len(f)-len(particle.ParticleExt)])
	}
	return out
}
