package editor

// editor_sprite_panel.go — the Sprites section of the editor's side panel.
//
// The volume panel's shape, with the shape swapped for the three things a
// billboard needs and nothing else does: which way it faces, how it blends, and
// what it rides.
//
// The level's sprite list is in the Explorer (editor_panel_explorer.go), with
// everything else the level contains; this file is the Add-menu entry and the
// selected sprite's properties.

import (
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/world"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorSpritePropertiesSection draws the selected sprite's fields. Nothing is
// drawn when no sprite is selected, so the caller can call it unconditionally.
func editorSpritePropertiesSection() {
	s := activeSprite()
	if s == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusSprite)
	if !imgui.CollapsingHeaderV("Sprite: "+s.ID+"##sprhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusSprite)
	editorSpriteProperties(s)
}

// editorSpriteAddMenu draws the sprite half of the Explorer's Add menu: the ID
// and texture the next one takes, and the row that arms placement.
func editorSpriteAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newspr", &Editor.NewSpriteID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"sprite\".\nMade unique on placement.")
	}
	imgui.SetNextItemWidth(160)
	textureCombo("Texture##newsprtex", &Editor.NewSpriteTexture, true)
	imgui.Separator()
	if imgui.MenuItem("Place By Clicking") {
		editorArmSpritePlacement()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A quad that turns to face the camera.\nDropped standing on the surface you click.")
	}
}

// editorSpriteProperties draws the selected sprite's editable state, in the
// same group order an entity and a volume use.
func editorSpriteProperties(s *world.Sprite) {
	editorPropGroupOpen("Identity##spridgrp", func() { editorSpriteIdentity(s) })
	editorPropGroupOpen("Transform##sprtfgrp", func() { editorSpriteTransform(s) })
	editorPropGroupOpen("Appearance##sprappgrp", func() { editorSpriteAppearance(s) })
	editorPropGroup("Sheet##sprsheetgrp", 0, func() { editorSpriteSheet(s) })
	editorPropGroup("Attach##sprattachgrp", 0, func() { editorSpriteAttach(s) })
	editorScriptDataUI(&s.ScriptData, "spr", strconv.Quote(s.ID))
	editorPropGroup("Actions##spractions", 0, func() { editorSpriteActions(s) })
}

// editorSpriteIdentity draws the ID field and the visible toggle.
//
// The rename commits on Enter rather than per keystroke, for the reason a
// volume's does: an ID is what a script addresses, and renaming through every
// half-typed spelling would fire a uniqueness check against each one.
func editorSpriteIdentity(s *world.Sprite) {
	id := s.ID
	if imgui.InputTextV("ID##sprid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != s.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("A sprite needs an ID for a script to address it")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			s.ID = id
		}
	}

	if imgui.Checkbox("Visible##sprvisible", &s.Visible) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A hidden sprite is not drawn, but stays selectable here\nand from the Explorer. Scripts toggle this.")
	}
}

// editorSpriteTransform draws position, size and rotation.
//
// Rotation is one field or three depending on the billboard mode, because in
// the two billboarded modes the other two axes are simply not read — showing
// them would offer a control that does nothing, which is worse than showing
// none.
func editorSpriteTransform(s *world.Sprite) {
	pos := [3]float32{s.Position.X(), s.Position.Y(), s.Position.Z()}
	if editorDragFloat3("Position##sprpos", &pos, 0, 0, "%.3f") {
		s.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if !s.Attach.IsZero() && imgui.IsItemHovered() {
		imgui.SetTooltip("Local to " + s.Attach.Describe() + ", not the world.")
	}

	editorDragFloat("Width##sprw", &s.Width, 0.01, 1000, "%.3f")
	editorDragFloat("Height##sprh", &s.Height, 0.01, 1000, "%.3f")

	if s.Billboard == gfx.BillboardFixed {
		rot := vec3ToDegrees(s.Rotation)
		if editorDragFloat3Step("Rotation (deg)##sprrot", &rot, -360, 360, "%.1f", 1) {
			s.Rotation = vec3FromDegrees(rot)
		}
	} else {
		roll := mgl32.RadToDeg(s.Rotation.Z())
		if editorDragFloatStep("Roll (deg)##sprroll", &roll, -360, 360, "%.1f", 1) {
			s.Rotation = mgl32.Vec3{0, 0, mgl32.DegToRad(roll)}
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A spin in the plane of the quad. The other two axes\nare not read while the sprite is billboarded.")
		}
	}
}

// editorSpriteAppearance draws texture, billboard mode, blend mode and the
// lighting toggles.
func editorSpriteAppearance(s *world.Sprite) {
	if textureCombo("Texture##sprtex", &s.Texture, false) {
		EditorPushUndo()
		if err := s.EnsureSpriteTexture(); err != nil {
			editorSetStatus("Sprite %s: %v", s.ID, err)
		}
	}

	if editorSpriteBillboardCombo(s) {
		EditorPushUndo()
	}
	if editorSpriteBlendCombo(s) {
		EditorPushUndo()
	}

	tint := [3]float32{s.Tint.X(), s.Tint.Y(), s.Tint.Z()}
	if imgui.ColorEdit3("Tint##sprtint", &tint) {
		s.Tint = mgl32.Vec3{tint[0], tint[1], tint[2]}
	}
	editorDragFloat("Alpha##spralpha", &s.Alpha, 0, 1, "%.2f")

	if imgui.Checkbox("Lit##sprlit", &s.Lit) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Take the scene's lights and shadows.\nLeave off for anything emissive - a flame lit by\nthe sun looks wrong.")
	}
	if imgui.Checkbox("Casts shadow##sprcast", &s.CastShadows) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Draw into the shadow maps, facing the light rather\nthan the camera. Convincing for solid sprites;\nleave off for glows.")
	}
}

