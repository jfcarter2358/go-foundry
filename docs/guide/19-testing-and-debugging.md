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

**Window > Log**, from any editor.

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

Read the FPS against the vsync line under it. With vsync on, a frame that costs
4ms and a frame that costs 16ms both report 60 — the number is the monitor's,
not the scene's. `F12` turns the wait off, and the frame rate that comes back is
the one you can actually compare between two versions of a level. Nothing else
caps the loop, so expect it to run hot while it is off.

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
| `F12` | vsync |

The same three collision switches are checkboxes in the **Engine** group of the
editor settings, which also says when one is off — the first thing to check when
something that should be solid is not.

Shadow cost switches (`F5`–`F9`) are covered in [Scatter](06-scatter.md) and
[Lighting](07-lighting.md).

## Debugging the shipped build

Everything above is the editor. A release build has none of it — until you start
it with:

```
FOUNDRY_DEBUG_ENABLED=true ./my-game
```

Then `F3` draws the same debug overlay, and `~` — the key that opens the editor
here — opens a **developer console** instead: the engine and script log,
scrolling live, with a prompt under it.

`help` lists the commands. The ones you will reach for are `tp` (to a tag, an
ID or three numbers), `spawn`, `destroy`, `find`, `entities`, `god`, `noclip`
and `level`. Tab completes against the level you are actually in, so `tp #` and
Tab lists the tags that exist.

`repl` turns the prompt into flang, and `> <line>` runs one line without
switching. It is the same language, bound to the same running level, so a thing
you would have typed into the script editor's Run button is a thing you can type
here:

```
] > distance(@player, @objective_marker)
41.83
] > @dropship.prop.velocity = <0.0, 4.0, 0.0>
```

The console owns the keyboard while it is up — the player will not walk while
you type — but the world keeps running behind it. `pause` stops it when you want
it stopped.

Without the variable set, both keys do nothing, so a player never finds any of
this. See [Shipping a game](20-shipping.md) and `docs/RELEASE.md`.

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

**A scatter volume is bare.** The slope range excludes the ground under it, the
draw range is shorter than where you are standing, there is no solid brush face
beneath it to plant on, or the chunk holding that face is disabled.

**A texture is missing.** Its name has no matching `.png` in `textures/`. The log
names the texture and the object keeps drawing what it had.

**You walk straight through a ship's ramp without boarding.** The doorway volume
is shallower than one step, or it is disabled, or it is not riding the vehicle.
See [Vehicles and frames](12-vehicles-and-frames.md).

**A shipped build draws no menu.** A development build never reads `game.yaml`.
`make release-linux` is how to see one.

## Headless: the engine without a window

Some questions are not worth twenty minutes of playing. The development binary
answers them from a terminal, running the same engine — the same loader, the same
frame, the same script runtime — with nothing drawn.

```
foundry check level1                     # does it load, and do the scripts' ids exist?
foundry sim -level level1 -seconds 30 \
    -watch 'aa_turret.rotation.y,player.position'
foundry render screen hud -o hud.png     # a .fui through the real UI renderer
foundry render model models/marine.fmdl -pose walk:0.25 -o walk.png
foundry model inspect models/marine.fmdl -pose walk:0.25 -bone foot.L,foot.R
foundry profile-load -level level1 -compiled
```

Run them from the project directory, or point `-root` at it.

**`check`** loads the level and lints every script *against it*, so `@id` and
`#tag` are resolved for real — it is what catches a script naming something that
is not there, and a script writing a property the engine will refuse. Every
`.fui` is loaded too. It exits non-zero on any problem, which is what makes it
usable from a generator's own build step.

**`sim`** runs the frame. The whole frame: chunks and brushes that carry a
velocity, vehicles, the player's own move, the AI controllers, the bodies they
steer, the frames, the waypoint followers, the particles, the effects, the UI
tick and the script tick — in the engine's order, because it *is* the engine's
frame (`internal/sim`, which `app.Run` calls too). Agents walk, bodies fall,
triggers fire. `-scripts-only` asks for the old cheaper mode, which runs the
script half and nothing that moves a body.

| | |
| --- | --- |
| `-watch a,b,c` | print these when they change |
| `-at 'x,y,z@t;...'` | teleport the player there at that second |
| `-walk-to 'x,y,z@t'` | *walk* there, on foot, through real collision |
| `-press t` | press interact |
| `-enable id,...` | switch a chunk or volume on before the run |
| `-wake tag@t` / `-kill tag@t` | wake an AI controller / kill everything with a tag |
| `-god` | hold the player's health up so a run is not ended by one rocket |
| `-restore-at t` | save, reload the level, restore, and report what did not come back |
| `-compiled` | load the `.fbsp`, as a release build does |
| `-menus hold` | leave a modal screen up instead of clicking its default |
| `-cpuprofile f` | a CPU profile of the run |

A watch is `<id>.<field>`, `<entity>/<brush>.<field>`, `<entity>#<attachment>.<field>`,
`<screen>:<widget>.<field>` for a widget on a screen that is up, or
`player.position`. Fields are the ones machinery is made of: position, rotation,
scale, velocity, enabled, visible, `attr.<name>`, and an agent's `ai_mode`,
`ai_target`, `ai_waypoints`.

### Scenarios

A run worth keeping goes in a file, with what should be true during it:

```yaml
level: level1
seconds: 45
watch: [player.position, ground_breach.enabled]
steps:
  - at: 0
    enable: [ground]
  - at: 2
    teleport: [12, 2, -380]
  - at: 3
    press: true
  - at: 20
    expect:
      - path: ground_breach.enabled
        equals: "false"
      - path: player.position
        near: [12, 2, -360]
        within: 5
```

`foundry sim -scenario breach.yaml` plays it and exits non-zero if an
expectation did not hold. The same file runs from a Go test
(`harness.Scenario.Run`), which is what stops a fixed bug coming back.

### Pictures

`render model` needs no GL at all and draws the mesh posed with the engine's own
pose maths — `-pose clip:seconds`, `-view front|back|left|right|top|iso`,
`-frame cx,cy,cz,span` to zoom in on a helmet, `-texture` to check a repaint
landed where the geometry is. `render screen` and `render viewmodel` draw through
the real UI and viewmodel passes and so need a display and a GL 4.1 driver.

There is no `render world` yet: the frame's *draw* half is still spelled out
inline in `app.Run` the way its simulation half was before `internal/sim`, and a
headless world view would be a second copy of it.

### From a test

All of it is `internal/harness`, so a repro that used to be a scratch `main` can
be a test that stays:

```go
s, err := harness.Open(harness.Options{Project: dir, Level: "level1"})
s.Teleport(mgl32.Vec3{12, 2, -380})
s.RunFor(5)
if s.Read("ground_breach.enabled") != "false" { ... }
```

## Tests

```
make test          # everything
make vet
make release-check # proves the editor is compiled out of a release build
```
