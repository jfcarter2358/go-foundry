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

A release build reads one file, `game.yaml`, from the `resources/` directory
beside its executable. It is the entire difference between "the engine" and
"this particular game".

```yaml
title: My Game            # required — window caption, installer, data directory
version: 1.0.0            # appended to the window caption
data_dir: MyGame          # optional — overrides the data directory's name
start_level: intro        # required — a level *name*, resolved under levels/
window:
  width: 1280
  height: 720
  fullscreen: false
```

On start, in either build (but not in `foundry build-release`), any of the
content directories `entities`, `fonts`, `levels`, `models`, `particles`,
`scripts`, `shaders`, `sounds`, `textures`, `ui` and `video` missing under
`resources/` is created empty, and a missing `resources/game.yaml` is written
with defaults: `title: Untitled Game`, `version: 0.1.0`, `start_level: untitled`
— the name the editor saves a new, unnamed level under. Failing to create them,
in a read-only install directory say, is a warning in the log rather than a
failure.

A manifest that still cannot be loaded — malformed, or missing because it could
not be written — is fatal in a release build, with the reason in the log: there
is no editor to open a level from and no blank level worth showing, so a broken
manifest means a broken install and the useful thing to do is say which file
and why. A development build ignores the manifest entirely.

`data_dir` is worth setting when the title contains punctuation, or when it may
change between releases — renaming the title should not silently orphan
everyone's settings.

Parsed by `internal/game`; the release boot that acts on it is
`internal/app/startup_release.go`.

## 2. Where a shipped game writes

Nowhere near itself. Every *asset* path resolves against
`fileutil.ResourcesPath` — the `resources/` directory beside the executable
(`fileutil.ExecutablePath`) — which is why an install is just a binary with its
content in a folder beside it. A stored install-relative path, such as an
entity's `models/crate.fmdl`, is relative to `resources/`. Every *write* goes
through `fileutil.StatePath`, which in a release build points at:

| Platform | Directory |
|---|---|
| Windows | `%APPDATA%\<data dir>` |
| macOS | `~/Library/Application Support/<data dir>` |
| Linux | `$XDG_DATA_HOME/<data dir>`, else `~/.local/share/<data dir>` |

Today that is `foundry.log` and `audio.yaml`; saves belong there too when they
exist. This is what lets the game be installed into `Program Files` or `/opt`
and still be played by a user who cannot write there.

Reads go through `fileutil.ReadState`, which falls back to a shipped copy in
`resources/` — so a project can *ship* an `audio.yaml` to launch at a chosen mix
by putting it at `resources/audio.yaml`, and the player's own copy takes over
the first time they move a slider.

In a development build `UserDataRoot` is empty and both of these resolve beside
the executable, exactly as they always did: the editor's own state (`imgui.ini`,
`foundry.log`, `release.yaml`, `audio.yaml`) sits next to the binary and next to
`resources/`, not inside it.

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

## 4. Packing — `internal/pack`

Stages a built binary and the content it can reach into an install tree.
`cmd/foundrypack` is its command line; the editor's Release window runs it
through `internal/release` (section 5).

```
go run ./cmd/foundrypack -goos linux -exe build/foundry-release -out build/install-linux -prune
```

`-src` is the content directory (default `resources`), and `-manifest` defaults
to the `game.yaml` in it. The install tree it stages has the same shape as a
project:

```
install-linux/
  my-game                  the executable, named after title:
  SDL2.dll                 (Windows) platform runtime libraries
  .foundrypack             the stamp, below
  install.sh
  resources/
    game.yaml
    levels/  textures/  scripts/  ...
```

Everything from the content directory lands under `resources/`: the content
directories, and any loose file at the top of it — `game.yaml`, a shipped
`audio.yaml`, a LICENSE. Platform runtime libraries are the exception. They are
taken from the top of the content directory and also from the directory holding
it, which is where a project keeps `SDL2.dll` beside its Windows editor, and
they always land beside the executable, because that is where the OS loader
looks for them.

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
transitively, to a fixpoint. Roots are the manifest's start level, plus four
the engine names from Go and a walk would never see:
`ui/theme.fth`, `ui/loading.fui`, `ui/pause.fui`, and `textures/default.png`.

That is a conservative approximation in the safe direction. A level with the
word "grass" in a comment pulls in `grass.png`: the pack is one texture larger.

**The one thing it cannot see** is a name a script assembles at runtime —
`load_level("level_" + n)`. No static analysis would. Pack with `PRUNE=0`, or
make sure the names appear literally somewhere the walk reaches. The
per-directory prune counts are printed on every pack, and `-v` names every file
dropped.

`shaders/` and `fonts/` are always copied whole: their
contents are named from Go, or scanned from the directory at startup, so there
is nothing for a content walk to find.

### The stamp

