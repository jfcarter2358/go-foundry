# Volumes

A volume is a region of space a script can react to: a shape, an ID, and a
script. It is never drawn outside the editor, never collides, and never moves on
its own. All it does is answer "is this point inside".

Use one for anything a model-less entity would have done — a trigger, a script
anchor, a level exit, a region of fog. It is the smaller idea, so it is the
smaller type: no velocity, no collision flags, no models. (It can *set* the
gravity inside itself — see below — but it never has one of its own.)

## Placing one

`Add... > Volume`, give it an ID, then click in the viewport.

| Field | |
| --- | --- |
| **ID** | committed on Enter; an ID used by anything else in the level is refused |
| **Enabled** | a disabled volume contains nothing, so it fires no events |
| **Shape** | `box`, `sphere`, `cylinder` |
| **Half-extents X/Y/Z** | half the region's size on each axis |

A volume is a wireframe cage in the 3D view. Click it to select it, then use the
translate, rotate and scale gizmos — scale drags the half-extents, since a
region's size *is* its extents.

Clicking tests against the real solid, so a sphere is picked where it looks
picked. A volume the camera is standing inside is skipped by the picker on
purpose, or a room-sized trigger would swallow every click in the room it wraps.
Select it from the Explorer instead.

## Gravity

Tick **This volume sets a gravity** under the **Gravity** group and everything
whose centre is inside the region — the player, entities and vehicles alike —
falls at that rate instead of the level's. `0` is a zero-g chamber, a negative
number throws things at the ceiling.

It only applies while the volume is **enabled**, so a script arms and disarms a
room's gravity by arming the volume. Where two gravity volumes overlap, the
smaller one wins, so a low-gravity chamber authored inside the room volume that
dims the lights behaves the way it reads. A script reaches it with
`@vol.prop.gravity`; writing `null` hands the region back to the level.

