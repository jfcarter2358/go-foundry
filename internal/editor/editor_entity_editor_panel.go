package editor

// editor_entity_editor_panel.go — the entity editor's toolbar, outline, right-
// hand properties column and dialogs.
//
// The layout answers the question an author is actually working through. The
// left column is *what this type is made of* — a list of models, chunks and
// seats, which is the thing you add to and delete from. The right column is
// split in two: what the whole type is (identity, bounds, collision, tags,
// attributes, script, vehicle) above, and whatever the outline has selected
// below. Selecting a model there and reading its transform here is the same
// gesture the model editor and the UI editor both use.
//
// Every mutating widget pushes undo. The drags push on the frame they *start*
// rather than once per frame they move — entityBeginEdit, the latch the
// particle editor uses for the same reason.
//
// The tag, attribute and script widgets are the shared ones from
// editor_script_panel.go rather than copies. They push undo themselves, which
// for a definition has to go somewhere other than the level's stack, so the
// whole panel runs inside withUndoRedirect — see editorUndoRedirect.

import (
	"fmt"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/world"
	"slices"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// Dialog visibility and the buffers behind them.
var (
	entityShowOpenDialog   bool
	entityShowSaveAsDialog bool
	entityShowImportChunk  bool
	entityDefFileList      []string
	entityChunkFileList    []string
	entitySaveAsName       string
)

// entityDragActive is the label of the drag in progress, so undo is pushed once
// when it starts rather than once per frame while it moves.
var entityDragActive string

// entityBeginEdit pushes undo for an edit identified by label, at most once per
// continuous drag, and marks the document changed.
func entityBeginEdit(label string) {
	if entityDragActive != label {
		entityDragActive = label
		EntityDefPushUndo()
	}
	entityMarkDirty()
}

// entityPushUndoDirty is what the shared tag/attribute/script widgets push
// through while this window's panel is drawing. They mutate the document as
// well as snapshotting it, and unlike the widgets written here they have no way
// to say so — so the redirect marks the document changed and rebuilds the
// preview alongside taking the snapshot.
func entityPushUndoDirty() {
	EntityDefPushUndo()
	entityMarkDirty()
	entityPreviewInvalidate()
}

// entityEndEdits clears the drag latch. Called once a frame, after the panel,
// when nothing is being dragged any more.
func entityEndEdits() {
	if !imgui.IsAnyItemActive() {
		entityDragActive = ""
	}
}

// ---------- toolbar ----------

func entityEditorToolbar() {
	w := entityEditorWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: entityToolbarH})
	imgui.BeginV("##enttoolbar", nil,
		imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoTitleBar|
			imgui.WindowFlagsNoScrollbar)

	if imgui.Button("New##entnew") {
		entityNewDoc()
	}
	imgui.SameLine()
	if imgui.Button("Open##entopen") {
		entityShowOpenDialog = true
		entityDefFileList = nil
	}
	imgui.SameLine()
	if imgui.Button("Save##entsave") {
		entitySaveCurrent()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Write " + world.EntitiesDirName + "/<id>.yaml and re-stamp\n" +
			"every instance of it in the open level.")
	}
	imgui.SameLine()
	if imgui.Button("Save As##entsaveas") {
		entitySaveAsName = entityDefName()
		entityShowSaveAsDialog = true
	}

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	if imgui.Button("Undo##entundo") {
		EntityDefUndo()
	}
	imgui.SameLine()
	if imgui.Button("Redo##entredo") {
		EntityDefRedo()
	}

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	name := entityDefName()
	if name == "" {
		name = "(unnamed)"
	}
	if entityDefDirty {
		name += " *"
	}
	imgui.Text(name)

	// Pop In sits at the right edge, as the model and particle editors' do.
	imgui.SameLine()
	imgui.SetCursorPos(imgui.Vec2{X: float32(w.W) - 90, Y: 6})
	if imgui.Button("Pop In##entpopin") {
		CloseEntityEditorWindow()
	}

	// Second row: view toggles and the status line.
	imgui.SetCursorPos(imgui.Vec2{X: 8, Y: 30})
	imgui.Checkbox("Grid [\u21e7G]##entgrid", &entityShowGrid)
	imgui.SameLine()
	imgui.Checkbox("Bounds [B]##entbnds", &entityShowBounds)
	imgui.SameLine()
	imgui.Checkbox("Models##entshowm", &entityShowModels)
	imgui.SameLine()
	imgui.Checkbox("Brushes##entshowb", &entityShowBrushes)
	imgui.SameLine()
	imgui.Checkbox("Seats##entshows", &entityShowSeats)
	imgui.SameLine()
	imgui.Checkbox("Effects##entshowe", &entityShowEffects)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Draw the type's emitters and the particles they are\n" +
			"producing. Off leaves the simulation running — it is a\n" +
			"view toggle, not the emitter's own switch.")
	}
	imgui.SameLine()
	imgui.Checkbox("Wire##entwire", &entityWireOverlay)
	imgui.SameLine()
	imgui.Checkbox("Play [P]##entplay", &entityPlayAnims)
	// ---- Group: edit context and selection unit ----
	//
	// The 3D view's own controls, on the 3D view's own keys, because the tools
	// under them are the 3D view's — see editor_workspace.go. Object mode moves
	// whole brushes about; edit mode reaches the points, edges and faces one is
	// made of.
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	if editorActiveToggleButton("Object [Tab]##entctxobj", !editorInEditMode()) {
		editorSetEditContext(editorObjectMode)
		entityBrushSelectionSync()
	}
	imgui.SameLine()
	if editorActiveToggleButton("Edit [Tab]##entctxedit", editorInEditMode()) {
		editorSetEditContext(editorEditMode)
		entityBrushSelectionSync()
	}
	if editorInEditMode() {
		for _, m := range editorSelModes {
			imgui.SameLine()
			if editorActiveToggleButton(
				fmt.Sprintf("%s [%s]##entsel%d", m, editorSelModeKey(m), int(m)),
				entityWS.SelMode == m) {
				editorSetSelMode(m)
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(editorSelModeTooltip(m))
			}
		}
	}

	// ---- Group: gizmo tool ----
	imgui.SameLine()
	editorToolbarGroupSep()
	for _, tool := range editorGizmoTools {
		imgui.SameLine()
		if editorActiveToggleButton(
			fmt.Sprintf("%s [%s]##enttool%d", tool, editorGizmoToolKey(tool), int(tool)),
			entityGizmoTool == tool) {
			entitySetGizmoTool(tool)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(entityGizmoToolTooltip(tool))
		}
	}

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	if editorActiveToggleButton("Fly [V]##entfly", entityCam.Fly) {
		entityCam.setFly(!entityCam.Fly)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("WASD to move, Space/Shift up and down, Ctrl to go faster,\n" +
			"right-drag to look — for getting inside a hull rather than\n" +
			"orbiting it. The keys move the pivot while orbiting.")
	}
	imgui.SameLine()
	if imgui.Button("Frame [F]##entframe") {
		entityCam.frame()
	}
	imgui.SameLine()
	imgui.SetNextItemWidth(100)
	imgui.ColorEdit3V("BG##entbg", &entityBgColor, imgui.ColorEditFlagsNoInputs)
	if w != nil {
		w.ClearColor = [4]float32{entityBgColor[0], entityBgColor[1], entityBgColor[2], 1}
	}

	if msg := entityToolbarStatus(); msg != "" {
		imgui.SameLine()
		editorToolbarGroupSep()
		imgui.SameLine()
		editorTextMuted(msg)
	}

	imgui.End()
}

