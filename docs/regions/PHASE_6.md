# Environment regions — phase 6: the savannah environment

Status: built, tested, generated end to end and measured on the GTX 1070
natively, 2026-10-12. The gate's seam half holds: renders from nine poses
across the canyon-to-savannah join show no straight lines or hard steps. The
frame-budget half does **not**: the world runs at 24–34 ms p50 (29–41 fps)
at the user's 2560×1080, not 16.7 ms. The costs are measured and ranked
below, and the defaults were chosen against them. Nothing was clicked in a
running editor (headless tests only).

`$S` below is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.
Native reports are in [perf_phase6/](perf_phase6/).

## 1. The vegetation freeze: outcome (a) — no freeze, natively or on WSLg

**What was tested.**

| Where | World | Poses | Result |
| --- | --- | --- | --- |
| WSLg (Mesa d3d12) | `desert_canyon` template, fresh, `plants` (biome volumes, 131) **on** (`$S/proj7`) | 7: SPAWN, FLOOR (down-canyon), WALL, SUN, two down-canyon views at the east end, OVER | all completed, p50 50–183 ms |
| WSLg | same, with the PERF.md fixes switched back to the old paths (`FOUNDRY_NANITE_VIS_DENSE=0`, `FOUNDRY_NANITE_DRAW_LOD_BOUND=0`, `FOUNDRY_NANITE_MAT_VARIANTS=0`) | SPAWN, FLOOR | completed, 83–93 ms |
| WSLg | same, plus the old 256 MB page pool and no VSM rect budget | SPAWN, FLOOR | completed (pool thrashing: 1,232–2,512 pages uploaded) |
| WSLg | canyon_savannah, BOTH regions' vegetation on (465 volumes) | 8 | completed, 134–302 ms |
| **GTX 1070, native** (dev exe cross-built with mingw, PERF.md's method) | the desert canyon above | the same 7 | completed, **p50 20.1–27.2 ms**; `perf_phase6/dcveg.txt` |
| **GTX 1070, native** | canyon_savannah, grass and acacias on (every run below) | 8–9 | completed |

**Foliage cost, native (F3 GPU, ms).** In the desert canyon, the camera's
foliage pass costs 1.2–1.7 ms and its shadow 0.6 ms, with 38–117 plant
clusters. In the final canyon_savannah world at 2560×1080 the camera's pass
costs 1.7–3.6 ms and its shadow 0.6 ms, with up to 756 plant clusters and
34k references (`fin_user.txt`).

**Reading.** The stall does not reproduce in this tree anywhere, so it was
not root-caused. Natively it is simply absent. On WSLg it is also gone,
even with the PERF.md fixes reverted by their switches. So it was either
fixed between phase 7 and now by something without a switch, or it needed
phase 7's exact world. Candidates for the first: the Nanite foliage cull
rewrite (memory note "nanite-foliage-cull-scaling") and the draw bound and
dense vis numbering. The second would be the 570 scanned cliff_wall chunks
that phase 8 replaced. One Mesa-only hazard is still on record: dense floor
plants on the Nanite impostor path hung d3d12 in the rainforest (memory
note "rainforest-biome"). The savannah biome's grass is not on that path
(its grass shapes are classic tiers), and only its acacias are Nanite.

**Decision.** The savannah preset ships its grass ON. No WSLg-only safeguard
was added: there is nothing reproducible to guard against. The
`desert_canyon` template's own `plants` stage is left off and unchanged,
because its hashes are pinned. It could be turned on now.

## 2. What was built

### The savannah preset (procgen/stages/environments_builtin.go)

**Terrain: wide rolling plains.**
- Relief 45 m, base frequency 0.0013 (swells ~800 m long), 5 octaves, warp
  140 m. There are no mesas and no canyons.
- Drainage strength 2.2, basin fill 0.12, hillslope creep 0.15. This gives a
  dendritic net of shallow, soft-sided dry washes.
- **Droplets 0.** On slopes this low the droplet trails piled into parallel
  N–S streaks, a comb in the relief (`$S/p6/e_cmp.png`, droplets on vs
  off).
- Talus 26°, detail 0.1.
- Savannah slopes: p50 5°, p95 13.8°, max 27°. The old stub was nearly flat
  (±4 m, p95 3.4°).

**Ground.**
- The same four Poly Haven textures as before.
- New `sand_tint` (0.88, 0.78, 0.55) on the grass layer: withered_grass no
  longer reads as pale straw from 100 m, but as the gold of a dry season.
- Baked-earth cover is 0.3 (was 0.45).

**Grass at scale.** The `grass` vegetation stage is now ON, with two rules:
- `grass`: the engine's `savannah` biome (grass, tall grass, a few dead
  bushes; rocks and acacias off), density 0.7, slopes to 22°.