Every pack writes `.foundrypack` into the install tree — title, version,
executable name, data directory — in a form a shell script can source. The
three installers downstream read it rather than each re-deriving those facts
from the manifest with their own half-copy of the rules.

### What a pack refuses

It empties its output directory first, so it refuses one that would take
something with it: a filesystem root, the home directory, a repository (anything
with a `.git`), the content directory being packed or any directory above it,
and anything inside a directory that is itself packed. An existing directory is
only emptied when it looks like an install tree: it has a `.foundrypack` stamp,
a `resources/game.yaml`, or — for a tree packed before content moved under
`resources/` — a top-level `game.yaml`. A project directory has a
`resources/game.yaml` in it too, so without the rule about directories above
the content, pointing the output at the project would have emptied it.

It also leaves out what a development build writes beside itself, should any of
it be in the content directory: `imgui.ini`, `foundry.log`, `release.yaml`,
every `*.bak`, and any loose file that is a native program (ELF, PE or Mach-O by
magic number) — the editor's own binary above all. In a project those sit beside
the executable, outside `resources/`, so normally there is nothing there to
leave out.

## 5. From the editor — `internal/release`

The editor's **Build > Release...** menu item builds a release of the project the editor has
open: `fileutil.ProjectPath`, the directory holding the `resources/`
(`fileutil.ResourcesPath`) it already reads and saves every asset in. So a game
can live in a repository of its own, with the editor binary beside its
`resources/`, and be built with no Foundry checkout in sight. The engine's own
repository root is a project in the same sense: `go build ./cmd/foundry` there
gives an editor that runs against the repository's `resources/`. The designer's
view of it is in `docs/guide/20-shipping.md`.

`release.Build` is the pipeline: compile every level (`release.CompileLevels`,
shared with `cmd/foundrycompile`), get a game binary per platform, `pack.Pack`,
then `pack.TarGz` (with `install.sh`, embedded from `packaging/`) or `pack.Zip`,
and optionally `pack.Deb` / `pack.NSIS` using the embedded scripts. Everything
checkable without compiling — the manifest, the output directory, whether a game
binary exists — is checked before compiling.

`foundry build-release -root` is the project directory, the one holding
`resources/`, and `-out` is relative to it. An output directory over the project
itself, or inside one of the content directories under its `resources/`, is
refused.

### It runs in a child process

The dev binary doubles as the builder: `foundry build-release [flags]`, handled
by `devRunCommand` at the very top of `app.Run`, before the log file is opened
(which would truncate the running editor's log) and before any window. The
Release window starts that as a child (`release.StartJob`) and draws its output.
Not a goroutine, because compiling loads levels through the engine's loader,
which fills the registries the editor is drawing from; because a crashing build
should not take unsaved work with it; and because Cancel has to stop a `go build`
two processes down (a process group on Unix, `taskkill /T` on Windows).

Output lines starting `==> `, `WARNING: `, `artifact: ` and `FAILED: ` are the
protocol between the two: the window colours by them and lists artifacts from
them. `devRunCommand` is a stub in the release build, so a shipped game has no
tool modes.

### Where the game binary comes from

- **`runtimes/` in the project**, named `foundry-release-<goos>-<goarch>[.exe]`,
  beside `foundry-setup-windows-amd64.exe`, the Windows installer program
  (section 6).
  `make runtimes RUNTIMES_DIR=...` builds them; `make linux` and `make windows`
  build their own platform's. The normal case: a Windows editor has no C
  cross-compiler, and a game repository has no source.
- **A Foundry checkout**, built with `go build -tags release` — when `source:`
  names one, or when the project directory is one or is inside one (the
  repository root itself, or `dist/`, which is what the Makefile does anyway).

Either way `release.CheckPlayer` reads the binary's embedded build info and
refuses anything not built with `-tags release` or built for the wrong GOOS or
GOARCH: a development build would boot as the game and ship the editor. It
warns when the binary's `vcs.revision` differs from the editor's — a warning
rather than a refusal, because the game refuses a `.fbsp` whose
`CompiledVersion` differs, and most commits do not change it.

### `release.yaml`

The window's settings, in the project directory beside the executable (not in
`resources/` with `game.yaml`), written on every Build:

```yaml
platforms: [linux, windows]
output: build          # relative to the project
prune: true
allow_leaks: false
source: ../foundry     # optional
```

`foundry build-release` reads it for every flag not given, so a terminal build
does what the window last did.

## 6. Installers

### Windows, from the editor — `cmd/foundrysetup`

The Release window and `foundry build-release` do not use NSIS. makensis is
almost never installed on the Windows machine an editor runs on, and a release
with no installer in it is not one a player can install. Every Windows build
makes `<game>-<version>-setup.exe` with Foundry's own installer instead:

