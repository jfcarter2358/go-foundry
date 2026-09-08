package collision

// raycast_test.go — the cast, against a room with things standing in it.
//
// Everything here runs with no GL context: Geometry.Load builds the CPU mesh
// and skips the upload (testenv.WithoutGeometryUploads), which is the whole
// reason the geometry half of the cast tests triangles rather than a proxy.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/leveltest"
	"foundry/internal/world"
)

// rayRoom is leveltest's square room — 10x10, floor at 0, ceiling at 4, walls
// all round — with its geometry actually built. Looking along +X from inside,
// the far wall is at x=10.
func rayRoom(t *testing.T) *world.Level {
	t.Helper()
	ch := leveltest.EdgeIDChunk(t)
	ch.ID = "room_chunk"
	leveltest.LoadGeometry(t, ch)
	return &world.Level{Chunks: []world.Chunk{*ch}}
}

// rayBox is a 1-unit cube entity centred at pos.
func rayBox(id string, pos mgl32.Vec3) world.Entity {
	return world.Entity{
		ID:         id,
		Position:   pos,
		Scale:      mgl32.Vec3{1, 1, 1},
		BoundHalfX: 0.5, BoundHalfY: 0.5, BoundHalfZ: 0.5,
		Visible: true,
	}
}

// eye is the middle of the room, looking down +X at the far wall.
var (
	eye     = mgl32.Vec3{1, 2, 5}
	forward = mgl32.Vec3{1, 0, 0}
)

func hitIDs(res RayResult) []string {
	out := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		id := h.ID
		if id == "" {
			id = h.Sector
		}
		out = append(out, id)
	}
	return out
}

func TestRaycastFindsNearestEntity(t *testing.T) {
	lvl := rayRoom(t)
	lvl.Entities = []world.Entity{
		rayBox("far", mgl32.Vec3{8, 2, 5}),
		rayBox("near", mgl32.Vec3{4, 2, 5}),
	}

	res := Raycast(lvl, eye, forward, DefaultRayFilter(100))
	h, ok := res.First()
	if !ok {
		t.Fatalf("nothing hit; got %v", hitIDs(res))
	}
	if h.ID != "near" {
		t.Errorf("first hit = %q, want the nearer entity", h.ID)
	}
	// The near face of a 1-unit cube centred at x=4, from x=1.
	if want := float32(2.5); h.T < want-1e-3 || h.T > want+1e-3 {
		t.Errorf("t = %v, want %v", h.T, want)
	}
	if !vecNear(h.Point, mgl32.Vec3{3.5, 2, 5}, 1e-3) {
		t.Errorf("point = %v", h.Point)
	}
}

func TestRaycastReportsEveryHitInTheLineOfFire(t *testing.T) {
	// Bodies do not stop the ray — that is what makes the "all" mode able to
	// answer a question the first-hit mode cannot.
	lvl := rayRoom(t)
	lvl.Entities = []world.Entity{
		rayBox("far", mgl32.Vec3{8, 2, 5}),
		rayBox("near", mgl32.Vec3{4, 2, 5}),
	}

	res := Raycast(lvl, eye, forward, DefaultRayFilter(100))
	if len(res.Hits) != 3 {
		t.Fatalf("hits = %v, want both entities and the far wall", hitIDs(res))
	}
	if res.Hits[0].ID != "near" || res.Hits[1].ID != "far" {
		t.Errorf("order = %v, want nearest first", hitIDs(res))
	}
	if res.Hits[2].Kind != RayHitWall {
		t.Errorf("last hit = %v, want the wall the ray stopped on", res.Hits[2].Kind)
	}
}

func TestRaycastStopsAtGeometry(t *testing.T) {
	// An entity outside the room is behind a wall, and a shot does not reach it.
	lvl := rayRoom(t)
	lvl.Entities = []world.Entity{rayBox("outside", mgl32.Vec3{14, 2, 5})}

	res := Raycast(lvl, eye, forward, DefaultRayFilter(100))
	if !res.Blocked {
		t.Fatal("the ray was not stopped by the wall")
	}
	for _, h := range res.Hits {
		if h.ID == "outside" {
			t.Error("hit an entity through a wall")
		}
	}
	if !vecNear(res.Stop, mgl32.Vec3{10, 2, 5}, 1e-3) {
		t.Errorf("stop = %v, want the far wall at x=10", res.Stop)
	}
}

