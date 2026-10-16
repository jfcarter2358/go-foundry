# Phase W4 — waterfalls, confluences, caustics polish, crest subsurface

Built 2026-10-16. Spec: docs/terrain-paint/SPEC.md ("Rivers", "Water >
Look"); earlier phases PHASE_W1, P1–P3, W2, W3, P4. The user-facing pages
are docs/guide/28-materials.md ("Water": `falls`, the caustics, the wet
bed), 33-generators.md (ground_edit: waterfall sheets, mist, cascades,
obstacles) and 36-terrain-paint.md ("Rivers and streams").

## What was built, item by item

### 1. Streams read white from above — fixed

Cause: **shore foam saturation**, not reflection or tone mapping. The shore
foam band is `0.25 + 0.75·foam` metres of water depth; `water_river` has
`foam: 1`, so the band was 1 m — deeper than a whole 0.45 m stream. Every
pixel of a stream was inside it, bank to bank, and the lace threshold falls
toward the shore, so the stream drew as a white strip from above (P4's
`above.png`) and as a flat white line from the side (`step_mid.png`, where
the grazing sky reflection added to it). Fixes (resources/shaders/water.glsl):

- On a flow-mapped surface the shore band is a third of a lake's
  (`WATER_RIVER_SHORE`). A river's foam comes from its vertex foam
  (riffles, boulders, falls, confluences), which W3 already bakes.
- **Wet bed**: the refracted bed is darkened by `WATER_WET` (0.72), easing in
  over the shoreline fade. A clear shallow stream over pale gravel read as a
  pale strip even without the foam.
- White water (the vertex foam) is rougher (roughness + 0.3·foam on flow-
  mapped surfaces) and choppier; the sky reflection is desaturated more on
  rough water (0.6 → 0.3 of its saturation as roughness goes 0.12 → 0.5). A
  churned plunge pool read as patches of saturated zenith blue.

Seen: `$S/w4s3/p4_above.png` — the P4 stream is grey-green rippled water
showing its bed, foam only as a thin line at the banks;
`$S/w4s3/p4_step_mid.png` — from eye height a blue-grey reflecting strip,
no longer white. W3's river shots re-taken (`$S/w4s2/bed.png`,
`confluence.png`): clear green water over the gravel bed, foam at the banks,
rings round boulders.

### 2. Waterfalls

- **Sheet geometry** (procgen/terrain/river_falls.go, pure):
  `Waterfall.Curtain(cell)` — a short level lead-in over the lip (1.5 cells
  upstream, so it meets the ribbon with no gap), then a **ballistic**
  profile `along = vx·t, y = lip − ½gt²` down to 0.25 m below the pool. `vx`
  is the river's speed over the lip (new `Waterfall.Speed`, 0.6–4 m/s); the
  landing distance is held between 0.12 and 0.5 of the drop (+0.1/+0.4 m).
  Rows evenly spaced in height (5–28), columns every ~0.6 m; width 0.94 of
  the river's at the lip, spreading up to 25% by the foot. Per vertex: U
  across, V metres of arc down the sheet, aeration (0.2 on the lead-in,
  0.3 + 0.7√progress, taller falls white sooner), edge fray, progress.
- **Placement** (procgen/stages/ground_edit_falls.go): `falls_<river>_<n>`
  cooked .fnm (keyed by its vertices: reused when unchanged), positioned at
  (lip x, foot, lip z), collision none, not solid, wearing `falls_material`
  (default `water_falls`). Vertex colour R aeration, G edge, B progress, A
  255 (fragment.glsl reads A as cavity).
- **Shader** (water.glsl `waterFallsShade`, behind `waterFlowMap.z`, the
  new `falls` param; `waterFlowMap.w` = `falls_speed`; packed into the W3
  uniform's spare lanes, so **no new uniform, sampler, FX bit or variant
  bit**): two layers of the detail map's noise squeezed across and drawn
  down V, scrolling at falls_speed (the second 1.4× faster); white where
  aerated, a boil band at the foot; glassy parts show the scene colour copy
  behind them, bent by the streaks and tinted by the river's absorption,
  with the sky by fresnel; frayed sides. Blended fallback without the
  colour copy. The sheet's normal is its vertex normal leaned toward the sky
  (aerated water scatters light from above whichever way it faces).
