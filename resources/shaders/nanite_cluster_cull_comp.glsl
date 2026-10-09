// nanite_cluster_cull_comp.glsl — the GPU-driven cull's cluster stage for
// CLUSTER tiles (render/nanite_cull.go): one workgroup per tile, one thread
// per cluster of it, each writing its slot of the indirect-command list.
// Follows nanite_cull_common.glsl, whose header explains the passes, and
// nanite_cluster_common.glsl, which holds the tests.

layout(local_size_x = 64) in;

shared uint survivors;

void main() {
    if (gl_LocalInvocationID.x == 0u) {
        survivors = 0u;
    }
    barrier();
    uint ti = kindTiles[tileBase + gl_WorkGroupID.x];
    Tile t = tiles[ti];
    uint ii = t.a.z;
    bool instVisible = instVis[ii] != 0u;
    mat4 m = inst[ii].model;
    float s = maxScale(m);
    uint fl = inst[ii].flags.x;
    bool cone = instanceCones(fl);
    bool walked = instanceWalked(fl);
    uint leafBase = inst[ii].flags.z;
    clusterPad = instancePad(ii);
    for (uint k = gl_LocalInvocationID.x; k < t.a.y; k += gl_WorkGroupSize.x) {
        uint ci = t.a.x + k;
        bool vis = instVisible;
        // A leaf the tree walk did not reach holds nothing of the cut: its
        // clusters are dropped without reading their bounds.
        if (vis && walked && leafVis[leafBase + clusters[ci].draw.w] != passStamp) {
            vis = false;
        }
        Cluster cl = clusters[ci];
        vis = vis && lodSelects(cl, m, s);
        bool draw = clusterDraws(cl, vis, m, s, cone, t.b.x + k);
        if (vis) {
            streamNote(ci, cl, m, s, draw);
        }
        // Small on screen: the compute rasteriser's, not a command.
        if (draw && swTake(cl, m, s, ii, t.b.w)) {
            draw = false;
        }
        uint o = (t.a.w + k) * 5u;
        cmds[o] = draw ? cl.draw.y : 0u;
        cmds[o + 1u] = draw ? 1u : 0u;
        cmds[o + 2u] = cl.draw.x;
        cmds[o + 3u] = cl.draw.z;
        cmds[o + 4u] = debugSlots != 0 ? t.a.w + k : ii;
        if (draw) {
            atomicAdd(survivors, 1u);
        }
    }
    barrier();
    if (gl_LocalInvocationID.x == 0u) {
        tileCounts[ti] = survivors;
    }
}
