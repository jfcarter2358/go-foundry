# Phase 2 — Many lines and playback

SPEC's line for this phase: multiple lines; staff headers (instrument
picker with search, hot swap while playing, mute, solo, duplicate, volume,
meter); grand staff with brace; mixer pane (volume, pan, mute, solo,
meters); transport (play/stop, start, loop, follow, position readout, tempo
slider); time ruler with bar numbers + time, click-to-seek, drag-to-loop;
click a note → playhead; follow playback; WAV export; `.fscore` in levels
via `world.Music` (`play_music`, `music:` block), sections from rehearsal
marks, `song_section`/`song_param`/mute verbs and the `:song_*:` events,
save games; the guide chapter. Every item shipped; the verification pass at
the end lists what it found and fixed.

Phase 2 is the first release to the musician. Step A is the editor side:
many lines with their headers, the mixer, the transport and the time
ruler, percussion staves, and WAV export. Step B is the game side: a
`.fscore` as a level's music through `world.Music`, steered by the song
verbs and answering with the song events, in save games, and in a release;
and the guide chapter.

## Step A: lines, the mixer, the transport, export

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/play` | `Player.SetMix(line, LineMix{Volume, Pan, SendA, SendB})`, `SetGain`, `SetReturns` — the mixer live, no new program; `LineSilenced` (a score's own mute/solo rule, shared with the editor); `RenderOptions.To` (render a region: the transport stops there, notes release, the tail follows) |
| `internal/tracker/engine.go` | `Engine.SetGain`, `Engine.SetReturns` (cheap setters the score player's commands use; the Tracker's sound is untouched) |
| `internal/score/drums.go` | `GMDrums` — where each General MIDI kit instrument (keys 35–59) sits on a percussion staff and with what notehead; `DrumForKey`, `DrumAt`, `DrumDisplay`, `DrumPitch`, `DrumName`, `PercussionBottom` |
| `internal/score/engrave` | Percussion staves: drums by place and notehead (x, circled x, diamond, triangle; per note, stems and widths from the shape every note shares), no key signature, no accidentals; `DrumHeadGlyph`; golden `percussion.png` |
| `internal/score/edit/lines.go` | `NewLineFor` (an instrument's staff: grand, percussion, transposed), `AddLine`, `DuplicateLine` (with notes / empty), `RemoveLine` (and its bindings), `MoveLine`, `RenameLine` (bindings follow), `UniqueLineName` |
| `editor_score_transport.go` | The transport with no ImGui: `scoreTransport` (play head, loop, tempo scale, follow), beats and snapping, written position ↔ tick ↔ clock, the loop region, play start rules, tempo marks from the slider; `scoreState` commands and the player ops queue |
| `editor_score_lines.go` | Lines and the mixer on `scoreState`: `mixChange` (one undo step per drag, `MixRev`), faders, pan, sends, returns, master, mute, solo, instrument swap, add / duplicate / remove / move / rename with the selection remapped |
| `editor_score_header.go` | The staff headers (name, rename, instrument chip, M, S, duplicate, volume slider, meter; compact when zoomed out), the instrument picker with search, the duplicate and line menus, small widgets |
| `editor_score_ruler.go` | The transport row (Score / Mixer tabs, Start, Play, Loop, Follow, readout, tempo slider, Write) and the time ruler (bars, times, beats, loop region, seek, drag-to-loop), the play head, follow |
| `editor_score_mixer.go` | The Mixer tab: line strips (sends and pan knobs, fader, meter, dB, M/S), bus returns, master with the mix's meter |
| `editor_score_export.go` | File > Export Audio (WAV): whole score, loop region or selection, tail, at the slider's tempo or not, off the frame |
| `editor_score_window.go` | The frame (`scoreDrawFrame`), the player sync (a new program when the music changed, `SetMix`/`SetGain`/`SetReturns`/`MuteIndex` when only the mixer did, `SetInstrument` for a swap), ops, menus (Lines, Play), commands |

### How it fits together

- **Two kinds of change.** Notes, lines and signatures go through `change`
  (`Rev`): the window hands the player a fresh clone (`SetScore`, which
  prepares). Mixer settings and an instrument swap go through `mixChange`
  (`MixRev`): the window sends only what moved, live, so a fader dragged
  while the music plays is heard at once and nothing is interpreted per
  frame. Both are undoable (one drag = one step); the save chip sees both.
  The player's `faders` are its own copy of each line's strip, reset from
  the score on every program, so the score and the player never disagree
  for long.
- **Hot swap** is `play.Player.SetInstrument` (phase 0): the program is
  copied with one instrument changed and the performance shared, so
  sounding notes finish on the old instrument and the next note is the new
  one's. `TestScoreHotSwapWhilePlaying` checks that the performance pointer
  is the same before and after.
- **Written positions vs ticks.** The ruler, notes and selections are
  written positions (bar + exact offset); the transport turns them into the
  tick a bar first plays at only when it talks to the player. The loop is
  kept as written positions, so edits under it keep it on its bars
  (`scoreFitLoop` trims it when bars go); its ticks are re-sent after every
  new program.
- **Ops.** What the transport asks of the player (seek, play, stop, loop,
  tempo, swap) is queued on `scoreState` and taken by the window once a
  frame, after bringing the player up to date — which is what lets the
  tests drive the transport with no device.

### Behaviour, and where it follows the mockup

- Space plays from the play head (Sibelius's playback line), the loop's
  start when the loop is on and the head outside it, the top from the end;
  stopping leaves the head where the music stopped (the transport noticing
  the end on its own does the same). P plays from the selection. A click on
  a note moves the play head to it (also while playing, as the mockup).
- The ruler: click seeks to the nearest beat (the meter's beats: 6/8 has
  two, 3+2+2/8 three; a pickup's are counted from its end); a drag of more
  than 4 px loops between the two beats (dragged either way; one beat when
  they meet) and turns the loop on; a loop set while playing elsewhere
  starts at the loop. L with bars selected loops them; with none, it (and
  the Loop button, Shift+L) switches the loop on and off.
- The tempo slider is 25–200 % in steps of 5, ramped over 0.15 s; the
  readout and the ruler's times are as heard at that speed. **Write**
  offers both readings of "write the slider as a tempo mark": every mark
  scaled (the piece plays as it just did, its tempo changes in proportion),
  or one mark at the selected bar / play head's bar with the tempo heard
  there (a mark's dotted unit is kept).
- Headers fold to the name and the buttons when a line's band is under
  70 px (zoomed out). A widget is only made when it is wholly below the
  ruler, so a half-hidden one never takes the ruler's clicks.
- Mute and solo are the file's `mute:`/`solo:` (the score plays the same
  in a level); the editor's effective mute is `play.LineSilenced`, the
  player's own rule.
- The Mixer tab replaces the score in the main area (the mockup's switcher
  tabs); M toggles it in the Sibelius keymap (Sibelius's mixer key), F10 in
  MuseScore's.

### Tests

| Test | Checks |
| --- | --- |
| `play`: `TestLiveMixAndGain`, `TestLiveReturns`, `TestRenderRegion`; `TestRandomCommandsKeepThePlayerSane` now sends mixes and gains | live mixer commands (no allocation), a later `SetScore` taking the score's values back, return levels, region renders (length, release at the end, at half speed, past the end) |
| `score`: `TestGMDrums` | the kit map: every key 35–59 placed, no two drums alike, stored as their keys |
| `engrave`: `TestPercussionStaff`, golden `percussion` | places and heads, no key or accidentals, unmapped keys at their letter |
| `edit`: `TestDuplicateLine`, `TestMoveRemoveRenameLines`, `TestNewLineFor` | duplication with notes / empty (deep copies, no solo, bindings kept), moves, removal with bindings, renames with bindings, unique names, staves for an instrument |
| `editor`: `TestScoreBeatsAndSnapping`, `TestScoreTicksAndClock`, `TestScoreLoopRegion`, `TestScoreTransportCommands`, `TestScoreWriteTempo` | the transport's arithmetic (beats, pickup, snapping, ticks through repeats and tempo changes, readout, clock, clamp) and commands (L, Space, Home, P, a clicked note, the slider, both tempo writes with undo) |
| `editor`: `TestScoreHotSwapWhilePlaying`, `TestScoreLiveMixer` | through the window's sync with a real scheduler rendering offline: the swap heard on the next note, notes unchanged, no new program; fader, mute, solo, master heard live, one undo step per drag, undo reaching the player |
| `editor`: `TestScoreLineCommands`, `TestScorePercussionEntry`, `TestScoreExportAudio` | line commands with selection remapping and undo; drums by place, refusals; export spans, seconds and the WAV's size |
| `editor`: `TestScoreHeadersRulerAndMixerThroughTheView` | through the real ImGui frame: M, S, name click and double-click, the picker, the duplicate menu, a ruler click and a ruler drag, the mixer's fader drag, every popup and the export dialog drawing |
| `editor`: `TestScoreGLWindowDraws` | now also shoots the mixer, a loop with a drum kit at 75 %, and the compact headers (`SCORE_SHOT=x.png` writes `x.png`, `x_mixer.png`, `x_loop.png`, `x_small.png`) |

### How to verify

```
go test ./internal/score/... ./internal/tracker/... ./internal/editor/ ./internal/app
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
foundry score render internal/score/engrave/testdata/percussion.fscore -png /tmp/perc.png -scale 24
```

The four GL shots were looked at: headers, brace, ruler times, loop shading,
the play head through every staff, the drum staff, the mixer's strips and
the compact headers.

### Decisions

- **Mixer settings are undoable score edits**, coalesced per drag; the
  Tracker's mute pushes undo too, and a snapshot undo would otherwise undo
  a fader along with the next note edit.
- **Mute and solo live in the file** (the model always had them), so the
  musician's balance is what the game plays.
- **The loop region, play head, tempo slider and follow are view state**,
  not saved; the score's own `loop:` (play again from the top) is
  unchanged.
- **Percussion uses the General MIDI kit map for every drum staff** until
  per-instrument drum maps (phase 6); keys outside 35–59 show at their own
  letter's place.
- **Fader range** 0 to 2 (+6 dB), unity in the middle; the master stops at
  0.01 because a file's `gain: 0` reads as unity.

### Deferred

- Phase 4: insert effects and bus effects other than reverb/delay are listed
  in the mixer as stored, not played (the effects rack); automation lanes;
  the Timeline tab.
- Phase 5: other export formats (OGG, MP3, FLAC, stems, …) and the export
  dialog of mockup screen 6; WAV is here.
- Phase 6: per-instrument drum maps (the percussion staff uses the GM map),
  the instrument library window of mockup screen 4 (the picker is the quick
  one).
- Reordering lines by dragging their headers: Move Up / Move Down in the
  header's menu do it; no phase names a drag.

## Step B: scores in the game

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/play/library.go` | `Play(name, PlayOptions{Section, Gain, FadeIn, Params, Resume, Tick})` — the score's `tracker.Play`: load, prepare, start, one voice on the music bus; `Prepared`, `Preload` (the score and its instruments into their caches, for a level load), `NewStinger` (a started copy that plays once: no loop, no section looping or moving on) |
| `internal/score/play/player.go` | `Player.Stinger(g audio.Generator, q)`: anything mixed over the score from the next boundary (beat, bar, section end, now), sample-accurate, at most four; a play-head jump (section, loop) is a boundary too; a finished score renders on until its stingers end; allocation-free |
| `internal/tracker/player.go` | `StingerGenerator(g, q)` (a score's player as a stinger over a song) and `NewStinger(s)`; stingers are `audio.Generator`s. `Stinger(s, q)` is unchanged in sound (golden test) |
| `internal/world/music.go` | `Music.Score` (the player), `ResumeTick`; `startScore` beside `startSong` (a song of the same name wins); `ScorePlayer`, `MusicSteerable`; `PlaySong`, `SetSongSection`, `SetSongParam`, `SetSongMute`, `MusicSection`, `MusicPause`, `StopMusic` take either |
| `internal/script/script_actions_song.go` | every song verb with a score: `play_song`, `song_section`, `song_param`, `song_mute` (lines by name or number), `song_tempo` (the tempo slider: the bar playing now at the bpm), `song_stinger` (a score or a song over either), `play_note` (a line; `"C-4"` or `"C#4"`), `song_position` (`bar`, `pass`, `beat`, `tick`, `seconds`); `postScoreEvents` |
| `internal/savefile`, `internal/savegame` | `MusicState.Tick`; a score saves its tick, section, playing, params and mutes, and resumes at the tick |
| `internal/levelio/level_loader.go` | a level whose music is a score preloads it (`play.Preload`) |
| `internal/editor/editor_claude_reference.go` | `list_assets` lists `scores` |
| `internal/flang/builtin_doc.go`, `internal/script/script_event_doc.go`, `internal/world/level_schema.go` (+ regenerated `editor_yaml_docs_gen.go`) | the verbs, events and `music:` keys documented for scores |
| `docs/guide/32-score-editor.md` | the level block, the `.finst` as a score uses it, *A score in a level*, *Scripting a score*; README entry; chapter 31 links to it |

### How it fits together

- **One voice, the same slots.** A score is the music bus's one generated
  voice, as a song is (`PriorityHigh`, exempt from the looping-voice cap by
  its bus — `audio.TestTheLoopingCapLeavesRoomForTheMusic` covers the
  request `play.Play` makes). `world.Music` holds `Song` or `Score`, never
  both; everything that read `Song` reads either.
- **Names.** Sections are the score's rehearsal marks (`sections:`), the
  channels its lines (`Score.LineIndex`: name without case, or number from
  1), params its own with their bindings (volume, pan, sends, cutoff — the
  player's phase 0 code). `"pattern"` quantises to the bar.
- **Events.** The scheduler posts a downbeat as `EventBar` then `EventBeat`;
  scripts hear `:song_beat:` then `:song_bar:`, as with a song. A score
  always posts beats (no `beat_events` switch); the beat counts from 0 in
  the bar, and the section a beat carries is the one its tick lies in
  (`Performance.SectionAt`). `:song_marker:`'s value is the marker's bar (a
  score marker has no number).
- **Stingers** start on their boundary's own frame, unlike a song's (which
  starts at the block the row is reached in); a loop or section jump starts
  a waiting one there, so it never waits a whole loop. Stop drops waiting
  ones.
- **Save games** store the tick (playing order, 960 a quarter); `StartAt`
  finds the bar, pass and section from it.
- **Headless.** With no device a score never gets a player (as a song); a
  level's `music.track`/`music.section` read what the level and scripts
  asked for (`foundry play run … read music.section`).

### Tests

| Test | Checks |
| --- | --- |
| `play`: `TestStingers`, `TestStingerCapAndAllocations`, `TestNewStingerPlaysOnce`, `TestPlayStartsWhereAsked` | onsets on the bar, the beat, the end, now and at a loop jump; the player outliving its piece for a stinger; the cap; no allocations; a stinger score plays once and leaves the cached score alone; `Play` at a section / resumed at a tick with params; `Preload` |
| `tracker`: `TestStingerGenerator` | any generator over a song, now and on a beat; nil refused |
| `world`: `TestMusicSteersAScore`, `TestMusicPauseFreezesAScore`, `TestMusicBlockNamesAScore` | section, param, mute (name and number) taken and remembered, unknown names refused, `play_song` of the playing score moves section, stop; the hard pause silent and frozen, then going on; the `music:` block |
| `script`: `TestScriptsSteerTheScoreAndHearItsEvents`, `TestScoreVerbs`, `TestPlaySongTakesAScore` | mirrors the song test: params/mutes remembered, `song_position`, marker, sections at the start and on a jump, beats from 0 with bars after, the end; `play_note`, `song_stinger` of a score file, `song_tempo` as half speed, refusals; `play_song` of a score file with no device |
| `savegame`: `TestAScoreResumesWhereItWasSaved` | capture → gob → apply: tick, section, playing, params, mutes, gain, fade; the resumed position |
| `harness`: `TestAWatchReadsAScoresTrackAndSection` | `music.track` / `music.section` for a score, headless |
| `pack`: `TestUnusedFollowsScoresAndTheirInstruments` (and phase 0's `TestScorePullsInItsInstruments`) | a score a script names, a stinger score, their instruments and samples are live; an unnamed score and its instrument are unused |

### How to verify

```
go test ./internal/score/play ./internal/tracker ./internal/world ./internal/script ./internal/savegame ./internal/harness ./internal/pack
foundry play run -level <a level with music: {track: score_demo, section: theme}> -draw=false \
    -script 'read music.track; read music.section'
```

Checked headless with a scratch project whose level names `score_demo` at
`theme` and whose script calls `play_song("score_demo", "bridge")` on a key:
the reads gave `score_demo`, `theme`, then `bridge`.

### Decisions

- **A song wins a name clash** (`.ftrk` over `.fscore`), as a song already
  wins over a recording.
- **`song_tempo` on a score is the tempo slider**, not a new tempo mark: the
  score keeps its tempo map and plays in proportion, clamped to 25–200 %.
- **Score beats are always posted**: a score has no `beat_events` key, and a
  beat a second or two is no load on the event channel.
- **Stingers mix both ways** (a score over a song, a song over a score),
  through `audio.Generator`, so a game can mix the two kinds freely.

### Deferred

- Nothing of step B's brief. The Claude panel does not reload a `.fscore`
  (or a `.ftrk`) it edits into a level already playing it; a level load or
  the editor's save does (`score.Save` drops the cache), as for songs.

## Verification pass

An adversarial pass over the code (not the reports) against SPEC's phase 2
line: every item was tried through the real ImGui frame, the GL shots, the
scheduler rendering offline, the CLI and the tests. What it found, all
fixed:

| Found | Fix | Test |
| --- | --- | --- |
| **A click on a note did not move the play head.** `seekToSel` existed and was tested directly, but the view's click (`scoreClick`) never called it, so SPEC's "click a note → playhead" and the guide's "or click a note" did not happen in the editor | `scoreClick` seeks to the clicked note or rest (playing or not), as the mockup's `seek` does | `editor`: `TestScoreClickThroughTheView` clicks a rest through the pane and checks the play head and the seek op |
| **`:song_bar:` could be lost.** The scheduler posts a downbeat's `EventBar` then its `EventBeat` from the mixer goroutine; a frame's drain falling between the two forgot the bar (the bar tick was a local of one drain) | the pending bar's tick lives on `ScriptRuntime` (`scoreBarTick`) across drains; `postScoreEventList` is the drain's body | `script`: `TestScoreBarSurvivesASplitDrain` posts a downbeat over two frames |
| **A looping score's WAV export started again in the tail.** "Whole score" rendered with no end tick, so a `loop: true` score jumped back to bar 1 during the reverb tail | the whole-score span runs to `perf.Length`, where the transport stops and the tail rings out | `editor`: `TestScoreExportOfALoopingScoreStopsAtTheEnd` (fails on the old code: sound at 16.1 s) |
| **A new program missed another line's solo.** `Player.install` re-muted only lines whose *own* mute/solo flags changed, so soloing line B through `SetScore` left line A sounding (the editor papered over it in `scoreAdopted`; any other `SetScore` caller did not) | `install` re-mutes where `LineSilenced` differs between the old and new score — a script's live mute of an untouched line still stands | `play`: `TestSetScoreFollowsAnotherLinesSolo` (fails on the old code) |
| **The meters barely moved.** Levels were drawn linearly, so a line playing at a normal level (-20 to -30 dB) filled 3–10 % of the bar; in the GL shot the piano's meter was four pixels | meters are in decibels, -48 dB empty to 0 dB full (`scoreMeterFrac`), yellow from about -14 dB, red from -6 dB, a cap over 0 dB — header and mixer alike | `editor`: `TestScoreGLWindowDraws` now shoots a `playing` frame with the real library rendered offline and counts the piano meter's green |
| **Nothing asserted the release links only score playback.** | `TestTheReleaseLinksOnlyScorePlayback` (in `internal/layering_test.go`): `go list -deps -tags release cmd/foundry` has `score`, `score/interpret`, `score/play` and no other score package (engraver, glyphs, edit, io, CLI) and no editor | itself |

Checked and found right: the hot swap (same performance, next note), live
faders/pan/sends/returns/master with one undo step per drag, the stale-mute
re-send after undo, the ruler's seek and drag-loop, L / Shift+L / Home /
Space / P, follow, the tempo slider and both Write readings, duplicate with
notes and empty, the instrument picker's search, the brace, percussion
staves (rendered through the CLI: the kit sounds, `peak 0.62`), every
template's instrument present in the shipped library, `play_music` /
`play_song` / `music:` reaching `startScore`, save-game capture and
resume, the event order, `song_tempo` as the slider, stingers either way.

### How to verify (the whole phase)

```
go test ./internal/score/... ./internal/tracker/... ./internal/editor/ ./internal/world ./internal/script \
    ./internal/savegame ./internal/harness ./internal/pack ./internal/app ./internal/
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
    # writes score.png, score_mixer.png, score_loop.png, score_small.png, score_playing.png
foundry score render internal/score/engrave/testdata/percussion.fscore -o /tmp/perc.wav
foundry score render internal/score/engrave/testdata/percussion.fscore -png /tmp/perc.png -scale 24
task test-release
```

The five GL shots were looked at after the fixes: headers with name, chip,
M/S, duplicate and volume; the brace; the ruler's bars, times and beat
ticks; the loop's shading; the play head through every staff, following
the player while it plays; the meters lit while playing; the mixer's
strips, buses and master; the compact headers.

### Deferred (the whole phase)

Nothing of SPEC's phase 2 line is deferred. What later phases pick up,
each named for its phase in SPEC (repeated from the steps above):

- Phase 4: line insert effects and bus effects beyond reverb/delay/compressor
  play (the mixer lists them as stored); automation lanes; the Timeline tab.
- Phase 5: OGG, MP3, FLAC, stems and the export dialog of mockup screen 6
  (WAV is here).
- Phase 6: per-instrument drum maps (every percussion staff uses the GM kit
  map, keys 35–59); the instrument library window of mockup screen 4 (the
  header's quick picker with search is here).
- Not named by any phase, kept as is: reordering lines by dragging a header
  (Move Up / Move Down do it); the Claude panel reloading a `.fscore` it
  edits into a level already playing it (as for `.ftrk`; a level load or
  the editor's Save picks it up).
