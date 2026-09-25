package editor

// editor_decal_panel.go — the Decals section of the editor's side panel.
//
// The sprite panel's shape with the billboard and sheet groups replaced by the
// three fields that decide whether a projection lands where it was meant: the
// depth it reaches, the angle past which it gives up, and the push that keeps it
// off its surface.
//
// Those three get more explanation than most fields here get, because they are the
// ones an author will reach for when a decal looks wrong, and none of them is
// guessable from its name. The other two things that make a decal look wrong —
// facing the wrong way, or floating in mid-air — are not fields at all: they are
// answered by placing it on a surface and by the cage the viewport draws around
// the selection.
//
// The level's decal list is in the Explorer (editor_panel_explorer.go); this file
// is the Add-menu entry and the selected decal's properties.

import (
	"strconv"

	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorDecalPropertiesSection draws the selected decal's fields. Nothing is
// drawn when no decal is selected, so the caller can call it unconditionally.
func editorDecalPropertiesSection() {
	d := activeDecal()
	if d == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusDecal)
	if !imgui.CollapsingHeaderV("Decal: "+d.ID+"##dechdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusDecal)
	editorDecalProperties(d)
}

// editorDecalAddMenu draws the decal half of the Explorer's Add menu: the ID and
// texture the next one takes, and the row that arms placement.
func editorDecalAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newdec", &Editor.NewDecalID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"decal\".\nMade unique on placement.")
	}
	imgui.SetNextItemWidth(160)
	textureCombo("Texture##newdectex", &Editor.NewDecalTexture, true)
	imgui.Separator()
	if imgui.MenuItem("Place By Clicking") {
		editorArmDecalPlacement()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A texture projected onto the level's own surfaces.\n" +
			"Click a surface: the decal faces the way that surface does,\n" +
			"so it lies flat on it and bends around its corners.")
	}
}

// editorDecalProperties draws the selected decal's editable state, in the same
// group order a sprite and an entity use.
func editorDecalProperties(d *world.Decal) {
	editorPropGroupOpen("Identity##decidgrp", func() { editorDecalIdentity(d) })
	editorPropGroupOpen("Transform##dectfgrp", func() { editorDecalTransform(d) })
	editorPropGroupOpen("Appearance##decappgrp", func() { editorDecalAppearance(d) })
	editorPropGroup("Projection##decprojgrp", 0, func() { editorDecalProjection(d) })
	editorScriptDataUI(&d.ScriptData, "dec", strconv.Quote(d.ID))
	editorPropGroup("Actions##decactions", 0, func() { editorDecalActions(d) })
}

// editorDecalIdentity draws the ID field and the visible toggle.
//
// The rename commits on Enter rather than per keystroke, for the reason a
// sprite's does: an ID is what a script addresses, and renaming through every
// half-typed spelling would fire a uniqueness check against each one.
func editorDecalIdentity(d *world.Decal) {
	id := d.ID
	if imgui.InputTextV("ID##decid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != d.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("A decal needs an ID for a script to address it")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			d.ID = id
		}
	}

	if imgui.Checkbox("Visible##decvisible", &d.Visible) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A hidden decal is not drawn, but keeps its mesh — so a\n" +
			"script showing it again costs nothing. Stays selectable here.")
	}
}

// editorDecalTransform draws position, size and rotation.
//
// All three rotation axes, unlike a billboarded sprite's single roll: a decal's
// rotation IS its projection direction, so every axis is read and every one
// matters.
func editorDecalTransform(d *world.Decal) {
	pos := [3]float32{d.Position.X(), d.Position.Y(), d.Position.Z()}
	if editorDragFloat3("Position##decpos", &pos, 0, 0, "%.3f") {
		d.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The centre of the projection box. Moving it re-projects\nthe decal onto whatever is there now.")
	}

	editorDragFloat("Width##decw", &d.Width, 0.01, 1000, "%.3f")
	editorDragFloat("Height##dech", &d.Height, 0.01, 1000, "%.3f")

	rot := vec3ToDegrees(d.Rotation)
	if editorDragFloat3Step("Rotation (deg)##decrot", &rot, -360, 360, "%.1f", 1) {
		d.Rotation = vec3FromDegrees(rot)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Which way the decal faces OUT of its surface, plus the\n" +
			"spin of the image about that. Placing a decal by clicking\n" +
			"a surface sets this, which is usually the last word on it.")
	}
	if imgui.Button("Face The Camera##decfacecam") {
		editorDecalFaceCamera(d)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Aim the projection back along the view. For a decal that has\n" +
			"ended up facing the wrong way: line the view up with the\n" +
			"surface you want it on, then press this.")
	}
}

// editorDecalFaceCamera aims the decal back along the view direction, keeping the
// roll it already had.
//
// The recovery for the one mistake this object makes that no other does. A decal
// facing the wrong way draws nothing at all, which gives an author nothing to
// grab and nothing to read; re-aiming from the camera needs no numbers and no
// second click.
func editorDecalFaceCamera(d *world.Decal) {
	// The view direction taken as the ray through the middle of the viewport,
	// rather than off gfx.Cam: the workspace owns the camera, so this is the one
	// answer that is right in the entity editor's window as well as the level's.
	w, h := ws().viewSize()
	_, dir := ws().ray(w/2, h/2)
	if dir.LenSqr() < 1e-12 {
		return
	}
	EditorPushUndo()
	// Back along the view: the decal has to look AT the eye to project onto what
	// the eye is looking at.
	d.Rotation = world.DecalRotationFor(dir.Mul(-1), d.Rotation.Z())
}

