// water.glsl — how a water surface is shaded, and the caustics on what lies
// under one (docs/terrain-paint/PHASE_W1.md). Included by fragment.glsl after
// its lighting functions; the height field is water_waves.glsl's.
//
// THE LOOK, per pixel of a water surface:
//
//   1. The normal: the swell's (waterNormal) plus three samples of the
//      generated ripple map — one tile at three sizes, headings and drift
//      speeds — summed as slopes (waterSurfaceNormal).
//   2. What is under the surface: the scene's depth copy says how far the
//      view ray runs through water and how deep the bed is; the colour copy,
//      sampled a little off by the ripples (refraction), is the bed. Light
//      reaching the eye from the bed is cut by Beer–Lambert absorption over
//      the way down and back (shallow_color ^ (path · absorption)), and what
//      it loses is replaced by scatter_color lit by the scene's light — deep
//      water is its own colour, shallow water is the bed's.
//   3. Reflection: a screen-space march along the reflected ray through the
//      depth copy (high quality); where it finds nothing, the sky, read from
//      an octahedral map render/water_sky.go draws from the sky shader itself
//      each frame, blurred by roughness through its mips; on a level with no
//      sky, the ambient light (GI where it is on) along the reflected ray.
//      Weighted by water's own fresnel (F0 0.02) times reflectivity / 0.5.
//   4. The sun: a GGX glint on the rough surface, shadowed like the ground.
//   5. A back-lit wave crest glows faintly with the deep colour.
//   6. Foam where the bed comes within a band of the surface, from the
//      detail map's foam network; the vertex colour's red adds foam (a
//      river ribbon's riffles, boulders, confluences and falls, phase W3).
//
// FLOW MAPS (phase W3, docs/terrain-paint/PHASE_W3.md). A material with
// flow_map > 0 on a mesh with vertex colours reads green and blue as the
// current along X and Z (0.5 still, ±WATER_FLOW_MAX m/s at the ends) — a
// generated river's ribbon bakes its flow map there, since this pass has no
// sampler free for a flow texture. The ripples and the foam are then
// ADVECTED along it, two-phase: two copies of the pattern, each carried
// downstream for one cycle (waterFlowMap.y seconds) and restarted, half a
// cycle apart, cross-faded so each restarts while invisible. Faster water
// is rougher. Without it, the one flow_direction drift as before.
//   7. The shoreline: within shore_fade of zero depth the whole effect fades
//      to the bed seen plainly, so the edge is soft.
//
// PHASE W4 (docs/terrain-paint/PHASE_W4.md):
//
//   - FALLING WATER. A material with `falls` (waterFlowMap.z) is a
//     waterfall's sheet (ground_edit's falls_<river>_<n>, wearing
//     water_falls): waterFallsShade replaces the whole look — streaks
//     scrolling down the mesh's V at falls_speed, aerated white where the
//     sheet's vertex red says so (glassy at a small fall's lip, white by
//     the foot), the scene behind seen through the glassy part, the sides
//     frayed by the vertex green. No new sampler: the streaks are the
//     detail map stretched down the sheet.
//   - CAUSTICS ARE THE WATER'S. The light network on a bed used to be drawn
//     by the bed (fragment.glsl, for any surface inside a listed body's box
//     and below its level), which lit dry hollows inside a pond's box and
//     could not follow a sloping river. Now the water surface draws it onto
//     the bed it shows: the bed's position comes back out of the depth copy
//     (along the view ray), its facing from that position's screen
//     derivatives, and the refracted bed colour is modulated by the same
//     procedural network, faded with the depth under THIS surface and its
//     absorption, carried down a river's current, and only where the sun
//     reaches the surface. So only ground actually under water — a pond, a
//     river at any slope — takes caustics (WATER_HAVE_CAUSTICS, high tier).
//   - WET BED. A submerged bed is darker than the same ground dry (light
//     trapped in the water film); the refracted bed is darkened by
//     WATER_WET, easing in over the soft shoreline. A shallow clear stream
//     over pale gravel read as a pale strip without it.
//   - SHORE FOAM ON RIVERS. A flow-mapped surface's shore-foam band is a
//     third of a lake's: a stream half a metre deep was inside the band
//     bank to bank and read as a flat white strip. Its foam comes from the
//     vertex foam (riffles, boulders, falls, confluences) instead, which
//     also makes those places choppier (white water is rough water).
//
// PHASE W5 (docs/terrain-paint/PHASE_W5.md):
//
//   - RINGS. Disturbances of the surface — a foot in the shallows, a body
//     falling in, a shot striking it — arrive as a short uniform array
//     (render/water_interact.go: centre, birth time, strength, reach) and
//     each is drawn as a damped wave packet spreading from its centre,
//     added to the ripple slopes (waterInteractSlope). A strong young one
//     leaves a little foam. RAIN (waterRippleInfo.y, the level's
//     rain_ripples) is a procedural field of small drop rings, three
//     offset grids of cells, a cell ringing when its hash falls under the
//     rain's strength. No texture: no sampler is free here.
//   - FROM BELOW. With the camera under the surface the surface is seen
//     from beneath: inside SNELL'S WINDOW (the cone within ~48.6° of
//     straight up, where light from above can leave the water toward the
//     eye) the world above shows, bent by the ripples, dimmed by fresnel;
//     outside it, TOTAL INTERNAL REFLECTION — the water's own lit murk.
//     The frame's fog volume (water_interact.go) fogs the way up; what the
//     colour copy holds was fogged by the same water, so it is unfogged
//     here before the fragment's own fog is applied once over it.
//   - FOAM in patches and streaks: the lace is gated by two scales of the
//     detail map's noise, so it gathers in clots and drifts rather than
//     covering the whole band as a net; on a flow-mapped surface its
//     coordinates are stretched along the current, so it trails as
//     streaks.
//   - FALLING SHEETS read white to clear: the glassy part shows the rock
//     behind only faintly (veiled, barely bent), so sand behind no longer
//     draws brown streaks; a sheet seen from behind faces the eye; seen
//     edge-on it thins away rather than showing its streaks as ribs.
//
// Without the colour copy (low quality, or a program nothing prepared) the
// water BLENDS over the bed instead: everything but the bed is the source
// colour and the bed's transmission is the alpha.
//
// BORROWED SAMPLERS. The fragment stage is at its sampler limit, so the water
// path reads its textures through samplers a water surface never uses for
// their own purpose (gfx/water_draw.go binds them):
#define waterSceneColour blendTex
#define waterSceneDepth  blendMask
#define waterSky         normalMap
#define waterDetail      tex

