# Environment presets — step 2: the nine presets

Status: built, tested headless, each generated as a one-region 512 m world
at 1 m in a scratch project and rendered through the real engine on WSLg
(Mesa d3d12 on the GTX 1070) from three ground poses and one 40 m pose, and
looked at. Nothing was clicked in a running editor; the OneDrive project was
not touched; no native (Windows) frame numbers were taken.

`$S` is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.
Scratch project: `$S/presets2` (levels `p_<preset>`, docs
`generators/p_<preset>_world.fgen`, written by `$S/p2/tools/mkworld.py`).
**Contact sheets: `$S/p2/sheets/<preset>.png`** (2×2: three ground poses,
one 40 m pose; the poses are in `<preset>.poses.txt`). Iteration renders are
in `$S/p2/shots/`.

## Direction change mid-step: Poly Haven plants only

Partway through, the user asked (via the coordinator) for **no engine
foliage biomes at all**: no `vegetation` stage, no `kind: biome` volumes, no
`resources/biomes/*.fbio` presets. Every tree, shrub, fern, flower and grass
clump is now a `polyhaven_plants` import placed by `scatter` rules (each
preset's `scatter` for trees and rocks, `cover` for ground cover). This also
applied to the existing **savannah**: its biome grass and engine acacias are
replaced by Poly Haven `grass_medium_02` clumps and `searsia_burchellii` in
clumps. That deliberately changes the `savannah` preset's (and so a
`canyon_savannah` world's) output; `TestSavannahPresetGrowsGrassInClumpsAndFrays`
became `TestSavannahPresetGrassIsPolyHavenScenery` and pins the new form.
The `desert_canyon` template and its pinned hashes are unchanged.

## Engine changes this needed (all opt-in, existing outputs unchanged)

1. **Thin foliage** (`polyhaven/phimport/decimate_cards.go`; the plants
   stage's Added `thin_foliage`). Poly Haven trees and grasses carry foliage
   as thousands of tiny cards (fir_sapling_medium: 71,000 cards of ~10
   triangles per tree). Edge-collapse decimation shredded them: a fir came
   out as a bare skeleton (`$S/p2/shots/pine2`, `pine3`). A slot of ≥ 64
   pieces averaging ≤ 48 triangles is now thinned by keeping a
   deterministic random subset of whole pieces, each enlarged about its
   centre by 1/√fraction (cap 4×) — stochastic simplification of aggregate
   detail. Other slots are edge-collapsed as before. Files are `_c<tris>`,
   so no existing `_t<tris>` mesh changes.
2. **Coverage-preserving alpha mips** (`texcompress/mip_coverage.go`,
   `gfx/texture_compress.go`). Even unthinned, needle cards vanished beyond
   ~15 m: box-filtered mips average a 24%-opaque alpha below the 0.5
   cutoff. Textures named `*_cov` get a CPU chain whose alpha is scaled so
   each level's coverage reaches level 0's (Castaño 2010), colours averaged
   weighted by alpha; they upload RGBA8 (never BC7). The importer names a
   cutout albedo `_cov` only under `thin_foliage`. Every other texture's
   chain is still glGenerateMipmap. Before/after: `pine3` → `pine4`.
3. **Scatter `min_size`** (Added, `place_common.go` `sizedVariants`). Poly
   Haven's grass sets split into clumps from 2 cm to 0.3 m; a cover rule
   keeps only the pieces at least this big.
4. **Sea over the backdrop** (`stages/region_terrain_water_far.go`). A
   coast's sea ended in a straight line at the union's edge with dry sea
   floor beyond (`$S/p2/shots/coast1`, pose 3). A level body of a region
   whose preset has a `coast`, reaching an edge, is now carried over the far
   backdrop as non-overlapping flat sheets (`water_far_<side>_<k>`). Only
   for coastlines: on a wetland the same sheets drowned the horizon
   (`wet1`). Runs only when the stage cooks and has a backdrop.

## The shared base and layer conventions

- **Band level 0 m.** Every preset meets its neighbours at 0 m: plains by
  their relief (centred on 0); pine (160 m), alpine (220 m), volcanic
  (150 m), dunes (120 m) and wetland (60 m) by `base_elevation: 0` +
  `edge_rise`. Standing water is below 0: the coast's sea −1.5 m, the
  wetland's table −0.25 m; tundra ponds are hollows. Tested
  (`TestEnvironmentPresetsShareTheBaseLevel`).
- **Shared layer catalogue** (`stages/environments_shared.go`). Presets name
  layers from one table; a layer is shared when id, repeat and projection
  match. All eleven presets: **22 palette entries**; every preset ≤ 4 layers;
  every pair fits a tile's eight (`TestEnvironmentPresetsShareLayers`).
  Empty roles point at an existing layer rather than leaving a key empty.
- **Triples** (three presets at one tile) that fit ≤ 8, among the natural
  ones: savannah+prairie+oak (5), prairie+oak+pine (5), pine+alpine+tundra
  (6), desert+savannah+dunes (8), savannah+prairie+pine (7),
  pine+tundra+volcanic (8), alpine+tundra+volcanic (8), prairie/oak + any one
  other. **Do not fit**: desert+savannah+prairie (9), desert+pine+anything
  but dunes/coast, savannah+alpine+anything, any triple with desert and
  wetland or alpine. Step 3's layout must avoid those junctions.

## Adjacency (`procgen/regions/adjacency.go`, `defaultPresetAdjacency`)

Kept ENVIRONMENT.md's rows; added: alpine↔tundra 200, pine↔tundra 250 (the
treeline), wetland↔pine/prairie/oak/tundra/volcanic 150, wetland↔savannah
200, prairie↔pine 300, prairie↔alpine 250, prairie↔desert 200 (badlands),
prairie↔dunes 200 (sand hills), dunes↔savannah 200, volcanic↔alpine 200,
volcanic↔savannah 200 (East Africa), volcanic↔desert 150, volcanic↔dunes
150. Forbidden: wetland↔desert_canyon and wetland↔dunes (insert savannah).
Decided: volcanic↔wetland **allowed** (marsh at the foot of old flows);
dunes↔tundra stays forbidden (ENVIRONMENT.md). A tundra with five layers
first forced tundra↔savannah/prairie/oak/volcanic to be forbidden, which
broke desert↔tundra's own intermediate (savannah); tundra was cut to four
layers instead and a test now checks every intermediate is allowed beside
both sides (`TestAdjacencyIntermediatesAreAllowed`).

## The presets

Budgets are **measured** scatter + cover placements in the 512 m region
(0.262 km²), scaled to a km². Target was ~3,000/km² (PHASE_6: ~1 ms CPU per
thousand in view on the GTX 1070). Poly Haven ids were all checked against
the live API (`foundry polyhaven search -t textures|models`, User-Agent and
cache as the package does; thumbnails fetched 4 at a time).

### pine_forest — `$S/p2/sheets/pine_forest.png`
- **Landform**: rolling hills, relief 55 m, freq 0.0032, light droplets,
  talus 33°; edge rise 160 m.
- **Layers**: needles `forest_leaves_04`@2.5 (tinted 0.47/0.43/0.37), rock
  `lichen_rock`@9 triplanar. Two layers.
- **Assets**: `fir_tree_01` (3 firs, 14–19 m, 7 M tris → 300k each, thinned),
  `fir_sapling_medium` (→120k), `fern_02`, `grass_medium_01` (clearings),
  `dead_tree_trunk`, `tree_stump_01/02`, `rock_moss_set_01/02`.
- **Budget**: 1,096 → **4,180/km²** (firs 2,460) — over target.
- **Wrong**: an open stand of 15–25 m firs, not a closed forest; the
  needle floor shows its repeat from 40 m; many firs read thin and a few
  as dark bare poles (the fir_tree_01 "dead" part); `pine_tree_01` (17 M
  tris) was skipped — decimating it needs ~18 GB. Clearings (forest soil,
  forest ground) and scree in gullies all rendered as pale blotches and were
  dropped. Rendering is slow on WSLg (0.4–1.1 s/frame at 1280×720 with
  ~300k-tri firs) — not measured natively.

### alpine — `$S/p2/sheets/alpine.png`
- **Landform**: ridged ranges 180 m on 750 m spacing, U-valleys 0.45, talus
  37°; edge rise 220 m.
- **Layers**: scree `rocks_ground_01`@4.5 (everywhere), turf
  `rocks_ground_03`@3 (gentle ground), `lichen_rock`@9 (tint grey-blue),
  snow `snow_02`@4 above 70–110 m on slopes < 34–46°.
- **Assets**: `boulder_01`, `rock_face_01`, `rock_face_02`, `mountainside`,
  `namaqualand_rocks_01` (scree debris), `grass_medium_01` tufts.
- **Budget**: 95 → 360/km² (masks are narrow).
- **Wrong**: the rock still reads orange-brown with a visible lattice from
  afar; snow shows only on the highest peaks of a 512 m region (heights ≤
  145 m); placements are too few to read; no alpine flowers on Poly Haven.
  `aerial_grass_rock` turf rendered acid yellow-green and was replaced.

### coast — `$S/p2/sheets/coast.png`
- **Landform**: coastline toward +x, shore 170 m inside, meander 45 m, sea
  −1.5 m, shelf 120 m to −10 m, 35 m beach rising 2.5 m, 12 m cliff; water
  at −1.5 m.
- **Layers**: `coast_sand_01`@8, wet `damp_beach_sand`@3 (14 m / 0.9 m),
  `lichen_rock`@9. Three layers.
- **Assets**: `coast_land_rocks_02/03` (→150k), `coast_rocks_05`,
  `sand_rocks_small_01`, `island_tree_02`, `grass_medium_02` (dune grass),
  `shrub_sorrel_01`.
- **Budget**: 312 → 1,190/km².
- **Wrong**: reads as a sandy dune coast, not a rocky one — the wave-cut
  cliff is not visible in these poses and the land behind is sand
  (terrain_material has no rule telling a beach flat from an inland one; the
  scree washes paved the land and were dropped). Grass clumps are too small
  to read. Poly Haven's huge coastal cliff scans (`coastal_cliff_04`, 87 m,
  2.9 M tris) were not used. A faint line where the far water sheets meet
  the tile water is visible from 40 m.

