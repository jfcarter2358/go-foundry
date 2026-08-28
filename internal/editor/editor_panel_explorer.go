package editor

// editor_panel_explorer.go — the Explorer: one tree holding everything the
// level contains.
//
// The panel used to draw a separate always-open section per kind — Spawn,
// Chunks, Entities, Volumes, Lights, Scripts — each with its own catalog, its
// own place button and a row of action buttons beside every row. All of it was
// on screen at once, in both editors, whether or not any of it was what was
// being worked on, and the object actually selected was somewhere below the
// pile.
//
// One tree instead. A row is the selection and nothing else: click it and
// Properties below draws that object's fields, and only that object's. What
// acts on a row as a whole — delete, duplicate, export, switch off — is on the
// row's right-click menu, where it is reachable without occupying a line, and
// what creates something is on the one Add menu at the top, where the first of
// anything gets placed.
//
// Nothing here draws a property. If a control edits a field of an object it
// belongs in Properties (editor_panel_properties.go); this file only decides
// what exists and what is picked.

import (
	"fmt"
	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// explorerMinHeight is the shortest the tree's scroll box may be set to. Below
// about this the tree is a slot rather than a list, and scrolling it costs more
// than the space it saves.
const explorerMinHeight = 80

// explorerRowFlags are the flags every parent row in the tree carries: the
// label selects and only the arrow expands, and the whole row's width is the
// click target so a short name is not a small one.
const explorerRowFlags = imgui.TreeNodeFlagsOpenOnArrow |
	imgui.TreeNodeFlagsSpanAvailWidth

// editorExplorerSection draws the Explorer: the Add menu, then the tree, in a
// scroll box of its own so Properties below it stays on screen no matter how
// much the level holds.
func editorExplorerSection(lvl *world.Level, kind editorPanelKind) {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Explorer##explorer", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	editorExplorerAddMenu(lvl, kind)
	imgui.SameLine()
	editorTextMuted("Right-click a row for its actions")
	editorExplorerPlacementBanner()

	h := Editor.ExplorerHeight
	if h < explorerMinHeight {
		h = explorerMinHeight
	}
	imgui.BeginChildV("##explorertree", imgui.Vec2{X: 0, Y: h}, true, 0)
	editorExplorerSpawnRow(lvl)
	editorExplorerChunkNodes(lvl)
	editorExplorerEntityNodes(lvl)
	editorExplorerVolumeNodes(lvl)
	editorExplorerSpriteNodes(lvl)
	editorExplorerEmitterNodes(lvl)
	editorExplorerSoundNodes(lvl)
	editorExplorerLightNodes(lvl)
	editorExplorerScriptNodes(lvl)
	imgui.EndChild()
}

// editorExplorerPlacementBanner says so while a click is armed to place
// something, and offers the way out of it.
//
// The Add menu closes the moment it is used, so without this the only sign
// that the next click will drop an entity is the entity that appears. It is
// drawn where the Add button is because that is what armed it.
func editorExplorerPlacementBanner() {
	label, cancel := "", func() {}
	switch {
	case Editor.EntityPlacementMode:
		what := Editor.EntityPlaceType
		if what == "" {
			what = "a blank entity"
		}
		label = "Click in the view to place " + what
		cancel = func() { Editor.EntityPlacementMode = false }
	case Editor.VolumePlacementMode:
		label = "Click in the view to place a volume"
		cancel = func() { Editor.VolumePlacementMode = false }
	case Editor.SpawnPlacementMode:
		label = "Click in the view to place the spawn point"
		cancel = func() { Editor.SpawnPlacementMode = false }
	case Editor.BiomeDrawing:
		label = fmt.Sprintf("Drawing a biome sector: %d corners (Enter to finish)",
			len(Editor.BiomeDrawPoints))
		cancel = func() {
			Editor.BiomeDrawing = false
			Editor.BiomeDrawPoints = nil
		}
	default:
		return
	}

	editorTextColored(imgui.Vec4{X: 1, Y: 0.75, Z: 0.2, W: 1}, label)
	if imgui.Button("Cancel##explorerplacecancel") {
		cancel()
	}
	if Editor.BiomeDrawing {
		imgui.SameLine()
		if imgui.Button("Finish##explorerplacefinish") {
			if ch := activeChunk(); ch != nil {
				finishBiomeSectorDraw(ch)
			}
		}
	}
}

// ---------- shared row mechanics ----------

// editorExplorerCategory draws a category node — Chunks, Entities, Lights and
// the rest. Nothing is selectable about a category; it is a container, and it
// opens itself when something inside it has just become the selection so a
// click in the viewport reveals the row it picked.
func editorExplorerCategory(label string, revealFor ...string) bool {
	if editorPanelFocused(revealFor...) {
		imgui.SetNextItemOpen(true, imgui.ConditionAlways)
	}
	return imgui.TreeNodeV(label, imgui.TreeNodeFlagsSpanAvailWidth|imgui.TreeNodeFlagsDefaultOpen)
}

// editorExplorerLeaf draws one selectable row and reports whether it was
// clicked. focus names the PanelFocus value that means "this row is what was
// just selected", which is what scrolls the tree to it.
func editorExplorerLeaf(label string, selected bool, focus string) bool {
	if selected && focus != panelFocusNone && editorPanelFocused(focus) {
		imgui.SetScrollHereY(0.5)
	}
	return imgui.SelectableV(label, selected, 0, imgui.Vec2{})
}

// editorExplorerMenu opens the right-click menu for the row just drawn and runs
// body inside it. Every row that has actions uses this, so they are all in the
// same place with the same gesture.
//
// id must be unique among the rows sharing an ID scope, and it is not optional:
// imgui-go's argument-free BeginPopupContextItem passes an empty *string* where
// ImGui wants a null pointer, so instead of "use the row that was clicked" it
// means "use the ID hashed from an empty name" — one id shared by every row in
// the window. Every row's menu then belongs to the same popup, they all Begin
// it in the same frame, and what comes out is not the menu for the row that was
// right-clicked. Naming it per row is what makes the gesture land on the right
// object.
func editorExplorerMenu(id string, body func()) {
	if !imgui.BeginPopupContextItemV(id, imgui.PopupFlagsMouseButtonRight) {
		return
	}
	body()
	imgui.EndPopup()
}

// ---------- spawn ----------

// editorExplorerSpawnRow draws the level's one spawn point.
//
// It is always a row, even when the level has none: "this level has nowhere to
// start" is worth reading off the tree, and the right-click menu is then how
// one gets put back.
func editorExplorerSpawnRow(lvl *world.Level) {
	label := "Player Spawn"
	if lvl.Spawn == nil {
		label += "  (none)"
	}
	if editorExplorerLeaf(label+"##spawnrow", Editor.SpawnSelected, panelFocusSpawn) && lvl.Spawn != nil {
		editorSelectSpawn()
	}
	editorExplorerMenu("spawnctx", func() {
		if lvl.Spawn == nil {
			if imgui.MenuItem("Add Player Spawn") {
				EditorPushUndo()
				placeSpawnPointAt(editorExplorerPlacePoint())
			}
			return
		}
		if imgui.MenuItem("Move To Camera") {
			EditorPushUndo()
			placeSpawnPointAt(editorExplorerPlacePoint())
		}
		if imgui.MenuItem("Place By Clicking") {
			editorSelectSpawn()
			Editor.SpawnPlacementMode = true
		}
		imgui.Separator()
		if imgui.MenuItem("Delete") {
			EditorPushUndo()
			lvl.Spawn.Delete()
			lvl.Spawn = nil
			editorClearSpawnSelection()
		}
	})
}

// editorExplorerPlacePoint is where a "put one here" action puts it: what the
// active sub-mode is looking at. The 3D view has a camera and the map has a
// pan centre, and neither knows about the other's.
func editorExplorerPlacePoint() mgl32.Vec3 {
	if Editor.Mode == Mode2D {
		return mgl32.Vec3{Editor.Pan2D.X(), 0, Editor.Pan2D.Y()}
	}
	return mgl32.Vec3{gfx.Cam.Position.X(), 0, gfx.Cam.Position.Z()}
}

// ---------- chunks, and what lives inside one ----------

// editorExplorerChunkNodes draws the chunk list, each chunk expanding into its
// sectors and its painted biome sectors.
//
// Clicking a chunk makes it both active and selected — see editor_chunk.go on
// why those are two things — so this list is still the only place either is
// decided, and the Context line above reports which.
func editorExplorerChunkNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Chunks (%d)##chunks", len(lvl.Chunks)),
		panelFocusChunk, panelFocusSector, panelFocusBiome, panelFocusWater) {
		return
	}
	defer imgui.TreePop()

	remove, duplicate, export, toggle := -1, -1, -1, -1
	for i := range lvl.Chunks {
		ch := &lvl.Chunks[i]
		imgui.PushID(fmt.Sprintf("chunk%d", i))

		label := ch.ID
		if !ch.IsEnabled() {
			// A disabled chunk draws nothing in the 3D view, so this row and
			// the sector map are the only places it is visible at all — say so
			// rather than leaving the author looking for geometry that will
			// never appear. See world/chunk_enable.go.
			label += "  (disabled)"
		}
		if i == Editor.ActiveChunkIdx {
			label += "  (active)"
		}
		flags := explorerRowFlags
		if Editor.ChunkSelected && i == Editor.ActiveChunkIdx {
			flags |= imgui.TreeNodeFlagsSelected
		}
		// The active chunk's contents are what everything else in the panel is
		// about, so its node starts open and the rest start shut.
		if i == Editor.ActiveChunkIdx {
			flags |= imgui.TreeNodeFlagsDefaultOpen
		}
		// "##chunknode" keeps the node's ID stable while its label is not:
		// imgui derives a node's ID from its label, and "  (active)" coming
		// and going would otherwise reset which chunks are expanded every time
		// a different one is clicked.
		//
		// NoTreePushOnOpen (and the manual Indent below) keeps the ID stack the
		// same whether the node is open or shut, so the row's popup keeps one
		// id across an expand — a popup whose id moves out from under it closes
		// on the frame it moves.
		open := imgui.TreeNodeV(label+"##chunknode", flags|imgui.TreeNodeFlagsNoTreePushOnOpen)
		if imgui.IsItemClicked() {
			editorSelectChunk(i)
		}
		editorExplorerMenu("chunkctx", func() {
			if imgui.MenuItemV("Enabled", "", ch.IsEnabled(), true) {
				toggle = i
			}
			imgui.Separator()
			if imgui.MenuItem("Duplicate") {
				duplicate = i
			}
			if imgui.MenuItem("Export...") {
				export = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				remove = i
			}
		})
		if open {
			imgui.Indent()
			editorExplorerSectorNodes(ch, i)
			editorExplorerBiomeNodes(ch, i)
			imgui.Unindent()
		}
		imgui.PopID()
	}

	// Acted on outside the loop: each rewrites the slice being walked.
	switch {
	case toggle >= 0:
		EditorPushUndo()
		ch := &lvl.Chunks[toggle]
		if world.SetChunkEnabled(ch, !ch.IsEnabled()) {
			editorChunkTransformChanged()
		}
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

// editorExplorerSectorNodes draws a chunk's sectors.
//
// The sector list used to be the 2D panel's alone, which meant the 3D view had
// no way to reach a sector except by finding a face of it to click — and a
// sector whose surfaces are all switched off has none.
func editorExplorerSectorNodes(ch *world.Chunk, chunkIdx int) {
	if len(ch.Sectors) == 0 {
		return
	}
	if !editorExplorerCategory(fmt.Sprintf("Sectors (%d)##sectors", len(ch.Sectors)), panelFocusSector) {
		return
	}
	for i := range ch.Sectors {
		selected := chunkIdx == Editor.ActiveChunkIdx && i == Editor.ActiveSectorIdx &&
			!Editor.ChunkSelected
		if editorExplorerLeaf(fmt.Sprintf("%s##sec%d", ch.Sectors[i].ID, i), selected, panelFocusSector) {
			editorSelectSector(chunkIdx, i)
		}
	}
	imgui.TreePop()
}

// editorExplorerBiomeNodes draws a chunk's painted biome sectors.
//
// Listed in both editors although only the map can reshape one: a water plane
// clicked in the 3D view selects the biome sector behind it, and the row is
// then what says which of them that was.
func editorExplorerBiomeNodes(ch *world.Chunk, chunkIdx int) {
	if len(ch.BiomeSectors) == 0 {
		return
	}
	if !editorExplorerCategory(fmt.Sprintf("Biome (%d)##biomes", len(ch.BiomeSectors)),
		panelFocusBiome, panelFocusWater) {
		return
	}
	remove := -1
	for i := range ch.BiomeSectors {
		bs := &ch.BiomeSectors[i]
		selected := chunkIdx == Editor.ActiveChunkIdx && i == Editor.ActiveBiomeSectorIdx
		label := fmt.Sprintf("%s (%s)##bs%d", bs.ID, biomeKindName(bs.Brush.Kind), i)
		if editorExplorerLeaf(label, selected, panelFocusBiome) {
			editorSelectBiomeSector(chunkIdx, i)
		}
		editorExplorerMenu(fmt.Sprintf("bsctx%d", i), func() {
			if imgui.MenuItem("Delete") {
				remove = i
			}
		})
	}
	if remove >= 0 {
		EditorPushUndo()
		deleteBiomeSector(ch, remove)
	}
	imgui.TreePop()
}

// ---------- entities ----------

func editorExplorerEntityNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Entities (%d)##entities", len(lvl.Entities)), panelFocusEntity) {
		return
	}
	defer imgui.TreePop()

	if len(lvl.Entities) == 0 {
		editorTextMuted("(none)")
		return
	}
	del, dup := -1, -1
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		label := e.ID
		if e.Type != "" && e.Type != e.ID {
			label = fmt.Sprintf("%s  (%s)", e.ID, e.Type)
		}
		if editorExplorerLeaf(fmt.Sprintf("%s##ent%d", label, i), i == Editor.ActiveEntityIdx, panelFocusEntity) {
			editorSelectEntity(i)
		}
		editorExplorerMenu(fmt.Sprintf("entctx%d", i), func() {
			if imgui.MenuItem("Duplicate") {
				dup = i
			}
			if imgui.MenuItem("Move To Camera") {
				// Eye height off, the same as the properties button: an
				// entity's position is its base.
				e.Position = gfx.Cam.Position.Sub(mgl32.Vec3{0, world.EyeHeight, 0})
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = i
			}
		})
	}
	// Both act on the selection, so the row is selected first — which is also
	// what the panel below should be showing once the action has run.
	if dup >= 0 {
		editorSelectEntity(dup)
		duplicateActiveEntity()
	}
	if del >= 0 {
		editorSelectEntity(del)
		deleteActiveEntity()
	}
}

