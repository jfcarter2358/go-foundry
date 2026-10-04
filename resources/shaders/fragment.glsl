#version 430

uniform sampler2D tex;
uniform float alpha; // per-geometry alpha multiplier (default 1.0)

// Second-texture blending (texture "splatting"). When hasBlend is set, tex is
// mixed with blendTex using blendMask's per-pixel luminance as the mix
// factor (0 = tex, 1 = blendTex) — see applyBlendUniforms in geometry.go. Any
// existing texture can serve as blendMask; it need not be literally
// grayscale, since only luminance is read.
uniform int       hasBlend;
uniform sampler2D blendTex;
uniform sampler2D blendMask;

// Normal (bump) mapping. When hasNormalMap is set, normalMap holds a
// tangent-space normal, RGB-encoded as n * 0.5 + 0.5, derived from the height
// field painted on the texture's bump channel in the texture editor (see
// texdoc/bump.go). Off (0) by default, so a program that links this shader
// without knowing about it — and every texture with no <name>_normal.png
// beside it — shades exactly as it did before.
uniform int       hasNormalMap;
uniform sampler2D normalMap;

// Alpha-cutout anti-aliasing, set for the opaque pass only (see
// AlphaSharpenUniform in main.go). Off (0) by default so any program that
// links this shader without knowing about it — and the sorted transparent
// pass, where alpha means blending, not coverage — behaves exactly as before.
uniform int alphaSharpen;

// TAA's stand-in for alpha-to-coverage (render/taa.go): in a TAA frame the
// scene target has one sample a pixel, so a cutout's edge coverage becomes the
// chance the pixel is kept, a different threshold each frame (coverageNoise),
// and TAA's history averages the frames back into the coverage. 0 — what an
// unwritten uniform holds — is off; otherwise one more than the frame's place
// in the jitter sequence. MUST match coverageNoise in foliage_frag.glsl,
// foliage_imp_frag.glsl, sprite_frag.glsl and nanite_vis_frag.glsl.
uniform int coverageDither;
float coverageNoise(vec2 p, int frame) {
    float ign = fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
    return fract(ign + float(frame) * 0.125);
}

// Where a cutout's edge sits once mip filtering has turned its hard 0/255
// alpha into a ramp: the level set that best reproduces the original
// silhouette is the halfway point, not "any alpha at all".
//
// alphaCutoffBias moves it, for a material carrying a `cutout` effect with a
// threshold of its own (material_kinds.go). A BIAS rather than the threshold,
// so that zero — what GL gives an uninitialised uniform, and what every
// surface with no cutout effect sends — is the 0.5 this was a constant at.
// Clamped away from both ends: a threshold of 0 discards nothing and one of 1
// discards everything, and neither is a thing an author means.
uniform float alphaCutoffBias;

// tintBias multiplies the albedo, carried as a BIAS FROM WHITE so that the zero
// an unwritten uniform holds is no tint at all — the trick uvScaleBias plays.
// A projected decal is what sets it: one grey scorch texture tinted eight ways
// is eight different marks (gfx.Geometry.Tint).
uniform vec3 tintBias;

// surfaceBlend says how the FRAMEBUFFER blend will combine this fragment, which
// the shading stage has to know about because two of the three answers are not a
// colour at all:
//
//   0  ordinary — the fragment replaces or alpha-blends over what is behind it
//   1  additive — it only ever ADDS light, so fog swallows it rather than
//      replacing it (applyFogAdditive)
//   2  modulate — it is a FACTOR on what is already in the framebuffer: a decal's
//      scorch mask, where white changes nothing and black blacks the wall out
//
// Only a projected decal sets it (gfx.Geometry.Blend); everything else in the
// engine leaves it at the 0 an unwritten uniform holds.
uniform int surfaceBlend;
float alphaCutoffLevel() {
    return clamp(0.5 + alphaCutoffBias, 0.01, 0.99);
}

// Untextured surfaces. A face that names no texture takes its albedo from its
// own normal, encoded as a colour (n * 0.5 + 0.5), instead of from tex — so a
// brush blocked out and not yet painted reads as shape, with every plane a
// different colour, rather than as the one flat slab that binding no texture
// gives. Off (0) by default, so a program that links this shader without
// knowing about it shades exactly as it did before.
uniform int normalColor;

