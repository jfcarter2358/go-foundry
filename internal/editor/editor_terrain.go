package editor

// editor_terrain.go — mining, building and painting a chunk's block terrain
// (world/terrain.go).
//
// # The tool
//
// T (or the toolbar's Terrain button) arms it. While armed, the cursor picks
// the face of the terrain under it — in block view or smooth view alike, a
// face names the block behind it — and the target is outlined: the block
// that would be mined or re-textured, the empty cell that would be filled, or
// the reach of the biome brush. Its keys only mean something while it is
// armed, the scenery brush's terms:
//
//	1..6  Mine, Place, Material, Paint Biome, Erase Biome, Volume
//	[ ]   reach down / up (0 is one block or face)
//	, .   previous / next material (or biome, while painting)
//	N     switch the terrain between blocks and the smooth surface
//	Shift inverts the mode for one stroke (mine<->place, paint<->erase)
//	Esc   put the tool away
//
// Mine, Place and Material act once on the click and then repeat on a timer
// while the button is held, a block at a time, the way digging and building
// feel in a block game; Paint and Erase brush every frame. Volume lays out a
// box of blocks to fill or carve in one go (editor_terrain_volume.go).
//
// # One stroke, one undo step
//
// The level is snapshotted when the button goes down and the entry kept only
// if the stroke changed something, the texture editor's rule. A stroke locks
// to the terrain it started on.

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
	Mode     terrainEditMode
	Radius   int
	Material uint8 // 1..4
	Biome    uint8 // palette slot, counting from 1

	// hover is where the cursor meets a terrain this frame.
	hoverValid bool
	hoverChunk int
	hoverFace  int32
	hoverPoint mgl32.Vec3 // world
	hoverLocal mgl32.Vec3 // in the hovered chunk's space

	// The stroke in progress, if any.
	stroking      bool
	strokeChunk   int
	strokeTerrain *world.Terrain
	strokeMode    terrainEditMode
	strokeChanged bool
	// strokePushed is whether the stroke's undo entry really went on the
	// stack — EditorPushUndo declines during a play session, and popping
	// then would throw away somebody else's entry.
	strokePushed bool
	// repeatIn counts down to the next repeat of a block mode.
	repeatIn float32
}

var terrainTool = terrainToolState{Mode: terrainMine, Material: 1, Biome: 1}

const (
	terrainMaxRadius   = 12
	terrainRepeatDelay = 0.3  // before the first repeat, seconds
	terrainRepeatEvery = 0.12 // between repeats after that
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
	editorSetStatus("Terrain: %s — click to use, 1-6 change mode, N blocks/smooth, Esc to stop", terrainTool.Mode)
}

// editorDisarmTerrainTool puts the tool away, finishing any stroke.
func editorDisarmTerrainTool() {
	editorTerrainEndStroke()
	editorTerrainVolumeCancel()
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
	if editorTerrainVolumeHandleKey(scancode) {
		return true
	}
	switch scancode {
	case sdl.SCANCODE_1, sdl.SCANCODE_2, sdl.SCANCODE_3, sdl.SCANCODE_4, sdl.SCANCODE_5, sdl.SCANCODE_6:
		editorSetTerrainMode(terrainEditModes[int(scancode-sdl.SCANCODE_1)])
	case sdl.SCANCODE_LEFTBRACKET:
		terrainTool.Radius = max(terrainTool.Radius-1, 0)
		editorSetStatus("Terrain reach %d", terrainTool.Radius)
	case sdl.SCANCODE_RIGHTBRACKET:
		terrainTool.Radius = min(terrainTool.Radius+1, terrainMaxRadius)
		editorSetStatus("Terrain reach %d", terrainTool.Radius)
	case sdl.SCANCODE_COMMA, sdl.SCANCODE_PERIOD:
		step := 1
		if scancode == sdl.SCANCODE_COMMA {
			step = -1
		}
		editorTerrainCycle(step)
	case sdl.SCANCODE_N:
		if ch := editorTerrainPanelChunk(); ch != nil {
			editorToggleTerrainSmooth(ch)
		}
	case sdl.SCANCODE_ESCAPE:
		editorDisarmTerrainTool()
		editorSetStatus("Terrain tool off")
	default:
		return false
	}
	return true
}

