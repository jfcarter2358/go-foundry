# Phase 3 — Expression

SPEC's line for this phase: voices 1–4 complete; dynamics marks + hairpins +
text cresc/dim; dynamics lane with drag-to-override segments; gliss
(styles); slurs; articulations → `.finst` articulations + playback
length/accent; technique text; repeats, voltas, D.S./D.C./coda/fine
playback; tempo text rit./accel./a tempo and tempo marks; grace notes;
ornaments; tremolo; fermata; arpeggio; pedal; ottava; clef changes;
transposing instruments + concert pitch toggle. Every item shipped; the
verification pass at the end lists what it found and fixed.

Step A is the model, the interpreter and the scheduler: every item has a
place in the file, in the voice grammar's mark vocabulary, and in what
plays. The engraving of every item (with its goldens) landed with it, in
commit 46f89dc, together with the editing model (`edit` and
`editor_score_marks.go`). Step B connected the editor to it: keys, the Marks
palette and menu, the expression box, the form dialogs, and the dynamics
lane.

## Step A: the model, the interpreter, the scheduler

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/score.go`, `form.go` | `Voltas` (endings, the passes each plays on), `Jumps` (segno, coda, to_coda, fine, dc, ds, with `al: fine|coda` and `repeats: true` for con rip.), `Concert` (concert-pitch view, saved), `Playback{IgnoreRepeats, Graces auto|before|on}`; `Volta.Label`, `Jump.Text`, `FirstJump`, `JumpAt` |
| `internal/score/tempo.go` | `TempoMark.To`/`HasTo`, text-only marks; `TempoMark.Change` classifies rit., rall., allarg., accel., string., a tempo, tempo primo, meno/più mosso, with poco/molto |
| `internal/score/staff.go` | `Staff.Clefs []ClefChange` and `ClefAt`; `Score.Ottavas`/`OttavaAt`; `Staff.Written`/`Sounding`; `Score.AlterInForce` (the accidental a staff position has at a point, across all voices of the staff, tie-aware); `Score.PosAt` |
| `internal/score/marks.go` | new marks `niente`, `trem2`, `on_beat`, `before_beat`, and values `tr=#`, `turn=b/#`, `trem=1..4`, `ferm=short|long|verylong`, `arp=down`, `gliss=wavy`, `cresc=text` / `cresc="poco a poco"`; `DynamicSteps`, `StepDynamic`, `FermataScale`, `OrnamentAccidentals`, `TremoloStrokes` |
| lines | `Line.Gliss` (slide, chromatic, diatonic; empty = the instrument's), `Line.Overrides []DynOverride` (`dynamic_overrides:` in the file) |
| `fscore.go`, `validate.go` | read/write every new key; validation of endings, jumps, tempo text, clef changes, overrides, unpaired spanner ends (warnings) and mark values |
| `internal/score/interpret/form.go` | the bars in playing order: repeats, endings, D.C./D.S., al Fine/al Coda, To Coda, Fine; after a jump repeats play once with their last ending unless `repeats: true`; `Options.NoRepeats` / `playback.ignore_repeats` play straight |
| `interpret/tempobuild.go` | one tempo map from tempo marks, tempo text (ramps to the next mark or `to:`) and fermatas (a fermata divides the tempo under it) |
| `interpret/dynamics.go` | each line's Written curve (marks, hairpins, text crescendos, al niente) and Effective curve (overrides over every pass) in `Performance.Dynamics`; `WrittenDynamics(s)` in written ticks for the lane; note Velocity and Accent (sfz-type and fp's forte are per-note accents) |
| `interpret/expression.go` | articulation lengths (staccato = the `.finst`'s `Playback.Staccato`), slurs as legato overlap + the "legato" articulation, glissandi (slide as `Note.Bend` merged into one note, or chromatic/diatonic runs in the key), trills/mordents/turns with the key's and the bar's accidentals, tremolo (measured, unmeasured → the instrument's "tremolo" articulation, two-note), arpeggios up/down, sustain pedal with re-pedalling; grace notes before or on the beat; technique words cancel only their own family |
| `interpret.Options` | `Instruments` (the lines' `.finst`s, for what depends on them), `NoSlides` (a slide as its chromatic run: the MIDI export uses it) |
| `internal/score/play` | notes interpreted with the line's instruments; a held note follows its line's level when the instrument sustains (a looping sample); pitch bends cued every 120 frames; no allocation; `tracker.Instrument.Sustains()`; a swap re-interprets only a line whose notes depend on the instrument |
| `internal/score/edit/dynamics.go` | `SetDynamicOverride`, `ClearDynamicOverrides` |
| `foundry score inspect` | endings, jumps, tempo text, gliss style, overrides, clef changes; flags a staff whose transposition disagrees with its instrument's |
| `internal/score/engrave` | (46f89dc) dynamics, hairpins, cresc./dim. text with dashes, slurs, gliss lines straight and wavy, articulations, ornaments with accidentals, tremolo strokes, fermatas, arpeggios, pedal and ottava lines, voltas, D.C./D.S./coda/segno/Fine text, tempo text with dashes, mid-staff clef changes, concert-pitch display; goldens `articulations`, `dynamics`, `form`, `lines`, `slurs`, `slurs_from_bar_6`, `voices4`, `transposing`, `transposing_concert` |

### The file

```yaml
tempo: [{bar: 1, bpm: 96, text: Moderato}, {bar: 8, text: rit., to: "9"}, {bar: 9, text: a tempo}]
repeats: [{start: 3, end: 5}]
voltas: [{start: 5, end: 5, passes: [1]}, {start: 6, end: 6, passes: [2]}]
jumps: [{mark: segno, bar: 2}, {mark: to_coda, bar: 3}, {mark: ds, bar: 10, al: coda}, {mark: coda, bar: 11}]
playback: {graces: auto, ignore_repeats: false}
concert: false
lines:
  - name: Trombone
    gliss: slide
    dynamic_overrides: [{at: "4", to: "5", level: 0.35}]
    staves:
      - clef: bass
        clefs: [{at: "3:3", clef: tenor}]
        voices: ["| qD3(mf,gliss) qA3 hF#3(cresc) | wD4(f,hairpin_end) |"]
```

The voice grammar's syntax is unchanged; everything new is a mark. Pitches
stay sounding pitch: ottava lines, clef changes and staff transposition
change only how notes are written (as MusicXML and Sibelius store them).

## Step B: the editor

### At a glance

| File | What |
| --- | --- |
| `editor_score_window.go` (`scoreRunCmd`), `editor_score_keymap.go` | S slur, H / Shift+H hairpins, Alt+G glissando, Ctrl+E expression box, Ctrl+Alt+T tempo, Ctrl+R rehearsal mark, Ctrl+Alt+P marks palette, Ctrl+Shift+L dynamics lane, Ctrl+Shift+D concert pitch; `.` `'` `Shift+.` `Shift+-` `Shift+'` articulations; keypad 7/8/9/+ by layout (first: natural, sharp, flat, accent; second: staccato, tenuto, staccatissimo, marcato; second-layout 5/6 grace notes); MuseScore's preset has its own articulation keys |
| `editor_score_marks.go` | the editing model: marks on the selection (a note, or bars: articulations on every note, a dynamic on each line's first note, a fermata on each voice's last, slurs per voice, hairpins per line, pedal under the last staff, ottava per staff), spanners, grace entry, moving notes between voices, tempo / rehearsal / repeat / ending / jump / clef-change edits, concert pitch, line gliss style, the lane's drag model |
| `editor_score_palette.go` | the Marks palette (toolbar **Marks**), the Marks menu with Remove, and the modal dialogs: Expression (filed by `edit.ClassifyExpression`), Tempo, Rehearsal, Repeat, Ending (suggests the next pass), Clef change (mid-bar too) |
| `editor_score_lane.go` | the dynamics lane after mockup screen 1: written curve dashed grey, effective solid blue, overrides shaded with a handle and "override 0.42"; drag on empty lane creates an override at the pointer's level over the beats crossed, drag its line to change the level, click + Delete removes it; heard while playing (paced `laneLive` sync); one undo step per drag |
| `editor_score_header.go`, `editor_score_lines.go` | the **D** lane toggle on each staff header; line menu: Dynamics Lane, Clear Dynamics Overrides, Glissandi Play (instrument's / slide / chromatic / diatonic); a swap writes the staff at the new instrument's transposition |
| `editor_score_keypad.go` | the keypad panel shows and lights the new keys |
| `internal/score/edit/form.go` | positions compared with `samePos` (a bar offset's zero can be 0/0 or 0/1) |

## How to verify

```
go test ./internal/score/... ./internal/tracker/... ./internal/editor/ ./internal/world ./internal/script ./internal/app ./internal/
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
    # also writes score_expression.png: two lanes with overrides and the palette
foundry score render internal/score/scorecli/testdata/expression.fscore -png /tmp/expr.png -scale 14
foundry score render internal/score/scorecli/testdata/expression.fscore -o /tmp/expr.wav
foundry score render internal/score/engrave/testdata/lines.fscore -png /tmp/lines.png      # ottava, pedal, clef changes
foundry score render internal/score/engrave/testdata/transposing.fscore -png /tmp/t.png -concert
foundry score inspect internal/score/engrave/testdata/form.fscore   # 12 bars, plays 24
task test-release
```

The demo's golden hash moved from `4a6f89d0e9c4c939` to `54bff7c5ac84c913`
because its last bar's fermatas now hold; with the fermatas taken out it
still renders bit-identical to phase 0 (`TestDemoWithoutFermatasIsUnchanged`).

### Tests

| Package | Tests |
| --- | --- |
| `interpret` | `TestEndingsPlayOnTheirPasses`, `TestJumpsFollowTheNavigation`, `TestIgnoreRepeats`, `TestFormSurvivesTheFile`, `TestTempoText`, `TestFermataHolds`, `TestHairpinsRampTheLevel`, `TestMomentaryDynamicsAndAccents`, `TestOverridesReplaceTheLevel`, `TestFourVoicesAreIndependent`, `TestArticulationLengths`, `TestArticulationAndTechniqueWords`, `TestSlursPlayLegato`, `TestGlissandoStyles`, `TestASlideIntoATremoloKeepsIt`, `TestGraceTiming`, `TestOrnaments`, `TestTremolos`, `TestArpeggios`, `TestPedalSustains`, `TestWritingChangesNoPitch`; `FuzzScoreFile` seeded with the fixture |
| `score` | `TestClefChanges`, `TestOttavaSpans`, `TestWrittenAndSoundingPitch`, `TestAlterInForce`, `TestDynamicSteps`, `TestMarkValues`, `TestValidateExpression`; the random round trip covers every key |
| `edit` | `TestDynamicOverrides`, `TestMoveToVoice`, `TestFormMarks`, `TestFormMarksFindTheirPlaceHoweverZeroIsSpelled`, `TestRetranspose`, the expression classifier |
| `engrave` | `expression_test.go` and the goldens above |
| `play` (offline renders) | `TestAHeldNoteSwellsUnderAHairpin`, `TestAccentsFallBackAndOverridesHold`, `TestASlideGlides`, `TestMarksPickArticulations`, `TestSwappingReperformsWhatDependsOnTheInstrument`, `TestFormAndFermataPlay`, `TestExpressionRendersWithoutAllocating` |
| `scorecli` | `TestDemoWithoutFermatasIsUnchanged`, `TestExpressionFixturePlays`, the re-captured golden |
| `editor` | `TestScoreExpressionKeys`, `TestScoreExpressionBox`, `TestScoreFormTools`, `TestScoreLaneDragModel`, `TestScoreLaneThroughTheView`, `TestScoreLaneHeardWhilePlaying`, `TestScoreGraceEntryAndSwitches`, `TestScoreMarksWindowDraws`, `TestScoreBarsMoveToAVoice`, `TestScoreSwapTakesTheInstrumentsTransposition`, `TestScoreWriteTempoKeepsTempoText`, `TestScoreGLWindowDraws` (expression shot) |

## Decisions

- **Pitches are sounding pitch.** Transposition, ottavas and clefs are how a
  note is written; playback reads the stored pitch. Note entry and the
  engraver go through `toWritten`/`toConcert` and `OttavaAt`.
- **A swap writes the staff at the new instrument's transposition**
  (Sibelius does this): the notes and the sound do not change, the page
  does. A grand-staff or percussion instrument is written as it sounds.
- **Concert pitch is a view switch saved with the score**, not an undo
  step; undo and redo keep it as it is.
- **sfz-type accents and fp's forte are per-note accents**, not spikes in
  the line's curve, so the lane shows the level the marks set.
- **A held note follows its line's dynamics only on a sustaining
  instrument** (a looping sample: strings, winds); a piano or harp note
  keeps the level it started at.
- **Hairpins are spelled `(cresc)` / `(dim)` … `(hairpin_end)`**: `>`
  already means accent. Open-ended, a hairpin rises one dynamic step; with
  no `hairpin_end` it runs to the next dynamic. `cresc=text` prints
  "cresc." with dashes; other words (`cresc="poco a poco"`, or "poco cresc."
  typed into the expression box) print as given.
- **Glissando is `Alt+G`**, not SPEC's `G`: `G` is the note G (a letter in
  note input, a pitch change out of it). Marcato is `Shift+'`, not `^`
  (`Shift+6` is the sixth below).
- **A slide merges its target into one note** (no second attack) unless the
  target re-strikes (an ornament or a tremolo): then the slide bends up to
  it and the target strikes as itself.
- **A D.S. needs its segno and an "al Fine" its Fine first**:
  `edit.SetJump` refuses otherwise and the status says why. After a jump,
  repeats play once with their last ending unless the jump says con rip.
- **Moving bars to a voice leaves a tuplet where it is** (a tuplet moves
  whole; `edit.MoveToVoice` refuses one of its notes), and says how many
  stayed.
- **Instrument swaps re-interpret only what depends on the instrument**
  (staccato length, glissando style, tremolo articulation); otherwise the
  performance is shared, as phase 2's hot swap expects.
- **The MIDI export writes a slide as its chromatic run** (`NoSlides`), so no
  target note is lost; pitch-bend export is phase 5's.

## Verification pass

An adversarial pass over the code (not the reports) against SPEC's phase 3
line: the fixtures engraved and rendered through the CLI and looked at,
the GL shots looked at, interpreter edge cases probed (graces at bar 1,
trills and mordents on ties, glissandi across ties, rests and barlines,
chained glissandi, tremolos on dotted notes, open hairpins, fermatas on
rests, accel. with no target, slurs across a barline). What it found, all
fixed:

| Found | Fix | Test |
| --- | --- | --- |
| **Moving notes to a voice worked for one note only.** `Ctrl+Alt+n` with bars selected just switched the entry voice, so SPEC's "voices 1–4 complete" lacked Sibelius's passage move | `moveBarsToVoice`: every note in the selected bars from the other voices moves at its onset (marks and graces with it), one undo step; tuplets stay and are counted | `editor`: `TestScoreBarsMoveToAVoice` |
| **A swap to a transposing instrument left the staff in C**, so a line swapped to a clarinet or horn showed concert pitch while the score was not in concert view (and `score inspect` flagged it) | `edit.Retranspose` in `setInstrument`, inside the swap's undo step; the layout is rebuilt through the layout counter, no new program | `edit`: `TestRetranspose`; `editor`: `TestScoreSwapTakesTheInstrumentsTransposition` |
| **Undo flipped concert pitch back.** Snapshots hold the whole score, so undoing an edit made before a concert-pitch switch switched the view back | `scoreState.restore` keeps the current `Concert` on undo and redo | `editor`: `TestScoreGraceEntryAndSwitches` (extended) |
| **The opening tempo mark could be removed** when its offset's zero was spelled 0/1: `removeTempo` compared `Pos` with `==` (the bug step B fixed in `edit` with `samePos`); `RemoveTempoMark`'s comment also promised a guard it did not have | compared by bar and `Offset.IsZero()`; the comment says where the guard lives | covered by `TestScoreFormTools` |
| **A slide swallowed a re-striking target.** `hC4(gliss) hG4(trem)` merged the G into the C's note and the tremolo vanished (tremolos and ornaments skip a bent note) | a slide into an ornament or tremolo bends up to it and leaves its attack | `interpret`: `TestASlideIntoATremoloKeepsIt` |
| **"poco cresc." was filed as plain text** by the expression box, so it printed but did not play | `ClassifyExpression` files words around cresc./dim. as a text hairpin with those words; `startName` takes `cresc=<words>` | `edit`: classifier cases; `editor`: `TestScoreExpressionBox` (poco dim.) |
| **A letter typed with no note before it** landed by the staff's opening clef even after a clef change | `staffMiddle` reads `ClefAt` at the entry point | covered by the entry tests |

Checked and found right: endings and D.S./D.C./coda/Fine through the CLI
(`form.fscore` plays 24 bars in the right order), the fermata and tempo-text
tempo maps, hairpin ramps and the lane's two curves, the lane drag through
the real ImGui frame and heard while playing, trills/mordents/turns with
the key's and the bar's accidentals, tremolo stroke values, two-note
tremolos on dotted notes, arpeggio rolls, pedal with re-pedalling, ottava
and 15mb display with sounding pitch kept, mid-bar clef changes, the
transposing and concert goldens, grace notes before and on the beat, the
release build linking only score playback.

### Deferred

Nothing of SPEC's phase 3 line is deferred. What later phases pick up:

- Phase 5: MusicXML import of two-note tremolos, endings and D.S./coda
  (the importer already writes the mark names this phase plays) and their
  export; MIDI export of slides as pitch bend (they are written as their
  chromatic run so no note is lost).
- Phase 5 (page view): an ottava, pedal or hairpin line breaking across
  systems and pages (the continuous view has one system).

Outside this project: `internal/glext`'s `TestEveryReadbackGoesThroughTheWrappers`
fails on GL readbacks in `internal/gfx` and `internal/render` tests from the
lighting work (commit b58584d); no score file is involved.
