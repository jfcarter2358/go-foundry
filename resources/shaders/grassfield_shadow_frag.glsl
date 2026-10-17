#version 430

// grassfield_shadow_frag.glsl — shadow depth for the grass instancer: a blade
// card casts the blade, not its quad.

in vec2 vUV;
in float vFade;

uniform sampler2D grassTex;

out vec4 fragColor;

void main() {
    if (vFade < 0.5 || texture(grassTex, vUV).a < 0.5) {
        discard;
    }
    fragColor = vec4(1.0);
}
