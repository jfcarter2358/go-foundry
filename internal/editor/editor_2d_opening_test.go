package editor

// editor_2d_opening_test.go — drawing and removing a hole from the map.

import (
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// drawOpening runs the tool end to end: arm it, place corners, close it.
func drawOpening(ch *world.Chunk, pts ...mgl32.Vec2) {
	EditorBeginOpening()
	Editor.OpeningDrawPoints = append([]mgl32.Vec2(nil), pts...)
	finishOpeningDraw(ch)
}

func squareAround(cx, cz, half float32) []mgl32.Vec2 {
	return []mgl32.Vec2{
		{cx - half, cz - half}, {cx + half, cz - half},
		{cx + half, cz + half}, {cx - half, cz + half},
	}
}

func TestCuttingAFloorOpeningAttachesItToTheSectorUnderIt(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 1 // the ledge
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(5, 5, 1.5)...)

	ledge := &ch.Sectors[1]
	if len(ledge.Floor.Openings) != 1 {
		t.Fatalf("the ledge's floor has %d openings, want 1", len(ledge.Floor.Openings))
	}
	if len(ch.Sectors[0].Floor.Openings) != 0 {
		t.Error("the hole was also cut into the ground sector on the layer below")
	}
	if id := ledge.Floor.Openings[0].ID; id == "" {
		t.Error("the opening was created without an ID")
	}
	// It reads as open where it was drawn, and solid outside it.
	if !world.SurfaceIsOpenAt(ledge.Floor.Openings, mgl32.Vec2{5, 5}) {
		t.Error("the middle of the new hole does not read as open")
	}
	if world.SurfaceIsOpenAt(ledge.Floor.Openings, mgl32.Vec2{1, 1}) {
		t.Error("a point well outside the hole reads as open")
	}
}

// The floor/ceiling switch decides which surface is cut — the same switch that
// already decides which corner handles a click picks.
func TestTheSurfaceSwitchDecidesWhichSurfaceIsCut(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 0
	Editor.EditFloorVerts = false // ceiling

	drawOpening(ch, squareAround(5, 5, 1.5)...)

	ground := &ch.Sectors[0]
	if len(ground.Ceiling.Openings) != 1 {
		t.Errorf("the ceiling has %d openings, want 1", len(ground.Ceiling.Openings))
	}
	if len(ground.Floor.Openings) != 0 {
		t.Errorf("the floor was cut instead: %d openings", len(ground.Floor.Openings))
	}
}

// A hole cut on one layer must not appear on another — the tool honours the
// active layer exactly as picking does.
func TestOpeningsAreCutOnTheActiveLayerOnly(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.EditFloorVerts = true

	Editor.ActiveLayer = 0
	drawOpening(ch, squareAround(3, 3, 1)...)
	Editor.ActiveLayer = 1
	drawOpening(ch, squareAround(7, 7, 1)...)

	if n := len(ch.Sectors[0].Floor.Openings); n != 1 {
		t.Errorf("the ground layer ended with %d openings, want 1", n)
	}
	if n := len(ch.Sectors[1].Floor.Openings); n != 1 {
		t.Errorf("the ledge ended with %d openings, want 1", n)
	}
}

func TestAnOpeningNeedsThreeCorners(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 0
	Editor.EditFloorVerts = true

	drawOpening(ch, mgl32.Vec2{4, 4}, mgl32.Vec2{6, 4})

	if n := len(ch.Sectors[0].Floor.Openings); n != 0 {
		t.Errorf("a two-corner polygon produced %d openings", n)
	}
	if Editor.OpeningDrawing {
		t.Error("the tool is still armed after a rejected polygon")
	}
}

// Drawn where no sector on the active layer covers it, the hole has nothing to
// belong to and must be refused rather than silently dropped onto sector 0.
func TestAnOpeningOutsideEverySectorIsRefused(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 0
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(80, 80, 2)...)

	for si := range ch.Sectors {
		if n := len(ch.Sectors[si].Floor.Openings); n != 0 {
			t.Errorf("sector %q gained %d openings from a polygon drawn outside it",
				ch.Sectors[si].ID, n)
		}
	}
}

