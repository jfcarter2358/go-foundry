package editor

// editor_score_import.go — File > Import (mockup screen 5): a MusicXML
// file (what Sibelius exports), a MIDI file or a Tracker song, as a new
// score.
//
//	┌ Import · hangar_sketch.mid ── Standard MIDI file, type 1 · 6 tracks · 412 notes ┐
//	│ Import  Track    Notes  MIDI program        Line      Instrument      Clef      │
//	│  [x]    Flute       96  74 Flute            New line  [Flute       v] Treble    │
//	│  [x]    Piano      120  1 Acoustic Grand    New line  [Grand piano v] Grand ... │
//	│  [ ]    Click       12  Channel 10 (drums)  Skip      [Drum kit    v] Percussion│
//	│ How notes are read                                                              │
//	│  Quantise to   8th (16th) 32nd 64th   [x] allow triplets                        │
//	│  Voices        One voice per staff (Split overlapping notes into voices)        │
//	│  Key signature [G major (from the file)  v]                                     │
//	│  Time signature[4/4 (from the file)      v]                                     │
//	│  Tempo         96 bpm, 2 tempo changes kept as tempo marks                      │
//	│  Dynamics      [x] Turn velocity into dynamic marks                             │
//	│                                               [Cancel] [Import 4 lines]         │
//	└─────────────────────────────────────────────────────────────────────────────────┘
//
// The dialog opens on a file browser (folders and the files it reads);
// choosing one reads it and fills the table with the importer's guesses
// (internal/score/io/midi.Analyze, musicxml.Document.Parts,
// ftrk.Analyze), every one of which can be changed: whether the track is
// imported, its line's name, its library instrument (the type-to-filter
// picker) and, for MIDI and songs, its clef — a grand staff splits at
// middle C. A MusicXML file is already notation, so for one the
// "How notes are read" block is only whether its page layout comes too.
// Importing runs off the frame (the master gain is fitted so the mix does
// not clip, as `foundry score import` does) and opens the result as a new
// score, unsaved: Save names it.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/io/ftrk"
	"foundry/internal/score/io/gm"
	"foundry/internal/score/io/midi"
	"foundry/internal/score/io/musicxml"
	"foundry/internal/score/io/notate"
	"foundry/internal/score/play"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreImportKind is what kind of file is being imported.
type scoreImportKind int

const (
	scoreImportMIDI scoreImportKind = iota
	scoreImportMusicXML
	scoreImportSong
)

// scoreImportRow is one track (a part) of the file and what to do with it.
type scoreImportRow struct {
	Import     bool
	Track      string
	Notes      int
	Program    string
	Name       string
	Instrument string
	// Clef is a MIDI part's: treble, bass, alto, tenor, grand or
	// percussion; Staves a MusicXML part's, as the file has them.
	Clef   string
	Staves string
}

// scoreImportDlg is the dialog's state.
type scoreImportDlg struct {
	Path    string
	Kind    scoreImportKind
	Summary string
	Err     string
	Rows    []scoreImportRow

	// How a MIDI file or a song is read.
	Grid      int // index into scoreGridUnits
	Triplets  bool
	OneVoice  bool
	Key       int // -1 the file's, else index into scoreImportKeys
	Meter     string
	Dynamics  bool
	KeyLabel  string // the file's key, as it says it
	MeterText string // the file's meter
	TempoText string
	// MusicXML.
	KeepLayout bool

	midiA   *midi.Analysis
	midiF   *midi.File
	song    *tracker.Song
	guesses []midi.Choice
	doc     *musicxml.Document
	parts   []musicxml.PartInfo

	browseDir, browsed string
	browseList         []os.DirEntry
	browseErr          string
}

var (
	scoreShowImport   bool
	scoreImp          scoreImportDlg
	scoreImportResult chan scoreImportDone
)

// scoreImportDone is a finished import.
type scoreImportDone struct {
	s     *score.Score
	name  string
	warns []string
	err   error
}

// scoreGridUnits are the quantise pills: 8th, 16th, 32nd, 64th.
var scoreGridUnits = []int64{8, 16, 32, 64}

// scoreImportClefs are the clefs a MIDI part can be given.
var scoreImportClefs = []string{"treble", "bass", "alto", "tenor", "grand", "percussion"}

// scoreImportMeters are the time signatures offered beside the file's.
var scoreImportMeters = []string{"2/4", "3/4", "4/4", "5/4", "6/8", "9/8", "12/8", "2/2", "3/8", "7/8"}

