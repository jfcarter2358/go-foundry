// nanite_cluster_common.glsl — what the two cluster stages of the GPU-driven
// cull share (render/nanite_cull.go): the cluster and tile records, the
// buffers they write, and the per-cluster tests. Follows
// nanite_cull_common.glsl; the #version line and RANGE_SLOTS are the Go
// side's.
//
// A cluster is tested, cheapest first, by: its instance's verdict from the
// first stage; its level of detail (its leaf reached by the tree walk, then
// the exact test: its own error within the threshold and its parent's not —
// internal/cluster/dag.go explains why that cut is complete and crack-free);
// its sphere against the frustum; its normal cone (every triangle facing away
// from the eye — internal/cluster explains the test); and in pass 2 its
// sphere against the Hi-Z pyramid. The cone test is made where the draw culls
// back faces too (CONE_ALL), since a pass that draws back faces would show
// what the cone threw away — and in a shadow pass on closed meshes only
// (CONE_CLOSED), where what it throws away is never the nearest depth.
//
// Two kinds of tile run the tests (render/nanite_cull.go, tileClusters and
// tileRange): a CLUSTER tile, a workgroup with a thread a cluster and a
// command slot a cluster (nanite_cluster_cull_comp.glsl); and a RANGE tile, a
// leaf of a DAG's tree whose clusters the LOD test answers alike, a thread
// for the whole tile and RANGE_SLOTS slots for its survivors' runs
// (nanite_range_cull_comp.glsl).

// sphere: local centre, radius. apex.xyz and cone (axis, cutoff): the normal
// cone, cutoff >= 1 for none. draw: firstIndex, indexCount, baseVertex, leaf.
// lod and parent: the spheres its own error and its parent's are projected
// from; errs: own error, parent error (< 0: a root), level.
struct Cluster {
    vec4 sphere;
    vec4 apex;
    vec4 cone;
    uvec4 draw;
    vec4 lod;
    vec4 parent;
    vec4 errs;
};

// a: first cluster, cluster count, instance, first command slot.
// b.x: first history entry; b.y: the first slot of the tile's group (for the
// packing pass); b.z: the mode — 0 a cluster tile, 1 a range tile.
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
// The tiles of each kind, by number: what each stage is dispatched over.
layout(std430, binding = 13) readonly buffer KindTiles {
    uint kindTiles[];
};

uniform uint tileBase;
uniform uint kindTileCount;
// A debug view's pass (render/nanite_debug.go): each command's base instance
// is its slot, not its instance, so the debug program can tell its clusters
// apart.
uniform int debugSlots;

// lodSelects is the LOD test: the cluster is in this pass's cut.
bool lodSelects(Cluster cl, mat4 m, float s) {
    if (lodMode == 0) {
        return cl.errs.z == 0.0;
    }
    // Own error fine enough, parent's not. A cluster and its parent read the
    // very same numbers for the parent group, so they never disagree about
    // which of them is drawn.
    bool own = lodWithin((m * vec4(cl.lod.xyz, 1.0)).xyz, cl.lod.w * s, cl.errs.x * s);
    bool parentFine = cl.errs.y >= 0.0 &&
        lodWithin((m * vec4(cl.parent.xyz, 1.0)).xyz, cl.parent.w * s, cl.errs.y * s);
    return own && !parentFine;
}

// clusterDraws is the rest — frustum, cone, and the pass's occlusion — for a
// cluster the LOD test took (vis false: it did not, or its instance is out),
// with history entry h, which pass 2 updates.
bool clusterDraws(Cluster cl, bool vis, mat4 m, float s, bool cone, uint h) {
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
    if (mode == CULL_PASS1) {
        return vis && history[h] != 0u;
    }
    if (mode == CULL_PASS2) {
        bool now = vis && !occluded(c, r);
        bool draw = now && history[h] == 0u;
        history[h] = now ? 1u : 0u;
        return draw;
    }
    return vis;
}

// ---------- streaming (render/nanite_stream.go; Phase 3) ----------
//
// A camera pass over a streamed mesh pool tells the CPU what it used and what
// it wants. For each cluster in the cut: its page was used this frame, and
// is worth the error its parent would show were it gone — the parent's error
// projected, in units of the threshold (> 1 by the cut's own test; a root's
// is the most there is). For each one DRAWN although its own cooked error is
// too coarse for the eye — drawn only because the finer pages are not
// resident (the residency gave its group error 0) — the page its finer
// version is on is asked for, as much as that cooked error projects to. The
// four arrays, each streamPages long: used (frame), asked (frame), what the
// use is worth, what the ask is worth. The frames are never cleared; the
// worths are, by the CPU, after each copy (render/nanite_stream.go).
// streamInfo: per cluster, its page, the page it asks for (0xffffffff:
// none, level 0), its cooked own error's bits.

uniform int streamOn;
uniform uint streamFrame;
uniform uint streamPages;

layout(std430, binding = 14) readonly buffer StreamInfo {
    uvec4 streamInfo[];
};
layout(std430, binding = 15) buffer StreamFeedback {
    uint streamFeedback[];
};

// streamWorth is an error e over the sphere (c, r) projected, in units of
// the threshold (lodWithin's test is streamWorth <= 1), as a fixed-point
// uint for atomicMax.
uint streamWorth(vec3 c, float r, float e) {
    float v = e * lodK / max(distance(c, lodEye) - r, LOD_NEAR);
    return uint(clamp(v * 64.0, 0.0, 4.0e9));
}

// streamNote records cluster ci, in this pass's cut, and drawn or not.
void streamNote(uint ci, Cluster cl, mat4 m, float s, bool drawn) {
    if (streamOn == 0) {
        return;
    }
    uvec4 si = streamInfo[ci];
    if (streamFeedback[si.x] != streamFrame) {
        streamFeedback[si.x] = streamFrame;
    }
    uint used = 0xffffffffu;
    if (cl.errs.y >= 0.0) {
        used = streamWorth((m * vec4(cl.parent.xyz, 1.0)).xyz, cl.parent.w * s, cl.errs.y * s);
    }
    atomicMax(streamFeedback[2u * streamPages + si.x], used);
    if (drawn && si.y != 0xffffffffu) {
        float cooked = uintBitsToFloat(si.z);
        if (cooked > cl.errs.x) {
            uint ask = streamWorth((m * vec4(cl.lod.xyz, 1.0)).xyz, cl.lod.w * s, cooked * s);
            if (ask > 64u) {
                streamFeedback[streamPages + si.y] = streamFrame;
                atomicMax(streamFeedback[3u * streamPages + si.y], ask);
            }
        }
    }
}

// instanceCones is whether the instance's clusters are cone-tested this pass.
bool instanceCones(uint fl) {
    return (useCone == CONE_ALL || (useCone == CONE_CLOSED && (fl & 4u) != 0u)) && (fl & 2u) == 0u;
}

// instanceWalked is whether the instance's leaf marks are this pass's.
bool instanceWalked(uint fl) {
    return lodMode != 0 && lodWalk != 0 && (fl & 8u) != 0u;
}