// editorTerrainCycle steps the material, or the biome while painting.
func editorTerrainCycle(step int) {
	ch := editorTerrainPanelChunk()
	if terrainTool.Mode == terrainPaint || terrainTool.Mode == terrainErase {
		n := 0
		if ch != nil {
			n = len(ch.Terrain.Biomes)
		}
		if n == 0 {
			editorSetStatus("No biomes yet — add one in the chunk's Terrain group")
			return
		}
		terrainTool.Biome = uint8((int(terrainTool.Biome)-1+step+n)%n + 1)
		editorSetStatus("Biome %d: %s", terrainTool.Biome, ch.Terrain.Biomes[terrainTool.Biome-1].Def().Label)
		return
	}
	terrainTool.Material = uint8((int(terrainTool.Material)-1+step+world.TerrainLayerCount)%world.TerrainLayerCount + 1)
	name := ""
	if ch != nil {
		name = ch.Terrain.Layers[terrainTool.Material-1]
	}
	editorSetStatus("Material %d %s", terrainTool.Material, name)
}

func editorSetTerrainMode(m terrainEditMode) {
	if m != terrainVolume {
		editorTerrainVolumeCancel()
	}
	terrainTool.Mode = m
	editorSetStatus("Terrain: %s", m)
}

// editorToggleTerrainSmooth switches ch's terrain between its blocks and its
// smooth surface, as one undo step.
func editorToggleTerrainSmooth(ch *world.Chunk) {
	t := ch.Terrain
	if t == nil {
		return
	}
	EditorPushUndo()
	t.Smooth = !t.Smooth
	t.Touch()
	editorChunkGeometryChanged(ch)
	if t.Smooth {
		editorSetStatus("%s: smooth surface", ch.ID)
	} else {
		editorSetStatus("%s: blocks", ch.ID)
	}
}

// editorTerrainHandleMouseDown starts a stroke. It consumes every left click
// while the tool is armed — a click meant for the ground must never also
// select the brush it landed beside.
func editorTerrainHandleMouseDown() bool {
	if !terrainTool.Active {
		return false
	}
	if terrainTool.Mode == terrainVolume {
		editorTerrainVolumeMouseDown()
		return true
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
	if mode == terrainPaint && (terrainTool.Biome == 0 || int(terrainTool.Biome) > len(ch.Terrain.Biomes)) {
		editorSetStatus("%s has no biome %d — add one in its Terrain group first", ch.ID, terrainTool.Biome)
		return true
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
	terrainTool.repeatIn = terrainRepeatDelay
	editorTerrainApply()
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
		if ci, hit, ok := terrainRayHit(Editor.level, origin, dir, only); ok {
			ch := &Editor.level.Chunks[ci]
			p := origin.Add(dir.Mul(hit.T))
			terrainTool.hoverValid = true
			terrainTool.hoverChunk = ci
			terrainTool.hoverFace = hit.Face
			terrainTool.hoverPoint = p
			terrainTool.hoverLocal = ch.ModelMatrix().Inv().Mul4x1(p.Vec4(1)).Vec3()
		}
	}
	if terrainTool.Mode == terrainVolume {
		editorTerrainVolumeFrame(mx, my, overPanel)
	}
	if !terrainTool.stroking {
		return
	}
	if terrainTool.strokeMode.repeats() {
		terrainTool.repeatIn -= min(dt, 0.1)
		if terrainTool.repeatIn > 0 {
			return
		}
		terrainTool.repeatIn = terrainRepeatEvery
	}
	editorTerrainApply()
}

// editorTerrainApply applies the stroke once at the hover point.
func editorTerrainApply() {
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
	e := terrainEdit{
		mode:     terrainTool.strokeMode,
		face:     terrainTool.hoverFace,
		point:    terrainTool.hoverLocal,
		radius:   terrainTool.Radius,
		material: terrainTool.Material,
		biome:    terrainTool.Biome,
	}
	if applyTerrainEdit(t, e) {
		terrainTool.strokeChanged = true
	}
}

// terrainRayHit is the nearest enabled terrain the world-space ray meets.
// only >= 0 restricts it to that chunk.
func terrainRayHit(lvl *world.Level, origin, dir mgl32.Vec3, only int) (chunk int, hit world.TerrainHit, ok bool) {
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
		if h, got := ch.Terrain.Raycast(lo, ld, best); got && h.T < best {
			best, chunk, hit, ok = h.T, ci, h, true
		}
	}
	return chunk, hit, ok
}

