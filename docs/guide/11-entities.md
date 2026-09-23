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

Editor strip: **Entity**. It edits a *type*, not a placement — a 3D view of the thing
being assembled, an outline of what it is made of down the left, the type's
properties down the right.

Reach it three ways: the editor strip, **Edit Type** beside a placed entity, or
**Make Type** beside a blank one — which seeds a new definition from something
already placed.

Saving writes the file, drops the definition cache, and re-stamps every instance
of that type in the level open beside it, so the change is visible in both
windows at once. Each instance keeps what it overrides — the script parameters
it bound, a tag it added, a bound it widened — and follows the type in
everything else, exactly as it would after reopening the level.

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

A type file may also give `scale: {x: 1.5, y: 1.5, z: 1.5}`: the size every
placement and every `spawn` of the type starts at. The bounds, the models and
what hangs off their sockets all grow with it. A placement's own scale replaces
the type's rather than multiplying it, and a level only writes one down when it
differs from the type's.

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

Any number, each with its own transform relative to the entity — or to another
of its models.

| Field | |
| --- | --- |
| **Name** | how a script asks for an animation on *this* model rather than another |
| **Model** | `.fmdl` or `.obj` under `models/` |
| **Texture** | `(none)` falls back to what the mesh was authored with |
| **Mount on** / **Bone** | another of this entity's models to ride, and a bone (socket) inside it — leave empty to ride the entity itself |
| **Offset / Rotation / Scale** | relative to whatever it rides |
| **Alpha** | |
| **UV scroll** | slides the texture across the attachment, in texture tiles a second along U and V — a tank's tracks running round, a conveyor. The mesh wants a texture that tiles in that direction. A script changes it as the thing moves (`uv_scroll`); YAML `uv_scroll: {x, y}` |
| **Visible** (eye) | hide without removing |
| **Animations** | see [Animation](10-animation.md) |

**Reload Models** re-reads every attachment from disk after you have edited its
`.fmdl`.

### Sockets

A bone in a model is a socket: somewhere to hang another model, an effect, a
volume, a sprite, a sound or a point light. The thing hung there sits at the
bone's **head**, with its +Y running along the bone toward the tail, and follows
the bone through every animation. The bone's roll does not turn it — use the
attachment's own **Rotation** for that.

To put a sword in a knight's hand:

1. In the model editor, give the knight's model a bone where the grip goes, and
   name it — say `hand_r`. It needs no weights, and the model needs no
   animation for the bone to work as a socket.
2. In the entity editor, add both models to the type, named `body` and `sword`.
3. Select `sword` and set **Mount on** to `body` and **Bone** to `hand_r`.
4. Nudge **Offset** and **Rotation** until the grip sits in the palm. They are
   now measured from the bone, so zero is the bone's head.

A weapon authored the usual way — grip at the origin, muzzle down its −Z, top
+Y — sits right in a socket bone pointing along the grip with a **Rotation** of
`(90°, 180°, 0)`. `entities/enemy_rifle.yaml` mounts one that way, and mounts a
grenade the same way on the other hand's socket — a prop needs no "forward" of
its own to hang correctly, and the same rotation lines its body up too.
`entities/grenade.yaml` is the thrown, live version of that model: a separate
entity a script spawns and gives a velocity, not the one riding the hand (see
that held attachment's comment for the two, and `scripts/grenade.flang` for
the fuse and the explosion).

A mounted model's own bones are sockets too, so a flash can hang off the tip of
that sword. **Mount on** leaves out any model that would end up riding itself.
Renaming a model keeps what rides it; deleting one puts what rode it back on the
entity. The same rows are on a placed entity's model in the level editor, where a
change is saved as an override on its type. An override can move a model to
another mount but not take it off one; only the type can do that.

## Motion

**Velocity** and **Angular Velocity** are plain state the game sets and the
engine carries forward — no damping, no forces. Integrated during play only; in
the editor entities stay where you put them while their animations keep running.

An entity an AI controller walks climbs a riser (up to its **step height**) by
being lifted onto it in a single tick, which is exact for collision but would
read as a pop. So the *drawn* body — its models, the weapon in its hand, and its
shadow — is eased up over the step instead, closing the gap in roughly a third
of a second. Only a real step is eased: walking up a slope, falling off a ledge,
boarding a ship and a script setting `position` are all drawn exactly where the
entity is. Nothing a script reads changes: `position`, `bone_position`, bounds
and hitscan all see the true, already-stepped-up body.

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
faces, `E`/`I`/`B` to extrude, inset and subdivide, `J` to join faces, `M` and `X`
to merge and dissolve points, `G`/`R`/`C` to move, turn and resize, and the same brush and face property blocks down the right.
Click a face in edit mode to texture it on its own; `Ctrl+click` more and a
field changed in the face block is written to all of them.
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

## Entities that carry volumes