#include "water_oct.glsl"

// The material's look (gfx/water_draw.go): shallow colour + absorption;
// deep colour + crest glow; roughness, refraction, foam, shore fade.
uniform vec4 waterAbsorb;
uniform vec4 waterScatter;
uniform vec4 waterLook;

// What the frame's water pass prepared (render/water_pass.go). Zero — any
// program nothing prepared — is the blended fallback with an assumed depth.
#define WATER_HAVE_DEPTH  1
#define WATER_HAVE_COLOUR 2
#define WATER_HAVE_SKY    4
#define WATER_HAVE_SSR    8
#define WATER_HAVE_CAUSTICS 16
uniform int   waterFrameFlags;
// The viewport the copies were taken of (x, y, w, h), and the projection's
// depth terms (P[2][2], P[3][2]) that turn a depth sample into a distance.
uniform vec4  waterViewport;
uniform vec2  waterDepthParams;
// The frame's projection × view, for the reflection march. Its own name:
// `projection` and `camera` are vertex.glsl's, and the visibility buffer's
// splice would declare them twice.
uniform mat4  waterViewProj;
// The sky map's mip count, for the roughness blur.
uniform float waterSkyMips;

// The vertex-colour flow map (gfx/water_draw.go): x its strength (0 off),
// y the two-phase cycle in seconds; z the falling sheet's strength (phase
// W4: 0 ordinary water), w its streaks' speed, m/s.
uniform vec4 waterFlowMap;
// The speed the flow map's full range encodes, m/s (terrain.RiverFlowMax).
#define WATER_FLOW_MAX 4.0

// The detail map's slope encoding: gfx/water_textures.go's
// waterDetailSlopeEncode, inverted.
#define WATER_DETAIL_SLOPE 4.0
// Water's reflectance looking straight down.
#define WATER_F0 0.02
// How much darker a submerged bed is than the same ground dry (phase W4).
#define WATER_WET 0.72
// A flow-mapped surface's shore-foam band, as a share of a lake's.
#define WATER_RIVER_SHORE 0.3

// ---------- interaction: rings and rain (phase W5) ----------

// The live rings near the camera (render/water_interact.go): x, z the
// centre, w the birth time on `time`'s clock, w = floor(reach·100) +
// strength·0.999. waterRippleInfo: x the count, y the rain (0..1).
#define WATER_MAX_RIPPLES 16
uniform vec4 waterRipples[WATER_MAX_RIPPLES];
uniform vec4 waterRippleInfo;

// waterPacket is the slope along the radius of a damped wave packet: a
// few crests of wavelength `lambda` under a Gaussian envelope of width
// sigma, x metres from its centre ring, amplitude A.
float waterPacket(float x, float lambda, float sigma, float A) {
    float k = 6.2831853 / lambda;
    float env = exp(-x * x / (2.0 * sigma * sigma));
    return A * env * (-x / (sigma * sigma) * cos(k * x) - k * sin(k * x));
}

// waterRingSlope is one ring's slope (d/dx, d/dz) at p.
vec2 waterRingSlope(vec2 p, vec4 r) {
    float age = time - r.z;
    float reach = floor(r.w) / 100.0;
    float strength = fract(r.w) / 0.999;
    if (age < 0.0) {
        return vec2(0.0);
    }
    float R = (0.5 + 0.25 * reach) * age;
    vec2 d = p - r.xy;
    float dist = length(d);
    float lambda = 0.2 + 0.05 * reach;
    float sigma = lambda * 0.6;
    float x = dist - R;
    if (abs(x) > 3.5 * sigma) {
        return vec2(0.0);
    }
    // Fading as it spreads (the energy goes round a longer ring) and with
    // age, gone by its reach.
    float fade = exp(-age * 1.2) * (1.0 - smoothstep(0.7 * reach, reach, R)) * smoothstep(0.0, 0.06, age);
    float A = 0.045 * strength * fade / (1.0 + 0.5 * R);
    return waterPacket(x, lambda, sigma, A) * d / max(dist, 1e-3);
}

// waterHash is a cheap hash of a cell, 0..1.
float waterHash(vec2 p) {
    p = fract(p * vec2(123.34, 456.21));
    p += dot(p, p + 45.32);
    return fract(p.x * p.y);
}

// waterRainSlope is the rain's drop rings at p, strength rain (0..1): three
// offset grids, each cell ringing once a period when its hash says so.
vec2 waterRainSlope(vec2 p, float rain) {
    vec2 sl = vec2(0.0);
    for (int layer = 0; layer < 3; layer++) {
        float fl = float(layer);
        float cell = 0.45 + 0.17 * fl;
        vec2 q = p / cell + vec2(fl * 0.37, fl * 0.71);
        vec2 id = floor(q);
        float h0 = waterHash(id + 13.1 * fl);
        if (waterHash(id + 71.7 + 5.3 * fl) > rain) {
            continue;
        }
        float period = 0.8 + 0.6 * h0;
        float ph = fract(time / period + h0 * 7.13);
        vec2 c = id + 0.3 + 0.4 * vec2(waterHash(id + 3.3), waterHash(id + 9.1));
        vec2 d = (q - c) * cell;
        float dist = length(d);
        float R = 0.32 * cell * pow(ph, 0.6);
        float fade = (1.0 - ph) * (1.0 - ph) * smoothstep(0.0, 0.04, ph);
        sl += waterPacket(dist - R, 0.08, 0.04, 0.0022 * fade) * d / max(dist, 1e-3);
    }
    return sl;
}

// waterRingSlopeLen is the rings' and rain's slope at this fragment,
// left by waterSurfaceNormal for waterShade's crest glint (a global: the
// two run once each per fragment, in that order).
float waterRingSlopeLen = 0.0;

// waterInteractSlope is every ring's and the rain's slope at p (both
// counts are uniforms: the loops are uniform control flow).
vec2 waterInteractSlope(vec2 p) {
    vec2 sl = vec2(0.0);
    int n = int(waterRippleInfo.x + 0.5);
    for (int i = 0; i < WATER_MAX_RIPPLES; i++) {
        if (i >= n) {
            break;
        }
        sl += waterRingSlope(p, waterRipples[i]);
    }
    if (waterRippleInfo.y > 0.0) {
        sl += waterRainSlope(p, waterRippleInfo.y);
    }
    return sl;
}

