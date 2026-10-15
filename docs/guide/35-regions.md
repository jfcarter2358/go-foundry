# Environment regions

A generated world can hold several environments at once — a desert canyon
running into savannah, a forest climbing to alpine scree — grown in ONE pass
and blended into each other across a band, with no seam. You mark where each
environment goes with **region volumes** in the level; the level's **world
generator** grows them all. This page is the whole workflow, start to finish.
The stage-by-stage detail is in [Generators](33-generators.md#world-generation-environment-regions);
the volume block itself is in [Volumes](13-volumes.md#environment-regions).

## Quick start: a canyon running into savannah

One command makes a whole two-environment world to start from:

```
foundry gen new canyon_world -template canyon_savannah
foundry gen run canyon_world
```

(or **File > New** in the Generator, template **Canyon into savannah**, saved
as `canyon_world`). The template is a world generator that already knows its
layout: a `desert_canyon` region (x −512..0) and a `savannah` region
(x 0..512), both 768 m deep, meeting over the 200 m band the adjacency table
gives that pair. Its first run **writes those two region volumes into the
level** — making the level `canyon` if there is none — and from then on the
level leads, exactly as if you had drawn them: move a footprint in the level
editor and the next run follows. (A level that already has region volumes
keeps its own; the template's layout is only a starting point.)

The run grows the eroded terrain over both (the canyon leads its erosion and
its rock look), an eight-layer ground material, each region's rocks, plants
and the savannah's grass, and the level's sky, haze, sun and a spawn on the
savannah looking at the canyon's mesas. The first run downloads the Poly
Haven scans and textures (a few hundred MB, cached) and takes about five
minutes; a rerun with nothing changed takes a second.

## The pieces

| Piece | Where | What it is |
| --- | --- | --- |
| Region volume | the level, a volume's `region:` block | A polygon footprint, the environment it grows, a priority, a transition width. **The level is the source of truth.** |
| Environment preset | `resources/environments/<name>.fgen` | An ordinary generator: its `terrain` stage shapes the region's ground, its `terrain_material` paints it, its placement stages run once per region. |
| World generator | `resources/generators/<level>_world.fgen` | The level's generation document: `regions` → `ground` (region_terrain) → `ground_mat` (region_material), plus every region's expanded stages. |
| Adjacency table | built in, + `resources/environments/adjacency.yaml` | Which environments may touch, and over how wide a band. |

## 1. Mark the regions

In the level editor, add a volume (or pick one) and tick **This volume is an
environment region** in its **Environment Region** group. Choose the
**Environment**, then shape the footprint in the 3D view: drag a point,
**Ctrl+click** an edge to add one, **right-click** a point to delete it. The
outline is drawn in the environment's colour.

- **Priority** decides overlaps: the higher region takes its share first.
- **Transition (m)** is the blend band, centred on the footprint's edge.
  Leave it at the default (150) and the band takes the adjacency table's
  width for each neighbour — 200 m against a savannah, 150 m against dunes —
  see [Adjacency](#4-adjacency-which-environments-may-touch).
- **Anchor** is where the preset's own coordinates (its canyon paths) are
  measured from.

Footprints may be concave; crossing edges are refused.

## 2. Make the world generator

Click **Open World Generator** in the region group (or **File > Generate
World for <level>…** in the Generator, or `foundry gen world <level>`). The
first time, it writes `<level>_world.fgen` from the `level_world` template
and the built-in presets that have no file yet (an edited preset is never
overwritten).

The `regions` stage holds a **copy** of the level's regions, synced before
every run and, while the level is open, twice a second. Its preview is the
**biome overlay**: each environment's colour mixed by its share.

## 3. Work in the Generator

With a world generator open, the right column shows a **Regions** list above
the stage's parameters:

- each region's colour, id, environment, priority and band; a `!` marks one
  whose neighbour the adjacency table forbids;
- **click** a region to select it: it is highlighted in the 2D view and the
  `regions` stage is selected, so its overlay is what the view shows;
- **Select in level** selects its volume in the level editor and frames it;
- **Weight overlay** draws the selected region's own share (0..1) over the
  view — where its ground, materials and placements will be — or the biome
  colours with no region selected;
- with the level open, the selected region's footprint has handles in the 2D
  view: **drag** a point, **Ctrl+click** an edge to add one, **right-click**
  a point to delete it. Every edit goes into the LEVEL (one undo step per
  drag — Ctrl+Z in the level editor takes it back) and the copy in the
  generator follows at once.

If the level is not open the list is read-only, with an **Open level**
button.

Run as usual (**Run**, or `foundry gen run <level>_world`). **Run > Run
Region** (`-region <id>`) reruns one region's own stages and splices only its
placements.

## 4. Adjacency: which environments may touch

Some environments should never meet: desert straight into tundra reads as a
mistake. The adjacency table says, for each pair, either the band it wants
or that it is **forbidden**, with an environment to put between:

| Pair | Rule |
| --- | --- |
| desert_canyon ↔ savannah | 200 m |
| desert_canyon ↔ dunes | 150 m |
| savannah ↔ pine_forest | 300 m |
| pine_forest ↔ alpine | 250 m |
| coast ↔ any | 100 m |
| desert_canyon (or dunes) ↔ alpine, tundra | forbidden — insert savannah |
| volcanic ↔ pine_forest | 150 m |
| tallgrass_prairie ↔ oak_savannah | 300 m |
| oak_savannah ↔ pine_forest | 300 m |
| tallgrass_prairie ↔ savannah | 200 m |
| alpine ↔ tundra | 200 m |
| pine_forest ↔ tundra | 250 m (the treeline) |
| wetland ↔ pine_forest, tallgrass_prairie, oak_savannah, tundra, volcanic | 150 m |
| wetland ↔ savannah | 200 m |
| wetland ↔ desert_canyon, dunes | forbidden — insert savannah |
| tallgrass_prairie ↔ pine_forest | 300 m |
| tallgrass_prairie ↔ alpine | 250 m (foothills out of the plains) |
| tallgrass_prairie ↔ desert_canyon, dunes | 200 m (badlands, sand hills) |
| dunes ↔ savannah | 200 m |
| volcanic ↔ alpine, savannah | 200 m |
| volcanic ↔ desert_canyon, dunes | 150 m |

Any pair not listed is allowed, each region blending over its own transition.

**Your own rules.** A project overrides the table in
`resources/environments/adjacency.yaml`. Each entry replaces whatever the
table said about its pair; `remove: true` drops a built-in rule:

```yaml
pairs:
  - between: [desert_canyon, savannah]
    transition: 250                      # a wider band than the built-in 200
  - between: [desert_canyon, pine_forest]
    forbidden: true
    intermediate: savannah               # what "Insert intermediate region" adds
    note: "no conifers off the canyon rim"
  - between: [coast, "*"]                # "*" is any environment
    remove: true
```

`foundry gen env adjacency` prints the table in effect (built-in plus your
file), and the same table as YAML you can start a file from.

**When is a pair adjacent?** When their shares overlap — somewhere both own
more than 1% of the ground — on a coarse raster over the regions. That
catches regions that touch, overlap, or sit across a narrow gap (a gap is
shared by the nearest regions), and ignores two that overlap only under a
third with a higher priority.

**Warnings, not errors.** A forbidden pair that touches is reported:

- in the region volume's inspector in the level editor (checked in the
  background a moment after the regions stop moving);
- in the Generator's region list;
- by `foundry gen check` and `foundry gen run` (in the log, and again by the
  run's summary) — the run still goes ahead.

**Insert intermediate region.** Next to each warning whose rule names an
intermediate environment is **Insert <env> region**. One click adds a region
volume of that environment covering the whole band where the two meet (with
room for its own band), with a priority above both and the default
transition — so it blends into each over the table's width for that pair.
It is one undo step. Afterwards the two forbidden neighbours no longer touch
and the warning clears. Where the two meet in two separate places, two
regions are added. Shape the new footprint like any other.

## 5. Incremental runs

Editing one region should not cost a whole world. Two things help:

- **Per-region stages.** Placement stages run once per region; editing a
  region's *preset* reruns only that region's stages (see
  [Generators](33-generators.md#world-generation-environment-regions)).
  Moving a footprint changes every region's shares, so every region's
  placement stages rerun.
- **Per-tile terrain.** The `ground` stage keeps its tiles between runs.
  Each terrain tile is hashed from exactly what it is made of — its heights
  (and the ring its normals read), the shared base and quantisation step,
  the mesh and cook settings — and the hashes are recorded in the output
  folder's `manifest.yaml` (`tiles:`). On a rerun a tile whose hash has not
  changed is neither meshed nor cooked: its `.fnm` is not touched. With
  **tiled erosion** (the regions stage's `erosion: tiled`) each erosion
  window's eroded ground is kept too (`erosion/w_<i>_<j>.bin` in the stage's
  folder) and read back when its inputs have not changed, so editing a far
  region re-erodes and recooks only the tiles within reach of it (its band,
  the edge meander, the erosion margin).

**Global erosion stays whole-stage.** With `erosion: global` (the default)
drainage routes the whole world and the droplets and talus see all of it, so
any region edit changes every sample a little, and every tile is recooked.
That is the price of rivers that cross the whole world; switch to `tiled`
while you iterate on a large world, and back to `global` for a final run if
you want the long rivers.

A cancelled or failed run never leaves a tile marked fresh that is not: the
manifest vouches only for tiles that were finished, and `-force` rebuilds
everything from nothing.

## The environments

Eleven built-in presets (`foundry gen env list` lists them with their overlay
colours; `foundry gen list` too):

| Environment | Landform | Ground | Life (all Poly Haven; *grass* = the grass instancer) |
| --- | --- | --- | --- |
| `desert_canyon` | red sandstone mesas cut by a stepped canyon | red sand, cracked ground, river pebbles, cliff_side | the `desert_canyon` template's scans, with fewer wash stones and bushes (a region shares the frame) |
| `savannah` | wide rolling plains (45 m over ~800 m swells), shallow dry washes | gold dry grass, baked earth, stony wash soil, boulder rock | searsia and quiver trees in clumps, searsia_burchellii as the acacia stand-in in clumps, a dry-season *grass* sward of grass_medium_02, shrubs, granite |
| `pine_forest` | rolling hills (55 m), rising from 0 m inside the footprint | pine needles, lichen rock | a close stand of big fir_tree_01, fir_sapling_medium, fallen trunks, stumps, mossy boulder sets; *grass*: a fern_02 understory and clearing grass |
| `alpine` | ridged ranges with U-valleys, scree aprons | scree, olive turf, grey rock (rock_surface, 16 m repeat), **snow** above 70–110 m | boulders, rock faces, mountainside crags, scree debris; *grass*: short meadow tufts |
| `coast` | shoreline toward +x, sea at −1.5 m, a narrow beach under a ~23 m wave-cut cliff | beach sand, **wet** damp sand at the water, lichen rock | coast rock blocks and reef rocks, island_tree_02, sorrel; *grass*: dune grass behind the beach, tufts down onto it |
| `dunes` | transverse dunes 22 m high with slip faces and ripples | the desert's sands (no new layer) | sparse dry tussocks, a few granite boulders |
| `volcanic` | a cinder cone with crater and lava lobes | near-black cinders, bare lava, basalt, grey **ash** round the vent | SDF basalt boulders, burnt trunks and stumps, sparse grass on the old flows |
| `tundra` | low swells, frost hummocks, thermokarst ponds | olive turf, frozen ground, lichen rock, **snow** patches | dwarf shrubs, erratic boulders, mossy rock sets; *grass*: short sparse sedge tufts |
| `wetland` | flat ground at a water table (−0.25 m), pools and channels | grass, leaf mud, lichen rock, **wet** mud at the water | weeds, shrubs, stunted trees, logs; *grass*: dense reed beds along every shore and in the shallows (grass_medium_01 ×6–9: Poly Haven has no reeds), tussock sedge |
| `tallgrass_prairie` | gently rolling plains | green-gold grass, dry grass, boulder rock | *grass*: a continuous 1–2 m sward of grass_medium_01's tall and mid clumps, grass_medium_02 dry patches, dandelion, celandine and heliophila drifts; flowering shrubs |
| `oak_savannah` | gently rolling plains | the prairie's | island_tree_01 and jacaranda_tree (Poly Haven has no oak) in clumps over the prairie's *grass*, sparser and shorter |

Edit a preset in `resources/environments/<name>.fgen` (write it first with
`foundry gen env write <name>`); every region of that environment follows on
the next run. Built-in presets are written only when no file of that name
exists.

**Plants are Poly Haven, never the engine's foliage biomes.** Every tree,
shrub, fern, flower and grass clump is a `polyhaven_plants` import. Trees,
shrubs, rocks and the odd large plant are scenery placed by `scatter` rules.
The grass, reeds, sedge, dune grass, ferns and wildflowers are a
`grass_field` stage (`sward` in every preset that has one) drawn by the
**grass instancer**: per terrain tile one compact instance file and one
grass volume, culled and thinned per 16 m cell, so a prairie is a continuous
sward of ~1.2 M clumps over 512 m rather than a few thousand scenery clumps
over a grass texture (docs/regions/PRESETS_2B.md has the design and the
cost). The player's `grass_density`, `grass_distance` and
`grass_shadow_distance` settings scale it.

**Thin foliage.** The presets' plants stages set `thin_foliage`: a needle
tree or a grass clump made of thousands of tiny cards is decimated by keeping
whole cards (fewer, enlarged) instead of collapsing their edges, and its
cutout texture (`_cov`) keeps its alpha coverage down the mip chain. Without
both a scanned fir is a bare skeleton of branches beyond ten metres.

**Shared layers and the base level.** The presets name their ground layers
from one catalogue (procgen/stages/environments_shared.go), so where two
meet a band tile needs at most eight layers: all eleven together are 22
palette entries, and every pair the adjacency table allows fits a tile.
Every preset meets its neighbours at **0 m** — the high ones (pine hills,
alpine, volcanic, dunes) rise from it inside their footprint with
`edge_rise` — and standing water lies below it (the coast's sea at −1.5 m,
the wetland's table at −0.25 m), so water never floods a neighbour's band.
Three presets meeting at one tile can exceed eight layers; see PRESETS_2.md
for the triples that fit.

**The sea runs to the horizon.** A coast's sea body that reaches the union's
edge is carried over the far backdrop as flat water sheets
(`water_far_<side>_<k>`), so the water does not end in a straight line at
the world's edge. Only regions with a coastline get them.

## The world generator's own knobs

On the `ground` (region_terrain) stage:

- **Lead region** (`lead`): the region whose erosion numbers (drainage,
  droplets, talus) the whole world erodes with, whose strata the masks
  index, and whose material-wide look (varnish, bedding, strata banding) the
  ground material takes. Empty: whichever region owns the most ground. Set it
  to the region with cliffs: `canyon_savannah` leads with the canyon.
- **Flow mask** (`flow_mask: mfd`): flow shared among all lower neighbours,
  with no flow under filled hollows or on dead flats. A band and a canyon's
  mouth leave broad flats that the classic single-neighbour mask (`d8`)
  crosses in ruled 0°/45° lines — which the ground's washes and the wash
  stones then draw. New world generators use `mfd`.

On the `ground_mat` (region_material) stage:

- **Per-tile materials** (`per_tile`): a world of two environments is one
  eight-layer ground material, and an eight-layer tile costs the GPU about
  half again a four-layer one — yet only the tiles in the transition band
  ever weigh more than four layers. With this on, the stage also writes a
  four-layer material for each set of layers the tiles use
  (`gen_<doc>_ground_mat_l1234`, `…_l5678`, a few mixed ones such as
  `…_l1567` at the band's edge, and `…_far_l…` for the backdrop) and dresses
  every tile, stitch strip and backdrop tile whose layers fit in four in it;
  the band keeps the eight-layer material. The picture does not change: a
  four-layer material is the eight-layer one less layers that weigh nothing
  on its tiles, sharing its textures, weights and settings, so the line
  between a four-layer tile and an eight-layer one cannot be seen (a GL test
  draws across it and compares pixel for pixel). It pays where a view is
  mostly ONE biome's tiles: on the `canyon_savannah` world's savannah and
  spawn views about 1 ms of a GTX 1070's frame; views of the band itself
  gain nothing, because the band's tiles must stay eight-layer
  (docs/regions/PHASE_6.md, "Per-tile materials"). New world generators
  switch it on; existing ones keep the single material until you tick it.
- **Tile margin** (`tile_margin`, 32 m): how far past a tile's edge a layer
  still counts as the tile's — the reach of the weights' mipmaps from far
  off. Lower it and more tiles go four-layer, at the risk of a faint line on
  a four-layer tile's edge seen from a distance.

The re-dressed tiles are placed by the material stage
(`gen.<doc>.ground_mat.tile_i_j`, replacing `gen.<doc>.ground.tile_i_j`);
their meshes are the terrain stage's, unchanged.

## More than eight ground layers: the palette

Each environment brings up to seven ground layers (its four rules and its
cover layers, below), and textures shared between presets (the same Poly
Haven id at the same scale) are one layer. A world of eleven environments
easily needs more than the eight one splat can mix. It does not have to fit:
past eight, the layers become a **palette** of up to 32, packed into one
shared atlas (8 across, 512 texels a layer — 32 layers take the same video
memory as an eight-layer world's atlas), and **every tile** wears a material
of the at most eight palette layers that weigh anything within
`tile_margin` of it — four-layer deep inside one environment, eight-layer
where two meet. This happens on its own; `per_tile` is ignored. Tiles of
different sets meet with no seam: they read the same weights bytes and the
same atlas texels for every layer they share.

What it cannot do is a tile that needs more than eight — three environments
of four distinct layers each meeting in one tile, say. The run stops and
names the tiles and their layers. Make the presets that meet there share
textures, keep three-way junctions apart, or lower `tile_margin`.

## Landforms, height and water

An environment preset's `terrain` stage can grow more than canyon country
([Generators: landforms](33-generators.md#landforms-beyond-canyon-country)):
ridged mountains with glacial valleys, dune fields shaped by a wind, a
coastline with a beach and sea cliffs, volcanic cones with craters and lava
lobes, frost-heave hummocks with pond hollows. Three settings on it only
mean something in a world of several regions:

- **Height compatibility** (`base_elevation`, `edge_rise`). Two regions at
  very different heights blend across their band into a ramp — an
  escarpment steeper than either. With `edge_rise` set, a region stands at
  its `base_elevation` across the band and its own relief rises back over
  `edge_rise` metres inside its footprint: an alpine region's ranges climb
  out of the savannah instead of meeting it as a cliff. Give the regions
  that meet it the same base level (their lowland's `base_height`).
- **Water** (`water`, `water_level`). The region's ground below the level
  stands under water: a coast's sea, a wetland's flooded channels and pools.
- **Ponds** (`ponds`, `pond_min_depth`, `pond_min_area`). The region's closed
  hollows — the hummock landform's pond hollows, kettle holes between
  ridges — fill to just below their rims.

The water is placed by the world generator's `ground` stage as flat water
surfaces per terrain tile (`gen.<doc>.ground.water_i_j`, wearing
`gen_<doc>_ground_water`: the engine's `water` effect over the `water`
texture, or the stage's `water_material`), and written as a depth mask the
ground material's wet rule reads.

## Ground covers: snow, wet ground, ash

A preset's `terrain_material` can lay **cover layers** over its four rules:
snow above a snow line on gentle ground (`snow`, `snow_height`,
`snow_slope`, `snow_breakup`), wet ground under and beside the region
terrain's water (`wet`, `wet_distance`, `wet_height`), ash round the
terrain's volcanic vents (`ash`, `ash_radius`, and `ash_hole` for an
apron round the cone's foot instead of a cap), and bare lava on the
terrain's lava lobes (`lava_lobes`, onto the preset's cracked layer). In a
world of several regions set `snow_reference: region`: `snow_height` is then
a pair of fractions of the environment's own height range, so a massif's
snow line does not depend on how big its footprint is or what it borders
(world metres assumed a 0 m base and a region big enough to climb past the
line). Each is a layer of its own
from the preset's textures stage, deduped and weighted by biome like the
others; the backdrop carries snow and ash too, so far ranges keep their
snow line.

## Command line

```
foundry gen new <level>_world -template canyon_savannah
                                           a two-region world; its first run writes the regions into the level
foundry gen world <level>                  make the level's world generator (once)
foundry gen env list                       environment presets: built-in, file or both
foundry gen env write <name>|-all          write a built-in preset, only if absent
foundry gen env adjacency                  the adjacency table in effect
foundry gen check <level>_world            validate; forbidden pairs as warnings; what a run would run
foundry gen run <level>_world              sync the regions, run what is stale, splice
foundry gen run <level>_world -region <id> one region's stages, spliced on their own
foundry gen run <level>_world -force       everything from nothing
```

## Files

| File | What |
| --- | --- |
| `resources/levels/<level>.yaml` | the region volumes (`region:` blocks) |
| `resources/generators/<level>_world.fgen` | the world generator (its `regions` stage is a synced copy) |
| `resources/environments/<name>.fgen` | environment presets |
| `resources/environments/adjacency.yaml` | the project's adjacency rules (optional) |
| `generated/<level>_world/manifest.yaml` | per stage: hash, files, and the terrain's per-tile hashes |
| `generated/<level>_world/regions/regions.yaml` | the resolved regions, with each one's pair widths (`pair_transitions`) |
| `generated/<level>_world/ground/erosion/` | kept erosion windows (tiled erosion) |
