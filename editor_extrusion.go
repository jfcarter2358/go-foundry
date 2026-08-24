package main

// editor_extrusion.go — ImGui panels and placement logic for wall extrusions.

import (
	"fmt"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// editorExtrusionPanel draws the extrusion sub-panel for a selected wall.
func editorExtrusionPanel(ch *world.Chunk, s *world.Sector, w *world.Wall) {
	imgui.Separator()
	if !imgui.CollapsingHeader(fmt.Sprintf("Extrusions (%d)##ext", len(w.Extrusions))) {
		return
	}

	// Placement mode status
	if Editor.ExtrusionMode {
		imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0, Y: 1, Z: 0.5, W: 1})
		if Editor.PlacingInner {
			imgui.Text(fmt.Sprintf("Placing inner (%d pts) — Click: add, Enter: finish, Tab: outer, Esc: cancel",
				len(Editor.PendingInner)))
		} else {
			imgui.Text(fmt.Sprintf("Placing outer (%d pts) — Click: add, Tab: inner, Enter: finish, Esc: cancel",
				len(Editor.PendingOuter)))
		}
		imgui.PopStyleColor()
		if imgui.Button("Cancel##extcancel") {
			editorCancelExtrusion()
		}
		return
	}

	// New extrusion button
	if imgui.Button("+ New Extrusion##extnew") {
		Editor.ExtrusionMode = true
		Editor.PlacingInner = false
		Editor.PendingOuter = nil
		Editor.PendingInner = nil
	}

	// List existing extrusions
	deleteIdx := -1
	for ei := range w.Extrusions {
		ext := &w.Extrusions[ei]
		open := imgui.TreeNodeV(fmt.Sprintf("Extrusion #%d (%d pts)##ext%d", ei, len(ext.Outer), ei),
			imgui.TreeNodeFlagsNone)
		if !open {
			continue
		}

		changed := false

		// Depth
		changed = editorDragFloat(fmt.Sprintf("Depth##extd%d", ei), &ext.Depth, -10, 10, "%.3f") || changed

		// Texture override
		changed = textureCombo(fmt.Sprintf("Texture##extt%d", ei), &ext.Texture, true) || changed

		// Back visible toggle
		changed = imgui.Checkbox(fmt.Sprintf("Visible##extv%d", ei), &ext.Visible) || changed
		changed = imgui.Checkbox(fmt.Sprintf("Back Visible##extbv%d", ei), &ext.BackVisible) || changed

		// Alpha (only affects custom-texture back face)
		changed = editorDragFloat(fmt.Sprintf("Alpha##exta%d", ei), &ext.Alpha, 0, 1, "%.2f") || changed

		// Per-vertex shape editing (extrusion outline on the wall surface)
		if imgui.TreeNodeV(fmt.Sprintf("Outer Shape##exto%d", ei), imgui.TreeNodeFlagsNone) {
			for vi := range ext.Outer {
				p := &ext.Outer[vi]
				changed = editorDragFloat(fmt.Sprintf("X##extoU%d_%d", ei, vi), &p.U, 0, 0, "%.3f") || changed
				changed = editorDragFloat(fmt.Sprintf("Y##extoV%d_%d", ei, vi), &p.V, 0, 0, "%.3f") || changed
			}
			imgui.TreePop()
		}

		if imgui.TreeNodeV(fmt.Sprintf("Inner Shape##exti%d", ei), imgui.TreeNodeFlagsNone) {
			for vi := range ext.Inner {
				p := &ext.Inner[vi]
				changed = editorDragFloat(fmt.Sprintf("X##extiU%d_%d", ei, vi), &p.U, 0, 0, "%.3f") || changed
				changed = editorDragFloat(fmt.Sprintf("Y##extiV%d_%d", ei, vi), &p.V, 0, 0, "%.3f") || changed
			}
			imgui.TreePop()
		}

		if imgui.Button(fmt.Sprintf("Delete##extdel%d", ei)) {
			deleteIdx = ei
		}

		changed = editorUVControls(fmt.Sprintf("WallExt%d", ei), &ext.UVOffset, &ext.UVScale, &ext.UVRotation) || changed

		if changed {
			_ = world.ReloadSector(ch, s)
		}

		imgui.TreePop()
	}

	// Process deletion outside the loop
	if deleteIdx >= 0 {
		EditorPushUndo()
		w.Extrusions = append(w.Extrusions[:deleteIdx], w.Extrusions[deleteIdx+1:]...)
		_ = world.ReloadSector(ch, s)
	}
}

