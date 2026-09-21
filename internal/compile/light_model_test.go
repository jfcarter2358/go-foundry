package compile

// light_model_test.go — the bake's lighting model: one-sided wrapped Lambert,
// hemispheric ambient, and a second answer for surfaces that shade smooth.

import (
	"math"
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

func maxX(ls []mgl32.Vec3) float32 {
	var m float32
	for _, l := range ls {
		m = max(m, l.X())
	}
	return m
}

// A light reaches only the side of a surface facing it. abs(N·L) used to light
// a ceiling from a sun above the roof as brightly as the floor under it.
func TestASurfaceFacingAwayFromTheSunGetsOnlyAmbient(t *testing.T) {
	tr := litRoom(t, world.LightSet{
		Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.1},
		Directional: []world.DirectionalLight{{
			Direction: mgl32.Vec3{0, -1, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1,
		}},
	})
	// The ceiling's underside faces down, straight away from the sun.
	ceil := faceAt(t, tr, mgl32.Vec3{-15, 4, 0}, mgl32.Vec3{0, -1, 0})
	for _, l := range ceil.Light {
		if math.Abs(float64(l.X()-0.1)) > 1e-4 {
			t.Fatalf("the ceiling's underside baked %v, want the ambient 0.1 and no sun", l)
		}
	}
}

// Wrap carries a light past side-on: a wall edge-on to the sun gets some of
// it, where plain Lambert would give it none.
func TestWrapLightsAWallSideOnToTheSun(t *testing.T) {
	sun := world.DirectionalLight{Direction: mgl32.Vec3{0, -1, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	hard := litRoom(t, world.LightSet{Directional: []world.DirectionalLight{sun}})
	soft := litRoom(t, world.LightSet{Global: world.GlobalLight{Wrap: 0.5},
		Directional: []world.DirectionalLight{sun}})
	at, facing := mgl32.Vec3{-20, 2, 0}, mgl32.Vec3{1, 0, 0} // room A's west wall
	if l := maxX(faceAt(t, hard, at, facing).Light); l != 0 {
		t.Errorf("at wrap 0 a wall side-on to the sun baked %v, want nothing", l)
	}
	// (0 + 0.5) / 1.5 = 1/3. The sun casts no shadow, so the roof is no
	// obstacle.
	if l := maxX(faceAt(t, soft, at, facing).Light); math.Abs(float64(l)-1.0/3) > 1e-3 {
		t.Errorf("at wrap 0.5 a wall side-on to the sun baked %v, want 1/3", l)
	}
}

// The ambient is brighter facing the sky than facing the ground, and the same
// on average.
func TestHemisphericAmbientFavoursUpFacingSurfaces(t *testing.T) {
	tr := litRoom(t, world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.4, Hemisphere: 1}})
	floor := maxX(faceAt(t, tr, mgl32.Vec3{-15, 0, 0}, mgl32.Vec3{0, 1, 0}).Light)
	ceil := maxX(faceAt(t, tr, mgl32.Vec3{-15, 4, 0}, mgl32.Vec3{0, -1, 0}).Light)
	wall := maxX(faceAt(t, tr, mgl32.Vec3{-20, 2, 0}, mgl32.Vec3{1, 0, 0}).Light)
	if math.Abs(float64(floor-0.6)) > 1e-4 || math.Abs(float64(ceil-0.2)) > 1e-4 || math.Abs(float64(wall-0.4)) > 1e-4 {
		t.Errorf("floor %v, ceiling %v, wall %v; want 0.6, 0.2 and 0.4 at hemisphere 1", floor, ceil, wall)
	}
}

// A double-sided face is baked once per side, each facing its own way.
func TestADoubleSidedFaceIsBakedPerSide(t *testing.T) {
	lvl := corridorLevel()
	// A free-standing double-sided panel in room A, facing +Y: its top toward
	// a point light above it, its underside away.
	panel := boxFromTo("panel", mgl32.Vec3{-17, 1, -2}, mgl32.Vec3{-13, 1.2, 2})
	for fi := range panel.Faces {
		panel.Faces[fi].DoubleSided = true
	}
	lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes, panel)
	lvl.Lights = world.LightSet{Points: []world.PointLight{{
		Position: mgl32.Vec3{-15, 3, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Radius: 6,
	}}}
	tr, _, _ := compiled(t, lvl)
	BakeLight(tr, lvl, DefaultLightOptions())
	top := faceAt(t, tr, mgl32.Vec3{-15, 1.2, 0}, mgl32.Vec3{0, 1, 0})
	if len(top.BackLight) != len(top.Points) {
		t.Fatalf("a double-sided face has %d back-side values for %d points", len(top.BackLight), len(top.Points))
	}
	if maxX(top.Light) == 0 || maxX(top.BackLight) != 0 {
		t.Errorf("panel top: front %v, back %v; want the light on the front only", maxX(top.Light), maxX(top.BackLight))
	}
}

// compilePrism is an eight-sided column standing in room A.
func compilePrism() world.Brush {
	const n = 8
	var verts []mgl32.Vec3
	for _, y := range []float32{0, 4} {
		for i := 0; i < n; i++ {
			a := 2 * math.Pi * float64(i) / n
			verts = append(verts, mgl32.Vec3{-15 + float32(math.Cos(a)), y, float32(math.Sin(a))})
		}
	}
	centre := mgl32.Vec3{-15, 2, 0}
	outward := func(idx []int) []int {
		p := func(k int) mgl32.Vec3 { return verts[idx[k]] }
		if p(1).Sub(p(0)).Cross(p(2).Sub(p(0))).Dot(p(0).Sub(centre)) < 0 {
			slices.Reverse(idx)
		}
		return idx
	}
	var faces []world.BrushFace
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		faces = append(faces, world.NewBrushFace(outward([]int{i, j, n + j, n + i}), ""))
	}
	return world.Brush{ID: "column", BrushMesh: world.BrushMesh{Verts: verts, Faces: faces}, Solid: true}
}

// A column's sides carry their smoothed normals through CSG, the BSP and the
// light grid, and bake a second, smooth answer from them.
func TestAColumnBakesASmoothAnswer(t *testing.T) {
	lvl := corridorLevel()
	lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes, compilePrism())
	lvl.Lights = world.LightSet{Points: []world.PointLight{{
		Position: mgl32.Vec3{-11, 2, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Radius: 8,
	}}}
	tr, _, _ := compiled(t, lvl)
	BakeLight(tr, lvl, DefaultLightOptions())

	smoothed := 0
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if f.Brush != "column" {
			if f.SmoothLight != nil {
				t.Fatalf("flat face of %s baked a smooth answer", f.Brush)
			}
			continue
		}
		ns := f.VertexNormals()
		if ns == nil || len(f.SmoothLight) != len(f.Points) {
			t.Fatalf("a column side has normals %v and %d smooth values for %d points", ns != nil, len(f.SmoothLight), len(f.Points))
		}
		for i, n := range ns {
			if math.Abs(float64(n.Len()-1)) > 1e-4 || n.Dot(f.Plane.N) < 0.9 {
				t.Fatalf("point %d's normal %v is not a unit vector near the side's plane %v", i, n, f.Plane.N)
			}
		}
		if !slices.Equal(f.Light, f.SmoothLight) {
			smoothed++
		}
	}
	if smoothed == 0 {
		t.Fatal("no column side's smooth bake differs from its flat one")
	}
}
