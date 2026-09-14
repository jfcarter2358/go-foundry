# Volumes

A volume is a region of space a script can react to: a shape, an ID, and a
script. It is never drawn outside the editor, never collides, and never moves on
its own. All it does is answer "is this point inside".

Use one for anything a model-less entity would have done — a trigger, a script
anchor, a level exit, a region of fog. It is the smaller idea, so it is the
smaller type: no velocity, no gravity, no collision flags, no models.

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

## AI objectives

A volume can be somewhere AI agents are sent to act: a console to hack, a post
to stand guard at, one stop on a patrol. Tick **Agents are sent here to act** in
its **AI Objective** group. An agent that lists the volume walks to a spot inside
it, stays for the objective's **Duration**, and then both the agent and the volume
receive the objective's **Action** atom and `:objective_complete:`. See
[AI agents](24-ai.md#objective-volumes).

## Fog volumes

A volume may be full of fog or full of cloud — the things a volume can be that
the player sees. See [Sky and fog](08-sky-and-fog.md) and its
[cloud volumes](08-sky-and-fog.md#cloud-volumes).

The short version: open the volume's **Fog** group, set a colour and a density.
Density is per world unit travelled *inside* the region, so the same number means
the same thing on a doorway and on a ravine. It still fires `:enter:` and
`:exit:`, which is usually the event you want when there is fog to be in.

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
