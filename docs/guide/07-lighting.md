# Lighting

Three kinds of light, all under **Lights** in the Explorer.

| | Count | Has |
| --- | --- | --- |
| **Global** | exactly one, always | colour, brightness, light wrap, sky/ground ambient |
| **Directional** | up to 4 | yaw, pitch, colour, brightness, shadows |
| **Point** | up to 8 | position, radius, colour, brightness, shadows |

Past those limits the `Add...` menu greys out the entry, and a level file
carrying more drops the extras in level order.

## Global light

Ambient fill. It always exists and can be switched off but not deleted. It is
what stops the shadowed side of everything being pure black.

Colour and brightness, plus the two settings that shape how *every* light in the
level falls across a surface:

| Field | |
| --- | --- |
| Light Wrap | how far a light reaches round a surface past side-on. At 0 the light stops dead where a surface turns away from it, a hard line down every curved surface. Higher values let it ease out instead. Default 0.5 |
| Sky/Ground Ambient | how much brighter the ambient is on surfaces facing up than on those facing down. The average stays the colour you set, so this reshapes the ambient without brightening it. 0 is the same from every side. Default 0.5 |

A light only reaches the side of a surface that faces it. A ceiling lit by a sun
above the roof gets ambient only, and a double-sided panel is lit separately on
each side. Levels saved before these settings existed load with the defaults. To
get the old hard look back, set both to 0. The engine used to light both sides of
every surface alike, so a level may look darker where walls face away from its
sun.

### Bounce light

Light that lands on a surface is partly passed on, tinted by that surface's
colour. A sunlit red floor lights the wall beside it red, and the doorway into a
dark room lights the floor inside it. The compile's light bake works this out.
Two more Global Light settings control it:

| Field | |
| --- | --- |
| Bounces | how many times light is passed on from one surface to the next. 0 bakes direct light only; 1 lets the floor light the wall; 2 lets that wall light the ceiling. Up to 4. Default 1 |
| Bounce Strength | multiplies the light each bounce carries. 1 is physical. Default 1 |

In the level file these are `bounce: {count, scale}` under `lights: global:`.
Levels saved before bounce existed get 1 bounce at strength 1.

Things to know:

