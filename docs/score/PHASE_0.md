# Phase 0 — Foundations

Phase 0 built everything under the Score editor except the editor itself:
the shared audio engine, the shared instrument library, the score model and
its file, the interpreter, the scheduler, offline rendering, MIDI import and
export, MusicXML import and the `foundry score` commands. It was built in
three steps (A, B, C, below) and then checked by a verification pass that
fixed what it found (the last sections).

## At a glance

| Package / file | What |
| --- | --- |
| `internal/tracker/engine.go`, `voice.go` | `tracker.Engine`: voices, strips, buses, DSP, sample-accurate cues. `tracker.Player` drives one; the score scheduler drives another. |
| `internal/tracker/finst.go`, `instrument.go` | `.finst` library instruments: zones, velocity layers, round robin, articulations, playback defaults. The Tracker can name one (`ref:`). |
| `internal/tracker/synth/instruments.go`, `variants.go` | The starter library (161 instruments) and its 30 variant samples; `foundry tracker gen-instruments`. |
| `internal/score` | The model, `Frac` time, pitches/intervals/durations, meters, marks, the voice grammar (parser + canonical printer), the `.fscore` file, the library, validation. |
| `internal/score/interpret` | Score → performance: timed notes, bars, beats, sections, markers, curves, the tempo map. |
| `internal/score/play` | The scheduler (`audio.Generator`), `Prepare`/`Program`, offline `Render`, `WriteWAV`, `FitGain`. |
| `internal/score/io/{gm,notate,midi,musicxml}` | General MIDI mapping, timed notes → notation, SMF read/write + import/export, MusicXML read + import. Not linked by the release runtime. |
| `internal/score/scorecli` | `foundry score list / inspect / check / render / play / fmt / import / export-midi`. |
| `resources/sounds/instruments/`, `resources/sounds/samples/orchestral/` | The generated starter library. |
| `resources/sounds/score_demo.fscore` | The example score (golden-hashed). |
| `docs/guide/32-score-editor.md` | The guide chapter: file format, the voice grammar, how a score plays, the commands, importing. Phase 2 adds the editor to it. |

Formats: `.finst` (YAML, version 1), `.fscore` (YAML, version 1, voices as
grammar text), `.ftrk` unchanged at version 1 (a slot may now be a `ref:`).

## Step A: `tracker.Engine` and `.finst` instruments

### What shipped

**`tracker.Engine`** (`internal/tracker/engine.go`, `voice.go`). Voices,
envelopes, voice filters, channel strips (filter, drive, crush, mute fades,
meters), the reverb/delay send buses, the master compressor and the scope
moved out of `Player` into an `Engine`. `Player` keeps the pattern sequencer
(rows, ticks, effect commands, params, bindings, sections, stingers) and
drives its Engine: per block it sets each strip's controls and the values a
channel's note follows (`rampControls`), then `eng.mixBlock`.

The API a sequencer outside the package uses (the score scheduler):

| Call | Does |
| --- | --- |
| `NewEngine(strips)`, `SetRate`, `SetStripCount` | build (allocating; do it off the mixer) |
| `SetBuses(reverb, delay, compressor, gain)`, `SetTempo(bpm)`, `SetInterp` | buses; the delay syncs to the tempo |
| `SetStrip(i, StripControls{Volume, Pan, SendA, SendB, CutoffHz, Resonance, Drive, Crush})`, `SetMute(i, on, secs)` | a line's mixer strip |
| `NoteOn(strip, inst, pitch, NoteOptions) NoteID` | start a note now; pitch is a fractional tracker note (49 = C-4) |
| `NoteOff`, `Kill`, `SetNoteControls(id, vol, bend, pan)`, `Playing`, `ReleaseAll`, `ReleaseStrip` | per note |
| `NewNoteID()` + `Cue(Cue{At: frame, Kind: CueNoteOn/NoteOff/Kill/NoteControl/Strip/Mute/ReleaseAll, ...})` | queue an event at a frame offset into the next `Render`; later frames carry over |
| `Render(out, frames, rate) bool` | render, cut at every cue (sample-accurate); true while anything sounds or waits |
| `StripLevel(i)`, `Scope(dst)`, `LiveVoices()` | atomics for the game thread |

`NoteOptions`: `Velocity` (picks the layer), `Articulation` (1-based,
`Instrument.ArticulationIndex(word)` finds it from a mark or technique
text), `Volume`, `Pan`, `Hold`, `Release` (overrides the fade-out, e.g. from
`Playback.Release`), `Offset`, `Reverse`, `Follow` (Tracker channels only).
Everything on the mixer goroutine; nothing allocates while rendering.

**Proof the Tracker sounds the same.** Golden hashes captured before the
refactor, unchanged after: `internal/tracker/engine_golden_test.go` (six
synthetic renders reaching every effect command, envelope, insert, bus,
preview note, mute/solo/stinger/tempo/pause command) and
`internal/tracker/trackercli/golden_test.go` (every shipped song, whole and
per section). All bit-identical. `TestRenderAllocationFree` covers
`Player.Render` and the cue-driven `Engine.Render` with `AllocsPerRun`.

