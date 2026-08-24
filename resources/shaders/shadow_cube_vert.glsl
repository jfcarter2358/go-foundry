#version 410

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

layout(location = 0) in vec3 vert;

out vec3 fragWorldPos;

void main() {
    vec4 worldPos = model * skinPosition(vert);
    fragWorldPos = worldPos.xyz;
    gl_Position = lightSpaceMatrix * worldPos;
}
