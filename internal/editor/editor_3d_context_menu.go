package editor

// editor_3d_context_menu.go — the 3D level editor's right-click menu.
//
// The same idea as the model editor's (editor_model_context_menu.go), on the
// same machinery (editor_context_menu.go): what the menu offers is decided by
// what is selected, so a wall is asked about textures and portals, a vertex
// about snapping, the spawn point about which way it faces, and empty space
// about what to put there.
//
// The 3D editor's four selections are mutually exclusive — clicking a vertex
// clears the spawn point, clicking a face clears the vertex — so the menu has
// one block per kind and never has to merge two of them, which is the one real
// difference from the model editor's, where a mesh selection and a bone
// selection are deliberately alive at once.
//
// Rows call into editor_3d_actions.go rather than editing the level here, so
// the menu and the side panel cannot disagree about what an action does or
// whether it is undoable.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/levelio"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// editor3DCtxPopupID names the popup; "##" because a popup's name is also its
// title, and this one has no title bar to show it in.
const editor3DCtxPopupID = "##editor3dctxmenu"

// editor3DRightPress is the viewport's right-button gesture: a drag turns the
// camera, a click without one opens this menu.
var editor3DRightPress ctxGesture

// editor3DMenuPoint is the world point the menu was opened on — where "place
// spawn point here" puts it. Recorded at the click because the menu is drawn a
// frame later, by which time the ray is gone.
var editor3DMenuPoint mgl32.Vec3

// ---------- input glue ----------

func editor3DContextPressed(mx, my int32) { editor3DRightPress.press(mx, my) }
func editor3DContextMoved(mx, my int32)   { editor3DRightPress.motion(mx, my) }

// editor3DContextCancel ends the gesture without opening anything — for a
// right-release the viewport never sees, because the cursor finished over the
// side panel. Without it the arm survives into the next gesture, and a later
// release could open a menu no press asked for.
func editor3DContextCancel() { editor3DRightPress.release() }

// editor3DContextReleased ends the gesture, opening the menu when it was a
// click rather than a camera turn. Returns whether it opened one.
func editor3DContextReleased(mx, my int32) bool {
	if !editor3DRightPress.release() {
		return false
	}
	editor3DOpenContextMenu(mx, my)
	return true
}

// editor3DOpenContextMenu queues the menu at (mx, my).
//
// With nothing selected the click first selects what it landed on, so a menu
// opened on a wall is about that wall. With something already selected it
// changes nothing — a right-click beside a careful vertex selection must not
// throw it away — and while a placement mode is armed it does not pick at all,
// since the click that resolves placement is the left one.
func editor3DOpenContextMenu(mx, my int32) {
	if Editor.level == nil {
		return
	}
	editor3DMenuPoint = editor3DPointAt(mx, my)
	if !editor3DHasSelection() && !editor3DPlacementArmed() {
		Editor3DHandleClick(sdl.MouseButtonEvent{
			Type:   sdl.MOUSEBUTTONDOWN,
			Button: sdl.ButtonLeft,
			X:      mx,
			Y:      my,
		})
	}
	editor3DRightPress.open(mx, my)
}

// editor3DPlacementArmed reports whether a left-click would currently place
// something rather than select it.
func editor3DPlacementArmed() bool {
	return Editor.SpawnPlacementMode || Editor.EntityPlacementMode ||
		Editor.VolumePlacementMode || Editor.BrushActive
}

// editor3DPointAt is where the cursor's ray meets the world — the surface it
// hits, or a short way out in front when it hits nothing, so "here" still means
// something over open space.
func editor3DPointAt(mx, my int32) mgl32.Vec3 { return workspacePointAt(mx, my) }

// workspacePointAt is that, through whichever workspace is active — so the
// brush stamp's ghost lands where the cursor meets the geometry in the entity
// editor's viewport as readily as in the level editor's.
func workspacePointAt(mx, my int32) mgl32.Vec3 {
	origin, dir := ws().ray(mx, my)
	if hit := pick3D(mx, my); hit.kind != pickNone {
		return origin.Add(dir.Mul(hit.t))
	}
	return origin.Add(dir.Mul(4))
}

