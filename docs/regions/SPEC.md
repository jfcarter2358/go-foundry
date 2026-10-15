# Foundry environment regions: implementation spec

Goal: seamless multi-biome worlds. A designer places region volumes (`region:` block) in a level,
each naming an environment ("desert canyon", "savannah"), and ONE level-wide generation
pass produces all of them with a natural transition band between them. Fields blend
across regions; erosion runs once over the union; materials and placement are
weighted by each biome's weight. WFC-style constraints apply only to which biomes may
touch (the adjacency table).

The design doc is ENVIRONMENT.md at the repo root (and the Claude Doc
https://claude.ai/code/artifact/d946f0fe-c900-479a-9741-50f1df8a9cb8). This spec turns it
into phases with concrete engine decisions. It builds on the desert canyon toolset:
read docs/canyon/SPEC.md (ground rules, engine facts) and skim docs/canyon/PHASE_0..8.md
for the parts you touch.

Repo: /home/jfcarter2358/personal/code/main/foundry (Go, OpenGL 4.3 core, SDL2, ImGui
editor). Branch: feature/jc/brush-editing.

## Ground rules for every phase

Everything in docs/canyon/SPEC.md "Ground rules for every phase" applies, with these
changes and reminders:

- **Phases run IN ORDER** in the same working tree, one phase per agent. Before starting,
  read this spec, ENVIRONMENT.md and every earlier docs/regions/PHASE_<n>.md.
- **Nobody commits, pushes, stashes, resets or switches branches.** Never
  `git add -A` / `git add .` / `git checkout -- <path>` / `git stash`.
- **Another session may be working in this tree** (the music score editor). Never edit
  internal/tracker/**, internal/score/**, internal/editor/editor_score*.go,
  internal/editor/editor_tracker*.go, docs/score/**. Shared files (editor_menubar.go,
  editor_save_state.go, editor_claude_reference.go, pack/refs.go, app/devtools_dev.go,
  docs/guide/README.md): small ADDITIVE edits only. If a build or test fails in their
  packages, note it and move on.
- **Must pass at the end of each phase:** `go build ./...`, `go vet ./...` on the
  packages you touched, `go test` for every package you touched plus
  ./internal/procgen/... ./internal/world/... ./internal/editor/... (and
  ./internal/render/... ./internal/gfx/... when you touched them), the GL tests
  (`FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...`) when you
  touched rendering — say honestly if no GL context was available. Known failure that
  predates this work: internal/harness TestAPlaySessionDrawsTheRoomItIsStandingIn.
- **Layering** (internal/layering_test.go): procgen core stays pure CPU (no world, gfx,
  render, editor). New pure packages under procgen get an `upward` entry like
  procgen/terrain's. procgen/stages may import world/levelio.
- **New behaviour defaults OFF for existing content.** The classic single-square
  `terrain` stage and the desert_canyon template must produce byte-identical output to
  today's (a golden test pins it). Existing levels must load and render identically.
- **RESOURCE LIMITS.** The box currently has ~23 GB RAM (`free -g`), shared with other
  sessions. ONE heavy job at a time. `GOFLAGS=-p=6 GOMEMLIMIT=6GiB` (export if missing).
  Targeted `go test ./internal/<pkg>/...`, never `./...`. Generator runs: `-workers 8`,
  `cook_memory` ≤ 2000. Check `free -g` first; wait if under 8 GB available. Every long
  command gets a `timeout`. memguard kills the biggest go process on OOM: if a run dies
  with SIGKILL, reduce parallelism and retry.
- **Generated output never goes into the repo's resources/.** Use a scratch project in
  the session scratchpad (symlink `resources/shaders` to the repo's, as PHASE_8 did).
- **House style:** long explanatory file-header comments, the comment density of the
  neighbouring files, small focused files, tests beside code. Match how
  procgen/stages/terrain*.go and procgen/terrain/*.go read.
- **Assets:** prefer Poly Haven CC0 downloads (visible credit, User-Agent, cached),
  as the canyon did.
- Write docs/regions/PHASE_<n>.md at the end: what was built, formats, files, tests,
  measured numbers, known gaps. Be honest about anything stubbed or untested.
- Update docs/guide/33-generators.md (or a new guide page, see phase 5) for every
  user-visible feature.

## Architecture (decided)

### Overview

```
level (YAML)                          resources/generators/<level>_world.fgen
  volumes:                              (the LEVEL GENERATION DOC; a normal .fgen)
    - region: {...}  ──sync──▶          stages:
      environment: savannah               - regions        (synced region list, grid settings)
      footprint: [[x,z]...]               - region_terrain (providers → blend → shared erosion → tiles)
      priority, transition, generator     - region_material(biome-weighted 8-layer splat)  
                                          - environment    (shared: sky, fog, backdrop settings)
                                        + EXPANDED per region at plan time:
resources/environments/<env>.fgen         r_<region>_<stage> for each placement/texture stage
  (environment PRESETS: a .fgen whose     of the region's preset (polyhaven_*, scatter,
   terrain stage is the field provider)   vegetation, cliff_wall, wall_sheet, rocks ...)
```

### Region volumes (world)

- Volumes have no `kind:` discriminator: a volume is a shape plus optional role blocks
  (`fog:`, `scatter:`, `objective:`, `context:` …). ENVIRONMENT.md's "`kind: region`" is
  therefore a new `region:` block — `RawVolume.Region *RawVolumeRegion`, with
  `world.VolumeRegion`, `VolumeRegionFromRaw/ToRaw`, mirroring volume_context.go. Fields:
  - `footprint`: a closed polygon in world XZ (`[[x, z], ...]`), at least 3 points,
    concave allowed, not self-intersecting. Polygons from day one (decided). The
    volume's own Position moves the whole footprint (the footprint is stored relative
    to the volume's position, so the translate gizmo works unchanged); the volume's box
    shape is kept fitted to the footprint's bounds (for picking, AABB and the cage).
  - `environment`: the environment preset name (`desert_canyon`, `savannah`, ...).
  - `generator`: optional .fgen override — a preset to use instead of
    resources/environments/<environment>.fgen.
  - `priority`: int, default 0. Higher wins where regions overlap.
  - `transition`: metres, default 150. The blend band's width.
  - `anchor`: optional XZ; the origin the preset's own coordinates (canyon paths) are
    measured from. Default: the footprint's centroid.
- **The level is the source of truth** for regions. The level doc's region list is a
  synced copy (read-only in the Generator's param panel, with a note saying so). Footprint
  editing: in the level editor's 3D view, a selected region volume shows point handles
  on its footprint at the volume's Y (drag to move on that plane, Ctrl+click an edge to
  insert, right-click a point to delete; one undo step per drag). Phase 5 adds editing in
  the Generator's 2D view that writes back to the open level's volume through the
  editor's mutation path.
- Region volumes are authoring data only: nothing renders or collides with them at play
  time. The editor draws them (outline + fill in the environment's colour).

### Environment presets

- An environment preset is a `.fgen` document under `resources/environments/<name>.fgen`.
  Its `terrain` stage is the region's FIELD PROVIDER (its size is ignored — the region
  supplies bounds); its `terrain_material` and `polyhaven_textures` stages are the
  region's ground material; its placement stages are expanded per region.
- Built-in presets are registered in Go (`procgen.RegisterEnvironment(Environment{Name,
  Label, Color, New func() *Doc})`, beside templates) and WRITTEN to the project's
  resources/environments/ only when no file of that name exists, so user edits are never
  overwritten (decided). `foundry gen env list|write <name>`.
- Each environment has an overlay colour (for the 2D biome overlay and region fills).

### The level generation doc and region sync

- The level generation doc is an ordinary .fgen: `resources/generators/<level>_world.fgen`
  with `level: <level>`, created on demand (editor button "Generate world", or
  `foundry gen world <level>`), from a `level_world` template.
- Its first stage is a `regions` stage. Its params hold the synced region list (a `list`
  param: id, environment, generator, priority, transition, anchor, footprint as a
  `spline`) plus the global settings: resolution, tile_size, margin, erosion mode, warp.
- **Sync, not live reads.** Stage hashes depend only on params, so before every run
  (editor and CLI) `genlevel.SyncRegions(doc, level)` copies the level's region volumes
  into the regions stage's list (sorted by id). Hashing stays pure and deterministic and
  the doc on disk records exactly what was generated.
- **Expansion.** At plan time the engine expands the doc: for each region, every stage
  of its preset whose kind is `Regional: PerRegion` is added after the shared stages as
  `r_<region>_<stage>` (a valid stage ID), with:
  - stage refs to the preset's `terrain` stage rewritten to the level doc's
    `region_terrain` stage; refs to its `terrain_material` rewritten to
    `region_material`; other refs to `r_<region>_<ref>`;
  - `Env.Region` set (key, environment, weight lookup, bounds), so the stage can weight
    and clip its work;
  - its seed derived from (doc seed, region id, preset stage id) so two regions with the
    same preset differ, and editing one never reseeds another.
  - Kinds handled by shared stages (`terrain`, `terrain_material`, `environment`) are
    not expanded. Kinds that cannot work per region yet are skipped with a log line
    (`drift` in v1: it recooks terrain tiles).
  `StageKind.Regional` (new field) says which: None (default; skipped with a log),
  PerRegion, Shared.
- Expanded stages hash like any stage, so editing region A's preset reruns only A's
  expanded stages; their outputs live in the level doc's output folder.

### Field providers and the union grid

- The terrain package splits into a PROVIDER (per region, pre-erosion: base shape,
  mesas, canyon carving, strata offsets and the strata model) and SHARED PASSES over one
  grid (drainage, terrace, droplets, thermal, detail, masks).
- The union grid is world-aligned: its origin and extent are whole multiples of
  tile_size from the world origin, covering every region's bounding box plus its
  transition band, padded to a square (terrain.Grid stays square; downstream stages read
  it unchanged). Providers evaluate only over their own bounding box + band, on
  sub-grids aligned to the union grid's samples.
- Per-sample region weights (sparse: at most a few regions per sample, usually one) are
  computed once (procgen/regions, pure): smoothstep of the SIGNED distance to the
  footprint polygon across the transition width, the distance domain-warped by noise so
  the boundary meanders; composited in priority order (higher priority takes its share
  first, equal priorities split by weight); normalised to 1; a sample no region reaches
  goes to the nearest region. Weights are a pure function of world XZ and the region
  list, so any tile can compute its own.
- Blending happens BEFORE erosion: provider heights and strata offsets blend by
  weight; erodibility, terrace, talus angle and detail are evaluated per region and
  blended per sample. Then erosion runs once over the union, so drainage crosses seams
  and a canyon wall degrades into savannah slopes through erosion, not a ramp.
- **The one-region identity.** A single region whose footprint is the classic square
  must reproduce today's `terrain` stage output byte for byte (same heights, masks,
  tiles). This is the phase 2 gate and stays a test forever.
- Erosion runs either globally (default) or in TILES with an overlap margin and a fixed
  per-tile seed (`erosion: tiled`), so editing one region regenerates its tiles plus a
  margin and leaves distant tiles byte-identical. Long rivers may then change course
  only within a window; seams must stay invisible (cross-fade the margins).
- Outputs are the canyon terrain's outputs (heightmap, masks, albedo, terrain.yaml,
  cooked tiles on the one world tile grid with one quantisation step, the far
  backdrop), plus per-ENVIRONMENT weight masks `mask_biome_<env>.png` and
  `biomes.png` (the overlay), so downstream stages and materials can read biome weight
  like any mask. The artifact is the same `terrain.Terrain`, plus a biome lookup.

### Biome-weighted materials

- `region_material` writes one .fmat for the union terrain tiles. Each environment
  contributes its splat stack (its terrain_material's layers and mask rules); the
  generator evaluates every biome's rules, multiplies them by the biome weight, and
  BAKES the per-layer weights for the union (so biome weighting is per pixel at mask
  resolution; height-blending within and across biomes happens in the shader).
- **Deviation from ENVIRONMENT.md (decided after the engine survey): no GL texture
  arrays.** The material pass holds 30 of 32 samplers (TestMaterialFxSamplersFitThe
  MaterialPass); every fx sampler rests on unit 0 between draws because separate units
  cost every draw on Mesa d3d12, and an array sampler cannot share unit 0; and
  `nvTexture` (the vis-buffer rewrite) is sampler2D-only. So the "layer arrays" are the
  existing proven ATLASES widened: a 4×2 atlas of 8 layers (albedo+height, and
  normal+AO — the same two samplers, units 25/26) with the same gutters and unfolded
  gradients, plus a SECOND weights texture (one new sampler, layers 5–8). Every layer is
  resampled to one tile size at build time (default 1k; `FOUNDRY_SPLAT_MAX_SIZE` caps).
  A new effect `splat8` (or an 8-layer mode of `splat` that is byte-identical when only
  4 layers are set) — today's 4-layer splat output must not change.
- Keep the Nanite visibility-buffer rewrite rules (textureGrad only, no new derivative
  calls, no runtime-indexed sampler calls — one unrolled call per layer, see
  surface_fx.glsl:302-311); the sampler-budget test must still pass with the new unit.

### Biome-weighted placement

- Expanded placement stages multiply every candidate's density by its biome (region)
  weight at the candidate point and reject candidates outside the region's bounds +
  band. Candidate lattices/Poisson seeds are world-aligned and keyed by (region seed,
  cell), so a region's placements depend only on its own inputs and the ground under it.
- IDs: the expanded stage ID carries the region (`gen.<doc>.r_<region>_<stage>.…`), so
  `genlevel` can splice one region by prefix (`gen.<doc>.r_<region>_`).

### Adjacency rules

- `procgen/regions` holds the adjacency table: each environment pair → transition width
  or forbidden + suggested intermediate. Built-in defaults from ENVIRONMENT.md's table;
  a project may override in resources/environments/adjacency.yaml.
- A pair is ADJACENT when their weights overlap (both > 0.01 at some sample, on a coarse
  raster). Forbidden adjacent pairs warn in the editor and in `foundry gen run`, and the
  editor offers "Insert intermediate region": a new region of the suggested environment
  covering the band between them, with a priority above both, that removes the overlap.
- When a pair has a table transition and a region's own `transition` is the default,
  the table's width is used for that pair.

### Incremental regeneration

- Per-stage-per-tile hashing for `region_terrain` (and the cooks): a tile's hash folds
  in only the region data whose footprint + band + erosion margin touch the tile's
  window. The run engine gains an opt-in for stages that manage their own files
  (`StageKind.Incremental`): it keeps the previous outputs and hands the stage the
  previous manifest entry (with per-tile hashes); the stage reuses fresh tiles and
  prunes the rest.

## Phases

Each phase closes on its GATE, verified by a test where possible.

### Phase 0: performance prerequisite — DONE
docs/canyon/PERF.md records the 1070 work. The biome-volume vegetation freeze on WSLg is
still open (ships disabled); phase 6 owns retesting it natively on Windows.

### Phase 1: region volumes and the level generation doc
1. The `region:` volume block: world model, YAML load/save round trip, fingerprint, copy/
   paste, undo, Claude reference, the volume inspector (environment combo listing the
   presets, generator file combo, priority, transition, anchor), and footprint editing
   (add/move/delete points in the editor's top-down or ground-plane interaction; reuse
   whatever polygon editing exists). Drawn in the 3D view as an outline in the
   environment's colour.
2. procgen/regions (pure): Region, RegionSet, polygon signed distance, warped
   weights, priority compositing, normalisation, `Weights(x, z)`, `Raster(grid)`,
   bounds, the adjacency table type (data only in this phase). Thorough unit tests
   (weights sum to 1, inside = 1, priority, determinism, concave polygons).
3. Environment presets: the registry, `desert_canyon` preset (from today's template,
   minus level-wide stages), a stub `savannah` preset (rolling plains terrain params,
   fleshed out in phase 6), write-if-absent, `foundry gen env list|write`.
4. The `regions` stage (Shared) with the synced list param and grid settings; its
   Preview draws the biome-colour overlay (and region outlines) in the Generator's 2D
   view; `genlevel.SyncRegions`; the `level_world` template; `foundry gen world
   <level>`; the editor's "Generate world" entry (create/open the level doc).
5. `procgen`: `StageKind.Regional`, `Env.Region` (key, env, weights, bounds), and plan-
   time expansion (with tests on a fake kind), even though only phase 2+ stages use it.
6. GATE: two regions (desert_canyon + savannah) in a level preview as a biome-colour
   overlay in the 2D view (a test renders/inspects the preview image).

### Phase 2: field providers and erosion over the union
1. Split procgen/terrain into providers + shared passes, keeping `Generate` as the
   one-provider case. Per-sample blended strata model for erodibility / terrace / talus
   / detail.
2. `region_terrain` stage (Shared): union grid, provider sub-grids, blend, shared
   erosion (global or tiled with margins and fixed per-tile seeds), masks + biome
   masks, tiles on one world grid with one quantisation step, the far backdrop
   continuing the union's edge (the dominant biome at each edge).
3. GATE: (a) the canyon regenerates byte-identically as one region (golden test against
   the classic stage); (b) a river crosses a region seam (test: a canyon in region A
   ending at the seam keeps a continuous high-flow channel into region B); (c) tiled
   erosion: editing a far region leaves distant tiles' heights byte-identical.

### Phase 3: biome-weighted materials
1. The 8-layer splat (see "Biome-weighted materials"): material kind/params, resolver,
   SurfaceState, uniforms, 4×2 atlas build + BC7 path, the second weights unit,
   surface_fx branch with unrolled layer scores and the existing skip-by-floor, vis-
   buffer compatibility, sampler budget test updated.
2. `region_material` stage: per-biome rule evaluation (reuse terrain_material's
   groundWeights per environment's params) × biome weight → baked 8-layer weights;
   layers deduped by texture id across biomes (two biomes sharing a rock share a layer;
   more than 8 distinct layers is a validation error naming them); the .fmat and the
   far variant.
3. GATE: the vis-buffer material program builds (test); forward and vis-buffer GL tests
   draw a two-biome tile; headless render from the ground at a seam shows no material
   seam (look at it).

### Phase 4: biome-weighted placement
1. Placement stages (scatter, vegetation, cliff_wall, wall_sheet, rocks and the
   polyhaven_* asset stages) become PerRegion: density × biome weight, region-bounded
   candidates, region-keyed seeds; asset stages dedupe shared downloads/cooks.
2. Region-keyed IDs and region-scoped splice in genlevel (and the editor splice).
3. GATE: re-running one region leaves every other region's placements byte-identical
   (test compares placements.yaml bytes).

### Phase 5: editor tools and adjacency rules
1. Generator editor: a region list for level docs (select ↔ highlight in 2D, jump to
   the level volume), the weight overlay, adjacency warnings, "Insert intermediate
   region"; the level editor shows the same warnings when regions move.
2. Adjacency table defaults + adjacency.yaml override; warnings in `foundry gen run`.
3. Per-tile incremental runs (`StageKind.Incremental`, per-tile hashes in the manifest).
4. A guide page: docs/guide/35-regions.md (next free number; check README) + README row.
5. GATE: a forbidden pair warns, and one click inserts an intermediate region that
   resolves it (test the operation headless; verify the warning clears).

### Phase 6: savannah environment
1. The `savannah` preset: wide rolling plains, soft strata, gentle drainage; Poly Haven
   savannah ground textures, searsia / quiver / acacia-stand-in trees, shrubs, grasses;
   dense grass at scale and scattered tree clumps.
2. The vegetation freeze: reproduce and fix, or prove fixed, NATIVELY on Windows
   (cross-build as docs/canyon/PERF.md describes and run through WSL interop on the
   user's GTX 1070); grass at scale depends on it.
3. A `canyon_savannah` level template: two regions with a 200 m band.
4. GATE: the canyon-to-savannah join shows no seam from the ground (headless renders
   from several poses, looked at) and holds the frame budget (numbers in PHASE_6.md).

## Engine facts (surveyed 2026-10-12, before phase 1)

### Volumes and the level editor
- `world.Volume` (world/volume.go:35-176) = a shape (box/sphere/cylinder, `VolumeShape`)
  + optional role blocks (`Fog`, `Cloud`, `Gravity`, `Doorway`, `Scatter`, `Objective`,
  `Context`, `LoadLevel`, `Checkpoint`). `RawVolume` is level_schema.go:12-57; each block
  `*RawX` with omitempty; conversion `VolumeFromRaw`/`VolumeToRaw` (volume.go:465-522); the
  block pattern to copy is volume_context.go (`VolumeContextFromRaw/ToRaw`, `IsContext`).
- Load levelio/level_loader.go:484-496; save level_save.go:137-144; prefabs carry volumes.
- Shape switches: `BoundingKind` :221, `CageMesh` :572, `ShapeContainsPoint` :240-252,
  `WorldAABB` :262. No polygon volume exists; geom has `PointInPolygon`
  (geom/triangulate.go:517), `EarClip` (:96).
- Touch list for a new block: schema + conversion; `volumeRoleSummary`
  (volume_context.go:326-359); `duplicateActiveVolume` (editor_volume.go:109-143 — deep-
  copies by hand; give Region its own copy); copy/paste/undo/fingerprint go through
  `VolumeToRaw` and RawLevel snapshots (nothing to do); render cage `CageMesh` /
  `render.RenderVolumes`; selected outline `drawSelectedVolumeOutline`
  (editor_volume.go:276-294, `gizmo.GizmoLine`); picking `pickVolumeRayT`/`pickVolumeAt`
  (:200/:219); explorer label (editor_panel_explorer.go:513-562); YAML field docs
  (`FOUNDRY_UPDATE_YAML_DOCS=1 go test ./internal/editor -run TestYAMLFieldDocsUpToDate`);
  strict YAML in the editor and genlevel `ToRawVolume`; docs/guide/13-volumes.md.
- Inspector: editor_volume_panel.go `editorVolumeProperties` (:62-81) — one
  `editorPropGroup` per block; copy `editorVolumeContext` (:641-677) / `editorVolumeScatter`
  (:198-278): a checkbox creates/deletes the block with `EditorPushUndo` first; combos via
  `imguibackend.BeginCombo/SelectableV`.
- The level editor has NO 2D top-down view; footprint points need 3D handles (ray vs the
  ground / y-plane of the volume) — or edit them in the Generator's 2D view.
- Gizmo: `gizmoKindVolume` moves `v.Position` only (translate/scale/rotate gizmos).

### Generator editor
- Files internal/editor/editor_generator_{window,panel,params,view2d,view3d,job,splice,
  undo,claude}.go. The 2D view draws the selected stage's `procgen.PreviewData` (Images,
  Points, Lines, Meshes, Notes) — `genView2DCanvas` (view2d.go:348-468), +X right, +Z down,
  `toScreen/toWorld` closures; spline editing `genStageSplines`/`genView2DInput`
  (:472-557) works on any `ParamSpline`, including inside list items — so a regions
  stage whose list items carry a `footprint` spline (closed) is editable there for free.
- Runs: `genBeginRun` (job.go:89) → `genStepRun` (:168) → `genRequestSplice`.
  Previews debounced via `procgen.Preview` + `NewPreviewCache`.
- `genLevelIsOpen`, `genOpenTargetLevel`; open level splices through the editor mutation
  path (`genSpliceOpenLevel`, one undo step), closed levels through `genlevel.SpliceFile`.

### genlevel / gencli
- `SpliceRaw` (splice.go:128-197) drops entries with the generator prefix `gen.<name>.`
  and appends the new ones; `ToRawScenery` resolves `chunk: auto` via `Options.ChunkAt`.
- `SettingKeys` fog/sky/view_distance/sun/ambient/tonemap/spawn (settings.go:33).
- gencli: `run` (-force -no-splice -workers -q -stage), `check`, `list`, `new`
  (-template -force). `resolveDoc` takes a name or path.

### Materials (splat)
- Kind rows in internal/material/material_kinds_terrain.go (`KindSplat`, `SplatLayers=4`,
  `splatParams()` :74-161); resolver material_resolve_fx.go (`SplatLayer`, `SurfaceFx.
  Splat*`); gfx/material_fx.go units 24 weights, 25 albedo atlas, 26 normal atlas, 27
  strata, 28/29 ground colour/height, 30 detail; `applySplat` :287-355; samplers rest on
  unit 0 (`BindSurfaceFxSamplers` :163-192). Atlases: gfx/material_fx_textures.go
  (`buildSplatArrays` :338, `packSplatAtlases`, gutter `max(size/32,2)`, BC7 path).
- Shader resources/shaders/surface_fx.glsl (fxSplatSurface :516-543, unrolled per layer,
  `fxSplatFloor` skip, anti-tile, relief); called from fragment.glsl:512.
- Vis-buffer rewrite render/nanite_vis_source.go `materialFragmentPart` :131-184.
- Budget test: render/material_fx_gl_test.go:260 `TestMaterialFxSamplersFitTheMaterial
  Pass` (wants exactly 7 fx samplers today; update it deliberately).
- terrain_material stage (procgen/stages/terrain_material.go): layer keys sand/cracked/
  gravel/rock → RGBA; `groundWeights` :225-322 from slope/flow/deposition/cliff masks
  with breakup; outputs weights/ground/height/strata PNGs; rect texel-centred on the grid
  (:637-643); `.fmat` via `writeMaterial` (materials_out.go) as `gen_<doc>_<stage>`; far
  variant terrain_material_far.go.

### Placement
- `terrain.Terrain` (terrain/meta.go:348): Meta, Grid, lazy masks, `MaskAt`, `HeightAt`,
  `NormalAt`, `Contains`. Mask names terrain/masks.go:34-44.
- place.Ground (place/ground.go) wraps it; `Rule.Weight` (place/scatter.go:210-247) is a
  product of bands; `scatterRule` :278 — 32 m blocks, block RNG `Hash64(seed, block)`.
  A biome factor is one more multiplicand in Rule.Weight (from a `biome` weight
  callback / mask), and blocks outside a region's bounds+band are skipped.
- IDs: scatter `<rule>_%05d`, cliff_wall `ledge_/chunk_%05d`, wall_sheet `wall_%d_%d`,
  vegetation `%s_%04d`; `numberPlacements` de-dups.
- vegetation emits `scatter: {kind: biome}` volumes per patch (stages/vegetation.go).
- drift supersedes terrain tiles and scatter placements (skip per-region in v1).

### Templates and environment
- procgen/stages/desert_canyon.go `DesertCanyon` (:49-151): tex (polyhaven_textures) →
  ground (terrain, 512 m @ 0.5) → ground_mat → rockface (wall_sheet) → rockfall (rocks)
  → cliffs/boulders/stones (polyhaven_rocks) → trees/deadwood/bushes (polyhaven_plants)
  → walls (cliff_wall, disabled) → scatter → drift → plants (vegetation, disabled) → air
  (environment: fog, sky, sun, view distance, spawn, tonemap, ambient).
- Far backdrop: terrain.FarField (terrain/far.go) continues the map's edge; stages/
  terrain_far.go writes far tiles + stitch strips; its material's weights clamp at edge.

### The vegetation freeze
- Still OPEN (docs/canyon/PHASE_7.md:298-301, 374-390): two down-canyon views stalled the
  WSLg GPU with biome volumes on; bisected to the volumes; untested on the 1070. The
  foliage path is render/foliage_volumes.go → foliage cells (Nanite foliage).
