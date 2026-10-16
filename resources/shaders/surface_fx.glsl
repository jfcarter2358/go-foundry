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
#define FX_SPLAT8    128 // the splat's eight-layer mode, beside FX_SPLAT
#define FX_TRAIL     256 // the splat's soft layers read the ground trails, beside FX_SPLAT

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
uniform vec4 fxSplatLayer;    // the atlases' tile, per layer (x = n%cols, y = n/cols); -1 none
uniform vec4 fxSplatAtlas;    // a tile's texels as a fraction of the atlas (u, v), its gutter's (u, v)
uniform float fxSplatCols;    // the atlases' tiles across: 2 (the 2×2), or 4 (the eight-layer 4×2), 8 (a palette)
uniform float fxSplatRows;    // the atlases' tiles down: 2, or a palette's rows (0, unset, reads as 2)
uniform vec3 fxSplatTint[8];  // per layer; 4..7 only in eight-layer mode
uniform vec3 fxSplatParams;   // blend, normal strength, ao amount
uniform sampler2D fxStrata;
uniform vec4 fxStrataParams;  // lo, 1/(hi - lo), strength (0: no ramp), warp
uniform vec4 fxSplatBedding;  // bedding relief (0: none), 1/bed spacing, side projections' normal strength, unused
uniform vec4 fxSplatRipple;   // sand ripples: strength (0: none), 1/wavelength, wind bearing (radians), layer 0–3
uniform vec4 fxSplatAnti;     // 1: anti-tiled (fxSplatTap), per layer
uniform vec4 fxSplatRelief;   // parallax depth (m) of the top-down layers (fxSplatRead), per layer; 0 none
uniform vec4 fxSplatStrataOn; // 1: the strata ramp and bedding band this layer (when triplanar), per layer

// The splat's EIGHT-LAYER MODE (FX_SPLAT8; docs/regions/PHASE_3.md): a
// second weights map for layers 5–8 and their per-layer values, the same
// meanings as the first four's. The layers share the two atlases (4×2
// then), so this is one sampler more, not eight. Read only behind
// FX_SPLAT8: a four-layer splat never touches any of it.
uniform sampler2D fxSplatWeights2;
uniform vec4 fxSplatInvScale2;
uniform vec4 fxSplatTri2;
uniform vec4 fxSplatHeight2;
uniform vec4 fxSplatLayer2;
uniform vec4 fxSplatAnti2;
uniform vec4 fxSplatRelief2;
uniform vec4 fxSplatStrataOn2;

// The GROUND TRAILS (FX_TRAIL; render/ground_trails.go, docs/trails/SPEC.md):
// footprints pressed into the splat's soft layers. The trail map is a
// square of ground around the player, wrapped (world X/Z over its span is
// its uv; REPEAT does the wrap), each texel how far the ground is pressed
// as a share of the soft layers' depth — red the share that fills back in
// soon, green the share that lasts — positive a dent, negative a rim.
// Per layer: its footprint depth in metres (0: firm) and its fill (how
// much of the red channel it shows rather than the green).
uniform sampler2D fxTrail;
uniform vec4 fxTrailRect;  // window's first corner x, z; 1/span; span (m)
uniform vec4 fxSplatSoft;  // footprint depth (m), per layer; 0 firm
uniform vec4 fxSplatSoft2; // layers 5–8 (eight-layer mode)
uniform vec4 fxSplatFill;  // fill share, per layer
uniform vec4 fxSplatFill2;

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

// macro_variation: 1/scale, strength, warmth, how much it varies with height.
uniform vec4 fxMacro;

// color_grade: the match ratio (target over source, each with its
// brightness divided out — gfx works it out) and the amount; cos and sin of
// the hue turn, the saturation and the brightness; the tint, brightness
// divided out.
uniform vec4 fxGradeMatch;
uniform vec4 fxGradeHS;
uniform vec3 fxGradeTint;

// ---------- noise (value noise; cheap, smooth, no textures) ----------

// fxHash amplifies its input ~2000×, so a corner must be hashed from
// EXACTLY the same bits in both cells that share it. Without `precise` a
// driver may fold (i + 1)·123.34 into i·123.34 + 123.34 (an FMA) in one
// cell and not the other, and the value noise below jumps at cell edges:
// the macro variation drew sharp-edged rectangles tens of metres across on
// the prairie (docs/regions/PRESETS_2B.md). `precise` forbids the
// reassociation, so the noise is as continuous as written.
float fxHash(vec2 p) {
    precise vec2 q = fract(p * vec2(123.34, 456.21));
    precise float d = dot(q, q + 45.32);
    precise vec2 r = q + d;
    precise float h = fract(r.x * r.y);
    return h;
}

