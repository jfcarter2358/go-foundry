#version 430

// taa_object_frag.glsl — see taa_object_vert.glsl.

in vec4 vNow;
in vec4 vThen;

out vec2 outMotion;

void main() {
    outMotion = (vNow.xy / vNow.w - vThen.xy / vThen.w) * 0.5;
}
