// grassfield_common.glsl — placing one vertex of a Poly Haven grass clump
// (render/grass_field.go; docs/regions/PRESETS_2B.md). Spliced into the grass
// colour and shadow vertex stages after wind_common.glsl, so a clump casts
// the shadow of the clump on screen, sway and thinning included.
//
// A kit vertex is in the clump's own frame (Y up, base at 0, unit scale) and
// carries its wind weights. An instance is three words (internal/grassfield
// pack.go): its position quantised over the field's box, its yaw, scale,
// tint and rank. The distance rules below are internal/grassfield
// falloff.go's, line for line: the CPU draws, per 16 m cell, the prefix of
// each run its nearest point admits; here each drawn instance shows as much
// as its OWN distance admits, dissolving over a narrow band of rank, and the
// survivors grow to keep the ground covered.
//
// Ground cover (internal/grassfield orient.go, card.go; docs/terrain-paint/
// PHASE_P3.md): an ORIENTED kind's clumps stand on their own up axis, the
// fourth instance word (an oriented field's), instead of +Y — the yawed
// clump is turned onto it by grassUpRotate (orient.go's UpRotate, line for
// line). A CARD (flat leaf litter) is also pulled toward the camera by a
// distance-scaled bias, so it never fights the terrain under it. Kinds with
// neither flag place exactly as before.

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in vec2 aUV;
// up (0 base .. 1 top), out (0 axis .. 1 widest), bend, flutter.
layout(location = 3) in vec4 aWind;
// x | z << 16; y | yaw << 16 | scale << 24; r | g << 8 | b << 16 | rank << 24.
layout(location = 4) in uvec3 iData;
// An oriented field's fourth word: the up axis, octahedral 12 + 12 bits.
// Not fetched (its array is off) for a field without oriented kinds.
layout(location = 5) in uint iUp;

uniform vec3 camPos;
// The field's box (low corner, extent) and its largest scale.
uniform vec3 fieldOrigin;
uniform vec3 fieldSize;
uniform float fieldScale;
// range, start, thin, comp
uniform vec4 grassRangeA;
// compUp, density, -, -
uniform vec4 grassRangeB;
// This draw's kinds' share of the range (0: all), and its tier's window.
uniform float kindRange;
uniform vec2 tierWindow;
// This draw's kinds' flags (grassfield.Kind.Flags): 2 oriented, 4 still,
// 8 card.
uniform int kindFlags;

const int GRASS_KIND_ORIENTED = 2;
const int GRASS_KIND_CARD = 8;

const float GRASS_RANK_BAND = 0.06;
const float GRASS_MAX_COMP = 3.0;
const float GRASS_TIER_BAND = 0.15;

float grassThin(float d) {
    float start = grassRangeA.y;
    return (grassRangeA.z > 0.0 && d > start && start > 0.0) ? pow(start / d, grassRangeA.z) : 1.0;
}

float grassKeep(float d) {
    float R = grassRangeA.x;
    if (kindRange > 0.0 && kindRange < 1.0) {
        R *= kindRange;
    }
    return grassThin(d) * (1.0 - smoothstep(R * 0.82, R, d)) * clamp(grassRangeB.y, 0.0, 1.0);
}

float grassRankFade(float keep, float rank) {
    float band = GRASS_RANK_BAND * max(keep, 1e-3);
    return clamp((keep * (1.0 + GRASS_RANK_BAND) - rank) / band, 0.0, 1.0);
}

vec2 grassGrow(float d) {
    float g = min(pow(grassThin(d), -0.5 * grassRangeA.w), GRASS_MAX_COMP);
    return vec2(g, pow(g, grassRangeB.x));
}

// (in, out) weights of the tier window at d; the tier shows min of them.
vec2 grassTierWeights(vec2 win, float d) {
    float wIn = win.x > 0.0 ? smoothstep(win.x - GRASS_TIER_BAND * win.x, win.x, d) : 1.0;
    float wOut = win.y > 0.0 ? 1.0 - smoothstep(win.y - GRASS_TIER_BAND * win.y, win.y, d) : 1.0;
    return vec2(wIn, wOut);
}

