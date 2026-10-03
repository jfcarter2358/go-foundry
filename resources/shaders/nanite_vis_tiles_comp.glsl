#version 430

// nanite_vis_tiles_comp.glsl — which screen tiles each material bin has
// pixels in (render/nanite_vis.go), so the material pass draws a bin's tiles
// and not the whole screen.
//
// TILES_RESET: a thread a bin, its indirect draw emptied (six vertices, no
// instances). Otherwise a workgroup a 64x64 tile, a thread 4x4 pixels: the
// bins the tile holds are gathered into a bit set in shared memory, then
// every bin set appends the tile to its own list and counts it as one more
// instance of its draw — the draw's instance count is the list's length.

layout(std430, binding = 3) buffer BinDraws {
    uvec4 binDraws[]; // count, instance count, first, base instance
};

#ifdef TILES_RESET
layout(local_size_x = 64) in;
uniform uint binCount;
void main() {
    uint i = gl_GlobalInvocationID.x;
    if (i < binCount) {
        binDraws[i] = uvec4(6u, 0u, 0u, 0u);
    }
}
#else
layout(local_size_x = 16, local_size_y = 16) in;

layout(std430, binding = 6) writeonly buffer TileLists {
    uint tileList[];
};

uniform usampler2D bins;
uniform ivec2 size;
uniform uint binCount;
uniform uint tilesX;
uniform uint tileCap;

// MAX_BINS / 32 words: render.naniteVisMaxBins.
shared uint seen[MAX_BIN_WORDS];

void main() {
    uint lid = gl_LocalInvocationIndex;
    uint words = (binCount + 31u) / 32u;
    for (uint w = lid; w < words; w += 256u) {
        seen[w] = 0u;
    }
    barrier();
    ivec2 base = ivec2(gl_WorkGroupID.xy) * 64 + ivec2(gl_LocalInvocationID.xy) * 4;
    for (int y = 0; y < 4; y++) {
        for (int x = 0; x < 4; x++) {
            ivec2 p = base + ivec2(x, y);
            if (p.x >= size.x || p.y >= size.y) {
                continue;
            }
            uint b = texelFetch(bins, p, 0).r;
            if (b != 0u && b <= binCount) {
                atomicOr(seen[(b - 1u) >> 5], 1u << ((b - 1u) & 31u));
            }
        }
    }
    barrier();
    uint tile = gl_WorkGroupID.y * tilesX + gl_WorkGroupID.x;
    for (uint w = lid; w < words; w += 256u) {
        uint bits = seen[w];
        while (bits != 0u) {
            int bit = findLSB(bits);
            bits &= bits - 1u;
            uint bin = w * 32u + uint(bit);
            uint slot = atomicAdd(binDraws[bin].y, 1u);
            tileList[bin * tileCap + slot] = tile;
        }
    }
}
#endif
