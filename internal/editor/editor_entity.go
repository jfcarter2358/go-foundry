package editor

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
	"foundry/internal/levelio"
	"foundry/internal/world"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- selection ----------

// activeEntity returns the selected entity, or nil.
func activeEntity() *world.Entity {
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
	editorClearObjectSelections()
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
func activeGizmoEntity() *world.Entity {
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
	world.ReloadEntityDefs()
	Editor.EntityDefNames = world.ScanEntityDefs()
	// Index 0 is the blank entry, so the last definition sits at len(), not
	// len()-1. A selection past that is one whose file has gone, and falls back
	// to blank rather than to some other author's type.
	if int(Editor.EntityDefIdx) > len(Editor.EntityDefNames) {
		Editor.EntityDefIdx = 0
	}
}

// editorSelectedDefName returns the definition name the catalog is pointing at,
// or "" for the blank entry.
//
// Index 0 is always blank rather than the first definition, so the catalog
// opens on "place something and edit it here" — the common case, and the only
// one that works on a checkout with no definitions in it at all. The
// definitions, when there are any, follow from index 1.
func editorSelectedDefName() string {
	i := int(Editor.EntityDefIdx) - 1
	if i < 0 || i >= len(Editor.EntityDefNames) {
		return ""
	}
	return Editor.EntityDefNames[i]
}

// ---------- placement ----------

// editorArmEntityPlacement puts the editor into placement mode. The next
// left-click in either view drops one entity of typeName, or a blank entity
// when typeName is empty.
func editorArmEntityPlacement(typeName string) {
	Editor.EntityPlaceType = typeName
	Editor.EntityPlacementMode = true
}

// placeEntityAt drops the armed entity at pos, selects it, and leaves placement
// mode. Reports whether one was placed.
//
// With no type armed it places a blank entity — a box with an ID and nothing
// else, authored from here on in the panel. With one, it stamps that
// definition.
//
// Placement mode is cleared even when the placement fails: leaving it armed
// after a definition that cannot load would arm every subsequent click on a
// mistake the user has no way to see.
func placeEntityAt(pos mgl32.Vec3) bool {
	typeName := Editor.EntityPlaceType
	Editor.EntityPlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return false
	}

	var def *world.EntityDef
	if typeName != "" {
		d, err := world.LoadEntityDef(typeName)
		if err != nil {
			editorSetStatus("Entity %s: %v", typeName, err)
			return false
		}
		def = d
	}

	EditorPushUndo()
	id := world.UniqueEntityID(lvl, entityPlacementID(typeName))
	e := world.NewBlankEntity(id)
	if def != nil {
		e = def.Instantiate(id)
		e.Type = typeName
	}
	e.Position = pos
	if err := e.Load(); err != nil {
		// A model that will not load costs the entity its mesh, not its
		// existence: the bounds, the attributes and the ID are all still
		// meaningful, and the editor can show a placeholder box for it.
		editorSetStatus("Entity %s: %v", id, err)
	}
	lvl.Entities = append(lvl.Entities, e)
	// The solid the type carries, if any (world/entity_brush.go). Placed after
	// the append so the entity is in the level its chunk is bound to.
	editorMaterializeEntityGeometry(lvl, &lvl.Entities[len(lvl.Entities)-1])
	editorSelectEntity(len(lvl.Entities) - 1)
	if def == nil {
		editorSetStatus("Placed entity %q — pick a model for it in the panel", id)
	} else {
		editorSetStatus("Placed %s %q", typeName, id)
	}
	return true
}

