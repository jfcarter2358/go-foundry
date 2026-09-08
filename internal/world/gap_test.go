package world

// gap_test.go — portal gap walls as a moving floor crosses its neighbour's.
//
// The riser between two sectors is the one piece of geometry neither sector
// owns, so it is rebuilt by the chunk (RebuildGaps) rather than by Sector.Load.
// A script animating a lift drives both halves of that every frame: the gap
// appearing as the floor drops, and going away again as it comes back.

import (
	"testing"

	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

// liftChunk is a "lift" sector (x 0..10) beside a "room" (x 10..20) sharing the
// edge at x=10, the lift's ceiling lower than the room's. Both floors start
// level, so the only gap the chunk has to begin with is the ceiling filler on
// the room's side.
func liftChunk() *Chunk { return liftChunkAt(0, 3) }

// liftChunkAt is liftChunk with the lift's own floor and ceiling chosen, so a
// test can stand the chunk up in the pose it is about, rather than inferring
// portals in one pose and then moving the sectors under them — inference never
// demotes a portal back to a wall, so the starting pose is the one that decides.
func liftChunkAt(liftFloor, liftCeil float32) *Chunk {
	ch := &Chunk{
		ID:         "block",
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {20, 0}, {20, 10}},
		PointNames: make([]string, 6),
	}
	mk := func(id string, idx []int, floorY, ceilY float32) Sector {
		n := len(idx)
		s := Sector{
			ID:           id,
			PointIndices: idx,
			EdgeKinds:    make([]string, n),
			Floor:        Floor{Enabled: true, Height: floorY, Texture: "t", Alpha: 1, VertexOffsets: make([]float32, n)},
			Ceiling:      Ceiling{Enabled: true, Height: ceilY, Texture: "t", Alpha: 1, VertexOffsets: make([]float32, n)},
		}
		for i := 0; i < n; i++ {
			s.EdgeKinds[i] = "wall"
			s.Walls = append(s.Walls, Wall{
				StartPoint:  ch.Points[idx[i]],
				EndPoint:    ch.Points[idx[(i+1)%n]],
				Texture:     "t",
				Visible:     true,
				DoubleSided: true,
				Alpha:       1,
			})
		}
		return s
	}
	ch.Sectors = []Sector{
		mk("lift", []int{0, 1, 2, 3}, liftFloor, liftCeil),
		mk("room", []int{1, 4, 5, 2}, 0, 6),
	}
	ch.InferPortalsAndReload()
	return ch
}

// gapOf returns the size and alpha of both gap meshes on the portal leading out
// of the named sector. A zero size is a gap that is not drawn at all; a zero
// alpha is one drawn fully transparent, which looks exactly the same on screen
// and is what the second test is here to tell apart.
func gapOf(t *testing.T, ch *Chunk, sector string) (floorN int, floorA float32, ceilN int, ceilA float32) {
	t.Helper()
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if s.ID != sector || len(s.Portals) == 0 {
			continue
		}
		p := &s.Portals[0]
		return len(p.FloorGap.Vertices), p.FloorGap.Alpha, len(p.CeilGap.Vertices), p.CeilGap.Alpha
	}
	t.Fatalf("sector %q has no portal", sector)
	return 0, 0, 0, 0
}

// The lift's floor drops below the room's and comes back, exactly as
// DrainScriptWork applies a script's writes: reload the sector it changed, then
// rebuild the chunk's gaps.
func TestGapFollowsAnAnimatedFloor(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := liftChunk()

	if n, _, _, _ := gapOf(t, ch, "lift"); n != 0 {
		t.Errorf("floors are level, but the lift already has %d floor-gap floats", n)
	}
	if _, _, n, _ := gapOf(t, ch, "room"); n == 0 {
		t.Error("the room's ceiling is higher than the lift's, but no ceiling filler was built")
	}

	drop := func(dy float32) {
		ch.Sectors[0].Floor.Height += dy
		if err := ReloadSector(ch, &ch.Sectors[0]); err != nil {
			t.Fatal(err)
		}
		ch.RebuildGaps()
	}

	drop(-3)
	n, alpha, _, _ := gapOf(t, ch, "lift")
	if n == 0 {
		t.Fatal("the lift's floor is 3 below the room's, but no riser was built")
	}
	if alpha != 1 {
		t.Errorf("the riser is drawn at alpha %v; a gap nothing set an alpha on must be opaque", alpha)
	}
	// The ceiling filler on the far side is untouched by a floor moving.
	if _, _, cn, ca := gapOf(t, ch, "room"); cn == 0 || ca != 1 {
		t.Errorf("the room's ceiling filler came back as %d floats at alpha %v", cn, ca)
	}

	drop(3)
	if n, _, _, _ := gapOf(t, ch, "lift"); n != 0 {
		t.Errorf("the floors are level again, but a %d-float riser is still drawn", n)
	}
}

