package editor

// editor_2d_layers_test.go — the map hit-tests one layer at a time.
//
// This is the whole reason Sector.Layer exists in the editor. Two sectors
// stacked at the same XZ are the same outline and the same corner crosses from
// directly overhead, so without filtering, every candidate list contains both
// and which one a click takes is decided by slice order.

import (
	"foundry/internal/leveltest"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// stackedTestChunk is two sectors sharing one 10x10 footprint on two layers:
// "ground" on layer 0 at y=0, "ledge" on layer 1 at y=6. They share the chunk's
// point pool, exactly as the 2D editor would build them.
func stackedTestChunk(t *testing.T) *world.Chunk {
	t.Helper()
	ch := &world.Chunk{
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: make([]string, 4),
		Layers: []world.ChunkLayer{
			{Name: "Ground", BaseY: 0, CeilY: 4},
			{Name: "Ledge", BaseY: 6, CeilY: 10},
		},
	}
	mk := func(id string, layer int, floorY, ceilY float32) world.Sector {
		idx := []int{0, 1, 2, 3}
		s := world.Sector{
			ID:           id,
			Layer:        layer,
			PointIndices: idx,
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        world.Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: true, Height: ceilY, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{10, 10},
		}
		for i := range idx {
			s.Walls = append(s.Walls, world.Wall{
				StartPoint: ch.Points[idx[i]],
				EndPoint:   ch.Points[idx[(i+1)%len(idx)]],
				Texture:    "concrete", Collision: true, Visible: true, Alpha: 1,
			})
		}
		return s
	}
	ch.Sectors = []world.Sector{mk("ground", 0, 0, 4), mk("ledge", 1, 6, 10)}

	lvl := &world.Level{Chunks: []world.Chunk{*ch}}
	Editor.level = lvl
	Editor.Mode = Mode2D
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeKind = ""
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveSurface = ""
	Editor.EditFloorVerts = true
	Editor.SelectedVertices = nil
	Editor.SpawnSelected = false
	Editor.DragVertexSI, Editor.DragVertexVI = -1, -1
	Editor.ActiveLayer = 0
	Editor.LayerView = layerViewStack
	return &lvl.Chunks[0]
}

func TestMapPicksOnlyTheActiveLayersCorners(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)

	// The two sectors share every corner, so without filtering a click near one
	// would offer both sectors' handles for it.
	for _, layer := range []int{0, 1} {
		Editor.ActiveLayer = layer
		cands := editor2DVertexCandidates(ch, 0.2, 0.2, 5)
		if len(cands) == 0 {
			t.Fatalf("layer %d: no corner candidates at all", layer)
		}
		for _, c := range cands {
			if c.isFreePoint() {
				continue
			}
			if got := ch.Sectors[c.sectorIdx].Layer; got != layer {
				t.Errorf("editing layer %d offered a corner of %q on layer %d",
					layer, ch.Sectors[c.sectorIdx].ID, got)
			}
		}
	}
}

func TestMapPicksOnlyTheActiveLayersEdges(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)

	for _, layer := range []int{0, 1} {
		Editor.ActiveLayer = layer
		cands := editor2DEdgeCandidates(ch, 5, 0.2, 5)
		if len(cands) == 0 {
			t.Fatalf("layer %d: no edge candidates at all", layer)
		}
		for _, c := range cands {
			if got := ch.Sectors[c.sectorIdx].Layer; got != layer {
				t.Errorf("editing layer %d offered an edge of %q on layer %d",
					layer, ch.Sectors[c.sectorIdx].ID, got)
			}
		}
	}
}

func TestFaceClickResolvesToTheActiveLayersSector(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)

	for _, tc := range []struct {
		layer int
		want  string
	}{{0, "ground"}, {1, "ledge"}} {
		Editor.ActiveLayer = tc.layer
		// Well inside the shared footprint and away from every edge, so this is
		// the sector-interior rung of the ladder rather than an edge hit.
		f, ok := editor2DFaceAt(ch, 5, 5, 0.1)
		if !ok {
			t.Fatalf("layer %d: clicking the interior selected nothing", tc.layer)
		}
		if got := ch.Sectors[f.sectorIdx].ID; got != tc.want {
			t.Errorf("editing layer %d selected %q, want %q", tc.layer, got, tc.want)
		}
	}
}

