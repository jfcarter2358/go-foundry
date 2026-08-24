#version 410

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

layout(location = 0) in vec3 vert;

void main() {
    // Skinned here too, or an animated entity would cast the silhouette of its
    // rest pose while the mesh moved — see skin_common.glsl.
    gl_Position = lightSpaceMatrix * model * skinPosition(vert);
}
