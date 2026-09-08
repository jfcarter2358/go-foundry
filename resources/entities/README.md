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
blast door across the hatch, and it is the worked example of everything above:
two model attachments, a chunk attachment for the inside, a script, and params
the level binds. `resources/levels/dropship_demo.yaml` places one and drops it.

It is worth reading before writing a type of your own with an interior, because
the way *in* to a moving thing is the part with a rule that is not obvious: the
doorway is a frame portal, its two halves live in two different spaces, and they
must not be in the same place. `chunks/dropship_hold.yaml` says why.

## The editor

The **Entity** button on the level editor's toolbar opens the entity editor: a
window with a 3D view of the type being built, a list of what it is made of
(models, chunks, seats) on the left, and the type's properties on the right.
Saving there re-stamps every instance of that type in the level open beside it.

Two ways to get there from a level: **Edit Type** beside a placed entity's type,
and **Make Type** beside a blank one — which seeds a new definition from
something already placed, and is the promotion this file describes.

