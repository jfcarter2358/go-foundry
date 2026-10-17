package editor

// editor_score_gl_test.go — the Score window drawn through a real context:
// the glyph atlas on the GPU, the score on paper, the toolbar's glyph
// buttons and the keypad, read back as pixels.
//
//	FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
//	SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
//
// The second form also writes the frame as a PNG, which is how the window's
// look is checked by eye without an editor session.

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/glext"
	"foundry/internal/gltest"
	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/play"
	"foundry/internal/score/scoredraw"
	"foundry/internal/testenv"
	"foundry/internal/tracker/fx"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.3-core/gl"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

func TestScoreGLWindowDraws(t *testing.T) {
	gltest.Context(t)
	prevRes := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	prevEd, prevRend, prevWin, prevKeys := scEd, scoreRend, scoreEditorWindow, scoreKeys
	t.Cleanup(func() {
		fileutil.ResourcesPath = prevRes
		scEd, scoreRend, scoreEditorWindow, scoreKeys = prevEd, prevRend, prevWin, prevKeys
		scoreList = nil
	})
	s, err := score.LoadFile(filepath.Join(fileutil.ResourcesPath, "sounds", "score_demo.fscore"))
	if err != nil {
		t.Fatal(err)
	}
	scEd = newScoreState(s)
	scoreKeys = scoreSibeliusKeymap()
	scoreRend = scoredraw.New()
	defer scoreRend.Release()
	const W, H = 1400, 900
	scoreEditorWindow = &uiwin.UIWindow{W: W, H: H}
	scoreShowKeypad = true
	scoreZoom = 9

	prevCtx, hadCtx := imgui.CurrentContext()
	imguibackend.ImGuiInit()
	ctx, _ := imgui.CurrentContext()
	defer func() {
		ctx.Destroy()
		if hadCtx == nil && prevCtx != nil {
			prevCtx.SetCurrent()
		}
	}()
	var vao, vbo, ebo uint32
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.GenBuffers(1, &ebo)

	var fbo, rbo uint32
	gl.GenFramebuffers(1, &fbo)
	gl.GenRenderbuffers(1, &rbo)
	gl.BindRenderbuffer(gl.RENDERBUFFER, rbo)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.RGBA8, W, H)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.RENDERBUFFER, rbo)
	defer func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteRenderbuffers(1, &rbo)
	}()

	// A selection, note input with the pointer over the flute's staff (the
	// ghost), and a note of the violins selected.
	scEd.clickBar(2, 5, false, false)
	scEd.clickBar(3, 6, false, true)
	l := scoreLayout()
	scEd.selectEvent(edit.Loc{Line: 0, Bar: 5, Event: 1}, -1)
	scEd.Input = true
	sb := l.Staves[0]
	bb := l.Bar(3)
	mouse := imgui.Vec2{X: scoreHeaderW + scorePad + float32(bb.XAt(0.25))*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(sb.Top+0.5)*scoreZoom}

	frame := func() {
		io := imgui.CurrentIO()
		io.SetDisplaySize(imgui.Vec2{X: W, Y: H})
		io.SetDeltaTime(1.0 / 60)
		io.SetMousePosition(mouse)
		imgui.NewFrame()
		scoreDrawFrame(W, H)
		imgui.Render()
		gl.Viewport(0, 0, W, H)
		gl.ClearColor(0.09, 0.10, 0.12, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT)
		imguibackend.ImguiRenderDrawData(imgui.RenderedDrawData(), vao, vbo, ebo)
	}
	frame()
	frame()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("GL error 0x%x", e)
	}
	img := readFrame(W, H)
	shot(t, "", img)
	// The paper is paper, and there is ink on it: dark pixels inside the
	// music area, on the first staff's lines.
	paper, ink := 0, 0
	y0 := int(scoreViewTop+scoreRulerH+scorePad) + int(float32(sb.Top)*scoreZoom)
	for y := y0; y < y0+int(4*scoreZoom); y++ {
		for x := int(scoreHeaderW) + 20; x < 900; x++ {
			c := img.RGBAAt(x, y)
			switch {
			case c.R > 240 && c.G > 240 && c.B > 230:
				paper++
			case c.R < 110 && c.G < 110 && c.B < 120:
				ink++
			}
		}
	}
	if paper < 1000 || ink < 200 {
		t.Errorf("first staff: %d paper pixels, %d ink pixels", paper, ink)
	}

	// The mixer tab: a strip per line, the buses and the master. The
	// faders' caps are light on the dark strips.
	scoreView = scoreViewMixer
	defer func() { scoreView = scoreViewScore }()
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "mixer", img)
	light := 0
	for y := int(scoreViewTop) + 150; y < H-60; y++ {
		for x := 40; x < 70; x++ {
			if c := img.RGBAAt(x, y); c.R > 200 && c.G > 200 && c.B > 200 {
				light++
			}
		}
	}
	if light < 40 {
		t.Errorf("the first strip's fader cap: %d light pixels", light)
	}

	// Effects: an EQ, a compressor (bypassed) and a delay on the flute, a
	// pre-fader reverb send, a chorus on bus A, a limiter on the master,
	// and the EQ's settings open.
	undoTo := scEd.undo.len()
	scEd.addFX(0, fx.KindEQ)
	scEd.setFXParam(0, 0, "low", -4)
	scEd.setFXParam(0, 0, "b2", 3)
	scEd.addFX(0, fx.KindCompressor)
	scEd.toggleFXBypass(0, 1)
	scEd.addFX(0, fx.KindDelay)
	scEd.togglePre(0, 0)
	scEd.addFX(scoreFXBusA, fx.KindChorus)
	scEd.addFX(scoreFXMaster, fx.KindLimiter)
	scoreOpenFXEdit(0, 0, imgui.Vec2{X: 140, Y: scoreViewTop + 70})
	frame()
	frame()
	shot(t, "effects", readFrame(W, H))
	for scEd.undo.len() > undoTo {
		scEd.undoStep()
	}
	frame()

	// Swing and Humanise, for the score: 62% on eighths, ±8 ms.
	scEd.setSwing(-1, score.Swing{Amount: 0.62, Unit: 8})
	scEd.setHumanize(-1, score.Humanize{Timing: 8, Velocity: 0.06, Seed: 3})
	scEd.endMixGesture()
	scoreOpenFeel()
	frame()
	frame()
	shot(t, "feel", readFrame(W, H))
	// A click on the mixer's empty floor closes the popup.
	was := mouse
	mouse = imgui.Vec2{X: 1200, Y: 760}
	imgui.CurrentIO().SetMouseButtonDown(0, true)
	frame()
	imgui.CurrentIO().SetMouseButtonDown(0, false)
	frame()
	mouse = was
	for scEd.undo.len() > undoTo {
		scEd.undoStep()
	}
	frame()

	// A loop over bars 3-4, a drum kit added on a percussion staff with a
	// beat in it, the play head in bar 2 and the tempo slider at 75%.
	scoreView = scoreViewScore
	scEd.Input = false
	scEd.Sel = scoreSel{}
	scEd.insertLine(len(scEd.Score.Lines), scorePercussionLine())
	kit := len(scEd.Score.Lines) - 1
	for i, d := range []string{"| eF#2+C#3 eF#2 eF#2+D2 eF#2 eF#2 eA#2 eF#2+D2 eF#2 |", "| qC2 qr qC2 qr |"} {
		v, err := score.ParseVoice(d)
		if err != nil {
			t.Fatal(err)
		}
		st := scEd.Score.Lines[kit].Staves[0]
		for len(st.Voices) <= i {
			st.Voices = append(st.Voices, &score.Voice{})
		}
		st.Voices[i] = v
	}
	scEd.touch()
	scEd.setLoop(scoreLoopBars(scEd.Score, 3, 4))
	scEd.seek(score.Pos{Bar: 2, Offset: score.F(1, 4)})
	scEd.setTempoScale(0.75)
	scEd.takeOps()
	mouse = imgui.Vec2{X: 5, Y: 5}
	frame()
	frame()
	shot(t, "loop", readFrame(W, H))

	// Zoomed out: the headers fold to a name and their buttons.
	scoreZoom = 5
	scoreList = nil
	frame()
	frame()
	shot(t, "small", readFrame(W, H))

	// Playing, with the real library's instruments rendered offline (there
	// is no device): the meters move and the play head is the player's.
	scoreZoom = 9
	scoreList = nil
	scEd.setLoop(scoreLoop{})
	scEd.takeOps()
	p, errs := play.New(scEd.Score.Clone())
	if p == nil {
		t.Fatal(errs)
	}
	prevPlayer := scorePlayer
	scorePlayer = p
	defer func() { scorePlayer, scoreSentMix, scoreSentMute, scoreMeterVals = prevPlayer, nil, nil, nil }()
	scoreAdopted()
	p.TempoScale(1, 0)
	p.PlayFrom(0)
	buf := make([]float32, 480*2)
	for i := 0; i < 350; i++ { // 3.5 s: into bar 2 at 92 bpm
		p.Render(buf, 480, 48000)
	}
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "playing", img)
	if pos := p.Position(); !pos.Playing || pos.Bar != 2 {
		t.Errorf("the player is at %+v, want playing in bar 2", pos)
	}
	// The piano is playing in bar 2: its header meter (the column's right
	// edge) has green in it.
	pl := scoreLayout()
	lo, hi, ok := scoreLineBand(pl, 4)
	if !ok {
		t.Fatal("no piano on the layout")
	}
	green := 0
	mx := int(scoreHeaderW) - 12
	for y := int(scoreViewTop + scoreRulerH + scorePad + float32(lo)*scoreZoom); y < int(scoreViewTop+scoreRulerH+scorePad+float32(hi)*scoreZoom); y++ {
		for x := mx; x < mx+4; x++ {
			if c := img.RGBAAt(x, y); c.G > 140 && c.R < 80 {
				green++
			}
		}
	}
	if green < 20 {
		t.Errorf("the piano's meter while it plays: %d green pixels", green)
	}

	// Expression: the first line's dynamics lane open with an override
	// over bars 2-3, the marks palette up.
	p.StopTransport()
	scEd.toggleLane(0)
	scEd.lanePress(0, score.Pos{Bar: 2}, 0.3, -1)
	scEd.laneMove(score.Pos{Bar: 4}, 0.3)
	scEd.laneRelease()
	// And the violins', whose marks move (pp, then p at bar 5), held loud
	// over bar 4.
	scEd.toggleLane(2)
	scEd.lanePress(2, score.Pos{Bar: 4}, 0.85, -1)
	scEd.laneMove(score.Pos{Bar: 5}, 0.85)
	scEd.laneRelease()
	scoreShowKeypad, scoreShowMarks = false, true
	defer func() { scoreShowMarks = false }()
	scoreZoom = 10
	scoreList = nil
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "expression", img)
	el := scoreLayout()
	if len(el.Lanes) != 2 {
		t.Fatalf("lanes %+v", el.Lanes)
	}
	// The lane's pale blue, and the override's shading, under the flute
	// (left of the palette).
	ln := el.Lanes[0]
	lane, held := 0, 0
	ly := int(scoreViewTop + scoreRulerH + scorePad + float32(ln.Top)*scoreZoom)
	for y := ly; y < ly+int(float32(ln.Height)*scoreZoom); y++ {
		for x := int(scoreHeaderW) + 20; x < 740; x++ {
			c := img.RGBAAt(x, y)
			switch {
			case c.R > 230 && c.G > 238 && c.B > 245 && c.B > c.R:
				lane++
			case c.B > 200 && c.R < 215 && c.B-c.R > 15:
				held++
			}
		}
	}
	if lane < 1500 || held < 300 {
		t.Errorf("the dynamics lane: %d lane pixels, %d override pixels", lane, held)
	}

	// The Timeline: the flute's volume lane (a swell, a step down), a
	// low-pass on the violins with its cutoff drawn, the piano roll of the
	// violins with two of their notes selected, and the play head in bar 3.
	scoreShowMarks = false
	scoreView = scoreViewTimeline
	scEd.Sel = scoreSel{}
	scEd.autoAdd(0, score.LaneTarget{Kind: score.LaneVolume})
	scEd.autoPress(0, "volume", score.Pos{Bar: 3}, 1.25, -1)
	scEd.autoRelease()
	scEd.autoPress(0, "volume", score.Pos{Bar: 5}, 0.5, -1)
	scEd.autoRelease()
	scEd.autoShape(0, "volume", 1, score.LaneStep, 0)
	scEd.autoAdd(2, score.LaneTarget{Kind: score.LanePan})
	scEd.autoPress(2, "pan", score.Pos{Bar: 2}, -0.6, -1)
	scEd.autoRelease()
	scEd.autoPress(2, "pan", score.Pos{Bar: 6}, 0.6, -1)
	scEd.autoRelease()
	scEd.autoShape(2, "pan", 1, score.LaneCurve, 0.7)
	scEd.addFX(2, fx.KindFilter)
	cut, _ := scEd.Score.Lines[2].LaneTargetForSlot(len(scEd.Score.Lines[2].Inserts)-1, fx.KindFilter.Spec().Param("cutoff"))
	if !scEd.autoAdd(2, cut) {
		t.Fatalf("cutoff lane: %s", scEd.Status)
	}
	scEd.autoPress(2, cut.Name(), score.Pos{Bar: 4}, 600, -1)
	scEd.autoRelease()
	scEd.TL.RollLine = 2
	scoreRollTopFor = -1
	if ns := scEd.rollNotes(); len(ns) > 3 {
		scEd.rollSelect(2, ns[2:4], false)
		scEd.Sel = scoreSel{}
	}
	scEd.seek(score.Pos{Bar: 3, Offset: score.F(1, 2)})
	scEd.takeOps()
	scoreTLZoom = 110
	// Scrolled to the violins' lanes, the cutoff's among them.
	scoreTLTrackScroll = 2 * scoreTLRowH
	defer func() { scoreView, scoreRollTopFor, scoreTLTrackScroll = scoreViewScore, -1, 0 }()
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "timeline", img)
	// The violins' notes are on the roll in voice 1's blue.
	tg := scoreTimelineGeom(imgui.Vec2{X: 0, Y: scoreViewTop}, imgui.Vec2{X: W, Y: H - scoreViewTop - scoreStatusH})
	blue := 0
	for y := int(tg.rollTop); y < int(tg.rollBottom); y++ {
		for x := int(tg.gridX); x < W; x++ {
			if c := img.RGBAAt(x, y); c.B > 200 && c.R < 120 && c.G > 120 {
				blue++
			}
		}
	}
	if blue < 400 {
		t.Errorf("the roll's notes: %d blue pixels", blue)
	}
	// The mix's tracks under the lines: bus A's return riding down, the
	// master's volume fading out at the end, scrolled to the bottom.
	if !scEd.autoAdd(score.OwnerBusA, score.LaneTarget{Kind: score.LaneReturn}) || !scEd.autoAdd(score.OwnerMaster, score.LaneTarget{Kind: score.LaneVolume}) {
		t.Fatalf("mix lanes: %s", scEd.Status)
	}
	scEd.autoPress(score.OwnerBusA, "return", score.Pos{Bar: 4}, 0.1, -1)
	scEd.autoRelease()
	scEd.autoPress(score.OwnerMaster, "volume", score.Pos{Bar: 7}, 1, -1)
	scEd.autoRelease()
	scEd.autoPress(score.OwnerMaster, "volume", score.Pos{Bar: 9}, 0, -1)
	scEd.autoRelease()
	scEd.autoShape(score.OwnerMaster, "volume", 1, score.LaneCurve, -0.6)
	rows := scoreTLRows(scEd)
	scoreTLTrackScroll = 1e6 // scoreTLClampView brings it to the bottom
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "timeline_mix", img)
	if last := rows[len(rows)-1]; last.line != score.OwnerMaster || last.lane != "volume" {
		t.Errorf("the last row is %+v, not the master's volume lane", last)
	}

	// The instrument library, the violins' instrument selected.
	scoreView = scoreViewScore
	scoreOpenLibrary(2, false)
	frame()
	frame()
	shot(t, "library", readFrame(W, H))
	// The kit's notation settings open: its drum map.
	scoreLibrary.sel, scoreLibrary.cat, scoreLibrary.openEdit = "percussion/drum_kit", "percussion", true
	frame()
	frame()
	shot(t, "library_edit", readFrame(W, H))
	scoreLibrary.edit = nil
	// Its import panel, browsing a folder with an .sfz chosen.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "samples"), 0o755)
	os.WriteFile(filepath.Join(dir, "Chamber Strings.sfz"), []byte("<region> sample=samples/a.wav"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a library"), 0o644)
	scoreLibrary.importing, scoreLibrary.browseDir = true, dir
	scoreLibChoose(filepath.Join(dir, "Chamber Strings.sfz"))
	frame()
	frame()
	shot(t, "import", readFrame(W, H))
	scoreLibrary.importing = false
	imgui.CloseCurrentPopup()
	scoreLibrary.open = false
	frame()

	// The page view: the demo on paper, a note selected; then the violins'
	// part with its page setup open.
	prevPage, prevPart, prevZoom := scorePageView, scorePagePart, scorePageZoom
	defer func() { scorePageView, scorePagePart, scorePageZoom = prevPage, prevPart, prevZoom }()
	scoreView, scorePageView, scorePagePart, scorePageZoom = scoreViewScore, true, -1, 5.5
	scEd.Input = false
	scEd.selectEvent(edit.Loc{Line: 0, Bar: 2, Event: 0}, -1)
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "pages", img)
	// Paper on the desk: the page's middle is paper, beside it the desk.
	if c := img.RGBAAt(W/2, int(scoreViewTop+scorePageStripH)+60); c.R < 240 || c.G < 240 {
		t.Errorf("no paper in the page view's middle: %v", c)
	}
	scorePagePart = 2
	scoreShowPageSetup = true
	frame()
	frame()
	shot(t, "page_setup", readFrame(W, H))
	scoreShowPageSetup = false
	imgui.CloseCurrentPopup()
	frame()
	scorePageView = false
	// The Export dialog as the mockup has it: PDF, MusicXML and OGG, a mix
	// and a stem per line.
	scoreExp = defaultExportDlg()
	scoreOpenExport()
	scoreExp.Stems = true
	for i := 0; i < 4; i++ {
		frame()
	}
	shot(t, "export", readFrame(W, H))
	scoreShowExport = false
	imgui.CloseCurrentPopup()
	frame()
	// The Import dialog: its browser, then a MIDI file's tracks and a
	// MusicXML file's parts.
	repo := testenv.RepoRoot(t)
	scoreImp = scoreImportDlg{browseDir: filepath.Join(repo, "internal", "score", "io", "midi", "testdata")}
	scoreOpenImport()
	frame()
	frame()
	shot(t, "import_browse", readFrame(W, H))
	for _, f := range []struct{ name, path string }{
		{"import_midi", filepath.Join(repo, "internal", "score", "io", "midi", "testdata", "format1_orchestra.mid")},
		{"import_musicxml", filepath.Join(repo, "internal", "score", "io", "musicxml", "testdata", "sibelius_piano.musicxml")},
	} {
		if err := scoreImportChoose(f.path); err != nil {
			t.Fatal(err)
		}
		scoreShowImport = true
		frame()
		frame()
		shot(t, f.name, readFrame(W, H))
	}
	scoreShowImport = false
	imgui.CloseCurrentPopup()
	frame()

	// Words and strings: a sung line with chord symbols over it, a guitar
	// with tablature linked under the notation, a bass's tablature alone,
	// a drum kit and a tambourine on its one line; a syllable being typed.
	var words *score.Score
	for _, name := range []string{"lyrics", "tab", "kits"} {
		ws, err := score.LoadFile(filepath.Join(repo, "internal", "score", "engrave", "testdata", name+score.FileExt))
		if err != nil {
			t.Fatal(err)
		}
		if words == nil {
			words = ws
			continue
		}
		words.Lines = append(words.Lines, ws.Lines...)
	}
	scEd = newScoreState(words)
	prevView := scorePageView
	defer func() { scorePageView = prevView }()
	scoreView, scorePageView = scoreViewScore, false
	scoreShowKeypad = false
	scoreZoom = 8
	scoreList = nil
	scEd.selectEvent(edit.Loc{Line: 0, Bar: 2, Event: 1}, -1)
	scEd.startLyrics(1)
	scEd.wordsInput("swee")
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "words", img)
	field := 0
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if c := img.RGBAAt(x, y); c.R > 230 && c.R < 245 && c.G > 235 && c.B > 250 {
				field++
			}
		}
	}
	if field < 200 {
		t.Errorf("no lyric field drawn (%d pixels of its colour)", field)
	}
	scEd.wordsKey(scoreWordEscape)
	frame()

	// The tools: the command palette narrowed to "trans", the Transpose
	// dialog over the whole score, the Find window with its matches, and
	// the MIDI Input dialog (with whatever ports this machine has).
	scEd = newScoreState(s)
	scoreList = nil
	scEd.clickBar(0, 1, false, false)
	scoreOpenPalette()
	frame()
	scorePalette.Query = "trans"
	scorePalette.shown = scorePaletteMatch(scorePalette.items, scorePalette.Query)
	frame()
	frame()
	img = readFrame(W, H)
	shot(t, "palette", img)
	if !scorePalette.Open {
		t.Error("the palette closed itself")
	}
	scorePaletteClose()
	frame()
	scEd.Sel = scoreSel{}
	scoreOpenTranspose()
	frame()
	frame()
	shot(t, "transpose", readFrame(W, H))
	scoreShowTranspose = false
	imgui.CloseCurrentPopup()
	frame()
	scEd.Find = scoreFind{Query: "D5"}
	scEd.find(&scEd.Find)
	scoreOpenFind()
	frame()
	frame()
	shot(t, "find", readFrame(W, H))
	scoreFindOpen = false
	frame()
	scoreOpenMIDI()
	frame()
	frame()
	shot(t, "midi", readFrame(W, H))
	scoreShowMIDI = false
	imgui.CloseCurrentPopup()
	frame()
}

// readFrame reads the framebuffer back as an opaque image, top row first.
func readFrame(W, H int) *image.RGBA {
	pix := make([]uint8, W*H*4)
	glext.ReadPixels(0, 0, int32(W), int32(H), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&pix[0]))
	// GL hands back straight colour; a window has no alpha, so the image
	// is opaque (as an image.RGBA it would be read as premultiplied, and the
	// PNG encoder's divide by alpha turns every antialiased edge to noise).
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		copy(img.Pix[y*W*4:(y+1)*W*4], pix[(H-1-y)*W*4:(H-y)*W*4])
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

// shot writes a frame to SCORE_SHOT (a name after the first, as
// shot_<name>.png beside it) when that is set.
func shot(t *testing.T, name string, img *image.RGBA) {
	path := os.Getenv("SCORE_SHOT")
	if path == "" {
		return
	}
	if name != "" {
		ext := filepath.Ext(path)
		path = strings.TrimSuffix(path, ext) + "_" + name + ext
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
}