// editorDecalAppearance draws texture, material, blend mode, tint, alpha and the
// lighting toggle.
func editorDecalAppearance(d *world.Decal) {
	if materialCombo("Material##decmat", &d.Material, material.DomainSurface) {
		EditorPushUndo()
	}
	if textureCombo("Texture##dectex", &d.Texture, false) {
		EditorPushUndo()
		if err := d.EnsureDecalTexture(); err != nil {
			editorSetStatus("Decal %s: %v", d.ID, err)
		}
	}

	if editorDecalBlendCombo(d) {
		EditorPushUndo()
	}

	tint := [3]float32{d.Tint.X(), d.Tint.Y(), d.Tint.Z()}
	if imgui.ColorEdit3("Tint##dectint", &tint) {
		d.Tint = mgl32.Vec3{tint[0], tint[1], tint[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies the texture. One grey scorch texture tinted\neight ways is eight different marks.")
	}
	editorDragFloat("Alpha##decalpha", &d.Alpha, 0, 1, "%.2f")

	order := int32(d.Order)
	if imgui.DragIntV("Order##decorder", &order, 1, -64, 64, "%d", 0) {
		d.Order = int(order)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Which of two overlapping decals lies on top: higher wins.\nEqual orders draw in the order they were placed.")
	}

	if imgui.Checkbox("Unlit##decunlit", &d.Unlit) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Ignore the scene's lights. Leave OFF for almost everything:\n" +
			"a decal is part of the surface it lies on, and one that stayed\n" +
			"bright in a dark room reads as a light source.")
	}
}

// editorDecalBlendCombo draws the blend-mode picker, with a line on each mode.
// The choice is not guessable from its name and picking wrong is the commonest
// way a decal ends up looking like a sticker.
func editorDecalBlendCombo(d *world.Decal) bool {
	changed := false
	if imguibackend.BeginCombo("Blend##decblend", gfx.DecalBlendName(d.Blend)) {
		for i, name := range gfx.DecalBlendNames {
			b := gfx.DecalBlend(i)
			if imguibackend.SelectableV(name, b == d.Blend, 0, imgui.Vec2{}) && b != d.Blend {
				d.Blend = b
				changed = true
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(decalBlendHelp[i])
			}
		}
		imguibackend.EndCombo()
	}
	return changed
}

// decalBlendHelp is parallel to gfx.DecalBlendNames.
var decalBlendHelp = []string{
	"Lays the texture over the surface, its alpha deciding how much\nof each shows. For posters, signs, bullet holes, wet splatter.",
	"Darkens the surface by the texture instead of covering it, so one\nmark reads correctly on every colour it lands on. For scorch,\nsoot, dirt and rub - most of what a fight leaves behind.",
	"Adds light and never darkens. For a glowing rune, a holographic\nstencil, a hot ember scar.",
}

// editorDecalProjection draws the three fields that decide where the projection
// lands. Collapsed by default: a decal placed by clicking a surface usually needs
// none of them.
func editorDecalProjection(d *world.Decal) {
	editorDragFloat("Depth##decdepth", &d.Depth, 0.001, 1000, "%.3f")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far along its facing the projection reaches, measured\n" +
			"either side of the position. Small is right: a deep decal on a\n" +
			"wall also prints itself on whatever stands behind the wall.\n" +
			"Raise it to wrap a mark around a corner or over a step.")
	}

	angle := d.EffectiveMaxAngle()
	if editorDragFloatStep("Max angle (deg)##decangle", &angle, 1, 179, "%.0f", 1) {
		d.MaxAngle = angle
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far a surface may lean from the decal's facing and still\n" +
			"be painted. Past it the projection is so oblique the texture\n" +
			"smears into streaks, so it is dropped instead. Lower this to\n" +
			"stop a wall decal creeping onto the floor.")
	}

	push := d.EffectivePush()
	if editorDragFloat("Push##decpush", &push, -0.1, 0.1, "%.4f") {
		d.Push = push
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Lifts the decal off its surface along the surface's normal.\n" +
			"The renderer already offsets decals in depth; raise this only\n" +
			"if one still shimmers where it is seen almost edge-on.\n" +
			"Negative sinks it in.")
	}

	pieces := len(d.Pieces)
	if pieces == 0 {
		editorTextMutedWrapped("Projecting onto nothing. Either it is in mid-air, " +
			"facing away from its surface, or Max angle is too low for the surface it is on.")
	} else {
		editorTextMutedWrapped("Projected onto " + strconv.Itoa(pieces) + " piece(s) of the world.")
	}
}

// editorDecalActions draws the whole-object buttons.
func editorDecalActions(d *world.Decal) {
	if imgui.Button("Duplicate##decdup") {
		duplicateActiveDecal()
	}
	imgui.SameLine()
	if imgui.Button("Delete##decdel") {
		deleteActiveDecal()
	}
	_ = d
}