// ---------- volumes ----------

func editorExplorerVolumeNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Volumes (%d)##volumes", len(lvl.Volumes)), panelFocusVolume) {
		return
	}
	defer imgui.TreePop()

	if len(lvl.Volumes) == 0 {
		editorTextMuted("(none)")
		return
	}
	del, dup := -1, -1
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		label := v.ID
		if label == "" {
			label = "(unnamed)"
		}
		if !v.Enabled {
			// A disarmed volume is still there and still selectable; saying so
			// on the row saves hunting for why a trigger stopped firing.
			label += "  (disabled)"
		}
		if editorExplorerLeaf(fmt.Sprintf("%s##vol%d", label, i), i == Editor.ActiveVolumeIdx, panelFocusVolume) {
			editorSelectVolume(i)
		}
		editorExplorerMenu(fmt.Sprintf("volctx%d", i), func() {
			enabled := v.Enabled
			if imgui.MenuItemV("Enabled", "", enabled, true) {
				EditorPushUndo()
				v.Enabled = !enabled
			}
			if imgui.MenuItem("Duplicate") {
				dup = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = i
			}
		})
	}
	if dup >= 0 {
		editorSelectVolume(dup)
		duplicateActiveVolume()
	}
	if del >= 0 {
		editorSelectVolume(del)
		deleteActiveVolume()
	}
}

