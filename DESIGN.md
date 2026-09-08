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

### Thickness

A floor or a ceiling is a plane by default, and from the room below a mezzanine
that reads as a sheet of nothing. Give one a **thickness** and it becomes a
slab: a second face offset away from the room — down from a floor, up from a
ceiling — plus a rim round the perimeter joining the two. The far face is a
selectable face in its own right, with its own texture, blend slots, UV and
double-sidedness, because what you see from underneath a floor is rarely what
you want to walk on.

By default the far face is built from the *same* surface mesh and the same
openings as the surface it hangs off, through a frame whose heights are shifted
by the thickness — a constant offset of the shape above it. Carving a pit into a
floor dips its underside with it, and cutting a stairwell through takes the hole
all the way down. That is what nearly every slab wants, and it costs the level
file nothing.

It is a default rather than a rule, because a slab whose two faces must stay
parallel cannot be a vaulted ceiling under a flat roof, or a floor with joists
beneath it. Shaping the far face gives it a mesh of its own and detaches the
two; a button in its panel empties that mesh and puts it back to following.
Shaping *starts* from whatever the mirror was showing rather than from a flat
ring, so a carved floor's underside begins with the carve — the two frames share
a parameter space, so the surface's mesh transfers verbatim. The perimeter stays
pinned either way: boundary points belong to the sector outline, so the rim
always meets both faces at their corners however the interiors are shaped.

Two consequences worth writing down. The offset frame keeps the surface's own
winding, so `SyncSurfaceBoundary` — which rewrites boundary points on every
rebuild — cannot renumber the corners of the mesh the two share; the far face is
turned to look outward by reversing its triangles instead. And the rim skips
**portal** edges, because across a portal the slab is not exposed: it continues
into the sector next door, whose own rim would land in the same place and
z-fight with it. Where the two floors are at different heights, the riser the
portal already draws is what fills the step.

Alpha and smoothing are the surface's rather than the slab's: it is the same
piece of material, and a half-transparent floor over an opaque underside is not
a solid that could exist. Thickness 0 is the default, builds nothing and writes
nothing, so every level that predates it is unchanged.

**A slab is solid.** Before it, what stopped a body walking under a mezzanine
was the *ceiling of the room down there*, so a mezzanine over an open hall had
nothing underneath it at all and a body could rise straight through the floor
above. A slab's underside is now a hard limit in its own right, folded into the
one query every clamp in `internal/collision` already went through
(`sectorCeilingHeightAt`) so the player, placed entities, particles, portal
entry and step entry all get it from one place. It is asked relative to the
*floor of the sector doing the asking* rather than to the body's own Y: a slab
above that floor is overhead for anyone in the sector whatever they are doing
vertically, and a stateless question keeps a head-bonk from depending on which
frame it lands in. A surface's openings cut the whole solid, so a stairwell
stays climbable. The consequence worth stating: giving a mezzanine thickness
means you can no longer jump up through it, which is the point, and is opt-in
per surface.

Ceiling slabs have no collision of their own. A ceiling's underside *is* the
ceiling, already the head-stop it always was, and the top of a ceiling slab can
only be reached from a sector above it — whose own floor is what a body there
stands on.

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
- A frame's transform **ignores its owner's scale**, but the geometry it
  carries does not. A frame is a place to stand, not a model: putting the scale
  in the frame would stretch everything stored in its coordinates — a rider's
  position, and the rider's own drawn model with it — while the bounding volumes
  the resolvers work in are not in the frame's units at all. So the owner's
  scale is published onto each owned chunk instead (`Chunk.FrameScale`), where
  it scales that chunk's own geometry inside the frame. Scale a ship 2x and its
  hold is twice as big, drawn and collided alike, while the player who walks
  into it stays their own size. It is a single uniform factor — the mean of the
  entity's three axis magnitudes — because a chunk's scale has only ever been
  uniform, and a walkable slope, the 2D map's projection and a click radius all
  depend on that.
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

**The two halves must overlap.** A crossing fires on *proximity* — an occupant
within `portalCrossingBand` (0.6) of the line is handed over, rather than being
tracked across it — so the far side has to be somewhere they can stand the
moment they arrive. Two portals authored in the same place therefore do not
work: crossing at 0.6 short of the line puts the walker 0.6 outside the sector
they were entering, in no sector at all. What works is a ship's threshold
reaching *past* the deck's portal line and the deck reaching past the ship's, by
more than the band each — the ship's boarding tunnel lying on the hangar floor,
with the two lines a couple of metres apart and both floors present in between.
The band is a radius rather than a slab, so the same applies across the doorway:
an occupant off the *end* of the segment crosses too, and the far sector has to
be wider than the segment by the band. `resources/chunks/dropship_hold.yaml` and
`resources/levels/dropship_demo.yaml` are the worked example.

**A doorway nothing was authored to meet is resolved by containment.** All of
the above describes a *dock*: both halves written into the level, the ship
parked where the level expected it. That cannot describe a ship that sets down
wherever the pilot puts it, because the level would have had to guess the spot.
So the moving side's half stands on its own. A **doorway sector** — one carrying
a portal that names another frame — is the boundary, and which side of it an
occupant is on is settled by where they are standing rather than by how close
they are to a line: standing inside a ship's doorway sector is being aboard that
ship, and being aboard while inside none of its sectors is having stepped off
it. Nothing is authored in the level at all; the ship's own `to_frame:` edge is
the whole declaration, and it works at any position and any yaw.

