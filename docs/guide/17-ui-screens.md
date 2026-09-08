# UI screens

The runtime UI is native — a shipped game draws its own main menu, HUD, pause
screen and loading screen. The same pass draws the UI editor's canvas, so what
you see while authoring is what ships.

A screen is a `.fui` in `resources/ui/`. Toolbar: **UI**.

## The window

Three columns: the hierarchy on the left, the canvas in the middle, the
properties on the right.

```
[New] [Open] [Save] [Save As] | [Undo] [Redo] | <screen name> | [Pop In]
[ ] Grid  [ ] Outlines  [ ] Safe area | <resolution> | [Data: Sample|Literal|Live]
```

**Resolution** is the size the document is laid out at, which is *not* this
window's size — the numbers on the panel have to be the numbers the game will
use.

**Data** decides what bound widgets read:

- **Sample** invents plausible values that *move*, because the only way to see
  whether a fill direction or a nine-slice works is to watch one change.
- **Literal** turns bindings off; every widget shows what the document says.
- **Live** reads the level the script runtime has.

| Key | |
| --- | --- |
| Click | select a widget; `Alt+click` selects its parent |
| Drag | move (`Alt` suspends snapping) |
| Drag a corner handle | resize |
| Drag an anchor marker | re-anchor **without moving the widget** |
| Middle-drag / wheel | pan / zoom about the cursor |
| `F` | frame the canvas |
| `G` / `B` | grid / outlines |
| `Ctrl+D` | duplicate |
| `Delete` or `X` | delete |
| `Ctrl+N` / `O` / `S`, `Ctrl+Shift+S` | new / open / save / save as |
| `Ctrl+Z` / `Ctrl+Shift+Z` | undo / redo |

## Widget kinds

| Kind | |
| --- | --- |
| `panel` | a rect with a background — the container |
| `image` | a textured quad |
| `video` | the current frame of a video file |
| `text` | a string |
| `button` | a panel with a label and per-state visuals |
| `bar` | a background and a fill: progress, health, loading |
| `slider` | a bar with a draggable handle, writing a value back |
| `toggle` | a checkbox or switch |
| `input` | a single-line text field |
| `include` | splices another document's children in |

## Anchors

A widget is placed by four anchors in 0..1 of its parent's rect, plus four pixel
offsets inward from the rect those anchors describe. One representation for both
the pinned and the stretched case.

The panel gives you the **anchor preset grid** — nine corner/edge/centre pins,
then stretch-x, stretch-y, stretch — and then, per axis, either a position and a
size or the two insets, whichever fits how that axis is anchored. It never shows
both spellings at once.

Dragging an anchor marker on the canvas re-expresses where the widget is in terms
of a different parent-relative origin and **does not move it**. That is the
control that makes anchors usable: set it to bottom-left and the widget stays put
at any aspect ratio.

Children anchor to their parent's solved rect, not its clip rect, so a list
inside a partly-off-screen panel keeps its positions. Clip rects intersect rather
than replace, so a nested panel cannot draw outside its grandparent.

There is no flexbox and no constraint solver.

## Properties

| Group | |
| --- | --- |
| **Identity** | ID, kind, Visible, Enabled, Blocks input, Alpha (multiplied down the whole subtree) |
| **Rect** | the anchor grid, anchor min/max, position+size or insets, pivot, rotation (leaf kinds only), scale, clip children |
| **Style** | colour, texture, UV rect, nine-slice insets in texels, Fit (`stretch` / `fit` / `fill` / `tile`) |
| **States** | hover / press / disabled, each with a colour and a texture. Unset states are derived by tinting, so a button nobody styled still responds |
| **Text** | text, font, size, colour, align, vertical align, wrap, **Bind**, **Format** |
| **Value** | value / min / max / step, fill direction, fill colour and texture, **Bind**, **Bind max** |
| **Navigation** | up / down / left / right over the screen's node ids, plus an action |
| **Include** | source `.fui` and a row per `{{param}}` |

Nodes spliced in from an include are shown muted and cannot be selected — editing
one would write the change into a file that is not the one being edited.

## The screen itself

Select the root:

