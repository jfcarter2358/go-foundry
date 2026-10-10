package world

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// Placed trees batch by species and variant, and anything an author can
// change about one changes the fingerprint the renderer rebuilds on.
func TestTreeBatchesFollowEveryEdit(t *testing.T) {
	lvl := &Level{Trees: []Tree{
		NewTree("a", FoliageSitkaSpruce, mgl32.Vec3{0, 0, 0}),
		NewTree("b", FoliageSitkaSpruce, mgl32.Vec3{20, 0, 0}),
		NewTree("c", FoliageHemlock, mgl32.Vec3{0, 0, 20}),
	}}
	lvl.Trees[1].Variant = 2
	bs, fp := TreeBatches(lvl)
	if len(bs) != 3 {
		t.Fatalf("%d batches for three species/variants", len(bs))
	}
	edits := []func(*Tree){
		func(tr *Tree) { tr.Position[0] += 0.5 },
		func(tr *Tree) { tr.Yaw = 1 },
		func(tr *Tree) { tr.Scale = 1.3 },
		func(tr *Tree) { tr.Variant = 1 },
		func(tr *Tree) { tr.Species = FoliageRedcedar },
		func(tr *Tree) { tr.Color = mgl32.Vec3{0.1, 0.5, 0.1} },
		func(tr *Tree) { tr.Accent = mgl32.Vec3{0.5, 0.5, 0.1} },
	}
	for i, e := range edits {
		cp := &Level{Trees: append([]Tree(nil), lvl.Trees...)}
		e(&cp.Trees[0])
		if _, f := TreeBatches(cp); f == fp {
			t.Errorf("edit %d left the fingerprint as it was", i)
		}
	}
	if _, f := TreeBatches(lvl); f != fp {
		t.Error("the same trees fingerprint differently twice")
	}
	if _, f := TreeBatches(&Level{}); f != 0 {
		t.Error("a level with no trees has a fingerprint")
	}
}

// A tree collides as a box round its trunk, scaled with it, and not at all
// when it is not solid or has no trunk.
func TestATreeCollidesAsItsTrunk(t *testing.T) {
	tr := NewTree("a", FoliageSitkaSpruce, mgl32.Vec3{5, 1, 5})
	lo, hi, ok := tr.TrunkBox()
	if !ok || hi.X()-lo.X() < 1 || hi.X()-lo.X() > 3 || hi.Y() < 30 {
		t.Fatalf("spruce trunk box %v..%v", lo, hi)
	}
	tr.Scale = 0.5
	lo2, hi2, _ := tr.TrunkBox()
	if hi2.X()-lo2.X() >= hi.X()-lo.X() {
		t.Error("a smaller tree has as wide a trunk")
	}
	tr.Solid = false
	if _, _, ok := tr.TrunkBox(); ok {
		t.Error("a tree that is not solid collides")
	}
	f := NewTree("f", FoliageSwordFern, mgl32.Vec3{})
	if _, _, ok := f.TrunkBox(); ok {
		t.Error("a fern has a trunk")
	}
}

// Every field survives the file; an untouched tree writes only what it must,
// and a species the engine does not know is kept as written.
func TestTreesRoundTripThroughTheirFiles(t *testing.T) {
	tr := NewTree("spruce_1", FoliageSitkaSpruce, mgl32.Vec3{12, 3, -40})
	tr.Variant, tr.Yaw, tr.Scale = 2, 0.6, 1.25
	tr.Color, tr.Accent = mgl32.Vec3{0.1, 0.3, 0.1}, mgl32.Vec3{0.4, 0.5, 0.1}
	tr.Solid = false
	back := TreeFromRaw(TreeToRaw(&tr))
	if back.Species != tr.Species || back.Variant != 2 || back.Position != tr.Position || back.Scale != 1.25 ||
		back.Color != tr.Color || back.Accent != tr.Accent || back.Solid || absf(back.Yaw-0.6) > 1e-4 {
		t.Fatalf("changed in the round trip:\nwant %+v\ngot  %+v", tr, back)
	}
	plain := TreeToRaw(&Tree{ID: "a", Species: FoliageHemlock, Scale: 1, Solid: true})
	if plain.Scale != 0 || plain.Solid != nil || plain.Color != nil || plain.Yaw != 0 || plain.Species != "hemlock" {
		t.Errorf("an untouched tree writes defaults: %+v", plain)
	}
	odd := TreeFromRaw(RawTree{ID: "x", Species: "giant_sequoia"})
	if r := TreeToRaw(&odd); r.Species != "giant_sequoia" {
		t.Errorf("an unknown species came back as %q", r.Species)
	}
	if bs, _ := TreeBatches(&Level{Trees: []Tree{odd}}); len(bs) != 0 {
		t.Error("a tree of an unknown species is drawn")
	}
}

