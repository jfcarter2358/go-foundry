// light_model.glsl — how light falls across a surface, for every pass that
// lights one.
//
// Spliced after the #version line of the world fragment stage (fragment.glsl,
// via gfx.InjectLightModel) and ahead of biome_light.glsl (render's
// biomeLightSnippet), so the walls, the ground patches, the scatter and lit
// sprites are one lighting model rather than one per pass. The level compiler
// bakes with the same one (world.GlobalLight.LightWrap/AmbientAt) — change a
// term here and change it there, or a baked surface stops matching a live one.

// The level's lighting-model knobs, world.GlobalLight's Wrap and Hemisphere —
// lightWrap and ambientHemisphere — ride in the scene block with the rest of
// the lights (world.UploadLightsTo).
#include "scene_block.glsl"
#include "amb_tiles.glsl"

// Wrapped Lambert: how much of a light a surface at cosine ndl to it takes. At
// lightWrap 0 it is max(N.L, 0), the hard terminator; above that the light
// carries on round the curve and eases out instead of stopping at side-on.
float lightWrapTerm(float ndl) {
    return max((ndl + lightWrap) / (1.0 + lightWrap), 0.0);
}

// Hemispheric ambient: the ambient colour brighter facing the sky and darker
// facing the ground, averaging to the colour itself.
vec3 ambientAt(vec3 ambient, vec3 n) {
    return ambient * (1.0 + 0.5 * clamp(ambientHemisphere, 0.0, 1.0) * n.y);
}

// ---------- Ambient volumes (world/volume_ambient.go) ----------
//
// Regions the sky's light does not reach — a cave, a slot canyon — for the
// frames live GI is not there to find that out. Inside one the sky term above
// is multiplied by its sky factor (surfaces facing up) or ground factor
// (facing down), eased in over its fade from the walls. The darkest volume a
// point is in wins.

// ambVolInside is how far inside ambient volume i the point p is, in metres
// (<= 0 outside): the shape's own normalised space, scaled back by its half
// extents. Mirrors world.shapeInsideDepth.
float ambVolInside(int i, vec3 p) {
    vec3 q = ambVolBasis[i] * (p - ambVolCenter[i].xyz);
    vec3 h = ambVolHalf[i].xyz;
    int shape = int(ambVolCenter[i].w + 0.5);
    if (shape == 1) {
        return (1.0 - length(q)) * min(h.x, min(h.y, h.z));
    }
    if (shape == 2) {
        return min((1.0 - length(q.xz)) * min(h.x, h.z), (1.0 - abs(q.y)) * h.y);
    }
    vec3 d = (1.0 - abs(q)) * h;
    return min(d.x, min(d.y, d.z));
}

// ambVolWeight is how fully volume i's cut applies at p: 0 outside, 1 a fade
// or more inside.
float ambVolWeight(int i, vec3 p) {
    float inside = ambVolInside(i, p);
    if (inside <= 0.0) {
        return 0.0;
    }
    float fade = ambVolHalf[i].w;
    return fade > 0.0 ? clamp(inside / fade, 0.0, 1.0) : 1.0;
}

// ambientVolumeFactor is the multiplier on the sky light at p for a surface
// of normal n: 1 outside every ambient volume.
float ambientVolumeFactor(vec3 p, vec3 n) {
    float f = 1.0;
    if (numAmbVolumes <= 0) {
        return f;
    }
    // Only the volumes p's screen tile can see (amb_tiles.glsl).
    uint seen = ambVolMaskAt(p);
    for (int i = 0; i < MAX_AMBIENT_VOLUMES; ++i) {
        if (i >= numAmbVolumes) {
            break;
        }
        if ((seen & (1u << uint(i))) == 0u) {
            continue;
        }
        float w = ambVolWeight(i, p);
        if (w > 0.0) {
            float k = mix(ambVolParams[i].y, ambVolParams[i].x, clamp(0.5 + 0.5 * n.y, 0.0, 1.0));
            f = min(f, mix(1.0, k, w));
        }
    }
    return f;
}

