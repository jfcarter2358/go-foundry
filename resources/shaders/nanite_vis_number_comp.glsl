#version 430

// nanite_vis_number_comp.glsl — numbers a cull pass's commands for the
// visibility buffer (render/nanite_vis.go), after the cull, the merge and the
// packing, before the ID pass draws them.
//
// A thread a command slot. Each command of a group the visibility buffer
// draws gets the next record of the frame's list — where its triangles start
// in its index buffer, its base vertex, the instance it draws, and the
// material bin its group shades in — and its base instance becomes that
// record's number. The ID pass reads the record through an instanced vertex
// attribute (so base instance picks it), and the material pass reads it back
// from the number the ID pass wrote. A group drawn the forward way (water, a
// blend: what the material pass does not shade) keeps its command as it was.

layout(local_size_x = 64) in;

layout(std430, binding = 5) buffer Commands {
    uint cmds[];
};
struct Record {
    uvec4 a; // first index, base vertex, instance, bin
    uvec4 b; // the record's number + 1, index count
};
layout(std430, binding = 1) writeonly buffer Records {
    Record records[];
};
// Each group's first slot, the slots numbered from it (a packed pass with a
// bound, cullBatch.drawMax: no more than its bound), whether the visibility
// buffer draws it, and its first record less recordBase — its first slot
// when every slot is numbered, else the bounds of the groups before it, so
// a pass takes only as many records as it can have commands.
layout(std430, binding = 2) readonly buffer Groups {
    uvec4 groups[];
};

uniform uint slots;
uniform uint groupCount;
uniform uint recordBase;
uniform uint binBase;

// DENSE numbering (dense = 1; render/nanite_vis.go, docs/canyon/PERF.md):
// only a command that draws takes a record, the next of the frame's from
// one counter every pass shares, instead of every slot its pass might have
// had a record by. A view of thousands of placements reserved more slots
// than an ID can name long before it drew that many commands, and every
// pass after the 2^20th went the forward way, without the compute
// rasteriser. A command past maxRecords (a frame that really draws that
// many) is dropped rather than numbered wrong. A packed pass's group holds
// counts[group] commands; the slots after them are last pass's leftovers.
layout(std430, binding = 3) coherent buffer VisNext {
    uint visNext;
};
layout(std430, binding = 4) readonly buffer Counts {
    uint counts[];
};
uniform uint dense;
uniform uint packedCounts;
uniform uint maxRecords;

void main() {
    uint i = gl_GlobalInvocationID.x;
    if (i >= slots || groupCount == 0u) {
        return;
    }
    uint lo = 0u, hi = groupCount;
    while (hi - lo > 1u) {
        uint mid = (lo + hi) / 2u;
        if (groups[mid].x <= i) {
            lo = mid;
        } else {
            hi = mid;
        }
    }
    uvec4 g = groups[lo];
    if (i < g.x || i >= g.x + g.y || g.z == 0u) {
        return;
    }
    uint o = i * 5u;
    uint r = recordBase + g.w + (i - g.x);
    if (dense != 0u) {
        if (packedCounts != 0u && i - g.x >= counts[lo]) {
            return;
        }
        if (cmds[o] == 0u || cmds[o + 1u] == 0u) {
            return; // culled: drawn as nothing, numbered as nothing
        }
        r = atomicAdd(visNext, 1u);
        if (r >= maxRecords) {
            cmds[o + 1u] = 0u;
            return;
        }
    }
    records[r].a = uvec4(cmds[o + 2u], cmds[o + 3u], cmds[o + 4u], binBase + lo);
    records[r].b = uvec4(r + 1u, cmds[o], 0u, 0u);
    cmds[o + 4u] = r;
}
