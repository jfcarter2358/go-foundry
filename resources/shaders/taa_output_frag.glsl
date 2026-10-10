#version 430

// taa_output_frag.glsl — the scene target's picture into the caller's
// (render/taa.go): the resolved history, or the frame itself with TAA off,
// sharpened. The history is a little soft — every frame it is resampled
// through a filter — and an unsharp mask of the TAA sharpen setting
// (gfx.TAASharpen) gives that back, its result held inside the five taps'
// own range so it can brighten an edge but never ring past it.

in vec2 vUV;

uniform sampler2D src;
uniform float sharpen;
uniform ivec2 size;
// The caller's viewport origin: gl_FragCoord counts from the framebuffer's
// corner, the source from the viewport's.
uniform ivec2 origin;

out vec4 outputColor;

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy) - origin;
    ivec2 last = size - 1;
    vec3 c = texelFetch(src, p, 0).rgb;
    if (sharpen <= 0.0) {
        outputColor = vec4(c, 1.0);
        return;
    }
    vec3 n = texelFetch(src, clamp(p + ivec2(0, 1), ivec2(0), last), 0).rgb;
    vec3 s = texelFetch(src, clamp(p - ivec2(0, 1), ivec2(0), last), 0).rgb;
    vec3 e = texelFetch(src, clamp(p + ivec2(1, 0), ivec2(0), last), 0).rgb;
    vec3 w = texelFetch(src, clamp(p - ivec2(1, 0), ivec2(0), last), 0).rgb;
    vec3 lo = min(c, min(min(n, s), min(e, w)));
    vec3 hi = max(c, max(max(n, s), max(e, w)));
    vec3 sharp = c + sharpen * (c - 0.25 * (n + s + e + w));
    outputColor = vec4(clamp(sharp, lo, hi), 1.0);
}
