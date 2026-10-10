#version 430

// nanite_merge_comp.glsl — joins runs of neighbouring surviving clusters into
// one draw (render/nanite_cull.go), between the cluster cull and the packing.
//
// A cluster is about a hundred triangles, and a draw command per cluster is a
// lot of commands: a field of dense rocks in full view is a hundred thousand
// of them, and a GPU's front end walks indirect draws at a few tens of
// millions a second — the draws, not the triangles, become the cost. But the
// slots of a tile are consecutive clusters of one instance, laid out one after
// another in the index buffer, so a run of survivors is one contiguous range
// of indices: the first command of the run takes the whole run's count, and
// the rest of it are emptied. A mesh in full view comes out as a command per
// tile, not per cluster.
//
// One workgroup per tile. It also overwrites the tile's survivor count with
// the number of commands left, which is what the packing's scan reads.

layout(local_size_x = 64) in;

struct Tile {
    uvec4 a;
    uvec4 b;
};

layout(std430, binding = 3) readonly buffer Tiles {
    Tile tiles[];
};
layout(std430, binding = 5) buffer Commands {
    uint cmds[];
};
layout(std430, binding = 9) writeonly buffer TileCounts {
    uint tileCounts[];
};
// The cluster tiles, by number: what this pass is dispatched over (a range
// tile's slots are runs already, which its own stage counted).
layout(std430, binding = 13) readonly buffer KindTiles {
    uint kindTiles[];
};

uniform uint workBase;
// mergeMax, when not 0, is the most clusters one command may take: a run is
// cut into commands of at most that many. The visibility buffer's camera
// passes set it (render/nanite_vis.go), since an ID numbers a command's
// triangles in 12 bits.
uniform uint mergeMax;

// A tile holds at most naniteTileClusters (256) slots.
shared uint live[256];
shared uint cnt[256];
shared uint heads;

void main() {
    uint lid = gl_LocalInvocationID.x;
    uint ti = kindTiles[workBase + gl_WorkGroupID.x];
    Tile t = tiles[ti];
    uint n = min(t.a.y, 256u);
    if (lid == 0u) {
        heads = 0u;
    }
    for (uint k = lid; k < n; k += 64u) {
        uint o = (t.a.w + k) * 5u;
        live[k] = cmds[o + 1u];
        cnt[k] = cmds[o];
    }
    barrier();
    for (uint k = lid; k < n; k += 64u) {
        if (live[k] == 0u) {
            continue;
        }
        uint o = (t.a.w + k) * 5u;
        // Where this cluster's run starts, and so whether it heads a command:
        // the run's first, or (capped) every mergeMax-th of it.
        uint start = k;
        while (start > 0u && live[start - 1u] != 0u) {
            start--;
        }
        bool head = mergeMax == 0u ? start == k : (k - start) % mergeMax == 0u;
        if (!head) {
            // Inside a run: its triangles are drawn by the run's head.
            cmds[o] = 0u;
            cmds[o + 1u] = 0u;
            continue;
        }
        uint total = cnt[k];
        uint end = mergeMax == 0u ? n : min(n, k + mergeMax);
        for (uint e = k + 1u; e < end && live[e] != 0u; e++) {
            total += cnt[e];
        }
        cmds[o] = total;
        atomicAdd(heads, 1u);
    }
    barrier();
    if (lid == 0u) {
        tileCounts[ti] = heads;
    }
}
