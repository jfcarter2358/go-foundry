# Scores

A score is music written as notation rather than tracker patterns: lines for
a violin section, a horn, a piano, each on its own staves, with real note
values, tuplets, ties, grace notes and dynamics. It plays back through the
same sample engine and the same instrument library as the
[Tracker](31-music-tracker.md), so a `.fscore` and a `.ftrk` can share every
instrument.

Scores live beside the other sounds, as `resources/sounds/<name>.fscore`. This
chapter covers the file, the notation text inside it, how a score plays, and
the `foundry score` commands that check, render, play, import and export
scores. The Score editor window, and playing a score in a level, come in a
later update. Until then, a score is written as text, imported from MIDI or
MusicXML, and heard with `foundry score render` or `foundry score play`.

## The file

A `.fscore` is YAML. The score-wide maps sit at the top, then the lines, then
the mix:

```yaml
version: 1
title: Hangar Theme
composer: ""
bars: 16
pickup: 1/4                    # optional: an incomplete first bar, in whole notes
tempo: [{bar: 1, bpm: 92, text: Andante}, {bar: 15, bpm: 92, text: rit., ramp: true}, {bar: 16, bpm: 66}]
meter: [{bar: 1, sig: 4/4}, {bar: 9, sig: 3+2+2/8}]
key:   [{bar: 1, fifths: -2, mode: major}]
sections: [{name: intro, bar: 1}, {name: theme, bar: 9, loop: true}]
markers: [{name: hit, bar: 11, beat: "3"}]
repeats: [{start: 5, end: 8, times: 2}]
params: [{name: intensity, default: 0}]
bindings: [{param: intensity, line: Horn, target: volume, from: 0, to: 1}]
lines:
  - name: Violin I
    instrument: strings/violin_section
    volume: 0.8
    pan: -0.3
    sends: {a: 0.3, b: 0}
    staves:
      - clef: treble
        voices:
          - |
            | q.Bb4(p) eC5 hD5
            | qEb5 qD5 hC5
            |
    lanes:
      volume: [{at: "5", value: 1}, {at: "6:3", value: 0.6}]
buses:
  a: {fx: [{fx: reverb, size: 0.7}], return: 0.25}
  b: {fx: [{fx: delay, beats: 0.75, feedback: 0.3}], return: 0.2}
master: [{fx: compressor, threshold: -12, ratio: 3}]
```