// terrainModeColor is the outline's colour for each mode, so what the next
// click does is readable without looking at the panel.
func terrainModeColor(m terrainEditMode) mgl32.Vec3 {
	switch m {
	case terrainMine:
		return mgl32.Vec3{1, 0.35, 0.25}
	case terrainPlace:
		return mgl32.Vec3{0.3, 1, 0.4}
	case terrainRetexture:
		return mgl32.Vec3{0.4, 0.8, 1}
	case terrainPaint:
		return mgl32.Vec3{1, 0.4, 1}
	case terrainErase:
		return mgl32.Vec3{1, 1, 0.4}
	}
	return mgl32.Vec3{1, 1, 1}
}

// drawTerrainBrushRing outlines what the next click acts on: the block (or
// the sphere of them) for the block modes, the reach on the surface for the
// biome modes.
func drawTerrainBrushRing() {
	if terrainTool.Active && Editor.level != nil && terrainTool.Mode == terrainVolume {
		drawTerrainVolume()
		return
	}
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
	w := func(p mgl32.Vec3) mgl32.Vec3 { return model.Mul4x1(p.Vec4(1)).Vec3() }
	bs := t.BlockSize
	r := terrainTool.Radius

	if mode.repeats() {
		x, y, z := terrainEditTarget(t, terrainEdit{mode: mode, face: terrainTool.hoverFace})
		lo := t.BlockMin(x, y, z)
		pad := bs * 0.02
		if r > 0 {
			lo = lo.Sub(mgl32.Vec3{float32(r) * bs, float32(r) * bs, float32(r) * bs})
		}
		size := bs * float32(2*r+1)
		drawWireBox(lo.Sub(mgl32.Vec3{pad, pad, pad}), lo.Add(mgl32.Vec3{size + pad, size + pad, size + pad}), w, col)
		return
	}
	// The biome brush: the face under the cursor, and a ring of the reach
	// lying on the face's plane.
	if f, ok := t.Face(terrainTool.hoverFace); ok {
		for i := 0; i < 4; i++ {
			gizmo.GizmoLine(w(f.Corners[i]), w(f.Corners[(i+1)%4]), col)
		}
		if r > 0 {
			_, n := f.PointAt(0.5, 0.5)
			a := n.Cross(mgl32.Vec3{0, 1, 0})
			if a.Len() < 0.1 {
				a = n.Cross(mgl32.Vec3{1, 0, 0})
			}
			a = a.Normalize()
			b := n.Cross(a)
			rad := (float32(r) + 0.5) * bs
			c := terrainTool.hoverLocal.Add(n.Mul(0.03 * bs))
			const segs = 40
			var prev mgl32.Vec3
			for i := 0; i <= segs; i++ {
				ang := float64(i) * 2 * math.Pi / segs
				p := c.Add(a.Mul(rad * float32(math.Cos(ang)))).Add(b.Mul(rad * float32(math.Sin(ang))))
				if i > 0 {
					gizmo.GizmoLine(w(prev), w(p), col)
				}
				prev = p
			}
		}
	}
}