// waterRippleFoam is the white a strong young ring leaves at its centre: a
// body dropping in, a splash.
float waterRippleFoam(vec2 p) {
    float f = 0.0;
    int n = int(waterRippleInfo.x + 0.5);
    for (int i = 0; i < WATER_MAX_RIPPLES; i++) {
        if (i >= n) {
            break;
        }
        vec4 r = waterRipples[i];
        float strength = fract(r.w) / 0.999;
        float age = time - r.z;
        if (strength < 0.45 || age < 0.0) {
            continue;
        }
        float reach = floor(r.w) / 100.0;
        float R = 0.25 + 0.3 * reach * min(age, 1.5);
        f = max(f, (strength - 0.4) * 1.6 * exp(-age * 1.1) * (1.0 - smoothstep(0.3 * R, R, length(p - r.xy))));
    }
    return clamp(f, 0.0, 1.0);
}

// ---------- caustics (every surface under water) ----------

// The water bodies near the camera whose beds take caustics
// (render/water_caustics.go): a box in X/Z and its surface height, strength,
// pattern scale and absorption. Count zero — everything but a high-quality
// frame with water in it — costs one uniform test.
#define WATER_MAX_CAUSTICS 8
uniform int  waterCausticCount;
uniform vec4 waterCausticBox[WATER_MAX_CAUSTICS];
uniform vec4 waterCausticInfo[WATER_MAX_CAUSTICS];

// waterCausticPattern is a tileable animated caustic network, 0..1, bright
// on the lines where a wavy surface focuses the sun. No texture (the
// material pass has no sampler to spare): a few rounds of a self-warping
// trigonometric field, the well-known "water caustic" construction.
float waterCausticPattern(vec2 p, float t) {
    // Into one tile about the construction's own working point (-250): its
    // terms scale with |p|, so fed world-scaled coordinates it saturated to
    // 1 everywhere far from the origin and blew up near it (found in phase
    // W4; the pattern tiles every 2π, i.e. every pattern size).
    p = mod(p, 6.2831853) - 250.0;
    vec2 i = p;
    float c = 1.0;
    const float inten = 0.005;
    for (int n = 0; n < 4; n++) {
        float tt = t * (1.0 - 3.5 / float(n + 1));
        i = p + vec2(cos(tt - i.x) + sin(tt + i.y), sin(tt - i.y) + cos(tt + i.x));
        c += 1.0 / length(vec2(p.x / (sin(i.x + tt) / inten), p.y / (cos(i.y + tt) / inten)));
    }
    c /= 4.0;
    c = 1.17 - pow(c, 1.4);
    return clamp(pow(abs(c), 8.0), 0.0, 1.0);
}

// waterCaustics is the factor the light on a surface at p (normal n) is
// multiplied by for the caustics of any water body it lies under: 1 where
// there are none.
float waterCaustics(vec3 p, vec3 n) {
    float f = 1.0;
    for (int i = 0; i < WATER_MAX_CAUSTICS; i++) {
        if (i >= waterCausticCount) {
            break;
        }
        vec4 b = waterCausticBox[i];
        vec4 info = waterCausticInfo[i];
        float depth = info.x - p.y;
        if (depth <= 0.0 || p.x < b.x || p.z < b.y || p.x > b.z || p.z > b.w) {
            continue;
        }
        // Strongest a little under the surface, fading as the water takes
        // the light, and on faces turned to the sky.
        float fade = smoothstep(0.0, 0.25, depth) * exp(-depth * info.w * 0.6) * clamp(n.y, 0.0, 1.0);
        if (fade <= 0.002) {
            continue;
        }
        // Two layers, turned off the world axes and off each other: the
        // construction has a grain along its own axes that a single layer
        // shows as lines.
        vec2 q = p.xz / max(info.z, 0.1) * 6.2831853;
        vec2 q1 = mat2(0.88, 0.47, -0.47, 0.88) * q;
        vec2 q2 = mat2(0.31, -0.95, 0.95, 0.31) * q * 0.71 + 3.1;
        float c = waterCausticPattern(q1, time * 0.6);
        float c2 = waterCausticPattern(q2, time * 0.45 + 1.7);
        f *= 1.0 + info.y * fade * ((c + c2) * 1.4 - 0.3);
    }
    return max(f, 0.0);
}

// ---------- the surface ----------

// waterLinearDepth is the view distance of a depth-buffer sample.
float waterLinearDepth(float d) {
    float ndc = d * 2.0 - 1.0;
    return waterDepthParams.y / (ndc + waterDepthParams.x);
}

// waterDetailSlope is one sample of the ripple map: the slope of the tile at
// size `scale` metres, turned by `ang` radians and drifting by `drift`.
vec2 waterDetailSlope(vec2 p, float scale, float ang, vec2 drift) {
    float c = cos(ang), s = sin(ang);
    vec2 q = vec2(c * p.x + s * p.y, -s * p.x + c * p.y);
    vec2 sl = (texture(waterDetail, (q - drift) / scale).rg - 0.5) * WATER_DETAIL_SLOPE;
    // Back from the turned frame into world X/Z.
    return vec2(c * sl.x - s * sl.y, s * sl.x + c * sl.y);
}

// waterFlowMapped reports whether this surface reads its vertex colours as
// a flow map (both terms are uniforms: control flow stays uniform).
bool waterFlowMapped() {
    return waterFlowMap.x > 0.0 && vertexColors != 0;
}

// waterFallsOn reports whether this surface is a falling sheet (a uniform:
// control flow stays uniform).
bool waterFallsOn() {
    return waterFlowMap.z > 0.0;
}

// waterVertexFlow is the current a flow-mapped surface's vertex colour
// carries, m/s in world X/Z, times the material's strength.
vec2 waterVertexFlow() {
    return (fragVertColor.gb * 2.0 - 1.0) * WATER_FLOW_MAX * waterFlowMap.x;
}

// waterFlowPhases is the two-phase advection at this moment: xy the two
// copies' offsets along the current (m), z the first copy's weight.
vec3 waterFlowPhases(vec2 flow) {
    float T = max(waterFlowMap.y, 0.25);
    float p0 = fract(time / T);
    float p1 = fract(time / T + 0.5);
    // A copy's weight is 0 just as it restarts.
    return vec3(p0 * T, p1 * T, 1.0 - abs(2.0 * p0 - 1.0));
}

