# Environment regions — phase 3: biome-weighted materials

Status: built and tested, 2026-10-12. Every gate has a test or a render
behind it. A two-region world (desert_canyon + savannah, 768 × 512 m at
1 m) was generated in a scratch project, rendered from the ground at the
seam through the real engine (WSLg, Mesa d3d12 on the GTX 1070), and looked
at. No material seam was found. A tile-edge normal seam was found, and it is
not the material's (see "Found on the way").

`$S` below is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.

## What was built

### 1. The eight-layer splat (material, gfx, shaders, render)

**An eight-layer MODE of `splat`, not a new effect.** It is chosen so that
existing materials resolve, bind and shade exactly as before.

- **Material schema** (material_kinds_terrain.go). New parameters are
  `weights2` and `layer5..8` (with `_scale`, `_triplanar`, `_height`,
  `_tint`, `_anti_tile` and `_relief`, mirroring 1–4), plus
  `layer1..8_strata` (bool, default **on**). They are appended after every
  existing parameter. A parameter at its default is never written, so
  existing `.fmat` files and every `terrain_material` output encode
  byte-identically. `SplatLayers` stays 4 (layers per weights map), and
  `SplatLayersMax = 8` is new.
- **Resolve** (material_resolve_fx.go). `SurfaceFx.SplatLayers` is now
  `[8]`. The new fields are `SplatWeights2`, `SplatEight` and
  `SplatLayer.NoStrata`. `SplatEight` is true only when `weights2` and at
  least one of layers 5–8 are set. Otherwise layers 5–8 and `weights2` are
  zeroed, so a four-layer material's `SurfaceFx` is the old value plus
  zero fields (the vis pass's `!=` still works).
- **Atlases** (gfx/material_fx_textures.go). More than four DISTINCT layers
  pack into a **4×2** atlas pair on the same units 25 and 26. The pair uses
  the same gutter (`size/32`, at least 2), the same unfolded gradients, the
  same RGBA8 and BC7 paths and the same skip-mips rule (halving is per
  tile). Each layer is at most `FxArray8MaxSize` = 1024 by default, or
  `FOUNDRY_SPLAT_MAX_SIZE` when that is set, so the pair is 4352 × 2176:
  half the texels of a four-layer 2k pair. The cache is keyed by all eight
  names. Four distinct layers or fewer still make the **2×2**, with the
  same key hash (`splatKeyHash` falls back to the old `hashNames`) and the
  same pixels (`packSplatAtlases` is now `packSplatAtlasGrid(…, 2)`, tested
  byte-equal). `SplatArraysFor([4]string)` still works, and
  `SplatArraysFor8` is new. `uploadAtlasWH` and
  `uploadSplatAtlasCompressedWH` take non-square atlases.
- **Uniforms** (gfx/material_fx.go).
  - Unit 31 (`FxUnitSplatWeights2`) holds the second weights map. Unit 31
    was free; `TestFxUnitsAreFree` covers it. Like the others, it rests on
    unit 0.
  - Layers 5–8 have `fxSplat{InvScale,Tri,Height,Layer,Anti,Relief,StrataOn}2`.
  - `fxSplatTint` is now `[8]`.
  - `fxSplatAtlas` is a vec4: the tile fraction and the gutter fraction,
    each per axis.
  - `fxSplatCols` is the column count (2 or 4).
  - `fxSplatStrataOn` holds the per-layer strata switches.
  - The new **matFx bit `fxSplat8` = 128** is set only when eight-layer
    mode applied. If its second map is missing, the splat draws its first
    four layers.
- **Shader** (surface_fx.glsl, fragment.glsl).
  - `fxSplatSurface` takes `bool eight`. In fragment.glsl's main it is
    `(matFx & FX_SPLAT8) != 0`.
  - Layers 5–8 are four more unrolled `fxSplatLayerScore` and
    `fxSplatAccum` calls with constant arguments. There is no runtime
    indexing.
  - They sit inside `if (eight)` blocks, as do the second weights read and
    their part of the `fxSplatFloor` lower bound. The floor is computed
    over all eight layers before any layer is scored, so a pixel samples
    only the layers that can win.
  - Height-lifted blending covers all eight: one `best`, one blend.
  - `fxAtlas` addresses `cols × 2` tiles. For the 2×2 every factor equals
    the old one exactly (1/cols = 0.5, u fractions = v fractions).
  - Strata tint and bedding apply to a triplanar layer only when its
    `strata` switch is on (default on: unchanged).
  - Only textureGrad is used, with no new derivatives.
- **Visibility buffer** (render/nanite_vis_variants.go). The new key bit
  `visKeySplat8` comes with the spec `(matFx & FX_SPLAT8) != 0`. A bin of
  four-layer splat gets a variant compiled with `(false && …)`, so layers
  5–8 are compiled out. That is the "compile-time branch four-layer
  materials never enter": the material pass's variants are where the
  compile-time path already existed. The forward/classic program takes it
  as a uniform branch.

### 2. The `region_material` stage (procgen/stages/region_material*.go)

- **Stage settings.** Kind `region_material`, Category Materials,
  `Regional: Shared`, `SharedFor: [terrain_material]`, `HashExtra`.
- **Params.** `terrain` (a `region_terrain` ref), `offline`, and
  `preview_layers` (PreviewOnly).
- **Which preset paints each environment.** For each environment, it uses
  the preset of the first region (by key) of that environment: generator
  override, else the env file, else the built-in. Its first enabled
  `terrain_material` stage is resolved, along with the `polyhaven_textures`
  stage that stage names.
- **How preset textures are fetched (decision).** The stage imports the
  preset's four layers itself, with `phimport.ImportTextures` (the textures
  stage's importer, the same disk cache and User-Agent), at most once per id.
  The textures stage is **not** made PerRegion. A textures stage places
  nothing, its downloads are shared, and expanding it would put eight
  `r_<region>_tex` stages in every world doc for no gain. This was the
  simplest robust path.
- **Weights.**
  1. `groundWeights(T, preset params, envSeed)` is the terrain_material
     stage's own function, unchanged, run over the WHOLE union terrain.
  2. Each result is multiplied by `mask_biome_<env>`.
  3. Each environment's four layers map into a DEDUPED global list. The
     dedupe key is Poly Haven id + scale + triplanar, so the same texture
     at the same repeat and projection is one layer.
  4. The sums are normalised per sample.
- **Output form.**
  - With ≤ 4 layers, the stage writes a four-layer splat.
  - With 5–8, it writes `weights2` and an eight-layer splat.
  - With more than 8, it fails with an error that names every layer (env,
    role, id, scale).
- **Material-level knobs** (blend, normal strength, AO, strata, bedding,
  wall normal, macro, varnish, detail) come from the **lead** environment's
  params (the lead region's, as terrain.yaml's strata are). Every
  triplanar layer not of the lead environment gets `layerN_strata: false`.
  Per-layer values are each environment's own, and a shared layer keeps
  the first environment's.
- **Outputs.**
  - `weights.png` and `weights2.png`.
  - `ground.png`: the tinted layer means by weight.
  - `height.png`: 16 bits.
  - `strata.png`: the lead's.
  - `far_weights.png` (+ `far_weights2.png`).
  - `region_material.yaml`: a `TerrainMaterialArtifact` with the new
    omitempty fields `weights2`, `far_weights2` and `layers`.
  - `materials/gen_<doc>_<stage>.fmat` and `_far.fmat`.
- **Far variant.** For each environment, `farGroundWeights` (extracted
  from terrain_material_far.go, unchanged arithmetic) is run with that
  environment's params. Each environment's share is its biome weight at the
  **nearest union sample** to each far lattice point, which matches
  `terrain.UnionFar`'s height blending. `farMaterialTweaks` (extracted) is
  then applied.
- **Preview.** Layers in eight false colours, with a note naming each
  colour, its layer and its share. Also the ground colour, per-layer grey
  maps on request, and the terrain meshes in ground colour. The preview
  never downloads: it reads the provenance and textures already on disk.
- **HashExtra.** It folds in each environment's preset `terrain_material`
  and textures params. An unreadable preset hashes as its error, so the
  plan still works and the run reports it. Editing a preset's ground
  reruns only `ground_mat`. This was tested, and it was also seen in the
  scratch run: only `ground_mat` reran after the savannah edit.
- **region_terrain default material.** `regionTerrainAutoMaterial`: when
  `material` (or `far_material`, with a backdrop) is empty, the tiles wear
  the first enabled `region_material` that names this terrain. This is
  folded into region_terrain's HashExtra **only** when it applies, so no
  existing hash moves. The **level_world template** is now regions →
  ground (`material: gen_<doc>_ground_mat`) → `ground_mat` (region_material).
- **Refactors in shared code** (no output change):
  `terrainMaterialExtras` (terrain_material.go), `farGroundWeights` and
  `farMaterialTweaks` (terrain_material_far.go), and `loadRegionPreset`
  (region_material.go, now used by `loadPresetTerrain`).

### 3. The savannah preset's ground (environments_builtin.go)

- **Textures stage.** A `polyhaven_textures` stage `tex` holds four layers.
  Their ids were checked against the live Poly Haven catalogue with
  `foundry polyhaven search/info` (cached, Foundry User-Agent):

  | Layer | Poly Haven id | Scale |
  | --- | --- | --- |
  | grass | withered_grass | 2.5 m |
  | baked | dry_ground_01 | 4 m |
  | stony | dry_ground_rocks | 3 m |
  | rock | rock_boulder_dry | 6 m |

- **Rules.** A `terrain_material` stage `ground_mat` maps
  sand → grass, cracked → baked, gravel → stony and rock → rock. Its
  settings:
  - rock slope 30–44°;
  - washes only on the main drainage (`wash_flow` 0.55–0.85, width 2.5 m);
    the first try paved 25% of the plain in stony soil;
  - baked earth on flats in patches;
  - low layer match;
  - no strata and no varnish;
  - macro 0.22 at 60 m;
  - anti-tile on.

  Coverage on the scratch world's savannah side: grass 26%, baked 11%,
  stony 12%, rock 2% (of the whole union).
- **Not yet.** Phase 6 tunes it. The grass reads pale straw from a
  distance and not yet like grass.

## Formats

The `.fmat` for a two-biome world (the scratch run's) holds
`weights` / `layer1..4` as before, then `weights2`, `layer5..8` with their
settings, and `layer8_strata: false` (the savannah's rock). The full file is
`$S/proj3/resources/materials/gen_seam_world_ground_mat.fmat`.

region_material.yaml adds to terrain_material.yaml's fields:

```yaml
weights2: generated/seam_world/ground_mat/weights2
far_weights2: generated/seam_world/ground_mat/far_weights2
layers:
- {name: ph_red_sand, id: red_sand, role: sand, environments: [desert_canyon], scale: 3, coverage: 0.159}
- ...
- {name: ph_rock_boulder_dry, id: rock_boulder_dry, role: rock, environments: [savannah], scale: 6, triplanar: true, coverage: 0.018}
```

`coverage` in that file is per ROLE (sand, cracked, gravel, rock) summed
over environments, so a rock's colour match still finds sand and rock.

## Tests

Run with `export GOFLAGS=-p=6 GOMEMLIMIT=6GiB`:

```
go build ./...                                                   clean
go vet ./internal/material/ ./internal/gfx/ ./internal/render/ ./internal/procgen/...   clean
go test ./internal/procgen/... ./internal/material/... ./internal/world/... ./internal/gfx/ ./internal/pack/...   all ok
go test ./internal/ -run TestPackagesDoNotDependUpward           ok
go test ./internal/editor/...       fails only TestScoreSibeliusKeymap, TestScoreRebinding (the score session's)
FOUNDRY_GL_TEST=1 go test ./internal/render/...                  see "GL results"
FOUNDRY_GL_TEST=1 go test ./internal/gfx/...                     see "GL results"
```

New tests:

- **material**: none new. The existing resolve tests pass, and the
  eight-layer resolve is covered by the GL and stage tests.
- **gfx**
  - `TestSplatPackGrid`: the 2×2 grid is byte-equal to `packSplatAtlases`,
    and the 4×2 puts layer n at (n%4, n/4) with its gutter wrapped.
  - **GL** `TestSplatAtlasEightLayers`: six distinct layers over eight names
    give Cols 4, tiles `[0 1 2 3 4 5 -1 1]` and a 1088 × 544 RGBA8 atlas
    with every sampled texel and height right. BC7 gives the same geometry
    at PSNR **44.2 dB**, recorded as BC7. Four names in an eight-name call
    give the same 2×2 object as `SplatArraysFor`.
- **render**
  - `TestFxUnitsAreFree` now includes unit 31.
  - **GL** `TestMaterialFxSamplersFitTheMaterialPass` was updated
    deliberately: 8 fx samplers (was 7), **31 of 32** in the material pass,
    and the driver allows 32.
  - **GL, GATE (a)** `TestMaterialPassBuildsTheEightLayerVariant`: the
    generic material program builds with the eight-layer branch. The
    variant for `splat|splat8` builds. The four-layer variant builds and
    folds `FX_SPLAT8` to `false`. `visShadeKey` sets the bit only for
    eight-layer splats.
  - **GL, GATE (b)** `TestMaterialFxEightLayerSplatOnEveryPath` uses three
    rocks: a four-layer red splat; an eight-layer splat with six distinct
    layers (4×2 atlas) weighted entirely on layer 6 (blue); and a
    **two-biome tile**, half on layer 1 (red) and half on layer 6 (blue).
    It runs on the vis page pool, the vis generic program, the vis flat
    pool, forward and classic. All five paths agree **exactly**
    (`[194 28 29] [27 36 208] [114 31 116]`).
- **procgen/stages** (fake Poly Haven):
  - `TestRegionMaterialBakesTwoBiomesIntoEightLayers`: two file presets, 7
    layers (sand shared). Coverage sums to 1, every texel's 8 weights sum
    to 1, and each region's own layers are absent deep in the other. The
    largest column-to-column step of the hills' share across the band is
    0.078 (a seam would be about 0.5). Exactly one of the two rock layers
    is banded by the strata.
  - `TestRegionMaterialFallsBackToFourLayers`.
  - `TestRegionMaterialFarBackdropAndAutoMaterial`: a cooked far backdrop
    gives an eight-layer `_far` with no detail. The savannah's own layers
    weigh ≥ 0.3 on the backdrop's east edge and ≤ 0.05 on its west. The
    tiles, far tiles and strips wear the auto materials.
  - `TestRegionMaterialRefusesMoreThanEightLayers`: 12 distinct layers are
    refused with a named list, and 8 fit.
  - `TestRegionMaterialHashFollowsThePresetsGround`: a preset ground edit
    moves `ground_mat`'s hash and not `ground`'s. Also Shared/SharedFor.
  - `TestSavannahPresetHasAGround`.
  - `TestRegionMaterialPreview`: the preview with no download — false
    colour, ground colour, one grey map per layer, and the
    "7 layers (eight-layer splat)" note.
  - `TestLevelWorldTemplate` and `TestLevelWorldGrowsARegionTerrain` now
    expect three stages and the material slot.
- **procgen/gencli** `TestGenWorldEnvAndRunSync` runs `-stage ground`. The
  template's region_material would download textures in a test.
- All goldens still pass, unchanged: `TestTerrainStageGolden`,
  `TestGenerateGolden`, `TestGateOneRegionIsTheClassicTerrain` and
  `TestDesertCanyonHashesUnchangedByRegions`.

### GL results

Both suites ran on a real context (WSLg, Mesa 26.2.3 d3d12 on the GTX
1070).

- `FOUNDRY_GL_TEST=1 go test ./internal/gfx/...`: **ok**.
- `FOUNDRY_GL_TEST=1 go test ./internal/render/...`: the full run hit the
  2400 s timeout after 191 of 267 tests. The 76 it did not reach were run
  on their own and all passed (144 s). Of the 191, two failed:
  - `TestNaniteFoliageDrawsTheClassicNearField`: the known failure
    recorded in docs/canyon/PHASE_5.md.
  - `TestNaniteStreamStreamsInFromTheRoots`: "frame 3 (6 of 52 pages):
    2268 pixels show a back face through a hole". This is a geometry
    streaming test with no material or effect in it, and this phase
    touched nothing it draws. It was not established whether it predates
    this phase.

  All the material tests passed: every-path, eight-layer, sampler budget
  and variant.

## GATE (c): the headless render at the seam

**Scratch project.** `$S/proj3/resources` holds:

- symlinks to the repo's `shaders`, `fonts` and `sounds`, which are only
  read;
- copies of `textures`, `materials`, `biomes`, `ui`, `particles`, `models`,
  `entities`, `scripts` and `game.yaml`;
- a level `levels/seam.yaml` with two region volumes: `canyon_west`
  (desert_canyon, x −384..0) and `savannah_east` (savannah, x 0..384),
  z ±256, both with a 200 m transition.

**Generation.**

- `foundry gen world seam` made the doc. It was edited to 1 m resolution,
  128 m tiles, cook_memory 2000 and a 1500 m backdrop at 8 m, plus the
  desert template's `environment` stage (sun, sky, fog, spawn).
- `foundry gen run seam_world -workers 8` took **25 s wall, peak RSS
  324 MB**. The union was 768 m square at 1 m. `ground_mat` took 11.4 s the
  first time (four savannah textures downloaded, 42 MB) and 1.8 s on its
  rerun.
- The atlas built in the engine at 1024² a layer, **4352 × 2176**, in
  1.08 s (RGBA8; compression off by default).

**Render.** It ran through `FOUNDRY_BENCH`, 1600 × 900, TAA, the
visibility buffer and VSM, with `XDG_DATA_HOME` pointed into the
scratchpad so the user's recent-project list was not touched:

```
cd $S/proj3 && XDG_DATA_HOME=$S/xdg FOUNDRY_BENCH="level=seam;compiled=0;frames=30;warm=60;size=1600x900;shots=<dir>;out=<dir>/bench.txt;at=<x,y,z,yaw,pitch>|..." \
  $S/bin/foundry -project $S/proj3
```

**Pictures looked at** (copied to `$S/p3/`):

- `seam_east.png`, `seam_west.png`: standing in the band at (0, 60).
- `desert_6m_east.png`: 6 m up on the desert side.
- `overview_along_seam.png`: 55 m up, looking along the seam.
- `savannah_wsw.png`, `savannah_gully_west.png`.
- `above_desert_east.png`, `above_savannah_west.png`: 25–30 m up, looking
  across the band.
- `band_ground.png`.
- `layers_falsecolour.png` and `ground_colour.png`: the baked maps.

**What they show.**

- The red sand, pebbles and cliff rock give way to dry grass, baked earth
  and stony soil over the meandering 200 m band.
- The layers meet by their height maps: gravel stones poke through grass,
  and baked plates fade into sand.
- No picture shows a straight line or a hard colour step at the biome
  boundary. The false-colour weights show a soft, meandering hand-over
  with no straight edge.
- The desert mesas that reach into the band keep their banded cliff rock,
  and the savannah's pale boulder rock takes over on the slopes the band
  gives to the savannah. Seen from 55 m this reads as a change of rock
  along the mesa foot, not a cut.

**Iteration.** The first run's savannah was a quarter stony washes, a
dendritic carpet (`$S/shots/a/layers.png`). The rules were retuned
(`wash_flow` 0.55–0.85, `wash_width` 2.5) and the rest reused.

## Measured numbers

**Frame cost of the eight-layer splat.** The poses were the seam,
both ways; the savannah toward the band; and 28 m up over the band. Runs
were interleaved A/B, 90 frames after 60 warm-up frames. The B side,
`FOUNDRY_FX_MASK=-129`, clears `FX_SPLAT8` only: the same scene and
atlas, drawn with layers 1–4 alone, and its vis variant compiles 5–8
out.

| Pose | 8 layers (run 1 / 2) | layers 5–8 off (run 1 / 2) |
| --- | --- | --- |
| seam, east | 23.41 / 23.60 ms | 20.34 / 20.50 ms |
| seam, west | 27.18 / 27.29 ms | 23.92 / 24.04 ms |
| savannah | 25.16 / 23.56 ms | 22.02 / 21.90 ms |
| over the band | 23.08 / 22.95 ms | 21.03 / 20.93 ms |

- That is about **+2–3 ms p50 on WSLg**. The figure is an upper bound on
  the extra layers' shading cost: with 5–8 off, savannah pixels have no
  layer to sample, return early, and shade nothing.
- **GPU timer queries read 0 on Mesa d3d12** (as in every earlier phase),
  so there is no per-pass GPU number. WSLg frame times are not the card's;
  only the GTX 1070 bench under Windows can judge (docs/canyon/PERF.md).
- **Four-layer materials** (every existing level) take neither the new bit
  nor the new samplers. In the visibility buffer their variant compiles
  the 5–8 code out. In the forward/classic program it is a uniform branch
  plus a few extra ALU ops in `fxAtlas` (a division by `fxSplatCols`).
  That last part was not measured: no "before" binary was benched, since
  this tree cannot be checked out.

## Found on the way (not this phase's)

**A tile-edge NORMAL seam in the cooked terrain.** From 28 m up there is a
straight line along z = 0, a 128 m tile boundary: the shading jogs across
it. It is not material:

- it is there with every effect off (`FOUNDRY_FX_MASK=0`, the normal-colour
  view);
- it is there with Nanite LOD off (`FOUNDRY_NANITE_LOD=0`);
- it is there with the visibility buffer off (`FOUNDRY_NANITE_VISBUFFER=0`).

All three are in `$S/p3/tile_edge_normals_fx_off_lod_off_vis_off.png`.
The likely cause is the per-tile cook simplifying each tile's interior
independently against a locked border, so a ridge crossing the border is
decimated differently on each side. That is the same tile path the classic
terrain stage uses, and it is not a region or material issue. It is worth
a look in a terrain phase.

## Deviations

- **Eight-layer mode of `splat`, not a separate `splat8` effect.** The
  4-layer splat is unchanged in its file format, resolved state, atlas key
  and pixels, and vis variant. The forward program carries the eight-layer
  code behind a uniform branch, not a separate compile.
- **Textures are imported by region_material, not by expanded textures
  stages** (see above).
- **One preset per environment.** Two regions of the same environment with
  DIFFERENT generator overrides share the first one's (by key) ground
  material, because biome masks are per environment. The run does not
  warn about this yet.
- **Material-wide knobs are the lead environment's.** If the savannah were
  the lead, the desert would lose its varnish and bedding and its rock
  would not be banded. The scratch world's lead is the desert.
- **The desert's varnish streaks apply over the whole material**,
  including the savannah's steep ground.
- **`coverage`** stays per role (4 numbers), and the per-layer shares are
  in `layers`.

## Known gaps

- **Placement consumers of a ground** (`scatter`, `cliff_wall`,
  `wall_sheet`, `drift`) still list only `terrain_material` in their
  `ground` param's kinds, and two of them assume four layers:
  - `wall_sheet` builds a pure-rock weights map on channel 4;
  - `drift` rewrites the four-channel weights with sand in channel 1.

  Pointing them at a region_material needs phase-4 work (expanded
  placement stages are rewritten to it through `SharedFor`, so it will
  come up there).
- **The desert's 17% gravel at 1 m** (dendritic washes over its flats in
  the union) is the desert preset's own rules at this resolution, unchanged
  by this phase.
- **The savannah ground is a first pass.** The grass reads as pale straw
  from a distance. Phase 6 owns it, with plants.
- **An existing project's** `resources/environments/savannah.fgen` (the
  phase-1 stub) is never overwritten, so it has no ground material. A
  world with a savannah region then fails `ground_mat` with "the preset
  has no enabled terrain_material stage". Delete the file and run
  `foundry gen env write savannah` (as was done here).
- The material editor shows the splat's (now ~80) parameters flat.
- **Not tried in the editor's Generator window by hand.** The preview runs
  headless in `TestRegionMaterialPreview`; nobody has looked at it in the
  2D view.
