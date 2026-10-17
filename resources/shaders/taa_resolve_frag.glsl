#version 430

// taa_resolve_frag.glsl — temporal anti-aliasing's resolve (render/taa.go).
//
// The frame was drawn from a camera nudged by a different fraction of a pixel
// each frame (gfx.ProjectionJitter, a Halton sequence), so over a few frames a
// pixel has been sampled at several places inside itself — which is what
// multisampling buys in one frame. This blends the new frame into the
// history of all the earlier ones:
//
//   1. Reproject: the history is read where this pixel's surface was last
//      frame (motion, taa_velocity_frag.glsl), through a Catmull-Rom filter
//      so the resampling every frame does not blur it.
//   2. Clamp: the history is only believed as far as this frame's 3x3
//      neighbourhood says it could be right — clipped toward the
//      neighbourhood's mean, within its variance, in YCoCg. A surface that
//      was uncovered this frame, or changed colour, has a history the
//      neighbourhood rejects; that, not motion vectors alone, is what stops
//      ghosting.
//   3. Blend: about a tenth of the new frame each time, more where the
//      pixel moves fast. A straight average: weighting it by inverse
//      luminance (Karis's anti-flicker) darkens every thin bright feature
//      — a cutout's dithered edge lost 0.8% of its area to it — and the
//      frame is low dynamic range, with no fireflies to tame.
//
// A pixel whose history is off screen, or a frame with no history at all (the
// first, a cut, a resize), takes the new frame as it is.
//
// NOT A NUMBER. A NaN or infinite pixel in the frame is read as black here
// (finite3), never blended. Unguarded, one NaN poisons the 3x3 statistics of
// every neighbour that reads it, and the RGBA16F history keeps the result:
// the NaN grows by a pixel each way every frame into a solid block. That is
// how a handful of NaN-lit triangles (a normal map on a surface with
// constant UVs, docs/regions/PRESETS_4.md) became white blocks over the
// biome showcase's scanned scenery from 600 m up. The source was fixed;
// this keeps the next one a pixel.

in vec2 vUV;

uniform sampler2D current;
uniform sampler2D history;
uniform sampler2D motion;
uniform int historyValid;
uniform vec2 size;
uniform float blendCurrent; // the new frame's share at rest
// UPSAMPLING (render/dynres.go; docs/adventure/PHASE_F.md, Phase F2). With
// upscale set, `current` and `motion` are renderSize — smaller than this
// output-sized history — and render pixel i holds the scene at
// i + 0.5 - jitterPx (render pixels: the projection was moved by the jitter,
// so its centre sampled the point jitterPx behind it). See resolveUpscaled.
uniform int upscale;
uniform vec2 renderSize;
uniform vec2 jitterPx;

out vec4 outColor;

// finite3 is c, or black when any channel is NaN or infinite.
vec3 finite3(vec3 c) {
    return (any(isnan(c)) || any(isinf(c))) ? vec3(0.0) : c;
}

vec3 toYCoCg(vec3 c) {
    return vec3(0.25 * c.r + 0.5 * c.g + 0.25 * c.b,
                0.5 * c.r - 0.5 * c.b,
               -0.25 * c.r + 0.5 * c.g - 0.25 * c.b);
}

vec3 fromYCoCg(vec3 c) {
    return vec3(c.x + c.y - c.z, c.x + c.z, c.x - c.y - c.z);
}

// Catmull-Rom in nine bilinear taps (the outer corners left out), after
// Jimenez's: sharper than a bilinear read, which would soften the history a
// little every frame it is moved.
vec3 sampleHistory(vec2 uv) {
    vec2 pos = uv * size;
    vec2 t1 = floor(pos - 0.5) + 0.5;
    vec2 f = pos - t1;
    vec2 w0 = f * (-0.5 + f * (1.0 - 0.5 * f));
    vec2 w1 = 1.0 + f * f * (-2.5 + 1.5 * f);
    vec2 w2 = f * (0.5 + f * (2.0 - 1.5 * f));
    vec2 w3 = f * f * (-0.5 + 0.5 * f);
    vec2 w12 = w1 + w2;
    vec2 t0 = (t1 - 1.0) / size;
    vec2 t3 = (t1 + 2.0) / size;
    vec2 t12 = (t1 + w2 / w12) / size;
    vec3 r = vec3(0.0);
    r += textureLod(history, vec2(t12.x, t0.y), 0.0).rgb * w12.x * w0.y;
    r += textureLod(history, vec2(t0.x, t12.y), 0.0).rgb * w0.x * w12.y;
    r += textureLod(history, vec2(t12.x, t12.y), 0.0).rgb * w12.x * w12.y;
    r += textureLod(history, vec2(t3.x, t12.y), 0.0).rgb * w3.x * w12.y;
    r += textureLod(history, vec2(t12.x, t3.y), 0.0).rgb * w12.x * w3.y;
    float wsum = w12.x * w0.y + w0.x * w12.y + w12.x * w12.y + w3.x * w12.y + w12.x * w3.y;
    return max(finite3(r / max(wsum, 1e-4)), vec3(0.0));
}

