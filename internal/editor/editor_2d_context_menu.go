package editor

// editor_2d_context_menu.go — the top-down map's right-click menu.
//
// Built on the same machinery as the 3D viewport's and the model editor's
// (editor_context_menu.go), so all three look and behave alike.
//
// The gesture is simpler here than in 3D. There, right-drag is mouse-look, so
// the menu has to be told apart from a camera turn by whether the cursor
// travelled. On the map the right button means nothing else — panning is the
// middle button — so a press opens the menu outright.
//
// What it replaced is worth naming: right-click used to delete the point under
// the cursor immediately, with no confirmation and no undo prompt, and to clear
// the selection when it hit nothing. Both survive as rows. Deleting geometry now
// takes a deliberate pick, which is the better bargain even before the menu had
// somewhere for Join to live.

import (
	"fmt"
	"foundry/internal/world"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// editor2DCtxPopupID names the popup; "##" because a popup's name is also its
// title, and this one has no title bar to show it in.
const editor2DCtxPopupID = "##editor2dctxmenu"

// editor2DRightPress carries the pending-open flag between the event that asks
// for the menu and the frame that draws it.
var editor2DRightPress ctxGesture

// editor2DMenuPoint is the active chunk's own local XZ the menu was opened on
// — where "add point here" puts one, and which point "Delete point" means.
// Recorded at the click because the menu is drawn a frame later, by which
// time the cursor has moved.
var editor2DMenuPoint mgl32.Vec2

// editor2DMenuPointIdx is the chunk point the menu was opened on, or -1.
var editor2DMenuPointIdx = -1

// ---------- input glue ----------

// editor2DContextPressed opens the menu at (mx, my).
//
// With nothing selected the click first selects what it landed on, so a menu
// opened on a corner is about that corner — the same rule the 3D viewport's
// menu follows. With something already selected it changes nothing: a
// right-click beside a carefully picked set of points must not throw it away,
// since picking them is exactly what Join is waiting on.
func editor2DContextPressed(mx, my int32) {
	ch := activeChunk()
	if ch == nil {
		return
	}
	wx, wz := screenToWorld(mx, my)
	lx, lz := chunkLocalXZ(ch, wx, wz)
	editor2DMenuPoint = mgl32.Vec2{lx, lz}
	editor2DMenuPointIdx = findChunkPointNear(ch, lx, lz,
		chunkLocalSize(ch, screenToWorldSize(editor2DSelectRadius, editor2DOrtho())))

	if !editor2DHasSelection() {
		editor2DSelectAt(Editor.level, ch, wx, wz)
	}
	editor2DRightPress.open(mx, my)
}

// Editor2DContextMenu draws the popup. Called once per frame from whichever
// window is hosting the map, after its side panel, so the rows are built from
// the state the panel is showing.
func Editor2DContextMenu() {
	if Editor.level == nil || Editor.Mode != Mode2D || Editor.BiomeLayerActive {
		return
	}
	ctxMenuFrame(editor2DCtxPopupID, &editor2DRightPress, editor2DContextMenuItems)
}

// ---------- the menu ----------

// editor2DContextMenuItems builds the menu: a heading naming the selection, the
// point-joining block it exists for, then what the map offers whatever is
// selected — the modes, the tools and undo.
func editor2DContextMenuItems() []ctxMenuItem {
	ch := activeChunk()
	if ch == nil {
		return []ctxMenuItem{{Label: "No chunk selected"}}
	}

	items := []ctxMenuItem{{Label: editor2DSelectionSummary()}, {Separator: true}}
	// An entity selection replaces the point-oriented blocks rather than
	// adding to them: Join and "delete point" have nothing to say about it.
	if activeEntity() != nil {
		items = append(items, editor2DEntityMenuItems()...)
	} else {
		items = append(items, editor2DJoinMenuItems()...)
		items = append(items, ctxMenuItem{Separator: true})
		items = append(items, editor2DPointMenuItems(ch)...)
	}
	items = append(items, ctxMenuItem{Separator: true})
	items = append(items, editor2DSharedMenuItems()...)
	return append(items,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Undo", Shortcut: "Ctrl+Z", Disabled: !EditorCanUndo(), Do: EditorUndo},
		ctxMenuItem{Label: "Redo", Shortcut: "Ctrl+Shift+Z", Disabled: !EditorCanRedo(), Do: EditorRedo},
	)
}

// editor2DSelectionSummary names the selection for the menu's heading, counting
// in points rather than handles — that is the unit Join works in, and a corner
// two sectors share is one point to it however many handles named it.
func editor2DSelectionSummary() string {
	if e := activeEntity(); e != nil {
		if e.Type != "" {
			return fmt.Sprintf("Entity %s (%s)", e.ID, e.Type)
		}
		return fmt.Sprintf("Entity %s", e.ID)
	}
	if n := selectedFaceCount(); n > 0 {
		if n > 1 {
			return fmt.Sprintf("%d faces", n)
		}
		if Editor.ActiveEdgeKind != "" && Editor.ActiveEdgeIdx >= 0 {
			return fmt.Sprintf("%s #%d", editor3DKindLabel(Editor.ActiveEdgeKind), Editor.ActiveEdgeIdx)
		}
		return editor3DKindLabel(Editor.ActiveSurface)
	}
	switch n := len(editorSelectedChunkPoints()); n {
	case 0:
		return "Nothing selected"
	case 1:
		return "1 point"
	default:
		return fmt.Sprintf("%d points", n)
	}
}

// ---------- entity ----------

// editor2DEntityMenuItems is the 3D menu's entity block, re-pointed at the
// map's own "here". Deliberately the same rows: the same object should not
// offer different actions depending on which view it was right-clicked in.
func editor2DEntityMenuItems() []ctxMenuItem {
	e := activeEntity()
	if e == nil {
		return nil
	}
	here := editor2DMenuWorldPoint()
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { Editor.PanelFocus = panelFocusEntity }},
		{Label: "Move here", Do: func() { editor3DMoveEntityTo(here) }},
		{Label: "Duplicate", Do: duplicateActiveEntity},
		{Label: "Reset to definition", Disabled: e.Type == "", Do: reapplyEntityDefinition,
			Tooltip: "Re-stamp from its type, keeping only where it is.\nEvery override on this instance is discarded."},
		{Separator: true},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}