| Benchmark (10 ms block) | Before | After |
| --- | --- | --- |
| `BenchmarkPlayer32Channels` | 379–385 µs | 379–385 µs |
| `BenchmarkEngine32Notes` (new: 32 layered notes, 32 control cues per block) | — | ~448 µs |

A cue cuts the block, so 32 cues at 32 different frames is 33 sub-blocks;
the scheduler should cue note-ons at their frame but put control changes
(dynamics curves, lanes) on block boundaries where it can.

**`.finst`** (`internal/tracker/finst.go`, `instrument.go`, `ftrk.go`). A
library instrument is `sounds/instruments/<category>/<name>.finst`: the same
YAML as a song's embedded instrument, plus zone `round_robin` and velocity
`layers` (MIDI velocity ranges; overlapping ranges crossfade at equal
power), `articulations` (name, `triggers`, own zones and/or volume
envelope, fade-out, gain) and `playback` (`range`, `transposition` written to
sounding, `clef`, `gliss` slide/chromatic/diatonic, `staccato` fraction,
`release`). Library: `LoadInstrument` (cached, samples resolved),
`ScanInstrumentNames`, `SaveInstrument`, `ReloadInstruments`. Format and
round trip: `ParseInstrument` / `MarshalInstrument`.

**Tracker support.** A song slot can be `{name, ref: strings/violin_section}`;
`ResolveSamples` fills it from the library (missing ones reported by
`tracker check`); `.ftrk` stays version 1 and old files load unchanged. The
volume column is the note's velocity (layer choice). New effect `@xx` picks
articulation xx (typed with the `2` key in the effect column). Editor: the
instrument pane's **Library** combo, a read-only view of a library slot
(articulations with their `@` numbers, layers, round robin, playback
defaults), **Make local copy**, **Save to library**.

**Starter library.** `foundry tracker gen-instruments` (`synth/instruments.go`,
`synth/variants.go`) writes 161 instruments: one per recipe in the recipe's
category, and an orchestral set — `strings/{violin,viola,cello,contrabass}_section`
(sustain in two registers × two velocity layers; staccato, pizzicato,
tremolo, muted), `woodwinds/{flute,oboe,clarinet,bassoon}`,
`brass/{horn,trumpet,trombone,tuba}` (soft/hard layers, muted),
`keys/{grand_piano,celesta,harp,organ}`, `choir/choir` (ah, ooh),
`percussion/{timpani,orchestral_percussion,drum_kit,xylophone,glockenspiel,marimba,vibraphone}`
(GM keys; snare and hat in round robin), `synth/` pads, leads, basses. The
30 variant samples it needs (recipes re-rendered at other base notes and
knob settings) are in `sounds/samples/orchestral/` (15 MB). The release
packer follows `ref:` into `.finst` files and on to their samples.

### How to verify

```
go test ./internal/tracker/... ./internal/pack ./internal/editor -run 'Tracker|Engine|Golden|Instrument|Library'
foundry tracker check          # every song and instrument loads
foundry tracker instruments    # the library
foundry tracker gen-instruments -list
```

`TestOrchestralInstrumentsInTune` checks pitch by autocorrelation across
registers and transposing instruments; `TestShippedInstrumentsSound` plays
every library instrument in every articulation at three velocities;
`TestStringTremoloPulses` checks the tremolo articulation.

## Step B: the score model, voice grammar, interpreter, scheduler and CLI

### What shipped

**`internal/score`** — the model and its file.

