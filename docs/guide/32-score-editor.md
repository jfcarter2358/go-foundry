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
subtitle: from the Hangar suite  # optional: printed under the title
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
playback: {ignore_repeats: false, graces: auto, swing: {amount: 0.62, unit: 8}, humanize: {timing: 8, velocity: 0.06, seed: 1}}
concert: false
params: [{name: intensity, default: 0}]
bindings: [{param: intensity, line: Horn, target: volume, from: 0, to: 1}]
lines:
  - name: Violin I
    short: Vln. I                # optional: the name on systems after the first
    instrument: strings/violin_section
    volume: 0.8
    pan: -0.3
    sends: {a: 0.3, b: 0, pre_a: true}   # pre_a/pre_b: the send is taken before the fader
    inserts: [{fx: eq, low: -2, b2: 3, b2_freq: 2500}, {fx: compressor, threshold: -18, bypass: true}]
    swing: {amount: 0.5}         # optional: this line's own swing (0.5 is straight) or humanize
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
      volume: [{at: "5", value: 1}, {at: "6:3", value: 0.6, step: true}, {at: "7", value: 0.9}]
      eq.low: [{at: "1", value: -2, curve: 0.5}, {at: "9", value: -8}]
buses:
  a:
    fx: [{fx: reverb, size: 0.7}]
    return: 0.25
    lanes:                       # optional: the bus's return, pan and effects' settings
      reverb.size: [{at: "1", value: 0.5}, {at: "9", value: 0.9}]
  b: {fx: [{fx: delay, beats: 0.75, feedback: 0.3}, {fx: filter, mode: 1, cutoff: 400}], return: 0.2, pan: 0.2}
master: [{fx: compressor, threshold: -12, ratio: 3}, {fx: limiter, ceiling: -0.3}]
master_lanes:                    # optional: the master's volume and its effects' settings
  volume: [{at: "14", value: 1}, {at: "16", value: 0, curve: -0.5}]
layout:                          # optional: how the score and its parts print
  paper: a4                      # letter (default), a4, tabloid, a3, legal, or custom with size: [w, h] mm
  landscape: false
  margins: {top: 15, bottom: 15, left: 14, right: 14}   # mm; 12.7 each by default
  staff: 6.5                     # staff height in mm (3-12; 7 by default)
  system_gap: 10                 # least room between systems, staff spaces (9 by default)
  spacing: 0.9                   # note spacing, 0.5-2 (1 by default): lower fits more bars a system
  breaks: [{bar: 9}, {bar: 13, page: true}]             # a new system (or page) at these bars
  title_page: false
  page_numbers: true
  names: full_then_short         # full_then_short, full, short or none
  cautionary: true               # clef, key and time at a system's end for the next one's change
  courtesy: false                # courtesy accidentals in the bar after an altered note
  brackets: true                 # a bracket down each family of two lines or more (false: none)
  hide_empty: false              # true: a system after the first leaves out lines with no notes in it
  parts:                         # a part's own settings, by line name
    - {line: Violin I, staff: 7.5, breaks: [{bar: 5}], multi_rests: true}
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
| `playback` | `ignore_repeats: true` plays the score straight through, every bar once. `graces: before` or `on` plays every grace note before or on the beat (`auto`: acciaccaturas before, appoggiaturas on). `swing` and `humanize` are the score's feel (see *How a score plays*); a line may have its own. |
| `concert` | Show every staff at concert pitch (the Concert Pitch toggle). |
| `params`, `bindings` | Values a script ramps while the score plays, bound to a line's volume, pan, sends or filter cutoff, as in the Tracker. |
| `lines` | Each line names a library instrument (`category/name`, a `.finst`), has a fader, pan, two sends (post-fader unless `pre_a`/`pre_b`), an insert chain, and one or more staves. A piano has two. |
| `inserts` | The line's effects, in signal order (see *Effects*). Each is `{fx: <kind>, <setting>: <value>, …}`; a setting left out takes its default, `bypass: true` keeps the effect in the chain but out of the signal. The editor holds up to 8 a chain. |
| `staves` | `clef` (`treble`, `bass`, `alto`, `tenor`, `treble_8vb`, `treble_8va`, `bass_8vb`, `bass_8va`, `percussion`), an optional `transpose` (written to sounding, so a B♭ clarinet's is `-M2` and a horn in F is `-P5`), and up to four voices. |
| `gliss` | How the line's glissandi play: `slide` (one continuous pitch), `chromatic` or `diatonic` (a run). Left out, the instrument's `playback.gliss`. |
| `dynamic_overrides` | Segments of the dynamics lane dragged to a level (0..1, mf is 0.63): the line plays that level between `at` and `to` without changing a printed mark. |
| `lanes` | Automation, followed sample by sample. `volume` multiplies the fader (and the dynamics: the two multiply), `pan` replaces the pan, `cutoff` is a low-pass over the line in Hz, `send_a`/`send_b` replace the sends, and `<fx>.<setting>` moves one of the line's inserts: `eq.low` the first EQ's low shelf, `delay#2.feedback` the second delay's feedback. Each point is a position and a value; `to:` holds the value flat to a second position. From a point to the next the value moves in a straight line, `step: true` holds it and jumps at the next point, `curve: -1..1` bends the line (above 0 it starts slowly). |
| `buses` | Send buses `a` and `b`: their effect chain (`fx:`, wet only unless a `mix` says otherwise; an empty chain returns what it is sent), `return` level, `pan` and `mute`, and `lanes:` — `return` (replaces the return level), `pan` (replaces the pan) and `<fx>.<setting>` for the bus's own effects (`reverb.size`). |
| `master` | The chain over the whole mix, before the master `gain`. |
| `subtitle` | Printed under the title on the first page (or the title page). |
| `short` | A line's name on every system after the first ("Vln. I"). Left out, one is made from the name: the usual orchestral abbreviations (Violin → Vln., Clarinet in B♭ → Cl. in B♭, Double Bass → D.B.), numbers kept, other words cut to three letters. |
| `layout` | How the score prints (see *Pages, parts and printing*): the paper and its margins (margins that would leave the music less than 25 mm either way are made smaller, with a warning), the staff size, the room between systems, the note spacing, the system and page breaks (`bar:` is the bar that starts the new system or page), a title page, page numbers, instrument names, cautionary signatures and courtesy accidentals, the family brackets (`brackets: false` leaves them out) and hidden empty staves (`hide_empty: true`). `parts:` gives a line's part its own staff size, system gap, paper and breaks (the score's breaks are not the part's), and `multi_rests: false` to print every rest bar on its own. Nothing here changes how the score plays. |
| `master_lanes` | The master's automation: `volume` (multiplies `gain`: a fade-out of the whole score) and `<fx>.<setting>` for the master chain's effects (`limiter.ceiling`). Points are written as a line's. |
| `chords` | A line's chord symbols, over its top staff: `[{at: "1", sym: Cmaj7}, {at: "2:3", sym: D7b9/F#}, {at: "5", sym: N.C.}]`. Each is read as a musician types it and written back in one spelling (see *Lyrics, chord symbols, drums and tablature*). |
| `chord_play` | How the line's chord symbols play: `{style: pad}` (each held to the next), `{style: comp}` (struck on every beat, the bass on the chord's first stroke and on downbeats) or `off`; `instrument:` plays them on another library instrument (the line's own when left out), `volume:` 0..2 (1, the default, sets the whole chord a little under one of the line's notes), `octave:` moves the voicing. |
| staff `lines` | A percussion staff's line count, 1–5: `lines: 1` for a single drum (a tambourine, a triangle). |
| staff `tab` | Tablature: `{tuning: guitar}` (or `strings: [D2, A2, D3, G3, B3, E4]`, lowest first), `capo: 2`, `only: true` for tablature alone instead of under the notation. The tunings: `guitar`, `guitar_drop_d`, `guitar_eb`, `guitar_dadgad`, `guitar_open_g`, `guitar_open_d`, `guitar_7`, `bass`, `bass_5`, `bass_6`, `ukulele`, `mandolin`, `banjo`. |