// editor2DMenuWorldPoint lifts the menu's chunk-local XZ point to a world
// position, taking its height from the floor under it — the map has no height
// of its own, and dropping an entity to y=0 under a raised floor would bury it.
func editor2DMenuWorldPoint() mgl32.Vec3 {
	ch := activeChunk()
	if ch == nil {
		return mgl32.Vec3{editor2DMenuPoint.X(), 0, editor2DMenuPoint.Y()}
	}
	wx, wz := chunkWorldXZ(ch, editor2DMenuPoint.X(), editor2DMenuPoint.Y())
	return mgl32.Vec3{wx, editor2DFloorHeightAt(ch, wx, wz), wz}
}

// ---------- joining ----------

func editor2DJoinMenuItems() []ctxMenuItem {
	can := editorCanJoinPoints()
	items := []ctxMenuItem{
		{Label: editorJoinLabel(false), Shortcut: "J", Disabled: !can,
			Do:      func() { EditorJoinSelectedPoints(false) },
			Tooltip: "Draw an edge between each picked point and the next, in\nthe order you picked them. Ctrl+click to pick them."},
		{Label: editorJoinLabel(true), Shortcut: "Shift+J", Disabled: !can,
			Do: func() { EditorJoinSelectedPoints(true) },
			Tooltip: "The same, plus the edge back to the first point — which\n" +
				"closes the outline and turns it into a sector."},
	}
	if len(Editor.PendingEdges) > 0 {
		items = append(items, ctxMenuItem{
			Label:   fmt.Sprintf("Discard %d unjoined edge(s)", len(Editor.PendingEdges)),
			Do:      EditorDiscardPendingEdges,
			Tooltip: "Edges that have been drawn but have not closed into a\nsector yet. The points themselves stay."})
	}
	return items
}

// ---------- points ----------

func editor2DPointMenuItems(ch *world.Chunk) []ctxMenuItem {
	nSel := len(editorSelectedChunkPoints())
	deleteLabel := "Delete Point"
	if nSel > 1 {
		deleteLabel = fmt.Sprintf("Delete %d Points", nSel)
	}
	return []ctxMenuItem{
		{Label: "Add Point Here", Do: editor2DAddPointAtMenuPoint,
			Tooltip: "Same as shift+click on empty map."},
		{Label: deleteLabel, Shortcut: "Del", Disabled: !editor2DCanDeletePoints(ch),
			Do:      editor2DDeleteSelectedPoints,
			Tooltip: "Removes the points and every edge meeting them. A sector\nleft with fewer than three corners goes with them."},
		{Label: "Clear Selection", Disabled: nSel == 0 && Editor.ActiveSurface == "",
			Do: editor2DClearSelection},
	}
}

