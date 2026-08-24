package main

// editor_surface2d.go — 2D surface editor for extrusions and terrain.
//
// Displays a wall (as a rectangle in UV space) or a floor/ceiling (as the
// sector polygon in XZ) and lets the user draw extrusion polygons and place
// terrain points directly on the surface.
//
// Extrusion workflow:
//   1. Shift+click to place outer polygon vertices (green)
//   2. Enter to finalize → extrusion created, inner = copy of outer
//   3. Drag inner (magenta) points to adjust bevel/slope
//   4. Connection lines between outer[i]↔inner[i] show the pairing
//
// Terrain workflow:
//   1. Click "+ Place Terrain Points" in the panel
//   2. Shift+click to place terrain points (orange)
//   3. Drag existing terrain points to reposition
//
// General:
//   Shift+click  : place a point
//   Left-click   : select / drag an existing point
//   Right-click  : delete nearest pending or terrain point
//   Middle-mouse : pan
//   Scroll       : zoom
//   Enter        : finalize extrusion polygon
//   Backspace    : undo last placed point
//   Escape       : cancel placement or exit surface editor

import (
	"fmt"
	"foundry/internal/gizmo"
	"foundry/internal/platform"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const surface2DSelectRadius = float32(8.0)

// ---------- coordinate helpers ----------

// surfaceView returns the surface UV editor's current orthoView (see
// editor_orthoview.go).
func surfaceView() orthoView {
	return orthoView{pan: Editor.SurfacePan, zoom: Editor.SurfaceZoom}
}

func surfaceOrtho() mgl32.Mat4 {
	return surfaceView().ortho()
}

func surfaceWorldToScreen(u, v float32) (sx, sy float32) {
	return surfaceView().worldToScreen(u, v)
}

func surfaceScreenToWorld(sx, sy int32) (u, v float32) {
	return surfaceView().screenToWorld(sx, sy)
}

func surfaceScreenToWorldSize(px float32) float32 {
	return surfaceView().screenToWorldSize(px)
}

// ---------- enter / exit ----------

func EnterSurface2D(kind string) {
	Editor.SurfacePrevMode = Editor.Mode
	Editor.Mode = ModeSurface2D
	Editor.SurfaceKind = kind
	Editor.SurfacePlacing = ""
	Editor.SurfacePendingPts = nil
	Editor.SurfaceDragPt = -1
	Editor.ActiveTerrainPtIdx = -1

	boundary := surfaceBoundary()
	if len(boundary) > 0 {
		var cx, cy float32
		for _, p := range boundary {
			cx += p.X()
			cy += p.Y()
		}
		cx /= float32(len(boundary))
		cy /= float32(len(boundary))
		Editor.SurfacePan = mgl32.Vec2{cx, cy}
	}
}

func exitSurface2D() {
	Editor.SurfacePlacing = ""
	Editor.SurfacePendingPts = nil
	Editor.SurfaceDragPt = -1
	Editor.Mode = Editor.SurfacePrevMode
}

// ---------- surface data accessors ----------

func surfaceSector() (*Chunk, *Sector) {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return nil, nil
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx < 0 || Editor.ActiveSectorIdx >= len(ch.Sectors) {
		return nil, nil
	}
	return ch, &ch.Sectors[Editor.ActiveSectorIdx]
}

func surfaceWall() *Wall {
	ch, s := surfaceSector()
	if ch == nil || s == nil {
		return nil
	}
	if Editor.ActiveEdgeKind != "wall" || Editor.ActiveEdgeIdx < 0 || Editor.ActiveEdgeIdx >= len(s.Walls) {
		return nil
	}
	return &s.Walls[Editor.ActiveEdgeIdx]
}

func surfaceBoundary() []mgl32.Vec2 {
	ch, s := surfaceSector()
	if ch == nil || s == nil {
		return nil
	}
	switch Editor.SurfaceKind {
	case "wall":
		w := surfaceWall()
		if w == nil {
			return nil
		}
		sPts := SectorPoints(ch, s)
		vi1 := findVertexIdx(sPts, w.StartPoint)
		vi2 := findVertexIdx(sPts, w.EndPoint)
		wallLen := mgl32.Vec2{w.EndPoint.X() - w.StartPoint.X(), w.EndPoint.Y() - w.StartPoint.Y()}.Len()
		floorH1 := s.Floor.VertexHeight(vi1)
		floorH2 := s.Floor.VertexHeight(vi2)
		ceilH1 := s.WallTopHeight(vi1)
		ceilH2 := s.WallTopHeight(vi2)
		wallH := ((ceilH1 - floorH1) + (ceilH2 - floorH2)) / 2
		return []mgl32.Vec2{{0, 0}, {wallLen, 0}, {wallLen, wallH}, {0, wallH}}
	case "floor", "ceiling":
		return SectorPoints(ch, s)
	}
	return nil
}

func surfaceExtrusions() *[]Extrusion {
	ch, s := surfaceSector()
	if ch == nil || s == nil {
		return nil
	}
	switch Editor.SurfaceKind {
	case "wall":
		w := surfaceWall()
		if w == nil {
			return nil
		}
		return &w.Extrusions
	case "floor":
		return &s.Floor.Extrusions
	case "ceiling":
		return &s.Ceiling.Extrusions
	}
	return nil
}

func surfaceTerrainPts() (*bool, *[]TerrainPoint) {
	ch, s := surfaceSector()
	if ch == nil || s == nil {
		return nil, nil
	}
	switch Editor.SurfaceKind {
	case "wall":
		w := surfaceWall()
		if w == nil {
			return nil, nil
		}
		return &w.Terrain, &w.TerrainPoints
	case "floor":
		return &s.Floor.Terrain, &s.Floor.TerrainPoints
	case "ceiling":
		return &s.Ceiling.Terrain, &s.Ceiling.TerrainPoints
	}
	return nil, nil
}

func surfaceReload() {
	ch, s := surfaceSector()
	if ch != nil && s != nil {
		_ = reloadSector(ch, s)
	}
}

// ---------- rendering ----------

func Surface2DRender() {
	boundary := surfaceBoundary()
	if len(boundary) < 3 {
		return
	}

	ortho := surfaceOrtho()

	// Draw background grid before any geometry
	drawEditorGrid(surfaceView())

	white := mgl32.Vec3{1, 1, 1}
	cyan := mgl32.Vec3{0, 1, 1}
	green := mgl32.Vec3{0.2, 0.9, 0.2}
	red := mgl32.Vec3{1, 0.3, 0.3}
	magenta := mgl32.Vec3{1, 0.3, 1}
	orange := mgl32.Vec3{1, 0.6, 0.1}
	grey := mgl32.Vec3{0.5, 0.5, 0.5}

	y := float32(0)
	crossSz := surfaceScreenToWorldSize(4)

	// Boundary outline
	for i := 0; i < len(boundary); i++ {
		a := boundary[i]
		b := boundary[(i+1)%len(boundary)]
		gizmo.GizmoLine(mgl32.Vec3{a.X(), y, a.Y()}, mgl32.Vec3{b.X(), y, b.Y()}, white)
	}
	// Boundary vertex markers
	for _, p := range boundary {
		gizmo.GizmoLine(mgl32.Vec3{p.X() - crossSz, y, p.Y()}, mgl32.Vec3{p.X() + crossSz, y, p.Y()}, red)
		gizmo.GizmoLine(mgl32.Vec3{p.X(), y, p.Y() - crossSz}, mgl32.Vec3{p.X(), y, p.Y() + crossSz}, red)
	}

	// Existing extrusions — outer, inner, and CONNECTION LINES
	layer := Editor.SurfaceEditLayer
	outerCol := cyan
	innerCol := magenta
	// Dim the non-active layer
	if layer == "inner" {
		outerCol = mgl32.Vec3{0, 0.4, 0.4}
	} else if layer == "outer" {
		innerCol = mgl32.Vec3{0.4, 0.15, 0.4}
	}

	exts := surfaceExtrusions()
	if exts != nil {
		for _, ext := range *exts {
			nPts := len(ext.Outer)
			// Outer polygon edges + vertices
			for i := 0; i < nPts; i++ {
				a := ext.Outer[i]
				b := ext.Outer[(i+1)%nPts]
				gizmo.GizmoLine(mgl32.Vec3{a.U, y, a.V}, mgl32.Vec3{b.U, y, b.V}, outerCol)
				gizmo.GizmoLine(mgl32.Vec3{a.U - crossSz, y, a.V}, mgl32.Vec3{a.U + crossSz, y, a.V}, outerCol)
				gizmo.GizmoLine(mgl32.Vec3{a.U, y, a.V - crossSz}, mgl32.Vec3{a.U, y, a.V + crossSz}, outerCol)
			}
			// Inner polygon edges + vertices
			for i := 0; i < len(ext.Inner); i++ {
				a := ext.Inner[i]
				b := ext.Inner[(i+1)%len(ext.Inner)]
				gizmo.GizmoLine(mgl32.Vec3{a.U, y, a.V}, mgl32.Vec3{b.U, y, b.V}, innerCol)
				sz := crossSz * 0.7
				gizmo.GizmoLine(mgl32.Vec3{a.U - sz, y, a.V}, mgl32.Vec3{a.U + sz, y, a.V}, innerCol)
				gizmo.GizmoLine(mgl32.Vec3{a.U, y, a.V - sz}, mgl32.Vec3{a.U, y, a.V + sz}, innerCol)
			}
			// Connection lines outer[i] ↔ inner[i]
			nConn := nPts
			if len(ext.Inner) < nConn {
				nConn = len(ext.Inner)
			}
			for i := 0; i < nConn; i++ {
				o := ext.Outer[i]
				in := ext.Inner[i]
				gizmo.GizmoLine(mgl32.Vec3{o.U, y, o.V}, mgl32.Vec3{in.U, y, in.V}, grey)
			}
		}
	}

	// Terrain points
	_, tPts := surfaceTerrainPts()
	if tPts != nil {
		for i, tp := range *tPts {
			sz := crossSz * 1.2
			col := orange
			if i == Editor.ActiveTerrainPtIdx {
				sz *= 1.5
				col = mgl32.Vec3{1, 1, 0} // yellow highlight
			}
			gizmo.GizmoLine(mgl32.Vec3{tp.U - sz, y, tp.V - sz}, mgl32.Vec3{tp.U + sz, y, tp.V + sz}, col)
			gizmo.GizmoLine(mgl32.Vec3{tp.U + sz, y, tp.V - sz}, mgl32.Vec3{tp.U - sz, y, tp.V + sz}, col)
		}
	}

	// Pending placement polygon
	if Editor.SurfacePlacing == "extrusion" {
		pts := Editor.SurfacePendingPts
		for i := 0; i < len(pts); i++ {
			a := pts[i]
			if i+1 < len(pts) {
				b := pts[i+1]
				gizmo.GizmoLine(mgl32.Vec3{a.U, y, a.V}, mgl32.Vec3{b.U, y, b.V}, green)
			}
			gizmo.GizmoLine(mgl32.Vec3{a.U - crossSz, y, a.V}, mgl32.Vec3{a.U + crossSz, y, a.V}, green)
			gizmo.GizmoLine(mgl32.Vec3{a.U, y, a.V - crossSz}, mgl32.Vec3{a.U, y, a.V + crossSz}, green)
		}
		if len(pts) >= 3 {
			a := pts[len(pts)-1]
			b := pts[0]
			gizmo.GizmoLine(mgl32.Vec3{a.U, y, a.V}, mgl32.Vec3{b.U, y, b.V}, green)
		}
	}

	gizmo.GizmoFlush(ortho)
}

func Surface2DRenderLabels() {
	drawList := imgui.ForegroundDrawList()

	boundary := surfaceBoundary()
	white := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 0.8})
	for i, p := range boundary {
		sx, sy := surfaceWorldToScreen(p.X(), p.Y())
		drawList.AddText(imgui.Vec2{X: sx + 5, Y: sy - 12}, white, fmt.Sprintf("%d", i))
	}

	// Placement hint
	if Editor.SurfacePlacing != "" {
		col := imgui.PackedColorFromVec4(imgui.Vec4{X: 0, Y: 1, Z: 0.5, W: 1})
		var hint string
		switch Editor.SurfacePlacing {
		case "extrusion":
			hint = fmt.Sprintf("Placing extrusion (%d pts) — Shift+click: add | Enter: finish | Backspace: undo | Esc: cancel", len(Editor.SurfacePendingPts))
		case "terrain":
			hint = "Shift+click to place terrain points — Esc to stop"
		}
		drawList.AddText(imgui.Vec2{X: 10, Y: float32(platform.WinHeight) - 30}, col, hint)
	} else {
		hint := "Shift+click: new extrusion | Drag: move points | MMB: pan | Scroll: zoom"
		col := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.7, Y: 0.7, Z: 0.7, W: 0.8})
		drawList.AddText(imgui.Vec2{X: 10, Y: float32(platform.WinHeight) - 30}, col, hint)
	}
}

