# Score editor — implementation spec

The Score editor is a notation-first music editor beside the Tracker. A
musician who knows Sibelius writes in standard notation; it plays back through
the same sample engine the game uses, with a DAW's mixer, effects and
automation under it. It is a **game tool**: a `.fscore` plays in a level the
way a `.ftrk` does.

- Plan (design doc): https://claude.ai/code/artifact/ee8cdd51-d7f9-4d4b-af2c-e28b73ea9ffb
- UI mockups with a working browser prototype: `docs/score/mockups/score-mockups.html`
  (open it in a browser; its JS engraver and input model are a reference
  implementation of the behaviour described here — match it).
- Per-phase notes: `docs/score/PHASE_<n>.md` (what shipped, what was deferred
  and why, how to verify). The next phase reads the previous notes first.

This file is the contract. Every item in it ships by the end of phase 6. If an
item proves impossible, the phase notes say why and what was built instead —
never silently drop one.

## Decisions (settled with John, 2026-10-10)

| Question | Decision |
| --- | --- |
| `.sib` import | Avid never published the format; import goes through **MusicXML** (Sibelius: File > Export > MusicXML). "Export for Sibelius" = MusicXML. |
| Default keymap | **Sibelius** (the musician uses it today). A MuseScore preset ships too; every shortcut is rebindable. |
| Third-party sample libraries | **SFZ and SF2** import → `.finst`. Nothing else. |
| MIDI keyboard | Phase 6. |
| Game tool or general tool | **Game tool.** `.fscore` in levels in phase 2. |
| Instrument library | **Shared** with the Tracker: `.finst` for both; the Tracker gains velocity layers and articulations. |
| MP3 | LAME, loaded at runtime as a separate library by the **editor only** (never the game runtime), LGPL-compliant. OGG and WAV come first. A missing library gives a clear message, not a crash. |

## Requirements (from the musician, all in scope)

