# Shipping a game

Foundry builds two ways from one source tree.

A **development build** is the engine: it boots into the editor on a blank level,
writes beside its own executable, and has every editor in it.

A **release build** is a game: no editor, no debug windows, boots into the level
its manifest names, and writes the player's settings to the per-user data
directory.

```
make release-linux        # build, stage, tar.gz
make release-windows      # cross-build, stage
make installer-linux      # the above, plus a .deb
make installer-windows    # the above, plus a Setup.exe
make release-check        # prove the editor is not in the binary
make test-release         # the suite, in the release configuration
```

Those targets build the engine's own `resources/`. A game kept in a project
directory of its own is built from the editor running there — see
[From the editor](#from-the-editor).

`docs/RELEASE.md` is the full reference. This is what a designer needs from it.

## From the editor

The toolbar's **Release** button opens a window that builds a release of the
project the editor is running from: the directory its binary is in, whose
`resources/` is where it has been reading and saving everything all along.
Nothing about it needs the Foundry source.

For each platform ticked, it does what `make release-*` does:

1. compiles every level — a leak fails the build unless **Allow leaking
   levels** is on;
2. stages an install tree in `build/install-<platform>`: the game binary, named
   after `title:`, with the content the game reaches in `resources/` beside it;
3. archives it — `<game>-<version>-linux-amd64.tar.gz`, with `install.sh`
   inside, or `<game>-<version>-windows-amd64.zip`;
4. builds the installer: a `<game>-<version>-setup.exe` for Windows, always, and
   a `.deb` for Linux wherever `dpkg-deb` is installed (the tarball's
   `install.sh` covers everywhere else).

The files it made are listed above the output, each with a Copy button. The
build runs as a separate process, so keep editing while it goes. Close the
window if you like: the Release button stays amber until the build finishes.
**Cancel** stops it outright.

**It builds what is saved on disk.** Save the level first.

### The game binary

A release is the engine's *release* build — the one with no editor in it — with
your content in `resources/` beside it. The window looks for one per platform in
`runtimes/`, beside the editor:

```
my-game/
  foundry                                     the editor
  resources/
    game.yaml
    levels/  textures/  scripts/  ...
  runtimes/foundry-release-linux-amd64
  runtimes/foundry-release-windows-amd64.exe
  runtimes/foundry-setup-windows-amd64.exe    the Windows installer
```

Content lives only under `resources/`; the editor's own files — `runtimes/`,
`release.yaml`, `imgui.ini`, `foundry.log` — stay beside the binary. Start the
editor in a directory with no `resources/` and it makes one: every missing
content folder, empty, and a default `game.yaml`. The Foundry repository is laid
out the same way, so `go build ./cmd/foundry` at its root gives an editor that
works on the repository's own `resources/`.

Build them from the Foundry checkout the editor came from:

```
make runtimes RUNTIMES_DIR=/path/to/my-game/runtimes
```

`make linux` and `make windows` build their own platform's alongside the editor.

**Rebuild them whenever you rebuild the editor.** The editor compiles the levels
and the game binary loads them, and a game refuses a level compiled by a
different version of the compiler. The build warns when the two binaries come
from different commits. A development build copied into `runtimes/` by mistake
is refused outright, because it would ship the editor.

Or put a Foundry checkout in **Source**, and the game binary is built from it on
the spot with `go build -tags release`. That needs Go on the machine, and
cross-building needs a C compiler for the other platform (mingw, for Windows
from Linux).

### Settings, and building from a terminal

The window's settings are kept in `release.yaml` beside the editor (not in
`resources/`), rewritten each time you press Build. It is never shipped. The
same build runs with no editor open:

```
./foundry build-release                                  # what the window last did
./foundry build-release -platforms linux,windows
./foundry build-release -h                               # every flag
```

Nothing the editor writes beside itself ships: `imgui.ini`, `foundry.log`,
`release.yaml`, any `*.bak`, and the editor binary itself are all left out.

## `game.yaml`

One file, `resources/game.yaml`, and the entire difference between "the engine"
and "this particular game". A release build reads it; a development build ignores
it completely.

```yaml
title: My Game            # required — window caption, installer, data directory
version: 1.0.0            # appended to the window caption
data_dir: MyGame          # optional — overrides the data directory's name
start_level: intro        # required — a level name, resolved under levels/
window:
  width: 1280
  height: 720
  fullscreen: false
```

Either build writes a default one on start if there is none: `title: Untitled
Game`, `version: 0.1.0` and `start_level: untitled` — the name the editor saves a
new, unnamed level under.

**There is no start screen.** A main menu is put up by the level that lists it
in its UI screens, or by a script, so a game can open on an intro level and bring
the menu up in the one after it — and a development build shows it the same way.

