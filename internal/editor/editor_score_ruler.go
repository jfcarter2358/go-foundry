package editor

// editor_score_ruler.go — the Score window's transport row and the time
// ruler over the music (mockup screen 1).
//
// The transport row, under the top bar: the Score / Mixer tabs, then To
// start, Play/Stop, Loop and Follow, the position (bar, beat, clock), and
// the tempo slider — 25% to 200% of the written tempo, playback only — with
// its Write menu, which puts what the slider gives into the score as tempo
// marks.
//
// The ruler: bar numbers and the time each bar first plays at (at the
// slider's speed), beat ticks, the loop region and the play head's flag. A
// click seeks to the nearest beat; a drag sets the loop region, snapped to
// beats, and switches it on. It is one InvisibleButton over the ruler's
// music part only.

import (
	"fmt"
	"math"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/interpret"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreTransportH is the transport row's height.
const scoreTransportH = float32(34)

// scoreViewKind is what the main area shows: the score, the timeline
// (editor_score_timeline.go) or the mixer.
type scoreViewKind uint8

const (
	scoreViewScore scoreViewKind = iota
	scoreViewMixer
	scoreViewTimeline
)

// scoreView is the tab on show.
var scoreView scoreViewKind

var (
	scoreColLoopOn   = scoreRGB(0x42, 0x96, 0xfa, 0.38)
	scoreColLoopOff  = scoreRGB(0x8c, 0x96, 0xa5, 0.22)
	scoreColLoopEdge = scoreRGB(0x42, 0x96, 0xfa, 1)
	scoreColLoopDim  = scoreRGB(0x6b, 0x72, 0x80, 1)
	scoreColLoopFill = scoreRGB(0x42, 0x96, 0xfa, 0.08)
	scoreColBeatTick = scoreRGB(0x3a, 0x3f, 0x48, 1)
	scoreColReadout  = scoreRGB(0x0f, 0x11, 0x14, 1)
	scoreColReadEdge = scoreRGB(0x2c, 0x31, 0x3a, 1)
	scoreColReadText = scoreRGB(0xcf, 0xe3, 0xff, 1)
)

// scoreRulerDrag is a press on the ruler: where it started, and whether it
// has moved far enough to be a loop rather than a seek.
var scoreRulerDrag struct {
	on    bool
	from  score.Pos
	x     float32
	moved bool
}

// ---------- the play head ----------

// scoreHeadTick is where the play head is: the player's position while it
// plays, the transport's otherwise.
func scoreHeadTick() int64 {
	if scorePlaying() {
		return scorePlayer.Position().Tick
	}
	return scEd.Transport.Head
}

// scoreHeadSource is the play head's tick and the performance it is in.
type scoreHeadSource struct {
	perf *interpret.Performance
	tick int64
}

// scoreHeadPerf is the play head and its performance: the player's while
// it plays (it may be an edit behind), the editor's otherwise.
func scoreHeadPerf() scoreHeadSource {
	if scorePlaying() {
		if p := scorePlayer.Performance(); p != nil {
			return scoreHeadSource{perf: p, tick: scorePlayer.Position().Tick}
		}
	}
	return scoreHeadSource{perf: scEd.perf(), tick: scEd.Transport.Head}
}

// scoreHeadX is the play head's x on the layout, in staff spaces.
func scoreHeadX(l *engrave.List) (float64, bool) {
	src := scoreHeadPerf()
	if src.perf == nil {
		return 0, false
	}
	bar, off, _ := scoreTickPos(src.perf, src.tick)
	bb := l.Bar(bar)
	if bb == nil {
		return 0, false
	}
	return bb.XAt(off), true
}

// scorePosX is a written position's x: a bar's start is its barline, its
// end the next one.
func scorePosX(l *engrave.List, s *score.Score, p score.Pos) (float64, bool) {
	bb := l.Bar(p.Bar)
	if bb == nil {
		return 0, false
	}
	switch {
	case p.Offset.IsZero():
		return bb.X0, true
	case !p.Offset.Less(s.BarLength(p.Bar)):
		return bb.X1, true
	}
	return bb.XAt(p.Offset.Float()), true
}

// scorePosAtX is the written position an x on the layout means, snapped
// to the nearest beat; x outside the bars clamps to the first or last.
func scorePosAtX(l *engrave.List, s *score.Score, x float64) (score.Pos, bool) {
	if len(l.Bars) == 0 {
		return score.Pos{}, false
	}
	bb := l.BarAt(x)
	switch {
	case bb != nil:
	case x < l.Bars[0].X0:
		bb = &l.Bars[0]
	default:
		bb = &l.Bars[len(l.Bars)-1]
	}
	if x >= bb.X1-1e-9 {
		return score.Pos{Bar: bb.Bar, Offset: s.BarLength(bb.Bar)}, true
	}
	off := bb.OffsetAt(x)
	if x < bb.Content {
		off = 0
	}
	return scoreSnapBeat(s, bb.Bar, off), true
}

// ---------- the transport row ----------

// scoreTransportBar is the row under the top bar.
func scoreTransportBar(pos imgui.Vec2, w float32) {
	e := scEd
	imgui.SetNextWindowPos(pos)
	imgui.SetNextWindowSize(imgui.Vec2{X: w, Y: scoreTransportH})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{X: 8, Y: 4})
	imgui.PushStyleColor(imgui.StyleColorWindowBg, imgui.Vec4{X: 0.118, Y: 0.129, Z: 0.153, W: 1})
	imgui.BeginV("##scoretransport", nil, trkPaneFlags|imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse)
	imgui.PopStyleColor()
	imgui.PopStyleVar()
	km := scoreKeymapActive()
	tab := func(label string, v scoreViewKind, c scoreCmd) {
		if editorActiveToggleButtonColor(label, scoreView == v, scoreColActive) && scoreView != v {
			scoreRunCmd(c)
		}
	}
	tab("Timeline##sctabtl", scoreViewTimeline, scTimeline)
	editorMenuTooltip("Tracks with their automation lanes, and a piano roll of the selected line" + scoreKeysSuffix(scTimeline))
	imgui.SameLine()
	tab("Score##sctabscore", scoreViewScore, scScoreView)
	editorMenuTooltip("The score: notation, staff headers, the ruler" + scoreKeysSuffix(scScoreView))
	imgui.SameLine()
	if editorActiveToggleButtonColor("Pages##sctabpages", scorePageView && scoreView == scoreViewScore, scoreColActive) {
		if scoreView != scoreViewScore && scorePageView {
			scoreRunCmd(scScoreView)
		} else {
			scoreRunCmd(scPageView)
		}
	}
	editorMenuTooltip("The score on pages, as it prints (off: one continuous system)" + scoreKeysSuffix(scPageView))
	imgui.SameLine()
	tab("Mixer##sctabmixer", scoreViewMixer, scMixer)
	editorMenuTooltip("A strip per line: fader, pan, sends, mute, solo, meters; the buses and the master" + scoreKeysSuffix(scMixer))
	editorToolbarGroupSep()
	imgui.SameLine()
	if imgui.Button("Start##scstart") {
		scoreRunCmd(scToStart)
	}
	editorMenuTooltip("Play head to the start (of the loop, when it is on)" + scoreKeysSuffix(scToStart))
	imgui.SameLine()
	label := "Play##scplay2"
	if scorePlaying() {
		label = "Stop##scplay2"
	}
	if editorActiveToggleButtonColor(label, scorePlaying(), scoreColInput) {
		scoreRunCmd(scPlay)
	}
	editorMenuTooltip("Play from the play head, or stop (" + km.label(scPlay) + "); " + km.label(scPlayFrom) + " plays from the selection")
	imgui.SameLine()
	if editorActiveToggleButtonColor("Rec##screc", scoreRecording(), scoreColRec) {
		scoreRunCmd(scRecord)
	}
	editorMenuTooltip("Record from the MIDI keyboard into the armed (or selected) line, from the\nselected bar, after a count-in" + scoreKeysSuffix(scRecord) + "; Space stops.")
	imgui.SameLine()
	if editorActiveToggleButtonColor("Loop##scloop", e.Transport.Loop.On, scoreColActive) {
		scoreRunCmd(scLoopToggle)
	}
	editorMenuTooltip(scoreLoopText(e.Score, e.Transport.Loop) + "\nDrag along the ruler to set the region; " + km.label(scLoop) + " loops the selected bars.")
	imgui.SameLine()
	if editorActiveToggleButtonColor("Follow##scfollow", e.Transport.Follow, scoreColActive) {
		scoreRunCmd(scFollow)
	}
	editorMenuTooltip("Scroll the score to keep the play head in sight while it plays" + scoreKeysSuffix(scFollow))
	editorToolbarGroupSep()

	// The position readout, framed like a display.
	src := scoreHeadPerf()
	text := scoreReadout(src.perf, src.tick, e.Transport.Scale)
	imgui.SameLine()
	at := imgui.CursorScreenPos()
	tw := float32(math.Max(float64(imgui.CalcTextSize(text, false, 0).X), 190))
	imgui.Dummy(imgui.Vec2{X: tw + 16, Y: imgui.FrameHeight()})
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(at, imgui.Vec2{X: at.X + tw + 16, Y: at.Y + imgui.FrameHeight()}, scoreColReadout, 4, imgui.DrawFlagsRoundCornersAll)
	dl.AddRectV(at, imgui.Vec2{X: at.X + tw + 16, Y: at.Y + imgui.FrameHeight()}, scoreColReadEdge, 4, imgui.DrawFlagsRoundCornersAll, 1)
	dl.AddText(imgui.Vec2{X: at.X + 8, Y: at.Y + (imgui.FrameHeight()-imgui.TextLineHeight())/2}, scoreColReadText, text)
	editorMenuTooltip("Bar, beat and time as heard (at the tempo slider's speed)")

	// The tempo slider, in whole percents, five at a time.
	imgui.SameLine()
	editorTextMuted("Tempo")
	imgui.SameLine()
	pct := int32(math.Round(e.Transport.Scale * 100))
	imgui.SetNextItemWidth(150)
	if imgui.SliderIntV("##sctempo", &pct, 25, 200, "%d%%", 0) {
		e.setTempoScale(float64((pct+2)/5*5) / 100)
	}
	if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
		e.setTempoScale(1)
	}
	editorMenuTooltip("Playback speed, 25% to 200% of the written tempo; the score is unchanged.\nDouble-click: 100%. Write puts it into the score.")
	imgui.SameLine()
	imgui.Text(scoreTempoText(src.perf, src.tick, e.Transport.Scale))
	imgui.SameLine()
	if imgui.Button("Write##sctempowrite") {
		imgui.OpenPopup("##sctempomenu")
	}
	editorMenuTooltip("Write the slider's tempo into the score as tempo marks")
	if imgui.BeginPopup("##sctempomenu") {
		k := e.Transport.Scale
		if imgui.MenuItemV(fmt.Sprintf("Scale Every Tempo Mark to %.0f%%", k*100), "", false, k != 1) {
			e.writeTempoScaled()
		}
		editorMenuTooltip("The whole piece plays as it does now; the slider goes back to 100%.")
		bar := e.selBar()
		if e.Sel.Kind == scoreSelNone {
			bar = e.headBar()
		}
		perf := e.perf()
		if t := scorePosTick(perf, score.Pos{Bar: bar}); t >= 0 && perf != nil {
			q := perf.Tempo.QPM(float64(t)) * k
			if imgui.MenuItemV(fmt.Sprintf("Tempo Mark at Bar %d: %.0f bpm", bar, q), "", false, true) {
				e.writeTempoAt(bar)
			}
			editorMenuTooltip("A tempo mark at the selection's bar (or the play head's) with the tempo heard there;\nthe music before it keeps its tempo. The slider goes back to 100%.")
		}
		imgui.EndPopup()
	}
	imgui.End()
}

