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
// beside it — shades exactly as it did before. 2 means the map is BC5
// (gfx/texture_compress.go): only x and y are stored, the blue channel reads
// 0, and z is rebuilt from them as sqrt(1 - x² - y²).
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

// Water surface. isWater gates the whole path; the height field and its
// uniforms (isWater, time, the swell) are water_waves.glsl's, shared with
// vertex.glsl so the two stages cannot drift. The shading is water.glsl's,
// included above main.
#include "water_waves.glsl"
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

// A compiled level's lightmaps (render/lightmap.go; compile/lightmap.go): rgb
// the static lights' direct light other than the baked sun's, a the sun's
// visibility. A lightmapped surface's baked row says where it reads them —
// fragBakedLight.xy the texel, .z the page, .w -2 (or -3: the sun was kept
// out of its chunk) where a per-vertex row has the sun's share.
uniform sampler2DArray lightmaps;

// The scene's lights, their shadows and the fog — globalLight*, dirLight*,
// pointLight*, cascade*, bakedSunLight and the rest — are one uniform block
// shared by every program that shades, filled once a frame (gfx/scene_block.go).
// bakedSunLight, among them, is the directional light whose share of a baked
// surface's light is carried per vertex (fragBakedLight.w;
// world.LightSet.BakedSun), or -1. Within its cascades' reach a baked surface
// takes that share back off and lights itself with the sun shadowed by the
// cascades instead — exactly what an uncompiled level does — because the
// bake's shadow is a per-vertex ramp a grid step wide, which washes a small
// caster's shadow out to nothing beside a face its own normal turns dark at
// full resolution. Past the cascades the bake's sun stands: it is the only
// shadow there is out there.
#include "scene_block.glsl"

// GGX specular for every light (specular.glsl; docs/lighting/PLAN.md).
#include "specular.glsl"

// The surface's specular inputs (gfx/material_pbr.go): BIASES from
// PBR_DEFAULT_ROUGHNESS and PBR_DEFAULT_SPECULAR, and a metalness, so the zero
// an unwritten uniform holds is a surface with no pbr effect. hasArmMap binds
// the texture's AO / roughness / metal map (gfx/texture_armmap.go), whose
// channels the factors multiply.
uniform float pbrRoughnessBias;
uniform float pbrMetallic;
uniform float pbrSpecularBias;
uniform int   hasArmMap;
uniform sampler2D armMap;

// Directional cascaded shadows: SHADOW_CASCADE_BLEND and SHADOW_FADE_FRACTION
// must match ShadowCascadeBlend / shadowFadeFraction in shadow.go
// (SHADOW_CASCADES is scene_block.glsl's).
#define SHADOW_CASCADE_BLEND  0.25
#define SHADOW_FADE_FRACTION  0.15

// One array texture holds every cascade of every casting light — MAX_DIR_LIGHTS
// * SHADOW_CASCADES separate sampler2Ds would blow the 16 fragment texture
// units GL 4.1 guarantees.
// A comparison sampler: each lookup passes the fragment's own depth as a
// reference and gets back the fraction of the four filtered texels it is in
// front of, so a tap is a smooth 0..1 rather than a per-texel yes/no. The
// compare mode lives on the texture (EnsureCascadeArray in shadow.go).
uniform sampler2DArrayShadow shadowMapArray;

// Point light cubemap shadow maps, and the lookup (pointShadow) the biome
// passes share.
#include "point_shadow.glsl"

// The terrain and rock material effects — triplanar, splat, ground blend,
// streaks, detail normal, macro variation, colour grade — each gated by a bit
// of matFx, which is 0 (none of them) for every surface whose material does
// not carry one. See surface_fx.glsl and docs/canyon/PHASE_5.md.
#include "surface_fx.glsl"