### dunes — `$S/p2/sheets/dunes.png`
- **Landform**: transverse dunes 22 m, 140 m spacing, wind 30°, asymmetry
  0.85, sinuosity 0.6, cover 0.75, ripples 0.12 m / 5 m; edge rise 120 m.
- **Layers**: the desert's `red_sand`@3, `cracked_red_ground`@4.5,
  `cliff_side`@10 (no new layer).
- **Assets**: `grass_medium_02` tussocks, `namaqualand_boulder_04/05`.
- **Budget**: 51 → 190/km².
- **Wrong**: reads well. The desert's `dry_river_pebbles` paved every
  interdune corridor (the flow mask runs down them) and was dropped. Ripples
  are subtle at 1 m.

### volcanic — `$S/p2/sheets/volcanic.png`
- **Landform**: one cone 160 m / 240 m radius, crater 0.2 / 30 m deep, four
  lava lobes 260×45×5 m, relief 15 m; edge rise 150 m.
- **Layers**: cinders `coast_sand_05`@6 (tinted 0.42), bare lava
  `dark_rock_02`@3, basalt `dark_rock`@9 triplanar, ash `ground_grey`@3
  (shared with tundra) within 0.7–1.6 cone radii.
- **Assets**: SDF `rocks` boulders (granite, custom near-black — Poly Haven
  has no dark volcanic rock), `dead_tree_trunk_02`, `tree_stump_02`,
  `grass_medium_02`.
