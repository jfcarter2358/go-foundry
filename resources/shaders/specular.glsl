// specular.glsl — the view-dependent half of the lighting model: GGX
// microfacet specular for the direct lights (docs/lighting/PLAN.md, phase 1).
//
// light_model.glsl is the diffuse half, and the half the level compiler bakes
// (world.GlobalLight.LightWrap/AmbientAt). This half depends on where the eye
// is, so it is never baked: every pass that lights a surface adds it live, on
// top of the diffuse light, for every light — a compiled surface's static
// lights included, whose diffuse arrived from the bake.
//
// Units. The engine's diffuse is albedo × light × N·L, with no 1/π — a light's
// colour × brightness is the irradiance it delivers divided by π. So the
// specular BRDF here is the textbook one multiplied by π, which is the same as
// leaving the π out of the GGX distribution (pbrSpecular).
//
// Included (#include, gfx/shader_include.go) by fragment.glsl and
// biome_light.glsl — the two shading paths — and by nothing else: the GI
// compute programs splice light_model.glsl but never this, so its samplers
// never reach them.
//
// specularOn (the scene block, the player's `specular` setting) is 0 in a
// block nothing wrote, and then every function here answers zero: a program or
// a test that has never heard of specular shades exactly as before.

#ifndef SPECULAR_GLSL
#define SPECULAR_GLSL

#include "scene_block.glsl"

// A surface with no `pbr` material effect: a rough dielectric, whose highlight
// is a soft sheen a few percent of the light at its peak. Must match
// material.DefaultPBRRoughness / DefaultPBRSpecular.
#define PBR_DEFAULT_ROUGHNESS 0.7
#define PBR_DEFAULT_SPECULAR  0.5
// The smoothest a surface is shaded: below this a GGX lobe is narrower than a
// pixel's footprint of the normal, and a highlight sparkles from frame to
// frame rather than shining.
#define PBR_MIN_ROUGHNESS 0.06

// PBRSurface is what the specular terms need to know about the surface being
// shaded, worked out once a fragment (pbrSurface).
struct PBRSurface {
    vec3  v;     // unit, from the surface toward the eye
    float NoV;   // n · v, clamped clear of 0
    vec3  F0;    // reflectance at normal incidence
    float rough; // perceptual roughness, PBR_MIN_ROUGHNESS..1
    float a2;    // GGX alpha squared (alpha = rough²)
    float on;    // 0: no specular at all (specularOn off, or the caller's say)
};

// pbrSurface sets up a fragment's specular from its position and normal, its
// roughness, how metallic it is and its dielectric `specular` level (0.5 is
// the 4% reflectance of most non-metals), and its albedo, which a metal's
// reflectance is.
PBRSurface pbrSurface(vec3 p, vec3 n, float rough, float metallic, float specular, vec3 albedo) {
    PBRSurface s;
    vec3 toEye = fogCamPos - p;
    float d = length(toEye);
    s.v = d > 1e-5 ? toEye / d : n;
    s.NoV = clamp(dot(n, s.v), 1e-4, 1.0);
    vec3 dielectric = vec3(0.08 * clamp(specular, 0.0, 1.0));
    s.F0 = mix(dielectric, albedo, clamp(metallic, 0.0, 1.0));
    s.rough = clamp(rough, PBR_MIN_ROUGHNESS, 1.0);
    float a = s.rough * s.rough;
    s.a2 = a * a;
    // FOLDED BY TEXT (render/nanite_vis_variants.go visFrameSpecs): keep this expression spelled exactly.
    s.on = specularOn != 0 ? 1.0 : 0.0;
    return s;
}

// pbrDiffuseWeight is what a surface's diffuse light is multiplied by: a metal
// has none (its colour is all in the reflection), a dielectric all of it.
float pbrDiffuseWeight(float metallic) {
    return 1.0 - clamp(metallic, 0.0, 1.0);
}

