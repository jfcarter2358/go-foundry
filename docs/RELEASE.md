# Shipping a game

Foundry builds two ways from one source tree. A **development build** is the
engine: it boots into the 3D editor on a blank level, writes beside its own
executable, and has every editor and sub-editor in it. A **release build** is a
game: no editor, no imgui, no secondary windows, boots into the level its
manifest names, and writes the player's settings and saves to the per-user data
directory.

```
make release-linux        # build, stage, tar.gz
make release-windows      # cross-build, stage
make installer-linux      # the above, plus a .deb
make installer-windows    # the above, plus a Setup.exe
make release-check        # prove the editor is not in the binary
make test-release         # the test suite, in the release configuration
```

---

## 1. `game.yaml` — what makes it a game

A release build reads one file, `game.yaml`, from beside its executable. It is
the entire difference between "the engine" and "this particular game".

```yaml
title: My Game            # required — window caption, installer, data directory
version: 1.0.0            # appended to the window caption
data_dir: MyGame          # optional — overrides the data directory's name
start_level: intro        # required — a level *name*, resolved under levels/
start_screen: main_menu   # optional — a UI screen put up over it at boot
window:
  width: 1280
  height: 720
  fullscreen: false
```

A missing or malformed manifest is fatal in a release build, with the reason in
the log: there is no editor to open a level from and no blank level worth
showing, so a broken manifest means a broken install and the useful thing to do
is say which file and why. A development build ignores the manifest entirely.

`data_dir` is worth setting when the title contains punctuation, or when it may
change between releases — renaming the title should not silently orphan
everyone's settings.

Parsed by `internal/game`; the release boot that acts on it is
`internal/app/startup_release.go`.

## 2. Where a shipped game writes

Nowhere near itself. Every *asset* path resolves against the executable's
directory (`fileutil.ExecutablePath`), which is why an install is just a binary
with the content beside it. Every *write* goes through `fileutil.StatePath`,
which in a release build points at:

| Platform | Directory |
|---|---|
| Windows | `%APPDATA%\<data dir>` |
| macOS | `~/Library/Application Support/<data dir>` |
| Linux | `$XDG_DATA_HOME/<data dir>`, else `~/.local/share/<data dir>` |

Today that is `foundry.log` and `audio.yaml`; saves belong there too when they
exist. This is what lets the game be installed into `Program Files` or `/opt`
and still be played by a user who cannot write there.

Reads go through `fileutil.ReadState`, which falls back to a copy beside the
executable — so a project can *ship* an `audio.yaml` to launch at a chosen mix,
and the player's own copy takes over the first time they move a slider.

In a development build `UserDataRoot` is empty and both of these resolve beside
the executable, exactly as they always did. The editor's save-next-to-the-exe
workflow is untouched.

## 3. The editor seam

`internal/editor`, `internal/textureui`, `internal/imguibackend` and
`internal/uiwin` are imported from exactly one file in the engine:
`internal/app/devtools_dev.go`. Everything in `internal/app` that needs them
goes through thin wrappers — `devActive()`, `devTick()`, `devRender()` — and
`internal/app/devtools_release.go` answers the same set with stubs.

`devActive()` is a constant `false` in the release half, so every `if
devActive()` in the frame loop compiles away rather than becoming a branch.

The two files are a pair that must agree, and nothing in the type system makes
them. `make release-check` is that check:

```
$ make release-check
release build's forbidden imports:
  none — editor, textureui, imguibackend, uiwin and imgui are all absent
```

Worth running in CI. It is what catches a new call site in the frame loop that
reached for `editor.` directly instead of adding a wrapper.

### Adding a call site

If you need something from the editor in `internal/app`, add a wrapper to
*both* devtools files rather than importing the editor where you need it.
Keep them in the same order so they can be read side by side.

### What else is tagged

| File | Why |
|---|---|
| `internal/app/keybindings.go` | The F1 reference describes the editor |
| `internal/app/script_reference.go` | Drawn with imgui into the F1 overlay |
| `internal/render/debug_overlay.go` | The F3 overlay is imgui |

