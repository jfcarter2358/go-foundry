# Phase W2 — ground_edit and ponds

Built 2026-10-15. Spec: docs/terrain-paint/SPEC.md ("Consumers" →
`ground_edit`, "Water" → "Ponds, lakes and marshes"); earlier phases are
PHASE_W1.md, PHASE_P1.md, PHASE_P2.md and PHASE_P3.md. The user-facing pages
are docs/guide/33-generators.md ("ground_edit (Terrain)") and
docs/guide/36-terrain-paint.md ("Ponds, lakes and marshes", "Height and
flatten", "Reruns").

## The mechanism, and why

**`ground_edit` is a separate stage and a terrain source.** It is in
`TerrainSources`, its artifact is a `*terrain.Terrain`, and its output folder
is a whole terrain folder. Stages that should see the edited ground name it
as their `terrain`:

- terrain_material
- region_material (its `terrain` now accepts a ground_edit)
- scatter
- grass_field
- drift
- wall_sheet, cliff_wall and vegetation

The alternative was the terrain stage applying the edits itself. It was
rejected for three reasons:

- every stroke would rerun erosion, which takes seconds to minutes;
- the classic `terrain` stage is not incremental at all;
- `region_terrain` with global erosion is not incremental below the stage.

As its own stage, a stroke reruns ground_edit against the terrain's files
read back, and recooks only the tiles it moved. The terrain stage's output is
never touched, so a document without ground_edit is byte for byte what it was
(TestTerrainStageGolden passes unchanged).

**ground_edit owns the terrain's placements downstream.** It supersedes every
placement of its source (tiles, backdrop ring, stitch strips, region water)
and places each again: a moved tile with its recooked mesh, the rest
untouched (same model path). So a later stage that supersedes or re-dresses
"the terrain's tiles" finds them all under ground_edit, as it would under a
terrain stage:

- drift's recooking;
- region_material's per-tile materials;
- the editor's live splice.

Stages that need the terrain stage's own parameters follow ground_edit to its
source (`terrainSourceStage`, `refersToTerrain`):

- drift's and cave's tile plan;
- region_material's regions and far segments;
- region_terrain's auto material (a region_material reading a ground_edit of
  this terrain still names the tiles' material).

All of these behave exactly as before when no ground_edit is in the chain.

## What was built

### `terrain.CarveBasins` (procgen/terrain/basin.go)

A pure function of the heights, a painted 0..1 area and a profile:

1. **Components.** Samples ≥ `threshold`, 8-connected. Each component is one
   body; one smaller than `min_area` is skipped.
2. **Signed shore distance**, metres. An exact Euclidean distance transform
   (Felzenszwalb, separable) inside and outside, then rounded by two 3×3 box
   passes so the outline is smooth.
   - A first try refined the edge by the painted value's slope,
     (v − t)/|∇v|. It stepped the bank where the brush saturates, because
     there the value has no slope, so it was dropped.
3. **Level.** By default the spill height: the lowest ground on the rim (the
   ring 1.5 cells outside the outline) less `freeboard`. Or a pinned level.
4. **Bed.** `level − depth(d)` with a shelving profile:
   - bank to `shelf_depth`, then a shelf `shelf` wide, then down to `depth`,
     reaching it 3/4 of the way to the most inland point;
   - a small body has its bank and shelf scaled down, and is shallower;
   - bed `noise` (none at the waterline);
   - marsh `hummocks` biased upward (+0.35), so some break the surface.

   Inside the outline the ground is only ever lowered.
5. **Bank, outside within `reach`.** Ground above
   `level + freeboard + bank_slope·dist` is cut down to it, faded over the
   last 40% of the reach. A pinned level above the rim raises a berm instead:
   `level + freeboard` for 1.5 cells, then falling away at the bank slope.
   The bed is clamped above the tiles' closed base (+0.25 m).
6. **Water** is flooded from the body's submerged inside through every
   8-connected sample below the level, so no water stands past the rim. The
   result is a `terrain.WaterResult`, the same type FindWater builds.

Also in procgen/terrain/edit.go:

- `QuantizeHeights` and `QuantizeUnit`: exactly what the files give back.
- `SurfaceMasks`: computeMasks' slope and curvature formulas over a window.
- `GroundEditMeta`: terrain.yaml's new `ground_edit:` key (`omitempty`, so
  every existing terrain.yaml is unchanged).
