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
#define VSM_LEVELS 11
#define VSM_PAGES 32
#define VSM_PAGE_TEXELS 128.0

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
    // The filter's bilinear taps reach 1.5 texels each way.
    ivec2 lo = ivec2(floor((t - 1.5) / VSM_PAGE_TEXELS));
    ivec2 hi = ivec2(floor((t + 1.5) / VSM_PAGE_TEXELS));
    for (int y = lo.y; y <= hi.y; y++) {
        for (int x = lo.x; x <= hi.x; x++) {
            if (vsmLayer(ivec2(x, y), l) < 0) {
                return -1.0;
            }
        }
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
            lit += vsmCompare(uv, float(vsmLayer(pg, l)), ref);
        }
    }
    return 1.0 - lit / 9.0;
}

// vsmShadow is the occlusion at worldPos (geometric normal, the light's
// direction of travel): its level's pages, or the nearest coarser level's
// that are resident, or lit.
float vsmShadow(vec3 worldPos, vec3 normal, vec3 lightDir) {
    for (int l = vsmLevelFor(distance(worldPos, vsmCamPos)); l < VSM_LEVELS; l++) {
        float s = vsmSampleLevel(worldPos, normal, lightDir, l);
        if (s >= 0.0) {
            return s;
        }
    }
    return 0.0;
}
