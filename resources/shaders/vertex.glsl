#version 410

uniform mat4 projection;
uniform mat4 camera;
uniform mat4 model;

#define MAX_DIR_LIGHTS 4
uniform int  numDirLights;
uniform mat4 lightSpaceMatrix[MAX_DIR_LIGHTS];

in vec3 vert;
in vec2 vertTexCoord;

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec4 fragPosLightSpace[MAX_DIR_LIGHTS];

void main() {
    fragTexCoord = vertTexCoord;
    vec4 worldPos = model * vec4(vert, 1.0);
    fragWorldPos = worldPos.xyz;

    for (int i = 0; i < numDirLights; i++) {
        fragPosLightSpace[i] = lightSpaceMatrix[i] * worldPos;
    }

    gl_Position = projection * camera * worldPos;
}
