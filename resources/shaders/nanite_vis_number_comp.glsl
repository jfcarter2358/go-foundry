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
// Each group's first slot, slot count, and whether the visibility buffer
// draws it.
layout(std430, binding = 2) readonly buffer Groups {
    uvec4 groups[];
};

uniform uint slots;
uniform uint groupCount;
uniform uint recordBase;
uniform uint binBase;

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
    uint r = recordBase + i;
    records[r].a = uvec4(cmds[o + 2u], cmds[o + 3u], cmds[o + 4u], binBase + lo);
    records[r].b = uvec4(r + 1u, cmds[o], 0u, 0u);
    cmds[o + 4u] = r;
}
