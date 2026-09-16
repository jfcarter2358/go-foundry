package compile

// light_test.go — the bake, checked against what the shader would have done.
//
// The assertions are about the model rather than about exact colours, because
// the model is the contract: a baked surface has to look like the live one, so
// each term here is checked for being present, correctly scaled, and correctly
// shadowed.

import (
	"math"
	"testing"

	"foundry/internal/bsp"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func litRoom(t *testing.T, lights world.LightSet) *BSPTree {
	t.Helper()
	lvl := corridorLevel()
	lvl.Lights = lights
	tr, _, _ := compiled(t, lvl)
	BakeLight(tr, lvl, DefaultLightOptions())
	return tr
}

// faceAt finds a compiled face containing a point, for asking what the bake
// made of a particular bit of wall or floor.
func faceAt(t *testing.T, tr *BSPTree, p mgl32.Vec3, normal mgl32.Vec3) *Face {
	t.Helper()
	var best *Face
	bestD := float32(1e9)
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if f.Plane.N.Dot(normal) < 0.99 || bsp.Abs(f.Plane.Distance(p)) > 0.01 {
			continue
		}
		c := bsp.Centroid(f.Points)
		if d := c.Sub(p).Len(); d < bestD {
			best, bestD = f, d
		}
	}
	if best == nil {
		t.Fatalf("no face at %v facing %v", p, normal)
	}
	return best
}

func TestEveryVertexGetsALightValue(t *testing.T) {
	tr := litRoom(t, world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.25}})
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if len(f.Light) != len(f.Points) {
			t.Fatalf("face %d has %d points and %d light values", fi, len(f.Points), len(f.Light))
		}
		for _, l := range f.Light {
			if bsp.Abs(l.X()-0.25) > 1e-5 {
				t.Fatalf("face %d baked %v, want the ambient 0.25 everywhere", fi, l)
			}
		}
	}
}