- `Terrain.RectKey` (below).

### The stage (procgen/stages/ground_edit*.go)

**Parameters:**

| Param | |
| --- | --- |
| `terrain` | the source: terrain or region_terrain |
| `height` | `[{layer, scale}]` |
| `water` | `[{layer, style: pond\|lake\|marsh, material, threshold, bank, shelf_depth, shelf, depth, noise, noise_size, hummocks, hummock_size, bank_slope, reach, min_area}]` |
| `bodies` | `[{at, level, auto, material}]` |
| `freeboard` | |
| `cook_memory` | |

The style's default material is `water_lake` for pond and lake, and
`water_swamp` for marsh.

**Run** (ground_edit_run.go):

1. The source heights, requantised to the source's own heightmap step. A
   source that ran this run is unquantised, and one read back is not; after
   this the edit is identical either way.
2. Height layers are added, then the water layers are carved in list order.
3. The heights are quantised onto heightmap.png's 16 bits over the source's
   range ±16 m (×4 until the edits fit). A range that followed the edits
   moved every sample by a quantum whenever a stroke dug a little deeper, and
   every downstream piece rebuilt. The tests found this.
4. The masks:
   - slope and curvature are recomputed within the stencil's reach of a moved
     sample;
   - the water mask is the source's, if any, with the bodies' depth over it;
   - flow, deposition, cliff, strata and albedo are the source's,
     hard-linked into the folder (copied if links fail).
5. Tiles: every source placement is superseded and re-placed. A tile whose
   samples (or the normals' ring) moved is recooked on the source's tile
   plan, base and quantisation step into `tile_<i>_<j>_<key10>.fnm`. The key
   in the file name makes a recook a new model path, so the editor's live
   splice reloads the mesh rather than keeping the cached one under the old
   name.
6. Water: one mesh per tile and material, `pond_<i>_<j>_<material>`. It
   reuses region_terrain_water.go's `waterCells` (the surface carried two
   cells under the bank) and `waterTileMesh`, cooked the same way (locked
   rect and border, collision none, not solid).
7. terrain.yaml (water bodies with kind pond/lake/marsh, the `ground_edit`
   record) and `water_bodies.yaml`.

`ArtifactAsLoaded` is true: the in-memory terrain is bit for bit what Load
reads back (a test checks every sample and mask).

**Incremental.** The stage is `Incremental`:

- tile and water-mesh keys hash the base hash, a version and the piece's
  quantised samples (or cells);
- there is no upstream lineage in the key, because step 1 makes it
  irrelevant (`groundPieceKey`).

**Body keys.** An entry in `bodies` applies to the component holding the
grid sample nearest its `at` point:

- repainting edges keeps it;
- painting the point dry orphans it (logged, listed in water_bodies.yaml and
  the panel);
- Lock writes the body's anchor (its most inland sample) as `at`.

This was chosen over a level channel in the paint: a channel would have to be
repainted consistently over the whole body, and a stroke across two bodies
would mix their levels.

### Downstream incremental correctness: rect keys (procgen, small edits)

A pond's level depends on its whole outline, so a stroke can move heights far
from the brush. P2's rule hashed upstream-only layers 192 m wide. That would
also have dirtied every piece of a 256 m world on every stroke.

- **`StageKind.RectKeyed`** and the **`procgen.RectKeyer`** interface
  (paint_incremental.go). For every RectKeyed ancestor (transitively), a
  piece's `PaintKey` folds in the artifact's `RectKey(rect ± margin)`. For
  ground_edit that is a hash of the heights and the water mask over the rect,
  plus the curvature stencil's reach.
