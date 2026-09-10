package editor

// editor_entity_context_menu.go — the entity editor's right-click menu.
//
// The third viewport to grow one, and deliberately the same one: the rows are
// ctxMenuItems, the popup is drawn by ctxMenuFrame (editor_context_menu.go),
// and the gesture that opens it is the gesture the other two use — a right
// drag turns the camera, a right press and release without travel opens this.
// A window that edits brushes with the level editor's own tools and then made
// them unreachable by the level editor's own gesture would be the odd one out
// twice over.
//
// What is here is only the half that is about a *type*: which rows a brush, a
// face, a corner, a model attachment or a seat gets. The actions themselves are
// the shared ones, resolving through ws() — so "Extrude", "Texture" and
// "Duplicate" here are the same functions the 3D view's menu calls, acting on
// this window's workspace because this window has made it current.

import "fmt"

// entityCtxPopupID names the popup; "##" because a popup's name is also its
// title, and this one has no title bar to show it in.
const entityCtxPopupID = "##entityctxmenu"

// entityRightPress is this viewport's right-button gesture.
var entityRightPress ctxGesture

// ---------- input glue ----------

// entityContextPressed arms the gesture at a right-button press on the viewport.
func entityContextPressed(mx, my int32) { entityRightPress.press(mx, my) }

// entityContextMoved is fed every motion event; travel makes the gesture a look.
func entityContextMoved(mx, my int32) { entityRightPress.motion(mx, my) }

// entityContextReleased ends the gesture, opening the menu when it was a click
// rather than a camera turn. Reports which of the two it was read as, which is
// what lets a test ask without an ImGui frame.
func entityContextReleased(mx, my int32) bool {
	if !entityRightPress.release() {
		return false
	}
	entityOpenContextMenu(mx, my)
	return true
}

// entityOpenContextMenu queues the menu at (mx, my).
//
// A click on empty air with nothing selected would open a menu of greyed-out
// rows, so it first tries to select what it landed on — and only then, because
// right-clicking beside a careful multi-face selection must not throw it away.
// While the stamp is armed it picks nothing at all: the click that resolves a
// placement is the left one.
func entityOpenContextMenu(mx, my int32) {
	if entityDef == nil {
		return
	}
	if !entityHasSelection() && !entityWS.BrushPlaceActive {
		entityGeometryClick(mx, my, false)
	}
	entityRightPress.open(mx, my)
}

// ---------- rendering ----------

// entityContextMenu draws the popup. Called once per frame from the window's
// draw, after the panels, so its rows are built from the state they show.
func entityContextMenu() {
	if entityDef == nil {
		return
	}
	ctxMenuFrame(entityCtxPopupID, &entityRightPress, entityContextMenuItems)
}

// ---------- what is selected ----------

// entityCtxSelKind is what the menu is about. The geometry selections come
// first because they are the ones a viewport click makes; the outline's own
// kinds are reached by clicking a row in the list.
type entityCtxSelKind int

const (
	entityCtxNone entityCtxSelKind = iota
	entityCtxVertices
	entityCtxFaces
	entityCtxBrush
	entityCtxModel
	entityCtxEmitter
	entityCtxSeat
)

// entityCtxSelection reports what the menu should be about.
func entityCtxSelection() entityCtxSelKind {
	if entityDef == nil {
		return entityCtxNone
	}
	switch {
	case len(entityWS.SelectedVertices) > 0:
		return entityCtxVertices
	case len(entityWS.SelectedFaces) > 0:
		if faceSelFace(entityWS.SelectedFaces[0]) != nil {
			return entityCtxFaces
		}
	case len(entityWS.SelectedBrushes) > 0:
		if brushSelValid(entityWS.SelectedBrushes[0]) {
			return entityCtxBrush
		}
	}
	switch entitySel.Kind {
	case entitySelModel:
		if entitySel.Idx < len(entityDef.Models) {
			return entityCtxModel
		}
	case entitySelEmitter:
		if entitySel.Idx < len(entityDef.Emitters) {
			return entityCtxEmitter
		}
	case entitySelSeat:
		if entityDef.Vehicle != nil && entitySel.Idx < len(entityDef.Vehicle.Seats) {
			return entityCtxSeat
		}
	}
	return entityCtxNone
}

func entityHasSelection() bool { return entityCtxSelection() != entityCtxNone }

