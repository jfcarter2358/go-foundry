package render

// scatter_fingerprint_test.go — what re-bakes a scatter volume's height texture.
//
// The fingerprint used to cover every enabled chunk in the level. A ship
// shrinking away over a grass field changed its own chunk's placement bounds
// every tick (FrameScale), so the field re-baked every frame — ~60 ms each —
// though the bake never looks at another frame's geometry.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// fingerprintScene is a 20x20 grass volume standing on a ground slab, with a
// ship's hold parked over it, a building well off to the side and a roof high
// above the volume's top.
func fingerprintScene() (*world.Level, *world.Volume) {
	v := &world.Volume{Position: mgl32.Vec3{0, 1, 0}, HalfX: 10, HalfY: 1, HalfZ: 10}
	lvl := &world.Level{Chunks: []world.Chunk{
		{ID: "ground", MinBound: mgl32.Vec3{-20, -1, -20}, MaxBound: mgl32.Vec3{20, 0, 20}},
		{ID: "building", Origin: mgl32.Vec3{100, 0, 100}, MinBound: mgl32.Vec3{-5, 0, -5}, MaxBound: mgl32.Vec3{5, 5, 5}},
		{ID: "roof", MinBound: mgl32.Vec3{-20, 10, -20}, MaxBound: mgl32.Vec3{20, 11, 20}},
		{ID: "ship.geometry", FrameOwner: "ship", MinBound: mgl32.Vec3{-3, 0, -5}, MaxBound: mgl32.Vec3{3, 4, 5}},
	}}
	return lvl, v
}

func chunkByID(lvl *world.Level, id string) *world.Chunk {
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID == id {
			return &lvl.Chunks[i]
		}
	}
	return nil
}

func TestScatterFingerprintIgnoresAnotherFramesGeometry(t *testing.T) {
	lvl, v := fingerprintScene()
	before := scatterGeometryFingerprint(lvl, v)

	ship := chunkByID(lvl, "ship.geometry")
	for _, k := range []float32{0.99, 0.5, 0.001} {
		ship.FrameScale = k
		if scatterGeometryFingerprint(lvl, v) != before {
			t.Fatalf("ship scaled to %v re-baked a world volume", k)
		}
	}
	ship.Disabled = true
	if scatterGeometryFingerprint(lvl, v) != before {
		t.Error("ship despawning re-baked a world volume")
	}
}

func TestScatterFingerprintIgnoresGeometryOutsideTheColumn(t *testing.T) {
	lvl, v := fingerprintScene()
	before := scatterGeometryFingerprint(lvl, v)

	chunkByID(lvl, "building").Origin = mgl32.Vec3{90, 3, 100}
	if scatterGeometryFingerprint(lvl, v) != before {
		t.Error("a building moving well clear of the volume re-baked it")
	}
	// Above the volume's top, where the column never starts (scatterColumn).
	chunkByID(lvl, "roof").Origin = mgl32.Vec3{0, 2, 0}
	if scatterGeometryFingerprint(lvl, v) != before {
		t.Error("a roof moving above the volume re-baked it")
	}
}

func TestScatterFingerprintFollowsTheGroundUnderIt(t *testing.T) {
	cases := []struct {
		name string
		edit func(lvl *world.Level)
	}{
		{"ground raised", func(lvl *world.Level) { chunkByID(lvl, "ground").Origin = mgl32.Vec3{0, 0.5, 0} }},
		{"ground disabled", func(lvl *world.Level) { chunkByID(lvl, "ground").Disabled = true }},
		{"ground gains a brush", func(lvl *world.Level) {
			g := chunkByID(lvl, "ground")
			g.Brushes = append(g.Brushes, world.Brush{})
		}},
		{"building moved under the volume", func(lvl *world.Level) { chunkByID(lvl, "building").Origin = mgl32.Vec3{0, 0, 0} }},
	}
	for _, c := range cases {
		lvl, v := fingerprintScene()
		before := scatterGeometryFingerprint(lvl, v)
		c.edit(lvl)
		if scatterGeometryFingerprint(lvl, v) == before {
			t.Errorf("%s: fingerprint unchanged, the volume would keep its stale bake", c.name)
		}
	}
}

// A volume aboard a ship bakes from the ship's own decks, so those are what it
// follows — and the world ground under the ship is not.
func TestScatterFingerprintOfAFrameVolumeFollowsItsFrame(t *testing.T) {
	lvl, _ := fingerprintScene()
	v := &world.Volume{Position: mgl32.Vec3{0, 1, 0}, HalfX: 2, HalfY: 1, HalfZ: 4, Attach: world.Attach{Frame: "ship"}}
	before := scatterGeometryFingerprint(lvl, v)

	chunkByID(lvl, "ground").Origin = mgl32.Vec3{0, 0.5, 0}
	if scatterGeometryFingerprint(lvl, v) != before {
		t.Error("world ground moving re-baked a volume aboard the ship")
	}
	chunkByID(lvl, "ship.geometry").FrameScale = 0.5
	if scatterGeometryFingerprint(lvl, v) == before {
		t.Error("the ship's own hold resizing left its volume's bake stale")
	}
}
