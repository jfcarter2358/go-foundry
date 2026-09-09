# Building levels

Level geometry is **brushes**: convex solids, each face individually textured,
grouped into **chunks**. Empty walkable space is whatever is not inside a brush,
so a room is not a shape you draw — it is a box you hollow out, or six slabs you
place around a gap.

```
level
  chunk           an origin, a yaw, a scale and an on/off flag
    brush         a convex polyhedron: a vertex list and a face list
      face        a planar loop of those vertices, with its own texture
```

Everything is authored in the 3D view. There is no floorplan and no top-down map.

## Making the first room

1. `Add... > Chunk...` in the Explorer. It is placed at the camera and starts
   with a hollow room already in it — six brushes around somewhere to stand.
2. Fly inside. You are in **object mode**: click a wall and the whole slab it
   belongs to lights up in cyan.
3. Drag it with `G` to make the room bigger.
4. Press `Tab` for **edit mode**, press `3`, and click one face of that slab. Now
   `E` extrudes it, `I` insets it and `B` subdivides it.

That is the whole loop: place solids in object mode, shape them in edit mode. See
[Editor basics](01-editor-basics.md) for the two contexts and
[Shaping surfaces](03-shaping-surfaces.md) for the shaping tools.

## Placing brushes

`Add... > Brush` offers three stamps, each two units across:

| | |
| --- | --- |
| **Box** | six faces. Sits *on* the surface you clicked rather than straddling it |
| **Wedge** | a box with one edge collapsed — ramps, roof slopes, buttresses |
| **Cylinder** | twelve sides and two caps. Pillars, tanks, towers |

Picking one *arms* the stamp rather than placing a single brush. A cyan box then
follows the cursor onto whatever surface it is over — snapped to the grid when
snapping is on — and **every click places another solid**, so a room is blocked
out in a dozen clicks instead of a dozen trips through the menu. `Esc` puts the
stamp away, as does picking the same kind again or **Cancel** in the Explorer's
banner. Right-clicking the view offers the same stamps under **Stamp brushes**.

Each brush lands selected, ready to be dragged into place once you are done
stamping, and `Ctrl+Z` takes back one brush per press.

Right-click a selected brush for **Duplicate** (a copy on top of the original,
ready to drag off), **Snap to grid** (every corner rounded onto the grid, whether
or not snapping is on) and **Delete**.

**Hollow** turns a solid into a room: it replaces the brush with the six brushes
of a shell filling its bounds. Block a building out as plain boxes, then hollow
the ones you need to walk inside. It works from the axis-aligned bounds, so
hollowing a wedge gives a box-shaped room.

## Brushes must stay convex

Every brush is required to be convex — every corner on or behind every face's
plane. Collision reads a brush's corners as a hull directly and the compiler
clips faces as half-spaces; both are *wrong* on a concave brush rather than
merely slow, so a dent in one is a dent nothing else in the engine knows about.

The stamps and the shaping tools preserve convexity by construction. Dragging a
single corner is the one thing that can break it, and when it does the editor
says so in the status line and in the brush's properties; compiling refuses
outright and names the brush.

An L-shaped room is two brushes. That is not a limitation to work around — it is
what keeps collision and the compiler cheap.

## Faces

Select a face in edit mode (`Tab`, `3`, click) and its properties sit below the
brush's:

| Field | |
| --- | --- |
| **Texture** | plus an optional **Blend Texture** and a grayscale **Blend Mask** |
| **Alpha** | 1 is opaque; anything less sorts into the transparent pass |
| **Double Sided** | draw it from behind as well |
| **Drawn** | off builds no geometry at all while the brush still collides — for a face buried against a neighbour |
| **Collision** | off makes this one face walk-through while the rest of the brush stays solid — a window opening in a wall |
| **UV** | offset, scale and rotation, in the face's own plane |
| **Smooth** | weld normals with the neighbours; see [Smooth shading](05-smoothing.md) |

The panel edits the primary selected face. To change a dozen at once, select them
all with `Ctrl+click` and use the right-click menu, whose rows say how many faces
they will reach.

## Solid, and not

**Solid** on the brush is collision for the whole thing. Turn it off and the
brush is decoration a body walks straight through — a light shaft, a bush, a
detail you do not want the player catching on.

With the per-face **Drawn** and **Collision** flags that gives the four useful
combinations: solid and drawn (a wall), solid and not drawn (a clip brush that
blocks an area invisibly), drawn and not solid (decoration), and neither — which
is a brush you should delete.

## Dynamic brushes

A brush is static by default. Its corners are baked into the compiled level's
geometry, clipped against its neighbours and partitioned into the BSP — none of
which survives it moving.

Tick **Dynamic** on a brush and it is left out of all of that, and a script can
move it during play:

```flang
@blast_door.prop.position = @blast_door.prop.position + <0.0, -3.0, 0.0>
```

| Property | |
| --- | --- |
| `position` | the brush's centre, in world space. Writing it moves the brush |
| `rotation` | euler angles in radians, turning it about its own centre |
| `velocity` | units per second; the engine keeps it moving with no script per frame |
| `angular_velocity` | radians per second, added to `rotation`; the engine keeps it turning |
| `dynamic` | read-only: whether this brush is one |

`velocity` and `angular_velocity` are rates rather than moves: writing one does
nothing at that instant, and every frame of play afterwards the engine slides or
turns the brush by it. A fan is one line —
`@fan.prop.angular_velocity = <0.0, 3.14, 0.0>` — and stops on a write of zero.
Nothing damps either one. Note that `velocity` is world-space like `position`,
while `angular_velocity` is in the brush's own basis like `rotation`, since it
is that property's rate of change.

**Its collision moves with it.** That is the point of the feature: what a body
hits is where the brush is drawn, not where it was authored. Frustum culling, the
spatial hash and raycasts all follow it too.

The authored corners are never rewritten. The movement is a placement riding on
top of them, so the level file always holds the brush at rest and leaving play
puts it back there.

Three things to know before relying on one:

- **Writing a placement on a brush that is not marked Dynamic is an error**, and
  the script log names the brush and the toggle. That is deliberate: without the
  toggle the compiler has already baked the brush, so the move would work in the
  editor and silently do nothing in a shipped build.
- **A dynamic brush cannot seal a level.** [Compiling](23-compiling-levels.md)
  needs the level's shell closed, and a brush that moves is not part of a shell.
  A doorway a dynamic door fills is a hole as far as the compiler is concerned —
  put a static frame around it, or accept the leak.
- **Nothing rides it.** A player standing on a rising lift is not carried up; the
  brush moves out from under them. Carrying riders is a chunk-scale feature —
  bind them with `AttachedChunk` and move the whole chunk instead.

## Chunks

Select a chunk in the Explorer to make it active — that is the one new geometry
goes into.

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
engine's default of 0.5 units — about a stair riser. Tick it to set a level that
makes the player pick their feet up more, or less.

## Level environment

Two more level-scoped blocks live in the **Environment** group: **Sky** and
**Fog**. The checkbox at the top of each creates and destroys the block, so
turning fog off is a different gesture from deleting it and keeps your numbers.
See [Sky and fog](08-sky-and-fog.md).
