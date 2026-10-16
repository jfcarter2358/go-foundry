# Phase W1 — water look

Built 2026-10-15. Spec: docs/terrain-paint/SPEC.md, "Water > Look". Every
water surface in the engine changed with this: brush faces wearing a `water`
material, compiled surfaces, water sectors (`water:` blocks), and the tiles
`region_terrain` generates. That change in appearance is intended.

## What was built

**A water pass.** Water is no longer drawn in the opaque pass. It is drawn in a
pass of its own after the sky and before the sorted transparent pass
(render/water_pass.go):

- `SurfaceState.IsTransparent` is true for water, so brushes and compiled
  surfaces queue it the way they queue glass. A cooked placement wearing water
  stays on the classic path (`world.Scenery.wearsWater`) instead of the GPU
  cull, because the cull draws everything it holds in the opaque pass. A
  generated water tile is a few triangles, so this costs nothing measurable.
- DrawWorld moves the water entries out of the transparent queue
  (`splitWaterDraws`). **water: copy** blits the frame's depth, and from medium
  quality also its colour, into textures, and draws the sky into the water's sky
  map. **water** then draws the surfaces back to front, blended and *writing
  depth*. Glass, volumetrics, sprites and TAA motion after it therefore sort
  against the surface, not against the bed.
- Water casts no shadow (`gfx.Geometry.CastsShadow`, `batchCastsShadow`, in
  every shadow walk that had an `Alpha < 0.05` test). The sun reaches the bed.

**The shading** (resources/shaders/water.glsl, included by fragment.glsl):

- **Depth colour.** The view ray's length through water and the bed's depth
  come from the depth copy. Light from the bed is cut by Beer–Lambert
  absorption over the way down and back up
  (`shallow_color ^ (path · absorption)`). What is lost is replaced by
  `scatter_color` lit by the surface's own light (sun, ambient, GI).
- **Soft shoreline.** Within `shore_fade` metres of zero depth the result fades
  to the plain bed.
- **Refraction** (medium and up). The colour copy is sampled offset by the
  ripple normal, scaled by depth. A sample that lands on something in front of
  the water is rejected.
- **Reflection.**
  - High quality: a 16-step screen-space march along the reflected ray through
    the depth copy, with 4 halving steps, a thin-hit test and edge fades.
  - Otherwise the **sky map**: an octahedral 128² RGBA16F texture drawn each
    frame from sky_frag.glsl itself (render/water_sky.go). It covers cubemap,
    gradient, transitions, yaw and the haze, and is blurred by roughness
    through its mips.
  - With no sky, `reflect_color` × the ambient light (GI where on) along the
    reflected ray.
  - The weight is water's fresnel (F0 0.02) × `reflectivity`/0.5.
    `reflect_color` is now a hue tint on the reflection.
- **GGX sun glint** with roughness: height-correlated Smith visibility, the
  sun's cascade/VSM shadow and the cloud shadow. Roughness is widened with
  distance against sparkle. This replaces Blinn-128.
- **Detail normals.** One generated tileable ripple map
  (gfx/water_textures.go), sampled three times at different sizes, headings
  and drift speeds, summed as slopes on top of the swell's normal. Its
  strength fades with distance and at grazing angles, where the anisotropic
  filter would draw streaks.
- **Gerstner swell** (`swell`, `swell_length`). Four deep-water waves round the
  flow heading, with a trochoid-like profile (sharp crests, flat troughs) at
  deep water's phase speed. **Vertical only**: true Gerstner also moves the
  surface sideways, and the fragment stage needs undisplaced X/Z. The old three
  sines are kept, with headings moved off the world axes, which used to line
  their crests up into a grid.
- **Shore foam.** The generated map's foam network is thresholded by a band
  where the bed is within `0.25 + 0.75·foam` m of the surface. Hook for later:
  a mesh with vertex colours adds its **red** channel to the foam mask, meant
  for W3's river ribbons.
- **Crest glow.** Back-lit crests (looking toward the sun, high on the swell)
  take a little of `scatter_color` × the sun (`subsurface`).
- **Caustics** (high quality). These are drawn by the surfaces *under* the
  water. fragment.glsl multiplies `lighting` by `waterCaustics()` for any
  non-water fragment below a listed body's surface and inside its box. It uses
  a procedural two-layer pattern, faded with depth, absorption and the face's
  upward-ness, and adds **no sampler** (the visibility buffer's material pass
  is at its limit). render/water_caustics.go lists up to 8 bodies nearest the
  camera: cooked placements and brush faces wearing water, each a box plus a
  surface height. It uploads them to the world program and to every material
  pass program (`frameUniforms`).

**Samplers.** None added. A water draw borrows `blendTex` (scene colour, unit
10), `blendMask` (scene depth, 11), `normalMap` (sky map, 12) and `tex`
(unit 0). A water surface wears the generated detail map as its albedo
(`SurfaceState.finishWater`). The borrowing is spelled out in
gfx/water_draw.go and as `#define`s in water.glsl. No FX bits and no
visibility-buffer variant keys are used.

