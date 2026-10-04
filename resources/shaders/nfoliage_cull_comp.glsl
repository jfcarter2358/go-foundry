// nfoliage_cull_comp.glsl — the Nanite foliage cull (render/nfoliage.go).
// The #version line and the defines are the Go side's, ahead of
// foliage_params.glsl.
//
// One workgroup per tile — a run of one variant's instances in the shared
// pool, on one terrain — and a thread per instance. An instance is asked, in
// order of cost, what foliage_cull_comp.glsl asks (is it thinned out at its
// distance, is its plant inside the frustum and not hidden behind the depth
// drawn so far), and then which of its variant's DAG clusters are in its CUT:
// a cluster whose error, seen from the camera, is within the threshold and
// whose parent's is not (cluster.LODWithin) — the per-cluster test that gives
// a complete cut with no traversal (internal/cluster's dag.go). A variant is
// a few dozen clusters at most, so the thread tests them all. Each one in the
// cut, inside the frustum and not occluded, becomes an ITEM (instance,
// terrain, cluster), and its draw command counts the references it will take
// — one for a triangle cluster, a part cluster's parts, a voxel cluster's
// bricks (nfoliage_scatter_comp.glsl writes them). A voxel cluster's bricks
// small on screen go, where the compute rasteriser runs, to its SMALL
// command, the rest to its boxes; the item says which (w).
//
// A shadow pass casts each variant's SHADOW PROXY instead of its cut (the
// classic far mesh, on the same bones: render/nfoliage_asset.go's
// buildProxy), culled cluster by cluster with the light's frustum and no
// Hi-Z.

layout(local_size_x = 64) in;

struct NFCluster {
    vec4 sphere;  // what it draws, plant frame at unit scale, with wind room
    vec4 lod;     // its own LOD sphere
    vec4 parent;  // its parent's
    vec4 errs;    // own error, parent error (< 0: a root), voxel size
    uvec4 info;   // kind, bucket, draw command, references per item
    uvec4 draw;   // first part / first brick, count, draw command for
                  // the compute rasteriser (voxels)
};

// x: first instance in the pool, y: how many, z: variant, w: terrain.
layout(std430, binding = 0) readonly buffer Tiles {
    uvec4 tiles[];
};
layout(std430, binding = 1) buffer Commands {
    uint cmds[];
};
layout(std430, binding = 2) writeonly buffer Items {
    uvec4 items[];
};
layout(std430, binding = 4) readonly buffer Clusters {
    NFCluster clusters[];
};
layout(std430, binding = 5) readonly buffer Buckets {
    uvec4 buckets[];
};
layout(std430, binding = 12) readonly buffer Terrains {
    mat4 terrainModel[];
};
layout(std430, binding = 13) buffer Counters {
    uint itemCount;
    uint itemCap;
    uint refCap;
    uint overflow;
};

uniform samplerBuffer instances;
uniform vec3 camPos;
uniform vec4 planes[6];
uniform mat4 projView;
uniform sampler2D hiz;
uniform ivec2 hizSize;
uniform int hizLevels;
uniform int useHiz;
uniform uint tileBase;
// The LOD test's k (cluster.LODWithin): pixels per unit over the threshold.
uniform float lodK;
// A shadow pass: only instances within shadowReach of the camera, and only
// those the thinning still draws at least half of (foliage_shadow_frag's
// rule).
uniform int shadowPass;
uniform float shadowReach;
// A voxel cluster's bricks under smallMax pixels across go to its SMALL
// command, for the compute rasteriser (0: it is not running), larger ones
// to its boxes (nfoliage_vox_vert.glsl); pxPerUnit is the view's pixels per
// unit at distance 1.
uniform float pxPerUnit;
uniform float smallMax;

bool inFrustum(vec3 c, float r) {
    for (int i = 0; i < 6; i++) {
        vec4 p = planes[i];
        if (dot(p.xyz, c) + p.w < -r * length(p.xyz)) {
            return false;
        }
    }
    return true;
}

// foliage_cull_comp.glsl's test: every texel under the sphere's screen
// rectangle is nearer than the sphere's nearest point.
bool occluded(vec3 c, float r) {
    if (useHiz == 0) {
        return false;
    }
    vec2 lo = vec2(1.0);
    vec2 hi = vec2(0.0);
    float zNear = 1.0;
    for (int i = 0; i < 8; i++) {
        vec3 corner = c + r * vec3((i & 1) != 0 ? 1.0 : -1.0, (i & 2) != 0 ? 1.0 : -1.0, (i & 4) != 0 ? 1.0 : -1.0);
        vec4 clip = projView * vec4(corner, 1.0);
        if (clip.w <= 1e-3) {
            return false;
        }
        vec3 ndc = clip.xyz / clip.w;
        vec2 uv = ndc.xy * 0.5 + 0.5;
        lo = min(lo, uv);
        hi = max(hi, uv);
        zNear = min(zNear, ndc.z * 0.5 + 0.5);
    }
    lo = clamp(lo, 0.0, 1.0);
    hi = clamp(hi, 0.0, 1.0);
    if (any(greaterThanEqual(lo, hi))) {
        return false;
    }
    vec2 span = (hi - lo) * vec2(hizSize);
    int level = clamp(int(ceil(log2(max(max(span.x, span.y), 1.0)))), 0, hizLevels - 1);
    ivec2 size = max(hizSize >> level, ivec2(1));
    ivec2 a = clamp(ivec2(lo * vec2(size)), ivec2(0), size - 1);
    ivec2 b = clamp(ivec2(hi * vec2(size)), ivec2(0), size - 1);
    float far = max(max(texelFetch(hiz, a, level).r, texelFetch(hiz, ivec2(b.x, a.y), level).r),
                    max(texelFetch(hiz, ivec2(a.x, b.y), level).r, texelFetch(hiz, b, level).r));
    return zNear > far;
}

