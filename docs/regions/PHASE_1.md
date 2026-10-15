# Environment regions — phase 1: region volumes and the level generation doc

Status: built and tested headless, 2026-10-12. Not yet exercised by hand in a
running editor (no interactive session was available to this agent): the
inspector, outlines and footprint handles compile, and their logic has unit
tests, but nobody has dragged a handle with a mouse yet.

## What was built

### 1. The `region:` volume block (internal/world)

- `world.VolumeRegion` / `RawVolumeRegion` (world/volume_region.go), on
  `Volume.Region` / `RawVolume.Region`, converted in `VolumeFromRaw/ToRaw`.
  Fields: `environment`, `generator` (optional .fgen override), `priority`
  (default 0), `transition` (m, default 150), `anchor` (optional `[x, z]`),
  `footprint` (`[[x, z], ...]`, ≥ 3 points).
- **Footprint and anchor are relative to the volume's position, through its
  rotation** (never its attachment), so the move gizmo carries a region with
  no special case. `RegionLocalToWorld/WorldToLocal`, `RegionWorldFootprint`,
  `RegionWorldAnchor`.
- `FitRegion` recentres the footprint on the volume's position and fits the
  box (X/Z half extents; Y default 25) — picking, the AABB and
  `pickVolumeRayT` use the fitted box unchanged.
- `CageMesh` draws a region as its footprint prism (`RegionCage`).
- `volumeRoleSummary` says `region <env>`; `Clone` deep-copies.
- YAML field docs regenerated (`editor_yaml_docs_gen.go`), guide section
  docs/guide/13-volumes.md "Environment regions".

```yaml
volumes:
  - id: canyon_west
    position: [-300, 40, 0]
    half: [300, 25, 300]
    region:
      environment: desert_canyon
      generator: environments/my_canyon.fgen   # optional
      priority: 0
      transition: 200
      anchor: [0, 0]                           # optional
      footprint: [[-300, -300], [300, -300], [300, 300], [-300, 300]]
```

### Editor (internal/editor)