// editorCancelExtrusion exits extrusion placement mode without creating anything.
func editorCancelExtrusion() {
	Editor.ExtrusionMode = false
	Editor.PendingOuter = nil
	Editor.PendingInner = nil
	Editor.PlacingInner = false
}

// editorFinalizeExtrusion commits the pending extrusion to the active wall.
func editorFinalizeExtrusion() {
	if len(Editor.PendingOuter) < 3 {
		editorCancelExtrusion()
		return
	}

	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveSectorIdx < 0 {
		editorCancelExtrusion()
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx >= len(ch.Sectors) {
		editorCancelExtrusion()
		return
	}
	s := &ch.Sectors[Editor.ActiveSectorIdx]
	if Editor.ActiveEdgeKind != "wall" || Editor.ActiveEdgeIdx < 0 || Editor.ActiveEdgeIdx >= len(s.Walls) {
		editorCancelExtrusion()
		return
	}
	w := &s.Walls[Editor.ActiveEdgeIdx]

	outer := make([]world.ExtrusionPoint, len(Editor.PendingOuter))
	copy(outer, Editor.PendingOuter)

	// If no inner polygon was placed, copy outer
	inner := make([]world.ExtrusionPoint, len(outer))
	if len(Editor.PendingInner) == len(outer) {
		copy(inner, Editor.PendingInner)
	} else {
		copy(inner, outer)
	}

	ext := world.Extrusion{
		Outer:       outer,
		Inner:       inner,
		Depth:       -0.1, // default slight inward
		Visible:     true,
		BackVisible: true,
		Alpha:       1,
	}
	EditorPushUndo()
	w.Extrusions = append(w.Extrusions, ext)
	_ = world.ReloadSector(ch, s)

	editorCancelExtrusion()
}

// editorExtrusionHandleKey processes keyboard input for extrusion placement mode.
// Returns true if the event was consumed.
func editorExtrusionHandleKey(t sdl.KeyboardEvent) bool {
	if !Editor.ExtrusionMode || t.Type != sdl.KEYDOWN {
		return false
	}

	switch t.Keysym.Scancode {
	case sdl.SCANCODE_ESCAPE:
		editorCancelExtrusion()
		return true
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		editorFinalizeExtrusion()
		return true
	case sdl.SCANCODE_TAB:
		if !Editor.PlacingInner && len(Editor.PendingOuter) >= 3 {
			// Switch to inner polygon placement, pre-fill from outer
			Editor.PlacingInner = true
			Editor.PendingInner = make([]world.ExtrusionPoint, len(Editor.PendingOuter))
			copy(Editor.PendingInner, Editor.PendingOuter)
		} else if Editor.PlacingInner {
			// Switch back to outer
			Editor.PlacingInner = false
		}
		return true
	}
	return false
}

// editorExtrusionHandleClick processes a mouse click during extrusion placement mode.
// It raycasts to the wall plane to determine the UV coordinates. Returns true if consumed.
func editorExtrusionHandleClick(t sdl.MouseButtonEvent) bool {
	if !Editor.ExtrusionMode || t.Button != sdl.ButtonLeft {
		return false
	}

	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveSectorIdx < 0 {
		return false
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx >= len(ch.Sectors) {
		return false
	}
	s := &ch.Sectors[Editor.ActiveSectorIdx]
	if Editor.ActiveEdgeKind != "wall" || Editor.ActiveEdgeIdx < 0 || Editor.ActiveEdgeIdx >= len(s.Walls) {
		return false
	}
	w := &s.Walls[Editor.ActiveEdgeIdx]

	// Raycast to wall plane
	origin, dir := screenRay(t.X, t.Y)
	u, v, hit := rayToWallUV(origin, dir, w, s, ch)
	if !hit {
		return true // consumed but missed
	}

	pt := world.ExtrusionPoint{U: u, V: v}
	if Editor.PlacingInner {
		Editor.PendingInner = append(Editor.PendingInner, pt)
	} else {
		Editor.PendingOuter = append(Editor.PendingOuter, pt)
	}
	return true
}

// rayToWallUV raycasts against a wall's plane and returns (u, v, hit).
func rayToWallUV(origin, dir mgl32.Vec3, w *world.Wall, s *world.Sector, ch *world.Chunk) (float32, float32, bool) {
	p1 := w.StartPoint
	p2 := w.EndPoint
	sPts := world.SectorPoints(ch, s)
	vi1 := world.FindVertexIdx(sPts, p1)
	vi2 := world.FindVertexIdx(sPts, p2)

	ceilH1 := s.WallTopHeight(vi1)
	ceilH2 := s.WallTopHeight(vi2)
	floorH1 := s.Floor.VertexHeight(vi1)
	floorH2 := s.Floor.VertexHeight(vi2)

	wDir, wNorm := world.WallFrame(p1, p2)
	wallLen := mgl32.Vec2{p2.X() - p1.X(), p2.Y() - p1.Y()}.Len()

	// Wall plane: defined by wall normal (in 3D) and a point on the wall
	planeNormal := mgl32.Vec3{wNorm.X(), 0, wNorm.Y()}
	planePoint := mgl32.Vec3{p1.X(), ceilH1, p1.Y()}

	denom := planeNormal.Dot(dir)
	if math.Abs(float64(denom)) < 1e-6 {
		return 0, 0, false
	}

	t := planeNormal.Dot(planePoint.Sub(origin)) / denom
	if t < 0 {
		return 0, 0, false
	}

	hitPoint := origin.Add(dir.Mul(t))

	// Convert hit point to wall-local U, V
	// U = projection along wall direction
	hitXZ := mgl32.Vec2{hitPoint.X() - p1.X(), hitPoint.Z() - p1.Y()}
	u := hitXZ.X()*wDir.X() + hitXZ.Y()*wDir.Y()

	// V = distance from ceiling downward
	uParam := u / wallLen
	if uParam < 0 {
		uParam = 0
	}
	if uParam > 1 {
		uParam = 1
	}
	ceilAtU := ceilH1 + (ceilH2-ceilH1)*uParam
	_ = floorH1
	_ = floorH2
	v := ceilAtU - hitPoint.Y()

	return u, v, true
}

// editorExtrusionRenderGizmos draws the pending extrusion outline on the wall.
func editorExtrusionRenderGizmos() {
	if !Editor.ExtrusionMode {
		return
	}

	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveSectorIdx < 0 {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx >= len(ch.Sectors) {
		return
	}
	s := &ch.Sectors[Editor.ActiveSectorIdx]
	if Editor.ActiveEdgeKind != "wall" || Editor.ActiveEdgeIdx < 0 || Editor.ActiveEdgeIdx >= len(s.Walls) {
		return
	}
	w := &s.Walls[Editor.ActiveEdgeIdx]

	p1 := w.StartPoint
	p2 := w.EndPoint
	sPts := world.SectorPoints(ch, s)
	vi1 := world.FindVertexIdx(sPts, p1)
	vi2 := world.FindVertexIdx(sPts, p2)

	dir, norm := world.WallFrame(p1, p2)
	wallLen := mgl32.Vec2{p2.X() - p1.X(), p2.Y() - p1.Y()}.Len()
	floorH1 := s.Floor.VertexHeight(vi1)
	floorH2 := s.Floor.VertexHeight(vi2)
	ceilH1 := s.WallTopHeight(vi1)
	ceilH2 := s.WallTopHeight(vi2)

	green := mgl32.Vec3{0, 1, 0.3}
	yellow := mgl32.Vec3{1, 1, 0}

	uvToWorld := func(ep world.ExtrusionPoint) mgl32.Vec3 {
		x, y, z := world.WallLocalToWorld(ep.U, ep.V, 0.01, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
		return mgl32.Vec3{x, y, z}
	}

	// Draw outer polygon
	drawPoly := func(pts []world.ExtrusionPoint, col mgl32.Vec3) {
		if len(pts) < 2 {
			return
		}
		for i := 0; i < len(pts); i++ {
			a := uvToWorld(pts[i])
			b := uvToWorld(pts[(i+1)%len(pts)])
			gizmo.GizmoLine(a, b, col)
		}
		// Draw vertex crosses
		for _, p := range pts {
			c := uvToWorld(p)
			gizmo.GizmoLine(c.Add(mgl32.Vec3{-0.05, 0, 0}), c.Add(mgl32.Vec3{0.05, 0, 0}), col)
			gizmo.GizmoLine(c.Add(mgl32.Vec3{0, -0.05, 0}), c.Add(mgl32.Vec3{0, 0.05, 0}), col)
		}
	}

	drawPoly(Editor.PendingOuter, green)
	if Editor.PlacingInner && len(Editor.PendingInner) > 0 {
		drawPoly(Editor.PendingInner, yellow)
	}
}

// editorRenderExtrusionOutlines draws outlines for existing extrusions on the selected wall.
func editorRenderExtrusionOutlines() {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveSectorIdx < 0 {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx >= len(ch.Sectors) {
		return
	}
	s := &ch.Sectors[Editor.ActiveSectorIdx]
	if Editor.ActiveEdgeKind != "wall" || Editor.ActiveEdgeIdx < 0 || Editor.ActiveEdgeIdx >= len(s.Walls) {
		return
	}
	w := &s.Walls[Editor.ActiveEdgeIdx]
	if len(w.Extrusions) == 0 {
		return
	}

	p1 := w.StartPoint
	p2 := w.EndPoint
	sPts := world.SectorPoints(ch, s)
	vi1 := world.FindVertexIdx(sPts, p1)
	vi2 := world.FindVertexIdx(sPts, p2)

	dir, norm := world.WallFrame(p1, p2)
	wallLen := mgl32.Vec2{p2.X() - p1.X(), p2.Y() - p1.Y()}.Len()
	floorH1 := s.Floor.VertexHeight(vi1)
	floorH2 := s.Floor.VertexHeight(vi2)
	ceilH1 := s.WallTopHeight(vi1)
	ceilH2 := s.WallTopHeight(vi2)

	cyan := mgl32.Vec3{0, 1, 1}

	for _, ext := range w.Extrusions {
		for i := 0; i < len(ext.Outer); i++ {
			a := ext.Outer[i]
			b := ext.Outer[(i+1)%len(ext.Outer)]
			ax, ay, az := world.WallLocalToWorld(a.U, a.V, 0.01, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
			bx, by, bz := world.WallLocalToWorld(b.U, b.V, 0.01, p1, p2, dir, norm, wallLen, floorH1, floorH2, ceilH1, ceilH2)
			gizmo.GizmoLine(mgl32.Vec3{ax, ay, az}, mgl32.Vec3{bx, by, bz}, cyan)
		}
	}
}

// ---------- Floor/Ceiling extrusion panel ----------

// editorSurfaceExtrusionPanel draws extrusion controls for a floor or ceiling.
// surfaceLabel: "Floor" or "Ceiling". extrusions: pointer to the extrusion slice.
func editorSurfaceExtrusionPanel(ch *world.Chunk, s *world.Sector, surfaceLabel string, extrusions *[]world.Extrusion) {
	imgui.Separator()
	if !imgui.CollapsingHeader(fmt.Sprintf("%s Extrusions (%d)##%sext", surfaceLabel, len(*extrusions), surfaceLabel)) {
		return
	}

	deleteIdx := -1
	for ei := range *extrusions {
		ext := &(*extrusions)[ei]
		open := imgui.TreeNodeV(fmt.Sprintf("%s Ext #%d (%d pts)##%sext%d", surfaceLabel, ei, len(ext.Outer), surfaceLabel, ei),
			imgui.TreeNodeFlagsNone)
		if !open {
			continue
		}

		changed := false
		changed = editorDragFloat(fmt.Sprintf("Depth##%sed%d", surfaceLabel, ei), &ext.Depth, -10, 10, "%.3f") || changed
		changed = textureCombo(fmt.Sprintf("Texture##%set%d", surfaceLabel, ei), &ext.Texture, true) || changed
		changed = imgui.Checkbox(fmt.Sprintf("Visible##%sev%d", surfaceLabel, ei), &ext.Visible) || changed
		changed = imgui.Checkbox(fmt.Sprintf("Back Visible##%sebv%d", surfaceLabel, ei), &ext.BackVisible) || changed
		changed = editorDragFloat(fmt.Sprintf("Alpha##%sea%d", surfaceLabel, ei), &ext.Alpha, 0, 1, "%.2f") || changed

		if imgui.TreeNodeV(fmt.Sprintf("Outer Shape##%seo%d", surfaceLabel, ei), imgui.TreeNodeFlagsNone) {
			for vi := range ext.Outer {
				p := &ext.Outer[vi]
				changed = editorDragFloat(fmt.Sprintf("X##%seoU%d_%d", surfaceLabel, ei, vi), &p.U, 0, 0, "%.3f") || changed
				changed = editorDragFloat(fmt.Sprintf("Z##%seoV%d_%d", surfaceLabel, ei, vi), &p.V, 0, 0, "%.3f") || changed
			}
			imgui.TreePop()
		}
		if imgui.TreeNodeV(fmt.Sprintf("Inner Shape##%sei%d", surfaceLabel, ei), imgui.TreeNodeFlagsNone) {
			for vi := range ext.Inner {
				p := &ext.Inner[vi]
				changed = editorDragFloat(fmt.Sprintf("X##%seiU%d_%d", surfaceLabel, ei, vi), &p.U, 0, 0, "%.3f") || changed
				changed = editorDragFloat(fmt.Sprintf("Z##%seiV%d_%d", surfaceLabel, ei, vi), &p.V, 0, 0, "%.3f") || changed
			}
			imgui.TreePop()
		}

		if imgui.Button(fmt.Sprintf("Delete##%sedel%d", surfaceLabel, ei)) {
			deleteIdx = ei
		}

		changed = editorUVControls(fmt.Sprintf("%sExt%d", surfaceLabel, ei), &ext.UVOffset, &ext.UVScale, &ext.UVRotation) || changed

		if changed {
			_ = world.ReloadSector(ch, s)
		}
		imgui.TreePop()
	}

	if deleteIdx >= 0 {
		EditorPushUndo()
		*extrusions = append((*extrusions)[:deleteIdx], (*extrusions)[deleteIdx+1:]...)
		_ = world.ReloadSector(ch, s)
	}
}

// ---------- Terrain panel (for walls, floors, ceilings) ----------

// editorTerrainPanel draws terrain point controls for a surface.
// surfaceLabel: "Floor", "Ceiling", or "Wall".
// terrain: pointer to the terrain toggle.
// terrainPts: pointer to the terrain points slice.
func editorTerrainPanel(ch *world.Chunk, s *world.Sector, surfaceLabel string, terrain *bool, terrainPts *[]world.TerrainPoint) {
	imgui.Separator()
	if !imgui.CollapsingHeader(fmt.Sprintf("%s Terrain (%d pts)##%sterr", surfaceLabel, len(*terrainPts), surfaceLabel)) {
		return
	}

	changed := false
	changed = imgui.Checkbox(fmt.Sprintf("Enabled##%sterrEn", surfaceLabel), terrain) || changed

	if imgui.Button(fmt.Sprintf("+ Add Point##%sterrAdd", surfaceLabel)) {
		EditorPushUndo()
		*terrainPts = append(*terrainPts, world.TerrainPoint{U: 0, V: 0, Depth: 0})
		Editor.ActiveTerrainPtIdx = len(*terrainPts) - 1
		changed = true
	}

	// List points as selectable items
	for i := range *terrainPts {
		selected := i == Editor.ActiveTerrainPtIdx
		if imgui.SelectableV(fmt.Sprintf("Point #%d##%sterr%d", i, surfaceLabel, i), selected, 0, imgui.Vec2{}) {
			if selected {
				Editor.ActiveTerrainPtIdx = -1
			} else {
				Editor.ActiveTerrainPtIdx = i
			}
		}
	}

	// Show selected point properties
	idx := Editor.ActiveTerrainPtIdx
	if idx >= 0 && idx < len(*terrainPts) {
		tp := &(*terrainPts)[idx]
		imgui.PushID(fmt.Sprintf("%sterr%d", surfaceLabel, idx))
		imgui.Separator()
		imgui.Text(fmt.Sprintf("Point #%d", idx))
		changed = editorDragFloat("U##tpu", &tp.U, 0, 0, "%.3f") || changed
		changed = editorDragFloat("V##tpv", &tp.V, 0, 0, "%.3f") || changed
		changed = editorDragFloat("Depth##tpd", &tp.Depth, -10, 10, "%.3f") || changed
		if imgui.Button("Delete##tpdel") {
			EditorPushUndo()
			*terrainPts = append((*terrainPts)[:idx], (*terrainPts)[idx+1:]...)
			Editor.ActiveTerrainPtIdx = -1
			changed = true
		}
		imgui.PopID()
	}

	if changed {
		_ = world.ReloadSector(ch, s)
	}
}
