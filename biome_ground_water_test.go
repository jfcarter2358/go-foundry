package main

import (
	"foundry/internal/geom"
	"foundry/internal/world"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// allBiomeKinds is every paintable kind, so the "every kind" tests below
// can't silently stop covering everything when a new one is appended (adding
// Moss did exactly that to the two tests that used to hardcode their lists).
var allBiomeKinds = []int32{
	world.BiomeGrass, world.BiomeRock, world.BiomeWildflower, world.BiomeCattail, world.BiomeSand, world.BiomeWater, world.BiomeMoss,
}

// TestAllBiomeKindsIsComplete guards allBiomeKinds itself: BiomeMoss is the
// highest kind constant, so the list must be exactly that long. Appending a
// kind without extending the list fails here rather than quietly shrinking
// every test's coverage.
func TestAllBiomeKindsIsComplete(t *testing.T) {
	if want := int(world.BiomeMoss) + 1; len(allBiomeKinds) != want {
		t.Fatalf("allBiomeKinds has %d entries, want %d — a new biome kind was added without extending it", len(allBiomeKinds), want)
	}
	seen := map[int32]bool{}
	for _, k := range allBiomeKinds {
		if seen[k] {
			t.Errorf("kind %d listed twice in allBiomeKinds", k)
		}
		seen[k] = true
	}
}

// TestIsScatterKind pins which kinds instance blades/rocks/clumps in grass.go
// versus which are ground-patch-only (Sand) or a companion primitive (Water).
// Moss is scatter-*capable* here but gated by its own toggle at draw time —
// see TestMossScatterGating.
func TestIsScatterKind(t *testing.T) {
	for _, k := range []int32{world.BiomeGrass, world.BiomeRock, world.BiomeWildflower, world.BiomeCattail, world.BiomeMoss} {
		if !world.IsScatterKind(k) {
			t.Errorf("kind %d should scatter", k)
		}
	}
	for _, k := range []int32{world.BiomeSand, world.BiomeWater} {
		if world.IsScatterKind(k) {
			t.Errorf("kind %d should not scatter", k)
		}
	}
}

// TestGroundTextureForEveryKind checks every kind resolves to a texture name
// that actually exists on disk (resources/textures), so a freshly-painted
// sector never renders black from an unregistered texture.
func TestGroundTextureForEveryKind(t *testing.T) {
	known := map[string]bool{"dirt": true, "rock": true, "sand": true, "moss": true}
	for _, k := range allBiomeKinds {
		tex := world.GroundTextureFor(k)
		if !known[tex] {
			t.Errorf("kind %d ground texture %q is not one of the known textures", k, tex)
		}
	}
}

// TestDefaultBiomeBrushSetsKind checks DefaultBiomeBrush always returns a
// brush tagged with the kind it was asked for, and never leaves a
// zero-valued Range on a scatter kind (which would fall back to 0 everywhere
// the renderer clamps it, rather than the intended default).
func TestDefaultBiomeBrushSetsKind(t *testing.T) {
	for _, k := range allBiomeKinds {
		b := world.DefaultBiomeBrush(k)
		if b.Kind != k {
			t.Fatalf("DefaultBiomeBrush(%d).Kind = %d", k, b.Kind)
		}
		if world.IsScatterKind(k) && b.Range <= 0 {
			t.Errorf("kind %d default brush has non-positive Range", k)
		}
	}
	water := world.DefaultBiomeBrush(world.BiomeWater)
	if !water.Water.Enabled {
		t.Fatal("default Water brush should carry Enabled water params")
	}

	wild := world.DefaultBiomeBrush(world.BiomeWildflower)
	if wild.HeadScale <= 0 {
		t.Fatal("default Wildflower brush should have a positive HeadScale (bloom size)")
	}
	cat := world.DefaultBiomeBrush(world.BiomeCattail)
	if cat.HeadScale <= 0 {
		t.Fatal("default Cattail brush should have a positive HeadScale (seed-head size)")
	}
}

// TestMaxStemHeightConstantIsPositiveAndFinite pins the cap value itself
// (renderBiomeSector clamps Grass/Wildflower's GrassHeight to this — see the
// constant's own doc comment — so a regression here would silently change
// the cap without any test noticing).
func TestMaxStemHeightConstantIsPositiveAndFinite(t *testing.T) {
	if world.MaxStemHeight <= 0 || world.MaxStemHeight != 5 {
		t.Fatalf("MaxStemHeight = %v, want 5", world.MaxStemHeight)
	}
}

// partBounds returns the local-space XZ half-extent and Y extent of the
// vertices in a template mesh whose aParam tag falls in [lo,hi) — see the
// aParam encoding in biome_vert.glsl (stem [0,1], wildflower bloom [2,3),
// cattail seed head [3,4), wildflower bloom center [4,5), wildflower basal
// leaves [5,6)).
func partBounds(t *testing.T, mesh []float32, lo, hi float32) (halfWidth, height float32) {
	t.Helper()
	minX, maxX := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	minY, maxY := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	found := false
	for i := 0; i < len(mesh); i += biomeVertexStride {
		if p := mesh[i+6]; p < lo || p >= hi {
			continue
		}
		found = true
		x, y := mesh[i], mesh[i+1]
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
	}
	if !found {
		t.Fatalf("mesh has no vertices tagged in [%g,%g)", lo, hi)
	}
	return (maxX - minX) / 2, maxY - minY
}

// TestCattailSeedHeadIsElongated is the regression test for the "corn dog"
// silhouette: the seed head must be markedly taller than it is wide in the
// template's own local coordinates.
//
// This used to hold only by accident — the head shared the stem's transform,
// which scales X/Z by bladeWidth and Y by bladeHeight, stretching even a
// squat blob into a spike. Adding the configurable HeadScale made the head
// scale uniformly, so the local radii are now the only thing keeping it
// elongated, and a flat blob renders as a disc instead.
func TestCattailSeedHeadIsElongated(t *testing.T) {
	halfWidth, height := partBounds(t, buildCattailMesh(), cattailHeadParam, cattailHeadParam+1)
	width := halfWidth * 2
	if height <= width*2 {
		t.Fatalf("cattail seed head is %.3f tall by %.3f wide — needs to be at least 2x "+
			"taller than wide for the corn dog silhouette", height, width)
	}
}

// TestWildflowerBloomAspects pins both bloom silhouettes. Unlike the cattail,
// the wildflower's proportions are NOT baked into its template — the mesh
// carries a unit blob and the shader's bloomAspect uniform shapes it, so one
// VAO serves both toggle states. These constants are therefore the only
// thing defining the two shapes.
func TestWildflowerBloomAspects(t *testing.T) {
	// Disc: squat, wider than tall — deliberately unlike the cattail spike.
	if wildflowerDiscAspect.Y() >= wildflowerDiscAspect.X() {
		t.Errorf("disc bloom aspect %v is not squat (Y should be well under X)", wildflowerDiscAspect)
	}
	// Spike: the cattail-like corn dog, markedly taller than wide.
	if wildflowerSpikeAspect.Y() <= wildflowerSpikeAspect.X()*2 {
		t.Errorf("spike bloom aspect %v is not elongated (Y should be >2x X)", wildflowerSpikeAspect)
	}
	// The toggle has to actually change the silhouette, not just nudge it.
	if wildflowerSpikeAspect.Y() <= wildflowerDiscAspect.Y() {
		t.Errorf("spike aspect %v is no taller than disc aspect %v — the toggle would be a no-op",
			wildflowerSpikeAspect, wildflowerDiscAspect)
	}
}

// TestWildflowerMeshHasBloomAndCenterParts checks the template carries both
// head parts, each tagged in its own aParam band, and that both are built as
// unit blobs — the shader assumes it can shape them purely from bloomAspect /
// its own center aspect, so a baked-in radius here would double-scale them.
func TestWildflowerMeshHasBloomAndCenterParts(t *testing.T) {
	mesh := buildWildflowerMesh()

	bloomHalfW, bloomH := partBounds(t, mesh, wildflowerBloomParam, wildflowerBloomParam+1)
	centerHalfW, centerH := partBounds(t, mesh, wildflowerCenterParam, wildflowerCenterParam+1)

	// Same generator, same unit radii — the two parts differ only in the
	// aspect the shader applies, so their raw extents must match.
	if math.Abs(float64(bloomHalfW-centerHalfW)) > 0.2 {
		t.Errorf("bloom (%.3f) and center (%.3f) half-widths differ — both should be unit blobs",
			bloomHalfW, centerHalfW)
	}
	if math.Abs(float64(bloomH-centerH)) > 0.3 {
		t.Errorf("bloom (%.3f) and center (%.3f) heights differ — both should be unit blobs",
			bloomH, centerH)
	}
	// A unit blob is roughly ±1 in XZ; a value near the old baked 0.24/0.85
	// radii would mean the aspect got baked back into the mesh.
	if bloomHalfW < 0.5 {
		t.Errorf("bloom half-width %.3f is too small to be a unit blob — aspect looks baked into the mesh", bloomHalfW)
	}
}

// TestGroundPatchGridDimsCapped checks the grid resolution is bounded by
// groundPatchMaxDim regardless of how large the sector's AABB is — the same
// bounded-cost guarantee the scatter passes give via their camera ring.
func TestGroundPatchGridDimsCapped(t *testing.T) {
	nx, nz := groundPatchGridDims(mgl32.Vec2{0, 0}, mgl32.Vec2{10000, 10000})
	if nx > groundPatchMaxDim || nz > groundPatchMaxDim {
		t.Fatalf("grid dims (%d,%d) exceed cap %d", nx, nz, groundPatchMaxDim)
	}
	if nx < 1 || nz < 1 {
		t.Fatalf("grid dims (%d,%d) must be at least 1", nx, nz)
	}

	// A tiny sector still gets at least one cell, not zero.
	nx2, nz2 := groundPatchGridDims(mgl32.Vec2{0, 0}, mgl32.Vec2{0.01, 0.01})
	if nx2 < 1 || nz2 < 1 {
		t.Fatalf("tiny sector grid dims (%d,%d) must be at least 1", nx2, nz2)
	}
}

// TestGroundDisplacementBounded checks every kind's displacement stays within
// the amplitude it advertises (groundFBM is normalized to [0,1], so
// (fbm*2-1)*amp must land in [-amp,amp] plus the sand ripple's small extra
// layer) — a runaway noise sum would make the ground patch spike through the
// scatter sitting on top of it.
func TestGroundDisplacementBounded(t *testing.T) {
	brush := &world.BiomeBrush{SandHeight: 0.5}
	seed := mgl32.Vec2{1.7, 4.2}
	for x := float32(0); x < 40; x += 3.3 {
		for z := float32(0); z < 40; z += 4.1 {
			d := groundDisplacement(world.BiomeSand, brush, seed, x, z)
			// Regression guard: dunes must be mounds rising from the floor,
			// never valleys dipping below it — a negative displacement here
			// used to sink the sand patch below the actual structural floor.
			if d < 0 {
				t.Fatalf("sand displacement %.3f at (%.1f,%.1f) is negative — sand must never dip below the floor", d, x, z)
			}
			if d > 0.7 { // amp 0.5 + up to 15% ripple headroom
				t.Fatalf("sand displacement %.3f at (%.1f,%.1f) exceeds expected bound", d, x, z)
			}
			d2 := groundDisplacement(world.BiomeGrass, brush, seed, x, z)
			if d2 < -0.1 || d2 > 0.1 {
				t.Fatalf("grass ground displacement %.3f at (%.1f,%.1f) exceeds expected bound", d2, x, z)
			}
		}
	}
}

// TestGroundDisplacementRespectsConfiguredAmplitude checks a brush's
// GroundAmplitude actually bounds the non-Sand displacement — the panel
// slider only has meaning if this actually reaches groundDisplacement.
func TestGroundDisplacementRespectsConfiguredAmplitude(t *testing.T) {
	seed := mgl32.Vec2{2.1, 0.6}
	small := &world.BiomeBrush{GroundAmplitude: 0.02, GroundFrequency: 0.3, GroundOctaves: 2}
	large := &world.BiomeBrush{GroundAmplitude: 0.9, GroundFrequency: 0.3, GroundOctaves: 2}

	maxAbs := func(brush *world.BiomeBrush, kind int32) float32 {
		m := float32(0)
		for x := float32(0); x < 30; x += 1.7 {
			for z := float32(0); z < 30; z += 2.3 {
				if d := groundDisplacement(kind, brush, seed, x, z); geom.Abs32(d) > m {
					m = geom.Abs32(d)
				}
			}
		}
		return m
	}

	for _, kind := range []int32{world.BiomeGrass, world.BiomeWater} {
		smallMax := maxAbs(small, kind)
		largeMax := maxAbs(large, kind)
		if smallMax > 0.02+1e-6 {
			t.Errorf("kind %d: amplitude 0.02 produced displacement up to %.4f, want <= 0.02", kind, smallMax)
		}
		if largeMax <= smallMax {
			t.Errorf("kind %d: amplitude 0.9 (max %.4f) should displace more than amplitude 0.02 (max %.4f)", kind, largeMax, smallMax)
		}
	}
}

// TestClampOctavesRange checks clampOctaves keeps groundFBM's octave count in
// its supported [1,6] range regardless of input.
func TestClampOctavesRange(t *testing.T) {
	cases := map[int32]int{-5: 1, 0: 1, 1: 1, 3: 3, 6: 6, 100: 6}
	for in, want := range cases {
		if got := clampOctaves(in); got != want {
			t.Errorf("clampOctaves(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestResolveGroundTexture checks an explicit GroundTexture overrides the
// kind's default, and an empty one falls back to GroundTextureFor.
func TestResolveGroundTexture(t *testing.T) {
	auto := &world.BiomeBrush{Kind: world.BiomeGrass}
	if got, want := resolveGroundTexture(auto), world.GroundTextureFor(world.BiomeGrass); got != want {
		t.Errorf("resolveGroundTexture with no override = %q, want kind default %q", got, want)
	}
	override := &world.BiomeBrush{Kind: world.BiomeGrass, GroundTexture: "stone_floor"}
	if got := resolveGroundTexture(override); got != "stone_floor" {
		t.Errorf("resolveGroundTexture with override = %q, want %q", got, "stone_floor")
	}
}

// TestResolveGroundUVScale covers the per-component fallback: an unset (or
// nonsense) component must land on DefaultGroundUVScale — the tiling every
// sector authored before GroundUVScale existed was fixed at — while a set one
// passes straight through.
func TestResolveGroundUVScale(t *testing.T) {
	def := mgl32.Vec2{world.DefaultGroundUVScale, world.DefaultGroundUVScale}
	if got := resolveGroundUVScale(&world.BiomeBrush{Kind: world.BiomeGrass}); got != def {
		t.Errorf("unset GroundUVScale = %v, want default %v", got, def)
	}
	set := &world.BiomeBrush{Kind: world.BiomeGrass, GroundUVScale: mgl32.Vec2{2, 0.5}}
	if got := resolveGroundUVScale(set); got != (mgl32.Vec2{2, 0.5}) {
		t.Errorf("set GroundUVScale = %v, want {2 0.5}", got)
	}
	half := &world.BiomeBrush{Kind: world.BiomeGrass, GroundUVScale: mgl32.Vec2{0, 4}}
	if got := resolveGroundUVScale(half); got != (mgl32.Vec2{world.DefaultGroundUVScale, 4}) {
		t.Errorf("half-set GroundUVScale = %v, want {%v 4}", got, world.DefaultGroundUVScale)
	}
}

// TestBuildGroundPatchMeshUsesGroundUVScale checks the brush's UV scale is the
// one actually baked into the patch's UVs (world XZ * scale, matching a
// structural floor's Surface.UVScale), which is what lets a patch be dialed to
// tile identically to the floor it's painted over.
func TestBuildGroundPatchMeshUsesGroundUVScale(t *testing.T) {
	lvl := &world.Level{} // no chunks: floorHeightUnderLevel falls back to 0 everywhere
	s := &world.BiomeSector{
		ID:     "gpuv",
		Points: []mgl32.Vec2{{0, 0}, {8, 0}, {8, 8}, {0, 8}},
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass, GroundUVScale: mgl32.Vec2{2, 0.5}},
	}
	verts := buildGroundPatchMesh(lvl, s)
	if len(verts) == 0 {
		t.Fatal("expected a non-empty ground patch mesh")
	}
	for i := 0; i < len(verts); i += groundVertexStride {
		x, z := verts[i], verts[i+2]
		u, v := verts[i+6], verts[i+7]
		if math.Abs(float64(u-x*2)) > 1e-4 || math.Abs(float64(v-z*0.5)) > 1e-4 {
			t.Fatalf("vertex (%.3f,_,%.3f) UV = (%.3f,%.3f), want (%.3f,%.3f)", x, z, u, v, x*2, z*0.5)
		}
	}

	// Unset scale falls back to the 1x1 default tiling.
	legacy := &world.BiomeSector{ID: "gpuv2", Points: s.Points, Brush: world.BiomeBrush{Kind: world.BiomeGrass}}
	lverts := buildGroundPatchMesh(lvl, legacy)
	if len(lverts) == 0 {
		t.Fatal("expected a non-empty ground patch mesh for the unset-scale sector")
	}
	x, u := lverts[0], lverts[6]
	if math.Abs(float64(u-x*world.DefaultGroundUVScale)) > 1e-4 {
		t.Fatalf("unset-scale U at x=%.3f is %.3f, want %.3f", x, u, x*world.DefaultGroundUVScale)
	}
}

// TestBiomeSeedChangesWithGroundFields is the regression test for the bug
// where adding BiomeBrush.GroundTexture (a string) broke BiomeSeed: passing
// a struct containing a string to binary.Write silently writes nothing,
// which made every field decorrelation this function exists for stop
// working — two sectors with different ground settings (or anything else)
// would hash identically as long as their ID matched.
func TestBiomeSeedChangesWithGroundFields(t *testing.T) {
	base := world.BiomeSector{ID: "s0", Brush: world.BiomeBrush{Kind: world.BiomeGrass, GroundFrequency: 0.2}}

	tex := base
	tex.Brush.GroundTexture = "rock"
	if world.BiomeSeed(&base) == world.BiomeSeed(&tex) {
		t.Fatal("different GroundTexture should produce a different seed")
	}

	amp := base
	amp.Brush.GroundAmplitude = 0.5
	if world.BiomeSeed(&base) == world.BiomeSeed(&amp) {
		t.Fatal("different GroundAmplitude should produce a different seed")
	}
}

// TestBuildGroundPatchMeshStaysInsidePolygon checks every emitted triangle's
// vertices fall within the sector polygon's own AABB (the per-quad
// all-corners-inside clip must not leak geometry past the painted shape) and
// that the vertex buffer is well-formed (a whole number of triangles, stride
// groundVertexStride).
func TestBuildGroundPatchMeshStaysInsidePolygon(t *testing.T) {
	s := &world.BiomeSector{
		ID:     "gp0",
		Points: []mgl32.Vec2{{0, 0}, {8, 0}, {8, 8}, {0, 8}},
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
	}
	lvl := &world.Level{} // no chunks: floorHeightUnderLevel falls back to 0 everywhere

	verts := buildGroundPatchMesh(lvl, s)
	if len(verts) == 0 {
		t.Fatal("expected a non-empty ground patch mesh for an 8x8 square sector")
	}
	if len(verts)%groundVertexStride != 0 {
		t.Fatalf("vertex buffer length %d is not a multiple of stride %d", len(verts), groundVertexStride)
	}
	nVerts := len(verts) / groundVertexStride
	if nVerts%3 != 0 {
		t.Fatalf("%d vertices is not a whole number of triangles", nVerts)
	}
	for i := 0; i < len(verts); i += groundVertexStride {
		x, z := verts[i], verts[i+2]
		if x < -0.01 || x > 8.01 || z < -0.01 || z > 8.01 {
			t.Fatalf("vertex (%.3f,_,%.3f) falls outside the sector's AABB", x, z)
		}
	}
}

// TestBuildGroundPatchMeshFollowsDiagonalEdge is the regression test for the
// reported bug: a diagonal sector edge rendered as a staircase of 90-degree
// steps instead of a straight line, because the old grid-quad clip only ever
// tested whole grid cells in/out. With a right-triangle sector whose
// hypotenuse is the line x+z=10, every mesh vertex must stay on the polygon
// side of that line (no staircase overshoot past it), and the mesh must
// still reach close to the hypotenuse rather than just shrinking away from
// it (which would "pass" a boundary check for the wrong reason).
func TestBuildGroundPatchMeshFollowsDiagonalEdge(t *testing.T) {
	s := &world.BiomeSector{
		ID:     "diag0",
		Points: []mgl32.Vec2{{0, 0}, {10, 0}, {0, 10}},
		Brush:  world.BiomeBrush{Kind: world.BiomeGrass},
	}
	lvl := &world.Level{}

	verts := buildGroundPatchMesh(lvl, s)
	if len(verts) == 0 {
		t.Fatal("expected a non-empty mesh for the right-triangle sector")
	}

	maxSum := float32(0)
	for i := 0; i < len(verts); i += groundVertexStride {
		x, z := verts[i], verts[i+2]
		if x < -0.05 || z < -0.05 {
			t.Fatalf("vertex (%.3f,_,%.3f) falls outside the triangle's right-angle legs", x, z)
		}
		if sum := x + z; sum > 10.3 { // small numerical slack
			t.Fatalf("vertex (%.3f,_,%.3f) has x+z=%.3f, well past the hypotenuse x+z=10 — "+
				"boundary is overshooting past the diagonal edge instead of following it", x, z, sum)
		} else if sum > maxSum {
			maxSum = sum
		}
	}
	if maxSum < 8 {
		t.Fatalf("mesh doesn't reach near the hypotenuse: max (x+z) = %.3f, want close to 10", maxSum)
	}
}

// TestDensifyPolygonRespectsMaxSegAndShape checks densifyPolygon inserts
// intermediate points so no edge segment exceeds maxSeg, without moving any
// original vertex or changing the polygon's shape (every inserted point is
// exactly on the line between the two vertices it was inserted between).
func TestDensifyPolygonRespectsMaxSegAndShape(t *testing.T) {
	pts := []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	out := densifyPolygon(pts, 3)

	if len(out) <= len(pts) {
		t.Fatalf("expected extra points for maxSeg=3 on 10-unit edges, got %d (started with %d)", len(out), len(pts))
	}
	for i := 0; i < len(out); i++ {
		a := out[i]
		b := out[(i+1)%len(out)]
		if d := a.Sub(b).Len(); d > 3.0+1e-3 {
			t.Fatalf("segment %v->%v has length %.3f, exceeds maxSeg 3", a, b, d)
		}
	}
	for _, p := range pts {
		found := false
		for _, o := range out {
			if o.Sub(p).Len() < 1e-6 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("original vertex %v missing from densified polygon", p)
		}
	}
}

// TestCcwPolygonFlipsClockwiseInput checks ccwPolygon normalizes a
// clockwise-wound polygon and leaves an already-CCW one untouched.
func TestCcwPolygonFlipsClockwiseInput(t *testing.T) {
	ccw := []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}}
	if world.SignedArea2D(ccw) <= 0 {
		t.Fatal("test polygon should already be CCW")
	}
	if got := ccwPolygon(ccw); world.SignedArea2D(got) <= 0 {
		t.Fatal("ccwPolygon flipped an already-CCW polygon")
	}

	cw := []mgl32.Vec2{{0, 0}, {0, 4}, {4, 4}, {4, 0}}
	if world.SignedArea2D(cw) >= 0 {
		t.Fatal("test polygon should be CW")
	}
	if got := ccwPolygon(cw); world.SignedArea2D(got) <= 0 {
		t.Fatal("ccwPolygon did not fix a CW polygon")
	}
}

// TestBuildBiomeWaterMeshIsFlat checks the water primitive mesh is flat at
// local y=0 (wave displacement happens in the shared water shader, not here —
// see TestWaterPlaneMeshIsTessellated for the hand-placed equivalent) and
// that its footprint is centered on the given centroid.
func TestBuildBiomeWaterMeshIsFlat(t *testing.T) {
	s := &world.BiomeSector{
		ID:     "w0",
		Points: []mgl32.Vec2{{10, 10}, {20, 10}, {20, 20}, {10, 20}},
	}
	centroid := mgl32.Vec2{15, 15}

	verts, indices := buildBiomeWaterMesh(s, centroid)
	if len(verts) == 0 || len(indices) == 0 {
		t.Fatal("expected a non-empty water mesh for a 10x10 square sector")
	}
	if len(indices)%3 != 0 {
		t.Fatalf("index count %d is not a multiple of 3", len(indices))
	}
	for i, v := range verts {
		if len(v) != 5 {
			t.Fatalf("vertex %d has %d components, want 5 [X,Y,Z,U,V]", i, len(v))
		}
		if v[1] != 0 {
			t.Errorf("vertex %d Y = %g, want 0 (wave displacement happens in the shader)", i, v[1])
		}
		// Local X/Z are world position minus centroid; the sector spans
		// world [10,20], so locals must fall within roughly [-5,5].
		if v[0] < -5.5 || v[0] > 5.5 || v[2] < -5.5 || v[2] > 5.5 {
			t.Errorf("vertex %d local XZ (%g,%g) not centered on the sector", i, v[0], v[2])
		}
	}
	for _, idx := range indices {
		if idx < 0 || idx >= len(verts) {
			t.Fatalf("index %d out of range (len=%d)", idx, len(verts))
		}
	}
}

// TestBiomeWaterCentroidAndFloor checks the reference point/height used to
// place the companion water primitive follows the structural floor at the
// polygon's center, matching floorHeightUnderLevel's own contract.
func TestBiomeWaterCentroidAndFloor(t *testing.T) {
	ch := world.Chunk{
		Points: []mgl32.Vec2{{-4, -4}, {4, -4}, {4, 4}, {-4, 4}},
		Sectors: []world.Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        world.Floor{Enabled: true, Height: 2},
		}},
	}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	s := &world.BiomeSector{Points: []mgl32.Vec2{{-2, -2}, {2, -2}, {2, 2}, {-2, 2}}}

	centroid, floorY := biomeWaterCentroidAndFloor(lvl, s)
	if centroid != (mgl32.Vec2{0, 0}) {
		t.Fatalf("centroid = %v, want (0,0)", centroid)
	}
	if floorY != 2 {
		t.Fatalf("floorY = %v, want 2", floorY)
	}
}