- `acacias`: the same biome with only its acacia (the engine's own acacia
  shape is the stand-in; Poly Haven has none). It uses cluster 1 at 70 m
  and threshold 1.7, so it grows only where the cluster noise is near its
  top. That gives clumps over ~8% of the plain (74 volumes over 42,000 m²),
  at density 2 (~10 m apart inside a clump), with open grass between.
- `fray` 12 m (below).

**Scanned trees and shrubs.**
- Searsia and quiver tree clumps (168) as before.
- Shrubs at density 0.25 (was 0.5), giving 1,509.
- Wash stones capped at 900 (was 2,500).
- 399 granite boulders.

**The desert preset in a region world.**
- `DesertCanyonPreset` now passes through `regionBudget`, which sets wash
  stones to 1,200 (template 3,000) and bushes to density 0.6 (template 1.2).
- The `desert_canyon` template itself is unchanged, and its pinned hashes
  pass.

### The `canyon_savannah` template (procgen/stages/canyon_savannah.go)

- **regions stage.**
  - Two regions: `canyon_west` (desert_canyon, x −512..0) and
    `savannah_east` (savannah, x 0..512), both z ±384.
  - The transition is left at the default, so the adjacency table's 200 m
    applies (tested).
  - Resolution 1 m, 128 m tiles, global erosion.
  - `seed_level: true`.
- **ground (region_terrain).** `lead: canyon_west`, `flow_mask: mfd`, and a
  2 km backdrop at 8 m. The tiles wear the region material automatically.
- **ground_mat.** region_material.
- **air (environment).** The compromise between the two environments:
  - from the canyon: its sky, sun (yaw 50°, pitch −30°, 1.5) and HDR at
    exposure 0.75;
  - aerial distance 2.2 km (desert 1.7);
  - height fog at half the density (0.003), with falloff 0.08 and a golden
    fog colour;
  - ambient 0.4;
  - view distance 4 km;
  - spawn at (300, 40), yaw 160°: on the savannah, looking at the mesas over
    the canyon's mouth across grass and an acacia clump (checked by render,
    `$S/p6/spawn_cmp.png`).

**One command.** `foundry gen new canyon_world -template canyon_savannah`
then `foundry gen run canyon_world` is the whole flow. The new
`genlevel/regions_seed.go` makes this work.
- The regions stage's `seed_level` switch is an `Added` param, so it is
  unhashed at its default.
- While the switch is on and the target level has no region volume, the
  run's sync writes the doc's list into the level as region volumes. It
  makes the level if needed.
- Each footprint is stored relative to its volume, and the box is fitted.
- The switch is then cleared, and the doc on disk records it.
- A level that already has regions keeps them, and the switch just clears.

The paths:
- **`gen run`** writes the level file.
- **`gen check`** says what a run would write and plans as the run would
  (in memory).
- **The editor**, with the level open, queues the write onto the main
  window (one `EditorPushUndo`, with volumes loaded) and syncs. The
  half-second tick never wipes the layout, and a run started before the
  write lands is refused with "run again". With the level closed, the run's
  sync writes the file (editor_generator_region_seed.go).

### Engine and stage changes found on the way

All additive, and all off for existing content.

**1. The eight-layer splat in two passes** (resources/shaders/surface_fx.glsl).

- **The cost.** Natively the eight-layer material pass cost about twice the
  four-layer one, even on desert pixels whose layers 5–8 weigh nothing.
  - Holding every scored layer's albedo and normal until `best` was known
    kept 16 vec4s live across 8 inlined samples, so the variant ran at low
    occupancy.
  - Now pass 1 scores each candidate from its height alone. The compiler
    drops the normal fetch, the frame, the bedding and the strata.
  - Pass 2 samples in full only the layers within the blend of the best,
    adding each to the sums in the old order.
  - Where a single layer is a candidate (most of a wall, most of a plain),
    pass 1 samples nothing: its score cannot change the result.
