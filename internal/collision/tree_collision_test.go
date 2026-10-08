package collision

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// A placed tree is solid as its trunk: a walker clipping it is pushed out, a
// shot at it stops on the bark and names the tree, and its crown — far wider
// than the trunk — stops nothing. One that is not solid stops nothing at all.
func TestAPlacedTreeIsSolidAsItsTrunk(t *testing.T) {
	tree := world.NewTree("spruce", world.FoliageSitkaSpruce, mgl32.Vec3{0, 0, 0})
	lo, hi, _ := tree.TrunkBox()
	start := hi.X() + 0.15
	lvl := &world.Level{
		Trees:    []world.Tree{tree},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{start, 1, 0})},
	}
	InvalidateScenery()
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 1 || lvl.Entities[0].Position.X() <= start {
		t.Fatalf("a walker clipping the trunk was not pushed out: %v, x %v", moved, lvl.Entities[0].Position.X())
	}

	// A tree blocks as scenery does: it stops the ray without being
	// reported, since nothing in a script names it.
	shot := Raycast(lvl, mgl32.Vec3{-20, 1.5, 0}, mgl32.Vec3{1, 0, 0}, RayFilter{Range: 50, Geometry: true})
	if !shot.Blocked || shot.Stop.X() > lo.X()+0.01 || shot.Stop.X() < lo.X()-0.2 {
		t.Fatalf("a shot at the trunk stopped at %v (blocked %v; trunk from x %v)", shot.Stop, shot.Blocked, lo.X())
	}
	if id, _, _, ok := sceneryRayNearest(lvl, mgl32.Vec3{-20, 1.5, 0}, mgl32.Vec3{1, 0, 0}, 50, nil, false); !ok || id != "spruce" {
		t.Fatalf("the trunk's hit names %q", id)
	}
	// Through the crown, well clear of the trunk: nothing.
	if r := Raycast(lvl, mgl32.Vec3{-20, 20, 4}, mgl32.Vec3{1, 0, 0}, RayFilter{Range: 50, Geometry: true}); r.Blocked {
		t.Fatalf("the crown stopped a shot at %v", r.Stop)
	}

	lvl.Trees[0].Solid = false
	InvalidateScenery()
	if r := Raycast(lvl, mgl32.Vec3{-20, 1.5, 0}, mgl32.Vec3{1, 0, 0}, RayFilter{Range: 50, Geometry: true}); r.Blocked {
		t.Fatalf("a tree that is not solid stopped a shot at %v", r.Stop)
	}
}
