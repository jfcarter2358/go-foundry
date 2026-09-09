package editor

// editor_entity_editor_io.go — new/open/save for the entity editor, and the
// structural edits the outline makes.
//
// Saving is the interesting half. A definition is not a document that only this
// window cares about: instances of it are standing in the level open in the
// main window right now, and the level editor's whole story about types is that
// changing one changes all of them. So a save is three things — write the file,
// drop the parsed-definition cache, and re-stamp every placed instance — and
// doing only the first would leave the author looking at the old barrel while
// the file on disk described a new one.
//
// Re-stamping deliberately discards instance overrides, exactly as the level
// editor's "Reset To Definition" does, and keeps only the placement. That is
// the same trade that button documents: an override is what stops a level
// tracking its type, and the point of this window is to make the type the thing
// worth tracking.

import (
	"fmt"
	"foundry/internal/levelio"
	"foundry/internal/uiwin"
	"foundry/internal/world"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// entityDefDirty says the document has unsaved changes — the ` *` in the
// toolbar, and the reason the close prompt exists.
var entityDefDirty bool

// entityMarkDirty records that the document has changed. Every mutating widget
// calls it, alongside pushing undo.
func entityMarkDirty() { entityDefDirty = true }

// entityDefName is the name the definition would be saved under: the id in the
// panel, which is also what a level writes as an entity's `type:`.
func entityDefName() string {
	if entityDef == nil {
		return ""
	}
	return entityDef.Name
}

// entityNewDoc starts a fresh type, discarding nothing that was saved.
func entityNewDoc() {
	entityGizmoMouseUp() // a drag into a document that is about to be replaced
	entityDef = entityNewDefaultDef(entityUniqueDefName("entity"))
	entityDefSaved = ""
	entityDefDirty = false
	entitySel = entityEditorSelection{}
	EntityDefClearUndoHistory()
	entityPreviewInvalidate()
	// The brushes came with it, so the live ones have to be rebuilt from the
	// new document. See entityGeomDirty.
	entityInvalidateGeometry()
	entityStatus = "New entity type"
}

// entityUniqueDefName returns base, or base with a numeric suffix no file under
// entities/ is using — so a second "New" does not silently offer to overwrite
// the type the first one saved.
func entityUniqueDefName(base string) string {
	existing := map[string]bool{}
	for _, n := range world.ScanEntityDefs() {
		existing[n] = true
	}
	if !existing[base] {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if !existing[name] {
			return name
		}
	}
}

// entityLoadDef reads a definition and makes it the document.
//
// A *clone* of what the registry hands back, because that value is shared with
// every entity instantiated from it and its doc comment forbids mutating it.
// Editing the registry's copy in place would silently change every placed
// entity halfway through an edit that had not been saved.
func entityLoadDef(name string) error {
	def, err := world.LoadEntityDef(name)
	if err != nil {
		return err
	}
	entityGizmoMouseUp() // as entityNewDoc: no drag survives a document swap
	entityDef = def.Clone()
	entityDef.Name = name
	entityDefSaved = name
	entityDefDirty = false
	entitySel = entityEditorSelection{}
	EntityDefClearUndoHistory()
	entityPreviewInvalidate()
	// The brushes came with it, so the live ones have to be rebuilt from the
	// new document. See entityGeomDirty.
	entityInvalidateGeometry()
	entityStatus = "Opened " + name
	return nil
}

// entitySaveCurrent writes the document under the name in the id field.
func entitySaveCurrent() {
	if entityDef == nil {
		return
	}
	if entityDef.Name == "" {
		entitySaveAsName = ""
		entityShowSaveAsDialog = true
		return
	}
	entitySaveDefAs(entityDef.Name)
}

// entitySaveDefAs writes the document as name, and pushes the change out to
// everything already placed from it.
func entitySaveDefAs(name string) {
	if entityDef == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		entityStatus = "A type needs a name"
		return
	}
	prev := entityDef.Name
	entityDef.Name = name
	if err := world.SaveEntityDef(entityDef); err != nil {
		entityDef.Name = prev
		entityStatus = err.Error()
		return
	}
	entityDefSaved = name
	entityDefDirty = false

	// The catalog in the level editor is a cached scan, and a type that was
	// just created is not in it.
	editorRefreshEntityDefs()
	// Against the *main* window's GL context. Re-stamping uploads meshes and
	// chunk geometry for entities the main window draws, and a VAO minted here
	// is a name that window cannot bind — see withMainGLFrom.
	var n int
	withMainGLFrom(entityEditorWindow, func() { n = ReloadEntitiesOfType(name) })
	entityStatus = fmt.Sprintf("Saved %s%s", name+entityDefFileSuffix, reloadedSuffix(n))
}

