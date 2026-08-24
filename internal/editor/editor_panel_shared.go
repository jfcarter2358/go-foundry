package editor

// editor_panel_shared.go — side-panel sections shared by the 3D and 2D editor
// sub-modes.
//
// The 3D panel grew every object-level tool (the spawn point, chunk management,
// lights) while the 2D panel only ever grew sector tools. Both modes edit the same level, so every section here is drawn
// by Editor3DPanel and Editor2DPanel alike; the few genuinely view-specific
// bits (fly-camera navigation, 2D pan/zoom readout) stay in their own panels.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/world"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// editorSpawnSection draws the level's player spawn point: the place/move
// toggle when there is none, and its properties once there is.
//
// One section rather than a catalog and a properties block, because a level has
// exactly one spawn point — there is nothing to pick from, and "place" and
// "move" are the same operation.
func editorSpawnSection(lvl *world.Level) {
	editorFocusOpen(panelFocusSpawn)
	if !imgui.CollapsingHeaderV("Player Spawn", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusSpawn)

	label := "Place Spawn Point"
	if lvl.Spawn != nil {
		label = "Move Spawn Point"
	}
	if imgui.Button(label + "##spawnplace") {
		Editor.SpawnPlacementMode = true
	}
	if Editor.SpawnPlacementMode {
		imgui.SameLine()
		imgui.Text("Click to place...")
	}

	if lvl.Spawn == nil {
		imgui.Text("This level has no spawn point.")
		return
	}
	imgui.Separator()
	editorSpawnPointSection(lvl)
}

// editorChunkListSection draws the chunk list plus the new/duplicate/save/
// delete/import controls. The popups it opens are drawn by editorChunkPopups.
func editorChunkListSection(lvl *world.Level) {
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
				editorClearSpawnSelection()
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
			files, _ := filepath.Glob(filepath.Join(fileutil.ExecutablePath, "chunks", "*.yaml"))
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
func editorChunkPropertiesSection(lvl *world.Level) {
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

			// Jump to the other editor, centred on this chunk. From the
			// sector map that means handing focus back to the main window's
			// 3D view (withEditor2DMode notices the mode change and raises
			// it); from the 3D view it means opening the map's window.
			if Editor.Mode == Mode2D {
				if imgui.Button("Switch to 3D Editor") {
					Editor.Mode = Mode3D
				}
			} else if imgui.Button("Switch to Sector Editor") {
				OpenEditor2DWindow()
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
func editorChunkPopups(lvl *world.Level) {
	// New chunk popup
	imguibackend.WithModalPopup("New Chunk", &Editor.ShowNewChunk, func() {
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
	imguibackend.WithModalPopup("Save Chunk", &Editor.ShowSaveChunk, func() {
		imgui.InputText("Filename", &Editor.SaveChunkName)
		if imgui.Button("Save") && Editor.SaveChunkName != "" {
			ch := &lvl.Chunks[Editor.ActiveChunkIdx]
			path := filepath.Join(fileutil.ExecutablePath, "chunks", Editor.SaveChunkName+".yaml")
			if err := levelio.SaveChunk(ch, path); err != nil {
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
	imguibackend.WithModalPopup("Delete Chunk", &Editor.ShowDeleteChunk, func() {
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
	imguibackend.WithModalPopup("Import Chunk", &Editor.ShowImportChunk, func() {
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
				path := filepath.Join(fileutil.ExecutablePath, "chunks", Editor.ImportChunkFiles[Editor.ImportChunkIdx])
				chunkID, err := levelio.ImportChunkIntoLevel(lvl, path)
				if err != nil {
					Editor.StatusMsg = fmt.Sprintf("Import error: %v", err)
				} else {
					Editor.ActiveChunkIdx = len(lvl.Chunks) - 1
					Editor.ActiveSectorIdx = -1
					Editor.ActiveEdgeIdx = -1
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

// editorEdgeKindCombo draws the wall↔portal converter for the selected edge.
// idSuffix keeps the imgui IDs distinct between the 2D and 3D panels.
// convertEdge rewrites the sector's edge arrays, so the change is snapshotted
// for undo like every other structural edit.
func editorEdgeKindCombo(ch *world.Chunk, s *world.Sector, idSuffix string) {
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

// editorSetStatus sets the editor's transient status line, the one-line
// feedback an action leaves behind when it has nothing else to show for
// itself. Formatted rather than plain so callers can name what they acted on
// without building the string first.
func editorSetStatus(format string, args ...any) {
	Editor.StatusMsg = fmt.Sprintf(format, args...)
	Editor.StatusTime = sdl.GetTicks()
}

// editorTextColored draws text in a colour. This imgui binding has no
// TextColored of its own, so it is the style-stack version — pushed and popped
// around one call so the stack cannot be left unbalanced.
func editorTextColored(col imgui.Vec4, s string) {
	imgui.PushStyleColor(imgui.StyleColorText, col)
	imgui.Text(s)
	imgui.PopStyleColor()
}

// editorTextMuted draws de-emphasised text, for a status line or an aside.
func editorTextMuted(s string) {
	editorTextColored(imgui.Vec4{X: 0.6, Y: 0.6, Z: 0.6, W: 1}, s)
}

// editorTextMutedWrapped is editorTextMuted for an aside long enough to need
// wrapping — a file path, or a list of names.
func editorTextMutedWrapped(s string) {
	editorTextWrappedColored(imgui.Vec4{X: 0.6, Y: 0.6, Z: 0.6, W: 1}, s)
}

// editorTextWrappedColored is the two together, for a diagnostic long enough to
// need wrapping and important enough to need a colour.
func editorTextWrappedColored(col imgui.Vec4, s string) {
	imgui.PushStyleColor(imgui.StyleColorText, col)
	editorTextWrapped(s)
	imgui.PopStyleColor()
}
