// biome_light.glsl — shared lighting + shadow block for the biome passes.
//
// Injected after the #version line of biome_vert.glsl (scatter placement),
// biome_frag.glsl (rock/moss) and ground_frag.glsl (the conforming ground
// patch) — see biomeLightSnippet in biome_light.go.
//
// Every uniform name below is deliberately IDENTICAL to fragment.glsl's, so
// UploadLightsTo (light.go), UploadShadowUniformsTo and
// UploadPointShadowUniformsTo (shadow.go) drive these programs unchanged.
// There is no biome-specific light upload path to keep in sync with the main
// shader's — adding a light type to the scene shader is enough.
//
// What differs from fragment.glsl is the COST, tuned per call site by three
// defines the compile helper sets:
//
//   BIOME_LIGHT_PCF   0 = a single tap, 1 = 3x3, 2 = 5x5 (fragment.glsl's own
//                     filter). Foliage samples the cascade ONCE PER INSTANCE
//                     in the vertex stage, where a single tap is not merely
//                     cheaper but better: a blade narrower than a shadow texel,
//                     filtered per-fragment, swims as the camera moves. Rock
//                     and moss take 3x3 per-fragment; the ground patch takes
//                     the full 5x5 so its shadow edges match the structural
//                     floor it drapes over exactly.
//
//   BIOME_LIGHT_POINT_SHADOWS  gates the cubemap lookup (point_shadow.glsl, the
//                     world shader's own). Left undefined
//                     for the vertex stage: foliage takes point-light diffuse
//                     but not point-light shadows.
//
//   BIOME_LIGHT_CASCADE_BLEND  gates fragment.glsl's cascade cross-fade band.
//                     Off for the scatter: it costs a second full cascade
//                     lookup per sample to hide a resolution seam that a blade
//                     or a pebble is too small to show. ON for the ground
//                     patch, which is a floor-sized surface that INTERSECTS the
//                     structural floor (a Water lakebed dips below it, a dune
//                     rises through it) — the two are visible pixel against
//                     pixel there, so a shadow edge that steps at a cascade
//                     split on one and cross-fades on the other reads as two
//                     differently-lit floors, not as one softened seam.
//
// SHADOW_CASCADES, SHADOW_CASCADE_BLEND and SHADOW_FADE_FRACTION must match
// ShadowCascades, ShadowCascadeBlend and shadowFadeFraction in shadow.go,
// exactly as fragment.glsl's copies do.

// The lights, the cascades and the point shadows' parameters are the scene
// block's members, under fragment.glsl's names — it also defines
// SHADOW_CASCADES, MAX_DIR_LIGHTS and MAX_POINT_LIGHTS.
#include "scene_block.glsl"

// GGX specular (specular.glsl): biomeLightingSpec's highlight, fragment.glsl's
// own model.
#include "specular.glsl"

#define SHADOW_FADE_FRACTION  0.15

#ifndef BIOME_LIGHT_PCF
#define BIOME_LIGHT_PCF 0
#endif

// Width of the cross-fade band at the tail of a cascade, as a fraction of that
// cascade's own span. Must match ShadowCascadeBlend in shadow.go, exactly as
// fragment.glsl's copy does. Only read when BIOME_LIGHT_CASCADE_BLEND is set.
#define SHADOW_CASCADE_BLEND  0.25

// World->view matrix, used only to recover the view-axis distance that picks a
// cascade. Matches vertex.glsl's fragViewDepth (-(camera * worldPos).z) so the
// biome passes land in the same cascade as the geometry they sit on.
uniform mat4 camera;

// Comparison sampler, matching fragment.glsl's — see EnsureCascadeArray in
// shadow.go for why the cascade array is sampled this way.
uniform sampler2DArrayShadow shadowMapArray;

#ifdef BIOME_LIGHT_POINT_SHADOWS
#include "point_shadow.glsl"
#endif

// Distance along the view axis, for cascade selection.
float biomeViewDepth(vec3 worldPos) {
    return -(camera * vec4(worldPos, 1.0)).z;
}