// ---------- event handling ----------

func Surface2DHandleClick(t sdl.MouseButtonEvent) {
	if imgui.CurrentIO().WantCaptureMouse() {
		return
	}

	if t.Button == sdl.ButtonMiddle {
		Editor.SurfaceLastMouse = [2]int32{t.X, t.Y}
		return
	}

	u, v := surfaceScreenToWorld(t.X, t.Y)
	mods := sdl.GetModState()

	// Right-click: delete nearest terrain point
	if t.Button == sdl.ButtonRight {
		threshold := surfaceScreenToWorldSize(surface2DSelectRadius)
		_, tPts := surfaceTerrainPts()
		if tPts != nil {
			bestIdx := -1
			bestDist := threshold
			for i, tp := range *tPts {
				d := dist2D(u, v, tp.U, tp.V)
				if d < bestDist {
					bestDist = d
					bestIdx = i
				}
			}
			if bestIdx >= 0 {
				EditorPushUndo()
				*tPts = append((*tPts)[:bestIdx], (*tPts)[bestIdx+1:]...)
				surfaceReload()
			}
		}
		return
	}

	if t.Button != sdl.ButtonLeft {
		return
	}

	// Shift+click: place a point
	if mods&sdl.KMOD_SHIFT != 0 {
		if Editor.SurfacePlacing == "" {
			// Auto-start extrusion placement
			Editor.SurfacePlacing = "extrusion"
			Editor.SurfacePendingPts = nil
		}
		if Editor.SurfacePlacing == "extrusion" {
			Editor.SurfacePendingPts = append(Editor.SurfacePendingPts, ExtrusionPoint{U: u, V: v})
		} else if Editor.SurfacePlacing == "terrain" {
			_, tPts := surfaceTerrainPts()
			if tPts != nil {
				EditorPushUndo()
				*tPts = append(*tPts, TerrainPoint{U: u, V: v, Depth: 0})
				surfaceReload()
			}
		}
		return
	}

	// Left-click: select/drag an existing point
	threshold := surfaceScreenToWorldSize(surface2DSelectRadius)

	layer := Editor.SurfaceEditLayer
	exts := surfaceExtrusions()
	if exts != nil {
		for ei := range *exts {
			ext := &(*exts)[ei]
			if layer == "both" || layer == "outer" {
				for vi, p := range ext.Outer {
					if dist2D(u, v, p.U, p.V) < threshold {
						EditorPushUndo() // snapshot at drag start, not per-frame during the drag
						Editor.SurfaceDragPt = vi
						Editor.SurfaceDragKind = "outer"
						Editor.SurfaceDragExtIdx = ei
						return
					}
				}
			}
			if layer == "both" || layer == "inner" {
				for vi, p := range ext.Inner {
					if dist2D(u, v, p.U, p.V) < threshold {
						EditorPushUndo() // snapshot at drag start, not per-frame during the drag
						Editor.SurfaceDragPt = vi
						Editor.SurfaceDragKind = "inner"
						Editor.SurfaceDragExtIdx = ei
						return
					}
				}
			}
		}
	}

	_, tPts := surfaceTerrainPts()
	if tPts != nil {
		for i, tp := range *tPts {
			if dist2D(u, v, tp.U, tp.V) < threshold {
				EditorPushUndo() // snapshot at drag start, not per-frame during the drag
				Editor.SurfaceDragPt = i
				Editor.SurfaceDragKind = "terrain"
				Editor.ActiveTerrainPtIdx = i
				return
			}
		}
	}

	Editor.SurfaceDragPt = -1
	Editor.ActiveTerrainPtIdx = -1
}

