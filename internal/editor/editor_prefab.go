package editor

// editor_prefab.go — placing, selecting and re-stamping prefab instances.
//
// A prefab is a piece of a level someone built once and wants twenty of, linked
// by file rather than copied (world/prefab.go). The editor's half is three
// things: place an instance, move it, and — the part that makes it a prefab at
// all — save a selection *as* one and have every existing placement update.
//
// The instance is the selection, not what it made. Clicking a materialised
// brush selects that brush, and a brush belonging to a prefab is not editable
// in place: the edit belongs in the file, or it is not a prefab edit. The
// Explorer's row and the gizmo both address the instance.
//
// Moving an instance RE-STAMPS it. Its brushes ride a chunk whose Origin is the
// instance's position, so those would follow a transform on their own; its
// entities, volumes and lights are level-scoped and world-positioned, with no
// parent to inherit from, so they would not. Re-stamping the lot is one rule
// rather than two, and a prefab file is small enough that re-reading it per
// drag-release is not worth avoiding.

import (
	"strconv"

	"foundry/internal/gizmo"
	"foundry/internal/levelio"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// ---------- selection ----------

// activePrefab is the selected prefab instance, or nil.
func activePrefab() *world.PrefabInstance {
	lvl := Editor.level
	if lvl == nil || Editor.ActivePrefabIdx < 0 || Editor.ActivePrefabIdx >= len(lvl.Prefabs) {
		return nil
	}
	return &lvl.Prefabs[Editor.ActivePrefabIdx]
}

// editorSelectPrefab makes one instance the selection.
func editorSelectPrefab(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Prefabs) {
		return
	}
	editorClearObjectSelections()
	Editor.ActivePrefabIdx = idx
	Editor.GizmoKind = gizmoKindPrefab
	Editor.PanelFocus = panelFocusPrefab
}

// editorClearPrefabSelection drops the instance from the selection, taking the
// gizmo with it so it cannot keep dragging something no longer selected.
func editorClearPrefabSelection() {
	Editor.ActivePrefabIdx = -1
	if Editor.GizmoKind == gizmoKindPrefab {
		Editor.GizmoKind = gizmoKindNone
	}
}

// ---------- placing ----------

// editorArmPrefabPlacement makes the next left-click drop an instance.
func editorArmPrefabPlacement() {
	Editor.PrefabPlacementMode = true
	Editor.VolumePlacementMode = false
	Editor.EntityPlacementMode = false
	Editor.SpawnPlacementMode = false
}

// placePrefabAt creates an instance of the armed file at pos, stamps it, and
// selects it. Reports whether one was placed.
func placePrefabAt(pos mgl32.Vec3) bool {
	Editor.PrefabPlacementMode = false
	lvl := Editor.level
	if lvl == nil || Editor.PrefabPlaceFile == "" {
		return false
	}
	if Editor.SnapToGrid && Editor.StepSize > 0 {
		pos = snapVec3(pos, Editor.StepSize)
	}

	EditorPushUndo()
	base := Editor.NewPrefabID
	if base == "" {
		base = Editor.PrefabPlaceFile
	}
	inst := world.PrefabInstance{
		ID:       world.UniquePrefabInstanceID(lvl, base),
		File:     Editor.PrefabPlaceFile,
		Position: pos,
		Scale:    1,
	}
	lvl.Prefabs = append(lvl.Prefabs, inst)
	if err := levelio.RestampPrefab(lvl, inst.ID); err != nil {
		editorSetStatus("Prefab %s: %v", inst.ID, err)
	} else {
		editorSetStatus("Placed %s as %q", inst.File, inst.ID)
	}
	editorSelectPrefab(len(lvl.Prefabs) - 1)
	return true
}