// entityDefFileSuffix is the extension a definition is written with, for the
// status line — world owns the constant, and this only spells it.
const entityDefFileSuffix = ".yaml"

// reloadedSuffix renders the "and n instances" tail, or nothing when there were
// none. A count of zero is worth not saying: most saves are of a type no level
// has placed yet.
func reloadedSuffix(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return " (1 instance updated)"
	default:
		return fmt.Sprintf(" (%d instances updated)", n)
	}
}

// ReloadEntitiesOfType re-stamps every instance of a type in the level open in
// the main window, keeping only where each one is. Reports how many changed.
//
// The counterpart of ReloadEmittersUsing for particle effects, and the reason
// this window is worth having beside the level rather than instead of it: save
// the type and the level in the other window is already showing it.
func ReloadEntitiesOfType(typeName string) int {
	lvl := Editor.level
	if lvl == nil || typeName == "" {
		return 0
	}
	def, err := world.LoadEntityDef(typeName)
	if err != nil {
		editorSetStatus("Entity type %s: %v", typeName, err)
		return 0
	}
	n := 0
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.Type != typeName {
			continue
		}
		fresh := def.Instantiate(e.ID)
		fresh.Type = typeName
		fresh.Position, fresh.Rotation, fresh.Scale = e.Position, e.Rotation, e.Scale
		fresh.Velocity, fresh.AngularVelocity = e.Velocity, e.AngularVelocity
		fresh.Frame, fresh.Seat = e.Frame, e.Seat

		e.Delete()
		*e = fresh
		if err := e.Load(); err != nil {
			editorSetStatus("Entity %s: %v", e.ID, err)
		}
		editorSyncEntityGeometry(lvl, e)
		n++
	}
	if n > 0 {
		// Chunks came and went, so the spatial hash and the frame set are both
		// stale — RebuildIndexes is what puts a materialised solid into the
		// world the player can walk on.
		if err := world.RebuildIndexes(lvl); err != nil {
			editorSetStatus("Entity type %s: %v", typeName, err)
		}
	}
	return n
}

// editorSyncEntityGeometry rebuilds the level chunk materialised from one
// entity's brushes: the old one released, the current one built.
//
// Called wherever an entity's identity or its geometry changes — placed,
// duplicated, renamed, deleted, re-stamped, or edited in the entity editor.
// Doing it in one place is what keeps "the level's chunk list agrees with its
// entities" from being six separate things to remember.
func editorSyncEntityGeometry(lvl *world.Level, e *world.Entity) {
	if lvl == nil || e == nil {
		return
	}
	levelio.DropEntityGeometry(lvl, e.ID)
	tex := map[string]bool{}
	if err := levelio.MaterializeEntityGeometry(lvl, e, tex); err != nil {
		editorSetStatus("%v", err)
	}
	if err := levelio.EnsureChunkTextures(tex); err != nil {
		editorSetStatus("%v", err)
	}
}

// ---------- structural edits ----------

// entityAddModel hangs a new attachment on the type, with no mesh yet — the
// same "add one, then pick a mesh" order the level editor's entity panel uses.
func entityAddModel() {
	if entityDef == nil {
		return
	}
	EntityDefPushUndo()
	entityDef.Models = append(entityDef.Models, world.EntityModel{
		Name:    world.UniqueModelName(entityDefAsEntity(), defaultModelName),
		Visible: true,
	})
	entitySel = entityEditorSelection{Kind: entitySelModel, Idx: len(entityDef.Models) - 1}
	entityMarkDirty()
	entityPreviewInvalidate()
}

// entityDefAsEntity wraps the document's model list in an Entity so the
// world-side uniqueness helpers, which take one, can be used unchanged. It is a
// view rather than a copy: the slice header is shared, which is all
// UniqueModelName reads.
func entityDefAsEntity() *world.Entity {
	if entityDef == nil {
		return nil
	}
	return &world.Entity{Models: entityDef.Models}
}

// entityRemoveModel drops one attachment.
func entityRemoveModel(idx int) {
	if entityDef == nil || idx < 0 || idx >= len(entityDef.Models) {
		return
	}
	EntityDefPushUndo()
	entityDef.Models = append(entityDef.Models[:idx:idx], entityDef.Models[idx+1:]...)
	entitySel = entityEditorSelection{}
	entityMarkDirty()
	entityPreviewInvalidate()
}