1. Notation for every line. Each line = a staff, grand staff or percussion staff, with its own clef, transposition and drum map.
2. Lines point at instrument files and switch on the fly (picker on the staff header; swaps while playing; notes unchanged).
3. Chords in any voice, and up to 4 independent voices per staff.
4. Durations down to 64th by default; 128th and 256th via a preference/second keypad layout. Dots, double dots, nested tuplets. Durations are **exact fractions**.
5. Tempo slider on the transport: scales playback 25%–200% without editing the score; an option writes the value as a tempo mark.
6. Volume per line (mixer fader) and within a line (per-note velocity + volume automation lane).
7. Dynamics in the notation (ppp–fff, sfz, fp, …) and hairpins set the level; a **dynamics lane** under the staff shows the curve they produce; dragging a segment overrides its level without changing the printed mark.
8. Number keys `2`–`9` add a diatonic interval above the selected note(s), spelled in the key; `Shift` adds below.
9. A click on a line or space writes the pitch the key signature implies (F# on the F line in G major). The ghost note shows that spelling before the click.
10. Time signature select: any meter incl. compound and additive (3+2+2/8), changeable at any bar; beaming follows it.
11. Sharp/flat/natural shortcuts; a second press gives double sharp/flat.
12. Export: PDF (score + parts), MIDI, MusicXML (for Sibelius), WAV, OGG, MP3, FLAC, per-line stems, `.ftrk`, PNG/SVG of a selection.
13. Duplicate a line (with notes, or empty with the same instrument and mixer settings).
14. Select and copy whole measures: click a bar = that line; Ctrl+click = every line; Shift extends. Copy, paste, `R` repeat (score grows if needed).
15. Audio effects on one line or the whole file: insert chain per line, two send buses, master chain.
16. Select and loop sections for playback (drag on the ruler, or select bars + `L`).
17. Play back and scan from a time ruler or by clicking the music; view follows playback.
18. Glissandi (straight or wavy line; plays as continuous slide or chromatic/diatonic run, per line setting).
19. Crescendo/decrescendo hairpins and "cresc."/"dim." text with dashes, played as ramps.
20. Import MIDI (quantise + voice separation dialog) and MusicXML.

**Common notation-software baseline (also required):** ties, slurs,
articulations (staccato, staccatissimo, accent, tenuto, marcato), fermatas,
ornaments (trill, mordent, turn), tremolo, grace notes (acciaccatura,
appoggiatura), arpeggio marks; clef changes, transposing instruments with a
concert-pitch toggle, ottava lines, pedal marks; repeats, voltas, D.S./D.C./coda
/fine with playback that follows them; rehearsal marks, tempo text (rit.,
accel., a tempo), lyrics, chord symbols, text; score and parts, title page,
page layout, multi-bar rests in parts, part extraction; transpose, respell,
undo/redo, find; MIDI keyboard step entry and real-time recording; playback
that reads articulations and dynamics, swing and humanise.

## Architecture

```
Score editor views ──edits──▶ .fscore model ──▶ interpreter ──▶ score scheduler ─┐
   ▲  (Score / Timeline / Mixer)     │  ▲                                        │
   └──── engraver (screen + PDF) ◀───┘  └── import/export (MusicXML, MIDI, …)   ▼
Tracker editor ──▶ .ftrk ──▶ tracker.Player ─────────────────────────▶ tracker.Engine ──▶ audio.Generator (music bus)
                                              .finst instruments ─────────────▲
world.Music (level + scripts) drives either scheduler.
```

| Piece | Package | Notes |
| --- | --- | --- |
| Score model | `internal/score` | lines, staves, voices, measures, events, marks, lanes |
| Time | `internal/score` | `Frac` (int64 num/den, always reduced) for positions and durations; 960 ticks per quarter for playback; tempo map, meter map, key map |
| Interpreter | `internal/score/interpret` | score + repeats + dynamics + articulations + lane overrides + tempo map → note events and control curves |
| Engraver | `internal/score/engrave` | layout into positioned glyphs/lines/paths (backend-agnostic display list) |
| Glyphs | `internal/score/glyph` | embedded Bravura (`bravura/Bravura.otf`, `bravura/bravura_metadata.json` = SMuFL engraving defaults + anchors), read with `golang.org/x/image/font/sfnt`; one outline source feeds a screen atlas texture and PDF paths |
| Audio engine | `internal/tracker` | `tracker.Engine` owns voices, strips, buses, lifted out of `Player`; `Player` keeps the pattern sequencer and drives an Engine |
| Score scheduler | `internal/score/play` | drives an Engine at tick resolution; same command/event channel style as `tracker.Player`; implements `audio.Generator` |
| Instruments | `internal/tracker` | `.finst` files in `resources/sounds/instruments/<category>/<name>.finst` |
| Effects | `internal/tracker/fx` | EQ, chorus, phaser, drive, crusher, delay, reverb, compressor, limiter; allocation-free `Process` |
| Editor | `internal/editor/editor_score*.go` | Score, Timeline, Mixer panes in one uiwin window like the Tracker |
| I/O | `internal/score/io/...` | midi, musicxml, pdf, audio encoders, sfz/sf2 |
| CLI | `foundry score …` | wired like `trackercli` (see `internal/app/devtools_dev.go`) |

### The Engine refactor (phase 0, gate for everything)

`renderVoice`, `mixStrip`, `rampControls`, the buses and DSP blocks move from
`tracker.Player` into `tracker.Engine`. The Tracker's sound **must not change**:
existing tracker tests (render tests, `songs_test.go`) pass unchanged and
`BenchmarkPlayer32Channels` stays under ~0.5 ms per 10 ms block. `Render` stays
allocation-free (no allocations on the mixer goroutine — test it with
`testing.AllocsPerRun`).

### Why not compile to tracker patterns

A pattern holds ≤256 rows; exact 64ths + triplets need 48 rows/beat. Gliss
curves, hairpin ramps and lanes want per-sample control. A quantised `.ftrk`
export still exists.

### `.finst`

YAML. Today's `tracker.Instrument` saved on its own (zones/keymap, envelopes,
filter, vibrato, polyphony, interpolation), plus:

- velocity layers per zone with crossfades;
- articulations: named zone sets (`sustain`, `staccato`, `pizzicato`,
  `tremolo`, `muted`, `legato`, …) chosen by notation marks or technique text
  ("pizz.", "arco", "con sord.");
- round robin;
- playback defaults: range, transposition (written vs sounding), default clef,
  gliss style (`slide` | `chromatic` | `diatonic`), staccato length, release.

Samples are still referenced by name from `sounds/samples/<cat>/<name>.wav`
(+ `.fsmp`). The Tracker can name a `.finst` instead of embedding an
instrument (its own embedded instruments keep working). Ship a starter
library generated from the 128 synth recipes plus an orchestral-ish set built
from them (strings, woodwinds, brass, keys, percussion, choir, synth) via a
`foundry tracker gen-instruments` command, like `gen-samples`.

### `.fscore`

YAML at `resources/sounds/<name>.fscore`. Voice content per measure is compact
text (diffable in the Git window, readable by the Claude panel), the way
`.ftrk` stores patterns as tracker text. Sketch:

```yaml
name: hangar_theme
title: Hangar Theme
composer: ""
tempo: [{bar: 1, bpm: 92, text: "Andante"}]
meter: [{bar: 1, sig: 4/4}]           # additive: "3+2+2/8"
key:   [{bar: 1, fifths: -2, mode: major}]
sections: [{name: intro, bar: 1}, {name: theme, bar: 9}]   # = rehearsal marks
params: [{name: intensity, default: 0}]
lines:
  - name: Violin I
    instrument: strings/violin_section
    volume: 0.8
    pan: -0.3
    inserts: [{fx: eq, low: -2}]
    sends: {a: 0.3, b: 0}
    staves:
      - clef: treble
        voices:
          - "| q.Bb4 e.C5 h.D5(p) | q.Eb5 q.D5 h.C5< | ..."
    lanes:
      volume: [{at: 5:1, to: 5:3, value: 0.6}]
buses: {a: {fx: [{fx: reverb, size: 0.7}], return: 0.25}, b: {...}}
master: [{fx: compressor, threshold: -12}]
```

Phase 0 defines the exact voice grammar (durations, dots, tuplets, chords,
ties, rests, grace notes, marks attached to events) with a parser + printer
that round-trip, documented in the guide chapter. Phase-specific additions
(marks, lyrics, chord symbols, layout) extend the grammar compatibly.

## Input model (default = Sibelius keymap)

| Action | Key |
| --- | --- |
| Note input toggle | `N` (ghost notehead follows the pointer, spelled in the key) |
| Letter names | `A`–`G` (nearest octave to previous note, in the key) |
| Interval above / below | `2`–`9` / `Shift+2`–`9` |
| Durations 32nd…whole | Keypad `1`–`6` (laptop `Alt+1`–`6`) |
| 64th, 128th, 256th, breve | `F8` second keypad layout, then keypad `1`–`4` |
| Dot / double dot | Keypad `.` once / twice |
| Rest | Keypad `0` (laptop `Alt+0`) |
| Tie | Keypad `Enter` (laptop `T`) |
| Sharp, flat, natural | `+`, `-`, `0` (keypad `8`, `9`, `7`); again = double |
| Semitone / step in key / octave | `Up/Down`, `Alt+Up/Down`, `Ctrl+Up/Down` |
| Respell | `Return` |
| Voice 1–4 | `Ctrl+Alt+1`–`4` |
| Tuplets | `Ctrl+3`, `Ctrl+5`, `Ctrl+2`–`9` |
| Slur, cresc, dim, gliss | `S`, `H`, `Shift+H`, `G` |
| Dynamic / expression text | `Ctrl+E` |
| Articulations | keypad second layout, or `'` `>` `_` `^` |
| Bar select / all lines / extend | click / `Ctrl`+click / `Shift`+click |
| Copy, paste (or Alt+click), repeat | `Ctrl+C`, `Ctrl+V`, `R` |
| Play/stop, play from selection, loop selection | `Space`, `P`, `L` |
| Command palette | `Ctrl+Shift+P` |
| Undo, redo | `Ctrl+Z`, `Ctrl+Y` |
| Keymap overlay | `F1` |