// ---------- Live global illumination (render/gi.go) ----------
//
// Bounce light from a grid of probes around the camera, traced every frame
// against a distance-field copy of the level. Where it is on, it takes the
// place of ambientAt: ambientLight below is what every pass calls, and with
// giEnabled 0 — what an unwritten uniform holds, and what a program GI never
// uploads to gets — it IS ambientAt, so nothing that does not know about GI
// changes.
//
// The probes are DDGI's: each stores irradiance (light arriving, cosine-
// weighted, by direction) and the mean and mean-square distance to what its
// rays hit, both octahedrally mapped into a tile of an atlas with a one-texel
// border so bilinear filtering never reads a neighbour. A point is lit by the
// eight probes around it, each weighted by how far it is (trilinear), whether
// the point is behind it, and whether it could see the point at all (a
// Chebyshev test on its distances) — the last is what keeps light from
// leaking through a wall.

#define GI_MAX_CASCADES 3
#define GI_IRR_TILE 8   // 6 x 6 interior + border
#define GI_VIS_TILE 16  // 14 x 14 interior + border

uniform int   giEnabled;
uniform int   giDebug;       // 1: the lighting alone, on an albedo of 1
uniform float giIntensity;
uniform float giAmbientFloor;
uniform int   giCascades;
uniform ivec3 giDims;        // probes per cascade on each axis
// xyz is the cascade's first probe as a world probe index (exact in a float);
// w its spacing in world units.
uniform vec4  giCascade[GI_MAX_CASCADES];
uniform vec3  giCamPos;
uniform sampler2D giIrradianceAtlas;
uniform sampler2D giVisibilityAtlas;
// Per probe: xyz how far it was moved out of the geometry it sat in, w its
// state (render/gi.go: giStateActive).
uniform sampler2D giProbeData;
// Each probe's nearest surface where it has one close by (render/gi.go, surf):
// the normal facing the probe and the distance to it; zero where none is.
uniform sampler2D giProbeSurface;

// giProbeData and giProbeSurface in one texel (gi_probe_update_comp.glsl
// writes it): x, y the probe's offset and state as halves, z, w its surface
// plane. The shading reads a probe's two with one fetch instead of two.
uniform usampler2D giProbePacked;

// giProbeAt is probe tile's data and surface plane, from giProbePacked.
void giProbeAt(ivec2 tile, out vec4 data, out vec4 plane) {
    uvec4 pk = texelFetch(giProbePacked, tile, 0);
    data = vec4(unpackHalf2x16(pk.x), unpackHalf2x16(pk.y));
    plane = vec4(unpackHalf2x16(pk.z), unpackHalf2x16(pk.w));
}

vec2 giOctEncode(vec3 n) {
    n /= abs(n.x) + abs(n.y) + abs(n.z);
    vec2 e = n.xz;
    if (n.y < 0.0) {
        e = (1.0 - abs(e.yx)) * vec2(e.x >= 0.0 ? 1.0 : -1.0, e.y >= 0.0 ? 1.0 : -1.0);
    }
    return e;
}

vec3 giOctDecode(vec2 e) {
    vec3 n = vec3(e.x, 1.0 - abs(e.x) - abs(e.y), e.y);
    if (n.y < 0.0) {
        vec2 f = (1.0 - abs(n.zx)) * vec2(n.x >= 0.0 ? 1.0 : -1.0, n.z >= 0.0 ? 1.0 : -1.0);
        n.x = f.x;
        n.z = f.y;
    }
    return normalize(n);
}

// giSlot is where world probe index wi is stored: the grid scrolls by
// wrapping, so a probe keeps its slot for as long as it stays in range.
// (GLSL's % is undefined for a negative operand, hence the floor.)
ivec3 giSlot(ivec3 wi) {
    return wi - giDims * ivec3(floor(vec3(wi) / vec3(giDims)));
}

ivec2 giTile(ivec3 slot, int c) {
    return ivec2(slot.x + slot.z * giDims.x, slot.y + c * giDims.y);
}

vec2 giAtlasUV(ivec2 tile, vec3 dir, int tileSize, vec2 atlasSize) {
    float inner = float(tileSize - 2);
    vec2 oct = giOctEncode(dir) * 0.5 + 0.5;
    return (vec2(tile * tileSize) + 1.0 + oct * inner) / atlasSize;
}