- **Material** `resources/materials/water_falls.fmat`: falls 1, speed 4,
  no swell, no flow map, no caustics/SSR, the river's colours.
- **Mist**: `resources/particles/waterfall_mist.fpar` (a prewarmed lit spray
  cloud billowing up + coarse droplets) with a generated soft puff texture
  `resources/textures/particle_mist.png` (64², 3 KB — the engine had no
  soft particle texture; `step_sand.fpar` names a `particle_puff` that does
  not exist in the repo). Placed by ground_edit as `mist_<river>_<n>_<k>`,
  one per 3 m of sheet across the foot, 0.15 m above the pool, rate scaled
  by the drop (0.6× on a cascade step). **New plumbing** (none existed for a
  stage to place an emitter): `procgen.Emitter`, `RunContext.PlaceEmitter`,
  `StageResult.Emitters` / `Result.Emitters()` (prefixed), the
  placements.yaml `emitters:` list, and `genlevel` splicing them under the
  generator's prefix (`Options.Emitters`; `SpliceResult` fills it; the
  editor's file splice and `foundry gen run` pass it). Stats print
  `emitters -n +m`.
- **Foam rings** in the plunge pool (`fallsFoam`): before the river masks
  and the ribbons read the foam, samples round the sheet's landing LINE get
  foam 1.3·(1 − smoothstep(0.1, 1, d/R))·rings, R = half the foot width +
  0.6·drop (0.5–4 m). The shader closes the lace into a churned white
  sheet where the vertex foam is near full (`boil`).
- **Notch** (terrain `notchFalls`, inside `carveRuns`): every sample under
  the sheet's footprint — just past the lip to a cell past the landing,
  across its width plus a cell — is lowered to ≥ 0.2 m below the sheet over
  it (lowered only, floor-clamped, ponds untouched). The fall drops through
  a notch whose sides stand clear of the sheet.
- **Cascades** (`RiverShape.CascadeStep`, list field `cascade_step`, Added,
  default 0): a W3 "rapids" stretch becomes a staircase of k falls about
  that high (k ≥ 2, each ≥ MinFall), the surface level from just past each
  lip to the next, a small pool under each; every step is a Waterfall
  (`Cascade`) with its own sheet, notch and mist. The Stream preset now sets
  1.2 m. Default 0 keeps W3's rapids (and its test).

The W3 ribbon over a fall (the cells pitched from lip to foot) is kept: it
sits behind the arcing sheet (the notch face), so removing it would only
open a gap between the sheet's landing and the next sample row.

### 3. Confluence turbulence

terrain/river_carve.go: the round splash at a tributary's mouth is weaker
(0.65 → 0.45; it read as a white disc), and a **mixing line** trails down the
main river from the mouth along the main's heading for `confluenceWake` (5)
of the wider river's widths (`mixingLine`: half the tributary's width
across at the mouth, widening 0.15 m per metre, foam 0.7 fading to 0). A
first try reused `splash` with a wake; its round head drew a white disc at
the mouth in the shots, so the line has no head. Choppier normal: water.glsl multiplies a flow-mapped
surface's ripple strength by (1 + 1.1·vertex foam) and its roughness by
+0.3·foam, so every white-water place — confluence, riffle, boulder, pool —
churns.

### 4. Caustics for rivers, and the pond-box fault — fixed by moving them