// entityToolbarStatus is the one line of feedback the toolbar has room for.
// A build error wins over a status message: a missing chunk file is a thing to
// fix, and "Saved barrel.yaml" is not worth hiding it behind.
func entityToolbarStatus() string {
	if entityPreviewErr != "" {
		return "! " + entityPreviewErr
	}
	return entityStatus
}

// ---------- outline ----------

// entityEditorOutline is the left column: what the type is made of.
func entityEditorOutline() {
	w := entityEditorWindow
	if w == nil || entityDef == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: entityToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: entityOutlineWidth, Y: float32(w.H) - entityToolbarH})
	imgui.BeginV("Contents##entoutline", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	withUndoRedirect(entityPushUndoDirty, func() {
		entityOutlineModels()
		imgui.Separator()
		entityOutlineBrushes()
		imgui.Separator()
		entityOutlineEmitters()
		imgui.Separator()
		entityOutlineSeats()
	})

	imgui.End()
}

func entityOutlineModels() {
	imgui.Text("Models")
	for i := range entityDef.Models {
		m := &entityDef.Models[i]
		imgui.PushID(fmt.Sprintf("outm%d", i))
		label := m.Name
		if m.Path == "" {
			label += "  (no mesh)"
		}
		if imgui.SelectableV(label, entitySel.Kind == entitySelModel && entitySel.Idx == i, 0, imgui.Vec2{}) {
			entitySel = entityEditorSelection{Kind: entitySelModel, Idx: i}
		}
		imgui.PopID()
	}
	if len(entityDef.Models) == 0 {
		editorTextMutedWrapped("None. A type with no model is still a position with attributes on it.")
	}
	if imgui.Button("Add Model##entaddm") {
		entityAddModel()
	}
}

// entityOutlineBrushes lists the type's own solid.
//
// The row and the viewport are one selection, not two that happen to agree:
// entitySelectBrush sets both this list's cursor and the shared object-mode
// brush selection, so clicking a name here puts the gizmo on that solid and
// clicking that solid highlights the name.
func entityOutlineBrushes() {
	imgui.Text("Brushes")
	n := entityBrushCount()
	for i := 0; i < n; i++ {
		b := entityBrushAt(i)
		if b == nil {
			continue
		}
		imgui.PushID(fmt.Sprintf("outb%d", i))
		label := b.ID
		switch {
		case b.Dynamic:
			label += "  (dynamic)"
		case !b.Solid:
			label += "  (not solid)"
		}
		if imgui.SelectableV(label, entitySel.Kind == entitySelBrush && entitySel.Idx == i, 0, imgui.Vec2{}) {
			entitySelectBrush(i)
		}
		imgui.PopID()
	}
	if n == 0 {
		editorTextMutedWrapped("None. Brushes give the type floors to stand on and walls to bump into, carried wherever it goes — and a body that walks inside them is carried with it.")
	}

	for i, kind := range brushKinds {
		k := kind
		if i > 0 {
			imgui.SameLine()
		}
		label := brushKindLabel(k)
		if entityWS.BrushPlaceActive && entityWS.BrushPlaceKind == k {
			// An armed stamp says so on its own button: the ghost is in the
			// viewport, but the button is where the author armed it.
			label = "* " + label
		}
		if imgui.Button(label + "##entstamp" + k) {
			entityArmBrushStamp(k)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Arms the stamp: a ghost follows the cursor and every click\n" +
			"places another solid, until Escape. Shape one with the tools\n" +
			"the 3D view uses — Tab for edit mode, E extrude, I inset,\n" +
			"B subdivide, G/R/C to move, turn and resize.")
	}
	if imgui.Button("Import##entimpc") {
		entityShowImportChunk = true
		entityChunkFileList = nil
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Copies the brushes out of a chunk under " + levelio.ChunksDirName + "/,\n" +
			"or out of the level open in the main window. A copy, not a\n" +
			"link: the type owns its geometry from then on.")
	}
}

