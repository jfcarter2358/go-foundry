// nanite_compact_comp.glsl — packs a pass's surviving indirect commands to
// the front of each group's run, in order, and counts them, for
// glMultiDrawElementsIndirectCount (render/nanite_cull.go). Only run where the
// driver has that draw (GL 4.6 or ARB_indirect_parameters): without it the
// fixed list, culled commands and all, is drawn as it stands. The #version
// line, and COMPACT_SCAN or COMPACT_SCATTER, are the Go side's.
//
// Two dispatches, both parallel:
//
//   COMPACT_SCAN    one workgroup per group: an exclusive prefix sum over its
//                   tiles' survivor counts (the cull wrote them), giving each
//                   tile its place in the group, and the group's count.
//   COMPACT_SCATTER one workgroup per tile: a prefix sum over its own slots,
//                   writing each survivor at the group's start + the tile's
//                   place + its own.
//
// The order survives the packing — a cluster that drew after another still
// does — so a capture stays the same from run to run and a coplanar tie
// resolves the way it did before.

layout(local_size_x = 64) in;

struct Tile {
    uvec4 a;
    uvec4 b;
};

layout(std430, binding = 3) readonly buffer Tiles {
    Tile tiles[];
};
// The cull's output: a command per slot, a culled one with no instances.
layout(std430, binding = 5) readonly buffer Commands {
    uint cmds[];
};
layout(std430, binding = 6) writeonly buffer Packed {
    uint outCmds[];
};
layout(std430, binding = 7) buffer Counts {
    uint counts[];
};
// Per group: first slot, slot count, first tile, tile count.
layout(std430, binding = 8) readonly buffer Groups {
    uvec4 groups[];
};
layout(std430, binding = 9) readonly buffer TileCounts {
    uint tileCounts[];
};
layout(std430, binding = 10) buffer TileOffsets {
    uint tileOffsets[];
};

uniform uint workBase;

shared uint scan[64];

// scanStep turns scan[] into its inclusive prefix sum.
void scanStep(uint lid) {
    for (uint off = 1u; off < 64u; off <<= 1u) {
        uint v = lid >= off ? scan[lid - off] : 0u;
        barrier();
        scan[lid] += v;
        barrier();
    }
}

void main() {
    uint lid = gl_LocalInvocationID.x;
#ifdef COMPACT_SCAN
    uvec4 g = groups[workBase + gl_WorkGroupID.x];
    uint total = 0u;
    for (uint base = 0u; base < g.w; base += 64u) {
        uint i = base + lid;
        uint n = i < g.w ? tileCounts[g.z + i] : 0u;
        scan[lid] = n;
        barrier();
        scanStep(lid);
        if (i < g.w) {
            tileOffsets[g.z + i] = total + scan[lid] - n;
        }
        total += scan[63];
        barrier();
    }
    if (lid == 0u) {
        counts[workBase + gl_WorkGroupID.x] = total;
    }
#else
    uint ti = workBase + gl_WorkGroupID.x;
    Tile t = tiles[ti];
    uint written = tileOffsets[ti];
    for (uint base = 0u; base < t.a.y; base += 64u) {
        uint i = base + lid;
        bool live = i < t.a.y && cmds[(t.a.w + i) * 5u + 1u] != 0u;
        scan[lid] = live ? 1u : 0u;
        barrier();
        scanStep(lid);
        if (live) {
            uint dst = (t.b.y + written + scan[lid] - 1u) * 5u;
            uint src = (t.a.w + i) * 5u;
            outCmds[dst] = cmds[src];
            outCmds[dst + 1u] = cmds[src + 1u];
            outCmds[dst + 2u] = cmds[src + 2u];
            outCmds[dst + 3u] = cmds[src + 3u];
            outCmds[dst + 4u] = cmds[src + 4u];
        }
        written += scan[63];
        barrier();
    }
#endif
}