// drawWireBox outlines the chunk-space box [lo, hi], carried out by w.
func drawWireBox(lo, hi mgl32.Vec3, w func(mgl32.Vec3) mgl32.Vec3, col mgl32.Vec3) {
	c := func(i int) mgl32.Vec3 {
		p := lo
		if i&1 != 0 {
			p[0] = hi[0]
		}
		if i&2 != 0 {
			p[1] = hi[1]
		}
		if i&4 != 0 {
			p[2] = hi[2]
		}
		return w(p)
	}
	for i := 0; i < 8; i++ {
		for _, bit := range [3]int{1, 2, 4} {
			if i&bit == 0 {
				gizmo.GizmoLine(c(i), c(i|bit), col)
			}
		}
	}
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
	for i, m := range terrainEditModes {
		if i%3 != 0 {
			imgui.SameLine()
		}
		if editorActiveToggleButton(fmt.Sprintf("%s [%d]##terrainmode%d", m, i+1, i), terrainTool.Mode == m) {
			editorSetTerrainMode(m)
		}
	}
	reach := int32(terrainTool.Radius)
	if imgui.SliderInt("Reach##terrainreach", &reach, 0, terrainMaxRadius) {
		terrainTool.Radius = int(reach)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How many blocks around the target the tool reaches; 0 is the one block or face.\n[ and ] change it from the viewport.")
	}
	editorTerrainVolumeSection()

	ch := editorTerrainPanelChunk()
	if ch == nil {
		editorTextMutedWrapped("No terrain under the cursor or selected. Select a chunk and use its Terrain group to add one.")
		return
	}
	t := ch.Terrain
	if editorActiveToggleButton("Blocks##terrainviewblocks", !t.Smooth) && t.Smooth {
		editorToggleTerrainSmooth(ch)
	}
	imgui.SameLine()
	if editorActiveToggleButton("Smooth [N]##terrainviewsmooth", t.Smooth) && !t.Smooth {
		editorToggleTerrainSmooth(ch)
	}

	imgui.Text("Material (" + ch.ID + ")  , .")
	for l := 0; l < world.TerrainLayerCount; l++ {
		name := t.Layers[l]
		if name == "" {
			name = "(none)"
		}
		if imgui.RadioButton(fmt.Sprintf("%d: %s##terrainmat%d", l+1, name, l), int(terrainTool.Material) == l+1) {
			terrainTool.Material = uint8(l + 1)
			if terrainTool.Mode != terrainPlace && terrainTool.Mode != terrainRetexture && terrainTool.Mode != terrainVolume {
				editorSetTerrainMode(terrainPlace)
			}
		}
	}
	imgui.Text("Biome")
	if len(t.Biomes) == 0 {
		editorTextMutedWrapped("No biomes yet — add one in the chunk's Terrain group.")
	}
	for i := range t.Biomes {
		if imgui.RadioButton(fmt.Sprintf("%d: %s##terrainbiome%d", i+1, t.Biomes[i].Def().Label, i), int(terrainTool.Biome) == i+1) {
			terrainTool.Biome = uint8(i + 1)
			if terrainTool.Mode != terrainPaint {
				editorSetTerrainMode(terrainPaint)
			}
		}
	}
	if terrainTool.hoverValid {
		x, y, z, d := t.FaceBlock(terrainTool.hoverFace)
		editorTextMuted(fmt.Sprintf("Block %d,%d,%d  face %s", x, y, z, terrainFaceName(d)))
	}
}

func terrainFaceName(d int) string {
	return [...]string{"+X", "-X", "top", "bottom", "+Z", "-Z"}[d]
}

// editorTerrainPanelChunk is the terrain the tool panel describes: the one
// being stroked, else the selected chunk's, else the one under the cursor.
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

// editorTerrainLayerCombos is the four material texture pickers.
func editorTerrainLayerCombos(ch *world.Chunk) {
	t := ch.Terrain
	for l := 0; l < world.TerrainLayerCount; l++ {
		name := t.Layers[l]
		if textureCombo(fmt.Sprintf("Material %d##terraintex%d", l+1, l), &name, true) {
			EditorPushUndo()
			t.Layers[l] = name
			t.Touch()
		}
	}
}