float pbrFresnelPow5(float x) {
    float y = 1.0 - x;
    float y2 = y * y;
    return y2 * y2 * y;
}

vec3 pbrFresnel(vec3 F0, float cosTheta) {
    return F0 + (1.0 - F0) * pbrFresnelPow5(clamp(cosTheta, 0.0, 1.0));
}

// pbrSpecular is light from unit direction l's specular reflection toward the
// eye, per unit of the light's colour × brightness, already × N·L: GGX
// distribution (without its 1/π — see Units above), height-correlated Smith
// visibility, Schlick Fresnel.
vec3 pbrSpecular(vec3 n, vec3 l, PBRSurface s) {
    float NoL = dot(n, l);
    if (s.on == 0.0 || NoL <= 0.0) {
        return vec3(0.0);
    }
    vec3 h = normalize(l + s.v);
    float NoH = clamp(dot(n, h), 0.0, 1.0);
    float VoH = clamp(dot(s.v, h), 0.0, 1.0);
    float d = (NoH * s.a2 - NoH) * NoH + 1.0;
    float D = s.a2 / max(d * d, 1e-7);
    float gv = NoL * sqrt(s.NoV * s.NoV * (1.0 - s.a2) + s.a2);
    float gw = s.NoV * sqrt(NoL * NoL * (1.0 - s.a2) + s.a2);
    float V = 0.5 / max(gv + gw, 1e-5);
    return pbrFresnel(s.F0, VoH) * (D * V * NoL);
}

// ---------- screen-space occlusion (render/ao.go; PLAN.md phases 3 and 6) ----------
//
// The last frame's ambient occlusion and the sun's contact shadow, at half
// resolution: r the AO, g the contact visibility, b the view-space distance
// of the surface the texel saw. A fragment finds its texel through the
// view-projection the texture was made with, and takes it only if that
// distance is its own; anywhere else — off screen last frame, uncovered since,
// no texture at all — it reads (1, 1): unoccluded.
uniform sampler2D ssaoTex;

vec2 ssaoAt(vec3 p) {
    // FOLDED BY TEXT (render/nanite_vis_variants.go visFrameSpecs): keep this expression spelled exactly.
    if (ssaoOn == 0) {
        return vec2(1.0);
    }
    vec4 c = ssaoPrevViewProj * vec4(p, 1.0);
    if (c.w <= 0.05) {
        return vec2(1.0);
    }
    vec2 uv = c.xy / c.w * 0.5 + 0.5;
    if (any(lessThan(uv, vec2(0.0))) || any(greaterThan(uv, vec2(1.0)))) {
        return vec2(1.0);
    }
    vec4 t = textureLod(ssaoTex, uv, 0.0);
    if (abs(t.b - c.w) > 0.04 * c.w + 0.05) {
        return vec2(1.0);
    }
    return vec2(mix(1.0, t.r, ssaoStrength), t.g);
}

// ssaoContact is the sun's contact visibility for directional light i at p, 1
// for any other light (contactShadowsOn is the sun's index + 1).
float ssaoContact(int i, vec2 occ) {
    // FOLDED BY TEXT (render/nanite_vis_variants.go visFrameSpecs): keep this expression spelled exactly.
    return i + 1 == contactShadowsOn ? occ.y : 1.0;
}

// pbrSpecularOcclusion is how much of the environment's reflection AO lets
// through, by how it meets the view and how rough it is (Lagarde and de
// Rousiers, "Moving Frostbite to PBR").
float pbrSpecularOcclusion(float NoV, float ao, float rough) {
    return clamp(pow(NoV + ao, exp2(-16.0 * rough - 1.0)) - 1.0 + ao, 0.0, 1.0);
}

// ---------- image-based specular (render/envmap.go; PLAN.md phase 2) ----------
//
// What a surface reflects of everything that is not a light: the environment
// array's sky cube (cube 0), or the reflection volume p stands in, each
// GGX-prefiltered so mip roughness × envMaxMip is the lobe. Light_model.glsl's
// ambientLight and giIrradiance must be spliced ahead of this file — they are,
// in both the passes that include it.

