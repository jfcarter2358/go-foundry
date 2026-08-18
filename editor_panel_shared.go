package main

// editor_panel_shared.go — side-panel sections shared by the 3D and 2D editor
// sub-modes.
//
// The 3D panel grew every object-level tool (catalogs, primitive/entity
// properties, chunk management, lights) while the 2D panel only ever grew
// sector tools. Both modes edit the same level, so every section here is drawn
// by Editor3DPanel and Editor2DPanel alike; the few genuinely view-specific
// bits (fly-camera navigation, 2D pan/zoom readout) stay in their own panels.

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// editorEntityCatalogSection draws the entity definition catalog and the
// "place" toggle. The click that actually spawns the entity is handled by the
// active sub-mode (Editor3DHandleClick / Editor2DHandleClick).
func editorEntityCatalogSection() {
	// --- Entity catalog ---
	if imgui.CollapsingHeader("Entity Catalog") {
		for i, def := range Editor.EntityDefs {
			selected := i == Editor.SelectedDefIdx
			if imgui.SelectableV(def.ID, selected, 0, imgui.Vec2{}) {
				Editor.SelectedDefIdx = i
			}
		}
		if imgui.Button("Place Selected") && Editor.SelectedDefIdx >= 0 {
			Editor.PlacementMode = true
		}
		if Editor.PlacementMode {
			imgui.SameLine()
			imgui.Text("Click to place...")
		}
	}
}

// editorPrimitiveCatalogSection draws the primitive type catalog and its place
// toggle.
func editorPrimitiveCatalogSection() {
	// --- Primitive catalog ---
	if imgui.CollapsingHeader("Primitive Catalog") {
		primTypes := []string{"rect_prism", "sphere", "cylinder", "plane", "water"}
		for _, pt := range primTypes {
			selected := pt == Editor.PrimPlacementType
			if imgui.SelectableV(pt, selected, 0, imgui.Vec2{}) {
				Editor.PrimPlacementType = pt
			}
		}
		if imgui.Button("Place Primitive") && Editor.PrimPlacementType != "" {
			Editor.PrimPlacementMode = true
			Editor.PlacementMode = false
		}
		if Editor.PrimPlacementMode {
			imgui.SameLine()
			imgui.Text("Click to place...")
		}
	}
}

// editorSceneryImportSection draws the scenery import entry point.
func editorSceneryImportSection() {
	// --- Scenery import ---
	if imgui.CollapsingHeader("Scenery") {
		if imgui.Button("Import Scenery") {
			Editor.ImportSceneryFiles = nil
			Editor.ImportSceneryIdx = 0
			Editor.ShowImportScenery = true
		}
		importSceneryPopup()
	}
}

