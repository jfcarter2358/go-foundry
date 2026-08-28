package biome

// biome_anchor_drape_test.go — CarryBiomesWithPoints re-fits painted polygons
// to an edited floor. It must only do that for paint actually DRAPED on that
// floor: a chunk floating above painted ground overlaps it in XZ but is not
// what the paint sits on, and dragging one of its points used to haul that
// paint far outside its own bounds (and re-bake its height texture on every
// mouse-move, which is what made it feel like a hang).

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// squareChunk is a chunk whose single sector is the axis-aligned box
// [lo,hi]x[lo,hi] at the given floor height.
func squareChunk(id string, lo, hi, floorH float32) world.Chunk {
	return world.Chunk{
		ID: id, Scale: 1,
		Points: []mgl32.Vec2{{lo, lo}, {hi, lo}, {hi, hi}, {lo, hi}},
		Sectors: []world.Sector{{
			ID: id + "_s", PointIndices: []int{0, 1, 2, 3},
			MinBound: mgl32.Vec2{lo, lo}, MaxBound: mgl32.Vec2{hi, hi},
			Floor: world.Floor{Enabled: true, Height: floorH},
		}},
	}
}

func bindLevel(t *testing.T, lvl *world.Level) {
	t.Helper()
	prev := world.CurrentLevel
	world.CurrentLevel = func() *world.Level { return lvl }
	t.Cleanup(func() { world.CurrentLevel = prev })
}

// dragCorner moves chunk point 0 and runs the carry, returning the paint's
// points before the edit.
func dragCorner(ch *world.Chunk, to mgl32.Vec2) mgl32.Vec2 {
	was := ch.Points[0]
	ch.Points[0] = to
	CarryBiomesWithPoints(ch, []PointMove{{PointIdx: 0, From: was}})
	return was
}

// The reported bug: a platform in the air over painted ground.
func TestPaintIsNotWarpedByAChunkFloatingAboveIt(t *testing.T) {
	ground := squareChunk("ground", 0, 40, 0)
	ground.BiomeSectors = []world.BiomeSector{{
		ID:     "grass",
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
		Points: []mgl32.Vec2{{10, 10}, {30, 10}, {30, 30}, {10, 30}},
	}}
	// Overlaps the grass in XZ, 25 units above it.
	platform := squareChunk("platform", 5, 35, 25)

	lvl := &world.Level{Chunks: []world.Chunk{ground, platform}}
	bindLevel(t, lvl)

	grass := &lvl.Chunks[0].BiomeSectors[0]
	before := append([]mgl32.Vec2(nil), grass.Points...)

	dragCorner(&lvl.Chunks[1], mgl32.Vec2{-20, -20})

	for i := range before {
		if grass.Points[i] != before[i] {
			t.Fatalf("grass point %d moved from %v to %v when the platform overhead was edited",
				i, before[i], grass.Points[i])
		}
	}
	if grass.HeightDirty || grass.GroundDirty {
		t.Error("the platform's edit dirtied the grass below it — that rebuild is what causes the lag")
	}
}

// The whole point of the carry: paint on the chunk being edited still follows
// its own floor.
func TestPaintOnTheEditedChunkStillFollowsIt(t *testing.T) {
	ch := squareChunk("room", 0, 40, 0)
	ch.BiomeSectors = []world.BiomeSector{{
		ID:     "grass",
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
		Points: []mgl32.Vec2{{10, 10}, {30, 10}, {30, 30}, {10, 30}},
	}}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	bindLevel(t, lvl)

	grass := &lvl.Chunks[0].BiomeSectors[0]
	before := append([]mgl32.Vec2(nil), grass.Points...)

	dragCorner(&lvl.Chunks[0], mgl32.Vec2{-20, -20})

	moved := false
	for i := range before {
		if grass.Points[i] != before[i] {
			moved = true
		}
	}
	if !moved {
		t.Error("paint on the edited chunk did not follow its own floor — the gate is too strict")
	}
	if !grass.HeightDirty || !grass.GroundDirty {
		t.Error("paint that moved was not marked for a rebuild")
	}
}

