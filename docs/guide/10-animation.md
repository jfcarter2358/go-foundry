# Animation

Animation is skeletal, and it lives entirely inside a `.fmdl`: bone hierarchy,
per-vertex skin weights, and any number of named clips. Author it in the model
editor's **pose mode**.

## Rigging

Press `4` for bone select, then work in the **Bones** section.

| | |
| --- | --- |
| **Add** | a new bone |
| **Extrude** | a child bone from the selected one — the fast way to build a limb |
| **Delete** | |
| **Parent** | a dropdown over the other bones |

Bones compose down the hierarchy, so rotating a shoulder carries the elbow with
it.

### Weights

**Assign points** binds mesh vertices to the active bone.

1. Select vertices (`1`), then pick the bone.
2. Set **Weight** — 1.0 binds them rigidly, less shares them with whatever else
   already claims them.
3. **Assign**.

**Remove** takes the selection off this bone, **Clear** clears their weights
entirely, and **Select assigned** picks the vertices a bone currently moves.

**Auto weights** binds every point to the bones nearest it. It is a distance
heuristic — check limbs that pass close together and fix those by hand.

Up to four bones influence any one vertex.

## Clips

Press `P` for pose mode. The **Animation** section manages clips:

| | |
| --- | --- |
| **New** / **Duplicate** / **Delete** | |
| **Name** | committed on Enter |
| **Duration** | seconds |
| **FPS** | |
| **Loop** | |

Pose mode deforms the mesh for display without editing the document underneath
it. The rest shape is what gets saved, whatever the viewport is showing.

### Keying

| Key | |
| --- | --- |
| `G` `R` `C` | pose the selected bones (scale runs on the bone's own axes) |
| `I` | key every posed bone at the playhead |
| `Space` | play / pause |
| `Left` / `Right` | step one frame |
| `Delete` | delete the selected keyframes |
| `Alt+G` / `Alt+R` / `Alt+S` | clear translation / rotation / scale |
| Click a diamond | select that keyframe (`Ctrl` to add) |
| Click or drag the timeline | scrub |

Panel buttons cover the same ground: **Key pose**, **Key selected**, **Delete
key**, **Reset pose**, **Reset bone**.

A clip only ever mentions the bones you actually moved. Keys store local
translation, rotation and scale channels rather than posed placements, so
rotations interpolate as rotations and a swinging limb does not shorten between
keys.

## Playing a clip in the game

This is two steps, and missing the second is the usual reason nothing happens.

1. **In the model editor**, make the clip. It needs an armature, at least one
   vertex weighted to a bone, and at least one keyframe.
2. **In the entity editor** (or the level's entity panel), select the model
   attachment, open **Animations**, and **Add Animation**. Give it the name your
   scripts will use and point it at the clip.

The two names are deliberately separate. A clip is what the `.fmdl` calls a
motion; an animation is what the game calls it. `walk` can be `walk_cycle_02` on
one model and `locomotion.walk` on another, and a script says `walk` either way.

An animation carries:

| Field | |
| --- | --- |
| **Clip** | which motion in the `.fmdl` |
| **Speed** | |
| **Loop** | follow clip / loop / once |
| **Autoplay** | start it when the entity spawns |
| **Blend** | crossfade time on switch; default 0.15s, 0 for a hard cut |

Then:

```
play_anim(@guard, "walk")                -- every attachment that has "walk"
play_anim(@tank, "turret", "traverse")   -- one attachment of several
stop_anim(@guard)
```

`play_anim` returns `false` and logs which of the two lists the name is missing
from, so a typo tells you which step you skipped.

## Events

A playing clip posts to the entity's script:

| | |
| --- | --- |
| `:anim_start:` | `src ref, model str, anim str` — a clip began, including each loop wrap |
| `:anim_end:` | `src ref, model str, anim str` — a clip finished, or a looping one wrapped |

`model` is the attachment's name; `anim` is the game's name for it, never the
underlying clip name. A looping clip emits both once per cycle, which is what
makes footstep and cycle-counting scripts writable.

## In the editor

Animations run in the level editor even though entities do not move, so you can
see a clip while placing the thing that plays it. In the entity editor, `Space`
plays and pauses every attachment's animations.
