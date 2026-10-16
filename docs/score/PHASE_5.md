# Phase 5 — Pages and output

SPEC's line for this phase: page view and layout (systems, system/page
breaks, spacing, title page, staff size, margins); score and parts (part
extraction, multi-bar rests in parts); PDF writer (own, vector glyph paths,
no font embedding); full MusicXML export and fuller import (marks,
dynamics, articulations, repeats, lyrics, layout hints); OGG, MP3 (LAME
runtime-loaded, editor only), FLAC; stems; `.ftrk` export (quantised) and
import (patterns → notation); PNG/SVG of a selection; export and import
dialogs as in the mockups.

Step A is the printed page: the page layout and the page view, parts, the
PDF writer, PNG and SVG of a selection, and `foundry score export-pdf`.
Step B (below) is the rest: MusicXML out and fuller in, WAV, FLAC, OGG and
MP3 with stems, `.ftrk` both ways, and the export and import dialogs of
mockup screens 6 and 5. A verification pass closed the phase (*Verification
pass*, at the end): what it found and fixed, and the phase's final
*Deferred* list.

## What shipped

| Spec item | Where | Command / UI |
| --- | --- | --- |
| Page view and layout: systems, system and page breaks, spacing (system gap, note spacing), title page, staff size, margins | `score/layout.go`, `engrave/page.go`, `editor_score_page.go`, `editor_score_layout.go` | **Pages** (`Ctrl+Alt+V`), **Layout** menu, Page Setup (`Ctrl+D`), System Break (`Alt+Return`), Page Break (`Ctrl+Return`); `render -page n` |
| Score and parts, part extraction, multi-bar rests in parts | `engrave.ScoreBook`/`PartBook`, `engrave/multirest.go`, `score.PartLayout` | the page view's Full score / part picker; `export-pdf -part`, `render -page n -part line` |
| PDF writer (own, vector glyph paths, no font embedding) | `score/io/pdf` | `export-pdf`, Export dialog |
| Full MusicXML export, fuller import (marks, dynamics, articulations, repeats, layout hints) | `score/io/musicxml` | `export-musicxml`, `import`, Export / Import dialogs |
| OGG, MP3 (LAME, runtime-loaded, editor only), FLAC, WAV; stems | `score/io/audioenc`, `score/io/exportset` | `export-audio`, `export`, `codecs`, Export dialog |
| `.ftrk` export (quantised) and import (patterns → notation) | `score/io/ftrk` | `export-ftrk`, `import song.ftrk`, Export / Import dialogs |
| PNG/SVG of a selection | `editor_score_layout.go` (`exportImage`), `score/io/svg`, `engrave/raster` | File > Export Selection as Image; `render -png/-svg -bars -lines` |
| Export and import dialogs as in the mockups (screens 6 and 5) | `editor_score_export.go`, `editor_score_import.go` | File > Export (`Ctrl+Shift+X`), File > Import (`Ctrl+Shift+I`) |

Lyrics in MusicXML are the one item of this phase's line not done: the
score has no lyrics yet (phase 6 adds them); see *Deferred*.

