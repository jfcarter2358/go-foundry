# Environment presets — step 3: the biome showcase world

Status: built in the user's OneDrive project, 2026-10-13. Generated end to end
with `foundry gen run`, rendered from every region (a ground pose and a 40 m
pose) and from every adjacent pair's seam, natively on the GTX 1070, and
looked at. Two engine bugs found on the way were fixed (with tests). The world
loads, `foundry check` is clean, the viewpoint script plays. **It is not fast**:
55–85 ms a frame at 2560×1080 where the scanned scenery is in view (numbers
below). Nothing was clicked in a running editor.

`$S` is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.

## Layout

Picture: **`$S/p3s/sheets/layout.png`** (relief + biome overlay, +x right,
+z up, spawn in red). Generator: `$S/p3s/tools/layout.py` (a planar
subdivision: shared edges are jittered identically for both neighbours, so no
gaps or overlaps) and `mkshow.py` (writes the doc).

**The union terrain is always a square** (region_terrain's grid is
`size × size` round the regions' bounds): a 3.1 × 1.9 km first layout became
a 3.3 km square — 11 km², twice the ground, half of it outside every
footprint. So the world is a **2.56 km square** (20 × 20 tiles of 128 m, 6.55
km², the requested area), at 1 m.

| Region | Environment | Footprint (approx.) | Transition |
| --- | --- | --- | --- |
| dunes | dunes | x −1280…−280, z −1280…−780 | 200 |
| desert | desert_canyon (lead) | x −1280…−780, z −780…1280 | 200 |
| savannah | savannah | x −780…−280, z −780…1280 | 200 |
| prairie | tallgrass_prairie | x −280…330, z −1280…−250 (less the wetland) | 150 |
| oak_savannah | oak_savannah | x −280…330, z −250…880 (less the wetland) | 150 |
| wetland | wetland | x −20…330, z −330…320 | 150 |
| pine_forest | pine_forest | x 330…780, z −1280…880 | 150 |
| coast | coast, sea to +z (`generators/biome_showcase_coast.fgen`) | x −280…1280, z 880…1280 | 100 |
| volcanic | volcanic | x 780…1280, z 380…880 | 150 |
| tundra | tundra | x 780…1280, z −300…380 | 200 |
| alpine | alpine | x 780…1280, z −1280…−300 | 200 |

Chains: dunes–desert–savannah–oak/prairie–pine–alpine–tundra; coast along the
north edge; wetland in the temperate middle; volcanic beside the forest.

**Why explicit transitions.** At the table's widths (300 m forest/prairie
bands) every 450–600 m region is band from side to side and many tiles mix
three or four presets: 29–35 tiles needed 9–11 layers. Every region carries an
explicit `transition` (150/200, coast 100). The tile check was done first in
Python (reach = band/2 + 45 m warp, tile ±33 m) and then by the engine at 4 m
in `$S/bs` (35 s): **no forbidden pair** (`gen check` and the run print none),
**23-layer palette, 55 per-tile materials**, no tile over 8. One more failure
showed only at the 1 m run: a 512 m backdrop ring tile (`far_8_2`) mixed
prairie/pine/alpine (9) — `far_tile: 256` fixed it.

## Project files (OneDrive `resources/`)

Created: `environments/*.fgen` (the 11 presets, none existed),
`generators/biome_showcase_world.fgen` (+ `.bak` from the tool),
`generators/biome_showcase_coast.fgen`, `levels/biome_showcase.yaml` (+ `.bak`),
`scripts/biome_showcase/level.flang`, `generated/biome_showcase_world/`, 55
`materials/gen_biome_showcase_world_*.fmat` + water, Poly Haven imports under
`models/polyhaven/` and `textures/`.

Edits to the freshly written presets (they are used only by region worlds):
`resolution: 1k` on every Poly Haven stage whose assets were all new to the
project (the palette packs each layer at 512 anyway; assets already imported
for the user's desert_canyon level stay 2k so their files are not rewritten —
`$S/p3s/tools/res1k.py`); pine `firs` density 0.32→0.12, fir 300k→50k and
sapling 120k→20k triangles; desert `talus` 5→1.2, `bushes` 0.6→0.15,
`boulder_field` 0.9→0.3; savannah `shrubs` 0.25→0.08; volcanic `ash_radius`
0.7/1.6→0.35/0.8 (ash covered the whole region); tundra `dwarf_shrubs`
`thin_foliage` false (below). Coast override: `coast: +z`, `coast_meander`
45→110 (the shore was a ruled line).

