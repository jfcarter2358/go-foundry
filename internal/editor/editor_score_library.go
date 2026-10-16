package editor

// editor_score_library.go — the instrument library dialog (mockup screen
// 4): every .finst under sounds/instruments/, shared with the Tracker, with
// more room than the staff header's quick picker.
//
//	┌ Instrument for Violin I ───────────────────── sounds/instruments/ ┐
//	│ All        160 │ [search]                  │ Violin Section       │
//	│ Strings     11 │ Violin section  .finst  4 │ File   strings/…     │
//	│ Imported     3 │ Chamber strings SFZ     6 │ Range  G3 – E7       │
//	│ …              │ …                         │ Layers 2 (p / f)     │
//	│                │                           │ ( sustain )( pizz. ) │
//	│                │                           │ ▯▯▯▮▮▮▮▮▮▮▮▯ range   │
//	│                │                           │ [Preview C4]         │
//	├────────────────┴───────────────────────────┴──────────────────────┤
//	│ [Import SFZ / SF2…]                       [Cancel] [Use on Vln I] │
//	└───────────────────────────────────────────────────────────────────┘
//
// Categories are the library's folders; imported libraries land in their
// own ("imported", internal/score/io/sfz). The details are what the
// notation can use: the playable range and clef, the velocity layers and
// round robin, the articulations marks and technique text pick. Preview
// plays the instrument by itself (its own engine on the music bus), so it
// can be heard before it is chosen; Use on swaps the line's instrument the
// way the quick picker does (while it plays, too).
//
// Import SFZ / SF2… browses for a file (folders and .sfz/.sf2 files), lists
// a SoundFont's presets to choose from, and imports in the background
// (internal/score/io/instimport); the new instruments are selected when it
// is done, with what the import could not carry over in the status.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/imguibackend"
	"foundry/internal/score/io/instimport"
	"foundry/internal/score/io/sf2"
	"foundry/internal/score/io/sfz"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreLibEntry is one library instrument as the dialog lists it.
type scoreLibEntry struct {
	Name     string // library name: "strings/violin_section"
	Category string
	Title    string
	// Source is "SFZ" or "SF2" for an imported library, "" for a .finst
	// made here.
	Source string
	Arts   int
	Inst   *tracker.Instrument // not resolved; nil when it will not load
	Err    string
}

// scoreLibrary is the dialog's state.
var scoreLibrary struct {
	show    bool
	open    bool
	line    int  // the line it chooses for
	add     bool // a new line at addAt instead
	addAt   int
	entries []scoreLibEntry
	cat     string // "" is every category
	query   string
	sel     string

	// The import panel.
	importing  bool
	browseDir  string
	browsed    string
	browseList []os.DirEntry
	browseErr  string
	file       string
	presets    []sf2.Preset
	presetOn   []bool
	presetErr  string
	result     chan scoreLibImportResult
	lastImport []string
}

// scoreLibImportResult is a finished background import.
type scoreLibImportResult struct {
	names    []string
	warnings []string
	err      error
}

// scoreOpenLibrary opens the dialog for line li (add: a new line at li).
func scoreOpenLibrary(li int, add bool) {
	L := &scoreLibrary
	L.show, L.line, L.add, L.addAt = true, li, add, li
	L.query, L.importing = "", false
	L.entries = scoreLibScan()
	L.sel = ""
	if l := scEd.line(li); l != nil && !add {
		L.sel = l.Instrument
	}
	L.cat = ""
	if L.sel != "" {
		L.cat = scoreInstCategory(L.sel)
	}
}