Positions are written `bar` or `bar:beat`, with beats counted the way the
bar's meter counts them: `5`, `5:3`, `5:2+1/2`.

### Effects

Every chain — a line's inserts, a bus, the master — takes the same
effects. Frequencies are in Hz, levels in dB, `mix` 0..1 is the wet share
(on a bus it is 1 unless set, so the bus returns only what the effect
makes). Settings glide when they change, so a knob turned while the music
plays never clicks.

| `fx:` | Settings (default) |
| --- | --- |
| `eq` | `low` shelf dB (0) at `low_freq` (120), `high` shelf dB (0) at `high_freq` (8000), bands `b1`–`b4` dB (0) with `b1_freq` … (250, 1000, 3000, 8000) and `b1_q` … (1), `output` dB (0). A band at 0 dB is left out. |
| `filter` | `mode` 0 low-pass, 1 high-pass, 2 band-pass; `cutoff` (1000), `resonance` 0..1 (0.2), `slope` 0 for 12 dB, 1 for 24 dB an octave; `mix` (1). |
| `chorus` | `rate` Hz (0.8), `depth` ms (2.5), `delay` ms (12), `feedback` -0.9..0.9 (0), `spread` 0..1 (1), `mix` (0.5). |
| `phaser` | `rate` Hz (0.5), `depth` 0..1 (0.7), `freq` centre (800), `feedback` (0.5), `stages` 2–12 (6), `spread` (0.5), `mix` (0.5). |
| `drive` | `drive` 0..1 (0.3), `tone` 0..1 (1, open), `mix` (1), `output` dB (0). Quiet signals pass at their level; only peaks saturate. |
| `crusher` | `bits` 1–16 (8), `downsample` 1–64 (1), `mix` (1). |
| `delay` | `beats` (0.75: a dotted eighth, in time with the tempo, gliding when it changes), `feedback` 0..0.95 (0.35), `tone` 0..1 (0.6), `ping_pong` 0/1 (1), `mix` (0.3). |
| `reverb` | `size` (0.6), `damping` (0.4), `width` (1), `mix` (0.25). |
| `compressor` | `threshold` dB (-12), `ratio` (3), `attack` s (0.005), `release` s (0.15), `makeup` dB (0), `enabled` 0/1 (1). |
| `limiter` | `ceiling` dB (-0.3), `gain` dB in (0), `release` s (0.1). Looks 1.5 ms ahead: nothing passes the ceiling. |

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
whether the score matches its file. A score the Claude panel rewrites is
re-read when the editor holds it with no edits of its own (with edits, the
panel is told and they stay), and a level that plays it next hears the new
file; an instrument it rewrites is heard on the next note.

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
| Cross staff | `Ctrl+Shift+Up` / `Down` (**Notes > Cross to Staff Above / Below**): the selected chord's notes — or every chord of the selected bars — written on the grand staff's other staff, in that staff's clef, while they stay in their own voice; a beam joining notes on both staves is flat in the gap between them, each staff's stems reaching it from its side. The same key back takes them home. Stored as the mark `cross=up` / `cross=down` |
| Delete | `Delete`: a note becomes a rest, one note of a chord goes, a rest in voices 2–4 is hidden; selected bars are cleared |
| Previous / next note | `Left` / `Right` |
| Key, time, clef | `K`, `Ctrl+Shift+T`, `Q` (or the toolbar): a key or time signature from the selected bar; a clef for a whole staff |

Changing the time signature **re-bars** the music up to the next time
signature, as Sibelius's "rewrite bars" does: the notes keep their time
and are written again across the new barlines — tied where a note now
crosses one, beamed by the new meter, tuplets where their notes need them,
every spelling, mark, syllable and grace note kept, the last bar filled
out with rests — and the bars after it move with them (chord symbols,
tempo and rehearsal marks, clef changes, lanes, repeats, endings and breaks
in the passage keep their moment; after it, their music). With **Re-bar
the music** unticked in the dialog each bar keeps its own notes instead and
is fitted to the new length (music past a new barline cut, a short bar
filled with rests). Ctrl+Z brings either back. Changing the key never
changes a note — the pitches are spelled — only what the page prints.

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

### Transposing, respelling, finding

**Notes > Transpose** (`Shift+T`) moves the selected bars (or, with nothing
selected or **The whole score** chosen, everything) **by an interval** —
up or down, a unison to an octave and up to three octaves more, spelled as
the interval implies (C up a minor third is E♭) — or **to a key** (from the
key at the passage's start: the closest way, or up, or down). **Diatonic**
moves by letters within each bar's key instead (C up a third is E in C
major), the key signatures as they are. When every line moves, the key
signatures move with them (**Transpose key signatures**); a key that would
need more than seven sharps or flats — or more than six where its
enharmonic needs fewer — is written as that enharmonic (D♭ up a minor
second is D major, not E♭♭), the notes spelled for it. Chord symbols move
with the music unless unticked; double sharps and flats a transposition
would spell are written with one accidental unless **Use double sharps and
flats** is ticked. Drum staves never move; a tablature's string choices are
worked out again. A note the move would take off the keyboard stays where
it is and the status line counts it. One undo step.

**Notes > Respell Selection** gives every selected note its other spelling
(F♯ ↔ G♭); **Notes > Spell in Key** writes every selected note — the whole
score with nothing selected — as its bar's key spells the sound: the key's
own note where it is one, else a sharp where the line rises to it and a
flat where it falls (what an import or a chromatic transposition can leave
untidy). `Return` respells the one selected note, as before.