// One cascade layer, filtered at BIOME_LIGHT_PCF. Returns occlusion in 0..1, or
// -1.0 when worldPos falls outside this cascade's box — the caller then falls
// through to a wider cascade rather than reporting the point as lit.
//
// textureGrad with zero gradients (not plain texture) because this block also
// compiles into a VERTEX stage, where there are no implicit derivatives to pick
// a level from. The cascade array has no mips, so an explicit lod 0 is what the
// fragment path would sample anyway — and textureGrad is the only explicit-lod
// form GLSL defines for a sampler2DArrayShadow, textureLod having no such
// overload.
float biomeSampleCascade(int matIdx, int layer, vec3 worldPos, vec3 normal, vec3 lightDir, float spread) {
    vec4 posLS = cascadeMatrix[matIdx] * vec4(worldPos, 1.0);
    vec3 projCoords = posLS.xyz / posLS.w;
    projCoords = projCoords * 0.5 + 0.5;

    if (projCoords.x < 0.0 || projCoords.x > 1.0 ||
        projCoords.y < 0.0 || projCoords.y > 1.0 ||
        projCoords.z > 1.0) {
        return -1.0;
    }

    // Same quadratic slope-scaled bias as fragment.glsl's sampleCascade.
    float base = cascadeBias[matIdx];
    float cosTheta = abs(dot(normal, lightDir));
    float bias = (base * (1.0 - cosTheta) * (1.0 - cosTheta) + base * 0.05) * spread;
    float currentDepth = projCoords.z;

    vec2 texelSize = 1.0 / vec2(textureSize(shadowMapArray, 0).xy);
    float ref = currentDepth - bias;

#if BIOME_LIGHT_PCF == 0
    float lit = textureGrad(shadowMapArray,
        vec4(projCoords.xy, float(layer), ref), vec2(0.0), vec2(0.0));
    return 1.0 - lit;
#else
    float lit = 0.0;
    float taps = 0.0;
    for (int x = -BIOME_LIGHT_PCF; x <= BIOME_LIGHT_PCF; x++) {
        for (int y = -BIOME_LIGHT_PCF; y <= BIOME_LIGHT_PCF; y++) {
            lit += textureGrad(shadowMapArray,
                vec4(projCoords.xy + vec2(x, y) * texelSize * spread, float(layer), ref),
                vec2(0.0), vec2(0.0));
            taps += 1.0;
        }
    }
    return 1.0 - lit / taps;
#endif
}

// Cascaded directional shadow lookup: tightest cascade covering the point,
// cross-faded into the next one at the split if BIOME_LIGHT_CASCADE_BLEND is
// set (see the header note), faded out as the shadow range runs out.
float biomeDirShadow(int lightIdx, vec3 worldPos, float viewDepth, vec3 normal, vec3 lightDir) {
    // The sun's virtual shadow map, when the frame drew one for this light
    // (vsm.glsl, render/vsm.go), faded out at the end of the shadow distance
    // as the cascades are below.
    if (vsmShadows(lightIdx)) {
        float vsmMaxDist = cascadeSplit[SHADOW_CASCADES - 1];
        float vsmFadeBegin = vsmMaxDist * (1.0 - SHADOW_FADE_FRACTION);
#ifdef BIOME_LIGHT_VSM_ONE_TAP
        float vs = vsmShadow1(worldPos, normal, lightDir);
#else
        float vs = vsmShadow(worldPos, normal, lightDir);
#endif
        return vs *
            (1.0 - clamp((viewDepth - vsmFadeBegin) / max(vsmMaxDist - vsmFadeBegin, 1e-4), 0.0, 1.0));
    }
    int layerBase = dirLightShadowLayer[lightIdx];
    if (layerBase < 0) {
        return 0.0;
    }

    int c = SHADOW_CASCADES;
    for (int i = 0; i < SHADOW_CASCADES; i++) {
        if (viewDepth <= cascadeSplit[i]) {
            c = i;
            break;
        }
    }
    if (c >= SHADOW_CASCADES) {
        return 0.0; // beyond ShadowMaxDistance — unshadowed
    }

    int matBase = lightIdx * SHADOW_CASCADES;
    float prev = (c == 0) ? 0.0 : cascadeSplit[c - 1];
    float span = max(cascadeSplit[c] - prev, 1e-4);
    float t = (viewDepth - prev) / span;
    // fragment.glsl's cascadeSpread, so a ground patch's shadow edge softens
    // with distance exactly as the floor beside it does. Moot unfiltered.
    float spread = 1.0;
#if BIOME_LIGHT_PCF != 0
    if (c + 1 < SHADOW_CASCADES) {
        float own = max(cascadeTexel[matBase + c], 1e-5);
        spread = mix(1.0, max(cascadeTexel[matBase + c + 1] / own, 1.0), smoothstep(0.25, 1.0, t));
    }
#endif
    float shadow = biomeSampleCascade(matBase + c, layerBase + c, worldPos, normal, lightDir, spread);
    bool fellThrough = false;
    if (shadow < 0.0) {
        // A point at the corner of a slice can sit just outside its own
        // cascade's box; the next one out always contains it.
        if (c + 1 < SHADOW_CASCADES) {
            c += 1;
            shadow = biomeSampleCascade(matBase + c, layerBase + c, worldPos, normal, lightDir, 1.0);
        }
        if (shadow < 0.0) {
            return 0.0;
        }
        fellThrough = true;
    }

#ifdef BIOME_LIGHT_CASCADE_BLEND
    // Cross-fade over the tail of this cascade so the jump in texel density
    // reads as a softening rather than a seam — fragment.glsl's calcDirShadow
    // does exactly this, and the ground patch has to agree with it where the
    // two surfaces meet.
    if (!fellThrough && c + 1 < SHADOW_CASCADES && t > 1.0 - SHADOW_CASCADE_BLEND) {
        float next = biomeSampleCascade(matBase + c + 1, layerBase + c + 1, worldPos, normal, lightDir, 1.0);
        if (next >= 0.0) {
            shadow = mix(shadow, next, smoothstep(1.0 - SHADOW_CASCADE_BLEND, 1.0, t));
        }
    }
#endif

    float maxDist = cascadeSplit[SHADOW_CASCADES - 1];
    float fadeBegin = maxDist * (1.0 - SHADOW_FADE_FRACTION);
    shadow *= 1.0 - clamp((viewDepth - fadeBegin) / max(maxDist - fadeBegin, 1e-4), 0.0, 1.0);

    return shadow;
}