// waterBedCaustics is the factor the bed a water surface shows is
// multiplied by for the caustics this surface throws on it (phase W4, the
// file comment): bp is the bed's position, bn its facing, depth how far it
// lies under this surface, sun how much of the sun reaches the surface.
float waterBedCaustics(vec3 bp, vec3 bn, float depth, float sun) {
    float fade = smoothstep(0.0, 0.25, depth) * exp(-depth * waterAbsorb.w * 0.6) * clamp(bn.y, 0.0, 1.0) * sun;
    float strength = waterSwell.z;
    if (fade * strength <= 0.002) {
        return 1.0;
    }
    vec2 q = bp.xz / max(waterFlow.z * 0.9, 0.5) * 6.2831853;
    float c;
    if (waterFlowMapped()) {
        // Down a river the network rushes with the current: two copies
        // carried along it half a cycle apart, cross-faded (as the ripples).
        vec2 fv = waterVertexFlow();
        vec3 ph = waterFlowPhases(fv);
        vec2 k = 6.2831853 / max(waterFlow.z * 0.9, 0.5) * fv;
        vec2 q0 = mat2(0.88, 0.47, -0.47, 0.88) * (q - k * ph.x);
        vec2 q1 = mat2(0.31, -0.95, 0.95, 0.31) * (q - k * ph.y) * 0.71 + 3.1;
        c = 2.0 * (waterCausticPattern(q0, time * 0.6) * ph.z + waterCausticPattern(q1, time * 0.45 + 1.7) * (1.0 - ph.z));
    } else {
        vec2 q1 = mat2(0.88, 0.47, -0.47, 0.88) * q;
        vec2 q2 = mat2(0.31, -0.95, 0.95, 0.31) * q * 0.71 + 3.1;
        c = waterCausticPattern(q1, time * 0.6) + waterCausticPattern(q2, time * 0.45 + 1.7);
    }
    return clamp(1.0 + strength * fade * (c * 1.4 - 0.3), 0.0, 2.5);
}

// waterSurfaceNormal is the swell's normal tilted by the ripples. Taken at
// the top of fragment.glsl's main, before anything can discard, since the
// ripple samples need screen-space derivatives.
vec3 waterSurfaceNormal(vec3 p) {
    if (waterFallsOn()) {
        // A falling sheet: its own facing, leaned toward the sky, since
        // aerated water scatters the light from above whichever way the
        // sheet faces (waterFallsShade does the rest).
        vec3 g = dot(fragNormal, fragNormal) > 1e-8 ? normalize(fragNormal) : vec3(0.0, 1.0, 0.0);
        // Seen from behind (inside the notch) the sheet faces the eye, not
        // away (phase W5): its data is one-sided.
        g *= dot(g, camPos - p) < 0.0 ? -1.0 : 1.0;
        return normalize(g + vec3(0.0, 0.8, 0.0));
    }
    vec3 n = waterNormal(p.xz);
    float scale = max(waterFlow.z, 0.1);
    float str = waterFlow.w * 0.09;
    // The rings and the rain (phase W5).
    vec2 isl = waterInteractSlope(p.xz);
    waterRingSlopeLen = length(isl);
    if (str <= 0.0) {
        return normalize(n + vec3(-isl.x, 0.0, -isl.y));
    }
    vec2 drift = waterFlow.xy * time;
    vec2 sl;
    if (waterFlowMapped()) {
        // Two copies of two layers carried down the current (file
        // comment), a third slow layer under them so the cross-fade never
        // shows as a pulse. The offset is taken off the position, so it is
        // in world X/Z whatever each layer's turn.
        vec2 fv = waterVertexFlow();
        vec3 ph = waterFlowPhases(fv);
        vec2 q0 = p.xz - fv * ph.x;
        vec2 q1 = p.xz - fv * ph.y + vec2(0.37, 0.71) * scale;
        vec2 s0 = waterDetailSlope(q0, scale, 0.37, vec2(0.0)) + 0.7 * waterDetailSlope(q0, scale * 0.53, 1.61, vec2(0.0));
        vec2 s1 = waterDetailSlope(q1, scale, 0.37, vec2(0.0)) + 0.7 * waterDetailSlope(q1, scale * 0.53, 1.61, vec2(0.0));
        sl = s0 * ph.z + s1 * (1.0 - ph.z)
           + 0.6 * waterDetailSlope(p.xz, scale * 2.17, -0.83, drift * 0.6);
        // Faster water is choppier, and white water — riffles, boulders,
        // a fall's pool, a confluence's mixing line (the vertex foam) — is
        // churned rougher still (phase W4).
        str *= (1.0 + 0.3 * min(length(fv), 3.0)) * (1.0 + 1.1 * fragVertColor.r);
    } else {
        // Three headings off the world axes and sizes in no simple ratio: a
        // repeating tile seen down its own period lines up into rays toward
        // the vanishing point, and three tiles that never repeat together
        // do not.
        sl = waterDetailSlope(p.xz, scale, 0.37, drift)
           + 0.7 * waterDetailSlope(p.xz, scale * 0.53, 1.61, drift * 1.35)
           + 0.8 * waterDetailSlope(p.xz, scale * 2.17, -0.83, drift * 0.6);
    }
    // Distant and grazing ripples are averaged by the mips already, and the
    // anisotropic filter stretches what is left along the view ray into
    // streaks; less of them there keeps the far surface calm.
    float grazing = clamp(normalize(camPos - p).y * 4.0, 0.35, 1.0);
    str *= grazing / (1.0 + fragViewDepth * 0.015);
    return normalize(n + vec3(-sl.x * str - isl.x, 0.0, -sl.y * str - isl.y));
}

// waterSkyLight is the light arriving along direction r from the sky, or
// from the ambient light where the level has no sky.
vec3 waterSkyLight(vec3 r, float rough) {
    if ((waterFrameFlags & WATER_HAVE_SKY) != 0) {
        // Near the horizon the blur is held back: a wide mip there averages
        // the zenith's saturated blue (and the map's lower half) into the
        // pale band a grazing reflection should show (phase W3).
        float lod = clamp(sqrt(rough) * waterSkyMips * mix(0.35, 1.0, smoothstep(0.05, 0.5, r.y)), 0.0, max(waterSkyMips - 1.0, 0.0));
        vec3 tint = reflectColor / max(max(reflectColor.r, reflectColor.g), max(reflectColor.b, 1e-3));
        // A little of the sky's saturation is taken out (a rough surface
        // mixes the horizon's haze into every reflected ray, and an HDR
        // blue sky survives tone mapping as pure blue otherwise), then the
        // tint: so a lake under a clear sky reads grey-blue-green, not
        // cartoon blue (phase W3).
        vec3 sky = textureLod(waterSky, waterOctEncode(r), lod).rgb;
        // Rougher water takes out more (phase W4): churned white water
        // mixes the whole sky into each pixel, and read as patches of
        // saturated zenith blue.
        sky = mix(vec3(dot(sky, vec3(0.2126, 0.7152, 0.0722))), sky, 0.6 - 0.3 * smoothstep(0.12, 0.5, rough));
        return sky * tint;
    }
    return reflectColor * ambientLight(globalLightColor * globalLightBrightness, fragWorldPos, r);
}

