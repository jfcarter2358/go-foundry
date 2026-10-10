#version 430

// foliage_shadow_vert.glsl — cascade depth stage for terrain foliage.
// Placement is foliage_common.glsl, the colour pass's own, so the shadow
// sways with the tree.

uniform mat4 lightSpaceMatrix;

out vec2 vUV;
flat out int vPart;
out float vFade;

void main() {
    if (!foliagePlace()) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vUV = vec2(0.0);
        vPart = 0;
        vFade = 0.0;
        return;
    }
    vUV = aUV;
    vPart = int(aParam.y + 0.5);
    vFade = fFade;
    gl_Position = lightSpaceMatrix * vec4(fWorldPos, 1.0);
}
