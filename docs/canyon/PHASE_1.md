# Phase 1: heightfield terrain and erosion — what was built

Phase 1 adds the `terrain` stage: a kilometre (or more) of eroded desert
canyon country as a heightfield, its masks for the later phases, and closed
Nanite tiles placed as mesh-collision scenery. It also makes the ground
queries (scatter, biome volumes) see mesh-collision scenery, makes
`foundry gen run` resolve `chunk: auto`, and fixes two things the tiles
exposed in the cook: tiles quantising on different grids, and the convex
decomposition dominating a dense tile's cook.

The user-facing description, with the full parameter table, is the
`terrain` section of [docs/guide/33-generators.md](../guide/33-generators.md).

## Packages and files

| Package | Files | What |
| --- | --- | --- |
| `internal/procgen/terrain` (new) | terrain.go, settings.go, shape.go, canyon.go, flood.go, fluvial.go, strata.go, droplet.go, thermal.go, detail.go, masks.go, meta.go; terrain_test.go, dump_test.go | The generator, pure CPU: imports only `procgen/noise` and yaml (layering-asserted). `Settings` → `Generate(s, pool, progress) (*Result, error)`; the file layout and `Terrain` (what later stages read). |
| `internal/procgen/stages` | terrain.go, terrain_run.go, terrain_preview.go; terrain_test.go, terrain_bench_test.go | The stage: schema, mapping onto `terrain.Settings`, the run (files, tiles, cook, placements), the preview, Load, the `terrain_demo` template. |
| `internal/biome` | ground_scenery.go (+ test); ground.go edited | `GroundQuery` casts against `collision: mesh` scenery. |
| `internal/levelio` | chunks_only.go | `LoadChunks(path)`: a level file's chunks alone, for the CLI's `chunk: auto`. |
| `internal/nanitecook` | quanthash.go; cook.go, mesh.go one line each | Position/UV/normal bits folded into the cook hash, only when set. |
| `internal/meshcollide` | build.go, meshcollide.go (+ test) | A cook whose `Settings.Mode` is mesh skips the convex decomposition. |
| `internal/procgen/meshout` | write.go, grid.go (+ test) | `CookOptions.PositionStep` + `QuantBitsFor`, `CookOptions.CollisionMesh`, `HeightGrid.UVOffset`. |
| `internal/procgen` | preview.go | `PreviewMesh.Image` + `UVs`: a mesh painted with one of the preview's image layers. |
| `internal/procgen/gencli` | gencli.go (+ test) | The file splice resolves `chunk: auto`. |
| `internal/editor` | editor_generator_view3d.go; editor_generator_terrain_test.go, editor_generator_terrain_gl_test.go | Preview meshes painted with their image; a `gl.Finish` after a preview rebuild (see gaps). |
| `internal` | layering_test.go | `procgen/terrain` may not reach gfx/model/nanitecook/world/render/editor/app/uiwin/procgen. |
| docs | docs/guide/33-generators.md, this file | |

No shared file from the spec's list was touched. `internal/layering_test.go`
got one additive map entry.

## The pipeline (internal/procgen/terrain)