## Step A: page layout, parts and PDF

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/layout.go` (new) | `Layout` (paper — named or custom, landscape, margins —, staff size, system gap, `Breaks`, title page, page numbers, `Names`, cautionary, courtesy, `Parts`), `PartLayout` (a line's own paper, staff, gap, breaks, multi-bar rests), `Paper.PaperSize`/`MarginsOrDefault`, `ForPart` (the score's layout with the part's over it; the score's breaks are not the part's), `SetBreak`/`SetBreakIn`/`BreakIn`, `EnsurePart`, `Line.ShortName` and `AbbreviateName` (orchestral abbreviations: Violin I → Vln. I, Clarinet in B♭ → Cl. in B♭), the file block, validation (breaks past the end, parts naming no line) |
| `score.go`, `fscore.go`, `validate.go` | `Score.Subtitle`, `Score.Layout`, `Line.Short`; `subtitle:`, `layout:` and a line's `short:` read and written; `Clone` copies the layout |
| `internal/score/engrave` | `Options.Width` (justify: one stretch factor over every gap's spring, never below its rod, found by bisection), `Options.Cautionary` (the next bar's clef before the last barline, a double barline before a key change, the key and time after it: `tailAfter`), `Options.Courtesy` (courtesy accidentals from the bar before, the layout's first bar reading the score's), `Options.MultiRests` (`multirest.go`); `List.Origin`/`List.End` (where the system starts and ends); bar gaps keep `rods` and `springs` (`stretch`); `startPositionsAt` (a system's opening signatures at any bar); `nextChanges`; new roles `RoleName`, `RoleTitle`, `RolePageNumber` |
| `engrave/page.go` (new) | `Paginate(s, PageOptions{Layout, Lines, Part, Concert, MultiRests, Spacing}) *Book`; `ScoreBook`, `PartBook`; `Book{Space, Width, Height, Pages}`, `Page{Number, TitlePage, Items, Systems}`, `System{From, To, At, List}`; `Book.List(page)` (a page as one display list), `Book.Find(bar)`, `Page.SystemAt(point)` |
| `engrave/multirest.go` (new) | Runs of rest bars folded into their first bar: the H-bar and its count; the bars after it zero width with no barlines (`barNone`), every bar keeping its box |
| `engrave/textfont.go` | `TextGlyphs` (a run as outlines, advanced and kerned as `TextWidthFont` measures it), `TextStart` |
| `internal/score/io/pdf` (new) | `Write`/`WriteBook`/`WriteFile`: PDF 1.4, standard library only, deflated streams, every music glyph and every letter one form XObject placed by a matrix, lines stroked with butt caps, paths filled nonzero, no fonts; the Info dictionary (title, author, subject, keywords, creator, producer, dates; UTF-16 when not ASCII); a stable file for the same pages. `Parse` (read.go): xref table, trailer, every object checked against its xref offset, streams by `Length`, the page tree. `Rasterize` (rasterize.go): a content-stream interpreter for the operators the writer uses, refusing any other. `Export` (export.go): `<base>.pdf` and `<base>_<line>.pdf`, paper and staff overrides, the score at concert pitch |
| `internal/score/io/svg` (new) | `Write`/`WriteFile`: a display list as SVG — glyphs and letters as `<path>`s in `<defs>` placed by `<use>`, lines, filled paths, a clip, a background, a title |
| `internal/score/scorecli` | `foundry score export-pdf <score> [-o dir|file.pdf] [-score=false] [-parts=false] [-part 1,Viola] [-concert] [-paper a4] [-staff mm] [-png dir] [-dpi n]`; `render -svg out.svg`; `render -png/-svg -page n [-part line]`; `inspect` prints the layout (paper, staff, pages, systems, breaks, parts) |
| `internal/editor/editor_score_layout.go` (new) | The model with no ImGui: the page view's book (cached by score, edit, layout, mixer and lane revisions, part and concert pitch), `layoutChange` (one undo step per gesture, `LayoutRev`, nothing sent to the player), `toggleBreak` (after the selected bar; the part's own while a part is shown), page setup setters, titles and short names, `scoreExportPDFs`, `exportImage` |
| `internal/editor/editor_score_page.go` (new) | The page view pane (pages on a desk, each system drawn and hit-tested through the continuous view's functions with its own geometry: clicks, note input, ghost, selection, caret, play head, follow, Ctrl+wheel zoom), the strip (full score or a part, page count, Page Setup, Export PDF, zoom), the Page Setup, Export PDF and Export Selection as Image dialogs |
| `editor_score_window.go`, `_keymap.go`, `_ruler.go`, `_edit.go` | **Pages** on the transport row, View > Page View, the **Layout** menu (Page View, Show full score / part, Page Setup, System Break, Page Break, Export PDF), File > Export PDF / Export Selection as Image; commands `page_view` (`Ctrl+Alt+V`), `page_setup` (`Ctrl+D`), `system_break` (Sibelius `Alt+Return`, and `Return` with bars selected; MuseScore `Return`), `page_break` (`Ctrl+Return`), `export_pdf` (`Ctrl+Shift+E`), `export_image`, in a new **Layout** group of the F1 overlay |
| `docs/guide/32-score-editor.md` | The `layout:` block, `subtitle:` and `short:` in the example file and its table; *Pages, parts and printing*; `export-pdf`, `render -svg`, `-page`, `-part` |

### How a page is made

- **Measuring once.** `Paginate` runs the engraver's collect and measure
  passes over the whole score (with the layout's multi-bar rests and its
  breaks as places a rest may not run through) and reads off, per bar, the
  fixed room it needs as a system's first bar (clef, key, time, lead) and
  as a later bar (barline, signature changes, lead), what a system ending
  with it closes with (barline and cautionaries), and every gap's rod and
  spring.
- **Breaking.** The breaks are chosen together (dynamic programming over
  break points, Knuth–Plass style): a system's badness is how far its
  natural width is from the measure — stretching `(A/W−1)²`, squeezing
  three times the square of how far toward its rods it must go, impossible
  when its rods alone are too wide —, plus a small cost per system so
  fewer, fuller systems win; the last system costs nothing when it is
  short. Writer's breaks are kept, and no system starts inside a
  multi-bar rest.
- **Engraving each system** on its own (`Engrave` with `From`/`To`) is
  what makes everything already engraved across a layout's edges work
  across systems: ties into and out of a system, slurs, hairpins, text
  crescendos, ottava and pedal lines continued (phase 3 deferred this to
  here), the clef and key at every system's start, the first bar's number.
  Each is justified to the measure (`Options.Width`); the last stays at its
  natural width under three quarters of it.
- **Pages.** Systems go down at least the system gap apart (staff spaces
  between one system's bottom line and the next one's top), more for their
  ink; a system that does not fit, or a page break, starts a page; a page
  more than two thirds full spreads its leftover room between its systems
  (at most two and a half gaps more each); the last page does not.
- **Furniture.** The title block (title in bold, subtitle in italic,
  composer right, a part's name left) on the first page, or a title page;
  later pages numbered at the outer top corner with a running header;
  instrument names at the left of each system, centred on the line's
  staves — full on the first system (indented by them), short after.
  Titles, headers and page numbers are sized in points, names in staff
  spaces (they scale with the staff, as Sibelius's do). On paper an empty
  bar's rest is printed like any other.
- **Parts** are `PartBook`: the line alone, its part's layout, multi-bar
  rests on, no names on the systems, the part's name in the title block and
  the header, written pitch.

### The file

```yaml
subtitle: from the Hangar suite
layout:
  paper: a4                      # letter (default), a4, tabloid, a3, legal, or custom with size: [w, h]
  landscape: false
  margins: {top: 15, bottom: 15, left: 14, right: 14}   # mm (12.7 each by default)
  staff: 6.5                     # mm, 3-12 (7 by default)
  system_gap: 10                 # staff spaces (9 by default)
  spacing: 0.9                   # note spacing, 0.5-2 (1 by default; added in the verification pass)
  breaks: [{bar: 9}, {bar: 13, page: true}]             # the bar that starts the new system / page
  title_page: false
  page_numbers: true
  names: full_then_short         # full, short, none
  cautionary: true
  courtesy: false
  parts: [{line: Violin I, staff: 7.5, breaks: [{bar: 5}], multi_rests: true, paper: a4}]
