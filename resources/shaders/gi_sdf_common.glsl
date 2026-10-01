// gi_sdf_common.glsl — reading the GI distance field (render/gi_sdf.go).
//
// The field is a clipmap: GI_SDF_LEVELS cubes of GI_SDF_RES³ voxels around the
// camera, each twice the voxel size of the one inside it. A voxel holds the
// signed distance to the nearest surface (negative inside), clamped to a band
// a few voxels deep, and the surface's albedo beside it in a second texture.
// Both wrap (GL_REPEAT): the cube scrolls with the camera by overwriting the
// slab of voxels it leaves with the slab it enters, so a world position's
// texel never moves while it stays in range.
//
// Spliced into the GI compute programs and the debug view after their
// #version line; it declares only uniforms, so a program that never reads it
// pays nothing.

#define GI_SDF_RES 128
#define GI_SDF_MAX_LEVELS 4
// How many voxels deep the stored distance reaches before it is clamped.
#define GI_SDF_BAND 4.0
// What giSdf answers outside every level: empty space as far as it knows.
#define GI_SDF_FAR 1e4

uniform int  giSdfLevels;
// xyz the level's lowest corner in world units, w its voxel size.
uniform vec4 giSdfLevel[GI_SDF_MAX_LEVELS];
uniform sampler3D giSdf0;
uniform sampler3D giSdf1;
uniform sampler3D giSdf2;
uniform sampler3D giSdf3;
uniform sampler3D giSurf0;
uniform sampler3D giSurf1;
uniform sampler3D giSurf2;
uniform sampler3D giSurf3;

// Constant indices only: a sampler array indexed by something that varies
// across a workgroup is not allowed in GLSL 4.30.
float giSdfRaw(int l, vec3 uvw) {
    if (l == 0) return texture(giSdf0, uvw).r;
    if (l == 1) return texture(giSdf1, uvw).r;
    if (l == 2) return texture(giSdf2, uvw).r;
    return texture(giSdf3, uvw).r;
}

vec4 giSurfRaw(int l, vec3 uvw) {
    if (l == 0) return texture(giSurf0, uvw);
    if (l == 1) return texture(giSurf1, uvw);
    if (l == 2) return texture(giSurf2, uvw);
    return texture(giSurf3, uvw);
}

// giSdfInside is whether level l holds p, a voxel clear of its faces (where
// trilinear filtering would read a voxel the level does not own).
bool giSdfInside(int l, vec3 p) {
    vec3 q = (p - giSdfLevel[l].xyz) / giSdfLevel[l].w;
    return all(greaterThan(q, vec3(1.0))) && all(lessThan(q, vec3(float(GI_SDF_RES) - 1.0)));
}

vec3 giSdfUVW(int l, vec3 p) {
    return p / (giSdfLevel[l].w * float(GI_SDF_RES));
}

// giSdfFrom is the distance from p to the nearest surface, from the finest
// level that holds p and is within its band of a surface — or, where every
// level that fine only knows the surface is further than its band, from a
// coarser one that can say how much further. voxel is the voxel size of the
// finest level holding p, -1 outside them all.
//
// level is where to start looking, and comes back as the level that answered.
// A ray marching through the field asks from one step to the next of nearly
// the same place, so starting at the last answer finds this one in a read or
// two: the trace's whole cost is these reads, and starting from the finest
// level every time cost four a step through open air, where coarse to fine
// would cost four a step along a wall.
float giSdfFrom(vec3 p, inout int level, out float voxel) {
    voxel = -1.0;
    int finest = -1, coarsest = -1;
    for (int l = 0; l < giSdfLevels && l < GI_SDF_MAX_LEVELS; l++) {
        if (giSdfInside(l, p)) {
            if (finest < 0) {
                finest = l;
            }
            coarsest = l;
        }
    }
    if (finest < 0) {
        return GI_SDF_FAR;
    }
    voxel = giSdfLevel[finest].w;

    // A level's answer as a bound: the finest level's is the distance; a
    // coarser one's is less a voxel for its own blur — never more than the
    // true distance, so a step by it cannot pass through.
    int l = clamp(level, finest, coarsest);
    float v = giSdfLevel[l].w;
    float s = giSdfRaw(l, giSdfUVW(l, p));
    float d = l == finest ? s : s - v;
    if (s < GI_SDF_BAND * v * 0.9) {
        // Within this level's band: a finer level within its own says more.
        while (l > finest) {
            float vf = giSdfLevel[l - 1].w;
            float sf = giSdfRaw(l - 1, giSdfUVW(l - 1, p));
            if (sf >= GI_SDF_BAND * vf * 0.9) {
                d = max(d, l - 1 == finest ? sf : sf - vf);
                break; // beyond the finer one's band: this level's answer stands
            }
            l--;
            d = l == finest ? sf : sf - vf;
        }
    } else {
        // Beyond it: a coarser level can say how much further.
        while (l < coarsest) {
            l++;
            v = giSdfLevel[l].w;
            s = giSdfRaw(l, giSdfUVW(l, p));
            d = max(d, s - v);
            if (s < GI_SDF_BAND * v * 0.9) {
                break;
            }
        }
    }
    level = l;
    return d;
}

