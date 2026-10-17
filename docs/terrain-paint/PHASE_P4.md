# Phase P4 — paths

Built 2026-10-16. Spec: docs/terrain-paint/SPEC.md ("Paths", the
direction kind); earlier phases PHASE_W1, P1–P3, W2, W3. The user-facing
pages are docs/guide/36-terrain-paint.md ("Paths", "Direction", "The
bands") and docs/guide/33-generators.md (ground_edit "Paths", scatter and
grass_field `orient_to` and mask excludes).

## Decisions, and why

**Paths live in `ground_edit`, beside the rivers.** W3's reasons hold
unchanged: a path is an edit of the finished ground (a flattened bed,
masks), and inside ground_edit it reuses the quantisation, the mask
rewrite, the tile recook, supersede-all, rect keys and incremental tile
keys. It also has to see the water: laid after the rivers (step 3c), a
path knows every river's wet samples and channel share and every pond's
body, so it fords rather than flattening into the water. A sibling stage
would have had to read another stage's grid to know that.

**One spec type, five styles.** `terrain.PathSpec` (points, width per
point, a `PathShape`) and a style table (`terrain.PathStyles`: which
stones, the surface, wear and clear peaks, whether it fords). Boardwalk is
NOT built: Poly Haven has no plank, deck or boardwalk model (searched the
cached model index: only furniture, crates and ships). Everything else is
Poly Haven CC0.

**Paths "write the direction layer" without writing paint.** Paint is an
input (SPEC). Rather than a stage stamping into the author's direction
layer, ground_edit writes the paths' tangents as terrain masks
(`path_dir_x/z`, strength 1 on the path easing out over its wear band),
and a rule's `orient_to: <direction layer>` reads the layer WITH the paths
laid over it (the path wins in proportion to its strength). `orient_to:
paths` reads the paths alone; `orient_to: downhill` reads the slope.
`downhill` and `paths` are new **mask words** (`procgen.ParamDef.MaskWords`):
a mask param accepts them without a declared layer; they validate, hash no
paint and show in the editor's combo. Every mask without words is
unchanged.

**The path's effects are masks the existing consumers read.** No new
splat or band machinery:

- the splat is W3's material `paint` item with `mask: path_<style>`;
- wear and keep-clear are `exclude` entries on grass_field and scatter,
  which now may name a terrain `mask` instead of a layer, with a
  `strength` and (grass) a `shrink` (shorter clumps by shrink × value);
- `path_wear` is 1 on the centreline easing to 0 `wear` metres past the
  edge: one mask gives "no grass on the middle" and "thinner, shorter grass
  in a band either side"; the recipes also tint the band with dirt
  (a 0.35 override).

**Stones are ground_edit placements**, as W3's boulders are: laid out in
procgen/terrain (`PathStones`), seated by the stage on the final quantised
grid. Placements carry a per-axis scale, so ANY scanned rock is stretched to
its stone's footprint and thickness. Findings from the shots:

- domed scans sunk to "a few cm proud" show an outline much smaller than
  their footprint, so the joints gaped. Stones are now sunk by half their
  thickness and scaled 12% past their layout rectangle; the overlap test
  uses 85% of the rectangle (rocks are rounded);
- the moss boulders (`rock_moss_set_02`) read as black lumps as
  flagstones; `rock_09` (a flat stone, height/length 0.2) scaled to each
  place reads as a flagstone. So a path item has its own `rocks` override:
  the Rock path preset uses `rock_09` for flagstones, while the stage's
  `path_rocks` (moss set) supplies stepping stones and every ford.

## What was built

### procgen/terrain (pure)

- **path.go**: styles, mask names/docs, `PathShape` (defaults), specs,
  stations, `PathResult`, water lookup (pond body, river wet sample, river
  channel share > 0.05).
- **path_lay.go**: `LayPaths`.
  - Resampled on the river code's Catmull-Rom at half a cell.
  - Target: the centreline ground box-filtered over `smooth` metres, three
    passes, DRY stations only (a ford keeps the banks' levels); optional
    steps (treads `step_rise` apart above `max_grade`).
  - Per sample: exact distance to the station polyline (both neighbouring
    chords) and side.
  - Cross-section: surface 1 inside he = hw(1 + edge·fBm) with a soft ring;
    wear 1 inside 0.6 he easing out `wear` m past it; clear to he + 1.2 m;
    direction strength to the wear's end; flatten weight 1 inside hw (no
    noise, a regular bed) easing over `blend`, × a fade over the last 2 m
    before water. Nothing flattened in water or a channel; no surface, wear
    or clear under water. Masks take the max over paths; flatten takes the
    strongest path.
