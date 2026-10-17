// nanite_range_cull_comp.glsl — the GPU-driven cull's cluster stage for
// RANGE tiles (render/nanite_cull.go, tileRange): one thread per tile. Follows
// nanite_cull_common.glsl and nanite_cluster_common.glsl.
//
// A range tile is a window of a DAG's clusters — at most 32, consecutive in
// the index buffer, and at most three whole leaves of its LOD tree
// (cluster.LeafRanges), within each of which the LOD test answers alike. Each
// cluster is tested as a cluster tile's would be (its leaf's mark, the LOD
// test, the frustum, the cone and the pass's occlusion, updating its
// history); then the survivors' runs become at most RANGE_SLOTS commands,
// each one contiguous range of the index buffer. While there are more runs
// than slots, the two nearest are joined into one command — but only across a
// gap that is all in the cut, whose clusters the frustum, the cone or the
// occlusion dropped, which costs drawing them and nothing else. A cluster
// outside the cut is never joined over: it would draw two levels of one
// surface at once. Since the cut takes or leaves a leaf whole, the runs no
// join can bridge are separated by whole leaves outside it, and three leaves
// leave at most two such runs — which is why a window holds at most three
// (rangeWindows), with RANGE_SLOTS two.
//
// A thread a tile rather than a workgroup: a field of rocks is thousands of
// windows a pass, which as workgroups cost more to launch than their tests
// (docs/nanite/PHASE_2.md).

layout(local_size_x = 64) in;

void main() {
    uint n = tileBase + gl_GlobalInvocationID.x;
    // Over the live list (render/nanite_live_tiles.go) the bound is its
    // length, which only the GPU knows.
    if (n >= (liveList != 0 ? liveCount : kindTileCount)) {
        return;
    }
    uint ti = kindTiles[n];
    Tile t = tiles[ti];
    uint ii = t.a.z;
    mat4 m = inst[ii].model;
    float s = maxScale(m);
    uint fl = inst[ii].flags.x;
    bool instVisible = instVis[ii] != 0u;
    bool walked = instanceWalked(fl);
    uint leafBase = inst[ii].flags.z;
    clusterPad = instancePad(ii);
    bool cone = instanceCones(fl);
    uint count = min(t.a.y, 32u);
    // drawn: the survivors; inCut: every cluster of the window the LOD test
    // took, drawn or not — what a join may cover.
    uint drawn = 0u;
    uint inCut = 0u;
    uint lastLeaf = 0xffffffffu;
    bool leafIn = false;
    for (uint k = 0u; k < count; k++) {
        uint ci = t.a.x + k;
        bool vis = instVisible;
        if (vis) {
            // The leaf's verdict, once per leaf: its mark, then the LOD test
            // on its first cluster here, which answers for all of it.
            uint leaf = clusters[ci].draw.w;
            if (leaf != lastLeaf) {
                lastLeaf = leaf;
                leafIn = !(walked && leafVis[leafBase + leaf] != passStamp) && lodSelects(clusters[ci], m, s);
            }
            vis = leafIn;
        }
        if (vis) {
            inCut |= 1u << k;
            bool d = clusterDraws(clusters[ci], true, m, s, cone, t.b.x + k);
            streamNote(ci, clusters[ci], m, s, d);
            // Small on screen: the compute rasteriser's. It stays in the cut,
            // so a join may still cover it — drawn twice then, harmlessly:
            // the same triangles at the same depth.
            if (d && !swTake(clusters[ci], m, s, ii, t.b.w)) {
                drawn |= 1u << k;
            }
        } else if (mode == CULL_PASS2) {
            // Out of the cut or out of view: nothing to read, but pass 2
            // still records that it was not seen.
            history[t.b.x + k] = 0u;
        }
    }
    // The survivors' runs, as [first, last] cluster pairs.
    uint lo[16];
    uint hi[16];
    uint runs = 0u;
    uint mask = drawn;
    while (mask != 0u) {
        uint a = uint(findLSB(mask));
        uint gap = ~mask & ~((1u << a) - 1u); // the first gap at or after a
        uint b = gap == 0u ? 31u : uint(findLSB(gap)) - 1u;
        mask = b >= 31u ? 0u : mask & ~((2u << b) - 1u);
        lo[runs] = a;
        hi[runs] = b;
        runs++;
    }
    // Too many for the slots: join the nearest two whose gap is all in the
    // cut, until they fit. (They always can — see above; were they not to,
    // the nearest are joined regardless: a doubled surface is better than a
    // hole.)
    while (runs > RANGE_SLOTS) {
        uint best = 0xffffffffu;
        uint bestGap = 0xffffffffu;
        uint forced = 0u;
        for (uint r = 0u; r + 1u < runs; r++) {
            uint between = ((1u << lo[r + 1u]) - 1u) & ~((2u << hi[r]) - 1u);
            uint g = lo[r + 1u] - hi[r];
            if ((between & ~inCut) == 0u && g < bestGap) {
                best = r;
                bestGap = g;
            }
            if (g < lo[forced + 1u] - hi[forced]) {
                forced = r;
            }
        }
        if (best == 0xffffffffu) {
            best = forced;
        }
        hi[best] = hi[best + 1u];
        for (uint r = best + 1u; r + 1u < runs; r++) {
            lo[r] = lo[r + 1u];
            hi[r] = hi[r + 1u];
        }
        runs--;
    }
    for (uint r = 0u; r < RANGE_SLOTS; r++) {
        uint o = (t.a.w + r) * 5u;
        bool on = r < runs;
        uvec4 a = clusters[t.a.x + (on ? lo[r] : 0u)].draw;
        uvec4 b = clusters[t.a.x + (on ? hi[r] : 0u)].draw;
        cmds[o] = on ? b.x + b.y - a.x : 0u;
        cmds[o + 1u] = on ? 1u : 0u;
        cmds[o + 2u] = a.x;
        cmds[o + 3u] = a.z;
        cmds[o + 4u] = debugSlots != 0 ? t.a.w + r : ii;
    }
    tileCounts[ti] = runs;
}
