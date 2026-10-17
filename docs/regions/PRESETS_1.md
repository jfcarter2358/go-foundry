# Environment presets — step 1: the engine work

Status: built and tested headless, 2026-10-13. Every item of
docs/regions/PRESETS.md "Engine work" has code, unit tests and a stage test;
the landforms were looked at as relief pictures, and a three-region scratch
world (a palette of ten layers, a highland with an edge rise, a lake and
snow/wet covers) was generated end to end and rendered through the real
engine on WSLg (Mesa d3d12 on the GTX 1070) and looked at. Nothing was
clicked in a running editor; no new preset exists yet (that is step 2) and
the OneDrive project was not touched.

`$S` below is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.
Pictures are in `$S/presets1/pics/`.

Every new parameter is `Added` and off at its default: the canyon and
savannah goldens, the one-region identity, the river crossing, tiled
locality, the per-tile exactness tests and the desert_canyon hash pin all
pass unchanged.

## 1. More than eight ground layers: the palette

**Material** (`material_kinds_terrain.go`, `material_resolve_fx.go`).
`SplatAtlasMax = 32`; the splat's shared-atlas params run `atlas1..atlas32`
(9–32 appended after everything, so no existing file changes; names are now
`strconv`, identical for 1–9). `SurfaceFx.SplatAtlas` is `[32]string`. A
splat still mixes at most eight layers.

**gfx** (`material_fx_textures.go`, `material_fx.go`). The atlas cache is
keyed by 32 names (`SplatArraysForAtlas`; `SplatArraysFor8` wraps it). More
than eight distinct names pack **8 across, `ceil(n/8)` rows (≥ 2)**, each
layer at most `FxArrayPaletteMaxSize` = 512 (`FOUNDRY_SPLAT_PALETTE_MAX_SIZE`):
32 layers are 4352×2176, the eight-layer 1k pair's size. **VRAM:** RGBA8
38 MB per atlas level 0, ~101 MB for the albedo+normal pair with mips; BC7
~25 MB. `SplatArrays.Rows` is new; `packSplatAtlasGridRows`,
`splatCompressibleRows` take rows. Eight names or fewer: the same key hash,
geometry and pixels as before (tested).

**Shader** (`surface_fx.glsl`). One new uniform `fxSplatRows`; `fxAtlas`
addresses `1/rows` instead of the constant 0.5 (`rows` reads 0 as 2, and
1.0/2.0 is exactly 0.5, so every existing atlas samples the same texels).
No new sampler: the material pass still holds 31 of 32
(`TestMaterialFxSamplersFitTheMaterialPass` passes unchanged).