Containment rather than proximity, because proximity needs two lines. It fires
while the occupant is still short of the one they are crossing, which is only
safe when the far side reaches past it — and a single shared boundary has
nothing to reach past, so a band around it claims the same ground from both
sides. Containment cannot fail that way: inside a sector and inside no sector
cannot both be true, so the two directions are antisymmetric by construction.
What it gives up is tunnelling protection, since there is no "before the line"
to hand anyone over at. A doorway sector therefore has to be deeper than one
step of whatever walks through it — the shipped vestibule is 3.3m against a walk
of a few centimetres a tick — and a projectile-speed body would pass straight
through. A dock, which has both halves, still gets the proximity rule.

A frame with no doorway at all is **sealed** in a second sense: it keeps its
occupants however far they wander off its floors, so a hold with a gap in the
deck does not quietly empty itself into the world. And a seat is exempt from the
stepped-off rule entirely — a seat is a constraint rather than a place, and one
authored outside the interior's own sectors (a cockpit on a nose) would
otherwise read as its occupant having walked off the deck on every tick.

**A crossing has to go deeper than the way back.** Overlap the two halves by
*less* than the band and both sides claim the same ground: the occupant is within
the band of the line they are leaving and, once carried over, within the band of
the line that leads straight back, so the two spaces trade them across once a
tick. The movement looks fine — what oscillates is `CurrentChunk`, which is where
portal-visibility culling starts its search, so each interior is culled on
alternate frames and the screen strobes between the two. A crossing therefore
only fires when it leaves the occupant *farther* from the return line than they
currently are from the line they are crossing. The test is antisymmetric, so the
reverse crossing cannot also pass; geometry that follows the overlap rule above
satisfies it on every crossing, and geometry that does not holds at the doorway
until a step carries the occupant far enough in to settle.

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

**The interact key is a press, not a state.** `E` is recorded on its down edge
and read once, and the vehicle half (`VehicleInteractRequest`) takes it from
there rather than testing the key every tick. That is not tidiness: the
precedence in `VehicleInteract` says a seated occupant with somewhere to stand
gets up and one aboard-and-standing with a seat in reach sits down, so running
it per frame simply alternates between the two. Nothing about the occupant
moves, because sitting and standing both put them at the seat — it shows up only
in the view, where a seated eye sits at the seat's own `eye` and a standing one
at the engine's eye height, so the camera hops between the two every frame. A
press the vehicle half had no use for is handed back rather than swallowed, so
`E` still reaches a scripted `:interact:` when there is no seat within reach.

**The camera is placed last, after `RefreshFrames`.** A rider stores its position
in coordinates that move, so where the eye goes depends on which transform those
coordinates are current for — and the room around it is drawn through exactly
that transform. The frame loop's last movers run after the player has moved (the
entity pass, and a vehicle's own world collision), and `RefreshFrames` is what
publishes their result, so `PlacePlayerCamera` is called after it and nowhere
else. Placed any earlier, the eye is computed from where the ship *was* while its
interior draws from where it *is*: the two disagree by however far the ship
travelled that tick, every tick, which reads on screen as the interior strobing
against whatever is outside it rather than as anything moving.

## Models

Models and their animations are authored in the **model editor**, which reads and
writes `.fmdl` documents in `models/`. An entity definition is what places one in
a world: an attachment names a model file, and the animations it declares name the
clips inside that file.

## Sprites and particles

A **sprite** is one camera-facing textured quad placed in a level. A **particle
emitter** is a placed instance of a `.fpar` effect, which simulates a pool of
quads. They are one feature because at draw time they are the same thing — a
textured, tinted, rolled quad turned to face the viewer — differing only in
where the transform comes from: authored, or simulated.

Both are level-scoped, beside entities and volumes, for the reason entities are:
one attached to a moving carrier crosses chunks, and its authored position is
world-space unless its `attach:` says otherwise. Both embed `ScriptData`, so
`@id`, `#tag`, attributes and an attached script reach them on the same terms
they reach everything else.

### Blend mode picks the pass

This is the part that is not merely a setting. A sprite's blend mode decides
*which pass it draws in*, not only how it is sorted:

- **cutout** draws inside the opaque pass, while the alpha-to-coverage that pass
  already runs is still on. It writes depth, needs no sorting, and gets its
  silhouette antialiased by MSAA for free. The right default for foliage and
  anything solid-looking.
- **alpha** draws after the world's own transparent pass, sorted back to front,
  with no depth write.
- **additive** draws last. It is order-independent, so it needs no sorting at
  all — which is the real reason fire and sparks should use it.

The existing transparent queue could not absorb this: it sorts one `Geometry` at
a time, and a particle batch is thousands of quads under one draw.

### The GPU path

One static unit quad, one streamed per-instance buffer, one
`glDrawArraysInstanced` per batch — where a batch is a (texture, blend,
billboard, lit) tuple. **This is the engine's first use of per-instance vertex
attributes.** Every other instanced draw here (biome scatter, detail scatter)
derives placement procedurally from `gl_InstanceID`, which is a placement trick
and cannot carry simulated state.

`sprite_common.glsl` holds the billboard basis and is injected into all three
vertex stages — colour, cascade depth, cubemap depth — so a quad casts the
shadow of the quad you can see. That rule bites harder here than for grass,
because a billboard's orientation depends on where it is being looked at *from*:
in the depth passes the quad faces the **light**, not the camera, or it would be
edge-on and cast nothing.

### Why the simulation is on the CPU

The engine targets GL 4.1 core: no compute shaders, no SSBOs. The alternative
was transform feedback, which puts particle state somewhere neither collision,
nor a script, nor a debugger can read it. Simulating on the CPU costs a per-frame
walk and a buffer upload, and buys particles that can bounce off a floor and be
asked where they are. At the pool sizes an effect actually uses, that walk is not
the frame's problem; the fill rate of the quads is.

