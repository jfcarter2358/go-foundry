// surface_fx.glsl — the terrain and rock material effects (material kinds in
// internal/material/material_kinds_terrain.go, uniforms from
// internal/gfx/material_fx.go, docs/canyon/PHASE_5.md). Included by
// fragment.glsl.
//
// Everything here projects in WORLD space: triplanar texturing, the splat
// terrain layers, the ground blend under a rock, desert-varnish streaks, a
// detail normal map, macro variation and a colour grade. Each is gated by a
// bit of matFx, a uniform, so a surface wearing none of them — every surface
// a material of these effects is not on, which is nearly all of them — runs
// none of this.
//
// TWO PATHS, ONE FILE. fragment.glsl also runs as the Nanite visibility
// buffer's material pass (render/nanite_vis_source.go), a full-screen pass in
// which screen-space derivatives of a reconstructed value are differences
// between unrelated triangles. So this file takes no derivative of its own:
// every sample is textureGrad (or texelFetch) with gradients worked out from
// the world-position derivatives main() already has (dPdx, dPdy — dFdx in the
// forward pass, the analytic nvDPdx in the material pass), which is exact on
// both. It also samples none of the four textures the material pass rewrites
// (tex, normalMap, blendTex, blendMask) through texture(): textureGrad on
// them is left alone by the rewrite, and is what both paths want. And no
// `in` declarations: the pass turns fragment.glsl's inputs into globals by
// pattern, and this file's values arrive as arguments.
//
// textureGrad needs no uniform control flow, which is what lets the splat
// skip a layer whose weight is nil and triplanar skip an axis that is
// facing away, per pixel.

uniform int matFx;
#define FX_TRIPLANAR 1
#define FX_SPLAT     2
#define FX_GROUND    4
#define FX_STREAKS   8
#define FX_DETAIL    16
#define FX_MACRO     32
#define FX_GRADE     64

// triplanar: 1/scale, sharpness, upright (0/1).
uniform vec3 fxTriplanar;

// splat. The four layers' albedo (rgb) and height (a) are the tiles of one
// 2×2 atlas, their normals (rgb) and AO (a) of another — two samplers, not
// eight (gfx.SplatArrays), which is what keeps the material pass inside 32.
// Atlases rather than 2D arrays so that every sampler here is a sampler2D
// and may rest on the albedo's unit while no effect draws (gfx/material_fx.go).
uniform sampler2D fxSplatWeights;
uniform sampler2D fxSplatAlbedo;
uniform sampler2D fxSplatNormal;
uniform vec4 fxSplatRect;     // min x, min z, 1/size x, 1/size z
uniform vec4 fxSplatInvScale; // 1/metres a repeat, per layer
uniform vec4 fxSplatTri;      // 1: triplanar, per layer
uniform vec4 fxSplatHeight;   // height influence, per layer
uniform vec4 fxSplatLayer;    // the atlases' tile, per layer (x = n%2, y = n/2); -1 none
uniform vec2 fxSplatAtlas;    // a tile's texels as a fraction of the atlas, its gutter's
uniform vec3 fxSplatTint[4];
uniform vec3 fxSplatParams;   // blend, normal strength, ao amount
uniform sampler2D fxStrata;
uniform vec4 fxStrataParams;  // lo, 1/(hi - lo), strength (0: no ramp), warp
uniform vec4 fxSplatBedding;  // bedding relief (0: none), 1/bed spacing, side projections' normal strength, unused

// ground_blend.
uniform sampler2D fxGroundColor;
uniform sampler2D fxGroundHeight; // 16 bits: red high byte, green low
uniform vec4 fxGroundRect;        // min x, min z, 1/size x, 1/size z
uniform vec2 fxGroundRange;       // height at 0, height span
uniform vec4 fxGroundParams;      // band, breakup, strength, contact

// streaks: strength, 1/width, 1/length, overhang; and the varnish colour.
uniform vec4 fxStreaks;
uniform vec3 fxStreakColor;

