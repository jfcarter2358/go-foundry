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

## The particle editor

Toolbar: **Particles**. It edits `.fpar` effect documents in
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
| **Effect** | ID, Gravity, **Seed** — the seed fixes every random draw, so the preview is reproducible |
| **Emission** | Rate /s, Duration, Loop, Prewarm, Max, plus timed **Bursts** (t and n per burst) |
| **Shape** | `point`, `sphere`, `hemisphere`, `box`, `cone`, `disc`, with whichever of Radius / Angle / Half extents / Thickness that kind reads. Direction is `shape`, `random` or `fixed`, with a Vector and a Spread |
| **Initial** | Life, Speed, Size, Rotation, Rot speed — min/max pairs |
| **Motion** | Gravity, Drag, Radial, Tangential, Turbulence, Turb freq, and **Local space** (carry live particles when the emitter moves) |
| **Appearance** | Texture, sheet cols/rows, Frames (`over-life` / `random` / `static`), Blend, Billboard, Tint, Lit, Cast shadows |
| **Collision** | `none` / `stop` / `bounce` / `kill`, Radius, Bounce, Friction |

Each layer's pool is preallocated and never grows. Emission stalls at **Max** —
a particle budget only means something if it is a ceiling.

### Ramps

Along the bottom, beside the transport: **Size**, **Alpha** and **Colour** over a
particle's normalised life. One strip at a time.

Click adds a stop, drag moves it, right-click deletes, and on the colour strip a
double-click opens a picker.

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
