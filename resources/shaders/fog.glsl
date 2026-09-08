// fog.glsl — fog, shared by every stage that shades world geometry.
//
// Two kinds, one file. DISTANCE fog is the air of the whole level: a
// function of how far away a point is, with no position of its own. A FOG
// VOLUME is a placed region that is full of fog, and its term is the length
// of the view ray that was inside it. They compose — see applyFog at the
// bottom, which is the one entry point every pass calls.
//
// Injected after the #version line of fragment.glsl (world geometry, water and
// the detail tessellation/scatter programs that reuse it), of biome_light.glsl's
// consumers (the scatter in biome_frag.glsl, the ground patch in
// ground_frag.glsl and sprites in sprite_frag.glsl) and of sky_frag.glsl — see
// InjectFog in gfx/graphics.go and biomeLightSnippet in render/biome_light.go.
//
// The same trick biome_light.glsl uses: ONE declaration of the uniform names,
// compiled into every program that shades, so world.UploadFogTo drives all of
// them and there is no per-pass fog model to keep in step with the others. A
// wall, the grass in front of it, the rock scattered over that grass and the
// sprite standing on it all fade at exactly the same rate, which is the whole
// point — fog that disagreed between passes would read as the passes
// disagreeing about where things are.
//
// Distance is RADIAL (from the eye), not the view-axis depth a fixed-function
// fog would use. Planar depth makes the fog on a fixed object change as the
// camera turns its head, which reads as the world breathing.

// 0 = off, 1 = linear, 2 = exponential, 3 = exponential-squared. Must match
// world.FogMode's values (fog.go).
uniform int   fogMode;
uniform vec3  fogColor;
uniform float fogStart;      // where fog begins, in world units, for every mode
uniform float fogEnd;        // linear only: where it reaches fogMaxOpacity
uniform float fogDensity;    // exp/exp2 only
uniform float fogMaxOpacity; // ceiling on the blend, so distant geometry can be
                             // left legible rather than erased
uniform vec3  fogCamPos;     // the eye the radial distance is measured from

// fogFactor is how much of fogColor this point has turned into: 0 = untouched,
// 1 = the fog and nothing else.
float fogFactor(vec3 worldPos) {
    if (fogMode == 0) {
        return 0.0;
    }
    float d = distance(worldPos, fogCamPos);
    float f;
    if (fogMode == 1) {
        f = (d - fogStart) / max(fogEnd - fogStart, 1e-4);
    } else if (fogMode == 2) {
        f = 1.0 - exp(-fogDensity * max(d - fogStart, 0.0));
    } else {
        float t = fogDensity * max(d - fogStart, 0.0);
        f = 1.0 - exp(-t * t);
    }
    return clamp(f, 0.0, 1.0) * fogMaxOpacity;
}

// ---------- fog volumes ----------
//
// A fog volume is a placed region that is FULL of fog, as against the distance
// fog above which is everywhere. The two are different questions: distance fog
// asks "how far away is this", a volume asks "how much of the air between the
// eye and this point was inside that box".
//
// So the term is not a function of distance at all — it is the LENGTH of the
// eye-to-fragment segment that lies inside the volume, run through the same
// exponential extinction. That is what makes a fog bank behave like a bank: it
// thickens as you look along it and thins as you look across it, a wall seen
// through a corner of it is only lightly touched, and walking in fills the
// screen because the segment now starts inside. A "distance from the centre"
// falloff gets all four of those wrong, and gets them wrong in a way that looks
// fine standing still.
//
// Every shape is solved in NORMALIZED space, where the volume is the unit box,
// unit sphere or unit cylinder: fogVolBasis maps a world-space offset from the
// centre through the inverse rotation and then divides by the half-extents, so
// one intersection routine per shape covers every size and orientation. The ray
// direction is carried through that same matrix WITHOUT renormalizing, which is
// what keeps the parameter t measured in world units — the span has to come out
// in metres, not in half-extents, or a wide volume would fog less than a narrow
// one at the same density.

#define MAX_FOG_VOLUMES 8

// Shape values match world.VolumeShape (volume.go): 0 box, 1 sphere, 2 cylinder.
#define FOG_VOL_BOX      0
#define FOG_VOL_SPHERE   1
#define FOG_VOL_CYLINDER 2

uniform int   numFogVolumes;
uniform mat3  fogVolBasis[MAX_FOG_VOLUMES];  // world offset -> normalized shape space
uniform vec3  fogVolCenter[MAX_FOG_VOLUMES];
uniform int   fogVolShape[MAX_FOG_VOLUMES];
uniform vec3  fogVolColor[MAX_FOG_VOLUMES];
uniform float fogVolDensity[MAX_FOG_VOLUMES];    // per world unit travelled inside
uniform float fogVolMaxOpacity[MAX_FOG_VOLUMES];

