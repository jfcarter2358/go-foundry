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
//   BIOME_LIGHT_POINT_SHADOWS  gates the 20-tap cubemap lookup. Left undefined
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

#define SHADOW_CASCADES       3
#define SHADOW_FADE_FRACTION  0.15

#define MAX_DIR_LIGHTS   4
#define MAX_POINT_LIGHTS 8

#ifndef BIOME_LIGHT_PCF
#define BIOME_LIGHT_PCF 0
#endif

// Width of the cross-fade band at the tail of a cascade, as a fraction of that
// cascade's own span. Must match ShadowCascadeBlend in shadow.go, exactly as
// fragment.glsl's copy does. Only read when BIOME_LIGHT_CASCADE_BLEND is set.
#define SHADOW_CASCADE_BLEND  0.12

// World->view matrix, used only to recover the view-axis distance that picks a
// cascade. Matches vertex.glsl's fragViewDepth (-(camera * worldPos).z) so the
// biome passes land in the same cascade as the geometry they sit on.
uniform mat4 camera;

uniform vec3  globalLightColor;
uniform float globalLightBrightness;

uniform int   numDirLights;
uniform vec3  dirLightDir[MAX_DIR_LIGHTS];
uniform vec3  dirLightColor[MAX_DIR_LIGHTS];
uniform float dirLightBrightness[MAX_DIR_LIGHTS];

// Comparison sampler, matching fragment.glsl's — see EnsureCascadeArray in
// shadow.go for why the cascade array is sampled this way.
uniform sampler2DArrayShadow shadowMapArray;
uniform mat4  cascadeMatrix[MAX_DIR_LIGHTS * SHADOW_CASCADES];
uniform float cascadeBias[MAX_DIR_LIGHTS * SHADOW_CASCADES];
uniform float cascadeSplit[SHADOW_CASCADES];
uniform int   dirLightShadowLayer[MAX_DIR_LIGHTS];
uniform bool  dirLightCastShadows[MAX_DIR_LIGHTS];
uniform float dirLightShadowStrength[MAX_DIR_LIGHTS];

uniform int   numPointLights;
uniform vec3  pointLightPos[MAX_POINT_LIGHTS];
uniform vec3  pointLightColor[MAX_POINT_LIGHTS];
uniform float pointLightBrightness[MAX_POINT_LIGHTS];
uniform float pointLightRadius[MAX_POINT_LIGHTS];

#ifdef BIOME_LIGHT_POINT_SHADOWS
uniform samplerCube pointShadowMap[MAX_POINT_LIGHTS];
uniform bool        pointLightCastShadows[MAX_POINT_LIGHTS];
uniform float       pointLightShadowStrength[MAX_POINT_LIGHTS];
uniform float       pointLightFarPlane[MAX_POINT_LIGHTS];
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
float biomeSampleCascade(int matIdx, int layer, vec3 worldPos, vec3 normal, vec3 lightDir) {
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
    float bias = base * (1.0 - cosTheta) * (1.0 - cosTheta) + base * 0.05;
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
                vec4(projCoords.xy + vec2(x, y) * texelSize, float(layer), ref),
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
    float shadow = biomeSampleCascade(matBase + c, layerBase + c, worldPos, normal, lightDir);
    if (shadow < 0.0) {
        // A point at the corner of a slice can sit just outside its own
        // cascade's box; the next one out always contains it.
        if (c + 1 < SHADOW_CASCADES) {
            c += 1;
            shadow = biomeSampleCascade(matBase + c, layerBase + c, worldPos, normal, lightDir);
        }
        if (shadow < 0.0) {
            return 0.0;
        }
    }

#ifdef BIOME_LIGHT_CASCADE_BLEND
    // Cross-fade over the tail of this cascade so the jump in texel density
    // reads as a softening rather than a seam — fragment.glsl's calcDirShadow
    // does exactly this, and the ground patch has to agree with it where the
    // two surfaces meet.
    float prev = (c == 0) ? 0.0 : cascadeSplit[c - 1];
    float span = max(cascadeSplit[c] - prev, 1e-4);
    float t = (viewDepth - prev) / span;
    if (c + 1 < SHADOW_CASCADES && t > 1.0 - SHADOW_CASCADE_BLEND) {
        float next = biomeSampleCascade(matBase + c + 1, layerBase + c + 1, worldPos, normal, lightDir);
        if (next >= 0.0) {
            shadow = mix(shadow, next, (t - (1.0 - SHADOW_CASCADE_BLEND)) / SHADOW_CASCADE_BLEND);
        }
    }
#endif

    float maxDist = cascadeSplit[SHADOW_CASCADES - 1];
    float fadeBegin = maxDist * (1.0 - SHADOW_FADE_FRACTION);
    shadow *= 1.0 - clamp((viewDepth - fadeBegin) / max(maxDist - fadeBegin, 1e-4), 0.0, 1.0);

    return shadow;
}

