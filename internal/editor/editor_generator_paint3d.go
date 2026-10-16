package editor

// editor_generator_paint3d.go — the paint brush in the level editor's 3D
// view (docs/terrain-paint/SPEC.md "Editor", docs/terrain-paint/PHASE_P2.md,
// docs/guide/36-terrain-paint.md).
//
// WHEN. The brush is in the 3D view while the Generator window is open with
// its Paint tool on and a layer chosen, and the level editor holds the
// generator's target level in 3D mode — and no other placement tool (the
// terrain tool, the scenery brush, a stamp) is armed. It is the same brush
// as the 2D view's: one genPaint state (layer, radius, strength, falloff,
// curve, spacing, mode), one stroke in progress, one undo stack, and every
// commit starts the live rerun (editor_generator_paint_live.go).
//
// AIMING. Each frame the cursor's ray is cast through the level
// (collision.Raycast, the level's solid geometry: the generated terrain's
// tiles collide as their cooked mesh, so the ray stops on the ground the
// author sees). The world x and z of the hit is where a stamp lands; paint
// is world-level, so nothing else about the hit matters. Left drag paints
// (stamps along the ray hits' path, spaced as the 2D view's), Shift held
// as it starts erases, [ and ] resize, Ctrl+wheel sets strength, Ctrl+Z /
// Ctrl+Shift+Z (and Ctrl+Y) undo and redo strokes ahead of the level's own
// undo while the brush is here.
//
// THE RING lies on the ground: 48 points round the brush, each dropped onto
// the terrain by a downward ray, drawn through the gizmo line buffer — the
// outer edge, and the inner edge of the falloff when there is one.
//
// THE OVERLAY is the active layer drawn ON the terrain, in a pass of its own
// after the world (editor-only, nothing of the world or Nanite material
// passes: no samplers, FX bits or variants claimed there). Each stored
// paint tile gets a mesh that drapes the ground — a 33 × 33 grid at 2 m,
// every vertex dropped onto the terrain by a downward ray and lifted a
// little — textured with the tile's overlay colours (genPaintTileRGBA, the
// 2D view's tint), alpha-blended, depth-tested against the world with a
// polygon offset so it neither sinks into the ground nor shows through
// hills. A tile's mesh depends only on the ground, so it is built once (a
// few tiles a frame) and kept; its texture is re-uploaded when a stamp
// writes the tile. The program is this file's own, inline, on texture unit
// 0 for its own draw.

