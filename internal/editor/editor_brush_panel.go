package editor

// editor_brush_panel.go — the Properties block for the selected brush and face.
//
// A sector needed six of these, one per kind of face: a floor had a height and
// a skirt, a wall had two endpoints and a collision flag, a portal had a target
// sector and no texture of its own. Each got its own section, and the panel
// grew a switch to decide which to draw.
//
// A brush has one kind of face, so there is one section. What is left of the
// old split is the nesting: the brush's own properties (ID, solidity, tags and
// script) sit above the selected face's, because the two are genuinely
// different objects — deleting a face is not a thing, and a script addresses
// the brush.
//
// The fields bind to the *primary* selected face only. There is no honest way
// for a slider to edit twelve floats at once, so bulk edits live in the
// right-click menu instead and editorFaceSelectionNote points at them.

import (
	"fmt"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorBrushPropertiesSection draws the selected brush, then the selected
// face inside it. Nothing at all when no face is selected — the panel's one
// rule, that it shows only what the selection can act on.
func editorBrushPropertiesSection() {
	ch, b := faceSelBrush(primaryFaceSel())
	f := faceSelFace(primaryFaceSel())
	if ch == nil || b == nil || f == nil {
		return
	}

	imgui.Separator()
	editorFocusOpen(panelFocusBrush, panelFocusFace)
	if !imgui.CollapsingHeaderV("Brush: "+b.ID+"##brushhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusBrush)

	editorFaceSelectionNote()

	editorPropGroupOpen("Brush##brushgrp", func() {
		id := b.ID
		if imgui.InputText("ID##brushid", &id) && id != "" && id != b.ID {
			EditorPushUndo()
			// Level-unique, not chunk-unique (world/identity.go), so a rename
			// that collides with a brush in another chunk has to be caught too.
			b.ID = uniqueBrushIDInLevelFor(id, primaryFaceSel())
		}
		if imgui.Checkbox("Solid##brushsolid", &b.Solid) {
			EditorPushUndo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Off makes the whole brush decorative — a body walks\n" +
				"straight through it. For one face only, use the face's\n" +
				"Collision box below.")
		}
		convex := b.IsConvex()
		if !convex {
			editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.55, Z: 0.3, W: 1},
				"This brush is not convex. Collision reads its corners as a hull and "+
					"the compiler clips its faces as half-spaces, so both will be wrong "+
					"about the dent. Pull the stray corner back in.")
		}
		imgui.Text(fmt.Sprintf("%d corners, %d faces", len(b.Verts), len(b.Faces)))
		editorScriptDataUI(&b.ScriptData, "brush", "This brush")
	})

	editorPropGroupOpen(fmt.Sprintf("Face %d##brushfacegrp", Editor.ActiveFaceIdx), func() {
		changed := false
		changed = textureCombo("Texture##bftex", &f.Texture, false) || changed
		changed = textureCombo("Blend Texture##bfbtex", &f.BlendTexture, true) || changed
		if f.BlendTexture != "" {
			changed = textureCombo("Blend Mask##bfbmask", &f.BlendMask, true) || changed
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Grayscale: black is Texture, white is Blend Texture.")
			}
		}
		changed = editorDragFloat("Alpha##bfalpha", &f.Alpha, 0, 1, "%.2f") || changed
		changed = imgui.Checkbox("Double Sided##bfds", &f.DoubleSided) || changed

		drawn := !f.NoDraw
		if imgui.Checkbox("Drawn##bfdrawn", &drawn) {
			f.NoDraw = !drawn
			changed = true
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Off builds no geometry for this face while the brush\n" +
				"still collides — for a side buried against a neighbour.")
		}
		solid := !f.NoCollide
		if imgui.Checkbox("Collision##bfcol", &solid) {
			f.NoCollide = !solid
			changed = true
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Off makes this one face walk-through while the rest\n" +
				"of the brush stays solid — a window opening in a wall.")
		}

		editorPropGroup("UV##bfuv", 0, func() {
			changed = editorDragFloat("Offset U##bfuo", &f.UVOffset[0], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Offset V##bfvo", &f.UVOffset[1], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Scale U##bfus", &f.UVScale[0], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Scale V##bfvs", &f.UVScale[1], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Rotation##bfur", &f.UVRotation, -6.2832, 6.2832, "%.3f") || changed
		})

		editorPropGroup("Shading##bfshade", 0, func() {
			changed = imgui.Checkbox("Smooth##bfsm", &f.Smooth) || changed
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Weld the normals along shared edges, so neighbouring\n" +
					"faces read as one curved surface.")
			}
			if f.Smooth {
				changed = editorDragFloat("Crease Angle##bfsa", &f.SmoothAngle, 0, 180, "%.1f") || changed
			}
		})

		changed = editorDetailPanel(fmt.Sprintf("bf%d", Editor.ActiveFaceIdx), &f.Detail) || changed

		if changed {
			// One reload for the whole block: a face edit is a handful of
			// fields and the panel redraws every frame, so rebuilding per
			// field would re-upload the brush a dozen times a frame while a
			// slider is held.
			_ = b.Load()
			ch.RecomputeBounds()
		}
	})

	imgui.Separator()
	imgui.Text("E extrudes, I insets, B subdivides — see the right-click menu.")
}

// primaryFaceSel is the face the panel edits: the first of the selection, or an
// unresolvable one when nothing is selected.
func primaryFaceSel() faceSel {
	if len(Editor.SelectedFaces) == 0 {
		return faceSel{chunkIdx: -1, brushIdx: -1, faceIdx: -1}
	}
	return Editor.SelectedFaces[0]
}

// uniqueBrushIDInLevelFor returns want, or want with a numeric suffix, such
// that no brush anywhere in the level other than the one at sel is using it.
//
// Chunk-scoped uniqueness is not enough: a brush is addressed by a bare name in
// scripts, with no chunk qualifier (world/identity.go), so two brushes in
// different chunks sharing an ID would make that name ambiguous.
func uniqueBrushIDInLevelFor(want string, sel faceSel) string {
	lvl := Editor.level
	if lvl == nil {
		return want
	}
	taken := func(id string) bool {
		for ci := range lvl.Chunks {
			for bi := range lvl.Chunks[ci].Brushes {
				if ci == sel.chunkIdx && bi == sel.brushIdx {
					continue
				}
				if lvl.Chunks[ci].Brushes[bi].ID == id {
					return true
				}
			}
		}
		return false
	}
	if !taken(want) {
		return want
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", want, n)
		if !taken(id) {
			return id
		}
	}
}
