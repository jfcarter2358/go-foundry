# Environment regions — phase 2: field providers and erosion over the union

Status: built and tested headless, 2026-10-12. Every gate has a test that
passes. The terrain was checked by eye from dumped shaded-relief and biome
pictures, but not in a running editor or the game: nothing was cooked and
walked.

## What was built

### 1. procgen/terrain split: providers + shared passes

The classic pipeline is now two halves (the file headers say this in detail):

- **Providers** (provider.go, shape.go, canyon.go, strata.go): the pre-erosion
  part for one region. That is the base shape, the mesa field, the strata
  offsets and the canyons, evaluated in the region's own coordinates
  (world − anchor) on a square sub-grid aligned to the union's samples, over
  the region's **support** (every sample where its weight > 0).
  `newMesaField`/`baseShape`/`newBaseShaper` take the rectangle the mesa
  quantiles and rim gradient are measured over. For a region that rectangle
  is its footprint bounds; for the classic stage it is
  `CentredSquare(size)`, the same arithmetic as before.
- **Material model** (model.go): `material` is one region's compiled strata,
  terrace gate, drainage K, talus angles, detail noises, cliff mask angles,
  sand colour and albedo noise. `fieldModel` stores sparse per-sample
  storage. `reg[k] < 0xF0` means the sample is owned outright (one region,
  its strata offset in `off[k]`), and answers use **no blending arithmetic**.
  Otherwise it is a band sample with up to 4 `(region, weight, offset)`
  entries. Methods: `erod`, `drainK`, `hardness`, `talus`, `terraceDelta`,
  `dominant`, `window` (a sub-square for tiled erosion, sharing the mix
  entries) and `downsample` (the drainage coarse grid: per-region
  box-averaged offsets, the classic box average when one region owns
  everything).
- **Shared passes** (field.go plus the rewritten fluvial.go, strata.go
  `terrace`, droplet.go, thermal.go, detail.go and masks.go) run over a
  `field` (grid, model, deposit, knobs). Per-region values are blended per
  sample: erodibility × drainage strength, terrace, talus angle, hardness,
  detail, cliff-mask angle and albedo. The strata index and strata height
  come from the **dominant** region (an index cannot be blended).
  Per-pass knobs (drainage iterations/cell/fill/diffusion, droplet
  parameters, talus iterations/rate, droplet seed) come from the **lead**
  provider: the one dominant on the most samples, or on the most samples of
  the window when tiled.
- **`Generate(s)`** is the one-provider case, with byte-identical output.
  **`GenerateUnion(u)`** (union.go) runs weigh → providers → canyons → blend
  → erosion (global, or tiled.go) → masks. The grid has an arbitrary
  world-aligned `MinX/MinZ`.
- **Tiled erosion** (tiled.go): the union is cut into windows of `Window`
  cells (a too-thin last window merges into the one before). Each window is
  eroded over itself plus `Margin` on every side. That area is made square
  and shifted inward at the union's edge. Each window gets its own lead's
  knobs and seed `Hash64(Union.Seed, 'TILE', i, j)`. Windows are cross-faded
  with a smoothstep partition of unity over ±margin/2 at each interior
  boundary.
- **Canyons carried through the band** (canyon.go `canyonExtend`): in a
  union of more than one provider, a canyon end within reach of the
  region's footprint rectangle continues straight for "band/2 + warp reach"
  metres. Without this, a canyon drawn to a region's edge ends in a wall of
  its own ground that the blend turns into a dam. It is off for one
  provider, which the identity needs.
- **Far backdrop** (far.go): `FarField` has an `anchor`, and
  `newFarField(s, over, anchor, g, paths, …)` backs `NewFarField`.
  `UnionFar` holds one FarField per provider (its own shape in its own
  coordinates, with only its own canyons carried on) and blends their
  heights by the model's weights at the **nearest union sample to the
  point**, so it follows the edge. This goes further than "the dominant
  environment per edge": it blends smoothly where a band meets the edge.
- `Grid.DrainPath` (flood.go) walks the priority flood's receivers (for the
  river test and tools). `Result.RegionWeights(r)`, `Result.Dominant(k)`,
  `Result.PathRegions` and `Result.Lead` were added.
