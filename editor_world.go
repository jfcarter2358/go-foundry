package main

// editor_world.go — procedural world builder editor mode (ModeWorld).
//
// A top-down orthographic brush editor that reuses the Mode2D camera (Pan2D /
// Zoom2D and the screenToWorld/worldToScreen helpers). Height tools sculpt the
// grid heightmap; paint tools set per-cell terrain type and city tags. After
// each stroke only the affected cell sectors are rebaked.

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// World brush tools.
const (
	WorldToolRaise = iota
	WorldToolLower
	WorldToolSmooth
	WorldToolFlatten
	WorldToolPaintTex
	WorldToolPaintCity
	WorldToolPaintBiome
	WorldToolErase
	WorldToolRoad // free-hand road stroke (true diagonal)
)

// GPU-scattered biome kinds selectable by the Paint Biome tool.
const (
	BiomeGrass = iota
	BiomeRock
)

// Brush falloff curves.
const (
	FalloffSmooth = iota
	FalloffLinear
	FalloffConstant
)

// activeWorldChunk returns the active chunk if it is a baked world chunk.
func activeWorldChunk() *Chunk {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return nil
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if ch.WorldGrid == nil {
		return nil
	}
	return ch
}

// EnterWorldEditor switches into world-builder mode, freeing the mouse and
// centering the view on the active world grid if one exists.
func EnterWorldEditor() {
	Editor.Mode = ModeWorld
	Cam.MouseLock = false
	sdl.ShowCursor(sdl.ENABLE)
	Window.SetGrab(false)
	if ch := activeWorldChunk(); ch != nil {
		g := ch.WorldGrid
		Editor.Pan2D = g.cellCenterWorld(g.Cols/2, g.Rows/2)
	} else {
		Editor.Pan2D = mgl32.Vec2{Cam.Position.X(), Cam.Position.Z()}
	}
}

// ---------- input ----------

// WorldHandleClick processes a mouse-button-down in world mode.
func WorldHandleClick(t sdl.MouseButtonEvent) {
	if t.Button != sdl.ButtonLeft {
		return
	}
	wx, wz := screenToWorld(t.X, t.Y)
	Editor.WorldMouse = mgl32.Vec2{wx, wz}
	if ch := activeWorldChunk(); ch != nil && Editor.WorldTool == WorldToolFlatten {
		Editor.WorldFlattenTarget = ch.WorldGrid.HeightAt(wx, wz)
	}
	Editor.WorldPainting = true
	if Editor.WorldTool == WorldToolRoad {
		// Begin a fresh free-hand road stroke.
		Editor.WorldRoadStroke = []mgl32.Vec2{{wx, wz}}
		return
	}
	WorldApplyBrush(wx, wz)
}

// WorldHandleRelease processes a mouse-button-up in world mode. Finishing a road
// stroke (or an erase that removed one) recarves the world geometry so the change
// shows immediately — no "Generate City" click required.
func WorldHandleRelease(t sdl.MouseButtonEvent) {
	if t.Button != sdl.ButtonLeft {
		return
	}
	Editor.WorldPainting = false

	if Editor.WorldTool == WorldToolRoad {
		if ch := activeWorldChunk(); ch != nil && len(Editor.WorldRoadStroke) >= 2 {
			w := Editor.WorldRoadWidth
			if w <= 0 {
				w = ch.WorldGrid.CellSize * 0.6
			}
			ch.WorldGrid.Roads = append(ch.WorldGrid.Roads, RoadStroke{
				Points: append([]mgl32.Vec2(nil), Editor.WorldRoadStroke...),
				Width:  w,
			})
			Editor.WorldNeedsRecarve = true
		}
		Editor.WorldRoadStroke = nil
	}

	if Editor.WorldNeedsRecarve {
		Editor.WorldNeedsRecarve = false
		generateCityForActive()
	}
}

// WorldHandleMouseMove tracks the cursor and supports middle-mouse panning.
func WorldHandleMouseMove(t sdl.MouseMotionEvent) {
	wx, wz := screenToWorld(t.X, t.Y)
	Editor.WorldMouse = mgl32.Vec2{wx, wz}

	if t.State&sdl.ButtonMMask != 0 {
		dx := float32(t.X-Editor.LastMouse2D[0]) / Editor.Zoom2D
		dy := float32(t.Y-Editor.LastMouse2D[1]) / Editor.Zoom2D
		Editor.Pan2D[0] -= dx
		Editor.Pan2D[1] -= dy
	}
	Editor.LastMouse2D = [2]int32{t.X, t.Y}
}

