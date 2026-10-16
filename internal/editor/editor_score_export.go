package editor

// editor_score_export.go — File > Export (mockup screen 6): every format
// in one pass, into exports/<name>/ beside resources/.
//
//	┌ Export · hangar_theme ────────────────────── to exports/hangar_theme/ ┐
//	│ [x] PDF  score + parts     │ PDF       Include [x] Full score [x] Parts │
//	│ [x] MusicXML  for Sibelius │           Paper  (Letter) A4  Tabloid      │
//	│ [ ] MIDI                   │           Staff size 7.0 mm                │
//	│ [x] OGG  game audio        │           Score in (Transposed) Concert    │
//	│ [ ] WAV                    │ Audio     Range (Whole) Loop  Selection    │
//	│ [ ] MP3                    │ (OGG)     Files  One mix (Mix + stems)     │
//	│ [ ] FLAC                   │           48 kHz · quality 6 · tail 2 bars │
//	│ [ ] Tracker song (.ftrk)   │                                            │
//	├────────────────────────────┴────────────────────────────────────────────┤
//	│ 8 files                                               [Cancel] [Export] │
//	└─────────────────────────────────────────────────────────────────────────┘
//
// A block of options shows for each kind of format ticked: the PDF's (the
// score and which parts, paper, staff size, transposed or concert), the
// audio's (range — whole score, loop region, selection —, one mix or a mix
// and a stem per line, rate, bit depth, OGG and MP3 quality, the tail in
// bars and whether it folds into the start so the file loops), MusicXML's
// (page layout, compressed) and the Tracker song's grid. The footer counts
// the files the export writes (exportset.Plan.Files). An OGG or MP3 whose
// encoder library is missing says so in the dialog, before anything runs.
//
// The export runs off the frame (internal/score/io/exportset), the status
// line following its steps; the formats it could not write are named when
// it finishes, the rest are written all the same. Export PDF and Export
// Audio open the same dialog with only their format ticked.

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/score/io/audioenc"
	"foundry/internal/score/io/exportset"
	"foundry/internal/score/io/ftrk"
	"foundry/internal/score/play"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreExportKind is what an audio export covers.
type scoreExportKind int

const (
	scoreExportWhole scoreExportKind = iota
	scoreExportLoop
	scoreExportSelection
)

// scoreExportDlg is the dialog's state; it is kept between openings.
type scoreExportDlg struct {
	Formats map[exportset.Format]bool
	PDF     scorePDFRequest
	// Audio.
	What       scoreExportKind
	Stems      bool
	Rate, Bits int
	OGGQuality float32
	MP3Quality int32
	MP3CBR     bool
	MP3Bitrate int32
	TailBars   float32
	Fold       bool
	Scaled     bool
	// MusicXML, MIDI and the Tracker song.
	Compressed bool
	NoLayout   bool
	NoRepeats  bool
	Rows       int32 // rows a beat, 0 the fewest that hold the rhythms
	Dir        string
	set        bool
}

var (
	scoreShowExport   bool
	scoreExp          scoreExportDlg
	scoreExportResult chan scoreExportMsg
	// scoreExportStep is what the running export is doing.
	scoreExportStep string
)

// scoreExportMsg is a running export's news: a step, or (done) the end.
type scoreExportMsg struct {
	text string
	done bool
}

// scoreMP3Bitrates are the constant bit rates offered.
var scoreMP3Bitrates = []int32{128, 160, 192, 256, 320}

// scoreRowChoices are the Tracker grids offered (0 is automatic).
var scoreRowChoices = []int32{0, 1, 2, 3, 4, 6, 8, 12, 16, 24, 32}