// entityCtxSummary names the selection for the menu's heading.
func entityCtxSummary() string {
	switch entityCtxSelection() {
	case entityCtxVertices:
		if n := len(entityWS.SelectedVertices); n > 1 {
			return fmt.Sprintf("%d corners", n)
		}
		return fmt.Sprintf("Corner #%d", entityWS.SelectedVertices[0].vertIdx)

	case entityCtxFaces:
		if n := selectedFaceCount(); n > 1 {
			return fmt.Sprintf("%d faces", n)
		}
		primary := entityWS.SelectedFaces[0]
		if _, b := faceSelBrush(primary); b != nil {
			return fmt.Sprintf("Face #%d — brush %s", primary.faceIdx, b.ID)
		}
		return fmt.Sprintf("Face #%d", primary.faceIdx)

	case entityCtxBrush:
		if n := len(entityWS.SelectedBrushes); n > 1 {
			return fmt.Sprintf("%d brushes", n)
		}
		if b := entityBrushAt(entityWS.SelectedBrushes[0].brushIdx); b != nil {
			return "Brush " + b.ID
		}
		return "Brush"

	case entityCtxModel:
		return "Model " + entityDef.Models[entitySel.Idx].Name

	case entityCtxEmitter:
		return "Emitter " + entityDef.Emitters[entitySel.Idx].Name

	case entityCtxSeat:
		return "Seat " + entityDef.Vehicle.Seats[entitySel.Idx].Name
	}
	return "Nothing selected"
}

// ---------- the menu ----------

// entityContextMenuItems builds the menu for the current selection, in the
// shape the other two viewports use: a heading saying what it is about, the
// rows belonging to that kind of selection, then the blocks every selection
// shares — what to add, which mode, the view toggles, and undo.
func entityContextMenuItems() []ctxMenuItem {
	if entityDef == nil {
		return nil
	}
	items := []ctxMenuItem{{Label: entityCtxSummary()}, {Separator: true}}

	switch entityCtxSelection() {
	case entityCtxVertices:
		items = append(items, entityCtxVertexItems()...)
	case entityCtxFaces:
		items = append(items, entityCtxFaceItems()...)
	case entityCtxBrush:
		items = append(items, entityCtxBrushItems()...)
	case entityCtxModel:
		items = append(items, entityCtxModelItems()...)
	case entityCtxEmitter:
		items = append(items, entityCtxEmitterItems()...)
	case entityCtxSeat:
		items = append(items, entityCtxSeatItems()...)
	default:
		items = append(items, ctxMenuItem{Label: entityCtxEmptyHint()})
	}

	items = append(items, ctxMenuItem{Separator: true})
	items = append(items, entityCtxSharedItems()...)
	items = append(items, ctxMenuItem{Separator: true})
	items = append(items,
		ctxMenuItem{Label: "Undo", Shortcut: "Ctrl+Z", Disabled: !EntityDefCanUndo(), Do: EntityDefUndo},
		ctxMenuItem{Label: "Redo", Shortcut: "Ctrl+Shift+Z", Disabled: !EntityDefCanRedo(), Do: EntityDefRedo},
	)
	return items
}

// ---------- faces ----------