**region_material** (`region_material.go`, `region_material_run.go`, new
`region_material_palette.go`). Deduped layers may number up to 32. Past
eight:
- there is **no union material** (no `weights.png`, no `.fmat` under the
  union's name); `TerrainMaterialArtifact.Palette` = the count;
- per-tile materials are **mandatory** (the `per_tile` switch is ignored);
- each near tile/strip and backdrop ring tile's raw set = palette layers
  with a weight byte ≥ 2 within `tile_margin` (the per-tile rule); more than
  eight is an **error naming each tile and its layers** (first four, then
  "and N more");
- raw sets join the largest chosen set containing them — but a ≤ 4 set only
  joins a ≤ 4 set, so tiles deep in one region stay four-layer;
- each set's material is `regionMaterialDoc` of its layers in palette order
  with `atlas1..N` = the palette; its weights are the palette's bytes over
  the bounding box of its tiles' windows, **snapped out to 64-texel
  alignment** (so mips 0–6 match the neighbour's map for shared layers):
  `weights_p<layers>.png` (+ `weights2_…` for 5–8 layers);
- every region_terrain placement wearing the union's name is superseded and
  re-placed; one it cannot identify is an error.
- `wall_sheet` takes the smallest per-tile material holding its region's
  rock (`paletteRockMaterial`); `drift` refuses a palette as it refuses an
  eight-layer ground; `rockLayerIndex` knows palettes.

Scratch world: 10 layers → 7 materials, 190 of 276 placements four-layer,
weights maps 1.7 M texels (9 MB RGBA8 with mips).

## 2. Cover rules (`terrain_material_cover.go`)

Optional layer keys on `terrain_material`: **`snow`** (`snow_height` world
metres, `snow_slope`, `snow_breakup`, `snow_breakup_scale`), **`wet`**
(`wet_distance`, `wet_height`; from the terrain's `mask_water`, via a
chamfer transform carrying the nearest water level), **`ash`** (`ash_radius`
in cone radii, from terrain.yaml `vents:`; not on slopes > ~50°). Order wet,
ash, snow; each takes its share from everything below it. Weights become 7
channels (`groundRoleKeys`); a terrain_material with covers writes
`weights2.png` and an eight-layer splat (covers at layers 5–7) and records
`cover_coverage`; the far backdrop takes snow and ash (`farCoverWeights`).
region_material dedupes cover layers like the others (roles `wet`, `ash`,
`snow` in `layers:`; covers keep tint 1). No cover key: `groundWeights`
untouched, byte-identical. "Forest floor under canopy" was not done (it needs
the placement stages' canopy, which the material runs before).

## 3. Landforms (`procgen/terrain/landform.go`, stage params `terrain_landform.go`)

Applied in `baseShaper.at` after the classic shape (nil when none is on, so
the classic path is untouched; the far backdrop continues them):

| Landform | Params (terrain stage) | Notes |
| --- | --- | --- |
| Ridged mountains | `ridge_height` (>0 on), `ridge_scale`, `ridge_octaves`, `ridge_valleys`, `ridge_warp` | ridged multifractal; valleys = soft-clamped floor + power (a U-profile reshape, no ice) |
| Dune field | `dune_height`, `dune_wavelength`, `dune_wind` (bearing toward, ° from +X to +Z), `dune_asymmetry`, `dune_sinuosity`, `dune_cover`, `dune_ripple`, `dune_ripple_wavelength` | lee capped at the 34° repose |
| Coastline | `coast` (off/+x/-x/+z/-z/line), `coast_line` spline (sea on its right), `sea_level`, `coast_offset`, `coast_meander`, `beach_width`, `beach_rise`, `shelf_width`, `sea_depth`, `cliff_height`, `cliff_width`, `coast_inland` | side = the footprint's bounding rectangle side |
| Volcanoes | `volcanoes` (seeded count) or `volcano_vents` spline, `volcano_height`, `volcano_radius`, `crater`, `crater_depth`, `lava_lobes`, `lava_length`, `lava_width`, `lava_thickness` | vents → `Result.Vents`, terrain.yaml `vents:` |
| Hummocks/ponds | `hummock_height`, `hummock_spacing`, `pond_cover` (measured quantile), `pond_depth`, `pond_scale` | ponds are closed hollows |

Pictures looked at (`$S/presets1/pics/*_relief.png`, from
`FOUNDRY_LANDFORM_DUMP`): eroded ridges with spurs and gullies (and the V
version for comparison), transverse sinuous dunes with shadowed slip faces,
a coast with sea floor, beach and cliff line, a drawn coast line, two
cratered cones with gullied flanks and lava tongues, hummocky ground with
flat pond hollows. The first ridge attempt (height/scale 0.27, no erosion)
was a field of steep blobs (p50 slope 52°); the test now uses realistic
proportions with drainage, and the doc says to keep height/scale < 0.25.

## 4. Water (`procgen/terrain/water.go`, `stages/region_terrain_water.go`)

Engine survey: there is **no water placement or volume** any more; a
level's water is a surface wearing a material with the `water` effect
(`resources/materials/water.fmat`), drawn forward (not prepassable) even on
Nanite scenery. So: preset terrain params `water`/`water_level` (a level
table: ground of the owning region below it) and `ponds`/`pond_min_depth`/
`pond_min_area` (priority-flood fill, `FillPits`; surface 5 cm under the
spill). `terrain.FindWater` → bodies (sea per region, pond per hollow).
region_terrain writes `mask_water.png` (depth/8 m), terrain.yaml `water:`,
`gen_<doc>_<stage>_water.fmat` (or `water_material`; `water_texture`,
`water_waves`), and per terrain tile `water_<i>_<j>.fnm` placed (collision
none, not solid, chunk auto) — owned by the stage's prefix, so the splice
replaces them with the tiles, and region_material's re-dress never touches
them. Each wet cell (any wet corner) plus **2 shore cells** is two triangles
over shared corners (no T-junctions: the first version merged rows into
quads and the cook's quantisation opened a visible crack along them — seen
in a render, fixed); the cook locks the tile rect and the open outline.

## 5. Height compatibility (`procgen/terrain/edge.go`)

Preset params `base_elevation`, `edge_rise`: across the band the provider
stands at the level, and its relief returns over `edge_rise` metres inside
its own ground — `h' = L + smoothstep(0, rise, d)·(h − L)`, d = chamfer
distance to the nearest sample it does not own outright. Applied to the
provider's base shape before canyons, blend and erosion. A one-provider
union has no such sample (fade exactly 1): identity holds. Measured (stage
test, 220 m ridges beside a plain, 120 m band): band mean 54.2 m → 0.0 m,
steepest band slope 78° → 4°; deep in the range unchanged.

## Files

New: `procgen/terrain/{landform,edge,water}.go` (+ `landform_test.go`,
`edge_water_test.go`); `procgen/stages/{terrain_landform,
terrain_material_cover,region_terrain_water,region_material_palette}.go`
(+ `region_terrain_presets_test.go`, `terrain_material_cover_test.go`,
`region_material_palette_test.go`); `gfx/material_fx_atlas_palette_gl_test.go`;
`render/material_fx_palette_gl_test.go`.
Changed: terrain `settings.go`, `shape.go`, `union.go`, `terrain.go`,
`meta.go`; stages `terrain.go`, `terrain_material.go`,
`terrain_material_far.go`, `region_terrain.go`, `region_terrain_run.go`,
`region_material.go`, `region_material_run.go`, `wall_sheet.go`,
`drift.go`, `place_region_ground.go`; material `material_kinds_terrain.go`,
`material_resolve_fx.go`; gfx `material_fx.go`, `material_fx_textures.go`;
`resources/shaders/surface_fx.glsl`. Tests updated deliberately for the
`[32]` atlas type: material/gfx shared-atlas tests, `material_fx_atlas8_gl_test`,
`region_material_tiles_test`; `TestRegionMaterialRefusesMoreThanEightLayers`
became `TestRegionMaterialPaletteLimits` (12 merge; > 32 refused).
Docs: `docs/guide/33-generators.md` (landforms, region-only params, cover
layers, water, palette), `docs/guide/35-regions.md` (three sections).

## Tests and commands

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                               clean
go vet procgen/... material/... gfx render                   clean
go test ./internal/procgen/... ./internal/material/... ./internal/gfx/ ./internal/world/... ./internal/levelio/...   ok
go test ./internal/editor/...        fails only TestScoreSibeliusKeymap, TestScoreRebinding (the score session's)
go test ./internal/ -run TestPackagesDoNotDependUpward       ok
FOUNDRY_GL_TEST=1 go test ./internal/gfx/...                 ok
FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'TestMaterialFx|TestMaterialPassBuildsTheEightLayerVariant|TestVisMaterialProgramBuilds|TestMaterialVariants|TestSceneBlockMatchesTheShader|TestFxUnitsAreFree'   ok (141 s)
```

New tests: landforms off = classic (`TestLandformOffIsClassic`), ridges
(U floors 50% gentle vs V 25%), dunes (lee p95 36.5° vs stoss 17.7°, under
40°), coast (sea side deep, cliff on most rows), drawn coast side, volcanoes
(rim ≥ 15 m above crater, ≥ 80 m above foot; vents deterministic and in
terrain.yaml), hummocks/ponds (6.9% in closed hollows); edge rise (terrain
and stage level) and its one-region identity; chamfer; `FindWater` (level,
pond spill/freeboard, min area, no rule); water mesh area/winding; water
surfaces placed (material, collision none, not solid, auto chunk, mask,
bodies); landform params default off, all `Added`, all mapped; cover shares
and stacking; snow cover on a classic terrain is eight-layer and sums to
one; region material carries wet ground; palette of 12 (all placements
re-dressed, ≤ 8 layers each, atlas = palette, weights on the 64-texel grid,
four- and eight-layer tiles both present); a tile needing 10 is refused by
name; palette limits; palette rock for walls. **GL**: `TestSplatAtlasPalette`
(20 layers → 8×3, tiles in place, BC7 same geometry PSNR 46.7 dB, eight
names still 4×2); **`TestMaterialFxPaletteTileMatchesItsOwnAtlas`**: a tile
on palette entries 10–13 and 17–20 of a 20-layer atlas (rows 1–2 of the 8×3)
against the same layers on their own atlas, eight- and four-layer, vis
buffer (variants and generic) and forward: **max difference 2 levels, 1
pixel past 1** (eight) and **1 level** (four).

## Pictures (looked at)

- `pics/*_relief.png`: the landforms (above).
- `pics/edge_off_relief.png`, `edge_on_relief.png`: mountains meeting a
  plain as a cliff band vs rising inside their footprint.
- `pics/w8_lake.png`, `w8_highland.png`, `w8_overview.png`: the scratch
  world (`$S/proj8`, level `w8`, three regions: dryland / grassland /
  highland with ridges, edge rise, a water level, snow and wet covers;
  `foundry gen run` 18 s, 252 MB peak): lake surface with shoreline, ranges
  rising from the plain, the pale "snow" cover band (the scratch preset used
  a rock scan as its snow texture — no snow scan was cached offline).

## Gaps and honest notes

- **Anti-tiling differs between a palette and an eight-layer world**: its
  patch noise is seeded by the layer's atlas tile index, so the same
  anti-tiled layer lays its patches differently in a palette (consistent
  between all tiles of one world, so no seam). The GL comparison therefore
  has no anti-tiled layer.
- **Water is cosmetic**: no volume, no swimming/buoyancy, collision none.
  Levels are per owning region without connectivity (low ground anywhere in
  a water region floods; the scratch world's highland flooded right up to
  its band because its base sat below its water level — a preset matter).
  No rivers/channels routing: a wetland's channels are its low ground under
  its level. Drainage `fill` can fill pond hollows before the water pass
  sees them (presets with ponds should keep fill low).
- The classic `terrain` stage ignores water and edge rise (no neighbours /
  no water pass); its landforms work.
- Edge rise is not applied to the far backdrop (it continues full relief).
- Coast sides and volcano placement use the footprint's bounding rectangle,
  not the polygon. Lava lobes run straight out from the vent, not downhill.
  Ripples need ≥ 4 samples per wavelength and survive erosion only on gentle
  stoss slopes.
- Snow height is absolute metres: a preset must know its heights (and edge
  rise / base elevation shift them).
- Palette: no union material file exists, so anything naming
  `gen_<doc>_<mat>` directly (a hand-placed tile, the editor's material
  preview of the union) finds nothing; wall_sheet and drift were checked,
  cliff_wall/scatter read only ground/height maps. Tile weights maps align
  mips only to level 6.
- A faint shading line along terrain tile edges is visible from 400 m up; it
  is pre-existing (same with the build before this work, rendered from the
  canyon_savannah scratch world) and independent of materials.
- Frame cost of a palette world was not measured natively (WSLg p50 17 ms
  at 1280×720 on the scratch world's lake view).
- The new parameters appear in the Generator's generic param panel; nobody
  has used them by hand in a running editor.
