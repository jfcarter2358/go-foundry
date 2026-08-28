package levelio

// opening_save_test.go — holes in a floor or ceiling across the YAML round trip.
//
// Same shape as the layer and frame tests, and for the same reason: `openings:`
// is omitempty, so a surface that has none must serialise exactly as it always
// did.

import (
	"foundry/internal/geom"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func squareOpening(id string) world.RawOpening {
	return world.RawOpening{
		ID: id,
		Points: []geom.Point{
			{X: 3, Y: 3}, {X: 6, Y: 3}, {X: 6, Y: 6}, {X: 3, Y: 6},
		},
	}
}

func TestFloorAndCeilingOpeningsRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	floorOp := squareOpening("stairwell")
	floorOp.Link = "under" // the far face of its slab
	raw.Sectors[0].Floor.Openings = []world.RawOpening{floorOp}
	raw.Sectors[0].Ceiling.Openings = []world.RawOpening{squareOpening("skylight")}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	s := &ch.Sectors[0]
	if len(s.Floor.Openings) != 1 || s.Floor.Openings[0].ID != "stairwell" {
		t.Fatalf("floor openings loaded as %+v", s.Floor.Openings)
	}
	if len(s.Ceiling.Openings) != 1 || s.Ceiling.Openings[0].ID != "skylight" {
		t.Fatalf("ceiling openings loaded as %+v", s.Ceiling.Openings)
	}
	if got := len(s.Floor.Openings[0].Points); got != 4 {
		t.Errorf("the stairwell loaded with %d corners, want 4", got)
	}
	// Points are chunk-local on disk and chunk-local in memory — no transform
	// on the way in, the same as the chunk's own point pool.
	if p := s.Floor.Openings[0].Points[0]; p.X() != 3 || p.Y() != 3 {
		t.Errorf("first corner loaded at %v, want (3,3)", p)
	}

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if len(back.Sectors[0].Floor.Openings) != 1 ||
		back.Sectors[0].Floor.Openings[0].ID != "stairwell" {
		t.Errorf("floor openings did not survive: %+v", back.Sectors[0].Floor.Openings)
	}
	if len(back.Sectors[0].Ceiling.Openings) != 1 {
		t.Errorf("ceiling openings did not survive: %+v", back.Sectors[0].Ceiling.Openings)
	}
	if p := back.Sectors[0].Floor.Openings[0].Points[2]; p.X != 6 || p.Y != 6 {
		t.Errorf("a corner came back as %+v, want (6,6)", p)
	}
	// The slab pairing has to survive too, or a reloaded level forgets that its
	// two halves are one hole and drags them apart.
	if s.Floor.Openings[0].Link != "under" {
		t.Errorf("link loaded as %q, want under", s.Floor.Openings[0].Link)
	}
	if back.Sectors[0].Floor.Openings[0].Link != "under" {
		t.Errorf("link saved as %q, want under", back.Sectors[0].Floor.Openings[0].Link)
	}
	// An unlinked hole writes no key.
	if back.Sectors[0].Ceiling.Openings[0].Link != "" {
		t.Error("a standalone hole came back with a link")
	}
}

func TestASurfaceWithNoOpeningsWritesNoOpeningsKey(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if back.Sectors[0].Floor.Openings != nil || back.Sectors[0].Ceiling.Openings != nil {
		t.Error("a sector with no holes in it emitted an openings list")
	}
	out, err := yaml.Marshal(back)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "openings:") {
		t.Errorf("a chunk that never used openings emitted the key:\n%s", out)
	}
}

// An opening survives a chunk clone with storage of its own — a cloned chunk
// that shared point slices with its source would have the two holes move
// together.
func TestCloningAChunkDeepCopiesItsOpenings(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.Sectors[0].Floor.Openings = []world.RawOpening{squareOpening("hatch")}
	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}

	cp := ch.Clone("copy", "_2")
	if len(cp.Sectors[0].Floor.Openings) != 1 {
		t.Fatal("the clone lost the hole")
	}
	cp.Sectors[0].Floor.Openings[0].Points[0] = mgl32.Vec2{99, 99}
	if p := ch.Sectors[0].Floor.Openings[0].Points[0]; p.X() == 99 {
		t.Error("moving the clone's hole moved the original's — the points are shared")
	}
}

// A floor's skirt mode round-trips, and a floor without one writes no key.
func TestSkirtRoundTrips(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.Sectors[0].Floor.Skirt = "floor"
	raw.Sectors[0].Floor.SkirtTexture = "rock"

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if ch.Sectors[0].Floor.Skirt != world.SkirtToFloor {
		t.Errorf("skirt loaded as %q", ch.Sectors[0].Floor.Skirt)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if back.Sectors[0].Floor.Skirt != "floor" || back.Sectors[0].Floor.SkirtTexture != "rock" {
		t.Errorf("skirt did not survive the round trip: %+v", back.Sectors[0].Floor)
	}
}

func TestNoSkirtWritesNoKey(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	ch, _, err := loadRawChunk(squareRawChunk(false))
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	out, err := yaml.Marshal(back)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "skirt") {
		t.Errorf("a chunk that never used a skirt emitted the key:\n%s", out)
	}
}
