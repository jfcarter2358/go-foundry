package main

// editor_model_import.go — placing model files into a level as entities.
//
// The model editor writes .fmdl documents into <exe>/models, and Entity.Load
// already draws either those or a plain .obj. What was missing was the step
// between: entities could only be spawned from the hand-written definitions in
// resources/entities, so a model authored in the editor stayed unreachable
// until someone wrote a matching YAML for it by hand.
//
// This is the level side of that pipeline — scan the models directory, pick a
// file, click to place it — and it deliberately derives what an EntityDef would
// otherwise state by hand: the bounding box comes from the mesh itself, so a
// model that is remodelled to a different size does not silently keep the
// bounds someone typed for the old one.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// modelImportFallbackTexture is what a placed model starts with for faces that
// name no texture of their own: modelSubmeshes maps those to "default", and
// Entity.Load resolves "default" through the entity's own TexturePath.
const modelImportFallbackTexture = "default"

// modelImportMinHalfExtent keeps a flat model — a plane, a billboard — from
// getting a zero half-extent on some axis. Entity.WorldAABB reads a zero extent
// as "no bounds authored" and refuses to cull the entity at all, so a floor
// tile would otherwise be drawn from everywhere on the map.
const modelImportMinHalfExtent = float32(0.05)

// ScanLevelModelFiles returns the file names, within modelsDir(), of every
// model the level editor can place. Both formats are listed together and keep
// their extension: the editor exports .obj beside the .fmdl it was made from,
// and those two are different files that can drift apart, so which one is being
// placed has to stay visible.
func ScanLevelModelFiles() []string {
	files := append(scanFilesByExt(modelsDir(), modelExt), scanFilesByExt(modelsDir(), objExt)...)
	sort.Strings(files)
	return files
}

// modelEntityBaseID derives an entity ID stem from a model file name, e.g.
// "rock.fmdl" -> "rock".
func modelEntityBaseID(fileName string) string {
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if base == "" {
		return "model"
	}
	return base
}

// uniqueEntityID returns base, or the first base_N free of collisions. Entity
// IDs are how the rest of the engine names an entity — the level YAML, the
// editor's own selection readout — so two rocks placed from the same model must
// not end up sharing one.
func uniqueEntityID(lvl *Level, base string) string {
	taken := make(map[string]bool, len(lvl.Entities))
	for i := range lvl.Entities {
		taken[lvl.Entities[i].ID] = true
	}
	if !taken[base] {
		return base
	}
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !taken[id] {
			return id
		}
	}
}

// modelEntityHalfExtents derives an entity's bounding half-extents from a
// model's mesh.
//
// Entity bounds are half-extents centred on the entity's position, while a
// model is authored around wherever its author put the origin — the centre for
// a primitive-derived shape, the base for anything meant to stand on a floor.
// So the extent taken per axis is how far the mesh reaches from that origin in
// either direction: that box always contains the mesh, at the cost of being
// twice as tall as it needs to be for a base-origin model. A box that is too
// big only costs some culling and a loose collision volume; one that is too
// small pops the model out of view when the camera turns.
func modelEntityHalfExtents(doc *ModelDoc) (hx, hy, hz float32) {
	min, max := doc.Bounds()
	var h [3]float32
	for i := 0; i < 3; i++ {
		h[i] = abs32(min[i])
		if e := abs32(max[i]); e > h[i] {
			h[i] = e
		}
		if h[i] < modelImportMinHalfExtent {
			h[i] = modelImportMinHalfExtent
		}
	}
	return h[0], h[1], h[2]
}

// newModelEntity builds the entity a placed model becomes. The GPU upload stays
// the caller's job (Entity.Load), so everything decided here — ID, bounds,
// fallback texture — is settled without a GL context.
func newModelEntity(id, modelPath, texture string, pos mgl32.Vec3, doc *ModelDoc) Entity {
	hx, hy, hz := modelEntityHalfExtents(doc)
	if texture == "" {
		texture = modelImportFallbackTexture
	}
	return Entity{
		ID:           id,
		Position:     pos,
		Scale:        mgl32.Vec3{1, 1, 1},
		ModelPath:    modelPath,
		TexturePath:  texture,
		BoundingKind: BoundingAABB,
		BoundHalfX:   hx,
		BoundHalfY:   hy,
		BoundHalfZ:   hz,
		Alpha:        1,
		Visible:      true,
	}
}

