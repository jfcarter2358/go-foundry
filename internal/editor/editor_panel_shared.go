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
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/world"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// editorOpenImportChunk scans chunks/ and raises the import dialog. The Chunks
// list's ↑ button calls it; the dialog itself is drawn by editorChunkPopups.
func editorOpenImportChunk() {
	files, _ := filepath.Glob(filepath.Join(fileutil.ExecutablePath, "chunks", "*.yaml"))
	Editor.ImportChunkFiles = nil
	for _, f := range files {
		Editor.ImportChunkFiles = append(Editor.ImportChunkFiles, filepath.Base(f))
	}
	Editor.ImportChunkIdx = 0
	Editor.ShowImportChunk = true
}

// editorChunkPropertiesSection draws the selected chunk's identity, its live
// placement, what it is carried by, its scripting block and the jump to the
// other editor.
//
// Shown only while the chunk is the selection — being the *active* chunk is not
// the same thing, and a properties block that was always up was the panel's
// loudest way of saying nothing.
func editorChunkPropertiesSection(lvl *world.Level) {
	ch := selectedChunk()
	if ch == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusChunk)
	if !imgui.CollapsingHeaderV("Chunk: "+ch.ID+"##chunkhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusChunk)

	editorPropGroupOpen("Identity##chunkidgrp", func() { editorChunkIdentity(lvl, ch) })
	editorPropGroupOpen("Transform##chunktfgrp", func() { editorChunkTransform(ch) })
	editorPropGroup("Carried By##chunkframegrp", 0, func() { editorChunkFrameOwnerRow(lvl, ch) })
	editorScriptDataUI(&ch.ScriptData, "chunk", "This chunk")
	editorPropGroup("Actions##chunkactions", 0, func() { editorChunkActions(ch) })
}

// editorChunkIdentity draws the chunk's ID and what it holds.
func editorChunkIdentity(lvl *world.Level, ch *world.Chunk) {
	id := ch.ID
	if imgui.InputTextV("ID##chunkid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != ch.ID {
		switch {
		case id == "":
			editorSetStatus("A chunk needs an ID")
		case world.LevelIDTaken(lvl, id):
			editorSetStatus("%q is already used by something else in this level", id)
		default:
			EditorPushUndo()
			ch.ID = id
		}
	}
	enabled := ch.IsEnabled()
	if imgui.Checkbox("Enabled##chunkenabled", &enabled) {
		EditorPushUndo()
		// Straight through SetChunkEnabled rather than by writing the flag: the
		// panel runs on the main thread with the GL context current, so the
		// buffers can be released or rebuilt now and the author sees the chunk
		// vanish or come back on this frame rather than the next. The spatial
		// hash is rebuilt because disabled chunks are left out of it.
		if world.SetChunkEnabled(ch, enabled) {
			editorChunkTransformChanged()
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A disabled chunk is not drawn, not solid, and its geometry\n" +
			"is unloaded. It stays on the sector map, and a script switches\n" +
			"it back on with `@" + ch.ID + ".prop.enabled = true`.")
	}

	imgui.Text(fmt.Sprintf("Sectors: %d", len(ch.Sectors)))
	imgui.Text(fmt.Sprintf("Points: %d", len(ch.Points)))
	imgui.Text(fmt.Sprintf("Biome sectors: %d", len(ch.BiomeSectors)))
}

// editorChunkActions draws the jump to the other editor, centred on this chunk.
//
// From the sector map that means handing focus back to the main window's 3D
// view (withEditor2DMode notices the mode change and raises it); from the 3D
// view it means opening the map's window.
func editorChunkActions(ch *world.Chunk) {
	if Editor.Mode == Mode2D {
		if imgui.Button("Switch to 3D Editor##chunkto3d") {
			Editor.Mode = Mode3D
		}
		return
	}
	if imgui.Button("Switch to Sector Editor##chunkto2d") {
		OpenEditor2DWindow()
		// Pan2D is a point on the map's plane, which IS this chunk's own local
		// space (see editor2DAnchor) — so the chunk-local centre goes in as-is.
		// Carrying it out to world first would pan the view off the chunk by
		// exactly its Origin.
		center := ch.MinBound.Add(ch.MaxBound).Mul(0.5)
		Editor.Pan2D = mgl32.Vec2{center.X(), center.Z()}
	}
}