func Surface2DHandleRelease(t sdl.MouseButtonEvent) {
	Editor.SurfaceDragPt = -1
}

func Surface2DHandleMouseMove(t sdl.MouseMotionEvent) {
	if t.State&sdl.ButtonMMask != 0 {
		dx := float32(t.X-Editor.SurfaceLastMouse[0]) / Editor.SurfaceZoom
		dy := float32(t.Y-Editor.SurfaceLastMouse[1]) / Editor.SurfaceZoom
		Editor.SurfacePan[0] -= dx
		Editor.SurfacePan[1] -= dy
		Editor.SurfaceLastMouse = [2]int32{t.X, t.Y}
		return
	}

	if t.State&sdl.ButtonLMask != 0 && Editor.SurfaceDragPt >= 0 {
		u, v := surfaceScreenToWorld(t.X, t.Y)
		if Editor.SnapToGrid {
			u = snapValue(u, Editor.StepSize)
			v = snapValue(v, Editor.StepSize)
		}

		exts := surfaceExtrusions()
		switch Editor.SurfaceDragKind {
		case "outer":
			if exts != nil && Editor.SurfaceDragExtIdx < len(*exts) {
				ext := &(*exts)[Editor.SurfaceDragExtIdx]
				if Editor.SurfaceDragPt < len(ext.Outer) {
					ext.Outer[Editor.SurfaceDragPt] = ExtrusionPoint{U: u, V: v}
					surfaceReload()
				}
			}
		case "inner":
			if exts != nil && Editor.SurfaceDragExtIdx < len(*exts) {
				ext := &(*exts)[Editor.SurfaceDragExtIdx]
				if Editor.SurfaceDragPt < len(ext.Inner) {
					ext.Inner[Editor.SurfaceDragPt] = ExtrusionPoint{U: u, V: v}
					surfaceReload()
				}
			}
		case "terrain":
			_, tPts := surfaceTerrainPts()
			if tPts != nil && Editor.SurfaceDragPt < len(*tPts) {
				(*tPts)[Editor.SurfaceDragPt] = TerrainPoint{U: u, V: v, Depth: (*tPts)[Editor.SurfaceDragPt].Depth}
				surfaceReload()
			}
		}
	}
}