- **Budget**: 333 → 1,270/km².
- **Wrong**: the cone reads as a grey ash cone, the field round it as dark
  lava; no columnar basalt (the rocks stage has no column form; boulders
  only); lava lobes run straight, not downhill; `burned_ground_01` as ash
  read as straw and was replaced; the woolly moss of a lava field has no
  usable Poly Haven model (`moss_01` is 1–3 cm sprigs).

### tundra — `$S/p2/sheets/tundra.png`
- **Landform**: relief 10 m, hummocks 0.6 m / 7 m, thermokarst ponds 20%
  cover, 1.2 m deep, filled (`ponds`).
- **Layers**: turf `rocks_ground_03`@3 (tinted greener), frozen
  `ground_grey`@3, `lichen_rock`@9, snow `snow_02`@4 above 1.5–4.5 m.
- **Assets**: `grass_medium_02` (sedge), `shrub_04` (dwarf shrubs at
  0.35–0.55), `boulder_01`, `rock_moss_set_02`.
- **Budget**: 621 → 2,370/km².
- **Wrong**: reads as tundra from 40 m (olive turf, snow patches, ponds);
  from the ground the sedge and shrubs are too small to read; snow patches
  are flat white without texture variation.

### wetland — `$S/p2/sheets/wetland.png`
- **Landform**: base +0.1 m, relief 2.2 m, hummocks 0.35 m / 5 m, pond
  hollows 25%; water table −0.25 m plus ponds; edge rise 60 m.
