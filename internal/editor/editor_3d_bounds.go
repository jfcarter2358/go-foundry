package editor

// editor_3d_bounds.go — the bounds view: the world-space bounding box the
// engine actually culls and collides against, drawn round every placed object
// that has one.
//
// It is a switch of its own beside shading and the camera rather than a third
// shading mode, because the question it answers — "what does the engine think
// is where" — is one you ask *of* the view you are already in. Solid, to see a
// box standing proud of the mesh it belongs to; wireframe, to see one buried
// inside geometry it was supposed to clear.
//
// Every box here is axis-aligned, because every box the engine keeps is: a
// rotated object's bounds are re-fitted to the world axes, or widened to the
// extents' length for the ones whose WorldAABB has to stay valid at any
// orientation (Entity.WorldAABB and the three that copy it). Drawing an
// oriented box would show a volume nothing ever tests against. The cyan box on
// the active chunk, drawn beside these by Editor3DRenderGizmos, is the
// deliberate exception — that one is a *placement*, not a bound.

import (
	"fmt"
	"math"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorBoundsKind is what a box belongs to. It only decides a colour and a
// legend row — the view draws every kind or none — but naming them is what
// lets the legend say which colour is which without a second list to keep in
// step.
type editorBoundsKind int

const (
	boundsKindChunk editorBoundsKind = iota
	boundsKindBrush
	boundsKindEntity
	boundsKindScenery
	boundsKindVolume
	boundsKindSprite
	boundsKindEmitter
	boundsKindCount
)

// editorBoundsStyles names each kind and gives it its colour.
//
// The colours deliberately avoid yellow: yellow is "selected" everywhere else
// in this viewport, and a bounds view that painted a whole kind in it would
// read as a level with everything selected at once.
var editorBoundsStyles = [boundsKindCount]struct {
	Name  string
	Color mgl32.Vec3
}{
	boundsKindChunk:   {"Chunks", mgl32.Vec3{0.35, 0.55, 0.9}},
	boundsKindBrush:   {"Brushes (active chunk)", mgl32.Vec3{0.6, 0.6, 0.68}},
	boundsKindEntity:  {"Entities", mgl32.Vec3{1, 0.5, 0.15}},
	boundsKindScenery: {"Scenery", mgl32.Vec3{0.35, 0.85, 0.45}},
	boundsKindVolume:  {"Volumes", mgl32.Vec3{0.75, 0.4, 1}},
	boundsKindSprite:  {"Sprites", mgl32.Vec3{1, 0.45, 0.7}},
	boundsKindEmitter: {"Emitters", mgl32.Vec3{0.4, 0.8, 1}},
}

// ---------- the walk ----------

// editorBoundsWalk calls fn once per drawable box in lvl.
//
// Brushes are the one kind not taken level-wide: they are counted in thousands
// where every other kind is counted in dozens, and a box round every brush in
// a level is a solid mass of lines with nothing readable in it. The active
// chunk's are the ones you are working in, so those are the ones that draw.
//
// An object whose WorldAABB reports ok=false is skipped rather than drawn at
// its own position with no size. That is the honest reading: an entity with no
// extents authored, a scenery whose model has not loaded and a sprite with no
// size are unbounded, not zero-sized. So is an emitter with nothing alive yet,
// whose bounds collapse to the point it sits on. The legend counts the gap, so
// the absence is visible rather than mysterious.
func editorBoundsWalk(lvl *world.Level, fn func(kind editorBoundsKind, lo, hi mgl32.Vec3)) {
	if lvl == nil {
		return
	}
	emit := func(kind editorBoundsKind, lo, hi mgl32.Vec3, ok bool) {
		if ok && editorBoundsUsable(lo, hi) {
			fn(kind, lo, hi)
		}
	}

	for i := range lvl.Chunks {
		lo, hi := lvl.Chunks[i].WorldBounds()
		emit(boundsKindChunk, lo, hi, true)
	}
	if ch := editorBoundsActiveChunk(lvl); ch != nil {
		for i := range ch.Brushes {
			lo, hi := ch.Brushes[i].WorldAABB(ch)
			emit(boundsKindBrush, lo, hi, true)
		}
	}
	for i := range lvl.Entities {
		lo, hi, ok := lvl.Entities[i].WorldAABB()
		emit(boundsKindEntity, lo, hi, ok)
	}
	for i := range lvl.Scenery {
		lo, hi, ok := lvl.Scenery[i].WorldAABB()
		emit(boundsKindScenery, lo, hi, ok)
	}
	for i := range lvl.Volumes {
		lo, hi, ok := lvl.Volumes[i].WorldAABB(lvl)
		emit(boundsKindVolume, lo, hi, ok)
	}
	for i := range lvl.Sprites {
		lo, hi, ok := lvl.Sprites[i].WorldAABB(lvl)
		emit(boundsKindSprite, lo, hi, ok)
	}
	for i := range lvl.Emitters {
		lo, hi, ok := lvl.Emitters[i].WorldAABB(lvl)
		emit(boundsKindEmitter, lo, hi, ok)
	}
}

// editorBoundsActiveChunk is the chunk whose brushes get boxes, or nil.
func editorBoundsActiveChunk(lvl *world.Level) *world.Chunk {
	if Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return nil
	}
	return &lvl.Chunks[Editor.ActiveChunkIdx]
}

