# Generators

A **generator** builds content from rules instead of by hand: terrain, rocks,
scatter, textures. It is a document, a `.fgen` in `resources/generators/`, that
you edit, preview and run inside the editor. Running it writes its outputs
(cooked Nanite meshes, masks, images) under `resources/generated/<name>/` and
places what it made into a level's scenery.

Open the editor with the **Generator** button in the editor strip.

Nothing here is a script or an external tool. Every stage is engine code, and
`foundry gen run` on the command line runs exactly what the editor's **Run**
button runs.

## The idea

A generator is a list of **stages**. Each stage is one kind of job, with its own
parameters: "a noise heightfield 512 m across", "boulders, twelve variants",
"scatter those boulders over that terrain where the slope is steep". A later
stage can read an earlier one by naming its ID, which is how the canyon's talus
knows where the cliffs are.

Three properties hold for every stage:

- **Deterministic.** The same document makes the same bytes. Each stage's
  random numbers start from a seed built from the document's seed, the stage's
  ID and its own seed parameter, so changing one stage's seed reshuffles only
  that stage.
- **Incremental.** A stage whose parameters, kind and upstream stages have not
  changed since its last run is skipped. A big generator re-runs in seconds when
  you change its last stage.
- **Owned output.** Everything a generator places carries the ID prefix
  `gen.<generator>.<stage>.`. A rerun replaces exactly those entries and leaves
  everything you placed by hand alone.

## Make a desert canyon in five minutes

The **Desert canyon** template wires every stage into one document: Poly Haven
ground textures and scans, an eroded mesa terrain with a backdrop to the
horizon, its material, cliff walls, scattered rocks and plants, the
atmosphere, HDR lighting and a spawn point.

1. **Make it.** In the Generator editor, File > New, pick **Desert canyon**
   and name it (say `canyon`). On the command line:
   `foundry gen new canyon -template desert_canyon`. The document names a
   level of the same name; it does not need to exist yet.
2. **Run it.** Press **Run** (F5), or `foundry gen run canyon -workers 8`. The
   first run downloads about 200 MB from Poly Haven (cached for every later
   run and project) and takes a minute or two; the progress modal shows each
   stage. When it finishes, the placements, the volumes, the fog, sky, sun,
   tone curve and spawn are spliced into `levels/canyon.yaml`, created if
   it was missing.
3. **Walk it.** Open the level and play. The spawn is on the canyon floor.
   HDR and the virtual shadow maps need TAA (the default anti-aliasing); the
   editor's own view stays LDR.
4. **Change it.** Everything is a parameter, and only what you change reruns:
   - the canyon's course: select `ground`, switch the view to 2D and drag the
     canyon path's points (a terrain rerun is about 20 s, and everything
     downstream follows);
   - the walls: `rockface`'s `depth` (how far ledges stand out), `bed_spacing`,
     `soft_share` and `joint_spacing` — a rerun of the walls is a few minutes;
   - the sand against the rocks: `drift`'s `wind`, `fillet` and `tail`;
   - the light: `air`'s `sun_yaw`, `sun_pitch`, `sun_brightness`,
     `exposure` and `ambient_brightness`;
   - the plants: the `trees`, `deadwood` and `bushes` rules of `scatter`.
   A rerun that touches only placement or materials takes about a second.

What it makes, stage by stage:

| Stage | Kind | What |
| --- | --- | --- |
| `tex` | polyhaven_textures | red sand, cracked ground, river pebbles, and `cliff_side`, a layered red cliff face, as the four ground layers |
| `ground` | terrain | 512 m of mesa country at 0.5 m with a stepped canyon through it; a 3 km backdrop of the same country round it (`far`) |
| `ground_mat` | terrain_material | the splat over the masks, every layer anti-tiled; sand bars drawn out down the washes; caprock on the high flats, bedding relief and stronger normals on the walls, and the backdrop's material |
| `rockface` | wall_sheet | the cliffs as rock geometry: hard beds as ledges over soft ones weathered back beneath them, joints, alcoves, a caprock along the rims |
| `rockfall` | rocks | sandstone blocks in the walls' own texture, for the talus |
| `cliffs` | polyhaven_rocks | scanned cliff faces from 5 to 40 m — phase 7's wall cladding, **off** (`rockface` builds the walls now) |
| `boulders`, `stones` | polyhaven_rocks | Namaqualand boulders (with light copies for scatter), stone and rock sets split into pieces |
| `trees`, `deadwood`, `bushes` | polyhaven_plants | quiver trees, a dead quiver trunk, rooibos and shrubs |
| `walls` | cliff_wall | the scans up the canyon's walls — **off**, as `cliffs` |
| `scatter` | scatter | talus (the `rockfall` blocks) under the walls, boulder fields, stones in the washes, the plants on the flats |
| `drift` | drift | sand piled against the talus, the boulders and the cliffs' feet, with tails down the wind and down the washes; the touched terrain tiles recooked |
| `plants` | vegetation | the desert biome's dry scrub: **off** by default (see the note under Performance) |
| `air` | environment | height fog, aerial haze, the canyon skybox, the sun, HDR with the ACES curve, the view distance the backdrop needs, the spawn |

The template ships as `resources/generators/desert_canyon.fgen`; File > New
writes the same document under your own name.

## The editor

- **Left:** the generators in `generators/`, and under them this generator's
  stages. Each stage row has an enable box and says `ok` (its outputs are up to
  date) or `stale` (a Run would run it). **Add...** offers every stage kind by
  category; right-click a stage (or use the buttons under the list) to
  duplicate, move or remove it.
- **Right:** the generator's own settings (note, seed, output folder, target
  level) and the selected stage's parameters. The panel is built from the stage
  kind's schema: every parameter has a tooltip, a parameter at its default shows
  a dim `default` and is not written to the file, and the `x` beside a changed
  one puts it back.
- **Centre:** the preview of the selected stage, in two modes (Tab switches):
  - **2D**, top-down: the stage's images (a heightmap, a mask, a shaded relief)
    with an optional overlay at an opacity, its points and paths, a metre grid,
    and the world position and value under the cursor. This is where paths are
    edited — see below.
  - **3D**: the stage's preview meshes, lit, through the engine's own shader.
    Drag to orbit, middle-drag to pan, wheel to zoom. Pick a lookdev texture in
    the overlay, or leave it empty to see the mesh in its normal colours (or,
    for a mesh with baked vertex colours — a rock — in those). A rock variant
    is framed on its own and turns on a turntable while **Spin** is ticked.
- **Bottom:** the document's problems, the preview's notes, and the last run.

The preview re-runs by itself a moment after you stop editing, on a background
thread, at reduced resolution. It never blocks the editor and a stale one is
cancelled when you edit again.

### Running

| | |
| --- | --- |
| **Run** (F5) | Run every stale stage, then place the results. |
| **Run Stage** | Rerun the selected stage, and any stale stage it reads from. |
| **Force** (Ctrl+F5) | Rerun everything, fresh or not. |
| **Cancel** | In the progress window: stop after the current step. Finished stages keep their outputs. |

A run happens on worker threads behind a progress window with each stage's
progress and a log. The document is copied when the run starts, so you can keep
editing; the level window stays live.

**Open Level** opens the generator's target level in the level editor.

### Editing paths

A `spline` parameter (a canyon's course, a wall's line) is a list of points.
Each spline has an **Edit in view** button; or pick it from the path menu above
the 2D view. The active path gets handles:

- **click** empty space to add a point (on a segment: inserted there; elsewhere:
  added to the nearer end);
- **drag** a handle to move it;
- **right-click** a handle to delete it.