func surface2DHandleKey(t sdl.KeyboardEvent) bool {
	if t.Type != sdl.KEYDOWN {
		return false
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_ESCAPE:
		if Editor.SurfacePlacing != "" {
			Editor.SurfacePlacing = ""
			Editor.SurfacePendingPts = nil
		} else {
			exitSurface2D()
		}
		return true

	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		if Editor.SurfacePlacing == "extrusion" && len(Editor.SurfacePendingPts) >= 3 {
			surface2DFinalizeExtrusion()
		}
		return true

	case sdl.SCANCODE_DELETE, sdl.SCANCODE_BACKSPACE:
		if Editor.SurfacePlacing == "extrusion" && len(Editor.SurfacePendingPts) > 0 {
			Editor.SurfacePendingPts = Editor.SurfacePendingPts[:len(Editor.SurfacePendingPts)-1]
		}
		return true
	}
	return false
}

func surface2DFinalizeExtrusion() {
	exts := surfaceExtrusions()
	if exts == nil || len(Editor.SurfacePendingPts) < 3 {
		return
	}

	outer := make([]ExtrusionPoint, len(Editor.SurfacePendingPts))
	copy(outer, Editor.SurfacePendingPts)

	// Inner starts as a copy of outer — user drags inner points to adjust bevel
	inner := make([]ExtrusionPoint, len(outer))
	copy(inner, outer)

	EditorPushUndo()
	*exts = append(*exts, Extrusion{
		Outer:       outer,
		Inner:       inner,
		Depth:       -0.1,
		Visible:     true,
		BackVisible: true,
		Alpha:       1,
	})
	surfaceReload()

	Editor.SurfacePlacing = ""
	Editor.SurfacePendingPts = nil
}

