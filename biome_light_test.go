package main

import (
	"foundry/internal/world"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func readBiomeShader(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("resources", "shaders", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(src)
}

// TestScatterStagesShareOnePlacement pins the invariant the whole casting
// feature rests on: the color pass and the two depth passes place instances
// with the SAME code. A second copy of the lattice/jitter/clump math in a depth
// stage would drift the moment either side was tuned, and every blade's shadow
// would slide off the blade — the kind of bug that looks like a shadow bias
// problem and isn't.
func TestScatterStagesShareOnePlacement(t *testing.T) {
	common := readBiomeShader(t, "biome_scatter_common.glsl")
	if !strings.Contains(common, "bool biomePlace(") {
		t.Fatal("biome_scatter_common.glsl no longer defines biomePlace")
	}

	for _, stage := range []string{"biome_vert.glsl", "biome_shadow_vert.glsl", "biome_cube_vert.glsl"} {
		src := readBiomeShader(t, stage)
		if !strings.Contains(src, "biomePlace(gl_InstanceID") {
			t.Errorf("%s does not call biomePlace — it must not place instances itself", stage)
		}
		// The lattice origin is the signature line of the placement math. If it
		// reappears in a stage, that stage has grown its own copy.
		if strings.Contains(src, "baseIdx") {
			t.Errorf("%s computes its own lattice (baseIdx) instead of using biomePlace", stage)
		}
	}
}

// TestCastRangeDoesNotMovePlacement guards the reason castRange is a separate
// uniform from range. range feeds the lattice origin and the distance fade, so
// shortening IT for the shadow passes would relocate every instance and the
// shadows would no longer match the visible scatter. castRange may only reject.
func TestCastRangeDoesNotMovePlacement(t *testing.T) {
	common := readBiomeShader(t, "biome_scatter_common.glsl")

	for _, placement := range []string{
		"vec2 baseIdx = floor((camXZ - vec2(range)) / spacing);", // lattice origin
		"smoothstep(range * (1.0 - rangeFrac), range, distJ)",    // ring fade, sized by rangeFadeFraction
	} {
		if !strings.Contains(common, placement) {
			t.Errorf("placement expression %q changed — check it still uses range, not castRange", placement)
		}
	}

	// castRange must appear only as a survival test. Comments mention it freely,
	// so strip those before looking at what the code actually does. distJ (not
	// dist) is expected here — see TestBoundaryJitterAppliesToAllCircularTests,
	// which pins the jitter offset itself being applied to dist exactly once,
	// upstream of every comparison including this one.
	for _, raw := range strings.Split(common, "\n") {
		line := raw
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		if !strings.Contains(line, "castRange") {
			continue
		}
		if strings.Contains(line, "uniform float castRange") || strings.Contains(line, "distJ <= castRange") {
			continue
		}
		t.Errorf("castRange used outside its rejection test: %q", strings.TrimSpace(raw))
	}
}

// TestBoundaryJitterAppliesToAllCircularTests pins BoundaryJitter's whole
// point: it has to perturb EVERY circular boundary an instance is tested
// against — the alive test's lodStart/range/castRange cutoffs AND both fade
// smoothsteps — via the SAME distJ, computed once. If any one of them kept
// using the raw dist, that boundary's population (or its visual shrink) would
// stay a perfect circle while the others bulge, which reads as a worse bug
// than no jitter at all (the mesh/density and the visible fade disagreeing).
func TestBoundaryJitterAppliesToAllCircularTests(t *testing.T) {
	common := readBiomeShader(t, "biome_scatter_common.glsl")

	if !strings.Contains(common, "float distJ = dist;") {
		t.Fatal("distJ is no longer derived from dist — check the jitter is still applied at a single point")
	}

	for _, want := range []string{
		"(distJ >= lodStart)",
		"(distJ <= range)",
		"(distJ <= castRange)",
		"smoothstep(range * (1.0 - rangeFrac), range, distJ)",
		"smoothstep(lodStart, lodMin, distJ)",
	} {
		if !strings.Contains(common, want) {
			t.Errorf("circular-boundary test %q no longer uses distJ — it would stay a perfect circle while the others jitter", want)
		}
	}
}

// TestLODCrossfadeDithersInsteadOfShrinking is the regression test for the
// visible gap at an LOD boundary: shrinking BOTH tiers' blade size across the
// same crossfade band sums to "one tier's worth" of height (fade0+fade1==1 by
// construction) split across two tiers of DIFFERENT base density, which reads
// as a bald band right at the seam. The fix keeps every surviving instance at
// full size and dithers WHICH instances survive instead — folded into the
// alive test via the shared `pick`, not into the late per-vertex `off` scale.
func TestLODCrossfadeDithersInsteadOfShrinking(t *testing.T) {
	common := readBiomeShader(t, "biome_scatter_common.glsl")

	if !strings.Contains(common, "alive = pick <= density * hc.y * lodGrowIn * lodShrinkOut;") {
		t.Error("LOD crossfade is no longer folded into the alive test as a dither — " +
			"if it moved back into a late size scale, the two-tiers-both-shrinking gap is back")
	}
	// The late per-vertex fade must NOT multiply in an LOD term any more — it
	// should only ever be the true-outer-edge shrink and the polygon edge
	// fade, both of which have no second tier picking up behind them.
	if !strings.Contains(common, "float fade = rangeFade * edgeFade;") {
		t.Error("the late `fade` (off scale) expression changed — it must not reintroduce a size-based LOD shrink")
	}
	if strings.Contains(common, "float fade = rangeFade * edgeFade * lodFade;") {
		t.Error("the late `fade` still multiplies in an LOD shrink term — this is exactly the reported LOD gap")
	}

	// The true outer edge (outerIsLOD == 0) must still shrink via rangeFade —
	// only an LOD split (outerIsLOD != 0) switches to the dither.
	if !strings.Contains(common, "float rangeFrac = (outerIsLOD == 0) ? clamp(rangeFadeFraction, 0.0, 1.0) : 0.0;") {
		t.Error("rangeFade is no longer gated by outerIsLOD — it would double-fade an LOD split " +
			"(once via the dither, once via this shrink) or stop shrinking the sector's true outer edge")
	}
	if !strings.Contains(common, "float lodShrinkOut = (outerIsLOD != 0)") {
		t.Error("lodShrinkOut is no longer gated by outerIsLOD")
	}
}

// TestOuterIsLODMatchesTierPosition pins the Go-side condition that decides
// which of a tier's two possible outer-edge fades applies: an LOD split
// (every tier except the last) dithers, the sector's true Range edge (the
// last tier, or a single-tier kind like Rock) shrinks. Getting this backwards
// on a single-tier kind would silently drop RangeFadeFraction's shrink
// entirely (dither with lodFadeFraction's fixed width instead, ignoring the
// user's own slider); getting it backwards on an LOD split reintroduces the
// exact gap this whole change fixes.
func TestOuterIsLODMatchesTierPosition(t *testing.T) {
	src, err := os.ReadFile("grass.go")
	if err != nil {
		t.Fatalf("read grass.go: %v", err)
	}
	if !strings.Contains(string(src), `gl.Uniform1i(p.u("outerIsLOD"), boolToInt32(tier < tierCount-1))`) {
		t.Error(`outerIsLOD is no longer uploaded as boolToInt32(tier < tierCount-1) — ` +
			"a single-tier kind (tierCount==1, tier==0) must resolve to false (the true Range edge shrinks), " +
			"and every tier but the last of a 3-tier kind must resolve to true (dithers)")
	}
}

// TestBiomeLightUniformsMatchSceneShader is what lets UploadLightsTo /
// UploadShadowUniformsTo / UploadPointShadowUniformsTo drive the biome programs
// with no biome-specific upload path. Renaming a uniform on either side would
// otherwise fail silently — glGetUniformLocation returns -1 and every
// glUniform* call against it is a no-op, so the lights would simply go out.
func TestBiomeLightUniformsMatchSceneShader(t *testing.T) {
	scene := readBiomeShader(t, "fragment.glsl")
	biome := readBiomeShader(t, "biome_light.glsl")

	for _, u := range []string{
		"globalLightColor", "globalLightBrightness",
		"numDirLights", "dirLightDir", "dirLightColor", "dirLightBrightness",
		"shadowMapArray", "cascadeMatrix", "cascadeBias", "cascadeSplit",
		"dirLightShadowLayer", "dirLightCastShadows", "dirLightShadowStrength",
		"numPointLights", "pointLightPos", "pointLightColor",
		"pointLightBrightness", "pointLightRadius",
		"pointShadowMap", "pointLightCastShadows", "pointLightShadowStrength",
		"pointLightFarPlane",
	} {
		if !strings.Contains(scene, u) {
			t.Errorf("fragment.glsl no longer declares %q", u)
		}
		if !strings.Contains(biome, u) {
			t.Errorf("biome_light.glsl no longer declares %q", u)
		}
	}

	// The cascade constants are duplicated in three places by necessity (Go,
	// fragment.glsl, biome_light.glsl); pin the biome copies against shadow.go.
	if !strings.Contains(biome, "#define SHADOW_CASCADES       3") {
		t.Errorf("biome_light.glsl SHADOW_CASCADES is out of step with ShadowCascades=%d", ShadowCascades)
	}
	if !strings.Contains(biome, "#define SHADOW_CASCADE_BLEND  0.12") {
		t.Errorf("biome_light.glsl SHADOW_CASCADE_BLEND is out of step with ShadowCascadeBlend=%v", ShadowCascadeBlend)
	}
}

// TestGroundPatchShadesLikeTheStructuralFloor guards the reason the ground
// patch has no shading of its own. It is not a decal floating above the floor:
// a Water lakebed dips BELOW the floor it was painted on and a sand dune rises
// THROUGH it, so the two surfaces interpenetrate and are visible pixel against
// pixel. Any term the patch applies that fragment.glsl doesn't — a noise AO
// multiplier, a perturbed normal, a cheaper shadow filter — shows up along that
// intersection as two floors lit by different scenes, which is exactly what
// this used to look like.
func TestGroundPatchShadesLikeTheStructuralFloor(t *testing.T) {
	frag := readBiomeShader(t, "ground_frag.glsl")

	// Lighting comes from the shared block, on the mesh's own facet normal —
	// the same normal fragment.glsl recovers from derivatives for the floor.
	if !strings.Contains(frag, "biomeLighting(vWorldPos, n, vViewDepth") {
		t.Error("ground_frag.glsl must light the raw facet normal via biomeLighting; " +
			"a perturbed normal makes the patch disagree with the floor it intersects")
	}
	// No private darkening term between the texture and the scene lighting:
	// the patch is texture * scene lighting and nothing else.
	if !strings.Contains(frag, "vec3 color = tc.rgb * clamp(lighting, 0.0, 1.0);") {
		t.Error("ground_frag.glsl's final color is no longer texture * scene lighting")
	}
	for _, banned := range []string{"noise3", "hash13", "bumpN", "float ao"} {
		if strings.Contains(frag, banned) {
			t.Errorf("ground_frag.glsl reintroduced %q — the patch must shade exactly like fragment.glsl", banned)
		}
	}

	// And it must buy the full-cost lighting block: 5x5 PCF, point-light
	// cubemaps, and the cascade cross-fade, so a shadow edge lands in the same
	// place on the patch and on the floor. Asserted against the Go source
	// rather than by calling biomeLightSnippet, which resolves shader paths
	// relative to ExecutablePath and so needs a running binary.
	ground, err := os.ReadFile("biome_ground.go")
	if err != nil {
		t.Fatalf("read biome_ground.go: %v", err)
	}
	if !strings.Contains(string(ground), "biomeLightSnippet(2, true, true)") {
		t.Error("the ground patch's fragment stage no longer takes pcf=2 + point shadows + cascade blend")
	}
	if !strings.Contains(readBiomeShader(t, "biome_light.glsl"), "#ifdef BIOME_LIGHT_CASCADE_BLEND") {
		t.Error("biome_light.glsl no longer implements the cascade cross-fade")
	}
	// The scatter deliberately does NOT pay for the cross-fade: a blade or a
	// pebble is too small to show the seam it hides.
	grass, err := os.ReadFile("grass.go")
	if err != nil {
		t.Fatalf("read grass.go: %v", err)
	}
	if strings.Contains(string(grass), "biomeLightSnippet(1, true, true)") {
		t.Error("the scatter should not enable the cascade cross-fade")
	}
}

func TestBiomeShadowsCastsKind(t *testing.T) {
	s := BiomeShadowSettings{CastRockMoss: true, CastFoliage: false, CastCascades: 1, CastRange: 45}

	for _, k := range []int32{world.BiomeRock, world.BiomeMoss} {
		if !s.CastsKind(k) {
			t.Errorf("kind %d should cast when CastRockMoss is on", k)
		}
	}
	for _, k := range []int32{world.BiomeGrass, world.BiomeWildflower, world.BiomeCattail} {
		if s.CastsKind(k) {
			t.Errorf("kind %d should not cast when CastFoliage is off", k)
		}
	}
	// Sand and Water have no scatter at all.
	for _, k := range []int32{world.BiomeSand, world.BiomeWater} {
		if s.CastsKind(k) {
			t.Errorf("kind %d has no scatter and must never cast", k)
		}
	}

	s.CastFoliage = true
	if !s.CastsKind(world.BiomeGrass) {
		t.Error("grass should cast once CastFoliage is on")
	}
}

func TestBiomeShadowsAnyCast(t *testing.T) {
	cases := []struct {
		name string
		s    BiomeShadowSettings
		want bool
	}{
		{"defaults", BiomeShadowSettings{CastRockMoss: true, CastCascades: 2, CastRange: 0}, true},
		{"no kinds", BiomeShadowSettings{CastCascades: 2, CastRange: 45}, false},
		{"zero cascades", BiomeShadowSettings{CastRockMoss: true, CastCascades: 0, CastRange: 45}, false},
		// CastRange==0 means "no extra cap beyond the shadow map's own reach",
		// NOT "disabled" — see castRangeFor's doc comment — so this must still cast.
		{"zero range is auto, not off", BiomeShadowSettings{CastRockMoss: true, CastCascades: 1, CastRange: 0}, true},
	}
	for _, c := range cases {
		if got := c.s.AnyCast(); got != c.want {
			t.Errorf("%s: AnyCast() = %v, want %v", c.name, got, c.want)
		}
	}
}

// castRangeFor must never LENGTHEN a sector's reach — a brush painted with a
// short range would otherwise cast shadows from instances it never draws —
// and must clamp to the shadow map's own reach (a cascade split, a point
// light's far plane) BEFORE the user's own CastRange, since drawing an
// instance past the map's own extent is wasted vertex work regardless of what
// CastRange says.
func TestCastRangeForNeverExtends(t *testing.T) {
	s := BiomeShadowSettings{CastRange: 45}
	if got := s.castRangeFor(20, 0); got != 20 {
		t.Errorf("castRangeFor(20, 0) = %v, want the sector's own 20", got)
	}
	if got := s.castRangeFor(120, 0); got != 45 {
		t.Errorf("castRangeFor(120, 0) = %v, want the 45 user cap", got)
	}
	if got := s.castRangeFor(120, 30); got != 30 {
		t.Errorf("castRangeFor(120, 30) = %v, want the tighter 30 map reach", got)
	}
	// The map reach only tightens, never loosens past CastRange.
	if got := s.castRangeFor(120, 90); got != 45 {
		t.Errorf("castRangeFor(120, 90) = %v, want the 45 user cap (tighter than the 90 map reach)", got)
	}

	// A user CastRange of 0 means "no extra cap" rather than "nothing" — a
	// zero here must not silently clamp every sector to no reach at all.
	unset := BiomeShadowSettings{}
	if got := unset.castRangeFor(80, 0); got != 80 {
		t.Errorf("castRangeFor(80, 0) with no cap at all = %v, want 80", got)
	}
	if got := unset.castRangeFor(80, 30); got != 30 {
		t.Errorf("castRangeFor(80, 30) with no user cap = %v, want the 30 map reach", got)
	}
}

// biomeScatterBounds clips a sector to the camera ring. A sector well outside
// it must come back inverted, which is how forEachScatterSector recognises
// "nothing of this sector is scattered right now" and skips it.
func TestBiomeScatterBoundsClipsToCameraRing(t *testing.T) {
	ch := &world.Chunk{MinBound: mgl32.Vec3{-100, 0, -100}, MaxBound: mgl32.Vec3{100, 10, 100}}
	s := &world.BiomeSector{Points: []mgl32.Vec2{{0, 0}, {200, 0}, {200, 200}, {0, 200}}}

	// Camera inside the sector: the box is the ring, not the whole polygon.
	bmin, bmax := biomeScatterBounds(ch, s, mgl32.Vec3{100, 0, 100}, 30)
	if bmin.X() != 70 || bmax.X() != 130 || bmin.Z() != 70 || bmax.Z() != 130 {
		t.Errorf("ring clip = X[%v,%v] Z[%v,%v], want [70,130] on both", bmin.X(), bmax.X(), bmin.Z(), bmax.Z())
	}
	if bmin.Y() >= ch.MinBound.Y() || bmax.Y() <= ch.MaxBound.Y() {
		t.Errorf("vertical span [%v,%v] should exceed the chunk's [%v,%v] by the instance pad",
			bmin.Y(), bmax.Y(), ch.MinBound.Y(), ch.MaxBound.Y())
	}

	// Camera far away: inverted box, so the sector is skipped entirely.
	bmin, bmax = biomeScatterBounds(ch, s, mgl32.Vec3{-500, 0, -500}, 30)
	if bmin.X() <= bmax.X() && bmin.Z() <= bmax.Z() {
		t.Errorf("sector outside the ring should invert, got X[%v,%v] Z[%v,%v]",
			bmin.X(), bmax.X(), bmin.Z(), bmax.Z())
	}
}

func TestResolvedBoundaryJitter(t *testing.T) {
	b := &world.BiomeBrush{}
	if got := resolvedBoundaryJitter(b); got != world.DefaultBoundaryJitter {
		t.Errorf("resolvedBoundaryJitter(zero brush) = %v, want the default %v", got, world.DefaultBoundaryJitter)
	}
	b.BoundaryJitter = 7
	if got := resolvedBoundaryJitter(b); got != 7 {
		t.Errorf("resolvedBoundaryJitter(7) = %v, want 7 unchanged", got)
	}
}

// TestBoundaryJitterClampsAtZero pins the fix for the hole-at-the-player's-
// feet bug: a kind's innermost tier has lodStart == 0, and the alive test
// requires distJ >= lodStart, so a negative jitter sample on an instance
// already standing right next to the camera (true dist near 0) could push
// distJ below zero and wrongly kill it — jitter creating exactly the kind of
// visible artifact it exists to remove. distJ must be floored at 0, not left
// to go negative.
func TestBoundaryJitterClampsAtZero(t *testing.T) {
	common := readBiomeShader(t, "biome_scatter_common.glsl")
	if !strings.Contains(common, "distJ = max(distJ + n * boundaryJitter, 0.0);") {
		t.Error("distJ is no longer clamped at 0 after applying the jitter offset — " +
			"a negative jitter sample near the camera could cull instances at the player's own feet")
	}
}

// TestForEachScatterSectorPadsCullBoxForJitter pins the pad that keeps a
// jitter-extended instance from being culled: distJ can push an instance's
// effective boundary up to resolvedBoundaryJitter world units past a sector's
// nominal range/cast bound (see biome_scatter_common.glsl's distJ), so the
// cull box built from that nominal bound has to grow by the same amount or a
// jittered-in instance near the true edge could be culled by a frustum that
// never expected it to reach that far. A text pin rather than a geometric one
// (forEachScatterSector's cull path needs a real Frustum + Level to exercise
// meaningfully) — same tradeoff TestBiomeWindTimeIsLatchedBeforeShadowPass
// below makes for the same reason.
func TestForEachScatterSectorPadsCullBoxForJitter(t *testing.T) {
	src, err := os.ReadFile("grass.go")
	if err != nil {
		t.Fatalf("read grass.go: %v", err)
	}
	body := string(src)
	fn := strings.Index(body, "func forEachScatterSector(")
	if fn < 0 {
		t.Fatal("could not locate forEachScatterSector in grass.go")
	}
	end := strings.Index(body[fn:], "\nfunc ")
	if end < 0 {
		end = len(body) - fn
	}
	section := body[fn : fn+end]
	if !strings.Contains(section, "resolvedBoundaryJitter(&s.Brush)") {
		t.Error("forEachScatterSector's cull box no longer pads by resolvedBoundaryJitter — " +
			"a jittered instance near the true edge could be culled out of a frustum that never expected it to reach that far")
	}
}

// The wind clock has to be one value per frame shared by the color and depth
// passes; a blade that sways in one and not the other sheds its shadow.
func TestBiomeWindTimeIsLatchedBeforeShadowPass(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)
	latch := strings.Index(body, "BiomeWindTime =")
	pass := strings.Index(body, "RenderShadowPass(level.Lights")
	if latch < 0 || pass < 0 {
		t.Fatal("could not locate the wind latch or the shadow pass in main.go")
	}
	if latch > pass {
		t.Error("BiomeWindTime is latched after RenderShadowPass — the depth passes " +
			"would animate a frame out of step with the color pass")
	}
}