// ---------- the ruler ----------

// scoreRulerRows are the ruler's two text rows' heights: bar numbers, then
// times.
const scoreRulerRow = float32(14)

// scoreRuler draws the ruler along the top of the view and takes its
// clicks and drags.
func scoreRuler(dl imgui.DrawList, l *engrave.List, g scoreViewGeom, wp, size imgui.Vec2) {
	e := scEd
	top, bottom := wp.Y, wp.Y+scoreRulerH
	dl.AddRectFilled(imgui.Vec2{X: wp.X, Y: top}, imgui.Vec2{X: wp.X + size.X, Y: bottom}, scoreColRuler)
	dl.AddLine(imgui.Vec2{X: wp.X, Y: bottom - 0.5}, imgui.Vec2{X: wp.X + size.X, Y: bottom - 0.5}, scoreColEdge)

	// The press and drag, on the ruler's music part only.
	sx, sy := imgui.ScrollX(), imgui.ScrollY()
	imgui.SetCursorPos(imgui.Vec2{X: sx + scoreHeaderW, Y: sy})
	imgui.InvisibleButton("##scruler", imgui.Vec2{X: max(g.visMax.X-g.visMin.X, 1), Y: scoreRulerH})
	mouseX := g.toLayout(imgui.MousePos()).X
	switch {
	case imgui.IsItemActivated():
		if p, ok := scorePosAtX(l, e.Score, mouseX); ok {
			scoreRulerDrag.on, scoreRulerDrag.from, scoreRulerDrag.x, scoreRulerDrag.moved = true, p, imgui.MousePos().X, false
		}
	case imgui.IsItemActive() && scoreRulerDrag.on:
		if float32(math.Abs(float64(imgui.MousePos().X-scoreRulerDrag.x))) > 4 {
			scoreRulerDrag.moved = true
		}
		if scoreRulerDrag.moved {
			if p, ok := scorePosAtX(l, e.Score, mouseX); ok {
				// Shown while dragging; the player hears it on release.
				e.Transport.Loop = scoreLoopBetween(e.Score, scoreRulerDrag.from, p)
			}
		}
	}
	if imgui.IsItemDeactivated() && scoreRulerDrag.on {
		if scoreRulerDrag.moved {
			e.setLoop(e.Transport.Loop)
		} else {
			e.seek(scoreRulerDrag.from)
		}
		scoreRulerDrag.on = false
	}
	if imgui.IsItemHovered() && !imgui.IsItemActive() {
		imgui.SetTooltip("Click: move the play head here. Drag: loop between two beats.")
	}

	dl.PushClipRect(imgui.Vec2{X: g.visMin.X, Y: top}, imgui.Vec2{X: g.visMax.X, Y: bottom})
	if lp := e.Transport.Loop; lp.Set {
		xa, oka := scorePosX(l, e.Score, lp.A)
		xb, okb := scorePosX(l, e.Score, lp.B)
		if oka && okb {
			a, b := g.toScreen(engrave.Point{X: xa}).X, g.toScreen(engrave.Point{X: xb}).X
			fill, edge := scoreColLoopOff, scoreColLoopDim
			if lp.On {
				fill, edge = scoreColLoopOn, scoreColLoopEdge
			}
			dl.AddRectFilledV(imgui.Vec2{X: a, Y: top + 2}, imgui.Vec2{X: b, Y: bottom - 2}, fill, 3, imgui.DrawFlagsRoundCornersAll)
			dl.AddRectV(imgui.Vec2{X: a, Y: top + 2}, imgui.Vec2{X: b, Y: bottom - 2}, edge, 3, imgui.DrawFlagsRoundCornersAll, 1)
		}
	}
	perf := e.perf()
	scale := e.Transport.Scale
	for _, bb := range l.Bars {
		x := g.toScreen(engrave.Point{X: bb.X0}).X
		if x > g.visMax.X || g.toScreen(engrave.Point{X: bb.X1}).X < g.visMin.X {
			continue
		}
		dl.AddLine(imgui.Vec2{X: x, Y: top + 4}, imgui.Vec2{X: x, Y: bottom}, scoreColDim)
		dl.AddText(imgui.Vec2{X: x + 5, Y: top + 3}, scoreColRulerText, fmt.Sprint(bb.Bar))
		if t := scorePosTick(perf, score.Pos{Bar: bb.Bar}); t >= 0 {
			dl.AddText(imgui.Vec2{X: x + 5, Y: top + 3 + scoreRulerRow + 2}, scoreColDim, scoreClock(perf.Tempo.Seconds(float64(t))/scale))
		}
		for _, off := range scoreBeatOffsets(e.Score, bb.Bar)[1:] {
			bx := g.toScreen(engrave.Point{X: bb.XAt(off.Float())}).X
			dl.AddLine(imgui.Vec2{X: bx, Y: bottom - 6}, imgui.Vec2{X: bx, Y: bottom}, scoreColBeatTick)
		}
	}
	if hx, ok := scoreHeadX(l); ok {
		x := g.toScreen(engrave.Point{X: hx}).X
		dl.AddTriangleFilled(imgui.Vec2{X: x - 6, Y: bottom - 9}, imgui.Vec2{X: x + 6, Y: bottom - 9}, imgui.Vec2{X: x, Y: bottom}, scoreColPlay)
	}
	dl.PopClipRect()
	dl.AddRectFilled(imgui.Vec2{X: wp.X, Y: top}, imgui.Vec2{X: wp.X + scoreHeaderW, Y: bottom}, scoreColRuler)
	dl.AddText(imgui.Vec2{X: wp.X + 10, Y: top + 3}, scoreColDim, "BAR")
	timeLabel := "TIME"
	if scale != 1 {
		timeLabel = fmt.Sprintf("TIME @ %.0f%%", scale*100)
	}
	dl.AddText(imgui.Vec2{X: wp.X + 10, Y: top + 3 + scoreRulerRow + 2}, scoreColDim, timeLabel)
}