// entityCtxFaceItems is the menu for a face selection: the texture, the three
// operations that reshape a solid without breaking its convexity, and the four
// flags a face carries. The same rows the 3D view's surface menu offers,
// because they are the same faces — a hold's wall is a wall.
func entityCtxFaceItems() []ctxMenuItem {
	r, ok := editor3DSurfaceRefs()
	if !ok {
		return nil
	}
	texture := ""
	if r.Texture != nil {
		texture = *r.Texture
	}
	multi := selectedFaceCount() > 1
	all := func(label string) string {
		if multi {
			return fmt.Sprintf("%s (%d faces)", label, selectedFaceCount())
		}
		return label
	}

	items := []ctxMenuItem{
		{Label: all("Texture"), Sub: ctxTextureMenuItems(texture, true, entityCtxSetFaceTexture)},
		{Separator: true},
	}
	items = append(items, entityCtxWrapFaceOps(editor3DFaceOpMenuItems(all))...)
	items = append(items, []ctxMenuItem{
		{Separator: true},
		{Label: all("Drawn"), Checked: faceDrawn(r.f), Do: entityCtxToggleFaceDrawn,
			Tooltip: "Off makes the face invisible while the brush still collides —\nfor a side buried against a neighbour."},
		{Label: all("Smooth"), Checked: r.Smooth != nil && *r.Smooth,
			Do: func() {
				editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Smooth })
				entityCtxGeometryEdited()
			},
			Tooltip: "Weld the normals along shared edges, so neighbouring\nfaces read as one curved surface."},
		{Label: all("Double sided"),
			Checked: r.DoubleSided != nil && *r.DoubleSided,
			Do: func() {
				editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.DoubleSided })
				entityCtxGeometryEdited()
			}},
		{Label: all("Collision"), Checked: faceSolid(r.f), Do: entityCtxToggleFaceSolid,
			Tooltip: "Off makes this one face walk-through while the rest of the\n" +
				"brush stays solid — a doorway in a hull the player boards through."},
	}...)
	if multi {
		items = append(items, ctxMenuItem{
			Label: "(ticks show the first face; a row sets them all to match it)"})
	}

	if editorInDetailMode() {
		return append(items,
			ctxMenuItem{Separator: true},
			ctxMenuItem{Label: "Select this brush's detail faces", Do: entityCtxSelectBrushFaces},
			ctxMenuItem{Label: "Select this brush's detail points", Do: entityCtxSelectBrushVertices},
			ctxMenuItem{Label: "Deselect", Do: entityClearGeometrySelection},
			ctxMenuItem{Separator: true},
			ctxMenuItem{Label: "Leave detail mode", Shortcut: "Tab", Do: entityCtxExitDetailMode})
	}
	return append(items,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Hollow this brush", Disabled: multi, Do: entityCtxHollowBrush,
			Tooltip: "Replace the solid with the six brushes of a room filling it —\n" +
				"the quick way to turn a blocked-out hull into one with a hold."},
		ctxMenuItem{Label: "Duplicate brush", Disabled: multi, Do: entityCtxDuplicateBrush,
			Tooltip: "A copy in place, selected and ready to drag off the original."},
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Select this brush's sides", Do: entityCtxSelectBrushSideFaces,
			Tooltip: "The upright faces only — the usual case, since a solid's sides\nwant one texture and its top and bottom another."},
		ctxMenuItem{Label: "Select this brush's faces", Do: entityCtxSelectBrushFaces},
		ctxMenuItem{Label: "Select this brush's corners", Do: entityCtxSelectBrushVertices,
			Tooltip: "Every corner, so a drag moves the whole solid."},
		ctxMenuItem{Label: "Deselect", Do: entityClearGeometrySelection},
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Delete brush", Shortcut: "Del", Do: entityDeleteSelectedBrushes},
	)
}

// ---------- corners ----------

func entityCtxVertexItems() []ctxMenuItem {
	return []ctxMenuItem{
		{Label: "Select this brush's corners", Do: entityCtxSelectBrushVertices,
			Tooltip: "Every corner, so a drag moves the whole solid."},
		{Label: "Deselect", Do: entityClearGeometrySelection},
	}
}

// ---------- a whole brush ----------

// entityCtxBrushItems is the menu for an object-mode brush selection. Short for
// the reason the 3D view's is: nearly everything a brush can be asked is a
// property of one of its faces, and the useful thing to offer here is the way
// in to edit mode plus the few actions that are about the solid itself.
func entityCtxBrushItems() []ctxMenuItem {
	n := len(entityWS.SelectedBrushes)
	all := func(label string) string {
		if n > 1 {
			return fmt.Sprintf("%s (%d brushes)", label, n)
		}
		return label
	}
	return []ctxMenuItem{
		{Label: "Edit this brush", Shortcut: "Tab", Do: func() { entityCtxSetEditContext(editorEditMode) },
			Tooltip: "Go into edit mode, where a click picks a face, an edge or a\ncorner and E/I/B reshape it."},
		{Label: "Detail this brush", Shortcut: "Ctrl+Shift+click", Disabled: n > 1,
			Do:      entityCtxEnterDetailMode,
			Tooltip: "Shape the surface drawn over this brush without moving the\nsolid the player collides with."},
		{Separator: true},
		{Label: "Duplicate", Disabled: n > 1, Do: entityCtxDuplicateBrush,
			Tooltip: "A copy in place, selected and ready to drag off the original."},
		{Label: "Hollow this brush", Disabled: n > 1, Do: entityCtxHollowBrush,
			Tooltip: "Replace the solid with the six brushes of a room filling it —\nfloor, ceiling and four walls."},
		{Separator: true},
		{Label: all("Delete"), Shortcut: "Del", Do: entityDeleteSelectedBrushes},
	}
}

// ---------- the outline's own kinds ----------

// The two outline kinds get one row each: everything else about a model
// attachment or a seat is a field to type a number into, which is the panel's
// job. Framing and the view toggles are in the shared block below.
func entityCtxModelItems() []ctxMenuItem {
	idx := entitySel.Idx
	return []ctxMenuItem{
		{Label: "Remove model", Shortcut: "Del", Do: func() { entityRemoveModel(idx) },
			Tooltip: "Takes the attachment off the type. The .fmdl on disk is untouched."},
	}
}

