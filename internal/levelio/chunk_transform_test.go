package levelio

// chunk_transform_test.go — Points/Origin/Rotation/Scale round-trip through
// loadRawChunk/chunkToRaw as plain, untransformed data now: the YAML already
// stored chunk-local points plus a separate origin/rotation/scale block, and
// the loader/saver no longer bake or unbake anything against it, so the round
// trip is a straight copy in both directions — see level_loader.go/
// level_save.go's top-of-function comments.

import (
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
	room := world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 4, 10}, 0.5, "concrete")
	brushes := make([]world.RawBrush, 0, len(room))
	for i := range room {
		brushes = append(brushes, world.BrushToRaw(&room[i]))
	}
	return world.RawChunk{
		ID:       "placed",
		Brushes:  brushes,
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

	// The brush verts are exactly what the file said, in chunk-local space:
	// the placement rides on ModelMatrix rather than being baked into them.
	want := placedRawChunk()
	for bi := range ch.Brushes {
		for vi, v := range ch.Brushes[bi].Verts {
			wx, wy, wz := want.Brushes[bi].Verts[vi*3], want.Brushes[bi].Verts[vi*3+1], want.Brushes[bi].Verts[vi*3+2]
			if v != (mgl32.Vec3{wx, wy, wz}) {
				t.Errorf("brush %d vert %d = %v, want %v (unchanged from the file — Origin/Rotation/Scale must not be baked in)",
					bi, vi, v, mgl32.Vec3{wx, wy, wz})
			}
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
	for bi := range first.Brushes {
		for vi := range first.Brushes[bi].Verts {
			if first.Brushes[bi].Verts[vi] != second.Brushes[bi].Verts[vi] {
				t.Errorf("brush %d vert %d drifted across the round trip: %v then %v",
					bi, vi, first.Brushes[bi].Verts[vi], second.Brushes[bi].Verts[vi])
			}
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
	box := world.NewBoxBrush("b", mgl32.Vec3{3, 0, 4}, mgl32.Vec3{1, 1, 1}, "concrete")
	ch, _, err := loadRawChunk(world.RawChunk{
		ID:      "placed",
		Brushes: []world.RawBrush{world.BrushToRaw(&box)},
		Origin:  world.RawChunkOrigin{X: 100, Y: 0, Z: 200},
		Scale:   1,
	})
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}

	lo, hi := ch.Brushes[0].LocalAABB()
	local := lo.Add(hi).Mul(0.5)
	m := ch.ModelMatrix()
	r := m.Mul4x1(local.Vec4(1))
	got := mgl32.Vec2{r.X(), r.Z()}
	want := mgl32.Vec2{103, 204}
	if got != want {
		t.Errorf("world-space point = %v, want %v", got, want)
	}
}

// TODO(brush migration, Phase 5): the biome drape-binding round-trip tests
// lived here. A scatter Volume has no drape binding to persist — it is placed
// where it is placed — so what replaces them is a round-trip of
// Volume.BiomeScatter once that field exists.
