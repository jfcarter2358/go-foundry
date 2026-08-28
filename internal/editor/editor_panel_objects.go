package editor

// editor_panel_objects.go — the Objects block: what the level contains.
//
// One list per kind, in a fixed order, drawn identically by both editors. Every
// list is reachable with nothing selected, because placing the first of
// something has to be — which is the one deliberate exception to the rule that
// a panel only shows what the selection can act on.
//
// A list row is the selection: clicking it selects the object, and Properties
// picks it up from there. Nothing in this file draws a field; the buttons
// alongside a row are the ones that act on the object as a whole (delete,
// duplicate, export, switch off), because those are list operations rather than
// properties of the thing.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/world"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorObjectsSection draws every list, in order.
func editorObjectsSection(lvl *world.Level, kind editorPanelKind) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Objects##objects", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorSpawnListSection(lvl)
	editorChunkListSection(lvl)
	editorEntitySection(lvl)
	editorVolumeSection(lvl)
	editorLightListSection(lvl)
	editorLevelScriptSection(lvl)
	_ = kind
}

// ---------- 2D map ----------

// editor2DMapSection draws the sector map's own actions: the corner-height
// toggle and the point-joining workflow that turns picked points into edges
// and, closed, into a sector.
//
// Between Context and Objects rather than folded into either: it is not level
// content (Objects) and it is not a selected object's fields (Properties) — it
// is what the map itself does with whatever is currently picked.
func editor2DMapSection() {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Map##map2d", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	editingFloor := Editor.EditFloorVerts
	if editingFloor {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.4, Z: 0.9, W: 1})
	}
	if imgui.Button("Floor Verts##map2dfloor") {
		Editor.EditFloorVerts = true
	}
	if editingFloor {
		imgui.PopStyleColor()
	}
	imgui.SameLine()
	if !editingFloor {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.9, Y: 0.3, Z: 0.3, W: 1})
	}
	if imgui.Button("Ceiling Verts##map2dceil") {
		Editor.EditFloorVerts = false
	}
	if !editingFloor {
		imgui.PopStyleColor()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Which of a corner's two handles the map picks and drags.")
	}

	editor2DLayerControls()
	editor2DCoplanarWarning()
	editor2DOpeningControls()
	editor2DJoinButtons()
}

// editor2DCoplanarWarning flags ceilings and floors drawn at the same height
// over the same ground, and offers to separate them.
//
// This is the one geometry mistake the renderer cannot paper over: opaque world
// geometry draws with face culling off, so both surfaces rasterize and z-fight,
// and no draw order settles it — the ceiling is correct seen from below and the
// floor is correct seen from above. The flicker is the level saying two things
// occupy one plane, so the editor says so too rather than leaving it to be
// found by looking at it.
func editor2DCoplanarWarning() {
	ch := activeChunk()
	if ch == nil {
		return
	}
	pairs := world.CoplanarStackedSurfaces(ch)
	if len(pairs) == 0 {
		return
	}
	imgui.Separator()
	imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.75, Z: 0.2, W: 1})
	imgui.Text(fmt.Sprintf("%d stacked surface(s) at the same height", len(pairs)))
	imgui.PopStyleColor()
	if len(pairs) > 0 {
		p := pairs[0]
		imgui.Text(fmt.Sprintf("e.g. %s ceiling / %s floor at y=%.2f", p.Lower.ID, p.Upper.ID, p.Height))
	}
	if imgui.Button("Separate##map2dcoplanar") {
		editorSeparateCoplanar(ch)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip(fmt.Sprintf(
			"Drop each offending ceiling by %.2f so the storeys have a slab\n"+
				"between them instead of sharing one plane. Floors are left alone —\n"+
				"moving one would change where everything above it stands.\n\n"+
				"The alternative is to disable one of the two surfaces, if the room\n"+
				"below does not need a ceiling of its own.", world.SlabThickness))
	}
}

// editorSeparateCoplanar applies the slab fix and rebuilds what it moved.
func editorSeparateCoplanar(ch *world.Chunk) {
	EditorPushUndo()
	n := world.SeparateCoplanarSurfaces(ch)
	for si := range ch.Sectors {
		if err := world.ReloadSector(ch, &ch.Sectors[si]); err != nil {
			editorSetStatus("separate: %v", err)
			return
		}
	}
	ch.RebuildGaps()
	editorSetStatus("lowered %d ceiling(s) by %.2f", n, world.SlabThickness)
}

