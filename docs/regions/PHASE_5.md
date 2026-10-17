# Environment regions — phase 5: editor tools and adjacency rules

Status: built and tested headless, 2026-10-12. The GATE has a test at three
levels (procgen/regions, genlevel, the real CLI) and the editor paths have
headless editor tests. **Nothing in this phase was clicked in a running
editor**: no interactive session was available to this agent. The region
list, the 2D-view handles, the inspector warnings and the Insert buttons
compile, their logic is tested through the same functions the buttons call,
but nobody has looked at them on screen or dragged a handle with a mouse.

`$S` below is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.

## What was built

### 1. Adjacency rules (procgen/regions, procgen/stages)

- **Defaults** (regions/adjacency.go): ENVIRONMENT.md's table was already
  transcribed by phase 1. Completed with dunes ↔ alpine / tundra forbidden
  (ENVIRONMENT.md's "Desert" row covers both deserts), so 13 pairs. Added
  `Clone`, `Delete`, `Rule` (exact pair, for listings) and `Rule.Describe`.
- **Project override** (regions/adjacency_file.go):
  `resources/environments/adjacency.yaml`, strict YAML. Each entry REPLACES
  its pair's rule (not a field merge); `remove: true` drops a rule; order of
  the pair does not matter; `"*"` is any environment. Parse errors name the
  entry. `ParseAdjacencyFile`, `AdjacencyFile.Apply`, `Adjacency.File/Encode`.

  ```yaml
  pairs:
    - between: [desert_canyon, savannah]
      transition: 250
    - between: [desert_canyon, pine_forest]
      forbidden: true
      intermediate: savannah
      note: "no conifers off the canyon rim"
    - between: [coast, "*"]
      remove: true
  ```
- `stages.ProjectAdjacency()` (stages/adjacency.go): built-in + the file,
  cached by the file's size and mtime. A broken file is a regions-stage
  Validate error (refuses the run).
- `foundry gen env adjacency` prints the effective table (pair, rule, note)
  and the same table as YAML in the override file's form.
- **Pair transitions** (regions/pairband.go). `Region.PairTransitions`
  (environment → width) is filled by `ResolvePairTransitions` for a region
  whose transition is the default (≤ 0 or exactly 150), from the table's
  non-forbidden widths. A region's band at a point is a smooth mix of its pair
  widths over its NEIGHBOURS (regions whose band boxes overlap), weighted by
  `smoothstep(1 − (d_j − d_min)/Band)` — continuous, symmetric for two
  default regions, and **the old arithmetic exactly** when no region has a
  pair width different from its own band (`nb[i] == nil`, tested bit for
  bit). `BandBounds` uses the widest band (`Set.MaxBand`).
  - Deterministic path into the weights: the regions stage resolves the
    widths before building its Set (`regionSet` → `resolvedRegions`); they are
    written to `regions.yaml` as `pair_transitions: {savannah: 200}` and read
    back by `ParseRegionsFile`, so a load needs no table.
  - Hash: the regions stage's new `HashExtra` (`regionsPairHash`) is every
    region's resolved widths, canonical. **Planner change:** an EMPTY
    HashExtra is now folded in as nothing (run.go and preview.go), so a
    world whose regions take no pair width keeps its hash (checked on the
    phase-4 scratch world: `gen check` says every stage up to date). The
    other HashExtras (region_terrain, region_material) only return "" for a
    doc with no regions.
- **Adjacency check** (regions/adjacent.go, pure). `FindAdjacent(ctx, set,
  adj, opts)`: `Set.Weights` sampled on a raster of at most 8 m / 256 samples
  a side over `Set.Bounds()`, parallel rows; a pair is adjacent where both
  shares > 0.01; per pair the sample where the smaller share is largest
  (`Where`, ties broken by position so the answer is goroutine-independent).
  `Adjacent{A, B, IDA, IDB, EnvA, EnvB, Rule, HasRule, Where, Overlap}`,
  `Forbidden()`, `Suggestion()`, `Warning()`, `ForbiddenPairs`, `Warnings`.
- **Warnings, not errors.** New `procgen.StageKind.Warn` hook and
  `procgen.Warnings(d)` ("[stage] …", a panicking hook reported as a
  warning). `Run` logs them first (`warning: …`) and returns them in
  `Result.Warnings`; `gen run` repeats them after its summary (so `-q` shows
  them); `gen check` prints them. The regions stage's `Warn` is the forbidden
  pairs. The run proceeds.

### 2. Insert intermediate region

- **Pure op** (regions/intermediate.go): `PlanIntermediate(set, adj, a, b,
  opts) → Intermediate{Environment, Priority, Footprints, Between}`. The
  argument (file header has it in full): with C above A and B in priority,
  wherever C's raw weight is 1 the pair gets nothing, and everywhere else
  their shares only shrink; so C must own outright the region where they
  overlapped. Construction, in the WARPED domain with the warp off (so it is
  right for any warp): mask where A and B both > 0.001 (every region at its
  widest band, counting the pair width it will take against C), over the
  set's bounds plus the warp reach → dilate by max(C's widest band, A's,
  B's)/2 + simplification tolerance + 2 cells → 4-connected components (two
  separate contacts make two regions) → holes filled (so no corner pinch) →
  traced along cell edges → Douglas–Peucker at 1.5 cells, halved until it
  does not self-intersect. Raster ≤ 4 m, ≤ 384 samples a side.
  Priority = max(A, B) + 1; transition left at the default, so each pair
  takes the table's width.
