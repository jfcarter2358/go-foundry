package main

// editor_entity.go — authoring entities: selecting them, placing them,
// duplicating and deleting them.
//
// Kept apart from entity.go and entity_def.go so the entity model stays free of
// imgui and GL and can be unit-tested headless — the same split spawn_point.go
// and editor_spawn_point.go have. The properties panel is further out still, in
// editor_entity_panel.go, because it is by far the largest part and none of the
// rest depends on it.
//
// Selection is an *index* into level.Entities rather than a pointer. Undo
// replaces the whole level (restoreLevelSnapshot), so a pointer taken before an
// undo would address an entity that no longer exists; an index is merely stale,
// and every accessor here bounds-checks it.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// ---------- selection ----------

// activeEntity returns the selected entity, or nil.
func activeEntity() *Entity {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveEntityIdx < 0 || Editor.ActiveEntityIdx >= len(lvl.Entities) {
		return nil
	}
	return &lvl.Entities[Editor.ActiveEntityIdx]
}

// editorSelectEntity makes the entity at idx the selection, which is what gives
// it the gizmos and opens its properties section.
func editorSelectEntity(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Entities) {
		return
	}
	Editor.ActiveEntityIdx = idx
	Editor.GizmoKind = gizmoKindEntity
	Editor.PanelFocus = panelFocusEntity
}

