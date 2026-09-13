# Lighting

Three kinds of light, all under **Lights** in the Explorer.

| | Count | Has |
| --- | --- | --- |
| **Global** | exactly one, always | colour, brightness |
| **Directional** | up to 4 | yaw, pitch, colour, brightness, shadows |
| **Point** | up to 8 | position, radius, colour, brightness, shadows |

Past those limits the `Add...` menu greys out the entry, and a level file
carrying more drops the extras in level order.

## Global light

Ambient fill. It always exists and can be switched off but not deleted. It is
what stops the shadowed side of everything being pure black.

Colour and brightness, and nothing else — it has no direction.

## Directional lights

The sun. Aimed by **Yaw** and **Pitch** in degrees rather than by a vector,
because a vector edited one component at a time passes through zero and flips.
The panel shows the resulting direction as a readout, and a compass card in the
corner of the viewport shows where the selected one points.

| Field | |
| --- | --- |
| Yaw / Pitch | which way it shines |
| Color, Brightness | |
| Dynamic | whether it may change in game — see [Static and dynamic lights](#static-and-dynamic-lights) |
| Cast shadows | |
| Shadow strength | how dark the shadow is, when casting |

Directional shadows use three cascades — high resolution near the camera, coarser
further out. You do not configure them per light beyond the on/off and strength.

A directional light has no position and no scale; those rows are absent from the
panel.

## Point lights

`Add... > Point Light` puts one at the camera. Move it with the translate gizmo
in the 3D view, by clicking it in the viewport, or by typing coordinates.

| Field | |
| --- | --- |
| Position | from the Transform block |
| Radius | how far it reaches |
| Color, Brightness | |
| Dynamic | whether it may change in game — see below |
| Cast shadows | a cubemap shadow, so it casts in every direction |
| Shadow strength | |

Rotation and scale do nothing on a point light.

## Static and dynamic lights

Every directional and point light is one or the other, set by its **Dynamic**
checkbox. Lights start static, and so does every light in a level saved before
the switch existed.

| | Static (the default) | Dynamic |
| --- | --- | --- |
| Walls, floors, brushes in a compiled level | baked in at compile time | lit live, every frame, with shadow maps |
| Models, entities, dynamic brushes, sprites, scatter | lit live | lit live |
| A script may change it, or `play_waypoints` move it | no — refused | yes |
| Cost in a release build | nothing on static geometry | the full per-pixel light loop and its shadow samples |

Use static for the lighting a level is *built* with: the sun that never moves,
the ceiling lamps, the glow from a window. Use dynamic for anything that moves,
flickers, switches or sweeps: a searchlight on a waypoint path, a lamp a trigger
turns off, a sun that sets.

The global ambient light has no switch. It is always baked.

Nothing is baked until a level is compiled, so in the editor and in a dev build
both kinds light live and look identical. What differs is only what a script
may do to them, and that is refused in every build, not just a compiled one. A
script that changed a static lamp in the editor would otherwise change it on the
props and not on the walls once shipped. The refusal names the light and the
checkbox, and the script editor flags it as you type.

The Explorer marks dynamic lights `(dynamic)`.

## What gets lit

Everything in the world pass: walls, floors, ceilings, surface meshes, models,
scenery props and scattered rock. Biome foliage is lit
per-vertex.

Sprites and particles take the lighting only if their **Lit** checkbox is on.
Turn it off for anything emissive — fire, sparks, glowing panels.

## Shadow cost

The expensive parts are on function keys, and mirrored in the **Rendering** group
of the editor settings:

| Key | |
| --- | --- |
| `F5` | biome scatter receives shadows |
| `F6` | rock / moss cast shadows |
| `F7` | foliage casts shadows — expensive |
| `F8` | how many cascades scatter casts into |
| `F9` | how many LOD tiers cast at all |

These are engine switches, not level data. They do not save with the level.

## Scripting lights

A light is addressable like anything else — give it an ID and it takes tags,
attributes and a script. Writing its properties needs it to be **Dynamic** (see
above). `@lamp.prop.dynamic` reads which it is, on either kind.

```
@lamp.prop.brightness = 0.0
#streetlights.prop.color = <1.0, 0.8, 0.5>
@sun.prop.pitch = -20.0
```
