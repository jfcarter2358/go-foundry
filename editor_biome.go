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
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"

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
		if geom.PointInPolygon(pt, ch.BiomeSectors[i].Points) {
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
			ID:     nextBiomeSectorID(Editor.level, ch),
			Points: pts,
			Brush:  defaultBiomeBrush(BiomeGrass),

			HeightDirty: true,
			GroundDirty: true,
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
	EvictBiomeSectorCaches(ch.BiomeSectors[idx].ID)
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
	bs.GroundDirty = true
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
	switch kind {
	case BiomeRock:
		return mgl32.Vec3{0.65, 0.65, 0.7}
	case BiomeWildflower:
		return mgl32.Vec3{0.9, 0.6, 0.75}
	case BiomeCattail:
		return mgl32.Vec3{0.55, 0.4, 0.2}
	case BiomeSand:
		return mgl32.Vec3{0.85, 0.75, 0.45}
	case BiomeWater:
		return mgl32.Vec3{0.3, 0.55, 0.9}
	case BiomeMoss:
		return mgl32.Vec3{0.2, 0.5, 0.25} // darker/duller than Grass, so the two read apart
	default: // BiomeGrass
		return mgl32.Vec3{0.25, 0.85, 0.3}
	}
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
			gizmo.GizmoLine(mgl32.Vec3{a.X(), y, a.Y()}, mgl32.Vec3{b.X(), y, b.Y()}, col)
		}
		if Editor.BiomeLayerActive && selected {
			ortho := editor2DOrtho()
			crossSz := screenToWorldSize(5, ortho)
			for _, p := range bs.Points {
				gp := mgl32.Vec3{p.X(), y, p.Y()}
				gizmo.GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), yellow)
				gizmo.GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), yellow)
			}
		}
	}

	if Editor.BiomeDrawing && len(Editor.BiomeDrawPoints) > 0 {
		pts := Editor.BiomeDrawPoints
		for i := 0; i < len(pts)-1; i++ {
			a, b := pts[i], pts[i+1]
			gizmo.GizmoLine(mgl32.Vec3{a.X(), y, a.Y()}, mgl32.Vec3{b.X(), y, b.Y()}, green)
		}
		ortho := editor2DOrtho()
		crossSz := screenToWorldSize(5, ortho)
		for _, p := range pts {
			gp := mgl32.Vec3{p.X(), y, p.Y()}
			gizmo.GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), green)
			gizmo.GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), green)
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
		// A triangle fan from Points[0] only fills correctly for convex
		// polygons; concave sectors need real ear-clip triangulation or the
		// fan bulges outside the boundary. EarClip assumes CCW input.
		poly := ccwPolygon(bs.Points)
		tris := geom.EarClip(poly)
		for i := 0; i+2 < len(tris); i += 3 {
			ax, ay := worldToScreen(poly[tris[i]].X(), poly[tris[i]].Y())
			bx, by := worldToScreen(poly[tris[i+1]].X(), poly[tris[i+1]].Y())
			cx, cy := worldToScreen(poly[tris[i+2]].X(), poly[tris[i+2]].Y())
			dl.AddTriangleFilled(
				imgui.Vec2{X: ax, Y: ay},
				imgui.Vec2{X: bx, Y: by},
				imgui.Vec2{X: cx, Y: cy},
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
			ch.BiomeSectors[i].GroundDirty = true
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

	kinds := []string{"Grass", "Rock", "Wildflower", "Cattail", "Sand", "Water", "Moss"}
	ki := int(bs.Brush.Kind)
	if ki < 0 || ki >= len(kinds) {
		ki = 0
	}
	imgui.Text("Brush")
	if imgui.BeginCombo("##biomekind", kinds[ki]) {
		for i, name := range kinds {
			if imgui.SelectableV(name, i == ki, 0, imgui.Vec2{}) {
				if int32(i) != bs.Brush.Kind {
					// Reset to that kind's defaults rather than carrying over
					// stale field values (a Rock's RockScale left on a
					// sector switched to Grass, etc.) — every field the new
					// kind actually reads starts sensible.
					bs.Brush = defaultBiomeBrush(int32(i))
					bs.HeightDirty = true
					bs.GroundDirty = true
				}
			}
		}
		imgui.EndCombo()
	}

	// Moss's scatter is opt-in (ground patch alone is the default look), and
	// this toggle gates whether Density/Draw Dist below mean anything at all —
	// so it has to come before them rather than in the per-kind switch.
	if bs.Brush.Kind == BiomeMoss {
		imgui.Checkbox("Scatter Clumps##bsmossscat", &bs.Brush.MossScatter)
	}

	if bs.Brush.Scatters() {
		imgui.SliderFloat("Density##bsd", &bs.Brush.Density, 0, 1)
		maxRange := float32(300)
		if bs.Brush.Kind == BiomeRock {
			maxRange = 400
		}
		if bs.Brush.Range <= 0 {
			bs.Brush.Range = defaultBiomeBrush(bs.Brush.Kind).Range
		}
		editorDragFloat("Draw Dist##bsr", &bs.Brush.Range, 4, maxRange, "%.1f")
	}

	if bs.Brush.Kind != BiomeSand && bs.Brush.Kind != BiomeWater {
		color := [3]float32{bs.Brush.Color.X(), bs.Brush.Color.Y(), bs.Brush.Color.Z()}
		if imgui.ColorEdit3("Color##bscol", &color) {
			bs.Brush.Color = mgl32.Vec3{color[0], color[1], color[2]}
		}
	}

	switch bs.Brush.Kind {
	case BiomeRock:
		sides := bs.Brush.RockSides
		if imgui.SliderInt("Rock Sides##bss", &sides, 3, 16) {
			bs.Brush.RockSides = sides
		}
		if bs.Brush.RockScale <= 0 {
			bs.Brush.RockScale = defaultRockScale
		}
		imgui.SliderFloat("Rock Size##bssz", &bs.Brush.RockScale, 0.05, 3)

	case BiomeWildflower:
		imgui.SliderFloat("Stem Height##bsh", &bs.Brush.GrassHeight, 0.05, maxStemHeight)
		imgui.SliderFloat("Stem Width##bsw", &bs.Brush.GrassWidth, 0.005, 0.2)
		if bs.Brush.HeadScale <= 0 {
			bs.Brush.HeadScale = defaultWildflowerHeadScale
		}
		imgui.SliderFloat("Bloom Size##bshead", &bs.Brush.HeadScale, 0.02, 1.5)

		// Basal leaves. Like the bloom center, they live in the shared
		// template and the shader degenerates them away when off, so this is
		// a pure uniform flip — no dirty flag or rebuild.
		imgui.Checkbox("Leaves##bsleaves", &bs.Brush.StemLeaves)
		if bs.Brush.StemLeaves {
			imgui.Text("Triangular leaves arcing out from the stem's base.")
			// Resolve through the same "0 means the default" path the renderer
			// uses, so the slider shows the size actually in effect rather
			// than a misleading 0 on a brush from before this field existed.
			bs.Brush.LeafScale = resolvedLeafScale(bs.Brush.LeafScale)
			imgui.SliderFloat("Leaf Size##bsleafsz", &bs.Brush.LeafScale, 0.2, maxWildflowerLeafScale)
		} else {
			imgui.Text("Bare stem.")
		}

		// Bloom shape/color options. None of these touch the mesh — the
		// template carries both a unit bloom blob and a unit center blob, and
		// the shader shapes, colors and (for the center) shows/hides them from
		// these flags — so no dirty flag or rebuild is needed here.
		imgui.Checkbox("Spike Bloom##bsspike", &bs.Brush.BloomSpike)
		if bs.Brush.BloomSpike {
			imgui.Text("Elongated, cattail-like bloom.")
		} else {
			imgui.Text("Squat petal disc.")
		}

		// Presented as the positive "random" framing, but stored inverted
		// (BloomSolidColor) so the zero value is the pre-existing random-hue
		// behavior and old saves need no migration.
		randomColors := !bs.Brush.BloomSolidColor
		if imgui.Checkbox("Random Bloom Colors##bsrand", &randomColors) {
			bs.Brush.BloomSolidColor = !randomColors
		}
		if bs.Brush.BloomSolidColor {
			imgui.Text("Bloom uses Color above directly.")
		} else {
			imgui.Text("Bloom picks a random hue, tinted by Color above.")
		}

		imgui.Checkbox("Bloom Center##bscenter", &bs.Brush.BloomCenter)
		if bs.Brush.BloomCenter {
			if bs.Brush.CenterColor == (mgl32.Vec3{}) {
				bs.Brush.CenterColor = defaultCenterColor
			}
			cc := [3]float32{bs.Brush.CenterColor.X(), bs.Brush.CenterColor.Y(), bs.Brush.CenterColor.Z()}
			if imgui.ColorEdit3("Center Color##bsccol", &cc) {
				bs.Brush.CenterColor = mgl32.Vec3{cc[0], cc[1], cc[2]}
			}
		}

	case BiomeCattail:
		imgui.SliderFloat("Reed Height##bsh", &bs.Brush.GrassHeight, 0.1, 2.5)
		imgui.SliderFloat("Reed Width##bsw", &bs.Brush.GrassWidth, 0.005, 0.15)
		if bs.Brush.HeadScale <= 0 {
			bs.Brush.HeadScale = defaultCattailHeadScale
		}
		imgui.SliderFloat("Seed Head Size##bshead", &bs.Brush.HeadScale, 0.02, 1.5)

	case BiomeSand:
		if bs.Brush.SandHeight <= 0 {
			bs.Brush.SandHeight = defaultSandHeight
		}
		if imgui.SliderFloat("Dune Height##bssand", &bs.Brush.SandHeight, 0.02, 2) {
			bs.GroundDirty = true
		}
		imgui.Text("Volumetric displacement, no scatter — paint Rock on top for outcrops.")

	case BiomeWater:
		biomeWaterControls(ch, bs)
		imgui.Text("Auto-fills the painted area with a water plane.")

	case BiomeMoss:
		if bs.Brush.MossScatter {
			if bs.Brush.RockScale <= 0 {
				bs.Brush.RockScale = defaultRockScale
			}
			imgui.SliderFloat("Clump Size##bsmosssz", &bs.Brush.RockScale, 0.05, 1.5)
		} else {
			imgui.Text("Ground patch only — enable Scatter Clumps for 3D growth.")
		}

	default: // BiomeGrass
		imgui.SliderFloat("Grass Height##bsh", &bs.Brush.GrassHeight, 0.05, maxStemHeight)
		imgui.SliderFloat("Blade Width##bsw", &bs.Brush.GrassWidth, 0.005, 0.3)

		// Pure shader uniform — clumping only rearranges where the same
		// instances land, so it touches neither the mesh nor the
		// height/coverage texture and needs no dirty flag or rebuild.
		imgui.SliderFloat("Clumping##bsclump", &bs.Brush.GrassClump, 0, 1)
		if bs.Brush.GrassClump <= 0 {
			imgui.Text("Blades scattered evenly.")
		} else {
			imgui.Text("Blades gather into plant-like tufts, arcing outward.")
		}
	}

	biomeSlopeSection(bs)
	biomeLODSection(bs)
	biomeFadeSection(bs)
	biomeGroundSection(bs)
}

// biomeLODSection draws the Near/Mid/Far distance-based LOD controls (see
// grass.go's package doc comment and biomeLODBounds) — only shown for the
// kinds that actually render three tiers (biomeLODTierCount). Sliders edit
// world-unit distances, clamped 0 < Near <= Mid <= Draw Dist, mirroring how
// Range's own "Draw Dist" slider already works.
func biomeLODSection(bs *BiomeSector) {
	if biomeLODTierCount(bs.Brush.Kind) != 3 {
		return
	}

	imgui.Separator()
	if !imgui.CollapsingHeaderV("Level of Detail##bslod", 0) {
		return
	}

	imgui.Text("Mesh detail drops Near -> Mid -> Far with distance; only Near")
	imgui.Text("casts real per-instance shadows by default (F9 changes that).")

	rng := biomeSectorRange(&bs.Brush)
	lod1 := resolvedLOD1(&bs.Brush)
	if editorDragFloat("Near Dist##bslod1", &lod1, 1, rng, "%.1f") {
		bs.Brush.LOD1Distance = lod1
		if bs.Brush.LOD2Distance > 0 && bs.Brush.LOD2Distance < lod1 {
			bs.Brush.LOD2Distance = lod1
		}
	}

	lod2 := resolvedLOD2(&bs.Brush)
	if editorDragFloat("Mid Dist##bslod2", &lod2, lod1, rng, "%.1f") {
		bs.Brush.LOD2Distance = lod2
	}

	imgui.Text(fmt.Sprintf("Far tier spans Mid Dist to Draw Dist (%.0f).", rng))
}

// biomeFadeSection draws the soft-edge controls: how the scatter's two
// boundaries (the outer camera-ring and the sector's own painted polygon) fade
// instead of cutting off hard. See BiomeBrush's own doc comments for what each
// field does.
func biomeFadeSection(bs *BiomeSector) {
	if !bs.Brush.Scatters() {
		return
	}

	imgui.Separator()
	if !imgui.CollapsingHeaderV("Soft Edges##bsfade", 0) {
		return
	}

	imgui.Text("Range Fade — the outer camera-ring boundary.")
	rangeFrac := bs.Brush.RangeFadeFraction
	if imgui.SliderFloat("Range Fade##bsrfade", &rangeFrac, 0, 1) {
		bs.Brush.RangeFadeFraction = rangeFrac
	}
	if bs.Brush.RangeFadeFraction <= 0 {
		imgui.Text("Hard cutoff at Draw Dist.")
	} else {
		imgui.Text("Instances shrink away over the fraction shown, above.")
	}

	imgui.Text("Edge Fade — the sector's own painted boundary.")
	// A real per-instance distance-to-polygon query (see polyEdgeDist in
	// biome_scatter_common.glsl), so — unlike Range Fade — this costs
	// something and defaults to off (0) rather than a nonzero look.
	if editorDragFloat("Edge Fade Dist##bsefade", &bs.Brush.EdgeFadeDistance, 0, 15, "%.2f") {
		if bs.Brush.EdgeFadeDistance < 0 {
			bs.Brush.EdgeFadeDistance = 0
		}
	}
	if bs.Brush.EdgeFadeDistance <= 0 {
		imgui.Text("Hard cutoff at the drawn polygon edge.")
	} else {
		imgui.Text("Instances grow in over that many world units from the edge.")
	}

	imgui.Text("Boundary Jitter — bulges Draw Dist (and any LOD rings)")
	imgui.Text("so the transition doesn't read as a circle around the camera.")
	jitter := resolvedBoundaryJitter(&bs.Brush)
	if imgui.SliderFloat("Jitter##bsbjit", &jitter, 0, 15) {
		bs.Brush.BoundaryJitter = jitter
	}
}

// biomeSlopeSection draws the slope-range controls that restrict where
// Grass/Sand/Rock/Moss scatter appears — the "only below a certain angle"
// gating (biome_displace.go). Applies to however the ground got its shape:
// procedural displacement detail, terrain mode, or plain per-vertex offsets.
// Hidden entirely for the kinds that never follow displacement
// (Wildflower/Cattail/Water), since the fields would do nothing for them.
func biomeSlopeSection(bs *BiomeSector) {
	if !biomeFollowsDisplacement(bs.Brush.Kind) {
		return
	}

	imgui.Separator()
	if !imgui.CollapsingHeaderV("Slope Range##bsslope", 0) {
		return
	}
	imgui.Text("Scatter only appears where the ground's angle is in range.")
	imgui.Text("0..90 covers everything. Sloped, terrain and displaced floors all count.")

	// Resolve through the same "0 means the default" path the renderer uses, so
	// the sliders show the values actually in effect rather than a misleading 0.
	minA := resolvedMinSlope(bs.Brush.Kind, bs.Brush.MinSlopeAngle)
	maxA := resolvedMaxSlope(bs.Brush.Kind, bs.Brush.MaxSlopeAngle)

	changed := false
	if imgui.SliderFloat("Min Angle##bsslopemin", &minA, 0, 90) {
		changed = true
	}
	// Max starts at 1, not 0: a stored 0 means "unset" and resolves to the full
	// 90, so a slider that could reach 0 would flip "show nothing" into "show
	// everything" at the far left. Min has no such problem — 0 stored and 0
	// resolved both mean "no lower limit".
	if imgui.SliderFloat("Max Angle##bsslopemax", &maxA, 1, 90) {
		changed = true
	}
	if changed {
		// Keep min <= max whichever one moved, so the range can never invert
		// into "covers nothing" (which reads as the biome silently vanishing).
		if minA > maxA {
			minA, maxA = maxA, minA
		}
		bs.Brush.MinSlopeAngle = minA
		bs.Brush.MaxSlopeAngle = maxA
		// Coverage is baked into BOTH sector-derived meshes rather than being a
		// live uniform — the scatter height/coverage texture (HeightDirty) and
		// the ground patch's per-vertex aCoverage (GroundDirty) — so a slope
		// edit has to invalidate both or the patch keeps its old cutoff.
		bs.HeightDirty = true
		bs.GroundDirty = true
	}
}

// biomeGroundSection draws the controls for the conforming ground patch
// every kind renders underneath it (biome_ground.go) — which texture it's
// painted with, and how its procedural noise displacement is shaped. Applies
// to every kind, including Water (its patch is the lakebed seen through the
// translucent surface).
func biomeGroundSection(bs *BiomeSector) {
	def := defaultBiomeBrush(bs.Brush.Kind)

	imgui.Separator()
	if !imgui.CollapsingHeaderV("Ground##bsground", 0) {
		return
	}
	if bs.Brush.Kind == BiomeWater {
		imgui.Text("Lakebed (seen through the water surface)")
	}

	// ensureGroundPatch already rebuilds whenever the resolved texture no
	// longer matches its cache, so the change doesn't need to set GroundDirty.
	textureCombo("Ground Texture##bsgtex", &bs.Brush.GroundTexture, true)

	// Texture tiling, in the same world-XZ units as a structural floor's UV
	// Scale (editorUVControls) — set these to the covered floor's numbers and
	// the patch tiles at exactly its texel size. Baked into the patch mesh's
	// UVs, so it needs a rebuild.
	uv := resolveGroundUVScale(&bs.Brush)
	uvArr := [2]float32{uv.X(), uv.Y()}
	if editorDragFloat("Texture Scale U##bsguvu", &uvArr[0], 0.01, 100, "%.3f") ||
		editorDragFloat("Texture Scale V##bsguvv", &uvArr[1], 0.01, 100, "%.3f") {
		bs.Brush.GroundUVScale = mgl32.Vec2{uvArr[0], uvArr[1]}
		bs.GroundDirty = true
	}

	if bs.Brush.Kind != BiomeSand {
		// Sand's amplitude is the dedicated SandHeight/"Dune Height" slider
		// above instead — much larger range, and already the sand-specific
		// control users expect.
		if bs.Brush.GroundAmplitude <= 0 {
			bs.Brush.GroundAmplitude = def.GroundAmplitude
		}
		ampLabel := "Roughness##bsgamp"
		if bs.Brush.Kind == BiomeWater {
			ampLabel = "Lakebed Roughness##bsgamp"
		}
		if imgui.SliderFloat(ampLabel, &bs.Brush.GroundAmplitude, 0.001, 1) {
			bs.GroundDirty = true
		}
	}

	if bs.Brush.GroundFrequency <= 0 {
		bs.Brush.GroundFrequency = def.GroundFrequency
	}
	if imgui.SliderFloat("Detail Frequency##bsgfreq", &bs.Brush.GroundFrequency, 0.01, 1) {
		bs.GroundDirty = true
	}

	octaves := bs.Brush.GroundOctaves
	if octaves <= 0 {
		octaves = def.GroundOctaves
	}
	if imgui.SliderInt("Detail Octaves##bsgoct", &octaves, 1, 6) {
		bs.Brush.GroundOctaves = octaves
		bs.GroundDirty = true
	}
}

// biomeWaterControls draws a Water-kind sector's depth and wave settings. Split
// out of biomePanelSection because the 3D view reaches the same settings by a
// different route: clicking the water plane itself (editorWaterSection,
// editor_3d.go), which lands on the sector that produced it.
func biomeWaterControls(ch *Chunk, bs *BiomeSector) {
	if bs.Brush.WaterDepth <= 0 {
		bs.Brush.WaterDepth = defaultWaterDepth
	}
	if editorDragFloat("Depth##bswd", &bs.Brush.WaterDepth, 0.05, 20, "%.2f") {
		bs.GroundDirty = true // moves the plane's Y — needs a mesh rebuild
	}
	if !bs.Brush.Water.Enabled {
		bs.Brush.Water = gfx.DefaultWaterParams()
	}
	// Push wave/reflection edits straight to the live companion surface's
	// Geometry when it already exists — these are pure shader uniforms, so this
	// avoids rebuilding the mesh on every slider-drag frame (see waterControls's
	// own doc comment). Falls back to a scratch Geometry (still edits
	// bs.Brush.Water) before the first sync.
	g := biomeWaterGeometryFor(ch, bs.ID)
	if g == nil {
		g = &gfx.Geometry{}
	}
	waterControls("bswater", &bs.Brush.Water, g)
}

// biomeWaterGeometryFor returns the live Geometry of sector id's companion
// water surface (see biome_water.go), or nil before the first sync has run for
// a newly-switched-to-Water sector.
func biomeWaterGeometryFor(ch *Chunk, id string) *gfx.Geometry {
	for i := range ch.WaterSurfaces {
		if ch.WaterSurfaces[i].ID == id {
			return &ch.WaterSurfaces[i].Geometry
		}
	}
	return nil
}

// biomeKindName returns the display name for a brush kind.
func biomeKindName(kind int32) string {
	switch kind {
	case BiomeRock:
		return "Rock"
	case BiomeWildflower:
		return "Wildflower"
	case BiomeCattail:
		return "Cattail"
	case BiomeSand:
		return "Sand"
	case BiomeWater:
		return "Water"
	case BiomeMoss:
		return "Moss"
	default:
		return "Grass"
	}
}
