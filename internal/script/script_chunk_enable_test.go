package script

// script_chunk_enable_test.go — streaming a level in and out from a script.
//
// `@chunk.prop.enabled` is the whole streaming API: a script switches an area
// off once the player has passed it and switches the next one on. What the
// write must NOT do is touch the GPU — a script tick runs before the frame's GL
// work — so these assert that the property moves the flag and leaves the
// residency to world.SyncChunkGeometry.

import (
	"testing"

	"foundry/internal/flang"
	"foundry/internal/world"
)

func TestChunkEnabledPropertyReadsAndWrites(t *testing.T) {
	lvl := hostTestLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.enabled\nend")
	if !flang.Equal(got, flang.BoolVal(true)) {
		t.Errorf("a chunk reads back %v, want true", got)
	}

	runScript(t, lvl, "", "def main() -> null\n    @chunk.prop.enabled = false\nend")
	if lvl.Chunks[0].IsEnabled() {
		t.Fatal("writing false left the chunk enabled")
	}

	got = runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.enabled\nend")
	if !flang.Equal(got, flang.BoolVal(false)) {
		t.Errorf("a disabled chunk reads back %v, want false", got)
	}

	runScript(t, lvl, "", "def main() -> null\n    @chunk.prop.enabled = true\nend")
	if !lvl.Chunks[0].IsEnabled() {
		t.Error("writing true left the chunk disabled")
	}
}

func TestChunkEnabledWriteDefersTheGeometryWork(t *testing.T) {
	// The write is a flag and nothing else: releasing buffers is gl.* work, and
	// the tick is not a place it may happen. SyncChunkGeometry, which the frame
	// loop runs between frames, is what closes the gap — so straight after the
	// write the chunk is disabled but not yet unloaded.
	lvl := hostTestLevel(t)
	runScript(t, lvl, "", "def main() -> null\n    @chunk.prop.enabled = false\nend")

	if lvl.Chunks[0].Unloaded {
		t.Error("the script write unloaded the chunk itself; that is GL work and must be deferred")
	}
	if !world.SyncChunkGeometry(lvl) {
		t.Fatal("SyncChunkGeometry saw nothing to do after the flag moved")
	}
	if !lvl.Chunks[0].Unloaded {
		t.Error("the reconcile did not unload the disabled chunk")
	}
	if world.SyncChunkGeometry(lvl) {
		t.Error("a second reconcile reported more work; it is not idempotent")
	}
}

func TestDisabledChunkStaysAddressable(t *testing.T) {
	// Otherwise nothing could ever switch it back on: `@id` has to keep
	// resolving to a chunk whose geometry is not resident.
	lvl := hostTestLevel(t)
	lvl.Chunks[0].Disabled = true
	world.SyncChunkGeometry(lvl)

	runScript(t, lvl, "", "def main() -> null\n    @chunk.prop.enabled = true\nend")
	if !lvl.Chunks[0].IsEnabled() {
		t.Error("a disabled chunk could not be switched back on by name")
	}
}