// TestRawBiomeBrushYAMLRoundTrip pins the new persisted fields (RockScale —
// previously never saved at all — SandHeight, WaterDepth, Water) surviving a
// YAML marshal/unmarshal cycle, the same contract editor_save.go/
// level_schema.go rely on for the level file.
func TestRawBiomeBrushYAMLRoundTrip(t *testing.T) {
	rb := world.RawBiomeSector{
		ID:     "b0",
		Points: []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}},
		Brush: world.RawBiomeBrush{
			Kind:       world.BiomeWater,
			Density:    0.5,
			RockScale:  0.42,
			SandHeight: 0.77,
			WaterDepth: 1.25,
			Water: &world.RawWater{
				WaveAmplitude: 0.2,
				WaveLength:    5,
				WaveSpeed:     0.8,
				Reflectivity:  0.6,
				ReflectColor:  world.RawColor3{R: 0.1, G: 0.2, B: 0.3},
			},
		},
	}

	out, err := yaml.Marshal(rb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got world.RawBiomeSector
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Brush.RockScale != rb.Brush.RockScale {
		t.Errorf("RockScale round trip: got %v, want %v", got.Brush.RockScale, rb.Brush.RockScale)
	}
	if got.Brush.SandHeight != rb.Brush.SandHeight {
		t.Errorf("SandHeight round trip: got %v, want %v", got.Brush.SandHeight, rb.Brush.SandHeight)
	}
	if got.Brush.WaterDepth != rb.Brush.WaterDepth {
		t.Errorf("WaterDepth round trip: got %v, want %v", got.Brush.WaterDepth, rb.Brush.WaterDepth)
	}
	if got.Brush.Water == nil || *got.Brush.Water != *rb.Brush.Water {
		t.Errorf("Water round trip: got %+v, want %+v", got.Brush.Water, rb.Brush.Water)
	}
}