#ifdef BIOME_LIGHT_POINT_SHADOWS
const int BIOME_POINT_SAMPLES = 20;
const vec3 biomePointOffsets[20] = vec3[](
    vec3( 1,  1,  1), vec3( 1, -1,  1), vec3(-1, -1,  1), vec3(-1,  1,  1),
    vec3( 1,  1, -1), vec3( 1, -1, -1), vec3(-1, -1, -1), vec3(-1,  1, -1),
    vec3( 1,  1,  0), vec3( 1, -1,  0), vec3(-1, -1,  0), vec3(-1,  1,  0),
    vec3( 1,  0,  1), vec3(-1,  0,  1), vec3( 1,  0, -1), vec3(-1,  0, -1),
    vec3( 0,  1,  1), vec3( 0, -1,  1), vec3( 0, -1, -1), vec3( 0,  1, -1)
);

// Point-light cubemap lookup, matching fragment.glsl's calcPointShadow.
float biomePointShadow(int lightIdx, vec3 worldPos) {
    vec3 fragToLight = worldPos - pointLightPos[lightIdx];
    float currentDist = length(fragToLight);
    float farPlane = pointLightFarPlane[lightIdx];

    float diskRadius = (1.0 + currentDist / farPlane) * 0.02;
    float bias = 0.05;

    float shadow = 0.0;
    for (int i = 0; i < BIOME_POINT_SAMPLES; i++) {
        float closestDist = texture(pointShadowMap[lightIdx],
            fragToLight + biomePointOffsets[i] * diskRadius).r;
        closestDist *= farPlane; // stored as dist/farPlane, convert back
        if (currentDist - bias > closestDist) {
            shadow += 1.0;
        }
    }
    return shadow / float(BIOME_POINT_SAMPLES);
}
#endif

// Full lighting multiplier at worldPos — global ambient, every directional
// light (shadowed), every point light in range. Mirrors fragment.glsl's main()
// so a blade, a pebble and the floor under them agree about how lit they are.
// receiveShadow lets a caller take the lighting without the cascade lookup (the
// BiomeShadows.Receive toggle, so the cost can be measured against zero).
vec3 biomeLighting(vec3 worldPos, vec3 normal, float viewDepth, bool receiveShadow) {
    vec3 lighting = globalLightColor * globalLightBrightness;

    for (int i = 0; i < numDirLights; i++) {
        vec3 ldir = -normalize(dirLightDir[i]);
        float diff = abs(dot(normal, ldir));
        vec3 contrib = dirLightColor[i] * dirLightBrightness[i] * diff;

        if (receiveShadow && dirLightCastShadows[i]) {
            float shadow = biomeDirShadow(i, worldPos, viewDepth, normal, ldir);
            contrib *= (1.0 - shadow * dirLightShadowStrength[i]);
        }
        lighting += contrib;
    }

    for (int i = 0; i < numPointLights; i++) {
        vec3  toLight = pointLightPos[i] - worldPos;
        float dist    = length(toLight);
        if (dist < pointLightRadius[i]) {
            float t           = 1.0 - (dist / pointLightRadius[i]);
            float attenuation = t * t;
            float diff        = abs(dot(normal, normalize(toLight)));
            vec3 contrib = pointLightColor[i] * pointLightBrightness[i] * diff * attenuation;

#ifdef BIOME_LIGHT_POINT_SHADOWS
            if (receiveShadow && pointLightCastShadows[i]) {
                float shadow = biomePointShadow(i, worldPos);
                contrib *= (1.0 - shadow * pointLightShadowStrength[i]);
            }
#endif
            lighting += contrib;
        }
    }

    return lighting;
}
