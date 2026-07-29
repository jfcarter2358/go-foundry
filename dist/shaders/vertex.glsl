#version 410

uniform mat4 projection;
uniform mat4 camera;
uniform mat4 model;

#define MAX_DIR_LIGHTS 4
uniform int  numDirLights;
uniform mat4 lightSpaceMatrix[MAX_DIR_LIGHTS];

// Water animation. isWater gates the whole path; time is seconds since start.
uniform int   isWater;
uniform float time;
uniform float waveAmplitude;
uniform float waveLength;
uniform float waveSpeed;

layout(location = 0) in vec3 vert;
layout(location = 1) in vec2 vertTexCoord;

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec4 fragPosLightSpace[MAX_DIR_LIGHTS];

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
    vec4 worldPos = model * vec4(vert, 1.0);

    // Vertical displacement only, so worldPos.xz still carries the undisplaced
    // surface coordinate the fragment stage needs to re-derive the normal.
    if (isWater != 0) {
        worldPos.y += waterHeight(worldPos.xz);
    }

    fragWorldPos = worldPos.xyz;

    for (int i = 0; i < numDirLights; i++) {
        fragPosLightSpace[i] = lightSpaceMatrix[i] * worldPos;
    }

    gl_Position = projection * camera * worldPos;
}