// TestWildflowerBloomOptionsRoundTrip covers the bloom toggles and center
// color surviving a save/load cycle, and — because every one of them is a
// plain omitempty field whose zero value is the pre-existing look — that a
// default brush writes none of them, so old levels reload unchanged.
func TestWildflowerBloomOptionsRoundTrip(t *testing.T) {
	set := world.RawBiomeSector{
		ID:     "w0",
		Points: []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}},
		Brush: world.RawBiomeBrush{
			Kind:            world.BiomeWildflower,
			BloomSpike:      true,
			BloomCenter:     true,
			BloomSolidColor: true,
			CenterColor:     world.RawColor{R: 0.9, G: 0.8, B: 0.2},
		},
	}

	out, err := yaml.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got world.RawBiomeSector
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Brush.BloomSpike || !got.Brush.BloomCenter || !got.Brush.BloomSolidColor {
		t.Errorf("bloom toggles did not survive the round trip: %+v", got.Brush)
	}
	if got.Brush.CenterColor != set.Brush.CenterColor {
		t.Errorf("CenterColor round trip: got %+v, want %+v", got.Brush.CenterColor, set.Brush.CenterColor)
	}

	// All-default bloom options must not be written at all.
	plain, err := yaml.Marshal(world.RawBiomeSector{ID: "w1", Brush: world.RawBiomeBrush{Kind: world.BiomeWildflower}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"bloom_spike", "bloom_center", "bloom_solid_color"} {
		if strings.Contains(string(plain), key) {
			t.Errorf("default brush wrote %q; zero values should be omitted so old levels stay unchanged:\n%s", key, plain)
		}
	}
}

