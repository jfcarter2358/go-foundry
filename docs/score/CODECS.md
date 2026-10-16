# The OGG Vorbis and MP3 encoders

The Score editor exports audio as WAV, FLAC, OGG Vorbis and MP3
(File > Export, `foundry score export-audio`, `foundry score export`). WAV
and FLAC are the editor's own code (`internal/score/io/audioenc`: a FLAC
encoder with fixed and LPC predictors and Rice coding, checked by decoding
everything it writes). OGG Vorbis and MP3 are the reference encoders:

| Library | Files (Windows / Linux) | Licence | Source |
| --- | --- | --- | --- |
| libogg 1.3.6 | `libogg-0.dll` / `libogg.so.0` | BSD-3-Clause (Xiph.Org) | https://xiph.org/downloads/ |
| libvorbis + libvorbisenc 1.3.7 | `libvorbis-0.dll`, `libvorbisenc-2.dll` / `libvorbis.so.0`, `libvorbisenc.so.2` | BSD-3-Clause (Xiph.Org) | https://xiph.org/downloads/ |
| LAME 3.100 (libmp3lame) | `libmp3lame-0.dll` / `libmp3lame.so.0` | GNU LGPL 2 | https://lame.sourceforge.io/ |

## Loaded, never linked

`internal/score/io/audioenc/dl.go` opens the libraries the first time an
export needs them — `dlopen` on Linux (and macOS), `LoadLibrary` on Windows
— and calls them through function pointers looked up by name. Nothing links
against them:

- an editor without them runs; WAV and FLAC still export, and the Export
  dialog says which library an OGG or MP3 export needs and how to get it
  (`audioenc.MissingLibraryError`), before anything renders;
- the release game never contains them: only the editor and `foundry score`
  import `audioenc`, both development-only, and
  `internal/layering_test.go` (`TestTheReleaseLinksOnlyScorePlayback`)
  fails a release build that links it;
- the LGPL's terms for a program that uses an LGPL library without being
  under the LGPL itself are met by using LAME as a separate, unmodified
  shared library the user can replace with any build of their own (same
  file name, same folder), with its licence and where its source is
  shipped beside it.

They are looked for, in order: the folder `$FOUNDRY_CODECS` names, the
`codecs/` folder beside the editor's executable, the executable's own
folder, then wherever the system keeps libraries (on Windows the DLL search
path; the library's own dependencies are found beside it). On Windows the
editor loads `libogg`, `libvorbis`, then `libvorbisenc`, so each finds the
one before. `foundry score codecs` prints where each was found, or why not.

LAME must be 3.99.5 or later (the float input the encoder uses); anything a
current distribution or MSYS2 ships is.

## How they ship with the editor

**Windows.** `task dist-editor` (via `task dist-editor-windows`) depends on
`task codecs-windows`, which runs `packaging/codecs/fetch-windows.sh`: it
downloads MSYS2's mingw64 packages of the three libraries at the pinned
versions, takes the four DLLs out, and fetches each project's licence from
upstream, into `build/codecs/windows-amd64/` (`CODECS_WINDOWS` to change
it). `foundrypack editor -codecs <dir>` copies them into the install's
`codecs/` with a `README.txt` (what they are, their licences, where their
source is, that they may be replaced) and refuses an install where a DLL
imports anything that is neither beside it nor part of Windows — the MSYS2
builds import only each other, `kernel32.dll` and `msvcrt.dll`. The CI
workflow (`.github/workflows/release-editor.yml`) installs `zstd` to
unpack the packages.

**Linux.** The tarball carries none by default: every desktop distribution
ships these libraries (Debian and Ubuntu: `libvorbisenc2`, `libmp3lame0`),
usually installed already, and the system's are what the editor finds. To
carry them anyway, put the `.so` files (and their licences) in a folder and
pass `task dist-editor CODECS_LINUX=<dir>`; they go into `codecs/` the same
way.

## Checking

```
foundry score codecs                                  # where each library came from
FOUNDRY_REQUIRE_CODECS=1 go test ./internal/score/io/audioenc/
FOUNDRY_CODECS_WINDOWS=build/codecs/windows-amd64 go test -run Codecs ./internal/pack/
foundry score export-audio score_demo -format ogg -o /tmp/demo.ogg -verify
```

The audioenc tests decode every OGG with `github.com/jfreymuth/oggvorbis`
and every MP3 with `github.com/hajimehoshi/go-mp3` (not the encoders' own
decoders) and compare them with what went in; they load the libraries from
a `codecs/` folder of copies to check the search order, and see that an
editor with none says so. Without `FOUNDRY_REQUIRE_CODECS` a machine
without the libraries skips the OGG and MP3 tests rather than failing them.
