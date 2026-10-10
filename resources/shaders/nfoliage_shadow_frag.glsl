#version 430

// nfoliage_shadow_frag.glsl — a Nanite foliage card's shadow is its leaf's
// shape, not its quad (foliage_shadow_frag.glsl's rule).

in vec2 vUV;
flat in int vPart;
in float vFade;

uniform sampler2D atlas;

out vec4 fragColor;

void main() {
    if (vFade < 0.5) {
        discard;
    }
    if ((vPart == 1 || vPart == 3) && texture(atlas, vUV).a < 0.5) {
        discard;
    }
    fragColor = vec4(1.0);
}