// editorPrimitivePropertiesSection draws the property editor for the selected
// primitive, if any.
func editorPrimitivePropertiesSection(lvl *Level) {
	// --- Selected primitive properties ---
	if Editor.ActivePrimitiveIdx >= 0 && Editor.ActiveChunkIdx >= 0 &&
		Editor.ActiveChunkIdx < len(lvl.Chunks) {
		ch := &lvl.Chunks[Editor.ActiveChunkIdx]
		if Editor.ActivePrimitiveIdx < len(ch.Primitives) {
			imgui.Separator()
			editorFocusOpen(panelFocusPrimitive)
			if imgui.CollapsingHeaderV("Primitive Properties", imgui.TreeNodeFlagsDefaultOpen) {
				editorFocusScroll(panelFocusPrimitive)
				p := &ch.Primitives[Editor.ActivePrimitiveIdx]
				imgui.InputText("ID##primid", &p.ID)

				typeNames := []string{"rect_prism", "sphere", "cylinder", "plane", "water"}
				currentType := int(p.Kind)
				if imgui.BeginCombo("Type##primtype", typeNames[currentType]) {
					for i, tn := range typeNames {
						if imgui.SelectableV(tn, i == currentType, 0, imgui.Vec2{}) {
							p.Kind = PrimitiveType(i)
							reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
						}
					}
					imgui.EndCombo()
				}

				pos := [3]float32{p.Position.X(), p.Position.Y(), p.Position.Z()}
				if editorDragFloat3("Position##prim", &pos, 0, 0, "%.3f") {
					p.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
					rebuildPrimitiveIndices(ch)
				}
				rot := [3]float32{
					p.Rotation.X() * 180 / math.Pi,
					p.Rotation.Y() * 180 / math.Pi,
					p.Rotation.Z() * 180 / math.Pi,
				}
				if editorDragFloat3("Rotation (deg)##prim", &rot, -360, 360, "%.1f") {
					p.Rotation = mgl32.Vec3{
						rot[0] * math.Pi / 180,
						rot[1] * math.Pi / 180,
						rot[2] * math.Pi / 180,
					}
				}
				sc := [3]float32{p.Scale.X(), p.Scale.Y(), p.Scale.Z()}
				if editorDragFloat3("Scale##prim", &sc, 0.01, 1000, "%.2f") {
					p.Scale = mgl32.Vec3{sc[0], sc[1], sc[2]}
					reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
				}

				textureCombo("Texture##primtex", &p.Texture, false)

				if p.Kind == PrimitiveWater {
					waterControls("prim", &p.Water, &p.Geometry)
				}

				if p.Kind == PrimitiveCylinder {
					sides := int32(p.Sides)
					if imgui.DragIntV("Sides##primsides", &sides, 1, 3, 64, "%d", 0) {
						p.Sides = int(sides)
						reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
					}
				}

				imgui.Checkbox("Collision##primcol", &p.Collision)
				if editorDragFloat("Alpha##prima", &p.Alpha, 0, 1, "%.2f") {
					p.Geometry.Alpha = p.Alpha
				}
				if editorSmoothPanel("Primitive", &p.Smooth, &p.SmoothAngle) {
					reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
				}

				if imgui.CollapsingHeader("Texture UV##primUVhdr") {
					uvChanged := false
					uvOff := [2]float32{p.UVOffset.X(), p.UVOffset.Y()}
					if editorDragFloat("UV Offset U##primuvou", &uvOff[0], 0, 0, "%.3f") ||
						editorDragFloat("UV Offset V##primuvov", &uvOff[1], 0, 0, "%.3f") {
						p.UVOffset = mgl32.Vec2{uvOff[0], uvOff[1]}
						uvChanged = true
					}
					uvSc := p.UVScale
					if uvSc == (mgl32.Vec2{}) {
						uvSc = mgl32.Vec2{1, 1}
					}
					uvScArr := [2]float32{uvSc.X(), uvSc.Y()}
					if editorDragFloat("UV Scale U##primuvsu", &uvScArr[0], 0.01, 100, "%.3f") ||
						editorDragFloat("UV Scale V##primuvsv", &uvScArr[1], 0.01, 100, "%.3f") {
						p.UVScale = mgl32.Vec2{uvScArr[0], uvScArr[1]}
						uvChanged = true
					}
					uvRotDeg := p.UVRotation * 180 / math.Pi
					if editorDragFloat("UV Rotation (deg)##primuvrot", &uvRotDeg, -360, 360, "%.1f") {
						p.UVRotation = uvRotDeg * math.Pi / 180
						uvChanged = true
					}
					if uvChanged {
						reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
					}
				}

				if imgui.Button("Delete Primitive") {
					ch.Primitives[Editor.ActivePrimitiveIdx].Geometry.Delete()
					ch.Primitives = append(ch.Primitives[:Editor.ActivePrimitiveIdx], ch.Primitives[Editor.ActivePrimitiveIdx+1:]...)
					rebuildPrimitiveIndices(ch)
					Editor.ActivePrimitiveIdx = -1
				}
			}
		}
	}
}

// editorEntityPropertiesSection draws the property editor for the selected
// entity, if any.
func editorEntityPropertiesSection(lvl *Level) {
	// --- Selected entity properties ---
	if Editor.ActiveEntityIdx >= 0 && Editor.ActiveEntityIdx < len(lvl.Entities) {
		imgui.Separator()
		editorFocusOpen(panelFocusEntity)
		if imgui.CollapsingHeaderV("Entity Properties", imgui.TreeNodeFlagsDefaultOpen) {
			editorFocusScroll(panelFocusEntity)
			e := &lvl.Entities[Editor.ActiveEntityIdx]
			imgui.Text(fmt.Sprintf("ID: %s", e.ID))

			pos := [3]float32{e.Position.X(), e.Position.Y(), e.Position.Z()}
			if editorDragFloat3("Position", &pos, 0, 0, "%.3f") {
				e.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
			}
			rot := [3]float32{
				e.Rotation.X() * 180 / math.Pi,
				e.Rotation.Y() * 180 / math.Pi,
				e.Rotation.Z() * 180 / math.Pi,
			}
			if editorDragFloat3("Rotation (deg)", &rot, -360, 360, "%.1f") {
				e.Rotation = mgl32.Vec3{
					rot[0] * math.Pi / 180,
					rot[1] * math.Pi / 180,
					rot[2] * math.Pi / 180,
				}
			}
			sc := [3]float32{e.Scale.X(), e.Scale.Y(), e.Scale.Z()}
			if editorDragFloat3("Scale", &sc, 0.01, 100, "%.2f") {
				e.Scale = mgl32.Vec3{sc[0], sc[1], sc[2]}
			}

			imgui.Text(fmt.Sprintf("Model: %s", filepath.Base(e.ModelPath)))
			imgui.Text(fmt.Sprintf("Texture: %s", e.TexturePath))
			if editorDragFloat("Alpha##enta", &e.Alpha, 0, 1, "%.2f") {
				e.Geometry.Alpha = e.Alpha
			}
		}
	}
}