- **On a level** (genlevel/adjacency.go): `LevelRegions(raw, adj)`,
  `LevelAdjacency(ctx, raw, adj, warp)`, `InsertIntermediate(raw, adj, a, b)
  → ids` appends one region volume per footprint: ID `<a>_<b>_<env>` (unique
  suffix), position = footprint bounds centre at the mean of the two
  volumes' Y, footprint relative to it, `FitRegion`, `HalfY` the region
  default.
- **Editor, level side** (editor/editor_region_adjacency.go): the region
  inspector lists the region's forbidden neighbours with **Insert <env>
  region** (`editorInsertIntermediate`: one `EditorPushUndo`, volumes
  appended with level-unique IDs, loaded, the first selected). The check runs
  OFF the UI thread, debounced 300 ms after the regions' signature (the
  synced items' text + adjacency.yaml's size/mtime) stops changing, with the
  level's world generator's warp (`stages.RegionWarp`) so it agrees with a
  run; the inspector shows "checking…" while a newer check is pending. The
  checker is a small reusable type (`regionAdjChecker`).

### 3. Generator editor (editor/editor_generator_region_list.go)

- A **Regions** section in the right column, above the stage parameters,
  for any doc with a regions stage: colour swatch, id, environment,
  priority, transition, `!` on a region with a forbidden neighbour. Click
  selects the region (and the regions stage, so its overlay shows); **Select
  in level** selects and frames the volume in the level editor (on the main
  window); **Weight overlay** sets the 2D view's overlay to
  `weight_<key>` (the selected region) or `biomes`.
- **Adjacency warnings** listed with **Insert <env> region** (queued onto
  the main window: `editorInsertIntermediate`, then a sync).
- **2D footprint editing** of the selected region (level open): handles
  drawn from the LIVE level volume; drag a point, Ctrl+click an edge to
  insert, right-click to delete. Every edit is queued onto the main window
  (`uiwin.RequestWindowOpen` — an undo snapshot and the cage reload touch GL)
  and goes through `EditorPushUndo` (one per drag, latched on the first
  move; insert and delete one each), refused while the level compiles, loads
  or plays (`genSpliceBlocked`). A drag's end refits the box and resyncs the
  doc. While a region is selected it owns the view's clicks (no spline is
  active); pan/zoom still work.
- **Level not open**: read-only list (the synced copy), the doc's own check
  (`stages.RegionAdjacency`, same off-thread checker), Insert buttons
  disabled, and **Open level**.

