package world

// chunk_enable_test.go — switching a chunk off and on again.
//
// What is asserted is what the flag promises: the chunk stops being findable,
// its buffers stay built (so switching an area on costs no stall) unless it
// opted into unloading, and a toggle is always reported so the indexes follow.

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

func TestDisableKeepsGeometryResident(t *testing.T) {
	// Switching an area on mid-play must not stall the frame, so disabling is a
	// flag and nothing more: the meshes stay built, and no mesh changed, so the
	// raycast accel and anything else keyed on the mesh generation stays warm.
	ch := enableTestChunk(t)
	gen := BrushMeshGeneration()

	if !SetChunkEnabled(ch, false) {
		t.Fatal("disabling an enabled chunk reported no change")
	}
	if ch.Unloaded || !chunkHasMeshes(ch) {
		t.Error("disabling released the geometry; only unload_when_disabled should")
	}
	if !SetChunkEnabled(ch, true) {
		t.Fatal("enabling a disabled chunk reported no change")
	}
	if ch.Unloaded || !chunkHasMeshes(ch) {
		t.Error("geometry missing after switching back on")
	}
	if g := BrushMeshGeneration(); g != gen {
		t.Errorf("toggling a resident chunk bumped the brush mesh generation %d -> %d", gen, g)
	}
}

func TestUnloadWhenDisabledReleasesAndRebuilds(t *testing.T) {
	ch := enableTestChunk(t)
	ch.UnloadWhenDisabled = true

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

// The trap in decoupling residency from the flag: SyncChunkGeometry reporting
// a change is the only thing that rebuilds the spatial hash after a script
// toggle, and FindChunkSH trusts the hash. A toggle that changes no residency
// still has to be reported, once.
func TestAFlagOnlyToggleStillRebuildsTheIndexes(t *testing.T) {
	ch := enableTestChunk(t)
	lvl := &Level{Chunks: []Chunk{*ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	inside := mgl32.Vec3{5, 1, 5}
	if FindChunkSH(lvl.Spatial, lvl.Chunks, inside) == nil {
		t.Fatal("fixture is not findable through the hash while enabled")
	}
	if SyncChunkGeometry(lvl) {
		t.Error("a freshly indexed level reported a change")
	}

	// What a script's `@room.prop.enabled = false` does: the flag alone.
	lvl.Chunks[0].Disabled = true
	if !SyncChunkGeometry(lvl) {
		t.Fatal("the frame's reconcile did not report the toggle; the hash would stay stale")
	}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	if c := FindChunkSH(lvl.Spatial, lvl.Chunks, inside); c != nil {
		t.Error("a chunk a script disabled is still found through the hash on the next frame")
	}
	if SyncChunkGeometry(lvl) {
		t.Error("the toggle was reported twice")
	}

	lvl.Chunks[0].Disabled = false
	if !SyncChunkGeometry(lvl) {
		t.Fatal("switching back on was not reported")
	}
	RebuildIndexes(lvl)
	if FindChunkSH(lvl.Spatial, lvl.Chunks, inside) == nil {
		t.Error("a chunk switched back on is not found through the hash")
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
