// scene_block.glsl — the scene-wide lighting, shadow and fog state, as ONE
// uniform block every program that shades reads from (gfx/scene_block.go).
//
// These used to be loose uniforms, declared in fragment.glsl, biome_light.glsl,
// light_model.glsl, cloud_shadow.glsl, fog.glsl, volume_common.glsl and the GI
// trace under the same names, and written into each linked program separately
// — five or more programs a frame, a few hundred glUniform calls apiece, with
// "every program must remember to call Upload*To" as the rule holding it
// together. As a block they are written into one buffer, once a frame, and
// every program bound to SCENE_BLOCK_BINDING reads that one copy.
//
// The member names are the old uniform names, so the code that reads them is
// unchanged; only the declarations moved here. Pulled in with
//
//	#include "scene_block.glsl"
//
// (expanded by gfx.CompileShader) from every file that reads a member, and
// guarded, so a program that splices several of those files declares the block
// once.
//
// LAYOUT: std140, and gfx.SceneBlock mirrors it field for field — change one
// and change the other in the same commit (TestSceneBlockMatchesTheShader
// compares every member's offset against the driver's). Under std140 a vec3
// takes 16 bytes unless a scalar fills its last four, and every element of an
// array of scalars or vec3s takes 16; the Go side spells that out with [4]
// arrays.
//
// The texture samplers that go with this state (the cascade array, the
// cubemaps, the cloud map) are NOT here — a sampler cannot live in a block —
// and stay per-program uniforms on fixed texture units.

#ifndef SCENE_BLOCK_GLSL
#define SCENE_BLOCK_GLSL

// Must match world.MaxDirLights / world.MaxPointLights, render.ShadowCascades
// and world.MaxFogVolumes (and gfx's Scene* copies of them).
#define MAX_DIR_LIGHTS   4
#define MAX_POINT_LIGHTS 8
#define SHADOW_CASCADES  3
#define MAX_FOG_VOLUMES  8

layout(std140, binding = 0) uniform SceneBlock {
    // ---- lights (world.UploadLightsTo) ----
    vec3  globalLightColor;
    float globalLightBrightness;
    // The level's lighting-model knobs, world.GlobalLight's Wrap and
    // Hemisphere (light_model.glsl).
    float lightWrap;
    float ambientHemisphere;
    int   numDirLights;
    int   numPointLights;
    vec3  dirLightDir[MAX_DIR_LIGHTS];
    vec3  dirLightColor[MAX_DIR_LIGHTS];
    float dirLightBrightness[MAX_DIR_LIGHTS];
    // 1 for a light left out of the bake — see PointLight.Dynamic in light.go.
    int   dirLightDynamic[MAX_DIR_LIGHTS];
    vec3  pointLightPos[MAX_POINT_LIGHTS];
    vec3  pointLightColor[MAX_POINT_LIGHTS];
    float pointLightBrightness[MAX_POINT_LIGHTS];
    float pointLightRadius[MAX_POINT_LIGHTS];
    int   pointLightDynamic[MAX_POINT_LIGHTS];

    // ---- directional cascaded shadows (render.UploadShadowUniformsTo) ----
    mat4  cascadeMatrix[MAX_DIR_LIGHTS * SHADOW_CASCADES];
    float cascadeBias[MAX_DIR_LIGHTS * SHADOW_CASCADES];
    // One texel of each cascade in world units (render.DirCascade.Texel).
    float cascadeTexel[MAX_DIR_LIGHTS * SHADOW_CASCADES];
    float cascadeSplit[SHADOW_CASCADES]; // view-space distance each cascade ends at
    int   dirLightShadowLayer[MAX_DIR_LIGHTS]; // first array layer, -1 = light doesn't cast
    bool  dirLightCastShadows[MAX_DIR_LIGHTS];
    float dirLightShadowStrength[MAX_DIR_LIGHTS];
    // bakedSunLight is the directional light whose share of a baked surface's
    // light is carried per vertex (fragBakedLight.w; world.LightSet.BakedSun),
    // or -1 — see fragment.glsl.
    int   bakedSunLight;
    // The clouds' shadow (cloud_shadow.glsl): 0 = no map this frame, and the
    // directional light the map was baked for.
    int   cloudShadowOn;
    int   cloudShadowLight;
    // 0 = off, 1 = linear, 2 = exponential, 3 = exponential-squared. Must
    // match world.FogMode's values (fog.go). Here rather than with the rest of
    // the fog to fill the slot.
    int   fogMode;

    // ---- point-light cubemap shadows (render.UploadPointShadowUniformsTo) ----
    bool  pointLightCastShadows[MAX_POINT_LIGHTS];
    float pointLightShadowStrength[MAX_POINT_LIGHTS];
    float pointLightFarPlane[MAX_POINT_LIGHTS];

    mat4  cloudShadowMatrix; // world -> [0,1]^3 in the bake's light space

    // ---- fog (world.UploadFogTo) ----
    vec3  fogColor;
    float fogStart;      // where fog begins, in world units, for every mode
    vec3  fogCamPos;     // the eye the radial distance is measured from
    float fogEnd;        // linear only: where it reaches fogMaxOpacity
    float fogDensity;    // exp/exp2 only
    float fogMaxOpacity; // ceiling on the blend, so distant geometry can be
                         // left legible rather than erased
    int   numFogVolumes;
    // The point shadows' tap rotation (point_shadow.glsl): 0 holds the disk
    // still; in a TAA frame, the frame's place in the jitter sequence plus one
    // (render.coverageDither). Here to fill the slot before the mat3 array.
    int   shadowNoiseFrame;
    mat3  fogVolBasis[MAX_FOG_VOLUMES];  // world offset -> normalized shape space
    vec3  fogVolCenter[MAX_FOG_VOLUMES];
    int   fogVolShape[MAX_FOG_VOLUMES];
    vec3  fogVolColor[MAX_FOG_VOLUMES];
    float fogVolDensity[MAX_FOG_VOLUMES];    // per world unit travelled inside
    float fogVolMaxOpacity[MAX_FOG_VOLUMES];
};

#endif
