// gi_probe_trace_comp.glsl — tracing and updating the GI probes (render/gi.go).
//
// One workgroup per probe this frame traces, one invocation per ray. Which
// probes those are the placement pass decided (gi_probe_update_comp.glsl): it
// lists them, and this pass is dispatched indirectly over the list, so a
// probe resting or waiting for its turn costs nothing here rather than a
// workgroup that read the probe's state and left. Each ray is sphere-traced
// through the distance field; where it lands it picks up what that surface
// sends back — its albedo times the lights reaching it (one of them, picked at
// random, shadowed by a ray of its own through the same field: see directAt)
// plus last frame's probes there, which is how the bounces pile up a frame at
// a time — and where it leaves the field it sees the sky. The rays then fold
// into the probe's two octahedral tiles: irradiance, cosine-weighted, and the
// mean and mean-square distance to what was hit, sharply weighted, for the
// shading's visibility test. A tile's border texels repeat its edge, wrapped
// as the octahedron folds, so bilinear filtering at an edge reads the right
// neighbour.
//
// GI_RAYS and GI_PROBES_PER_CASCADE (defines), gi_sdf_common.glsl and
// light_model.glsl (the probe layout and the lighting terms) are spliced in
// ahead of this file.

layout(local_size_x = GI_RAYS) in;

layout(r11f_g11f_b10f, binding = 0) uniform image2D irradianceImage;
layout(rg16f, binding = 1) uniform image2D visibilityImage;
layout(rgba16f, binding = 2) uniform image2D probeImage;

// The probes to trace this frame, cascade by cascade: the placement pass
// appends each one's slot (its index in the cascade, x fastest) at
// cascade * GI_PROBES_PER_CASCADE, and counts them into the indirect
// dispatch this pass runs as.
layout(std430, binding = 0) readonly buffer ProbeList {
    uint probeList[];
};

uniform int   cascade;
uniform int   frameIndex;
uniform mat3  rayRotation;  // a fresh random turn of the ray set each frame
uniform float maxDistance;  // how far a ray looks before it calls it sky
uniform float hysteresis;   // how much of the old value a texel keeps

// The scene's lights: the scene block's, the ones the world programs are lit
// by (world.UploadLightsTo). Whether each casts, and its GI switch
// (Light.NoGI), are GI's own (uploadTraceLights): the block's cast flags are
// the shadow pass's answer — "has a cascade layer this frame" — and the trace
// runs before that pass, and asks only whether the light casts at all.
#include "scene_block.glsl"

#define MAX_STEPS 32
#define STATE_ACTIVE 1.0
#define STATE_FRESH 2.0
#define STATE_RESTING 4.0

shared vec3  rayRadiance[GI_RAYS];
shared vec3  rayDir[GI_RAYS];
shared float rayDist[GI_RAYS];

// The interior texels the fold works out (the borders are copies of them),
// and how many each invocation takes: see main.
#define IRR_TEXELS ((GI_IRR_TILE - 2) * (GI_IRR_TILE - 2))
#define FOLD_TEXELS (IRR_TEXELS + (GI_VIS_TILE - 2) * (GI_VIS_TILE - 2))
#define FOLD_SLOTS ((FOLD_TEXELS + GI_RAYS - 1) / GI_RAYS)

// The finished interior texels, for the border texels that repeat them.
shared vec3 irrOut[(GI_IRR_TILE - 2) * (GI_IRR_TILE - 2)];
shared vec2 visOut[(GI_VIS_TILE - 2) * (GI_VIS_TILE - 2)];

// Spherical Fibonacci: n directions spread evenly over the sphere.
vec3 fibonacciDir(int i, int n) {
    const float golden = 2.39996323; // pi * (3 - sqrt(5))
    float y = 1.0 - (float(i) + 0.5) / float(n) * 2.0;
    float r = sqrt(max(1.0 - y * y, 0.0));
    float phi = float(i) * golden;
    return vec3(cos(phi) * r, y, sin(phi) * r);
}

// luminance, giHash, directAt and bounceAt — what a ray's hit sends back —
// are gi_hit_light.glsl's, shared with the reflection capture
// (env_capture_comp.glsl). It reads maxDistance, declared above.
#include "gi_hit_light.glsl"

