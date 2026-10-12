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
   - the walls: `walls`' `max_chunks`, `spacing` and `scale`, or `cliffs`'
     asset list (Browse… opens the Poly Haven browser);
   - the light: `air`'s `sun_yaw`, `sun_pitch`, `sun_brightness`,
     `exposure` and `ambient_brightness`;
   - the plants: the `trees`, `deadwood` and `bushes` rules of `scatter`.
   A rerun that touches only placement or materials takes about a second.

What it makes, stage by stage:

| Stage | Kind | What |
| --- | --- | --- |
| `tex` | polyhaven_textures | red sand, cracked ground, river pebbles, and `cliff_side`, a layered red cliff face, as the four ground layers |
| `ground` | terrain | 512 m of mesa country at 0.5 m with a stepped canyon through it; a 3 km backdrop of the same country round it (`far`) |
| `ground_mat` | terrain_material | the splat over the masks; caprock on the high flats, bedding relief and stronger normals on the walls, and the backdrop's material |
| `cliffs` | polyhaven_rocks | scanned cliff faces from 5 to 40 m: the walls' cladding |
| `boulders`, `stones` | polyhaven_rocks | Namaqualand boulders (with light copies for scatter), stone and rock sets split into pieces |
| `trees`, `deadwood`, `bushes` | polyhaven_plants | quiver trees, a dead quiver trunk, rooibos and shrubs |
| `walls` | cliff_wall | the scans up the canyon's walls, nearest the spawn first, colour-matched to the bare rock |
| `scatter` | scatter | talus under the walls, boulder fields, stones in the washes, the plants on the flats |
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

A vec2's `y` is written `"y"`, quoted: YAML 1.1 reads a bare `y` as `true`.

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
| avoid | stage | "" | A cliff_wall (or another scatter) stage whose footprints these rocks keep clear of. |
| cliff_threshold | float | 0.5 | The cliff mask level that counts as a cliff for the cliff-base weight. |
| margin | float | 2 | Footprints keep this far in from the terrain's edge. |
| preview_max | int | 20000 | The most rocks a rule places in the preview (the run uses Max rocks). |
| preview_triangles | int | 240 | The 3D preview draws each rule's rocks as one proxy of its previewed variant, decimated to this many triangles. |

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
| ground | stage | "" | A terrain_material stage: the chunks blend into its ground colour at their feet and match toward it. |
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
| cap_rock | float | 0 | Bare rock on the high flats — a mesa's caprock, slickrock with sand in its hollows — rather than sand: how much, in patches. 0: the flats are sand and cracked ground at any height. |
| cap_height | vec2 | (0.5,0.7) | Caprock fades in between these fractions of the terrain's height range. |
| bedding | float | 0 | Ledges and laminations bent into the rock's normals on steep walls (the splat's bedding): each bed weathers back under a sharp lip, so the heightfield's smooth walls read as layered rock in raking light. The recess as a fraction of a bed; 0: none. |
| bedding_spacing | float | 1.2 | The beds' typical thickness. |
| wall_normal | float | 1 | Multiplies the rock layer's normal map on walls (its side projections) on top of Normal strength. |

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
```

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
- The vegetation stage's biome volumes cost about 9 ms a frame on WSLg and
  stalled its GPU in some views; the template ships it disabled, with the
  scanned plants placed by scatter instead.

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
