#version 410

// volume_frag.glsl — one volumetric draw: a cloud volume, the sky's cloud
// deck, or a group of particle blobs.
//
// fog.glsl and volume_common.glsl are spliced in above this (VolumetricInit).
//
// The fragment stage owns the whole ray. It is rebuilt from gl_FragCoord and
// the inverse projection rather than read from the proxy's interpolated
// position, which is what lets one shader serve a box proxy, the deck's
// fullscreen triangle, a half-resolution target and a full one alike.
//
// The march is the standard single-scattering integral, with the analytic step
// integration from Hillaire's "Physically Based Sky, Atmosphere and Cloud
// Rendering in Frostbite": over a step of constant extinction sigma and source
// S, the light added is T * (S - S * exp(-sigma dt)) / sigma. A naive
// S * dt * T overshoots in dense cloud, and the overshoot is visible as
// brightening that depends on the step count.
//
// Output is PREMULTIPLIED — (light, 1 - transmittance) — and is blended with
// ONE, ONE_MINUS_SRC_ALPHA, so a fireball's emitted light adds over what is
// behind it while its smoke hides it, in one blend state.
//
// The ray is clipped by the scene: volSceneDist is the distance from the eye
// to the nearest opaque surface under this pixel (the depth downsample in
// volume_depth_frag.glsl), so a cloud behind a wall is not drawn over it and a
// fireball half-buried in the floor is half-buried.

#define VOL_KIND_CLOUD 0
#define VOL_KIND_BLOBS 1
#define VOL_KIND_DECK  2

#define VOL_MAX_STEPS       128
#define VOL_MAX_LIGHT_STEPS 8

uniform int         volKind;
uniform CloudParams volCloud;        // CLOUD and DECK
uniform mat3        volProxyBasis;   // BLOBS: world offset -> the group's unit box
uniform vec3        volProxyCenter;

uniform mat4      volInvProjView;
uniform vec3      volCamPos;
uniform vec2      volTargetSize;
uniform sampler2D volSceneDist;
uniform int       volSteps;
uniform int       volLightSteps;
uniform float     volLightReach;     // how far toward the sun a sample looks
uniform float     volDeckRange;
uniform float     volPixelFootprint; // world size of a target pixel one unit from the eye

uniform vec3  volBlobAlbedo;
uniform float volBlobAbsorption;

out vec4 outColor;

// Interleaved gradient noise (Jimenez): a per-pixel offset for where along its
// step each sample lands. Without it the steps show as contour bands through
// the cloud; with it they dissolve into a fine grain the low-res upsample
// blurs away.
float volIGN(vec2 p) {
    return fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
}

float volDensity(vec3 p, float footprint, bool detail, inout vec3 glow) {
    if (volKind == VOL_KIND_BLOBS) {
        return blobDensity(p, footprint, detail, glow);
    }
    return cloudDensity(volCloud, p, footprint, detail);
}

bool volInterval(vec3 ro, vec3 rd, inout float t0, inout float t1) {
    if (volKind == VOL_KIND_BLOBS) {
        return fogShapeInterval(VOL_SHAPE_BOX, volProxyBasis, volProxyCenter, ro, rd, t0, t1);
    }
    return cloudInterval(volCloud, ro, rd, t0, t1);
}

float volHeight(vec3 p) {
    if (volKind == VOL_KIND_BLOBS) {
        return clamp((volProxyBasis * (p - volProxyCenter)).y * 0.5 + 0.5, 0.0, 1.0);
    }
    return clamp(cloudHeight(volCloud, p), 0.0, 1.0);
}

// volLightDepth is the optical depth between p and the edge of the region
// along the sun direction — how much of the cloud's own mass the sunlight
// crossed to get here.
float volLightDepth(vec3 p, float footprint) {
    float t0 = 0.0;
    float t1 = volLightReach;
    if (!volInterval(p, volSunDir, t0, t1)) {
        return 0.0;
    }
    int n = clamp(volLightSteps, 1, VOL_MAX_LIGHT_STEPS);
    float stepL = (t1 - t0) / float(n);
    float od = 0.0;
    vec3 ignored = vec3(0.0);
    for (int j = 0; j < VOL_MAX_LIGHT_STEPS; ++j) {
        if (j >= n) {
            break;
        }
        vec3 q = p + volSunDir * (t0 + stepL * (float(j) + 0.5));
        od += volDensity(q, max(footprint, stepL), false, ignored) * stepL;
    }
    return od;
}