// entityPlacementID returns the base ID a newly placed entity gets: whatever
// the panel's ID field holds, or the type name, or plain "entity" for a blank
// one. UniqueEntityID makes it unique afterwards, so "barrel" placed three
// times gives barrel, barrel_2, barrel_3.
func entityPlacementID(typeName string) string {
	if Editor.NewEntityID != "" {
		return Editor.NewEntityID
	}
	if typeName == "" {
		return world.BlankEntityID
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
	e.ID = world.UniqueEntityID(lvl, src.ID)
	e.Tags = append([]string(nil), src.Tags...)
	e.Attributes = src.Attributes.Clone()
	// The source's Models hold live GPU handles; a struct copy would alias
	// them, and deleting either entity would then leave the other drawing from
	// a released VAO. Copy the authored fields and upload afresh.
	e.Models = nil
	for i := range src.Models {
		e.Models = append(e.Models, src.Models[i].TemplateCopy())
	}
	// The brushes are plain data, but the copy needs its own deep copy: a Brush
	// holds slices, and two entities sharing one backing array would mean
	// dragging a corner of the copy reshaped the original.
	e.Brushes = world.CloneEntityBrushes(src.Brushes)
	e.Position = src.Position.Add(mgl32.Vec3{entityDuplicateOffset, 0, entityDuplicateOffset})
	if err := e.Load(); err != nil {
		editorSetStatus("Entity %s: %v", e.ID, err)
	}

	lvl.Entities = append(lvl.Entities, e)
	editorMaterializeEntityGeometry(lvl, &lvl.Entities[len(lvl.Entities)-1])
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
	// The chunk this entity's brushes made goes with it. Without this it would
	// stay in the level with a FrameOwner naming an entity that is gone, which
	// BuildFrames reports as an error and refuses the whole level over.
	editorReleaseEntityGeometry(lvl, id)
	lvl.Entities = append(lvl.Entities[:idx:idx], lvl.Entities[idx+1:]...)
	editorClearEntitySelection()
	editorSetStatus("Deleted entity %q", id)
}

// ---------- model attachments ----------

// editorEntityAddModel hangs a new attachment on the entity, with no mesh yet.
//
// Attachments used to come only from a definition, which meant a thing had to
// have a .yaml file before it could have a shape. It does not: this is how a
// blank entity gets its first model, and how anything grows a second one.
func editorEntityAddModel(e *world.Entity) {
	if e == nil {
		return
	}
	EditorPushUndo()
	e.Models = append(e.Models, world.EntityModel{
		Name:    world.UniqueModelName(e, defaultModelName),
		Visible: true,
	})
}

// defaultModelName is what a new attachment is called before it has a mesh to
// take a name from — see editorEntitySetModelPath.
const defaultModelName = "model"

// editorEntityRemoveModel drops one attachment, releasing its GPU buffers.
func editorEntityRemoveModel(e *world.Entity, idx int) {
	if e == nil || idx < 0 || idx >= len(e.Models) {
		return
	}
	EditorPushUndo()
	e.Models[idx].Delete()
	e.Models = append(e.Models[:idx:idx], e.Models[idx+1:]...)
}

// editorEntitySetModelPath points an attachment at a different mesh and uploads
// it. prev is what the path was before the picker wrote to it, so the undo
// snapshot is taken of the state being left rather than the one arrived at.
//
// An attachment still carrying the placeholder name it was created with is
// renamed after its new mesh, so the common case — add one, pick a mesh — ends
// up with "barrel" rather than "model" without anyone typing it. A name that
// has been chosen is left alone: scripts address attachments by name, and
// renaming one out from under a script is not a thing to do quietly.
func editorEntitySetModelPath(e *world.Entity, m *world.EntityModel, prev, next string) {
	m.Path = prev
	EditorPushUndo()
	m.Path = next

	if next != "" && isPlaceholderModelName(m.Name) {
		m.Name = world.UniqueModelName(e, world.ModelNameForPath(next))
	}
	if err := m.Load(e.ID); err != nil {
		editorSetStatus("Entity %s: %v", e.ID, err)
		return
	}
	if next == "" {
		editorSetStatus("Cleared %s's model %q", e.ID, m.Name)
		return
	}
	editorSetStatus("%s: %s is now %s", e.ID, m.Name, next)
}

// isPlaceholderModelName reports whether a name is one editorEntityAddModel
// made up rather than one an author chose.
func isPlaceholderModelName(name string) bool {
	return name == "" || name == defaultModelName ||
		strings.HasPrefix(name, defaultModelName+"_")
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
	def, err := world.LoadEntityDef(e.Type)
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
	editorSyncEntityGeometry(lvl, e)
	if err := world.RebuildIndexes(lvl); err != nil {
		editorSetStatus("Entity %s: %v", e.ID, err)
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
			Label:   "Add entity here",
			Do:      func() { editor3DAddEntityAt("", pos) },
			Tooltip: "A blank entity with no model, where the cursor met\nthe world. Give it one in the Entities panel.",
		}
	}
	return ctxMenuItem{
		Label:   "Add " + name + " here",
		Do:      func() { editor3DAddEntityAt(name, pos) },
		Tooltip: "Places the type selected in the Entities panel,\nwhere the cursor met the world.",
	}
}

// ---------- carried geometry ----------

// editorMaterializeEntityGeometry builds the level chunk an entity's brushes
// describe, and refreshes the indexes that chunk belongs to.
//
// Split from editorSyncEntityGeometry (editor_entity_editor_io.go) so the
// common case — an entity that has just been placed and has nothing to drop
// first — does not walk the chunk list twice.
func editorMaterializeEntityGeometry(lvl *world.Level, e *world.Entity) {
	if lvl == nil || e == nil || !e.HasBrushes() {
		return
	}
	editorSyncEntityGeometry(lvl, e)
	// A materialised chunk is geometry the player can stand on and a frame the
	// entity carries, and neither exists until the indexes are rebuilt.
	if err := world.RebuildIndexes(lvl); err != nil {
		editorSetStatus("Entity %s: %v", e.ID, err)
	}
}

// editorReleaseEntityGeometry drops every chunk materialised from the named
// entity, and rebuilds the indexes without them.
func editorReleaseEntityGeometry(lvl *world.Level, id string) {
	if lvl == nil || levelio.DropEntityGeometry(lvl, id) == 0 {
		return
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		editorSetStatus("%v", err)
	}
}

// editorRenameEntity changes an entity's ID, taking its carried solid with it.
//
// The materialised chunk's ID, its FrameOwner and every brush ID inside it are
// all built from the entity's ID, so a rename that touched only the entity
// would leave a chunk bound to a frame owner that no longer exists — which
// BuildFrames refuses the level over. Rebuilding is cheaper than rewriting
// those fields in place, and it is the same path every other change to the
// geometry takes.
func editorRenameEntity(lvl *world.Level, e *world.Entity, id string) {
	if lvl == nil || e == nil || id == e.ID {
		return
	}
	EditorPushUndo()
	editorReleaseEntityGeometry(lvl, e.ID)
	e.ID = id
	editorMaterializeEntityGeometry(lvl, e)
}