// envCubes: the environment array (render.EnvTexUnit). A 1×1 black stand-in
// while envOn is 0.
uniform samplerCubeArray envCubes;

float pbrLum(vec3 c) { return dot(c, vec3(0.2126, 0.7152, 0.0722)); }

// pbrEnvBRDF is the split sum's second half, Karis's analytic fit ("Physically
// Based Shading on Mobile"): the scale and bias on F0 of the GGX lobe's
// integral, by n·v and roughness.
vec2 pbrEnvBRDF(float NoV, float rough) {
    const vec4 c0 = vec4(-1.0, -0.0275, -0.572, 0.022);
    const vec4 c1 = vec4(1.0, 0.0425, 1.04, -0.04);
    vec4 r = rough * c0 + c1;
    float a004 = min(r.x * r.x, exp2(-9.28 * NoV)) * r.x + r.y;
    return vec2(-1.04, 1.04) * a004 + r.zw;
}

// ---------- screen-space reflections (envOn 2: the high reflection tier) ----------
//
// The last frame's finished picture (TAA's history, scene units, before the
// tone curve), marched against the last frame's depth — the distances the
// occlusion texture carries (ssaoTex.b) — along the reflected ray, both
// through ssaoPrevViewProj: no G-buffer, so the forward pass traces its own.
// What it finds is what that frame drew there, its own reflections included.
uniform sampler2D ssrColor;

#define SSR_STEPS 16
#define SSR_MAX_DIST 16.0
#define SSR_MAX_ROUGH 0.5

// ssrTrace is the reflection of p along unit R from the last frame's picture:
// rgb the colour, a how far to trust it (0: nothing found).
vec4 ssrTrace(vec3 p, vec3 R, float rough) {
    if (envOn != 2 || ssaoOn == 0 || rough >= SSR_MAX_ROUGH) {
        return vec4(0.0);
    }
    // A jitter of the march per point, so the steps' banding becomes noise
    // TAA averages away.
    float jit = fract(sin(dot(p, vec3(12.9898, 78.233, 37.719))) * 43758.5453);
    float prevT = 0.0;
    for (int i = 1; i <= SSR_STEPS; i++) {
        float f = (float(i) - jit * 0.9) / float(SSR_STEPS);
        float t = SSR_MAX_DIST * f * f;
        vec3 q = p + R * t;
        vec4 c = ssaoPrevViewProj * vec4(q, 1.0);
        if (c.w <= 0.05) {
            break;
        }
        vec2 uv = c.xy / c.w * 0.5 + 0.5;
        if (any(lessThan(uv, vec2(0.0))) || any(greaterThan(uv, vec2(1.0)))) {
            break;
        }
        float d = textureLod(ssaoTex, uv, 0.0).b;
        float behind = c.w - d;
        if (d > 0.0 && behind > 0.0 && behind < max(0.25, t * 0.15)) {
            // Between the last step in front and this one behind: halve in.
            float lo = prevT, hi = t;
            for (int k = 0; k < 4; k++) {
                float m = 0.5 * (lo + hi);
                vec4 cm = ssaoPrevViewProj * vec4(p + R * m, 1.0);
                vec2 um = cm.xy / cm.w * 0.5 + 0.5;
                if (cm.w - textureLod(ssaoTex, um, 0.0).b > 0.0) {
                    hi = m;
                    uv = um;
                } else {
                    lo = m;
                }
            }
            vec2 edge = min(uv, 1.0 - uv);
            float conf = smoothstep(0.0, 0.08, min(edge.x, edge.y))
                * (1.0 - f * f)
                * (1.0 - smoothstep(SSR_MAX_ROUGH * 0.5, SSR_MAX_ROUGH, rough));
            return vec4(textureLod(ssrColor, uv, 0.0).rgb, conf);
        }
        prevT = t;
    }
    return vec4(0.0);
}