// Self-lit surfaces — a laser, a glowing readout. When set, the texture's own
// colour is the result: no light, no shadow, no clamp to what the scene's
// lights allow. Fog still applies. Off (0) by default, so a program that links
// this shader without knowing about it shades exactly as it did before.
uniform int unlit;

// Water surface. isWater gates the whole path.
uniform int   isWater;
uniform float time;
uniform float waveAmplitude;
uniform float waveLength;
uniform float waveSpeed;
uniform float reflectivity;
uniform vec3  reflectColor;
uniform vec3  camPos;

// bakedLight selects a compiled level's static-geometry path: the static
// lights for this fragment were summed once, offline, and arrive interpolated
// on fragBakedLight; only the dynamic lights (dirLightDynamic,
// pointLightDynamic) are still summed here. Uniform rather than inferred from
// the attribute, for the reason skin_common.glsl gives about `skinned`: a
// disabled vertex attribute reads a driver default, not zero, so an unlit
// surface would read as "baked to black" rather than as "not baked".
uniform int   bakedLight;
// bakedSunLight is the directional light whose share of a baked surface's
// light is carried per vertex (fragBakedLight.w; world.LightSet.BakedSun), or
// -1. Within its cascades' reach a baked surface takes that share back off and
// lights itself with the sun shadowed by the cascades instead — exactly what
// an uncompiled level does — because the bake's shadow is a per-vertex ramp a
// grid step wide, which washes a small caster's shadow out to nothing beside a
// face its own normal turns dark at full resolution. Past the cascades the
// bake's sun stands: it is the only shadow there is out there.
uniform int   bakedSunLight;

// Global ambient light

uniform vec3  globalLightColor;
uniform float globalLightBrightness;

// Directional lights (e.g. sun)
#define MAX_DIR_LIGHTS 4
uniform int   numDirLights;
uniform vec3  dirLightDir[MAX_DIR_LIGHTS];
uniform vec3  dirLightColor[MAX_DIR_LIGHTS];
uniform float dirLightBrightness[MAX_DIR_LIGHTS];
// 1 for a light left out of the bake — see PointLight.Dynamic in light.go.
uniform int   dirLightDynamic[MAX_DIR_LIGHTS];

// Directional cascaded shadow maps.
//
// SHADOW_CASCADES, SHADOW_CASCADE_BLEND and SHADOW_FADE_FRACTION must match
// ShadowCascades / ShadowCascadeBlend / shadowFadeFraction in shadow.go.
#define SHADOW_CASCADES       3
#define SHADOW_CASCADE_BLEND  0.12
#define SHADOW_FADE_FRACTION  0.15

// One array texture holds every cascade of every casting light — MAX_DIR_LIGHTS
// * SHADOW_CASCADES separate sampler2Ds would blow the 16 fragment texture
// units GL 4.1 guarantees.
// A comparison sampler: each lookup passes the fragment's own depth as a
// reference and gets back the fraction of the four filtered texels it is in
// front of, so a tap is a smooth 0..1 rather than a per-texel yes/no. The
// compare mode lives on the texture (EnsureCascadeArray in shadow.go).
uniform sampler2DArrayShadow shadowMapArray;
uniform mat4  cascadeMatrix[MAX_DIR_LIGHTS * SHADOW_CASCADES];
uniform float cascadeBias[MAX_DIR_LIGHTS * SHADOW_CASCADES];
uniform float cascadeSplit[SHADOW_CASCADES]; // view-space distance each cascade ends at
uniform int   dirLightShadowLayer[MAX_DIR_LIGHTS]; // first array layer, -1 = light doesn't cast
uniform bool  dirLightCastShadows[MAX_DIR_LIGHTS];
uniform float dirLightShadowStrength[MAX_DIR_LIGHTS];

// Point lights
#define MAX_POINT_LIGHTS 8
uniform int   numPointLights;
uniform vec3  pointLightPos[MAX_POINT_LIGHTS];
uniform vec3  pointLightColor[MAX_POINT_LIGHTS];
uniform float pointLightBrightness[MAX_POINT_LIGHTS];
uniform float pointLightRadius[MAX_POINT_LIGHTS];
uniform int   pointLightDynamic[MAX_POINT_LIGHTS];

