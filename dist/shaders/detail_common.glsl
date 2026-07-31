// detail_common.glsl — shared deterministic displacement (summed sine waves plus
// optional fractal noise) for procedural face detail. Concatenated (after the
// #version line) into every tessellation
// stage of the detail programs so the lit surface and its shadow silhouette use
// the exact same displacement. NOT injected into the reused fragment shaders.

uniform float dispSeed; // per-face seed

// Unit normal of the face's *average* plane, supplied by the CPU. Displacement
// direction and the in-plane wave basis both come from this rather than from the
// geometric normal of the patch being evaluated: a floor/ceiling quad whose four
// corners have independent heights is not planar, so its two triangles have
// different geometric normals and would displace along different directions —
// tearing the surface open along the shared diagonal. One constant normal per
// face makes the displacement a pure function of world position, so any two
// patches agree on every shared edge.
uniform vec3 dispNormal;

// Up to DETAIL_MAX_WAVES sine layers are summed to shape the surface. A slot with
// zero amplitude or zero frequency contributes nothing (disabled in the editor).
#define DETAIL_MAX_WAVES 3
uniform float waveAmp[DETAIL_MAX_WAVES];  // peak displacement along the normal
uniform float waveFreq[DETAIL_MAX_WAVES]; // cycles per world unit
uniform float waveRot[DETAIL_MAX_WAVES];  // travel direction in the face plane, radians

// Smooth shading, mirroring the owning surface's setting. When set, the
// evaluation stage shades from detailNormal() below instead of letting the
// fragment stage recover a flat facet per generated triangle.
uniform int   smoothShading;
// Central-difference step detailNormal() differentiates over, sized CPU-side
// from the finest feature this spec can produce (see dispNormalEps in
// detail_render.go).
uniform float dispNormalEps;

// Optional fractal value-noise added on top of the summed waves.
uniform int   noiseOn;   // 0 = waves only
uniform float noiseAmp;  // max noise displacement along the normal
uniform float noiseFreq; // noise features per world unit
uniform int   noiseOct;  // fractal octaves (1..6)

// Edge taper: fades displacement to 0 within `edgeTaper` world units of the
// face's outer boundary so the surface meets adjacent walls seamlessly. The
// boundary is supplied as world-space edge segments (edgeA[i]..edgeB[i]).
uniform float edgeTaper; // taper band width; <= 0 disables tapering
uniform int   numEdges;
#define DETAIL_MAX_EDGES 32
uniform vec3  edgeA[DETAIL_MAX_EDGES];
uniform vec3  edgeB[DETAIL_MAX_EDGES];

// Distance from point p to segment a..b.
float d_distSeg(vec3 p, vec3 a, vec3 b) {
    vec3 ab = b - a;
    float t = clamp(dot(p - a, ab) / max(dot(ab, ab), 1e-8), 0.0, 1.0);
    return length(p - (a + ab * t));
}

// detailTaper returns 1 in the interior and ramps to 0 at the boundary. Because
// it is a function of world position, adjacent tessellated triangles agree on
// shared edges (C0), so the interior stays crack-free while the perimeter — the
// edges shared with walls — is pinned exactly in place.
float detailTaper(vec3 w) {
    if (edgeTaper <= 0.0 || numEdges <= 0) {
        return 1.0;
    }
    float md = 1e20;
    for (int i = 0; i < DETAIL_MAX_EDGES; i++) {
        if (i >= numEdges) break;
        md = min(md, d_distSeg(w, edgeA[i], edgeB[i]));
    }
    return smoothstep(0.0, edgeTaper, md);
}

float d_hash13(vec3 p) {
    p = fract(p * 0.3183099 + vec3(0.1, 0.2, 0.3));
    p += dot(p, p.yzx + 19.19);
    return fract((p.x + p.y) * p.z);
}

// 3D value noise in [0,1].
float d_vnoise(vec3 x) {
    vec3 i = floor(x);
    vec3 f = fract(x);
    vec3 u = f * f * (3.0 - 2.0 * f);
    float n000 = d_hash13(i + vec3(0.0, 0.0, 0.0));
    float n100 = d_hash13(i + vec3(1.0, 0.0, 0.0));
    float n010 = d_hash13(i + vec3(0.0, 1.0, 0.0));
    float n110 = d_hash13(i + vec3(1.0, 1.0, 0.0));
    float n001 = d_hash13(i + vec3(0.0, 0.0, 1.0));
    float n101 = d_hash13(i + vec3(1.0, 0.0, 1.0));
    float n011 = d_hash13(i + vec3(0.0, 1.0, 1.0));
    float n111 = d_hash13(i + vec3(1.0, 1.0, 1.0));
    float nx00 = mix(n000, n100, u.x);
    float nx10 = mix(n010, n110, u.x);
    float nx01 = mix(n001, n101, u.x);
    float nx11 = mix(n011, n111, u.x);
    float nxy0 = mix(nx00, nx10, u.y);
    float nxy1 = mix(nx01, nx11, u.y);
    return mix(nxy0, nxy1, u.z);
}

