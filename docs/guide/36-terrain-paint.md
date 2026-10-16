# Terrain paint

A generator grows its ground from rules: grass on gentle slopes, gravel in the
washes, rocks under cliffs. **Paint** is how you say where by hand: a meadow
*here*, a path kept clear *there*, a gravel trail along it. You paint onto the
generator's 2D view or straight onto the terrain in the level editor's 3D
view, and the stages read what you painted as one more input to the rules
they already have. With the generator's level open, a stroke shows in the
level about half a second after you let go.

Paint is an **input** to the generator. It is never a placement. A stroke
changes a paint layer; the next run grows the grass from it. So a rerun never
loses paint, and painted cover still obeys the land: grass painted up a cliff
does not grow on the cliff, because the rule's slope band still says no.

The stage-by-stage detail is in [Generators](33-generators.md). This page is
the workflow and the formats.

## Quick start: a meadow with a path through it

1. Open a generator with a terrain and a `grass_field` stage (the
   `tallgrass_prairie` environment preset has one), and select the terrain
   stage, so the 2D view shows its shaded relief.
2. Tick **Paint** in the 2D view's bar. Click **New layer…** and make
   `meadow`, kind `density`. Drag over the relief to paint a patch.
3. Make a second layer, `path`, kind `exclude`, and paint a line across the
   meadow.
4. In the `grass_field` stage, add a rule for the dense grass and set its
   **Paint layer** to `meadow`. Add `path` to the stage's **Exclude paint**
   list.
5. Save (Ctrl+S) and run (F5). The grass grows thick in the patch, sparse
   elsewhere, and not at all on the path.

The same from the command line:

```
foundry gen paint prairie -layer meadow -falloff 0.3 -disc 0,15,28
foundry gen paint prairie -layer path -kind exclude -line -128,0,128,0,3
foundry gen run prairie
```

## Paint layers

A generator declares its layers at the top of the `.fgen`:

```yaml
paint:
  - {name: meadow, kind: density}
  - {name: path, kind: exclude}
  - {name: trail, kind: material, resolution: 0.25}
```

| Field | Meaning |
| --- | --- |
| `name` | lower-case letters, digits and `_`; what a stage's mask parameter names |
| `kind` | what one texel holds (below) |
| `resolution` | metres per texel, 0.125–8, dividing 64 (default 0.5) |

| Kind | Holds | Read by |
| --- | --- | --- |
| `density` | 0..1 | a `paint` band on a scatter or grass rule, or a `band` item of a material stage |
| `exclude` | 0..1 | a stage's `exclude` list: keep every rule clear |
| `material` | 0..1 | an `override` item of `terrain_material` / `region_material`: force a ground layer |
| `water` | 0..1 | pond, lake and marsh area: a `ground_edit` stage's `water` list carves each painted area as a body ([Ponds, lakes and marshes](#ponds-lakes-and-marshes)) |
| `height` | metres, ± | a signed height offset: a `ground_edit` stage's `height` list adds it to the ground ([Height and flatten](#height-and-flatten)) |
| `direction` | (x, z), −1..1 | a flow vector: a scatter or grass rule's `orient_to` turns its placements along it ([Direction](#direction)). Paint it by dragging: the drag's direction is the stamp's |

The kind is only a convention for the bands. Any layer can feed any band; the
kind sets how a texel is stored and how the editor tints it.

**Paint is world-level.** A layer covers the world in fixed 64 m tiles counted
from the origin, so it does not depend on which terrain it lies on. Resizing
or re-tiling the terrain leaves the paint where it was. In a level generation
doc ([Environment regions](35-regions.md)) every region's stages read the
level doc's layers. A preset's own layers are never read there, and a preset
stage that names a layer the level doc does not declare reads zeros.

## The brush (Generator 2D view)

Tick **Paint** in the bar above the 2D view. While it is on, a second row
holds:

| Control | |
| --- | --- |
| layer | the layer to paint (with its kind) |
| Preset | a brush preset: one stroke paints several layers ([Brush presets](#brush-presets)) |
| **New layer…** | name, kind and resolution. Adding a layer is an ordinary document edit (Ctrl+Z takes it back once the tool is off) |
| Radius | metres; `[` and `]` shrink and grow it |
| Strength | how much one stamp changes, 0..1; Ctrl+wheel nudges it |
| Falloff | the outer fraction of the radius the brush eases out over: 0 a hard edge, 1 soft all the way in |
| mode | **add** paints up, **erase** takes away, **smooth** blurs; on a height layer, **flatten** levels the ground ([Height and flatten](#height-and-flatten)). Hold **Shift** as a stroke starts to erase whatever the mode |
| curve | the falloff's shape: **smooth**, **linear**, **sharp** (a pen pressed lightly: the stamp is mostly its centre) or **dome** (pressed hard: full nearly to the rim) |
| Spacing | how far apart a drag lays its stamps, a fraction of the radius (0.25 by default). Stamps are laid along the path, so a fast drag leaves no gaps |
| Undo / Redo | the brush's own history (also Ctrl+Z, and Ctrl+Y or Ctrl+Shift+Z, while the tool is on) |
| **Layer…** | rename or delete the active layer (below) |
| **Live** | update the open level after each stroke ([Live updates](#live-updates)); on by default |

A left drag paints. It lays a stamp every spacing × radius along the mouse's
path, straight into the layer, and the tinted overlay shows it as you go:

- density: green
- exclude: red
- material: amber
- water: blue
- height: blue below zero, red above
- direction: a hue by heading

Middle or right drag still pans and the wheel still zooms. Paths are not
edited while the tool is on.

Each stroke, from mouse down to mouse up, is one step of the brush's undo,
apart from the document's. A document undo never changes paint. Letting go of
the mouse marks the generator edited, so the save chip turns dirty and the
preview reruns. Saving, or **Save All**, writes the changed tiles. **Run**
reruns the stages that read a painted layer, and only the tiles the paint
changed ([Reruns](#reruns)).

**Renaming and deleting layers.** **Layer…** shows which stages read the
active layer, and renames or deletes it. A rename changes the declaration
and every stage parameter that names the layer, and the pixels go with it. A
delete drops the declaration and clears the parameters that named it, so the
document still validates. Both are document edits: Ctrl+Z with the tool off
takes them back, pixels included. A deleted layer's folder stays on disk
until you remove it.

## Painting in the 3D view

The same brush paints straight onto the terrain in the level editor. It is
there when:

- the Generator window is open with **Paint** ticked and a layer chosen;
- the level editor holds the generator's target level, in 3D;
- no other placement tool (the terrain tool, the scenery brush, a stamp) is
  armed.

Then:

- A ring lies on the ground under the cursor: the brush's radius, and the
  start of its falloff. It turns red while Shift (erase) is held.
- The active layer is drawn on the terrain in its tint, so you see what is
  painted where it will grow.
- A left drag paints, at the world point under the cursor, with the same
  radius, strength, falloff, curve, spacing and mode as the 2D view. The two
  views are one brush.
- `[` and `]` resize, Ctrl+wheel sets strength, Shift erases.
- Ctrl+Z and Ctrl+Shift+Z (or Ctrl+Y) undo and redo strokes ahead of the
  level's own undo, on the same history as the 2D view.

The brush aims at the level's solid geometry, which is the generated
terrain's tiles. A click always belongs to the brush while it is there, so a
stroke never selects the rock it started on.

## Brush presets

Ground cover is several layers at once. A forest floor is a litter splat,
fallen twigs, a few ferns and less grass. A **preset** paints them all in one
stroke. Pick one in the **Preset** combo beside the layer picker; it works in
the 2D view and the 3D view alike. **(no preset)** goes back to painting one
layer.

Each preset is a list of layers and a level for each:

| Preset | Paints | Grows (Poly Haven, CC0) |
| --- | --- | --- |
| Forest floor | forest_litter 1, forest_twigs 0.6, forest_ferns 0.3, grass_clear 0.5 | `forest_leaves_04` splat and cut-out leaf cards; `dry_branches_medium_01` laid on the ground; `fern_02`; the odd `dead_tree_trunk` or `tree_stump_01` (a scatter, so it collides) |
| Meadow | meadow 1 | `grass_medium_01` tall pieces, the odd `dandelion_01` |
| Wildflowers | wildflowers 1 | `dandelion_01`, `periwinkle_plant`, `shrub_sorrel_01` as clover, short grass |
| Dense grass | dense_grass 1 | `grass_medium_01`, 6 a square metre |
| Fern understorey | ferns 1, grass_clear 0.3 | `fern_02`, `shrub_04` |
| Moss | moss 1, grass_clear 0.4 | `moss_01` cushions laid on the ground |
| Gravel | gravel 1, grass_clear 0.8 | `gravel_floor` splat, `stone_01` chips |
| Pebbles | pebbles 1, grass_clear 0.6 | `pebbles` splat, `stone_01` and small `rock_moss_set_02` stones half buried |
| Mud | mud 1, path_clear 0.9 | `brown_mud_02` splat |
| Dirt trail | (a path spline) | worn `dirt_floor` earth on a flattened bed, a ragged edge, stepping stones across water ([Paths](#paths)) |
| Gravel path | (a path spline) | `gravel_floor` on a flattened bed, a tidier edge |
| Rock path | (a path spline) | `rock_09` flagstones bedded in a `rock_path` splat on a flattened bed; `rock_moss_set_02` ford stones |
| Stepping stones | (a path spline) | a line of `rock_moss_set_02` stones a stride apart, the grass worn between them, no flatten |
| Game trail | (a path spline) | a faint, narrow line of thinner, shorter grass and a little `dirt_floor` |
| Scree | scree 1, grass_clear 0.8 | `rock_ground_02` splat, stones of every size |
| Snow patch | snow_patch 1, path_clear 1 | `snow_02` splat where painted (no snow line) |
| Sand drift | sand_drift 1, grass_clear 0.6 | `sand_02` splat |
| Ash | ash 1, path_clear 1 | `burned_ground_01` splat |
| Pond | pond 1 (water) | a carved pond at its spill height; reeds and sedge (`grass_medium_01`) on the shore; `brown_mud_02` wet banks; no scattered rocks in it |
| Lake | lake 1 (water) | a wide, deep body with long shallow margins; the same shore |
| Marsh | marsh 1 (water) | shallow broken water over hummocks, tussock islands, reeds standing in the shallows, mud; `water_swamp` |
| River | (a river spline) | a carved river with flowing `water_river`; `gravel_floor` bed, `sand_02` bars, `brown_mud_02` banks, `rock_moss_set_02` boulders in the riffles, reeds and sedge ([Rivers and streams](#rivers-and-streams)) |
| Stream | (a river spline) | a narrow quick stream; gravel bed, small boulders, sedge |
| Keep clear | keep_clear 1 | nothing: no grass, cover or scattered rocks |

**The level is a ceiling.** Each stamp raises a preset's layer by the brush's
strength, but never past the preset's level for that layer, and never lowers
a layer painted higher before. So a drag over the same ground leaves twigs at
0.6, not 1. Erase takes every layer of the preset away.

**Two kinds of clearing.** `grass_clear` thins the generator's own grass
stages, but not the presets' ground cover (the `cover` stage). `path_clear`
clears every grass stage, cover included. The path brushes clear by the
ground_edit's path masks instead (`path_wear` in every grass stage,
`path_clear` in every scatter).

### The recipe

A preset's layers only grow something if stages read them. So each preset
carries a **recipe**: the layers, the Poly Haven textures and models, the
`grass_field` and `scatter` rules that read them, and the splat entries.
Picking a preset whose layers the generator lacks opens a popup listing what
the recipe adds. **Add** applies it as one document edit (Ctrl+Z with the tool
off takes it back). Run the generator once and the preset paints straight
away.

What applying a recipe does, in order:

1. Declares the layers the document lacks.
2. Adds the textures to the `polyhaven_textures` stage your
   `terrain_material` reads. A texture already there under another name (the
   same Poly Haven scan) is used as it is. With no material stage, the
   textures go to a new stage, `cover_tex`.
3. Merges the recipe's stages by ID: a missing stage is added, and a stage
   you already have gains the rules (by name) or assets (by id) it lacks. The
   ground cover lives in one `grass_field` stage, `cover`; its Poly Haven
   sources are `cover_forest`, `cover_grass`, `cover_flowers`,
   `cover_stones`, `cover_moss` and `cover_shrubs`. A stage your own stages
   now read is moved above them.
4. Adds the splat entries to the `terrain_material`'s paint list. Each takes a
   splat slot:
   - a role already wearing the same scan;
   - else the first free cover slot it lists (`wet`, `ash` or `snow`, when
     the document leaves it empty). A snow slot used this way gets a snow
     line far out of reach, so only the paint lays it;
   - else its fallback role. A `terrain_material` has seven slots (four
     roles, three covers), so a document can run out, and then, say, a dirt
     trail paints its cracked ground instead. The popup and the CLI say so.
   - A `region_material` gets its fallback role.
5. Adds `exclude` entries to the stages that should stay clear.

Applying a recipe twice changes nothing the second time.

### Preset files

Presets are YAML files in `resources/paint/<name>.fpb`. The combo rereads the
folder whenever it opens.

```yaml
name: Forest floor
doc: Leaf litter, fallen twigs, a few ferns; thins the grass.
order: 10                  # the picker's order
layers:                    # what one stroke paints, and to what level
  - {layer: forest_litter, strength: 1}
  - {layer: forest_twigs, strength: 0.6}
  - {layer: grass_clear, strength: 0.5}
recipe:
  paint:                   # the layers (as the .fgen's paint: list)
    - {name: forest_litter, kind: material}
    - {name: forest_twigs, kind: density}
    - {name: grass_clear, kind: exclude}
  textures:                # Poly Haven texture layers
    - {name: forest_litter, id: forest_leaves_04, scale: 2.5}
  material:                # splat entries
    - {layer: forest_litter, texture: forest_litter, slots: [ash, snow, wet], fallback: cracked}
  exclude:                 # exclude lists to join
    - {layer: grass_clear, kind: grass_field, except: [cover]}
  stages:                  # .fgen stage syntax, merged by ID
    - stage: grass_field
      id: cover
      params:
        terrain: $terrain  # the document's terrain stage
        rules:
          - {name: forest_twigs, plants: cover_forest, orient: ground, wind: false, ...}
```

In a recipe's stages, `$terrain`, `$textures` and `$scatter` stand for the
document's terrain stage, the textures stage of step 2 and its first
`scatter` stage. The ground cover rule fields (`orient`, `wind`, `sink`,
`tilt_jitter`, `card`) are in [Generators](33-generators.md#grass_field-placement).

From the command line:

```
foundry gen paint -presets
foundry gen paint p3_demo -preset forest_floor -falloff 0.35 -disc -60,0,28
foundry gen paint p3_demo -preset pebbles -no-recipe -line 40,0,70,10,4
```

`-preset` applies the recipe first when the document lacks the preset's
layers, and prints what it added; `-no-recipe` refuses instead.

## Ponds, lakes and marshes

Paint with the **Pond**, **Lake** or **Marsh** preset, in the 2D view or the
3D view. Each paints a `water` layer; the recipe adds a `ground_edit` stage
that carves it ([33-generators.md](33-generators.md), "ground_edit") and
points the generator's terrain readers — the material, the grass, the
scatters — at it, so they stand on the carved ground and see its water.

- **Each painted patch is one body**: the outline is where the brush paints
  at least half strength. The bed shelves from the outline (a bank, a
  shallow ledge, a deeper middle), a little uneven; a small pond is
  shallower than a big one. Ground outside the outline that stands too high
  is cut back into a sloping bank, so a pond painted on a hillside has no
  cliff on its uphill side.
- **The water stands at the spill height**, the lowest point of the rim, so
  it never floods past the outline. To hold it higher or lower, use the
  **Water bodies** panel (below).
- **Pond** and **Lake** wear the `water_lake` preset, **Marsh**
  `water_swamp`. A marsh's bed is hummocky: tussocks break the surface as
  islands and the water lies in broken channels between them.
- **Around the water**: reeds stand along the shore and into the shallows
  (grass_field's `shore` band and `max_depth`), sedge a little further out,
  and the ground is wet mud on the banks (the material's `wet` slot, and the
  water layer painting it). Grass never stands in the water: a grass rule's
  `max_depth` is 0 unless it says otherwise. Pond and Lake keep scattered
  rocks out of the water (the layer joins the scatters' `exclude`).
- In the 3D view the water appears when the live update lands, a moment
  after the stroke ([Live updates](#live-updates)): the ground_edit reruns,
  recooks only the terrain tiles the stroke moved, and the water meshes
  that changed.

**The Water bodies panel.** Select the `ground_edit` stage: above its
parameters the panel lists the bodies the last run carved, one row each:

| Column | |
| --- | --- |
| Body | the layer and style; the tooltip gives its anchor, depth and painted area |
| Level | the water's height. Automatic: the spill height (the tooltip shows the rim). Locked: drag it |
| Auto / Lock | **Lock** pins the water at its current level; **Auto** goes back to the spill height. A level above the rim raises a berm to hold the water |
| Preset | the body's water material, or its layer's |
| Area | the water's area |

A lock is a `bodies` entry keyed by a point inside the body (its most inland
point when you lock it), so repainting the pond's edges keeps its level. A
pond painted dry leaves its entry orphaned; the panel lists it with
**Remove**. Each change is a document edit and starts a live update.

## Rivers and streams

Pick the **River** or **Stream** preset and drag along where the water
should run, in the 2D view or the 3D view. A river brush paints no layer:
the stroke becomes a **river spline** in the `ground_edit` stage
([33-generators.md](33-generators.md), "ground_edit", Rivers), simplified to
a few points, every point as wide as the brush's diameter (Stream: 0.7 of
it). The first stroke on a generator without a ground_edit applies the
preset's recipe (the ground_edit, the dressing below); a document that has
one gets the dressing it lacks.

- **Direction doesn't matter.** Drag up or down the valley: the river is
  turned to run downhill.
- **Width per point.** The new river's path is the active spline in the 2D
  view: drag its points, click a segment to add one, right-click to delete,
  and **Shift+wheel** over a point to widen or narrow it there. Its banks are
  drawn either side. The **Rivers** panel (below) has − / + for the whole
  river.
- **No invented wiggles.** A painted river bends where you drew it; its
  `meander` is 0. Rivers traced from drainage get a mild wander.
- **What the channel does.** The water surface never runs uphill; deep slow
  **pools** alternate with shallow fast **riffles** every few widths; a bend
  scours its outer bank and leaves a **sand bar** inside; a steep drop
  becomes a **waterfall** with a plunge pool (a long steep run is foamy
  **rapids** instead, or with `cascade_step` set — the Stream preset sets
  1.2 m — a **cascade** of small falls); a stream that ends on a river
  joins it at its level (a **confluence**, white water trailing down the
  river from the mouth); a river that runs into a pond or lake ends at its
  level and leaves it from there.
- **Waterfalls look like waterfalls.** Every fall gets a falling sheet over
  the lip — streaks running down, glassy at the top of a small fall and
  white at the foot, frayed at the sides — dropping through a notch cut in
  the ground, with mist rising from its foot (a `waterfall_mist` emitter,
  placed with the generator's other placements) and foam rings in its pool.
- **Flowing water.** The ribbon wears `water_river`, whose flow map moves the
  ripples and the foam downstream — faster through narrows and riffles,
  slow in pools, foam streaming off riffles, boulders, confluences and the
  lip of a fall.
- **The dressing** (the recipes): a gravel bed under the water (a material
  entry reading the `river_bed` mask), sand on the point bars (`river_bar`;
  the River preset), wet mud on the banks (the `wet` slot), reeds along the
  water and sedge behind (grass_field's `shore` band — grass never stands in
  the channel, its `max_depth` being 0), and **boulders** in the riffles
  (a polyhaven_rocks stage named by `river_rocks`), each throwing foam;
  scatter and grass keep out of the boulders (and a path's stones).
- **You can hear it** (phase W5). The River, Stream, Pond, Lake and Marsh
  presets turn on the ground_edit's **Water sounds**: the flow along every
  river (rapids at riffles), a roar at each waterfall, the rush at a
  confluence and lapping along pond and lake shores, as looping positional
  sounds placed with the generator's other placements, spaced so they never
  stack ([Water](37-water.md)).
- **Wading and splashes.** Walk into the shallows and the water rings round
  your legs and splashes at every stride; jump in and it splashes; duck under
  and the world turns to murk, with the sky overhead through Snell's window
  ([Water](37-water.md)). Nothing to set up.
- **Rivers from drainage.** Turn on the ground_edit's **Rivers from
  drainage** to trace streams down the ground's own drainage above
  `drainage_threshold`; their width grows with the area they drain. They run
  into the hand rivers, never beside them.

| Preset | Width | Depth | Character |
| --- | --- | --- | --- |
| River | 2 × radius | 1.3 m | pools 0.65, boulders 0.8, a 4 m floodplain, sand bars |
| Stream | 1.4 × radius | 0.45 m | quick (1.2 m/s), riffly (pools 0.8), boulders 1.5, little floodplain, cascades in 1.2 m steps |

**The Rivers panel.** Select the `ground_edit` stage: below the water
bodies, one row per river — the hand rivers from the document (a fresh
stroke shows before the run) and the drainage rivers the last run traced:

| Column | |
| --- | --- |
| River | name and kind (painted, adopted, drainage) |
| Run | length, drop and what it flows into, from the last run |
| Material | the water material (hand rivers) |
| Edit, −, +, Delete | Edit makes its path the 2D view's active spline; − / + scale every point's width; Delete removes it |
| Adopt | (a drainage river) copy it into a hand river you can edit; the trace then gives way to it |

Under the table: how many waterfalls, confluences and boulders the run made.
Every change is a document edit (one undo step; with the brush on, Ctrl+Z
takes a river stroke back like any stroke) and starts a live update.

From the command line, a river brush's stroke is its `-line` (and `-disc`)
points in order, the last radius the brush's:

```
foundry gen paint valley -preset river -line -120,30,-40,10,3.5 -line -40,10,120,-5,3.5
```

## Paths

Pick a path preset — **Rock path**, **Stepping stones**, **Dirt trail**,
**Gravel path** or **Game trail** — and drag where the path should go, in
the 2D view or the 3D view. Like a river brush, a path brush paints no
layer: the stroke becomes a **path spline** in the `ground_edit` stage
([33-generators.md](33-generators.md), "ground_edit", Paths), simplified to
a few points, every point as wide as the brush's diameter (Game trail: half
of it), in the preset's style. The first stroke on a generator without a
ground_edit applies the preset's recipe; a later path preset adds the
splat entry and stones it brings.

- **Width per point.** The new path is the 2D view's active spline: drag its
  points, click a segment to add one, right-click to delete, **Shift+wheel**
  over a point to widen or narrow it. Its edges are drawn either side.
- **The bed** (rock, dirt, gravel; `flatten` on). The ground along the path
  is smoothed along its length (bumps shorter than `smooth`, 6 m, levelled
  out) and the cross-section is levelled to the centreline: on a side slope
  the uphill side is cut and the downhill filled, eased out over `blend`
  (1.2 m) past the edge. With `steps` on, a stretch steeper than
  `max_grade` gets treads `step_rise` apart. Stepping stones and game trails
  leave the ground alone.
- **A worn edge.** The style's splat (`rock_path`, `gravel_floor`,
  `dirt_floor`) is painted with an edge that wanders in and out (`edge`, a
  fraction of the half width, on a noise of `edge_size` metres), not a
  ruled line.
- **Wear.** Grass thins toward the centreline (none at all on a rock, dirt
  or gravel path's middle; some between stepping stones; a little on a game
  trail) and is thinner and shorter in a soft band `wear` metres wide either
  side, where the ground takes a dirt tint. Large scatter — rocks, logs — is
  kept off the path and a little beyond.
- **Stones.** A rock path is filled with **flagstones**: rows across it, two
  or three a row by the width, each a flat scanned stone scaled to its
  place, turned along the path with a little jitter, tilted to the ground
  and bedded half its thickness, the joints irregular. A stepping-stone path
  lays a line of **stepping stones** a stride apart (0.6–0.8 m), slightly
  zig-zagged. No two stones overlap, and none float.
- **Water.** Where a path crosses a river (or a pond) it **fords** it: no
  flatten in the water or the river's channel, no splat under the water, and
  stepping stones across, standing on the bed with their tops above the
  surface (bigger the deeper they stand; none in water deeper than 0.75 m —
  wade there). A game trail just wades. There are no bridges yet.

| Preset | Width | Flatten | Stones | Splat |
| --- | --- | --- | --- | --- |
| Rock path | 2 × radius | yes | flagstones (`rock_09`), a ford (`rock_moss_set_02`) | `rock_path`, worn edge |
| Stepping stones | 2 × radius | no | stepping stones and a ford (`rock_moss_set_02`) | a little `dirt_floor` |
| Dirt trail | 2 × radius | yes | a ford | `dirt_floor`, ragged edge |
| Gravel path | 2 × radius | yes | a ford | `gravel_floor`, tidy edge |
| Game trail | 1 × radius | no | none | a faint `dirt_floor` line |

**The Paths panel.** Select the `ground_edit` stage: below the rivers, one
row per path — its name, a **Style** combo, **Flatten**, the last run's
length, fords and stones, and Edit (its spline becomes the active one), − / +
(every point's width) and Delete. Every change is one document undo step and
starts a live update. If the stage has no **Path rocks**, the run lays out
the stones but places none, and the panel says so.

From the command line, a path brush's stroke is its `-line` points in order,
the last radius the brush's:

```
foundry gen paint valley -preset rock_path -line -24,4,-33,13,1.1 -line -33,13,-58,38,1.1
```

## Direction

A **direction** layer holds a vector per texel. Paint it with the brush on
the layer: every stamp paints the direction the brush is moving, so a drag
down a slope points it downhill and a click without moving paints nothing.

A `scatter` or `grass_field` rule turns its placements along a direction
field with `orient_to`:

| `orient_to` | The field |
| --- | --- |
| a direction layer | the layer, with the ground_edit's **paths** laid over it: on a path its tangent wins (paths write the direction a rule reads, without anyone's paint being written) |
| `paths` | the paths' tangents alone, strength 1 on the path easing out over its wear band |
| `downhill` | down the terrain's slope, full strength from a 6% grade |

`orient_jitter` (12°) turns each placement up to that far off the field;
`orient_offset` turns them all (90: across it). The field's strength blends
back toward a random yaw where it is weak. A scatter rock's long side
follows the field; a grass piece whose length is along its X gets a quarter
turn. Both ends of a direction are the same (half the placements are turned
end for end). Nothing else moves: positions, kinds, scales and tints are as
without `orient_to`.

```yaml
- name: forest_logs
  rocks: cover_logs_src
  orient_to: downhill      # fallen logs lie down the slope
  orient_jitter: 20
```

## Height and flatten

A `height` layer is metres added to the ground (negative lowers it), through
a `ground_edit` stage's `height` list. New layer… → kind `height`, then add
it to the stage's list. Its brush:

- **add** raises by the strength in metres at the brush's centre (Shift:
  erase back toward the generated ground);
- **smooth** blurs the offset;
- **flatten** levels: as the stroke starts it samples the ground under the
  brush (the generated terrain from the last run, plus the height layers),
  and every stamp moves the layer toward the offset that brings the ground to
  that height. Drag it across a slope for a terrace or a path's bed. Run the
  generator once first: with no output there is no ground to level against,
  and flatten paints the layer itself toward the value.

From the command line, `-mode flatten -value 12` levels to 12 m.

## Live updates

When the generator's level is open in the level editor and the generator has
already run into it once, every stroke (and every undo or redo of one)
updates the level:

1. 300 ms after you let go (strokes in quick succession make one update),
   the generator reruns in the background, without the run window. Only the
   stages reading the painted layer rerun, and only the tiles the stroke
   reached ([Reruns](#reruns)).
2. The open level is changed in place: the generator's placements and
   volumes that changed are swapped, the rest left alone. Grass fields,
   material maps and materials the run rewrote are reloaded. There is no
   loading bar and no level-editor undo step: undo the stroke instead, and
   the level follows.
3. The status line says how long it took, from mouse-up to the level
   updated.

The level is then unsaved, as after any edit. Save it to keep the new
placements; the paint is saved with the generator.

Measured on a 768 m world in 128 m tiles (the development box: WSL, 20
threads; the work is on the CPU), from mouse-up to the level updated, the
300 ms wait included:

| Stroke | Reran | Time |
| --- | --- | --- |
| a meadow disc (grass_field) | 1 grass field of 36 | 0.44 s |
| a trail (terrain_material, and the scatter and grass reading it) | the splat's paint; 42 of 144 scatter tiles; 10 grass fields | 0.60 s |
| a rubble disc (scatter, and the grass avoiding it) | 11 of 144 scatter tiles; 10 grass fields | 0.52 s |

The next frame draws the result. Grass fields are read back within that
frame, and new or changed scenery joins the Nanite pool, which rebuilds when
the set of placements changes.

Live updates wait while the level compiles, loads or is played, and while a
full run is in progress. Untick **Live** to paint without them.

## The bands

**Scatter and grass rules** each have a paint band:

| Field | Default | Meaning |
| --- | --- | --- |
| `paint` | (none) | the layer |
| `paint_band` | (0, 1) | how the painted value v becomes weight: 0 at or below the first number, 1 at or above the second, linear between. (0, 1) is v itself; (0, 0.5) makes a half-strength stroke full density |
| `paint_invert` | false | grow everywhere *except* where painted |

The band multiplies the rule's other bands, so a meadow painted over a gully
still follows the gully's slope band. A scatter candidate where the band is 0
is dropped even inside a weighted cell, so painted edges are sharp.

**Exclude** is a stage-level list on `scatter` and `grass_field`:

```yaml
exclude:
  - layer: path
  - mask: path_wear     # a terrain mask instead (a ground_edit's paths)
    strength: 1
    shrink: 0.6         # grass_field: clumps shorter too
```

Each entry multiplies every rule of the stage by 1 − strength × v (strength
1 by default). An entry may read a **terrain mask** instead of a layer — a
ground_edit's `path_clear` or `path_wear`, a river's `river_bed` — and a
terrain without that mask reads 0. On `grass_field`, `shrink` also makes
clumps shorter by shrink × v: a path's wear band.

**Material stages.** `terrain_material` and `region_material` take a `paint`
list:

```yaml
paint:
  - layer: trail
    target: gravel      # a role, or (region_material) a Poly Haven id / albedo name
    mode: override      # or band
    band: [0, 1]
    invert: false
    strength: 1
```

- **override** moves the splat toward the target by strength × band(v): a
  trail painted at full strength is all gravel. In `region_material`, a role
  target goes to every environment's layer of that role by their weights
  there, so a trail of `gravel` is the desert's gravel on desert ground and
  the savannah's on the savannah.
- **band** keeps the target to the painted ground. Outside it, its weight
  goes to the other layers.

Paint is applied after the stage's rules and covers, so the ground colour map
and the coverage numbers agree with what was painted. The far backdrop is not
painted.

Everything defaults off. A rule or a stage that names no layer runs, and
hashes, exactly as it did before paint existed.

## Reruns

A stage that names a layer folds that layer's content hash into its own
stage hash. So:

- painting `meadow` reruns the stages that read `meadow`, and nothing else;
- the terrain stays fresh;
- painting a layer back to nothing brings back the hash it had before.

**Only the touched tiles.** The stages that read paint rerun per tile. Each
piece of their output is keyed by what it reads: the stage's settings
without the paint, and the painted texels its own ground can reach. A piece
whose key did not change is kept as it was. The result is exactly what a run
from nothing would make; the tests check it byte for byte.

| Stage | Piece | What a stroke reruns |
| --- | --- | --- |
| `grass_field` | a terrain tile's field (`grass_<i>_<j>.fgrass`) | the fields whose tiles the stroke reaches |
| `scatter` | a 64 m paint tile's rocks | the tiles the stroke reaches, and their neighbours while the rocks there keep changing |
| `terrain_material`, `region_material` | the splat's weights before paint | the paint step and the maps after it. The rules, the slow part, are kept |

Some things to know:

- **A scatter that reads paint is tiled.** It keeps its rocks apart tile by
  tile in 64 m world tiles, in four passes, instead of over the whole ground
  at once, so a stroke only moves the rocks near it. This places rocks
  differently from the classic scatter. Tiling applies only to a scatter that
  reads a layer (its own band or exclude, or a stage it builds on, such as a
  painted `ground`). Every other scatter places as before. Its placement IDs
  are per tile, `<rule>_<i>_<j>_<nnnn>` (a negative coordinate spelt
  `m<n>`), so a re-scattered tile never renumbers another tile's rocks. A
  rule whose biggest footprint reaches half a tile (a 16 m boulder at
  spacing 1) cannot be tiled. That scatter reruns whole, and the log says
  so.
- **Paint read through another stage.** A grass field avoiding a painted
  scatter reruns the tiles within 192 m of the stroke. That is the reach of
  the scatter's own tile chain.
- **Paint read through a ground_edit.** A stage standing on a `ground_edit`
  reruns only the pieces whose ground or water moved: the ground_edit gives
  each piece a key of the heights and water over it, and that key stands in
  for its water and height layers (a pond's level depends on its whole
  outline, so a stroke can move water far from the brush; the key follows
  it). A layer the stage reads itself as well (the material painting mud by
  the pond layer) also counts as read through the ground_edit: its
  paint-free weights are remade when the ground moved.
- **After an upstream stage runs**, the next rerun rebuilds every piece once.
  A terrain read back from its files differs slightly from the fresh one
  (its heights are quantised), so a piece made against one is not reused
  against the other. The log says which stage ran.

The log says what was reused:

```
[sward] reused 34 of 36 tiles (nothing they read changed)
[rubble] tiled: 144 tiles, 133 reused (nothing they read changed)
[ground_mat] unpainted weights reused (only the paint changed)
```

## On disk

The pixels live beside the generator, sparse:

```
generators/meadow.fgen
generators/meadow.paint/meadow/0_0.png      # tile (0, 0): x 0..64, z 0..64
generators/meadow.paint/meadow/-1_0.png
generators/meadow.paint/path/...
```

- Tile `(i, j)` covers x from 64 i to 64 i + 64 and z from 64 j to 64 j + 64.
- A tile is 64 m / resolution texels a side (128 at 0.5 m), row 0 at its
  lowest z.

| Kind | PNG |
| --- | --- |
| density, exclude, material, water | 8-bit grey, 0–255 = 0–1 |
| height | 16-bit grey, 32768 = 0 m, 1/256 m a step (±128 m) |
| direction | 8-bit RGB, R = x and G = z, 128 = 0, ±127 = ±1 |

Untouched tiles store nothing. A tile painted back to zero is deleted, and so
is an emptied folder. A layer taken out of the `paint:` list keeps its folder:
declare it again and its pixels are back. Values are kept at the file's
precision while you paint, so what you save is exactly what loads.

## From the command line

```
foundry gen paint <name|file.fgen> -layer L|-preset P [-no-recipe] [-kind K] [-res R]
    [-mode add|erase|smooth|set|raise|flatten] [-strength S] [-falloff F] [-curve C]
    [-value V] [-dir x,z]
    [-disc x,z,r]... [-line x0,z0,x1,z1,r]... [-clear]
foundry gen paint <name> -list
foundry gen paint -presets
```

- Each `-disc` stamps once.
- Each `-line` stamps along the segment as the brush's drag does.
- They run in the order given.
- A layer the document does not declare is declared with `-kind` (density by
  default).
- `-mode flatten` on a height layer levels the ground to `-value` metres (the
  ground_edit's source terrain from the last run is the ground).
- On a direction layer, `-dir x,z` is the vector each stamp paints.
- A river or path preset's `-disc` / `-line` points are the stroke (in
  order, the last radius the brush's): a spline, not paint.
- The document and its changed tiles are written back, as the editor's Save
  writes them.

It is how the tests and headless checks paint.