// ---------- lights ----------

// editorExplorerLightNodes draws the global light and both light lists as one
// flat run of rows.
//
// Flat rather than nested by kind: the labels already say which kind each is,
// and a level rarely has enough lights for the extra level of tree to be
// paying for itself.
func editorExplorerLightNodes(lvl *world.Level) {
	n := 1 + len(lvl.Lights.Directional) + len(lvl.Lights.Points)
	if !editorExplorerCategory(fmt.Sprintf("Lights (%d)##lights", n), panelFocusLight) {
		return
	}
	defer imgui.TreePop()

	globalLabel := "Global Light"
	if lvl.Lights.Global.Disabled {
		globalLabel += "  (off)"
	}
	if editorExplorerLeaf(globalLabel+"##globallightrow",
		Editor.SelectedLightKind == lightKindGlobal, panelFocusLight) {
		editorSelectLight(lightKindGlobal, 0)
	}
	editorExplorerMenu("globallightctx", func() {
		on := !lvl.Lights.Global.Disabled
		if imgui.MenuItemV("Enabled", "", on, true) {
			EditorPushUndo()
			lvl.Lights.Global.Disabled = on
			world.UploadLights(lvl.Lights)
		}
	})

	editorExplorerDirLightRows(lvl)
	editorExplorerPointLightRows(lvl)
}