// detail_normal: 1/scale, strength, 1/distance, BC5 (1: rebuild z).
uniform sampler2D fxDetail;
uniform vec4 fxDetailParams;

// macro_variation: 1/scale, strength, warmth.
uniform vec3 fxMacro;

// color_grade: the match ratio (target over source, each with its
// brightness divided out — gfx works it out) and the amount; cos and sin of
// the hue turn, the saturation and the brightness; the tint, brightness
// divided out.
uniform vec4 fxGradeMatch;
uniform vec4 fxGradeHS;
uniform vec3 fxGradeTint;

// ---------- noise (value noise; cheap, smooth, no textures) ----------

float fxHash(vec2 p) {
    p = fract(p * vec2(123.34, 456.21));
    p += dot(p, p + 45.32);
    return fract(p.x * p.y);
}

float fxNoise(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    vec2 u = f * f * (3.0 - 2.0 * f);
    float a = fxHash(i);
    float b = fxHash(i + vec2(1.0, 0.0));
    float c = fxHash(i + vec2(0.0, 1.0));
    float d = fxHash(i + vec2(1.0, 1.0));
    return mix(mix(a, b, u.x), mix(c, d, u.x), u.y);
}

float fxFbm(vec2 p) {
    float s = 0.0, a = 0.5;
    for (int i = 0; i < 3; i++) {
        s += a * fxNoise(p);
        p = p * 2.03 + vec2(17.1, 3.7);
        a *= 0.5;
    }
    return s / 0.875;
}

// ---------- projections ----------

// fxTriWeights is how much each axis's projection counts on a surface facing
// n: the normal's components raised to the sharpness, summing to one.
vec3 fxTriWeights(vec3 n, float sharp) {
    vec3 w = pow(abs(n), vec3(sharp));
    return w / max(w.x + w.y + w.z, 1e-5);
}

// fxProj is axis a's projection of world position p (0: along X, onto Z-Y;
// 1: straight down, onto X-Z; 2: along Z, onto X-Y) facing n: its
// coordinates, and the world directions u and v run in, which ARE the
// tangent and bitangent a normal map wants (cotangentFrame's dP/du and
// dP/dv). The side projections read left to right as a viewer facing the
// surface sees it, and upright (v down the world's height) when asked.
vec2 fxProj(int a, vec3 p, vec3 n, bool upright, out vec3 T, out vec3 B) {
    if (a == 1) {
        T = vec3(1.0, 0.0, 0.0);
        B = vec3(0.0, 0.0, 1.0);
        return p.xz;
    }
    float vs = upright ? -1.0 : 1.0;
    B = vec3(0.0, vs, 0.0);
    if (a == 0) {
        float s = n.x >= 0.0 ? -1.0 : 1.0;
        T = vec3(0.0, 0.0, s);
        return vec2(s * p.z, vs * p.y);
    }
    float s = n.z >= 0.0 ? 1.0 : -1.0;
    T = vec3(s, 0.0, 0.0);
    return vec2(s * p.x, vs * p.y);
}

// fxTangentToWorld bends geometric normal N by tangent-space normal tn in
// the frame (T, B) — T and B made perpendicular to N first, so a projection
// that is not quite the surface's plane still gives a unit normal on it.
vec3 fxTangentToWorld(vec3 tn, vec3 T, vec3 B, vec3 N, float strength) {
    vec3 t = normalize(T - N * dot(N, T));
    vec3 b = normalize(B - N * dot(N, B) - t * dot(t, B));
    tn.xy *= strength;
    return normalize(t * tn.x + b * tn.y + N * max(tn.z, 1e-3));
}

vec3 fxDecodeNormal(vec4 s, bool bc5) {
    vec3 tn = s.xyz * 2.0 - 1.0;
    if (bc5) {
        tn.z = sqrt(max(1.0 - dot(tn.xy, tn.xy), 0.0));
    }
    return tn;
}

