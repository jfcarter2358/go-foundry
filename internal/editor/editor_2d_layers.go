package editor

// editor_2d_layers.go — the 2D map's view of a chunk's authoring layers.
//
// A chunk can hold sectors that overlap in XZ (world.Chunk.Layers, layer.go),
// which is what an overhang or a mezzanine is. Seen from directly above, those
// sectors are drawn on top of each other: their outlines coincide, their corner
// crosses land on the same pixels, and every click resolves to whichever the
// candidate list happened to sort first. The map would be unusable long before
// the geometry was wrong.
//
// So the map shows one layer at a time. The active layer draws and clicks
// exactly as the whole chunk used to; the others are drawn as dim context so
// the stack stays readable, and are not hit-tested at all. That last part is
// the important one — it is what stops a drag meant for the ledge's corner from
// grabbing the ground corner sitting underneath it.
//
// Points are the exception, and deliberately. Chunk.Points is one shared pool
// with no layer of its own, so a point used by sectors on two layers is one
// point: moving it from either layer moves both. That is not a bug to design
// around, it is how a cliff edge should behave — the overhang's outer corner
// and the cliff face below it are the same corner. Filtering is done on the
// SECTORS a point is reached through, so the pool stays shared while the
// picking stays unambiguous.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// How much of the chunk the map draws besides the layer being edited.
const (
	layerViewStack   = iota // every layer, the inactive ones ghosted
	layerViewBelow          // the active layer plus the ones under it
	layerViewIsolate        // the active layer alone
)

// activeLayer is the layer the map is currently editing, clamped to the active
// chunk. Clamped on read rather than kept in step on write because the chunk
// under the editor changes constantly — undo swaps the level wholesale, and the
// active chunk can be switched to one with fewer layers.
func activeLayer() int {
	ch := activeChunk()
	if ch == nil {
		return 0
	}
	return ch.ClampLayer(Editor.ActiveLayer)
}

// editorSetActiveLayer moves the map to layer i, dropping any selection that
// belonged to the layer being left.
//
// Dropping it is what keeps the gizmo honest: a selection is drawn and dragged
// wherever it is, so a corner selected on the ledge would still be draggable
// from a map that is no longer showing the ledge — with no visible handle to
// say what was moving.
func editorSetActiveLayer(i int) {
	ch := activeChunk()
	if ch == nil {
		return
	}
	i = ch.ClampLayer(i)
	if i == Editor.ActiveLayer {
		return
	}
	Editor.ActiveLayer = i
	editor2DClearSelection()
	// ActiveSectorIdx is not part of the selection editor2DClearSelection drops,
	// but it indexes the layer just left — leaving it would point the panel at a
	// sector the map is no longer showing.
	Editor.ActiveSectorIdx = -1
	Editor.DragVertexSI = -1
	Editor.DragVertexVI = -1
	// A polygon half-drawn on the old layer would finish on the new one.
	Editor.PendingEdges = nil
	Editor.BiomeDrawing = false
	Editor.BiomeDrawPoints = nil
}

// editorStepLayer moves up or down the stack by delta, stopping at the ends
// rather than wrapping — wrapping from the top layer to the ground one reads as
// a glitch when the two are far apart vertically.
func editorStepLayer(delta int) {
	ch := activeChunk()
	if ch == nil {
		return
	}
	editorSetActiveLayer(activeLayer() + delta)
}

// editorAddLayer appends a layer above the top of the stack and moves to it, so
// the next thing drawn lands on it.
func editorAddLayer() {
	ch := activeChunk()
	if ch == nil {
		return
	}
	EditorPushUndo()
	editorSetActiveLayer(ch.AddLayer())
	editorSetStatus("added layer %q", ch.LayerAt(activeLayer()).Name)
}

// editorRemoveLayer deletes the active layer, merging whatever was on it onto
// the layer below. Geometry is never deleted — see Chunk.RemoveLayer.
func editorRemoveLayer() {
	ch := activeChunk()
	if ch == nil || ch.LayerCount() <= 1 {
		return
	}
	EditorPushUndo()
	i := activeLayer()
	name := ch.LayerAt(i).Name
	ch.RemoveLayer(i)
	Editor.ActiveLayer = ch.ClampLayer(i - 1)
	editor2DClearSelection()
	editorSetStatus("merged layer %q down", name)
}