// editorBoundsUsable rejects a box that is not a box.
//
// Three of those turn up in a real level. A chunk with points but no sectors
// never gets a vertical extent and still holds the inverted sentinel range the
// load started with; carried through a moved chunk's matrix that sentinel
// becomes infinities, which draw as twelve lines to the horizon; and a brush
// with no vertices at all reports a box at the origin with no size. A flat box
// is kept — a zero-thickness brush is a real thing to want to see the outline
// of — but a wholly degenerate one is not.
func editorBoundsUsable(lo, hi mgl32.Vec3) bool {
	if lo == hi {
		return false
	}
	for i := 0; i < 3; i++ {
		// Written as a negated >=, so a NaN on either end fails it too.
		if !(hi[i] >= lo[i]) {
			return false
		}
		if math.IsInf(float64(lo[i]), 0) || math.IsInf(float64(hi[i]), 0) {
			return false
		}
	}
	return true
}

// ---------- drawing ----------

// drawEditorBounds queues the view's boxes. Called from Editor3DRenderGizmos
// inside the depth-tested pass, so a box is hidden by the world in front of it
// the way the spawn marker's and the active chunk's already are — the overlay
// sits in the scene rather than floating over it.
func drawEditorBounds() {
	if !Editor.ShowBounds {
		return
	}
	editorBoundsWalk(Editor.level, func(kind editorBoundsKind, lo, hi mgl32.Vec3) {
		gizmo.GizmoAABB(lo, hi, editorBoundsStyles[kind].Color)
	})
}

// ---------- the switch ----------

// editorSetShowBounds turns the view on or off. Unlike shading it leaves the
// visibility cache alone: bounds are drawn over the world, not instead of it,
// so what a click can reach does not change.
func editorSetShowBounds(on bool) { Editor.ShowBounds = on }

// editorBoundsLabel names the toggle. Unlike shading, whose two states are both
// a way of drawing the world and so both deserve a name, this one is a single
// overlay that is either up or not — the button's lit background says which.
func editorBoundsLabel() string { return "Bounds [B]" }

// editorBoundsTooltip is the same text on the toolbar button, the panel button
// and the context menu row.
const editorBoundsTooltip = "B — Draw the world bounding box the engine culls and\n" +
	"collides against, round every object that has one.\n" +
	"Brushes are the active chunk's only.\n" +
	"The View panel's legend says which colour is which."

// editorBoundsToggleButton draws the switch, for the toolbar and the panel to
// share rather than each keeping its own copy of the label and the tooltip.
func editorBoundsToggleButton() {
	on := Editor.ShowBounds
	if editorActiveToggleButtonColor(editorBoundsLabel(), on,
		imgui.Vec4{X: 0.5, Y: 0.3, Z: 0.6, W: 1}) {
		editorSetShowBounds(!on)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip(editorBoundsTooltip)
	}
}

// ---------- the legend ----------

// editorBoundsTally is how many objects of each kind the level holds, and how
// many of those the view could draw a box for.
type editorBoundsTally struct {
	Drawn [boundsKindCount]int
	Total [boundsKindCount]int
}

// editorBoundsCount tallies the level for the legend, off the same walk that
// draws it — so the legend cannot claim a box the view did not put up.
func editorBoundsCount(lvl *world.Level) editorBoundsTally {
	var t editorBoundsTally
	if lvl == nil {
		return t
	}
	t.Total[boundsKindChunk] = len(lvl.Chunks)
	if ch := editorBoundsActiveChunk(lvl); ch != nil {
		t.Total[boundsKindBrush] = len(ch.Brushes)
	}
	t.Total[boundsKindEntity] = len(lvl.Entities)
	t.Total[boundsKindScenery] = len(lvl.Scenery)
	t.Total[boundsKindVolume] = len(lvl.Volumes)
	t.Total[boundsKindSprite] = len(lvl.Sprites)
	t.Total[boundsKindEmitter] = len(lvl.Emitters)
	editorBoundsWalk(lvl, func(kind editorBoundsKind, _, _ mgl32.Vec3) {
		t.Drawn[kind]++
	})
	return t
}

// editorBoundsLegendRow is one kind's line: its name in its own colour, how
// many boxes it drew, and — when the two differ — how many objects it had to
// choose from. The gap is the useful number: "no box" is the only way the view
// can report an object with nothing authored, and without the total beside it
// that reads as the view being broken rather than the level being incomplete.
func editorBoundsLegendRow(kind editorBoundsKind, t editorBoundsTally) string {
	st := editorBoundsStyles[kind]
	if t.Drawn[kind] == t.Total[kind] {
		return fmt.Sprintf("%s — %d", st.Name, t.Drawn[kind])
	}
	return fmt.Sprintf("%s — %d of %d (the rest have no box to draw)",
		st.Name, t.Drawn[kind], t.Total[kind])
}

// editorBoundsLegend draws the colour key under the View controls, and only
// while the view is on: a key to colours nothing is showing is noise.
func editorBoundsLegend() {
	if !Editor.ShowBounds {
		return
	}
	t := editorBoundsCount(Editor.level)
	for kind := editorBoundsKind(0); kind < boundsKindCount; kind++ {
		c := editorBoundsStyles[kind].Color
		editorTextColored(
			imgui.Vec4{X: c.X(), Y: c.Y(), Z: c.Z(), W: 1},
			editorBoundsLegendRow(kind, t))
	}
}
