// nfoliage_scan_comp.glsl — the Nanite foliage's draw commands, laid out
// (render/nfoliage.go). The #version line is the Go side's.
//
// One workgroup. The cull counted, in each command's instance count, the
// references its items will take; this turns those counts into each
// command's run of the reference list — an exclusive prefix sum, written as
// its base instance — and a cursor pair (next, end) per command that the
// scatter fills the run through. Whatever would run past the list's capacity
// is cut (and noted), so a frame that outgrows it draws less, never garbage.
// It also writes the scatter's indirect dispatch, and where the small
// bricks' references start and how many there are, for the compute
// rasteriser (nfoliage_voxraster_comp.glsl) and its dispatch.

layout(local_size_x = 1024) in;

layout(std430, binding = 1) buffer Commands {
    uint cmds[];
};
layout(std430, binding = 6) writeonly buffer Cursors {
    uint cursors[];
};
layout(std430, binding = 13) buffer Counters {
    uint itemCount;
    uint itemCap;
    uint refCap;
    uint overflow;
    uvec4 scatterDispatch;  // x, y, z, refs used
    uvec4 voxDispatch;      // x, y, z, 0: the rasteriser's
    uvec4 voxRefs;          // first, count, bricks as boxes, 0
};

// The pass's commands: a shadow pass's proxies, or a camera pass's
// everything else (render/nfoliage.go's cullPass). Others are left as they
// are — no pass draws them.
uniform uint cmdFirst;
uniform uint cmdCount;
// The voxel clusters' SMALL commands — the bricks small on screen, which
// the compute rasteriser takes where it runs: first and count (one run).
uniform uint voxFirst;
uniform uint voxCount;
// Their boxes commands, likewise, for the count.
uniform uint boxFirst;
uniform uint boxCount;

shared uint sums[1024];

void main() {
    uint lid = gl_LocalInvocationID.x;
    uint carry = 0u;
    for (uint base = 0u; base < cmdCount; base += 1024u) {
        uint i = cmdFirst + base + lid;
        bool mine = base + lid < cmdCount;
        uint v = mine ? cmds[i * 5u + 1u] : 0u;
        sums[lid] = v;
        barrier();
        for (uint off = 1u; off < 1024u; off <<= 1u) {
            uint t = lid >= off ? sums[lid - off] : 0u;
            barrier();
            sums[lid] += t;
            barrier();
        }
        uint excl = carry + sums[lid] - v;
        if (mine) {
            uint n = v;
            if (excl >= refCap) {
                n = 0u;
            } else if (excl + n > refCap) {
                n = refCap - excl;
            }
            if (n != v) {
                atomicOr(overflow, 2u);
            }
            cmds[i * 5u + 1u] = n;
            cmds[i * 5u + 4u] = excl;
            cursors[i * 2u] = excl;
            cursors[i * 2u + 1u] = excl + n;
        }
        carry += sums[1023];
        barrier();
    }
    memoryBarrierBuffer();
    barrier();
    if (lid == 0u) {
        uint items = min(itemCount, itemCap);
        scatterDispatch = uvec4((items + 63u) / 64u, 1u, 1u, min(carry, refCap));
        uint first = 0u;
        uint count = 0u;
        if (voxCount > 0u) {
            first = cmds[voxFirst * 5u + 4u];
            uint last = voxFirst + voxCount - 1u;
            count = cmds[last * 5u + 4u] + cmds[last * 5u + 1u] - first;
        }
        uint boxes = 0u;
        if (boxCount > 0u) {
            uint last = boxFirst + boxCount - 1u;
            boxes = cmds[last * 5u + 4u] + cmds[last * 5u + 1u] - cmds[boxFirst * 5u + 4u];
        }
        voxRefs = uvec4(first, count, boxes, 0u);
        // A thread a brick, 64 to a workgroup.
        voxDispatch = uvec4((count + 63u) / 64u, 1u, 1u, 0u);
    }
}
