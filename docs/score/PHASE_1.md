# Phase 1 — Engraving and entry on one staff

Phase 1 shipped in three steps. Step A is the notation itself: the music
font, the engraver, the image backend and `foundry score render -png`.
Step B is the Score editor window, note entry, selection, undo and keymaps.
Step C is the verification pass: every SPEC item for the phase was tried
through the code (not the reports), and what was wrong was fixed — see
"Step C" at the end, and "Deferred" there for what later phases pick up.

## Summary for the next phase

| What | Where |
| --- | --- |
| Music font, metrics, outlines, atlas | `internal/score/glyph` (+ `glyph/bravura` for the OFL text) |
| Engraver → display list + pointer boxes | `internal/score/engrave` |
| Display list → PNG | `internal/score/engrave/raster`; `foundry score render <score> -png out.png` |
| Display list → imgui | `internal/score/scoredraw` |
| Editing operations, no UI | `internal/score/edit` (`Write`, `WriteOver`, rhythm, pitch, bars, signatures) |
| The editor | `internal/editor/editor_score_*.go` (window, state, view, hit, keymap, input, settings, keypad, help, templates) |
| Formats | `.fscore` unchanged from phase 0; `score.yaml` (editor keymap, recents, keypad; never packed) |

## Step A: glyphs and the engraver

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/glyph` | Bravura, embedded: SMuFL names → code points, metrics, anchors and engraving defaults in staff spaces, outlines (`cff.go`), the editor's glyph atlas (`atlas.go`). |
| `internal/score/glyph/bravura` | The font files, and a tiny package carrying only `OFL.txt` (`bravura.License`) for installers. |
| `internal/score/engrave` | Layout: a score → a display list (`list.go`) of glyphs, lines, filled paths and text in staff spaces, plus the boxes an editor maps the pointer with. |
| `internal/score/engrave/raster` | The display list → an `image.RGBA` (x/image/vector); the snapshot tests. |
| `internal/score/scoredraw` | The display list → an imgui draw list: atlas page uploaded as a GL texture, glyph quads, lines, triangulated paths. For the editor (step B). |
| `internal/score/scorecli/engrave.go` | `foundry score render <score> -png out.png [-bars a-b] [-lines 1,3] [-scale px] [-concert] [-spacing k] [-bar-numbers n]`. |
| `internal/pack/editor.go` | An editor install now carries `licenses/Bravura-OFL.txt`. |
| `docs/guide/32-score-editor.md` | `render -png` and the engraving rules a writer relies on. |

### What shipped

**Glyphs.** `glyph.Bravura()` parses the embedded font once:
`bravura/glyphnames.json` (SMuFL's name → code point table, added to the
font files this phase; Bravura's OTF carries no glyph names) plus the
metadata's optional glyphs; `bravura_metadata.json`'s bounding boxes,
anchors (`stemUpSE`, `stemDownNW`, `stemUpNW`/`stemDownSW` on flags,
`graceNoteSlash*`, `noteheadOrigin`, …), advance widths and
`engravingDefaults`. Every coordinate the package hands out is in staff
spaces and **y-down** (SMuFL's y is up; it is flipped once, here).

`Font.Glyph(name)` loads outlines from the font's CFF table with the
package's own Type 2 charstring interpreter (`cff.go`):
golang.org/x/image/font/sfnt has no `flex`/`flex1` operators, and Bravura
draws 97 glyphs with them — including the 1, 4, 5 and 7 of every time
signature and tuplet number (a 3/4 rendered with no 4 until this was
found). sfnt still maps code points (cmap). `TestCFFMatchesSFNT` checks the
interpreter against sfnt point for point on all 3300+ glyphs sfnt can read;
`TestEveryGlyphLoads` loads every named glyph.

`glyph.Atlas` rasterises glyphs for one zoom onto one RGBA page (white,
coverage in alpha, so a tint is the colour), at any size (grace notes are
0.66), growing the page to 4096 and re-baking on growth (`Rev` /
`UploadedRev`, as `internal/uifont`'s pages). `glyph.Atlases.For(px)` keeps
one page per zoom bucket (`ZoomBucket`: quarter steps of an octave, 2–64 px
a space), so a pinch does not bake a page a frame. `AtlasGlyph.Quad` is the
quad in staff spaces from the glyph's origin, independent of the bucket.

**The engraver.** `engrave.Engrave(score, Options{From, To, Lines,
Concert, Spacing, BarNumbers}) *List`. The list is flat items
(`ItemGlyph` at a point and size, with `ScaleY` for the brace; `ItemLine`
with a width; `ItemPath` filled, nonzero; `ItemText` with a font, size and
alignment), each with a `Role` (notehead, stem, beam, rest, tie, …), a `Ref`
(line, staff, voice, bar, event, note) and `Faint` (an empty bar's implied
rest). Beside them: `Staves []StaffBox` (top, clef, bottom-line diatonic,
written key, transposed; `Diatonic(y)` / `Y(d)` — what a click means),
`Bars []BarBox` (barlines, where music starts after the signatures,
`Columns` onset → x; `XAt` / `OffsetAt`), `Events []EventBox` (each event's
x, ink box and offset), `BarAt`, `Bar`, `StaffAt`, `Bounds`, `Translate`,
`ItemBounds`. `Flatten` and `Triangulate` (path.go) turn paths into
triangles for backends that need them.

What it engraves (with where the rule lives):

- staff lines; clefs (treble, bass, alto, tenor, treble/bass 8va/8vb,
  percussion); key signatures in the traditional patterns per clef (the
  tenor clef's own sharps), changes cancelled with naturals for what the new
  key drops, after a double barline; time signatures incl. additive
  (`3+2+2/8`, with SMuFL's numerator plus), common and cut, at the start and
  wherever the meter changes (`clefkey.go`);
- barlines: single, double before a key change, final at the score's end;
  the system's opening barline; barlines through a grand staff's gaps but
  not between lines; the brace, stretched (`draw.go`);
- noteheads by value (breve by its `noteheadOrigin`); chords with seconds and
  altered unisons in columns either side of the stem — from the stem's end,
  each note takes the first column more than a step from the last note
  there, so a cluster alternates and E F♭ F takes three (`chord.go`);
- accidentals by the bar against the (written) key, shared by the staff's
  voices in time order, grace notes first; a tied-over note neither shows
  one nor sets the bar's state; `!` forces, `?` parenthesises; both notes of
  an altered unison show theirs; stacked top, bottom, second from top… into
  the fewest columns that do not collide by the glyphs' boxes, clear of
  displaced heads; ledger lines kept short of them;
- ledger lines, as wide as the heads that need each one;
- stems: away from the note furthest from the middle line (balanced: the
  majority; the middle line: down), forced up for voices 1 and 3 and down
  for 2 and 4 when two or more voices have music in the bar, `up`/`down`
  marks first; joined at the font's anchors; an octave long, longer for
  32nd-and-shorter flags (by the flags' anchors), reaching the middle line
  from far outside the staff (`beam.go`);
- flags; beams grouped by the meter (`beamUnits`: simple meters by the beat,
  4/4 eighths by the half bar, compound by the dotted beat, additive by its
  groups, 5/8 as 3+2, 7/8 as 2+2+3, 8/8 as 3+3+2; a pickup's beats aligned
  to the bar's end; a tuplet as its own group; rests and quarters break),
  stems all one way per group, the slope a quarter space per step of the
  interval up to a space and at most 0.4 spaces a space, flat for a repeated
  note or a concave shape, the nearest note's stem standard (+¾ space per
  beam past two), ends quantised to sit on, hang from or straddle a line;
  secondary beams over runs, partial beams toward the note they belong with;
  a beamed acciaccatura group's slash;
- dots in spaces (a line note's in the space above, below for a lower voice;
  no two notes of a chord share one), every voice's dots in one column after
  all the heads at that onset, a rest's dot clear of them;
- rests by value, centred on a notehead; in multi-voice music moved up or
  down (two spaces; whole and half rests one, staying on lines) and further
  while they hit another voice's notes (`rest.go`); a bar rest or a bar
  written as one whole rest centred in the bar (a breve rest from 4/2 up);
- voices side by side: a voice a second from or crossing an earlier one at
  the same onset moves right; a unison of the same value shares its head;
- ties: away from the stem, by the voice in multi-voice music, the chord's
  upper half up and lower half down; outer ties from over/under the head,
  inner ones (and any on the stem side) between the heads; after the dots;
  across barlines; into the layout's first bar and out past its last, to the
  barline (`tie.go`);
- tuplets: the number (or `5:3` when not the usual ratio) by the beam when
  the tuplet is exactly one beam, else a bracket with hooks clear of every
  note and stem, outside the staff; nested ones inside out (`tuplet.go`);
- grace notes at two thirds size, left of their note's accidentals (or before
  the barline at a bar's end), with their own accidentals and ledger lines,
  stems up (down in a lower voice), flagged and slashed (acciaccatura) or
  beamed; two voices' graces at one onset one after the other;
- spacing (`spacing.go`): columns are the onsets of every staff, so staves
  align; the room after a column is the larger of what its duration asks
  (an eighth 2.7 spaces, each doubling 65% of that more, shorter values by
  a 0.45 power) and the ink rod (the column's right ink + the next's left
  ink + 0.45); the bar's lead after a barline or signatures, the last note's
  gap to the barline, a minimum for a bar rest;
- staves stacked by their ink (7 spaces between lines, 6 inside a grand
  staff, more where ink needs it); bar numbers above the top staff;
  transposing staves at written pitch and key (`Options.Concert` for
  sounding).

**Designed for page layout (phase 5).** `measure` fixes each bar's columns,
ink extents, rods and springs (`barCtx.widths`, `lead`, `prefixW`) without
placing anything; `placeGalley` puts bars end to end; only then does `draw`
make items, because beams and ties need final x. A page layout replaces
`placeGalley` with a line breaker that assigns bars to systems and stretches
each system's widths to the measure (the ideal part of each width is the
spring), and calls the drawing once per system with that system's bars:
`draw` needs to take a bar range and a y offset rather than every bar, and
the start-of-system prefix (`startPositions`) to show the clef and key on
every system (it already shows them at the layout's first bar). Spacing is
relative to an eighth, the same in every bar, which a galley wants; a page
can switch to each system's shortest value (`ideal` is the one place).

**Backends.** `raster.Render(list, Options{Scale, Ink, Paper, Colour,
Clip})` fills every item as its own vector path (so overlapping items never
cancel), text in the Go fonts (x/image's gofont; the display list asks for
a face, not a file). `scoredraw.Renderer.Draw(canvas, list, View{Origin,
Scale, Clip, Ink, Colour, Faint})` draws through imgui (`imgui.DrawList` is
a `Canvas`): it bakes every visible glyph first, uploads the page if its
revision moved, then emits quads, so UVs never change mid-frame; lines are
at least a pixel; paths are flattened to a quarter pixel and triangulated.
It needs the GL context current; `Upload`/`Delete`/`FontSize` are swappable
for tests.

**CLI.** `foundry score render <score> -png out.png` with `-bars 5-8`,
`-lines 1,5`, `-scale 12`, `-concert`, `-spacing`, `-bar-numbers`; prints
size, bars, staves, items and a pixel hash.

**Licence.** `pack.StageEditor` writes `licenses/Bravura-OFL.txt` (from
`bravura.License`, which links only the text) into every editor install.
The release game runtime links none of `internal/score` yet, so a game
release carries no font and needs no notice; when phase 2 puts score
playback in the runtime it still links no glyph or engrave code (only the
model, interpreter and scheduler).

### Tests

| Test | Checks |
| --- | --- |
| `glyph`: `TestEngraverGlyphsLoad`, `TestEveryGlyphLoads`, `TestCFFMatchesSFNT`, `TestMetricsAreYDown`, `TestAtlasBakes`, `TestZoomBuckets`, `TestLicenseEmbedded` | the font, the interpreter against sfnt, y-down metrics/anchors/defaults, atlas coverage, growth and caching |
| `engrave`: `TestStemDirections`, `TestStemsReachTheMiddleLine`, `TestSecondsGoEitherSideOfTheStem`, `TestAccidentalsByTheBar`, `TestAccidentalsStackWithoutColliding`, `TestKeySignaturePositions` (every accidental of every key on four clefs is a pitch the key alters), `TestTimeSignatures`, `TestBeamingFollowsTheMeter`, `TestBeamGeometry`, `TestPartialBeamsPointToTheirNote`, `TestDotsSitInSpaces`, `TestRestsMoveForVoices`, `TestBarRestsAreCentred`, `TestStavesShareOnsets`, `TestBarlines`, `TestTransposingStaves`, `TestTies`, `TestTuplets`, `TestGraceNotes`, `TestBoxesMapPointerToMusic`, `TestTriangulateTiesAndBeams` | each rule above, against the geometry |
| `engrave`: `TestEngraveEveryScore` | every fixture and shipped score, whole and bar by bar, concert and written: finite, every glyph has a code point |
| `engrave`: `TestNoCollisions` | 150 random scores (random meters, keys, clefs, chords with accidentals and cautionaries, dots, rests, triplets, graces, one or two voices): no notehead, accidental or dot touches another event's, nor an accidental its own chord. `SCORE_ENGRAVE_LONG=1` runs 5000 (clean). It found and drove the fixes for: unison+second chords, two voices' grace runs at one onset, a dotted rest's dot on another voice's head or dot, dots beside a shifted voice. |
| `engrave/raster`: `TestGolden` | 17 snapshots (accidentals, chords, beams, meters, keys, clefs, rests, voices, dots, ties, ties from bar 2, tuplets, graces, grand staff, transposing written and concert, demo bars 9–12) at 8 px a space against `testdata/golden/*.png`, at most 0.02% of pixels off by a quarter of the grey range; `TestGoldenCatchesAChange` proves one lost natural fails it |
| `scoredraw`: `TestDrawEmitsEveryItem`, `TestDrawPlacesGlyphQuads`, `TestDrawClipsAndColours`; `TestGLAtlasUpload` (opt-in GL: the page on the GPU, read back) | the imgui backend |
| `scorecli`: `TestRenderPNG` | the verb and its flag errors |
| `pack`: `TestAnEditorInstallIsTheEngineOnly` | the OFL in an editor install |

`BenchmarkEngraveDemo`: the 16-bar, six-staff demo engraves in about
1.2 ms.

### How to verify

```
go test ./internal/score/glyph/... ./internal/score/engrave/... ./internal/score/scoredraw ./internal/score/scorecli ./internal/pack
SCORE_ENGRAVE_LONG=1 go test ./internal/score/engrave -run NoCollisions
FOUNDRY_GL_TEST=1 go test -run GL ./internal/score/scoredraw
foundry score render score_demo -png /tmp/demo.png -scale 10
foundry score render internal/score/engrave/testdata/accidentals.fscore -png /tmp/acc.png -scale 24
SCORE_ENGRAVE_GOLDEN=update go test ./internal/score/engrave/raster -run Golden   # after an intended change; then look at every image
```

Every fixture in `internal/score/engrave/testdata` and the demo were
rendered at 14–50 px a space and checked by eye against standard engraving
(Behind Bars; Bravura's anchors), as were imported MusicXML fixtures (the
Sibelius piano: nested triplets, a grace, voices, a grand staff; the
transposing set). What looking found, beyond the collision test's finds:
the missing flex glyphs; x/image/vector's Alpha fast path ignoring a
sub-image's stride (the atlas baked two rows of each glyph); the raster
backend indexing past the image for a clipped item; additive numerators
using the whole-signature plus; time-signature numerals set by their
advances touching; per-bar spacing making a bar with one quintuplet twice
as wide as its neighbours (spacing is now relative to an eighth
everywhere); grace notes crowding their note; a full-bar `wr` not centred;
half/whole rests in multi-voice moved off the staff; an outer tuplet's
number on its inner bracket.

### Decisions

- **Own CFF interpreter** rather than a newer x/image (none supports flex,
  and the newer ones need Go 1.25).
- **SMuFL's `glyphnames.json` is embedded** (300 KB) beside the font: the
  OTF has no glyph names, and the table is SMuFL's own.
- **Spacing is relative to an eighth**, not each bar's shortest value: in a
  galley the same value is the same width everywhere. Ink rods keep short
  values apart.
- **Concert pitch in the file, written pitch on the page** by default
  (`Options.Concert` for sounding), the key moved with the staff.
- **An empty bar shows a faint bar rest** (`Item.Faint`): the editor can
  tint it; it is not in the score.
- **The brace** is Bravura's `brace` at up to 2.4× width, stretched
  vertically to the grand staff.

### Deferred

Nothing in step A's brief is left out. These belong to later phases by
SPEC.md and are not drawn yet (their marks are kept in the model): dynamics,
hairpins, articulations, fermatas, ornaments, slurs, glissandi, technique
and expression text, tempo text and rehearsal marks above the system,
repeats and voltas, clef changes inside a staff, ottava and pedal lines
(phase 3); lyrics, chord symbols, percussion staves with drum maps (an
unpitched hit is drawn at its GM key's pitch, far below the staff, until
then), guitar tab (phase 6); systems, pages, multi-bar rests (phase 5).
Cross-staff beaming, secondary-beam breaks inside compound beats, and
shortening outward stems in multi-voice music are refinements no phase
names; they are not done.

## Step B: the Score editor window and note entry

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/edit` | The editing operations, no UI: `Write` (an event into a bar at an offset, reflowing it), `SetDuration`, `ToggleDots`, `MakeRest`, `Delete`, `ToggleTie`, `MakeTuplet` (nests), `FlipStem`, `FitMeasure`; chords and pitches (`AddPitch`, `AddInterval`/`IntervalFrom` spelled in the key, `Transform`, `Accidental`, `Semitone`, `Step`, `Octave`, `Respell`/`Enharmonics`, `NearestLetter`); bars (`CopyBars`/`PasteBars`/`ClearBars` over a `Clip`, `SetKey`, `SetMeter`, `SetClef`). |
| `editor_score_window.go` | The uiwin window: lifecycle, the score scheduler as the editor's voice (`play.Player` behind a keep-alive, synced by clone on change), file ops (templates, open, open recent, save, save as, Save All, save chip), menus, toolbar row, key/time/clef dialogs, `scoreRunCmd`. |
| `editor_score_edit.go` | `scoreState`: selection (event or bars over lines), note input, caret, value, voice, keypad layout, armed accidental, clipboard, snapshot undo (`change` rolls a refused edit back), auditions, status text. Written/concert pitch for transposing staves. |
| `editor_score_view.go` | The paper view (engraving cached per edit, drawn through scoredraw), line-name column, bar ruler, bar selection, caret, playhead, ghost note + tooltip, click/Alt+click/zoom, reveal-selection scrolling, status bar, glyph buttons. |
| `editor_score_hit.go` | Pointer → place to write (snapped to the voice's onsets and a value grid, never inside a tuplet), note/rest hit, bar hit, stem direction, boxes. |
| `editor_score_keymap.go`, `_input.go`, `_settings.go` | Commands, chords (scancode + Ctrl/Alt/Shift, text form), the Sibelius and MuseScore presets, rebinding with steal, overrides saved to `score.yaml`; the SDL key handler with repeat rules and key capture. |
| `editor_score_keypad.go`, `_help.go` | The floating keypad (mockup screen 7, Notes/More tabs, lit cells); the F1 keymap overlay and Edit > Keyboard Shortcuts. |
| `editor_score_templates.go` | Solo line, Piano, String quartet, Chamber ensemble (transposing clarinet and horn). |
| `uiwin`: `UIWindow.HelpOverlay` | A window can draw its own F1 overlay (the Score window's keymap) instead of the shared reference. |
| `scoredraw` | Horizontal and vertical lines are snapped to whole pixels (`pixelRect`), through a new `Canvas.AddRectFilled`. |
| `engrave.WrittenFifths` | Exported: the key a transposed staff shows, for spelling clicks. |
| `pack.developmentFiles` | `score.yaml` never ships. |

The switcher strip, the Window menu (it lists the strip), `openEditors` (Save
All) and the F1 table (`app/keybindings.go`) know the Score editor.

### Behaviour, and where it follows the mockup

- **Writing into a bar** (`edit.Write`) is the mockup's `writeAt` in exact
  time: the event lands at the snapped offset, the event it lands in keeps
  the longest value that fits before it (rests after that), what it covers
  goes, the event it ends in becomes rests, and an event longer than the
  room left is cut to the longest value (up to two dots) that fits. Gaps in
  the bar are split by the meter through `notate.Fill` (the importer's rest
  writer), so beats stay visible. Written inside a tuplet the event joins
  it; a region that cuts part of a tuplet takes the whole tuplet out
  (half a tuplet is not music). An empty bar or a bar rest is a bar of rests
  to write into. `TestRandomEditsKeepBarsFull` runs 300 rounds of 40 random
  writes, value changes, dots, tuplets, deletes and intervals in seven
  meters: every bar stays exactly full, the score validates, and every voice
  round-trips through the grammar.
- **Spelling**: a click writes `score.InKey` of the staff position in the
  written key at that bar; intervals and Shift+letter are diatonic in the
  key; Up/Down spell by direction and key (`SpellMIDI`); every pitch command
  works at written pitch on a transposing staff (a natural on a B♭
  clarinet's written C leaves it), stored at concert.
- **Keys** follow SPEC's table for Sibelius. Choices where the table is
  silent: the time-signature dialog is `Ctrl+Shift+T` (`T` is the laptop
  tie), `K` key, `Q` clef, `X`/keypad `-` flip, keypad `*` next voice,
  `Left`/`Right` previous/next note, `Space`/`P` play (stop).
- **Undo** snapshots the whole score per edit (`boundedStack`, the editor's
  depth); a refused edit is rolled back and leaves no step.

### Tests

| Test | Checks |
| --- | --- |
| `score/edit`: `TestWriteIntoAnEmptyBar`, `TestWriteGrowsTheScoreAndVoices`, `TestWriteReflowsTheBar` (14 cases incl. tuplets, chords, bar rests, 6/8), `TestWriteRefusesWhatIsNotTime`, `TestSetDurationAndDots`, `TestRestsTiesAndDelete`, `TestTuplets` (nested), `TestFitMeasureAndMeterChanges`, `TestKeysAndClefs`, `TestPitchOperations` (33 cases), `TestChordEdits`, `TestCopyPasteBars`, `TestRandomEditsKeepBarsFull` | every operation |
| `editor`: `TestScoreClickWritesTheKeysPitch`, `TestScoreLetterEntry`, `TestScoreIntervalsAndPitchMoves`, `TestScoreDurationsAndRhythm`, `TestScoreTupletsDeleteAndFlip`, `TestScoreBarSelection`, `TestScoreCopyPasteRepeat`, `TestScoreUndoRedo`, `TestScoreSignaturesAndClefs`, `TestScoreTransposingStaff` | every command through `scoreState` |
| `editor`: `TestScoreCommandsAreDescribed`, `TestScoreSibeliusKeymap`, `TestScoreMuseScoreKeymap` (no key bound twice in either), `TestScoreChordText`, `TestScoreRebinding` (steal, overrides round trip, capture rules), `TestScoreDispatch` (repeat rules) | the keymaps |
| `editor`: `TestScoreHitNotesAndRests`, `TestScorePointerTarget`, `TestScoreHitBarsAndBoxes` | hit testing against a real engraving |
| `editor`: `TestScoreWindowDraws` (every pane, every dialog, the F1 overlay, ImGui stacks even), `TestScoreClickThroughTheView` (a click in note input writes F♯5 through the real pane; a click selects a note; a click selects a bar) | the window, headless |
| `editor`: `TestScoreGLWindowDraws` (opt-in GL: the demo on paper, read back; `SCORE_SHOT=x.png` writes the frame) | the window, rasterised |
| `scoredraw`: `TestDrawSnapsStraightLines` | pixel-snapped lines |

### How to verify

```
go test ./internal/score/edit ./internal/score/scoredraw ./internal/editor -run 'Score|Draw|Write|Pitch|Tuplet|Fit|Keys|Chord|Copy|Random|Rests|SetDuration'
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
```

The rendered window was looked at (`SCORE_SHOT`) and fixed: thin lines drawn
as antialiased 1-pixel lines read as faint grey (now snapped rectangles);
toolbar glyphs too small; the keypad's labels overflowing and its panel
see-through. The first screenshots also showed rainbow fringes on every
antialiased edge — the test's own fault (GL's straight alpha read into a
premultiplied `image.RGBA`), fixed in the test, not the renderer.

### Decisions

- **The music operations are a package** (`internal/score/edit`), not editor
  code, so MIDI step entry (phase 6) and the Claude panel can reuse them.
- **A meter change fits bars** (cut at the new barline, short bars padded)
  rather than re-barring the music across the new barlines; the status line
  says when music was cut and undo restores it.
- **The keymap settings are the Score editor's own dialog.** The editor has
  no shared shortcut-settings UI to extend: the existing rebinding UI is the
  game's Controls screen for `platform.Action`s (gameplay keys, `bind_key`),
  which editor commands are not. The dialog follows its model (capture the
  next key, a key in use moves) and stores to `score.yaml`.
- **F1 draws the active keymap** through the new `UIWindow.HelpOverlay`,
  because the Score window's keys depend on the preset and rebindings.
- **Plain-text accidentals in the UI** ("F#5", "Bb major"): the editor's
  ImGui font has no ♯/♭ glyphs; the music itself is Bravura.

### Deferred

- **Clef changes inside a staff**: the dialog changes a staff's clef; a clef
  change at a bar needs a model change and its engraving, which SPEC puts in
  phase 3 ("clef changes"), where step A already left them.
- **Keypad `+` (accent) and the articulation/jazz keypad tabs**: articulations
  are phase 3 (marks are not engraved yet); the cell is blank.
- **Re-barring on a meter change** (above).
- **Transport UI** (ruler seek, loop, tempo slider, follow) is phase 2; this
  step plays and stops from the selection and draws the playhead.

## Step C: verification

Everything SPEC lists for phase 1 was exercised: every fixture and the demo
rendered with `foundry score render -png` and looked at (plus a new set of
tricky beaming/accidental/meter cases), the window drawn through a real GL
context (`SCORE_SHOT`) with the F1 overlay and the shortcuts dialog, and a
Sibelius user's sessions typed key by key through `scoreState`. The
engraving held up; entry did not, in four places, and the F1 overlay was
broken.

### What was wrong, and is fixed

- **A value chosen in note input changed the note just typed.** `4 C 3 D E 5
  F` wrote `eC5 eD5 hE5 qF#5`: the keypad and the dot rewrote the selected
  (just-entered) note, so letter entry — choose the value, then the letter,
  as Sibelius and MuseScore both do — could not be used. In note input a
  value and the dot are now the next note's only; out of note input they
  change the selected note as before. The toolbar and the keypad light the
  value the next note takes while in note input (`scoreState.shownDur`).
- **A note longer than the bar had left was cut short.** A half typed on
  beat 4 became a quarter, silently. New `edit.WriteOver` (used by every
  note-input write: clicks, letters, rests) carries the value over the
  barline as tied notes — each bar's part the longest values that fit
  (5/8 left is h~e), through as many bars as it needs, the note's own tie
  kept on its last part, rests split without ties; inside a tuplet, or into a
  bar that starts with one, it is cut as before. The last part is selected,
  so the next note follows it, and the whole write is one undo step.
- **Changing voice lost the place.** With a note selected, `Ctrl+Alt+2`
  selected whatever voice 2 had at the same *index* (an unrelated beat), or
  fell back to the whole bar, so the second voice was typed from the bar's
  start. A new selection kind, the caret (`scoreSelCaret`: a line, staff,
  voice, bar and offset with no event), keeps the place: in note input the
  new voice starts where the next note was going (under the old voice's
  last note's end); out of note input the new voice's event at the same
  onset is selected, or a caret there. Left/Right from a caret select the
  events either side; paste, play-from, the clef/key/time dialogs, the
  header's line marker and Shift+click all understand it; a caret a meter
  change leaves past its bar's end is dropped (`dropStaleSel`, which undo and
  redo use too).
- **Typing into selected bars always went to a line's top staff.** A
  piano's bass staff could only be entered by clicking notes. A bar click now
  remembers the staff it landed on (`clickBarStaff`, `scoreHitBarStaff`), and
  note input into the selection starts there; Ctrl+click over every line
  starts on the first line's top staff.
- **Intervals and pitch moves needed a single note.** SPEC 8 says "the
  selected note(s)". With bars selected, `2`–`9` (`Shift` below) now add the
  interval to every chord in them — every staff and voice, spelled in each
  bar's written key, a chord that already has the letter or would go out of
  range left alone — and `Up`/`Down`, `Alt+`, `Ctrl+` move every note, as
  one undo step; a selection with nothing to change is refused and leaves no
  step.
- **The F1 overlay was unreadable.** It used legacy ImGui columns, where each
  group's `Separator` resets every column's line start, so the second
  column started half way down the first and the third never appeared; the
  description offset added the column offset twice, pushing the second
  column's descriptions out of sight; long labels were clipped. It is now
  three groups side by side, balanced by rows (a long group carries on at
  the next column's top under its heading), keys sized to the widest in each
  column, labels wrapped. The keypad slots' labels say what they are on both
  layouts ("32nd (second layout: 64th)") instead of "Keypad 1 (by the keypad
  layout)".

- **The GL atlas test read the texture back with a raw `gl.GetTexImage`**,
  which `glext`'s `TestEveryReadbackGoesThroughTheWrappers` forbids (a new
  failure in the whole-suite run); it uses `glext.GetTexImage` now.

### Tests added

| Test | Checks |
| --- | --- |
| `score/edit`: `TestWriteOverTiesAcrossTheBarline` (9 cases + a kept tie) | ties over one and several barlines, h~e splits, over the next bar's music, rests without ties, the score growing, tuplets stopping it |
| `score/edit`: `TestRandomEditsKeepBarsFull` | now writes through `WriteOver` half the time, and engraves every round's result |
| `editor`: `TestScoreTypingASession` | `4 C 3 D E 5 F`, a half tied over the barline, `4 . D`, undo/redo of the tied write as one step |
| `editor`: `TestScoreSecondVoiceStartsUnderTheFirst` | the caret on a voice change in and out of note input, Right from a caret, a stale caret dropped by a meter change |
| `editor`: `TestScoreTypingIntoATuplet` | `3 C Ctrl+3 D E F` fills the triplet and goes on after it |
| `editor`: `TestScoreTypingOnTheLowerStaff` | a bar click on a piano's bass staff types there |
| `editor`: `TestScorePassageRepeatAndPaste` | R after the last bars grows the score, Ctrl+click copy and Alt+click paste over every line, one undo |
| `editor`: `TestScorePassageIntervalsAndMoves` | thirds over two bars spelled in G, octave and semitone moves, a refused passage edit leaves no undo step |

`TestScoreDurationsAndRhythm` and `TestScoreDispatch` were changed to the
note-input value rule above.

### How to verify (the whole phase)

```
go test ./internal/score/... ./internal/editor/ ./internal/pack ./internal/uiwin ./internal/app
SCORE_ENGRAVE_LONG=1 go test ./internal/score/engrave -run NoCollisions
FOUNDRY_GL_TEST=1 go test -run GL ./internal/score/scoredraw ./internal/editor
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
foundry score render score_demo -png /tmp/demo.png -scale 10
task test-release        # the release build links no score, glyph or editor code
```

### Decisions

- **In note input a value is the next note's** (Sibelius, MuseScore), not
  the selected note's as the browser prototype does it: the prototype has
  no letter entry, and with letters the prototype's rule makes `4 C 3 D`
  impossible. Out of note input the prototype's rule stands.
- **Over the barline means tied** for note entry (`WriteOver`); `edit.Write`
  keeps cutting at the bar's end for callers that want exactly one event
  (pastes, value changes, the MIDI step entry of phase 6 can choose).
- **A voice change out of note input selects, it does not move.** Sibelius
  moves a selected note into the other voice; that needs "voices 1–4
  complete" (phase 3), so here the voice change only moves the selection.

### Deferred

Later phases must pick these up (each is named in SPEC for that phase or is
a refinement no phase names; none is silently dropped):

- Phase 3: clef changes inside a staff (model + engraving; the dialog sets a
  whole staff's clef); dynamics, hairpins, articulations (and the keypad's
  `+` accent cell and its Artic./Jazz tabs), fermatas, ornaments, slurs,
  glissandi, technique/expression/tempo text, rehearsal marks, repeats and
  voltas, ottava and pedal lines — kept in the model, not drawn yet;
  grace-note *entry* (they are engraved and survive edits, but no command
  writes one); moving a selected note into another voice (Sibelius's
  `Ctrl+Alt+n` on a selection).
- Phase 2: the transport (ruler seek, loop `L`, tempo slider, follow
  playback); this phase plays and stops from the selection and draws the
  playhead.
- Phase 5: systems and pages, multi-bar rests.
- Phase 6: percussion staves and drum maps (an unpitched hit is drawn at its
  GM key's pitch until then), lyrics, chord symbols, guitar tab, the command
  palette.
- A time-signature change fits bars (cut at the new barline, short bars
  padded, undoable) instead of re-barring the music across the new
  barlines.
- Text items (bar numbers) in the imgui backend are drawn at ImGui's font
  size; imgui-go v4's text call takes no size.
- Refinements no phase names: cross-staff beaming, secondary-beam breaks
  inside compound beats, shortening outward stems in multi-voice music.