void main() {
    vec2 uv = gl_FragCoord.xy / volTargetSize;
    vec4 farH = volInvProjView * vec4(uv * 2.0 - 1.0, 1.0, 1.0);
    vec3 rd = normalize(farH.xyz / farH.w - volCamPos);

    float t0 = 0.0;
    float t1 = (volKind == VOL_KIND_DECK) ? volDeckRange : 1e7;
    t1 = min(t1, textureLod(volSceneDist, uv, 0.0).r);
    if (!volInterval(volCamPos, rd, t0, t1)) {
        outColor = vec4(0.0);
        return;
    }

    int n = clamp(volSteps, 1, VOL_MAX_STEPS);
    float dt = (t1 - t0) / float(n);
    float jitter = volIGN(gl_FragCoord.xy);

    bool sun = dot(volSunColor, volSunColor) > 0.0;
    float phase = volPhase(dot(rd, volSunDir));
    vec3 albedo = (volKind == VOL_KIND_BLOBS) ? volBlobAlbedo : volCloud.albedo;
    float absorption = (volKind == VOL_KIND_BLOBS) ? volBlobAbsorption : volCloud.absorption;
    float ambientK = (volKind == VOL_KIND_BLOBS) ? 1.0 : volCloud.ambient;

    float T = 1.0;
    vec3 L = vec3(0.0);
    float wSum = 0.0;
    float tSum = 0.0;
    for (int i = 0; i < VOL_MAX_STEPS; ++i) {
        if (i >= n) {
            break;
        }
        float t = t0 + dt * (float(i) + jitter);
        vec3 p = volCamPos + rd * t;
        float footprint = t * volPixelFootprint;

        vec3 glow = vec3(0.0);
        float sigma = volDensity(p, footprint, true, glow);
        if (sigma <= 1e-6) {
            continue;
        }

        vec3 light = mix(volAmbientBottom, volAmbientTop, volHeight(p)) * ambientK;
        if (sun) {
            float od = volLightDepth(p, footprint) * absorption;
            // Direct sunlight through the cloud's own mass, plus a cheap stand-in
            // for light scattered more than once: a far weaker extinction with
            // no phase, which is what keeps the deep side of a thick cloud grey
            // rather than black.
            light += volSunColor * (phase * exp(-od) + 0.3 * exp(-od * 0.25));
        }
        light += volPointLights(p);

        vec3 S = sigma * albedo * light + glow;
        float Ts = exp(-sigma * dt);
        L += T * (S - S * Ts) / sigma;

        // Where along the ray the visible mass is, for the fog term below.
        float w = T * (1.0 - Ts);
        wSum += w;
        tSum += w * t;

        T *= Ts;
        if (T < 0.01) {
            break;
        }
    }

    float alpha = 1.0 - T;
    if (wSum > 1e-5) {
        float tMean = tSum / wSum;
        // Fog, at the depth of the mass rather than of the region's surface: a
        // deep bank of cloud seen through haze is hazed by how far away the
        // cloud IS. Applied to the unpremultiplied colour, then premultiplied
        // back, which is the same blend fog.glsl does to a surface.
        vec3 c = L / max(alpha, 1e-4);
        L = applyFog(c, volCamPos + rd * tMean) * alpha;
        if (volKind == VOL_KIND_DECK) {
            // The deck runs out at its range; fading it over the last stretch is
            // what turns that into a horizon rather than an edge.
            float fade = 1.0 - smoothstep(volDeckRange * 0.6, volDeckRange, tMean);
            L *= fade;
            alpha *= fade;
        }
    }
    outColor = vec4(L, alpha);
}