// scoreImportKeys are the key signatures offered: every major, then every
// minor.
var scoreImportKeys = func() []score.KeyMark {
	var out []score.KeyMark
	for _, mode := range []string{"major", "minor"} {
		for f := -7; f <= 7; f++ {
			out = append(out, score.KeyMark{Bar: 1, Fifths: f, Mode: mode})
		}
	}
	return out
}()

// scoreImportable reports a file the dialog reads.
func scoreImportable(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mid", ".midi", ".rmi", ".smf", ".musicxml", ".xml", ".mxl", tracker.FileExt:
		return true
	}
	return false
}

// scoreImportBusy reports an import running.
func scoreImportBusy() bool { return scoreImportResult != nil }

// scoreOpenImport opens the dialog on its browser, where it last was (or
// the project's folder).
func scoreOpenImport() {
	if scoreImp.browseDir == "" {
		scoreImp.browseDir = filepath.Dir(filepath.Clean(scoreImportStartDir()))
	}
	scoreShowImport = true
}

// scoreImportStartDir is where the browser first looks: the project.
func scoreImportStartDir() string {
	if d := filepath.Dir(scoreExportsDir()); d != "" {
		return d
	}
	return "."
}

// scoreImportChoose reads a file and fills the table with the importer's
// guesses. On an error the dialog stays on the browser and says it.
func scoreImportChoose(path string) error {
	d := &scoreImp
	*d = scoreImportDlg{browseDir: d.browseDir, browsed: d.browsed, browseList: d.browseList,
		Path: path, Grid: 1, Triplets: true, Key: -1, Dynamics: true, KeepLayout: true}
	fail := func(err error) error {
		d.Err = err.Error()
		d.Rows = nil
		return err
	}
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".musicxml", ".xml", ".mxl":
		doc, err := musicxml.ReadFile(path)
		if err != nil {
			return fail(err)
		}
		d.Kind, d.doc, d.parts = scoreImportMusicXML, doc, doc.Parts()
		sw := doc.Software
		if sw == "" {
			sw = "unknown software"
		}
		notes := 0
		for _, p := range d.parts {
			notes += p.Notes
			var clefs []string
			for _, c := range p.Clefs {
				clefs = append(clefs, scoreImportClefLabel(c))
			}
			row := scoreImportRow{Import: true, Track: p.Name, Notes: p.Notes, Name: p.Name, Instrument: p.Instrument,
				Staves: strings.Join(clefs, " + ")}
			if p.Program >= 0 {
				row.Program = fmt.Sprintf("%d %s", p.Program+1, gm.Names[p.Program])
			}
			if p.Transpose != (score.Interval{}) {
				row.Staves += " (" + p.Transpose.String() + ")"
			}
			d.Rows = append(d.Rows, row)
		}
		d.Summary = fmt.Sprintf("MusicXML %s from %s · %d part(s) · %d notes", doc.Version, sw, len(d.parts), notes)
	case ".mid", ".midi", ".rmi", ".smf", tracker.FileExt:
		if ext == tracker.FileExt {
			song, err := tracker.LoadFile(path)
			if err != nil {
				return fail(err)
			}
			a, f, guesses, _, err := ftrk.Analyze(song)
			if err != nil {
				return fail(err)
			}
			d.Kind, d.song, d.midiA, d.midiF, d.guesses = scoreImportSong, song, a, f, guesses
			d.Summary = fmt.Sprintf("Tracker song · %d channels · %d patterns · %d notes", len(song.Channels), len(song.Patterns), a.Notes)
		} else {
			f, err := midi.ReadFile(path)
			if err != nil {
				return fail(err)
			}
			a := midi.Analyze(f)
			d.Kind, d.midiA, d.midiF = scoreImportMIDI, a, f
			d.Summary = fmt.Sprintf("Standard MIDI file, type %d · %d tracks · %d notes", a.Format, a.Tracks, a.Notes)
		}
		a := d.midiA
		for i, p := range a.Parts {
			row := scoreImportRow{Import: true, Track: p.Name, Notes: p.Notes, Name: p.Name,
				Instrument: p.Instrument, Clef: p.Clef}
			if i < len(d.guesses) && d.guesses[i].Instrument != "" {
				row.Instrument = d.guesses[i].Instrument
			}
			switch {
			case p.Percussion:
				row.Program = "Channel 10 (drums)"
			case p.Program >= 0:
				row.Program = fmt.Sprintf("%d %s", p.Program+1, gm.Names[p.Program])
			default:
				row.Program = "-"
			}
			d.Rows = append(d.Rows, row)
		}
		how := func(guessed bool) string {
			if guessed {
				return "guessed"
			}
			return "from the file"
		}
		d.KeyLabel = fmt.Sprintf("%s (%s)", scoreKeyName(a.Fifths, map[bool]string{true: "minor", false: "major"}[a.Minor]), how(a.KeyGuessed))
		d.MeterText = fmt.Sprintf("%s (%s)", a.Meter, how(a.MeterGuessed))
		d.TempoText = fmt.Sprintf("%g bpm", float64(int(a.Tempo*10+0.5))/10)
		switch a.TempoChanges {
		case 0:
			d.TempoText += ", steady"
		case 1:
			d.TempoText += ", 1 tempo change kept as a tempo mark"
		default:
			d.TempoText += fmt.Sprintf(", %d tempo changes kept as tempo marks", a.TempoChanges)
		}
	default:
		return fail(fmt.Errorf("%s is not a MusicXML, MIDI or Tracker file", filepath.Base(path)))
	}
	d.browseDir = filepath.Dir(path)
	return nil
}

