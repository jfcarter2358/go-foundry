package render

// opening_culling_test.go — portal culling has to walk through a hole in a floor.
//
// Portal culling only ever *removes* sectors the frustum already accepted, and
// it removes anything reachable through the portal graph but not currently
// facing the camera. Two stacked sectors joined only by a stairwell are not in
// that graph at all unless openings are edges of it — so before this, standing
// on a mezzanine and looking down the hole showed nothing underneath.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// towerChunk is a hall at y=0 under a mezzanine at y=6 sharing one footprint,
// with a stairwell cut in the mezzanine's floor. The two share no edge, so the
// hole is their only connection.
func towerChunk(withHole bool) *world.Chunk {
	ch := &world.Chunk{
		ID:         "tower",
		Scale:      1,
		Points:     []mgl32.Vec2{{0, 0}, {20, 0}, {20, 20}, {0, 20}},
		PointNames: make([]string, 4),
	}
	mk := func(id string, layer int, floorY, ceilY float32) world.Sector {
		return world.Sector{
			ID:           id,
			Layer:        layer,
			PointIndices: []int{0, 1, 2, 3},
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        world.Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: true, Height: ceilY, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{20, 20},
		}
	}
	hall := mk("hall", 0, 0, 5)
	mezz := mk("mezz", 1, 6, 12)
	if withHole {
		mezz.Floor.Openings = []world.Opening{{
			ID:     "stairwell",
			Points: []mgl32.Vec2{{8, 8}, {12, 8}, {12, 12}, {8, 12}},
		}}
	}
	ch.Sectors = []world.Sector{hall, mezz}
	ch.RecomputeBounds()
	return ch
}

func TestAnOpeningConnectsTwoSectorsInThePortalGraph(t *testing.T) {
	ch := towerChunk(true)

	// From the mezzanine, down through the hole.
	got := PortalConnectedSectors(ch, "mezz")
	if !got["hall"] {
		t.Error("the hall under the stairwell is not reachable from the mezzanine")
	}
	if !got["mezz"] {
		t.Error("the starting sector is missing from its own reachable set")
	}

	// Visibility uses the same graph, and an opening is traversed whichever way
	// the camera happens to point — a hole faces up and down at once.
	for _, cam := range []mgl32.Vec3{{10, 8, 10}, {2, 8, 2}, {10, 2, 10}} {
		if vis := PortalVisibleSectors(ch, "mezz", cam); !vis["hall"] {
			t.Errorf("camera at %v: the hall is culled away through the stairwell", cam)
		}
	}
}

// Without the hole the two are genuinely disconnected, which is what lets the
// caller fall back to frustum-only culling for them (see app.go). The opening
// must not be the thing that makes every stacked sector "connected".
func TestWithoutAnOpeningStackedSectorsStayDisconnected(t *testing.T) {
	ch := towerChunk(false)

	if got := PortalConnectedSectors(ch, "mezz"); got["hall"] {
		t.Error("the hall is reachable from the mezzanine with no opening between them")
	}
}

// A hole over nothing connects to nothing — OpeningTarget returns nil and the
// BFS must simply not add an edge, rather than adding an empty one.
func TestAnOpeningOverAVoidAddsNoEdge(t *testing.T) {
	ch := towerChunk(true)
	ch.Sectors = ch.Sectors[1:] // drop the hall

	got := PortalConnectedSectors(ch, "mezz")
	if len(got) != 1 || !got["mezz"] {
		t.Errorf("a hole over nothing produced the reachable set %v", got)
	}
}

// The mirror direction: a hole in a ceiling reaches the sector above it.
func TestACeilingOpeningReachesUpward(t *testing.T) {
	ch := towerChunk(false)
	ch.Sectors[0].Ceiling.Openings = []world.Opening{{
		ID:     "up",
		Points: []mgl32.Vec2{{8, 8}, {12, 8}, {12, 12}, {8, 12}},
	}}

	if got := PortalConnectedSectors(ch, "hall"); !got["mezz"] {
		t.Error("the mezzanine above is not reachable through a hole in the hall's ceiling")
	}
}

// ---------- one edge, two sectors ----------

