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
// own selection unit is (a 3D face/vertex, or the 2D sector map's sector list
// and vertex/edge blocks), then Entity, Volume, and Lights.

import (
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorPropertiesSection draws every properties block, in order.
func editorPropertiesSection(lvl *world.Level, kind editorPanelKind) {
	editorChunkPropertiesSection(lvl)

	if kind == panelKind2D {
		if ch := activeChunk(); ch != nil {
			editor2DSectorSections(ch)
			biomePanelSection()
		}
	} else {
		editorWaterSection(lvl)
		editor3DSectorPropertiesSection(lvl)
		editor3DVertexPanel(lvl)
	}

	editorEntityProperties(lvl)
	editorVolumePropertiesSection()
	editorLightPropertiesSection(lvl)
}

// editor3DSectorPropertiesSection draws the selected sector's floor, ceiling
// and edge — whatever a 3D click landed on. Extracted from Editor3DPanel's old
// inline body so it can take its place in Properties like everything else here.
func editor3DSectorPropertiesSection(lvl *world.Level) {
	if Editor.SpawnSelected || Editor.ActiveSectorIdx < 0 ||
		!activeChunkIndexValid() {
		return
	}
	sch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx >= len(sch.Sectors) {
		return
	}
	s3d := &sch.Sectors[Editor.ActiveSectorIdx]

	imgui.Separator()
	editorFocusOpen(panelFocusFloor, panelFocusCeiling, panelFocusWall)
	if !imgui.CollapsingHeaderV("Sector: "+s3d.ID, imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	imgui.InputText("ID##sid3d", &s3d.ID)
	editorFaceSelectionNote()
	// The sector's own tags, attributes and script. Its floor, ceiling and
	// edges each carry their own set below.
	editorScriptDataUI(&s3d.ScriptData, "sec3d", "This sector")

	editorFocusOpen(panelFocusFloor)
	if imgui.TreeNode("Floor##3df") {
		editorFocusScroll(panelFocusFloor)
		changed := false
		changed = imgui.Checkbox("Enabled##fe3d", &s3d.Floor.Enabled) || changed
		imgui.Checkbox("Hide Texture##fhid3d", &s3d.Floor.Hidden)
		changed = editorDragFloat("Height##fh3d", &s3d.Floor.Height, 0, 0, "%.3f") || changed
		changed = textureCombo("Texture##ft3d", &s3d.Floor.Texture, false) || changed
		changed = textureCombo("Blend Texture##fbt3d", &s3d.Floor.BlendTexture, true) || changed
		changed = textureCombo("Blend Mask##fbm3d", &s3d.Floor.BlendMask, true) || changed
		changed = imgui.Checkbox("Terrain##fter3d", &s3d.Floor.Terrain) || changed
		imgui.Checkbox("Double Sided##fds3d", &s3d.Floor.DoubleSided)
		changed = editorDragFloat("Alpha##fa3d", &s3d.Floor.Alpha, 0, 1, "%.2f") || changed
		if changed {
			_ = world.ReloadSector(sch, s3d)
		}
		imgui.TreePop()
	}
	if imgui.Button("Edit Floor Surface##floorSurf3d") {
		EnterSurface2D("floor")
	}
	if editorUVControls("Floor", &s3d.Floor.UVOffset, &s3d.Floor.UVScale, &s3d.Floor.UVRotation) {
		_ = world.ReloadSector(sch, s3d)
	}
	editorSurfaceExtrusionPanel(sch, s3d, "Floor", &s3d.Floor.Extrusions)
	editorTerrainPanel(sch, s3d, "Floor", &s3d.Floor.Terrain, &s3d.Floor.TerrainPoints)
	if editorSmoothPanel("Floor", &s3d.Floor.Smooth, &s3d.Floor.SmoothAngle) {
		_ = world.ReloadSector(sch, s3d)
	}
	editorDetailPanel("Floor", &s3d.Floor.Detail)
	editorScriptDataUI(&s3d.Floor.ScriptData, "floor3d", "This floor")

	editorFocusOpen(panelFocusCeiling)
	if imgui.TreeNode("Ceiling##3dc") {
		editorFocusScroll(panelFocusCeiling)
		changed := false
		changed = imgui.Checkbox("Enabled##ce3d", &s3d.Ceiling.Enabled) || changed
		changed = editorDragFloat("Height##ch3d", &s3d.Ceiling.Height, 0, 0, "%.3f") || changed
		changed = textureCombo("Texture##ct3d", &s3d.Ceiling.Texture, false) || changed
		changed = textureCombo("Blend Texture##cbt3d", &s3d.Ceiling.BlendTexture, true) || changed
		changed = textureCombo("Blend Mask##cbm3d", &s3d.Ceiling.BlendMask, true) || changed
		changed = imgui.Checkbox("Terrain##cter3d", &s3d.Ceiling.Terrain) || changed
		imgui.Checkbox("Double Sided##cds3d", &s3d.Ceiling.DoubleSided)
		changed = editorDragFloat("Alpha##ca3d", &s3d.Ceiling.Alpha, 0, 1, "%.2f") || changed
		if changed {
			_ = world.ReloadSector(sch, s3d)
		}
		imgui.TreePop()
	}
	if imgui.Button("Edit Ceiling Surface##ceilSurf3d") {
		EnterSurface2D("ceiling")
	}
	if editorUVControls("Ceiling", &s3d.Ceiling.UVOffset, &s3d.Ceiling.UVScale, &s3d.Ceiling.UVRotation) {
		_ = world.ReloadSector(sch, s3d)
	}
	editorSurfaceExtrusionPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Extrusions)
	editorTerrainPanel(sch, s3d, "Ceiling", &s3d.Ceiling.Terrain, &s3d.Ceiling.TerrainPoints)
	if editorSmoothPanel("Ceiling", &s3d.Ceiling.Smooth, &s3d.Ceiling.SmoothAngle) {
		_ = world.ReloadSector(sch, s3d)
	}
	editorDetailPanel("Ceiling", &s3d.Ceiling.Detail)
	editorScriptDataUI(&s3d.Ceiling.ScriptData, "ceil3d", "This ceiling")

	if Editor.ActiveEdgeIdx >= 0 {
		imgui.Separator()
		editorEdgeKindCombo(sch, s3d, "3d")
	}
	switch {
	case Editor.ActiveEdgeKind == "wall" && Editor.ActiveEdgeIdx >= 0 && Editor.ActiveEdgeIdx < len(s3d.Walls):
		imgui.Separator()
		editorFocusScroll(panelFocusWall)
		w := &s3d.Walls[Editor.ActiveEdgeIdx]
		// The ID rather than the index: the index shifts under nearly every
		// edit, and the ID is what a script addresses.
		imgui.Text("Wall " + world.EdgeIDDisplay(w.ID))
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A script reaches this edge as @" + s3d.ID + ".edge." + w.ID)
		}
		changed := false
		changed = textureCombo("Texture##wt3d", &w.Texture, false) || changed
		changed = textureCombo("Blend Texture##wbt3d", &w.BlendTexture, true) || changed
		changed = textureCombo("Blend Mask##wbm3d", &w.BlendMask, true) || changed
		changed = imgui.Checkbox("Collision##wc3d", &w.Collision) || changed
		changed = imgui.Checkbox("Pinned##wp3d", &w.Pinned) || changed
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Portal inference will not convert this wall.")
		}
		changed = imgui.Checkbox("Visible##wv3d", &w.Visible) || changed
		changed = imgui.Checkbox("Double Sided##wds3d", &w.DoubleSided) || changed
		changed = editorDragFloat("Alpha##wa3d", &w.Alpha, 0, 1, "%.2f") || changed
		if changed {
			_ = world.ReloadSector(sch, s3d)
		}
		if imgui.Button("Edit Wall Surface##wallSurf3d") {
			EnterSurface2D("wall")
		}
		if editorUVControls("Wall", &w.UVOffset, &w.UVScale, &w.UVRotation) {
			_ = world.ReloadSector(sch, s3d)
		}
		editorExtrusionPanel(sch, s3d, w)
		editorTerrainPanel(sch, s3d, "Wall", &w.Terrain, &w.TerrainPoints)
		if editorSmoothPanel("Wall", &w.Smooth, &w.SmoothAngle) {
			_ = world.ReloadSector(sch, s3d)
		}
		editorDetailPanel("Wall", &w.Detail)
		editorScriptDataUI(&w.ScriptData, "wall3d", "This wall")

	case Editor.ActiveEdgeKind == "portal" && Editor.ActiveEdgeIdx >= 0 && Editor.ActiveEdgeIdx < len(s3d.Portals):
		imgui.Separator()
		p3d := &s3d.Portals[Editor.ActiveEdgeIdx]
		imgui.Text("Portal " + world.EdgeIDDisplay(p3d.ID))
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A script reaches this edge as @" + s3d.ID + ".edge." + p3d.ID)
		}
		changed := false
		changed = imgui.InputText("To Sector##pts3d", &p3d.ToSector) || changed
		changed = editorPortalFrameCombo(lvl, sch, p3d) || changed
		changed = imgui.Checkbox("Draw Gap##pdg3d", &p3d.DrawGap) || changed
		if p3d.DrawGap {
			changed = textureCombo("Gap Texture##pgt3d", &p3d.GapTexture, true) || changed
		}
		changed = imgui.Checkbox("Visible##pv3d", &p3d.Visible) || changed
		// The gap quad bakes DoubleSided in, so this needs the rebuild below —
		// unlike floor/ceiling, which read the flag at draw time.
		changed = imgui.Checkbox("Double Sided##pds3d", &p3d.DoubleSided) || changed
		changed = editorDragFloat("Alpha##pa3d", &p3d.Alpha, 0, 1, "%.2f") || changed
		if changed {
			_ = world.ReloadSector(sch, s3d)
			sch.RebuildGaps()
		}
		editorScriptDataUI(&p3d.ScriptData, "portal3d", "This portal")

	default:
		imgui.Text("Click a vertex handle to edit its position")
	}
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

// The 2D editor's own sector/vertex/edge properties (editor2DSectorSections,
// editorEdgePanel) and the biome layer's (biomePanelSection) stay in their
// existing homes — editor_ui.go and editor_biome.go — since they were already
// factored out as their own functions. This file only wires them into the
// shared Properties order above.