// A point light falls off quadratically to nothing at its radius, and cuts off
// hard beyond it — the shader's own falloff, which the bake has to match or a
// baked surface stops looking like a live one.
func TestAPointLightFallsOffTheWayTheShaderDoes(t *testing.T) {
	// Directly above a patch of floor, well inside room A.
	lp := mgl32.Vec3{-15, 3, 0}
	tr := litRoom(t, world.LightSet{Points: []world.PointLight{{
		Position: lp, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Radius: 6,
	}}})

	under := faceAt(t, tr, mgl32.Vec3{-15, 0, 0}, mgl32.Vec3{0, 1, 0})
	var brightest float32
	for _, l := range under.Light {
		if l.X() > brightest {
			brightest = l.X()
		}
	}
	// Straight below the light at three units, on a floor whose normal points
	// at it: t = 1 - 3/6, attenuation 0.25, N.L = 1.
	if brightest < 0.2 || brightest > 0.26 {
		t.Errorf("directly under the light the floor baked to %v, want about 0.25", brightest)
	}

	// Beyond the radius there is nothing at all, not merely very little — the
	// shader's cutoff is a branch, not a curve that tails off.
	checked := 0
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		for i, p := range f.Points {
			if p.Sub(lp).Len() <= 6 {
				continue
			}
			checked++
			if f.Light[i] != (mgl32.Vec3{}) {
				t.Fatalf("a vertex %v units from the light baked to %v, want nothing",
					p.Sub(lp).Len(), f.Light[i])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no vertex fell outside the light's radius; the test proves nothing")
	}
}

// The reason the bake is worth doing at all: geometry between a surface and a
// light darkens it, and the compiler works that out once rather than the
// shader working it out every frame.
func TestGeometryBetweenASurfaceAndALightShadowsIt(t *testing.T) {
	// A light in room B, and floor in room A on the far side of the wall. The
	// corridor is on the centre line, so the floor at z = 8 is behind solid
	// wall from the light's point of view.
	lights := world.LightSet{Points: []world.PointLight{{
		Position: mgl32.Vec3{2, 2, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Radius: 40,
		CastShadows: true, ShadowStrength: 1,
	}}}
	tr := litRoom(t, lights)

	lit := faceAt(t, tr, mgl32.Vec3{-2, 0, 0}, mgl32.Vec3{0, 1, 0})      // down the corridor
	shadowed := faceAt(t, tr, mgl32.Vec3{-5, 0, 8}, mgl32.Vec3{0, 1, 0}) // behind the wall

	var litMax, shadowMax float32
	for _, l := range lit.Light {
		if l.X() > litMax {
			litMax = l.X()
		}
	}
	for _, l := range shadowed.Light {
		if l.X() > shadowMax {
			shadowMax = l.X()
		}
	}
	if litMax <= 0 {
		t.Fatal("floor with a clear line to the light baked to nothing")
	}
	if shadowMax != 0 {
		t.Errorf("floor behind a wall baked to %v, want a full shadow", shadowMax)
	}
}

// A light that is switched off contributes nothing, exactly as
// EffectiveBrightness makes it contribute nothing at run time. Baking a
// disabled light in would make the switch stop working for static geometry.
func TestASwitchedOffLightBakesToNothing(t *testing.T) {
	tr := litRoom(t, world.LightSet{
		Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Disabled: true},
		Points: []world.PointLight{{
			Position: mgl32.Vec3{-15, 2, 0}, Color: mgl32.Vec3{1, 1, 1},
			Brightness: 1, Radius: 20, Disabled: true,
		}},
	})
	for fi := range tr.Faces {
		for _, l := range tr.Faces[fi].Light {
			if l != (mgl32.Vec3{}) {
				t.Fatalf("face %d baked %v with every light switched off", fi, l)
			}
		}
	}
}

// A dynamic light is left out of the bake entirely — the shader sums it live on
// top of the baked value, so baking it in as well would light the walls twice.
// Checked beside an identical static light, which has to bake, so the test
// cannot pass by baking nothing at all.
func TestADynamicLightIsNotBaked(t *testing.T) {
	light := world.PointLight{
		Position: mgl32.Vec3{-15, 2, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Radius: 20,
	}
	static := litRoom(t, world.LightSet{Points: []world.PointLight{light}})
	light.Dynamic = true
	sun := world.DirectionalLight{Yaw: 0, Pitch: -60, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Dynamic: true}
	sun.SyncDirection()
	dynamic := litRoom(t, world.LightSet{
		Global:      world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.1},
		Points:      []world.PointLight{light},
		Directional: []world.DirectionalLight{sun},
	})

	lit := false
	for fi := range static.Faces {
		for _, l := range static.Faces[fi].Light {
			lit = lit || l != (mgl32.Vec3{})
		}
	}
	if !lit {
		t.Fatal("the static light baked to nothing; the test proves nothing")
	}
	for fi := range dynamic.Faces {
		for _, l := range dynamic.Faces[fi].Light {
			// The ambient, which is always baked, and nothing else.
			if bsp.Abs(l.X()-0.1) > 1e-5 {
				t.Fatalf("face %d baked %v with only dynamic lights beside the 0.1 ambient", fi, l)
			}
		}
	}
}

// A static light marked NoBake is left out the same way: it belongs to geometry
// the compile leaves out, and baked it would stay on the level after that
// geometry had gone.
func TestANoBakeLightIsNotBaked(t *testing.T) {
	light := world.PointLight{
		Position: mgl32.Vec3{-15, 2, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Radius: 20, NoBake: true,
	}
	sun := world.DirectionalLight{Yaw: 0, Pitch: -60, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, NoBake: true}
	sun.SyncDirection()
	tr := litRoom(t, world.LightSet{
		Global:      world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.1},
		Points:      []world.PointLight{light},
		Directional: []world.DirectionalLight{sun},
	})
	for fi := range tr.Faces {
		for _, l := range tr.Faces[fi].Light {
			if bsp.Abs(l.X()-0.1) > 1e-5 {
				t.Fatalf("face %d baked %v with only no_bake lights beside the 0.1 ambient", fi, l)
			}
		}
	}
}

// Cutting on the world grid rather than on each face's own bounds is what makes
// two faces meeting at an edge agree about where their vertices are. Cut
// per-face and the shared edge gets vertices on one side with no counterpart on
// the other, and every one of those is a seam.
func TestFacesAreCutOnTheWorldGrid(t *testing.T) {
	// A single wide floor slab, cut on a grid of 4.
	lvl := levelOf(boxFromTo("floor", mgl32.Vec3{-10, -1, -10}, mgl32.Vec3{10, 0, 10}))
	faces, err := CSG(lvl)
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildBSP(faces)
	tr.SubdivideFaces(4)

	for fi := range tr.Faces {
		lo, hi := bsp.Bounds(tr.Faces[fi].Points)
		for a := 0; a < 3; a++ {
			if hi[a]-lo[a] > 4+0.01 {
				t.Fatalf("face %d spans %v on axis %d, want no more than the grid step",
					fi, hi[a]-lo[a], a)
			}
			// Every cut is on a multiple of the step, so a coordinate that is
			// not one of the slab's own edges is one.
			for _, v := range []float32{lo[a], hi[a]} {
				onGrid := bsp.Abs(v-float32(math.Round(float64(v)/4))*4) < 0.01
				own := bsp.Abs(bsp.Abs(v)-10) < 0.01 || bsp.Abs(bsp.Abs(v)-1) < 0.01
				if !onGrid && !own {
					t.Fatalf("face %d has an edge at %v on axis %d, which is neither a grid line nor the slab's own", fi, v, a)
				}
			}
		}
	}
	// The cutting is lossless: the pieces still cover the slab.
	var area float32
	for fi := range tr.Faces {
		area += bsp.PolygonArea(tr.Faces[fi].Points)
	}
	if want := totalArea(faces); area < want-0.05 || area > want+0.05 {
		t.Errorf("the cut pieces cover %v, want the slab's %v", area, want)
	}
}

// A light's BakeExclude names chunks its bake skips, while it keeps baking
// normally into everything else — the hangar-and-ground case this was added
// for (see world.DirectionalLight.BakeExclude): the sun bakes into the ground
// but not into a hangar chunk the level disables for good.
func TestBakeExcludeSkipsOnlyTheNamedChunk(t *testing.T) {
	lvl := &world.Level{Chunks: []world.Chunk{
		{ID: "ground", Brushes: []world.Brush{boxFromTo("floor", mgl32.Vec3{-10, -1, -10}, mgl32.Vec3{10, 0, 10})}},
		{ID: "hangar", Brushes: []world.Brush{boxFromTo("floor", mgl32.Vec3{-10, -1, 20}, mgl32.Vec3{10, 0, 40})}},
	}}
	sun := world.DirectionalLight{Yaw: 0, Pitch: -90, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1,
		BakeExclude: []string{"hangar"}}
	sun.SyncDirection()
	lvl.Lights = world.LightSet{
		Global:      world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.1},
		Directional: []world.DirectionalLight{sun},
	}

	faces, err := CSG(lvl)
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildBSP(faces)
	BakeLight(tr, lvl, DefaultLightOptions())

	up := mgl32.Vec3{0, 1, 0}
	ground := faceAt(t, tr, mgl32.Vec3{0, 0, 0}, up)
	hangar := faceAt(t, tr, mgl32.Vec3{0, 0, 30}, up)

	// Straight down from directly overhead, N·L = 1: ambient 0.1 plus the
	// sun's full 1.0, exactly the sun's own colour.
	if got := ground.Light[0].X(); bsp.Abs(got-1.1) > 1e-4 {
		t.Fatalf("ground (not excluded) baked %v, want the sun included: 1.1", got)
	}
	if got := hangar.Light[0].X(); bsp.Abs(got-0.1) > 1e-4 {
		t.Fatalf("hangar (excluded) baked %v, want only the 0.1 ambient", got)
	}
}
