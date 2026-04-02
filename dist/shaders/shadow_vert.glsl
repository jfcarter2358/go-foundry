#version 410

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

in vec3 vert;

void main() {
    gl_Position = lightSpaceMatrix * model * vec4(vert, 1.0);
}