// fogVolumeSpan is how far the segment from fogCamPos to worldPos travels inside
// volume i, in world units. 0 when the segment misses it entirely.
//
// The interval starts clamped to [0, segLen], which is what makes the two cases
// that matter fall out with no special-casing: a camera INSIDE the volume keeps
// t0 = 0 (the fog starts at the eye), and a volume behind the fragment is
// clipped away by t1 rather than fogging things in front of it.
float fogVolumeSpan(int i, vec3 worldPos) {
    vec3 seg = worldPos - fogCamPos;
    float segLen = length(seg);
    if (segLen < 1e-5) {
        return 0.0;
    }
    vec3 dir = seg / segLen;

    mat3 basis = fogVolBasis[i];
    vec3 o = basis * (fogCamPos - fogVolCenter[i]);
    vec3 d = basis * dir;

    float t0 = 0.0;
    float t1 = segLen;
    int shape = fogVolShape[i];

    if (shape == FOG_VOL_SPHERE) {
        // Unit sphere. |o + t*d| = 1.
        float a = dot(d, d);
        float b = 2.0 * dot(o, d);
        float c = dot(o, o) - 1.0;
        float disc = b * b - 4.0 * a * c;
        if (a < 1e-12 || disc <= 0.0) {
            return 0.0;
        }
        float sq = sqrt(disc);
        t0 = max(t0, (-b - sq) / (2.0 * a));
        t1 = min(t1, (-b + sq) / (2.0 * a));
    } else {
        // Box and cylinder share the |y| <= 1 slab; the box adds the other two
        // slabs, the cylinder a unit disc in XZ.
        int lo = (shape == FOG_VOL_CYLINDER) ? 1 : 0;
        int hi = (shape == FOG_VOL_CYLINDER) ? 1 : 2;
        for (int ax = lo; ax <= hi; ++ax) {
            if (abs(d[ax]) < 1e-6) {
                // Parallel to this pair of planes: either always between them
                // or never, and never means the whole segment misses.
                if (abs(o[ax]) > 1.0) {
                    return 0.0;
                }
            } else {
                float inv = 1.0 / d[ax];
                float ta = (-1.0 - o[ax]) * inv;
                float tb = ( 1.0 - o[ax]) * inv;
                t0 = max(t0, min(ta, tb));
                t1 = min(t1, max(ta, tb));
            }
        }
        if (shape == FOG_VOL_CYLINDER) {
            vec2 o2 = o.xz;
            vec2 d2 = d.xz;
            float a = dot(d2, d2);
            float c = dot(o2, o2) - 1.0;
            if (a < 1e-12) {
                // Straight up the axis: inside for its whole length, or outside
                // for all of it.
                if (c > 0.0) {
                    return 0.0;
                }
            } else {
                float b = 2.0 * dot(o2, d2);
                float disc = b * b - 4.0 * a * c;
                if (disc <= 0.0) {
                    return 0.0;
                }
                float sq = sqrt(disc);
                t0 = max(t0, (-b - sq) / (2.0 * a));
                t1 = min(t1, (-b + sq) / (2.0 * a));
            }
        }
    }

    return max(t1 - t0, 0.0);
}

// fogVolumeOpacity is volume i's share of this pixel: how much of its colour has
// replaced what was behind it.
float fogVolumeOpacity(int i, vec3 worldPos) {
    float span = fogVolumeSpan(i, worldPos);
    if (span <= 0.0) {
        return 0.0;
    }
    return clamp(1.0 - exp(-fogVolDensity[i] * span), 0.0, 1.0) * fogVolMaxOpacity[i];
}

// applyFogVolumes blends every volume the segment crosses over the surface,
// in level order.
//
// Level order, rather than sorted back-to-front: two overlapping volumes are the
// one case the order is visible, sorting per-pixel is not something a fragment
// shader can do to a uniform array, and volumes are authored to fill rooms
// rather than to be stacked. Each one composites correctly against everything
// already there, so a single volume — which is the case that exists — is exact.
vec3 applyFogVolumes(vec3 color, vec3 worldPos) {
    for (int i = 0; i < MAX_FOG_VOLUMES; ++i) {
        if (i >= numFogVolumes) {
            break;
        }
        color = mix(color, fogVolColor[i], fogVolumeOpacity(i, worldPos));
    }
    return color;
}

// fogVolumeTransmittance is how much of an ADDITIVE surface survives the
// volumes in front of it — the same reasoning as applyFogAdditive: the
// framebuffer already holds them, so a spark deep in a fog bank is swallowed
// rather than tinted.
float fogVolumeTransmittance(vec3 worldPos) {
    float t = 1.0;
    for (int i = 0; i < MAX_FOG_VOLUMES; ++i) {
        if (i >= numFogVolumes) {
            break;
        }
        t *= 1.0 - fogVolumeOpacity(i, worldPos);
    }
    return t;
}

// ---------- what a shading pass calls ----------

// applyFog is the ordinary case: the surface is replaced by the fog in front of
// it, in proportion.
vec3 applyFog(vec3 color, vec3 worldPos) {
    // Volumes first, then the distance fog over the top of them. The distance
    // fog fills the WHOLE segment, including the part in front of the nearest
    // volume, so it is the outermost layer; doing it the other way round would
    // let a fog bank tint the haze that is actually between the viewer and it.
    color = applyFogVolumes(color, worldPos);
    return mix(color, fogColor, fogFactor(worldPos));
}

// applyFogAdditive is the same physical answer for a surface drawn with an
// ADDITIVE blend, where the framebuffer already holds the fog behind it. Mixing
// toward fogColor there would add the fog a second time and make a distant
// spark BRIGHTER than a near one; what fog does to something that only ever
// adds light is swallow it, so the contribution is attenuated instead.
vec3 applyFogAdditive(vec3 color, vec3 worldPos) {
    return color * (1.0 - fogFactor(worldPos)) * fogVolumeTransmittance(worldPos);
}