// Free points belong to no sector and so to no layer — they are the raw
// material of a new one, and a Join has to be able to reach them whichever layer
// is active. But a point held by a HIDDEN layer's sector is not free, and
// offering it as such would let a Join quietly reuse the ledge's corner.
func TestFreePointsIgnoreLayersButHeldPointsDoNot(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)
	free := ch.AddPoint(mgl32.Vec2{50, 50})

	for _, layer := range []int{0, 1} {
		Editor.ActiveLayer = layer
		cands := editor2DVertexCandidates(ch, 50, 50, 5)
		if len(cands) != 1 || !cands[0].isFreePoint() || cands[0].pointIdx != free {
			t.Errorf("layer %d: the free point was not offered (%d candidates)", layer, len(cands))
		}

		// Point 0 is held by sectors on both layers, so it is never free.
		for _, c := range editor2DVertexCandidates(ch, 0, 0, 5) {
			if c.isFreePoint() {
				t.Errorf("layer %d: a point held by a sector was offered as free", layer)
			}
		}
	}
}

func TestLayerViewModesDecideWhatIsDrawn(t *testing.T) {
	defer saveEditorSelection()()
	stackedTestChunk(t)
	Editor.ActiveLayer = 1

	Editor.LayerView = layerViewStack
	if !layerDrawn(0) || !layerDrawn(1) {
		t.Error("stack view hid a layer")
	}
	Editor.LayerView = layerViewBelow
	if !layerDrawn(0) || !layerDrawn(1) {
		t.Error("below view hid the active layer or the one under it")
	}
	Editor.LayerView = layerViewIsolate
	if layerDrawn(0) {
		t.Error("isolate view drew a layer other than the active one")
	}
	if !layerDrawn(1) {
		t.Error("isolate view hid the active layer")
	}

	// Only the active layer is ever editable, whatever is drawn.
	for _, v := range []int{layerViewStack, layerViewBelow, layerViewIsolate} {
		Editor.LayerView = v
		if layerEditable(0) || !layerEditable(1) {
			t.Errorf("view %d: editability follows the view instead of the active layer", v)
		}
	}
}

// Switching layers must not leave a selection behind on the layer being left:
// it would still be drawn and dragged by the gizmo, with no visible handle on
// the map to say what was moving.
func TestSwitchingLayersDropsTheSelection(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)

	if !editor2DHandlePointClick(ch, 0, 0, 5, false, false) {
		t.Fatal("could not select a corner on the ground layer")
	}
	if len(Editor.SelectedVertices) == 0 {
		t.Fatal("the click selected nothing")
	}
	editorSetActiveLayer(1)
	if len(Editor.SelectedVertices) != 0 {
		t.Errorf("%d handles still selected after moving to another layer",
			len(Editor.SelectedVertices))
	}
	if activeLayer() != 1 {
		t.Errorf("active layer = %d, want 1", activeLayer())
	}
}

// The active layer is clamped on read, so a chunk swapped underneath the editor
// — by undo, or by switching chunks — can never leave the map pointed at a
// layer that no longer exists.
func TestActiveLayerIsClampedToTheChunk(t *testing.T) {
	defer saveEditorSelection()()
	stackedTestChunk(t)

	Editor.ActiveLayer = 7
	if got := activeLayer(); got != 1 {
		t.Errorf("activeLayer = %d, want it clamped to the chunk's top layer 1", got)
	}
	Editor.ActiveLayer = -3
	if got := activeLayer(); got != 0 {
		t.Errorf("activeLayer = %d, want it clamped to 0", got)
	}
}

