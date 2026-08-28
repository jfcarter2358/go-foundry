package editor

// editor_sector_panel.go — a sector's properties, grouped by what they are
// about.
//
// A sector is the editor's densest object: two horizontal faces and a ring of
// edges, each carrying a texture and two blend slots, a shapeable mesh, UV,
// smoothing, procedural detail, and its own tags/attributes/script. Drawn flat
// that is a hundred controls in one column, with only the order saying which of
// them belong to the floor and which to the ceiling.
//
// So each face is one group that folds away as a unit, holding everything about
// that face and nothing about any other — the plain fields directly, and the
// bigger subjects (Shape, Texture UV, Skirt, Detail, Scripting) as groups
// within it.
//
// Both editors draw through here. The 2D panel used to carry its own copy of
// all of this, and the copies had drifted: the map's edge block had no Pinned
// flag, no portal target frame and no scripting at all, so the same wall
// answered different questions depending on which window was asking.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// sectorFace is a floor or a ceiling as one editable thing.
//
// world.Floor and world.Ceiling are separate structs that happen to share
// nearly every field — a floor additionally hides its texture and grows a
// skirt — and Go has no way to write one function over both. Rather than keep
// two copies of the panel in step by hand (which is how the 2D and 3D versions
// drifted apart in the first place), each is projected onto this once and the
// panel is written once.
type sectorFace struct {
	kind        string // "floor" or "ceiling"
	label       string // "Floor" or "Ceiling"
	focus       string // the panelFocus* a click on this face asks the panel for
	script      *world.ScriptData
	enabled     *bool
	hidden      *bool // nil for a ceiling, which has no such field
	height      *float32
	texture     *string
	blendTex    *string
	blendMask   *string
	doubleSided *bool
	alpha       *float32
	uvOffset    *mgl32.Vec2
	uvScale     *mgl32.Vec2
	uvRotation  *float32
	smooth      *bool
	smoothAngle *float32
	detail      *world.DetailSpec
}

func sectorFloorFace(f *world.Floor) sectorFace {
	return sectorFace{
		kind: "floor", label: "Floor", focus: panelFocusFloor,
		script: &f.ScriptData, enabled: &f.Enabled, hidden: &f.Hidden,
		height: &f.Height, texture: &f.Texture, blendTex: &f.BlendTexture,
		blendMask: &f.BlendMask, doubleSided: &f.DoubleSided, alpha: &f.Alpha,
		uvOffset: &f.UVOffset, uvScale: &f.UVScale, uvRotation: &f.UVRotation,
		smooth: &f.Smooth, smoothAngle: &f.SmoothAngle, detail: &f.Detail,
	}
}

func sectorCeilingFace(c *world.Ceiling) sectorFace {
	return sectorFace{
		kind: "ceiling", label: "Ceiling", focus: panelFocusCeiling,
		script: &c.ScriptData, enabled: &c.Enabled,
		height: &c.Height, texture: &c.Texture, blendTex: &c.BlendTexture,
		blendMask: &c.BlendMask, doubleSided: &c.DoubleSided, alpha: &c.Alpha,
		uvOffset: &c.UVOffset, uvScale: &c.UVScale, uvRotation: &c.UVRotation,
		smooth: &c.Smooth, smoothAngle: &c.SmoothAngle, detail: &c.Detail,
	}
}

// editorSectorPropertiesBody draws everything inside a sector's properties
// header: what the sector is, then a group per face, then the sector's own
// scripting. idSuffix keeps the two editors' imgui IDs apart.
func editorSectorPropertiesBody(lvl *world.Level, ch *world.Chunk, s *world.Sector, idSuffix string) {
	imgui.InputText("ID##sid"+idSuffix, &s.ID)
	editorFaceSelectionNote()

	editorSectorSurfaceGroup(ch, s, sectorFloorFace(&s.Floor), idSuffix)
	editorSectorSurfaceGroup(ch, s, sectorCeilingFace(&s.Ceiling), idSuffix)
	editorSectorEdgeGroup(lvl, ch, s, idSuffix)

	// The sector's own set, after the faces that each carry theirs. Last
	// because it is the least specific: an edit here is about the room, not
	// about the surface being looked at.
	editorScriptDataUI(&s.ScriptData, "sec"+idSuffix, "This sector")
}

// editorSectorSurfaceGroup draws one of a sector's two horizontal faces.
func editorSectorSurfaceGroup(ch *world.Chunk, s *world.Sector, f sectorFace, idSuffix string) {
	id := f.kind + idSuffix
	uvID := f.label + idSuffix

	editorFocusOpen(f.focus)
	editorPropGroup(f.label+"##grp"+id, 0, func() {
		editorFocusScroll(f.focus)

		changed := false
		changed = imgui.Checkbox("Enabled##en"+id, f.enabled) || changed
		if f.hidden != nil {
			// Read at draw time, so no rebuild — the same reason Double Sided
			// below is not counted as a change either.
			imgui.Checkbox("Hide Texture##hid"+id, f.hidden)
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Stop drawing this face's own texture, keeping the\n" +
					"surface itself: what a biome ground patch covers.")
			}
		}
		changed = editorDragFloat("Height##h"+id, f.height, 0, 0, "%.3f") || changed
		changed = textureCombo("Texture##tex"+id, f.texture, false) || changed
		changed = textureCombo("Blend Texture##btex"+id, f.blendTex, true) || changed
		changed = textureCombo("Blend Mask##bmask"+id, f.blendMask, true) || changed
		imgui.Checkbox("Double Sided##ds"+id, f.doubleSided)
		changed = editorDragFloat("Alpha##a"+id, f.alpha, 0, 1, "%.2f") || changed
		if editorSmoothPanel(uvID, f.smooth, f.smoothAngle) {
			changed = true
		}
		if changed {
			_ = world.ReloadSector(ch, s)
		}

		editorSurfacePanel(ch, s, surfaceFaceSel(Editor.ActiveChunkIdx, Editor.ActiveSectorIdx, f.kind))
		if editorUVControls(uvID, f.uvOffset, f.uvScale, f.uvRotation) {
			_ = world.ReloadSector(ch, s)
		}
		if f.kind == "floor" {
			editorSkirtPanel(ch, s)
		}
		editorDetailPanel(uvID, f.detail)
		editorScriptDataUI(f.script, id, "This "+f.kind)
	})
}

