#version 410

// ground_frag.glsl — textured, procedurally bump-mapped shading for the
// biome ground patch. The mesh itself already carries the coarse
// displacement (dunes, unevenness); this adds fine-grained fake detail via a
// noise-gradient "bump map" so the simple geometry reads as rougher terrain
// than its triangle count would otherwise suggest — the same
// detail-from-shaders idea as the rock scatter in biome_frag.glsl.
//
// Lighting is biomeLighting() from biome_light.glsl — the full scene model,
// every directional light with its cascade shadow and every point light with
// its cubemap, on the same uniforms fragment.glsl is fed. The patch is an
// ordinary opaque surface with an ordinary fragment count, so it can afford
// exactly what the structural floor pays; anything less shows up immediately,
// because a shadow crossing the terrain would sweep up to the painted sector's
// edge and stop dead there.

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
// same idea onto the structural floor surrounding the patch.
uniform float fakeShadowStrength;

out vec4 outputColor;

float hash13(vec3 p) {
    p = fract(p * 0.1031);
    p += dot(p, p.yzx + 19.19);
    return fract((p.x + p.y) * p.z);
}

// Trilinearly-interpolated value noise.
float noise3(vec3 p) {
    vec3 i = floor(p);
    vec3 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    float n000 = hash13(i + vec3(0.0, 0.0, 0.0));
    float n100 = hash13(i + vec3(1.0, 0.0, 0.0));
    float n010 = hash13(i + vec3(0.0, 1.0, 0.0));
    float n110 = hash13(i + vec3(1.0, 1.0, 0.0));
    float n001 = hash13(i + vec3(0.0, 0.0, 1.0));
    float n101 = hash13(i + vec3(1.0, 0.0, 1.0));
    float n011 = hash13(i + vec3(0.0, 1.0, 1.0));
    float n111 = hash13(i + vec3(1.0, 1.0, 1.0));
    float nx00 = mix(n000, n100, f.x);
    float nx10 = mix(n010, n110, f.x);
    float nx01 = mix(n001, n101, f.x);
    float nx11 = mix(n011, n111, f.x);
    float nxy0 = mix(nx00, nx10, f.y);
    float nxy1 = mix(nx01, nx11, f.y);
    return mix(nxy0, nxy1, f.z);
}

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

    // Fake bump: perturb the flat-shaded normal by the gradient of a small
    // noise field, expressed in a local tangent frame so the perturbation
    // stays roughly on the surface regardless of its orientation.
    vec3 p = vWorldPos * 2.2;
    float e = 0.08;
    float h = noise3(p);
    vec3 grad = vec3(noise3(p + vec3(e, 0.0, 0.0)) - h,
                      noise3(p + vec3(0.0, e, 0.0)) - h,
                      noise3(p + vec3(0.0, 0.0, e)) - h) / e;
    vec3 up = abs(n.y) < 0.99 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
    vec3 tangent = normalize(cross(up, n));
    vec3 bitangent = cross(n, tangent);
    vec3 bumpN = normalize(n - (tangent * grad.x + bitangent * grad.z + n * grad.y) * 0.5);

    vec4 tc = texture(tex, vUV);
    float ao = mix(0.75, 1.0, h); // crevices (low noise) read darker

    // The bumped normal drives the diffuse term, but note that biomeLighting
    // also hands it to the shadow lookup's slope-scaled bias — acceptable here
    // because the bump is gentle (0.5 weight) and the patch is near-horizontal;
    // it is the flat facet the depth map actually holds.
    vec3 lighting = biomeLighting(vWorldPos, bumpN, vViewDepth, receiveShadows != 0);
    vec3 color = tc.rgb * ao * clamp(lighting, 0.0, 1.0) * (1.0 - fakeShadowStrength);
    outputColor = vec4(color, tc.a);
}