The caustics are now drawn by the **water surface onto the bed it shows**
(water.glsl `waterBedCaustics`, frame flag `waterHaveCaustics` = 16 at the
high tier). The bed's position comes back out of the depth copy along the
view ray (the refracted sample's depth when refraction took one), its facing
from that position's screen derivatives; the refracted bed colour is
multiplied by the procedural network, faded with the depth under THIS
surface and its absorption, carried down a river's current (two-phase, as
the ripples) and scaled by the sun's visibility at the surface. So:

- rivers at any slope get caustics (W3 had dropped sloping ribbons);
- dry ground — including a hollow below the water level inside a pond
  tile's box (W1's fault) — never does: no water surface covers it;
- every bed kind takes them (block terrain, biome ground, grass, scatter),
  which the bed-side path never reached.

The bed-side path is **retired**: render/water_caustics.go now keeps the
body count at 0 (fragment.glsl's test stays one uniform compare; the
visibility buffer's material variants fold it out through
`visKeyNoCaustics`), so no shared file was edited. Lost: caustics seen
from under the surface (W5's underwater view).

**A pattern bug found on the way**: `waterCausticPattern` (the classic
self-warping construction) scales with |p|; fed world-scaled coordinates it
saturated to 1 almost everywhere (mean 0.89 over a 60 m square, measured
numerically) and blew up near the origin. It now wraps p into one tile
about the construction's working point (`mod(p, 2π) − 250`, mean 0.11, 1%
above 0.5 — thin bright lines), and the factor is clamped to 2.5. Seen in
the GL dump `w4_caustics_high.png`: a proper caustic network on the floor
under the water, none beside it.

### 5. The ghost rectangle — root cause: not a bug

W1's "lighter rectangles beside rocks" are **two submerged rocks**. The W1
test level (`$S/proj/resources/levels/water_test.yaml`) has five rock
brushes; `rock2` (x 7.5…12.5, top −1.2 m) and `rock3` (x −14…−10, top −1.6
m) stand entirely under the water (surface y = 0). From the shore they are
exactly "the rock's underwater footprint, offset sideways": the boxes beside
rock1 and rock0 on screen. A debug shader drawing the depth copy's water
depth (`$S/w4dbg/depthviz.png`) shows them as the same shallow teal as the
shore band and the columns under the visible rocks, and the colour and
depth copies line up with the frame. Refraction, SSR and the caustics box
were all innocent; the water was correctly showing shallower water over
the rocks' tops. Nothing to fix.

**Crest subsurface polish** (water.glsl): the back-lit crest now takes a
tint brighter than the deep colour — scatter × 1.5 + what ~0.5 m of the
water transmits (`shallow^(2·absorption)` × 0.06), so clear water's crests
glow green-turquoise and a swamp's barely — and is weighted toward the wave
faces turned away from the sun (toward the eye), 0.35 + 0.65·face.

### 6. SSR via Hi-Z — not done (measured instead)

The engine's Hi-Z (render/hiz.go) is a **furthest**-depth pyramid built for
occlusion culling at points of the frame owned by the foliage and Nanite
culls. A reflection march can only skip space with a **nearest**-depth
pyramid, and reading any pyramid from the water draw needs a sampler — the
world program is at 32 and the task forbids adding one. Building a
min-pyramid into the water depth copy's own mips (readable through the
already-borrowed `blendMask`) is possible but is a new reduction pass and a
hierarchical march; it did not fit beside items 1–5. Measured cost of the
current 16 + 4 step march: see "Measured".

### 7. Boulders and stones in scatter footprints

ground_edit lists every rock it placed — river boulders (radius = half the
size) and path stones (radius = a quarter of length + width) — in
terrain.yaml `obstacles: [[x, z, r], …]` (`terrain.Meta.Obstacles`; absent
when none). Every `scatter` and `grass_field` whose terrain is that
ground_edit adds them to its avoid list (stages/ground_obstacles.go). Tiled
scatter tile keys and grass tile keys fold in the obstacles reaching the
piece (`obstacleKey`, "" — key unchanged — when none do), so a moved
boulder rebuilds the pieces round it and documents without obstacles keep
every key.

### 8. Headless hang — see "Hang investigation" below.

