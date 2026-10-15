// scenery_cull_comp.glsl — the classic scenery's per-pass cull, on the GPU
// (render/scenery_gpu_cull.go). The #version line and the stage's #define are
// the Go side's: one file, four programs.
//
// The placements' matrices and boxes are uploaded once (and again only when a
// placement moves); each pass that draws them — the camera, a cascade, a cube
// face — culls them here and draws each group with one indirect instanced
// draw. What it leaves in the instance buffer is what the CPU's fill left:
// every group's survivors, in member order, the groups one after another — a
// STABLE compaction (a prefix sum, not an atomic counter), so a pass draws
// its instances in the same order every frame and the same order the CPU path
// did.
//
//   SC_FLAGS    a thread a placement: visible or not, and its place among
//               its block's visible ones (a 256-wide scan in shared memory);
//               each block's total
//   SC_BLOCKS   one workgroup: the blocks' totals into offsets, and the grand
//               total after them
//   SC_SCATTER  a thread a placement: a visible one's matrix to its place
//   SC_COMMANDS a thread a group: its instance count and first instance

#ifdef SC_BLOCKS
layout(local_size_x = 256) in;
#elif defined(SC_COMMANDS)
layout(local_size_x = 64) in;
#else
layout(local_size_x = 256) in;
#endif

struct Instance {
    mat4 model;
    // lo.w: 1 + the distance it stops casting at (0: none), negated when
    // the placement has no box to cull by; hi.w the distance it stops being
    // drawn at (0: none) — render/scenery_gpu_cull.go classicDrawCuts.
    vec4 lo;
    vec4 hi;
};
layout(std430, binding = 0) readonly buffer Instances {
    Instance instances[];
};
layout(std430, binding = 1) readonly buffer MemberGroups {
    uint memberGroup[];
};
// x: the group's first member, y: its members, z: 1 for a transparent group
// (left out of a colour pass), w: 1 for an indexed draw.
layout(std430, binding = 2) readonly buffer Groups {
    uvec4 groupInfo[];
};
// Each placement's place among its block's visible ones, shifted up a bit,
// and in the low bit whether it is visible.
layout(std430, binding = 3) buffer Local {
    uint local[];
};
// Each block's visible count, then (SC_BLOCKS) its first visible's place, and
// after the last block the total.
layout(std430, binding = 4) buffer Blocks {
    uint blocks[];
};
layout(std430, binding = 5) writeonly buffer Out {
    mat4 outModel[];
};
// Five words a group: DrawElementsIndirectCommand, or DrawArraysIndirect-
// Command in the first four.
layout(std430, binding = 6) buffer Commands {
    uint cmds[];
};

uniform uint count;
uniform uint blockCount;
uniform uint groupCount;
uniform int skipTransparent;
uniform vec4 planes[6];
// The camera, the draw distances are measured from, and whether this is a
// shadow pass (its cut is the shadow distance's).
uniform vec3 drawEye;
uniform int shadowPass;

bool drawCutKeeps(Instance in_) {
    float cut = shadowPass != 0 ? max(abs(in_.lo.w) - 1.0, 0.0) : in_.hi.w;
    return cut <= 0.0 || distance(drawEye, in_.model[3].xyz) < cut;
}

shared uint scan[256];

// render.AABBInFrustum, to the operation: the positive corner against each
// plane, the sum in the same order, nothing fused.
bool boxInFrustum(vec3 lo, vec3 hi) {
    for (int i = 0; i < 6; i++) {
        vec4 p = planes[i];
        vec3 c = vec3(p.x < 0.0 ? lo.x : hi.x, p.y < 0.0 ? lo.y : hi.y, p.z < 0.0 ? lo.z : hi.z);
        precise float d = p.x * c.x + p.y * c.y + p.z * c.z + p.w;
        if (d < 0.0) {
            return false;
        }
    }
    return true;
}

// scanShared leaves in scan[] the exclusive prefix sum of what each thread
// put there, and returns the workgroup's total.
uint scanShared(uint lid, uint v) {
    scan[lid] = v;
    barrier();
    for (uint off = 1u; off < 256u; off <<= 1) {
        uint add = lid >= off ? scan[lid - off] : 0u;
        barrier();
        scan[lid] += add;
        barrier();
    }
    uint total = scan[255];
    uint excl = scan[lid] - v;
    barrier();
    scan[lid] = excl;
    barrier();
    return total;
}

// excl is how many placements before i are visible.
uint excl(uint i) {
    if (i >= count) {
        return blocks[blockCount];
    }
    return blocks[i / 256u] + (local[i] >> 1);
}

void main() {
#ifdef SC_FLAGS
    uint i = gl_GlobalInvocationID.x;
    uint lid = gl_LocalInvocationID.x;
    uint v = 0u;
    if (i < count) {
        uvec4 g = groupInfo[memberGroup[i]];
        if (skipTransparent == 0 || g.z == 0u) {
            Instance in_ = instances[i];
            v = ((in_.lo.w <= 0.0 || boxInFrustum(in_.lo.xyz, in_.hi.xyz)) && drawCutKeeps(in_)) ? 1u : 0u;
        }
    }
    uint total = scanShared(lid, v);
    if (i < count) {
        local[i] = (scan[lid] << 1) | v;
    }
    if (lid == 0u) {
        blocks[gl_WorkGroupID.x] = total;
    }
#elif defined(SC_BLOCKS)
    uint lid = gl_LocalInvocationID.x;
    uint carry = 0u;
    for (uint base = 0u; base < blockCount; base += 256u) {
        uint b = base + lid;
        uint v = b < blockCount ? blocks[b] : 0u;
        uint total = scanShared(lid, v);
        if (b < blockCount) {
            blocks[b] = carry + scan[lid];
        }
        carry += total;
        barrier();
    }
    if (lid == 0u) {
        blocks[blockCount] = carry;
    }
#elif defined(SC_SCATTER)
    uint i = gl_GlobalInvocationID.x;
    if (i < count) {
        uint l = local[i];
        if ((l & 1u) != 0u) {
            outModel[blocks[i / 256u] + (l >> 1)] = instances[i].model;
        }
    }
#else
    uint g = gl_GlobalInvocationID.x;
    if (g < groupCount) {
        uvec4 info = groupInfo[g];
        uint first = excl(info.x);
        uint last = excl(info.x + info.y);
        cmds[g * 5u + 1u] = last - first;
        cmds[g * 5u + (info.w != 0u ? 4u : 3u)] = first;
    }
#endif
}
