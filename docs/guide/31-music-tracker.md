# Music tracker

Foundry has a tracker built in: songs are note data plus samples, played live
by the engine. A song is a few kilobytes instead of megabytes of audio, and
because the engine plays it note by note, a script can change it while it
plays. It can move to the combat section on the next bar, bring the drums in
as a fight heats up, or muffle everything while the player is underwater.

Songs live beside every other sound, as `resources/sounds/<name>.ftrk`. A level
plays one the way it plays any track:

```yaml
music:
  track: combat_theme      # sounds/combat_theme.ftrk
  section: intro           # optional: where to start
  params: {intensity: 0}   # optional: song params at the start
```

`play_music("combat_theme")` works too. A recording and a song with the same
name can't both exist: the `.ftrk` wins.

## The Tracker editor

Open it from the editor switcher (**Tracker**). **File > New** offers starter
songs (Empty, 4-on-the-floor, Chiptune, Ambient, Combat loop); **Open**,
**Save**, **Save As**, **Export WAV** (to `exports/<name>.wav`) and **Reload
samples** are beside it. Unsaved changes show on the save chip, and **Save
All** saves the song with everything else.

| Pane | |
| --- | --- |
| **Toolbar** | Play song (`F5`), play pattern (`F6`), play from cursor (`F7`), stop (`F8`); BPM, rows per beat, octave, edit step, follow playback, edit mode, live voices and a scope |
| **Order** (left) | The order list, coloured by section, with brackets beside it. Insert, repeat, duplicate, make unique, move, delete. Below it, the selected section's name, range, loop and next. |
| **Pattern** (centre) | The grid. Beats and bars are shaded and the playing row follows. Channel headers rename on double-click and have mute, solo, a piano-roll toggle, a meter, and a mixer popup (volume, pan, sends, drive, crush). A strip above shows how busy each pattern is; click it to jump. |
| **Instruments** (right) | The list (click a name to hear C-4) and the selected instrument: samples, keymap zones on a piano strip, levels, four envelopes, filter, auto-vibrato, polyphony, interpolation. |
| **Bottom tabs** | **Sample**: waveform, loop points that snap to zero crossings (hold `Alt` to place them exactly), normalize, fade, reverse, trim, base note with pitch detect, and **Generate…** from the built-in recipes. **Song**: tempo, ticks, beats per bar, effect columns, gain, loop, beat events, interpolation, channel count, reverb, delay, compressor. **Params & markers**: live sliders, bindings, marker names. |

Effects read as words ("Vibrato 4/4"). The **hex** toggle shows the raw
`V44`, and hovering any effect explains it. In an effect column, `Enter` (or
`Ctrl+Space`) opens a searchable list of every command.

### Entering notes

Click the grid to give it the keyboard. The keys are laid out like a piano:
`Z S X D C V G B H N J M` is the lower octave and `Q 2 W 3 E R 5 T 6 Y 7 U I`
the upper; `[` and `]` move the octave. `1` is note off and `` ` `` is note
cut. Hex digits fill the instrument, volume and effect columns. After each
entry the cursor moves on by the edit step (`Ctrl+0`–`9`).

`Space` toggles edit mode. With it off, the keys only play notes, for trying
ideas. A held key holds its note until you let go.

Select with `Shift`+arrows or a drag. `Ctrl+C`/`X`/`V` copy, cut and paste as
tracker text, so a block pasted into a text editor (or the Claude panel) reads
like the file. `Ctrl+Shift+V` pastes only into empty fields.
`Ctrl+Up`/`Down` transposes (with `Shift`, by an octave), and `Ctrl+I`
interpolates volumes and effect values across the selection. Right-click for
humanise, fill every N rows, chord stamp and scale lock (typed notes snap to a
key).

`F1` lists every key.

## How a song is built

| Piece | What it is |
| --- | --- |
| **Pattern** | A grid of rows (1–256) by channels (1–64). Time runs down. |
| **Cell** | One channel on one row: a note, an instrument, a volume, and 1–4 effect commands. |
| **Order** | The list of patterns to play, in order; a pattern can appear many times. |
| **Section** | A named run of the order list (`intro`, `combat_loop`, `outro`). It either loops until a script asks for another, or goes on to its `next`. |
| **Instrument** | One or more samples, plus a keymap, envelopes, a filter, vibrato and a release. |
| **Sample** | A sound from the library, `sounds/samples/<category>/<name>.wav`, with a `.fsmp` sidecar holding its base note, loop and tags. |
| **Param** | A named value from 0 to 1 that scripts ramp, such as `intensity`. **Bindings** connect a param to a channel's volume, pan, sends or low-pass cutoff. |
| **Marker** | A name for an `E` command's number. Scripts hear `:song_marker:` when it plays. |

Tempo is **BPM** and **LPB** (rows per beat, normally 4). **Ticks per row**
(normally 6) is how often slides and vibrato update within a row.
**Beats per bar** sets the bar that quantised changes and `:song_bar:` wait for.

### Cells as text

A `.ftrk` file is YAML, and each pattern is stored as tracker text, one line
per row with channels separated by `|`:

```
C-4 01 40 V44 ... | ... .. .. ... ...
=== .. .. ... ... | E-4 02 .. P40 ...
```

| Field | Spelling |
| --- | --- |
| Note | `C-4`, `C#4`; `===` note off (release); `^^^` note cut; `...` empty |
| Instrument | two hex digits, `01`–`FF`; `..` keeps the channel's last instrument |
| Volume | `00`–`40` hex; `..` means full volume for a new note, unchanged otherwise |
| Effect | a letter plus two hex digits, `V44`; `...` empty |

