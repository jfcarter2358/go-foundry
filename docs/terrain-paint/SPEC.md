# Foundry terrain paint and water: implementation spec

Goal: paint onto GENERATED terrain (the `.fgen` heightfield / region-terrain
worlds, not block terrain) — ground cover such as leaf litter, twigs, dense
grass and flowers, rock paths — and paint water: ponds, lakes, marshes and
rivers, with water that looks real and vibrant.

It builds on the canyon and regions toolsets: read docs/canyon/SPEC.md (ground
rules, engine facts), docs/regions/SPEC.md, and docs/guide/33-generators.md for
the stages this touches (`terrain`, `region_terrain`, `scatter`, `grass_field`,
`terrain_material`, `region_material`, `drift`).

Repo: /home/jfcarter2358/personal/code/main/foundry. Branch:
feature/jc/brush-editing.

## Decisions taken (2026-10-15)

- **Paint is an INPUT to the generator, never a placement.** A stroke writes a
  named paint mask; stages read it as one more band. Placements stay owned by
  the `gen.<fgen>.<stage>.` prefix, so a rerun never loses paint, and painted
  cover still obeys the land (no grass on a cliff, none under water).
- **Rivers: hand-painted AND generated from drainage.** A painted stroke
  becomes an editable spline; a `from_drainage` option traces rivers down the
  terrain's own flow mask above a threshold. Both feed one river builder.
- **Water look: one default plus per-material colour presets** (`.fmat`
  presets: clear/tropical, alpine, temperate lake, river, swamp/murky, sea).
  The preset is the material; every number can be overridden in the `.fmat`.
- Poly Haven for plants, rocks and ground textures (existing rule).
  Water normals, foam and caustics are GENERATED: Poly Haven has none.

Confirmed by the user 2026-10-15:

- Brush surface: the Generator 2D view first, the 3D viewport second.
- Paint is world-level (world XZ), multiplied by region share where a band
  already is; no per-region-preset paint.
- Height paint is IN scope: ponds and rivers need it, and paths flatten.

## Engine facts (surveyed 2026-10-15)

- Generated terrain = heightfield cooked to Nanite tiles; masks (`flow`,
  `slope`, `deposition`, `cliff`, `strata`, `curvature`, region `water`) are
  (N+1)² 0..1 grids sample-aligned with the heights (procgen/terrain/masks.go).
- `scatter`, `grass_field` and the material stages weigh by mask BANDS; adding
  a band is additive and per rule.
- `grass_field` writes one `grass_<i>_<j>.fgrass` per tile (12 B/clump) and a
  kit `.fgk`; drawn by internal/grassfield + render with thinning/LOD/wind.
- `drift` already recooks terrain tiles downstream of `terrain` and uses
  `RunContext.Supersede` — the precedent for a height-editing stage.
