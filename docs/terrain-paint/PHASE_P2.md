# Phase P2 — live painting

Built 2026-10-15. Spec: docs/terrain-paint/SPEC.md ("Editor", "Consumers");
P1 is PHASE_P1.md. The user-facing pages are docs/guide/36-terrain-paint.md
("Painting in the 3D view", "Live updates", "Reruns") and the scatter,
grass_field, terrain_material and region_material sections of
docs/guide/33-generators.md.

## What was built

### Per-tile incremental reruns

**Engine** (procgen/paint_incremental.go, small edits to run.go, regional.go,
context.go, manifest.go):

- Every planned stage now has a **base hash**: its stage hash with no paint
  folded in, its refs' base hashes standing in for their hashes. A stroke
  never moves it. `Env.BaseHash` hands it to the stage.
- The plan records the layers a stage names (`paintOwn`) and the layers its
  upstream stages read (`paintUp`, transitive).
- `Env.PaintKey(rect, margin)` is the content key of the paint a piece
  covering `rect` can read:
  - its own layers within one texel (bilinear reach);
  - upstream-only layers within `UpstreamPaintReach` (192 m) more, the reach
    of a tiled scatter's neighbour chain.
- `Env.PieceKey(version, parts...)` hashes a piece's key: base hash, lineage,
  version, parts.
- **Lineage** (`upstreamLineage`): how each upstream output came to be this
  run. `L` means read back from its files; `F(...)` means it ran. A fresh
  terrain and a loaded one differ (quantised heights), and a fresh scatter's
  footprints and loaded ones differ (rounded to the cm). So a piece is never
  reused across the two. The first rerun after an upstream stage ran
  rebuilds every piece once, and the log says which stage ran.
  - A stage can declare `RunContext.ArtifactAsLoaded`: its fresh product is
    exactly its loaded one. A tiled scatter (footprints rounded in memory)
    and a painted material stage (its manifest read back) do, so a stroke
    rerunning them does not make their readers rebuild everything.
- `Env.ReadsPaint`, `ReadsOwnPaint`, `UpstreamPaintHash`,
  `RunContext.PreviousVolumes` (last run's volumes, for re-placing a reused
  piece byte for byte), and `procgen.PlanEnv(doc, id)` (an Env without a run,
  for tools and tests).
- `ManifestStage.removeFiles` now removes nested folders deepest first. A
  pruned tiled scatter used to leave `scatter/tiles/` behind.

**paint** (paint/region.go, set_edit.go; stroke.go edited):

- `Layer.RectHash(rect)` hashes exactly the texels a sample in the rectangle
  reads:
  - a stored tile wholly in range by its content hash;
  - a partly covered neighbour by just the texels in range;
  - an all-zero strip as nothing.
  So a stroke in the middle of a tile does not dirty its neighbours.
