// light_model.glsl — how light falls across a surface, for every pass that
// lights one.
//
// Spliced after the #version line of the world fragment stage (fragment.glsl,
// via gfx.InjectLightModel) and ahead of biome_light.glsl (render's
// biomeLightSnippet), so the walls, the ground patches, the scatter and lit
// sprites are one lighting model rather than one per pass. The level compiler
// bakes with the same one (world.GlobalLight.LightWrap/AmbientAt) — change a
// term here and change it there, or a baked surface stops matching a live one.

// The level's lighting-model knobs, world.GlobalLight's Wrap and Hemisphere,
// uploaded with the rest of the lights (world.UploadLightsTo).
uniform float lightWrap;
uniform float ambientHemisphere;

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

vec3 giSampleCascade(int c, vec3 p, vec3 n, vec3 v) {
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
        vec4 pd = texelFetch(giProbeData, tile, 0);
        if (mod(pd.w, 2.0) < 0.5) {
            continue; // buried in geometry: it has nothing true to say
        }
        vec3 probe = vec3(wi) * s + pd.xyz;
        // On the far side of the surface the probe is up against: under the
        // floor the point stands on, behind the wall it is lit in front of.
        // Its visibility moments are least to be trusted there (see
        // gi_probe_update_comp.glsl), and the surface is between them.
        vec4 plane = texelFetch(giProbeSurface, tile, 0);
        if (dot(plane.xyz, plane.xyz) > 0.25 && dot(bp - probe, plane.xyz) < -plane.w) {
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
        if (mod(texelFetch(giProbeData, tile, 0).w, 2.0) < 0.5) {
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
        total += giSampleCascade(c, p, n, v) * w;
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
    vec3 sky = ambientAt(ambient, n);
    if (giEnabled == 0) {
        return sky;
    }
    float cover;
    vec3 gi = giIrradiance(p, n, cover) * giIntensity;
    return gi + sky * mix(1.0, giAmbientFloor, cover);
}