// stackedChunk is one chunk holding two sectors that overlap in XZ at
// different heights — a ground floor with a mezzanine over part of it. Both
// sectors are built from their own points so they can be dragged independently.
func stackedChunk(groundH, mezzH float32) world.Chunk {
	return world.Chunk{
		ID: "tower", Scale: 1,
		Points: []mgl32.Vec2{
			{0, 0}, {40, 0}, {40, 40}, {0, 40}, // 0..3 ground
			{5, 5}, {35, 5}, {35, 35}, {5, 35}, // 4..7 mezzanine, overlapping it
		},
		Sectors: []world.Sector{
			{
				ID: "ground", PointIndices: []int{0, 1, 2, 3},
				MinBound: mgl32.Vec2{0, 0}, MaxBound: mgl32.Vec2{40, 40},
				Floor: world.Floor{Enabled: true, Height: groundH},
			},
			{
				ID: "mezzanine", PointIndices: []int{4, 5, 6, 7},
				MinBound: mgl32.Vec2{5, 5}, MaxBound: mgl32.Vec2{35, 35},
				Floor: world.Floor{Enabled: true, Height: mezzH},
			},
		},
	}
}

// The within-chunk half of the same question: two stacked sectors of ONE
// chunk. Paint on the ground floor must not be dragged when the mezzanine
// above it is reshaped — chunk-level gating alone could not tell these apart,
// which is why the drape resolution compares sectors.
func TestPaintIsNotWarpedByAMezzanineAboveIt(t *testing.T) {
	ch := stackedChunk(0, 20)
	ch.BiomeSectors = []world.BiomeSector{{
		ID:     "grass",
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
		Points: []mgl32.Vec2{{10, 10}, {30, 10}, {30, 30}, {10, 30}},
	}}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	bindLevel(t, lvl)

	c := &lvl.Chunks[0]
	grass := &c.BiomeSectors[0]
	before := append([]mgl32.Vec2(nil), grass.Points...)

	// Drag a mezzanine corner (point index 4).
	was := c.Points[4]
	c.Points[4] = mgl32.Vec2{-15, -15}
	CarryBiomesWithPoints(c, []PointMove{{PointIdx: 4, From: was}})

	for i := range before {
		if grass.Points[i] != before[i] {
			t.Fatalf("grass point %d moved from %v to %v when the mezzanine 20 units "+
				"above it was edited", i, before[i], grass.Points[i])
		}
	}
	if grass.HeightDirty || grass.GroundDirty {
		t.Error("the mezzanine's edit dirtied the ground-floor paint below it")
	}
}

// ...and the ground floor's own edit must still carry that paint, even though
// a mezzanine overlaps it.
func TestPaintStillFollowsTheGroundFloorUnderAMezzanine(t *testing.T) {
	ch := stackedChunk(0, 20)
	ch.BiomeSectors = []world.BiomeSector{{
		ID:     "grass",
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
		Points: []mgl32.Vec2{{10, 10}, {30, 10}, {30, 30}, {10, 30}},
	}}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	bindLevel(t, lvl)

	c := &lvl.Chunks[0]
	grass := &c.BiomeSectors[0]
	before := append([]mgl32.Vec2(nil), grass.Points...)

	// Drag a ground-floor corner (point index 0).
	was := c.Points[0]
	c.Points[0] = mgl32.Vec2{-20, -20}
	CarryBiomesWithPoints(c, []PointMove{{PointIdx: 0, From: was}})

	moved := false
	for i := range before {
		if grass.Points[i] != before[i] {
			moved = true
		}
	}
	if !moved {
		t.Error("paint on the ground floor did not follow it — the sector gate is too strict")
	}
}