// Point light cubemap shadow maps
uniform samplerCube pointShadowMap[MAX_POINT_LIGHTS];
uniform bool        pointLightCastShadows[MAX_POINT_LIGHTS];
uniform float       pointLightShadowStrength[MAX_POINT_LIGHTS];
uniform float       pointLightFarPlane[MAX_POINT_LIGHTS];

// Smooth shading. When set, the surface normal is the interpolated per-vertex
// normal supplied by the vertex/tessellation stage instead of the flat facet
// the derivatives below recover — see smooth.go.
uniform int smoothShading;

in vec2 fragTexCoord;
in vec3 fragWorldPos;
in vec3 fragNormal;
// Distance along the view axis, used to pick a cascade. The light-space
// positions themselves are derived here from fragWorldPos rather than passed as
// varyings — a vec4 per light per cascade would be 48 float varyings against a
// guaranteed budget of 60.
in float fragViewDepth;
// The compiled level's baked lighting, interpolated across the triangle: xyz
// the colour, w the baked sun's share (bakedSunLight; -1 where the sun was not
// baked into this surface at all). Junk unless bakedLight is set — see the
// uniform.
in vec4 fragBakedLight;
// The part of fragBakedLight.rgb that is ambient and bounce (vertex.glsl).
in vec3 fragBakedIndirect;

out vec4 outputColor;

// MUST stay identical to waterHeight() in vertex.glsl — see the note there.
float waterHeight(vec2 p) {
    float k = 6.2831853 / max(waveLength, 0.001);
    float t = time * waveSpeed * 6.2831853;

    float h = 0.0;
    h += sin(p.x * k + t) * 0.50;
    h += sin((p.x * 0.7 + p.y * 0.7) * k * 1.3 - t * 1.1) * 0.30;
    h += sin(p.y * k * 0.8 + t * 0.8) * 0.20;
    return h * waveAmplitude;
}

// Central-difference gradient of the wave field. Done per-pixel rather than
// per-vertex so the swell stays smooth regardless of mesh density, and so no
// extra varying is needed — this fragment shader is also linked against the
// detail tessellation and scatter vertex stages, which know nothing of water.
vec3 waterNormal(vec2 p) {
    float e  = max(waveLength * 0.02, 0.01);
    float hL = waterHeight(p - vec2(e, 0.0));
    float hR = waterHeight(p + vec2(e, 0.0));
    float hD = waterHeight(p - vec2(0.0, e));
    float hU = waterHeight(p + vec2(0.0, e));
    return normalize(vec3(hL - hR, 2.0 * e, hD - hU));
}

// Tangent frame reconstructed from screen-space derivatives, rather than from
// per-vertex tangent attributes.
//
// Every mesh in the engine — walls, floors, surface meshes, skinned models,
// tessellated detail patches, scattered rock instances — shares one 5-float
// [X,Y,Z,U,V] vertex record (see Geometry in geometry.go). Adding a tangent to
// it would mean touching every builder, every buffer and every shadow pass for
// something the fragment stage can recover exactly here: dp/du and dp/dv are
// what a tangent frame *is*, and dFdx/dFdy of position and UV give them.
//
// The cost is one frame per fragment instead of one per vertex; the gain is
// that a normal map works on every surface in the engine the day it lands,
// including the procedurally displaced ones that have no authored UVs to hang
// a tangent off.
mat3 cotangentFrame(vec3 N, vec3 dp1, vec3 dp2, vec2 duv1, vec2 duv2) {
    vec3 dp2perp = cross(dp2, N);
    vec3 dp1perp = cross(N, dp1);
    vec3 T = dp2perp * duv1.x + dp1perp * duv2.x;
    vec3 B = dp2perp * duv1.y + dp1perp * duv2.y;
    // Scale both axes by the same factor so the frame stays orthonormal-ish
    // rather than stretching the normal along whichever axis is denser.
    float invmax = inversesqrt(max(dot(T, T), dot(B, B)));
    return mat3(T * invmax, B * invmax, N);
}

