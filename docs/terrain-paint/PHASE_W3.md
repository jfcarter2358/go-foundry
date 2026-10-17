# Phase W3 — rivers

Built 2026-10-15. Spec: docs/terrain-paint/SPEC.md ("Rivers"); earlier
phases PHASE_W1.md, PHASE_P1–P3.md, PHASE_W2.md. The user-facing pages are
docs/guide/33-generators.md ("ground_edit", Rivers), 36-terrain-paint.md
("Rivers and streams") and 28-materials.md ("Water": `flow_map`).

## Decisions, and why

**Rivers live inside `ground_edit`, not in a stage of their own.** A river
is an edit of the finished ground — a channel cut, banks trimmed, water in
it — which is exactly what ground_edit is. Inside it a river is one more
input to the same grid, carved after the height layers and the basins (so it
sees the ponds it runs into), and everything W2 built applies unchanged: the
quantisation onto heightmap.png, the mask rewrite, recooking only the moved
tiles, supersede-all, rect keys, the incremental tile keys. A separate
`river` stage would be another terrain source redoing all of that, and a
river entering a pond would cross two stages' grids.

**One spec type, two sources.** Hand rivers (the `rivers` list: a
`ParamSpline` with `width` and `depth` channels plus shape fields) and
drainage rivers (`drainage` + `drainage_*`, traced by
`terrain.TraceDrainage`) are both `terrain.RiverSpec`s fed to one builder,
`terrain.CarveRivers`. Adopt copies a traced river into a hand river
(`from: adopted`); traced rivers give way to hand rivers.

**River params are RectCovered** (new `procgen.ParamDef.RectCovered`). They
are left out of the stage's BASE hash and kept in its full hash. Editing a
river reruns the stage, and:

- ground_edit's own pieces (tiles, pond and river meshes) are keyed by their
  content, so only the moved ones rebuild;
- downstream incremental pieces fold in the rect key, which now also covers
  the rewritten flow mask and the river masks, so only pieces where the
  ground or the masks moved rebuild.

To keep that correct:

- `Env.PaintKey` now folds rect keys in even when no paint is read anywhere
  (a river-only document);
- `Env.UpstreamPaintHash` folds in a hash of every ancestor's RectCovered
  params (`plannedStage.coveredUp`), so a material's paint-free weight cache
  is not reused after a river edit.

Neither changes any key of a document without RectCovered params (all W3
params are also `Added`). Tests:

- TestRiverEditMovesTheRectKey: the base hash is unchanged, the full hash
  changes, and the rect key changes over the river and not far from it;
- TestRiverEditRebuildsOnlyWhatItReached: incremental == full, some grass
  tiles reused and some not.

**The surface lives on the grid, not along the spline.** A swept ribbon cut
at tile edges would have to match its neighbour tile's cut, and another
river's ribbon at a confluence, vertex for vertex. Instead every river's
surface is merged on the terrain samples (`terrain.RiverSample`, each
sample owned by the river it is deepest inside). A cell is water when a
corner is wet and all four corners have a surface, plus one ring under the
banks, two triangles per cell. So:

- tiles meet on the same samples at the same heights (no cracks);
- a confluence is one sheet;
- cells with a corner in a pond's water are the pond's, and the pond's
  under-bank skirt gives way to the river's cells (`writeGroundWater` takes
  the river cells as an exclusion). So there is no dry gap and no doubled
  sheet where a river runs into a pond at its level.

TestRiverRibbonsMeetAcrossTiles checks shared edge vertices and heights.

**Flow reaches the shader per vertex. No sampler was added.** The forward
water pass draws through the world program, whose fragment stage is at its
32-sampler limit (gfx/water_draw.go; W1 already borrows blendTex, blendMask,
normalMap and tex). So the flow map is written twice:

- as two masks, `river_flow_x` / `river_flow_z` (0.5 + 0.5·v/4 m/s), next to
  `mask_water`, hashed in `Terrain.RectKey`, for any stage that wants it;