**The height field** lives in one file, resources/shaders/water_waves.glsl,
included by both vertex.glsl and fragment.glsl. The two stages cannot drift
apart, and a test pins that neither defines its own. The visibility buffer's
splice keeps working because the include guard drops the second copy and the
vertex part's calls land on the first. nanite_vis_source_test's expectation was
updated.

## Parameters (the `water` effect)

Old, kept so every .fmat still parses: `wave_amplitude`, `wave_length`,
`wave_speed`, `reflectivity` (now a multiplier where 0.5 is physical; old 0.45
files stay close to physical) and `reflect_color` (now a tint).

New: `absorption`, `shallow_color`, `scatter_color`, `subsurface`,
`roughness`, `refraction`, `foam`, `shore_fade`, `flow_speed`,
`flow_direction`, `detail_scale`, `detail_strength`, `swell`, `swell_length`,
`caustics`, `ssr`.

Defaults are `material.DefaultWater()` (material/material_kinds_water.go), the
single source that `gfx.DefaultWaterParams` and the ParamDefs read. A level's
`water:` block keeps its five numbers over the default look
(`world.WaterParamsFromRaw`). The full table is in docs/guide/28-materials.md,
"Water".

**Presets** (resources/materials/): `water` (the default spelled out),
`water_tropical`, `water_alpine`, `water_lake`, `water_river`, `water_swamp`
and `water_sea`. region_terrain's `water_material` already names any material.
A preset there makes the generated tiles wear it, and stay classic-path through
`wearsWater`. Documented in 33-generators.md.

## Setting

`gfx.WaterQuality` takes `low`, `medium` or `high` (default high):

| Tier | What it adds |
| --- | --- |
| low | depth copy only: depth colour, sky reflection, foam, blended over the bed |
| medium | + colour copy and refraction |
| high | + SSR and caustics |

Where it is set:

- graphics.yaml `water_quality` (app/graphics_settings.go)
- `@level.prop.water_quality`
- console `water_quality`
- `foundry play -water`

F3 profiler rows: `water: copy`, `water`. The game's own Graphics tab is
generated in the OneDrive project (tools/gen_settings_fui.py). That file was
**not** touched: the engine side is there for it.

## Files

New:

- internal/material/material_kinds_water.go
- internal/gfx/water_draw.go
- internal/gfx/water_textures.go
- internal/gfx/water_settings.go
- internal/render/water_pass.go
- internal/render/water_sky.go
- internal/render/water_caustics.go
- resources/shaders/water.glsl, water_waves.glsl, water_oct.glsl
- the six preset .fmat files
- tests:
  - internal/material/material_water_test.go
  - internal/gfx/water_textures_test.go
  - internal/render/water_pass_test.go
  - internal/render/water_gl_test.go

Edited (small, targeted):

- Shaders: fragment.glsl (uniform block → include; waterNormal call → waterSurfaceNormal; include water.glsl; the caustics multiply; the old water block → `waterShade`, with `finalAlpha = alpha`) and vertex.glsl (include)
- material: material_kinds.go, material_resolve.go
- gfx: geometry.go (WaterParams embeds material.Water; old applyWaterUniforms moved) and material_surface.go (overlay line, finishWater ×2, IsTransparent, water locs and texture binds in Apply/ApplyLocs)
- render:
  - world_frame.go (caustics upload, water pass)
  - nanite_vis.go (frameUniforms)
  - sky.go (uniform upload factored into `bindSkyLook`)
  - shadow.go, scenery_instanced.go, scenery_gpu_cull.go, chunk_batch.go, chunk_nanite.go, static_batch.go, static_nanite.go (shadow casting)
- world: scenery.go (`wearsWater`) and water_surface.go
- settings and tools: app/graphics_settings.go, script/script_props_level.go, devui/commands.go, harness/cmd_play.go
- tests: material_test.go, material_shipped_test.go, gfx/material_surface_test.go, world/water_test.go, render/nanite_vis_source_test.go
- docs: guide 28, 33, 19, 17 and SCRIPTING.md

## Tests

- `go test`:
  - internal/material passes, after updating two older tests (TestWaterMaterialMatchesTheEngineDefaults, TestRoundTripKeepsWhatWasChangedAndDropsWhatWasNot) that pinned the old wave defaults.
  - Pass: internal/gfx, world, render (non-GL), app, script, devui, harness, procgen/stages.
- GL (`FOUNDRY_GL_TEST=1`, WSLg Mesa d3d12):
  - **TestWaterPassDrawsDepthColourAtEveryTier.** A pool with a ledge 0.3 m down and a floor 5 m down, under one water face, drawn through DrawWorld at each tier. Checks:
    - one surface in the pass, with the right copy flags per tier;
    - the shallow ledge brighter than the deep end;
    - the deep end leans blue-green;
    - the picture changes;
    - `water_swamp` hides the ledge.
  - **TestWaterPassCost** (logs only).
  - TestMaterialFxSamplersFitTheMaterialPass passes again after the other sessions freed their slots. W1 adds none.
  - TestUnlit passes, so the world program builds and draws.