func editorExplorerDirLightRows(lvl *world.Level) {
	remove, duplicate := -1, -1
	for i := range lvl.Lights.Directional {
		dl := &lvl.Lights.Directional[i]
		imgui.PushID(fmt.Sprintf("dirlight%d", i))
		label := editorDirLightLabel(dl, i) + "  (directional)"
		if dl.Disabled {
			label += "  (off)"
		}
		selected := Editor.SelectedLightKind == lightKindDir && Editor.SelectedLightIdx == i
		if editorExplorerLeaf(label+"##dirlightrow", selected, panelFocusLight) {
			editorSelectLight(lightKindDir, i)
		}
		editorExplorerMenu("dirlightctx", func() {
			on := !dl.Disabled
			if imgui.MenuItemV("Enabled", "", on, true) {
				EditorPushUndo()
				dl.Disabled = on
				world.UploadLights(lvl.Lights)
			}
			if imgui.MenuItemV("Duplicate", "", false,
				len(lvl.Lights.Directional) < world.MaxDirLights) {
				duplicate = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				remove = i
			}
		})
		imgui.PopID()
	}

	if duplicate >= 0 {
		EditorPushUndo()
		copy := lvl.Lights.Directional[duplicate]
		copy.ID = ""
		copy.ScriptData = world.CloneScriptData(copy.ScriptData)
		lvl.Lights.Directional = append(lvl.Lights.Directional, copy)
		world.AssignLightIDs(&lvl.Lights)
		editorSelectLight(lightKindDir, len(lvl.Lights.Directional)-1)
		world.UploadLights(lvl.Lights)
	}
	if remove >= 0 {
		EditorPushUndo()
		lvl.Lights.Directional = append(lvl.Lights.Directional[:remove], lvl.Lights.Directional[remove+1:]...)
		editorLightRemoved(lightKindDir, remove)
		world.UploadLights(lvl.Lights)
	}
}

