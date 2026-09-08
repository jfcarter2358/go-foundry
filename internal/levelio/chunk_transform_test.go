package levelio

// chunk_transform_test.go — Points/Origin/Rotation/Scale round-trip through
// loadRawChunk/chunkToRaw as plain, untransformed data now: the YAML already
// stored chunk-local points plus a separate origin/rotation/scale block, and
// the loader/saver no longer bake or unbake anything against it, so the round
// trip is a straight copy in both directions — see level_loader.go/
// level_save.go's top-of-function comments.

import (
	"foundry/internal/geom"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// placedRawChunk builds a chunk with a non-identity Origin/Rotation/Scale, the
// case the old bake/unbake path existed for.
func placedRawChunk() world.RawChunk {
	return world.RawChunk{
		ID: "placed",
		Points: map[string]geom.Point{
			"A": {X: 0, Y: 0},
			"B": {X: 10, Y: 0},
			"C": {X: 10, Y: 10},
			"D": {X: 0, Y: 10},
		},
		Sectors: []world.RawSector{{
			ID:      "room",
			Floor:   world.RawFloor{Enabled: true, Height: 1, Texture: "concrete"},
			Ceiling: world.RawCeiling{Enabled: true, Height: 4, Texture: "concrete"},
			Edges: []world.RawEdge{
				{From: "A", To: "B", Kind: "wall", Texture: "concrete"},
				{From: "B", To: "C", Kind: "wall", Texture: "concrete"},
				{From: "C", To: "D", Kind: "wall", Texture: "concrete"},
				{From: "D", To: "A", Kind: "wall", Texture: "concrete"},
			},
		}},
		Origin:   world.RawChunkOrigin{X: 50, Y: 3, Z: -20},
		Rotation: 0.4,
		Scale:    2.5,
	}
}

// TestLoadRawChunkDoesNotBakeOriginIntoPoints is the core behavior this change
// exists for: a chunk's Points stay exactly what the file says, in chunk-local
// coordinates, however far from the world origin its own Origin sits.
func TestLoadRawChunkDoesNotBakeOriginIntoPoints(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	raw := placedRawChunk()
	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}

	if ch.Origin != (mgl32.Vec3{50, 3, -20}) {
		t.Errorf("ch.Origin = %v, want the raw origin unchanged", ch.Origin)
	}
	if ch.Rotation != 0.4 || ch.Scale != 2.5 {
		t.Errorf("ch.Rotation/Scale = %v/%v, want 0.4/2.5", ch.Rotation, ch.Scale)
	}

	want := map[string]mgl32.Vec2{
		"A": {0, 0}, "B": {10, 0}, "C": {10, 10}, "D": {0, 10},
	}
	if len(ch.Points) != len(want) {
		t.Fatalf("got %d points, want %d", len(ch.Points), len(want))
	}
	for i, name := range ch.PointNames {
		wp, ok := want[name]
		if !ok {
			t.Fatalf("unexpected point name %q", name)
		}
		if ch.Points[i] != wp {
			t.Errorf("point %q = %v, want %v (unchanged from the file — Origin/Rotation/Scale must not be baked in)", name, ch.Points[i], wp)
		}
	}
}

// TestChunkOriginRotationScaleSurviveSaveLoadRoundTrip locks in that a chunk's
// placement and its local points round-trip through save/load bit-identical —
// the loader/saver are now straight field copies, so this should hold exactly,
// not just approximately.
func TestChunkOriginRotationScaleSurviveSaveLoadRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	first, _, err := loadRawChunk(placedRawChunk())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	raw, err := chunkToRaw(&first)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if raw.Origin != (world.RawChunkOrigin{X: 50, Y: 3, Z: -20}) {
		t.Errorf("saved Origin = %v, want {50 3 -20}", raw.Origin)
	}
	if raw.Rotation != 0.4 || raw.Scale != 2.5 {
		t.Errorf("saved Rotation/Scale = %v/%v, want 0.4/2.5", raw.Rotation, raw.Scale)
	}

	second, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if second.Origin != first.Origin || second.Rotation != first.Rotation || second.Scale != first.Scale {
		t.Errorf("placement changed across the round trip: %v/%v/%v then %v/%v/%v",
			first.Origin, first.Rotation, first.Scale, second.Origin, second.Rotation, second.Scale)
	}
	for i := range first.Points {
		if first.Points[i] != second.Points[i] {
			t.Errorf("point %d drifted across the round trip: %v then %v", i, first.Points[i], second.Points[i])
		}
	}
}