// deleteActivePrefab removes the selected instance and everything it made.
func deleteActivePrefab() {
	lvl := Editor.level
	inst := activePrefab()
	if lvl == nil || inst == nil {
		return
	}
	id := inst.ID
	idx := Editor.ActivePrefabIdx
	EditorPushUndo()
	levelio.DropPrefabContent(lvl, id)
	lvl.Prefabs = append(lvl.Prefabs[:idx], lvl.Prefabs[idx+1:]...)
	// The only DropPrefabContent that is not followed by a re-stamp, so it is
	// the only one that has to re-index by hand: dropping the instance's
	// chunks shifts every index past them. See RestampPrefab.
	if err := world.RebuildIndexes(lvl); err != nil {
		editorSetStatus("Deleted %q, but frames are unhappy: %v", id, err)
	} else {
		editorSetStatus("Deleted prefab instance %q", id)
	}
}

// duplicateActivePrefab copies the selected instance beside itself and selects
// the copy. Offset rather than coincident, for the reason a duplicated volume
// is: two placements in exactly the same place cannot be told apart or clicked
// apart.
func duplicateActivePrefab() {
	lvl := Editor.level
	inst := activePrefab()
	if lvl == nil || inst == nil {
		return
	}
	EditorPushUndo()
	clone := *inst
	clone.ID = world.UniquePrefabInstanceID(lvl, inst.ID+"_copy")
	clone.Position = clone.Position.Add(mgl32.Vec3{2, 0, 0})
	lvl.Prefabs = append(lvl.Prefabs, clone)
	if err := levelio.RestampPrefab(lvl, clone.ID); err != nil {
		editorSetStatus("Prefab %s: %v", clone.ID, err)
	}
	editorSelectPrefab(len(lvl.Prefabs) - 1)
}

// restampActivePrefab re-materialises the selected instance — what a change to
// its transform, its file or its frame has to do.
func restampActivePrefab() {
	lvl := Editor.level
	inst := activePrefab()
	if lvl == nil || inst == nil {
		return
	}
	if err := levelio.RestampPrefab(lvl, inst.ID); err != nil {
		editorSetStatus("Prefab %s: %v", inst.ID, err)
	}
}

// ---------- saving a selection as a prefab ----------