// 5x5 PCF sample of a single cascade layer. Returns occlusion in 0..1, or -1.0
// when the fragment falls outside this cascade's box — the caller then falls
// through to a wider cascade rather than reporting the fragment as lit.
float sampleCascade(sampler2DArrayShadow map, int matIdx, int layer, vec3 normal, vec3 lightDir) {
    vec4 posLS = cascadeMatrix[matIdx] * vec4(fragWorldPos, 1.0);
    vec3 projCoords = posLS.xyz / posLS.w;
    projCoords = projCoords * 0.5 + 0.5;

    if (projCoords.x < 0.0 || projCoords.x > 1.0 ||
        projCoords.y < 0.0 || projCoords.y > 1.0 ||
        projCoords.z > 1.0) {
        return -1.0;
    }

    // Quadratic slope-scaled bias — tighter than linear at seams. The base bias
    // is per-cascade because each cascade normalizes the same world-space bias
    // over its own depth range.
    float base = cascadeBias[matIdx];
    float cosTheta = abs(dot(normal, lightDir));
    float bias = base * (1.0 - cosTheta) * (1.0 - cosTheta) + base * 0.05;
    float currentDepth = projCoords.z;

    // 5x5 PCF for softer edges. Every tap is itself bilinearly filtered by the
    // comparison sampler, so the result is continuous across a texel rather
    // than stepping from one whole texel to the next — which is what kept
    // shadow edges on nearby geometry looking like a staircase of blocks, a
    // cascade-0 texel being centimetres wide in the world but tens of pixels
    // wide on screen when the surface is a couple of metres from the camera.
    float ref = currentDepth - bias;
    float lit = 0.0;
    vec2 texelSize = 1.0 / vec2(textureSize(map, 0).xy);
    for (int x = -2; x <= 2; x++) {
        for (int y = -2; y <= 2; y++) {
            lit += texture(map,
                vec4(projCoords.xy + vec2(x, y) * texelSize, float(layer), ref));
        }
    }
    return 1.0 - lit / 25.0;
}

// dirShadowReach is how far the cascades can be believed at this fragment: 1
// within their range, falling to 0 over the fade at its end and 0 beyond.
float dirShadowReach() {
    float maxDist = cascadeSplit[SHADOW_CASCADES - 1];
    float fadeBegin = maxDist * (1.0 - SHADOW_FADE_FRACTION);
    return 1.0 - clamp((fragViewDepth - fadeBegin) / max(maxDist - fadeBegin, 1e-4), 0.0, 1.0);
}

// calcDirShadowUnfaded is the cascades' shadow at this fragment for light
// lightIdx, whose cascades in the array start at layerBase (-1: none), before
// the fade at the end of their range.
float calcDirShadowUnfaded(int layerBase, int lightIdx, vec3 normal, vec3 lightDir) {
    if (layerBase < 0) {
        return 0.0;
    }

    int c = SHADOW_CASCADES;
    for (int i = 0; i < SHADOW_CASCADES; i++) {
        if (fragViewDepth <= cascadeSplit[i]) {
            c = i;
            break;
        }
    }
    if (c >= SHADOW_CASCADES) {
        return 0.0; // beyond ShadowMaxDistance — unshadowed
    }

    int matBase = lightIdx * SHADOW_CASCADES;
    float shadow = sampleCascade(shadowMapArray, matBase + c, layerBase + c, normal, lightDir);
    if (shadow < 0.0) {
        // A fragment at the corner of a slice can sit just outside its own
        // cascade's box; the next one out always contains it.
        if (c + 1 < SHADOW_CASCADES) {
            c += 1;
    // The sun's virtual shadow map, when the frame drew one for this light
    // in place of its cascades (vsm.glsl, render/vsm.go).
    if (vsmShadows(lightIdx)) {
        return vsmShadow(fragWorldPos, normal, lightDir);
    }
            shadow = sampleCascade(shadowMapArray, matBase + c, layerBase + c, normal, lightDir);
        }
        if (shadow < 0.0) {
            return 0.0;
        }
    }

    // Cross-fade over the tail of this cascade so the jump in texel density
    // reads as a softening rather than a seam.
    float prev = (c == 0) ? 0.0 : cascadeSplit[c - 1];
    float span = max(cascadeSplit[c] - prev, 1e-4);
    float t = (fragViewDepth - prev) / span;
    if (c + 1 < SHADOW_CASCADES && t > 1.0 - SHADOW_CASCADE_BLEND) {
        float next = sampleCascade(shadowMapArray, matBase + c + 1, layerBase + c + 1, normal, lightDir);
        if (next >= 0.0) {
            shadow = mix(shadow, next, (t - (1.0 - SHADOW_CASCADE_BLEND)) / SHADOW_CASCADE_BLEND);
        }
    }

    return shadow;
}

