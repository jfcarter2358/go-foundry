// foliage_cull_comp.glsl — the GPU cull for terrain foliage
// (render/foliage_gpu.go). The #version line is the Go side's, ahead
// of foliage_params.glsl.
//
// One workgroup per tile: a run of one bucket's instances in the shared pool,
// the part of a unit's batch the CPU's rank prefix still admits. Each thread
// takes instances of it in turn and asks, in order of cost: is it thinned
// out at its distance; is its bounding sphere inside the frustum; is the
// sphere hidden behind what is already drawn (the Hi-Z pyramid — nearest
// depth of the sphere against the furthest depth under it). A survivor is
// written into the output list of every tier whose window it is in (two in a
// cross-fade band), counted by an atomic add on that tier's indirect draw.

layout(local_size_x = 64) in;

// x: first instance in the pool, y: how many, z: bucket.
layout(std430, binding = 0) readonly buffer Tiles {
    uvec4 tiles[];
};
// DrawElementsIndirectCommand: count, instanceCount, firstIndex, baseVertex,
// baseInstance.
layout(std430, binding = 1) buffer Commands {
    uint cmds[];
};
// x: the instance in the pool, y: bucket | tier << 8 — foliage_common.glsl.
layout(std430, binding = 2) writeonly buffer Refs {
    uvec2 refs[];
};

uniform samplerBuffer instances;
uniform mat4 model;
uniform vec3 camPos;
uniform vec4 planes[6];
uniform mat4 projView;
uniform sampler2D hiz;
uniform ivec2 hizSize;
uniform int hizLevels;
uniform int useHiz;
uniform uint tileBase;
uniform uint cmdBase;
// Per bucket: its tier 0, 1 and 2 command (relative to cmdBase; -1: none),
// and in w the most instances any one of them can be handed.
uniform ivec4 bucketCmd[FOLIAGE_BUCKETS];
// Per shape: the radius its plant fits in at scale 1 (world.FoliageExtent).
uniform float shapeExtent[FOLIAGE_SHAPES];

bool inFrustum(vec3 c, float r) {
    for (int i = 0; i < 6; i++) {
        vec4 p = planes[i];
        if (dot(p.xyz, c) + p.w < -r * length(p.xyz)) {
            return false;
        }
    }
    return true;
}

// Hidden behind the depth already drawn: every texel under the sphere's
// screen rectangle is nearer than the sphere's nearest point.
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

void emit(uint idx, uint bucket, int tier, int cmd, uint cap) {
    if (cmd < 0) {
        return;
    }
    uint c = (cmdBase + uint(cmd)) * 5u;
    uint slot = atomicAdd(cmds[c + 1u], 1u);
    if (slot < cap) {
        refs[cmds[c + 4u] + slot] = uvec2(idx, bucket | (uint(tier) << 8u));
    }
}

void main() {
    uvec4 tile = tiles[tileBase + gl_WorkGroupID.x];
    uint bucket = tile.z;
    ivec4 cmd = bucketCmd[bucket];
    int shape = bucketInfo[bucket].x;
    vec4 R = shapeRange[shape];
    mat3 m3 = mat3(model);
    for (uint i = gl_LocalInvocationID.x; i < tile.y; i += gl_WorkGroupSize.x) {
        uint idx = tile.x + i;
        vec4 ps = texelFetch(instances, int(idx) * 4);
        vec4 cr = texelFetch(instances, int(idx) * 4 + 2);
        vec3 anchor = (model * vec4(ps.xyz, 1.0)).xyz;
        float d = length(anchor - camPos);
        if (foliageRankFade(foliageKeep(R, d), cr.w) <= 0.0) {
            continue;
        }
        vec4 ua = texelFetch(instances, int(idx) * 4 + 1);
        float r = shapeExtent[shape] * ps.w * foliageComp(R, d) * length(m3[1]);
        vec3 c = anchor + normalize(m3 * ua.xyz) * r * 0.5;
        if (!inFrustum(c, r) || occluded(c, r)) {
            continue;
        }
        for (int t = 0; t < 3; t++) {
            if (cmd[t] < 0) {
                continue;
            }
            vec2 w = foliageTierWeights(foliageTierWindow(shape, t), d);
            if (w.x > 0.0 && w.y > 0.0) {
                emit(idx, bucket, t, cmd[t], uint(cmd.w));
            }
        }
    }
}