A field is (N+1)² samples over a square centred on the origin. Generate runs,
in order (each file's header has the detail):

1. **Shape** (shape.go). Domain-warped FBM lowland, plus a **mesa field**: a
   low-frequency noise, warped twice (broad meanders; fine ragged rims scaled by
   roughness), cut at tier thresholds. Thresholds are measured quantiles of the
   field (so `mesa_coverage` means what it says on any seed), the rise's width
   comes from the measured gradient (so `mesa_rim` is metres), the rise sits
   mostly below the threshold (small plateaus are buttes with tops, not cones),
   and its width breathes along the rim (benches widen and pinch out). Optional
   joint network near rims (`mesa_joints`, off by default: it reads as tiles).
2. **Canyons** (canyon.go). Catmull-Rom densified path, meandered by noise of
   arc length (tapered at the ends), width ±20%. Floor = rim ground − depth,
   smoothed, forced non-increasing downstream, smoothed again (a moving average
   of a monotone sequence stays monotone) and clamped monotone once more.
   Tributaries walk out upstream at 35–70° with noise-steered headings, graded
   down to just above the main floor. The cut is `h = min(h, floor + (h −
   floor)(1 − p(u)))`, row-parallel over segments binned by row.
3. **Drainage** (fluvial.go, flood.go). On a coarse grid (`drainage_cell`):
   priority flood to the map edge → visit order and receivers; drainage area by
   **multiple flow directions** (shared by slope; single-receiver area made
   straight 0°/45° channels and V notches); implicit stream-power incision
   (Braun–Willett, unconditionally stable) scaled by the strata erodibility;
   pits fill toward their spill height; a little hillslope diffusion. The
   change is added back with a **cubic B-spline** upsample (bilinear left a
   visible 4 m lattice in the relief).
4. **Strata** (strata.go). Layers stacked from `strata_base`, repeating; strata
   coordinate = height + warp noise + dip. Terrace remap per layer: a hard
   layer's face climbs over the outer 1/(1.5+7k) of the band (nearly straight),
   turns at a sharp lip and holds a bench (8% of the old slope); soft layers stay
   slopes. Applied in proportion to the pre-terrace slope (8°→30°), so mesa
   tops and lowlands stay smooth. Monotonic, fixes layer boundaries (tested).
5. **Droplets** (droplet.go). Beyer/Lague droplets in metre units: volumes
   (m³) for load and capacity, rates per metre run, speed with drag. Erosion
   from a brush about the nearest sample, never below where the droplet goes
   (no pits); deposition never above the droplet (no spikes) — the first
   version without those limits blew up to ±4 km. Deterministic parallelism:
   2×2-coloured blocks of B = 2(steps + r + 2) cells, droplets confined to
   their block ± M; one goroutine per block, RNG keyed by (seed, pass, block).
6. **Thermal** (thermal.go). Jacobi talus, 8-neighbour pair fluxes of Rate/4
   of the excess, angle by hardness (talus angle → cliff angle).
7. **Detail** (detail.go). Ridged swells and an anisotropic Voronoi joint set
   on hard layers, fine bumps on soft ones, faded out by deposition; octaves
   shorter than two samples dropped.
8. **Masks** (masks.go): flow (full-resolution flood + D8 area, log scaled,
   widened 2.5 m), slope, deposition, cliff, strata index, strata height,
   curvature, albedo hint; and `Relief()` for pictures.

Everything is metres/degrees, so the preview (`Settings.Scaled`: coarser grid,
fewer droplet passes and talus rounds, a coarser drainage grid) shows the same
landforms.

## The stage's params

71 rows including list fields; the full table (generated from the schema) is in
the guide. Groups:

- grid: `size` 1024, `resolution` 1, `tile_size` 128, `seed`;
- base: `base_height`, `relief` 35, `frequency` 0.0035, `octaves` 6, `warp` 90;
- mesas: `mesa_height` 95, `mesa_coverage` 0.6, `mesa_scale` 520, `mesa_tiers` 2,
  `mesa_rim` 40, `mesa_roughness` 0.55, `mesa_top_relief` 3, `mesa_joints` 0,
  `mesa_joint_spacing` 70;
- strata: `strata` (list: thickness, hardness, color; 6 default layers),
  `strata_base` −20, `terrace` 0.85, `terrace_slope_lo`/`_hi` 8°/30°,
  `strata_warp` 3, `strata_dip` (0, 0), `resistance` 0.6;
- canyons: `canyons` (list: `path` spline x, z, width, depth; `profile`
  v|u|slot|stepped|custom; `section` curve; `floor` 0.15; `steps` 3;
  `meander` 40; `meander_length` 260; `tributaries` 4; `tributary_length` 220;
  `seed`);
- drainage: `drainage` 2, `drainage_iterations` 100, `drainage_cell` 4,
  `drainage_fill` 0.5, `drainage_diffusion` 0.1;
- droplets: `droplets` 1.5 /m², `droplet_lifetime` 48 m, `droplet_inertia`
  0.08, `droplet_capacity` 0.25, `droplet_erosion` 0.1, `droplet_deposition`
  0.1, `droplet_evaporation` 0.02, `droplet_radius` 1.5, `droplet_gravity` 4,
  `droplet_passes` 4;
- talus: `talus_angle` 34, `talus_cliff_angle` 80, `talus_iterations` 60,
  `talus_rate` 0.5;
- detail: `detail` 0.5 m, `detail_joint_spacing` 8;
- masks: `cliff_angle` 50, `sand_color`;
- output: `mesh_step` 1, `collision` mesh, `texture`, `uv_mode` world|terrain,
  `uv_scale` 0.125, `cook` true, `cook_memory` 3000 MB, `preview_cells` 256.

`TestTerrainSchemaDefaultsAreTheGeneratorsDefaults` pins that an empty
document runs exactly `terrain.Defaults()`.

## Output layout and metadata (what later phases consume)

In `resources/generated/<generator>/<stage>/`:

| File | Encoding |
| --- | --- |
| `terrain.yaml` | `terrain.Meta`, below |
| `heightmap.png` | 16-bit grey, (N+1)²; metres = `height.lo` + v/65535·(`height.hi` − `height.lo`) |
| `mask_flow.png` | 8-bit, `unit`: log drainage area, 0 at 4 m², 1 at 0.25 km², widened 2.5 m |
| `mask_slope.png` | `unit`: angle/90° |
| `mask_deposition.png` | `unit`: d/(d+1), d metres deposited (droplets, talus, basin fill) |
| `mask_cliff.png` | `unit`: smoothstep(cliff_angle ± 6°) of the slope |
| `mask_strata.png` | `index`: the 8-bit value IS the strata list index |
| `mask_strata_height.png` | `unit`: position within that layer, 0 bottom, 1 top |
| `mask_curvature.png` | `unit`: 0.5 flat, >0.5 concave, <0.5 convex (Laplacian over ~2 m) |
| `albedo.png` | RGB hint: layer colour ± noise, darker on cliffs, toward `sand_color` by deposition and flow |
| `relief.png` | RGB shaded relief (people only) |
| `tile_<i>_<j>.fnm` | cooked tiles (`.glb` with `cook: false`) |
| `placements.yaml` | the run engine's |

Pixel (i, j) is the sample at (`bounds.min_x` + i·`resolution`,
`bounds.min_z` + j·`resolution`); row 0 is min Z.

`terrain.yaml` (from the 0.5 m run, lists shortened):

```yaml
version: 1
size: 1024
resolution: 0.5
samples: 2049
bounds: {min_x: -512, min_z: -512, max_x: 512, max_z: 512, min_y: -19.91, max_y: 192.95}
height: {file: heightmap.png, lo: -19.91, hi: 192.95, encoding: u16}
masks:
- {name: flow, file: mask_flow.png, encoding: unit, doc: "drainage area, log-scaled: 0 at 4 m², 1 at 0.25 km²"}
- ... slope, deposition, cliff, strata (encoding: index), strata_height, curvature
albedo: albedo.png
relief: relief.png
strata:
  base: -20
  dip: [0, 0]          # m per 100 m along X, Z
  warp: 3              # strata coordinate = height + dip + warp noise
  layers: [{thickness: 30, hardness: 0.1, color: [0.62, 0.36, 0.27]}, ...]
tiles:
  size: 128            # tile (i, j) spans min_x + i·size .. ; placed at its centre
  count: 8             # a side
  base: -25            # the closed tiles' common bottom
  step: 0.00048828125  # the shared quantisation step (0 when not cooked)
  files: [tile_0_0.fnm, tile_1_0.fnm, ...]
  triangles: 134144    # per tile, before the cook
paths:                 # carved centrelines, downstream, ~every few metres
- {points: [[-560, -260], ...], width: [90, ...], floor: [82.1, ...]}
- {tributary: true, points: ..., width: ..., floor: ...}
```

Reading it from Go: `terrain.Load(dir)` (or the stage's `Load`, which
`rc.UpstreamArtifact(id)` calls) → `*terrain.Terrain` with `Meta`, `Grid`
(heights in memory), `Mask(name)` (lazily decoded, 0..1; strata = index/255),
`MaskAt(name, x, z)`, `HeightAt`, `NormalAt`, `Contains`. A run's own
artifact is the in-memory `terrain.FromResult`.

## Tiles and seams

- Each tile is a closed `meshout.HeightGrid` (top, skirts to `tiles.base`,
  bottom) in tile-local coordinates about its centre, placed at the centre with
  `collision` (default mesh) and `chunk: auto`. `ClosedSolid` is checked.
- `LockRect` on the tile rectangle pins the rim through the DAG.
- **Shared quantisation grid.** The cook's position step is a power of two
  from the mesh's largest extent, so a tall tile got a coarser grid than its
  neighbour and their shared rim vertices snapped to different points.
  `planTiles` picks one step from the tallest tile (`ext = max(tile_size, hi −
  base)`, step = 2^(⌊log2 ext⌋ − 18)) and `meshout.CookOptions.PositionStep`
  sets each tile's `PositionBits` so `nanite.NewQuant` lands exactly on it
  (`QuantBitsFor`, tested against NewQuant). PositionBits were not in the cook
  hash at all; now they are, only when set.
- `TestTerrainTilesMeetExactlyAtEveryLOD`: 2×2 cooked 32 m tiles at 0.5 m with
  a 120 m mesa (so the tiles' natural steps differ — asserted), read back from
  the .fnm, flattened, and for every shared edge the top-rim vertices' WORLD
  positions (float bits, after quantisation) compared at cut errors 0, 0.005,
  0.02, 0.1, 0.5, 2, 1e9 on each side independently, plus the full count
  per edge at every error. With the shared step switched off the same test
  fails (seam vertices 1 step apart) — checked by hand.
- **Mesh-only collision cook.** Profiling one 134k-triangle tile: 4.1 s wall,
  37 s CPU, 1.13 GB peak, 38% of the CPU in the convex decomposition, which a
  `collision: mesh` placement never uses. `meshcollide.Build` now skips it when
  `Settings.Mode` is mesh, `meshout.CookOptions.CollisionMesh` sets it, and the
  terrain stage does for `mesh`/`auto` tiles: 1.84 s, 14 s CPU, 131 MB. (Mode
  was already in the cook hash, so only these cooks change.) A placement that
  asks such a mesh for hulls falls back to its box.
- **Memory bound.** Cooks run at most `cook_memory` / (triangles × bytes per
  triangle) at a time (8500 B with hulls, 1000 mesh-only, measured), at least
  one, at most the workers.

## Ground queries

`biome.GroundQuery` (scatter height bakes, biome volumes) now also casts the
column against every world-frame, in-world scenery placement whose resolved
mode is mesh: per placement a world box reject, the column carried into mesh
space (affine inverse keeps t), `meshcollide.Resident.Raycast` (double-sided,
nearest), up-facing only (an overhang's underside is passed through, up to 8
times). The resident decode is lazy, once per mesh, `sync.Once`-guarded for the
parallel bakes. Tests: height within 8 cm on a tilted tile at an offset, normal
direction, off-tile and beyond-drop misses, `none`/`hulls`/chunk-off/ship-frame
not ground, and brush vs scenery picking the higher.

## CLI `chunk: auto`

`foundry gen run` now resolves `chunk: auto` for the file splice:
`levelio.LoadChunks` builds only the target level's chunks with GPU uploads
off, and `world.SceneryChunkAt` picks per placement — the editor splice's rule.
Only when some placement asks. Test: two chunks, four tiles, each lands in the
right one. Prefab-instanced chunks are not included (see gaps).

## Editor

Everything the stage shows goes through phase 0's generic views:

- 2D: relief as the base layer (readout = height), any of Height, Albedo, Flow,
  Slope, Deposition, Cliff, Strata, Strata height, Curvature as the overlay;
  carved canyon centrelines (tributaries paler), the tile grid, each canyon
  control point's half width as a circle. Canyon paths (a spline inside each
  `canyons` list item) are edited in the view with phase 0's spline tools.
- 3D: one preview mesh per tile painted with the albedo hint
  (`PreviewMesh.Image`/`UVs`, uploaded once per preview as one GL texture,
  released with the meshes); the lookdev texture picker still overrides.
- Tests: the whole window drawn over the terrain stage with every tree open,
  the layers uploaded, a mask overlay, the canyon path found and made active;
  GL: four tiles drawn with the image, mostly warm pixels.

## Tests

| Package | What |
| --- | --- |
| procgen/terrain | identical bytes on 1 and 13 workers, seed sensitivity, cancellation mid-drainage, progress monotonic, terrace monotonic/continuous/boundaries fixed, strata repeat and lookups, every profile falls centre→rim, canyon floors never rise (main + tributaries) and the canyon is cut, droplets neither dig pits nor raise spikes but do erode, soft ridges relax toward the talus angle while hard ones stand, every mask in 0..1 and not flat, Scaled keeps landforms; `TestDump` (env-gated picture writer) |
| procgen/stages | end to end (files, placements at tile centres, closed solids, meta, Load round trip of heights within one 16-bit step and every mask within half an 8-bit step, lookups, strata indices in range, rerun skips), determinism on 7 vs 1 workers, seams after quantisation at every cut (above), preview layers/meshes/paths/artifact, validation messages, cook concurrency bound, schema defaults = `terrain.Defaults()`; `TestTerrainBench` (env-gated full run) |
| procgen/meshout | `QuantBitsFor` gives exactly the step for 8 extents; rejects non-power-of-two and too-coarse steps |
| procgen/gencli | `chunk: auto` resolved in the level file |
| biome | the four ground-query tests above |
| meshcollide | a mesh-mode build has no hulls, validates, resolves auto→mesh and hulls→box |
| editor | the terrain window draw test; GL: tiles drawn with their image |

Results at the end of the phase:

- `go build ./...` and `go vet ./...` clean.
- `go test` passes for procgen/..., biome, meshcollide, nanitecook, levelio,
  world/..., editor, render/... and internal (layering).
- GL (`FOUNDRY_GL_TEST=1`): gfx and editor pass (the generator GL tests also
  pass run twice in one process). render: every test passes except
  `TestNaniteFoliageDrawsTheClassicNearField`, the failure phase 0 recorded as
  predating this work (403 s run, no GODEBUG needed any more).
- Note: during the phase the other session's work in progress broke the editor
  package's build and then its init (editor_score_keymap.go index out of
  range) for a while, so the `foundry` binary could not run. The timings were
  taken with `TestTerrainBench` (the same procgen.Run) in that window, and
  remade with `foundry gen run` once their build came back.

