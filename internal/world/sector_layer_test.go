package world

// sector_layer_test.go — sectors that overlap in XZ inside one chunk.
//
// This is the whole point of layers: an overhang, a mezzanine, a bridge over
// the ground it crosses. Before the vertical ranking in spatial.go every lookup
// took the first polygon containing the point, so the second sector of a stack
// was unreachable and the only way to author one was to put it in its own chunk
// and let chunkContainsY tell the two apart.
//
// The rule under test is floors, not spans — see the "vertical sector choice"
// section in spatial.go for why a sector's top cannot be used as a bound.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// overhangChunk is one chunk holding two sectors that share the same 10x10
// footprint: the ground at y=0, and a ledge over the top of it at y=6.
//
// The ground's ceiling is deliberately LOW (y=5, under the ledge's floor) in
// the ordinary reading of this shape — the ledge's underside is the ground's
// ceiling. openSky instead disables the ground's ceiling entirely, which is the
// outdoor cliff case and the one that would break a span-based rule.
func overhangChunk(openSky bool) *Chunk {
	ch := &Chunk{
		ID:         "cliff",
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: make([]string, 4),
		Layers: []ChunkLayer{
			{Name: "Ground", BaseY: 0, CeilY: 5},
			{Name: "Ledge", BaseY: 6, CeilY: 10},
		},
	}
	ground := Sector{
		ID:           "ground",
		Layer:        0,
		PointIndices: []int{0, 1, 2, 3},
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Floor:        Floor{Enabled: true, Height: 0, VertexOffsets: []float32{0, 0, 0, 0}},
		Ceiling:      Ceiling{Enabled: !openSky, Height: 5, VertexOffsets: []float32{0, 0, 0, 0}},
		MinBound:     mgl32.Vec2{0, 0},
		MaxBound:     mgl32.Vec2{10, 10},
	}
	ledge := Sector{
		ID:           "ledge",
		Layer:        1,
		PointIndices: []int{0, 1, 2, 3},
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Floor:        Floor{Enabled: true, Height: 6, VertexOffsets: []float32{0, 0, 0, 0}},
		Ceiling:      Ceiling{Enabled: true, Height: 10, VertexOffsets: []float32{0, 0, 0, 0}},
		MinBound:     mgl32.Vec2{0, 0},
		MaxBound:     mgl32.Vec2{10, 10},
	}
	ch.Sectors = []Sector{ground, ledge}
	ch.RecomputeBounds()
	return ch
}

func TestOverlappingSectorsInOneChunkResolveByHeight(t *testing.T) {
	for _, openSky := range []bool{false, true} {
		name := "enclosed"
		if openSky {
			name = "open sky"
		}
		ch := overhangChunk(openSky)
		lvl := &Level{Chunks: []Chunk{*ch}}
		if err := RebuildIndexes(lvl); err != nil {
			t.Fatal(err)
		}

		cases := []struct {
			y    float32
			want string
			why  string
		}{
			{0, "ground", "standing on the ground"},
			{1.8, "ground", "a body's height above the ground"},
			{4, "ground", "head-high under the ledge"},
			{5.9, "ground", "just under the ledge's floor"},
			{6, "ledge", "standing on the ledge"},
			{7.8, "ledge", "a body's height above the ledge"},
		}
		for _, tc := range cases {
			pos := mgl32.Vec3{5, tc.y, 5}

			_, s := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, pos)
			if s == nil {
				t.Errorf("%s y=%v (%s): the spatial hash found no sector", name, tc.y, tc.why)
			} else if s.ID != tc.want {
				t.Errorf("%s y=%v (%s): the spatial hash found %q, want %q", name, tc.y, tc.why, s.ID, tc.want)
			}

			_, s = FindChunkAndSector(lvl.Chunks, pos)
			if s == nil {
				t.Errorf("%s y=%v (%s): the linear scan found no sector", name, tc.y, tc.why)
			} else if s.ID != tc.want {
				t.Errorf("%s y=%v (%s): the linear scan found %q, want %q", name, tc.y, tc.why, s.ID, tc.want)
			}
		}
	}
}

