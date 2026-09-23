# UI screens

The runtime UI is native — a shipped game draws its own main menu, HUD, pause
screen and loading screen. The same pass draws the UI editor's canvas, so what
you see while authoring is what ships.

A screen is a `.fui` in `resources/ui/`. Editor strip: **UI**.

## The window

Three columns: the hierarchy on the left, the canvas in the middle, the
properties on the right.

```
File  Edit  View  Window | <editor strip>                                <screen name> [Saved]
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

A texture a screen names — a widget's own, a state's, a bar's fill — is loaded
when the screen goes up, whether the level put it up or a script's
`show_screen` did. It is ready by the frame after, so an image shown on the
same tick it is loaded may draw as its plain colour for that one frame.

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

**Pauses the game** freezes the world but leaves scripts running, so a level's
`:tick:` handlers and timers carry on behind the screen. To stop those too, the
screen's script sets `@level.prop.paused` on `:shown:` and clears it on
`:hidden:`, which is what the shipped pause menu does. Screens and widgets keep
running under it. See *Pausing* in SCRIPTING.md.

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
level object does. In the UI editor they are the **Scripting** group near the
bottom of the properties panel: select a widget for its own, or the root for the
screen's.

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

### Pinning a widget to the world

`world_anchor` takes a point in the world, and from then on the widget rides on
it: every frame its pivot is put where that point is on screen, and while the
point is behind the camera the widget and everything under it are hidden. Its
size is still the one it was authored with; only where it is changes. Writing
`null` puts it back where the layout has it. It is how a prompt floats over
something the player can use, or a name over a squadmate:

```
p = widget("hud", "interact_prompt")
p.prop.text = format("Press {} to interact", binding_name("interact", 1))
p.prop.world_anchor = @crate.prop.position + <0.0, 0.6, 0.0>
p.prop.visible = true
```

Author the widget with its pivot where it should touch the point — bottom
centre (0.5, 1) for a label that sits above it. The anchor is runtime state, like
a script's writes to `text` or `alpha`: it is never saved, and the UI editor
shows the widget where it was authored.

### A compass

A compass is a tape across the top of the screen with marks that slide along
it as the player turns. Author the tape as a panel with `clip` on, and each
mark — a letter, a tick, an objective's diamond — as a child of it, pivot at
its centre (0.5 on X). Then pin each mark to a bearing:

- `compass_heading` — a fixed bearing, in radians, in an entity's yaw
  convention: 0 is -Z ("north"), `pi/2` is -X, `pi` is +Z, `-pi/2` is +X.
- `compass_target` — a point in the world; the bearing is the direction from
  the viewer to it, so it swings as the player walks past.

The tape's width stands for `compass_span` radians of heading (half a turn,
`pi`, unless set), centred on where the camera looks. A mark whose bearing is
off the tape's ends is hidden — or, with `compass_clamp` on, held at the nearer
end, which is what an objective wants: the player can still see which way to
turn. Only the horizontal position is the pin's; the mark's size and height on
the tape are what was authored. Writing `null` to either bearing unpins it.

```
hud = screen("hud")
n = widget(hud, "compass_n")
n.prop.compass_heading = 0.0
m = widget(hud, "compass_obj_1")
m.prop.compass_clamp = true
m.prop.compass_target = @command_point_a.prop.position
m.prop.visible = true
```

Like `world_anchor`, a pin is runtime state: never saved, and the UI editor
shows the marks where they were authored. A script that puts markers up keeps
them up by writing them again — on `:spawn:` for the cardinal points, and every
so often for anything whose target moves or whose label (a distance) changes.

### A settings screen

A slider that declares a binding shows the level's number and cannot be
dragged, so a settings slider is left unbound: its script copies the setting in
on `:shown:` (`widget(@self, "sens").prop.value = @level.prop.mouse_sensitivity`)
and writes it back on `:change:`. The engine persists the mouse settings, the
key bindings, the volume (`@level.prop.master_volume`) and Smooth Lighting
(`@level.prop.smooth_lighting`, a toggle, saved to `graphics.yaml`) itself — see
[Entities](11-entities.md#mouse-look-and-the-players-settings) — so the screen
needs no Save button, and leaving with Escape keeps what was set.

Sub-sections are panels. Put each tab's widgets inside a panel and show one at a
time with `.prop.visible`; a hidden panel takes its buttons out of reach of the
pointer and the arrow keys alike.

### Rebinding keys

Every gameplay action has two binding slots, 1 and 2. A button for one calls
`bind_key`, and the player's next key or mouse button press becomes the binding:

```
on_click(:click:, src ref) when src.id == "jump_1"
    if bind_key("jump", 1)
        src.prop.text = "Press a key"
    end

def on_bound(:bound:, src ref, action str, slot int) -> null
    widget(@self, "jump_1").prop.text = binding_name("jump", 1)
end
```

The press is taken before the UI or the game sees it, so the key being bound
does not also act. Escape cancels and Backspace clears the slot; `F1`–`F12`,
`~`, `0`, `8` and `9` belong to the engine and are ignored. `:bound:` arrives
however it ended, at the screen or widget that called `bind_key`. A key bound
to one action comes off any other action live at the same moment, so redraw
every label, not just the one clicked. A vehicle's keys and the on-foot ones
never clash, so `Q` can be both next weapon and roll left. `reset_bindings()`
puts everything back.

The actions are `move_forward` `move_back` `move_left` `move_right` `jump`
`look_up` `look_down` `look_left` `look_right` `sprint` `crouch` `interact`
`primary_fire` `secondary_fire` `reload` `throw` `next_weapon` `prev_weapon`
`weapon_1`–`weapon_5` `roll_left` `roll_right` `pitch_up` `pitch_down`.

### The game's own settings

Anything a settings screen offers beyond the mouse, the keys and the volume is
the game's to define, and `setting`/`set_setting` keep it for every level and
session in `settings.yaml`, beside `controls.yaml`:

```
on_click(:click:, src ref) when src.id == "hud_left"
    set_setting("hud_corner", "left")

-- anywhere, any time — the HUD's own script, say:
corner = setting("hud_corner", "right")
```

The default is returned when nothing has been saved, and is also the
setting's type: a str, int, float or bool. A saved value of another kind is
ignored with a warning. Like the mouse settings, the file is written once the
change has settled, so a slider dragged end to end is one write.

## Which screens a level puts up

The **UI screens** block in the level properties panel lists the screens the
level shows when play starts. A menu a script opens later is not listed there —
`show_screen` names it directly.

A main menu is no different: list it on the level that should open with it, or
show it from a script. Nothing puts a screen up game-wide, so a game can start on
an intro level and bring the menu up in the one after it.

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
