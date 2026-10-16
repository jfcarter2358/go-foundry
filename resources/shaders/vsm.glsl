// vsm.glsl — the virtual shadow map's lookup (render/vsm.go; Phase 5 of the
// Nanite plan, component 10). Spliced into every program that shades with
// the sun's shadow: the world program (gfx.WorldProgramSources), and the
// biome, sprite and foliage programs with biome_light.glsl. The marking pass
// (vsm_mark_comp.glsl) compiles it too, so a receiver reads exactly the page
// it asked for.
//
// The map is a clipmap of VSM_LEVELS levels in light space, each twice the
// last one's width, each VSM_PAGES × VSM_PAGES pages of VSM_PAGE_TEXELS²
// texels round the camera (render/vsm_pages.go). A point reads the level
// whose texel is about its own pixel's footprint (vsmLevelFor) — the level is
// chosen by distance, not view depth, so turning on the spot keeps every
// point on the same page. A page that is not resident (not asked for yet,
// or something in it just changed) falls back to the next coarser level, and
// a point no level holds is lit.
//
// The page table is a usampler2DArray, a layer a level: the physical page's
// layer in vsmAtlas + 1, or 0. vsmAtlas is one page a layer, so a bilinear
// compare never mixes two pages' texels; a filter tap across a page's edge
// finds its own page.
//
// vsmAtlas is a plain sampler2DArray, compared by hand (vsmCompare: a
// textureGather of the 2x2 texels and the bilinear weights a comparison
// sampler would use), NOT a sampler2DArrayShadow: on Mesa's D3D12 driver a
// second comparison sampler array in one program — fragment.glsl already has
// the cascades' shadowMapArray — leaves the first reading as lit everywhere,
// whether or not the second is ever sampled.
//
// Must match render/vsm_pages.go.
// softShadowsOn, sunAngularRadius and shadowNoiseFrame (vsmSampleLevelPCSS).
#include "scene_block.glsl"

#define VSM_LEVELS 11
#define VSM_PAGES 32
#define VSM_PAGE_TEXELS 128.0
// What a level's lookup answers when its pages are not all resident: any
// answer above it is an occlusion in [0, 1] (vsmSampleLevel clamps its own).
#define VSM_NOT_RESIDENT -0.5

// One more than the directional light the map shadows; 0 (what a program
// nobody told holds) for none — the cascades as before.
uniform int vsmLightSlot;

// vsmShadows is whether light lightIdx reads the virtual map.
bool vsmShadows(int lightIdx) {
    return lightIdx + 1 == vsmLightSlot;
}
uniform sampler2DArray vsmAtlas;
uniform usampler2DArray vsmPageTable;
// World to light space, about the camera: rows across the light (x, y) and
// along it (z, growing away from the light).
uniform mat3 vsmRot;
uniform vec3 vsmCamPos;
// The camera's place in each level's window, in that level's texels.
uniform vec2 vsmCamTexel[VSM_LEVELS];
// Level 0's texel in world units, and log2 of (a pixel's footprint at unit
// distance / level 0's texel): a point d away wants level log2(d) + this.
uniform float vsmTexel0;
uniform float vsmLevelBias;
// Light-space z about the camera to the map's [0, 1] depth.
uniform float vsmDepthOffset;
uniform float vsmDepthScale;
// The receiver's depth bias and normal offset, in its level's texels.
uniform float vsmBias;
uniform float vsmNormalOffset;

// vsmLevelFor is the level a point at distance d from the camera reads.
int vsmLevelFor(float d) {
    return clamp(int(floor(log2(max(d, 1e-3)) + vsmLevelBias + 0.5)), 0, VSM_LEVELS - 1);
}

// vsmWindowTexel is where light-space point ls (about the camera) falls in
// level l's window, in texels.
vec2 vsmWindowTexel(vec3 ls, int l) {
    return ls.xy / (vsmTexel0 * exp2(float(l))) + vsmCamTexel[l];
}

