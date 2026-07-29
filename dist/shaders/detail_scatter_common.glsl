// detail_scatter_common.glsl — shared per-face scatter placement. Concatenated
// (after the #version line) into every scatter vertex stage (color + shadow).
// Places rock instances over a face's plane, deterministically from a seed.

uniform vec3  faceOrigin; // world position of the face's (u=0,v=0) corner
uniform vec3  faceU;      // unit in-plane axis (world)
uniform vec3  faceV;      // unit in-plane axis (world), perpendicular to faceU
uniform vec3  faceNrm;    // unit face normal (world)
uniform float faceUMax;   // face extent along faceU (world units)
uniform float faceVMax;   // face extent along faceV (world units)
uniform float sSpacing;   // lattice spacing (world units)
uniform float sDensity;   // coverage 0..1
uniform float sScale;     // world size of one instance
uniform float sJitter;    // positional jitter 0..1
uniform float sSeed;      // per-face seed
uniform int   gridNU;     // lattice slots along U (instanceCount = gridNU*gridNV)

float s_hash11(float n) { return fract(sin(n) * 43758.5453123); }
vec2 s_hash22(vec2 p) {
    p = vec2(dot(p, vec2(127.1, 311.7)), dot(p, vec2(269.5, 183.3)));
    return fract(sin(p) * 43758.5453);
}

// scatterPlace computes the world position of local rock vertex `local` for
// instance `id`. Returns false when the instance is culled (empty slot / thinned
// by density / outside the face rectangle).
bool scatterPlace(int id, vec3 local, out vec3 outWorld) {
    int ix = id % gridNU;
    int iz = id / gridNU;
    vec2 slot = (vec2(float(ix), float(iz)) + 0.5) * sSpacing;
    if (slot.x > faceUMax || slot.y > faceVMax) { outWorld = vec3(0.0); return false; }

    vec2 h = s_hash22(slot * 1.7 + vec2(sSeed * 0.13, sSeed * 0.37));
    vec2 uv = slot + (h - 0.5) * sSpacing * sJitter;
    uv = clamp(uv, vec2(0.0), vec2(faceUMax, faceVMax));

    float pick = s_hash11(dot(uv, vec2(12.9898, 78.233)) + sSeed * 0.0001);
    if (pick > sDensity) { outWorld = vec3(0.0); return false; }

    vec3 base = faceOrigin + faceU * uv.x + faceV * uv.y;

    // Orient the rock: local +Y follows the face normal; random yaw about it.
    float yaw = h.x * 6.2831853;
    float ca = cos(yaw), sa = sin(yaw);
    vec3 t = faceU * ca + faceV * sa;
    vec3 b = cross(faceNrm, t);
    float scl = sScale * (0.6 + 0.8 * h.y);
    vec3 lp = local * scl;
    vec3 off = t * lp.x + faceNrm * lp.y + b * lp.z;

    // Nestle the rock half-buried so it reads as sitting on the surface.
    base -= faceNrm * scl * 0.35;
    outWorld = base + off;
    return true;
}