func TestRemovingAnOpeningFillsItBackIn(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(5, 5, 1.5)...)
	if len(ch.Sectors[1].Floor.Openings) != 1 {
		t.Fatal("setup: the hole was not cut")
	}

	if !openingAt(ch, 5, 5) {
		t.Error("openingAt does not see the hole that was just cut")
	}
	if !EditorDeleteOpeningAt(ch, 5, 5) {
		t.Fatal("removing the hole reported nothing to remove")
	}
	if n := len(ch.Sectors[1].Floor.Openings); n != 0 {
		t.Errorf("%d openings left after removing the only one", n)
	}
	if openingAt(ch, 5, 5) {
		t.Error("the surface still reads as open where the hole was filled in")
	}
	// A second removal has nothing to do and must say so.
	if EditorDeleteOpeningAt(ch, 5, 5) {
		t.Error("removing from a solid floor reported a removal")
	}
}

// Escape abandons the polygon without touching the level.
func TestCancellingLeavesNoTrace(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 0
	Editor.EditFloorVerts = true

	EditorBeginOpening()
	Editor.OpeningDrawPoints = squareAround(5, 5, 1.5)
	Editor.OpeningDrawing = false
	Editor.OpeningDrawPoints = nil

	for si := range ch.Sectors {
		if len(ch.Sectors[si].Floor.Openings) != 0 {
			t.Errorf("sector %q gained an opening from a cancelled draw", ch.Sectors[si].ID)
		}
	}
}

// Arming the opening tool must put the biome polygon tool away: both take a
// bare left click, and two armed at once would each consume it.
func TestArmingTheOpeningToolDisarmsTheBiomeTool(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	stackedTestChunk(t)

	Editor.BiomeDrawing = true
	Editor.BiomeDrawPoints = []mgl32.Vec2{{1, 1}, {2, 2}}
	EditorBeginOpening()

	if Editor.BiomeDrawing || Editor.BiomeDrawPoints != nil {
		t.Error("the biome polygon tool is still armed alongside the opening tool")
	}
	if !Editor.OpeningDrawing {
		t.Error("the opening tool did not arm")
	}
	Editor.OpeningDrawing = false
}

// ---------- moving an opening's corners ----------

func TestGrabbingAndDraggingAnOpeningCorner(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true
	Editor.SnapToGrid = false

	drawOpening(ch, squareAround(5, 5, 1.5)...)
	op := &ch.Sectors[1].Floor.Openings[0]
	corner := op.Points[0]

	if !openingGrabVertex(ch, corner.X(), corner.Y(), 0.5) {
		t.Fatal("clicking a corner did not grab it")
	}
	if !openingDraggingVertex() {
		t.Fatal("the drag did not arm")
	}
	if Editor.OpeningDragSector != 1 || Editor.OpeningDragIdx != 0 {
		t.Errorf("grabbed sector %d opening %d, want 1/0",
			Editor.OpeningDragSector, Editor.OpeningDragIdx)
	}
	if !Editor.OpeningDragFloor {
		t.Error("the drag latched the ceiling while the map is editing floors")
	}

	// Move it, the way the motion handler does.
	moved := mgl32.Vec2{corner.X() - 1, corner.Y() - 1}
	op.Points[Editor.OpeningDragVertex] = moved
	if got := ch.Sectors[1].Floor.Openings[0].Points[0]; got != moved {
		t.Errorf("the corner is at %v, want %v", got, moved)
	}

	openingEndDrag()
	if openingDraggingVertex() {
		t.Error("the drag is still armed after release")
	}
}

