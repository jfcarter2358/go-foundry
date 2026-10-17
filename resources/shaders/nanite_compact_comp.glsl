// nanite_compact_comp.glsl — packs a pass's surviving indirect commands to
// the front of each group's run, in order, and counts them, for
// glMultiDrawElementsIndirectCount (render/nanite_cull.go). Only run where the
// driver has that draw (GL 4.6 or ARB_indirect_parameters): without it the
// fixed list, culled commands and all, is drawn as it stands. The #version
// line, and COMPACT_SCAN or COMPACT_SCATTER, are the Go side's.
//
// The dispatches, all parallel:
//
//   COMPACT_BLOCKS  a workgroup per 256 tiles: the exclusive prefix sum of
//                   their survivor counts (the cull wrote them) within the
//                   block, and the block's total.
//   COMPACT_BLOCKSCAN  one workgroup: the exclusive prefix sum of the
//                   blocks' totals, a 256 of them at a time.
//   COMPACT_FINISH  a thread per tile: its place in its GROUP — its prefix
//                   over every tile less its group's first tile's (the tiles
//                   of a group are consecutive, tile.b.w names the group) —
//                   and, a thread per group, the group's count.
//   COMPACT_SCATTER one workgroup per cluster tile: a prefix sum over its own
//                   slots, writing each survivor at the group's start + the
//                   tile's place + its own.
//   COMPACT_SCATTER_RANGE  one thread per range tile, the same for its few
//                   slots.
//
// (Phase 5: the prefix sum was one workgroup per group, walking its tiles
// 64 at a time — a field of rocks is one group of forty thousand tiles, and
// that walk, on one multiprocessor, cost a millisecond. Docs/nanite/
// PHASE_5.md.)
//
// The order survives the packing — a cluster that drew after another still
// does — so a capture stays the same from run to run and a coplanar tie
// resolves the way it did before.

#if defined(COMPACT_BLOCKS) || defined(COMPACT_BLOCKSCAN)
layout(local_size_x = 256) in;
#else
layout(local_size_x = 64) in;
#endif

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
// The tiles of each kind, by number (render/nanite_cull.go): the cluster
// scatter is dispatched over the cluster tiles, the range scatter over the
// range tiles.
layout(std430, binding = 13) readonly buffer KindTiles {
    uint kindTiles[];
};

// The scan's own: each tile's prefix within its block, the blocks' totals
// (made exclusive prefixes by COMPACT_BLOCKSCAN), and the tile count.
layout(std430, binding = 11) buffer TilePrefix {
    uint tilePrefix[];
};
layout(std430, binding = 12) buffer BlockPrefix {
    uint blockPrefix[];
};

uniform uint workBase;
uniform uint kindTileCount;
// liveList: the kind list bound at 13 is this pass's live range tiles,
// liveCount long (render/nanite_live_tiles.go; the range scatter only).
uniform int liveList;
layout(std430, binding = 18) readonly buffer LiveCount {
    uint liveCount;
};
uniform uint tileCount;
uniform uint blockCount;
uniform uint groupCount;

shared uint scan[256];

// scanBlock turns scan[0..256) into its inclusive prefix sum (256 threads).
void scanBlock(uint lid) {
    for (uint off = 1u; off < 256u; off <<= 1u) {
        uint v = lid >= off ? scan[lid - off] : 0u;
        barrier();
        scan[lid] += v;
        barrier();
    }
}

// prefixOf is tile i's exclusive prefix over every tile (i == tileCount: the
// total).
uint prefixOf(uint i) {
    if (i >= tileCount) {
        return blockPrefix[blockCount];
    }
    return tilePrefix[i] + blockPrefix[i / 256u];
}

// scanStep turns scan[0..64) into its inclusive prefix sum (64 threads).
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
#if defined(COMPACT_BLOCKS)
    uint b = workBase + gl_WorkGroupID.x;
    uint i = b * 256u + lid;
    uint n = i < tileCount ? tileCounts[i] : 0u;
    scan[lid] = n;
    barrier();
    scanBlock(lid);
    if (i < tileCount) {
        tilePrefix[i] = scan[lid] - n;
    }
    if (lid == 255u) {
        blockPrefix[b] = scan[255];
    }
#elif defined(COMPACT_BLOCKSCAN)
    uint total = 0u;
    for (uint base = 0u; base < blockCount; base += 256u) {
        uint i = base + lid;
        uint n = i < blockCount ? blockPrefix[i] : 0u;
        scan[lid] = n;
        barrier();
        scanBlock(lid);
        if (i < blockCount) {
            blockPrefix[i] = total + scan[lid] - n;
        }
        total += scan[255];
        barrier();
    }
    if (lid == 0u) {
        blockPrefix[blockCount] = total;
    }
#elif defined(COMPACT_FINISH)
    uint i = workBase + gl_GlobalInvocationID.x;
    if (i < tileCount) {
        tileOffsets[i] = prefixOf(i) - prefixOf(groups[tiles[i].b.w].z);
    }
    if (i < groupCount) {
        uvec4 g = groups[i];
        counts[i] = prefixOf(g.z + g.w) - prefixOf(g.z);
    }
#elif defined(COMPACT_SCATTER_RANGE)
    // A thread a range tile: its RANGE_SLOTS_COMPACT slots, in order.
    uint n = workBase + gl_GlobalInvocationID.x;
    if (n >= (liveList != 0 ? liveCount : kindTileCount)) {
        return;
    }
    uint ti = kindTiles[n];
    Tile t = tiles[ti];
    uint written = tileOffsets[ti];
    for (uint r = 0u; r < RANGE_SLOTS_COMPACT; r++) {
        uint src = (t.a.w + r) * 5u;
        if (cmds[src + 1u] == 0u) {
            continue;
        }
        uint dst = (t.b.y + written) * 5u;
        outCmds[dst] = cmds[src];
        outCmds[dst + 1u] = cmds[src + 1u];
        outCmds[dst + 2u] = cmds[src + 2u];
        outCmds[dst + 3u] = cmds[src + 3u];
        outCmds[dst + 4u] = cmds[src + 4u];
        written++;
    }
#else
    uint ti = kindTiles[workBase + gl_WorkGroupID.x];
    Tile t = tiles[ti];
    uint written = tileOffsets[ti];
    uint slots = t.a.y;
    for (uint base = 0u; base < slots; base += 64u) {
        uint i = base + lid;
        bool live = i < slots && cmds[(t.a.w + i) * 5u + 1u] != 0u;
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
