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

	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorBrushPropertiesSection draws the selected brush, and — in edit mode —
// the selected face inside it. Nothing at all when neither is selected: the
// panel's one rule is that it shows only what the selection can act on.
//
// The two contexts reach it from opposite ends. Object mode selects the brush
// outright, so the brush block is all there is; edit mode selects one of its
// faces, and the brush the face belongs to is shown above it because the two
// are genuinely different objects and a script addresses the brush.
func editorBrushPropertiesSection() {
	ch, b := brushPanelSubject()
	if ch == nil || b == nil {
		return
	}
	f := faceSelFace(primaryFaceSel())

	imgui.Separator()
	editorFocusOpen(panelFocusBrush, panelFocusFace)
	if !imgui.CollapsingHeaderV("Brush: "+b.ID+"##brushhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusBrush)

	if f != nil {
		editorFaceSelectionNote()
	} else {
		editorBrushSelectionNote()
	}

	editorPropGroupOpen("Brush##brushgrp", func() {
		id := b.ID
		if imgui.InputText("ID##brushid", &id) && id != "" && id != b.ID {
			EditorPushUndo()
			// Level-unique, not chunk-unique (world/identity.go), so a rename
			// that collides with a brush in another chunk has to be caught too.
			ci, bi := activeBrushIndices()
			b.ID = uniqueBrushIDInLevelFor(id, ci, bi)
		}
		if imgui.Checkbox("Solid##brushsolid", &b.Solid) {
			EditorPushUndo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Off makes the whole brush decorative — a body walks\n" +
				"straight through it. For one face only, use the face's\n" +
				"Collision box below.")
		}
		if imgui.Checkbox("Dynamic##brushdyn", &b.Dynamic) {
			EditorPushUndo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A door, a lift, a crusher: a script can write this brush's\n" +
				"position, rotation and velocity, and its collision moves with it.\n\n" +
				"It is also left out of the compiled level's static geometry, so it\n" +
				"clips nothing, nothing clips it, and it cannot be part of the shell\n" +
				"that seals the level.")
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

	brushDetailPanel(ch, b)

	if f == nil {
		// Object mode: there is no face selection to describe. Say where the
		// other half of the panel lives rather than leaving a gap.
		imgui.Separator()
		imgui.Text("Tab to edit this brush — its faces, edges and corners.")
		return
	}

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

		if changed {
			// One reload for the whole block: a face edit is a handful of
			// fields and the panel redraws every frame, so rebuilding per
			// field would re-upload the brush a dozen times a frame while a
			// slider is held.
			_ = b.Load()
			editorChunkGeometryChanged(ch)
		}
	})

	imgui.Separator()
	if editorInDetailMode() {
		imgui.Text("E extrudes, Shift+B smooths, B subdivides, Del removes.")
		return
	}
	imgui.Text("E extrudes, I insets, B subdivides — see the right-click menu.")
}

