#version 430

// nanite_vis_bindepth_frag.glsl — each visibility-buffer pixel's material bin
// as a DEPTH (render/nanite_vis.go): bin b (one more than its number) at
// b / 2^20, which a float depth holds exactly. The material pass then draws
// each bin's tiles at its own depth under GL_EQUAL, so the hardware's early
// depth test throws out every other bin's pixels before any shading runs.
// A pixel with no bin keeps the clear, 1.

uniform usampler2D bins;

void main() {
    uint b = texelFetch(bins, ivec2(gl_FragCoord.xy), 0).r;
    if (b == 0u) {
        discard;
    }
    gl_FragDepth = float(b) / 1048576.0;
}