Each layer's pool is preallocated and never grows: dead particles are
swap-removed and emission stalls at the cap. A particle budget only means
something if it is a ceiling rather than a suggestion.

Collision reaches the world through a function variable
(`particle.Collide`) that `internal/app` installs, because `internal/particle`
sits below `internal/collision` — the same inversion `world.CurrentLevel` and
`hooks.EditorActive` use. Nil means "no collision", which is correct for a
headless test and for the particle editor before it swaps in its own ground
plane.

### The effect document

One `.fpar` is one *effect*, made of layers — `torch.fpar` is flame plus smoke
plus sparks, because that is the unit an author places and a script names. A
layer carries its emission, its shape, its motion, its appearance, its collision
and three ramps over a particle's normalised life (size, alpha, colour). Effects
are authored in the **particle editor**, a pop-out window with a live preview
that drives the same simulation and the same draw path the game does.

### Attachment

A sprite, an emitter or a sound can ride a frame (a vehicle), an entity, or a named bone
inside one of that entity's model attachments. One `Attach` struct serves all
three, resolved cheapest-first to a world matrix once a frame. Bone attachment
reads the bone palette `EntityModel` already maintains for GPU skinning, so a
flame on a swinging weapon, the weapon, and both their shadows cannot disagree.

## Audio

Two problems that look like one. **Music** has no position: it plays at the same
level wherever the listener stands, and switching track is a crossfade. **A
positional sound** is somewhere — a torch, an engine, an NPC talking — and gets
quieter and pans as the listener moves and turns. The engine treats them as the
same voice on different buses, which is why there is one mixer rather than two
subsystems.

**The split is the particle split.** `internal/audio` owns clips, the voice pool
and the mixer, and has no idea what a level is; `world.Sound` is the placement
that says where, and carries only the knobs worth varying between two placements
of the same recording. Everything else about how a sound behaves is a property
of the file, not of the level entry — the same rule that keeps a `.fpar` out of
the emitter that names it.

`internal/audio`'s entry in the layering table is one name stricter than
`internal/particle`'s: it must not reach `internal/gfx` either. The listener is
four plain vectors that `internal/app` copies out of `gfx.Cam` once a frame, and
keeping the camera out is what makes the spatial maths a pure function a test
can call with no GL context and no window.

### Why the mixer is written in Go

SDL2 is already linked and `sdl.Init(sdl.INIT_EVERYTHING)` already starts the
audio subsystem, so output costs no new native library — which matters, because
the Windows cross-build copies its DLLs by hand. SDL_mixer would have added
`SDL2_mixer.dll` plus a codec DLL each, and its positional support is a coarse
angle-and-distance pan rather than a per-source model. OpenAL Soft would have
given real HRTF at the cost of another cgo dependency, another DLL, and a mix
that cannot be tested.

So the engine opens a device with `sdl.OpenAudioDevice` and pushes blocks with
`sdl.QueueAudio`, and does the mixing itself. go-sdl2 declares
`AudioSpec.Callback` as a raw C function pointer with no Go wrapper, so a
callback-driven device would have needed an `//export`ed cgo shim and would run
the whole mixer on a thread the Go scheduler does not own; pushing costs a
little latency and buys ordinary Go code. Decoding is pure Go for the same
reason — WAV is parsed here rather than through `sdl.LoadWAV`, which wraps
SDL-allocated memory in a hand-built slice header and never frees it.

The payoff is that the parts that can be wrong are testable. Distance
attenuation, the constant-power pan, cones, doppler, voice stealing and the
declick ramp are all ordinary functions asserted against ordinary numbers, with
no device attached. Only one test in the engine opens real hardware, and it is
opt-in behind `FOUNDRY_AUDIO_TEST=1`.

### The listener, and one sign convention

The listener is `gfx.Cam` — position, direction and `gfx.CamUp()`. The editor's
fly camera is the same camera, which is what makes auditioning a placed sound
work at all.

The one trap is handedness. The engine's right vector is written inline in
`gfx.MoveCamera` as `{Dir.Z, 0, -Dir.X}`, and for a direction from
`DirectionFromAngles` that is `Up x Forward`, not `Forward x Up` — the engine is
Y-up but left-handed about it. Taking the textbook cross product puts every
sound in the game on the wrong side of the listener, reads as correct on the
page, and is caught by exactly one test.

### Two threads

The mixer runs on its own goroutine, so a long frame produces slightly stale
positions rather than a gap. The game thread never blocks on it. Discrete events
— start this, stop that — go down a buffered channel with a non-blocking send,
where a full channel drops the command and counts it rather than stalling the
render loop. Continuous state — where everything is this frame, where the
listener is — is published as one immutable snapshot through an atomic pointer,
so the newest always wins and a burst of one-shots cannot leave a stale listener
queued behind it.

### The voice pool

Fixed at 32. When it is full a new sound steals the quietest voice of equal or
lower priority, and is **dropped** if there is none: cutting off the sound the
player is listening to in order to start a quieter one is a worse outcome than
losing the new one, and it is what makes a busy scene sound broken rather than
merely busy. Sustained voices are capped separately, so a script bug that starts
a loop every frame cannot eat the pool.

Every stop ramps over a few milliseconds rather than cutting. A waveform cut at
a non-zero sample is a step, and a step is a click.

## Level Composition

Levels are composed of chunks. These chunks can be thought of as floors of a house. They are ultimately "one level" each (though stairs and ramps can be included, you just cannot have a sector that goes over top of another). Chunks can then be combined together to create the larger level, so you might stack the "second floor" chunk on top of the "first floor" chunk to create a two-story house