// editor3DContextMenu draws the popup. Called once per frame from the editor's
// UI pass, after the side panel, so its rows are built from the state the panel
// is showing.
func editor3DContextMenu() {
	if Editor.level == nil || Editor.Mode != Mode3D {
		return
	}
	ctxMenuFrame(editor3DCtxPopupID, &editor3DRightPress, editor3DContextMenuItems)
}

// ---------- the menu ----------

// editor3DContextMenuItems builds the menu for the current selection: a heading
// saying what it is about, the block belonging to that kind of selection, then
// the blocks every selection shares — what to add, which tool, and undo.
func editor3DContextMenuItems() []ctxMenuItem {
	if Editor.level == nil {
		return nil
	}
	items := []ctxMenuItem{{Label: editor3DSelectionSummary()}, {Separator: true}}

	switch editor3DSelection() {
	case sel3DSpawn:
		items = append(items, editor3DSpawnMenuItems()...)
	case sel3DSurface:
		items = append(items, editor3DSurfaceMenuItems()...)
	case sel3DBrush:
		items = append(items, editor3DBrushMenuItems()...)
	case sel3DVertices:
		items = append(items, editor3DVertexMenuItems()...)
	case sel3DEntity:
		items = append(items, editor3DEntityMenuItems()...)
	case sel3DVolume:
		items = append(items, editor3DVolumeMenuItems()...)
	case sel3DPrefab:
		items = append(items, editor3DPrefabMenuItems()...)
	case sel3DSprite:
		items = append(items, editor3DSpriteMenuItems()...)
	case sel3DScenery:
		items = append(items, editor3DSceneryMenuItems()...)
	default:
		items = append(items, editor3DEmptyMenuItems()...)
	}

	items = append(items, ctxMenuItem{Separator: true})
	items = append(items, editor3DSharedMenuItems()...)
	items = append(items, ctxMenuItem{Separator: true})
	items = append(items,
		ctxMenuItem{Label: "Undo", Shortcut: "Ctrl+Z", Disabled: !EditorCanUndo(), Do: EditorUndo},
		ctxMenuItem{Label: "Redo", Shortcut: "Ctrl+Shift+Z", Disabled: !EditorCanRedo(), Do: EditorRedo},
	)
	return items
}

// ---------- spawn point ----------

func editor3DSpawnMenuItems() []ctxMenuItem {
	if editor3DActiveSpawn() == nil {
		return nil
	}
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusSpawn) },
			Tooltip: "Open the spawn point's section of the side panel."},
		{Label: "Face the camera", Do: editor3DFaceCamera,
			Tooltip: "Point the marker where you are looking — including the\npitch, which placing it only takes the yaw of."},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// ---------- brush (object mode) ----------

