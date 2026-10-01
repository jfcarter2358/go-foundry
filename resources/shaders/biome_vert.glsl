#version 430

// biome_vert.glsl — color-pass vertex stage for the GPU-scattered biome pass.
//
// Placement and shaping live in biome_scatter_common.glsl, shared verbatim with
// the two depth stages (biome_shadow_vert.glsl, biome_cube_vert.glsl) so a
// blade casts the shadow of the blade you can see. All this stage adds is
// lighting.
//
// FOLIAGE IS LIT HERE, PER VERTEX, AND THAT INCLUDES ITS SHADOW LOOKUP — the
// single most important performance decision in the biome pass. Grass caps out
// at 320x320 instances per sector (maxGrassGridN in grass.go); at ~8 vertices
// per blade template that is a large but bounded vertex count, whereas the same
// grass covering half the screen is an unbounded fragment count. Sampling the
// cascade once per VERTEX and letting it interpolate costs orders of magnitude
// less than fragment.glsl's 25-tap PCF per pixel — and looks better besides: a
// blade narrower than a shadow texel, filtered per fragment, swims as the
// camera moves. Rocks and moss are the exception and light per fragment in
// biome_frag.glsl, where a bump-perturbed normal and a specular highlight
// actually need the resolution; there are two orders of magnitude fewer of them.
//
// That one evaluation point is a 3x3-averaged tap (BIOME_LIGHT_PCF 1 — see
// GrassInit), not a single hard one: a boolean in/out test has nothing to
// smooth it, so a blade near a shadow edge would flip fully lit/shadowed
// outright as the cascade (necessarily refit to the camera every frame)
// nudged which texel the sample landed in — a flicker across the whole
// blade, since one vertex decides its entire color. Averaging turns that
// flip into a soft ramp instead.

layout(location = 0) in vec3  aLocal;   // template-local position (blade: y in [0,1] up; rock: unit blob)
layout(location = 1) in vec3  aNormal;  // template normal (rock lighting; foliage ignores)
layout(location = 2) in float aParam;   // see the aParam-encoding note in biome_scatter_common.glsl

uniform mat4 projView;
uniform mat4 model;

// BiomeShadows.Receive (biome_light.go). Off skips the cascade lookup entirely
// so its cost can be measured against zero rather than estimated.
uniform int receiveShadows;

// Foliage samples its shadow at its own vertex position raised slightly along
// +Y. Without the lift, a blade standing in a sward that casts (BiomeShadows.
// CastFoliage) reads its own footprint in the depth map at ground contact and
// the whole plant goes dark; with it, the blade still catches its NEIGHBOURS'
// shadows, which is the effect worth having.
const float BIOME_FOLIAGE_SHADOW_LIFT = 0.06;

out vec3  vColor;     // foliage: final Gouraud color. Rocks ignore this.
out vec3  vWorldPos;  // rocks: for per-fragment lighting/bump
out vec3  vNormal;    // rocks: flat facet normal, world space
out vec3  vBaseColor; // rocks: per-instance tinted base color
out float vViewDepth; // rocks: cascade selection, matching vertex.glsl's fragViewDepth

void main() {
    if (!biomePlace(gl_InstanceID, aLocal, aNormal, aParam)) {
        // Collapse the whole instance off-screen; it clips away.
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vColor = vec3(0.0);
        vWorldPos = vec3(0.0);
        vNormal = vec3(0.0);
        vBaseColor = vec3(0.0);
        vViewDepth = 0.0;
        return;
    }

    // bWorldPos/bNormal are chunk-local (biomePlace's own lattice runs in the
    // owning chunk's local frame — see biome_scatter_common.glsl and the
    // camXZ uniform's doc comment); model carries them out to world space in
    // one step, same as vertex.glsl, before anything below touches lighting
    // or shadows, both of which need true world-space inputs.
    vec4 worldPos4 = model * vec4(bWorldPos, 1.0);
    vec3 worldPos = worldPos4.xyz;
    vec3 worldNormal = transpose(inverse(mat3(model))) * bNormal;

    vWorldPos = worldPos;
    vNormal = worldNormal;
    vViewDepth = biomeViewDepth(worldPos);

    if (shapeKind == 1) {
        // Rock/moss: hand everything to biome_frag.glsl, which needs the
        // per-fragment bumped normal before it can light anything.
        vBaseColor = bBaseColor;
        vColor = vec3(0.0);
    } else {
        // Foliage: full scene lighting — global ambient, every directional
        // light with its cascade shadow, every point light in range — resolved
        // here and interpolated. Same uniforms and same math as the floor
        // underneath, so a blade and the ground it stands on agree.
        vec3 lightPos = worldPos + vec3(0.0, BIOME_FOLIAGE_SHADOW_LIFT, 0.0);
        vec3 lighting = biomeLighting(lightPos, worldNormal, vViewDepth, receiveShadows != 0, true);
        vColor = giDebugAlbedo(bBaseColor) * clamp(lighting, 0.0, 1.0) * bAO;
        vBaseColor = vec3(0.0);
    }

    gl_Position = projView * worldPos4;
}