| Group | |
| --- | --- |
| **Screen** | ID |
| **Canvas** | scale mode (`screen` / `pixel`), reference width and height, match, snap to pixels |
| **Input** | **Modal**, **Pauses the game**, **Shows the cursor**, initial focus |

There is no application-state enum. A HUD is a non-modal, non-pausing screen at
the bottom of the stack. A pause menu is a modal, pausing, cursor-releasing
screen pushed on top of it. A loading screen is the same thing pushed by the load
job. "Is the game paused" is a question about the stack.

## Bindings

A widget says what it reads and the stack polls it once a tick:

```yaml
- id: health_back
  kind: bar
  value:
    bind: "@player.attr.health"
    bind_max: "@player.attr.health_max"
    max: 100
```

```yaml
- id: health_text
  kind: text
  text:
    bind: "@player.attr.health"
    format: "%.0f"
```

Expressing the relationship once, in the document, means there is exactly one
place for it to be wrong — rather than every place that changes health having to
remember to update the bar.

**A widget whose document declares a binding is read-only to scripts.** Writing
to it is refused, and the message names the binding, because the fix is to write
*that* instead. A widget with no binding is the script's to write.

## The theme

A tab beside Properties. It edits `resources/ui/theme.fth`, the one small file
every screen reads: font, default size, text and muted text, panel colour /
texture / slice, the four button states, bar background and fill, and an accent.

Only what differs from the compiled-in default is written, so the file is a list
of decisions rather than a dump. Deleting a line puts that colour back; deleting
the file puts all of them back.

Edits show in the preview immediately. **Revert** puts the file's version back.

The font is a `.ttf` in `resources/fonts/`. Leave it blank and every label
renders in the built-in bitmap face, deliberately crude, so a missing font reads
as missing rather than as a rendering bug.

## Scripts on screens and widgets

A screen and a widget carry the same tags, attributes, script and params any
level object does.

| Event | Posted with | |
| --- | --- | --- |
| `:click:` | `src ref` | a button, toggle or slider was activated |
| `:hover:` / `:unhover:` | `src ref` | the pointer or navigation highlight entered / left |
| `:change:` | `src ref, value` | a slider, toggle or field's value changed |
| `:submit:` | `src ref, text str` | Enter in a text field |
| `:cancel:` | — | Escape, while this screen is the topmost modal one |
| `:shown:` / `:hidden:` | — | on and off the stack |

A UI event goes to the widget, or to its **screen** when the widget carries no
script of its own. One level of bubbling — so one handler on a screen can serve
every button on it, and `src.id` says which was pressed.

```
show_screen("pause")
hide_screen(@pause)
widget(screen("pause"), "ok")
focus(widget(@pause, "resume"))
```

## Which screens a level puts up

The **UI screens** block in the level properties panel lists the screens the
level shows when play starts. A menu a script opens later is not listed there —
`show_screen` names it directly.

In a release build, `game.yaml`'s `start_screen` is where the main menu belongs.
A menu is a property of the game, not of the beach it happens to be drawn over.

## Video

A `video` widget shows a clip from `resources/video/` — an AVI of motion-JPEG
frames, or an animated GIF. Encode with:

```
ffmpeg -i in.mp4 -c:v mjpeg -q:v 3 -c:a pcm_s16le out.avi
```

The widget carries the clip, autoplay, loop, mute and volume. The UI editor gives
you a clip picker and a transport that previews it in place.

Its soundtrack plays on the music bus, so the player's music slider applies.

Transport is properties rather than verbs — whether a film is playing is a fact
about the widget:

```
@intro.film.prop.playing = true    -- on a finished clip, start again
@intro.film.prop.time = 12.5       -- seek, in seconds
@intro.film.prop.loop = true
@intro.film.prop.volume = 0.6
@intro.film.prop.video = "outro"   -- a different clip, from its start
```

`prop.time` and `prop.duration` are readable, which is what a progress bar binds
to. A clip that reaches its end posts `:finished:`; a looping one never does.

Two things that catch people out:

- **A hidden video keeps playing.** A HUD element faded out for a second and a
  background loop behind a menu both expect to come back where they would have
  been. `playing = false` is how to stop one.
- Its background is black and its fill mode is **fit**, because a widget's rect
  matching the film's aspect ratio is a coincidence and white bars read as a
  rendering fault.
