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
// The fields bind to the *primary* selected face, and whatever an edit changes
// on it is then written to every other selected face too — only the fields that
// changed, so the others keep everything the author did not touch.

import (
	"fmt"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
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
			ws().pushUndo()
			// Level-unique, not chunk-unique (world/identity.go), so a rename
			// that collides with a brush in another chunk has to be caught too.
			ci, bi := activeBrushIndices()
			b.ID = uniqueBrushIDInLevelFor(id, ci, bi)
		}
		if imgui.Checkbox("Solid##brushsolid", &b.Solid) {
			ws().pushUndo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Off makes the whole brush decorative — a body walks\n" +
				"straight through it. For one face only, use the face's\n" +
				"Collision box below.")
		}
		if imgui.Checkbox("Dynamic##brushdyn", &b.Dynamic) {
			ws().pushUndo()
			// A dynamic brush may move, so the raycast keeps it out of the
			// trees it culls with; see world/brush_generation.go.
			world.MarkBrushMeshesChanged()
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
	editorObjectWaypointsUI(world.BrushWaypointFollower(ch, b))
	brushPaintPanel()

	if f == nil {
		// Object mode: there is no face selection to describe. Say where the
		// other half of the panel lives rather than leaving a gap.
		imgui.Separator()
		imgui.Text("Tab to edit this brush — its faces, edges and corners.")
		return
	}

	editorPropGroupOpen(fmt.Sprintf("Face %d##brushfacegrp", ws().ActiveFaceIdx), func() {
		changed := false
		// The widgets bind to the primary face; what they changed on it is
		// carried to the rest of the selection afterwards. See
		// applyFaceEditToSelection.
		before := *f
		// "(none)" is a real choice here, not an absent one: a face with no
		// texture is drawn from its own normal, which is what a brush looks
		// like until someone paints it.
		changed = textureCombo("Texture##bftex", &f.Texture, true) || changed
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

		editorPropGroup("Texture Override##bfover", 0, func() {
			if imgui.Checkbox("Override##bfoveron", &f.TextureOverride) {
				ws().pushUndo()
				changed = true
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Draw another texture on this face, projected onto its own\n" +
					"plane, instead of its unwrapped or painted one. The unwrap is\n" +
					"kept, so switching this off brings the painting back.")
			}
			if !f.TextureOverride {
				return
			}
			changed = textureCombo("Override Texture##bfovtex", &f.OverrideTexture, true) || changed
			changed = editorDragFloat("Offset U##bfovuo", &f.OverrideUVOffset[0], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Offset V##bfovvo", &f.OverrideUVOffset[1], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Scale U##bfovus", &f.OverrideUVScale[0], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Scale V##bfovvs", &f.OverrideUVScale[1], -100, 100, "%.3f") || changed
			changed = editorDragFloat("Rotation##bfovur", &f.OverrideUVRotation, -6.2832, 6.2832, "%.3f") || changed
			editorTextMutedWrapped("UV mode [U] points G, R and C at this texture in the viewport.")
		})

		editorPropGroup("UV##bfuv", 0, func() {
			if f.TextureOverride {
				editorTextMutedWrapped("Not drawn while the texture override is on.")
			}
			if f.HasUVs() {
				editorTextMutedWrapped("Laid out by an unwrap: each corner has its own place on " +
					"the texture, and these move the whole face from there.")
				if imgui.Button("Reset to projected##bfuvreset") {
					ws().pushUndo()
					f.UVs = nil
					changed = true
				}
				if imgui.IsItemHovered() {
					imgui.SetTooltip("Forget the unwrap and project this face onto its own\n" +
						"plane again — what a face wearing a tiling material wants.")
				}
			}
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
			applyFaceEditToSelection(&before, f)
		}
	})

	imgui.Separator()
	if editorInDetailMode() {
		imgui.Text("E extrudes, Shift+B smooths, B subdivides, Del removes.")
		return
	}
	imgui.Text("E extrudes, I insets, B subdivides — see the right-click menu.")
}

// applyFaceEditToSelection carries an edit made through the panel on the
// primary face to every other selected face.
//
// Only the fields that actually changed travel, and a vector travels one
// component at a time: dragging Offset U on the primary must not flatten the
// Offset V each of the others had. A per-corner unwrap does not travel at all —
// its corners are the primary's — except for being thrown away, which is what
// "Reset to projected" does.
func applyFaceEditToSelection(before, after *world.BrushFace) {
	sels := ws().SelectedFaces
	if len(sels) < 2 {
		return
	}
	type brushKey struct{ chunkIdx, brushIdx int }
	touched := map[brushKey]bool{}
	for _, s := range sels[1:] {
		f := faceSelFace(s)
		if f == nil || f == after {
			continue
		}
		copyChangedFaceFields(before, after, f)
		touched[brushKey{s.chunkIdx, s.brushIdx}] = true
	}
	for k := range touched {
		ch, b := faceSelBrush(faceSel{chunkIdx: k.chunkIdx, brushIdx: k.brushIdx})
		if b == nil {
			continue
		}
		_ = b.Load()
		editorChunkGeometryChanged(ch)
	}
}

// copyChangedFaceFields writes into dst every panel-editable field that differs
// between before and after.
func copyChangedFaceFields(before, after, dst *world.BrushFace) {
	str := func(b, a string, d *string) {
		if b != a {
			*d = a
		}
	}
	f32 := func(b, a float32, d *float32) {
		if b != a {
			*d = a
		}
	}
	flag := func(b, a bool, d *bool) {
		if b != a {
			*d = a
		}
	}
	vec2 := func(b, a mgl32.Vec2, d *mgl32.Vec2) {
		for i := range a {
			if b[i] != a[i] {
				d[i] = a[i]
			}
		}
	}
	str(before.Texture, after.Texture, &dst.Texture)
	str(before.BlendTexture, after.BlendTexture, &dst.BlendTexture)
	str(before.BlendMask, after.BlendMask, &dst.BlendMask)
	f32(before.Alpha, after.Alpha, &dst.Alpha)
	flag(before.DoubleSided, after.DoubleSided, &dst.DoubleSided)
	flag(before.NoDraw, after.NoDraw, &dst.NoDraw)
	flag(before.NoCollide, after.NoCollide, &dst.NoCollide)

	flag(before.TextureOverride, after.TextureOverride, &dst.TextureOverride)
	str(before.OverrideTexture, after.OverrideTexture, &dst.OverrideTexture)
	vec2(before.OverrideUVOffset, after.OverrideUVOffset, &dst.OverrideUVOffset)
	vec2(before.OverrideUVScale, after.OverrideUVScale, &dst.OverrideUVScale)
	f32(before.OverrideUVRotation, after.OverrideUVRotation, &dst.OverrideUVRotation)

	if len(before.UVs) > 0 && after.UVs == nil {
		dst.UVs = nil
	}
	vec2(before.UVOffset, after.UVOffset, &dst.UVOffset)
	vec2(before.UVScale, after.UVScale, &dst.UVScale)
	f32(before.UVRotation, after.UVRotation, &dst.UVRotation)

	flag(before.Smooth, after.Smooth, &dst.Smooth)
	f32(before.SmoothAngle, after.SmoothAngle, &dst.SmoothAngle)
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
			ws().pushUndo()
			b.ResetDetail()
			_ = b.Load()
			editorChunkGeometryChanged(ch)
			clearFaceSelection()
			clearVertexSelection()
			ws().setStatus("Re-seeded %s's detail mesh from its solid", b.ID)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Throw the detail away and copy the solid's faces again.\n" +
				"What to reach for after reshaping the brush underneath it.")
		}

		if imgui.Button("Remove detail##brushdetailclear") {
			ws().pushUndo()
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
			ws().setStatus("Removed %s's detail mesh", b.ID)
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
	// worked on is named by ws().DetailBrush and by nothing else.
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
	if n := len(ws().SelectedBrushes); n > 1 {
		editorTextMutedWrapped(fmt.Sprintf(
			"%d brushes selected. The gizmo moves, turns and scales all of them; "+
				"the fields below edit the first.", n))
	}
}

// primaryFaceSel is the face the panel edits: the first of the selection, or an
// unresolvable one when nothing is selected.
func primaryFaceSel() faceSel {
	if len(ws().SelectedFaces) == 0 {
		return faceSel{chunkIdx: -1, brushIdx: -1, faceIdx: -1}
	}
	return ws().SelectedFaces[0]
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
	lvl := ws().level
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