// TestClampGrassClumpRange pins the 0..1 range the shader's tuft math is
// parameterized over, including that 0 stays 0 (it means "no clumping", not
// "use the kind's default" the way most brush fields' zero does) and that a
// junk value out of a hand-edited save can't reach the uniform.
func TestClampGrassClumpRange(t *testing.T) {
	cases := []struct{ in, want float32 }{
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{-0.25, 0}, // clamped, not treated as "default"
		{4, 1},     // clamped
		{float32(math.NaN()), 0},
	}
	for _, c := range cases {
		if got := world.ClampGrassClump(c.in); got != c.want {
			t.Errorf("ClampGrassClump(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestGrassClumpRoundTrip covers the clumping amount surviving a save/load
// cycle, and — because its zero value is the pre-existing even scatter — that
// an unclumped brush writes no key at all, so levels authored before the field
// existed reload byte-identically.
func TestGrassClumpRoundTrip(t *testing.T) {
	set := world.RawBiomeSector{
		ID:     "g0",
		Points: []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}},
		Brush:  world.RawBiomeBrush{Kind: world.BiomeGrass, GrassClump: 0.65},
	}
	out, err := yaml.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got world.RawBiomeSector
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Brush.GrassClump != set.Brush.GrassClump {
		t.Errorf("GrassClump round trip: got %v, want %v", got.Brush.GrassClump, set.Brush.GrassClump)
	}

	plain, err := yaml.Marshal(world.RawBiomeSector{ID: "g1", Brush: world.RawBiomeBrush{Kind: world.BiomeGrass}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), "grass_clump") {
		t.Errorf("unclumped brush wrote grass_clump; the zero value must be omitted:\n%s", plain)
	}
}

// TestFadeRoundTrip pins the soft-edge fields (BiomeBrush.RangeFadeFraction/
// EdgeFadeDistance) surviving a save/load cycle, and — since both are plain
// omitempty fields whose zero value is the pre-existing hard-edged look —
// that a sector saved before these existed loads back completely unaffected.
func TestFadeRoundTrip(t *testing.T) {
	set := world.RawBiomeSector{
		ID:     "f0",
		Points: []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}},
		Brush: world.RawBiomeBrush{
			Kind:              world.BiomeGrass,
			RangeFadeFraction: 0.5,
			EdgeFadeDistance:  2.5,
			// BoundaryJitter's zero value means something different from its
			// siblings above (see BiomeBrush's doc comment: "use the kind
			// default", not "off") but the wire format doesn't care — it's
			// still a plain omitempty float, so it belongs in the same
			// round-trip check.
			BoundaryJitter: 6.5,
		},
	}
	out, err := yaml.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got world.RawBiomeSector
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Brush != set.Brush {
		t.Errorf("brush round trip: got %+v, want %+v", got.Brush, set.Brush)
	}

	plain, err := yaml.Marshal(world.RawBiomeSector{ID: "f1", Brush: world.RawBiomeBrush{Kind: world.BiomeGrass}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		"range_fade_fraction", "edge_fade_distance", "boundary_jitter",
	} {
		if strings.Contains(string(plain), key) {
			t.Errorf("zero-valued brush wrote %q; the zero value must be omitted so old saves load unchanged:\n%s", key, plain)
		}
	}
}

// TestDefaultGrassBrushIsUnclumped pins the promise that clumping is opt-in:
// a freshly painted grass sector scatters exactly the way it did before the
// feature existed, so nothing about an existing level changes on upgrade.
func TestDefaultGrassBrushIsUnclumped(t *testing.T) {
	for _, kind := range allBiomeKinds {
		if c := world.DefaultBiomeBrush(kind).GrassClump; c != 0 {
			t.Errorf("DefaultBiomeBrush(%d).GrassClump = %v, want 0", kind, c)
		}
	}
}

// TestBiomeSeedIgnoresGrassClump pins the one deliberate exception to
// BiomeSeed hashing every brush field. Folding GrassClump in would reshuffle
// the scatter of every already-authored sector the moment the field existed
// (breaking "clumping 0 looks exactly like before") and would teleport the
// whole lattice while the slider is dragged, hiding the effect being tuned.
func TestBiomeSeedIgnoresGrassClump(t *testing.T) {
	base := world.BiomeSector{ID: "s0", Brush: world.BiomeBrush{Kind: world.BiomeGrass, Density: 0.8, GrassHeight: 0.5}}
	clumped := base
	clumped.Brush.GrassClump = 1

	if world.BiomeSeed(&base) != world.BiomeSeed(&clumped) {
		t.Fatal("GrassClump changed the scatter seed; it is deliberately excluded from BiomeSeed")
	}
}

// TestFlowerAndReedStemsAreConstantWidth is the regression test for stalks vs
// blades: a wildflower or cattail stem must hold one thickness the whole way
// up. Both used to be built with buildTaperedStem, which sharpens to a point
// and so reads as a blade of grass — right for grass, wrong for a stalk with
// a bloom or a seed head on top.
func TestFlowerAndReedStemsAreConstantWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		mesh []float32
	}{
		{"wildflower", buildWildflowerMesh()},
		{"cattail", buildCattailMesh()},
	} {
		// Widest and narrowest |x| among the stem's vertices (aParam in
		// [0,1]); a taper would drive the narrowest to 0 at the tip.
		widest, narrowest := float32(0), float32(math.MaxFloat32)
		for i := 0; i < len(tc.mesh); i += biomeVertexStride {
			if p := tc.mesh[i+6]; p < 0 || p > 1 {
				continue
			}
			w := float32(math.Abs(float64(tc.mesh[i])))
			widest, narrowest = max(widest, w), min(narrowest, w)
		}
		if widest <= 0 {
			t.Fatalf("%s: stem has no width at all", tc.name)
		}
		if narrowest != widest {
			t.Errorf("%s stem tapers: half-width runs %.4f..%.4f, want constant",
				tc.name, narrowest, widest)
		}
	}
}