// Per-vertex colour and cavity (gfx.VertexColorsUniform): when set, the albedo
// is multiplied by fragVertColor.rgb and fragVertColor.a is the surface's
// cavity/ambient occlusion — all of the ambient and VERTEX_AO_DIRECT of the
// direct light is taken by it (a crevice loses the sky first, and a little of
// the sun to the inter-reflection it no longer gets). A generated rock bakes
// both (procgen/rocks, docs/canyon/PHASE_2.md). Off (0) by default — every
// mesh without colours — and then the lighting below is the code it always
// was, operation for operation.
uniform int vertexColors;
#define VERTEX_AO_DIRECT 0.35

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
// The vertex colour and cavity (vertexColors); white without them.
in vec4 fragVertColor;

layout(location = 0) out vec4 outputColor;
// The stylize pass's record of this surface (light_model.glsl's stylizePack;
// render/stylize.go). Draw buffer 1 is the scene target's normal buffer only
// while a painted level's opaque world draws (stylizeBeginWrites); everywhere
// else nothing is bound there and the write goes nowhere.
layout(location = 1) out uvec4 outputStylize;

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
    float m = max(dot(T, T), dot(B, B));
    // A surface whose UVs do not change across the pixel has NO tangent
    // frame: T and B are zero, inversesqrt(0) is infinite and 0 * inf is
    // NaN — a NaN normal, NaN light, which the GTX 1070 showed as white and
    // Mesa as black. Scans carry such faces (fir_tree_01's trunk_c has no
    // TEXCOORD_0 at all, so every vertex sits at UV 0,0), and a DAG level
    // can collapse a small UV island to a point. Any frame about N is as
    // right as another there (the map reads one texel), so build one from N
    // alone. `!(m > ...)` also catches a NaN that came in.
    if (!(m > 1e-24)) {
        vec3 a = abs(N.y) < 0.99 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
        vec3 t = normalize(cross(a, N));
        return mat3(t, cross(N, t), N);
    }
    float invmax = inversesqrt(m);
    return mat3(T * invmax, B * invmax, N);
}

// 5x5 PCF sample of a single cascade layer, its taps spread texels apart.
// Returns occlusion in 0..1, or -1.0 when the fragment falls outside this
// cascade's box — the caller then falls through to a wider cascade rather than
// reporting the fragment as lit.
float sampleCascade(sampler2DArrayShadow map, int matIdx, int layer, vec3 normal, vec3 lightDir, float spread) {
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
    // Scaled by the spread: a tap further out along a slope reads a depth
    // that much further from this fragment's, and would shadow the surface
    // it stands on.
    float bias = (base * (1.0 - cosTheta) * (1.0 - cosTheta) + base * 0.05) * spread;
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
                vec4(projCoords.xy + vec2(x, y) * texelSize * spread, float(layer), ref));
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

// cascadeSpread is how many of its own texels apart cascade c's PCF taps are,
// at fraction t of the way through its depth range: one, its own resolution,
// for the near part of it, widening to the next cascade's texel by the split.
// Each cascade's texels are several times the last one's, so a fixed
// footprint in texels made shadow edges jump that much softer at every
// hand-over; matched like this, the two cascades filter alike where they meet
// and the edges soften steadily with distance instead.
float cascadeSpread(int matBase, int c, float t) {
    if (c + 1 >= SHADOW_CASCADES) {
        return 1.0;
    }
    float own = max(cascadeTexel[matBase + c], 1e-5);
    float ratio = max(cascadeTexel[matBase + c + 1] / own, 1.0);
    return mix(1.0, ratio, smoothstep(0.25, 1.0, t));
}

