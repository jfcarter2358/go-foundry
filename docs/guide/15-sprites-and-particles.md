# Sprites and particles

A **sprite** is one camera-facing textured quad placed in a level. A **particle
emitter** is a placed instance of a `.fpar` effect, which simulates a pool of
them. At draw time they are the same thing; the difference is where the transform
comes from — authored, or simulated.

Both can ride a frame, an entity, or a named bone inside one of its models.

## Sprites

`Add... > Sprite`, give it an ID and a texture, then click — it lands standing on
the surface you clicked.

| Field | |
| --- | --- |
| **Position / Width / Height** | position is local to the carrier when attached |
| **Roll** | one field while billboarded; the full X/Y/Z triple only in Fixed |
| **Texture** | |
| **Billboard** | `full`, `y-axis`, `fixed` |
| **Blend** | `cutout`, `alpha`, `additive` |
| **Tint / Alpha** | |
| **Lit** | takes the scene's lights and shadows; off for anything emissive |
| **Casts shadow** | faces the light in the depth passes rather than the camera |
| **Sheet** | columns, rows and cell, for a sprite sheet |
| **Material** | a [`.fmat`](28-materials.md) the sprite wears, overlaid on the rows above |

### Blend mode picks the pass

This is the setting to get right; picking wrong is the usual way a sprite looks
broken.

| Mode | |
| --- | --- |
| **cutout** | draws inside the opaque pass. Writes depth, needs no sorting, and its silhouette is antialiased for free. **The right default** for foliage and anything solid-looking. Set its **Alpha** below 1 — to fade it in or out — and it draws in the alpha pass until it is back at 1, keeping its cutout edge |
| **alpha** | draws after the world's transparent pass, sorted back to front, no depth write |
| **additive** | draws last, order-independent. What fire and sparks should use |

**Billboard** matters nearly as much: `full` always faces the camera, `y-axis`
spins about vertical only (right for trees and grass — a `full` tree lies down
when you look from above), `fixed` does not turn at all.

Both, and the tint, the sheet and the lit/shadow pair, can come from a
[material](28-materials.md) instead — which is how forty sparks in six different
effects are made to agree about what a spark is. A particle **layer** takes one
on the same terms, and a layer's volumetric look can come from one too; only its
ramps stay on the layer.

## The particle editor

Editor strip: **Particles**. It edits `.fpar` effect documents in
`resources/particles/`. Three columns: the layer list, a live preview, and the
parameters for the selected layer. The preview drives the same simulation and the
same draw path the game does.

One `.fpar` is one **effect**, made of layers — `torch.fpar` is flame plus smoke
plus sparks, because that is the unit you place and a script names.

| Key | |
| --- | --- |
| `Space` | play / pause |
| `R` | restart |
| `.` | step one frame |
| `F` | frame the camera |
| `B` | show / hide the emission shape |
| Middle- or right-drag | orbit; `Shift`+middle to pan; wheel to zoom |
| `Ctrl+N` / `O` / `S` | new / open / save |
| `Ctrl+Z` / `Ctrl+Shift+Z` | undo / redo |

### Layers

`[S]` solos (a view state, not saved), `[M]` mutes (saved with the effect).
`[+] [-] [^] [v]` — order is draw order. Right-click a row for duplicate, move,
delete.

### Parameters

| Group | |
| --- | --- |
| **Effect** | ID, Gravity, **Seed** — the seed fixes every random draw, so the preview is reproducible — and the effect's own **Light** (see *A light on the effect* below) |
| **Emission** | Rate /s, Duration, Loop, Prewarm, Max, plus timed **Bursts** (t and n per burst) |
| **Shape** | `point`, `sphere`, `hemisphere`, `box`, `cone`, `disc`, with whichever of Radius / Angle / Half extents / Thickness that kind reads. Direction is `shape`, `random` or `fixed`, with a Vector and a Spread |
| **Initial** | Life, Speed, Size, Rotation, Rot speed — min/max pairs |
| **Motion** | Gravity, Drag, Radial, Tangential, Turbulence, Turb freq, and **Local space** (carry live particles when the emitter moves) |
| **Appearance** | **Render** (`sprite` / `volume`); for a sprite layer Texture, sheet cols/rows, Frames (`over-life` / `random` / `static`), Blend, Billboard, Tint, Lit, Cast shadows; for a volume layer, see *Volume layers* below |
| **Collision** | `none` / `stop` / `bounce` / `kill`, Radius, Bounce, Friction |