import (
	"math"
	"time"
	"unsafe"

	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/levelio"
	"foundry/internal/procgen/paint"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// genPaint3D is the 3D brush's per-frame state.
var genPaint3D struct {
	hoverValid bool
	hover      mgl32.Vec3
	stroking   bool
	// downAt is when the stroke began, for the stroke's own timing.
	downAt time.Time
}

// genPaint3DActive reports whether the 3D view's clicks belong to the paint
// brush (file comment).
func genPaint3DActive() bool {
	if !genPaint.on || generatorEditorWindow == nil || genDoc == nil ||
		(genPaint.layer == "" || genDoc.PaintDef(genPaint.layer) == nil) && !genPaintSplineBrush() || Editor.level == nil || Editor.Mode != Mode3D {
		return false
	}
	if genDoc.Level == "" || !genLevelIsOpen(levelio.LevelPath(genDoc.Level)) {
		return false
	}
	if terrainTool.Active || Editor.BrushActive || (ws() != nil && ws().placementArmed()) {
		return false
	}
	return ws() == nil || ws().level == Editor.level
}

// genPaintRayHit is where the world-space ray meets the level's solid
// geometry (the generated terrain), within 4 km.
var genPaintRayHit = func(lvl *world.Level, origin, dir mgl32.Vec3) (mgl32.Vec3, bool) {
	r := collision.Raycast(lvl, origin, dir, collision.RayFilter{Range: 4000, Geometry: true})
	if !r.Blocked {
		return mgl32.Vec3{}, false
	}
	return r.Stop, true
}

// genPaintGroundAt is the ground's height under (x, z), dropped from above
// the cursor's hit; ok false where no ground is found.
func genPaintGroundAt(lvl *world.Level, x, z, above float32) (float32, bool) {
	p, ok := genPaintRayHit(lvl, mgl32.Vec3{x, above, z}, mgl32.Vec3{0, -1, 0})
	return p.Y(), ok
}

// genPaint3DFrame runs once a frame from EditorRender: find the ground under
// the cursor and carry the stroke on.
func genPaint3DFrame() {
	if !genPaint3DActive() {
		genPaint3D.hoverValid = false
		if genPaint3D.stroking {
			genPaint3DEnd()
		}
		return
	}
	mx, my, _ := sdl.GetMouseState()
	overPanel := imgui.CurrentIO().WantCaptureMouse() || mx >= panelLeftEdgeX()
	genPaint3D.hoverValid = false
	if !overPanel || genPaint3D.stroking {
		origin, dir := ws().ray(mx, my)
		if p, ok := genPaintRayHit(Editor.level, origin, dir); ok {
			genPaint3D.hover, genPaint3D.hoverValid = p, true
		}
	}
	if genPaint3D.stroking && genPaint3D.hoverValid {
		genPaintDrag(float64(genPaint3D.hover.X()), float64(genPaint3D.hover.Z()))
	}
}

// genPaint3DMouseDown starts a stroke at the cursor's ground; true when the
// brush took the click (every left click while it is here, so a click meant
// for the ground never selects what it landed on).
func genPaint3DMouseDown() bool {
	if !genPaint3DActive() {
		return false
	}
	if genPaint3D.hoverValid {
		genPaint3D.stroking = true
		genPaint3D.downAt = time.Now()
		genPaintBegin(float64(genPaint3D.hover.X()), float64(genPaint3D.hover.Z()))
	}
	return true
}

// genPaint3DEnd commits the 3D stroke (mouse-up anywhere).
func genPaint3DEnd() bool {
	if !genPaint3D.stroking {
		return false
	}
	genPaint3D.stroking = false
	genPaintEnd()
	return true
}

// genPaint3DHandleKey takes the brush's keys in the level editor: undo and
// redo of strokes (ahead of the level's undo) and [ ] for the radius.
func genPaint3DHandleKey(sc sdl.Scancode, mods sdl.Keymod) bool {
	if !genPaint3DActive() {
		return false
	}
	ctrl, shift := mods&sdl.KMOD_CTRL != 0, mods&sdl.KMOD_SHIFT != 0
	switch {
	case ctrl && sc == sdl.SCANCODE_Z && shift, ctrl && sc == sdl.SCANCODE_Y:
		genPaint3DEnd()
		genPaintRedo()
	case ctrl && sc == sdl.SCANCODE_Z:
		genPaint3DEnd()
		genPaintUndo()
	case !ctrl && sc == sdl.SCANCODE_LEFTBRACKET:
		genPaintResize(1 / 1.2)
	case !ctrl && sc == sdl.SCANCODE_RIGHTBRACKET:
		genPaintResize(1.2)
	default:
		return false
	}
	return true
}

// genPaintResize scales the brush's radius.
func genPaintResize(f float64) {
	genPaint.radius = float32(math.Min(500, math.Max(0.25, float64(genPaint.radius)*f)))
}

// genPaintWheelStrength is Ctrl+wheel: strength up or down a notch per
// click; true when it took the wheel.
func genPaintWheelStrength(dy int32, ctrl bool) bool {
	if !ctrl || dy == 0 {
		return false
	}
	s := float64(genPaint.strength) * math.Pow(1.15, float64(dy))
	genPaint.strength = float32(math.Min(1, math.Max(0.01, s)))
	genStatusf("Paint strength %.2f", genPaint.strength)
	return true
}

// genPaint3DWheel takes Ctrl+wheel in the 3D view while the brush is here.
func genPaint3DWheel(dy int32) bool {
	return genPaint3DActive() && genPaintWheelStrength(dy, sdl.GetModState()&sdl.KMOD_CTRL != 0)
}

// ---------- the ring ----------

// genPaintRingPoints is the brush's ring of n points at radius r round the
// hover point, each dropped onto the ground (a point with no ground under it
// keeps the hover height).
func genPaintRingPoints(lvl *world.Level, c mgl32.Vec3, r float32, n int) []mgl32.Vec3 {
	out := make([]mgl32.Vec3, n)
	for i := range out {
		a := float64(i) * 2 * math.Pi / float64(n)
		x, z := c.X()+r*float32(math.Cos(a)), c.Z()+r*float32(math.Sin(a))
		y := c.Y()
		if gy, ok := genPaintGroundAt(lvl, x, z, c.Y()+r+50); ok {
			y = gy
		}
		out[i] = mgl32.Vec3{x, y + 0.08, z}
	}
	return out
}

// drawGenPaintRing outlines the brush on the ground (gizmo lines).
func drawGenPaintRing() {
	if !genPaint3DActive() || !genPaint3D.hoverValid {
		return
	}
	col := mgl32.Vec3{1, 1, 1}
	if genPaint.mode == paint.ModeErase || (genPaint.stroking && genPaint.strokeMode == paint.ModeErase) ||
		(!genPaint.stroking && genPaintShiftHeld()) {
		col = mgl32.Vec3{1, 0.4, 0.3}
	}
	ring := func(r float32, c mgl32.Vec3) {
		pts := genPaintRingPoints(Editor.level, genPaint3D.hover, r, 48)
		for i := range pts {
			gizmo.GizmoLine(pts[i], pts[(i+1)%len(pts)], c)
		}
	}
	ring(genPaint.radius, col)
	if f := genPaint.falloff; f > 0 && f < 1 {
		ring(genPaint.radius*(1-f), col.Mul(0.55))
	}
	// The centre: a short upright tick.
	h := genPaint3D.hover
	gizmo.GizmoLine(h, h.Add(mgl32.Vec3{0, 0.6 + genPaint.radius*0.05, 0}), col)
}

// ---------- the projected overlay ----------

// genPaint3DGrid is a tile mesh's vertices a side (2 m apart over 64 m).
const genPaint3DGrid = 33

// genPaint3DLift is how far above the ground the overlay floats (metres),
// with the polygon offset doing the rest.
const genPaint3DLift = 0.12

// genPaint3DBuildsPerFrame bounds the tile meshes built a frame.
const genPaint3DBuildsPerFrame = 2

// genPaint3DOverlay caches the overlay's tile meshes and textures.
var genPaint3DOverlay = struct {
	layer string
	lvl   *world.Level
	tiles map[paint.TileKey]*genPaint3DTile
	dirty map[paint.TileKey]bool
	// reset asks the next draw to drop everything (it is set from the
	// Generator window, whose context may not be the one they live in).
	reset  bool
	prog   uint32
	locPV  int32
	failed bool
}{tiles: map[paint.TileKey]*genPaint3DTile{}, dirty: map[paint.TileKey]bool{}}

// genPaint3DTile is one paint tile's draped mesh and texture.
type genPaint3DTile struct {
	verts []float32 // x, y, z, u, v
	vbo   uint32
	tex   uint32
	built bool
}

// genPaint3DFreeOverlay asks for every tile to be rebuilt (deferred to the
// next draw, on the main window's context).
func genPaint3DFreeOverlay() { genPaint3DOverlay.reset = true }

// genPaint3DDrop releases the overlay's GL objects. Main window context.
func genPaint3DDrop() {
	o := &genPaint3DOverlay
	var texs []uint32
	for _, t := range o.tiles {
		if t.vbo != 0 {
			gl.DeleteBuffers(1, &t.vbo)
		}
		texs = append(texs, t.tex)
	}
	genDeleteTextures(texs)
	o.tiles = map[paint.TileKey]*genPaint3DTile{}
	o.dirty = map[paint.TileKey]bool{}
	o.reset = false
}

// genPaint3DTileMesh drapes tile k's grid over the ground: two triangles a
// cell, every vertex on the terrain plus the lift. ok false when no vertex
// found ground.
func genPaint3DTileMesh(lvl *world.Level, k paint.TileKey, above float32) ([]float32, bool) {
	r := k.Rect()
	n := genPaint3DGrid
	step := float32(paint.TileSize) / float32(n-1)
	ys := make([]float32, n*n)
	got := make([]bool, n*n)
	any := false
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			x, z := float32(r[0])+float32(i)*step, float32(r[1])+float32(j)*step
			if y, ok := genPaintGroundAt(lvl, x, z, above); ok {
				ys[j*n+i], got[j*n+i], any = y+genPaint3DLift, true, true
			}
		}
	}
	if !any {
		return nil, false
	}
	out := make([]float32, 0, (n-1)*(n-1)*6*5)
	v := func(i, j int) {
		x, z := float32(r[0])+float32(i)*step, float32(r[1])+float32(j)*step
		out = append(out, x, ys[j*n+i], z, float32(i)/float32(n-1), float32(j)/float32(n-1))
	}
	for j := 0; j+1 < n; j++ {
		for i := 0; i+1 < n; i++ {
			// A cell with a corner off the ground (a hole, the edge of the
			// world) is left out rather than pulled to y = 0.
			if !got[j*n+i] || !got[j*n+i+1] || !got[(j+1)*n+i] || !got[(j+1)*n+i+1] {
				continue
			}
			v(i, j)
			v(i, j+1)
			v(i+1, j)
			v(i+1, j)
			v(i, j+1)
			v(i+1, j+1)
		}
	}
	return out, true
}