// borderSource is the interior texel a tile texel copies: itself inside, and
// on the border the edge texel the octahedron folds onto it.
ivec2 borderSource(ivec2 t, int size) {
    int n = size - 2; // interior texels on a side, at 1..n
    bool bx = t.x == 0 || t.x == n + 1;
    bool by = t.y == 0 || t.y == n + 1;
    if (bx && by) {
        return ivec2(t.x == 0 ? n : 1, t.y == 0 ? n : 1);
    }
    if (by) {
        return ivec2(n + 1 - t.x, t.y == 0 ? 1 : n);
    }
    if (bx) {
        return ivec2(t.x == 0 ? 1 : n, n + 1 - t.y);
    }
    return t;
}

vec3 texelDir(ivec2 src, int size) {
    float n = float(size - 2);
    vec2 oct = (vec2(src - 1) + 0.5) / n * 2.0 - 1.0;
    return giOctDecode(oct);
}

void main() {
    int linear = int(probeList[cascade * GI_PROBES_PER_CASCADE + int(gl_WorkGroupID.x)]);
    ivec3 slot = ivec3(linear % giDims.x, (linear / giDims.x) % giDims.y, linear / (giDims.x * giDims.y));
    ivec2 tile = giTile(slot, cascade);
    vec4 pd = imageLoad(probeImage, tile);
    bool fresh = mod(pd.w, 4.0) >= STATE_FRESH;
    ivec3 lo = ivec3(giCascade[cascade].xyz);
    float s = giCascade[cascade].w;
    ivec3 wi = lo + giSlot(slot - lo);
    vec3 origin = vec3(wi) * s + pd.xyz;

    // --- trace: one ray per invocation ---
    int r = int(gl_LocalInvocationIndex);
    vec3 dir = rayRotation * fibonacciDir(r, GI_RAYS);
    vec3 ambient = globalLightColor * globalLightBrightness;
    float t;
    int level;
    float voxel;
    vec3 radiance;
    if (giSdfTraceAt(origin, dir, maxDistance, MAX_STEPS, t, level, voxel)) {
        vec3 hp = origin + dir * t;
        // The trace's last read was of hp itself, so its level and voxel are
        // what giSdf would answer there; the normal's taps start from them.
        vec3 n = giSdfNormalFrom(hp, voxel, level);
        vec4 surf = giSdfSurface(hp);
        vec3 lp = hp + n * voxel; // off the surface, for its own shadow rays
        // This ray's pick of the light to shadow: a fresh one for every ray
        // of every probe, every frame.
        uint seed = giHash(uint(linear * GI_RAYS + r) ^ giHash(uint(frameIndex) * 3u + uint(cascade)));
        float u = float(seed >> 8) * (1.0 / 16777216.0);
        radiance = surf.rgb * (directAt(lp, n, u) + bounceAt(lp, n)) + surf.rgb * surf.a;
    } else {
        // The sky, as the flat ambient has always stood for it.
        radiance = ambientAt(ambient, dir);
        t = maxDistance;
    }
    rayRadiance[r] = radiance;
    rayDir[r] = dir;
    rayDist[r] = t;
    barrier();

    // --- fold the rays into the probe's two tiles ---
    // The interior texels only: a border texel is a copy of the interior one
    // the octahedron folds onto it (borderSource), so it is written from that
    // one's result below rather than folded over every ray again — 232 folds
    // a probe instead of 320.
    //
    // Each invocation folds FOLD_SLOTS texels at once, in one walk over the
    // rays: a ray read from shared memory once serves all of them, where a
    // texel at a time read every ray again for each, and the irradiance and
    // distance texels share the walk instead of diverging into two. A slot
    // is a texel of either tile — both forms of its weight are worked out
    // and the one it needs kept, which is cheaper than the branch.
    float h = fresh ? 0.0 : hysteresis;
    vec3 tdir[FOLD_SLOTS];
    bool irrSlot[FOLD_SLOTS];
    vec3 sum[FOLD_SLOTS];
    float wsum[FOLD_SLOTS];
    for (int m = 0; m < FOLD_SLOTS; m++) {
        int k = r + m * GI_RAYS;
        irrSlot[m] = k < IRR_TEXELS;
        int size = irrSlot[m] ? GI_IRR_TILE : GI_VIS_TILE;
        int inner = size - 2;
        int idx = irrSlot[m] ? k : k - IRR_TEXELS;
        // A slot past the last texel folds with no direction: every weight
        // zero, and nothing stored.
        tdir[m] = k < FOLD_TEXELS ? texelDir(ivec2(idx % inner, idx / inner) + 1, size) : vec3(0.0);
        sum[m] = vec3(0.0);
        wsum[m] = 0.0;
    }
    for (int j = 0; j < GI_RAYS; j++) {
        vec3 rd = rayDir[j];
        vec3 radiance = rayRadiance[j];
        float d = rayDist[j];
        vec3 moments = vec3(d, d * d, 0.0);
        for (int m = 0; m < FOLD_SLOTS; m++) {
            float w = max(dot(tdir[m], rd), 0.0);
            // cos^32 by squaring: the sharp lobe DDGI weights distances by,
            // without a pow per ray per texel.
            float wv = w * w;
            wv *= wv; wv *= wv; wv *= wv; wv *= wv;
            sum[m] += irrSlot[m] ? radiance * w : moments * wv;
            wsum[m] += irrSlot[m] ? w : wv;
        }
    }
    for (int m = 0; m < FOLD_SLOTS; m++) {
        int k = r + m * GI_RAYS;
        if (k >= FOLD_TEXELS) {
            continue;
        }
        int size = irrSlot[m] ? GI_IRR_TILE : GI_VIS_TILE;
        int inner = size - 2;
        int idx = irrSlot[m] ? k : k - IRR_TEXELS;
        ivec2 texel = tile * size + ivec2(idx % inner, idx / inner) + 1;
        if (irrSlot[m]) {
            vec3 value = wsum[m] > 0.0 ? sum[m] / wsum[m] : vec3(0.0);
            vec3 old = imageLoad(irradianceImage, texel).rgb;
            float hk = h;
            // A big change — a flash, a door — is let through quickly rather
            // than faded in over a second.
            float lo2 = luminance(old), ln = luminance(value);
            if (abs(ln - lo2) / max(lo2, 0.05) > 0.5) {
                hk = min(hk, 0.8);
            }
            vec3 v = mix(value, old, hk);
            imageStore(irradianceImage, texel, vec4(v, 1.0));
            irrOut[idx] = v;
        } else {
            vec2 value = wsum[m] > 0.0 ? sum[m].xy / wsum[m] : vec2(maxDistance, maxDistance * maxDistance);
            vec2 old = imageLoad(visibilityImage, texel).rg;
            vec2 v = mix(value, old, h);
            imageStore(visibilityImage, texel, vec4(v, 0.0, 0.0));
            visOut[idx] = v;
        }
    }
    barrier();

    // --- the borders, from the interior texels they repeat ---
    // A tile of side `size` has 4 * (size - 1) border texels, walked as its
    // four edges, each starting at a corner. What a border texel stored
    // before was always its source's value (it was folded from the same
    // direction, blended with the same old value), so the copy is exactly
    // what folding it again gave.
    const int irrBorder = 4 * (GI_IRR_TILE - 1);
    const int visBorder = 4 * (GI_VIS_TILE - 1);
    for (int k = r; k < irrBorder + visBorder; k += GI_RAYS) {
        bool irr = k < irrBorder;
        int size = irr ? GI_IRR_TILE : GI_VIS_TILE;
        int e = irr ? k : k - irrBorder;
        int side = e / (size - 1), along = e % (size - 1);
        ivec2 tt = side == 0 ? ivec2(along, 0)
                 : side == 1 ? ivec2(size - 1, along)
                 : side == 2 ? ivec2(size - 1 - along, size - 1)
                 : ivec2(0, size - 1 - along);
        ivec2 src = borderSource(tt, size) - 1;
        ivec2 texel = tile * size + tt;
        if (irr) {
            imageStore(irradianceImage, texel, vec4(irrOut[src.y * (size - 2) + src.x], 1.0));
        } else {
            imageStore(visibilityImage, texel, vec4(visOut[src.y * (size - 2) + src.x], 0.0, 0.0));
        }
    }

    // Traced once: no longer fresh.
    if (r == 0 && fresh) {
        imageStore(probeImage, tile, vec4(pd.xyz, pd.w - STATE_FRESH));
    }
}