// ---------- triplanar: the surface's own texture, world-projected ----------

// fxTriAxis adds axis a's share (weight wa) of fxTriplanarSurface's
// projection to its sums. One call per axis with a literal a: no loop the
// compiler might keep rolled (see "NO RUNTIME INDEXING" below).
void fxTriAxis(int a, float wa, sampler2D albedoTex, sampler2D normalTex, int hasNormal, vec3 p, vec3 N,
               vec3 dPdx, vec3 dPdy, float inv, bool upright, inout vec4 col, inout vec3 nrm, inout float sum) {
    if (wa < 0.01) {
        return;
    }
    sum += wa;
    vec3 T, B;
    vec2 uv = fxProj(a, p, N, upright, T, B) * inv;
    vec2 gx = vec2(dot(dPdx, T), dot(dPdx, B)) * inv;
    vec2 gy = vec2(dot(dPdy, T), dot(dPdy, B)) * inv;
    col += wa * textureGrad(albedoTex, uv, gx, gy);
    if (hasNormal != 0) {
        vec3 tn = fxDecodeNormal(textureGrad(normalTex, uv, gx, gy), hasNormal == 2);
        nrm += wa * fxTangentToWorld(tn, T, B, N, 1.0);
    }
}

// fxTriplanarSurface replaces albedo with tex projected along the three axes
// and, when the surface has a normal map (hasNormal: 1 RGB, 2 BC5), bends N
// by it the same way. Returns the bent normal (N when there is no map).
vec3 fxTriplanarSurface(sampler2D albedoTex, sampler2D normalTex, int hasNormal, vec3 p, vec3 N,
                        vec3 dPdx, vec3 dPdy, inout vec4 albedo) {
    float inv = fxTriplanar.x;
    vec3 w = fxTriWeights(N, fxTriplanar.y);
    bool upright = fxTriplanar.z > 0.5;
    vec4 col = vec4(0.0);
    vec3 nrm = vec3(0.0);
    float sum = 0.0;
    fxTriAxis(0, w.x, albedoTex, normalTex, hasNormal, p, N, dPdx, dPdy, inv, upright, col, nrm, sum);
    fxTriAxis(1, w.y, albedoTex, normalTex, hasNormal, p, N, dPdx, dPdy, inv, upright, col, nrm, sum);
    fxTriAxis(2, w.z, albedoTex, normalTex, hasNormal, p, N, dPdx, dPdy, inv, upright, col, nrm, sum);
    albedo = col / max(sum, 1e-4);
    return hasNormal != 0 ? normalize(nrm) : N;
}

// ---------- splat ----------

// fxAtlas samples tile `tile` of a splat atlas at repeating coordinate uv:
// the repeat folded into the tile (fract), offset past its gutter, and the
// gradients scaled with it — taken from the continuous uv, so the fold makes
// no seam in the mip choice.
vec4 fxAtlas(sampler2D s, float tile, vec2 uv, vec2 gx, vec2 gy) {
    vec2 t = vec2(mod(tile, 2.0), floor(tile * 0.5)) * 0.5;
    vec2 a = t + fxSplatAtlas.y + fract(uv) * fxSplatAtlas.x;
    return textureGrad(s, a, gx * fxSplatAtlas.x, gy * fxSplatAtlas.x);
}

// fxBedLip is the lip of a bed at period position u: a bump about each bed
// boundary (u = 0, wrapping) of unit mean over the period, so that 1 - lip
// is the slope of a recess that climbs steadily up the bed and snaps back
// under the next bed's lip, and the relief closes on itself every bed.
float fxBedLip(float u) {
    const float sigma = 0.07;
    float d = min(u, 1.0 - u) / sigma;
    return exp(-d * d) / (sigma * 1.7725); // sigma * sqrt(pi): the bump's integral
}