func TestRaycastWithoutGeometrySeesThroughWalls(t *testing.T) {
	lvl := rayRoom(t)
	lvl.Entities = []world.Entity{rayBox("outside", mgl32.Vec3{14, 2, 5})}

	f := DefaultRayFilter(100)
	f.Geometry = false
	res := Raycast(lvl, eye, forward, f)
	if h, ok := res.First(); !ok || h.ID != "outside" {
		t.Fatalf("hits = %v, want the entity beyond the wall", hitIDs(res))
	}
	if res.Blocked {
		t.Error("the cast reported being blocked with geometry switched off")
	}
	if !vecNear(res.Stop, eye.Add(forward.Mul(100)), 1e-2) {
		t.Errorf("stop = %v, want the far end of the range", res.Stop)
	}
}

func TestRaycastRangeIsADistanceInWorldUnits(t *testing.T) {
	lvl := rayRoom(t)
	lvl.Entities = []world.Entity{rayBox("near", mgl32.Vec3{4, 2, 5})}

	// The entity's near face is 2.5 away. A range either side of that decides it.
	if res := Raycast(lvl, eye, forward, DefaultRayFilter(2)); len(res.Hits) != 0 {
		t.Errorf("range 2 hit %v, want nothing", hitIDs(res))
	}
	res := Raycast(lvl, eye, forward, DefaultRayFilter(3))
	if h, ok := res.First(); !ok || h.ID != "near" {
		t.Errorf("range 3 hit %v, want the entity", hitIDs(res))
	}

	// An unnormalised direction must not change what "3 units" means.
	long := Raycast(lvl, eye, forward.Mul(50), DefaultRayFilter(3))
	if h, ok := long.First(); !ok || h.ID != "near" || long.Hits[0].T != res.Hits[0].T {
		t.Errorf("a long direction vector changed the answer: %v", hitIDs(long))
	}
}

func TestRaycastVisibleOnlySkipsHiddenEntitiesButNotTheirBlockers(t *testing.T) {
	lvl := rayRoom(t)
	hidden := rayBox("ghost", mgl32.Vec3{4, 2, 5})
	hidden.Visible = false
	lvl.Entities = []world.Entity{hidden}

	f := DefaultRayFilter(100)
	f.VisibleOnly = true
	res := Raycast(lvl, eye, forward, f)
	for _, h := range res.Hits {
		if h.ID == "ghost" {
			t.Error("visible_only reported an entity that is not drawn")
		}
	}
	// The wall behind it is still there, and still stops the ray.
	if !res.Blocked {
		t.Error("visible_only changed what stopped the ray")
	}
}

func TestRaycastTagFiltersReportingNotBlocking(t *testing.T) {
	lvl := rayRoom(t)
	tagged := rayBox("target", mgl32.Vec3{4, 2, 5})
	tagged.Tags = []string{"enemy"}
	lvl.Entities = []world.Entity{tagged, rayBox("crate", mgl32.Vec3{3, 2, 5})}

	f := DefaultRayFilter(100)
	f.Tag = "enemy"
	res := Raycast(lvl, eye, forward, f)
	if len(res.Hits) != 1 || res.Hits[0].ID != "target" {
		t.Fatalf("hits = %v, want only the tagged entity", hitIDs(res))
	}
	// The untagged wall was still what ended the ray, so a tagged entity
	// outside the room is not suddenly reachable.
	if !res.Blocked {
		t.Error("a tag filter let the ray through the wall")
	}
}

func TestRaycastIgnorePassesStraightThrough(t *testing.T) {
	lvl := rayRoom(t)
	lvl.Entities = []world.Entity{
		rayBox("shield", mgl32.Vec3{3, 2, 5}),
		rayBox("target", mgl32.Vec3{5, 2, 5}),
	}

	f := DefaultRayFilter(100)
	f.Ignore = map[string]bool{"shield": true}
	res := Raycast(lvl, eye, forward, f)
	if h, ok := res.First(); !ok || h.ID != "target" {
		t.Fatalf("hits = %v, want the ignored entity skipped", hitIDs(res))
	}
}