// editor2DOpeningControls draws the floor/ceiling-opening tool: the button that
// arms it, and a count of what the active layer already has.
//
// Which surface a new hole is cut into follows the Floor/Ceiling Verts switch
// above, so the two controls read together — pick the surface, then cut it.
func editor2DOpeningControls() {
	ch := activeChunk()
	if ch == nil {
		return
	}
	imgui.Separator()

	surface := "Floor"
	if !Editor.EditFloorVerts {
		surface = "Ceiling"
	}
	if Editor.OpeningDrawing {
		imgui.Text(fmt.Sprintf("Drawing %s opening: %d corners", surface, len(Editor.OpeningDrawPoints)))
		if imgui.Button("Finish##map2dopenfinish") {
			finishOpeningDraw(ch)
		}
		imgui.SameLine()
		if imgui.Button("Cancel##map2dopencancel") {
			Editor.OpeningDrawing = false
			Editor.OpeningDrawPoints = nil
		}
		imgui.Text("(Enter to finish, Esc to cancel)")
		return
	}

	if imgui.Button(fmt.Sprintf("Cut %s Opening##map2dopen", surface)) {
		EditorBeginOpening()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Cut a hole through this layer's " + strings.ToLower(surface) + " —\n" +
			"a stairwell, a hatch, a hole in a mezzanine.\n" +
			"Click its corners on the map, then Enter.\n" +
			"Right-click a hole to remove it.")
	}

	nFloor, nCeil := 0, 0
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if !layerEditable(s.Layer) {
			continue
		}
		nFloor += len(s.Floor.Openings)
		nCeil += len(s.Ceiling.Openings)
	}
	if nFloor > 0 || nCeil > 0 {
		imgui.Text(fmt.Sprintf("openings: %d floor, %d ceiling", nFloor, nCeil))
	}

	// Repair for holes cut before an opening went through both faces of its
	// slab: they read as open from the side they were drawn on and are covered
	// from the other. Offered rather than applied on load, since it changes the
	// shape of the level.
	if n := world.UnpairedOpenings(ch); n > 0 {
		imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.75, Z: 0.2, W: 1})
		imgui.Text(fmt.Sprintf("%d opening(s) cut through one face only", n))
		imgui.PopStyleColor()
		if imgui.Button("Cut Through Slab##map2dopenpair") {
			editorPairOpenings(ch)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Cut the matching hole in the surface on the other side,\n" +
				"so the shaft is open from both rooms. A hole through only one\n" +
				"face is still covered by the other.")
		}
	}
}

// editorPairOpenings applies the slab-pairing repair and rebuilds the chunk.
func editorPairOpenings(ch *world.Chunk) {
	EditorPushUndo()
	n := world.PairOpeningsThroughSlabs(ch)
	for si := range ch.Sectors {
		if err := world.ReloadSector(ch, &ch.Sectors[si]); err != nil {
			editorSetStatus("pair openings: %v", err)
			return
		}
	}
	ch.RebuildGaps()
	editorSetStatus("cut %d opening(s) through to the other side", n)
}