- Layers that reach a stage **only** through RectKeyed refs
  (`paintCovered`, computed in planPaint) are then left out of the 192 m
  hash. If any RectKeyed artifact fails to give a key, they go back in.
- **Two fixes to P2's rules, found here.** A layer that a stage reads itself
  AND an upstream stage also reads used to count only as "own". Here that is
  the pond layer: the material paints mud by it, and the ground_edit carves
  it.
  - `PaintKey` now hashes such a layer at the upstream reach too, unless it
    is covered.
  - `UpstreamPaintHash` now includes own layers that are also upstream.

  Before the fix, terrain_material reused its paint-free weights after a pond
  stroke ("unpainted weights reused"), although the slopes and the wet cover
  had moved (TestMaterialOnGroundEditIncrementalMatchesFull). For every P2
  document, no layer is both own and upstream unless a stage both reads a
  layer and builds on a stage that reads it. Such a document's cache keys
  change once, and its outputs do not.

### Paint: flatten (procgen/paint/stroke.go)

`ModeFlatten` ("flatten") moves a height layer toward `Value − Base(x, z)`.
`Stroke.Base` is the ground under the layer; with none, flatten is set. A
sub-quantum step still moves one quantum, as erase and smooth do.

`stages.HeightLayerBase` (ground_edit_base.go) gives the base:

- the first ground_edit listing the layer;
- its source terrain from the last run's output folder;
- plus the stage's other height layers.

Used by:

- the editor: the mode combo offers flatten on height layers, and the stroke
  start samples the target as base + layer under the cursor;
- `foundry gen paint -mode flatten -value <m>`.

### Presets and recipes

`resources/paint/pond.fpb`, `lake.fpb` and `marsh.fpb` (orders 160, 165,
170, before keep_clear).

Each recipe:

- declares its `water` layer;
- adds the `ground_edit` stage if missing, with its water item;
- adds `cover_grass` (grass_medium_01);
- adds `cover` rules:
  - `shore_reeds`: tall pieces ×6–9, `shore: [0, 4]`, `max_depth: 0.4`;
  - `shore_sedge`: mid pieces, `shore: [1, 9]`;
  - the marsh gets `marsh_reeds` instead (sparser, banded by the marsh
    layer);
- adds the `pond_mud` texture (brown_mud_02) as a material entry with slots
  `[wet, ash, snow]`. This claims the `wet` cover slot, so the wet rule muds
  the banks, and the layer paints mud over the bed;
- pond and lake add the layer to every scatter's `exclude` (no boulders in
  the water).

Grass is kept out of the water by the water mask (every grass rule's
`max_depth` is 0 by default) once it stands on the ground_edit. So no grass
exclude is added; it would also thin the grass on the bank ring the brush's
falloff paints.

Recipe engine changes (paint_preset_recipe.go):

- a new `$source` placeholder (the terrain or region_terrain itself);
- `$terrain` is now the document's ground_edit when there is one, or the one
  the recipe brings;
- `water` and `height` lists merge by `layer`;
- **rewiring**: a recipe that adds a ground_edit points every enabled stage
  whose `terrain` names the source (and accepts a ground_edit) at it, with a
  note per stage. Hoisting then moves the ground_edit above its readers.
- **Fixed a latent P3 bug.** Step 4 used a pointer to the material stage
  taken before stages were appended and hoisted. Once a hoist moved stages,
  material edits landed on the wrong stage, or were lost. It now finds the
  material stage again.

### Editor

**Flatten** (editor_generator_paint.go): `genPaintIsHeight`,
`genPaintFlattenBase` (a variable, stubbed in tests) and
`genPaintBeginFlatten`.

**Water bodies panel** (editor_generator_water.go). It is drawn above a
ground_edit stage's parameters through `genStageExtraPanels`: one line in
editor_generator_panel.go, no new import there.

- It reads `<out>/<stage>/water_bodies.yaml`, rereading on mtime.
- One row per body:
  - layer (style), with a tooltip for anchor, depth and painted area;
  - level: text when automatic, a drag when locked;
  - **Lock** / **Auto**;
  - preset combo: every loaded material with a water effect, or "(layer's)";
  - area.