// Clips h toward the centre of the box [lo, hi] (Playdead's clip_aabb): the
// point where the segment from the box's centre to h leaves the box.
vec3 clipToBox(vec3 lo, vec3 hi, vec3 h) {
    vec3 c = 0.5 * (hi + lo);
    vec3 e = 0.5 * (hi - lo) + 1e-5;
    vec3 v = h - c;
    vec3 a = abs(v / e);
    float m = max(a.x, max(a.y, a.z));
    return m > 1.0 ? c + v / m : h;
}

// resolveUpscaled is main's resolve for an output pixel when the frame was
// drawn smaller than the output (UE4's TAAU, after Karis): this frame's
// colour at the pixel is the 3x3 render samples round it weighted by a
// Gaussian of their distance from its centre (exp(-2.29 d²), d in render
// pixels: a Blackman-Harris fit); the clamp box is the same nine; and the
// new frame's share is scaled by how near its nearest sample fell (d in
// output pixels), normalised so its mean over a render pixel is the old
// share — a pixel no sample came near this frame keeps its history.
vec4 resolveUpscaled() {
    vec2 rp = gl_FragCoord.xy / size * renderSize; // output centre, render px
    vec2 c = rp - 0.5 + jitterPx;                   // in sample indices
    ivec2 base = ivec2(floor(c + 0.5));
    ivec2 last = ivec2(renderSize) - 1;
    vec3 sum = vec3(0.0);
    float wsum = 0.0;
    vec3 m1 = vec3(0.0), m2 = vec3(0.0);
    vec3 lo = vec3(1e9), hi = vec3(-1e9);
    float dmin = 1e9;
    for (int dy = -1; dy <= 1; dy++) {
        for (int dx = -1; dx <= 1; dx++) {
            ivec2 q = base + ivec2(dx, dy);
            vec3 s = toYCoCg(finite3(texelFetch(current, clamp(q, ivec2(0), last), 0).rgb));
            vec2 d = vec2(q) + 0.5 - jitterPx - rp;
            float d2 = dot(d, d);
            float w = exp(-2.29 * d2);
            sum += w * s;
            wsum += w;
            dmin = min(dmin, d2);
            m1 += s;
            m2 += s * s;
            lo = min(lo, s);
            hi = max(hi, s);
        }
    }
    vec3 cur = sum / max(wsum, 1e-5);
    vec2 uv = gl_FragCoord.xy / size;
    vec2 mv = texelFetch(motion, clamp(ivec2(rp), ivec2(0), last), 0).xy;
    vec2 prev = uv - mv;
    if (historyValid == 0 || any(lessThan(prev, vec2(0.0))) || any(greaterThan(prev, vec2(1.0)))) {
        return vec4(fromYCoCg(cur), 1.0);
    }
    vec3 mean = m1 / 9.0;
    vec3 sigma = sqrt(max(m2 / 9.0 - mean * mean, vec3(0.0)));
    vec3 boxLo = max(lo, mean - 1.25 * sigma);
    vec3 boxHi = min(hi, mean + 1.25 * sigma);
    vec3 h = clipToBox(boxLo, boxHi, toYCoCg(sampleHistory(prev)));
    // The nearest sample's distance in output pixels, and its weight's mean
    // over a render pixel's area (pi / 2.29 output px² over 1/s² of them).
    float s = renderSize.x / size.x;
    float conf = exp(-2.29 * dmin / (s * s)) / (1.372 * s * s);
    float speed = length(mv * size);
    float a = mix(blendCurrent, 0.5, clamp(speed / 32.0, 0.0, 1.0));
    a = clamp(a * conf, 0.0, 0.5);
    return vec4(fromYCoCg(mix(h, cur, a)), 1.0);
}

void main() {
    if (upscale != 0) {
        outColor = resolveUpscaled();
        return;
    }
    ivec2 p = ivec2(gl_FragCoord.xy);
    ivec2 last = ivec2(size) - 1;
    vec3 cur = toYCoCg(finite3(texelFetch(current, p, 0).rgb));
    vec3 m1 = vec3(0.0), m2 = vec3(0.0);
    vec3 lo = cur, hi = cur;
    for (int dy = -1; dy <= 1; dy++) {
        for (int dx = -1; dx <= 1; dx++) {
            vec3 s = toYCoCg(finite3(texelFetch(current, clamp(p + ivec2(dx, dy), ivec2(0), last), 0).rgb));
            m1 += s;
            m2 += s * s;
            lo = min(lo, s);
            hi = max(hi, s);
        }
    }
    vec2 mv = texelFetch(motion, p, 0).xy;
    vec2 prev = vUV - mv;
    if (historyValid == 0 || any(lessThan(prev, vec2(0.0))) || any(greaterThan(prev, vec2(1.0)))) {
        outColor = vec4(fromYCoCg(cur), 1.0);
        return;
    }
    vec3 mean = m1 / 9.0;
    vec3 sigma = sqrt(max(m2 / 9.0 - mean * mean, vec3(0.0)));
    vec3 boxLo = max(lo, mean - 1.25 * sigma);
    vec3 boxHi = min(hi, mean + 1.25 * sigma);
    vec3 h = clipToBox(boxLo, boxHi, toYCoCg(sampleHistory(prev)));

    // A fast-moving pixel's history has been resampled more and believed
    // less; take more of the new frame there.
    float speed = length(mv * size);
    float a = mix(blendCurrent, 0.5, clamp(speed / 32.0, 0.0, 1.0));
    outColor = vec4(fromYCoCg(mix(h, cur, a)), 1.0);
}
