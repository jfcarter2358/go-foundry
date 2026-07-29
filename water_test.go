package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// TestWaterPlaneMeshIsTessellated pins the property the wave animation depends
// on: a water plane must have interior vertices to displace. A flat 4-vertex
// quad (what generatePlane returns) can only move its corners.
func TestWaterPlaneMeshIsTessellated(t *testing.T) {
	verts, indices := generateWaterPlane(4)

	if got, want := len(verts), 5*5; got != want {
		t.Fatalf("vertex count = %d, want %d", got, want)
	}
	// 4x4 cells, 2 triangles per face, 2 faces, 3 indices per triangle.
	if got, want := len(indices), 4*4*12; got != want {
		t.Fatalf("index count = %d, want %d", got, want)
	}

	for i, v := range verts {
		if len(v) != 5 {
			t.Fatalf("vertex %d has %d components, want 5 [X,Y,Z,U,V]", i, len(v))
		}
		if v[0] < -0.5 || v[0] > 0.5 || v[2] < -0.5 || v[2] > 0.5 {
			t.Errorf("vertex %d XZ (%g,%g) outside the unit footprint", i, v[0], v[2])
		}
		if v[1] != 0 {
			t.Errorf("vertex %d Y = %g, want 0 (displacement happens in the shader)", i, v[1])
		}
		if v[3] < 0 || v[3] > 1 || v[4] < 0 || v[4] > 1 {
			t.Errorf("vertex %d UV (%g,%g) outside [0,1]", i, v[3], v[4])
		}
	}

	for _, idx := range indices {
		if idx < 0 || idx >= len(verts) {
			t.Fatalf("index %d out of range (len=%d)", idx, len(verts))
		}
	}
}

// TestWaterPlaneMeshMatchesPlaneFootprint checks the tessellated water mesh
// occupies exactly the same extent as the plain plane primitive, so the two
// scale identically and the bounding maths stays shared.
func TestWaterPlaneMeshMatchesPlaneFootprint(t *testing.T) {
	verts, _ := generateWaterPlane(6)

	minX, maxX := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	minZ, maxZ := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for _, v := range verts {
		minX, maxX = min32(minX, v[0]), max32(maxX, v[0])
		minZ, maxZ = min32(minZ, v[2]), max32(maxZ, v[2])
	}

	if minX != -0.5 || maxX != 0.5 || minZ != -0.5 || maxZ != 0.5 {
		t.Fatalf("footprint = X[%g,%g] Z[%g,%g], want [-0.5,0.5] on both axes",
			minX, maxX, minZ, maxZ)
	}
}

// TestPrimitiveWorldAABBCoversFullExtent is the regression test for the bug
// where a water plane vanished once its centre left the view: culling used the
// owning sector's bounds, so the plane's own extent never entered the decision.
// The AABB must span the whole surface, not just a region around the centre.
func TestPrimitiveWorldAABBCoversFullExtent(t *testing.T) {
	p := Primitive{
		Kind:       PrimitiveWater,
		Position:   mgl32.Vec3{10, 2, -5},
		BoundHalfX: 250,
		BoundHalfY: 0.01,
		BoundHalfZ: 250,
	}

	lo, hi := p.WorldAABB()

	if lo.X() > -240 || hi.X() < 260 {
		t.Errorf("X span [%g,%g] does not cover the 500-unit plane", lo.X(), hi.X())
	}
	if lo.Z() > -255 || hi.Z() < 245 {
		t.Errorf("Z span [%g,%g] does not cover the 500-unit plane", lo.Z(), hi.Z())
	}
	// A far corner of the surface must be inside the bounds used for culling.
	corner := mgl32.Vec3{p.Position.X() + 250, p.Position.Y(), p.Position.Z() + 250}
	for a := 0; a < 3; a++ {
		if corner[a] < lo[a] || corner[a] > hi[a] {
			t.Fatalf("far corner %v falls outside AABB [%v,%v]", corner, lo, hi)
		}
	}
}

// TestPrimitiveWorldAABBIncludesWaveCrests checks displaced geometry is not
// culled at the moment a crest rises above the flat surface.
func TestPrimitiveWorldAABBIncludesWaveCrests(t *testing.T) {
	p := Primitive{
		Kind:       PrimitiveWater,
		Position:   mgl32.Vec3{0, 0, 0},
		BoundHalfX: 5,
		BoundHalfY: 0.01,
		BoundHalfZ: 5,
		Water:      WaterParams{Enabled: true, WaveAmplitude: 0.5},
	}

	lo, hi := p.WorldAABB()
	if hi.Y() < 0.5 || lo.Y() > -0.5 {
		t.Fatalf("Y span [%g,%g] does not contain wave crests of amplitude 0.5", lo.Y(), hi.Y())
	}
}

// TestPrimitiveWorldAABBAccountsForRotation covers a river rotated off-axis:
// the axis-aligned bounds must grow to contain the rotated footprint.
func TestPrimitiveWorldAABBAccountsForRotation(t *testing.T) {
	p := Primitive{
		Kind:       PrimitiveWater,
		Position:   mgl32.Vec3{0, 0, 0},
		Rotation:   mgl32.Vec3{0, math.Pi / 4, 0}, // 45° about Y
		BoundHalfX: 10,
		BoundHalfY: 0.01,
		BoundHalfZ: 2,
	}

	lo, hi := p.WorldAABB()

	// Rotating a 20x4 footprint by 45° widens its Z extent well past ±2.
	if hi.Z() < 8 || lo.Z() > -8 {
		t.Fatalf("Z span [%g,%g] ignores rotation; expected roughly ±8.5", lo.Z(), hi.Z())
	}
}

// TestWaterParamsRoundTrip checks a configured water plane survives save/load.
func TestWaterParamsRoundTrip(t *testing.T) {
	p := Primitive{
		Kind: PrimitiveWater,
		Water: WaterParams{
			Enabled:       true,
			WaveAmplitude: 0.25,
			WaveLength:    7.5,
			WaveSpeed:     1.25,
			Reflectivity:  0.8,
			ReflectColor:  mgl32.Vec3{0.1, 0.2, 0.3},
		},
	}

	got := waterParamsFromRaw(waterParamsToRaw(&p), PrimitiveWater)

	if got != p.Water {
		t.Fatalf("round trip changed params:\n got %+v\nwant %+v", got, p.Water)
	}
}

// TestWaterParamsDefaultWhenAbsent covers water planes saved before these
// settings existed — they must load with a usable look rather than a flat,
// motionless surface with zero amplitude.
func TestWaterParamsDefaultWhenAbsent(t *testing.T) {
	got := waterParamsFromRaw(nil, PrimitiveWater)

	if !got.Enabled {
		t.Fatal("water params from absent YAML block are disabled")
	}
	if got.WaveAmplitude <= 0 || got.WaveLength <= 0 {
		t.Fatalf("defaults leave the surface flat: %+v", got)
	}
}

// TestNonWaterPrimitiveHasNoWaterParams ensures the water shader path can never
// be switched on by a cube or sphere.
func TestNonWaterPrimitiveHasNoWaterParams(t *testing.T) {
	if got := waterParamsFromRaw(nil, PrimitiveRectPrism); got.Enabled {
		t.Fatalf("non-water primitive got enabled water params: %+v", got)
	}
	p := Primitive{Kind: PrimitiveSphere, Water: DefaultWaterParams()}
	if waterParamsToRaw(&p) != nil {
		t.Fatal("non-water primitive serialised a water block")
	}
}
