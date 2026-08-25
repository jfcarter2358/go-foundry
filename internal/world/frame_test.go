package world

// frame_test.go — the coordinate systems, and the rules that keep nesting honest.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// frameLevel builds a level with one carrier entity and one chunk in its frame.
func frameLevel(t *testing.T, owner string, pos mgl32.Vec3, rot mgl32.Vec3) *Level {
	t.Helper()
	lvl := &Level{
		Entities: []Entity{{ID: owner, Position: pos, Rotation: rot, Scale: mgl32.Vec3{1, 1, 1}}},
		Chunks: []Chunk{
			{ID: "outside"},
			{ID: "interior", FrameOwner: owner,
				MinBound: mgl32.Vec3{-2, 0, -3}, MaxBound: mgl32.Vec3{2, 3, 3}},
		},
	}
	return lvl
}

func TestWorldFrameIsExactlyIdentity(t *testing.T) {
	fs, err := BuildFrames(&Level{})
	if err != nil {
		t.Fatal(err)
	}
	w := fs.World()
	if !w.IsWorld() {
		t.Error("the world frame does not report itself as the world")
	}
	p := mgl32.Vec3{3, -4, 5}
	if w.ToWorld(p) != p || w.FromWorld(p) != p {
		t.Errorf("the world frame moved a point: %v -> %v / %v", p, w.ToWorld(p), w.FromWorld(p))
	}
	// A nil frame reads as the world, so callers that know nothing about frames
	// can pass nil rather than having to find one.
	var nilFrame *Frame
	if !nilFrame.IsWorld() || nilFrame.ToWorld(p) != p {
		t.Error("a nil frame did not read as the world")
	}
}

func TestFrameRoundTripsAPoint(t *testing.T) {
	lvl := frameLevel(t, "ship", mgl32.Vec3{100, 20, -50}, mgl32.Vec3{0.3, 1.2, -0.4})
	fs, err := BuildFrames(lvl)
	if err != nil {
		t.Fatal(err)
	}
	RefreshFrames(lvl, fs)

	f := fs.Frame("ship")
	if f == nil {
		t.Fatal("no frame was built for the chunk's owner")
	}
	for _, p := range []mgl32.Vec3{{0, 0, 0}, {1, 2, 3}, {-5, 0.5, 8}} {
		if got := f.FromWorld(f.ToWorld(p)); !vecNear(got, p, 1e-3) {
			t.Errorf("%v round-tripped to %v", p, got)
		}
	}
	// The frame's origin is the owner's position.
	if got := f.ToWorld(mgl32.Vec3{}); !vecNear(got, mgl32.Vec3{100, 20, -50}, 1e-3) {
		t.Errorf("frame origin is at %v, want the owner's position", got)
	}
}

func TestFrameIgnoresTheOwnersScale(t *testing.T) {
	// A frame is a place to stand, not a model: scaling it would scale the deck
	// without scaling the body walking on it.
	lvl := frameLevel(t, "ship", mgl32.Vec3{10, 0, 0}, mgl32.Vec3{})
	lvl.Entities[0].Scale = mgl32.Vec3{5, 5, 5}
	fs, _ := BuildFrames(lvl)
	RefreshFrames(lvl, fs)

	f := fs.Frame("ship")
	if got := f.ToWorld(mgl32.Vec3{1, 0, 0}); !vecNear(got, mgl32.Vec3{11, 0, 0}, 1e-4) {
		t.Errorf("a point one unit into a 5× scaled frame landed at %v, want 11 units out", got)
	}
}

func TestFrameCarriesDirectionsWithoutTranslating(t *testing.T) {
	lvl := frameLevel(t, "ship", mgl32.Vec3{100, 0, 0}, mgl32.Vec3{0, math.Pi / 2, 0})
	fs, _ := BuildFrames(lvl)
	RefreshFrames(lvl, fs)
	f := fs.Frame("ship")

	// Yawed 90° about Y: local +X becomes world −Z.
	got := f.DirToWorld(mgl32.Vec3{1, 0, 0})
	if !vecNear(got, mgl32.Vec3{0, 0, -1}, 1e-3) {
		t.Errorf("local +X became %v, want world -Z", got)
	}
	if got := f.DirFromWorld(f.DirToWorld(mgl32.Vec3{0, 1, 0})); !vecNear(got, mgl32.Vec3{0, 1, 0}, 1e-3) {
		t.Errorf("a direction did not round-trip: %v", got)
	}
	// Local up stays world up under a pure yaw, and picks up no translation.
	if got := f.DirToWorld(mgl32.Vec3{0, 1, 0}); !vecNear(got, mgl32.Vec3{0, 1, 0}, 1e-3) {
		t.Errorf("up became %v under a yaw", got)
	}
}