- **Full GL suite** (`FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...`, run once near the end): gfx passes; render shows **13 failures**, none of them the water tests:
  - picture-parity tests: entity prepass, volumetrics depth, frame prep, grass, SW raster, wind, TAA;
  - TestMaterialFxOnEveryPath and its palette/per-tile variants;
  - TestGIKeepsBakedDirectLightAndDropsBakedIndirect;
  - TestNaniteFoliageDrawsTheClassicNearField;
  - TestUntexturedSurfaceIsPaintedWithItsNormal (lit result about 0.67× the albedo).

  Each is a lighting-level difference (small parity deltas, or a dimmer ambient). They come from files the lighting and soft-ground sessions were changing during the run (specular, AO/ambient, point-shadow arrays). No water surface appears in those scenes, and W1's only non-water shader change is gated by a uniform those tests never set. **Not bisected**, so not proven unrelated.
- Render check: a generated shore level (sand bed sloping from 0.8 m above to 10 m below the water, five rock boxes, sky_day, a sun) shot with `foundry play` from a scratch project, for every preset and every tier. Seen in the screenshots:
  - clear shallows showing the sand, turning teal-deep with depth;
  - the sky in the far water and at grazing angles;
  - the rocks reflected by SSR;
  - a glitter path of GGX glints toward the sun;
  - a foam line at the shore and round the rocks' waterlines;
  - caustics on the shallow bed;
  - each preset distinct: turquoise tropical, black-brown swamp, ocean sea.

## Measured cost

Measured with TestWaterPassCost: a 1440² target (about 1080p's pixel count),
the pool filling the lower part of the view, timed as the wall-clock
difference with the pass skipped, `glFinish` every frame, on WSL's d3d12 GL
over the GTX-class card. The profiler's GPU timestamps read zero under WSL, so
this is the only number available, and it is noisy.

| Tier | Cost of the pass |
| --- | --- |
| low | 1.9–2.5 ms |
| medium | 0.6–1.4 ms |
| high | 1.8–3.0 ms (after cutting the SSR to 16 + 4 steps) |

Low reading above medium is noise or WSL overhead, not a real ordering.
**Not measured natively on the 1070.** The spec's budget (≤ 2 ms at high) is
probably met there, but that is unconfirmed. Use the F3 rows on Windows.

## Known gaps / honest notes

- **Caustics** reach fragment.glsl surfaces only: brushes, compiled surfaces,
  models, and generated terrain through the material pass. Not the block
  terrain (terrain_frag.glsl), the biome ground, scatter or grass
  (biome_light.glsl), which have their own stages. Bodies are boxes plus a
  height, so a dry hollow below the water level inside a pond tile's box would
  wrongly take caustics. Bodies are re-gathered only when the level's scenery
  or chunk count changes.
- **Gerstner is vertical-only** (see above). Generated water tiles and brush
  faces are coarse meshes, so swell displacement barely shows on them anyway;
  the swell lives in the normal.
- **SSR** is a linear-then-halving march against the full-resolution depth
  copy, not Hi-Z. False hits behind thin objects are limited by a thin-hit
  test, but not eliminated.
- **Underwater view** (camera below the surface) gets only a plain absorption
  tint, no reflection. Underwater fog is W5.
- **Flow**: one heading per material. Flow maps and two-phase flow-mapped
  normals are W3. The vertex-colour foam hook exists but nothing writes it yet.
- The material editor's preview draws water with the shader's fallbacks (no
  copies: blended, assumed depth, ambient reflection).
- In the editor's wireframe mode the water is drawn as lines by the water pass (the copies are still taken).
- **Intermittent GPU hang in headless runs on WSL.** Some `foundry play` runs
  hung in a GPU wait inside the next frame's `glBufferSubData`. It happened at
  one view: the first shot after teleporting onto a rock and turning to face
  the sun. Two causes overlapped:
  - The first hangs came from a stale binary running against shared shaders
    that another session had changed (the point-shadow layout). Rebuilding
    stopped those.
  - After the rebuild, about 1 run in 4 still hung at that view. With
    `FOUNDRY_NANITE_VSM=0` it passed 3/3 (default and sea presets, low tier
    included).
  This points at the known WSL d3d12 hang around virtual-shadow-map page draws
  (docs/guide/19), made likelier by the extra work the water pass puts in the
  same frame. That is a guess, not a proof. It is not seen in the GL tests, and
  untested on native Windows/NVIDIA.
- **A lighter rectangle beside some rocks**, on the water, in some views and in
  every tier. It is roughly the rock's underwater footprint, offset sideways.
  Not explained. The SSR thin-hit test did not remove it, and medium (no SSR)
  shows it too. Best guess: the submerged rock seen through the refraction or
  shadow edges.
- The game's Graphics tab (OneDrive tools/gen_settings_fui.py) needs a
  `water_quality` row. Not done here: that is the user's project.