// editorChunkListSection draws the chunk list plus the new/duplicate/save/
// delete/import controls. The popups it opens are drawn by editorChunkPopups.
func editorChunkListSection(lvl *Level) {
	// --- Chunk list ---
	imgui.Separator()
	if imgui.CollapsingHeader("Chunks") {
		for i := range lvl.Chunks {
			ch := &lvl.Chunks[i]
			selected := i == Editor.ActiveChunkIdx
			if imgui.SelectableV(ch.ID, selected, 0, imgui.Vec2{}) {
				// Selecting a chunk by name is the explicit "I want the whole
				// chunk" action, so it takes the translate gizmo.
				Editor.ActiveChunkIdx = i
				Editor.ActiveEntityIdx = -1
				Editor.ActivePrimitiveIdx = -1
				clearVertexSelection()
				Editor.GizmoKind = gizmoKindChunk
			}
		}
		if imgui.Button("New Chunk") {
			Editor.ShowNewChunk = true
		}
		if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
			imgui.SameLine()
			if imgui.Button("Duplicate Chunk") {
				EditorPushUndo()
				duplicateActiveChunk()
			}
			imgui.SameLine()
			if imgui.Button("Save Chunk") {
				Editor.SaveChunkName = lvl.Chunks[Editor.ActiveChunkIdx].ID
				Editor.ShowSaveChunk = true
			}
			imgui.SameLine()
			if imgui.Button("Delete Chunk") {
				Editor.ShowDeleteChunk = true
			}
		}
		if imgui.Button("Import Chunk") {
			files, _ := filepath.Glob(filepath.Join(executablePath, "chunks", "*.yaml"))
			Editor.ImportChunkFiles = nil
			for _, f := range files {
				Editor.ImportChunkFiles = append(Editor.ImportChunkFiles, filepath.Base(f))
			}
			Editor.ImportChunkIdx = 0
			Editor.ShowImportChunk = true
		}
	}
}

// editorChunkPropertiesSection draws the selected chunk's origin/rotation/scale
// and the jump-to-the-other-sub-mode button.
func editorChunkPropertiesSection(lvl *Level) {
	// --- Selected chunk properties ---
	if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		imgui.Separator()
		if imgui.CollapsingHeader("Chunk Properties") {
			ch := &lvl.Chunks[Editor.ActiveChunkIdx]
			imgui.Text(fmt.Sprintf("ID: %s", ch.ID))
			imgui.Text(fmt.Sprintf("Sectors: %d", len(ch.Sectors)))

			orig := [3]float32{ch.Origin.X(), ch.Origin.Y(), ch.Origin.Z()}
			if editorDragFloat3("Origin", &orig, 0, 0, "%.3f") {
				moveChunk(ch, mgl32.Vec3{orig[0], orig[1], orig[2]})
			}
			editorDragFloat("Rotation##chunkRot", &ch.Rotation, 0, 0, "%.3f")
			sc := ch.Scale
			if sc == 0 {
				sc = 1
			}
			if editorDragFloat("Scale##chunkScale", &sc, 0.01, 100, "%.2f") {
				ch.Scale = sc
			}

			// Jump to whichever sub-mode isn't the current one, centred on
			// this chunk.
			if Editor.Mode == Mode2D {
				if imgui.Button("Switch to 3D Editor") {
					Editor.Mode = Mode3D
				}
			} else if imgui.Button("Switch to Sector Editor") {
				if Editor2DPoppedOut() {
					// The sector editor is a window of its own right now.
					OpenEditor2DWindow()
				} else {
					Editor.Mode = Mode2D
				}
				Editor.Pan2D = mgl32.Vec2{
					(ch.MinBound.X() + ch.MaxBound.X()) / 2,
					(ch.MinBound.Z() + ch.MaxBound.Z()) / 2,
				}
			}
		}
	}
}