- `ChangedTiles`, `SnapshotChanged` and `Set.CopyAs` (for rename).
- `Stroke.Curve`: smooth (the zero value, P1's smoothstep), linear, sharp or
  dome.

**Stages.** grass_field, scatter, terrain_material and region_material are
now `Incremental` kinds.

- **grass_field** (grass_field_incremental.go): a tile's key is base hash,
  name, rectangle and `PaintKey(rect)`. A matching tile keeps its `.fgrass`
  untouched, re-places last run's volume, and takes its per-kind counts from
  the field's runs. An empty tile with a matching key stays empty. The kit is
  rebuilt (same bytes).
- **scatter** (place/scatter_tiled.go, stages/scatter_incremental.go):
  **tiled mode** when the stage reads paint, own or upstream.
  - Tiles are 64 m, world-aligned, from the origin.
  - Each tile runs every rule over its own 32 m blocks (seeded by world
    block, as a region's are). Satellites stay inside their tile. The cap is
    shared out by area.
  - Tiles run in four parity phases. A tile keeps clear of its
    earlier-phase neighbours' rocks and the avoid list. Same-phase tiles are
    a tile apart, and the reach is checked: a footprint reaching half a tile
    returns `ErrTiledReach`, and the stage falls back to the classic scatter
    and logs why.
  - A tile's key is its own inputs (base hash, `PaintKey(tile)`) plus the
    output hashes of its earlier-phase neighbours. So a change spreads only
    while rocks keep changing.
  - Tiles are cached exactly (`place.EncodeScatterTile`: every float as its
    bits, plus the tile's stats) as `tiles/t_<i>_<j>.bin`.
  - IDs are `<rule>_<i>_<j>_<nnnn>` (negative coordinates spelt `m<n>`).
  - A scatter reading no paint is byte-identical to before.
  - The preview tiles too when the stage names a layer (no cache).
- **terrain_material / region_material** (paint_weight_cache.go): with a
  `paint` list, the weights the rules (or the regions' blend) produce
  BEFORE paint are cached as `prepaint.bin` (exact float32s), keyed by base
  hash and `UpstreamPaintHash`.
  - A stroke reuses them and reruns only the pointwise paint step and the
    maps.
  - height.png and strata.png follow no paint, so they are kept when the
    cache was reused (`keepUnpainted`).
  - A stage with no paint list writes no cache.
- Fallbacks are logged: "not tiled (…)", "unpainted weights rebuilt
  (something besides the stage's own paint changed)", "<stage> ran this
  time: pieces made against its last output are rebuilt", "reused N of M
  tiles".

### Live update in the editor

editor_generator_paint_live.go:

- **Trigger.** Mouse-up of a stroke (2D or 3D), an undo or a redo arms a
  300 ms debounce, when:
  - the generator's level is open in the level editor;
  - its output folder has a manifest;
  - **Live** is ticked (on by default).
- **Run.** `procgen.Run` on a clone (paint frozen copy-on-write), off the UI
  thread, with no modal. It waits for a full run and for `EditorCompiling()`.
  A stroke made during a run reruns after it. The step runs from both
  `genStepJobs` and the level editor's frame, so it works with the Generator
  window shut.
- **Live splice** (`genLiveSplice`), on the main window's context once
  `genSpliceBlocked()` clears. The open level is changed in place:
  - only the generator's entries under the prefixes of the stages that ran
    (and those they supersede) are compared with the run's, by normalised
    raw form;
  - unchanged entries are kept;
  - changed ones are replaced at their index, new ones appended, gone ones
    removed (scenery released with `Delete`);
  - there is no reload and no level-editor undo step.
- **Refresh.**
  - Grass fields rewritten since the run began go through
    `render.ReloadGrassFields` (new, render/grass_field_reload.go): it drops
    the resident copies and lifts the per-frame load budget once.
  - `.png`s the ran stages rewrote that are registered textures go through
    `gfx.ReloadTexture`.
  - When a material stage ran: `material.LoadAll()` and
    `gfx.RefreshFxDataMaps()` (new, gfx/material_fx_refresh.go), which makes
    the splat's data maps re-stat their files on the next bind instead of
    within `fxStampEvery` (1 s).
  - `collision.InvalidateScenery()`.
- **Timing.** Each update records wait, run, splice and total from mouse-up
  (`genLiveTimes`). The status line, the log and the Live checkbox's tooltip
  show it.

### 3D viewport brush

editor_generator_paint3d.go:

- **Active when** the Generator window is open with Paint on and a layer
  chosen, the level editor holds the generator's level in 3D, and no
  terrain tool, scenery brush or stamp is armed.
- **Aim.** `collision.Raycast` against solid geometry (the generated
  terrain's tiles collide as their cooked mesh). `genPaintRayHit` is a
  variable, which the tests stub with a plane.
- **Strokes** go through the same `genPaintBegin` / `genPaintDrag` /
  `genPaintEnd` as the 2D view, so there is one state, one undo stack and
  the same live kick.
- **Hooks** are small additive edits:
  - editor.go: keys ahead of the level's Ctrl+Z, Ctrl+wheel, the frame, the
    live step, mouse-up;
  - editor_3d.go: mouse-down;
  - editor_3d_pick.go: the overlay and the ring;
  - editor_generator_job.go: the live step;
  - editor_generator_view2d.go: Ctrl+wheel in 2D.
- **Ring**: 48 points dropped onto the ground by downward rays, gizmo lines,
  an inner falloff ring, red while erasing.
- **Overlay**: its own pass after the world, in the depth-tested part of
  `editorRenderInteractiveOverlays`.
  - Each stored tile of the active layer gets a mesh draped over the ground:
    33 × 33 at 2 m, each vertex dropped by a ray and lifted 0.12 m, with a
    polygon offset.
  - It is textured with P1's tint (`genPaintTileRGBA`), alpha-blended and
    depth-tested.
  - The program is inline (`gfx.CompileProgramFromSources`) and samples
    texture unit 0 for its own draw only.
  - No world or Nanite pass, sampler, FX bit or variant is touched.
  - Meshes build two a frame and are kept. Textures re-upload when a stamp
    writes the tile. A reset from the Generator window is deferred to the
    main context.

### Brush feel and P1's gaps

- Shift held as a stroke starts erases (`genPaint.strokeMode`; the brush's
  mode is unchanged).
- `[` and `]` resize in both views. Ctrl+wheel sets strength in both.
  Ctrl+Shift+Z redoes.
- A curve combo (smooth, linear, sharp, dome) and a spacing slider. Drags
  stamp every spacing × radius along the path, so a fast drag leaves no
  gaps.
- `foundry gen paint -curve`.
- **Layer…** menu (editor_generator_paint_layers.go, procgen/paint_edit.go):
  - Rename rewrites the declaration and every mask param naming the layer.
    The pixels follow as a copy-on-write clone.
  - Delete drops the declaration and clears the params. The pixels stay
    undeclared in memory, so a document undo brings them back. The folder
    stays on disk.
  - Both are document undo steps.

## Files

New:

- internal/procgen/:
  - paint_incremental.go
  - paint_edit.go
  - paint_edit_test.go
- internal/procgen/paint/:
  - region.go
  - set_edit.go
  - region_test.go
- internal/procgen/place/:
  - scatter_tiled.go
  - scatter_tiled_test.go
- internal/procgen/stages/:
  - grass_field_incremental.go
  - scatter_incremental.go
  - paint_weight_cache.go
  - paint_incremental_test.go
- internal/editor/:
  - editor_generator_paint_live.go
  - editor_generator_paint3d.go
  - editor_generator_paint_layers.go
  - editor_generator_paint_live_test.go
  - editor_generator_paint3d_gl_test.go
  - editor_generator_paint_bench_test.go (opt-in)
- internal/render/:
  - grass_field_reload.go
  - grass_field_reload_test.go
- internal/gfx/material_fx_refresh.go
- this file

Edited (targeted):

- procgen:
  - run.go (plan fields, base hash, lineage, `AsLoaded`, a log line)
  - regional.go (base hash)
  - context.go (Env and RunContext fields)
  - manifest.go (nested folder removal)
- procgen/paint/stroke.go (Curve)
- procgen/place/scatter.go (settings, result, the tiled branch)
- procgen/stages:
  - scatter.go, grass_field.go, terrain_material.go, region_material.go
    (`Incremental: true`)
  - scatter_run.go (tiled placement, IDs, preview, artifact)
  - grass_field_run.go (tile reuse)
  - terrain_material.go, region_material_run.go (weight cache, kept maps,
    artifact)
- procgen/gencli: gencli_paint.go (`-curve`), gencli.go (usage)
- editor:
  - editor_generator_paint.go (curve, spacing, Shift, Live, Layer menu, live
    kicks, 3D overlay dirtying)
  - editor.go, editor_3d.go, editor_3d_pick.go, editor_generator_job.go,
    editor_generator_view2d.go (hooks above)
- docs:
  - guide/36-terrain-paint.md
  - guide/33-generators.md

## Tests

**New:**

- procgen/paint:
  - TestRectHashFollowsOnlyTheTexelsInReach
  - TestChangedTilesAndSnapshots
  - TestFalloffCurves
- procgen:
  - TestRenameAndDeletePaintLayer
  - TestBaseHashIgnoresPaintAndPaintKeyFollowsIt
- procgen/place:
  - TestTiledScatterKeepsApartAcrossTilesAndCaches: no overlaps across tile
    edges; 1 and 7 workers identical; all 16 tiles from the cache place the
    same rocks.
  - TestTiledScatterRefusesHugeFootprints
- procgen/stages, **incremental == full, byte for byte**: run, make the stage
  from nothing, stroke, rerun incrementally, keep its folder, make it from
  nothing again in the same folder, compare every file. The same folder
  keeps the same paths in the files.
  - TestGrassFieldIncrementalMatchesFull (asserts "reused 14 of 16 tiles")
  - TestScatterIncrementalMatchesFull (two rules, satellites, a paint band
    and an exclude; asserts some tiles reused and some re-scattered)
  - TestTerrainMaterialIncrementalMatchesFull and
    TestRegionMaterialIncrementalMatchesFull (assert "unpainted weights
    reused")
  - TestPaintKeysFollowOnlyTheirTiles: the dirty-tile computation. A stroke
    mid-tile plus one on a tile edge change exactly grass keys [2_3 3_3].
  - TestScatterWithoutPaintStaysClassic
- editor (headless, stubbed ground):
  - TestGeneratorPaint3DBrush: a fast drag has no gaps; one undo step; Shift
    erases; Ctrl+Z and Ctrl+Shift+Z through the level editor's keys; `[ ]`;
    Ctrl+wheel; the ring on the ground; the draped mesh; disarms under the
    terrain tool.
  - TestGeneratorPaintLiveSpliceDiffs: replaced, added, removed and kept;
    hand-made and not-run stages untouched.
  - TestGeneratorPaintLiveUndoRerunsAndSplices: stroke → debounce → real
    run → splice; a second stroke; undo reruns and removes the tile's
    placement; the level file is never written.
  - TestGeneratorPaintLayerRenameAndDelete (with undo).
- editor GL (`FOUNDRY_GL_TEST=1`): TestGeneratorPaint3DGLOverlayDraws. A
  painted disc draws its green tint at the centre and nothing 26 m off, with
  no GL error. It passes.
- render: TestReloadGrassFieldsForgetsOnlyThoseNamed

**Suites at the end:**

- `go build ./...` passes.
- `go vet` passes on procgen, paint, place, stages, gencli, editor, render
  and gfx.
- `go test ./internal/procgen/...` passes in every package:
  - stages 179 s
  - rocks 37 s
  - terrain 24 s
  - gencli 20 s
  - TestTerrainStageGolden is unchanged.
- `go test ./internal/editor/...` passes except TestScoreSibeliusKeymap and
  TestScoreRebinding (the score session's).
- `go test ./internal/render/...` (non-GL), `./internal/gfx/` and
  `./internal/` (layering) pass.
- GL: the editor's TestGeneratorGL* and the new overlay test pass. The full
  `FOUNDRY_GL_TEST=1 ./internal/render/...` suite was not run: the only
  render change is a non-GL map edit, unit-tested.

## Measured

The scratch project is `$S/pproj` (`$S` = this session's scratchpad). Its
`live_demo.fgen` is:

- a 768 m terrain at 1 m in 128 m tiles (36 tiles);
- a terrain_material with a trail override;
- grass_field `sward`: a sparse rule plus a `meadow`-banded 4/m² rule,
  `path` excluded, avoiding `rubble`;
- scatter `rubble`: a `rubble`-banded rule, `path` excluded, `ground:
  ground_mat`, so the trail is upstream paint for it;
- 144 scatter tiles.

The first full run took 15.5 s (terrain 10.9 s).

**Editor**, stroke-to-visible (TestGeneratorPaintLiveBench, a GL context, the
level loaded as the editor loads it: 903 scenery, 36 volumes). Each stroke is
a small disc somewhere unpainted. The time runs from mouse-up to the level
edited and the files reloaded, including the real 300 ms debounce:

| Stroke | Total | Run | Splice | What reran |
| --- | --- | --- | --- | --- |
| meadow (grass) | 0.44–0.47 s | 137–169 ms | 2 ms | 1 grass field |
| trail (material) | 0.60 s | 293 ms | 6 ms | splat paint, 42 of 144 scatter tiles, 10 grass fields |
| rubble (scatter) | 0.52–0.57 s | 198–214 ms | 26–55 ms | 11 of 144 tiles (+32 rocks), 10 grass fields |

These exclude the next frame's draw. Grass fields load in that frame. Added
or changed scenery makes the Nanite pool rebuild (its signature changes);
that cost was not measured. Before the height/strata reuse, the trail
stroke's run took 792 ms (1.10 s total).

**CLI** (`foundry gen run -no-splice`, process wall time including about
70 ms of start-up, on a box at load average 6–20 from other sessions):

- meadow: 0.18–0.41 s
- trail: 0.6–3.4 s; the slow end was under a load average of 22
- rubble: 0.26–1.3 s

## Verified, and not

- **Rendered**: before/after shots of an incremental stroke via
  `foundry gen paint` → `foundry gen run` (incremental, spliced) →
  `FOUNDRY_NANITE_VSM=0 foundry play run`. They are in `$S/p2before/` and
  `$S/p2after/` (persp.png, top.png, low.png), and were looked at.
  - After the strokes, the view at (−150, 150) shows a dense round meadow
    where there was only sparse grass, and a straight withered-grass trail
    through it with no grass on it.
  - A cluster of new rubble rocks sits to its east.
  - The rest of the frame is unchanged.
  - That run reused 81 of 144 scatter tiles and 20 of 36 grass fields, and
    the material's unpainted weights.
- **GL**: the overlay pass draws correctly into a real context
  (TestGeneratorPaint3DGLOverlayDraws). The live pipeline ran against a real
  GL context and a fully loaded level (the bench).
- **NOT verified in a real interactive editor window.** No one has dragged
  the brush in the 3D viewport, seen the ring or overlay over real Nanite
  terrain, or watched a live update land on screen. All of that is covered
  only by the headless and GL tests above.

## Known gaps (carried forward)

- **Upstream-only layers are hashed with a 192 m margin.** That is right for
  a scatter feeding a grass avoid, but conservative for a material feeding a
  scatter's `ground` (the rocks do not depend on the ground map by
  position). A trail stroke re-scatters about 7×7 tiles round it.
- **The first rerun after an upstream stage ran rebuilds every piece** (the
  lineage rule). This includes the first stroke after a full run that
  cooked the terrain.
- **Tiled scatter changes rock layout** for any scatter that reads paint,
  P1's included. It is per tile and deterministic, but not the classic
  layout. A huge footprint falls back to the classic, whole.
- **Materials**:
  - The weights are one texture over the terrain, so a stroke rewrites and
    re-reads the whole PNG. There is no sub-rectangle upload.
  - The paint step runs over every sample. It is pointwise and cheap.
  - region_material's far weights and per-tile materials rerun whole.
- **The Nanite pool rebuild** on a scenery change after a scatter stroke is
  not measured. The splice itself is 26–55 ms on 900 placements.
- **The overlay** follows the ground at 2 m. It can clip into a sharp ridge
  between vertices, and uses the polygon offset to compensate. Meshes are
  built two tiles a frame, so a freshly opened, heavily painted world fills
  in over a few frames. It draws only in the main level window.
- **The brush aims at collision geometry.** A terrain cooked without
  collision is not hit, and the ring does not show.
- **No tablet pressure**: "pressure-like" is the falloff curve choice.
- **Layer delete** leaves the layer's folder on disk. A renamed layer's old
  folder stays too, undeclared.
- **P1's preview** still reruns whole after a stroke (no per-tile preview
  cache).