// waterSSR marches the reflected ray r from p through the depth copy. rgb
// is the scene there, a the confidence (0: nothing found).
vec4 waterSSR(vec3 p, vec3 r) {
    float stepLen = 0.15 + fragViewDepth * 0.015;
    vec3 prev = p;
    vec3 pos = p;
    for (int i = 0; i < 16; i++) {
        stepLen *= 1.22;
        prev = pos;
        pos += r * stepLen;
        vec4 c = waterViewProj * vec4(pos, 1.0);
        if (c.w <= 0.05) {
            break;
        }
        vec2 uv = c.xy / c.w * 0.5 + 0.5;
        if (uv.x < 0.0 || uv.y < 0.0 || uv.x > 1.0 || uv.y > 1.0) {
            break;
        }
        float d = textureLod(waterSceneDepth, uv, 0.0).r;
        if (d >= 0.999999) {
            continue; // the sky: the sky map answers it better
        }
        float sz = waterLinearDepth(d);
        // A crossing, and a THIN one: a ray that passes a metre behind a
        // rock is not reflecting the rock, and accepting it draws the rock's
        // ghost beside it on the water.
        if (c.w > sz && c.w - sz < clamp(stepLen * 1.2, 0.25, 0.8)) {
            // Halve back to the crossing.
            vec3 a = prev, b = pos;
            for (int k = 0; k < 4; k++) {
                vec3 m = (a + b) * 0.5;
                vec4 cm = waterViewProj * vec4(m, 1.0);
                float dm = waterLinearDepth(textureLod(waterSceneDepth, cm.xy / cm.w * 0.5 + 0.5, 0.0).r);
                if (cm.w > dm) {
                    b = m;
                } else {
                    a = m;
                }
            }
            vec4 cb = waterViewProj * vec4(b, 1.0);
            vec2 huv = cb.xy / cb.w * 0.5 + 0.5;
            vec2 edge = smoothstep(vec2(0.0), vec2(0.08), huv) * (1.0 - smoothstep(vec2(0.92), vec2(1.0), huv));
            float conf = edge.x * edge.y * (1.0 - float(i) / 16.0);
            return vec4(textureLod(waterSceneColour, huv, 0.0).rgb, conf);
        }
    }
    return vec4(0.0);
}

// waterGGX is the GGX glint of one light of radiance `rad` from ldir.
vec3 waterGGX(vec3 n, vec3 v, vec3 ldir, vec3 rad, float rough) {
    vec3 h = normalize(ldir + v);
    float nl = max(dot(n, ldir), 0.0), nv = max(dot(n, v), 1e-3), nh = max(dot(n, h), 0.0);
    if (nl <= 0.0) {
        return vec3(0.0);
    }
    float a = max(rough * rough, 0.0008);
    float a2 = a * a;
    float dd = nh * nh * (a2 - 1.0) + 1.0;
    float D = a2 / (3.14159265 * dd * dd);
    // Height-correlated Smith visibility.
    float gv = nl * sqrt(nv * nv * (1.0 - a2) + a2);
    float gl = nv * sqrt(nl * nl * (1.0 - a2) + a2);
    float vis = 0.5 / max(gv + gl, 1e-5);
    float F = WATER_F0 + (1.0 - WATER_F0) * pow(1.0 - max(dot(h, v), 0.0), 5.0);
    return rad * (D * vis * F * nl);
}