### 9. Water colour across presets — see "Measured and seen".

## Files

New:

- internal/procgen/terrain/river_falls.go, river_falls_test.go
- internal/procgen/stages/ground_edit_falls.go, ground_edit_falls_test.go,
  ground_obstacles.go
- internal/procgen/genlevel/splice_emitters_test.go
- internal/particle/particle_waterfall_test.go
- internal/render/water_w4_gl_test.go
- resources/materials/water_falls.fmat, resources/particles/waterfall_mist.fpar,
  resources/textures/particle_mist.png

Edited (targeted):

- procgen core: placement.go (Emitter), context.go (PlaceEmitter), run.go
  (StageResult.Emitters, Result.Emitters)
- genlevel/splice.go (emitters), gencli/gencli.go and
  editor/editor_generator_splice.go (pass them)
- terrain: river.go (Waterfall.Speed/Cascade, RiverShape.CascadeStep,
  speeds), river_hydro.go (cascade hook), river_carve.go (notch, confluence
  wake), meta.go (Obstacles)
- stages: ground_edit_run.go (fallsFoam, writeGroundFalls, obstacles),
  ground_edit_river_params.go (cascade_step, falls_material, falls_mist),
  ground_edit_river.go / ground_edit_path*.go (obstacle discs),
  scatter_run.go, scatter_incremental.go, grass_field_run.go,
  grass_field_incremental.go (obstacles)
- material: material_kinds_water.go, material_resolve.go (falls,
  falls_speed), material_water_test.go
- gfx/water_draw.go (waterFlowMap.zw)
- render: water_pass.go (caustics flag), water_caustics.go (retired list),
  water_gl_test.go (flags)
- resources/shaders/water.glsl; resources/paint/stream.fpb (cascade_step)
- docs: guide 28, 33, 36; this file

Not touched: fragment.glsl, vertex.glsl, the Nanite material pass, texture
units, FX bits, variant bits.

## Tests

All pass:

- procgen/terrain: TestFallsCurtainFollowsTheLipAndABallisticProfile (lip
  width, lead-in, every row exactly ballistic, landing range, spread,
  aeration, fray, faster river throws further),
  TestFallsNotchKeepsTheGroundUnderTheSheet,
  TestCascadeTurnsALongSteepStretchIntoSteps (≥ 4 steps on a 16 m slope at
  1.5 m, each ≥ MinFall, level just past each lip, monotone),
  TestConfluenceFoamTrailsDownTheMainRiver (8 m down foamy, 8 m up not,
  fading by 18 m); every W3 river test unchanged (rapids test still rapids:
  cascades default off).