// TestGrassBladeStillTapers is the other half of the pair above: grass keeps
// the tapered silhouette, so "stems are constant width" must not have been
// applied across the board.
func TestGrassBladeStillTapers(t *testing.T) {
	mesh := buildBladeMesh()
	tipWidth := float32(math.MaxFloat32)
	for i := 0; i < len(mesh); i += biomeVertexStride {
		if mesh[i+6] < 1 { // aParam == y; only the topmost vertices
			continue
		}
		tipWidth = min(tipWidth, float32(math.Abs(float64(mesh[i]))))
	}
	if tipWidth != 0 {
		t.Errorf("grass blade tip half-width is %.4f, want 0 — the blade must still taper to a point", tipWidth)
	}
}

// TestWildflowerLeavesArcOutward pins the leaf shape the toggle produces: a
// triangle (full width at the stem, a point at the tip) swept along an arc
// that reaches outward and then bends back over, rather than a flat spike.
func TestWildflowerLeavesArcOutward(t *testing.T) {
	leaves := buildWildflowerLeaves()
	if len(leaves) == 0 || len(leaves)%biomeVertexStride != 0 {
		t.Fatalf("leaf mesh is %d floats, want a non-zero multiple of %d", len(leaves), biomeVertexStride)
	}

	var maxRadius, maxY, tipY float32
	for i := 0; i < len(leaves); i += biomeVertexStride {
		x, y, z, p := leaves[i], leaves[i+1], leaves[i+2], leaves[i+6]
		if p < wildflowerLeafParam || p >= wildflowerLeafParam+1 {
			t.Fatalf("leaf vertex tagged %v, outside the [%v,%v) band", p, wildflowerLeafParam, wildflowerLeafParam+1)
		}
		// aParam's fraction must be the vertex's own height, which is what
		// the shader bends the leaf by in the wind.
		if got := p - wildflowerLeafParam; math.Abs(float64(got-y)) > 1e-5 {
			t.Fatalf("leaf aParam fraction %v does not match vertex height %v", got, y)
		}
		r := float32(math.Hypot(float64(x), float64(z)))
		maxRadius, maxY = max(maxRadius, r), max(maxY, y)
		if r > maxRadius-1e-6 {
			tipY = y
		}
	}

	if maxRadius <= 0 {
		t.Fatal("leaves reach no distance from the stem")
	}
	// They have to read as leaves, not as a collar: a meaningful reach out.
	if maxRadius < wildflowerLeafLen*0.9 {
		t.Errorf("leaves reach only %.3f, want about %.3f", maxRadius, wildflowerLeafLen)
	}
	// The arc must peak BEFORE the tip — that bend-back is the "curving out"
	// shape; a monotonically rising leaf is just a spike pointing up and out.
	if tipY >= maxY {
		t.Errorf("leaf tip (%.3f) is its highest point (%.3f) — the arc never bends back over", tipY, maxY)
	}
	// Leaves belong at the base of the plant, not up among the petals.
	if maxY > 0.5 {
		t.Errorf("leaves rise to %.3f of the stem height, too high to read as basal", maxY)
	}
}

