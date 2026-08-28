# Go-Foundry

## Engine Design

The go-foundry engine takes a doom/quake approach to a game engine. The world consists of the following structure:

objects:
  - name: chunk
    description: Chunks are containers for a group of sectors. They have a bounding box enclosing the entire thing which is used to dictate whether the player needs to calculate any sort of collision checks against the items inside of that chunk
    children:
      - name: sector
        description: A sector is a 2d map of connected convex polygons. The edges of the polygons are either walls, or portals (which are essentially empty walls, not displayed and no collision checks. They connect two sectors together). The sector can have a ceiling and a floor (though they are optional) and the ceiling point positions can be modified to create sloped walls and such. Each wall, floor, and ceiling of the sector can have different textures assigned to them
        children:
          - name: wall
            description: A wall connects a ceiling to a floor along the edge of a sector. Walls can be textured and optionally (though by default yes) be included in collision checks
          - name: portal
            description: Portals do not have collision or any sort of display and are used to connect sectors together
          - name: ceiling
            description: The ceiling is a convex polygon created by following the edges of the sector along the top which can be optionally displayed and optionally checked for collision
          - name: floor
            description: The floor is a convex polygon created by following the edges of the sector along the bottom which can be optionally displayed and optionally checked for collision. Like every other surface it can carry a **surface mesh** — an editable web of points and faces, shaped in the 3D editor, which is how terrain, steps, pits and holes are all authored. See "Surface meshes" below.
      - name: biome sector
        description: A polygon painted over a chunk's floors that clothes them — GPU-instanced scatter (grass, rocks, wildflowers, cattails, moss), a conforming textured ground patch, or, for the Water kind, an animated water surface filling the polygon. Biome sectors are decoration and never take part in collision.

## Surface meshes

Every wall, floor and ceiling is flat until somebody shapes it. When they do, it
grows a **surface mesh**: a shared pool of points and a set of n-gon faces
indexing into them — the same representation the model editor runs on, and
edited with the same gestures. Select a face in the 3D view, press Tab, and the
selection modes retarget from the sector's topology to that surface's own:
`1`/`2`/`3` pick its points, edges and faces, `E` extrudes, `I` insets, `C`
connects two points into a new edge, `H` opens a face as a hole.

This replaced four separate mechanisms — per-vertex height offsets, Delaunay
"terrain" points, `Extrusion` prisms, and floor/ceiling `Opening` polygons —
three of which could not be combined with each other, and none of which could be
applied twice. An `Extrusion` was a single prism, so there was no way to put a
bump on the floor of a recess or a bevel on a hole's rim. Extruding a mesh face
is a topological operation that can be applied to its own output, so both fall
out for free.

Two things make it work as part of a *level* rather than as a free-floating mesh:

- **Points are parametric, not positional.** A point is `(U, V)` in the face's
  own space — along/down for a wall, chunk-local X/Z for a floor — plus a
  `Depth` along the face's depth axis. A `SurfaceFrame` converts that to world
  space using the sector's *current* geometry, so the mesh rides the surface:
  change the ceiling height and the wall's mesh stretches with it. Free 3D
  positions would be orphaned the moment anybody dragged a corner on the map.
- **The outline belongs to the sector, not to the mesh.** Points bound to an
  outline corner are re-derived on every rebuild and cannot be dragged across
  the surface. Sectors share corners with their neighbours and must stay convex
  for portal culling, so a mesh that owned its own outline would let the three
  faces meeting at a corner drift apart. When the outline itself changes — a
  wall split, a corner inserted or deleted — `RemapSurfaceMesh` carries the
  interior across rather than discarding the shape.

Collision follows the mesh for floors and ceilings: the height query reads the
same triangles that were drawn, so a bump you can see is a bump you walk up. A
**wall's collision plane stays flat** — the mesh moves what you see, and what
stops you is still the wall's segment on the 2D map, with the mesh's `Hole`
faces punched through it as pass-through windows. Bumps you walk into; holes you
walk through. That is the same contract procedural detail already ships with,
and it keeps wall collision a segment test rather than a swept mesh query.

`Opening` survives alongside this, because it is not a shape but a *connection*:
it pairs two faces of one slab, is walked by the portal-culling BFS, and is what
lets a body fall between storeys. Surface meshes cut geometry; openings connect
sectors.

