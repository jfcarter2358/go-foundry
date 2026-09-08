# Building levels

Level geometry is a stack of **chunks**. A chunk is one storey — a flat 2D map of
convex polygons, each with a floor height and a ceiling height. Put a chunk on
top of another and you have two floors of a house. You cannot have one sector
over another inside a single chunk; that is what the stack is for.

```
level
  chunk           a storey. Has an origin, a yaw, a scale, an on/off flag
    sector        a convex polygon with a floor and a ceiling
      edge        a side of the polygon: either a wall or a portal
      floor       optional, has a height and a texture
      ceiling     optional, same
    biome sector  painted decoration over the floors (see the Biomes guide)
```

## Making the first room

1. `Add... > Chunk...` in the Explorer. Name it and it is placed at the camera.
2. Open the **2D Window** — this is where outlines are drawn.
3. `Shift+click` on the map to drop points. Four of them, roughly square.
4. `Ctrl+click` each point in order, then press `Shift+J` (**Join & Close Loop**).
   That draws the edges and closes them into a sector.
5. Back in the 3D view, click the floor and set its **Height** and **Texture**.

`J` joins the picked points into a chain without closing it — for a wall across a
room, or an outline you mean to finish later. `Shift+J` closes the loop, which is
what actually creates a sector.

`Shift+click` near an existing edge splits that edge instead of dropping a free
point, which is how you add a corner to a room you have already made.

## The 2D map

| | |
| --- | --- |
| `W` `A` `S` `D`, middle-drag | pan |
| Wheel | zoom |
| Left click / drag | select and move a corner |
| `Ctrl+click` | add to the selection |
| `Shift+click` | split the nearest edge, or drop a free point |
| Right click | remove the point under the cursor, else deselect |
| `J` / `Shift+J` | join the picked points / join and close the loop |

Two buttons at the top of the Map section decide which of a corner's two handles
you are dragging: **Floor Verts** or **Ceiling Verts**. A corner carries a height
offset for each surface, so tilting a floor and tilting the ceiling over it are
separate edits. `Refresh Heights` re-samples biome paint after you have moved
geometry under it.

Sectors share their corners. Drag a corner that two rooms meet at and both
follow — which is what keeps the seam closed. Sectors must stay convex; cut an
L-shaped room into two sectors joined by a portal.

## Walls and portals

Every edge is one or the other, and converting between them keeps the edge's ID,
so a script addressing it does not break.

**A wall** is drawn and solid. It has a texture, a visibility flag, a collision
flag, alpha, its own UV transform, and can carry a surface mesh, detail and a
script.

**A portal** connects two sectors. It is not drawn and does not collide — you
walk straight through. Select an edge and set **Kind** to `portal` to make one.

A portal's useful extras:

| Field | |
| --- | --- |
| **To Sector** | which sector is on the far side. Usually inferred |
| **Draw Gap** | fill the height difference between the two sectors with geometry |
| **Gap Texture** | what that filler is textured with |
| **To Frame** | join two spaces that move relative to each other — see [Vehicles and frames](12-vehicles-and-frames.md) |

`Draw Gap` is how a doorway with a header over it works, and how a step between
two floors gets a riser. Lowering a sector's ceiling with `Draw Gap` on is also
how the dropship's blast door is built: the gap *is* the door.

**Pinned** on a wall stops the editor's portal inference from converting it.

## Floors and ceilings

Both are optional. A sector with no ceiling is open to the sky; one with no floor
is a hole you fall through.

| Field | |
| --- | --- |
| **Height** | chunk-local Y. Per-corner offsets on top of it make a slope |
| **Texture** | resolved against `textures/` |
| **Visible** (eye) | hides the structural texture without disabling the surface — for a floor completely covered by biome paint |
| **Double Sided** | draw and collide from underneath too |
| **Alpha** | translucency |
| **Shape Surface** | shape it — see [Shaping surfaces](03-shaping-surfaces.md) |

**Skirt** (floors only) closes the band between this floor and whatever is
underneath it — the slab edge of a storey, the rock face under an overhang.
`ceiling` stops at the ceiling of the room below; `floor` carries all the way
down to its floor. It takes its own texture, or the floor's if you leave it
empty.

## Openings

An opening is a hole cut through a floor or ceiling that connects to the sector
below or above — a stairwell, a hatch, a gap in a mezzanine. Bodies fall through
it and the renderer sees through it.

Draw one on the 2D map: arm the tool, click the corners, `Enter` to close,
`Escape` to abandon. Which surface it cuts follows the Floor Verts / Ceiling
Verts switch. It attaches to whichever sector its centre lands in, and what lies
under it is worked out fresh each time, so it cannot go stale.

Openings connect; **surface meshes** cut shapes. If you want a hole in a *wall*,
that is a surface mesh hole, not an opening.

## Chunks

Select a chunk in the Explorer to make it active — that is the one the 2D map is
drawing and the one new geometry goes into.

| Field | |
| --- | --- |
| **Origin X/Y/Z** | where the chunk sits. Everything in it is relative to this |
| **Rotation Yaw** | Y axis only; a chunk does not pitch or roll |
| **Scale** | uniform |
| **Carried By** | bind the chunk to a vehicle entity's frame, making it an interior |

Right-click a chunk to **Export...** it to `resources/chunks/`, and
`Add... > Import Chunk...` to bring one into another level. That is how a
building gets reused.

A chunk can be **switched off** — right-click, `Enabled`. A disabled chunk is not
drawn, not solid, and its buffers are released. This is how a level streams: a
script writes `@east_wing.prop.enabled = true` as the player approaches and
`false` behind them. Author one starting off with `disabled: true` in the level
file and it is never built until something asks for it.

## The spawn point

One per level: where the player starts and which way they face. It is not an
object in the level, it is a property of the level.

`Add... > Player Spawn`, or right-click the Spawn row in the Explorer for
**Move To Camera** and **Face Camera** — fly to where you want to start, aim, and
press both.

## Player step height

Under **Player** in the properties panel. It is how tall a riser the player walks
up rather than being stopped by. Leave the checkbox off and it follows the
engine's default, which is the player's own height. Tick it to set a level that
makes the player pick their feet up less.

## Level environment

Two more level-scoped blocks live in the **Environment** group: **Sky** and
**Fog**. The checkbox at the top of each creates and destroys the block, so
turning fog off is a different gesture from deleting it and keeps your numbers.
See [Sky and fog](08-sky-and-fog.md).