// brushDetailPanel is the brush's detail-mesh section: whether it has one, the
// way into shaping it, and the two ways back out of one that has gone wrong.
//
// It lives on the BRUSH rather than on a face because a detail mesh belongs to
// the whole solid — it replaces every face at once — and because the question
// it answers is a question about the brush: is what I am looking at the shape
// the player will collide with?
func brushDetailPanel(ch *world.Chunk, b *world.Brush) {
	title := "Detail##brushdetail"
	if b.Detail != nil {
		title = fmt.Sprintf("Detail (%d faces)##brushdetail", len(b.Detail.Faces))
	}
	editorPropGroup(title, 0, func() {
		if b.Detail == nil {
			editorTextMutedWrapped(
				"This brush is drawn as the solid it is. Give it a detail mesh to " +
					"shape its surface — a ledge, a reveal, a panel — with the model " +
					"editor's tools, without changing where the player stops.")
			if imgui.Button("Shape detail...##brushdetailenter") {
				editorEnterDetailModeOnSelection()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Ctrl+Shift+click a brush in the viewport does the same thing.")
			}
			return
		}

		editorTextWrappedColored(imgui.Vec4{X: 0.85, Y: 0.7, Z: 0.35, W: 1},
			"Drawn from its detail mesh. Collision, sealing and the compiled level's "+
				"cells all still use the solid below, which is outlined while you shape it.")

		if editorInDetailMode() {
			if imgui.Button("Leave detail mode##brushdetailleave") {
				editorExitDetailMode()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Tab or Escape does the same thing.")
			}
		} else if imgui.Button("Shape detail...##brushdetailenter2") {
			editorEnterDetailModeOnSelection()
		}

		if imgui.Button("Re-seed from solid##brushdetailreset") {
			// For the case the mode cannot prevent: the hull was reshaped
			// afterwards and the two no longer describe the same wall. Nothing
			// re-seeds automatically, because that would silently discard work
			// every time an author nudged a corner.
			EditorPushUndo()
			b.ResetDetail()
			_ = b.Load()
			editorChunkGeometryChanged(ch)
			clearFaceSelection()
			clearVertexSelection()
			editorSetStatus("Re-seeded %s's detail mesh from its solid", b.ID)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Throw the detail away and copy the solid's faces again.\n" +
				"What to reach for after reshaping the brush underneath it.")
		}

		if imgui.Button("Remove detail##brushdetailclear") {
			EditorPushUndo()
			if editorInDetailMode() {
				// Leave first: the selection about to be thrown away is in the
				// detail mesh's index space, and the mode has nothing to point
				// at once the mesh is gone.
				editorExitDetailMode()
			}
			b.ClearDetail()
			_ = b.Load()
			editorChunkGeometryChanged(ch)
			clearFaceSelection()
			clearVertexSelection()
			editorSetStatus("Removed %s's detail mesh", b.ID)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Back to being drawn as the solid it is.")
		}
	})
}

// brushPanelSubject is the brush the panel is describing: the primary of the
// brush selection in object mode, or the brush the primary selected face
// belongs to in edit mode. Nil when there is no selection of either kind.
func brushPanelSubject() (*world.Chunk, *world.Brush) {
	// Detail mode first: it clears the brush selection on the way in — clicks
	// in there address the detail mesh, not the solid — so the brush being
	// worked on is named by Editor.DetailBrush and by nothing else.
	if ch, b := detailBrush(); b != nil {
		return ch, b
	}
	if s := primaryBrushSel(); brushSelValid(s) {
		return vertexSelBrush(vertexSel{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx})
	}
	ch, b := faceSelBrush(primaryFaceSel())
	if faceSelFace(primaryFaceSel()) == nil {
		return nil, nil
	}
	return ch, b
}

// editorBrushSelectionNote is the object-mode counterpart of
// editorFaceSelectionNote: it says how many brushes a bulk action would reach,
// since the fields below bind to the primary one only.
func editorBrushSelectionNote() {
	if n := len(Editor.SelectedBrushes); n > 1 {
		editorTextMutedWrapped(fmt.Sprintf(
			"%d brushes selected. The gizmo moves, turns and scales all of them; "+
				"the fields below edit the first.", n))
	}
}

// primaryFaceSel is the face the panel edits: the first of the selection, or an
// unresolvable one when nothing is selected.
func primaryFaceSel() faceSel {
	if len(Editor.SelectedFaces) == 0 {
		return faceSel{chunkIdx: -1, brushIdx: -1, faceIdx: -1}
	}
	return Editor.SelectedFaces[0]
}

// uniqueBrushIDInLevelFor returns want, or want with a numeric suffix, such that
// no brush anywhere in the level other than the one at (selChunk, selBrush) is
// using it.
//
// Indices rather than a selection, because both contexts call it: object mode
// has a brush selected and edit mode has one of its faces, and the brush being
// renamed has to be excluded either way or typing its own current name back
// would read as a collision with itself.
//
// Chunk-scoped uniqueness is not enough: a brush is addressed by a bare name in
// scripts, with no chunk qualifier (world/identity.go), so two brushes in
// different chunks sharing an ID would make that name ambiguous.
func uniqueBrushIDInLevelFor(want string, selChunk, selBrush int) string {
	lvl := Editor.level
	if lvl == nil {
		return want
	}
	taken := func(id string) bool {
		for ci := range lvl.Chunks {
			for bi := range lvl.Chunks[ci].Brushes {
				if ci == selChunk && bi == selBrush {
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