// editor3DBrushMenuItems is the menu for a whole-brush selection.
//
// Short on purpose. Almost everything a brush can be asked is either a property
// of one of its faces (a texture, a UV, smoothing) or an operation on one (an
// extrude, an inset), and both belong to edit mode — so the useful thing to
// offer here is the way in, plus the handful of actions that really are about
// the solid: duplicate it, snap it, delete it.
func editor3DBrushMenuItems() []ctxMenuItem {
	n := len(Editor.SelectedBrushes)
	all := func(label string) string {
		if n > 1 {
			return fmt.Sprintf("%s (%d brushes)", label, n)
		}
		return label
	}
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusBrush) },
			Tooltip: "Opens the side panel on the first selected brush."},
		{Label: "Edit this brush", Shortcut: "Tab",
			Do:      func() { editorSetEditContext(editorEditMode) },
			Tooltip: "Go into edit mode, where a click picks a face, an edge or a\ncorner and E/I/B reshape it."},
		{Label: "Detail this brush", Shortcut: "Ctrl+Shift+click", Disabled: n > 1,
			Do: editorEnterDetailModeOnSelection,
			Tooltip: "Shape the surface drawn over this brush with the model editor's\n" +
				"own tools — extrude, subdivide, smooth, delete — without moving\n" +
				"the solid the player collides with."},
		{Separator: true},
		{Label: "Duplicate", Disabled: n > 1, Do: editorDuplicateSelectedBrush,
			Tooltip: "A copy in place, selected and ready to drag off the original."},
		{Label: "Hollow this brush", Disabled: n > 1, Do: editorHollowSelectedBrush,
			Tooltip: "Replace the solid with the six brushes of a room filling it —\nfloor, ceiling and four walls."},
		{Label: "Save selection as prefab...", Do: func() { Editor.ShowSavePrefab = true },
			Tooltip: "Write the selected brushes to a file, so the same thing can be\n" +
				"placed twenty times and edited once."},
		{Separator: true},
		{Label: all("Snap to grid"), Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: all("Delete"), Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// ---------- surface ----------

func editor3DSurfaceMenuItems() []ctxMenuItem {
	r, ok := editor3DSurfaceRefs()
	if !ok {
		return nil
	}

	texture := ""
	if r.Texture != nil {
		texture = *r.Texture
	}

	// Every row below acts on the whole face selection, not only the primary —
	// which is what multi-select is for. The labels say so once it holds more
	// than one, since "Texture" on its own would read as being about the face
	// the panel happens to be showing.
	multi := selectedFaceCount() > 1
	all := func(label string) string {
		if multi {
			return fmt.Sprintf("%s (%d faces)", label, selectedFaceCount())
		}
		return label
	}

	items := []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusFace) },
			Tooltip: "Opens the side panel on the first selected face. The panel\nedits that one; these rows edit all of them."},
		{Label: all("Texture"), Sub: ctxTextureMenuItems(texture, editor3DSetSurfaceTexture)},
		{Separator: true},
	}
	items = append(items, editor3DFaceOpMenuItems(all)...)
	items = append(items, []ctxMenuItem{
		{Separator: true},
		// Every brush face has every one of these fields — a sector's six kinds
		// each had a different subset, which is why this block used to grey
		// rows out per kind and name the same flag two different things.
		{Label: all("Drawn"), Checked: faceDrawn(r.f),
			Do:      func() { editor3DToggleFaceDrawn() },
			Tooltip: "Off makes the face invisible while the brush still collides —\nfor a side buried against a neighbour."},
		{Label: all("Smooth"), Checked: r.Smooth != nil && *r.Smooth,
			Do:      func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Smooth }) },
			Tooltip: "Weld the normals along shared edges, so neighbouring\nfaces read as one curved surface."},
		{Label: all("Double sided"), Checked: r.DoubleSided != nil && *r.DoubleSided,
			Do: func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.DoubleSided }) }},
		{Label: all("Collision"), Checked: faceSolid(r.f),
			Do:      func() { editor3DToggleFaceSolid() },
			Tooltip: "Off makes this one face walk-through while the rest of the\nbrush stays solid — a window opening in a wall."},
	}...)
	if multi {
		// A mixed selection has no single "before" value, so the ticks above
		// describe the first face only. Saying which way the row will go is
		// more use than a tick that is right about one of twelve.
		items = append(items, ctxMenuItem{
			Label: "(ticks show the first face; a row sets them all to match it)"})
	}

	if editorInDetailMode() {
		// The rows below are about the SOLID — hollowing it, duplicating it,
		// saving it to a prefab — and none of them is a thing to do to one
		// face of the surface drawn over it.
		return append(items,
			ctxMenuItem{Separator: true},
			ctxMenuItem{Label: "Select this brush's detail faces", Do: editor3DSelectBrushFaces},
			ctxMenuItem{Label: "Select this brush's detail points", Do: editor3DSelectBrushVertices},
			ctxMenuItem{Label: "Deselect", Do: clearFaceSelection},
			ctxMenuItem{Separator: true},
			ctxMenuItem{Label: "Leave detail mode", Shortcut: "Tab", Do: editorExitDetailMode})
	}

	return append(items,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Hollow this brush", Disabled: multi, Do: editorHollowSelectedBrush,
			Tooltip: "Replace the solid with the six brushes of a room filling it —\nfloor, ceiling and four walls."},
		ctxMenuItem{Label: "Duplicate brush", Disabled: multi, Do: editorDuplicateSelectedBrush,
			Tooltip: "A copy in place, selected and ready to drag off the original."},
		ctxMenuItem{Label: "Save selection as prefab...", Do: func() { Editor.ShowSavePrefab = true },
			Tooltip: "Write the selected brushes to a file, so the same thing can be\n" +
				"placed twenty times and edited once. The prefab is authored around\n" +
				"the bottom-centre of what is selected, so it sits on the ground\n" +
				"wherever it is dropped."},
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Select this brush's sides", Do: editor3DSelectBrushSideFaces,
			Tooltip: "The upright faces only — the usual case, since a solid's sides\nwant one texture and its top and bottom another."},
		ctxMenuItem{Label: "Select this brush's faces", Do: editor3DSelectBrushFaces,
			Tooltip: "Ctrl+click adds one face at a time; this takes every side of\nthe solid at once, including the ones behind you."},
		ctxMenuItem{Label: "Select this brush's vertices", Do: editor3DSelectBrushVertices,
			Tooltip: "Every corner, so a drag moves the whole solid."},
		ctxMenuItem{Label: "Deselect", Do: clearFaceSelection},
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Delete brush", Shortcut: "Del", Do: EditorDeleteSelected},
	)
}

