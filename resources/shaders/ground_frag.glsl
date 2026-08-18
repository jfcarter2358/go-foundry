#version 410

// ground_frag.glsl — textured shading for the biome ground patch.
//
// Deliberately WITHOUT any surface-detail term of its own: no fake bump, no
// noise AO. The patch is a drape over the structural floor and routinely
// INTERSECTS it — a Water lakebed dips below the floor it was painted on,
// a sand dune rises through one — so the two surfaces end up visible side by
// side, pixel against pixel. Anything the patch layers on top of the scene
// lighting model (a darkening multiplier, a perturbed normal) reads there as
// the two floors being lit by different scenes, which is exactly the seam the
// shared lighting block below exists to avoid. The coarse unevenness the patch
// is supposed to show is real geometry, baked into the mesh by biome_ground.go,
// and comes through the shared lighting rather than beside it.
//
// So the shading here is fragment.glsl's, term for term: biomeLighting() from
// biome_light.glsl — the full scene model, every directional light with its
// cascade shadow (5x5 PCF plus the cascade cross-fade, matching the main
// shader tap for tap) and every point light with its cubemap, on the same
// uniforms fragment.glsl is fed — evaluated on the mesh's own facet normal,
// which is what fragment.glsl recovers from derivatives for the floor
// underneath. The patch is an ordinary opaque surface with an ordinary
// fragment count, so it can afford exactly what the structural floor pays.
//
// (The per-fragment bump/AO idea still lives in biome_frag.glsl, where it
// belongs: a scattered rock is a separate object sitting ON the ground, not a
// second copy of it, so it is free to shade richer than the floor does.)

in vec3 vWorldPos;
in vec3 vNormal;
in vec2 vUV;
in float vCoverage;
in float vViewDepth;

uniform sampler2D tex;
uniform int receiveShadows; // BiomeShadows.Receive — see biome_light.go

// Fake ground-contact shadow strength for whatever's scattered on THIS patch
// (see BiomeBrush.FakeShadowStrength) — 0 for kinds that don't have one (Rock,
// Sand, Water, or real per-instance casting already covering it). A flat
// post-lighting darkening multiplier rather than a separate shadow term: cheap
// (one uniform, one multiply, no samples), and "reasonable, not perfect" is
// exactly the brief — see biome_shadow_decal.go for the halo that extends this
// same idea onto the structural floor surrounding the patch, which is also what
// keeps this term from breaking the match with the floor: the two surfaces are
// darkened together, by the same painted strength, rather than only one of them.
uniform float fakeShadowStrength;

out vec4 outputColor;

void main() {
    // Slope gating: drop the patch entirely where the ground is outside the
    // brush's slope range, so the painted biome layer — texture included, not
    // just the scattered clumps on top — genuinely stops at the angle the user
    // set and the structural floor shows through beyond it. Thresholded at the
    // midpoint of slopeCoverage's feather band, so the cutoff lands on the real
    // slope contour rather than on a patch-triangle edge.
    if (vCoverage < 0.5) {
        discard;
    }

    vec3 n = normalize(vNormal);

    vec4 tc = texture(tex, vUV);

    vec3 lighting = biomeLighting(vWorldPos, n, vViewDepth, receiveShadows != 0);
    vec3 color = tc.rgb * clamp(lighting, 0.0, 1.0) * (1.0 - fakeShadowStrength);
    outputColor = vec4(color, tc.a);
}
