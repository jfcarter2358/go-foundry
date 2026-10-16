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

The frame section times each pass on the CPU and the GPU. When the GPU is the
slower of the two, the CPU waits for it in one row, `gpu wait`, after `swap`. A
large `gpu wait` means the GPU column is the one to read: look there for the pass
to cut, not at the CPU times.

## Engine switches

These are engine-wide, not level data, and they do not save with the level. They
exist so "this entity is not solid" can be told apart from "collision is off" in
one keypress.

| Key | |
| --- | --- |
| `8` | entity-vs-world collision |
| `9` | entity-vs-entity collision |
| `0` | free-fly (the jump and sprint keys move the body up and down) |
| `F10` | anti-aliasing (in TAA: the jitter and history; see [Anti-aliasing](#anti-aliasing)) |
| `F11` | fullscreen |
| `F12` | vsync |

The same three collision switches are checkboxes in the **Engine** group of the
editor settings, which also says when one is off — the first thing to check when
something that should be solid is not.

Shadow cost switches (`F5`–`F9`) are covered in [Scatter](06-scatter.md) and
[Lighting](07-lighting.md).

## Anti-aliasing

The game draws with TAA (temporal anti-aliasing) unless the player picked
otherwise (`anti_aliasing` in `graphics.yaml`, `@level.prop.anti_aliasing`):
the world is drawn into a target of the renderer's own from a camera nudged a
fraction of a pixel each frame, and each frame is blended into a history of the
ones before it, along each pixel's motion. A still view converges on a
supersampled picture over eight frames or so; a cutout's edge (foliage, a
cutout sprite) is dithered frame to frame and the history averages it into a
soft edge. `"msaa"` is the fallback, the window's own multisampling as before
— a game started in TAA has no samples in its window, so MSAA takes effect at
its next start — and `"off"` is neither. `taa_sharpen` (0 to 1) sharpens the
TAA picture back up.

**Render scale.** Under TAA the world can be drawn smaller than the screen and
reconstructed to it by the TAA resolve, which then gathers each output pixel
from the jittered samples of several frames (Unreal's TSR does the same for its
Nanite scenes). `render_scale` (0.5 to 1, default 1: off) is the share of the
width and height drawn; the HUD and every screen are always drawn at full size.
`dynamic_resolution: true` moves the scale every frame, between
`dynamic_resolution_min` (default 0.5) and `render_scale`, to hold the world's
GPU time at `dynamic_resolution_target_ms` (default 15, 60 fps with room to
spare). All four are in `graphics.yaml`, the script properties of the same
names and the console's `render_scale` command; `FOUNDRY_RENDER_SCALE` and
`FOUNDRY_DYNAMIC_RES=1` set them for a run, and `FOUNDRY_RENDER_SCALE_PATH=0`
turns the path off. The measurements are in
[docs/adventure/PHASE_F.md](../adventure/PHASE_F.md) ("Phase F2").

In a TAA frame, `F10` turns the jitter and the history off and shows the frame
as drawn, one sample a pixel; in MSAA it switches the multisampling, as it
always has. `FOUNDRY_AA=taa|msaa|off` sets the mode for a run whatever the
player saved. The editor's own 3D views always draw the MSAA way; **Play** draws
the game's way.

Cooked meshes and compiled levels are shaded through a **visibility buffer** in
a TAA frame: the GPU cull draws which triangle is nearest at each pixel, and a
material pass shades each pixel once from that, the same light model and
shadows as everything else. The `F3` overlay then says `Nanite shade:
visibility buffer` with how many materials it shaded; the profiler's rows are
`nanite: vis`, `nanite: material`, `taa: motion`, `taa: resolve` and
`taa: output`. `FOUNDRY_NANITE_VISBUFFER=0` shades them as they are drawn
instead, and `FOUNDRY_TAA_RESOLVE=0` keeps the TAA frame's target but shows it
unresolved — the pair the renderer's picture tests compare.

Where the driver has 64-bit shader atomics (NVIDIA's GL does), the clusters
of cooked meshes that are small on screen are drawn by a **compute
rasteriser** into that visibility buffer instead of by the hardware, which
is slow on triangles of a pixel or two. The overlay says `Nanite raster:
compute (64-bit atomics) for clusters under 32 px`, and the profiler adds
`nanite: sw raster` and `nanite: sw resolve` under `nanite: vis`.
`FOUNDRY_NANITE_SWRASTER=0` leaves every triangle to the hardware; `=1` turns
it on even without 64-bit atomics, in a slower two-pass form (for drivers such
as WSL's that lack them); `FOUNDRY_NANITE_SWRASTER_PX=n` sets the largest
cluster, in pixels across, it takes.

The sun's **virtual shadow map** (see [Lighting](07-lighting.md)) adds a
`Sun shadow: virtual` line to the overlay — pages resident, drawn this frame
and in how many rectangles, and any the page pool had no room for — and the
profiler rows `vsm` (under `shadows: sun`) and `vsm: mark`.
`FOUNDRY_NANITE_VSM=0` gives the sun its three cascades back;
`FOUNDRY_NANITE_VSM_PAGES=n` sizes the page pool (2048 pages of 64 KB by
default) and `FOUNDRY_NANITE_VSM_RES=r` its resolution, in texels a screen
pixel across (1 by default; 0.5 needs a quarter of the pages). On WSL's GL
(Mesa's D3D12 driver) a frame that draws shadow pages waits for the GPU
afterwards, to step round a driver hang (`FOUNDRY_NANITE_VSM_FINISH=0`
turns that off, `=1` on everywhere).

A headless shot (`foundry play`'s `shot`) draws the frame sixteen times at the
same instant before taking it, so the picture is TAA's converged one; a
virtual shadow map has its pages by then.

### Nanite foliage

Terrain trees, shrubs, rocks and bushes are drawn as **Nanite foliage** (see
[Terrain](30-terrain.md)): parts and voxels chosen per plant by
screen size, bent on bones. The `F3` overlay adds a `Foliage: Nanite` line —
how many plant clusters and references the last frame drew, how many voxel
bricks, and whether the bricks small on screen go to a **compute
rasteriser** (`compute raster (64-bit atomics)`, NVIDIA's GL) or are
ray-marched as boxes (WSL's GL and other drivers without 64-bit atomics). The
profiler rows, under `foliage`: `foliage: bones`, `foliage: nanite cull`,
`foliage: nanite draw`, `foliage: nanite voxels` with `foliage: voxel raster`
and `foliage: voxel resolve`; the bones and the cull show again under
`shadows: sun`.

| Switch | |
| --- | --- |
| `FOUNDRY_NANITE_FOLIAGE=0` | the older far meshes and impostors instead |
| `FOUNDRY_NANITE_FOLIAGE_GROUND=1` | grass, flowers and the rest of the ground cover on the Nanite path too (costlier: a meadow of one-pixel voxels) |
| `FOUNDRY_NANITE_FOLIAGE_PX=n` | the level of detail's error, in pixels (default 1) |
| `FOUNDRY_NANITE_FOLIAGE_SWRASTER=0` | ray-march every voxel brick, even where the compute rasteriser could run |
| `FOUNDRY_NANITE_FOLIAGE_GI=1` | put the tree crowns into live GI, so a canopy shades the ground under it (off by default: a forest comes out noticeably darker) |
| `FOUNDRY_FOLIAGE_CACHE=off` | build the Nanite foliage every launch instead of reading it from the cache; any other value is the cache's folder |
| `FOUNDRY_NANITE_FOLIAGE_FLUSH=0` / `=1` | on WSL's GL (Mesa's D3D12 driver) the Nanite foliage submits the GPU's queued work between its shadow and camera passes, to step round a driver hang; `0` turns that off, `1` on everywhere |

## Live GI

[Live GI](07-lighting.md#live-gi) adds two rows to the `F3` overlay: its tier,
how many sources its copy of the level was built from and how many probes it
traced, then how many bricks of that copy it rebuilt this frame and how many
are still waiting. A large "waiting" count that does not shrink means something
is changing every frame, such as a moving entity or a moving frame. The
profiler (`F3`, the frame section) times it as `gi: field` and `gi: probes`.

The developer console, and **View > Live GI** in the editor, can show what GI
sees:

| `gi_debug` | |
| --- | --- |
| `lighting` | every surface drawn white, so only the light is left |
| `probes` | each probe as a dot of its own light; red where it is buried in a wall |
| `field` | the distance field GI traces against, from the camera |
| `off` | back to normal |

`water_quality low|medium|high` sets the player's water tier (the F3 profiler's
`water: copy` and `water` rows are its cost). `gi_quality off|low|medium|high` sets the tier. Light leaking through a wall
shows as lit probes on the dark side in the `probes` view, and as a gap in
the wall in the `field` view.

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
ID or three numbers), `spawn`, `destroy`, `find`, `entities`, `god`, `fly`,
`gravity`, `noclip` and `level`. `fly` and `gravity` are separate: `gravity off`
is weightlessness, `fly on` is a camera you steer, and `noclip` is `fly` plus no
world collision. Tab completes against the level you are actually in, so `tp #` and
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
`task release-linux` is how to see one.

## Headless: the engine without a window

Some questions are not worth twenty minutes of playing. The development binary
answers them from a terminal, running the same engine — the same loader, the same
frame, the same script runtime — with nothing drawn.

```
foundry check level1                     # does it load, and do the scripts' ids exist?
foundry ai check                         # do the decision trees compile?
foundry sim -level level1 -seconds 30 \
    -watch 'aa_turret.rotation.y,player.position'
foundry play run -level level1 -seconds 4 \
    -script 'look 90 -10; forward 3; use; shot door.png'
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
usable from a generator's own build step. It also prints **warnings**, which do
not fail it: a prop whose [collision](14-scenery-brush.md#nanite-collision) is
worth a second look (hulls cut short by the hull cap, hulls of a mesh with open
edges, a solid prop that collides as nothing), one line per mesh.

**`ai check`** is the same question asked of a decision tree (`.fai`): does it
compile, does every link point at a node, does every field parse, is anything
drawn that nothing reaches. Naming a script as well —
`foundry ai check soldier -script scripts/enemy_combat.flang` — also checks that
every `call` node names a function that script defines, which is the one mistake
a document cannot see by itself. `-v` prints every tree and node; `foundry ai
list` just says which messages each document answers.

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

### Playing it

`sim` runs the level; `play` *plays* it. The difference is a player: a script of
moves — look here, walk forward three seconds, press use, take a picture — spent
through the same frame, with a screenshot of what the player can see at the end
of it. It is the answer to "does this actually work", which no watch list has
ever quite been.

```
foundry play commands                    # the vocabulary
foundry play run -level level1 -script 'look 180; forward 2; use'
```

The commands are one per line or separated by `;`, and a comment is `//` (not
`#`, which is a tag):

| | |
| --- | --- |
| `look <yaw> [pitch]`, `turn`, `look_at <x,y,z\|id\|#tag>` | where the view points, in degrees |
| `forward [s]`, `back`, `left`, `right`, `stop` | walk, for so many seconds |
| `to <x,y,z\|id\|#tag> [s]` | face a place and walk to it, giving up when blocked |
| `crouch on\|off`, `sprint on\|off`, `jump` | the held actions |
| `fire [s]`, `aim on\|off`, `reload`, `throw`, `weapon <n>` | the gun |
| `use`, `click <widget>` | interact, and a button on a screen that is up |
| `cursor <x,y>\|center\|off`, `tap [button]`, `drag <x,y> [s] [button]` | free the pointer at a window pixel, click the world there, drag it somewhere |
| `wait <s>`, `say <text>`, `read <path>`, `shot [name.png]` | the run itself |
| `view normal\|lighting\|probes\|field\|wireframe\|clusters\|lod\|overdraw` | the debug view the shots after it are drawn in |
| `teleport`, `enable`, `wake #tag`, `kill #tag` | skipping the parts a short run cannot reach |

Every one of them goes through the engine's own front door — the same
`hooks.Script*Request` a key raises, the same `sim.Input` a held key fills — so a
scripted shot is fired by the code that fires a clicked one. `forward 2` is a
real walk into whatever is in the way.

A run ends with the player's situation in words: where they stand, what they are
holding, what the crosshair is on, which screens are up and what they say, and
what is near them with the distance, the bearing and whether it can actually be
seen from here.

**A session that stays.** A single run cannot decide what to do next from what
it just saw. A session can:

```
foundry play serve -level level1 &            # keeps ticking, at wall-clock pace
foundry play do 'to 12,2,-30; look_at #enemy; fire 0.5'
foundry play state
foundry play shot lit.png
foundry play stop
```

`do` plays a script in the running session and answers with what happened, the
state and a screenshot. Between commands the level keeps running, so a fight
does not stand still while the next command is being decided.

Both take `-draw=false` to play blind (no display, no GL), `-size WxH`,
`-shots <dir>`, `-seconds n` to let a level's own opening play out before the
script starts, and the `sim` flags for `-watch`, `-enable`, `-god`, `-compiled`
and `-menus`.

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
the real UI and viewmodel passes and so need a display and a GL 4.3 driver.

The world itself is drawn by `foundry play` (`shot`), through `render.DrawWorld`
— the same call `app.Run` makes, so a headless picture is the frame the game
would have drawn, HUD and viewmodel included, minus the editor's own markers.
`-gi off|low|medium|high` fixes the live GI tier. Without it the tier is picked
by GPU, as a new player's is, so the same script can draw differently on two
machines. `-water low|medium|high` fixes the water quality the same way (high by
default; see [Materials](28-materials.md#water)).

`-view` draws the pictures in one of the editor's debug views, and a script's
`view` command switches mid-run, so one script can photograph the same spot both
ways:

```
foundry play run -level level1 -script 'look 90 -10; shot lit; view lighting; shot light-only'
```

| view | what it draws |
| --- | --- |
| `normal` | the frame a player sees |
| `lighting` | the light alone, every surface white: where it is dark, where a shadow lands, what a light reaches. Works with GI off too |
| `probes`, `field` | live GI's probes and its distance field — nothing while GI is off, so pair them with `-gi medium` |
| `wireframe` | the geometry as lines, the filled passes (scatter, sky, decals, volumetrics, sprites) left out |
| `clusters` | what the Nanite cull draws (cooked meshes, compiled surfaces, chunk brushes), each ~128-triangle cluster its own colour; everything else as normal |
| `lod` | the same, coloured by the level of detail each cluster comes from: white is the full detail, then blue, cyan, green, yellow, orange, red, magenta for each coarser level |
| `overdraw` | every fragment those draws rasterise, added up with no depth test: dark where a pixel is covered once, bright where clusters pile up |

### From a test

All of it is `internal/harness`, so a repro that used to be a scratch `main` can
be a test that stays:

```go
s, err := harness.Open(harness.Options{Project: dir, Level: "level1"})
s.Teleport(mgl32.Vec3{12, 2, -380})
s.RunFor(5)
if s.Read("ground_breach.enabled") != "false" { ... }

// And the same script a terminal plays, with the same pictures:
s, err := harness.Open(harness.Options{Project: dir, Level: "level1", Draw: true})
defer s.Close()
shots, err := s.Play("look 90; forward 3; use; shot door.png", 30)
```

A drawing session pins the goroutine that opened it — a GL context is current on
one thread — so everything it is asked to do has to happen on that goroutine.

## Tests

```
task test          # everything
task vet
task release-check # proves the editor is compiled out of a release build
```
