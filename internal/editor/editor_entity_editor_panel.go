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
	imgui.Checkbox("Chunks##entshowc", &entityShowChunks)
	imgui.SameLine()
	imgui.Checkbox("Seats##entshows", &entityShowSeats)
	imgui.SameLine()
	imgui.Checkbox("Wire##entwire", &entityWireOverlay)
	imgui.SameLine()
	imgui.Checkbox("Play [Space]##entplay", &entityPlayAnims)
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
		imgui.SetTooltip("WASD/QE to move, right-drag to look — for getting\ninside a hull rather than orbiting it.")
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
		entityOutlineChunks()
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

func entityOutlineChunks() {
	imgui.Text("Chunks")
	for i := range entityDef.Chunks {
		c := &entityDef.Chunks[i]
		imgui.PushID(fmt.Sprintf("outc%d", i))
		label := c.Name
		switch {
		case c.File == "":
			label += "  (no file)"
		case c.Disabled:
			label += "  (off)"
		}
		if imgui.SelectableV(label, entitySel.Kind == entitySelChunk && entitySel.Idx == i, 0, imgui.Vec2{}) {
			entitySel = entityEditorSelection{Kind: entitySelChunk, Idx: i}
		}
		imgui.PopID()
	}
	if len(entityDef.Chunks) == 0 {
		editorTextMutedWrapped("None. A chunk gives the type floors to stand on and walls to bump into, carried wherever it goes.")
	}
	if imgui.Button("Add Chunk##entaddc") {
		entityAddChunk()
	}
	imgui.SameLine()
	if imgui.Button("Import##entimpc") {
		entityShowImportChunk = true
		entityChunkFileList = nil
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Attach a chunk from " + levelio.ChunksDirName + "/, or lift one\n" +
			"out of the level open in the main window.")
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
	case entitySelChunk:
		if entitySel.Idx < len(entityDef.Chunks) {
			editorPropGroupOpen("Chunk: "+entityDef.Chunks[entitySel.Idx].Name+"##entselc",
				func() { entityPanelChunk(entitySel.Idx) })
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

func entityPanelChunk(idx int) {
	c := &entityDef.Chunks[idx]
	name := c.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##entcname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		entityRenameChunkAttachment(c, name)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Part of the chunk's ID in a level (`<entity>.<name>`),\n" +
			"so a script addresses this deck by it.")
	}
	imgui.SameLine()
	if imgui.Button("Delete##entcdel") {
		entityRemoveChunk(idx)
		return
	}

	file := c.File
	if entityChunkFileCombo("File##entcfile", &file) {
		entitySetChunkFile(c, file)
	}
	if c.File == "" {
		editorTextMutedWrapped("No file: this attachment carries no geometry. Import one, or export a chunk from the level editor first.")
	}

	off := [3]float32{c.Offset.X(), c.Offset.Y(), c.Offset.Z()}
	if editorDragFloat3Step("Offset##entcoff", &off, -10000, 10000, "%.3f", 0.05) {
		entityBeginEdit("coff")
		c.Offset = mgl32.Vec3{off[0], off[1], off[2]}
		entityPreviewInvalidate()
	}
	rot := mgl32.RadToDeg(c.Rotation)
	if editorDragFloatStep("Rotation (deg)##entcrot", &rot, -360, 360, "%.1f", 1) {
		entityBeginEdit("crot")
		c.Rotation = mgl32.DegToRad(rot)
		entityPreviewInvalidate()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("About Y only — a chunk's own placement has never had\nmore, and the entity's rotation carries the rest.")
	}
	scale := c.EffectiveScale()
	if editorDragFloatStep("Scale##entcscale", &scale, 0.01, 100, "%.3f", 0.01) {
		entityBeginEdit("cscale")
		c.Scale = scale
		entityPreviewInvalidate()
	}
	if imgui.Checkbox("Starts disabled##entcdis", &c.Disabled) {
		entityBeginEdit("cdis")
		entityPreviewInvalidate()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Not drawn, not collided with, never uploaded until a\nscript sets `@<entity>." + c.Name + ".prop.enabled = true`.")
	}

	if entityDef.Vehicle != nil {
		aboard := entityDef.Vehicle.CarriesChunk(c.Name)
		if imgui.Checkbox("Walkable interior##entcint", &aboard) {
			EntityDefPushUndo()
			if aboard {
				entityDef.Vehicle.AddInterior(c.Name)
			} else {
				entityDef.Vehicle.RemoveInterior(c.Name)
			}
			entityMarkDirty()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("An occupant can stand up and walk around on this deck\nwhile the vehicle moves.")
		}
	}

	if c.File != "" {
		editorTextMutedWrapped("Placing an instance of this type materialises " +
			world.EntityChunkID("<entity>", c.Name) + " into the level, bound to its frame. " +
			"The level file does not store the geometry — this attachment does.")
	}
}

