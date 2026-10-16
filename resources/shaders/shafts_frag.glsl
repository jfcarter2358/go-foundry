#version 430

// shafts_frag.glsl — light shafts: the sun scattered by dusty air between the
// eye and the first surface, SHADOWED (render/shafts.go; world/fog_shafts.go;
// docs/adventure/PHASE_B.md).
//
// fog.glsl's atmosphere is a closed form and is unshadowed — haze in a
// canyon's shade glows as brightly as haze in the sun. This is the part of the
// air that is marched instead: shSteps samples from the eye to the scene
// (the volume pass's distances, volume_depth_frag.glsl), at most shDistance,
// each offset by a per-pixel jitter that turns every frame so TAA's history
// averages the steps together. Each sample is lit by
//
//   - the sun, through the shadow map — the virtual shadow map or the
//     cascades, whichever the frame drew (biome_light.glsl's biomeDirShadow,
//     one tap) — and the clouds' shadow, with a Henyey-Greenstein phase
//     relative to isotropic (1 on average), so the beams glow looking toward
//     the sun;
//   - a share of the sky's ambient (shAmbient), cut by the ambient volumes as
//     a surface there would be (light_model.glsl), so shaded dust in a cave is
//     dark and in the open grey, not black smoke;
//
// and the medium is shDensity at shBase thinning by e every 1/shFalloff
// metres up, plus each ambient volume's dust inside it. The step integral is
// the analytic one volume_frag.glsl uses (Hillaire): over a step of constant
// extinction the light added is T * S * (1 - exp(-sigma dt)) / sigma, with an
// albedo of 1 so S / sigma is the light itself.
//
// Output is premultiplied (light, 1 - transmittance) into the volume target,
// blended ONE, ONE_MINUS_SRC_ALPHA, first — the clouds and fireballs march
// over it — and composited over the frame by the depth-aware upsample.
//
// biome_light.glsl (with cloud_shadow.glsl, vsm.glsl, light_model.glsl and
// fog.glsl ahead of and after it) is spliced in after the #version line.

uniform mat4      shInvProjView;
uniform vec3      shCamPos;
uniform vec2      shTargetSize;
uniform sampler2D shSceneDist;
uniform int       shSteps;
uniform float     shJitterTurn;
uniform float     shDistance;
uniform float     shDensity;
uniform float     shBase;
uniform float     shFalloff;
uniform float     shG;
uniform float     shStrength;
uniform float     shAmbient;
uniform vec3      shColor;
// The directional light the shafts are lit by, or -1.
uniform int       shSun;

out vec4 outColor;

#define SH_MAX_STEPS 64

float shIGN(vec2 p) {
    return fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
}

// shPhase is Henyey-Greenstein over isotropic: 1 on average over the sphere.
float shPhase(float mu) {
    float g = clamp(shG, -0.95, 0.95);
    float den = max(1.0 + g * g - 2.0 * g * mu, 1e-4);
    return (1.0 - g * g) / (den * sqrt(den));
}

// shDensityAt is the medium's extinction per metre at p.
float shDensityAt(vec3 p) {
    float d = shDensity;
    if (shFalloff > 0.0) {
        d *= exp(-clamp(shFalloff * (p.y - shBase), -30.0, 80.0));
    }
    // Only the volumes p's screen tile can see (amb_tiles.glsl).
    uint seen = numAmbVolumes > 0 ? ambVolMaskAt(p) : 0u;
    for (int i = 0; i < MAX_AMBIENT_VOLUMES; ++i) {
        if (i >= numAmbVolumes) {
            break;
        }
        if ((seen & (1u << uint(i))) == 0u) {
            continue;
        }
        if (ambVolParams[i].w > 0.0) {
            d += ambVolParams[i].w * ambVolWeight(i, p);
        }
    }
    return d;
}

void main() {
    vec2 uv = gl_FragCoord.xy / shTargetSize;
    vec4 farH = shInvProjView * vec4(uv * 2.0 - 1.0, 1.0, 1.0);
    vec3 rd = normalize(farH.xyz / farH.w - shCamPos);

    float tEnd = min(textureLod(shSceneDist, uv, 0.0).r, shDistance);
    int n = clamp(shSteps, 1, SH_MAX_STEPS);
    float dt = tEnd / float(n);
    float jitter = fract(shIGN(gl_FragCoord.xy) + shJitterTurn);

    bool sun = shSun >= 0 && shSun < numDirLights;
    vec3 toSun = vec3(0.0, 1.0, 0.0);
    vec3 sunLight = vec3(0.0);
    bool casts = false;
    float strength = 0.0;
    if (sun) {
        toSun = -normalize(dirLightDir[shSun]);
        sunLight = dirLightColor[shSun] * dirLightBrightness[shSun] * shStrength * shPhase(dot(rd, toSun));
        casts = dirLightCastShadows[shSun];
        strength = dirLightShadowStrength[shSun];
    }
    vec3 amb = globalLightColor * globalLightBrightness * shAmbient;

    float T = 1.0;
    vec3 L = vec3(0.0);
    for (int i = 0; i < SH_MAX_STEPS; ++i) {
        if (i >= n) {
            break;
        }
        float t = dt * (float(i) + jitter);
        vec3 p = shCamPos + rd * t;
        float sigma = shDensityAt(p);
        if (sigma <= 1e-6) {
            continue;
        }
        vec3 light = amb * ambientVolumeFactor(p, vec3(0.0));
        if (sun) {
            float lit = cloudShadowFactor(shSun, p);
            if (casts && lit > 0.0) {
                // The normal is the sun's own direction: no slope bias, and
                // the normal offset pushes the sample toward the light.
                lit *= 1.0 - strength * biomeDirShadow(shSun, p, biomeViewDepth(p), toSun, toSun);
            }
            light += sunLight * lit;
        }
        float Ts = exp(-sigma * dt);
        L += T * light * (1.0 - Ts);
        T *= Ts;
        if (T < 0.01) {
            break;
        }
    }
    outColor = vec4(L * shColor, 1.0 - T);
}