`internal/render/debug.go` — the counters behind that overlay — is *not*
tagged. They cost almost nothing and a crash report is better with them in it.

## 4. Packing — `cmd/foundrypack`

Stages a built binary and the content it can reach into an install tree.

```
go run ./cmd/foundrypack -goos linux -exe build/foundry-release -out build/install-linux -prune
```

Two modes:

- **without `-prune`** — copy the content tree wholesale. What `make linux` has
  always done, and the right answer while a project is being built.
- **with `-prune`** — copy only what the game can reach from its manifest. A
  repository's `resources/` accumulates: half-finished levels, textures nothing
  samples any more. None of it should ship, and none of it should have to be
  deleted by hand to not ship.

`make release-*` prunes by default. `PRUNE=0 make release-linux` does not.

### How the prune decides

It does not parse the formats. Every authored format in the engine is YAML or
flang source, and between them they name assets a dozen ways — a texture by
bare name, a model by install-relative path, a script with or without its
extension, a level a volume exits into, a level a script loads from inside a
string. Teaching the packer each shape would be a second copy of the engine's
loaders, wrong the first time a field is added.

So it builds an index of every name any asset could be referred to by, scans
each reachable file for tokens, and follows the ones that hit the index —
transitively, to a fixpoint. Roots are the manifest's start level and start
screen, plus four the engine names from Go and a walk would never see:
`ui/theme.fth`, `ui/loading.fui`, `ui/pause.fui`, and `textures/default.png`.

That is a conservative approximation in the safe direction. A level with the
word "grass" in a comment pulls in `grass.png`: the pack is one texture larger.

**The one thing it cannot see** is a name a script assembles at runtime —
`load_level("level_" + n)`. No static analysis would. Pack with `PRUNE=0`, or
make sure the names appear literally somewhere the walk reaches. The
per-directory prune counts are printed on every pack, and `-v` names every file
dropped.

`shaders/`, `fonts/` and `detail_presets/` are always copied whole: their
contents are named from Go, or scanned from the directory at startup, so there
is nothing for a content walk to find.

### The stamp

Every pack writes `.foundrypack` into the install tree — title, version,
executable name, data directory — in a form a shell script can source. The
three installers downstream read it rather than each re-deriving those facts
from the manifest with their own half-copy of the rules.

## 5. Installers

### Windows — `packaging/windows/installer.nsi`

NSIS, because it cross-packages from Linux (`apt install nsis`) using the same
WSL toolchain that already cross-compiles the binary with mingw. Inno Setup
makes a better-looking installer and needs Wine to build from here.

A **per-user** install into `$LOCALAPPDATA\Programs` — no administrator, no UAC
prompt. Nothing is lost by that: a release build never writes into its install
directory. Produces a Start Menu entry, an optional desktop shortcut, an
Add/Remove Programs entry and an uninstaller. Uninstalling offers to remove the
player's saves and defaults to keeping them.

Everything game-specific arrives as a `/D` define from the Makefile.

### Linux — `packaging/linux/`

- `make-deb.sh` builds a `.deb`: the tree under `/opt/<slug>`, a launcher on
  `PATH`, a desktop entry. Depends on `libsdl2-2.0-0` and `libgl1`, which are
  dynamically linked against the system's own — a distribution's SDL2 is the one
  that knows about its display server.
- `install.sh` ships *inside* the tarball for everywhere `dpkg` is not. Same
  layout under a prefix: `PREFIX=~/.local` by default, no root needed.
  `./install.sh --uninstall` reverses it.

Both replace an existing install wholesale rather than merging, so an asset
renamed between releases cannot linger and keep being found.

## 6. Checklist for a real release

1. `game.yaml` names the real `start_level` and `start_screen`.
2. `make release-check` — the editor is out.
3. `make test-release` — the suite passes in the release configuration.
4. `make release-linux` / `make release-windows`; read the prune counts and
   confirm nothing live was dropped.
5. Run the staged tree from a directory that is *not* the repository, and
   confirm the log lands in the user data directory and nothing is written into
   the install.
6. `make installer-linux` / `make installer-windows`.