// A sector drawn on the ledge must start at the ledge's height. Copying the
// chunk's first sector — what the map did before layers — would bury it in the
// ground floor.
func TestNewSectorTakesItsLayersHeights(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)

	Editor.ActiveLayer = 1
	layer, floorY, ceilY := newSectorLayerDefaults(ch)
	if layer != 1 {
		t.Errorf("new sector layer = %d, want the active layer 1", layer)
	}
	if floorY != 6 || ceilY != 10 {
		t.Errorf("new sector heights = %v..%v, want the ledge band's 6..10", floorY, ceilY)
	}

	Editor.ActiveLayer = 0
	if _, floorY, ceilY = newSectorLayerDefaults(ch); floorY != 0 || ceilY != 4 {
		t.Errorf("ground heights = %v..%v, want 0..4", floorY, ceilY)
	}
}

// Clicking a sector you can see on a ghosted layer moves the map to it, rather
// than doing nothing — but only after everything on the active layer has missed.
func TestClickingAGhostedSectorSwitchesLayer(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 0

	if got := layerOfSectorAt(ch, 5, 5); got != 1 {
		t.Errorf("layerOfSectorAt over the ledge = %d, want 1", got)
	}
	// Isolate hides the ledge, so there is nothing there to click through to.
	Editor.LayerView = layerViewIsolate
	if got := layerOfSectorAt(ch, 5, 5); got != -1 {
		t.Errorf("a hidden layer was reachable by click-through: %d", got)
	}
}

// A single-layer chunk must behave exactly as it did before layers existed —
// this is the shape of nearly every chunk, and every one authored earlier.
func TestASingleLayerChunkIsUnaffected(t *testing.T) {
	defer saveEditorSelection()()
	lvl := &world.Level{Chunks: []world.Chunk{*leveltest.SplitChunk()}}
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	Editor.ActiveLayer = 0
	Editor.LayerView = layerViewStack
	Editor.EditFloorVerts = true
	Editor.SelectedVertices = nil
	ch := &lvl.Chunks[0]

	if ch.LayerCount() != 1 {
		t.Fatalf("LayerCount = %d, want 1", ch.LayerCount())
	}
	// Every corner within range is still offered, from both sectors.
	if got := editor2DVertexCandidates(ch, 9.5, 0, 20); len(got) < 3 {
		t.Errorf("only %d corner candidates on an unlayered chunk", len(got))
	}
	if got := editor2DEdgeCandidates(ch, 10, 5, 20); len(got) == 0 {
		t.Error("no edge candidates on an unlayered chunk")
	}
}

// The map paints with the depth test off, so the last line written to a pixel
// is the one that shows. The layer being edited has to go last, or a ghosted
// layer stored after it in the sector slice covers the selection handles that
// are the whole point of the map.
func TestTheEditedLayerIsPaintedLast(t *testing.T) {
	defer saveEditorSelection()()
	ch := stackedTestChunk(t)

	for _, layer := range []int{0, 1} {
		Editor.ActiveLayer = layer
		order := layerDrawOrder(ch)
		if len(order) != len(ch.Sectors) {
			t.Fatalf("layer %d: draw order has %d entries, want %d — a sector would go undrawn",
				layer, len(order), len(ch.Sectors))
		}
		seen := map[int]bool{}
		lastGhost := -1
		firstActive := len(order)
		for pos, si := range order {
			if seen[si] {
				t.Fatalf("layer %d: sector %d appears twice in the draw order", layer, si)
			}
			seen[si] = true
			if ch.Sectors[si].Layer == layer {
				if pos < firstActive {
					firstActive = pos
				}
				continue
			}
			lastGhost = pos
		}
		if firstActive == len(order) {
			t.Fatalf("layer %d: the layer being edited is never drawn", layer)
		}
		if lastGhost > firstActive {
			t.Errorf("layer %d: a ghosted sector is painted at %d, after the edited layer at %d",
				layer, lastGhost, firstActive)
		}
	}
}
