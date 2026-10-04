// nfoliage_scatter_comp.glsl — the Nanite foliage's reference list
// (render/nfoliage.go). The #version line is the Go side's.
//
// A thread per item the cull kept. Its cluster's draw command was given a
// run of the list (nfoliage_scan_comp.glsl); the item takes the next `mult`
// places of it — one for a triangle cluster, one per part of a part cluster,
// one per brick of a voxel cluster — and writes (item, which) into each. The
// draws read the list as an instanced attribute, so instance i of a command
// is reference baseInstance + i: a part cluster's command draws its
// template once per (item, part), a voxel cluster's its box once per
// (item, brick). Without gl_BaseInstance in the shader (GL 4.6) the list is
// how an instance learns which of the item's parts or bricks it is.

layout(local_size_x = 64) in;

struct NFCluster {
    vec4 sphere;
    vec4 lod;
    vec4 parent;
    vec4 errs;
    uvec4 info;   // kind, bucket, draw command, references per item
    uvec4 draw;
};

layout(std430, binding = 2) readonly buffer Items {
    uvec4 items[];
};
layout(std430, binding = 3) writeonly buffer Refs {
    uvec2 refs[];
};
layout(std430, binding = 4) readonly buffer Clusters {
    NFCluster clusters[];
};
layout(std430, binding = 6) buffer Cursors {
    uint cursors[];
};
layout(std430, binding = 13) readonly buffer Counters {
    uint itemCount;
    uint itemCap;
    uint refCap;
    uint overflow;
};

void main() {
    uint i = gl_GlobalInvocationID.x;
    if (i >= min(itemCount, itemCap)) {
        return;
    }
    uvec4 it = items[i];
    NFCluster cl = clusters[it.z];
    uvec4 info = cl.info;
    // A voxel cluster's small-brick command, where the cull chose it.
    uint cmd = it.w != 0u ? cl.draw.z : info.z;
    uint mult = info.w;
    uint at = atomicAdd(cursors[cmd * 2u], mult);
    uint end = cursors[cmd * 2u + 1u];
    for (uint j = 0u; j < mult; j++) {
        if (at + j < end) {
            refs[at + j] = uvec2(i, j);
        }
    }
}