A body's own `gravity_scale` multiplies whatever it finds here — see
[Entities](11-entities.md#collision).

## Events

Give it a script and it gets:

| | |
| --- | --- |
| `:enter:` | `other ref` — something's origin entered the region |
| `:exit:` | `other ref` — and left |
| `:interact:` | `src ref` — the interact key was pressed inside it |
| `:spawn:` `:destroy:` `:tick:` | as everything else |

```
def on_enter(:enter:, other ref) -> null
    if other == @player
        play_music("tension", 2.0)
    end
end
```

Disabling a volume disarms it. That is how a locked door works — the trigger is
there, switched off, and unlocking it is
`@cave_mouth.prop.enabled = true`.

## Riding something

A volume can ride a vehicle, an entity, or a bone of one of that entity's
models: the same **Frame / Entity / Model / Bone** rows a sprite has, at the top
of Transform. Attached, **Position** and **Rotation** are in the carrier's space,
so a region authored at `0, 1, 3` on a guard sits three units in front of
whichever way the guard faces, and goes wherever the guard goes.

- A volume never reports **its own carrier**. A guard standing in the middle of
  its own sight cone fires no `:enter:`.
- `@cone.prop.carrier` is the entity it rides, or null. `attach(@cone, @guard)`
  and `detach(@cone)` bind it from a script.
- A **doorway** and **scatter** honour only the Frame row: both are about a
  vehicle's decks, which a walking entity does not have.
- The gizmos drag the authored offset, as they do for an attached sprite.

In a level file the binding is an `attach:` block. A volume saved before volumes
could ride entities says `frame: shuttle` instead, and still loads.

```yaml
volumes:
  - id: cockpit
    attach: {frame: shuttle}
    position: {x: 0, y: 1, z: 2}
    half: {x: 1, y: 1, z: 1}
```

### Carried by an entity type

When every placement of a thing wants the same region, put the volume on the
**type** instead — see [Entities](11-entities.md). Each placement then gets its
own, named `<entity id>.<volume name>` and shown in the Explorer as
`(from guard_1)`. It belongs to the type, so nothing changed on it in the level is
saved, and its Duplicate and Delete are switched off.

## Level exits

A volume carrying a **level exit** loads that level when the player walks in.
Set it in the volume's **Level Exit** group, from a list of the levels that
exist. No script needed.

- Only the **player** takes one. A guard on patrol crossing it does not end the
  level.
- Only while the volume is **enabled**.
- It fires once, on the way in, not every frame the player stands inside.

**Place the destination's spawn clear of its own exit back.** The player arrives
inside a volume they were not in a moment ago, which is an entry like any other —
two levels whose spawns sit in each other's exits bounce between themselves
forever.

An exit may carry a script as well. Its `:enter:` handler runs in the frame the
load is requested, before the load starts, which is where "save what the player
was carrying, then move on" belongs.

Write `load_level("vault")` in a script instead when the destination is a
decision rather than a fact:

```
def on_enter(:enter:, other ref) -> null
    if @self.attr.key_found
        load_level("vault")
    else
        show_screen("locked")
    end
end
```

## Checkpoints

Tick **Checkpoint** on a volume and it becomes a checkpoint. The first time the
player walks in, the game autosaves, and the death screen's **Retry** comes back
to that point.

- Like an exit, only the **player** triggers it, and only while the volume is
  **enabled**.
- It fires once per play-through, not once per entry. The set of checkpoints
  reached is saved, so a retry does not reach the same one again.
- A script on the volume still gets `:enter:`. The save is taken after that
  handler has run.

See [Saves and checkpoints](26-saves-and-checkpoints.md).

## AI objectives

A volume can be somewhere AI agents are sent to act: a console to hack, a post
to stand guard at, one stop on a patrol. Tick **Agents are sent here to act** in
its **AI Objective** group. An agent that lists the volume walks to a spot inside
it, stays for the objective's **Duration**, and then both the agent and the volume
receive the objective's **Action** atom and `:objective_complete:`. See
[AI agents](24-ai.md#objective-volumes).

## Naming a place for Claude

A volume can name a *place* — a room, a corridor, a courtyard — for the
[Claude Code panel](27-claude-code.md). Tick **Describe this region to Claude**
in its **Assistant Context** group (`context:` in the file), and the volume's ID
becomes something you can say in the panel:

> take a look at @mess_hall and change the style of the room

Claude gets the region's bounds and everything inside it, with positions and
textures. The optional note beside the tick is for what the geometry cannot say —
"the crew's mess — lived-in, warm light".

Unlike every other block on this page, this one does nothing in the game. It is
authoring metadata: the volume is not a trigger, draws nothing, and the level
behaves exactly as it would without it.

## Environment regions

A volume with a **region** block marks part of a generated world as one
environment — "this stretch is desert canyon, that one savannah". The level's
world generator (`resources/generators/<level>_world.fgen`, see
[Generators](33-generators.md#world-generation-environment-regions)) grows
every region's environment in one pass, blended across a transition band.
Nothing about play changes: a region is authoring data, never drawn or
collided with in the game.

Tick **This volume is an environment region** in the volume's
**Environment Region** group. The footprint starts as the box's rectangle; then:

- **Environment**: the preset grown there — the built-ins `desert_canyon` and
  `savannah`, or any `resources/environments/<name>.fgen`.
- **Generator**: optionally a different `.fgen` to grow instead of the preset.
- **Priority**: where regions overlap, the higher wins; equal priorities share
  the band evenly.
- **Transition (m)**: the blend band's width, centred on the footprint's edge
  (default 150).
- **Anchor**: where the preset's own coordinates (its canyon paths) are
  measured from, relative to the volume; off is the footprint's centroid.

**Shaping the footprint.** Select the region; every footprint point shows a
handle at the volume's height, and the outline is drawn in the environment's
colour. Drag a handle to move the point (one undo step per drag),
**Ctrl+click** near an edge to add a point there, **right-click** a handle to
delete it (a footprint keeps 3). Concave outlines are fine; crossing edges
are refused when the world generator runs. The move gizmo carries the whole
region, and the scale gizmo (or the Shape group's X/Z) scales the footprint —
the box is always fitted to it.

**Open World Generator** in the group (or the Generator's **File > Generate
World for <level>…**) makes the level's world generator the first time and
opens it.

**Forbidden neighbours.** The group also lists any neighbouring region whose
environment the adjacency table forbids next to this one (desert beside
tundra, say), checked in the background as regions move, with **Insert
<env> region**: one click adds a region of the table's suggested environment
between them (one undo step). See [Environment regions](35-regions.md#4-adjacency-which-environments-may-touch).

```yaml
volumes:
  - id: canyon_west
    position: [-300, 40, 0]
    half: [300, 25, 300]         # kept fitted to the footprint
    region:
      environment: desert_canyon
      priority: 0
      transition: 200
      footprint: [[-300, -300], [300, -300], [300, 300], [-300, 300]]   # relative to position
```

## Grass fields

A volume with a `grass:` block is one terrain tile's **Poly Haven grass**,
drawn by the grass instancer. A generator's `grass_field` stage places them
(one per tile, its box the tile and the tallest clump on it); you do not
author them by hand.

```yaml
- id: gen.my_world.r_main_sward.grass_3_4
  position: {x: -128, y: 6, z: 64}
  half: {x: 64, y: 9, z: 64}
  grass:
    file: generated/my_world/r_main_sward/grass_3_4.fgrass
    chunk: auto
```

`file` is the field (resources-relative). `chunk` streams it with a chunk's
switch: `auto` takes the chunk whose footprint holds the volume, a chunk ID
names one, empty is always in. A grass volume is not a trigger, nothing
collides with it, and a level without one draws exactly as before. See the
Generators guide's `grass_field` stage for what is in the file and how it
draws.

## Fog volumes

A volume may be full of fog or full of cloud — the things a volume can be that
the player sees. See [Sky and fog](08-sky-and-fog.md) and its
[cloud volumes](08-sky-and-fog.md#cloud-volumes). It can also keep cloud *out*:
**Clears clouds** (`clears_clouds: true`) draws no volumetric cloud inside the
region, which is how a ship's hold stays clear in a cloud bank — see
[Keeping cloud out](08-sky-and-fog.md#keeping-cloud-out).

The short version: open the volume's **Fog** group, set a colour and a density.
Density is per world unit travelled *inside* the region, so the same number means
the same thing on a doorway and on a ravine. It still fires `:enter:` and
`:exit:`, which is usually the event you want when there is fog to be in.

## Ambient volumes (dark interiors)

The level's ambient light is a sky that is the same everywhere. Live GI finds
out by itself that a cave's floor cannot see it; with GI off
(`gi_quality: off`) a cave is lit like the desert outside, and only what was
baked with vertex AO darkens. An **ambient** volume says so instead: inside it
the sky light every lit surface takes — walls, terrain, Nanite scenery, rocks,
characters, scatter, sprites — is multiplied down, and the sky-lit haze along
the part of the view inside it is dimmed too.

```yaml
volumes:
- id: cave_dark
  shape: box
  position: {x: 86.5, y: 7, z: 0}
  half: {x: 17.5, y: 11, z: 14}
  ambient:
    sky: 0.08      # multiplier for surfaces facing up (deep inside)
    ground: 0.12   # for surfaces facing down — roofs, overhangs (default: = sky)
    fade: 2.5      # metres in from the volume's faces over which it eases in
    fog: 1         # share of the cut the haze inside takes, 0..1
    dust: 0.03     # extra light-shaft density inside, per metre
```

The cut eases in over `fade` metres **inside** the volume's faces, so make the
volume enclose the cave's rock — its floor, walls and roof at least `fade` deep
inside it — and put the face where the darkening should start at the mouth: a
cave darkens as you walk in rather than at a line. Overlapping volumes take the
darkest. Eight are in effect at once, the nearest the camera. The sun is not
touched (its shadow does that); point lights are not touched. With GI on the
probes see the walls already, and the volume still scales the share of the sky
GI leaves. Editor: the volume's **Ambient (dark interior)** group.

## Tags, attributes, parameters

A volume carries the same scripting block everything else does. Parameters are
the reason twenty trigger volumes can share one script file:

```yaml
volumes:
  - id: button_north
    script: scripts/button.flang
    params:
      target: {type: str,   value: door_north}
      drop:   {type: float, value: 6.0}
```

The Script section of the panel lists every parameter the attached script
declares, one row each, with a picker over the level's IDs for a `ref`. See
[Scripting](18-scripting.md).
