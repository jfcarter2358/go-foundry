#version 410

// detail_scatter_vert.glsl — color-pass vertex stage for scattered rock
// instances. Emits the varyings fragment.glsl consumes so scattered rocks get
// full lighting + shadow receiving. Placement comes from the injected
// detail_scatter_common.glsl.

layout(location = 0) in vec3 aLocal;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in float aParam;

uniform mat4 projection;
uniform mat4 camera;

#define MAX_DIR_LIGHTS 4
uniform int  numDirLights;
uniform mat4 lightSpaceMatrix[MAX_DIR_LIGHTS];

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec4 fragPosLightSpace[MAX_DIR_LIGHTS];

void main() {
    vec3 w;
    if (!scatterPlace(gl_InstanceID, aLocal, w)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0); // collapse off-screen; clipped away
        fragTexCoord = vec2(0.0);
        fragWorldPos = vec3(0.0);
        return;
    }
    fragTexCoord = aLocal.xz * 0.5 + 0.5;
    fragWorldPos = w;
    for (int i = 0; i < numDirLights; i++) {
        fragPosLightSpace[i] = lightSpaceMatrix[i] * vec4(w, 1.0);
    }
    gl_Position = projection * camera * vec4(w, 1.0);
}