- `cmd/foundrysetup` is a small pure-Go Windows program: message boxes, the
  registry and the known folders through the standard library's syscalls, and
  shortcuts through the Windows Script Host by way of PowerShell. Pure Go, so it
  cross-compiles with no mingw. `make runtime-windows` puts it in `runtimes/`,
  or it is built from source like the game binary.
- A Setup.exe is that program with a zip of the install tree and a 32-byte
  trailer appended (`internal/setup`). Making one is concatenation, done in Go,
  so any host builds it.
- The same program with nothing appended is the uninstaller. Setup writes one
  into the tree as `uninstall.exe`, which re-runs itself from `%TEMP%` so that it
  can delete its own folder.

It installs exactly as the NSIS script below does — the same folder, the same
`HKCU\Software\<name>` and Uninstall keys, the same Start Menu folder and
optional desktop shortcut, the same stamp-guarded whole-tree replacement on an
upgrade, the same running-game check, and saves kept unless the player asks —
so either installer upgrades and removes what the other put there.
`Setup.exe -silent [-desktop]` and `uninstall.exe -silent [-delete-saves]` (or
NSIS's `/S`) ask nothing, and exit 2 when the game is running.

The uninstaller's copy in `%TEMP%` runs with `%TEMP%` as its working directory:
the Start Menu shortcut starts it in the install folder, and Windows will not
delete a process's working directory.

It is not code-signed, so SmartScreen warns the first time it is run. Signing is
not a drop-in step yet: a signature is appended to the end of the file, which is
where the installer looks for its payload.

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

**Upgrading is the normal path.** Running a newer `Setup.exe` over an existing
install replaces it in place — no uninstall first, and the player's saves and
settings in `%APPDATA%\<data dir>` are untouched. It finds the old install
through `HKCU\Software\<AppName>\InstallDir`, confirms the folder is really
ours by the `.foundrypack` stamp foundrypack left there, and then skips the
Directory page entirely, so a player clicking Next cannot end up with a second
copy in a second folder.

The old tree is deleted whole rather than directory by directory. That is
deliberate: a hand-kept list of directories to clear has to be kept in step with
foundrypack's `contentDirs`, and it was already out of step. Deleting everything
is safe precisely because a release build never writes under the install
directory, and the `.foundrypack` stamp is the guard — without it nothing is
deleted recursively.

The one thing that will stop an upgrade is the game being open, because Windows
locks the running `.exe`. That is checked before any file is touched and named
plainly ("still running — close it, then click Retry") rather than surfacing as
a stock abort/retry/ignore box half way through the copy.

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

### The editor itself — `make dist-editor`

The editor is shipped as a Foundry release of its own. `make dist-editor` builds
the development binary for Windows (mingw, a console program as `make windows`
builds it) and Linux, plus `make runtimes` into `build/editor-runtimes`. Then
`foundrypack editor` (`cmd/foundrypack/editor.go` → `pack.StageEditor`) stages:

```
foundry[.exe]  SDL2.dll  .foundrypack (TITLE='Foundry Editor', KIND='editor')
resources/shaders/      the shaders the binary was built from
runtimes/               release binaries for both platforms + the installer program
```

It then writes a zip and a Setup.exe (Windows) or a tar.gz (Linux) into
`build/editor-dist`. The install holds no project, so the ordinary game
installer rules apply unchanged: a per-user install and a whole-tree upgrade.
The only thing `KIND` changes is wording ("Open it now?").

What makes this work is that the project is no longer the executable's folder:

- `fileutil.ProjectPath` is the project (`resources/`, and where a development
  build writes state: `fileutil.StateDir`). `internal/project` chooses it at
  startup: `-project`, then the executable's folder if it holds
  `resources/game.yaml`, then the recent list, then a new project in Documents.
  A release build's is its install.
- `fileutil.EngineShadersDir` is set when the executable carries
  `resources/shaders` and runs apart from its project. Every shader load goes
  through `fileutil.ShaderPath`/`ShadersDir`, and new ones must too. A release
  pack takes its shaders from there, or from the source checkout it built from
  (`pack.Options.Shaders`).
- `release.PrebuiltPlayer`/`PrebuiltSetupStub` look in the project's `runtimes/`,
  then in the executable's.
- File → Open/New Project sets `editor.PendingProject` and ends the frame loop.
  `app.Run`'s first defer starts the program again with `-project`, because
  every registry was filled from one project at boot.

## 7. Checklist for a real release

1. `resources/game.yaml` names the real `start_level`.
2. `make release-check` — the editor is out.
3. `make test-release` — the suite passes in the release configuration.
4. `make release-linux` / `make release-windows`; read the prune counts and
   confirm nothing live was dropped.
5. Run the staged tree from a directory that is *not* the repository, and
   confirm the log lands in the user data directory and nothing is written into
   the install.
6. `make installer-linux` / `make installer-windows`.
