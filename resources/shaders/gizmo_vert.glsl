#version 410
uniform mat4 projection;
uniform mat4 view;
in vec3 vert;
in vec3 vertColor;
out vec3 fragColor;
void main() {
    fragColor   = vertColor;
    gl_Position = projection * view * vec4(vert, 1.0);
}