- editor_volume_region.go: the **Environment Region** inspector group (checkbox
  creates the block with a footprint = the box's rectangle; environment combo
  of registered + on-disk presets; generator combo of presets and
  generators/*.fgen; priority; transition; anchor toggle + XZ; point count and
  area; **Open World Generator**).
- Every region's footprint is outlined in its environment's colour (depth test
  off, with the selection marks); the selected region shows a handle per
  point and its anchor.
- Footprint editing in the 3D view on the volume's plane: **drag** a handle
  (one `EditorPushUndo` per drag, latched on the first movement),
  **Ctrl+click** near an edge to insert (then keeps dragging), **right-click**
  a handle to delete (≥ 3 kept). Hooks: `Editor3DHandleClick`, the right-press,
  motion and left-release branches of `EditorHandleEvent` /
  `editorEndViewportDrag`.
- `editorReconcileRegion` (per frame, selected region): a box whose X/Z
  changed (scale gizmo, Shape group) scales the footprint; an off-centre
  footprint is refitted.
- `duplicateActiveVolume` deep-copies the region; the explorer row says
  `(region <env>)`; the Shape group notes the fitted box.
- Copy/paste/undo/fingerprint go through `VolumeToRaw` (nothing to add).

### 2. procgen/regions (pure; layering entry added)

- `Region`, `Set` (`NewSet` validates and sorts by key), `Key(id)`,
  `Validate` (≥ 3 points, area, no crossing edges, unique keys, environment).
- `SignedDistance` (concave OK, either winding), `PointInPolygon`,
  `SignedArea`, `Centroid`, `PolygonBounds`, `SelfIntersection`.
- `Weights(x, z)` (sparse, sorted, sums to 1), `Weight(i, x, z)`,
  `WeightFunc(i)`:
  1. raw = smoothstep(0.5 − d/band) of the signed distance at a domain-warped
     point (one warp for the set, so shared edges stay shared);
  2. priority groups high → low, each taking `remaining × (1 − Π(1 − w))`,
     split by raw weight;
  3. **the remainder goes to the nearest region, split smoothly** between
     regions whose distance is within a band of the nearest's
     (smoothstep(1 − Δd/band)). See "Deviations".
- `GridOver`, `Raster(ctx, grid, workers)` (dense per-region planes),
  `Bounds`, `RegionBounds`, `BandBounds`.
- `Adjacency` type (`Rule{Transition, Forbidden, Intermediate, Note}`,
  `Lookup` with `*` = any) and `DefaultAdjacency()` = ENVIRONMENT.md's table.
  Data only; nothing consults it yet (phase 5).
- names.go: the regions stage's kind/param names, shared by stages and
  genlevel.

### 3. Environment presets (internal/procgen/environments.go, stages/environments_builtin.go)

- `procgen.RegisterEnvironment(Environment{Name, Label, Doc, Color, New})`,
  `EnvironmentByName`, `Environments`, `EnvironmentNames` (registered ∪
  resources/environments/*.fgen), `LoadEnvironment` (file first, else
  built-in), `LoadGenerator` (preset name or resources path),
  `WriteEnvironment` / `WriteEnvironments` (**write only when absent**),
  `EnvironmentColor` (unregistered names get a stable hashed colour).
- `desert_canyon` = the template minus its `environment` stage and target
  level; `savannah` = a stub with one terrain stage (relief 14 m, no mesas,
  soft thick strata, terrace 0, no canyons). Colours: desert (0.86, 0.46,
  0.24), savannah (0.78, 0.76, 0.30).
- Presets are NOT shipped as files in the repo's resources/ (they are written
  into a project on demand by `gen world`, `gen env write` or the editor's
  Open World Generator).

### 4. The `regions` stage, sync, template, CLI, editor entry

- stages/regions.go + regions_run.go: kind `regions` (Category Level,
  `Regional: Shared`, `Expand` hook). Params: `regions` (ReadOnly list: id,
  environment, generator, priority, transition, anchored, anchor, footprint —
  a Closed spline, MinItems 3), `warp_amount` 40, `warp_scale` 300,
  `resolution`/`tile_size`/`margin`/`erosion` (global|tiled) — declared,
  documented as **phase 2, unused** — and `preview_size` (PreviewOnly).
- Run writes `regions.yaml` (warp, bounds, settings, each region with key,
  resolved anchor, bounds, footprint) and sets `*regions.Set` as Artifact;
  Load parses it back (`ParseRegionsFile`).
- Preview: `biomes` overlay (env colours mixed by weight, readout = top
  share), one grey `weight_<key>` layer per region, a closed outline
  LineLayer per region in its colour, notes.
- `genlevel.SyncRegions(doc, raw)` / `SyncRegionsFromFile` /
  `RegionItems` / `RegionsStage`: world-XZ footprints (through position and
  rotation, rounded to mm), sorted by id; no-op for docs without a regions
  stage.
- `level_world` template (`LevelWorld(name)`: level = name minus `_world`,
  one `regions` stage).
- gencli: `foundry gen world <level>` (`EnsureWorldDoc`; also writes absent
  presets), `foundry gen env list`, `foundry gen env write <name>|-all`;
  `run` syncs (and writes the doc back when changed), `check` syncs in
  memory and says so.
- Editor: `genBeginRun` syncs first (open level, else file); `genStepJobs`
  syncs from the open level every 500 ms (so moving a point re-previews the
  overlay); File > **Generate World for <level>…**; the param panel shows a
  ReadOnly list dimmed with a note and no reset; the 2D view skips ReadOnly
  splines and draws Closed splines closed.
- New ParamDef flags `Closed` and `ReadOnly` (editor-only; not hashed).

### 5. procgen core expansion (regional.go)

- `StageKind.Regional` (`RegionalNone` default / `RegionalPerRegion` /
  `RegionalShared`), `StageKind.SharedFor`, `StageKind.Expand`,
  `Env.Region *RegionContext{Key, ID, Environment, Preset, Weight, Bounds,
  Band, Anchor}`, `RegionExpansion`, `RegionStageID`, `RegionStagePrefix`,
  `RegionStageSeed`, `RewriteStageRefs`.
- `plan` → `planWithNotes`: after the listed stages, each Expand stage's
  regions expand their presets' PerRegion stages as `r_<key>_<stage>`: refs to
  a Shared-kind preset stage → the level doc's stage whose kind lists it in
  `SharedFor` (error if none); refs to an expanded sibling → `r_<key>_<ref>`;
  refs to an unexpanded stage → skip with a note; None kinds → skip with a
  note; disabled → silently skipped. Seed = Hash(doc seed, region key, preset
  stage ID, seed params). Hash = StageHash over rewritten params + refs'
  hashes + the defining (regions) stage's hash. Kind Validate runs on each.
- Run logs the notes, accepts `-stage r_…`, and no longer prunes expanded
  stages' outputs (prune keeps planned IDs).
- `terrain`, `terrain_material`, `environment` are marked Shared. Every other
  real kind is still None, so **today nothing expands** — every preset stage
  is logged as skipped. That is expected for phase 1.

## Tests (all pass)

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...
go vet ./internal/procgen/... ./internal/world/ ./internal/editor/ ./internal/levelio/
go test ./internal/procgen/... ./internal/world/... ./internal/levelio/...
go test ./internal/editor/...            # see "Known failures"
go test ./internal/ -run TestPackagesDoNotDependUpward
```

- procgen/regions/regions_test.go: signed distance (square, concave L, winding
  independence), validation refusals, Key, weights sum to 1 (with and without
  warp, over a grid including gaps), inside = exactly 1, band symmetry and
  monotonicity for equal priority, priority wins both ways, gap → nearest, no
  step past outer corners, determinism / input-order independence /
  seed-sensitivity of the warp, raster = Weights, honest band boxes,
  adjacency defaults.
- procgen/regional_test.go (fake kinds tregions/tterrain/tunion/tplace/tdrift):
  expansion IDs, ref rewriting (Shared stand-in and sibling), Env.Region
  weights, distinct seeds per region, skip notes, prefixed placements, rerun
  skips and prunes nothing, editing one region's preset reruns only its
  stages, `Only` on an expanded stage, dropping a region prunes its outputs,
  missing stand-in errors, no-regions docs plan unchanged.
- procgen/environments_test.go: write-if-absent, user edit survives, loader
  prefers the file, project presets listed, LoadGenerator by path, colours.
- stages/regions_test.go: **GATE** `TestGateTwoRegionsPreviewAsABiomeOverlay`
  (two 600 m regions meeting at x = 0, 200 m band: exact desert / savannah
  colours deep inside each, ≥ 15 of 21 seam samples strictly between the two
  colours, monotone red across the band, two closed outlines, three image
  layers); run + Load round trip; presets valid; **hash pin**
  `TestDesertCanyonHashesUnchangedByRegions` (the template's 13 stage hashes
  recorded before any phase-1 change, still identical); level_world template.
  `FOUNDRY_REGIONS_DUMP=<png>` writes the overlay; it was looked at (a clean
  orange/yellow-green split with a meandering soft band).
- genlevel/regions_test.go: sorted ids, world footprints through rotation,
  mm rounding, anchors, idempotence, no-regions docs untouched.
- gencli/gencli_regions_test.go: `gen world` (doc + presets, once), `gen env
  write -all` leaves an edited preset, `env list`, `run` syncs a moved region
  into the doc on disk.
- world/volume_region_test.go: YAML round trip (strict), defaults, no key on
  plain volumes, deep copies, FitRegion keeps the world footprint (rotated
  too), cage, helpers.
- editor/editor_volume_region_test.go: duplicate owns its footprint, box
  scale → footprint scale, reconcile idempotent, Generator sync from the open
  level (and following a move), EnsureWorldDoc once.

### Known failures (not this work)

- internal/editor `TestScoreSibeliusKeymap`, `TestScoreRebinding` — the score
  session's in-progress keymap (editor_score_keymap_test.go). Untouched.
- internal/harness TestAPlaySessionDrawsTheRoomItIsStandingIn — predates this
  work; harness not run.

No GL tests were run: nothing in render/ or gfx/ was touched.

## Deviations from the spec

- **Nearest-region fallback is smooth.** The spec says "a sample no region
  reaches goes to the nearest region". A hard nearest switch made a visible
  step in the overlay past two neighbours' outer corners, so the share no
  priority group takes is split between the nearest region and any whose
  distance is within one band of it. Deep in a gap the nearest still gets
  exactly 1; only the hand-off line is a band instead of a step.
- **Anchor is relative to the volume**, like the footprint, so moving a
  region moves its anchor (the spec does not say which).
- **Expanded stages hash in the defining (regions) stage's hash**, so editing
  ANY region's geometry reruns every expanded stage (weights depend on the
  whole set). Editing one region's preset reruns only that region's stages,
  as specified. Per-region/per-tile incrementality is phase 5.
- `RegionContext.Band` is "footprint + half band + warp reach" (where the
  region's own blend reaches); Weight can be > 0 outside it only as the
  nearest region to an unclaimed gap. Placement stages should clip to Band.
- Preview does NOT expand regions (lenientPlan is unchanged); only runs do.
- Presets are written on demand, not shipped in the repo's resources/.

## Gaps

- No hands-on editor session: handle picking/dragging, the outline colours
  and the inspector are untested by eye.
- Selecting a hand-written region whose footprint is not centred refits it
  silently (position changes, no undo step) the first frame it is selected.
- Footprint editing ignores Snap to grid.
- The level editor has no warning when a footprint self-intersects; the
  world generator's validation reports it at run/preview.
- `list_assets`' enum was not extended (the `all` listing includes the new
  `environments` kind); the enum line is shared with the score session.
- savannah is a terrain-only stub; phase 6 fleshes it out.
- resolution/tile_size/margin/erosion are inert until phase 2.
