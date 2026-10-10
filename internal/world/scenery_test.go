package world

// scenery_test.go — the two halves of a prop that are easy to get wrong.
//
// One: the YAML contract. Scenery is the type a brush makes thousands of, so
// "a prop nobody touched writes four lines" is not a nicety — it is the
// difference between a readable level file and an unreadable one. That contract
// is held entirely by pointer fields and omitempty, and it breaks silently the
// first time someone adds a plain-valued key.
//
// Two: the bounds. They are read off the model rather than authored, which
// means nothing in the editor shows an author the number that collision will
// use — so a mistake here is invisible until something walks through a rock.
// The interesting case is the one every shipped mesh has: an origin at the base
// rather than at the centre, carried through rotation and scale.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

// boxScenery returns a prop whose model bounds are a 2x2x2 box standing on its
// own origin — the convention every mesh in this engine is authored to, and the
// one BoundCenter's doc comment describes.
func boxScenery() Scenery {
	s := NewScenery("box", "models/box.fmdl", mgl32.Vec3{5, 0, -3})
	s.LocalMin = mgl32.Vec3{-1, 0, -1}
	s.LocalMax = mgl32.Vec3{1, 2, 1}
	return s
}

func TestSceneryBoundCentreSitsOnTheMeshNotTheOrigin(t *testing.T) {
	s := boxScenery()
	if got, want := s.BoundCenter(), (mgl32.Vec3{5, 1, -3}); !vecNear(got, want, 1e-5) {
		t.Errorf("centre of a base-origin model: got %v, want %v", got, want)
	}
	if got, want := s.BoundHalfExtents(), (mgl32.Vec3{1, 1, 1}); !vecNear(got, want, 1e-5) {
		t.Errorf("half extents: got %v, want %v", got, want)
	}
}

// A scaled prop's box has to grow *and* stay on the mesh. Scaling the extents
// but not the offset is the bug that leaves a doubled tree's collision box
// buried in the ground up to its branches.
func TestSceneryScaleMovesTheCentreAsWellAsTheExtents(t *testing.T) {
	s := boxScenery()
	s.Scale = mgl32.Vec3{2, 3, 2}

	if got, want := s.BoundHalfExtents(), (mgl32.Vec3{2, 3, 2}); !vecNear(got, want, 1e-5) {
		t.Errorf("half extents: got %v, want %v", got, want)
	}
	// The mesh now spans y=0..6, so its centre is at 3 — not at the unscaled 1.
	if got, want := s.BoundCenter(), (mgl32.Vec3{5, 3, -3}); !vecNear(got, want, 1e-5) {
		t.Errorf("centre: got %v, want %v", got, want)
	}
}

// A negative scale mirrors a mesh. It must not give it negative size, or every
// overlap test against it silently reports no overlap at all.
func TestSceneryNegativeScaleKeepsPositiveExtents(t *testing.T) {
	s := boxScenery()
	s.Scale = mgl32.Vec3{-2, 1, 1}
	if got := s.BoundHalfExtents().X(); got != 2 {
		t.Errorf("mirrored prop has half-extent %v, want 2", got)
	}
}

// A rotated prop stays put. Turning a box about its own upright axis moves no
// part of it up or down and does not move its centre off the mesh.
func TestSceneryRotationKeepsTheCentreOnTheMesh(t *testing.T) {
	s := boxScenery()
	s.Rotation = mgl32.Vec3{0, mgl32.DegToRad(90), 0}
	if got, want := s.BoundCenter(), (mgl32.Vec3{5, 1, -3}); !vecNear(got, want, 1e-5) {
		t.Errorf("yawed prop's centre moved: got %v, want %v", got, want)
	}
}

// CollisionAABB is the exact span and WorldAABB the conservative one. The
// resolver uses the first as a cheap reject and culling uses the second, so the
// exact box must be inside the loose one and — for anything actually rotated —
// genuinely tighter. If they ever agreed, the cheap reject would have stopped
// rejecting anything.
func TestSceneryCollisionBoxIsTighterThanTheCullingBox(t *testing.T) {
	s := boxScenery()
	s.Rotation = mgl32.Vec3{0, mgl32.DegToRad(45), 0}

	cmin, cmax, ok := s.CollisionAABB()
	if !ok {
		t.Fatal("no collision bounds for a loaded prop")
	}
	wmin, wmax, ok := s.WorldAABB()
	if !ok {
		t.Fatal("no world bounds for a loaded prop")
	}
	for i := 0; i < 3; i++ {
		if cmin[i] < wmin[i]-1e-5 || cmax[i] > wmax[i]+1e-5 {
			t.Fatalf("axis %d: collision box %v..%v escapes the culling box %v..%v",
				i, cmin[i], cmax[i], wmin[i], wmax[i])
		}
	}
	// A 2x2x2 box turned 45 degrees spans sqrt(2) either side on X and Z, and
	// is untouched on Y. The culling box uses |half| = sqrt(3) on every axis.
	if got := cmax.Y() - cmin.Y(); got > 2.0001 || got < 1.9999 {
		t.Errorf("a yaw changed the box's height: %v, want 2", got)
	}
	if cmax.Y() >= wmax.Y() {
		t.Error("the exact box is no tighter than the conservative one on Y")
	}
}

