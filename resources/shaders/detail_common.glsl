// detail_common.glsl — shared deterministic displacement noise for procedural
// face detail. Concatenated (after the #version line) into every tessellation
// stage of the detail programs so the lit surface and its shadow silhouette use
// the exact same displacement. NOT injected into the reused fragment shaders.

uniform float dispSeed; // per-face seed
uniform float dispAmp;  // max displacement along the normal (world units)
uniform float dispFreq; // noise frequency (features per world unit)
uniform int   dispOct;  // fractal octaves (1..6)

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

// Fractal Brownian motion in [-1,1].
float d_fbm(vec3 p) {
    float sum = 0.0;
    float amp = 0.5;
    float freq = 1.0;
    for (int i = 0; i < 6; i++) {
        if (i >= dispOct) break;
        sum += amp * (d_vnoise(p * freq) * 2.0 - 1.0);
        freq *= 2.0;
        amp *= 0.5;
    }
    return sum;
}

// detailDisplace pushes world position `pos` along `nrm` by seeded fractal
// noise. Deterministic: identical for the same world position + seed every frame
// and across runs.
vec3 detailDisplace(vec3 pos, vec3 nrm) {
    vec3 off = vec3(dispSeed * 13.1, dispSeed * 7.7, dispSeed * 3.3);
    float d = d_fbm(pos * dispFreq + off);
    return pos + nrm * (d * dispAmp * detailTaper(pos));
}