- **Exactness.** The arithmetic per layer and the summation order are
  unchanged. The GL tests that pin exact colours on every path pass:
  `TestMaterialFxEightLayerSplatOnEveryPath` ([194 28 29] / [27 36 208] /
  [114 31 116]), `TestMaterialFxOnEveryPath` and the variant tests.
- **Native result**, material pass, same world and poses (`orig3` →
  `twopass`):

  | Pose | Before | After |
  | --- | --- | --- |
  | savannah → canyon | 31.0 ms | 18.2 ms |
  | band → canyon | 35.4 ms | 21.9 ms |
  | savannah grass | 21.0 ms | 12.2 ms |

  The single-candidate skip took another 0.5–2 ms (`n2` → `n2only`).

**2. `region_terrain` `lead`** (terrain.Union.Lead). This picks the lead
region by hand. Auto picked the savannah here: it owns as many samples, and
the padding rows went its way. With the savannah leading, the whole world
eroded with its numbers (fill, droplets) and the material took its
material-wide look. Tested: auto, set, and unknown (refused).

**3. `region_terrain` `flow_mask: mfd`** (terrain.Union.FlowMFD,
masks.go). The problem it fixes is on the flats.
- The canyon's mouth and the band are broad flats. The classic D8 flow mask
  crossed them in ruled 0°/45° lines (`$S/p6/flow_a2m.png` before,
  `flow_a2m3.png` after), which the washes and the wash stones would have
  drawn.
- MFD accumulation fixes this (the fluvial pass already had it).
- The mask is cut under more than 0.1–0.8 m of fill (a playa is not a
  channel) and on ground flatter than about 0.5°.
- Heights are identical. The level_world template now sets it too.
- Test: ruled samples drop from 48,506 (d8) to 9,827 (mfd).

**4. `vegetation` `fray`.** Each volume grows by 0.625 × fray past its
cells and gets `edge_fade_distance: fray`.
- Two neighbours then overlap by 1.25 × fray, and their smoothstep edge
  fades are complementary: S(t) + S(1.25 − t) = 1. Their densities
  cross-fade instead of stepping at a straight line.
- The outer edges are ragged.
- At 0 the output is classic and the hash unchanged
  (`TestClassicPlacementsPinned` passes).

**5. `terrain_material` `sand_tint`.** It multiplies the sand layer's tint,
in region materials too. Tested.

## 3. Files

**New:**
- `procgen/stages/canyon_savannah.go`
- `procgen/stages/canyon_savannah_test.go`
- `procgen/genlevel/regions_seed.go`
- `procgen/genlevel/regions_seed_test.go`
- `procgen/gencli/gencli_template_test.go`
- `editor/editor_generator_region_seed.go`
- `editor/editor_generator_region_seed_test.go`
- `docs/regions/perf_phase6/*.txt`

**Changed:**

| Area | Files |
| --- | --- |
| Shaders | `resources/shaders/surface_fx.glsl` |
| `procgen/stages` | `environments_builtin.go`, `level_world.go`, `regions.go` (`seed_level`), `region_terrain.go` (`lead`, `flow_mask`), `vegetation.go` (`fray`), `terrain_material.go`, `region_material_run.go` (`sand_tint`) |
| `procgen/stages` tests | `regions_test.go` (preset check reads the terrain stage), `region_terrain_test.go` (seam p99 tolerance 0.25°: both sit at the 80° talus cap, now 80.7° each) |
| `procgen/terrain` | `union.go`, `field.go`, `masks.go` |
| `procgen/regions` | `names.go` |
| `procgen/gencli` | `gencli_regions.go` (seeding in `syncRegions`) |
| `editor` | `editor_generator_regions.go`, `editor_generator_job.go` |
| Docs | `docs/guide/35-regions.md` (quick start, the environments, the knobs), `docs/guide/33-generators.md` |

No score or tracker file was touched.

