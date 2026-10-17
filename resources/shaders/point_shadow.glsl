// point_shadow.glsl — a point light's cubemap shadow, for every pass that
// receives one: the world shader (fragment.glsl) and the biome passes that
// define BIOME_LIGHT_POINT_SHADOWS (biome_light.glsl). One copy, so the floor
// and the grass on it agree on where a lamp's shadow falls.
//
// The cubemaps hold each face's distance to the nearest caster as a depth,
// dist / farPlane (shadow_cube_frag.glsl), and are COMPARISON textures
// (render.EnsurePointShadowMaps sets the compare mode): a tap hands the
// hardware this fragment's own distance and gets back how much of the 2x2
// texels around it are farther away — a filtered 0..1, not one yes/no. Eight
// such taps on a disk across the light's direction make the penumbra.
//
// The disk turns per pixel and, in a TAA frame, per frame
// (shadowNoiseFrame), so the eight taps land somewhere new every frame and
// TAA's history sums them into many more — the noise is the sampling, and it
// resolves. Outside TAA the disk holds still, so a frame never shimmers.
//
// It used to take twenty unfiltered-compare taps — reading the depth through
// a plain samplerCube, so the bilinear filter averaged DEPTHS and the compare
// came after — per casting light per fragment.

#ifndef POINT_SHADOW_GLSL
#define POINT_SHADOW_GLSL

#include "scene_block.glsl"

// Every casting light's cube, one cube-map array (render/shadow.go): cube
// pointShadowCube[light]. One sampler where there were eight.
uniform samplerCubeArrayShadow pointShadowCubes;

#define POINT_SHADOW_TAPS 8
// How far the disk reaches, in the units of the old kernel's diskRadius: its
// twenty offsets were a cube's corners and edge midpoints, whose part across
// the light direction averages ~1.25 of it and reaches ~1.4; a sunflower disk
// of radius 1.6 averages ~1.07 and the hardware's 2x2 filter adds the rest.
#define POINT_SHADOW_SPREAD 1.6
#define POINT_SHADOW_BIAS 0.05

// pointShadowTurn is this pixel's turn of the tap disk, in turns: interleaved
// gradient noise, stepped by the golden ratio each TAA frame.
float pointShadowTurn() {
    if (shadowNoiseFrame == 0) {
        return 0.0;
    }
    vec2 p = gl_FragCoord.xy;
    float ign = fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
    return fract(ign + float(shadowNoiseFrame) * 0.618034);
}

// pointShadow is how shadowed worldPos is from point light lightIdx: 0 lit,
// 1 fully in shadow. The caller checks pointLightCastShadows first.
float pointShadow(int lightIdx, vec3 worldPos) {
    int cube = pointShadowCube[lightIdx];
    if (cube < 0) {
        return 0.0;
    }
    vec3 fragToLight = worldPos - pointLightPos[lightIdx];
    float currentDist = length(fragToLight);
    float farPlane = pointLightFarPlane[lightIdx];

    // Disk radius scales with distance for consistent softness, as before.
    float diskRadius = (1.0 + currentDist / farPlane) * 0.02 * POINT_SHADOW_SPREAD;
    // Lit where the caster the cube saw is no nearer than this fragment, less
    // the bias — the old `currentDist - bias > closest` test, as a compare.
    float ref = (currentDist - POINT_SHADOW_BIAS) / farPlane;

    // Two axes across the light direction for the disk to lie in.
    vec3 n = fragToLight / max(currentDist, 1e-4);
    vec3 helper = abs(n.y) < 0.99 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
    vec3 t = normalize(cross(helper, n));
    vec3 b = cross(n, t);

    float turn = 6.2831853 * pointShadowTurn();
    float lit = 0.0;
    for (int i = 0; i < POINT_SHADOW_TAPS; i++) {
        // A Vogel (sunflower) disk: even coverage for any tap count.
        float r = sqrt((float(i) + 0.5) / float(POINT_SHADOW_TAPS));
        float a = float(i) * 2.39996323 + turn;
        vec3 offset = (t * cos(a) + b * sin(a)) * (r * diskRadius);
        lit += texture(pointShadowCubes, vec4(fragToLight + offset, float(cube)), ref);
    }
    return 1.0 - lit / float(POINT_SHADOW_TAPS);
}

#endif