// WorldTick applies the brush each frame while painting, and handles WASD pan.
func WorldTick(keyStates []uint8) {
	panSpeed := float32(5.0) / Editor.Zoom2D * 10
	if keyStates[sdl.SCANCODE_W] != 0 {
		Editor.Pan2D[1] -= panSpeed
	}
	if keyStates[sdl.SCANCODE_S] != 0 {
		Editor.Pan2D[1] += panSpeed
	}
	if keyStates[sdl.SCANCODE_A] != 0 {
		Editor.Pan2D[0] -= panSpeed
	}
	if keyStates[sdl.SCANCODE_D] != 0 {
		Editor.Pan2D[0] += panSpeed
	}
	if Editor.WorldPainting {
		WorldApplyBrush(Editor.WorldMouse.X(), Editor.WorldMouse.Y())
	}
}

// brushFalloff returns the brush weight at normalized distance t in [0,1].
func brushFalloff(t float32, mode int) float32 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	switch mode {
	case FalloffLinear:
		return 1 - t
	case FalloffConstant:
		return 1
	default: // smooth
		return 0.5 * (1 + float32(math.Cos(float64(t)*math.Pi)))
	}
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// neighborAvg returns the average height of the orthogonal neighbors of vertex.
func gridNeighborAvg(g *WorldGrid, vx, vy int) float32 {
	sum := float32(0)
	n := 0
	add := func(x, y int) {
		if idx := g.vIdx(x, y); idx >= 0 {
			sum += g.Heights[idx]
			n++
		}
	}
	add(vx-1, vy)
	add(vx+1, vy)
	add(vx, vy-1)
	add(vx, vy+1)
	if n == 0 {
		return g.Heights[g.vIdx(vx, vy)]
	}
	return sum / float32(n)
}

// markCellsAroundVertex adds the up-to-4 cells sharing a vertex to the set.
func markCellsAroundVertex(g *WorldGrid, vx, vy int, touched map[int]bool) {
	for _, c := range [4][2]int{{vx - 1, vy - 1}, {vx, vy - 1}, {vx - 1, vy}, {vx, vy}} {
		if ci := g.cIdx(c[0], c[1]); ci >= 0 {
			touched[ci] = true
		}
	}
}

