# Scores

A score is music written as notation rather than tracker patterns: lines for
a violin section, a horn, a piano, each on its own staves, with real note
values, tuplets, ties, grace notes and dynamics. It plays back through the
same sample engine and the same instrument library as the
[Tracker](31-music-tracker.md), so a `.fscore` and a `.ftrk` can share every
instrument.

Scores live beside the other sounds, as `resources/sounds/<name>.fscore`, and
a level plays one the way it plays a song:

```yaml
music:
  track: hangar_theme      # sounds/hangar_theme.fscore
  section: theme           # optional: a rehearsal mark to start at
  params: {intensity: 0}   # optional: the score's params at the start
```

This chapter covers the file, the notation text inside it, how a score plays,
the Score editor window (where notes are entered the way Sibelius or MuseScore
enters them), playing a score in a level and steering it from scripts, and the
`foundry score` commands that check, render, play, import and export scores.

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
voltas: [{start: 7, end: 8, passes: [1]}, {start: 9, end: 9, passes: [2]}]
jumps: [{mark: segno, bar: 3}, {mark: to_coda, bar: 10}, {mark: ds, bar: 14, al: coda}, {mark: coda, bar: 15}]
playback: {ignore_repeats: false, graces: auto}
concert: false
params: [{name: intensity, default: 0}]
bindings: [{param: intensity, line: Horn, target: volume, from: 0, to: 1}]
lines:
  - name: Violin I
    instrument: strings/violin_section
    volume: 0.8
    pan: -0.3
    sends: {a: 0.3, b: 0}
    gliss: slide                 # optional: slide, chromatic or diatonic
    dynamic_overrides: [{at: "2", to: "3", level: 0.2}]
    staves:
      - clef: treble
        clefs: [{at: "6:3", clef: alto}]   # optional clef changes
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
| `tempo` | Marks at a bar (and `beat:`): `bpm` of a quarter, or of `unit:` (`q.` = 60 in 6/8), and `text:`. A mark can be text alone: `rit.`, `rall.`, `accel.`, `a tempo`, `tempo primo`, `meno mosso`, `più mosso` (see *How a score plays*), with `to:` where a gradual change's dashes end. `ramp: true` moves smoothly from this mark's bpm to the next mark's (the older way to write a rit.). |
| `meter` | Any time signature from any bar: `4/4`, `6/8`, additive `3+2+2/8`, `C`, `C\|`. Beats follow it (6/8 counts two dotted quarters). |
| `key` | Sharps (positive) or flats (negative) from a bar, and the mode's name. |
| `sections` | Rehearsal marks. A game moves between them by name. `loop: true` repeats a section until asked to move on; `next:` goes to another section at its end. |
| `markers` | Named points that tell scripts when playback passes them. |
| `repeats` | Bars `start`..`end` played `times` times (2 by default). |
| `voltas` | 1st/2nd endings: bars `start`..`end` played only on the `passes` listed, counted through the repeat they belong to. |
| `jumps` | `segno`, `coda` (where playback lands: the start of the bar), `to_coda`, `fine`, `dc`, `ds` (where it leaves: the end of the bar). A `dc` or `ds` can say `al: fine` or `al: coda`, and `repeats: true` to take the repeats again on the way through. |
| `playback` | `ignore_repeats: true` plays the score straight through, every bar once. `graces: before` or `on` plays every grace note before or on the beat (`auto`: acciaccaturas before, appoggiaturas on). |
| `concert` | Show every staff at concert pitch (the Concert Pitch toggle). |
| `params`, `bindings` | Values a script ramps while the score plays, bound to a line's volume, pan, sends or filter cutoff, as in the Tracker. |
| `lines` | Each line names a library instrument (`category/name`, a `.finst`), has a fader, pan and two sends, and one or more staves. A piano has two. |
| `staves` | `clef` (`treble`, `bass`, `alto`, `tenor`, `treble_8vb`, `treble_8va`, `bass_8vb`, `bass_8va`, `percussion`), an optional `transpose` (written to sounding, so a B♭ clarinet's is `-M2` and a horn in F is `-P5`), and up to four voices. |
| `gliss` | How the line's glissandi play: `slide` (one continuous pitch), `chromatic` or `diatonic` (a run). Left out, the instrument's `playback.gliss`. |
| `dynamic_overrides` | Segments of the dynamics lane dragged to a level (0..1, mf is 0.63): the line plays that level between `at` and `to` without changing a printed mark. |
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
a string quartet, a chamber ensemble (flute, clarinet in B♭ and horn in F
written transposed, violins, cello) and a rhythm section (piano, bass, drum
kit on a percussion staff), each eight empty bars of 4/4 in C.
**File > Open**, **Open Recent**, **Save** (Ctrl+S), **Save As** and **Save
All** work as in every editor, and the chip at the right of the menu bar says
whether the score matches its file.

The score is drawn as one continuous system on paper, scrolled with the
wheel (Shift+wheel across) and zoomed with Ctrl+wheel about the pointer,
Ctrl+= and Ctrl+-. Each line's header sits in the column on the left (see
*Lines* below), the time ruler along the top, and the transport in the row
under the toolbar. **View > Concert Pitch** shows transposing instruments as they
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