// ---------- side panel ----------

func Surface2DPanel() {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	imgui.SetNextWindowPos(imgui.Vec2{X: float32(platform.WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(platform.WinHeight) - 28})
	imgui.BeginV("Surface Editor", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	ch, s := surfaceSector()
	if ch == nil || s == nil {
		imgui.Text("No surface selected")
		imgui.End()
		return
	}

	imgui.Text(fmt.Sprintf("Surface: %s  |  Sector: %s", Editor.SurfaceKind, s.ID))
	imgui.Text(fmt.Sprintf("Zoom: %.0f px/unit", Editor.SurfaceZoom))

	editorGridSettings()

	if imgui.Button("Back (Esc)##surfBack") {
		exitSurface2D()
	}

	imgui.Separator()

	// --- Edit layer toggle ---
	imgui.Text("Edit Layer:")
	imgui.SameLine()
	currentLayer := Editor.SurfaceEditLayer
	if currentLayer == "both" {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.6, Z: 0.2, W: 1})
	}
	if imgui.Button("Both##layerBoth") {
		Editor.SurfaceEditLayer = "both"
	}
	if currentLayer == "both" {
		imgui.PopStyleColor()
	}
	imgui.SameLine()
	if currentLayer == "outer" {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0, Y: 0.7, Z: 0.7, W: 1})
	}
	if imgui.Button("Outer##layerOuter") {
		Editor.SurfaceEditLayer = "outer"
	}
	if currentLayer == "outer" {
		imgui.PopStyleColor()
	}
	imgui.SameLine()
	if currentLayer == "inner" {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.7, Y: 0.2, Z: 0.7, W: 1})
	}
	if imgui.Button("Inner##layerInner") {
		Editor.SurfaceEditLayer = "inner"
	}
	if currentLayer == "inner" {
		imgui.PopStyleColor()
	}

	imgui.Separator()

	// --- Extrusion tools ---
	if imgui.CollapsingHeaderV("Extrusions##surfExt", imgui.TreeNodeFlagsDefaultOpen) {
		if Editor.SurfacePlacing == "extrusion" {
			imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0, Y: 1, Z: 0.5, W: 1})
			imgui.Text(fmt.Sprintf("Placing extrusion (%d pts)", len(Editor.SurfacePendingPts)))
			imgui.PopStyleColor()
			if imgui.Button("Finish (Enter)##surfFinish") {
				surface2DFinalizeExtrusion()
			}
			imgui.SameLine()
			if imgui.Button("Cancel (Esc)##surfCancel") {
				Editor.SurfacePlacing = ""
				Editor.SurfacePendingPts = nil
			}
		} else if Editor.SurfacePlacing == "" {
			if imgui.Button("+ New Extrusion##surfNewExt") {
				Editor.SurfacePlacing = "extrusion"
				Editor.SurfacePendingPts = nil
			}
		}

		imgui.Text("Drag cyan (outer) / magenta (inner) points to edit.")
		imgui.Text("Grey lines show outer↔inner connections.")

		// List existing extrusions
		exts := surfaceExtrusions()
		if exts != nil {
			deleteIdx := -1
			for ei := range *exts {
				ext := &(*exts)[ei]
				if imgui.TreeNodeV(fmt.Sprintf("Ext #%d (%d pts)##surfext%d", ei, len(ext.Outer), ei), imgui.TreeNodeFlagsNone) {
					changed := false
					changed = editorDragFloat(fmt.Sprintf("Depth##sed%d", ei), &ext.Depth, -10, 10, "%.3f") || changed
					changed = textureCombo(fmt.Sprintf("Texture##set%d", ei), &ext.Texture, true) || changed
					changed = imgui.Checkbox(fmt.Sprintf("Visible##sev%d", ei), &ext.Visible) || changed
					changed = imgui.Checkbox(fmt.Sprintf("Back Visible##sebv%d", ei), &ext.BackVisible) || changed
					if imgui.Button(fmt.Sprintf("Delete##sedel%d", ei)) {
						deleteIdx = ei
					}
					if changed {
						surfaceReload()
					}
					imgui.TreePop()
				}
			}
			if deleteIdx >= 0 {
				EditorPushUndo()
				*exts = append((*exts)[:deleteIdx], (*exts)[deleteIdx+1:]...)
				surfaceReload()
			}
		}
	}

	imgui.Separator()

	// --- Terrain tools ---
	if imgui.CollapsingHeaderV("Terrain##surfTerr", imgui.TreeNodeFlagsDefaultOpen) {
		terrFlag, tPts := surfaceTerrainPts()
		if terrFlag != nil {
			changed := imgui.Checkbox("Terrain Enabled##surfTerrEn", terrFlag)

			if Editor.SurfacePlacing == "terrain" {
				imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.6, Z: 0.1, W: 1})
				imgui.Text("Shift+click to place | Right-click to delete")
				imgui.PopStyleColor()
				if imgui.Button("Done##surfTerrDone") {
					Editor.SurfacePlacing = ""
				}
			} else if Editor.SurfacePlacing == "" {
				if imgui.Button("+ Place Terrain Points##surfTerrPlace") {
					Editor.SurfacePlacing = "terrain"
				}
			}

			if tPts != nil {
				imgui.Text(fmt.Sprintf("%d points — click to select", len(*tPts)))
				idx := Editor.ActiveTerrainPtIdx
				if idx >= 0 && idx < len(*tPts) {
					tp := &(*tPts)[idx]
					imgui.PushID(fmt.Sprintf("surftp%d", idx))
					imgui.Text(fmt.Sprintf("Point #%d", idx))
					changed = editorDragFloat("U##stpu", &tp.U, 0, 0, "%.3f") || changed
					changed = editorDragFloat("V##stpv", &tp.V, 0, 0, "%.3f") || changed
					changed = editorDragFloat("Depth##stpd", &tp.Depth, -10, 10, "%.3f") || changed
					if imgui.Button("Delete##stpdel") {
						EditorPushUndo()
						*tPts = append((*tPts)[:idx], (*tPts)[idx+1:]...)
						Editor.ActiveTerrainPtIdx = -1
						changed = true
					}
					imgui.PopID()
				}
			}

			if changed {
				surfaceReload()
			}
		}
	}

	imgui.End()
}