// fxBedding bends a steep face's normal (na.xyz) by the BEDDING RELIEF of
// layered rock, and darkens its AO (na.w) under each lip.
//
// A smooth heightfield wall, a few samples stretched over tens of metres of
// height, has nothing in its geometry at the scale a wall is read at: the
// metre-scale beds of a sandstone cliff, each weathered back under the lip
// of the harder one above. Here the face is displaced in by D(y) — a
// recess that deepens steadily up each bed (its face tilts a little toward
// the sky and catches the sun) and snaps back at the bed's top (a short
// overhang that faces down and goes dark) — but only in the normal: the
// face's slope dD/dy turns it about the horizontal, n' = n + up·dD/dy.
//
//   - Beds are fxSplatBedding.y⁻¹ metres thick on average, their heights
//     warped by slow noise across the face so they wander, and each bed's
//     recess scaled by its own hash so some stand proud and some are
//     flush; laminations inside them at about a third of the spacing add a
//     finer, fainter set.
//   - The relief is faded out where a pixel spans a good part of a bed
//     (the footprint, from dPdx/dPdy's height): analytic antialiasing, so
//     a distant wall turns smoothly to its texture instead of shimmering.
//   - Only steep faces take it, faded from slopes of about 30° from
//     vertical; ground and benches are untouched.
//
// No derivatives and no samplers: everything is a function of the world
// position and the projected gradients, so the visibility buffer's material
// pass takes it as it is.
void fxBedding(vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, inout vec4 na) {
    float steep = 1.0 - smoothstep(0.35, 0.75, abs(N.y));
    if (steep <= 0.0) {
        return;
    }
    float invS = fxSplatBedding.y;
    float foot = (abs(dPdx.y) + abs(dPdy.y)) * invS;
    float fade = 1.0 - smoothstep(0.12, 0.45, foot);
    if (fade <= 0.0) {
        return;
    }
    // The beds' heights wander across the face (and along it: a bed is not
    // one height round a whole mesa).
    float warp = (fxNoise(p.xz * 0.045) - 0.5) * 1.8 + (fxNoise(p.xz * 0.21 + 3.1) - 0.5) * 0.45;
    // Beds of unequal thickness: the height coordinate is bent by a slow
    // noise of itself before it is cut into beds (its slope stays positive,
    // so the beds keep their order), which spaces the lips irregularly
    // instead of like the lines of a ruled page.
    float yr = p.y * invS;
    yr += 0.85 * (fxNoise(vec2(yr * 0.37, 2.9)) - 0.5) + 0.3 * (fxNoise(vec2(yr * 0.83, 5.3)) - 0.5);
    float yb = yr + warp;
    float bed = floor(yb);
    float u = yb - bed;
    // Each bed its own depth of recess — a third of them flush, with no lip
    // to speak of — and the whole set stronger in patches along the face.
    float hb = fxHash(vec2(bed, 7.31));
    float own = hb < 0.33 ? 0.12 : 0.35 + 1.1 * (hb - 0.33);
    float spread = 0.55 + 0.9 * fxNoise(p.xz * 0.08 + vec2(bed * 0.37, 1.7));
    float amt = fxSplatBedding.x * own * spread;
    float lip = fxBedLip(u);
    float slope = amt * (1.0 - lip);
    // Laminations: a third of the spacing, a third of the depth, faded on
    // their own footprint.
    float fadeL = 1.0 - smoothstep(0.12, 0.45, foot * 3.3);
    float yl = yb * 3.3 + fxNoise(p.xz * 0.6) * 0.8;
    float lipL = fxBedLip(fract(yl));
    slope += fadeL * 0.15 * fxSplatBedding.x * spread * (1.0 - lipL);
    float k = steep * fade;
    na.xyz = normalize(na.xyz + vec3(0.0, slope * k, 0.0));
    // The overhang's shadow, which reads even with the sun behind the wall.
    float under = smoothstep(1.5, 8.0, lip) * min(amt, 1.0);
    na.w *= 1.0 - 0.45 * under * k;
}