A type may carry **volumes** as well: regions in its own coordinates that go
wherever it goes — a guard's sight cone, the pickup radius around a crate, the
plate under a trap. Add them from the **Volumes** list in the entity editor's
outline. Each is drawn as a cage in that window, and the move, rotate and scale
gizmos place and size it.

| Field | |
| --- | --- |
| **Name** | what the level ID is built from, and what an instance override matches on |
| **Enabled** | a disabled volume contains nothing — what a region that arms on cue ships with |
| **Clears clouds** | no volumetric cloud is drawn inside it — a ship's hold that stays clear in a cloud bank. See [Keeping cloud out](08-sky-and-fog.md#keeping-cloud-out) |
| **Model** / **Bone** | a socket inside one of the type's model attachments — leave empty to hang it off the entity's origin |
| **Offset / Rotation** | placement, in the entity's space or the bone's |
| **Shape / Half-extents** | as a level volume's. Scale drags the half-extents, and the entity's own scale reaches them once placed |
| **Scripting** | the tags, attributes, script and parameters the placed volume carries |

Placing the type puts a real volume in the level, riding the entity, addressed as
`<entity id>.<volume name>` — so `@guard_2.sight` names exactly one, and
`@guard_2.volumes` lists them all. Its script gets that volume's `:enter:` and
`:exit:` with `@self` the volume, and `@self.prop.carrier` is the guard it
belongs to. The guard itself never enters its own volume. `spawn` gives a new
guard its own, and `destroy` takes them away with it.

A carried volume is a trigger. A level exit, a doorway, fog and scatter are
places rather than parts of a thing, so they stay on the level's own volumes —
one of which can still ride an entity by hand (see [Volumes](13-volumes.md)).

The level file stores the placement and never the volumes. Selecting one in the
level editor shows a note naming the entity that owns it.

## Entities that carry paths

A type may carry **waypoint animations** too: paths in its own coordinates that
go wherever it goes — a blast door sliding shut, a hatch swinging open, a lift
car inside a moving platform. Add one from the **Paths** list in the entity
editor's outline, or select a **Dynamic** brush and use **New Path From Here** in
its **Paths** group, which starts the path on that brush.

