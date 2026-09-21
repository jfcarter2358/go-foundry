# Editor basics

The editor is always there. `` ` `` or `~` toggles between editing and playing;
the level you are editing is the level you play, and the camera returns to where
it was when you come back out.

## The main window

The top bar is two rows: menus, then the tools the mouse uses.

```
File  Edit  View  Build  Window | Level Texture Model Entity Script YAML Particles UI   <name> [Saved]
Object/Edit Tab | Point 1  Edge 2  Face 3 | Move G  Rotate R  Scale C  UV U | Solid/Wireframe Z  Bounds B  Orbit/Fly V
```

- **File** — Open Level... (which also creates one), Save, Save As..., Save All.
- **Edit** — Undo, Redo.
- **View** — Wireframe, Bounds, Fly Camera; the same toggles as the buttons on
  the row below.
- **Build** — Compile, Preview Compiled, Release...
- **Window** — the tool windows: Log, Git, Release. **Git** and **Release** say
  so in the menu while a push, pull or build is still running behind a closed
  window — see [Shipping a game](20-shipping.md#from-the-editor).

After the menus comes the **editor strip**: every editor, by name. Click one to
open it, or to bring it forward if it is already open. The strip is the same in
every editor window, with the one you are in lit and any that are not open yet
dimmed, so switching between them never means going back to the main window
first. They are real windows — move them to a second monitor, leave them open
while you play, close them from their own close button or **File > Close
Window**. Closing one keeps its document; opening it again resumes where you
left off.

At the right edge is the document's name and whether it is saved.

Every other editor's top bar has the same shape: **File**, **Edit** and
**View** menus for its own document, then **Window** and the strip, then its
name and save chip; its tools on the row below.

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

## Object mode, edit mode and detail mode

`Tab` switches between the first two, and it is the first thing to know about the
viewport, because it decides what every click means.

**Object mode** is where you start, and where most of the work happens. A click
picks up a *whole brush* — or an entity, a volume, a light, the spawn marker —
and the gizmos move, turn and scale all of it. This is blocking out: stamp a box,
drag it into place, duplicate it, stretch it into a wall.

**Edit mode** is where you reshape one. A click picks a point, an edge or a face
of a brush, and `E`, `I` and `B` extrude, inset and subdivide what you selected.
This is the model editor's mesh editing, pointed at level geometry — see
[Shaping surfaces](03-shaping-surfaces.md).

**Detail mode** is where you shape what a brush *looks like* without moving what
it *is*. `Ctrl+Shift+click` a brush to go in: from then on it is drawn from a
detail mesh you carve with the model editor's own tools — the real extrude, with
side walls; subdivide; Catmull-Clark smoothing; delete — while collision, and the
compiled level's cells, keep using the solid underneath, which is drawn as a dim
wire cage while you work. `Tab` or `Esc` comes back out. This is how a wall gets
a ledge, a reveal or panelling that a player will never catch on; see
[Shaping surfaces](03-shaping-surfaces.md).

The selected brush is outlined in cyan in object mode and the selected face in
orange in edit mode, so a glance at the viewport tells you which you are in
without reading the top bar. The tool row's leftmost button names the context
outright.

`Tab` does not lose your place in either direction: going in, the brush you had
selected is the one you are now shaping; coming out, the brush that owns whatever
face or corner you had ends up selected. Leaving detail mode does the same.

## Selecting within edit mode

Three select modes decide what a click can land on. `1`, `2` and `3` switch them
— and, pressed in object mode, take you into edit mode on the way, because
reaching for a corner is what you meant.

| Key | Mode | A click lands on |
| --- | --- | --- |
| `1` | Point | a brush corner's handle box |
| `2` | Edge | the prism around a brush edge |
| `3` | Face | one face of a brush |

`Ctrl+click` adds to the selection — in object mode too, where it builds up a set
of brushes to move together. Repeated clicks on stacked handles cycle through
them. Switching between Point and Edge keeps what you had; crossing to or from
Face clears it.

Editing a property applies it to everything selected of that type, and relative
positions, rotations and scales are preserved.

## Gizmos

`G` move, `R` rotate, `C` scale. They act on the centre of the whole selection.
Drag an arm to move along that axis.

Neither a brush nor a face has a transform of its own to write to — the shape
*is* the corner list — so both resolve to the handles underneath them and the
tools drag those. A selected brush is all of its corners, a selected face is the
corners of that face. Moving a brush therefore moves it; turning one turns its
corners about the middle of the selection.

(That is authoring. A brush a *script* moves during play does have a transform
of its own, which the editor never touches — see **Dynamic brushes** in
[Building levels](02-building-levels.md).)

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

`B` toggles the bounding-box view. It draws, over whatever shading is on, the
world bounding box the engine culls and collides against for every chunk,
entity, scenery prop, volume, sprite and emitter in the level, plus every brush
in the active chunk — brushes are level-wide in the thousands, so only the
chunk you are working in gets them. Each kind has its own colour, and the View
section of the panel lists the key along with how many boxes each kind drew.
Where it drew fewer boxes than there are objects, the difference is objects with
no box to draw — an entity with no extents authored, a prop whose model has not
loaded, an emitter with nothing alive yet — which is a thing worth seeing rather
than a box worth faking.

## Undo

`Ctrl+Z` undoes, `Ctrl+Y` or `Ctrl+Shift+Z` redoes. Undo covers structural edits
— placing, deleting, moving, shaping. Each pop-out editor (model, texture,
particle, UI, entity) has its own undo stack for its own document.

## Saving

**File > Save** writes the level back to the file named at the right of the top
bar. **Save As...** names a new one. Levels go to `resources/levels/`.
**File > Save All**, in every editor, saves every open editor with unsaved
changes at once.

Nothing else auto-saves either: the model, texture, particle, UI and entity
editors each have their own Save, and an unsaved document is marked with `*`.

## Editing the level file

**YAML** opens the level's file as text, for the edits the panels are slow at: a
value changed on forty entities at once, a block pasted from another level, a
key no panel shows yet. It has find and replace (`Ctrl+F`, `Ctrl+H`; case, whole
word and regex), colouring, and completion that knows the level format:

- A key offers the keys that mapping can hold and does not have yet, with each
  one's documentation beside the list. Hovering a key shows the same, and says so
  when a key is one the loader does not know and would ignore.
- A value offers `true`/`false`, the options a key lists (`shape:` →
  `box`, `sphere`, `cylinder`), the files it can name (scripts, textures, entity
  types, prefabs, effects, sounds, screens), or else the values the same key has
  elsewhere in the file.

The file is checked as you type. An error — something the loader would refuse —
marks its line red and stops `Save`; a warning (an unknown key, a key given
twice) marks it amber and does not. The status line under the text shows where
the caret is in the level, as in `chunks[3] hangar › brushes[12] › faces[0]`.

`Save` (`Ctrl+S`) writes the file. When it is the level the editor has open,
the level is rebuilt from it behind the loading bar, as one undo step: `Ctrl+Z`
in the main window puts back what was there. A save made while playing reaches
the editor when you stop.

The window reads the file on disk, which may not hold edits you have made in the
editor and not saved. **From Level Editor** replaces the text with the level as
the editor holds it (written the way `Save` writes it, so comments are lost;
`Ctrl+Z` takes it back). When the editor saves the file, a clean buffer follows it;
one with edits of its own asks first.

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