An on-screen keypad panel mirrors the keypad (mockup screen 7).

## Phases (one commit each, `ENH: Score editor phase N - <title>`)

0. **Foundations** — Engine refactor; `.finst` (+ Tracker support + starter library + `gen-instruments`); score model, voice grammar, Frac time, tempo/meter/key maps; interpreter (basic); score scheduler; offline render; MIDI import/export; MusicXML import (notes, voices, key/time, tempo); `foundry score inspect | play | render` (render → WAV; `play` uses the audio device).
1. **Engraving and entry on one staff** — glyph atlas; engraver: notes, rests, chords, accidentals in the key (bar-scoped), beaming by meter, stems, flags, dots, ties, tuplets, ledger lines, clefs, key and time signatures, multi-voice stem directions and rest offsets; continuous (scroll) view; Score editor window registered in the switcher + save chip + Save All + Window menu; note entry (mouse ghost, letters, intervals, accidentals, durations to 64th + second layout, voices, tuplets, rests, ties); bar selection, copy, paste, repeat; undo/redo; keymaps + rebinding + F1 overlay + keypad panel; `foundry score render -png` snapshot tests.
2. **Many lines and playback (first release to the musician)** — multiple lines; staff headers (instrument picker with search, hot swap while playing, mute, solo, duplicate, volume, meter); grand staff with brace; mixer pane (volume, pan, mute, solo, meters); transport (play/stop, start, loop, follow, position readout, tempo slider); time ruler with bar numbers + time, click-to-seek, drag-to-loop; click a note → playhead; follow playback; WAV export; `.fscore` in levels via `world.Music` (`play_music`, `music:` block), sections from rehearsal marks, `song_section`/`song_param`/mute verbs and `:song_bar:`/`:song_beat:`/`:song_section:`/`:song_marker:`/`:song_end:` events, save games; guide chapter `docs/guide/32-score-editor.md`.
3. **Expression** — voices 1–4 complete; dynamics marks + hairpins + text cresc/dim; dynamics lane with drag-to-override segments; gliss (styles); slurs; articulations → `.finst` articulations + playback length/accent; technique text; repeats, voltas, D.S./D.C./coda/fine playback; tempo text rit./accel./a tempo and tempo marks; grace notes; ornaments; tremolo; fermata; arpeggio; pedal; ottava; clef changes; transposing instruments + concert pitch toggle.
4. **DAW layer** — insert effects per line + master; two send buses; automation lanes (volume, pan, filter cutoff, any fx param) drawn and edited in the timeline; timeline view (tracks, regions, lanes) and an editable piano roll of the selected line, all views over the same model; SFZ and SF2 import → `.finst`; swing and humanise.
5. **Pages and output** — page view and layout (systems, system/page breaks, spacing, title page, staff size, margins); score and parts (part extraction, multi-bar rests in parts); PDF writer (own, vector glyph paths, no font embedding); full MusicXML export and fuller import (marks, dynamics, articulations, repeats, lyrics, layout hints); OGG, MP3 (LAME runtime-loaded, editor only), FLAC; stems; `.ftrk` export (quantised) and import (patterns → notation); PNG/SVG of a selection; export and import dialogs as in the mockups.
6. **Extras** — lyrics; chord symbols; percussion staves + drum maps; MIDI device input (ALSA on Linux, WinMM on Windows) with step entry and real-time recording + quantise; guitar tab; transpose dialog, respell, find; command palette; anything left from earlier phases' "deferred" lists. Finish with a completeness pass against this spec.

