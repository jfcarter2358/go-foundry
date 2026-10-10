package editor

// editor_entity_editor_test.go — the entity editor's logic, exercised without a
// window.
//
// Everything here is deliberately reachable with no GL context and no imgui
// frame: the keyboard map is a pure function, the undo stack is plain data, and
// the naming rules are string handling. What is left over — the panel and the
// preview upload — is the part a test could only assert by drawing, and is kept
// out of these functions for exactly that reason.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// entityTestDoc installs a document and clears the editor's global state around
// it, so tests do not inherit each other's selection or undo stack.
func entityTestDoc(t *testing.T) *world.EntityDef {
	t.Helper()
	prevDef, prevSel, prevDirty, prevSaved := entityDef, entitySel, entityDefDirty, entityDefSaved
	t.Cleanup(func() {
		entityDef, entitySel, entityDefDirty, entityDefSaved = prevDef, prevSel, prevDirty, prevSaved
		EntityDefClearUndoHistory()
	})

	entityDef = entityNewDefaultDef("test")
	entitySel = entityEditorSelection{}
	entityDefDirty = false
	entityDefSaved = ""
	EntityDefClearUndoHistory()
	return entityDef
}

func TestEntityShortcutFor(t *testing.T) {
	cases := []struct {
		sc     sdl.Scancode
		ctrl   bool
		shift  bool
		want   entityShortcut
		reason string
	}{
		{sdl.SCANCODE_S, true, false, entityShortcutSave, "Ctrl+S saves"},
		{sdl.SCANCODE_S, true, true, entityShortcutSaveAs, "Ctrl+Shift+S saves as"},
		{sdl.SCANCODE_S, false, false, entityShortcutNone, "bare S is a fly key, not a shortcut"},
		{sdl.SCANCODE_Z, true, false, entityShortcutUndo, "Ctrl+Z undoes"},
		{sdl.SCANCODE_Z, true, true, entityShortcutRedo, "Ctrl+Shift+Z redoes"},
		{sdl.SCANCODE_Y, true, false, entityShortcutRedo, "Ctrl+Y redoes"},
		{sdl.SCANCODE_F, false, false, entityShortcutFrameCamera, "F frames"},
		{sdl.SCANCODE_V, false, false, entityShortcutToggleFly, "V flies"},
		{sdl.SCANCODE_DELETE, false, false, entityShortcutDeleteSelection, "Delete removes the selection"},
		{sdl.SCANCODE_X, false, false, entityShortcutDeleteSelection, "X removes it too"},
		{sdl.SCANCODE_N, true, false, entityShortcutNew, "Ctrl+N is a new type"},
		{sdl.SCANCODE_G, false, false, entityShortcutToolMove, "G is the move tool, as in every other viewport"},
		{sdl.SCANCODE_R, false, false, entityShortcutToolRotate, "R rotates"},
		{sdl.SCANCODE_C, false, false, entityShortcutToolScale, "C scales"},
		{sdl.SCANCODE_G, false, true, entityShortcutToggleGrid, "Shift+G is the grid, which gave the bare key up to the tool"},
	}
	for _, c := range cases {
		if got := entityShortcutFor(c.sc, c.ctrl, c.shift); got != c.want {
			t.Errorf("%s: got %v, want %v", c.reason, got, c.want)
		}
	}
}

// TestBareSIsNotSave is the collision worth pinning on its own: S is a movement
// key here, and mapping it to Save without the modifier would write a file
// every time someone backed the camera up. Space is on the list for the same
// reason — it rises, and used to toggle animation playback.
func TestBareSIsNotSave(t *testing.T) {
	for _, sc := range []sdl.Scancode{
		sdl.SCANCODE_W, sdl.SCANCODE_A, sdl.SCANCODE_S, sdl.SCANCODE_D,
		sdl.SCANCODE_SPACE,
	} {
		if got := entityShortcutFor(sc, false, false); got != entityShortcutNone {
			t.Errorf("movement key %v is bound to %v", sc, got)
		}
	}
}