**Edit > Find** (`Ctrl+F`) opens a small window over the score: type a
**pitch** (`F#4` by sound, so `Gb4` is found too unless **Exact spelling**
is ticked; `F#` in any octave; a range `C4..G5`), a **mark** by its musical
name (`staccato`, `accent`, `fermata`, `trill`, `slur`, `hairpin`, `pedal`,
`tie`, `grace`, `tuplet`, `dynamic`, or a dynamic itself: `mf`) or any
**text** (an expression, a technique word, a lyric, a chord symbol, a
rehearsal mark, tempo text), and press `Return`. **Anything** decides which
from what was typed. The matches are listed by line, bar and beat; the
first at or after the selection is selected and scrolled to, and `Ctrl+G`
(or `F3`) and `Ctrl+Shift+G` (`Shift+F3`) step to the next and previous,
wrapping round, with the window closed too. **In the selection** searches
only the selected bars.

### The command palette

`Ctrl+Shift+P` (**View > Command Palette**) lists every command the editor
has — each with its keys in the active keymap — and the menus' other
actions (a new score from a template, a recent score, Save All, swing and
humanise, the instrument library). Type words to narrow it: each must be in
the command's name, its group or its keys (`voice 2`, `trans`,
`ctrl+shift+f`); the best matches come first. `Up` and `Down` move, `Return`
runs, `Escape` or a click elsewhere closes it.

### A MIDI keyboard

