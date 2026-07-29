package main

// editor_biome.go — biome sector layer inside the 2D top-down editor.
//
// The biome layer is a separate polygon layer from structural sector
// geometry: while Editor.BiomeLayerActive, the 2D view draws/selects/edits
// BiomeSector polygons instead of structural sectors. Point placement mirrors
// the existing wall-extrusion placement pattern (editor_extrusion.go): click
// to add a point, click near the first point (or press Enter) to close the
// polygon, Escape to cancel. Once drawn, a sector is selected (from the 2D
// view or the side-panel list) and assigned a brush — the sector owns that
// brush outright, so editing it only ever affects that one sector, and
// multiple sectors may be drawn overlapping to layer looks together.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// activeChunk returns the chunk currently shown in the 2D/3D editor, or nil.
func activeChunk() *Chunk {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return nil
	}
	return &lvl.Chunks[Editor.ActiveChunkIdx]
}

// biomeSectorAt returns the index of the topmost (last-drawn) BiomeSector in
// ch containing world point (wx,wz), or -1.
func biomeSectorAt(ch *Chunk, wx, wz float32) int {
	pt := mgl32.Vec2{wx, wz}
	for i := len(ch.BiomeSectors) - 1; i >= 0; i-- {
		if pointInPolygon(pt, ch.BiomeSectors[i].Points) {
			return i
		}
	}
	return -1
}

// finishBiomeSectorDraw commits the in-progress polygon (if it has enough
// points) as a new BiomeSector with default brush settings, and selects it.
func finishBiomeSectorDraw(ch *Chunk) {
	if len(Editor.BiomeDrawPoints) >= 3 {
		EditorPushUndo()
		pts := append([]mgl32.Vec2(nil), Editor.BiomeDrawPoints...)
		if signedArea2D(pts) < 0 {
			for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
				pts[i], pts[j] = pts[j], pts[i]
			}
		}
		bs := BiomeSector{
			ID:     nextBiomeSectorID(ch),
			Points: pts,
			Brush: BiomeBrush{
				Kind:        BiomeGrass,
				Density:     defaultBiomeDensity,
				Range:       defaultGrassRange,
				Color:       mgl32.Vec3{0.4, 0.7, 0.3},
				GrassHeight: defaultGrassHeight,
				GrassWidth:  defaultGrassWidth,
				RockSides:   defaultRockSides,
				RockScale:   defaultRockScale,
			},
			HeightDirty: true,
		}
		ch.BiomeSectors = append(ch.BiomeSectors, bs)
		Editor.ActiveBiomeSectorIdx = len(ch.BiomeSectors) - 1
		Editor.StatusMsg = fmt.Sprintf("Added biome sector %s", bs.ID)
		Editor.StatusTime = sdl.GetTicks()
	}
	Editor.BiomeDrawing = false
	Editor.BiomeDrawPoints = nil
}

// deleteBiomeSector removes ch.BiomeSectors[idx] and evicts its cached GPU
// height texture.
func deleteBiomeSector(ch *Chunk, idx int) {
	if ch == nil || idx < 0 || idx >= len(ch.BiomeSectors) {
		return
	}
	EvictBiomeHeightCache(ch.BiomeSectors[idx].ID)
	ch.BiomeSectors = append(ch.BiomeSectors[:idx], ch.BiomeSectors[idx+1:]...)
	Editor.ActiveBiomeSectorIdx = -1
	Editor.BiomeDragVertexIdx = -1
}

// ---------- 2D input hooks (called from editor_2d.go) ----------