// giCascadeCover is how fully cascade c covers p: 1 well inside it, falling to
// 0 a probe from its edge, where the eight probes around p stop all existing.
float giCascadeCover(int c, vec3 p) {
    vec3 q = p / giCascade[c].w - giCascade[c].xyz;
    vec3 hi = vec3(giDims) - 1.0;
    vec3 d = min(q, hi - q);
    return clamp(min(d.x, min(d.y, d.z)) - 1.0, 0.0, 1.0);
}

// giSampleCascade is cascade c's light at p facing n, seen along v — and
// found, how much of it the probes really gave: 1 where at least one probe
// passed the tests below, 0 where every one of the eight was buried or
// refused. The caller (giIrradiance) counts a cascade's cover only as far as
// found says, so a cell with no usable probe is answered by the next cascade
// out, or the flat ambient — never by a confident zero. That zero, at full
// cover and an ambient floor of 0, was the black patches on big cooked rocks
// (docs/canyon/PHASE_6.md): on a rugged scan, every probe of a coarse
// cascade's cell sat within half a spacing of SOME bump whose plane put the
// point behind it.
vec3 giSampleCascade(int c, vec3 p, vec3 n, vec3 v, out float found) {
    float s = giCascade[c].w;
    // Off the surface, toward the viewer and along the normal, so a probe the
    // point sits exactly level with does not see it as half buried.
    vec3 bp = p + (n * 0.2 + v * 0.8) * (0.3 * s);
    vec3 q = bp / s;
    ivec3 base = ivec3(floor(q));
    vec3 a = q - vec3(base);
    ivec3 lo = ivec3(giCascade[c].xyz);
    vec2 irrSize = vec2(textureSize(giIrradianceAtlas, 0));
    vec2 visSize = vec2(textureSize(giVisibilityAtlas, 0));

    vec3 sum = vec3(0.0);
    float wsum = 0.0;
    for (int i = 0; i < 8; i++) {
        ivec3 off = ivec3(i, i >> 1, i >> 2) & ivec3(1);
        ivec3 wi = base + off;
        ivec3 local = wi - lo;
        if (any(lessThan(local, ivec3(0))) || any(greaterThanEqual(local, giDims))) {
            continue;
        }
        ivec2 tile = giTile(giSlot(wi), c);
        vec4 pd, plane;
        giProbeAt(tile, pd, plane);
        if (mod(pd.w, 2.0) < 0.5) {
            continue; // buried in geometry: it has nothing true to say
        }
        vec3 probe = vec3(wi) * s + pd.xyz;
        // On the far side of the surface the probe is up against: under the
        // floor the point stands on, behind the wall it is lit in front of.
        // Its visibility moments are least to be trusted there (see
        // gi_probe_update_comp.glsl), and the surface is between them.
        //
        // Only when the point's own surface faces away from the probe's —
        // the top of the floor the probe is under, the far face of the wall:
        // that is the case the plane is for, a thin thing with the probe on
        // its back. A point facing the same way as the probe's surface is on
        // another part of the same side, round a bump of a rugged rock, and
        // the plane through that bump says nothing about whether the probe
        // can see it — the visibility test below does.
        if (dot(plane.xyz, plane.xyz) > 0.25 && dot(bp - probe, plane.xyz) < -plane.w
            && dot(n, plane.xyz) < 0.0) {
            continue;
        }
        vec3 tri = mix(1.0 - a, a, vec3(off));
        float w = 1.0;

        // Behind the surface: a probe on the far side of a wall from the
        // point weighs little, smoothly, so a point never snaps between sets.
        vec3 toProbe = normalize(probe - p);
        float wrap = (dot(toProbe, n) + 1.0) * 0.5;
        w *= wrap * wrap + 0.2;

        // Could the probe see the point? Chebyshev on its distance moments.
        vec3 fromProbe = bp - probe;
        float d = length(fromProbe);
        vec2 m = textureLod(giVisibilityAtlas, giAtlasUV(tile, fromProbe / max(d, 1e-4), GI_VIS_TILE, visSize), 0.0).rg;
        if (d > m.x) {
            float var = abs(m.y - m.x * m.x);
            float dd = d - m.x;
            float cheb = var / (var + dd * dd);
            w *= max(cheb * cheb * cheb, 0.0);
        }
        w = max(w, 1e-6);
        // Crush the faintest weights, which are almost always a leak.
        const float crush = 0.2;
        if (w < crush) {
            w *= w * w / (crush * crush);
        }
        w *= tri.x * tri.y * tri.z;

        vec3 irr = textureLod(giIrradianceAtlas, giAtlasUV(tile, n, GI_IRR_TILE, irrSize), 0.0).rgb;
        sum += irr * w;
        wsum += w;
    }
    found = wsum > 0.0 ? 1.0 : 0.0;
    return wsum > 0.0 ? sum / wsum : vec3(0.0);
}

