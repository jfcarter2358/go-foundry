// foliage_params.glsl — how far each foliage shape reaches and how it thins,
// shared by every stage that decides whether an instance is drawn: the colour
// and depth vertex stages, the impostor stage and the GPU cull
// (render/foliage.go, render/foliage_gpu.go). One copy of the rules is what
// keeps the compute cull, the CPU's draw prefixes (foliageKeep in Go mirrors
// these) and the vertex stage agreeing about the same instance.
//
// FOLIAGE_SHAPES and FOLIAGE_BUCKETS are defined by the Go side ahead of this
// file. A bucket is one (shape, variant): one template.

// Per shape: draw range, the distance thinning starts, how steeply it thins
// (the fraction drawn falls as (start/d)^pow; 0 keeps everything until the
// final fade) and how much of the lost coverage the survivors make up by
// growing (1: all of it — the fraction drawn times each one's footprint
// stays constant).
uniform vec4 shapeRange[FOLIAGE_SHAPES];
// Per shape: how much of that growth goes into height (the rest is width),
// whether it sinks into the ground as it fades out, the near-to-far mesh
// distance and the distance its far tier (clump cards, or an impostor) takes
// over (0: it has none).
uniform vec4 shapeMore[FOLIAGE_SHAPES];
// Per bucket: its shape, and its impostor's layer (-1: none).
uniform ivec2 bucketInfo[FOLIAGE_BUCKETS];
// Per bucket: its impostor's centre (plant frame, unit scale) and radius.
uniform vec4 bucketImp[FOLIAGE_BUCKETS];

// The rank band an instance fades over as the drawn fraction passes it.
const float FOLIAGE_RANK_BAND = 0.06;
// The most a survivor grows to cover for the ones thinned out around it.
const float FOLIAGE_MAX_COMP = 4.0;

// The fraction of shape R's instances the thinning still draws at d, before
// the fade at the end of the range.
float foliageThin(vec4 R, float d) {
    return (R.z > 0.0 && d > R.y) ? pow(R.y / d, R.z) : 1.0;
}

// The fraction of shape R's instances drawn at distance d.
float foliageKeep(vec4 R, float d) {
    return foliageThin(R, d) * (1.0 - smoothstep(R.x * 0.82, R.x, d));
}

// How much an instance of rank `rank` is still drawn when keep of them are:
// 1 well under it, fading to 0 over a band FOLIAGE_RANK_BAND of the survivors
// wide — a fraction of them, not of every rank, or where only a tenth
// survive most of the survivors would be mid-dissolve. Scaled so that at
// keep = 1 even the highest rank is wholly in.
float foliageRankFade(float keep, float rank) {
    float band = FOLIAGE_RANK_BAND * max(keep, 1e-3);
    return clamp((keep * (1.0 + FOLIAGE_RANK_BAND) - rank) / band, 0.0, 1.0);
}

// How much a survivor grows at d: the inverse square root of the thinning,
// so its footprint (which goes as the square) makes up what was thinned.
float foliageComp(vec4 R, float d) {
    return min(pow(foliageThin(R, d), -0.5 * R.w), FOLIAGE_MAX_COMP);
}

// A tier's window is the distance range it draws over, (from, to), 0 for
// open-ended. It fades in over the band below `from` and out over the band
// below `to`, so the tier on either side of a boundary dissolves into the
// other through complementary dithers (foliage_frag.glsl).
float foliageBand(float x) {
    return 0.12 * x;
}

vec2 foliageTierWeights(vec2 win, float d) {
    float wIn = win.x > 0.0 ? smoothstep(win.x - foliageBand(win.x), win.x, d) : 1.0;
    float wOut = win.y > 0.0 ? 1.0 - smoothstep(win.y - foliageBand(win.y), win.y, d) : 1.0;
    return vec2(wIn, wOut);
}

// The window of tier 0 (near mesh), 1 (far mesh) or 2 (clump cards or
// impostor) of a shape, when every instance picks its own tier (the GPU cull).
vec2 foliageTierWindow(int shape, int tier) {
    float lod = shapeMore[shape].z;
    float far = shapeMore[shape].w;
    if (tier == 0) {
        return vec2(0.0, lod);
    }
    if (tier == 1) {
        return vec2(lod, far);
    }
    return vec2(far, 0.0);
}