// Cross-chunk draping is real and must survive: paint that spills off its own
// chunk onto a neighbour's floor follows that neighbour when it is edited.
func TestPaintSpillingOntoANeighbourStillFollowsIt(t *testing.T) {
	// The painted chunk's own floor covers only [0,10]; the paint reaches out
	// to 30, so its far corners sit over the neighbour instead.
	painted := squareChunk("painted", 0, 10, 0)
	painted.BiomeSectors = []world.BiomeSector{{
		ID:     "grass",
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
		Points: []mgl32.Vec2{{2, 2}, {28, 2}, {28, 28}, {2, 28}},
	}}
	// Same floor height — genuinely the ground the spill lands on.
	neighbour := squareChunk("neighbour", 12, 40, 0)

	lvl := &world.Level{Chunks: []world.Chunk{painted, neighbour}}
	bindLevel(t, lvl)

	grass := &lvl.Chunks[0].BiomeSectors[0]
	before := append([]mgl32.Vec2(nil), grass.Points...)

	// Drag the neighbour's near corner; the spilled grass corners sit on it.
	dragCorner(&lvl.Chunks[1], mgl32.Vec2{20, 20})

	movedSpilled := false
	for i := range before {
		if grass.Points[i] != before[i] {
			movedSpilled = true
		}
	}
	if !movedSpilled {
		t.Error("paint spilling onto a neighbouring chunk's floor did not follow it — " +
			"the drape gate must not sever cross-chunk draping")
	}
	// The corner inside the painted chunk's OWN floor must be left alone: it
	// drapes on its own chunk, which was not edited.
	if grass.Points[0] != before[0] {
		t.Errorf("the corner over its own chunk's floor moved from %v to %v; only the "+
			"spilled corners drape on the neighbour", before[0], grass.Points[0])
	}
}

// The warp gate reads the same binding the height sample does — that is the
// point of there being one resolver. Paint bound to the terrace follows the
// terrace's edit and ignores the basement's, which is the exact reverse of
// what the unbound heuristic would do.
func TestWarpGateHonoursTheRecordedSector(t *testing.T) {
	build := func() (*world.Level, *world.Chunk, *world.BiomeSector) {
		ch := stackedChunk(0, 12) // "ground" at 0, "mezzanine" at 12
		ch.BiomeSectors = []world.BiomeSector{{
			ID:          "grass",
			Brush:       world.BiomeBrush{Kind: world.BiomeGrass},
			Points:      []mgl32.Vec2{{10, 10}, {30, 10}, {30, 30}, {10, 30}},
			DrapeSector: "mezzanine", // painted on the upper surface
		}}
		lvl := &world.Level{Chunks: []world.Chunk{ch}}
		c := &lvl.Chunks[0]
		return lvl, c, &c.BiomeSectors[0]
	}

	// Editing the bound (upper) sector must carry it.
	lvl, c, grass := build()
	bindLevel(t, lvl)
	before := append([]mgl32.Vec2(nil), grass.Points...)
	was := c.Points[4] // a mezzanine corner
	c.Points[4] = mgl32.Vec2{-15, -15}
	CarryBiomesWithPoints(c, []PointMove{{PointIdx: 4, From: was}})
	moved := false
	for i := range before {
		if grass.Points[i] != before[i] {
			moved = true
		}
	}
	if !moved {
		t.Error("paint bound to the mezzanine did not follow the mezzanine's own edit")
	}

	// Editing the other (lower) sector must not.
	lvl2, c2, grass2 := build()
	world.CurrentLevel = func() *world.Level { return lvl2 }
	before2 := append([]mgl32.Vec2(nil), grass2.Points...)
	was2 := c2.Points[0] // a ground corner
	c2.Points[0] = mgl32.Vec2{-20, -20}
	CarryBiomesWithPoints(c2, []PointMove{{PointIdx: 0, From: was2}})
	for i := range before2 {
		if grass2.Points[i] != before2[i] {
			t.Fatalf("paint bound to the mezzanine was dragged by the ground floor's "+
				"edit: point %d moved from %v to %v", i, before2[i], grass2.Points[i])
		}
	}
}
