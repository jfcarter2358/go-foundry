# Phase 3: placement — what was built

Phase 3 adds the three placement stages — `scatter` (rule lists of rocks over
a terrain), `cliff_wall` (cliff chunks stacked up the heightfield's walls) and
`vegetation` (the engine's biomes grown as `kind: biome` volumes on the
terrain's tiles) — instanced preview meshes in the Generator editor, budgeted
placement dots in its 2D view, and a Nanite cull fix that makes a level of a
thousand hero cliffs cost what is in view rather than what is in the level.

The user-facing description, with the full parameter tables, is the
`scatter`, `cliff_wall` and `vegetation` sections of
[docs/guide/33-generators.md](../guide/33-generators.md).

## Packages and files

| Package | Files | What |
| --- | --- | --- |
| `internal/procgen/place` (new) | place.go, ground.go, spatial.go, scatter.go, trace.go, wall.go, vegetation.go; place_test.go | The placement itself, pure computation: imports only `procgen/terrain` and `procgen/noise` (layering-asserted). `Ground` (heights, masks, footprint-fitted normals, the cliff field), `Scatter`, `TraceCliffFeet`, `SplineLines`, `Walls`, `Patches`; `Variant` / `Instance` / `Disc`; `Mat3` rotations and `EulerYXZ` (world.EulerMatrixYXZ's order). |
| `internal/procgen/stages` | place_common.go, scatter.go, scatter_run.go, cliffwall.go, vegetation.go, placement_demo.go; scatter_test.go | The three stage kinds (schema, run, preview, Load), the shared band params, variants from a `RocksArtifact`, placements and instance matrices, the decimated preview proxy, the `placement_demo` template. |
| `internal/procgen` | preview.go | `PreviewMesh.Instances [][16]float32` (column-major mesh-to-world matrices) and `PreviewMesh.InstanceBounds`. Additive. |
| `internal/gfx` | material_surface.go | `Geometry.DrawInstancedThroughProgram(prog, first, count)` beside `DrawThroughProgram`. Additive. |
| `internal/editor` | editor_generator_view3d.go, editor_generator_view2d.go; editor_generator_scatter_test.go, editor_generator_scatter_gl_test.go | Instanced preview meshes (an instance buffer per mesh at the world program's instance attributes, framed by their instance bounds, counted in the overlay); the 2D view's points drawn within a vertex budget. |
| `internal/render` | nanite_live_tiles.go (new), nanite_cull.go; nanite_live_tiles_gl_test.go | The live-tile list (below). |
| `resources/shaders` | nanite_live_tiles_comp.glsl (new), nanite_range_cull_comp.glsl, nanite_compact_comp.glsl, nanite_cluster_common.glsl | The list's two stages; the range cull and the range scatter take their bound from the list's length when `liveList` is set. |
| `internal` | layering_test.go | the `procgen/place` rule. |
| docs | this file, docs/guide/33-generators.md | |

No shared file from the spec's list was touched.

## scatter

`place.Scatter` (scatter.go's header has the whole method):

1. **Weight.** Per rule, a soft band per mask — `slope` (degrees, from the
   footprint-free heights), `height` (fraction of the terrain's range),
   `flow`, `deposition`, `curvature`, `cliff` — times the **cliff-base**
   term and a clumping noise. The cliff field (ground.go) is a two-pass
   8-neighbour chamfer over the cliff mask that carries each sample's nearest
   cliff sample; a point is at a cliff's FOOT when the way to that cliff runs
   with the cliff's height gradient (uphill), at its rim when it runs against
   it — so talus gathers under walls, not on top of them.
2. **Candidates** per 32 m block, side by side on the worker pool, each block
   its own RNG stream (seed, rule, block): the block's weight integrated over
   2 m cells gives its expected count at `density` (per 100 m² of full
   weight); candidates drop into cells in proportion to weight. Satellites
   add candidates round every rock the earlier rules kept (stream per host).
   Every candidate draws variant, power-law scale (truncated Pareto,
   exponent `scale_bias`), yaw, tilt axis and angle, sink jitter up front.
3. **Keep**: serial, biggest footprint first (ties by draw order), against a
   hash grid of every footprint so far (this rule's, earlier rules', the
   `avoid` stage's): a centre must be at least max(spacing) × (r₁ + r₂) from
   every other — a variable-radius Poisson disk.
4. **Seat**: align to the normal of the plane fitted over the footprint
   circle, tilt, yaw; carry the footprint ring (12 points at half the base
   band) through the rotation and lower the rock until no ring point and not
   the centre is above the ground; sink `sink` × height × (0.6..1.4) (× 0.4 for
   a flat-based variant). A candidate whose ring needs more than `max_bury` ×
   height between its lowest and highest point is dropped.
5. **Collision by size**: under `collision_min` none, hulls up to `hull_max`
   when the variant was cooked with hulls, else mesh (or the rule's explicit
   choice).

The stage writes placements `<rule>_<nnnnn>` (uniform scale, Euler YXZ,
`chunk: auto`) and `footprints.yaml` (`version`, `discs: [[x, z, r], ...]`,
per-rule kept/candidates/area), which `loadFootprints` reads back for a later
stage's `avoid`. A rule's `max_triangles` keeps the source's variants under
that budget (or its lightest one) — for scan sets that mix 100k and 2.4M
triangles.

**Rock sources.** `rocks` refs accept `RockSources = ["rocks",
"polyhaven_rocks"]`, and the stages read the upstream ARTIFACT
(`*RocksArtifact`), never the kind: a later scan stage that writes the same
`rocks.yaml` plugs in by joining that list.

## cliff_wall

`place.Walls` (wall.go's header):

- **Lines**: `TraceCliffFeet` — the cliff mask averaged onto a 2 m grid,
  marching squares (saddles decided by the centre, the same rule each cell),
  lines walked through shared edge IDs in ID order, split where the band's
  outward normal stops pointing downhill (a foot, not a rim), dropped under
  `min_length`, smoothed and resampled at 1 m; or `SplineLines` from authored
  splines with a side (auto: the downhill side by majority). Optional region
  clip.
- **Measuring** (`measureWall`): a 0.25 m transect across the line, ±`search`
  m; the steepest point must reach `wall_slope` (60°); outward to `foot_slope`
  (40°) is the foot, inward to `rim_slope` (48°) the rim; the profile is the
  face's distance in from the foot at each height. (With the first defaults,
  32°/28°, a transect ran on up the talus slope above a cliff band to the mesa
  top, and the columns stacked chunks up the slope as a facade — fixed by
  looking.)
- **Seating** (`seatChunk`): the chunk's **wall frame** (`backFrame`): turned
  so its back direction (the manifest's `back`, default −Z) faces into the
  wall, its bounding box in that frame gives the back plane (the box's face
  across the back direction), depth, lateral centre and width — so a scan's
  irregular back is seated by its box and buried by `inset`. Lean follows the
  face (`lean` × the face's lean over the tier, ≤ `max_lean`). The back's
  distance in is bounded by "buried by `inset` everywhere" and "front
  `protrude` × depth proud of the face everywhere", read at three transects
  across the width; the middle of the two, or the first when the face leans
  too far for both.
- **Tiers**: the count that fits the wall at the nominal scale with `overlap`
  (≤ `tiers`), then the scale that spans foot to rim; odd tiers half a column
  along; per-chunk variant, yaw/roll/along/drop/scale jitter.
- **Ledges** jut from a tier's top or cap the rim; placed at the wall's front
  at their height: on a chunk, the nearer of its front and the face; above the
  tiers, the face. (Before: a rim ledge took the last chunk's front at the
  rim's height and floated up to 55 m out in the air — found in the level by
  listing every placement's height over the ground; pinned by
  `TestLedgesSitOnTheWall` on a cliff-under-a-slope ground, which fails with
  the old rule.)
- **Budget**: every column of every line is surveyed first; lines are cut
  into `stretch` (60 m) stretches scored by their columns' mean wall height,
  and walled highest first, each whole and in order, each column from its own
  RNG stream (seed, line, column), until `max_chunks`. (Longest line first
  spent the whole cap on one 2 km line; column by column, tallest first, dotted
  lone columns over the faces like stacked crates.) Chunks of different lines
  that would cross are dropped.

## vegetation

`place.Patches`: the rule's weight (the scatter bands) on a `cell` grid (mean
of 4×4 samples), cells over `threshold` merged into rectangles — runs along
each row carried down the rows while the same run continues, at most
`max_size` a side — each with its ground's height range. Each becomes a volume
`{id, position, half, scatter: {kind: biome, biome: {preset, density, amounts,
tint}}}`; it is decoded through the level's strict schema by the splice
(tested). **Verified in the engine**: the bench pictures show the desert
biome's dead bushes, barrel cacti and saguaros standing on the cooked terrain
tiles (phase 1's GroundQuery over mesh-collision scenery). `world.Tree`
placements were not needed and are not emitted.

## Editor

- **Instancing.** `PreviewMesh.Instances` → `genUploadInstances` (one
  STATIC_DRAW buffer per mesh, `gfx.AttachInstanceMatrices` on its VAO) →
  `DrawInstancedThroughProgram` (`instanced` = 1, one
  glDraw*InstancedBaseInstance). The scatter preview's proxies are the
  previewed variant decimated by the cluster simplifier to
  `preview_triangles` (240). Framing uses the instance bounds. Overlay: "N
  meshes, M instances, T triangles".
- **2D dots.** One `PointLayer` per rule in its colour (white → a palette by
  rule index). `genDrawPoints` draws only on-screen dots, each an unrounded
  3 px square, a footprint circle only from 3 px radius (8–32 segments), all
  within a 40,000-vertex budget: ImGui's draw lists index with 16 bits here,
  and 60,000 dots drawn the old way tripped its "too many vertices" assert
  (`AddRectFilled`'s default rounding alone made each dot an arc's worth).
  What did not fit is counted on screen ("N dots not drawn: zoom in").

## The per-placement cost and the fix (render/nanite_live_tiles.go)

**Diagnosis**, on the scratch canyon (below), WSLg, 1920×1080, p50 frame:

| level | wall view | sky view |
| --- | --- | --- |
| terrain only (64 tiles, 20,762 range tiles) | 29.7 ms | 24.8 ms |
| + 7,069 scattered rocks (45,774 tiles) | 58.2 | — |
| + 1,265 cliff chunks and ledges, no scatter (287,125 tiles) | 140.1 | 121.3 |
| + 434 vegetation volumes, no rocks | 38.9 | — |
| everything | 175.3 | — |

Looking at the empty sky cost 121 ms with the walls: per-tile work paid for
instances nobody sees. A/B on the walls' sky view: occlusion off 55.8 ms
(two passes → one); packing off 142.5; visibility buffer off 123.7; the range
cull's thread **returning early for a culled instance: 124.8 (no change)**;
skipping the range dispatch outright: 31.0. So the cost is the launch of a
thread per tile per pass (the range cull's per-thread arrays keep its
occupancy low), not what the thread does.

**Fix**: a live-tile list. Range tiles are filed by RUN (one instance's
consecutive tiles in one group). Each pass, after the instance and LOD stages,
`LIVE_LIST` (a thread a run) appends a visible run's tiles to a list via an
atomic add; `LIVE_ARGS` writes the indirect dispatch; the range cull and the
packing's range scatter run `glDispatchComputeIndirect` over the list. Command
slots stay fixed. A run going out of view has its slots and survivor counts
zeroed once (`runLive`), and in pass 2 its clusters' history zeroed once
(`runSeen`) — exactly what the full dispatch would have left. (The first
version cleared the whole command buffer and the counts every pass instead:
correct, and ~10 ms a frame slower on WSLg over the canyon's passes — 59.8 vs
48.1 ms.) `FOUNDRY_NANITE_LIVE_TILES=0` restores the old dispatch, which is
kept, as is every other path. Debug views (cluster tiles) never take it.

**After**, same poses:

| level | wall view | sky view |
| --- | --- | --- |
| terrain only | 24.9 ms | — |
| walls only (287k tiles) | 140.1 → 48.1 | 121.3 → 21.4 |
| scatter only | 58.2 → 45.3 | 27.7 |
| everything (that config) | 175.3 → 68.2 | |

Final scratch level (13,868 rocks + 1,005–1,038 chunks + ~190 ledges, 330,008
range tiles, 434 volumes): wall view 189.4 → 78.9 ms, sky 166.1 → 45.8,
overview of the canyon 204.5 → 127.4 (`FOUNDRY_NANITE_LIVE_TILES=0` vs on).

Tile cost per placement measured (`FOUNDRY_NANITE_STATS=1
FOUNDRY_NANITE_STATS_LOG=1`): ~3.1–3.5 range tiles per 600/2,500-triangle
rock, ~210 per 120k-triangle cliff chunk, 324 per 134k-triangle terrain tile.

**Tests**: `TestNaniteLiveTilesWriteWhatEveryTileWould` (GL) — 64 rocks, four
poses including one that culls every instance, each live pass preceded by a
live pass of the previous pose (so the out-of-view clearing is exercised):
every slot's live command and every tile's survivor count equal the full
dispatch's. The existing `TestNanitePackingKeepsEveryLiveCommandInOrder`
(fixed list vs packed list) passes over the live path.

## The scratch level

`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/8b1c0d12-038d-4935-9ad4-2bf2380ff5b6/scratchpad/p3/`
(shaders, textures, biomes and fonts symlinked to the repo's):
`resources/generators/canyon.fgen` — phase 1's 1 km canyon at 0.5 m, boulders
(8 × 2,500 tris), rubble (8 × 600), cliff chunks (4 × 120k), ledges
(3 × 12k), `walls` (traced, `max_chunks` 1200, `scale` 1..3), `scatter`
(talus: density 6 at cliff feet within 10 m; boulder_field: 0.6, cluster 1 at
30 m on deposition; rubble: satellites 2.5, cap 2500; wash_pebbles: density 20
on flow ≥ 0.35, slope ≤ 14°), `plants` (desert at 0.15, `rock=0 pebbles=0
grass=0 cactus=0.35`). `levels/canyon.yaml` holds the splice;
`canyon_t/_s/_w/_v/_sw.yaml` are the layer splits measured above.

Generation (`foundry gen run -workers 8`): everything from nothing 3 min 17 s,
2.18 GB peak (the 0.5 m terrain ~75 s, the cliff chunks 1 min 45 s); the
placement stages alone: walls 0.5 s, scatter 0.7–1.5 s for 14k rocks, plants
0.02 s. Level load (`foundry bench`, yaml, GPU path): 2.2–2.3 s for 15.5k
placements. The level YAML is 4.7 MB at 15.6k placements (~300 B each).

## Pictures inspected

All under the scratchpad's `shots/` (each `pose_0.png`, one bench run each —
WSLg multi-shot runs hang):

- `bench_a`, `full3`: the first walls — vertical facades stood in front of
  talus slopes, rim ledges floating in the air (fixed: `wall_slope`, the
  steeper foot/rim, ledges on the face).
- `full6`: the wall view after those fixes: tiers end at the cliff band, ledges
  along the joints.
- `p_t3`: terrain only — the dark floating quads and blotches seen in every
  view are the terrain/VSM's, present without any placement (not this phase's).
- `v_talus`/`v_talus2`: a canyon floor from a bench: talus gathered along the
  wall feet, rocks seated on the slopes; `v_talus` had the desert biome far too
  dense (saguaros everywhere) → plants at 0.15 with cactus 0.35.
- `v_field`/`v_field2`: a boulder field: grounded, no floaters; still reads as
  scattered singles at that density.
- `v_rubble`: close-up: rubble sitting in the sand, sunk, shadows grounded.
- `v_wash2`/`v_wash3`: a wash: pebbles small and sunk, dead bushes and deadwood
  from the biome.
- `fin_over_on`, `fin_over2`, `fin_over3`, `fin_over4`: the canyon from above
  — the walls on one long line (longest-first), then dotted lone columns
  (tallest column first), then contiguous patches on the tall mesa faces
  (stretches), then bigger chunks covering more of them.
- `../overview.png`, `../ov_mid*.png`: the relief with every placement as a
  dot (rule colours) and the vegetation rectangles.

## Tests

| Package | What |
| --- | --- |
| procgen/place | Euler YXZ round trip and FromTo; scatter identical on 1 and 9 workers, seed-sensitive, satellites thrown; every pair of footprints ≥ spacing × radii; no base-ring point above the ground and no rock buried past MaxBury + sink; slope band honoured, power law skews small; cliff-base rocks only at the foot; collision by size; traced feet on the foot side facing out; walls face out, backs in the wall, tiers reach the rim, ledges, footprints, deterministic; any back direction seats its back plane where a −Z chunk's goes; ledges jut from the wall (fails with the old rim rule); patches only where weighted, never overlapping, within MaxSize |
| procgen/stages | the placement doc end to end (walls, two rules, vegetation): placements prefixed, scaled, chunk auto, NaN-free, rule names in IDs; volumes spliced through the strict level schema; scatter clear of the walls' footprints; rerun skips walls and Load serves `avoid`; identical placements and volumes on 1 and 7 workers; previews (a dot layer per rule in distinct colours, instanced proxies ≤ 300 triangles with bounds, wall lines and dots, vegetation rectangles); preview-only params never stale; validation messages; `max_triangles` and the rock sources |
| editor | the scatter stage's window in 2D and 3D, the overlay; 60,000 dots (zoomed out and in) stay within the 16-bit draw list |
| editor (GL) | three instances of a cube draw in the left, middle and right thirds; the existing generator GL tests |
| render (GL) | `TestNaniteLiveTilesWriteWhatEveryTileWould`; packing, colour, LOD, streaming and visibility-buffer suites over the live path |
| internal | layering: `procgen/place` |

Results:

- `go build ./...` and `go vet ./...` clean.
- `go test` passes for procgen/... (core, gencli, genlevel, meshout, noise,
  place, rocks, sdf, stages, terrain), world/..., render/..., gfx/...,
  biome, levelio, pack and internal (layering). editor: every generator test
  passes; `TestScoreSibeliusKeymap` and `TestScoreRebinding` fail — the score
  session's work in progress (editor_score_keymap_test.go), not touched here.
- GL (`FOUNDRY_GL_TEST=1`): gfx passes; editor's generator GL tests pass;
  render: every test passes except `TestNaniteFoliageDrawsTheClassicNearField`
  ("the forest covers only 4588 pixels"), the failure recorded since phase 0
  (406 s run). An earlier run also failed the packing test, before the
  out-of-view clearing existed.

## Known gaps

- **The generated cliff chunks read as stacked blocks** from a distance —
  their shape (box-like, a repeated alcove), not their placement; the scanned
  cliffs are to replace them. The walls still show seams between columns
  where chunks of different sizes meet.
- **Coverage is budgeted.** 20 km of traced feet would want ~15k chunks;
  1,200 cover ~100–150 columns of the tallest stretches. After the live-tile
  fix the cost of hero chunks is what is in view, but every chunk still adds
  ~210 tiles of GPU memory (32 B each) and two command slots (40 B), and
  stretches visible at once still cost their windows.
- **`foundry profile-load` is not usable on these levels.** It loads with
  GPU uploads off, so cooked scenery takes the classic path, which de-indexes
  each placement's full mesh into per-vertex slices: 1,200 copies of a
  120k-triangle chunk exhausted memory (it never finished on 10k placements,
  and an earlier attempt on 38k placements is the likely cause of the WSL OOM
  of 2026-10-11). Load times here come from `foundry bench`. The classic path
  (no GPU cull) would have the same problem in a game; sharing a cooked
  mesh's classic geometry across placements is future work.
- **Remaining frame costs with many instances** (CPU, WSLg): the virtual
  shadow map's CPU side ~14 ms at 15k instances even at the sky view; the page
  pool thrashes (256 MB budget, ~2,700 pages up/evicted per pose); none
  measured on a GTX 1070. GPU timer queries read 0 on WSLg's Mesa d3d12, so
  the F3 breakdown here is CPU-side; GPU attribution was by A/B switches.
- **Pass-2 history** of an instance that goes out of view is cleared once per
  run rather than written every pass; equivalent for every reader, but the
  live-tile GL test checks plain-mode passes only (commands and counts).
- **Wash pebbles** are small (10–28 cm) and read faintly; boulder fields read
  as scattered singles more than clusters at the template's density.
- **Vegetation** is volumes only (no `world.Tree` placements); the plants'
  look is the biome's.
- **CLI splice and an open editor**, `chunk: auto` for prefab chunks: as
  phases 0–1.
- Phase 1/2's scratch projects and pictures were lost when the WSL VM was
  reset mid-phase; their docs' paths no longer exist.