- meta.go: `Meta.Biomes *BiomesMeta` (omitempty), `BiomeMask(env)` =
  `biome_<env>`, `BiomesFile`, `Terrain.Biomes()` and `Terrain.BiomeAt(env, x, z)`.

### 2. The `region_terrain` stage (procgen/stages/region_terrain*.go)

- Kind `region_terrain`, Category Level, `Regional: Shared`,
  `SharedFor: [terrain]`. Params: `regions` (stage ref), `seed` (tiled window
  seeds), `extent` (footprints|bands), `window` (tiles), the terrain stage's
  output params verbatim (mesh_step, collision, texture, material, uv_mode,
  uv_scale, cook, cook_memory), `preview_cells` (PreviewOnly) and the
  terrain's far params. Resolution, tile_size, margin and erosion are read
  from the referenced **regions** stage, whose docs now say so.
- Each region's preset comes from its generator override, else the
  environment file, else the built-in. Its first enabled terrain stage is
  mapped through `terrainSettings`. Cell = union resolution. Seed =
  `RegionStageSeed(doc seed, key, preset stage ID, seed params)`
  (`regionProviderSeed`, a var so the identity test can inject the classic
  seed). Anchor = the region's anchor point. Measure = footprint bounds.
- Union rect: whole `tile_size` multiples from the world origin covering
  the footprints (default) or band bounds, padded to a square (extra tiles
  split evenly, the odd one on the max side).
- Weights: `regions.Set.Weights`, the same function the regions preview
  paints, adapted to `terrain.WeightFunc`. With one region nothing is
  asked: the single region owns every sample with weight exactly 1.
- Outputs: the terrain stage's files (heightmap, 7 masks, albedo, relief,
  tiles `tile_<i>_<j>` with `chunk: auto` and the `material` param, on one
  world tile grid with one quantisation step via `planTiles`/`writeTiles`,
  and the far ring + stitch via `writeFarWith` + `UnionFar`), plus
  `mask_biome_<env>.png` per environment (registered in `meta.masks`, so
  `Terrain.Mask("biome_<env>")` works), `biomes.png`, and terrain.yaml
  `biomes:`. That section holds the environments (name, colour, mask), the
  regions (key, environment, preset source, preset stage, seed, anchor,
  footprint bounds, priority, transition, strata list), the erosion
  mode/window/margin, the lead and the extent. `meta.strata` is the lead
  region's.
- Artifact: `*terrain.Terrain` (`FromResult`). Load: `terrain.Load`.
- **Preview**: providers `Settings.Scaled(preview_cells)`, always global.
  Layers: Relief, Biome relief (shaded in biome colours), biomes overlay,
  Height, Albedo, Flow, Slope, Deposition, Cliff, Curvature and one
  `biome_<env>` per environment. Also canyon lines, region outlines, the
  tile grid, per-tile preview meshes and notes (lead, sizes, time).
- **Hashing**: new `procgen.StageKind.HashExtra`, folded into the plan's and
  the preview's hash only when set, so no existing hash changes.
  region_terrain's folds in every region's preset terrain stage's resolved
  params, so editing a preset reruns the terrain (tested).
- The terrain consumers' stage refs (`terrain_material`, `scatter`,
  `cliff_wall`, `wall_sheet`, `drift`, `vegetation`, `environment`) accept
  `TerrainSources` = {terrain, region_terrain}. StageKinds is not hashed.
- `level_world` template: `regions`, then `ground` (region_terrain), with a
  commented slot for phase 3's `region_material`.

## Formats

terrain.yaml: as docs/canyon/PHASE_1.md, plus `biome_<env>` entries in
`masks` (encoding unit) and the `biomes:` block shown in the
region_terrain_run.go header. New files: `mask_biome_<env>.png` (8-bit,
share 0..1) and `biomes.png` (RGB).