// pbrEnvSpecular is the environment's specular reflection at p: the light it
// sends back toward the eye, ready to add to the direct lights' (spec in
// fragment.glsl). ambientRef is the ambient light the surface itself took —
// what tells a cave from a field when there are no probes to ask — and
// specOcc how much of the reflection its cavity and AO let through.
//
// The sky's cube knows nothing of what stands round p, so its reflection is
// scaled to the light that really arrives: with GI, the probes' light at the
// surface over the sky cube's own diffuse there (and, as the surface
// roughens, the probes' light outright); without, the share of the open sky's
// ambient the surface kept. A reflection volume's cube was
// captured in place and is taken as it is, through its box.
vec3 pbrEnvSpecular(PBRSurface s, vec3 p, vec3 n, vec3 ambientRef, float specOcc) {
    // FOLDED BY TEXT (render/nanite_vis_variants.go visFrameSpecs): keep this expression spelled exactly.
    if (s.on == 0.0 || envOn == 0) {
        return vec3(0.0);
    }
    vec3 R = reflect(-s.v, n);
    // The lobe's peak leans toward the normal as it roughens (Frostbite).
    float a = s.rough * s.rough;
    R = normalize(mix(n, R, (1.0 - a) * (sqrt(1.0 - a) + a)));
    float mip = s.rough * envMaxMip;

    vec3 sky = textureLod(envCubes, vec4(R, 0.0), mip).rgb;
    if (envGINormalize != 0) {
        // The probes' light the surface already took (ambientRef, on its own
        // normal) over what the sky cube alone would give it there: no second
        // probe lookup along R — that was a whole GI sample a pixel more, and
        // the registers to hold it in every material program.
        vec3 skyIrr = textureLod(envCubes, vec4(n, 0.0), envMaxMip).rgb;
        float k = clamp(pbrLum(ambientRef) / max(pbrLum(skyIrr), 1e-4), 0.0, 2.0);
        sky = mix(sky * k, ambientRef, a);
    } else {
        vec3 open = ambientAt(globalLightColor * globalLightBrightness, n);
        sky *= clamp(pbrLum(ambientRef) / max(pbrLum(open), 1e-4), 0.0, 1.0);
    }

    vec3 vol = vec3(0.0);
    float wsum = 0.0;
    for (int i = 0; i < MAX_REFL_VOLUMES; i++) {
        if (i >= numReflVolumes || wsum >= 1.0) {
            break;
        }
        vec3 lo = reflVolMin[i].xyz;
        vec3 hi = reflVolMax[i].xyz;
        vec3 inLo = p - lo, inHi = hi - p;
        float depth = min(min(min(inLo.x, inLo.y), inLo.z), min(min(inHi.x, inHi.y), inHi.z));
        if (depth <= 0.0) {
            continue;
        }
        float fade = reflVolMin[i].w;
        float w = fade > 0.0 ? clamp(depth / fade, 0.0, 1.0) : 1.0;
        w = min(w, 1.0 - wsum);
        // Box projection: where R leaves the box, seen from the capture point.
        vec3 tHi = inHi / R;
        vec3 tLo = -inLo / R;
        vec3 tFar = max(tHi, tLo);
        float t = min(min(tFar.x, tFar.y), tFar.z);
        vec3 Rb = normalize(p + R * t - reflVolCenter[i].xyz);
        vol += w * reflVolMax[i].w * textureLod(envCubes, vec4(Rb, reflVolCenter[i].w), mip).rgb;
        wsum += w;
    }
    vec3 env = vol + sky * (1.0 - wsum);
    // What the screen shows along R, where it shows it, over all of that.
    vec4 ssr = ssrTrace(p, R, s.rough);
    env = mix(env, ssr.rgb, ssr.a);

    vec2 ab = pbrEnvBRDF(s.NoV, s.rough);
    return env * (s.F0 * ab.x + ab.y) * (specOcc * envIntensity);
}

#endif
