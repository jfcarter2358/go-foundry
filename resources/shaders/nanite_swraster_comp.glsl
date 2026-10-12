// nanite_swraster_comp.glsl — the compute rasteriser (render/
// nanite_swraster.go; Phase 5 of the Nanite plan, component 7). The #version
// line, and SW64 when the driver has 64-bit atomics, are the Go side's.
//
// What it draws is what the cluster cull listed instead of drawing
// (nanite_cluster_common.glsl, swTake): clusters small on screen, whose
// triangles are a few pixels each. The hardware rasteriser spends a fixed
// setup per triangle and shades in 2x2 quads, so a triangle of two pixels
// costs it about what one of thirty does; here a thread takes a triangle and
// walks the pixels of its box itself.
//
// The result is the visibility buffer's: per pixel the nearest depth and the
// triangle's ID — (record + 1) << 12 | its triangle in the cluster, the ID
// pass's packing (nanite_vis_frag.glsl), a cluster never more than 128
// triangles. Each listed cluster gets its own record, written here (the Go
// side reserves them after the batch's commands' records), so the material
// pass shades its pixels exactly as it shades the hardware's.
//
// "Nearest" is an atomic max, so the order threads finish in does not matter:
//
//   - SW64: one 64-bit word a pixel, key << 32 | ID, atomicMax — the nearest
//     depth wins, and on a tie the larger ID, so the picture is the same
//     every run.
//   - otherwise two passes over the same triangles (idPass 0, then 1): the
//     first takes atomicMax of the key alone, the second writes its ID,
//     again by atomicMax, only where its key is the one that won. Twice the
//     work, on GL 4.3's 32-bit atomics.
//
// The key is the depth's bits turned round (0xffffffff - bits): window depth
// is in [0, 1], whose float bits order as the numbers do, and the turn makes
// the nearest the largest. 0 is no surface (the cleared buffer).
//
// The resolve (nanite_swresolve_frag.glsl) then writes each covered pixel's
// depth and ID into the scene target under the depth test, against what the
// hardware drew — over the screen box of what was written here (swBox,
// gathered per workgroup in shared memory, then once into the buffer), not
// the whole screen.
//
// Each cluster's corners go through one matrix, projection · camera · model,
// made once per cluster by the workgroup's first thread: the ID pass's three
// products per corner (vertex.glsl's) cost the compute pass more than its
// rasterising did. The positions differ from the ID pass's in the last bits,
// which is harmless: a cluster is drawn by one rasteriser or the other, and
// neighbours meet at depths a hair apart, never on the same triangle.
//
// Coverage is the pixel centre inside the triangle, edges included: two
// triangles sharing an edge both take a pixel centred on it, and the depth
// test sorts that out — there is never a crack between them. Positions are
// snapped to 1/256 pixel (the hardware's 8 bits of subpixel precision) and the
// edge functions are integers, so coverage does not depend on where on
// screen a triangle is. Back faces are drawn, as the ID pass draws them,
// except on instances of a closed mesh (flags.x bit 2), where a back face is
// never the nearest surface of anything this small.

layout(local_size_x = 128) in;

struct Instance {
    mat4 model;
    vec4 sphere;
    uvec4 flags;
    vec4 wind;
};
layout(std430, binding = 0) readonly buffer Instances {
    Instance inst[];
};
struct Record {
    uvec4 a; // first index, base vertex, instance, bin
    uvec4 b; // the record's number + 1, index count
};
layout(std430, binding = 1) writeonly buffer Records {
    Record records[];
};
layout(std430, binding = 2) readonly buffer Indices {
    uint indices[];
};
layout(std430, binding = 3) readonly buffer Vertices {
    float vertexData[]; // x y z u v
};
layout(std430, binding = 16) readonly buffer SwList {
    uint swCount;
    uint swPad0;
    uint swPad1;
    uint swPad2;
    uvec4 swEntries[];
};
#ifdef SW64
layout(std430, binding = 5) coherent buffer VisSW {
    uint64_t visSW[];
};
#else
layout(std430, binding = 5) coherent buffer VisDepth {
    uint visDepth[];
};
layout(std430, binding = 6) coherent buffer VisIDs {
    uint visIDs[];
};
uniform int idPass;
#endif

// The screen box of the pixels written: min x, min y, max x, max y.
layout(std430, binding = 7) coherent buffer SwBox {
    ivec4 swBox;
};

uniform mat4 projection;
uniform mat4 camera;
uniform ivec2 viewSize;

shared mat4 mvp;
// A windy placement (wind_common.glsl, spliced in by the Go side): its
// corners go through the model matrix, take the wind's offset, then the
// view-projection, as vertex.glsl's do. windy says so; vp, model, wnd and
// sph are the cluster's instance's, made by the first thread with mvp.
shared bool windy;
shared mat4 vp;
shared mat4 mdl;
shared vec4 wnd;
shared vec4 sph;
shared int boxLoX, boxLoY, boxHiX, boxHiY;
uniform uint recordBase;
uniform uint binBase;
uniform uint swCap;
// Dense numbering (nanite_vis_number_comp.glsl): each listed cluster takes
// the frame's next record from the shared counter rather than recordBase +
// its place in the list; one past maxRecords is not drawn.
layout(std430, binding = 4) coherent buffer VisNext {
    uint visNext;
};
uniform uint dense;
uniform uint maxRecords;
shared uint swRec;

// orient is twice the signed area of (a, b, p): positive when p is to the
// left of a->b.
int orient(ivec2 a, ivec2 b, ivec2 p) {
    return (b.x - a.x) * (p.y - a.y) - (b.y - a.y) * (p.x - a.x);
}