func TestRaycastHitsFloorAndCeiling(t *testing.T) {
	lvl := rayRoom(t)

	down := Raycast(lvl, mgl32.Vec3{5, 2, 5}, mgl32.Vec3{0, -1, 0}, DefaultRayFilter(100))
	h, ok := down.First()
	if !ok || h.Kind != RayHitFloor || h.Sector != "room" {
		t.Fatalf("looking down hit %v, want the floor of room", hitIDs(down))
	}
	if h.Chunk != "room_chunk" {
		t.Errorf("floor hit named chunk %q", h.Chunk)
	}
	if !vecNear(h.Point, mgl32.Vec3{5, 0, 5}, 1e-3) {
		t.Errorf("floor point = %v", h.Point)
	}

	up := Raycast(lvl, mgl32.Vec3{5, 2, 5}, mgl32.Vec3{0, 1, 0}, DefaultRayFilter(100))
	if h, ok := up.First(); !ok || h.Kind != RayHitCeiling {
		t.Fatalf("looking up hit %v, want the ceiling", hitIDs(up))
	}
}

func TestRaycastNamesTheWallEdgeAScriptAddresses(t *testing.T) {
	lvl := rayRoom(t)
	res := Raycast(lvl, eye, forward, DefaultRayFilter(100))
	h, ok := res.First()
	if !ok || h.Kind != RayHitWall {
		t.Fatalf("hits = %v, want a wall", hitIDs(res))
	}
	s := &lvl.Chunks[0].Sectors[0]
	if wallByEdgeID(s, h.ID) == nil {
		t.Errorf("wall hit reported ID %q, which is no edge of the sector", h.ID)
	}
}

func TestRaycastVolumesAreOptIn(t *testing.T) {
	lvl := rayRoom(t)
	lvl.Volumes = []world.Volume{{
		ID:       "trigger",
		Position: mgl32.Vec3{4, 2, 5},
		HalfX:    1, HalfY: 1, HalfZ: 1,
		Enabled: true,
	}}

	if res := Raycast(lvl, eye, forward, DefaultRayFilter(100)); len(res.Hits) != 1 {
		t.Errorf("hits = %v, want the wall alone — volumes are off by default", hitIDs(res))
	}

	f := DefaultRayFilter(100)
	f.Volumes = true
	res := Raycast(lvl, eye, forward, f)
	if h, ok := res.First(); !ok || h.ID != "trigger" {
		t.Fatalf("hits = %v, want the volume", hitIDs(res))
	}

	// A disabled volume contains nothing, so it is not there to be hit either.
	lvl.Volumes[0].Enabled = false
	if res := Raycast(lvl, eye, forward, f); len(res.Hits) != 1 {
		t.Errorf("hits = %v, want a disabled volume skipped", hitIDs(res))
	}
}

func TestRaycastReadsTheThreeBoundingKinds(t *testing.T) {
	// The half-extents are the box the shape is inscribed in — the same reading
	// world.ShapeContainsPoint gives — so the three kinds agree dead centre and
	// differ exactly where the inscribed shape pulls away from the box.
	//
	// A cube of half-extent 0.5 centred at x=4, shot along +X from x=1:
	// centre is 2.5 away for all three. Offset 0.4 towards a face, the box is
	// still flat at 2.5, while a round cross-section has receded to
	// 4 - 0.5·sqrt(1-0.8²) = 3.7. A cylinder is round in XZ and flat in Y, so
	// it recedes on the Z offset and not on the Y one — which is the whole
	// difference between it and a sphere.
	lvl := rayRoom(t)
	const flat, round = float32(2.5), float32(2.7)
	for _, tc := range []struct {
		kind             world.BoundingType
		name             string
		offsetZ, offsetY float32
	}{
		{world.BoundingAABB, "aabb", flat, flat},
		{world.BoundingSphere, "sphere", round, round},
		{world.BoundingCylinder, "cylinder", round, flat},
	} {
		e := rayBox("subject", mgl32.Vec3{4, 2, 5})
		e.BoundingKind = tc.kind
		lvl.Entities = []world.Entity{e}

		castT := func(from mgl32.Vec3) (float32, bool) {
			for _, h := range Raycast(lvl, from, forward, DefaultRayFilter(100)).Hits {
				if h.ID == "subject" {
					return h.T, true
				}
			}
			return 0, false
		}
		for _, c := range []struct {
			what string
			from mgl32.Vec3
			want float32
		}{
			{"centre", mgl32.Vec3{1, 2, 5}, flat},
			{"offset in z", mgl32.Vec3{1, 2, 5.4}, tc.offsetZ},
			{"offset in y", mgl32.Vec3{1, 2.4, 5}, tc.offsetY},
		} {
			got, ok := castT(c.from)
			if !ok {
				t.Errorf("%s: %s ray missed", tc.name, c.what)
				continue
			}
			if got < c.want-1e-3 || got > c.want+1e-3 {
				t.Errorf("%s: %s t = %v, want %v", tc.name, c.what, got, c.want)
			}
		}

		// Past the extents in every reading, so all three miss.
		if _, ok := castT(mgl32.Vec3{1, 2, 5.6}); ok {
			t.Errorf("%s: a ray outside the extents still hit", tc.name)
		}
	}
}

