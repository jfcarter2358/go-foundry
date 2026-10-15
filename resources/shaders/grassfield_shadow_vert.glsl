#version 430

// grassfield_shadow_vert.glsl — the grass instancer's shadow depth stage
// (render/grass_field_shadow.go): the colour pass's own placement
// (grassfield_common.glsl), so a clump casts the clump on screen, sway and
// thinning included.

uniform mat4 lightSpaceMatrix;

out vec2 vUV;
out float vFade;

void main() {
    if (!grassPlace()) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vUV = vec2(0.0);
        vFade = 0.0;
        return;
    }
    vUV = aUV;
    vFade = gFade;
    gl_Position = lightSpaceMatrix * vec4(gWorldPos, 1.0);
}