void rasterise(uint first, int baseVertex, uint ii, uint t, uint id) {
    vec2 sp[3];
    float z[3];
    for (int k = 0; k < 3; k++) {
        uint v = uint(int(indices[first + 3u * t + uint(k)]) + baseVertex);
        vec4 p = vec4(vertexData[5u * v], vertexData[5u * v + 1u], vertexData[5u * v + 2u], 1.0);
        vec4 clip;
        if (windy) {
            vec4 w = mdl * p;
            w.xyz += windOffset(p.xyz, mdl, wnd, sph);
            clip = vp * w;
        } else {
            clip = mvp * p;
        }
        if (clip.w <= 0.0) {
            return; // never, in front of swNear; a safety net
        }
        vec3 ndc = clip.xyz / clip.w;
        sp[k] = (ndc.xy * 0.5 + 0.5) * vec2(viewSize);
        z[k] = ndc.z * 0.5 + 0.5;
    }
    vec2 lo = min(sp[0], min(sp[1], sp[2]));
    vec2 hi = max(sp[0], max(sp[1], sp[2]));
    // The pixels whose centres (x + 0.5) can be inside.
    ivec2 p0 = max(ivec2(ceil(lo - 0.5)), ivec2(0));
    ivec2 p1 = min(ivec2(floor(hi - 0.5)), viewSize - 1);
    if (any(greaterThan(p0, p1))) {
        return;
    }
    // Snapped to 1/256 pixel, relative to the first pixel's centre: a
    // cluster is under swPixels across, so these stay small and the edge
    // functions' products fit 32 bits.
    vec2 o = vec2(p0) + 0.5;
    ivec2 v0 = ivec2(round((sp[0] - o) * 256.0));
    ivec2 v1 = ivec2(round((sp[1] - o) * 256.0));
    ivec2 v2 = ivec2(round((sp[2] - o) * 256.0));
    int area = orient(v0, v1, v2);
    if (area == 0) {
        return;
    }
    float z0 = z[0], z1 = z[1], z2 = z[2];
    if (area < 0) {
        if ((inst[ii].flags.x & 4u) != 0u) {
            return; // a closed mesh's back face
        }
        ivec2 tv = v1;
        v1 = v2;
        v2 = tv;
        float tz = z1;
        z1 = z2;
        z2 = tz;
        area = -area;
    }
    float inv = 1.0 / float(area);
    // The box can over-reach the pixels actually covered; the resolve finds
    // nothing in the rest.
    atomicMin(boxLoX, p0.x);
    atomicMin(boxLoY, p0.y);
    atomicMax(boxHiX, p1.x);
    atomicMax(boxHiY, p1.y);
    for (int y = p0.y; y <= p1.y; y++) {
        for (int x = p0.x; x <= p1.x; x++) {
            ivec2 p = ivec2(x - p0.x, y - p0.y) * 256;
            int w0 = orient(v1, v2, p);
            int w1 = orient(v2, v0, p);
            int w2 = orient(v0, v1, p);
            if ((w0 | w1 | w2) < 0) {
                continue;
            }
            float d = (float(w0) * z0 + float(w1) * z1 + float(w2) * z2) * inv;
            if (d < 0.0 || d > 1.0) {
                continue; // the hardware's depth clip
            }
            uint key = 0xffffffffu - floatBitsToUint(d);
            uint pix = uint(y) * uint(viewSize.x) + uint(x);
#ifdef SW64
            atomicMax(visSW[pix], (uint64_t(key) << 32) | uint64_t(id));
#else
            if (idPass == 0) {
                atomicMax(visDepth[pix], key);
            } else if (visDepth[pix] == key) {
                atomicMax(visIDs[pix], id);
            }
#endif
        }
    }
}

void main() {
    uint n = min(swCount, swCap);
    if (gl_LocalInvocationID.x == 0u) {
        boxLoX = viewSize.x;
        boxLoY = viewSize.y;
        boxHiX = -1;
        boxHiY = -1;
    }
    // A fixed number of workgroups, each taking every so-many-th cluster:
    // the count is the GPU's, never read back. The loop's bounds are the
    // workgroup's own, so the barriers in it are reached by all its threads.
    for (uint e = gl_WorkGroupID.x; e < n; e += gl_NumWorkGroups.x) {
        uvec4 a = swEntries[2u * e];
        uvec4 b = swEntries[2u * e + 1u];
        barrier();
        if (gl_LocalInvocationID.x == 0u) {
            swRec = dense != 0u ? atomicAdd(visNext, 1u) : recordBase + e;
        }
        barrier();
        uint rec = swRec;
        if (rec >= maxRecords) {
            continue; // the same for the whole workgroup: swRec is shared
        }
        if (gl_LocalInvocationID.x == 0u) {
            records[rec].a = uvec4(a.x, a.z, a.w, binBase + b.x);
            records[rec].b = uvec4(rec + 1u, a.y, 0u, 0u);
            mvp = projection * camera * inst[a.w].model;
            wnd = inst[a.w].wind;
            windy = windReach(inst[a.w].model, wnd) > 0.0;
            if (windy) {
                vp = projection * camera;
                mdl = inst[a.w].model;
                sph = inst[a.w].sphere;
            }
        }
        barrier();
        uint tris = min(a.y / 3u, 128u);
        uint t = gl_LocalInvocationID.x;
        if (t < tris) {
            rasterise(a.x, int(a.z), a.w, t, ((rec + 1u) << 12) | t);
        }
    }
    barrier();
    if (gl_LocalInvocationID.x == 0u && boxHiX >= 0) {
        atomicMin(swBox.x, boxLoX);
        atomicMin(swBox.y, boxLoY);
        atomicMax(swBox.z, boxHiX);
        atomicMax(swBox.w, boxHiY);
    }
}
