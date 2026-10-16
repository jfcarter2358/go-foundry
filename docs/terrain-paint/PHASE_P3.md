# Phase P3 — ground cover

Built 2026-10-15. Spec: docs/terrain-paint/SPEC.md ("Ground-cover drawing",
"Brush presets"); P1 and P2 are PHASE_P1.md and PHASE_P2.md. The
user-facing pages are docs/guide/36-terrain-paint.md ("Brush presets") and
the grass_field section of docs/guide/33-generators.md ("Ground cover").

## What was built

### Instancer options per clump kind

Every option is a `grass_field` rule field, `Added`, defaulting to what a
grass rule did before. A rule that sets none of them hashes, places, writes
and draws byte for byte as before (tests below).

| Field | Default | What |
| --- | --- | --- |
| `orient` | upright | `ground` lays each clump along the terrain's normal under it, at its random yaw (the roll) |
| `wind` | true | false: bend and flutter 0 in the vertices, `KindNoWind` |
| `tilt_jitter` | 0 | leans each clump by up to this many degrees, in a random direction |
| `sink` | 0 | pushes each clump down by this fraction of its height (straight down, so it never leaves its tile's box) |
| `card_textures`, `card` | "" | the rule is CARDS, cut out of a polyhaven_textures layer |
| `card_cut` | 0.55 | the local-height threshold of the cut-out |
| `card_tiles` | 2 | n × n atlas tiles, one card kind each, one texture (one draw group) |

`plants` now also accepts a `polyhaven_rocks` stage.

**Kit (.fgk)**: no format change. Three flags go in the kind's existing
flags word:

- `KindOriented` (2);
- `KindNoWind` (4);
- `KindCard` (8).

KitVersion stays 1. An old kit reads unchanged.

**Field (.fgrass)**: a new **version 2, the oriented field**.

- 16 bytes a clump: word 3 is the up axis, octahedral 12 + 12 bits, the top
  byte reserved (grassfield/orient.go).
- The code range is 0..4094, so level ground's axis (0, 1, 0) is exact.
- A field is oriented when its kit has an oriented kind, or any instance has
  an axis.
- A field without one is still version 1 at 12 bytes. It is pinned by a
  sha256 golden taken from HEAD's grassfield on the same instances.
- `DecodeField` reads both versions.

**Placement** (place/grass.go): `GrassRule.Ground` and `TiltJitter` fill
`GrassInstance.Up`.

- The axis is the terrain's normal (`Ground.Normal` over a cell), then a lean
  drawn from hash slots 8 and 9, which no older rule read.
- So positions, kinds, yaw, scale and tint are unchanged (tested).
- Upright rules keep a zero axis.

**Stage** (stages/grass_field_cover.go, grass_field_card.go, edits to
grass_field.go and grass_field_run.go):

- Kinds are keyed by piece plus a flag suffix (`#e`) only when the rule has
  options. A plain kit's kind names and order are unchanged.
- A **solid** piece (fewer than 6 connected parts, and only in a rule with
  options) decimates with `phimport.Decimate` for its LODs instead of being
  thinned by whole blades.
- Cards: a 9-vertex cupped quad per atlas tile (grassfield/card.go,
  `CardCup` 0.03), always oriented, still and non-casting.
- The card **cut-out** `textures/ph_<id>_card<cut>x<tiles>.png` is written
  only when its bytes differ. It is built as:
  - the albedo × the set's AO (a card is lit per vertex, with none of the
    splat's normal or AO maps);
  - alpha from a smoothstep of the **local height**: the displacement minus a
    twice-box-blurred copy (1/64 of the side), normalised to its own
    5–95 % range;
  - a noisy fade at each tile's edge.
- It is deliberately **not `_cov`**: coverage-kept mips turned distant cards
  into opaque smudges.
- The cut-out uses local height because thresholding the raw displacement
  (broad lumps) made each card one smooth blob. The shots showed both.

**Renderer** (render/grass_field.go):

- A kind's flags (less `KindCasts`) join its draw group and reach the vertex
  stage as `kindFlags`.
- Attribute 5 (`uint iUp`, binding 1, offset 12) is enabled only while an
  oriented field is bound, with stride `Field.Words()*4`. Otherwise its array
  is off and the stride is the old 12.
- Thinning, LOD tiers, alpha-to-coverage and shadows go through the same
  code. No world or material texture unit, FX bit or variant is touched.

**Shaders**: grassfield_common.glsl and grassfield_vert.glsl.

- `grassUnpackUp` and `grassUpRotate` mirror orient.go's `UnpackUp` and
  `UpRotate` line for line.
- A card is pulled toward the camera by 1 cm + 2 mm/m of distance (the depth
  bias), lit with the ground's normal, and gets no light-through.
- An oriented clump lights against its own axis.

### Kits (Poly Haven, CC0)

All are fetched by the existing polyhaven_plants, polyhaven_rocks and
polyhaven_textures stages, with credit, User-Agent and cache as before.

- **Models**:
  - `dry_branches_medium_01` (twigs and branches, 3 parts)
  - `fern_02`
  - `stone_01` (0.15 m quartz pebble)
  - `rock_moss_set_02` (7 mossy stones of 1.2–2.2 m, scaled to 6–40 cm)
  - `moss_01`
  - `dandelion_01`, `periwinkle_plant`
  - `shrub_sorrel_01` (as clover)
  - `shrub_04`
  - `grass_medium_01`
  - `dead_tree_trunk` and `tree_stump_01` (scatter, so they collide)
- **Textures**:
  - `forest_leaves_04` (litter splat and cards)
  - `gravel_floor`
  - `pebbles`
  - `brown_mud_02`
  - `dirt_floor`
  - `rock_path`
  - `rock_ground_02` (scree)
  - `snow_02`
  - `sand_02`
  - `burned_ground_01`
- **Not on Poly Haven**:
  - **pine cones**;
  - **mushrooms**;
  - **clover** (sorrel is the nearest);
  - **loose pine needles** as a model. Pine needles exist only as ground
    textures (`forrest_ground_03`, `forrest_sand_01`). They can be cut into
    cards with the same rule, but no preset ships them.
  - **A moss ground texture.** The moss preset is `moss_01` cushions alone.
- Nothing was generated as a substitute.

### Brush presets (`resources/paint/*.fpb`)

stages/paint_preset.go and paint_preset_recipe.go.

**Preset files.**

- YAML: `name`, `doc`, `order`, `layers: [{layer, strength}]`, `recipe`.
- A layer's strength is the **level** a stroke paints it to. Add-mode stamps
  become the new `paint.ModeRaise`: add up to the value, never past it, never
  lowering.
- So "twigs 0.6" ends at 0.6 however many stamps overlap.
- Erase and smooth act on every layer.

**Recipes**: layers, textures, material entries, exclude entries and stages
in .fgen syntax with `$terrain`, `$textures` and `$scatter`. `ApplyRecipe`:

- declares layers;
- adds textures to the material's own textures stage (`cover_tex` if
  there is none), reusing a layer of the same Poly Haven id;
