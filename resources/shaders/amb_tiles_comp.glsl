#version 430
// amb_tiles_comp.glsl — the ambient volumes each screen tile can see
// (render/amb_tiles.go, amb_tiles.glsl). One workgroup a tile: the farthest
// depth over the tile, grown by atPad pixels each way (TAA's jitter, and the
// shafts' half-resolution distances, which may come from a pixel next door),
// makes the tile's frustum from the eye to that depth; its world box against
// each volume's box gives the tile's bit for it. Conservative only ever
// toward keeping a volume.

layout(local_size_x = 8, local_size_y = 8) in;

uniform sampler2D atDepth;  // the scene's depth, [0, 1]
uniform mat4  atInvVP;      // clip to world, the matrix the depth was drawn with
uniform ivec2 atSize;       // the depth's size in pixels
uniform int   atTile;       // a tile's side in pixels
uniform int   atPad;        // pixels the tile is grown by each way
uniform ivec2 atCount;      // tiles across, down
uniform int   atVolumes;    // volumes packed, ≤ 8
uniform vec3  atLo[8];
uniform vec3  atHi[8];

layout(std430, binding = 0) writeonly buffer AtMasks {
    uint atMask[];
};

shared float atMax[64];

void main() {
    ivec2 tile = ivec2(gl_WorkGroupID.xy);
    ivec2 o = tile * atTile - atPad;
    ivec2 e = o + atTile + 2 * atPad;
    float m = 0.0;
    for (int y = o.y + int(gl_LocalInvocationID.y); y < e.y; y += 8) {
        for (int x = o.x + int(gl_LocalInvocationID.x); x < e.x; x += 8) {
            ivec2 q = clamp(ivec2(x, y), ivec2(0), atSize - 1);
            m = max(m, texelFetch(atDepth, q, 0).r);
        }
    }
    uint li = gl_LocalInvocationIndex;
    atMax[li] = m;
    barrier();
    for (uint s = 32u; s > 0u; s >>= 1u) {
        if (li < s) {
            atMax[li] = max(atMax[li], atMax[li + s]);
        }
        barrier();
    }
    if (li != 0u) {
        return;
    }
    float zFar = atMax[0] * 2.0 - 1.0;
    vec2 p0 = vec2(o) / vec2(atSize) * 2.0 - 1.0;
    vec2 p1 = vec2(e) / vec2(atSize) * 2.0 - 1.0;
    vec3 lo = vec3(1e30), hi = vec3(-1e30);
    for (int c = 0; c < 8; c++) {
        vec4 ndc = vec4((c & 1) == 0 ? p0.x : p1.x, (c & 2) == 0 ? p0.y : p1.y, (c & 4) == 0 ? -1.0 : zFar, 1.0);
        vec4 w = atInvVP * ndc;
        vec3 pw = w.xyz / w.w;
        lo = min(lo, pw);
        hi = max(hi, pw);
    }
    uint mask = 0u;
    for (int i = 0; i < 8; i++) {
        if (i >= atVolumes) {
            break;
        }
        if (all(lessThanEqual(lo, atHi[i])) && all(greaterThanEqual(hi, atLo[i]))) {
            mask |= 1u << uint(i);
        }
    }
    atMask[tile.y * atCount.x + tile.x] = mask;
}