Set `data_dir` when the title contains punctuation, or when it may change between
releases: renaming the title should not silently orphan everyone's settings.

## Where a shipped game writes

Nowhere near itself. Assets resolve against the `resources/` directory beside
the executable, so an install is a binary with its content in a folder next to
it. Writes go to:

| Platform | |
| --- | --- |
| Windows | `%APPDATA%\<data dir>` |
| macOS | `~/Library/Application Support/<data dir>` |
| Linux | `$XDG_DATA_HOME/<data dir>`, else `~/.local/share/<data dir>` |

Today that is `foundry.log` and `audio.yaml`. That is what lets the game be
installed into `Program Files` or `/opt` and still be played by someone who
cannot write there.

You can *ship* an `audio.yaml` to launch at a chosen mix, by putting one at
`resources/audio.yaml`; the player's own copy takes over the first time they
move a slider.

## Levels are compiled, not copied

A shipped game reads **only compiled levels** (`.fbsp`). It has no CSG, no BSP
builder, no visibility flood and no light baker in it, so it could not make one
if it wanted to — and reading the authored `.yaml` would mean shipping a level
that had never been compiled and quietly performing like it.

`make release-*` therefore runs the compiler first:

```
make compile-levels        # or it happens as part of make release-linux
```

Compiling is `-strict` there: a **leaking** level fails the build rather than
shipping with its culling switched off. See
[Compiling levels](23-compiling-levels.md), which is also where to look when it
does fail.

Two consequences worth knowing:

- **A level you edited and did not recompile ships as it was.** The `.fbsp` is
  what gets packed; the editor's Compile button and `make compile-levels` are
  the two ways to bring it up to date.
- **The authored `.yaml` is not shipped** once a `.fbsp` exists beside it. The
  binary cannot read it, and it is a level's authoring data.

Play in the editor is still uncompiled — it evaluates the brushes live, every
frame, so what you see is the level rather than the last compile of it. That
means the shipped build is the *faster* of the two, and that a leak is
something to check for with the Compile button rather than to discover at
package time.

## Packing and the prune

`make release-*` copies only the content the game can reach from its manifest.
A project's `resources/` accumulates — half-finished levels, textures nothing
samples any more — and none of it should have to be deleted by hand to not ship.

`PRUNE=0 make release-linux` copies everything instead.

The prune scans each reachable file for tokens that name other assets, and
follows them to a fixpoint. It errs on the safe side: a level with the word
"grass" in a comment pulls in `grass.png`, and the pack is one texture larger.

**The one thing it cannot see is a name a script assembles at runtime** —
`load_level("level_" + n)`. Either pack with `PRUNE=0`, or make sure the names
appear literally somewhere the walk reaches.

Per-directory prune counts print on every pack. Read them and confirm nothing
live was dropped.

`shaders/` and `fonts/` are always copied whole.

## Installers

**Windows** is a `Setup.exe` — a per-user install into
`%LOCALAPPDATA%\Programs`, no administrator and no UAC prompt. Start Menu entry,
optional desktop shortcut, Add/Remove Programs entry, uninstaller. Uninstalling
offers to remove saves and defaults to keeping them.

A release built from the editor makes it with Foundry's own installer, which
needs nothing installed on the machine doing the building; `make
installer-windows` makes the same install with NSIS. It is not code-signed, so
the first time a player runs it Windows SmartScreen says "Windows protected your
PC" — **More info → Run anyway**. `Setup.exe -silent` installs without asking
anything.

Shipping an update is just handing players the new `Setup.exe`: it finds the
existing install, replaces it in place, and keeps their saves. They do not
uninstall first. They do have to close the game first — Windows locks the
running `.exe`, and the installer will say so.

**Linux** is a `.deb` (tree under `/opt/<slug>`, a launcher on `PATH`, a desktop
entry), plus an `install.sh` inside the tarball for everywhere `dpkg` is not
(`PREFIX=~/.local` by default, no root; `--uninstall` reverses it).

Both replace an existing install wholesale rather than merging, so an asset
renamed between releases cannot linger and keep being found.

## Checklist

1. `resources/game.yaml` names the real `start_level`, not the default
   `untitled`.
2. `make compile-levels` — every level compiles, and none of them leaks.
3. `make release-check` — the editor is out of the binary.
4. `make test-release` — the suite passes in the release configuration.
5. `make release-linux` / `make release-windows`, or the editor's **Release**
   window; read the prune counts.
6. Run the staged tree from a directory that is **not** the repository. Confirm
   the log lands in the user data directory and nothing is written into the
   install.
7. `make installer-linux` / `make installer-windows`.