// NO RUNTIME INDEXING below (docs/canyon/PERF.md). The splat used to loop
// over its four layers and three projections with `continue`s, indexing
// vec4 uniforms and local arrays by the loop counter; NVIDIA's compiler kept
// those loops rolled, put the arrays in local memory and sized the whole
// program's registers for it, which made the visibility buffer's material
// pass three times dearer on the canyon (GTX 1070: 12.8 → 7.9 ms at
// 2560x1080). Every layer and axis is now its own call with constant
// arguments — a uniform's component by name, an axis by literal — and every
// layer's result its own variable, so nothing is indexed at run time. The
// arithmetic, and so the picture, is unchanged.

// fxSplatAxis adds triplanar axis a's share (weight wa) of layer tile
// `layer` at repeat 1/inv to the sums: albedo+height, world normal, AO and
// weight.
void fxSplatAxis(int a, float wa, float layer, float inv, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy,
                 inout vec4 ah, inout vec3 nrm, inout float ao, inout float sum) {
    if (wa < 0.02) {
        return;
    }
    vec3 T, B;
    vec2 uv = fxProj(a, p, N, true, T, B) * inv;
    vec2 gx = vec2(dot(dPdx, T), dot(dPdx, B)) * inv;
    vec2 gy = vec2(dot(dPdy, T), dot(dPdy, B)) * inv;
    ah += wa * fxAtlas(fxSplatAlbedo, layer, uv, gx, gy);
    vec4 s = fxAtlas(fxSplatNormal, layer, uv, gx, gy);
    float ns = a == 1 ? fxSplatParams.y : fxSplatParams.y * fxSplatBedding.z;
    nrm += wa * fxTangentToWorld(s.xyz * 2.0 - 1.0, T, B, N, ns);
    ao += wa * s.w;
    sum += wa;
}

// fxSplatSample is a splat layer's albedo+height and normal+AO at p — its
// atlas tile `layer`, 1/repeat inv, top down or triplanar (tri) — with its
// normal already in world space (xyz of the second result; w the AO).
void fxSplatSample(float layer, float inv, bool tri, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, out vec4 ah, out vec4 na) {
    if (!tri) {
        vec2 uv = p.xz * inv;
        vec2 gx = dPdx.xz * inv;
        vec2 gy = dPdy.xz * inv;
        ah = fxAtlas(fxSplatAlbedo, layer, uv, gx, gy);
        vec4 s = fxAtlas(fxSplatNormal, layer, uv, gx, gy);
        na = vec4(fxTangentToWorld(s.xyz * 2.0 - 1.0, vec3(1.0, 0.0, 0.0), vec3(0.0, 0.0, 1.0), N, fxSplatParams.y), s.w);
        return;
    }
    vec3 w = fxTriWeights(N, 6.0);
    ah = vec4(0.0);
    vec3 nrm = vec3(0.0);
    float ao = 0.0, sum = 0.0;
    fxSplatAxis(0, w.x, layer, inv, p, N, dPdx, dPdy, ah, nrm, ao, sum);
    fxSplatAxis(1, w.y, layer, inv, p, N, dPdx, dPdy, ah, nrm, ao, sum);
    fxSplatAxis(2, w.z, layer, inv, p, N, dPdx, dPdy, ah, nrm, ao, sum);
    sum = max(sum, 1e-4);
    ah /= sum;
    na = vec4(normalize(nrm), ao / sum);
    if (fxSplatBedding.x > 0.0) {
        fxBedding(p, N, dPdx, dPdy, na);
    }
    // The bedding: a tint by world height, warped so the bands wander.
    if (fxStrataParams.z > 0.0) {
        float y = p.y + (fxNoise(p.xz * 0.05) - 0.5) * 2.0 * fxStrataParams.w;
        float t = (y - fxStrataParams.x) * fxStrataParams.y;
        float g = dot(dPdx, vec3(0.0, 1.0, 0.0)) * fxStrataParams.y;
        float h = dot(dPdy, vec3(0.0, 1.0, 0.0)) * fxStrataParams.y;
        vec3 band = textureGrad(fxStrata, vec2(t, 0.5), vec2(g, 0.0), vec2(h, 0.0)).rgb * 2.0;
        ah.rgb *= mix(vec3(1.0), band, fxStrataParams.z);
    }
}