// A click nowhere near a corner must fall through, or the opening tool would
// swallow every click on a floor that happens to have a hole in it.
func TestAClickAwayFromEveryCornerFallsThrough(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(5, 5, 1.5)...)

	if openingGrabVertex(ch, 9, 9, 0.5) {
		t.Error("a click far from the hole grabbed one of its corners")
	}
	if openingDraggingVertex() {
		t.Error("a missed click armed a drag")
	}
}

// Corners on the surface the map is not editing are not grabbable, for the same
// reason their handles are not drawn: a floor hole and a ceiling hole over it
// coincide from above.
func TestOnlyTheEditedSurfacesCornersAreGrabbable(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 0

	Editor.EditFloorVerts = true
	drawOpening(ch, squareAround(5, 5, 1.5)...)
	corner := ch.Sectors[0].Floor.Openings[0].Points[0]

	// Flip to the ceiling: the floor's corner must now be out of reach.
	Editor.EditFloorVerts = false
	if openingGrabVertex(ch, corner.X(), corner.Y(), 0.5) {
		t.Error("a floor hole's corner was grabbed while editing the ceiling")
	}
}

// Corners on another layer are not grabbable either.
func TestCornersOnAnotherLayerAreNotGrabbable(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.EditFloorVerts = true

	Editor.ActiveLayer = 1
	drawOpening(ch, squareAround(5, 5, 1.5)...)
	corner := ch.Sectors[1].Floor.Openings[0].Points[0]

	Editor.ActiveLayer = 0
	if openingGrabVertex(ch, corner.X(), corner.Y(), 0.5) {
		t.Error("a corner on the ledge was grabbed while editing the ground layer")
	}
}

// The nearest corner wins, so two holes close together stay individually
// reachable.
func TestTheNearestCornerIsGrabbed(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := stackedTestChunk(t)
	Editor.ActiveLayer = 0
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(3, 3, 0.8)...)
	drawOpening(ch, squareAround(7, 7, 0.8)...)
	if n := len(ch.Sectors[0].Floor.Openings); n != 2 {
		t.Fatalf("setup: %d openings, want 2", n)
	}

	// Just outside the second hole's near corner.
	want := ch.Sectors[0].Floor.Openings[1].Points[0]
	if !openingGrabVertex(ch, want.X()+0.1, want.Y()+0.1, 2) {
		t.Fatal("nothing grabbed")
	}
	if Editor.OpeningDragIdx != 1 {
		t.Errorf("grabbed opening %d, want the nearer one (1)", Editor.OpeningDragIdx)
	}
	openingEndDrag()
}

// ---------- a hole goes through both faces of the slab ----------

// twoStoreyChunk is the shape the bug was reported against: a room with a
// second one built on top of it, both covering the same ground, with a slab
// between the lower room's ceiling and the upper room's floor.
func twoStoreyChunk(t *testing.T) *world.Chunk {
	t.Helper()
	ch := stackedTestChunk(t)
	ch.Sectors[0].Floor.Height, ch.Sectors[0].Ceiling.Height = 0, 4
	ch.Sectors[1].Floor.Height, ch.Sectors[1].Ceiling.Height = 4.2, 8.2
	ch.Sectors[0].Ceiling.Enabled = true
	ch.Sectors[1].Floor.Enabled = true
	return ch
}

