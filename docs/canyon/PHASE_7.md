# Phase 7: the desert-canyon template and integration — what was built

Phase 7 wires every stage of the project into one document, the
**`desert_canyon` template**, runs it from nothing into a fresh project, and
spends the rest of the phase on what the phase-6 renders showed was still
wrong:

- (a) bare heightfield walls dominating every view;
- (b) rock stacks stuck onto pillar flanks;
- (c) the map's edge;
- (d) flat, LDR lighting;
- (e) the splat atlases' memory and start-up cost.

The user-facing chapters are "Make a desert canyon in five minutes" and the
stage sections of [docs/guide/33-generators.md](../guide/33-generators.md),
"HDR and the tone curve" in [08-sky-and-fog.md](../guide/08-sky-and-fog.md),
the splat row of [28-materials.md](../guide/28-materials.md) and the template
and atlas notes in [34-polyhaven.md](../guide/34-polyhaven.md).

`$S` below is the session scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/8b1c0d12-038d-4935-9ad4-2bf2380ff5b6/scratchpad`.

## The template

`internal/procgen/stages/desert_canyon.go` registers `desert_canyon` (File >
New in the Generator editor, `foundry gen new <name> -template
desert_canyon`). The repo ships the same document as
`resources/generators/desert_canyon.fgen`; a test fails if the two drift.

| Stage | Kind | Defaults that matter |
| --- | --- | --- |
| `tex` | polyhaven_textures | sand `red_sand` 3 m, cracked `cracked_red_ground` 4.5 m, gravel `dry_river_pebbles` 2 m, cliff `cliff_side` 10 m |
| `ground` | terrain | 512 m at 0.5 m in 128 m tiles; mesas 55 m, coverage 0.75, scale 340, rim 26; one stepped canyon (width 70–100, depth 55–72, no tributaries); `far` 3000 m at 8 m, blend 140 m, 512 m ring tiles; wears `gen_<doc>_ground_mat`, ring wears `…_far` |
| `ground_mat` | terrain_material | rock = cliff at 10 m, strata 0.45, streaks 0.4, bedding 0.22 at 2.4 m, wall normal 1.8, normal strength 1.2, caprock 0.75, rock tint (1.1, 0.94, 0.84), cracked cover 0.3 |
| `cliffs` | polyhaven_rocks | namaqualand_cliff_01/02, rock_face_01, mountainside, coastal_cliff_02 (decimated to 400k) |
| `boulders` | polyhaven_rocks | namaqualand_boulder_02..05, no back, 5k lite copies |
| `stones` | polyhaven_rocks | namaqualand_stones_01, namaqualand_rocks_01 (split into pieces), 2k lite, no collision |
| `trees`, `deadwood`, `bushes` | polyhaven_plants | quiver_tree_01/02; dead_quiver_trunk; wild_rooibos_bush, shrub_04 |
| `walls` | cliff_wall | max 1500 chunks, scale 1–3, spacing 0.45, min wall 6 m, stretch 40 m, focus = the spawn at 220 m, match to rock 0.9, brightness 0.8 |
| `scatter` | scatter | talus 5/100 m², boulder field 0.9, wash stones 14 (≤ 3000), trees 0.12, deadwood 0.06, bushes 1.2 |
| `plants` | vegetation | desert scrub at 0.08 — **disabled** (see Known gaps) |
| `air` | environment | the phase-6 atmosphere, aerial 1700 m, view distance 4500 m, **HDR on, exposure 0.75**, sun 1.5, ambient 0.38 bluish, spawn on the canyon floor near (−150, −45) facing 20° |

TAA, the virtual shadow map and live GI are on by default (player settings);
the template does not turn them off, and the level does not disable GI.

New plumbing the template needed:

- **Level settings** `tonemap` and `spawn` (genlevel/settings.go), decoded
  through the level schema like the phase-6 keys.
- **A missing target level is created** by the file splice
  (genlevel/splice.go), so `gen new` + `gen run` works in an empty project.
- **environment** gained `hdr`, `exposure`, `ambient_brightness`,
  `ambient_color`, `spawn`, `spawn_at`, `spawn_yaw` (all `Added`, so existing
  documents keep their hashes). The spawn snaps to the nearest canyon
  centreline point within 80 m.

## (a) Walls that read as rock

Two halves: cover more wall with scans, and make the bare wall that is left
read as layered rock.

### More, and better placed, scans (cliff_wall, place/wall.go; Version 7 → 8)

- **Bigger pieces**, in the template: namaqualand_cliff_02 (20 m),
  mountainside (10 m) and coastal_cliff_02 (41 × 10 m), beside the 5–8 m
  pieces. `rock_face_02` was tried and dropped (its glTF is 0.56 of Poly
  Haven's listed size: a pebble beside the others).
- **Height evening** (`height_norm`, 0.7): each variant is scaled by
  (mean height / its height)^0.7, so mixing a 40 m and a 5 m scan does not
  make one eight times the other. Column spacing uses the evened widths
  (it was driven by the 41 m piece and spaced every column ~21 m).
- **No fixed pitch** (`tier_jitter`, 0.35): each tier starts at the top of
  the chunk actually placed below it, less a jittered overlap; before, every
  column stacked at `meanH·sc·(1−overlap)` steps, which read as courses.
- **A failed tier no longer ends its column**: the tiers above are still
  tried (a bulge under one tier only).
- **Budget by what is seen** (`focus`, `focus_range`): stretches are ranked
  by mean height × 1/(1 + distance to focus / range). The template focuses on
  the spawn.
- **Colour** (`match_to: rock`, `match_brightness`): the chunks match the
  terrain material's rock layer (`TerrainMaterialArtifact.RockMean`, new)
  rather than the ground average, and are darkened 0.8 — the pale Karoo
  granite scans stood out as white blobs. Also fixed: `rockMaterial` used to
  overwrite any match target with the ground mean whenever a ground blend was
  on.
- Result on the final template: 43 traced lines (4.5 km of wall feet), 317
  columns, **570 chunks** (was 333 on the first template run and 300 in the
  phase-4/6 scratch canyon), 68 M triangles at full detail.

### Bare walls (terrain_material + surface_fx.glsl)

- **`cliff_side`** — Poly Haven's layered red cliff face — as the rock layer
  at 10 m a repeat (was `rock_face` at 6 m, a varnished face with no
  bedding). See `$S/tex_sheet7.png` for the candidates looked at: most
  `rock_wall_*` sets are masonry.
- **Bedding relief** (new splat params `bedding`, `bedding_spacing`;
  `fxBedding` in surface_fx.glsl). On steep faces of triplanar layers the
  normal is bent by the slope of a bed profile D(y): each bed recesses
  steadily upward (faces tilt to the sun) and snaps back at a lip (a short
  down-facing overhang that goes dark, plus an AO darkening under it). Bed
  thickness is irregular (the height coordinate is bent by a slow
  monotone-preserving noise of itself), a third of the beds are flush, the
  depth varies in patches along the face, laminations at a third of the
  spacing are faint, and the whole effect fades analytically by the pixel's
  footprint in bed units, so distant walls do not shimmer. No samplers, no
  derivatives: the visibility buffer's material pass takes it unchanged
  (`TestVisMaterialProgramBuilds`, `TestMaterialFxSamplersFitTheMaterialPass`
  pass).
  - First version (spacing 1.4, recess 0.32, laminations 0.35) read as
    contour lines / a ruled page (`$S/shots7/a1_SPAWN`, `a1_OVER`); the
    irregular beds and weaker laminations are from looking.
- **`wall_normal`** (new splat param): the triplanar layers' normal map is
  multiplied on side projections only (template 1.8).
- **`rock_tint`** (terrain_material): a warm/red multiplier on the rock layer.
- **`cap_rock`, `cap_height`** (terrain_material): bare rock on high flats,
  in patches, not where sand is deep — caprock instead of sand plateaus.
- The cracked-ground layer read as paving and the riverbed-rock gravel as a
  cobbled road (`$S/shots7/d_WALL`, `e_WALL`): cracked is at 4.5 m and cover
  0.3, and gravel is `dry_river_pebbles`.

Honest result: close up and at 50–300 m the walls now read as layered red
sandstone with ledges (`fin_FLOOR`, `fin_SPAWN`, `fin_EXIT`). From a
distance the big walls are still smooth, extruded, slightly vertically
streaked surfaces with regular banding, and mesa outlines are too smooth
and trapezoidal. A heightfield cannot overhang, and 570 scans still clad
only the lower tiers of the walls nearest the spawn.

## (b) Rock stacks on pillar flanks

Two tests in `seatChunk`/`Walls` (place/wall.go):

- **Contact** (`min_contact` 0.65, `contact_gap` 0.6 m): `backContact`
  samples the chunk's back — every covered cell of its back profile (phase
  5's `MeasureBack`), or a 5×6 grid over its box — at its seated position
  (tb in from the foot, leaned, stood forward by the cell's gap) and counts
  the cells whose face, read at the nearest transect, is no more than
  `contact_gap` behind it. A side whose transect could not be measured (the
  wall ends: a pillar narrower than the chunk) touches nowhere. Under the
  share, the next variants are tried, then the tier is left empty.
- **Backing** (`backing` 0.8 chunk widths): going in from the rim, the ground
  must stay above the wall's lower third for that far, or the column is a
  pillar or fin and gets nothing.
- In the final run: 65 tiers dropped for contact, 41 pillar columns skipped,
  273 seats taken by a second variant.
- `TestWallsLeavePillarsAndHangingSeats`: five 7 m pillars beside a mesa wall
  get 75 chunks with the tests off and 0 with them on, while the mesa wall
  keeps ≥ 10. (With 9 m pillars, chunks the pillar's own width still land,
  buried at its centre: that is a chunk wrapping a pillar, which reads fine.)

No floating cantilevered pieces showed in the final renders; the phase-6
`a1_SPAWN` floater (top right) is gone.

## (c) The map edge

**A far backdrop generated by the terrain stage** (terrain/far.go,
stages/terrain_far.go, stages/terrain_material_far.go):

- `terrain.FarField`: the base shape (shape.go's `baseShaper`, refactored out
  of `baseShape` with identical arithmetic) continued past the edge with the
  map's own mesa thresholds, terraced through the strata where it is steep
  (≥ 22°), with a few metres of ridged detail; eased from the map's edge
  sample over `far_blend`; every authored canyon that leaves the map carried
  on along its heading, wandering, narrowing and shallowing out; eased to
  the lowland in the last eighth of the reach.
- **Ring tiles**: the far square less the map and a one-cell strip, cut on the
  lattice into ≤ `far_tile` rectangles, each a closed HeightGrid with
  LockRect, no collision, `chunk` unset, wearing `far_material`.
- **Stitch strips**, one per edge tile and side: inner edge = that tile's own
  rim samples in the same tile-local frame and quantisation step, outer edge
  = the ring's lattice row one cell out, zipped, plus corner quads. They wear
  the map's own material (the splat clamps at the edge). The edge tiles'
  skirts are now under ground.
- **Far material**: terrain_material writes `far_weights.png` (rock by slope,
  caprock by height, cracked patches, sand) on the far lattice and
  `gen_<doc>_<id>_far.fmat` with the same layers/tints/strata, a softer
  height blend (≥ 0.7: a crisp one cut the layers into lattice squares) and
  triple-scale macro variation.
- Template: 3 km at 8 m → 192 ring tiles + 16 strips, 14.5 s of the run,
  100 MB of `.fnm`. At 16 m it was 48 tiles, 3.5 s and ~25 MB, but the near
  ring looked blocky from the rims (`$S/shots7/i_RIM`).
- `view_distance` 4500 in the template; the aerial haze (1700 m) carries
  the horizon into the sky.
- No skybox change: `sky_canyon`'s painted landscape sits behind the ring's
  hazed silhouettes.

Seen: from the canyon floor (`fin_FLOOR`, `fin_EXIT`) and toward the sun
(`fin_SUN`) no edge or skirt is visible; the canyon walls close the view.
From a rim on the map's edge (`fin_RIM`; the iterations `h_RIM`…`k_RIM`)
there is no hard edge, and far mesas stand to the horizon in the haze, but
the first few hundred metres past the edge are visibly coarser and smoother
than the map, and the carried-on canyon starts as a straight trench (the
map's edge profile extruded over the blend).

## (d) Lighting: HDR and a filmic tone curve

**Behind a level setting, default off; on in the template.**

- `world.Tonemap` / the level's `tonemap:` block (`exposure`, `curve: aces`,
  `disabled`); `RawLevel.Tonemap`, loader and saver lines; editor YAML docs
  regenerated.
- `gfx.HDRFrame` (gfx/hdr.go): set by `render.sceneFrameBegin` only when the
  frame draws into the RGBA16F scene target (TAA frames). `world.PackFog`
  packs `HdrMode`/`HdrExposure` into the scene block's former std140 padding
  (size unchanged, 3968 bytes; `TestSceneBlockMatchesTheShader` passes).
- fragment.glsl: the light clamp is `hdrLightMax()` (1 in LDR, 64 in HDR);
  unlit surfaces go through `hdrFromDisplay`; lit surfaces are fogged with
  `applyFogScene`. With `hdrMode` 0 every expression reduces to the old one.
- fog.glsl: `hdrFromDisplay` (the ACES fit's analytic inverse, divided by the
  exposure). `applyFog` (every HDR-unaware pass) carries its input and the fog
  colours into scene units; the atmosphere's medium colour is carried, and
  the **sun's lobe is added as light in scene units** — carried through the
  inverse it saturated channel by channel and drew green/yellow rings round
  the sun (`$S/shots7/e_SUN`).
- sky_frag.glsl: the whole sky, haze and volumes are computed in display
  colour as before and carried once at the end — mixing the haze in scene
  units after the inverse amplified the cubemap's 8-bit steps near white into
  bands (`e_WALL`, `e_TREE` skies).
- taa_output_frag.glsl: `tonemap`/`exposure` uniforms; each tap is
  `ACES(c · exposure)` before the sharpen.
- **Baked light**: kept as scene-linear at exposure 1, not inverted (it is
  light). A baked level that opts in sees its brightest baked light at
  ACES(1) ≈ 0.8 × exposure; documented in guide 08. The canyon is lit live.
- Not converted: additive blends (read a little dimmer under the curve).
- Tests: `TestHDRDisplayColourSurvivesTheCurve` (GL: screen colours through
  applyFog + the curve come back within 0.02 at exposures 1 and 1.6; deep
  haze comes out the haze colour; untouched outside a scene frame or without
  the block); `TestACESFilmInverseRoundTrips`; `TestTonemapPacksOnlyInASceneFrame`.

Looking (`$S/shots7/sheet_exp.png`: LDR sun 1 | HDR exposure 0.95 | HDR
0.75, at the spawn and the wall views): HDR gives brighter sunlit rock and
deeper relative shade, and the bedding and scan relief read more strongly.
At exposure 0.95 the sunlit rock bleached toward cream (the ACES fit
desaturates highlights); 0.75 keeps most of the colour. The LDR frame is
still the more saturated red — the honest trade-off of this curve.

## (e) Splat atlases (sub-agent)

- `gfx.EnsureMaterialTextures` (startup, material/texture reload) no longer
  builds splat atlases or loads fx data maps: placement load
  (`world.Scenery`) and the first draw (`applySplat`, which is also the
  material editor's preview) do.
- With `TextureCompression` on, both atlases upload BC7 (albedo with height
  in alpha, normal with AO in alpha), cached by the packed atlas bytes through
  `texcompress.LoadOrEncode`; skip-mips drops leading levels of the full-size
  chain where the halving is exact (byte-identical to packing small), else
  packs small as before. Refusal and encode failures fall back to RGBA8.
  Compression off: the old upload exactly.
- Measured (four 2k layers, 4352² atlas): RGBA8 0.88 s, 192.7 MB; BC7 first
  build 2.9 s (encode 2.24 s), 48.2 MB; from cache 1.05 s; BC7 + skip 1,
  12.0 MB.
- Tests: `TestEnsureMaterialTexturesBuildsNoSplatAtlas`,
  `TestSplatHalvesExactly`, `TestSplatPackFullThenHalveIsPackSmall` (no GL);
  `TestSplatAtlasCompressedUploadsAsBC7` (42.7 / 42.9 dB vs RGBA8, VRAM delta
  exact, cache hits), `TestSplatAtlasSmallStaysRGBA8` (GL).
- Gap: an atlas built before the setting changes is not rebuilt until its
  next build.

## Numbers (dev box: 20 threads, WSL2, WSLg Mesa d3d12 on the GTX 1070)

**Generator** (`foundry gen run desert_canyon -workers 8`, fresh project
`$S/p7b`, `/usr/bin/time -v`):

| Run | Wall | Peak RSS |
| --- | --- | --- |
| From nothing, empty Poly Haven cache (~230 MB downloaded) | 1 min 50 s | 1.21 GB |
| From nothing, warm Poly Haven cache | 1 min 44 s | 1.17 GB |
| Rerun, nothing changed | 0.9 s | 0.19 GB |
| Rerun after a placement/material change | 1–2 s | |

Cold-run stages: tex 2.6 s; ground 32.9 s (field 2.0 s, 16 tiles cooked in
16.3 s, backdrop 14.5 s); ground_mat 1.3 s; cliffs 42.0 s (coastal_cliff_02's
decimation from 1.77 M is most of it); boulders 16.8 s; stones 5.4 s; trees
5.6 s; deadwood 1.1 s; bushes 1.9 s; walls 0.15 s; scatter 0.22 s; air 0 s.
Downloads are a few seconds of the cold run: the cooks dominate both.

**Disk** (fresh project): `generated/desert_canyon` 213 MB (ground 206 MB:
map tiles 97 MB, backdrop 100 MB), `models/polyhaven` 128 MB, `textures/ph_*`
437 MB (PNG at BestSpeed, about twice the JPGs), level YAML 2.4 MB (6,607
placements), Poly Haven cache 246 MB.

**Level load** (`FOUNDRY_BENCH`, yaml, GPU path): 8.0–8.8 s.

**Frame** (1920×1080, TAA, visibility buffer, VSM, GI, WSLg; p50 of 30
frames after 60 warm):

| Pose (`$S/shots7/fin_<pose>`) | p50 frame | CPU frame | F3 "shadows: sun" (CPU) | F3 "nanite: scenery" (CPU) |
| --- | --- | --- | --- | --- |
| SPAWN | 146.6 ms | 20.2 ms | 5.4 ms | 9.5 ms |
| FLOOR (down-canyon) | 126.4 | 21.9 | 5.6 | 11.2 |
| OVER (rim overview, 230 m up) | 279.6 | 21.0 | 5.3 | 9.7 |
| WALL | 114.2 | 21.7 | 5.6 | 10.8 |
| SUN (toward the sun) | 125.5 | 20.7 | 5.8 | 10.0 |
| TREE (quiver tree and boulders) | 186.0 | 21.0 | 5.3 | 10.3 |
| EXIT (down-canyon at the east exit) | 49.8 | 15.3 | 6.4 | 4.3 |
| RIM (outward from the west rim) | 37.2 | 15.3 | 5.8 | 4.3 |

- The CPU frame is 15–22 ms in every view; the rest is GPU wait (the
  F3 "foliage" row held 100 ms of it while the vegetation volumes were on —
  their A/B was 132 vs 123 ms p50). GPU timer queries read 0 on Mesa d3d12, so
  WSLg cannot attribute GPU time; **every frame number here needs the GTX
  1070 under Windows to judge.** Phase 6's floor view was 77 ms on a 256 m
  map with 1,540 placements; this level has 6,607 placements, a 3 km
  backdrop and 2.4× the screen-filling wall area.
- Texture memory: ~1 GB uncompressed (72 textures); turn
  `texture_compression` on (a quarter of that).

## Pictures

All in `$S/shots7/<name>/pose_0.png` (bench runs, one pose each).

- **Final** (fresh project `$S/p7b`): `fin_SPAWN`, `fin_FLOOR` (down-canyon),
  `fin_OVER` (rim overview from 230 m), `fin_WALL`, `fin_SUN`, `fin_TREE`,
  `fin_EXIT` (down-canyon at the east exit), `fin_RIM` (from a rim on the
  west edge, outward); sheet `sheet_fin.png`.
- **Before/after**: `sheet_before_after7.png` — phase 6's
  `shots6/after_OVER`, `after_FLOOR`, `after_SUN` (left) against the final
  overview, floor and sun views (right).
- HDR: `sheet_hdr_ab.png` (LDR left, HDR right) and `sheet_exp.png`.
- Iterations: `a1_*` (first template: washed out, contour-line bedding, a
  floating chunk), `b_*`/`c_*` (cameras inside rock: poses now from the
  heightmap), `d_*` (paving, grey walls), `e_*` (the sun's ring and sky
  banding), `f_*` (fixed), `g_*`, `h_RIM`…`k_RIM` (the backdrop near the edge),
  `nv_*`/`t_*` (the vegetation hang bisection).

## Tests

| Package | New |
| --- | --- |
| procgen/stages | `TestDesertCanyonTemplateIsShippedAndValid`, `TestFarBackdropRingStitchAndMaterial`, `TestStitchStripMeetsTheTileRim` |
| procgen/terrain | `TestFarFieldContinuesTheMap` |
| procgen/place | `TestWallsLeavePillarsAndHangingSeats`, `TestTiersAreNotEvenlyPitched` |
| world | `TestTonemapPacksOnlyInASceneFrame` |
| gfx | `TestACESFilmInverseRoundTrips`; the sub-agent's atlas tests (above) |
| render (GL) | `TestHDRDisplayColourSurvivesTheCurve` |

Results at the end of the phase:

- `go build ./...` and `go vet ./...` clean.
- `go test` passes for procgen/... (all), world/..., render/..., gfx/...,
  material, levelio, polyhaven/..., texcompress, pack, app, harness and
  internal (layering). editor passes except the score session's
  `TestScoreSibeliusKeymap` and `TestScoreRebinding`.
- GL (`FOUNDRY_GL_TEST=1`): gfx passes; editor's Generator, Poly Haven,
  Material, Environment, Fog, Sky and YAML tests pass; render passes except
  the recorded `TestNaniteFoliageDrawsTheClassicNearField` (457 s run) —
  including the parity suites, `TestMaterialFxOnEveryPath`,
  `TestVisMaterialProgramBuilds`, `TestSceneBlockMatchesTheShader` and the
  fog and sky tests, so levels without the new blocks shade as before.

## Known gaps — the honest distance to the Nanite demo

The template now makes something that reads as a red-rock canyon from the
floor: layered sandstone walls with scanned blocks at their feet, sand
dunes, pebble washes, boulders and quiver trees, haze, a bright sun and deep
shade, and mesas to the horizon. It is not "Lumen in the Land of Nanite":

- **Geometry.** The demo's cliffs are millions of scanned triangles all the
  way up. Here the walls are a 0.5 m heightfield — smooth, extruded, never
  overhanging — with 570 scans on their lower tiers. From any distance the
  silhouettes (straight trapezoidal mesa tops, smooth curved rims) give it
  away, and the vertical facet streaks of a stretched heightfield remain.
  The bedding normal trick reads at 20–300 m and fades beyond.
- **Material.** One rock texture with triplanar seams on curved faces, a
  strata tint and bedding. No per-region rock variety, no talus/rockfall
  colour, no varnish that follows the actual drainage.
- **Lighting.** Live GI and the virtual shadow map, with an ACES curve: no
  Lumen-style multi-bounce in the canyon's shade, no specular, and the ACES
  fit desaturates highlights (exposure 0.75 is the compromise). No sky
  occlusion beyond GI's probes.
- **Backdrop.** No hard edge, but the first few hundred metres past the map
  are coarse and smoother than the map, the carried-on canyon starts
  straight, and the backdrop is 100 MB at 8 m.
- **Vegetation stage.** The biome volumes (the engine's Nanite foliage path,
  not this phase's code) made WSLg's GPU stall for good in two of the views
  down the canyon (bisected: no hang without the volumes, with the walls,
  the scatter or the backdrop removed instead it still hung). The template
  ships the stage disabled; the scanned plants carry the vegetation. Needs
  the 1070 to see whether it is a driver issue.
- **Performance** is unjudged on the target: 110–290 ms on WSLg, GPU-bound.
- **Atlas rebuild** on a compression/skip-mips change waits for the next
  build; HDR is LDR in the editor view and in MSAA/off modes; additive
  effects are not carried through the curve.
- **cliff_wall** budget is still a count (`max_chunks`) ranked by
  height × focus distance, not true screen area; the `focus` is one point.
- The phase-0 `list_assets` enum gap still stands.

## Recommended next steps

1. Judge everything on the 1070: frame time per pose, the vegetation hang,
   texture compression on.
2. Walls: displaced or tessellated cliff meshes along the traced walls (a
   "wall sheet" generated from the heightfield's cliff band, with SDF
   strata/undercuts from phase 2's library and the cliff_side texture), so
   silhouettes and overhangs are geometry; or many more, larger scans with a
   GPU-memory budget rather than a count.
3. Mesa silhouettes: rim noise at the 5–20 m scale and capstone breakup in
   the terrain's shape, and caprock scans along rims.
4. Backdrop: blend region at the map's resolution (a finer first ring), a
   meandering start for the carried-on canyon, and a 16 m outer ring to cut
   its disk size.
5. Lighting: a better tone curve (AgX-like) or a saturation-preserving ACES
   variant; Lumen-style screen-space GI or more GI cascades in canyons.
6. A screen-area budget for cliff_wall from the spawn's and the canyon
   floor's visibility.

## Files

Changed this phase, outside the packages new to the canyon project
(internal/procgen/**, internal/polyhaven/**, internal/texcompress/**):

| File | Change |
| --- | --- |
| internal/gfx/hdr.go (new), hdr_test.go (new) | `HDRFrame`, `ACESFilm`, `ACESFilmInverse`, `HDRLightMax` |
| internal/gfx/scene_block.go | `HdrMode`, `HdrExposure` in the former padding |
| internal/gfx/material_fx.go | the `fxSplatBedding` uniform |
| internal/gfx/material_fx_textures.go, material_textures.go (sub-agent) | lazy, BC7-compressed splat atlases |
| internal/gfx/material_fx_atlas_test.go, material_fx_atlas_export_test.go, material_fx_atlas_gl_test.go (new, sub-agent) | atlas tests |
| internal/material/material_kinds_terrain.go, material_resolve_fx.go | splat `bedding`, `bedding_spacing`, `wall_normal` |
| internal/world/tonemap.go (new), tonemap_test.go (new) | the `tonemap:` block |
| internal/world/fog.go | `packTonemap` from `PackFog` |
| internal/world/level.go, level_schema.go | `Level.Tonemap`, `RawLevel.Tonemap` |
| internal/levelio/level_loader.go, level_save.go | one line each |
| internal/render/taa.go | `gfx.HDRFrame`; the output pass's tonemap uniforms |
| internal/render/hdr_gl_test.go (new) | the HDR round trip |
| internal/editor/editor_yaml_docs_gen.go | regenerated (`tonemap`) |
| resources/shaders/fog.glsl, fragment.glsl, scene_block.glsl, sky_frag.glsl, taa_output_frag.glsl, surface_fx.glsl | HDR; bedding and wall normals |
| resources/generators/desert_canyon.fgen (new) | the template's document |
| docs/guide/08-sky-and-fog.md, 28-materials.md, 33-generators.md, 34-polyhaven.md | the chapters above |
| docs/canyon/PHASE_5.md | one "(fixed in phase 7)" line under its atlas gaps (sub-agent) |

Inside the canyon packages: procgen/terrain far.go (new), far_test.go (new),
shape.go (baseShaper), meta.go (FarMeta, FarHeights); procgen/stages
desert_canyon.go, desert_canyon_test.go, terrain_far.go, terrain_far_test.go,
terrain_material_far.go (new), terrain.go, terrain_run.go,
terrain_material.go, cliffwall.go, environment.go, materials_out.go;
procgen/place wall.go, wall_contact_test.go (new); procgen/genlevel
settings.go, splice.go. Removed: `internal/procgen/stages/materials/`, two
`.fmat` files the placement tests wrote into the package directory on every
run (a cliff_wall stage with `fileutil.ResourcesPath` unset); the new
`stages/main_test.go` gives the package's tests a temporary resources folder,
so it no longer comes back.

No shared file from the spec's list was touched, and none of the score
session's paths.