// editorClearEntitySelection drops the entity from the selection, taking the
// gizmo with it so it cannot keep dragging something no longer selected.
func editorClearEntitySelection() {
	Editor.ActiveEntityIdx = -1
	if Editor.GizmoKind == gizmoKindEntity {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoEntity returns the entity the gizmo is attached to, or nil. The
// gizmo kind is checked as well as the index so a stale selection left behind
// by another tool cannot silently be dragged.
func activeGizmoEntity() *Entity {
	if Editor.GizmoKind != gizmoKindEntity {
		return nil
	}
	return activeEntity()
}

// ---------- the definition catalog ----------

// editorRefreshEntityDefs rescans resources/entities/ and drops the parsed-
// definition cache, so a definition edited outside the running engine is picked
// up. Called when the entity section is first opened and by its Reload button.
//
// Rescanned on demand rather than watched: definitions change at authoring
// speed, and a file watcher would be a thread and a platform dependency to save
// one button press.
func editorRefreshEntityDefs() {
	ReloadEntityDefs()
	Editor.EntityDefNames = ScanEntityDefs()
	if int(Editor.EntityDefIdx) >= len(Editor.EntityDefNames) {
		Editor.EntityDefIdx = 0
	}
}

// editorSelectedDefName returns the definition name the catalog is pointing at,
// or "" when there are none.
func editorSelectedDefName() string {
	if len(Editor.EntityDefNames) == 0 {
		return ""
	}
	i := int(Editor.EntityDefIdx)
	if i < 0 || i >= len(Editor.EntityDefNames) {
		return ""
	}
	return Editor.EntityDefNames[i]
}

// ---------- placement ----------

// editorArmEntityPlacement puts the editor into placement mode for a type. The
// next left-click in either view drops one.
func editorArmEntityPlacement(typeName string) {
	if typeName == "" {
		editorSetStatus("No entity definitions found in %s", entitiesDirName)
		return
	}
	Editor.EntityPlaceType = typeName
	Editor.EntityPlacementMode = true
}

// placeEntityAt instantiates the armed definition at pos, selects it, and
// leaves placement mode. Reports whether one was placed.
//
// Placement mode is cleared even when the placement fails: leaving it armed
// after a definition that cannot load would arm every subsequent click on a
// mistake the user has no way to see.
func placeEntityAt(pos mgl32.Vec3) bool {
	typeName := Editor.EntityPlaceType
	Editor.EntityPlacementMode = false
	lvl := Editor.level
	if lvl == nil || typeName == "" {
		return false
	}

	def, err := LoadEntityDef(typeName)
	if err != nil {
		editorSetStatus("Entity %s: %v", typeName, err)
		return false
	}

	EditorPushUndo()
	id := uniqueEntityID(lvl, entityPlacementID(typeName))
	e := def.Instantiate(id)
	e.Position = pos
	if err := e.Load(); err != nil {
		// A model that will not load costs the entity its mesh, not its
		// existence: the bounds, the attributes and the ID are all still
		// meaningful, and the editor can show a placeholder box for it.
		editorSetStatus("Entity %s: %v", id, err)
	}
	lvl.Entities = append(lvl.Entities, e)
	editorSelectEntity(len(lvl.Entities) - 1)
	editorSetStatus("Placed %s %q", typeName, id)
	return true
}

// entityPlacementID returns the base ID a newly placed entity gets: whatever
// the panel's ID field holds, or the type name. uniqueEntityID makes it unique
// afterwards, so "barrel" placed three times gives barrel, barrel_2, barrel_3.
func entityPlacementID(typeName string) string {
	if Editor.NewEntityID != "" {
		return Editor.NewEntityID
	}
	return typeName
}

// ---------- duplicate / delete ----------

// duplicateActiveEntity copies the selected entity beside itself and selects
// the copy. The copy is offset rather than coincident: two entities in exactly
// the same place cannot be told apart, let alone clicked apart.
func duplicateActiveEntity() {
	lvl := Editor.level
	src := activeEntity()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()

	e := *src
	e.ID = uniqueEntityID(lvl, src.ID)
	e.Tags = append([]string(nil), src.Tags...)
	e.Attributes = src.Attributes.Clone()
	// The source's Models hold live GPU handles; a struct copy would alias
	// them, and deleting either entity would then leave the other drawing from
	// a released VAO. Copy the authored fields and upload afresh.
	e.Models = nil
	for i := range src.Models {
		e.Models = append(e.Models, src.Models[i].templateCopy())
	}
	e.Position = src.Position.Add(mgl32.Vec3{entityDuplicateOffset, 0, entityDuplicateOffset})
	if err := e.Load(); err != nil {
		editorSetStatus("Entity %s: %v", e.ID, err)
	}

	lvl.Entities = append(lvl.Entities, e)
	editorSelectEntity(len(lvl.Entities) - 1)
	editorSetStatus("Duplicated entity %q", e.ID)
}

// entityDuplicateOffset is how far a duplicate lands from its original, in
// world units — far enough to be clickable at a normal working zoom.
const entityDuplicateOffset = float32(1.0)

// deleteActiveEntity removes the selected entity and releases its GPU buffers.
func deleteActiveEntity() {
	lvl := Editor.level
	idx := Editor.ActiveEntityIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Entities) {
		return
	}
	EditorPushUndo()
	id := lvl.Entities[idx].ID
	lvl.Entities[idx].Delete()
	lvl.Entities = append(lvl.Entities[:idx:idx], lvl.Entities[idx+1:]...)
	editorClearEntitySelection()
	editorSetStatus("Deleted entity %q", id)
}

// ---------- reload ----------

// reloadActiveEntityModels re-reads every attachment on the selected entity
// from disk. What the panel's model controls call after changing a texture or a
// path, and what picks up a .fmdl re-saved by the model editor.
func reloadActiveEntityModels() {
	e := activeEntity()
	if e == nil {
		return
	}
	if err := e.Load(); err != nil {
		editorSetStatus("Entity %s: %v", e.ID, err)
		return
	}
	editorSetStatus("Reloaded models for %q", e.ID)
}

// reapplyEntityDefinition re-stamps the selected entity from its definition,
// keeping only the placement — position, rotation, scale and velocities.
//
// This is the escape hatch for a definition that has changed since the entity
// was placed: instance overrides are what keep a level from tracking its
// definition, and this throws them away deliberately rather than by surprise.
func reapplyEntityDefinition() {
	lvl := Editor.level
	e := activeEntity()
	if lvl == nil || e == nil || e.Type == "" {
		return
	}
	def, err := LoadEntityDef(e.Type)
	if err != nil {
		editorSetStatus("Entity type %s: %v", e.Type, err)
		return
	}
	EditorPushUndo()

	fresh := def.Instantiate(e.ID)
	fresh.Position = e.Position
	fresh.Rotation = e.Rotation
	fresh.Scale = e.Scale
	fresh.Velocity = e.Velocity
	fresh.AngularVelocity = e.AngularVelocity

	e.Delete()
	*e = fresh
	if err := e.Load(); err != nil {
		editorSetStatus("Entity %s: %v", e.ID, err)
		return
	}
	editorSetStatus("Reset %q to definition %s", e.ID, def.Name)
}

// ---------- menu helpers ----------

// editor3DMoveEntityTo puts the selected entity at pos — the context menu's
// "move here", which is the one placement action that acts on something already
// in the level rather than adding to it.
func editor3DMoveEntityTo(pos mgl32.Vec3) {
	e := activeEntity()
	if e == nil {
		return
	}
	EditorPushUndo()
	e.Position = pos
}

// editorAddEntityMenuItem builds the context menus' "add an entity here" row.
//
// It names the type it would place rather than opening a submenu of every
// definition: the catalog in the panel is where a type is chosen, and a menu
// that re-offered the whole list would be a second place to choose one that
// could disagree with the first.
func editorAddEntityMenuItem(pos mgl32.Vec3) ctxMenuItem {
	if Editor.EntityDefNames == nil {
		editorRefreshEntityDefs()
	}
	name := editorSelectedDefName()
	if name == "" {
		return ctxMenuItem{
			Label:    "Add entity here",
			Disabled: true,
			Tooltip:  "No definitions found in " + entitiesDirName + "/.",
		}
	}
	return ctxMenuItem{
		Label:   "Add " + name + " here",
		Do:      func() { editor3DAddEntityAt(name, pos) },
		Tooltip: "Places the type selected in the Entities panel,\nwhere the cursor met the world.",
	}
}