- `ParamSpline` (+ the Generator editor's path editing) exists; there is no
  mask/image parameter.
- Water today (terrain/water.go, stages/region_terrain_water.go): water table
  + pond fill of closed hollows; one flat Nanite mesh per tile wearing a
  `water` effect material; `mask_water.png`. No rivers.
- Water shading today (fragment.glsl `isWater`, vertex.glsl `waterHeight`):
  three sines, scrolling texture, Schlick fresnel to a FLAT `reflect_color`,
  Blinn-128 glint, alpha blend. Drawn forward, outside the visibility buffer.
  No refraction, depth colour, sky/scene reflection, foam or flow.
- Available to build on: the volumetrics' scene depth copy
  (render/volumetric.go `copyDepth`/`sceneDepthDirect`), Hi-Z (render/hiz.go),
  the sky cubemap (render/sky.go, gfx/texture_cube.go), DDGI probes, TAA.
- Budget: GTX 1070 is already 24–34 ms in canyon_savannah. Every new GPU cost
  needs a graphics-setting tier and an F3 profiler row.

## Architecture

### Paint layers

- New param type `ParamMask` (procgen/params.go) naming a layer; the doc holds
  the layer list: `paint: [{name, kind, resolution}]`.
- Kinds: `density` (0..1), `exclude` (keep clear), `direction` (RG flow
  vector + strength), `height` (signed metres offset / flatten target),
  `water` (pond/marsh area + level), `material` (splat override index).
- Storage: sparse, tile-aligned 8-bit (16-bit for height) PNGs beside the
  `.fgen`: `generators/<name>.paint/<layer>/<i>_<j>.png`, default 0.5 m/texel.
  Untouched tiles store nothing. Rivers are splines in the doc, not pixels.
- Hashing: each tile's content hash feeds the per-tile hash of the stages that
  read the layer, so a stroke dirties only the tiles it touched.
- A pure package `procgen/paint` (layering `upward` entry) reads/samples
  layers; no world/gfx imports.

### Consumers

- `paint` band on `scatter`, `grass_field`, `terrain_material` and
  `region_material` rules: `{layer, band: [lo, hi], invert}`, multiplied in
  with the existing bands. `exclude` layers are a global multiplier.
- `material` layers: a splat override (paint "trail" and that layer wins).
- `direction` layers orient placements (path stones, fallen branches).
- New stage `ground_edit` (Terrain): applies `height` layers, pond basins and
  river channels to the terrain after erosion, recooks touched tiles, and
  rewrites the masks it changed (water, flow near rivers). Every downstream
  stage reads its heights. Modelled on `drift`.

### Ground-cover drawing

Dense flat clutter can't be scenery (~1 ms CPU per thousand in view). The
grass instancer gains per-kind options:

- `orient: ground` — lie along the surface normal, random roll (leaves,
  twigs, needles, pebbles);
- `wind: false` for rocks and branches;
- `card` kinds — flat alpha-cut quads with depth bias, for litter.

Large items (logs, stumps, path stones, boulders) stay `scatter` placements
so they collide.

### Brush presets

A preset drives several layers per stroke, e.g. "Forest floor": litter splat
1.0, twigs 0.6, ferns 0.3, grass exclude 0.5. File: `paint/*.fpb` (YAML).
Shipped presets: forest floor, meadow, wildflowers, dense grass, fern
understorey, moss, gravel, pebbles, mud, dirt trail, rock path, scree,
snow patch, sand drift, ash, pond, marsh, river, stream, keep clear.

### Editor

- Generator 2D view: paint over the shaded relief; radius, strength,
  falloff, erase, smooth; layer / preset picker; overlay tint of the active
  layer; its own undo stack (as the YAML editor has).
- 3D viewport: same brush, raycast onto the generated terrain tiles, overlay
  projected onto the terrain.
- Commit on mouse-up (debounced) → incremental rerun of dirty tiles only. While
  dragging: overlay + preview dots, never a rerun.
- Incremental requirements: `grass_field` per-tile (already per-tile files);
  `scatter` needs tile-scoped Supersede; splat weights need per-tile rewrites
  (start from region_material_tiles.go).

## Water

### Look (shader) — applies to ALL water, existing included

| Feature | Needs |
| --- | --- |
| Depth colour: Beer–Lambert absorption + scatter colour | scene depth behind the surface (reuse the volumetrics depth copy) |
| Soft shoreline fade | same depth difference |
| Refraction of the bed | a scene COLOUR copy before the alpha pass (new) |
| Reflection: SSR on the plane → sky cubemap → GI probe fallback | Hi-Z; sky cubemap bound to the water path |
| Detail normals: 2–3 scrolling tileable maps + Gerstner swell (lakes/sea) | generated normal maps (baked from an ocean spectrum) |
| GGX sun glint with roughness | replaces Blinn-128 |
| Foam: shoreline by depth, rivers by foam mask, advected by flow | generated foam texture |
| Caustics on the bed | ground shaders read `mask_water` and add animated caustics |
| Wave-crest subsurface tint | cheap approximation |

- `waterHeight` must stay identical in vertex.glsl and fragment.glsl (it
  already says so); Gerstner changes both.
- The `water` effect grows params: `absorption`, `scatter_color`,
  `shallow_color`, `foam`, `roughness`, `refraction`, `flow_speed`,
  `caustics`, `ssr`. Defaults reproduce a good-looking default, not today's
  flat look — existing levels change appearance; that is intended.
- Presets as `.fmat` files in resources/materials/: `water.fmat` (default),
  `water_tropical`, `water_alpine`, `water_lake`, `water_river`,
  `water_swamp`, `water_sea`. `region_terrain`'s and the river stage's
  `water_material` name one.
- Settings tier `gfx.WaterQuality`: low = no SSR/refraction (sky + depth
  colour only), medium = refraction, high = SSR + caustics. Budget ≤ 2 ms on
  the 1070 at high.

### Ponds, lakes and marshes

- Pond brush (`water` layer): `ground_edit` carves a basin — shelving bank,
  shallow ledge, deeper middle — and the water stands at the painted level or
  the spill height. Reuses terrain.FindWater's pond fill (a painted pond is a
  hollow guaranteed to exist).
