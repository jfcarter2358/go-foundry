// nfoliage_voxraster_comp.glsl — the Nanite foliage's compute rasteriser
// for voxel bricks (render/nfoliage_voxraster.go). The #version line, the
// 64-bit extensions, the defines, foliage_params.glsl, nfoliage_common.glsl
// and nfoliage_voxel.glsl are the Go side's, ahead of this.
//
// What it draws is the bricks the cull found small on screen
// (nfoliage_cull_comp.glsl put them on their clusters' SMALL commands). A
// brick a few pixels across costs the hardware rasteriser twelve triangles
// and a ray march per covered pixel; here each occupied voxel projects its
// centre and writes the pixel (or the few pixels) its footprint covers —
// Phase 5's rasteriser (nanite_swraster_comp.glsl) for voxels rather than
// triangles.
//
// A thread a voxel of the list nfoliage_voxlist_comp.glsl made — every
// occupied voxel of every small brick, the brick's place in the world worked
// out once there. A voxel covers the pixels whose centres fall in its
// footprint: a square of the area its cube shows along the ray (one face's
// area times |dx| + |dy| + |dz| of the ray's direction in the brick's frame),
// centred where its centre projects — usually one pixel, sometimes none
// (then a neighbour takes the pixel), never the few around it, which would
// count a voxel's coverage several times over.
//
// A voxel stops the pixel's ray with the chance its coverage gives along it
// (nfVoxelCoverage) times the instance's fade, drawn per pixel and per frame,
// as the ray march draws it. Where it does, the pixel's word takes
//
//	(0xffffffff − depth bits) << 32 | reference << 6 | voxel
//
// by a 64-bit atomic max: the nearest voxel wins, and on a tie the larger
// word, so the picture does not depend on the order threads finish in. The
// resolve (nfoliage_voxresolve_frag.glsl) shades each written pixel once,
// against the depth the hardware drew, over the screen box gathered here.

layout(local_size_x = 64) in;

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
    uvec4 voxRefs; // first, count
};
layout(std430, binding = 14) coherent buffer VisFoliage {
    uint64_t vis[];
};
// The screen box of the pixels written: min x, min y, max x, max y.
layout(std430, binding = 15) coherent buffer VisBox {
    ivec4 visBox;
};
struct BrickSetup {
    vec4 origin;    // corner, fade (0: not drawn)
    vec4 a0, a1, a2;
    uvec4 brick;    // mask lo, mask hi, first voxel, brick
};
layout(std430, binding = 16) readonly buffer Setup {
    BrickSetup setup[];
};
layout(std430, binding = 17) readonly buffer VoxList {
    uint voxCount;
    uint voxDropped;
    uint pad0;
    uint pad1;
    uvec4 rasterDispatch;
    uint voxList[];
};
uniform uint listCap;

uniform mat4 projView;
uniform vec4 viewport;   // x, y, w, h
uniform float pxPerUnit; // at distance 1
uniform float nearClip;
uniform int coverageDither;

shared int boxLoX, boxLoY, boxHiX, boxHiY;

ivec4 gBox = ivec4(1 << 30, 1 << 30, -1, -1);

void splat(ivec2 p, uint64_t key, float alpha, uint brick, int l) {
    ivec2 size = ivec2(viewport.zw);
    if (any(lessThan(p, ivec2(0))) || any(greaterThanEqual(p, size))) {
        return;
    }
    if (nfHash01(uvec4(uvec2(p), uint(coverageDither) * 131u + uint(l), brick)) >= alpha) {
        return;
    }
    atomicMax(vis[uint(p.y) * uint(size.x) + uint(p.x)], key);
    gBox = ivec4(min(gBox.xy, p), max(gBox.zw, p));
}

void rasterVoxel(uint e) {
    uint k = e >> 6u;
    int l = int(e & 63u);
    BrickSetup bs = setup[k];
    float fade = bs.origin.w;
    if (fade <= 0.0) {
        return;
    }
    uint lo = bs.brick.x, hi = bs.brick.y;
    int rank = l < 32 ? bitCount(lo & ((1u << uint(l)) - 1u))
                      : bitCount(lo) + bitCount(hi & ((1u << uint(l - 32)) - 1u));
    uvec4 vox = texelFetch(nfVoxels, int(bs.brick.z) + rank);
    mat3 A = mat3(bs.a0.xyz, bs.a1.xyz, bs.a2.xyz);
    vec3 c = vec3(float(l & 3), float((l >> 2) & 3), float(l >> 4)) + 0.5;
    vec3 world = bs.origin.xyz + A * c;
    vec4 clip = projView * vec4(world, 1.0);
    if (clip.w <= nearClip) {
        return;
    }
    vec3 ndc = clip.xyz / clip.w;
    if (any(greaterThan(abs(ndc), vec3(1.02)))) {
        return;
    }
    uint r = voxRefs.x + k;
    vec2 px = (ndc.xy * 0.5 + 0.5) * viewport.zw;
    float depth = clamp(ndc.z * 0.5 + 0.5, 0.0, 1.0);
    uint64_t key = (uint64_t(0xffffffffu - floatBitsToUint(depth)) << 32) | uint64_t((r << 6u) | uint(l));
    vec3 dn = normalize(inverse(A) * (world - camPos));
    float alpha = nfVoxelCoverage(vox, dn) * fade;
    // The footprint: the cube's silhouette along the ray, as a square.
    float edge = (length(A[0]) + length(A[1]) + length(A[2])) * (1.0 / 3.0);
    vec3 ad = abs(dn);
    float span = edge * pxPerUnit / clip.w * sqrt(ad.x + ad.y + ad.z);
    ivec2 p0 = ivec2(ceil(px - 0.5 * span - 0.5));
    ivec2 p1 = min(ivec2(floor(px + 0.5 * span - 0.5)), p0 + 3);
    for (int y = p0.y; y <= p1.y; y++) {
        for (int x = p0.x; x <= p1.x; x++) {
            splat(ivec2(x, y), key, alpha, bs.brick.w, l);
        }
    }
}

void main() {
    if (gl_LocalInvocationID.x == 0u) {
        boxLoX = 1 << 30;
        boxLoY = 1 << 30;
        boxHiX = -1;
        boxHiY = -1;
    }
    barrier();
    uint i = (gl_WorkGroupID.y * 65535u + gl_WorkGroupID.x) * 64u + gl_LocalInvocationID.x;
    if (i < min(voxCount, listCap)) {
        rasterVoxel(voxList[i]);
    }
    if (gBox.z >= 0) {
        atomicMin(boxLoX, gBox.x);
        atomicMin(boxLoY, gBox.y);
        atomicMax(boxHiX, gBox.z);
        atomicMax(boxHiY, gBox.w);
    }
    barrier();
    if (gl_LocalInvocationID.x == 0u && boxHiX >= 0) {
        atomicMin(visBox.x, boxLoX);
        atomicMin(visBox.y, boxLoY);
        atomicMax(visBox.z, boxHiX);
        atomicMax(visBox.w, boxHiY);
    }
}
