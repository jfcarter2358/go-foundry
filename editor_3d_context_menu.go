package main

// editor_3d_context_menu.go — the 3D level editor's right-click menu.
//
// The same idea as the model editor's (editor_model_context_menu.go), on the
// same machinery (editor_context_menu.go): what the menu offers is decided by
// what is selected, so a wall is asked about textures and portals, a vertex
// about snapping, an entity about its model, and empty space about what to put
// there.
//
// The 3D editor's five selections are mutually exclusive — clicking a vertex
// clears the entity, clicking a face clears the vertex — so the menu has one
// block per kind and never has to merge two of them, which is the one real
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

// editor3DMenuPoint is the world point the menu was opened on — where "add a
// primitive here" puts one. Recorded at the click because the menu is drawn a
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
	return Editor.PlacementMode || Editor.PrimPlacementMode || Editor.ModelPlacementMode
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
	case sel3DEntity:
		items = append(items, editor3DEntityMenuItems()...)
	case sel3DPrimitive:
		items = append(items, editor3DPrimitiveMenuItems()...)
	case sel3DSurface:
		items = append(items, editor3DSurfaceMenuItems()...)
	case sel3DVertices:
		items = append(items, editor3DVertexMenuItems()...)
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

// ---------- entity ----------

func editor3DEntityMenuItems() []ctxMenuItem {
	e := editor3DActiveEntity()
	if e == nil {
		return nil
	}
	hasModel := e.ModelPath != ""

	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusEntity) },
			Tooltip: "Open this entity's section of the side panel."},
		{Label: "Texture", Sub: ctxTextureMenuItems(e.TexturePath, editor3DSetEntityTexture),
			Tooltip: "The fallback for faces the model names no texture for."},
		{Label: "Visible", Checked: e.Visible, Do: func() {
			EditorPushUndo()
			e.Visible = !e.Visible
		}},
		{Separator: true},
		{Label: "Fit bounds to model", Disabled: !hasModel, Do: func() {
			if err := fitEntityBoundsToModel(e); err != nil {
				editorSetStatus("Entity %q: %v", e.ID, err)
				return
			}
			editorSetStatus("Fitted bounds of %q to its model", e.ID)
		}, Tooltip: "The bounds are the collision volume and the culling box —\nthe mesh itself is never tested."},
		{Label: "Reload model", Disabled: !hasModel, Do: func() {
			if err := e.Load(); err != nil {
				editorSetStatus("Entity %q: %v", e.ID, err)
				return
			}
			editorSetStatus("Reloaded %s", relAssetPath(e.ModelPath))
		}},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// ---------- primitive ----------

func editor3DPrimitiveMenuItems() []ctxMenuItem {
	ch, p := editor3DActivePrimitive()
	if ch == nil {
		return nil
	}
	// A spawn marker never renders in the world, so its surface properties are
	// meaningless — the panel hides them too, and shows its facing instead.
	spawn := isSpawnPoint(p)

	kinds := make([]ctxMenuItem, 0, len(primitiveTypeNames))
	for i, name := range primitiveTypeNames {
		kind := PrimitiveType(i)
		kinds = append(kinds, ctxMenuItem{
			Label:   name,
			Checked: p.Kind == kind,
			Do:      func() { editor3DSetPrimitiveKind(kind) },
		})
	}

	items := []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusPrimitive) }},
		{Label: "Type", Sub: kinds},
		{Label: "Texture", Disabled: spawn,
			Sub: ctxTextureMenuItems(p.Texture, editor3DSetPrimitiveTexture)},
		{Label: "Collision", Checked: p.Collision, Disabled: spawn, Do: func() {
			editor3DTogglePrimitiveFlag(func(p *Primitive) *bool { return &p.Collision }, false)
		}},
		{Label: "Smooth", Checked: p.Smooth, Disabled: spawn, Do: func() {
			editor3DTogglePrimitiveFlag(func(p *Primitive) *bool { return &p.Smooth }, true)
		}},
	}
	if spawn {
		items = append(items, ctxMenuItem{Label: "Face the camera", Do: editor3DFaceCamera,
			Tooltip: "Point the marker where you are looking — including the\npitch, which placing it only takes the yaw of."})
	}
	return append(items,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		ctxMenuItem{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	)
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

	items := []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(editor3DSurfaceFocus(r.kind)) }},
		{Label: textureLabel, Disabled: r.Texture == nil,
			Sub: ctxTextureMenuItems(texture, editor3DSetSurfaceTexture)},
		{Label: fmt.Sprintf("Edit %s in 2D", r.kind), Disabled: r.kind == "portal",
			Do:      editor3DEditSurfaceIn2D,
			Tooltip: "The surface's own 2D view: extrusions, terrain points and\nthe holes cut into it."},
		{Separator: true},
		{Label: onLabel, Checked: on, Disabled: r.On == nil,
			Do: func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.On }) }},
		{Label: "Smooth", Checked: r.Smooth != nil && *r.Smooth, Disabled: r.Smooth == nil,
			Do:      func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Smooth }) },
			Tooltip: "Weld the normals along shared edges, so neighbouring\nfaces read as one curved surface."},
		{Label: "Double sided", Checked: r.DoubleSided != nil && *r.DoubleSided, Disabled: r.DoubleSided == nil,
			Do: func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.DoubleSided }) }},
		{Label: "Collision", Checked: r.Collision != nil && *r.Collision, Disabled: r.Collision == nil,
			Do: func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Collision }) }},
		{Label: "Terrain", Checked: r.Terrain != nil && *r.Terrain, Disabled: r.Terrain == nil,
			Do:      func() { editor3DToggleSurfaceFlag(func(r surface3DRefs) *bool { return r.Terrain }) },
			Tooltip: "Drive the surface's shape from its terrain points\ninstead of keeping it flat."},
	}

	if isEdge {
		to, label := "portal", "Convert to portal"
		if r.kind == "portal" {
			to, label = "wall", "Convert to wall"
		}
		items = append(items,
			ctxMenuItem{Separator: true},
			ctxMenuItem{Label: label, Do: func() { editor3DConvertSelectedEdge(to) },
				Tooltip: "A portal is an opening between sectors; a wall is solid."},
		)
	}

	return append(items,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Select this sector's vertices", Do: editor3DSelectSectorRing,
			Tooltip: "One ring — the floor's or the ceiling's — so a drag moves\neach chunk point once."},
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

