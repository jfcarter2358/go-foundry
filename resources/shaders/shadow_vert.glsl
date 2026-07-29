#version 410

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

layout(location = 0) in vec3 vert;

void main() {
    gl_Position = lightSpaceMatrix * model * vec4(vert, 1.0);
}