- **Layers**: `leafy_grass`@3, `brown_mud_leaves_01`@3, `lichen_rock`@9, wet
  `brown_mud`@3 (6 m / 0.35 m).
- **Assets**: `grass_medium_01` scaled 3–5× as the **reed stand-in (Poly
  Haven has no reeds or rushes)**, `nettle_plant`, `weed_plant_02`,
  `shrub_02/03`, `island_tree_02`, `dead_tree_trunk`.
- **Budget**: 580 → 2,210/km².
- **Wrong**: reads as a marsh of channels and pools with mud margins; there
  are no visible reed beds — the stand-in clumps are lost at these
  distances. A base of −0.55 m flooded almost everything (`wet1`).

### tallgrass_prairie — `$S/p2/sheets/tallgrass_prairie.png`
- **Landform**: rolling plains, relief 22 m, freq 0.0017, drainage 2, fill
  0.04, no droplets.
- **Layers**: `leafy_grass`@3 (tinted 0.9/0.86/0.62), `withered_grass`@2.5
  (savannah's), `rock_boulder_dry`@6 (savannah's).
- **Assets**: `grass_medium_01` (×3.5–6), `grass_medium_02`, `dandelion_01`,
  `celandine_01`, `flower_heliophila`, `shrub_01`,
  `namaqualand_boulder_04/05`.
- **Budget**: 1,588 → **6,060/km²** (grass 5,400) — twice the target.
- **Wrong — honestly does not look like tallgrass prairie.** It is a green-
  gold grass texture with scattered clumps. Poly Haven's grass is 0.1–0.3 m
  tufts; one scenery placement each, even scaled 3.5–6× and at twice the
  budget, covers ~3% of the ground. A prairie needs a continuous 1–2 m sward:
  tens of thousands of clumps per km² (≈ 30–60 ms of CPU frame by PHASE_6's
  rate), or an instanced grass path. The stony draws and dry-grass flats
  paved half the plain and were reduced.

### oak_savannah — `$S/p2/sheets/oak_savannah.png`
- **Landform**: as the prairie, relief 28 m.
- **Layers**: the prairie's three.
- **Assets**: **no oak on Poly Haven** — `island_tree_01` (gnarled, wide
  crown; its mesh is 5 m, Poly Haven lists 12.5 m; placed 2–2.8×, 400k
  tris thinned) in clumps; `jacaranda_tree` (24 m as scanned, placed
  0.55–0.75, 150k) as the odd great tree; the prairie's grass at half
  density, flowers, boulders.
- **Budget**: 914 → 3,490/km².
- **Wrong**: reads well as open-grown trees over grass; distant crowns go
  pale in the haze; the grass has the prairie's problem at a smaller
  scale. `jacaranda_tree` at full resolution is ~3.9 M tris (a 208 MB
  buffer) despite the catalogue's 312k: the uncapped cook ran past 50 min
  and was capped at 150k.