**Edit > MIDI Input** chooses the keyboard: every MIDI source the system
offers (ALSA's sequencer on Linux — USB keyboards, virtual ports, other
programs — WinMM's devices on Windows), the channel to listen to (or all),
and **MIDI thru**, which plays each key on the selected line's instrument as
it is pressed. The choice is kept in `score.yaml` and the keyboard is opened
again when the Score window next opens; one unplugged mid-session is
noticed and said so. The dialog shows the last message heard, which is the
quickest check that a keyboard reaches the editor (`foundry score midi
-monitor 10` does the same from a terminal).

**Step entry.** In note input a key writes a note of the chosen value after
the selection, as Sibelius does; keys played while it is still held join it
as a chord. A key is a sound: what is written is the pitch it played,
spelled in the key signature (a sharp when the line rises to a note not in
the key, a flat when it falls), shown where the staff shows that sound — a
B♭ clarinet's written pitch, under an ottava line an octave down. On a
percussion staff a key is the drum the line's drum map plays on it. Values,
dots, rests and ties stay on the computer keyboard (keypad `1`–`6`, `.`,
`0`, `Enter`). Out of note input the keyboard only sounds.

**Recording.** **Play > Record** (`Ctrl+Shift+F`, the **Rec** button on the
transport) records into the first **armed** line (the **R** on a Timeline
track), else the selected one, from the selected bar (else the play head's):
a count-in of one bar of clicks (0–4 in the dialog), then the score plays,
with a click on every beat unless **Click** is off, and every key is kept
with its moment — one played a little early in the count-in's last beat
goes on the downbeat. Space, the record command again or the score's end
stops it, and the take is written over the bars it covered exactly as the
MIDI importer writes a file's part: quantised to the grid the dialog chooses
(16ths and triplets by default), split onto a grand staff at middle C,
dealt into up to the dialog's number of voices (two by default), small gaps
closed, notes played as flicks before the beat made grace notes, velocity
read as dynamics, the sustain pedal as pedal marks — one undo step. The
recorded line's old notes play while you record over them. A take stops
where the performance comes back to a bar it has played (a repeat's second
time), and the loop is off while recording. **Latency** in the dialog
moves every key earlier by what the audio device delays the click and the
music by, should takes come out late.

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

### Lyrics, chord symbols, drums and tablature

| To | Press |
| --- | --- |
| Type lyrics | select a note, `Ctrl+L` (`Ctrl+Alt+L` for verse 2): a field opens under it. `Space` goes to the next note (the word ends), `-` to the next note with a hyphen (the word goes on), `_` holds the syllable over the next note too (a melisma, with an extender line), and `Space` in an empty field holds the syllable before over this note. Rests, grace notes and notes tied over are passed over. `Backspace` in an empty field goes back a note, `Tab` moves on keeping what is typed, `Ctrl+Alt+L` in the field moves to the next verse, `Return` or `Esc` finish. Each syllable is one undo step |
| Type a chord symbol | select a note or a bar, `Ctrl+K`: a field opens over the staff. Type the symbol as you would write it — `Cmaj7`, `CΔ7`, `F#m7b5`, `Fø7`, `Bb7(b9,#11)/D`, `G7sus4`, `C6/9`, `Eadd9`, `A7alt`, `N.C.` — then `Space` for the next beat or note (whichever comes first), `Return` to finish. A symbol that will not read stays in the field with the reason in the status line; an empty field takes the symbol away. Click a chord symbol to edit it |
| Drums by letter | on a percussion staff `A`–`G` write the drum map's sound with that shortcut (the starter kit: `B` bass drum, `A` snare, `G` closed hi-hat, `F` pedal hi-hat, `C` crash, `D` ride, `E` high tom), `Shift`+letter adds it to the chord, `Up`/`Down` walk a note through every sound of the kit, and an articulation the map names (**Marks > Open / Stopped**: an "o" on a closed hi-hat) plays that sound |
| Frets | in note input a click on a tablature string writes that string open; the number keys type the fret on the string in hand (two digits within a second: 10 and up); `Up`/`Down` move to the string above or below, `Alt+Up`/`Down` move the selected note to another string keeping its pitch. Out of note input, click a fret number and type |

A line's menu has **Chord Symbols** (printed only, or played as a pad or as
comping, on the line's instrument or another) and **Tablature** (none, under
the notation, alone; the tuning; the capo). **Marks** has the lyrics and
chord symbol commands, **Clear Lyrics in the Selection** and **Open /
Stopped**.

**Lyrics** are marks on the notes they are sung on (`ly=` the first verse,
`ly2=` … `ly8=`): `qC4(ly=Hal-) qD4(ly=le-) qE4(ly=lu-) hF4(ly=jah_)`. A
trailing `-` hyphenates the syllable to the verse's next; a trailing `_`
holds it with an extender to the verse's next syllable or the next rest. A
value with other characters is quoted: `(ly="don't")`. Each verse prints in
a line of its own under the staff, all of a system's at one height, below
the lowest note; syllables are centred under their notes (a melisma's first
syllable set at its note's left), hyphens between a word's syllables (more
along a long gap), and spacing keeps two syllables apart, across barlines
too. A hyphen or extender over a system break runs to the end and on from
the next system's start.

**Chord symbols** stand over the line's top staff at one height, each at its
place; a change inside a held note gets a column of its own. They print as a
lead sheet does: the root large, its flat or sharp the chord-symbol glyph,
the rest smaller — `m`, `maj7`, `7(♭9,♯11)` — with ° and ø for diminished
and half-diminished. On a transposing staff at written pitch they are
transposed with it. What is read, in order after the root: the quality (`m`,
`min`, `mi`, `-`; `dim`, `°`, `o`; `m7b5`, `ø`, `h`; `aug`, `+`; `sus2`,
`sus4`, `sus`, both together `sus2sus4`; `5`), the seventh (`7`, `maj7`,
`M7`, `Δ`, `dim7`; minor-major `mmaj7`, `m(maj7)`, printed `m(maj7)`),
extensions (`9`, `11`, `13`, `6`, `6/9`), alterations (`b5`, `#5`, `b9`,
`#9`, `#11`, `b13`, `alt`; a `+` with no number after the seventh, `C7+`,
is `#5`), additions (`add2`, `add9`, `add#11` …; a pop chart's `C2` is
`Cadd2`), omissions (`no3`, `no5`) and a slash bass (`/E`). The file keeps
one spelling of each (`CΔ7` is `Cmaj7`, `Cø` is `Cm7b5`). Played (`chord_play`),
a chord is voiced close: the bass (its slash bass, or its root) in the octave
below the bass clef's middle line, the chord's notes stacked from its root
around the E below middle C (a dominant 13th leaves out its 11th, as players
do); where the music jumps back the symbol in force there sounds again; the
notes take the line's dynamics, pedal and mixer strip.

**Drum maps** are the instrument's (the `.finst`'s `playback: drums:`, see
*Instruments*): each sound has the MIDI key that plays it, a staff position,
a notehead (`normal`, `x`, `circle_x`, `diamond`, `triangle`, `slash`), an
optional articulation it is written with (`open`, `stopped`, …: two sounds
can share a place and a head, an open and a closed hi-hat) and an optional
shortcut letter. A single drum's instrument asks for a one-line staff
(`playback: staff_lines: 1`): the note sits on the line, the barlines reach
a space either side, there are no ledger lines.

**Tablature** is the staff's own notes on the strings, not a copy: under
the notation (a second staff, string 1 on top, fret numbers on the lines,
the line broken round each number) or alone (with the rhythm under the
strings — stems, beams, flags, dots and rests). Each note goes on the string
the music walked from its start puts it on: a string a note of a chord, the
hand's stretch as small as it can be, near where it was for the note before,
open strings and low frets first; a note tied over shows its fret in
brackets, and a note the tuning cannot reach is left off (`foundry score
check` warns). The `str=` mark keeps a note on a string of your choosing:
`qE4(str=2)`, `hG3+D4(str=G3/3+D4/2)`. Frets count from the capo. Where
the music starts, a tuning other than the instrument's standard one and a
capo head the tablature, as guitar books print them: the retuned strings
circled (`⑥ = D` for drop D; `Tuning: D A D F♯ A D` when most strings
move) and `Capo 2`.

Flat, sharp and natural signs typed into any text — a title, a part's
words (`text="F♯ dolce"`), a syllable, a tempo mark — are drawn as music
signs in every view and export (the text fonts have none; they come from
the music font). Letters the text fonts lack altogether (CJK) are left out.

### Lines

Every line is a staff (a piano's is a grand staff with a brace, a drum kit's
a percussion staff) with a header beside it:

| Part of the header | Does |
| --- | --- |
| Name | click selects every bar of the line; double-click renames it (scripts and bindings find lines by name, so names stay unique and bindings follow a rename); drag it up or down to move the line there |
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

On a **percussion staff** each drum has its place and notehead from the
line's **drum map** — its instrument's (see *Lyrics, chord symbols, drums
and tablature*; an instrument with none uses the General MIDI kit's): bass
drum in the bottom space, snare in the third space, toms between, hi-hat as
an x above the staff (open, with an "o" over it), crash on the ledger line
above, ride as an x on the top line. A click writes the first drum on that
line or space (the ghost note's label names it), stored as the key that
plays it.

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
line — its insert effects, its reverb and delay sends and pan as knobs
(drag up or down, double-click to reset), its fader with a meter, the level
in dB, mute and solo — then the two send buses (their effects, the return's
pan, fader, meter and mute) and the master (its chain, fader and the mix's
meter). The faders are the same values the staff headers show, kept in the
file, and everything is heard at once while the score plays; each drag is
one undo step.

| On a strip | |
| --- | --- |
| **+ Insert** | add an effect to the end of the chain; its settings open |
| An effect's name | its settings: a slider for each (double-click: the default), Bypass, Reset, Remove. Heard as you move them |
| The dot | bypass the effect (grey) or put it back (green) |
| Right-click an effect | Settings, Bypass, Move Up, Move Down, Reset to Defaults, Remove |
| Right-click a send knob | make the send pre-fader (marked **pre**: the bus hears the line whatever its fader does) or post-fader again |

An effect a lane moves says so in its settings; while the lane plays, the
lane's value wins. Moving or removing an effect takes its lanes with it.

**Play > Swing and Humanise…** sets the score's swing (50 % straight to
75 %, on eighths or sixteenths) and humanise (timing ±ms, velocity ±%, a
seed), or — with one line's bars selected, or picked in the popup — a
line's own, which then replaces the score's for it. Each slider drag is
one undo step and is heard as the score plays.

### The timeline and the piano roll

The **Timeline** tab (`Ctrl+Alt+M`) is the DAW side of the same file: a
track per line along a time ruler, automation lanes under the tracks, and a
piano roll of one line underneath. Nothing in it is a copy — a note drawn in
the roll is written into the score, spelled and in values the score view
shows at once, and an edit on the staff is on the roll the next frame. The
time axis is the written score (bars as printed, a repeat once). Ctrl+wheel
zooms it, Shift+wheel or the bar along the bottom scrolls it, the wheel over
the tracks scrolls them and over the roll moves up and down the keys
(Ctrl+wheel over the keys makes them taller).

| On a track | |
| --- | --- |
| **M**, **S** | mute, solo (the mixer's) |
| **R** | arm the line for recording; with nothing selected the piano roll shows an armed line |
| **A** | show or hide its automation lanes (the count is how many it has) |
| **+** | add a lane: volume, pan, cutoff, send A or B, or any setting of one of its insert effects |

Under the lines are **Bus A**, **Bus B** and the **Master**: no region,
their chain written across, and the same **A** and **+**. A bus takes
lanes for its return, its pan and any setting of its effects; the master
for its volume (a fade of the whole mix) and any setting of its chain.
Their lanes are edited, drawn and heard exactly as a line's.
| The region | its notes as a strip; a click selects that bar of the line (Shift extends, Ctrl every line) and shows the line in the roll; a double-click goes to the bar in the Score tab |
| The ruler | as the score's: a click moves the play head, a drag loops between two beats |

In an automation lane a click adds a point (on the grid; Shift: off it) and
a drag moves it — in time between its neighbours, in value freely — heard
as it moves; each drag is one undo step. Right-click a point for the shape
of the segment after it (straight, a step, or a curve with its bend) or to
delete it; `Delete` deletes the selected point. With the **Draw** tool a
drag draws the lane freehand: the stroke replaces the points it crosses,
thinned, and the lane runs on from its end to the next point. A switch's
lane (a filter's mode) only steps. The lane shows its value at the play head.

The piano roll is the selected line (its staves and voices together, each
voice in its colour, a selected note outlined, the score view's selected
note too). **Grid** is what notes snap to — 1/4 … 1/32, or a triplet of 1/4,
1/8 or 1/16, or Off; Alt while dragging leaves it.

| Select tool | |
| --- | --- |
| Click a note | select it (Shift or Ctrl adds); it is selected on the staff too, and heard |
| Drag a note | move it — and every selected note — on the grid in time, by semitones in pitch |
| Drag a note's right end | stretch it (and the other selected notes by as much) |
| Drag empty roll | select the notes in a box |
| Double-click empty roll | draw a note one grid step long |
| Right-click a note | delete it |
| `Delete` | delete the selected notes |

The **Draw** tool (`Ctrl+Alt+D`) draws a note as long as the drag instead.
A drawn note goes on its pitch's staff (a grand staff splits at middle C)
in the current voice (**Voice** 1–4 beside the grid); one that would cut a
note already in that voice goes into the next voice free there, so drawing
loses nothing, and one that starts and stops with a note in the voice joins
it as a chord. It is spelled in the key (as a transposing staff is written,
then stored at sounding pitch), and written as notation: values the meter
allows, tied across beats and barlines, a triplet where the grid put it.
The bars a roll edit touches are written afresh this way; what was written
in them is kept wherever it still applies — each note's spelling and
accidental display, its event's marks and grace notes, the marks on a rest.
A note moved on its own takes its articulation and slur marks with it; a
dynamic stays where it was written (on the rest left behind).

The **velocity** lane under the roll has a stalk a note: grey at the level
the dynamics give it, in the voice's colour where the note sets its own.
Drag a stalk to set the velocity (of every selected note, when it is one of
them); double-click it to give the note back to the dynamics. It is written
as a `vel=` mark on the note's event, which plays at that MIDI velocity
whatever the marks say and prints nothing.

### Exporting

**File > Export** (`Ctrl+Shift+X`) writes the score in any of its formats
at once, into `exports/<name>/` beside `resources/` (or a folder you type):
tick the formats on the left, and a block of options shows on the right for
each kind ticked. The footer counts the files it will write.

| Format | What it writes |
| --- | --- |
| **PDF** (score + parts) | `<name>.pdf` and `<name>_<part>.pdf`: the full score and the parts ticked, the layout's paper or Letter, A4 or Tabloid for this export, a staff size, the score transposed or at concert pitch (parts are always transposed) |
| **MusicXML** (for Sibelius) | `<name>.musicxml` (or compressed `.mxl`): MusicXML 4.0, which Sibelius, MuseScore, Dorico and Finale open; with the page layout and the breaks, or without them so the program lays it out itself |
| **MIDI** | `<name>.mid`, format 1, repeats played out or the score as written |
| **OGG** (game audio), **WAV**, **MP3**, **FLAC** | the audio: see below |
| **Tracker song (.ftrk)** | `<name>.ftrk`: the score quantised onto the Tracker's grid, for the Tracker editor |

**Audio** renders through the same scheduler the game uses, with the
mixer's mutes and solos, at the written tempo or the slider's:

- **Range**: the whole score, the loop region, or the selected bars (named
  in the file: `<name>_loop.ogg`, `<name>_bars5-8.wav`).
- **Files**: one mix, or the mix and a **stem** per line —
  `<name>_<line>.ogg`, the line alone through its own effects, the buses and
  the master (the lines the mix plays; a muted line has none).
- **Format**: 44.1 or 48 kHz; WAV and FLAC at 16 or 24 bits; OGG Vorbis at a
  quality from -1 to 10 (6 is about 192 kb/s); MP3 VBR at V0 (best) to V9,
  or a constant 128 to 320 kb/s.
- **Tail**: bars rendered after the end for releases and reverb, at the
  end's tempo. **Fold into the start** makes a file that loops seamlessly,
  for game music: the tail is mixed back over the beginning and the file is
  cut exactly at the end, so played on a loop the last bars ring over the
  first. (OGG loops sample-exactly; MP3 adds the coder's own silence at both
  ends, so loop with OGG.)

A score that loops (`loop: true`) exports one pass: the music stops at the
end and the tail is its ring-out, not the top again. WAV and FLAC are
written by the editor itself (FLAC losslessly compressed, about half a
WAV's size). OGG and MP3 use the reference encoders, libvorbis and LAME,
which the editor loads when it needs them: the Windows editor carries them
in its `codecs/` folder, and on Linux they are the system's (the
`libvorbisenc2` and `libmp3lame0` packages, on almost every desktop
already). Without one, the dialog says which is missing and how to get it,
and the other formats still export. `foundry score codecs` says where each
was found. (docs/score/CODECS.md has the details and the licences.)

**Export PDF** (`Ctrl+Shift+E`) and **Export Audio** open the same dialog
with only that format ticked. Exports run in the background; the status
line follows them, and names any format that could not be written.

### Importing

**File > Import** (`Ctrl+Shift+I`) opens a MusicXML file (what Sibelius
writes with **File > Export > MusicXML**), a MIDI file or a Tracker song as
a new score. Pick the file in the browser; its tracks come up in a table
with what the importer would do with each: **Import** (or, under **Line**,
**Skip**), the line's name, its **Instrument** from the library (the guess
from its name and program; type to filter) and, for MIDI and songs, its
**Clef** (a piano is a grand staff split at middle C). Below the table:

- For **MIDI and Tracker songs**, how the notes are read: **Quantise to**
  8ths, 16ths, 32nds or 64ths, **allow triplets**; **one voice per staff**
  or **split overlapping notes into voices**; the **key** and **time
  signature** from the file (or guessed, which it says) or chosen; the
  tempo it found; and whether **velocity becomes dynamic marks**.
- For **MusicXML**, whether the file's **page layout** comes too (its paper,
  staff size and breaks; Sibelius writes a break for every system it laid
  out, so leave it off to lay the score out afresh).

**Import** makes the score and opens it, not yet saved: **Save** writes it
under the file's name (or asks for one, if a score already has it). The
master gain is lowered if the mix would clip.

### Pages, parts and printing

The Score tab shows the music one of two ways: as one continuous system
(the default, for writing) or on pages, as it prints. **Pages** on the
transport row, **View > Page View** or `Ctrl+Alt+V` switches. The page view
is the same editor — clicks select and write, note input's ghost follows
the pointer, the selection, the caret and the play head are drawn, follow
playback turns the pages — with the pages one under another on a grey desk;
`Ctrl`+wheel zooms. The strip over the pages picks **Full score** or one
line's **Part**, says how many pages there are, and has **Page Setup** and
**Export PDF**.

How the pages are made:

- **Systems.** The bars are broken into systems the way a paragraph is set:
  all the breaks are chosen together so each system's natural spacing is as
  close as it can be to the width of the page, a little stretch preferred to
  any squeeze, no system squeezed tighter than its notes allow. Each system
  is then stretched to the margins (justified); the last one stays at its
  natural width when it is less than three quarters full. Every system
  starts with its clef and key, and its first bar's number; the time
  signature only where it changes.
- **Breaks.** Select a bar and press `Alt+Return` (or `Return` with bars
  selected, as in Sibelius; `Return` in MuseScore's keymap) for a new system
  after it, `Ctrl+Return` for a new page; again takes it away, and one kind
  where the other is changes it (**Layout > System Break / Page Break**).
  While a part is shown, breaks are the part's own.
- **Pages.** Systems go down the page at least the system gap apart (more
  where dynamics, slurs or ledger lines need it); one that does not fit
  starts the next page, as does a page break. A page more than two thirds
  full spreads what is left between its systems; the last page does not.
- **Titles and numbers.** The first page has the title, the subtitle and the
  composer (a part's name at the top left); **Title page** puts them on a
  page of their own. Pages after the first are numbered at the outer top
  corner (odd pages right, even left) beside a running header: the title,
  and the part's name.
- **Names.** Full instrument names on the first system, short names after
  (or full, or short, everywhere, or none — **Instrument names** in the
  setup), each centred on its line's staves; the first system is indented
  by them. Parts print no names on their systems.
- **Cautionary signatures.** When the next system starts with a key, time or
  clef change, this one ends with it: the clef just before the last barline,
  a double barline before a key change, then the new key and time after it.
  **Cautionary** in the setup turns them off.
- **Courtesy accidentals** (off by default): a note at a position the bar
  before altered shows its accidental again, in parentheses, the first time
  in the bar.
- **Brackets.** Two lines or more of one family in a row — woodwinds,
  brass, percussion, voices, strings, by their instruments' categories — are
  joined by a bracket down the system's left edge, and their barlines run
  through the gaps between them; a grand staff keeps its brace. The same in
  the continuous view. **Family brackets** in the setup (`brackets: false`)
  leaves them out.
- **Even staff spacing.** The staves of every system on a page stand the
  same distance apart — as far as the system that needs most room (a high
  passage with ledger lines, a lyric line) needs — so a line keeps its place
  down the page; a page that could no longer hold its systems so keeps its
  own spacing.
- **Hidden empty staves** (off by default; **Hide empty staves** in the
  setup, `hide_empty: true`): after the first system, a line with no notes
  (or chord symbols) in a system's bars is left out of that system, as an
  orchestral score sets resting instruments.
- **Parts** are a line each, laid out with the score's settings and the
  part's own over them (staff size, system gap, paper, breaks). At written
  pitch always. Runs of two or more bars in which the part only rests become
  one **multi-bar rest** — an H-bar with the count over it — broken wherever
  the player has something to read between the bars: a rehearsal mark, a
  tempo mark, a key, time or clef change, a repeat or an ending, a jump, a
  break.

**Layout > Page Setup** (`Ctrl+D`) has all of it: the title, subtitle and
composer; title page and page numbers; the paper (Letter, A4, Tabloid, A3,
Legal or a custom size), landscape, the four margins; the staff size (the
staff's height in mm, 7 by default), the system gap, the note spacing
(lower fits more bars on a system), the instrument names,
cautionary signatures and courtesy accidentals; each line's short name; and
while a part is shown, the part's staff size, system gap, paper and
multi-bar rests. A system taller than the page (many staves at a large
staff size) is not cut off: the pages are set at a smaller staff until it
fits, and the strip over the pages says so. Every change is one undo step (one per drag of a slider) and is
saved in the file's `layout:` block; none of it changes the sound.

**File > Export PDF** (`Ctrl+Shift+E`) is the Export dialog with PDF ticked
(see *Exporting*): the full score and any of the parts (all of them by
default), `<name>.pdf` and `<name>_<part>.pdf` in `exports/<name>/`. The
PDFs are drawn with the music font's and the text font's outlines — no font
is embedded, so they look the same everywhere — and carry the title,
composer and part in their properties.

**File > Export Selection as Image** writes the selected bars (of the
selected lines) as a PNG or as an SVG, at a chosen number of pixels per
staff space, as the continuous view engraves them.

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
| Notation | `ferm` (`ferm=short`, `long`, `verylong`), `arp` (`arp=down`), `up`, `down`, `on_beat` / `before_beat` (a grace group's timing), `cross=up` / `cross=down` (the notes written on the line's staff above or below their own) |
| Playback | `vel=96`: the event's notes play at this MIDI velocity (1–127) whatever the dynamics say — the piano roll's velocity lane; nothing prints |
| Lyrics | `ly=Hal-` (verse 1), `ly2=` … `ly8=`: the syllable, `-` after it a hyphen to the next, `_` an extender |
| Tablature | `str=2` (a single note's string), `str=G3/3+D4/2` (each note of a chord by its pitch) |
| Open, stopped | `open`, `stopped`: the "o" and "+" over a note (a horn's; a hi-hat's, which a drum map can play as another sound) |
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
- **Lanes** move what they name sample by sample: a straight lane is a
  smooth ramp, a step lands on its frame (and fades over at most 64
  samples, so it does not click), a curve bends. A volume lane multiplies
  with the dynamics. A bus's and the master's lanes play the same way: a
  bus's return and pan, the master's volume and every effect setting
  ramp sample by sample.
- **Swing** (`swing: {amount, unit}`, unit 8 or 16) lengthens the first
  of each pair of eighths (or sixteenths) to `amount` of the pair — 0.5 is
  straight, 0.67 a triplet feel, 0.75 dotted — and shortens the second;
  everything inside each half moves in proportion. Pairs count from the
  bar's start (a pickup counts from where its full bar would begin); a
  pair the bar cuts short stays straight. A line's own `swing` replaces the
  score's.
- **Humanize** (`humanize: {timing, velocity, seed}`) moves each note's
  start by up to `timing` milliseconds either way and its velocity by up to
  `velocity` of itself. The nudges come from the seed and where each note
  is written, so the score plays the same way every time, a chord's notes
  move together, and an edit leaves the rest of the score's nudges as they
  were. A line's own `humanize` uses the score's seed unless it gives one.

A MIDI export writes a slide as pitch bend when nothing else on its line
sounds while it bends (the channel's bend range set by RPN 0 to the widest
slide), and as the chromatic run it passes otherwise; the pedal goes out
as CC 64 with the notes at their written lengths.

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
  tuning: guitar         # a fretted instrument's strings, offered for its tablature
  staff_lines: 1         # a percussion staff of one line (a single drum)
  drums:                 # a kit's drum map: how a percussion staff writes each sound
    - {key: 42, name: Closed Hi-Hat, line: 9, head: x, shortcut: G}
    - {key: 46, name: Open Hi-Hat, line: 9, head: x, articulation: open}
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

### The instrument library dialog

**Instrument Library…** on a line's menu (or **Library…** in the quick
picker) opens the library with more room: the categories (the library's
folders, with their counts), the instruments in one (a search box filters
them; each says whether it is a `.finst` made here or an imported SFZ or
SF2, and how many articulations it has), and the chosen one's details — its
file, playable range, velocity layers (named by the dynamic each sits at),
round robin, clef, glissando style, samples, the articulations the notation
can pick, and a keyboard with its range lit. **Preview** plays it by itself
before it is chosen; **Use on** puts it on the line (heard at once, while
the score plays too); a double-click does both.

**Notation and playback**, under the details, edits what the notation reads
from the instrument and saves it back to its `.finst`: the playable range
(note names, sounding), the clef a new line takes, the transposition, how
its glissandi play, its staccato length and release, a fretted
instrument's tuning, a percussion instrument's staff lines and its **drum
map** — each sound's key and name, and under them its place on the staff,
notehead, articulation and note-input letter, with **Add a Sound** and
**x** to take one out — and the words that pick each articulation. **Save
to the Library** checks the file as it would read (a drum map with two
sounds on one key is refused, saying why) and writes it; every score and
song using the instrument reads the new settings, and its staves redraw
with the new map. **Open in Instrument Editor** edits the sound itself —
samples, zones, velocity layers, round robin, envelopes, filter — in the
Tracker's instrument editor: the Tracker opens on a scratch song holding a
copy of the instrument, with its library name ready in **Save to
library**, which writes it back (a Tracker song with unsaved edits is left
alone and you are asked to save it first).

### Importing SFZ and SF2

**Import SFZ / SF2…** in the dialog browses for a file (folders and `.sfz` /
`.sf2` files; a SoundFont lists its presets to tick) and imports it in the
background; the new instruments land in the **Imported** category, selected.
`foundry tracker import-instrument` does the same from the command line. An
`.sfz` becomes `sounds/instruments/imported/<name>.finst`; a SoundFont one
instrument a preset under `imported/<bank>/` (a bank of one preset is
`imported/<bank>.finst`); the samples go under `sounds/samples/imported/`.

What carries over:

| SFZ | In the `.finst` |
| --- | --- |
| `<control>`, `<global>`, `<master>`, `<group>`, `<region>` | headers fold into each region, the lower overriding the higher; `default_path`, `octave_offset`, `note_offset`, `#define`, `#include` |
| `lokey`, `hikey`, `key` (numbers or names, `c4` = 60), `lovel`, `hivel` | zones and velocity layers: the keyboard is cut wherever a region starts or stops, so overlapping regions map exactly |
| `pitch_keycenter` (or `=sample`: the WAV's own root), `transpose`, `tune` | each sample's base note and fine tune |
| `volume`, `amplitude`, `pan` | the sample's level; a pan every region shares |
| `offset`, `end`, `loop_mode`, `loop_start`, `loop_end` (else the WAV's smpl loop) | the sample cut and looped as the region plays it (each distinct way of playing a file is its own sample) |
| `ampeg_delay/attack/hold/decay/sustain/release` | the volume envelope (with its sustain point) and the release |
| `seq_length` / `seq_position`, `lorand` / `hirand` | round robin, in turn order |
| `sw_last` (`sw_down`), `sw_label`, `sw_default`, `sw_lokey`/`sw_hikey` | articulations, named from the label as the starter library names them (*Pizz* → pizzicato, *Spiccato* → staccato, *Trem* → tremolo, *Con sord.* → muted, *Legato*, *Sustain/Arco*), the label kept as a trigger word, so marks and technique text pick them; the keyswitch range is not part of the playable range |
| `cutoff`, `fil_type`, `resonance` | the instrument's filter, when every region agrees |

| SF2 | In the `.finst` |
| --- | --- |
| presets, instruments, zones, global zones | one instrument a preset; a preset zone's generators add to its instrument's, key and velocity ranges intersect |
| key range, velocity range, root key, coarse and fine tune, the sample's pitch correction | zones, layers, base notes, tuning |
| sample data (16-bit, with `sm24` 24-bit), loop points and sample modes, the address offsets | WAV samples cut and looped |
| linked left and right samples | one stereo sample |
| volume envelope, attenuation, pan, initial filter | envelope and release, level, pan, filter |

What cannot (said in the import's notes, not dropped silently): release
triggers, modulators and the modulation envelope and LFOs, `<curve>` and
`<effect>` headers, stacked regions over the same keys and velocities with
no round robin (a layer plays one sample a note), one-shot loops (a
note-off releases them unless every region is one-shot, when the release is
the longest sample). FLAC samples are read (and kept as WAV in the
library), as are WAV, OGG and MP3.

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
foundry score import <file> [-o name]      a MIDI, MusicXML or Tracker (.ftrk) file as a score (-list shows the parts first)
foundry score export-midi <score> [-o f]   the score as a Standard MIDI File
foundry score export-pdf <score> [flags]   the full score and its parts as PDFs
foundry score export-musicxml <score>      the score as MusicXML 4.0 (-o f.musicxml or f.mxl, -no-layout)
foundry score export-audio <score> [flags] WAV, FLAC, OGG or MP3, a mix or a mix and stems
foundry score export-ftrk <score> [-o f]   the score as a Tracker song (-rows 4: the grid)
foundry score export <score> [flags]       every format at once, as the Export dialog writes them
foundry score codecs                       where the OGG and MP3 encoders were found, or why not
foundry score midi [-monitor 10] [-port n] the MIDI input ports the editor can use; -monitor prints what one sends
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
and a hash of its pixels. `-svg out.svg` writes the same as a vector image
(the glyphs and text as outlines). `-page 2` draws a page of the page
layout instead (with the score's `layout:`), and `-part Violin` (a name or
a number) a page of that line's part.

`export-audio` takes `-format wav|flac|ogg|mp3` (or the extension of
`-o file`), `-bars 5-8` or `-section name` (the first time they play),
`-stems` (a file per line beside the mix), `-rate 44100|48000`,
`-bits 16|24` (WAV, FLAC), `-quality 6` (OGG), `-mp3-quality 2` or
`-bitrate 192` (MP3), `-tail 2` (bars), `-fold` (the tail folded into the
start: a seamless loop), `-tempo 0.75`, and `-verify`, which decodes every
file written with a decoder that is not the encoder (and checks a FLAC's
MD5). `export` takes `-formats pdf,musicxml,ogg` (or `all`), `-o dir`
(default `exports/<score>/`), the audio flags, and the PDF, MusicXML and
Tracker ones (`-parts=false`, `-concert`, `-paper`, `-staff`, `-mxl`,
`-no-layout`, `-no-repeats`, `-rows`).

`export-pdf` writes `<name>.pdf` and `<name>_<line>.pdf` for every line
into the working directory, or `-o dir` (or `-o file.pdf`: the score there,
the parts beside it). `-parts=false` or `-score=false` leave either out,
`-part 1,Viola` picks parts, `-concert` sets the full score at concert
pitch, `-paper a4` and `-staff 6.5` replace the layout's paper and staff
size for this export, and `-png dir` reads every PDF written back and draws
its pages as PNGs (`-dpi 100`) — a quick look without a PDF viewer.

The engraving follows the usual rules, so what a score says is what a
player expects to read. Accidentals last to the barline and are shared by a
staff's voices; a note tied over the barline does not repeat its
accidental; `!` on a note forces one and `?` puts it in parentheses. Beams
follow the meter: beats, half bars of eighths in 4/4, dotted beats in 6/8,
the groups of 3+2+2/8 (5/8 beams 3+2 and 7/8 2+2+3), and each tuplet on its
own; in a compound meter a dotted beat of 16ths keeps one primary beam
with its 16ths' beam broken at each eighth, so the beat's three eighths
read. With two or more voices on a staff, voices 1 and 3 have stems up and 2
and 4 down, whatever the pitch, and rests move out of the way; a stem that
points away from the staff from a note outside it is shortened to two and a
half spaces; the `up` and `down` marks force a stem. A sung staff (a line
with lyrics, or a choir's or a voice's) has its dynamics over it, out of
the words' way. An ottava line carried onto the next system starts there
with "(8va)". A clef change at a bar's start stands before its barline
with a clear half space either side. A tablature on its own carries the
articulations, slurs, ornaments and fermatas over its strings, tremolo
strokes on its rhythm stems, an arpeggio beside the fret numbers and
tuplets under the rhythm. A bar written as `R`, or as one whole rest, has
its rest in the middle; an empty bar shows a faint one. Copyright: the
Bravura font is © Steinberg Media Technologies, under the SIL Open Font
License (an editor install carries it in `licenses/`).

## Importing and exporting MIDI, MusicXML and Tracker songs

**From Sibelius, Finale, Dorico or MuseScore**, export MusicXML (in Sibelius:
**File > Export > MusicXML**) and import that. Sibelius's own `.sib` format
was never published, so it can't be read directly. **To Sibelius**, export
MusicXML (`foundry score export-musicxml`, or **MusicXML** in the Export
dialog) and open it there.

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
which are stored at concert pitch with the staff's transposition. Dynamics
(and `cresc.`/`dim.` written as words with dashes), articulations,
ornaments with the accidentals on their auxiliary notes, tremolos (one
note and two), fermatas by shape, arpeggios, glissandi (wavy or straight),
slurs, hairpins, ottava lines, pedal marks, technique and expression text,
rehearsal marks, tempo text (`rit.` and `accel.` with their dashes, `a
tempo`), repeats, 1st and 2nd endings, segno, coda, To Coda, Fine, D.C.
and D.S., clef changes anywhere in a staff, the part's short name, volume
and pan, and the page layout (paper, margins, staff size, system and page
breaks; `-no-layout` leaves it out) are kept too, and so are lyrics (every
verse, hyphens and extenders), chord symbols (from the program's own
spelling when it reads, else from the chord's kind and degrees), tablature
(a TAB staff with its tuning and capo, each note's string; a TAB staff that
repeats the notation over it, as MuseScore writes linked staves, becomes
that staff's linked tablature) and one-line percussion staves. Text keeps
its ♭ ♯ ♮; a part's name spells them as letters ("Clarinet in Bb", as the
staff headers set names in the editor's own font), and a Sibelius
part-name display with its accidental is read whole. Anything the score
can't hold is reported as a warning, never dropped silently.

`export-musicxml` writes MusicXML 4.0 that Sibelius and MuseScore open as
the score shows: parts and staves (a piano's two braced), voices 1–4 and
5–8 as Sibelius numbers them, transposing parts at written pitch with their
transposition, the accidentals each bar needs, beams by the meter, nested
tuplet brackets, ties, and every mark above; tempo marks with their
metronome marks and the tempo they play; the layout as hints. The part's
library instrument goes in its `<virtual-instrument>`, so a score exported
and imported again plays what it played, and a note's `vel=` its velocity.
What MusicXML has no place for — the mixer and its effects, lanes, params,
bindings, markers, a section's loop and next — stays in the `.fscore`. A
score exported and imported again is the same in everything MusicXML holds
(the round-trip tests check every fixture and shipped score). MuseScore
draws a nested tuplet (a triplet inside a triplet) as one flat tuplet; that
is MuseScore's reading of the file, which is to the standard. Every note's
stem goes out as the page draws it (so another program sets a kit's two
voices as the score does), a family of lines as a bracketed part group with
joined barlines, and a cross-staff note on the staff it is written on, in
its own staff's voice — which is also how other programs' cross-staff notes
come in.

**Tracker songs** come in through the MIDI importer, with the same options:
the song is played through once (pattern breaks taken, a jump back read as
the end) into notes, each instrument a line playing the `.finst` its slot
names (or the guess from its name). The sections keep their loops and next
sections; params, bindings, the reverb, delay and compressor come along as
the score's params, bindings, buses and master. `export-ftrk` goes the
other way: the score is played (repeats out) and quantised to the Tracker's
grid — the fewest rows a beat that hold its rhythms exactly (16ths are 4,
16th triplets 6, both 12), or `-rows n`, saying how many notes moved —
with a channel for each note a line sounds at once, each line's `.finst` as
the song's instrument, velocities in the volume column, articulations as
`@` commands, tempo changes as `M`, markers as `E`, sections on whole
order positions and identical patterns shared.

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
- **Instruments.** A track's instrument comes from its name and program; a
  file Foundry exported names each line's library instrument, and comes
  back with it.
- **Piano.** A piano part becomes a grand staff split at middle C (`-split`).
- **Dynamics.** Velocities are read as dynamics (`-no-dynamics` keeps them
  out).
- **Controllers.** CC7 and CC10 become the line's volume and pan, and CC11
  becomes a volume lane.
- **Meter and key.** These come from the file's time and key signatures.
  When the file has none they are guessed — the meter from where the strong
  notes fall (4/4 unless 3/4, 5/4, 7/8 or 9/8 is clearly better, then 3/4 or
  6/8 and 4/4 or 12/8 by how the beats divide), the keys from the pitches
  bar by bar, so a piece that modulates gets its key changes — or you can
  set them with `-meter 6/8` and `-key Eb` (or `F#m`, `"C minor"`, `-3`).
- **Pitch bend and the pedal.** A note the pitch wheel slides to another
  pitch and holds there is written as the two notes with a glissando
  between (how a slide is exported); the sustain pedal (CC 64) becomes
  pedal marks on the lowest staff.
- **Drums.** Channel 10's General MIDI keys are written as the line's drum
  map's own sounds — by name, else by place and notehead — so a kit with
  keys of its own reads a General MIDI file right.
- **Tempo and markers.** Tempo changes become tempo marks, and a run of small
  steps in one direction becomes a ramp. Markers on a bar line become
  rehearsal marks.

`export-midi` writes format 1 at 960 ticks per quarter, which holds every
note exactly:

- **Conductor track:** tempo (ramps as steps), time and key signatures,
  sections as markers.
- **One track per line:** its General MIDI program, volume and pan, the
  volume lane as CC11, and notes with velocities from the dynamics; a
  slide as pitch bend (see *How a score plays*), the pedal as CC 64, and a
  kit's sounds as the General MIDI keys they are.

Repeats are played out unless you pass `-no-repeats`. Importing an export
gives back the same notes.