- **path_stones.go**: `PathStones` — flagstone rows (2–3 per row by
  width, a quarter of rows one fewer, a sixth one more, jittered, ±9°),
  stepping stones a stride apart zig-zagged ±12 cm, fords over every wet
  stretch ±1 m (tops `FordProud` = 7 cm above the surface, ×(1 + 0.6·depth)
  bigger, none deeper than `FordMaxDepth` = 0.75 m); oriented-rectangle
  overlap test, shrink twice then drop.
- **path_meta.go**: terrain.yaml `footpaths:` (`Meta.Paths` was already a
  canyon's centrelines).
- **edit.go**: `RectKey` folds the path masks in when `Footpaths` is set.

### procgen/stages

- **ground_edit_path_params.go**: `paths` (Added, RectCovered) and
  `path_rocks` (Added).
- **ground_edit_path.go**: specs, the lay, masks, meta, `paths.yaml`.
- **ground_edit_path_stones.go**: variants (flatter half for flags and
  steps), half turned end for end, ground plane from four edge midpoints,
  sunk, per-axis scale; flagstones collision none.
- **ground_edit_run.go / ground_edit.go**: step 3c, masks, files, step 10,
  validation, docs.
- **paint_orient.go**: `orient_to` / `orient_jitter` / `orient_offset` on
  scatter and grass rules; `orientField`.
- **paint_bands.go**: exclude `mask` / `strength` / `shrink`;
  `terrainMaskSampler`.
- **scatter_run.go, grass_field_run.go**: pass the terrain, orient (grass
  kinds long along X get a quarter turn).
- **paint_preset_path.go**: `path:` preset block, `PathItem`,
  `AddPathStroke`. paint_preset.go / paint_preset_river.go: `Preset.Path`;
  `NeedsRecipe` also when a recipe's mask material entry is missing (so the
  second path preset on a document still adds its splat entry).
- **paint_preset_recipe.go**: exclude entries with `mask`, `paths` merged
  by name, mask names in the "no free slot" note.

### procgen core and place

- params.go `MaskWords` / `IsMaskWord`; validate.go and paint_doc.go skip
  words.
- place: `Rule.Orient` (+ jitter, offset) used in `seat`, the scatter's long
  axis followed; `GrassRule.Orient`, `KindYaw`, `Shrink`; `OrientYaw`. No
  RNG draws added: positions are unchanged (tested).

### Presets (resources/paint)

`rock_path.fpb` and `dirt_trail.fpb` are now path brushes; new
`stepping_stones.fpb`, `gravel_path.fpb`, `game_trail.fpb`. Each recipe:
the style's texture (rock_path, dirt_floor, gravel_floor), a wear tint,
`{mask: path_wear, shrink: 0.6}` in every grass stage, `{mask: path_clear}`
in every scatter, a ground_edit with `path_rocks` (moss set), and for the
rock path `flag_rocks_src` (rock_09) as the item's `rocks`.

### gencli

`foundry gen paint -preset <path preset> -line …` (shares the river path).

### Editor (editor_generator_path.go; hooks in paint, paint3d, river, water, params)

- Path brush in 2D and 3D: the river brush's stroke recording, finished by
  `AddPathStroke`; one document undo step; the new path is the active
  spline (width channel banks and Shift+wheel come from W3).
- Paths panel under Rivers: style combo, Flatten, run column (length,
  fords, stones by kind), Edit, −/+, Delete, the run's note.
- Direction by hand: a direction layer's stamps paint the drag's direction;
  a click without motion paints nothing.
- The mask combo offers a param's words.

## Tests

- terrain: TestPathFlattenLevelsTheCrossSection, TestPathStepsOnASteepClimb,
  TestPathMasksHaveAWornEdgeAndAWearBand,
  TestFlagstonesFaceAlongThePathAndNeverOverlap,
  TestSteppingStonesAreAStrideApartAndZigZag,
  TestPathAcrossARiverFordsItWithoutTouchingTheChannel.
- place: TestOrientYawAlignsAndBlends, TestScatterOrientsLogsAlongTheField
  (no rock moves).
- procgen: TestMaskWordsNameNoLayer.
- stages: TestGroundEditLaysPaths (table, meta, stones flat/sunk, masks ==
  Load, no paint written), TestGroundEditWithoutPathsWritesNoPathOutput,
  TestPathEditRebuildsOnlyWhatItReached (incremental == full for grass on
  path_wear and for the ground_edit; base hash, full hash, rect keys),
  TestPathFordsARiverAndKeepsScatterOff (channel heights identical with and
  without the path, ford stones, no rubble on the path),
  TestPathPresetsSlotInAGroundEdit, TestOrientToFollowsPathsLayersAndTheSlope;
  TestShippedPresetsParse and TestRecipeSlotsShareAndRunOut updated (dirt
  trail is a path brush now).