- merges stages by ID (rules by name, assets by id);
- hoists any appended stage above its first reader;
- claims splat slots: a role already wearing the scan, else the first free
  cover (`wet`, `ash`, `snow`; a snow slot gets a snow line at 9–10 km),
  else the fallback role, with a note;
- adds exclude entries (`except: [cover]`).

It is idempotent (tested). The editor shows its notes in a popup worked out
on a clone, and applies them as one document undo step.

**Shipped presets** (16): forest floor, meadow, wildflowers, dense grass,
fern understorey, moss, gravel, pebbles, mud, dirt trail, rock path
(texture only), scree, snow patch, sand drift, ash and keep clear.

- Pond, marsh, river and stream are left out (W2/W3). The guide and
  keep_clear.fpb say so.
- `grass_clear` thins the document's own grass but not the `cover` stage.
- `path_clear` clears every grass stage; dirt trail and rock path also clear
  scatters.

**Editor**:

- A Preset combo beside the layer combo (editor_generator_paint_presets.go),
  rereading the folder when it opens.
- Strokes write every preset layer. `genPaintStep.more` holds the other
  layers, so one stroke is one brush undo step.
- The 3D brush shares it, as it shares all brush state.

**CLI**:

- `foundry gen paint <doc> -preset <id> [-no-recipe] ...` applies the recipe
  when layers are missing and prints its notes.
- `foundry gen paint -presets` lists the presets.

## Files

**New:**

- internal/grassfield/:
  - orient.go, card.go
  - orient_test.go
- internal/procgen/stages/:
  - grass_field_cover.go, grass_field_card.go
  - paint_preset.go, paint_preset_recipe.go
  - grass_field_cover_test.go, paint_preset_test.go
- internal/editor/:
  - editor_generator_paint_presets.go
  - editor_generator_paint_presets_test.go