- Marsh brush: shallow, broken water; reeds via the `shore` band, wet ground
  via the existing wet rule.

### Rivers

- Sources: painted strokes (→ an editable spline per river, width from the
  brush radius, editable along the length) and `from_drainage` (trace the flow
  mask above `drainage_threshold`, simplify to splines, `min_length`). Both
  produce the same river splines; drainage rivers can be "adopted" into
  hand-editable ones.
- Hydrology: the surface descends monotonically (running minimum along the
  path); channel carved to it (thalweg, banks, floodplain); pool/riffle
  alternation; a WATERFALL where the drop exceeds `max_slope` (surface step,
  falls mesh, mist emitter, plunge pool); confluences where a spline ends on
  another.
- Mesh: a ribbon per river cooked like the pond tiles, UV across × along,
  vertex data = speed + foam.
- Flow map: per tile, RG direction × speed, baked from the spline and the
  channel width; the shader does two-phase flow-mapped normals and foam.
- Downstream: gravel/stone bed splat under water, boulders in riffles (foam),
  sand bars on inside bends (`drift`), reeds/wet banks, grass excluded from
  the channel. Later: river sound emitters along the path, underwater fog.

## Phases

Run IN ORDER, one agent per phase, same ground rules as docs/regions/SPEC.md
(no commits, additive edits in shared files, resource limits, scratch project
for generated output, PHASE_<n>.md at the end).

- **W1 — water look.** Shader rebuild + water `.fmat` presets + quality tiers
  + profiler row. Independent of everything else; ships first.
- **P1 — paint core.** `ParamMask`, `procgen/paint`, storage + hashing,
  `paint` band in scatter / grass_field / terrain_material / region_material,
  Generator 2D-view brush, full rerun.
- **P2 — live painting.** 3D viewport brush, overlay, undo, per-tile
  incremental rerun (tile-scoped Supersede, per-tile splat rewrites).
- **P3 — ground cover.** Instancer `orient: ground` / `wind: false` / cards;
  litter, twigs, needles, pebbles kits from Poly Haven; brush presets
  (`.fpb`).
- **W2 — ground_edit + ponds.** Height layers, pond and marsh brushes, basin
  carving, painted water levels.
- **W3 — rivers.** Painted + drainage river splines, hydrology, ribbon mesh,
  flow map, flow-mapped shading, bed dressing.
- **P4 — paths.** Direction layer, rock path / dirt trail recipes: splat
  override, oriented stepping stones, wear, flatten.
- **W4 — waterfalls, confluences, caustics polish, crest subsurface.**
- **W5 — interaction.** Ripples (player, rain), splashes, underwater view,
  river audio.

Each phase adds its part of docs/guide (33-generators.md stage sections; a
new guide page for painting and one for water).

## Status (2026-10-16, after phase W5)

All nine phases are built; each PHASE_<n>.md has the detail, the files, the
tests and the measurements. Nothing here is committed.

### What shipped