// entityOutlineEmitters lists the effects the type carries.
//
// The label carries the state that decides whether an author is looking at a
// bug or at what they asked for: no document chosen, a document that would not
// load, and an attachment deliberately shipped switched off all look identical
// in the viewport, because all three draw nothing.
func entityOutlineEmitters() {
	imgui.Text("Emitters")
	for i := range entityDef.Emitters {
		m := &entityDef.Emitters[i]
		imgui.PushID(fmt.Sprintf("oute%d", i))
		label := m.Name
		switch {
		case m.Effect == "":
			label += "  (no effect)"
		case !entityEmitterLoaded(i):
			label += "  (missing)"
		case !m.Enabled:
			label += "  (off)"
		case !m.Emitting:
			label += "  (not emitting)"
		}
		if imgui.SelectableV(label, entitySel.Kind == entitySelEmitter && entitySel.Idx == i, 0, imgui.Vec2{}) {
			entitySel = entityEditorSelection{Kind: entitySelEmitter, Idx: i}
		}
		imgui.PopID()
	}
	if len(entityDef.Emitters) == 0 {
		editorTextMutedWrapped("None. An emitter hangs a particle effect off the type — off its origin, or off a bone of one of its models — and it is carried wherever the thing goes.")
	}
	if imgui.Button("Add Emitter##entadde") {
		entityAddEmitter()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Adds an effect attachment. Pick its " + ParticleExtLabel + " on the\n" +
			"right, and aim it with the move and rotate gizmos.")
	}
}

func entityOutlineSeats() {
	imgui.Text("Seats")
	if entityDef.Vehicle == nil {
		editorTextMutedWrapped("Not a vehicle. The Vehicle group on the right makes it one.")
		return
	}
	v := entityDef.Vehicle
	for i := range v.Seats {
		s := &v.Seats[i]
		imgui.PushID(fmt.Sprintf("outs%d", i))
		label := s.Name
		if s.Driver {
			label += "  (driver)"
		}
		if imgui.SelectableV(label, entitySel.Kind == entitySelSeat && entitySel.Idx == i, 0, imgui.Vec2{}) {
			entitySel = entityEditorSelection{Kind: entitySelSeat, Idx: i}
		}
		imgui.PopID()
	}
	if len(v.Seats) == 0 {
		editorTextMutedWrapped("None. A vehicle with no seat cannot be boarded.")
	}
	if imgui.Button("Add Seat##entadds") {
		entityAddSeat()
	}
}

// ---------- properties ----------

// entityEditorPanel is the right column: the whole type above, the selection
// below.
func entityEditorPanel() {
	w := entityEditorWindow
	if w == nil || entityDef == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(w.W) - entityPanelWidth, Y: entityToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: entityPanelWidth, Y: float32(w.H) - entityToolbarH})
	imgui.BeginV("Type##entpanel", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	withUndoRedirect(entityPushUndoDirty, func() {
		editorPropGroupOpen("Identity##entdefid", entityPanelIdentity)
		editorPropGroup("Bounds##entdefbounds", 0, entityPanelBounds)
		editorPropGroup("Collision##entdefcol", 0, entityPanelCollision)
		editorPropGroup("Scripting##entdefscript", 0, entityPanelScripting)
		editorPropGroup("Vehicle##entdefveh", 0, entityPanelVehicle)

		imgui.Separator()
		entityPanelSelection()
	})

	imgui.End()
}

func entityPanelIdentity() {
	id := entityDef.Name
	if imgui.InputTextV("Type id##entdefname", &id, 0, nil) && id != entityDef.Name {
		entityBeginEdit("defname")
		entityDef.Name = strings.TrimSpace(id)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The name a level writes as an entity's `type:`, and the\n" +
			"file it is saved to. Changing it and saving forks the type.")
	}
	desc := entityDef.Description
	if imgui.InputTextV("Description##entdefdesc", &desc, 0, nil) {
		entityBeginEdit("defdesc")
		entityDef.Description = desc
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("One line, shown as the tooltip in the level editor's\nplacement catalog.")
	}
	if imgui.Checkbox("Visible##entdefvis", &entityDef.Visible) {
		entityBeginEdit("defvis")
		entityPreviewInvalidate()
	}
	if entityDefSaved != "" && entityDefSaved != entityDef.Name {
		editorTextMutedWrapped("Opened as " + entityDefSaved + ". Saving now writes a new type and leaves that one alone.")
	}
}

func entityPanelBounds() {
	editorTextMutedWrapped("The volume the world and other entities collide with — routinely nothing like the shape of the mesh inside it.")
	if imgui.BeginCombo("Shape##entdefbk", world.BoundingTypeName(entityDef.BoundingKind)) {
		for i, n := range world.BoundingTypeNames {
			if imgui.SelectableV(n, world.BoundingType(i) == entityDef.BoundingKind, 0, imgui.Vec2{}) {
				entityBeginEdit("defbk")
				entityDef.BoundingKind = world.BoundingType(i)
			}
		}
		imgui.EndCombo()
	}
	half := [3]float32{entityDef.BoundHalfX, entityDef.BoundHalfY, entityDef.BoundHalfZ}
	if editorDragFloat3Step("Half extents##entdefhalf", &half, 0, 1000, "%.3f", 0.05) {
		entityBeginEdit("defhalf")
		entityDef.BoundHalfX, entityDef.BoundHalfY, entityDef.BoundHalfZ = half[0], half[1], half[2]
	}
	off := [3]float32{entityDef.BoundOffset.X(), entityDef.BoundOffset.Y(), entityDef.BoundOffset.Z()}
	if editorDragFloat3Step("Centre offset##entdefboff", &off, -1000, 1000, "%.3f", 0.05) {
		entityBeginEdit("defboff")
		entityDef.BoundOffset = mgl32.Vec3{off[0], off[1], off[2]}
	}
	if imgui.Button("Fit To Contents##entdeffit") {
		entityFitBoundsToContents()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Set the box to everything the preview draws — a starting\npoint to trim, not usually the final answer.")
	}
	if entityDef.BoundHalfX <= 0 || entityDef.BoundHalfY <= 0 || entityDef.BoundHalfZ <= 0 {
		editorTextWrapped("No volume: every extent must be above zero. Instances are never culled and never contain a point.")
	}
}

