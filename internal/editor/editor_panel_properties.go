package editor

// editor_panel_properties.go — the Properties block: the selected object's
// fields.
//
// Entirely selection-driven. Every function here checks its own precondition
// and draws nothing when it does not hold, so the caller lists them in a fixed
// order and only the ones with something to say appear — the panel's one rule
// ("only ever shows what the current mode and the current selection can
// actually act on") lives here more than anywhere else in the panel.
//
// Order follows SETTINGS_LAYOUT.md: Chunk, then whatever the current editor's
// own selection unit is (a 3D face/vertex, or the 2D sector map's vertex/edge
// blocks), then the painted biome sector, Entity, Volume, and Lights.

import (
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorPropGroup draws one collapsible group of related fields inside a
// properties block, and runs body when it is open.
//
// Groups are what make a properties block readable: a sector's floor carries a
// height, a texture, a mesh to shape, UV, a skirt, smoothing, procedural detail
// and its own tags/attributes/script, and drawn flat that is thirty controls in
// one column with nothing saying which of them are about the floor. One group
// per thing, and every group folds away as a unit.
//
// A tree node rather than a collapsing header, because these nest: the framed
// bar is the properties block itself, and what sits inside it is indented under
// it rather than competing with it.
//
// label carries its own "##id" suffix wherever the visible text can change —
// imgui derives a node's ID from its label, so a group titled after the thing
// it edits would forget whether it was open every time that name changed.
func editorPropGroup(label string, flags imgui.TreeNodeFlags, body func()) {
	if !imgui.TreeNodeV(label, flags|imgui.TreeNodeFlagsSpanAvailWidth) {
		return
	}
	body()
	imgui.TreePop()
}

// editorPropGroupOpen is editorPropGroup for the group a block opens on — the
// one holding what the object *is*, which is what someone who just selected it
// came to see.
func editorPropGroupOpen(label string, body func()) {
	editorPropGroup(label, imgui.TreeNodeFlagsDefaultOpen, body)
}

// editorPropertiesSection draws every properties block, in order.
func editorPropertiesSection(lvl *world.Level, kind editorPanelKind) {
	editorChunkPropertiesSection(lvl)

	if kind == panelKind2D {
		if ch := activeChunk(); ch != nil {
			editor2DSectorSections(lvl, ch)
		}
	} else {
		editor3DSectorPropertiesSection(lvl)
		editor3DVertexPanel(lvl)
	}
	// Both editors: a water plane clicked in the 3D view selects the biome
	// sector that produced it, so the brush has to be reachable from there
	// too — it used to be a water-only block of its own, which meant the same
	// sector showed two different subsets of itself depending on which editor
	// was asking.
	biomePanelSection()

	editorSpawnPointSection(lvl)
	editorLevelUISection(lvl)
	editorEnvironmentSection(lvl)
	editorPlayerSection(lvl)
	editorEntityProperties(lvl)
	editorVolumePropertiesSection()
	editorSpritePropertiesSection()
	editorSceneryPropertiesSection()
	// Not selection-driven, unlike everything around it: the brush block is
	// about the *next* click rather than about anything selected, which is why
	// it draws whenever the brush has a model — see editor_scenery_panel.go.
	editorSceneryBrushSection()
	editorEmitterPropertiesSection()
	editorSoundPropertiesSection()
	editorLightPropertiesSection(lvl)
}

// editor3DSectorPropertiesSection draws the selected sector — whatever a 3D
// click landed on. The block itself is shared with the sector map's panel; see
// editor_sector_panel.go.
func editor3DSectorPropertiesSection(lvl *world.Level) {
	if Editor.ActiveSectorIdx < 0 || !activeChunkIndexValid() {
		return
	}
	sch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx >= len(sch.Sectors) {
		return
	}
	s3d := &sch.Sectors[Editor.ActiveSectorIdx]

	imgui.Separator()
	editorFocusOpen(panelFocusSector, panelFocusFloor, panelFocusCeiling, panelFocusWall,
		panelFocusFloorSlab, panelFocusCeilingSlab)
	if !imgui.CollapsingHeaderV("Sector: "+s3d.ID+"##sechdr3d", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusSector)
	editorSectorPropertiesBody(lvl, sch, s3d, "3d")
}

// editorPortalFrameCombo draws the target-frame picker for a portal: which
// frame ToSector lives in when it is not this chunk's own — what makes the
// portal a *frame portal* joining two spaces that move relative to each other.
// Reports whether the value changed.
func editorPortalFrameCombo(lvl *world.Level, ch *world.Chunk, p *world.Portal) bool {
	label := p.ToFrame
	if label == "" {
		label = "(this chunk)"
	}
	changed := false
	if !imgui.BeginCombo("To Frame##ptf3d", label) {
		return false
	}
	if imgui.SelectableV("(this chunk)", p.ToFrame == "", 0, imgui.Vec2{}) && p.ToFrame != "" {
		p.ToFrame = ""
		changed = true
	}
	// "world" is always offered — a frame-owned chunk's own way to name the
	// stationary world it is riding through, whether or not any frame is
	// carrying anything right now.
	if imgui.SelectableV("world", p.ToFrame == "world", 0, imgui.Vec2{}) && p.ToFrame != "world" {
		p.ToFrame = "world"
		changed = true
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.Vehicle == nil || !e.Vehicle.HasInterior() || e.ID == ch.FrameOwner {
			continue
		}
		if imgui.SelectableV(e.ID, p.ToFrame == e.ID, 0, imgui.Vec2{}) && p.ToFrame != e.ID {
			p.ToFrame = e.ID
			changed = true
		}
	}
	imgui.EndCombo()
	return changed
}

// The sector block itself lives in editor_sector_panel.go, drawn by both
// editors; the map's corner block (editor2DVertexGroup) stays in editor_ui.go
// with the rest of the map's own drawing, and the biome brush in
// editor_biome.go. This file wires them into the shared Properties order above.