// A jumping body leaves the top of its sector; an outdoor sector has no top at
// all. Either way it must stay in the sector it took off from, or gravity hands
// it back to a world it is no longer part of.
func TestJumpingDoesNotLeaveTheSector(t *testing.T) {
	ch := overhangChunk(true)
	// Raise the chunk's own ceiling well clear so chunkContainsY is not what is
	// under test here — the question is whether the SECTOR choice survives.
	ch.MaxBound[1] = 200
	lvl := &Level{Chunks: []Chunk{*ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	lvl.Chunks[0].MaxBound[1] = 200

	for _, y := range []float32{6.5, 8, 12, 40} {
		_, s := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{5, y, 5})
		if s == nil {
			t.Fatalf("y=%v: found no sector above the ledge", y)
		}
		if s.ID != "ledge" {
			t.Errorf("y=%v: found %q, want the ledge it jumped from", y, s.ID)
		}
	}
}

// The other half of the ranking: a position under every candidate's floor takes
// the lowest of them, the surface it is falling toward.
//
// Reaching that branch takes a sloped floor. A chunk's MinBound.Y is its lowest
// floor ANYWHERE in it, so a position inside the chunk's bounds is normally at
// or above the flat floor beneath it — but a floor that slopes up to y=10 is
// well above a body at y=2 standing over its high end, while the chunk's bounds
// still start at 0.
func TestBelowEveryFloorPicksTheLowest(t *testing.T) {
	ch := overhangChunk(false)
	ch.Sectors[0].Floor.VertexOffsets = []float32{0, 0, 10, 10}
	ch.Sectors[0].Ceiling.Height = 16
	ch.Sectors[1].Floor.Height = 20 // lift the ledge clear of the ramp
	ch.Sectors[1].Ceiling.Height = 24
	ch.RecomputeBounds()

	lvl := &Level{Chunks: []Chunk{*ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	// Over the ramp's high end, under both floors.
	_, s := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{5, 2, 8})
	if s == nil {
		t.Fatal("found no sector under both floors")
	}
	if s.ID != "ground" {
		t.Errorf("found %q, want the lower of the two floors above it", s.ID)
	}
}

// The 2D lookups have no height to rank by, so they must at least be
// deterministic — and the lowest floor is the answer the biome drape heuristic
// already documents.
func TestFindSectorWithoutAHeightTakesTheLowestFloor(t *testing.T) {
	ch := overhangChunk(false)
	if s := FindSector(ch, mgl32.Vec2{5, 5}); s == nil || s.ID != "ground" {
		t.Fatalf("FindSector = %v, want the ground sector", s)
	}
	// Slice order must not be what decides it.
	ch.Sectors[0], ch.Sectors[1] = ch.Sectors[1], ch.Sectors[0]
	if s := FindSector(ch, mgl32.Vec2{5, 5}); s == nil || s.ID != "ground" {
		t.Fatalf("FindSector after reordering = %v, want the ground sector", s)
	}
	// With a height it is the ranked answer instead.
	if s := FindSectorAt(ch, mgl32.Vec2{5, 5}, 6); s == nil || s.ID != "ledge" {
		t.Fatalf("FindSectorAt(y=6) = %v, want the ledge", s)
	}
}

// A moved chunk's floors are chunk-local, so the ranking has to carry them out
// to world space before weighing one candidate against the next — otherwise a
// stack inside a chunk that has been lifted resolves against raw local numbers
// that mean nothing in the query's space.
func TestOverlapRankingFollowsAMovedChunk(t *testing.T) {
	ch := overhangChunk(false)
	ch.Origin = mgl32.Vec3{100, 50, 100}
	lvl := &Level{Chunks: []Chunk{*ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		y    float32
		want string
	}{
		{50, "ground"},
		{54, "ground"},
		{56, "ledge"},
		{58, "ledge"},
	}
	for _, tc := range cases {
		_, s := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{105, tc.y, 105})
		if s == nil {
			t.Errorf("y=%v: found no sector in the moved chunk", tc.y)
			continue
		}
		if s.ID != tc.want {
			t.Errorf("y=%v: found %q, want %q", tc.y, s.ID, tc.want)
		}
	}
}

func TestLayerBandsAreSynthesizedForUnlabelledLayers(t *testing.T) {
	ch := overhangChunk(false)
	ch.Layers = nil // an old save: sectors carry layers, the chunk has no metadata

	if got := ch.LayerCount(); got != 2 {
		t.Errorf("LayerCount = %d, want 2 (derived from the sectors)", got)
	}
	// The band is read off the sectors actually on the layer, not invented from
	// the index — the ledge is at y=6, not at 1*DefaultLayerHeight.
	if l := ch.LayerAt(1); l.BaseY != 6 || l.CeilY != 10 {
		t.Errorf("LayerAt(1) = %+v, want the ledge's own 6..10", l)
	}
	// A layer with nothing on it has nothing to read, so it stacks by index.
	if l := ch.LayerAt(5); l.BaseY != 5*DefaultLayerHeight {
		t.Errorf("LayerAt(5).BaseY = %v, want %v", l.BaseY, 5*DefaultLayerHeight)
	}
}

func TestRemoveLayerMergesDownwardAndKeepsGeometry(t *testing.T) {
	ch := overhangChunk(false)
	ch.RemoveLayer(1)

	if got := ch.LayerCount(); got != 1 {
		t.Errorf("LayerCount after removing the ledge's layer = %d, want 1", got)
	}
	if len(ch.Sectors) != 2 {
		t.Fatalf("removing a layer deleted geometry: %d sectors left", len(ch.Sectors))
	}
	for i := range ch.Sectors {
		if ch.Sectors[i].Layer != 0 {
			t.Errorf("sector %q left on layer %d, want the ground layer",
				ch.Sectors[i].ID, ch.Sectors[i].Layer)
		}
	}
	// The ground layer itself cannot be removed out from under its sectors.
	ch.RemoveLayer(0)
	if ch.LayerCount() != 1 || len(ch.Sectors) != 2 {
		t.Errorf("removing the last layer changed the chunk: %d layers, %d sectors",
			ch.LayerCount(), len(ch.Sectors))
	}
}