func entityCtxEmitterItems() []ctxMenuItem {
	idx := entitySel.Idx
	m := &entityDef.Emitters[idx]
	return []ctxMenuItem{
		{Label: "Restart", Do: func() { entityEmitterRestart(idx) },
			Tooltip: "Clear what is in flight and rewind, for judging a burst."},
		{Label: "Edit effect...", Disabled: m.Effect == "", Do: func() { OpenParticleEditorOn(m.Effect) },
			Tooltip: "Open this emitter's .fpar in the particle editor."},
		{Separator: true},
		{Label: "Remove emitter", Shortcut: "Del", Do: func() { entityRemoveEmitter(idx) },
			Tooltip: "Takes the effect off the type. The .fpar on disk is untouched."},
	}
}

func entityCtxSeatItems() []ctxMenuItem {
	idx := entitySel.Idx
	return []ctxMenuItem{
		{Label: "Remove seat", Shortcut: "Del", Do: func() { entityRemoveSeat(idx) }},
	}
}

// entityCtxEmptyHint says what a click would pick up, which depends on the
// context the window is in — the same three the 3D view's empty menu names.
func entityCtxEmptyHint() string {
	switch {
	case editorInDetailMode():
		return "Detail mode — click the surface; E extrudes, B subdivides, Del removes (Tab or Escape to leave)"
	case editorInEditMode():
		return "Edit mode — click a face, an edge or a corner to select it (Tab for object mode)"
	}
	return "Object mode — click a brush, or a model, emitter or seat in the outline (Tab to edit a brush)"
}

// ---------- shared ----------

// entityCtxSharedItems is the block every selection gets: what to add, which
// mode a click addresses, and the view toggles. Kept in a fixed place at the
// bottom for the reason the other two menus keep theirs there — muscle memory
// only works if the row is where it was last time.
func entityCtxSharedItems() []ctxMenuItem {
	modes := []ctxMenuItem{
		{
			Label:    "Object",
			Shortcut: "Tab",
			Checked:  !editorInEditMode() && !editorInDetailMode(),
			Do:       func() { entityCtxSetEditContext(editorObjectMode) },
			Tooltip:  "Click picks up a whole brush; the gizmo moves, turns and scales all of it.",
		},
		{
			Label:    "Detail",
			Shortcut: "Ctrl+Shift+click",
			Checked:  editorInDetailMode(),
			Do:       func() { entityCtxSetEditContext(editorDetailMode) },
			Tooltip: "Shape the mesh drawn over one brush's surface. Collision stays\n" +
				"with the solid underneath, which is drawn as a wire cage.",
		},
		{Separator: true},
	}
	for _, mode := range editorSelModes {
		m := mode
		modes = append(modes, ctxMenuItem{
			Label:    m.String(),
			Shortcut: editorSelModeKey(m),
			Checked:  editorInEditMode() && entityWS.SelMode == m,
			Do: func() {
				entityCtxSetEditContext(editorEditMode)
				editorSetSelMode(m)
			},
			Tooltip: editorSelModeTooltip(m),
		})
	}

	return []ctxMenuItem{
		entityCtxStampMenuItem(),
		{Label: "Add model", Do: entityAddModel,
			Tooltip: "Attach an .fmdl to the type. Pick which one in the panel."},
		{Label: "Add emitter", Do: entityAddEmitter,
			Tooltip: "Hang a particle effect on the type. Pick which .fpar, and\n" +
				"where it hangs, in the panel."},
		{Label: "Add seat", Disabled: entityDef.Vehicle == nil, Do: entityAddSeat,
			Tooltip: "Somewhere for a rider to sit. Greyed out until the type is a\nvehicle, which the panel's Vehicle group makes it."},
		{Label: "Select", Sub: modes},
		{Separator: true},
		{Label: "Wireframe", Shortcut: "Z", Checked: entityWS.Wireframe, Do: entityCtxToggleWireframe,
			Tooltip: "Solid hides the far side of the hull, and hidden points and\nedges cannot be clicked. Wireframe shows all of it."},
		{Label: "Bounding box", Shortcut: "B", Checked: entityShowBounds,
			Do: func() { entityShowBounds = !entityShowBounds }},
		{Label: "Grid", Shortcut: "Shift+G", Checked: entityShowGrid,
			Do: func() { entityShowGrid = !entityShowGrid }},
		{Label: "Play animations", Shortcut: "P", Checked: entityPlayAnims,
			Do: func() { entityPlayAnims = !entityPlayAnims }},
		{Separator: true},
		{Label: "Orbit camera", Shortcut: "V", Checked: !entityCam.Fly,
			Do:      func() { entityCam.setFly(!entityCam.Fly) },
			Tooltip: "Turn around the pivot instead of flying. WASD, Space and\nShift slide the pivot; F frames the type and puts it there."},
		{Label: "Frame the type", Shortcut: "F", Do: entityCam.frame},
		{Label: "Snap to grid while dragging", Checked: entityWS.SnapToGrid,
			Do: func() { entityWS.SnapToGrid = !entityWS.SnapToGrid }},
	}
}

