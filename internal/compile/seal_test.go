package compile

// seal_test.go — the level's sealing box: that it seals, and that sealing is
// all it does.

import (
	"strings"
	"testing"

	"foundry/internal/bsp"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// openCorridor is the corridor level with its north wall gone — open to the
// void along one whole side — with a crate standing in the west room.
func openCorridor() *world.Level {
	lvl := corridorLevel()
	lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes[:5], lvl.Chunks[0].Brushes[6:]...)
	lvl.Entities = []world.Entity{{ID: "crate", Position: mgl32.Vec3{-15, 2, 0}}}
	return lvl
}

func TestTheSealBoxSealsALevelOpenToTheVoid(t *testing.T) {
	lvl := openCorridor()
	if leaks := leaksOf(t, lvl); len(leaks) != 1 {
		t.Fatalf("the fixture should leak without a seal, got %+v", leaks)
	}

	lvl.Seal = FitSeal(lvl)
	r, err := Compile(lvl, Options{SkipLight: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stats.Leaks) != 0 {
		t.Errorf("a sealed level still leaks: %+v", r.Stats.Leaks)
	}
	if r.Stats.InteriorLeaves == 0 {
		t.Error("a sealed level has no interior cells")
	}
	if !r.Tree.Interior(r.Tree.FindLeaf(mgl32.Vec3{-15, 2, 0})) {
		t.Error("the room the crate stands in is not inside the level")
	}
	if len(r.Stats.BeyondSeal) != 0 {
		t.Errorf("a fitted seal left brushes outside it: %v", r.Stats.BeyondSeal)
	}
}

// The slabs are structure: nothing the seal adds can be drawn.
func TestTheSealAddsNothingToDraw(t *testing.T) {
	drawable := func(fs []Face) int {
		n := 0
		for _, f := range fs {
			if !f.NoDraw {
				n++
			}
		}
		return n
	}
	lvl := openCorridor()
	plain := drawable(mustCSG(t, lvl))
	lvl.Seal = FitSeal(lvl)
	faces := mustCSG(t, lvl)
	if got := drawable(faces); got != plain {
		t.Errorf("%d drawable faces with the seal, %d without", got, plain)
	}
	slabs := 0
	for _, f := range faces {
		if f.Chunk == sealChunkName {
			slabs++
			if !f.NoDraw {
				t.Errorf("a slab face (%s %d) would be drawn", f.Brush, f.FaceIdx)
			}
		}
	}
	if slabs == 0 {
		t.Error("the seal put no slabs into the compile")
	}
}

// Switched off, it is as if there were none: the level seals itself or leaks.
func TestADisabledSealLeavesTheLevelToSealItself(t *testing.T) {
	lvl := openCorridor()
	lvl.Seal = FitSeal(lvl)
	lvl.Seal.Disabled = true
	leaks := leaksOf(t, lvl)
	if len(leaks) != 1 || leaks[0].OutsideSeal {
		t.Errorf("leaks = %+v, want the crate leaking through the missing wall", leaks)
	}
}

func TestSomethingOutsideTheSealIsReported(t *testing.T) {
	lvl := corridorLevel()
	lvl.Seal = FitSeal(lvl)
	lo, _ := lvl.Seal.Bounds()
	lvl.Entities = []world.Entity{{ID: "ship", Position: lo.Sub(mgl32.Vec3{10, 0, 0})}}

	leaks := leaksOf(t, lvl)
	if len(leaks) != 1 || !leaks[0].OutsideSeal {
		t.Fatalf("leaks = %+v, want the ship outside the seal", leaks)
	}
	if d := leaks[0].Describe(); !strings.Contains(d, "outside the level's seal box") {
		t.Errorf("Describe = %q", d)
	}
}

// Fitted to what compiles, and not to what does not: a disabled chunk, a
// vehicle's interior and a dynamic brush would stretch the box over open space
// for nothing.
func TestFitSealWrapsWhatCompiles(t *testing.T) {
	door := boxFromTo("far_door", mgl32.Vec3{-500, 0, 0}, mgl32.Vec3{-499, 1, 1})
	door.Dynamic = true
	lvl := &world.Level{
		Chunks: []world.Chunk{
			{ID: "static", Scale: 1, Brushes: []world.Brush{
				boxFromTo("floor", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 1, 10}), door,
			}},
			{ID: "off", Scale: 1, Disabled: true, Brushes: []world.Brush{
				boxFromTo("far_off", mgl32.Vec3{500, 0, 0}, mgl32.Vec3{501, 1, 1}),
			}},
			{ID: "ship", Scale: 1, FrameOwner: "dropship", Brushes: []world.Brush{
				boxFromTo("far_ship", mgl32.Vec3{0, 0, 500}, mgl32.Vec3{1, 1, 501}),
			}},
		},
		Entities: []world.Entity{{ID: "crate", Position: mgl32.Vec3{5, 20, 5}}},
	}

	lo, hi := FitSeal(lvl).Bounds()
	p := SealPadding
	wantLo, wantHi := mgl32.Vec3{-p, -p, -p}, mgl32.Vec3{10 + p, 20 + p, 10 + p}
	if !lo.ApproxEqualThreshold(wantLo, 1e-4) || !hi.ApproxEqualThreshold(wantHi, 1e-4) {
		t.Errorf("fitted box %v..%v, want %v..%v", lo, hi, wantLo, wantHi)
	}
}