// fxSplatLayerOn is whether a layer (its tile and weight) takes part at all.
bool fxSplatLayerOn(float layer, float wt) {
    return layer >= 0.0 && wt >= 0.004;
}

// fxSplatFloor is a lower bound on a taking-part layer's score: its weight,
// less any negative height lift.
float fxSplatFloor(float layer, float wt, float hgt) {
    return fxSplatLayerOn(layer, wt) ? wt + min(hgt, 0.0) * 0.35 * min(wt * 4.0, 1.0) : -1e9;
}

// fxSplatLayerScore samples one layer and scores it (-1e9: not sampled). A layer
// whose score cannot come within the blend of the best one adds nothing in
// fxSplatAccum (its share is 0), so it is not sampled at all: its score is
// at most its weight plus its whole height lift (the height is in [0, 1]),
// and the best is at least floorBest. Exact — the picture is the same as
// sampling every layer, at a fraction of the fetches where one dominates.
void fxSplatLayerScore(float layer, float inv, float tri, float wt, float hgt, float floorBest, float blend,
                       vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, out vec4 ah, out vec4 na, out float score) {
    score = -1e9;
    ah = vec4(0.0);
    na = vec4(N, 1.0);
    if (!fxSplatLayerOn(layer, wt)) {
        return;
    }
    if (wt + max(hgt, 0.0) * 0.35 * min(wt * 4.0, 1.0) <= floorBest - blend) {
        return;
    }
    fxSplatSample(layer, inv, tri >= 0.5, p, N, dPdx, dPdy, ah, na);
    // A layer's height lifts it over its neighbours, fully once it has a
    // quarter of the weight: where stones and sand meet, the stones' tops
    // win and the sand fills between them.
    score = wt + ah.a * hgt * 0.35 * min(wt * 4.0, 1.0);
}

// fxSplatAccum adds a scored layer's share — how far it is within the blend
// of the best — to the blend's sums.
void fxSplatAccum(float score, float best, float blend, vec4 ah, vec4 na, vec3 tint,
                  inout vec4 col, inout vec3 nrm, inout float occ, inout float sum) {
    float b = max(score - (best - blend), 0.0);
    if (b <= 0.0) {
        return;
    }
    col += b * vec4(ah.rgb * tint, 1.0);
    nrm += b * na.xyz;
    occ += b * na.w;
    sum += b;
}

