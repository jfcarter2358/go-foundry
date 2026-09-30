# Terrain

A chunk can carry **terrain**: ground built from a grid of blocks that you
mine, build on and paint in the 3D view. Show it as **blocks**, or switch it
to a **smooth** surface built from the same blocks. Paint **biomes** on its
faces and they grow their own plants: grass in drifts, trees where there is
room and light, ferns in the shade, stones at the foot of cliffs, and moss and
vines on the rock.

Terrain is ordinary level geometry:
- it draws with the level's lights, shadows, cloud shadows and fog;
- bodies stand on it, are stopped by its walls and bump their heads on its
  overhangs;
- rays, bullets and line-of-sight tests hit it;
- AI agents navigate over it, into its caves, and over the ground above them;
- scatter volumes grow on it, and decals land on it.

It belongs to its chunk, so it moves, streams and switches on and off with it.

## Adding terrain to a chunk

Select a chunk in the Explorer and open its **Terrain** group:

| Field | |
| --- | --- |
| **Size (blocks)** | the grid's width, height and depth in blocks (at most 1024 each way) |
| **Block size** | the edge of one block, in units |
| **Ground height** | how many blocks deep the ground starts |
| **Hills** | how many blocks the ground rises and falls; 0 is flat |
| **Hill size** | the typical width of a hill, in blocks |
| **Seed** | picks the hills |
| **Grow** | a biome painted over the whole top of the new ground, or bare ground |
| **Add Terrain** | makes the grid, centred on the chunk with the ground's top at its origin, and arms the Terrain tool |

New ground is layered the way ground is: the top block is material 1, the few
under it material 2, and everything deeper material 3. A hillside steep enough
to show its side shows the layers.

Once a chunk has terrain, the same group sets it up:

