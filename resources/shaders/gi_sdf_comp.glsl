// gi_sdf_comp.glsl — writing bricks of the GI distance field (render/gi_sdf.go).
//
// One workgroup per brick of 8³ voxels, one invocation per voxel. Each voxel
// takes the union (the minimum) of every source that reaches its brick — the
// CPU has already listed which those are — and the surface colour of the one
// that won. Distances are clamped to GI_SDF_BAND voxels: far from everything,
// all a trace needs to know is that it may take a long step.

layout(local_size_x = 8, local_size_y = 8, local_size_z = 8) in;

layout(r16f, binding = 0) uniform writeonly image3D sdfImage;
layout(rgba8, binding = 1) uniform writeonly image3D surfImage;

#define GI_SDF_RES 128
#define GI_SDF_BAND 4.0

// The kinds of source (render/gi_sources.go, giKind*).
#define KIND_HULL 0
#define KIND_CAPSULE 1
#define KIND_MESH 2
#define KIND_TERRAIN 3

struct Brick {
    ivec4 brick; // xyz the brick's world brick index
    uvec4 list;  // x the first entry in srcIndex, y how many
};

struct Source {
    vec4 lo;     // world bounds; lo.w the kind
    vec4 hi;
    vec4 color;  // albedo, emissive
    vec4 p0;     // by kind — see render/gi_sources.go
    vec4 p1;
    vec4 c1;     // terrain: layers 2..4's albedo
    vec4 c2;
    vec4 c3;
    mat4 m;      // mesh, terrain: world -> the source's own grid
};

layout(std430, binding = 0) readonly buffer Bricks { Brick bricks[]; };
layout(std430, binding = 1) readonly buffer Lists { uint srcIndex[]; };
layout(std430, binding = 2) readonly buffer Sources { Source src[]; };
layout(std430, binding = 3) readonly buffer Planes { vec4 planes[]; };
layout(std430, binding = 4) readonly buffer MeshData { float meshData[]; };
layout(std430, binding = 5) readonly buffer BlockData { uint blockData[]; };

uniform float voxel;   // this level's voxel size
uniform uint brickBase; // the first brick of this dispatch

float hullDist(Source s, vec3 p) {
    uint off = uint(s.p0.x), n = uint(s.p0.y);
    float d = -1e9;
    for (uint i = 0u; i < n; i++) {
        vec4 pl = planes[off + i];
        d = max(d, dot(pl.xyz, p) - pl.w);
    }
    return d;
}

float capsuleDist(Source s, vec3 p) {
    vec3 a = s.p0.xyz, b = s.p1.xyz;
    vec3 ab = b - a;
    float h = clamp(dot(p - a, ab) / max(dot(ab, ab), 1e-8), 0.0, 1.0);
    return length(p - a - ab * h) - s.p0.w;
}

float meshCell(uint off, int n, ivec3 c) {
    c = clamp(c, ivec3(0), ivec3(n - 1));
    return meshData[off + uint((c.z * n + c.y) * n + c.x)];
}

float meshDist(Source s, vec3 p) {
    uint off = uint(s.p0.x);
    int n = int(s.p0.y);
    vec3 g = (s.m * vec4(p, 1.0)).xyz; // grid coordinates, voxel centres at integers
    vec3 gc = clamp(g, vec3(0.0), vec3(float(n - 1)));
    float outside = length(g - gc);
    ivec3 b = ivec3(floor(gc));
    vec3 f = gc - vec3(b);
    float c000 = meshCell(off, n, b);
    float c100 = meshCell(off, n, b + ivec3(1, 0, 0));
    float c010 = meshCell(off, n, b + ivec3(0, 1, 0));
    float c110 = meshCell(off, n, b + ivec3(1, 1, 0));
    float c001 = meshCell(off, n, b + ivec3(0, 0, 1));
    float c101 = meshCell(off, n, b + ivec3(1, 0, 1));
    float c011 = meshCell(off, n, b + ivec3(0, 1, 1));
    float c111 = meshCell(off, n, b + ivec3(1, 1, 1));
    float inner = mix(mix(mix(c000, c100, f.x), mix(c010, c110, f.x), f.y),
                      mix(mix(c001, c101, f.x), mix(c011, c111, f.x), f.y), f.z);
    return (outside + inner) * s.p0.z; // grid cells -> world units
}

uint terrainBlock(Source s, ivec3 c) {
    ivec3 size = ivec3(s.p0.yzw);
    if (any(lessThan(c, ivec3(0))) || any(greaterThanEqual(c, size))) {
        return 0u;
    }
    uint i = uint(s.p1.y) + uint((c.z * size.y + c.y) * size.x + c.x);
    return (blockData[i >> 2] >> ((i & 3u) * 8u)) & 0xffu;
}

// terrainDist is the distance to the nearest block of the other kind — solid
// from air, air from solid — searched a few blocks out; past that, the band.
float terrainDist(Source s, vec3 p, out uint layer) {
    vec3 g = (s.m * vec4(p, 1.0)).xyz; // block coordinates: block c spans [c, c+1)
    ivec3 c = ivec3(floor(g));
    float blockWorld = s.p1.x;
    uint here = terrainBlock(s, c);
    bool solid = here != 0u;
    int r = int(clamp(ceil(GI_SDF_BAND * voxel / blockWorld), 1.0, 2.0));
    float best = 1e9;
    layer = here;
    for (int z = -r; z <= r; z++) {
        for (int y = -r; y <= r; y++) {
            for (int x = -r; x <= r; x++) {
                ivec3 n = c + ivec3(x, y, z);
                uint b = terrainBlock(s, n);
                if ((b != 0u) == solid) {
                    continue;
                }
                vec3 q = clamp(g, vec3(n), vec3(n) + 1.0);
                float d = length(g - q);
                if (d < best) {
                    best = d;
                    if (!solid) {
                        layer = b;
                    }
                }
            }
        }
    }
    float d = min(best * blockWorld, GI_SDF_BAND * voxel);
    return solid ? -d : d;
}

void main() {
    Brick br = bricks[brickBase + gl_WorkGroupID.x];
    ivec3 v = br.brick.xyz * 8 + ivec3(gl_LocalInvocationID);
    vec3 p = (vec3(v) + 0.5) * voxel;

    float band = GI_SDF_BAND * voxel;
    float best = band;
    vec4 color = vec4(0.5, 0.5, 0.5, 0.0);
    for (uint i = 0u; i < br.list.y; i++) {
        Source s = src[srcIndex[br.list.x + i]];
        int kind = int(s.lo.w);
        float d;
        vec4 c = s.color;
        if (kind == KIND_HULL) {
            d = hullDist(s, p);
        } else if (kind == KIND_CAPSULE) {
            d = capsuleDist(s, p);
        } else if (kind == KIND_MESH) {
            d = meshDist(s, p);
        } else {
            uint layer;
            d = terrainDist(s, p, layer);
            if (layer == 2u) c = s.c1;
            else if (layer == 3u) c = s.c2;
            else if (layer >= 4u) c = s.c3;
        }
        if (d < best) {
            best = d;
            color = c;
        }
    }

    ivec3 texel = v - GI_SDF_RES * ivec3(floor(vec3(v) / float(GI_SDF_RES)));
    imageStore(sdfImage, texel, vec4(clamp(best, -band, band)));
    imageStore(surfImage, texel, color);
}