Trailing empty rows are left out. The format is meant to be read and edited
by hand, and by the Claude panel.

### Effect commands

`xx` is one hex byte; `xy` is two nibbles. A zero parameter on a slide,
vibrato, tremolo or volume slide repeats the channel's last value.

| | Name | Does |
| --- | --- | --- |
| `Uxx` / `Dxx` | Pitch slide | up / down by xx/16 of a semitone every tick |
| `Gxx` | Glide | from the playing note to this row's note at xx/16 semitone per tick, without restarting it |
| `Vxy` | Vibrato | speed x, depth y/8 semitone |
| `Axy` | Arpeggio | note, +x, +y semitones, one per tick |
| `Jxx` | Fine tune | 80 is none; ±1 cent per step |
| `Sxy` | Volume slide | up x or down y per tick |
| `Txy` | Tremolo | speed x, depth y |
| `Cxx` | Note cut | silence after xx ticks |
| `Nxx` | Note delay | start the note xx ticks late |
| `Qxx` | Retrigger | restart the note every xx ticks |
| `Pxx` | Pan | 00 left, 80 centre, FF right |
| `Yxy` | Pan slide | right x or left y per tick |
| `Xxx` / `Zxx` | Sends | reverb / delay send level |
| `Oxx` | Sample offset | start xx/256 of the way in |
| `Ixx` | Reverse | 01 backwards, 00 forwards |
| `Fxx` / `Rxx` | Filter | channel low-pass cutoff (00 dark, FF open) / resonance |
| `Wxy` | Cutoff slide | open x or close y per tick |
| `Bxx` | Jump | continue from order position xx |
| `Kxx` | Break | next pattern, at row xx |
| `Lxx` | Loop rows | `L00` marks the start; `L0x` repeats back x times |
| `Mxx` | Tempo | set BPM to xx (20–FF) |
| `Hxy` | Set param | param number x to y/15 |
| `Exx` | Marker | fire marker xx to scripts |

### The mix

Each channel has a volume, pan, mute, two inserts (**drive** saturation and
**crush** to a bit depth), and two **sends**. Send A feeds a
reverb (size, damping, width). Send B feeds a tempo-synced stereo delay
(time in beats, feedback, tone, ping-pong). An optional compressor sits on the
master. The whole song plays as one voice on the engine's **music** bus, so
the player's music slider, fades and the limiter apply to it as to any track.

## Scripting a song

Every change waits for a boundary, so a switch lands on the beat. The quantise
word is `"beat"` (the default), `"bar"`, `"pattern"`, `"section"` (when the
playing section ends) or `"now"`.

| Verb | |
| --- | --- |
| `play_song(name, section, fade)` | play a song as the level's track, from a section. If it's already playing, change section on the next beat instead. |
| `song_section(name, quantise)` | move to a section at the next boundary |
| `song_param(name, value, seconds)` | ramp a param (0–1) |
| `song_mute(channel, on, fade)` | fade a channel out (`true`) or back in; by name or number from 1 |
| `song_tempo(bpm, seconds)` | ramp the tempo |
| `song_stinger(name, quantise)` | play a short song once over this one |
| `play_note(instrument, note, volume, seconds)` | play one note on the song's instrument, outside its patterns, for a musical pickup or UI sound |
| `song_position()` | `{playing, song, section, order, row, beat}` |

The song posts events to the level's scripts and the player's:

| Event | Args | When |
| --- | --- | --- |
| `:song_marker:` | `name str, value int` | an `E` command played |
| `:song_section:` | `name str` | a section started |
| `:song_beat:` | `beat int, section str` | each beat, if the song has `beat_events` on |
| `:song_bar:` | `section str` | each bar's first beat, after its `:song_beat:` |
| `:song_end:` | `song str` | a song that doesn't loop ran out |

```
-- in the level's script; a guard sends (:fight_started:,) to the level
def on_fight(:fight_started:) -> null
    song_section("combat", "bar")
    song_param("intensity", 1, 4)

def on_marker(:song_marker:, name str, value int) -> null
    if name == "drop"
        @self.state.dropped = true
    end
```

The level's hard pause (`@level.prop.paused`) freezes the song where it is,
silent, and it picks up mid-phrase when the game resumes. A save game records
the song, its section and position, and the params and mutes scripts set, so a
loaded checkpoint resumes the music the player was hearing.

## The sample library

`sounds/samples/` holds 128 built-in samples across ten categories: drums,
bass, leads, pads, keys, plucked, winds, chip, vocal and fx. Every one is
generated by a recipe in `internal/tracker/synth`, so the library rebuilds
exactly with:

```
foundry tracker gen-samples              # all of them
foundry tracker gen-samples -only kick_808,pad_warm
```

Add your own by dropping a `.wav`, `.ogg` or `.mp3` anywhere under
`sounds/samples/`. A `.fsmp` sidecar beside it sets how it plays:

```yaml
base_note: C-4      # the note at which it plays at its own pitch
fine_tune: 0        # cents
loop: forward       # none, forward or pingpong
loop_start: 1200    # frames
loop_end: 48000
volume: 1
tags: [bass, analog]
```

A release ships only the songs a level reaches and only the samples those
songs name.

## From the command line

```
foundry tracker list                     songs and their length
foundry tracker inspect combat_theme     channels, sections, instruments, missing samples
foundry tracker check                    every song: does it parse, do its samples load
foundry tracker render combat_theme -section combat -param intensity=1 -o out.wav
foundry tracker samples                  the library, as songs name it
foundry tracker new boss_theme           an empty song
```

`render` is the same player the game runs, so the WAV is exactly what a
player would hear. The same song and settings always render the same audio.
