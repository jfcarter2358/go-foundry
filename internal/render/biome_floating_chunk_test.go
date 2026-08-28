package render

// biome_floating_chunk_test.go — biome paint drapes over the ground it was
// painted on, not over whatever chunk happens to hang in the air above it.
//
// The regression: a floating platform overlapping a lake in XZ used to win the
// floor sample outright, because FloorHeightNormalUnderLevel took the first
// chunk in the level whose sector contained the point. That lifted the lakebed
// ground patch up to the platform's floor height, so the bed poked through the
// water surface and the water read as "removed" everywhere the platform
// covered it.

import (
	"testing"

	"foundry/internal/biome"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	floatingPlatformY = 25
	lakeGroundY       = 0
)

// lakeUnderPlatformLevel builds a ground chunk carrying a Water-kind biome
// sector, plus a platform chunk hanging directly over the middle of it.
// platformFirst pins the chunk order, which is what used to decide the result.
func lakeUnderPlatformLevel(platformFirst bool) (*world.Level, *world.Chunk, *world.BiomeSector) {
	ground := world.Chunk{
		ID: "ground", Scale: 1,
		Points: []mgl32.Vec2{{-20, -20}, {20, -20}, {20, 20}, {-20, 20}},
		Sectors: []world.Sector{{
			ID:           "groundFloor",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: lakeGroundY},
			Ceiling:      world.Ceiling{Height: 30},
		}},
		BiomeSectors: []world.BiomeSector{{
			ID:     "lake",
			Brush:  world.BiomeBrush{Kind: world.BiomeWater},
			Points: []mgl32.Vec2{{-10, -10}, {10, -10}, {10, 10}, {-10, 10}},
		}},
	}
	platform := world.Chunk{
		ID: "platform", Scale: 1,
		Points: []mgl32.Vec2{{-5, -5}, {5, -5}, {5, 5}, {-5, 5}},
		Sectors: []world.Sector{{
			ID:           "platformFloor",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: floatingPlatformY},
			Ceiling:      world.Ceiling{Height: 30},
		}},
	}

	lvl := &world.Level{}
	groundIdx := 0
	if platformFirst {
		lvl.Chunks = []world.Chunk{platform, ground}
		groundIdx = 1
	} else {
		lvl.Chunks = []world.Chunk{ground, platform}
	}
	owner := &lvl.Chunks[groundIdx]
	return lvl, owner, &owner.BiomeSectors[0]
}

// The floor a lake's paint sits on is its own chunk's, whichever order the
// chunks happen to be stored in.
func TestFloatingChunkDoesNotStealTheFloorUnderIt(t *testing.T) {
	for _, platformFirst := range []bool{true, false} {
		lvl, owner, _ := lakeUnderPlatformLevel(platformFirst)

		// (0,0) is directly under the platform.
		if got := biome.FloorHeightUnderLevel(lvl, owner, "", 0, 0); got != lakeGroundY {
			t.Errorf("platformFirst=%v: floor under the lake at (0,0) = %v, want %v — "+
				"the floating platform at %v stole the sample",
				platformFirst, got, lakeGroundY, floatingPlatformY)
		}
		// ...and a point clear of the platform must be unaffected either way.
		if got := biome.FloorHeightUnderLevel(lvl, owner, "", 8, 8); got != lakeGroundY {
			t.Errorf("platformFirst=%v: floor clear of the platform = %v, want %v",
				platformFirst, got, lakeGroundY)
		}
	}
}

// The water plane's own reference height comes from one centroid sample, so a
// platform over the middle of a lake used to move the entire surface.
func TestWaterPlaneIgnoresAFloatingChunkOverhead(t *testing.T) {
	lvl, owner, lake := lakeUnderPlatformLevel(true)

	_, floorY := biome.BiomeWaterCentroidAndFloor(lvl, owner, lake)
	if floorY != lakeGroundY {
		t.Errorf("water reference floor = %v, want %v — the whole surface would be "+
			"lifted to the platform", floorY, lakeGroundY)
	}
}

// The lakebed patch is sampled per vertex, so this is the one that actually
// produced the reported symptom: bed vertices under the platform shooting up
// through the water.
func TestLakebedStaysUnderTheWaterBeneathAFloatingChunk(t *testing.T) {
	lvl, owner, lake := lakeUnderPlatformLevel(true)
	seed := world.BiomeSeed(lake)

	// Sweep the whole lake, including the part the platform covers.
	for x := float32(-9); x <= 9; x += 1.5 {
		for z := float32(-9); z <= 9; z += 1.5 {
			h, _ := biome.BiomeSurfaceAt(lvl, owner, "", &lake.Brush, seed, x, z)
			// The bed sits at the ground plus its own bias/noise — nowhere near
			// the platform overhead.
			if h > lakeGroundY+2 {
				t.Fatalf("lakebed at (%.1f,%.1f) = %v, want near %v — the patch is "+
					"being lifted toward the platform floor at %v",
					x, z, h, lakeGroundY, floatingPlatformY)
			}
		}
	}
}