// defaultExportDlg is the dialog as it first opens: PDF, MusicXML and OGG
// ticked, as the mockup has it.
func defaultExportDlg() scoreExportDlg {
	return scoreExportDlg{
		Formats: map[exportset.Format]bool{exportset.PDF: true, exportset.MusicXML: true, exportset.OGG: true},
		Rate:    48000, Bits: 24, OGGQuality: audioenc.DefaultOGGQuality, MP3Quality: audioenc.DefaultMP3Quality,
		MP3Bitrate: 192, TailBars: 2, set: true,
	}
}

// exportSpan is the ticks an export covers and a name for it ("bars
// 5-8"), or why it cannot be had. The whole score runs to its end and stops
// there: a score that loops (`loop: true`) would otherwise start again in
// the tail.
func (e *scoreState) exportSpan(k scoreExportKind) (from, to int64, name string, err error) {
	perf := e.perf()
	if perf == nil {
		return 0, 0, "", fmt.Errorf("the score cannot be performed")
	}
	switch k {
	case scoreExportLoop:
		a, b, ok := scoreLoopTicks(perf, e.Transport.Loop)
		if !ok {
			return 0, 0, "", fmt.Errorf("there is no loop region (drag along the ruler, or select bars and press L)")
		}
		return a, b, "loop", nil
	case scoreExportSelection:
		var a, z int
		switch e.Sel.Kind {
		case scoreSelBars:
			a, z = e.Sel.BarA, e.Sel.BarB
		case scoreSelEvent, scoreSelCaret:
			a, z = e.Sel.Loc.Bar, e.Sel.Loc.Bar
		default:
			return 0, 0, "", fmt.Errorf("nothing is selected")
		}
		lp := scoreLoopBars(e.Score, a, z)
		from, to, ok := scoreLoopTicks(perf, lp)
		if !ok {
			return 0, 0, "", fmt.Errorf("bars %d-%d do not play", a, z)
		}
		if a == z {
			return from, to, fmt.Sprintf("bar%d", a), nil
		}
		return from, to, fmt.Sprintf("bars%d-%d", a, z), nil
	}
	return 0, perf.Length, "", nil
}

// exportSeconds is how long an export plays, tail aside.
func (e *scoreState) exportSeconds(k scoreExportKind, scaled bool) float64 {
	perf := e.perf()
	from, to, _, err := e.exportSpan(k)
	if perf == nil || err != nil {
		return 0
	}
	end := perf.Seconds
	if to > 0 {
		end = perf.Tempo.Seconds(float64(to))
	}
	sec := end - perf.Tempo.Seconds(float64(from))
	if scaled && e.Transport.Scale > 0 {
		sec /= e.Transport.Scale
	}
	return sec
}

// formats are the ticked formats, in the dialog's order.
func (d scoreExportDlg) formats() []exportset.Format {
	var out []exportset.Format
	for _, f := range exportset.Formats {
		if d.Formats[f] {
			out = append(out, f)
		}
	}
	return out
}

// audioFormats are the ticked audio formats.
func (d scoreExportDlg) audioFormats() []exportset.Format {
	var out []exportset.Format
	for _, f := range d.formats() {
		if f.Audio() {
			out = append(out, f)
		}
	}
	return out
}

// exportPlan is the dialog as an export plan for the score being edited.
func (e *scoreState) exportPlan(d scoreExportDlg) (exportset.Plan, error) {
	p := exportset.Plan{Dir: d.Dir, Base: scoreExportBase(), Formats: d.formats(), Compressed: d.Compressed}
	if p.Dir == "" {
		p.Dir = scoreExportsDir()
	}
	if len(p.Formats) == 0 {
		return p, fmt.Errorf("tick a format to export")
	}
	p.PDF = d.PDF.pdfOptions()
	if p.Has(exportset.PDF) && !p.PDF.Score && len(p.PDF.Parts) == 0 {
		return p, fmt.Errorf("the PDF has neither the full score nor a part")
	}
	p.MusicXML.NoLayout = d.NoLayout
	p.MIDI.NoRepeats = d.NoRepeats
	p.FTRK = ftrk.ExportOptions{RowsPerBeat: int(d.Rows), NoRepeats: d.NoRepeats}
	a := exportset.DefaultAudio()
	a.Rate, a.Bits, a.Stems = d.Rate, d.Bits, d.Stems
	a.OGGQuality, a.MP3Quality = d.OGGQuality, float32(d.MP3Quality)
	if d.MP3CBR {
		a.MP3Bitrate = int(d.MP3Bitrate)
	}
	a.TailBars, a.FoldTail = float64(d.TailBars), d.Fold
	if d.Scaled {
		a.TempoScale = e.Transport.Scale
	}
	if p.HasAudio() {
		var err error
		if a.From, a.To, a.RangeName, err = e.exportSpan(d.What); err != nil {
			return p, err
		}
	}
	p.Audio = a
	return p, nil
}