// biomeHandleClick processes a mouse click while the biome layer is active.
// Returns true if it consumed the click (so structural-sector editing is
// skipped for the rest of Editor2DHandleClick).
func biomeHandleClick(t sdl.MouseButtonEvent) bool {
	if !Editor.BiomeLayerActive {
		return false
	}
	if t.Button == sdl.ButtonMiddle {
		return false // let middle-mouse pan through
	}
	ch := activeChunk()
	if ch == nil {
		return true
	}
	wx, wz := screenToWorld(t.X, t.Y)
	threshold := screenToWorldSize(editor2DSelectRadius, editor2DOrtho())

	if Editor.BiomeDrawing {
		if t.Button == sdl.ButtonRight {
			if len(Editor.BiomeDrawPoints) > 0 {
				Editor.BiomeDrawPoints = Editor.BiomeDrawPoints[:len(Editor.BiomeDrawPoints)-1]
			}
			return true
		}
		if t.Button != sdl.ButtonLeft {
			return true
		}
		if len(Editor.BiomeDrawPoints) >= 3 &&
			dist2D(wx, wz, Editor.BiomeDrawPoints[0].X(), Editor.BiomeDrawPoints[0].Y()) < threshold {
			finishBiomeSectorDraw(ch)
			return true
		}
		if len(Editor.BiomeDrawPoints) < biomeMaxPolyVerts {
			Editor.BiomeDrawPoints = append(Editor.BiomeDrawPoints, mgl32.Vec2{wx, wz})
		}
		return true
	}

	if t.Button == sdl.ButtonRight {
		if idx := biomeSectorAt(ch, wx, wz); idx >= 0 {
			EditorPushUndo()
			deleteBiomeSector(ch, idx)
		}
		return true
	}
	if t.Button != sdl.ButtonLeft {
		return true
	}

	// Vertex-drag start on the currently selected sector takes priority over
	// re-selecting, so you can grab a handle even inside another sector.
	if Editor.ActiveBiomeSectorIdx >= 0 && Editor.ActiveBiomeSectorIdx < len(ch.BiomeSectors) {
		bs := &ch.BiomeSectors[Editor.ActiveBiomeSectorIdx]
		for vi, p := range bs.Points {
			if dist2D(wx, wz, p.X(), p.Y()) < threshold {
				EditorPushUndo() // snapshot at drag start, not per-frame during the drag
				Editor.BiomeDragVertexIdx = vi
				return true
			}
		}
	}

	Editor.ActiveBiomeSectorIdx = biomeSectorAt(ch, wx, wz)
	Editor.BiomeDragVertexIdx = -1
	return true
}

// biomeHandleMouseMove drags the active sector's grabbed vertex, if any.
func biomeHandleMouseMove(t sdl.MouseMotionEvent) {
	if !Editor.BiomeLayerActive || Editor.BiomeDragVertexIdx < 0 {
		return
	}
	if t.State&sdl.ButtonLMask == 0 {
		return
	}
	ch := activeChunk()
	if ch == nil || Editor.ActiveBiomeSectorIdx < 0 || Editor.ActiveBiomeSectorIdx >= len(ch.BiomeSectors) {
		return
	}
	bs := &ch.BiomeSectors[Editor.ActiveBiomeSectorIdx]
	vi := Editor.BiomeDragVertexIdx
	if vi < 0 || vi >= len(bs.Points) {
		return
	}
	wx, wz := screenToWorld(t.X, t.Y)
	bs.Points[vi] = mgl32.Vec2{wx, wz}
	bs.HeightDirty = true
}

// biomeHandleRelease ends a vertex drag.
func biomeHandleRelease(t sdl.MouseButtonEvent) {
	if t.Button == sdl.ButtonLeft {
		Editor.BiomeDragVertexIdx = -1
	}
}

// biomeHandleKey processes Enter/Escape while placing a new sector's points.
// Returns true if the event was consumed.
func biomeHandleKey(t sdl.KeyboardEvent) bool {
	if !Editor.BiomeDrawing || t.Type != sdl.KEYDOWN {
		return false
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_ESCAPE:
		Editor.BiomeDrawing = false
		Editor.BiomeDrawPoints = nil
		return true
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		if ch := activeChunk(); ch != nil {
			finishBiomeSectorDraw(ch)
		} else {
			Editor.BiomeDrawing = false
			Editor.BiomeDrawPoints = nil
		}
		return true
	}
	return false
}

// ---------- 2D rendering ----------

// biomeSectorColor returns the outline/fill tint for a sector's brush kind.
func biomeSectorColor(kind int32) mgl32.Vec3 {
	if kind == BiomeRock {
		return mgl32.Vec3{0.65, 0.65, 0.7}
	}
	return mgl32.Vec3{0.25, 0.85, 0.3}
}