// Cascaded directional shadow lookup: pick the tightest cascade covering this
// fragment, cross-fade into the next at the split, and fade out entirely as the
// shadow range runs out, so the boundary isn't a hard line across the ground.
float calcDirShadow(int lightIdx, vec3 normal, vec3 lightDir) {
    return calcDirShadowUnfaded(dirLightShadowLayer[lightIdx], lightIdx, normal, lightDir)
        * dirShadowReach();
}

// Sample offsets for soft point light shadows (20 directions).
const int NUM_POINT_SAMPLES = 20;
const vec3 sampleOffsets[20] = vec3[](
    vec3( 1,  1,  1), vec3( 1, -1,  1), vec3(-1, -1,  1), vec3(-1,  1,  1),
    vec3( 1,  1, -1), vec3( 1, -1, -1), vec3(-1, -1, -1), vec3(-1,  1, -1),
    vec3( 1,  1,  0), vec3( 1, -1,  0), vec3(-1, -1,  0), vec3(-1,  1,  0),
    vec3( 1,  0,  1), vec3(-1,  0,  1), vec3( 1,  0, -1), vec3(-1,  0, -1),
    vec3( 0,  1,  1), vec3( 0, -1,  1), vec3( 0, -1, -1), vec3( 0,  1, -1)
);

// Point light cubemap shadow sampling with PCF-like disk.
float calcPointShadow(int lightIdx, vec3 fragPos) {
    vec3 fragToLight = fragPos - pointLightPos[lightIdx];
    float currentDist = length(fragToLight);
    float farPlane = pointLightFarPlane[lightIdx];

    // Disk radius scales with distance for consistent softness
    float diskRadius = (1.0 + currentDist / farPlane) * 0.02;
    float bias = 0.05;

    float shadow = 0.0;
    for (int i = 0; i < NUM_POINT_SAMPLES; i++) {
        float closestDist = texture(pointShadowMap[lightIdx], fragToLight + sampleOffsets[i] * diskRadius).r;
        closestDist *= farPlane; // stored as dist/farPlane, convert back
        if (currentDist - bias > closestDist) {
            shadow += 1.0;
        }
    }
    shadow /= float(NUM_POINT_SAMPLES);

    return shadow;
}

// dirLightContrib is directional light i's share of this fragment: wrapped
// Lambert on the side facing the viewer (light_model.glsl), then its cascade
// shadow and the clouds'. geoNormal is the unbumped normal the shadow bias
// wants — see main.
vec3 dirLightContrib(int i, vec3 normal, vec3 geoNormal) {
    vec3 ldir = -normalize(dirLightDir[i]);
    float diff = lightWrapTerm(dot(normal, ldir));
    if (diff <= 0.0) {
        return vec3(0.0); // facing away: no shadow lookup to pay for
    }
    vec3 contrib = dirLightColor[i] * dirLightBrightness[i] * diff;

    if (dirLightCastShadows[i]) {
        float shadow = calcDirShadow(i, geoNormal, ldir);
        // Shadow strength: 0 = no darkening, 1 = full shadow
        contrib *= (1.0 - shadow * dirLightShadowStrength[i]);
    }
    // The clouds' shadow, on top of the geometry's. Its own map, not a
    // cascade: a cloud is a volume and casts a soft, partial shadow no depth
    // map can hold. See cloud_shadow.glsl.
    contrib *= cloudShadowFactor(i, fragWorldPos);
    return contrib;
}

// pointLightContrib is point light i's share: wrapped Lambert, quadratic
// falloff to nothing at its radius, then its cubemap shadow.
vec3 pointLightContrib(int i, vec3 normal) {
    vec3  toLight = pointLightPos[i] - fragWorldPos;
    float dist    = length(toLight);
    if (dist >= pointLightRadius[i]) {
        return vec3(0.0);
    }
    float t           = 1.0 - (dist / pointLightRadius[i]);
    float attenuation = t * t;
    float diff        = lightWrapTerm(dot(normal, normalize(toLight)));
    if (diff <= 0.0) {
        return vec3(0.0);
    }
    vec3 contrib = pointLightColor[i] * pointLightBrightness[i] * diff * attenuation;

    if (pointLightCastShadows[i]) {
        float shadow = calcPointShadow(i, fragWorldPos);
        contrib *= (1.0 - shadow * pointLightShadowStrength[i]);
    }
    return contrib;
}