lines:
  - name: Violin I
    short: Vln. I
```

Defaults are left out when written, so a score with no layout writes no
`layout:` block.

### Tests

| Package | Tests |
| --- | --- |
| `score` | `TestLayoutFile` (every key, defaults, `ForPart`, round trip, nothing written for no layout), `TestLayoutErrors` (14 bad blocks refused), `TestLayoutValidateAndBreaks` (`SetBreak`, `EnsurePart`, warnings, deep clone), `TestShortNames`; the random round trip covers the layout, parts and short names |
| `engrave` | `TestTheDemoOnPages`, `TestBreaksAreKept`, `TestCautionaries`, `TestCourtesyAccidentals`, `TestPartsGatherRests`, `TestTitlePageAndPaper`, `TestBreakingFillsSystems`, `TestTextGlyphsMatchTheirWidth`, `TestEveryScorePaginates` (every fixture and shipped score, and 40 long random scores with rest bars, key changes, random paper, staff sizes, breaks, title pages — full score and part). `checkBook` is the page rules: every bar on one system in order, systems justified to the measure (the last at most to it), inside the margins, apart by at least the system gap and their ink |
| `engrave/raster` | `TestPageGolden`: six page snapshots (the demo's pages 1 and 3 and its flute part; the new `pages.fscore` fixture's two pages and its clarinet part — courtesy natural, cautionary key, time and clef, a page break, multi-bar rests broken at a rehearsal mark); every existing golden unchanged (the galley engraves bit for bit as before) |
| `io/pdf` | `TestAScoreWritesAndParsesBack` (pages, MediaBox, contents, Info, forms reused by reference, no fonts, the same file twice), `TestPartsMetadataAndUnicode`, `TestThePDFDrawsWhatTheListDraws` (every page of the score and a part rasterised from the PDF and compared with the display list's PNG: under 1% of inked pixels apart), `TestDamagedFilesAreRefused` (no header, truncated, no `%%EOF`, shifted offsets, a wrong `Length`, a wrong `Size`, an operator the writer never writes), `TestUncompressedStreamsAreReadable`, `TestWriteRefusesNoPages`, `TestNumbers` |
| `io/svg` | `TestTheSVGIsWellFormedAndComplete` (parsed back: every `use` resolves, every item drawn, the size), `TestClipAndEscapes`, `TestPathData` |
| `scorecli` | `TestExportPDFVerb` (every file parses, the PNGs, `-o file.pdf`, `-part`, `-paper`, `-staff`, `-concert`, refusals), `TestRenderPagesAndSVG`, `TestInspectShowsTheLayout` |
| `editor` | `TestScoreBreakCommands` (system, page, kind change, removal, undo, Return on bars, the last bar refused, a part's own breaks, the page view following), `TestScorePageSetupEdits` (a drag one step, titles, short names, part settings, the save chip), `TestScoreExports`, `TestScorePageViewThroughTheFrame` (a click on a note selects it, note input writes on a page, a click on the desk clears, Ctrl+wheel zooms, a part, every dialog drawn, back to continuous); `TestScoreGLWindowDraws` shoots `pages`, `page_setup` and `export_pdf`; the keymap tests cover the new commands (no key bound twice in either preset) |

### How to verify

```
go test ./internal/score/... ./internal/editor/
SCORE_ENGRAVE_GOLDEN=update go test ./internal/score/engrave/raster -run PageGolden   # after an intended change; look at every page
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
    # also writes score_pages.png, score_page_setup.png, score_export_pdf.png