// editorChunkTransform draws the chunk's live Origin/Rotation/Scale — the
// same fields the translate/rotate/scale gizmos drag (editor_translate_gizmo.go
// and friends), for typing an exact value instead of eyeballing a drag.
// Rotation is a single Y-axis angle and Scale a single uniform factor (see
// Chunk.ModelMatrix), not the three-component fields an entity gets.
func editorChunkTransform(ch *world.Chunk) {
	// Each write re-buckets the spatial hash, the same as the gizmos do — see
	// editorChunkTransformChanged.
	pos := [3]float32{ch.Origin.X(), ch.Origin.Y(), ch.Origin.Z()}
	if editorDragFloat3("Origin##chunkorigin", &pos, 0, 0, "%.3f") {
		ch.Origin = mgl32.Vec3{pos[0], pos[1], pos[2]}
		editorChunkTransformChanged()
	}

	rotDeg := mgl32.RadToDeg(ch.Rotation)
	if editorDragFloat("Rotation (deg)##chunkrot", &rotDeg, -360, 360, "%.1f") {
		ch.Rotation = mgl32.DegToRad(rotDeg)
		editorChunkTransformChanged()
	}

	sc := ch.Scale
	if sc == 0 {
		sc = 1
	}
	if editorDragFloat("Scale##chunkscale", &sc, 0, 100, "%.3f") {
		ch.Scale = world.EntityScaleClamp(sc)
		editorChunkTransformChanged()
	}
}

// editorChunkFrameOwnerRow draws which vehicle carries this chunk as its
// interior, and keeps both halves of that binding in step.
//
// A chunk names its frame owner and a vehicle names its interior; either one
// alone is a level that loads wrong, so setting it from this side writes the
// vehicle's half too — the same contract editorVehicleInteriorPicker keeps from
// the other end.
func editorChunkFrameOwnerRow(lvl *world.Level, ch *world.Chunk) {
	label := ch.FrameOwner
	if label == "" {
		label = "(nothing — world chunk)"
	}
	if imgui.BeginCombo("Carried by##chunkframe", label) {
		if imgui.SelectableV("(nothing — world chunk)", ch.FrameOwner == "", 0, imgui.Vec2{}) &&
			ch.FrameOwner != "" {
			EditorPushUndo()
			editorSetChunkCarrier(lvl, ch, "")
		}
		for i := range lvl.Entities {
			e := &lvl.Entities[i]
			if e.Vehicle == nil {
				continue
			}
			if imgui.SelectableV(e.ID, ch.FrameOwner == e.ID, 0, imgui.Vec2{}) && ch.FrameOwner != e.ID {
				EditorPushUndo()
				editorSetChunkCarrier(lvl, ch, e.ID)
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The vehicle whose deck this chunk's geometry is, authored\n" +
			"around the origin and carried around the world by it.\n" +
			"Biome paint and procedural detail do not work on one.")
	}
	if ch.FrameOwner != "" {
		editorTextMutedWrapped("Authored around the origin in the 2D map, not where the vehicle sits. " +
			"Bind a portal to `world` to walk in and out of it.")
	}
}

// editorSetChunkCarrier binds ch to the vehicle owner (empty to unbind), taking
// the binding off whatever held it and writing the vehicle's own Interiors
// list. A vehicle may carry more than one chunk this way — a floor and a roof
// over the same footprint, say — so this only ever touches ch's own binding,
// never another chunk's.
func editorSetChunkCarrier(lvl *world.Level, ch *world.Chunk, owner string) {
	// Whoever used to carry this chunk stops naming it.
	for i := range lvl.Entities {
		if v := lvl.Entities[i].Vehicle; v != nil {
			v.RemoveInterior(ch.ID)
		}
	}
	// This chunk stops being carried, whatever it was carried by.
	if ch.FrameOwner != "" {
		ch.Xform = mgl32.Mat4{}
	}
	ch.FrameOwner = owner
	if owner != "" {
		if e := world.FindEntity(lvl, owner); e != nil && e.Vehicle != nil {
			e.Vehicle.AddInterior(ch.ID)
		}
	}
	_ = world.RebuildIndexes(lvl)
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
				// Land the import wherever the active sub-mode is looking at —
				// the same "here" createChunkAtCamera uses — rather than at
				// whatever Origin the source file happened to carry, which was
				// authored relative to a different level entirely.
				target := mgl32.Vec3{gfx.Cam.Position.X(), 0, gfx.Cam.Position.Z()}
				if Editor.Mode == Mode2D {
					target = mgl32.Vec3{Editor.Pan2D.X(), 0, Editor.Pan2D.Y()}
				}
				chunkID, err := levelio.ImportChunkIntoLevel(lvl, path, target)
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
