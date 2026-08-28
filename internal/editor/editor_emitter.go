package editor

// editor_emitter.go — placing, selecting and picking particle emitters.
//
// The sprite workflow with the quad swapped for a marker: an emitter has no
// geometry of its own to click, because what it produces is the whole of what
// you see. So the picker tests a small camera-facing disc at its origin, and the
// viewport draws that disc plus, on request, the shape particles are born in.
//
// Selection is an index for the reason everything else here is: undo replaces
// the level wholesale and a pointer into the old one would outlive it.

import (
	"foundry/internal/gizmo"
	"foundry/internal/particle"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// emitterMarkerRadius is the clickable disc's size in world units. Big enough
// to hit without hunting, small enough not to swallow clicks meant for the
// geometry behind it.
const emitterMarkerRadius = 0.25

// ---------- selection ----------

// activeEmitter returns the selected emitter, or nil.
func activeEmitter() *world.Emitter {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveEmitterIdx < 0 || Editor.ActiveEmitterIdx >= len(lvl.Emitters) {
		return nil
	}
	return &lvl.Emitters[Editor.ActiveEmitterIdx]
}

// editorSelectEmitter makes the emitter at idx the selection.
func editorSelectEmitter(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Emitters) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveEmitterIdx = idx
	Editor.GizmoKind = gizmoKindEmitter
	Editor.PanelFocus = panelFocusEmitter
}

// editorClearEmitterSelection drops the emitter from the selection, taking the
// gizmo with it.
func editorClearEmitterSelection() {
	Editor.ActiveEmitterIdx = -1
	if Editor.GizmoKind == gizmoKindEmitter {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoEmitter returns the emitter the gizmo is attached to, or nil.
func activeGizmoEmitter() *world.Emitter {
	if Editor.GizmoKind != gizmoKindEmitter {
		return nil
	}
	return activeEmitter()
}

// ---------- placement ----------

// editorArmEmitterPlacement makes the next left-click drop an emitter.
func editorArmEmitterPlacement() {
	Editor.EmitterPlacementMode = true
	Editor.SpritePlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.EntityPlacementMode = false
	Editor.SpawnPlacementMode = false
}

// placeEmitterAt creates an emitter at pos, selects it, and leaves placement
// mode.
func placeEmitterAt(pos mgl32.Vec3) bool {
	Editor.EmitterPlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return false
	}

	EditorPushUndo()
	base := Editor.NewEmitterID
	if base == "" {
		base = "emitter"
	}
	e := world.NewEmitter(world.UniqueEmitterID(lvl, base), pos)
	e.Effect = Editor.NewEmitterEffect
	if err := e.Load(); err != nil {
		// A missing effect is a marker to fix, not a refusal: the placement is
		// real level content either way.
		editorSetStatus("Emitter %s: %v", e.ID, err)
	}
	lvl.Emitters = append(lvl.Emitters, e)
	editorSelectEmitter(len(lvl.Emitters) - 1)
	editorSetStatus("Placed emitter %q", e.ID)
	return true
}

// ---------- duplicate / delete ----------

// duplicateActiveEmitter copies the selected emitter beside itself.
func duplicateActiveEmitter() {
	lvl := Editor.level
	src := activeEmitter()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()
	cp := *src
	cp.ScriptData = world.CloneScriptData(src.ScriptData)
	cp.ID = world.UniqueEmitterID(lvl, src.ID)
	cp.Position = src.Position.Add(mgl32.Vec3{1, 0, 0})
	// The copy gets its own simulation rather than sharing the original's —
	// two emitters pointing at one pool would each reap the other's particles.
	cp.Doc = nil
	cp.Systems = nil
	if err := cp.Load(); err != nil {
		editorSetStatus("Emitter %s: %v", cp.ID, err)
	}
	lvl.Emitters = append(lvl.Emitters, cp)
	editorSelectEmitter(len(lvl.Emitters) - 1)
	editorSetStatus("Duplicated emitter %q", cp.ID)
}

// deleteActiveEmitter removes the selected emitter.
func deleteActiveEmitter() {
	lvl := Editor.level
	idx := Editor.ActiveEmitterIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Emitters) {
		return
	}
	EditorPushUndo()
	id := lvl.Emitters[idx].ID
	lvl.Emitters = append(lvl.Emitters[:idx], lvl.Emitters[idx+1:]...)
	editorClearEmitterSelection()
	editorSetStatus("Deleted emitter %q", id)
}