// TestEntityPlayIsOnP pins where the playback toggle went when Space became the
// camera's "up".
func TestEntityPlayIsOnP(t *testing.T) {
	if got := entityShortcutFor(sdl.SCANCODE_P, false, false); got != entityShortcutTogglePlay {
		t.Errorf("P is bound to %v, want the playback toggle", got)
	}
}

// TestEntityCameraMoveInBothModes is the bug this pair of bindings was reported
// as: up and down did nothing. Flying moves the eye; orbiting moves the pivot,
// rather than nothing at all.
func TestEntityCameraMoveInBothModes(t *testing.T) {
	fly := entityCamera{Fly: true, Dist: 6}
	fly.move(0, 2, 0)
	if fly.Pos.Y() != 2 {
		t.Errorf("flying up moved the eye to %v, want Y=2", fly.Pos)
	}
	fly.move(0, -3, 0)
	if fly.Pos.Y() != -1 {
		t.Errorf("flying down moved the eye to %v, want Y=-1", fly.Pos)
	}

	orbit := entityCamera{Dist: 6}
	orbit.move(0, 2, 0)
	if orbit.Target.Y() != 2 {
		t.Errorf("orbiting up moved the pivot to %v, want Y=2", orbit.Target)
	}
	if orbit.Pos != (mgl32.Vec3{}) {
		t.Errorf("orbiting moved the eye directly: %v", orbit.Pos)
	}
}

func TestEntityAddAndRemoveModel(t *testing.T) {
	def := entityTestDoc(t)

	entityAddModel()
	if len(def.Models) != 1 {
		t.Fatalf("added %d models, want 1", len(def.Models))
	}
	if entitySel.Kind != entitySelModel || entitySel.Idx != 0 {
		t.Errorf("adding a model did not select it: %+v", entitySel)
	}
	if !entityDefDirty {
		t.Error("adding a model left the document clean")
	}

	// A second one may not take the first's name: a script addresses an
	// attachment by it.
	entityAddModel()
	if entityDef.Models[0].Name == entityDef.Models[1].Name {
		t.Errorf("both attachments are called %q", entityDef.Models[0].Name)
	}

	entityRemoveModel(0)
	if len(entityDef.Models) != 1 {
		t.Fatalf("after removing one, %d remain", len(entityDef.Models))
	}
	if entitySel.Kind != entitySelNone {
		t.Error("the selection outlived the thing it pointed at")
	}
}

func TestEntityUndoRestoresTheDocument(t *testing.T) {
	def := entityTestDoc(t)
	def.BoundHalfY = 0.5

	EntityDefPushUndo()
	entityDef.BoundHalfY = 4
	entityAddModel() // pushes a second snapshot of its own

	// One step back is the model, not the bounds: each edit is its own entry.
	EntityDefUndo()
	if len(entityDef.Models) != 0 {
		t.Errorf("undo left %d models behind", len(entityDef.Models))
	}
	if entityDef.BoundHalfY != 4 {
		t.Errorf("one undo also reverted the bounds (%v); the steps are collapsed", entityDef.BoundHalfY)
	}

	EntityDefUndo()
	if entityDef.BoundHalfY != 0.5 {
		t.Errorf("BoundHalfY = %v after the second undo, want 0.5", entityDef.BoundHalfY)
	}

	EntityDefRedo()
	if entityDef.BoundHalfY != 4 {
		t.Errorf("BoundHalfY = %v after redo, want 4", entityDef.BoundHalfY)
	}
	EntityDefRedo()
	if len(entityDef.Models) != 1 {
		t.Errorf("redo restored %d models, want 1", len(entityDef.Models))
	}
}

// TestEntityUndoDoesNotTouchTheLevelStack is what the redirect exists for: the
// shared tag/attribute widgets push through EditorPushUndo, and while this
// window's panel is drawing that has to land on the document's stack.
func TestEntityUndoDoesNotTouchTheLevelStack(t *testing.T) {
	entityTestDoc(t)
	before := levelUndoStack.len()

	withUndoRedirect(entityPushUndoDirty, func() {
		EditorPushUndo() // what editorTagsUI does when a tag is added
	})

	if levelUndoStack.len() != before {
		t.Errorf("the level's undo stack grew from %d to %d", before, levelUndoStack.len())
	}
	if !EntityDefCanUndo() {
		t.Error("the redirect did not reach the document's own stack")
	}
	if !entityDefDirty {
		t.Error("the redirect snapshotted but did not mark the document changed")
	}
}

