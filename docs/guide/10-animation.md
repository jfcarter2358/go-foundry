# Animation

Animation is skeletal: a bone hierarchy, per-vertex skin weights, and any number
of named clips. Author it in the model editor's **pose mode**.

The bones and the clips can live in the `.fmdl` itself, or — when several models
are built on the same rig — in a **skeleton** file they all share. See
[Skeletons](#skeletons) below.

## Rigging

Press `4` for bone select, then work in the **Bones** section.

| | |
| --- | --- |
| **Add** | a new bone |
| **Extrude** (`E`) | a connected child from the selected bone's tail — the fast way to build a limb |
| **From head** (`Shift+E`) | a child that starts at the bone's head and points away from it — legs off a pelvis |
| **Fork** (`Alt+E`) | a mirrored `.L`/`.R` pair from a joint on the mirror plane — arms off a spine |
| **Delete** | |
| **Parent** | a dropdown over the other bones |

A bare `E` grows from the head instead when the head handle is the joint you
last clicked. A child grown from the head is not connected (connected welds a
head to the parent's *tail*), but it still follows its parent in a pose.

Bones compose down the hierarchy, so rotating a shoulder carries the elbow with
it.

### Mirror

Tick **Mirror** to build both sides of a skeleton at once. Every edit that
places a bone — a move, rotate or scale drag, a nudge, a typed head, tail or
roll — is copied, reflected, onto its opposite-side partner. **Across** picks
the axis; the mirror plane passes through the model origin, and `.L` is the
positive side.

Partners are found by name: `Arm.L` and `Arm.R`, `leg_l` and `leg_r`, `L_Hand`
and `R_Hand`, `LeftFoot` and `RightFoot`. The partner does not need to be
selected, and is drawn in purple so you can see what will move. Rename one half
and the pair is broken.

While mirror is on, **Add** off the plane makes a `Bone.L`/`Bone.R` pair, and
**Extrude** on either half of a pair extrudes both. Extruding a bone on the
plane makes another centre bone, which is what a spine wants — use **Fork** to
branch a pair off it. Forking turns mirror on.

Pose mode is never mirrored.

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

## Skeletons

A skeleton is the armature and its clips saved as their own document,
`skeletons/<name>.fskl`. Any number of models can link one, and they then share
a single copy of the rig: fix a walk cycle once and every character on that
skeleton walks the new way.

Build it the same way you always did — the bones are still dragged in the model
viewport and the clips still keyed in the timeline. The **Skeleton** section of
the model panel only decides where the result is saved.

| | |
| --- | --- |
| **New Skeleton...** | move this model's bones and clips into a new `.fskl`, and link this model to it |
| **Link Skeleton...** | replace this model's armature with an existing one |
| **Unlink** | keep the bones and clips exactly as they are, but save them inside this `.fmdl` again |

Both are also on the model editor's **File** menu.

Linking carries the skin weights across **by bone name**. A vertex bound to a
bone the new skeleton does not have loses that influence, and the panel says how
many did — so a rename mismatch shows up as a number rather than as a limb that
quietly stops moving.

Once a model is linked:

- Its `.fmdl` holds the mesh, the UVs and the weights. The bones and clips are
  not in it at all, and its weights name their bones instead of numbering them,
  so reordering the shared rig cannot repoint them at the wrong limb.
- **Save** writes both files. The Bones and Animation sections carry a line
  naming the other models on the rig, because an edit reaches all of them.
- Saving reloads every model on that skeleton in the level open beside you, not
  just the one being edited.
- `foundry model inspect` prints the skeleton and who else wears it.

A skeleton needs at least one bone. Clips still key their tracks on bone names,
and a `.fskl` that drives a bone it does not contain is refused on load rather
than loaded with a silent limb.

### When not to share

Sharing a rig means sharing the **rest pose**, not just the bone names. A clip
holds each bone's motion relative to where that bone rests, so two models whose
arms rest in different places cannot play one clip and both look right. If two
characters were rigged separately, retarget one onto the other's rest pose
before linking them — or give them a skeleton each.

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