// vsmLayer is the atlas layer of window page pg on level l, -1 for none.
int vsmLayer(ivec2 pg, int l) {
    if (any(lessThan(pg, ivec2(0))) || any(greaterThanEqual(pg, ivec2(VSM_PAGES)))) {
        return -1;
    }
    return int(texelFetch(vsmPageTable, ivec3(pg, l), 0).r) - 1;
}

// vsmCompare is the fraction of the bilinear footprint at uv in layer that
// is at least ref deep: what a comparison sampler's lookup returns, from a
// gather of its 2x2 texels.
float vsmCompare(vec2 uv, float layer, float ref) {
    vec4 lit = vec4(greaterThanEqual(textureGather(vsmAtlas, vec3(uv, layer), 0), vec4(ref)));
    vec2 f = fract(uv * VSM_PAGE_TEXELS - 0.5);
    // Gather's order: (0,1), (1,1), (1,0), (0,0).
    return mix(mix(lit.w, lit.z, f.x), mix(lit.x, lit.y, f.x), f.y);
}

// vsmSampleLevel is the occlusion (0 lit .. 1 shadowed) at worldPos from
// level l, or -1 when the pages its filter needs are not all resident: nine
// bilinear comparison taps a texel apart, each on the page it falls in.
//
// (Four gathers with per-texel weights would do the same work in fewer
// fetches, but measured, the shape of this function is what the world
// program and the material pass pay for on every pixel, the virtual map on
// or not: the four-gather forms cost the material pass 0.9 and 5.4 ms more
// on level1 with the map off, and this one nothing measurable. docs/nanite/PHASE_5.md.)
float vsmSampleLevel(vec3 worldPos, vec3 normal, vec3 lightDir, int l) {
    float texel = vsmTexel0 * exp2(float(l));
    // Pushed off the surface by a texel or so along its normal, and its
    // depth biased by more where the sun grazes it: one texel of a slope seen
    // edge-on spans that much depth.
    vec3 p = worldPos + normal * (texel * vsmNormalOffset);
    vec3 ls = vsmRot * (p - vsmCamPos);
    vec2 t = vsmWindowTexel(ls, l);
    // The filter's bilinear taps reach 1.5 texels each way: a page or the
    // next on each axis (a page is far wider than the filter), so at most
    // the four pages lo..hi, each read from the table once here. Every tap
    // below falls on one of them and takes its layer from these, where it
    // used to read the table again: thirteen fetches a level at worst, now
    // four, and one where the filter sits inside a page, as it nearly always
    // does.
    ivec2 lo = ivec2(floor((t - 1.5) / VSM_PAGE_TEXELS));
    ivec2 hi = ivec2(floor((t + 1.5) / VSM_PAGE_TEXELS));
    int l00 = vsmLayer(lo, l);
    int l10 = hi.x != lo.x ? vsmLayer(ivec2(hi.x, lo.y), l) : l00;
    int l01 = hi.y != lo.y ? vsmLayer(ivec2(lo.x, hi.y), l) : l00;
    int l11 = hi.x != lo.x ? (hi.y != lo.y ? vsmLayer(hi, l) : l10) : l01;
    if (min(min(l00, l10), min(l01, l11)) < 0) {
        return -1.0;
    }
    float cosTheta = clamp(abs(dot(normal, lightDir)), 0.05, 1.0);
    float slope = min(sqrt(1.0 - cosTheta * cosTheta) / cosTheta, 8.0);
    float ref = (ls.z + vsmDepthOffset) * vsmDepthScale - texel * (vsmBias + 0.5 * slope) * vsmDepthScale;
    float lit = 0.0;
    for (int y = -1; y <= 1; y++) {
        for (int x = -1; x <= 1; x++) {
            vec2 tt = t + vec2(x, y);
            ivec2 pg = ivec2(floor(tt / VSM_PAGE_TEXELS));
            vec2 uv = (tt - vec2(pg) * VSM_PAGE_TEXELS) / VSM_PAGE_TEXELS;
            bvec2 up = greaterThan(pg, lo);
            int layer = up.y ? (up.x ? l11 : l01) : (up.x ? l10 : l00);
            lit += vsmCompare(uv, float(layer), ref);
        }
    }
    // Clamped: nine bilinear weights that should sum to 9 on a fully lit
    // receiver can round to a hair over it, and 1 - 9.0000005/9 is a hair
    // under zero — which vsmShadow took for "not resident" (VSM_NOT_RESIDENT)
    // and walked on to the coarser levels, whose pages nobody asks for: a
    // lit pixel then read whatever a coarse page held when it was last
    // drawn, and a moving character left its shadow behind on them
    // (docs/adventure/PHASE_B.md, "the ghost shadow").
    return clamp(1.0 - lit / 9.0, 0.0, 1.0);
}