// Editor2DRenderBiomeSectors draws every biome sector in ch as an outline
// (dim when the layer isn't active, bright + vertex handles when it is), the
// selected sector in yellow, and the in-progress draw polygon in green.
// Called from Editor2DRender alongside the structural-sector rendering.
func Editor2DRenderBiomeSectors(ch *Chunk) {
	const y = float32(0.12)
	yellow := mgl32.Vec3{1, 1, 0}
	green := mgl32.Vec3{0.2, 0.9, 0.2}

	for si := range ch.BiomeSectors {
		bs := &ch.BiomeSectors[si]
		n := len(bs.Points)
		if n < 2 {
			continue
		}
		col := biomeSectorColor(bs.Brush.Kind)
		if !Editor.BiomeLayerActive {
			col = col.Mul(0.45)
		}
		selected := si == Editor.ActiveBiomeSectorIdx
		if selected {
			col = yellow
		}
		for i := 0; i < n; i++ {
			a := bs.Points[i]
			b := bs.Points[(i+1)%n]
			GizmoLine(mgl32.Vec3{a.X(), y, a.Y()}, mgl32.Vec3{b.X(), y, b.Y()}, col)
		}
		if Editor.BiomeLayerActive && selected {
			ortho := editor2DOrtho()
			crossSz := screenToWorldSize(5, ortho)
			for _, p := range bs.Points {
				gp := mgl32.Vec3{p.X(), y, p.Y()}
				GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), yellow)
				GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), yellow)
			}
		}
	}

	if Editor.BiomeDrawing && len(Editor.BiomeDrawPoints) > 0 {
		pts := Editor.BiomeDrawPoints
		for i := 0; i < len(pts)-1; i++ {
			a, b := pts[i], pts[i+1]
			GizmoLine(mgl32.Vec3{a.X(), y, a.Y()}, mgl32.Vec3{b.X(), y, b.Y()}, green)
		}
		ortho := editor2DOrtho()
		crossSz := screenToWorldSize(5, ortho)
		for _, p := range pts {
			gp := mgl32.Vec3{p.X(), y, p.Y()}
			GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), green)
			GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), green)
		}
	}
}

// Editor2DRenderBiomeOverlay draws a translucent fill over every biome
// sector so painted regions are visible at a glance, tinted by their brush
// color. Uses the ImGui background draw list, so it must be called within an
// ImGui NewFrame/Render bracket.
func Editor2DRenderBiomeOverlay() {
	ch := activeChunk()
	if ch == nil || len(ch.BiomeSectors) == 0 {
		return
	}
	dl := imgui.BackgroundDrawList()
	for si := range ch.BiomeSectors {
		bs := &ch.BiomeSectors[si]
		n := len(bs.Points)
		if n < 3 {
			continue
		}
		c := bs.Brush.Color
		a := float32(0.18)
		if si == Editor.ActiveBiomeSectorIdx {
			a = 0.30
		}
		col := imgui.PackedColorFromVec4(imgui.Vec4{X: c.X(), Y: c.Y(), Z: c.Z(), W: a})
		p0x, p0y := worldToScreen(bs.Points[0].X(), bs.Points[0].Y())
		for i := 1; i < n-1; i++ {
			ax, ay := worldToScreen(bs.Points[i].X(), bs.Points[i].Y())
			bx, by := worldToScreen(bs.Points[i+1].X(), bs.Points[i+1].Y())
			dl.AddTriangleFilled(
				imgui.Vec2{X: p0x, Y: p0y},
				imgui.Vec2{X: ax, Y: ay},
				imgui.Vec2{X: bx, Y: by},
				col,
			)
		}
	}
}

// ---------- side panel ----------