// editorRefreshModelCatalog re-reads the models directory. The model editor can
// save a new document while the level editor is open, so the list has to be
// refreshable without restarting.
func editorRefreshModelCatalog() {
	Editor.ModelFiles = ScanLevelModelFiles()
	Editor.ModelFilesScanned = true
	if Editor.SelectedModelIdx >= len(Editor.ModelFiles) {
		Editor.SelectedModelIdx = len(Editor.ModelFiles) - 1
	}
	if Editor.ModelPlacementTexture == "" {
		Editor.ModelPlacementTexture = modelImportFallbackTexture
	}
}

// editorModelCatalogSection draws the model catalog and its place toggle. Like
// the entity catalog, the click that actually spawns is handled by the active
// sub-mode (Editor3DHandleClick / editor2DHandlePlacement).
func editorModelCatalogSection() {
	if !imgui.CollapsingHeader("Model Catalog") {
		return
	}
	// Scanned once on first open rather than every frame: this is a directory
	// read, and the Refresh button covers the case of a model saved since.
	if !Editor.ModelFilesScanned {
		editorRefreshModelCatalog()
	}

	if len(Editor.ModelFiles) == 0 {
		imgui.Text(fmt.Sprintf("No %s or %s files in %s", modelExt, objExt, modelsDir()))
	}
	for i, name := range Editor.ModelFiles {
		if imgui.SelectableV(name, i == Editor.SelectedModelIdx, 0, imgui.Vec2{}) {
			Editor.SelectedModelIdx = i
		}
	}

	// The fallback only reaches faces that named no texture in the model, so a
	// fully textured .fmdl ignores it entirely.
	textureCombo("Fallback Texture##modelimport", &Editor.ModelPlacementTexture, false)

	if imgui.Button("Place Model##modelimport") && Editor.SelectedModelIdx >= 0 &&
		Editor.SelectedModelIdx < len(Editor.ModelFiles) {
		Editor.ModelPlacementMode = true
		Editor.PlacementMode = false
		Editor.PrimPlacementMode = false
	}
	imgui.SameLine()
	if imgui.Button("Refresh##modelimport") {
		editorRefreshModelCatalog()
	}
	if Editor.ModelPlacementMode {
		imgui.SameLine()
		imgui.Text("Click to place...")
	}
}

// placeSelectedModelAt spawns the catalog-selected model at pos as an entity
// and makes it the selection. Placement mode is cleared either way, so a model
// that fails to load doesn't leave the editor armed.
func placeSelectedModelAt(pos mgl32.Vec3) {
	Editor.ModelPlacementMode = false
	lvl := Editor.level
	if lvl == nil || Editor.SelectedModelIdx < 0 || Editor.SelectedModelIdx >= len(Editor.ModelFiles) {
		return
	}
	name := Editor.ModelFiles[Editor.SelectedModelIdx]
	path := filepath.Join(modelsDir(), name)

	// Read the document before touching the level: the bounds come from it, and
	// a half-placed entity is worse than none.
	doc, err := loadModelDocument(path)
	if err != nil {
		editorSetStatus("Model %q: %v", name, err)
		return
	}

	entity := newModelEntity(uniqueEntityID(lvl, modelEntityBaseID(name)), path,
		Editor.ModelPlacementTexture, pos, doc)
	if err := entity.Load(); err != nil {
		editorSetStatus("Model %q: %v", name, err)
		return
	}

	EditorPushUndo()
	lvl.Entities = append(lvl.Entities, entity)
	selectPlacedEntity(lvl)
	editorSetStatus("Placed %s as %q", name, entity.ID)
}

// selectPlacedEntity makes the last-appended entity the editor's selection and
// arms the translate gizmo on it, so a freshly placed object can be dragged
// into position without a second click to select it first.
func selectPlacedEntity(lvl *Level) {
	Editor.ActiveEntityIdx = len(lvl.Entities) - 1
	Editor.ActivePrimitiveIdx = -1
	Editor.GizmoKind = gizmoKindEntity
}

// fitEntityBoundsToModel re-derives an entity's bounding box from its model
// file. Bounds are what the player collides with and what culling tests, and a
// model that has been remodelled since it was placed — or one that arrived with
// hand-written bounds from an entity definition — keeps the old box until
// something recomputes it.
func fitEntityBoundsToModel(e *Entity) error {
	if e.ModelPath == "" {
		return fmt.Errorf("entity has no model")
	}
	doc, err := loadModelDocument(e.ModelPath)
	if err != nil {
		return err
	}
	e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = modelEntityHalfExtents(doc)
	return nil
}

// editorSetStatus sets the transient status line the editor draws, stamping it
// so it expires on its own.
func editorSetStatus(format string, args ...any) {
	Editor.StatusMsg = fmt.Sprintf(format, args...)
	Editor.StatusTime = sdl.GetTicks()
}
