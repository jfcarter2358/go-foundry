# Terrain

A chunk can carry **terrain**: open ground stored as a grid of heights,
sculpted and painted in the 3D view, not built from brushes. Use it for
hills, valleys, riverbeds and basins, the ground *between* the things you build.
Keep building walls, cliffs, caves and bunkers from brushes; they sit on the
terrain or come up through holes cut in it.

Terrain is ordinary level geometry:
- it draws with the level's lights, shadows, cloud shadows and fog;
- bodies stand on it and are stopped by its steep slopes;
- rays, bullets and line-of-sight tests hit it;
- AI agents navigate over it;
- scatter volumes grow on it;
- decals land on it.

It belongs to its chunk, so it moves, streams and switches on and off with it.

## Adding terrain to a chunk

Select a chunk in the Explorer and open its **Terrain** group:

| Field | |
| --- | --- |
| **Cells X / Z** | the grid's size, in cells (at most 1024 each way) |
| **Cell size** | the edge of one cell, in units |
| **Add Terrain** | creates a flat grid centred on the chunk's origin, and arms the Terrain tool |

The cell size sets how fine the ground can be shaped. The grid is split into
triangles along whichever diagonal of each cell is flatter, so ridges and
valleys stay crisp. One unit per cell suits the engine's metre-scale detail.
Most levels shouldn't need less.

Once a chunk has terrain, the same group sets it up:

| Field | |
| --- | --- |
| **Corner** | where the grid's first corner sits in the chunk; heights are measured up from its Y |
| **Cell size** | stretches the whole grid |
| **Texture scale** | how many units one repeat of a layer's texture covers (default 2) |
| **Layer 1–4** | the textures the ground is painted with; layer 1 is what unpainted ground shows |
| **Resize X / Z**, **Apply Resize** | grow or crop the grid from its far edges; the first corner stays put |
| **Remove Terrain** | |

A terrain with no layer textures is drawn in its normal colour, like an
untextured brush face.

## The Terrain tool

Press **T** or click **Terrain** in the toolbar. While it's armed, a ring
follows the cursor over the ground. Hold the left button to brush. The
continuous modes are rates, so holding still keeps building.

| Mode | Key | |
| --- | --- | --- |
| **Raise** | `1` | lift the ground |
| **Lower** | `2` | sink it |
| **Smooth** | `3` | pull each point toward its neighbours |
| **Flatten** | `4` | pull toward the height where the stroke started |
| **Paint** | `5` | paint the selected layer's texture |
| **Hole** | `6` | cut whole cells out of the ground |
| **Fill** | `7` | mend them |

| Key | |
| --- | --- |
| `Shift` + hold | the opposite mode for that stroke: lower instead of raise, fill instead of hole |
| `[` / `]` | radius down / up |
| `-` / `=` | strength down / up |
| `T` / `Esc` | put the tool away |

The **Terrain Tool** block in the panel holds the same controls, plus these:
- **Hardness:** how much of the radius works at full strength before the effect fades out.
- **Paint layer:** picks which layer Paint lays down. Choosing a layer switches to Paint.
- **Layer textures:** the terrain under the cursor (or the selected chunk's).

Every stroke is one undo step, from button-down to button-up.

A stroke stays on the terrain it started on: dragging off the edge of one
chunk's ground doesn't start painting its neighbour's.

## Holes

A hole cell isn't drawn and isn't ground: bodies fall through it, rays pass
through it, navigation treats it as open. Use holes to join terrain to brushes:
- cut the ground away where a cave mouth or a sunken bunker goes, and build the
  inside from brushes;
- build a sheer cliff from brushes along a line of holes.

## Clicking terrain

With the tool put away, clicking terrain selects its chunk. Things placed at the
cursor (entities, props, decals, the scenery brush) land on the ground.

## Compiled levels

The compiler leaves terrain out of CSG and BSP, so a heightfield is never
shredded into fragments. It keeps the terrain on its chunk in the `.fbsp`, and a
release build draws and collides with it exactly as the editor does. The
level's seal box is fitted around baked chunks' terrain too.

Terrain is **lit live**, not baked. Static lights reach it at full quality, with
real-time shadows. What it doesn't have:
- bounce light from the bake;
- `bake_exclude` rules.

It also doesn't cast into baked lighting: a hill doesn't shadow a brush floor
in the bake. Where that matters, make the light dynamic.

## Performance

- **Drawing:** the grid is drawn in 32×32-cell patches, each culled on its own.
- **Editing:** a stroke re-uploads only the rows it touched.
- **Collision:** reads the height straight from the grid under a body.
- **Rays:** walk the grid cell by cell.
- **Navigation:** navigation is built from 8×8-cell patches, so sculpting one
  hill re-bakes only the tiles under it.

The level file stores the grid packed. Heights are kept to the millimetre and
compressed, so a flat or gently shaped 128×128 terrain is a few kilobytes of
YAML.
