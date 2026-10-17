// bloom_comp.glsl — the glow round bright light (render/bloom.go;
// docs/lighting/PLAN.md, phase 4): Jimenez's mip chain ("Next Generation Post
// Processing in Call of Duty: Advanced Warfare"). DOWN: each mip from the one
// above by a 13-tap filter, the first with Karis's average so a lone bright
// texel cannot flicker the whole glow; UP: each mip, smallest first, adds a
// 3x3 tent of the one below it to itself. Mip 0 then holds every scale's
// glow summed, which the frame's output mixes in before the curve.
//
// #version is spliced ahead of it.

layout(local_size_x = 8, local_size_y = 8) in;

layout(rgba16f, binding = 0) uniform image2D bloomDst;

uniform sampler2D bloomSrc;   // DOWN: the level above (or the frame); UP: the bloom chain
uniform float srcLod;         // the level of bloomSrc to read
uniform vec2  srcTexel;       // 1 / its size
uniform ivec2 dstSize;
uniform int   upPass;         // 0 down, 1 up
uniform int   karis;          // the first downsample

vec3 tap(vec2 uv) {
    return textureLod(bloomSrc, uv, srcLod).rgb;
}

float karisWeight(vec3 c) {
    return 1.0 / (1.0 + dot(c, vec3(0.2126, 0.7152, 0.0722)));
}

void main() {
    ivec2 p = ivec2(gl_GlobalInvocationID.xy);
    if (p.x >= dstSize.x || p.y >= dstSize.y) {
        return;
    }
    vec2 uv = (vec2(p) + 0.5) / vec2(dstSize);
    vec2 t = srcTexel;
    if (upPass == 0) {
        vec3 a = tap(uv + t * vec2(-2.0, 2.0)), b = tap(uv + t * vec2(0.0, 2.0)), c = tap(uv + t * vec2(2.0, 2.0));
        vec3 d = tap(uv + t * vec2(-2.0, 0.0)), e = tap(uv), f = tap(uv + t * vec2(2.0, 0.0));
        vec3 g = tap(uv + t * vec2(-2.0, -2.0)), h = tap(uv + t * vec2(0.0, -2.0)), i = tap(uv + t * vec2(2.0, -2.0));
        vec3 j = tap(uv + t * vec2(-1.0, 1.0)), k = tap(uv + t * vec2(1.0, 1.0));
        vec3 l = tap(uv + t * vec2(-1.0, -1.0)), m = tap(uv + t * vec2(1.0, -1.0));
        vec3 r;
        if (karis != 0) {
            vec3 g0 = (a + b + d + e) * 0.25, g1 = (b + c + e + f) * 0.25;
            vec3 g2 = (d + e + g + h) * 0.25, g3 = (e + f + h + i) * 0.25;
            vec3 g4 = (j + k + l + m) * 0.25;
            float w0 = karisWeight(g0), w1 = karisWeight(g1), w2 = karisWeight(g2), w3 = karisWeight(g3), w4 = karisWeight(g4);
            r = (g0 * w0 * 0.125 + g1 * w1 * 0.125 + g2 * w2 * 0.125 + g3 * w3 * 0.125 + g4 * w4 * 0.5)
                / (w0 * 0.125 + w1 * 0.125 + w2 * 0.125 + w3 * 0.125 + w4 * 0.5);
        } else {
            r = e * 0.125 + (a + c + g + i) * 0.03125 + (b + d + f + h) * 0.0625 + (j + k + l + m) * 0.125;
        }
        imageStore(bloomDst, p, vec4(max(r, vec3(0.0)), 1.0));
    } else {
        vec3 r = tap(uv + t * vec2(-1.0, 1.0)) + tap(uv + t * vec2(1.0, 1.0))
               + tap(uv + t * vec2(-1.0, -1.0)) + tap(uv + t * vec2(1.0, -1.0))
               + 2.0 * (tap(uv + t * vec2(0.0, 1.0)) + tap(uv + t * vec2(0.0, -1.0))
                      + tap(uv + t * vec2(-1.0, 0.0)) + tap(uv + t * vec2(1.0, 0.0)))
               + 4.0 * tap(uv);
        r /= 16.0;
        vec3 own = imageLoad(bloomDst, p).rgb;
        imageStore(bloomDst, p, vec4(own + r, 1.0));
    }
}
