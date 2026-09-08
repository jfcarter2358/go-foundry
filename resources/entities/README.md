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
may carry **chunk attachments** — world geometry, named by a file under
`chunks/`, placed in the entity's own space:

```yaml
id: shuttle
chunks:
  - name: hull
    file: shuttle_hull
    offset: {x: 0, y: 0.5, z: 0}
vehicle:
  mode: air
  interiors: [hull]
  seats:
    - {name: pilot, driver: true, position: {x: 0, y: 0.2, z: 0}}
```

Placing one of these materialises a chunk called `<entity id>.hull`, bound to
that entity's frame, so the geometry is carried around by it — solid, drawn, and
walkable while it moves. The level file stores the placement and never the
geometry: the definition is authoritative, and a fix to the hull reaches every
ship. See `internal/world/entity_chunk.go`.

## What ships

`dropship.yaml` is an assault shuttle with a hold you walk into and a powered
blast door across the hatch: two model attachments, a chunk attachment for the
inside, a script, and params the level binds.

The way *in* to a moving thing is the part with a rule that is not obvious. The
doorway is a **volume** riding the ship, ticked as a frame doorway (see
[Vehicles and frames](../../docs/guide/12-vehicles-and-frames.md)), and standing
in it is being aboard. It has to be deeper than one step of whatever walks
through it, because that is the whole of what decides which side you are on.

> **TODO(brush migration, Phase 10):** every shipped level and chunk was deleted
> in the sector→brush cutover, so the worked example this section used to point
> at — `resources/levels/dropship_demo.yaml` and `chunks/dropship_hold.yaml` —
> does not currently exist. The dropship *type* above is intact; what is missing
> is a level placing it.

## The editor

The **Entity** button on the level editor's toolbar opens the entity editor: a
window with a 3D view of the type being built, a list of what it is made of
(models, chunks, seats) on the left, and the type's properties on the right.
Saving there re-stamps every instance of that type in the level open beside it.

Two ways to get there from a level: **Edit Type** beside a placed entity's type,
and **Make Type** beside a blank one — which seeds a new definition from
something already placed, and is the promotion this file describes.