foundry score export-pdf score_demo -o /tmp/pdf -png /tmp/pdf/png -dpi 60
foundry score render internal/score/engrave/testdata/pages.fscore -png /tmp/p1.png -page 1 -scale 5
foundry score render internal/score/engrave/testdata/pages.fscore -png /tmp/cl.png -page 1 -part 1 -scale 5
foundry score render score_demo -svg /tmp/sel.svg -bars 9-12 -lines 1,5
foundry score inspect internal/score/engrave/testdata/pages.fscore      # the layout line
SCORE_PDF_DIFF=/tmp go test ./internal/score/io/pdf -run DrawsWhat      # on a failure, both renders
```

Checked by eye (each rendered and looked at, at several scales): the demo's
four score pages and six parts, the pages fixture's pages and part, the
`lines`, `dynamics` and `slurs` fixtures with a break before every bar
(ottava and pedal lines, hairpins, text crescendos and slurs carried across
systems), an SVG selection, and the GL shots of the page view and both
dialogs. The written PDFs were also opened with an independent renderer
(MuPDF, in a scratch environment, not a dependency): metadata as written,
no fonts, and its rendering of every page matched `Rasterize`'s pixel for
pixel (no pixel more than a quarter of the grey range apart); the SVG
rendered by MuPDF matched the PNG.

### Decisions

- **One engraving per system.** A page layout re-engraves each system
  rather than cutting one long system up: what the engraver does at a
  layout's edges (ties, slurs, lines, opening signatures) is what a system
  break needs, already tested, and the continuous view's output is
  unchanged bit for bit.
- **Justification stretches springs, never rods.** Every gap of a system is
  `max(rod, k · spring)` for one `k`; spacing stays relative to an eighth in
  every system (as in the galley), so a system of whole notes and one of
  sixteenths look like the same piece.
- **Breaks are "before a bar"** in the file (the bar that starts the new
  system or page), "after the selected bar" in the editor, as Sibelius
  puts them on the barline.
- **Parts take the score's layout with their own over it**, but never the
  score's breaks (a part breaks elsewhere), never a title page, and at
  written pitch whatever the score's concert view.
- **Layout edits are not music edits**: `layoutChange` is one undo step per
  gesture, saved in the file, seen by the save chip, and does not re-perform
  the score or reach the player.
- **No fonts in the PDF.** Text is the Go fonts' outlines (the same faces the
  layout measures with), so widths are exact and nothing needs embedding or
  licensing; titles' text is still in the Info dictionary.
- **The PDF reader and rasteriser are the package's own** and deliberately
  narrow (classic xref, the writer's operators): they exist to check the
  writer and to let the CLI draw what it wrote, not to read arbitrary PDFs.
- **Courtesy accidentals are parenthesised**, on the first note at the
  altered position in the next bar only (Gould's narrowest rule).
- **A rehearsal mark at a part's system start** strokes its box a hair
  (0.08 space) into the left margin; left as is.

### Deferred

Nothing of step A's brief is left out. What it left to later steps
(MusicXML export and fuller import with layout hints, OGG/MP3/FLAC, stems,
`.ftrk` both ways, the all-formats Export and Import dialogs) step B built.

Refinements no phase names, not done: instrument-family brackets in the
full score (only grand staves are braced), hiding empty staves in a
system, staff spacing made uniform across a page's systems (each system's
staves are spaced by their own ink), parenthesised "(8va)" on an ottava
continued onto a new system (it repeats "8va"), and a clef change's gap
before the barline (as in the continuous view, it stands close against
it).

## Step B: interchange and audio

MusicXML out (and more of it in), every audio format with stems, ranges
and loop tails, `.ftrk` both ways, and the Export and Import dialogs of
mockup screens 6 and 5.

### At a glance

| Package / file | What |
| --- | --- |
| `internal/score/io/musicxml/export.go`, `xmlw.go` (new) | `Export`/`ExportFile`/`Compress`: MusicXML 4.0 partwise. Parts and staves (`<staves>`, braced), voices numbered 1–4 / 5–8 per staff, written pitch with `<transpose>` and the part's written key, the accidentals each bar needs (`AlterInForce`), beams by the meter as the engraver groups them (`Meter.BeamUnits`, 4/4's eighth pairs, tuplets alone, hooks for partial beams), nested tuplets (`time-modification` the product, a bracket per level with its own numbers), ties, grace notes (slash, steal-time for `before_beat`/`on_beat`), chords, rests, bar rests, spaces as `<forward>`, percussion as `<unpitched>` with noteheads and an instrument per drum; dynamics (with `sound dynamics`), hairpins, text cresc./dim. with dashes, slurs (numbered by depth), glissandi, articulations, ornaments with `accidental-mark`s, tremolos single and two-note, fermatas by shape, arpeggios, stems (`up`/`down`), `vel=` as the note's dynamics, ottava and pedal lines (re-pedal as `change`), technique / staff / expression text (told apart by the direction's id); tempo marks with words, metronome (dotted units), `sound tempo`, rit./accel. dashes to their `To`; rehearsal marks, repeats with times, endings (stop or discontinue), segno, coda, To Coda, Fine, D.C./D.S. (al Fine / al Coda / con rip.); clef changes at bar starts and inside bars (with `<offset>` for part-wide directions between notes); credits (title, subtitle, composer), `<defaults>` (scaling from the staff size, page size, margins) and `<print new-system/new-page>` from the layout's breaks (`NoLayout` leaves them out); part names, abbreviations, MIDI channel, program, volume, pan, and the library instrument in `<virtual-instrument>`. Deterministic: the same score writes the same bytes |
| `internal/score/io/musicxml/import.go` | Voltas from `<ending>` (numbers, start/stop/discontinue), jumps from `<sound>`, direction words and `<segno>`/`<coda>`, clef changes at bar starts and mid-bar, fermata shapes, ornament accidentals, `trem2`, wavy glissandi, arpeggio direction, `other-articulation` legato, rests' fermatas, `n` (niente), pedal `change` as a re-pedal, italic words below as `expr`, tempo text (rit., a tempo, bold words over the first part) as text-only tempo marks with dashes ending at `To`, words with dashes as text crescendos, part abbreviation (`Short`, when not the automatic one), volume, subtitle and title credits, the page layout and breaks (`Options.NoLayout`). A file whose `<software>` is ours (`Software`) also gives stems, velocities, grace timing and its `<virtual-instrument>`. Lyrics and chord symbols still warn |
| `internal/score/meter.go` | `Meter.BeamUnits` (moved from the engraver, which now calls it; goldens unchanged) |
| `internal/score/io/audioenc` (new) | `Encode`/`WriteFile`/`Available`/`DefaultOptions`/`ParseFormat`: WAV 16/24 (`tracker.WriteWAV`), FLAC (`flac.go`: own encoder — 4096-sample blocks, the best of left/right, left/side, right/side, mid/side; constant, verbatim, fixed 0–4 and LPC 1–12 per channel from a Tukey-windowed autocorrelation and Levinson–Durbin, 14-bit coefficients with error feedback; Rice partitions 0–8 with the reference encoder's bit estimate; STREAMINFO with frame sizes, sample count and MD5; VORBIS_COMMENT tags), `flacdec.go` (`DecodeFLAC`: every subframe kind, both residual methods, CRC-8, CRC-16 and MD5 checked), OGG Vorbis and MP3 through `dl.go` (cgo; `dlopen`/`LoadLibraryExA`, typed C trampolines and the encode loops in C over opaque blocks, output in a C buffer copied out; Vorbis VBR by quality with ENCODER/TITLE/ARTIST comments, fixed serial; LAME VBR (`vbr_mtrh`, V0–V9) or CBR, the LAME/Xing tag written over its placeholder, an ID3v2.3 tag of our own), `dl_nocgo.go` (a build without cgo says so), `MissingLibraryError` (which library, where it looked, how to get it), `CodecDirs`, `Libraries` |
| `internal/score/io/exportset` (new) | `Plan` (folder, base name, formats, the PDF / MusicXML / MIDI / `.ftrk` options, `AudioPlan`), `Plan.Files` (what it will write), `Run` (each format; audio rendered once — the mix, then each line alone for its stem — and encoded in every audio format; a failing format is in `Result.Failed` and the rest are written), `Fold` (the tail mixed back over the start), `TailSeconds` (bars at the end's tempo), `BarsRange`, `SectionRange` |
| `internal/score/io/ftrk` (new) | `Export` (interpret with the instruments, repeats out, slides as runs; the grid the fewest rows a beat that hold every onset and end, `chooseGrid`, up to 24, or `RowsPerBeat`; a channel per simultaneous note of a line; instrument per line by `Ref`; velocity in the volume column, articulation `@`, tempo `M` (ramps a beat at a time), markers `E` with names; patterns of up to four bars broken at sections, identical ones shared; sections, loop/next, params, bindings, drive/crush inserts, reverb/delay/compressor; warnings for what a song cannot hold), `ToMIDI`/`Analyze`/`Import` (the order list played once — K, forward B, N, C, M, E — into a MIDI file of a track an instrument naming its `.finst`, then the MIDI importer; sections' loop/next, params, bindings, buses and master after) |
| `internal/score/io/midi/import.go` | A track's instrument-name event naming a library instrument is its instrument (what the export writes) |
| `internal/score/scorecli/export.go` (new), `import.go` | `export-musicxml`, `export-audio` (`-verify` decodes with other decoders), `export-ftrk`, `export` (every format, the dialog's plan), `codecs`; `import` reads `.ftrk` (a path or a song's name) with the MIDI flags, and `-no-layout` for MusicXML |
| `internal/editor/editor_score_export.go` (rewritten) | The Export dialog: the formats column with its notes, option blocks for PDF (score, parts, paper pills, staff size, transposed / concert), MusicXML (layout, `.mxl`), MIDI and the song (repeats, grid), audio (range pills with reasons when unavailable, one mix / mix + stems, rate, bits, OGG quality, MP3 VBR or CBR, tail in bars, fold, the slider's tempo, a summary line, the stems named, a missing codec in red), the folder, the file count; `exportPlan` (no ImGui), `scoreRunExport`, the run off the frame with the status following each step. Export PDF and Export Audio open it with their format alone; the old WAV and PDF dialogs are gone |
| `internal/editor/editor_score_import.go` (new) | The Import dialog: a browser (roots, project, folders and the files it reads), then the table (Import, the line's name, notes, program, New line / Skip, the instrument picker, the clef) and "How notes are read" (quantise pills, triplets, voices, key and time from the file or chosen, tempo, velocity → dynamics) or, for MusicXML, the page layout; `scoreImportChoose`/`run` (no ImGui), the import off the frame, `scoreFinishImport` (a new unsaved score, named after the file unless a score has that name) |
| `editor_score_window.go`, `_keymap.go`, `_page.go` | File > Import (`import`, `Ctrl+Shift+I`), Export (`export`, `Ctrl+Shift+X`), Export Audio, Export PDF; the page view's Export PDF opens the dialog |
| `internal/pack/editor.go`, `cmd/foundrypack` | `EditorOptions.Codecs` / `-codecs dir`: the install's `codecs/` (the platform's libraries, their licences, a README with their sources), each Windows DLL's imports checked |
| `packaging/codecs/fetch-windows.sh` (new), `Taskfile.yml`, `release-editor.yml` | `task codecs-windows` fetches MSYS2's pinned libogg, libvorbis/libvorbisenc and LAME DLLs with upstream licences; `dist-editor-windows` carries them; `CODECS_LINUX` optional; CI installs zstd |
| `docs/score/CODECS.md` (new), `docs/guide/32-score-editor.md` | How the encoders load, ship and are licensed; *Exporting*, *Importing*, the new commands, MusicXML export and fuller import, Tracker songs both ways |

### How the formats work

- **MusicXML out.** Each measure writes its print and barlines, then
  attributes (divisions once, key, time, staves, clefs, transposition),
  then each staff's voices in turn with a `<backup>` between them. The
  first part carries the score-wide directions, each placed in the first
  voice at its offset (an `<offset>` when it falls between notes); jumps
  that act at a bar's end go after the first voice padded to the barline.
  A spanner the file must close is closed: a hairpin or ottava the next
  event starts another of ends after this one, and lines still open at
  the voice's end end there. Divisions are the least common multiple of
  every length and position's denominator in quarters, so every duration
  is exact.
- **What comes back.** Exported and imported again, every fixture and the
  shipped scores are the same in what MusicXML holds (the comparison reads
  marks in name order, the plain tremolo as `trem`, a forced accidental
  the bar needs anyway as automatic, space at a bar's end as nothing, and
  a spanner's end where the next spanner or the voice's end would put it
  as implied). The engraver's four fixtures that overfill a bar are not
  compared: MusicXML reads such a bar as one of another length.
- **Checked outside.** Every fixture's export validates against the
  MusicXML 4.0 XSD (lxml, in a scratch environment) and MuseScore 4.7
  (the AppImage, headless, in a scratch environment; neither is a
  dependency) opens and renders the demo, the form, percussion,
  transposing and grand staff fixtures and a score of every construct as
  written: transposed keys, clefs mid-bar, endings, segno and coda, D.S.
  al Coda, rit. with dashes, text diminuendo, two-note tremolo, ornaments
  with accidentals, drum noteheads. MuseScore flattens a nested tuplet into
  one (its reading; the file is to the standard).
- **Audio.** A plan renders the mix once and each stem once (the line
  soloed, through its own inserts, the buses and the master) and encodes
  every format from those. The range is performance ticks (the loop's,
  the selection's first time through, the whole score). The tail is in
  bars at the end's tempo; with the fold, the render's tail is added back
  over the start and the file is exactly the range long, so it loops
  seamlessly. The OGG and MP3 encoders are checked before anything renders.
- **`.ftrk`.** A song's grid is rows a quarter (its LPB); a score's notes
  are performance ticks (960 a quarter), so a grid that divides 960 and
  holds every note's start and end makes the song exact; otherwise notes
  move to the nearest row and the warning says how many. Each line's
  channels are dealt greedily: a note takes the first channel free at its
  start.

### Tests

| Package | Tests |
| --- | --- |
| `musicxml` | `TestExportRoundTripsEveryFixture`, `TestExportRoundTripsEveryConstruct` (a score of every construct; the document's elements checked), `TestExportWithoutLayout`, `TestExportIsStableAndCompresses` (same bytes twice; `.mxl` reads back), `TestExportRefusesAnEmptyScore`; `TestImportWarnsAboutWhatItCannotKeep` now checks voltas, D.C. and a clef change are kept and lyrics and chord symbols warn. `MUSICXML_DUMP=f` writes the every-construct file |
| `audioenc` | `TestFLACIsLossless` (stereo and mono, 16/24-bit, 44.1/48 kHz and a rate the header cannot name, one short block, exactly one block, nothing; decoded sample for sample, tags), `TestFLACCompresses`, `TestFLACDecoderChecks` (a flipped bit, a cut-off file, no magic), `TestWAV`, `TestOGGDecodesBack` (oggvorbis: length to the sample, correlation ≥ 0.99, comments, quality matters, deterministic, bad quality refused), `TestMP3DecodesBack` (go-mp3: VBR with its Xing header and CBR, both rates, ID3 title, delay found and correlation ≥ 0.98; bad rate and channels refused), `TestMissingLibrariesAreSaid`, `TestLibrariesLoadFromTheCodecsFolder`, `TestParseFormatAndDescribe`, `BenchmarkFLAC` (about 40× real time). `AUDIOENC_DUMP=dir` writes a file of each format |
| `exportset` | `TestEveryFormat` (every format with stems: the files `Files` promised, each read back as what it is), `TestRangesTailsAndStems` (rung out, folded — exactly the region long, its start the start plus the tail —, stems adding up to the mix), `TestFoldAndTail`, `TestPlansAreChecked` |
| `ftrk` | `TestExportTheDemoScore` (every note on the grid, refs, sections, BPM, saved and loaded, rendered by the Tracker its loudness follows the score's (correlation ≥ 0.8) and ends where it does), `TestTheGridFitsTheRhythms`, `TestChannelsTempoAndMarkers`, `TestImportASong` (parts and refs, the flute's notes through a cut and a delay, the tempo change, sections' loop and next, params and bindings, sends, buses, master, volume), `TestSongScoreSongRoundTrip` (the same notes at the same rows), `TestTheShippedSongsImport` |
| `midi` | `TestImportTakesTheInstrumentAnExportNames` |
| `scorecli` | `TestExportMusicXMLVerb`, `TestExportAudioVerb` (each format with `-verify`, a section's stems folded, bad flags), `TestExportFTRKVerbAndImport`, `TestExportVerb` (several formats, the default folder, `codecs`) |
| `editor` | `TestScoreExportDialogPlans` (defaults, the plan's files, stems/loop/fold, Export PDF / Audio alone, refusals), `TestScoreExportRunsThroughThePoll`, `TestScoreImportDialog` (MIDI with changed rows and options, MusicXML with and without layout, a song, finishing, a broken file), `TestScoreExportAndImportDialogsDraw`; `TestScoreExportAudio` and the looping-score test go through the new plan; `TestScoreGLWindowDraws` shoots `export`, `import_browse`, `import_midi`, `import_musicxml` |
| `pack` | `TestAnEditorInstallCarriesItsCodecs`, `TestTheFetchedWindowsCodecsStage` (with `FOUNDRY_CODECS_WINDOWS`) |
| `internal` | `TestTheReleaseLinksOnlyScorePlayback` names the codec loader |

### How to verify

```
go test ./internal/score/... ./internal/pack/ ./internal/editor/
FOUNDRY_REQUIRE_CODECS=1 go test ./internal/score/io/audioenc/ ./internal/score/io/exportset/
SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/   # score_export.png, score_import_*.png
foundry score export-musicxml score_demo -o /tmp/demo.musicxml      # open it in MuseScore or Sibelius
foundry score export score_demo -formats all -stems -o /tmp/demo
foundry score export-audio score_demo -format ogg -section bridge -fold -tail 2 -o /tmp/bridge.ogg -verify
foundry score export-ftrk score_demo -o /tmp/demo.ftrk && foundry score import /tmp/demo.ftrk -o demo_again
foundry score codecs
task codecs-windows && FOUNDRY_CODECS_WINDOWS=build/codecs/windows-amd64 go test -run Codecs ./internal/pack/
```

Checked by eye: the GL shots of both dialogs (the Export dialog as the
mockup has it — PDF, MusicXML, OGG, stems; the Import dialog's browser, a
MIDI file's tracks and a MusicXML file's parts) and MuseScore's renders of
the exports above. The written FLACs were also decoded by the reference
libFLAC (its MD5 check on, in a scratch environment) and every format by
libsndfile; both read them as written.

### Decisions

- **The library instrument travels in the file.** MusicXML's
  `<virtual-instrument>` and MIDI's instrument-name event carry the
  `.finst`, so an export imported again plays the same sounds; other
  programs ignore them.
- **Our own files say more.** Stems, velocities and grace timing are only
  read from files `Foundry Score` wrote: Sibelius writes a `<stem>` on
  every note and some exporters a dynamics attribute on every note, which
  as marks would freeze stems and dynamics.
- **Staff text, technique and expression** look alike as `<words>`: the
  export tells them apart in the direction's id, which other programs
  ignore; from other files italic words below are expression and the
  technique words (pizz., arco …) technique.
- **Spanners are closed.** MusicXML wants every wedge, octave shift, pedal
  and slur stopped, so the export closes one the score leaves open where
  the score would end it; the importer reads that end back as written.
- **Encoders are loaded, never linked**, and the encode loops are C so no
  Go pointer is held by C and no Go callback runs on a C stack. The opaque
  libvorbis/libogg structs are oversized blocks rather than declarations
  that could drift from a library version.
- **Stems are the line alone through the whole mix chain** (its inserts,
  its sends into the buses, the master), the way a DAW bounces them; with
  the master compressor on they do not sum to the mix exactly.
- **The loop tail folds into the start** rather than being cut, which is
  what makes a looping game cue seamless; "ring out" (no fold) appends it.
- **Import makes a new score** (named after its file, unsaved) rather than
  merging lines into the open one: the table's Line column is New line or
  Skip.

### Deferred

- **Lyrics and chord symbols** in MusicXML, both ways: the score has no
  model for them yet (SPEC.md puts lyrics and chord symbols in phase 6);
  the importer warns, as before.
- **A part's own layout** (`layout.parts`) is not in the MusicXML: a
  partwise file has one page layout, the score's.
- **Tempo ramps** (`ramp: true`) export as their marks only: MusicXML has
  no element for a smooth change between two metronome marks.
- **What MusicXML has no place for** stays in the `.fscore`: the mixer and
  its effects, lanes, params, bindings, markers, a section's loop and next,
  swing and humanise.
- **Linux codecs** are the system's by default (`CODECS_LINUX` carries them
  when given): bundling distribution `.so` files would tie the tarball to
  one distribution's library versions.
- **Stem directions** are written only where the score forces one, so
  MuseScore draws drum voices with its own drum-set stems.

## Verification pass

An adversarial check of both steps against SPEC.md's phase line: every
CLI verb run, every format written and read back by other software
(PyMuPDF for the PDFs and SVG, libsndfile for WAV/FLAC/OGG/MP3, the
MusicXML 4.0 XSD, MuseScore 4.7 reading our MusicXML and writing its own
back for our importer), pages and parts rendered and looked at, odd
inputs tried (a 60×50 mm page at a 12 mm staff, Unicode and markup in
titles, names longer than the page, twenty staves on letter paper,
margins wider than the paper, folded loop tails, ranges past the end).

### Found and fixed

- **A system taller than the page ran off the paper.** Twenty staves at the
  default 7 mm on letter printed the first thirteen and cut off the rest
  (the page rule test let a page's only system run past the bottom).
  `Paginate` now sets the pages at a smaller staff until every system
  fits (down to 3 mm), `Book.Staff`/`Book.Reduced` say so, and the page
  strip, `inspect` and `export-pdf` report it. The page-rule checker now
  refuses a system past the bottom margin unless it is already at the
  smallest staff. Test: `TestTallSystemsShrinkTheStaff`.
- **Long instrument names pushed the music off the page.** A name's width
  was the indent, however long. Names now take at most a quarter of the
  text width on the first system and a sixth after, printing smaller
  when longer; titles, the composer, a part's name and the running header
  shrink to fit the page too. Test: `TestLongNamesAndTitlesFit`
  (checked failing without the fix).
- **Note spacing was not part of the page layout** (SPEC's "spacing"): the
  layout had the system gap but no note spacing. `layout: spacing:`
  (0.5–2, 1 by default; Sibelius's note-spacing rule) scales every
  duration's room before justification, in the score and its parts, with a
  Page Setup slider. Tests: `TestLayoutSpacing`, `TestLayoutFile`,
  `TestLayoutErrors`, the random round trip.
- **Margins could leave the music no paper** (custom paper with wide
  margins: a negative measure, a bar a system off the page). The page now
  lays out with `Paper.TextMargins` (margins scaled down to leave 25 mm
  each way) and Validate warns; the file still loads, so a Page Setup
  slider can never make a score unloadable. Test:
  `TestTightMarginsAreMadeSmaller`.
- **A part's own layout was lost on a rename** and left behind on a
  removal (it is kept by line name): `edit.RenameLine` and
  `edit.RemoveLine` now carry it, and the page view's shown part follows
  its line when lines move or go. Tests: `TestMoveRemoveRenameLines`,
  `TestScorePagePartFollowsItsLine`.
- **Page Setup could not set a part's paper**, which the file and step A's
  notes allow: a Part paper picker (as the score, or a named size, and
  landscape) is beside the part's staff size and gap. The page strip
  showed the score's paper while a part with its own was shown; it shows
  the part's.
- **Importing a Tracker song with its own (embedded) instruments played
  most of them on the piano** (demo_theme's kick, snare, hat, pluck, crash
  and riser). `ftrk.ToMIDI` now gives each embedded instrument the
  library's nearest — the `.finst` named for its sample (the starter
  library has one per synth sample), else one its name matches — and the
  import says which. The Tracker and score renders of demo_theme now
  correlate 0.94 in loudness with matching spectra. Test:
  `TestEmbeddedInstrumentsTakeLibrarySounds`. The CLI printed a song's
  analysis warnings twice; once now.
- **MusicXML from MuseScore imported a cello as violins**: the importer
  read the sound id before the part name, and MuseScore writes a cello's
  sound as `strings.baryton`. Names (part, instrument) now come before the
  sound id. Test: `TestImportNamesBeatSoundIDs`. (With that, MuseScore's
  own re-export of our demo imports with every note identical to ours.)
- **Audio exports clipped silently** (`score render` warns; the export did
  not: the demo's folded bridge peaks at 1.01). The export now warns per
  file with the master gain that would fit (`exportset.Peak`). Test:
  `TestClippingIsSaid`.

### Checked and found right

PDFs: no fonts, metadata as written, PyMuPDF renders every page as our
rasteriser does. SVG selection renders as the PNG. Every fixture's
MusicXML validates against the 4.0 XSD. WAV, FLAC, OGG and MP3 (mix and
stems) decode in libsndfile at the right length and rate; FLAC's MD5
checks; folded tails are exactly the range long; stems are the line alone
through the chain. Pagination of a 20-line, 200-bar score takes well
under a second. The release build (`task test-release`) passes except
the GL readback lint below, and links no `score/io` or editor package.

### Test status at the phase's end

`go build ./...` passes. `go test ./...`: every package passes except
`internal/glext`'s `TestEveryReadbackGoesThroughTheWrappers`, which fails
the same way before this phase (it names GL tests under `internal/gfx`
and `internal/render` — the render session's work, not this project's).
`task test-release`: the same one failure.

## Deferred (for phase 6 or later)

- **Lyrics and chord symbols**, in the score and so in MusicXML both
  ways: the score has no model for them yet (SPEC.md phase 6). The
  MusicXML importer warns that it leaves them out.
- **A part's own layout in MusicXML** (`layout.parts`): a partwise file has
  one page layout, so only the score's is written.
- **Tempo ramps** (`ramp: true`) export to MusicXML as their marks only;
  MusicXML has no smooth change between two metronome marks.
- **Mixer, effects, lanes, params, bindings, game markers, a section's
  loop/next, swing and humanise** stay in the `.fscore`; MusicXML has no
  place for them.
- **Linux codecs** are the system's libraries by default (`CODECS_LINUX`
  bundles a folder): bundling distribution `.so` files would tie the
  tarball to one distribution.
- **Single-sound percussion from a Tracker song** (a kick that is its own
  `.finst`) imports onto a percussion staff whose drum map does not name
  its key, so its notes sit off the staff's usual positions; it plays
  right. Drum maps are phase 6 (percussion staves + drum maps).
- **Text is outlines in the PDF**, so it cannot be searched or copied
  (the price of no font embedding, which SPEC asks for); titles are in
  the document's metadata. Letters the Go fonts lack (CJK, ♯ in text) are
  left out of printed text.
- Refinements no phase names, still not done: instrument-family brackets
  in the full score (only grand staves are braced), hiding empty staves,
  uniform staff spacing across a page's systems, "(8va)" on a continued
  ottava, a gap between a clef change and the barline it stands before,
  stem directions in MusicXML only where the score forces one.