Chunks are defined by their floor and ceiling sector maps, which are a 2d map defining the bottom (or top) sector definitions which consist of convex polygons connected. Where they connect can be a wall or a portal, portals are empty walls with no collision that serve to connect sectors. The height of the sector maps' points can be changed as well as their positions in the horizontal plane can be offset to create things like sloped walls

Sectors have textures and their positions are baked into an optimized BSP-tree for collision checks

### A chunk that moves

A chunk carries a `Velocity` in world units per second, and `world.AdvanceChunks`
adds it to the chunk's `Origin` once per tick of play — a lift that rises, a
barge that drifts, a blast wall that closes. Plain Euler integration with no
damping, the same deal an entity's own velocity gets: what should slow a
platform down is a rule the game has rather than one the engine invents.

Only the origin moves. Points, sector heights and biome paint are all
chunk-local and are carried into world space through `ModelMatrix`, so a moving
chunk rebuilds no geometry at all — the one thing that has to follow it is the
spatial hash, whose keys are world cells, and a chunk that moved is re-bucketed
at the top of the tick before anything resolves against it. Gameplay only, like
entity motion: a platform that slid away from where it was just placed would
make placing one impossible.

An entity does not ride a moving chunk just by standing on it — a body left to
collision alone slides out from under it exactly as before. An entity can be
pinned to one explicitly, though: `Entity.AttachedChunk` names a chunk, and
whenever that chunk's `Origin` moves — `AdvanceChunks` during play, or the
editor's own chunk-move paths — the entity's `Position` moves the same
distance (`world.CarryAttachedEntities`). It is a plain translation, not a
change of coordinate system the way a frame is: the entity's `Position` stays
in whatever space it already occupied, so the two have to agree on that space
(a world chunk carries a world-space entity, a frame-owned chunk carries an
entity riding that same frame) or the attachment silently does nothing.
Rotating or scaling the chunk leaves an attached entity exactly where it
stood, the same translation-only scope `Velocity` itself has. An interior
meant to be properly ridden — walked around inside, crossed in and out of
through a portal — still belongs to a vehicle (see `frame:`); this is for the
simpler case of something fixed to a moving platform. A disabled chunk does
not move either: it is out of the world entirely, and a streaming level
should not find its switched-off areas had wandered off before it switched
them back on.

### Switching a chunk off

A chunk carries an `Enabled` flag (stored as `Disabled`, so the zero value is
"on"), and it is the unit a level streams by. Disabled means three things at
once, and all three have to hold or the flag is a lie: nothing draws it, nothing
collides with it — it is left out of the spatial hash, which collision, entity
location and the script location verbs all resolve through — and its vertex
buffers are released. A level file may start a chunk disabled with `disabled:
true`, in which case the loader never builds it in the first place.

The chunk is the right unit because it is already every other system's unit:
sectors, biome paint and portal gaps belong to one, the renderer culls by one,
the hash buckets by one, and a script can already address one by name. So "load
the next area" is `@east_wing.prop.enabled = true` rather than a second, parallel
notion of a region.

The flag and the residency are deliberately two things. Writing the flag is
cheap and happens anywhere — a script handler, the editor panel, an undo —
while releasing and rebuilding buffers is `gl.*` work that has to happen on the
main thread between frames. `world.SyncChunkGeometry` reconciles the two once a
frame and rebuilds the level indexes when the set of chunks that exist has
moved. It is the same split `DrainScriptWork` exists for. See
`internal/world/chunk_enable.go`.

## Game Loop

The main game loop will run physics/movement calculations in compute shaders wherever possible to improve performance. Additionally it will host the script event loop once that is eventually implemented.

## Rendering

Rendering should cull as many chunks/sectors/walls as possible _before_ rendering. The goal is to make the engine as performant as possible. Biome scatter uses GPU instancing. So do sprites and particles, through a shared instanced billboard pass — the only one in the engine that uses per-instance vertex attributes rather than deriving placement from `gl_InstanceID`.

## Bump and normal maps

Relief belongs to the material, not to the surface wearing it. A texture
document carries a second layer stack — the *bump channel* — beside its colour
one, and a wall, floor, model or detail patch gets relief simply by using that
texture. Nothing in a level file mentions normal maps, and there is no
per-surface toggle: painting a height field into `brick.ftex` lights every
brick in the game, which is the whole reason to author it once.

### The artist paints a height, not a normal

The bump channel stores one *height* byte per texel, not the three channels of
a tangent-space normal. A brush stroke then means "this bit is higher", which
is a thing a person can draw; the three channels of a normal map are not, and
nothing in a 256-colour indexed painter could express them anyway. The normal
map is derived from the height field with a Sobel gradient at save time and
written out beside the albedo as `<name>_normal.png`.

Height bytes are not palette indices, which is why the two channels do not
share storage: a palette edit or a remap walks the colour layers only
(`TexDoc.EachLayer`), and cannot reach into a height field and quantise it into
nonsense. Zero is the one convention they do share — on a colour layer slot 0
is transparent, on a height layer 0 means "nothing here" and lets the stack
below show through, so a stroke on a new layer does not flatten everything
around it.

The gradient is taken with *wrapped* sampling coordinates. World textures tile,
so the texel past the right edge really is the texel at the left edge, and
clamping there would draw a ridge of false relief down every tile seam.

Bump strength is baked into the derived map rather than sent to the shader as a
uniform. The renderer then needs to know nothing about it, and every pass that
samples a normal map — the world program, the detail tessellation and scatter
programs, whatever comes next — gets the same relief without each one having to
look a per-texture value up per draw.