- Orphaned entries are listed with **Remove**.
- Every change is one document undo step and kicks the live rerun
  (`genPaintLiveKick`). The level drag kicks it on release.

The pond and marsh brushes are the presets. They work in the 2D and 3D
brushes unchanged:

- the 3D brush aims at the recooked tiles' collision mesh;
- water surfaces are collision none, so the brush paints the bed through
  them;
- the live splice handles the supersede-all on the first stroke and the
  changed model paths afterwards.

## Files

New:

- internal/procgen/terrain/: basin.go, edit.go, basin_test.go
- internal/procgen/stages/:
  - ground_edit.go, ground_edit_run.go, ground_edit_water.go,
    ground_edit_base.go
  - ground_edit_test.go, ground_edit_recipe_test.go
- internal/procgen/paint/flatten_test.go
- internal/editor/editor_generator_water.go and
  editor_generator_water_test.go
- resources/paint/pond.fpb, lake.fpb, marsh.fpb
- this file

Edited (targeted):

- procgen:
  - registry.go (`RectKeyed`)
  - context.go (Env fields)
  - run.go (plannedStage.paintCovered, rect keys into the Env)
  - paint_incremental.go (RectKeyer, rect keys, covered layers, the own+up
    fixes, docs)
- procgen/paint: stroke.go (ModeFlatten, Stroke.Base), paint.go (kind docs)
- procgen/terrain/meta.go (`GroundEdit` field)
- procgen/stages:
  - region_terrain.go (TerrainSources, auto material via refersToTerrain)
  - drift.go and cave.go (tile plan via the source)
  - region_material.go (StageKinds, regions via the source)
  - region_material_tiles.go (far segments via the source)
  - paint_preset_recipe.go (above)
  - paint_preset_test.go (the three presets in the shipped list)
- procgen/gencli: gencli_paint.go (flatten), gencli.go (usage)
- editor: editor_generator_paint.go (flatten), editor_generator_panel.go
  (one call)
- resources/paint/keep_clear.fpb (comment)
- docs/guide/33-generators.md and 36-terrain-paint.md

## Tests

New:

- **terrain:**
  - TestEDTIsExact
  - TestBasinProfileShelvesFromTheShore: shore at the outline, shelf depth a
    bank in, full depth in the middle, monotone with no step, outside
    untouched, water stops at the rim, area
  - TestBasinOnASlopeCutsTheBankAndHoldsTheWater: spill on the downhill rim,
    an uphill bank no steeper than bank_slope, every rim sample above the
    water
  - TestBasinPinsSurviveRepaintingAndSetTheLevel: pinned level, berm, a
    lopsided repaint keeps the pin, dry pin orphaned
  - TestMarshHummocksBreakTheSurface
- **paint:** TestFlattenLevelsTheGroundUnderTheLayer
- **stages:**
  - TestGroundEditCarvesPondsAtTheirSpillHeight:
    - two bodies at spill − freeboard, deep beds, rims above the water;
    - water placements, recooked and kept tiles, all 16 superseded;
    - water mask and terrain.yaml;
    - untouched ground the source's;
    - the artifact equals Load, every sample and mask.
  - TestGroundEditPinnedLevelAndStableKeys: a pin 0.4 m over the spill with
    its berm and swamp material, an edge repaint keeps it, painting the point
    dry orphans it.
  - TestGroundEditHeightLayerAndFlatten: a 2 m mound; a flatten terrace at
    12 m via HeightLayerBase.
  - TestGroundEditDownstreamReadsTheEditedGround: grass stands on the edited
    ground, none in the water.
  - TestGroundEditIncrementalMatchesFull (byte for byte, some tiles reused).
  - TestGrassOnGroundEditIncrementalMatchesFull: byte for byte; the log shows
    "tiles: 2 recooked, 5 reused" and "[grass] reused 12 of 16 tiles".
  - TestMaterialOnGroundEditIncrementalMatchesFull (the own+up fix).
  - TestGroundEditOverARegionTerrain: region_material per_tile through the
    ground_edit, tiles wear its material, the source's tiles superseded.
  - TestDocWithoutGroundEditIsUnchanged.
  - TestWaterPresetRecipesSlotInAGroundEdit: pond, marsh and lake on one doc:
    - one ground_edit with three water items;
    - every reader rewired and after it;
    - the wet slot, three paint items, scatter excludes, the reeds;
    - idempotent.
