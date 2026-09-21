package compile

// bounce_test.go — light carried from one surface to the next.

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// withAlbedos makes the named textures average to the given colours for the
// length of a test; anything else is untextured grey.
func withAlbedos(t *testing.T, colours map[string]mgl32.Vec3) {
	t.Helper()
	old := textureAlbedo
	textureAlbedo = func(name string) (mgl32.Vec3, bool) {
		c, ok := colours[name]
		return c, ok
	}
	t.Cleanup(func() { textureAlbedo = old })
}

func paint(b world.Brush, texture string) world.Brush {
	for i := range b.Faces {
		b.Faces[i].Texture = texture
	}
	return b
}

// floorAndWall is a red floor in full overhead sun with a white wall standing
// on it. The sun is straight down and the wrap is zero, so the wall's sides
// get none of it directly; with no ambient, anything on them is bounce.
func floorAndWall(bounces int, wallChunk string, exclude []string, ambient float32) *world.Level {
	floor := paint(boxFromTo("floor", mgl32.Vec3{-10, -1, -10}, mgl32.Vec3{10, 0, 10}), "red")
	wall := paint(boxFromTo("wall", mgl32.Vec3{0, 0, -2}, mgl32.Vec3{1, 4, 2}), "white")
	lvl := &world.Level{Chunks: []world.Chunk{{ID: "ground", Brushes: []world.Brush{floor}}}}
	if wallChunk == "ground" {
		lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes, wall)
	} else {
		lvl.Chunks = append(lvl.Chunks, world.Chunk{ID: wallChunk, Brushes: []world.Brush{wall}})
	}
	sun := world.DirectionalLight{Pitch: -90, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, BakeExclude: exclude}
	sun.SyncDirection()
	lvl.Lights = world.LightSet{
		Global:      world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: ambient, Bounces: bounces, BounceScale: 1},
		Directional: []world.DirectionalLight{sun},
	}
	return lvl
}

func bake(t *testing.T, lvl *world.Level) *BSPTree {
	t.Helper()
	faces, err := CSG(lvl)
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildBSP(faces)
	BakeLight(tr, lvl, DefaultLightOptions())
	return tr
}

// wallSide is the lowest-centred piece of the wall's +X side: the part that
// sees the most floor.
func wallSide(t *testing.T, tr *BSPTree) *Face {
	return faceAt(t, tr, mgl32.Vec3{1, 1, 0}, mgl32.Vec3{1, 0, 0})
}

func maxLight(ls []mgl32.Vec3) mgl32.Vec3 {
	var out mgl32.Vec3
	for _, l := range ls {
		if l.X() > out.X() {
			out = l
		}
	}
	return out
}

var redWhite = map[string]mgl32.Vec3{"red": {0.8, 0.1, 0.1}, "white": {0.9, 0.9, 0.9}}

// The point of the whole thing: a wall the sun cannot reach picks up light off
// the sunlit floor beside it, in the floor's colour.
func TestBounceCarriesAFloorsColourOntoAWall(t *testing.T) {
	withAlbedos(t, redWhite)

	none := maxLight(wallSide(t, bake(t, floorAndWall(0, "ground", nil, 0))).Light)
	if none.X() > 1e-6 {
		t.Fatalf("with no bounce the wall's side baked %v, want nothing: the fixture is wrong", none)
	}

	got := maxLight(wallSide(t, bake(t, floorAndWall(1, "ground", nil, 0))).Light)
	// Near the foot of the wall about half its hemisphere is floor, lit at 1
	// and reflecting 0.8 of red: about 0.4.
	if got.X() < 0.15 || got.X() > 0.55 {
		t.Errorf("one bounce put %v on the wall, want about 0.4 red", got)
	}
	if got.Y() > got.X()/4 || got.Z() > got.X()/4 {
		t.Errorf("one bounce put %v on the wall, want it the floor's red", got)
	}
}

// More bounces only ever add light, and they converge: each one carries less
// than the one before, because no surface hands on all it receives.
func TestMoreBouncesAddLessEachTime(t *testing.T) {
	withAlbedos(t, redWhite)

	var totals []float64
	for b := 0; b <= 3; b++ {
		lvl := corridorLevel()
		lvl.Lights = world.LightSet{
			Global: world.GlobalLight{Bounces: b, BounceScale: 1},
			Points: []world.PointLight{{Position: mgl32.Vec3{-15, 2, 0}, Color: mgl32.Vec3{1, 1, 1},
				Brightness: 1, Radius: 20, CastShadows: true, ShadowStrength: 1}},
		}
		tr := bake(t, lvl)
		var sum float64
		for fi := range tr.Faces {
			for _, l := range tr.Faces[fi].Light {
				if math.IsNaN(float64(l.X())) || math.IsInf(float64(l.X()), 0) {
					t.Fatalf("%d bounces baked %v", b, l)
				}
				sum += float64(l.X())
			}
		}
		totals = append(totals, sum)
	}
	for b := 1; b < len(totals); b++ {
		if totals[b] <= totals[b-1] {
			t.Fatalf("totals by bounce %v: bounce %d added nothing", totals, b)
		}
	}
	for b := 2; b < len(totals); b++ {
		if totals[b]-totals[b-1] >= totals[b-1]-totals[b-2] {
			t.Errorf("totals by bounce %v: bounce %d added as much as the one before", totals, b)
		}
	}
}