## Engineering rules

- **Never discard anything in git:** no `git checkout`/`restore`/`stash`/`reset`
  of files, ever. Undo your own edits by editing back. The tree can hold other
  sessions' uncommitted work.
- **Resource limits (WSL2, 32 GB shared):** one heavy job at a time (a full
  `go test ./...`, the GL suite, a big build or render); prefer targeted
  `go test ./internal/<pkg>/...`; run with `GOFLAGS=-p=6 GOMEMLIMIT=6GiB`
  (export them if missing); put a `timeout` on long commands
  (`timeout 1800 go test ...`); check `free -g` first and wait if under 8 GB
  available. A `memguard` service SIGKILLs the largest go/test process when
  memory runs out (log `~/.cache/memguard.log`) — if a run dies with SIGKILL,
  reduce parallelism and retry.

- **Another session is editing Nanite/render code in this same working tree at
  the same time.** Never `git add -A`, `git add .` or `git commit -a`: stage
  only the files this project created or changed, by explicit path (check
  `git status` and `git diff --stat` before committing, and leave anything
  under Nanite/render/gfx/shaders or other unrelated areas unstaged). Never
  revert, stash, reformat or "fix" changes you did not make. If `go build` or a
  test fails in a package this project did not touch, it is probably that
  session's work in progress: wait a few minutes and retry, and do not count
  it against this phase; note it in the phase notes if it persists.
- That session (procgen/canyon generator) may also add small hunks to files
  this project shares: `internal/editor/editor_menubar.go` (switcher entry),
  `internal/editor/editor_claude_reference.go` (a section at the end),
  `internal/pack/refs.go`, `docs/guide/README.md`, and a `procgen` case in
  `internal/app/devtools_dev.go`'s devRunCommand switch (its scope is in
  `docs/canyon/SPEC.md`, "Ground rules"). When a file you are
  committing contains hunks that are not this project's, stage ONLY this
  project's hunks: write a patch of your hunks (e.g. edit `git diff <file>`
  output down to your hunks) and `git apply --cached` it, then check
  `git diff --cached` shows nothing of theirs (no `procgen`, `editor_gen`,
  canyon, nanite references). Never stage new files outside this project's
  paths.

- New audio DSP is allocation-free on the mixer goroutine; game thread talks to
  players via command chan / event chan / atomics only (see `tracker.Player`).
- Match surrounding code style and comment density; tests beside code.
- Use the headless tools (`foundry check/sim/render/play`, the new `foundry score …`) rather than throwaway harnesses.
- Editor traps already learned (see the Tracker editor code):
  - a hand-hit-tested overlay drawn after an `InvisibleButton` never sees clicks
    if the button covers it (the Tracker header bug) — cover only the area you mean;
  - `imgui.Selectable` inside a modal dismisses it; all combos go through
    `imguibackend.BeginCombo/SelectableV` (type-to-filter);
  - gameplay keys are `platform.Action`s rebound via `bind_key`; editor shortcuts
    go through the existing key-binding settings;
  - the save chip compares content fingerprints (`editor_save_state.go`); add
    the Score editor to `openEditor` lists and Save All;
  - structural edits push undo; level-mutating per-frame work skips while `EditorCompiling()`.
- GL tests that draw through the world program must bind the shadow samplers first.
- Headless runs have no audio device: test scheduling/rendering offline.
- `go build ./...` and `go test ./...` pass at every commit (the release build
  too: `task test-release`). Keep `go vet` clean for new packages.
- The release game runtime must not link editor-only code (LAME loader, editor
  panes); keep score playback (model + interpreter + scheduler) in the runtime.