// editor2DLayerControls draws the map's layer selector: which of the chunk's
// authoring bands is being edited, how much of the rest is drawn around it, and
// the band's own name and heights.
//
// Shown only for a chunk that actually has more than one layer, plus the button
// that makes a second one. A chunk with a single storey — which is most of
// them, and every chunk authored before layers existed — should not have to
// look at a control for a thing it does not use.
func editor2DLayerControls() {
	ch := activeChunk()
	if ch == nil {
		return
	}
	imgui.Separator()

	n := ch.LayerCount()
	cur := activeLayer()
	if n <= 1 {
		if imgui.Button("Add Layer##map2dlayeradd") {
			editorAddLayer()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Layers let one chunk hold sectors that overlap from above —\n" +
				"an overhang, a mezzanine, a bridge. The map edits one at a time.")
		}
		return
	}

	imgui.Text(fmt.Sprintf("Layer %d/%d", cur+1, n))
	imgui.SameLine()
	if imgui.Button("-##map2dlayerdown") {
		editorStepLayer(-1)
	}
	imgui.SameLine()
	if imgui.Button("+##map2dlayerup") {
		editorStepLayer(1)
	}
	imgui.SameLine()
	imgui.Text("(PgUp/PgDn)")

	// The band's own fields. Editing them materializes stored metadata for
	// every layer up to this one — before that the band may only have been
	// implied, and there is nothing to write into.
	band := ch.LayerAt(cur)
	name := band.Name
	if imgui.InputText("Name##map2dlayername", &name) {
		EditorPushUndo()
		ch.EnsureLayers(cur + 1)
		ch.Layers[cur].Name = name
	}
	base, ceil := band.BaseY, band.CeilY
	if imgui.DragFloat("Base Y##map2dlayerbase", &base) {
		EditorPushUndo()
		ch.EnsureLayers(cur + 1)
		ch.Layers[cur].BaseY = base
	}
	if imgui.DragFloat("Ceil Y##map2dlayerceil", &ceil) {
		EditorPushUndo()
		ch.EnsureLayers(cur + 1)
		ch.Layers[cur].CeilY = ceil
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The heights a sector drawn on this layer starts at.\n" +
			"Advisory: nothing stops geometry leaving its band, and a ramp is supposed to.")
	}

	views := []struct {
		label string
		mode  int
		tip   string
	}{
		{"Stack##map2dlayerall", layerViewStack, "Draw every layer, the inactive ones ghosted."},
		{"Below##map2dlayerbelow", layerViewBelow, "Draw this layer and the ones under it."},
		{"Only##map2dlayeriso", layerViewIsolate, "Draw this layer alone."},
	}
	for i, v := range views {
		if i > 0 {
			imgui.SameLine()
		}
		on := Editor.LayerView == v.mode
		if on {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.4, Z: 0.9, W: 1})
		}
		if imgui.Button(v.label) {
			Editor.LayerView = v.mode
		}
		if on {
			imgui.PopStyleColor()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(v.tip)
		}
	}

	if imgui.Button("Add Layer##map2dlayeradd") {
		editorAddLayer()
	}
	imgui.SameLine()
	if imgui.Button("Merge Down##map2dlayerdel") {
		editorRemoveLayer()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Delete this layer, moving its sectors onto the one below.\n" +
			"Geometry is kept — only which band it is filed under changes.")
	}
}

// ---------- spawn point ----------

// editorSpawnListSection draws the level's one spawn point as a row.
//
// A row rather than a list, because a level has exactly one: there is nothing
// to pick from, and "place" and "move" are the same operation. The checkbox is
// whether the level has one at all — unticking deletes it, ticking puts one
// back at the origin, which is where a new level's starts.
func editorSpawnListSection(lvl *world.Level) {
	has := lvl.Spawn != nil
	on := has
	if imgui.Checkbox("##spawnexists", &on) && on != has {
		EditorPushUndo()
		if on {
			placeSpawnPointAt(mgl32.Vec3{})
		} else {
			lvl.Spawn.Delete()
			lvl.Spawn = nil
			editorClearSpawnSelection()
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Whether this level has a spawn point at all.\n" +
			"Ticking puts one back at the origin.")
	}
	imgui.SameLine()
	if imgui.SelectableV("Player Spawn##spawnrow", Editor.SpawnSelected, 0, imgui.Vec2{}) {
		if lvl.Spawn != nil {
			editorSelectSpawn()
		}
	}
	if lvl.Spawn == nil {
		return
	}

	label := "Move Spawn Point"
	if imgui.Button(label + "##spawnplace") {
		Editor.SpawnPlacementMode = true
	}
	if Editor.SpawnPlacementMode {
		imgui.SameLine()
		imgui.Text("Click to place...")
	}
}

// ---------- chunks ----------