// calcDirShadowUnfaded is the cascades' shadow at this fragment for light
// lightIdx, whose cascades in the array start at layerBase (-1: none), before
// the fade at the end of their range.
float calcDirShadowUnfaded(int layerBase, int lightIdx, vec3 normal, vec3 lightDir) {
    // The sun's virtual shadow map, when the frame drew one for this light
    // in place of its cascades (vsm.glsl, render/vsm.go).
    if (vsmShadows(lightIdx)) {
        return vsmShadow(fragWorldPos, normal, lightDir);
    }
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
    float prev = (c == 0) ? 0.0 : cascadeSplit[c - 1];
    float span = max(cascadeSplit[c] - prev, 1e-4);
    float t = (fragViewDepth - prev) / span;
    float shadow = sampleCascade(shadowMapArray, matBase + c, layerBase + c, normal, lightDir,
        cascadeSpread(matBase, c, t));
    if (shadow < 0.0) {
        // A fragment at the corner of a slice can sit just outside its own
        // cascade's box; the next one out always contains it.
        if (c + 1 < SHADOW_CASCADES) {
            c += 1;
            shadow = sampleCascade(shadowMapArray, matBase + c, layerBase + c, normal, lightDir, 1.0);
        }
        if (shadow < 0.0) {
            return 0.0;
        }
        return shadow;
    }

    // Cross-fade over the tail of this cascade, so what the filter matching
    // in cascadeSpread leaves of the change in depth resolution reads as a
    // softening rather than a seam.
    if (c + 1 < SHADOW_CASCADES && t > 1.0 - SHADOW_CASCADE_BLEND) {
        float next = sampleCascade(shadowMapArray, matBase + c + 1, layerBase + c + 1, normal, lightDir, 1.0);
        if (next >= 0.0) {
            shadow = mix(shadow, next, smoothstep(1.0 - SHADOW_CASCADE_BLEND, 1.0, t));
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

// dirLightContrib is directional light i's share of this fragment: wrapped
// Lambert on the side facing the viewer (light_model.glsl), then its cascade
// shadow and the clouds'. geoNormal is the unbumped normal the shadow bias
// wants — see main. Its specular reflection, under the same shadow, is added
// to spec.
vec3 dirLightContrib(int i, vec3 normal, vec3 geoNormal, PBRSurface ps, vec2 occ, inout vec3 spec) {
    vec3 ldir = -normalize(dirLightDir[i]);
    float diff = lightWrapTerm(dot(normal, ldir));
    if (diff <= 0.0) {
        return vec3(0.0); // facing away: no shadow lookup to pay for
    }
    vec3 radiance = dirLightColor[i] * dirLightBrightness[i];

    float vis = 1.0;
    if (dirLightCastShadows[i]) {
        float shadow = calcDirShadow(i, geoNormal, ldir);
        // Shadow strength: 0 = no darkening, 1 = full shadow
        vis = 1.0 - shadow * dirLightShadowStrength[i];
    }
    // The clouds' shadow, on top of the geometry's. Its own map, not a
    // cascade: a cloud is a volume and casts a soft, partial shadow no depth
    // map can hold. See cloud_shadow.glsl.
    vis *= cloudShadowFactor(i, fragWorldPos);
    // The sun's contact shadow (render/ao.go): what its shadow map is too
    // coarse to hold.
    vis *= ssaoContact(i, occ);
    spec += radiance * vis * pbrSpecular(normal, ldir, ps);
    return radiance * diff * vis;
}

// pointLightAt is point light i's colour × brightness × falloff at this
// fragment (quadratic to nothing at its radius) and the unit direction to it;
// false out of its reach.
bool pointLightAt(int i, out vec3 radiance, out vec3 ldir) {
    vec3  toLight = pointLightPos[i] - fragWorldPos;
    float dist    = length(toLight);
    if (dist >= pointLightRadius[i]) {
        return false;
    }
    float t = 1.0 - (dist / pointLightRadius[i]);
    radiance = pointLightColor[i] * pointLightBrightness[i] * (t * t);
    ldir = toLight / max(dist, 1e-5);
    return true;
}

// pointLightVisibility is how much of point light i its cubemap shadow lets
// through.
float pointLightVisibility(int i) {
    if (!pointLightCastShadows[i]) {
        return 1.0;
    }
    float shadow = pointShadow(i, fragWorldPos); // point_shadow.glsl
    return 1.0 - shadow * pointLightShadowStrength[i];
}

// pointLightContrib is point light i's share: wrapped Lambert, quadratic
// falloff to nothing at its radius, then its cubemap shadow. Its specular, the
// same shadow on it, is added to spec.
vec3 pointLightContrib(int i, vec3 normal, PBRSurface ps, inout vec3 spec) {
    vec3 radiance, ldir;
    if (!pointLightAt(i, radiance, ldir)) {
        return vec3(0.0);
    }
    float diff = lightWrapTerm(dot(normal, ldir));
    if (diff <= 0.0) {
        return vec3(0.0);
    }
    float vis = pointLightVisibility(i);
    spec += radiance * vis * pbrSpecular(normal, ldir, ps);
    return radiance * diff * vis;
}

// pointLightSpecular is point light i's specular alone, for a baked surface:
// its diffuse arrived from the bake, but a highlight depends on where the eye
// is and cannot.
vec3 pointLightSpecular(int i, vec3 normal, PBRSurface ps) {
    if (ps.on == 0.0) {
        return vec3(0.0);
    }
    vec3 radiance, ldir;
    if (!pointLightAt(i, radiance, ldir) || dot(normal, ldir) <= 0.0) {
        return vec3(0.0);
    }
    return radiance * pointLightVisibility(i) * pbrSpecular(normal, ldir, ps);
}

// The water surface's shading and the caustics under it (water.glsl).
#include "water.glsl"

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
        normal = waterSurfaceNormal(fragWorldPos);
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
        // happens to be bound. A mesh with vertex colours has a colour of its
        // own, though: white here, its colour multiplied in below.
        texColor = vertexColors != 0 ? vec4(1.0) : vec4(albedoNormal * 0.5 + 0.5, 1.0);
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

    // The effects that make the albedo and the normal themselves, projected in
    // world space (surface_fx.glsl): the surface's own texture triplanar, or
    // the terrain's splat layers; then the grade, varnish streaks and macro
    // variation on the colour that gives. fxAO is the splat layers' ambient
    // occlusion, 1 everywhere else. matFx is a uniform: a surface with none of
    // them takes none of these branches.
    float fxAO = 1.0;
    if ((matFx & FX_TRIPLANAR) != 0 && normalColor == 0) {
        normal = fxTriplanarSurface(tex, normalMap, isWater == 0 ? hasNormalMap : 0, fragWorldPos, normal, dPdx, dPdy, texColor);
    }
    if ((matFx & FX_SPLAT) != 0) {
        // FX_SPLAT8 tested here, in main, so the material variants fold it
        // (render/nanite_vis_variants.go): a four-layer bin drops 5–8.
        // FX_TRAIL likewise: a bin with no soft layer drops the ground trails.
        fxSplatSurface(fragWorldPos, normal, dPdx, dPdy, (matFx & FX_SPLAT8) != 0, (matFx & FX_TRAIL) != 0, texColor,
                       normal, fxAO);
    }
    if ((matFx & FX_GRADE) != 0) {
        fxGradeApply(texColor);
    }
    if ((matFx & FX_STREAKS) != 0) {
        fxStreaksApply(fragWorldPos, geoNormal, texColor);
    }
    if ((matFx & FX_MACRO) != 0) {
        fxMacroApply(fragWorldPos, texColor);
    }

    // The albedo tint, on the sampled texture rather than on the shaded result:
    // a tint says what colour the surface IS, so it has to be in place before
    // the lighting multiplies it — tinting afterwards would scale the light as
    // well and turn a dark red decal into an unlit patch. Applied to the
    // normalColor path too, so an untextured surface can still be tinted.
    texColor.rgb *= vec3(1.0) + tintBias;

    // The vertex colour, on the albedo like the tint (see vertexColors).
    float vertexAO = 1.0;
    if (vertexColors != 0) {
        texColor.rgb *= fragVertColor.rgb;
        vertexAO = clamp(fragVertColor.a, 0.0, 1.0);
    }
    // A rock's foot takes the ground's colour (surface_fx.glsl) — after the
    // vertex colour, so the cavity and strata a generated rock baked do not
    // stain the sand it fades into.
    if ((matFx & FX_GROUND) != 0) {
        fxGroundBlend(fragWorldPos, dPdx, dPdy, texColor);
    }
    vertexAO *= fxAO;

    // Normal mapping, on everything but water — water derives its normal from
    // the wave field, which is the whole point of that path, and a tangent
    // frame built on a surface that is displacing under the fragment would not
    // describe it anyway. Both conditions are uniforms, so the texture() call
    // inside stays under uniform control flow.
    // A triplanar or splat surface has had its normal mapped already, by
    // projections the mesh's UVs know nothing of.
    if (hasNormalMap != 0 && isWater == 0 && (matFx & (FX_TRIPLANAR | FX_SPLAT)) == 0) {
        mat3 tbn = cotangentFrame(normal, dPdx, dPdy, dUVdx, dUVdy);
        vec3 tangentNormal = texture(normalMap, uv).xyz * 2.0 - 1.0;
        if (hasNormalMap == 2) {
            tangentNormal.z = sqrt(max(1.0 - dot(tangentNormal.xy, tangentNormal.xy), 0.0));
        }
        if (dot(tangentNormal, tangentNormal) > 1e-6) {
            normal = normalize(tbn * tangentNormal);
        }
    }
    if ((matFx & FX_DETAIL) != 0 && isWater == 0) {
        normal = fxDetailApply(fragWorldPos, geoNormal, normal, dPdx, dPdy, fragViewDepth);
    }

    // The specular's inputs (specular.glsl): the surface's factors, times its
    // ARM map's roughness and metalness where it has one, whose AO joins the
    // vertex cavity. Water has a reflection model of its own; an unlit
    // surface, a decal's mask and an additive mark reflect nothing.
    float pbrRough = PBR_DEFAULT_ROUGHNESS + pbrRoughnessBias;
    float pbrMetal = pbrMetallic;
    if (hasArmMap != 0 && isWater == 0 && (matFx & (FX_TRIPLANAR | FX_SPLAT)) == 0) {
        vec3 arm = texture(armMap, uv).rgb;
        vertexAO *= arm.r;
        if (hasArmMap == 2) {
            // No pbr effect: the map's own roughness and metal
            // (gfx.ARMReplaces).
            pbrRough = arm.g;
            pbrMetal = arm.b;
        } else {
            pbrRough *= arm.g;
            pbrMetal *= arm.b;
        }
    }
    PBRSurface ps = pbrSurface(fragWorldPos, normal, pbrRough, pbrMetal,
        PBR_DEFAULT_SPECULAR + pbrSpecularBias, texColor.rgb);
    if (isWater != 0 || unlit != 0 || surfaceBlend != 0) {
        ps.on = 0.0;
    } else if (giDebug == 3) {
        // The mirror view (render.GIDebugMirror): a white, perfectly smooth
        // metal, so what the environment holds is seen whole.
        ps = pbrSurface(fragWorldPos, normal, 0.0, 1.0, 1.0, vec3(1.0));
        ps.on = 1.0;
    }
    vec3 spec = vec3(0.0);

    vec3 lighting;
    // The ambient light the surface took, for the environment's reflection to
    // be scaled by (specular.glsl's pbrEnvSpecular).
    vec3 ambientRef;
    // The last frame's screen-space occlusion here (specular.glsl's ssaoAt):
    // x on the ambient and the reflection, y on the sun.
    vec2 occ = ssaoAt(fragWorldPos);

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
        // A lightmapped row: the direct light from the texel, the sun's
        // visibility with it; the indirect part still per vertex.
        bool mapped = fragBakedLight.w < -1.5;
        vec4 lmTexel = vec4(0.0);
        vec3 bakedDirect = max(fragBakedLight.rgb - fragBakedIndirect, vec3(0.0));
        if (mapped) {
            lmTexel = textureLod(lightmaps, vec3(fragBakedLight.xy, fragBakedLight.z), 0.0);
            bakedDirect = lmTexel.rgb;
        }
        // The bake's ambient and bounce (fragBakedIndirect) take the screen's
        // occlusion like a live surface's ambient does.
        ambientRef = fragBakedIndirect * occ.x;
        // Live GI replaces the ambient and the bounce the bake folded in
        // (fragBakedIndirect is exactly that part), and keeps the static
        // lights' own direct light. See render/gi.go.
        if (giEnabled != 0) {
            ambientRef = ambientLight(globalLightColor * globalLightBrightness, fragWorldPos, normal) * occ.x;
        }
        lighting = bakedDirect + ambientRef;
        // The baked sun, re-shadowed by the cascades where they reach (see
        // bakedSunLight). What the bake put in comes off: the sun's unshadowed
        // light on the unbumped normal the bake lit with, times the share it
        // kept. What goes on is the sun as an uncompiled level has it: on the
        // bumped normal, so a normal map shows in the sunlight, times what the
        // cascades keep — and they hold everything that casts, the compiled
        // world, entities and chunks drawn live, so this is the whole of the
        // sun's shadow, crisp. Where they fade out, the bake's share stands.
        if (mapped && bakedSunLight >= 0 && fragBakedLight.w > -2.5) {
            // The lightmap's sun: its visibility from the texel, its light
            // worked out here on whatever normal the surface shades with,
            // and the cascades' shadow in place of the texel's within their
            // reach, as below.
            int s = bakedSunLight;
            vec3 ldir = -normalize(dirLightDir[s]);
            float diff = lightWrapTerm(dot(normal, ldir));
            if (diff > 0.0) {
                float kept = lmTexel.a;
                float reach = dirShadowReach();
                if (reach > 0.0) {
                    float shadow = calcDirShadowUnfaded(dirLightShadowLayer[s], s, geoNormal, ldir);
                    kept = mix(kept, (1.0 - shadow * dirLightShadowStrength[s]) * ssaoContact(s, occ), reach);
                }
                vec3 sun = dirLightColor[s] * dirLightBrightness[s];
                lighting += sun * diff * kept;
                spec += sun * kept * pbrSpecular(normal, ldir, ps);
            }
        } else if (bakedSunLight >= 0 && fragBakedLight.w >= 0.0) {
            int s = bakedSunLight;
            vec3 ldir = -normalize(dirLightDir[s]);
            float baked = lightWrapTerm(dot(geoNormal, ldir));
            float diff = lightWrapTerm(dot(normal, ldir));
            if (baked > 0.0 || diff > 0.0) {
                float kept = fragBakedLight.w;
                float reach = dirShadowReach();
                if (reach > 0.0) {
                    float shadow = calcDirShadowUnfaded(dirLightShadowLayer[s], s, geoNormal, ldir);
                    kept = mix(kept, (1.0 - shadow * dirLightShadowStrength[s]) * ssaoContact(s, occ), reach);
                }
                vec3 sun = dirLightColor[s] * dirLightBrightness[s];
                lighting = max(lighting + sun * (diff * kept - baked * fragBakedLight.w), vec3(0.0));
                // The sun's highlight, under the shadow just worked out; the
                // clouds' below take their share of it with the rest.
                spec += sun * kept * pbrSpecular(normal, ldir, ps);
            }
        }
        if (dirLightDynamic[clamp(cloudShadowLight, 0, MAX_DIR_LIGHTS - 1)] == 0) {
            float cloud = mix(1.0,
                cloudShadowFactor(cloudShadowLight, fragWorldPos), CLOUD_SHADOW_BAKED_SHARE);
            lighting *= cloud;
            spec *= cloud;
        }
        for (int i = 0; i < numDirLights; i++) {
            if (dirLightDynamic[i] != 0) {
                lighting += dirLightContrib(i, normal, geoNormal, ps, occ, spec);
            }
        }
        for (int i = 0; i < numPointLights; i++) {
            if (pointLightDynamic[i] != 0) {
                lighting += pointLightContrib(i, normal, ps, spec);
            } else {
                spec += pointLightSpecular(i, normal, ps);
            }
        }
    } else if (vertexColors != 0 || vertexAO < 1.0) {
        // Every light, with the vertex cavity taken off (see vertexColors) —
        // and a splat's layer AO and an ARM map's, which vertexAO carries too.
        vec3 direct = vec3(0.0);
        vec3 directSpec = vec3(0.0);
        for (int i = 0; i < numDirLights; i++) {
            direct += dirLightContrib(i, normal, geoNormal, ps, occ, directSpec);
        }
        for (int i = 0; i < numPointLights; i++) {
            direct += pointLightContrib(i, normal, ps, directSpec);
        }
        ambientRef = ambientLight(globalLightColor * globalLightBrightness, fragWorldPos, normal) * (vertexAO * occ.x);
        lighting = ambientRef + direct * mix(1.0, vertexAO, VERTEX_AO_DIRECT);
        spec += directSpec * mix(1.0, vertexAO, VERTEX_AO_DIRECT);
    } else {
        // Every light, static or dynamic, on anything that was not baked.
        ambientRef = ambientLight(globalLightColor * globalLightBrightness, fragWorldPos, normal) * occ.x;
        lighting = ambientRef;
        for (int i = 0; i < numDirLights; i++) {
            lighting += dirLightContrib(i, normal, geoNormal, ps, occ, spec);
        }
        for (int i = 0; i < numPointLights; i++) {
            lighting += pointLightContrib(i, normal, ps, spec);
        }
    }

    // What the environment sends back (specular.glsl; render/envmap.go): the
    // sky or the reflection volume p stands in, by roughness, dimmed by the
    // surface's cavity.
    spec += pbrEnvSpecular(ps, fragWorldPos, normal, ambientRef,
        vertexAO * pbrSpecularOcclusion(ps.NoV, occ.x, ps.rough));

    // Caustics on whatever lies under a water surface (water.glsl;
    // render/water_caustics.go). Zero bodies — the usual case — is one test.
    if (waterCausticCount > 0 && isWater == 0 && unlit == 0) {
        lighting *= waterCaustics(fragWorldPos, normal);
    }

    // A painted level's light ramp (light_model.glsl): cool shadow, warm
    // light. On the diffuse light alone — the highlights are the lights' own
    // colour — and after everything that adds to or scales it.
    lighting = stylizeLight(lighting);

    // A water surface's `tex` is the generated detail map, whose alpha is
    // noise, not coverage (water.glsl).
    float finalAlpha = (isWater != 0 ? 1.0 : texColor.a) * alpha;

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

    // The light is clamped to what the frame can show: 1 in LDR, as it always
    // was, or far above in an HDR frame (gfx/hdr.go), where an unlit
    // surface's colour, being screen colour, is carried into the scene's
    // units instead.
    // A metal's colour is its reflection's: it has no diffuse (specular.glsl).
    vec3 surface = unlit != 0 ? hdrFromDisplay(texColor.rgb)
        : texColor.rgb * pbrDiffuseWeight(pbrMetal) * clamp(lighting, 0.0, hdrLightMax())
            + clamp(spec, 0.0, hdrLightMax());
    // The GI debug view (gi_debug): the light alone, as if every surface
    // were white, so what GI adds can be told from what the textures are.
    if (giDebug == 1 && unlit == 0) {
        surface = clamp(lighting, 0.0, 1.0);
    }
    // The specular and mirror views: the reflection alone.
    if ((giDebug == 2 || giDebug == 3) && unlit == 0) {
        surface = spec;
    }
    // The occlusion view (render.GIDebugAO): the AO in grey, the sun's
    // contact shadow taking the green and blue out.
    if (giDebug == 4 && unlit == 0) {
        surface = vec3(occ.x, occ.x * occ.y, occ.x * occ.y);
    }

    if (isWater != 0) {
        // Depth colour, refraction, reflection, glint, foam and the soft
        // shoreline (water.glsl); finalAlpha comes back as the blend's. It
        // starts from the surface's own alpha alone: `tex` is the generated
        // detail map on a water surface, and its alpha is noise.
        finalAlpha = alpha;
        surface = waterShade(normal, lighting, finalAlpha);
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
        outputStylize = uvec4(0u);
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
        : applyFogScene(surface, fragWorldPos);

    outputColor = vec4(surface, finalAlpha);
    // Self-lit things, water and blended marks take no paint. Packed only for
    // a painted level (styRamp.w, a uniform): elsewhere nothing reads it.
    outputStylize = styRamp.w == 0.0 ? uvec4(0u)
        : stylizePack(geoNormal, normal, fragViewDepth,
            (unlit != 0 || isWater != 0 || surfaceBlend != 0) ? 0.0 : 1.0);
}