// WorldApplyBrush applies the current tool at world position (wx,wz) and rebakes
// the affected cells.
func WorldApplyBrush(wx, wz float32) {
	ch := activeWorldChunk()
	if ch == nil {
		return
	}
	g := ch.WorldGrid
	radius := Editor.WorldRadius
	if radius <= 0 {
		radius = 1
	}

	// Free-hand road: append to the in-progress stroke (spaced) — no cell edits.
	if Editor.WorldTool == WorldToolRoad {
		p := mgl32.Vec2{wx, wz}
		spacing := g.CellSize * 0.3
		if spacing < 0.3 {
			spacing = 0.3
		}
		n := len(Editor.WorldRoadStroke)
		if n == 0 {
			Editor.WorldRoadStroke = append(Editor.WorldRoadStroke, p)
		} else if last := Editor.WorldRoadStroke[n-1]; p.Sub(last).Len() >= spacing {
			Editor.WorldRoadStroke = append(Editor.WorldRoadStroke, p)
		}
		return
	}

	touched := map[int]bool{}

	switch Editor.WorldTool {
	case WorldToolRaise, WorldToolLower, WorldToolSmooth, WorldToolFlatten:
		amt := Editor.WorldStrength * DeltaTime
		dir := float32(1)
		if Editor.WorldTool == WorldToolLower {
			dir = -1
		}
		for vy := 0; vy <= g.Rows; vy++ {
			for vx := 0; vx <= g.Cols; vx++ {
				vp := g.vertexWorld(vx, vy)
				d := dist2D(wx, wz, vp.X(), vp.Y())
				if d > radius {
					continue
				}
				w := brushFalloff(d/radius, Editor.WorldFalloff)
				idx := g.vIdx(vx, vy)
				switch Editor.WorldTool {
				case WorldToolRaise, WorldToolLower:
					g.Heights[idx] += dir * amt * w
				case WorldToolFlatten:
					g.Heights[idx] += (Editor.WorldFlattenTarget - g.Heights[idx]) * w * clamp01(amt)
				case WorldToolSmooth:
					avg := gridNeighborAvg(g, vx, vy)
					g.Heights[idx] += (avg - g.Heights[idx]) * w * clamp01(amt)
				}
				markCellsAroundVertex(g, vx, vy, touched)
			}
		}
	case WorldToolPaintTex, WorldToolPaintCity, WorldToolPaintBiome, WorldToolErase:
		for cy := 0; cy < g.Rows; cy++ {
			for cx := 0; cx < g.Cols; cx++ {
				cp := g.cellCenterWorld(cx, cy)
				d := dist2D(wx, wz, cp.X(), cp.Y())
				if d > radius {
					continue
				}
				ci := g.cIdx(cx, cy)
				switch Editor.WorldTool {
				case WorldToolPaintTex:
					g.CellType[ci] = Editor.WorldPaintType
					// Roads can't host buildings — clear any city tag so roads
					// can be drawn straight through a painted city region.
					if Editor.WorldPaintType == TerrainRoad {
						g.CellCity[ci] = 0
					}
					touched[ci] = true
				case WorldToolPaintCity:
					// Don't tag roads or water as city.
					if g.CellType[ci] != TerrainRoad && g.CellType[ci] != TerrainRiver {
						g.CellCity[ci] = Editor.WorldCityID
					}
					touched[ci] = true
				case WorldToolPaintBiome:
					// Biome coverage is a GPU-scatter mask only — it never changes
					// the baked terrain, so these cells are NOT added to `touched`
					// (no rebake). Feather by falloff and accumulate via max so a
					// stroke's center stays dense while its edge fades in.
					w := brushFalloff(d/radius, Editor.WorldFalloff)
					target := clamp01(Editor.WorldBiomeDensity) * w
					dst := &g.GrassDensity
					if Editor.WorldBiome == BiomeRock {
						dst = &g.RockDensity
					}
					if ci < len(*dst) && target > (*dst)[ci] {
						(*dst)[ci] = target
					}
				case WorldToolErase:
					g.CellCity[ci] = 0
					g.CellType[ci] = TerrainGeneral
					if ci < len(g.GrassDensity) {
						g.GrassDensity[ci] = 0
					}
					if ci < len(g.RockDensity) {
						g.RockDensity[ci] = 0
					}
					touched[ci] = true
				}
			}
		}
		// Erase also deletes any road stroke passing under the brush.
		if Editor.WorldTool == WorldToolErase {
			if eraseRoadStrokesNear(g, mgl32.Vec2{wx, wz}, radius) {
				Editor.WorldNeedsRecarve = true
			}
		}
	}

	cells := make([]int, 0, len(touched))
	for ci := range touched {
		cells = append(cells, ci)
	}
	g.RebakeCells(ch, cells)

	// Height sculpting moves the surface grass/rocks sit on, and biome/erase
	// strokes change coverage — either way the GPU biome textures are stale.
	MarkBiomesDirty()
}

// ---------- world creation / city generation ----------