// giSampleFar is the outermost cascade's light for a point beyond its reach:
// the probes around the nearest place inside it, by position and by the
// surface's facing alone. The visibility test is left out — a probe a
// hundred metres from the point has nothing to say about whether it can see
// it — and so are probes buried in geometry. found is 0 when every one of
// them was buried.
//
// It is a guess, and a much better one than the flat ambient it replaces:
// that ignores everything the probes know about how much sky a place sees, so
// a shadow beyond the cascades came out as bright as an open field and
// darkened as the player walked up to it and the probes took it over.
vec3 giSampleFar(int c, vec3 p, vec3 n, out float found) {
    float s = giCascade[c].w;
    vec3 lo = giCascade[c].xyz;
    // A probe clear of the edge, so all eight around it are in the grid.
    vec3 q = clamp(p / s, lo + 1.0, lo + vec3(giDims) - 2.001);
    ivec3 base = ivec3(floor(q));
    vec3 a = q - vec3(base);
    vec2 irrSize = vec2(textureSize(giIrradianceAtlas, 0));
    vec3 sum = vec3(0.0);
    float wsum = 0.0;
    for (int i = 0; i < 8; i++) {
        ivec3 off = ivec3(i, i >> 1, i >> 2) & ivec3(1);
        ivec3 wi = base + off;
        ivec2 tile = giTile(giSlot(wi), c);
        if (mod(unpackHalf2x16(texelFetch(giProbePacked, tile, 0).y).y, 2.0) < 0.5) {
            continue;
        }
        vec3 tri = mix(1.0 - a, a, vec3(off));
        float w = max(tri.x * tri.y * tri.z, 1e-4);
        sum += textureLod(giIrradianceAtlas, giAtlasUV(tile, n, GI_IRR_TILE, irrSize), 0.0).rgb * w;
        wsum += w;
    }
    found = wsum > 0.0 ? 1.0 : 0.0;
    return wsum > 0.0 ? sum / wsum : vec3(0.0);
}

// giIrradiance is the probes' light at p for a surface facing n, and how much
// of the answer the probes gave (cover): the cascades where they reach, then
// the outermost one's edge (giSampleFar) for everything past it — so a
// surface's light does not change as the cascades, which follow the camera,
// arrive at it. cover falls short of 1 only where there are no probes to ask,
// and the caller makes up the rest.
vec3 giIrradiance(vec3 p, vec3 n, out float cover) {
    vec3 v = normalize(giCamPos - p);
    vec3 total = vec3(0.0);
    float left = 1.0;
    for (int c = 0; c < giCascades && c < GI_MAX_CASCADES; c++) {
        float w = giCascadeCover(c, p) * left;
        if (w <= 0.0) {
            continue;
        }
        float found;
        vec3 irr = giSampleCascade(c, p, n, v, found);
        // A cascade with no usable probe round p gives nothing and claims
        // nothing: what it would have covered falls to the next one out.
        w *= found;
        total += irr * w;
        left -= w;
        if (left <= 0.0) {
            break;
        }
    }
    int outer = min(giCascades, GI_MAX_CASCADES) - 1;
    if (left > 0.0 && outer >= 0) {
        float found;
        vec3 far = giSampleFar(outer, p, n, found);
        total += far * left * found;
        left -= left * found;
    }
    cover = 1.0 - left;
    return total;
}

