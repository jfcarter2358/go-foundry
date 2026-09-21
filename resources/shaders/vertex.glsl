#version 410

uniform mat4 projection;
uniform mat4 camera;
uniform mat4 model;

// Water animation. isWater gates the whole path; time is seconds since start.
uniform int   isWater;
uniform float time;
uniform float waveAmplitude;
uniform float waveLength;
uniform float waveSpeed;

// Smooth shading: 1 = shade from vertNormal, 0 = let the fragment stage recover
// a flat facet normal from screen-space derivatives. Locations are pinned so the
// same VAO also feeds the detail tessellation program (see detail_vert.glsl).
uniform int smoothShading;

layout(location = 0) in vec3 vert;
layout(location = 1) in vec2 vertTexCoord;
// Per-vertex normal, present only on surfaces built with smoothing on. The
// attribute array is disabled otherwise, so this reads (0,0,0) and is ignored.
layout(location = 2) in vec3 vertNormal;
// Baked lighting, present only on a compiled level's static surfaces.
// Locations 3 and 4 belong to skinning (skin_common.glsl), so this takes 5.
// The attribute array is disabled otherwise; bakedLight is what gates reading
// it, for the same reason `skinned` gates the bone attributes — an unenabled
// attribute reads a driver default, not zero.
// xyz is the baked colour; w the baked sun's share (see fragment.glsl).
layout(location = 5) in vec4 vertLight;

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec3 fragNormal;
// View-axis distance; the fragment stage uses it to select a shadow cascade.
out float fragViewDepth;
// The baked value, passed through and interpolated across the triangle.
out vec4 fragBakedLight;

// Sum of three travelling sine waves at different headings, so the surface
// never reads as a single repeating ripple. Driven by world XZ, which keeps
// wave size independent of how far the plane is scaled up.
//
// MUST stay identical to waterHeight() in fragment.glsl — the vertex stage
// displaces geometry with it and the fragment stage differentiates it for the
// shading normal. If they drift, lit highlights slide off the actual crests.
float waterHeight(vec2 p) {
    float k = 6.2831853 / max(waveLength, 0.001);
    float t = time * waveSpeed * 6.2831853;

    float h = 0.0;
    h += sin(p.x * k + t) * 0.50;
    h += sin((p.x * 0.7 + p.y * 0.7) * k * 1.3 - t * 1.1) * 0.30;
    h += sin(p.y * k * 0.8 + t * 0.8) * 0.20;
    return h * waveAmplitude;
}

void main() {
    fragTexCoord = vertTexCoord;
    fragBakedLight = vertLight;
    // skinPosition is the identity unless this draw call set `skinned`
    // (skin_common.glsl), so every static surface takes the same path it always
    // did.
    vec4 worldPos = model * skinPosition(vert);

    // Vertical displacement only, so worldPos.xz still carries the undisplaced
    // surface coordinate the fragment stage needs to re-derive the normal.
    if (isWater != 0) {
        worldPos.y += waterHeight(worldPos.xz);
    }

    fragWorldPos = worldPos.xyz;

    // Inverse-transpose rather than mat3(model): primitives carry per-axis
    // scale, which skews a plain rotation of the normal off the surface. Only
    // paid for on smoothed surfaces.
    fragNormal = vec3(0.0);
    if (smoothShading != 0) {
        fragNormal = transpose(inverse(mat3(model))) * skinNormal(vertNormal);
    }

    vec4 viewPos = camera * worldPos;
    fragViewDepth = -viewPos.z;

    gl_Position = projection * viewPos;
}