float fxNoise(vec2 p) {
    precise vec2 i = floor(p);
    vec2 f = fract(p);
    vec2 u = f * f * (3.0 - 2.0 * f);
    precise vec2 i10 = i + vec2(1.0, 0.0);
    precise vec2 i01 = i + vec2(0.0, 1.0);
    precise vec2 i11 = i + vec2(1.0, 1.0);
    float a = fxHash(i);
    float b = fxHash(i10);
    float c = fxHash(i01);
    float d = fxHash(i11);
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
// no seam in the mip choice. The atlas is fxSplatCols tiles across and
// fxSplatRows down (two, but for a palette of more than eight layers: 8
// across, up to 4 down): for the 2×2 every factor below is the old one
// exactly (1/cols and 1/rows are 0.5, and u's fractions equal v's), so a
// four-layer splat reads the same texels it always did.
vec4 fxAtlas(sampler2D s, float tile, vec2 uv, vec2 gx, vec2 gy) {
    float cols = fxSplatCols;
    float rows = fxSplatRows > 0.0 ? fxSplatRows : 2.0;
    vec2 t = vec2(mod(tile, cols), floor(tile / cols)) * vec2(1.0 / cols, 1.0 / rows);
    vec2 a = t + fxSplatAtlas.zw + fract(uv) * fxSplatAtlas.xy;
    return textureGrad(s, a, gx * fxSplatAtlas.xy, gy * fxSplatAtlas.xy);
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

// RIPPLES (fxSplatRipple). Wind-blown sand is never a flat plane: it lies
// in ripples a hand's breadth apart, crests across the wind, a long gentle
// windward face and a short steep lee, and their sinuous, forking crests
// are what make a sand floor read as sand in shade and in raking light.
// Here they are a height h(u) over a phase u along the wind — warped by two
// noises so the crests wander and fork — bent into the normal only: the
// gradient is h'(u) times the phase's gradient, the latter taken by finite
// differences (so the warp turns the crests and their normals together).
// Patchy in strength, on level ground only, faded where a pixel covers a
// good part of a ripple (analytic antialiasing, as the bedding does), and
// a touch of AO in the troughs so they read under the sky light alone.
float fxRipplePhase(vec2 q, vec2 dir, float k) {
    float w = (fxNoise(q * 0.9) - 0.5) * 3.2 + (fxNoise(q * 3.3 + vec2(7.7, 1.3)) - 0.5) * 0.9;
    return dot(q, dir) * k + w;
}

void fxRipple(vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, float share, inout vec3 normal, inout float ao) {
    float k = fxSplatRipple.y;
    float level = smoothstep(0.86, 0.97, N.y);
    float foot = (length(dPdx.xz) + length(dPdy.xz)) * k;
    float fade = 1.0 - smoothstep(0.22, 0.55, foot);
    float spots = smoothstep(0.25, 0.75, fxNoise(p.xz * 0.11 + vec2(3.7, 9.1)));
    float amt = fxSplatRipple.x * share * level * fade * (0.3 + 0.7 * spots);
    if (amt <= 0.001) {
        return;
    }
    float a = fxSplatRipple.z;
    vec2 dir = vec2(sin(a), cos(a));
    vec2 q = p.xz;
    float ph = fxRipplePhase(q, dir, k);
    const float e = 0.02;
    vec2 g = vec2(fxRipplePhase(q + vec2(e, 0.0), dir, k) - ph, fxRipplePhase(q + vec2(0.0, e), dir, k) - ph) / (e * k);
    float t = 6.2831853 * fract(ph);
    // h = sin t + 0.35 sin 2t: a steeper lee than stoss; h' over its peak.
    float dh = (cos(t) + 0.7 * cos(2.0 * t)) / 1.7;
    float h = (sin(t) + 0.35 * sin(2.0 * t)) / 1.35;
    vec2 tilt = amt * dh * g;
    normal = normalize(normal - vec3(tilt.x, 0.0, tilt.y));
    ao *= 1.0 - 0.18 * min(amt, 1.0) * (0.5 - 0.5 * h);
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

// ANTI-TILING (fxSplatAnti). A texture laid on a grid repeats its features
// on that grid, and across a canyon floor or up a 50 m wall the eye finds
// them. Here the plane is cut into patches by the bands of a slow noise (a
// band per eighth of its range, so a patch is a repeat or so across and its
// edges wander) and each band lays the texture shifted by its own hash and,
// top down, turned by the golden angle times its index — so no two
// neighbours share a grid, and nine variants is plenty for nobody to match
// them. Where two bands meet, the two patches are blended by their height
// maps (the higher wins, the lower fills between), which keeps the blend's
// contrast instead of cross-fading two images into a smear. Outside the
// middle of a band only its own patch can win, and only that one is read:
// about half the pixels pay one fetch pair, the rest two. The derivation of
// the turned frame: u' = c·u − s·v, v' = s·u + c·v, so T' = c·T − s·B and
// B' = s·T + c·B.

// RELIEF (fxSplatRelief). A normal map turns a pebble's light but not its
// place: seen across a wash, every stone stays pasted flat on the plane,
// and gravel reads as a photograph of gravel. Parallax moves it. The layer's
// height map (the albedo atlas's alpha) is a surface `rel` repeats deep,
// centred on the mesh's plane; the texel seen along the view ray V is found
// by fixed-point iteration, u = u0 − d·(0.5 − h(u)) with d the ray's slope
// in the layer's frame — three height reads, which converge for gravel's
// gentle slopes — and the crevices it reveals are darkened in the AO by
// the same height. All of it fades out as a pixel comes to cover a good
// share of a repeat (where the offset is sub-texel and would only shimmer),
// and at grazing angles the slope is capped (V·N ≥ 0.3).

// fxSplatRead is a layer at uv (gradients gx, gy) in the frame (T, B):
// albedo+height and the world normal + AO, with relief rel (repeats; 0 none)
// seen from V.
void fxSplatRead(float layer, vec2 u, vec2 gx, vec2 gy, vec3 T, vec3 B, vec3 N, float ns, float rel, vec3 V,
                 out vec4 ah, out vec4 na) {
    float k = 0.0;
    if (rel > 0.0) {
        float foot = length(gx) + length(gy);
        k = rel * (1.0 - smoothstep(0.02, 0.12, foot));
    }
    if (k > 0.0) {
        vec2 d = vec2(dot(V, T), dot(V, B)) / max(dot(V, N), 0.3) * k;
        vec2 u0 = u;
        u = u0 - d * (0.5 - fxAtlas(fxSplatAlbedo, layer, u, gx, gy).a);
        u = u0 - d * (0.5 - fxAtlas(fxSplatAlbedo, layer, u, gx, gy).a);
        u = u0 - d * (0.5 - fxAtlas(fxSplatAlbedo, layer, u, gx, gy).a);
    }
    ah = fxAtlas(fxSplatAlbedo, layer, u, gx, gy);
    vec4 t = fxAtlas(fxSplatNormal, layer, u, gx, gy);
    na = vec4(fxTangentToWorld(t.xyz * 2.0 - 1.0, T, B, N, ns), t.w);
    if (k > 0.0) {
        na.w *= mix(1.0, 0.45 + 0.55 * smoothstep(0.05, 0.6, ah.a), k / rel);
    }
}

// fxSplatPatch samples patch i of a layer at uv (gradients gx, gy) in the
// frame (T, B): albedo+height, and the world normal + AO.
void fxSplatPatch(float layer, float i, bool turn, vec2 uv, vec2 gx, vec2 gy, vec3 T, vec3 B, vec3 N, float ns,
                  float rel, vec3 V, out vec4 ah, out vec4 na) {
    vec2 off = vec2(fxHash(vec2(i, 1.7)), fxHash(vec2(i, 9.2))) * 7.0;
    float ang = turn ? i * 2.39996 : 0.0;
    float c = cos(ang), s = sin(ang);
    vec2 u = vec2(c * uv.x - s * uv.y, s * uv.x + c * uv.y) + off;
    vec2 rx = vec2(c * gx.x - s * gx.y, s * gx.x + c * gx.y);
    vec2 ry = vec2(c * gy.x - s * gy.y, s * gy.x + c * gy.y);
    fxSplatRead(layer, u, rx, ry, c * T - s * B, s * T + c * B, N, ns, rel, V, ah, na);
}

// fxSplatTap is a layer at uv in the frame (T, B), straight (anti < 0.5) or
// anti-tiled (turn: top-down layers also turn their patches), with relief
// rel (repeats) seen from V.
void fxSplatTap(float layer, float anti, bool turn, vec2 uv, vec2 gx, vec2 gy, vec3 T, vec3 B, vec3 N, float ns,
                float rel, vec3 V, out vec4 ah, out vec4 na) {
    if (anti < 0.5) {
        fxSplatRead(layer, uv, gx, gy, T, B, N, ns, rel, V, ah, na);
        return;
    }
    float l = fxNoise(uv * 0.21 + vec2(layer * 3.7, 0.0)) * 8.0;
    float i = floor(l);
    // The share patch i+1 has before the heights have their say (they move
    // it by up to ±0.8): outside 0.24..0.76 of the band one patch is certain.
    float b = (l - i - 0.5) * 5.0 + 0.5;
    vec4 ahA = vec4(0.0), naA = vec4(N, 1.0), ahB = vec4(0.0), naB = vec4(N, 1.0);
    bool needA = b - 0.8 < 1.0;
    bool needB = b + 0.8 > 0.0;
    if (needA) {
        fxSplatPatch(layer, i, turn, uv, gx, gy, T, B, N, ns, rel, V, ahA, naA);
    }
    if (needB) {
        fxSplatPatch(layer, i + 1.0, turn, uv, gx, gy, T, B, N, ns, rel, V, ahB, naB);
    }
    float t = needA && needB ? smoothstep(0.0, 1.0, b + (ahB.a - ahA.a) * 0.8) : (needB ? 1.0 : 0.0);
    ah = mix(ahA, ahB, t);
    na = vec4(normalize(mix(naA.xyz, naB.xyz, t)), mix(naA.w, naB.w, t));
}

// fxSplatAxis adds triplanar axis a's share (weight wa) of layer tile
// `layer` at repeat 1/inv to the sums: albedo+height, world normal, AO and
// weight.
void fxSplatAxis(int a, float wa, float layer, float inv, float anti, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy,
                 inout vec4 ah, inout vec3 nrm, inout float ao, inout float sum) {
    if (wa < 0.02) {
        return;
    }
    vec3 T, B;
    vec2 uv = fxProj(a, p, N, true, T, B) * inv;
    vec2 gx = vec2(dot(dPdx, T), dot(dPdx, B)) * inv;
    vec2 gy = vec2(dot(dPdy, T), dot(dPdy, B)) * inv;
    float ns = a == 1 ? fxSplatParams.y : fxSplatParams.y * fxSplatBedding.z;
    vec4 tah, tna;
    // Shifted only, never turned: the side projections stand upright so a
    // rock's bedding stays level.
    fxSplatTap(layer, anti, false, uv, gx, gy, T, B, N, ns, 0.0, N, tah, tna);
    ah += wa * tah;
    nrm += wa * tna.xyz;
    ao += wa * tna.w;
    sum += wa;
}

// fxSplatTriSum is a layer's three world projections, blended by N.
void fxSplatTriSum(float layer, float inv, float anti, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, out vec4 ah, out vec4 na) {
    vec3 w = fxTriWeights(N, 6.0);
    ah = vec4(0.0);
    vec3 nrm = vec3(0.0);
    float ao = 0.0, sum = 0.0;
    fxSplatAxis(0, w.x, layer, inv, anti, p, N, dPdx, dPdy, ah, nrm, ao, sum);
    fxSplatAxis(1, w.y, layer, inv, anti, p, N, dPdx, dPdy, ah, nrm, ao, sum);
    fxSplatAxis(2, w.z, layer, inv, anti, p, N, dPdx, dPdy, ah, nrm, ao, sum);
    sum = max(sum, 1e-4);
    ah /= sum;
    na = vec4(normalize(nrm), ao / sum);
}

// fxSplatSample is a splat layer's albedo+height and normal+AO at p — its
// atlas tile `layer`, 1/repeat inv, top down or triplanar (tri) — with its
// normal already in world space (xyz of the second result; w the AO).
// strata (0/1): whether the bedding relief and the strata ramp band it when
// it is triplanar (a second biome's rock is not the strata's).
void fxSplatSample(float layer, float inv, bool tri, float anti, float relief, float strata, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy,
                   out vec4 ah, out vec4 na) {
    if (!tri) {
        // A top-down layer (sand, gravel) on a steep face — a wall mesh the
        // ground's weights put sand on, an overhang — would smear down it
        // in streaks: below 53° from level it blends into its own triplanar
        // projection, wholly so past 70°.
        float up = abs(N.y);
        if (up > 0.34) {
            vec2 uv = p.xz * inv;
            vec2 gx = dPdx.xz * inv;
            vec2 gy = dPdy.xz * inv;
            fxSplatTap(layer, anti, true, uv, gx, gy, vec3(1.0, 0.0, 0.0), vec3(0.0, 0.0, 1.0), N, fxSplatParams.y,
                       relief * inv, normalize(camPos - p), ah, na);
            if (up >= 0.6) {
                return;
            }
        }
        vec4 tah, tna;
        fxSplatTriSum(layer, inv, anti, p, N, dPdx, dPdy, tah, tna);
        float t = smoothstep(0.34, 0.6, up);
        if (up <= 0.34) {
            ah = tah;
            na = tna;
            return;
        }
        ah = mix(tah, ah, t);
        na = vec4(normalize(mix(tna.xyz, na.xyz, t)), mix(tna.w, na.w, t));
        return;
    }
    fxSplatTriSum(layer, inv, anti, p, N, dPdx, dPdy, ah, na);
    if (strata < 0.5) {
        return;
    }
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

// fxSplatCandidate is whether a layer can come within the blend of the best:
// it takes part, and its score — at most its weight plus its whole height
// lift (the height is in [0, 1]) — can beat floorBest − blend, the least the
// best can be. A layer that cannot adds nothing in fxSplatAdd (its share is
// 0), so it is never sampled. Exact.
bool fxSplatCandidate(float layer, float wt, float hgt, float floorBest, float blend) {
    return fxSplatLayerOn(layer, wt) && wt + max(hgt, 0.0) * 0.35 * min(wt * 4.0, 1.0) > floorBest - blend;
}

// fxSplatLayerScore scores a candidate layer (-1e9: not a candidate). With
// only set — this is the sole candidate — its score cannot matter: it is the
// best, its share the whole blend, and the result its own sample; so it is
// not sampled here at all (0). Otherwise the layer is sampled and only its
// HEIGHT used (the albedo atlas's alpha): the sample's normal fetch, its
// tangent frame, bedding and strata tint are dead code the compiler drops,
// so scoring a layer costs its albedo fetches alone.
float fxSplatLayerScore(bool cand, bool only, float layer, float inv, float tri, float anti, float relief, float strata,
                        float wt, float hgt, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy) {
    if (!cand) {
        return -1e9;
    }
    if (only) {
        return 0.0;
    }
    vec4 ah, na;
    fxSplatSample(layer, inv, tri >= 0.5, anti, relief, strata, p, N, dPdx, dPdy, ah, na);
    // A layer's height lifts it over its neighbours, fully once it has a
    // quarter of the weight: where stones and sand meet, the stones' tops
    // win and the sand fills between them.
    return wt + ah.a * hgt * 0.35 * min(wt * 4.0, 1.0);
}

// fxSplatAdd adds a scored layer's share — how far it is within the blend of
// the best — to the blend's sums, sampling it in full (albedo, normal, AO)
// only when that share is not 0. The sample is the very call fxSplatLayerScore
// made, so its height (and so its score) is the one the share was judged by.
void fxSplatAdd(float score, float best, float blend, float layer, float inv, float tri, float anti, float relief,
                float strata, vec3 tint, vec3 p, vec3 N, vec3 dPdx, vec3 dPdy,
                inout vec4 col, inout vec3 nrm, inout float occ, inout float sum) {
    float b = max(score - (best - blend), 0.0);
    if (b <= 0.0) {
        return;
    }
    vec4 ah, na;
    fxSplatSample(layer, inv, tri >= 0.5, anti, relief, strata, p, N, dPdx, dPdy, ah, na);
    col += b * vec4(ah.rgb * tint, 1.0);
    nrm += b * na.xyz;
    occ += b * na.w;
    sum += b;
}

// ---------- ground trails ----------

// fxTrailPress is how far the ground at xz is pressed, as a share of the
// soft depth: the trail map's two channels mixed by fill (gradients g, h).
float fxTrailPress(vec2 xz, float fill, vec2 g, vec2 h) {
    vec2 v = textureGrad(fxTrail, xz * fxTrailRect.z, g, h).rg;
    return mix(v.g, v.r, fill);
}

// fxTrailAt is the ground trails at p, whose splat weights are wt (and wt2
// in eight-layer mode): ps, the point the layers are to be sampled at —
// moved along the view ray to where it meets the bottom of a dent, which is
// what makes a footprint a hole seen in parallax rather than a stain — the
// dent's own slope (its depth's gradient in x and z, to bend the normal
// by) and press, how hard the ground is pressed there (0..1; below 0 a
// rim), faded out toward the window's edge. Nothing (ps = p) away from the
// soft layers, outside the window, or where nothing has been pressed.
//
// The march: from the surface down the view ray in six steps to the deepest
// a print can be, the first step below the trail's depth there, then a
// linear refine between it and the step before (relief mapping's secant).
// A print is a few centimetres deep and the slope of the ray is capped
// (V·N ≥ 0.25), so the whole march spans at most ~30 cm of ground.
// Probed first at p and at the march's far end: ground with no print near
// it — nearly all of it — costs those two reads.
void fxTrailAt(vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, vec4 wt, vec4 wt2, bool eight,
               out vec3 ps, out vec2 slope, out float press) {
    ps = p;
    slope = vec2(0.0);
    press = 0.0;
    float wsum = dot(wt, vec4(1.0)) + (eight ? dot(wt2, vec4(1.0)) : 0.0);
    vec4 on = step(vec4(1e-5), fxSplatSoft), on2 = step(vec4(1e-5), fxSplatSoft2);
    float softW = dot(wt, on) + (eight ? dot(wt2, on2) : 0.0);
    if (softW < 0.02 || N.y < 0.5) {
        return; // firm ground, or a slope too steep to hold a print
    }
    float depth = (dot(wt, fxSplatSoft) + (eight ? dot(wt2, fxSplatSoft2) : 0.0)) / max(wsum, 1e-4);
    float fill = (dot(wt, fxSplatFill * on) + (eight ? dot(wt2, fxSplatFill2 * on2) : 0.0)) / softW;
    // The window's edge: faded over its last three metres, nothing outside.
    vec2 rel = (p.xz - fxTrailRect.xy) * fxTrailRect.z;
    float edge = min(min(rel.x, 1.0 - rel.x), min(rel.y, 1.0 - rel.y)) * fxTrailRect.w;
    float fade = smoothstep(0.5, 3.0, edge);
    if (fade <= 0.0 || depth < 0.002) {
        return;
    }
    vec2 g = dPdx.xz * fxTrailRect.z, h = dPdy.xz * fxTrailRect.z;
    vec3 V = normalize(camPos - p);
    // Horizontal travel along the ray per metre of descent.
    vec2 run = -V.xz / max(V.y, 0.25);
    float far = depth * 1.05;
    float p0 = fxTrailPress(p.xz, fill, g, h);
    float p1 = fxTrailPress(p.xz + run * far, fill, g, h);
    if (max(p0, p1) < 0.01 && p0 > -0.01) {
        return;
    }
    // The march, in metres of descent t; the trail's depth at the ray's
    // point is press·depth (a rim, negative, stands above the surface,
    // and the ray is already below it).
    float tPrev = 0.0, dPrev = max(p0, 0.0) * depth;
    float t = 0.0, d = dPrev;
    if (dPrev > 0.0) {
        for (int i = 1; i <= 6; i++) {
            t = far * float(i) / 6.0;
            d = max(fxTrailPress(p.xz + run * t, fill, g, h), 0.0) * depth;
            if (t >= d) {
                break;
            }
            tPrev = t;
            dPrev = d;
        }
        // The secant between the last step above the floor and the first
        // at or below it.
        float a = dPrev - tPrev, b = t - d;
        t = mix(tPrev, t, a / max(a + b, 1e-5));
    }
    vec2 hit = p.xz + run * t;
    ps = vec3(hit.x, p.y - t, hit.y);
    press = fxTrailPress(hit, fill, g, h) * fade;
    // The dent's slope at the hit, by central differences a texel apart,
    // capped (a print's wall is near vertical; its normal would otherwise
    // turn right over).
    float e = fxTrailRect.w / float(textureSize(fxTrail, 0).x);
    float dx = fxTrailPress(hit + vec2(e, 0.0), fill, g, h) - fxTrailPress(hit - vec2(e, 0.0), fill, g, h);
    float dz = fxTrailPress(hit + vec2(0.0, e), fill, g, h) - fxTrailPress(hit - vec2(0.0, e), fill, g, h);
    slope = vec2(dx, dz) * depth / (2.0 * e) * fade;
    float sl = length(slope);
    if (sl > 1.5) {
        slope *= 1.5 / sl;
    }
}

// fxSplatSurface is the splat: the layers' albedo, normal and AO at p,
// height-blended by the weights over the terrain's rectangle. eight is the
// eight-layer mode (matFx's FX_SPLAT8, tested in main so the visibility
// buffer's variants can fold it to a constant): layers 5–8 from the second
// weights map, scored, skipped and blended exactly as the first four —
// one floor, one best, one height-lifted blend over all eight, so a sand
// of one biome and a grass of the other meet by their height maps the way
// sand and gravel do. Off, the code below is the four-layer splat's to the
// operation.
//
// TWO PASSES (docs/regions/PHASE_6.md). The first scores every layer that
// can take part from its height alone — none at all where a single layer is
// a candidate, whose score cannot change the result; the second samples in
// full only the layers within the blend of the best, adding each to the sums
// as it goes.
// Holding every scored layer's albedo and normal until the best was known
// kept sixteen vec4s live across eight inlined samples: on the GTX 1070 the
// eight-layer variant then ran at half the occupancy and its material pass
// cost twice the four-layer one's even on pixels where layers 5–8 weigh
// nothing. The arithmetic per layer, and the order the sums are added in,
// are unchanged, so the picture is the one-pass splat's; a winning layer's
// albedo fetch is simply repeated (from the texture cache).
//
// trail is FX_TRAIL (also tested in main): the ground trails (fxTrailAt)
// move the point every layer is sampled at into the footprints, bend the
// blend's normal down their walls and darken their floors.
void fxSplatSurface(vec3 p, vec3 N, vec3 dPdx, vec3 dPdy, bool eight, bool trail, inout vec4 albedo, inout vec3 normal,
                    inout float ao) {
    vec2 wuv = (p.xz - fxSplatRect.xy) * fxSplatRect.zw;
    vec4 wt = textureGrad(fxSplatWeights, wuv, dPdx.xz * fxSplatRect.zw, dPdy.xz * fxSplatRect.zw);
    float blend = fxSplatParams.x;
    vec4 L = fxSplatLayer, S = fxSplatInvScale, T = fxSplatTri, H = fxSplatHeight, A = fxSplatAnti, R = fxSplatRelief;
    vec4 Q = fxSplatStrataOn;
    float floorBest = max(max(fxSplatFloor(L.x, wt.x, H.x), fxSplatFloor(L.y, wt.y, H.y)),
                          max(fxSplatFloor(L.z, wt.z, H.z), fxSplatFloor(L.w, wt.w, H.w)));
    // Layers 5–8: their weights, and their part of the floor, before any
    // layer is scored (a fifth layer that dominates lets the first four skip).
    vec4 wt2 = vec4(0.0);
    vec4 L2 = vec4(-1.0), S2 = vec4(1.0), T2 = vec4(0.0), H2 = vec4(0.0), A2 = vec4(0.0), R2 = vec4(0.0), Q2 = vec4(0.0);
    if (eight) {
        wt2 = textureGrad(fxSplatWeights2, wuv, dPdx.xz * fxSplatRect.zw, dPdy.xz * fxSplatRect.zw);
        L2 = fxSplatLayer2;
        S2 = fxSplatInvScale2;
        T2 = fxSplatTri2;
        H2 = fxSplatHeight2;
        A2 = fxSplatAnti2;
        R2 = fxSplatRelief2;
        Q2 = fxSplatStrataOn2;
        floorBest = max(floorBest, max(max(fxSplatFloor(L2.x, wt2.x, H2.x), fxSplatFloor(L2.y, wt2.y, H2.y)),
                                       max(fxSplatFloor(L2.z, wt2.z, H2.z), fxSplatFloor(L2.w, wt2.w, H2.w))));
    }
    // The ground trails: where the layers are seen, through any footprint.
    vec3 ps = p;
    vec2 dent = vec2(0.0);
    float press = 0.0;
    if (trail) {
        fxTrailAt(p, N, dPdx, dPdy, wt, wt2, eight, ps, dent, press);
    }
    // The candidates: where one layer dominates (most of a wall, most of a
    // plain) it is the only one, and pass 1 samples nothing.
    bool c0 = fxSplatCandidate(L.x, wt.x, H.x, floorBest, blend);
    bool c1 = fxSplatCandidate(L.y, wt.y, H.y, floorBest, blend);
    bool c2 = fxSplatCandidate(L.z, wt.z, H.z, floorBest, blend);
    bool c3 = fxSplatCandidate(L.w, wt.w, H.w, floorBest, blend);
    bool c4 = false, c5 = false, c6 = false, c7 = false;
    if (eight) {
        c4 = fxSplatCandidate(L2.x, wt2.x, H2.x, floorBest, blend);
        c5 = fxSplatCandidate(L2.y, wt2.y, H2.y, floorBest, blend);
        c6 = fxSplatCandidate(L2.z, wt2.z, H2.z, floorBest, blend);
        c7 = fxSplatCandidate(L2.w, wt2.w, H2.w, floorBest, blend);
    }
    bool only = int(c0) + int(c1) + int(c2) + int(c3) + int(c4) + int(c5) + int(c6) + int(c7) == 1;
    // Pass 1: the scores.
    float s0 = fxSplatLayerScore(c0, only, L.x, S.x, T.x, A.x, R.x, Q.x, wt.x, H.x, ps, N, dPdx, dPdy);
    float s1 = fxSplatLayerScore(c1, only, L.y, S.y, T.y, A.y, R.y, Q.y, wt.y, H.y, ps, N, dPdx, dPdy);
    float s2 = fxSplatLayerScore(c2, only, L.z, S.z, T.z, A.z, R.z, Q.z, wt.z, H.z, ps, N, dPdx, dPdy);
    float s3 = fxSplatLayerScore(c3, only, L.w, S.w, T.w, A.w, R.w, Q.w, wt.w, H.w, ps, N, dPdx, dPdy);
    float best = max(max(s0, s1), max(s2, s3));
    float s4 = -1e9, s5 = -1e9, s6 = -1e9, s7 = -1e9;
    if (eight) {
        s4 = fxSplatLayerScore(c4, only, L2.x, S2.x, T2.x, A2.x, R2.x, Q2.x, wt2.x, H2.x, ps, N, dPdx, dPdy);
        s5 = fxSplatLayerScore(c5, only, L2.y, S2.y, T2.y, A2.y, R2.y, Q2.y, wt2.y, H2.y, ps, N, dPdx, dPdy);
        s6 = fxSplatLayerScore(c6, only, L2.z, S2.z, T2.z, A2.z, R2.z, Q2.z, wt2.z, H2.z, ps, N, dPdx, dPdy);
        s7 = fxSplatLayerScore(c7, only, L2.w, S2.w, T2.w, A2.w, R2.w, Q2.w, wt2.w, H2.w, ps, N, dPdx, dPdy);
        best = max(best, max(max(s4, s5), max(s6, s7)));
    }
    if (best < -1e8) {
        return; // no layer here: the surface's own texture stands
    }
    // Pass 2: the layers within the blend, sampled and added in order.
    vec4 col = vec4(0.0);
    vec3 nrm = vec3(0.0);
    float occ = 0.0, sum = 0.0;
    fxSplatAdd(s0, best, blend, L.x, S.x, T.x, A.x, R.x, Q.x, fxSplatTint[0], ps, N, dPdx, dPdy, col, nrm, occ, sum);
    fxSplatAdd(s1, best, blend, L.y, S.y, T.y, A.y, R.y, Q.y, fxSplatTint[1], ps, N, dPdx, dPdy, col, nrm, occ, sum);
    fxSplatAdd(s2, best, blend, L.z, S.z, T.z, A.z, R.z, Q.z, fxSplatTint[2], ps, N, dPdx, dPdy, col, nrm, occ, sum);
    fxSplatAdd(s3, best, blend, L.w, S.w, T.w, A.w, R.w, Q.w, fxSplatTint[3], ps, N, dPdx, dPdy, col, nrm, occ, sum);
    if (eight) {
        fxSplatAdd(s4, best, blend, L2.x, S2.x, T2.x, A2.x, R2.x, Q2.x, fxSplatTint[4], ps, N, dPdx, dPdy, col, nrm, occ, sum);
        fxSplatAdd(s5, best, blend, L2.y, S2.y, T2.y, A2.y, R2.y, Q2.y, fxSplatTint[5], ps, N, dPdx, dPdy, col, nrm, occ, sum);
        fxSplatAdd(s6, best, blend, L2.z, S2.z, T2.z, A2.z, R2.z, Q2.z, fxSplatTint[6], ps, N, dPdx, dPdy, col, nrm, occ, sum);
        fxSplatAdd(s7, best, blend, L2.w, S2.w, T2.w, A2.w, R2.w, Q2.w, fxSplatTint[7], ps, N, dPdx, dPdy, col, nrm, occ, sum);
    }
    albedo = vec4(col.rgb / sum, albedo.a);
    normal = normalize(nrm);
    ao *= mix(1.0, occ / sum, fxSplatParams.z);
    // The footprints: their walls turn the normal (the ground's height is
    // minus the dent, so its normal leans along the dent's slope), their
    // floors are packed darker and shadowed, and a rim stands a little
    // lighter in the light.
    if (press != 0.0 || dent != vec2(0.0)) {
        normal = normalize(normal + vec3(dent.x, 0.0, dent.y) * N.y);
        float pr = clamp(press, 0.0, 1.0);
        albedo.rgb *= 1.0 - 0.12 * pr + 0.05 * clamp(-press, 0.0, 1.0);
        ao *= 1.0 - 0.3 * pr;
    }
    // The sand ripples, on the rippling layer's share of the blend.
    if (fxSplatRipple.x > 0.0) {
        float rl = fxSplatRipple.w;
        float rs = rl < 0.5 ? s0 : (rl < 1.5 ? s1 : (rl < 2.5 ? s2 : s3));
        // A footprint has trodden its ripples flat.
        float share = max(rs - (best - blend), 0.0) / sum * (1.0 - clamp(press, 0.0, 1.0));
        if (share > 0.0) {
            fxRipple(ps, N, dPdx, dPdy, share, normal, ao);
        }
    }
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
    // Height sheared into the plane (fxMacro.w of it): a wall's patches
    // change up its face. Ground rises slowly, so there it is the same noise.
    vec2 q = p.xz + p.y * fxMacro.w * vec2(0.83, -0.56);
    float v = fxFbm(q * fxMacro.x) * 2.0 - 1.0;
    float w = fxFbm(q * fxMacro.x * 0.7 + vec2(31.0, 17.0)) * 2.0 - 1.0;
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