Each gesture is one undo step. The point table in the parameter panel shows the
same points as numbers, including any per-point values the stage reads (the
carve path's `width` and `depth`, for instance).

A `curve` parameter is a little graph in the panel: click to add a point, drag
to move, right-click to remove.

## Placing into a level

Set the generator's **Level**. After every run the placements go into that
level's `scenery:` (and `volumes:`, for stages that make volumes):

- If the level is **open** in the level editor, they go into it there, as one
  undoable step, and the level is left unsaved — save it, or Ctrl+Z in the level
  editor takes the whole run back.
- If it is **not** open, the level file is rewritten (with a `.bak` beside it),
  and only when something actually changed.

The placement waits while the level is compiling, loading or being played, and
lands as soon as it can.

Every placed entry's ID starts `gen.<generator>.`. Before placing, everything
with that prefix is removed. So:

- rerunning replaces the old placements, however many there were;
- removing a stage removes its placements on the next run;
- **never give a hand-made object an ID starting `gen.`** — the next run will
  delete it.

Renaming a generator (Save As under another name) gives its placements a new
prefix, and the old ones stay in the level until you remove them.

**Level settings.** A stage can also own a level-wide setting — the
[environment](#environment-level) stage's `fog`, `sky`, `view_distance`, the
sun and the ambient. A level has one of each, so there is no prefix: every
run writes the setting again, over whatever the level held. The sun is the
level's **first** directional light (its angles, colour and brightness; its
ID and script are kept), made if the level has none. Removing the stage
leaves the level with the last values it wrote. A setting the level format
cannot hold fails the splice before anything is written.

## The file

```yaml
doc: Desert canyon, red sandstone
seed: 1234
output: generated/desert_canyon      # under resources/; default generated/<name>
level: desert_canyon                 # optional: the level that receives placements
paint:                               # optional: paint layers (36-terrain-paint.md)
  - {name: meadow, kind: density}
stages:
  - stage: noise_preview             # which kind of stage
    id: ground                       # unique; lower-case letters, digits, _
    params:
      size: 512
      octaves: 6
  - stage: noise_preview
    id: test
    enabled: false                   # kept, not run, not placed
```

The file's name is the generator's name, and is part of every ID it places, so
it follows the same rule as a stage ID. Loading is strict: an unknown key, a
parameter the stage kind does not have, or a value of the wrong shape is an
error, and every such problem in the file is listed at once with where it is
(`stage ground: param sise ... (did you mean "size"?)`).

### Parameter types

| Type | In the file | In the editor |
| --- | --- | --- |
| float, int | `1.5`, `4` | drag box, clamped to the range |
| bool | `true` | checkbox |
| enum | `ridged` | menu of the choices |
| vec2, vec3 | `{x: 1, y: 2}`, `{x: 1, y: 2, z: 3}` | two or three drag boxes |
| color | `{r: 1, g: 0.5, b: 0.2}` | colour picker |
| texture | `sandstone` | the texture picker |
| string | `any text` | text box |
| seed | `1234` | number box with a **Dice** button |
| stage | `ground` | menu of the stages above this one (of the right kind) |
| spline | `[{x: 0, z: 0, width: 30}, ...]` | point table, and the 2D view |
| curve | `[{x: 0, y: 1}, {x: 1, y: 0}]` | a graph to drag points on |
| list | `[{thickness: 4, hardness: 0.8}, ...]` | one foldout per item: add, remove, reorder |
| mask | `meadow` | menu of the document's paint layers ([Terrain paint](36-terrain-paint.md)) |

A mask names one of the document's `paint:` layers. A stage that names a layer
folds the layer's content into its hash, so painting reruns it, and painting a
layer it does not name does not. The stages that read paint rerun per tile:
only the pieces whose painted ground changed are made again, and the result is
what a run from nothing makes ([Terrain paint: Reruns](36-terrain-paint.md#reruns)).

A vec2's `y` is written `"y"`, quoted: YAML 1.1 reads a bare `y` as `true`.

## World generation: environment regions

One generator can grow several environments at once, blended into each
other: a desert canyon running into savannah with no seam. The workflow,
the Generator's region list, the adjacency rules ("Insert intermediate
region") and incremental runs are in [Environment regions](35-regions.md);
this section is how the stages do it. The pieces:

- **Region volumes** in the level (a volume's `region:` block — see
  [Environment regions](13-volumes.md#environment-regions)): a polygon
  footprint, the environment it grows, a priority and a transition width.
  The level is the source of truth.
- **Environment presets**: ordinary `.fgen` documents under
  `resources/environments/<name>.fgen`. A preset's `terrain` stage is the
  region's ground, its materials the region's ground material, and its
  placement stages run once per region. The built-ins (`desert_canyon`,
  `savannah` — wide rolling plains with shallow dry washes, gold dry-grass
  ground, the savannah biome's dense grass and acacia clumps as vegetation,
  scanned searsia and quiver trees in clumps, shrubs and granite boulders)
  are written to
  the project only when no file of that name exists, so an edited preset is
  never overwritten. `foundry gen env list` shows them; `foundry gen env
  write <name>` writes one.
- **The world generator**, `resources/generators/<level>_world.fgen`, made
  from the `level_world` template by the region inspector's **Open World
  Generator**, the Generator's **File > Generate World…**, or `foundry gen
  world <level>` — or from the `canyon_savannah` template, which ships a
  two-region layout: its regions stage has `seed_level` on, so the first
  run writes the layout into the target level as region volumes (making the
  level if needed; a level with regions keeps its own) and turns the switch
  off ([Environment regions](35-regions.md#quick-start-a-canyon-running-into-savannah)). Its first stage, `regions`, holds a copy of the level's
  region volumes, synced (sorted by id) before every run and — while the
  level is open — every preview. The copy is read-only in the parameter
  panel.

The `regions` stage's preview is the **biome overlay**: each environment's
colour, mixed by its share across the bands, with every footprint outlined.
Edit a footprint in the level editor and the overlay follows.

**How the shares work.** Each region's share of a point is a smoothstep of
the distance to its footprint across its band — 1 half a band inside, 0 half a
band outside, an even split on the edge between two equal neighbours — with
the edge meandering (`warp_amount`, `warp_scale`) so the band follows a
natural line. Higher priorities take their share first; what is left goes to
the nearest region. The shares always sum to 1.

**One terrain for every region.** The world generator's second stage,
`ground` ([region_terrain](#region_terrain-level)), grows each region's ground
from its preset's `terrain` stage and blends them across the bands BEFORE
erosion; then drainage, droplets and talus run once over the whole union, so
a river leaving a canyon carries on across the savannah and the canyon
country's edge wears down into the plain through gullies rather than a ramp.
It writes what a `terrain` stage writes — heightmap, masks, albedo, tiles on
one world tile grid, the far backdrop — plus a weight mask per environment
(`mask_biome_<env>.png`) and `biomes.png`, so later stages read it like any
terrain. Its preview shows the relief shaded in the biome colours.

**What runs per region.** At plan time each region's preset stages that can
run per region are added as `r_<region>_<stage>` (their placements are
`gen.<doc>.r_<region>_<stage>.…`), seeded from the region and the preset
stage so two regions of one preset differ; a preset stage's ref to its
preset's `terrain` is rewritten to the world generator's `region_terrain`,
and one to its `terrain_material` to the world generator's
`region_material`. The kinds that run per region are the placement stages —
`scatter`, `vegetation`, `cliff_wall`, `wall_sheet` — and the asset stages
they read — `rocks`, `polyhaven_rocks`, `polyhaven_plants`,
`polyhaven_textures` (an asset stage is added only when a placement stage of
the same region reads it, so a region whose placements are skipped downloads
nothing). `drift` does not run per region (it recooks the shared terrain's
tiles); the run logs it, and any preset stage whose stand-in the world
generator lacks (a scatter needing a `region_material`), as skipped.

**Placement by biome weight.** A region's placement stages place only its
share:

- `scatter`: every candidate's weight is multiplied by the region's share
  there, so rocks and plants thin out across the transition band instead of
  stopping at a line; its 32 m candidate blocks are counted from the world
  origin and seeded by world position, so a region's placements depend only
  on its own preset and the ground under it — editing, adding or moving
  another region's *preset* leaves them byte-identical (editing a region's
  footprint changes every region's shares, so every region reruns).
- `vegetation`: each cell's weight times the share, on a grid from a world
  multiple of `cell`.
- `cliff_wall`: only the columns where the region's share is at least ½
  (two regions' walls meet in the middle of their band). Its `paths`,
  `region_min`/`region_max` and `focus` are in the preset's coordinates,
  measured from the region's anchor.
- `wall_sheet`: the rock stands out of the terrain by the region's share, so
  a wall sinks back into the slope across the band; it wears its own
  environment's rock layer of the region material.
- A rock's `match_ground` aims at the ground colour of its own region (the
  region material's ground map weighed by the region's share), not the
  whole world's.
- Generated `rocks` are made once per preset stage and parameters into
  `generated/<doc>/_shared/rocks_<hash>/` and every region of that preset
  places the same files; Poly Haven imports were always shared (one folder
  per asset), and a second region's stage reuses an import the first made
  in the same run.

**Running one region.** `foundry gen run <doc> -region <id>` (or the
Generator's **Run > Run Region**) runs only that region's expanded stages,
plus any shared stage they need that is stale, and splices only their
placements (and those of the shared stages that ran): every other region's
placements stay in the level as they are. A plain run still splices
everything.

**One ground material for every region.** The world generator's third stage,
`ground_mat` ([region_material](#region_material-materials)), paints the
union with every environment's own ground: each preset's `terrain_material`
rules (rock on steep ground, gravel in the washes, cracked ground on the
flats, sand between) and its Poly Haven textures, evaluated over the whole
terrain and weighted by that environment's biome mask, baked into ONE splat
of up to eight layers. Across a band the two biomes' layers meet by their
height maps, the way sand and gravel do inside one biome. `ground`'s tiles
wear it (the template names it in `ground`'s `material`; a region terrain
with no material named wears the region_material that reads it).

## Stage kinds

`foundry gen list` prints every stage kind this build has; `foundry gen list
-kind <name>` prints one kind's parameters in full. The Claude panel's
`generator_stages` tool serves the same table.

### noise_preview (Debug)

A noise heightfield, cut into closed tiles, cooked as Nanite meshes and placed
as scenery. A debugging aid: it touches every part of the machinery, and it is
the quickest way to tell a plumbing problem from a terrain problem.

| Param | Type | Default | |
| --- | --- | --- | --- |
| size | float | 256 | Side of the square in metres, centred on the origin. |
| resolution | int | 128 | Cells a side. Must divide by `tiles`. |
| tiles | int | 2 | Tiles a side, each cooked on its own and border-locked so neighbours meet at every LOD. |
| height | float | 40 | The noise's height range in metres. |
| shape | enum | fbm | `fbm` rolling, `ridged` sharp crests, `billow` rounded lumps. |
| basis | enum | gradient | `gradient` (Perlin) or `value`. |
| octaves | int | 5 | Octaves of detail. |
| frequency | float | 0.008 | First octave's cycles per metre. |
| gain | float | 0.5 | Each octave's amplitude relative to the last. |
| warp | float | 0 | Domain warp distance in metres. |
| seed | seed | 0 | Changes the noise and nothing else. |
| profile | curve | 0→0, 1→1 | Remaps the noise before scaling: flatten lows, terrace, invert. |
| path | spline (x, z, width, depth) | none | A channel carved along the path. |
| bumps | list (position, radius, height) | none | Round hills (or pits, with a negative height). |
| cracks | float | 0 | Depth of a cellular crack network (30 m cells). |
| collision | enum | mesh | How the tiles collide: mesh, hulls, box, none, auto. |
| texture | texture | none | The tiles' texture. |
| cook | bool | true | Off: writes the images and the tiles as `.glb` only, places nothing. |
| note | string | | Free text, unused. |

Outputs: `heightmap.png` (16-bit), `slope.png`, `meta.yaml`, one
`tile_<i>_<j>.fnm` per tile, and the placements. Each tile is closed — top,
skirts down below the lowest point, and a bottom — so with `collision: mesh` it
is one solid to the navigation grid. Tiles go in with `chunk: auto`, which picks
the chunk whose footprint holds them when the level is open in the editor.

Start one from the **Noise demo** template (File > New), which has a carve path
and a bump already.

### terrain (Terrain)

Eroded desert canyon country as a heightfield: mesas with stepped strata
ledges, canyons carved along paths, washes and dendritic gullies, talus aprons
under the cliffs. It writes a 16-bit heightmap, masks the later stages read, an
albedo hint and a metadata file, and places the terrain as closed Nanite tiles.
Start one from the **Canyon terrain** template (File > New, or `foundry gen new
<name> -template terrain_demo`).

It is built in this order, and each group of parameters below is one step:

1. **Base shape.** A low, domain-warped noise for the lowland, and a **mesa
   field**: a large, warped noise cut into flat-topped plateaus in tiers
   (`mesa_tiers` 2 stands buttes on the mesas). `mesa_coverage` is the share of
   the map the first tier covers; `mesa_rim` is how much ground a rise takes.
2. **Canyons.** Each item of `canyons` is a path you draw in the 2D view. Its
   points carry the canyon's `width` (rim to rim) and `depth` (below the ground
   around it). A path runs **downstream**: its floor never rises along it. The
   cross-section is `v`, `u`, `slot`, `stepped` or `custom` (the `section`
   curve); `meander` makes it wander and `tributaries` branches side canyons
   off it.
3. **Drainage.** Water is routed over the whole map to its edge and channels
   cut in proportion to the area draining into them. This makes the washes and
   the branching gully networks that cut back into the rims; closed basins fill
   into flat sandy floors (`drainage_fill`).
4. **Strata.** The surface is stepped through the `strata` list, bottom up and
   repeated as high and low as the heights go. A **hard** layer stands as a cliff
   with a bench on top of it; a **soft** one weathers back to a slope. Only
   steep ground is stepped (`terrace_slope_lo`/`_hi`), so mesa tops and
   lowlands stay smooth. `strata_warp` and `strata_dip` make the ledges wander
   and tilt. The default stack is a Canyonlands-like section: a shale slope, a
   pale bench-forming sandstone, mudstone slopes, a massive cliff-former, a
   ledgy cap. Few, contrasting layers read best; many similar thin ones terrace
   every slope into contour lines.
5. **Droplets.** Particle erosion at full resolution cuts rills and gullies
   (the fluting on talus slopes) and drops sediment in hollows. Hard layers
   resist it (`resistance`).
6. **Talus.** Rock steeper than it can stand slides down: soft layers relax to
   `talus_angle`, hard ones stand up to `talus_cliff_angle`, and what falls
   piles up at the foot of each cliff.
7. **Detail.** Fine texture: jointed slickrock on hard layers, crumbly bumps on
   soft ones, nothing on sand.

The landforms are defined in metres, not samples, so changing `resolution`
changes the detail and not the shape. The preview is the same document at
`preview_cells` samples a side with lighter erosion: about 0.3 s at 256.

**The preview** shows the shaded relief by default (its readout is the
height); overlay any mask on it from the bar above the view: Height, Albedo,
Flow, Slope, Deposition, Cliff, Strata, Strata height, Curvature. The carved
canyons (meandered, tributaries paler) and the tile grid are drawn over it,
and each canyon control point shows its half width as a circle. The 3D view
shows the tiles painted with the albedo hint.

| Param | Type | Default | |
| --- | --- | --- | --- |
| size | float | 1024 | The side of the square, centred on the origin. Must be a whole number of tiles. |
| resolution | float | 1 | Metres between height samples. 0.5 is hero ground; 1 is a good default; the landforms are the same at any resolution. |
| tile_size | float | 128 | The side of each cooked tile (at most 128 m, so the GI distance field stays fine). Must divide Size, and be a whole number of samples. |
| seed | seed | 0 | Changes the noise, the droplets and the tributaries without changing anything else. |
| base_height | float | 0 | The lowland's mean elevation. |
| relief | float | 35 | The lowland's rolling relief (FBM half range). Keep it modest: the cliffs carry canyon country's relief. |
| frequency | float | 0.0035 | The lowland noise's first octave, cycles per metre. |
| octaves | int | 6 | The lowland noise's octaves. |
| warp | float | 90 | Domain warp of the lowland noise: bends its features. |
| mesa_height | float | 95 | Each plateau tier's rise above the one below. 0 turns the mesas off. |
| mesa_coverage | float | 0.6 | The share of the map the first tier covers; each higher tier covers about 45% of the one below. |
| mesa_scale | float | 520 | The plateaus' typical size. |
| mesa_tiers | int | 2 | Plateaus stacked on plateaus: 2 stands buttes on the mesas. |
| mesa_rim | float | 40 | How much ground a tier's rise takes. The strata turn the rise into cliffs and ledges; narrower is a sheerer escarpment. |
| mesa_roughness | float | 0.55 | Ragged rims: alcoves, points and spurs. |
| mesa_top_relief | float | 3 | The undulation left on the flat tops. |
| mesa_joints | float | 0 | How strongly the rims follow a fracture network: straight, angular rim segments and slots. Strong values read as tiles. |
| mesa_joint_spacing | float | 70 | The rim fracture network's spacing. |
| strata | list | 6 items | The rock layers, bottom up, repeated upward (and downward) as far as the heights go. Hard layers stand as cliffs with benches on top, soft ones weather to slopes; the colours feed the albedo hint. |
| strata.thickness | float | 10 | The layer's thickness. |
| strata.hardness | float | 0.5 | 0 is shale (slope-former, erodes fast), 1 is sandstone (cliff-former, resists). |
| strata.color | color | (0.72,0.45,0.3) | Rock colour for the albedo hint. |
| strata_base | float | -20 | The height the first layer starts at. |
| terrace | float | 0.85 | How strongly the surface steps through the layers: 0 smooth, 1 sheer cliffs and flat benches. |
| terrace_slope_lo | float | 8 | Ground gentler than this is not stepped: mesa tops and lowlands stay smooth. |
| terrace_slope_hi | float | 30 | Ground this steep is stepped at full strength. |
| strata_warp | float | 3 | Noise displacement of the layer heights, so ledges wander. |
| strata_dip | vec2 | (0,0) | Tilts the layers: metres of rise per 100 m along X and Z. |
| resistance | float | 0.6 | How much hardness slows erosion: at 1 a layer of hardness 1 does not erode at all. |
| canyons | list | 0 items | Canyons carved along paths. A path runs downstream (its floor never rises along it); edit it in the top-down view. |
| canyons.path | spline | 0 points | The centreline, downstream, with the rim-to-rim width and the depth below the ground around it at each point. |
| canyons.profile | enum | stepped | v: a V gorge. u: steep walls, a wide floor. slot: sheer walls. stepped: benches down the walls. custom: the curve below. |
| canyons.section | curve | 3 points | The custom cross-section: depth (1 floor, 0 rim) by distance from the centre (0) to the rim (1). |
| canyons.floor | float | 0.15 | The flat floor's share of the width. |
| canyons.steps | int | 3 | The stepped profile's benches. |
| canyons.meander | float | 40 | How far the canyon wanders off the drawn path. |
| canyons.meander_length | float | 260 | The meanders' wavelength. |
| canyons.tributaries | int | 4 | Side canyons branching off upstream, narrower and shallower, graded down to the main floor. |
| canyons.tributary_length | float | 220 | How far a side canyon reaches. |
| canyons.seed | seed | 0 | Varies this canyon's meanders and tributaries. |
| canyons.hold | float | 0 | How much of the carved shape survives the erosion (see [Authored landforms](#authored-landforms-stamps)): 1 keeps a slot canyon's sheer walls and open floor, which the talus would otherwise fill. |
| canyons.hold_margin | float | 6 | How far past the rims the hold reaches, easing out. |
| drainage | float | 2 | Stream-power erosion of the whole map: washes and dendritic gully networks cut back into the rims. 0 turns it off. |
| drainage_iterations | int | 100 | Routing-and-incision rounds. More is a more mature network. |
| drainage_cell | float | 4 | The drainage grid's spacing: the narrowest wash it cuts is a couple of these. |
| drainage_fill | float | 0.5 | How fast closed basins fill with sediment: flat playas and sandy wash floors. |
| drainage_diffusion | float | 0.1 | Rounds the ground between channels, per round. |
| droplets | float | 1.5 | Particle hydraulic erosion: rills, gullies and deposition at full resolution. 0 turns it off. |
| droplet_lifetime | float | 48 | How far a droplet runs. |
| droplet_inertia | float | 0.08 | How much a droplet keeps its heading rather than turning downhill: higher is straighter, smoother channels. |
| droplet_capacity | float | 0.25 | Sediment (m³) a droplet carries per unit of slope, speed and water. The main strength control. |
| droplet_erosion | float | 0.1 | Pick-up rate per metre run. |
| droplet_deposition | float | 0.1 | Drop rate per metre run once over capacity. |
| droplet_evaporation | float | 0.02 | Water lost per metre run. |
| droplet_radius | float | 1.5 | The brush a droplet erodes with. |
| droplet_gravity | float | 4 | How fast a droplet speeds up downhill. |
| droplet_passes | int | 4 | Rounds the droplets are split into, each over the whole map. |
| talus_angle | float | 34 | The angle of repose of soft layers: scree slopes relax to it. |
| talus_cliff_angle | float | 80 | The steepest a hard layer stands at. |
| talus_iterations | int | 60 | Rounds of sliding; more reaches further down the slope. 0 turns it off. |
| talus_rate | float | 0.5 | The share of the excess moved per round. |
| detail | float | 0.5 | Fine texture after erosion: jointed slickrock swells and cracks on hard layers, crumbly bumps on soft ones, none on sand. |
| detail_joint_spacing | float | 8 | The slickrock crack network's spacing (the cracks run in a set along one bearing). |
| cliff_angle | float | 50 | The cliff mask's threshold. |
| sand_color | color | (0.8,0.62,0.44) | The albedo hint's colour of washes and deposits. |
| mesh_step | int | 1 | Height samples per tile vertex: 2 meshes every other sample. Must divide a tile's samples. |
| collision | enum | mesh | How the tiles collide. mesh is the terrain's real surface; tiles are closed, so nav sees each as one solid. |
| texture | texture | "" | The tiles' texture; none draws them in their normal colours. |
| material | string | "" | The .fmat the tiles wear over their texture: a terrain_material stage's, gen_<generator>_<its id> (it prints the name). Empty: none. |
| chunk | enum | auto | Which level chunk the tiles stream with: `auto`, the chunk whose footprint holds each tile's centre (as every placement); `none`, always in — the ground under a level split into area chunks stays when an area is switched off. |
| uv_mode | enum | world | world: a top-down projection at UV scale, for a tiling texture. terrain: one 0..1 square over the whole terrain, for a terrain-wide image such as the albedo hint. |
| uv_scale | float | 0.125 | UVs per metre, in the world UV mode. |
| cook | bool | true | Cook and place the tiles. Off writes them as .glb and places nothing. |
| cook_memory | int | 3000 | How much memory the tile cooks may use together; fewer cook at once when tiles are big. |
| preview_cells | int | 256 | The preview's samples a side, at most (a fast mode: the same landforms, coarser erosion). |
| far | float | 0 | How far past the map's edge a coarse backdrop continues its mesas and canyons, toward the horizon; 0 for none. Raise the level's view distance (the environment stage's) to see it. |
| far_cell | float | 16 | The backdrop's sample spacing. Must divide Size and Tile size. |
| far_blend | float | 400 | Over this distance past the edge the map's edge eases into the backdrop's own landforms. |
| far_tile | float | 1024 | The backdrop tiles' largest side. |
| far_material | string | "" | The .fmat the backdrop tiles wear: a terrain_material stage's far material, gen_<generator>_<its id>_far. Empty: the tiles' texture. |
| far_terrace | float | 1 | How strongly the backdrop's steep ground steps through the strata (multiplies `terrace` out there). Steep far peaks whose ground rises about a bed per far cell alias their steps into a regular pattern of scales; lower it for them (the haze hides the beds). |

**Outputs**, in the stage's folder:

| File | |
| --- | --- |
| `terrain.yaml` | the metadata: bounds, resolution, how each file decodes, the strata, the tiles, the carved canyon paths |
| `heightmap.png` | 16-bit grey; metres = `lo` + v/65535 · (`hi` - `lo`) from `terrain.yaml` |
| `mask_flow.png` | drainage area, log scale: 0 at 4 m², 1 at 0.25 km² (washes) |
| `mask_slope.png` | slope / 90° |
| `mask_deposition.png` | deposited sediment d/(d+1), d in metres (sand, scree) |
| `mask_cliff.png` | slope past `cliff_angle`, feathered |
| `mask_strata.png` | the strata list index of the surface layer (the 8-bit value is the index) |
| `mask_strata_height.png` | position within that layer, 0 bottom to 1 top |
| `mask_curvature.png` | 0.5 flat, brighter concave (hollows), darker convex (rims) |
| `albedo.png` | an RGB colour hint from the strata colours, sand and cliffs |
| `relief.png` | a shaded relief, for looking at |
| `tile_<i>_<j>.fnm` | the cooked tiles (`.glb` when `cook` is off) |

Every image's pixel (i, j) is the sample at x = `min_x` + i · resolution, z =
`min_z` + j · resolution: one pixel more than `size / resolution` a side.

**Tiles.** The field is cut into `tile_size` squares, each a closed mesh (the
surface, skirts down to one common base, a bottom), so `collision: mesh` makes
each one solid to the navigation grid. Each is placed at its centre with
`chunk: auto`. Neighbouring tiles meet exactly at every level of detail: their
edges are locked against simplification and every tile is cooked on one shared
position grid. A tile that collides as its mesh is cooked without the convex
hull decomposition (which it would never use), which makes the cook about
twice as fast and a tenth of the memory.

Scatter and biome volumes grow on the tiles: the ground they look for now
includes any scenery placed with `collision: mesh`.

**The far backdrop** (`far` above 0). A square map ends: from its rims you
would see its edge tiles' skirts and, past them, nothing but the skybox. With
`far` set the stage continues the same country past the edge, coarsely, out to
`far` metres: the same mesa field, lowland and strata (so the far mesas have
the near ones' size, height and stepped walls), the canyons carried straight
on from where they leave the map, the map's edge eased into it over
`far_blend` metres, and the relief eased down to the lowland at the very end of
the reach so the backdrop's own edge, deep in the haze, has no silhouette. It
is sampled every `far_cell` metres (16 by default) and written as:

| File | |
| --- | --- |
| `far_height.png` | the backdrop's heights over the whole far square, 16-bit (decoded by `terrain.yaml`'s `far:` record) |
| `far_<i>_<j>.fnm` | the ring: closed tiles of up to `far_tile` metres, placed with no collision, wearing `far_material` |
| `strip_<side>_<k>.fnm` | the stitch: one cell wide between each edge tile and the ring, its inner edge the tile's own rim vertices, wearing the terrain's `material` |

The stitch is what hides the map's edge: the ring meets the map with no step
and no gap, and the edge tiles' skirts end up under ground. A 3 km backdrop is
about 50 ring tiles of a few thousand triangles; it adds a few seconds to the
run. Raise the level's view distance (the environment stage's
`view_distance`) to see it. The map's edge is still the playable edge: the
backdrop has no collision.

To see the tiles in the game's colours before the material phases exist, copy
`albedo.png` into `textures/` under a name, set `texture` to it and `uv_mode`
to `terrain`.

#### Landforms beyond canyon country

Environment presets other than the desert canyon need other ground. Each of
these is part of the **base shape** — added before anything erodes, so the
drainage, droplets and talus work it over like any other ground, and the far
backdrop continues it past the edge — and each is **off at its default**
(an existing document's hash and output do not move):

| Landform | Switch | What it makes |
| --- | --- | --- |
| Ridged mountains | `ridge_height` > 0 | Sharp crests and branching spurs `ridge_height` metres above the lowland, ranges `ridge_scale` apart (`ridge_octaves`, `ridge_warp`). `ridge_valleys` flattens the valley floors into broad, flat-floored troughs under steepened walls — glacial U-valleys (a cheap reshaping; no ice is simulated). Keep height/scale under about 0.25 or the walls stand steeper than talus lets them. |
| Dune field | `dune_height` > 0 | Transverse dunes across the wind (`dune_wind`: the bearing it blows toward, degrees from +X toward +Z): a long gentle stoss slope up to the crest and a short lee slip face (`dune_asymmetry`, held at the 34° angle of repose), crests `dune_wavelength` apart and bent by `dune_sinuosity`, broken into dune trains and flat interdune corridors (`dune_cover`), with wind ripples (`dune_ripple`, `dune_ripple_wavelength`) on the stoss slopes. |
| Coastline | `coast` ≠ off | A shoreline `coast_offset` metres inside one side of the region's footprint (`+x`, `-x`, `+z`, `-z`: the sea lies that way), or along the drawn `coast_line` (the sea on its right, walking it in order), wandering by `coast_meander`: the sea floor shelving to `sea_depth` below `sea_level` over `shelf_width`, a beach `beach_width` wide rising `beach_rise`, an optional wave-cut cliff (`cliff_height`, `cliff_width`), and the land's own relief returning over `coast_inland`. Fill the sea with the region terrain's water (below). |
| Volcanoes | `volcanoes` > 0 or `volcano_vents` | Cones `volcano_height` high and `volcano_radius` across (placed by seed inside the footprint, at least 1.2 radii apart, or at the vents you draw) with gullied flanks, a summit crater (`crater` of the radius, `crater_depth` deep) and `lava_lobes` lava tongues per cone — flat-topped, steep-fronted, `lava_length` × `lava_width` × `lava_thickness`. The vents are written to terrain.yaml (`vents:`) for the ground material's ash rule, and the lobes (`lobes:`: start, direction, length, half width, outline seed) for its lava rule. |
| Hummocks and ponds | `hummock_height` or `pond_cover` > 0 | Frost-heave hummocks (`hummock_height`, `hummock_spacing`) and flat-floored hollows over `pond_cover` of the ground (`pond_depth`, `pond_scale`): thermokarst ponds, which the region terrain fills with water (below). |

#### Authored landforms (stamps)

Noise puts landforms wherever the seed says. A level is laid out by its
designer: the slot canyon here, the mountain behind it, a ledge high on the
mountain for the cave to come out on, a long slope down for the ruins.
`stamps` is a list of such landforms. Each is a **kind** and **points**
drawn in the 2D view (a spline: click to add, drag, right-click to delete),
each point with a `radius` and a `height`. Like the landforms above they are
part of the **base shape** — applied after them, in list order, before
anything erodes — so the drainage, droplets, strata and talus work them over
like any other ground and the backdrop continues them. The preview draws
each point's radius (and, dimmer, the falloff past it).

| Kind | Points | `height` is | What it makes |
| --- | --- | --- | --- |
| `mountain` | the crest (one point: a peak) | the rise | A massif: a concave profile out to `radius`, spurs and gullies from a ridged noise (`ridges`), a lumpy outline (`rough`). Segments of the crest combine by the larger rise. |
| `plateau` | the top's spine (one point: a round table) | the rise | A flat top `radius` out from the points, its rim falling over `falloff`. The strata turn the rim into cliffs. |
| `platform` | each point a disc | the **absolute** height | Flattens the ground to the height within `radius`, easing out over `falloff` — a ledge on a mountainside, a terrace. |
| `ramp` | a path | the **absolute** height along it | A flattened way `radius` either side of the path at the heights interpolated along it — a road down a slope. |
| `cut` | a path or a point | the depth | Lowers the ground — a notch, a pass, a pit. |

| Field | Type | Default | |
| --- | --- | --- | --- |
| stamps.kind | enum | mountain | As above. |
| stamps.points | spline | 0 points | x, z, radius, height. |
| stamps.falloff | float | 20 | How far past the radius a plateau, platform, ramp or cut eases out. |
| stamps.rough | float | 0.4 | Ragged outlines; lumpy mountain flanks. |
| stamps.ridges | float | 0.6 | A mountain's spurs and gullies. |
| stamps.strength | float | 1 | How fully a platform or ramp flattens the ground. |
| stamps.hold | float | 0 | How much of the stamp's shape survives the erosion: 1 for a ledge that must stay flat. |
| stamps.seed | seed | 0 | Varies the stamp's noise. |

**Hold.** Erosion is what makes the ground look old, and what fills a slot
canyon with talus and rounds a ledge into a slope. A stamp's `hold` (and a
canyon's, with `hold_margin` past its rims) keeps its footprint: after the
drainage, terracing, droplets and talus, the heights there are blended back
toward their pre-erosion shape by `hold` (then the fine detail goes on). Use
it on the shapes a layout depends on — a walkable ledge, a slot's floor and
sheer walls — and leave it 0 on mountains. With no stamps and no hold, the
terrain is exactly what it was (the parameters are `Added`).

A slot canyon, for instance: `profile: slot`, `floor: 0.35`, widths 4–10 m,
depths 30–45 m, `meander` 5 over `meander_length` 70, `tributaries: 0`,
`hold: 1` at 0.5 m resolution carves sheer walls with a flat sandy floor.
The floor is graded downstream from the ground at its rims, so where a slot
heads into rising ground (a mountain's foot) its floor climbs steeply: keep
the head on level ground, or lower its `depth` there.

Two more only mean something in a world of several regions
([Environment regions](35-regions.md)), and the plain terrain stage ignores
them:

| Param | Type | Default | |
| --- | --- | --- | --- |
| base_elevation | float | 0 | The ground level this region meets its neighbours at. |
| edge_rise | float | 0 | Across the transition band the region stands at `base_elevation`, and its relief rises back over this many metres inside its own footprint — mountains climbing out of the plain instead of meeting it as an escarpment across the band. Only borders with another region fade; the union's outer edge does not. 0: off. |
| water | bool | false | Ground of this region below `water_level` is under water (a sea, a flooded wetland): the region terrain places water surfaces over it. |
| water_level | float | 0 | That water's height. |
| ponds | bool | false | This region's closed hollows fill with water to just below their rims. |
| pond_min_depth | float | 0.3 | A hollow shallower than this stays dry. |
| pond_min_area | float | 40 | A hollow smaller than this (m²) stays dry. |

### ground_edit (Terrain)

Painted edits to a finished terrain (phases W2 and W3): height layers
raise, lower and flatten the ground, water layers carve ponds, lakes and
marshes with the water standing in them, and rivers — painted splines, or
traced down the ground's own drainage — cut channels with flowing water in
them. It runs on the eroded heights of a
`terrain` or `region_terrain` stage, so a stroke never reruns erosion; the
painting side is in [36-terrain-paint.md](36-terrain-paint.md).

**Downstream stages name `ground_edit` as their `terrain`.** It is a
terrain source like the terrain stages: its output folder is a whole
terrain folder (terrain.yaml, heightmap.png, every mask), so
`terrain_material`, `region_material`, `scatter`, `grass_field`, `drift`,
`wall_sheet` and `vegetation` read the edited ground and its water. It also
takes over the terrain's placements: every tile (and backdrop piece) of its
source is superseded and placed again, a moved tile with a recooked mesh.
The pond, lake and marsh presets' recipes add a `ground_edit` and point the
document's terrain readers at it. A document without one is exactly what it
was.

- **Height layers** (`height`): each a paint layer of kind `height`, its
  metres times `scale` added to the ground. Paint them with add, erase,
  smooth, or **flatten**, which levels the ground toward the height under
  the brush where the stroke started (a terrace, a path's bed).
- **Water layers** (`water`): each connected painted area of a `water`
  layer (where it is at least `threshold`) is one **body**, carved as a
  basin:
  - the bed shelves from the outline: down to `shelf_depth` over `bank`
    metres, a ledge `shelf` wide, then down to `depth` in the middle, with
    `noise` on the bed (none at the waterline). A small body is shallower
    and its bank and shelf narrower;
  - outside the outline, ground standing higher than the water plus
    `bank_slope` rise per metre is cut down to it, out to `reach`: a pond
    painted on a slope gets a cut bank uphill rather than a cliff;
  - `hummocks` (a marsh) are bumps biased upward, `hummock_size` apart, so
    some break the surface as islands;
  - the water stands at the body's **spill height** — the lowest ground on
    its rim, less `freeboard` — so it never runs out; or at a level pinned
    in `bodies` (with a berm raised where that is above the rim);
  - the ground is only lowered inside the outline: a natural hollow deeper
    than the profile stays.
- **Bodies** (`bodies`): per-body settings keyed by a world point inside the
  body (`at`): a pinned `level` (unless `auto`) and a `material`. The body is
  whichever painted area holds the point, so repainting its edges keeps the
  setting; a point painted dry orphans the entry (the run's log and the
  Generator's water bodies panel say so). The panel writes these entries:
  Lock pins a body at its current level, keyed by its most inland point.
- **Styles** (`style`): `pond` and `lake` wear `water_lake`, `marsh`
  `water_swamp`, unless `material` names another water preset
  ([28-materials.md](28-materials.md), "Water").

The water goes through the same path as region_terrain's: per terrain tile
and material a flat mesh `pond_<i>_<j>_<material>` at the water's height,
carried two cells under the bank so the ground draws the shoreline, collision
none; the water mask (`mask_water.png`, the source's own merged in) that the
material's `wet` rule and grass_field's `shore` band and `max_depth` read;
and terrain.yaml's `water:` bodies (kind pond, lake or marsh). The stage also
writes `water_bodies.yaml`: every body's layer, style, anchor, level, spill
height, whether it is locked, material, painted and wet areas, and deepest
water.

What it rewrites: the heights (heightmap.png, over the source's range plus
16 m each way), the slope and curvature masks round what moved, and the
water mask. Flow, deposition, cliff, strata and the albedo are the source's
(hard-linked into its folder). **Incremental**: a stroke recooks only the
tiles whose samples moved (each recooked tile's file name carries a key of
its heights, so the editor's live update reloads it), reuses the water
meshes that did not change, and downstream stages rebuild only the pieces
whose heights or water moved (the stage's rect key; see "Reruns" in
36-terrain-paint.md).

**Rivers** (phase W3; the painting side is "Rivers and streams" in
36-terrain-paint.md). Rivers are carved after the height layers and the
basins, into the same grid, so everything above (quantisation, recooking,
rect keys) applies to them unchanged:

- **Hand rivers** (`rivers`): each a spline `path` with a `width` (bank to
  bank) and `depth` (the deepest point below the water) at every point. The
  River and Stream brushes add one per stroke; edit it in the 2D view (drag,
  click to add, right-click to delete, Shift+wheel over a point for its
  width). It may be drawn either way round: it is **turned to run
  downhill** (the ground under its first and last seventh compared).
- **Drainage rivers** (`drainage`): the ground is routed (a priority flood)
  and every sample whose drained area is at least `drainage_threshold` on
  the flow mask's scale (0 at 4 m², 1 at a quarter of a km²) is channel;
  the network is traced into rivers (main stems first, tributaries ending
  on them), shorter than `drainage_min_length` dropped, simplified,
  `drainage_width` and `drainage_depth` growing with the flow, a mild
  `drainage_meander`. The trace runs on the ground with the hand rivers
  already cut, and is cut where it meets a hand river's corridor and joined
  to it — so a hand river always wins. **Adopt** (the rivers panel) copies a
  traced river into a hand river (`from: adopted`).
- **The surface** never rises downstream: the ground along the channel's
  middle less `bank_height`, run through a running minimum and smoothed.
  Where a river ends on another (a **confluence**) its mouth is the other's
  surface there; where it runs into a pond or lake it ends at the water's
  level, and the run leaving the water starts at it (the river is split at
  the standing water: `<name>`, `<name>.2`).
- **Waterfalls**: a concentrated drop steeper than `max_slope` (rise per
  metre) and at least `min_fall` high is a step — the surface holds to the
  lip and drops to the foot — with a **plunge pool** dug below. A long steep
  stretch (more than three widths and twice its drop) stays a sloping,
  foamy **rapid** — or, with `cascade_step` set on the river, becomes a
  **cascade**: a staircase of small falls about that high, each with its own
  pool, sheet and mist (0, the default, keeps the rapid). Every fall is
  recorded (terrain.yaml `rivers: falls:` lip, foot, heading, width).
- **Waterfall sheets** (phase W4): over every fall the stage hangs the
  falling sheet, `falls_<river>_<n>`, wearing `falls_material` (default
  `water_falls`, [28-materials.md](28-materials.md) "Water"): from a short
  level lead-in over the lip, down a ballistic curve (thrown further out
  the faster the river runs over the lip, landing between an eighth and
  half the drop out), spreading a little, to just under the pool. Its
  vertex colours say where it is aerated (glassy at a small fall's lip,
  white by the foot), where its sides fray and how far down it is. The
  player walks through it. The ground under it is cut into a **notch**
  that keeps clear of the sheet across its width and a metre beside it, so
  the water never hangs over or through smooth ground.
- **Mist** (phase W4): `falls_mist` (default `waterfall_mist`, a shipped
  `.fpar`; empty for none) is placed as particle emitters across each
  fall's foot, one per three metres of sheet, their rate scaled by the drop
  (`mist_<river>_<n>_<k>`). They are spliced under the generator's prefix
  like its scenery (a level's `emitters:`), so a rerun replaces them.
  The mist drifts downwind (the `.fpar`'s `wind`, phase W5).
- **Sound** (phase W5): with `water_sounds` on (off by default; the River,
  Stream, Pond, Lake and Marsh brush presets turn it on) ground_edit places
  looping positional sounds, spliced into the level's `sounds:` under the
  generator's prefix like the mist: a roar at each fall's foot
  (`roar_<river>_<n>`, `water_falls`, louder and carrying further for a
  higher, wider fall; a cascade's steps share one), a sound every
  `water_sound_spacing` metres (default 24) along each river
  (`flow_<river>_<k>`: `water_rapids` at riffles and white water,
  `water_river` elsewhere, louder for wider and faster water), the rush at
  each confluence (`join_<n>`) and lapping along every pond, lake and marsh
  shore (`lap_<body>_<k>`, `water_lap`, at most eight a body). None is
  placed within 0.7 of the spacing of another (a fall keeps a wider berth),
  each carries a little past the spacing and is detuned a little by its
  name. Past its max distance a loop gives its voice back, so a long river
  costs nothing out of earshot. The clips ship generated
  ([Water](37-water.md)); a recording of the same name in a project
  replaces them. The run logs `water sounds: n (… flow, … rapids, … falls,
  … lapping)` and the splice `sounds -n +m`.
- **Plunge pools** foam in rings round where the sheet lands; a
  **confluence** foams round the tributary's mouth and trails a mixing line
  down the main river for five of its widths, fading. White water in the
  vertex foam is churned choppier and rougher by the water shader.
- **The channel**: within the banks a parabolic section down to the depth;
  **pools and riffles** alternate every `riffle_spacing` (default six
  widths, `pools` their strength: deeper and slow, shallower, fast and
  foamy); on a bend the deepest line swings to the outer bank and a
  shelving **point bar** forms inside. Outside, ground above the bank lip,
  a gentle `floodplain` and then `bank_slope` is cut down to it (faded at
  `reach`); where the ground beside the river is lower than the water a low
  levee holds it. `meander` (0 for a painted river: exactly the bends drawn)
  makes it wander.
- **Boulders**: with `river_rocks` naming a rocks or polyhaven_rocks stage,
  `boulders` per riffle per 4 m of width are dropped on the riffle crests
  (`boulder_<river>_<n>`), sunk a third into the bed; they throw foam round
  themselves and in a wake downstream. Every boulder (and every path stone)
  is listed in terrain.yaml's `obstacles:` (x, z, radius), and every
  `scatter` and `grass_field` over this ground keeps out of them, whatever
  its own `avoid` — no bush through a boulder, no blade through a stepping
  stone.
- **The water** is one ribbon per terrain tile and material,
  `river_<i>_<j>_<material>` (default `water_river`), on the terrain's own
  samples — no cracks between tiles or at confluences — running a ring of
  cells under the banks like the ponds. Its vertices carry the **flow map**
  (the current, in the vertex colour's green and blue) and the **foam**
  (red: riffles, rapids, boulders, confluences, a fall's lip and pool), which
  a material with `flow_map` reads ([28-materials.md](28-materials.md),
  "Water").
- **What it writes**: the rivers' wet samples go into the water mask (wet
  ground, the reeds' `shore` band and grass's `max_depth` follow rivers);
  the **flow mask** is raised along the channels (to 0.9 of the channel
  share); and five masks are added — `river_flow_x`, `river_flow_z` (the
  flow map: 0.5 + 0.5 v / 4 m/s), `river_foam`, `river_bed` (1 in the bed,
  fading over the banks) and `river_bar` (the point bars). A material's
  `paint` item can read any of them with `mask` (gravel on `river_bed`, sand
  on `river_bar`). terrain.yaml gains `rivers:` (rivers, falls,
  confluences) and the stage writes `rivers.yaml` (each river's kind, entry,
  material, length, source and mouth levels, what it flows into, and every
  traced river's points for Adopt).

The river parameters are **rect-covered**: editing a river reruns the stage
and recooks only the tiles the channel moved, and downstream pieces rebuild
only where the heights, the water or the river masks changed (and, for a
scatter or grass tile, where a boulder or stone it keeps out of moved).

**Paths** (phase P4; the painting side is "Paths" in 36-terrain-paint.md).
Laid after the rivers (so a path knows where the water is), each `paths`
item is a spline with a `width` per point and a `style`:

| style | stones | surface | wear | fords water |
| --- | --- | --- | --- | --- |
| rock_path | flagstones filling the width | 1 | 1 | yes |
| stepping_stones | a line a stride apart, zig-zagged | 0.45 | 0.7 | yes |
| dirt_trail | — | 1 | 1 | yes |
| gravel_path | — | 1 | 1 | yes |
| game_trail | — | 0.55 | 0.6 | no |

- **Flatten** (`flatten`, on by default; the path presets turn it off for
  stepping stones and game trails): the centreline's height is smoothed
  over `smooth` metres (reading only dry ground, so a ford keeps the banks'
  levels), and the cross-section is levelled to it, easing out over `blend`
  past the edge. `steps` cuts treads `step_rise` apart where the path is
  steeper than `max_grade`. Nothing in water or a river's channel is
  flattened, and the flatten fades out over the last two metres before
  water.
- **What it writes**, when at least one path is laid: nine masks —
  `path_<style>` for each style (the style's splat: 1 on the path, a worn
  edge that wanders by `edge` × the half width on a noise of `edge_size`
  metres; 0 under water), `path_wear` (1 along the middle, easing to 0
  `wear` metres past the edge; thins and shortens grass, tints the ground),
  `path_clear` (keeps large scatter off the path and a metre past it) and
  `path_dir_x` / `path_dir_z` (the tangent: 0.5 + 0.5 t × strength, what
  `orient_to` reads). Material `paint` items read the surfaces and the wear
  with `mask`; scatter and grass `exclude` entries read `path_clear` and
  `path_wear` with `mask`. terrain.yaml gains `footpaths:` and the stage
  writes `paths.yaml` (each path's style, entry, length, fords and stones).
- **Stones**, from `path_rocks` (or a path's own `rocks` for its flagstones
  or stepping stones): each is a rock variant scaled per axis to its stone
  (a flagstone 0.6–0.8 m, a stepping stone half a metre, 10–18 cm thick),
  turned along the path with a little jitter, tilted to the ground under it
  and sunk half its thickness, so none floats; no two overlap. Flagstones do
  not collide (the flattened bed is what a walker stands on). `stones` keeps
  a share of them, `stone_size` scales them, `stride` spaces stepping stones.
- **Fords**: where a path (not a game trail) crosses water, stepping stones
  carry on across it, standing on the bed with their tops 7 cm above the
  surface, bigger the deeper they stand; none in water deeper than 0.75 m.
  A path never carves across a channel. Bridges are not built.

`paths` is rect-covered, like `rivers`.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain or region_terrain stage whose finished heights are edited (it must cook its tiles). |
| height | list | [] | Height layers: `layer` (a height paint layer), `scale` (1). |
| water | list | [] | Water layers: `layer`, `style` (pond, lake, marsh), `material` (""), `threshold` (0.5), `bank` (3), `shelf_depth` (0.4), `shelf` (3), `depth` (2.2), `noise` (0.12), `noise_size` (7), `hummocks` (0), `hummock_size` (4), `bank_slope` (0.3), `reach` (14), `min_area` (6 m²). |
| bodies | list | [] | Per-body settings: `at` (a point inside the body), `level`, `auto` (false), `material` (""). |
| freeboard | float | 0.05 | How far below its rim an automatic level stands. |
| cook_memory | int | 2000 | How much memory the recooked tiles' cooks may hold at once. |
| rivers | list | [] | Hand rivers: `name` (""), `path` (spline of x, z, `width` 4, `depth` 1), `material` ("": water_river), `from` (painted, adopted), `bank_height` (0.35), `floodplain` (3), `bank_slope` (0.35), `reach` (10), `meander` (0), `meander_length` (40), `pools` (0.6), `riffle_spacing` (0: six widths), `max_slope` (0.35), `min_fall` (0.6), `speed` (0.9 m/s), `boulders` (0). |
| drainage | bool | false | Also trace rivers from the ground's drainage. |
| drainage_threshold | float | 0.55 | The flow value from which ground carries a river. |
| drainage_min_length | float | 40 | Traced rivers shorter than this are dropped (m). |
| drainage_width | vec2 | (1.5, 8) | Width at the threshold and at full flow (m). |
| drainage_depth | vec2 | (0.35, 1.4) | Depth at the threshold and at full flow (m). |
| drainage_meander | float | 1 | A mild wander for traced rivers (m). |
| drainage_boulders | float | 0 | Boulders per riffle per 4 m of width in traced rivers. |
| drainage_material | string | "" | Traced rivers' water material ("": water_river). |
| drainage_max | int | 0 | Keep at most this many traced rivers, largest first (0: all). |
| river_rocks | stage | "" | A rocks or polyhaven_rocks stage whose rocks are dropped as boulders in the riffles. |
| paths | list | [] | Footpaths: `name` (""), `style` (rock_path, stepping_stones, dirt_trail, gravel_path, game_trail; dirt_trail), `path` (spline of x, z, `width` 1.5), `flatten` (true), `blend` (1.2), `smooth` (6), `steps` (false), `step_rise` (0.18), `max_grade` (0.3), `edge` (0.3), `edge_size` (1.6), `wear` (1.2), `stones` (1), `stone_size` (1), `stride` (0.7), `rocks` ("": Path rocks). |
| path_rocks | stage | "" | A rocks or polyhaven_rocks stage whose rocks become the paths' flagstones, stepping stones and fords. |

```yaml
- stage: ground_edit
  id: ground_edit
  params:
    terrain: ground
    height: [{layer: terrace}]
    water:
      - {layer: pond, style: pond}
      - {layer: marsh, style: marsh, threshold: 0.4, bank: 2, shelf_depth: 0.15, shelf: 2, depth: 0.4,
         hummocks: 0.6, hummock_size: 4, bank_slope: 0.2, reach: 8}
    bodies:
      - {at: {x: -60, "y": 40}, level: 0.4}            # the pond held 0.4 m up
    rivers:
      - name: brook                                     # what a River brush stroke writes
        path:
          - {x: -120, z: 30, width: 6, depth: 1.3}
          - {x: -40, z: 10, width: 7, depth: 1.3}
          - {x: 120, z: -5, width: 8, depth: 1.3}
        pools: 0.65
        boulders: 1
    drainage: true                                      # and the streams the ground drains into it
    drainage_threshold: 0.6
    river_rocks: river_rocks_src
    paths:
      - name: rock_path_1                               # what a Rock path brush stroke writes
        style: rock_path
        path:
          - {x: -24, z: 4, width: 2.2}
          - {x: -33, z: 13, width: 2.2}
          - {x: -58, z: 38, width: 2.2}
        rocks: flag_rocks_src                           # flat flagstones (rock_09)
    path_rocks: river_rocks_src                         # the fords' stones
- stage: terrain_material
  id: ground_mat
  params: {terrain: ground_edit, ...}                   # reads the edited ground
```

### rocks (Rocks)

A family of weathered rock **variants** of one form, each cooked to its own
Nanite mesh with its colour baked in, and a manifest (`rocks.yaml`) that the
placement stages place them from. Seven forms, modelled on the sandstone of the
Colorado Plateau and the granite of Joshua Tree:

| preset | size | triangles | what it is |
| --- | --- | --- | --- |
| `boulder` | 1.6 m long | 15k | a block broken out of a bed along a dozen fracture faces, its edges rounded by weathering, a few spalls and pits; sandstone keeps its bedding, tilted as it landed; granite is rounder |
| `cliff_chunk` | 8 m high | 600k | a section of a sandstone wall: hard beds standing over soft ones that weather back under them (undercuts, alcoves), broken-rock relief at several scales, slab scars, a vertical joint set, flutes. Its **back** (−Z) and **base** are flat, so `cliff_wall` can stack chunks against a wall |
| `mesa_cap` | 10 m wide | 300k | caprock: a thick hard bed over a soft one weathered back beneath it, the rim spalled and broken into blocks, the top flat and pitted |
| `ledge_slab` | 4 m long | 60k | one bed broken out along its joints: flat top and bottom, a polygonal outline, bedding lines on the edges |
| `pillar` | 7 m high | 300k | a hoodoo: soft beds necked in between harder ones, a broken caprock, a flared pedestal |
| `rubble` | 0.4 m | 3k | an angular talus fragment |
| `pebble` | 8 cm | 800 | a stream-worn pebble |

Every variant is a different seed; `size`, `triangles`, `strata_thickness`,
`joint_spacing` and `ao_radius` at 0 take the preset's. **Keep scattered rocks
light** (the defaults: 15k triangles and under) — every placement costs its
mesh's clusters — and give hero pieces the triangles (a cliff chunk's 600k).

**The colour.** There is no texture yet (the material phases bring them): a
rock's look is its **vertex colour**, baked from the shape itself — each bed its
own mix of the palette's colours (soft beds darker and redder), bleached and
stained patches, a shade per joint block, darker crevices and paler worn edges
(`cavity`), desert varnish streaking down steep faces (`varnish`), dust on the
tops (`dust`), and **ambient occlusion** (`ao`) in the colour's alpha, which the
shader takes off the ambient light and part of the sun. With no `texture` the
vertex colour is the whole albedo; with one, it multiplies the texture (box
mapped, `uv_scale` repeats a metre). Palettes: `auto` (Entrada red for
sandstone, grey for granite), `entrada`, `navajo` (cream), `wingate` (deep
red-brown), `kayenta` (purple-red), `granite_grey`, `granite_pink`, `custom`
(`color` and `band_color`).

**Collision.** `auto` gives a variant under `hull_size` (4 m) convex **hulls** —
what a body rolls off and a grenade bounces from — and a bigger one its
**mesh** (cooked without the hull decomposition, which it would never use);
pebbles get none. Each variant's choice is in the manifest for the placement
stages to use.

**The preview** makes one variant (`preview_variant`), at no more than
`preview_triangles`, and shows it on a **turntable** in the 3D view (untick
**Spin** to stop it), with its colours, its triangle count and where the time
went (meshing, decimation, bake) in the notes; the 2D view shows its footprint.
The two preview parameters change only the preview: picking another variant to
look at never makes the stage stale. **gallery** places every variant in a row
along +X from `gallery_origin` in the target level, for looking at them in the
game.

Start one from the **Rock variants** template (File > New, or `foundry gen new
<name> -template rocks_demo`): boulders, a cliff chunk, pebbles.

| Param | Type | Default | |
| --- | --- | --- | --- |
| preset | enum | boulder | The rock form: boulder — a weathered block broken out of a bed: fracture faces, rounded edges, spalls (1.6 m length, 15000 triangles); cliff_chunk — a section of cliff: undercut beds, joints, flutes, a flat back to stack against a wall (8 m height, 600000 triangles); mesa_cap — caprock over a weathered-back soft bed, its rim broken into blocks (10 m width, 300000 triangles); ledge_slab — a slab of one bed with a broken outline: a ledge or a fallen flagstone (4 m length, 60000 triangles); pillar — a hoodoo: necked beds under a wide caprock, on a flared pedestal (7 m height, 300000 triangles); rubble — an angular talus fragment (0.4 m length, 3000 triangles); pebble — a stream-worn pebble (0.08 m length, 800 triangles); |
| variants | int | 6 | How many variants: each a different seed, cooked to its own mesh. |
| seed | seed | 0 | Changes every variant without changing anything else. |
| size | float | 0 | The preset's main dimension (a boulder's length, a cliff chunk's or hoodoo's height, a mesa cap's width). 0 is the preset's. |
| size_jitter | float | 0.2 | How much the size varies from variant to variant (±, as a fraction; at most 0.5). |
| triangles | int | 0 | The triangle budget of each variant before the cook (the cook's LODs go down from it). 0 is the preset's: light for scattered rocks (≤ 15k), dense for hero pieces (600k for a cliff chunk). Scattered rocks must stay light: every placement costs its mesh's clusters. |
| lithology | enum | sandstone | Sandstone keeps its beds (strata, laminations, cross-bedding); granite has none and weathers rounder, with a speckled mottle. |
| palette | enum | auto | The colours: auto (Entrada red for sandstone, grey for granite), formations of the Colorado Plateau, granite, or custom (the two colours below). |
| color | color | (0.7,0.37,0.23) | The custom palette's base colour. |
| band_color | color | (0.84,0.62,0.45) | The custom palette's band colour, which beds mix toward. |
| color_variation | float | 0.6 | How far each bed (and each joint block) strays from the base colour. |
| fracture | float | 0.5 | Broken faces and open joints: a boulder's fracture planes, a cliff's joint sets and blocks. |
| joint_spacing | float | 0 | The joint network's spacing. 0 is the preset's, from the size. |
| weathering | float | 0.5 | How rounded the edges and corners are (convex places weather first). |
| roughness | float | 0.5 | Grooves, lumps and grain on the faces. |
| chips | float | 0.5 | Spalls knocked off the edges. |
| strata_thickness | float | 0 | The mean bed thickness. 0 is the preset's, from the size. |
| strata_relief | float | 0.6 | How far soft beds weather back behind the hard ones. |
| undercut | float | 0.6 | How a soft bed under a hard one recedes: flush at its base, deepest under the overhang. |
| cavity | float | 0.6 | Darker crevices, paler worn edges (baked into the colour). |
| ao | float | 0.8 | The baked occlusion's strength (the colour's alpha, which the shader takes off the ambient light). |
| ao_radius | float | 0 | How far the occlusion looks. 0 is the preset's, from the size. |
| varnish | float | 0.25 | Desert varnish: dark streaks down steep faces, heavier under overhangs. |
| dust | float | 0.3 | Dust settled on the up-facing faces. |
| sharpness | float | -1 | How sharp the mesher keeps edges: 0 rounds every vertex to its cell's centroid, 1 keeps creases exact. −1 is the preset's. |
| texture | texture | "" | A texture under the vertex colour (box-mapped). None: the vertex colour is the albedo. |
| uv_scale | float | 0.5 | Texture repeats per metre of the box mapping. |
| collision | enum | auto | How the variants collide: hulls (a convex decomposition, for rocks a body rolls off), mesh (the mesh itself; the cook skips the decomposition), none. Auto: hulls up to the hull size, mesh above, none for pebbles. |
| hull_size | float | 4 | Auto collision's limit: a variant whose largest extent is under it gets hulls. |
| cook | bool | true | Cook each variant to a Nanite mesh (.fnm). Off writes .glb files instead. |
| cook_memory | int | 3000 | Variants are made and cooked side by side only as far as this budget allows. |
| gallery | bool | false | Also place every variant in a row in the target level, for looking at them (the placement stages place them for real). |
| gallery_origin | vec3 | (0,0,0) | Where the gallery row starts; it runs along +X. |
| preview_variant | int | 0 | Which variant the turntable shows (the preview only: not part of the run). |
| preview_triangles | int | 30000 | The most triangles the preview makes the variant with (the run uses Triangles). |

**Outputs**, in the stage's folder: `<preset>_<nn>.fnm` per variant (`.glb`
with `cook` off), and `rocks.yaml`:

```yaml
version: 1
preset: boulder
lithology: sandstone
palette: auto
frame: "+Y up; lowest point at y=0; footprint centred on the Y axis; ..."
variants:
- name: boulder_00            # also the gallery placement's ID suffix
  file: boulder_00.fnm
  seed: 1234567
  size: 1.71                  # the preset's dimension, as it came out
  min: [-0.85, 0, -0.71]      # bounds, in the placement frame
  max: [0.85, 1.08, 0.71]
  extent: [1.7, 1.08, 1.42]
  height: 1.08
  radius: 0.95                # bounding cylinder about +Y
  footprint_radius: 0.81      # what touches the ground (within base_band of it)
  base_band: 0.13
  sphere_center: [0, 0.54, 0]
  sphere_radius: 1.2
  top: 1.04                   # the upper surface's height (ledges, the next tier)
  flat_base: false
  volume: 1.05                # m³
  triangles: 15000
  vertices: 8123
  collision: hulls            # what the cook built
  place_collision: hulls      # what a placement should ask for (hulls, mesh, none)
  mesh_ms: 1520.3             # and simplify_ms, bake_ms, cook_ms
  mesh_triangles: 33000
  cell: 0.022
```

Every variant is in the same **placement frame**: +Y up, its lowest point on
y = 0 (placed at a ground point it sits on it; move it down to sink it), its
footprint centred on the Y axis. A cliff chunk (`back: [0, 0, -1]`) has its
flat back on the plane z = 0 and stands along +Z: place it on a wall with +Z
turned out of the wall.

### scatter (Placement)

Rocks scattered over a terrain by a list of **rules**: talus at the feet of
cliffs, boulder fields, rubble round the boulders, pebbles in the washes. Each
rule places the variants of one rock source: a `rocks` stage, or any stage
whose product is a rocks manifest (the downloaded-scan stage to come writes the
same format). Start from the **Canyon with placed rocks** template (`foundry
gen new <name> -template placement_demo`), which wires a terrain, four rocks
stages, a cliff wall, four scatter rules and desert plants together.

How a rule places its rocks:

1. **Where.** Every point of the ground gets a weight from 0 to 1: the product
   of soft bands on the terrain's masks — `slope` (degrees), `height` (a
   fraction of the terrain's range), `flow`, `deposition`, `curvature`,
   `cliff` — and two terms of its own. `cliff_base` draws the rule to the
   **foot** of cliffs, within `cliff_base_distance` metres below one (not their
   rims); `cluster` gathers it into patches about `cluster_scale` across, with
   bare ground between, which is what keeps a field from looking sprinkled.
2. **How many.** `density` is candidates per 100 m² at full weight. Each
   candidate draws its variant, its size (a power law: `scale_bias` 2–3 gives
   many small rocks and a few big ones), its yaw, tilt and sink. `satellites`
   adds candidates round every rock the **earlier** rules placed: rubble
   skirting boulders.
3. **Spacing.** Candidates are taken biggest first and kept only if their
   footprint keeps clear of every rock already placed — this rule's, the
   earlier rules', and the `avoid` stage's (a cliff wall's bottom tier) — by
   `spacing` times the sum of their footprint radii. **List the big rocks
   first**: each rule fills in round what the ones above it placed.
4. **Seating.** A rock is turned toward the ground's slope by `align`
   (averaged over its footprint), tilted up to `tilt`, yawed at random, then
   lowered until no part of its base floats and sunk a further `sink` of its
   height. A candidate whose ground rises more than `max_bury` of its height
   across its footprint (a ledge edge, a gully) is dropped.
5. **Collision by size.** Rocks under `collision_min` collide as nothing,
   rocks up to `hull_max` as their convex hulls (when their cook made them),
   bigger ones as their mesh.

**Keep dense rules light.** Every placement costs the Nanite cull its mesh's
leaf windows: about 3 for a 600-triangle stone, 210 for a 120k-triangle cliff
chunk, about a thousand for a scanned hero rock. `max_triangles` places only a
source's variants under that many triangles (or its lightest one). The
template's boulders are 2,500 triangles and its rubble 600.

**The preview** runs the same rules over the terrain's preview, each rock
source stood in for by the variant its own preview makes. The 2D view draws a
dot per rock in its rule's colour (white picks one per rule), each with its
footprint once it is a few pixels across; the 3D view draws every rule's rocks
as one decimated proxy (`preview_triangles`), instanced, so ten thousand rocks
are one draw. `preview_max` caps a rule in the preview only.

Each run writes `footprints.yaml` (every rock's footprint disc and each rule's
counts) beside its placements, which a later stage's `avoid` reads.

**Paint** ([Terrain paint](36-terrain-paint.md)). Every rule has a paint band:
`paint` names a paint layer, `paint_band` (0–1 by default) ramps its painted
value into the rule's weight (0 at or below the first number, 1 at or above the
second), and `paint_invert` flips it. The stage's `exclude` list names layers
that keep every rule clear, each multiplying the weight by 1 − strength × its
painted value (strength 1 by default); an entry may read a terrain `mask`
instead, such as a ground_edit's `path_clear`, which keeps rocks and logs off
its paths (a terrain without the mask reads 0). All of it is off until a layer
is named, and multiplies the other bands, so painted rocks still keep off
cliffs.

**Orientation** (phase P4). A rule's `orient_to` turns its rocks along a
direction field instead of at random: a direction paint layer (with the
terrain's paths laid over it), `paths` (a ground_edit's path tangents) or
`downhill` (down the slope, full strength from a 6% grade), with
`orient_jitter` (12°) and `orient_offset` (0°). A rock's long side (its
footprint's longer axis) follows the field; where the field is weak the yaw
blends back toward the random one. Positions are unchanged.

**Tiled when it reads paint.** A scatter that reads a layer (its own band or
exclude list, or a stage it builds on, such as a painted `ground`) keeps its
rocks apart tile by tile, in 64 m world tiles, so a stroke re-scatters only
the tiles near it. Each tile's rocks are cached in `tiles/t_<i>_<j>.bin` and
numbered per tile, `<rule>_<i>_<j>_<nnnn>`. Rocks land differently from the
classic whole-ground scatter, which every scatter reading no paint still
uses. A footprint reaching half a tile or more cannot be tiled: that scatter
reruns whole, and the log says why.

#### Draw distances and impostors

In a level generation doc's regions (the environment presets' scatters) every
placement is written with a `draw_distance`, a `shadow_distance` and, for a
tree, an `impostor` (see [Scenery](14-scenery-brush.md#draw-distance-and-impostors)),
all from the placement's own size: its bounding sphere's radius r, scaled.

| | Automatic value |
| --- | --- |
| draw distance | 220·r m, 120–1500 m; a tree with an impostor 800–1500 m |
| shadow distance | half the draw distance; a tree with an impostor 0.6 of it |
| impostor | a Poly Haven plant variant 3 m tall or more at the rule's largest scale |
| impostor distance | 25·r m, 80–300 m |

A rule's own `draw_distance`, `shadow_distance`, `impostor` and
`impostor_distance` override them. A classic document's rules write nothing
unless they set these (its output is pinned to the byte, so an existing level
regenerates exactly as before). The new keys are at their defaults in every
existing document, so no scatter goes stale for them; to give an existing
region world its draw distances, force its scatters:
`foundry gen run <doc> -stage r_<region>_scatter …` (one `-stage` a region).

An impostor is baked once per cooked variant (on the CPU, headless: about a
second for a 50k-triangle fir), beside it as `<stem>_imp.fnm` with
`<stem>_imp.yaml` (what it was baked from), its atlas
`textures/ph_imp_<stem>_cov.png` (a coverage-preserving cutout, so distant
crowns keep their density) and that atlas's `_normal` map. A rerun bakes again
only when the variant's cook changed.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage to scatter over: its heights and masks. |
| seed | seed | 0 | Changes every rule's rocks without changing anything else. |
| rules | list | 0 items | The rules, run in order: each keeps clear of what the earlier ones placed, so list the big rocks first. |
| rules.name | string | rocks | The rule's name: its placements' IDs start with it. |
| rules.enabled | bool | true | Off skips the rule. |
| rules.rocks | stage | "" | The rocks stage (generated rocks, Poly Haven rock or plant scans — any stage whose product is a rocks manifest) whose variants this rule places, picked at random per rock. For plants keep Align and Tilt at 0 (trees grow upright) and raise Spacing: a plant's footprint is its trunk or stems. |
| rules.max_triangles | int | 0 | Place only the rocks stage's variants of at most this many triangles (0: all; none that light: its lightest). Every placement costs the cull its mesh's leaf tiles, so a dense rule wants light variants — a scanned boulder can be millions. |
| rules.color | color | (1,1,1) | The rule's dots in the 2D view. White picks a colour per rule. |
| rules.density | float | 1 | Candidates per 100 m² of ground at full weight. Spacing then thins them where they crowd. |
| rules.spacing | float | 1 | How far apart footprints keep: centres at least this times the sum of their radii (1: just touching). |
| rules.scale | vec2 | (0.6,1.6) | The scale range, on top of the variants' own sizes. |
| rules.scale_bias | float | 2.2 | The power law's exponent: 0 draws scales evenly, 2–3 many small and few big, as real rock fields are. |
| rules.slope | vec2 | (0,40) | The ground slopes it grows on, degrees (from, to). |
| rules.slope_soft | float | 6 | How many degrees beyond the slope band the weight fades out over. |
| rules.height | vec2 | (0,1) | The heights it grows at, as a fraction of the terrain's range (0 its lowest, 1 its highest). |
| rules.flow | vec2 | (0,1) | The flow mask's band: 0 is dry ground, toward 1 washes and canyon floors that carry water. |
| rules.deposition | vec2 | (0,1) | The deposition mask's band: toward 1 where sediment settled (fans, talus, wash floors). |
| rules.curvature | vec2 | (0,1) | The curvature mask's band: 0.5 is flat, above it hollows, below it crests. |
| rules.cliff | vec2 | (0,0.6) | The cliff mask's band: keep it low to stay off the walls themselves. |
| rules.soft | float | 0.1 | How far beyond each 0..1 mask band the weight fades out over. |
| rules.cliff_base | float | 0 | How strongly it keeps to the FOOT of cliffs — talus under a wall: 0 ignores cliffs, 1 grows only within the distance below one. |
| rules.cliff_base_distance | float | 25 | How far out from a cliff's foot the cliff-base weight reaches. |
| rules.cluster | float | 0.5 | Clumping: 0 spreads it evenly over the ground its bands allow, 1 gathers it in patches with bare ground between. |
| rules.cluster_scale | float | 25 | The patches' size. |
| rules.near | spline | 0 points | Keep the rule to an area: within each point's `radius` of the path through the points (one point: a disc) — rubble along a slot canyon's floor, plants down a stairway. Empty: the whole ground. |
| rules.near_soft | float | 4 | How far past the area's radius the rule fades out. |
| rules.satellites | float | 0 | Extra candidates round each rock the EARLIER rules placed, per metre of its footprint radius: rubble skirting the boulders. |
| rules.satellite_spread | float | 2.5 | How far out the satellites reach, in the host's footprint radii. |
| rules.align | float | 0.6 | How far each rock is turned toward the ground's normal (averaged over its footprint): 0 upright, 1 flush with the slope. |
| rules.tilt | float | 12 | A random tilt on top, up to this many degrees about a random horizontal axis (every rock is yawed at random too). |
| rules.sink | float | 0.1 | How far each rock sits in the ground, as a fraction of its height (±40% per rock; less for a flat-based variant). Before the sink it is lowered until no part of its base floats. |
| rules.max_bury | float | 0.6 | A candidate whose ground rises more than this fraction of its height across its footprint is dropped (a ledge edge, a gully). |
| rules.collision | enum | auto | Auto by size: none under Collision min, hulls up to Hull max (when the variant was cooked with hulls), the mesh above. |
| rules.collision_min | float | 0.35 | Rocks smaller than this (largest extent, scaled) do not collide: a body would only snag on them. |
| rules.hull_max | float | 4 | Rocks up to this size collide as their hulls; larger ones as their mesh. |
| rules.max_instances | int | 20000 | The most this rule places (0: no cap). The biggest are kept. |
| rules.draw_distance | float | 0 | How far from the camera the rule's placements are drawn (fading out over the last tenth). 0: by size in a region world (about 220 m a metre of bounding radius, 120–1500 m; trees with impostors at least 800 m), unlimited in a classic document. Negative: no limit. |
| rules.shadow_distance | float | 0 | How far they cast a shadow. 0: half the draw distance (a tree's impostor 0.6 of it). Negative: as far as drawn. |
| rules.impostor | enum | auto | Far impostors baked from each cooked variant. auto: a region world's Poly Haven plant trees (3 m or taller at the rule's largest scale); on: every variant; off: none. |
| rules.impostor_distance | float | 0 | Where a placement's mesh hands over to its impostor. 0: by size, 80–300 m. |
| avoid | stage | "" | A cliff_wall (or another scatter) stage whose footprints these rocks keep clear of. |
| cliff_threshold | float | 0.5 | The cliff mask level that counts as a cliff for the cliff-base weight. |
| margin | float | 2 | Footprints keep this far in from the terrain's edge. |
| preview_max | int | 20000 | The most rocks a rule places in the preview (the run uses Max rocks). |
| preview_triangles | int | 240 | The 3D preview draws each rule's rocks as one proxy of its previewed variant, decimated to this many triangles. |

### grass_field (Placement)

Poly Haven grass, flowers, ferns and reeds as a **dense sward drawn by the
grass instancer** — hundreds of thousands of clumps per square kilometre —
instead of one scenery placement per clump (which a `scatter` rule makes, at
about 1 ms of CPU frame per thousand in view). Each **rule** names a
`polyhaven_plants` stage (`plants`) and grows its clumps where its bands
allow; every enabled rule grows at once (unlike scatter's, they do not keep
clear of each other: a meadow is grass and flowers mixed). The environment
presets' grass is all on this stage (`sward`).

How a rule places its clumps: a **jittered lattice** at `density` clumps per
m², counted from the world origin, each point kept with probability equal to
its weight — the scatter bands' (`slope`, `height`, `flow`, `deposition`,
`curvature`, `cliff`, `cliff_base`, `cluster`), times the region's share in a
region world, times the optional `shore` band (metres from standing water;
reeds round a pond are `shore: [0, 6]`). No clump stands deeper in water than
`max_depth`. Its scale is drawn from `scale` and shortened where its weight
is low (`edge_shrink`: a patch's margin is shorter, sparser grass); its tint
mixes `tint_a` and `tint_b` by a slow noise `tint_scale` metres across, with
`tint_jitter` per clump. `avoid` (a scatter stage) keeps grass out of its
rocks' footprints (taken at `avoid_shrink`).

**Which clumps.** Every variant of the plants stage is a clump kind:
`pieces` keeps only those whose names contain one of its words (`tall`
takes grass_medium_01's tall_a/b/c), `min_size` drops the small ones. Poly
Haven's grass pieces are about a quarter of life size as scanned: a 1–2 m
tallgrass sward is the tall pieces at `scale: [4.4, 6.4]`.

**What it writes.** `grass.fgk`, the kit: each kind as up to three meshes —
as imported (capped at `max_triangles`), then thinned to `lod_ratios` of that
by keeping whole blades, each widened to keep the clump's cover — with its
`_cov` albedo and its wind (`wind_bend`, `wind_flutter`). One
`grass_<i>_<j>.fgrass` per terrain tile with any clump: 12 bytes a clump,
sorted into `cell`-metre cells by kind in a fixed hashed order. And one level
**volume** per field with a `grass:` block naming it (see Volumes), boxed to
the tile, `chunk: auto`, owned by the stage's prefix like every placement.
`grass.yaml` sums it up (clumps per rule, kinds).

**How it draws.** Per cell on the CPU: cells out of the frustum or past the
draw range are skipped; each kind's run draws the prefix its nearest point
admits, thinning past ~22 m in that fixed order (instances fade over a narrow
band of rank — no popping wave) while the survivors grow to keep the ground
covered; the mid and far meshes take over at ~16 m and ~45 m, cross-faded per
clump. Wind is the scene's (the same gust the trees bend in), shadows only
within the player's grass shadow distance and only for rules with `shadows`
on, alpha-to-coverage (or TAA's dither) on the cutout. `range` stops a rule's
clumps short of the full range (flowers). The player's `grass_density`,
`grass_distance` and `grass_shadow_distance` (graphics.yaml, or
`@level.prop.<name>`) scale all of it; the F3 profiler's `grass` row is its
cost.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage to grow on: its heights, masks, tiles and water. |
| seed | seed | 0 | Changes every rule's clumps. |
| rules | list | — | The rules (below). |
| avoid | stage | "" | A scatter (or cliff_wall) stage whose footprints the grass keeps out of. |
| avoid_shrink | float | 0.7 | Each avoided footprint's radius is taken at this fraction. |
| cell | float | 16 | The culling cell (metres). |
| lod_ratios | vec2 | 0.35, 0.1 | The mid and far meshes' share of the near mesh's triangles (0: none). |
| margin | float | 0.5 | Clumps keep this far in from the terrain's edge. |
| preview_density | float | 0.05 | The preview's dots per m² at most. |

Rule fields: `name`, `enabled`, `plants`, `pieces`, `min_size`,
`max_triangles` (1200), `density` (1.5/m²), `scale` (3–5), `edge_shrink`
(0.4), the scatter bands (with `cluster` 0.25 over 30 m and `cliff` 0–0.3 by
default), `shore`, `shore_soft`, `max_depth`, `tint_a`, `tint_b`,
`tint_scale`, `tint_jitter`, `wind_bend` (0.25), `wind_flutter` (0.04 m),
`range` (1) and `shadows` (on), and the paint band `paint`, `paint_band`,
`paint_invert` (scatter's, above), and `orient_to`, `orient_jitter` and
`orient_offset` (scatter's, above: a piece's length is taken along its Z, and
a kind clearly longer along X gets a quarter turn — twigs lying downhill,
grass combed along a path). The stage-level `exclude` list keeps every
rule out of the painted ground of the layers it names: a path through a
meadow ([Terrain paint](36-terrain-paint.md)). Its entries may read a
terrain `mask` with a `strength`, and on grass a `shrink`: `{mask: path_wear,
shrink: 0.6}` is a path's wear — no grass on its middle, thinner and shorter
grass in a band either side. A stroke rewrites only the
fields of the tiles it reaches; the rest are kept as they were (`reused N of
M tiles` in the log).

The preview draws each rule's clumps as dots, thinned to `preview_density`
(a preview never imports, so it has no kit).

**Ground cover.** The same rules draw leaf litter, twigs, pine needles,
moss and pebbles: dense flat clutter that would cost far too much as scenery.
A few more rule fields, all off by default (a rule that sets none of them
places, writes and draws exactly as before):

| Field | Default | |
| --- | --- | --- |
| `orient` | upright | `ground` lays each clump along the terrain's normal under it, at its random turn about that normal: a twig lies down a bank, a pebble sits on it |
| `wind` | true | off keeps the clumps still (branches, stones, litter) |
| `tilt_jitter` | 0 | leans each clump at random by up to this many degrees (a jumble of twigs, stones not all sitting square) |
| `sink` | 0 | pushes each clump into the ground by this fraction of its height: pebbles half buried, a branch bedded in the litter |
| `card_textures`, `card` | "" | make the rule **cards**: flat alpha-cut squares lying on the ground, cut out of layer `card` of the `polyhaven_textures` stage `card_textures` |
| `card_cut` | 0.55 | what of the scan a card keeps: the texels above this height (0..1 of the scan's own range) |
| `card_tiles` | 2 | the cut-out is split into this many tiles a side; each is a card kind (1 m at scale 1) showing one tile |

A **card** is how litter is drawn. Poly Haven publishes leaf litter as ground
textures (`forest_leaves_04`, `dry_decay_leaves`, `winter_leaves`), not as
cut-out leaves, but every set has a displacement map and on a litter scan the
leaves are what stands up. Keeping the texels above `card_cut` keeps the
scan's own leaves and drops the soil between them. The cut-out is written once
as `textures/ph_<id>_card<cut>x<tiles>.png` and shared. Unlike a grass
cutout its mips do not keep their coverage: far off, a card dissolves into the
splat under it. Laid over the
same texture as a splat, the cards add a layer of real leaves that catch the
light. A card always lies along the ground, never sways and casts no shadow.
It is drawn with a small depth bias toward the camera, so it never fights the
terrain under it.

`plants` may name a `polyhaven_rocks` stage as well as a `polyhaven_plants`
one (pebbles). A piece of only a few connected parts (a pebble, a branch) is
**solid**: its distant meshes are the scan decimated to `max_triangles` and
the LOD ratios, not thinned by whole blades, which cannot shorten one piece.

A kind laid down, leaned or kept still is stored per instance with its up
axis: such a stage writes **oriented** fields (version 2, 16 bytes a clump).
A stage without any of these options writes the version-1, 12-byte fields it
always did, and an old field still loads. The brush presets
([Terrain paint](36-terrain-paint.md#brush-presets)) carry ready-made ground
cover rules.

### cliff_wall (Placement)

Cliff chunks (a `rocks` stage of the `cliff_chunk` preset, or any rocks
manifest whose variants have a back) stacked in tiers up a terrain's walls:
faces out of the wall, backs seated into the heightfield's stretched cliff
faces so the chunks cover them, ledge slabs between the tiers and capping the
rims. This is what gives a canyon vertical and overhanging rock a heightfield
cannot.

- **Where the walls run** (`source`): `traced` follows the **feet** of every
  cliff band of the terrain's cliff mask (its outline, averaged onto a
  `trace_cell` grid, kept where it faces downhill, longer than `min_length`,
  inside `region_min`..`region_max` when set); `splines` follows the `paths`
  you draw in the 2D view along a wall's foot, each facing `side` (auto:
  downhill); `both`.
- **Measuring a wall.** At each column a transect is taken across the wall:
  it must reach `wall_slope` somewhere (gentler banks are talus country, and
  chunks against them read as a facade in front of a hill); the foot is where
  it eases below `foot_slope` going out, the rim below `rim_slope` going in.
  Walls lower than `min_wall` metres get nothing.
- **Tiers.** A column takes as many tiers as fit its wall at the chunks' size
  with `overlap` between them (at most `tiers`), and the scale (within
  `scale`) that makes them span foot to rim. Odd tiers are shifted half a
  column along the wall, and every chunk gets its own variant, `yaw_jitter`,
  `roll_jitter`, `along_jitter`, `drop_jitter` and `scale_jitter`.
- **Seating.** Each chunk leans back with the face (`lean`, at most
  `max_lean`); its back is buried at least `inset` behind the face over its
  whole height and its front stands `protrude` of its depth proud of it, read
  at three points across its width. A scanned cliff's back is not a plane: its
  bounding box's face across the manifest's back direction stands for it, and
  the inset buries the rest.
- **Ledges.** With `ledges` set, a ledge slab may jut from a tier's top
  (`ledge_chance`) or cap a rim (`rim_ledge`), about `ledge_scale` of the
  chunk's width long.
- **Budget.** Hero chunks are dense, so `max_chunks` caps the set, and the
  walls are lined in `stretch`-metre stretches, the **tallest first**, each
  whole: a capped set covers the big stretched faces rather than dotting
  columns about. Two lines' chunks never cross (a narrow slot's two feet).

The bottom tier's footprints go to `footprints.yaml`, for a scatter's `avoid`.
The preview shows the traced feet, a dot per chunk and ledge, and the chunks
as one instanced proxy of the previewed chunk.

**What stays off the walls, and how a column stacks** (phase 7):

- **Pillars and fins** (`backing`): a wall with less rock behind its rim than
  `backing` chunk widths — the flank of a pillar, a thin fin between two
  canyons — gets no chunks. Stacked on it, they hang off its sides.
- **Hanging seats** (`min_contact`, `contact_gap`): a chunk whose back
  touches the face over less than `min_contact` of its back (buried in it, or
  within `contact_gap` of it) is tried as another variant and the tier is
  left empty if none fits: such a piece is cantilevered off the wall with
  daylight behind it. The tiers above are still tried.
- **Sizes** (`height_norm`): each variant is brought toward the variants'
  mean height, so a 40 m scan beside a 5 m one does not come out eight times
  as big; and the column spacing uses the widths as evened.
- **Pitch** (`tier_jitter`): each tier starts at the top of the chunk below
  it less a jittered overlap, so the joints up a column do not run at one
  fixed step like courses of brick.
- **Budget** (`focus`, `focus_range`): with a cap, stretches are ranked by
  their height times how near they are to `focus` (the spawn, say), so the
  walls you see first are the ones clad.
- **Colour** (`match_to`, `match_brightness`): `rock` matches the chunks to
  the terrain material's rock layer — the bare walls around them — rather than
  the ground's average; `match_brightness` darkens pale scans to sit with it.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage whose walls are lined. |
| chunks | stage | "" | The cliff chunks to stack: a rocks stage of the cliff_chunk preset, or any stage whose rocks manifest gives its variants a back (a scanned cliff's back need not be flat: its box's face across the back direction is seated, buried by Inset). |
| ledges | stage | "" | Ledge slabs (a rocks stage of the ledge_slab preset, or any rocks manifest) for ledges between tiers and on rims; none for no ledges. |
| seed | seed | 0 | Changes every chunk's variant and jitter. |
| source | enum | traced | Where the walls run: traced along the feet of the cliff mask's bands, along the splines below, or both. |
| paths | list | 0 items | Authored wall lines (source splines or both). |
| paths.path | spline | 0 points | A wall's line, drawn along its foot in the 2D view. |
| paths.side | enum | auto | Which side of the line the wall faces (as the line is drawn): auto faces downhill. |
| region_min | vec2 | (0,0) | Traced walls only inside this XZ rectangle (with Region max); both zero for the whole terrain. |
| region_max | vec2 | (0,0) | The region's other corner. |
| cliff_threshold | float | 0.5 | The cliff mask level the traced feet follow. |
| trace_cell | float | 2 | The grid the cliff mask is averaged onto before tracing: coarser follows the walls' broad lines, finer every notch. |
| min_length | float | 24 | Traced feet shorter than this are dropped (boulder-sized steep spots). |
| smooth | int | 3 | Smoothing passes over a traced line. |
| min_height | float | 0.8 | Walls lower than this fraction of the smallest chunk (at the smallest scale) get none. |
| min_wall | float | 8 | Walls lower than this many metres get no chunks (the heightfield's own steps and banks are left alone). |
| scale | vec2 | (0.8,2.5) | The chunks' scale range; within it the scale is chosen so a column's tiers span foot to rim. |
| scale_jitter | float | 0.18 | Each chunk's scale varies by up to this fraction. |
| tiers | int | 10 | The most tiers a column stacks; a wall taller than they reach keeps its heightfield above. |
| overlap | float | 0.25 | How much of a chunk's height the tier above overlaps. |
| spacing | float | 0.62 | The column step along the wall, as a fraction of a chunk's width (under 1: neighbours overlap). |
| foot_slope | float | 40 | Across the wall, where the slope eases below this outward is its foot. |
| wall_slope | float | 60 | A wall must be at least this steep somewhere across it: gentler banks are left to the talus. |
| rim_slope | float | 48 | Where it eases below this inward is its rim. |
| search | float | 24 | How far across the line the wall is looked for, each way. |
| inset | float | 0.4 | How far behind the heightfield's face a chunk's back is buried, at its least. |
| protrude | float | 0.2 | How far a chunk's front stands proud of the face, as a fraction of its depth. |
| lean | float | 0.8 | How much of the face's lean a chunk follows (tilting back into the wall). |
| max_lean | float | 30 | The most a chunk leans back. |
| sink | float | 0.06 | How far the bottom tier sits below the wall's foot, as a fraction of its height. |
| yaw_jitter | float | 14 | Each chunk turns by up to this about the vertical. |
| roll_jitter | float | 6 | Each chunk rolls by up to this about its facing. |
| along_jitter | float | 0.3 | Each chunk slides along the wall by up to this fraction of a column step. |
| drop_jitter | float | 0.12 | Each chunk above the first tier moves up or down by up to this fraction of its height. |
| ledge_chance | float | 0.18 | The chance of a ledge slab between two tiers. |
| rim_ledge | float | 0.12 | The chance of a ledge slab capping a column's rim. |
| ledge_scale | float | 0.9 | A ledge's length as a fraction of the chunk's width it sits on. |
| collision | enum | auto | How the chunks and ledges collide: auto is what their rocks stages ask (mesh for cliff chunks). |
| max_chunks | int | 4000 | The most chunks and ledges placed (0: no cap). Hero chunks are dense: keep this to what the walls need. The tallest stretches are walled first. |
| stretch | float | 60 | Walls are lined in stretches this long, the tallest first and each whole, so a capped wall set covers the big faces rather than dotting columns about. |
| seat | enum | silhouette | How a chunk's back is buried: silhouette pushes it in until the rim of its back — what is seen of an open-backed scan from the side — is in the wall; box buries its bounding box's back face (phase 3's rule, which leaves an open-backed scan standing proud with a gap behind it). |
| max_back_gap | float | 0.75 | A chunk that the silhouette seating would bury deeper than this fraction of its depth past the box seating is tried as another variant, and dropped if none fits: it would vanish into the wall. |
| back_cover | float | 0.6 | The share of the back's rim the silhouette seating buries (1: all of it; lower lets the odd far-forward lip stay out rather than bury the piece). |
| ground | stage | "" | A terrain_material (or region_material) stage: the chunks blend into its ground colour at their feet and match toward it. |
| match_to | enum | ground | What the chunks' colour match (match_ground) aims at: the ground's average colour, or the terrain material's rock layer — the colour of the bare heightfield walls the chunks stand on, which is what they must not stand out from. |
| match_brightness | float | 1 | Scales the chunks' colour after the match (with match_ground above 0): below 1 darkens pale scans to sit with the walls. |
| min_contact | float | 0.65 | The share of a chunk's back that must touch the face (buried, or within Contact gap of it). A piece with less hangs off the wall with daylight behind it — a rock stuck to a pillar's flank — and is tried as another variant, then dropped. 0: no test. |
| contact_gap | float | 0.6 | How far in front of the face a chunk's back may stand and still count as touching it. |
| backing | float | 0.8 | How much rock a wall must have behind its rim, in chunk widths: a pillar or fin narrower than that gets no chunks (they would hang off its sides). 0: no test. |
| height_norm | float | 0.7 | Evens the variants' sizes in a column: 0 scales every variant alike (a 40 m scan beside a 5 m one stays eight times as big), 1 brings each to the variants' mean height. |
| tier_jitter | float | 0.35 | Each tier's overlap with the one below varies by up to this fraction, so the joints up a column do not run at one pitch. |
| focus | vec2 | (0,0) | Where the walls are mostly seen from (the spawn, the canyon floor): with a cap, the stretches nearest it are walled first (with Focus range). |
| focus_range | float | 0 | The distance from Focus at which a wall's priority halves; 0 ignores Focus (the tallest stretches first). |
| match_ground | float | -1 | How far the rocks' colour is carried toward the ground's (0: their own colour; 1: the ground's hue and saturation, their own brightness and detail). -1: what their Poly Haven rocks stage says. |
| ground_band | float | 0.5 | With a terrain material (the stage's `ground`), how far up a rock's foot fades into the ground's colour. 0: no blend. |
| streaks | float | 0.3 | Dark vertical varnish streaks down the rocks' steep faces. |

### wall_sheet (Placement)

A terrain's cliffs built as rock geometry (phase 8). A heightfield cannot
overhang, and its walls are a few samples stretched over tens of metres:
however good the texture, they read as smooth extruded surfaces. This stage
lays a **shell** of signed-distance rock over every wall, with the
structure a weathered sandstone cliff has:

- **beds** of irregular thickness, thickening and thinning in formations up
  the wall; the **soft** ones (`soft_share`) weathered back into hollows
  (`soft_depth`) under the **hard** ones, which stand as ledges with an eased
  under-lip — overhangs and the shadow under them;
- **joints**: vertical fractures about `joint_spacing` apart that cut the
  face into panels, each set back its own amount (`panel_depth`); a joint is
  open through a run of a few beds and dies out, so the face does not read as
  masonry;
- **alcoves** hollowed now and then into a panel's soft bed (`alcoves`);
- a **caprock**: the few metres under each rim are hard and unjointed, so the
  skyline is not notched;
- **roughness** (`rough`) rounded by the mesher's `weathering`.

The shell is the terrain's own surface pushed **out** along its normal by up
to `depth` metres on steep ground, less the recess those features carve — so
it always stands in front of the terrain, which never changes: no hole is
cut in the tiles, and `drift` (which recooks them) is unaffected. On flat
ground it lies 12 cm under the terrain, out of sight, and it comes out of
the ground exactly where the wall starts to rise. Ground steeper than the
`steep` band is wall, unless its relief over 8 m is under `min_wall` (a dune
face is steep and is not a cliff).

The shell may rise at most 25 cm over the ground just uphill of it, so a
rim's caprock never stands over the mesa top as a parapet. The walls are
meshed in 32 m sections on a world-aligned grid (dual contouring at
`cell`). Everything buried under the terrain is dropped, and each section
is simplified within 4 cm with its edges to the next section locked, coloured with the field's ambient occlusion and a tint
per bed and panel, and cooked with mesh collision. They wear the `ground`
material's rock layer alone (its strata, macro variation and anti-tiling
included, its bedding relief turned down to fine `bedding` laminations now
the beds are geometry) and blend into the floor's colour at their feet
(`ground_band`).

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage whose cliffs are built. |
| ground | stage | "" | The terrain_material (or region_material) stage: the walls wear its rock layer (in an eight-layer region material, the region's own environment's rock), and their feet blend into its ground. |
| cell | float | 0.4 | The mesher's grid: the finest feature the rock has. Halving it is four times the triangles. |
| depth | float | 2.4 | How far the rock stands out of the terrain at most: the depth of the deepest recess, the reach of the furthest ledge. |
| steep | vec2 | (40,55) | Ground steeper than the first starts to be wall; by the second it is all wall. |
| min_wall | float | 6 | Steep ground with less relief than this (over 8 m) is not a cliff — a dune face, a bank — and gets no rock. |
| bed_spacing | float | 3.2 | The beds' typical thickness. |
| soft_share | float | 0.45 | The share of beds that are soft and weather back under the hard ones (none in the caprock). |
| soft_depth | float | 0.75 | How far back a soft bed weathers, as a share of the relief. |
| joint_spacing | float | 17 | The vertical joints' typical spacing: the width of the panels the face breaks into. |
| joint_depth | float | 0.55 | How deep an open joint cuts, as a share of the relief. |
| panel_depth | float | 0.35 | How far a panel between joints can stand back from its neighbours, as a share of the relief. |
| alcoves | float | 0.06 | The chance a panel's soft bed is hollowed into an alcove. |
| rough | float | 0.14 | The surface's lumps and grain. |
| weathering | int | 1 | Steps of curvature flow that round the rock's convex edges. |
| bedding | float | 0.06 | The material's bedding relief on the walls: the beds are geometry now, so only fine laminations. |
| ground_band | float | 0.8 | How far up a wall's foot takes the floor's colour. 0: none. |
| cook_memory | int | 3000 | How much memory the sections' meshing and cooks may hold at once. |
| caves | stage | "" | A cave stage whose void is cut out of the walls, so its mouths open through the rock. |

### cave (Placement)

A sandstone cave system along an authored path through a terrain
(docs/adventure/PHASE_C.md): a sandy floor strewn with fallen blocks, walls
stepped by strata ledges, a vault hung with rock drapery, dark inside and
bright at its mouths. It cuts the terrain open where it comes through the
ground.

**The path** is a 3D spline (`x, y, z, width, height`): each point is on the
cave's **floor**, with its width wall to wall and height floor to roof. Start
and end it outside the ground — those are the mouths — with the floor at the
ground there (where the ground lies up to 2.5 m over the floor, the floor
rises to just under it, so the ground itself is the floor out of a mouth). A
wide, tall point is a chamber. The 2D view draws the path, its walls and each
point's width; the 3D view a coarse shell round the void.

**The void** is cut from the path: vertical walls up to the vault
(`arch`), a flat, rippled sand floor (`sand`), the walls and roof pushed
about by noise (`rough`), soft beds weathered back between hard ones as
ledges and roof steps (`ledges`, `bed_spacing`), thin fins and curtains of
rock hanging from the roof in patches (`drapery`, `drapery_length`,
`drapery_spacing`), and angular blocks fallen on the floor, big ones and
rubble, half buried in the sand (`blocks`, `block_size`). `clear` keeps a
lane down the middle: fewer blocks, its rubble no higher than a step, its
rocks small and without collision.

**The cut.** Every terrain quad the void passes through (within
`cut_margin`, more than half a metre over its floor — ground at the floor is
the floor carried out of a mouth) is dropped. Every terrain tile the cave
touches — a hole, or the cave passing under it — is recooked from the terrain
stage's own heights, tile plan, quantisation step and base, without the
dropped quads and without skirts (a skirt would be a wall across the
tunnel), and **supersedes** the terrain stage's tile (as `drift` does). The
recooked tiles are open meshes: navigation reads them triangle by
triangle, which is what lets an agent walk into the cave a closed tile would
fill.

**The rock** is the terrain's solid less the void, near the cave: its top
lies 12 cm under the terrain except round a hole in steep ground, where it
stands `lip` over it (falling off over `lip_band`) and covers the cut
tiles' ragged edge. It is meshed in 32 m sections on a world-aligned grid
(dual contouring at `cell`; each box overlaps its neighbours by half a cell
so a wall crossing a seam is meshed whole), the triangles nobody sees
dropped (the box's faces through rock, the top under the terrain), and
simplified within 1.2 cm with its open edges and box sides locked. Each
section is three meshes:

| Part | Triangles | Material |
| --- | --- | --- |
| `sand_*` | the void's floor | the ground material's sand layer alone |
| `rock_*` | walls, roof, blocks | the ground material's rock layer, its feet blended into the floor's sand colour (`ground_band`) at the cave floor's own height (`floor_height.png`, `floor_color.png`) |
| `ground_*` | the rock's top round a mouth | the terrain tiles' own material |

all with mesh collision, `draw_distance` and `shadow_distance`.

**Dark inside.** Vertex AO (alpha, which scales the ambient light) is the
field's near AO times the **sky's visibility**: on a voxel grid
(`sky_cell`) reaching `sky_reach` round the cave, the share of a hemisphere
of rays from each voxel of air that reach the open sky — out through a mouth,
or past the grid over the terrain. The interior loses `darkness` of its
ambient where it sees no sky; `sky_gain` keeps more light round a mouth. The
mouths stay bright; the deep interior is close to black without lights.

**Rocks.** With `rocks` (a rocks or polyhaven_rocks stage), its variants are
strewn on the floor at `rock_density` per m², `rock_size` metres, more
against the walls, each wearing a copy of its material darkened by the sky
it sees (a scan has no AO of the cave's own), with a fifth of the cave's
draw and shadow distances.

**Outputs.** `cave.yaml` holds the settings (from which the void is rebuilt
exactly), the **mouths** (floor point, the direction out, width, height),
the **interior** as boxes of 16 m of path under the ground (where an
ambient-darkening volume goes), and **samples** every 6 m of path a third of
the height up with the sky's visibility there and whether they are under the
ground (where lights go). Later stages read it as the artifact; `wall_sheet`'s
`caves` cuts the void out of its shell so the mouth opens through the cliff.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage the cave is cut through (it must cook its tiles). |
| ground | stage | "" | The terrain_material stage: the walls wear its rock layer, the floor its sand layer. |
| path | spline | 0 points | The course: floor points with width and height. |
| arch | float | 0.6 | The share of the height that is vaulted roof. |
| wander | float | 2 | How far the cave wanders off the drawn path (never at the mouths). |
| rough | float | 0.6 | Lumps and hollows in the walls and the roof (m). |
| ledges | float | 0.7 | How far soft beds weather back (m). |
| bed_spacing | float | 1.6 | The beds' typical thickness (m). |
| drapery | float | 0.5 | The share of the roof hung with drapery. |
| drapery_length | float | 3 | How far the longest drapery hangs (at most half the height). |
| drapery_spacing | float | 2.2 | The fins' typical spacing. |
| blocks | float | 0.45 | How thickly the floor is strewn with fallen blocks. |
| block_size | float | 2.2 | The big blocks' typical size; rubble is about a third of it. |
| clear | float | 0.6 | How much clearer the middle lane is. |
| sand | float | 0.08 | The sand floor's ripple (m). |
| cell | float | 0.3 | The mesher's grid. Halving it is four times the triangles. |
| weathering | int | 1 | Curvature-flow steps that round convex edges. |
| cut_margin | float | 0.3 | A terrain quad this close to the void is cut. |
| lip | float | 0.4 | How far the rock stands over a hole's edge in steep ground. |
| lip_band | float | 2.2 | How far round a hole the lip reaches. |
| sky_cell | float | 1 | The sky bake's grid. |
| sky_reach | float | 24 | How far round the cave the sky grid reaches. |
| darkness | float | 0.95 | How much ambient the deep interior loses. |
| sky_gain | float | 2.5 | Scales the sky's visibility before it darkens. |
| sand_color | color | (0,0,0) | The floor colour the walls' feet blend into; black: the ground material's own sand. |
| ground_band | float | 0.7 | How far up the walls and blocks the sand colour reaches. |
| rocks | stage | "" | A rocks or polyhaven_rocks stage strewn on the floor. |
| rock_density | float | 0.12 | Rocks per m² of floor. |
| rock_size | vec2 | (0.25,1.4) | The rocks' sizes (small ones far more often). |
| draw_distance | float | 700 | How far the sections are drawn (the rocks: a fifth). |
| shadow_distance | float | 250 | How far they cast (the rocks: a fifth). |
| cook_memory | int | 3000 | Memory for the sections' and tiles' cooks. |
| preview_cell | float | 1 | The 3D preview's grid. |
| chunk | string | "" | The level chunk the sections and rocks stream with (an ID), except those within `chunk_mouth` of a mouth, which stream by their place (a mouth seen from outside stays when the interior is switched off). Empty: every placement by its place. |
| chunk_mouth | float | 28 | How far from a mouth placements keep streaming by their place. |
| ambient | bool | false | Place an ambient-darkening volume (a volume's `ambient:` block) over each interior box, grown by `ambient_margin` so the walls and roof lie inside it past the fade — the player, scanned rocks and entities in the cave lose the sky's ambient as the walls do. A box whose samples see sky keeps more. |
| ambient_sky, ambient_ground | float | 0.1, 0.15 | The share of the sky ambient left deep inside on upward and downward faces. |
| ambient_fade, ambient_margin, ambient_dust | float | 3, 4, 0.02 | The volumes' fade inside their faces, how far each box is grown, and extra dust for the light shafts inside. |

**Performance** (dev box, `-workers 6`, the phase-C test: a 180 m cave
climbing 60 m, 12 sections): the cut and 4 tile recooks 5 s, the sky 0.7 s,
the sections 24 s in all (1.9 M triangles meshed, 0.16 M kept), about 9 MB
of meshes; peak memory 0.45 GB.

**Known limits.** A `drift` stage that recooks a tile the cave cut would
seal it again (put no drift on the cave's tiles). The recooked tiles are
open, so a nav build reads them triangle by triangle. A scanned rock's
darkening is in its material, not its lighting: a light carried into the
cave lights it less than the walls.

### ruins (Placement)

Ancient sandstone architecture laid out along a path down a slope
(docs/adventure/PHASE_D.md): terraces held by battered retaining walls,
stairs between them, stepped temple towers with cornices, dentils, carved
niches and blind arches, domed crowns and miniature corner shrines,
pavilions on columns, gateways over the walkway, crenellated walls,
colonnades, fallen columns and rubble — weathered, some of it collapsed.

**The path** (`x, z, width`) runs top to bottom in plan. It is cut into
**terraces** and **stairs** alternately, each a chord of the path, so one ends
exactly where the next begins and the way down is unbroken:

- a terrace's floor is `clearance` over the highest ground under its
  walkway; each side reaches out to the point's `width` or until the ground
  rises to the floor (a side slope's uphill side is cut back); its body
  reaches `foundation` under the lowest ground beneath it, so on a slope its
  downhill side is a retaining wall (battered, coursed in big blocks,
  buttressed where it is tall, an overhanging coping and a crenellated
  parapet opened for the walkway);
- a stair drops to the next floor in risers no taller than `rise` on treads
  of `tread`: the fewest steps that reach the ground under the next terrace,
  or, where the ground falls faster than a stair can, `max_flight` and a
  floor standing on a foundation. Steep ground ahead shortens the terraces.
  Keep `rise` under the player's step height (0.5 by default): the stairs are
  walkable with mesh collision.

Buildings stand on the terraces either side of the walkway (`walkway`
wide), fronts to it; a gateway (`gates`) may stand over the walkway at a
terrace's top; colonnades line it on some terraces; walls (`walls`) run along
long edges; `outliers` towers and shrines stand on the slope beside the path
within `spread`, each on a plinth reaching into the ground (none taller than
`max_plinth`); `debris` rubble and fallen columns lie in the courts and at
ruined plinths' feet. `ruin` is the share of ruined variants.

**Instanced library.** The buildings are a library of 14 variants (four
towers — one with an open arched pavilion and dome on top, one fallen — a
shrine, two pavilions, a gateway, two walls, a column, a fallen column, two
rubble heaps), each modelled as a signed-distance field (mouldings as
profiles swept round stepped plans; carving cut in face frames with
repetition), meshed **once** at a fine cell (3–7 cm × `detail`; the tallest
are capped by the mesher's 512 cells), its buried base dropped, simplified
within `simplify`, baked (vertex AO from the field and the floor under it,
`dust` on what faces up) and cooked to Nanite with mesh collision. Only the
variants the layout uses are cooked; each placement is a transform.
**The site** (terraces, stairs, plinths) is one field meshed in 32 m world
sections at `site_cell` (nothing more than a metre under the terrain is
meshed; box sides and open edges locked in the simplification; one
quantisation step), placed with mesh collision.

**Materials.** Three, `gen_<gen>_<id>_temple`, `_wall` and `_site`: the
`textures` stage's `temple_layer`, `wall_layer` and `site_layer` (falling
back to each other) projected triplanar at the layer's size ×
`texture_scale`, colour-graded toward a mix of `stone_color` and the
`ground` stage's rock (`rock_share`, `match`) with each scan's mean
brightness brought to `brightness`, macro variation and desert varnish
(`streaks`); the site's feet blend into the ground (`ground_band`). Good
Poly Haven layers: `old_sandstone_02` (temples: warm, weathered blocks),
`medieval_blocks_02` (walls: warm rubble masonry), `sandstone_cracks`
(terraces — their block joints are geometry already). Avoid
`large_sandstone_blocks` on the temples: it is a grey trim sheet whose
green-stained band and yellow line repeat along every moulding, and
`rabdentse_ruins_wall` reads near-black in shade.

**ruins.yaml** (the artifact): every terrace's and plinth's corners and
floor, every stair (from, to, steps, rise, tread, width), every instance, the
free **spots** on the floors (for props), and each variant's triangles,
size and instance count.

```yaml
- stage: polyhaven_textures
  id: masonry
  params:
    layers:
    - {name: temple, id: old_sandstone_02}
    - {name: masonry, id: medieval_blocks_02}
    - {name: terrace, id: sandstone_cracks}
- stage: ruins
  id: ruins
  params:
    terrain: ground
    ground: ground_mat
    textures: masonry
    path:                     # top to bottom; zigzag across steep slopes
    - {x: 0, z: -22, width: 28}
    - {x: 22, z: -62, width: 30}
    - {x: -18, z: -112, width: 32}
    outliers: 30
```

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage the ruins stand on. |
| ground | stage | "" | Optional terrain_material: colour match toward its rock, the site's ground blend. |
| textures | stage | "" | The polyhaven_textures stage with the masonry layers. |
| temple_layer, wall_layer, site_layer | string | temple, masonry, terrace | Its layers for the temples, the walls, the terraces. |
| texture_scale | float | 1 | Multiplies the layers' metres a repeat. |
| path | spline | 0 points | The way down: x, z, width. |
| terrace_length | vec2 | (12,22) | A terrace's length along the path. |
| walkway | float | 3.6 | The clear way, and the stairs' width. |
| rise, tread | float | 0.18, 0.34 | The tallest riser, a step's run. |
| max_flight | float | 20 | The longest stair (m). |
| clearance | float | 0.25 | A floor's height over the ground under the walkway. |
| foundation | float | 1.5 | How far bodies reach under the lowest ground. |
| buildings | float | 0.75 | How full the terraces' sides are. |
| gates, walls | float | 0.35, 0.5 | Chance of a gateway per terrace, of a wall per long edge. |
| outliers, spread, max_plinth | int, float, float | 14, 40, 24 | Buildings on the slope beside the path, how far out, their tallest plinth. |
| ruin, debris | float | 0.35, 0.5 | Share of ruined variants; how much rubble. |
| scale | vec2 | (0.9,1.15) | The buildings' size range. |
| weathering | float | 1 | Erosion: swells, grain, eroded patches (≈5 cm). |
| rounding | int | 1 | Curvature-flow steps rounding convex edges. |
| detail | float | 1 | Scales the buildings' meshing resolution. |
| site_cell | float | 0.1 | The terraces' meshing grid (under the riser). |
| simplify | float | 0.006 | Simplification error (m). |
| stone_color, rock_share, match, brightness, saturation | color, float, float, float, float | (0.8,0.56,0.37), 0.4, 0.85, 0.5, 1.25 | The colour grade (version 2: warmer and more saturated; scans shot under grey skies read grey in desert light). |
| dust, streaks, ground_band | float | 0.4, 0.25, 1.2 | Up-facing dust (vertex colour), varnish, the site's ground blend. |
| draw_distance, shadow_distance | float | 1500, 500 | Buildings and terraces. |
| small_draw_distance | float | 350 | Columns, walls, rubble (shadows a third). |
| cook_memory | int | 3000 | Memory for the site sections' meshing and cooks. |
| preview_cell | float | 0.5 | The 3D preview's grid (the site; buildings as boxes). |

**Performance** (dev box, `-workers 6`, the phase-D test: 8 terraces, 7
flights of 58 steps, 30 plinths, ~70 instances): the library about 2 min
(0.65 M triangles kept, 29 MB), the site 5–6 min (61 sections, 2.6 M
triangles kept of 13 M meshed, 113 MB); peak memory 2.5 GB. The site
dominates: raise `site_cell` toward the riser or `simplify` to cut it.

### drift (Placement)

Sand that answers the rocks on it (phase 8). The terrain is generated
before any rock is placed, and a scattered rock is seated by sinking it in,
so it meets the ground in a flat cut. `drift` runs after a `scatter` stage
and raises the terrain against each rock of the chosen `rules`:

- a **fillet** where sand meets rock: `fillet` of the rock's height above
  the ground at the contact (at most `max_height`), concave, its slope at the
  rock 2/`spread` (34° at 3, sand's angle of repose), following the rock's
  real outline (its cooked mesh is rasterised onto the terrain's grid);
- a **tail** `tail` times longer downwind (`wind`, degrees clockwise from
  +Z) and a ramp `windward` times shorter on the side the wind comes from;
  in a wash (the `flow` mask band) the tail turns downhill — the water lays
  the sand;
- rocks lower than `min_height` get none (a drift a grid cell across is a
  pimple), drifts fade out on steep ground (`slope_fade`), and two rocks'
  drifts meet in one, never a double heap.

Then the terrain tiles the sand touches are **recooked** with the terrain
stage's own tile plan and replace its tiles (a stage can *supersede* an
earlier stage's placements: `RunContext.Supersede`); the material's weights
are painted sand over the drifts (`sand_cover`, full at `sand_depth`), and
its ground colour and height maps rewritten; the scatter's rocks are
re-placed wearing materials whose ground blend reads the new sand line
(`ground_band`), and with `lift` the pebbles and plants standing on a drift
are lifted onto it.

**Against the cliffs** (`walls`, a `wall_sheet` stage): the cliffs' feet get
one smooth sand surface, worked out over the whole map at once. The foot is
read off the terrain: open, gentle ground within the wall stage's `depth` of
steep ground that rises above it under the rock. From every foot point a
ramp runs out — `wall_height` high (rising and falling along the wall by
`wall_vary` over about 15 m), `wall_spread` long per metre of it, `wall_tail`
longer downwind — standing on the foot's ground averaged along the wall, and
the sand fills up to the highest of them: hollows at the foot are filled and
humps buried, a bench's sand spills off it as a slope, and ground under the
rock's flare takes the full height so the sand meets the rock with no dip.
The terrain's own steep wall behind the rock, ledges up the face and the
ground over a rim get none. The walls are re-placed so their feet blend into
the sand where it now lies.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage whose tiles the sand is added to (it must cook its tiles). |
| ground | stage | "" | The terrain_material stage the tiles wear: its weights get the sand, and the recooked tiles wear a copy of it. An eight-layer region material is refused (drift paints a four-layer splat). |
| scatter | stage | "" | The scatter stage whose rocks the sand piles against. |
| rules | string | "" | The scatter's rules (by name, separated by spaces) whose rocks get drifts; empty: every rule. |
| wind | float | 60 | The direction the wind blows toward, degrees clockwise from +Z seen from above (90: toward +X). The tails run this way. |
| flow | vec2 | (0.2,0.5) | Between these levels of the terrain's flow mask, a rock's tail turns from the wind to downhill. |
| walls | stage | "" | A wall_sheet stage whose cliffs get sand ramps at their feet. Empty: none. |
| wall_height | float | 2.2 | How high the sand climbs a cliff's foot, on average. |
| wall_spread | float | 4.5 | How far a cliff's ramp runs out per metre of its height. |
| wall_tail | float | 0.6 | How much further a cliff's ramp runs downwind. |
| wall_vary | float | 0.6 | How much a cliff's ramp rises and falls along the wall, over about 15 m. |
| fillet | float | 0.3 | How high the sand climbs a rock, as a fraction of the rock's height above the ground. |
| max_height | float | 1.2 | The most sand against any rock, metres. |
| min_height | float | 0.45 | Rocks standing less than this above the ground get none. |
| spread | float | 3 | How far the sand runs out per metre of its height: its slope at the rock is 2/spread. |
| tail | float | 2.5 | How much longer the sand runs downwind, as a multiple of its run elsewhere. |
| windward | float | 0.4 | How much shorter the ramp on the side the wind comes from is. |
| slope_fade | vec2 | (24,36) | Drifts fade out between these ground slopes. |
| sand_cover | float | 1 | How fully the drifts are painted the sand layer. |
| sand_depth | float | 0.12 | The drift depth at which the sand covers fully. |
| ground_band | float | 0 | The re-placed rocks' ground blend height; 0 keeps their scatter rule's. |
| lift | bool | true | Lift the scatter's other placements standing on a drift onto it. |
| cook_memory | int | 2000 | How much memory the tiles' cooks may hold at once. |

### vegetation (Placement)

Sparse plants from the engine's own biomes (`biomes/*.fbio`: desert,
badlands, mesa, scrub...) grown on a terrain where its masks allow. A
`kind: biome` scatter volume grows its biome on whatever ground a column cast
from its top meets, the terrain's mesh-collision tiles included, so each rule
weighs the ground with the same bands as a scatter rule, cuts it into `cell`
squares, keeps the cells whose mean weight is over `threshold`, and merges
them into rectangles at most `max_size` a side: one volume each, its density
the rule's `density` times the cells' mean weight, its height spanning the
ground under it (`headroom` above and below). The biome's own slope limits
still apply per plant. `amounts` scales species by shape key (`rock=0
pebbles=0 grass=0 cactus=0.35`): the default turns the biome's own rocks off,
since the scatter stage places real ones. The preview outlines the volumes;
the plants themselves grow in the level.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage to grow on. |
| seed | seed | 0 | Moves the clumps. |
| rules | list | 0 items | The rules: each a biome and where it grows. |
| rules.name | string | plants | The rule's name: its volumes' IDs start with it. |
| rules.enabled | bool | true | Off skips the rule. |
| rules.biome | string | desert | The biome to grow: a file in biomes/ or a built-in (desert, badlands, mesa, scrub, savannah...). |
| rules.density | float | 0.5 | The biome's density (1 as the biome has it), times each rectangle's mean weight. |
| rules.amounts | string | rock=0 | pebbles=0 Per-species amounts by shape key, `key=value` separated by spaces (0 none, 1 as the biome has it, up to 4): e.g. `rock=0 pebbles=0 grass=0.3 deadwood=2`. The default leaves the rocks to the scatter stage. |
| rules.tint | color | (1,1,1) | Multiplies every colour in the biome (white: none). |
| rules.color | color | (1,1,1) | The rule's rectangles in the 2D view. White picks a colour per rule. |
| rules.threshold | float | 0.4 | The mean weight a cell needs to grow anything. |
| rules.slope | vec2 | (0,40) | The ground slopes it grows on, degrees (from, to). |
| rules.slope_soft | float | 6 | How many degrees beyond the slope band the weight fades out over. |
| rules.height | vec2 | (0,1) | The heights it grows at, as a fraction of the terrain's range (0 its lowest, 1 its highest). |
| rules.flow | vec2 | (0,1) | The flow mask's band: 0 is dry ground, toward 1 washes and canyon floors that carry water. |
| rules.deposition | vec2 | (0,1) | The deposition mask's band: toward 1 where sediment settled (fans, talus, wash floors). |
| rules.curvature | vec2 | (0,1) | The curvature mask's band: 0.5 is flat, above it hollows, below it crests. |
| rules.cliff | vec2 | (0,0.6) | The cliff mask's band: keep it low to stay off the walls themselves. |
| rules.soft | float | 0.1 | How far beyond each 0..1 mask band the weight fades out over. |
| rules.cliff_base | float | 0 | How strongly it keeps to the FOOT of cliffs — talus under a wall: 0 ignores cliffs, 1 grows only within the distance below one. |
| rules.cliff_base_distance | float | 25 | How far out from a cliff's foot the cliff-base weight reaches. |
| rules.cluster | float | 0.5 | Clumping: 0 spreads it evenly over the ground its bands allow, 1 gathers it in patches with bare ground between. |
| rules.cluster_scale | float | 25 | The patches' size. |
| cell | float | 16 | The grid the weights are judged on: finer follows the masks closer with more volumes. |
| fray | float | 0 | Cross-fades neighbouring volumes: each grows 0.625 × fray past its cells and fades its plants over fray at its edge (`edge_fade_distance`), so two volumes of different density blend over their overlap instead of meeting on a ruled line, and a patch's outer edge is ragged. 0: hard rectangles. About three quarters of `cell`. |
| max_size | float | 96 | The longest side a volume may have (each regrows on its own when edited, and large ones take longer). |
| headroom | float | 4 | How far a volume reaches above the highest ground under it (and below the lowest). |

### polyhaven_rocks (Rocks)

Rock and cliff **scans downloaded from Poly Haven** (CC0) in place of generated
rocks — the canyon's default source (the generated `rocks` stage stays for odd
shapes). Name the assets by ID (the end of their polyhaven.com/a/ address; the
param panel shows each one's thumbnail, and **Browse…** opens the [Poly Haven
browser](34-polyhaven.md), whose **Add to stage** appends here), or by a query —
`category`, `tags`, `search`, `count` — which takes matches in ID order, so a
rerun picks the same ones. Good desert pieces: `namaqualand_cliff_01`
(8.5 m), `namaqualand_cliff_02` (20 m), `namaqualand_boulder_02`..`06`,
`rock_face_01`/`02`, `boulder_01`.

Each asset is downloaded once (cached and md5-checked in the user's cache
directory, four at a time), **imported** into `models/polyhaven/<id>/` — the
glTF re-pointed at engine textures `textures/ph_<id>.png`, `ph_<id>_normal.png`
(Poly Haven's `nor_gl` with its green turned over: the engine's V runs down
the image), `ph_<id>_arm.png`, `ph_<id>_height.png` — put in the placement
frame (base on y = 0, footprint centred; for a cliff piece a **back** estimated
from the scan — the area-weighted horizontal normal points out of a rock face —
and turned to −Z on z = 0, as `cliff_wall` wants), and cooked to a Nanite mesh
with **hulls** up to `hull_size` and the mesh alone above it. A file that holds
several pieces side by side (several root nodes: a set of variations) becomes
one variant each, `<id>_<node>`. The stage writes the same `rocks.yaml` a
generated rocks stage writes, its variants pointing at the shared import
(`model:`), so `scatter` and `cliff_wall` take it unchanged; and `credits.yaml`
(each asset's authors). A second stage, or another generator, naming the same
asset at the same settings downloads and cooks nothing.

`max_triangles` decimates before the cook (keep scanned cliffs dense: Nanite
draws them cheaply); `lite_triangles` adds a light copy of each, `<id>_lite`,
which a scatter rule's `max_triangles` picks for rocks placed by the hundred.
Don't give a cliff_wall's source lite copies: cliff_wall uses every variant.
The preview never downloads: it lists what is imported and turns the chosen
asset's cooked mesh.

| Param | Type | Default | |
| --- | --- | --- | --- |
| assets | list | 3 items | The assets, by ID, in order. Browse… opens the Poly Haven browser. |
| assets.id | string | "" | The Poly Haven asset ID (the end of its polyhaven.com/a/ address). |
| assets.back | string | "" | This asset's back, over the stage's (empty: the stage's). |
| assets.max_triangles | int | 0 | This asset's triangle budget, over the stage's (0: the stage's). |
| category | string | "" | A query instead of (or as well as) the list: a Poly Haven category (rocks, plants, trees...). The query takes matching assets in ID order, so a rerun picks the same ones. |
| tags | string | "" | The query's tags, all required (space or comma separated): e.g. `namaqualand desert`. |
| search | string | "" | The query's words, matched against IDs, names, tags and categories. |
| count | int | 0 | The most assets the query adds (0: every match). |
| resolution | enum | 2k | The texture resolution downloaded (the largest offered below it when it is not). 2k is plenty for scattered pieces. |
| max_size | int | 0 | Scale the textures down further to at most this many pixels a side (0: as downloaded). |
| max_triangles | int | 0 | Decimate each scan to this budget before the cook (0: as scanned). Nanite draws dense meshes cheaply, but every placement costs the cull its leaves, and the disk holds every one. |
| lite_triangles | int | 0 | Also make a light copy of each scan at this budget (named <id>_lite) — what a scatter rule's Max triangles picks for pieces placed by the thousand. |
| back | string | auto | Which way the piece's flat back faces, for cliff_wall: auto estimates it from the scan (the area-weighted horizontal normal points out of a rock face); none for a piece seen from all round; -z, +z, -x, +x or a heading in degrees name the back's direction in the file. |
| collision | enum | auto | What the cook builds and placements ask for: auto gives hulls to pieces up to Hull size and the mesh alone above it. |
| hull_size | float | 4 | Auto collision's largest extent for hulls. |
| cook | bool | true | Cook Nanite meshes (off writes .glb files). |
| cook_memory | int | 2000 | The memory the imports may use at once: they run side by side as far as this allows. |
| offline | bool | false | Use only what is already downloaded (the cache in the user's cache directory): no requests. |
| gallery | bool | false | Place every variant in a row along +X from Gallery origin, to look at in the target level. |
| gallery_origin | vec3 | (0,0,0) | Where the gallery row starts. |
| preview_asset | int | 0 | Which asset the 3D preview turns (an imported one's cooked mesh, at its fallback LOD). |

### polyhaven_plants (Rocks)

Plant scans from Poly Haven — `quiver_tree_01`/`02`, `dead_quiver_trunk`,
`wild_rooibos_bush`, `searsia_lucida`, `othonna_cerarioides`, shrubs, dead wood —
imported like `polyhaven_rocks` but with **no back and no collision**, and their
**cutout leaves**: a material Poly Haven marks `MASK` (or `BLEND` with an alpha
map) gets the asset's alpha map merged into its albedo, and draws alpha-tested
and double-sided on every path (Nanite's visibility buffer, its forward draw and
the classic path — `render/nanite_cutout_gl_test.go`), shadows cut out too. The
manifest is the same, so a **`scatter` rule takes a plants stage as its rocks**:
keep `align` and `tilt` at 0 so trees grow upright and raise `spacing`, since a
plant's footprint is its trunk or stems. Several plants in one file (the rooibos
bush is five) become a variant each.

| Param | Type | Default | |
| --- | --- | --- | --- |
| assets | list | 2 items | The assets, by ID, in order. Browse… opens the Poly Haven browser. |
| assets.id | string | "" | The Poly Haven asset ID (the end of its polyhaven.com/a/ address). |
| assets.max_triangles | int | 0 | This asset's triangle budget, over the stage's (0: the stage's). |
| category | string | "" | A query instead of (or as well as) the list: a Poly Haven category (rocks, plants, trees...). The query takes matching assets in ID order, so a rerun picks the same ones. |
| tags | string | "" | The query's tags, all required (space or comma separated): e.g. `namaqualand desert`. |
| search | string | "" | The query's words, matched against IDs, names, tags and categories. |
| count | int | 0 | The most assets the query adds (0: every match). |
| resolution | enum | 2k | The texture resolution downloaded (the largest offered below it when it is not). 2k is plenty for scattered pieces. |
| max_size | int | 0 | Scale the textures down further to at most this many pixels a side (0: as downloaded). |
| max_triangles | int | 0 | Decimate each scan to this budget before the cook (0: as scanned). Nanite draws dense meshes cheaply, but every placement costs the cull its leaves, and the disk holds every one. |
| lite_triangles | int | 0 | Also make a light copy of each scan at this budget (named <id>_lite) — what a scatter rule's Max triangles picks for pieces placed by the thousand. |
| cook | bool | true | Cook Nanite meshes (off writes .glb files). |
| cook_memory | int | 2000 | The memory the imports may use at once: they run side by side as far as this allows. |
| offline | bool | false | Use only what is already downloaded (the cache in the user's cache directory): no requests. |
| gallery | bool | false | Place every variant in a row along +X from Gallery origin, to look at in the target level. |
| gallery_origin | vec3 | (0,0,0) | Where the gallery row starts. |
| preview_asset | int | 0 | Which asset the 3D preview turns (an imported one's cooked mesh, at its fallback LOD). |

### polyhaven_textures (Materials)

Named texture **layers** — sand, cracked ground, gravel, sandstone, rock wall —
each a Poly Haven texture, imported as an engine texture set: `ph_<id>.png`
(albedo), `_normal` (green turned over, as above), `_arm`, `_rough` and `_ao`
(split out of the ARM), `_height` (the displacement), plus
`textures/polyhaven/<id>/polyhaven.yaml`. It writes `textures.yaml` (each layer's
name, ID, resolution, real-world size in metres a repeat, and texture names),
the texture-set artifact the material phase splats terrain with. Until then, a
layer's albedo can be any texture param's value (the terrain stage's `texture`).

| Param | Type | Default | |
| --- | --- | --- | --- |
| layers | list | 6 items | The layers, each a name and a texture. Browse… opens the Poly Haven browser on textures. |
| layers.name | string | layer | The layer's name: what a material names it by. |
| layers.id | string | "" | The Poly Haven texture's ID. |
| layers.scale | float | 0 | Metres a repeat (0: the texture's real-world size, from Poly Haven). |
| resolution | enum | 2k | The resolution downloaded. |
| max_size | int | 0 | Scale the textures down further to at most this many pixels a side (0: as downloaded). |
| offline | bool | false | Use only what is already downloaded. |

Of the default layers, use `cliff` (rock_face, a natural varnished face) for
rock on slopes: `rock` (red_sandstone_wall) is masonry, blocks and mortar,
and on a heightfield slope it reads as a brick wall.

### terrain_material (Materials)

The material a terrain's tiles wear (phase 5; see
[28-materials.md](28-materials.md) for the effects). From a `terrain` stage's
masks and a `polyhaven_textures` stage's layers it writes a **splat**
material, `materials/gen_<generator>_<stage id>.fmat`, that paints the
ground the way the land is shaped:

- **rock** (default layer `cliff`) on slopes steeper than `rock_slope` and
  wherever the cliff mask says cliff — projected **triplanar**, so a wall is
  not smeared down, at `rock_scale` metres a repeat, and banded by a **strata
  ramp** built from the terrain's own strata colours (`strata`, `strata_warp`);
- **gravel** in the washes: the flow mask blurred over `wash_width`, between
  the two `wash_flow` levels;
- **cracked** ground on the flats (`flat_slope`) away from washes and sand,
  in patches (`cracked_cover`);
- **sand** everywhere else — the deposition fans (`sand_deposition`), the
  gentle slopes.

Each band's edge is bent by noise (`breakup`, `breakup_scale`), and the
shader height-blends the layers by their `_height` maps (`blend`), so stones
poke out of sand rather than fading through it; the layers' `_ao` darkens
the ambient (`ao`), and `layer_match` tints the cracked, gravel and rock
layers toward the sand's hue so scans shot in different places sit
together. Macro variation and desert varnish are on by default.

It also writes the maps a rock needs to sit in this ground, over the
terrain's rectangle: `ground.png` (the layers' colours mixed by the weights)
and `height.png` (16 bits in red and green). Name the stage as `scatter`'s
and `cliff_wall`'s `ground` and their rocks get a ground blend and a colour
match toward it.

**The tiles wear it when the terrain stage's `material` names it** — the
stage logs the name, `gen_<generator>_<stage id>`.

**Walls that read as rock** (phase 7). A heightfield wall is a few samples
stretched over tens of metres, smooth and streaky however good its texture.
Three parameters make the bare walls read as layered rock:

- a layered rock texture as the `rock` layer, at a large `rock_scale` (the
  template's `cliff_side` at 10 m a repeat);
- `bedding`: ledges and laminations bent into the rock's normals on steep
  faces — each bed weathers back under a sharp lip, faces tilt up to catch the
  sun and lips go dark — with irregular bed thicknesses (`bedding_spacing` on
  average), a third of the beds flush, faded with distance so it never
  shimmers. No geometry and no textures: it costs a few noise taps per pixel;
- `wall_normal`: the rock layer's normal map stronger on walls than on the
  ground (the template's 1.8).

`rock_tint` warms the rock layer on top of the layer match, and `cap_rock`
puts bare rock on the high flats (between the `cap_height` fractions of the
height range, in patches, not where sand is deep): a mesa's caprock rather
than a sand plateau.

**Textures that do not repeat** (phase 8). `anti_tile` lays every layer in
patches about a repeat across, each shifted (on the ground, also turned) its
own way and height-blended where they meet, so no pebble, crack or ledge of a
texture recurs on a grid — the splat's `layerN_anti_tile`. The far backdrop
never takes it. `macro_vertical` lets the macro variation change with height
too, so a wall's colour varies up its face. `wash_bars` breaks the wash's
gravel bed into channels and **sand bars** drawn out along the flow (noise
smeared down the downhill streamlines, `wash_bar_size` long and wide), so a
wash is not one gravel carpet. `gravel_relief` and `cracked_relief` give
those layers **parallax**: the height map moves each texel along the view,
so stones overlap and stand off the ground instead of lying painted on it,
and the gaps between them darken (three more texture reads; it fades out
with distance).

**The backdrop's material.** When the terrain has a far backdrop, the stage
also writes `far_weights.png` (rock by slope, cracked ground in patches,
sand, on the backdrop's lattice) and a second material,
`gen_<generator>_<id>_far`, with the same layers, tints and strata: set the
terrain's `far_material` to it.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | The terrain stage whose masks place the layers. |
| textures | stage | "" | The Poly Haven textures stage the layers come from. |
| sand | string | sand | The textures stage's layer for sand: deposition, gentle slopes, everything the others leave. |
| cracked | string | cracked | Its layer for cracked ground, on the flats. |
| gravel | string | gravel | Its layer for gravel, in the washes. |
| rock | string | cliff | Its layer for rock, on steep slopes and cliffs. The textures stage's default `cliff` is rock_face, a natural varnished face; its `rock`, red_sandstone_wall, is masonry — blocks and mortar — and reads as a brick wall on a slope. |
| rock_slope | vec2 | (32,46) | Rock takes over between these slopes, degrees. |
| cliff | float | 1 | How much the cliff mask adds rock on top of the slope rule. |
| flat_slope | vec2 | (5,12) | Ground flatter than the first is flat (cracked ground's), steeper than the second is not. |
| wash_flow | vec2 | (0.22,0.42) | Gravel takes over between these levels of the flow mask. |
| wash_width | float | 5 | The flow mask is a channel a few metres wide; it is blurred over this width first, so a wash is a gravel bed, not a line. |
| sand_deposition | vec2 | (0.25,0.65) | Where the deposition mask passes these, the flats are sand rather than cracked ground. |
| layer_match | float | 0.5 | How far the cracked, gravel and rock layers' colours are tinted toward the sand's hue (keeping their brightness), so scans shot in different places sit together. |
| cracked_cover | float | 0.7 | How much of the flats cracked ground covers (the rest stays sand), in patches. |
| breakup | float | 0.5 | How much noise bends the edges between layers. |
| breakup_scale | float | 14 | The size of the bends. |
| blend | float | 0.3 | How soft the transitions are in height-map units: low, the higher layer's tops win crisply. |
| rock_triplanar | bool | true | Project the rock layer along all three axes, so cliffs are not smeared. |
| rock_scale | float | 6 | Metres a repeat of the rock layer (0: the textures stage's). Larger than a ground texture's: a cliff is seen from far off, and a 2 m repeat reads as cloth. |
| strata | float | 0.4 | How strongly the terrain's strata colours band the rock. |
| strata_warp | float | 1.5 | How far the bands wander up and down. |
| macro | float | 0.18 | Slow brightness and warmth variation, which hides the textures' repeat at distance. |
| macro_scale | float | 45 | The variation's patch size. |
| streaks | float | 0.35 | Dark varnish streaks down the steep rock. |
| detail | texture | "" | A texture whose normal map adds close-up grain everywhere (empty: none). |
| detail_scale | float | 0.6 | Metres a repeat of the detail. |
| normal_strength | float | 1 | Scales the layers' normal maps. |
| ao | float | 0.8 | How much the layers' ambient occlusion darkens the ambient light. |
| rock_tint | color | (1,1,1) | Multiplies the rock layer's colour on top of the layer match: warmer and redder makes desert-varnished sandstone of a pale scan. |
| sand_tint | color | (1,1,1) | Multiplies the sand layer's colour (the everywhere layer: a savannah's grass); less blue deepens a bleached scan toward gold. |
| cracked_tint | color | (1,1,1) | Multiplies the cracked layer's colour on top of the layer match (a volcanic preset's bare lava, a prairie's dry grass). In a region world a texture several environments share is one palette entry wearing the tint of the first environment by name. |
| cap_rock | float | 0 | Bare rock on the high flats — a mesa's caprock, slickrock with sand in its hollows — rather than sand: how much, in patches. 0: the flats are sand and cracked ground at any height. |
| cap_height | vec2 | (0.5,0.7) | Caprock fades in between these fractions of the terrain's height range. |
| bedding | float | 0 | Ledges and laminations bent into the rock's normals on steep walls (the splat's bedding): each bed weathers back under a sharp lip, so the heightfield's smooth walls read as layered rock in raking light. The recess as a fraction of a bed; 0: none. |
| bedding_spacing | float | 1.2 | The beds' typical thickness. |
| wall_normal | float | 1 | Multiplies the rock layer's normal map on walls (its side projections) on top of Normal strength. |
| anti_tile | bool | false | Lay every layer in shifted (on the ground, also turned) patches so no feature of a texture recurs on a grid (the splat's anti_tile). About half again the texture reads; the far backdrop never takes it. |
| macro_vertical | float | 0 | How much the macro variation also changes with height, so a wall is not one colour top to bottom. |
| wash_bars | float | 0 | Sand bars in the washes: streaks of sand drawn out along the flow between gravel channels, so a wash is not one gravel carpet. How much of the gravel they take; 0: none. |
| wash_bar_size | vec2 | (28,5) | A bar's typical length along the flow and width across it. |
| sand_ripple | float | 0 | Wind ripples on the sand layer (the splat's `ripple`): sinuous crests a hand's breadth apart bent into the normals, a steep lee and a gentle windward face, patchy, on level ground only, faded where a pixel spans a ripple; a little AO in the troughs. The ripples' slope; 0: none. The cave's sand floor takes them too. |
| sand_ripple_wavelength | float | 0.14 | Crest to crest (m). |
| sand_ripple_wind | float | 60 | The way the wind blows across them, degrees clockwise from +Z seen from above. |
| gravel_relief | float | 0 | Parallax depth of the gravel layer (the splat's relief): its stones stand up off the ground instead of lying painted on it. 0: none. |
| cracked_relief | float | 0 | Parallax depth of the cracked-ground layer: its plates lift at their edges. 0: none. |
| snow, wet, ash | string | "" | Cover layers (below): the textures stage's layer for each. Empty: off. |
| snow_height | vec2 | (300,400) | Snow fades in between these heights: world metres, or with `snow_reference: region` fractions (0–1) of the environment's own height range. |
| snow_reference | enum | world | `world`: `snow_height` is in world metres. `region`: it is fractions of the environment's own height range — the 2nd to 98th percentile of the terrain's heights where its biome weight is at least 0.9 (the whole terrain on a classic one) — so a region's snow line sits at the same share of its relief whatever its size, base or neighbours. |
| snow_slope | vec2 | (35,50) | Snow holds below the first slope and is gone by the second: cliffs stay rock. |
| snow_breakup | float | 0.5 | How much noise bends the snow line and its slope limit into drifts and bare patches. |
| snow_breakup_scale | float | 40 | Their size. |
| wet_distance | float | 8 | Wet ground reaches this far from standing water's edge. |
| wet_height | float | 1.5 | ...and this far above its surface. |
| ash_radius | vec2 | (1,2.2) | Ash covers the ground within the first of these distances from a vent and is gone by the second, in cone radii. |
| ash_cover | float | 1 | The most of the ground the ash takes: below 1 the cinders show through (a dusting rather than a blanket). |
| ash_hole | float | 0 | No ash within this distance of a vent (cone radii), fading in over its last fifth: with `ash_radius` past it the ash is an apron round the cone's foot rather than a cap. 0: none. |
| lava_lobes | float | 0 | The share of the ground the cracked layer (a volcanic preset's bare lava) takes on the terrain's lava lobes (terrain.yaml `lobes:`), so the tongues read against the field. 0: off. |

**Cover layers.** Besides the four rules, a ground can be covered by what
lies on it rather than how it eroded: **snow** by altitude and slope,
**wet** ground (mud, wet sand) under and beside standing water (the
terrain's water mask, which a region terrain writes when a preset fills
water in), **ash** and cinders round volcanic vents (terrain.yaml's
`vents:`). Each is a layer key like the four; set any and the material is an
[eight-layer splat](28-materials.md#terrain-and-rock): the covers are layers
5–7, their weights `weights2.png`. They stack in that order, each taking its
share of everything under it, so snow lies over ash and mud alike. The far
backdrop takes snow and ash by the same rules from its own heights. With no
cover key set the material is the four-layer splat it always was.

**Paint.** The `paint` list changes the splat where it is painted
([Terrain paint](36-terrain-paint.md)). Each item names a `layer`, a `target`
role (`sand`, `cracked`, `gravel`, `rock`; `wet`, `ash`, `snow` with covers)
and a `mode`: `override` (the default) moves the splat toward the target by
`strength` × the painted value through `band` — a gravel trail; `band` keeps
the target to the painted ground, its share elsewhere going to the other
layers. Applied after the rules and covers, before the maps are written; the
far backdrop is not painted. Empty by default. With a paint list, the weights
before paint are kept in `prepaint.bin`, so a stroke reruns only the paint
step and the maps after it, not the rules. An item may read a **terrain mask**
instead of a layer (`mask`, phase W3): `river_bed` puts gravel in a
ground_edit's river channels, `river_bar` sand on their point bars, and any
other mask the terrain writes works the same way (the River and Stream
presets' recipes add these).

### Rock materials (scatter, cliff_wall, rocks, polyhaven_rocks)

The placing stages write the material their rocks wear,
`gen_<generator>_<stage>_<rule>` (scatter, per rule; cliff_wall `_chunks`
and `_ledges`), and set it on every placement. It keeps each scan's own
texture and adds:

- **ground_blend** when the stage names a `ground` (a terrain_material
  stage): the rock's foot fades into the ground's colour over `ground_band`
  metres, with a contact shadow;
- **color_grade** with `match_ground`: the rock's average colour carried
  toward the ground's — every texel by the same ratio, brightness kept — so
  Poly Haven's grey Karoo granite reads as the red rock of sandstone country
  at 0.7–1. `-1` (the default) takes the `polyhaven_rocks` stage's own
  `match_ground` (default 0);
- **streaks** (desert varnish) at `streaks`; cliff_wall's default is 0.3.

Plants (`polyhaven_plants` rules) take none of it. With no ground, no match
and no varnish nothing is written and placements wear what their source
says.

A generated `rocks` stage names a `textures` stage and a `texture_layer`
(default `cliff`) to wear that layer **triplanar** (`triplanar_scale`) under
its vertex colour's cavity and strata — a base material the placing stages
build on.

**Seating open-backed scans** (cliff_wall `seat`, `back_cover`,
`max_back_gap`). Scanned cliffs are shells: their back is open and their rim
stands well forward of their box's back face (the namaqualand cliffs' rim is
at half their depth). By default (`seat: silhouette`) the stage measures each
chunk's back from its cooked mesh — a 24² z-buffer from behind, a rim cell
counting only where it looks into the open inside — and buries the
`back_cover` share (0.6) of the rim in the wall too, trying the next variants
when that would push a piece more than `max_back_gap` (0.75) of its depth past
the box's seat. `seat: box` is phase 3's rule.

### environment (Level)

The target level's air and light (phase 6; the shader side is
[08-sky-and-fog.md](08-sky-and-fog.md#the-atmosphere)). It places nothing by
default: it writes the level's `fog:` block — the **atmosphere** — and its
sky and sun through the splice ([Level settings](#placing-into-a-level)).
Every default is the desert canyon's:

- **height fog** lying in the canyon bottom: densest at `height_base` (above
  the terrain's lowest point), thinning by e every 1/`height_falloff` metres
  up, a warm dust colour;
- **aerial perspective**: a haze that takes 63% of what is behind it over
  `aerial_distance` metres, its extinction weighted per channel by
  `aerial_tint` (blue above red, as Rayleigh scattering has it), so far rock
  goes bluish before it goes pale, thinning upward over `aerial_scale_height`;
- the **sun's glow** through both, looking toward the sun;
- the **sky**: the `sky_canyon` skybox, taking `sky_haze` of the same
  atmosphere along its rays, so the far walls and the horizon end in the same
  air;
- the **sun**: yaw 50°, pitch −30°, a little warm.

With `dust` it also places one box volume over the terrain filled with the
`dust_haze` cloud preset: drifting banks of dust in the volumetric march,
where the height fog is smooth. It costs a march over much of the screen
(measured on WSLg: +2.4 ms on a canyon-floor view, +1 ms on an overview), so
it is off by default.

Its preview is notes: how much of a surface is left at 50–500 m along the
floor and 30 m up, and per channel at 500 m.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | "" | A terrain stage: heights below are measured from its lowest point, and the dust haze covers it. Empty: heights are world heights. |
| fog_mode | enum | none | The old distance fade on top of the atmosphere: none leaves the atmosphere alone, which is what a canyon wants. |
| fog_color | color | (0.8,0.78,0.74) | The distance fade's colour, and the sky's horizon blend's. |
| fog_start | float | 400 | Where the distance fade begins (linear, exp, exp2). |
| fog_end | float | 3000 | Where the linear fade is complete. |
| max_opacity | float | 1 | The most of anything the fog and the atmosphere may hide. |
| height_fog | bool | true | Exponential height fog: densest at its base, thinning upward — dust lying in the canyon bottom. |
| height_density | float | 0.006 | Extinction per metre at the base: 0.006 hides about 45% of the floor 100 m off. |
| height_base | float | 0 | The height the density is measured at, above the terrain's lowest point (or a world height). |
| height_falloff | float | 0.12 | Per metre: the fog thins by e every 1/falloff metres up (0.12: every 8 m). |
| height_color | color | (0.84,0.74,0.62) | The ground haze's colour: a warm dust. |
| aerial | bool | true | Haze that thickens with range, tinted bluish at distance by a Rayleigh-like per-channel extinction. |
| aerial_distance | float | 1100 | How far the haze takes 63% of what is behind it (at its base). Short for a hazy day; a kilometre makes a 250 m canyon read as deep. |
| aerial_color | color | (0.8,0.78,0.74) | The haze's colour at range: match the sky's horizon, so far rock melts into it. |
| aerial_tint | color | (0.75,1,1.35) | Per-channel extinction weights. Blue above red is Rayleigh's: far rock goes bluish before it goes pale. (1, 1, 1) is a grey dust haze. |
| aerial_scale_height | float | 300 | How far up the haze thins by e: low keeps the sky clear overhead. |
| sun_glow | bool | true | The sun's forward scattering through the haze: looking toward the sun, the air glows. |
| sun_glow_strength | float | 0.05 | How much of the sun's light the lobe moves toward looking into it. |
| sun_glow_anisotropy | float | 0.7 | Henyey-Greenstein g: near 1 a tight glow round the sun, 0 none. |
| sky_haze | float | 0.6 | How much of the atmosphere the sky takes. A skybox with its own haze painted in wants less than 1. |
| sky | enum | canyon | keep: the level's own; canyon: the sky_canyon skybox (desert sky over canyon country); gradient: a warm desert gradient. |
| sun | bool | true | Set the level's first directional light (made if there is none) from the four below. |
| sun_yaw | float | 50 | The way the sunlight travels, degrees about +Y from +X. |
| sun_pitch | float | -30 | Its elevation, negative down: -30 is a late-morning sun that rakes the walls. |
| sun_color | color | (1,0.95,0.85) | The sunlight's colour: a little warm. |
| sun_brightness | float | 1 | The sunlight's strength. |
| view_distance | float | 0 | The level's far plane. 0 leaves it as it is. |
| hdr | bool | false | Shade the level in high dynamic range with a filmic (ACES) tone curve: light above 1 is kept and rolled off instead of clipped, so a strong sun makes bright, contrasty rock. Writes the level's tonemap block; off leaves the level's own. Takes effect in frames drawn with TAA (the scene target); the editor's view stays LDR. |
| exposure | float | 1 | With HDR: multiplies the scene's light before the curve. |
| ambient_brightness | float | -1 | The level's global (ambient) light's brightness; -1 leaves it as it is. With HDR, a sun well above 1 over an ambient near 0.5 is the look of a clear desert day. |
| spawn | bool | false | Put the level's spawn point (the player's start) on the terrain at Spawn at, facing Spawn yaw. |
| spawn_at | vec2 | (0,0) | Where the spawn stands, world X and Z — moved onto the nearest canyon centreline within 80 m, so it lands on the floor; its height is the terrain's there. |
| spawn_yaw | float | 0 | Which way the player starts facing, degrees about +Y from +X (the sun's yaw convention). |
| ambient_color | color | (0.62,0.7,0.85) | The ambient light's colour, set with Ambient brightness: the blue sky's light in the shadows. |
| shafts | bool | false | Light shafts: shadowed sunlight in dusty air near the eye (the level fog's `shafts:` block; [Sky and fog](08-sky-and-fog.md#light-shafts)). |
| shafts_density | float | 0.01 | The dust's extinction per metre at its base. |
| shafts_distance | float | 150 | How far from the eye the march reaches (m). |
| shafts_steps | int | 24 | Samples a ray takes. |
| shafts_color | color | (1,1,1) | Tints the light the dust scatters. |
| shafts_falloff | float | 0 | Per metre above the terrain's lowest point; 0 is uniform. |
| shafts_anisotropy | float | 0.5 | Henyey-Greenstein g of the scattering. |
| shafts_ambient | float | 0.5 | How much of the level's ambient light the dust scatters (the shafts block's `ambient`): low keeps the shade clear so only the sunbeams glow. |
| shafts_strength | float | 1 | Scales the sunlight the dust scatters. |
| dust | bool | false | A drifting dust haze over the terrain: one box volume of the dust_haze cloud preset, marched by the volumetrics. Costs a march over much of the screen. |
| dust_height | float | 18 | How deep the dust layer is, from the base up. |
| dust_density | float | 0.006 | The dust's extinction per metre in its thickest parts. |
| dust_coverage | float | 0.8 | How much of the layer the banks fill. |


**HDR and the tone curve** (`hdr`). On, the stage writes the level's
`tonemap:` block: the level shades in high dynamic range — light above 1 is
kept rather than clipped — and the frame is mapped to the screen through
`exposure` and the ACES filmic curve, which rolls highlights off and lifts the
mid-tones' contrast. That is what lets a strong sun (`sun_brightness` 1.5 in
the template) make bright, contrasty sunlit rock beside dark shade. The sky,
the fog colours and passes that do not shade in HDR are carried through the
curve's inverse first, so they look as they always did. It applies in frames
drawn with TAA (the renderer's RGBA16F scene target); the editor's view and
other anti-aliasing modes stay LDR. See [Sky and fog](08-sky-and-fog.md) for
the block. `ambient_brightness` (≥ 0) sets the global light with it: a blue
sky light of about 0.4 under a sun of 1.5 is a clear desert day.

**The spawn** (`spawn`). On, the level's spawn point is put on the terrain at
`spawn_at`, moved onto the nearest canyon centreline within 80 m so it lands
on the floor, facing `spawn_yaw`.

The level settings a generator may write are `fog`, `sky`, `view_distance`,
`sun`, `ambient`, `tonemap` and `spawn`; the splice decodes each through the
level's own schema.

### regions (Level)

A level generation doc's environment regions (see
[World generation](#world-generation-environment-regions)). Writes
`regions.yaml` — the resolved set: keys, anchors, bounds, the warp — and
previews as the biome overlay plus one grey weight layer per region.

| Param | Type | Default | |
| --- | --- | --- | --- |
| regions | list | [] | The level's region volumes, synced before every run (sorted by id). Read-only: edit the volumes. Fields: id, environment, generator, priority, transition, anchored, anchor, footprint (a closed spline, world XZ). |
| warp_amount | float | 40 | How far the boundary wanders from the footprint's straight edges, m. 0 keeps them straight. |
| warp_scale | float | 300 | The wavelength of the boundary's wander, m. |
| resolution | float | 0.5 | The union terrain's (region_terrain's) sample spacing, m. Must divide tile_size. |
| tile_size | float | 128 | The world tile grid every region's terrain tiles share: the union is whole tiles from the world origin. |
| margin | float | 64 | With tiled erosion, the overlap round each window, m; window seams are cross-faded over it. |
| erosion | enum | global | `global`: erosion once over the union (rivers cross the whole world). `tiled`: in overlapping windows with fixed per-window seeds, so editing one region leaves distant tiles' heights byte-identical. |
| preview_size | int | 384 | The overlay's longer side in pixels (preview only). |
| seed_level | bool | false | Set by a template that ships a layout (`canyon_savannah`): before the first run, a target level with no region volumes gets this list as region volumes, then the switch turns itself off. |

It also applies the **adjacency table** (built in, plus
`resources/environments/adjacency.yaml`; see
[Environment regions](35-regions.md#4-adjacency-which-environments-may-touch)):
a region left at the default transition (150) blends over the table's width
for each neighbouring environment (the widths it took are in regions.yaml's
`pair_transitions` and in the stage's hash, so editing them in
adjacency.yaml reruns the world), and every forbidden pair whose shares
overlap is a **warning** in `gen check`, `gen run` and the run's log — the
run proceeds. A broken adjacency.yaml refuses the run.

### region_terrain (Level)

One eroded terrain over every region of a world generator. For each region
it loads the preset (the region's `generator`, else
`resources/environments/<environment>.fgen`, else the built-in) and reads its
first enabled `terrain` stage exactly as that stage would — except its size
(the region supplies the extent), its resolution (the regions stage's) and
its seed (the region's own, so two regions of one preset differ). The
preset's coordinates are measured from the region's anchor (the footprint's
centroid unless anchored): its canyon paths land relative to it, and moving
the region volume moves its mesas. Each region's mesa coverage is measured
over its own footprint.

The **union grid** is world-aligned: whole tiles from the world origin
covering every footprint (`extent: footprints`) or footprint plus band
(`bands`), padded to a square. Each region's ground is evaluated only where
it has a share, blended by the regions stage's weights (the overlay's), and
then the shared passes run over the union. Erodibility, drainage strength,
terrace, talus and cliff angles, detail and colours are each region's,
blended per sample; the strata index is the dominant region's (its own
strata list, recorded under `biomes:` in terrain.yaml). The few numbers no
sample can own — drainage iterations, droplet run, talus rounds — are the
**lead** region's: the one covering the most ground (per window, when tiled).
A canyon drawn to a region's edge is carried on straight through the band so
it runs into its neighbour instead of ending in a dam.

With `erosion: tiled` (the regions stage) the union is eroded in windows of
`window` tiles plus the regions stage's `margin` round each, each window with
a seed fixed by its position, cross-faded into its neighbours over the
margin: a tile's heights then depend only on the regions with a share in its
window's square (and, inside a fade, its neighbour's). Rivers may change
course only within a window.

| Param | Type | Default | |
| --- | --- | --- | --- |
| regions | stage | | The regions stage it grows from (and whose resolution, tile size, erosion and margin it uses). |
| seed | seed | 0 | Changes the tiled erosion's window seeds. |
| extent | enum | footprints | What the union covers: the footprints, or footprints plus half a band and the edge meander. |
| window | int | 4 | Tiled erosion's window side, in tiles. |
| mesh_step, collision, texture, material, uv_mode, uv_scale, cook, cook_memory | | | As the terrain stage's. |
| preview_cells | int | 256 | The preview's samples a side at most (always global erosion). |
| far, far_cell, far_blend, far_tile, far_material | | | As the terrain stage's backdrop: each region's own country continued past the union's edge, blended by the shares at the nearest edge point. |
| lead | string | "" | The lead region (volume ID); empty: the one owning the most ground. |
| flow_mask | enum | d8 | d8 or mfd (see above). |
| water_material | string | "" | The material the water surfaces wear (any .fmat with a water effect — a preset such as `water_lake`, `water_tropical`, `water_swamp` or `water_sea`, see [Materials](28-materials.md#water)); empty: the stage writes `gen_<doc>_<stage>_water`, the default water with the stage's wave height. |
| water_texture | texture | water | That written material's texture. |
| water_waves | float | 0.06 | Its swell, metres. |

**Water.** A region whose preset's terrain stage says `water: true` stands
under water wherever its ground (after erosion) is below `water_level`; with
`ponds: true` its closed hollows fill to just below their spill height. A
sample belongs to the region with the largest share there. The stage places
the water the way a level's water is authored — a surface wearing a material
with the `water` effect — as one flat mesh per terrain tile,
`water_<i>_<j>` (collision none, not solid, `chunk: auto`), running a couple
of cells on under the banks so the shoreline is where the ground rises
through the water; and writes `mask_water.png` (depth over 8 m; the ground
material's wet rule reads it) and terrain.yaml's `water:` (each body: sea or
pond, level, bounds, area, region). There is no water volume (nothing in the
engine swims yet).

With `material` (or `far_material`, when it has a backdrop) left empty, the
tiles wear the material of the first enabled `region_material` stage that
names this terrain — `gen_<doc>_<that stage>` (and `_far`).

`lead` (a region's volume ID; empty: the region owning the most ground) sets
the lead region by hand: its erosion numbers, its strata for the strata mask,
and its material-wide look in the region material (varnish, bedding,
banding). Lead with the region that has cliffs.

`flow_mask: mfd` draws the flow mask by multiple flow directions instead of
one downhill neighbour per sample (`d8`, the terrain stage's), and cuts it
under filled hollows (more than a few decimetres of fill) and on ground
flatter than about half a degree. A band and a canyon's mouth leave broad
flats that d8 crosses in ruled 0°/45° lines, which the material's washes and
the wash stones then drew. Heights are the same either way. The
`level_world` and `canyon_savannah` templates set it.

Outputs: everything the terrain stage writes, plus `mask_biome_<env>.png`
per environment (the sum of its regions' shares; listed in terrain.yaml's
`masks`, so `Terrain.Mask("biome_<env>")` and `BiomeAt` read it),
`biomes.png` (the environments' colours mixed by share) and terrain.yaml's
`biomes:` section (environments with colours, regions with their preset,
seed, anchor, bounds and strata, the erosion mode and the lead). One region
whose footprint is a terrain stage's square, anchored at the origin, writes
that terrain stage's files byte for byte.

**Incremental.** A rerun keeps the previous run's tiles and reuses each one
whose inputs (its heights and normal ring, the shared base and quantisation
step, the mesh and cook settings) hash the same — recorded per tile in
`manifest.yaml`'s `tiles:` — without meshing or cooking it. With tiled
erosion each eroded window is kept as `erosion/w_<i>_<j>.bin` and read back
when its inputs are unchanged, and the tiles' base snaps to a multiple of
64 m so a far edit does not move it. With global erosion every region edit
changes every tile, so every tile is recooked. See
[Environment regions](35-regions.md#5-incremental-runs).

### region_material (Materials)

The ground material of a world generator's region terrain: every
environment's own ground, one splat. For each environment the terrain holds
it takes the preset of that environment's first region (by id) — the same
preset its ground grew from — and reads the preset's first enabled
`terrain_material` stage and the `polyhaven_textures` stage that names. It
imports those four layers itself (the same importer and download cache as the
textures stage, so a preset's textures stage is never run per region), then:

1. evaluates that `terrain_material`'s rules over the whole region terrain,
   with that preset's numbers — rock slope, wash flow, cracked cover,
   caprock, wash bars, breakup — exactly as the stage would;
2. multiplies the four weights by the environment's biome mask
   (`mask_biome_<env>`);
3. puts every environment's layers into ONE list, the same Poly Haven
   texture at the same repeat and projection being one layer (two biomes
   that share a rock share its weight), and sums the weights into it.

Four layers or fewer make the ordinary four-layer splat; five to eight make
the [eight-layer splat](28-materials.md#terrain-and-rock) (`weights2` holds
layers 5–8). Cover layers (snow, wet, ash) are layers like the four.

**More than eight is a palette** (up to 32). No splat mixes more than eight,
but a tile only ever sees the few environments within reach of it. The
layers are packed into ONE shared atlas — 8 across, 512 texels a layer, so
32 layers take the eight-layer atlas's 4352×2176 — and per-tile materials
become mandatory (whatever `per_tile` says): each terrain tile, stitch strip
and backdrop ring tile wears a material of the layers that weigh anything
within `tile_margin` of it (four or fewer: a four-layer splat; five to
eight: an eight-layer one), all naming the palette's atlas, with their
weights over an aligned rectangle round the tiles that wear them. There is
no union material. A tile that needs more than eight layers is an error
naming the tile and its layers: share textures between the presets that
meet there, or keep those regions apart. More than 32 distinct layers is an
error that lists them.

Per-layer settings (scale, triplanar, tint, anti-tile, relief) are each
environment's own; a shared layer keeps the first environment's (by name).
The material-wide settings — height blend, normal strength, AO, the strata
ramp, bedding, wall normal, macro variation, varnish, detail normal — are
the **lead** environment's (the lead region's, the one owning most of the
union), and so is the strata ramp (terrain.yaml's strata are the lead's): a
triplanar layer of any other environment has its `layerN_strata` off, so a
savannah's rock is not banded with the canyon's beds.

The far backdrop (when the terrain has one) gets `<material>_far`: each
environment's backdrop rules (as `terrain_material`'s far material) blended
by the biome shares at the nearest union sample — the shares the backdrop's
heights are blended by.

| Param | Type | Default | |
| --- | --- | --- | --- |
| terrain | stage | | The region terrain (a `region_terrain` stage). |
| offline | bool | false | Use only Poly Haven textures already downloaded. |
| preview_layers | bool | false | Preview only: one grey picture per layer's weight as well. |

Outputs: `weights.png` (layers 1–4) and `weights2.png` (5–8, when there
are more than four), `ground.png`, `height.png`, `strata.png`,
`far_weights.png` (+ `far_weights2.png`), `region_material.yaml` (what a
`terrain_material` writes, plus `weights2`, `far_weights2` and `layers`:
each layer's texture, Poly Haven id, role, environments, scale and share),
and `materials/gen_<doc>_<stage>.fmat` (+ `_far`). The preview shows the
layers in false colour (the note lists which colour is which, with each
layer's share) and the ground colour on the terrain's meshes.

The stage's hash folds in every environment's preset `terrain_material` and
textures params, so editing a preset's ground reruns only the material.
It stands in for the presets' `terrain_material` stages: a preset placement
stage expanded per region that names its preset's `terrain_material` is
rewritten to it.

Its own `paint` list is `terrain_material`'s, over the union's layers. A
`target` is a role, for every environment's layer of that role (a painted
trail of `gravel` turns to the desert's gravel on desert ground and the
savannah's on savannah, by their weights there), or one layer by its Poly
Haven id or albedo texture name. Paint is world-level: the presets'
`terrain_material` paint lists are not read here ([Terrain paint](36-terrain-paint.md)).
As in `terrain_material`, the blended weights before paint are kept
(`prepaint.bin`), so a stroke reruns only the paint and the maps.

## Outputs on disk

```
resources/generated/<generator>/
  manifest.yaml          what each stage last made, and from which hash
  <stage>/...            that stage's files
  <stage>/placements.yaml
```

The manifest is how a run knows what is stale: a stage whose hash (its kind,
its kind's version, its parameters with defaults spelled out, its seed and its
upstream stages' hashes) matches its entry, and whose files are all still
there, is skipped. Delete a stage's folder to force it to rerun. The output
folder is the generator's: files in it are replaced and deleted on later runs.

A release ships whatever in `generated/` a level names (the cooked meshes), and
leaves the rest (heightmaps, manifests) behind. **Window > Unused Assets** never
offers files under `generated/`; the generator manages them. The `.fgen`
documents themselves are editor content and do not ship.

## From the command line

```
foundry gen run <name|file.fgen> [-force] [-stage id]... [-no-splice] [-workers N] [-q]
foundry gen check <name|file.fgen>        validate; say what a run would run
foundry gen list [-kind name]             stage kinds, their params, templates
foundry gen new <name> [-template name] [-force]
foundry gen world <level>                 make the level's world generator (<level>_world)
foundry gen env list                      environment presets: built-in, file, or both
foundry gen env write <name>|-all         write a built-in preset's file, only if absent
foundry gen paint <name> -layer L -disc x,z,r   brush strokes into a paint layer (36-terrain-paint.md)
```

A document with a `regions` stage takes its level's region volumes before
`run` (and writes itself back when they changed); `check` reports when they
differ.

`run` places into the document's level by rewriting the level file, as the
editor does for a level that is not open. Placements marked `chunk: auto` take
the chunk whose footprint they stand in, as they do in the editor. Don't run it on a level that is open
with unsaved edits in an editor. `-workers` bounds each stage's threads, which
also bounds its memory (see below). Ctrl+C cancels cleanly.

## Performance

Measured on the dev box (20 threads, WSL), with `noise_preview`:

| | |
| --- | --- |
| Preview, 96 cells a side | ~7 ms |
| 256 m at 2 m, 4 tiles, cooked | 0.9 s |
| 1024 m at 1 m, 64 tiles of 33k triangles, cooked | 31 s, 3.6 GB peak memory |
| the same with `-workers 4` | 40 s, 1.2 GB |

Cooking dominates. Each tile cooking at once holds its whole mesh, DAG and
collision in memory, so for big runs lower `-workers` before you run out of
memory.

With `terrain` (the template's kilometre with its canyon, defaults otherwise):

| | |
| --- | --- |
| Preview, 256 samples a side | ~0.3 s |
| 1024 m at 1 m: 1025² field, 64 tiles of 34k triangles | 20 s, 0.5 GB peak |
| 1024 m at 0.5 m: 2049² field, 64 tiles of 134k triangles | 74 s, 1.9 GB peak |

At 0.5 m the field itself takes 8 s (drainage 2.7, droplets 1.4, talus 1.2,
masks 1.5) and the cook the rest. The terrain stage bounds how many tiles cook
at once by `cook_memory`, so it does not need `-workers`.

With `rocks` (dev box, 20 threads; one variant at a time, all threads on it):

| preset | budget | triangles | meshed | cell | mesh | decimate | bake | cook | .fnm | collision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| pebble | 800 | 800 | 1740 | 0.3 cm | 0.01 s | 0.03 s | 0.01 s | 0.05 s | 0.1 MB | none |
| rubble | 3000 | 3000 | 6428 | 0.9 cm | 0.21 s | 0.11 s | 0.05 s | 0.10 s | 0.1 MB | hulls |
| boulder | 5000 | 5000 | 11036 | 3.0 cm | 0.63 s | 0.19 s | 0.27 s | 0.21 s | 0.2 MB | hulls |
| boulder | 15000 | 15000 | 33516 | 1.7 cm | 1.76 s | 0.64 s | 0.70 s | 0.35 s | 0.6 MB | hulls |
| boulder | 50000 | 50000 | 112200 | 1.0 cm | 5.65 s | 2.31 s | 2.07 s | 0.87 s | 1.6 MB | hulls |
| boulder | 150000 | 150000 | 300252 | 0.6 cm | 28.91 s | 5.81 s | 6.40 s | 2.29 s | 4.4 MB | hulls |
| ledge_slab | 60000 | 60000 | 99220 | 1.6 cm | 1.81 s | 1.86 s | 1.47 s | 1.35 s | 2.0 MB | hulls |
| mesa_cap | 300000 | 304784 | 304784 | 3.4 cm | 15.18 s | 0.00 s | 4.11 s | 4.65 s | 10.0 MB | mesh |
| pillar | 300000 | 299976 | 299976 | 2.0 cm | 20.21 s | 0.00 s | 4.36 s | 5.21 s | 9.5 MB | mesh |
| cliff_chunk | 150000 | 149999 | 315688 | 3.0 cm | 20.80 s | 6.28 s | 5.64 s | 1.99 s | 6.9 MB | mesh |
| cliff_chunk | 600000 | 615044 | 615044 | 2.3 cm | 44.82 s | 0.00 s | 10.87 s | 15.11 s | 22.1 MB | mesh |
| cliff_chunk | 1200000 | 738580 | 738580 | 2.1 cm | 32.41 s | 0.00 s | 12.96 s | 19.05 s | 26.0 MB | mesh |

Most of the time is making the shape (evaluating its distance field), then the
cook. The 1.2M row hit the mesher's 512-cell limit, so it stopped at 739k.

Hero pieces are worth the wait once: a stage whose parameters have not changed
is skipped, and the preview makes its one variant at `preview_triangles`.

With the placement stages (the dev box, WSLg on Mesa d3d12, 1920×1080, TAA, the
virtual shadow map): a kilometre of canyon at 0.5 m (64 tiles), 13,900 scattered
rocks (2,500- and 600-triangle variants) and 1,200 cliff chunks and ledges
(120k and 12k triangles). Placing it all takes about 1.5 s; the level loads in
2.3 s. The frame, looking at a wall / at the empty sky / over the whole canyon:

| | wall | sky | overview |
| --- | --- | --- | --- |
| before the live-tile list | 189 ms | 166 ms | 205 ms |
| after | 79 ms | 46 ms | 127 ms |

The Nanite cull used to launch a thread for every leaf window of every
placement every pass, in view or not: 1,200 hero chunks are 266,000 of them.
It now runs over the windows of the instances each pass keeps
(`FOUNDRY_NANITE_LIVE_TILES=0` restores the old dispatch). What a placement
still costs where it is in view is its windows, so the budget rules stand:
scatter light variants by the thousand, keep hero pieces to a few hundred or a
thousand, and judge final frame times on real hardware (WSLg's GPU path is
several times slower than a GTX 1070).

With the **Desert canyon** template (dev box, `-workers 8`, a fresh project):

| | |
| --- | --- |
| from nothing, empty Poly Haven cache | 1 min 50 s, 1.2 GB peak |
| from nothing, cache warm | 1 min 44 s |
| nothing changed | 0.9 s |
| a placement or material change | 1–2 s |
| output on disk | 213 MB generated (100 MB of it the backdrop), 128 MB models, 437 MB textures |
| level load | about 8 s |

The cooks dominate: the terrain's 16 tiles (16 s), the backdrop's 192 tiles
(15 s) and the cliff scans (42 s, most of it decimating `coastal_cliff_02`).
On WSLg the frame is GPU-bound at 110–290 ms (judge it on real hardware).

**Performance tips.**

- Iterate on the 2D preview and on single stages (**Run Stage**): placement
  and material stages rerun in about a second; only the terrain and the
  imports are slow, and they rerun only when their own parameters change.
- Scatter by the thousand only light variants (`max_triangles` on a rule
  picks the imports' lite copies); keep wall chunks to hundreds (`max_chunks`)
  and spend them near where the player is (`focus`).
- The backdrop at `far_cell` 16 is a quarter of the disk and cook of 8,
  coarser near the edge; at 32 it is fine for maps the player cannot climb
  out of.
- Turn on texture compression for a canyon (a generated canyon is about 1 GB
  of textures uncompressed): it also compresses the splat atlases.
- The vegetation stage's biome volumes: the GPU stall seen on WSLg in phase 7
  of the canyon work does not reproduce in this tree, on WSLg or natively on
  a GTX 1070 (docs/regions/PHASE_6.md); natively they cost 2–3.6 ms a frame
  (camera) plus ~0.6 ms (shadows) for the savannah's dense grass. The
  savannah preset grows its grass with them; the `desert_canyon` template
  still ships its own `plants` stage off (unchanged).
- Every placement costs CPU each frame (about 1 ms per thousand in view on a
  GTX 1070 machine): keep the small, numerous pieces (stones, bushes) to a
  few thousand.

## Writing a stage

(For engine developers.) A stage kind is one Go file in
`internal/procgen/stages/` that calls `procgen.RegisterStage` from `init`:

```go
procgen.RegisterStage(procgen.StageKind{
	Name: "terrain", Label: "Terrain", Category: "Terrain", Version: 1,
	Doc:    "...",
	Params: []procgen.ParamDef{{Name: "size", Type: procgen.ParamFloat, Default: procgen.Float(1024), Doc: "..."}},
	Run:     func(rc *procgen.RunContext) error { ... },          // required
	Preview: func(pc *procgen.PreviewContext) (*procgen.PreviewData, error) { ... },
	Load:    func(res *procgen.StageResult) (any, error) { ... }, // rebuild from files
})
```

`Run` reads `rc.Params` (complete and clamped), seeds from `rc.RNG()`, spreads
work with `rc.Parallel` / `rc.ParallelProgress`, checks `rc.Err()` in its loops,
writes files with `rc.WriteFile` / `rc.Path`, places with `rc.Place`, and sets
`rc.Artifact` for downstream stages. `Preview` returns image, point, line and
mesh layers the editor draws without knowing what they are. Bump `Version`
whenever the output changes for the same parameters. No switch anywhere else
needs a case. See `docs/canyon/PHASE_0.md` for the full API.
