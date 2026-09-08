# Testing and debugging

## Play

`` ` `` or `~` toggles between editing and playing. The level you are editing is
the level you play — there is no build step.

Entering play snapshots the level and leaving restores it, so nothing a script
did is written back. The camera returns to where it was when you pressed play,
and your other editor windows stay open.

Entities only move while the editor is closed. Their animations run in the editor
anyway, so you can see a clip while placing the thing that plays it.

## The log window

Toolbar: **Log**, or the button in the script editor.

It carries script output from `log(...)`, runtime errors with the file and line
they happened at, and the engine's own log beside them.

| | |
| --- | --- |
| `Space` | pause / resume the view — the log keeps filling |
| `Ctrl+L` | clear the buffer |
| `Ctrl+F` | jump to the filter box |

Checkboxes filter by `info`, `warn`, `error` and `engine`, plus a text filter and
**Follow** for autoscroll.

**Errors are clickable** — clicking one opens the script that produced it at the
line.

Everything also goes to the terminal and to `foundry.log`, so nothing is lost by
running headless or reading a crash after the fact.

## The F1 reference

`F1` opens the in-engine reference, and it is generated from the engine's own
tables rather than written by hand — it cannot describe a version of flang, or a
set of keybindings, that is not the one running.

It contains:

- every keybinding, with the bindings for whatever mode you are in brought to the
  front
- every event the engine posts, with its arguments
- every standard-library function with its signature and return type
- every property of every object kind, with whether a script may write it

`Esc` closes it.

## The debug overlay

`F3`. FPS, memory, and object counts.

## Engine switches

These are engine-wide, not level data, and they do not save with the level. They
exist so "this entity is not solid" can be told apart from "collision is off" in
one keypress.

| Key | |
| --- | --- |
| `8` | entity-vs-world collision |
| `9` | entity-vs-entity collision |
| `0` | gravity |
| `F10` | anti-aliasing |
| `F11` | fullscreen |

The same three collision switches are checkboxes in the **Engine** group of the
editor settings, which also says when one is off — the first thing to check when
something that should be solid is not.

Shadow cost switches (`F5`–`F9`) are covered in [Biomes](06-biomes.md) and
[Lighting](07-lighting.md).

## When something does not appear

Work down this list; it is roughly ordered by how often each one is the answer.

**A model attachment shows nothing.** The mesh failed to load — the panel says
so. Or its **Visible** eye is off.

**An animation does nothing.** You made the clip but did not add an **Animation**
naming it on the attachment. `play_anim` returns `false` and the log says which
of the two lists the name is missing from. See [Animation](10-animation.md).

**An entity is not solid.** Its `world` or `entities` flag is off on the *type*,
or the engine-wide switch (`8` / `9`) is off, or the other side of the pair does
not set `entities` — solidity is mutual.

**A script never runs.** Nothing is attached (the Script dropdown says `(none)`),
or the level is in the editor rather than in play, or every clause's guard is
declining. Check the log for load errors.

**A `?param` reads null.** Nobody bound it in the panel, or the ID it names is
gone. Both read `null`, which is why `if ?target == null` is the check to write.

**A biome polygon is bare.** The slope range excludes the ground under it, or the
draw distance is shorter than where you are standing, or the chunk it belongs to
is disabled.

**A texture is missing.** Its name has no matching `.png` in `textures/`. The log
names the texture and the object keeps drawing what it had.

**A frame portal drops you into nothing, or the screen strobes.** The two halves
do not overlap by enough. See
[Vehicles and frames](12-vehicles-and-frames.md) — this is the rule that catches
everyone.

**A shipped build draws no menu.** A development build never reads `game.yaml`.
`make release-linux` is how to see one.

## Tests

```
make test          # everything
make vet
make release-check # proves the editor is compiled out of a release build
```