// scoreLibScan reads every library instrument's file (samples are not
// loaded), sorted by category and title.
func scoreLibScan() []scoreLibEntry {
	var out []scoreLibEntry
	for _, n := range tracker.ScanInstrumentNames() {
		en := scoreLibEntry{Name: n, Category: scoreInstCategory(n), Title: scoreInstrumentLabel(n)}
		in, err := tracker.LoadInstrumentFile(tracker.InstrumentPath(n))
		if err != nil {
			en.Err = err.Error()
		} else {
			en.Inst = in
			if in.Name != "" {
				en.Title = in.Name
			}
			en.Arts = len(in.Articulations)
			en.Source = scoreLibSource(in)
		}
		out = append(out, en)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}

// scoreLibSource is "SFZ" or "SF2" for an instrument an import made.
func scoreLibSource(in *tracker.Instrument) string {
	d := strings.ToLower(in.Description)
	if !strings.HasPrefix(d, "imported from ") {
		return ""
	}
	switch {
	case strings.Contains(d, ".sf2"):
		return "SF2"
	case strings.Contains(d, ".sfz"):
		return "SFZ"
	}
	return ""
}

// scoreLibCategories are the categories with their counts, "All" first.
func scoreLibCategories(es []scoreLibEntry) (names []string, counts map[string]int) {
	counts = map[string]int{"": len(es)}
	for _, e := range es {
		if counts[e.Category] == 0 {
			names = append(names, e.Category)
		}
		counts[e.Category]++
	}
	return append([]string{""}, names...), counts
}

// scoreLibShown are the entries the list shows: the category's, matching
// the search.
func scoreLibShown(es []scoreLibEntry, cat, query string) []scoreLibEntry {
	var out []scoreLibEntry
	for _, e := range es {
		if cat != "" && e.Category != cat {
			continue
		}
		if !imguibackend.ComboFilterMatches(query, e.Title+" "+e.Name+" "+e.Source) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// scoreLibDetail is the details column's facts about an instrument.
type scoreLibDetail struct {
	File, Range, Layers, RoundRobin, Clef, Gliss, Source string
	Articulations                                        []string
	// Low and High are the range as MIDI keys (0 when it has none).
	Low, High int
	Samples   int
	// Preview is the note Preview plays (MIDI).
	Preview int
}

// scoreLibDescribe reads what the details show from an instrument.
func scoreLibDescribe(name string, in *tracker.Instrument) scoreLibDetail {
	d := scoreLibDetail{File: name + tracker.InstrumentExt, Samples: len(in.Samples), Source: in.Description}
	lo, hi := int(in.Playback.Low), int(in.Playback.High)
	if lo == 0 && hi == 0 {
		lo, hi = 255, 0
		for _, z := range in.Zones {
			lo, hi = min(lo, int(z.Low)), max(hi, int(z.High))
		}
		if hi < lo {
			lo, hi = 0, 0
		}
	}
	toMIDI := func(n int) int { return n + 60 - int(tracker.NoteC4) }
	if hi > 0 {
		d.Low, d.High = toMIDI(lo), toMIDI(hi)
		d.Range = scoreNoteName(lo) + " - " + scoreNoteName(hi)
	} else {
		d.Range = "the whole keyboard"
	}
	// Velocity layers: the most any zone has, named by the dynamic each
	// layer's middle sits at.
	most := 0
	var names []string
	rr := 1
	for _, z := range in.Zones {
		rr = max(rr, len(z.RoundRobin)+1)
		for _, l := range z.Layers {
			rr = max(rr, len(l.RoundRobin)+1)
		}
		if len(z.Layers) > most {
			most = len(z.Layers)
			names = names[:0]
			for _, l := range z.Layers {
				names = append(names, scoreDynName(float32(int(l.VelLow)+int(l.VelHigh))/2/127))
			}
		}
	}
	switch most {
	case 0, 1:
		d.Layers = "1"
	default:
		d.Layers = fmt.Sprintf("%d (%s)", most, strings.Join(names, " / "))
	}
	d.RoundRobin = fmt.Sprint(rr)
	if rr == 1 {
		d.RoundRobin = "none"
	}
	d.Clef = scoreTitle(in.Playback.Clef)
	if d.Clef == "" {
		d.Clef = "Treble"
	}
	switch in.Playback.Gliss {
	case tracker.GlissChromatic:
		d.Gliss = "Chromatic run"
	case tracker.GlissDiatonic:
		d.Gliss = "Diatonic run"
	default:
		d.Gliss = "Continuous slide"
	}
	d.Articulations = []string{"sustain"}
	for _, a := range in.Articulations {
		if a.Name == "sustain" {
			continue
		}
		w := a.Name
		if len(a.Triggers) > 0 && len(a.Triggers[0]) < len(w) {
			w += " (" + a.Triggers[0] + ")"
		}
		d.Articulations = append(d.Articulations, w)
	}
	d.Preview = 60
	if d.High > 0 && (d.Low > 60 || d.High < 60) {
		d.Preview = (d.Low + d.High) / 2
	}
	return d
}

// scoreNoteName is a tracker note as a musician writes it: "C4", "F#3".
func scoreNoteName(n int) string {
	if n <= 0 || n > int(tracker.NoteMax) {
		return "?"
	}
	return strings.ReplaceAll(tracker.NoteName(uint8(n)), "-", "")
}

// ---------- preview ----------

// scorePreviewGen plays one note of an instrument by itself on the music
// bus: an engine of its own, the note held for a second, then its release.
type scorePreviewGen struct {
	eng  *tracker.Engine
	left int // frames until it stops
}

func (p *scorePreviewGen) Render(dst []float32, frames, rate int) bool {
	p.eng.Render(dst, frames, rate)
	p.left -= frames
	return p.left > 0
}

// scorePreviewVoice is the preview playing, stopped by the next.
var scorePreviewVoice audio.VoiceID

// scorePreview plays an instrument's note (MIDI) through its own engine.
func scorePreview(name string, midi int) error {
	in, err := tracker.LoadInstrument(name)
	if err != nil {
		return err
	}
	e := tracker.NewEngine(1)
	e.SetStrip(0, tracker.StripControls{Volume: 0.8})
	pitch := float32(midi - 60 + int(tracker.NoteC4))
	if e.NoteOn(0, in, pitch, tracker.NoteOptions{Velocity: 0.75, Hold: 1}) == 0 {
		return fmt.Errorf("%s plays nothing at %s", name, scoreNoteName(int(pitch)))
	}
	if scorePreviewVoice != 0 {
		audio.Stop(scorePreviewVoice)
	}
	g := &scorePreviewGen{eng: e, left: 48000 * 4}
	scorePreviewVoice = audio.PlayGenerator(g, audio.PlayOptions{Bus: audio.BusMusic, Priority: audio.PriorityHigh})
	return nil
}

// ---------- import ----------

// scoreLibBrowse lists the folders and the importable files in a folder.
func scoreLibBrowse(dir string) {
	L := &scoreLibrary
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	L.browseDir, L.browsed, L.browseList, L.browseErr = dir, dir, nil, ""
	list, err := os.ReadDir(dir)
	if err != nil {
		L.browseErr = err.Error()
		return
	}
	for _, en := range list {
		if strings.HasPrefix(en.Name(), ".") {
			continue
		}
		if en.IsDir() || instimport.Supported(en.Name()) {
			L.browseList = append(L.browseList, en)
		}
	}
	sort.SliceStable(L.browseList, func(i, j int) bool {
		a, b := L.browseList[i], L.browseList[j]
		if a.IsDir() != b.IsDir() {
			return a.IsDir()
		}
		return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
	})
}

// scoreLibChoose picks the file to import: a SoundFont's presets are read
// so they can be chosen.
func scoreLibChoose(path string) {
	L := &scoreLibrary
	L.file, L.presets, L.presetOn, L.presetErr = path, nil, nil, ""
	if !strings.EqualFold(filepath.Ext(path), ".sf2") {
		return
	}
	f, err := sf2.Open(path)
	if err != nil {
		L.presetErr = err.Error()
		return
	}
	L.presets = f.Presets()
	L.presetOn = make([]bool, len(L.presets))
	for i := range L.presetOn {
		L.presetOn[i] = true
	}
}

// scoreLibStartImport imports the chosen file in the background.
func scoreLibStartImport() {
	L := &scoreLibrary
	path := L.file
	o := instimport.Options{}
	if L.presets != nil {
		o.Presets = []int{}
		for i, on := range L.presetOn {
			if on {
				o.Presets = append(o.Presets, L.presets[i].Index)
			}
		}
		if len(o.Presets) == 0 {
			scEd.Status = "Choose at least one preset to import"
			return
		}
	}
	ch := make(chan scoreLibImportResult, 1)
	L.result = ch
	go func() {
		var r scoreLibImportResult
		defer func() {
			// A file the importers did not foresee fails the import, never
			// the editor (this goroutine's panic would take it down).
			if p := recover(); p != nil {
				ch <- scoreLibImportResult{err: fmt.Errorf("the import of %s failed: %v", filepath.Base(path), p)}
			}
		}()
		built, err := instimport.Import(path, o)
		for _, b := range built {
			r.names = append(r.names, b.Name)
			for _, w := range b.Warnings {
				r.warnings = append(r.warnings, b.Name+": "+w)
			}
		}
		r.err = err
		ch <- r
	}()
	scEd.Status = "Importing " + filepath.Base(path) + "…"
}

// scoreLibPollImport takes a finished import: the library is scanned
// again and the first new instrument selected.
func scoreLibPollImport() {
	L := &scoreLibrary
	if L.result == nil {
		return
	}
	select {
	case r := <-L.result:
		L.result = nil
		scoreLibFinish(r)
	default:
	}
}

func scoreLibFinish(r scoreLibImportResult) {
	L := &scoreLibrary
	L.entries = scoreLibScan()
	scoreInstNames = nil
	L.lastImport = r.names
	if len(r.names) > 0 {
		L.sel, L.cat, L.query, L.importing = r.names[0], sfz.ImportedCategory, "", false
	}
	msg := fmt.Sprintf("Imported %d instrument%s into %s/", len(r.names), scorePlural(len(r.names)), sfz.ImportedCategory)
	if len(r.warnings) > 0 {
		msg += fmt.Sprintf(" (%d note%s: %s)", len(r.warnings), scorePlural(len(r.warnings)), r.warnings[0])
	}
	if r.err != nil {
		msg = "Import failed: " + r.err.Error()
		if len(r.names) > 0 {
			msg = fmt.Sprintf("Imported %d, then: %v", len(r.names), r.err)
		}
	}
	scEd.Status = msg
}

// ---------- the dialog ----------

var (
	scoreColLibSel  = imgui.Vec4{X: 0.26, Y: 0.59, Z: 0.98, W: 0.22}
	scoreColTagSFZ  = imgui.Vec4{X: 0.91, Y: 0.76, Z: 0.48, W: 1}
	scoreColTagFile = imgui.Vec4{X: 0.68, Y: 0.71, Z: 0.75, W: 1}
	scoreColKeyIn   = scoreRGB(0x9c, 0xc4, 0xff, 1)
)

// scoreLibraryDialog draws the dialog while it is open.
func scoreLibraryDialog() {
	L := &scoreLibrary
	e := scEd
	scoreLibPollImport()
	if L.show {
		L.show = false
		L.open = true
		imgui.OpenPopup("Instrument Library##sclib")
	}
	imgui.SetNextWindowSizeV(imgui.Vec2{X: 900, Y: 520}, imgui.ConditionAppearing)
	if !imgui.BeginPopupModalV("Instrument Library##sclib", &L.open, 0) {
		return
	}
	title := "Instrument for " + e.lineName(L.line)
	if L.add {
		title = "Add a line"
	}
	imgui.Text(title)
	imgui.SameLineV(imgui.WindowWidth()-170, 0)
	editorTextMuted("sounds/instruments/")
	imgui.Separator()

	footH := imgui.FrameHeightWithSpacing() + 10
	bodyH := imgui.ContentRegionAvail().Y - footH
	if L.importing {
		scoreLibImportPanel(bodyH)
	} else {
		scoreLibColumns(bodyH)
	}
	imgui.Separator()
	busy := L.result != nil
	if L.importing {
		if imgui.Button("Back to the Library##sclibback") {
			L.importing = false
		}
	} else if imgui.Button("Import SFZ / SF2...##sclibimp") {
		L.importing = true
		if L.browseDir == "" {
			home, _ := os.UserHomeDir()
			L.browseDir = home
		}
		scoreLibBrowse(L.browseDir)
	}
	editorMenuTooltip("Import a sample library: an .sfz (with its samples) or a SoundFont .sf2,\ninto sounds/instruments/imported/ and sounds/samples/imported/")
	imgui.SameLineV(imgui.WindowWidth()-260, 0)
	if imgui.Button("Cancel##sclibcancel") {
		imgui.CloseCurrentPopup()
		L.open = false
	}
	imgui.SameLine()
	use := "Use on " + e.lineName(L.line)
	if L.add {
		use = "Add the Line"
	}
	if L.importing {
		label := "Import##sclibgo"
		if busy {
			label = "Importing...##sclibgo"
		}
		if imgui.Button(label) && !busy && L.file != "" {
			scoreLibStartImport()
		}
	} else if imgui.Button(use+"##sclibuse") && L.sel != "" {
		scoreLibUse(L.sel)
		imgui.CloseCurrentPopup()
		L.open = false
	}
	imgui.EndPopup()
}

// scoreLibUse puts the chosen instrument on the line (or adds the line).
func scoreLibUse(name string) {
	L := &scoreLibrary
	e := scEd
	if L.add {
		e.addLine(L.addAt, name)
		return
	}
	e.setInstrument(L.line, name, scorePlaying())
	if !scorePlaying() {
		e.auditions = append(e.auditions, scoreAudition{Line: L.line, MIDI: scorePickAudition(L.line)})
	}
}

// scoreLibColumns are the categories, the list and the details.
func scoreLibColumns(h float32) {
	L := &scoreLibrary
	cats, counts := scoreLibCategories(L.entries)
	imgui.BeginChildV("##sclibcats", imgui.Vec2{X: 170, Y: h}, true, 0)
	for _, c := range cats {
		label := "All"
		switch {
		case c == "fx":
			label = "FX"
		case c != "":
			label = scoreTitle(c)
		}
		if imgui.SelectableV(fmt.Sprintf("%s##sclibc%s", label, c), L.cat == c, imgui.SelectableFlagsDontClosePopups, imgui.Vec2{}) {
			L.cat = c
		}
		n := fmt.Sprint(counts[c])
		imgui.SameLineV(170-imgui.CalcTextSize(n, false, 0).X-18, 0)
		editorTextMuted(n)
	}
	imgui.EndChild()
	imgui.SameLine()

	imgui.BeginChildV("##sclibmid", imgui.Vec2{X: 380, Y: h}, true, 0)
	imgui.SetNextItemWidth(-1)
	imgui.InputTextWithHint("##sclibq", "Search the library", &L.query)
	shown := scoreLibShown(L.entries, L.cat, L.query)
	if len(L.entries) == 0 {
		editorTextMuted("No instruments under sounds/instruments/.\n`foundry tracker gen-instruments` writes the starter library.")
	}
	for _, en := range shown {
		y := imgui.CursorPosY()
		if imgui.SelectableV(fmt.Sprintf("%s##sclibi%s", en.Title, en.Name), L.sel == en.Name, imgui.SelectableFlagsDontClosePopups|imgui.SelectableFlagsAllowDoubleClick, imgui.Vec2{}) {
			L.sel = en.Name
			if imgui.IsMouseDoubleClicked(0) {
				scoreLibUse(en.Name)
				imgui.CloseCurrentPopup()
				L.open = false
			}
		}
		imgui.SetCursorPos(imgui.Vec2{X: 230, Y: y})
		switch {
		case en.Err != "":
			editorTextColored(imgui.Vec4{X: 0.9, Y: 0.4, Z: 0.35, W: 1}, "error")
		case en.Source != "":
			editorTextColored(scoreColTagSFZ, en.Source)
		default:
			editorTextColored(scoreColTagFile, ".finst")
		}
		imgui.SetCursorPos(imgui.Vec2{X: 290, Y: y})
		editorTextMuted(fmt.Sprintf("%d artic.", en.Arts+1))
	}
	if len(shown) == 0 && len(L.entries) > 0 {
		editorTextMuted("No instrument matches")
	}
	imgui.EndChild()
	imgui.SameLine()

	imgui.BeginChildV("##sclibdet", imgui.Vec2{X: 0, Y: h}, true, 0)
	var sel *scoreLibEntry
	for i := range L.entries {
		if L.entries[i].Name == L.sel {
			sel = &L.entries[i]
		}
	}
	if sel == nil {
		editorTextMuted("Choose an instrument")
	} else if sel.Inst == nil {
		imgui.Text(sel.Title)
		editorTextMutedWrapped(sel.Err)
	} else {
		scoreLibDetails(*sel)
	}
	imgui.EndChild()
}

// scoreLibDetails is the details column for an instrument.
func scoreLibDetails(en scoreLibEntry) {
	d := scoreLibDescribe(en.Name, en.Inst)
	imgui.Text(en.Title)
	imgui.Spacing()
	kv := func(k, v string) {
		// A value too long for its column (an imported instrument's file)
		// goes on its own line rather than under the panel's edge.
		fits := imgui.CalcTextSize(v, false, 0).X+124 <= imgui.ContentRegionAvail().X
		editorTextMuted(k)
		if fits {
			imgui.SameLineV(124, 0)
		} else {
			imgui.Indent()
			defer imgui.Unindent()
		}
		imgui.Text(v)
	}
	kv("File", d.File)
	kv("Range", d.Range)
	kv("Velocity layers", d.Layers)
	kv("Round robin", d.RoundRobin)
	kv("Clef", d.Clef)
	kv("Gliss", d.Gliss)
	kv("Samples", fmt.Sprint(d.Samples))
	if d.Source != "" {
		editorTextMutedWrapped(d.Source)
	}
	imgui.Spacing()
	editorTextMuted("Articulations the notation picks")
	dl := imgui.WindowDrawList()
	x0 := imgui.CursorScreenPos().X
	maxX := x0 + imgui.ContentRegionAvail().X
	at := imgui.CursorScreenPos()
	for i, a := range d.Articulations {
		tw := imgui.CalcTextSize(a, false, 0).X + 14
		if at.X+tw > maxX && at.X > x0 {
			at = imgui.Vec2{X: x0, Y: at.Y + 22}
		}
		col := scoreColFrame
		if i == 0 {
			col = scoreRGB(0x42, 0x96, 0xfa, 0.3)
		}
		dl.AddRectFilledV(at, imgui.Vec2{X: at.X + tw, Y: at.Y + 18}, col, 9, imgui.DrawFlagsRoundCornersAll)
		dl.AddText(imgui.Vec2{X: at.X + 7, Y: at.Y + 2}, scoreColText, a)
		at.X += tw + 4
	}
	imgui.SetCursorScreenPos(imgui.Vec2{X: x0, Y: at.Y + 26})
	// The keyboard, the playable keys lit.
	kp := imgui.CursorScreenPos()
	w := imgui.ContentRegionAvail().X
	const lo, hi = 21, 108
	whites := 0
	for m := lo; m <= hi; m++ {
		if !scoreBlackKey(m) {
			whites++
		}
	}
	kw := w / float32(whites)
	k := 0
	for m := lo; m <= hi; m++ {
		if scoreBlackKey(m) {
			continue
		}
		col := scoreColKeyWhite
		if d.High > 0 && m >= d.Low && m <= d.High || d.High == 0 {
			col = scoreColKeyIn
		}
		a := imgui.Vec2{X: kp.X + float32(k)*kw, Y: kp.Y}
		dl.AddRectFilled(a, imgui.Vec2{X: a.X + kw - 0.5, Y: a.Y + 34}, col)
		if m == 60 {
			dl.AddCircleFilled(imgui.Vec2{X: a.X + kw/2, Y: a.Y + 28}, 2, scoreColEdge)
		}
		k++
	}
	imgui.Dummy(imgui.Vec2{X: w, Y: 40})
	if imgui.Button(fmt.Sprintf("Preview %s##sclibprev", strings.ReplaceAll(tracker.NoteName(uint8(d.Preview-60+int(tracker.NoteC4))), "-", ""))) {
		if err := scorePreview(en.Name, d.Preview); err != nil {
			scEd.Status = "Preview: " + err.Error()
		}
	}
	editorMenuTooltip("Hear the instrument by itself (the line keeps its own until you choose)")
}

// scoreLibImportPanel is the import's file browser and, for a SoundFont,
// its presets.
func scoreLibImportPanel(h float32) {
	L := &scoreLibrary
	if L.browsed != L.browseDir {
		scoreLibBrowse(L.browseDir)
	}
	imgui.BeginChildV("##sclibbrowse", imgui.Vec2{X: 520, Y: h}, true, 0)
	imgui.SetNextItemWidth(-1)
	dir := L.browseDir
	if imgui.InputTextV("##sclibdir", &dir, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		if st, err := os.Stat(dir); err == nil && !st.IsDir() && instimport.Supported(dir) {
			scoreLibChoose(dir)
			dir = filepath.Dir(dir)
		}
		L.browseDir = dir
	}
	for i, r := range folderRoots() {
		if i > 0 {
			imgui.SameLine()
		}
		if imgui.Button(r.label + "##sclibroot" + r.path) {
			L.browseDir = r.path
		}
	}
	if parent := filepath.Dir(L.browseDir); parent != L.browseDir {
		if imgui.SelectableV(".. (up one folder)##sclibup", false, imgui.SelectableFlagsDontClosePopups, imgui.Vec2{}) {
			L.browseDir = parent
		}
	}
	if L.browseErr != "" {
		editorTextColored(releaseErrorColor, L.browseErr)
	}
	for i, en := range L.browseList {
		full := filepath.Join(L.browseDir, en.Name())
		label := en.Name()
		if en.IsDir() {
			label += "/"
		}
		if imgui.SelectableV(fmt.Sprintf("%s##sclibb%d", label, i), full == L.file, imgui.SelectableFlagsDontClosePopups, imgui.Vec2{}) {
			if en.IsDir() {
				L.browseDir = full
			} else {
				scoreLibChoose(full)
			}
		}
	}
	if len(L.browseList) == 0 && L.browseErr == "" {
		editorTextMuted("No folders or .sfz / .sf2 files here")
	}
	imgui.EndChild()
	imgui.SameLine()
	imgui.BeginChildV("##sclibimpdet", imgui.Vec2{X: 0, Y: h}, true, 0)
	switch {
	case L.file == "":
		editorTextMutedWrapped("Choose an .sfz or .sf2 file. An .sfz becomes one instrument (its keyswitches its articulations); a SoundFont one instrument a preset. They go into the Imported category, their samples beside the library's.")
	default:
		imgui.Text(filepath.Base(L.file))
		editorTextMutedWrapped(filepath.Dir(L.file))
		if L.presetErr != "" {
			editorTextColored(releaseErrorColor, L.presetErr)
		}
		if L.presets != nil {
			editorTextMuted(fmt.Sprintf("%d presets", len(L.presets)))
			if imgui.Button("All##sclibpall") {
				for i := range L.presetOn {
					L.presetOn[i] = true
				}
			}
			imgui.SameLine()
			if imgui.Button("None##sclibpnone") {
				for i := range L.presetOn {
					L.presetOn[i] = false
				}
			}
			imgui.BeginChildV("##sclibpresets", imgui.Vec2{X: 0, Y: max(h-130, 60)}, false, 0)
			for i, p := range L.presets {
				imgui.Checkbox(fmt.Sprintf("%03d:%03d  %s##sclibp%d", p.Bank, p.Program, p.Name, i), &L.presetOn[i])
			}
			imgui.EndChild()
		} else if strings.EqualFold(filepath.Ext(L.file), ".sfz") {
			editorTextMuted("imports as " + sfz.ImportedCategory + "/" + sfz.Slug(strings.TrimSuffix(filepath.Base(L.file), filepath.Ext(L.file))))
		}
		if L.result != nil {
			editorTextMuted("Importing...")
		}
	}
	imgui.EndChild()
}