// entityAddSeat adds a seat to the vehicle block, or nothing when the type is
// not a vehicle.
func entityAddSeat() {
	if entityDef == nil || entityDef.Vehicle == nil {
		return
	}
	EntityDefPushUndo()
	v := entityDef.Vehicle
	name := uniqueSeatName(v, "seat")
	v.Seats = append(v.Seats, world.VehicleSeat{
		Name:   name,
		Driver: len(v.Seats) == 0, // the first seat drives; a vehicle with none is furniture
		Eye:    0.7,
		Exit:   mgl32.Vec3{1.5, 0, 0},
	})
	entitySel = entityEditorSelection{Kind: entitySelSeat, Idx: len(v.Seats) - 1}
	entityMarkDirty()
}

// entityRemoveSeat drops one seat.
func entityRemoveSeat(idx int) {
	if entityDef == nil || entityDef.Vehicle == nil {
		return
	}
	v := entityDef.Vehicle
	if idx < 0 || idx >= len(v.Seats) {
		return
	}
	EntityDefPushUndo()
	v.Seats = append(v.Seats[:idx:idx], v.Seats[idx+1:]...)
	entitySel = entityEditorSelection{}
	entityMarkDirty()
}

// entityDeleteSelection removes whatever the outline has selected — the Delete
// key, so the list can be worked with one hand.
func entityDeleteSelection() {
	switch entitySel.Kind {
	case entitySelModel:
		entityRemoveModel(entitySel.Idx)
	case entitySelBrush:
		entityRemoveBrush(entitySel.Idx)
	case entitySelSeat:
		entityRemoveSeat(entitySel.Idx)
	}
}

// OpenEntityEditorFrom opens the editor on a new type seeded from a placed
// entity — the level editor's "Make Type" beside a blank one.
//
// This is the promotion the entities README describes and the editor could not
// previously do: a blank entity is the quick path, and a definition is what it
// becomes once a second level wants the same thing. What carries over is
// everything about *what it is* — models, brushes, bounds, collision, tags,
// attributes, script and vehicle. What does not is where it is: a definition
// has no position, because a definition is not anywhere.
//
// The placed entity is left exactly as it is. Turning it into an instance of
// the new type would be a second decision — it would discard whatever it
// overrides — and it is one click away in the panel once the type is saved.
func OpenEntityEditorFrom(e *world.Entity) {
	if e == nil {
		return
	}
	def := &world.EntityDef{
		Name:         entityUniqueDefName(entityTypeNameFor(e)),
		Tags:         append([]string(nil), e.Tags...),
		Script:       e.Script,
		BoundingKind: e.BoundingKind,
		BoundHalfX:   e.BoundHalfX,
		BoundHalfY:   e.BoundHalfY,
		BoundHalfZ:   e.BoundHalfZ,
		BoundOffset:  e.BoundOffset,
		Visible:      e.Visible,
		Collision:    e.Collision,
		Attributes:   e.Attributes.Clone(),
		Params:       e.ScriptParams.Clone(),
		Brushes:      world.CloneEntityBrushes(e.Brushes),
		Vehicle:      world.CloneVehicleSpec(e.Vehicle),
	}
	for i := range e.Models {
		def.Models = append(def.Models, e.Models[i].TemplateCopy())
	}

	uiwin.RequestWindowOpen(func() {
		entityDef = def
		// Never saved: the name is a suggestion, and Save is what commits it.
		// Marked dirty from the start for the same reason — there is nothing on
		// disk that matches what is on screen.
		entityDefSaved = ""
		entityDefDirty = true
		entitySel = entityEditorSelection{}
		EntityDefClearUndoHistory()
		entityPreviewInvalidate()
		// The brushes came with it, so the live ones have to be rebuilt from the
		// new document. See entityGeomDirty.
		entityInvalidateGeometry()
		entityStatus = "New type from " + e.ID + " — Save to commit it"
		OpenEntityEditorWindow()
	})
}

// entityTypeNameFor is the type name a placed entity suggests: its own ID with
// any numeric suffix the editor appended taken back off, so the third barrel
// placed proposes "barrel" rather than "barrel_3".
func entityTypeNameFor(e *world.Entity) string {
	name := e.ID
	if i := strings.LastIndex(name, "_"); i > 0 {
		if _, err := strconv.Atoi(name[i+1:]); err == nil {
			name = name[:i]
		}
	}
	if name == "" || name == world.BlankEntityID {
		return "entity"
	}
	return name
}