// createWorld creates a brand-new level file containing only the baked world
// grid (plus default lights and an invisible player), then loads it so the
// editor is now working on that level rather than the previously open one.
func createWorld(name string, cols, rows int, cellSize float32, seed int64) {
	if name == "" {
		name = "world"
	}
	// Grid centered on the world origin.
	origin := mgl32.Vec2{-float32(cols) * cellSize / 2, -float32(rows) * cellSize / 2}
	g := NewWorldGrid(cols, rows, cellSize, origin, seed, "world")
	chunk := g.BakeChunk()

	lvl := &Level{
		Chunks: []Chunk{chunk},
		Entities: []Entity{{
			ID:           "player",
			Position:     mgl32.Vec3{0, 2, 0},
			Scale:        mgl32.Vec3{1, 1, 1},
			BoundingKind: BoundingAABB,
			BoundHalfX:   0.3,
			BoundHalfY:   0.9,
			BoundHalfZ:   0.3,
			Alpha:        1,
		}},
		Lights: LightSet{
			Global: GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.45},
			Directional: []DirectionalLight{{
				Direction:      mgl32.Vec3{-0.3, -1, -0.2},
				Color:          mgl32.Vec3{1, 1, 1},
				Brightness:     0.8,
				CastShadows:    true,
				ShadowStrength: 0.5,
			}},
		},
	}

	lvl.Spatial = BuildSpatialHash(lvl.Chunks, 8.0)

	// Swap the level in-memory (do NOT round-trip through YAML, which would
	// reorder the chunk's point pool and break grid↔sector indexing).
	*Editor.level = *lvl
	UploadLights(Editor.level.Lights)
	PlayerEntity = nil
	for i := range Editor.level.Entities {
		if Editor.level.Entities[i].ID == "player" {
			PlayerEntity = &Editor.level.Entities[i]
			break
		}
	}

	// Ensure terrain + building textures are uploaded.
	style := resolveBuildingStyle(g.StyleName)
	texs := append([]string{"dirt", "stone_floor", "concrete", style.FloorTexture}, style.Textures...)
	ensureTexturesLoaded(texs)

	// Reset editor selection state.
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1

	path := filepath.Join(Editor.LevelsDir, name+".yaml")
	Editor.SavePath = path
	if err := SaveLevel(Editor.level, path); err != nil {
		Editor.StatusMsg = fmt.Sprintf("New world save failed: %v", err)
	} else {
		Editor.StatusMsg = fmt.Sprintf("Created world level %s.yaml", name)
	}
	Editor.StatusTime = sdl.GetTicks()

	Editor.Mode = ModeWorld
	Editor.Pan2D = mgl32.Vec2{0, 0}
}

// generateCityForActive (re)generates the city for the active world chunk.
func generateCityForActive() {
	ch := activeWorldChunk()
	if ch == nil {
		Editor.StatusMsg = "No active world chunk"
		Editor.StatusTime = sdl.GetTicks()
		return
	}
	g := ch.WorldGrid
	style := resolveBuildingStyle(g.StyleName)
	names := GenerateCity(ch, g, style)
	ensureTexturesLoaded(names)
	Editor.level.Spatial = BuildSpatialHash(Editor.level.Chunks, 8.0)
	Editor.StatusMsg = "City generated"
	Editor.StatusTime = sdl.GetTicks()
}

// ---------- render ----------

// WorldEditorRender draws the bounded cell grid (tinted by terrain type and
// shaded by height so edits are visible), city tags, and the brush ring.
func WorldEditorRender() {
	ch := activeWorldChunk()
	ortho := editor2DOrtho()

	if ch != nil {
		g := ch.WorldGrid

		// Height range for shading.
		minH, maxH := float32(0), float32(0)
		for i, h := range g.Heights {
			if i == 0 || h < minH {
				minH = h
			}
			if i == 0 || h > maxH {
				maxH = h
			}
		}
		rng := maxH - minH

		for cy := 0; cy < g.Rows; cy++ {
			for cx := 0; cx < g.Cols; cx++ {
				ci := g.cIdx(cx, cy)
				col := worldCellColor(g.CellType[ci], g.CellCity[ci] > 0)
				if rng > 1e-4 {
					hh := g.cellCornerHeights(cx, cy)
					avg := (hh[0] + hh[1] + hh[2] + hh[3]) / 4
					col = col.Mul(0.45 + 0.55*(avg-minH)/rng)
				}
				worldGizmoCellOutline(g, cx, cy, col)

				// Biome coverage feedback: painted grass/rock draw an inset marker
				// tinted by kind and brightened by painted density.
				grass, rock := g.biomeDensityAt(cx, cy)
				if grass > 0.01 || rock > 0.01 {
					worldGizmoCellBiome(g, cx, cy, grass, rock)
				}
			}
		}

		// Legacy per-cell roads: bright line connecting road-cell centers.
		road := mgl32.Vec3{0.98, 0.85, 0.15}
		for cy := 0; cy < g.Rows; cy++ {
			for cx := 0; cx < g.Cols; cx++ {
				if g.CellType[g.cIdx(cx, cy)] != TerrainRoad {
					continue
				}
				c := g.cellCenterWorld(cx, cy)
				cp := mgl32.Vec3{c.X(), 0.12, c.Y()}
				if cellIsRoad(g, cx+1, cy) {
					n := g.cellCenterWorld(cx+1, cy)
					GizmoLine(cp, mgl32.Vec3{n.X(), 0.12, n.Y()}, road)
				}
				if cellIsRoad(g, cx, cy+1) {
					n := g.cellCenterWorld(cx, cy+1)
					GizmoLine(cp, mgl32.Vec3{n.X(), 0.12, n.Y()}, road)
				}
			}
		}

		// Free-hand road strokes: draw the actual ribbon (smoothed centerline plus
		// both offset edges) so the true diagonal road shape shows live as you draw.
		for _, s := range g.Roads {
			worldGizmoRoadRibbon(s.Points, roadStrokeWidth(g, s.Width), road)
		}
		if Editor.WorldTool == WorldToolRoad && len(Editor.WorldRoadStroke) > 0 {
			worldGizmoRoadRibbon(Editor.WorldRoadStroke, roadStrokeWidth(g, Editor.WorldRoadWidth), mgl32.Vec3{1, 1, 0.3})
		}
	} else {
		// No world yet — show the generic grid as a hint.
		drawEditorGrid(Editor.Pan2D, Editor.Zoom2D)
	}

	// Brush cursor ring.
	worldGizmoRing(Editor.WorldMouse, Editor.WorldRadius, mgl32.Vec3{1, 1, 0})

	GizmoFlush(ortho)
}

