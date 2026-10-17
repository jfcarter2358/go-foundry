# Phase 4 — DAW layer

SPEC's line for this phase: insert effects per line + master; two send
buses; automation lanes (volume, pan, filter cutoff, any fx param) drawn
and edited in the timeline; timeline view (tracks, regions, lanes) and an
editable piano roll of the selected line, all views over the same model;
SFZ and SF2 import → `.finst`; swing and humanise.

Step A is the sound of it: the effects, the routing, automation played
sample by sample, swing and humanise, the file grammar for all of it, and
the Mixer pane's effects rack. Step B is the views over it and the
libraries: the Timeline tab (tracks, regions, automation lanes drawn and
edited), the piano roll of the selected line (notes drawn as notation, a
velocity lane), SFZ and SF2 import into `.finst`, and the instrument
library dialog that imports them. The verification pass (last section)
added lanes for the send buses and the master — "any fx param" includes
theirs — and fixed what it found.

## Step A: effects, routing, automation playback, swing and humanise

### At a glance

| Package / file | What |
| --- | --- |
| `internal/tracker/fx` (new) | `fx.go`: `Kind`, `Specs` (every effect's parameters: name, label, unit, range, default, log/int/choices; `BusMix` — wet only on a bus), `Effect` (`Process` in place on interleaved stereo, `SetParam(i, v, frames)` glides, `SetTempo`, `Reset`), `New`; parameters glide linearly (in log2 space for frequencies, times and ratios), switches and counts step, a value at rest costs nothing; effects work in 64-frame chunks so a glide re-designs filters at control rate |
| `fx/eq.go` | Parametric EQ: low and high shelves + four bells, RBJ biquads in float64, a band at 0 dB out of the signal (a fresh EQ is an exact wire), output gain |
| `fx/modulation.go` | Chorus (two modulated taps a side, rotating-phasor LFO, spread, feedback), phaser (2–12 first-order allpasses swept on a log scale, feedback, stereo spread) |
| `fx/distortion.go` | Drive (the Tracker's tanh at unity small-signal gain — the drive trap — plus tone and output), bit crusher (bits + fractional sample-and-hold downsample) |
| `fx/time.go` | Delay (tempo-synced beats; the echo line glides to a new length over 60 ms on a tempo change or a turn of the knob, so a rit. does not click), reverb (Freeverb) |
| `fx/dynamics.go` | Compressor (the Tracker's soft-knee peak compressor), limiter (1.5 ms look-ahead brick wall: running minimum + window average, so no sample passes the ceiling), filter (TPT state-variable low/high/band-pass, 12 or 24 dB/oct) |
| `fx/chain.go` | `Chain`: effects in series; bypass crossfades over 256 frames and a bypassed effect is not run; `SameKinds` / `Take` move a playing chain to a rebuilt one's settings keeping its tails |
| `fx/kernels.go` | The Tracker's own DSP blocks, moved here unchanged and exported: `SVF`, `Freeverb`, `Echo` (+ `GlideTo`), `Comp`, `DriveK`/`Drive`, `CrushSteps`/`Crush`, `flush` for denormals. `internal/tracker/dsp.go` keeps only the spec → kernel setters |
| `internal/tracker/engine.go`, `engine_fx.go` (new) | The engine's **effects path**, on once `SetBusChains(a, b, master)` is called (a Tracker song never calls it, so its renders are bit-identical — the golden tests pass unchanged): per strip the strip's filter/drive/crush, its insert chain (`SetInserts`; it rings on after the notes until silent for `MaxEchoSeconds + 1`), fader and pan ramped across the sub-block, then the sends pre- or post-fader (`StripControls.PreA/PreB`); each bus runs its chain and returns with its own level, pan and mute (`SetBus`), the master chain runs before the master gain. `CueStrip` takes `Frames` (the gains reach the new controls in a straight line over exactly that), `CueFX` glides one effect parameter of a strip's, a bus's or the master chain (`FXBusA`, `FXBusB`, `FXMaster`); `Chain(target)`, `BusLevel(bus)` |
| `internal/score/mix.go` (new) | `Swing{Amount, Unit}`, `Humanize{Timing, Velocity, Seed}`, `SwingFor`/`HumanizeFor` (a line's own replaces the score's); lane targets `ParseLaneTarget` — `volume`, `pan`, `cutoff`, `send_a`, `send_b`, `<fx>.<param>` and `<fx>#<n>.<param>` — with `Range`, `Line.InsertSlot`, `LaneTargetForSlot`; `LaneShape` (linear, step, curve) and `ShapeValue`; `NewFX`, `FX.SetParam`, chain validation |
| `score.go`, `fscore.go`, `validate.go` | `FX.Bypass`; `Bus.Pan/Mute`; `Line.PreA/PreB`, `Line.Swing/Humanize`; `PlaybackOptions.Swing/Humanize`; `LanePoint.Shape/Curve`; the grammar below; validation of lane names (unknown target: error; an effect lane with no such insert: warning), lane values against their range, unknown effects / settings / out-of-range values on every chain |
| `internal/score/interpret` | `feel.go` (new): swing as a warp of performance time inside each pair of eighths or sixteenths (from the bar's start, a pickup's from its full bar's; a pair the bar cuts short stays straight), before the tempo map, moving notes, ends, slide breakpoints and graces in proportion; humanise as a seeded hash of each note's place (line, staff, voice, tick, pass — so a chord moves together and an edit elsewhere leaves every other nudge alone), triangle-distributed. `interpret.go`: lanes with step and curve segments (a curve as 24 straight pieces), `CurvePoint.Step`, `Curve.Before` (the value arriving at a tick), `Curve.Constant`, the last bar's end point |
| `internal/score/play/mixer.go` (new), `player.go`, `program.go` | The Program holds each chain as a plan (kinds, values, bypass); each Player builds its own chains on the game thread (at the device's rate when one is open) and hands them over with the program; a new program whose chain has the same effects keeps the playing chain and glides it to the new settings (`Chain.Take`), so an edit never cuts a reverb tail. Lanes: once a block their values at the play head; while playing, `cueLanes` cues every moving line every 64 frames and on each breakpoint, each cue carrying the value at the next — the engine and the effect ramp exactly across it, so a straight lane is followed sample by sample and a step lands on its frame. `SetEffects` (a structural chain change, performance shared), `SetFX`, `SetBypass`, `SetBus`, `BusLevel`, `Engine`; `LineMix.PreA/PreB`; `sameNote` finds a note humanise moved. The phase 0–3 `Buses`/`Unplayed` stand-ins are gone |
| `internal/score/edit/mix.go` (new) | `AddEffect`, `RemoveEffect`, `MoveEffect` (≤ `MaxEffects` = 8 a chain); `AddInsert`, `RemoveInsert`, `MoveInsert` keep a line's effect lanes on their effect (renamed `eq#2.low` → `eq.low` when the first EQ goes; a removed effect's lanes go, counted); `HasEffectLanes` |
| `internal/score/io/midi` | The pan lane exports as CC 10, the cutoff lane as CC 74 (brightness, log scale), beside the volume lane's CC 11 |
| `foundry score inspect` | Each line's sends (pre marked), inserts (bypassed / off marked), lanes, own swing and humanise; each bus's return, pan, mute and chain; the master chain; the score's swing and humanise |
| `editor_score_mixer.go` | The Mixer tab's rack (mockup screen 3): every strip's chain as slots — click the name for its settings popup (a slider per parameter, log where the parameter is, combos for switches, double-click for the default, Bypass/Reset/Remove, which lanes automate it), the dot to bypass, right-click for Settings / Bypass / Move Up / Move Down / Reset / Remove; dashed **+ Insert** opens the effect list (each with its description); send knobs' right-click toggles pre-fader (marked **pre**); bus strips with their chain, return pan knob, fader, meter (`BusLevel`), mute; master strip with its chain, fader and the mix's meter |
| `editor_score_fx.go` (new) | The editing model with no ImGui: `addFX`, `removeFX`, `moveFX`, `toggleFXBypass`, `setFXParam` (one undo step per drag), `resetFX`, `togglePre`, `setBusPan`, `toggleBusMute`; `scoreSyncFX` tells the player only what moved (a parameter as `SetFX`, a bypass as `SetBypass`, the buses as `SetBus`, a chain whose effects changed as one `SetEffects`) |
| `editor_score_feel.go` (new) | Play > Swing and Humanise…: the score's or a line's own (with one line's bars selected, or picked in the popup), amount 50–75 % on eighths/sixteenths, timing ±ms, velocity ±%, seed; one undo step per drag; a change to the music (performed again) |
| `docs/guide/32-score-editor.md` | The grammar, an *Effects* table of every effect's settings and defaults, the rack, swing and humanise, how lanes play; `internal/score/guide_test.go` parses the guide's example file |

### The file

```yaml
playback: {swing: {amount: 0.62, unit: 8}, humanize: {timing: 8, velocity: 0.06, seed: 1}}
lines:
  - name: Bass
    sends: {a: 0, b: 0.4, pre_b: true}
    inserts: [{fx: drive, drive: 0.4, tone: 0.5}, {fx: filter, cutoff: 900, bypass: true}]
    swing: {amount: 0.5}                     # this line plays straight
    humanize: {timing: 12, velocity: 0.1}    # the score's seed
    lanes:
      volume: [{at: "1", value: 1}, {at: "6:3", value: 0.6, step: true}, {at: "7", value: 0.9}]
      filter.cutoff: [{at: "1", value: 300, curve: 0.6}, {at: "5", value: 4000}]
      delay#2.feedback: [{at: "3", value: 0.2}, {at: "4", value: 0.7}]
      send_b: [{at: "1", value: 0}, {at: "3", value: 0.5}]
buses:
  a: {fx: [{fx: reverb, size: 0.7}, {fx: eq, high: -4}], return: 0.5}
  b: {fx: [{fx: delay, beats: 0.75}, {fx: filter, mode: 1, cutoff: 400}], return: 0.35, pan: -0.2, mute: false}
master: [{fx: eq, low: 1}, {fx: compressor, threshold: -12}, {fx: limiter, ceiling: -1}]
```

An effect is `{fx: <kind>, <setting>: <value>, …}`; a setting left out
takes its default for where the effect sits (`mix` is 1 on a bus). A lane
point's `step: true` holds its value to the next point, `curve: -1..1`
bends the segment after it; neither is a straight line.

## Step B: the timeline, the piano roll, SFZ and SF2

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/io/sfz` (new) | `parse.go`: SFZ text → `Region`s — `<control>/<global>/<master>/<group>/<region>` folded (each level overriding the one above), `#define`, `#include` (anywhere on a line), `//` and `/* */` comments, note names (`c4` = 60, `c#4`, `db4`), `octave_offset`, `note_offset`, `default_path`, sample paths with spaces; keys/velocities, `pitch_keycenter` (`=sample`), `transpose`, `tune`, `volume`, `amplitude`, `pan`, `offset`, `end`, `loop_mode`/`loop_start`/`loop_end`, `ampeg_*`, `seq_length`/`seq_position`, `lorand`/`hirand`, `sw_last`/`sw_down`/`sw_label`/`sw_default`/`sw_lokey`/`sw_hikey`, `trigger`, `fil_type`/`cutoff`/`resonance`; anything else counted in `File.Ignored`. `wav.go`: a WAV's `smpl` chunk (root, pitch fraction, loop). `build.go`: `Build` — the keyboard cut wherever any region starts or stops (so overlapping regions map exactly onto the engine's first-match zones), velocity layers, round robin in turn order (stacked regions with no round robin: the first kept, counted), keyswitches as articulations named like the starter library's (`ArticulationName`: Pizz → pizzicato, Spiccato → staccato, Trem → tremolo, Con sord. → muted …, the label a trigger), unswitched regions in every articulation, a default keyswitch's own word as an articulation over the default zones; each distinct way a region plays a file (stretch, loop, root, tune, volume) one library sample; ADSR → a volume envelope with a sustain point, or a fade-out when it only switches; a uniform pan or filter carried; playable range without the keyswitch range, a clef guessed from it; `Built.Write` → `instruments/imported/<name>.finst` + `samples/imported/<name>/<file>.wav/.fsmp`; `Import`; `Slug`, `Title` |
| `internal/score/io/sf2` (new) | RIFF `sfbk`: INFO name, `sdta` 16-bit `smpl` (+ `sm24`), the `pdta` hydra (`phdr pbag pmod pgen inst ibag imod igen shdr`); global zones; a preset zone's generators added to its instrument's (ranges intersect); generators read: key/velocity range, root key, coarse/fine tune + the sample's pitch correction, sample modes, all address offsets (fine and coarse), the volume envelope (timecents, sustain in cB), attenuation, pan, initial filter; linked left/right samples under the same keys → one stereo sample; modulators, the modulation envelope, LFOs, exclusive class, scale tuning and ROM samples counted. `Regions(preset)` → `sfz.Region`s, then `sfz.Build`, so the two importers agree. `Import`: a bank of one preset is `imported/<bank>.finst`, of several a folder `imported/<bank>/<preset>.finst` (whichever presets are picked) |
| `internal/score/io/instimport` (new) | `Import(path, Options)` by extension, `Supported`, `Exts` — the CLI's and the dialog's one entry point |
| `foundry tracker import-instrument` | `<file.sfz|.sf2> [-name imported/x] [-preset 0,3] [-list]`: prints each instrument's zones, samples, range, articulations and the import's notes |
| `internal/score/edit/roll.go` (new) | `RollNotes` (a line's notes as timed notes, every staff and voice, tie chains merged, with their events), `RollFreeVoice`, `RollAdd`, `RollRemove`, `RollReplace`, `RollMove` (many at once: out first, then in, so a moved run never lands on itself), `RollSetVelocity`. Writing back re-notates the bars an edit touches (widened to whole tie chains) through `notate.Write`: cut at every start and stop, each stretch a chord, the notes held through tied (`Chord.TieNext`), values the meter allows, tuplets where an onset needs one; kept: each note's spelling (`Chord.Pitches`) and accidental display, the event's marks and grace notes at the same onset (`Chord.GraceEvents`), a rest's marks; a note moved on its own takes its note marks (`noteMarks`) and graces, a dynamic stays on the rest left |
| `internal/score/io/notate` | `Chord.Pitches`, `Chord.TieNext`, `Chord.GraceEvents` (all optional; importers unchanged) |
| `internal/score/edit/lanes.go` (new) | `AddLane` (one point at bar 1 holding what the line plays without it; a switch's lane stepped), `RemoveLane`, `AddLanePoint` (takes the shape of the segment it splits), `MoveLanePoint` (stops at its neighbours, keeps a hold's length), `RemoveLanePoint` (a lane keeps one), `SetLaneShape`, `DrawLane` (a stroke replaces the points it crosses, Ramer–Douglas–Peucker-thinned in the lane's normalised, log-for-frequency scale), `LaneNorm`/`LaneDenorm` |
| `internal/score/mix.go` | `LaneTarget.Label`, `Log`, `Stepped`, `Format`, `Clamp`; `Line.LaneDefault`; `Score.LaneValue` (a lane at a written time as the first pass plays it) |
| `internal/score/marks.go`, `validate.go`, `interpret` | the `vel=1..127` mark (`MarkPlayback`: prints nothing): its notes play at that velocity whatever the dynamics say (no accent on top); `Event.VelocityMark`, `SetVelocityMark`, `ParseVelocity`; validated |
| `editor_score_timeline.go` (new) | The **Timeline** tab (mockup screen 2): `scoreTimelineGeom` (one time axis for the ruler, tracks and roll, its own zoom and scroll; Ctrl+wheel zooms, Shift+wheel / the bar along the bottom scrolls, the wheel scrolls the tracks or the keys), the ruler (seek, drag-to-loop), tracks (name, instrument, M S R, the lanes switch with its count, + for a lane; the region with the line's notes as a strip, the bar selection; click selects the bar and gives the roll the line, double-click goes to it in the Score tab), automation lanes (curve sampled with exact step corners, points, holds, the stroke as it is drawn, the value at the play head; click adds, drag moves on the grid — Shift off it — heard as it moves; right-click for the segment's shape, the bend, delete), the piano roll (keys that sound when clicked, rows, bar/beat/grid lines, notes in voice colours, tied parts' joins, the roll's and the staff's selection outlined, ghosts of a move, stretch, draw or box), the velocity lane (stalks grey from the dynamics, coloured where set; drag sets, double-click clears), the play head, follow, the tracks' scroll thumb; popups for adding a lane and a point's menu |
| `editor_score_timeline_edit.go` (new) | The editing model with no ImGui: `scoreGrid` (1/4 … 1/32, triplets, off) and `scoreGridSnap` (from each bar's start, a pickup from its full bar's); tracks' arm and lanes switches (kept by line name, through renames); `autoAdd/Remove/Press/Move/Release/Delete/Shape` (a drag one undo step, heard while it goes through `laneLive`); `rollDraw` (the pitch's staff — a grand staff splits at middle C —, the current voice or the next free one, spelled as the staff is written), `rollMove` (selection, time on the grid, semitones, stretch), `rollDelete`, `rollVelocity`, `rollSelect` (one note selected on the staff too), `rollSelectInBox`; `scDelete` acts on the lane point or the roll's notes first in the Timeline |
| `editor_score_library.go` (new) | The **instrument library** dialog (mockup screen 4): categories with counts, the list (search, `.finst` / SFZ / SF2 tag, articulation count; double-click uses it), details (file, range, velocity layers named by dynamic, round robin, clef, gliss, samples, articulations as chips, the keyboard with the range lit), **Preview** (its own engine on the music bus, before choosing), **Use on** / **Add the Line**; **Import SFZ / SF2…**: a browser of folders and importable files, a SoundFont's presets to tick, the import in the background, the new instruments selected with the import's notes in the status. Opened from a line's menu (**Instrument Library…**) and the quick picker's **Library…** |
| `editor_score_window.go`, `_ruler.go`, `_keymap.go`, `_header.go`, `_lines.go`, `_edit.go` | Timeline / Score / Mixer tabs; View > Timeline; `scTimeline` (`Ctrl+Alt+M`), `scScoreView`, `scRollDraw` (`Ctrl+Alt+D`); `scoreState.TL`; the dialogs drawn from `scoreDialogs` |
| `docs/guide/32-score-editor.md`, `31-music-tracker.md` | *The timeline and the piano roll*, *The instrument library dialog*, *Importing SFZ and SF2* (what carries over, what cannot), the `vel=` mark, `import-instrument` |

### The file

Nothing new but one mark: `qG5(vel=110)` plays its notes at MIDI velocity
110. Lanes, inserts and everything the Timeline edits are step A's grammar.

## How to verify

```
go test ./internal/tracker/... ./internal/score/... ./internal/editor/ ./internal/world ./internal/app
go test -run xxx -bench 'BenchmarkPlayer32Channels|BenchmarkChain' -benchmem ./internal/tracker/ ./internal/tracker/fx/
go test -run xxx -bench BenchmarkScheduler16Lines -benchmem ./internal/score/play/
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
    # also writes score_effects.png (the rack with an EQ's settings open) and score_feel.png
foundry score inspect internal/score/scorecli/testdata/mixer.fscore
foundry score render internal/score/scorecli/testdata/mixer.fscore -o /tmp/mixer.wav
go test -tags release ./internal/score/... ./internal/tracker/... ./internal/world
    # step B:
go test ./internal/score/io/sfz/ ./internal/score/io/sf2/ ./internal/score/io/instimport/ ./internal/score/edit/ ./internal/tracker/trackercli/
go test -run 'TestScoreGridSnap|TestScoreRoll|TestScoreAutoLane|TestScoreTrackSwitches|TestScoreTimeline|TestScoreLibrary' ./internal/editor/
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
    # also writes score_timeline.png, score_library.png, score_import.png
foundry tracker import-instrument <some.sfz>      # or .sf2 -list / -preset
```

Measured here: `BenchmarkPlayer32Channels` 0.42 ms per 10 ms block (the
budget is 0.5), 0 allocations; every effect in one chain 0.16 ms per 10 ms
block; `BenchmarkScheduler16Lines` 0.26 ms, 0 allocations.

The demo's golden hash moved from `54bff7c5ac84c913` to `74ceacc483c638a9`
because its volume lane is now followed sample by sample (ramped, not set
once a block) and its delay bus glides through the rit. instead of jumping.
With one tempo and no lane it renders bit for bit what phase 3's mixer did
(`TestDemoAtASteadyTempoWithNoLanesIsUnchanged`), though every line, bus and
the master now go through `fx` chains.

### Tests

| Package | Tests |
| --- | --- |
| `fx` | `TestSpecsAreConsistent`, `TestEveryEffectIsAllocationFree`, `TestNoDenormals` (every effect at its longest-memory settings, a burst then 12 s of silence), `TestEQ` (flat = wire, shelf step response, bell gain at its frequency), `TestFilter` (step responses: low-pass settles at 1, high-pass dies), `TestDriveKeepsUnityForQuietSignals`, `TestCrusher`, `TestDelayIsTempoSyncedAndGlides` (impulse lands on the beat; a tempo change glides without a step), `TestReverbTail`, `TestCompressor`, `TestLimiterIsABrickWall`, `TestChorusAndPhaser` (impulse delay, the notch), `TestParametersGlide`, `TestChainBypassAndTake`, `TestEchoKernelJumpsLikeTheTracker`, `BenchmarkChain` |
| `tracker` | `TestEffectsPathMatchesTheTrackersMix` (empty chains + the default effects = the Tracker's mix), `TestEffectsPathRampsStripControls`, `TestEffectsPathInsertsSendsAndBuses` (pre/post-fader, bus pan/mute, insert tails), `TestEffectsPathCuesAnEffectParameter`, `TestEffectsPathRendersWithoutAllocating`; every existing render and song golden unchanged |
| `score` | the random round trip covers every new key; `TestTheGuidesExampleParses` |
| `interpret` | `TestSwingWarpsEachPair`, `TestSwingKeepsBarsAndFollowsLines`, `TestHumanizeIsBoundedAndRepeatable`, `TestLaneShapes` |
| `play` | `TestInsertsPlay`, `TestBusesAndMasterPlayTheirChains`, `TestAStepLaneIsSampleAccurate`, `TestALinearLaneIsFollowedSmoothly`, `TestALaneMovesAnEffectParameter`, `TestDynamicsComposeWithTheVolumeLane`, `TestAutomationRendersWithoutAllocating`, `TestLiveEffectCommands`, `TestSwingPlays`, `TestSameNoteUnderHumanise` |
| `edit` | `TestEffectChains`, `TestLanesFollowTheirInserts` |
| `midi` | `TestExportPanAndCutoffLanes` |
| `scorecli` | `TestMixerFixturePlays` (every effect, every lane kind, swing and humanise through the real library: deterministic, under the limiter, inspect lists it), `TestDemoAtASteadyTempoWithNoLanesIsUnchanged`, the re-captured golden |
| `editor` | `TestScoreEffectsEditing`, `TestScoreEffectsHeardLive`, `TestScoreMixerEffectsThroughTheView`, `TestScoreSwingAndHumanise`, `TestScoreFeelPopupDraws`, `TestScoreGLWindowDraws` (effects and feel shots) |
| `sfz` (step B) | `TestParseFoldsHeaders`, `TestBuildMapsTheRegions` (zones, layers, round robin, keyswitched pizzicato, rendered at the right pitch through the Engine), `TestBuildLoopsTuningAndEnvelope`, `TestImportWritesAPlayableInstrument` (through the library cache), `TestMissingSamples`, `TestArticulationNames` — fixtures are WAVs (with smpl chunks) written by the test |
| `sf2` (step B) | `TestParseAndRegions`, `TestImportWritesEveryPreset` — the test writes a small SoundFont (a split piano with a velocity layer, a looped tuned sample, a global zone; a stereo pad from a linked pair narrowed and attenuated by its preset) and plays what comes back |
| `instimport`, `trackercli` (step B) | `TestImportByExtension`, `TestImportInstrumentVerb` (then `tracker check` is clean) |
| `edit` (step B) | `TestRollNotesMergeTiesAndChords`, `TestRollAddWritesNotation`, `TestRollAddTupletsAndOddLengths`, `TestRollOverlapAndFreeVoice`, `TestRollReplaceKeepsMarks`, `TestRollRemove`, `TestRollTieChains`, `TestRollVelocity`, `TestLaneEditing`, `TestDrawLane`, `TestFXLanes` |
| `interpret` (step B) | `TestVelocityMarks` |
| `editor` (step B) | `TestScoreGridSnap`, `TestScoreRollEditing`, `TestScoreRollStavesAndSpelling`, `TestScoreAutoLaneEditing`, `TestScoreTrackSwitches`, `TestScoreTimelineThroughTheView` (a note drawn by a drag on the roll, moved, its velocity dragged and double-clicked back, a double-click draw, a region click, a lane point added by a click, every popup), `TestScoreLibraryImportsAndUses` (import through the dialog's background path, details, use on a line, the dialog and its import panel drawn), `TestScoreLibraryReadsTheShippedLibrary`, `TestScoreGLWindowDraws` (timeline, library, import shots) |

## Decisions

- **The Tracker keeps its mix; a Score gets the effects path.** One engine,
  two mixes: `SetBusChains` switches an engine onto chains. The Tracker's
  fixed reverb/delay/compressor and per-channel filter/drive/crush are
  the same kernels as the `fx` effects (moved, not copied), so the two
  sound alike where they overlap and the Tracker's goldens do not move.
- **Smoothing is in the effect.** `SetParam` takes the frames to glide
  over: a knob is 20 ms, an automation cue exactly the stretch to the next
  cue. A lane is therefore piecewise linear at ≤ 64 frames a piece, with
  its breakpoints exact.
- **A bypass fades** (256 frames) instead of cutting, and a bypassed
  effect costs nothing. Bringing one back that has not run starts it from
  silence (no stale tail).
- **Chains survive edits.** A program with the same effects in the same
  order glides the playing chain to the new values; only a structural
  change (an effect added, removed, moved) builds a new one.
- **Lanes name effects by kind and number** (`eq#2.low`), not by slot, so a
  lane follows its effect through a reorder; edits rename them. A line
  whose lanes follow its inserts makes a structural chain change an edit
  to the music (the performance's curve names change); otherwise it is a
  mixer step.
- **The cutoff lane is a low-pass over the line** (the strip's own filter),
  combined with a cutoff binding by taking the lower; a filter insert's
  `filter.cutoff` is a separate lane.
- **Swing happens in score time, humanise in seconds.** Swing warps
  written positions before the tempo map (so it scales with tempo, as a
  player's does); humanise nudges by milliseconds (so a slow passage is not
  sloppier than a fast one). Both are deterministic.
- **Delay feedback is 0..0.95** (the echo's own ceiling): the slider no
  longer offers values above what plays.

Step B:

- **A piano-roll edit writes notation.** The bars it touches are written
  afresh through the importers' notate step (so drawn notes come out in the
  values a MIDI import would choose — a dotted rest where notate allows
  one, a triplet where the grid put the note), keeping spellings,
  accidental display, marks and graces where they still apply. Bars it
  does not touch are left exactly as written.
- **Drawing never loses a note.** A note that would cut one in the current
  voice goes into the first voice free there; one that starts and stops
  with a note in the voice is a chord with it. Moving uses the same rule.
- **Velocity is a mark.** The piano roll's velocity lane writes `vel=` on
  the note's event (a chord shares it), which replaces the dynamics' level
  for those notes and prints nothing — Sibelius's "live velocity". Removing
  it (double-click) gives the note back to the dynamics.
- **Lane drags change the score as they go** (as the dynamics lane's do,
  paced through `laneLive`) and are one undo step; note drags are a ghost
  until released.
- **Timeline time is written time**, bars as printed, a repeat once — the
  same positions lanes are stored in; the play head maps there from the
  performance.
- **One region model for both importers.** SF2 zones are read as SFZ
  regions and built by the same code, so zones, layers, loops, envelopes
  and naming agree. Loop points and tuning live on the samples (the
  `.fsmp`), so each distinct way of playing a file is its own sample.
- **Imports go under `imported/`** for instruments and samples; a bank of
  several presets is a folder whichever presets are picked, so a preset
  imported later does not land on another.

### Deferred

- **MIDI recording into armed lines.** The record-arm switches arm lines
  (and the roll shows an armed line); recording needs MIDI device input,
  which SPEC puts in phase 6 with step entry and real-time recording.
- **FLAC samples in an SFZ.** The audio decoders read WAV, OGG and MP3
  only, and no FLAC decoder is in SPEC (phase 5 adds a FLAC encoder for
  export). A region naming one is skipped with a note saying to convert it
  to WAV.
- **SFZ/SF2 features with no place in a `.finst`**: release triggers,
  modulators/CC, the modulation envelope and LFOs, `<curve>`/`<effect>`,
  stacked layers with no round robin (the engine plays one sample a layer).
  Each is counted in the import's notes rather than dropped silently.

## Verification pass

An adversarial pass over steps A and B: the code read against SPEC's
phase 4 line and the mockups, randomised and corrupted-input tests where
coverage was thin, renders and GL screenshots looked at, a real SoundFont
(TimGM6mb, 136 presets) imported and played.

### Added: lanes on the send buses and the master

SPEC asks for automation of "any fx param"; steps A and B automated only
a line's own inserts. Now every chain's effects can be automated, and the
buses' and the master's levels too.

| Where | What |
| --- | --- |
| File | a bus's `lanes:` (`return`, `pan`, `<fx>.<param>` of the bus's effects) and the score's `master_lanes:` (`volume`, times `gain`; `<fx>.<param>` of the master chain); points as a line's |
| `internal/score` (`mix.go`, `score.go`, `fscore.go`, `validate.go`) | `Bus.Lanes`, `Score.MasterLanes`; lane owners — a line's index or `OwnerBusA`/`OwnerBusB`/`OwnerMaster` — with `LanesOf`, `ChainOf`, `LaneOf`, `OwnerName`, `HasOwner`, `LaneDefaultOf`; `LaneScope` and `ParseLaneTargetIn` (each scope's own names), `LaneReturn`, `ChainSlot`, `ChainTargetForSlot`, `ScopeLaneKinds`; one `lanesFromRaw`/`lanesToRaw` for lines, buses and the master; one `validateLanes` |
| `interpret` | the buses' and master's lanes become curves too (`Curve.Line` is the owner) |
| `tracker.Engine` | `CueBus` (a bus's return, pan, mute ramped over `Frames`), `CueGain` (the master gain ramped); on the effects path the bus returns ramp across a cue's frames like a strip's, and the master gain ramps (it used to step) |
| `play` | `Program.mixLanes`; lane units (the lines, then bus A, bus B, the master) share one `cueLanes`; the player keeps `busBase`/`gainBase` (the score's or what `SetBus`/`SetReturns`/`SetGain` set) and lays the lanes over them every block and every cue |
| `edit` | every lane call takes an owner; `AddOwnedEffect`, `RemoveOwnedEffect`, `MoveOwnedEffect`, `OwnerHasEffectLanes` keep a bus's or the master's lanes on their effects as `AddInsert` … do a line's (now thin wrappers) |
| Timeline | **Bus A**, **Bus B** and **Master** tracks under the lines (name, **A**, **+**, the chain written across, no region) with their lanes, edited, drawn and heard as a line's; the add menu offers each owner's own targets |
| Mixer | moving or removing a bus or master effect with lanes renames or drops them (one undo step); an effect's settings say which of its parameters are automated on any chain |
| `foundry score inspect` | each bus's and the master's lanes |
| Guide | the file block, the key table, the Timeline section, *How a score plays* |

Tests: `TestMixLanes` (`score`: grammar, round trip, validation by scope),
the random round trip covers them; `TestMasterLanes`, `TestBusLanes`,
`TestMixLanesRenderWithoutAllocating` (`play`: a master fade followed
sample by sample at odd block sizes, a step on its frame under a master
gain, master and bus effect lanes, a return lane, a pan sweep, a live bus
command under a lane); `TestMixOwnerLanes`, `TestRandomLaneEdits` (`edit`);
`TestScoreMixOwnerLanes` and the master lane added through the view in
`TestScoreTimelineThroughTheView` (`editor`); the mixer fixture now has bus
and master lanes; the GL test's `score_timeline_mix.png`.

### Fixed

- **A strip started at its first cue's target**, not where it was first
  set: the effects path primed a strip's gains in the first sub-block it
  mixed, after the frame-0 lane cue, so the first piece of every lane
  jumped (by up to the whole first block when the cue queue was short).
  `SetStrip` primes them now.
- **Lane cues could be refused.** The engine's queue holds 1024 cues; many
  automated lines under a long device buffer overflowed it at one cue per
  64 frames, and refused cues left lanes behind. `cueLanes` now counts the
  cues a block needs and widens the grid for all moving lines alike when
  they would not fit beside `laneReserve` (256) for notes, keeping every
  breakpoint while there is room (`Engine.CueRoom`;
  `TestManyMovingLanesInALongBlock`: 40 automated lines, 8192-frame blocks).
- **Humanise left slides behind**: a nudged note's bend breakpoints stayed
  at their written ticks (up to 50 ms off its own start). They move with
  the note now (`TestHumanizeMovesASlideWithItsNote`).
- **A piano-roll move could cut a note**: when a moved note's voice and
  every other voice were taken, it was written into its own voice anyway,
  cutting the note there, and two notes moved note-by-note into free
  voices did not see each other. `RollMove` now writes each such note
  before the next looks, refuses (with the score left exactly as it was —
  the move is all or nothing) when no voice is free, and the editor says
  why (`TestRollRandomEditsKeepEveryNote`: thousands of random draws,
  deletes, moves and stretches on eighth, triplet and sixteenth grids in
  4/4, 6/8 and 3+2+2/8, the note set checked after every step;
  `TestRollMoveRefusedLeavesTheScore`).
- **SFZ: one bad value refused a whole library.** A value an opcode cannot
  take (`lokey=h2`, `tune=nan`) is now skipped with a warning, once per
  place, the region keeping what it had, as players do; NaN, infinite and
  absurd numbers are refused rather than written into a `.finst`.
- **Import names could leave the library**: `-name ../x` (or the dialog's
  equivalent) wrote outside `sounds/instruments/`. `sfz.Build` refuses a
  name with an empty, `.` or `..` part, a backslash or a colon.
- **SF2 imports were 5–15 dB quiet and dull.** `initialAttenuation` is now
  applied at the EMU/FluidSynth 0.4 the banks are voiced for; a filter the
  modulation envelope opens (`modEnvToFilterFc`) is set where the envelope
  holds it over a note's first second rather than at its closed cutoff (a
  GM piano was low-passed at 440 Hz); the modulator count in an import's
  notes is the preset's own, not the whole bank's.
- **Import zones were fragmented**: neighbouring keyboard stretches that
  play alike are one zone (TimGM6mb's piano: 33 zones to 9); regions whose
  cutoffs are within half an octave share one filter at their geometric
  mean (with a note) instead of the instrument playing unfiltered.
- **Sends and returns out of range** played as written; they are now
  clamped (sends 0..1, returns 0..4, bus pan -1..1) and `Validate` warns.
- **Timeline**: a track or lane row scrolled partly under the ruler drew
  over it, and lost its buttons and press area entirely; rows are clipped
  to the tracks area, each button shows when it is itself in sight and a
  row's press area is cut to its visible part. The tracks' scroll is kept
  within the rows every frame (hiding lanes left it past the end).
- **Library dialog**: a long file name ran under the panel's edge (it goes
  on its own line); a panic in the background import would have taken the
  editor down (recovered, reported as the import's error).

New robustness tests: `fx` `TestExtremeSettingsStayFinite` (every effect at
every corner of its parameter space, all at one end, random automation in
odd blocks, NaN and out-of-range settings; outputs finite, tails dying
away) and `TestChainSurvivesExtremes`; `sf2` `TestCorruptBanksDoNotPanic`
(4000 damaged banks) and `TestModulatorsCountedPerPreset`; `sfz`
`TestRandomFilesDoNotPanic` (3000 generated files, zones checked),
`TestNamesStayInTheLibrary`, `TestNearlyEqualFiltersAreOne`.

### Checked and found right

The effects path's sound with no lanes (the Tracker's goldens and the
steady-tempo score golden unchanged), every effect's allocation-free
`Process`, a real SoundFont's presets imported and played at the right
pitches (`foundry tracker import-instrument TimGM6mb.sf2 -preset …`, then
a score rendered and analysed), the mixer fixture's render (smooth levels,
under the limiter), the GL screenshots (effects rack, feel popup, mixer,
timeline, mix tracks, library, import), `BenchmarkPlayer32Channels` 0.42 ms
per 10 ms block, `BenchmarkScheduler16Lines` 0.27 ms, both 0 allocations;
the release build links none of sfz, sf2, instimport, edit or the editor.

### How to verify the pass

```
go test ./internal/tracker/... ./internal/score/... ./internal/editor/
go test -run 'Extreme|Chain' ./internal/tracker/fx/
go test -run 'Corrupt|RandomFiles|Names|NearlyEqual|Modulators' ./internal/score/io/...
go test -run 'RollRandom|RollMoveRefused|RandomLane|MixOwner' ./internal/score/edit/
go test -run 'MasterLanes|BusLanes|MixLanes|ManyMoving' ./internal/score/play/
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/   # + score_timeline_mix.png
foundry score inspect internal/score/scorecli/testdata/mixer.fscore             # bus and master lanes listed
go test -tags release $(go list -deps -tags release ./cmd/foundry | grep foundry/) ./internal/pack
```

### Decisions (pass)

- **Lanes have owners, one path for all.** A bus's and the master's lanes
  are stored with them (`buses.a.lanes`, `master_lanes`) and named as a
  line's, so editing, interpreting, scheduling and drawing them is the
  line code given an owner number — the same numbers the editor already
  used for the bus and master chains.
- **The master volume lane multiplies `gain`; a bus's return lane
  replaces the return**, as a line's volume lane multiplies its fader and
  its send lanes replace the sends. A live fader or knob moves what the
  lane does not override.
- **MIDI export leaves bus and master lanes out**: a Standard MIDI File has
  no buses or master to carry them.
- **SF2 attenuation at 0.4** is FluidSynth's (and the EMU hardware's)
  reading, which the banks people have are voiced for; the spec's full
  figure plays them far quieter than any player does.

### Deferred (pass)

- **"Open in instrument editor"** on the library dialog (mockup screen 4):
  there is no editor for a library `.finst` — the Tracker's instrument pane
  edits a song's own instruments and only plays library ones (its *Make
  local copy* embeds one). A `.finst` editor is phase 6 work (with the
  instrument-related extras); the dialog shows the file and previews it.
- MIDI recording into armed lines, FLAC in an SFZ and the SFZ/SF2 features
  a `.finst` cannot hold stay deferred as listed above.

