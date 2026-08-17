package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// squareChunk builds a chunk equivalent to one just loaded from disk: four
// named points forming a square, wired into a single sector.
func squareChunk() Chunk {
	return Chunk{
		ID:         "main",
		Scale:      1,
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: []string{"A", "E0", "E1", "E2"},
		Sectors: []Sector{{
			ID:           "room",
			PointIndices: []int{0, 1, 2, 3},
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Walls:        make([]Wall, 4),
		}},
	}
}

// ring returns the sector's edge ring as point names, and verifies the edges
// chain head-to-tail.
func ring(t *testing.T, rc RawChunk, sectorIdx int) []string {
	t.Helper()
	edges := rc.Sectors[sectorIdx].Edges
	names := make([]string, len(edges))
	for i, e := range edges {
		names[i] = e.From
		next := edges[(i+1)%len(edges)]
		if e.To != next.From {
			t.Fatalf("edge %d ends at %q but edge %d starts at %q", i, e.To, (i+1)%len(edges), next.From)
		}
	}
	return names
}

// Moving a vertex must not cause the save path to mint a name that is already
// taken by a different point. Before the index-based naming fix, dragging a
// single vertex produced the ring [A E0 E1 E0] — the corner at (10,0) was
// silently teleported onto the dragged point's position.
func TestSaveDoesNotReuseNameAfterVertexMove(t *testing.T) {
	ch := squareChunk()

	// Editor drags point 3 ("E2") from (0,10) to (0,20); PointNames is
	// index-aligned, so the point keeps its name.
	ch.Points[3] = mgl32.Vec2{0, 20}

	rc, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRawChunk(&rc); err != nil {
		t.Fatalf("saved chunk failed validation: %v", err)
	}

	got := ring(t, rc, 0)
	want := []string{"A", "E0", "E1", "E2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("edge ring = %v, want %v", got, want)
		}
	}

	// Every ring position must resolve to the point actually held in the pool.
	for i, name := range got {
		p, ok := rc.Points[name]
		if !ok {
			t.Fatalf("ring references undefined point %q", name)
		}
		want := ch.Points[i]
		if p.X != want.X() || p.Y != want.Y() {
			t.Errorf("point %q = (%v, %v), want (%v, %v)", name, p.X, p.Y, want.X(), want.Y())
		}
	}
}

// A point the editor added carries no name; the save path must give it one that
// does not collide with a name already in the pool.
func TestSaveNamesNewPointsWithoutCollision(t *testing.T) {
	ch := squareChunk()

	// Shift+click adds an unnamed point and splices it into the sector ring.
	idx := ch.AddPoint(mgl32.Vec2{5, -5})
	if idx != 4 {
		t.Fatalf("AddPoint returned index %d, want 4", idx)
	}
	if len(ch.PointNames) != len(ch.Points) {
		t.Fatalf("PointNames (%d) out of step with Points (%d)", len(ch.PointNames), len(ch.Points))
	}
	ch.Sectors[0].PointIndices = []int{0, 4, 1, 2, 3}
	ch.Sectors[0].EdgeKinds = []string{"wall", "wall", "wall", "wall", "wall"}
	ch.Sectors[0].Walls = make([]Wall, 5)

	rc, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRawChunk(&rc); err != nil {
		t.Fatalf("saved chunk failed validation: %v", err)
	}

	if len(rc.Points) != 5 {
		t.Fatalf("wrote %d points, want 5 (one per live pool entry)", len(rc.Points))
	}
	// The pre-existing names must survive untouched.
	for name, want := range map[string]mgl32.Vec2{
		"A": {0, 0}, "E0": {10, 0}, "E1": {10, 10}, "E2": {0, 10},
	} {
		p, ok := rc.Points[name]
		if !ok {
			t.Fatalf("original point %q missing from saved chunk", name)
		}
		if p.X != want.X() || p.Y != want.Y() {
			t.Errorf("point %q moved to (%v, %v), want (%v, %v)", name, p.X, p.Y, want.X(), want.Y())
		}
	}
	// The new point got some other name, at the right place.
	r := ring(t, rc, 0)
	newName := r[1]
	if newName == "A" || newName == "E0" || newName == "E1" || newName == "E2" {
		t.Fatalf("new point reused existing name %q", newName)
	}
	if p := rc.Points[newName]; p.X != 5 || p.Y != -5 {
		t.Errorf("new point %q = (%v, %v), want (5, -5)", newName, p.X, p.Y)
	}
}