### 4. Per-tile incremental runs

- **Engine** (procgen/incremental.go, run.go, manifest.go):
  `StageKind.Incremental`. When such a stage reruns (not `-force`), its files
  are KEPT and `RunContext.Previous` is its last manifest entry. While it
  runs the entry on disk is `{kind, files}` — no hash, no tiles — so a killed
  run vouches for nothing. `ReuseTile(name, hash, files…)` (records the files
  untouched and carries the hash), `BeginTile`, `SetTile`, `PreviousTile`.
  On success: `{hash, files, tiles}` and the previous files neither written
  nor reused are deleted. On failure/cancel: `{files: old ∪ new, tiles:
  untouched previous + finished}`, still no hash. Manifest format: `tiles:
  {tile_0_0: <sha256>, …}` (omitempty; version unchanged).
- **Erosion window cache** (terrain/tiled_cache.go): `Union.Cache
  WindowCache`, `Provider.Key`. Each tiled-erosion window's key is a SHA-256
  of exactly its inputs (square, knobs, seed, per-sample owner/offset/band
  entries, heights, deposits, present providers' keys); the value is the
  eroded heights + deposits over the faded span and the droplet count. Hit →
  no erosion; a cached run is the uncached run to the bit (tested).
  region_terrain stores windows as `erosion/w_<i>_<j>.bin` (magic + key
  header: self-verifying, atomic writes).
- **region_terrain** (stages/region_terrain_incremental.go): `Incremental:
  true`; provider keys = preset terrain `StageHash` + seed + anchor +
  measure + extend + cell + size; tiles hashed by `tileInputHash` (tile
  samples + the normal ring, base, quant, step/per/size, grid origin and
  cell, cook/mesh-only, texture/uv; kind Version + a local version) and
  reused without meshing or cooking. With tiled erosion the tile base snaps
  down to a multiple of 64 m (`stableTileBase`) so a far edit does not move
  every tile.
- **Deviation from the spec's wording: content hashes, not region-data
  hashes.** The spec says a tile's hash folds in "only the region data whose
  band + warp + erosion margin touch the tile's window". The weights' gap
  fill gives a region a share far past its band (PHASE_2 "Gap fill reaches
  far" — and, found here, ALONG THE UNION'S OUTER EDGE, where every region
  sits on its own edge with raw weight ½ and the fill hands the rest to any
  region within a band of distance), so a geometric rule is either wrong or
  reuses nothing. Hashing what the generator actually read is exact by
  construction. The cost: providers, weighing and blending always run over
  the whole union (weights + shape ≈ 2.4 s of PHASE_2's 1 km² 0.5 m tiled
  run of 10.9 s).
- **Global erosion stays whole-stage**: every sample depends on every region
  (drainage routes the whole union; droplet RNG blocks and the lead's knobs
  are global), so a region edit changes every tile's heights and every tile
  recooks. Tile hashes still help there for edits that do not change heights
  (cook_memory, a rerun after a cancelled cook).

### 5. Docs

- docs/guide/35-regions.md (new): the whole workflow — region volumes,
  presets, the world generator, the region list, adjacency and Insert,
  incremental runs, CLI, files. README row added (additive).
- docs/guide/33-generators.md: pointer to 35, the regions stage's adjacency
  paragraph, region_terrain's "Incremental" paragraph.
- docs/guide/13-volumes.md: the inspector's forbidden-neighbour list.

## Files

New: `procgen/regions/{adjacency_file,pairband,adjacent,intermediate,
adjacency_test}.go`, `procgen/incremental.go`, `procgen/incremental_test.go`,
`procgen/terrain/tiled_cache.go`, `tiled_cache_test.go`,
`procgen/stages/{adjacency,region_terrain_incremental,
region_terrain_incremental_test}.go`, `procgen/genlevel/adjacency.go`,
`adjacency_test.go`, `procgen/gencli/gencli_adjacency_test.go`,
`editor/editor_region_adjacency.go`, `editor_region_adjacency_test.go`,
`editor/editor_generator_region_list.go`, `docs/guide/35-regions.md`.

Changed: regions/{adjacency,regions,weights,regions_test}.go; procgen/{run,
manifest,context,registry,preview}.go; terrain/{tiled,union,terrain}.go;
stages/{regions,regions_run,region_terrain,region_terrain_run,
terrain_run}.go; gencli/{gencli,gencli_regions}.go;
editor/{editor_volume_region,editor_generator_panel,
editor_generator_view2d}.go; docs/guide/{README,13-volumes,
33-generators}.md. No shared file other than the README was touched.

## Tests

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                          clean
go vet ./internal/procgen/... ./internal/editor/        clean
go test ./internal/procgen/... ./internal/world/... ./internal/levelio/...   all ok (stages 73 s)
go test ./internal/editor/...     fails only TestScoreSibeliusKeymap, TestScoreRebinding (the score session's)
go test -count=1 ./internal/ -run TestPackagesDoNotDependUpward            ok
```

No render/gfx/shader code was touched; no GL tests were run.

- **GATE**, three ways:
  - `gencli/TestGateForbiddenPairWarnsAndOneInsertResolvesIt` — the real
    CLI: adjacency.yaml forbids desert_canyon|savannah with intermediate
    `steppe` (a stub preset copied from savannah); `gen env adjacency` shows
    the override; `gen check` warns ("canyon (desert_canyon) and grass
    (savannah) … Insert intermediate region adds steppe"); ONE
    `genlevel.InsertIntermediate` on the level file; the next `gen check`
    (which re-syncs) has no warning.
  - `genlevel/TestInsertIntermediateResolvesAForbiddenPairOnALevel` —
    desert_canyon beside a rotated, higher-priority tundra; one insert adds
    `cold_dry_savannah` (priority 3, default transition, mean Y); no
    forbidden pair after; the savannah touches both; a second insert
    refuses.
  - `editor/TestLevelEditorWarnsAndInsertsAnIntermediateRegion` — the
    off-thread check finds the pair, `editorInsertIntermediate` is one undo
    step and selects the new region, the re-check is clean.
- regions: override file (replace, remove, new pair, round trip, five
  refusals); pair width 200 used (savannah 0 at −101, > 0 at −90, ½ on the
  seam, symmetric); explicit transitions take none; **no pair widths = the
  old arithmetic, bit for bit**; band continuous round a corner between a
  200 m and a 150 m neighbour; FindAdjacent finds exactly the forbidden pair
  near x = 0; Insert on a straight contact and on a concave U-shaped
  contact, re-checked clean; an allowed pair refuses.
- procgen `TestIncrementalStageReusesFreshTiles` (fake kind): only the
  changed tile written, a reused file's mtime untouched, a dropped tile's
  file pruned; a failure leaves no hash and vouches only for untouched and
  finished tiles (not the half-written one); the next run reuses them;
  Force rebuilds; a fresh stage does not run.
- terrain `TestWindowCacheIsExactAndLocal`: a storing run = an uncached run
  (ResultHash); a rerun takes every window from the cache, same bits and
  droplets; a far edit reuses some windows and equals a full run of the
  edited union; no provider keys → no caching.
- stages `TestRegionTerrainIncrementalLeavesNearTilesAlone` (three regions
  tiling a 768 m union at 2 m, tiled, window 1, cooked): after a far preset
  edit tile column 0's .fnm files keep their bytes and mtime and their
  hashes; heightmap.png and terrain.yaml equal a fresh run's; every tile
  hash equals the fresh run's.
- editor `TestGeneratorFootprintEditsWriteBackToTheLevel`: a two-move drag is
  one undo step and lands in the level and the synced doc; insert and delete
  are one step each; Undo restores.

## Measured numbers (WSL2, 20 threads, `-workers 8`)

| | |
| --- | --- |
| FindAdjacent, 3 regions, 256² raster | 5.3 ms |
| PlanIntermediate, desert/tundra, 3 regions | 10 ms (26-point footprint) |
| stages incremental test (768 m at 2 m, 36 tiles cooked) | first run 5.1 s → incremental 3.4 s; 9 tiles kept, 27 recooked |

Scratch world `$S/proj5` (a copy of phase 4's proj3; level edited to three
regions tiling the 768 m union — canyon_west, savannah_mid, savannah_east on
its own copy of the savannah preset — `erosion: tiled`, `window: 1`, 1 m,
cooked, far backdrop), `foundry gen run seam_world -stage ground
-no-splice -workers 8`:

| Run | Wall | Erosion | Cook | Peak RSS |
| --- | --- | --- | --- | --- |
| full (`-force`) | 24.5 s | 11.1 s, 36 windows eroded | 7.9 s, 36 tiles | 365 MB |
| after editing savannah_east's preset relief | 19.8 s | 8.3 s, 26 eroded / 10 reused | 7.6 s, 3 tiles kept | 362 MB |
| rerun with nothing changed (forced by `-stage`) | 1.7 s | 0.3 s, 36 reused | 2 ms, 36 kept | 173 MB |

The far edit's locality is poor in this world: savannah_east's share
reaches about 200 m along the union's top and bottom edges through the
nearest-region fill (the outer edge of every footprint is a ½ raw weight,
and the remainder goes to any region within one band of distance), so the
windows of tile column 1 change and fade into column 0's corner tiles. In
the original two-region proj3 layout (768 × 512 regions in a 768² union,
padded rows filled from the nearest regions) nothing at all was reused after
a savannah edit (36 eroded, 0 tiles kept) — the gap rows give the savannah a
share almost everywhere. Both are the weights model's fill (phase 1), not
the cache: the debug dump of window keys confirmed the inputs genuinely
changed there, and stayed identical where reuse happened.

## Deviations

- **Content hashes instead of region-data hashes** for tiles and windows
  (see 4). The window cache is keyed by the eroder's inputs; providers and
  blending always run.
- **Pair width is a smooth mix over neighbours**, not one number per pair
  edge: a region's band varies along its edge with whichever neighbour is
  near. The nearest-region fill still uses each region's own Band().
- **Empty HashExtra now hashes as none** (planner change in run.go and
  preview.go), so adding the regions stage's HashExtra moved no existing
  hash.
- `StageKind.Warn` is a new hook rather than a mode of Validate (Validate's
  problems refuse runs).
- Under `-force` an Incremental stage reuses nothing (files deleted, as
  before).
- dunes ↔ alpine/tundra added to the defaults (13 pairs, was 11).

## Gaps

- **Not used in a running editor.** Region list, swatches, handles, the
  Insert buttons and the inspector warnings were never seen on screen;
  Ctrl+click/drag/right-click in the 2D view are untested by hand (their
  write-back functions are tested).
- **Locality is limited by the fill**: along the union's outer edge and in
  padded gaps a region reaches a band's distance or more; incremental runs
  reuse correspondingly less (numbers above). Fixing it is a weights-model
  change (e.g. limit the fill to unclaimed samples INSIDE the union or cap
  the fill distance) and would move every existing world's weights.
- **The inserted region also claims the fill wedge** beyond the contact's
  ends (where the two would otherwise split an unclaimed gap), so it is wider
  at its ends than across the seam (a desert/tundra pair 800 m long gave a
  savannah ~930 × 1380 m). Correct by the argument above; an author may
  want to trim it. Its footprint is a simplified staircase (4 m raster).
- The adjacency check's raster (≤ 8 m) can miss a contact thinner than a
  sample.
- Providers + blend always run (≈ 20% of a run without cook); global
  erosion recooks everything after any region edit (by design).
- The window cache files cost disk: about 8 bytes per sample of each
  window's faded span (≈ 9 MB for the 768 m world at 1 m).
- Per-tile reuse covers the main tiles; the far ring and stitch strips rely
  on the cook's own content-hash skip (meshes built every run).
- No GL tests (nothing in render/gfx changed).
