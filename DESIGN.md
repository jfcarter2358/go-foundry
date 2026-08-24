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
            description: The floor is a convex polygon created by following the edges of the sector along the bottom which can be optionally displayed and optionally checked for collision. Additionally, floors can have the `terrain` attribute set to `true` which means that they now have a web of triangles inside of them that can have their points manipulated to create varied terrain within the sector.
      - name: biome sector
        description: A polygon painted over a chunk's floors that clothes them — GPU-instanced scatter (grass, rocks, wildflowers, cattails, moss), a conforming textured ground patch, or, for the Water kind, an animated water surface filling the polygon. Biome sectors are decoration and never take part in collision.

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
settled back against the world. An overlap is pushed apart along whichever axis
it is shallowest on, which is what makes something dropped onto something else
settle on top rather than squirt out sideways.

Three details earn their keep, all of them about stacks settling exactly rather
than sagging a little further every frame:

- Volumes are compared as **boxes** whatever their `bounding` kind. The kinds
  refine `ContainsPoint`, which is the query scripts ask, not the push-apart.
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

The player carries an axis-aligned bounding box, and everything it is resolved against is level geometry. First it works out which world chunk it is in; once it knows that, the chunk's spatial index gives it the sector. If the sector has `terrain == true`, a 'top-down' check figures out which terrain triangle the player is over and the collision is resolved against that triangle and any applicable edges. Walls, floors, ceilings, slopes and wall extrusions are each resolved in turn.

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