// scoreRunExport runs a plan: the export's work, off the frame. The
// message is what was written and what was not.
func scoreRunExport(e *scoreState, p exportset.Plan, step func(string)) (string, error) {
	res, err := exportset.Run(e.Score, p, func(what string, done, total int) {
		if step != nil {
			step(fmt.Sprintf("Exporting (%d/%d): %s", done+1, total, what))
		}
	})
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Exported %d file(s) to %s", len(res.Files), p.Dir)
	if err := res.Error(); err != nil {
		if len(res.Files) == 0 {
			return "", err
		}
		msg += "; not written: " + err.Error()
	}
	if len(res.Warnings) > 0 {
		msg += fmt.Sprintf(" (%d note(s): %s)", len(res.Warnings), res.Warnings[0])
	}
	return msg, nil
}

// scoreExportBusy reports an export running.
func scoreExportBusy() bool { return scoreExportResult != nil }

// scoreStartExport runs the dialog's export in the background, on a copy
// of the score.
func scoreStartExport() {
	if scoreExportBusy() {
		return
	}
	e := scEd
	p, err := e.exportPlan(scoreExp)
	if err != nil {
		e.Status = "Export: " + scoreErrText(err)
		return
	}
	snap := *e
	snap.Score = e.Score.Clone()
	ch := make(chan scoreExportMsg, 64)
	scoreExportResult = ch
	e.Status = "Exporting..."
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- scoreExportMsg{text: fmt.Sprintf("Export failed: %v", r), done: true}
			}
		}()
		msg, err := scoreRunExport(&snap, p, func(s string) {
			select {
			case ch <- scoreExportMsg{text: s}:
			default:
			}
		})
		if err != nil {
			msg = "Export failed: " + err.Error()
		}
		ch <- scoreExportMsg{text: msg, done: true}
	}()
}

// scorePollExport shows a running export's step and its end.
func scorePollExport() {
	for scoreExportResult != nil {
		select {
		case m := <-scoreExportResult:
			scEd.Status = m.text
			if m.done {
				scoreExportResult = nil
				scoreExportStep = ""
			} else {
				scoreExportStep = m.text
			}
		default:
			return
		}
	}
}

// scoreOpenExport opens the dialog as it was last left, its range the
// loop when one is on, else the selected bars when there are some, else
// the whole score; every part, the layout's paper.
func scoreOpenExport() {
	e := scEd
	if !scoreExp.set {
		scoreExp = defaultExportDlg()
	}
	d := &scoreExp
	d.What = scoreExportWhole
	switch {
	case e.Transport.Loop.On:
		d.What = scoreExportLoop
	case e.Sel.Kind == scoreSelBars:
		d.What = scoreExportSelection
	}
	d.Scaled = e.Transport.Scale != 1
	if len(d.PDF.Parts) != len(e.Score.Lines) {
		d.PDF = scorePDFRequest{Score: true, Parts: make([]bool, len(e.Score.Lines)), Concert: e.Score.Concert}
		for i := range d.PDF.Parts {
			d.PDF.Parts[i] = true
		}
	}
	d.Dir = scoreExportsDir()
	scoreShowExport = true
}