**Pre-existing files touched**: the shared desert assets' `polyhaven.yaml`
and `.bin` were rewritten by the import (same bytes for the `.bin`s; three
provenance files list extra mesh variants). `shrub_04.fnm/.gltf` were
**rewritten with `_cov` textures** by the tundra's `thin_foliage` import —
that changed the user's desert_canyon level's shrubs; turning thin_foliage
off for it restored them byte-for-byte in size (992153 / 2457 / 2562) and the
stray `ph_shrub_04_cov*` textures were deleted. Engine hazard, not fixed: a
`thin_foliage` import of an asset another stage imports plainly rewrites the
shared files (they would flip on every run).

## Level

Spawn (−60, 120), yaw 10°: tallgrass with the wetland pools, the pine line,
the alpine range and the volcano in one view (`final_spawn_overview.png`).
Environment: gradient sky, sun yaw 40 pitch −38 ×1.5, HDR exposure 0.75,
height fog 0.0022 neutral grey, aerial 2.6 km, ambient 0.42, view 5 km.

**Viewpoints** (`scripts/biome_showcase/level.flang`, attached via `scripts:`):
**Page Down / Page Up** cycle 12 viewpoints (one per region + an overview
from 600 m) — the player is moved there and the camera framed; **End** gives
the camera back. Played with `foundry play` (log: `viewpoint 1 of 12: desert
canyon` … `7 of 12: pine forest`; shots `$S/p3s/play/shots/`).
`foundry check biome_showcase -scripts biome_showcase`: **all clear**
(other project scripts have 498 pre-existing lint problems).

## Runs, disk

| Run | Wall | Peak RSS | What |
| --- | --- | --- | --- |
| first (failed at ground_mat, far tile) | 3 m 46 s | — | |
| full | 37 m 4 s | 4.1 GB | 84 stages, 26,015 placements, Poly Haven imports |
| retune (firs, densities) | 13 m 55 s | 4.0 GB | 30 stages |
| final trims / spawn / shrub | 1 m 11 s, 16 s, 24 s | 0.6 GB | |

Final: 14,303 scenery + 527 grass/region volumes + 11 region volumes.
Ground stage at 1 m: 2 m 48 s (400 tiles cooked 1 m 45 s; 608 ring tiles).

**Disk** (`du resources/`): before **2,329 MB**, after **4,692 MB → +2.36
GB** (generated/biome_showcase_world 1,399 MB, of it ground 973 MB and the
desert wall sheets 274 MB; models +581 MB; textures +381 MB; levels +12 MB).
Removed after the run: the stale 300k/120k fir meshes and the two raw
downloads `fir_tree_01.bin` (478 MB) and `jacaranda_tree.bin` (208 MB) — not
read at run time (the level uses the cooked `.fnm`); a rerun of those import
stages copies them back from the download cache.

## Renders and contact sheets (native GTX 1070, 1280×720)

`$S/p3s/sheets/final_regions_1.png`, `final_regions_2.png` (ground + 40 m in
every region), `final_seams_1.png`, `final_seams_2.png` (all 21 adjacent
pairs from the ground), `final_spawn_overview.png`, `layout.png`. First pass
(before the fixes): `v1_*.png`. Single frames: `$S/p3s/native/fin/named/`.

WSLg (Mesa d3d12) could not be trusted for this world: whole black frames
(below) and the same holes; the renders used were native.

What the renders showed, and what happened:

| Seen | Cause | Done |
| --- | --- | --- |
| Whole frames black on WSLg | the scenery cull's command buffer (20 B a slot; 3,668 300k-triangle firs ≈ 8 M slots) passed Mesa's 128 MB storage-block limit | firs cut; **engine: a once-a-run warning** (`render/nanite_cull_block.go`) |
| Terrain tiles missing (square holes to the void) natively and on WSLg, near and far | **engine bug**: the draw bound's LOD keys were built from the cluster records as the residency left them AT LOAD; a page not resident then never counted, and when it streamed in its commands were cut off past the bound | **fixed** (`render/nanite_stream.go` `staticClusterRecords`, `nanite_scenery.go`): keys from the cooked errors. Verified A/B (`FOUNDRY_NANITE_DRAW_LOD_BOUND=0`, stream off and a 3 GB pool all hid it; the fix does too) |
| 5–15 fps | scanned scenery in view (below) | densities and fir triangles cut |
| Whole volcanic region pale | ash cover radius 1.6 cones | 0.35–0.8 |
| Straight shoreline | coast meander 45 m | 110 m |
| Desert poses into a wall | pose choice | re-posed |

Seams: no tile lines, steps or density cliffs at any of the 21 joins; bands
read as blends (dunes→prairie grass on red sand, savannah→oak gold to green,
pine→tundra ferns thinning to turf).

## Native numbers (GTX 1070, 2560×1080, user graphics.yaml, 60 + 120 frames)

`docs/regions/perf_presets3/final_user_2560.txt` (before the last trim:
`before_trim_user_2560.txt`). ms, GPU sections averaged:

| Pose | p50 | p95 | CPU | GPU | scenery | occlusion | material | sun | grass (instances) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| spawn | 55.7 | 57.3 | 26.7 | 55.6 | 25.2 | 18.2 | 8.6 | 1.1 | 1.4 (18.6 k) |
| prairie ground | 49.4 | 52.0 | 21.5 | 49.4 | 20.4 | 15.2 | 8.4 | 1.1 | 3.3 (41.6 k) |
| pine ground | 84.7 | 86.7 | 29.9 | 84.5 | 37.7 | 33.4 | 9.5 | 1.0 | 1.7 (12.1 k) |
| desert ground | 84.5 | 85.6 | 27.3 | 84.4 | 39.7 | 31.8 | 11.3 | 0.5 | 0.0 |
| tundra 40 m | 23.4 | 24.7 | 17.7 | 23.8 | 7.1 | 4.2 | 11.3 | 0.1 | 0.1 |
| volcanic ground | 19.8 | 20.8 | 16.9 | 20.0 | 3.9 | 2.9 | 11.4 | 0.7 | 0.0 |

A/B on the pre-trim level (`ab_*.txt`): **without any scanned scenery the
same views run 15–17 ms** (terrain, palette material, water, grass); without
firs, spawn 26 ms; without the desert wall sheets, no change. So the cost is
the Nanite cull + raster of ~15 k scattered scans over 6.5 km² seen across a
5 km view: there is no per-placement draw distance, so every boulder, bush and
fir in the frustum is culled cluster by cluster. Load: 24 s native.

## Still wrong (honest)

- **Frame rate**: 20–85 ms. Needs an engine draw-distance/impostor path for
  small scattered scans (or chunk streaming of placements by distance), not
  more content cuts.
- **Fir trunks render white** natively (black on Mesa) on part of
  `fir_tree_01` — on both raster paths, with valid textures; the "dead"
  `trunk_c`/`bark` slots are suspects. Very visible in the pine forest from
  40 m. Not root-caused.
- **Overview (600 m up)**: white blocks over the pine/tundra/volcanic scans
  (ground-only level is clean; not the draw bound, occlusion, VSM or pool
  size). Not root-caused; likely the same as the white trunks.
- **VSM artifacts**: a fan-shaped dark patch near the camera at 40 m on open
  ground (gone with `FOUNDRY_NANITE_VSM=0`) and floating dark blobs over
  tundra/coast ground (gone with grass hidden, even though those swards do
  not cast).
- **Pine forest is open**, not a closed canopy (density cut for frame time);
  firs read thin.
- **Alpine shows no snow** (its 500 m region tops out ~140 m; snow line
  70–110 m absolute is mostly not reached under the edge rise); volcanic cone
  reads pale grey with dark strata bands (the desert lead's banding on its
  rock).
- **Savannah ground is pale**, near white sand from 40 m, not gold.
- The layout is visibly columnar from the overview.
- Headless `foundry sim`/`check` load takes 51 s from a local copy but
  **4.6 min from OneDrive** (9P), 90 % of it Go GC scanning the scenery load
  (`SyncPlacementChunks`); WSLg `foundry play` loads in ~80 s.
- Union is square-only (see layout).

## Engine changes (repo)

- `fileutil.WriteFileAtomicNoBackup`; `nanite.Mesh.WriteNoBackup`;
  `nanitecook.CookMeshFile` uses it — a generator's recooked meshes no longer
  leave a `.fnm.bak` of every tile (it doubled the output folder on reruns;
  test in `nanitecook/lock_test.go`).
- `render/nanite_stream.go` `staticClusterRecords` + `nanite_scenery.go`:
  the draw bound's keys from cooked errors (the missing-tile fix); test
  `render/nanite_draw_lod_bound_stream_test.go` (shows the load-time records
  under-count, the cooked ones never do).
- `render/nanite_cull_block.go` + test: warn once when a cull command buffer
  passes the driver's storage-block limit.

Tests: `go build ./...`; `go vet` nanite, nanitecook, fileutil, render;
`go test` nanite, nanitecook, fileutil, render, world, levelio, procgen/...
ok; editor fails only TestScoreSibeliusKeymap/TestScoreRebinding (score
session); layering ok; `FOUNDRY_GL_TEST=1 go test ./internal/render/ -run
'TestNaniteStream|TestNaniteDrawBound|TestNaniteVis|TestNaniteScenery'` ok
(including TestNaniteStreamStreamsInFromTheRoots, previously listed as
known-failing).

Windows bench copy `%LOCALAPPDATA%\Temp\foundry-showcase-perf` deleted;
nothing left running. The project's `resources/shaders` is empty (the editor
build supplies shaders), so nothing there was stale; renders used the repo's
shaders through `$S/showl` (a local copy) and the bench copy.