// Full lighting multiplier at worldPos — global ambient, every directional
// light (shadowed), every point light in range. Mirrors fragment.glsl's main()
// so a blade, a pebble and the floor under them agree about how lit they are.
// receiveShadow lets a caller take the lighting without the cascade lookup (the
// BiomeShadows.Receive toggle, so the cost can be measured against zero).
// twoSided is for the paper-thin things — a grass blade, a sprite — whose
// normal says little about which side the light is on: they take it from
// either side, as light through a leaf. A solid surface (a ground patch, a
// rock) takes light_model.glsl's one-sided model, as the floor does.
float biomeDiffuse(float ndl, bool twoSided) {
    return lightWrapTerm(twoSided ? abs(ndl) : ndl);
}

// biomeLightingSpec is biomeLighting with each light's specular reflection, under
// the same shadow, added to spec (specular.glsl). A two-sided surface takes it
// only from the light on the side the normal faces.
// How much of a light a thin, two-sided surface (a leaf, a blade) passes to
// the eye looking toward the light through it, at its strongest.
#define BIOME_TRANSLUCENCY 0.35

// biomeAmbientRef is the ambient the last biomeLightingSpec took, for the
// environment's reflection (pbrEnvSpecular) to be scaled by.
vec3 biomeAmbientRef = vec3(0.0);

vec3 biomeLightingSpec(vec3 worldPos, vec3 normal, float viewDepth, bool receiveShadow, bool twoSided,
                       PBRSurface ps, inout vec3 spec) {
    // The last frame's screen-space occlusion (specular.glsl's ssaoAt): on
    // the ambient here, on the sun below.
    vec2 occ = ssaoAt(worldPos);
    vec3 lighting = ambientLight(globalLightColor * globalLightBrightness, worldPos, normal) * occ.x;
    biomeAmbientRef = lighting;

    for (int i = 0; i < numDirLights; i++) {
        vec3 ldir = -normalize(dirLightDir[i]);
        float diff = biomeDiffuse(dot(normal, ldir), twoSided);
        vec3 radiance = dirLightColor[i] * dirLightBrightness[i];
        float vis = 1.0;

        if (receiveShadow && dirLightCastShadows[i]) {
            float shadow = biomeDirShadow(i, worldPos, viewDepth, normal, ldir);
            vis = 1.0 - shadow * dirLightShadowStrength[i];
        }
        // Clouds shade the grass under them as they shade the floor —
        // cloud_shadow.glsl, spliced in ahead of this file by biomeLightSnippet.
        if (receiveShadow) {
            vis *= cloudShadowFactor(i, worldPos) * ssaoContact(i, occ);
        }
        lighting += radiance * diff * vis;
        spec += radiance * vis * pbrSpecular(normal, ldir, ps);
        // Light through a leaf or a blade (docs/lighting/PLAN.md, phase 6):
        // seen against the sun, a thin thing glows with what it lets
        // through, tinted by itself (the albedo the caller multiplies in).
        if (twoSided) {
            vec3 V = normalize(fogCamPos - worldPos);
            float through = max(dot(-V, ldir), 0.0);
            through *= through;
            through *= through;
            lighting += radiance * vis * (BIOME_TRANSLUCENCY * through);
        }
    }

    for (int i = 0; i < numPointLights; i++) {
        vec3  toLight = pointLightPos[i] - worldPos;
        float dist    = length(toLight);
        if (dist < pointLightRadius[i]) {
            float t           = 1.0 - (dist / pointLightRadius[i]);
            float attenuation = t * t;
            vec3  ldir        = toLight / max(dist, 1e-5);
            float diff        = biomeDiffuse(dot(normal, ldir), twoSided);
            vec3 radiance = pointLightColor[i] * pointLightBrightness[i] * attenuation;
            float vis = 1.0;

#ifdef BIOME_LIGHT_POINT_SHADOWS
            if (receiveShadow && pointLightCastShadows[i]) {
                float shadow = pointShadow(i, worldPos); // point_shadow.glsl
                vis = 1.0 - shadow * pointLightShadowStrength[i];
            }
#endif
            lighting += radiance * diff * vis;
            spec += radiance * vis * pbrSpecular(normal, ldir, ps);
        }
    }

    // A painted level's light ramp (light_model.glsl); untouched without one.
    return stylizeLight(lighting);
}

// biomeLighting is the diffuse alone, for the passes that take no highlight
// (the vertex-lit blades and cards).
vec3 biomeLighting(vec3 worldPos, vec3 normal, float viewDepth, bool receiveShadow, bool twoSided) {
    PBRSurface ps;
    ps.on = 0.0;
    vec3 spec = vec3(0.0);
    return biomeLightingSpec(worldPos, normal, viewDepth, receiveShadow, twoSided, ps, spec);
}