### savannah (converted) — `$S/p2/sheets/savannah.png`
- Grass `grass_medium_02` ×2.5–4.5 in patches (0.3/100 m²),
  `searsia_burchellii` (2–3.4 m bushes as scanned, ×1.6–2.4) as the acacia
  stand-in in clumps; everything else as phase 6.
- **Budget**: 3,033 in the 512 m region (grass 908, acacias 78; the stones'
  absolute cap of 900 dominates a small region) → 11,570/km² here; in the
  phase-6 1 km world the grass adds ~0.3/100 m² × its area.
- **Wrong**: the dense biome grass of phase 6 is gone; clumps are sparse.

## Tests

New: `stages/environment_presets_test.go` — per preset: registered, valid,
deterministic, no vegetation stage, every plants stage thins, every
per-region stage expands, the terrain stage runs as a region at 4 m and
shows its landform (span/slope/vents/water); layer sharing (≤ 4 each, every
allowed pair ≤ 8, ≤ 32 overall); the base level. `phimport/
decimate_cards_test.go` (whole cards kept and enlarged, sheet slot
edge-collapsed, deterministic); `texcompress/mip_coverage_test.go`
(coverage never below level 0, alpha-weighted colour); `stages/
region_terrain_water_far_test.go` (sheets per side, no overlap, ponds and
non-coasts skipped; `sizedVariants`); `regions` adjacency intermediates;
`gencli` `TestEnvListShowsEveryBuiltInWithItsColour` (`gen env list` and
`gen list` show all 11 with colours). Updated deliberately: the savannah
preset test (above), `TestAdjacencyDefaults` (pair count).

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                                          clean
go vet ./internal/procgen/... ./internal/polyhaven/... ./internal/texcompress/ ./internal/gfx/   clean
go test ./internal/procgen/... ./internal/world/... ./internal/polyhaven/... ./internal/texcompress/ ./internal/gfx/   ok
go test ./internal/editor/...   fails only TestScoreSibeliusKeymap, TestScoreRebinding (the score session's)
go test ./internal/ -run TestPackagesDoNotDependUpward                  ok
```
Not run: the GL suites (`FOUNDRY_GL_TEST=1`). The coverage-mip upload path
was exercised only by the WSLg renders.

## Files

New: `procgen/stages/{environments_shared, environment_pine_forest,
environment_alpine, environment_coast, environment_dunes,
environment_volcanic, environment_tundra, environment_wetland,
environment_prairie, region_terrain_water_far}.go` + tests;
`polyhaven/phimport/decimate_cards.go` + test;
`texcompress/mip_coverage.go` + test.
Changed: `stages/environments_builtin.go` (savannah), `polyhaven.go`
(`thin_foliage`), `polyhaven_region.go` (import key), `scatter.go`
(`min_size`), `place_common.go`, `region_terrain_run.go`,
`region_terrain_water.go` (returns its material), `phimport/model.go`,
`gfx/texture_compress.go`, `regions/adjacency.go`, `gencli/gencli.go`,
`gencli_regions.go`; tests `canyon_savannah_test.go`,
`regions/regions_test.go`, `gencli_regions_test.go`; docs
`docs/guide/35-regions.md`.

## Gaps and honest notes

- **Grass at scale is not solved** without an instanced (non-scenery) grass
  path: prairie, savannah, wetland reeds and coast dune grass all read as a
  texture with sparse clumps. This is the main visual shortfall.
- Over budget: pine 4.2k/km², prairie 6.1k/km², savannah's small region.
- Height-band rules (coast dune grass, wetland reeds, volcanic moss band)
  use fractions of the union's height range, so in a multi-region world
  their bands shift with the neighbours' heights.
- One-region scratch worlds only; no two-region seams were rendered for the
  new presets (step 3's showcase will).
- `island_tree_01`/`fir_sapling_medium`/`jacaranda_tree` scale warnings:
  Poly Haven meshes are 0.4–0.6× their catalogue sizes; rules compensate.
- The `rocks` stage has no columnar-basalt form.
- WSLg frame times only (not representative); native numbers not taken.
