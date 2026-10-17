# Phase W5 — interaction: ripples, splashes, underwater, river audio

Built 2026-10-16. Spec: docs/terrain-paint/SPEC.md ("Phases", W5); earlier
phases PHASE_W1, P1–P3, W2, W3, P4, W4 (whose "Gaps for W5" are items 4 and
6 here). The user-facing pages are docs/guide/37-water.md (new), 28-materials.md
("Water"), 33-generators.md (ground_edit "Sound"), 36-terrain-paint.md
("Rivers and streams"), 26-saves-and-checkpoints.md and docs/SCRIPTING.md
("Water").

## What was built, item by item

### 1. Water query — `world.WaterAt`

internal/world/water_query.go. Built from **what the renderer draws**: every
triangle of every placement slot whose material has the water effect, and
every brush face wearing one, in world space — not from the generator's
terrain.yaml, which a release build does not ship. A falling sheet (`falls`)
and any triangle steeper than ~73° are left out. Flow comes from a flow-mapped
mesh's vertex colours (green/blue, ±4 m/s × the material's `flow_map`),
turned with the placement. The triangles go into a 4 m X/Z grid (counts +
prefix sums, two int32 arrays); a query is one cell's point-in-triangle tests.
Overlapping surfaces: the one just above the point, else the highest below.
"In" means at most `WaterQueryMaxDepth` (40 m) under a surface (a surface
has no bed). Rebuilt lazily when the level pointer, scenery count, chunk
count or `BrushMeshGeneration` change; `InvalidateWaterQuery()` forces it.
Lock-protected, safe from any goroutine. `WaterCrossing(a, b)` finds where a
segment enters water from above (half-cell steps).

Scripts: `water_at(place)` → null, or `{"under", "depth", "surface", "flow",
"material"}` (script_actions_water.go; `in` is a flang keyword, hence
`under`); doc row in flang/builtin_doc.go; docs/SCRIPTING.md "Water".

On w5_demo: 9 water placements (pond, marsh and river tiles), the grid
built in a few ms at the first frame; the debug check found the pond, the
plunge pool (with its 1.7 m/s current) and the stream exactly where the
shots stood.

### 2. Ripples (player, entities, splashes, rain)

- **Source**: `world.PushWaterRipple` (water_ripples.go), a mutex-guarded
  capped queue like the trail stamps. Pushed by the sim's wading watch and by
  every splash.
- **Renderer**: render/water_interact.go drains it each frame into a live
  list stamped with the frame clock, keeps the **16 nearest the camera**, and
  uploads `uniform vec4 waterRipples[16]` (x, z, birth time, floor(reach·100)
  + strength·0.999) and `uniform vec4 waterRippleInfo` (count, rain, -, -) to
  the world program in the water pass. **No sampler, no texture unit, no FX
  or variant bit.** water.glsl `waterRingSlope` draws each as a damped wave
  packet (λ 0.2–0.35 m, Gaussian envelope, spreading at 0.5 + 0.25·reach m/s,
  fading with age and toward its reach); `waterInteractSlope` adds them to the
  ripple slopes in `waterSurfaceNormal`. Seen from straight above, where the
  fresnel leaves little reflection to bend, rings barely showed (the GL test's
  first frame), so ring crests add a little of the reflected sky
  (`waterRingSlopeLen`, a global the normal pass leaves for waterShade).
  A strong young ring (a body dropping in) leaves foam at its heart
  (`waterRippleFoam`).
- **Rain**: the engine has no weather, so a level key `rain_ripples:` (0–1),
  `@level.prop.rain_ripples` (script_props_level.go), saved in the save game
  (savefile `RainRipples`/`HasRainRipples`, so older saves keep the level's
  value). The shader draws three offset hashed grids of drop rings, a cell
  ringing when its hash is under the rain's strength (`waterRainSlope`).
- **Hitscan**: a shot crossing water splashes it on the way (the shot is not
  stopped): script_actions_hitscan.go calls `splashShot`.

### 3. Splashes

