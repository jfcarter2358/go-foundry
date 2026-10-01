#version 430

// biome_cube_vert.glsl — point-light (cubemap) depth stage for the biome
// scatter. Outputs the placed world position for shadow_cube_frag.glsl's
// linear-distance depth. See biome_shadow_vert.glsl for the shared-placement
// and castRange notes.

layout(location = 0) in vec3  aLocal;
layout(location = 1) in vec3  aNormal;
layout(location = 2) in float aParam;

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

out vec3 fragWorldPos;

void main() {
    if (!biomePlace(gl_InstanceID, aLocal, aNormal, aParam)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        fragWorldPos = vec3(0.0);
        return;
    }
    // bWorldPos is chunk-local (see biome_vert.glsl's matching comment);
    // model carries it out to world space in one step.
    vec4 worldPos = model * vec4(bWorldPos, 1.0);
    fragWorldPos = worldPos.xyz;
    gl_Position = lightSpaceMatrix * worldPos;
}