// The bug: cutting a stairwell in the upper floor left the lower room's ceiling
// solid, and since opaque geometry draws with culling off it covered the hole
// from above as well as below.
func TestCuttingAStairwellAlsoCutsTheCeilingBelow(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(5, 5, 1.5)...)

	upper, lower := &ch.Sectors[1], &ch.Sectors[0]
	if len(upper.Floor.Openings) != 1 {
		t.Fatalf("the upper floor has %d openings, want 1", len(upper.Floor.Openings))
	}
	if len(lower.Ceiling.Openings) != 1 {
		t.Fatalf("the ceiling below was not cut: %d openings", len(lower.Ceiling.Openings))
	}
	// Both cover the same ground, or the hole is blocked from one side.
	for _, xz := range []mgl32.Vec2{{5, 5}, {4.2, 4.2}, {5.8, 5.8}} {
		if !world.SurfaceIsOpenAt(upper.Floor.Openings, xz) {
			t.Errorf("%v is not open in the upper floor", xz)
		}
		if !world.SurfaceIsOpenAt(lower.Ceiling.Openings, xz) {
			t.Errorf("%v is not open in the ceiling below", xz)
		}
	}
	// And they know about each other.
	if upper.Floor.Openings[0].Link != lower.Ceiling.Openings[0].ID {
		t.Errorf("the upper hole links to %q, want %q",
			upper.Floor.Openings[0].Link, lower.Ceiling.Openings[0].ID)
	}
	if lower.Ceiling.Openings[0].Link != upper.Floor.Openings[0].ID {
		t.Errorf("the lower hole links to %q, want %q",
			lower.Ceiling.Openings[0].Link, upper.Floor.Openings[0].ID)
	}
}

// The mirror: cutting the lower room's ceiling opens the floor above it.
func TestCuttingACeilingAlsoCutsTheFloorAbove(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)
	Editor.ActiveLayer = 0
	Editor.EditFloorVerts = false

	drawOpening(ch, squareAround(5, 5, 1.5)...)

	if n := len(ch.Sectors[0].Ceiling.Openings); n != 1 {
		t.Fatalf("the lower ceiling has %d openings, want 1", n)
	}
	if n := len(ch.Sectors[1].Floor.Openings); n != 1 {
		t.Errorf("the floor above was not cut: %d openings", n)
	}
}

func TestRemovingOneHalfOfASlabHoleRemovesBoth(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(5, 5, 1.5)...)
	if !EditorDeleteOpeningAt(ch, 5, 5) {
		t.Fatal("nothing removed")
	}
	if n := len(ch.Sectors[1].Floor.Openings); n != 0 {
		t.Errorf("%d openings left in the upper floor", n)
	}
	if n := len(ch.Sectors[0].Ceiling.Openings); n != 0 {
		t.Errorf("%d openings left in the ceiling below — the shaft is filled back in "+
			"from underneath while looking removed from above", n)
	}
}

func TestDraggingOneHalfOfASlabHoleMovesBoth(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true
	Editor.SnapToGrid = false

	drawOpening(ch, squareAround(5, 5, 1.5)...)
	upper, lower := &ch.Sectors[1], &ch.Sectors[0]
	corner := upper.Floor.Openings[0].Points[0]

	if !openingGrabVertex(ch, corner.X(), corner.Y(), 0.5) {
		t.Fatal("could not grab the corner")
	}
	// Drive the motion path proper, so the partner update is what is tested.
	moveOpeningDragTo(ch, corner.X()-1, corner.Y()-1)
	openingEndDrag()

	up := upper.Floor.Openings[0].Points[0]
	down := lower.Ceiling.Openings[0].Points[0]
	if up != down {
		t.Errorf("the two faces drifted: upper corner %v, lower corner %v", up, down)
	}
	if up == corner {
		t.Error("the corner did not move at all")
	}
}

// moveOpeningDragTo applies one step of the drag without going through SDL.
func moveOpeningDragTo(ch *world.Chunk, lx, lz float32) {
	s := &ch.Sectors[Editor.OpeningDragSector]
	list := &s.Floor.Openings
	if !Editor.OpeningDragFloor {
		list = &s.Ceiling.Openings
	}
	op := &(*list)[Editor.OpeningDragIdx]
	op.Points[Editor.OpeningDragVertex] = mgl32.Vec2{lx, lz}
	if far, twin, ok := world.OpeningPartner(ch, s, op, Editor.OpeningDragFloor); ok {
		twin.Points[Editor.OpeningDragVertex] = mgl32.Vec2{lx, lz}
		_ = world.ReloadSector(ch, far)
	}
	_ = world.ReloadSector(ch, s)
}