## 4. Tests and commands

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                                     clean
go vet ./internal/procgen/... ./internal/editor/                   clean
go test ./internal/procgen/... ./internal/world/... ./internal/levelio/...   all ok (stages 77 s)
go test ./internal/editor/...     fails only TestScoreSibeliusKeymap, TestScoreRebinding (the score session's)
go test -count=1 ./internal/ -run TestPackagesDoNotDependUpward    ok
FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'TestMaterialFx|TestMaterialPassBuildsTheEightLayerVariant|TestVisMaterialProgramBuilds|TestMaterialVariants|TestSceneBlockMatchesTheShader|TestFxUnitsAreFree'
                                                                   ok (122 s, WSLg Mesa d3d12)
```

**New tests:**

- **genlevel:**
  - Seeding writes the template's regions into an empty level. A trigger
    named `canyon_west` already there makes the new region `canyon_west_2`.
    The relative footprint and fitted box are right, and syncing reads back
    the template's world footprints.
  - A level with regions is left alone.
  - The cleared switch hashes like a plain regions stage, and the switch
    when on does not.
  - `SeedLevelFile` makes the level, and a second seed writes nothing.
- **gencli** `TestCanyonSavannahTemplateSeedsItsLevel`, the real CLI:
  - `new`, then `check` reports what a run would write and writes nothing;
  - `run -stage regions -no-splice` writes 2 volumes, clears the switch on
    disk and syncs;
  - a second run does not touch the level.
- **stages:**
  - `TestCanyonSavannahTemplate`: valid, plans, two regions, 200 m pair
    band, lead and mfd, spawn.
  - `TestSavannahPresetGrowsGrassInClumpsAndFrays`: also checks that the
    desert preset is lighter while the template is unchanged.
  - `TestRegionTerrainLeadIsChosen`
  - `TestMFDFlowMaskHasNoRuledLines`
  - `TestVegetationFrayGrowsAndFadesVolumes`
  - `TestSandTintTintsTheSandLayer`
- **editor:**
  - With the level open: the tick waits, the queued write lands, there is
    one undo step, and the doc syncs.
  - With the level closed: the run's sync writes the file.

**Not run:** the full GL render suite (40+ min on WSLg). The tests that
exercise the changed shader were run by pattern, as above. The known
failures `TestNaniteFoliageDrawsTheClassicNearField` and
`TestNaniteStreamStreamsInFromTheRoots` were not re-run. No foliage or
Nanite code was touched.

## 5. The scratch world and the gate

**Generation.** Fresh project `$S/proj6f`, from scratch:

```
foundry gen new canyon_world -template canyon_savannah -root $S/proj6f
GOMEMLIMIT=6GiB foundry gen run canyon_world -workers 8 -root $S/proj6f
```

- **Time and memory:** 4 min 48 s wall, peak RSS 576 MB. Of that, the
  canyon's rockface took 3 min 46 s, the terrain 25 s, and the
  region_material 2.7 s.
- **Output:** `canyon_world: wrote 2 region volume(s) into level canyon …
  ran 19 … 10385 placements`.
- **Placements by region:** canyon_west 6,518 scatter and 405 wall sheets;
  savannah_east 2,976 scatter plus 278 vegetation volumes (204 grass, 74
  acacia); 208 terrain, backdrop and stitch tiles.
- **Union:** 1,024 m square at 1 m, heights −7.3…131 m, 1.57 M droplets
  (the desert lead's), desert lead.

**Iteration** (`$S/proj6`, the same world while tuning): `$S/p6/veg1`,
`g2`…`g7*` and `sx_*`/`e_*`/`flow_*` (relief and flow-mask pictures).

Each version was rendered and looked at, and the next fix came from what
the renders showed:

| What the renders showed | Fix |
| --- | --- |
| A nearly flat savannah | the terrain retune |
| Acacias evenly spread over the whole plain | the clump rule |
| Pale straw at distance | `sand_tint` |
| Ruled D8 wash lines and a 300 m filled pan raying from its spill point (the canyon's trench ends in the savannah) | MFD plus the fill and flat cut |
| Desert erosion numbers lost to the savannah lead | `lead` |
| A dark straight-edged patch of grass | investigated: hidden GI, hidden sun shadow (the hide key did not take), no fog, no volumes, no wall sheets. A heightmap ray cast proved it is the shadow of a 112 m mesa at (−71, −36), with the sun 30° up. Natural; kept |

**Gate renders** (WSLg, 1600×900, TAA, vis buffer, VSM; looked at):
`$S/p6/gateF/pose_0..8.png`, sheets `sheet_a/b/c.png`.

| Pose | Eye (x, z), yaw | View |
| --- | --- | --- |
| 0 | (−112, 68), 10° | desert floor → savannah (canyon floor, grass appearing at the horizon) |
| 1 | (250, 60), 180° | savannah → canyon (grass to the mesa foot) |
| 2, 3 | (25, 150) | standing in the band, east and west |
| 4 | (50, −250), 90° | along the escarpment in the band |
| 5 | (150, −250) at 30 m | over the band toward the mesas |
| 6 | (60, −380) at 30 m | along the seam |
| 7 | (300, −200) | savannah grass with an acacia clump |
| 8 | spawn | |

No pose shows a straight line, a hard colour step or a density step at the
join:
- sand gives way to grass over the meandering band;
- the canyon's walls keep their banded rock and stand in grass at their
  foot;
- the acacia clumps read as clumps.

**Frame budget, native GTX 1070** (dev exe, 60 warm-up + 60 measured
frames, PERF.md method). All times are ms.

User settings, 2560×1080 (GI off, 1024 MB pool; `fin_user.txt`):

| Pose | p50 | p95 | CPU | GPU | material | scenery | occlusion | foliage | sun |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| desert → savannah | 24.3 | 24.9 | 13.4 | 24.1 | 10.8 | 5.6 | 4.3 | 2.3 | 0.0 |
| savannah → canyon | 32.1 | 33.3 | 13.8 | 32.0 | 18.4 | 5.0 | 2.9 | 2.8 | 1.7 |
| band, east | 26.0 | 27.5 | 13.1 | 26.0 | 12.0 | 4.8 | 3.2 | 3.1 | 1.8 |
| band, west | 34.4 | 35.4 | 13.1 | 34.2 | 24.3 | 3.2 | 2.0 | 2.0 | 1.5 |
| escarpment | 30.3 | 32.0 | 14.2 | 30.3 | 14.6 | 5.8 | 4.3 | 3.0 | 1.5 |
| 30 m up, band | 29.9 | 30.7 | 13.3 | 29.8 | 20.1 | 3.4 | 2.5 | 1.7 | 1.0 |
| 30 m up, seam | 34.0 | 35.0 | 14.5 | 33.9 | 17.8 | 7.0 | 4.6 | 2.5 | 0.9 |
| savannah grass | 28.8 | 29.6 | 13.8 | 28.6 | 12.8 | 5.3 | 4.4 | 3.6 | 1.4 |
| spawn | 25.3 | 26.8 | 12.8 | 25.2 | 12.4 | 3.6 | 3.3 | 3.2 | 1.5 |

Default settings, 1920×1080 (GI medium, ~2.5 ms; `fin_def.txt`):
p50 24.5–32.4, CPU 12.8–15.0, material 8.8–21.1.

WSLg, 1600×900 (not the card's numbers; GPU timers read 0): p50 124–265.

**Where the time goes, and what the defaults chose**:

1. **The material pass.** It is the biggest cost: 11–24 ms at 2560 and
   GPU-bound.
   - Measured with FX masks (`n1fx-*`):
     - no splat at all: 5–7.4 ms;
     - the four-layer variant (layers 5–8 compiled out): 8–19.8 ms;
     - eight layers: 12.9–25 ms.
   - The desert's own four layers cost up to ~12 ms in canyon views (a
     triplanar anti-tiled cliff layer with bedding and strata). The
     eight-layer residual over the four-layer one is about 4–6 ms.
   - Texture compression (BC7) saves ~1 ms (`n1bc7`).
   - The two-pass rewrite above took 9–13 ms off.
2. **The CPU frame.** Before the placement budget the CPU frame was 19–21
   ms at 16.6k placements. Halving the small pieces (7.9k, `n1lite`) gave
   11–12 ms CPU and 2–4 ms less GPU. That is about 1 ms per thousand
   placements, mostly VSM and Nanite scenery bookkeeping per instance.
   - The defaults now place 10.1k: savannah stones 900, shrubs 0.25; the
     desert preset's wash stones 1,200, bushes 0.6.
   - CPU is 12.8–14.5 ms, now under the 16.7 ms budget.
3. **Scenery + occlusion** (Nanite raster of scans and walls): 5–11.5 ms.
4. **Foliage** (the grass): 1.7–3.6 ms camera plus 0.6 ms shadow.

**History at 2560×1080, user settings** (the sav1 world is the older
savannah terrain, so it is comparable in kind, not pose for pose):

| Run | Change | p50 |
| --- | --- | --- |
| `sav1` | 16.6k placements, one-pass splat | 39–48 ms |
| `n1` | two-pass splat | 28–40 ms |
| `n2` | placement budget | 25–35 ms |
| `n2only` / `fin_user` | single-candidate skip | 24–34 ms |

**Verdict: 60 fps is not reached.** The canyon half alone is GPU-bound at
~20–25 ms: the standalone `desert_canyon` template measures 20–27 ms on the
same machine (`dcveg.txt`). Holding 16.7 ms needs material-pass work beyond
this phase:
- **(a) Per-biome tile materials.** Tiles wholly inside one biome would
  wear a four-layer material of their environment's layers, and only band
  tiles the eight-layer one. Expected −4 to −6 ms on pure tiles. It needs a
  per-material atlas size, so the 1k/2k atlas resolutions match at tile
  seams.
- **(b) A cheaper far-terrain variant**, as PERF.md lists.
- **(c) Half-rate shading** of distant terrain.

## 6. Deviations

- **The freeze was not root-caused.** It does not reproduce, so it was
  neither found nor fixed (outcome a, with WSLg also clean). No safeguard
  was added.
- **The 60 fps target is not met** (above). The defaults were cut where
  that measurably helps: placements, and with them the CPU frame.
- **`DesertCanyonPreset` now differs from its template** (fewer wash stones
  and bushes). Phase 1 described the preset as "the template minus its
  environment stage".
- **The acacia is the engine's built-in foliage shape** (the biome's), not
  a Poly Haven scan. Poly Haven has none, and it renders well.
- **The seam slope test's p99 comparison has a 0.25° tolerance** (both at
  the cap).

## 7. Gaps

- **Not used in a running editor.** The editor seeding path is tested
  headless only.
- **The savannah's 300 m dry pan** at the canyon's mouth: the canyon's
  extended trench ends in a closed basin that the drainage fills flat. It
  reads as a pan, which is plausible, but it is an accident of the layout.
  A regional tilt or an outlet would make it a wash.
- **Mesa shadows on the band at 30° sun are strong** (ambient 0.4). They
  are natural, but dark.
- **The band's grass on the desert talus thins from 30 m up**, leaving
  yellow specks on pale sand. That is foliage distance thinning, unchanged
  from the engine's.
- **Not re-run:** the full GL suite, the known Nanite failures, and a
  moving-camera (`fly=1`) bench of this world.
- **Windows copy deleted.** The bench copy
  (`%LOCALAPPDATA%\Temp\foundry-regions-perf`) was removed at the end. The
  reports are in `perf_phase6/`. The older `foundry-canyon-perf` copy (not
  this session's) was left.

## 8. Per-tile materials (follow-up, 2026-10-12)

The recommended next step (a) of section 5: tiles wholly inside one biome
wear a four-layer material, and only the band keeps the eight-layer one.

### What was built

- **`region_material` `per_tile`** (Added, default off; `tile_margin`, 32 m)
  in `procgen/stages/region_material_tiles.go`.
  - For each terrain tile, stitch strip and backdrop ring tile, its LAYER
    SET is every union layer whose weight byte is ≥ 2 anywhere in the tile's
    window: the texels its fragments sample (one round the edge, for the
    bilinear filter) plus `tile_margin` for the weights' mipmaps. Byte 1 is
    left out exactly: the shader takes a layer part only from 0.004
    (`fxSplatLayerOn`), and no filtering of bytes ≤ 1 reaches that.
  - Sets are widened to the fewest materials: inside layers 1–4 → 1–4,
    inside 5–8 → 5–8, a mixed set joins a larger mixed set that holds it;
    more than four → the union.
  - One four-layer material per set, `gen_<doc>_<stage>_l1234` (`_far_l…`
    for the backdrop), built by the same `regionMaterialDoc` as the union:
    its layers in the union's order with the union's per-layer settings and
    material knobs. Its weights are the union's bytes for its layers: for
    1–4 that IS `weights.png`, for 5–8 `weights2.png` (reused, no new
    texture); a mixed set gets `weights_l….png` (same rectangle, same
    resolution, so the same filtering and mip chain).
  - **Placement:** region_terrain still places every tile (the weights come
    from its masks, so it runs first); region_material SUPERSEDES the tiles
    it re-dresses (`RunContext.Supersede`, as drift does) and re-places
    each unchanged but for its material. Only placements wearing the union
    (or its far material) are touched. The level IDs become
    `gen.<doc>.ground_mat.tile_i_j`.
  - The manifest lists them (`tiles:` in region_material.yaml).
  - The `canyon_savannah` and `level_world` templates switch it on; existing
    documents keep their hash and output (test).
- **Shared atlas** (`material`: splat params `atlas1..8`,
  `SurfaceFx.SplatAtlas`; `gfx`: `splatAtlasNames`, `splatLayerTiles`). A
  four-layer tile material names the union's eight layers, so it is packed
  into — and samples — the union's 4×2 atlas: the same texels, gutters and
  mips, and no second atlas pair in video memory (the bench log shows one
  atlas build). Kept only when every layer of the splat is in it. No shader
  change: `fxSplatLayer` already carried each layer's tile, and the atlas's
  columns come from the atlas.
- **Why the boundary is exact.** On a four-layer tile every layer outside
  its set is below the shader's cut on the union too (not a candidate,
  never sampled, never added), so the union draws exactly the four-layer
  material's operations in the same order.

### The seam test

`render/material_fx_pertile_gl_test.go`: three rolling tiles (west, band,
east) under one weights map, eight patterned layers with distinct height
maps, a triplanar layer per biome, a tint, anti-tiling, relief. Reference:
all three on the eight-layer union; then west and east on four-layer
materials built as the stage builds them. Two layouts: aligned sets (1–4
and 5–8, the union's own weights maps) and mixed sets ({1,6}, {3,8},
remapped maps). **Max difference 0 levels at every pixel** on the vis buffer
(variants and generic program) and the forward path; the vis pass shades 3
bins instead of 1, so the four-layer variants really ran.

### Native numbers (GTX 1070, 2560×1080, user graphics.yaml)

Same method, poses and settings as section 5. The world is `$S/proj6f`,
rerun with `per_tile: true`:
- only `ground_mat`, the rockface and the two scatters reran (3 min 48 s);
- 56 of 96 tiles and strips and 88 of 112 ring tiles went four-layer, with
  4 materials (l1234, l5678 and their far twins), so no extra weights maps;
- the scatter rerun moved 4 placements (6,518 → 6,522), the usual rerun
  drift.

The same exe ran both: before = the level before the splice (every tile on
the union). Reports: `perf_phase6/pt_before.txt`, `pt_after.txt`,
`pt_fx129.txt`. All times are ms. "Floor" is `FOUNDRY_FX_MASK=-129`, every
splat forced four-layer (a wrong picture, the most any per-tile scheme
could give).

| Pose | p50 before → after | p95 before → after | material before → after | floor (material) |
| --- | --- | --- | --- | --- |
| desert → savannah | 24.3 → 24.4 | 25.0 → 25.0 | 10.8 → 10.9 | 9.4 |
| savannah → canyon | 32.1 → 31.6 | 33.4 → 32.8 | 18.3 → 18.1 | 16.1 |
| band, east | 26.0 → 26.1 | 27.2 → 27.3 | 11.9 → 11.9 | 11.4 |
| band, west | 34.2 → 34.0 | 35.5 → 35.5 | 24.3 → 24.2 | 18.1 |
| escarpment | 30.5 → 30.2 | 31.9 → 32.1 | 14.7 → 14.5 | 13.4 |
| 30 m up, band | 29.6 → 29.9 | 30.9 → 30.9 | 19.9 → 20.0 | 16.0 |
| 30 m up, seam | 33.7 → 34.0 | 34.7 → 34.9 | 17.6 → 17.8 | 13.9 |
| savannah grass | 28.4 → 27.6 | 29.5 → 28.7 | 12.5 → 11.8 | 11.1 |
| spawn | 25.1 → 24.2 | 26.4 → 25.2 | 12.3 → 11.4 | 10.6 |

**Reading: the change is correct, but the gain is small on this world.**
- The pure-biome views gain about 1 ms: savannah grass −0.8 ms p50 and
  spawn −0.9 ms. That is 60–70% of what forcing every tile four-layer
  would give there.
- The band views gain nothing (±0.3 ms is noise), and cannot. The band's
  layers reach far:
  - the savannah's layers carry weight ≥ 2/255 out to x = −244, and the
    desert's to x = +246;
  - even at ≥ 77/255 they reach x = −178 and +159;
  - this is the 200 m transition plus the warp.
- So the four 128 m tile columns from x −256 to 256, half the world, need
  eight layers, whatever the margin (8 m and 48 m give the same columns).
- The gate poses stand at the join on purpose, so most of their terrain
  pixels are band tiles.
- Even the floor saves at most 6 ms, and only in band views. Phase 6's
  "4–6 ms on every tile" was the cost on band pixels. Pure tiles had
  mostly lost it already to the two-pass rewrite and the single-candidate
  skip.
- 60 fps still needs (b) or (c) of section 5, or a narrower band.

**Seam check from the ground** (WSLg, 1600×900, the nine gate poses; same
binary, before against after level):
- Renders: `$S/p6/gatePT_before`, `gatePT_after`; with foliage hidden
  (`hide=foliage`), `gatePTh_before`, `gatePTh_after`. Diff images are
  `gatePTh_after/diff_<n>.png`.
- With the grass hidden, a mean of 0.05–0.23 levels differ, and > 8 levels
  on 0.03–0.21% of pixels.
- The differing pixels are swaying shrubs, VSM/TAA speckle on boulders, and
  the rerun scatter. There is no tile-shaped edge or line in any diff.
- Looked at: poses 4 and 5 show the band, the escarpment's foot and the
  canyon walls with no seam.
- With the grass on, the diffs are the grass's wind.

### Also fixed

`render/nfoliage.go` `texBuffer.make` sized a texture buffer with headroom
(`need*2`) but uploaded it from a slice of `need` bytes. BufferData then
read past the slice's end, and the native bench crashed at its first
foliage frame with an access violation, in this world as it stood. The data
now goes up with BufferSubData when the buffer is bigger. This is a
pre-existing bug, and the fix does not change behaviour otherwise.

### Files and tests

**New:**
- `procgen/stages/region_material_tiles.go`
- `procgen/stages/region_material_tiles_test.go`: set widening, the byte
  cut, windows, and end to end:
  - every tile placed once, unmoved;
  - west tiles four-layer, band tiles on the union;
  - each material's layers, knobs, atlas and weights bytes are the union's;
  - the hash is unchanged when off.
- `render/material_fx_pertile_gl_test.go`
- `material/material_splat_atlas_test.go`
- `gfx/material_fx_shared_atlas_test.go`
- `docs/regions/perf_phase6/pt_*.txt`

**Changed:**
- `material/material_kinds_terrain.go` (atlas params)
- `material/material_resolve_fx.go` (`SplatAtlas`)
- `gfx/material_fx.go`
- `gfx/material_fx_textures.go`
- `procgen/stages/region_material.go` (params)
- `procgen/stages/region_material_run.go`: the far material returns its
  bytes, and the per-tile call
- `procgen/stages/terrain_far.go` (`farSegments`, a pure refactor)
- `procgen/stages/terrain_material.go` (`Tiles` in the artifact)
- `procgen/stages/canyon_savannah.go`, `procgen/stages/level_world.go`
- `render/nfoliage.go`
- `docs/guide/35-regions.md`

### Gaps

- **Stale materials are not cleaned up.** Per-tile `.fmat` files a later
  run no longer writes stay in `resources/materials`, as every generated
  material does.
- **Turning it on reruns more than the material.** Toggling `per_tile`
  reruns the stages downstream of `ground_mat` (rockface, scatter) by hash
  chaining, about 4 minutes here.
- **No per-material atlas size.** It was not needed: the four-layer tiles
  share the union's 1k atlas.
- **The margin is a heuristic for far mips.** The margin covers mip reach
  out to about level 4–5. A much further, grazing view could in principle
  read a foreign layer's mip average on a four-layer tile. No line showed
  in the gate renders.
- **Not run:** the full GL suite, and the known Nanite failures.
- **Windows copy deleted:** `%LOCALAPPDATA%\Temp\foundry-regions-perf`.
