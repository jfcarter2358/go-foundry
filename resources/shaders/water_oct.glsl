// water_oct.glsl — the octahedral mapping between a direction and a point of
// a square texture, for the sky map the water reflects (render/water_sky.go
// writes it with waterOctDecode, water.glsl reads it with waterOctEncode).
// Y is up: the upper hemisphere fills the inner diamond, the lower one folds
// into the corners.
#ifndef WATER_OCT_GLSL
#define WATER_OCT_GLSL

vec2 waterOctSign(vec2 v) {
    return vec2(v.x >= 0.0 ? 1.0 : -1.0, v.y >= 0.0 ? 1.0 : -1.0);
}

// waterOctEncode maps a unit direction to [0,1]².
vec2 waterOctEncode(vec3 d) {
    d /= abs(d.x) + abs(d.y) + abs(d.z);
    vec2 o = d.xz;
    if (d.y < 0.0) {
        o = (1.0 - abs(d.zx)) * waterOctSign(d.xz);
    }
    return o * 0.5 + 0.5;
}

// waterOctDecode is waterOctEncode's inverse.
vec3 waterOctDecode(vec2 uv) {
    vec2 o = uv * 2.0 - 1.0;
    vec3 d = vec3(o.x, 1.0 - abs(o.x) - abs(o.y), o.y);
    if (d.y < 0.0) {
        d.xz = (1.0 - abs(d.zx)) * waterOctSign(d.xz);
    }
    return normalize(d);
}

#endif