// entityFitBoundsToContents sizes the bounding volume around the preview.
//
// It is a starting point rather than an answer, and says so: a character's box
// should be narrower than their outstretched arms, and a ship's should usually
// be the hull rather than the aerial. But typing six numbers to get near the
// right ballpark is worse than trimming a box that is already close.
func entityFitBoundsToContents() {
	min, max, ok := entityPreviewBounds()
	if !ok || entityDef == nil {
		entityStatus = "Nothing to fit to yet"
		return
	}
	EntityDefPushUndo()
	c := min.Add(max).Mul(0.5)
	half := max.Sub(min).Mul(0.5)
	entityDef.BoundOffset = c
	entityDef.BoundHalfX, entityDef.BoundHalfY, entityDef.BoundHalfZ = half.X(), half.Y(), half.Z()
	entityMarkDirty()
}

func entityPanelCollision() {
	c := &entityDef.Collision
	if imgui.Checkbox("Collide with world##entdefcw", &c.World) {
		entityBeginEdit("defcw")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Resolve against walls, floors, ceilings and slopes —\nthe same resolvers the player uses.")
	}
	if imgui.Checkbox("Collide with entities##entdefce", &c.Entities) {
		entityBeginEdit("defce")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Push apart from other entities that also have this on,\nand from the player. Both sides have to opt in.")
	}
	if imgui.Checkbox("Gravity##entdefcg", &c.Gravity) {
		entityBeginEdit("defcg")
	}
	if imgui.Checkbox("Movable##entdefcm", &c.Movable) {
		entityBeginEdit("defcm")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off makes it immovable scenery: it pushes other entities\nand is never pushed itself, and gravity does not touch it.")
	}
	editorTextMutedWrapped("Set here, every instance placed gets them — which is what this window is for.")
}

func entityPanelScripting() {
	editorTagsUI(&entityDef.Tags, "entdef", "Tags")
	editorAttributesUI(&entityDef.Attributes, "entdef", "Attributes", strconv.Quote(entityDefName()))
	editorScriptUI(&entityDef.Script, &entityDef.Params, "entdef", strconv.Quote(entityDefName()))
	editorTextMutedWrapped("Attributes and parameters here are the type's defaults. An instance overrides them one name at a time.")
}

// ---------- the selection ----------

func entityPanelSelection() {
	switch entitySel.Kind {
	case entitySelModel:
		if entitySel.Idx < len(entityDef.Models) {
			editorPropGroupOpen("Model: "+entityDef.Models[entitySel.Idx].Name+"##entselm",
				func() { entityPanelModel(entitySel.Idx) })
		}
	case entitySelBrush:
		// Not a panel of its own: editorBrushPropertiesSection is the level
		// editor's brush and face block, and it reads the workspace this window
		// has made current — so the ID, the solid and dynamic flags, the
		// tags/attributes/script, the convexity warning and every face field
		// are the same widgets writing to the same places.
		editorBrushPropertiesSection()
	case entitySelEmitter:
		if entitySel.Idx < len(entityDef.Emitters) {
			editorPropGroupOpen("Emitter: "+entityDef.Emitters[entitySel.Idx].Name+"##entsele",
				func() { entityPanelEmitter(entitySel.Idx) })
		}
	case entitySelSeat:
		if entityDef.Vehicle != nil && entitySel.Idx < len(entityDef.Vehicle.Seats) {
			editorPropGroupOpen("Seat: "+entityDef.Vehicle.Seats[entitySel.Idx].Name+"##entsels",
				func() { entityPanelSeat(entitySel.Idx) })
		}
	default:
		editorTextMutedWrapped("Pick something on the left to edit it.")
	}
}

func entityPanelModel(idx int) {
	m := &entityDef.Models[idx]
	name := m.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##entmname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		switch {
		case name == "":
			entityStatus = "An attachment needs a name"
		case name != m.Name && entityDefAsEntity().Model(name) != nil:
			entityStatus = fmt.Sprintf("This type already has an attachment called %q", name)
		default:
			EntityDefPushUndo()
			m.Name = name
			entityMarkDirty()
		}
	}
	imgui.SameLine()
	if imgui.Button("Delete##entmdel") {
		entityRemoveModel(idx)
		return
	}

	if modelFileCombo("Mesh##entmpath", &m.Path) {
		EntityDefPushUndo()
		if m.Path != "" && isPlaceholderModelName(m.Name) {
			m.Name = world.UniqueModelName(entityDefAsEntity(), world.ModelNameForPath(m.Path))
		}
		entityMarkDirty()
		entityPreviewInvalidate()
	}
	if m.Path == "" {
		editorTextMutedWrapped("No mesh: this attachment draws nothing.")
	}
	if textureCombo("Texture##entmtex", &m.Texture, true) {
		EntityDefPushUndo()
		entityMarkDirty()
		entityPreviewInvalidate()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Overrides the model document's own texture —\nfor reskinning one shared mesh per type.")
	}
	if imgui.Checkbox("Visible##entmvis", &m.Visible) {
		entityBeginEdit("mvis")
		entityPreviewInvalidate()
	}

	off := [3]float32{m.Offset.X(), m.Offset.Y(), m.Offset.Z()}
	if editorDragFloat3Step("Offset##entmoff", &off, -1000, 1000, "%.3f", 0.05) {
		entityBeginEdit("moff")
		m.Offset = mgl32.Vec3{off[0], off[1], off[2]}
		entityPreviewInvalidate()
	}
	rot := vec3ToDegrees(m.Rotation)
	if editorDragFloat3Step("Rotation (deg)##entmrot", &rot, -360, 360, "%.1f", 1) {
		entityBeginEdit("mrot")
		m.Rotation = vec3FromDegrees(rot)
		entityPreviewInvalidate()
	}
	sc := m.EffectiveScale()
	scv := [3]float32{sc.X(), sc.Y(), sc.Z()}
	if editorDragFloat3Step("Scale##entmscale", &scv, -100, 100, "%.3f", 0.01) {
		entityBeginEdit("mscale")
		m.Scale = mgl32.Vec3{
			world.EntityScaleClamp(scv[0]),
			world.EntityScaleClamp(scv[1]),
			world.EntityScaleClamp(scv[2]),
		}
		entityPreviewInvalidate()
	}
	alpha := m.EffectiveAlpha()
	if editorDragFloatStep("Alpha##entmalpha", &alpha, 0, 1, "%.2f", 0.02) {
		entityBeginEdit("malpha")
		m.Alpha = alpha
		entityPreviewInvalidate()
	}

	entityPanelModelAnimations(idx)
}