func editorExplorerPointLightRows(lvl *world.Level) {
	remove, duplicate := -1, -1
	for i := range lvl.Lights.Points {
		pl := &lvl.Lights.Points[i]
		imgui.PushID(fmt.Sprintf("ptlight%d", i))
		label := editorPointLightLabel(pl, i) + "  (point)"
		if pl.Disabled {
			label += "  (off)"
		}
		selected := Editor.SelectedLightKind == lightKindPoint && Editor.SelectedLightIdx == i
		if editorExplorerLeaf(label+"##ptlightrow", selected, panelFocusLight) {
			editorSelectLight(lightKindPoint, i)
		}
		editorExplorerMenu("ptlightctx", func() {
			on := !pl.Disabled
			if imgui.MenuItemV("Enabled", "", on, true) {
				EditorPushUndo()
				pl.Disabled = on
				world.UploadLights(lvl.Lights)
			}
			if imgui.MenuItemV("Duplicate", "", false,
				len(lvl.Lights.Points) < world.MaxPointLights) {
				duplicate = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				remove = i
			}
		})
		imgui.PopID()
	}

	if duplicate >= 0 {
		EditorPushUndo()
		copy := lvl.Lights.Points[duplicate]
		copy.ID = ""
		copy.ScriptData = world.CloneScriptData(copy.ScriptData)
		lvl.Lights.Points = append(lvl.Lights.Points, copy)
		world.AssignLightIDs(&lvl.Lights)
		editorSelectLight(lightKindPoint, len(lvl.Lights.Points)-1)
		world.UploadLights(lvl.Lights)
	}
	if remove >= 0 {
		EditorPushUndo()
		lvl.Lights.Points = append(lvl.Lights.Points[:remove], lvl.Lights.Points[remove+1:]...)
		editorLightRemoved(lightKindPoint, remove)
		world.UploadLights(lvl.Lights)
	}
}

