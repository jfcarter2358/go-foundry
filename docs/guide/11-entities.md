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
| Right-click (no drag) | context menu for whatever is selected |
| `Shift`+middle-drag | pan |
| `V` | orbit / fly |
| `WASD`, `Space` / `Shift` | move (`Ctrl` faster) — the eye in fly mode, the pivot while orbiting |
| `F` | frame the whole type |
| `Shift+G` / `B` | grid / bounds cage |
| `P` | play / pause the attachment animations |
| `Delete` or `X` | remove the selected model, brush, emitter or seat |
| `Ctrl+N` / `O` / `S`, `Ctrl+Shift+S` | new / open / save / save as |

The brush half of the window is the 3D view's, key for key — `Tab`, `1`/`2`/`3`,
`G`/`R`/`C`, `E`/`I`/`B`, `Z`. See [Keybindings](21-keybindings.md); `B` is
subdivide when a face is selected and the bounds cage when one is not.

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

A type is built out of **brushes** as well as models — its own solid, in its own
coordinates, carried around by it: drawn, solid, and walkable while it moves.

Build them in the entity editor with the level editor's own tools. The stamp
buttons under the outline's **Brushes** list arm a box, wedge or cylinder: a
ghost follows the cursor and every click places another, until `Escape`. From
there it is the 3D view — `Tab` for edit mode, `1`/`2`/`3` for points, edges and
faces, `E`/`I`/`B` to extrude, inset and subdivide, `G`/`R`/`C` to move, turn and
resize, and the same brush and face property blocks down the right.
`Ctrl+Shift+click` a brush for detail mode, which shapes what it looks like
without moving what a body walks into — see
[Shaping surfaces](03-shaping-surfaces.md).

**Import** copies brushes in from a chunk under `chunks/`, or from the level open
in the main window. A copy, not a link: the type owns its geometry from then on,
and editing it here does not reach back into the level it came from.

The level file stores the placement and never the geometry, so a fix to the hull
reaches every ship in the game.

### What you get for free

**You can walk in.** A body that gets inside an entity's brushes is handed into
that entity's frame, with no doorway to author — so build a room with a way in
and the way in works. From outside the hull is solid, which is what makes the
opening you built the only way through it.

**A passenger is carried.** Once inside, a body is in the entity's coordinates:
the entity moves and the body does not, because nothing had to move it.

**A brush can be driven from a script.** Tick **Dynamic** on one and a script may
write its `position`, `rotation` and `velocity` — a door, a lift, a crusher —
in the *entity's* own coordinates, so "two metres to port" stays two metres to
port while the ship turns, and the ship's motion carries the door on top of it.
The materialised brush is addressed as `<entity id>.<brush id>`. See
[Scripting](18-scripting.md).

`resources/entities/dropship.yaml` is the worked example: a hold of five static
brushes and one dynamic blast door, with `scripts/dropship.flang` driving it.

## Entities that carry effects

A type may also hang **emitters** on itself: particle effects in its own
coordinates, carried around by it — a thruster plume, a torch flame, smoke off a
wrecked crate. The **Emitters** list in the entity editor's outline is where they
are added, and they run in that window while you aim them, so a cone is pointed
by eye rather than by placing the type in a level and looking.

| Field | |
| --- | --- |
| **Name** | what the level ID is built from, and what an instance override matches on |
| **Effect** | a `.fpar` under `particles/`; **Edit...** opens it in the particle editor |
| **Model** / **Bone** | a socket inside one of the type's model attachments — leave empty to hang it off the entity's origin |
| **Offset / Rotation** | placement, in the entity's space or the bone's. `G` and `R` drag them |
| **Enabled** | off stops the simulation dead |
| **Emitting** | off stops new particles but lets the ones in flight finish — what a type whose effect only runs on cue ships with |
| **Rate scale / Tint** | multiply every layer's own, for varying a shared document without a second one |
| **Tags** | carried onto the placement, so `#engine` reaches every ship's plume at once |

What an effect *is* still lives in its `.fpar` and is still edited in the
particle editor — this is only where one hangs and the handful of knobs worth
varying per type. Retuning a plume is one document, not every ship that has one.

Placing the type puts a real emitter in the level, riding the entity, addressed
as `<entity id>.<emitter name>` — so `@shuttle_2.thruster` names exactly one
plume and every particle verb in [Scripting](18-scripting.md) works on it
unchanged. **Bone-mounted effects follow the pose**: a flame on a hand bone
swings with the hand, because it resolves through the same skinning palette the
mesh and its shadow do.

The level file stores the placement and never the emitters, the way it stores
neither the models nor the brushes. Selecting one in the level editor shows it
with a note saying which entity owns it: turn its knobs to see what they do, but
the change that keeps is the one made on the type.

## The player

The engine creates the player rather than reading it from a level. It has no
type, no models and is never saved. Its world collision is its own path, but it
takes part in entity-against-entity collision on the same terms as anything else
— so a solid crate is solid to walk into and a movable one is solid to shove.

`@player` addresses it from a script.
