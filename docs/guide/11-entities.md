# Entities

Geometry is the shape of the world. An **entity** is a thing in it: a transform,
a bounding volume, and whatever you hang off that — models, tags, attributes, a
script, collision flags, a vehicle spec.

Entities are the only objects a level places, and apart from the player they are
the only things that move.

## Two ways to author one

**A blank entity** is the default: `Add... > Entity > (blank entity)`, then click
in the viewport. It gets an ID, a box for bounds, and stock defaults, and you
describe it from there in the properties panel. The level file carries all of it.
That is the right shape for a thing one level needs once.

**A definition** is a type: `resources/entities/<name>.yaml`. It holds what the
thing *is* — models, bounds, collision flags, default attributes, tags, script,
params — and a level places *instances* of it that store only where each one is.

Write a definition when a second level wants the same thing, or when "every
barrel gains 10 health" should be one edit rather than a search. Definitions show
up in the `Add... > Entity` menu above the blank entry.

An instance may override what it needs to. Tags union with the type's, attributes
merge over them by name, and model attachments match by name so an instance can
swap one attachment's mesh or texture. Saving writes only the difference, so a
level that has not overridden something keeps tracking the definition.

**Reset To Definition** on a placed entity throws its overrides away and keeps
only where it is.

## The entity editor

Toolbar: **Entity**. It edits a *type*, not a placement — a 3D view of the thing
being assembled, an outline of what it is made of down the left, the type's
properties down the right.

Reach it three ways: the toolbar button, **Edit Type** beside a placed entity, or
**Make Type** beside a blank one — which seeds a new definition from something
already placed.

Saving writes the file, drops the definition cache, and re-stamps every instance
of that type in the level open beside it, so the change is visible in both
windows at once.

| Key | |
| --- | --- |
| Middle- or right-drag | orbit (mouse-look in fly mode) |
| `Shift`+middle-drag | pan |
| `V` | orbit / fly |
| `WASD` `Q` `E` | fly (`Shift` faster) |
| `F` | frame the whole type |
| `G` / `B` | grid / bounds cage |
| `Space` | play / pause the attachment animations |
| `Delete` or `X` | remove the selected model, chunk or seat |
| `Ctrl+N` / `O` / `S`, `Ctrl+Shift+S` | new / open / save / save as |

## Bounds

The volume everything else tests against — collision, `:enter:`, hitscan.

| Field | |
| --- | --- |
| **Method** | `aabb`, `sphere` or `cylinder` |
| **Half X / Y / Z** | half the size on each axis |
| **Centre Offset X/Y/Z** | where the volume sits relative to the entity's origin |

The offset is what lets a model stand on its feet while its volume is centred at
hip height. **Fit To Contents** sizes the bounds to the attached models.

## Collision

Four flags, all authored on the type. The first three default **off**.

| Flag | |
| --- | --- |
| `world` | resolve against walls, floors, ceilings and slopes |
| `entities` | push apart from other entities that also set it, and from the player |
| `gravity` | fall, and stop when something is underneath |
| `movable` | whether a resolution may displace *this* entity (default **true**) |

Two things to know:

- **`entities` has to be set on both sides.** Solidity is mutual.
- **`movable: false` is immovable scenery** — it pushes others and is never
  pushed itself, and gravity does not touch it. A boulder is
  `entities: true, movable: false`.

Two engine-wide master switches sit over the top: `8` and `9` in play, and
checkboxes in the editor's Engine settings. A per-entity flag says what *wants*
to collide; the master says whether the engine is listening. If something that
should be solid is not, check those first — the panel says when they are off.

This is not a physics engine. There is no restitution, no friction and no angular
response. Overlapping bodies are pushed apart along the shortest way out, which
is what makes something dropped on something else settle on top rather than
squirt out sideways.

Entities only move while the editor is closed.

## Model attachments

Any number, each with its own transform relative to the entity.

| Field | |
| --- | --- |
| **Name** | how a script asks for an animation on *this* model rather than another |
| **Model** | `.fmdl` or `.obj` under `models/` |
| **Texture** | `(none)` falls back to what the mesh was authored with |
| **Offset / Rotation / Scale** | |
| **Alpha** | |
| **Visible** (eye) | hide without removing |
| **Animations** | see [Animation](10-animation.md) |

**Reload Models** re-reads every attachment from disk after you have edited its
`.fmdl`.

## Motion

**Velocity** and **Angular Velocity** are plain state the game sets and the
engine carries forward — no damping, no forces. Integrated during play only; in
the editor entities stay where you put them while their animations keep running.

## Tags, attributes and scripts

Every entity carries the same three blocks everything scriptable does:

- **Tags** — how a script addresses all of a kind at once (`#breakable`)
- **Attributes** — named `int` / `float` / `bool` / `str` values the script reads
  and writes
- **Script** — a `.flang` file, plus a row per parameter it declares

Attribute writes made during play are discarded when play ends. See
[Scripting](18-scripting.md).

## Entities that carry geometry

A definition may name a **chunk** — a file under `chunks/`, placed in the
entity's own space. Every instance materialises it as a chunk bound to that
entity's frame, so the geometry is carried around by it: drawn, solid, and
walkable while it moves.

In the entity editor: **Add Chunk** picks a file already under `chunks/`, or
**Import** exports a chunk of the level open in the main window and attaches it
in one click.

The level stores the placement and never the geometry, so a fix to the hull
reaches every ship in the game. See
[Vehicles and frames](12-vehicles-and-frames.md) for what to do with it.

## The player

The engine creates the player rather than reading it from a level. It has no
type, no models and is never saved. Its world collision is its own path, but it
takes part in entity-against-entity collision on the same terms as anything else
— so a solid crate is solid to walk into and a movable one is solid to shove.

`@player` addresses it from a script.
