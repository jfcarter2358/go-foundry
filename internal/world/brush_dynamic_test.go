package world

// brush_dynamic_test.go — the run-time placement a script moves a brush by.
//
// What matters here is not the matrix maths but the consequences of it: that a
// moved brush REPORTS where it is (so culling, the spatial hash and collision
// all follow it), that its authored corners are untouched (so the level file
// and the compiled bake are unaffected), and that a brush nobody moved pays for
// none of it.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func testBoxBrush() Brush {
	return NewBoxBrush("door", mgl32.Vec3{0, 1, 0}, mgl32.Vec3{1, 1, 1}, "concrete")
}

// A brush nobody has moved is at rest, and everything downstream takes the
// cheap path on it.
func TestAuthoredBrushIsAtRest(t *testing.T) {
	b := testBoxBrush()
	if !b.AtRest() {
		t.Fatal("a freshly authored brush is not at rest")
	}
	if b.Placement() != mgl32.Ident4() {
		t.Errorf("Placement of a resting brush = %v, want the identity", b.Placement())
	}
	if got := b.Position(); !vecNear(got, mgl32.Vec3{0, 1, 0}, 1e-5) {
		t.Errorf("Position = %v, want the box's centre", got)
	}
}

// Moving a brush moves what it says its bounds are — which is what makes the
// chunk's bounds, the spatial hash and every frustum cull follow it — without
// touching the corners the level file is written from.
func TestMovingABrushMovesItsBoundsAndNotItsVerts(t *testing.T) {
	b := testBoxBrush()
	authored := append([]mgl32.Vec3(nil), b.Verts...)

	b.Dynamic = true
	b.MoveTo(mgl32.Vec3{10, 1, 0})

	if !vecNear(b.Position(), mgl32.Vec3{10, 1, 0}, 1e-5) {
		t.Errorf("Position after MoveTo = %v, want {10 1 0}", b.Position())
	}
	lo, hi := b.LocalAABB()
	if !vecNear(lo, mgl32.Vec3{9, 0, -1}, 1e-5) || !vecNear(hi, mgl32.Vec3{11, 2, 1}, 1e-5) {
		t.Errorf("LocalAABB = %v..%v, want the box around {10 1 0}", lo, hi)
	}
	for i := range b.Verts {
		if b.Verts[i] != authored[i] {
			t.Fatalf("vert %d was rewritten: %v, want the authored %v", i, b.Verts[i], authored[i])
		}
	}
}

// Rotation turns the brush about its own centre, not about the chunk origin —
// a door authored across the room must not swing round it.
func TestRotationTurnsAboutTheBrushCentre(t *testing.T) {
	b := NewBoxBrush("door", mgl32.Vec3{20, 1, 0}, mgl32.Vec3{2, 1, 0.25}, "concrete")
	b.Dynamic = true
	b.Rotation = mgl32.Vec3{0, math.Pi / 2, 0}

	if got := b.Position(); !vecNear(got, mgl32.Vec3{20, 1, 0}, 1e-4) {
		t.Fatalf("a quarter turn moved the centre to %v, want it left at {20 1 0}", got)
	}
	// Four units wide on X becomes four units deep on Z.
	lo, hi := b.LocalAABB()
	if got := hi.X() - lo.X(); got > 0.6 {
		t.Errorf("X extent after the turn = %v, want the thin axis (~0.5)", got)
	}
	if got := hi.Z() - lo.Z(); got < 3.9 {
		t.Errorf("Z extent after the turn = %v, want the wide axis (~4)", got)
	}
}

// The engine keeps a brush carrying a velocity moving, and a static one still.
func TestAdvanceBrushesIntegratesOnlyDynamicOnes(t *testing.T) {
	ch := Chunk{ID: "c", Scale: 1}
	moving := testBoxBrush()
	moving.ID, moving.Dynamic, moving.Velocity = "lift", true, mgl32.Vec3{0, 2, 0}
	still := testBoxBrush()
	still.ID, still.Velocity = "wall", mgl32.Vec3{0, 2, 0} // dynamic never ticked
	ch.Brushes = []Brush{moving, still}
	lvl := &Level{Chunks: []Chunk{ch}}

	if !AdvanceBrushes(lvl, 0.5) {
		t.Fatal("AdvanceBrushes reported nothing moved")
	}
	if got := lvl.Chunks[0].Brushes[0].Offset; !vecNear(got, mgl32.Vec3{0, 1, 0}, 1e-5) {
		t.Errorf("the dynamic brush's offset = %v, want {0 1 0} after half a second at 2/s", got)
	}
	if got := lvl.Chunks[0].Brushes[1].Offset; got != (mgl32.Vec3{}) {
		t.Errorf("a brush that is not dynamic moved anyway, to %v", got)
	}
	// The chunk's own extent has to have grown with the brush inside it, or
	// culling and the spatial hash would still be describing where it was.
	if lvl.Chunks[0].MaxBound.Y() < 2.9 {
		t.Errorf("chunk bounds top = %v, want it carried up with the lift", lvl.Chunks[0].MaxBound.Y())
	}
}

