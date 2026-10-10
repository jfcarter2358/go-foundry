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
// Order follows SETTINGS_LAYOUT.md: Chunk, then the geometry selection (the
// selected brush and face, or the selected vertices), then Entity, Volume, and
// Lights.

import (
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorPropGroup draws one collapsible group of related fields inside a
// properties block, and runs body when it is open.
//
// Groups are what make a properties block readable: a brush face carries a
// texture, a blend pair, UV, smoothing and procedural detail, and its brush
// carries an ID, solidity and its own tags/attributes/script — drawn flat that
// is thirty controls in one column with nothing saying which of them are about
// which. One group per thing, and every group folds away as a unit.
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
func editorPropertiesSection(lvl *world.Level) {
	editorChunkPropertiesSection(lvl)

	editorBrushPropertiesSection()
	editor3DVertexPanel(lvl)

	editorSpawnPointSection(lvl)
	editorSealSection(lvl)
	editorLevelUISection(lvl)
	editorLevelSaveSection(lvl)
	editorEnvironmentSection(lvl)
	editorPlayerSection(lvl)
	editorEntityProperties(lvl)
	editorPrefabPropertiesSection()
	editorVolumePropertiesSection()
	editorSpritePropertiesSection()
	editorDecalPropertiesSection()
	editorSceneryPropertiesSection()
	editorTreePropertiesSection()
	// Not selection-driven, unlike everything around it: the brush block is
	// about the *next* click rather than about anything selected, which is why
	// it draws whenever the brush has a model — see editor_scenery_panel.go.
	editorSceneryBrushSection()
	// The terrain tool's block, on the brush's terms: about the next stroke.
	editorTerrainToolSection()
	editorEmitterPropertiesSection()
	editorSoundPropertiesSection()
	editorEffectPropertiesSection()
	editorWaypointPropertiesSection()
	editorLightPropertiesSection(lvl)
}

// The frame-portal target picker used to live here: which frame a portal's
// destination sector was in, and so what made the portal a *frame* portal
// joining two spaces that move relative to each other. Portals went with the
// sector, and Phase 5's Volume.FrameDoorway replaces the mechanism — see the
// migration handoff's regressions list.