func TestRaycastRotatedBoundsRotateWithTheEntity(t *testing.T) {
	lvl := rayRoom(t)
	// A slab 6 long in X and 0.2 thin in Z, set off to one side of the ray.
	// Unrotated its thin face is nowhere near; turned a quarter turn its length
	// lies across the ray instead.
	e := rayBox("slab", mgl32.Vec3{4, 2, 7.5})
	e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = 3, 0.5, 0.1
	lvl.Entities = []world.Entity{e}

	for _, h := range Raycast(lvl, eye, forward, DefaultRayFilter(100)).Hits {
		if h.ID == "slab" {
			t.Fatal("unrotated slab was hit; the test no longer tests anything")
		}
	}

	lvl.Entities[0].Rotation = mgl32.Vec3{0, math.Pi / 2, 0}
	res := Raycast(lvl, eye, forward, DefaultRayFilter(100))
	h, ok := res.First()
	if !ok || h.ID != "slab" {
		t.Fatalf("rotated slab was missed: %v", hitIDs(res))
	}
	if want := float32(2.9); h.T < want-1e-3 || h.T > want+1e-3 {
		t.Errorf("t = %v, want %v — the slab's near face after the turn", h.T, want)
	}
}

func TestRaycastSceneryBlocksButIsNotReported(t *testing.T) {
	// Nothing in the scripting language addresses a scenery placement, so a
	// prop can end a shot and still have nothing to say about it.
	lvl := rayRoom(t)
	lvl.Entities = []world.Entity{rayBox("behind_the_rock", mgl32.Vec3{6, 2, 5})}
	lvl.Scenery = []world.Scenery{{
		ID:       "rock",
		Position: mgl32.Vec3{4, 2, 5},
		Scale:    mgl32.Vec3{1, 1, 1},
		Solid:    true,
		LocalMin: mgl32.Vec3{-0.5, -0.5, -0.5},
		LocalMax: mgl32.Vec3{0.5, 0.5, 0.5},
	}}

	res := Raycast(lvl, eye, forward, DefaultRayFilter(100))
	if len(res.Hits) != 0 {
		t.Errorf("hits = %v, want nothing reported behind the prop", hitIDs(res))
	}
	if !res.Blocked || !vecNear(res.Stop, mgl32.Vec3{3.5, 2, 5}, 1e-3) {
		t.Errorf("stop = %v (blocked=%v), want the prop's near face", res.Stop, res.Blocked)
	}

	// Not solid: the ray goes through it and finds what is behind.
	lvl.Scenery[0].Solid = false
	if h, ok := Raycast(lvl, eye, forward, DefaultRayFilter(100)).First(); !ok || h.ID != "behind_the_rock" {
		t.Error("a prop that is not solid stopped the ray")
	}
}

func TestRaycastSkipsDisabledChunks(t *testing.T) {
	// A released chunk has no vertex buffers and is not drawn, so there is
	// nothing along the ray to stop it. See world/chunk_enable.go.
	lvl := rayRoom(t)
	lvl.Chunks[0].Disabled = true

	res := Raycast(lvl, eye, forward, DefaultRayFilter(100))
	if res.Blocked || len(res.Hits) != 0 {
		t.Errorf("a disabled chunk was hit: %v", hitIDs(res))
	}
}

func TestRaycastDegenerateInputsCastNothing(t *testing.T) {
	lvl := rayRoom(t)
	for _, tc := range []struct {
		name string
		lvl  *world.Level
		dir  mgl32.Vec3
		rng  float32
	}{
		{"no level", nil, forward, 100},
		{"zero direction", lvl, mgl32.Vec3{}, 100},
		{"zero range", lvl, forward, 0},
		{"negative range", lvl, forward, -5},
	} {
		res := Raycast(tc.lvl, eye, tc.dir, DefaultRayFilter(tc.rng))
		if len(res.Hits) != 0 || res.Blocked {
			t.Errorf("%s: cast something: %v", tc.name, hitIDs(res))
		}
	}
}