// TestWildflowerMeshCarriesLeaves checks the leaves ride in the one shared
// template (they are switched off in the shader, not by swapping VAOs) and
// that they didn't disturb the bands the other parts are tagged in.
func TestWildflowerMeshCarriesLeaves(t *testing.T) {
	mesh := buildWildflowerMesh()
	counts := map[int]int{}
	for i := 0; i < len(mesh); i += biomeVertexStride {
		counts[int(mesh[i+6])]++
	}
	for _, band := range []struct {
		param int
		name  string
	}{
		{0, "stem"},
		{int(wildflowerBloomParam), "bloom"},
		{int(wildflowerCenterParam), "bloom center"},
		{int(wildflowerLeafParam), "leaves"},
	} {
		if counts[band.param] == 0 {
			t.Errorf("wildflower template carries no %s vertices (aParam band %d)", band.name, band.param)
		}
	}
	if counts[int(cattailHeadParam)] != 0 {
		t.Errorf("wildflower template has vertices in the cattail seed-head band")
	}
}

// TestStemLeavesRoundTrip covers the leaves toggle surviving a save/load
// cycle, and that a bare-stemmed flower writes no key — its zero value is the
// pre-existing look, so levels authored before leaves existed reload unchanged.
func TestStemLeavesRoundTrip(t *testing.T) {
	set := world.RawBiomeSector{
		ID:     "f0",
		Points: []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}},
		Brush:  world.RawBiomeBrush{Kind: world.BiomeWildflower, StemLeaves: true},
	}
	out, err := yaml.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got world.RawBiomeSector
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Brush.StemLeaves {
		t.Error("StemLeaves did not survive the round trip")
	}

	plain, err := yaml.Marshal(world.RawBiomeSector{ID: "f1", Brush: world.RawBiomeBrush{Kind: world.BiomeWildflower}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), "stem_leaves") {
		t.Errorf("bare-stemmed brush wrote stem_leaves; the zero value must be omitted:\n%s", plain)
	}
	if world.DefaultBiomeBrush(world.BiomeWildflower).StemLeaves {
		t.Error("DefaultBiomeBrush turns leaves on; they are meant to be opt-in")
	}
	if world.BiomeSeed(&world.BiomeSector{ID: "x"}) != world.BiomeSeed(&world.BiomeSector{ID: "x", Brush: world.BiomeBrush{StemLeaves: true}}) {
		t.Error("StemLeaves changed the scatter seed; it is deliberately excluded from BiomeSeed")
	}
}