// scoreDrawLoopShade tints the loop region over the staves, under the music.
func scoreDrawLoopShade(dl imgui.DrawList, l *engrave.List, g scoreViewGeom) {
	lp := scEd.Transport.Loop
	if !lp.Set || !lp.On {
		return
	}
	xa, oka := scorePosX(l, scEd.Score, lp.A)
	xb, okb := scorePosX(l, scEd.Score, lp.B)
	if !oka || !okb {
		return
	}
	a, b := g.toScreen(engrave.Point{X: xa}).X, g.toScreen(engrave.Point{X: xb}).X
	dl.AddRectFilled(imgui.Vec2{X: a, Y: g.visMin.Y}, imgui.Vec2{X: b, Y: g.visMax.Y}, scoreColLoopFill)
}

// scoreFollow scrolls the view along with the play head while it plays:
// when it nears the right edge (or is off to the left) it is put back a
// little in from the left.
func scoreFollow(l *engrave.List, g scoreViewGeom) {
	if !scorePlaying() || !scEd.Transport.Follow {
		return
	}
	hx, ok := scoreHeadX(l)
	if !ok {
		return
	}
	x := g.toScreen(engrave.Point{X: hx}).X
	if x > g.visMax.X-90 || x < g.visMin.X {
		imgui.SetScrollX(max(0, imgui.ScrollX()+x-(g.visMin.X+60)))
	}
}