func TestNestedFramesCompose(t *testing.T) {
	// A pod inside a shuttle inside a carrier. The pod's contents must land
	// where all three transforms put them.
	lvl := &Level{
		Entities: []Entity{
			{ID: "carrier", Position: mgl32.Vec3{1000, 0, 0}, Scale: mgl32.Vec3{1, 1, 1}},
			{ID: "shuttle", Frame: "carrier", Position: mgl32.Vec3{0, 10, 0}, Scale: mgl32.Vec3{1, 1, 1}},
			{ID: "pod", Frame: "shuttle", Position: mgl32.Vec3{0, 0, 5}, Scale: mgl32.Vec3{1, 1, 1}},
		},
		Chunks: []Chunk{
			{ID: "hangar", FrameOwner: "carrier"},
			{ID: "cabin", FrameOwner: "shuttle"},
			{ID: "hold", FrameOwner: "pod"},
		},
	}
	fs, err := BuildFrames(lvl)
	if err != nil {
		t.Fatal(err)
	}
	RefreshFrames(lvl, fs)

	if d := fs.Frame("carrier").Depth; d != 1 {
		t.Errorf("carrier depth %d, want 1", d)
	}
	if d := fs.Frame("pod").Depth; d != 3 {
		t.Errorf("pod depth %d, want 3", d)
	}
	// 1000 out, 10 up, 5 along — composed.
	if got := fs.Frame("pod").ToWorld(mgl32.Vec3{}); !vecNear(got, mgl32.Vec3{1000, 10, 5}, 1e-3) {
		t.Errorf("the pod's origin is at %v, want [1000 10 5]", got)
	}
}

func TestFrameOrderPutsParentsFirst(t *testing.T) {
	lvl := &Level{
		Entities: []Entity{
			{ID: "pod", Frame: "shuttle", Scale: mgl32.Vec3{1, 1, 1}},
			{ID: "carrier", Scale: mgl32.Vec3{1, 1, 1}},
			{ID: "shuttle", Frame: "carrier", Scale: mgl32.Vec3{1, 1, 1}},
		},
		Chunks: []Chunk{
			{ID: "c", FrameOwner: "pod"},
			{ID: "b", FrameOwner: "shuttle"},
			{ID: "a", FrameOwner: "carrier"},
		},
	}
	fs, err := BuildFrames(lvl)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range fs.Order() {
		if f.Parent != "" && fs.Frame(f.Parent) != nil && !seen[f.Parent] {
			t.Errorf("%q came before its parent %q", f.OwnerID, f.Parent)
		}
		seen[f.OwnerID] = true
	}
	if len(fs.Order()) != 3 {
		t.Errorf("built %d frames, want 3", len(fs.Order()))
	}
}

func TestFrameCyclesAndOverDeepNestingAreRefused(t *testing.T) {
	cycle := &Level{
		Entities: []Entity{
			{ID: "a", Frame: "b", Scale: mgl32.Vec3{1, 1, 1}},
			{ID: "b", Frame: "a", Scale: mgl32.Vec3{1, 1, 1}},
		},
		Chunks: []Chunk{{ID: "ca", FrameOwner: "a"}, {ID: "cb", FrameOwner: "b"}},
	}
	if _, err := BuildFrames(cycle); err == nil {
		t.Error("a frame inside itself was accepted")
	}

	// One deeper than the limit.
	deep := &Level{}
	prev := ""
	for i := 0; i <= MaxFrameDepth; i++ {
		id := string(rune('a' + i))
		deep.Entities = append(deep.Entities, Entity{ID: id, Frame: prev, Scale: mgl32.Vec3{1, 1, 1}})
		deep.Chunks = append(deep.Chunks, Chunk{ID: "c" + id, FrameOwner: id})
		prev = id
	}
	if _, err := BuildFrames(deep); err == nil {
		t.Errorf("nesting %d deep was accepted; the limit is %d", MaxFrameDepth+1, MaxFrameDepth)
	}
}

func TestUnknownFrameOwnerIsRefused(t *testing.T) {
	lvl := &Level{Chunks: []Chunk{{ID: "interior", FrameOwner: "ghost"}}}
	if _, err := BuildFrames(lvl); err == nil {
		t.Error("a chunk owned by an entity that does not exist was accepted")
	}
}

