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
| **Texture** | `(none)` on a fresh brush — an untextured face is painted with its own normal, see [Textures](04-textures.md#no-texture) — plus an optional **Blend Texture** and a grayscale **Blend Mask** |
| **Alpha** | 1 is opaque; anything less sorts into the transparent pass |
| **Double Sided** | draw it from behind as well |
| **Drawn** | off builds no geometry at all while the brush still collides — for a face buried against a neighbour |
| **Collision** | off makes this one face walk-through while the rest of the brush stays solid — a window opening in a wall |
| **UV** | offset, scale and rotation, in the face's own plane — on top of the face's own layout once it has been unwrapped, when **Reset to projected** forgets the unwrap |
| **Smooth** | weld normals with the neighbours; see [Smooth shading](05-smoothing.md) |

The panel edits the primary selected face. To change a dozen at once, select them
all with `Ctrl+click` and use the right-click menu, whose rows say how many faces
they will reach.

## Painting brushes

A face's texture normally tiles: it is projected onto the face's own plane and
repeats at the same size whatever shape the face is. That is what you want for
brick, and no use at all for *painting* a wall — every face projected onto one
texture shares every texel of it, so painting one wall paints the others too.

**Unwrap & Paint...** gives each face a region of one texture to itself, the way
a model's atlas does. It is in the brush's **Paint** section and on the
right-click menu: in object mode it takes whole brushes, in edit and detail mode
the faces you picked.

| Field | |
| --- | --- |
| **Texture name** | the texture to make; it is saved straight away, over any texture of the same name |
| **Size** | 64 to 1024, square |
| **Seam angle** | how far two faces sharing an edge may turn before they get separate regions |
| **Gutter** | empty texels kept around each region, so filtering and mipmaps do not bleed one into the next |

It lays the faces out, points them at the new texture, and opens it in the
[texture editor](04-textures.md) with a checker guide under an empty **Paint**
layer. The level updates as you paint; turn on **UV layout** under the canvas to
see which region is which face. A wall's region hangs the right way up — the top
of the wall is the top of its region.

Worth knowing:

- **Every face already wearing that texture is laid out again too**, so no two
  faces ever share texels. Unwrapping a new brush onto a room's atlas re-lays
  the whole room. The dialog says how many extra faces that is before it runs;
  if the number is large, the name probably belongs to a tiling material.
- Unwrapping makes a **fresh** texture. Whatever was painted on the old one is
  gone, so settle the geometry first.
- The layout rides the corners. Moving, turning or scaling a brush keeps its
  painting on it; dragging one corner stretches the painting with it.
- Subdividing and insetting keep each face's region. A face the unwrap has never
  seen — the side walls of an extrude in detail mode — goes back to tiling until
  you unwrap again.
- Texel density is the same across everything one texture covers, and the status
  line reports it as *texels per unit*. A large room on a small texture comes out
  blurry: use a bigger size, or paint it as several textures.
- A compiled level keeps the painting exactly, however the compiler cuts the
  faces up.

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
| `scale` | a multiplier on its authored size, growing about its own centre |
| `waypoints` | read-only: the [waypoint animation](#waypoint-animations) it is following, or null |
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

## Waypoint animations

A waypoint animation is a path: a row of arrows placed in the 3D view, joined by
a line, each saying where an object should be, how it should be turned and
scaled there, and when to get there. A script sends a dynamic brush along one —
or an entity, a sprite, an emitter, a sound, a light or a volume — and the
engine does the rest.

A path that belongs to a thing rather than to the level — a ship's door, which
has to close wherever the ship has flown — is authored on the entity's type
instead, and rides every placement: see
[Entities that carry paths](11-entities.md#entities-that-carry-paths).

**Placing one.** `Add... > Waypoint Animation > Place By Clicking`, then click in
the view: that places the animation's first point and selects it. **Ctrl+click**
somewhere else and the next point goes there, straight after the selected one —
keep Ctrl+clicking to lay the rest of the path. With an earlier point selected, a
Ctrl+click inserts between it and the one after. The arrows are only ever drawn
in the editor. Two other ways to place the first point:

- Select anything a path can carry and use **New Animation From Here** in its
  **Waypoints** group. The first point goes where the object is, turned as it
  is — a brush's centre, or, for a directional light, a few units in front of the
  camera aimed where the light shines — so the first Ctrl+click is where it goes
  first.
- Right-click in the view: **Add waypoint animation here**.

**Shaping it.** Click an arrow to select its point. Move, rotate and scale (G, R,
C) act on that point; select the animation's row in the Explorer instead and the
move gizmo carries the whole path. A new point copies the rotation and scale of
the one before it (for its time, see *Timing it*). The Properties panel lists the points in order, and
has **Add Point**, **Insert After**, **Move Earlier/Later** and **Delete Point**.
Del deletes the selected point and joins the point before it straight to the
point after, leaving the point before selected so a Ctrl+click fills the gap;
deleting an animation's only point deletes the animation.

| Point field | |
| --- | --- |
| **Position** | world space: where the object goes — a brush's centre, anything else's position |
| **Rotation** | the object's own turn there; a directional light shines the way the arrow points |
| **Scale** | a multiplier on a brush's or an entity's authored size |
| **Time** | when the object *arrives* at this point, in seconds from being sent — so the first point's time is how long the leg from wherever the object started takes, and 0 starts the path on it. Held between the times of the points either side |
| **Events** | atoms posted to the object when it reaches this point, each as `(:atom:, animation ref)` — see *Playing it* |

Each kind reads the parts of a point it has. A brush and an entity take all
three; a sprite takes all three too, its scale's X growing its width and Y its
height; an emitter, a sound and a volume ignore the scale; a point light only
moves; and a directional light only turns, which is how a sun is swept across the
sky.

Position, rotation and scale all ease together across a leg, so an object that
turns on the way to a point finishes turning as it arrives. The rotation eases between
the numbers as written — 0° to 270° is three quarters of a turn, 0° to 720° two
whole ones — so a spinning platform is one point, and 350° to 10° goes the long
way round (write 370° for the short way). Two points at the same time make the
leg between them instant.

**Timing it.** Selecting an animation opens its **timeline** along the bottom of
the 3D view. The ruler is in seconds, and each point is a diamond at the moment
the brush arrives there. Drag a diamond to retime its point: it moves in 0.05 s
steps (hold **Shift** to drag it freely), and it cannot pass the points either
side of it, because the order of the list is the path. **Length** is how long
the whole animation takes. Change it and every point's time stretches with it;
drag the last point earlier than the end and the object waits on it for the rest,
and only then hears `:waypoints_end:`. A point Ctrl+clicked onto the end of the
path is timed as if the brush walked to it at two units a second, and the length
grows to fit; one inserted between two points takes the moment halfway between
them, so nothing else moves. Each leg's length is written above its line in the
view, and hovering a diamond shows the leg's distance, time and speed.

Click or drag anywhere else on the timeline to scrub the playhead. An orange arrow
shows where the path is at that moment, and **Key At Playhead** adds a point
exactly there, on the path, ready to be moved. `|<`, `<`, `>` and `>|` jump to the
start, the previous point, the next point and the end.

**Previewing it.** Pick an object in the timeline's **Object** box (or the
animation's **Preview** group) — any dynamic brush, entity other than a vehicle,
sprite, emitter, sound, light or volume — and it follows the playhead without
entering play, setting off from where it was authored — the same place play
starts it. **Play** runs the playhead, **Loop** replays it, and points can be
dragged or retimed while it runs; **Stop** puts the object back. While the
preview holds an object it cannot be moved by hand, and saving, compiling and
undo all see it where it was authored. An orange line shows the first leg, from
the object to the first point, which is the one nothing else draws. Each of those
objects has a **Waypoints** group in its own panel with the same Preview button.

**Playing it.**

```flang
def open(:interact:, who ref) -> null
    play_waypoints(@blast_door, @door_open)
end

def arrived(:waypoints_end:, src ref, anim ref) -> null
    log("the door is open")
end
```

`play_waypoints` sets off from wherever the object is now, `stop_waypoints` leaves
it where it has got to, and `@blast_door.prop.waypoints` says which animation it
is following. The object hears `:waypoint:` at every point and `:waypoints_end:`
when the animation's length runs out. While a brush or an entity follows a path
its `velocity` waits — and an entity does not fall — picking up again from
wherever the path left it. The player and vehicles cannot be sent along one:
their motion belongs to the player's input and the drive model.

**Events on a point.** To hear about one particular point by name, give it an
event: select the point, type an atom into its **Events** group (`door_open`, or
`:door_open:` — the colons are optional) and press **Add Event**. When the object
reaches that point, it is posted `(:door_open:, animation ref)`, straight after
the point's `:waypoint:`; a point with several events posts them in the order
listed. The name must be lowercase letters and underscores, and cannot be one of
the engine's own events. A point's events show after it in the point lists.

```flang
def at_top(:lift_top:, anim ref) -> null
    @top_gate <- (:open:,)
end
```

Unlike counting `:waypoint:` indices, an event stays on its point when points are
inserted in front of it.

### Paths for AI agents

An AI agent can walk a waypoint animation too. It finds its own way to each point
and ignores the times. Each point can also carry an **AI** group: wait here, run
here, take cover near here, hold fire on the way, or stop anywhere inside a volume.
See [AI agents](24-ai.md#waypoint-paths).

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

The **Player** group decides what the player can do when they arrive. Untick
**Player control** and they can neither move nor look around — the view stays
where the spawn aims it; untick
**Gravity** and they hang where they spawn. Both are for a start the level runs
for them — a ride in, an intro — so a script hands them back with
`@level.prop.player_control = true` and `@level.prop.player_gravity = true`.

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
