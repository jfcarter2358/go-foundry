// nanite_lod_traverse_comp.glsl — the LOD selection's tree walk
// (render/nanite_cull.go, "Level of detail"), between the instance stage and
// the cluster stage. Follows nanite_cull_common.glsl. Phase 2 of the Nanite
// plan (docs/nanite/PHASE_2.md), component 5.
//
// One workgroup per instance whose mesh has a cluster DAG. It walks the
// mesh's tree over its cluster groups (internal/cluster/lod_tree.go) breadth
// first, a level of the tree at a time, the frontier in shared memory and a
// thread per node. A node is skipped, with everything under it, when
//
//   - its cull sphere is out of the frustum;
//   - even its largest parent error, projected from the nearest point of a
//     sphere round every parent sphere under it, is fine enough: then every
//     cluster under it has a parent the cut takes instead;
//   - even its smallest own error, projected from the FURTHEST point of a
//     sphere round every own sphere under it, is too coarse.
//
// (cluster.TreeSkips is the same test on the CPU, with the same slack.) A
// leaf reached is marked with the pass's stamp; the cluster stage then
// decides each of its clusters exactly, and drops every cluster of a leaf not
// marked without reading it. The walk only ever skips what the exact test
// would reject, so the cut is the same with or without it.
//
// The frontier holds FRONTIER nodes. A node whose children do not fit marks
// every leaf under it instead (they are one run of leaf numbers) — more
// clusters for the exact test, never fewer.

layout(local_size_x = 64) in;

// sphere: the cull sphere. parent, own: spheres round every parent / own LOD
// sphere under the node. errs: largest parent error (>= 3e38: a root is under
// it), smallest own error. link: first child, child count (0: a leaf), first
// leaf, leaf count.
struct Node {
    vec4 sphere;
    vec4 parent;
    vec4 own;
    vec4 errs;
    uvec4 link;
};

layout(std430, binding = 11) readonly buffer Nodes {
    Node nodes[];
};

uniform uint workBase;

#define FRONTIER 1024u
#define NO_NODE 0xffffffffu

shared uint frontier[2][FRONTIER];
shared uint frontierLen[2];

bool nodeWanted(Node n, mat4 m, float s) {
    if (!inFrustum((m * vec4(n.sphere.xyz, 1.0)).xyz, n.sphere.w * s)) {
        return false;
    }
    if (n.errs.x < 3.0e38) {
        float d = max(distance((m * vec4(n.parent.xyz, 1.0)).xyz, lodEye) - n.parent.w * s, LOD_NEAR);
        if (n.errs.x * s * lodK < d * (1.0 - 1e-4)) {
            return false;
        }
    }
    float far = max(distance((m * vec4(n.own.xyz, 1.0)).xyz, lodEye) + n.own.w * s, LOD_NEAR);
    return !(n.errs.y * s * lodK > far * (1.0 + 1e-4) + 1e-6);
}

void main() {
    uint ii = workBase + gl_WorkGroupID.x;
    uint lid = gl_LocalInvocationID.x;
    // The same answer for the whole workgroup, so the barriers below are
    // reached by all of it or none.
    if (ii >= instanceCount || (inst[ii].flags.x & 8u) == 0u || instVis[ii] == 0u) {
        return;
    }
    mat4 m = inst[ii].model;
    float s = maxScale(m);
    uint leafBase = inst[ii].flags.z;
    if (lid == 0u) {
        frontier[0][0] = inst[ii].flags.y;
        frontierLen[0] = 1u;
        frontierLen[1] = 0u;
    }
    barrier();
    uint cur = 0u;
    for (int depth = 0; depth < 64; depth++) {
        uint n = min(frontierLen[cur], FRONTIER);
        if (n == 0u) {
            break;
        }
        uint nxt = 1u - cur;
        for (uint k = lid; k < n; k += 64u) {
            uint ni = frontier[cur][k];
            if (ni == NO_NODE) {
                continue;
            }
            Node nd = nodes[ni];
            if (!nodeWanted(nd, m, s)) {
                continue;
            }
            if (nd.link.y == 0u) {
                leafVis[leafBase + nd.link.z] = passStamp;
                continue;
            }
            uint at = atomicAdd(frontierLen[nxt], nd.link.y);
            if (at + nd.link.y > FRONTIER) {
                // No room: every leaf under it, and the slots it did get
                // marked empty.
                for (uint c = at; c < FRONTIER; c++) {
                    frontier[nxt][c] = NO_NODE;
                }
                for (uint l = 0u; l < nd.link.w; l++) {
                    leafVis[leafBase + nd.link.z + l] = passStamp;
                }
                continue;
            }
            for (uint c = 0u; c < nd.link.y; c++) {
                frontier[nxt][at + c] = nd.link.x + c;
            }
        }
        barrier();
        if (lid == 0u) {
            frontierLen[cur] = 0u;
        }
        barrier();
        cur = nxt;
    }
}