// entityPanelModelAnimations names the clips in the attachment's model. The
// clip list comes from the *preview*, which is the only place the .fmdl has
// actually been read — the definition holds a path, not a document.
func entityPanelModelAnimations(idx int) {
	editorPropGroupOpen("Animations##entmanims", func() {
		m := &entityDef.Models[idx]
		var clips []string
		var live *world.EntityModel
		if entityPreview != nil {
			live = entityPreview.Model(m.Name)
		}
		if live != nil && live.Anim != nil {
			clips = live.Anim.ClipNames()
		}
		if len(clips) == 0 {
			editorTextMutedWrapped("This mesh has no armature or no clips, so there is nothing to name.")
		}

		for i := range m.Animations {
			if entityAnimationRow(m, i, clips, live) {
				return // the list was rewritten; resume next frame
			}
		}
		if len(m.Animations) == 0 {
			imgui.Text("(none)")
		}
		if imgui.Button("Add Animation##entanimadd") {
			EntityDefPushUndo()
			base := "animation"
			if len(clips) > 0 {
				base = clips[0]
			}
			m.Animations = append(m.Animations, world.EntityAnimation{
				Name: world.UniqueAnimationName(m, base),
				Clip: base,
			})
			entityMarkDirty()
			entityPreviewInvalidate()
		}
	})
}

// entityAnimationRow draws one named animation. Reports whether the list was
// structurally changed, which ends the caller's iteration for this frame.
func entityAnimationRow(m *world.EntityModel, idx int, clips []string, live *world.EntityModel) bool {
	a := &m.Animations[idx]
	imgui.PushID(fmt.Sprintf("entanim%d", idx))
	defer imgui.PopID()

	name := a.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##name", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) && name != "" {
		if m.Animation(name) == nil {
			EntityDefPushUndo()
			a.Name = name
			entityMarkDirty()
		} else {
			entityStatus = fmt.Sprintf("This model already has an animation called %q", name)
		}
	}
	imgui.SameLine()
	if imgui.Button("x##del") {
		EntityDefPushUndo()
		m.Animations = append(m.Animations[:idx:idx], m.Animations[idx+1:]...)
		entityMarkDirty()
		entityPreviewInvalidate()
		return true
	}

	// Shown even when it is not among the model's clips, so a mapping that has
	// gone stale is visible rather than silently reset to something that
	// happens to exist.
	if imgui.BeginCombo("Clip##clip", a.EffectiveClip()) {
		known := false
		for _, c := range clips {
			if c == a.EffectiveClip() {
				known = true
			}
			if imgui.SelectableV(c, c == a.EffectiveClip(), 0, imgui.Vec2{}) {
				EntityDefPushUndo()
				a.Clip = c
				entityMarkDirty()
				entityPreviewInvalidate()
			}
		}
		if !known {
			imgui.SelectableV(a.EffectiveClip()+" (missing)", true, 0, imgui.Vec2{})
		}
		imgui.EndCombo()
	}
	speed := a.EffectiveSpeed()
	if editorDragFloatStep("Speed##speed", &speed, 0.01, 100, "%.2f", 0.05) {
		entityBeginEdit("animspeed")
		a.Speed = speed
		entityPreviewInvalidate()
	}
	// The shared loop and blend widgets write straight through, with no undo
	// push and nothing to report a change — they were written for the level
	// panel, where a field edit is not a tracked step. Comparing either side of
	// them is what lets the document's dirty marker stay honest here.
	before := animationSettings(a)
	editorEntityLoopCombo(a)
	editorEntityBlendField(a)
	if animationSettings(a) != before {
		entityMarkDirty()
		entityPreviewInvalidate()
	}
	if imgui.Checkbox("Autoplay##autoplay", &a.Autoplay) {
		entityBeginEdit("animauto")
		entityPreviewInvalidate()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Starts when an instance loads. At most one per\nattachment is honoured.")
	}

	// Preview playback acts on the live copy, not the document: which clip is
	// showing right now is not something the type ships with.
	if live != nil && live.Anim != nil {
		if live.Playing() == a.Name {
			imgui.Text("(playing)")
			imgui.SameLine()
			if imgui.Button("Stop##stop") {
				live.Stop()
			}
		} else if imgui.Button("Play##play") {
			if !live.Play(a.Name) {
				entityStatus = fmt.Sprintf("Cannot play %q — clip %q is not in this model",
					a.Name, a.EffectiveClip())
			}
		}
	}
	imgui.Separator()
	return false
}

// entityBrushSelectionSync keeps the outline's cursor and the viewport's
// object-mode selection agreeing when the click came from the viewport.
//
// entitySelectBrush does it in the other direction. Both exist because the two
// selections are genuinely different things — an outline row and a picked
// solid — and an author who clicks one expects the other to follow.
func entityBrushSelectionSync() {
	if len(entityWS.SelectedBrushes) == 0 {
		if entitySel.Kind == entitySelBrush {
			entitySel = entityEditorSelection{}
		}
		return
	}
	idx := entityWS.SelectedBrushes[0].brushIdx
	if entitySel.Kind != entitySelBrush || entitySel.Idx != idx {
		entitySel = entityEditorSelection{Kind: entitySelBrush, Idx: idx}
	}
}