// biomePanelSection draws the biome-sector-layer controls inside the 2D side panel.
func biomePanelSection() {
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Biome Sectors", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	ch := activeChunk()
	if ch == nil {
		imgui.Text("No active chunk.")
		return
	}

	imgui.Checkbox("Edit Biome Layer##biomelayer", &Editor.BiomeLayerActive)
	if !Editor.BiomeLayerActive {
		imgui.Text("Enable to draw/select biome sectors in the 2D view.")
		return
	}

	if Editor.BiomeDrawing {
		imgui.Text(fmt.Sprintf("Placing sector (%d pts)", len(Editor.BiomeDrawPoints)))
		imgui.Text("Click: add point. Click near start / Enter: finish. Right-click: undo. Esc: cancel.")
		if imgui.Button("Finish##biomefinish") {
			finishBiomeSectorDraw(ch)
		}
		imgui.SameLine()
		if imgui.Button("Cancel##biomecancel") {
			Editor.BiomeDrawing = false
			Editor.BiomeDrawPoints = nil
		}
		return
	}

	if imgui.Button("+ New Sector##biomenew") {
		Editor.BiomeDrawing = true
		Editor.BiomeDrawPoints = nil
		Editor.ActiveBiomeSectorIdx = -1
	}
	imgui.SameLine()
	if imgui.Button("Refresh Heights##biomerefresh") {
		for i := range ch.BiomeSectors {
			ch.BiomeSectors[i].HeightDirty = true
		}
	}

	imgui.Separator()
	imgui.Text(fmt.Sprintf("Sectors (%d)", len(ch.BiomeSectors)))
	imgui.BeginChildV("##biomesectorlist", imgui.Vec2{X: 0, Y: 120}, true, 0)
	for i := range ch.BiomeSectors {
		bs := &ch.BiomeSectors[i]
		label := fmt.Sprintf("%s (%s)##bs%d", bs.ID, biomeKindName(bs.Brush.Kind), i)
		if imgui.SelectableV(label, i == Editor.ActiveBiomeSectorIdx, 0, imgui.Vec2{}) {
			Editor.ActiveBiomeSectorIdx = i
			Editor.BiomeDragVertexIdx = -1
		}
	}
	imgui.EndChild()

	idx := Editor.ActiveBiomeSectorIdx
	if idx < 0 || idx >= len(ch.BiomeSectors) {
		return
	}
	bs := &ch.BiomeSectors[idx]

	imgui.Separator()
	imgui.Text(fmt.Sprintf("Selected: %s (%d pts)", bs.ID, len(bs.Points)))
	if imgui.Button("Delete##biomedel") {
		EditorPushUndo()
		deleteBiomeSector(ch, idx)
		return
	}

	kinds := []string{"Grass", "Rock"}
	ki := int(bs.Brush.Kind)
	if ki < 0 || ki >= len(kinds) {
		ki = 0
	}
	imgui.Text("Brush")
	if imgui.BeginCombo("##biomekind", kinds[ki]) {
		for i, name := range kinds {
			if imgui.SelectableV(name, i == ki, 0, imgui.Vec2{}) {
				bs.Brush.Kind = int32(i)
			}
		}
		imgui.EndCombo()
	}

	imgui.SliderFloat("Density##bsd", &bs.Brush.Density, 0, 1)
	if bs.Brush.Kind == BiomeRock {
		if bs.Brush.Range <= 0 {
			bs.Brush.Range = defaultRockRange
		}
		editorDragFloat("Draw Dist##bsr", &bs.Brush.Range, 4, 400, "%.1f")
	} else {
		if bs.Brush.Range <= 0 {
			bs.Brush.Range = defaultGrassRange
		}
		editorDragFloat("Draw Dist##bsr", &bs.Brush.Range, 4, 300, "%.1f")
	}

	color := [3]float32{bs.Brush.Color.X(), bs.Brush.Color.Y(), bs.Brush.Color.Z()}
	if imgui.ColorEdit3("Color##bscol", &color) {
		bs.Brush.Color = mgl32.Vec3{color[0], color[1], color[2]}
	}

	if bs.Brush.Kind == BiomeRock {
		sides := bs.Brush.RockSides
		if imgui.SliderInt("Rock Sides##bss", &sides, 3, 16) {
			bs.Brush.RockSides = sides
		}
		if bs.Brush.RockScale <= 0 {
			bs.Brush.RockScale = defaultRockScale
		}
		imgui.SliderFloat("Rock Size##bssz", &bs.Brush.RockScale, 0.05, 3)
	} else {
		imgui.SliderFloat("Grass Height##bsh", &bs.Brush.GrassHeight, 0.05, 3)
		imgui.SliderFloat("Blade Width##bsw", &bs.Brush.GrassWidth, 0.005, 0.3)
	}
}

// biomeKindName returns the display name for a brush kind.
func biomeKindName(kind int32) string {
	if kind == BiomeRock {
		return "Rock"
	}
	return "Grass"
}
