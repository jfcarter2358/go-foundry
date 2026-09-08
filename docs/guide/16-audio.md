# Audio

Two kinds of sound, and the tools follow the split.

**A placed sound** is somewhere — a torch, a stream, an engine, an NPC. It gets
quieter and pans as the listener moves and turns.

**Music** has no position. It plays at the same level wherever you stand, and
switching track is a crossfade.

Clips go in `resources/sounds/` as `.wav`, `.ogg` or `.mp3`.

## Placing a sound

`Add... > Sound`, give it an ID and a clip, then click.

| Field | |
| --- | --- |
| **Enabled** | off gives up the voice entirely |
| **Playing** | off stops it but keeps the placement |
| **Loop** | on for ambience, off plays once |
| **Clip** | the panel reports its length, rate and channel count, or why it would not load |
| **Gain** | (named gain, not volume — Volume is a level object type) |
| **Pitch** | clamped to what the mixer resamples cleanly |
| **Bus** | `effects` or `music` — which of the player's sliders it answers to |
| **Position / Rotation** | rotation aims the cone |

**Audition** plays it at the camera, ignoring distance — for checking the clip
itself.

## Attenuation

The substance of the panel: how far the sound carries.

| Field | |
| --- | --- |
| **Ref distance** | full gain within this radius |
| **Max distance** | silent beyond it, and skipped by the mixer entirely |
| **Rolloff** | how fast it falls off between them |
| **Falloff** | `inverse`, `linear`, `exponential` |
| **Show ranges** | draws the two distances as rings |

Turn **Show ranges** on. It is the only way to place a sound by eye.

## Cones

Collapsed by default, because most sounds are omnidirectional.

**Inner** and **Outer** are total widths in degrees, not half-angles. **Outside
gain** is what you hear behind it. Rotation aims the cone.

## Attaching

A sound rides a frame, an entity or a bone exactly as a sprite does — same panel,
same rules. An engine hum follows the ship; a torch crackle follows the hand
carrying it.

## Music

Level-scoped, not a placed object. A level names one global track.

From a script:

```
play_music("cavern")        -- crossfades over one second
play_music("cavern", 4.0)   -- over four
stop_music(2.0)
```

`play_music` returns `false` when that track is already playing, so a handler
that sets the track every tick does not restart it every tick.

## One-shots

What gameplay reaches for. Both place a sound that removes itself when it
finishes, so a script firing one per impact does not fill the level with spent
ones.

```
play_sound("clank", @door)              -- at an object
play_sound_at("thud", <3.0, 0.0, 4.0>)  -- at a point
play_sound_at("thud", at, 0.5)          -- quieter
```

The commonest use is an impact handler, where the collision already hands you the
place:

```
def on_collide(:collide:, other ref, at vec) -> null
    play_sound_at("impact", at)
end
```

And for a placed sound the level already has:

```
play(@waterfall)   -- start it, switching it on if it was off
stop(@waterfall)   -- stop it, without giving up the placement
```

## Settings

Under **Audio** in the editor settings, and the only settings in the engine that
survive a restart:

- **Master gain** — multiplies everything, music included
- **Music gain**
- **Effects gain**

Plus a live readout of active voices, the last block's peak, and any underruns.
With no audio device the whole group is replaced by one line saying so.

Written to `audio.yaml` beside the executable when a slider is released. In a
shipped game they go to the per-user data directory instead.

## Limits worth knowing

- **32 voices.** When the pool is full, a new sound steals the quietest voice of
  equal or lower priority, and is dropped if there is none. Sustained voices are
  capped separately, so a script bug that starts a loop every frame cannot eat
  the pool.
- **`sounding` is not `playing`.** A sound can be switched on and inaudible
  because it is out of range or because there is no device. `sounding` is the
  read-only property that tells you which.
- **`clip` is read-only from a script.** Changing it means reading a file from
  disk, and a tick must not do file I/O.
- Naming a clip with no matching file is reported once and the placement is kept,
  so you can see it in the editor and fix it.