## Tests

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                   # clean
go vet ./internal/procgen/...                    # clean
go test ./internal/procgen/... ./internal/world/...   # all ok
go test ./internal/ -run TestPackagesDoNotDependUpward  # ok
go test ./internal/editor/...                    # see below
```

- **Goldens recorded before any refactor** (they still pass, unchanged):
  `terrain/golden_test.go` `TestGenerateGolden` hashes the heights, deposit,
  every mask, strata index, albedo and paths for 256 m @ 1 m with canyon,
  drainage, droplets, thermal and detail, and for a 2 m variant with dip,
  joints and a U canyon. `stages/terrain_golden_test.go`
  `TestTerrainStageGolden` hashes every file of the desert_canyon
  template's ground (256 m @ 1 m, uncooked, so the tile .glb meshes are
  included) and of a cooked far-backdrop doc (all files but the .fnm and
  placements.yaml, which are not byte-stable run to run: the cook's own
  parallelism and absolute paths). A third hash, `far_meshes`, covers the
  far lattice heights and every stitch strip's positions and indices.
- **GATE (a)** `TestGateOneRegionIsTheClassicTerrain`: one region, footprint
  [-128,128]², anchored at the origin, preset = the desert ground's params,
  provider seed = the classic stage's seed. heightmap, 7 masks, albedo,
  relief and all 4 tile .glb files are **byte-identical** to the classic
  stage's. The union grid is 256 cells from (-128,-128), and the biome mask
  is exactly 1 everywhere. Terrain level: `TestUnionOfOneIsGenerate`
  (ResultHash equal, with and without a weight function returning 1−1e-16)
  and `TestUnionFarOfOneIsTheFarField`.
- **GATE (b)** `TestGateRiverCrossesTheSeam`: canyon region A
  [-384,0]×[-192,192] with a U canyon drawn east to x = 0, and a plain B 40 m
  lower, at 2 m. From the canyon floor 100 m before the drawn end,
  `DrainPath` enters the channel. Flow then stays ≥ 0.45 (observed ≈ 0.9)
  until the path has run 150 m through samples where B's share is ≥ 0.5.
  No step rises more than 0.5 m, and the end is below the start.
- **GATE (c)** `TestGateTiledErosionLeavesDistantTilesAlone`: three regions
  tiling a 768 m union at 2 m, tiled erosion, window 1 tile, margin 32 m.
  Changing the far east region's preset relief leaves every sample of tile
  column 0 bit-identical, and samples near the edited region do change.
  Terrain level: `TestTiledErosionIsLocal` and
  `TestTiledWindowsPartitionUnity`.
- **Seam** `TestSeamMakesNoCliffs`: the built-in desert_canyon and savannah
  presets, 2 m, 200 m band. Steepest slope: band 82.3°, desert 82.2°,
  savannah 10.3°. 99th percentile: band (desert side) 80.9°, desert 80.9°.
  The test allows the maximum 1° over the desert's (both sit at the talus
  cliff cap of 80° plus detail bumps) and requires the 99th percentile to
  be no higher.
- Others: `TestUnionBlendsAcrossTheBand` (shares sum to 1, owned = exactly
  1, the step down, per-region albedo), `TestUnionIsTheSameOnAnyWorkerCount`
  (global and tiled, 1 vs 7 workers), `TestRegionTerrainOutputsAndLoad`
  (files, Load, BiomeAt, padded bounds, rerun skips, a preset edit reruns),
  `TestRegionTerrainPreview` and `TestLevelWorldGrowsARegionTerrain`.
- Phase-1 tests touched: `TestLevelWorldTemplate` now expects 2 stages, and
  `TestRegionsRunAndLoad` runs only the regions stage (the template's
  region_terrain at 0.5 m, cooked, took 160 s there).
- Looking aids: `FOUNDRY_REGION_TERRAIN_DUMP=<dir>` writes relief, biomes,
  flow, slope, cliff, albedo and biome-shaded relief.
  `FOUNDRY_REGION_BENCH=<dir>` runs the benchmark below.

Editor tests: `go test ./internal/editor/...` fails only
TestScoreSibeliusKeymap and TestScoreRebinding (the score session's work in
progress, not ours). No GL tests were run: nothing in render/gfx was touched.

## Measured numbers

`TestRegionTerrainBench`: 1 km² (desert_canyon x < 0, savannah x > 0, 200 m
band), built-in presets, uncooked, 8 workers, `/usr/bin/time -v`, WSL2 dev
box.

| Run | Wall (whole test) | Generate | Peak RSS |
| --- | --- | --- | --- |
| 1 m, global | 3.7–3.9 s | 3.4 s (weights 0.11, shape 0.52, drainage 1.34, droplets 0.72, thermal 0.38, masks 0.27) | 214–223 MB |
| 0.5 m, global | 10.4–11.0 s | 8.8–9.4 s (weights 0.39, shape 2.0, drainage 1.35, droplets 1.96, thermal 1.62, detail 0.38, masks 1.43) | 770–776 MB |
| 1 m, tiled (4-tile windows, 64 m margin) | 4.4 s | 4.0 s (erosion 3.2) | 220 MB |
| 0.5 m, tiled | 12.5 s | 10.9 s (erosion 7.1) | 793 MB |

For comparison, the classic 1 km canyon field at 0.5 m was 6.7–7.6 s
(PHASE_1). The union adds the weights and a second provider's shape over
the band. Cooking is unchanged (the tile cook dominates any cooked run, as
in PHASE_1).

## Pictures looked at

Session scratchpad `p2/`: `bench_{1,0.5}_{global,tiled}_*` (relief,
biome-shaded relief, biomes, flow, slope, cliff, albedo), `pair_*` (the
seam test) and `seam_*` (the river test). The desert's mesas and cliffs
break down into a ledgy, gullied edge that dies out into the plain. The
boundary meanders, with no straight seam line in relief, albedo or flow. The
extended canyon runs on into the savannah as a shallowing trench. The tiled
run shows no window seam at its window corners. The river test's flow mask
shows one continuous channel from the canyon across the band.

## Deviations from the spec

- **The union covers the footprints by default, not the band bounds**
  (`extent: footprints`; `bands` is available). Past the union's outer edge
  there is no neighbour to blend with, and covering bands would make a
  one-region union bigger than the classic square, breaking gate (a).
- **Providers are evaluated over their weight support** (samples with share
  ≥ 1e-4), not their band bounds. It is the same within a band and correct
  in gaps, where phase 1's fill gives the nearest regions a share outside
  their bands.
- **Far backdrop**: blended by the shares at the nearest union sample
  rather than "the dominant environment per edge" (smoother; the dominant
  rule would put a step in the backdrop where dominance switches).
- **Global knobs** (iterations, droplet run, talus rounds) come from one
  provider (the lead), not blended. Strength, erodibility, angles and
  detail are blended.
- **Provider seeds** come from (doc seed, region key, preset stage ID, seed
  param), so gate (a) injects the classic seed through `regionProviderSeed`.
  A real one-region world differs from the classic stage only in its noise
  seed.
- New core hook `StageKind.HashExtra` (spec silent), needed so preset edits
  rerun the terrain.
- **Canyon extension** through the band (spec silent; needed for gate (b) to
  be natural rather than dammed).

## Known gaps

- **Gap fill reaches far.** A sample no footprint covers (the square
  padding round a non-square region layout) is shared among every region
  within one band of the nearest (phase 1's fill). So in tiled mode a
  region can influence windows well beyond its own band through such gaps.
  Gate (c)'s footprints tile the union; a padded layout's locality is
  weaker.
- **Escarpments**: two regions at different base heights meet over the band
  as a ramp of slope ~ΔH·1.5/band. The higher region's hard strata terrace
  it into ledges. In the river fixture (ΔH 40 m, 150 m band) the band away
  from the canyon reached 47° against the canyon region's own 36°. That is
  canyon country's edge, but it is steeper than either region alone. The
  real desert/savannah pair stays within the desert's own slopes.
- **Flow-mask streaks on flats**: the savannah stub's filled basins (drainage
  fill 0.5) show straight D8 lines in the flow mask and albedo (the
  PHASE_1 flow-mask gap, visible on large flats). Phase 6's savannah should
  lower `drainage_fill` or the mask needs MFD.
- **Tile quantisation and the tile base** are global (from the union's height
  range), so a far edit that changes the union's range changes every cooked
  tile's bytes even where tiled heights are identical. Per-tile incremental
  cooking is phase 5.
- **Provider sub-grids are square** (the Grid type), so a long thin support
  evaluates extra samples it never reads.
- The `strata` mask indexes the dominant region's list. Consumers that look
  up `meta.strata.layers` see the lead's list only; per-region lists are
  under `biomes.regions[].strata`. terrain_material (phase 3) must take
  this into account.
- The preview always uses global erosion. The tiled window/margin, scaled to
  preview cells, would be degenerate.
- Not exercised in a running editor, not cooked at scale, not walked.