// A tall sector's edge can front two sectors at once: a stairwell shaft running
// the full height of a building borders the ground-floor room at the bottom of
// its south edge and the upper room at the top. A portal records only ONE
// ToSector, so walking portals directly reached whichever of the two
// InferPortals happened to pick and left the other out of the visibility set —
// where it was culled away while plainly in view, blinking in and out as the
// camera turned.
func splitEdgeChunk() *world.Chunk {
	ch := &world.Chunk{
		ID:    "block",
		Scale: 1,
		// The rooms SHARE the shaft's east-edge points rather than duplicating
		// them: edges are matched by point index, so a coincident-but-separate
		// pair of points is not a shared edge and no portal forms at all.
		Points: []mgl32.Vec2{
			{0, 0}, {10, 0}, {10, 10}, {0, 10}, // shaft
			{20, 0}, {20, 10}, // the rooms' far side
		},
		PointNames: make([]string, 6),
	}
	mk := func(id string, idx []int, floorY, ceilY float32, ceilOn bool, lo, hi mgl32.Vec2) world.Sector {
		s := world.Sector{
			ID:           id,
			PointIndices: idx,
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        world.Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: ceilOn, Height: ceilY, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     lo, MaxBound: hi,
		}
		for i := range idx {
			s.Walls = append(s.Walls, world.Wall{
				StartPoint: ch.Points[idx[i]], EndPoint: ch.Points[idx[(i+1)%len(idx)]],
				Collision: true, Visible: true, DoubleSided: true, Alpha: 1,
			})
		}
		return s
	}
	lo, hi := mgl32.Vec2{0, 0}, mgl32.Vec2{10, 10}
	elo, ehi := mgl32.Vec2{10, 0}, mgl32.Vec2{20, 10}
	ch.Sectors = []world.Sector{
		// The shaft, open from the ground to the top of the upper storey.
		mk("shaft", []int{0, 1, 2, 3}, 0, 8.2, false, lo, hi),
		// Two rooms sharing the shaft's east edge, one above the other.
		mk("ground", []int{1, 4, 5, 2}, 0, 4.0, true, elo, ehi),
		mk("upper", []int{1, 4, 5, 2}, 4.2, 8.2, true, elo, ehi),
	}
	ch.Sectors[2].Layer = 1
	ch.InferPortals()
	ch.RecomputeBounds()
	return ch
}

func TestBothSectorsOnASharedEdgeStayVisible(t *testing.T) {
	ch := splitEdgeChunk()

	// From inside the shaft, both rooms across its east edge are in view.
	conn := PortalConnectedSectors(ch, "shaft")
	for _, id := range []string{"ground", "upper"} {
		if !conn[id] {
			t.Errorf("%q is not reachable from the shaft; a portal's single ToSector "+
				"named only one of the two rooms on that edge", id)
		}
	}

	// And it stays reachable whichever way the camera faces along that edge.
	for _, cam := range []mgl32.Vec3{{5, 1, 5}, {5, 6, 5}, {2, 6, 2}, {8, 1, 8}} {
		vis := PortalVisibleSectors(ch, "shaft", cam)
		for _, id := range []string{"ground", "upper"} {
			if conn[id] && !vis[id] {
				t.Errorf("camera at %v: %q is culled while the shaft's east edge faces it", cam, id)
			}
		}
	}
}

// The other direction: standing in the upper room, the shaft must be reachable,
// so what is across it does not vanish.
func TestTheShaftIsReachableFromTheUpperRoom(t *testing.T) {
	ch := splitEdgeChunk()
	if conn := PortalConnectedSectors(ch, "upper"); !conn["shaft"] {
		t.Error("the shaft is not reachable from the room above it")
	}
}

// The height check still holds: the two stacked rooms share every edge with
// each other and must not border one another.
//
// Asserted on the border graph rather than on reachability, because the two ARE
// reachable from each other — through the shaft, up which you can see and walk.
// What must not exist is a direct way through the floor between them.
func TestStackedRoomsOnTheSameEdgeDoNotBorder(t *testing.T) {
	ch := splitEdgeChunk()
	borders := ch.SectorBorderGraph()
	idx := map[string]int{}
	for i := range ch.Sectors {
		idx[ch.Sectors[i].ID] = i
	}
	for _, b := range borders[idx["ground"]] {
		if ch.Sectors[b.To].ID == "upper" {
			t.Error("the ground-floor room borders the storey directly above it")
		}
	}
	// The shaft, though, borders both — that is the whole point.
	var sawGround, sawUpper bool
	for _, b := range borders[idx["shaft"]] {
		switch ch.Sectors[b.To].ID {
		case "ground":
			sawGround = true
		case "upper":
			sawUpper = true
		}
	}
	if !sawGround || !sawUpper {
		t.Errorf("the shaft borders ground=%v upper=%v; it fronts both on one edge",
			sawGround, sawUpper)
	}
}