// fxSplatSurface is the splat: the layers' albedo, normal and AO at p,
// height-blended by the weights over the terrain's rectangle.
void fxSplatSurface(vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, inout vec4 albedo, inout vec3 normal, inout float ao) {
    vec2 wuv = (p.xz - fxSplatRect.xy) * fxSplatRect.zw;
    vec4 wt = textureGrad(fxSplatWeights, wuv, dPdx.xz * fxSplatRect.zw, dPdy.xz * fxSplatRect.zw);
    float blend = fxSplatParams.x;
    vec4 L = fxSplatLayer, S = fxSplatInvScale, T = fxSplatTri, H = fxSplatHeight;
    float floorBest = max(max(fxSplatFloor(L.x, wt.x, H.x), fxSplatFloor(L.y, wt.y, H.y)),
                          max(fxSplatFloor(L.z, wt.z, H.z), fxSplatFloor(L.w, wt.w, H.w)));
    vec4 ah0, ah1, ah2, ah3, na0, na1, na2, na3;
    float s0, s1, s2, s3;
    fxSplatLayerScore(L.x, S.x, T.x, wt.x, H.x, floorBest, blend, p, N, dPdx, dPdy, ah0, na0, s0);
    fxSplatLayerScore(L.y, S.y, T.y, wt.y, H.y, floorBest, blend, p, N, dPdx, dPdy, ah1, na1, s1);
    fxSplatLayerScore(L.z, S.z, T.z, wt.z, H.z, floorBest, blend, p, N, dPdx, dPdy, ah2, na2, s2);
    fxSplatLayerScore(L.w, S.w, T.w, wt.w, H.w, floorBest, blend, p, N, dPdx, dPdy, ah3, na3, s3);
    float best = max(max(s0, s1), max(s2, s3));
    if (best < -1e8) {
        return; // no layer here: the surface's own texture stands
    }
    vec4 col = vec4(0.0);
    vec3 nrm = vec3(0.0);
    float occ = 0.0, sum = 0.0;
    fxSplatAccum(s0, best, blend, ah0, na0, fxSplatTint[0], col, nrm, occ, sum);
    fxSplatAccum(s1, best, blend, ah1, na1, fxSplatTint[1], col, nrm, occ, sum);
    fxSplatAccum(s2, best, blend, ah2, na2, fxSplatTint[2], col, nrm, occ, sum);
    fxSplatAccum(s3, best, blend, ah3, na3, fxSplatTint[3], col, nrm, occ, sum);
    albedo = vec4(col.rgb / sum, albedo.a);
    normal = normalize(nrm);
    ao *= mix(1.0, occ / sum, fxSplatParams.z);
}

// ---------- ground blend ----------

float fxGroundHeightAt(vec2 xz) {
    ivec2 size = textureSize(fxGroundHeight, 0);
    vec2 t = (xz - fxGroundRect.xy) * fxGroundRect.zw * vec2(size) - 0.5;
    ivec2 i = ivec2(floor(t));
    vec2 f = t - vec2(i);
    ivec2 hi = size - 1;
    float h[4];
    for (int k = 0; k < 4; k++) {
        ivec2 q = clamp(i + ivec2(k & 1, k >> 1), ivec2(0), hi);
        vec2 rg = texelFetch(fxGroundHeight, q, 0).rg;
        h[k] = (rg.r * 65280.0 + rg.g * 255.0) / 65535.0;
    }
    float v = mix(mix(h[0], h[1], f.x), mix(h[2], h[3], f.x), f.y);
    return fxGroundRange.x + v * fxGroundRange.y;
}

// fxGroundBlend fades albedo toward the ground's colour near the ground.
void fxGroundBlend(vec3 p, vec3 dPdx, vec3 dPdy, inout vec4 albedo) {
    float band = fxGroundParams.x;
    float d = max(p.y - fxGroundHeightAt(p.xz), 0.0);
    if (d > band * (1.0 + fxGroundParams.y)) {
        return;
    }
    // A ragged top: the band's height wanders with noise.
    float n = fxFbm(p.xz * 2.3 + p.y * 0.7);
    float top = band * max(1.0 + fxGroundParams.y * (n - 0.5) * 1.6, 0.05);
    float t = 1.0 - smoothstep(0.0, top, d);
    vec2 uv = (p.xz - fxGroundRect.xy) * fxGroundRect.zw;
    vec3 g = textureGrad(fxGroundColor, uv, dPdx.xz * fxGroundRect.zw, dPdy.xz * fxGroundRect.zw).rgb;
    albedo.rgb = mix(albedo.rgb, g, t * t * fxGroundParams.z);
    // The contact shadow: the last few centimetres.
    albedo.rgb *= 1.0 - fxGroundParams.w * (1.0 - smoothstep(0.0, max(band * 0.2, 0.03), d));
}

// ---------- streaks, macro variation, grade, detail ----------