// ---------- vertices ----------

func editor3DVertexMenuItems() []ctxMenuItem {
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusVertex) },
			Tooltip: "Open the vertex section: the corner's X, Y and Z."},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid,
			Tooltip: "Rounds onto the grid whatever the snap toggle is set to —\nthat one decides what dragging does."},
		{Label: "Select this brush's vertices", Do: editor3DSelectBrushVertices},
		{Label: "Deselect", Do: clearVertexSelection},
	}
}

// ---------- entity ----------

func editor3DEntityMenuItems() []ctxMenuItem {
	e := activeEntity()
	if e == nil {
		return nil
	}
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusEntity) },
			Tooltip: "Open the entity's section: transform, tags, attributes,\nmodels and animations."},
		{Label: "Move here", Do: func() { editor3DMoveEntityTo(editor3DMenuPoint) },
			Tooltip: "Put it where the ray under the cursor met the world."},
		{Label: "Duplicate", Do: duplicateActiveEntity},
		{Label: "Reset to definition", Disabled: e.Type == "", Do: reapplyEntityDefinition,
			Tooltip: "Re-stamp from its type, keeping only where it is.\nEvery override on this instance is discarded."},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// ---------- volume ----------

// ---------- prefab ----------

func editor3DPrefabMenuItems() []ctxMenuItem {
	p := activePrefab()
	if p == nil {
		return nil
	}
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusPrefab) },
			Tooltip: "Open the instance's section: which file it places, and where."},
		{Label: "Move here", Do: func() {
			EditorPushUndo()
			p.Position = editor3DMenuPoint
			restampActivePrefab()
		}, Tooltip: "Put it where the ray under the cursor met the world."},
		{Label: "Re-stamp from file", Do: func() {
			levelio.InvalidatePrefabCache(p.File)
			restampActivePrefab()
		},
			Tooltip: "Read the file again. The editor does this for you when YOU\nsave the prefab; this is for a file changed on disk."},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Duplicate", Do: duplicateActivePrefab},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// ---------- volume ----------

func editor3DVolumeMenuItems() []ctxMenuItem {
	v := activeVolume()
	if v == nil {
		return nil
	}
	shapes := make([]ctxMenuItem, 0, len(world.VolumeShapeNames))
	for i := range world.VolumeShapeNames {
		kind := world.VolumeShape(i)
		shapes = append(shapes, ctxMenuItem{
			Label:   world.VolumeShapeNames[i],
			Checked: kind == v.Shape,
			Do:      func() { editor3DSetVolumeShape(kind) },
		})
	}
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusVolume) },
			Tooltip: "Open the volume's section: transform, shape, tags,\nattributes and the script it runs."},
		{Label: "Move here", Do: func() { editor3DMoveVolumeTo(editor3DMenuPoint) },
			Tooltip: "Put it where the ray under the cursor met the world."},
		{Label: "Shape", Sub: shapes},
		{Label: "Enabled", Checked: v.Enabled, Do: editor3DToggleVolumeEnabled,
			Tooltip: "A disabled volume contains nothing, so it fires no events.\nIt stays visible and selectable here."},
		{Label: "Duplicate", Do: duplicateActiveVolume},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// editor3DSetVolumeShape switches the selected volume's shape, rebuilding the