// editor2DCanDeletePoints reports whether there is a point the delete row would
// remove — a selected one, or the one the menu was opened on.
func editor2DCanDeletePoints(ch *world.Chunk) bool {
	if len(editorSelectedChunkPoints()) > 0 {
		return true
	}
	return editor2DMenuPointIdx >= 0 && editor2DMenuPointIdx < len(ch.Points)
}

// editor2DAddPointAtMenuPoint drops a free point where the menu was opened.
func editor2DAddPointAtMenuPoint() {
	ch := activeChunk()
	if ch == nil {
		return
	}
	EditorPushUndo()
	ch.AddPoint(editor2DMenuPoint)
	editorSetStatus("Added a point")
}

// editor2DDeleteSelectedPoints removes the selected chunk points, falling back
// to the one the menu was opened on when nothing is selected.
//
// Highest index first: removeChunkPoint renumbers everything above the point it
// removes, so descending order leaves the indices still to be deleted correct.
func editor2DDeleteSelectedPoints() {
	ch := activeChunk()
	if ch == nil {
		return
	}
	pts := editorSelectedChunkPoints()
	if len(pts) == 0 {
		if editor2DMenuPointIdx < 0 || editor2DMenuPointIdx >= len(ch.Points) {
			return
		}
		pts = []int{editor2DMenuPointIdx}
	}
	sorted := append([]int{}, pts...)
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))

	EditorPushUndo()
	for _, pi := range sorted {
		if pi >= 0 && pi < len(ch.Points) {
			removeChunkPoint(ch, pi)
		}
	}
	editor2DMenuPointIdx = -1
	editorSetStatus("Deleted %d point(s)", len(sorted))
}

// ---------- shared ----------

func editor2DSharedMenuItems() []ctxMenuItem {
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
	tools := make([]ctxMenuItem, 0, len(editorGizmoTools))
	for _, tool := range editorGizmoTools {
		tl := tool
		tools = append(tools, ctxMenuItem{
			Label:    tl.String(),
			Shortcut: editorGizmoToolKey(tl),
			Checked:  Editor.GizmoTool == tl,
			Do:       func() { editorSetGizmoTool(tl) },
			Tooltip:  editorGizmoToolTooltip(tl),
		})
	}

	heightLabel := "Editing floor corners"
	if !Editor.EditFloorVerts {
		heightLabel = "Editing ceiling corners"
	}

	// Openings, offered only where there is one. Removing a hole is the one
	// thing about it that needs a place to be done from — cutting it has the
	// panel's tool, and there is nothing else to edit on a polygon the gizmo
	// does not own.
	var openings []ctxMenuItem
	if ch := activeChunk(); ch != nil && openingAt(ch, editor2DMenuPoint.X(), editor2DMenuPoint.Y()) {
		pt := editor2DMenuPoint
		openings = []ctxMenuItem{
			{Label: "Remove opening", Do: func() { EditorDeleteOpeningAt(ch, pt.X(), pt.Y()) },
				Tooltip: "Fill this hole back in. The surface is retriangulated solid."},
			{Separator: true},
		}
	}

	return append(openings, []ctxMenuItem{
		editorAddEntityMenuItem(editor2DMenuWorldPoint()),
		{Label: "Select", Sub: modes},
		{Label: "Gizmo", Sub: tools},
		{Label: heightLabel, Do: func() { Editor.EditFloorVerts = !Editor.EditFloorVerts },
			Tooltip: "Which of a corner's two handles the map picks and drags."},
		{Separator: true},
		{Label: "Snap to grid while dragging", Checked: Editor.SnapToGrid,
			Do: func() { Editor.SnapToGrid = !Editor.SnapToGrid }},
		{Label: "Show all point boxes", Checked: Editor.ShowPointSelectRadius,
			Do: func() { Editor.ShowPointSelectRadius = !Editor.ShowPointSelectRadius }},
	}...)
}
