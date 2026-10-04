// nfoliage_voxlist_comp.glsl — the Nanite foliage compute rasteriser's first
// stage (render/nfoliage_voxraster.go). The #version line, the 64-bit
// extensions, the defines, foliage_params.glsl, nfoliage_common.glsl and
// nfoliage_voxel.glsl are the Go side's, ahead of this.
//
// A thread a small brick (the references the cull put on the voxel
// clusters' SMALL commands): it places the brick — the plant and its fade
// (nfBeginRef), the brick's map to the world (nfBrickMap: the bones, the
// gusts, the plant's frame) — writes that for the raster, and lists the
// brick's occupied voxels, one entry each (brick << 6 | voxel), at a place
// an atomic counter gives it. The raster (nfoliage_voxraster_comp.glsl) then
// runs a thread a listed voxel: every thread busy, where a thread a brick
// ran as long as the densest brick of its warp, and a thread a voxel slot
// mostly idled on empty ones (measured on the GTX 1070,
// docs/nanite/PHASE_6.md). Its last stage (NF_DISPATCH, one thread) turns
// the count into the raster's dispatch.

#ifdef NF_DISPATCH
layout(local_size_x = 1) in;
#else
layout(local_size_x = 64) in;
#endif

layout(std430, binding = 3) readonly buffer Refs {
    uvec2 refs[];
};
layout(std430, binding = 13) readonly buffer Counters {
    uint itemCount;
    uint itemCap;
    uint refCap;
    uint overflow;
    uvec4 scatterDispatch;
    uvec4 voxDispatch;
    uvec4 voxRefs; // first, count: the small bricks' references
};
// Per small brick: its corner and fade (0: not drawn), its map's three
// columns, its mask, first voxel and number (what its voxels' chances are
// drawn by, as the march draws them: the same every frame whatever order the
// references came in).
struct BrickSetup {
    vec4 origin;
    vec4 a0, a1, a2;
    uvec4 brick;
};
layout(std430, binding = 16) writeonly buffer Setup {
    BrickSetup setup[];
};
layout(std430, binding = 17) buffer VoxList {
    uint voxCount;
    uint voxDropped;
    uint pad0;
    uint pad1;
    uvec4 rasterDispatch;
    uint voxList[];
};

// The room in each.
uniform uint setupCap;
uniform uint listCap;

#ifdef NF_DISPATCH
void main() {
    uint n = min(voxCount, listCap);
    rasterDispatch = uvec4(min((n + 63u) / 64u, 65535u), max((n + 64u * 65535u - 1u) / (64u * 65535u), 1u), 1u, 0u);
    if (n == 0u) {
        rasterDispatch = uvec4(0u, 1u, 1u, 0u);
    }
}
#else
void main() {
    uint k = gl_GlobalInvocationID.x;
    if (k >= voxRefs.y) {
        return;
    }
    if (k >= setupCap) {
        atomicAdd(voxDropped, 1u);
        return;
    }
    BrickSetup b;
    b.origin = vec4(0.0);
    b.a0 = vec4(0.0);
    b.a1 = vec4(0.0);
    b.a2 = vec4(0.0);
    b.brick = uvec4(0u);
    if (!nfBeginRef(refs[voxRefs.x + k])) {
        setup[k] = b;
        return;
    }
    int bi = nFirst + int(nWhich);
    uvec4 b1 = texelFetch(nfBricks, bi * 2 + 1);
    vec3 o;
    mat3 A;
    nfBrickMap(false, bi, o, A);
    b.origin = vec4(o, nFade);
    b.a0 = vec4(A[0], 0.0);
    b.a1 = vec4(A[1], 0.0);
    b.a2 = vec4(A[2], 0.0);
    b.brick = uvec4(b1.xyz, uint(bi));
    setup[k] = b;
    uint n = uint(bitCount(b1.x) + bitCount(b1.y));
    uint at = atomicAdd(voxCount, n);
    if (at + n > listCap) {
        atomicAdd(voxDropped, 1u);
        return;
    }
    for (int part = 0; part < 2; part++) {
        uint bits = part == 0 ? b1.x : b1.y;
        while (bits != 0u) {
            int l = findLSB(bits) + 32 * part;
            bits &= bits - 1u;
            voxList[at++] = (k << 6u) | uint(l);
        }
    }
}
#endif