// giSdf is giSdfFrom for a lone point: from the finest level out.
float giSdf(vec3 p, out float voxel) {
    int level = 0;
    return giSdfFrom(p, level, voxel);
}

// giSdfSurface is the albedo (rgb) and emissive strength (a) at p, from the
// finest level that holds it.
vec4 giSdfSurface(vec3 p) {
    for (int l = 0; l < giSdfLevels && l < GI_SDF_MAX_LEVELS; l++) {
        if (giSdfInside(l, p)) {
            return giSurfRaw(l, giSdfUVW(l, p));
        }
    }
    return vec4(0.5, 0.5, 0.5, 0.0);
}

// giSdfNormal is the field's gradient at p, from four taps a voxel out on the
// corners of a tetrahedron — the same first-order estimate as central
// differences on the three axes, for four reads of the field instead of six.
vec3 giSdfNormal(vec3 p, float voxel) {
    float h = max(voxel, 1e-3) * 0.5773503; // the corners a voxel from p
    float v;
    const vec2 k = vec2(1.0, -1.0);
    vec3 g = k.xyy * giSdf(p + k.xyy * h, v)
           + k.yyx * giSdf(p + k.yyx * h, v)
           + k.yxy * giSdf(p + k.yxy * h, v)
           + k.xxx * giSdf(p + k.xxx * h, v);
    float len = length(g);
    return len > 1e-6 ? g / len : vec3(0.0, 1.0, 0.0);
}

// giSdfTrace sphere-traces from o along unit dir for at most maxT. It returns
// true on a hit, with t the distance to it.
bool giSdfTrace(vec3 o, vec3 dir, float maxT, int maxSteps, out float t) {
    t = 0.0;
    int level = 0;
    for (int i = 0; i < maxSteps; i++) {
        float voxel;
        float d = giSdfFrom(o + dir * t, level, voxel);
        if (voxel < 0.0) {
            return false; // left the field: nothing more it knows of
        }
        if (d < 0.5 * voxel) {
            return true;
        }
        t += max(d, 0.5 * voxel);
        if (t >= maxT) {
            return false;
        }
    }
    return false;
}

// giSdfShadow is how much of a light from direction l (unit) reaches p, soft
// by how near the shadow ray passes to what it does not hit.
float giSdfShadow(vec3 p, vec3 l, float maxT) {
    float res = 1.0;
    float voxel;
    giSdf(p, voxel);
    float t = max(voxel, 0.05) * 1.5;
    int level = 0;
    for (int i = 0; i < 24; i++) {
        float d = giSdfFrom(p + l * t, level, voxel);
        if (voxel < 0.0) {
            break;
        }
        if (d < 0.25 * voxel) {
            return 0.0;
        }
        res = min(res, 4.0 * d / t);
        t += max(d, 0.5 * voxel);
        if (t >= maxT) {
            break;
        }
    }
    return clamp(res, 0.0, 1.0);
}
