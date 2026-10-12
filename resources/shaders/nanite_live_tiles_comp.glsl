// nanite_live_tiles_comp.glsl — the GPU-driven cull's live-tile list
// (render/nanite_live_tiles.go): the range tiles of only the instances the
// instance stage kept, so the range cull and the packing's range scatter are
// dispatched over what can draw rather than over every tile in the level.
//
// Why: a range tile's thread costs its launch whatever it then does — the
// range cull holds two 16-entry arrays and runs at low occupancy, so a thread
// that only learns its instance was culled costs nearly what a drawing one
// does. A level of a thousand hero cliffs is 287,000 range tiles; on the dev
// box a pass over them cost ~45 ms with every one of them out of view
// (docs/canyon/PHASE_3.md). The tiles are filed by RUN — a run is one
// instance's consecutive range tiles within one group — so the list is built
// a thread a run, not a thread a tile.
//
//   LIVE_LIST  a thread per run: an instance the instance stage kept appends
//              its run's tiles to the list (an atomic add reserves the
//              space; the order varies, which nothing downstream minds —
//              every tile writes its own fixed command slots). In pass 2 a
//              culled run whose clusters were seen since it was last
//              cleared has its history zeroed, once: the range cull would
//              have written "not seen" for every one of them, and now never
//              runs for it.
//   LIVE_ARGS  one thread: the indirect dispatch's workgroups, 64 tiles
//              each, from the list's length.
//
// A tile left out writes nothing, so its command slots and survivor count
// keep whatever the last pass to run it wrote. The list clears them when its
// run goes from run to left out (runLive): every later reader — a shadow
// view drawing the fixed list, the packing's prefix sum over every tile —
// then sees what the full dispatch would have left there, a slot that draws
// nothing. The cost is a run's going out of view, not every pass.

layout(local_size_x = 64) in;

struct Tile {
    uvec4 a;
    uvec4 b;
};

layout(std430, binding = 1) readonly buffer InstanceVisible {
    uint instVis[];
};
layout(std430, binding = 3) readonly buffer Tiles {
    Tile tiles[];
};
layout(std430, binding = 4) buffer History {
    uint history[];
};
layout(std430, binding = 13) readonly buffer KindTiles {
    uint kindTiles[];
};
// The list's length, then the dispatch's workgroups (x, y, z).
layout(std430, binding = 18) buffer LiveCount {
    uint liveCount;
    uint groupsX;
    uint groupsY;
    uint groupsZ;
};
layout(std430, binding = 19) writeonly buffer LiveTiles {
    uint liveTiles[];
};
// A run: first kind-list index, tile count, instance, unused.
layout(std430, binding = 20) readonly buffer Runs {
    uvec4 runs[];
};
// Per run: its clusters have history set since it was last cleared.
layout(std430, binding = 21) buffer RunSeen {
    uint runSeen[];
};
// Per run: its slots and counts hold what the range cull last wrote (not
// cleared since).
layout(std430, binding = 22) buffer RunLive {
    uint runLive[];
};
layout(std430, binding = 5) buffer Commands {
    uint cmds[];
};
layout(std430, binding = 9) buffer TileCounts {
    uint tileCounts[];
};

uniform uint runCount;
uniform uint workBase;
uniform int mode; // the cull's mode: 2 is CULL_PASS2

void main() {
#if defined(LIVE_ARGS)
    if (gl_GlobalInvocationID.x == 0u) {
        groupsX = (liveCount + 63u) / 64u;
        groupsY = 1u;
        groupsZ = 1u;
    }
#else
    uint r = workBase + gl_GlobalInvocationID.x;
    if (r >= runCount) {
        return;
    }
    uvec4 run = runs[r];
    if (instVis[run.z] != 0u) {
        uint base = atomicAdd(liveCount, run.y);
        for (uint k = 0u; k < run.y; k++) {
            liveTiles[base + k] = kindTiles[run.x + k];
        }
        runLive[r] = 1u;
        if (mode == 2) {
            runSeen[r] = 1u;
        }
        return;
    }
    if (runLive[r] != 0u) {
        for (uint k = 0u; k < run.y; k++) {
            uint ti = kindTiles[run.x + k];
            uint slot = tiles[ti].a.w;
            for (uint c = 0u; c < RANGE_SLOTS * 5u; c++) {
                cmds[slot * 5u + c] = 0u;
            }
            tileCounts[ti] = 0u;
        }
        runLive[r] = 0u;
    }
    if (mode == 2 && runSeen[r] != 0u) {
        for (uint k = 0u; k < run.y; k++) {
            Tile t = tiles[kindTiles[run.x + k]];
            uint n = min(t.a.y, 32u);
            for (uint c = 0u; c < n; c++) {
                history[t.b.x + c] = 0u;
            }
        }
        runSeen[r] = 0u;
    }
#endif
}