Levels written before surface meshes existed load unchanged — the loader
migrates `extrusions:` and `terrain_points:` into a mesh once, and the writer
emits only meshes from then on.

A level also carries one optional **spawn point**: the position and facing gameplay starts the player at. It is a property of the level rather than an object in it — there is exactly one, it is never drawn outside the editor, and it never collides.

## Entities

Everything above is geometry — the shape of the world. An **entity** is a thing
*in* it: a transform, a bounding volume, and a bag of user data. Entities are the
only objects a level places, and the only things besides the player that move.

An entity carries:

- **an ID**, unique within the level, which is how a script addresses one (`@id`)
- **tags**, which are how a script addresses all of a kind at once (`#tag`)
- **a transform** — position, rotation, scale
- **a velocity and an angular velocity**, integrated every frame during play
- **a bounding volume** — an AABB, a sphere or an upright cylinder, sized by
  half-extents and optionally offset from the entity's origin, so a model can
  stand on its feet while its volume is centred at hip height
- **model attachments**, any number of them, each with its own offset, rotation,
  scale and texture *relative to the entity*, and each with its own set of named
  animations
- **attributes**: arbitrary named values of type `int`, `float`, `bool` or `str`
  that the engine only stores, and the scripting engine reads

Every part beyond the transform is optional. An entity with no models is a
perfectly good trigger volume or script anchor; one with no attributes is a prop.

### Blank entities and definitions

An entity is authored one of two ways, and starts as the first.

A **blank entity** is the editor's default placement: a box with an ID, no model
and stock defaults, dropped where you click and described from there in the
properties panel — pick a mesh, set the bounds, add attributes. It has no `type`,
and the level file carries all of it. Nothing ships in `resources/entities/`,
because nothing has to.

A **definition** is what a blank entity becomes when a second level wants the same
thing. It has two halves, in two files: **what it is** lives under
`resources/entities/<name>.yaml` — its models, its bounds, its default attributes,
the tags that classify it — and **where it is** lives in the level that places it,
as an *instance* naming that definition by `type`.

The split is what makes a type editable — giving every barrel in the game a second
animation is one file, not a search across every level that ever placed one — and
it keeps level files small, since a level says "a barrel, here" rather than
restating the mesh, the rig and the bounding box each time.

An instance may override what it needs to. Tags **union** with the definition's,
attributes are **merged by name** over it, and model attachments are matched by
name so an instance can adjust one attachment's mesh, texture, transform or
visibility — and may add an attachment of its own, which the level then writes out
in full. Saving writes only the difference, so a level that has not overridden
something keeps tracking the definition rather than freezing a copy of it.

### Motion

Velocity and angular velocity are plain state the game sets and the engine carries
forward — Euler integration, no damping. Motion is integrated during play only; in
the editor entities stay exactly where they were placed, while their animations
keep running so a clip can be seen while placing the thing that plays it.

### Collision

Stopping an entity going through things is the engine's job, not a script's. Four
flags say what an entity takes part in, all authored on the definition:

| flag | what it does |
| --- | --- |
| `world` | resolve against walls, floors, ceilings and slopes — the same resolvers the player uses |
| `entities` | push apart from other entities that also set it, and from the player |
| `gravity` | fall, and stop when something is underneath |
| `movable` | whether a resolution may displace *this* entity (default **true**) |

The first three default to **off**. An entity placed before any of this existed
passes through the world exactly as it did, and an engine update must never
silently start shoving a level's props around.

`entities` has to be set on **both** sides of a pair. Solidity is mutual: an
entity that could push something which could not push back would make which of
the two moves depend on the order the level happens to list them in.

`movable: false` is immovable scenery — it pushes others and is never pushed
itself, and gravity does not touch it. That is how a boulder is expressed:
`entities: true, movable: false`.