Shaping and timing one works as it does in the level editor (see
[Waypoint animations](02-building-levels.md#waypoint-animations)), in the
entity's space instead of the world's. **Ctrl+click** in the view adds the next
point after the selected one, a click on an arrow selects its point, and `G`,
`R` and `C` move, turn and scale it. The timeline along the bottom of the
viewport retimes the points. Pick a dynamic brush in the timeline's **Brush** box
and **Play** runs it along the path without leaving the window; **Stop** puts it
back, and nothing the preview does is saved.

Placing the type puts a real waypoint animation in the level, addressed as
`<entity id>.<path name>`, whose points ride the entity: a door sent shut while
its ship is climbing closes against the hull, not against the pad the ship left.
From the type's own script, name both halves through `@self`:

```flang
def shut(:boarded:, who ref) -> null
    play_waypoints(@self.hatch, @self.door_close)
end
```

A string with the name the type gave the path works too:
`play_waypoints(@self.hatch, "door_close")`. Either way each placement's hatch
closes along its own copy. The entity itself cannot follow a path it carries;
send one of its parts along it instead.

The level file stores the placement and never the paths. The level editor draws
each one where its entity is and can preview it on any object, but the points
belong to the type: their fields are greyed out, and an **Edit Type** button
opens the type in the entity editor. Saving the type re-stamps every placement.

## Entities that think

An entity with an **AI** controller walks the level by itself: to where a script
sends it, after the player or anything else on its target list, and through the
objective volumes it is given. Add one from the **AI** group of a placed entity
or a type. See [AI agents](24-ai.md).

## The player

The engine creates the player rather than reading it from a level. It has no
type and is never saved. Its world collision is its own path, but it takes
part in entity-against-entity collision on the same terms as anything else —
so a solid crate is solid to walk into and a movable one is solid to shove.

`@player` addresses it from a script.

### Crouching

Holding `C` or left `Ctrl` crouches the player: the body shrinks from 1.8 tall
to 1.0 with its feet kept where they were, the eye eases down, and walking
drops to under half speed. Letting go stands back up only if the taller body
fits, so a crouch under a low ceiling or a hatch stays crouched until the
player is out from under it. A crouched player is a smaller target too — AI
sight and hitscans test the same shorter bounds. Scripts read it as
`@player.prop.crouching`.

### Sprinting

Holding `Left Shift` sprints at 1.65× walking speed. It lasts while both
`Left Shift` and `W` are held, and ends when either comes up, or the player
crouches, sits down, or loses control. Scripts read it as
`@player.prop.sprinting`. Writing `false` ends a sprint and keeps it off while
the key stays down, and writing `true` lets a key that is still held sprint
again. Neither one starts a sprint the player is not holding the key for. The
shipped `player_combat.flang` writes `false` when the player fires or aims, and
`true` shortly after they stop.

### Mouse look and the player's settings

Gameplay mouse look is smoothed and scaled by three player settings, which are
the player's rather than any level's: `@level.prop.mouse_sensitivity` (a
multiplier), `@level.prop.mouse_smoothing` (0 raw – 1 heaviest) and
`@level.prop.mouse_invert_y`. A script writes them — the shipped
`ui/settings.fui` does — and the engine saves them to `controls.yaml` in the
same place as `audio.yaml` once a change has settled. Smoothing only spreads a
movement over a few frames; it never changes how far it turns. Zoomed in
(`@level.prop.camera_fov` below the default) the turn slows in proportion, so
aiming down sights is not twitchier than the hip.

The player's keys are saved in the same `controls.yaml`: every gameplay action
(moving, jumping, firing, the weapon keys, a vehicle's roll and pitch…) has two
bindings a settings screen can change with `bind_key` — see
[UI screens](17-ui-screens.md#rebinding-keys). The volume is
`@level.prop.master_volume` (with `music_volume` and `effects_volume` under
it), saved to `audio.yaml` the same settled way.

### The player's script

Nothing hears an event sent to `@player` until the level gives the player a
script. The `player:` block takes the same three keys an entity type does:

```yaml
player:
  script: scripts/player_combat.flang
  attributes:
    health: {type: int, value: 100}
    health_max: {type: int, value: 100}
  params:
    restart_level: {type: str, value: level1}
```

That script receives everything addressed to the player — the fire, throw and
weapon-slot input below, `:damage:` from an enemy or a grenade, `:tick:`,
`:anim_end:` from its viewmodel clips — and runs under `@level.prop.paused` like
any other level object. Every play session, and every level change, starts the
player from these attributes again, so health written during one session is
not what the next one begins with.

The level editor's Player panel has **Script** and **Attributes** groups for
this, the same ones a placed entity shows.

### First-person viewmodel

A level's `player:` block (see [Player step height](02-building-levels.md#player-step-height)
for the rest of it) may carry a `models:` list — the same shape, and the same
socket rules, as any entity type's own. This is what puts hands and a held
weapon on screen: unlike every other model attachment in the engine, the
player's own draws directly in front of the camera rather than at its
physical position in the world, so it is never occluded by nearby geometry
and casts no shadow.

The level editor's Player panel has a Viewmodel section for this — the same
attachment editor a placed entity's own properties panel shows (mesh, texture,
socket, transform, animations), plus a combo to switch it to any AI entity's
attachments instead of the player's own. Or write `models:` into the level
file's `player:` block by hand, the way the example below does.

```yaml
player:
  models:
  - name: hands
    path: models/fp_arms.fmdl
    animations:
    - {name: rifle_idle, autoplay: true}
    - {name: rifle_fire, loop: false, blend: 0.03}
  - name: rifle
    path: models/rifle.fmdl
    parent: hands
    bone: hand_socket.R
    rotation: {x: 3.14159265, "y": 1.57079633, z: 0}
```

A first-person rig is authored exactly like any other rigged model — bones,
weights, clips, hand sockets — with one difference worth knowing before you
build one: its own local **−Z is the direction the camera looks**, and **+Y is
the camera's up**, with no additional rotation of the kind a third-person
attachment needs (`docs/guide/11-entities.md`'s own weapon example turns the
body about because that model faces +Z; a viewmodel built facing −Z from the
start needs no such turn on its root attachment). A weapon mounts on its hand
socket exactly the way it would on any other rig — the `(90°, 180°, 0°)`
rotation from the Sockets section above still applies unchanged.

Switching what the player holds is `visible` and `play_anim`, the same pattern
a carried grenade uses on an NPC:

```
@player.model.rifle.prop.visible = false
@player.model.pistol.prop.visible = true
play_anim(@player, "hands", "pistol_idle")
```

The viewmodel is drawn through a **lens of its own** — 55° by default, with a
near plane ten times closer than the world's — rather than through the camera's.
That is what keeps aiming down a scope from magnifying the arms along with the
target, and what stops a hand held close to the eye being sliced open by the
near plane. A script narrows `@level.prop.camera_fov` to zoom the *world*; it
moves `@level.prop.viewmodel_fov` only to push the gun itself nearer or further
away. A gun's sight alignment is unaffected by either: a sight line through the
eye projects to the middle of the screen at any field of view.

A level that says nothing in `player: models:` gives the player no viewmodel
at all — unlike `step_height`, this is not merged with whatever the previous
level had; each level states its own.

Pressing fire, switching weapons and throwing are input events posted
straight to `@player` — `:primary_fire:`/`:primary_fire_end:` (left mouse),
`:secondary_fire:`/`:secondary_fire_end:` (right mouse), `:throw:` (G) and
`:weapon_slot:` (1 to 5, with the slot number as its argument). See the Events
table in [Scripting](18-scripting.md#events) for the full shape, and
`enemy_combat.flang` for the state-machine pattern (fire-on-a-timer while
held, one-shot clips, a carried grenade) a player-side handler for these wants
too.
