#version 430

// nanite_vis_classify_frag.glsl — each visibility-buffer pixel's material bin
// (render/nanite_vis.go). Drawn full screen under a stencil test that passes
// only where the ID pass's triangle is still the nearest thing — a forward
// draw over it (an entity, a moved brush, the terrain) set the stencil back
// to 0 — so a pixel some other pass now owns is never shaded twice.
//
// Writes one more than the bin (0: no bin), which the tile pass gathers and
// the material pass tests its own against.

uniform usampler2D visIDs;

struct Record {
    uvec4 a;
    uvec4 b;
};
layout(std430, binding = 1) readonly buffer Records {
    Record records[];
};

out uint outBin;

void main() {
    uint id = texelFetch(visIDs, ivec2(gl_FragCoord.xy), 0).r;
    if (id == 0u) {
        discard;
    }
    outBin = records[(id >> 12) - 1u].a.w + 1u;
}
