package main

import (
	"foundry/internal/biome"
	"foundry/internal/geom"
	"foundry/internal/render"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// biome_displace_surface_test.go — biome scatter and ground patches over a
// displaced floor.
//
// Split from internal/detail's tests: those cover the CPU port of the
// displacement shader, these cover what the biome code does with it, and the
// biome code sits a layer above.

// testSeed is an arbitrary but fixed per-sector scatter seed.
var testSeed = mgl32.Vec2{3.5, 11.25}

// flatFloorLevel returns a level with one 32x32 sector whose floor sits at
// y=5, carrying the given detail spec.
func flatFloorLevel(spec world.DetailSpec) *world.Level {
	return &world.Level{Chunks: []world.Chunk{{
		Points: []mgl32.Vec2{{-16, -16}, {16, -16}, {16, 16}, {-16, 16}},
		Sectors: []world.Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: 5, Detail: spec},
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
	plain := flatFloorLevel(world.DetailSpec{})

	// An authored slope ceiling, since the shipped default range covers
	// everything and would never gate — see TestResolvedSlopeDefaultsGateNothing.
	grass := &world.BiomeBrush{Kind: world.BiomeGrass, MaxSlopeAngle: 25}
	diverged, sawPartialCoverage := false, false
	for x := float32(-12); x <= 12; x += 0.7 {
		for z := float32(-12); z <= 12; z += 0.9 {
			h, cov := biome.BiomeSurfaceAt(displaced, grass, testSeed, x, z)
			flatH, flatCov := biome.BiomeSurfaceAt(plain, grass, testSeed, x, z)
			if geom.Abs32(h-flatH) > 1e-4 {
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
	cattail := &world.BiomeBrush{Kind: world.BiomeCattail}
	for x := float32(-12); x <= 12; x += 1.9 {
		for z := float32(-12); z <= 12; z += 2.3 {
			h, cov := biome.BiomeSurfaceAt(displaced, cattail, testSeed, x, z)
			flatH, _ := biome.BiomeSurfaceAt(plain, cattail, testSeed, x, z)
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
// and disagreed — the patch added biome.GroundPatchYBias + biome.GroundDisplacement while
// the scatter height used the bare floor — so every instance was planted
// under the visible patch, floating wherever the patch noise rose. Tall grass
// blades hid it; a short moss clump did not.
//
// Both now go through biomeSurfaceAt, so every vertex the patch mesh emits
// must equal what the height texture would sample at that same XZ.
func TestGroundPatchMeshMatchesScatterHeight(t *testing.T) {
	lvl := flatFloorLevel(*rockySpec())

	for _, kind := range allBiomeKinds {
		s := &world.BiomeSector{
			ID:     "gp",
			Points: []mgl32.Vec2{{-10, -10}, {10, -10}, {10, 10}, {-10, 10}},
			Brush:  world.DefaultBiomeBrush(kind),
		}
		verts := render.BuildGroundPatchMesh(lvl, s)
		if len(verts) == 0 {
			t.Fatalf("kind %d produced an empty ground patch mesh", kind)
		}
		seed := world.BiomeSeed(s)

		for i := 0; i < len(verts); i += render.GroundVertexStride {
			x, y, z := verts[i], verts[i+1], verts[i+2]
			want, _ := biome.BiomeSurfaceAt(lvl, &s.Brush, seed, x, z)
			if geom.Abs32(y-want) > 1e-4 {
				t.Fatalf("kind %d: ground patch vertex at (%.3f,%.3f) sits at y=%.5f but scatter would be planted at y=%.5f — "+
					"the patch and the scatter height must come from the same surface", kind, x, z, y, want)
			}
		}
	}
}

// groundPatchCoverageRange returns the min/max of the per-vertex aCoverage
// attribute across a built ground patch mesh (the last float of each vertex,
// see render.GroundVertexStride).
func groundPatchCoverageRange(t *testing.T, verts []float32) (min, max float32) {
	t.Helper()
	if len(verts) == 0 {
		t.Fatal("empty ground patch mesh")
	}
	min, max = 1, 0
	for i := 0; i < len(verts); i += render.GroundVertexStride {
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

	_, N, _ := biome.FloorHeightNormalUnderLevel(lvl, 0, 0)
	angle := float32(math.Acos(float64(N.Normalize().Y()))) * 180 / math.Pi
	if angle < 20 {
		t.Fatalf("fixture is only %.1f degrees; expected a steep floor", angle)
	}

	newPatch := func(minA, maxA float32) []float32 {
		s := &world.BiomeSector{
			ID:     "slopegate",
			Points: []mgl32.Vec2{{-7, -7}, {7, -7}, {7, 7}, {-7, 7}},
			Brush:  world.DefaultBiomeBrush(world.BiomeMoss),
		}
		s.Brush.MinSlopeAngle, s.Brush.MaxSlopeAngle = minA, maxA
		return render.BuildGroundPatchMesh(lvl, s)
	}

	// Permissive: fully covered everywhere.
	if lo, hi := groundPatchCoverageRange(t, newPatch(0, 90)); lo != 1 || hi != 1 {
		t.Errorf("patch coverage with a 0..90 range = %v..%v, want all 1", lo, hi)
	}

	// Ceiling well below the terrain's angle: excluded everywhere, so
	// ground_frag.glsl discards the whole patch.
	if lo, hi := groundPatchCoverageRange(t, newPatch(0, angle-biome.SlopeFeatherDegrees-3)); lo != 0 || hi != 0 {
		t.Errorf("patch coverage with a ceiling below the terrain's %.1f degrees = %v..%v, want all 0 — "+
			"the ground patch is not honouring the slope range", angle, lo, hi)
	}

	// Floor (minimum) well above it: also excluded everywhere.
	if lo, hi := groundPatchCoverageRange(t, newPatch(angle+biome.SlopeFeatherDegrees+3, 90)); lo != 0 || hi != 0 {
		t.Errorf("patch coverage with a minimum above the terrain's %.1f degrees = %v..%v, want all 0", angle, lo, hi)
	}

	// A kind that opts out of displacement must stay fully covered regardless.
	s := &world.BiomeSector{
		ID:     "cattail",
		Points: []mgl32.Vec2{{-7, -7}, {7, -7}, {7, 7}, {-7, 7}},
		Brush:  world.DefaultBiomeBrush(world.BiomeCattail),
	}
	s.Brush.MinSlopeAngle, s.Brush.MaxSlopeAngle = 80, 90
	if lo, hi := groundPatchCoverageRange(t, render.BuildGroundPatchMesh(lvl, s)); lo != 1 || hi != 1 {
		t.Errorf("Cattail patch coverage = %v..%v, want all 1 (it never follows displacement)", lo, hi)
	}
}

// TestBiomeSurfaceAtIncludesGroundPatchOffsets pins the other half of that
// fix: the returned height is the *rendered* patch surface, so it must include
// the patch's z-fight bias and its procedural displacement rather than being
// the bare floor height. Without this, a future refactor could quietly go back
// to returning the raw floor height and re-plant everything underground.
func TestBiomeSurfaceAtIncludesGroundPatchOffsets(t *testing.T) {
	lvl := flatFloorLevel(world.DetailSpec{}) // undisplaced: isolate the patch's own offsets
	brush := world.DefaultBiomeBrush(world.BiomeMoss)

	sawAbove, sawBelow, differsFromFloor := false, false, false
	for x := float32(-10); x <= 10; x += 0.6 {
		for z := float32(-10); z <= 10; z += 0.8 {
			h, _ := biome.BiomeSurfaceAt(lvl, &brush, testSeed, x, z)
			if geom.Abs32(h-5) > 1e-4 {
				differsFromFloor = true
			}
			if h > 5 {
				sawAbove = true
			}
			if h < 5 {
				sawBelow = true
			}
			// Bias plus the kind's noise amplitude bounds how far it can stray.
			if bound := biome.GroundPatchYBias + brush.GroundAmplitude + 1e-4; geom.Abs32(h-5) > bound {
				t.Fatalf("height %v at (%.1f,%.1f) strays further from the floor than bias+amplitude (%v)", h, x, z, bound)
			}
		}
	}
	if !differsFromFloor {
		t.Fatal("height equals the bare floor everywhere — biome.GroundPatchYBias/biome.GroundDisplacement are not being applied")
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
	plain := flatFloorLevel(world.DetailSpec{})

	brush := &world.BiomeBrush{Kind: world.BiomeGrass}
	for x := float32(-8); x <= 8; x += 2.1 {
		for z := float32(-8); z <= 8; z += 2.7 {
			h, cov := biome.BiomeSurfaceAt(scatterOnly, brush, testSeed, x, z)
			want, _ := biome.BiomeSurfaceAt(plain, brush, testSeed, x, z)
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
	mesh := render.BuildMossMesh()
	if len(mesh) == 0 {
		t.Fatal("render.BuildMossMesh returned no vertices")
	}

	minY, maxY := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	maxAbsXZ := float32(0)
	for i := 0; i < len(mesh); i += render.BiomeVertexStride {
		x, y, z := mesh[i], mesh[i+1], mesh[i+2]
		minY, maxY = min(minY, y), max(maxY, y)
		maxAbsXZ = max(maxAbsXZ, max(geom.Abs32(x), geom.Abs32(z)))
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

func flatFloorAt(height float32, offsets []float32) *world.Level {
	return &world.Level{Chunks: []world.Chunk{{
		Points: []mgl32.Vec2{{-8, -8}, {8, -8}, {8, 8}, {-8, 8}},
		Sectors: []world.Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: height, VertexOffsets: offsets},
		}},
	}}}
}
