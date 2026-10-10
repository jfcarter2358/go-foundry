package geom

import (
	"math"
	"testing"
)

// v builds a [X,Y,Z,U,V] vertex record; UVs are irrelevant to normal generation.
func v(x, y, z float32) []float32 { return []float32{x, y, z, 0, 0} }

func normalAt(t *testing.T, out []float32, corner int) vec3 {
	t.Helper()
	if len(out) < (corner+1)*3 {
		t.Fatalf("normal %d missing: got %d floats", corner, len(out))
	}
	return vec3{out[corner*3], out[corner*3+1], out[corner*3+2]}
}

func assertNear(t *testing.T, got vec3, want vec3, tol float32) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if d := got[i] - want[i]; d > tol || d < -tol {
			t.Fatalf("normal = %v, want %v (tol %g)", got, want, tol)
		}
	}
}

// A flat quad must come out with exactly the facet normal on every corner, so
// enabling smoothing on already-flat geometry changes nothing.
func TestSmoothNormalsFlatQuadUnchanged(t *testing.T) {
	verts := [][]float32{v(0, 0, 0), v(1, 0, 0), v(1, 0, 1), v(0, 0, 1)}
	// CCW seen from +Y.
	out := ComputeSmoothNormals(verts, []int{0, 2, 1, 0, 3, 2}, 60)

	if len(out) != 6*3 {
		t.Fatalf("got %d floats, want %d", len(out), 6*3)
	}
	for c := 0; c < 6; c++ {
		assertNear(t, normalAt(t, out, c), vec3{0, 1, 0}, 1e-5)
	}
}

// Two facets meeting at a shallow angle share the vertices along their seam, so
// those normals average — that averaging is what removes the lighting step.
func TestSmoothNormalsAveragesAcrossShallowSeam(t *testing.T) {
	// A ridge: quad 1 lies flat, quad 2 tilts up 45° about the shared X edge.
	const s = 0.70710678
	verts := [][]float32{
		v(0, 0, 0), v(1, 0, 0), // shared seam
		v(0, 0, -1), v(1, 0, -1), // flat side  → normal (0,1,0)
		v(0, s, s), v(1, s, s), // tilted side → normal (0,s,-s)
	}
	indices := []int{
		0, 1, 3, 0, 3, 2, // flat quad
		0, 5, 1, 0, 4, 5, // tilted quad
	}
	out := ComputeSmoothNormals(verts, indices, 60)

	// Corner 0 is seam vertex (0,0,0) on the flat triangle: it must have moved
	// off the facet normal towards the tilted face.
	seam := normalAt(t, out, 0)
	if seam[1] <= 0 {
		t.Fatalf("seam normal points the wrong way: %v", seam)
	}
	if seam[2] >= -0.05 {
		t.Fatalf("seam normal %v was not pulled towards the tilted face", seam)
	}
	if l := math.Sqrt(float64(dot3(seam, seam))); math.Abs(l-1) > 1e-5 {
		t.Fatalf("seam normal length = %v, want 1", l)
	}

	// The far corner of the flat quad touches only flat triangles, so it stays
	// exactly flat — smoothing is local to the seam.
	far := normalAt(t, out, 2) // vertex (1,0,-1)
	assertNear(t, far, vec3{0, 1, 0}, 1e-5)
}

// Facets steeper than the crease angle must not average, so a hard edge stays
// hard however many surfaces meet at it.
func TestSmoothNormalsKeepsCreaseSharp(t *testing.T) {
	// A right-angle corner: floor in XZ and wall in XY sharing the X axis edge.
	verts := [][]float32{
		v(0, 0, 0), v(1, 0, 0), // shared edge
		v(0, 0, 1), v(1, 0, 1), // floor    → normal (0,1,0)
		v(0, 1, 0), v(1, 1, 0), // wall top → normal (0,0,1)
	}
	indices := []int{
		0, 2, 3, 0, 3, 1, // floor
		0, 1, 5, 0, 5, 4, // wall
	}

	// 60° threshold: the 90° crease must be rejected on both sides.
	out := ComputeSmoothNormals(verts, indices, 60)
	assertNear(t, normalAt(t, out, 0), vec3{0, 1, 0}, 1e-5) // floor side of the seam
	assertNear(t, normalAt(t, out, 6), vec3{0, 0, 1}, 1e-5) // wall side of the seam

	// Raise the threshold past 90° and the same corner smooths.
	soft := ComputeSmoothNormals(verts, indices, 120)
	n := normalAt(t, soft, 0)
	if n[1] < 0.1 || n[2] < 0.1 {
		t.Fatalf("crease angle 120 should have blended the corner, got %v", n)
	}
}

// A double-sided surface stores both windings at the same positions. Those
// facets are exact opposites and must never cancel each other to zero.
func TestSmoothNormalsDoubleSidedDoesNotCancel(t *testing.T) {
	verts := [][]float32{v(0, 0, 0), v(1, 0, 0), v(1, 0, 1), v(0, 0, 1)}
	indices := []int{
		0, 2, 1, 0, 3, 2, // front (+Y)
		0, 1, 2, 0, 2, 3, // back  (-Y)
	}
	for _, angle := range []float32{60, 179, 0 /* default */} {
		out := ComputeSmoothNormals(verts, indices, angle)
		for c := 0; c < 6; c++ {
			assertNear(t, normalAt(t, out, c), vec3{0, 1, 0}, 1e-5)
		}
		for c := 6; c < 12; c++ {
			assertNear(t, normalAt(t, out, c), vec3{0, -1, 0}, 1e-5)
		}
	}
}

// Every corner gets a unit normal even when the mesh contains degenerate
// triangles, so a bad vertex can't put NaNs into the buffer.
func TestSmoothNormalsDegenerateTriangle(t *testing.T) {
	verts := [][]float32{v(0, 0, 0), v(1, 0, 0), v(1, 0, 1)}
	out := ComputeSmoothNormals(verts, []int{0, 1, 1}, 60) // zero-area

	if len(out) != 9 {
		t.Fatalf("got %d floats, want 9", len(out))
	}
	for i, f := range out {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			t.Fatalf("out[%d] = %v", i, f)
		}
	}
}

func TestSmoothNormalsEmptyMesh(t *testing.T) {
	if out := ComputeSmoothNormals(nil, nil, 60); out != nil {
		t.Fatalf("got %v, want nil", out)
	}
}

func TestSmoothAngleOrDefault(t *testing.T) {
	cases := []struct{ in, want float32 }{
		{0, DefaultSmoothAngle},
		{-5, DefaultSmoothAngle},
		{30, 30},
		{179, 179},
		{200, 179}, // clamped below 180 so opposed facets always read as a crease
	}
	for _, c := range cases {
		if got := smoothAngleOrDefault(c.in); got != c.want {
			t.Errorf("smoothAngleOrDefault(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
