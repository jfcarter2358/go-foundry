package main

import (
	"fmt"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// TestDetailRawRoundTrip verifies a DetailSpec survives save→load unchanged.
func TestDetailRawRoundTrip(t *testing.T) {
	in := DetailSpec{
		Enabled: true, Preset: "rock", Seed: 42,
		Displace: true,
		Waves: [detailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.35, Frequency: 0.6, Rotation: 0, TessLevel: 24},
			{Enabled: false, Amplitude: 0.1, Frequency: 1.5, Rotation: 47.5, TessLevel: 32},
			{Enabled: true, Amplitude: 0.05, Frequency: 3, Rotation: -120, TessLevel: 8},
		},
		Noise: true, NoiseAmplitude: 0.12, NoiseFrequency: 1.4, NoiseOctaves: 4, NoiseTessLevel: 20,
		EdgeTaper: 0.4,
		Scatter:   true, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.5, ScatterSpacing: 1.4, ScatterScale: 0.5,
		ScatterJitter: 0.8, ScatterRange: 60,
	}
	raw := detailToRaw(in)
	if raw == nil {
		t.Fatal("detailToRaw returned nil for an enabled spec")
	}
	out := detailFromRaw(raw)
	if !out.Enabled || out.Preset != in.Preset || out.Seed != in.Seed ||
		out.Displace != in.Displace || out.Waves != in.Waves ||
		out.Noise != in.Noise || out.NoiseAmplitude != in.NoiseAmplitude ||
		out.NoiseFrequency != in.NoiseFrequency || out.NoiseOctaves != in.NoiseOctaves ||
		out.NoiseTessLevel != in.NoiseTessLevel || out.EdgeTaper != in.EdgeTaper ||
		out.Scatter != in.Scatter ||
		out.ScatterTex != in.ScatterTex || out.ScatterDensity != in.ScatterDensity ||
		out.ScatterSpacing != in.ScatterSpacing || out.ScatterScale != in.ScatterScale ||
		out.ScatterJitter != in.ScatterJitter || out.ScatterRange != in.ScatterRange {
		t.Fatalf("round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

// TestDetailLegacyDisplaceMigrates verifies a pre-waves level's single noise
// layer (amplitude/frequency/octaves/tess_level) loads as the noise layer.
func TestDetailLegacyDisplaceMigrates(t *testing.T) {
	out := detailFromRaw(&RawDetail{
		Preset: "rock", Seed: 7, Displace: true,
		Amplitude: 0.35, Frequency: 0.6, Octaves: 4, TessLevel: 24,
	})
	if !out.Noise || out.NoiseAmplitude != 0.35 || out.NoiseFrequency != 0.6 ||
		out.NoiseOctaves != 4 || out.NoiseTessLevel != 24 {
		t.Fatalf("legacy displace params did not migrate to the noise layer: %+v", out)
	}
	if out.tessLevelFor() != 24 {
		t.Fatalf("expected effective tessellation 24, got %.0f", out.tessLevelFor())
	}
	for i, w := range out.Waves {
		if w.Enabled {
			t.Fatalf("legacy load should leave wave %d disabled: %+v", i, w)
		}
	}
}

// ridgeSeparation returns how far apart two wave rotations are as ridge families,
// in degrees (0..90). A plane wave looks the same travelling forwards or
// backwards, so rotations 180 apart are the *same* pattern, not opposite ones.
func ridgeSeparation(a, b float32) float64 {
	d := math.Mod(math.Abs(float64(a-b)), 180)
	if d > 90 {
		d = 180 - d
	}
	return d
}

// TestDetailWaveRotationsDiffer verifies no two active waves land on nearly the
// same ridge direction. Near-parallel waves stack into one ridge family instead
// of reading as separate layers — which is what the old seed-hashed directions
// did (with the default seed, waves 1 and 2 came out 8 degrees apart and almost
// in phase). Covers both fresh slots and every preset.
func TestDetailWaveRotationsDiffer(t *testing.T) {
	const minSep = 15.0

	var defaults []float32
	for i := 0; i < detailMaxWaves; i++ {
		defaults = append(defaults, detailDefaultWave(i).Rotation)
	}
	checkSpread := func(what string, rots []float32) {
		for i := 0; i < len(rots); i++ {
			for j := i + 1; j < len(rots); j++ {
				if sep := ridgeSeparation(rots[i], rots[j]); sep < minSep {
					t.Fatalf("%s: waves %d and %d are only %.1f degrees apart (rotations %.1f, %.1f)",
						what, i+1, j+1, sep, rots[i], rots[j])
				}
			}
		}
	}
	checkSpread("default wave slots", defaults)

	for name, p := range detailPresets {
		var rots []float32
		for _, w := range p.Waves {
			if w.Enabled {
				rots = append(rots, w.Rotation)
			}
		}
		checkSpread(fmt.Sprintf("preset %q", name), rots)
	}
}

// TestDetailTessLevelIsMaxOfActiveLayers verifies disabled layers do not drive
// the shared per-patch subdivision level.
func TestDetailTessLevelIsMaxOfActiveLayers(t *testing.T) {
	d := DetailSpec{
		Waves: [detailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.2, Frequency: 0.5, TessLevel: 12},
			{Enabled: false, Amplitude: 0.2, Frequency: 0.5, TessLevel: 64},
			{Enabled: true, Amplitude: 0.2, Frequency: 0.5, TessLevel: 30},
		},
		Noise: false, NoiseTessLevel: 48,
	}
	if got := d.tessLevelFor(); got != 30 {
		t.Fatalf("expected 30 (max of enabled waves), got %.0f", got)
	}
	d.Noise = true
	if got := d.tessLevelFor(); got != 48 {
		t.Fatalf("expected 48 (noise layer active), got %.0f", got)
	}
}

// TestDetailDisabledOmitted verifies a disabled spec serializes to nil (omitted).
func TestDetailDisabledOmitted(t *testing.T) {
	if detailToRaw(DetailSpec{}) != nil {
		t.Fatal("disabled DetailSpec should serialize to nil")
	}
	if detailFromRaw(nil).Enabled {
		t.Fatal("nil RawDetail should load as disabled")
	}
}

// TestDispNormalIsFaceConstant checks that a NON-planar quad — a floor whose
// four corners have independent heights, split into two triangles — reports one
// normal for the whole face rather than each triangle's own. The displacement
// shaders push along this single normal so the two triangles stay welded along
// their shared diagonal instead of tearing open.
func TestDispNormalIsFaceConstant(t *testing.T) {
	// Quad (0,0)-(2,0)-(2,2)-(0,2) with corner heights 0,0,1,0 — the fan splits
	// it on the (0,0)-(2,2) diagonal into two triangles at different tilts.
	v := func(x, y, z float32) []float32 { return []float32{x, y, z, 0, 0} }
	var verts []float32
	tri := func(a, b, c []float32) {
		verts = append(verts, a...)
		verts = append(verts, b...)
		verts = append(verts, c...)
	}
	p00, p20, p22, p02 := v(0, 0, 0), v(2, 0, 0), v(2, 1, 2), v(0, 0, 2)
	tri(p00, p20, p22)
	tri(p00, p22, p02)

	// The two triangles really are non-coplanar, or the test proves nothing.
	triNrm := func(a, b, c []float32) mgl32.Vec3 {
		pa, pb, pc := vtx(a, 0), vtx(b, 0), vtx(c, 0)
		return pb.Sub(pa).Cross(pc.Sub(pa)).Normalize()
	}
	n1, n2 := triNrm(p00, p20, p22), triNrm(p00, p22, p02)
	if n1.Sub(n2).Len() < 1e-3 {
		t.Fatalf("test quad is planar (%v vs %v); it cannot exercise the seam", n1, n2)
	}

	geo := &Geometry{Vertices: verts}
	n := dispNormalFor(geo)
	if math.Abs(float64(n.Len()-1)) > 1e-4 {
		t.Fatalf("dispNormal must be unit length, got %v (len %.4f)", n, n.Len())
	}
	// It is the face's average plane, so it matches neither triangle exactly and
	// sits between them.
	if n.Sub(n1).Len() < 1e-4 || n.Sub(n2).Len() < 1e-4 {
		t.Fatalf("dispNormal %v is a single triangle's normal, not the face average", n)
	}
	if n.Dot(n1) < 0.9 || n.Dot(n2) < 0.9 {
		t.Fatalf("dispNormal %v should lie between the triangle normals %v / %v", n, n1, n2)
	}
	// Cached repeat must be identical, not merely close — the shader relies on
	// every patch of the face getting bit-identical uniforms.
	if again := dispNormalFor(geo); again != n {
		t.Fatalf("dispNormal not stable across calls: %v then %v", n, again)
	}
}

// TestComputeFaceBasis checks the plane basis + bounds for a flat floor quad in
// the XZ plane (y = 1), spanning X:[0,2], Z:[0,3], wound CCW seen from above.
func TestComputeFaceBasis(t *testing.T) {
	// Triangle-fan quad (0,0)-(2,0)-(2,3)-(0,3) at y=1; 5 floats/vertex [x,y,z,u,v].
	v := func(x, z float32) []float32 { return []float32{x, 1, z, 0, 0} }
	var verts []float32
	tri := func(a, b, c []float32) {
		verts = append(verts, a...)
		verts = append(verts, b...)
		verts = append(verts, c...)
	}
	p00, p20, p23, p03 := v(0, 0), v(2, 0), v(2, 3), v(0, 3)
	tri(p00, p20, p23)
	tri(p00, p23, p03)

	b := computeFaceBasis(verts)
	if !b.ok {
		t.Fatal("computeFaceBasis failed on a valid quad")
	}
	// Normal should be vertical (|n.y| ~ 1).
	if math.Abs(float64(b.n.Y())) < 0.99 {
		t.Fatalf("expected vertical normal, got %v", b.n)
	}
	// Bounding rectangle should cover the 2x3 extent (order of u/v may swap).
	uv := []float64{float64(b.uMax), float64(b.vMax)}
	got2, got3 := false, false
	for _, e := range uv {
		if math.Abs(e-2) < 1e-3 {
			got2 = true
		}
		if math.Abs(e-3) < 1e-3 {
			got3 = true
		}
	}
	if !got2 || !got3 {
		t.Fatalf("expected extents {2,3}, got uMax=%.3f vMax=%.3f", b.uMax, b.vMax)
	}
}

// TestComputeBoundaryEdges verifies a triangle-fan quad yields exactly its 4
// perimeter edges (the shared diagonal is interior and excluded), so the edge
// taper pins the true polygon boundary.
func TestComputeBoundaryEdges(t *testing.T) {
	v := func(x, z float32) []float32 { return []float32{x, 1, z, 0, 0} }
	var verts []float32
	tri := func(a, b, c []float32) {
		verts = append(verts, a...)
		verts = append(verts, b...)
		verts = append(verts, c...)
	}
	p00, p20, p23, p03 := v(0, 0), v(2, 0), v(2, 3), v(0, 3)
	tri(p00, p20, p23) // shares diagonal p00-p23
	tri(p00, p23, p03)

	edges := computeBoundaryEdges(verts)
	if len(edges) != 4 {
		t.Fatalf("expected 4 boundary edges for a quad fan, got %d", len(edges))
	}
	// The diagonal p00-p23 must NOT appear among the boundary edges.
	c00, c23 := mgl32.Vec3{0, 1, 0}, mgl32.Vec3{2, 1, 3}
	for _, e := range edges {
		onDiag := func(p mgl32.Vec3) bool { return approxV(p, c00) || approxV(p, c23) }
		if onDiag(e[0]) && onDiag(e[1]) {
			t.Fatalf("interior diagonal p00-p23 wrongly reported as boundary: %v", e)
		}
	}
}

func approxV(a, b mgl32.Vec3) bool {
	return math.Abs(float64(a.X()-b.X())) < 1e-3 &&
		math.Abs(float64(a.Y()-b.Y())) < 1e-3 &&
		math.Abs(float64(a.Z()-b.Z())) < 1e-3
}