// Deleting a point must keep names aligned with the remaining pool entries.
func TestRemovePointKeepsNamesAligned(t *testing.T) {
	ch := squareChunk()
	ch.RemovePoint(1) // drop "E0"

	if len(ch.PointNames) != len(ch.Points) {
		t.Fatalf("PointNames (%d) out of step with Points (%d)", len(ch.PointNames), len(ch.Points))
	}
	want := []string{"A", "E1", "E2"}
	for i := range want {
		if ch.PointNames[i] != want[i] {
			t.Fatalf("PointNames = %v, want %v", ch.PointNames, want)
		}
	}
	if ch.Points[1] != (mgl32.Vec2{10, 10}) {
		t.Errorf("point 1 = %v, want (10, 10)", ch.Points[1])
	}
}

// Repeated save/edit cycles must stay stable rather than accumulating orphaned
// points, which is how the corruption compounded across sessions.
func TestSaveIsStableAcrossRepeatedEdits(t *testing.T) {
	ch := squareChunk()
	for i := 0; i < 5; i++ {
		ch.Points[i%4] = mgl32.Vec2{float32(i), float32(i * 2)}
		rc, err := chunkToRaw(&ch)
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		if err := validateRawChunk(&rc); err != nil {
			t.Fatalf("cycle %d failed validation: %v", i, err)
		}
		if len(rc.Points) != 4 {
			t.Fatalf("cycle %d wrote %d points, want 4 — orphans are accumulating", i, len(rc.Points))
		}
	}
}

func TestValidateRawChunkRejectsRepeatedPoint(t *testing.T) {
	rc := RawChunk{
		ID:     "main",
		Points: map[string]Point{"A": {}, "B": {}, "C": {}},
		Sectors: []RawSector{{
			ID: "room",
			Edges: []RawEdge{
				{From: "A", To: "B"}, {From: "B", To: "A"},
				{From: "A", To: "C"}, {From: "C", To: "A"},
			},
		}},
	}
	if err := validateRawChunk(&rc); err == nil {
		t.Fatal("expected a repeated-point error, got nil")
	}
}

func TestValidateRawChunkRejectsBrokenRing(t *testing.T) {
	rc := RawChunk{
		ID:     "main",
		Points: map[string]Point{"A": {}, "B": {}, "C": {}},
		Sectors: []RawSector{{
			ID: "room",
			Edges: []RawEdge{
				{From: "A", To: "B"}, {From: "B", To: "C"}, {From: "C", To: "B"},
			},
		}},
	}
	if err := validateRawChunk(&rc); err == nil {
		t.Fatal("expected a broken-ring error, got nil")
	}
}

func TestValidateRawChunkRejectsUndefinedPoint(t *testing.T) {
	rc := RawChunk{
		ID:     "main",
		Points: map[string]Point{"A": {}, "B": {}},
		Sectors: []RawSector{{
			ID: "room",
			Edges: []RawEdge{
				{From: "A", To: "B"}, {From: "B", To: "Z"}, {From: "Z", To: "A"},
			},
		}},
	}
	if err := validateRawChunk(&rc); err == nil {
		t.Fatal("expected an undefined-point error, got nil")
	}
}

func TestValidateRawChunkAcceptsWellFormed(t *testing.T) {
	ch := squareChunk()
	rc, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRawChunk(&rc); err != nil {
		t.Fatalf("well-formed chunk rejected: %v", err)
	}
}

// sceneryPrimToRaw used to omit Smooth/SmoothAngle entirely (it built its
// own RawPrimitive literal by hand instead of sharing primitiveToRawCommon
// with the level editor's primitiveToRaw), so toggling smooth shading on a
// scenery primitive was silently lost on save. Covers both the save side
// (sceneryPrimToRaw) and the load side (rawToSceneryPrimitive), which had
// the same field missing.
func TestSceneryPrimitiveRoundTripsSmoothing(t *testing.T) {
	p := Primitive{
		ID:          "block",
		Kind:        PrimitiveRectPrism,
		Position:    mgl32.Vec3{1, 2, 3},
		Scale:       mgl32.Vec3{1, 1, 1},
		Smooth:      true,
		SmoothAngle: 42,
	}
	rp := sceneryPrimToRaw(&p)
	if !rp.Smooth {
		t.Fatal("sceneryPrimToRaw dropped Smooth=true")
	}
	if rp.SmoothAngle != 42 {
		t.Fatalf("sceneryPrimToRaw dropped SmoothAngle: got %v, want 42", rp.SmoothAngle)
	}

	back := rawToSceneryPrimitive(rp)
	if !back.Smooth {
		t.Fatal("rawToSceneryPrimitive dropped Smooth=true")
	}
	if back.SmoothAngle != 42 {
		t.Fatalf("rawToSceneryPrimitive dropped SmoothAngle: got %v, want 42", back.SmoothAngle)
	}
}