// The ground patch mesh is what is actually drawn, so pin it end to end too.
func TestGroundPatchUnderFloatingChunkStaysOnItsOwnFloor(t *testing.T) {
	lvl, owner, lake := lakeUnderPlatformLevel(true)

	verts := BuildGroundPatchMesh(lvl, owner, lake)
	if len(verts) == 0 {
		t.Fatal("expected a non-empty lakebed patch")
	}
	for i := 0; i < len(verts); i += GroundVertexStride {
		if y := verts[i+1]; y > lakeGroundY+2 {
			t.Fatalf("lakebed vertex at (%.2f,%.2f) sits at y=%v, want near %v",
				verts[i], verts[i+2], y, lakeGroundY)
		}
	}
}

// Paint really does drape across a chunk boundary — the fix must prefer the
// owning chunk without severing that.
func TestPaintStillFindsAFloorOnAnotherChunk(t *testing.T) {
	// The painted chunk has no floor of its own at all; the neighbour does.
	painted := world.Chunk{
		ID: "painted", Scale: 1,
		BiomeSectors: []world.BiomeSector{{
			ID:     "spill",
			Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
			Points: []mgl32.Vec2{{-4, -4}, {4, -4}, {4, 4}, {-4, 4}},
		}},
	}
	neighbour := world.Chunk{
		ID: "neighbour", Scale: 1,
		Points: []mgl32.Vec2{{-20, -20}, {20, -20}, {20, 20}, {-20, 20}},
		Sectors: []world.Sector{{
			ID:           "n0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: 7},
			Ceiling:      world.Ceiling{Height: 30},
		}},
	}
	lvl := &world.Level{Chunks: []world.Chunk{painted, neighbour}}

	if got := biome.FloorHeightUnderLevel(lvl, &lvl.Chunks[0], "", 0, 0); got != 7 {
		t.Errorf("paint spilling onto a neighbouring chunk's floor sampled %v, want 7 — "+
			"preferring the owning chunk must not stop the cross-chunk fallback", got)
	}
}

// The cross-chunk fallback has to convert into the candidate's own frame: two
// chunks both authored around their local origin are not in the same place.
func TestCrossChunkFallbackHonorsTheCandidatesTransform(t *testing.T) {
	painted := world.Chunk{
		ID: "painted", Scale: 1,
		BiomeSectors: []world.BiomeSector{{
			ID:     "spill",
			Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
			Points: []mgl32.Vec2{{-4, -4}, {4, -4}, {4, 4}, {-4, 4}},
		}},
	}
	// Same local layout as the painted chunk's neighbourhood, but parked 500
	// units away: it must NOT answer for the paint's own position.
	faraway := world.Chunk{
		ID: "faraway", Scale: 1, Origin: mgl32.Vec3{500, 0, 500},
		Points: []mgl32.Vec2{{-20, -20}, {20, -20}, {20, 20}, {-20, 20}},
		Sectors: []world.Sector{{
			ID:           "f0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: 7},
			Ceiling:      world.Ceiling{Height: 30},
		}},
	}
	lvl := &world.Level{Chunks: []world.Chunk{painted, faraway}}

	if got := biome.FloorHeightUnderLevel(lvl, &lvl.Chunks[0], "", 0, 0); got != 0 {
		t.Errorf("a chunk 500 units away answered the floor query with %v, want 0 "+
			"(open ground) — the candidate's own transform was not applied", got)
	}
}

// ---------- drape resolution among overlapping candidates ----------
//
// Two sectors only compete when they genuinely overlap in XZ. Where they do,
// the lower one is the ground and the upper one is a structure above it —
// resolved that way rather than by whichever the level file happens to list
// first, which is what the fallback used to do.