// ---------- level scripts ----------

// editorExplorerScriptNodes draws the .flang files the level as a whole runs.
//
// A script is a file rather than an object, so a row has no properties to open
// and clicking one opens it in the script editor instead — which is the only
// thing anyone wants from a script row.
func editorExplorerScriptNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Scripts (%d)##lvlscripts", len(lvl.Scripts))) {
		return
	}
	defer imgui.TreePop()

	if len(lvl.Scripts) == 0 {
		editorTextMuted("(none)")
		return
	}
	remove := -1
	for i, path := range lvl.Scripts {
		if editorExplorerLeaf(fmt.Sprintf("%s##lvlscript%d", path, i), false, "") {
			OpenScriptEditorWindow()
			scriptEditorOpenFile(strings.TrimPrefix(path, fileutil.ScriptsDirName+"/"))
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Runs for the level as a whole, rather than for one\n" +
				"object in it. Click to open it in the script editor.")
		}
		editorExplorerMenu(fmt.Sprintf("scriptctx%d", i), func() {
			if imgui.MenuItem("Remove From Level") {
				remove = i
			}
		})
	}
	if remove >= 0 {
		EditorPushUndo()
		lvl.Scripts = append(lvl.Scripts[:remove], lvl.Scripts[remove+1:]...)
	}
}

// editorLevelHasScript reports whether the level already runs path, so the add
// menu does not offer a duplicate — running one file twice does nothing the
// first run did not.
func editorLevelHasScript(lvl *world.Level, path string) bool {
	for _, s := range lvl.Scripts {
		if s == path {
			return true
		}
	}
	return false
}

// ---------- the Add menu ----------

// editorExplorerAddMenu draws the one button that creates things, and the menu
// behind it.
//
// One menu rather than a place button per kind: creating something is rare
// next to selecting something, and the catalogs it needs — the entity type
// list, the script folder — are three lines each that were on screen
// permanently for the sake of a click that happens once a session.
func editorExplorerAddMenu(lvl *world.Level, kind editorPanelKind) {
	if imgui.Button("Add...##exploreradd") {
		imgui.OpenPopup("explorer_add")
	}
	if !imgui.BeginPopup("explorer_add") {
		return
	}
	defer imgui.EndPopup()

	if imgui.MenuItem("Chunk...") {
		Editor.ShowNewChunk = true
	}
	if imgui.MenuItem("Import Chunk...") {
		editorOpenImportChunk()
	}

	imgui.Separator()
	if imgui.BeginMenu("Entity") {
		editorEntityAddMenu()
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Volume") {
		editorVolumeAddMenu()
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Sprite") {
		editorSpriteAddMenu()
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Emitter") {
		editorEmitterAddMenu()
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Sound") {
		editorSoundAddMenu()
		imgui.EndMenu()
	}

	imgui.Separator()
	if imgui.MenuItemV("Directional Light", "", false,
		len(lvl.Lights.Directional) < world.MaxDirLights) {
		editorAddDirLight(lvl)
	}
	if imgui.MenuItemV("Point Light", "", false,
		len(lvl.Lights.Points) < world.MaxPointLights) {
		editorAddPointLight(lvl)
	}

	if kind == panelKind2D && activeChunk() != nil {
		imgui.Separator()
		if imgui.MenuItem("Biome Sector") {
			Editor.BiomeLayerActive = true
			Editor.BiomeDrawing = true
			Editor.BiomeDrawPoints = nil
			Editor.ActiveBiomeSectorIdx = -1
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Paint a ground patch on the map: click its corners, then Enter.")
		}
	}

	if lvl.Spawn == nil {
		imgui.Separator()
		if imgui.MenuItem("Player Spawn") {
			EditorPushUndo()
			placeSpawnPointAt(editorExplorerPlacePoint())
		}
	}

	imgui.Separator()
	if imgui.BeginMenu("Level Script") {
		editorExplorerAddScriptMenu(lvl)
		imgui.EndMenu()
	}
}