world/water_splash.go `SplashWater(lvl, at, kind, strength, tint)`: rings +
a positional sound + a one-shot emitter (never written to the level, never
saved — level_save.go already skips one-shots), tinted by the water's
shallow colour. sim/water_wade.go (`stepWater`, called after `stepWalkers`
in sim.go) watches the player and every falling entity (not vehicles, not
frame riders): crossing the surface downward faster than 2.2 m/s is
`SplashEnter` (a body under 0.6 m tall: `SplashImpact`); wading (depth under
¾ of the body's height) makes a `SplashStep` every stride and a wake ring
every 0.28 s while moving (a slow ring when still); deeper, rings above the
body as it moves near the top. New presets: resources/particles/water_step,
water_splash, water_impact.fpar (particle_mist texture). Wading foam trails:
not built (optional).

### 4. Underwater view

All in render/water_interact.go, decided once a frame at the eye
(`world.WaterAt(gfx.Cam.Position)`) with a hysteresis band of 4 cm + the
material's wave amplitude (`FOUNDRY_WATER_LOG=1` logs each transition):

- **Murk as a fog volume**, not the distance fog: a box from the surface down
  80 m, 400 m round the eye, is put first in the frame's fog volumes
  (`frameVolumes(lvl)` replaces `lvl.Volumes` at the six fog upload sites:
  world_frame, sky, shafts, volumetric, nanite_vis, sprite_render). Density =
  mean of −absorption·ln(shallow_color) per channel (clamped 0.06–3 /m);
  colour = scatter_color × (sky ambient + sun·sin elevation) × 2.2, darker
  with depth. A volume fogs only the in-water part of each ray, so the bank
  seen up through the surface is not fogged by its whole distance. The level
  is never written. (A first version swapped the distance fog; that fogged
  the world above the surface by its whole distance and was dropped.)
- **Caustics from below**: the bed-side path W4 retired (fragment.glsl
  `waterCaustics`, the 8-body list) lists this one body again under water at
  the high tier (water_caustics.go). The visibility buffer folds that path out
  when the list is empty (`visKeyNoCaustics`); `warmMaterialVariants` now also
  builds the caustic variants once per level when the camera first comes
  within 8 m above a surface (`waterInteract.near`), so the first dive does
  not stall on a program build, and a level whose water nobody approaches
  builds nothing extra.
- **The surface from below** (water.glsl `waterBelowShade`): Snell's window
  (sinθt = 1.333 sinθi, soft rim), water→air fresnel inside it, total
  internal reflection outside showing the murk (read back from the frame's own
  fog at a level point 150 m off, so it matches the volume exactly). The
  colour copy behind the surface was fogged by the same water; it is unfogged
  with the fog's affine form (`applyFogScene(0)`, `applyFogScene(1)`) before
  the fragment's fog applies once.
- **Muffled sound**: audio/audio_muffle.go — under water the effects bus is
  summed into its own buffer, two one-pole low-passes (corner swept 4 kHz →
  500 Hz, cross-faded in from dry), ducked 45 %, music untouched, the amount
  gliding over 0.12 s. Zero cost above water.
- **Transition**: the hysteresis band keeps a passing wave from flipping the
  state each frame. **Not handled**: the near plane exactly at the water line
  (half the screen should be wet); a third-person camera decides by the
  camera, not the player.

### 5. River audio

procgen: new `procgen.Sound` + `RunContext.PlaceSound`, `StageResult.Sounds`,
`Result.Sounds()`, placements.yaml `sounds:`, genlevel `Options.Sounds`,
`ToRawSound`, Stats `sounds -n +m` (gencli and the editor's file splice pass
them) — the W4 emitter plumbing, mirrored. procgen/stages/ground_edit_audio.go
places, with `water_sounds` on (Added, default **off**; RectCovered, so
toggling it rebuilds no downstream piece; the River, Stream, Pond, Lake and
Marsh presets turn it on): `roar_<river>_<n>` (water_falls) at each fall,
first; `join_<n>` (water_rapids) at confluences; `flow_<river>_<k>` every
`water_sound_spacing` (24 m) along each river — water_rapids where the
stations within half a spacing are riffly/foamy/fast, water_river elsewhere;
`lap_<body>_<k>` (water_lap) along pond/lake/marsh shores, ≤ 8 a body. No
sound within 0.7 spacing of another (a roar keeps max(0.7 sp, 0.35 reach)),
pitch 0.92–1.08 hashed from the ID. w5_demo: 19 sounds (4 flow, 8 rapids,
2 falls, 5 lapping).

**Distance culling** (world/sound.go `SoundUpdate`): a looping sound past its
max distance gives its voice back (0.25 s fade, 15 % hysteresis margin) and
takes one when the listener returns. Needed: the mixer keeps only 8 loops.

**Audio source: GENERATED**, not CC0 (no CC0 water recordings were at hand):
audio/audio_water_sounds.go synthesises seeded noise + chirping bubbles,
deterministic; 4 seamless loops (16 kHz, 3–5 s) and 3 one-shots (22.05 kHz):
water_river, water_rapids, water_falls, water_lap, water_step, water_splash,
water_impact (resources/sounds, 550 KB total). The test regenerates them with
`FOUNDRY_WRITE_WATER_SOUNDS=1` and otherwise checks the shipped files match
(±2 LSB) and every loop's seam is no bigger than its largest inner step.
Nobody has listened to them through speakers in this phase.

### 6. W4 leftovers

- **Pool foam**: the lace is now gated by two scales of the detail map's
  noise into patches, the thickest closing into clots, and on flow-mapped
  water the foam coordinates are stretched up to 3× along the current so it
  trails as streaks (water.glsl foam block; one more detail-map sample, two
  on flow-mapped water).
- **Falls brown streaks**: the refraction through the glassy sheet is a third
  of W4's and the seen-through rock is half-desaturated and veiled; thick
  aerated parts never go fully clear (`white ≥ 0.28·aeration`); white water
  takes 0.22 of the sky light (it read grey in the notch's shade).
- **Back face**: a sheet seen from behind faces the eye.
- **Ribbing edge-on**: streaks flatten and the sheet thins toward grazing
  (`edgeOn`), white water staying longest.
- **Mist follows the wind**: new particle layer key `wind` (particle_wind.go;
  the renderer hands the level's wind, 1.5 m/s × strength, each frame;
  editor slider "Wind"); waterfall_mist's spray has `wind: 0.8`. Following
  the *current* is not done.
- **Caustics underwater**: item 4.
- Hi-Z SSR: skipped, as W4 explained.

### Recipe stage order (P4 bug, fixed)

The path brush applied its recipe (appending `flag_rocks_src`) and only then
added the path item naming it, so the hoist inside ApplyRecipe never saw the
reference. `stages.OrderStagesByRefs` (the hoist, exported, capped against
reference cycles) now also runs after AddPathStroke and AddRiverStroke add
their item: every stage named by an earlier one through any ParamStage,
top-level or in a list item, moves up to just above its first reader.
TestRockPathOverAnExistingGroundEditKeepsTheOrder applies river→path,
path→river, pond→path and pond→river→path and checks every reference points
upward and the document validates (it fails without the fix).

### Also

- play's state report prints `water <material>: surface, feet … m under,
  eye … m under` (internal/play/state.go) — how the shots were placed.
- Particle editor: "Wind" slider.

## Files

New: world/water_query.go (+_test), water_ripples.go, water_splash.go;
render/water_interact.go, water_w5_gl_test.go; sim/water_wade.go (+_test);
script/script_actions_water.go, script_water_test.go; audio/audio_muffle.go
(+_test), audio_water_sounds.go (+_test); particle/particle_wind.go,
particle_water_test.go; procgen/stages/ground_edit_audio.go (+_test);
procgen/genlevel/splice_sounds_test.go;
procgen/stages/paint_preset_order_test.go; resources/particles/water_step,
water_splash, water_impact.fpar; resources/sounds/water_*.wav (7);
docs/guide/37-water.md; this file.

Edited (targeted): world level.go, level_schema.go, sound.go; levelio
level_loader.go, level_save.go; savefile, savegame (rain); script
script_actions.go, script_actions_hitscan.go, script_props_level.go; flang
builtin.go, builtin_doc.go; sim/sim.go (one call); audio audio_mix.go,
audio_voice.go; particle particle_doc.go, particle_file.go, particle_sim.go;
render water_pass.go, water_caustics.go, world_frame.go, sky.go, shafts.go,
volumetric.go, nanite_vis.go, sprite_render.go, nanite_vis_variants.go;
procgen placement.go, context.go, run.go, genlevel/splice.go,
gencli/gencli.go, stages/ground_edit_run.go, ground_edit_river_params.go,
paint_preset_recipe.go, paint_preset_path.go, paint_preset_river.go;
editor editor_generator_splice.go, editor_particle_panel.go,
editor_yaml_docs_gen.go (regenerated: one line, rain_ripples); play/state.go;
resources/shaders/water.glsl; resources/particles/waterfall_mist.fpar;
resources/paint river, stream, pond, marsh, lake.fpb (`water_sounds: true`);
docs guide 26, 28, 33, 36, README (one row), SCRIPTING.md, SPEC.md (Status).

Not touched: fragment.glsl, vertex.glsl, fog.glsl, scene_block.go/glsl
(foundry-42's), GI, shadows, material/splat files (foundry-6e's),
sim/footsteps.go, score/tracker. No texture unit, sampler, FX bit or variant
bit claimed; the uniform cost is 17 vec4 in the world program's fragment
stage (rings + info).

## Tests

All pass (exact runs at the end of the phase):

- `go build ./...`; `go vet` on world, levelio, savefile, savegame, script,
  flang, sim, audio, particle, render, procgen (+stages, genlevel, gencli),
  editor, play — clean.
- New: TestWaterAtFindsAGeneratedSurfaceAndItsFlow,
  TestWaterAtPicksTheSurfaceJustAboveAndReadsBrushWater,
  TestWaterQueryLeavesFallingSheetsOut (world);
  TestWadingRingsTheWaterAndAJumpSplashes (sim); TestWaterAtAndRainRipples
  (script); TestMuffleCutsTheHighsOfTheEffectsBusOnly,
  TestWaterSoundsAreSeamlessAndShipped (audio);
  TestShippedWaterSplashesBurst, TestWindCarriesALayerWithWind (particle);
  TestWaterSoundsFollowTheWaterWithoutStacking (stages);
  TestSpliceReplacesTheGeneratorsSounds (genlevel); GL (`FOUNDRY_GL_TEST=1`,
  WSLg d3d12): TestWaterRipplesRingTheSurface (a ring changes the surface
  where pushed, gone 7 s later; rain changes it everywhere),
  TestWaterUnderwaterView (under: muffle 1, one caustic body at high, the
  murk volume first; the frame differs from the dry one; back above:
  everything handed back, pixel-identical centre).
- Suites: see "Test results" at the end.

## Measured and seen

Scene: scratch `$S/pproj` generator **w5_demo** (W3's valley + a forest-floor
disc, a meadow disc, a marsh, a rock path across stream_2 with ford stones,
the pond deepened to 4.2 m so a standing player's eye is under, water sounds
on): 5 rivers, 2 waterfalls, 2 confluences, 2 standing bodies (pond, marsh),
407 path stones, 19 sounds, 3 mist emitters. Note: the rock_path recipe put
its `flag_rocks_src` stage after ground_edit and the run refused ("names a
stage which runs after this one"); moved by hand at the time — a P4 bug,
**fixed** (see "Recipe stage order" above).

Shots (`$S/w5fin`, tools `$S/w5tools/shots.sh`, all looked at):

- `overview.png` — the valley: river into the dark pond, out over the white
  fall, the stream with the path's stones across it, the marsh.
- `path.png` (w5s1) — flagstones running down to the stream, a ford stone,
  sedge either side, clear water with foam at the banks.
- `wade_pond` (w5s3) / `wade_shelf.png` — knee-to-thigh deep in the pond's
  inlet looking down: the bed through clear green water, the wake's rings as
  soft bright arcs; `wade.png` — the stream's edge: sand bed, caustics, foam
  streaks along the bank (the feet ended just above the water there).
- `splash.png` — dropped in from 5 m: droplets thrown up at the feet (the
  crown is mostly below the first-person view; the splash reads better in the
  GL test and from outside the body).
- `falls_under.png` — 3.5 m from the 2.2 m sheet in the plunge pool: a pale
  streaked curtain, glassy gaps only between streaks, no brown, mist droplets.
- `underwater_side.png` — in the pond, 0.27 m under: the bed and the basin's
  edge sinking into green murk within ~10 m (water_lake: "the bed lost after a
  few metres").
- `underwater.png` (w5s3, pitch 59) / `underwater_up.png` — Snell's window:
  the sky (with the sun's glow at the rim) inside a wavy disc, the murk
  mirrored outside it. The sky looks soft because the level's sky is soft
  looking straight up (`$S/w5dbg/skyup_s.png` from dry land is the same).
- `rain.png` (level w5_rain, `rain_ripples: 1`) — drop rings all over the
  pond, the wake rings larger. The first try's rings were bright bullseyes
  (bench pose crop `$S/w5tools/rain_crop.png`); amplitude and the crest glint
  were cut.
- `forest.png` — litter, twigs and seedlings on the forest-floor disc;
  `marsh.png` — reeds; `meadow.png` missed the meadow (camera aimed at bare
  ground) — not re-shot.
- GL dumps (`$S/w5dump`): calm / ringed / rain from above; above / under /
  under-looking-up of a brush pool.

**Frame cost** (FOUNDRY_BENCH, w5_demo, 1600×900, TAA, VSM on, GI default,
200 frames after 60 warm; WSL d3d12 — the profiler's GPU columns read 0 here,
so costs are frame-time deltas; the GPU is not a 1070):

| pose | high | medium | low | high, water hidden | high, rain 1 |
| --- | --- | --- | --- | --- | --- |
| path (stream ahead) | 31.3 | 30.8 | 30.8 | 29.1 | 31.6 |
| wade (water fills the view) | 32.0 | 31.5 | 31.9 | 25.3 | 32.7 |
| falls (in the plunge pool) | 60.0 → 50.9* | 60.1 | 59.6 | 56.6 | 51.1 |
| under (in the pond) | 31.0 | 30.3 | 29.9 | 28.2 | 31.6 |
| overview | 27.1 | 26.5 | 26.9 | 25.1 | 27.0 |

So the whole water pass with every W5 feature is ~2 ms at a normal view and
~6.7 ms with water filling a 1600×900 screen; the tiers differ by under 1 ms
here (within noise: the cost is the shader body and the copies, not SSR or
caustics); rain adds ~0.3–0.7 ms full-screen; rings cost nothing with none
live (the bench player stands elsewhere). *The falls pose was 60 ms because
of W4's **mist**: hiding sprites took it to 37.9 ms — the camera inside the
cloud draws ~90 full-screen alpha puffs. The spray was cut to 60 smaller,
slightly denser puffs (50.9 ms); a near-camera fade in the sprite shader is
the real fix (open). CPU: water rows 0.05 + 0.15 ms.

**Headless runs**: 40 play/bench runs this phase, **0 hung** (VSM on, the
engine default), after foundry-42's SceneBuffer orphaning change.

## Gaps and notes

- **Splashes are first-person-shy**: the crown and the foot splashes are at
  the feet, mostly below a first-person view; they show from outside (a
  third-person camera, another body). No wading foam trail.
- **Underwater**: no split view when the near plane cuts the surface; no
  swimming, buoyancy or breath; the sky through Snell's window is not hazed;
  the murk is one colour (no per-channel extinction: red is not lost first
  along the view); light shafts under water are not drawn.
- **Rings**: 16 nearest the camera; packets are radially symmetric (no
  moving-body wake V); rings are not stopped by the shore.
- **Rain**: rings only — no falling streaks, wet surfaces or sound.
- **Audio**: generated clips (not recordings); the muffle filters only the
  effects bus; a river's sound does not follow its local speed between
  placements; sounds past the 8 looping voices nearby are refused (spacing
  keeps it to 2–4 in practice).
- **Mist** follows the wind, not the current; close-up overdraw (above).
- **Query**: surfaces only (no bed: "in" is within 40 m under); a moving
  (dynamic) water brush is not tracked until a brush mesh change; the grid
  is rebuilt on the first query after a change (a few ms on w5_demo).
- Not verified in a live interactive editor: the particle editor's Wind
  slider, ground_edit's Water sounds checkbox and spacing, and how any of
  this sounds through speakers.

## Test results

Final runs (GOFLAGS=-p=6 GOMEMLIMIT=6GiB):

- `go build ./...`: ok. `go vet` on every touched package: clean.
- `go test` world, levelio, savefile, savegame, script, flang, sim,
  audio/..., particle, play, internal (layering): all ok.
- `go test ./internal/procgen/... ./internal/render/... ./internal/gfx/...
  ./internal/harness/...`: all ok (harness's known
  TestAPlaySessionDrawsTheRoomItIsStandingIn did not fail this time).
- `go test ./internal/editor/...`: only the score session's known
  TestScoreSibeliusKeymap and TestScoreRebinding fail;
  TestYAMLFieldDocsUpToDate passes after regenerating (one line).
- GL (`FOUNDRY_GL_TEST=1`, WSLg d3d12): `./internal/render -run
  'Water|NaniteVisMaterial|Caustic'` ok (W1, W4 and W5 water tests, the
  material-variant tests); `./internal/gfx/...` ok. The **whole** render GL
  suite was started with a 55 min timeout and killed by hand after 32
  minutes with no failure reported so far: its process had grown to 27 GB
  resident on a 31 GB box shared with other sessions (the suite's own growth
  across hundreds of GL scenes; not investigated further). So the full render
  GL suite was not run to completion.

## Fixes after W5

Three bugs the user found in terrain_paint_demo (2026-10-16):

1. **Water drawn pure black from below its own height.** Two causes.
   (a) The shader's "seen from under the surface" test was `camPos.y <
   fragWorldPos.y`: any reach of a sloping river higher than the eye — the
   river above a fall seen from its pool, every upstream reach seen from
   downstream — took the from-below path (Snell's window / total internal
   reflection), whose murk is the water's fog volume, absent in the open
   air, so black. Now a surface is shaded from below only when the CPU says
   the eye is under water (`waterRippleInfo.z`, set by water_interact.go
   from the same hysteresis state) AND the eye is on the underside of the
   surface's own plane — the mesh normal, which a river ribbon now gives
   per vertex along its slope (`riverSurfaceNormal`, groundRiverVersion
   `ground-river-2`; the gentler one-sided slope, none past 0.6, so a fall's
   step tilts nothing). Seen from beneath in the air the surface is shaded
   as from above with the view mirrored through the plane; the depth into
   the water is measured along that normal, and the ripple normal rides the
   slope. (b) `WaterAt` counted any triangle under 73° from level as a
   surface, so the ribbon's strip pitched down a fall's step stood "over"
   the plunge pool's air: now 41° (`waterMinFacing` 0.75), steeper than any
   river or rapids. (c) The murk darkened as exp(−depth·density·0.5): two
   metres into a river it was near black; now exp(−depth·density·0.12),
   floored at 0.35.
2. **White water across the current.** The flow vertex colours were right
   (TestRibbonFlowRunsAlongTheRiver: both directions, drawn either way).
   The shader was not: the foam was squeezed along the current by turning
   the WORLD POSITION into the current's frame with a speed-dependent
   squeeze, per pixel. Tens of metres from the origin, a riffle's speed
   change or a bend swung that coordinate by metres per metre, crushing the
   foam along the current into stripes across it. Now two of four fixed
   axes 45° apart are squeezed (constant 2.6×) and cross-faded by the
   heading (`waterFoamBin`, textureGrad with the position's own gradients);
   the vertex foam (a riffle's band across the river) is broken into
   streaks by the squeezed patch noise; the slow third ripple layer is
   advected down the current (it drifted along the material's
   `flow_direction`, +X, whatever way the river ran); the swell's
   fixed-heading sines are cut to 35 % on flow-mapped water.
3. **Falls tiled side to side.** Mesh (terrain/river_falls.go, seeded by
   the fall's position): columns every ~0.35 m (odd count), each thrown its
   own distance (`ColVX`, 0.8–1.2×) from an uneven lip (`ColLip`, −5..+3
   cm), aeration thick and thin along the lip, sides frayed wider and more
   raggedly lower down, the odd gap opening below the lip; the notch clears
   the lowest column (`VXMin`, `OutMax`). Shader (`waterFallsShade`):
   low-frequency lip noise for glassy stretches and white ropes; streaks in
   jittered columns, each with hashed width/length/speed/phase, blended at
   the seams (`waterFallsColumn`, textureGrad); falling clots and tears that
   grow toward the foot and sides; scrolls wrapped with fract. No new
   sampler, uniform or texture unit (the under flag rides in the existing
   `waterRippleInfo.z`).

Tests: TestWaterAtBelowAFallIsNotUnderTheLip (world; fails at 0.3),
TestRibbonFlowRunsAlongTheRiver (stages), TestFallsCurtainVariesAlongTheLip
(terrain), GL TestWaterBelowTheSurfaceHeightIsNotBlack (render).

Verified on terrain_paint_demo (regenerated with `-force`; only its
generated outputs and level changed): from the plunge pool and from
downstream the river above the lip is water, not a black slab; the spawn
view's far upstream strip is no longer black; confluence foam trails down
the main river instead of lying across it; the main fall is thick and white
in places, thin and glassy in others, with an uneven lip. All water GL tests
pass. Open: a headless run with the eye under the lower river hangs in the
driver (glBufferData in SceneBuffer.Flush), before these changes as after;
the submerged-in-a-river case is covered by the GL test instead.

Follow-up (falls read as a stained curtain): through thin water the sand in
the colour copy showed as tan blotches. What shows through glassy water is
now the fall's face, wet and shadowed: strongly darkened (×0.3) and nearly
grey. Thick and thin now mostly show as brighter and fainter white water
(`white` holds ≥55 %, the veil is 0.4, the interior cover ≥0.9). The mesh's
gaps are rarer and narrower, weighted toward the sides and the lower half
(almost none mid-top). The per-column throw and hashed streaks are kept.