| File | What |
| --- | --- |
| `frac.go` | `Frac`: exact time in whole notes (int64 num/den, always reduced, zero value = 0). Arithmetic cancels before multiplying and panics with `*OverflowError` past int64; `RecoverOverflow` turns that into an error at every entry point (`ParseVoice`, `Parse`, `ParsePos`, `Validate`, `interpret.Interpret`). `TicksPerQuarter = 960`; `Frac.Ticks()` rounds, `TicksFloat()` does not. |
| `pitch.go` | `Pitch{Step, Alter, Octave}` spelled, concert pitch, scientific octave (C4 = MIDI 60, B#3 = 60, Cb4 = 59). `KeyAlter`, `InKey` (what a click on a line writes: F#5 on the F line in G), `SpellMIDI(midi, fifths, dir)`, `Interval{Diatonic, Chromatic}` with `"-M2"`-style text, `Pitch.Transpose`. `Duration{Base, Dots}` from breve (`b`) to 256th (`z`), up to three dots, `DurationFor`. |
| `meter.go` | `Meter{Groups, Den, Symbol}`: 4/4, 6/8, additive 3+2+2/8, C, C\|. `Beats()` is the conductor's beats (6/8 = two dotted quarters; 3+2+2/8 = 3/8 2/8 2/8) — what beat events, positions and (phase 1) beaming follow. |
| `score.go` | `Score` (title, composer, bars, pickup, tempo/meter/key maps, sections = rehearsal marks with `loop`/`next`, markers, repeats, params + bindings, loop, gain, lines, buses A/B, master chain), `Line` (instrument `.finst` name, volume, pan, mute, solo, sends A/B, inserts, staves, lanes), `Staff` (clef, `Transpose` written→sounding, ≤ 4 voices), `Voice` → `Measure{Events, Tuplets}` → `Event{Kind, Dur, Grace, Notes, Tuplet, Marks}`, `Note{Pitch, Tie, Acc}`, `Mark{Name, Value}`, `Lane{Name, Points}`. Tuplets are per measure, nested by `Parent`; an event names its innermost. `Pos{Bar, Offset}` written as `"bar:beat"`. Deep `Clone`s; `MeterAt`, `KeyAt`, `BarLength`, `BarStarts`, `ParsePos`/`FormatPos`. |
| `marks.go` | The mark vocabulary: dynamics (pppp..ffff, sf/sfz/sffz/fz/rf/rfz momentary, fp/sfp/sfpp) with MIDI-style levels, articulations, ornaments, notation-only marks, technique (`tech=pizz.`), text, spanner names reserved for phase 3 (`cresc`, `dim`, `hairpin_end`, `slur`, `slur_end`, `gliss`, `8va`…, `ped`…). Unknown names are kept and warned about. |
| `grammar.go` | The voice text (below): `ParseVoice`, `ParseMeasure`, `FormatVoice`, `FormatMeasure`, `FormatEvent`, `Measure.Actual`/`Offsets` (tuplet-scaled lengths, exact). |
| `fscore.go` | `.fscore` YAML: `Parse`/`Marshal`/`LoadFile`/`SaveFile`, `FX` as `{fx: kind, setting: value…}` in order, positions as `"bar:beat"`. Unknown keys are errors (`UnmarshalStrict`), like `.ftrk`. |
| `library.go` | `ScorePath`, `IsScore`, `Load` (cached, shared), `Reload`, `Save`, `ScanScoreNames`, `InstrumentRefs`, `ScoreSamples` (for the packer). |
| `validate.go` | `Score.Validate(instrumentExists) []Issue`: bars that do not add up (over = error, a short first voice = warning), bar rests not alone, ties that land on nothing, pitches outside MIDI, unknown marks, sections (duplicate, `next` to nowhere, order), repeats (range, overlap, > 16), bindings, missing instruments, lanes. |

**The voice grammar** (documented in full at the top of `grammar.go`; the
guide chapter in phase 2 copies it). One bar to a line, bars separated by
`|`:

```
| q.Bb4(mp) eC5 hD5~
| qD5 3{eEb5 eD5 eC5} hBb4+D5+F5(ferm)
| /eA4 qBb4 qr h_
| R
|
```

| Piece | Spelling |
| --- | --- |
| duration | `b w h q e s t x y z` = breve … 256th, then `.` × 0–3; omitted = the previous event's (the printer always writes it) |
| note / chord | `C4`, `F#5`, `Bb3`, `C##4` (`x` = `##`), `En4`; chord `C4+E4+G4`; octave −1..9 |
| per note | `~` tie, `!` always show the accidental, `?` cautionary |
| rest / space / bar rest | `qr`, `q_` (takes time, prints nothing), `R` (fills the bar) |
| grace | `/eD5` acciaccatura, `^eD5` appoggiatura |
| tuplet | `3{ … }`, `5:3{ … }`; default "in the time of" = `DefaultTupletDen`; nest freely; inside one bar |
| marks | `(p)`, `(stacc,acc)`, `(tech=pizz.)`, `("dolce")` = `text`; inside the parentheses `. ' > _ ^` = stacc, staccmo, acc, ten, marc |

Printing is canonical (explicit durations, one bar a line, trailing empty
bars dropped, `n{`…`}` without spaces inside). `Parse(Format(v)) == v` is
checked on 3000 random voices (`TestGrammarRoundTripProperty`), whole random
scores survive Marshal → Parse (`TestFileRoundTripProperty`), and
`FuzzVoiceGrammar` (anything that parses prints to text that parses back
the same) ran about 10 M executions clean.

**`internal/score/interpret`** — `Interpret(score, Options) (*Performance, error)`.

- Unrolls `repeats` (`Bar.Pass` counts the times through); `Options.NoRepeats` plays it as written.
- Tempo map (`tempo.go`): points in performance ticks, steady or a linear ramp (`ramp: true` on a mark moves to the next one — what phase 3's rit./accel. text sets); seconds are the closed-form integral both ways (`Seconds`, `Tick`, `QPM`). A point at every played bar's start, so a repeat jumping back gets the right tempo. Marks with a `unit` (q. = 60) convert to quarters.
- Notes: absolute `Tick`/`Ticks` (rounded from exact positions; seconds come from the exact value, not the rounded tick), `Start`/`End` seconds, `MIDI`, `Cents`, `Velocity`, `Line/Staff/Voice`, `Bar/Pass`, `Grace`, `Articulation` (word from an articulation mark: `stacc` → "staccato"), `Technique` (the line's technique text in force; `arco`/`ord.` reset). Ties merge into one note across bars and passes (matched by MIDI pitch).
- Grace notes: appoggiaturas on the beat taking their written value from the main note (at most half of it, squeezed proportionally); acciaccaturas just before the beat, each at most a 32nd, cutting short whatever the voice still has sounding; at the very top of the score they go on the beat.
- Dynamics apply per line from the event they are on (every voice and staff of the line reads them). Default mf. Momentary ones (sfz) only on the notes at that moment; fp/sfp play the first level on the moment and the second after.
- Lanes become `Curve`s (breakpoints, linear, ends held; a `to:` holds a value flat), laid over the playing order with a step at repeat jumps.
- Also `Bars`, `Beats` (pickup beats counted from the bar's end), `Sections` (first played run, `Next` as a span index), `Markers`, `Length`, `Seconds`, and lookups `BarAt`, `BeatAt`, `SectionAt`, `SectionIndex`, `BarTick`, `FirstNoteAt`.
- Extension points for phase 3: hairpins, slurs, articulation lengths/accents, voltas, D.S./coda and lane overrides are further passes in `walk`/`finish` and further `Options`; `Note` already carries the articulation and technique words.

**`internal/score/play`** — the scheduler, an `audio.Generator`.

| Call | Does |
| --- | --- |
| `Prepare(s) (*Program, errs, err)` | game thread: interpret, `LoadInstrument` per line (missing → silent line, reported), each note's articulation index, curves, buses |
| `New(s)` / `NewFromProgram(p)`, `Start(section)`, `StartAt(tick)`, `SetParamNow` | build and place before handing to the mixer |
| `Play`, `StopTransport`, `PlayFrom(tick)`, `MoveTo(tick)` | transport |
| `SetLoop(from, to)`, `ClearLoop` | loop region (ticks) |
| `TempoScale(scale, secs)` | 0.25–2 × the written tempo, ramped; the score is unchanged |
| `Section(name, tracker.Quantize)` | jump now / next beat / next bar / end of the section |
| `SetParam`, `Mute(line)`, `MuteIndex`, `Solo` | params (bindings like the Tracker's: volume, pan, sends, cutoff), mute fades, solo |
| `SetInstrument(line, name)` | hot swap: later notes play the new instrument, sounding ones finish on the old |
| `SetScore(s)` | editor swap; the play head keeps its tick |
| `Note(key, line, midi, vel)`, `NoteOff(key)`, `NoteFor(line, midi, vel, secs)`, `Pause` | auditions and the level's hard pause |
| `Events(dst)` | `EventBar`, `EventBeat`, `EventSection` (on entering one, and on every jump into one), `EventMarker`, `EventEnd` |
| `Position()` | playing, paused, tick, seconds, bar, pass, beat, section — atomics |
| `LineLevel(i)`, `Scope`, `LiveVoices` | meters |
| `Render(prog, RenderOptions)`, `RenderScore`, `WriteWAV` | offline render (section or tick start, length, tail, tempo scale, params, block size) |

Render walks the block to its next boundary (block end, loop end, a
looping/`next` section's end, a queued jump, the piece's end — the earliest
wins), cues every note-on and note-off inside on its exact frame
(`tracker.Engine.Cue`), handles the jump, and carries on; the engine renders
the block once and cuts at the cues. Fader × volume curve × bindings, pan
(curve), sends and the tempo-synced delay are set once a block. Velocity
picks the instrument's layer and sets the level (`VelocityGain` =
v^1.5: mf ≈ −6 dB, pp ≈ −18 dB). A line's `Playback.Release` replaces its
fade-out at the written end. Bus A's `reverb`, bus B's `delay` and a master
`compressor` are played; line inserts and other bus effects are kept in the
file and listed by `inspect` as not played until the effects rack (phase 4).

| Benchmark (10 ms block) | |
| --- | --- |
| `BenchmarkScheduler16Lines` (16 lines of 16th-note dyads at 140, engine included) | ~234 µs |

**CLI** (`internal/score/scorecli`, wired in `internal/app/devtools_dev.go`):
`foundry score list | inspect <name|path> | check | render <name|path> [-o
out.wav -section s -from 9:3 -seconds n -tempo 1.25 -param k=v -tail n
-rate r] | play <name> [same flags] | fmt [-check] [names]`. `inspect`
prints the maps, sections with times, each line's instrument (and whether
the library has it), staves, note count and range, notes outside the
instrument's playable range, effects not yet played, and every validation
issue; it exits 1 on errors. `play` opens SDL audio itself and prints the
bar/beat as it goes (ctrl-C stops).

**Example**: `resources/sounds/score_demo.fscore` — 16 bars in G (20 with
the repeat of the theme), five lines (flute, horn written a fifth up,
violins, cello, piano on a grand staff with two voices in the right hand),
chords, eighth and quintuplet tuplets, an acciaccatura, ties across bars,
dynamics from pp to f, a volume lane, a rit. ramp into the last bar,
rehearsal marks intro/theme/bridge/coda, a marker and a param bound to the
horn. It renders in about a second (`foundry score render score_demo`).

**Release packer**: `.fscore` is scanned for references, so a level's score
pulls in its instruments and their samples (`TestScorePullsInItsInstruments`).

### How to verify

```
go test ./internal/score/... ./internal/pack
go test ./internal/score -run XXX -fuzz FuzzVoiceGrammar -fuzztime 60s
foundry score check
foundry score inspect score_demo
foundry score render score_demo -o /tmp/demo.wav      # hash 4a6f89d0e9c4c939
foundry score fmt -check
```

`TestOnsetsAreSampleAccurate` renders a score of tuplets and a tempo change
at three tempo scales and three block sizes and finds every onset on
`round(Start / scale × rate)`; `TestRenderAllocationFree` covers steady play,
a loop region, a tempo ramp and a queued section jump; `TestShippedScoresGolden`
holds the demo's render hash (re-capture with `SCORE_GOLDEN_PRINT=1`).

## Step C: MIDI and MusicXML

### What shipped

Four packages under `internal/score/io/`, none linked by the release
runtime (only `scorecli` imports them; `go list -deps -tags release
./cmd/foundry` has none of them):

| Package | What |
| --- | --- |
| `gm` | General MIDI: `Names[128]`, `Instrument(program)` → starter `.finst` (every one checked to exist), `Program(finst)` back (a category default for the rest), `Percussive`, `DrumKit`, `Match(name)` — a part or track name or a MusicXML instrument-sound ("Violin I", "Clarinet in B♭", "Pno.", "wind.reed.clarinet.bflat"; ordered rules so "bass clarinet" ≠ "bass", "english horn" ≠ "horn"), `Pick(candidates…)` (first the library has, then the family, then the piano), `Playback(finst)` (clef/range/transposition without loading samples), `Transposition(semis)`. |
| `notate` | Timed notes → notation, shared with the phase-5 `.ftrk` import and the phase-6 MIDI recording. `Quantize(notes, layout, Grid)` (per beat: the plain grid or a tuplet grid, a tuplet only when it fits clearly better — `tupletBias` 0.6); `Separate(notes, maxVoices, tol)` (chords = same start, ends within tol; each chord to the free voice with the nearest last pitch; top voice first); `Legato(voice, layout, maxGap)` (closes a gap shorter than half the note and ≤ maxGap, to the next note or the bar line); `Write(chords, layout, WriteOptions)` (bars, ties at bar lines and beats, values by the meter — half-value alignment, no crossing the middle of 4/4 or a beat of 6/8 or 3+2+2/8 off the beat, rests beat to beat, double dots only on their own value — tuplet brackets where offsets need them, merged across beats a note crosses, pitches spelled in the bar's key, sharps up and flats down); `Fill` (a gap in a measure as rests/spaces); `DetectKey` (Krumhansl–Kessler); `ParseGrid` ("16,3", "8t"). |
| `midi` | `smf.go`: `Read`/`ReadFile` (formats 0/1/2, running status, note-on 0 as off, sysex F0/F7, meta, unknown chunks, RIFF/RMID, missing end-of-track, unreleased notes; SMPTE timing is an error), `File.Write` (tick order, running status, end-of-track added), meta helpers. `import.go`: `Analyze(f) *Analysis` (parts = track × channel with notes; names; program; channel 10; range; guessed instrument and clef — "grand" for a piano; CC7/CC10 as fader/pan; meter, key and tempo from the file or guessed) and `Import(f, Options) (*score.Score, warnings, error)`. `export.go`: `Export(s, ExportOptions{NoRepeats})`, `ExportFile`. |
| `musicxml` | `read.go`: `Read`/`ReadFile` — .musicxml/.xml, compressed `.mxl` (container rootfile, else the first score outside META-INF), UTF-8/UTF-16 (BOM)/Latin-1, HTML entities, partwise and timewise (turned partwise) — into an ordered element tree. `import.go`: `Document.Parts() []PartInfo` (for the dialog) and `Import(d, Options) (*score.Score, warnings, error)`. |

**MIDI import**, in order: parts (format 0 split by channel and named by
program, its track name the title; format 1's first note-less track is the
conductor and its name the title; copyright = composer); bars from the time
signatures (a mid-bar signature cuts a short bar in its own meter; a first
signature lasting one bar and shorter than the next is a pickup); keys at the
bar they fall in (within a 16th before counts); tempo marks snapped to the
grid, four or more changes a beat apart all one way become one ramp
(`Ramp: true` → the last value), a steady repeat dropped; markers on a bar
line → rehearsal marks (unique names), others and cue points → markers.
With no time signature `guessMeter` scores 4/4 against 3/4 (and 3/4 against
6/8) by how much loud/long/low weight falls on the bar lines; with no key,
`DetectKey`. Then per part: `MarkGraces` (verification pass, below) →
`Quantize` → a piano (instrument clef `grand`)
split at `Split` (C4) onto treble and bass → `Separate` (at most 4 voices;
drums 2) → `Legato` (an eighth at most) → `AttachGraces` → dynamics (each bar's mean velocity
read as the nearest of ppp..fff; a mark on the bar's first chord when the
level changes and the mean is 11 or more velocity from the current level's,
so playing that wavers at a boundary does not flicker) → `Write`. CC11 becomes a
`volume` lane (collinear points dropped). The staff's transposition is the
instrument's `Playback.Transposition`. `Options`: Grid, Voices,
NoDynamics, NoLegato, NoGraces, Split, Key, Meter, per-part Choices (skip, name,
instrument, clef incl. "grand"). Time signatures no score can hold (65/4,
3/256) are ignored, as a sequencer would.

**MIDI export**: the performance (`interpret`, repeats played unless
`NoRepeats`) at 960 ticks a quarter, so onsets are exact. Track 0: title,
composer (copyright), tempo (ramps as 16th-note steps), time signatures (a
pickup as its own short signature), keys, sections as markers, score markers
as cue points. A track per line: name, instrument name, program, CC7 =
volume×100, CC10 pan, the volume lane as CC11 (16th steps, changes only),
notes with velocity = dynamic level; kits (`gm.Percussive`, percussion clef)
on channel 10, the rest round the other 15; a repeated pitch is cut where it
starts again; at a tick offs come first, then by key (a deterministic file).

**MusicXML import**: notes, chords, rests (a lone whole rest or
`measure="yes"` = bar rest), dots, ties (`tie` and `tied`; a tie that does
not reach its pitch in the voice is dropped), tuplets at any depth (bracket
ratios from `tuplet-actual/normal`, else the time modification over the
open brackets; without brackets an implicit group closes where its notes
reach a plain value), grace notes (slash = acciaccatura), voices (a voice
number lives on the staff most of its notes are on — cross-staff notes stay
in it — unless a quarter of them are elsewhere; voices on a staff numbered
lowest first, past 4 dropped with a warning), staves (any number; grand
staff), clefs (G/F/C by line, octave clefs, percussion, TAB → treble_8vb),
keys and meters (additive "3+2+2", composite 2/4+3/8 → 4+3/8, C, C|) taken
from the first untransposed part and moved to concert, `<transpose>` →
`Staff.Transpose` with pitches moved to concert, tempo from `metronome`
(dotted units) or `sound tempo` with the direction's words as its text
(the first part at a place wins; bar 1 always has one), part names and
`instrument-sound`/names/`midi-program` → instrument, `midi-unpitched` →
the drum's GM key, `pan`. Also kept already (cheap, reserved names): dynamics,
articulations, ornaments, fermata, arpeggio, slurs, gliss, wedges
(`cresc`/`dim`/`hairpin_end`), octave shifts, pedal, words (technique words
as `tech=`, others `text`), rehearsal marks → sections, simple repeats.
Sibelius specifics handled: 256 divisions (triplets 85/85/86, nested 57 —
the voice's own exact position wins within a division), voice 5 on staff 2,
`backup`/`forward`, pickup `number="0" implicit="yes"`, invisible rests and
cue notes as spaces, zero-length notes ignored, a cadenza bar longer than
its meter given a meter of its own (warned). An accidental the file prints
where key and bar would not becomes `!` (forced); cautionary/editorial ones
`?`.

**CLI** (`internal/score/scorecli/import.go`):

```
foundry score import <file.mid|.midi|.rmi|.musicxml|.xml|.mxl> [-o name] [-title t] [-list]
      [-skip 2,5] [-instrument 3=strings/viola_section] [-force]
      [-grid 16,3] [-voices 4] [-no-dynamics] [-no-legato] [-split C4] [-key Eb|F#m|"C minor"|-3] [-meter 6/8]
foundry score export-midi <score> [-o out.mid] [-no-repeats]
```

`-list` prints the dialog's table (part, notes, program, instrument, clef;
meter/key "from the file" or "guessed") and writes nothing. An import
refuses to overwrite without `-force`, prints the lines and every warning,
and fits the gain: `play.FitGain(s, 0.9)` renders offline (32 kHz) and
lowers the master gain until the peak is ≤ 0.9 (an import at unity with a
forte piano peaked at 1.35). The editor's import dialog (phase 5) is a UI
over `Analyze`/`Parts` + `Choices` + `FitGain`.

### How to verify

```
go test ./internal/score/...
foundry score import internal/score/io/midi/testdata/format1_orchestra.mid -list
foundry score import internal/score/io/midi/testdata/format1_orchestra.mid -o hangar_sketch
foundry score import internal/score/io/musicxml/testdata/sibelius_piano.musicxml -o piano_study
foundry score inspect piano_study && foundry score render piano_study
foundry score export-midi score_demo -o demo.mid
```

Fixtures are hand-made: `midi/testdata/make_fixtures.py` writes the `.mid`
files byte by byte (independent of the package's writer): `format0_band`
(three interleaved channels, running status, sysex, unknown meta, D major),
`format1_orchestra` (seeded human timing ±15 ticks and 90% releases, eighth
triplets, two violin voices, a grand-staff piano, CC7/10/11, a tempo ramp,
3/4 + key change at bar 9, markers, a cue), `waltz_nometa` (no time or key
signature: guessed 3/4, F major), `sloppy` (RIFF wrapper, junk chunk, no
end-of-track, a note never released). `musicxml/testdata`: `simple`,
`sibelius_piano` (above), `transposing` (B♭ clarinet listed first, F horn,
alto viola, 8vb guitar, unpitched drum set, composite meter), `timewise`;
`make_fixtures.py` writes `compressed.mxl` and `utf16.musicxml` from them.
`TestExportImportPreservesNotes` exports a score with a pickup, tempo,
meter and key changes, triplets over one and two beats, ties, chords, a
two-voice grand staff, a transposing line and a kit, reads it back and
finds every note at the same tick, length, pitch and velocity, the maps and
lines back, the flute reading as written, and a re-export identical.
`TestWriteRandomRhythmsAddUp` writes random 16th/triplet rhythms in seven
meters and checks every bar adds up and every onset survives.
I checked a piano-roll overlay of `format1_orchestra.mid` against the
export of its import (every onset aligned, legato closed, staccato kept)
and a spectrogram of the import's render (all five lines, the ramp, peak
0.90).

## Verification pass

An adversarial pass over steps A–C: the code read against SPEC.md, the CLI
run on good and broken scores, the demo and each of its lines rendered and
looked at as spectrograms, the demo exported to MIDI and imported back and
diffed note by note, a hand-written MuseScore-style MusicXML file
imported, fuzzing of every reader, and new edge-case tests. What it found
and fixed:

- **MIDI import wrote grace notes as chord tones and split the line.** An
  exported acciaccatura (a 32nd just before the beat) quantised onto the
  next note's onset, became a chord tone there, pushed the flute into a
  second voice, and the voice choice then hopped between the two voices for
  bars. New `notate.MarkGraces` (a run of at most four onsets each at most
  half a grid unit long, ending about where the next starts, at least a
  unit after the onset before) marks played graces; `Quantize` keeps them
  out of the grid and puts them at their main note's snapped onset;
  `AttachGraces` hangs them on that chord (in their staff, else the other
  staff of a grand staff); `Write` prints them as acciaccaturas (an eighth
  alone, 16ths in a run), spelled toward the main note. `-no-graces` /
  `Options.NoGraces` turn it off.
- **Voice separation hopped between voices by nearest pitch.** `Separate`
  now counts the time a free voice has been silent against it (12
  semitones a whole note), so a line that split for a moment comes back
  together.
- **A quintuplet on the default grid became a chord.** `Quantize` now
  notices when a beat's chosen grid would snap onsets played more than
  half a unit apart onto one point and then tries every tuplet of the grid
  plus 3, 5 and 7, keeping the one that merges fewest (only when the grid
  allows tuplets at all; `-grid 16` still means none). The demo's
  quintuplet now round-trips through MIDI exactly.
- **Runaway files hung the importers.** A MusicXML measure a million
  quarters long, or a MIDI note-off at tick 2^34, laid out millions of
  bars (minutes, gigabytes). `score.MaxBars` (10000) and
  `score.MaxBarLength` (32 whole notes) are now enforced by `Parse`,
  `ParseVoice` and both importers, which refuse such files at once with a
  clear error.
- **Fuzzing found imports that did not read back:** a MIDI time signature
  of 65/4; a MusicXML cadenza bar given a 70/4 meter of its own (now 35/2,
  or even additive groups past 64); a MusicXML `<transpose>` with
  mismatched diatonic/chromatic steps that produced "Ebbbb". Fixed at the
  root too: `Pitch.Transpose` never spells past a double sharp or flat (it
  takes the next letter), `ParseInterval` rejects intervals past doubly
  augmented/diminished (`Interval.Valid`), and the MusicXML importer
  respells an unnameable transposition from its semitones. The failing
  inputs are kept as fuzz seeds (`testdata/fuzz/`).
- **Interpreter:** grace notes with no main note (the voice stops, or falls
  silent for a bar) played at the end of the whole score, or were carried
  bars later onto the next note; they now play where the voice stopped.
- **Scheduler:** `Solo(-1)` unmuted every line, including lines the score
  itself mutes; unsolo now restores the score's own mutes and solos.
  `RenderOptions.TempoScale` was unclamped (the transport clamps to
  0.25–2); it now clamps the same way.
- **CLI:** `score check` ignored its arguments (and so, run outside a
  project, checked nothing and reported success); it now takes names or
  paths. `render` silently rendered invalid scores and clipping mixes; it
  now prints the score's errors and a clip warning with the gain that would
  fix it. `-tempo` outside 0.25–2 is refused. `-no-graces` added.
- **Editor test hygiene:** the instrument-pane draw test wrote an
  `imgui.ini` section into the package directory; it no longer writes one.
- **Docs:** SPEC asks for the voice grammar to be documented in the guide
  chapter; `docs/guide/32-score-editor.md` now exists (file, grammar,
  marks, playback rules, instruments, commands, import/export) and is in
  the guide index.

New tests: `TestGraceNotesFromPlaying`, `TestQuantizeKeepsQuintupletsApart`,
`TestSeparateLineComesBackTogether` (fails without the idle penalty),
`TestExportImportGracesAndQuintuplets`, `TestImportRefusesRunawayLength`,
`TestImportRefusesRunawayMeasures`, `TestTransposeStaysWritable`,
`TestGracesWithNoMainNote`, `TestUnsoloKeepsTheScoresMutes`,
`TestRenderClampsTempoScale`, `TestRandomCommandsKeepThePlayerSane` (3000
random transport/loop/section/tempo/mute/solo/pause/preview/swap commands
with random block sizes: no NaN, play head in range, silence after stop),
`TestCheckAndRenderReportProblems`; fuzz targets `midi.FuzzImport`,
`musicxml.FuzzImport`, `interpret.FuzzScoreFile` (parse → validate →
perform → save fixpoint) and `tracker.FuzzInstrumentFile` (parse → save
fixpoint → play every articulation at four velocities), each run 1.5–3
minutes clean after the fixes.

Checked and found right: the regenerated starter library is byte-identical
to the checked-in one (`gen-instruments` is deterministic); loop points of
every orchestral variant are seamless; `BenchmarkPlayer32Channels` at HEAD
and in the tree measure the same on this machine (≈430–460 µs per 10 ms
block under load, under the 0.5 ms bar); the release build links
`internal/tracker` but none of `internal/score/io/...` or the CLI;
`foundry score play` plays on this machine's device.

## Decisions

- **Spanner end marks are inclusive.** `hairpin_end`, `slur_end`,
  `ottava_end`, `ped_end` go on the last event the line covers (the
  MusicXML importer puts a wedge stop on the event before the stop's
  place). Phase 3's playback must end a hairpin at the end of the event
  carrying `hairpin_end`.
- **Pitches are concert pitch in the file**; `Staff.Transpose` (written →
  sounding) only changes display. Imports move transposed parts to concert.
- **Velocity is the dynamic.** A note's velocity comes from the dynamics
  in force (default mf); it picks the velocity layer and sets the level
  (`VelocityGain` = v^1.5).
- **Bus A = reverb, bus B = delay, master = compressor** until the phase-4
  rack; other effects round-trip and are listed by `inspect`.
- **Control curves are set per block** (10 ms at 48 k); note-ons and
  note-offs are cued on their exact frame.
- **Imports are dry by default** (sends 0), with the gain fitted to a 0.9
  peak.
- **Rendering a score with errors still renders** (to hear what is wrong),
  but prints the errors; `check`/`inspect` exit 1 on them.
- **Caps:** 10000 bars, 32 whole notes a bar, 4 voices a staff, three dots,
  256th notes, numerators to 64, denominators to 128.

## How to verify (whole phase)

```
go build ./... && go vet ./internal/score/... ./internal/tracker/...
go test ./internal/tracker/... ./internal/score/... ./internal/pack ./internal/editor
task test-release
foundry tracker check && foundry score check && foundry score fmt -check
foundry score inspect score_demo
foundry score render score_demo -o /tmp/demo.wav        # hash 4a6f89d0e9c4c939
foundry score export-midi score_demo -o /tmp/demo.mid
foundry score import /tmp/demo.mid -o demo_back          # one flute voice, its grace notes, the quintuplet
go test ./internal/score/io/midi -run XXX -fuzz FuzzImport -fuzztime 60s
go test ./internal/score/io/musicxml -run XXX -fuzz FuzzImport -fuzztime 60s
go test ./internal/score/interpret -run XXX -fuzz FuzzScoreFile -fuzztime 60s
go test ./internal/tracker -run XXX -fuzz FuzzInstrumentFile -fuzztime 60s
```

## Deferred

Everything below is assigned to the phase SPEC.md gives it; none of it was
part of phase 0's line in SPEC.md.

- **Phase 1 (engraving):** none of the model needs changing for it; the
  `up`/`down` notation marks, `!`/`?` accidental display and `Meter.Beats()`
  are there for the engraver.
- **Phase 2 (many lines, playback, levels):** `world.Music` playing a
  `.fscore` (level `music:`, `play_music`, `song_*` verbs, `:song_*:`
  events, save games via `Player.StartAt`/`Position().Tick`); the scheduler's
  API mirrors `tracker.Player`'s and the packer already follows `.fscore`
  references. The guide chapter gains the editor. The transport's "write
  the tempo slider as a tempo mark" option.
- **Phase 3 (expression):** playback of hairpins (`cresc`/`dim` …
  `hairpin_end`, inclusive), slurs, glissandi (instrument `Playback.Gliss`),
  articulation lengths and accents (every .finst already carries `Playback.Staccato`), voltas,
  D.S./D.C./coda/fine, ottava, pedal, ornaments, tremolo marks, fermata
  timing, arpeggio, clef changes inside a staff, the dynamics lane and its
  overrides; the mark names are reserved, stored and round-tripped. A
  grammar shorthand for hairpins (the SPEC sketch's `h.C5<`) is not in the
  grammar (`>` already means accent inside marks); phase 3 decides its
  spelling. MIDI pitch bend and CC64 import wait for gliss and pedal
  playback.
- **Phase 4 (DAW layer):** line inserts and bus/master effects beyond
  reverb/delay/compressor play (`play.Unplayed` lists them); pan and cutoff
  lanes as MIDI controllers; SFZ/SF2 import → `.finst`; editing velocity
  layers, round robin and articulations in an instrument editor UI (the
  Tracker shows and plays them today; authoring is YAML or
  `gen-instruments`); swing and humanise.
- **Phase 5 (output):** MusicXML export; fuller MusicXML import (layout
  hints); `.ftrk` export/import (`notate` is ready for the import); the
  import dialog over `midi.Analyze` / `musicxml.Document.Parts` + `Choices`
  + `FitGain`.
- **Phase 6 (extras):** lyrics and chord symbols (the importer warns when a
  file has them), percussion drum maps (imported hits are stored at their
  GM key), MIDI recording (reuses `notate`). Meter guessing is 4/4, 3/4 or
  6/8 and key guessing one key per file, and a tuplet cannot cross a bar
  line (the importer closes it there); these stay as limits unless phase 6
  needs more.
- **Kept as limits, by design:** SMPTE-timed MIDI files are refused with a
  clear error (no beats to put bars on).
