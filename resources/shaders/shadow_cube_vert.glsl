#version 410

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

layout(location = 0) in vec3 vert;

out vec3 fragWorldPos;

void main() {
    vec4 worldPos = model * vec4(vert, 1.0);
    fragWorldPos = worldPos.xyz;
    gl_Position = lightSpaceMatrix * worldPos;
}