func TestFrameChunksAreNotInTheWorldLookup(t *testing.T) {
	// The trap this avoids: an interior authored around its own origin would
	// otherwise answer for a player standing at world (0,0).
	lvl := frameLevel(t, "ship", mgl32.Vec3{500, 0, 500}, mgl32.Vec3{})
	interior := &lvl.Chunks[1]
	interior.Points = []mgl32.Vec2{{-2, -2}, {2, -2}, {2, 2}, {-2, 2}}
	interior.PointNames = make([]string, 4)
	interior.Sectors = []Sector{{
		ID: "cabin", PointIndices: []int{0, 1, 2, 3},
		EdgeKinds: []string{"wall", "wall", "wall", "wall"},
		Floor:     Floor{Enabled: true}, Ceiling: Ceiling{Enabled: true, Height: 3},
		MinBound: mgl32.Vec2{-2, -2}, MaxBound: mgl32.Vec2{2, 2},
	}}
	interior.RecomputeBounds()

	lvl.Spatial = BuildSpatialHash(lvl.Chunks, 8)
	origin := mgl32.Vec3{0, 1, 0}
	if c, s := FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, origin); c != nil || s != nil {
		t.Errorf("the world lookup found frame geometry at the origin: chunk %v", c.ID)
	}
	if c := FindChunk(lvl.Chunks, origin); c != nil {
		t.Errorf("the linear chunk scan found frame geometry at the origin: %v", c.ID)
	}
	if c, _ := FindChunkAndSector(lvl.Chunks, origin); c != nil {
		t.Errorf("the linear sector scan found frame geometry at the origin: %v", c.ID)
	}

	// But the frame's own search does find it, in the frame's own coordinates.
	fs, err := BuildFrames(lvl)
	if err != nil {
		t.Fatal(err)
	}
	RefreshFrames(lvl, fs)
	c, s := fs.Frame("ship").ChunkAndSectorAt(lvl, origin)
	if c == nil || s == nil {
		t.Fatal("the frame's own search did not find its interior at its own origin")
	}
	if s.ID != "cabin" {
		t.Errorf("found sector %q, want cabin", s.ID)
	}
}

func TestChunkWorldBoundsFollowTheFrame(t *testing.T) {
	lvl := frameLevel(t, "ship", mgl32.Vec3{100, 5, 0}, mgl32.Vec3{})
	fs, _ := BuildFrames(lvl)
	RefreshFrames(lvl, fs)

	interior := &lvl.Chunks[1] // local bounds [-2,0,-3]..[2,3,3]
	min, max := interior.WorldBounds()
	if !vecNear(min, mgl32.Vec3{98, 5, -3}, 1e-3) || !vecNear(max, mgl32.Vec3{102, 8, 3}, 1e-3) {
		t.Errorf("translated bounds %v..%v, want [98 5 -3]..[102 8 3]", min, max)
	}

	// Yawed 90°: the 4-wide, 6-deep box becomes 6-wide and 4-deep.
	lvl.Entities[0].Rotation = mgl32.Vec3{0, math.Pi / 2, 0}
	RefreshFrames(lvl, fs)
	min, max = interior.WorldBounds()
	if w := max.X() - min.X(); math.Abs(float64(w-6)) > 1e-3 {
		t.Errorf("after a 90° yaw the box is %v wide in X, want 6", w)
	}
	if d := max.Z() - min.Z(); math.Abs(float64(d-4)) > 1e-3 {
		t.Errorf("after a 90° yaw the box is %v deep in Z, want 4", d)
	}

	// A world chunk's bounds are untouched, and cost nothing to ask for.
	outside := &lvl.Chunks[0]
	omin, omax := outside.WorldBounds()
	if omin != outside.MinBound || omax != outside.MaxBound {
		t.Error("a world chunk's WorldBounds differ from its own bounds")
	}
}

func TestWorldPositionOfAnEntityInAFrame(t *testing.T) {
	lvl := frameLevel(t, "ship", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, math.Pi, 0})
	lvl.Entities = append(lvl.Entities, Entity{
		ID: "crate", Frame: "ship", Position: mgl32.Vec3{0, 0, 4}, Scale: mgl32.Vec3{1, 1, 1},
	})
	fs, _ := BuildFrames(lvl)
	RefreshFrames(lvl, fs)

	crate := FindEntity(lvl, "crate")
	// Turned all the way around: 4 ahead in the ship is 4 behind in the world.
	if got := fs.WorldPosition(crate); !vecNear(got, mgl32.Vec3{0, 0, -4}, 1e-3) {
		t.Errorf("the crate is at world %v, want [0 0 -4]", got)
	}
	// A world-space entity is where it says it is.
	ship := FindEntity(lvl, "ship")
	if got := fs.WorldPosition(ship); !vecNear(got, ship.Position, 1e-6) {
		t.Errorf("a world-space entity moved: %v vs %v", got, ship.Position)
	}
}