- internal/render/grass_field_cover_gl_test.go
- resources/paint/*.fpb (16 files)
- this file

**Edited (targeted):**

- grassfield: grassfield.go (flags, versions, `Oriented`, `Words`), pack.go,
  codec.go, thin.go (`PieceCount`)
- procgen/place: grass.go, grass_test.go
- procgen/stages: grass_field.go, grass_field_run.go
- procgen/paint/stroke.go (`ModeRaise`)
- procgen/gencli: gencli_paint.go, gencli.go (usage)
- editor/editor_generator_paint.go (per-layer `before`, multi-layer steps,
  the combo)
- render: grass_field.go, grass_field_test.go
- resources/shaders: grassfield_common.glsl, grassfield_vert.glsl
- docs/guide: 33-generators.md, 36-terrain-paint.md

## Tests

**New:**

- grassfield:
  - TestUpPacksAndUnpacks (< 0.1° everywhere, the clamp, reserved bits)
  - TestUpRotateStandsAClumpOnItsAxis (orthonormal, right-handed, identity,
    slope)
  - **TestUprightFieldIsByteIdenticalToVersion1** (sha256 golden from HEAD)
  - TestOrientedFieldRoundTrips (version 2, axes, truncation)
  - TestCardMeshAndFlagsThroughTheKit
- place: TestGrassGroundOrientation (axes are normals, the lean is within
  the jitter, nothing else moves)
- stages:
  - TestGrassFieldGroundCover (still, sunk, tilted twigs plus 4 card kinds
    beside plain grass through a real run; flags, zero wind in every vertex,
    the cut-out written, oriented fields, axes = normals, sink depth, grass
    untouched)
  - TestGrassFieldWithoutCoverStaysVersion1
  - TestShippedPresetsParse
  - TestParsePresetRejectsBadFiles
  - TestForestFloorRecipeOnAFreshDoc (validates, slots, refs, idempotent,
    round-trips)
  - TestRecipeSlotsShareAndRunOut (shared scan, running out, snow line,
    excludes, hoisting)
  - TestRecipeOnARegionDocUsesTheFallbackRole
  - TestPresetStampsRaiseEachLayerToItsLevel
  - TestPresetPaintsAndRuns (recipe → stroke → run grows cards and twigs)
- editor: TestGeneratorPaintPresetStroke (asks, recipe as one undo step, one
  stroke for both layers at their levels, one brush undo, no-preset)
- render:
  - TestGrassShaderKindFlagsMatchGo
  - GL: TestGrassFieldDrawsGroundCover. It draws an oriented card field
    beside an upright one on both frame paths with no GL error. Oriented on
    level ground draws the same as upright. Tilted axes change the picture,
    and still frames agree. It **passes**.

**Suites at the end:**

- `go build ./...` passes.
- `go vet` passes on grassfield, place, paint, stages, gencli, editor and
  render.
- `go test ./internal/procgen/... ./internal/grassfield/...` passes in every
  package (stages 179 s). TestTerrainStageGolden and the P2
  incremental == full tests are unchanged.
- GL: `FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'Grass'` passes
  (TestGrassFieldDraws and the new test).
- `go test ./internal/editor/...` passes except TestScoreSibeliusKeymap and
  TestScoreRebinding (the score session's).
- `go test ./internal/render/...` (non-GL), `./internal/pack/` and
  `./internal/` (layering) pass.

## Measured

Scratch project `$S/pproj` (`$S` = the session scratchpad). `p3_demo.fgen`
is a 256 m terrain in 128 m tiles with a sparse sward, painted from the CLI:

- forest floor at (−60, 0) r 28;
- meadow r 22 with wildflowers r 13 at the origin;
- pebbles at (55, 10) r 14;
- gravel at (55, −25) r 12.

The full run is 24 s, with the downloads cached by the first run. The cover
stage is 20–23 k clumps.

**Shots** (`FOUNDRY_NANITE_VSM=0 foundry play run`; final set in `$S/p3f/`,
earlier iterations in `$S/p3s1`…`p3s9`, all looked at):

- **Forest floor, close (1.7 m eye, 40° and 75° down)**: dense layered leaf
  litter, the cut-out leaves over the same scan as a splat. Twigs lie flat
  and bedded in, do not float and do not move between frames. Ferns stand
  among them. No z-fighting.
- **Forest floor, mid (15 m)**: an even litter floor, the odd log.
- **Meadow**: knee-high grass with dandelions and periwinkle that read at mid distance (re-shot in `$S/p3g/`).
- **Pebbles and gravel**: stones lie flat and half buried, at 7–40 cm, with
  no floaters.

How the cards got there:

- **Iteration 1** used `_cov` mips, raw displacement and cards at 1/3 of life
  size. It read as a "crazy paving" of smudges.
- **Fixes**: life-size scale, plain mips, AO, the local-height cut.
- **rock_moss_set_02** stones were 1–2 m boulders at first, and are scaled
  down now.

**Grass cost, dense forest floor** (`FOUNDRY_BENCH`, 1600×900, TAA, VSM on,
the yaml level):

- `p3_bench` is the forest floor painted over the whole 256 m world: 200,731
  cover clumps (142,905 cards, 38,994 twigs, 18,832 ferns) plus the 38,947
  sward.
- It is compared with the same level with the cover volumes removed.

| | grass CPU row | drawn | triangles | frame p50 |
| --- | --- | --- | --- | --- |
| with cover, pose 1 | 0.30 ms | 14,356 inst | 1.81 M | 27.4 ms |
| with cover, pose 2 | 0.32 ms | 15,250 inst | 2.07 M | 26.6 ms |
| without, pose 1 | 0.17 ms | 3,931 inst | 0.30 M | 21.9 ms |
| without, pose 2 | 0.12 ms | 4,164 inst | 0.40 M | 21.9 ms |

- About +5 ms p50 frame, almost all GPU vertex load (1.5–1.7 M more
  triangles), after cutting the twigs to 300 and the ferns to 600 triangles.
- GPU timer columns read 0 on this box (WSL), so the GPU share is inferred
  from the p50 frame.
- The GPU was shared with another session's GL tests, which explains the
  p99 spikes.

## Known gaps (carried forward)

- **Splat slots run out.** terrain_material has 7 slots, so after about 3
  texture presets a document's new textures fall back to an existing role.
  The popup and CLI say so. More splat layers are a material-system change.
- **region_material**: a recipe adds only the fallback-role override, and
  textures are not added to the region atlas.
- **Recipe excludes apply to the stages that exist when applied.** A
  keep_clear applied before `cover` exists does not reach `cover`.
- **Decimation does not always reach max_triangles.** `dry_branches_medium_01_a`
  stays at 823 triangles with one LOD, a slot or seam limit of the
  simplifier. Ferns that are one piece keep their LOD 0 (the old thinning
  rule).
- **Kind sizes are scan-dependent.** The presets' scales were tuned by eye on
  the shots: flowers under a tall meadow are small, `stone_01` is one quartz
  pebble repeated (tinted down), and `rock_moss_set_02` is used at 5–15 %
  of scan size.
- **Card lighting is per vertex and flat.** The AO baked into the cut-out
  stands in for the splat's normal map. A card never casts.
- **`foundry play` GPU hangs: investigated, not P3.**
  - **Audit.** Every 12-byte assumption is per field now:
    - the VBO size is `len(Data)*4`;
    - the instance stride is `Words()*4`, attribute 5 sits at offset 12 and
      is enabled only for oriented fields;
    - the counts come from runs that `DecodeField` checks;
    - there is no TBO or SSBO;
    - grass_field_reload.go only releases fields;
    - the shadow pass shares `drawBatches`;
    - mixed v1 and v2 fields use separate kit VAOs;
    - card indices are bounded by `EncodeKit`;
    - the indirect buffer is re-specified at this frame's size, not grown.
  - **Reproduced on an idle GPU** (no other foundry or test processes, the
    GPU 0–7 % busy before each run), p3_demo shot script, 8 runs each:

    | Configuration | Hung |
    | --- | --- |
    | full | 3/8 |
    | no card rule | 4/8 |
    | cover volumes removed | 4/8 |
    | **no grass volumes at all** | 3/8 |
    | full with `-gi off` | 3/8 |

  - In a no-grass hang the main goroutine is blocked in `glBufferData` in
    gi_sdf.go `uploadSSBO`. Earlier hangs blocked in the grass indirect
    upload. The stall lands on whichever call next needs the GPU.
  - It is an engine- or driver-level hang (WSL GPU), present without any
    grass instancer content. It needs its own investigation. A shot run
    needs a retry on this box.
- **Meadow flowers** at 10–20 m: the meadow grass is now knee-high
  (scale 2–3), periwinkle 0.9/m² at scale 2–3, and dandelions 0.8/m² at
  scale 3–4.5 with `range` 0.8. Wildflowers have `range` 0.8 and dandelions
  at scale 2–3. The re-shot `$S/p3g/meadow_mid.png` shows yellow and pink
  flowers through the patch.
- **Not verified in an interactive editor**: the Preset combo and the recipe
  popup are covered only by the headless test.