// editorExplorerAddScriptMenu lists the scripts folder.
//
// A picker over what is on disk rather than a text field, for the reason every
// other script picker in the editor is one: a mistyped path is a script that
// silently never runs, which is among the harder things to notice.
func editorExplorerAddScriptMenu(lvl *world.Level) {
	if Editor.ScriptFileNames == nil {
		editorRefreshScriptFiles()
	}
	if imgui.MenuItem("Rescan Folder") {
		editorRefreshScriptFiles()
	}
	imgui.Separator()

	offered := 0
	for _, name := range Editor.ScriptFileNames {
		path := fileutil.ScriptsDirName + "/" + name
		if editorLevelHasScript(lvl, path) {
			continue
		}
		offered++
		if imgui.MenuItem(name) {
			EditorPushUndo()
			lvl.Scripts = append(lvl.Scripts, path)
		}
	}
	if offered == 0 {
		editorTextMuted("(nothing left to add)")
	}
}

// ---------- sprites ----------

// editorExplorerSpriteNodes draws the level's sprites, one leaf each, with the
// same per-row menu the volume rows carry.
func editorExplorerSpriteNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Sprites (%d)##sprites", len(lvl.Sprites)), panelFocusSprite) {
		return
	}
	defer imgui.TreePop()

	if len(lvl.Sprites) == 0 {
		editorTextMuted("(none)")
		return
	}
	del, dup := -1, -1
	for i := range lvl.Sprites {
		s := &lvl.Sprites[i]
		label := s.ID
		if label == "" {
			label = "(unnamed)"
		}
		// What the row says beyond the name is what would otherwise send an
		// author clicking through every sprite to find the one that is off, or
		// the one riding the ship.
		if !s.Visible {
			label += "  (hidden)"
		}
		if !s.Attach.IsZero() {
			label += "  [" + s.Attach.Describe() + "]"
		}
		if editorExplorerLeaf(fmt.Sprintf("%s##spr%d", label, i), i == Editor.ActiveSpriteIdx, panelFocusSprite) {
			editorSelectSprite(i)
		}
		editorExplorerMenu(fmt.Sprintf("sprctx%d", i), func() {
			visible := s.Visible
			if imgui.MenuItemV("Visible", "", visible, true) {
				EditorPushUndo()
				s.Visible = !visible
			}
			if imgui.MenuItem("Duplicate") {
				dup = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = i
			}
		})
	}
	if dup >= 0 {
		editorSelectSprite(dup)
		duplicateActiveSprite()
	}
	if del >= 0 {
		editorSelectSprite(del)
		deleteActiveSprite()
	}
}

// ---------- emitters ----------