void fxStreaksApply(vec3 p, vec3 N, inout vec4 albedo) {
    float steep = 1.0 - smoothstep(0.35, 0.75, abs(N.y));
    float over = smoothstep(0.0, -0.6, N.y);
    if (steep > 0.0) {
        // Across the face: X on a face looking along Z, Z on one along X.
        float wx = abs(N.x) / max(abs(N.x) + abs(N.z), 1e-4);
        vec2 s = vec2(fxStreaks.y, fxStreaks.z);
        float a = fxFbm(vec2(p.x * s.x, p.y * s.y + fxNoise(vec2(p.x * s.x * 0.3, 0.0)) * 2.0));
        float b = fxFbm(vec2(p.z * s.x + 41.0, p.y * s.y + fxNoise(vec2(p.z * s.x * 0.3, 7.0)) * 2.0));
        float streak = smoothstep(0.48, 0.78, mix(a, b, wx));
        albedo.rgb *= mix(vec3(1.0), fxStreakColor, fxStreaks.x * streak * steep);
    }
    albedo.rgb *= 1.0 - fxStreaks.w * 0.6 * over;
}

void fxMacroApply(vec3 p, inout vec4 albedo) {
    float v = fxFbm(p.xz * fxMacro.x) * 2.0 - 1.0;
    float w = fxFbm(p.xz * fxMacro.x * 0.7 + vec2(31.0, 17.0)) * 2.0 - 1.0;
    albedo.rgb *= 1.0 + v * fxMacro.y;
    albedo.rgb *= vec3(1.0 + w * fxMacro.z * 0.12, 1.0, 1.0 - w * fxMacro.z * 0.12);
}

void fxGradeApply(inout vec4 albedo) {
    vec3 c = albedo.rgb;
    c *= mix(vec3(1.0), fxGradeMatch.rgb, fxGradeMatch.a);
    // Hue and saturation in YIQ: luma held, chroma turned and scaled.
    float y = dot(c, vec3(0.299, 0.587, 0.114));
    float i = dot(c, vec3(0.596, -0.274, -0.322));
    float q = dot(c, vec3(0.211, -0.523, 0.312));
    vec2 iq = vec2(i * fxGradeHS.x - q * fxGradeHS.y, i * fxGradeHS.y + q * fxGradeHS.x) * fxGradeHS.z;
    c = vec3(y + 0.956 * iq.x + 0.621 * iq.y, y - 0.272 * iq.x - 0.647 * iq.y, y - 1.106 * iq.x + 1.703 * iq.y);
    c *= fxGradeTint;
    albedo.rgb = max(c * fxGradeHS.w, vec3(0.0));
}

// fxDetailAxis adds axis a's share of the detail normal to d (one call per
// axis, a literal: see "NO RUNTIME INDEXING").
void fxDetailAxis(int a, float wa, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, float inv, inout vec3 d) {
    if (wa < 0.02) {
        return;
    }
    vec3 T, B;
    vec2 uv = fxProj(a, p, N, true, T, B) * inv;
    vec2 gx = vec2(dot(dPdx, T), dot(dPdx, B)) * inv;
    vec2 gy = vec2(dot(dPdy, T), dot(dPdy, B)) * inv;
    vec3 tn = fxDecodeNormal(textureGrad(fxDetail, uv, gx, gy), fxDetailParams.w > 0.5);
    d += wa * fxTangentToWorld(tn, T, B, N, 1.0);
}

vec3 fxDetailApply(vec3 p, vec3 N, vec3 n, vec3 dPdx, vec3 dPdy, float viewDepth) {
    float fade = 1.0 - smoothstep(0.5, 1.0, viewDepth * fxDetailParams.z);
    if (fade <= 0.0) {
        return n;
    }
    float inv = fxDetailParams.x;
    vec3 w = fxTriWeights(N, 4.0);
    vec3 d = vec3(0.0);
    fxDetailAxis(0, w.x, p, N, dPdx, dPdy, inv, d);
    fxDetailAxis(1, w.y, p, N, dPdx, dPdy, inv, d);
    fxDetailAxis(2, w.z, p, N, dPdx, dPdy, inv, d);
    return normalize(n + (normalize(d) - N) * fxDetailParams.y * fade);
}