void main() {
    // Screen-space derivatives, taken once at the top of main and outside every
    // branch: derivatives are only defined under uniform control flow, and
    // gl_FrontFacing below is anything but.
    vec3 dPdx = dFdx(fragWorldPos);
    vec3 dPdy = dFdy(fragWorldPos);
    vec2 dUVdx = dFdx(fragTexCoord);
    vec2 dUVdy = dFdy(fragTexCoord);

    // Flat facet normal from screen-space position derivatives. Correct for a
    // flat surface and the fallback whenever no better normal is available.
    //
    // The cross product of two screen-space derivatives says nothing about the
    // triangle's winding: window x cross window y always comes out facing the
    // eye. So it is turned OUTWARD by the winding (away from the eye on a back
    // face), which is the orientation the interpolated normals below arrive
    // in and the one everything after this reads. Lighting is one-sided
    // (light_model.glsl), so a facet normal facing the wrong way would light a
    // surface from behind — which abs(N.L) used to hide.
    vec3 faceNormal = normalize(cross(dPdx, dPdy));
    faceNormal *= gl_FrontFacing ? 1.0 : -1.0;

    vec3 normal;
    if (isWater != 0) {
        // Re-derive from the wave field; the facet normal would only recover the
        // flat triangle and lose the swell entirely.
        normal = waterNormal(fragWorldPos.xz);
    } else if (smoothShading != 0 && dot(fragNormal, fragNormal) > 1e-12) {
        // Interpolated across the triangle, so lighting varies continuously
        // instead of stepping at every shared edge.
        normal = normalize(fragNormal);
    } else {
        normal = faceNormal;
    }

    // The outward normal as the polygon itself declares it, kept before the
    // back-face flip below: it is what normalColor paints an untextured face
    // with, and a face has to keep one colour whichever side it is seen from —
    // flipping it would make the inside of a hollowed room the photographic
    // negative of its outside.
    vec3 albedoNormal = normal;

    // Flip normal for back faces so double-sided surfaces are lit correctly.
    if (!gl_FrontFacing) {
        normal = -normal;
    }

    // The shape of the surface, before any normal map perturbs it. The shadow
    // lookups keep shading off this one: their slope-scaled bias describes how
    // far the *geometry* is tilted away from the light, and feeding it a
    // per-texel bumped normal would make the bias flicker texel to texel and
    // stipple acne across an otherwise clean surface. Same reasoning as the
    // flat-normal shadow term in biome_frag.glsl.
    vec3 geoNormal = normal;

    vec2 uv = fragTexCoord;
    if (isWater != 0) {
        // Two layers scrolling at different speeds/headings so the texture
        // never reads as a rigid sheet sliding across the surface.
        float t = time * waveSpeed;
        uv += vec2(t * 0.03, t * 0.02);
    }

    vec4 texColor;
    if (normalColor != 0) {
        // No texture named, so there is nothing to sample and nothing to blend
        // over: the normal is the albedo. Opaque, whatever texture object
        // happens to be bound.
        texColor = vec4(albedoNormal * 0.5 + 0.5, 1.0);
    } else {
        texColor = texture(tex, uv);
        if (isWater != 0) {
            vec2 uv2 = fragTexCoord * 1.7 - vec2(time * waveSpeed * 0.021, time * waveSpeed * 0.034);
            texColor = mix(texColor, texture(tex, uv2), 0.5);
        }

        if (hasBlend != 0) {
            vec4 blendColor = texture(blendTex, uv);
            vec3 maskColor = texture(blendMask, uv).rgb;
            float maskLum = dot(maskColor, vec3(0.299, 0.587, 0.114));
            texColor = mix(texColor, blendColor, maskLum);
        }
    }

    // The albedo tint, on the sampled texture rather than on the shaded result:
    // a tint says what colour the surface IS, so it has to be in place before
    // the lighting multiplies it — tinting afterwards would scale the light as
    // well and turn a dark red decal into an unlit patch. Applied to the
    // normalColor path too, so an untextured surface can still be tinted.
    texColor.rgb *= vec3(1.0) + tintBias;

    // Normal mapping, on everything but water — water derives its normal from
    // the wave field, which is the whole point of that path, and a tangent
    // frame built on a surface that is displacing under the fragment would not
    // describe it anyway. Both conditions are uniforms, so the texture() call
    // inside stays under uniform control flow.
    if (hasNormalMap != 0 && isWater == 0) {
        mat3 tbn = cotangentFrame(normal, dPdx, dPdy, dUVdx, dUVdy);
        vec3 tangentNormal = texture(normalMap, uv).xyz * 2.0 - 1.0;
        if (dot(tangentNormal, tangentNormal) > 1e-6) {
            normal = normalize(tbn * tangentNormal);
        }
    }

    vec3 lighting;

    // A compiled level's static geometry had the static lights summed once,
    // offline, and interpolates that answer instead — the light loops, the
    // cascade selection and the shadow-map samples for every one of them are
    // what that buys. The dynamic lights were left out of the bake, so they
    // are the only ones still summed for it.
    if (bakedLight != 0) {
        // Clouds still move over a baked level, so their shadow is taken off
        // the baked answer here — see CLOUD_SHADOW_BAKED_SHARE in
        // cloud_shadow.glsl for why a share rather than the whole of it. Unless
        // the sun the map follows is dynamic: then none of the baked answer is
        // its light, and its own term below takes the shadow in full.
        lighting = fragBakedLight.rgb;
        // Live GI replaces the ambient and the bounce the bake folded in
        // (fragBakedIndirect is exactly that part), and keeps the static
        // lights' own direct light. See render/gi.go.
        if (giEnabled != 0) {
            lighting = max(lighting - fragBakedIndirect, vec3(0.0))
                + ambientLight(globalLightColor * globalLightBrightness, fragWorldPos, normal);
        }
        // The baked sun, re-shadowed by the cascades where they reach (see
        // bakedSunLight). What the bake put in comes off: the sun's unshadowed
        // light on the unbumped normal the bake lit with, times the share it
        // kept. What goes on is the sun as an uncompiled level has it: on the
        // bumped normal, so a normal map shows in the sunlight, times what the
        // cascades keep — and they hold everything that casts, the compiled
        // world, entities and chunks drawn live, so this is the whole of the
        // sun's shadow, crisp. Where they fade out, the bake's share stands.
        if (bakedSunLight >= 0 && fragBakedLight.w >= 0.0) {
            int s = bakedSunLight;
            vec3 ldir = -normalize(dirLightDir[s]);
            float baked = lightWrapTerm(dot(geoNormal, ldir));
            float diff = lightWrapTerm(dot(normal, ldir));
            if (baked > 0.0 || diff > 0.0) {
                float kept = fragBakedLight.w;
                float reach = dirShadowReach();
                if (reach > 0.0) {
                    float shadow = calcDirShadowUnfaded(dirLightShadowLayer[s], s, geoNormal, ldir);
                    kept = mix(kept, 1.0 - shadow * dirLightShadowStrength[s], reach);
                }
                vec3 sun = dirLightColor[s] * dirLightBrightness[s];
                lighting = max(lighting + sun * (diff * kept - baked * fragBakedLight.w), vec3(0.0));
            }
        }
        if (dirLightDynamic[clamp(cloudShadowLight, 0, MAX_DIR_LIGHTS - 1)] == 0) {
            lighting *= mix(1.0,
                cloudShadowFactor(cloudShadowLight, fragWorldPos), CLOUD_SHADOW_BAKED_SHARE);
        }
        for (int i = 0; i < numDirLights; i++) {
            if (dirLightDynamic[i] != 0) {
                lighting += dirLightContrib(i, normal, geoNormal);
            }
        }
        for (int i = 0; i < numPointLights; i++) {
            if (pointLightDynamic[i] != 0) {
                lighting += pointLightContrib(i, normal);
            }
        }
    } else {
        // Every light, static or dynamic, on anything that was not baked.
        lighting = ambientLight(globalLightColor * globalLightBrightness, fragWorldPos, normal);
        for (int i = 0; i < numDirLights; i++) {
            lighting += dirLightContrib(i, normal, geoNormal);
        }
        for (int i = 0; i < numPointLights; i++) {
            lighting += pointLightContrib(i, normal);
        }
    }

    float finalAlpha = texColor.a * alpha;

    if (alphaSharpen != 0) {
        // Re-derive a crisp, roughly one-pixel-wide edge from the soft alpha
        // ramp mip filtering leaves behind, and hand that to
        // alpha-to-coverage (SetAlphaToCoverage, antialias.go) as the
        // fraction of the pixel the cutout actually covers.
        //
        // Without this, mipmapping and cutouts fight each other: distant mip
        // levels average a foliage texture's alpha down below the cutoff over
        // more and more of the shape, so it thins and eventually dissolves —
        // the classic regression from turning mipmaps on over alpha-tested
        // art. Rescaling by fwidth is what makes the edge's *screen-space*
        // width constant instead of its texture-space width: distance is
        // divided back out, so the silhouette stays put and only the last
        // pixel of it is partially covered.
        //
        // On a fully opaque texel fwidth is 0, the division saturates, and
        // the clamp pins coverage at 1 — ordinary opaque geometry is
        // untouched by this path, which is why it can stay on for the whole
        // opaque pass rather than being switched per surface.
        finalAlpha = clamp((finalAlpha - alphaCutoffLevel()) / max(fwidth(finalAlpha), 1e-5) + 0.5, 0.0, 1.0);
        if (coverageDither != 0) {
            if (finalAlpha <= coverageNoise(gl_FragCoord.xy, coverageDither)) discard;
            finalAlpha = 1.0;
        }
    }

    if (finalAlpha < 0.001) discard;

    vec3 surface = unlit != 0 ? texColor.rgb : texColor.rgb * clamp(lighting, 0.0, 1.0);
    // The GI debug view (gi_debug): the light alone, as if every surface
    // were white, so what GI adds can be told from what the textures are.
    if (giDebug == 1 && unlit == 0) {
        surface = clamp(lighting, 0.0, 1.0);
    }

    if (isWater != 0) {
        vec3 viewDir = normalize(camPos - fragWorldPos);

        // Schlick fresnel: water is barely reflective looking straight down and
        // near-mirror at grazing angles, which is what sells it as a surface.
        float cosTheta = clamp(dot(normal, viewDir), 0.0, 1.0);
        float fresnel  = reflectivity + (1.0 - reflectivity) * pow(1.0 - cosTheta, 5.0);

        surface = mix(surface, reflectColor * clamp(lighting, 0.0, 1.0), fresnel);

        // Blinn-Phong glint from each directional light — the moving specular
        // highlight that reads as sun on water.
        for (int i = 0; i < numDirLights; i++) {
            vec3  ldir = -normalize(dirLightDir[i]);
            vec3  halfVec = normalize(ldir + viewDir);
            float spec = pow(max(dot(normal, halfVec), 0.0), 128.0);
            surface += dirLightColor[i] * dirLightBrightness[i] * spec * fresnel;
        }

        // Grazing angles also read as more opaque, matching the reflection.
        finalAlpha = clamp(finalAlpha + fresnel * (1.0 - finalAlpha) * 0.5, 0.0, 1.0);
    }

    if (surfaceBlend == 2) {
        // A modulate decal is a MASK, not a surface. Three things follow, and
        // all three are wrong if it is shaded like anything else here.
        //
        // It is never lit: the wall under it has already been lit, and lighting
        // the mask as well would apply the room's light to that wall twice.
        //
        // Its alpha cannot be left to the framebuffer blend. The GL multiply
        // blend can express `src * dst` and not `dst * mix(1, src, a)`, so a
        // half-transparent WHITE texel would come out brighter than the wall it
        // is supposed to leave alone. Folding the alpha toward white here is what
        // makes a partly-faded mask fade instead of glowing — which is also what
        // lets a decal's lifetime fade one out at all.
        //
        // And its fog fades it toward white rather than toward the fog colour,
        // for the reason applyFogModulate gives.
        surface = mix(vec3(1.0), texColor.rgb, finalAlpha);
        surface = applyFogModulate(surface, fragWorldPos);
        outputColor = vec4(surface, 1.0);
        return;
    }

    // Distance fog, last: it acts on the shaded result, so a fogged surface
    // loses its own lighting along with its colour rather than glowing through
    // the fog. See fog.glsl, injected above this file's own #version line.
    //
    // An additive fragment takes the other form of the same answer: the
    // framebuffer already holds the fog behind it, so mixing toward the fog
    // colour would add the fog a second time and make a distant mark brighter
    // than a near one.
    surface = surfaceBlend == 1
        ? applyFogAdditive(surface, fragWorldPos)
        : applyFog(surface, fragWorldPos);

    outputColor = vec4(surface, finalAlpha);
}