// Fractal Brownian motion in [-1,1], noiseOct octaves.
float d_fbm(vec3 p) {
    float sum = 0.0;
    float amp = 0.5;
    float freq = 1.0;
    for (int i = 0; i < 6; i++) {
        if (i >= noiseOct) break;
        sum += amp * (d_vnoise(p * freq) * 2.0 - 1.0);
        freq *= 2.0;
        amp *= 0.5;
    }
    return sum;
}

#define D_TWO_PI 6.28318530718

float d_hash21(vec2 p) {
    return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
}

// d_planeBasis fills the face's in-plane axes, derived from the face-constant
// dispNormal so every patch gets the same pair and the summed waves stay C0
// across patch edges. On a wall `t` is horizontal, so rotation 0 gives vertical
// ridges and 90 bands. (t, b, dispNormal) is right-handed: cross(t, b) is
// dispNormal, which is what makes detailNormal() come out facing outwards.
void d_planeBasis(out vec3 t, out vec3 b) {
    t = (abs(dispNormal.y) < 0.9) ? cross(vec3(0.0, 1.0, 0.0), dispNormal)
                                  : cross(vec3(1.0, 0.0, 0.0), dispNormal);
    t = normalize(t);
    b = normalize(cross(dispNormal, t));
}

// d_waveSum sums the enabled sine layers at world position `pos`. Each wave runs
// along its own direction *within the face plane* — set by waveRot, so it is
// authored rather than random — which makes it visible on floors, ceilings and
// walls alike. Only the phase comes from the seed, offset by a further third of a
// cycle per slot so slots that hash to similar values still don't crest together.
float d_waveSum(vec3 pos) {
    vec3 t, b;
    d_planeBasis(t, b);

    // Fold big seeds into a small range first, so the hash keeps float precision.
    float s = mod(dispSeed, 1024.0);

    float sum = 0.0;
    for (int i = 0; i < DETAIL_MAX_WAVES; i++) {
        if (waveAmp[i] == 0.0 || waveFreq[i] == 0.0) continue;
        float fi = float(i);
        float phase = (d_hash21(vec2(s, fi)) + fi / float(DETAIL_MAX_WAVES)) * D_TWO_PI;
        vec3 dir = t * cos(waveRot[i]) + b * sin(waveRot[i]);
        sum += waveAmp[i] * sin(dot(pos, dir) * waveFreq[i] * D_TWO_PI + phase);
    }
    return sum;
}

// detailDisplace pushes world position `pos` along the face normal by the summed
// sine waves plus (when enabled) seeded fractal noise. A pure function of `pos`
// (everything else is a per-face uniform), so it is identical for the same world
// position + seed every frame, across runs, and — crucially — across two patches
// meeting at a shared edge.
vec3 detailDisplace(vec3 pos) {
    float d = d_waveSum(pos);
    if (noiseOn != 0) {
        vec3 off = vec3(dispSeed * 13.1, dispSeed * 7.7, dispSeed * 3.3);
        d += noiseAmp * d_fbm(pos * noiseFreq + off);
    }
    return pos + dispNormal * (d * detailTaper(pos));
}

// detailNormal returns the unit normal of the displaced surface at world
// position `pos`, by central-differencing detailDisplace along the face plane.
//
// The displaced surface exists only on the GPU, so there are no vertices to
// average normals over the way smooth.go does for ordinary geometry. Because
// detailDisplace is a pure function of world position, differentiating it
// instead gives a normal that is continuous everywhere — across the triangles
// the tessellator generates *and* across patch boundaries — and independent of
// the subdivision level, so raising tessellation refines the silhouette without
// changing the shading.
vec3 detailNormal(vec3 pos) {
    vec3 t, b;
    d_planeBasis(t, b);
    float e = max(dispNormalEps, 1e-4);

    vec3 du = detailDisplace(pos + t * e) - detailDisplace(pos - t * e);
    vec3 dv = detailDisplace(pos + b * e) - detailDisplace(pos - b * e);

    vec3 n = cross(du, dv);
    float l = length(n);
    // Degenerate only where the field is locally flat, where the face normal is
    // the right answer anyway.
    return (l > 1e-8) ? n / l : dispNormal;
}
