#version 430

// taa_output_frag.glsl — the scene target's picture into the caller's
// (render/taa.go): the resolved history, or the frame itself with TAA off,
// sharpened. The history is a little soft — every frame it is resampled
// through a filter — and an unsharp mask of the TAA sharpen setting
// (gfx.TAASharpen) gives that back, its result held inside the five taps'
// own range so it can brighten an edge but never ring past it.
//
// In an HDR frame (gfx/hdr.go: a level's `tonemap:` block) the scene target
// holds light, not screen colour, and this is where it becomes screen colour:
// each tap is scaled by the exposure and mapped through the ACES fit
// (gfx.ACESFilm) before the sharpen, which works on what is shown.

in vec2 vUV;

uniform sampler2D src;
uniform float sharpen;
uniform ivec2 size;
// The caller's viewport origin: gl_FragCoord counts from the framebuffer's
// corner, the source from the viewport's.
uniform ivec2 origin;
// 1: map through exposure and the ACES fit (an HDR frame); 0: as drawn.
uniform int tonemap;
uniform float exposure;
// 1 (the editor's view, render/editor_scene_frame.go): the scene's depth is
// written into the caller's depth buffer too, for the overlays drawn after.
uniform int writeDepth;
uniform sampler2D depthSrc;

out vec4 outputColor;

vec3 shown(ivec2 q) {
    vec3 c = texelFetch(src, q, 0).rgb;
    if (tonemap == 0) {
        return c;
    }
    vec3 x = max(c * exposure, vec3(0.0));
    return clamp((x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14), 0.0, 1.0);
}

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy) - origin;
    ivec2 last = size - 1;
    gl_FragDepth = writeDepth == 1 ? texelFetch(depthSrc, p, 0).r : gl_FragCoord.z;
    vec3 c = shown(p);
    if (sharpen <= 0.0) {
        outputColor = vec4(c, 1.0);
        return;
    }
    vec3 n = shown(clamp(p + ivec2(0, 1), ivec2(0), last));
    vec3 s = shown(clamp(p - ivec2(0, 1), ivec2(0), last));
    vec3 e = shown(clamp(p + ivec2(1, 0), ivec2(0), last));
    vec3 w = shown(clamp(p - ivec2(1, 0), ivec2(0), last));
    vec3 lo = min(c, min(min(n, s), min(e, w)));
    vec3 hi = max(c, max(max(n, s), max(e, w)));
    vec3 sharp = c + sharpen * (c - 0.25 * (n + s + e + w));
    outputColor = vec4(clamp(sharp, lo, hi), 1.0);
}