// cage that draws it — the menu's half of the panel's shape combo, going
// through the same rebuild so the two cannot disagree.
func editor3DSetVolumeShape(kind world.VolumeShape) {
	v := activeVolume()
	if v == nil || v.Shape == kind {
		return
	}
	EditorPushUndo()
	v.Shape = kind
	rebuildActiveVolumeCage()
}

// editor3DToggleVolumeEnabled arms or disarms the selected volume.
func editor3DToggleVolumeEnabled() {
	v := activeVolume()
	if v == nil {
		return
	}
	EditorPushUndo()
	v.Enabled = !v.Enabled
}

// ---------- nothing selected ----------

func editor3DEmptyMenuItems() []ctxMenuItem {
	return []ctxMenuItem{
		{Label: editor3DEmptyMenuHint()},
	}
}

// editor3DEmptyMenuHint says what a click would select, which depends on the
// context: whole objects, the parts of a brush, or the parts of the detail mesh
// drawn over one.
func editor3DEmptyMenuHint() string {
	switch {
	case editorInDetailMode():
		_, b := detailBrush()
		id := "a brush"
		if b != nil {
			id = b.ID
		}
		return "Detail mode on " + id +
			" — click its surface; E extrudes, B subdivides, Del removes (Tab or Escape to leave)"
	case editorInEditMode():
		return "Edit mode — click a face, an edge or a corner to select it (Tab for object mode)"
	}
	return "Object mode — click a brush, an entity, a volume or the spawn point to select it (Tab to edit a brush)"
}

// ---------- shared ----------

func editor3DSharedMenuItems() []ctxMenuItem {
	spawnLabel := "Place spawn point here"
	if Editor.level.Spawn != nil {
		spawnLabel = "Move spawn point here"
	}
	// The context first, then the units within it — the units are edit mode's,
	// and picking one from here goes there, exactly as pressing 1/2/3 does.
	modes := make([]ctxMenuItem, 0, len(editorSelModes)+2)
	modes = append(modes,
		ctxMenuItem{
			Label:    "Object",
			Shortcut: "Tab",
			Checked:  !editorInEditMode() && !editorInDetailMode(),
			Do:       func() { editorSetEditContext(editorObjectMode) },
			Tooltip:  "Click picks up a whole brush; the gizmo moves, turns and scales all of it.",
		},
		ctxMenuItem{
			Label:    "Detail",
			Shortcut: "Ctrl+Shift+click",
			Checked:  editorInDetailMode(),
			Do:       func() { editorSetEditContext(editorDetailMode) },
			Tooltip: "Shape the mesh drawn over one brush's surface. Collision stays\n" +
				"with the solid underneath, which is drawn as a wire cage.",
		},
		ctxMenuItem{Separator: true},
	)
	for _, mode := range editorSelModes {
		m := mode
		modes = append(modes, ctxMenuItem{
			Label:    m.String(),
			Shortcut: editorSelModeKey(m),
			Checked:  editorInEditMode() && Editor.SelMode == m,
			Do: func() {
				editorSetEditContext(editorEditMode)
				editorSetSelMode(m)
			},
			Tooltip: editorSelModeTooltip(m),
		})
	}

	return []ctxMenuItem{
		{Label: spawnLabel, Do: func() { editor3DAddSpawnPointAt(editor3DMenuPoint) },
			Tooltip: "Dropped where the ray under the cursor met the world.\nA level has one spawn point, so this moves an existing one."},
		editorAddEntityMenuItem(editor3DMenuPoint),
		{Label: "Add volume here", Do: func() { editor3DAddVolumeAt(editor3DMenuPoint) },
			Tooltip: "A one-unit box trigger where the cursor met the world.\nGive it a script in the Volumes panel."},
		editorSceneryBrushMenuItem(),
		editorBrushStampMenuItem(),
		{Label: "Select", Sub: modes},
		{Label: "Wireframe", Shortcut: "Z", Checked: Editor.Wireframe,
			Do:      func() { editorSetWireframe(!Editor.Wireframe) },
			Tooltip: "Solid hides the world's far side, and hidden points and\nedges cannot be clicked. Wireframe shows all of it."},
		{Label: "Bounding boxes", Shortcut: "B", Checked: Editor.ShowBounds,
			Do:      func() { editorSetShowBounds(!Editor.ShowBounds) },
			Tooltip: editorBoundsTooltip},
		{Label: "Orbit camera", Shortcut: "V", Checked: !editorCam3D.Fly,
			Do:      func() { editorSetFlyCam(editorCam3D.Fly) },
			Tooltip: "Turn around the pivot instead of flying. F frames the\nselection and puts the pivot on it."},
		{Label: "Frame selection", Shortcut: "F", Do: editorFrameSelection,
			Tooltip: "Orbit whatever is selected, falling back to the active chunk."},
		{Separator: true},
		{Label: "Snap to grid while dragging", Checked: Editor.SnapToGrid,
			Do: func() { Editor.SnapToGrid = !Editor.SnapToGrid }},
		{Label: "Show all point/edge boxes", Checked: Editor.ShowPointSelectRadius,
			Do:      func() { Editor.ShowPointSelectRadius = !Editor.ShowPointSelectRadius },
			Tooltip: "Draw every vertex handle and edge prism in range, not\nonly the ones the cursor is near.\nWireframe already does this."},
	}
}