## Measured numbers (dev box: 20 threads, WSL2)

The `terrain_demo` template (1024 m, its canyon with 5 tributaries, defaults):

| Run | Wall | Peak RSS | Notes |
| --- | --- | --- | --- |
| 0.5 m, `foundry gen run` | 73.7 s | 2.17 GB | 2049² field 6.7 s; 64 tiles × 134,144 triangles cooked 20 at a time in 66 s |
| 0.5 m, `TestTerrainBench` | 73.9 s | 1.88 GB | shape 0.79 s, canyons 0.05, drainage 2.75, strata 0.02, droplets 1.43 (1.57 M), thermal 1.20, masks 1.49, images 0.55, cook 65.4 |
| 0.5 m, before the mesh-only cook | 183.7 s | 3.33 GB | cook-memory bound held it to 3 cooks at once |
| 1 m, `TestTerrainBench` | 20.1 s | 0.52 GB | field 4.5 s (drainage 2.95), cook 15.4 s (64 × 34,304 triangles) |
| one 128 m tile at 0.5 m, cook | 1.84 s / 14 s CPU / 131 MB | | was 4.1 s / 37 s / 1.13 GB with hulls |
| preview, 128 / 256 / 512 samples | 0.16 / 0.32 / 1.0 s | | after reusing the flood's buffers, linear MFD weights and a 2× preview drainage cell (was 0.33 / 1.0 / 1.4) |