// lines is how many lines the import makes.
func (d *scoreImportDlg) lines() int {
	n := 0
	for _, r := range d.Rows {
		if r.Import {
			n++
		}
	}
	return n
}

// grid is the quantise grid the pills and the triplet box make.
func (d *scoreImportDlg) grid() notate.Grid {
	g := notate.Grid{Unit: score.F(1, scoreGridUnits[max(0, min(d.Grid, len(scoreGridUnits)-1))])}
	if d.Triplets {
		g.Tuplets = []int{3}
	}
	return g
}

// libraryName is the score's library name: the file's, made a name.
func (d *scoreImportDlg) libraryName() string {
	base := strings.TrimSuffix(filepath.Base(d.Path), filepath.Ext(d.Path))
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case !under && b.Len() > 0:
			b.WriteByte('_')
			under = true
		}
	}
	if n := strings.TrimSuffix(b.String(), "_"); n != "" {
		return n
	}
	return "imported"
}

// run imports with the dialog's choices.
func (d *scoreImportDlg) run() (*score.Score, []string, error) {
	if d.lines() == 0 {
		return nil, nil, fmt.Errorf("no track is ticked to import")
	}
	name := d.libraryName()
	var s *score.Score
	var warns []string
	var err error
	switch d.Kind {
	case scoreImportMusicXML:
		o := musicxml.Options{Name: name, NoLayout: !d.KeepLayout, Choices: make([]musicxml.Choice, len(d.Rows))}
		for i, r := range d.Rows {
			o.Choices[i] = musicxml.Choice{Skip: !r.Import, Name: r.Name, Instrument: r.Instrument}
			if i < len(d.parts) && r.Name == d.parts[i].Name {
				o.Choices[i].Name = ""
			}
		}
		s, warns, err = musicxml.Import(d.doc, o)
	default:
		o := midi.Options{Name: name, Grid: d.grid(), NoDynamics: !d.Dynamics, Choices: make([]midi.Choice, len(d.Rows))}
		if d.OneVoice {
			o.Voices = 1
		}
		if d.Key >= 0 && d.Key < len(scoreImportKeys) {
			k := scoreImportKeys[d.Key]
			o.Key = &k
		}
		if d.Meter != "" {
			m, err := score.ParseMeter(d.Meter)
			if err != nil {
				return nil, nil, err
			}
			o.Meter = &m
		}
		for i, r := range d.Rows {
			o.Choices[i] = midi.Choice{Skip: !r.Import, Name: r.Name, Instrument: r.Instrument, Clef: r.Clef}
		}
		if d.Kind == scoreImportSong {
			s, warns, err = ftrk.Import(d.song, o)
		} else {
			s, warns, err = midi.Import(d.midiF, o)
		}
	}
	if err != nil {
		return nil, warns, err
	}
	// Lines arrive at full volume: lower the master until it does not clip.
	if peak, err := play.FitGain(s, 0.9); err == nil && peak > 0.9 {
		warns = append(warns, fmt.Sprintf("the mix peaked at %.2f; master gain set to %.2f", peak, s.Gain))
	}
	return s, warns, nil
}