Each layer's pool is preallocated and never grows. Emission stalls at **Max** —
a particle budget only means something if it is a ceiling.

### Ramps

Along the bottom, beside the transport: **Size**, **Alpha** and **Colour** over a
particle's normalised life. One strip at a time.

Click adds a stop, drag moves it, right-click deletes, and on the colour strip a
double-click opens a picker.

### A light on the effect

A muzzle flash is two things: the flare on the end of the barrel, and the wall
beside the gun lighting up for a twentieth of a second. The layers are the
first; **Light** in the Effect group is the second.

Turn **Light** (its brightness) up and the effect casts a real point light
while it goes off — dynamic, shadowless, and summed on baked surfaces like any
other. **Light colour**, **Light reach** and **Light time** are the rest of it;
`Light` at 0 is no light at all, which is what every effect has until you ask
for one. In the file it is a `light:` block on the document:

```yaml
id: muzzle_flash
light: {color: {r: 1, g: 0.86, b: 0.62}, brightness: 6, radius: 7, duration: 0.05}
layers:
  - name: flash
    ...
```

It goes off **with the effect's cycle**: at the `spawn_effect`, at a `restart`,
and once per lap of a looping effect. `stop_emit` puts it out with the
particles and `emit` brings it back. The preview does not show it — there is no
scene there to light.

The shader has eight point-light slots, and a firefight can easily want more
than a level has left. When more lights are lit than there are slots, every
**shadow-casting** light keeps its own — its cubemap is found by its slot — and
everything else, flashes included, competes on what the camera can actually
see: brightness and reach count for a light, distance from the camera against
it. So the flash beside the player's head is never the one dropped for a rifle
across the valley, and a level with more lights than slots lights what is in
front of you rather than whatever came first in the file.

### Volume layers: fire and smoke