// genPaint3DSync brings the cache in step with the active layer: tiles
// erased away dropped, new ones' meshes built (a few a frame), written ones'
// textures re-uploaded. Main window context.
func genPaint3DSync(l *paint.Layer, above float32) {
	o := &genPaint3DOverlay
	if o.reset || o.layer != genPaint.layer || o.lvl != Editor.level {
		genPaint3DDrop()
		o.layer, o.lvl = genPaint.layer, Editor.level
	}
	for k, t := range o.tiles {
		if l.Tile(k) == nil {
			if t.vbo != 0 {
				gl.DeleteBuffers(1, &t.vbo)
			}
			genDeleteTextures([]uint32{t.tex})
			delete(o.tiles, k)
		}
	}
	builds := genPaint3DBuildsPerFrame
	for _, k := range l.Keys() {
		t := o.tiles[k]
		if t == nil {
			if builds <= 0 {
				continue
			}
			builds--
			t = &genPaint3DTile{}
			t.verts, t.built = genPaint3DTileMesh(Editor.level, k, above)
			if t.built {
				gl.GenBuffers(1, &t.vbo)
				gl.BindBuffer(gl.ARRAY_BUFFER, t.vbo)
				gl.BufferData(gl.ARRAY_BUFFER, len(t.verts)*4, gl.Ptr(t.verts), gl.STATIC_DRAW)
				gl.BindBuffer(gl.ARRAY_BUFFER, 0)
			}
			o.tiles[k] = t
			o.dirty[k] = true
		}
		if o.dirty[k] && t.built {
			genDeleteTextures([]uint32{t.tex})
			tile := l.Tile(k)
			t.tex = genUploadTexture(tile.N, tile.N, genPaintTileRGBA(l.Def, tile))
		}
		delete(o.dirty, k)
	}
}

