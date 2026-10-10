# Prefabs

A prefab is a piece of a level you built once and want twenty of: a street lamp
that is a post, a light and a trigger; a guard tower that is six brushes and a
ladder volume; a modular corridor section.

What makes it a prefab rather than a copy-paste is the **link**. A level stores
the *placement*, not the content — so editing the prefab changes every placement
of it, the way editing an `.fmdl` changes every prop that names it.

## Making one

1. Build the thing in the level, out of ordinary brushes.
2. Select every brush that belongs to it (ctrl+click adds one at a time, or use
   the face menu's "Select this brush's faces").
3. Right-click → **Save selection as prefab...**, and name it.

It is written to `prefabs/<name>.yaml`.

The prefab's own origin is the **bottom centre** of what you selected, not its
middle. That is so a lamp sits on the ground wherever it is dropped rather than
half-buried — and it means the height you build at does not matter.

## Placing one

**Add... → Prefab → `<name>`**, then click in the view. The instance lands where
the ray met the world, snapped to the grid if snapping is on.

Each placement gets an ID. Everything it makes is named after it: the third
lamp's light is `lamp3_bulb`, addressable from a script like anything else.

## Editing one

Select the instance — its row in the Explorer, or its outlined box in the view —
and the Properties panel gives you its file, position, rotation and scale.
Moving it re-stamps it, so what you see is always what the file says.

To change what a prefab *is*, edit it in a level and save it under the same
name. Every placement in the open level updates when you do.

**What an instance made is not editable in place.** Click the lamp's post and
you select that brush, but the change belongs in the file or it is not a prefab
edit — and it would be thrown away by the next re-stamp anyway.

## What a prefab can hold

Brushes, entities, volumes and point lights.

Not sprites, emitters, scenery or sounds — not because they would be hard, but
because none of them is *structural*, and a prefab is for the pieces a level is
built out of. Scatter props with the [scenery brush](14-scenery-brush.md), which
is the tool for placing forty of something that has no other content attached.

Scale reaches everything with a size: half-scaling a lamp halves its trigger's
extents and its light's radius as well as its geometry. Position and rotation
are carried through the instance's own transform.

## Prefabs versus chunks

A **chunk** is a streaming and frame-ownership container: what a script switches
off to unload a region, and what a vehicle owns to have an interior. That is a
property of *where* content is.

A **prefab** is a property of *what* content is. The two are independent, and a
level wants both — twenty lamp prefabs spread across four streaming chunks. A
prefab's brushes do become a chunk when stamped, one per instance, but that
chunk is the instance's, not the author's.

## In the level file

```yaml
prefabs:
  - id: lamp3
    file: street_lamp
    position: {x: 40, y: 0, z: -12}
    rotation: 1.5708      # radians about Y
    scale: 1
```

And nothing else — no chunks, no volumes, no lights. If you see the prefab's
content written into a level file, something has gone wrong with the link.