// terrainDefaultLayers are the textures a new terrain's materials start as,
// where the project has them.
var terrainDefaultLayers = [world.TerrainLayerCount]string{"grass", "dirt", "rock", "sand"}

// The Add Terrain / Regenerate form's values, kept between frames.
var (
	terrainNewSize      = [3]int32{64, 32, 64}
	terrainNewBlockSize float32 = 1
	terrainFill                 = world.TerrainFill{Height: 10, Hills: 5, HillSize: 22, Top: 1, Under: 2, Deep: 3, UnderDepth: 3}
	terrainFillBiome            = "meadow"
	terrainAddPreset            = "meadow"
	terrainResize       [3]int32
	terrainResizeFor    *world.Terrain
)

// editorTerrainFillForm is the ground-shape fields shared by Add Terrain and
// Regenerate.
func editorTerrainFillForm(maxHeight int) {
	h := int32(terrainFill.Height)
	if imgui.SliderInt("Ground height##terrainfillh", &h, 1, int32(maxHeight)) {
		terrainFill.Height = int(h)
	}
	editorDragFloatStep("Hills##terrainfillhills", &terrainFill.Hills, 0, float32(maxHeight), "%.1f", 0.5)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How many blocks the ground rises and falls; 0 is flat.")
	}
	editorDragFloatStep("Hill size##terrainfillsize", &terrainFill.HillSize, 2, 256, "%.0f", 1)
	seed := int32(terrainFill.Seed)
	if imgui.DragInt("Seed##terrainfillseed", &seed) {
		terrainFill.Seed = uint32(seed)
	}
	if imgui.BeginCombo("Grow##terrainfillbiome", terrainPresetLabel(terrainFillBiome)) {
		if imgui.SelectableV("(bare ground)##terrainfillnone", terrainFillBiome == "", 0, imgui.Vec2{}) {
			terrainFillBiome = ""
		}
		for _, name := range world.FoliagePresetNames() {
			if imgui.SelectableV(terrainPresetLabel(name)+"##terrainfill"+name, terrainFillBiome == name, 0, imgui.Vec2{}) {
				terrainFillBiome = name
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A biome painted over the whole top of the ground as it is made.")
	}
}

func terrainPresetLabel(name string) string {
	if name == "" {
		return "(bare ground)"
	}
	if p := world.FoliagePresetByName(name); p != nil {
		return p.Label
	}
	return name
}

// editorGenerateTerrain fills t from the form, painting its biome over the
// top if one is chosen (adding it to the palette if it is not there yet).
func editorGenerateTerrain(t *world.Terrain) {
	f := terrainFill
	f.Height = min(f.Height, t.SizeY)
	f.Biome = 0
	if terrainFillBiome != "" {
		for i := range t.Biomes {
			if t.Biomes[i].Preset == terrainFillBiome {
				f.Biome = uint8(i + 1)
			}
		}
		if f.Biome == 0 && len(t.Biomes) < world.TerrainMaxBiomes {
			t.Biomes = append(t.Biomes, world.TerrainBiome{Preset: terrainFillBiome})
			f.Biome = uint8(len(t.Biomes))
		}
	}
	t.Generate(f)
}

// editorChunkTerrain is the chunk properties' Terrain group: add a terrain,
// or set up the one the chunk has.
func editorChunkTerrain(ch *world.Chunk) {
	t := ch.Terrain
	if t == nil {
		editorTextMutedWrapped("Ground built from blocks: mine it, build on it, show it as blocks or as a smooth surface, and paint biomes on it that grow their own plants. Worked with the Terrain tool [T].")
		imgui.DragInt3V("Size (blocks)##terrainnewsize", &terrainNewSize, 1, 1, world.TerrainMaxSize, "%d", 0)
		editorDragFloatStep("Block size##terrainnewbs", &terrainNewBlockSize, 0.125, 16, "%.3f", 0.25)
		editorTerrainFillForm(int(terrainNewSize[1]))
		w := float32(terrainNewSize[0]) * terrainNewBlockSize
		d := float32(terrainNewSize[2]) * terrainNewBlockSize
		editorTextMuted(fmt.Sprintf("%.1f x %.1f units, centred on the chunk's origin", w, d))
		if imgui.Button("Add Terrain##terrainadd") {
			EditorPushUndo()
			sz := [3]int{int(terrainNewSize[0]), int(terrainNewSize[1]), int(terrainNewSize[2])}
			top := float32(min(terrainFill.Height, sz[1])) * terrainNewBlockSize
			t := world.NewTerrain(mgl32.Vec3{-w / 2, -top, -d / 2}, terrainNewBlockSize, sz[0], sz[1], sz[2])
			for l, name := range terrainDefaultLayers {
				if _, ok := gfx.TextureNames[name]; ok {
					t.Layers[l] = name
					_ = gfx.EnsureTextureLoaded(name)
				}
			}
			editorGenerateTerrain(t)
			ch.Terrain = t
			editorChunkGeometryChanged(ch)
			editorArmTerrainTool()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("The ground's top lands level with the chunk's origin.")
		}
		return
	}

	editorTextMuted(fmt.Sprintf("%d x %d x %d blocks of %.3g", t.SizeX, t.SizeY, t.SizeZ, t.BlockSize))
	if editorActiveToggleButton("Blocks##chunkterrainblocks", !t.Smooth) && t.Smooth {
		editorToggleTerrainSmooth(ch)
	}
	imgui.SameLine()
	if editorActiveToggleButton("Smooth##chunkterrainsmooth", t.Smooth) && !t.Smooth {
		editorToggleTerrainSmooth(ch)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Draw (and collide with) the blocks as a smooth surface: a dual-contoured\nsurface net over the same grid. Mining and building still work block by block.")
	}
	imgui.SameLine()
	if editorActiveToggleButton("Edit [T]##terrainarm", terrainTool.Active) {
		editorToggleTerrainTool()
	}
	if t.Smooth {
		sm := t.EffectiveSmoothing()
		if editorDragFloatStep("Smoothing##terrainsmoothing", &sm, world.TerrainSmoothingMin, world.TerrainSmoothingMax, "%.2f", 0.1) {
			t.Smoothing = sm
			t.Touch()
			editorChunkGeometryChanged(ch)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("How widely the smooth surface blends the blocks, in blocks.\nHigher rolls gentler hills — steps further apart — into one slope;\nlower keeps more of the blocks' shape. Cliffs, tunnels and ledges survive either way.")
		}
	}

	origin := [3]float32{t.Origin.X(), t.Origin.Y(), t.Origin.Z()}
	if editorDragFloat3("Corner##terrainorigin", &origin, -1e5, 1e5, "%.2f") {
		t.Origin = mgl32.Vec3{origin[0], origin[1], origin[2]}
		t.Touch()
		editorChunkGeometryChanged(ch)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Where the grid's first corner (its lowest X, Y and Z) sits in the chunk.")
	}
	size := t.BlockSize
	if editorDragFloatStep("Block size##terrainblocksize", &size, 0.125, 16, "%.3f", 0.125) && size > 0 {
		t.BlockSize = size
		t.Touch()
		editorChunkGeometryChanged(ch)
	}
	scale := t.EffectiveTextureScale()
	if editorDragFloatStep("Texture scale##terraintexscale", &scale, 0.125, 64, "%.3f", 0.125) {
		t.TextureScale = scale
		t.Touch()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Units of ground one repeat of a material's texture covers.")
	}
	editorTerrainLayerCombos(ch)

	imgui.Separator()
	editorTerrainBiomes(ch)

	imgui.Separator()
	if imgui.TreeNodeV("Regenerate##terrainregen", 0) {
		editorTerrainFillForm(t.SizeY)
		if imgui.Button("Regenerate Ground##terrainregenbtn") {
			EditorPushUndo()
			editorTerrainEndStroke()
			editorGenerateTerrain(t)
			editorChunkGeometryChanged(ch)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Replaces every block and all the painting. Ctrl+Z brings them back.")
		}
		imgui.TreePop()
	}
	if imgui.TreeNodeV("Resize##terrainresizenode", 0) {
		if terrainResizeFor != t {
			terrainResizeFor = t
			terrainResize = [3]int32{int32(t.SizeX), int32(t.SizeY), int32(t.SizeZ)}
		}
		imgui.DragInt3V("Size##terrainresize", &terrainResize, 1, 1, world.TerrainMaxSize, "%d", 0)
		same := int(terrainResize[0]) == t.SizeX && int(terrainResize[1]) == t.SizeY && int(terrainResize[2]) == t.SizeZ
		imguibackend.EditorPushDisabled(same)
		if imgui.Button("Apply Resize##terrainresizebtn") {
			EditorPushUndo()
			editorTerrainEndStroke()
			t.Resize(int(terrainResize[0]), int(terrainResize[1]), int(terrainResize[2]))
			editorChunkGeometryChanged(ch)
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Grows or crops the grid from its far sides; the first corner stays put.")
		}
		imgui.TreePop()
	}

	if imgui.Button("Remove Terrain##terrainremove") {
		EditorPushUndo()
		editorTerrainEndStroke()
		ch.Terrain = nil
		editorChunkGeometryChanged(ch)
	}
}

// editorTerrainBiomes is the Terrain group's biome palette: what can be
// painted, and how thickly and in what colours each grows.
func editorTerrainBiomes(ch *world.Chunk) {
	t := ch.Terrain
	imgui.Text("Biomes")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Paint these onto the terrain's faces with the tool's Paint Biome mode [4].\nWhat grows where — grass on open slopes, ferns in the shade, moss and vines\non cliffs, stones at a cliff's foot — is worked out from the blocks.")
	}
	for _, name := range biomeFileErrors() {
		imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.45, Z: 0.4, W: 1})
		imgui.Text("biomes/" + name + world.BiomeExt + " does not parse")
		imgui.PopStyleColor()
		if imgui.IsItemHovered() {
			imgui.SetTooltip(biomePoll.errors[name])
		}
	}
	remove := -1
	for i := range t.Biomes {
		b := &t.Biomes[i]
		imgui.PushID(fmt.Sprintf("terrainbiome%d", i))
		open := imgui.TreeNodeV(fmt.Sprintf("%d: %s###biome", i+1, b.Def().Label), 0)
		if open {
			if imgui.BeginCombo("Kind", b.Def().Label) {
				for _, name := range world.FoliagePresetNames() {
					if imgui.SelectableV(terrainPresetLabel(name), b.Preset == name, 0, imgui.Vec2{}) && b.Preset != name {
						EditorPushUndo()
						b.Preset = name
						t.Touch()
					}
				}
				imgui.EndCombo()
			}
			dens := b.EffectiveDensity()
			if editorDragFloatStep("Density", &dens, 0.1, 4, "%.2f", 0.05) {
				b.Density = dens
				t.Touch()
			}
			tint := b.EffectiveTint()
			col := [3]float32{tint[0], tint[1], tint[2]}
			if imgui.ColorEdit3("Tint", &col) {
				b.Tint = mgl32.Vec3{col[0], col[1], col[2]}
				t.Touch()
			}
			editorTerrainBiomeAmounts(t, b)
			own := "(the block's own)"
			if tex := b.Def().GroundTexture; tex != "" {
				own = "(the biome's: " + tex + ")"
			}
			layer := own
			if b.GroundLayer > 0 && b.GroundLayer <= world.TerrainLayerCount {
				layer = fmt.Sprintf("%d: %s", b.GroundLayer, t.Layers[b.GroundLayer-1])
			}
			if imgui.BeginCombo("Ground", layer) {
				if imgui.SelectableV(own, b.GroundLayer == 0, 0, imgui.Vec2{}) {
					EditorPushUndo()
					b.GroundLayer = 0
					t.Touch()
				}
				for l := 0; l < world.TerrainLayerCount; l++ {
					if imgui.SelectableV(fmt.Sprintf("%d: %s", l+1, t.Layers[l]), b.GroundLayer == l+1, 0, imgui.Vec2{}) {
						EditorPushUndo()
						b.GroundLayer = l + 1
						t.Touch()
					}
				}
				imgui.EndCombo()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("What painted faces show. By default the biome's own ground texture\n(its file's ground.texture), or the block's layer if it names none;\npick a layer to use that instead.")
			}
			if imgui.Button("Remove") {
				remove = i
			}
			imgui.TreePop()
		}
		imgui.PopID()
	}
	if remove >= 0 {
		EditorPushUndo()
		editorTerrainEndStroke()
		t.RemoveBiome(remove + 1)
		if int(terrainTool.Biome) > len(t.Biomes) {
			terrainTool.Biome = uint8(max(len(t.Biomes), 1))
		}
	}
	if len(t.Biomes) < world.TerrainMaxBiomes {
		if imgui.BeginCombo("##terrainaddpreset", terrainPresetLabel(terrainAddPreset)) {
			for _, name := range world.FoliagePresetNames() {
				if imgui.SelectableV(terrainPresetLabel(name), terrainAddPreset == name, 0, imgui.Vec2{}) {
					terrainAddPreset = name
				}
			}
			imgui.EndCombo()
		}
		imgui.SameLine()
		if imgui.Button("Add Biome##terrainaddbiome") && terrainAddPreset != "" {
			EditorPushUndo()
			t.Biomes = append(t.Biomes, world.TerrainBiome{Preset: terrainAddPreset})
			t.Touch()
			terrainTool.Biome = uint8(len(t.Biomes))
		}
	}
	seed := int32(t.FoliageSeed)
	if imgui.DragInt("Foliage seed##terrainfseed", &seed) {
		t.FoliageSeed = uint32(seed)
		t.Touch()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Changes where every plant falls without changing where anything grows.")
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
		imgui.SetTooltip("Mine, build and paint biomes on chunks' block terrain.\n" +
			"Click to use; hold to keep going; Shift inverts; 1-5 pick the mode;\n" +
			"[ ] reach, , . material or biome, N blocks/smooth, Esc puts it away.")
	}
}

