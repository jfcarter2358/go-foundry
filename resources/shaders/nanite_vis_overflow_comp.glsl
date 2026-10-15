#version 430

// nanite_vis_overflow_comp.glsl — the visibility buffer's overflow, handed to
// the forward path (render/nanite_vis_overflow.go; docs/regions/PRESETS_4.md).
//
// An ID names one of 2^20 records. With dense numbering
// (nanite_vis_number_comp.glsl) a command that comes after the last record
// is not numbered: its instance count is set to 0, so the ID pass skips it —
// and nothing else drew it, so it was a HOLE in the picture, the colour
// target showing through white. The biome showcase's overview from 600 m
// draws ~1.2 M commands in a frame and lost a tenth of them.
//
// Run after the ID draws on a pass whose frame is over (or near) the limit,
// this turns the pass's commands round for one more draw, the forward way:
// a command the numbering refused (count drawn, instance count 0, its base
// instance still the instance — the numbering returns before it writes the
// record there) draws, and one it numbered — already in the visibility
// buffer — does not. Groups the visibility buffer does not draw (water, a
// blend: g.z == 0) were never numbered and are left as they are; a packed
// pass's slots past its group's count are last pass's leftovers, skipped as
// the numbering skips them.

layout(local_size_x = 64) in;

layout(std430, binding = 5) buffer Commands {
    uint cmds[];
};
// Each group's first slot, the slots it numbers, whether the visibility
// buffer draws it, its first record (the numbering's table).
layout(std430, binding = 2) readonly buffer Groups {
    uvec4 groups[];
};
layout(std430, binding = 4) readonly buffer Counts {
    uint counts[];
};

uniform uint slots;
uniform uint groupCount;
uniform uint packedCounts;

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
    if (packedCounts != 0u && i - g.x >= counts[lo]) {
        return;
    }
    uint o = i * 5u;
    if (cmds[o] == 0u) {
        return; // culled or merged away: drawn by nobody
    }
    cmds[o + 1u] = cmds[o + 1u] == 0u ? 1u : 0u;
}
