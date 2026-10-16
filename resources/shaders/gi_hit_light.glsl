// gi_hit_light.glsl — the light a GI ray's hit sends back (render/gi.go):
// the scene's lights at the hit, one of them shadowed by a ray through the
// distance field (directAt), and last frame's probes for the next bounce
// (bounceAt). Shared by the probe trace (gi_probe_trace_comp.glsl) and the
// reflection capture (env_capture_comp.glsl), so a reflection shows a wall
// lit exactly as the probes see it.
//
// The including file declares `uniform float maxDistance` (how far a ray
// looks) and splices gi_sdf_common.glsl and light_model.glsl ahead of it; the
// lights' GI switches below are render.giRenderer.uploadTraceLights'.

#ifndef GI_HIT_LIGHT_GLSL
#define GI_HIT_LIGHT_GLSL

#include "scene_block.glsl"

// The scene's lights: the scene block's, the ones the world programs are lit
// by (world.UploadLightsTo). Whether each casts, and its GI switch
// (Light.NoGI), are GI's own (uploadTraceLights): the block's cast flags are
// the shadow pass's answer — "has a cascade layer this frame" — and the trace
// runs before that pass, and asks only whether the light casts at all.
uniform bool  giDirLightCasts[MAX_DIR_LIGHTS];
uniform bool  giPointLightCasts[MAX_POINT_LIGHTS];
uniform int   giDirLightOn[MAX_DIR_LIGHTS];
uniform int   giPointLightOn[MAX_POINT_LIGHTS];

float luminance(vec3 c) { return dot(c, vec3(0.2126, 0.7152, 0.0722)); }

// giHash is a 32-bit integer mix (Chris Wellons' lowbias32): a seed's bits
// spread over the whole word, so neighbouring rays and frames pick unrelated
// lights.
uint giHash(uint x) {
    x ^= x >> 16;
    x *= 0x7feb352du;
    x ^= x >> 15;
    x *= 0x846ca68bu;
    x ^= x >> 16;
    return x;
}

// directAt is the light from the scene's lights reaching a surface at p facing
// n — fragment.glsl's dirLightContrib and pointLightContrib, with the shadow
// maps' answer replaced by a ray through the field, which unlike a shadow map
// also knows about what is behind the camera.
//
// A shadow ray is a march through the field as long as the ray that found p,
// and it used to be paid once per shadowing light: the sun and every lamp in
// range, four or five marches a ray in level1's hangar. Now each ray shadows
// ONE of them, picked at random (u, uniform in [0, 1)) with a chance in
// proportion to the light it would bring unshadowed, and counts its shadowed
// light divided by that chance. That is an unbiased estimate of the sum — its
// average over the picks is exactly the old answer — so what it trades is
// noise, not brightness: and a probe texel already averages its share of the
// sixty-four rays, and keeps most of its old value each update (the
// hysteresis), which is what smooths the noise out. The lights that cast no
// shadow cost no march, and are summed as they always were.
//
// The pick is made as the lights go by, with nothing kept but the one picked
// so far (a reservoir of one): each candidate replaces it with the chance its
// weight is of the total so far, which leaves every candidate picked in
// proportion to its weight at the end. One uniform number serves for every
// choice, rescaled after each to be uniform again over what the choice left.
// (Lists of the candidates, picked from afterwards, cost more than every
// shadow ray they saved: arrays a GPU thread indexes at run time spill out of
// its registers.)
struct giPick {
    vec3 c;     // its unshadowed light
    vec3 l;     // toward it
    float maxT; // how far its shadow ray goes
    float w;    // its weight
};

void giOffer(inout giPick pick, inout float total, inout float u, vec3 c, vec3 l, float maxT) {
    float w = luminance(c);
    if (w <= 0.0) {
        return;
    }
    total += w;
    float chance = w / total;
    if (u < chance) {
        pick = giPick(c, l, maxT, w);
        u = u / chance;
    } else {
        u = (u - chance) / (1.0 - chance);
    }
}

vec3 directAt(vec3 p, vec3 n, float u) {
    vec3 sum = vec3(0.0);
    giPick pick = giPick(vec3(0.0), vec3(0.0, 1.0, 0.0), 0.0, 0.0);
    float total = 0.0;
    for (int i = 0; i < numDirLights && i < MAX_DIR_LIGHTS; i++) {
        if (giDirLightOn[i] == 0) {
            continue;
        }
        vec3 l = -normalize(dirLightDir[i]);
        float diff = lightWrapTerm(dot(n, l));
        if (diff <= 0.0) {
            continue;
        }
        vec3 c = dirLightColor[i] * dirLightBrightness[i] * diff;
        if (giDirLightCasts[i]) {
            giOffer(pick, total, u, c, l, maxDistance * 2.0);
        } else {
            sum += c;
        }
    }
    for (int i = 0; i < numPointLights && i < MAX_POINT_LIGHTS; i++) {
        if (giPointLightOn[i] == 0) {
            continue;
        }
        vec3 toLight = pointLightPos[i] - p;
        float dist = length(toLight);
        if (dist >= pointLightRadius[i] || dist < 1e-4) {
            continue;
        }
        vec3 l = toLight / dist;
        float t = 1.0 - dist / pointLightRadius[i];
        float diff = lightWrapTerm(dot(n, l));
        if (diff <= 0.0) {
            continue;
        }
        vec3 c = pointLightColor[i] * pointLightBrightness[i] * diff * t * t;
        if (giPointLightCasts[i]) {
            giOffer(pick, total, u, c, l, dist);
        } else {
            sum += c;
        }
    }
    if (pick.w <= 0.0) {
        return sum;
    }
    float shadow = giSdfShadow(p, pick.l, pick.maxT);
    return sum + pick.c * (shadow * total / pick.w);
}

// bounceAt is last frame's probe light at a ray's hit, for the next bounce:
// the finest cascade that holds p, its eight probes weighted by distance and
// facing only. The shading's visibility test is left out — a bounce of a
// bounce is a small term, and this runs once per ray.
vec3 bounceAt(vec3 p, vec3 n) {
    for (int c = 0; c < giCascades && c < GI_MAX_CASCADES; c++) {
        if (giCascadeCover(c, p) <= 0.0) {
            continue;
        }
        float s = giCascade[c].w;
        vec3 q = (p + n * 0.3 * s) / s;
        ivec3 base = ivec3(floor(q));
        vec3 a = q - vec3(base);
        vec2 irrSize = vec2(textureSize(giIrradianceAtlas, 0));
        vec3 sum = vec3(0.0);
        float wsum = 0.0;
        for (int i = 0; i < 8; i++) {
            ivec3 off = ivec3(i, i >> 1, i >> 2) & ivec3(1);
            ivec3 wi = base + off;
            ivec2 tile = giTile(giSlot(wi), c);
            vec3 tri = mix(1.0 - a, a, vec3(off));
            vec3 toProbe = normalize(vec3(wi) * s - p);
            float wrap = (dot(toProbe, n) + 1.0) * 0.5;
            float w = tri.x * tri.y * tri.z * (wrap * wrap + 0.2);
            sum += textureLod(giIrradianceAtlas, giAtlasUV(tile, n, GI_IRR_TILE, irrSize), 0.0).rgb * w;
            wsum += w;
        }
        return wsum > 0.0 ? sum / wsum : vec3(0.0);
    }
    vec3 ambient = globalLightColor * globalLightBrightness;
    return ambientAt(ambient, n);
}

#endif
