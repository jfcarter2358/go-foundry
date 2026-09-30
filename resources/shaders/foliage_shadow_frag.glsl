#version 410

// foliage_shadow_frag.glsl — cascade depth for terrain foliage: a leaf card
// casts the leaf's shape, not its quad.

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