- baked into the ribbon's vertex colours: green/blue the current, red the
  foam (W1's foam hook), alpha 255 (fragment.glsl reads alpha as cavity).

The cook's colour weight keeps vertices where the flow changes. At 1 m cells
this is as fine as a 1 m flow texture would be. Nothing was added to the
Nanite material pass, and no unit 3–9 is claimed. One uniform was added,
`waterFlowMap` (strength, cycle), set from the new `flow_map` water param.

## What was built

### procgen/terrain (pure)

- **river.go**: types (`RiverSpec`, `RiverShape`, `RiverStation`, `River`,
  `Waterfall`, `Confluence`, `Boulder`, `RiverSample`, `RiverResult`) and
  `CarveRivers`.
- **river_path.go**: the path.
  - Catmull-Rom resampling at half a cell.
  - Meander only when asked (painted rivers default to 0, so they keep
    exactly the bends drawn).
  - Downhill auto-orientation: the mean ground under the first vs the last
    seventh; the spline is reversed if the start is lower.
  - Splitting at standing water: a run entering a pond ends at its level,
    the run leaving it starts at its level. Runs are named `<name>`,
    `<name>.2`.
- **river_hydro.go**: the surface and the river's character.
  - Confluences are solved main stems first; a cycle is broken.
  - The surface is a running minimum of ground − bank height, then
    min(z, box(z)) twice, so it is smooth and never rises. The mouth is
    fitted to the confluence or pond level (a ramp down, or a backwater
    hold).
  - **Waterfalls**: a concentrated stretch steeper than `max_slope` that
    drops at least `min_fall` becomes a step at the lip, with a plunge pool
    (0.35·drop + 0.4 m, at most 3 m). A long steep stretch (longer than
    three widths and twice its drop) is **rapids** instead: no step, foam ≥
    0.6. Holding the surface to the top of a long slope would cut a slot
    gorge the length of it.
  - Pools and riffles every `riffle_spacing` (default six widths).
  - On bends the thalweg moves toward the outer bank and a point bar forms
    inside.
  - Speed comes from a discharge held steady along the river (width ×
    depth smoothed over ten widths), so it is faster in narrows and riffles
    and slow in pools; × √(slope/median), clamped 0.1–3.8 m/s.
  - Foam: riffles, fast water, rapids, a fall's lip and pool.
  - Boulders on the riffle crests.
- **river_carve.go**: per-sample nearest point on each run (sparse maps).
  - Within the banks, a parabolic section about the offset thalweg. Outside,
    W2's bank cut measured from a line: a bank lip, a gentle floodplain,
    then `bank_slope`, faded at `reach`.
  - A levee where the side ground is lower than the water (never inside any
    channel).
  - A free end is a rounded head. An end into a pond or onto another river
    is OPEN: the channel runs straight on for 1.5 widths. A closed head left
    a dry sill one sample wide between river and pond (found in the shots).
    Samples under a pond's water stay the pond's (no surface, bed or bar
    from the river).
  - Ponds are untouched; the bed is clamped to the floor.
  - Fields per sample: owner, wet, surface (carried 2.5 cells past the
    banks), flow, foam, U/V, bed share, bar share.
  - Confluence foam, and boulder foam with a downstream wake.
- **river_drainage.go**:
  - `TraceDrainage`: priority flood, D8 area on the flow mask's scale,
    outlets largest first, main stem up the largest donor, tributaries
    ending on the junction sample. Douglas–Peucker at 4 cells, then one
    Chaikin pass. Width and depth from flow.
  - `TrimAgainst`: cuts a traced river where it enters a hand river's
    corridor (banks + floodplain + reach) and runs it on to the hand river's
    centreline, so it joins as a tributary. This catches traces running
    beside the river on the same valley floor.
- **river_meta.go**: terrain.yaml `rivers:` (rivers, falls, confluences),
  mask names/docs, `RiverFlowMax`.
- **edit.go**: `RectKey` folds in flow + the river masks when `Rivers` is set
  (unchanged otherwise).
- **meta.go**: one field.

### procgen/stages

- **ground_edit_river_params.go**: the params (`rivers`, `drainage*`,
  `river_rocks`) and why rivers live here.
- **ground_edit_river.go**: the specs (drainage traced on a copy with the
  hand rivers already cut, so it runs into their channels), the carve, the
  river masks, the flow mask raised to 0.9 × the channel share,
  `riversMeta`, rivers.yaml (`GroundRivers`, with every traced river's
  points for Adopt, `AdoptedRiver`), and boulders placed as the rocks
  stage's variants.
- **ground_edit_river_mesh.go**: the ribbons `river_<i>_<j>_<material>`
  (keyed file names; reused when the cells and corner values match).
- **ground_edit_run.go**: step 3b and step 9 (targeted).
- **ground_edit.go**: params appended, a validation line, docs.
- **terrain_material_paint.go**: a paint item may read a terrain `mask`
  (river_bed, river_bar, …) instead of a layer. terrain_material.go and
  region_material_run.go pass the terrain (variadic, so test calls are
  unchanged).
- **paint_preset.go / paint_preset_river.go**: river brushes.
  - A preset's `river:` block makes it a river brush.
  - `RiverItem` simplifies a stroke and sets width = 2·radius·width_scale.
  - `AddRiverStroke` applies the recipe when needed.
  - `NeedsRecipe`: missing layers, no ground_edit, or a missing dressing
    stage.
- **paint_preset_recipe.go**: material entries with `mask`; a ground_edit
  the document has takes the recipe's unset top-level params
  (river_rocks); `rivers` merges by name.
- **resources/paint/river.fpb, stream.fpb** (orders 175, 177).

### procgen core

- params.go: `RectCovered`.
- hash.go: `StageBaseHash`, `CoveredHash`.
- run.go, context.go, paint_incremental.go: covered hashes, the PaintKey
  gate.

### gencli

`foundry gen paint <gen> -preset river -line …`: the -line/-disc points are
the stroke.

### Shading (resources/shaders/water.glsl, gfx/water_draw.go, material)

- `flow_map` water param (material_kinds_water.go, material_resolve.go) →
  uniform `waterFlowMap`.
- `waterSurfaceNormal`: when flow-mapped, two-phase advection of two detail
  layers along the vertex flow (copies half a cycle apart, cross-faded,
  1.6 s cycle) over a slow static layer. Faster water is choppier.
- Foam: the foam pattern is advected the same way; the vertex red already
  adds to the foam mask.
- **Colour fixes (W2's complaints)**, in the shader:
  - roughness-aware fresnel (Lagarde): rough water is never a full mirror
    at grazing angles;
  - the sky reflection desaturated 40% before the tint;
  - the sky blur held back near the horizon;
  - the reflection direction uses the ripple slopes at 0.35 (the fresnel
    and the glint keep the full normal).

  The cause, found by shooting with reflectivity 0: the near-black body
  plus an HDR blue sky reflection that tone mapping keeps saturated. Halving
  the reflection barely changed the pixels; desaturating did.
- **Colour fixes**, in the presets:
  - water_lake: absorption 0.9 → 0.5, a brighter deep colour, a warm
    reflection tint, reflectivity 0.34;
  - water_swamp: a visible tea-brown body, an olive tint, rougher;
  - water_river: silt-green, flow_map 1;
  - lake.fpb: shelf 8 → 4 m, depth 7 → 4.5 m. The hard deep blob was the
    bed: a 4 m drop over about 4 m of run after an 8 m shelf, with
    absorption saturating by 3 m.

### Render

render/water_caustics.go: a cooked water placement whose box is more than
0.3 m tall (a sloping river ribbon) throws no caustics. As one box under
its highest point it lit every dry bank below that height. This showed in
the shots as a bright square over the pond's tile. Per-river caustics are
W4's.

### Editor (editor_generator_river.go; hooks in paint, paint3d, presets, view2d, water panel)

- **River brush**:
  - with a river preset, a drag in 2D or 3D records the centre path, drawn
    as a band;
  - mouse-up → `AddRiverStroke`, one document undo step, plus a marker on
    the brush stack so Ctrl+Z with the brush on undoes it;
  - the new river becomes the active spline, and the live rerun is kicked.
- **Width**: river splines draw their banks. Shift+wheel over a point scales
  its width, and the live rerun follows half a second after.
- **Rivers panel** under Water bodies: hand rivers (Edit, −/+, Delete,
  material) and drainage rivers (Adopt), with the run's lengths, drops and
  mouths, and the falls/confluences/boulders count.

## Tests

- terrain:
  - TestRiverSurfaceNeverRisesAndTheChannelHoldsIt
  - TestPaintedRiverIsOrientedDownhill
  - TestTributaryMeetsTheMainRiverSurface
  - TestSteepDropBecomesAWaterfall (step, plunge pool, lip foam)
  - TestLongSteepStretchIsRapidsNotAFall
  - TestRiverIntoAPondEndsAtItsLevel
  - TestDrainageTracesTheValleyFloors (main stem, a tributary ending on it,
    width grows, carved confluence, trimmed under a hand river)
- stages:
  - TestGroundEditCarvesARiver (rivers.yaml, ribbons, masks, flow map,
    artifact == Load for every river mask, the flow and the heights)
  - TestRiverRibbonsMeetAcrossTiles
  - TestRiverEditRebuildsOnlyWhatItReached (incremental == full, partial
    grass reuse)
  - TestRiverEditMovesTheRectKey
  - TestDrainageRiversAndAdopt
  - TestGroundEditWithoutRiversWritesNoRiverOutput (no rivers.yaml, masks,
    meta or placements; CoveredHash empty)
  - TestRiverBrushStrokeBecomesASpline (recipe slotting, hoisting, mask
    entries, second stroke, too-short stroke, stream on a pond document)
  - shipped presets list += river, stream
- editor: TestGeneratorRiverBrushAndPanel.
- Golden: TestTerrainStageGolden and every W2/P2 incremental test pass
  unchanged.

## Measured and seen

The scratch project is `$S/pproj` (`$S` = this session's scratchpad). The
generator is `w3_demo.fgen`.

**The scene:**

- a 256 m terrain with a canyon-profile valley;
- three height layers painted from the CLI:
  - `tilt`: 4.5 % falling east;
  - `falls`: a 3 m step in the valley floor at x = 42–46;
  - `cross`: the plateaus sloping into the valley;
- a Pond (preset) on the path;
- the River preset stroke down the valley (edited to a curvier 12-point
  path, a 4.5 m narrows before the step, boulders 2);
- the Stream preset down the north side;
- drainage on (threshold 0.62).

**Run:**

- ground_edit 1.2–1.3 s: 5 rivers (river_1 into the pond, river_1.2 out
  of it, stream_2, 2 traced), 2 waterfalls, 2 confluences, 19 boulders,
  about 4,100 samples moved;
- 4 ribbons, 3,283 cells.

**Shots** (`FOUNDRY_NANITE_VSM=0 foundry play run`, one shot per run, 2 of
14 runs hung and were retried). All were looked at:

- `$S/w3s2/bank.png`: the river winding in its channel, sand banks, reeds
  along both sides, foam lace along the edges, two boulders with foam
  rings mid-stream.
- `$S/w3s2/bridge_a.png` (looking downstream from above the water): the
  green-teal river narrowing toward the falls, foam at the lip, the valley
  walls either side.
- `$S/w3s2/falls.png`: the step with white water over the lip and the
  plunge pool below, boulders in the reach downstream.
- `$S/w3s4/riffle_a.png` (from above): clear shallow water over the bed,
  three boulders each in a foam ring with a wake.
- `$S/w3fin/above.png`: the whole system:
  - the river into and out of the pond (no dry sill after the open-end
    fix);
  - the stream joining from the south, its 8 m fall down the valley side;
  - the traced stream joining just above the falls.
- `$S/w3s9/pond.png`: the pond between the two river mouths, shallow and
  muddy with a darker middle.

**Flow:** `$S/w3s3/flow_a/b.png`, 0.5 s apart from a player standing on the
bank.

- The cliff region matches at shift (0, 0), so the camera is stable.
- The water region's best match is (+9, −2) px. With the camera at
  (30, 0, 13) facing (40, −7.5, 4), screen right/up is east and away:
  downstream. The ripples and foam move downstream about 9 px in 0.5 s.
- A pair taken from the air was useless: the player falls between shots.

**W2 scene re-shot** (`$S/w3w2*`, `$S/w3fin`):

- `lake_bank`: before, (108, 149, 216) saturated blue; after, (104, 133,
  188) grey-blue, with the far shore's colour readable.
- `top_lake`, `w2_overview`: the lake's deep middle now fades in gradually
  to a dark green. The marsh is grey-brown from above (it was pure blue).
- `marsh_low`: muted, no cartoon blue through the reeds.

**Colour debugging, for the record:**

- With reflectivity 0 the lake read (14, 31, 20): all the blue was the sky
  reflection.
- Lowering reflectivity and tinting changed the pixels by only a few
  percent: the HDR sky blue saturates through tone mapping.
- Desaturating the reflected sky 40% did the job.
- Flattening the reflection normal and holding back the horizon blur
  changed little, but are kept as reasonable.

## The pond the river runs through (follow-up fix)

In the first shots, the pond the river crosses read as a dry mud bowl
covered in reeds. **Cause: (b), not (a).** The pond's water was intact:

- TestRiverThroughAPondKeepsThePond shows the same level, area and depth
  with and without the river;
- the demo's water_bodies.yaml kept level −3.997 and 312 m².

What was wrong:

- **Shallow water, visually hidden.** The pond preset's default 0.4 m shelf
  covered most of the small pond. Clear water_lake over the dark mud bed
  showed only mud.
- **Reeds across the whole pond.** Reeds were allowed in water up to
  `max_depth: 0.4` (pond, lake) and 0.3 (river), so they grew over the
  whole shelf.
- **Doubled reed walls along rivers.** The river preset's `river_reeds` /
  `river_sedge` and the pond preset's `shore_*` rules both ringed every
  water edge.

**Fixes:**

- pond.fpb: `shelf_depth: 0.7, depth: 2.5`. Deepest is now 1.9 m.
- Reeds `max_depth: 0.15` in pond, lake and river.
- The river and stream presets reuse the names `shore_reeds` /
  `shore_sedge`. Recipes merge rules by name, so a shore is ringed once. In
  the demo, reed and sedge clumps fell from 74k to 20k.
- water_lake: absorption 0.75, a greener deep colour, roughness 0.15,
  reflectivity 0.3, so a 1–2 m pond shows a green body.
- **The outflow artifacts**, the small light rectangles at the right-hand
  mouth: the outflow started at the ground under the pond, 0.5 m BELOW the
  pond's level, so the pond's under-bank cells stood proud of the river.
  A run leaving standing water now starts AT its level and eases down over
  three widths (river_hydro.go). The demo's river_1.2 source is now −3.997
  = the pond's level. TestRiverIntoAPondEndsAtItsLevel now asserts that
  equality.

**Shots:**

- `$S/w3fix3/above.png`: the pond reads as dark-green water with a deeper
  middle, reeds only round its rim, the river in and out with no seam.
- `$S/w3fix3/pond_bank.png`: open water, grey-teal (53, 73, 78), reeds
  only on the bank, the outflow clean.

The pond is darker than the river from above because its bed is mud and
the river's is gravel.

## Gaps and notes for W4

- **Waterfall data is ready, visuals are not.** terrain.yaml `rivers:
  falls:` and rivers.yaml list each fall:
  - lip centre `at`, `lip` and `foot` surface heights, `dir` over the lip,
    `width`.

  The ribbon over a fall is just the steep cell strip between the lip and
  foot samples. W4 should:
  - build a curtain mesh from lip to foot along `dir` × `width`, wearing a
    falls material that uses the vertex flow;
  - put a mist emitter at the foot;
  - add extra foam in the plunge pool (the vertex foam is already high
    there).

  A fall on a steep valley side is carved as a notch from the lip to the
  foot (the surface drops at the lip); a long steep run is rapids instead.
  Cascades (several smaller steps) are not made.
- **Confluence foam**: the vertex foam already rises within a width of each
  mouth (`rivers: confluences:` lists main, tributary, at, level). W4 can add
  a turbulence/streak pattern there.
- **Flow map resolution** is the grid's (1 m in the demo). A 0.5 m grid gives
  0.5 m. Swirl behind boulders is not modelled, only the foam wake.
- **UVs jump at confluences** (each sample takes its owner river's
  across/along). The water shader samples in world space, so it does not
  show.
- **Boulders** are placed by ground_edit itself (so their foam is known when
  the ribbon is meshed); they need a `river_rocks` stage. They are not in
  any scatter's footprints, so a scatter does not avoid them.
- **Sand bars** are the `river_bar` mask plus a material entry and the
  shelving inside of each bend. drift does not read the mask.
- **Long thin painted rivers simplify to few points** (Douglas–Peucker at
  0.35 × radius): a nearly straight drag becomes a straight spline. Draw the
  bends you want, or add points in the 2D view.
- **Ripples tile visibly from high above** (W1's detail map). Not addressed.
- The editor's brush, panel and Shift+wheel were exercised headless only,
  like P2/P3/W2's editor features.
- **River ribbons throw no caustics** (see Render).