// worldGizmoRoadRibbon draws a road stroke as its true smoothed ribbon: the
// centerline plus both offset edges, so a diagonally-painted stroke reads as a
// diagonal road strip in the paint view (matching what carving produces).
func worldGizmoRoadRibbon(pts []mgl32.Vec2, width float32, col mgl32.Vec3) {
	if len(pts) < 2 {
		return
	}
	sm := chaikin(pts, 2)
	hw := width * 0.5
	const y = float32(0.14)
	v3 := func(p mgl32.Vec2) mgl32.Vec3 { return mgl32.Vec3{p.X(), y, p.Y()} }
	for i := 0; i+1 < len(sm); i++ {
		a, b := sm[i], sm[i+1]
		GizmoLine(v3(a), v3(b), col)
		d := b.Sub(a)
		if d.Len() < 1e-6 {
			continue
		}
		nrm := mgl32.Vec2{-d.Y(), d.X()}.Normalize().Mul(hw)
		GizmoLine(v3(a.Add(nrm)), v3(b.Add(nrm)), col)
		GizmoLine(v3(a.Sub(nrm)), v3(b.Sub(nrm)), col)
	}
}

// worldCellColor returns the base overlay color for a cell.
func worldCellColor(t WorldTerrainType, city bool) mgl32.Vec3 {
	if city {
		return mgl32.Vec3{0.2, 0.9, 0.3}
	}
	switch t {
	case TerrainRoad:
		return mgl32.Vec3{0.98, 0.85, 0.15}
	case TerrainRiver:
		return mgl32.Vec3{0.3, 0.55, 1}
	case TerrainHill:
		return mgl32.Vec3{0.85, 0.55, 0.25}
	default:
		return mgl32.Vec3{0.45, 0.5, 0.55}
	}
}

func worldGizmoCellOutline(g *WorldGrid, cx, cy int, col mgl32.Vec3) {
	a := g.vertexWorld(cx, cy)
	b := g.vertexWorld(cx+1, cy)
	c := g.vertexWorld(cx+1, cy+1)
	d := g.vertexWorld(cx, cy+1)
	y := float32(0.05)
	pa := mgl32.Vec3{a.X(), y, a.Y()}
	pb := mgl32.Vec3{b.X(), y, b.Y()}
	pc := mgl32.Vec3{c.X(), y, c.Y()}
	pd := mgl32.Vec3{d.X(), y, d.Y()}
	GizmoLine(pa, pb, col)
	GizmoLine(pb, pc, col)
	GizmoLine(pc, pd, col)
	GizmoLine(pd, pa, col)
}

