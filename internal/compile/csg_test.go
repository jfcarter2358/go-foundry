package compile

// csg_test.go — what survives the clip.
//
// Every case here is a shape a blocked-out level actually contains, because the
// failure modes are all about surfaces that touch: two rooms sharing a wall, a
// pillar standing on a floor, a slab lying on a slab. A CSG that is merely
// "correct on separated boxes" removes nothing at all.

import (
	"testing"

	"foundry/internal/bsp"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// levelOf wraps brushes in one chunk at the origin.
func levelOf(brushes ...world.Brush) *world.Level {
	return &world.Level{Chunks: []world.Chunk{{ID: "c0", Brushes: brushes}}}
}

// mustCSG compiles a level a test asserts about, failing the test rather than
// returning an error the assertion would then have to thread through.
func mustCSG(t *testing.T, lvl *world.Level) []Face {
	t.Helper()
	faces, err := CSG(lvl)
	if err != nil {
		t.Fatalf("CSG: %v", err)
	}
	return faces
}

// facesOn returns the compiled faces whose plane normal points roughly along n.
func facesOn(faces []Face, n mgl32.Vec3) []Face {
	var out []Face
	for _, f := range faces {
		if f.Plane.N.Dot(n) > 0.99 {
			out = append(out, f)
		}
	}
	return out
}

// totalArea is how much surface a set of faces covers.
func totalArea(faces []Face) float32 {
	var a float32
	for _, f := range faces {
		a += bsp.PolygonArea(f.Points)
	}
	return a
}

func TestALoneBoxKeepsEverySide(t *testing.T) {
	lvl := levelOf(world.NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"))
	faces := mustCSG(t, lvl)
	if len(faces) != 6 {
		t.Fatalf("got %d faces, want a box's 6", len(faces))
	}
	if got, want := totalArea(faces), float32(24); got < want-0.01 || got > want+0.01 {
		t.Errorf("total area %v, want a 2x2x2 box's 24", got)
	}
}

// The case the whole stage exists for: two boxes flush against each other bury
// one face each, and exactly one of the pair survives — not both (z-fighting)
// and not neither (a hole).
func TestTwoFlushBoxesKeepOneOfTheSharedPair(t *testing.T) {
	lvl := levelOf(
		world.NewBoxBrush("a", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"),
		world.NewBoxBrush("b", mgl32.Vec3{2, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"),
	)
	faces := mustCSG(t, lvl)

	// Two boxes have 12 faces; the shared surface at x=1 accounts for two of
	// them, and one of those goes.
	if len(faces) != 11 {
		t.Fatalf("got %d faces, want 11 (12 less one of the buried pair)", len(faces))
	}
	// Exactly one face sits on the shared surface, whichever way it points.
	// Matched on the plane's x-intercept rather than on D: an X-facing plane's
	// D is signed by its normal, so box a's own -X face at x=-1 also has D=1
	// and a naive test counts it.
	shared := 0
	for _, f := range faces {
		if bsp.Abs(bsp.Abs(f.Plane.N.X())-1) < 0.01 && bsp.Abs(f.Plane.D/f.Plane.N.X()-1) < 0.01 {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("%d faces on the shared plane, want exactly 1", shared)
	}
}

// A brush wholly inside another contributes nothing: every one of its faces is
// buried, and the container's own faces are untouched.
func TestABoxInsideAnotherDisappears(t *testing.T) {
	lvl := levelOf(
		world.NewBoxBrush("outer", mgl32.Vec3{}, mgl32.Vec3{4, 4, 4}, "concrete"),
		world.NewBoxBrush("inner", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"),
	)
	faces := mustCSG(t, lvl)
	if len(faces) != 6 {
		t.Fatalf("got %d faces, want the outer box's 6", len(faces))
	}
	for _, f := range faces {
		if f.Brush == "inner" {
			t.Errorf("a face of the swallowed brush survived: %+v", f.Plane)
		}
	}
}

// A pillar SUNK INTO a floor buries a patch of it. The floor comes back as
// fragments that together cover what is left — the point being that a
// partly-buried face is CUT, not dropped.
//
// Sunk rather than resting on top, which is a different case entirely: a pillar
// whose base is exactly flush with the floor's surface has its underside
// coplanar with it, and clipPlanesFor resolves that pair by keeping one whole
// rather than by cutting either. See TestAPillarRestingOnAFloorKeepsItWhole.
func TestAPillarSunkIntoAFloorCutsIt(t *testing.T) {
	// A 20x20 floor slab with its top at y=0, and a 2x2 pillar whose base is
	// half a unit below that.
	lvl := levelOf(
		world.NewBoxBrush("floor", mgl32.Vec3{0, -1, 0}, mgl32.Vec3{10, 1, 10}, "concrete"),
		world.NewBoxBrush("pillar", mgl32.Vec3{0, 1.75, 0}, mgl32.Vec3{1, 2.25, 1}, "concrete"),
	)
	faces := mustCSG(t, lvl)

	tops := facesOn(faces, mgl32.Vec3{0, 1, 0})
	var floorTop []Face
	for _, f := range tops {
		if f.Brush == "floor" {
			floorTop = append(floorTop, f)
		}
	}
	if len(floorTop) < 2 {
		t.Fatalf("the floor's top came back as %d piece(s); a pillar should have cut it", len(floorTop))
	}
	// 20x20 less the pillar's 2x2 footprint.
	want := float32(400 - 4)
	if got := totalArea(floorTop); got < want-0.05 || got > want+0.05 {
		t.Errorf("floor top area %v, want %v (400 less the pillar's 4)", got, want)
	}
	// And the pillar's own underside, buried in the slab, is gone.
	for _, f := range facesOn(faces, mgl32.Vec3{0, -1, 0}) {
		if f.Brush == "pillar" {
			t.Error("the pillar's underside survived being sunk into the floor")
		}
	}
}

// The flush case, which is what a blocked-out level is actually full of: a
// pillar resting exactly on a floor. The two coplanar surfaces resolve to one —
// the floor's, since its brush came first — and the pillar's underside goes.
//
// The floor is kept WHOLE rather than cut, which is worth being explicit about:
// the region under the pillar is covered by exactly one surface either way, and
// cutting the floor there would trade one face for several to no visible end.
func TestAPillarRestingOnAFloorKeepsItWhole(t *testing.T) {
	lvl := levelOf(
		world.NewBoxBrush("floor", mgl32.Vec3{0, -1, 0}, mgl32.Vec3{10, 1, 10}, "concrete"),
		world.NewBoxBrush("pillar", mgl32.Vec3{0, 2, 0}, mgl32.Vec3{1, 2, 1}, "concrete"),
	)
	faces := mustCSG(t, lvl)

	var floorTop []Face
	for _, f := range facesOn(faces, mgl32.Vec3{0, 1, 0}) {
		if f.Brush == "floor" {
			floorTop = append(floorTop, f)
		}
	}
	if len(floorTop) != 1 {
		t.Fatalf("the floor's top came back as %d pieces, want 1 whole", len(floorTop))
	}
	if got := totalArea(floorTop); got < 399.95 || got > 400.05 {
		t.Errorf("floor top area %v, want the full 400", got)
	}
	for _, f := range facesOn(faces, mgl32.Vec3{0, -1, 0}) {
		if f.Brush == "pillar" {
			t.Error("both halves of the flush pair survived — they will z-fight")
		}
	}
}

// A hollow room is the shape a level is actually made of. Its INNER surfaces —
// the ones the player sees — must all survive, and the shell's own touching
// faces must not multiply.
func TestAHollowRoomKeepsItsInsideSurfaces(t *testing.T) {
	lvl := levelOf(world.NewHollowRoom("room",
		mgl32.Vec3{0, 0, 0}, mgl32.Vec3{8, 4, 8}, 0.5, "concrete")...)
	faces := mustCSG(t, lvl)

	// The floor's top at y=0 is what the player stands on.
	found := false
	for _, f := range facesOn(faces, mgl32.Vec3{0, 1, 0}) {
		if bsp.Abs(f.Plane.D) < 0.01 {
			found = true
		}
	}
	if !found {
		t.Error("the room's floor surface was clipped away")
	}
	// And the ceiling's underside at y=4.
	found = false
	for _, f := range facesOn(faces, mgl32.Vec3{0, -1, 0}) {
		if bsp.Abs(f.Plane.D+4) < 0.01 {
			found = true
		}
	}
	if !found {
		t.Error("the room's ceiling surface was clipped away")
	}
}

// A non-solid brush is decorative: it clips nothing, and its own faces are
// still clipped by the solids around them.
func TestANonSolidBrushClipsNothing(t *testing.T) {
	deco := world.NewBoxBrush("deco", mgl32.Vec3{2, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete")
	deco.Solid = false
	lvl := levelOf(
		world.NewBoxBrush("a", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"),
		deco,
	)
	faces := mustCSG(t, lvl)

	// The solid box keeps all six: nothing solid buries any of them.
	solid := 0
	for _, f := range faces {
		if f.Brush == "a" {
			solid++
		}
	}
	if solid != 6 {
		t.Errorf("the solid box kept %d faces, want 6 — a decorative brush must not clip", solid)
	}
}

// A NoDraw face is never drawn, so it is not output — but the brush it belongs
// to still clips, because it is still solid.
func TestNoDrawFacesAreDroppedButStillClip(t *testing.T) {
	b := world.NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	b.Faces[0].NoDraw = true
	lvl := levelOf(b, world.NewBoxBrush("n", mgl32.Vec3{2, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"))
	faces := mustCSG(t, lvl)

	for _, f := range faces {
		if f.Brush == "b" && f.FaceIdx == 0 {
			t.Error("a NoDraw face was compiled")
		}
	}
	// The neighbour still lost its own face to b's surface, so b clipped.
	shared := 0
	for _, f := range faces {
		if bsp.Abs(f.Plane.D-1) < 0.01 && bsp.Abs(bsp.Abs(f.Plane.N.X())-1) < 0.01 {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("%d faces on the shared plane, want 1 — a NoDraw face's brush still clips", shared)
	}
}

// A frame-owned chunk moves, so its geometry cannot be baked into a static
// world-space list — and it must not clip the static world either, or a parked
// ship would carve a hole in the ground it is sitting on.
func TestFrameOwnedChunksAreLeftOut(t *testing.T) {
	lvl := &world.Level{Chunks: []world.Chunk{
		{ID: "world", Brushes: []world.Brush{
			world.NewBoxBrush("ground", mgl32.Vec3{}, mgl32.Vec3{10, 1, 10}, "concrete"),
		}},
		{ID: "ship", FrameOwner: "shuttle", Brushes: []world.Brush{
			world.NewBoxBrush("hull", mgl32.Vec3{}, mgl32.Vec3{2, 2, 2}, "concrete"),
		}},
	}}
	faces := mustCSG(t, lvl)

	for _, f := range faces {
		if f.Chunk == "ship" {
			t.Error("a frame-owned chunk's geometry was compiled into the static world")
		}
	}
	if len(faces) != 6 {
		t.Errorf("got %d faces, want the ground's 6 — the ship must not have clipped it", len(faces))
	}
}

// A disabled chunk is not in the world at all.
func TestDisabledChunksAreLeftOut(t *testing.T) {
	lvl := levelOf(world.NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"))
	lvl.Chunks[0].Disabled = true
	if faces := mustCSG(t, lvl); len(faces) != 0 {
		t.Errorf("got %d faces from a disabled chunk", len(faces))
	}
}

// A chunk placed away from the origin compiles where it IS: the output is world
// space, because the chunk will not exist at run time to carry it there.
func TestFacesComeOutInWorldSpace(t *testing.T) {
	lvl := levelOf(world.NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"))
	lvl.Chunks[0].Origin = mgl32.Vec3{100, 5, -30}
	lvl.Chunks[0].Scale = 1

	faces := mustCSG(t, lvl)
	if len(faces) != 6 {
		t.Fatalf("got %d faces", len(faces))
	}
	lo, hi := bsp.Bounds(faces[0].Points)
	all := [2]mgl32.Vec3{lo, hi}
	for _, f := range faces[1:] {
		a, b := bsp.Bounds(f.Points)
		for i := 0; i < 3; i++ {
			if a[i] < all[0][i] {
				all[0][i] = a[i]
			}
			if b[i] > all[1][i] {
				all[1][i] = b[i]
			}
		}
	}
	wantLo := mgl32.Vec3{99, 4, -31}
	for i := 0; i < 3; i++ {
		if bsp.Abs(all[0][i]-wantLo[i]) > 0.01 {
			t.Fatalf("compiled bounds %v, want the box carried to the chunk's origin (%v)", all[0], wantLo)
		}
	}
}

// Compilation has to be deterministic: the BSP's plane choice, the bsp.PVS's leaf
// numbering and the baked lighting all follow from the order faces arrive in.
func TestCSGIsDeterministic(t *testing.T) {
	build := func() []Face {
		lvl := levelOf(world.NewHollowRoom("room",
			mgl32.Vec3{0, 0, 0}, mgl32.Vec3{8, 4, 8}, 0.5, "concrete")...)
		f := mustCSG(t, lvl)
		SortFaces(f)
		return f
	}
	a, b := build(), build()
	if len(a) != len(b) {
		t.Fatalf("two runs produced %d and %d faces", len(a), len(b))
	}
	for i := range a {
		if a[i].Brush != b[i].Brush || a[i].FaceIdx != b[i].FaceIdx || len(a[i].Points) != len(b[i].Points) {
			t.Fatalf("face %d differs between runs: %+v vs %+v", i, a[i].Plane, b[i].Plane)
		}
		for j := range a[i].Points {
			if a[i].Points[j] != b[i].Points[j] {
				t.Fatalf("face %d point %d differs between runs", i, j)
			}
		}
	}
}