// TestUndoRedirectIsRestored: the redirect is a global, so leaving it set would
// silently send the level editor's own undo to a closed window's stack.
func TestUndoRedirectIsRestored(t *testing.T) {
	entityTestDoc(t)
	withUndoRedirect(entityPushUndoDirty, func() {})
	if editorUndoRedirect != nil {
		t.Error("withUndoRedirect left the redirect installed")
	}
}

func TestEntityClampSelectionDropsStaleIndices(t *testing.T) {
	def := entityTestDoc(t)
	def.Models = []world.EntityModel{{Name: "body"}}

	entitySel = entityEditorSelection{Kind: entitySelModel, Idx: 0}
	entityClampSelection()
	if entitySel.Kind != entitySelModel {
		t.Error("a valid selection was cleared")
	}

	entitySel = entityEditorSelection{Kind: entitySelModel, Idx: 3}
	entityClampSelection()
	if entitySel.Kind != entitySelNone {
		t.Errorf("a selection past the end survived: %+v", entitySel)
	}

	// Seats, when there is no vehicle at all, are the same case.
	entitySel = entityEditorSelection{Kind: entitySelSeat, Idx: 0}
	entityClampSelection()
	if entitySel.Kind != entitySelNone {
		t.Error("a seat selection survived a type with no vehicle")
	}
}

// TestEntityBrushRoundTripsThroughTheDocument covers the rule the whole
// geometry seam rests on: the scratch chunk holds the live brushes, and the
// document's copy is taken from it at the end of a frame.
//
// Committing eagerly instead would make Ctrl+Z a no-op, because a tool pushes
// its undo entry BEFORE it edits and the document has to still be holding the
// pre-edit state at that moment.
func TestEntityBrushRoundTripsThroughTheDocument(t *testing.T) {
	def := entityTestDoc(t)
	t.Cleanup(entityUseWorkspace())

	ch := world.Chunk{ID: entityGeomChunkID, Scale: 1}
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("box", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"),
	}
	ch.RecomputeBounds()
	prev, prevDirty := entityGeomLevel, entityGeomDirty
	entityGeomLevel = &world.Level{Chunks: []world.Chunk{ch}}
	entityWS.level = entityGeomLevel
	entityWS.ActiveChunkIdx = 0
	// Hand-seeded, which is what entitySeedGeometry would have done — and it
	// clears the flag, because the two copies are in step from that moment.
	entityGeomDirty = false
	t.Cleanup(func() {
		entityGeomLevel, entityGeomDirty = prev, prevDirty
		entityWS.level = prev
	})

	if len(def.Brushes) != 0 {
		t.Fatalf("the document started with %d brushes", len(def.Brushes))
	}
	entityCommitBrushes()
	if len(def.Brushes) != 1 || def.Brushes[0].ID != "box" {
		t.Fatalf("the commit did not reach the document: %+v", def.Brushes)
	}

	// A frame that changed nothing must not re-copy and must not dirty the
	// document — otherwise moving the camera would mark a type unsaved.
	entityDefDirty = false
	entityCommitBrushes()
	if entityDefDirty {
		t.Error("a frame that changed no geometry marked the document dirty")
	}

	// And a real edit does get through.
	entityGeomLevel.Chunks[0].Brushes[0].Verts[0] = mgl32.Vec3{9, 9, 9}
	entityCommitBrushes()
	if def.Brushes[0].Verts[0] != (mgl32.Vec3{9, 9, 9}) {
		t.Errorf("a moved corner did not reach the document: %v", def.Brushes[0].Verts[0])
	}
	if !entityDefDirty {
		t.Error("a moved corner left the document clean")
	}
}