- editor: TestGeneratorPathBrushAndPanel,
  TestGeneratorDirectionPaintFollowsTheDrag.
- TestTerrainStageGolden and every W2/W3/P2 incremental test unchanged.

## Measured and seen

Scratch project `$S/pproj`, generator `p4_demo.fgen` (`$S/p4/p4_base.fgen`,
painted by `$S/p4/paint.sh`, run by `$S/p4/run.sh`, shot by
`$S/p4/shots.sh`): a 192 m valley at 0.5 m with a shallow 3.5 m stream;
forest floor up the north side, meadow and wildflowers on the south; a
rock path from the stream bank up the north side, stepping stones across
the stream, a dirt trail and a game trail through the meadow; the forest
logs and twigs `orient_to: downhill`.

- ground_edit: 4 paths, 1,761 samples flattened, 1 crossing, 279 stones
  (rock path ~230 flagstones; stepping stones 39 + 5 ford), about 1.4 s
  cold, 0.13 s when only paths changed.

Shots (`FOUNDRY_NANITE_VSM=0 foundry play run`, one shot per run, 180 s
timeout, retried; 6 of 18 runs in the final set hung in the known GPU wait,
rock_close four times running). All were looked at; final set `$S/p4s4/`,
earlier ones `$S/p4s1`–`p4s3`:

- **rock_mid** (eye height, the bank): the flagstone path winding from the
  valley floor up the north side, bedded stones with irregular joints, a
  grey `rock_path` splat in the joints with a worn edge, a soft dirt-tinted
  wear band, grass thinning to it. Nothing floats or z-fights.
- **rock_slope** (on the climb): the path cut into the hillside as a sunken
  lane (the cross-section levelled on a 0.28 side slope), stones tilted
  with the ground, half buried.
- **rock_close** (`$S/p4s3`, before the row variation): flagstones fill the
  width. In `$S/p4s1`/`p4s2` (moss boulders, 4 cm proud) they were small
  dark lumps with wide gaps — fixed by the half-thickness bury, the 12%
  grow and the flat `rock_09` scan.
- **step_close / step_mid**: a slightly zig-zagged line a stride apart,
  carrying on across the stream as ford stones standing above the water;
  the banks untouched.
- **dirt_close / dirt_mid**: worn earth with a soft ragged edge, grass
  shorter and sparser beside it, the trail cutting up the valley side.
- **game_close / game_mid**: a faint narrow line of wear through the
  meadow, grass thinned and shortened, no stones.
- **above, above_rock, above_step**: the four paths from above; the ford
  stones across the stream; the rock path through the forest floor (litter,
  ferns and twigs kept off it by the wear).

The stream's water reads very white from above (W3's foam/ripple look, not
changed here).

## Suites at the end

- `go build ./...` passes.
- `go vet` passes on procgen, terrain, place, stages, gencli and editor.
- `go test ./internal/procgen/...` passes in every package (stages 223 s,
  rocks 31 s, terrain 20 s, gencli 16 s).
- `go test ./internal/editor/...` passes except TestScoreSibeliusKeymap and
  TestScoreRebinding (the score session's). TestYAMLFieldDocsUpToDate passes.
- `go test ./internal/render/...` (non-GL) and `./internal/` (layering) pass.
- No GL test was added: P4 touches no render, shader or material file.

## Gaps (for W4/W5 and later)

- **No bridges** (out of scope). Water deeper than 0.75 m gets no ford
  stones: the path just stops at the bank.
- **No boardwalk** (no Poly Haven asset).
- **Masks are on the terrain grid** (0.5–1 m): a 1 m game trail's worn edge
  is a few samples wide; a finer grid shows it better.
- **Flagstones are one stretched scan** (rock_09) turned and flipped; the
  texture stretches with the per-axis scale. A proper flagstone set would
  read better.
- **Stepping stones in grass**: grass is thinned by `path_wear` but not
  avoided per stone (stones are not scatter footprints), so a blade can
  poke through a stone's edge.
- **Steps** are treads on the grid; at 0.5–1 m cells they read as a
  terraced ramp, not crisp risers.
- **Splat slots**: three path textures plus a wear tint use covers; with
  ponds and rivers in a document the paths fall back to existing roles
  (the recipe notes say so).
- **orient_to on grass** assumes a piece's length lies along its Z (a
  quarter turn when X is clearly longer); `orient_offset` corrects others.
- The editor's path brush and panel were exercised headless only, like
  P2–W3's editor features.
