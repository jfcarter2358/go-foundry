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

// enableTestChunk builds one box brush with loaded geometry, GL-free.
func enableTestChunk(t *testing.T) *Chunk {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	ch := &Chunk{
		ID:       "room",
		MinBound: mgl32.Vec3{0, -1, 0},
		MaxBound: mgl32.Vec3{10, 5, 10},
		Brushes:  []Brush{NewBoxBrush("A", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{5, 2, 5}, "concrete")},
	}
	ch.ReloadGeometry()
	return ch
}

func chunkHasMeshes(ch *Chunk) bool {
	for bi := range ch.Brushes {
		for fi := range ch.Brushes[bi].Faces {
			if len(ch.Brushes[bi].Faces[fi].Geometry.Vertices) > 0 {
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

	if c := FindChunk(chunks, inside); c == nil {
		t.Fatalf("fixture is not findable while enabled: chunk=%v", c)
	}
	if sh := BuildSpatialHash(chunks, SpatialCellSize); len(sh.Cells) == 0 {
		t.Fatal("fixture buckets into no cells while enabled")
	}

	chunks[0].Disabled = true

	if c := FindChunk(chunks, inside); c != nil {
		t.Errorf("a disabled chunk was found by FindChunk: %v", c)
	}
	sh := BuildSpatialHash(chunks, SpatialCellSize)
	if len(sh.Cells) != 0 {
		t.Errorf("a disabled chunk was bucketed into %d cells, want 0", len(sh.Cells))
	}
	if c := FindChunkSH(sh, chunks, inside); c != nil {
		t.Errorf("a disabled chunk was found through the hash: chunk=%v", c)
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

// A compiled chunk has no meshes of its own, and must not grow any when it
// comes back: they would be a second copy of what the static list already
// draws. And the static list has to be told, since releasing the chunk's
// (empty) buffers reaches nothing that is on screen.
func TestACompiledChunkStreamsThroughTheStaticList(t *testing.T) {
	lvl := &Level{
		Chunks: []Chunk{{
			ID: "area", Compiled: true, Scale: 1,
			Brushes: []Brush{NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")},
		}},
		Static: &StaticGeometry{Surfaces: []StaticSurface{{Chunk: "area"}}},
	}

	SetChunkEnabled(&lvl.Chunks[0], false)
	SyncChunkGeometry(lvl)
	if !lvl.Static.Hidden(0) {
		t.Error("switching a compiled chunk off left its surfaces drawable")
	}

	SetChunkEnabled(&lvl.Chunks[0], true)
	SyncChunkGeometry(lvl)
	if lvl.Static.Hidden(0) {
		t.Error("switching it back on left its surfaces hidden")
	}
	for fi := range lvl.Chunks[0].Brushes[0].Faces {
		if len(lvl.Chunks[0].Brushes[0].Faces[fi].Geometry.Vertices) > 0 {
			t.Fatalf("face %d grew a mesh; it would be drawn on top of the compiled surface", fi)
		}
	}
}