// editorChunkListSection draws the chunk list plus the new/import controls.
// The popups it opens are drawn by editorChunkPopups.
//
// This list is the only way to choose which chunk is active — the toolbar's
// dropdown is gone. Two controls for one piece of state meant the picker and
// the list could disagree about it, and the list is where every other selection
// in the panel is made.
func editorChunkListSection(lvl *world.Level) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Chunks##chunks", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	if imgui.Button("+##chunknew") {
		Editor.ShowNewChunk = true
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A new chunk at the camera.")
	}
	imgui.SameLine()
	if imgui.Button("Import##chunkimport") {
		editorOpenImportChunk()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Import a chunk from a file in chunks/.")
	}
	imgui.SameLine()
	imgui.Text(fmt.Sprintf("(%d)", len(lvl.Chunks)))

	remove, duplicate, export := -1, -1, -1
	for i := range lvl.Chunks {
		ch := &lvl.Chunks[i]
		imgui.PushID(fmt.Sprintf("chunkrow%d", i))
		if imgui.Button("x") {
			remove = i
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Delete this chunk.")
		}
		imgui.SameLine()
		if imgui.Button("Export") {
			export = i
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Export this chunk to a file.")
		}
		imgui.SameLine()
		if imgui.Button("+") {
			duplicate = i
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Duplicate this chunk.")
		}
		imgui.SameLine()
		// Selected — not merely active — is what puts the highlight on, so the
		// context line's "(selected)" and this row agree.
		selected := Editor.ChunkSelected && i == Editor.ActiveChunkIdx
		label := ch.ID
		if i == Editor.ActiveChunkIdx && !selected {
			label += "  (active)"
		}
		if imgui.SelectableV(label, selected, 0, imgui.Vec2{}) {
			editorSelectChunk(i)
		}
		imgui.PopID()
	}

	// Acted on outside the loop: each rewrites the slice being walked.
	switch {
	case export >= 0:
		Editor.ActiveChunkIdx = export
		Editor.SaveChunkName = lvl.Chunks[export].ID
		Editor.ShowSaveChunk = true
	case duplicate >= 0:
		EditorPushUndo()
		Editor.ActiveChunkIdx = duplicate
		duplicateActiveChunk()
	case remove >= 0:
		Editor.ActiveChunkIdx = remove
		Editor.ShowDeleteChunk = true
	}
}

// ---------- level scripts ----------

// editorLevelScriptSection draws the level's own .flang handlers.
//
// The level has carried these since scripting arrived and no panel has ever
// shown them, so the only way to attach one was to edit the .yaml by hand. They
// are level-wide rather than hung on an object, which is what a script wants
// when it is about the level itself: what happens on load, what a global timer
// does, what the map's rules are.
func editorLevelScriptSection(lvl *world.Level) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Scripts##lvlscripts", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorTextMutedWrapped("Run for the level as a whole, rather than for one object in it.")

	if Editor.ScriptFileNames == nil {
		editorRefreshScriptFiles()
	}

	remove := -1
	for i, path := range lvl.Scripts {
		imgui.PushID(fmt.Sprintf("lvlscript%d", i))
		if imgui.Button("x") {
			remove = i
		}
		imgui.SameLine()
		if imgui.Button("Edit") {
			OpenScriptEditorWindow()
			scriptEditorOpenFile(strings.TrimPrefix(path, fileutil.ScriptsDirName+"/"))
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Open in the script editor.")
		}
		imgui.SameLine()
		imgui.Text(path)
		imgui.PopID()
	}
	if len(lvl.Scripts) == 0 {
		editorTextMuted("(none)")
	}
	if remove >= 0 {
		EditorPushUndo()
		lvl.Scripts = append(lvl.Scripts[:remove], lvl.Scripts[remove+1:]...)
	}

	// The add row is a picker over the scripts folder rather than a text field,
	// for the reason every other script picker is: a mistyped path is a script
	// that silently never runs, which is among the harder things to notice.
	if imgui.BeginCombo("##lvlscriptadd", "Add script...") {
		for _, name := range Editor.ScriptFileNames {
			path := fileutil.ScriptsDirName + "/" + name
			if editorLevelHasScript(lvl, path) {
				continue
			}
			if imgui.SelectableV(name, false, 0, imgui.Vec2{}) {
				EditorPushUndo()
				lvl.Scripts = append(lvl.Scripts, path)
			}
		}
		imgui.EndCombo()
	}
	imgui.SameLine()
	if imgui.Button("Rescan##lvlscriptrescan") {
		editorRefreshScriptFiles()
	}
}

// editorLevelHasScript reports whether the level already runs path, so the add
// picker does not offer a duplicate — running one file twice does nothing the
// first run did not.
func editorLevelHasScript(lvl *world.Level, path string) bool {
	for _, s := range lvl.Scripts {
		if s == path {
			return true
		}
	}
	return false
}