| Field | |
| --- | --- |
| **Blocks** / **Smooth** | how the terrain is shown (and collided with); see below |
| **Smoothing** | how widely the smooth view blends the blocks; see below |
| **Edit [T]** | arms the Terrain tool |
| **Corner** | where the grid's first corner (lowest X, Y and Z) sits in the chunk |
| **Block size** | stretches the whole grid |
| **Texture scale** | how many units one repeat of a material's texture covers (default 2) |
| **Material 1–4** | the texture each block material shows |
| **Biomes** | the palette you paint with; see [Biomes](#biomes) |
| **Regenerate** | replaces every block and all the painting with new ground from the form (Ctrl+Z brings them back) |
| **Resize** | grows or crops the grid from its far sides; the first corner stays put |
| **Remove Terrain** | |

A terrain with no material textures is drawn in its normal colour, like an
untextured brush face. New terrain starts with `grass`, `dirt`, `rock` and
`sand` where the project has those textures.

## Blocks and smooth

The same blocks can be shown two ways. Switch with the **Blocks** / **Smooth**
buttons, or **N** while the tool is armed. The switch is one undo step and is
saved with the level.

- **Blocks** shows every block's exposed sides as flat squares: the raw grid,
  steps and all.
- **Smooth** shows a single smooth surface built over the blocks (a
  *surface net*, placed by *dual contouring*). A one-block step becomes a slope,
  a stack of blocks becomes a cliff, and a lone block becomes a boulder. The
  surface blends materials where they meet.

A gentle hill built from blocks is a staircase: flat for a few blocks, then a
one-block step. Smooth view doesn't show those shelves. Each patch of ground
takes its height from the ground around it, so the staircase becomes one even
slope. Only ground at a similar height counts, so a cliff's top and foot never
blend and cliffs stay cliffs. Caves and the ground above them are smoothed
separately.

**Smoothing** (shown while Smooth is on) sets how wide that blending is, in
blocks (default 3). Raise it if very gentle hills still show steps, and lower it
to keep more of the blocks' shape. At any setting, a mined tunnel or a
one-block ledge stays open and stays put: no point of the surface moves more
than half a block from the blocks it covers.

Everything else follows whichever view is on: bodies walk the slopes of a
smooth terrain and step up the blocks of a blocky one, and bullets and
navigation do the same. You can keep mining and building in the smooth view.
The cursor still picks a block, and the surface reshapes around it.

## The Terrain tool

Press **T** or click **Terrain** in the toolbar. While it's armed, the cursor
outlines what the next click acts on: the block to mine or re-texture, the empty
cell to fill, or the patch of faces to paint.

| Mode | Key | |
| --- | --- | --- |
| **Mine** | `1` | remove the block under the cursor |
| **Place** | `2` | add a block against the face under the cursor |
| **Material** | `3` | change the material of the block under the cursor |
| **Paint Biome** | `4` | paint the selected biome on the faces under the cursor |
| **Erase Biome** | `5` | clear the paint |
| **Volume** | `6` | lay out a box of blocks, then fill or carve it in one go |

| Key | |
| --- | --- |
| Click | use the mode once |
| Hold | keep going: Mine, Place and Material repeat a block at a time, painting brushes every frame |
| `Shift` + click | the opposite mode for that stroke: place instead of mine, erase instead of paint |
| `[` / `]` | reach down / up |
| `,` / `.` | previous / next material (biome while painting) |
| `N` | blocks / smooth |
| `T` / `Esc` | put the tool away |

**Reach** is how many blocks around the target the tool touches. At 0 it is the
one block, or the one face. Higher reaches mine or fill a ball of blocks, or
paint every face within that distance.

The **Terrain Tool** block in the panel holds the same controls, plus the
material and biome pickers. Picking a material switches to Place and picking
a biome switches to Paint.

Every stroke is one undo step, from button-down to button-up. A stroke stays on
the terrain it started on.

Mining a block takes the paint on it with it, and placing a block against a
painted face covers that face and its paint. The new block's faces start bare.

### Volumes

Volume mode builds big shapes in one step, which you can then mine into.
Click to put down a one-block box in the empty cell in front of the face under
the cursor. On a terrain with nothing solid yet, the box goes on the grid's
floor. The box is only a preview until you fill or carve it.

Each face of the box is a handle. The face under the cursor lights up
yellow: either the face itself, or the small square at its centre, which
you can grab from any side. Drag it to slide that face in or out a whole
block at a time. The block grid is drawn on the box so you can count its size, and
the panel shows it too. Click anywhere off the box to start a new one.

| Key | |
| --- | --- |
| `Enter` | fill the box with the current material |
| `Shift`+`Enter`, `Backspace`, `Delete` | carve the box out |
| `Esc` | drop the box (press again to put the tool away) |

A fill replaces every block in the box, solid or not. The box can reach past
the terrain's grid on any side. Filling it grows the grid to fit, and the
ground already built stays where it is. Carving never grows the grid. Either
way it's one undo step.

## Biomes

A biome is **what grows** on the faces you paint it on. You don't place plants.
Each biome is a community of species, and each species knows the conditions it
grows in. Paint a whole hillside and every face grows what suits it:

| Biome | Ground | Walls and overhangs |
| --- | --- | --- |
| **Meadow** | grass, flowers in single-colour drifts, tall grass and shrubs gathered at the foot of slopes, stones | moss, vines hanging from a cliff's lip and from overhangs |
| **Broadleaf Forest** | trees with room to grow, shrubs, ferns and mushrooms in the shade, leaf litter, fallen branches, mossy stones; grass only in the clearings | moss, ferns, vines |
| **Pine Forest** | conifers, fallen needles, ferns and mushrooms in the shade, stones, sparse dry grass | moss |
| **Wetland** | reeds and cattails in the low ground, lush grass, irises | moss, vines |
| **Dry Scrub** | tufts of dry grass with bare ground between, shrubs, stones, pebbles, deadwood | |
| **Mossy Cliff** | moss, ferns, grass on the ledges, stones | dense moss, ferns in the cracks, vines hanging from the lip and the roof |
| **Rocky Scree** | boulders and pebbles gathered below cliffs, sparse grass | lichen-coloured moss |
| **Dense Jungle** | tall broadleaf trees close together, palms, big-leaved elephant ears and bananas, ferns, shrubs, bright flowers, leaf litter, mushrooms and fallen branches everywhere, not only in the shade | moss, ferns, thick curtains of vines from the lip and the roof |
| **Desert** | open sand, saguaros standing well apart, barrel cacti and prickly pears in flower, dead grey bushes, a little dry grass, sandstone rocks and pebbles, bleached deadwood | |
| **Beach** | leaning palms with coconuts, dune grass, pale pebbles and shells toward the wet ground, bleached driftwood, rocks below cliffs | |
| **Tundra** | short sedge, grey lichen, knee-high shrubs in autumn red, cotton grass in the hollows, small white and yellow flowers, the odd stunted spruce, frost-heaved stones | lichen |
| **Badlands** | red boulders piled below every bank, red gravel, dead brush, dry grass in the gullies, the odd prickly pear | |
| **Volcanic Plains** | a thick grey-green blanket of woolly moss over black lava rubble, lupines in purple drifts, thin grass | woolly moss |
| **Savannah** | a sea of golden grass, acacias standing alone and far apart, shrubs in the gullies, dead thornbush, rocks | |
| **Mesa** | red boulders fallen at the foot of the walls, gravel, juniper, dead brush, dry grass, the odd prickly pear | banded red and cream strata |
| **Glacier** | leaning seracs and broken ice blocks, dark moraine gravel and boulders along its edges; nothing grows | blue ice |
| **Ice Plains** | wind-rippled snow with patches of blue ice, frozen tufts, ice blocks, frosted stones | lichen |
| **Rocky Cliff** | talus piled at the foot, gravel, lichen, a tuft of grass or a stunted pine where soil holds | bare weathered rock, lichen |

What decides where each species takes, read from the blocks:

- **The surface:** ground, wall or ceiling, from the shape of the surface. On
  ground it also depends on the slope, so trees keep off steep banks.
- **Light:** how much sky the spot can see. Ferns and mushrooms grow under
  trees and overhangs, and grass and flowers grow in the open. A cave painted
  Meadow grows moss and vines, not grass.
- **Edges:** stones, shrubs and ferns gather at the foot of a wall. Trees keep
  back from the lip of a drop. Vines hang only from the top of a cliff.
- **Moisture:** low, sheltered ground is wetter. Reeds want it and dry grass
  avoids it.
- **Patchiness:** each species grows in drifts of its own size, so flowers come
  in patches and dry grass leaves bare ground between its tufts.
- **Borders:** where one biome's paint meets another's, or bare ground, the edge
  frays instead of stopping on a block line.

Plants in better spots grow bigger, and colours drift across the landscape, so
a field is lusher in one place and drier in another.

Painted ground shows the biome's own **ground texture**: sand under a desert,
leaf mould under a forest, black cinders under a lava field. It lies on ground
that faces up, to fairly steep banks; cliffs keep their block's texture unless
the biome says otherwise (Mossy Cliff does). At the edge of the paint it frays
into the block's texture rather than stopping on a line. It works on a terrain
with no textures of its own, too.

Painted ground also takes the biome's colour: a meadow's green, a forest floor's
brown. It shows a little up close and fully in the distance, where the grass has
thinned out, so a meadow still reads as a meadow at the far end of the view.

Each biome in the **Biomes** list has:

| Field | |
| --- | --- |
| **Kind** | which biome it is |
| **Density** | how thickly everything in it grows (1 is natural) |
| **Tint** | multiplies every colour in it: an autumn forest, a sun-scorched meadow |
| **Amounts** | one slider per kind of thing the biome grows (grass, flowers, rocks, trees...): 0 grows none, 1 as the biome has it, up to 4 times as many. It applies to this terrain only; the biome file is unchanged. Ctrl+click a slider to type a value; **Reset Amounts** puts them all back to 1 |
| **Ground** | what the painted faces show: the biome's own ground texture by default, or one of the terrain's materials instead |
| **Remove** | removes the biome and all its paint |

In the level file the amounts are a map by shape:
`biomes: [{preset: forest, amounts: {broadleaf: 0.3, fern: 2}}]`.

**Foliage seed** reshuffles where every plant falls without changing where
anything grows.

Painting is fast. Plants reappear as you paint, nearest first.

## Biome files

Every biome is a YAML file in `resources/biomes/`, named after the biome:
`jungle.fbio`, `desert.fbio`. Edit one and save it, and the editor picks up the
change within a second: every terrain painted with it regrows and takes the new
ground. A new file adds a biome to the **Kind** list. If a file has a mistake,
the biome keeps its last good definition and the terrain panel says which file
does not parse (hover it for why).

```yaml
label: Desert
doc: Open sand with saguaros standing apart.
ground:
  texture: ground_desert   # a texture in textures/, laid over the painted ground
  scale: 4                 # units per repeat (default 4)
  walls: false             # true lets it climb cliffs too
  tint: [0.78, 0.64, 0.44] # the colour it reads as from far away
  cover: 0.2               # how strongly that colour is laid on
species:
  - shape: cactus
    variants: [0]          # 0 saguaro, 1 barrel, 2 prickly pear
    spacing: 11            # distance between neighbours, in units
    where: [ground]        # ground, wall, ceiling
    slope: [0, 30]         # degrees from flat it grows on
    scale: [0.7, 1.3]
    colors: [[0.42, 0.54, 0.34], [0.54, 0.64, 0.42]]
    light: [0.6, 1]        # sky it wants: 0 a cave, 1 open sky
    clearance: 4           # blocks of air it needs above
```

Each species can also set `accents` (flower, fruit and cap colours), `upright`,
`sink`, `cluster` and `cluster_size`, `edge`, `moisture`, `color_noise` and
`top_edge`. `foundry biomes schema` prints every key with its range, and every
plant shape with its variants.

The engine has these biomes built in too. A file replaces the built-in one of
the same name, and a project with no `biomes/` folder paints with the
built-ins. To start from them:

| Command | |
| --- | --- |
| `foundry biomes export` | writes the built-ins out as files (`-force` overwrites) |
| `foundry biomes check` | parses every file and checks its ground texture exists |
| `foundry biomes list` | every biome, its ground texture and where it comes from |

The ground textures are `textures/ground_<biome>.png`, made by
`go run ./cmd/genbiomeground` (or `... genbiomeground desert` for one). Any
seamless PNG works as a biome's ground.

In the editor, the Claude Code panel has `biome_schema`, `biome_list`,
`biome_get` and `biome_save` tools, so you can ask it to change a biome or make
a new one. `biome_save` checks the file before writing it.

## Clicking terrain

With the tool put away, clicking terrain selects its chunk. Things placed at the
cursor (entities, props, decals, the scenery brush) land on the ground.

## Compiled levels

The compiler leaves terrain out of CSG and BSP, so blocks are never shredded
into fragments. It keeps the terrain on its chunk in the `.fbsp`, and a release
build draws, collides with and grows it exactly as the editor does. The level's
seal box is fitted around baked chunks' terrain too.

Terrain is **lit live**, not baked. Static lights reach it at full quality, with
real-time shadows. What it doesn't have:
- bounce light from the bake;
- `bake_exclude` rules.

It also doesn't cast into baked lighting: a hill doesn't shadow a brush floor
in the bake. Where that matters, make the light dynamic.

## Performance

- **Editing:** the surface is built in 16×16×16-block sections. Mining a block
  rebuilds and re-uploads only the sections around it.
- **Drawing:** each section is drawn and culled on its own.
- **Collision:** a few short rays from inside each body. Bodies nowhere near a
  solid block skip terrain entirely.
- **Rays:** walk the grid in two-block steps, testing only the triangles in
  each step.
- **Navigation:** each column of solid blocks is its own piece, so caves stay
  open and editing re-bakes only the tiles nearby.

Foliage is built once per section, when it first comes into range and again
only when something it depends on changes. It is never built per frame. It is
then drawn from static buffers:
- each kind of plant thins out evenly toward the edge of its draw distance and
  fades rather than pops;
- past a short distance each kind switches to a far mesh with a fraction of the
  triangles;
- trees, shrubs, rocks, ferns and fallen wood cast shadows; grass, flowers and
  moss don't.

Two player settings control the cost. A settings screen can offer them, and
both are saved to `graphics.yaml`:

| Setting | |
| --- | --- |
| `@level.prop.foliage_distance` | how far foliage is drawn, 0.25 to 2 (default 1). At 0.5 it draws about half as far and costs roughly half as much |
| `@level.prop.foliage_shadows` | whether foliage casts shadows (default on) |

The level file stores the blocks and the paint packed and compressed. A
96×40×96 terrain with hills, cliffs, a cave and six painted biomes is about
20 KB of YAML.