// ---------- scenery ----------

func editor3DSceneryMenuItems() []ctxMenuItem {
	sc := activeScenery()
	if sc == nil {
		return nil
	}
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusScenery) },
			Tooltip: "Open the prop's section: transform, model and collision."},
		{Label: "Move here", Do: func() { editor3DMoveSceneryTo(editor3DMenuPoint) },
			Tooltip: "Put it where the ray under the cursor met the world."},
		{Label: "Use as brush", Do: func() {
			Editor.BrushRotation = sc.Rotation
			Editor.BrushScale = sc.EffectiveScale().X()
			Editor.BrushSolid = sc.Solid
			editorArmBrush(sc.Model)
		},
			Tooltip: "Arm the brush with this prop's model, facing and size -\nthe fastest way to make thirty more of one you like."},
		{Label: "Solid", Checked: sc.Solid, Do: editor3DToggleScenerySolid,
			Tooltip: "Stops the player and world-colliding entities.\nThe volume is the model's own bounding box."},
		{Label: "Duplicate", Do: duplicateActiveScenery},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// ---------- sprite ----------

func editor3DSpriteMenuItems() []ctxMenuItem {
	s := activeSprite()
	if s == nil {
		return nil
	}

	modes := make([]ctxMenuItem, 0, len(gfx.BillboardModeNames))
	for i := range gfx.BillboardModeNames {
		m := gfx.BillboardMode(i)
		modes = append(modes, ctxMenuItem{
			Label:   gfx.BillboardModeNames[i],
			Checked: m == s.Billboard,
			Tooltip: spriteBillboardHelp[i],
			Do:      func() { editor3DSetSpriteBillboard(m) },
		})
	}
	blends := make([]ctxMenuItem, 0, len(gfx.SpriteBlendNames))
	for i := range gfx.SpriteBlendNames {
		b := gfx.SpriteBlend(i)
		blends = append(blends, ctxMenuItem{
			Label:   gfx.SpriteBlendNames[i],
			Checked: b == s.Blend,
			Tooltip: spriteBlendHelp[i],
			Do:      func() { editor3DSetSpriteBlend(b) },
		})
	}

	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusSprite) },
			Tooltip: "Open the sprite's section: transform, texture,\nbillboard and blend modes, and what it rides."},
		{Label: "Move here", Do: func() { editor3DMoveSpriteTo(editor3DMenuPoint) },
			Tooltip: "Put it where the ray under the cursor met the world."},
		{Label: "Texture", Sub: ctxTextureMenuItems(s.Texture, editor3DSetSpriteTexture)},
		{Label: "Billboard", Sub: modes},
		{Label: "Blend", Sub: blends},
		{Label: "Lit", Checked: s.Lit, Do: editor3DToggleSpriteLit,
			Tooltip: "Take the scene's lights and shadows. Leave off for\nanything emissive."},
		{Label: "Visible", Checked: s.Visible, Do: editor3DToggleSpriteVisible,
			Tooltip: "A hidden sprite is not drawn, but stays selectable."},
		{Label: "Duplicate", Do: duplicateActiveSprite},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// editor3DSetSpriteBillboard switches which way the selected sprite faces.