- **editor:**
  - TestGeneratorWaterBodiesPanel: draws before and after a run; Lock, a
    preset, Auto, clearing; an orphan row.
  - TestGeneratorPaintFlatten.

Suites at the end:

- `go build ./...` passes.
- `go vet` passes on procgen, paint, terrain, stages, gencli and editor.
- `go test ./internal/procgen/...` passes in every package (stages 176 s,
  rocks 33 s, terrain 21 s, gencli 18 s). TestTerrainStageGolden and every
  P2 incremental == full test pass.
- `go test ./internal/render/...` (non-GL) and `./internal/` (layering) pass.
- `go test ./internal/editor/...` passes except TestScoreSibeliusKeymap and
  TestScoreRebinding (the score session's).
- No GL test was added. W2 touches no render, shader or material file.

## Measured and seen

Scratch project `$S/pproj` (`$S` = this session's scratchpad). The generator
is `w2_demo.fgen`:

- a 256 m gently rolling terrain at 1 m in four 128 m tiles;
- leafy/withered grass and a sward;
- painted from the CLI with the presets:
  - **pond** at (−60, 40), r 14;
  - **lake**: three discs round (40, −30);
  - **marsh**: two discs round (−50, −65), falloff 0.5.

Runs (`foundry gen run -workers 8`):

- **First full run**: ground_edit 1.3–1.4 s. It carved 5,233 samples,
  recooked 4 of 4 tiles, and made 3 bodies in 4 water surfaces across 2
  materials.
- **Bodies**:

  | Body | Level | Area | Deepest |
  | --- | --- | --- | --- |
  | pond | −0.25 m | 437 m² | 2.1 m |
  | lake | −0.93 m | 3,324 m² | 4.8 m |
  | marsh | −1.0 m | 498 m² wet of 1,130 painted | 0.4 m |

- **A pond stroke** (`gen paint -layer pond -mode raise -disc -48,48,8`, then
  `gen run`), 1.28 s wall for the whole run:
  - ground_edit 568 ms: 1 tile recooked, 3 reused, 3 water meshes reused;
  - terrain_material's paint-free weights reused. That was BEFORE the own+up
    fix, and wrong;
  - both grass stages reused 3 of 4 tiles.
- **The same kind of stroke after the fix** (`-disc -70,30,6`): 1.30 s wall.
  - ground_edit 573 ms: 1 recooked, 3 reused;
  - "unpainted weights rebuilt (something besides the stage's own paint
    changed)";
  - both grass stages reused 3 of 4 tiles.

Shots (`FOUNDRY_NANITE_VSM=0 foundry play run`), all looked at:

- `$S/w2b`, `$S/w2c`: bare-ground variants, the level with the grass volumes
  removed, to judge the banks.
- `$S/w2f`, `$S/w2c/marsh_reeds.png`: the full scene with grass and reeds.

What they show:

- **Pond from the bank (bare)**: a smooth round shore, a mud ring (the wet
  cover and the layer's mud), the water meeting the bank with W1's soft fade
  and foam. No step, no gap, no floating water.
- **Pond shore close**: the shelf visible through the shallows, shore foam
  along the waterline, the bank rising gently from the water.
- **Lake**: a clean, irregular outline from three discs, a narrow mud margin,
  the deep middle as a darker area seen from above.
- **Marsh**:
  - first iteration: one main pool with only edge islands (hummocks 0.4 over
    a 0.45 m bed);
  - after tuning (hummocks 0.6, depth 0.4): broken water between many
    tussock islands; about 44% of the painted area is wet.
- **With grass**: reeds ring the pond and lake shores and stand into the
  shallows; sedge behind; the sward stops at the water.
  - First iteration: reeds at 4/m² were a wall hiding the water, and the
    marsh's 6/m² hid it completely.
  - Tuned to 2.5/m² clustered (`cluster 0.6`), the water shows between the
    clumps.
- **Overview from above**: marsh and lake in their mud rings, the lake's
  shelf and deep middle readable.

`foundry play` hangs (the known WSL GPU wait, PHASE_P3.md):

- multi-shot scripts hung in all 6 runs, each at a different shot;
- one shot per run, retried (`$S/shoot.sh`): 2 hangs in 12 runs.

## Known gaps (carried forward)

- **Shapes come from the brush.** A disc paints a round pond. The bank
  profile is radial from the outline, so a perfectly round brush makes a
  perfectly round bowl; varied strokes look natural. There is no shoreline
  noise beyond the bed noise.
- **The lake's drop-off** reaches full depth 3/4 of the way in. Seen from
  above, the deep middle is a distinct dark area (the absorption saturates).
  Plausible, but a gentler run or a depth curve parameter might read better.
- **One level per body.** No sloping or stepped water (that is a river, W3).
  A pinned level above the rim makes a berm, which on steep ground is a
  visible levee.
- **Flow is not recomputed.** Flow, deposition and cliff stay the source's,
  so a wash running into a pond is still a wash on its bed. Under water the
  material's wet rule wins, so it does not show.
- **The source's own water** (a region terrain's sea or ponds) keeps its
  meshes. A pond carved inside a sea, or a carve that lowers ground under a
  region pond, is not reconciled. The water masks are merged by max.
- **Height layers** are summed with no clamp. Flatten assumes the layer's
  scale is 1, and samples the base from the last run's source output (no
  output: flatten sets the layer).