// Recompiling an unchanged level gives the same bake to the bit — the rays are
// a fixed fan, not random, and the parallel gather writes each face once.
func TestTheBounceIsTheSameEveryTime(t *testing.T) {
	withAlbedos(t, redWhite)
	a := bake(t, floorAndWall(2, "ground", nil, 0.1))
	b := bake(t, floorAndWall(2, "ground", nil, 0.1))
	if len(a.Faces) != len(b.Faces) {
		t.Fatal("two compiles of one level differ in face count")
	}
	for fi := range a.Faces {
		for i := range a.Faces[fi].Light {
			if a.Faces[fi].Light[i] != b.Faces[fi].Light[i] {
				t.Fatalf("face %d point %d baked %v then %v", fi, i, a.Faces[fi].Light[i], b.Faces[fi].Light[i])
			}
		}
	}
}

// A light kept off a chunk stays off it second-hand: the sun that may not bake
// into the hangar must not arrive there off the sunlit ground either. The
// ambient, which is allowed everywhere, still bounces in.
func TestBakeExcludeKeepsBouncedLightOutToo(t *testing.T) {
	withAlbedos(t, redWhite)
	const ambient = 0.2

	excluded := maxLight(wallSide(t, bake(t, floorAndWall(1, "hangar", []string{"hangar"}, ambient))).Light)
	allowed := maxLight(wallSide(t, bake(t, floorAndWall(1, "hangar", nil, ambient))).Light)

	// The ambient bounced off the floor is at most ambient × albedo.
	if excluded.X() <= ambient || excluded.X() > ambient*(1+0.8) {
		t.Errorf("excluded wall baked %v, want the %v ambient and its own bounce only", excluded, ambient)
	}
	if allowed.X() < excluded.X()+0.15 {
		t.Errorf("wall allowed the sun baked %v against %v excluded: the sun's bounce is missing", allowed, excluded)
	}
}

// Zero bounces, or a zero scale, is the direct bake exactly.
func TestNoBounceIsTheDirectBake(t *testing.T) {
	withAlbedos(t, redWhite)
	direct := bake(t, floorAndWall(0, "ground", nil, 0.1))
	scaled := floorAndWall(2, "ground", nil, 0.1)
	scaled.Lights.Global.BounceScale = 0
	zero := bake(t, scaled)
	for fi := range direct.Faces {
		for i := range direct.Faces[fi].Light {
			if direct.Faces[fi].Light[i] != zero.Faces[fi].Light[i] {
				t.Fatalf("face %d point %d: %v with bounce scale 0, %v with no bounce",
					fi, i, zero.Faces[fi].Light[i], direct.Faces[fi].Light[i])
			}
		}
	}
}

// The tree finds the same nearest face a walk over every face does.
func TestFaceBVHFindsTheNearestFace(t *testing.T) {
	tr, _, _ := compiled(t, corridorLevel())
	tr.SubdivideFaces(4)
	bv := buildFaceBVH(tr, true)
	rng := rand.New(rand.NewSource(1))
	hits := 0
	for n := 0; n < 2000; n++ {
		o := mgl32.Vec3{rng.Float32()*30 - 20, rng.Float32() * 4, rng.Float32()*20 - 10}
		d := mgl32.Vec3{float32(rng.NormFloat64()), float32(rng.NormFloat64()), float32(rng.NormFloat64())}.Normalize()

		want, wantT := -1, float32(math.Inf(1))
		for fi := range tr.Faces {
			if t, ok := rayHitsFace(&tr.Faces[fi], o, d); ok && t < wantT {
				want, wantT = fi, t
			}
		}
		got, gotT, ok := bv.nearest(o, d, -1)
		if !ok {
			got = -1
		}
		if (want < 0) != (got < 0) || (want >= 0 && math.Abs(float64(gotT-wantT)) > 1e-4) {
			t.Fatalf("ray %v %v: tree hit face %d at %v, walk hit %d at %v", o, d, got, gotT, want, wantT)
		}
		if want >= 0 {
			hits++
		}
	}
	if hits == 0 {
		t.Fatal("no ray hit anything; the test proves nothing")
	}
}