| Key | |
| --- | --- |
| `tempo` | Marks at a bar (and `beat:`): `bpm` of a quarter, or of `unit:` (`q.` = 60 in 6/8). `ramp: true` moves smoothly to the next mark (a rit. or accel.) instead of stepping at it. |
| `meter` | Any time signature from any bar: `4/4`, `6/8`, additive `3+2+2/8`, `C`, `C\|`. Beats follow it (6/8 counts two dotted quarters). |
| `key` | Sharps (positive) or flats (negative) from a bar, and the mode's name. |
| `sections` | Rehearsal marks. A game moves between them by name. `loop: true` repeats a section until asked to move on; `next:` goes to another section at its end. |
| `markers` | Named points that tell scripts when playback passes them. |
| `repeats` | Bars `start`..`end` played `times` times (2 by default). |
| `params`, `bindings` | Values a script ramps while the score plays, bound to a line's volume, pan, sends or filter cutoff, as in the Tracker. |
| `lines` | Each line names a library instrument (`category/name`, a `.finst`), has a fader, pan and two sends, and one or more staves. A piano has two. |
| `staves` | `clef` (`treble`, `bass`, `alto`, `tenor`, `treble_8vb`, `treble_8va`, `bass_8vb`, `bass_8va`, `percussion`), an optional `transpose` (written to sounding, so a B♭ clarinet's is `-M2` and a horn in F is `-P5`), and up to four voices. |
| `lanes` | Automation. `volume` multiplies the fader and `pan` replaces the pan. Each point is a position and a value; `to:` holds the value flat to a second position. |
| `buses`, `master` | Bus A's reverb, bus B's delay and the master compressor play today. Other effects are kept in the file and listed by `foundry score inspect` as not played yet. |

Positions are written `bar` or `bar:beat`, with beats counted the way the
bar's meter counts them: `5`, `5:3`, `5:2+1/2`.

Pitches in a score are always **concert pitch** (what sounds). A staff's
`transpose` only changes how it is shown. The notes in the file never change
when you change it.

## Notation as text

Each voice is text with one bar to a line and bars separated by `|`. The text
diffs cleanly in the Git window and is easy to read in the Claude panel:

```
| q.Bb4(mp) eC5 hD5~
| qD5 3{eEb5 eD5 eC5} hBb4+D5+F5(ferm)
| /eA4 qBb4 qr h_
| R
|
```

An event is written in this order: an optional grace sign, a duration, then
what sounds, then optional marks.

| Piece | Spelling |
| --- | --- |
| Duration | `b` breve, `w` whole, `h` half, `q` quarter, `e` eighth, `s` 16th, `t` 32nd, `x` 64th, `y` 128th, `z` 256th, then up to three dots: `q.`, `e..`. Left out, an event takes the previous event's value. The printer always writes it. |
| Note | A capital letter, accidentals (`#`, `##` or `x`, `b`, `bb`, `n` natural) and an octave: `C4` is middle C, `F#5`, `Bb3`. Octaves run from -1 to 9. |
| Chord | Notes joined with `+`: `qC4+E4+G4`. |
| Per note | `~` ties into the same pitch in the next event, `!` always prints the accidental, `?` prints it in brackets. |
| Rest | `qr` is a rest, and `q_` takes time but prints nothing (a space). `R` alone fills the bar, whatever its meter. |
| Grace note | `/eD5` is an acciaccatura (slashed, played just before the beat), `^eD5` an appoggiatura (played on the beat). Grace notes take no time in the bar. |
| Tuplet | `3{eC4 eD4 eE4}` is three in the time of two. `5:3{…}` gives the ratio. Without one, `n{` plays n in the time of the usual number: 3 in 2, 2 in 3, 5, 6 or 7 in 4, 9 in 8. Tuplets nest, and each stays inside one bar. |
| Marks | In brackets after the event, separated by commas: `(p)`, `(stacc,acc)`, `(tech=pizz.)`, `("dolce")`. Inside the brackets, `.` `'` `>` `_` `^` stand for staccato, staccatissimo, accent, tenuto and marcato. |

Spelling matters. `F#4` and `Gb4` sound the same but are different notes on
the page. The key signature never changes what a written pitch means; it only
decides whether its accidental is printed. Every bar must add up to its meter.
A bar that runs over is an error, and one that runs short is a warning.

### Marks

| Kind | Names |
| --- | --- |
| Dynamics | `pppp` … `ffff`; the accents `sf`, `sfz`, `sffz`, `fz`, `rf`, `rfz`; `fp`, `sfp`, `sfpp` |
| Articulations | `stacc`, `staccmo`, `acc`, `ten`, `marc`, `portato`, `legato` |
| Ornaments | `tr`, `mord`, `imord`, `turn`, `iturn`, `trem` |
| Notation | `ferm`, `arp`, `up`, `down` |
| Text | `"dolce"` (`text=`), `expr=`, `tech=` (playing technique: `tech=pizz.`, `tech="con sord."`) |
| Spanners | `cresc`, `dim` and `hairpin_end`; `slur` and `slur_end`; `gliss`; `8va`, `8vb`, `15ma`, `15mb` and `ottava_end`; `ped` and `ped_end`. An end mark goes on the last note the line covers. |

A name the build doesn't know is kept and saved, and `foundry score inspect`
warns about it. Nothing is lost when an older build opens a newer file.

## How a score plays

- **Time is exact.** Note values and tuplets are kept as exact fractions, so
  a note after a thousand triplets is exactly where it was written. Every
  note starts on the exact sample its time falls on.
- **Tempo** follows the tempo marks, including ramps, and the repeats unroll.
- **Dynamics** set the level of every note on the line from the mark on.
  Before the first one a line plays at mf. `sfz` and the other accents
  affect only the notes they are on. `fp` plays forte on its note and piano
  after it. The level picks the instrument's velocity layer and sets the
  volume.
- **Ties** join into one held note, across bars and repeats.
- **Grace notes.** An acciaccatura is crushed in just before the beat, taking
  its time from the note before it. An appoggiatura takes time from the
  note it leans on, at most half of it.
- **Articulations and technique** choose the instrument's articulation:
  `(stacc)` plays its staccato samples, and `tech=pizz.` switches the line to
  pizzicato until `arco` or `ord.`.
- **Lanes** move the line's volume and pan.

Hairpins, slurs, glissandi, articulation lengths and accents, voltas,
D.S./D.C./coda, ottava lines and pedalling are written and kept today. They
are not played yet.

## Instruments

A line names a `.finst` from the shared instrument library under
`resources/sounds/instruments/` (see [the instrument library](31-music-tracker.md#the-instrument-library)).
The starter library includes an orchestral set: string sections with sustain,
staccato, pizzicato, tremolo and muted articulations; woodwinds; brass; grand
piano, celesta, harp and organ; choir; timpani and tuned percussion; synths.
Each instrument carries its range, its default clef and its transposition.
`foundry score inspect` flags notes outside the instrument's range.

## Commands

```
foundry score list                         every score and how long it runs
foundry score inspect <score>              maps, sections and their times, lines, staves, ranges, problems
foundry score check [score …]              does it parse, validate and find its instruments (all scores with no names)
foundry score render <score> [flags]       render to a WAV
foundry score play <score> [flags]         play on the audio device, printing the bar and beat
foundry score fmt [-check] [score …]       rewrite scores in canonical form
foundry score import <file> [-o name]      a MIDI or MusicXML file as a score (-list shows the parts first)
foundry score export-midi <score> [-o f]   the score as a Standard MIDI File
```

A score is named by its name under `sounds/` without the extension, or by a
path. `render` and `play` take `-section name`, `-from 9:3`, `-seconds n`,
`-tempo 1.25` (25% to 200% of the written tempo; the score is unchanged) and
`-param name=value`. `render` also takes `-o out.wav`, `-tail` (seconds of
reverb after the end) and `-rate`. It prints the length, the peak and a hash
of the audio, so two renders can be compared.

## Importing MIDI and MusicXML

**From Sibelius, Finale, Dorico or MuseScore**, export MusicXML (in Sibelius:
**File > Export > MusicXML**) and import that. Sibelius's own `.sib` format
was never published, so it can't be read directly.

```
foundry score import nocturne.musicxml -o nocturne
foundry score import sketch.mid -list
foundry score import sketch.mid -o hangar_sketch -instrument 3=strings/viola_section -skip 5
```

`-list` shows each part with its notes, program, guessed instrument and clef,
and the meter and key (from the file, or guessed), and writes nothing. An
import never overwrites an existing score unless you pass `-force`. It prints
the lines it made and every warning, and lowers the master gain if the music
would clip.

**MusicXML** is already notation, so it comes in as written: notes, chords,
rests, dots, ties, tuplets, grace notes, up to four voices per staff, any
number of staves, clefs, keys, time signatures, tempo marks, part names and
instruments (which choose the library instrument), and transposing parts,
which are stored at concert pitch with the staff's transposition. Dynamics,
articulations, ornaments, slurs, hairpins, ottava lines, pedal marks,
technique text, rehearsal marks and simple repeats are kept too. Anything the
score can't hold yet is reported as a warning, never dropped silently:
lyrics, chord symbols, voltas, D.S./coda, and a clef change partway through
a staff.

**MIDI** is timing, so it is notated on the way in:

- **Quantising.** Notes snap to 16ths, and a beat can be triplets instead
  (`-grid 16,3` is the default; `-grid 32,3,5` adds quintuplets). A beat
  whose notes would otherwise merge into a chord can also become a
  quintuplet or septuplet.
- **Grace notes.** A very short note just before another becomes a grace
  note (`-no-graces` turns this off).
- **Voices.** Overlapping notes are split into chords and up to four voices
  (`-voices 1` writes overlaps as chords).
- **Legato.** Small gaps a player leaves between notes are closed
  (`-no-legato` keeps them as rests).
- **Piano.** A piano part becomes a grand staff split at middle C (`-split`).
- **Dynamics.** Velocities are read as dynamics (`-no-dynamics` keeps them
  out).
- **Controllers.** CC7 and CC10 become the line's volume and pan, and CC11
  becomes a volume lane.
- **Meter and key.** These come from the file's time and key signatures.
  When the file has none they are guessed, or you can set them with
  `-meter 6/8` and `-key Eb` (or `F#m`, `"C minor"`, `-3`).
- **Tempo and markers.** Tempo changes become tempo marks, and a run of small
  steps in one direction becomes a ramp. Markers on a bar line become
  rehearsal marks.

`export-midi` writes format 1 at 960 ticks per quarter, which holds every
note exactly:

- **Conductor track:** tempo (ramps as steps), time and key signatures,
  sections as markers.
- **One track per line:** its General MIDI program, volume and pan, the
  volume lane as CC11, and notes with velocities from the dynamics.

Repeats are played out unless you pass `-no-repeats`. Importing an export
gives back the same notes.