// entityCtxStampMenuItem is editorBrushStampMenuItem's row for this window.
//
// Its own rather than the shared one because a type with no geometry yet has no
// chunk for a brush to live in: the shared row greys itself out and says to make
// one, and here there is nothing to make one *in*. entityArmBrushStamp seeds the
// scratch chunk instead, which is what turns "add a box" into the first box.
func entityCtxStampMenuItem() ctxMenuItem {
	if entityWS.BrushPlaceActive {
		return ctxMenuItem{
			Label:   "Stop stamping " + brushKindLabel(entityWS.BrushPlaceKind) + " brushes",
			Do:      editorDisarmBrushPlacement,
			Tooltip: "Put the brush stamp away. Escape does the same.",
		}
	}
	kinds := make([]ctxMenuItem, 0, len(brushKinds))
	for _, kind := range brushKinds {
		k := kind
		kinds = append(kinds, ctxMenuItem{Label: brushKindLabel(k), Do: func() { entityArmBrushStamp(k) }})
	}
	return ctxMenuItem{
		Label:   "Stamp brushes",
		Sub:     kinds,
		Tooltip: "A box follows the cursor and every click places another\nsolid. Stays on until Escape.",
	}
}

// ---------- the actions, with this window's bookkeeping ----------
//
// Every row above that edits geometry goes through one of these rather than
// calling the shared action directly. What they add is the two things the level
// editor's own menu has no need of: marking the document dirty, and telling the
// window that what it derives from the brushes — the bounds cage and the
// framing box — is stale. Missing either shows up as a Save that writes the
// state before the edit.

func entityCtxGeometryEdited() {
	entityGeometryEdited()
	entityMarkDirty()
}

func entityCtxSetFaceTexture(name string) {
	editor3DSetSurfaceTexture(name)
	entityCtxGeometryEdited()
}

func entityCtxToggleFaceDrawn() {
	editor3DToggleFaceDrawn()
	entityCtxGeometryEdited()
}

func entityCtxToggleFaceSolid() {
	editor3DToggleFaceSolid()
	entityCtxGeometryEdited()
}

// entityCtxWrapFaceOps re-points the shared face operations' rows at this
// window's bookkeeping. The rows themselves are the 3D view's — one list of
// what a face can be asked, not two that drift.
func entityCtxWrapFaceOps(items []ctxMenuItem) []ctxMenuItem {
	for i := range items {
		do := items[i].Do
		if do == nil {
			continue
		}
		items[i].Do = func() {
			do()
			entityCtxGeometryEdited()
		}
	}
	return items
}

func entityCtxDuplicateBrush() {
	editorDuplicateSelectedBrush()
	entityBrushSelectionSync()
	entityCtxGeometryEdited()
}

func entityCtxHollowBrush() {
	editorHollowSelectedBrush()
	entityBrushSelectionSync()
	entityCtxGeometryEdited()
}

func entityCtxSelectBrushFaces() {
	editor3DSelectBrushFaces()
	entityBrushSelectionSync()
}

func entityCtxSelectBrushSideFaces() {
	editor3DSelectBrushSideFaces()
	entityBrushSelectionSync()
}

func entityCtxSelectBrushVertices() {
	editor3DSelectBrushVertices()
	entityBrushSelectionSync()
}

func entityCtxSetEditContext(c editorEditContext) {
	editorSetEditContext(c)
	entityBrushSelectionSync()
	entityStatus = "Edit context: " + entityWS.EditContext.String()
}

func entityCtxEnterDetailMode() {
	editorEnterDetailModeOnSelection()
	entityBrushSelectionSync()
}

func entityCtxExitDetailMode() {
	editorExitDetailMode()
	entityBrushSelectionSync()
	entityStatus = "Left detail mode"
}

func entityCtxToggleWireframe() {
	entityWS.Wireframe = !entityWS.Wireframe
	entityWireOverlay = entityWS.Wireframe
}