Set a layer's **Render** to `volume` and each of its particles stops being a
quad and becomes a soft ball of volumetric smoke or fire — ray-marched, lit by
the sun and sky like a [cloud](08-sky-and-fog.md#cloud-volumes), with its surface
pushed in and out by noise that rolls outward as it ages. A fireball is a burst
of eight or so of these. The simulation is unchanged: they move, drag, rise and
collide exactly as quads would.

The ramps mean something different on a volume layer:

| Ramp | on a volume layer |
| --- | --- |
| **Size** | the ball's diameter |
| **Alpha** (shown as **Density**) | its density |
| **Color** | the light it gives off, times Tint |
| **Emission** | how brightly it glows over its life |
| **Erosion** | how much its edges break up over its life |

and the look has its own fields:

| Field | |
| --- | --- |
| **Density** | thickness at a ball's core |
| **Noise scale** | noise repeats across a ball's radius — low is big lumps |
| **Displacement** | how far the noise pushes the surface |
| **Billow** | how fast the noise rolls outward |
| **Erosion** | fine-noise erosion, times the Erosion ramp |
| **Smoke colour** | what the smoke looks like lit |
| **Absorption** | how dark its underside gets |
| **Emission** | glow strength, times the Emission ramp. 0 is plain smoke |

The shape that reads as an explosion: a fireball layer with Emission starting
high and ramping to 0 while Erosion ramps up, then a slower smoke layer released
a moment later with no emission, negative gravity and a growing size ramp.
`explosion.fpar` ships as exactly that, with a sparks layer on top.

At most 64 balls of one layer are drawn at once, 16 per march. Volume layers
cast no shadow-map shadows.

## Placing an emitter

`Add... > Emitter`, give it an ID and an effect, then click.

| Field | |
| --- | --- |
| **Enabled** | off stops the simulation dead; live particles vanish |
| **Emitting** | off stops new particles but lets the ones in flight finish |
| **Effect** | `.fpar` picker, with **Edit...** opening the particle editor on it |
| **Rate scale** | multiplies every layer's rate |
| **Tint** | over every layer's own |
| **Position / Rotation** | rotation aims the emission shape |
| **Show emission shape** | outlines where particles are born |
| **Reload / Restart** | |

## Emitters an entity carries

An emitter placed here belongs to the level. An entity *type* can also carry its
own — a ship's thruster plume, a torch's flame — authored in the entity editor's
**Emitters** list, aimed against the thing they hang off, and optionally mounted
on a bone of one of its models so they follow the pose.

Placing that type puts a real emitter in the level for each one, already
attached, addressed as `<entity id>.<emitter name>`. Everything below works on it
unchanged. What is different is where it is edited: those emitters are the
type's, so the level file does not store them and nothing changed on the
placement is saved — the panel says so, with a button through to the type. See
[Entities](11-entities.md).

## Attaching

The **Attach** group on a sprite, emitter or sound: pick a Frame, Entity, Model
and Bone, each row unlocking once the one above is answered.

From a script:

```
attach(@smoke, @ship)                     -- ride an entity
attach(@flame, @guard, "hand_r")          -- ride a bone of its only model
attach(@flame, @tank, "turret", "muzzle") -- name the attachment too
attach(@wake, "ship")                     -- ride a frame: a vehicle's own space
detach(@smoke)                            -- back to world space
```

`attach` leaves `position` alone — attached, a position is an offset in the
carrier's space, which is the reason to reach for it. `detach` moves the object
to wherever it currently is in the world, so a plume released from a sinking ship
stays over the water.

## Driving effects from a script

```
emit(@fountain)         -- start it, switching it on if it was off
stop_emit(@fountain)    -- stop NEW particles; the ones in flight finish
burst(@fountain, 40)    -- release 40 now
burst(@torch, 12, "sparks")   -- from one layer
@fountain.prop.enabled = false  -- everything vanishes at once
```

`spawn_effect` is what gameplay reaches for — an effect played once at a point no
level authored an emitter for. It returns a ref, and the emitter removes itself
when its last particle dies:

```
def on_collide(:collide:, other ref, at vec) -> null
    spawn_effect("impact_sparks", at)
end
```

Naming an effect with no matching `.fpar` is reported before the script runs,
with a "did you mean".

## Effects: explosions

An **effect** is spectacle the engine draws by itself from a handful of
numbers, rather than from a `.fpar` document. The first kind is an
**explosion**: a ray-marched fireball that radiates outward, cools from a white
flash through orange and red into sooty smoke, tears apart as the smoke thins,
and lights the level with a flash while it burns.

Place one from the Explorer's **Add → Effect → Explosion**, then click. Its centre
lands half its radius above the surface you clicked, selected, going off on a
loop so you can size it by eye. The selected explosion's full radius is outlined in the
viewport; the move, rotate and scale gizmos all work on it, and scaling resizes
the fireball.

| Panel group | Fields |
|---|---|
| Identity | ID, Enabled, Playing (goes off when the level starts), Loop, Loop delay |
| Explosion | Radius, Duration, Brightness, Fire tint, Smoke colour, Density, Turbulence, Rise |
| Light | Colour, Brightness (the flash's peak; 0 lights nothing), Radius |
| Actions | **Detonate** (set it off now), Duplicate, Delete |

The flash is a dynamic point light that takes one of the level's eight point
light slots while it is lit. It never casts shadows. When the level's own lights
and its flashes want more slots than there are, they compete: see
[A light on the effect](#a-light-on-the-effect) for the rule. Rotation turns the billows, so two explosions side by side don't
look alike. Effects live under `effects:` in the level file.

An explosion drawn by the volumetric pass follows its quality setting (F4): with
volumetrics off, the flash still lights the level but the fireball is not drawn.

### From a script

For one a script sets off, untick **Playing** and **Loop**, then:

```
play(@boom)                   -- set it off from the start
stop(@boom)                   -- stop it where it is; it vanishes
@boom.prop.radius = 8.0       -- radius, duration, brightness, density,
                              -- turbulence, rise, light_brightness, light_radius
@boom.prop.live               -- whether it is burning right now (read-only)
```

`explode` is `spawn_effect`'s twin for explosions no level placed: it sets one
off at a point or at an object's position, optionally with its own radius, and
the effect removes itself when the smoke has cleared.

```
def on_collide(:collide:, other ref, at vec) -> null
    explode(at, 3.0)
end
```