// editorChunkPopups draws the modal popups opened from editorChunkListSection.
// Must be called after the owning window's imgui.End().
func editorChunkPopups(lvl *Level) {
	// New chunk popup
	withModalPopup("New Chunk", &Editor.ShowNewChunk, func() {
		imgui.InputText("Chunk ID", &Editor.NewChunkID)
		if imgui.Button("Create") && Editor.NewChunkID != "" {
			EditorPushUndo()
			createChunkAtCamera(Editor.NewChunkID)
			Editor.NewChunkID = ""
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel") {
			imgui.CloseCurrentPopup()
		}
	})

	// Save chunk popup
	withModalPopup("Save Chunk", &Editor.ShowSaveChunk, func() {
		imgui.InputText("Filename", &Editor.SaveChunkName)
		if imgui.Button("Save") && Editor.SaveChunkName != "" {
			ch := &lvl.Chunks[Editor.ActiveChunkIdx]
			path := filepath.Join(executablePath, "chunks", Editor.SaveChunkName+".yaml")
			if err := SaveChunk(ch, path); err != nil {
				Editor.StatusMsg = fmt.Sprintf("Save chunk error: %v", err)
			} else {
				Editor.StatusMsg = fmt.Sprintf("Saved chunk to %s", path)
			}
			Editor.StatusTime = sdl.GetTicks()
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##savechunk") {
			imgui.CloseCurrentPopup()
		}
	})

	// Delete chunk confirmation popup
	withModalPopup("Delete Chunk", &Editor.ShowDeleteChunk, func() {
		chunkName := ""
		if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
			chunkName = lvl.Chunks[Editor.ActiveChunkIdx].ID
		}
		imgui.Text(fmt.Sprintf("Delete chunk %q?", chunkName))
		if imgui.Button("Delete") {
			EditorPushUndo()
			deleteActiveChunk()
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##deletechunk") {
			imgui.CloseCurrentPopup()
		}
	})

	// Import chunk popup
	withModalPopup("Import Chunk", &Editor.ShowImportChunk, func() {
		if len(Editor.ImportChunkFiles) == 0 {
			imgui.Text("No chunk files found in chunks/")
		} else {
			if imgui.BeginComboV("##chunkfile", Editor.ImportChunkFiles[Editor.ImportChunkIdx], 0) {
				for i, name := range Editor.ImportChunkFiles {
					if imgui.SelectableV(name, int32(i) == Editor.ImportChunkIdx, 0, imgui.Vec2{}) {
						Editor.ImportChunkIdx = int32(i)
					}
				}
				imgui.EndCombo()
			}
			if imgui.Button("Import") {
				EditorPushUndo()
				path := filepath.Join(executablePath, "chunks", Editor.ImportChunkFiles[Editor.ImportChunkIdx])
				chunkID, err := ImportChunkIntoLevel(lvl, path)
				if err != nil {
					Editor.StatusMsg = fmt.Sprintf("Import error: %v", err)
				} else {
					Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
					Editor.ActiveSectorIdx = -1
					Editor.ActiveEdgeIdx = -1
					Editor.ActivePrimitiveIdx = -1
					Editor.StatusMsg = fmt.Sprintf("Imported chunk %q", chunkID)
				}
				Editor.StatusTime = sdl.GetTicks()
				imgui.CloseCurrentPopup()
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##importchunk") {
			imgui.CloseCurrentPopup()
		}
	})
}