// TestStemsReachTheirHeadAnchor is the regression test for a floating corn
// dog: the stalk has to run all the way up to the point its head hangs from.
//
// The two used to be tuned independently (a 0.88-tall reed under a 0.95
// anchor), leaving a gap the head's own radius was expected to cover. That
// only worked at the default head size — the gap scales with the stem's world
// height while the head is sized absolutely, so a tall reed or a small seed
// head pulled them apart with visible daylight between.
func TestStemsReachTheirHeadAnchor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mesh   []float32
		anchor float32
	}{
		{"wildflower", buildWildflowerMesh(), wildflowerHeadAnchor},
		{"cattail", buildCattailMesh(), cattailHeadAnchor},
	} {
		top := float32(0)
		for i := 0; i < len(tc.mesh); i += biomeVertexStride {
			if p := tc.mesh[i+6]; p < 0 || p > 1 { // stem band only
				continue
			}
			top = max(top, tc.mesh[i+1])
		}
		if math.Abs(float64(top-tc.anchor)) > 1e-5 {
			t.Errorf("%s stem tops out at %.4f but its head anchors at %.4f — "+
				"the head would float clear of the stem at some head sizes",
				tc.name, top, tc.anchor)
		}
	}
}

// TestResolvedLeafScale pins the "0 means the kind's default" convention and
// the upper clamp for the wildflower leaf-size knob.
func TestResolvedLeafScale(t *testing.T) {
	cases := []struct{ in, want float32 }{
		{0, world.DefaultWildflowerLeafScale},                   // unset / pre-field save
		{-1, world.DefaultWildflowerLeafScale},                  // junk
		{float32(math.NaN()), world.DefaultWildflowerLeafScale}, // junk
		{0.5, 0.5}, // honored
		{world.MaxWildflowerLeafScale + 10, world.MaxWildflowerLeafScale}, // clamped
	}
	for _, c := range cases {
		if got := world.ResolvedLeafScale(c.in); got != c.want {
			t.Errorf("ResolvedLeafScale(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if world.DefaultBiomeBrush(world.BiomeWildflower).LeafScale != world.DefaultWildflowerLeafScale {
		t.Error("a fresh wildflower brush should start at the default leaf scale")
	}
}

// TestLeafScaleRoundTrip covers the leaf-size knob surviving a save/load
// cycle, and that it is omitted when unset so a level authored before the
// field existed reloads at the default rather than at a leafless 0.
func TestLeafScaleRoundTrip(t *testing.T) {
	set := world.RawBiomeSector{
		ID:     "l0",
		Points: []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}},
		Brush:  world.RawBiomeBrush{Kind: world.BiomeWildflower, StemLeaves: true, LeafScale: 2.25},
	}
	out, err := yaml.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got world.RawBiomeSector
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Brush.LeafScale != set.Brush.LeafScale {
		t.Errorf("LeafScale round trip: got %v, want %v", got.Brush.LeafScale, set.Brush.LeafScale)
	}

	plain, err := yaml.Marshal(world.RawBiomeSector{ID: "l1", Brush: world.RawBiomeBrush{Kind: world.BiomeWildflower}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), "leaf_scale") {
		t.Errorf("unset LeafScale was written; it must be omitted so 0 resolves to the default:\n%s", plain)
	}
	// An unset value must land on the default, NOT on zero-sized leaves.
	if world.ResolvedLeafScale(got.Brush.LeafScale) <= 0 {
		t.Error("an unset LeafScale resolved to zero-sized leaves")
	}
}