// editorTerrainBiomeAmounts is a biome slot's per-kind amounts: one slider
// for each kind of thing its biome grows — grass, flowers, rocks, trees — so
// a meadow can be made all flowers, or a forest thinned to a few trees over
// the same undergrowth, on this terrain only. The biome file is untouched.
func editorTerrainBiomeAmounts(t *world.Terrain, b *world.TerrainBiome) {
	shapes := b.Def().Shapes()
	if len(shapes) == 0 {
		return
	}
	label := "Amounts"
	if len(b.Amounts) > 0 {
		label = fmt.Sprintf("Amounts (%d changed)", len(b.Amounts))
	}
	if !imgui.TreeNodeV(label+"###amounts", 0) {
		if imgui.IsItemHovered() {
			imgui.SetTooltip("How much of each kind of thing grows here, on this terrain.")
		}
		return
	}
	changed := false
	for _, sh := range shapes {
		v := b.Amount(sh)
		if imgui.SliderFloatV(sh.String(), &v, 0, world.MaxFoliageAmount, "%.2fx", 0) {
			b.SetAmount(sh, v)
			changed = true
		}
		if imgui.IsItemActivated() {
			// Once per drag, before the first change: the whole drag is one
			// undo step.
			EditorPushUndo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(world.FoliageShapeDocs[sh] + "\n\n0 grows none, 1 as the biome has it, 4 four times as many.\nCtrl+click to type a value.")
		}
	}
	if len(b.Amounts) > 0 && imgui.Button("Reset Amounts") {
		EditorPushUndo()
		b.Amounts = nil
		changed = true
	}
	if changed {
		// What grows changed, not the ground's shape: the foliage and face
		// looks rebuild, the surface does not.
		t.TouchPaint(0, 0, 0, t.SizeX-1, t.SizeY-1, t.SizeZ-1)
	}
	imgui.TreePop()
}