// ---------- nothing selected ----------

func editor3DEmptyMenuItems() []ctxMenuItem {
	items := []ctxMenuItem{
		{Label: "Click a face, object, vertex or edge to select it"},
	}
	// The catalogs' placement, aimed at the point clicked rather than at a
	// fixed distance in front of the camera the way the toolbar's arm-and-click
	// is. Only offered when the catalog has something chosen, since the panel
	// is the only place to choose it.
	if def := editor3DSelectedEntityDef(); def != "" {
		items = append(items, ctxMenuItem{
			Label: fmt.Sprintf("Place %s here", def),
			Do:    func() { placeSelectedEntityAt(editor3DMenuPoint) },
		})
	}
	if name := editor3DSelectedModelName(); name != "" {
		items = append(items, ctxMenuItem{
			Label: fmt.Sprintf("Place %s here", name),
			Do:    func() { placeSelectedModelAt(editor3DMenuPoint) },
		})
	}
	return items
}

// editor3DSelectedEntityDef is the catalog entity's id, or "" when none is
// chosen.
func editor3DSelectedEntityDef() string {
	if Editor.SelectedDefIdx < 0 || Editor.SelectedDefIdx >= len(Editor.EntityDefs) {
		return ""
	}
	return Editor.EntityDefs[Editor.SelectedDefIdx].ID
}

// editor3DSelectedModelName is the catalog model's file name, or "".
func editor3DSelectedModelName() string {
	if Editor.SelectedModelIdx < 0 || Editor.SelectedModelIdx >= len(Editor.ModelFiles) {
		return ""
	}
	return Editor.ModelFiles[Editor.SelectedModelIdx]
}

// ---------- shared ----------

func editor3DSharedMenuItems() []ctxMenuItem {
	// Only move and scale exist here: a sector's shape is its points and its
	// heights, so there is nothing for a rotate gizmo to grab that the panel
	// does not do better.
	tools := []ctxMenuItem{
		{Label: "Move", Shortcut: "G", Checked: editorGizmoTool == modelToolMove,
			Do: func() { editorSetGizmoTool(modelToolMove) }},
		{Label: "Scale", Shortcut: "C", Checked: editorGizmoTool == modelToolScale,
			Do:      func() { editorSetGizmoTool(modelToolScale) },
			Tooltip: "Entities only — a sector or a primitive is scaled from\nits own properties."},
	}

	prims := make([]ctxMenuItem, 0, len(primitiveTypeNames))
	for _, n := range primitiveTypeNames {
		name := n
		prims = append(prims, ctxMenuItem{
			Label: name,
			Do:    func() { editor3DAddPrimitiveAt(name, editor3DMenuPoint) },
		})
	}

	return []ctxMenuItem{
		{Label: "Add primitive here", Sub: prims,
			Tooltip: "Dropped where the ray under the cursor met the world,\nin the active chunk."},
		{Label: "Transform tool", Sub: tools},
		{Label: "Snap to grid while dragging", Checked: Editor.SnapToGrid,
			Do: func() { Editor.SnapToGrid = !Editor.SnapToGrid }},
		{Label: "Show all point/edge boxes", Checked: Editor.ShowPointSelectRadius,
			Do:      func() { Editor.ShowPointSelectRadius = !Editor.ShowPointSelectRadius },
			Tooltip: "Draw every vertex handle and edge prism in range, not\nonly the ones the cursor is near."},
	}
}