// waterFallsShade is a falling sheet's colour (phase W4; the file comment):
// streaks of the detail map stretched down the sheet's V and scrolled at
// falls_speed, aerated white by the vertex red, glassy elsewhere with the
// scene behind seen through it, frayed at the sides by the vertex green. A
// mesh without vertex colours (a brush face hung as a fall) is taken as
// fairly aerated all over.
vec3 waterFallsShade(vec3 n, vec3 lighting, inout float alpha) {
    vec3 V = normalize(camPos - fragWorldPos);
    bool vc = vertexColors != 0;
    float aer = vc ? fragVertColor.r : 0.7;
    float edge = vc ? fragVertColor.g : 1.0;
    float prog = vc ? fragVertColor.b : 0.5;
    float strength = clamp(waterFlowMap.z, 0.0, 1.0);
    float sp = max(waterFlowMap.w, 0.1);
    float sc = max(waterFlow.z, 0.1);
    vec2 uv = fragTexCoord;
    // Streaks: the detail map's noise squeezed across and drawn out down
    // the sheet, two layers, the second faster (the water speeds up as it
    // falls) and offset so their repeats never line up; a little of the
    // foam network on top for the ragged white threads.
    vec4 s1 = texture(waterDetail, vec2(uv.x / (sc * 0.3), (uv.y - time * sp) / (sc * 4.0)));
    vec4 s2 = texture(waterDetail, vec2(uv.x / (sc * 0.17) + 0.37, (uv.y - time * sp * 1.4) / (sc * 2.6) + 0.21));
    float streak = clamp((0.6 * s1.a + 0.4 * s2.a - 0.5) * 2.2 + 0.5 + 0.3 * s2.b, 0.0, 1.0);
    // Seen edge-on the streaks, scrolling down V, read as horizontal ribs
    // (phase W4's note): toward grazing they flatten out and the sheet
    // thins away, the white water staying longest.
    vec3 gN = dot(fragNormal, fragNormal) > 1e-8 ? normalize(fragNormal) : vec3(0.0, 0.0, 1.0);
    float edgeOn = smoothstep(0.04, 0.35, abs(dot(gN, V)));
    streak = mix(0.5, streak, edgeOn);
    // White water: the sheet's aeration, broken up by the streaks, and the
    // boil at the foot.
    float white = aer * (0.55 + 0.8 * streak) + 0.45 * smoothstep(0.75, 1.0, prog);
    white = smoothstep(0.25, 0.8, white) * strength;
    // Never quite clear where the sheet is thick: a veil of white over
    // what shows through (phase W5).
    white = max(white, 0.28 * strength * smoothstep(0.1, 0.5, aer) * edge);
    vec3 light = clamp(lighting, 0.0, hdrLightMax());
    // Foam scatters what reaches it in every direction: lit by the scene's
    // light, a touch of the sky's on top.
    vec3 N = n;
    float nv = clamp(abs(dot(N, V)), 0.0, 1.0);
    vec3 R = reflect(-V, N);
    R.y = abs(R.y);
    float rough = clamp(waterLook.x, 0.05, 1.0);
    vec3 sky = waterSkyLight(R, rough);
    // A good share of the sky too (phase W5): white water in the shade of
    // its notch still glows with the skylight it scatters.
    vec3 foamCol = vec3(0.94, 0.94, 0.92) * (light + 0.22 * sky);
    float F = clamp((WATER_F0 + (1.0 - WATER_F0) * pow(1.0 - nv, 5.0)) * reflectivity * 2.0, 0.0, 1.0);
    // How much of the sheet is there at all: none past its frayed sides,
    // thin where it is glassy and the streaks part.
    float cover = smoothstep(0.0, 1.0, edge * (0.85 + 0.6 * streak)) * mix(0.75 + 0.25 * streak, 1.0, white);
    cover *= mix(edgeOn, 1.0, 0.6 * white);
    vec2 suv = (gl_FragCoord.xy - waterViewport.xy) / max(waterViewport.zw, vec2(1.0));
    if ((waterFrameFlags & WATER_HAVE_COLOUR) != 0) {
        // Glassy water: the scene behind it bent by the streaks and tinted
        // by a few centimetres of the river, the sky on top by fresnel.
        // Barely bent and veiled (phase W5): bent by the full streaks, the
        // pale sand behind a fall was drawn down the sheet as brown streaks.
        vec2 off = vec2(s1.r - 0.5, s2.g - 0.5) * 0.01 * waterLook.y;
        vec3 behind = textureLod(waterSceneColour, clamp(suv + off, vec2(0.001), vec2(0.999)), 0.0).rgb;
        vec3 plain = textureLod(waterSceneColour, suv, 0.0).rgb;
        vec3 T = pow(clamp(waterAbsorb.rgb, vec3(0.001), vec3(1.0)), vec3(0.6 * waterAbsorb.w));
        vec3 seen = behind * T;
        seen = mix(vec3(dot(seen, vec3(0.2126, 0.7152, 0.0722))), seen, 0.5) * 0.75;
        vec3 glass = seen * (1.0 - F) + sky * F + waterScatter.rgb * light * 0.5 + foamCol * 0.12;
        vec3 col = mix(glass, foamCol, white);
        return mix(plain, col, cover);
    }
    // Blended: the glassy part lets the framebuffer through.
    vec3 glass = sky * F + waterScatter.rgb * light * 0.5;
    alpha *= cover * mix(0.35, 1.0, white);
    return mix(glass / max(mix(0.35, 1.0, white), 1e-3) * mix(0.35, 1.0, white), foamCol, white);
}

// waterBelowShade is the surface seen from under the water (phase W5; the
// file comment): inside Snell's window — the light from above that refracts
// down to the eye, within ~48.6° of straight up — the world above, bent by
// the ripples and dimmed by the water-to-air fresnel; outside it the water
// mirrors itself (total internal reflection), its own lit murk. The colour
// copy behind the surface was fogged by the same water between the eye and
// the surface (water_interact.go's fog volume); it is unfogged here (the
// fog is affine in the colour) since the fragment's fog is applied once
// more over what this returns.
vec3 waterBelowShade(vec3 N, vec3 V, vec3 light, vec2 uv, bool haveColour, inout float alpha) {
    vec3 Nb = -N;
    float cosI = clamp(dot(V, Nb), 0.0, 1.0);
    float sinT = 1.333 * sqrt(max(1.0 - cosI * cosI, 0.0));
    float window = 1.0 - smoothstep(0.88, 1.0, sinT);
    float cosT = sqrt(max(1.0 - sinT * sinT, 0.0));
    float F = mix(1.0, WATER_F0 + (1.0 - WATER_F0) * pow(1.0 - cosT, 5.0), window);
    // The water's murk: what the frame's fog makes of a point far off
    // level with the eye — under water, the water's own fog volume
    // (render/water_interact.go), lit as the CPU lit it. The surface's own
    // lighting from below is unlit back-face light, near black.
    vec3 murk = applyFogScene(vec3(0.0), camPos + vec3(150.0, 0.0, 0.0));
    if (haveColour) {
        // The window swims with the ripples, more than the bed does from
        // above: the slopes bend the steep rays near its rim the most. Held
        // to a few pixels — the surface may be a hand's breadth from the
        // lens, where the ripples are huge on screen.
        vec2 off = N.xz * (0.015 + 0.04 * (1.0 - cosI)) * max(waterLook.y, 0.2);
        vec3 seen = textureLod(waterSceneColour, clamp(uv + off, vec2(0.001), vec2(0.999)), 0.0).rgb;
        vec3 f0 = applyFogScene(vec3(0.0), fragWorldPos);
        vec3 f1 = applyFogScene(vec3(1.0), fragWorldPos);
        seen = max((seen - f0) / max(f1 - f0, vec3(0.03)), vec3(0.0));
        return seen * (1.0 - F) + murk * F;
    }
    // Blended: the window lets the framebuffer (the sky, the bank) through.
    alpha *= mix(1.0, 0.25, window * (1.0 - F));
    return murk;
}