Two engine-wide master switches sit over the top (`8` and `9` in gameplay, and
checkboxes in the editor's Entities panel). A per-entity flag says what *wants*
to collide; the master says whether the engine is listening. They exist so
"this entity is not solid" can be told apart from "collision is off" in one
keypress.

**What this is not: a physics engine.** There is no restitution, no friction, no
angular response and no solver iteration. Each frame, everything falls and moves,
then the whole set is separated at once, then anything a separation displaced is
settled back against the world. An overlap is pushed apart along its **minimum
translation vector** — the shortest way out — which is what makes something
dropped onto something else settle on top rather than squirt out sideways.

For two axis-aligned boxes that is whichever of the three axes they overlap
least on, and that case keeps its own arithmetic: it is by far the most common,
and every stack in every existing level is tuned against it. Anything rotated or
round goes through **GJK/EPA** instead, over the support functions in
`internal/collision/shape.go`. One pair of routines covers every combination of
box, oriented box, ellipsoid and cylinder, including the ones with no tidy closed
form — which is what a hull that can pitch and roll needs.

Three details earn their keep, all of them about stacks settling exactly rather
than sagging a little further every frame:

- An entity resting on the ground **cannot be pushed downward**. Splitting an
  overlap evenly is right for two things floating into each other, but the moment
  one is on the floor, half the separation goes into pressing it through that
  floor and the world resolve undoes it. Refusing hands the whole separation to
  the one that can take it.
- Pairs are visited **bottom-up**. With one solver iteration, a correction is not
  re-propagated to pairs already visited, so a stack resolved top-down separates
  its topmost box from a middle box that has not yet been lifted — and ends every
  frame one gravity-step short. Working upward means each entity is final by the
  time the one above it is resolved against it.

The ground flag an entity exposes is last frame's, deliberately: it is written
and read during the same pass, and reading the live value would make the result
depend on the order the level lists its entities in.

### Moving quickly

Every resolver here is *positional*: it looks at where a body ended up and
pushes it back out. That holds while a frame's movement is small against the
body, and fails completely when it is not — a step that clears a wall and leaves
the sector in one go lands somewhere the sector lookup returns nothing for, so
nothing resolves and the body keeps going.

So a move is **split into steps no longer than half the body's smallest
half-extent**, and the ordinary resolve chain runs on each. That is deliberately
not a second collision routine: the resolvers stay the only description of what a
wall does, and at walking speed the split produces exactly one step and exactly
the arithmetic it always did. Only something fast enough to need it pays for it.

Past a ceiling on the number of steps the *displacement* is shortened rather
than the step count being clamped, which imposes a speed limit — for the player,
around 38 units a frame — and fails in the recoverable direction. Clamping the
count instead would hand back steps larger than the body at precisely the speeds
where going through a wall is most likely.

`SweepHit` is the general shape-against-shape form, by conservative advancement
over `Distance`: measure the gap, advance by no more than the gap, repeat. A step
is never longer than the clearance, so there is no step size an obstacle can hide
between.

## Frames

Until vehicles there was one coordinate system: the world. Every chunk's
geometry is baked into it at load, and every entity's position is expressed in
it. A **frame** is a second one — a rigid transform, carried by an entity that
moves, that geometry and other entities can live in.

The transform is the trivial part. What earns the idea is that a body inside a
frame stores its position **in that frame's coordinates**, so the whole existing
collision path — which takes a chunk, a sector and a position, and knows nothing
about which space they are in — resolves a player walking around a moving ship
with no change at all. Local gravity is local −Y. A rider is carried exactly,
because nothing had to carry it: it never moved.

A chunk names its frame with `frame: <entity id>`, and its geometry is authored
around that frame's origin. An entity names one the same way. Nothing else
changes shape:

- A frame-owned chunk is **kept out of the world spatial hash**. Its geometry
  sits around its own origin, so indexing it by those coordinates would have a
  player standing at world (0,0) fall into a ship parked a mile away. Each frame
  is searched by scanning its own handful of chunks instead — usually one, which
  is shorter than the hash lookup it would replace.
- A frame's transform **ignores its owner's scale**. A frame is a place to
  stand, not a model: scaling one would scale the deck without scaling the body
  walking on it.
- Frames **nest**, which is how a shuttle in a carrier's hangar works. Nothing
  downstream sees the nesting — render and collision only ever get a final
  matrix and a local space — but building the transforms does, so parents are
  always resolved before children, a frame may not end up inside itself, and the
  chain is depth-limited.

Three things are deliberately not in a frame. **Biome paint and procedural
detail** build their meshes and instance transforms directly in world
coordinates with no model matrix anywhere in the pipeline, so a framed chunk
would leave its grass standing where the ship used to be; a vehicle interior is
a built environment rather than terrain, and the editor refuses to paint one.
And **two entities in different frames do not collide**: comparing their volumes
would be subtracting numbers that do not mean the same thing, and the one pair
that is always cross-frame is a rider against the hull carrying it — which it is
inside by definition, so resolving that overlap would eject it through the wall.

Anything that compares positions across frames — a script's `enter`, an
`interact` press, a volume test — converts to world space first. That is what
`FrameSet.WorldPosition` is for, and a local position and a world one are
otherwise not numbers that can be subtracted.

## Vehicles

A vehicle is an **entity with a `vehicle:` block**, not a type of its own. An
entity already carries an ID, tags, attributes, a script and its parameters, a
transform, a velocity, bounds, collision flags and model attachments with
animations — which is the whole of what a vehicle needs before anything
vehicle-specific starts. This is the inverse of the argument for `Volume`: a
volume is a *smaller* idea than an entity so it gets its own type, and a vehicle
is a strictly larger one, so it extends rather than duplicates. Scripting
therefore needs no new ref kind, and `@id` and `#tag` reach vehicles already.

```yaml
vehicle:
  mode: air                                # ground | air | water | space
  interiors: [shuttle_deck, shuttle_roof]  # chunks with `frame: shuttle`; omit for seat-only
  seats:
    - name: pilot
      driver: true       # sitting here hands over the movement keys
      position: {x: 0, y: 0, z: -3}   # vehicle-local, the occupant's feet
      eye: 0.7
      exit: {x: 3, y: 0, z: -3}       # where they are put down on leaving
      yaw_limit: 1.2                  # look cone; omit for unrestricted
  drive:
    max_speed: 40        # every field optional; unmentioned ones keep the
    accel: 14            # mode's defaults, and all are script-writable
```

### Orientation

`Entity.Rotation` is an Euler triple, which cannot express a tumbling body
without gimbal lock — and integrating an angular velocity *as* Euler rates is
wrong even before it locks. So a vehicle's **quaternion is authoritative**, and
`Entity.Rotation` is written back from it each frame so scripts, the editor
panel and the saved file keep working on the field they already know. Nothing
else in the engine learns quaternions.

Ground and water vehicles are flattened to a yaw every frame. Their *body* may
tilt to the surface for looks, but the frame an occupant stands in keeps its own
up, or walking around a cabin would fight the terrain.

### Riding

Being aboard is two fields: a **frame**, and optionally a **seat**. Seated pins
the occupant to the seat every frame — a seat is a constraint, not a starting
position — and standing lets them walk the deck. Sitting down and standing up
are the same entity changing one string, which is what makes the two authored
modes one system rather than two.

Mounting is a **change of frame, not a teleport**: the occupant's position is
converted out of whatever space it was in and into the vehicle's, so it ends
where it started in the world and is carried from then on.

### Frame portals

A portal whose `to_frame:` names another frame joins two spaces that move
relative to each other — a shuttle's ramp onto a hangar deck, or onto the ground
(`to_frame: world`, a reserved name, since `""` already means "does not cross
frames"). Crossing carries position, velocity **and** facing through the relative
transform; converting all three is what makes it seamless, since converting only
the position leaves the walker arriving sideways.

**Frames must be gravity-aligned to be joined.** Every resolver here is
XZ-planar: it assumes down is −Y, a footprint is a 2D polygon and a height is a
scalar. Two frames only reconcile inside those assumptions while their up
vectors agree, because then the relative transform is a yaw and a translation —
and a yaw-rotated polygon is still a polygon. A ship resting on a deck qualifies,
and so does one flying straight and level. A barrel-rolling ship does not, and
while misaligned the portal **seals**: it resolves as a solid wall, so nobody
walks out of a doorway that is currently pointing at the sky. Lifting the rule
would mean making collision non-planar, which is a rewrite of the engine's
oldest assumption rather than a feature.

Cross-frame *visibility* falls back to frustum-only culling. Portal culling only
ever removes sectors the frustum already accepted, so the far side is still
drawn — the engine just does not get to skip anything behind it.

### What this is not

There is no inertia tensor and no torque. Controls are **rates**: a vehicle turns
as fast as its tuning says, which is what makes an arcade drive model
predictable to author against. A hull striking a wall stops; it does not spin.

### The player

The player is the one entity the engine creates rather than reading from a level:
a position, a velocity and a bounding box, with the camera riding on it. It has no
type and no models, because the view is from inside its head, and it is never
saved. Its world collision is the main loop's own resolve path rather than the
`world` flag, but it does take part in entity-against-entity collision on the same
terms as anything else — so a solid crate is solid to walk into, and a movable one
is solid to shove.

## Models

Models and their animations are authored in the **model editor**, which reads and
writes `.fmdl` documents in `models/`. An entity definition is what places one in
a world: an attachment names a model file, and the animations it declares name the
clips inside that file.

## Level Composition

Levels are composed of chunks. These chunks can be thought of as floors of a house. They are ultimately "one level" each (though stairs and ramps can be included, you just cannot have a sector that goes over top of another). Chunks can then be combined together to create the larger level, so you might stack the "second floor" chunk on top of the "first floor" chunk to create a two-story house

Chunks are defined by their floor and ceiling sector maps, which are a 2d map defining the bottom (or top) sector definitions which consist of convex polygons connected. Where they connect can be a wall or a portal, portals are empty walls with no collision that serve to connect sectors. The height of the sector maps' points can be changed as well as their positions in the horizontal plane can be offset to create things like sloped walls

Sectors have textures and their positions are baked into an optimized BSP-tree for collision checks

## Game Loop

The main game loop will run physics/movement calculations in compute shaders wherever possible to improve performance. Additionally it will host the script event loop once that is eventually implemented.

## Rendering

Rendering should cull as many chunks/sectors/walls as possible _before_ rendering. The goal is to make the engine as performant as possible. Biome scatter uses GPU instancing.

## Collision Checking

The player carries an axis-aligned bounding box, and everything it is resolved against is level geometry. First it works out which world chunk it is in; once it knows that, the chunk's spatial index gives it the sector. If the floor carries a surface mesh, a 'top-down' check figures out which of its triangles the player is over and the collision is resolved against that triangle and any applicable edges. Walls, floors, ceilings, slopes and the openings cut through walls are each resolved in turn.

An entity with `collision.world` set goes through those same resolvers. It is
handed to them as a *proxy* placed at its bounding volume's centre with that
volume's scaled extents, because that is the shape they are written against —
an entity's own position is its base, and its bounds can be offset from it
besides. Whatever the resolvers do to the proxy is applied back as a delta.

The floor and ceiling clamp is the one part entities do not share. The player's
`ResolveFloorCollision` is internally asymmetric — its floor treats the position
as the body centre while its ceiling treats it as the feet — and that asymmetry
is load-bearing tuning for how gameplay feels, so entities get their own clamp
that is symmetric about the volume's centre. The two share the underlying height
queries (`sectorFloorHeightAt`, `sectorCeilingHeightAt`) rather than the clamp.

## Level format

Levels are YAML files holding the chunk and sector information, the biome paint, the lights, an optional top-level `spawn:` block giving the player's starting position and facing, and an optional top-level `entities:` list. Textures are named, not pathed — the loader resolves a name against `textures/`.

Entities sit at the level rather than inside a chunk because they move: one that
walked out of the chunk it was authored in would otherwise have to be re-parented
mid-frame, and its saved position is world-space for the same reason. Which chunk
and sector an entity is currently in is derived every frame, never authored.

Each entry gives a placement, and optionally names a definition by `type`. With
no type it is a blank entity, and every field below is its own rather than an
override:

```yaml
entities:
  - type: barrel          # optional; resources/entities/barrel.yaml
    id: barrel_01         # unique in the level; how a script says @barrel_01
    position: {x: 4, y: 0, z: 2}
    rotation: {x: 0, y: 1.57, z: 0}
    velocity: {x: 0, y: 0, z: 0}
    angular_velocity: {x: 0, y: 0, z: 0}
    tags: [quest_item]    # unioned with the definition's
    attributes:           # merged over the definition's
      health: {type: int, value: 50}
    models:               # matched by attachment name
      - name: lid
        texture: gold
        path: models/lid_gilded.fmdl   # an instance may swap the mesh too
    collision:            # per-flag override; unmentioned flags follow the type
      movable: false
```

And a definition describes the type itself:

```yaml
id: barrel
description: A barrel.
tags: [prop, breakable]
collision:                # all three default off; movable defaults true
  world: true
  entities: true
  gravity: true
bounding: cylinder        # aabb | sphere | cylinder
bound_half_x: 0.4
bound_half_y: 0.5
bound_half_z: 0.4
bound_offset: {x: 0, y: 0.5, z: 0}
models:
  - name: body
    path: models/barrel.fmdl
    texture: wood
    offset: {x: 0, y: 0.5, z: 0}
    animations:
      - name: idle        # what the game calls it
        clip: idle_cycle  # what the .fmdl calls it; defaults to `name`
        loop: true        # omit to follow the clip's own flag
        autoplay: true
attributes:
  health: {type: int, value: 25}
```

## Package layout

The engine is a tree of packages under `internal/`, layered so that nothing
imports upward. `internal/layering_test.go` asserts the direction per package
through `go list -deps`, and fails on the first import that runs the wrong way —
Go itself will not complain until two packages reach for each other, by which
point the cycle is someone else's problem to unpick.

Bottom to top:

```
cmd/foundry           main(), and runtime.LockOSThread() before anything else
cmd/gentextures       the texture generator
cmd/flang             the language, standalone

internal/strutil      one string measure
internal/fileutil     asset paths, atomic writes, directory scans
internal/platform     the window, GL context, key state, frame clock
internal/geom         scalar maths, Point, triangulation, smooth normals
internal/texdoc       the layered texture document (pure data)
internal/flang        the scripting language (pure; see below)
internal/gfx          Geometry and its draw path, the shader program and its
                      uniforms, the texture registry, GPU skinning, the camera
internal/gizmo        immediate-mode line/wireframe drawing
internal/imguibackend ImGui/SDL/GL plumbing, clipboard, shared widget idioms
internal/hooks        write-only buffers engine code drops script facts into
internal/texture      .ftex/.pal I/O, the global palette, the editing session
internal/model        the .fmdl document: meshes, bones, skinning, poses, UVs
internal/world        the level data model — chunks, sectors, walls, entities,
                      volumes, biomes, lights. One package because the types are
                      mutually recursive.
internal/detail       the CPU port of the displacement shader, user presets
internal/biome        how biome paint drapes over the floor beneath it
internal/collision    the resolver: player, world, entity-against-entity
internal/render       every GPU pass — shadows, scatter, detail, culling, AA
internal/script       the engine side of scripting: Host, refs, messages, verbs
internal/levelio      the YAML boundary, and the play-session snapshot
internal/uiwin        multi-window plumbing, the F1 keybinding overlay
internal/textureui    the texture editor's ImGui window
internal/editor       the level and model editors
internal/app          the render loop, input, the keybinding table
```

Test-only packages sit outside that order: `internal/testenv` (repository root,
the GPU-upload seam), `internal/leveltest` and `internal/modeltest` (fixtures
more than one package's tests build on), and `internal/gltest` (a real GL
context for the shader smoke tests). A test helper cannot cross a package
boundary without becoming part of that package's API, which is what these are
for.

Three rules the layout rests on:

- **A shared helper belongs below everything that shares it.** Most of what made
  the flat layout tangled was general-purpose code filed under whichever caller
  needed it first — geometry rebuilds and Raw↔runtime conversion under
  `editor_`, CPU maths inside the renderers that used it.
- **Where a lower layer genuinely needs something above it, it declares a
  function variable and the owner installs it.** `world.CurrentLevel`,
  `hooks.EditorActive` and `textureui.UVGuideForTexture` are the three; each
  defaults to "not available", which is correct for a shipped game and for every
  test. `internal/flang`'s `Host` is the same inversion at full size.
- **Data lives with the data, effects stay up.** `internal/texdoc` holds the
  texture document and `internal/texture` the file format and GPU upload;
  `internal/world` holds the level and `internal/levelio` the YAML.

## Scripting language

Levels are scripted in **flang** — see SCRIPTING.md for the language itself. What
matters at this level of the design is where it sits.

The language lives in `internal/flang` and knows nothing about Foundry: an object
is an opaque handle, a value is one of eleven flang types, and everything it can
do to a world goes through one `Host` interface. That is what lets the whole
language — lexer, parser, dispatcher, interpreter, standard library — be built
and tested with no window, no GL context and no level.

The engine side is the other half of that interface. A handle is a *path of
stable names* rather than a pointer or an index, because the level is rebuilt
wholesale by undo and by a reload: `Level.Entities` is a slice of values, so any
pointer dies on the next append, and one Ctrl+Z invalidates every index in the
world at once. Names survive all of it. Making them survive is why chunks,
sectors, edges, lights and volumes all now carry IDs that no edit can shift.

Scripts run once per frame during play, immediately after the world has settled,
so an event describes what happened rather than what was about to. Messages are
queued and drained to a fixpoint within the frame — a `hit → damage → die →
open_door` chain resolves in one frame rather than three variable-length ones —
with four independent limits so a bad script cannot hang the game.

Play never changes the authored level. Entering play snapshots it and leaving
restores it, gated on a script having actually written something, so a session
that changed nothing costs nothing to leave.

## Volumes

A **volume** is a region of space a script can react to: a shape, an ID, and a
script. It is never drawn outside the editor, never collides, and never moves on
its own. All it does is answer "is this point inside", which is what `enter`,
`exit` and `interact` resolve through.

A model-less entity could have served, and nearly did — it already has an ID,
tags, attributes and a bounding volume. But an entity also carries velocity,
gravity, four collision flags, model attachments and a definition it came from,
none of which a trigger has any use for. "Make a box entity, then turn off three
flags and give it no models" is a rule to remember rather than a thing to place,
and it puts triggers in the same list as the crates. A volume is the smaller
idea, so it gets the smaller type.

Volumes sit at the level rather than in a chunk, for the same reason entities do:
a trigger can span a doorway between two of them.

In the editor a volume is a wireframe cage that can be clicked in the 3D
viewport, which gives it the same translate, rotate and scale gizmos an entity
gets — the scale one dragging its half-extents, since a region's size *is* its
extents and it has no scale factor of its own. The click tests the ray against
the real solid rather than the box around it, so a sphere and a cylinder are
picked where they look picked. A volume the camera is standing inside is skipped
by the picker on purpose: a room-sized trigger would otherwise sit in front of
every surface in the room it wraps and swallow every click meant for one, and
the panel's list is still there to select it from.

## Animations

Models carry their own armature and their own clips: a `.fmdl` holds a bone
hierarchy, per-vertex skin weights, and any number of named animations, so one
file is the whole thing. Nothing is shared between models, which costs some
duplication and buys never having a rig and a mesh drift apart.

A pose is one local transform per bone — translation, rotation, scale, in that
bone's own rest space — composed down the hierarchy, so rotating a shoulder
carries the elbow with it. Keyframes store those channels rather than posed bone
placements: rotations interpolate as rotations (slerp), where interpolating
endpoints would make a swinging limb visibly shorten between keys. Scale along a
bone's own axis stretches both what it deforms and where its children sit.

Clips are authored in the model editor's **pose mode**, which deforms the mesh
for display without editing the document underneath it — the rest shape is what
gets saved, whatever the viewport is showing. A timeline keys the bones that have
actually been moved, so a clip only ever mentions the parts of the rig it drives.

Deformation is linear blend skinning, capped at four influences per vertex. The
editor runs it on the CPU, because its wireframe, its picking and its gizmos all
want ordinary vertex positions to work from.

The renderer skins on the GPU — `Geometry` carries the bone attributes, and the
colour pass and both shadow passes read one shared bone-matrix palette, so a
moving model and its shadow cannot disagree. That path is fed by entity model
attachments: each one owns a palette, rewrites it in place each frame, and hands
every submesh a slice header onto it once at load.

An attachment's *animations* are a naming layer over a model's clips. A clip is
what a `.fmdl` calls a motion; an animation is what the game calls it, plus a
speed, an optional loop override and an autoplay flag. The indirection is the
point: `walk` can be `walk_cycle_02` on one model and `locomotion.walk` on
another, and a script says `walk` either way.