// entityChunkFileCombo lists what is under chunks/. Rescanned only while the
// dropdown is open, the rule modelFileCombo follows: a chunk exported from the
// level editor a moment ago has to appear without a restart, and rescanning
// every frame would be a directory read per frame of a menu nobody opened.
func entityChunkFileCombo(label string, file *string) bool {
	preview := *file
	if preview == "" {
		preview = "(none)"
	}
	if !imgui.BeginCombo(label, preview) {
		return false
	}
	defer imgui.EndCombo()

	changed := false
	if imgui.SelectableV("(none)", *file == "", 0, imgui.Vec2{}) && *file != "" {
		*file = ""
		changed = true
	}
	known := false
	for _, n := range levelio.ScanChunkNames() {
		if n == *file {
			known = true
		}
		if imgui.SelectableV(n, n == *file, 0, imgui.Vec2{}) && n != *file {
			*file = n
			changed = true
		}
	}
	if !known && *file != "" {
		imgui.SelectableV(*file+" (missing)", true, 0, imgui.Vec2{})
	}
	return changed
}

// entityPanelEmitter draws one effect attachment: what it runs, where it hangs
// and the four knobs worth varying per type.
//
// Deliberately short, exactly as the level editor's emitter panel is and for the
// same reason: everything about how the effect *looks* lives in its .fpar and is
// edited in the particle editor. Edit... is the seam between the two.
func entityPanelEmitter(idx int) {
	m := &entityDef.Emitters[idx]
	name := m.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##entename", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		switch {
		case name == "":
			entityStatus = "An emitter needs a name"
		case name != m.Name && entityDef.EmitterNamed(name) != nil:
			entityStatus = fmt.Sprintf("This type already has an emitter called %q", name)
		default:
			EntityDefPushUndo()
			m.Name = name
			entityMarkDirty()
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Qualified by the placement in a level, so a script says\n" +
			"@<entity>." + m.Name + " to reach this ship's and not every ship's.")
	}
	imgui.SameLine()
	if imgui.Button("Delete##entedel") {
		entityRemoveEmitter(idx)
		return
	}

	entityPanelEmitterEffect(idx, m)
	entityPanelEmitterSocket(m)

	off := [3]float32{m.Offset.X(), m.Offset.Y(), m.Offset.Z()}
	if editorDragFloat3Step("Offset##enteoff", &off, -1000, 1000, "%.3f", 0.05) {
		entityBeginEdit("eoff")
		m.Offset = mgl32.Vec3{off[0], off[1], off[2]}
	}
	if m.Bone != "" && imgui.IsItemHovered() {
		imgui.SetTooltip("Local to the bone, not to the entity.")
	}
	rot := vec3ToDegrees(m.Rotation)
	if editorDragFloat3Step("Rotation (deg)##enterot", &rot, -360, 360, "%.1f", 1) {
		entityBeginEdit("erot")
		m.Rotation = vec3FromDegrees(rot)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Turns the emission shape — a cone fires wherever it\n" +
			"points. The stalk on the marker is that direction.")
	}

	editorTagsUI(&m.Tags, "ente", "Tags")
}

// entityPanelEmitterEffect draws the document picker, what it resolved to, and
// the two switches.
func entityPanelEmitterEffect(idx int, m *world.EntityEmitter) {
	if particleFileCombo("Effect##entefx", &m.Effect) {
		EntityDefPushUndo()
		entityMarkDirty()
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(m.Effect == "")
	if imgui.Button("Edit...##enteedit") {
		OpenParticleEditorOn(m.Effect)
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open this effect in the particle editor, where its forty\nparameters actually are.")
	}

	switch {
	case m.Effect == "":
		editorTextMutedWrapped("No effect chosen: this emitter produces nothing.")
	case !entityEmitterLoaded(idx):
		editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.5, Z: 0.3, W: 1},
			"Could not load "+m.Effect+ParticleExtLabel+" — the attachment is here but produces nothing.")
	default:
		editorTextMuted(fmt.Sprintf("live: %d", entityEmitterLiveCount(idx)))
	}
	if imgui.Button("Restart##enterestart") {
		entityEmitterRestart(idx)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Clear what is in flight and rewind — for judging a burst\nrather than waiting for the next one.")
	}

	if imgui.Checkbox("Enabled##enteen", &m.Enabled) {
		entityBeginEdit("een")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off stops the simulation dead — every live particle\nvanishes at once.")
	}
	if imgui.Checkbox("Emitting##enteem", &m.Emitting) {
		entityBeginEdit("eem")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off stops new particles but lets the ones in flight finish.\n" +
			"A type whose effect only runs when its script says so\nships with this off.")
	}

	if editorDragFloat("Rate scale##enterate", &m.RateScale, 0, 10, "%.2f") {
		entityBeginEdit("erate")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies every layer's emission rate. 1 is the\neffect's own.")
	}
	tint := m.EffectiveTint()
	t := [3]float32{tint.X(), tint.Y(), tint.Z()}
	if imgui.ColorEdit3("Tint##entetint", &t) {
		entityBeginEdit("etint")
		m.Tint = mgl32.Vec3{t[0], t[1], t[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies every layer's own tint — one green torch\nwithout a second document.")
	}
}

// entityPanelEmitterSocket draws the model-and-bone picker: which of the type's
// meshes the effect rides, if any.
//
// The bone list comes from the preview, which is the only place the .fmdl has
// actually been read — the same seam the animation clip list uses. A model with
// no rig therefore offers nothing, which is the honest answer.
func entityPanelEmitterSocket(m *world.EntityEmitter) {
	models := entityEmitterModelNames()
	if len(models) == 0 {
		editorTextMutedWrapped("Hangs off the type's origin. Add a model with a rig to hang it off a bone instead.")
		return
	}
	if entityNameCombo("Model##entemodel", &m.Model, models, "(the entity itself)") {
		EntityDefPushUndo()
		if m.Model == "" {
			// A bone is meaningless without the attachment it belongs to, and
			// one left behind would silently do nothing.
			m.Bone = ""
		}
		entityMarkDirty()
	}
	if m.Model == "" {
		return
	}
	bones := entityEmitterBoneNames(m.Model)
	if len(bones) == 0 {
		editorTextMutedWrapped("That attachment has no bones — either it is not rigged, or its mesh has not loaded.")
		return
	}
	if entityNameCombo("Bone##entebone", &m.Bone, bones, "(the attachment's origin)") {
		EntityDefPushUndo()
		entityMarkDirty()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The effect rides this bone's posed placement, so a flame\n" +
			"in a hand follows the hand. Its Offset is then local to\nthe bone.")
	}
}