// entityRenameChunkAttachment renames an attachment, carrying the vehicle's
// interior binding across with it.
//
// The binding names the attachment, so a rename that did not update it would
// silently turn a walkable deck into one you fall through — the kind of thing
// that is noticed a level later.
func entityRenameChunkAttachment(c *world.EntityChunk, name string) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		entityStatus = "A chunk attachment needs a name"
		return
	case name == c.Name:
		return
	}
	for i := range entityDef.Chunks {
		if &entityDef.Chunks[i] != c && entityDef.Chunks[i].Name == name {
			entityStatus = fmt.Sprintf("This type already has a chunk called %q", name)
			return
		}
	}
	EntityDefPushUndo()
	if entityDef.Vehicle != nil && entityDef.Vehicle.CarriesChunk(c.Name) {
		entityDef.Vehicle.RemoveInterior(c.Name)
		entityDef.Vehicle.AddInterior(name)
	}
	c.Name = name
	entityMarkDirty()
	entityPreviewInvalidate()
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
}

// entityImportChunkDialog offers the two ways geometry gets into a type: a file
// already under chunks/, or a chunk in the level open in the main window.
//
// The second is the one that matters in practice. A hull is built where hulls
// are easy to build — in the 2D map, with sectors and portals — and the step
// that used to be missing was carrying it over. Lifting one exports it to
// chunks/ and attaches it in the same click, so the file it leaves behind is a
// real chunk file that any other type can use too.
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
		entityAttachChunkFile(name)
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

// entityAttachChunkFile adds an attachment pointing at an existing chunk file.
func entityAttachChunkFile(file string) {
	EntityDefPushUndo()
	entityDef.Chunks = append(entityDef.Chunks, world.EntityChunk{
		Name:  world.UniqueDefChunkName(entityDef.Chunks, chunkAttachNameFor(file)),
		File:  file,
		Scale: 1,
	})
	entitySel = entityEditorSelection{Kind: entitySelChunk, Idx: len(entityDef.Chunks) - 1}
	entityMarkDirty()
	entityPreviewInvalidate()
	entityStatus = "Attached " + file
}

// entityImportLevelChunk exports a chunk out of the open level and attaches it.
//
// The chunk's own origin is dropped: it was where the thing sat in that level,
// and what an attachment wants is where it sits within the *entity*. Starting
// at the entity's origin is the only answer that is not arbitrary.
func entityImportLevelChunk(ch *world.Chunk) {
	name := entityUniqueChunkFileName(ch.ID)
	if err := levelio.SaveChunk(ch, levelio.ChunkFilePath(name)); err != nil {
		entityStatus = err.Error()
		return
	}
	entityChunkFileList = nil
	entityAttachChunkFile(name)
	entityStatus = fmt.Sprintf("Lifted %s into %s/%s%s",
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

	// The decks, read-only here: a chunk attachment is bound as an interior
	// from its own section, where the chunk it names is in front of you. Listing
	// them here is so "which of these can be walked on" is answerable without
	// clicking through every attachment.
	if len(v.Interiors) == 0 {
		editorTextMutedWrapped("Seats only — no walkable deck. Tick \"Walkable interior\" on a chunk attachment to add one.")
	} else {
		imgui.Text("Walkable decks: " + strings.Join(v.Interiors, ", "))
	}
	for _, name := range v.Interiors {
		if entityChunkAttachment(name) == nil {
			// A deck naming an attachment that has been deleted or renamed
			// would be a hole in the floor at runtime, and nothing else would
			// report it.
			editorTextWrapped("Deck \"" + name + "\" names no chunk attachment on this type.")
		}
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

// entityChunkAttachment finds an attachment by name on the document.
func entityChunkAttachment(name string) *world.EntityChunk {
	if entityDef == nil {
		return nil
	}
	for i := range entityDef.Chunks {
		if entityDef.Chunks[i].Name == name {
			return &entityDef.Chunks[i]
		}
	}
	return nil
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