// scoreOpenExportOnly opens the dialog with one format ticked: Export PDF,
// Export Audio.
func scoreOpenExportOnly(f exportset.Format) {
	scoreOpenExport()
	scoreExp.Formats = map[exportset.Format]bool{f: true}
}

// scoreOpenPDF is Export PDF.
func scoreOpenPDF() { scoreOpenExportOnly(exportset.PDF) }

// scoreAudioSummary is the audio block's last line: "48 kHz · quality 6 ·
// tail 2 bars, folded into the start".
func (d scoreExportDlg) audioSummary() string {
	var parts []string
	for _, f := range d.audioFormats() {
		o := audioenc.DefaultOptions(audioenc.Format(f))
		o.Bits, o.Quality = d.Bits, d.OGGQuality
		if f == exportset.MP3 {
			o.Quality = float32(d.MP3Quality)
			if d.MP3CBR {
				o.Bitrate = int(d.MP3Bitrate)
			}
		}
		parts = append(parts, f.Label()+" "+o.Describe(d.Rate))
	}
	tail := fmt.Sprintf("tail %g bar(s)", d.TailBars)
	switch {
	case d.TailBars == 0:
		tail = "no tail"
	case d.Fold:
		tail = fmt.Sprintf("loop tail %g bar(s), folded into the start", d.TailBars)
	}
	return strings.Join(parts, "; ") + " · " + tail
}

// scorePillOff is an unchosen pill: dimmer than a button, so the chosen
// one (scoreColActive) stands out as the mockup's pills do.
var scorePillOff = imgui.Vec4{X: 0.22, Y: 0.23, Z: 0.26, W: 1}

// scorePills draws a row of choices as a pill of buttons and returns the
// one chosen (or cur); a disabled one is dimmed and says why.
func scorePills(id string, labels []string, cur int, why func(i int) string) int {
	for i, l := range labels {
		if i > 0 {
			imgui.SameLine()
		}
		reason := ""
		if why != nil {
			reason = why(i)
		}
		if reason != "" {
			imgui.PushStyleVarFloat(imgui.StyleVarAlpha, 0.45)
		}
		col := scorePillOff
		if i == cur {
			col = scoreColActive
		}
		imgui.PushStyleColor(imgui.StyleColorButton, col)
		if imgui.Button(l+"##"+id+fmt.Sprint(i)) && reason == "" {
			cur = i
		}
		imgui.PopStyleColor()
		if reason != "" {
			imgui.PopStyleVar()
			editorMenuTooltip(reason)
		}
	}
	return cur
}

// scoreOptLabel starts a row of the options grid with its label.
func scoreOptLabel(label string) {
	imgui.TableNextRow()
	imgui.TableNextColumn()
	editorTextMuted(label)
	imgui.TableNextColumn()
}

// scoreOptHeading is a block's heading row.
func scoreOptHeading(text string) {
	imgui.TableNextRow()
	imgui.TableNextColumn()
	imgui.Text(text)
	imgui.TableNextColumn()
}