// cluster.LODWithin.
bool lodWithin(float errv, vec3 c, float r) {
    return errv * lodK <= max(length(c - camPos) - r, 1e-3);
}

// The instance's frame, for toWorld.
vec3 gAnchor;
vec3 gX, gUp, gZ;
vec3 gScale;
mat4 gModel;

vec3 toWorld(vec3 q) {
    vec3 lp = q * gScale;
    return (gModel * vec4(gAnchor + gX * lp.x + gUp * lp.y + gZ * lp.z, 1.0)).xyz;
}

void main() {
    uvec4 tile = tiles[tileBase + gl_WorkGroupID.x];
    uint bucket = tile.z;
    uint terrain = tile.w;
    gModel = terrainModel[terrain];
    float modelScale = length(gModel[1].xyz);
    int shape = bucketInfo[bucket].x;
    vec4 R = shapeRange[shape];
    uvec4 bk = buckets[bucket * 2];
    vec4 bs = uintBitsToFloat(buckets[bucket * 2 + 1]);
    uint firstCluster = bk.z;
    uint nClusters = bk.w;
    for (uint i = gl_LocalInvocationID.x; i < tile.y; i += gl_WorkGroupSize.x) {
        uint idx = tile.x + i;
        vec4 ps = texelFetch(instances, int(idx) * 4);
        vec4 ua = texelFetch(instances, int(idx) * 4 + 1);
        vec4 cr = texelFetch(instances, int(idx) * 4 + 2);
        vec3 anchorW = (gModel * vec4(ps.xyz, 1.0)).xyz;
        float d = length(anchorW - camPos);
        float fade = foliageRankFade(foliageKeep(R, d), cr.w);
        if (fade <= 0.0) {
            continue;
        }
        if (shadowPass != 0 && (fade < 0.5 || d > shadowReach)) {
            continue;
        }
        // The plant's frame: foliage_common.glsl's foliageBegin.
        vec3 up = normalize(ua.xyz);
        vec3 ref = abs(up.y) < 0.95 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
        vec3 t0 = normalize(cross(ref, up));
        vec3 b0 = cross(up, t0);
        float c = cos(ua.w), s = sin(ua.w);
        gX = t0 * c + b0 * s;
        gZ = cross(gX, up);
        gUp = up;
        gAnchor = ps.xyz;
        float comp = foliageComp(R, d);
        float scale = ps.w;
        if (shapeMore[shape].y > 0.5) {
            scale *= mix(0.25, 1.0, fade);
        }
        gScale = vec3(comp, pow(comp, shapeMore[shape].x), comp) * scale;
        // Errors and radii scale by the largest stretch: never coarser than
        // the plant needs.
        float sMax = max(max(gScale.x, gScale.y), gScale.z) * modelScale;
        vec3 wc = toWorld(bs.xyz);
        float wr = bs.w * sMax;
        if (!inFrustum(wc, wr) || occluded(wc, wr)) {
            continue;
        }
        for (uint k = 0u; k < nClusters; k++) {
            NFCluster cl = clusters[firstCluster + k];
            // A shadow pass casts the variant's shadow proxy and nothing
            // else; a camera pass never draws it.
            bool proxy = cl.info.x == 3u;
            if (proxy != (shadowPass != 0)) {
                continue;
            }
            if (!proxy && !lodWithin(cl.errs.x * sMax, toWorld(cl.lod.xyz), cl.lod.w * sMax)) {
                continue;
            }
            if (!proxy && cl.errs.y >= 0.0 && lodWithin(cl.errs.y * sMax, toWorld(cl.parent.xyz), cl.parent.w * sMax)) {
                continue;
            }
            vec3 cw = toWorld(cl.sphere.xyz);
            float cr2 = cl.sphere.w * sMax;
            if (!inFrustum(cw, cr2) || occluded(cw, cr2)) {
                continue;
            }
            uint slot = atomicAdd(itemCount, 1u);
            if (slot >= itemCap) {
                atomicOr(overflow, 1u);
                continue;
            }
            items[slot] = uvec4(idx, terrain, firstCluster + k, 0u);
            uint cmd = cl.info.z;
            uint mult = cl.info.w;
            if (cl.info.x == 2u && smallMax > 0.0) {
                float brick = 4.0 * 1.7320508 * cl.errs.z * sMax;
                float px = brick * pxPerUnit / max(length(cw - camPos) - cr2, 1e-3);
                if (px <= smallMax) {
                    cmd = cl.draw.z;
                    items[slot].w = 1u;
                }
            }
            atomicAdd(cmds[cmd * 5u + 1u], mult);
        }
    }
}