| Phase | Shipped |
| --- | --- |
| W1 | The water shader rebuild (depth colour, refraction, SSR → sky map → ambient, GGX glint, generated detail normals, shore foam, caustics, crest glow), seven `.fmat` presets, `gfx.WaterQuality` low/medium/high, the `water: copy` / `water` profiler rows |
| P1 | `ParamMask`, `procgen/paint`, sparse tile PNG storage + hashing, `paint` bands on scatter / grass_field / terrain_material / region_material, exclude and material layers, the Generator 2D-view brush, `foundry gen paint` |
| P2 | The 3D-viewport brush and overlay, paint undo, per-tile incremental reruns (tile-scoped Supersede for scatter, per-tile splat rewrites, rect keys) |
| P3 | Instancer `orient: ground`, `wind: false`, card kinds; litter/twig/needle/pebble kits from Poly Haven; `.fpb` brush presets with recipes (20 shipped) |
| W2 | `ground_edit`: height layers, flatten, pond / lake / marsh basins with painted or spill levels, recooked tiles, rewritten water masks |
| W3 | Painted and drainage rivers → one builder: monotone surface, carved channel, pools/riffles, point bars, waterfalls (data), confluences, flow-mapped ribbons, river masks, boulders, bed dressing |
| P4 | Direction layers, the path brushes (rock path, dirt trail, gravel path, stepping stones, game trail): splat override, oriented stones, wear, flatten, ford stones |
| W4 | Waterfall sheets (ballistic curtain, notch, falls shader, `water_falls`), mist emitters (and the generator → level emitter plumbing), cascades, confluence mixing lines, caustics moved onto the water, wet bed, obstacles in scatter footprints |
| W5 | `world.WaterAt` + `water_at`, rings (wading, entering, shots, splashes) and rain rings (`rain_ripples`), splash presets and sounds, the underwater view (murk fog volume, caustics from below, Snell's window, muffled effects bus, hysteresis), river/falls/confluence/shore sounds placed by ground_edit with distance-culled loops, W4 polish (foam patches/streaks, falls look, mist wind), docs/guide/37-water.md |

### Still open (carried by the phase notes)

- **Rendering / water**: Hi-Z SSR (a min-pyramid march; SSR measured ≈ 0.2
  ms, so it would buy reach, not time); ripples tile visibly from high above;
  Gerstner swell is vertical only; the material editor's preview has no
  copies; no split view when the near plane cuts the water line; one-colour
  underwater murk; no swimming, buoyancy or breath; no rain weather beyond
  rings; rings are radial packets (no wake V, no shore reflection); 16 live
  rings; the waterfall mist's close-up overdraw (a near-camera sprite fade);
  mist follows the wind, not the current; the falls sheet is one-sided data
  (shaded two-sided since W5); the game's Graphics tab still lacks a
  `water_quality` row (the user's OneDrive project).
- **Audio**: generated water clips, not recordings; the muffle is effects-bus
  only; loops beyond the mixer's eight in earshot are refused.
- **Paint / incremental**: upstream-only layers hashed with a 192 m margin;
  the first rerun after an upstream stage rebuilds every piece; region_material
  far weights and per-tile materials rerun whole; whole-PNG splat re-reads;
  layer delete/rename leave folders on disk; no tablet pressure; the brush
  aims at collision geometry; the overlay can clip ridges.
- **Ground cover / presets**: seven splat slots run out after ~3 texture
  presets (paths fall back to existing roles); recipe excludes only reach
  stages that exist when applied; decimation does not always reach
  `max_triangles`; scan-dependent kind sizes; per-vertex card lighting, cards
  never cast. (The rock_path recipe placing its rocks stage after an
  existing ground_edit, found in W5, is fixed: `OrderStagesByRefs`.)
- **Terrain edits**: round brushes make round bowls; one level per standing
  body; flow/deposition not recomputed after edits; a region terrain's own
  water not reconciled with carved ponds; summed, unclamped height layers;
  heightmap range widening shifts every sample once; recipe rewiring only on
  apply.
- **Rivers / paths**: flow maps at grid resolution (no swirl behind
  boulders); long thin painted rivers simplify to few points; sand bars not
  read by `drift`; cascades opt-in; obstacles are discs; no bridges or
  boardwalks; masks on the terrain grid (thin trails); one stretched flagstone
  scan; stones not grass-avoided per stone; steps read as a terraced ramp.
- **Hangs**: the intermittent headless GPU wait (W1–W4: 1 in 4 to 1 in 10
  runs; evidence with foundry-42). After foundry-42's SceneBuffer orphaning
  change, W5's 40 headless runs had none.

### Never verified in a live, interactive editor window

Every editor feature of these phases was exercised headlessly (editor tests,
`foundry gen paint`, `foundry gen run`, `foundry play` shots), never by hand
in a running editor: the 2D-view and 3D-view brushes, overlays and their
undo; the paint layer panel; the preset combo and recipe popup; the pond,
marsh, river and path brushes and their panels (Shift+wheel width, Adopt,
Edit, −/+); flatten; live updates while painting with water; the
ground_edit "Water sounds" controls; the particle editor's "Wind" slider; and
how the water clips sound through speakers.