// EditorSaveSelectionAsPrefab writes the selected brushes' chunk-local geometry
// to a prefab file, and re-stamps every instance already placed from it.
//
// The brushes are taken from the *face* selection, because that is the only
// geometry selection there is: a brush is selected by one of its faces
// (editor_face_sel.go). Their verts are carried out through their chunk's own
// placement and then back to the selection's own centre, so a prefab is
// authored around its origin whatever corner of the level it was built in — a
// street lamp saved from world (400, 0, -90) places at the origin, not four
// hundred metres away.
func EditorSaveSelectionAsPrefab(name string) error {
	lvl := Editor.level
	if lvl == nil {
		return nil
	}
	// Resolved once, chunk and all: the two halves below have to walk the same
	// set, or a selection made by vertex rather than by face would measure an
	// origin from brushes it then did not save.
	type owned struct {
		ch *world.Chunk
		b  *world.Brush
	}
	var picked []owned
	seen := map[*world.Brush]bool{}
	add := func(ch *world.Chunk, b *world.Brush) {
		if ch == nil || b == nil || seen[b] {
			return
		}
		seen[b] = true
		picked = append(picked, owned{ch, b})
	}
	// Every unit that names a brush, so the same command works from either
	// context: a whole brush in object mode, one of its faces or corners in
	// edit mode. Saving a group of brushes as a prefab is an object-mode act
	// above all, and that selection is the one that says so directly.
	for _, sel := range Editor.SelectedBrushes {
		add(vertexSelBrush(vertexSel{chunkIdx: sel.chunkIdx, brushIdx: sel.brushIdx}))
	}
	for _, sel := range Editor.SelectedFaces {
		add(faceSelBrush(sel))
	}
	for _, sel := range Editor.SelectedVertices {
		add(vertexSelBrush(sel))
	}
	if len(picked) == 0 {
		editorSetStatus("Select the brushes to save first")
		return nil
	}

	// The extent of everything being saved, in world space.
	var lo, hi mgl32.Vec3
	for i, o := range picked {
		bmin, bmax := o.b.WorldAABB(o.ch)
		if i == 0 {
			lo, hi = bmin, bmax
			continue
		}
		lo = mgl32.Vec3{min32(lo.X(), bmin.X()), min32(lo.Y(), bmin.Y()), min32(lo.Z(), bmin.Z())}
		hi = mgl32.Vec3{max32(hi.X(), bmax.X()), max32(hi.Y(), bmax.Y()), max32(hi.Z(), bmax.Z())}
	}
	// The horizontal centre but the LOWEST point vertically: a prefab is
	// something placed on the ground, and an origin in the middle of a lamp
	// post would have every placement half-buried.
	origin := mgl32.Vec3{(lo.X() + hi.X()) / 2, lo.Y(), (lo.Z() + hi.Z()) / 2}

	var p world.Prefab
	for _, o := range picked {
		moved := world.CloneBrush(o.b, "")
		// BOTH meshes. A detail mesh is authored in the same chunk-local space
		// the hull is and is not derived from it, so a prefab that re-based
		// only the corners would come back with its ledges and reveals still
		// standing where the original chunk's origin used to be.
		for i := range moved.Verts {
			moved.Verts[i] = o.ch.LocalToWorld(moved.Verts[i]).Sub(origin)
		}
		if moved.Detail != nil {
			for i := range moved.Detail.Verts {
				moved.Detail.Verts[i] = o.ch.LocalToWorld(moved.Detail.Verts[i]).Sub(origin)
			}
		}
		p.Brushes = append(p.Brushes, world.BrushToRaw(&moved))
	}

	if err := levelio.SavePrefab(name, &p); err != nil {
		editorSetStatus("Prefab %s: %v", name, err)
		return err
	}
	Editor.PrefabFileNames = nil // rescan, so the picker shows the new file

	// The live link: every placement of this file picks the change up now.
	n, errs := levelio.RestampPrefabsOfFile(lvl, name)
	for _, err := range errs {
		editorSetStatus("Prefab %s: %v", name, err)
	}
	editorSetStatus("Saved %d brush(es) as prefab %q (%d placement(s) updated)",
		len(p.Brushes), name, n)
	return nil
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// ---------- the panel ----------

// editorPrefabAddMenu draws the prefab half of the Explorer's Add menu: which
// file the next placement uses, the ID to give it, and the row that arms it.
func editorPrefabAddMenu() {
	if Editor.PrefabFileNames == nil {
		Editor.PrefabFileNames = levelio.ScanPrefabNames()
	}
	if len(Editor.PrefabFileNames) == 0 {
		imgui.Text("No prefabs in prefabs/")
		editorTextMutedWrapped("Select some brushes and use \"Save Selection as Prefab\" " +
			"in the viewport's right-click menu to make one.")
		return
	}
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newprefab", &Editor.NewPrefabID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it after the file.\nMade unique on placement.")
	}
	imgui.Separator()
	for _, name := range Editor.PrefabFileNames {
		if imgui.MenuItem(name) {
			Editor.PrefabPlaceFile = name
			editorArmPrefabPlacement()
		}
	}
}