// vsmShadow is the occlusion at worldPos (geometric normal, the light's
// direction of travel): its level's pages, or the nearest coarser level's
// that are resident, or lit.
// vsmSampleLevel1 is vsmSampleLevel's one bilinear tap, for a caller that
// evaluates the shadow per VERTEX by the million (the grass instancer,
// render/grass_field.go): a blade is narrower than a texel, so the nine-tap
// filter's softness is lost on it, and it cost the grass ~8 ms on the GTX
// 1070 (docs/regions/PRESETS_2B.md). -1 when the page is not resident.
float vsmSampleLevel1(vec3 worldPos, vec3 normal, vec3 lightDir, int l) {
    float texel = vsmTexel0 * exp2(float(l));
    vec3 p = worldPos + normal * (texel * vsmNormalOffset);
    vec3 ls = vsmRot * (p - vsmCamPos);
    vec2 t = vsmWindowTexel(ls, l);
    ivec2 pg = ivec2(floor(t / VSM_PAGE_TEXELS));
    int layer = vsmLayer(pg, l);
    if (layer < 0) {
        return -1.0;
    }
    float cosTheta = clamp(abs(dot(normal, lightDir)), 0.05, 1.0);
    float slope = min(sqrt(1.0 - cosTheta * cosTheta) / cosTheta, 8.0);
    float ref = (ls.z + vsmDepthOffset) * vsmDepthScale - texel * (vsmBias + 0.5 * slope) * vsmDepthScale;
    vec2 uv = (t - vec2(pg) * VSM_PAGE_TEXELS) / VSM_PAGE_TEXELS;
    return clamp(1.0 - vsmCompare(uv, float(layer), ref), 0.0, 1.0);
}

// vsmShadow1 is vsmShadow with one tap a level (vsmSampleLevel1).
float vsmShadow1(vec3 worldPos, vec3 normal, vec3 lightDir) {
    for (int l = vsmLevelFor(distance(worldPos, vsmCamPos)); l < VSM_LEVELS; l++) {
        float s = vsmSampleLevel1(worldPos, normal, lightDir, l);
        if (s > VSM_NOT_RESIDENT) {
            return s;
        }
    }
    return 0.0;
}

// ---------- soft shadows: PCSS (docs/lighting/PLAN.md, phase 6) ----------
//
// Percentage-closer soft shadows (Fernando, 2005): the sun is a disc, not a
// point, so a shadow is sharp where its caster touches the ground and widens
// with the distance from it. A blocker search round the point finds how far
// above it, on average, the casters are; the penumbra that distance throws
// at the sun's angular size (sunAngularRadius) is the width the comparison is
// then filtered over. The atlas holds raw depths, so both read it directly.
// softShadowsOn (the scene block, the player's soft_shadows) switches it;
// off, vsmShadow is the 3x3 filter above, as before.

#define VSM_PCSS_SEARCH 8
#define VSM_PCSS_TAPS 12
#define VSM_PCSS_MAX_TEXELS 10.0

// vsmDepthAt is the raw depth stored at window texel tt of level l, or 1e9
// where its page is not resident.
float vsmDepthAt(vec2 tt, int l) {
    ivec2 pg = ivec2(floor(tt / VSM_PAGE_TEXELS));
    int layer = vsmLayer(pg, l);
    if (layer < 0) {
        return 1e9;
    }
    vec2 uv = (tt - vec2(pg) * VSM_PAGE_TEXELS) / VSM_PAGE_TEXELS;
    return textureLod(vsmAtlas, vec3(uv, float(layer)), 0.0).r;
}