// entityNameCombo picks one name out of a list, with an explicit empty choice at
// the top. Beside particleFileCombo in spirit: the empty entry is a real answer
// — "off the entity itself" — rather than a missing one.
func entityNameCombo(label string, value *string, names []string, emptyLabel string) bool {
	preview := *value
	if preview == "" {
		preview = emptyLabel
	}
	changed := false
	if imgui.BeginCombo(label, preview) {
		if imgui.SelectableV(emptyLabel, *value == "", 0, imgui.Vec2{}) && *value != "" {
			*value = ""
			changed = true
		}
		for _, n := range names {
			if imgui.SelectableV(n, n == *value, 0, imgui.Vec2{}) && n != *value {
				*value = n
				changed = true
			}
		}
		imgui.EndCombo()
	}
	if *value != "" && !slices.Contains(names, *value) {
		// A name the type no longer has: shown rather than silently reset, so
		// the author can see what a renamed attachment left behind.
		editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.5, Z: 0.3, W: 1},
			*value+" is not one of this type's — the effect falls back to the entity's origin.")
	}
	return changed
}

func entityPanelSeat(idx int) {
	v := entityDef.Vehicle
	s := &v.Seats[idx]
	name := s.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##entsname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) && name != "" {
		EntityDefPushUndo()
		s.Name = name
		entityMarkDirty()
	}
	imgui.SameLine()
	if imgui.Button("Delete##entsdel") {
		entityRemoveSeat(idx)
		return
	}
	if imgui.Checkbox("Driver##entsdrv", &s.Driver) {
		entityBeginEdit("sdrv")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Sitting here hands the controls over. A vehicle with no\ndriver seat is furniture that happens to move.")
	}
	pos := [3]float32{s.Position.X(), s.Position.Y(), s.Position.Z()}
	if editorDragFloat3Step("Position##entspos", &pos, -1000, 1000, "%.3f", 0.05) {
		entityBeginEdit("spos")
		s.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	yaw := mgl32.RadToDeg(s.Yaw)
	if editorDragFloatStep("Rest yaw (deg)##entsyaw", &yaw, -360, 360, "%.1f", 1) {
		entityBeginEdit("syaw")
		s.Yaw = mgl32.DegToRad(yaw)
	}
	eye := s.Eye
	if editorDragFloatStep("Eye height##entseye", &eye, 0, 5, "%.2f", 0.01) {
		entityBeginEdit("seye")
		s.Eye = eye
	}
	yl := mgl32.RadToDeg(s.YawLimit)
	if editorDragFloatStep("Yaw limit (deg)##entsyl", &yl, 0, 180, "%.1f", 1) {
		entityBeginEdit("syl")
		s.YawLimit = mgl32.DegToRad(yl)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far the occupant may look from the rest facing.\n0 is unrestricted.")
	}
	pl := mgl32.RadToDeg(s.PitchLimit)
	if editorDragFloatStep("Pitch limit (deg)##entspl", &pl, 0, 90, "%.1f", 1) {
		entityBeginEdit("spl")
		s.PitchLimit = mgl32.DegToRad(pl)
	}
	ex := [3]float32{s.Exit.X(), s.Exit.Y(), s.Exit.Z()}
	if editorDragFloat3Step("Exit##entsexit", &ex, -1000, 1000, "%.3f", 0.05) {
		entityBeginEdit("sexit")
		s.Exit = mgl32.Vec3{ex[0], ex[1], ex[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Where the occupant is put down on leaving. Authored,\nbecause guessing puts people inside walls.")
	}
}

// ---------- dialogs ----------

func entityEditorDialogs() {
	imguibackend.WithModalPopup("Open Entity Type##entopendlg", &entityShowOpenDialog, func() {
		if entityDefFileList == nil {
			entityDefFileList = world.ScanEntityDefs()
		}
		if len(entityDefFileList) == 0 {
			editorTextMutedWrapped("Nothing in " + world.EntitiesDirName + "/ yet. Save one first.")
		}
		for _, name := range entityDefFileList {
			if !imgui.Selectable(name) {
				continue
			}
			if err := entityLoadDef(name); err != nil {
				entityStatus = err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##entopencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Entity Type As##entsavedlg", &entityShowSaveAsDialog, func() {
		imgui.InputTextV("Type id##entsavename", &entitySaveAsName, 0, nil)
		editorTextMutedWrapped("Written to " + world.EntitiesDirName + "/<id>" + entityDefFileSuffix)
		if imgui.Button("Save##entsavego") && entitySaveAsName != "" {
			entitySaveDefAs(entitySaveAsName)
			entityDefFileList = nil
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##entsavecancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Import Chunk##entimportdlg", &entityShowImportChunk, entityImportChunkDialog)

	// The type's brushes are painted the same way the level's are, through this
	// window's own copy of the form.
	drawBrushUnwrapDialog()
}

// entityImportChunkDialog offers the two ways existing geometry gets into a
// type: a chunk file under chunks/, or a chunk of the level open in the main
// window.
//
// Both COPY. The type owns its brushes from then on, so editing them here does
// not reach back into the level or the file they came from, and editing the
// level does not reach forward into the type. That is the whole difference from
// the chunk attachment this replaces, and it is what makes a type one file.
func entityImportChunkDialog() {
	if entityDef == nil {
		return
	}
	imgui.Text("From " + levelio.ChunksDirName + "/")
	if entityChunkFileList == nil {
		entityChunkFileList = levelio.ScanChunkNames()
	}
	if len(entityChunkFileList) == 0 {
		editorTextMutedWrapped("Nothing there yet.")
	}
	for _, name := range entityChunkFileList {
		if !imgui.Selectable(name) {
			continue
		}
		if err := entityImportChunkFile(name); err != nil {
			entityStatus = err.Error()
		}
		imgui.CloseCurrentPopup()
	}

	imgui.Separator()
	imgui.Text("From the open level")
	lvl := Editor.level
	if lvl == nil || len(lvl.Chunks) == 0 {
		editorTextMutedWrapped("No level open, or it has no chunks.")
	}
	if lvl != nil {
		for i := range lvl.Chunks {
			ch := &lvl.Chunks[i]
			if ch.FromEntity != "" {
				// Already some entity's geometry — importing it would make a
				// second copy of a thing that already has an owner.
				continue
			}
			if !imgui.Selectable(ch.ID + "##lvlchunk" + strconv.Itoa(i)) {
				continue
			}
			entityImportLevelChunk(ch)
			imgui.CloseCurrentPopup()
		}
	}

	imgui.Separator()
	if imgui.Button("Cancel##entimportcancel") {
		imgui.CloseCurrentPopup()
	}
}

// entityImportLevelChunk copies a chunk out of the open level into the type.
//
// It goes through chunks/ rather than straight across, which looks like a
// detour and is not: the export is the thing that makes the geometry available
// to a second type and to a second level, and it costs one file write on a path
// an author takes once. The chunk's own origin is dropped on the way — it was
// where the thing sat in that level, and what the type wants is where it sits
// within itself. Its authored coordinates are the only answer that is not
// arbitrary.
func entityImportLevelChunk(ch *world.Chunk) {
	name := entityUniqueChunkFileName(ch.ID)
	if err := levelio.SaveChunk(ch, levelio.ChunkFilePath(name)); err != nil {
		entityStatus = err.Error()
		return
	}
	entityChunkFileList = nil
	if err := entityImportChunkFile(name); err != nil {
		entityStatus = err.Error()
		return
	}
	entityStatus = fmt.Sprintf("Lifted %s into %s/%s%s and copied it in",
		ch.ID, levelio.ChunksDirName, name, entityDefFileSuffix)
}

// entityUniqueChunkFileName avoids overwriting a chunk file someone else's type
// is already using. Exporting is a write to a shared directory, and silently
// replacing a file another definition points at would change that type too.
func entityUniqueChunkFileName(base string) string {
	existing := map[string]bool{}
	for _, n := range levelio.ScanChunkNames() {
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

// ---------- vehicle ----------

// entityPanelVehicle draws the vehicle block on the *type* — the thing the
// level editor's version of this panel could only ever set on one placement.
//
// The seats themselves are in the outline, because a seating plan is a list you
// add to and pick from rather than a field; what is here is what applies to the
// whole vehicle.
func entityPanelVehicle() {
	if entityDef.Vehicle == nil {
		editorTextWrapped("Not a vehicle. Making one gives it seats to sit in, a drive " +
			"model, and lets its chunk attachments be walkable decks.")
		if imgui.Button("Make this a vehicle##entdefvehadd") {
			EntityDefPushUndo()
			entityDef.Vehicle = &world.VehicleSpec{
				Mode:  world.VehicleGround,
				Drive: world.DefaultDriveSpec(world.VehicleGround),
				Seats: []world.VehicleSeat{{
					Name: "driver", Driver: true, Eye: 0.7,
					Exit: mgl32.Vec3{1.5, 0, 0},
				}},
			}
			entityMarkDirty()
		}
		return
	}
	v := entityDef.Vehicle

	mode := int32(v.Mode)
	if imgui.Combo("Mode##entdefvehmode", &mode, world.VehicleModeNames) {
		EntityDefPushUndo()
		newMode := world.VehicleMode(mode)
		// Re-default the tuning when the mode changes, unless the author has
		// already moved it away from the old mode's defaults. Silently keeping a
		// boat's drag on a spacecraft is a worse surprise than re-defaulting.
		if v.Drive == world.DefaultDriveSpec(v.Mode) {
			v.Drive = world.DefaultDriveSpec(newMode)
		}
		v.Mode = newMode
		entityMarkDirty()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("ground: steers and stays upright.\nair: pitch and roll, with lift.\n" +
			"water: like ground, floating.\nspace: full 6DOF, no gravity or drag.")
	}

	// Whether there is anywhere to stand, which is now simply whether the type
	// has brushes. It used to be a per-attachment tick box, because a type
	// could carry several chunks and only some of them were decks; a type has
	// one solid now, and a solid you can stand inside is one you can stand
	// inside. See world/entity_brush.go.
	if entityBrushCount() == 0 {
		editorTextMutedWrapped("Seats only — no walkable deck. Give the type brushes and an occupant can stand up and walk around inside them while it moves.")
	} else {
		imgui.Text(fmt.Sprintf("Walkable: this type's %d brush(es).", entityBrushCount()))
	}
	if len(v.Interiors) > 0 {
		// Left over from the chunk-attachment era, when a deck was named. The
		// list means nothing now and saying so is better than silently
		// dropping something an author put there.
		editorTextWrapped("This type still names decks (" + strings.Join(v.Interiors, ", ") +
			") from before its geometry moved onto the type itself. They are ignored; its brushes are its interior.")
	}

	if imgui.Button("Not a vehicle##entdefvehdel") {
		EntityDefPushUndo()
		entityDef.Vehicle = nil
		if entitySel.Kind == entitySelSeat {
			entitySel = entityEditorSelection{}
		}
		entityMarkDirty()
		return
	}

	editorVehicleDriveTuning(v)
}

// animSettings is the two overridable fields flattened to comparable values.
//
// Flattened rather than compared as the pointers they are, because
// editorEntityBlendField writes *through* its pointer — a copy of the struct
// taken beforehand would share that float and compare equal however far the
// slider moved. The set/unset flag rides alongside so "follow the clip" and an
// explicit value stay distinguishable.
type animSettings struct {
	hasLoop, loop bool
	hasBlend      bool
	blend         float32
}

func animationSettings(a *world.EntityAnimation) animSettings {
	s := animSettings{hasLoop: a.Loop != nil, hasBlend: a.BlendTime != nil}
	if s.hasLoop {
		s.loop = *a.Loop
	}
	if s.hasBlend {
		s.blend = *a.BlendTime
	}
	return s
}
