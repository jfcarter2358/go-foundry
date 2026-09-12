#version 410

// cloud_shadow_bake_frag.glsl — the cloud shadow map, one texel at a time.
//
// fog.glsl and volume_common.glsl are spliced in above this, so the density
// marched here is the density the volume pass draws — see the header of
// volume_common.glsl. What cloud_shadow.glsl reads back is described there.
//
// Each texel is one line of sunlight crossing the bake's light-space box from
// the sun side (light depth 0) to the far side (light depth 1). The line is
// clipped to the union of every cloud's region first, so a texel with no cloud
// over it costs a handful of intersections and no samples at all.

#define MAX_SHADOW_CLOUDS 8
#define SHADOW_BAKE_STEPS 24

uniform int         numShadowClouds;
uniform CloudParams shadowClouds[MAX_SHADOW_CLOUDS];
uniform mat4        bakeInvLightViewProj;
uniform vec2        bakeTargetSize;
uniform float       bakeTexelWorld; // world size of one texel, for mip selection

out vec4 outShadow;

void main() {
    vec2 uv = gl_FragCoord.xy / bakeTargetSize;
    vec4 a = bakeInvLightViewProj * vec4(uv * 2.0 - 1.0, -1.0, 1.0);
    vec4 b = bakeInvLightViewProj * vec4(uv * 2.0 - 1.0, 1.0, 1.0);
    vec3 ro = a.xyz / a.w;
    vec3 re = b.xyz / b.w;
    float len = distance(ro, re);
    vec3 rd = (re - ro) / max(len, 1e-4);

    float lo = len;
    float hi = 0.0;
    for (int i = 0; i < MAX_SHADOW_CLOUDS; ++i) {
        if (i >= numShadowClouds) {
            break;
        }
        float t0 = 0.0;
        float t1 = len;
        if (cloudInterval(shadowClouds[i], ro, rd, t0, t1)) {
            lo = min(lo, t0);
            hi = max(hi, t1);
        }
    }
    if (hi <= lo) {
        // Open sky: every light gets through, and no receiver is below any mass.
        outShadow = vec4(1.0, 1.0, 0.0, 1.0);
        return;
    }

    float dt = (hi - lo) / float(SHADOW_BAKE_STEPS);
    float footprint = max(bakeTexelWorld, dt);
    float T = 1.0;
    float wSum = 0.0;
    float zSum = 0.0;
    for (int k = 0; k < SHADOW_BAKE_STEPS; ++k) {
        float t = lo + dt * (float(k) + 0.5);
        vec3 p = ro + rd * t;
        float sigma = 0.0;
        for (int i = 0; i < MAX_SHADOW_CLOUDS; ++i) {
            if (i >= numShadowClouds) {
                break;
            }
            sigma += cloudDensity(shadowClouds[i], p, footprint, false) * shadowClouds[i].absorption;
        }
        if (sigma <= 0.0) {
            continue;
        }
        float Ts = exp(-sigma * dt);
        float w = T * (1.0 - Ts);
        wSum += w;
        zSum += w * (t / len);
        T *= Ts;
        if (T < 0.005) {
            break;
        }
    }
    outShadow = vec4(T, wSum > 1e-4 ? zSum / wSum : 1.0, 0.0, 1.0);
}
