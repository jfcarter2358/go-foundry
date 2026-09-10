# Entity definitions

A `.yaml` file here describes one *type* of entity: its models, its bounds, its
collision flags, its default attributes and tags, and the script that drives it.
A level then places instances of it, storing only where each one is and whatever
it overrides — see `entity_def.go`.

One type ships — `dropship.yaml`, described below — and nothing else has to.
The editor's Entities panel places a **blank entity**: a box with an ID, no
model, and defaults filled in on the spot, written out in full in the level
file. That is the right shape for a thing one level needs once.

Write a definition when a *second* level wants the same thing, or when "every
barrel gains 10 health" should be one edit rather than a search across every
level that placed one. Definitions found here show up in the panel's Type
picker, under the blank entry.

## What a definition holds

Beyond the models, bounds, collision flags, tags, attributes and script, a type
may carry **brushes** — its own solid geometry, in its own coordinates, written
in the same format a level's brushes are:

```yaml
id: shuttle
brushes:
  - id: deck
    verts: [...]
    faces: [...]
  - id: door
    dynamic: true      # a script may move this one
    verts: [...]
    faces: [...]
vehicle:
  mode: air
  seats:
    - {name: pilot, driver: true, position: {x: 0, y: 0.2, z: 0}}
```

Nobody types that by hand: the entity editor's brush tools write it, and they
are the level editor's own tools — stamps, extrude, inset, subdivide, object and
edit mode, the gizmos, the face panel. See `internal/world/entity_brush.go`.

Placing one of these materialises a chunk called `<entity id>.geometry`, bound
to that entity's frame, so the geometry is carried around by it — solid, drawn,
and walkable while it moves. The level file stores the placement and never the
geometry: the definition is authoritative, and a fix to the hull reaches every
ship.

Three things follow from the brushes being the *entity's* rather than a chunk
file's:

- **A brush is addressed per placement.** The materialised IDs are
  `<entity id>.<brush id>`, so `@shuttle_2.door` names exactly one solid however
  many shuttles a level has placed.
- **A script writes it in the entity's own space.** `position`, `rotation` and
  `velocity` on a brush an entity carries are that entity's coordinates, not the
  world's — a door closing means "two metres to port", which stays true while
  the ship turns, and the ship's own motion carries the door on top of it.
- **Walking in is walking in.** A body that gets inside an entity's brushes is
  handed into its frame by containment, with no doorway volume authored
  anywhere, and is carried when the entity moves. From outside the hull is
  solid, which is what makes the only way in the way you built.

A type may also carry **emitters** — particle effects in its own coordinates,
carried around by it the same way:

```yaml
emitters:
  - name: thruster
    effect: plume          # particles/plume.fpar
    offset: {x: 0, y: 0.4, z: -6.2}
    rotation: {x: 3.14159, y: 0, z: 0}   # aim the cone aft
    tags: [engine]
  - name: torch
    effect: flame
    model: hull            # ride a bone of one of the type's attachments
    bone: brazier
    emitting: false        # the script switches it on
```

What the effect *is* still lives in its `.fpar` and is still edited in the
particle editor; this says where one hangs and carries only the knobs worth
varying per type — `enabled`, `emitting`, `rate_scale`, `tint` and its tags.
Retuning a plume is one document rather than every ship that has one.

Placing one of these puts a real emitter in the level for each, already attached
to the entity, addressed as `<entity id>.<emitter name>` — so
`@shuttle_2.thruster` names exactly one plume and every particle verb works on
it unchanged. With `model` and `bone` set it rides that bone's *posed*
placement, so a flame in a hand swings with the hand. As with the brushes, the
level file stores the placement and never the emitters. See
`internal/world/entity_emitter.go`.

## What ships

`dropship.yaml` is an assault shuttle with a hold you walk into and a powered
blast door across the hatch: two model attachments, six brushes for the inside,
a script, and params the level binds.

It is worth reading as the worked example of all three rules above. The hold is
five static brushes and one marked `dynamic` — the blast door — authored *shut*,
because authored geometry is always a brush's rest state and nothing on disk
records where a script left it. `scripts/dropship.flang` slides it clear on
spawn and shuts it before the drop, writing `position` in the ship's own
coordinates, which is what keeps the door where it was put while the ship is two
hundred metres into a fall.

There is no portal to line up and no doorway volume to place: park the ship and
walk in through the aft opening.

> **TODO(brush migration, Phase 10):** every shipped level was deleted in the
> sector→brush cutover, so there is currently no level placing the dropship. The
> type and its script are current.

## The editor

The **Entity** button on the level editor's toolbar opens the entity editor: a
window with a 3D view of the type being built, a list of what it is made of
(models, brushes, emitters, seats) on the left, and the type's properties on the right.
Saving there re-stamps every instance of that type in the level open beside it.

The brush half of it is the 3D view's, not a second implementation: **Tab**
swaps object and edit mode, **1/2/3** pick points, edges or faces, **G/R/C** are
move, rotate and scale, **E/I/B** extrude, inset and subdivide, and the stamp
buttons in the outline arm a ghost that follows the cursor until Escape.
**Import** copies brushes in from a chunk under `chunks/` or from the level open
in the main window — a copy, not a link: the type owns its geometry from then
on.

Two ways to get there from a level: **Edit Type** beside a placed entity's type,
and **Make Type** beside a blank one — which seeds a new definition from
something already placed, and is the promotion this file describes.