### Expression

Marks go on the selection, as in Sibelius: on the selected note, or — with
bars selected — an articulation or ornament on every note in them, a
dynamic or a word on each line's first note there, a fermata on each
voice's last, a slur over each voice's notes, a hairpin over each line's
first voice, a pedal line under each line's last staff, an ottava line over
each staff. The same key again takes an articulation off; a mark takes the
place of its neighbour (staccatissimo replaces staccato, marcato the accent).

| To | Press |
| --- | --- |
| Slur | `S`: from the selected note to the next; `S` again on its first note carries it one note further |
| Crescendo / diminuendo hairpin | `H` / `Shift+H`, the same way. **Marks > cresc. (Text)** and **dim. (Text)** write the words with dashes |
| Glissando | `Alt+G` (`G` is the note G): a straight line to the next note; **Marks > Wavy Glissando** for the wavy one. How it plays is the line's (right-click the header: **Glissandi Play**) or the instrument's |
| Dynamic or words | `Ctrl+E` opens the expression box: type `mf`, `sfz`, `pizz.`, `con sord.`, `dolce`, `cresc.` or `rit.` and press `Enter`, or click one of its buttons. It files what you type by what it is: a dynamic, a technique word (which switches the instrument's articulation), cresc./dim. with dashes (words around them, such as `poco cresc.` or `dim. molto`, are printed as typed), tempo text (at the selection, played as a tempo change) or expression text |
| Articulations | `.` staccato, `'` staccatissimo, `Shift+.` (`>`) accent, `Shift+-` (`_`) tenuto, `Shift+'` marcato; or the keypad's second layout (`F8`): `7` staccato, `8` tenuto, `9` staccatissimo, `+` marcato. On the first layout keypad `+` is the accent |
| Grace notes | the second layout's keypad `5` (acciaccatura) or `6` (appoggiatura) in note input: the next notes typed are grace notes; press it again and type the note they lean on |
| Move notes to another voice | `Ctrl+Alt+1`–`4` out of note input, with a note selected or with bars selected (every note in them from the other voices moves, its marks and grace notes with it; a tuplet's notes stay, since a tuplet moves whole) |
| Tempo mark or tempo text | `Ctrl+Alt+T`: words, a tempo and the beat it counts (0 for text only: rit., accel., a tempo) |
| Rehearsal mark | `Ctrl+R`: a section name at the selected bar, printed as a boxed letter with the name |

The **Marks** palette (toolbar **Marks**, `Ctrl+Alt+P`) has the rest as
buttons, lit when the selected note has them: dynamics, slurs, hairpins,
glissandi, pedal and ottava lines, articulations, ornaments (trill,
mordents, turns), arpeggios, tremolos (one to three strokes, two-note),
fermatas (short, normal, long, very long), grace notes, and the form —
**Repeat** (the selected bars, with how many times), **Ending** (the passes
it plays on: the dialog offers the next one after an ending), **Rehearsal**,
**Tempo**, **Clef** (a clef change from the selection, mid-bar too), segno,
coda, To Coda, Fine, D.C. and D.S. (al Fine, al Coda). A D.S. needs its
segno and an "al Fine" its Fine first. The **Marks** menu has all of it,
and **Marks > Remove** takes dynamics, text, lines, navigation marks or
dynamics overrides off the selection. **View > Concert Pitch**
(`Ctrl+Shift+D`) shows transposing staves at sounding pitch.

**The dynamics lane.** The **D** button on a line's header (or
`Ctrl+Shift+L` with the line selected) opens a lane under its staves. The
dashed grey curve is the level the printed marks and hairpins give; the
solid blue one is what plays. Drag across the lane to hold the line at the
pointer's level over the beats you cross — an *override*, shaded blue with
its level written on it; drag an override's line up or down to change its
level; click it and press `Delete` to give that span back to its marks. The
printed dynamics never change, each drag is one undo step, and while the
score plays the change is heard as you drag (a held string or wind note
follows it). Overrides are saved in the file as `dynamic_overrides`.

### Lines

Every line is a staff (a piano's is a grand staff with a brace, a drum kit's
a percussion staff) with a header beside it:

| Part of the header | Does |
| --- | --- |
| Name | click selects every bar of the line; double-click renames it (scripts and bindings find lines by name, so names stay unique and bindings follow a rename) |
| Instrument | click for the picker: type to search the whole `.finst` library, click (or `Enter` for the first match) to play the line on it. It works while the score plays — the notes do not change; what is sounding finishes on the old instrument and the next note is the new one's. The staff takes the new instrument's transposition (a line moved to a B♭ clarinet is written a tone up), so the page changes and the sound does not |
| `M` / `S` | mute, solo (several lines can be soloed) |
| Two squares | duplicate the line: **with notes**, or **empty** with the same instrument, clefs, transposition and mixer settings |
| Slider | the line's volume (the mixer's fader); double-click for 0 dB |
| Meter | the line's level while it plays, in decibels (empty at -48 dB, full at 0 dB; yellow from about -14 dB, red from -6 dB, a red cap over 0 dB) |

