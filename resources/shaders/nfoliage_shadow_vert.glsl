#version 430

// nfoliage_shadow_vert.glsl — the Nanite foliage's triangles and parts into
// a sun's shadow map (render/nfoliage.go): nfoliage_vert.glsl's placement and
// bones, so a tree casts the shadow of the tree on screen, bent alike.

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in vec2 aUV;
layout(location = 3) in vec3 aParam;
layout(location = 5) in vec2 aBind;
// The reference: item, part (nfoliage_scatter_comp.glsl).
layout(location = 4) in uvec2 aRef;

uniform mat4 projView;

out vec2 vUV;
flat out int vPart;
out float vFade;

void main() {
    if (!nfBeginRef(aRef)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vUV = vec2(0.0);
        vPart = 0;
        vFade = 0.0;
        return;
    }
    vec3 p = aPos;
    int bone = int(aBind.x + 0.5);
    float w = aBind.y;
    if (aBind.x < 0.0) {
        int pi = (nFirst + int(nWhich)) * 4;
        vec4 right = texelFetch(nfParts, pi);
        vec4 upv = texelFetch(nfParts, pi + 1);
        vec4 base = texelFetch(nfParts, pi + 3);
        p = base.xyz + right.xyz * aPos.x + upv.xyz * aPos.y;
        bone = int(base.w + 0.5);
        w = mix(right.w, upv.w, aBind.y);
    }
    vec4 r0, r1, r2;
    nfSkinMatrix(false, bone, w, r0, r1, r2);
    vUV = aUV;
    vPart = int(aParam.y + 0.5);
    vFade = nFade;
    gl_Position = projView * vec4(nfToWorld(nfApply(r0, r1, r2, p)), 1.0);
}