// reloadActiveEmitter re-reads the selected emitter's document from disk — what
// the panel's Reload button and a save from the particle editor both want.
func reloadActiveEmitter() {
	e := activeEmitter()
	if e == nil {
		return
	}
	if err := e.Load(); err != nil {
		editorSetStatus("Emitter %s: %v", e.ID, err)
		return
	}
	editorSetStatus("Reloaded %q from %s", e.ID, e.Effect)
}

// ReloadEmittersUsing re-reads every emitter in the level built on effect,
// which is what a save in the particle editor triggers so a level being
// authored beside it updates without being reloaded.
func ReloadEmittersUsing(effect string) {
	lvl := Editor.level
	if lvl == nil || effect == "" {
		return
	}
	n := 0
	for i := range lvl.Emitters {
		if lvl.Emitters[i].Effect != effect {
			continue
		}
		if err := lvl.Emitters[i].Load(); err == nil {
			n++
		}
	}
	if n > 0 {
		editorSetStatus("Reloaded %d emitter(s) using %s", n, effect)
	}
}

// ---------- picking ----------

// pickEmitterRayT returns the distance at which a picking ray crosses the
// emitter's marker disc, or +Inf.
//
// A disc facing the camera rather than a sphere: the marker is drawn as a ring
// in the viewport, and testing what is drawn is what makes it land where it
// looks like it should.
func pickEmitterRayT(origin, dir mgl32.Vec3, lvl *world.Level, e *world.Emitter, camPos mgl32.Vec3) float32 {
	c := e.WorldPosition(lvl)
	toCam := camPos.Sub(c)
	if toCam.LenSqr() < 1e-8 {
		return float32(math.MaxFloat32)
	}
	n := toCam.Normalize()
	denom := dir.Dot(n)
	if denom > -1e-6 && denom < 1e-6 {
		return float32(math.MaxFloat32)
	}
	t := c.Sub(origin).Dot(n) / denom
	if t <= 0 {
		return float32(math.MaxFloat32)
	}
	if origin.Add(dir.Mul(t)).Sub(c).LenSqr() > emitterMarkerRadius*emitterMarkerRadius {
		return float32(math.MaxFloat32)
	}
	return t
}

// ---------- menu helpers ----------

func editor3DMoveEmitterTo(pos mgl32.Vec3) {
	e := activeEmitter()
	if e == nil {
		return
	}
	EditorPushUndo()
	e.Position = pos
}

func editor3DAddEmitterAt(pos mgl32.Vec3) { placeEmitterAt(pos) }

// ---------- drawing ----------

// emitterMarkerColor is the ring every emitter is drawn with in the editor;
// emitterSelColor is the selected one's, matching the yellow every other
// selection mark uses.
var (
	emitterMarkerColor = mgl32.Vec3{0.4, 0.8, 1}
	emitterSelColor    = mgl32.Vec3{1, 1, 0}
)

// emitterRingSegments is how many segments approximate the marker and the
// emission-shape outlines. Enough to read as round at the size they are drawn.
const emitterRingSegments = 20

// RenderEmitterMarkers draws every emitter's marker, and the selected one's
// emission shape when the toggle is on.
//
// An authoring aid, like the volume cages and the spawn arrow: an emitter that
// has not started yet is otherwise invisible, and one that has is a cloud with
// no obvious origin to click.
func RenderEmitterMarkers(lvl *world.Level, camPos mgl32.Vec3) {
	if lvl == nil {
		return
	}
	for i := range lvl.Emitters {
		e := &lvl.Emitters[i]
		col := emitterMarkerColor
		if i == Editor.ActiveEmitterIdx {
			col = emitterSelColor
		}
		if !e.Enabled {
			// Dimmed rather than hidden: a switched-off emitter is still there
			// and still has to be found.
			col = col.Mul(0.45)
		}
		drawEmitterMarker(e.WorldPosition(lvl), camPos, col)
		if i == Editor.ActiveEmitterIdx && Editor.ShowEmitterShapes {
			drawEmitterShapes(lvl, e)
		}
	}
}

