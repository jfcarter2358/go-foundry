package world

// frame_scale_test.go — scaling an entity scales the geometry it carries.
//
// The rule under test has two halves that pull against each other. A ship
// scaled 2x has to have a hold twice as big — drawn *and* collided, or the
// deck stops being where it looks. But the frame itself must stay rigid, or
// everything stored in its coordinates (a rider's position, a rider's own
// model) would be stretched along with it.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// scaledCarrier builds a level with one carrier entity, scaled by s, owning a
// single interior chunk placed at the given offset within it.
func scaledCarrier(t *testing.T, pos, s mgl32.Vec3, chunkOrigin mgl32.Vec3) *Level {
	t.Helper()
	interior := placedRoom("hold", chunkOrigin, 0, 1)
	interior.FrameOwner = "ship"
	lvl := &Level{
		Entities: []Entity{{ID: "ship", Position: pos, Scale: s}},
		Chunks:   []Chunk{interior},
	}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	return lvl
}

func TestEntityFrameScaleAveragesMagnitudes(t *testing.T) {
	cases := []struct {
		name string
		e    *Entity
		want float32
	}{
		{"nil is identity", nil, 1},
		{"the zero value is identity", &Entity{}, 1},
		{"uniform is exact", &Entity{Scale: mgl32.Vec3{2, 2, 2}}, 2},
		{"non-uniform takes the mean", &Entity{Scale: mgl32.Vec3{1, 2, 3}}, 2},
		// A negative scale mirrors a model but cannot mirror a place: a flipped
		// chunk would invert its winding and read its floors as ceilings.
		{"negatives contribute their magnitude", &Entity{Scale: mgl32.Vec3{-2, -2, -2}}, 2},
		{"a collapsed scale is clamped off zero", &Entity{Scale: mgl32.Vec3{1e-9, 1e-9, 1e-9}}, EntityScaleMin},
	}
	for _, tc := range cases {
		if got := EntityFrameScale(tc.e); math.Abs(float64(got-tc.want)) > 1e-6 {
			t.Errorf("%s: EntityFrameScale = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestScalingACarrierScalesTheChunkItCarries(t *testing.T) {
	lvl := scaledCarrier(t, mgl32.Vec3{100, 0, 50}, mgl32.Vec3{2, 2, 2}, mgl32.Vec3{})
	ch := &lvl.Chunks[0]

	if got := ch.FrameScale; got != 2 {
		t.Fatalf("the owner's scale was not republished onto the chunk: FrameScale = %v, want 2", got)
	}
	// The room's local (10,0,10) far corner sits 10 units out at scale 1, so
	// 20 out at scale 2 — and the entity's own position is *not* scaled, since
	// it is where the frame is, not how big it is.
	corner := ch.LocalToWorld(mgl32.Vec3{10, 0, 10})
	if want := (mgl32.Vec3{120, 0, 70}); !vecNear(corner, want, 1e-3) {
		t.Errorf("the carried chunk's far corner is at %v, want %v", corner, want)
	}
	// And collision resolves against the scaled geometry, not just the drawn
	// one — the resolvers work in frame coordinates, where the hold is now
	// 20x16x20 rather than 10x8x10. Ceiling height is a local Y baked into the
	// geometry, so a point above the unscaled ceiling is inside the scaled one.
	f := lvl.Frames.Frame("ship")
	if f == nil {
		t.Fatal("no frame was built for the chunk's owner")
	}
	if c := f.ChunkAt(lvl, mgl32.Vec3{10, 12, 10}); c == nil {
		t.Error("a point between the unscaled and scaled ceilings is outside the hold; Height must scale with the carrier")
	}
	if c := f.ChunkAt(lvl, mgl32.Vec3{10, 20, 10}); c != nil {
		t.Error("a point above the scaled ceiling resolved inside the hold")
	}
}

func TestACarriersScaleCarriesItsChunksOwnOffset(t *testing.T) {
	// An attachment a metre forward of the ship's centre sits two metres
	// forward once the ship is twice the size — the owner's scale composes
	// outside the chunk's own placement, not under it.
	lvl := scaledCarrier(t, mgl32.Vec3{}, mgl32.Vec3{2, 2, 2}, mgl32.Vec3{3, 0, 0})
	origin := lvl.Chunks[0].LocalToWorld(mgl32.Vec3{})
	if want := (mgl32.Vec3{6, 0, 0}); !vecNear(origin, want, 1e-3) {
		t.Errorf("the carried chunk's origin is at %v, want %v", origin, want)
	}
}

func TestACarriersScaleMultipliesTheChunksOwnScale(t *testing.T) {
	interior := placedRoom("hold", mgl32.Vec3{}, 0, 3)
	interior.FrameOwner = "ship"
	lvl := &Level{
		Entities: []Entity{{ID: "ship", Scale: mgl32.Vec3{2, 2, 2}}},
		Chunks:   []Chunk{interior},
	}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	if got := lvl.Chunks[0].PlacementScale(); got != 6 {
		t.Errorf("PlacementScale = %v, want 6 (the chunk's own 3 and its owner's 2)", got)
	}
}

func TestACarriersScaleLeavesTheFrameItselfRigid(t *testing.T) {
	lvl := scaledCarrier(t, mgl32.Vec3{100, 0, 50}, mgl32.Vec3{2, 2, 2}, mgl32.Vec3{})
	f := lvl.Frames.Frame("ship")
	if f == nil {
		t.Fatal("no frame was built for the chunk's owner")
	}
	// A rider's stored position is in frame coordinates. Scaling the ship must
	// not move it, and must not stretch the model drawn at it: the geometry is
	// what grows, not the space.
	rider := mgl32.Vec3{0, 0, 4}
	if got := f.ToWorld(rider); !vecNear(got, mgl32.Vec3{100, 0, 54}, 1e-3) {
		t.Errorf("a rider at %v was carried to %v, want {100 0 54} — the frame took its owner's scale", rider, got)
	}
	if got := f.FromWorld(f.ToWorld(rider)); !vecNear(got, rider, 1e-3) {
		t.Errorf("the frame no longer round-trips a point under a scaled owner: %v -> %v", rider, got)
	}
}

func TestChangingACarriersScaleTakesEffectOnTheNextRefresh(t *testing.T) {
	// The editor's scale gizmo is a plain field write; nothing rebuilds the
	// level under a drag. RefreshFrames runs every tick, so republishing the
	// scale there is what makes the hold follow the drag.
	lvl := scaledCarrier(t, mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, mgl32.Vec3{})
	lvl.Entities[0].Scale = mgl32.Vec3{4, 4, 4}
	RefreshFrames(lvl, lvl.Frames)

	if got := lvl.Chunks[0].FrameScale; got != 4 {
		t.Errorf("FrameScale = %v after the owner was rescaled, want 4", got)
	}
	corner := lvl.Chunks[0].LocalToWorld(mgl32.Vec3{10, 0, 10})
	if want := (mgl32.Vec3{40, 0, 40}); !vecNear(corner, want, 1e-3) {
		t.Errorf("the carried chunk's far corner is at %v, want %v", corner, want)
	}
}

func TestAnUnframedChunkIsUntouchedByTheOwnerScalePath(t *testing.T) {
	ch := placedRoom("world", mgl32.Vec3{}, 0, 1)
	lvl := &Level{
		Entities: []Entity{{ID: "ship", Scale: mgl32.Vec3{5, 5, 5}}},
		Chunks:   []Chunk{ch},
	}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	if got := lvl.Chunks[0].FrameScale; got != 0 {
		t.Errorf("a chunk no entity carries picked up FrameScale = %v", got)
	}
	if got := lvl.Chunks[0].LocalPlacementMatrix(); got != mgl32.Ident4() {
		t.Errorf("an unplaced, unframed chunk no longer has an identity placement: %v", got)
	}
}

func TestClearFrameTransformDropsBothHalves(t *testing.T) {
	lvl := scaledCarrier(t, mgl32.Vec3{10, 0, 0}, mgl32.Vec3{2, 2, 2}, mgl32.Vec3{})
	ch := &lvl.Chunks[0]
	ch.FrameOwner = ""
	ch.ClearFrameTransform()
	if ch.Xform != (mgl32.Mat4{}) || ch.FrameScale != 0 {
		t.Errorf("unbinding left a stale placement behind: Xform=%v FrameScale=%v", ch.Xform, ch.FrameScale)
	}
	if got := ch.LocalPlacementMatrix(); got != mgl32.Ident4() {
		t.Errorf("a chunk that stopped being carried is still scaled by its old owner: %v", got)
	}
}