// vsmLitAt is vsmCompare at window texel tt of level l, through its page; lit
// where the page is missing.
float vsmLitAt(vec2 tt, int l, float ref) {
    ivec2 pg = ivec2(floor(tt / VSM_PAGE_TEXELS));
    int layer = vsmLayer(pg, l);
    if (layer < 0) {
        return 1.0;
    }
    vec2 uv = (tt - vec2(pg) * VSM_PAGE_TEXELS) / VSM_PAGE_TEXELS;
    return vsmCompare(uv, float(layer), ref);
}

vec2 vsmVogel(int i, int n, float turn) {
    float r = sqrt((float(i) + 0.5) / float(n));
    float a = float(i) * 2.39996323 + turn;
    return vec2(cos(a), sin(a)) * r;
}

// vsmSampleLevelPCSS is vsmSampleLevel with the penumbra the caster's height
// throws; -1 where level l does not hold the point.
float vsmSampleLevelPCSS(vec3 worldPos, vec3 normal, vec3 lightDir, int l) {
    float texel = vsmTexel0 * exp2(float(l));
    vec3 p = worldPos + normal * (texel * vsmNormalOffset);
    vec3 ls = vsmRot * (p - vsmCamPos);
    vec2 t = vsmWindowTexel(ls, l);
    if (vsmLayer(ivec2(floor(t / VSM_PAGE_TEXELS)), l) < 0) {
        return -1.0;
    }
    float cosTheta = clamp(abs(dot(normal, lightDir)), 0.05, 1.0);
    float slope = min(sqrt(1.0 - cosTheta * cosTheta) / cosTheta, 8.0);
    float ref = (ls.z + vsmDepthOffset) * vsmDepthScale - texel * (vsmBias + 0.5 * slope) * vsmDepthScale;
    // A turn of the taps by the point (no gl_FragCoord: the vertex-lit
    // foliage shades through this too), changing with the TAA frame.
    float turn = 6.2831853 * fract(sin(dot(floor(worldPos * 50.0), vec3(12.9898, 78.233, 45.164))) * 43758.5453
        + float(shadowNoiseFrame) * 0.618034);
    float tanA = tan(sunAngularRadius);

    // The blockers: how far toward the sun, on average, the depths nearer
    // than the point are, within the widest penumbra this level filters.
    float search = VSM_PCSS_MAX_TEXELS;
    float sum = 0.0;
    float n = 0.0;
    for (int i = 0; i < VSM_PCSS_SEARCH; i++) {
        float d = vsmDepthAt(t + vsmVogel(i, VSM_PCSS_SEARCH, turn) * search, l);
        if (d < ref) {
            sum += d;
            n += 1.0;
        }
    }
    if (n == 0.0) {
        return 0.0; // nothing above it: lit
    }
    float blocker = (ref - sum / n) / max(vsmDepthScale, 1e-6); // metres above the point
    float radius = clamp(blocker * tanA / texel, 1.0, VSM_PCSS_MAX_TEXELS);
    float lit = 0.0;
    for (int i = 0; i < VSM_PCSS_TAPS; i++) {
        lit += vsmLitAt(t + vsmVogel(i, VSM_PCSS_TAPS, turn + 1.3) * radius, l, ref);
    }
    return clamp(1.0 - lit / float(VSM_PCSS_TAPS), 0.0, 1.0);
}

float vsmShadow(vec3 worldPos, vec3 normal, vec3 lightDir) {
    for (int l = vsmLevelFor(distance(worldPos, vsmCamPos)); l < VSM_LEVELS; l++) {
        // FOLDED BY TEXT (render/nanite_vis_variants.go visFrameSpecs): keep this expression spelled exactly.
        float s = softShadowsOn != 0 ? vsmSampleLevelPCSS(worldPos, normal, lightDir, l)
                                     : vsmSampleLevel(worldPos, normal, lightDir, l);
        if (s > VSM_NOT_RESIDENT) {
            return s;
        }
    }
    return 0.0;
}