// scoreStartImport imports in the background.
func scoreStartImport() {
	if scoreImportBusy() {
		return
	}
	d := scoreImp
	d.Rows = append([]scoreImportRow(nil), scoreImp.Rows...)
	ch := make(chan scoreImportDone, 1)
	scoreImportResult = ch
	scEd.Status = "Importing " + filepath.Base(d.Path) + "..."
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- scoreImportDone{err: fmt.Errorf("%v", r)}
			}
		}()
		s, warns, err := d.run()
		ch <- scoreImportDone{s: s, name: d.libraryName(), warns: warns, err: err}
	}()
}

// scorePollImport opens a finished import as the score being edited.
func scorePollImport() {
	if scoreImportResult == nil {
		return
	}
	select {
	case r := <-scoreImportResult:
		scoreImportResult = nil
		if r.err != nil {
			scEd.Status = "Import failed: " + r.err.Error()
			return
		}
		scoreFinishImport(r.s, r.name, r.warns)
	default:
	}
}

// scoreFinishImport makes an imported score the one being edited: unsaved,
// named after its file when no score has that name yet (else Save asks).
func scoreFinishImport(s *score.Score, name string, warns []string) {
	scoreEnsureDoc()
	scEd.setScore(s)
	scEd.Name = ""
	if score.ScorePath(name) == "" {
		scEd.Name = name
	}
	if scorePlayer != nil {
		scorePlayer.StopTransport()
	}
	scoreSyncPlayer(true)
	scEd.seekTick(0)
	msg := fmt.Sprintf("Imported %q: %d bars, %d lines (not saved yet)", s.Title, s.Measures(), len(s.Lines))
	if len(warns) > 0 {
		msg += fmt.Sprintf("; %d note(s): %s", len(warns), warns[0])
	}
	scEd.Status = msg
}

// scoreImportBrowse lists a folder: its folders and the files it reads.
func scoreImportBrowse(dir string) {
	d := &scoreImp
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	d.browseDir, d.browsed, d.browseList, d.browseErr = dir, dir, nil, ""
	list, err := os.ReadDir(dir)
	if err != nil {
		d.browseErr = err.Error()
		return
	}
	for _, en := range list {
		if strings.HasPrefix(en.Name(), ".") {
			continue
		}
		if en.IsDir() || scoreImportable(en.Name()) {
			d.browseList = append(d.browseList, en)
		}
	}
	sort.SliceStable(d.browseList, func(i, j int) bool {
		a, b := d.browseList[i], d.browseList[j]
		if a.IsDir() != b.IsDir() {
			return a.IsDir()
		}
		return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
	})
}