// worldGizmoCellBiome draws an inset marker inside cell (cx,cy) showing painted
// biome coverage: a green box (with an X at higher density) for grass and a gray
// diamond for rock. Marker brightness scales with density so faint edges of a
// stroke read as dimmer than its center. Grass and rock can both show at once.
func worldGizmoCellBiome(g *WorldGrid, cx, cy int, grass, rock float32) {
	min, max := g.cellWorldRect(cx, cy)
	y := float32(0.07) // above the terrain outline (0.05)
	lerp := func(a, b, t float32) float32 { return a + (b-a)*t }
	pt := func(tx, tz float32) mgl32.Vec3 {
		return mgl32.Vec3{lerp(min.X(), max.X(), tx), y, lerp(min.Y(), max.Y(), tz)}
	}

	if grass > 0.01 {
		c := mgl32.Vec3{0.25, 0.9, 0.3}.Mul(0.4 + 0.6*clamp01(grass))
		// Inset square.
		a, b, cc, d := pt(0.2, 0.2), pt(0.8, 0.2), pt(0.8, 0.8), pt(0.2, 0.8)
		GizmoLine(a, b, c)
		GizmoLine(b, cc, c)
		GizmoLine(cc, d, c)
		GizmoLine(d, a, c)
		// Diagonal fill hint appears once coverage is substantial.
		if grass > 0.4 {
			GizmoLine(a, cc, c)
			GizmoLine(b, d, c)
		}
	}

	if rock > 0.01 {
		c := mgl32.Vec3{0.72, 0.72, 0.76}.Mul(0.4 + 0.6*clamp01(rock))
		// Centered diamond, sized by density.
		s := 0.18 + 0.17*clamp01(rock)
		top, right := pt(0.5, 0.5-s), pt(0.5+s, 0.5)
		bot, left := pt(0.5, 0.5+s), pt(0.5-s, 0.5)
		GizmoLine(top, right, c)
		GizmoLine(right, bot, c)
		GizmoLine(bot, left, c)
		GizmoLine(left, top, c)
	}
}

func worldGizmoRing(center mgl32.Vec2, radius float32, col mgl32.Vec3) {
	const segs = 48
	y := float32(0.1)
	prev := mgl32.Vec3{center.X() + radius, y, center.Y()}
	for i := 1; i <= segs; i++ {
		a := float64(i) / segs * 2 * math.Pi
		cur := mgl32.Vec3{
			center.X() + radius*float32(math.Cos(a)),
			y,
			center.Y() + radius*float32(math.Sin(a)),
		}
		GizmoLine(prev, cur, col)
		prev = cur
	}
}

// ---------- panel ----------