// An angular velocity turns the brush the same way a velocity slides it: onto
// Rotation, about the brush's own pivot, with the chunk's extent following the
// corners round.
func TestAdvanceBrushesIntegratesAngularVelocity(t *testing.T) {
	spinning := testBoxBrush()
	spinning.ID, spinning.Dynamic = "fan", true
	spinning.AngularVelocity = mgl32.Vec3{0, math.Pi / 2, 0}
	still := testBoxBrush()
	still.ID, still.AngularVelocity = "wall", mgl32.Vec3{0, math.Pi / 2, 0} // not dynamic
	lvl := &Level{Chunks: []Chunk{{ID: "c", Scale: 1, Brushes: []Brush{spinning, still}}}}

	if !AdvanceBrushes(lvl, 1) {
		t.Fatal("AdvanceBrushes reported nothing moved")
	}
	b := &lvl.Chunks[0].Brushes[0]
	if got := b.Rotation.Y(); math.Abs(float64(got)-math.Pi/2) > 1e-5 {
		t.Errorf("after a second at pi/2 rad/s the rotation is %v, want pi/2", got)
	}
	// A turn is not a slide: the offset is untouched and the pivot stays put.
	if b.Offset != (mgl32.Vec3{}) {
		t.Errorf("turning wrote an offset of %v", b.Offset)
	}
	if !vecNear(b.Position(), b.Pivot(), 1e-5) {
		t.Errorf("the brush's centre moved to %v, want its pivot %v", b.Position(), b.Pivot())
	}
	if lvl.Chunks[0].Brushes[1].Rotation != (mgl32.Vec3{}) {
		t.Errorf("a brush that is not dynamic turned anyway, to %v", lvl.Chunks[0].Brushes[1].Rotation)
	}
}

// Rotation wraps as it is integrated rather than growing without bound, so a
// brush left spinning keeps its float32 precision. Entity.Integrate does the
// same for the same reason.
func TestAdvanceBrushesWrapsRotation(t *testing.T) {
	b := testBoxBrush()
	b.Dynamic, b.AngularVelocity = true, mgl32.Vec3{0, 3, 0}
	lvl := &Level{Chunks: []Chunk{{ID: "c", Scale: 1, Brushes: []Brush{b}}}}

	for i := 0; i < 20; i++ {
		AdvanceBrushes(lvl, 1)
	}
	if got := lvl.Chunks[0].Brushes[0].Rotation.Y(); math.Abs(float64(got)) > math.Pi {
		t.Errorf("after 60 radians of spin the rotation is %v, want it wrapped into [-pi, pi]", got)
	}
}

// A disabled chunk is out of the world entirely, so nothing in it moves.
func TestAdvanceBrushesSkipsDisabledChunks(t *testing.T) {
	b := testBoxBrush()
	b.Dynamic, b.Velocity, b.AngularVelocity = true, mgl32.Vec3{0, 2, 0}, mgl32.Vec3{0, 2, 0}
	lvl := &Level{Chunks: []Chunk{{ID: "c", Scale: 1, Disabled: true, Brushes: []Brush{b}}}}

	if AdvanceBrushes(lvl, 0.5) {
		t.Fatal("a disabled chunk's brushes moved")
	}
	if lvl.Chunks[0].Brushes[0].Offset != (mgl32.Vec3{}) {
		t.Error("offset was written on a disabled chunk's brush")
	}
	if lvl.Chunks[0].Brushes[0].Rotation != (mgl32.Vec3{}) {
		t.Error("rotation was written on a disabled chunk's brush")
	}
}

// The dynamic flag is authored, so it has to survive a save and a load; the
// placement is not, so it must not appear in the file at all.
func TestDynamicRoundTripsAndPlacementDoesNot(t *testing.T) {
	b := testBoxBrush()
	b.Dynamic = true
	b.Offset, b.Rotation = mgl32.Vec3{5, 0, 0}, mgl32.Vec3{0, 1, 0}
	b.Velocity, b.AngularVelocity = mgl32.Vec3{0, 0, 3}, mgl32.Vec3{0, 2, 0}

	back, err := BrushFromRaw(BrushToRaw(&b))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !back.Dynamic {
		t.Error("dynamic was lost in the round trip")
	}
	if !back.AtRest() || back.Velocity != (mgl32.Vec3{}) || back.AngularVelocity != (mgl32.Vec3{}) {
		t.Errorf("the run-time placement was saved: offset %v, rotation %v, velocity %v, angular velocity %v",
			back.Offset, back.Rotation, back.Velocity, back.AngularVelocity)
	}
}

// The compiled draw loop asks this before walking a chunk's brushes at all.
func TestHasDynamicBrushes(t *testing.T) {
	ch := Chunk{Brushes: []Brush{testBoxBrush(), testBoxBrush()}}
	if ch.HasDynamicBrushes() {
		t.Error("a chunk of static brushes reported a dynamic one")
	}
	ch.Brushes[1].Dynamic = true
	if !ch.HasDynamicBrushes() {
		t.Error("a chunk holding a dynamic brush reported none")
	}
}