- **The heightmap range** is the source's ±16 m, widened ×4 when an edit
  leaves it. Widening moves every sample by a quantum once, and every
  downstream piece rebuilds.
- **The first stroke after the terrain stage itself reran** still rebuilds
  downstream pieces once. ground_edit's own pieces are lineage-free, but the
  terrain's other readers are not.
- **The editor panel and flatten were not tried in a real interactive
  window.** They are covered by headless tests only, as P2's and P3's editor
  features were. The live update with water was not watched in the 3D view;
  the pipeline is P2's.
- **Recipe rewiring** points terrain readers at the ground_edit only when the
  recipe adds it. A stage added later by hand that names the raw terrain
  does not see the ponds. The validator does not warn.

## Notes for W3 (rivers)

- **Channel carving** fits as another ground_edit input. The cleanest route
  is a list of river splines whose carve runs after the height layers and
  before or after the basins, writing into the same grid. Then the
  quantisation, mask rewrite, tile recook, supersede-all, rect keys and
  incremental keys all apply unchanged. `CarveBasins`' signed-distance and
  bank machinery (the EDT, the bank cut with fade) can be reused for a
  channel's cross-section, using distance to the spline instead of to an
  outline.
- **The water body path**:
  - a river's water is not flat, so `waterCells` / `waterTileMesh` do not
    fit;
  - the ribbon needs its own mesher, with vertex colours (W1's red = foam
    hook);
  - placement should still follow the `pond_<i>_<j>_<material>` pattern per
    tile, with keyed file names;
  - write it into the same `groundWater.water` WaterResult so the water mask
    (and so the wet rule, the reeds' shore band and grass max_depth) covers
    rivers too. `WaterResult.Level` is per sample, so a sloping surface fits.
- **Body keys**: a river spline is its own key. The `bodies` table can stay
  ponds-only.
- **Downstream correctness** relies on `Terrain.RectKey` covering everything
  an edit changes: heights and the water mask today. A flow-map texture or
  any new mask W3 writes must be hashed there too, or downstream pieces will
  be wrongly reused.
- The **recipe engine's `$source` / `$terrain` split and rewiring** are
  ready for river and stream presets.
