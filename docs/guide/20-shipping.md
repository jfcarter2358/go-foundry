# Shipping a game

Foundry builds two ways from one source tree.

A **development build** is the engine: it boots into the editor on a blank level,
writes beside its own executable, and has every editor in it.

A **release build** is a game: no editor, no debug windows, boots into the level
its manifest names, and writes the player's settings to the per-user data
directory.

```
task release-linux        # build, stage, tar.gz
task release-windows      # cross-build, stage
task installer-linux      # the above, plus a .deb
task installer-windows    # the above, plus a Setup.exe
task release-check        # prove the editor is not in the binary
task test-release         # the suite, in the release configuration
```

Those tasks build the engine's own `resources/`. A game kept in a project
directory of its own is built from the editor running there — see
[From the editor](#from-the-editor).

`docs/RELEASE.md` is the full reference. This is what a designer needs from it.

## From the editor

**Build > Release...** (or **Window > Release**) opens a window that builds a release of the
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
task runtimes RUNTIMES_DIR=/path/to/my-game/runtimes
```

`task linux` and `task windows` build their own platform's alongside the editor.

**Rebuild them whenever you rebuild the editor.** The editor compiles the levels
and the game binary loads them, and a game refuses a level compiled by a
different version of the compiler. The build warns when the two binaries come
from different commits. A development build copied into `runtimes/` by mistake
is refused outright, because it would ship the editor.

Or put a Foundry checkout in **Source**, and the game binary is built from it on
the spot with `go build -tags release`. That needs Go on the machine, and
cross-building needs a C compiler for the other platform (mingw, for Windows
from Linux).

### Keeping something out of the release

A script can ask which build it is in:

```flang
widget(@self, "tab_game").prop.visible = @level.prop.dev_build
```

`@level.prop.dev_build` is read-only, true in the editor's build — including
its play mode — and false in a shipped game. It is for the settings and the
screens that exist to make a level rather than to play it: a difficulty the
tester can change in the middle of a mission, a debug view, a level-skip
button. The player's build simply does not show them.

It is not a security boundary. Content that must not ship at all belongs
outside `resources/`, or behind the [prune](#packing-and-the-prune).

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
task compile-levels        # or it happens as part of task release-linux
```

Compiling is `-strict` there: a **leaking** level fails the build rather than
shipping with its culling switched off. See
[Compiling levels](23-compiling-levels.md), which is also where to look when it
does fail.

Two consequences worth knowing:

- **A level you edited and did not recompile ships as it was.** The `.fbsp` is
  what gets packed; the editor's Compile button and `task compile-levels` are
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

`PRUNE=0 task release-linux` copies everything instead.

The prune scans each reachable file for tokens that name other assets, and
follows them to a fixpoint. It errs on the safe side: a level with the word
"grass" in a comment pulls in `grass.png`, and the pack is one texture larger.

A name a script assembles at runtime is followed when its fixed part is
written down: `format("ammo_{}", n)` reaches every `ammo_*` asset,
`"pickup_" + kind` every `pickup_*`, and `format("{}_{}_icon", w, c)` every
`*_icon`. A pattern needs at least three fixed characters, so `format("{}_{}")`
reaches nothing. **What it still cannot see is a name built some other way** —
from a variable holding the whole prefix, say. Either pack with `PRUNE=0`, or
make sure the names appear literally somewhere the walk reaches.

Per-directory prune counts print on every pack. Read them and confirm nothing
live was dropped.

`shaders/` and `fonts/` are always copied whole.

### Finding unused assets

The same walk, turned round, lists what you could delete. **Window > Unused
Assets** scans `resources/` and lists every file nothing refers to, grouped by
directory with its size. For a file that something does mention, it names what
mentions it: a texture "named only by unused models/old.fmdl" goes when that
model goes.

- **Count every level as used** (on by default) keeps levels the start level
  never leads to, and everything they use, out of the list. Test maps stay put.
  Turn it off to see exactly what a pruned release leaves out.
- Tick files, or use **Select shown** (after typing a filter) or **Select
  backups** (the `*.bak` copies the editor leaves beside what it saves). Then
  **Delete**. You confirm the list first, and a deleted file cannot be got back
  unless `resources/` is under git (**Window > Git**).
- `textures/palette.pal` (the texture editor's palette) and directory READMEs
  are never listed. Nor are `shaders/` and `fonts/`.

From a terminal, `foundry find-unused` prints the same report and deletes
nothing. Add `-from-start` to count only what the start level leads to, and
`-root <project>` to scan a project other than the one beside the binary or in
the working directory.

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

## Giving the editor to your team

The editor is distributed on its own, as a Foundry release, and opens projects
from wherever people keep them — typically a clone of the game's repository.
From a Foundry checkout (on Linux, with the mingw cross-compiler):

```
task dist-editor
gh release create v1.4.0 build/editor-dist/*
```

`task dist-editor` builds three files into `build/editor-dist/`, versioned from
`git describe`:

| File | What |
|---|---|
| `foundry-editor-<v>-windows-amd64-setup.exe` | per-user install into `%LOCALAPPDATA%\Programs\Foundry Editor`, no admin; Start Menu, Add/Remove Programs |
| `foundry-editor-<v>-windows-amd64.zip` | the same, portable |
| `foundry-editor-<v>-linux-amd64.tar.gz` | needs `libsdl2` from the distribution |

Each carries the shaders it was built from and a `runtimes/` folder (the release
game binaries for both platforms and the Windows installer program), so the
Release window can ship a game with nothing else installed. Installing a newer
Setup.exe replaces the editor in place. Nothing of anybody's is in there to lose,
because projects live elsewhere.

**Which project it opens:**

1. `foundry -project <folder>`, or a folder dropped onto the program;
2. the editor's own folder, if it holds `resources/game.yaml` — a checkout, or
   the old layout with `foundry.exe` copied into a game's folder, both still
   work;
3. the project opened last;
4. otherwise a new one at `Documents/Foundry Projects/Untitled`.

**File → Open Project...** (with a recent list) and **New Project...** restart
the editor on the chosen folder, after warning about anything unsaved. Pick one
from Recent — a double-click opens it outright — or type the path, or hit
**Browse...** for a folder browser that tags every folder already holding a
project. A new project can be made in an empty folder, or a fresh clone holding
only `.git`.
The editor's log and settings (`foundry.log`, `release.yaml`, `graphics.yaml`
and so on) are written in the project folder, as before; the recent list is kept
in the per-user `Foundry Editor` data folder.

**Shaders belong to the editor.** They have to match the binary, so an installed
editor reads its own `resources/shaders` instead of the project's. A release
built from it ships those shaders too. A project opened that way does not need
a `shaders/` folder, and one is not created. **Runtimes** are looked for in the
project's `runtimes/` first, so a project can pin its own, and then in the
editor's.

## Debugging the built game

A release build has no editor. Start it with `FOUNDRY_DEBUG_ENABLED=true` in the
environment and it gains two keys: `F3` for the debug overlay, and `~` for a
developer console — a live log, a command prompt (`tp`, `spawn`, `destroy`,
`find`, `god`, `noclip`, `level`, …) and a flang prompt over the running level.
Without the variable, neither key does anything, so a player never sees it.

[Testing and debugging](19-testing-and-debugging.md) has the details.

## Checklist

1. `resources/game.yaml` names the real `start_level`, not the default
   `untitled`.
2. `task compile-levels` — every level compiles, and none of them leaks.
3. `task release-check` — the editor is out of the binary.
4. `task test-release` — the suite passes in the release configuration.
5. `task release-linux` / `task release-windows`, or the editor's **Release**
   window; read the prune counts. **Window > Unused Assets** lists what the
   prune left out, if you'd rather delete it than carry it.
6. Run the staged tree from a directory that is **not** the repository. Confirm
   the log lands in the user data directory and nothing is written into the
   install.
7. `task installer-linux` / `task installer-windows`.