// The wind on a clump: wind_common.glsl's gust (windNow, the same travelling
// wave and clock every swaying mesh reads), with the lean and flutter
// weights the kit baked per vertex instead of measured from a mesh's
// bounds.
vec3 grassWind(vec3 root, vec3 p, float height, float up, float out_, float bend_, float flutter) {
    float k = windNow.z;
    if (k <= 0.0 || (bend_ <= 0.0 && flutter <= 0.0)) {
        return vec3(0.0);
    }
    float t = windNow.w;
    vec2 dir = windNow.xy;
    vec3 wd = vec3(dir.x, 0.0, dir.y);
    float phase = dot(root.xz, dir) * 0.09 - t * 1.3;
    float gust = 0.5 + 0.5 * sin(phase + 1.7 * sin(phase * 0.37 + root.x * 0.05));
    float rock = sin(t * 1.7 + root.x * 0.71 + root.z * 0.53);
    float bend = k * bend_ * height * up * up * (0.2 + 0.8 * gust + 0.2 * rock);
    vec3 d = wd * bend;
    d.y -= bend * bend * 0.5 / max(height, 0.05);
    float f = k * flutter * (0.3 + 0.7 * out_) * up * (0.5 + 0.5 * gust);
    float l1 = sin(t * 7.1 + dot(p, vec3(3.1, 2.3, 2.7)));
    float l2 = sin(t * 5.3 + dot(p, vec3(-2.2, 3.4, 1.9)));
    d += vec3(l1, 0.3 * l2, l2 - l1) * f;
    return d;
}

// The up axis an oriented instance's fourth word holds (orient.go).
vec3 grassUnpackUp(uint w) {
    vec2 f = vec2(float(w & 0xfffu), float((w >> 12u) & 0xfffu)) / 4094.0 * 2.0 - 1.0;
    vec3 n = vec3(f.x, 1.0 - abs(f.x) - abs(f.y), f.y);
    if (n.y < 0.0) {
        n.xz = (1.0 - abs(n.zx)) * vec2(n.x >= 0.0 ? 1.0 : -1.0, n.z >= 0.0 ? 1.0 : -1.0);
    }
    return normalize(n);
}

// grassUpRotate turns v from the upright frame onto axis n: the shortest
// rotation taking +Y to n (orient.go's UpRotate).
vec3 grassUpRotate(vec3 n, vec3 v) {
    float k = 1.0 / (1.0 + n.y);
    return vec3((1.0 - n.x * n.x * k) * v.x + n.x * v.y - n.x * n.z * k * v.z,
                -n.x * v.x + n.y * v.y - n.z * v.z,
                -n.x * n.z * k * v.x + n.z * v.y + (1.0 - n.z * n.z * k) * v.z);
}

// What grassPlace works out for the stages.
vec3 gWorldPos;
vec3 gNormal;
vec3 gRoot;
vec3 gTint;
float gFade;
vec2 gXfade;
float gUp;
// gAxis is the clump's up axis (+Y unless oriented).
vec3 gAxis;

// grassPlace places this vertex; false when the instance shows nothing here
// (thinned out, or outside this draw's tier window).
bool grassPlace() {
    uvec3 I = iData;
    vec3 q = vec3(float(I.x & 0xffffu), float(I.y & 0xffffu), float(I.x >> 16u)) / 65535.0;
    gRoot = fieldOrigin + q * fieldSize;
    float d = distance(camPos, gRoot);
    float keep = grassKeep(d);
    float rank = (float(I.z >> 24u) + 0.5) / 256.0;
    gFade = grassRankFade(keep, rank);
    gXfade = grassTierWeights(tierWindow, d);
    if (gFade <= 0.0 || min(gXfade.x, gXfade.y) <= 0.0) {
        return false;
    }
    float yaw = float((I.y >> 16u) & 0xffu) * (6.28318530718 / 256.0);
    float sc = float(I.y >> 24u) / 255.0 * fieldScale;
    gTint = vec3(float(I.z & 0xffu), float((I.z >> 8u) & 0xffu), float((I.z >> 16u) & 0xffu)) / 128.0;
    vec2 grow = grassGrow(d);
    // An instance dissolving out sinks a little as well, so the thinning
    // front never reads as a line of full-height clumps.
    float hs = grow.y * mix(0.55, 1.0, gFade);
    float c = cos(yaw), s = sin(yaw);
    vec3 l = aPos * vec3(sc * grow.x, sc * hs, sc * grow.x);
    vec3 w = vec3(c * l.x + s * l.z, l.y, -s * l.x + c * l.z);
    vec3 n = vec3(c * aNormal.x + s * aNormal.z, aNormal.y, -s * aNormal.x + c * aNormal.z);
    gAxis = vec3(0.0, 1.0, 0.0);
    if ((kindFlags & GRASS_KIND_ORIENTED) != 0) {
        gAxis = grassUnpackUp(iUp);
        w = grassUpRotate(gAxis, w);
        n = grassUpRotate(gAxis, n);
    }
    vec3 p = gRoot + w;
    gUp = aWind.x;
    float height = max(sc * hs * aPos.y / max(aWind.x, 1e-3), 0.05);
    p += grassWind(gRoot, p, height, aWind.x, aWind.y, aWind.z, aWind.w);
    if ((kindFlags & GRASS_KIND_CARD) != 0) {
        // Toward the camera by 1 cm plus 2 mm a metre: the litter stays
        // over the ground under it at every distance.
        p += normalize(camPos - p) * (0.01 + 0.002 * d);
    }
    gWorldPos = p;
    gNormal = normalize(n + vec3(0.0, 1e-4, 0.0));
    return true;
}
