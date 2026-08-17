package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// TestRotateRight90PointsOutwardForCCWPolygon pins the sign convention
// buildBiomeDecalMesh's whole halo direction depends on: for a CCW polygon
// (positive signedArea2D — the same winding ccwPolygon/delaunayTriangulate
// already assume elsewhere), rotating a directed edge -90 degrees must point
// AWAY from the interior. Getting this backwards would grow the halo INTO the
// sector instead of out past its edge — the exact opposite of the feature.
func TestRotateRight90PointsOutwardForCCWPolygon(t *testing.T) {
	// Unit square (0,0)-(1,0)-(1,1)-(0,1), CCW per the shoelace formula.
	square := []mgl32.Vec2{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	if signedArea2D(square) <= 0 {
		t.Fatal("test fixture square is not CCW — fix the fixture, not the assertion")
	}

	cases := []struct {
		name        string
		a, b        mgl32.Vec2
		wantOutward mgl32.Vec2
	}{
		{"bottom edge", mgl32.Vec2{0, 0}, mgl32.Vec2{1, 0}, mgl32.Vec2{0, -1}},
		{"right edge", mgl32.Vec2{1, 0}, mgl32.Vec2{1, 1}, mgl32.Vec2{1, 0}},
		{"top edge", mgl32.Vec2{1, 1}, mgl32.Vec2{0, 1}, mgl32.Vec2{0, 1}},
		{"left edge", mgl32.Vec2{0, 1}, mgl32.Vec2{0, 0}, mgl32.Vec2{-1, 0}},
	}
	for _, c := range cases {
		got := rotateRight90(c.b.Sub(c.a))
		if got.Sub(c.wantOutward).Len() > 1e-5 {
			t.Errorf("%s: rotateRight90(%v) = %v, want %v (outward)", c.name, c.b.Sub(c.a), got, c.wantOutward)
		}
	}
}

// TestBiomeDecalCollapsesWithNoSurroundingFloor pins the fallback in
// buildBiomeDecalMesh's outerAt: when a halo vertex's outward-offset point
// lands somewhere with no structural floor beneath it (here: EVERY point,
// since the level has no chunks at all), it must collapse to its inner
// point rather than draw a halo hanging in open space. Collapsing yields
// a zero-area triangle at that edge (two coincident vertices), which is the
// mechanism that makes it draw nothing instead of something wrong.
func TestBiomeDecalCollapsesWithNoSurroundingFloor(t *testing.T) {
	level := &Level{}
	s := &BiomeSector{
		ID:     "decal-test",
		Points: []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
		Brush:  defaultBiomeBrush(BiomeGrass),
	}

	verts := buildBiomeDecalMesh(level, s, 1.5)
	if len(verts) == 0 {
		t.Fatal("buildBiomeDecalMesh returned no geometry for a valid square sector")
	}
	if len(verts)%(decalVertexStride*3) != 0 {
		t.Fatalf("vertex count %d is not a whole number of triangles (stride %d)", len(verts)/decalVertexStride, decalVertexStride)
	}

	// Every "outer" vertex (weight 0) must sit exactly on some "inner" vertex
	// (weight 1) — with no floor anywhere, outerAt always falls back to inner.
	type v3 struct{ x, y, z float32 }
	inner := map[v3]bool{}
	for i := 0; i < len(verts); i += decalVertexStride {
		if verts[i+3] == 1 {
			inner[v3{verts[i], verts[i+1], verts[i+2]}] = true
		}
	}
	if len(inner) == 0 {
		t.Fatal("no weight-1 (inner) vertices produced at all")
	}
	for i := 0; i < len(verts); i += decalVertexStride {
		if verts[i+3] != 0 {
			continue
		}
		p := v3{verts[i], verts[i+1], verts[i+2]}
		if !inner[p] {
			t.Errorf("outer vertex %v has no floor beneath it but did not collapse onto an inner vertex", p)
		}
	}
}

// TestFakesGroundShadowKindSet mirrors BiomeShadowSettings.CastsKind's
// foliage branch on purpose (see fakesGroundShadow's doc comment) — the fake
// decal exists specifically for the kinds without a strong real shadow.
func TestFakesGroundShadowKindSet(t *testing.T) {
	for _, k := range []int32{BiomeGrass, BiomeWildflower, BiomeCattail} {
		if !fakesGroundShadow(k) {
			t.Errorf("kind %d should fake a ground shadow", k)
		}
	}
	for _, k := range []int32{BiomeRock, BiomeMoss, BiomeSand, BiomeWater} {
		if fakesGroundShadow(k) {
			t.Errorf("kind %d should not fake a ground shadow", k)
		}
	}
}

func TestResolvedFakeShadowSpread(t *testing.T) {
	if got := resolvedFakeShadowSpread(0); got != defaultFakeShadowSpread {
		t.Errorf("resolvedFakeShadowSpread(0) = %v, want the default %v", got, defaultFakeShadowSpread)
	}
	if got := resolvedFakeShadowSpread(2.5); got != 2.5 {
		t.Errorf("resolvedFakeShadowSpread(2.5) = %v, want 2.5 unchanged", got)
	}
}
