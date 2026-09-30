package editor

// editor_terrain.go — sculpting, hole-cutting and texture painting on a
// chunk's heightfield ground (world/terrain.go).
//
// # The tool
//
// T (or the toolbar's Terrain button) arms it. While armed, the left button
// paints with the brush wherever the cursor meets a terrain, every frame it is
// held — so raising is a rate, and holding still over a spot keeps building
// it — and the brush's reach is traced on the ground as a ring. Its keys only
// mean something while it is armed, the scenery brush's terms:
//
//	1..7  Raise, Lower, Smooth, Flatten, Paint, Hole, Fill
//	[ ]   radius down / up
//	- =   strength down / up
//	Shift inverts the mode for one stroke (raise<->lower, hole<->fill)
//	Esc   put the tool away
//
// # One stroke, one undo step
//
// The level is snapshotted when the button goes down and the entry kept only
// if the stroke changed something, the texture editor's rule. A stroke locks
// to the terrain it started on: dragging off the edge of one ground onto a
// neighbouring chunk's does not start painting that one halfway through.
//
// The grid is written in place and the renderer re-uploads the rows the stroke
// touched (Terrain.TouchRect); collision, rays and the nav signature read the
// same arrays, so walking the level mid-sculpt stands on what is drawn.

import (
	"fmt"
	"math"

	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/imguibackend"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// terrainToolState is the tool's settings and what it is doing this frame.
type terrainToolState struct {
	Active   bool
	Mode     terrainBrushMode
	Radius   float32 // world units
	Strength float32 // 0..1
	Hardness float32 // 0..1
	Layer    int     // Paint's layer

	// hover is where the cursor meets a terrain this frame.
	hoverValid bool
	hoverChunk int
	hoverPoint mgl32.Vec3 // world
	hoverLocal mgl32.Vec3 // in the hovered chunk's space

	// The stroke in progress, if any.
	stroking      bool
	strokeChunk   int
	strokeTerrain *world.Terrain
	strokeMode    terrainBrushMode
	strokeChanged bool
	// strokePushed is whether the stroke's undo entry really went on the
	// stack — EditorPushUndo declines during a play session, and popping
	// then would throw away somebody else's entry.
	strokePushed  bool
	flattenTarget float32 // local height Flatten pulls toward
}

var terrainTool = terrainToolState{Mode: terrainRaise, Radius: 4, Strength: 0.5, Hardness: 0.3}

const (
	terrainMinRadius = 0.25
	terrainMaxRadius = 64
)

// editorArmTerrainTool switches the tool on, putting every other armed tool
// away: they all answer the next left-click.
func editorArmTerrainTool() {
	if Editor.level == nil {
		return
	}
	ws().disarmOtherPlacements()
	Editor.BrushPlaceActive = false
	terrainTool.Active = true
	if !editorLevelHasTerrain() {
		editorSetStatus("Terrain tool — no chunk has terrain yet: select a chunk and use Terrain > Add Terrain")
		return
	}
	editorSetStatus("Terrain: %s — hold the left button to paint, 1-7 change mode, Esc to stop", terrainTool.Mode)
}

// editorDisarmTerrainTool puts the tool away, finishing any stroke.
func editorDisarmTerrainTool() {
	editorTerrainEndStroke()
	terrainTool.Active = false
	terrainTool.hoverValid = false
}

func editorToggleTerrainTool() {
	if terrainTool.Active {
		editorDisarmTerrainTool()
		editorSetStatus("Terrain tool off")
		return
	}
	editorArmTerrainTool()
}

// TerrainToolActive reports whether the terrain tool is armed, for the F1
// reference's section.
func TerrainToolActive() bool { return terrainTool.Active }

func editorLevelHasTerrain() bool {
	if Editor.level == nil {
		return false
	}
	for ci := range Editor.level.Chunks {
		if Editor.level.Chunks[ci].Terrain != nil {
			return true
		}
	}
	return false
}

// editorTerrainHandleKey owns the tool's keys while it is armed.
func editorTerrainHandleKey(scancode sdl.Scancode) bool {
	if !terrainTool.Active {
		return false
	}
	switch scancode {
	case sdl.SCANCODE_1, sdl.SCANCODE_2, sdl.SCANCODE_3, sdl.SCANCODE_4,
		sdl.SCANCODE_5, sdl.SCANCODE_6, sdl.SCANCODE_7:
		editorSetTerrainMode(terrainBrushModes[int(scancode-sdl.SCANCODE_1)])
	case sdl.SCANCODE_LEFTBRACKET:
		terrainTool.Radius = clampf(terrainTool.Radius/1.25, terrainMinRadius, terrainMaxRadius)
		editorSetStatus("Terrain radius %.2f", terrainTool.Radius)
	case sdl.SCANCODE_RIGHTBRACKET:
		terrainTool.Radius = clampf(terrainTool.Radius*1.25, terrainMinRadius, terrainMaxRadius)
		editorSetStatus("Terrain radius %.2f", terrainTool.Radius)
	case sdl.SCANCODE_MINUS:
		terrainTool.Strength = clampf(terrainTool.Strength-0.1, 0.05, 1)
		editorSetStatus("Terrain strength %.2f", terrainTool.Strength)
	case sdl.SCANCODE_EQUALS:
		terrainTool.Strength = clampf(terrainTool.Strength+0.1, 0.05, 1)
		editorSetStatus("Terrain strength %.2f", terrainTool.Strength)
	case sdl.SCANCODE_ESCAPE:
		editorDisarmTerrainTool()
		editorSetStatus("Terrain tool off")
	default:
		return false
	}
	return true
}

func editorSetTerrainMode(m terrainBrushMode) {
	terrainTool.Mode = m
	editorSetStatus("Terrain: %s", m)
}

func clampf(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// editorTerrainHandleMouseDown starts a stroke. It consumes every left click
// while the tool is armed — a click meant for the ground must never also
// select the brush it landed beside.
func editorTerrainHandleMouseDown() bool {
	if !terrainTool.Active {
		return false
	}
	lvl := Editor.level
	if lvl == nil || !terrainTool.hoverValid || terrainTool.hoverChunk >= len(lvl.Chunks) {
		return true
	}
	ch := &lvl.Chunks[terrainTool.hoverChunk]
	if ch.Terrain == nil {
		return true
	}
	mode := terrainTool.Mode
	if sdl.GetModState()&sdl.KMOD_SHIFT != 0 {
		mode = mode.inverse()
	}
	if mode == terrainPaint {
		if l := terrainTool.Layer; l < 0 || l >= world.TerrainLayerCount || ch.Terrain.Layers[l] == "" {
			editorSetStatus("Layer %d of %s has no texture — pick one in the Terrain Tool panel first", terrainTool.Layer+1, ch.ID)
			return true
		}
	}
	// The conditions EditorPushUndo pushes under; the stack's length cannot
	// say, since a full stack drops its oldest entry and stays the same size.
	terrainTool.strokePushed = editorUndoRedirect == nil && !inPlaySession()
	EditorPushUndo()
	terrainTool.stroking = true
	terrainTool.strokeChunk = terrainTool.hoverChunk
	terrainTool.strokeTerrain = ch.Terrain
	terrainTool.strokeMode = mode
	terrainTool.strokeChanged = false
	terrainTool.flattenTarget = terrainTool.hoverLocal.Y()
	// A click with no drag still does something: one frame's worth, at least,
	// and the whole of it for the modes that are not rates.
	editorTerrainApply(1.0 / 30)
	return true
}

// editorTerrainEndStroke finishes the stroke in progress: its undo entry is
// kept if it changed anything and dropped otherwise, and the spatial index is
// brought up to the ground's new bounds.
func editorTerrainEndStroke() {
	if !terrainTool.stroking {
		return
	}
	terrainTool.stroking = false
	if !terrainTool.strokeChanged {
		if terrainTool.strokePushed {
			EditorPopUndo()
		}
		terrainTool.strokeTerrain = nil
		return
	}
	if lvl := Editor.level; lvl != nil && terrainTool.strokeChunk < len(lvl.Chunks) {
		ch := &lvl.Chunks[terrainTool.strokeChunk]
		if ch.Terrain == terrainTool.strokeTerrain {
			editorChunkGeometryChanged(ch)
		}
	}
	terrainTool.strokeTerrain = nil
}

// editorTerrainFrame runs once a frame from EditorRender: find where the
// cursor meets the ground, and carry on any stroke.
func editorTerrainFrame(dt float32) {
	if !terrainTool.Active {
		return
	}
	if Editor.Mode != Mode3D || Editor.level == nil || ws().level != Editor.level {
		terrainTool.hoverValid = false
		return
	}
	// Arming anything else puts the terrain tool away, the same bargain the
	// placement modes strike with each other.
	if ws().placementArmed() {
		editorDisarmTerrainTool()
		return
	}
	mx, my, _ := sdl.GetMouseState()
	overPanel := imgui.CurrentIO().WantCaptureMouse() || mx >= panelLeftEdgeX()
	terrainTool.hoverValid = false
	if !overPanel || terrainTool.stroking {
		origin, dir := ws().ray(mx, my)
		only := -1
		if terrainTool.stroking {
			only = terrainTool.strokeChunk
		}
		if ci, t, ok := terrainRayHit(Editor.level, origin, dir, only); ok {
			ch := &Editor.level.Chunks[ci]
			p := origin.Add(dir.Mul(t))
			inv := ch.ModelMatrix().Inv()
			terrainTool.hoverValid = true
			terrainTool.hoverChunk = ci
			terrainTool.hoverPoint = p
			terrainTool.hoverLocal = inv.Mul4x1(p.Vec4(1)).Vec3()
		}
	}
	if terrainTool.stroking {
		if dt > 0.1 {
			dt = 0.1 // a hitch must not dump a second of raising at once
		}
		editorTerrainApply(dt)
	}
}

// editorTerrainApply applies one frame of the stroke at the hover point.
func editorTerrainApply(dt float32) {
	lvl := Editor.level
	if !terrainTool.stroking || lvl == nil || terrainTool.strokeChunk >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[terrainTool.strokeChunk]
	t := ch.Terrain
	if t == nil || t != terrainTool.strokeTerrain {
		// The level changed under the stroke (a load, an undo): stop.
		terrainTool.stroking = false
		return
	}
	if !terrainTool.hoverValid || terrainTool.hoverChunk != terrainTool.strokeChunk {
		return
	}
	scale := ch.EffectiveScale() * ch.EffectiveFrameScale()
	b := terrainBrush{
		mode:     terrainTool.strokeMode,
		cx:       terrainTool.hoverLocal.X(),
		cz:       terrainTool.hoverLocal.Z(),
		radius:   terrainTool.Radius / scale,
		strength: terrainTool.Strength,
		hardness: terrainTool.Hardness,
		layer:    terrainTool.Layer,
		target:   terrainTool.flattenTarget,
		dt:       dt,
	}
	if changed, x0, z0, x1, z1 := applyTerrainBrush(t, b); changed {
		t.TouchRect(x0, z0, x1, z1)
		terrainTool.strokeChanged = true
	}
}

// terrainRayHit is the nearest enabled terrain the world-space ray meets, and
// the distance along it. only >= 0 restricts it to that chunk.
func terrainRayHit(lvl *world.Level, origin, dir mgl32.Vec3, only int) (chunk int, t float32, ok bool) {
	best := float32(math.MaxFloat32)
	for ci := range lvl.Chunks {
		if only >= 0 && ci != only {
			continue
		}
		ch := &lvl.Chunks[ci]
		if ch.Terrain == nil || !ch.IsEnabled() {
			continue
		}
		inv := ch.ModelMatrix().Inv()
		lo := inv.Mul4x1(origin.Vec4(1)).Vec3()
		ld := inv.Mul4x1(dir.Vec4(0)).Vec3()
		if d, _, hit := ch.Terrain.Raycast(lo, ld, best); hit && d < best {
			best, chunk, ok = d, ci, true
		}
	}
	return chunk, best, ok
}

// terrainModeColor is the ring's colour for each mode, so what the next click
// does is readable without looking at the panel.
func terrainModeColor(m terrainBrushMode) mgl32.Vec3 {
	switch m {
	case terrainRaise:
		return mgl32.Vec3{0.3, 1, 0.4}
	case terrainLower:
		return mgl32.Vec3{1, 0.5, 0.2}
	case terrainSmooth:
		return mgl32.Vec3{0.4, 0.8, 1}
	case terrainFlatten:
		return mgl32.Vec3{1, 1, 0.4}
	case terrainPaint:
		return mgl32.Vec3{1, 0.4, 1}
	case terrainHole, terrainFill:
		return mgl32.Vec3{1, 0.25, 0.25}
	}
	return mgl32.Vec3{1, 1, 1}
}

// drawTerrainBrushRing traces the brush's reach on the ground: an outer ring
// at the radius and an inner one where the falloff starts, each point dropped
// onto the terrain so the ring hugs the hills it is about to reshape.
func drawTerrainBrushRing() {
	if !terrainTool.Active || !terrainTool.hoverValid || Editor.level == nil ||
		terrainTool.hoverChunk >= len(Editor.level.Chunks) {
		return
	}
	ch := &Editor.level.Chunks[terrainTool.hoverChunk]
	t := ch.Terrain
	if t == nil {
		return
	}
	mode := terrainTool.Mode
	if terrainTool.stroking {
		mode = terrainTool.strokeMode
	} else if sdl.GetModState()&sdl.KMOD_SHIFT != 0 {
		mode = mode.inverse()
	}
	col := terrainModeColor(mode)
	model := ch.ModelMatrix()
	scale := ch.EffectiveScale() * ch.EffectiveFrameScale()
	c := terrainTool.hoverLocal
	lift := 0.05 / scale
	ring := func(r float32, col mgl32.Vec3) {
		const segs = 48
		var prev mgl32.Vec3
		for i := 0; i <= segs; i++ {
			a := float64(i) * 2 * math.Pi / segs
			x := c.X() + r*float32(math.Cos(a))
			z := c.Z() + r*float32(math.Sin(a))
			y := c.Y()
			if h, _, ok := t.HeightAt(x, z); ok {
				y = h
			}
			p := model.Mul4x1(mgl32.Vec4{x, y + lift, z, 1}).Vec3()
			if i > 0 {
				gizmo.GizmoLine(prev, p, col)
			}
			prev = p
		}
	}
	r := terrainTool.Radius / scale
	ring(r, col)
	if terrainTool.Hardness > 0.02 && terrainTool.Hardness < 0.98 {
		ring(r*terrainTool.Hardness, col.Mul(0.6))
	}
	// A short stalk at the centre, so the exact point is visible on flat
	// ground where the ring alone floats.
	up := model.Mul4x1(mgl32.Vec4{c.X(), c.Y() + 0.6/scale, c.Z(), 1}).Vec3()
	gizmo.GizmoLine(terrainTool.hoverPoint, up, col)
}

// ---------- panel ----------

// editorTerrainToolSection is the tool's own block, shown while it is armed.
// Not selection-driven: like the scenery brush's, it is about the next click.
func editorTerrainToolSection() {
	if !terrainTool.Active {
		return
	}
	imgui.Separator()
	if !imgui.CollapsingHeaderV("Terrain Tool##terraintoolhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	for i, m := range terrainBrushModes {
		if i%4 != 0 {
			imgui.SameLine()
		}
		if editorActiveToggleButton(fmt.Sprintf("%s [%d]##terrainmode%d", m, i+1, i), terrainTool.Mode == m) {
			editorSetTerrainMode(m)
		}
	}
	editorDragFloatStep("Radius##terrainradius", &terrainTool.Radius, terrainMinRadius, terrainMaxRadius, "%.2f", 0.25)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("[ and ] change it from the viewport.")
	}
	editorDragFloatStep("Strength##terrainstrength", &terrainTool.Strength, 0.05, 1, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("- and = change it from the viewport.")
	}
	editorDragFloatStep("Hardness##terrainhardness", &terrainTool.Hardness, 0, 1, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How much of the radius works at full strength before it fades out.")
	}

	ch := editorTerrainPanelChunk()
	if ch == nil {
		editorTextMutedWrapped("No terrain under the cursor or selected. Select a chunk and use its Terrain group to add one.")
		return
	}
	imgui.Text("Paint layer (" + ch.ID + ")")
	for l := 0; l < world.TerrainLayerCount; l++ {
		name := ch.Terrain.Layers[l]
		label := fmt.Sprintf("%d: %s##terrainlayer%d", l+1, name, l)
		if name == "" {
			label = fmt.Sprintf("%d: (none)##terrainlayer%d", l+1, l)
		}
		if imgui.RadioButton(label, terrainTool.Layer == l) {
			terrainTool.Layer = l
			if terrainTool.Mode != terrainPaint {
				editorSetTerrainMode(terrainPaint)
			}
		}
	}
	editorTerrainLayerCombos(ch)
	if terrainTool.hoverValid {
		editorTextMuted(fmt.Sprintf("Cursor: %.2f, %.2f, %.2f", terrainTool.hoverPoint.X(),
			terrainTool.hoverPoint.Y(), terrainTool.hoverPoint.Z()))
	}
}

// editorTerrainPanelChunk is the terrain the tool panel describes: the one
// under the cursor, or else the selected chunk's.
func editorTerrainPanelChunk() *world.Chunk {
	lvl := Editor.level
	if lvl == nil {
		return nil
	}
	if terrainTool.stroking && terrainTool.strokeChunk < len(lvl.Chunks) {
		if ch := &lvl.Chunks[terrainTool.strokeChunk]; ch.Terrain != nil {
			return ch
		}
	}
	if ch := selectedChunk(); ch != nil && ch.Terrain != nil {
		return ch
	}
	if terrainTool.hoverValid && terrainTool.hoverChunk < len(lvl.Chunks) {
		if ch := &lvl.Chunks[terrainTool.hoverChunk]; ch.Terrain != nil {
			return ch
		}
	}
	return nil
}

// editorTerrainLayerCombos is the four layer texture pickers.
func editorTerrainLayerCombos(ch *world.Chunk) {
	t := ch.Terrain
	for l := 0; l < world.TerrainLayerCount; l++ {
		name := t.Layers[l]
		if textureCombo(fmt.Sprintf("Layer %d##terraintex%d", l+1, l), &name, true) {
			EditorPushUndo()
			t.Layers[l] = name
			t.Touch()
		}
		if l == 0 && imgui.IsItemHovered() {
			imgui.SetTooltip("Layer 1 is what unpainted ground shows.")
		}
	}
}

// terrainNewCells, terrainNewCellSize are the Add Terrain form's values,
// kept between frames.
var (
	terrainNewCellsX   int32   = 64
	terrainNewCellsZ   int32   = 64
	terrainNewCellSize float32 = 1
	terrainResizeX     int32
	terrainResizeZ     int32
	terrainResizeFor   *world.Terrain
)

// editorChunkTerrain is the chunk properties' Terrain group: add a terrain,
// or set up and resize the one the chunk has.
func editorChunkTerrain(ch *world.Chunk) {
	t := ch.Terrain
	if t == nil {
		editorTextMutedWrapped("Open ground, sculpted and painted with the Terrain tool [T] rather than built from brushes.")
		imgui.DragIntV("Cells X##terrainnewx", &terrainNewCellsX, 1, 1, world.TerrainMaxCells, "%d", 0)
		imgui.DragIntV("Cells Z##terrainnewz", &terrainNewCellsZ, 1, 1, world.TerrainMaxCells, "%d", 0)
		editorDragFloatStep("Cell size##terrainnewsize", &terrainNewCellSize, 0.125, 16, "%.3f", 0.25)
		w := float32(terrainNewCellsX) * terrainNewCellSize
		d := float32(terrainNewCellsZ) * terrainNewCellSize
		editorTextMuted(fmt.Sprintf("%.1f x %.1f units, centred on the chunk's origin", w, d))
		if imgui.Button("Add Terrain##terrainadd") {
			EditorPushUndo()
			ch.Terrain = world.NewTerrain(mgl32.Vec3{-w / 2, 0, -d / 2}, terrainNewCellSize,
				int(terrainNewCellsX), int(terrainNewCellsZ))
			if _, ok := gfx.TextureNames["grass"]; ok {
				ch.Terrain.Layers[0] = "grass"
				_ = gfx.EnsureTextureLoaded("grass")
			}
			editorChunkGeometryChanged(ch)
			editorArmTerrainTool()
		}
		return
	}

	editorTextMuted(fmt.Sprintf("%d x %d cells of %.3g (%.1f x %.1f units)", t.CellsX, t.CellsZ, t.CellSize,
		float32(t.CellsX)*t.CellSize, float32(t.CellsZ)*t.CellSize))
	if editorActiveToggleButton("Sculpt & Paint [T]##terrainarm", terrainTool.Active) {
		editorToggleTerrainTool()
	}

	origin := [3]float32{t.Origin.X(), t.Origin.Y(), t.Origin.Z()}
	if editorDragFloat3("Corner##terrainorigin", &origin, -1e5, 1e5, "%.2f") {
		t.Origin = mgl32.Vec3{origin[0], origin[1], origin[2]}
		t.Touch()
		editorChunkGeometryChanged(ch)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Where the grid's first corner sits in the chunk; heights are measured up from its Y.")
	}
	size := t.CellSize
	if editorDragFloatStep("Cell size##terraincellsize", &size, 0.125, 16, "%.3f", 0.125) && size > 0 {
		t.CellSize = size
		t.Touch()
		editorChunkGeometryChanged(ch)
	}
	scale := t.EffectiveTextureScale()
	if editorDragFloatStep("Texture scale##terraintexscale", &scale, 0.125, 64, "%.3f", 0.125) {
		t.TextureScale = scale
		t.Touch()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Units of ground one repeat of a layer's texture covers.")
	}
	editorTerrainLayerCombos(ch)

	if terrainResizeFor != t {
		terrainResizeFor, terrainResizeX, terrainResizeZ = t, int32(t.CellsX), int32(t.CellsZ)
	}
	imgui.DragIntV("Resize X##terrainrx", &terrainResizeX, 1, 1, world.TerrainMaxCells, "%d", 0)
	imgui.DragIntV("Resize Z##terrainrz", &terrainResizeZ, 1, 1, world.TerrainMaxCells, "%d", 0)
	imguibackend.EditorPushDisabled(int(terrainResizeX) == t.CellsX && int(terrainResizeZ) == t.CellsZ)
	if imgui.Button("Apply Resize##terrainresize") {
		EditorPushUndo()
		t.Resize(int(terrainResizeX), int(terrainResizeZ))
		editorChunkGeometryChanged(ch)
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Grows or crops the grid from its far edges; the first corner stays put.")
	}

	if imgui.Button("Remove Terrain##terrainremove") {
		EditorPushUndo()
		editorTerrainEndStroke()
		ch.Terrain = nil
		editorChunkGeometryChanged(ch)
	}
}

// editorTerrainToolbarButton is the row-two toggle.
func editorTerrainToolbarButton() {
	imgui.SameLine()
	if editorActiveToggleButtonColor("Terrain [T]##terraintool", terrainTool.Active,
		imgui.Vec4{X: 0.25, Y: 0.55, Z: 0.2, W: 1}) {
		editorToggleTerrainTool()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Sculpt, cut holes in and paint the ground of chunks that have terrain.\n" +
			"Hold the left button to brush; Shift inverts; 1-7 pick the mode;\n" +
			"[ ] radius, - = strength, Esc puts it away.")
	}
}