- procgen/stages: TestGroundEditPlacesWaterfallSheetsAndMist (sheet per
  fall, material/collision, mist effect/height/position, prefixed in the
  result, pool foam at the landing), TestFallsMeshCarriesTheSheetData,
  TestFallsEditRebuildsToTheFullRun (incremental == full for the
  ground_edit's files with falls), TestGroundEditWithoutFallsPlacesNoSheet,
  TestScatterKeepsOffTheRiverBoulders (obstacles listed, no rubble inside a
  boulder). TestTerrainStageGolden, every W2/W3/P2/P4 incremental and
  rect-key test pass unchanged.
- procgen/genlevel: TestSpliceReplacesTheGeneratorsEmitters.
- material: TestWaterFallsPresetIsAFallingSheet (+ the W1 preset tests).
- particle: TestShippedWaterfallMistLoadsAndRuns.
- GL (`FOUNDRY_GL_TEST=1`, WSLg d3d12): TestWaterCausticsOnlyUnderTheSurface
  (dry floor 1.2 m under the level beside the pool pixel-identical to the
  dry level at two moments; the floor under the water changes high vs
  medium), TestWaterFallsSheetShadesAsFallingWater (sheet 0.54 vs wall 0.36
  luminance; streaks move), TestWaterPassDrawsDepthColourAtEveryTier (high
  flags now include caustics), TestWaterPassCost, TestWaterSSRCost; the
  whole gfx GL suite.
- Suites: `go build ./...`; `go vet` on procgen/..., render, gfx,
  material, particle, editor; `go test` ./internal/procgen/... ,
  ./internal/render/... ./internal/gfx/... ./internal/material/...
  ./internal/particle/... ./internal/world/... ./internal/ (non-GL) pass;
  ./internal/editor/... fails only TestScoreSibeliusKeymap and
  TestScoreRebinding (the score session's); TestYAMLFieldDocsUpToDate passes.

## Measured and seen

Scratch project `$S/pproj` (`$S` = the session scratchpad), generator
`w3_demo.fgen` (W3's valley; stream_2 given `cascade_step` to make
ground_edit rerun): 5 rivers, **2 waterfalls** (river_1.2: 2.2 m, 4.7 m
wide; stream_2: 7.6 m down the valley side), **2 confluences**, 19
boulders; ground_edit 1.0 s, "waterfalls: 2 sheets, 3 mist emitters";
splice "emitters -0 +3". Tools in `$S/w4tools` (shoot.sh, w3shots.sh,
hang.sh). Shots, all looked at (`$S/w4fin`, earlier `$S/w4s1`–`w4s3`):

- `falls_pool.png` — from the plunge pool: a white sheet across the notch,
  vertical streaks with glassy gaps showing the rock face behind, frayed
  sides, mist haze at the foot, a churned white boil where it lands
  fading to lace across the pool. (`w4s1/falls_pool.png` was the first
  try: a lacy translucent web — the foam network stretched; switched to
  the noise channel.)
- `falls_side.png` — from the bank: the sheet arcing out over the pool
  from the lip, the notch walls standing clear of it either side, mist.
- `w4s2/stream_fall.png` — stream_2's 7.6 m fall from below: a tall white
  ribbon between dark notch walls.
- `confluence3.png` — the stream joining river_1: a lace trail running
  downstream from the mouth (`confluence.png`/`confluence2.png`: the white
  disc the first two tries made).
- `w4s2/bed.png` — clear green river over its gravel bed, foam rings round
  boulders, caustic light on the shallow bed.
- `w4s3/p4_above.png`, `p4_step_mid.png` — the P4 stream (item 1).
- `w4s3/fromshore.png` — W1's rock view: the "rectangles" are the two
  submerged rocks (`$S/w4dbg/depthviz.png` proves it).
- Item 9, `montage.png`: water, lake, swamp, sea, river, alpine, tropical
  (W1's per-preset shore levels) and W2's lake/pond. Every preset distinct:
  default blue-teal, lake green, swamp tea-brown with a sun path, sea
  ocean blue with shore foam, river silt-green, alpine glass-clear with
  caustics on the sand, tropical turquoise. None flat white; none
  cartoon blue except the default and alpine leaning blue at a grazing
  look, which is their intended clear water. Note: every W1 scene now reads
  bluer than in W1's own shots because lit surfaces are darker (the grass
  and sand too) since the lighting session's changes; the sky reflection
  did not change with them.

**Cost** (wall clock with glFinish on WSL d3d12, 1440², noisy as W1 said):
TestWaterPassCost low 3.9, medium 2.5, high 3.1 ms (W1: 1.9–2.5 / 0.6–1.4
/ 1.8–3.0 — the run was slower overall: the frame without water was 4.0
ms). **SSR**: TestWaterSSRCost high tier with `ssr` 1 vs 0 on the same
pool: 6.44 ms vs 7.37 / 5.03 ms → SSR ≈ 0.2 ms, inside the noise. The
march is cheap; a Hi-Z version would buy reach, not time.

## Hang investigation (time-boxed, ~1.5 h)

Method: `$S/w4tools/hang.sh` runs one fixed headless shot n times, 150 s
each, with SIGQUIT on timeout so Go prints every goroutine (the GL thread is
goroutine 1). Results:

| run | view | VSM | GI | hangs |
| --- | --- | --- | --- | --- |
| default | p4_demo from above | on | default | 0 / 8 |
| vsm0 | p4_demo from above | off | default | 1 / 8 |
| sfall_gi | w3_demo stream fall | off | default | 0 / 6 |
| sfall_gioff | w3_demo stream fall | off | **off** | 2 / 6 |

plus during the shot batches 3 hangs in ~35 runs (VSM off). Where goroutine
1 was blocked:

- `gfx.(*SceneBuffer).Flush` → `glBufferSubData(GL_UNIFORM_BUFFER, 0,
  0x14f0)` from `render.UploadShadowUniformsTo` at the top of DrawWorld
  (vsm0 run 7) and from `UploadPointShadowUniformsTo` in
  `naniteVisPass.frameUniforms` (sfall_gioff run 4);
- `render.(*grassFieldRenderer).upload` → `glBufferData` (sfall_gioff
  run 1).

Findings:

- **Not the VSM** (hangs with FOUNDRY_NANITE_VSM=0), **not live GI** (hangs
  with `-gi off`). Water was not switched off in these runs (both views
  have water in them); W1 and the task note hangs with no grass and in
  views without new water features, so it is unlikely to be ours.
- The blocked call is always a **buffer update of a buffer the GPU may still
  be reading** (the scene UBO, the grass instance buffer). On Mesa's D3D12
  translation such an update waits for the GPU; the GPU never finishes the
  previous frame's work, so the CPU waits forever. The blocked call is the
  victim, not the cause: whatever the GPU is stuck on was submitted earlier
  in that frame or the one before.
- Rate is view dependent (0/8 to 2/6), so 6–8 runs per configuration cannot
  rank smaller factors; narrowing further needs per-pass `glFinish` +
  timestamped logging to find the last pass that completes.

**For foundry-42 (lighting/GI owns scene_block.go):** the hottest victim is
`SceneBuffer.Flush`'s `glBufferSubData` on the scene UBO, called several
times a frame (shadow uniforms, point-shadow uniforms, the material pass's
frame uniforms). Two cheap experiments worth trying there: orphan before
writing (`glBufferData(..., nil, GL_STREAM_DRAW)` then SubData) or a
ring of 2–3 UBOs bound per frame, so the CPU never writes a buffer the GPU
may still read; and a `FOUNDRY_*_FINISH`-style `glFinish` after the passes
suspected (as VSM and the foliage already do on D3D12) to find which pass
the GPU stalls in. Not changed here: the file is theirs. Evidence:
`$S/w4hang/*/run*.log` (stacks at the end of each hung run).

## Gaps and notes for W5

- **Hi-Z SSR** not built (above); SSR measured at ≈ 0.2 ms.
- **Underwater view**: caustics are now the water's, so from under the
  surface there are none (W1's bed-side path drew them there); W5's
  underwater view should add them back from below.
- **The sheet is one-sided in data**: seen from behind (inside the notch) it
  is shaded with the flipped normal; fine from the pool and the banks.
- **Streaks from the side** read as faint horizontal ribs (the V scroll
  seen edge-on); a stronger fray toward grazing would hide them.
- **Mist** is a sprite effect: no interaction with the wind or the player,
  and its rate does not follow the river's current, only the drop. A
  `waterfall_mist` placed by hand works as any emitter.
- **Pool foam** reads as the detail map's lace away from the boil; a
  flow-advected foam texture would read better (no sampler free).
- **Cascades** are opt-in (`cascade_step`, set in the Stream preset); W3
  documents' rapids are unchanged. A cascade on a very long slope makes
  many small sheets (one draw each).
- **Obstacles** are discs; a long flagstone is approximated by a disc of a
  quarter of length + width.
- **River audio, ripples, splashes, the underwater fog**: W5.
- The game's Graphics tab still needs W1's `water_quality` row (user's
  project).