// editorSpriteBillboardCombo draws the billboard-mode picker, with a line on
// each mode — the choice is not guessable from its name, and picking wrong is
// the most common way a sprite ends up looking broken.
func editorSpriteBillboardCombo(s *world.Sprite) bool {
	changed := false
	if imgui.BeginCombo("Billboard##sprbb", gfx.BillboardModeName(s.Billboard)) {
		for i, name := range gfx.BillboardModeNames {
			m := gfx.BillboardMode(i)
			if imgui.SelectableV(name, m == s.Billboard, 0, imgui.Vec2{}) && m != s.Billboard {
				s.Billboard = m
				changed = true
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(spriteBillboardHelp[i])
			}
		}
		imgui.EndCombo()
	}
	return changed
}

// spriteBillboardHelp is parallel to gfx.BillboardModeNames.
var spriteBillboardHelp = []string{
	"Faces the camera on both axes.\nFor things with no up of their own: glows, puffs, sparks.",
	"World up stays up; only the yaw follows the camera.\nFor anything standing on the ground - a bush must not\nlean back as the camera rises.",
	"No billboarding. The quad faces wherever Rotation puts it.\nFor a poster on a wall.",
}

// editorSpriteBlendCombo draws the blend-mode picker. The mode decides which
// pass the sprite draws in, not merely how it is sorted, which is why each one
// gets a line about what that buys.
func editorSpriteBlendCombo(s *world.Sprite) bool {
	changed := false
	if imgui.BeginCombo("Blend##sprblend", gfx.SpriteBlendName(s.Blend)) {
		for i, name := range gfx.SpriteBlendNames {
			b := gfx.SpriteBlend(i)
			if imgui.SelectableV(name, b == s.Blend, 0, imgui.Vec2{}) && b != s.Blend {
				s.Blend = b
				changed = true
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(spriteBlendHelp[i])
			}
		}
		imgui.EndCombo()
	}
	return changed
}

// spriteBlendHelp is parallel to gfx.SpriteBlendNames.
var spriteBlendHelp = []string{
	"Opaque with a hard alpha edge, drawn in the opaque pass.\nWrites depth, needs no sorting, and the edge is\nantialiased. The right default for foliage.",
	"Ordinary translucency, sorted back to front.\nDoes not write depth. For glass and ghosts.",
	"Adds light and never occludes. Order-independent, so\nnothing sorts. For fire, glows and sparks.",
}

// editorSpriteSheet draws the sprite-sheet grid and the cell to show. Collapsed
// by default: most sprites use a whole texture and never touch this.
func editorSpriteSheet(s *world.Sprite) {
	cols, rows, frame := int32(s.SheetCols), int32(s.SheetRows), int32(s.Frame)
	if imgui.DragIntV("Columns##sprcols", &cols, 1, 0, 64, "%d", 0) {
		s.SheetCols = int(cols)
	}
	if imgui.DragIntV("Rows##sprrows", &rows, 1, 0, 64, "%d", 0) {
		s.SheetRows = int(rows)
	}
	n := s.SheetCols * s.SheetRows
	imguibackend.EditorPushDisabled(n <= 0)
	if imgui.DragIntV("Cell##sprframe", &frame, 1, 0, int32(maxInt(n-1, 0)), "%d", 0) {
		s.Frame = int(frame)
	}
	imguibackend.EditorPopDisabled()
	if n <= 0 {
		editorTextMutedWrapped("Zero on either axis uses the whole texture.")
	}
}

// editorSpriteAttach draws the carrier binding. The rows cascade, because a
// bone is meaningless without the attachment it belongs to, which is meaningless
// without the entity that carries it.
func editorSpriteAttach(s *world.Sprite) {
	lvl := Editor.level
	if editorAttachUI(lvl, &s.Attach, "spr") {
		EditorPushUndo()
	}
	if s.Attach.IsZero() {
		editorTextMutedWrapped("Unattached: Position is world-space.")
	} else {
		editorTextMutedWrapped("Position is local to " + s.Attach.Describe() + ".")
	}
}

// editorSpriteActions draws the whole-object buttons.
func editorSpriteActions(s *world.Sprite) {
	if imgui.Button("Duplicate##sprdup") {
		duplicateActiveSprite()
	}
	imgui.SameLine()
	if imgui.Button("Delete##sprdel") {
		deleteActiveSprite()
	}
	_ = s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
