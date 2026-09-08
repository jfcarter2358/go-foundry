# Editor basics

The editor is always there. `` ` `` or `~` toggles between editing and playing;
the level you are editing is the level you play, and the camera returns to where
it was when you come back out.

## The main window

The toolbar runs across the top:

```
[2D Window] [Textures] [Model] [Entity] [Script] [Particles] [UI] [Log]
| Point 1  Edge 2  Face 3
| Move G  Rotate R  Scale C
| Solid/Wireframe Z   Orbit/Fly V
| Level: <name>  [Open] [Save] [Undo] [Redo] [Save As...]
```

Each window button lights while its window is open. They are real windows — move
them to a second monitor, leave them open while you play, close them from their
own close button.

The panel down the right side is always in the same order, whatever you have
selected:

```
Context  ->  Explorer  ->  Properties  ->  View  ->  Editor
```

**Context** is the top three lines: which editor, which select mode, which gizmo,
what is selected, which chunk is active, and the last thing that happened.

**Explorer** is one tree over everything in the level. Click a row to select it.
Right-click a row for what acts on it (duplicate, delete, move to camera). The
`Add...` button at the top is the only thing that creates objects.

**Properties** is whatever is selected, and only that. One thing is selected at a
time — picking a wall drops the entity you had before it.

**View** and **Editor** are the camera and grid controls, and they do not change
between modes.

## Selecting

Three select modes decide what a click can land on. `1`, `2` and `3` switch them,
and they are shared between the 3D view and the 2D map.

| Key | Mode | A click lands on |
| --- | --- | --- |
| `1` | Point | sector corner handles |
| `2` | Edge | the prism around a sector edge |
| `3` | Face | a wall, floor, ceiling, water plane, entity, volume, prop or the spawn point |

`Ctrl+click` adds to the selection. Repeated clicks on stacked handles cycle
through them. Switching between Point and Edge keeps what you had; crossing to or
from Face clears it.

Editing a property applies it to everything selected of that type, and relative
positions, rotations and scales are preserved.

## Gizmos

`G` move, `R` rotate, `C` scale. They act on the centre of the whole selection,
and they work in both the 3D view and the 2D map. Drag an arm to move along that
axis.

A selected face has no handles of its own, so it resolves to the corners it is
built from — a wall is its four corners, a floor its ring. Move, rotate and scale
all work on it that way.

## Cameras

`V` toggles fly and orbit.

| | Fly | Orbit |
| --- | --- | --- |
| Right- or middle-drag | look around | orbit the pivot |
| `W` `A` `S` `D` | fly | slide the pivot |
| `Space` / `Left Shift` | up / down | up / down |
| `Shift`+middle-drag | pan | pan |
| Wheel | dolly | zoom the radius |

`F` frames the selection. `Z` toggles wireframe — solid hides the far side of the
world and only lets you click what you can see, wireframe draws everything as
lines and lets you click any of it.

## Undo

`Ctrl+Z` undoes, `Ctrl+Y` or `Ctrl+Shift+Z` redoes. Undo covers structural edits
— placing, deleting, moving, shaping. Each pop-out editor (model, texture,
particle, UI, entity) has its own undo stack for its own document.

## Saving

`Save` writes the level back to the file named in the toolbar. `Save As...`
names a new one. Levels go to `resources/levels/`.

Nothing else auto-saves either: the model, texture, particle, UI and entity
editors each have their own Save, and an unsaved document is marked with `*`.

## Play

`` ` `` enters play. Entities move, gravity applies, scripts run, sounds play.
The level is snapshotted going in and restored coming out, so nothing a script
did during play is written to your file.

While playing:

| Key | |
| --- | --- |
| `W` `A` `S` `D` | move |
| `Space` | jump, or fly up with gravity off |
| `Left Shift` | fly down with gravity off |
| Mouse | look (the cursor is captured) |
| `E` | interact, or sit / stand in a vehicle |
| `0` | gravity on/off |
| `8` / `9` | entity-vs-world / entity-vs-entity collision, engine-wide |

Opening the editor does not close your other windows, and playing does not
either.