// stackedChunkLevel is one chunk with a ground floor and a mezzanine over part
// of it, plus paint. sectorOrder flips which is listed first.
func stackedChunkLevel(mezzanineFirst bool) (*world.Level, *world.Chunk, *world.BiomeSector) {
	ground := world.Sector{
		ID: "ground", PointIndices: []int{0, 1, 2, 3},
		MinBound: mgl32.Vec2{0, 0}, MaxBound: mgl32.Vec2{40, 40},
		Floor: world.Floor{Enabled: true, Height: 0}, Ceiling: world.Ceiling{Height: 30},
	}
	mezz := world.Sector{
		ID: "mezzanine", PointIndices: []int{4, 5, 6, 7},
		MinBound: mgl32.Vec2{5, 5}, MaxBound: mgl32.Vec2{35, 35},
		Floor: world.Floor{Enabled: true, Height: 20}, Ceiling: world.Ceiling{Height: 30},
	}
	ch := world.Chunk{
		ID: "tower", Scale: 1,
		Points: []mgl32.Vec2{
			{0, 0}, {40, 0}, {40, 40}, {0, 40},
			{5, 5}, {35, 5}, {35, 35}, {5, 35},
		},
		BiomeSectors: []world.BiomeSector{{
			ID:     "grass",
			Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
			Points: []mgl32.Vec2{{10, 10}, {30, 10}, {30, 30}, {10, 30}},
		}},
	}
	if mezzanineFirst {
		ch.Sectors = []world.Sector{mezz, ground}
	} else {
		ch.Sectors = []world.Sector{ground, mezz}
	}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	owner := &lvl.Chunks[0]
	return lvl, owner, &owner.BiomeSectors[0]
}

// Paint under a mezzanine drapes on the ground floor, whichever order the two
// sectors are stored in.
func TestDrapeResolvesToTheLowerOfTwoStackedSectors(t *testing.T) {
	for _, mezzFirst := range []bool{true, false} {
		lvl, owner, _ := stackedChunkLevel(mezzFirst)

		if got := biome.FloorHeightUnderLevel(lvl, owner, "", 20, 20); got != 0 {
			t.Errorf("mezzanineFirst=%v: floor under the paint = %v, want 0 (the ground "+
				"floor, not the mezzanine 20 above it)", mezzFirst, got)
		}
		_, s := world.DrapeTarget(lvl, owner, "", 20, 20)
		if s == nil || s.ID != "ground" {
			t.Errorf("mezzanineFirst=%v: drape sector = %v, want \"ground\"", mezzFirst, s)
		}
	}
}

// Outside the mezzanine's footprint there is only one candidate, so nothing
// changes there.
func TestDrapeIsUnaffectedWhereSectorsDoNotOverlap(t *testing.T) {
	lvl, owner, _ := stackedChunkLevel(true)
	_, s := world.DrapeTarget(lvl, owner, "", 2, 2) // inside ground, outside mezzanine
	if s == nil || s.ID != "ground" {
		t.Errorf("drape sector outside the overlap = %v, want \"ground\"", s)
	}
}

// The cross-chunk fallback resolves the same way: among chunks a spilled point
// lands on, the lowest world floor wins rather than the first listed.
func TestCrossChunkFallbackPrefersTheLowestFloor(t *testing.T) {
	painted := world.Chunk{ID: "painted", Scale: 1} // no floors of its own
	low := world.Chunk{
		ID: "low", Scale: 1,
		Points: []mgl32.Vec2{{-20, -20}, {20, -20}, {20, 20}, {-20, 20}},
		Sectors: []world.Sector{{
			ID: "lowFloor", PointIndices: []int{0, 1, 2, 3},
			MinBound: mgl32.Vec2{-20, -20}, MaxBound: mgl32.Vec2{20, 20},
			Floor: world.Floor{Enabled: true, Height: 1}, Ceiling: world.Ceiling{Height: 30},
		}},
	}
	high := low
	high.ID = "high"
	high.Sectors = []world.Sector{low.Sectors[0]}
	high.Sectors[0].ID = "highFloor"
	high.Sectors[0].Floor.Height = 18

	// The high one listed first, so first-match would pick it.
	lvl := &world.Level{Chunks: []world.Chunk{painted, high, low}}
	owner := &lvl.Chunks[0]

	if got := biome.FloorHeightUnderLevel(lvl, owner, "", 0, 0); got != 1 {
		t.Errorf("spilled paint sampled %v, want 1 (the lower of the two overlapping "+
			"chunks, not whichever is listed first)", got)
	}
}

// ---------- the recorded drape binding ----------
//
// Where two sectors are stacked, geometry alone cannot say which one the paint
// was drawn over — so it is recorded at paint time (BiomeSector.DrapeSector)
// and honoured here. "Lowest wins" is only the fallback for unbound paint.