func TestAnEmptyLevelGetsADefaultSeal(t *testing.T) {
	for _, lvl := range []*world.Level{nil, {}} {
		s := FitSeal(lvl)
		if s == nil || s.Half != defaultSealHalf || s.Position != (mgl32.Vec3{}) {
			t.Errorf("FitSeal(%v) = %+v, want the default box at the origin", lvl, s)
		}
	}
}

// The bake tests a sun's shadow against the tree's solid cells, and the slabs
// are solid. Past the seal is sky, so a floor open to it stays lit.
func TestTheSealDoesNotShadowTheSun(t *testing.T) {
	sun := world.LightSet{Directional: []world.DirectionalLight{{
		Direction: mgl32.Vec3{0, -1, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1,
		CastShadows: true, ShadowStrength: 1,
	}}}
	darkest := func(lvl *world.Level) float32 {
		lvl.Lights = sun
		tr, _, _ := compiled(t, lvl)
		BakeLight(tr, lvl, DefaultLightOptions())
		f := faceAt(t, tr, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
		d := float32(1e9)
		for _, l := range f.Light {
			d = min(d, l.X())
		}
		return d
	}
	floor := func() *world.Level {
		return levelOf(boxFromTo("floor", mgl32.Vec3{-10, -1, -10}, mgl32.Vec3{10, 0, 10}))
	}

	if got := darkest(floor()); bsp.Abs(got-1) > 1e-3 {
		t.Fatalf("the open floor baked %v under a full sun; the fixture is wrong", got)
	}
	sealed := floor()
	sealed.Seal = FitSeal(sealed)
	if got := darkest(sealed); bsp.Abs(got-1) > 1e-3 {
		t.Errorf("with a seal the floor baked %v, want the full sun: the slab above it cast a shadow", got)
	}
}

// A seal too small for its level still compiles, and names what reaches past it.
func TestBrushesReachingPastTheSealAreNamed(t *testing.T) {
	lvl := corridorLevel()
	lvl.Seal = &world.Seal{Position: mgl32.Vec3{-15, 2, 0}, Half: mgl32.Vec3{4, 4, 4}}
	r, err := Compile(lvl, Options{SkipLight: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range r.Stats.BeyondSeal {
		found = found || id == "floor"
	}
	if !found {
		t.Errorf("BeyondSeal = %v, want the floor among them", r.Stats.BeyondSeal)
	}
	if w := r.Stats.SealWarning(); !strings.Contains(w, "reach outside the seal box") {
		t.Errorf("SealWarning = %q", w)
	}
}