// A mesh that is flat on an axis — a decal quad, a painted sign — has no volume
// to collide with. Reporting one would make it an infinitely thin wall.
func TestFlatSceneryHasNoBounds(t *testing.T) {
	s := NewScenery("decal", "models/decal.fmdl", mgl32.Vec3{})
	s.LocalMin = mgl32.Vec3{-1, -1, 0}
	s.LocalMax = mgl32.Vec3{1, 1, 0}
	if s.HasBounds() {
		t.Error("a flat mesh reported a collision volume")
	}
	if _, _, ok := s.CollisionAABB(); ok {
		t.Error("a flat mesh produced a collision box")
	}
}

// ---------- YAML ----------

// The omitempty contract: a prop dropped by the brush and left alone is an ID,
// a model and a position, and nothing else.
func TestUntouchedSceneryWritesNoOptionalKeys(t *testing.T) {
	s := NewScenery("rock", "models/rock.fmdl", mgl32.Vec3{1, 2, 3})
	r := SceneryToRaw(&s)
	if r.Scale != nil {
		t.Errorf("an unscaled prop wrote scale: %+v", *r.Scale)
	}
	if r.Solid != nil {
		t.Errorf("a solid prop wrote solid: %v", *r.Solid)
	}
	if r.Rotation != (RawVec3{}) {
		t.Errorf("an unrotated prop wrote rotation: %+v", r.Rotation)
	}
	if r.Wind != nil {
		t.Errorf("a still prop wrote wind: %+v", *r.Wind)
	}
}

// And the other direction: absent means the default, not the zero value. A prop
// whose scale key is missing must load at full size rather than collapsing, and
// one whose solid key is missing must be solid.
func TestAbsentSceneryKeysMeanTheirDefaults(t *testing.T) {
	got := SceneryFromRaw(RawScenery{ID: "rock", Model: "models/rock.fmdl"})
	if got.Scale != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("absent scale loaded as %v, want {1 1 1}", got.Scale)
	}
	if !got.Solid {
		t.Error("absent solid loaded as not solid")
	}
}

func TestSceneryRoundTripsEveryField(t *testing.T) {
	want := Scenery{
		ID:       "barrel_07",
		Model:    "models/barrel.fmdl",
		Position: mgl32.Vec3{4, 0.5, -2},
		Rotation: mgl32.Vec3{0.1, 1.25, -0.3},
		Scale:    mgl32.Vec3{2, 0.5, 2},
		Solid:    false,
		Wind:     SceneryWind{Bend: 0.03, Flutter: 0.1},
	}
	got := SceneryFromRaw(SceneryToRaw(&want))
	// Compared field by field rather than with reflect.DeepEqual: the runtime
	// half (Submeshes, and the bounds Load fills in) is not part of the file
	// format and is deliberately absent on both sides here.
	if got.ID != want.ID || got.Model != want.Model ||
		got.Position != want.Position || got.Rotation != want.Rotation ||
		got.Scale != want.Scale || got.Solid != want.Solid || got.Wind != want.Wind {
		t.Fatalf("scenery changed across a round trip:\n got %+v\nwant %+v", got, want)
	}
}

// An ID has to be unique across every kind, or the Explorer and the editor's
// own rename check disagree about what a name refers to.
func TestSceneryIDsAreUniqueAcrossKinds(t *testing.T) {
	lvl := Level{
		Entities: []Entity{{ID: "crate"}},
		Scenery:  []Scenery{{ID: "rock"}},
	}
	if got := UniqueSceneryID(&lvl, "crate"); got == "crate" {
		t.Error("a prop was allowed to take an entity's ID")
	}
	if got := UniqueEntityID(&lvl, "rock"); got == "rock" {
		t.Error("an entity was allowed to take a prop's ID")
	}
	if got := UniqueSceneryID(&lvl, "fern"); got != "fern" {
		t.Errorf("an unused ID was renamed: %q", got)
	}
}

// ---------- loading ----------

// Load is what fills in the bounds every test above sets by hand, so at least
// once they have to come from a real file.
func TestSceneryLoadReadsBoundsFromTheModel(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}

	path := filepath.Join(t.TempDir(), "box.obj")
	// A box from (-1, 0, -1) to (1, 2, 1): the base-origin convention again,
	// this time on disk. One face is enough to give the loader something to
	// triangulate; the bounds come from the vertex list either way.
	obj := `
v -1 0 -1
v 1 0 -1
v 1 2 1
v -1 2 1
f 1 2 3 4
`
	if err := os.WriteFile(path, []byte(obj), 0o644); err != nil {
		t.Fatalf("write obj: %v", err)
	}

	s := NewScenery("box", path, mgl32.Vec3{0, 0, 0})
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !s.HasBounds() {
		t.Fatalf("loaded prop has no bounds: %v..%v", s.LocalMin, s.LocalMax)
	}
	if got, want := s.BoundCenter(), (mgl32.Vec3{0, 1, 0}); !vecNear(got, want, 1e-5) {
		t.Errorf("centre from the file: got %v, want %v", got, want)
	}
}

// A model that is not there costs the placement its mesh, not its existence —
// the rule that keeps one renamed asset from making a level unopenable.
func TestSceneryLoadFailsWithoutLosingThePlacement(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	s := NewScenery("ghost", filepath.Join(t.TempDir(), "missing.obj"), mgl32.Vec3{1, 2, 3})
	if err := s.Load(); err == nil {
		t.Fatal("loading a missing model reported success")
	}
	if s.Position != (mgl32.Vec3{1, 2, 3}) || s.ID != "ghost" {
		t.Error("a failed load disturbed the placement")
	}
	if s.HasGeometry() || s.HasBounds() {
		t.Error("a failed load left geometry or bounds behind")
	}
}