const genPaint3DVert = `#version 430 core
layout(location = 0) in vec3 pos;
layout(location = 1) in vec2 uv;
uniform mat4 projView;
out vec2 vUV;
void main() { vUV = uv; gl_Position = projView * vec4(pos, 1.0); }
`

const genPaint3DFrag = `#version 430 core
in vec2 vUV;
uniform sampler2D overlay;
out vec4 color;
void main() {
	vec4 c = texture(overlay, vUV);
	if (c.a < 0.01) discard;
	color = vec4(c.rgb, min(c.a * 1.2, 0.85));
}
`

// drawGenPaintOverlay draws the active layer on the terrain (file comment).
// Called with depth testing on, after the world.
func drawGenPaintOverlay(projView mgl32.Mat4) {
	o := &genPaint3DOverlay
	if !genPaint3DActive() || o.failed {
		if o.reset && len(o.tiles) > 0 {
			genPaint3DDrop()
		}
		return
	}
	l := genPaintLayer()
	if l == nil {
		return
	}
	if o.prog == 0 {
		p, err := gfx.CompileProgramFromSources(genPaint3DVert, genPaint3DFrag)
		if err != nil {
			o.failed = true
			editorSetStatus("Paint overlay: %v", err)
			return
		}
		o.prog = p
		o.locPV = gl.GetUniformLocation(p, gl.Str("projView\x00"))
	}
	above := float32(2000)
	if genPaint3D.hoverValid {
		above = genPaint3D.hover.Y() + 500
	}
	genPaint3DSync(l, above)

	prevBlend, prevCull := gl.IsEnabled(gl.BLEND), gl.IsEnabled(gl.CULL_FACE)
	var vao uint32
	gl.GenVertexArrays(1, &vao)
	gl.BindVertexArray(vao)
	gl.UseProgram(o.prog)
	gl.UniformMatrix4fv(o.locPV, 1, false, &projView[0])
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	gl.DepthMask(false)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(-2, -4)
	gl.Disable(gl.CULL_FACE)
	stride := int32(5 * 4)
	for _, t := range o.tiles {
		if !t.built || t.tex == 0 || t.vbo == 0 {
			continue
		}
		gl.BindTexture(gl.TEXTURE_2D, t.tex)
		gl.BindBuffer(gl.ARRAY_BUFFER, t.vbo)
		gl.EnableVertexAttribArray(0)
		gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
		gl.EnableVertexAttribArray(1)
		gl.VertexAttribPointerWithOffset(1, 2, gl.FLOAT, false, stride, uintptr(3*unsafe.Sizeof(float32(0))))
		gl.DrawArrays(gl.TRIANGLES, 0, int32(len(t.verts)/5))
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.DepthMask(true)
	if !prevBlend {
		gl.Disable(gl.BLEND)
	}
	if prevCull {
		gl.Enable(gl.CULL_FACE)
	}
	gl.BindVertexArray(0)
	gl.DeleteVertexArrays(1, &vao)
	gl.UseProgram(gfx.Program)
}

// genPaintCtrlHeld reports whether Ctrl is down.
func genPaintCtrlHeld() bool { return sdl.GetModState()&sdl.KMOD_CTRL != 0 }
