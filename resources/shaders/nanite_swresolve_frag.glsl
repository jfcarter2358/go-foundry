// nanite_swresolve_frag.glsl — the compute rasteriser's resolve (render/
// nanite_swraster.go; Phase 5). The #version line, and SW64, are the Go side's.
//
// Drawn over the screen box the rasteriser wrote in (nanite_swresolve_
// vert.glsl), into the scene target's visibility-ID attachment right after
// it, with the depth test on and the stencil set to 1 where it passes — the
// ID pass's own rule (nanite_vis.go) — so a pixel the rasteriser covered
// nearer than anything the hardware drew becomes the visibility buffer's,
// depth and all, and the Hi-Z build and every pass after see it as they see
// the hardware's.
//
// Every pixel's word is cleared as it is read, which is what readies the
// buffer for the next batch: no separate clear pass.

#ifdef SW64
layout(std430, binding = 5) coherent buffer VisSW {
    uint64_t visSW[];
};
#else
layout(std430, binding = 5) coherent buffer VisDepth {
    uint visDepth[];
};
layout(std430, binding = 6) coherent buffer VisIDs {
    uint visIDs[];
};
#endif

uniform ivec2 viewSize;

layout(location = 0) out uint visID;

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    uint i = uint(p.y) * uint(viewSize.x) + uint(p.x);
#ifdef SW64
    uint64_t v = visSW[i];
    if (v == uint64_t(0)) {
        discard;
    }
    visSW[i] = uint64_t(0);
    uint key = uint(v >> 32);
    uint id = uint(v & uint64_t(0xffffffffu));
#else
    uint key = visDepth[i];
    if (key == 0u) {
        discard;
    }
    uint id = visIDs[i];
    visDepth[i] = 0u;
    visIDs[i] = 0u;
#endif
    gl_FragDepth = uintBitsToFloat(0xffffffffu - key);
    visID = id;
}