### No tangent attributes

Every mesh in the engine shares one five-float `[X, Y, Z, U, V]` vertex record.
Adding a tangent to it would mean touching every builder, every buffer and
every shadow pass — for something the fragment stage can recover exactly:
`dFdx`/`dFdy` of world position and UV *are* dp/du and dp/dv, which is what a
tangent frame is. `fragment.glsl` reconstructs the frame per fragment
(`cotangentFrame`). The cost is one frame per pixel instead of one per vertex;
the gain is that a normal map works on every surface in the engine the day it
lands, including the procedurally displaced ones that have no authored UVs to
hang a tangent off.

Shadow lookups keep shading off the *geometric* normal rather than the bumped
one. Their slope-scaled bias describes how far the geometry is tilted away from
the light; feeding it a per-texel normal makes the bias flicker texel to texel
and stipples acne across an otherwise clean surface. Water is skipped entirely
— it derives its normal from the wave field, and a tangent frame on a surface
that is displacing under the fragment would not describe it anyway.

### The name is the binding

A normal map is an ordinary entry in the texture registry under a derived name
(`<name>_normal`), so the upload queue, the animated-texture clock and live
editing all work on it unchanged — an animated texture gets an animated normal
map riding the same playback clock. `gfx.LoadTexture` is the single point every
path that puts a texture on the GPU goes through, so picking the companion up
there is what makes "the PNG is beside the albedo, so the surface has relief"
true everywhere without a caller having to remember. The same suffix is
filtered out of `ScanTextures`, so a normal map never appears as a texture in
its own right in the editor's pickers, and taught to the release packer, which
would otherwise prune every one of them as unreferenced.

Three passes deliberately do not sample one. Water derives its normal from the
wave field. Biome foliage already perturbs its normal from a procedural noise
field of its own (`biome_frag.glsl`), and the ground patch is deliberately
without any surface-detail term (`ground_frag.glsl`). Sprites and particles are
camera-facing billboards, where a tangent frame describes the screen rather
than a surface.

## Sky and fog

The two ends of "what is behind and between things". Both are level-scoped and
both are optional: a level with no `sky:` and no `fog:` block renders exactly as
it did before either existed, which is what let them be added without touching a
single existing level file.

**The sky** is one cube, drawn once, in `internal/render/sky.go`. What makes it
cheap is *where in the frame it sits* rather than anything about the geometry.
`sky_vert.glsl` writes `gl_Position = p.xyww`, forcing every vertex to depth 1.0,
and the pass runs **after** the whole opaque pass — so the engine's global
`GL_LEQUAL` depth test discards the sky everywhere the world already drew, and it
costs one fragment per *uncovered* pixel rather than one per pixel on screen. It
runs **before** the transparent pass, because translucent water and alpha sprites
have to blend against it.

It has two modes behind one uniform rather than two programs. `cubemap` is the
skybox proper: six textures, named individually in the level file (not derived
from a base name — a base name indexes nothing, and `cmd/foundrypack` finds the
textures a game reaches by scanning content for tokens). `gradient` is three
colours up the dome and no art at all, and it is the mode most levels want,
because "the horizon should stop being a flat grey wall" is a lighting decision
rather than an art request. A cubemap that will not load — a missing face, or the
half-filled state an author is in while picking them — falls back to the gradient
rather than to nothing.