- **It only exists in a compiled level.** You see it in Preview and in a
  release build. It is not visible in the editor viewport or in a dev build,
  because those light everything live. Use Preview to judge it. With
  [live GI](#live-gi) on, GI replaces the baked bounce. This bounce is what a
  player with GI off sees.
- **Only baked surfaces take part.** Walls, floors and brushes in a compiled
  level both receive bounce light and pass it on. Entities, models, dynamic
  brushes and anything else lit live do neither. Dynamic lights are not
  bounced either.
- **A surface's colour is its texture's average colour.** A face with no
  texture counts as neutral grey. It does not use the normal colour the editor
  paints it with. Nothing passes on more than 90% of what it receives, so an
  all-white room cannot keep brightening itself. Translucent faces neither
  pass light on nor block it.
- **Bake excludes still apply.** A light excluded from a chunk does not reach
  that chunk by bouncing either. The sun kept off the hangar does not bounce
  in off the sunlit ground outside the door.
- **The ambient is still added on top.** Bounce light comes on top of the
  ambient, and the ambient already stands in for light from the sky. If a level
  looks washed out once bounce is on, lower the Global Light's brightness.
- **Each bounce costs about as much compile time as the first.** One bounce
  added about 15% to level1's compile.

### Live GI

Live GI works bounce light out every frame from the lights as they are now.
A dynamic light, an explosion flash or a door swinging open lights the room
around it. An entity walking from daylight into a corridor darkens with its
surroundings. The bake can do neither: it only knows the static lights, and
only as they were at compile time.

Where it is on, live GI replaces two things:

- the flat ambient, everywhere: entities, models, terrain, foliage, sprites,
  and uncompiled levels in the editor
- the baked ambient and bounce, on a compiled level's surfaces. The bake keeps
  the static lights' own direct light, and GI supplies the rest.

Whether GI runs at all is the **player's** choice: `gi_quality` in their
graphics settings (`off`, `low`, `medium`, `high`; see
[UI screens](17-ui-screens.md#a-settings-screen)). A player who has never
chosen gets Medium on a discrete NVIDIA or AMD card and Off on anything else.
Off is exactly the lighting there was before GI. The editor's
**View > Live GI** menu sets the tier for the viewport.

The level's side is under the Global Light, below Bounce:

| Field | |
| --- | --- |
| Live GI | off turns GI off for this level, whatever the player has chosen |
| GI Intensity | multiplies the bounce light GI finds. 1 is physical. Default 1 |
| Probe Spacing | how far apart the finest probes sit, in world units. Smaller catches thinner walls and smaller rooms but reaches less far. 0.5 to 16, default 2 |
| Ambient Floor | how much of the flat ambient is kept under GI, so a sealed room with no light in it is dim rather than black. 0 to 1, default 0 |

In the level file these are `gi: {disabled, intensity, probe_spacing,
ambient_floor}` under `lights: global:`. Each directional and point light also
has **Leave out of GI** (`no_gi: true`). The light still lights what it shines
on, but nothing it lights passes any of it on. Use it for a fill light that is
there to light one thing.

Things to know:

- **The sky comes in through openings.** A sealed room with no light in it is
  black under GI, where the flat ambient used to light it. A room with a
  doorway is lit through the doorway. If an interior goes darker than you want,
  add a light or raise Ambient Floor.
- **What GI sees.** It traces against a coarse copy of the level: brushes as
  their solid hulls (a detail mesh counts as its hull), block terrain, and
  entity models that are opaque, not unlit and not skinned. Characters,
  foliage, sprites, particles, water and glass do not block or bounce GI
  light, but they are lit by it. (With `FOUNDRY_NANITE_FOLIAGE_GI=1`, terrain
  trees within about 120 units of the camera count by their crowns — the
  dense part of each, from its [Nanite foliage](30-terrain.md) voxels — so a
  forest floor is shaded by the canopy over it. It is off by default: it
  makes a forest noticeably darker.)
- **Thin walls can leak.** GI's probes sit on a grid, and a wall much thinner
  than the probe spacing can let light through to the far side. If it does,
  lower Probe Spacing or thicken the wall.
- **It settles over a few frames.** Big changes, such as a flash, show at once.
  Small ones, such as a light sliding along, follow over a fraction of a
  second. A level is warmed up before its first frame, so it does not open
  dark.
- **It reaches about 100 units.** Probes and the copy of the level are centred
  on the camera. Past their reach, surfaces take the light of the probes at the
  edge, so a distant shadow is lit like a near one rather than by the flat
  ambient. Expect it to be roughly right rather than exact out there.
- **Its cost is set per frame, not per level.** It is about the same in a
  small room or a big valley; see [Testing and debugging](19-testing-and-debugging.md#live-gi)
  for the overlay rows and the debug views.

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

In the game (Play, and a release build drawing with TAA), the sun's shadow is a
**virtual shadow map**: one shadow of very high resolution, cut into small
pages, of which only the pages something on screen reads are drawn, so a shadow
edge is about a pixel sharp whether it is two metres away or two hundred. A page
stays drawn until something that casts into it moves, so a still scene costs
almost nothing and a character walking through it redraws only the pages under
it. If a level has more than one shadowing directional light, the sun (the baked
one in a compiled level, else the first) takes the virtual map and the others
use three **cascades**: high resolution near the camera, coarser further out.
The cascades are also what the editor's own 3D view, the MSAA and "off"
anti-aliasing modes, and `FOUNDRY_NANITE_VSM=0` draw. You do not configure
either per light beyond the on/off and strength.

A page that has not been drawn yet, in the first frame or two after something
comes into view, reads a coarser level of the map until its own arrives: a
softer shadow for a moment, never a missing one within the shadow range.

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
| Attach | a frame, entity, model and bone to ride — see below |

Rotation and scale do nothing on a point light.

### Attaching a point light

The **Attach** group rides a point light on a vehicle, an entity, or a bone in
one of an entity's models: a lantern in a hand, headlights on a truck, a glow in
a reactor that moves with the ship. Pick Frame, Entity, Model and Bone, each row
unlocking once the one above is answered. Attached, **Position** is an offset in
the carrier's space, so zero puts the light right on the bone's head.

An attached light is always **dynamic**. It moves, and a static light is baked
where it stands. Attaching one ticks Dynamic for you, and the box stays ticked
until the light is detached. Its shadow cubemap is rendered from wherever the
carrier has taken it.

From a script, `attach(@lantern, @guard, "hand_r")` and `detach(@lantern)` work
on a dynamic point light exactly as on a sprite. See [Scripting](18-scripting.md).

### Flashes

Two things light the level without a light being placed at all: an
[explosion's](15-sprites-and-particles.md#effects-explosions) flash, and a
particle effect whose document carries a
[`light:`](15-sprites-and-particles.md#a-light-on-the-effect) block — a muzzle
flash lighting the wall beside the gun. Both are dynamic and shadowless, and both compete for the level's eight
point-light slots with everything else that is lit: a shadow-casting light
always keeps its slot, and the rest go to whatever the camera can see most of
(brightness and reach for, distance against).

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

### The sun's shadow on baked geometry

One light gets its shadow on baked geometry live, the same as in the editor:
the **baked sun**, which is the first directional light that is static, casts
shadows and is on. The bake stores sunlight per vertex, so its shadow edges
blur across about a grid step. A crate's shadow would wash out next to a face
its own angle turns dark. So each baked vertex also remembers how much of the
sun it received. Within the shadow range (300 units from the camera), the
renderer swaps that share for the sun shadowed by the live shadow maps. Those
hold everything that casts: the level itself, entities, soldiers, a dropship,
and chunks switched on after load. The result is a sharp edge that matches
editor play. The sun also picks up a surface's normal map, as it does live. Further out, the baked shadow fades in and takes over, so distant
cliffs still shade the valley below them.

The sun's bounce and the ambient stay baked (or come from live GI when it is
on), and other static lights keep their baked shadows. Chunks the sun is
excluded from (below) are left alone: the swap never puts the sun back where
its author kept it out.

Nothing is baked until a level is compiled, so in the editor and in a dev build
both kinds light live and look identical. What differs is only what a script
may do to them, and that is refused in every build, not just a compiled one. A
script that changed a static lamp in the editor would otherwise change it on the
props and not on the walls once shipped. The refusal names the light and the
checkbox, and the script editor flags it as you type.

The Explorer marks dynamic lights `(dynamic)`.

### Bake excludes

A static light bakes into every compiled chunk it reaches, by default. **Bake
excludes**, in a static light's own group (only offered while **Leave out of
bake** is off — a light already left out of the whole bake has no per-chunk
list to offer), names chunks it skips: baked everywhere else, as if it were
never there on those. Type a chunk's ID and click **Exclude Chunk**; the
Explorer's chunk list has the spelling.

For a light that lights a chunk correctly while that chunk exists but has no
business staying baked into it — the level's sun, say, over a hangar a script
disables for good partway through the level (the ground it flies away from
still wants the sun baked in). Baking the sun into the hangar's own walls would
light them for nobody once the chunk is gone, and costs bake time for nothing.

This only trims the *bake*. The light still lights anything drawn live inside
that chunk while it exists — an entity standing in it, in particular, since
entities are always lit live (the table above) whatever a light's own Static or
Dynamic setting is. Excluding a chunk answers "is this light frozen into these
walls", not "does this light ever reach in here".

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
Under the virtual shadow map, `F8` still applies: a page is cast into by the
scatter when the distance it is read at falls in a cascade `F8` allows.

## Scripting lights

A light is addressable like anything else — give it an ID and it takes tags,
attributes and a script. Writing its properties needs it to be **Dynamic** (see
above). `@lamp.prop.dynamic` reads which it is, on either kind.

The one exception is `@lamp.prop.enabled`, which a static light marked **Leave
out of bake** (`no_bake: true`) also accepts. Such a light is lit live rather
than baked, so switching it off switches it off everywhere — it is for a light
belonging to geometry that is not compiled either, like a hangar that flies away
(see [Compiling levels](23-compiling-levels.md#streamed-chunks)).

```
@lamp.prop.brightness = 0.0
#streetlights.prop.color = <1.0, 0.8, 0.5>
@sun.prop.pitch = -20.0
```