// A hole with nothing under it has no second face to cut, and must not invent
// one or refuse to be cut.
func TestAHoleOverNothingStaysStandalone(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)
	ch.Sectors = ch.Sectors[1:] // drop the room below
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(5, 5, 1.5)...)

	if n := len(ch.Sectors[0].Floor.Openings); n != 1 {
		t.Fatalf("the hole was not cut: %d openings", n)
	}
	if link := ch.Sectors[0].Floor.Openings[0].Link; link != "" {
		t.Errorf("a hole over nothing was linked to %q", link)
	}
}

// A room below with no ceiling of its own has no surface in the way, so there
// is nothing to cut and nothing to link.
func TestNoCeilingBelowMeansNothingToCut(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)
	ch.Sectors[0].Ceiling.Enabled = false
	Editor.ActiveLayer = 1
	Editor.EditFloorVerts = true

	drawOpening(ch, squareAround(5, 5, 1.5)...)

	if n := len(ch.Sectors[0].Ceiling.Openings); n != 0 {
		t.Errorf("cut %d holes in a ceiling that is not enabled", n)
	}
	if link := ch.Sectors[1].Floor.Openings[0].Link; link != "" {
		t.Errorf("linked to a ceiling that does not exist: %q", link)
	}
}

// A hole cut before openings went through both faces reads as open from one
// side and covered from the other. The repair has to find it and cut through.
func TestPairingRepairsAOneSidedHole(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)

	// The shape the old tool produced: a hole in the upper floor, nothing in
	// the ceiling below.
	ch.Sectors[1].Floor.Openings = []world.Opening{{
		ID:     "opening_1",
		Points: squareAround(5, 5, 1.5),
	}}

	if n := world.UnpairedOpenings(ch); n != 1 {
		t.Fatalf("UnpairedOpenings = %d, want 1", n)
	}
	if n := world.PairOpeningsThroughSlabs(ch); n != 1 {
		t.Fatalf("paired %d openings, want 1", n)
	}
	if n := len(ch.Sectors[0].Ceiling.Openings); n != 1 {
		t.Fatalf("the ceiling below has %d openings after the repair, want 1", n)
	}
	if ch.Sectors[1].Floor.Openings[0].Link == "" {
		t.Error("the original hole was not linked to its new twin")
	}
	if world.UnpairedOpenings(ch) != 0 {
		t.Error("still unpaired after the repair")
	}
	// Running it again must not cut a third hole.
	if n := world.PairOpeningsThroughSlabs(ch); n != 0 {
		t.Errorf("a second repair cut %d more openings", n)
	}
}

// Two halves cut separately are one shaft: the repair links them rather than
// stacking a third hole on top.
func TestPairingAdoptsAnExistingHoleOnTheFarSide(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	ch := twoStoreyChunk(t)

	pts := squareAround(5, 5, 1.5)
	ch.Sectors[1].Floor.Openings = []world.Opening{{ID: "up", Points: pts}}
	ch.Sectors[0].Ceiling.Openings = []world.Opening{{
		ID:     "down",
		Points: append([]mgl32.Vec2(nil), pts...),
	}}

	if n := world.PairOpeningsThroughSlabs(ch); n != 0 {
		t.Errorf("cut %d new holes when both halves already existed", n)
	}
	if len(ch.Sectors[0].Ceiling.Openings) != 1 || len(ch.Sectors[1].Floor.Openings) != 1 {
		t.Fatal("the repair added a duplicate hole")
	}
	if ch.Sectors[1].Floor.Openings[0].Link != "down" {
		t.Errorf("upper links to %q, want down", ch.Sectors[1].Floor.Openings[0].Link)
	}
	if ch.Sectors[0].Ceiling.Openings[0].Link != "up" {
		t.Errorf("lower links to %q, want up", ch.Sectors[0].Ceiling.Openings[0].Link)
	}
}