Every field is also a scriptable `@level.prop.sky_*` property (see SCRIPTING.md),
and a script's `ease_sky(seconds)` can cross-fade between two complete sky
states — kind, art, colour, tint, rotation, all of it — rather than only
snapping between them. A colour lerps; a `kind` cannot, so the blend happens
one level down from there: `sky_frag.glsl` evaluates the sky twice, once for
the state a script is easing away from and once for the state it authored
afterward, and mixes the two finished colours (`skyLook`, called with each
state's own uniforms). `world.Sky` carries at most one such blend at a time —
a runtime-only `TransitionFrom` snapshot plus an elapsed/duration pair
`SkyUpdate` advances once a frame, the same way `AdvanceChunks` and
`EmitterUpdate` are what make time pass for theirs — so calling `ease_sky`
again before an earlier one finishes retargets it rather than queuing a second.

**Fog** is declared exactly once, in `resources/shaders/fog.glsl`, and spliced
into the `#version` line of every stage that shades: the world shader, the two
detail programs that reuse its fragment stage, the biome scatter and ground
patch, sprites, and the sky. This is the same trick `biome_light.glsl` plays with
the lighting uniforms, and for a sharper reason. Fog that disagreed between
passes would not read as a fog bug — it would read as the *passes* disagreeing
about where things are: a wall fading into the distance with the grass in front
of it staying crisp looks like broken terrain, not a missing `#include`. One
declaration, one `world.UploadFogTo`, five programs — and, since fog volumes were
added below, one splice that carried them everywhere too.

Two details in that file are not arbitrary. Distance is **radial** from the eye,
not the view-axis depth fixed-function fog used — planar depth makes the fog on a
stationary object change as the camera turns its head, which reads as the world
breathing. And an **additive** sprite is attenuated (`applyFogAdditive`) rather
than mixed toward the fog colour, because the framebuffer under it already holds
the fog; mixing would add the fog twice and make a distant spark brighter than a
near one.

The two features meet at the horizon. Fog carries a `sky_blend`, which is how
much of the fog colour the sky takes near the horizon and how tightly that is
confined to it. Without it, the ground fades to the fog while the sky behind it
stays crisp, and the join becomes the most conspicuous edge on screen — at
exactly the distance the fog was added to hide. For the same reason the colour
buffer is cleared per frame from the level rather than to a fixed slate blue
(`render.WorldClearColor`): with fog and no sky, geometry that faded into a
contrasting background would fade into a silhouette rather than into nothing.

**Fog volumes** are the other half of the idea: fog that has a position. The
level's `fog:` is the air of the whole level and has none, which is exactly why
it cannot do a cellar that is damp while the courtyard outside is clear, or a
ravine holding a bank the road above it does not. So a volume may carry a `fog:`
block of its own, and the region fills with it.

The term is not a distance. It is the **length of the view ray inside the
region**, run through the same exponential extinction — which is what makes a
bank behave like a body of fog rather than like a sticker: looking along its
length is thick and across it is thin, a wall past its far face is fogged by
exactly its depth, and walking in fills the screen because the segment now starts
at the eye. Every shape is solved in a normalized space where it is the unit box,
sphere or cylinder, so one intersection routine per shape covers every size and
orientation; the ray direction is carried through that same matrix *without*
being renormalized, which is what keeps the span in world units instead of in
half-extents.

It lives in `fog.glsl` beside the distance fog rather than in a pass of its own,
because `applyFog` is already the one entry point every shading stage calls — so
the banks reach the walls, the grass, the scattered rock, the sprites and the sky
without a single call site changing. `UploadFogTo` takes the level's volumes
alongside its fog for the same reason: two upload functions would be five places
to remember the second one, and a pass that had the distance fog and not the
volumes looks completely correct until an author places a bank and it stops at
the edge of the grass. The sky takes volumes at full strength rather than the
horizon share it takes of distance fog — a volume is bounded, so the ray leaves
it, and standing inside a bank under a crisp sky is the most obvious way for one
to stop reading as fog.

The shader carries eight, and a level with more drops the extras in level order,
the same truncation `MaxPointLights` does. A fog volume is still an ordinary
volume: it fires `enter` and `exit`, a script can thicken it or put it out, and
disabling the volume does both — a region that kept fogging after being switched
off would be half there.

## Collision Checking

The player carries an axis-aligned bounding box, and everything it is resolved against is level geometry. First it works out which world chunk it is in; once it knows that, the chunk's spatial index gives it the sector. If the floor carries a surface mesh, a 'top-down' check figures out which of its triangles the player is over and the collision is resolved against that triangle and any applicable edges. Walls, floors, ceilings, slopes and the openings cut through walls are each resolved in turn.

An entity with `collision.world` set goes through those same resolvers. It is
handed to them as a *proxy* placed at its bounding volume's centre with that
volume's scaled extents, because that is the shape they are written against —
an entity's own position is its base, and its bounds can be offset from it
besides. Whatever the resolvers do to the proxy is applied back as a delta.

How tall a riser the player walks up rather than being stopped by is the
level's to say — the `player:` block under Level format, resolved once per frame by
`MovePlayer` and enforced by `ResolveStepEntry`. It defaults to the body's own
height, which is what the engine used before it was configurable. The allowance
is measured from the higher of the floor being left and the body's own feet, so
raising or lowering it changes what walking reaches without sealing off ledges
that a jump clears.

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

The level's environment is two more optional top-level blocks. Both are omitted
entirely unless set, so no existing level file changed when they were added:

```yaml
sky:
  kind: cubemap           # gradient (default) | cubemap
  faces:                  # cubemap only; +X, -X, +Y, -Y, +Z, -Z, in GL's order
    [dusk_px, dusk_nx, dusk_py, dusk_ny, dusk_pz, dusk_nz]
  zenith:  {r: 0.25, g: 0.45, b: 0.85}   # gradient only, and the three stops
  horizon: {r: 0.72, g: 0.82, b: 0.92}   # are kept when the kind is switched
  ground:  {r: 0.18, g: 0.17, b: 0.16}
  tint: {r: 1, g: 1, b: 1}   # multiplies whichever mode produced the colour
  intensity: 1
  rotation: 45            # degrees about +Y; lines art up with the level's sun

fog:
  mode: linear            # linear (default) | exp | exp2
  disabled: false         # switched off but kept, the way a light is
  color: {r: 0.62, g: 0.68, b: 0.76}
  start: 40               # world units, every mode
  end: 400                # linear only
  density: 0.006          # exp/exp2 only
  max_opacity: 1          # below 1 leaves a landmark as a silhouette
  sky_blend: 0.7          # how much fog the sky takes at the horizon
  sky_falloff: 4
```

Both are edited in the Environment block of the editor's properties panel, where
the checkbox at the top of each group is what creates and destroys the block —
turning fog *off* is a different gesture from deleting it, and keeps the numbers.

The player is the one body in a level nothing authors — the engine builds it,
no `entities:` entry describes it, nothing saves it — so its tuning has nowhere
to live but a top-level block of its own, edited in the Player block of the same
panel:

```yaml
player:
  step_height: 0.5      # world units; the tallest riser walked up rather than
                        # stopped by. Omitted means the player's own height,
                        # which is what the engine did before the block existed.
```

Every key in it is optional and the block is omitted entirely unless set, so no
existing level file changed when it was added. A non-positive `step_height`
reads as the default rather than as "no stepping at all": a body that could not
step would be stopped by the seam between two floors that differ by a rounding
error.

A chunk may carry `disabled: true` to start switched off, and a volume may carry
`load_level:` to be the level exit. Both keys are omitted unless set, so no
existing level file changed when either was added:

```yaml
chunks:
  - id: east_wing
    disabled: true        # not built, not drawn, not solid, until a script asks
    # ...

  - id: lift
    velocity: {x: 0, y: 2, z: 0}   # world units/second, added to origin during play
    # ...

volumes:
  - id: cave_mouth
    position: {x: 40, y: 0, z: -12}
    half: {x: 2, y: 3, z: 1}
    load_level: caves     # the player walking in loads levels/caves.yaml

  - id: cellar_damp
    position: {x: 12, y: 1, z: -4}
    half: {x: 6, y: 2, z: 6}
    fog:                  # the region fills with fog; see Sky and fog
      color: {r: 0.55, g: 0.58, b: 0.6}
      density: 0.2        # per world unit travelled INSIDE the region
      max_opacity: 1
```


## User interface

There was none until recently: every pixel of UI in the engine was Dear ImGui,
which is an editor tool. A shipped game could not draw a main menu, a loading
screen or a health bar.

The runtime UI is native. A game build links no ImGui at all, and the same pass
that draws a HUD over the world draws the UI editor's canvas — so what an author
sees while authoring is what ships, because there is one implementation of it.

### The three packages, and why there are three

`internal/uifont` bakes a `.ttf` into a glyph atlas and lays text out against
it. Pure CPU, and below `internal/gfx` as well as everything above it, because
the layout solve *measures* text: a label that auto-sizes and a paragraph that
wraps both need metrics before a rect can be decided. Metrics are data, so they
sit with the data rather than arriving through a function variable.

`internal/ui` is the `.fui` document, the layout solve, hit testing, directional
navigation and the screen stack. No GL, no level, no ImGui. That is the same
split `internal/particle` makes and for the same reason: the editor's live
preview and the running game have to drive one solver, so it must sit below
both.

`internal/uidraw` is the screen-space pass. Above `gfx`, below `world` — every
entry point in `internal/render` takes a `*world.Level`, and a UI that needed a
level could not draw a main menu before one had loaded, nor the loading screen
covering that load.

### Anchors, not a layout engine

A widget is placed by four anchors in 0..1 of its parent's rect and four pixel
offsets inward from the rect those anchors describe. One representation for both
the pinned and the stretched case: Unity keeps `anchoredPosition`+`sizeDelta`
for one and `offsetMin`/`offsetMax` for the other, and most of the confusion
`RectTransform` is famous for comes from the two disagreeing. The spelling an
author wants for a pinned axis — a position and a size — is a derived accessor,
not a second stored field.

There is no flexbox and no constraint solver. The solve is one depth-first walk
with parents before children, because an anchor is measured against a parent
that is already solved. Two rules in it are invisible until they are wrong:
children anchor to their parent's *solved* rect rather than its clip rect, so a
list inside a partly-off-screen panel keeps its positions; and clip rects
intersect rather than replace, so a nested panel cannot draw outside its
grandparent.

The one control that makes anchors usable rather than terrifying is
`SetAnchorsPreservingRect`: dragging an anchor handle re-expresses *where a node
is* in terms of a different parent-relative origin, and does not move it. It has
a test over every pair of the twelve presets.

### One shader, no mode uniform

A solid fill samples a 1x1 white texture, and a glyph page is white RGB with
coverage in alpha. So a flat rectangle, an image and a letter all come out of
`Color * texture(Tex, UV)` — which means they also batch together whenever the
clip allows. A HUD is three to eight draw calls; a menu with one atlas and one
font is two.

The batching key is `(texture, scissor rect)`, which is exactly the two things
the shader cannot vary per vertex. The vertex is twenty bytes and the GL state
save-and-restore is cribbed from `imguibackend.ImguiRenderDrawData`, deliberately:
that renderer has already had every scissor and state-leak bug beaten into it.

Pixel snapping happens at draw time rather than in the solve, and snaps *edges*
rather than origin-and-size — rounding a position and a width independently
opens a one-pixel seam between abutting elements that appears and vanishes as a
window resizes. Keeping it out of the solve means hit testing, anchor drags and
the editor's numeric readouts all work on stable continuous numbers.

### There is no application-state enum

A HUD is a non-modal, non-pausing screen at the bottom of the stack. A pause
menu is a modal, pausing, cursor-releasing screen pushed on top of it. A loading
screen is the same thing pushed by the load job. One mechanism, three uses —
which is why "is the game paused" is a question about the stack rather than a
variable somebody has to remember to set.

Input reaches the stack as intents (pointer moved, navigate down, activate)
rather than as SDL events, so `internal/ui` never learns which device asked.
A gamepad is a translation layer in `internal/app` and no change at all in the
UI itself.

### Bindings are pulled, not pushed

A widget's document says `bind: "@player.attr.health"`, and the stack polls it
once a tick. Under a push model every place that changes health — the collision
resolver, a damage script, a pickup, the restore that runs when play ends — has
to remember to update the bar, and the one that forgets produces a HUD that is
wrong in a way no test catches. A pull expresses the relationship once, in the
document, where the editor can show it and where there is exactly one place for
it to be wrong.

The precedence is unambiguous and it has to be: a script writing to a widget the
document binds is *refused*, with a message naming the binding. Letting both
write would make the winner depend on frame timing, which is not something
anyone can debug.

### Video

A `video` widget shows a decoded frame the way an `image` widget shows a
texture. The decoding is `internal/video`, which sits where `internal/audio`
sits and under the same rule: no GL, no level, no window. A frame comes out as
an `image.RGBA` and getting it onto a texture is somebody else's job — which is
what lets a test push a four-minute clip through the player in a millisecond
with no context current.

Two formats, both read by the standard library, and no new native dependency:
an AVI carrying motion-JPEG frames and PCM audio, and an animated GIF. What is
deliberately absent is a modern inter-frame codec. H.264 or VP9 means either
cgo — which this engine has spent real effort not having — or a decoder that
would be a larger program than the renderer beside it. Motion-JPEG pays for
that in file size and nothing else: every frame is a keyframe, so seeking is a
division rather than a hunt backwards for the last I-frame, and the encode is
one line:

```
ffmpeg -i in.mp4 -c:v mjpeg -q:v 3 -c:a pcm_s16le out.avi
```

The `idx1` index at the end of an AVI is ignored even though that is what it is
for. Two generations of muxer wrote its offsets relative to two different bases
— the file, and the `movi` list — and a file written against the other
convention indexes to garbage. Walking `movi` costs one pass at open and cannot
be wrong.

**A widget describes; a player plays.** The `.fui` carries four fields — which
clip, autoplay, loop, muted, volume — and the playhead is not among them. A
document is cloned every time a screen is shown, and a clone carrying somebody
else's playhead would be a cutscene that started half way through the second
time it was watched. The player lives in a registry keyed by stack, screen and
widget, and it is closed by a generation sweep once a frame in which nobody
asked for it: a hidden screen, a deleted widget and a stack reset all reach it
by the same route. The join between the two is `ui.VideoDriver`, a function
variable `internal/uidraw` installs — the same inversion the binding resolver
uses, for the same reason.

The frame reaches the GPU through the queue the glyph pages use, under a name
starting `@video/` that no scanned asset can collide with, and only when the
player's revision has moved. That last part is not an optimisation detail: a
twelve-frames-a-second animation on a 144 Hz display would otherwise re-upload
eight megabytes a hundred and forty-four times a second, and a *paused* one
would too.

The soundtrack is demuxed into an `audio.Clip` and played on the music bus. Not
a second mixer: a cutscene whose audio ignored the master volume would be a bug
reported as "the settings do nothing". Seeking it needs no mixer support at all
— a `Clip` is immutable and its samples are never written, so playing from four
seconds in is a second `Clip` sharing the tail of the first.

The clock is accumulated wall-clock time rather than the audio device's cursor.
Making the soundtrack the master would be more correct in principle and is what
a media player does; it needs the mixer to publish a live sample position, and
it buys accuracy nothing here can perceive. `Advance` is fed real elapsed
seconds, so a frame hitch moves picture and sound by the same amount.

Two defaults are kind-specific because a video is not an image. Its background
is black and its fill mode is *fit*: a widget's rect matching its film's aspect
ratio is a coincidence, so there will be bars, and white ones read as a
rendering fault. For the same reason the picture is drawn untinted where an
image is tinted by `Style.Color` — one colour cannot be both the bars and a tint
on the picture between them, and bars is the half every clip needs. The node's
alpha still applies, so fading a video in and out works.

### The loading screen

`levelio.LoadJob` is a level load split into steps the frame loop pumps. Not a
goroutine — loading ends in `gl.*` calls, and the GL context belongs to the main
thread, the same constraint `DrainScriptWork` exists for. Not "draw one frame
then load" either, which gives exactly one static frame with a bar that never
moves.

It runs the same four functions `rawToLevel` does, in the same order. That is
the whole safety argument for having two drivers over one loader, and there is a
test asserting the stepped load builds an identical level to the synchronous
one.

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
internal/scriptdata   tags, attributes, script and params — the triple both a
                      level object and a UI widget carry
internal/flang        the scripting language (pure; see below)
internal/uifont       .ttf into a glyph atlas, and text layout against it
internal/gfx          Geometry and its draw path, the shader program and its
                      uniforms, the texture registry, GPU skinning, the camera
internal/gizmo        immediate-mode line/wireframe drawing
internal/imguibackend ImGui/SDL/GL plumbing, clipboard, shared widget idioms
internal/hooks        write-only buffers engine code drops script facts into
internal/texture      .ftex/.pal I/O, the global palette, the editing session
internal/model        the .fmdl document: meshes, bones, skinning, poses, UVs
internal/particle     the .fpar effect document and the CPU particle simulation
internal/ui           the .fui screen document, the anchor layout solve, hit
                      testing and the screen stack. No GL, no level.
internal/uidraw       the screen-space pass: batching, nine-slice, the UI shader
internal/audio        clips, the voice pool and the software mixer; no level,
                      no GL, and deliberately no camera either
internal/world        the level data model — chunks, sectors, walls, entities,
                      volumes, sprites, emitters, biomes, lights. One package
                      because the types are mutually recursive.
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

A volume may also carry a `LoadLevel`, which makes it the **level exit**: the
player walking in loads that level, through the same deferred request the
`load_level` verb records. It is the one thing enough volumes do that an
authored field beats making every author write the same three-line script, and
it is a field on the existing type rather than a fourth kind of object, because
everything else an exit needs — a shape to stand in, an `enabled` flag to lock
it behind, an ID a script can re-point it by — a volume already has. Only the
player takes one; there is no reading of "a player entered" that includes the
guard on patrol. A trigger that carries a script as well still gets its
`:enter:`, in the frame before the load starts.

A volume may also carry a `Fog`, which fills the region with fog — the one thing
a volume can be that the player sees. It is a field on the existing type for the
same reason `LoadLevel` is: a shape, a rotation, half-extents, an `enabled` flag
a script can flip, a cage to author it against and the gizmos to size it are all
things a volume already has, and a separate type would have been those six copied
so the seventh could be new. What it looks like is in the Sky and fog section
above; a fog volume still fires `enter` and `exit`, which is usually the event a
level wants when there is fog to be in.

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