// waterShade is a water fragment's colour. n is the surface normal (either
// side), lighting the diffuse light at the surface; alpha comes in as the
// surface's own and leaves as what the framebuffer blend should use.
vec3 waterShade(vec3 n, vec3 lighting, inout float alpha) {
    if (waterFallsOn()) {
        return waterFallsShade(n, lighting, alpha);
    }
    vec3 N = n.y < 0.0 ? -n : n;
    vec3 V = normalize(camPos - fragWorldPos);
    bool below = camPos.y < fragWorldPos.y;
    float rough = clamp(waterLook.x, 0.01, 1.0);
    // White water is rough water: a river's vertex foam (riffles, a fall's
    // pool, a confluence) blurs its reflection and keeps it off a mirror
    // (phase W4: a churned plunge pool read as patches of saturated sky).
    if (waterFlowMapped()) {
        rough = min(rough + 0.3 * fragVertColor.r, 1.0);
    }
    // A rough look at distance: a pixel there covers many ripples.
    rough = sqrt(rough * rough + fragViewDepth * 0.0004);

    vec2 uv = (gl_FragCoord.xy - waterViewport.xy) / max(waterViewport.zw, vec2(1.0));
    bool haveDepth = (waterFrameFlags & WATER_HAVE_DEPTH) != 0;
    bool haveColour = (waterFrameFlags & WATER_HAVE_COLOUR) != 0;

    // How far the ray runs through water, and how deep the bed is under it.
    float surfZ = max(fragViewDepth, 1e-3);
    float rayLen = length(camPos - fragWorldPos);
    float sceneZ = haveDepth ? waterLinearDepth(textureLod(waterSceneDepth, uv, 0.0).r) : surfZ + 3.0;
    float path = max(sceneZ - surfZ, 0.0) * rayLen / surfZ;
    float vdepth = max(V.y * path, 0.0);

    // Refraction: the bed seen bent by the ripples, more so the deeper it
    // is — unless the bent sample lands on something in front of the water.
    vec2 ruv = uv;
    if (haveColour) {
        vec2 off = N.xz * waterLook.y * 0.08 * clamp(vdepth, 0.0, 1.5) / (1.0 + surfZ * 0.03);
        vec2 cand = clamp(uv + off, vec2(0.001), vec2(0.999));
        float cz = waterLinearDepth(textureLod(waterSceneDepth, cand, 0.0).r);
        if (cz > surfZ) {
            ruv = cand;
            path = max(cz - surfZ, 0.0) * rayLen / surfZ;
            vdepth = max(V.y * path, 0.0);
        }
    }
    path = min(path, 400.0);
    vdepth = min(vdepth, 400.0);

    // Beer–Lambert over the way down to the bed and back up the view ray.
    vec3 shallow = clamp(waterAbsorb.rgb, vec3(0.001), vec3(1.0));
    vec3 T = pow(shallow, vec3((path + vdepth) * waterAbsorb.w));
    vec3 light = clamp(lighting, 0.0, hdrLightMax());
    vec3 inscatter = waterScatter.rgb * light;

    // The sun: its glint, its share of the crest glow.
    vec3 glint = vec3(0.0);
    vec3 crest = vec3(0.0);
    float amp = max(waveAmplitude + waterSwell.x, 1e-3);
    float hRel = clamp(waterHeight(fragWorldPos.xz) / amp * 0.5 + 0.5, 0.0, 1.0);
    // The colour light picks up through the thin top of a wave: the deep
    // colour, brightened by what half a metre of this water lets through
    // (clear water's crests glow green-turquoise, a swamp's barely).
    vec3 crestTint = waterScatter.rgb * 1.5 + pow(shallow, vec3(2.0 * waterAbsorb.w)) * 0.06;
    // How much of the sun reaches the surface (the caustics on the bed
    // need it; phase W4).
    float sunVis = 0.0;
    for (int i = 0; i < numDirLights; i++) {
        vec3 ldir = -normalize(dirLightDir[i]);
        vec3 rad = dirLightColor[i] * dirLightBrightness[i];
        float vis = 1.0;
        if (dirLightCastShadows[i]) {
            vis = 1.0 - calcDirShadow(i, vec3(0.0, 1.0, 0.0), ldir) * dirLightShadowStrength[i];
        }
        vis *= cloudShadowFactor(i, fragWorldPos);
        if (!below) {
            glint += waterGGX(N, V, ldir, rad, rough) * vis;
        }
        sunVis = max(sunVis, vis * clamp(ldir.y * 4.0, 0.0, 1.0));
        // Looking toward the sun across the water: the crests are back-lit.
        // Strongest on the high part of a wave (hRel) and on the faces
        // turned away from the sun toward the eye, which the light leaves
        // through (phase W4: the face term and the tint).
        vec3 hl = normalize(vec3(ldir.x, 0.0, ldir.z) + 1e-5);
        float back = pow(clamp(dot(normalize(vec3(-V.x, 0.0, -V.z) + 1e-5), hl), 0.0, 1.0), 4.0);
        float face = 0.35 + 0.65 * clamp(-dot(N.xz, hl.xz) * 6.0, 0.0, 1.0);
        crest += crestTint * rad * (waterScatter.a * back * face * hRel * hRel * vis * clamp(ldir.y + 0.2, 0.0, 1.0) * 2.0);
    }

    // Reflection.
    float nv = clamp(dot(N, V), 0.0, 1.0);
    // Roughness-aware Schlick (Lagarde): a rough surface never reaches a
    // full mirror at grazing angles — the ripples' own slopes and shadowing
    // keep some of the body showing — which is what stopped a choppy river
    // or a marsh from reading as a sheet of pure sky blue (phase W3).
    float Fmax = max(1.0 - rough, WATER_F0);
    float F = clamp((WATER_F0 + (Fmax - WATER_F0) * pow(1.0 - nv, 5.0)) * reflectivity * 2.0, 0.0, 1.0);
    // The reflected ray from the ripples' slopes cut to a third: seen at a
    // grazing angle, the full slopes swing the ray from the pale horizon up
    // to the saturated zenith, and the far water read as a sheet of pure sky
    // blue. Real ripples average out over the many a pixel covers there (the
    // fresnel and the glint keep the full normal). Phase W3.
    vec3 Nr = normalize(vec3(N.x * 0.35, N.y, N.z * 0.35));
    vec3 R = reflect(-V, Nr);
    R.y = abs(R.y);
    vec3 refl = waterSkyLight(R, rough);
    if ((waterFrameFlags & WATER_HAVE_SSR) != 0 && waterSwell.w > 0.0 && haveDepth && haveColour && !below) {
        vec4 s = waterSSR(fragWorldPos, R);
        refl = mix(refl, s.rgb, s.a * waterSwell.w * (1.0 - smoothstep(0.25, 0.6, rough)));
    }

    // Foam where the bed comes within a band of the surface — a third of
    // the band on a river, whose foam is its vertex foam (phase W4).
    float band = (0.25 + 0.75 * waterLook.z) * (waterFlowMapped() ? WATER_RIVER_SHORE : 1.0);
    float foamMask = haveDepth ? 1.0 - smoothstep(0.0, band, vdepth) : 0.0;
    float vfoam = vertexColors != 0 ? fragVertColor.r : 0.0;
    foamMask = max(foamMask, vfoam);
    // A strong young ring's white (a body dropping in; phase W5).
    foamMask = max(foamMask, waterRippleFoam(fragWorldPos.xz));
    float foam = 0.0;
    // Sampled outside the branch below: implicit derivatives are only defined
    // under uniform control flow.
    float fscale = max(waterFlow.z, 0.1);
    // Foam drifts as streaks along the water's motion (phase W5): its
    // coordinates are squeezed along the current — a river's flow map, or
    // still water's drift — by up to 3x on fast water.
    vec2 fmove = waterFlowMapped() ? waterVertexFlow() : waterFlow.xy;
    float fspeed = length(fmove);
    vec2 fdir = fspeed > 1e-4 ? fmove / fspeed : vec2(1.0, 0.0);
    vec2 fperp = vec2(-fdir.y, fdir.x);
    float fstretch = 1.0 + 2.0 * clamp(fspeed / 1.5, 0.0, 1.0);
    vec2 sp = fdir * (dot(fragWorldPos.xz, fdir) / fstretch) + fperp * dot(fragWorldPos.xz, fperp);
    vec2 sdrift = fdir * (dot(waterFlow.xy * time * 0.4, fdir) / fstretch);
    vec4 d1 = texture(waterDetail, (sp - sdrift) / (fscale * 0.8));
    vec4 d2 = texture(waterDetail, fragWorldPos.xz / (fscale * 5.0) + vec2(0.37, 0.71));
    // The patches: a mid scale of the noise, carried with the foam.
    vec4 d3 = texture(waterDetail, (sp - sdrift * 0.7) / (fscale * 2.3) + vec2(0.11, 0.83));
    if (waterFlowMapped()) {
        // The foam carried down the current, two-phase like the ripples:
        // a river's foam streams away from the riffle that throws it.
        vec2 fv = fmove;
        vec3 ph = waterFlowPhases(fv);
        vec2 o0 = fdir * (dot(fv * ph.x, fdir) / fstretch);
        vec2 o1 = fdir * (dot(fv * ph.y, fdir) / fstretch);
        vec4 a = texture(waterDetail, (sp - o0) / (fscale * 0.8));
        vec4 b = texture(waterDetail, (sp - o1) / (fscale * 0.8) + vec2(0.53, 0.29));
        d1 = a * ph.z + b * (1.0 - ph.z);
        vec4 a3 = texture(waterDetail, (sp - o0) / (fscale * 2.3) + vec2(0.11, 0.83));
        vec4 b3 = texture(waterDetail, (sp - o1) / (fscale * 2.3) + vec2(0.64, 0.42));
        d3 = a3 * ph.z + b3 * (1.0 - ph.z);
    }
    if (waterLook.z > 0.0 && foamMask > 0.0) {
        float pattern = d1.b * (0.6 + 0.8 * d2.a);
        // Stronger toward the shore: the threshold falls, so more of the
        // network shows, but the cells between its lines stay open — lace,
        // not a white band.
        float edgeBand = foamMask * min(waterLook.z, 1.5);
        float thr = 1.0 - 0.75 * min(edgeBand, 1.0);
        foam = smoothstep(thr, thr + 0.2, pattern + 0.2 * edgeBand);
        // Organic, not a net (phase W5): the lace only where two scales of
        // noise gather it into patches, which the stronger band widens, and
        // the hearts of the thickest patches close into clots.
        float clump = 0.55 * d2.a + 0.45 * d3.a;
        float patches = smoothstep(0.46, 0.72, clump + 0.22 * min(edgeBand, 1.0) - 0.08);
        float clot = smoothstep(0.66, 0.86, clump) * smoothstep(0.3, 0.9, edgeBand);
        foam = max(foam * patches, clot * (0.55 + 0.45 * d1.b));
        foam = clamp(foam, 0.0, 1.0) * 0.85 * smoothstep(0.0, 0.15, edgeBand);
        // Where the vertex foam is near full — the boil under a fall, the
        // white water round a boulder — the lace closes into a churned
        // white sheet, broken only by the coarse noise (phase W4).
        float boil = smoothstep(0.6, 0.95, vfoam) * min(waterLook.z, 1.0);
        foam = max(foam, boil * smoothstep(0.25, 0.75, 0.55 * d2.a + 0.45 * d1.b + 0.25 * boil));
    }

    // Everything but the bed, and how much of the bed shows through.
    vec3 Tv = T * (1.0 - F) * (1.0 - foam);
    vec3 body = inscatter * (1.0 - T) + crest;
    vec3 c = body * (1.0 - F) * (1.0 - foam) + refl * F + glint + vec3(0.92) * light * foam;
    // A ring's crests catch the sky (phase W5): seen from above, where the
    // fresnel leaves little reflection to bend, rings would otherwise only
    // shift the bed a pixel or two.
    c += refl * (min(waterRingSlopeLen * 0.8, 0.18) * (1.0 - foam) * (1.0 - F));

    float edge = haveDepth ? smoothstep(0.0, max(waterLook.w, 1e-3), vdepth) : 1.0;
    // The bed under this pixel, back out of the depth copy along the view
    // ray (the refracted sample's depth where refraction took one), and its
    // facing from its screen derivatives: taken here, under uniform control
    // flow, for the caustics below (phase W4).
    float bedZ = surfZ + path * surfZ / max(rayLen, 1e-3);
    vec3 bedPos = camPos + (fragWorldPos - camPos) * (bedZ / surfZ);
    vec3 bedN = cross(dFdx(bedPos), dFdy(bedPos));
    bedN = dot(bedN, bedN) > 1e-12 ? normalize(bedN) : vec3(0.0, 1.0, 0.0);
    bedN *= bedN.y < 0.0 ? -1.0 : 1.0;
    if (below) {
        // From under the surface (phase W5): Snell's window and the total
        // internal reflection round it. Here, after every derivative was
        // taken under uniform control flow.
        return waterBelowShade(N, V, light, uv, haveColour, alpha);
    }
    if (haveColour) {
        vec3 bed = textureLod(waterSceneColour, ruv, 0.0).rgb;
        vec3 bedPlain = textureLod(waterSceneColour, uv, 0.0).rgb;
        // Wet, and lit by the caustics this surface throws on it.
        bed *= mix(1.0, WATER_WET, edge);
        if ((waterFrameFlags & WATER_HAVE_CAUSTICS) != 0 && !below) {
            bed *= waterBedCaustics(bedPos, bedN, max(fragWorldPos.y - bedPos.y, 0.0), sunVis);
        }
        vec3 col = c + bed * Tv;
        // The soft shoreline: to the bed seen plainly at zero depth.
        return mix(bedPlain, col, edge);
    }
    // Blended: the bed is what the framebuffer already holds.
    float a = clamp(1.0 - dot(Tv, vec3(1.0 / 3.0)), 0.02, 1.0);
    alpha *= a * edge;
    return c / a;
}