// TestClumpTuftsAreNotGridAligned is the regression test for tufts reading as
// the lattice they are built on. Three separate mechanisms in the shader keep
// them scattered, and each is easy to weaken back to a visible grid, so pin
// all three:
//
//   - the root jitter has to span most of a tuft cell, not a fraction of it;
//   - tufts have to vary in size;
//   - tufts have to be individually rotated — without that every tuft fans its
//     blades along the same compass directions the slot grid hands them, and
//     the repeat is obvious however far the roots are jittered.
//
// These live in GLSL, so this checks the constants the shader is parameterized
// over rather than running it.
func TestClumpTuftsAreNotGridAligned(t *testing.T) {
	// The clump constants live in the shared placement snippet, not the color
	// vertex stage — all three scatter stages (color, cascade depth, cubemap
	// depth) compile the same copy so a tuft is shaped identically in each.
	src, err := os.ReadFile(filepath.Join("resources", "shaders", "biome_scatter_common.glsl"))
	if err != nil {
		t.Fatalf("read shader: %v", err)
	}
	shader := string(src)

	jitter := glslFloatConst(t, shader, "CLUMP_ROOT_JITTER")
	if jitter < 0.7 {
		t.Errorf("CLUMP_ROOT_JITTER is %.2f — roots wander over too little of their cell "+
			"and the tufts will read as a grid", jitter)
	}
	lo := glslFloatConst(t, shader, "CLUMP_SIZE_MIN")
	hi := glslFloatConst(t, shader, "CLUMP_SIZE_MAX")
	if hi-lo < 0.3 {
		t.Errorf("per-tuft size range %.2f..%.2f is too narrow to break up the repeat", lo, hi)
	}
	// The rotation itself: a per-tuft angle, applied to BOTH the blade offsets
	// and the splay direction. Missing either one leaves the grid showing.
	for _, want := range []string{"tuftRot", "tuftM * (cellPos - rootPos)", "tuftM * fromMid"} {
		if !strings.Contains(shader, want) {
			t.Errorf("shader no longer contains %q — per-tuft rotation is what stops every "+
				"tuft being the same stamp on a grid", want)
		}
	}
}

// glslFloatConst pulls the value of a `const float NAME = <v>;` out of a
// shader source.
func glslFloatConst(t *testing.T, shader, name string) float64 {
	t.Helper()
	re := regexp.MustCompile(`const\s+float\s+` + name + `\s*=\s*([0-9.]+)`)
	m := re.FindStringSubmatch(shader)
	if m == nil {
		t.Fatalf("shader has no `const float %s`", name)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parsing %s = %q: %v", name, m[1], err)
	}
	return v
}