#ifdef GI_HALF_RES
// ---------- The material pass's half-resolution probe light ----------
//
// Only the visibility buffer's material pass is built with GI_HALF_RES
// (render/gi_halfres.go): it has the probes' light worked out at half
// resolution beforehand (gi_halfres_comp.glsl), from the frame's depth, and
// reads it back here for the pixel its main (nanite_material_frag.glsl) puts
// in giHalfResPixel before the shading runs.
uniform int giHalfResOn;
uniform sampler2D giHalfResIrr;   // rgb irradiance, a cover
uniform sampler2D giHalfResGuide; // the sample's normal (oct) and plane
uniform ivec2 giHalfResOffset;    // the pixel of a 2x2 block each texel sampled
vec2 giHalfResPixel;              // gl_FragCoord.xy of the pixel being shaded

// giHalfResIrradiance is giIrradiance for a pixel at p facing n, from the four
// half-resolution samples around it, each weighted by how near it is
// (bilinear), how alike its facing is, and how near p lies to its surface's
// plane — so a sample across an edge, on another object or a wall at an
// angle, gives nothing. False where none of the four is on p's surface (an
// edge, a thin rail, something smaller than a block): the caller asks the
// probes itself there.
bool giHalfResIrradiance(vec3 p, vec3 n, out vec3 irr, out float cover) {
    ivec2 size = textureSize(giHalfResIrr, 0);
    vec2 c = (giHalfResPixel - 0.5 - vec2(giHalfResOffset)) * 0.5;
    ivec2 base = ivec2(floor(c));
    vec2 f = c - vec2(base);
    // How far off a sample's plane p may be: a few percent of its distance,
    // what two pixels of a surface seen edge-on can span.
    float slack = 0.02 * max(distance(p, giCamPos), 0.05);
    vec4 sum = vec4(0.0);
    float wsum = 0.0, best = 0.0;
    for (int i = 0; i < 4; i++) {
        ivec2 o = ivec2(i & 1, i >> 1);
        ivec2 t = clamp(base + o, ivec2(0), size - 1);
        vec4 g = texelFetch(giHalfResGuide, t, 0);
        if (g.w < 0.5) {
            continue;
        }
        vec3 gn = giOctDecode(g.xy);
        float facing = max(dot(n, gn), 0.0);
        facing *= facing;
        facing *= facing;
        float onPlane = max(1.0 - abs(dot(gn, p) - g.z) / slack, 0.0);
        float match = facing * onPlane;
        best = max(best, match);
        // Never quite zero for the farther ones, so a block whose nearest
        // sample is across an edge still blends the others.
        vec2 bw = mix(1.0 - f, f, vec2(o));
        float w = (bw.x * bw.y + 0.05) * match;
        sum += texelFetch(giHalfResIrr, t, 0) * w;
        wsum += w;
    }
    if (best < 0.4 || wsum <= 0.0) {
        return false;
    }
    irr = sum.rgb / wsum;
    cover = sum.a / wsum;
    return true;
}
#endif

// giIrradianceShaded is giIrradiance for a pass shading a surface: the
// material pass's half-resolution answer where it has one.
vec3 giIrradianceShaded(vec3 p, vec3 n, out float cover) {
#ifdef GI_HALF_RES
    if (giHalfResOn != 0) {
        vec3 irr;
        if (giHalfResIrradiance(p, n, irr, cover)) {
            return irr;
        }
    }
#endif
    return giIrradiance(p, n, cover);
}

// ambientLight is the light a surface gets from everything but the lights
// themselves: GI where it is on and reaches, ambientAt where it does not.
// giDebugAlbedo is a surface's colour as the lighting view (gi_debug lighting,
// the `lighting` view) sees it: white, so what is left on screen is the light
// alone. Every shader that multiplies a colour by its lighting goes through
// this, or that view shows its textures as if it were the normal one.
vec3 giDebugAlbedo(vec3 albedo) {
    return giDebug == 1 ? vec3(1.0) : albedo;
}

vec3 ambientLight(vec3 ambient, vec3 p, vec3 n) {
    vec3 sky = ambientAt(ambient, n) * ambientVolumeFactor(p, n);
    if (giEnabled == 0) {
        return sky;
    }
    float cover;
    vec3 gi = giIrradianceShaded(p, n, cover) * giIntensity;
    return gi + sky * mix(1.0, giAmbientFloor, cover);
}
