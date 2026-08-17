package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// worldUp is the dispNormal a floor face displaces along.
var worldUp = mgl32.Vec3{0, 1, 0}

// rockySpec returns a displacement spec with both a wave layer and noise
// active — the interesting case for every property below.
func rockySpec() *DetailSpec {
	d := &DetailSpec{
		Enabled:  true,
		Displace: true,
		Seed:     12345,
		Waves: [detailMaxWaves]DetailWave{
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
			if abs32(off) > bound+1e-5 {
				t.Fatalf("displacement %.4f at %v exceeds the advertised amplitude sum %.4f", off, p, bound)
			}
		}
	}
}

// TestDetailDisplaceAtFlatSpecIsIdentity checks a spec with nothing enabled
// leaves the position untouched, so a face that merely *has* a DetailSpec
// (Enabled but no waves, no noise) never silently shifts the biome layer.
func TestDetailDisplaceAtFlatSpecIsIdentity(t *testing.T) {
	d := &DetailSpec{Enabled: true, Displace: true, Seed: 7}
	p := mgl32.Vec3{3, 2, -4}
	if got := detailDisplaceAt(d, worldUp, nil, p); got != p {
		t.Fatalf("flat spec displaced %v to %v, want identity", p, got)
	}
}

// TestDetailDisplaceAtDisabledWaveSlotIgnored checks a slot with Enabled=false
// contributes nothing even when it still carries a stale amplitude/frequency
// (which it does — DetailSpec.clamp deliberately leaves disabled slots
// untouched so ensureDisplaceDefaults can tell "never configured" apart).
func TestDetailDisplaceAtDisabledWaveSlotIgnored(t *testing.T) {
	d := &DetailSpec{
		Enabled: true, Displace: true, Seed: 3,
		Waves: [detailMaxWaves]DetailWave{
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
	eps := dispNormalEps(d)

	for x := float32(-8); x <= 8; x += 1.3 {
		for z := float32(-8); z <= 8; z += 1.7 {
			n := detailNormalAt(d, worldUp, nil, eps, mgl32.Vec3{x, 0, z})
			if l := n.Len(); abs32(l-1) > 1e-4 {
				t.Fatalf("normal at (%.1f,%.1f) has length %.6f, want 1", x, z, l)
			}
			if n.Y() <= 0 {
				t.Fatalf("floor normal at (%.1f,%.1f) points downward: %v", x, z, n)
			}
		}
	}
}

// TestDetailNormalAtFlatSpecIsFaceNormal checks a spec that displaces nothing
// yields the face normal exactly, so an unshaped face reads as perfectly flat
// (coverage 1 for a below-an-angle kind) rather than picking up noise.
func TestDetailNormalAtFlatSpecIsFaceNormal(t *testing.T) {
	d := &DetailSpec{Enabled: true, Displace: true, Seed: 9}
	n := detailNormalAt(d, worldUp, nil, dispNormalEps(d), mgl32.Vec3{2, 0, 3})
	if (n.Sub(worldUp)).Len() > 1e-5 {
		t.Fatalf("flat spec normal = %v, want %v", n, worldUp)
	}
}

// TestDetailNormalAtVariesOverBumps checks the normals actually tilt across a
// displaced surface — without this the slope gating would be uniform and the
// whole feature a no-op. Asserts a real spread of angles, not just non-zero.
func TestDetailNormalAtVariesOverBumps(t *testing.T) {
	d := rockySpec()
	eps := dispNormalEps(d)

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
				v := detailFBM3(mgl32.Vec3{x, 0.5, z}, oct)
				if v < -1.001 || v > 1.001 {
					t.Fatalf("detailFBM3 octaves=%d at (%.1f,%.1f) = %v, outside [-1,1]", oct, x, z, v)
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
				v := detailValueNoise3(mgl32.Vec3{x, y, z})
				if v < 0 || v > 1 {
					t.Fatalf("detailValueNoise3(%.1f,%.1f,%.1f) = %v, outside [0,1]", x, y, z, v)
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
	for _, n := range []mgl32.Vec3{
		{0, 1, 0}, {0, -1, 0}, {1, 0, 0}, {0, 0, 1},
		mgl32.Vec3{0.3, 0.8, -0.5}.Normalize(),
	} {
		tan, bit := detailPlaneBasis(n)
		if l := tan.Len(); abs32(l-1) > 1e-5 {
			t.Errorf("n=%v: tangent length %v, want 1", n, l)
		}
		if l := bit.Len(); abs32(l-1) > 1e-5 {
			t.Errorf("n=%v: bitangent length %v, want 1", n, l)
		}
		if d := tan.Dot(bit); abs32(d) > 1e-5 {
			t.Errorf("n=%v: tangent·bitangent = %v, want 0", n, d)
		}
		if d := tan.Dot(n); abs32(d) > 1e-5 {
			t.Errorf("n=%v: tangent·normal = %v, want 0", n, d)
		}
		if c := tan.Cross(bit); (c.Sub(n)).Len() > 1e-5 {
			t.Errorf("n=%v: cross(t,b) = %v, want the face normal (right-handed)", n, c)
		}
	}
}

// TestSmoothstep32MatchesGLSL spot-checks the smoothstep port, including the
// degenerate edge0==edge1 case GLSL leaves undefined but slopeCoverage can
// reach (a zero-width feather band).
func TestSmoothstep32MatchesGLSL(t *testing.T) {
	if got := smoothstep32(0, 1, -1); got != 0 {
		t.Errorf("smoothstep32 below the range = %v, want 0", got)
	}
	if got := smoothstep32(0, 1, 2); got != 1 {
		t.Errorf("smoothstep32 above the range = %v, want 1", got)
	}
	if got := smoothstep32(0, 1, 0.5); abs32(got-0.5) > 1e-6 {
		t.Errorf("smoothstep32 midpoint = %v, want 0.5", got)
	}
	if got := smoothstep32(5, 5, 4); got != 0 {
		t.Errorf("degenerate smoothstep32 below = %v, want 0", got)
	}
	if got := smoothstep32(5, 5, 6); got != 1 {
		t.Errorf("degenerate smoothstep32 above = %v, want 1", got)
	}
}

// testSeed is an arbitrary but fixed per-sector scatter seed.
var testSeed = mgl32.Vec2{3.5, 11.25}

// flatFloorLevel returns a level with one 32x32 sector whose floor sits at
// y=5, carrying the given detail spec.
func flatFloorLevel(spec DetailSpec) *Level {
	return &Level{Chunks: []Chunk{{
		Points: []mgl32.Vec2{{-16, -16}, {16, -16}, {16, 16}, {-16, 16}},
		Sectors: []Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        Floor{Enabled: true, Height: 5, Detail: spec},
		}},
	}}}
}

// TestBiomeSurfaceAtFollowsDisplacedFloor is the end-to-end check that the
// bridge actually does something. It compares the same brush over a displaced
// floor against an otherwise identical undisplaced one, which isolates
// "follows the face's displacement" from the ground patch's own procedural
// noise (both levels get that, so it cancels): a following kind's height must
// diverge, and its coverage must dip below 1 somewhere (slope gating
// engaging), while a non-following kind must come out bit-identical.
func TestBiomeSurfaceAtFollowsDisplacedFloor(t *testing.T) {
	displaced := flatFloorLevel(*rockySpec())
	plain := flatFloorLevel(DetailSpec{})

	// An authored slope ceiling, since the shipped default range covers
	// everything and would never gate — see TestResolvedSlopeDefaultsGateNothing.
	grass := &BiomeBrush{Kind: BiomeGrass, MaxSlopeAngle: 25}
	diverged, sawPartialCoverage := false, false
	for x := float32(-12); x <= 12; x += 0.7 {
		for z := float32(-12); z <= 12; z += 0.9 {
			h, cov := biomeSurfaceAt(displaced, grass, testSeed, x, z)
			flatH, flatCov := biomeSurfaceAt(plain, grass, testSeed, x, z)
			if abs32(h-flatH) > 1e-4 {
				diverged = true
			}
			if flatCov != 1 {
				t.Fatalf("coverage over an *undisplaced* floor at (%.1f,%.1f) = %v, want 1", x, z, flatCov)
			}
			if cov < 1 {
				sawPartialCoverage = true
			}
			if cov < 0 || cov > 1 {
				t.Fatalf("coverage %v at (%.1f,%.1f) outside [0,1]", cov, x, z)
			}
		}
	}
	if !diverged {
		t.Error("Grass height over a displaced floor never diverged from the undisplaced one — the bridge isn't wired up")
	}
	if !sawPartialCoverage {
		t.Error("Grass coverage was 1 everywhere over a displaced floor — slope gating never engaged")
	}

	// Cattail opts out of displacement entirely, so the displaced and plain
	// floors must agree exactly — same code path, same inputs.
	cattail := &BiomeBrush{Kind: BiomeCattail}
	for x := float32(-12); x <= 12; x += 1.9 {
		for z := float32(-12); z <= 12; z += 2.3 {
			h, cov := biomeSurfaceAt(displaced, cattail, testSeed, x, z)
			flatH, _ := biomeSurfaceAt(plain, cattail, testSeed, x, z)
			if h != flatH {
				t.Fatalf("Cattail height at (%.1f,%.1f) = %v over a displaced floor but %v over a flat one — it must not follow displacement", x, z, h, flatH)
			}
			if cov != 1 {
				t.Fatalf("Cattail coverage at (%.1f,%.1f) = %v, want 1", x, z, cov)
			}
		}
	}
}

// TestGroundPatchMeshMatchesScatterHeight is the regression test for the
// reported bug: moss scattered as "discs floating in thin air". The ground
// patch mesh and the scatter height texture computed their heights separately
// and disagreed — the patch added groundPatchYBias + groundDisplacement while
// the scatter height used the bare floor — so every instance was planted
// under the visible patch, floating wherever the patch noise rose. Tall grass
// blades hid it; a short moss clump did not.
//
// Both now go through biomeSurfaceAt, so every vertex the patch mesh emits
// must equal what the height texture would sample at that same XZ.
func TestGroundPatchMeshMatchesScatterHeight(t *testing.T) {
	lvl := flatFloorLevel(*rockySpec())

	for _, kind := range allBiomeKinds {
		s := &BiomeSector{
			ID:     "gp",
			Points: []mgl32.Vec2{{-10, -10}, {10, -10}, {10, 10}, {-10, 10}},
			Brush:  defaultBiomeBrush(kind),
		}
		verts := buildGroundPatchMesh(lvl, s)
		if len(verts) == 0 {
			t.Fatalf("kind %d produced an empty ground patch mesh", kind)
		}
		seed := biomeSeed(s)

		for i := 0; i < len(verts); i += groundVertexStride {
			x, y, z := verts[i], verts[i+1], verts[i+2]
			want, _ := biomeSurfaceAt(lvl, &s.Brush, seed, x, z)
			if abs32(y-want) > 1e-4 {
				t.Fatalf("kind %d: ground patch vertex at (%.3f,%.3f) sits at y=%.5f but scatter would be planted at y=%.5f — "+
					"the patch and the scatter height must come from the same surface", kind, x, z, y, want)
			}
		}
	}
}

// groundPatchCoverageRange returns the min/max of the per-vertex aCoverage
// attribute across a built ground patch mesh (the last float of each vertex,
// see groundVertexStride).
func groundPatchCoverageRange(t *testing.T, verts []float32) (min, max float32) {
	t.Helper()
	if len(verts) == 0 {
		t.Fatal("empty ground patch mesh")
	}
	min, max = 1, 0
	for i := 0; i < len(verts); i += groundVertexStride {
		c := verts[i+8]
		if c < 0 || c > 1 {
			t.Fatalf("baked coverage %v outside [0,1]", c)
		}
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	return min, max
}

// TestGroundPatchIsSlopeGated is the regression test for "changing the angle
// still has no effect": a Moss sector is ground-patch-only unless MossScatter
// is on, and the patch originally ignored coverage entirely ("it always drapes
// fully"), so the slope sliders were invisible no matter how steep the ground.
// The patch now bakes coverage per vertex for ground_frag.glsl to threshold,
// so a range excluding the terrain's angle must drive it to 0 and a permissive
// one must leave it at 1.
func TestGroundPatchIsSlopeGated(t *testing.T) {
	// A steep floor: two corners raised 12 over a 16-unit span (~37 degrees).
	lvl := flatFloorAt(0, []float32{0, 0, 12, 12})

	_, n, _ := floorHeightNormalUnderLevel(lvl, 0, 0)
	angle := float32(math.Acos(float64(n.Normalize().Y()))) * 180 / math.Pi
	if angle < 20 {
		t.Fatalf("fixture is only %.1f degrees; expected a steep floor", angle)
	}

	newPatch := func(minA, maxA float32) []float32 {
		s := &BiomeSector{
			ID:     "slopegate",
			Points: []mgl32.Vec2{{-7, -7}, {7, -7}, {7, 7}, {-7, 7}},
			Brush:  defaultBiomeBrush(BiomeMoss),
		}
		s.Brush.MinSlopeAngle, s.Brush.MaxSlopeAngle = minA, maxA
		return buildGroundPatchMesh(lvl, s)
	}

	// Permissive: fully covered everywhere.
	if lo, hi := groundPatchCoverageRange(t, newPatch(0, 90)); lo != 1 || hi != 1 {
		t.Errorf("patch coverage with a 0..90 range = %v..%v, want all 1", lo, hi)
	}

	// Ceiling well below the terrain's angle: excluded everywhere, so
	// ground_frag.glsl discards the whole patch.
	if lo, hi := groundPatchCoverageRange(t, newPatch(0, angle-slopeFeatherDegrees-3)); lo != 0 || hi != 0 {
		t.Errorf("patch coverage with a ceiling below the terrain's %.1f degrees = %v..%v, want all 0 — "+
			"the ground patch is not honouring the slope range", angle, lo, hi)
	}

	// Floor (minimum) well above it: also excluded everywhere.
	if lo, hi := groundPatchCoverageRange(t, newPatch(angle+slopeFeatherDegrees+3, 90)); lo != 0 || hi != 0 {
		t.Errorf("patch coverage with a minimum above the terrain's %.1f degrees = %v..%v, want all 0", angle, lo, hi)
	}

	// A kind that opts out of displacement must stay fully covered regardless.
	s := &BiomeSector{
		ID:     "cattail",
		Points: []mgl32.Vec2{{-7, -7}, {7, -7}, {7, 7}, {-7, 7}},
		Brush:  defaultBiomeBrush(BiomeCattail),
	}
	s.Brush.MinSlopeAngle, s.Brush.MaxSlopeAngle = 80, 90
	if lo, hi := groundPatchCoverageRange(t, buildGroundPatchMesh(lvl, s)); lo != 1 || hi != 1 {
		t.Errorf("Cattail patch coverage = %v..%v, want all 1 (it never follows displacement)", lo, hi)
	}
}

// TestBiomeSurfaceAtIncludesGroundPatchOffsets pins the other half of that
// fix: the returned height is the *rendered* patch surface, so it must include
// the patch's z-fight bias and its procedural displacement rather than being
// the bare floor height. Without this, a future refactor could quietly go back
// to returning the raw floor height and re-plant everything underground.
func TestBiomeSurfaceAtIncludesGroundPatchOffsets(t *testing.T) {
	lvl := flatFloorLevel(DetailSpec{}) // undisplaced: isolate the patch's own offsets
	brush := defaultBiomeBrush(BiomeMoss)

	sawAbove, sawBelow, differsFromFloor := false, false, false
	for x := float32(-10); x <= 10; x += 0.6 {
		for z := float32(-10); z <= 10; z += 0.8 {
			h, _ := biomeSurfaceAt(lvl, &brush, testSeed, x, z)
			if abs32(h-5) > 1e-4 {
				differsFromFloor = true
			}
			if h > 5 {
				sawAbove = true
			}
			if h < 5 {
				sawBelow = true
			}
			// Bias plus the kind's noise amplitude bounds how far it can stray.
			if bound := groundPatchYBias + brush.GroundAmplitude + 1e-4; abs32(h-5) > bound {
				t.Fatalf("height %v at (%.1f,%.1f) strays further from the floor than bias+amplitude (%v)", h, x, z, bound)
			}
		}
	}
	if !differsFromFloor {
		t.Fatal("height equals the bare floor everywhere — groundPatchYBias/groundDisplacement are not being applied")
	}
	if !sawAbove || !sawBelow {
		t.Errorf("ground noise should push the surface both above and below the floor (above=%v below=%v)", sawAbove, sawBelow)
	}
}

// TestBiomeSurfaceAtIgnoresDisplaceOffFace checks a face carrying a detail
// spec with Displace turned off is treated as flat — HasDisplace() gates the
// whole path, matching how the renderer decides whether to tessellate. Its
// heights must match an undisplaced floor's exactly.
func TestBiomeSurfaceAtIgnoresDisplaceOffFace(t *testing.T) {
	spec := *rockySpec()
	spec.Displace = false // scatter-only detail: the surface itself stays flat
	scatterOnly := flatFloorLevel(spec)
	plain := flatFloorLevel(DetailSpec{})

	brush := &BiomeBrush{Kind: BiomeGrass}
	for x := float32(-8); x <= 8; x += 2.1 {
		for z := float32(-8); z <= 8; z += 2.7 {
			h, cov := biomeSurfaceAt(scatterOnly, brush, testSeed, x, z)
			want, _ := biomeSurfaceAt(plain, brush, testSeed, x, z)
			if h != want {
				t.Fatalf("height at (%.1f,%.1f) = %v over a Displace=false face, want the undisplaced %v", x, z, h, want)
			}
			if cov != 1 {
				t.Fatalf("coverage at (%.1f,%.1f) = %v over a Displace=false face, want 1", x, z, cov)
			}
		}
	}
}

// TestMossClumpIsSunkAndNotAFlatDisc pins the mesh side of the floating-disc
// fix: the clump must sit mostly *below* its origin (so uneven ground closes
// over its rim — it is drawn with a random yaw but no tilt toward the surface
// normal, so a clump balanced exactly on the origin lifts off wherever the
// ground falls away) and must stay a mound rather than a razor-thin disc.
func TestMossClumpIsSunkAndNotAFlatDisc(t *testing.T) {
	mesh := buildMossMesh()
	if len(mesh) == 0 {
		t.Fatal("buildMossMesh returned no vertices")
	}

	minY, maxY := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	maxAbsXZ := float32(0)
	for i := 0; i < len(mesh); i += biomeVertexStride {
		x, y, z := mesh[i], mesh[i+1], mesh[i+2]
		minY, maxY = min(minY, y), max(maxY, y)
		maxAbsXZ = max(maxAbsXZ, max(abs32(x), abs32(z)))
	}

	if minY >= 0 {
		t.Fatalf("moss clump's lowest vertex is at y=%.3f — it must extend below the origin to sit buried", minY)
	}
	if maxY <= 0 {
		t.Fatalf("moss clump is entirely below the origin (top at y=%.3f) — it would be invisible", maxY)
	}
	if buried := -minY / (maxY - minY); buried < 0.5 {
		t.Errorf("only %.0f%% of the moss clump sits below the origin; want at least half buried so its rim never lifts off uneven ground", buried*100)
	}

	// Squashed relative to its width, but not a degenerate plate.
	width := maxAbsXZ * 2
	height := maxY - minY
	if height >= width {
		t.Errorf("moss clump is %.3f tall by %.3f wide — expected a low, squashed mound", height, width)
	}
	if height < width*0.15 {
		t.Errorf("moss clump is %.3f tall by %.3f wide — too disc-like; a flat plate reads as floating on uneven ground", height, width)
	}
}