// TestEntityBrushesAreDeepCopied: the document and the live chunk must not
// share a backing array, or undo would restore a snapshot that had already been
// edited in place.
func TestEntityBrushesAreDeepCopied(t *testing.T) {
	src := []world.Brush{world.NewBoxBrush("box", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")}
	dst := world.CloneEntityBrushes(src)
	dst[0].Verts[0] = mgl32.Vec3{5, 5, 5}
	if src[0].Verts[0] == (mgl32.Vec3{5, 5, 5}) {
		t.Error("the copy shares its vertex array with the original")
	}
	dst[0].Faces[0].Indices[0] = 7
	if src[0].Faces[0].Indices[0] == 7 {
		t.Error("the copy shares a face's index loop with the original")
	}
}

// TestEntityTypeNameFor: the third barrel placed proposes "barrel", not
// "barrel_3" — the suffix is the editor's, not the author's.
func TestEntityTypeNameFor(t *testing.T) {
	cases := map[string]string{
		"barrel":   "barrel",
		"barrel_3": "barrel",
		"crate_2":  "crate",
		// Not a suffix the editor minted; a name that happens to end in a word.
		"lift_car": "lift_car",
	}
	for id, want := range cases {
		e := world.NewBlankEntity(id)
		if got := entityTypeNameFor(&e); got != want {
			t.Errorf("entityTypeNameFor(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestFitBoundsUsesTheBoundsItself covers the degenerate case the button has to
// survive: a type with no models and no chunks still has its own cage, and
// fitting to it must not collapse it to a point.
func TestFitBoundsToContentsWithOnlyACage(t *testing.T) {
	def := entityTestDoc(t)
	def.BoundOffset = mgl32.Vec3{0, 1, 0}
	def.BoundHalfX, def.BoundHalfY, def.BoundHalfZ = 0.5, 1, 0.5

	entityFitBoundsToContents()

	if !vec3Close(entityDef.BoundOffset, mgl32.Vec3{0, 1, 0}, 1e-4) {
		t.Errorf("centre moved to %v", entityDef.BoundOffset)
	}
	if !nearlyEqual(entityDef.BoundHalfY, 1, 1e-4) {
		t.Errorf("half height became %v, want 1", entityDef.BoundHalfY)
	}
}

// TestAnimationSettingsSeesAValueChange is the aliasing trap the dirty marker
// depends on: editorEntityBlendField writes through the pointer it is given, so
// a struct copy taken beforehand shares that float and compares equal however
// far the slider moved.
func TestAnimationSettingsSeesAValueChange(t *testing.T) {
	blend := float32(0.15)
	a := world.EntityAnimation{Name: "walk", BlendTime: &blend}

	before := animationSettings(&a)
	*a.BlendTime = 0.4
	if animationSettings(&a) == before {
		t.Error("a blend change through the pointer was not noticed")
	}

	// And the tri-state stays a tri-state: unset is not the same as an explicit
	// value that happens to be zero.
	off := false
	unset := animationSettings(&world.EntityAnimation{})
	explicit := animationSettings(&world.EntityAnimation{Loop: &off})
	if unset == explicit {
		t.Error("follow-the-clip and an explicit `once` compare equal")
	}
}

// TestOpeningATypeSurvivesTheEndOfTheFrame is the bug that made an opened type
// look blank: the Open dialog is drawn near the END of the frame, long after
// entityPreviewSync has had its chance to re-seed, so the live chunk on that
// frame is still the previous document's — an empty one, for the blank type the
// window starts on. Committing it into the freshly loaded document threw away
// every brush the file had, and marked the type dirty while doing it.
//
// The document is authoritative until the next frame re-seeds from it, which is
// what entityGeomDirty says.
func TestOpeningATypeSurvivesTheEndOfTheFrame(t *testing.T) {
	entityTestDoc(t)
	t.Cleanup(entityUseWorkspace())

	// The live chunk of the document being replaced: the blank type the entity
	// editor opens on has no brushes at all.
	prev, prevDirty := entityGeomLevel, entityGeomDirty
	entityGeomLevel = &world.Level{Chunks: []world.Chunk{{ID: entityGeomChunkID, Scale: 1}}}
	entityWS.level = entityGeomLevel
	entityWS.ActiveChunkIdx = 0
	entityGeomDirty = false
	t.Cleanup(func() {
		entityGeomLevel, entityGeomDirty = prev, prevDirty
		entityWS.level = prev
	})

	// What entityLoadDef does, minus the file: a new document, and the live
	// brushes declared stale.
	entityDef = entityNewDefaultDef("ship")
	entityDef.Brushes = []world.Brush{
		world.NewBoxBrush("hull", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, ""),
	}
	entityDefDirty = false
	entityInvalidateGeometry()

	entityCommitBrushes()

	if len(entityDef.Brushes) != 1 {
		t.Fatalf("opening a type emptied it: %d brushes left", len(entityDef.Brushes))
	}
	if entityDefDirty {
		t.Error("opening a type marked it unsaved before anyone had edited it")
	}
}

func TestEntityAddAndRemoveEmitter(t *testing.T) {
	def := entityTestDoc(t)

	entityAddEmitter()
	if len(def.Emitters) != 1 {
		t.Fatalf("added %d emitters, want 1", len(def.Emitters))
	}
	if entitySel.Kind != entitySelEmitter || entitySel.Idx != 0 {
		t.Errorf("adding an emitter did not select it: %+v", entitySel)
	}
	if !entityDefDirty {
		t.Error("adding an emitter left the document clean")
	}
	// The defaults have to be the running ones, or an author who adds an
	// effect and picks a document sees nothing and has no reason to look at
	// three switches to find out why.
	m := &def.Emitters[0]
	if !m.Enabled || !m.Emitting || m.RateScale != 1 {
		t.Errorf("a freshly added emitter is not running: %+v", m)
	}

	// A second one may not take the first's name: it is what an instance
	// override matches on and what the level ID is built from.
	entityAddEmitter()
	if def.Emitters[0].Name == def.Emitters[1].Name {
		t.Errorf("both attachments are called %q", def.Emitters[0].Name)
	}

	entityRemoveEmitter(0)
	if len(def.Emitters) != 1 {
		t.Fatalf("after removing one, %d remain", len(def.Emitters))
	}
	if entitySel.Kind != entitySelNone {
		t.Error("the selection outlived the thing it pointed at")
	}
}

// TestEmitterSelectionDrivesTheGizmo pins the one thing that makes an emitter
// aimable by eye: the move and rotate handles have to resolve to its offset and
// rotation, and there must be no scale handle, since an effect's size is its
// document's and a handle writing nothing reads as broken.
func TestEmitterSelectionDrivesTheGizmo(t *testing.T) {
	def := entityTestDoc(t)
	entityAddEmitter()

	target, ok := entityGizmoTargetOf()
	if !ok {
		t.Fatal("a selected emitter exposes no gizmo target")
	}
	if target.Pos != &def.Emitters[0].Offset {
		t.Error("the move gizmo does not write the emitter's offset")
	}
	if target.Rot != &def.Emitters[0].Rotation {
		t.Error("the rotate gizmo does not write the emitter's rotation")
	}
	if target.Scale != nil || target.ScaleU != nil {
		t.Error("an emitter offered a scale handle, which would write nothing")
	}
}

func TestEntityClampDropsAStaleEmitterSelection(t *testing.T) {
	def := entityTestDoc(t)
	def.Emitters = []world.EntityEmitter{world.NewEntityEmitter("thruster")}

	entitySel = entityEditorSelection{Kind: entitySelEmitter, Idx: 0}
	entityClampSelection()
	if entitySel.Kind != entitySelEmitter {
		t.Error("a valid emitter selection was cleared")
	}

	entitySel = entityEditorSelection{Kind: entitySelEmitter, Idx: 4}
	entityClampSelection()
	if entitySel.Kind != entitySelNone {
		t.Errorf("a selection past the end survived: %+v", entitySel)
	}
}