// scoreImportDialog is the dialog: the browser until a file is read, then
// its table and options.
func scoreImportDialog() {
	d := &scoreImp
	title := "Import###scimport"
	if d.Path != "" && d.Err == "" {
		title = "Import · " + filepath.Base(d.Path) + "###scimport"
	}
	imguibackend.WithModalPopup(title, &scoreShowImport, func() {
		if d.Path == "" || d.Err != "" || d.Rows == nil {
			scoreImportBrowser()
		} else {
			scoreImportTable()
		}
		imgui.Separator()
		ok := d.Path != "" && d.Err == "" && d.lines() > 0 && !scoreImportBusy()
		dim := !ok
		if d.Rows != nil && d.Err == "" {
			if imgui.Button("Choose another file...##scimpother") {
				d.Rows = nil
			}
			imgui.SameLine()
		}
		imgui.SetCursorPos(imgui.Vec2{X: max(imgui.CursorPosX(), imgui.WindowWidth()-230), Y: imgui.CursorPosY()})
		if imgui.Button("Cancel##scimpcancel") {
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if dim {
			imgui.PushStyleVarFloat(imgui.StyleVarAlpha, 0.45)
		}
		label := fmt.Sprintf("Import %d line(s)##scimpgo", d.lines())
		switch {
		case d.Rows == nil || d.Err != "":
			label = "Import##scimpgo"
		case d.lines() == 1:
			label = "Import 1 line##scimpgo"
		}
		if imgui.Button(label) && ok {
			scoreStartImport()
			imgui.CloseCurrentPopup()
		}
		if dim {
			imgui.PopStyleVar()
		}
	})
}

// scoreImportBrowser picks the file.
func scoreImportBrowser() {
	d := &scoreImp
	if d.browsed != d.browseDir || d.browseList == nil && d.browseErr == "" {
		scoreImportBrowse(d.browseDir)
	}
	editorTextMuted("A MusicXML file (.musicxml, .xml, .mxl; from Sibelius: File > Export > MusicXML), a MIDI file (.mid) or a Tracker song (.ftrk)")
	imgui.SetNextItemWidth(640)
	dir := d.browseDir
	if imgui.InputTextV("##scimpdir", &dir, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		if st, err := os.Stat(dir); err == nil && !st.IsDir() && scoreImportable(dir) {
			scoreImportChoose(dir)
			return
		}
		d.browseDir = dir
	}
	for i, r := range folderRoots() {
		if i > 0 {
			imgui.SameLine()
		}
		if imgui.Button(r.label + "##scimproot" + r.path) {
			d.browseDir = r.path
		}
	}
	imgui.SameLine()
	if imgui.Button("Project##scimpproj") {
		d.browseDir = scoreImportStartDir()
	}
	imgui.BeginChildV("##scimplist", imgui.Vec2{X: 640, Y: 320}, true, 0)
	if parent := filepath.Dir(d.browseDir); parent != d.browseDir {
		if imgui.SelectableV(".. (up one folder)##scimpup", false, imgui.SelectableFlagsDontClosePopups, imgui.Vec2{}) {
			d.browseDir = parent
		}
	}
	if d.browseErr != "" {
		editorTextColored(releaseErrorColor, d.browseErr)
	}
	for i, en := range d.browseList {
		full := filepath.Join(d.browseDir, en.Name())
		label := en.Name()
		if en.IsDir() {
			label += "/"
		}
		if imgui.SelectableV(fmt.Sprintf("%s##scimpb%d", label, i), full == d.Path, imgui.SelectableFlagsDontClosePopups, imgui.Vec2{}) {
			if en.IsDir() {
				d.browseDir = full
			} else {
				scoreImportChoose(full)
			}
		}
	}
	if len(d.browseList) == 0 && d.browseErr == "" {
		editorTextMuted("No folders or MusicXML, MIDI or Tracker files here")
	}
	imgui.EndChild()
	if d.Err != "" {
		editorTextColored(releaseErrorColor, d.Err)
	}
}

// scoreImportTable is the tracks and the options.
func scoreImportTable() {
	d := &scoreImp
	editorTextMuted(d.Summary)
	if scoreInstNames == nil {
		scoreInstNames = tracker.ScanInstrumentNames()
	}
	flags := imgui.TableFlagsBorders | imgui.TableFlagsRowBg
	if imgui.BeginTableV("##scimptracks", 7, flags, imgui.Vec2{}, 0) {
		for _, c := range []struct {
			label string
			w     float32
		}{{"Import", 50}, {"Track", 150}, {"Notes", 50}, {"MIDI program", 170}, {"Line", 100}, {"Instrument", 220}, {"Clef", 210}} {
			imgui.TableSetupColumnV(c.label, imgui.TableColumnFlagsWidthFixed, c.w, 0)
		}
		imgui.TableHeadersRow()
		for i := range d.Rows {
			r := &d.Rows[i]
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Checkbox(fmt.Sprintf("##scimpon%d", i), &r.Import)
			imgui.TableNextColumn()
			// The track's name is the line's, to keep or change.
			imgui.SetNextItemWidth(-1)
			imgui.InputText(fmt.Sprintf("##scimpname%d", i), &r.Name)
			if r.Name != r.Track {
				editorMenuTooltip("The file calls it " + r.Track)
			} else {
				editorMenuTooltip("The line's name")
			}
			imgui.TableNextColumn()
			imgui.Text(fmt.Sprint(r.Notes))
			imgui.TableNextColumn()
			imgui.Text(r.Program)
			imgui.TableNextColumn()
			imgui.SetNextItemWidth(-1)
			line := "Skip"
			if r.Import {
				line = "New line"
			}
			if imguibackend.BeginCombo(fmt.Sprintf("##scimpline%d", i), line) {
				if imguibackend.SelectableV(fmt.Sprintf("New line##scimpnew%d", i), r.Import, 0, imgui.Vec2{}) {
					r.Import = true
				}
				if imguibackend.SelectableV(fmt.Sprintf("Skip##scimpskip%d", i), !r.Import, 0, imgui.Vec2{}) {
					r.Import = false
				}
				imguibackend.EndCombo()
			}
			imgui.TableNextColumn()
			imgui.SetNextItemWidth(-1)
			if imguibackend.BeginCombo(fmt.Sprintf("##scimpinst%d", i), scoreInstLabel(r.Instrument)) {
				for k, n := range scoreInstNames {
					if imguibackend.SelectableV(fmt.Sprintf("%s##scimpi%d_%d", n, i, k), n == r.Instrument, 0, imgui.Vec2{}) {
						r.Instrument = n
					}
				}
				imguibackend.EndCombo()
			}
			editorMenuTooltip(r.Instrument)
			imgui.TableNextColumn()
			if d.Kind == scoreImportMusicXML {
				imgui.Text(r.Staves)
				continue
			}
			imgui.SetNextItemWidth(-1)
			if imguibackend.BeginCombo(fmt.Sprintf("##scimpclef%d", i), scoreImportClefLabel(r.Clef)) {
				for _, c := range scoreImportClefs {
					if imguibackend.SelectableV(scoreImportClefLabel(c)+fmt.Sprintf("##scimpc%d%s", i, c), c == r.Clef, 0, imgui.Vec2{}) {
						r.Clef = c
					}
				}
				imguibackend.EndCombo()
			}
		}
		imgui.EndTable()
	}
	if !imgui.BeginTableV("##scimpopts", 2, 0, imgui.Vec2{}, 0) {
		return
	}
	imgui.TableSetupColumnV("label", imgui.TableColumnFlagsWidthFixed, 130, 0)
	imgui.TableSetupColumnV("value", imgui.TableColumnFlagsWidthFixed, 760, 0)
	if d.Kind == scoreImportMusicXML {
		scoreOptHeading("Page layout")
		scoreOptLabel("")
		imgui.Checkbox("Keep the file's paper, staff size and system and page breaks##scimplayout", &d.KeepLayout)
		editorMenuTooltip("Off: the score is laid out its own way (Sibelius writes a break for every system it laid out)")
		imgui.EndTable()
		return
	}
	scoreOptHeading("How notes are read")
	scoreOptLabel("Quantise to")
	d.Grid = scorePills("scimpgrid", []string{"8th", "16th", "32nd", "64th"}, d.Grid, nil)
	imgui.SameLine()
	imgui.Checkbox("allow triplets##scimptrip", &d.Triplets)
	scoreOptLabel("Voices")
	v := 1
	if d.OneVoice {
		v = 0
	}
	d.OneVoice = scorePills("scimpvoices", []string{"One voice per staff", "Split overlapping notes into voices"}, v, nil) == 0
	scoreOptLabel("Key signature")
	imgui.SetNextItemWidth(300)
	keyPreview := d.KeyLabel
	if d.Key >= 0 {
		k := scoreImportKeys[d.Key]
		keyPreview = scoreKeyName(k.Fifths, k.Mode)
	}
	if imguibackend.BeginCombo("##scimpkey", keyPreview) {
		if imguibackend.SelectableV(d.KeyLabel+"##scimpkeyfile", d.Key < 0, 0, imgui.Vec2{}) {
			d.Key = -1
		}
		for i, k := range scoreImportKeys {
			if imguibackend.SelectableV(fmt.Sprintf("%s##scimpkey%d", scoreKeyName(k.Fifths, k.Mode), i), d.Key == i, 0, imgui.Vec2{}) {
				d.Key = i
			}
		}
		imguibackend.EndCombo()
	}
	scoreOptLabel("Time signature")
	imgui.SetNextItemWidth(300)
	meterPreview := d.MeterText
	if d.Meter != "" {
		meterPreview = d.Meter
	}
	if imguibackend.BeginCombo("##scimpmeter", meterPreview) {
		if imguibackend.SelectableV(d.MeterText+"##scimpmeterfile", d.Meter == "", 0, imgui.Vec2{}) {
			d.Meter = ""
		}
		for _, m := range scoreImportMeters {
			if imguibackend.SelectableV(m+"##scimpm"+m, d.Meter == m, 0, imgui.Vec2{}) {
				d.Meter = m
			}
		}
		imguibackend.EndCombo()
	}
	scoreOptLabel("Tempo")
	imgui.Text(d.TempoText)
	scoreOptLabel("Dynamics")
	imgui.Checkbox("Turn velocity into dynamic marks##scimpdyn", &d.Dynamics)
	imgui.EndTable()
}

// scoreInstLabel is a library instrument as the picker shows it: its
// name without the category.
func scoreInstLabel(name string) string {
	if name == "" {
		return "(none)"
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// scoreImportClefLabel is a clef as the table says it.
func scoreImportClefLabel(c string) string {
	switch c {
	case "grand":
		return "Grand staff, split at C4"
	case "":
		return "By range"
	}
	return strings.ToUpper(c[:1]) + strings.ReplaceAll(c[1:], "_", " ")
}