After the flood speed-up (buffers reused, linear MFD weights) the drainage
phase is 1.45 s at 1 m and 1.66 s at 0.5 m (was 2.8 s). The final 0.5 m
`TestTerrainBench`, with every default as shipped: 76.9 s, 1.93 GB peak, field
7.6 s, cook 68.7 s.

Phase 0's 1 km at 1 m (noise_preview, 33k-triangle tiles, hull cooks) was 31
s and 3.6 GB; the terrain stage's is 20 s and 0.5 GB.

## Pictures inspected

All in the session scratchpad
(`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/8b1c0d12-038d-4935-9ad4-2bf2380ff5b6/scratchpad/`):

- `p1/` is a scratch project (shaders and textures symlinked to the repo's)
  with `generators/canyon.fgen` (the template at 0.5 m, painted with its own
  albedo hint through `uv_mode: terrain`) and `levels/canyon.yaml`.
- `p1/resources/generated/canyon/ground/relief.png`, `heightmap.png`,
  `albedo.png`, `mask_*.png` — the 0.5 m CLI run; `masks05.png` puts flow,
  cliff, deposition, curvature, strata and albedo side by side; `r05_full.png`
  is the relief downsampled.
- `v/<name>/relief.png` and the `cmp.png` composites — the tuning runs from
  `TestDump` (seed 1234), e.g. `v/d3` (0.5 m, final droplets), `v/e1` (1 m,
  final defaults).
- `shots/r1.png` … `r4.png` (and `grid4.png`): `foundry play run` in the
  scratch level — a rim view across buttes, the canyon floor looking
  downstream, an overview of the whole kilometre, a wall with its talus;
  `shots/a.png`, `b.png`, `c.png` from earlier iterations.

What the tuning changed, in order, and why (each from looking): droplet
volumes/limits (pits and spikes); a Canyonlands-like six-layer stack instead of
many thin layers (contour-line terracing); terrace gated by slope (stepped
lowlands); sharp-lipped benches (rounded "melted wax" rims); B-spline drainage
upsample (4 m lattice); MFD drainage (straight V notches); rim width breathing
and bench-scale rim noise (cobbled ledges, uniform contour bands); asymmetric
tier rise (conical buttes); mesa coverage 0.6 and rim 40 m (canyon through
tableland, compact escarpments); 1.5 droplets/m² at capacity 0.25 (fluted talus);
an anisotropic joint set (flagstone cracks).

## Known gaps

- **Appearance is the heightfield's.** Vertical and overhanging walls, boulder
  aprons and rock detail are phases 2–3; the tiles' look on walls is a
  top-down projection (stretched texels) until phase 5's triplanar/splat
  materials. The canyon walls are steep (terrace + cliff angle 80°) but a
  heightfield cannot undercut.
- **The flow mask** is D8 area on the final surface: on planar slopes it shows
  faint parallel streaks. The drainage phase itself uses MFD.
- **Drainage is serial** (one flood per iteration, ~14 ms at 257²). Fine at
  these sizes; a 4 km map at 4 m drainage cells would want a parallel router.
- **Droplet RNG layout depends on block size**, i.e. on resolution and droplet
  run: changing those changes the droplets' pattern (deterministically).
- **Joints** (`mesa_joints`) are a Voronoi network; strong values read as
  tiles, so the default is 0. Orthogonal joint sets (the Needles) are not
  modelled.
- **Map edges.** Tiles at the edge show their skirts as walls from outside the
  map; the base is a flat slab under the whole field. Surround terrain (or an
  edge falloff) is for phase 7's template.
- **Decals do not clip scenery** (spec: optional; not done).
- **`chunk: auto` in the CLI** builds only the level's own `chunks:`;
  prefab-instanced chunks are not considered.
- **A hulls override on a mesh-only tile** collides as its box (documented);
  re-cook with `collision: hulls` to get hulls.
- **GL driver deadlock.** On WSLg's Mesa d3d12 a second large preview drawn and
  read back in one process deadlocked in `glReadPixels` unless the uploads were
  finished first; `genEnsureView3D` now calls `gl.Finish()` once per rebuilt
  preview. Not seen in a window, but the same uploads happen there.
- **Lighting in the screenshots** is the harness's (no TAA, so VSM shadows are
  not representative); judge final looks on the 1070 with phase 6's
  atmosphere.
- The phase-0 gap on `list_assets` still stands (no change here).
