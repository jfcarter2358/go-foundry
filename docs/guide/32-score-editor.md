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
scores, and the Score editor window, where notes are entered the way Sibelius
or MuseScore enters them. Playing a score in a level comes in a later update;
until then a score is heard in the editor, or with `foundry score render` or
`foundry score play`.

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

## The Score editor

Open it from the editor strip (**Score**) or **Window > Score**. It starts on
a one-line sketch; **File > New** offers a solo line, a piano (grand staff),
a string quartet and a chamber ensemble (flute, clarinet in B♭ and horn in F
written transposed, violins, cello), each eight empty bars of 4/4 in C.
**File > Open**, **Open Recent**, **Save** (Ctrl+S), **Save As** and **Save
All** work as in every editor, and the chip at the right of the menu bar says
whether the score matches its file.

The score is drawn as one continuous system on paper, scrolled with the
wheel (Shift+wheel across) and zoomed with Ctrl+wheel about the pointer,
Ctrl+= and Ctrl+-. Line names sit in the column on the left, bar numbers
along the top. **View > Concert Pitch** shows transposing instruments as they
sound; otherwise they are shown — and typed, clicked and sharpened — at
written pitch, and stored at concert pitch. **View > Voice Colours** colours
voices 2–4 green, orange and magenta.

### Entering notes (Sibelius keys)

| To | Press |
| --- | --- |
| Note input on / off | `N` — a ghost note follows the pointer, spelled in the key, with its pitch, value and voice beside it |
| Write a note | click a line or space: the pitch the key signature implies (F♯ on the top line in G), the current value, at the beat under the pointer. What it lands on is cut short, what it covers goes, the rest of the bar is rests. A value longer than the bar has left carries on over the barline, tied |
| Letters | `A`–`G` write after the selection, in the octave nearest the note before. Out of note input a letter changes the selected note; `Shift`+letter adds that letter above the chord |
| Interval above / below | `2`–`9` / `Shift+2`–`9`, spelled in the key (a third above D in G is F♯). With bars selected, every chord in them takes it |
| Sharp, flat, natural | `+` (or `=`), `-`, `0`, keypad `8` `9` `7`; again for a double sharp or flat. With nothing selected the accidental goes on the next note written |
| Semitone, step, octave | `Up`/`Down`, `Alt+Up`/`Down` (in the key), `Ctrl+Up`/`Down` — the selected note, or every note of the selected bars |
| Respell | `Return` (F♯ ↔ G♭) |
| Values | keypad `1`–`6` = 32nd … whole (laptop `Alt+1`–`6`); `F8` or keypad `/` switches to the second layout, where `1`–`4` are 64th, 128th, 256th and breve. In note input the value is the next note's, chosen before the letter (`4 C 3 D E` is a quarter and two eighths); out of note input the selected note takes it and the bar reflows |
| Dot | keypad `.` (`Alt+.`): once a dot, again a double dot, again none — like the value, the next note's in note input |
| Rest | keypad `0` (`Alt+0`): in note input a rest after the selection, otherwise the selected note becomes one |
| Tie | keypad `Enter` or `T` |
| Voice | `Ctrl+Alt+1`–`4`, or keypad `*` for the next. In note input the caret stays where it was, so the new voice starts under the old one's last note; out of it the new voice's note at the same beat is selected |
| Tuplet | `Ctrl+2`–`9` start one on the selected note: three of its value in the time of two, five in four… Inside a tuplet the new one nests |
| Flip a stem | `X` or keypad `-` |
| Delete | `Delete`: a note becomes a rest, one note of a chord goes, a rest in voices 2–4 is hidden; selected bars are cleared |
| Previous / next note | `Left` / `Right` |
| Key, time, clef | `K`, `Ctrl+Shift+T`, `Q` (or the toolbar): a key or time signature from the selected bar; a clef for a whole staff |

Changing the time signature keeps each bar's music but fits it to the new
length: music past a new barline is cut and a short bar filled with rests
(Ctrl+Z brings it back). Changing the key never changes a note — the pitches
are spelled — only what the page prints.

### Selecting, copying, repeating

Click a note (or rest) to select it; it plays. Click empty space in a bar to
select that bar of that line (typing into it starts on the staff you
clicked, so a piano's bass staff is a click away), `Ctrl`+click for that bar of every line,
`Shift`+click to extend; `Ctrl+A` selects everything and `Esc` clears.
`Ctrl+C` and `Ctrl+V` copy and paste bars (every voice of every staff of the
lines) or a single note; `Alt`+click pastes at the clicked bar or beat. `R`
repeats the selection right after itself, growing the score when it has to,
and selects the copy, so `R` again carries on. A paste into bars of another
meter is fitted to them. `Ctrl+Z` / `Ctrl+Y` undo and redo every edit.

`Space` plays from the selection and stops; `P` plays from the selection.
The full transport (ruler, loop, tempo) comes with many-line playback.

### The keypad, the keymap and F1

The keypad panel (toolbar **Keypad**, `Ctrl+Alt+K`) mirrors the numeric
keypad in Sibelius's layout and is clickable; the selected note's value,
dot, accidental, tie and rest are lit, and its **More** tab is the second
layout. **F1** in the Score window shows the active keymap over the score.

**Edit > Keyboard Shortcuts** picks the keymap — **Sibelius** (the default) or
**MuseScore** (digits `1`–`8` are 64th … breve, `Alt+2`–`9` intervals, `J`
respells, `0` rests, `T` ties) — and rebinds any command: **Set** replaces a
command's keys with the next one pressed, **Add** gives it another, **Clear**
unbinds it; a key already in use moves to the new command. The choice and the
rebindings are kept in `score.yaml` beside the project (never packed).

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
foundry score render <score> -png out.png  engrave the notation to an image
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

`render -png out.png` engraves the score instead: the notation as the Score
editor shows it, every bar on one continuous system, in Bravura. It takes
`-bars 5-8` (or one bar), `-lines 1,3` (1-based), `-scale 12` (pixels per
staff space; a staff is four spaces tall), `-concert` (transposing staves at
sounding pitch rather than written), `-spacing 1.2` (wider or tighter) and
`-bar-numbers 5` (number every fifth bar; `0`, the default, numbers only the
first bar shown when it is not bar 1, `-1` none). It prints the image's size
and a hash of its pixels.

The engraving follows the usual rules, so what a score says is what a
player expects to read. Accidentals last to the barline and are shared by a
staff's voices; a note tied over the barline does not repeat its
accidental; `!` on a note forces one and `?` puts it in parentheses. Beams
follow the meter: beats, half bars of eighths in 4/4, dotted beats in 6/8,
the groups of 3+2+2/8 (5/8 beams 3+2 and 7/8 2+2+3), and each tuplet on its
own. With two or more voices on a staff, voices 1 and 3 have stems up and 2
and 4 down, whatever the pitch, and rests move out of the way; the `up` and
`down` marks force a stem. A bar written as `R`, or as one whole rest, has
its rest in the middle; an empty bar shows a faint one. Copyright: the
Bravura font is © Steinberg Media Technologies, under the SIL Open Font
License (an editor install carries it in `licenses/`).

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
