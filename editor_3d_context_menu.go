package main

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
	return Editor.SpawnPlacementMode || Editor.EntityPlacementMode
}

// editor3DPointAt is where the cursor's ray meets the world — the surface it
// hits, or a short way out in front when it hits nothing, so "here" still means
// something over open space.
func editor3DPointAt(mx, my int32) mgl32.Vec3 {
	origin, dir := screenRay(mx, my)
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
	case sel3DVertices:
		items = append(items, editor3DVertexMenuItems()...)
	case sel3DEntity:
		items = append(items, editor3DEntityMenuItems()...)
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

// ---------- surface ----------

func editor3DSurfaceMenuItems() []ctxMenuItem {
	r, ok := editor3DSurfaceRefs()
	if !ok {
		return nil
	}
	isEdge := r.kind == "wall" || r.kind == "portal"

	// "Enabled" on a floor or ceiling, "Visible" on a wall or portal: the
	// fields mean the same thing to the user and are named differently in the
	// schema, and the menu follows the schema so the panel and it agree.
	onLabel := "Enabled"
	if isEdge {
		onLabel = "Visible"
	}
	on := r.On != nil && *r.On

	texture := ""
	if r.Texture != nil {
		texture = *r.Texture
	}
	textureLabel := "Texture"
	if r.kind == "portal" {
		textureLabel = "Gap texture"
	}

	// Every row below acts on the whole face selection, not only the primary —
	// which is what multi-select is for. The labels say so once it holds more
	// than one, since "Texture" on its own would read as being about the wall
	// the panel happens to be showing.
	multi := selectedFaceCount() > 1
	all := func(label string) string {
		if multi {
			return fmt.Sprintf("%s (%d faces)", label, selectedFaceCount())
		}
		return label
	}

	items := []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(editor3DSurfaceFocus(r.kind)) },
			Tooltip: "Opens the side panel on the first selected face. The panel\nedits that one; these rows edit all of them."},
		{Label: all(textureLabel), Disabled: r.Texture == nil,
			Sub: ctxTextureMenuItems(texture, editor3DSetSurfaceTexture)},
		{Label: fmt.Sprintf("Edit %s in 2D", r.kind), Disabled: r.kind == "portal" || multi,
			Do:      editor3DEditSurfaceIn2D,
			Tooltip: "The surface's own 2D view: extrusions, terrain points and\nthe holes cut into it. One surface at a time."},
		{Separator: true},
		{Label: all(onLabel), Checked: on, Disabled: r.On == nil,
			Do: func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.On }) }},
		{Label: all("Smooth"), Checked: r.Smooth != nil && *r.Smooth, Disabled: r.Smooth == nil,
			Do:      func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Smooth }) },
			Tooltip: "Weld the normals along shared edges, so neighbouring\nfaces read as one curved surface."},
		{Label: all("Double sided"), Checked: r.DoubleSided != nil && *r.DoubleSided, Disabled: r.DoubleSided == nil,
			Do: func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.DoubleSided }) }},
		{Label: all("Collision"), Checked: r.Collision != nil && *r.Collision, Disabled: r.Collision == nil,
			Do: func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Collision }) }},
		{Label: all("Terrain"), Checked: r.Terrain != nil && *r.Terrain, Disabled: r.Terrain == nil,
			Do:      func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Terrain }) },
			Tooltip: "Drive the surface's shape from its terrain points\ninstead of keeping it flat."},
	}
	if multi {
		// A mixed selection has no single "before" value, so the ticks above
		// describe the first face only. Saying which way the row will go is
		// more use than a tick that is right about one of twelve.
		items = append(items, ctxMenuItem{
			Label: "(ticks show the first face; a row sets them all to match it)"})
	}

	if isEdge {
		to, label := "portal", "Convert to portal"
		if r.kind == "portal" {
			to, label = "wall", "Convert to wall"
		}
		items = append(items,
			ctxMenuItem{Separator: true},
			ctxMenuItem{Label: all(label), Do: func() { editor3DConvertSelectedEdge(to) },
				Tooltip: "A portal is an opening between sectors; a wall is solid.\n" +
					"Only the selected edges of the other kind are converted."},
		)
	}

	return append(items,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Select this sector's walls", Do: editor3DSelectSectorWalls,
			Tooltip: "Ctrl+click adds one face at a time; this takes the whole\nring at once, including the walls behind you."},
		ctxMenuItem{Label: "Select this sector's faces", Do: editor3DSelectSectorFaces,
			Tooltip: "The walls, the portals, the floor and the ceiling."},
		ctxMenuItem{Label: "Select this sector's vertices", Do: editor3DSelectSectorRing,
			Tooltip: "One ring — the floor's or the ceiling's — so a drag moves\neach chunk point once."},
		ctxMenuItem{Label: "Deselect", Do: clearFaceSelection},
	)
}

// editor3DSurfaceFocus is the panel section a surface kind belongs to. Portals
// live inside the wall section, which is where their properties are drawn.
func editor3DSurfaceFocus(kind string) string {
	switch kind {
	case "floor":
		return panelFocusFloor
	case "ceiling":
		return panelFocusCeiling
	default:
		return panelFocusWall
	}
}

// ---------- vertices ----------

func editor3DVertexMenuItems() []ctxMenuItem {
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusVertex) },
			Tooltip: "Open the vertex section: X, Z and the floor/ceiling offsets."},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid,
			Tooltip: "Rounds onto the grid whatever the snap toggle is set to —\nthat one decides what dragging does."},
		{Label: "Select this sector's vertices", Do: editor3DSelectSectorRing},
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

// ---------- nothing selected ----------

func editor3DEmptyMenuItems() []ctxMenuItem {
	return []ctxMenuItem{
		{Label: "Click a face, an entity, the spawn point, a vertex or an edge to select it"},
	}
}

// ---------- shared ----------

func editor3DSharedMenuItems() []ctxMenuItem {
	spawnLabel := "Place spawn point here"
	if Editor.level.Spawn != nil {
		spawnLabel = "Move spawn point here"
	}
	modes := make([]ctxMenuItem, 0, len(editorSelModes))
	for _, mode := range editorSelModes {
		m := mode
		modes = append(modes, ctxMenuItem{
			Label:    m.String(),
			Shortcut: editorSelModeKey(m),
			Checked:  Editor.SelMode == m,
			Do:       func() { editorSetSelMode(m) },
			Tooltip:  editorSelModeTooltip(m),
		})
	}

	return []ctxMenuItem{
		{Label: spawnLabel, Do: func() { editor3DAddSpawnPointAt(editor3DMenuPoint) },
			Tooltip: "Dropped where the ray under the cursor met the world.\nA level has one spawn point, so this moves an existing one."},
		editorAddEntityMenuItem(editor3DMenuPoint),
		{Label: "Select", Sub: modes},
		{Label: "Wireframe", Shortcut: "Z", Checked: Editor.Wireframe,
			Do:      func() { editorSetWireframe(!Editor.Wireframe) },
			Tooltip: "Solid hides the world's far side, and hidden points and\nedges cannot be clicked. Wireframe shows all of it."},
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