// editorPrefabPropertiesSection draws the selected instance's block.
func editorPrefabPropertiesSection() {
	inst := activePrefab()
	if inst == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusPrefab)
	if !imgui.CollapsingHeaderV("Prefab: "+inst.ID+"##prefabhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusPrefab)

	editorPropGroupOpen("Identity##prefabidgrp", func() {
		id := inst.ID
		if imgui.InputTextV("ID##prefabid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != inst.ID {
			EditorPushUndo()
			// Everything stamped from the instance is named after it, so a
			// rename has to re-stamp — the old content's IDs no longer match.
			levelio.DropPrefabContent(Editor.level, inst.ID)
			inst.ID = world.UniquePrefabInstanceID(Editor.level, id)
			restampActivePrefab()
		}
		editorTextMuted("File: " + strconv.Quote(inst.File))
	})

	editorPropGroupOpen("Transform##prefabtfgrp", func() {
		moved := false
		pos := [3]float32{inst.Position.X(), inst.Position.Y(), inst.Position.Z()}
		if editorDragFloat3("Position##prefabpos", &pos, 0, 0, "%.3f") {
			inst.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
			moved = true
		}
		rotDeg := inst.Rotation * 180 / 3.14159265
		if editorDragFloatStep("Rotation (deg)##prefabrot", &rotDeg, -360, 360, "%.1f", 1) {
			inst.Rotation = rotDeg * 3.14159265 / 180
			moved = true
		}
		s := inst.EffectiveScale()
		if editorDragFloat("Scale##prefabscale", &s, 0.01, 100, "%.3f") {
			inst.Scale = s
			moved = true
		}
		if moved {
			// Re-stamped on every drag frame rather than on release: the
			// content IS the feedback, and an instance whose brushes only
			// caught up when the mouse came up would be a gizmo dragging
			// nothing visible.
			restampActivePrefab()
		}
	})

	editorPropGroup("Actions##prefabactions", 0, func() {
		if imgui.Button("Re-stamp##prefabrestamp") {
			// The button exists for a file changed on disk, so it is the one
			// path that has to re-read rather than trust the cache.
			levelio.InvalidatePrefabCache(inst.File)
			restampActivePrefab()
			editorSetStatus("Re-stamped %q from %s", inst.ID, inst.File)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Read the file again. The editor does this for you when\n" +
				"YOU save the prefab; this is for a file changed on disk.")
		}
		imgui.SameLine()
		if imgui.Button("Duplicate##prefabdup") {
			duplicateActivePrefab()
		}
		imgui.SameLine()
		if imgui.Button("Delete##prefabdel") {
			deleteActivePrefab()
		}
	})

	editorTextMutedWrapped("What this instance made is not editable in place — the edit " +
		"belongs in the file, or it is not a prefab edit. Every placement of " +
		strconv.Quote(inst.File) + " updates when it is saved.")
}

// DrawPrefabOutlines draws a wireframe box round every placed instance.
//
// A prefab's content is ordinary level objects, so without this an instance is
// invisible as a *thing*: you can see the lamp, but not that the lamp is one
// unit that moves and deletes together. The selected one is drawn brighter, the
// same way a selected volume's cage is.
func DrawPrefabOutlines() {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	for i := range lvl.Prefabs {
		lo, hi, ok := prefabWorldBounds(lvl, &lvl.Prefabs[i])
		if !ok {
			continue
		}
		col := mgl32.Vec3{0.45, 0.35, 0.7}
		if i == Editor.ActivePrefabIdx {
			col = mgl32.Vec3{0.8, 0.6, 1}
		}
		gizmo.GizmoAABB(lo, hi, col)
	}
}

// prefabWorldBounds is the box round everything an instance made. False when it
// made nothing — an instance with no file yet, or one whose file is missing.
func prefabWorldBounds(lvl *world.Level, inst *world.PrefabInstance) (mgl32.Vec3, mgl32.Vec3, bool) {
	var lo, hi mgl32.Vec3
	first := true
	grow := func(a, b mgl32.Vec3) {
		if first {
			lo, hi, first = a, b, false
			return
		}
		lo = mgl32.Vec3{min32(lo.X(), a.X()), min32(lo.Y(), a.Y()), min32(lo.Z(), a.Z())}
		hi = mgl32.Vec3{max32(hi.X(), b.X()), max32(hi.Y(), b.Y()), max32(hi.Z(), b.Z())}
	}
	for i := range lvl.Chunks {
		if lvl.Chunks[i].FromPrefab == inst.ID {
			a, b := lvl.Chunks[i].WorldBounds()
			grow(a, b)
		}
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].FromPrefab != inst.ID {
			continue
		}
		if a, b, ok := lvl.Volumes[i].WorldAABB(); ok {
			grow(a, b)
		}
	}
	for i := range lvl.Entities {
		if lvl.Entities[i].FromPrefab != inst.ID {
			continue
		}
		if a, b, ok := lvl.Entities[i].WorldAABB(); ok {
			grow(a, b)
		}
	}
	if first {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	return lo, hi, true
}
