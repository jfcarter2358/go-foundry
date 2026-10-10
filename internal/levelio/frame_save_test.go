package levelio

// frame_save_test.go — a chunk's frame owner across the YAML round trip.
//
// The important half is the negative one: a chunk with no frame must serialise
// exactly as it always did. `frame:` is omitempty, so a level written before
// frames existed and re-saved by this build has to come back byte-identical.

import (
	"foundry/internal/testenv"
	"foundry/internal/world"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestChunkFrameOwnerRoundTrips(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.ID = "cabin"
	raw.Frame = "shuttle"

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if ch.FrameOwner != "shuttle" {
		t.Fatalf("FrameOwner = %q, want shuttle", ch.FrameOwner)
	}
	if !ch.InFrame() {
		t.Error("a chunk with a frame owner does not report being in a frame")
	}

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if back.Frame != "shuttle" {
		t.Errorf("frame owner did not survive the round trip: %q", back.Frame)
	}
}

func TestAChunkWithNoFrameWritesNoFrameKey(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.ID = "room"

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if ch.InFrame() {
		t.Error("a chunk with no frame owner claimed to be in one")
	}

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	out, err := yaml.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	// omitempty: a level that predates frames must not gain a key when re-saved.
	if strings.Contains(string(out), "frame:") {
		t.Errorf("an unframed chunk serialised a frame key:\n%s", out)
	}
}

func TestUnknownFrameOwnerFailsTheLoad(t *testing.T) {
	// A chunk owned by an entity that does not exist has its geometry nowhere:
	// out of the world index, and in a frame nothing drives. Better to refuse
	// the level than to draw it at the origin and wonder where the ship went.
	lvl := &world.Level{Chunks: []world.Chunk{{ID: "cabin", FrameOwner: "nobody"}}}
	err := world.RebuildIndexes(lvl)
	if err == nil {
		t.Fatal("a chunk owned by a missing entity was accepted")
	}
	if !strings.Contains(err.Error(), "nobody") {
		t.Errorf("the error does not name the missing owner: %v", err)
	}
	// The world half of the level is still usable, so a caller that chooses to
	// carry on is not left without a spatial hash.
	if lvl.Spatial == nil {
		t.Error("the spatial hash was not rebuilt despite the frame error")
	}
	if lvl.Frames == nil {
		t.Error("Frames was left nil rather than empty; every caller would have to nil-check")
	}
}