// scoreExportDialog is the dialog.
func scoreExportDialog() {
	title := fmt.Sprintf("Export · %s###scexport", scoreExportBase())
	imguibackend.WithModalPopup(title, &scoreShowExport, func() {
		e := scEd
		d := &scoreExp
		if d.Formats == nil {
			*d = defaultExportDlg()
		}
		if len(d.PDF.Parts) != len(e.Score.Lines) {
			d.PDF.Parts = make([]bool, len(e.Score.Lines))
		}
		// The folder as the project sees it: exports/<name>/.
		rel := d.Dir
		if r, err := filepath.Rel(filepath.Dir(filepath.Clean(fileutil.ResourcesPath)), d.Dir); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
		editorTextMuted("to " + filepath.ToSlash(rel) + "/")
		if imgui.BeginTableV("##scexpcols", 2, imgui.TableFlagsBordersInnerV, imgui.Vec2{}, 0) {
			imgui.TableSetupColumnV("formats", imgui.TableColumnFlagsWidthFixed, 230, 0)
			imgui.TableSetupColumnV("options", imgui.TableColumnFlagsWidthFixed, 600, 0)
			imgui.TableNextRow()
			imgui.TableNextColumn()
			for _, f := range exportset.Formats {
				on := d.Formats[f]
				if imgui.Checkbox(f.Label()+"##scexpf"+string(f), &on) {
					d.Formats[f] = on
				}
				if n := f.Note(); n != "" {
					imgui.SameLine()
					editorTextMuted(n)
				}
				if f.Audio() {
					if err := audioenc.Available(audioenc.Format(f)); err != nil {
						editorMenuTooltip(err.Error())
					}
				}
			}
			imgui.TableNextColumn()
			scoreExportOptions(e, d)
			imgui.EndTable()
		}
		imgui.SetNextItemWidth(560)
		imgui.InputText("Folder##scexpdir", &d.Dir)
		p, err := e.exportPlan(*d)
		files := 0
		if err == nil {
			files = len(p.Files(e.Score))
		}
		imgui.Separator()
		if err != nil {
			editorTextColored(releaseErrorColor, scoreErrText(err))
		} else {
			editorTextMuted(fmt.Sprintf("%d file(s)", files))
		}
		imgui.SameLine()
		imgui.SetCursorPos(imgui.Vec2{X: max(imgui.CursorPosX(), imgui.WindowWidth()-170), Y: imgui.CursorPosY()})
		if imgui.Button("Cancel##scexpcancel") {
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		ok := err == nil && files > 0 && strings.TrimSpace(d.Dir) != "" && !scoreExportBusy()
		dim := !ok
		if dim {
			imgui.PushStyleVarFloat(imgui.StyleVarAlpha, 0.45)
		}
		if imgui.Button("Export##scexpgo") && ok {
			scoreStartExport()
			imgui.CloseCurrentPopup()
		}
		if dim {
			imgui.PopStyleVar()
		}
	})
}

// scoreExportOptions draws the blocks of the ticked formats.
func scoreExportOptions(e *scoreState, d *scoreExportDlg) {
	if !imgui.BeginTableV("##scexpopts", 2, 0, imgui.Vec2{}, 0) {
		return
	}
	imgui.TableSetupColumnV("label", imgui.TableColumnFlagsWidthFixed, 110, 0)
	imgui.TableSetupColumnV("value", imgui.TableColumnFlagsWidthFixed, 480, 0)
	none := true
	if d.Formats[exportset.PDF] {
		none = false
		r := &d.PDF
		scoreOptHeading("PDF")
		scoreOptLabel("Include")
		imgui.Checkbox("Full score##scexpscore", &r.Score)
		n := 0
		for _, on := range r.Parts {
			if on {
				n++
			}
		}
		all := n == len(r.Parts) && n > 0
		imgui.SameLine()
		if imgui.Checkbox(fmt.Sprintf("Parts (%d)##scexpparts", len(r.Parts)), &all) {
			for i := range r.Parts {
				r.Parts[i] = all
			}
		}
		if len(r.Parts) > 1 {
			for i := range e.Score.Lines {
				if i%3 != 0 {
					imgui.SameLine()
				}
				imgui.Checkbox(fmt.Sprintf("%s##scexpp%d", e.lineName(i), i), &r.Parts[i])
			}
		}
		scoreOptLabel("Paper")
		papers := []string{"", "letter", "a4", "tabloid"}
		cur := 0
		for i, p := range papers {
			if p == r.Paper {
				cur = i
			}
		}
		labels := []string{"As laid out"}
		for _, p := range papers[1:] {
			labels = append(labels, scorePaperLabel(p))
		}
		r.Paper = papers[scorePills("scexppaper", labels, cur, nil)]
		scoreOptLabel("Staff size")
		staff := r.Staff
		if staff == 0 {
			staff = float32(e.Score.Layout.StaffOrDefault())
		}
		imgui.SetNextItemWidth(200)
		if imgui.SliderFloatV("mm##scexpstaff", &staff, 3, 12, "%.1f", 0) {
			r.Staff = float32(math.Round(float64(staff)*10) / 10)
		}
		scoreOptLabel("Score in")
		c := 0
		if r.Concert {
			c = 1
		}
		r.Concert = scorePills("scexpconcert", []string{"Transposed", "Concert pitch"}, c, nil) == 1
		editorMenuTooltip("Parts are always at written pitch, as players read them")
	}
	if d.Formats[exportset.MusicXML] {
		none = false
		scoreOptHeading("MusicXML")
		scoreOptLabel("Layout")
		keep := !d.NoLayout
		if imgui.Checkbox("Page layout and breaks##scexplayout", &keep) {
			d.NoLayout = !keep
		}
		editorMenuTooltip("Paper, margins, staff size and the system and page breaks, so Sibelius lays it out as the page view does")
		imgui.SameLine()
		imgui.Checkbox("Compressed (.mxl)##scexpmxl", &d.Compressed)
	}
	if d.Formats[exportset.MIDI] || d.Formats[exportset.FTRK] {
		none = false
		var names []string
		if d.Formats[exportset.MIDI] {
			names = append(names, "MIDI")
		}
		if d.Formats[exportset.FTRK] {
			names = append(names, "Tracker song")
		}
		scoreOptHeading(strings.Join(names, ", "))
		scoreOptLabel("Repeats")
		played := !d.NoRepeats
		if imgui.Checkbox("Played out##scexprep", &played) {
			d.NoRepeats = !played
		}
		editorMenuTooltip("Off: the score once through as written, every ending, no D.C. or D.S.")
		if d.Formats[exportset.FTRK] {
			scoreOptLabel("Grid")
			cur := 0
			for i, r := range scoreRowChoices {
				if r == d.Rows {
					cur = i
				}
			}
			preview := func(r int32) string {
				if r == 0 {
					return "Automatic (the fewest rows that hold the rhythms)"
				}
				return fmt.Sprintf("%d rows a beat", r)
			}
			imgui.SetNextItemWidth(360)
			if imguibackend.BeginCombo("##scexprows", preview(scoreRowChoices[cur])) {
				for i, r := range scoreRowChoices {
					if imguibackend.SelectableV(preview(r)+fmt.Sprintf("##scexprow%d", i), i == cur, 0, imgui.Vec2{}) {
						d.Rows = r
					}
				}
				imguibackend.EndCombo()
			}
		}
	}
	if afs := d.audioFormats(); len(afs) > 0 {
		none = false
		var names []string
		for _, f := range afs {
			names = append(names, f.Label())
		}
		scoreOptHeading("Audio (" + strings.Join(names, ", ") + ")")
		scoreOptLabel("Range")
		d.What = scoreExportKind(scorePills("scexprange", []string{"Whole score", "Loop region", "Selection"}, int(d.What), func(i int) string {
			if _, _, _, err := e.exportSpan(scoreExportKind(i)); err != nil {
				return scoreErrText(err)
			}
			return ""
		}))
		scoreOptLabel("Files")
		files := 0
		if d.Stems {
			files = 1
		}
		d.Stems = scorePills("scexpstems", []string{"One mix", "Mix + stem per line"}, files, nil) == 1
		scoreOptLabel("Rate")
		rate := 1
		if d.Rate == 44100 {
			rate = 0
		}
		d.Rate = []int{44100, 48000}[scorePills("scexprate", []string{"44.1 kHz", "48 kHz"}, rate, nil)]
		lossless := false
		for _, f := range afs {
			lossless = lossless || f == exportset.WAV || f == exportset.FLAC
		}
		if lossless {
			imgui.SameLine()
			imgui.Text("   ")
			imgui.SameLine()
			bits := 1
			if d.Bits == 16 {
				bits = 0
			}
			d.Bits = []int{16, 24}[scorePills("scexpbits", []string{"16-bit", "24-bit"}, bits, nil)]
		}
		if d.Formats[exportset.OGG] {
			scoreOptLabel("OGG quality")
			imgui.SetNextItemWidth(200)
			imgui.SliderFloatV("##scexpoggq", &d.OGGQuality, -1, 10, "%.1f", 0)
			d.OGGQuality = float32(math.Round(float64(d.OGGQuality)*10) / 10)
			editorMenuTooltip("Vorbis quality: 3 is about 112 kb/s, 6 about 192 kb/s, 10 about 500 kb/s")
		}
		if d.Formats[exportset.MP3] {
			scoreOptLabel("MP3")
			cbr := 0
			if d.MP3CBR {
				cbr = 1
			}
			d.MP3CBR = scorePills("scexpcbr", []string{"VBR", "Constant"}, cbr, nil) == 1
			imgui.SameLine()
			imgui.SetNextItemWidth(160)
			if d.MP3CBR {
				if imguibackend.BeginCombo("##scexpmp3br", fmt.Sprintf("%d kb/s", d.MP3Bitrate)) {
					for _, b := range scoreMP3Bitrates {
						if imguibackend.SelectableV(fmt.Sprintf("%d kb/s##scexpbr%d", b, b), b == d.MP3Bitrate, 0, imgui.Vec2{}) {
							d.MP3Bitrate = b
						}
					}
					imguibackend.EndCombo()
				}
			} else {
				imgui.SliderIntV("##scexpmp3q", &d.MP3Quality, 0, 9, "V%d", 0)
				editorMenuTooltip("LAME's VBR quality: V0 is the best, V2 transparent for most music, V9 the smallest")
			}
		}
		scoreOptLabel("Tail")
		imgui.SetNextItemWidth(160)
		imgui.SliderFloatV("bars##scexptail", &d.TailBars, 0, 8, "%.1f", 0)
		d.TailBars = float32(math.Round(float64(d.TailBars)*2) / 2)
		editorMenuTooltip("Time after the end for releases and reverb, in bars at the end's tempo")
		imgui.SameLine()
		imgui.Checkbox("Fold into the start##scexpfold", &d.Fold)
		editorMenuTooltip("Game audio: the tail is mixed back over the beginning and the file is cut at the end,\nso played on a loop the last bars ring over the first with no gap or click")
		if e.Transport.Scale != 1 {
			scoreOptLabel("Tempo")
			imgui.Checkbox(fmt.Sprintf("At the tempo slider's %.0f%%##scexpscale", e.Transport.Scale*100), &d.Scaled)
		}
		scoreOptLabel("")
		secs := e.exportSeconds(d.What, d.Scaled)
		editorTextMutedWrapped(fmt.Sprintf("%s of music · %s. Mutes and solos are the mixer's.", scoreClock(secs), d.audioSummary()))
		for _, f := range afs {
			if err := audioenc.Available(audioenc.Format(f)); err != nil {
				scoreOptLabel("")
				imgui.PushTextWrapPosV(imgui.CursorPosX() + 470)
				editorTextColored(releaseErrorColor, err.Error())
				imgui.PopTextWrapPos()
			}
		}
		if d.Stems {
			scoreOptLabel("")
			var names []string
			for i := range e.Score.Lines {
				if !play.LineSilenced(e.Score, i) {
					names = append(names, e.lineName(i))
				}
			}
			editorTextMutedWrapped("Stems: " + strings.Join(names, ", "))
		}
	}
	if none {
		scoreOptLabel("")
		editorTextMuted("Tick the formats to export on the left.")
	}
	imgui.EndTable()
}
