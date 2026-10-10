#version 430

// foliage_imp_bake_vert.glsl — one view of a foliage impostor
// (render/foliage_impostor.go): a template mesh, in the plant's own frame at
// unit scale, through the view's orthographic projection. No instance, no
// wind: the impostor is the plant standing still.

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in vec2 aUV;
layout(location = 3) in vec3 aParam;

uniform mat4 viewProj;

out vec3 vNormal;
out vec2 vUV;
flat out int vPart;
out float vTone;

void main() {
    vNormal = aNormal;
    vUV = aUV;
    vPart = int(aParam.y + 0.5);
    vTone = aParam.z;
    gl_Position = viewProj * vec4(aPos, 1.0);
}
