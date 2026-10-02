// nanite_cluster_cull_comp.glsl — the GPU-driven cull's second stage
// (render/nanite_cull.go): one workgroup per tile, one thread per cluster of
// it, each writing its slot of the indirect-command list. Follows
// nanite_cull_common.glsl, whose header explains the passes.
//
// A cluster is tested, cheapest first, by: its instance's verdict from the
// first stage; its sphere against the frustum; its normal cone (every
// triangle facing away from the eye — internal/cluster explains the test);
// and in pass 2 its sphere against the Hi-Z pyramid. The cone test is made
// where the draw culls back faces too (CONE_ALL), since a pass that draws back
// faces would show what the cone threw away — and in a shadow pass on closed
// meshes only (CONE_CLOSED), where what it throws away is never the nearest
// depth.

layout(local_size_x = 64) in;

// sphere: local centre, radius. apex.xyz and cone (axis, cutoff): the normal
// cone, cutoff >= 1 for none. draw: firstIndex, indexCount, baseVertex.
struct Cluster {
    vec4 sphere;
    vec4 apex;
    vec4 cone;
    uvec4 draw;
};

// a: first cluster, cluster count, instance, first command slot.
// b.x: first history entry; b.y: the first slot of the tile's group (for the
// packing pass).
struct Tile {
    uvec4 a;
    uvec4 b;
};

layout(std430, binding = 2) readonly buffer Clusters {
    Cluster clusters[];
};
layout(std430, binding = 3) readonly buffer Tiles {
    Tile tiles[];
};
// Per cluster of the batch: drawn last frame (pass 2's verdict).
layout(std430, binding = 4) buffer History {
    uint history[];
};
// DrawElementsIndirectCommand: count, instanceCount, firstIndex, baseVertex,
// baseInstance.
layout(std430, binding = 5) writeonly buffer Commands {
    uint cmds[];
};

// Per tile: how many of its slots draw — what the packing pass
// (nanite_compact_comp.glsl) turns into each tile's place in its group.
layout(std430, binding = 9) writeonly buffer TileCounts {
    uint tileCounts[];
};

uniform uint tileBase;

shared uint survivors;

void main() {
    if (gl_LocalInvocationID.x == 0u) {
        survivors = 0u;
    }
    barrier();
    Tile t = tiles[tileBase + gl_WorkGroupID.x];
    uint ii = t.a.z;
    bool instVisible = instVis[ii] != 0u;
    mat4 m = inst[ii].model;
    float s = maxScale(m);
    uint fl = inst[ii].flags.x;
    bool cone = (useCone == CONE_ALL || (useCone == CONE_CLOSED && (fl & 4u) != 0u)) && (fl & 2u) == 0u;
    for (uint k = gl_LocalInvocationID.x; k < t.a.y; k += gl_WorkGroupSize.x) {
        Cluster cl = clusters[t.a.x + k];
        bool vis = instVisible;
        vec3 c = vec3(0.0);
        float r = 0.0;
        if (vis) {
            c = (m * vec4(cl.sphere.xyz, 1.0)).xyz;
            r = cl.sphere.w * s;
            vis = inFrustum(c, r);
            if (vis && cone && cl.cone.w < 1.0) {
                vec3 apex = (m * vec4(cl.apex.xyz, 1.0)).xyz;
                vec3 axis = normalize(mat3(m) * cl.cone.xyz);
                vec3 d = eye.w > 0.5 ? apex - eye.xyz : eye.xyz;
                if (dot(d, axis) >= cl.cone.w * length(d)) {
                    vis = false;
                }
            }
        }
        bool draw = vis;
        uint h = t.b.x + k;
        if (mode == CULL_PASS1) {
            draw = vis && history[h] != 0u;
        } else if (mode == CULL_PASS2) {
            bool now = vis && !occluded(c, r);
            draw = now && history[h] == 0u;
            history[h] = now ? 1u : 0u;
        }
        uint o = (t.a.w + k) * 5u;
        cmds[o] = draw ? cl.draw.y : 0u;
        cmds[o + 1u] = draw ? 1u : 0u;
        cmds[o + 2u] = cl.draw.x;
        cmds[o + 3u] = cl.draw.z;
        cmds[o + 4u] = ii;
        if (draw) {
            atomicAdd(survivors, 1u);
        }
    }
    barrier();
    if (gl_LocalInvocationID.x == 0u) {
        tileCounts[tileBase + gl_WorkGroupID.x] = survivors;
    }
}