// placeSelectedEntityAt spawns the catalog-selected entity definition at pos
// and makes it the selection. Placement mode is cleared either way, so a
// failed load doesn't leave the editor armed.
func placeSelectedEntityAt(pos mgl32.Vec3) {
	Editor.PlacementMode = false
	lvl := Editor.level
	if lvl == nil || len(Editor.EntityDefs) == 0 || Editor.SelectedDefIdx < 0 ||
		Editor.SelectedDefIdx >= len(Editor.EntityDefs) {
		return
	}
	def := Editor.EntityDefs[Editor.SelectedDefIdx]

	entity := Entity{
		ID:          fmt.Sprintf("%s_%d", def.ID, len(lvl.Entities)),
		Position:    pos,
		Scale:       mgl32.Vec3{1, 1, 1},
		ModelPath:   executablePath + "/" + def.Model,
		TexturePath: def.Texture,
		BoundHalfX:  def.BoundHalfX,
		BoundHalfY:  def.BoundHalfY,
		BoundHalfZ:  def.BoundHalfZ,
		Visible:     def.Model != "",
	}
	switch def.Bounding {
	case "sphere":
		entity.BoundingKind = BoundingSphere
	case "cylinder":
		entity.BoundingKind = BoundingCylinder
	default:
		entity.BoundingKind = BoundingAABB
	}
	if err := entity.Load(); err == nil {
		EditorPushUndo()
		lvl.Entities = append(lvl.Entities, entity)
		Editor.ActiveEntityIdx = len(lvl.Entities) - 1
		Editor.ActivePrimitiveIdx = -1
	}
}

// placeSelectedPrimitiveAt spawns the catalog-selected primitive type at pos in
// the active chunk and makes it the selection.
func placeSelectedPrimitiveAt(pos mgl32.Vec3) {
	Editor.PrimPlacementMode = false
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]

	var kind PrimitiveType
	switch Editor.PrimPlacementType {
	case "sphere":
		kind = PrimitiveSphere
	case "cylinder":
		kind = PrimitiveCylinder
	case "plane":
		kind = PrimitivePlane
	case "water":
		kind = PrimitiveWater
	default:
		kind = PrimitiveRectPrism
	}

	prim := Primitive{
		ID:       fmt.Sprintf("prim_%d", len(ch.Primitives)),
		Kind:     kind,
		Position: pos,
		Scale:    mgl32.Vec3{1, 1, 1},
		Texture:  "concrete",
		Sides:    16,
		ChunkID:  ch.ID,
		Alpha:    1.0,
	}
	if kind == PrimitiveWater {
		prim.Texture = "water"
		prim.Scale = mgl32.Vec3{8, 1, 8}
		prim.Alpha = 0.75
		prim.UVScale = mgl32.Vec2{4, 4}
		prim.Water = DefaultWaterParams()
	}
	_ = ensureTextureLoaded(prim.Texture)

	// Compute bounding volumes
	switch kind {
	case PrimitiveRectPrism:
		prim.BoundingKind = BoundingAABB
		prim.BoundHalfX = 0.5
		prim.BoundHalfY = 0.5
		prim.BoundHalfZ = 0.5
	case PrimitiveSphere:
		prim.BoundingKind = BoundingSphere
		prim.BoundHalfX = 0.5
		prim.BoundHalfY = 0.5
		prim.BoundHalfZ = 0.5
	case PrimitiveCylinder:
		prim.BoundingKind = BoundingCylinder
		prim.BoundHalfX = 0.5
		prim.BoundHalfY = 0.5
		prim.BoundHalfZ = 0.5
	case PrimitivePlane, PrimitiveWater:
		prim.BoundingKind = BoundingAABB
		prim.BoundHalfX = prim.Scale.X() / 2
		prim.BoundHalfY = 0.01
		prim.BoundHalfZ = prim.Scale.Z() / 2
	}

	if err := prim.Load(); err == nil {
		EditorPushUndo()
		ch.Primitives = append(ch.Primitives, prim)
		Editor.ActivePrimitiveIdx = len(ch.Primitives) - 1
		Editor.ActiveEntityIdx = -1
		rebuildPrimitiveIndices(ch)
	}
}

// editorEdgeKindCombo draws the wall↔portal converter for the selected edge.
// idSuffix keeps the imgui IDs distinct between the 2D and 3D panels.
// convertEdge rewrites the sector's edge arrays, so the change is snapshotted
// for undo like every other structural edit.
func editorEdgeKindCombo(ch *Chunk, s *Sector, idSuffix string) {
	kind := Editor.ActiveEdgeKind
	idx := Editor.ActiveEdgeIdx
	if idx < 0 || (kind != "wall" && kind != "portal") {
		return
	}
	if imgui.BeginCombo("Kind##ek"+idSuffix, kind) {
		for _, target := range []string{"wall", "portal"} {
			if imgui.SelectableV(target, kind == target, 0, imgui.Vec2{}) && target != kind {
				EditorPushUndo()
				convertEdge(ch, s, kind, idx, target)
			}
		}
		imgui.EndCombo()
	}
}
