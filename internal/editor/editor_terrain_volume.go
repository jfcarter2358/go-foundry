package editor

// editor_terrain_volume.go — the terrain tool's Volume mode: lay a box of
// blocks out by dragging its faces, then fill it (or carve it) in one go.
//
// # The box
//
// A click puts down a one-block box in the cell the cursor points at — the
// cell Place would fill, in front of the face under it; over a terrain that
// has nothing solid to point at, the cell on the grid's floor. The box is a
// preview: nothing changes until it is filled or carved.
//
// Each of the box's six faces is a handle. The one under the cursor lights
// up (the face itself where the box is in front of it, or the square at its
// centre from any side), and dragging it slides that face along its normal a
// whole block at a time — the brush editor's face drag, on the grid. A click
// anywhere off the box starts a new one.
//
//	Enter          fill the box with the current material
//	Shift+Enter    carve it out (Backspace and Delete too)
//	Esc            drop the box (again: put the tool away)
//
// # Past the grid
//
// The box may reach outside the terrain's grid on any side. Filling it grows
// the grid to fit (world.Terrain.GrowToInclude) without moving anything
// already built; carving only ever touches blocks that exist, so it never
// grows it. Fill or carve, it is one undo step.

import (
	"fmt"
	"math"

	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// terrainVolumeState is the box being laid out, if any.
type terrainVolumeState struct {
	active  bool
	chunk   int
	terrain *world.Terrain
	// lo and hi are the box's first and last block, inclusive, in the
	// terrain's block coordinates; either may lie outside the grid.
	lo, hi [3]int

	// hoverFace is the face (a world.TerrainDirs index) under the cursor,
	// or -1.
	hoverFace int

	// The face drag in progress: dragFace is -1 when there is none.
	dragFace   int
	dragOrigin mgl32.Vec3 // world point the face's axis runs through
	dragAxis   mgl32.Vec3 // world, unit, the face's outward normal
	dragUnit   float32    // world length of one block along dragAxis
	dragStart  float32    // axis parameter at the press
	dragFrom   int        // the face's block coordinate at the press
}

var terrainVol = terrainVolumeState{hoverFace: -1, dragFace: -1}

// editorTerrainVolumeCancel drops the box.
func editorTerrainVolumeCancel() {
	terrainVol = terrainVolumeState{hoverFace: -1, dragFace: -1}
}

// terrainVolumeChunk is the chunk the box belongs to, or nil — and the box
// dropped — once the level has changed under it (an undo, a load, the
// terrain removed).
func terrainVolumeChunk() *world.Chunk {
	if !terrainVol.active {
		return nil
	}
	lvl := Editor.level
	if lvl == nil || terrainVol.chunk >= len(lvl.Chunks) || lvl.Chunks[terrainVol.chunk].Terrain != terrainVol.terrain {
		editorTerrainVolumeCancel()
		return nil
	}
	return &lvl.Chunks[terrainVol.chunk]
}

// terrainVolumeSize is the box's extent in blocks.
func terrainVolumeSize() [3]int {
	return [3]int{
		terrainVol.hi[0] - terrainVol.lo[0] + 1,
		terrainVol.hi[1] - terrainVol.lo[1] + 1,
		terrainVol.hi[2] - terrainVol.lo[2] + 1,
	}
}

// terrainVolumeBounds is the box's chunk-space corners.
func terrainVolumeBounds(t *world.Terrain) (lo, hi mgl32.Vec3) {
	l, h := terrainVol.lo, terrainVol.hi
	return t.BlockMin(l[0], l[1], l[2]), t.BlockMin(h[0]+1, h[1]+1, h[2]+1)
}

// terrainBoxFaceCenter is face d's centre and its outward normal, both in
// chunk space.
func terrainBoxFaceCenter(lo, hi mgl32.Vec3, d int) (c, n mgl32.Vec3) {
	c = lo.Add(hi).Mul(0.5)
	s := world.TerrainDirs[d]
	a := d / 2
	if s[a] > 0 {
		c[a] = hi[a]
	} else {
		c[a] = lo[a]
	}
	return c, mgl32.Vec3{float32(s[0]), float32(s[1]), float32(s[2])}
}

// terrainBoxFaceCorners is face d's four corners in chunk space.
func terrainBoxFaceCorners(lo, hi mgl32.Vec3, d int) [4]mgl32.Vec3 {
	a := d / 2
	u, v := (a+1)%3, (a+2)%3
	var out [4]mgl32.Vec3
	for i := 0; i < 4; i++ {
		p := lo
		if world.TerrainDirs[d][a] > 0 {
			p[a] = hi[a]
		}
		if i == 1 || i == 2 {
			p[u] = hi[u]
		}
		if i >= 2 {
			p[v] = hi[v]
		}
		out[i] = p
	}
	return out
}

// editorTerrainVolumeStart puts a one-block box down where the cursor
// points, and reports whether it could.
func editorTerrainVolumeStart(origin, dir mgl32.Vec3) bool {
	lvl := Editor.level
	if lvl == nil {
		return false
	}
	var ci, x, y, z int
	switch {
	case terrainTool.hoverValid && terrainTool.hoverChunk < len(lvl.Chunks) && lvl.Chunks[terrainTool.hoverChunk].Terrain != nil:
		ci = terrainTool.hoverChunk
		t := lvl.Chunks[ci].Terrain
		x, y, z = terrainEditTarget(t, terrainEdit{mode: terrainPlace, face: terrainTool.hoverFace})
	default:
		// Nothing solid under the cursor: the grid's floor of the terrain the
		// panel is showing, so an empty grid can still be built on.
		ch := editorTerrainPanelChunk()
		if ch == nil {
			return false
		}
		t := ch.Terrain
		inv := ch.ModelMatrix().Inv()
		lo := inv.Mul4x1(origin.Vec4(1)).Vec3()
		ld := inv.Mul4x1(dir.Vec4(0)).Vec3()
		if math.Abs(float64(ld.Y())) < 1e-6 {
			return false
		}
		s := (t.Origin.Y() - lo.Y()) / ld.Y()
		if s <= 0 {
			return false
		}
		x, _, z = t.BlockAt(lo.Add(ld.Mul(s)))
		if !t.In(x, 0, z) {
			return false
		}
		ci = -1
		for i := range lvl.Chunks {
			if &lvl.Chunks[i] == ch {
				ci = i
			}
		}
		if ci < 0 {
			return false
		}
	}
	terrainVol = terrainVolumeState{
		active:    true,
		chunk:     ci,
		terrain:   lvl.Chunks[ci].Terrain,
		lo:        [3]int{x, y, z},
		hi:        [3]int{x, y, z},
		hoverFace: -1,
		dragFace:  -1,
	}
	editorSetStatus("Volume: drag its faces to size it, Enter to fill, Shift+Enter to carve, Esc to drop it")
	return true
}

// terrainVolumePickFace is the box face a ray from the cursor at (mx, my)
// grabs, or -1: a face-centre handle within reach on screen first, since those
// are the only way to grab the faces turned away from the camera, then the
// face the ray enters the box through.
func terrainVolumePickFace(ch *world.Chunk, mx, my int32, origin, dir mgl32.Vec3) int {
	t := ch.Terrain
	model := ch.ModelMatrix()
	lo, hi := terrainVolumeBounds(t)
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	best, bestD := -1, float32(gizmoPickPx)
	for d := 0; d < 6; d++ {
		c, _ := terrainBoxFaceCenter(lo, hi, d)
		sp, ok := projectToScreen(projView, model.Mul4x1(c.Vec4(1)).Vec3())
		if !ok {
			continue
		}
		if dd := sp.Sub(cursor).Len(); dd <= bestD {
			best, bestD = d, dd
		}
	}
	if best >= 0 {
		return best
	}
	inv := model.Inv()
	o := inv.Mul4x1(origin.Vec4(1)).Vec3()
	r := inv.Mul4x1(dir.Vec4(0)).Vec3()
	return rayBoxEntryFace(o, r, lo, hi)
}

// rayBoxEntryFace is the face (a world.TerrainDirs index) the ray o + s*d,
// s >= 0, enters box [lo, hi] through, or -1 when it misses it or starts
// inside it.
func rayBoxEntryFace(o, d, lo, hi mgl32.Vec3) int {
	t0, t1 := float32(0), float32(math.MaxFloat32)
	face := -1
	for a := 0; a < 3; a++ {
		if math.Abs(float64(d[a])) < 1e-9 {
			if o[a] < lo[a] || o[a] > hi[a] {
				return -1
			}
			continue
		}
		inv := 1 / d[a]
		n, f := (lo[a]-o[a])*inv, (hi[a]-o[a])*inv
		// Entering through the low side looks along -a.
		enter := 2*a + 1
		if n > f {
			n, f = f, n
			enter = 2 * a
		}
		if n > t0 {
			t0, face = n, enter
		}
		t1 = min(t1, f)
		if t0 > t1 {
			return -1
		}
	}
	return face
}

// editorTerrainVolumeMouseDown answers a left press in Volume mode: grab a
// face of the box, or put a new box down.
func editorTerrainVolumeMouseDown() {
	mx, my, _ := sdl.GetMouseState()
	origin, dir := ws().ray(mx, my)
	if ch := terrainVolumeChunk(); ch != nil {
		if d := terrainVolumePickFace(ch, mx, my, origin, dir); d >= 0 {
			editorTerrainVolumeBeginDrag(ch, d, mx, my)
			return
		}
	}
	editorTerrainVolumeStart(origin, dir)
}

// editorTerrainVolumeBeginDrag grabs face d of the box.
func editorTerrainVolumeBeginDrag(ch *world.Chunk, d int, mx, my int32) {
	t := ch.Terrain
	model := ch.ModelMatrix()
	lo, hi := terrainVolumeBounds(t)
	c, n := terrainBoxFaceCenter(lo, hi, d)
	axis := model.Mul4x1(n.Vec4(0)).Vec3()
	unit := axis.Len() * t.BlockSize
	if unit <= 0 {
		return
	}
	axis = axis.Mul(1 / axis.Len())
	wc := model.Mul4x1(c.Vec4(1)).Vec3()
	rayO, rayD := ws().ray(mx, my)
	start, ok := axisParamForRay(rayO, rayD, wc, axis)
	if !ok {
		editorSetStatus("That face points at the camera — turn the view to drag it")
		return
	}
	a := d / 2
	from := terrainVol.lo[a]
	if world.TerrainDirs[d][a] > 0 {
		from = terrainVol.hi[a]
	}
	terrainVol.dragFace = d
	terrainVol.dragOrigin = wc
	terrainVol.dragAxis = axis
	terrainVol.dragUnit = unit
	terrainVol.dragStart = start
	terrainVol.dragFrom = from
}

// editorTerrainVolumeDragging reports whether a face is being dragged.
func editorTerrainVolumeDragging() bool { return terrainVol.dragFace >= 0 }

// editorTerrainVolumeEndDrag lets the face go.
func editorTerrainVolumeEndDrag() { terrainVol.dragFace = -1 }

// editorTerrainVolumeFrame follows the cursor: the face being dragged, or the
// face the next press would grab.
func editorTerrainVolumeFrame(mx, my int32, overPanel bool) {
	ch := terrainVolumeChunk()
	if ch == nil {
		return
	}
	if !editorTerrainVolumeDragging() {
		terrainVol.hoverFace = -1
		if !overPanel {
			origin, dir := ws().ray(mx, my)
			terrainVol.hoverFace = terrainVolumePickFace(ch, mx, my, origin, dir)
		}
		return
	}
	rayO, rayD := ws().ray(mx, my)
	if p, ok := axisParamForRay(rayO, rayD, terrainVol.dragOrigin, terrainVol.dragAxis); ok {
		terrainVolumeDragTo(p)
	}
}

// terrainVolumeDragTo moves the dragged face to where axis parameter p lands,
// a whole block at a time, never past the opposite face (the box keeps at
// least one block) or further than a grid can reach.
func terrainVolumeDragTo(p float32) {
	steps := int(math.Round(float64((p - terrainVol.dragStart) / terrainVol.dragUnit)))
	d := terrainVol.dragFace
	a := d / 2
	before := terrainVolumeSize()
	if world.TerrainDirs[d][a] > 0 {
		terrainVol.hi[a] = min(max(terrainVol.dragFrom+steps, terrainVol.lo[a]), terrainVol.lo[a]+world.TerrainMaxSize-1)
	} else {
		terrainVol.lo[a] = max(min(terrainVol.dragFrom-steps, terrainVol.hi[a]), terrainVol.hi[a]-world.TerrainMaxSize+1)
	}
	if terrainVolumeSize() != before {
		s := terrainVolumeSize()
		editorSetStatus("Volume %d x %d x %d (%d blocks)", s[0], s[1], s[2], s[0]*s[1]*s[2])
	}
}

// editorTerrainVolumeCommit fills the box with the current material, or
// carves it out, as one undo step, and drops it.
func editorTerrainVolumeCommit(carve bool) {
	ch := terrainVolumeChunk()
	if ch == nil {
		return
	}
	editorTerrainVolumeEndDrag()
	t := ch.Terrain
	lo, hi := terrainVol.lo, terrainVol.hi
	v := terrainTool.Material
	if carve {
		v = 0
	}
	// The conditions EditorPushUndo pushes under, as a stroke records them.
	pushed := editorUndoRedirect == nil && !inPlaySession()
	EditorPushUndo()
	changed := false
	if !carve {
		shift, ok := t.GrowToInclude(lo, hi)
		if !ok {
			if pushed {
				EditorPopUndo()
			}
			editorSetStatus("Volume too big: a terrain is at most %d blocks along a side and %d in all", world.TerrainMaxSize, world.TerrainMaxBlocks)
			return
		}
		if shift != [3]int{} || !t.In(hi[0], hi[1], hi[2]) {
			changed = true
		}
		for a := 0; a < 3; a++ {
			lo[a] += shift[a]
			hi[a] += shift[a]
		}
	}
	changed = t.FillBox(lo, hi, v) || changed
	s := terrainVolumeSize()
	editorTerrainVolumeCancel()
	if !changed {
		if pushed {
			EditorPopUndo()
		}
		editorSetStatus("Volume: nothing to change")
		return
	}
	editorChunkGeometryChanged(ch)
	verb := "Filled"
	if carve {
		verb = "Carved"
	}
	editorSetStatus("%s %d x %d x %d blocks", verb, s[0], s[1], s[2])
}

// editorTerrainVolumeHandleKey owns Enter, Backspace, Delete and Esc while a
// box is down.
func editorTerrainVolumeHandleKey(scancode sdl.Scancode) bool {
	if terrainTool.Mode != terrainVolume || terrainVolumeChunk() == nil {
		return false
	}
	switch scancode {
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		editorTerrainVolumeCommit(sdl.GetModState()&sdl.KMOD_SHIFT != 0)
	case sdl.SCANCODE_BACKSPACE, sdl.SCANCODE_DELETE:
		editorTerrainVolumeCommit(true)
	case sdl.SCANCODE_ESCAPE:
		editorTerrainVolumeCancel()
		editorSetStatus("Volume dropped")
	default:
		return false
	}
	return true
}

// drawTerrainVolume outlines the box, its face handles and the face under the
// cursor — or, with no box down, the cell a click would start one in.
func drawTerrainVolume() {
	col := terrainModeColor(terrainVolume)
	ch := terrainVolumeChunk()
	if ch == nil {
		if !terrainTool.hoverValid || terrainTool.hoverChunk >= len(Editor.level.Chunks) {
			return
		}
		hc := &Editor.level.Chunks[terrainTool.hoverChunk]
		t := hc.Terrain
		if t == nil {
			return
		}
		model := hc.ModelMatrix()
		w := func(p mgl32.Vec3) mgl32.Vec3 { return model.Mul4x1(p.Vec4(1)).Vec3() }
		x, y, z := terrainEditTarget(t, terrainEdit{mode: terrainPlace, face: terrainTool.hoverFace})
		lo := t.BlockMin(x, y, z)
		pad := t.BlockSize * 0.02
		drawWireBox(lo.Sub(mgl32.Vec3{pad, pad, pad}), lo.Add(mgl32.Vec3{t.BlockSize + pad, t.BlockSize + pad, t.BlockSize + pad}), w, col)
		return
	}
	t := ch.Terrain
	model := ch.ModelMatrix()
	w := func(p mgl32.Vec3) mgl32.Vec3 { return model.Mul4x1(p.Vec4(1)).Vec3() }
	lo, hi := terrainVolumeBounds(t)
	drawWireBox(lo, hi, w, col)

	// The block grid on the box's faces, so its size in blocks reads at a
	// glance; skipped when the lines would crowd into a solid wash.
	s := terrainVolumeSize()
	faint := col.Mul(0.45)
	for d := 0; d < 6; d++ {
		a := d / 2
		u, v := (a+1)%3, (a+2)%3
		if s[u] > 64 || s[v] > 64 {
			continue
		}
		c := terrainBoxFaceCorners(lo, hi, d)
		for i := 1; i < s[u]; i++ {
			off := float32(i) * t.BlockSize
			p, q := c[0], c[3]
			p[u] += off
			q[u] += off
			gizmo.GizmoLine(w(p), w(q), faint)
		}
		for i := 1; i < s[v]; i++ {
			off := float32(i) * t.BlockSize
			p, q := c[0], c[1]
			p[v] += off
			q[v] += off
			gizmo.GizmoLine(w(p), w(q), faint)
		}
	}

	active := terrainVol.hoverFace
	if editorTerrainVolumeDragging() {
		active = terrainVol.dragFace
	}
	yellow := mgl32.Vec3{1, 1, 0}
	for d := 0; d < 6; d++ {
		c, _ := terrainBoxFaceCenter(lo, hi, d)
		hc := col
		if d == active {
			hc = yellow
		}
		gizmoBox(w(c), gizmoTipHalfPx, hc)
	}
	if active >= 0 {
		c := terrainBoxFaceCorners(lo, hi, active)
		for i := 0; i < 4; i++ {
			gizmo.GizmoLine(w(c[i]), w(c[(i+1)%4]), yellow)
		}
		gizmo.GizmoLine(w(c[0]), w(c[2]), yellow)
		gizmo.GizmoLine(w(c[1]), w(c[3]), yellow)
	}
}

// editorTerrainVolumeSection is Volume mode's part of the tool panel.
func editorTerrainVolumeSection() {
	if terrainTool.Mode != terrainVolume {
		return
	}
	if terrainVolumeChunk() == nil {
		editorTextMutedWrapped("Click to put a one-block box down in front of the face under the cursor, then drag its faces to size it.")
		return
	}
	s := terrainVolumeSize()
	imgui.Text(fmt.Sprintf("Volume %d x %d x %d  (%d blocks)", s[0], s[1], s[2], s[0]*s[1]*s[2]))
	if imgui.Button("Fill [Enter]##terrainvolfill") {
		editorTerrainVolumeCommit(false)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Fill the box with the selected material, growing the grid if the box reaches past it.")
	}
	imgui.SameLine()
	if imgui.Button("Carve [Shift+Enter]##terrainvolcarve") {
		editorTerrainVolumeCommit(true)
	}
	imgui.SameLine()
	if imgui.Button("Drop [Esc]##terrainvolcancel") {
		editorTerrainVolumeCancel()
	}
}