// WorldEditorPanel draws the world-builder side panel.
func WorldEditorPanel() {
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(WinWidth) - Editor.PanelWidth, Y: 28})
	imgui.SetNextWindowSize(imgui.Vec2{X: Editor.PanelWidth, Y: float32(WinHeight) - 28})
	imgui.BeginV("World Builder", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	if imgui.Button("New World...") {
		Editor.ShowNewWorld = true
	}
	imgui.SameLine()
	imgui.Text("Scroll: zoom | MMB/WASD: pan")

	ch := activeWorldChunk()
	if ch == nil {
		imgui.Separator()
		imgui.Text("No world on the active chunk.")
		imgui.Text("Create one with New World.")
		imgui.End()
		worldNewWorldDialog()
		return
	}
	g := ch.WorldGrid

	imgui.Separator()
	imgui.Text(fmt.Sprintf("Grid: %dx%d  cell %.1f", g.Cols, g.Rows, g.CellSize))

	// ---- Tool selector ----
	imgui.Separator()
	tools := []string{"Raise", "Lower", "Smooth", "Flatten", "Paint Terrain", "Paint City", "Paint Biome", "Erase", "Road (draw)"}
	if Editor.WorldTool < 0 || Editor.WorldTool >= len(tools) {
		Editor.WorldTool = WorldToolRaise
	}
	imgui.Text("Tool")
	if imgui.BeginCombo("##worldtool", tools[Editor.WorldTool]) {
		for i, name := range tools {
			if imgui.SelectableV(name, i == Editor.WorldTool, 0, imgui.Vec2{}) {
				Editor.WorldTool = i
			}
		}
		imgui.EndCombo()
	}

	// ---- Brush params ----
	editorDragFloat("Radius##wr", &Editor.WorldRadius, 0.5, 1000, "%.1f")
	editorDragFloat("Strength##ws", &Editor.WorldStrength, 0.1, 1000, "%.2f")
	falloffs := []string{"Smooth", "Linear", "Constant"}
	if Editor.WorldFalloff < 0 || Editor.WorldFalloff >= len(falloffs) {
		Editor.WorldFalloff = FalloffSmooth
	}
	imgui.Text("Falloff")
	if imgui.BeginCombo("##worldfalloff", falloffs[Editor.WorldFalloff]) {
		for i, name := range falloffs {
			if imgui.SelectableV(name, i == Editor.WorldFalloff, 0, imgui.Vec2{}) {
				Editor.WorldFalloff = i
			}
		}
		imgui.EndCombo()
	}

	// ---- Terrain type (for Paint Terrain) ----
	imgui.Separator()
	terrains := []string{"General", "Road", "River", "Hill"}
	ti := int(Editor.WorldPaintType)
	if ti < 0 || ti >= len(terrains) {
		ti = 0
	}
	imgui.Text("Paint Terrain Type")
	if imgui.BeginCombo("##worldterrain", terrains[ti]) {
		for i, name := range terrains {
			if imgui.SelectableV(name, i == ti, 0, imgui.Vec2{}) {
				Editor.WorldPaintType = WorldTerrainType(i)
			}
		}
		imgui.EndCombo()
	}
	cityID := int32(Editor.WorldCityID)
	if imgui.InputInt("City Region ID##wcid", &cityID) {
		if cityID < 1 {
			cityID = 1
		}
		Editor.WorldCityID = cityID
	}

	// ---- Biome scatter (for Paint Biome) ----
	imgui.Separator()
	biomes := []string{"Grass", "Rock"}
	if Editor.WorldBiome < 0 || Editor.WorldBiome >= len(biomes) {
		Editor.WorldBiome = BiomeGrass
	}
	imgui.Text("Paint Biome")
	if imgui.BeginCombo("##worldbiome", biomes[Editor.WorldBiome]) {
		for i, name := range biomes {
			if imgui.SelectableV(name, i == Editor.WorldBiome, 0, imgui.Vec2{}) {
				Editor.WorldBiome = i
			}
		}
		imgui.EndCombo()
	}
	imgui.SliderFloat("Density##wbd", &Editor.WorldBiomeDensity, 0, 1)
	// Camera-ring draw distances (world units) — how far from the camera each
	// scatter is generated. Edits mark the biome textures dirty via the next stroke.
	if imgui.SliderFloat("Grass Dist##wgr", &g.GrassRange, 4, 300) {
		MarkBiomesDirty()
	}
	if imgui.SliderFloat("Rock Dist##wrr", &g.RockRange, 4, 400) {
		MarkBiomesDirty()
	}
	// Appearance. Grass height/width are live shader uniforms; Rock Sides rebuilds
	// the rock template (faces == 2 × sides) — all apply on the next frame.
	if g.GrassHeight <= 0 {
		g.GrassHeight = defaultGrassHeight
	}
	if g.GrassWidth <= 0 {
		g.GrassWidth = defaultGrassWidth
	}
	if g.RockSides < 3 {
		g.RockSides = defaultRockSides
	}
	imgui.SliderFloat("Grass Height##wgh", &g.GrassHeight, 0.05, 3)
	imgui.SliderFloat("Blade Width##wbw", &g.GrassWidth, 0.005, 0.3)
	imgui.SliderInt("Rock Sides##wrf", &g.RockSides, 3, 16)

	// ---- Presets ----
	imgui.Separator()
	imgui.Text("Presets")
	if imgui.Button("Road") {
		// Free-hand road: drag to draw a path that carves a real (diagonal) road.
		Editor.WorldTool = WorldToolRoad
	}
	imgui.SameLine()
	editorDragFloat("Road Width##wrw", &Editor.WorldRoadWidth, 1, 20, "%.1f")
	imgui.SameLine()
	if imgui.Button("River") {
		Editor.WorldTool = WorldToolLower
		Editor.WorldPaintType = TerrainRiver
	}
	imgui.SameLine()
	if imgui.Button("Hills") {
		Editor.WorldTool = WorldToolRaise
		Editor.WorldPaintType = TerrainHill
	}
	imgui.SameLine()
	if imgui.Button("Terrain") {
		Editor.WorldTool = WorldToolPaintTex
		Editor.WorldPaintType = TerrainGeneral
	}
	if imgui.Button("City") {
		Editor.WorldTool = WorldToolPaintCity
	}
	imgui.SameLine()
	if imgui.Button("Grass") {
		Editor.WorldTool = WorldToolPaintBiome
		Editor.WorldBiome = BiomeGrass
	}
	imgui.SameLine()
	if imgui.Button("Rock") {
		Editor.WorldTool = WorldToolPaintBiome
		Editor.WorldBiome = BiomeRock
	}
	imgui.SameLine()
	if imgui.Button("Erase") {
		Editor.WorldTool = WorldToolErase
	}

	// ---- City generation ----
	imgui.Separator()
	styleNames := make([]string, 0, len(BuildingStyles))
	for name := range BuildingStyles {
		styleNames = append(styleNames, name)
	}
	if g.StyleName == "" {
		g.StyleName = "desert"
	}
	// Palette: when non-empty, Generate City stamps these designed buildings;
	// otherwise it falls back to the procedural style below.
	if len(g.Palette) > 0 {
		imgui.Text(fmt.Sprintf("Palette: %d building(s)", len(g.Palette)))
	} else {
		imgui.Text("Palette empty — using procedural style:")
		imgui.Text("Building Style")
		if imgui.BeginCombo("##worldstyle", g.StyleName) {
			for _, name := range styleNames {
				if imgui.SelectableV(name, name == g.StyleName, 0, imgui.Vec2{}) {
					g.StyleName = name
				}
			}
			imgui.EndCombo()
		}
	}
	if imgui.Button("Building Editor...") {
		EnterBuildingEditor()
	}

	// Density + road-facing controls.
	editorDragFloat("Density##wd", &g.Density, 0, 1, "%.2f")
	if g.Density < 0 {
		g.Density = 0
	}
	if g.Density > 1 {
		g.Density = 1
	}
	imgui.Checkbox("Face Roads##wfr", &g.FaceRoads)

	if imgui.Button("Generate City") {
		generateCityForActive()
	}
	imgui.SameLine()
	if imgui.Button("Reseed") {
		g.Seed = int64(sdl.GetTicks())
		generateCityForActive()
	}
	imgui.Text("Brush strokes overwrite painted cells.")
	imgui.Text("Generate City rebuilds all buildings;")
	imgui.Text("tweaks survive save/reload but not regen.")

	imgui.End()
	worldNewWorldDialog()
}