// layerEditable reports whether layer l is the one being edited — the only one
// the map hit-tests, and the one a newly drawn sector joins.
func layerEditable(l int) bool { return l == activeLayer() }

// layerDrawn reports whether layer l is drawn at all, under the current view
// mode. An inactive layer that is drawn is drawn ghosted (see layerTint).
func layerDrawn(l int) bool {
	if l == activeLayer() {
		return true
	}
	switch Editor.LayerView {
	case layerViewIsolate:
		return false
	case layerViewBelow:
		return l < activeLayer()
	default:
		return true
	}
}

// Ghost tints. A layer below the active one is dimmed warm and one above it
// dimmed cool, so which side of the stack a ghosted outline belongs to is
// legible without reading a label — the same warm-near/cool-far cue the eye
// already uses for depth.
var (
	layerGhostBelow = mgl32.Vec3{0.45, 0.34, 0.26}
	layerGhostAbove = mgl32.Vec3{0.26, 0.32, 0.48}
)

// layerTint returns the colour a line belonging to layer l should be drawn in,
// given the colour it would have on the active layer.
//
// A ghosted line takes its layer's flat tint rather than a dimmed version of
// its own colour: the point of a ghost is to read as context, and preserving
// the wall/portal/selected distinctions inside it only competes with the layer
// actually being edited.
func layerTint(l int, active mgl32.Vec3) mgl32.Vec3 {
	if l == activeLayer() {
		return active
	}
	if l < activeLayer() {
		return layerGhostBelow
	}
	return layerGhostAbove
}

// layerOfSectorAt returns the layer of the topmost drawn sector whose polygon
// contains chunk-local (wx,wz) and which is NOT on the active layer, or -1.
//
// This is what makes a ghosted layer clickable: the map hit-tests only the
// active layer, so without it the only way to reach the ledge you can plainly
// see is to know which layer index it is on. Searching from the top of the
// stack down matches what the ghosts look like from above.
func layerOfSectorAt(ch *world.Chunk, wx, wz float32) int {
	if ch == nil {
		return -1
	}
	best := -1
	xz := mgl32.Vec2{wx, wz}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if s.Layer == activeLayer() || !layerDrawn(s.Layer) {
			continue
		}
		if s.Layer > best && world.PointInSector(ch, s, xz) {
			best = s.Layer
		}
	}
	return best
}

// newSectorLayerDefaults returns the layer index and the floor/ceiling heights a
// sector drawn on the map right now should start with.
//
// The heights come from the layer's band rather than from the chunk's first
// sector, which is what createSectorFromPolygon used to copy unconditionally.
// Copying sector 0 is right on a single-layer chunk and exactly wrong on a
// stacked one: a sector drawn on the ledge would arrive at the ground floor's
// height, buried inside the geometry below it.
func newSectorLayerDefaults(ch *world.Chunk) (layer int, floorY, ceilY float32) {
	if ch == nil {
		return 0, 0, world.DefaultLayerHeight
	}
	l := activeLayer()
	band := ch.LayerAt(l)
	return l, band.BaseY, band.CeilY
}

// layerDrawOrder returns the chunk's sector indices in the order the map should
// paint them: every other layer first, the layer being edited last.
//
// The map draws with the depth test off (drawEditor2DWindow), so the last line
// written to a pixel is the one that shows. Painting in plain index order meant
// a ghosted layer stored after the active one covered it — and with it the
// yellow of a selected edge and the crosses on a selected corner, which is the
// one thing on the map that has to be readable. Order within each group is kept
// as it was, so nothing else about what the map looks like changes.
func layerDrawOrder(ch *world.Chunk) []int {
	if ch == nil {
		return nil
	}
	out := make([]int, 0, len(ch.Sectors))
	for si := range ch.Sectors {
		if !layerEditable(ch.Sectors[si].Layer) {
			out = append(out, si)
		}
	}
	for si := range ch.Sectors {
		if layerEditable(ch.Sectors[si].Layer) {
			out = append(out, si)
		}
	}
	return out
}