// TestChunkVelocitySurvivesSaveLoadRoundTrip pins the authored half of a moving
// chunk. It is a rate rather than a placement, so nothing about the geometry
// records it — a level that lost it on save would reload with every lift and
// barge standing still.
func TestChunkVelocitySurvivesSaveLoadRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	rc := placedRawChunk()
	rc.Velocity = world.RawVec3{X: 0, Y: 1.5, Z: -0.25}

	first, _, err := loadRawChunk(rc)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if first.Velocity != (mgl32.Vec3{0, 1.5, -0.25}) {
		t.Fatalf("loaded Velocity = %v, want {0 1.5 -0.25}", first.Velocity)
	}

	raw, err := chunkToRaw(&first)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if raw.Velocity != rc.Velocity {
		t.Errorf("saved Velocity = %v, want %v", raw.Velocity, rc.Velocity)
	}

	second, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if second.Velocity != first.Velocity {
		t.Errorf("velocity changed across the round trip: %v then %v", first.Velocity, second.Velocity)
	}
}

// TestAStillChunkWritesNoVelocityKey keeps `velocity:` out of every level that
// has no moving chunk in it — the omitempty half of the field, and what stops
// this from churning every file in the project the first time it is saved.
func TestAStillChunkWritesNoVelocityKey(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, _, err := loadRawChunk(placedRawChunk())
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	raw, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "velocity") {
		t.Errorf("a still chunk serialised a velocity key:\n%s", out)
	}
}

// TestChunkModelMatrixPlacesLoadedGeometryInWorldSpace exercises the whole
// chain end to end: a point authored in the file lands where Origin/Rotation/
// Scale say it should once ModelMatrix is applied, which is the behavior the
// editor's move/rotate/scale gizmos and the renderer both rely on.
func TestChunkModelMatrixPlacesLoadedGeometryInWorldSpace(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch, _, err := loadRawChunk(world.RawChunk{
		ID:     "placed",
		Points: map[string]geom.Point{"A": {X: 3, Y: 4}},
		Origin: world.RawChunkOrigin{X: 100, Y: 0, Z: 200},
		Scale:  1,
	})
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}

	local := ch.Points[0]
	m := ch.ModelMatrix()
	r := m.Mul4x1(mgl32.Vec3{local.X(), 0, local.Y()}.Vec4(1))
	got := mgl32.Vec2{r.X(), r.Z()}
	want := mgl32.Vec2{103, 204}
	if got != want {
		t.Errorf("world-space point = %v, want %v", got, want)
	}
}

// ---------- the drape binding ----------

// TestBiomeDrapeSectorSurvivesSaveLoadRoundTrip pins the persisted half of the
// drape binding. It exists precisely because it cannot be re-derived: two
// sectors stacked at the same XZ look identical from above, so a level that
// lost this on save would silently fall back to "lowest wins" and move the
// paint to a different surface than the one it was drawn on.
func TestBiomeDrapeSectorSurvivesSaveLoadRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := placedRawChunk()
	raw.BiomeSectors = []world.RawBiomeSector{{
		ID: "grass",
		Points: []geom.Point{
			{X: 2, Y: 2}, {X: 8, Y: 2}, {X: 8, Y: 8}, {X: 2, Y: 8},
		},
		Brush:       world.RawBiomeBrush{Kind: world.BiomeGrass, Density: 1},
		DrapeSector: "room",
	}}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if got := ch.BiomeSectors[0].DrapeSector; got != "room" {
		t.Fatalf("after load, DrapeSector = %q, want %q", got, "room")
	}

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if got := back.BiomeSectors[0].DrapeSector; got != "room" {
		t.Errorf("after save, DrapeSector = %q, want %q", got, "room")
	}
}

// Unbound paint must stay unbound, and the field must stay out of the YAML so
// a level written before the binding existed round-trips unchanged.
func TestUnboundBiomeDrapeSectorStaysEmpty(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := placedRawChunk()
	raw.BiomeSectors = []world.RawBiomeSector{{
		ID:     "grass",
		Points: []geom.Point{{X: 2, Y: 2}, {X: 8, Y: 2}, {X: 8, Y: 8}},
		Brush:  world.RawBiomeBrush{Kind: world.BiomeGrass, Density: 1},
	}}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if got := back.BiomeSectors[0].DrapeSector; got != "" {
		t.Errorf("unbound paint came back bound to %q", got)
	}
}