Right-click a header for the rest: rename, instrument, add a line above or
below, move up or down, mute, solo, remove. **Lines > Add Line** adds a line
for any library instrument, written on the staff the instrument asks for (a
grand staff for a piano, a percussion staff for a kit, a horn transposed);
**Add Grand Staff** and **Add Percussion Staff** add a piano and a drum kit.
Every line change is one undo step.

On a **percussion staff** each drum has its place and notehead, after the
usual drum-set notation: bass drum in the bottom space, snare in the third
space, toms between, hi-hat as an x above the staff (open hi-hat circled),
crash on the ledger line above, ride as an x on the top line. A click writes
the drum on that line or space (the ghost note's label names it), stored as
its General MIDI key, which is what the kit plays; letters and accidentals do
not apply there. Keys outside the kit (35–59) show at their own letter's
place.

### Playing: the transport and the ruler

| To | Do |
| --- | --- |
| Play / stop | `Space` or **Play**: from the play head (the green line across every staff), or from the loop's start when the loop is on and the head is outside it. Stopping leaves the head where the music stopped |
| Play from the selection | `P` |
| Move the play head | click the ruler (it snaps to the nearest beat), or click a note |
| Back to the start | `Home` or **Start** (the loop's start when the loop is on) |
| Loop | drag along the ruler between two beats; or select bars and press `L`. `L` with no bars selected, **Loop** and `Shift+L` switch the region on and off (off, it stays on the ruler, grey) |
| Follow | **Follow** scrolls the score to keep the play head in sight |
| Tempo | the **Tempo** slider plays at 25 %–200 % of the written tempo without changing the score (double-click: 100 %). **Write** puts it into the score: every tempo mark scaled to the slider, or one tempo mark at the selected bar (or the play head's) with the tempo heard there — then the slider goes back to 100 % |

The readout shows the bar (with *x2* on a repeat's second time), the beat and
the time as heard at the slider's speed; the ruler shows each bar's number
and the time it first plays at.

### The mixer

The **Mixer** tab (`M`; `F10` in the MuseScore keymap) shows a strip per
line — its reverb and delay sends and pan as knobs (drag up or down,
double-click to reset), its fader with a meter, the level in dB, mute and
solo — then the two send buses' returns (bus A's reverb, bus B's delay) and
the master fader with the mix's meter. The faders are the same values the
staff headers show, kept in the file, and they are heard at once while the
score plays; each drag is one undo step. A line's insert effects are listed
as the file has them (the effects rack that plays them comes later).

### Exporting audio

**File > Export Audio (WAV)** renders the whole score, the loop region or
the selected bars — through the same scheduler the game uses, with the
mixer's mutes and solos, at the written tempo or the slider's — plus a tail
for the reverb to ring out, as a 48 kHz 24-bit stereo WAV. A score that
loops (`loop: true`) exports one pass: the music stops at the end and the
tail is its ring-out, not the top again. It goes to
`exports/<name>.wav` beside `resources/` unless you type another path.

### The keypad, the keymap and F1

The keypad panel (toolbar **Keypad**, `Ctrl+Alt+K`) mirrors the numeric
keypad in Sibelius's layout and is clickable; the selected note's value,
dot, accidental, accent, tie and rest are lit, and its **More** tab is the
second layout (64th to breve, grace notes, articulations). **F1** in the Score window shows the active keymap over the score.

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
| Dynamics | `pppp` … `ffff`, `niente`; the accents `sf`, `sfz`, `sffz`, `fz`, `rf`, `rfz`; `fp`, `sfp`, `sfpp` |
| Articulations | `stacc`, `staccmo`, `acc`, `ten`, `marc`, `portato`, `legato` |
| Ornaments | `tr`, `mord`, `imord`, `turn`, `iturn` (value: the auxiliary's accidental, `tr=#`, a turn's `turn=b/#` upper/lower); `trem=n` (1–4 strokes, bare 3), `trem2=n` (a two-note tremolo with the next event) |
| Notation | `ferm` (`ferm=short`, `long`, `verylong`), `arp` (`arp=down`), `up`, `down`, `on_beat` / `before_beat` (a grace group's timing) |
| Text | `"dolce"` (`text=`), `expr=`, `tech=` (playing technique: `tech=pizz.`, `tech="con sord."`) |
| Spanners | `cresc`, `dim` and `hairpin_end` (`cresc=text` prints "cresc." with dashes, any other value prints itself); `slur` and `slur_end`; `gliss` (to the next note; `gliss=wavy`); `8va`, `8vb`, `15ma`, `15mb` and `ottava_end`; `ped` and `ped_end`. An end mark goes on the last note the line covers. |

A name the build doesn't know is kept and saved, and `foundry score inspect`
warns about it. Nothing is lost when an older build opens a newer file.

## How a score plays

- **Time is exact.** Note values and tuplets are kept as exact fractions, so
  a note after a thousand triplets is exactly where it was written. Every
  note starts on the exact sample its time falls on.
- **Form.** Repeats unroll; an ending plays only on its passes; a D.C. goes
  back to the top and a D.S. to the segno, once each, and on that time
  through a Fine ends the piece and a To Coda goes on to the coda. Repeats
  are played once after a jump, with their last ending, unless the jump says
  `repeats: true`. `playback: {ignore_repeats: true}` plays it as written.
- **Tempo** follows the tempo marks. `rit.`/`rall.`/`allarg.` slow down and
  `accel.`/`string.` speed up gradually from where they are to their `to:`
  or the next mark: to that mark's bpm when it has one, else by a quarter
  (`poco` half that, `molto` half again). `a tempo` goes back to the last
  tempo set by a number, `tempo primo` to the first, `meno mosso` and `più
  mosso` step 15% slower or faster.
- **Fermatas** hold their note, and everything sounding with it, twice as
  long (`short` 1.5, `long` 3, `verylong` 4): the tempo under it is divided.
- **Dynamics** set the level of the whole line from the mark on (every voice
  and staff). Before the first one a line plays at mf. Hairpins and
  `cresc.`/`dim.` text ramp it in a straight line to the dynamic at their
  end, or one step (mp to mf) when there is none; without a `hairpin_end`
  they run to the next dynamic. `sfz` and the other accents, and `fp`'s
  forte, act only on the notes they are on. The level picks the
  instrument's velocity layer and sets the volume. A held note on an
  instrument that sustains (strings, winds) follows the level while it
  sounds; a piano's or a harp's keeps the level it started at. The
  dynamics lane's overrides replace the level over their span.
- **Accents** start a note louder: `acc` by a quarter, `marc` by 40%, `ten`
  a little. On a held note they fall back within a 16th.
- **Lengths.** `stacc` plays the instrument's staccato length (half the
  note by default), `staccmo` half that, `portato` (or staccato with
  tenuto) and staccato under a slur three quarters, `marc` four fifths.
  Under a slur each note runs a 64th into the next and asks for the
  instrument's `legato` articulation.
- **Glissandi** go from their note to the next: a slide bends the note over
  the last three quarters of it and carries on as the next note without a
  new attack; a run plays every semitone (chromatic) or every note of the
  key (diatonic) between.
- **Ornaments** on a chord's top note: a trill alternates with the note
  above about 12 times a second, starting and ending on the note; mordents
  and turns in 32nds and 16ths. Neighbours are the key's, after any
  accidental earlier in the bar, unless the mark names one.
- **Tremolo**: `trem=n` re-strikes the note at its value with n more beams;
  an unmeasured one (3 strokes) on an instrument with a `tremolo`
  articulation plays that instead. `trem2=n` alternates two notes.
- **Arpeggios** roll from the bottom (`arp=down`: the top), a 64th a note.
- **The pedal**: a note that ends while it is down sounds until it comes up;
  a `ped` while it is down re-pedals.
- **Written, not sounding.** Ottava lines, clef changes and a staff's
  transposition change only how notes are written; the stored (concert)
  pitches play.
- **Ties** join into one held note, across bars and repeats.
- **Grace notes.** An acciaccatura is crushed in just before the beat, taking
  its time from the note before it. An appoggiatura takes time from the
  note it leans on, at most half of it. `playback: {graces: …}` or an
  `on_beat`/`before_beat` mark on the group changes which.
- **Articulations and technique** choose the instrument's articulation:
  `(stacc)` plays its staccato samples, and `tech=pizz.` switches the line to
  pizzicato until `arco` or `ord.`. Technique words replace only their own
  kind: `con sord.` lasts through `pizz.` and `arco` until `senza sord.`.
- **Lanes** move the line's volume and pan.

A MIDI export writes a slide as the chromatic run it passes, since a note
in a MIDI file cannot bend.

## Instruments

A line names a `.finst` from the shared instrument library under
`resources/sounds/instruments/` by its path there without the extension
(`strings/violin_section`). The Tracker's song slots name the same files, so
a song and a score can share every instrument. The starter library includes an
orchestral set: string sections with sustain, staccato, pizzicato, tremolo and
muted articulations; woodwinds; brass; grand piano, celesta, harp and organ;
choir; timpani and tuned percussion; a General MIDI drum kit; synths.

A `.finst` is YAML: the instrument's samples (named from `sounds/samples/`),
its keymap zones with velocity layers and round-robin takes, its envelopes,
filter and vibrato, its **articulations**, and its **playback** defaults:

```yaml
version: 1
name: Violin Section
samples: [orchestral/vln_sus_soft_g3, orchestral/vln_sus_loud_g3]
zones:
  - sample: 1
    layers:
      - {sample: 0, velocity: [0, 96]}
      - {sample: 1, velocity: [64, 127], gain: -1}
articulations:
  - {name: pizzicato, triggers: [pizz., pizz], zones: [...]}
playback:
  range: [G-3, E-7]      # sounding
  transposition: 0       # written to sounding, semitones
  clef: treble           # the staff Lines > Add Line writes it on (grand, percussion …)
  gliss: slide           # slide, chromatic or diatonic
  staccato: 0.4          # fraction of its length a staccato note sounds
  release: 0             # seconds a note rings past its written end
```

How a score uses it:

- **Velocity layers.** The dynamic a note is played at (from the marks) is
  its velocity, which picks the layer, crossfading where layers overlap.
- **Articulations.** A mark (`stacc`) or technique text (`tech=pizz.`) picks
  the articulation whose name or `triggers` match; `arco` and `ord.` go back
  to the instrument's own zones.
- **Playback defaults.** The clef and transposition choose the staff a new
  line gets, and the range is what `foundry score inspect` checks notes
  against.

The whole format — every key, and the `gen-instruments` command that builds
the library — is in [the instrument library](31-music-tracker.md#the-instrument-library).

## A score in a level

A level's `music:` block names a score by its path under `sounds/` without
the extension, exactly as it names a recording or a song, and
`play_music("hangar_theme")` switches to one from a script. If a `.ftrk` and
a `.fscore` share a name, the song plays. The score plays live, through the
same scheduler the editor's transport uses, as one voice on the engine's
**music** bus: the player's music slider, the fades, the master gain and the
limiter reach it, and it never counts against the cap on looping sounds that
ambience and engine loops share.

| Key | |
| --- | --- |
| `track` | the score |
| `section` | the rehearsal mark (one of `sections:`) to start at; the top when left out |
| `params` | the score's params, 0 to 1, before the first note |
| `gain`, `fade`, `loop` | as for any track. A score loops by its own `loop:`; `loop` here is ignored for scores and songs |

A level load reads the score and every instrument its lines play, so the
music starts without touching the disk. Scores are steered by the line names
and the section names in the file, so renaming a line in the editor renames
what a script mutes (the editor's bindings follow a rename; scripts do not).

A release ships every score a level or a script names (a `music:` block,
`play_music`, `play_song`, `song_stinger`), the library instruments their
lines play and those instruments' samples, and nothing else from the
library. **Window > Unused Assets** (`foundry find-unused`) follows the same
references, so a score that nothing names is listed there, and the
instruments only it plays with it.

## Scripting a score

A playing score is steered by the Tracker's song verbs, and it answers with
the song events (see [Scripting a song](31-music-tracker.md#scripting-a-song)).
For a score, the **sections** are its rehearsal marks, the **channels** its
lines, and the **markers** its `markers:`. Every change waits for a boundary:
`"beat"` (the default), `"bar"`, `"section"` (when the playing section ends;
the end of the piece when it has none) or `"now"`. `"pattern"` is a bar.

| Verb | With a score |
| --- | --- |
| `play_music(name, fade)` | switch the level's track to the score |
| `play_song(name, section, fade)` | play the score from a rehearsal mark; if it is already playing, move there on the next beat instead |
| `song_section(name, quantise)` | move to a rehearsal mark at the next boundary. A section with `loop: true` repeats until asked to move on, and one with `next:` goes there at its end |
| `song_param(name, value, seconds)` | ramp one of the score's params (0–1). Its `bindings` turn it into a line's volume, pan, sends or low-pass cutoff |
| `song_mute(line, on, fade)` | fade a line out (`true`) or back in; by name or number from 1 |
| `song_tempo(bpm, seconds)` | the tempo slider: the bar playing now goes at `bpm` quarters a minute and every tempo mark follows in proportion (25 % to 200 % of the written tempo) |
| `song_stinger(name, quantise)` | play a short score — or a song — once over this one, from its top, starting on the boundary (a loop or section jump counts as one) |
| `play_note(line, note, volume, seconds)` | one note on a line's instrument, outside the score; `note` is `"C-4"` or `"C#4"` |
| `song_position()` | `{playing, song, section, bar, pass, beat, tick, seconds}`: the written bar, the time through it (`pass` 2 on a repeat), the beat from 0 in the bar, the tick (960 a quarter, in playing order) and the score's seconds |

| Event | Args | With a score |
| --- | --- | --- |
| `:song_marker:` | `name str, value int` | a marker played; `value` is the bar it is in |
| `:song_section:` | `name str` | a rehearsal mark's section started (also at the start, when the score starts on one) |
| `:song_beat:` | `beat int, section str` | every beat, counted from 0 in the bar; a score needs no `beat_events` switch. 6/8 has two beats, 3+2+2/8 three |
| `:song_bar:` | `section str` | each bar's first beat, after its `:song_beat:` |
| `:song_end:` | `song str` | a score without `loop: true` ran out |

```
-- in the level's script
def on_fight(:fight_started:) -> null
    song_section("battle", "bar")
    song_param("intensity", 1, 4)
    song_mute("Choir", false, 2)
end

def on_bar(:song_bar:, section str) -> null
    if section == "battle"
        play_sound("war_drum")
    end
end
```

The level's hard pause (`@level.prop.paused`) freezes the score where it is,
silent, and it carries on mid-phrase when the game resumes. A save game
records the score, the tick it was at, its section, whether it was playing,
and the params and line mutes scripts set; loading it starts the score at
that tick with those settings. A headless `foundry play` has no audio device,
so no score sounds there, but `music.track` and `music.section` read what the
level and its scripts asked for.

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
