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

`docs/RELEASE.md` is the full reference. This is what a designer needs from it.

## `game.yaml`

One file beside the executable, and the entire difference between "the engine"
and "this particular game". A release build reads it; a development build ignores
it completely.

```yaml
title: My Game            # required — window caption, installer, data directory
version: 1.0.0            # appended to the window caption
data_dir: MyGame          # optional — overrides the data directory's name
start_level: intro        # required — a level name, resolved under levels/
start_screen: main_menu   # optional — a UI screen put up over it at boot
window:
  width: 1280
  height: 720
  fullscreen: false
```

**The main menu belongs here, not in a level's own screen list.** A menu is a
property of the game, not of the beach it happens to be drawn over, and a level
reached later must not put it back up.

The cost is that a development build — which never reads this file — enters play
with no menu. `make release-linux` is how to see one.

Set `data_dir` when the title contains punctuation, or when it may change between
releases: renaming the title should not silently orphan everyone's settings.

## Where a shipped game writes

Nowhere near itself. Assets resolve against the executable's directory, so an
install is a binary with the content beside it. Writes go to:

| Platform | |
| --- | --- |
| Windows | `%APPDATA%\<data dir>` |
| macOS | `~/Library/Application Support/<data dir>` |
| Linux | `$XDG_DATA_HOME/<data dir>`, else `~/.local/share/<data dir>` |

Today that is `foundry.log` and `audio.yaml`. That is what lets the game be
installed into `Program Files` or `/opt` and still be played by someone who
cannot write there.

You can *ship* an `audio.yaml` to launch at a chosen mix; the player's own copy
takes over the first time they move a slider.

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

**Windows** is an NSIS `Setup.exe` — a per-user install into
`%LOCALAPPDATA%\Programs`, no administrator and no UAC prompt. Start Menu entry,
optional desktop shortcut, Add/Remove Programs entry, uninstaller. Uninstalling
offers to remove saves and defaults to keeping them.

**Linux** is a `.deb` (tree under `/opt/<slug>`, a launcher on `PATH`, a desktop
entry), plus an `install.sh` inside the tarball for everywhere `dpkg` is not
(`PREFIX=~/.local` by default, no root; `--uninstall` reverses it).

Both replace an existing install wholesale rather than merging, so an asset
renamed between releases cannot linger and keep being found.

## Checklist

1. `game.yaml` names the real `start_level` and `start_screen`.
2. `make compile-levels` — every level compiles, and none of them leaks.
3. `make release-check` — the editor is out of the binary.
4. `make test-release` — the suite passes in the release configuration.
5. `make release-linux` / `make release-windows`; read the prune counts.
6. Run the staged tree from a directory that is **not** the repository. Confirm
   the log lands in the user data directory and nothing is written into the
   install.
7. `make installer-linux` / `make installer-windows`.