// A sector reloaded on its own — the properties panel, and every editor
// operation that touches one sector — must not leave the gaps it did not
// rebuild invisible. ReloadSector propagates the portal's alpha onto its gap
// meshes, so a portal carrying the zero value used to blank them.
func TestReloadSectorKeepsGapsOpaque(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := liftChunk()
	ch.Sectors[0].Floor.Height = -3
	if err := ReloadSector(ch, &ch.Sectors[0]); err != nil {
		t.Fatal(err)
	}
	ch.RebuildGaps()

	// Reload both sides again with no gap rebuild after it.
	for si := range ch.Sectors {
		if err := ReloadSector(ch, &ch.Sectors[si]); err != nil {
			t.Fatal(err)
		}
	}
	if n, a, _, _ := gapOf(t, ch, "lift"); n == 0 || a != 1 {
		t.Errorf("after a bare reload the riser is %d floats at alpha %v, want it still drawn and opaque", n, a)
	}
	if _, _, n, a := gapOf(t, ch, "room"); n == 0 || a != 1 {
		t.Errorf("after a bare reload the ceiling filler is %d floats at alpha %v", n, a)
	}
}

// A door built as a sector's own moving surface collapses to zero height when
// it shuts, and inverts if the leaf travels past the surface it seats against.
// Portal inference must keep calling the neighbouring rooms its neighbours
// through all of it: a doorway that stops being a portal target is a doorway
// whose leaf — which IS the gap geometry — is not drawn at all, and the level
// reloads that way every time because it is the saved heights that say so.
func TestAShutDoorSectorStaysAPortalNeighbour(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	// "lift" stands in for the doorway here: its ceiling is fixed and its floor
	// is the leaf, driven from below the ceiling to above it.
	for _, tc := range []struct {
		name        string
		floor, ceil float32
	}{
		{"open, the leaf stowed below", 0, 3},
		{"shut, collapsed to nothing", 3, 3},
		{"driven past the stop, inverted", 4, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := liftChunkAt(tc.floor, tc.ceil)
			// The target the neighbour names goes stale exactly as a rename
			// leaves it; re-inference is what is meant to repair it.
			for pi := range ch.Sectors[1].Portals {
				ch.Sectors[1].Portals[pi].ToSector = "renamed_away"
			}
			ch.InferPortalsAndReload()

			if got := ch.Sectors[1].Portals[0].ToSector; got != "lift" {
				t.Fatalf("the room's portal still leads to %q; inference did not repair it", got)
			}
			if n, _, _, c := gapOf(t, ch, "room"); n == 0 && c == 0 {
				t.Error("the doorway has no gap geometry on the room's side, so the door does not draw")
			}
		})
	}
}

// The rule the case above relaxes still has to hold: a storey stacked on
// another meets it exactly at the slab and has no way through, so a shared edge
// between them is a wall on both sides and not a portal.
func TestStackedStoreysAreNotPortalNeighbours(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	// The "lift" starts as the storey above the room: its floor at the room's
	// ceiling, which is where a first storey's slab sits.
	ch := liftChunkAt(6, 9)

	if len(ch.Sectors[1].Portals) != 0 {
		t.Errorf("the room grew %d portal(s) into the storey above it", len(ch.Sectors[1].Portals))
	}
}