// worldNewWorldDialog renders the New World modal.
func worldNewWorldDialog() {
	if Editor.ShowNewWorld {
		imgui.OpenPopup("New World")
		Editor.ShowNewWorld = false
		if Editor.NewWorldCols == 0 {
			Editor.NewWorldCols = 32
			Editor.NewWorldRows = 32
			Editor.NewWorldCellSize = 6.0
			Editor.NewWorldSeed = 1
		}
		if Editor.NewWorldName == "" {
			Editor.NewWorldName = "world"
		}
	}
	if imgui.BeginPopupModalV("New World", nil, imgui.WindowFlagsAlwaysAutoResize) {
		imgui.Text("Creates a new level file in the levels folder.")
		imgui.InputText("Level Name##nwn", &Editor.NewWorldName)
		imgui.InputInt("Columns##nwc", &Editor.NewWorldCols)
		imgui.InputInt("Rows##nwr", &Editor.NewWorldRows)
		imgui.DragFloatV("Cell Size##nwcs", &Editor.NewWorldCellSize, 0.5, 0.5, 100, "%.1f", 0)
		imgui.InputInt("Seed##nws", &Editor.NewWorldSeed)
		if imgui.Button("Create##nw") {
			cols := int(Editor.NewWorldCols)
			rows := int(Editor.NewWorldRows)
			if cols < 1 {
				cols = 1
			}
			if rows < 1 {
				rows = 1
			}
			cs := Editor.NewWorldCellSize
			if cs <= 0 {
				cs = 4.0
			}
			name := Editor.NewWorldName
			if name == "" {
				name = "world"
			}
			createWorld(name, cols, rows, cs, int64(Editor.NewWorldSeed))
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##nw") {
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
}