// editorExplorerEmitterNodes draws the level's particle emitters.
func editorExplorerEmitterNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Emitters (%d)##emitters", len(lvl.Emitters)), panelFocusEmitter) {
		return
	}
	defer imgui.TreePop()

	if len(lvl.Emitters) == 0 {
		editorTextMuted("(none)")
		return
	}
	del, dup := -1, -1
	for i := range lvl.Emitters {
		e := &lvl.Emitters[i]
		label := e.ID
		if label == "" {
			label = "(unnamed)"
		}
		// The effect it names, and any reason it is producing nothing. An
		// emitter with a broken document looks exactly like one that is simply
		// off, and the row is where that distinction is cheapest to make.
		switch {
		case e.Effect == "":
			label += "  (no effect)"
		case !e.Loaded():
			label += "  (" + e.Effect + ": failed)"
		default:
			label += "  [" + e.Effect + "]"
		}
		if !e.Enabled {
			label += "  (off)"
		} else if !e.Emitting {
			label += "  (not emitting)"
		}
		if !e.Attach.IsZero() {
			label += "  <" + e.Attach.Describe() + ">"
		}
		if editorExplorerLeaf(fmt.Sprintf("%s##em%d", label, i), i == Editor.ActiveEmitterIdx, panelFocusEmitter) {
			editorSelectEmitter(i)
		}
		editorExplorerMenu(fmt.Sprintf("emctx%d", i), func() {
			enabled := e.Enabled
			if imgui.MenuItemV("Enabled", "", enabled, true) {
				EditorPushUndo()
				e.Enabled = !enabled
			}
			emitting := e.Emitting
			if imgui.MenuItemV("Emitting", "", emitting, true) {
				EditorPushUndo()
				e.Emitting = !emitting
				e.SyncSystems()
			}
			if imgui.MenuItem("Restart") {
				e.Reset()
			}
			if imgui.MenuItem("Duplicate") {
				dup = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = i
			}
		})
	}
	if dup >= 0 {
		editorSelectEmitter(dup)
		duplicateActiveEmitter()
	}
	if del >= 0 {
		editorSelectEmitter(del)
		deleteActiveEmitter()
	}
}

// editorExplorerSoundNodes draws the level's placed sounds.
func editorExplorerSoundNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Sounds (%d)##sounds", len(lvl.Sounds)), panelFocusSound) {
		return
	}
	defer imgui.TreePop()

	if len(lvl.Sounds) == 0 {
		editorTextMuted("(none)")
		return
	}
	del, dup := -1, -1
	for i := range lvl.Sounds {
		sd := &lvl.Sounds[i]
		label := sd.ID
		if label == "" {
			label = "(unnamed)"
		}
		// The clip it names, and any reason it is silent. A sound with a
		// broken clip looks exactly like one that is simply off, and the row
		// is where that distinction is cheapest to make.
		switch {
		case sd.Clip == "":
			label += "  (no clip)"
		case !soundClipLoads(sd.Clip):
			label += "  (" + sd.Clip + ": failed)"
		default:
			label += "  [" + sd.Clip + "]"
		}
		if !sd.Enabled {
			label += "  (off)"
		} else if !sd.Playing {
			label += "  (stopped)"
		}
		if !sd.Attach.IsZero() {
			label += "  <" + sd.Attach.Describe() + ">"
		}
		if editorExplorerLeaf(fmt.Sprintf("%s##snd%d", label, i), i == Editor.ActiveSoundIdx, panelFocusSound) {
			editorSelectSound(i)
		}
		editorExplorerMenu(fmt.Sprintf("sndctx%d", i), func() {
			enabled := sd.Enabled
			if imgui.MenuItemV("Enabled", "", enabled, true) {
				EditorPushUndo()
				sd.Enabled = !enabled
			}
			playing := sd.Playing
			if imgui.MenuItemV("Playing", "", playing, true) {
				EditorPushUndo()
				sd.Playing = !playing
			}
			looping := sd.Loop
			if imgui.MenuItemV("Loop", "", looping, true) {
				EditorPushUndo()
				sd.Loop = !looping
			}
			if imgui.MenuItemV("Audition", "", false, sd.Clip != "") {
				editorSelectSound(i)
				auditionActiveSound()
			}
			if imgui.MenuItem("Duplicate") {
				dup = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = i
			}
		})
	}
	if dup >= 0 {
		editorSelectSound(dup)
		duplicateActiveSound()
	}
	if del >= 0 {
		editorSelectSound(del)
		deleteActiveSound()
	}
}

// soundClipLoads reports whether a clip is readable, for the row label. The
// clip cache remembers failures, so asking once a frame per sound costs a map
// lookup rather than a decode attempt.
func soundClipLoads(name string) bool {
	_, err := audio.ClipFor(name)
	return err == nil
}