// drawEmitterMarker draws a camera-facing ring with a cross through it, so an
// emitter reads as a point rather than as a sphere that might be its size.
func drawEmitterMarker(c, camPos, col mgl32.Vec3) {
	toCam := camPos.Sub(c)
	if toCam.LenSqr() < 1e-8 {
		return
	}
	n := toCam.Normalize()
	ref := mgl32.Vec3{0, 1, 0}
	if absF32(n.Y()) > 0.999 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	right := ref.Cross(n).Normalize().Mul(emitterMarkerRadius)
	up := n.Cross(right.Normalize()).Mul(emitterMarkerRadius)

	prev := c.Add(right)
	for i := 1; i <= emitterRingSegments; i++ {
		a := float64(i) / emitterRingSegments * 2 * math.Pi
		p := c.Add(right.Mul(float32(math.Cos(a)))).Add(up.Mul(float32(math.Sin(a))))
		gizmo.GizmoLine(prev, p, col)
		prev = p
	}
	gizmo.GizmoLine(c.Sub(right), c.Add(right), col)
	gizmo.GizmoLine(c.Sub(up), c.Add(up), col)
}

// drawEmitterShapes outlines the region each layer's particles are born in,
// under the emitter's own transform — the only way to aim a cone by eye.
func drawEmitterShapes(lvl *world.Level, e *world.Emitter) {
	if e.Doc == nil {
		return
	}
	m := e.OriginMatrix(lvl)
	at := func(p mgl32.Vec3) mgl32.Vec3 {
		q := m.Mul4x1(p.Vec4(1))
		return mgl32.Vec3{q.X(), q.Y(), q.Z()}
	}
	for i := range e.Doc.Layers {
		l := &e.Doc.Layers[i]
		if l.Muted {
			continue
		}
		switch l.Shape {
		case particle.ShapeSphere, particle.ShapeHemisphere:
			ring(at, l.Radius, 0, emitterShapeColor, true)
		case particle.ShapeDisc, particle.ShapeCone:
			ring(at, l.Radius, 0, emitterShapeColor, false)
			if l.Shape == particle.ShapeCone {
				drawConeOutline(at, l.Radius, l.Angle)
			}
		case particle.ShapeBox:
			drawBoxOutline(at, l.BoxHalf)
		}
	}
}

var emitterShapeColor = mgl32.Vec3{0.3, 1, 0.5}

// ring draws a circle in the XZ plane at height y, optionally with the two
// vertical rings that make a sphere read as one.
func ring(at func(mgl32.Vec3) mgl32.Vec3, r, y float32, col mgl32.Vec3, sphere bool) {
	if r <= 0 {
		return
	}
	circle := func(ax, az, by, bz int) {
		var prev mgl32.Vec3
		for i := 0; i <= emitterRingSegments; i++ {
			a := float64(i) / emitterRingSegments * 2 * math.Pi
			c, s := float32(math.Cos(a))*r, float32(math.Sin(a))*r
			var p mgl32.Vec3
			p[ax], p[az] = c, s
			if by >= 0 {
				p = mgl32.Vec3{}
				p[by], p[bz] = c, s
			}
			p[1] += y
			w := at(p)
			if i > 0 {
				gizmo.GizmoLine(prev, w, col)
			}
			prev = w
		}
	}
	circle(0, 2, -1, -1) // XZ
	if sphere {
		circle(0, 0, 0, 1) // XY
		circle(0, 0, 1, 2) // YZ
	}
}

// drawConeOutline draws four rays up the cone's slant, which is what makes its
// half-angle legible.
func drawConeOutline(at func(mgl32.Vec3) mgl32.Vec3, r, angleDeg float32) {
	const length = 1.0
	half := float64(angleDeg) * math.Pi / 180
	tan := float32(math.Tan(half))
	for i := 0; i < 4; i++ {
		a := float64(i) / 4 * 2 * math.Pi
		c, s := float32(math.Cos(a)), float32(math.Sin(a))
		base := mgl32.Vec3{c * r, 0, s * r}
		tip := mgl32.Vec3{c * (r + tan*length), length, s * (r + tan*length)}
		gizmo.GizmoLine(at(base), at(tip), emitterShapeColor)
	}
}

// drawBoxOutline draws the twelve edges of the emission box.
func drawBoxOutline(at func(mgl32.Vec3) mgl32.Vec3, half mgl32.Vec3) {
	corner := func(i int) mgl32.Vec3 {
		return at(mgl32.Vec3{
			world.CornerSign(i, 0) * half.X(),
			world.CornerSign(i, 1) * half.Y(),
			world.CornerSign(i, 2) * half.Z(),
		})
	}
	for i := 0; i < 8; i++ {
		for bit := 0; bit < 3; bit++ {
			if j := i | (1 << bit); j != i {
				gizmo.GizmoLine(corner(i), corner(j), emitterShapeColor)
			}
		}
	}
}

func absF32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