// terraceLevel is the case the heuristic gets wrong on its own: grass on a
// raised terrace with a basement directly beneath it in the same chunk.
func terraceLevel() (*world.Level, *world.Chunk) {
	ch := world.Chunk{
		ID: "villa", Scale: 1,
		Points: []mgl32.Vec2{
			{0, 0}, {40, 0}, {40, 40}, {0, 40}, // basement
			{5, 5}, {35, 5}, {35, 35}, {5, 35}, // terrace above it
		},
		Sectors: []world.Sector{
			{
				ID: "basement", PointIndices: []int{0, 1, 2, 3},
				MinBound: mgl32.Vec2{0, 0}, MaxBound: mgl32.Vec2{40, 40},
				Floor: world.Floor{Enabled: true, Height: 0}, Ceiling: world.Ceiling{Height: 30},
			},
			{
				ID: "terrace", PointIndices: []int{4, 5, 6, 7},
				MinBound: mgl32.Vec2{5, 5}, MaxBound: mgl32.Vec2{35, 35},
				Floor: world.Floor{Enabled: true, Height: 12}, Ceiling: world.Ceiling{Height: 30},
			},
		},
	}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	return lvl, &lvl.Chunks[0]
}

// Bound to the terrace, the paint stays on the terrace — the case "lowest
// wins" would get wrong, which is why the binding is persisted at all.
func TestDrapeHonoursTheRecordedSector(t *testing.T) {
	lvl, owner := terraceLevel()

	_, s := world.DrapeTarget(lvl, owner, "terrace", 20, 20)
	if s == nil || s.ID != "terrace" {
		t.Fatalf("drape sector = %v, want \"terrace\" (the recorded one)", s)
	}
	if got := biome.FloorHeightUnderLevel(lvl, owner, "terrace", 20, 20); got != 12 {
		t.Errorf("floor under terrace-bound paint = %v, want 12", got)
	}
}

// The same geometry, bound the other way, resolves the other way.
func TestDrapeHonoursTheRecordedSectorBothWays(t *testing.T) {
	lvl, owner := terraceLevel()

	if got := biome.FloorHeightUnderLevel(lvl, owner, "basement", 20, 20); got != 0 {
		t.Errorf("floor under basement-bound paint = %v, want 0", got)
	}
}

// Unbound paint keeps the heuristic, so nothing written before the binding
// existed changes behaviour.
func TestUnboundPaintStillFallsBackToLowest(t *testing.T) {
	lvl, owner := terraceLevel()

	if got := biome.FloorHeightUnderLevel(lvl, owner, "", 20, 20); got != 0 {
		t.Errorf("unbound paint sampled %v, want the lowest floor 0", got)
	}
}

// A binding to a sector that has since been deleted must degrade to the
// heuristic rather than resolving to nothing.
func TestStaleDrapeBindingFallsBack(t *testing.T) {
	lvl, owner := terraceLevel()

	if got := biome.FloorHeightUnderLevel(lvl, owner, "demolished", 20, 20); got != 0 {
		t.Errorf("paint bound to a deleted sector sampled %v, want the fallback 0", got)
	}
	_, s := world.DrapeTarget(lvl, owner, "demolished", 20, 20)
	if s == nil {
		t.Error("a stale binding resolved to no sector at all; it must fall back")
	}
}

// Paint spanning off its bound surface onto a neighbouring one stays on the
// level it belongs to rather than dropping to whatever is lowest.
func TestBoundPaintSpanningOffItsSectorStaysAtItsLevel(t *testing.T) {
	lvl, owner := terraceLevel()
	ch := owner

	// A second terrace-height sector beside the first, overlapping the
	// basement but not the terrace.
	base := len(ch.Points)
	ch.Points = append(ch.Points,
		mgl32.Vec2{36, 5}, mgl32.Vec2{39, 5}, mgl32.Vec2{39, 35}, mgl32.Vec2{36, 35})
	ch.Sectors = append(ch.Sectors, world.Sector{
		ID: "walkway", PointIndices: []int{base, base + 1, base + 2, base + 3},
		MinBound: mgl32.Vec2{36, 5}, MaxBound: mgl32.Vec2{39, 35},
		Floor: world.Floor{Enabled: true, Height: 12.5}, Ceiling: world.Ceiling{Height: 30},
	})

	// (37,20) is over the basement AND the walkway, but not the terrace.
	_, s := world.DrapeTarget(lvl, owner, "terrace", 37, 20)
	if s == nil || s.ID != "walkway" {
		t.Errorf("paint bound to the terrace, spanning to (37,20), resolved to %v — "+
			"want \"walkway\" (nearest the terrace's own height), not the basement", s)
	}
}
