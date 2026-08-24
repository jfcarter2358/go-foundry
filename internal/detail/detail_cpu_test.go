package detail

import (
	"foundry/internal/geom"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// worldUp is the dispNormal a floor face displaces along.
var worldUp = mgl32.Vec3{0, 1, 0}

// rockySpec returns a displacement spec with both a wave layer and noise
// active — the interesting case for every property below.
func rockySpec() *world.DetailSpec {
	d := &world.DetailSpec{
		Enabled:  true,
		Displace: true,
		Seed:     12345,
		Waves: [world.DetailMaxWaves]world.DetailWave{
			{Enabled: true, Amplitude: 0.5, Frequency: 0.4, Rotation: 0, TessLevel: 24},
			{Enabled: true, Amplitude: 0.25, Frequency: 1.1, Rotation: 65, TessLevel: 24},
			{},
		},
		Noise: true, NoiseAmplitude: 0.2, NoiseFrequency: 1.2, NoiseOctaves: 4, NoiseTessLevel: 24,
	}
	return d
}

// TestDetailDisplaceAtIsPure checks the CPU port is a pure function of world
// position — the same property detail_common.glsl relies on for crack-free
// patches. Two calls at the same point must agree exactly, and displacement
// must move only along the face normal (nothing sideways).
func TestDetailDisplaceAtIsPure(t *testing.T) {
	d := rockySpec()
	for x := float32(-5); x <= 5; x += 1.3 {
		for z := float32(-5); z <= 5; z += 1.7 {
			p := mgl32.Vec3{x, 0, z}
			a := detailDisplaceAt(d, worldUp, nil, p)
			b := detailDisplaceAt(d, worldUp, nil, p)
			if a != b {
				t.Fatalf("detailDisplaceAt not deterministic at %v: %v vs %v", p, a, b)
			}
			if a.X() != p.X() || a.Z() != p.Z() {
				t.Fatalf("displacement at %v moved sideways to %v — must run along the face normal only", p, a)
			}
		}
	}
}

// TestDetailDisplaceAtBoundedByAmplitudes checks the offset never exceeds the
// sum of the amplitudes the spec advertises. detailPad (detail_render.go)
// grows cull bounds by exactly that sum, so a violation here would mean
// geometry culled while still on screen.
func TestDetailDisplaceAtBoundedByAmplitudes(t *testing.T) {
	d := rockySpec()
	bound := d.Waves[0].Amplitude + d.Waves[1].Amplitude + d.NoiseAmplitude

	for x := float32(-20); x <= 20; x += 0.9 {
		for z := float32(-20); z <= 20; z += 1.1 {
			p := mgl32.Vec3{x, 0, z}
			off := detailDisplaceAt(d, worldUp, nil, p).Y() - p.Y()
			if geom.Abs32(off) > bound+1e-5 {
				t.Fatalf("displacement %.4f at %v exceeds the advertised amplitude sum %.4f", off, p, bound)
			}
		}
	}
}

// TestDetailDisplaceAtFlatSpecIsIdentity checks a spec with nothing enabled
// leaves the position untouched, so a face that merely *has* a DetailSpec
// (Enabled but no waves, no noise) never silently shifts the biome layer.
func TestDetailDisplaceAtFlatSpecIsIdentity(t *testing.T) {
	d := &world.DetailSpec{Enabled: true, Displace: true, Seed: 7}
	p := mgl32.Vec3{3, 2, -4}
	if got := detailDisplaceAt(d, worldUp, nil, p); got != p {
		t.Fatalf("flat spec displaced %v to %v, want identity", p, got)
	}
}

// TestDetailDisplaceAtDisabledWaveSlotIgnored checks a slot with Enabled=false
// contributes nothing even when it still carries a stale amplitude/frequency
// (which it does — DetailSpec.Clamp deliberately leaves disabled slots
// untouched so ensureDisplaceDefaults can tell "never configured" apart).
func TestDetailDisplaceAtDisabledWaveSlotIgnored(t *testing.T) {
	d := &world.DetailSpec{
		Enabled: true, Displace: true, Seed: 3,
		Waves: [world.DetailMaxWaves]world.DetailWave{
			{Enabled: false, Amplitude: 5, Frequency: 2, TessLevel: 16},
			{}, {},
		},
	}
	p := mgl32.Vec3{1, 0, 1}
	if got := detailDisplaceAt(d, worldUp, nil, p); got != p {
		t.Fatalf("disabled wave slot contributed displacement: %v -> %v", p, got)
	}
}

// TestDetailTaperAtPinsBoundary checks the edge taper reaches 0 exactly on a
// boundary edge and 1 well inside it — the property that keeps a displaced
// floor meeting its adjacent walls seamlessly.
func TestDetailTaperAtPinsBoundary(t *testing.T) {
	d := rockySpec()
	d.EdgeTaper = 1.0
	// One boundary edge along the X axis at z=0.
	edges := [][2]mgl32.Vec3{{{-10, 0, 0}, {10, 0, 0}}}

	onEdge := detailTaperAt(d, edges, mgl32.Vec3{0, 0, 0})
	if onEdge != 0 {
		t.Errorf("taper on the boundary edge = %v, want 0", onEdge)
	}
	inside := detailTaperAt(d, edges, mgl32.Vec3{0, 0, 5})
	if inside != 1 {
		t.Errorf("taper well inside the face = %v, want 1", inside)
	}
	mid := detailTaperAt(d, edges, mgl32.Vec3{0, 0, 0.5})
	if mid <= 0 || mid >= 1 {
		t.Errorf("taper half a band in = %v, want a partial value", mid)
	}

	// With the taper pinning it, displacement on the edge must be zero even
	// though the underlying field is non-zero there.
	p := mgl32.Vec3{0, 0, 0}
	if got := detailDisplaceAt(d, worldUp, edges, p); got != p {
		t.Errorf("displacement on a tapered boundary edge = %v, want %v", got, p)
	}
}

// TestDetailTaperAtDisabled checks EdgeTaper<=0 means no tapering at all
// (matching the shader's early-out), so boundary edges are irrelevant then.
func TestDetailTaperAtDisabled(t *testing.T) {
	d := rockySpec()
	d.EdgeTaper = 0
	edges := [][2]mgl32.Vec3{{{-10, 0, 0}, {10, 0, 0}}}
	if got := detailTaperAt(d, edges, mgl32.Vec3{0, 0, 0}); got != 1 {
		t.Fatalf("taper with EdgeTaper=0 = %v, want 1", got)
	}
}

// TestDetailNormalAtIsUnitAndUpward checks the differentiated normal is always
// unit length and, on a floor, never points downward — slopeCoverage derives
// an angle from acos(n.Y), which a non-unit or flipped normal would corrupt.
func TestDetailNormalAtIsUnitAndUpward(t *testing.T) {
	d := rockySpec()
	eps := DispNormalEps(d)

	for x := float32(-8); x <= 8; x += 1.3 {
		for z := float32(-8); z <= 8; z += 1.7 {
			N := detailNormalAt(d, worldUp, nil, eps, mgl32.Vec3{x, 0, z})
			if l := N.Len(); geom.Abs32(l-1) > 1e-4 {
				t.Fatalf("normal at (%.1f,%.1f) has length %.6f, want 1", x, z, l)
			}
			if N.Y() <= 0 {
				t.Fatalf("floor normal at (%.1f,%.1f) points downward: %v", x, z, N)
			}
		}
	}
}

// TestDetailNormalAtFlatSpecIsFaceNormal checks a spec that displaces nothing
// yields the face normal exactly, so an unshaped face reads as perfectly flat
// (coverage 1 for a below-an-angle kind) rather than picking up noise.
func TestDetailNormalAtFlatSpecIsFaceNormal(t *testing.T) {
	d := &world.DetailSpec{Enabled: true, Displace: true, Seed: 9}
	N := detailNormalAt(d, worldUp, nil, DispNormalEps(d), mgl32.Vec3{2, 0, 3})
	if (N.Sub(worldUp)).Len() > 1e-5 {
		t.Fatalf("flat spec normal = %v, want %v", N, worldUp)
	}
}

// TestDetailNormalAtVariesOverBumps checks the normals actually tilt across a
// displaced surface — without this the slope gating would be uniform and the
// whole feature a no-op. Asserts a real spread of angles, not just non-zero.
func TestDetailNormalAtVariesOverBumps(t *testing.T) {
	d := rockySpec()
	eps := DispNormalEps(d)

	minY, maxY := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for x := float32(-12); x <= 12; x += 0.35 {
		for z := float32(-12); z <= 12; z += 0.45 {
			y := detailNormalAt(d, worldUp, nil, eps, mgl32.Vec3{x, 0, z}).Y()
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxY-minY < 0.1 {
		t.Fatalf("normal Y spread %.4f over a rocky spec is implausibly flat (min %.4f, max %.4f)", maxY-minY, minY, maxY)
	}
	// Steepest point should be well off vertical for these amplitudes.
	steepest := float64(math.Acos(float64(minY))) * 180 / math.Pi
	if steepest < 10 {
		t.Errorf("steepest slope over a rocky spec is only %.1f degrees — expected a meaningfully tilted surface", steepest)
	}
}

// TestDetailFBM3Bounded checks the fractal noise stays in [-1,1] like its GLSL
// counterpart, for every supported octave count (including out-of-range
// inputs, which must clamp rather than run away).
func TestDetailFBM3Bounded(t *testing.T) {
	for _, oct := range []int{-1, 0, 1, 3, 6, 99} {
		for x := float32(0); x < 12; x += 1.1 {
			for z := float32(0); z < 12; z += 1.3 {
				V := detailFBM3(mgl32.Vec3{x, 0.5, z}, oct)
				if V < -1.001 || V > 1.001 {
					t.Fatalf("detailFBM3 octaves=%d at (%.1f,%.1f) = %v, outside [-1,1]", oct, x, z, V)
				}
			}
		}
	}
}

// TestDetailValueNoise3Bounded checks the underlying value noise stays in
// [0,1] — d_vnoise's contract, which d_fbm's own bounds depend on.
func TestDetailValueNoise3Bounded(t *testing.T) {
	for x := float32(0); x < 8; x += 0.7 {
		for y := float32(0); y < 8; y += 0.9 {
			for z := float32(0); z < 8; z += 1.1 {
				V := detailValueNoise3(mgl32.Vec3{x, y, z})
				if V < 0 || V > 1 {
					t.Fatalf("detailValueNoise3(%.1f,%.1f,%.1f) = %v, outside [0,1]", x, y, z, V)
				}
			}
		}
	}
}

// TestDetailPlaneBasisIsRightHanded checks (t, b, n) is orthonormal and
// right-handed — detail_common.glsl notes cross(t,b) must equal the face
// normal, which is what makes the differentiated normal face outwards rather
// than into the surface.
func TestDetailPlaneBasisIsRightHanded(t *testing.T) {
	for _, N := range []mgl32.Vec3{
		{0, 1, 0}, {0, -1, 0}, {1, 0, 0}, {0, 0, 1},
		mgl32.Vec3{0.3, 0.8, -0.5}.Normalize(),
	} {
		tan, bit := detailPlaneBasis(N)
		if l := tan.Len(); geom.Abs32(l-1) > 1e-5 {
			t.Errorf("n=%v: tangent length %v, want 1", N, l)
		}
		if l := bit.Len(); geom.Abs32(l-1) > 1e-5 {
			t.Errorf("n=%v: bitangent length %v, want 1", N, l)
		}
		if d := tan.Dot(bit); geom.Abs32(d) > 1e-5 {
			t.Errorf("n=%v: tangent·bitangent = %v, want 0", N, d)
		}
		if d := tan.Dot(N); geom.Abs32(d) > 1e-5 {
			t.Errorf("n=%v: tangent·normal = %v, want 0", N, d)
		}
		if c := tan.Cross(bit); (c.Sub(N)).Len() > 1e-5 {
			t.Errorf("n=%v: cross(t,b) = %v, want the face normal (right-handed)", N, c)
		}
	}
}

// TestSmoothstep32MatchesGLSL spot-checks the smoothstep port, including the
// degenerate edge0==edge1 case GLSL leaves undefined but slopeCoverage can
// reach (a zero-width feather band).
func TestSmoothstep32MatchesGLSL(t *testing.T) {
	if got := Smoothstep32(0, 1, -1); got != 0 {
		t.Errorf("Smoothstep32 below the range = %v, want 0", got)
	}
	if got := Smoothstep32(0, 1, 2); got != 1 {
		t.Errorf("Smoothstep32 above the range = %v, want 1", got)
	}
	if got := Smoothstep32(0, 1, 0.5); geom.Abs32(got-0.5) > 1e-6 {
		t.Errorf("Smoothstep32 midpoint = %v, want 0.5", got)
	}
	if got := Smoothstep32(5, 5, 4); got != 0 {
		t.Errorf("degenerate Smoothstep32 below = %v, want 0", got)
	}
	if got := Smoothstep32(5, 5, 6); got != 1 {
		t.Errorf("degenerate Smoothstep32 above = %v, want 1", got)
	}
}
