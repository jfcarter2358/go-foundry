package world

// chunk_enable_test.go — switching a chunk off and on again.
//
// Three things are asserted, and they are the three the flag promises: the
// chunk stops being findable, its buffers are actually released, and switching
// it back on rebuilds them from the same authored data the loader would have
// read.

import (
	"testing"

	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

// enableTestChunk builds one square sector with loaded geometry, GL-free.
func enableTestChunk(t *testing.T) *Chunk {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	ch := &Chunk{
		ID:         "room",
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: []string{"P0", "P1", "P2", "P3"},
		MinBound:   mgl32.Vec3{0, -1, 0},
		MaxBound:   mgl32.Vec3{10, 5, 10},
	}
	idx := []int{0, 1, 2, 3}
	var walls []Wall
	for i := range idx {
		walls = append(walls, Wall{
			StartPoint: ch.Points[idx[i]],
			EndPoint:   ch.Points[idx[(i+1)%len(idx)]],
			Texture:    "concrete",
			Collision:  true,
			Visible:    true,
			Alpha:      1,
		})
	}
	s := Sector{
		ID:           "A",
		PointIndices: idx,
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Walls:        walls,
		Floor:        Floor{Enabled: true, Texture: "concrete", VertexOffsets: []float32{0, 0, 0, 0}},
		Ceiling:      Ceiling{Enabled: true, Height: 4, Texture: "concrete", VertexOffsets: []float32{0, 0, 0, 0}},
	}
	ch.Sectors = []Sector{s}
	ch.ReloadGeometry()
	return ch
}

func chunkHasMeshes(ch *Chunk) bool {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if len(s.Floor.Geometry.Vertices) > 0 || len(s.Ceiling.Geometry.Vertices) > 0 {
			return true
		}
		for wi := range s.Walls {
			if len(s.Walls[wi].Geometry.Vertices) > 0 {
				return true
			}
		}
	}
	return false
}

func TestChunkStartsEnabled(t *testing.T) {
	// The zero value has to be "in the world": every chunk a test, an import or
	// a level file written before this existed builds is one.
	var ch Chunk
	if !ch.IsEnabled() {
		t.Error("a zero-value chunk is disabled; the flag is the wrong way round")
	}
}

func TestDisableReleasesGeometryAndEnableRebuildsIt(t *testing.T) {
	ch := enableTestChunk(t)
	if !chunkHasMeshes(ch) {
		t.Fatal("fixture has no geometry to release")
	}

	if !SetChunkEnabled(ch, false) {
		t.Fatal("disabling an enabled chunk reported no change")
	}
	if !ch.Unloaded {
		t.Error("chunk is disabled but not marked unloaded")
	}
	if chunkHasMeshes(ch) {
		t.Error("geometry survived the disable; the buffers are still held")
	}

	if !SetChunkEnabled(ch, true) {
		t.Fatal("enabling a disabled chunk reported no change")
	}
	if ch.Unloaded {
		t.Error("chunk is enabled but still marked unloaded")
	}
	if !chunkHasMeshes(ch) {
		t.Error("geometry was not rebuilt on enable")
	}
}

func TestSetChunkEnabledIsIdempotent(t *testing.T) {
	ch := enableTestChunk(t)
	if SetChunkEnabled(ch, true) {
		t.Error("enabling an already-enabled chunk reported a change")
	}
	SetChunkEnabled(ch, false)
	if SetChunkEnabled(ch, false) {
		t.Error("disabling an already-disabled chunk reported a change")
	}
}

func TestDisabledChunkIsNotFound(t *testing.T) {
	// The point of leaving it out of the hash: collision, entity location and
	// the script location verbs all resolve through these, so a disabled chunk
	// has to be invisible to every one of them.
	ch := enableTestChunk(t)
	chunks := []Chunk{*ch}
	inside := mgl32.Vec3{5, 1, 5}

	if c, s := FindChunkAndSector(chunks, inside); c == nil || s == nil {
		t.Fatalf("fixture is not findable while enabled: chunk=%v sector=%v", c, s)
	}
	if sh := BuildSpatialHash(chunks, SpatialCellSize); len(sh.Cells) == 0 {
		t.Fatal("fixture buckets into no cells while enabled")
	}

	chunks[0].Disabled = true

	if c, s := FindChunkAndSector(chunks, inside); c != nil || s != nil {
		t.Errorf("a disabled chunk was found by FindChunkAndSector: chunk=%v sector=%v", c, s)
	}
	if c := FindChunk(chunks, inside); c != nil {
		t.Errorf("a disabled chunk was found by FindChunk: %v", c)
	}
	sh := BuildSpatialHash(chunks, SpatialCellSize)
	if len(sh.Cells) != 0 {
		t.Errorf("a disabled chunk was bucketed into %d cells, want 0", len(sh.Cells))
	}
	if c, s := FindChunkAndSectorSH(sh, chunks, inside); c != nil || s != nil {
		t.Errorf("a disabled chunk was found through the hash: chunk=%v sector=%v", c, s)
	}
}

func TestCloneCarriesDisabled(t *testing.T) {
	// Duplicating a switched-off chunk must not quietly switch the copy on.
	ch := enableTestChunk(t)
	ch.Disabled = true
	cp := ch.Clone("room2", "_2")
	if cp.IsEnabled() {
		t.Error("the copy of a disabled chunk came back enabled")
	}
	if cp.Unloaded {
		t.Error("the copy is marked unloaded; it has no buffers to release yet")
	}
}

func TestReleaseLevelGeometryFreesEveryChunk(t *testing.T) {
	ch := enableTestChunk(t)
	lvl := &Level{Chunks: []Chunk{*ch}}
	ReleaseLevelGeometry(lvl)
	if chunkHasMeshes(&lvl.Chunks[0]) {
		t.Error("chunk geometry survived ReleaseLevelGeometry")
	}
}