func editor3DSetSpriteBillboard(m gfx.BillboardMode) {
	s := activeSprite()
	if s == nil || s.Billboard == m {
		return
	}
	EditorPushUndo()
	s.Billboard = m
}

// editor3DSetSpriteBlend switches which pass the selected sprite draws in.
func editor3DSetSpriteBlend(b gfx.SpriteBlend) {
	s := activeSprite()
	if s == nil || s.Blend == b {
		return
	}
	EditorPushUndo()
	s.Blend = b
}

// editor3DSetSpriteTexture retextures the selected sprite, going through the
// same load the panel's combo does so a missing texture is caught either way.
func editor3DSetSpriteTexture(name string) {
	s := activeSprite()
	if s == nil || s.Texture == name {
		return
	}
	EditorPushUndo()
	s.Texture = name
	if err := s.EnsureSpriteTexture(); err != nil {
		editorSetStatus("Sprite %s: %v", s.ID, err)
	}
}

func editor3DToggleSpriteLit() {
	if s := activeSprite(); s != nil {
		EditorPushUndo()
		s.Lit = !s.Lit
	}
}

func editor3DToggleSpriteVisible() {
	if s := activeSprite(); s != nil {
		EditorPushUndo()
		s.Visible = !s.Visible
	}
}

// editor3DFaceOpMenuItems are the shaping operations offered on a face
// selection, which are not the same three in every context.
//
// Edit mode's act on the SOLID and each preserves convexity, which is what
// limits them to three (editor_brush_mesh.go). Detail mode's act on a surface
// with no such rule, so they are the model editor's own — the real extrude that
// leaves side walls behind, and the smoothing that would turn a brush into a
// shape collision could not read (editor_detail_mesh.go).
func editor3DFaceOpMenuItems(all func(string) string) []ctxMenuItem {
	if editorInDetailMode() {
		return []ctxMenuItem{
			{Label: all("Extrude [E]"), Do: func() { detailExtrudeSelection(detailExtrudeDefault()) },
				Tooltip: "Duplicate the face and wall in the gap with sides — a bump\n" +
					"standing off the surface. Shift+E presses it in instead."},
			{Label: all("Subdivide [B]"), Do: detailSubdivideSelection,
				Tooltip: "Split the face at its edge midpoints, sharing the new corners\nwith its neighbours."},
			{Label: "Smooth the whole mesh [Shift+B]", Do: detailSmoothSelection,
				Tooltip: "One round of Catmull-Clark over every detail face. Rounds the\n" +
					"surface off; the solid underneath is untouched."},
			{Label: all("Delete [Del]"), Do: detailDeleteSelection,
				Tooltip: "Remove these faces from the detail mesh. The brush keeps its\nshape — this is only what is drawn."},
		}
	}
	return []ctxMenuItem{
		{Label: all("Extrude [E]"), Do: func() { brushExtrudeSelectedFaces(brushExtrudeDefault()) },
			Tooltip: "Slide the face along its own normal, growing the solid.\nShift+E pulls it back in."},
		{Label: all("Inset [I]"), Do: func() { brushInsetSelectedFaces(brushInsetFraction) },
			Tooltip: "Draw the face's corners toward its centre, in its own plane —\nturning the solid into a frustum of itself."},
		{Label: all("Subdivide [B]"), Do: brushSubdivideSelectedFaces,
			Tooltip: "Fan the face into one triangle per corner, meeting at its centre.\nAdds no volume; makes the face finer to shape."},
	}
}