// editorSectorEdgeGroup draws the selected edge — a wall or a portal — as one
// group alongside the two faces.
//
// Only the selected one: a sector has as many edges as it has corners, and
// picking between them is the viewport's job and the map's, not this block's.
func editorSectorEdgeGroup(lvl *world.Level, ch *world.Chunk, s *world.Sector, idSuffix string) {
	kind, idx := Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx

	switch {
	case kind == "wall" && idx >= 0 && idx < len(s.Walls):
		editorSectorWallGroup(ch, s, &s.Walls[idx], idx, idSuffix)
	case kind == "portal" && idx >= 0 && idx < len(s.Portals):
		editorSectorPortalGroup(lvl, ch, s, &s.Portals[idx], idSuffix)
	default:
		editorTextMuted("No edge selected.")
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Click a wall in the 3D view, or an edge line on the map,\n" +
				"and it appears here beside the floor and the ceiling.")
		}
	}
}

func editorSectorWallGroup(ch *world.Chunk, s *world.Sector, w *world.Wall, idx int, idSuffix string) {
	editorFocusOpen(panelFocusWall)
	// Named by ID rather than index: the index shifts under nearly every edit,
	// and the ID is what a script addresses.
	editorPropGroup("Wall "+world.EdgeIDDisplay(w.ID)+"##grpwall"+idSuffix, 0, func() {
		editorFocusScroll(panelFocusWall)
		editorTextMuted("A script reaches this edge as @" + s.ID + ".edge." + w.ID)
		editorEdgeKindCombo(ch, s, idSuffix)

		changed := false
		changed = textureCombo("Texture##wt"+idSuffix, &w.Texture, false) || changed
		changed = textureCombo("Blend Texture##wbt"+idSuffix, &w.BlendTexture, true) || changed
		changed = textureCombo("Blend Mask##wbm"+idSuffix, &w.BlendMask, true) || changed
		changed = imgui.Checkbox("Collision##wc"+idSuffix, &w.Collision) || changed
		changed = imgui.Checkbox("Pinned##wp"+idSuffix, &w.Pinned) || changed
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Portal inference will not convert this wall.")
		}
		changed = imgui.Checkbox("Visible##wv"+idSuffix, &w.Visible) || changed
		// The wall mesh bakes DoubleSided in, so this needs the rebuild below —
		// unlike floor/ceiling, which read the flag at draw time.
		changed = imgui.Checkbox("Double Sided##wds"+idSuffix, &w.DoubleSided) || changed
		changed = editorDragFloat("Alpha##wa"+idSuffix, &w.Alpha, 0, 1, "%.2f") || changed
		if editorSmoothPanel("Wall"+idSuffix, &w.Smooth, &w.SmoothAngle) {
			changed = true
		}
		if changed {
			_ = world.ReloadSector(ch, s)
			ch.RebuildGaps()
		}

		editorSurfacePanel(ch, s, edgeFaceSel(Editor.ActiveChunkIdx, Editor.ActiveSectorIdx, "wall", idx))
		if editorUVControls("Wall"+idSuffix, &w.UVOffset, &w.UVScale, &w.UVRotation) {
			_ = world.ReloadSector(ch, s)
		}
		editorDetailPanel("Wall"+idSuffix, &w.Detail)
		editorScriptDataUI(&w.ScriptData, "wall"+idSuffix, "This wall")
	})
}

func editorSectorPortalGroup(lvl *world.Level, ch *world.Chunk, s *world.Sector, p *world.Portal, idSuffix string) {
	editorFocusOpen(panelFocusWall)
	editorPropGroup("Portal "+world.EdgeIDDisplay(p.ID)+"##grpportal"+idSuffix, 0, func() {
		editorFocusScroll(panelFocusWall)
		editorTextMuted("A script reaches this edge as @" + s.ID + ".edge." + p.ID)
		editorEdgeKindCombo(ch, s, idSuffix)

		changed := false
		changed = imgui.InputText("To Sector##pts"+idSuffix, &p.ToSector) || changed
		changed = editorPortalFrameCombo(lvl, ch, p) || changed
		changed = imgui.Checkbox("Draw Gap##pdg"+idSuffix, &p.DrawGap) || changed
		if p.DrawGap {
			changed = textureCombo("Gap Texture##pgt"+idSuffix, &p.GapTexture, true) || changed
		}
		changed = imgui.Checkbox("Visible##pv"+idSuffix, &p.Visible) || changed
		// Same as walls: the gap quad bakes DoubleSided in.
		changed = imgui.Checkbox("Double Sided##pds"+idSuffix, &p.DoubleSided) || changed
		changed = editorDragFloat("Alpha##pa"+idSuffix, &p.Alpha, 0, 1, "%.2f") || changed
		if changed {
			_ = world.ReloadSector(ch, s)
			ch.RebuildGaps()
		}
		editorScriptDataUI(&p.ScriptData, "portal"+idSuffix, "This portal")
	})
}
