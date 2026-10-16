package editor

// editor_score_timeline.go — the Score window's Timeline tab (mockup
// screen 2): the DAW side of the same file.
//
//	┌──────────────┬───────────────────────────────────────────────────┐
//	│ TRACKS       │ ruler: bars, the loop region, the play head        │
//	├──────────────┼───────────────────────────────────────────────────┤
//	│ Violin I  MSR│ ▭▭▭ region: the line's notes as a strip ▭▭▭▭▭▭▭▭▭ │
//	│  ↳ Volume  ✕ │ ──●────●───  automation lane, breakpoints          │
//	│ Piano     MSR│ ▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭▭ │
//	├──────────────┴───────────────────────────────────────────────────┤
//	│ Piano roll · Violin I   Select Draw  Grid 1/16   voices 1 2       │
//	├───────────┬──────────────────────────────────────────────────────┤
//	│ keys C4 ▯ │ notes, coloured by voice, on the grid                │
//	├───────────┼──────────────────────────────────────────────────────┤
//	│ velocity  │ ┃ ┃  ┃  a stalk a note                                │
//	└───────────┴──────────────────────────────────────────────────────┘
//
// Every line is a track: its name and instrument, mute, solo and record
// arm, the lanes switch (A) and + for a new lane. Under the lines are the
// mix's own tracks — Bus A, Bus B, the Master — with no region, only
// their lanes (a bus's return, pan and effects', the master's volume and
// effects'). A click on a region
// selects that bar of the line (the score view shows it) and gives the
// piano roll that line; a double-click goes to the bar in the Score tab.
// Under an opened track are its automation lanes: a click adds a point,
// a drag moves one (in time on the grid, in value freely), right-click a
// point for its segment's shape or to delete it, and with the Draw tool a
// drag draws the lane freehand.
//
// The piano roll is the selected line: drawn notes are written as notation
// (editor_score_timeline_edit.go). Select tool: click a note to select it
// (Shift adds), drag it to move (snapped to the grid, by semitones), drag
// its right end to stretch it, drag on empty roll to select in a box,
// double-click empty roll to draw a note one grid step long. Draw tool: a
// drag on empty roll draws a note as long as the drag, a right-click
// deletes. The velocity lane under it sets each note's velocity (a
// double-click gives it back to the dynamics). Delete deletes the selected
// notes, or the selected lane point.
//
// The time axis is written time — bars as printed, a repeat once — shared
// by the ruler, the tracks and the roll, with its own zoom and scroll
// (Ctrl+wheel zooms, Shift+wheel scrolls across, the bar under the roll
// drags). Each area is one InvisibleButton over only itself, so none eats
// another's clicks (the Tracker header trap).

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/edit"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// The Timeline's layout.
const (
	scoreTLLeft     = float32(168)
	scoreTLRulerH   = float32(26)
	scoreTLRowH     = float32(46)
	scoreTLMixRowH  = float32(30)
	scoreTLLaneH    = float32(44)
	scoreRollBarH   = float32(30)
	scoreRollKeysW  = float32(46)
	scoreRollVelH   = float32(64)
	scoreTLScrollH  = float32(14)
	scoreRollGrab   = float32(6) // how near a note's end a press stretches it
	scoreTLPointHit = float32(6)
)

// The Timeline's look, after the mockup.
var (
	scoreColTLBg       = imgui.Vec4{X: 0.102, Y: 0.110, Z: 0.129, W: 1}
	scoreColTLLeft     = scoreRGB(0x1e, 0x21, 0x27, 1)
	scoreColTLRow      = scoreRGB(0x2a, 0x2e, 0x36, 1)
	scoreColTLBar      = scoreRGB(0x30, 0x35, 0x3e, 1)
	scoreColTLBeat     = scoreRGB(0x23, 0x27, 0x2e, 1)
	scoreColTLGrid     = scoreRGB(0x1f, 0x22, 0x28, 1)
	scoreColRollWhite  = scoreRGB(0x22, 0x25, 0x2c, 1)
	scoreColRollBlack  = scoreRGB(0x1d, 0x20, 0x26, 1)
	scoreColKeyWhite   = scoreRGB(0xd6, 0xd9, 0xde, 1)
	scoreColKeyBlack   = scoreRGB(0x30, 0x34, 0x3c, 1)
	scoreColKeyEdge    = scoreRGB(0x15, 0x17, 0x1b, 1)
	scoreColRollSel    = scoreRGB(0xff, 0xff, 0xff, 0.9)
	scoreColRollGhost  = scoreRGB(0xff, 0xff, 0xff, 0.35)
	scoreColRollBox    = scoreRGB(0x42, 0x96, 0xfa, 0.18)
	scoreColAutoCurve  = scoreRGB(0x7f, 0xb3, 0xff, 1)
	scoreColAutoFill   = scoreRGB(0x42, 0x96, 0xfa, 0.12)
	scoreColAutoPoint  = scoreRGB(0x7f, 0xb3, 0xff, 1)
	scoreColAutoPtSel  = scoreRGB(0xff, 0xff, 0xff, 1)
	scoreColArm        = scoreRGB(0xb0, 0x30, 0x30, 1)
	scoreColLanesOn    = scoreRGB(0x2c, 0x5f, 0xae, 1)
	scoreColStroke     = scoreRGB(0xff, 0xd0, 0x60, 1)
	scoreColVelStalk   = scoreRGB(0x7f, 0xb3, 0xff, 1)
	scoreColVelDynamic = scoreRGB(0x7c, 0x8a, 0xa3, 1)

	// scoreTrackCols are the tracks' colours, line by line (the mockup's).
	scoreTrackCols = []imgui.PackedColor{
		scoreRGB(0x4f, 0x9c, 0xff, 1), scoreRGB(0x5c, 0xc8, 0x8a, 1), scoreRGB(0xe0, 0xa4, 0x4a, 1), scoreRGB(0xc7, 0x7d, 0xff, 1),
		scoreRGB(0xff, 0x7a, 0x8a, 1), scoreRGB(0x4a, 0xd0, 0xd0, 1), scoreRGB(0xd0, 0xd0, 0x5a, 1), scoreRGB(0x9a, 0xa8, 0xff, 1),
	}
	// scoreRollVoiceCols are the voices on the roll's dark ground.
	scoreRollVoiceCols = [score.MaxVoices]imgui.PackedColor{
		scoreRGB(0x4f, 0x9c, 0xff, 1), scoreRGB(0x5e, 0xd4, 0x94, 1), scoreRGB(0xf0, 0xa3, 0x5c, 1), scoreRGB(0xf0, 0x80, 0xc0, 1),
	}
)

// The Timeline's view, never saved with the score.
var (
	// scoreTLZoom is pixels a whole note; scoreTLScroll the written time
	// at the left edge, in whole notes.
	scoreTLZoom   = float32(120)
	scoreTLScroll = 0.0
	// scoreTLTrackScroll is how far the tracks are scrolled down, pixels.
	scoreTLTrackScroll float32
	// scoreRollTop is the MIDI key on the roll's top row; scoreRollKeyH a
	// key's height. scoreRollTopSet says it was centred on the line's notes.
	scoreRollTop    = 84
	scoreRollKeyH   = float32(10)
	scoreRollTopFor = -1
	// scoreTLTracksShare is the share of the pane the tracks take.
	scoreTLTracksShare = float32(0.42)
)

// scoreTLGeom is the Timeline's layout on screen this frame.
type scoreTLGeom struct {
	pos, size imgui.Vec2
	// gridX is where written time scoreTLScroll is; right the grid's end.
	gridX, right float32
	rulerTop     float32
	tracksTop    float32
	tracksBottom float32
	rollBarTop   float32
	rollTop      float32
	rollBottom   float32
	velTop       float32
	velBottom    float32
	scrollTop    float32
	zoom         float32
	scroll       float64
	keyH         float32
	topMIDI      int
}

// scoreTimelineGeom lays the Timeline out in a pane.
func scoreTimelineGeom(pos, size imgui.Vec2) scoreTLGeom {
	g := scoreTLGeom{pos: pos, size: size, zoom: scoreTLZoom, scroll: scoreTLScroll, keyH: scoreRollKeyH, topMIDI: scoreRollTop}
	g.gridX = pos.X + scoreTLLeft
	g.right = pos.X + size.X
	g.rulerTop = pos.Y
	g.tracksTop = pos.Y + scoreTLRulerH
	rest := size.Y - scoreTLRulerH - scoreRollBarH - scoreRollVelH - scoreTLScrollH
	tracksH := float32(math.Round(float64(max(rest*scoreTLTracksShare, scoreTLRowH))))
	g.tracksBottom = g.tracksTop + tracksH
	g.rollBarTop = g.tracksBottom
	g.rollTop = g.rollBarTop + scoreRollBarH
	g.rollBottom = max(g.rollTop+40, pos.Y+size.Y-scoreRollVelH-scoreTLScrollH)
	g.velTop = g.rollBottom
	g.velBottom = g.velTop + scoreRollVelH
	g.scrollTop = g.velBottom
	return g
}

// x is a written time's x.
func (g scoreTLGeom) x(t score.Frac) float32 {
	return g.gridX + float32((t.Float()-g.scroll)*float64(g.zoom))
}

// t is the written time at an x, exact to a 7680th of a whole note (so a
// triplet's or a quintuplet's grid line is hit exactly), never before 0.
func (g scoreTLGeom) t(x float32) score.Frac {
	v := g.scroll + float64(x-g.gridX)/float64(g.zoom)
	return score.F(int64(math.Round(math.Max(v, 0)*7680)), 7680)
}

// y is a MIDI key's row top in the roll; midi the key at a y.
func (g scoreTLGeom) y(midi int) float32 {
	return g.rollTop + float32(g.topMIDI-midi)*g.keyH
}

func (g scoreTLGeom) midi(y float32) int {
	return g.topMIDI - int(math.Floor(float64((y-g.rollTop)/g.keyH)))
}

// scoreTLRow is one row of the tracks area: a line's track, or one of its
// automation lanes.
type scoreTLRow struct {
	// line is the lane owner: a line, or score.OwnerBusA, OwnerBusB,
	// OwnerMaster for the mix's tracks.
	line int
	lane string // "" for the track
	y, h float32
}

// scoreTLRows lays out the tracks area's rows, top to bottom, before
// scrolling: the lines, then the buses and the master.
func scoreTLRows(e *scoreState) []scoreTLRow {
	var out []scoreTLRow
	y := float32(0)
	owners := make([]int, 0, len(e.Score.Lines)+3)
	for li := range e.Score.Lines {
		owners = append(owners, li)
	}
	owners = append(owners, score.OwnerBusA, score.OwnerBusB, score.OwnerMaster)
	for _, o := range owners {
		h := scoreTLRowH
		if o < 0 {
			h = scoreTLMixRowH
		}
		out = append(out, scoreTLRow{line: o, y: y, h: h})
		y += h
		if !e.tlOpen(o) {
			continue
		}
		for _, ln := range e.ownerLanes(o) {
			out = append(out, scoreTLRow{line: o, lane: ln.Name, y: y, h: scoreTLLaneH})
			y += scoreTLLaneH
		}
	}
	return out
}

// scoreTLRowsHeight is the tracks' full height.
func scoreTLRowsHeight(rows []scoreTLRow) float32 {
	if len(rows) == 0 {
		return 0
	}
	r := rows[len(rows)-1]
	return r.y + r.h
}

// Timeline popups and drags, opened by flags and drawn from the top level.
var (
	scoreTLAddOpen, scoreTLPointOpen bool
	scoreTLMenuLine                  int
	scoreTLMenuLane                  string
	scoreTLMenuPoint                 int
	scoreTLMenuPos                   imgui.Vec2
)

// scoreRollDragKind is what a press in the roll is doing.
type scoreRollDragKind uint8

const (
	scoreRollNone scoreRollDragKind = iota
	scoreRollMoving
	scoreRollStretching
	scoreRollDrawing
	scoreRollBoxing
	scoreRollVelocity
)

// scoreRollPress is a press in the roll or its velocity lane: what it is
// doing, the note it took, where it started (on screen, in time, in
// pitch), whether it has moved, the velocity it sets, and whether it adds
// to the selection.
type scoreRollPress struct {
	kind   scoreRollDragKind
	note   edit.RollNote
	press  imgui.Vec2
	t0     score.Frac
	midi0  int
	moved  bool
	vel    int
	extend bool
}

// scoreRollDrag is the press under way.
var scoreRollDrag scoreRollPress

// scoreTLHScroll is a drag of the scroll bar's thumb.
var scoreTLHScroll struct {
	on     bool
	x0     float32
	scroll float64
}

// scoreTimelinePane draws the Timeline in the main area.
func scoreTimelinePane(pos, size imgui.Vec2) {
	e := scEd
	imgui.SetNextWindowPos(pos)
	imgui.SetNextWindowSize(size)
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	imgui.PushStyleColor(imgui.StyleColorWindowBg, scoreColTLBg)
	imgui.BeginV("##scoretimeline", nil, trkPaneFlags|imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse)
	imgui.PopStyleColor()
	imgui.PopStyleVar()

	scoreTLClampView(size)
	g := scoreTimelineGeom(pos, size)
	scoreRollCentre(e, g)
	g = scoreTimelineGeom(pos, size)
	dl := imgui.WindowDrawList()
	scoreTLWheel(g)
	g = scoreTimelineGeom(pos, size)
	scoreTLFollow(g)
	g = scoreTimelineGeom(pos, size)

	scoreTLRuler(dl, g)
	scoreTLTracks(dl, g)
	scoreRollToolbar(dl, g)
	scoreRollPane(dl, g)
	scoreRollVelocityLane(dl, g)
	scoreTLScrollbar(dl, g)
	scoreTLPlayhead(dl, g)
	imgui.End()
}

// scoreTLClampView keeps the scroll inside the score and the roll inside
// the keyboard.
func scoreTLClampView(size imgui.Vec2) {
	e := scEd
	end := scoreTimeEnd(e.Score).Float()
	visible := float64(max(size.X-scoreTLLeft, 1)) / float64(scoreTLZoom)
	scoreTLScroll = math.Max(0, math.Min(scoreTLScroll, end+1-visible*0.5))
	scoreTLZoom = clampF32(scoreTLZoom, 16, 2400)
	scoreRollKeyH = clampF32(scoreRollKeyH, 5, 24)
	scoreRollTop = clampI(scoreRollTop, 20, 127)
}

// scoreRollCentre puts a line's notes in sight the first time the roll
// shows it.
func scoreRollCentre(e *scoreState, g scoreTLGeom) {
	li := e.rollLine()
	if scoreRollTopFor == li {
		return
	}
	scoreRollTopFor = li
	ns := e.rollNotes()
	lo, hi := 60, 72
	if len(ns) > 0 {
		lo, hi = 127, 0
		for _, n := range ns {
			lo, hi = min(lo, n.MIDI()), max(hi, n.MIDI())
		}
	}
	rows := int((g.rollBottom - g.rollTop) / g.keyH)
	scoreRollTop = clampI((lo+hi)/2+rows/2, 20, 127)
}

// scoreTLWheel scrolls and zooms by the wheel over the pane: over the
// tracks it scrolls them, over the roll the keys; Shift scrolls across,
// Ctrl zooms time about the pointer (over the roll's keys, the keys).
func scoreTLWheel(g scoreTLGeom) {
	// The tracks stay scrolled within their rows every frame (lanes hidden
	// or removed shrink them), not only when the wheel turns.
	defer func() {
		full := scoreTLRowsHeight(scoreTLRows(scEd))
		scoreTLTrackScroll = clampF32(scoreTLTrackScroll, 0, max(full-(g.tracksBottom-g.tracksTop), 0))
	}()
	io := imgui.CurrentIO()
	across, wheel := io.MouseWheel()
	if !imgui.IsWindowHovered() {
		return
	}
	if across != 0 {
		scoreTLScroll -= float64(across) * 60 / float64(scoreTLZoom)
	}
	if wheel == 0 {
		return
	}
	m := imgui.MousePos()
	switch {
	case io.KeyCtrlPressed() && m.X < g.gridX && m.Y >= g.rollTop && m.Y < g.rollBottom:
		scoreRollKeyH *= float32(math.Pow(1.15, float64(wheel)))
	case io.KeyCtrlPressed():
		at := g.t(m.X).Float()
		scoreTLZoom = clampF32(scoreTLZoom*float32(math.Pow(1.2, float64(wheel))), 16, 2400)
		scoreTLScroll = at - float64(m.X-g.gridX)/float64(scoreTLZoom)
	case io.KeyShiftPressed():
		scoreTLScroll -= float64(wheel) * 60 / float64(scoreTLZoom)
	case m.Y >= g.rollTop && m.Y < g.rollBottom:
		scoreRollTop += int(math.Round(float64(wheel) * 3))
	case m.Y >= g.tracksTop && m.Y < g.tracksBottom:
		scoreTLTrackScroll -= wheel * 40
	}
}

// scoreTLFollow keeps the play head in sight while it plays.
func scoreTLFollow(g scoreTLGeom) {
	if !scorePlaying() || !scEd.Transport.Follow {
		return
	}
	t, ok := scoreTLHeadTime()
	if !ok {
		return
	}
	x := g.x(t)
	if x > g.right-60 || x < g.gridX {
		scoreTLScroll = math.Max(0, t.Float()-float64(40)/float64(scoreTLZoom))
	}
}

// scoreTLHeadTime is the play head as written time.
func scoreTLHeadTime() (score.Frac, bool) {
	src := scoreHeadPerf()
	if src.perf == nil {
		return score.Frac{}, false
	}
	bar, off, _ := scoreTickPos(src.perf, src.tick)
	starts := scEd.Score.BarStarts()
	if bar < 1 || bar > len(starts)-1 {
		return score.Frac{}, false
	}
	return starts[bar-1].Add(score.F(int64(math.Round(off*7680)), 7680)), true
}

// scoreTLBars draws the bar and beat lines (and the grid's, when there is
// room) over a band.
func scoreTLBars(dl imgui.DrawList, g scoreTLGeom, top, bottom float32, grid bool) {
	e := scEd
	s := e.Score
	starts := s.BarStarts()
	for b := 1; b < len(starts); b++ {
		x0 := g.x(starts[b-1])
		if x0 > g.right {
			break
		}
		if g.x(starts[b]) < g.gridX {
			continue
		}
		if grid {
			u := e.TL.Grid.Unit()
			if float32(u.Float())*g.zoom >= 6 {
				for t := starts[b-1].Add(u); t.Less(starts[b]); t = t.Add(u) {
					if x := g.x(t); x >= g.gridX {
						dl.AddLine(imgui.Vec2{X: x, Y: top}, imgui.Vec2{X: x, Y: bottom}, scoreColTLGrid)
					}
				}
			}
		}
		for _, off := range scoreBeatOffsets(s, b)[1:] {
			if x := g.x(starts[b-1].Add(off)); x >= g.gridX {
				dl.AddLine(imgui.Vec2{X: x, Y: top}, imgui.Vec2{X: x, Y: bottom}, scoreColTLBeat)
			}
		}
		if x0 >= g.gridX {
			dl.AddLine(imgui.Vec2{X: x0, Y: top}, imgui.Vec2{X: x0, Y: bottom}, scoreColTLBar)
		}
	}
	if x := g.x(starts[len(starts)-1]); x >= g.gridX && x <= g.right {
		dl.AddLineV(imgui.Vec2{X: x, Y: top}, imgui.Vec2{X: x, Y: bottom}, scoreColTLBar, 2)
	}
}

// ---------- the ruler ----------

func scoreTLRuler(dl imgui.DrawList, g scoreTLGeom) {
	e := scEd
	s := e.Score
	top, bottom := g.rulerTop, g.rulerTop+scoreTLRulerH
	dl.AddRectFilled(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.right, Y: bottom}, scoreColRuler)
	dl.AddText(imgui.Vec2{X: g.pos.X + 12, Y: top + 6}, scoreColDim, "TRACKS")

	imgui.SetCursorScreenPos(imgui.Vec2{X: g.gridX, Y: top})
	imgui.InvisibleButton("##sctlruler", imgui.Vec2{X: max(g.right-g.gridX, 1), Y: scoreTLRulerH})
	at := func() score.Pos {
		p := scorePosOfTime(s, g.t(imgui.MousePos().X))
		if !p.Offset.IsZero() {
			return scoreSnapBeat(s, p.Bar, p.Offset.Float())
		}
		return p
	}
	switch {
	case imgui.IsItemActivated():
		scoreRulerDrag.on, scoreRulerDrag.from, scoreRulerDrag.x, scoreRulerDrag.moved = true, at(), imgui.MousePos().X, false
	case imgui.IsItemActive() && scoreRulerDrag.on:
		if math.Abs(float64(imgui.MousePos().X-scoreRulerDrag.x)) > 4 {
			scoreRulerDrag.moved = true
		}
		if scoreRulerDrag.moved {
			e.Transport.Loop = scoreLoopBetween(s, scoreRulerDrag.from, at())
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

	dl.PushClipRect(imgui.Vec2{X: g.gridX, Y: top}, imgui.Vec2{X: g.right, Y: bottom})
	if lp := e.Transport.Loop; lp.Set {
		starts := s.BarStarts()
		a, b := g.x(s.PosFrac(lp.A, starts)), g.x(s.PosFrac(lp.B, starts))
		fill, edge := scoreColLoopOff, scoreColLoopDim
		if lp.On {
			fill, edge = scoreColLoopOn, scoreColLoopEdge
		}
		dl.AddRectFilledV(imgui.Vec2{X: a, Y: top + 2}, imgui.Vec2{X: b, Y: bottom - 2}, fill, 3, imgui.DrawFlagsRoundCornersAll)
		dl.AddRectV(imgui.Vec2{X: a, Y: top + 2}, imgui.Vec2{X: b, Y: bottom - 2}, edge, 3, imgui.DrawFlagsRoundCornersAll, 1)
	}
	starts := s.BarStarts()
	last := float32(-100)
	for b := 1; b < len(starts); b++ {
		x := g.x(starts[b-1])
		if x > g.right {
			break
		}
		dl.AddLine(imgui.Vec2{X: x, Y: top + 4}, imgui.Vec2{X: x, Y: bottom}, scoreColDim)
		if x-last >= 22 {
			dl.AddText(imgui.Vec2{X: x + 4, Y: top + 6}, scoreColRulerText, fmt.Sprint(b))
			last = x
		}
		for _, off := range scoreBeatOffsets(s, b)[1:] {
			bx := g.x(starts[b-1].Add(off))
			dl.AddLine(imgui.Vec2{X: bx, Y: bottom - 6}, imgui.Vec2{X: bx, Y: bottom}, scoreColBeatTick)
		}
	}
	if t, ok := scoreTLHeadTime(); ok {
		x := g.x(t)
		dl.AddTriangleFilled(imgui.Vec2{X: x - 6, Y: bottom - 9}, imgui.Vec2{X: x + 6, Y: bottom - 9}, imgui.Vec2{X: x, Y: bottom}, scoreColPlay)
	}
	dl.PopClipRect()
	dl.AddLine(imgui.Vec2{X: g.pos.X, Y: bottom - 0.5}, imgui.Vec2{X: g.right, Y: bottom - 0.5}, scoreColEdge)
}

// ---------- tracks ----------

func scoreTLTracks(dl imgui.DrawList, g scoreTLGeom) {
	e := scEd
	rows := scoreTLRows(e)
	top, bottom := g.tracksTop, g.tracksBottom
	dl.AddRectFilled(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.gridX, Y: bottom}, scoreColTLLeft)
	dl.PushClipRect(imgui.Vec2{X: g.gridX, Y: top}, imgui.Vec2{X: g.right, Y: bottom})
	scoreTLBars(dl, g, top, bottom, false)
	scoreTLLoopShade(dl, g, top, bottom)
	dl.PopClipRect()
	// Every row inside the tracks area: one scrolled half under the ruler
	// draws only its visible half (each row's own clips intersect this).
	dl.PushClipRect(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.right, Y: bottom})
	defer dl.PopClipRect()
	for _, r := range rows {
		y := top + r.y - scoreTLTrackScroll
		if y+r.h <= top || y >= bottom {
			continue
		}
		switch {
		case r.lane != "":
			scoreTLLane(dl, g, r.line, r.lane, y)
		case r.line < 0:
			scoreTLMixTrack(dl, g, r.line, y)
		default:
			scoreTLTrack(dl, g, r.line, y)
		}
	}
	// More tracks than fit: a thumb down the right edge says where the
	// wheel has them.
	if full, view := scoreTLRowsHeight(rows), bottom-top; full > view {
		th := max(view*view/full, 16)
		ty := top + (view-th)*scoreTLTrackScroll/(full-view)
		dl.AddRectFilledV(imgui.Vec2{X: g.right - 6, Y: ty + 2}, imgui.Vec2{X: g.right - 2, Y: ty + th - 2}, scoreColFrameHi, 2, imgui.DrawFlagsRoundCornersAll)
	}
	dl.AddLine(imgui.Vec2{X: g.pos.X, Y: bottom - 0.5}, imgui.Vec2{X: g.right, Y: bottom - 0.5}, scoreColEdge)
}

// scoreTLLoopShade tints the loop region over a band.
func scoreTLLoopShade(dl imgui.DrawList, g scoreTLGeom, top, bottom float32) {
	lp := scEd.Transport.Loop
	if !lp.Set || !lp.On {
		return
	}
	s := scEd.Score
	starts := s.BarStarts()
	dl.AddRectFilled(imgui.Vec2{X: g.x(s.PosFrac(lp.A, starts)), Y: top}, imgui.Vec2{X: g.x(s.PosFrac(lp.B, starts)), Y: bottom}, scoreColLoopFill)
}

// scoreTrackCol is line li's colour.
func scoreTrackCol(li int) imgui.PackedColor { return scoreTrackCols[li%len(scoreTrackCols)] }

// scoreWithAlpha is a colour at another opacity.
func scoreWithAlpha(c imgui.PackedColor, a float32) imgui.PackedColor {
	v := imgui.Vec4{X: float32(c&0xff) / 255, Y: float32(c>>8&0xff) / 255, Z: float32(c>>16&0xff) / 255, W: a}
	return imgui.PackedColorFromVec4(v)
}

// inSight reports whether a band of the tracks pane is all in
// sight: a widget only then, since one half under the ruler would take
// the ruler's clicks.
func (g scoreTLGeom) inSight(a, b float32) bool { return a >= g.tracksTop && b <= g.tracksBottom }

// pressBand is the part of a row from y, h tall, in sight: a row's
// press area is cut to it (a row half scrolled away still takes clicks
// on its visible half). ok is false when too little shows.
func (g scoreTLGeom) pressBand(y, h float32) (y0, y1 float32, ok bool) {
	y0, y1 = max(y, g.tracksTop), min(y+h, g.tracksBottom)
	return y0, y1, y1-y0 >= 6
}

// scoreTLTrack draws one line's track row.
func scoreTLTrack(dl imgui.DrawList, g scoreTLGeom, li int, y float32) {
	e := scEd
	l := e.Score.Lines[li]
	col := scoreTrackCol(li)
	dl.AddLine(imgui.Vec2{X: g.pos.X, Y: y + scoreTLRowH - 0.5}, imgui.Vec2{X: g.right, Y: y + scoreTLRowH - 0.5}, scoreColTLRow)
	if li == e.rollLine() {
		dl.AddRectFilled(imgui.Vec2{X: g.pos.X, Y: y}, imgui.Vec2{X: g.pos.X + 3, Y: y + scoreTLRowH}, col)
	}
	dl.PushClipRectV(imgui.Vec2{X: g.pos.X, Y: y}, imgui.Vec2{X: g.pos.X + 104, Y: y + scoreTLRowH}, true)
	dl.AddText(imgui.Vec2{X: g.pos.X + 12, Y: y + 7}, scoreColText, e.lineName(li))
	dl.AddText(imgui.Vec2{X: g.pos.X + 12, Y: y + 24}, scoreColDim, scoreInstrumentLabel(l.Instrument))
	dl.PopClipRect()
	bx := g.pos.X + 104
	if g.inSight(y+6, y+22) {
		if scoreSmallButton(fmt.Sprintf("M##sctlm%d", li), imgui.Vec2{X: bx, Y: y + 6}, l.Mute, scoreColMute) {
			e.toggleMute(li)
		}
		editorMenuTooltip("Mute")
		if scoreSmallButton(fmt.Sprintf("S##sctls%d", li), imgui.Vec2{X: bx + 21, Y: y + 6}, l.Solo, scoreColSolo) {
			e.toggleSolo(li)
		}
		editorMenuTooltip("Solo")
		if scoreSmallButton(fmt.Sprintf("R##sctlr%d", li), imgui.Vec2{X: bx + 42, Y: y + 6}, e.armed(li), scoreColArm) {
			e.toggleArm(li)
		}
		editorMenuTooltip("Arm for recording: the piano roll shows an armed line when nothing is selected")
	}
	if g.inSight(y+25, y+41) {
		label := fmt.Sprintf("A%d", len(l.Lanes))
		if len(l.Lanes) == 0 {
			label = "A"
		}
		if scoreSmallButton(label+fmt.Sprintf("##sctla%d", li), imgui.Vec2{X: bx + 21, Y: y + 25}, e.tlOpen(li), scoreColLanesOn) {
			e.toggleAutoLanes(li)
		}
		editorMenuTooltip("Show or hide the line's automation lanes")
		if scoreSmallButton(fmt.Sprintf("+##sctladd%d", li), imgui.Vec2{X: bx + 42, Y: y + 25}, false, scoreColLanesOn) {
			scoreTLAddOpen, scoreTLMenuLine, scoreTLMenuPos = true, li, imgui.Vec2{X: bx + 42, Y: y + 43}
		}
		editorMenuTooltip("Add an automation lane: volume, pan, cutoff, a send, an effect's setting")
	}

	// The region: the line's notes as a strip.
	starts := e.Score.BarStarts()
	x0, x1 := g.x(score.Frac{}), g.x(starts[len(starts)-1])
	dl.PushClipRectV(imgui.Vec2{X: g.gridX, Y: y}, imgui.Vec2{X: g.right, Y: y + scoreTLRowH}, true)
	dl.AddRectFilledV(imgui.Vec2{X: x0 + 1, Y: y + 4}, imgui.Vec2{X: x1 - 1, Y: y + scoreTLRowH - 4}, scoreWithAlpha(col, 0.18), 5, imgui.DrawFlagsRoundCornersAll)
	dl.AddRectV(imgui.Vec2{X: x0 + 1, Y: y + 4}, imgui.Vec2{X: x1 - 1, Y: y + scoreTLRowH - 4}, scoreWithAlpha(col, 0.7), 5, imgui.DrawFlagsRoundCornersAll, 1)
	if q := e.Sel; q.Kind == scoreSelBars && li >= q.LineA && li <= q.LineB {
		a, b := g.x(starts[q.BarA-1]), g.x(starts[min(q.BarB, len(starts)-1)])
		dl.AddRectFilled(imgui.Vec2{X: a, Y: y + 4}, imgui.Vec2{X: b, Y: y + scoreTLRowH - 4}, scoreColSelFill)
		dl.AddRect(imgui.Vec2{X: a, Y: y + 4}, imgui.Vec2{X: b, Y: y + scoreTLRowH - 4}, scoreColSel)
	}
	ns := scoreTLLineNotes(li)
	lo, hi := 60, 61
	if len(ns) > 0 {
		lo, hi = 127, 0
		for _, n := range ns {
			lo, hi = min(lo, n.MIDI()), max(hi, n.MIDI())
		}
		if hi == lo {
			hi = lo + 1
		}
	}
	for _, n := range ns {
		a, b := g.x(n.Start), g.x(n.End)
		if b < g.gridX || a > g.right {
			continue
		}
		yy := y + scoreTLRowH - 9 - float32(n.MIDI()-lo)/float32(hi-lo)*(scoreTLRowH-18)
		dl.AddRectFilledV(imgui.Vec2{X: a + 1, Y: yy}, imgui.Vec2{X: max(b-1, a+2), Y: yy + 3}, col, 1, imgui.DrawFlagsRoundCornersAll)
	}
	dl.PopClipRect()

	y0, y1, ok := g.pressBand(y, scoreTLRowH)
	if !ok {
		return
	}
	imgui.SetCursorScreenPos(imgui.Vec2{X: g.gridX, Y: y0})
	imgui.InvisibleButton(fmt.Sprintf("##sctlregion%d", li), imgui.Vec2{X: max(g.right-g.gridX, 1), Y: y1 - y0})
	if imgui.IsItemClicked() {
		io := imgui.CurrentIO()
		p := scorePosOfTime(e.Score, g.t(imgui.MousePos().X))
		e.TL.RollLine = li
		e.TL.RollSel = nil
		e.clickBar(li, min(p.Bar, e.Score.Measures()), io.KeyCtrlPressed(), io.KeyShiftPressed())
	}
	if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
		scoreView = scoreViewScore
		e.Reveal = true
	}
	if imgui.IsItemHovered() && !imgui.IsItemActive() {
		imgui.SetTooltip("Click: select the bar (Shift extends, Ctrl every line); double-click: show it in the score")
	}
}

// scoreTLMixTrack draws a bus's or the master's track row: its name, the
// lanes switch and + for a lane, and across it its chain in words. It has
// no region.
func scoreTLMixTrack(dl imgui.DrawList, g scoreTLGeom, owner int, y float32) {
	e := scEd
	s := e.Score
	h := scoreTLMixRowH
	dl.AddLine(imgui.Vec2{X: g.pos.X, Y: y + h - 0.5}, imgui.Vec2{X: g.right, Y: y + h - 0.5}, scoreColTLRow)
	dl.PushClipRectV(imgui.Vec2{X: g.pos.X, Y: y}, imgui.Vec2{X: g.pos.X + 100, Y: y + h}, true)
	dl.AddText(imgui.Vec2{X: g.pos.X + 12, Y: y + 8}, scoreColText, s.OwnerName(owner))
	dl.PopClipRect()
	bx := g.pos.X + 125
	if g.inSight(y+7, y+23) {
		lanes := e.ownerLanes(owner)
		label := fmt.Sprintf("A%d", len(lanes))
		if len(lanes) == 0 {
			label = "A"
		}
		if scoreSmallButton(label+fmt.Sprintf("##sctlma%d", -owner), imgui.Vec2{X: bx - 21, Y: y + 7}, e.tlOpen(owner), scoreColLanesOn) {
			e.toggleAutoLanes(owner)
		}
		editorMenuTooltip("Show or hide the " + s.OwnerName(owner) + "'s automation lanes")
		if scoreSmallButton(fmt.Sprintf("+##sctlmadd%d", -owner), imgui.Vec2{X: bx, Y: y + 7}, false, scoreColLanesOn) {
			scoreTLAddOpen, scoreTLMenuLine, scoreTLMenuPos = true, owner, imgui.Vec2{X: bx, Y: y + 25}
		}
		editorMenuTooltip("Add an automation lane: " + map[bool]string{true: "the return, its pan", false: "the volume"}[owner != score.OwnerMaster] + ", an effect's setting")
	}
	// The band: the chain, in words.
	dl.PushClipRectV(imgui.Vec2{X: g.gridX, Y: y}, imgui.Vec2{X: g.right, Y: y + h}, true)
	dl.AddRectFilled(imgui.Vec2{X: g.gridX, Y: y + 3}, imgui.Vec2{X: g.right, Y: y + h - 3}, scoreRGB(0x1b, 0x1e, 0x24, 1))
	var names []string
	for _, f := range *s.ChainOf(owner) {
		n := fxLabel(f)
		if fxSilent(f) {
			n += " (off)"
		}
		names = append(names, n)
	}
	chain := "no effects"
	if len(names) > 0 {
		chain = strings.Join(names, " → ")
	}
	dl.AddText(imgui.Vec2{X: g.gridX + 8, Y: y + 8}, scoreColDim, chain)
	dl.PopClipRect()
}

// scoreTLLineNotes are a line's notes for its track's strip, read once an
// edit.
var scoreTLNoteCache struct {
	rev   uint64
	s     *score.Score
	lines [][]edit.RollNote
}

func scoreTLLineNotes(li int) []edit.RollNote {
	e := scEd
	c := &scoreTLNoteCache
	if c.rev != e.Rev || c.s != e.Score || len(c.lines) != len(e.Score.Lines) {
		c.rev, c.s = e.Rev, e.Score
		c.lines = make([][]edit.RollNote, len(e.Score.Lines))
		for i := range c.lines {
			c.lines[i] = edit.RollNotes(e.Score, i)
		}
	}
	return c.lines[li]
}

// scoreLaneNorm is a lane value as 0..1 of its lane's height.
func scoreLaneNorm(t score.LaneTarget, v float32) float32 {
	lo, hi := t.Range()
	if t.Kind == score.LaneVolume {
		// Volume reads on a fader's scale: unity three quarters of the way.
		hi = 4.0 / 3
	}
	return float32(min(max(edit.LaneNorm(t, v, lo, hi), 0), 1))
}

// scoreLaneValue is the value at 0..1 of a lane's height.
func scoreLaneValue(t score.LaneTarget, n float32) float32 {
	lo, hi := t.Range()
	if t.Kind == score.LaneVolume {
		hi = 4.0 / 3
	}
	return t.Clamp(edit.LaneDenorm(t, float64(n), lo, hi))
}

// scoreTLLane draws one automation lane and takes its presses.
func scoreTLLane(dl imgui.DrawList, g scoreTLGeom, li int, name string, y float32) {
	e := scEd
	s := e.Score
	ln := s.LaneOf(li, name)
	if ln == nil {
		return
	}
	t, err := scoreLaneTarget(li, name)
	if err != nil {
		return
	}
	starts := s.BarStarts()
	h := scoreTLLaneH
	yOf := func(v float32) float32 { return y + h - 5 - scoreLaneNorm(t, v)*(h-10) }
	dl.AddLine(imgui.Vec2{X: g.pos.X, Y: y + h - 0.5}, imgui.Vec2{X: g.right, Y: y + h - 0.5}, scoreColTLRow)
	dl.AddRectFilled(imgui.Vec2{X: g.gridX, Y: y}, imgui.Vec2{X: g.right, Y: y + h}, scoreRGB(0x16, 0x18, 0x1c, 1))
	label := t.Label()
	if t.Kind == score.LaneFX && score.ChainSlot(*s.ChainOf(li), t) < 0 {
		label += " (no such effect)"
	}
	dl.PushClipRectV(imgui.Vec2{X: g.pos.X, Y: y}, imgui.Vec2{X: g.pos.X + scoreTLLeft - 26, Y: y + h}, true)
	// A hook from the track down to the lane.
	dl.AddLineV(imgui.Vec2{X: g.pos.X + 14, Y: y + 2}, imgui.Vec2{X: g.pos.X + 14, Y: y + 13}, scoreColDim, 1)
	dl.AddLineV(imgui.Vec2{X: g.pos.X + 14, Y: y + 13}, imgui.Vec2{X: g.pos.X + 21, Y: y + 13}, scoreColDim, 1)
	dl.AddText(imgui.Vec2{X: g.pos.X + 25, Y: y + 6}, scoreColText, label)
	head := score.Frac{}
	if ht, ok := scoreTLHeadTime(); ok {
		head = ht
	}
	dl.AddText(imgui.Vec2{X: g.pos.X + 30, Y: y + 23}, scoreColDim, t.Format(s.LaneValue(ln, head, starts)))
	dl.PopClipRect()
	if g.inSight(y+6, y+22) {
		if scoreSmallButton(fmt.Sprintf("x##sctlrm%d%s", li, name), imgui.Vec2{X: g.pos.X + scoreTLLeft - 24, Y: y + 6}, false, scoreColMute) {
			e.autoRemove(li, name)
			return
		}
		editorMenuTooltip("Remove the lane")
	}

	// The curve: sampled across the visible stretch, steps drawn square.
	dl.PushClipRectV(imgui.Vec2{X: g.gridX, Y: y}, imgui.Vec2{X: g.right, Y: y + h}, true)
	scoreTLBars(dl, g, y, y+h, false)
	var pts []imgui.Vec2
	for px := g.gridX; px <= g.right+2; px += 2 {
		pts = append(pts, imgui.Vec2{X: px, Y: yOf(s.LaneValue(ln, g.t(px), starts))})
	}
	for k := range ln.Points {
		// The breakpoints themselves, so a step's corner is exact.
		x := g.x(s.PosFrac(ln.Points[k].At, starts))
		if x < g.gridX || x > g.right {
			continue
		}
		v0 := s.LaneValue(ln, s.PosFrac(ln.Points[k].At, starts).Sub(score.F(1, 7680)), starts)
		pts = append(pts, imgui.Vec2{X: x, Y: yOf(v0)}, imgui.Vec2{X: x, Y: yOf(ln.Points[k].Value)})
	}
	sortVec2ByX(pts)
	for i := 1; i < len(pts); i++ {
		dl.AddLineV(pts[i-1], pts[i], scoreColAutoCurve, 1.5)
	}
	for k, p := range ln.Points {
		c := imgui.Vec2{X: g.x(s.PosFrac(p.At, starts)), Y: yOf(p.Value)}
		col := scoreColAutoPoint
		sel := e.TL.AutoSel.Line == li && e.TL.AutoSel.Lane == name && e.TL.AutoSel.Index == k
		if sel {
			col = scoreColAutoPtSel
		}
		dl.AddCircleFilled(c, 3.5, col)
		if sel {
			dl.AddCircleV(c, 6, col, 12, 1)
		}
		if p.HasTo {
			dl.AddLineV(c, imgui.Vec2{X: g.x(s.PosFrac(p.To, starts)), Y: c.Y}, scoreColAutoCurve, 2.5)
		}
	}
	if d := e.TL.drag; d.On && d.Line == li && d.Lane == name && d.Stroke != nil {
		for i := 1; i < len(d.Stroke); i++ {
			a, b := d.Stroke[i-1], d.Stroke[i]
			dl.AddLineV(imgui.Vec2{X: g.x(s.PosFrac(a.At, starts)), Y: yOf(a.Value)}, imgui.Vec2{X: g.x(s.PosFrac(b.At, starts)), Y: yOf(b.Value)}, scoreColStroke, 2)
		}
	}
	dl.PopClipRect()
	y0, y1, ok := g.pressBand(y, h)
	if !ok {
		return
	}

	// The presses.
	imgui.SetCursorScreenPos(imgui.Vec2{X: g.gridX, Y: y0})
	imgui.InvisibleButton(fmt.Sprintf("##sctllane%d%s", li, name), imgui.Vec2{X: max(g.right-g.gridX, 1), Y: y1 - y0})
	m := imgui.MousePos()
	valueAt := func() float32 { return scoreLaneValue(t, (y+h-5-m.Y)/(h-10)) }
	posAt := func() score.Pos {
		tt := g.t(m.X)
		if !imgui.CurrentIO().KeyShiftPressed() {
			tt = scoreGridSnap(s, tt, e.TL.Grid, false)
		}
		return scorePosOfTime(s, tt)
	}
	hit := -1
	for k, p := range ln.Points {
		c := imgui.Vec2{X: g.x(s.PosFrac(p.At, starts)), Y: yOf(p.Value)}
		if dx, dy := c.X-m.X, c.Y-m.Y; dx*dx+dy*dy <= scoreTLPointHit*scoreTLPointHit*2 {
			hit = k
		}
	}
	switch {
	case imgui.IsItemActivated() && imgui.IsMouseDown(0):
		e.autoPress(li, name, posAt(), valueAt(), hit)
	case imgui.IsItemActive() && e.TL.drag.On:
		if e.TL.drag.Stroke != nil {
			// A stroke takes the pointer as it goes, unsnapped.
			e.autoMove(scorePosOfTime(s, g.t(m.X)), valueAt())
		} else {
			e.autoMove(posAt(), valueAt())
		}
	}
	if imgui.IsItemDeactivated() && e.TL.drag.On {
		e.autoRelease()
	}
	if (imgui.IsItemHovered() && imgui.IsMouseClicked(1)) && hit >= 0 {
		e.TL.AutoSel = scoreAutoSel{Line: li, Lane: name, Index: hit}
		scoreTLPointOpen, scoreTLMenuLine, scoreTLMenuLane, scoreTLMenuPoint, scoreTLMenuPos = true, li, name, hit, m
	}
	if imgui.IsItemHovered() && !imgui.IsItemActive() {
		tip := "Click: add a point. Drag a point: move it (Shift: off the grid). Right-click a point: its shape, delete."
		if e.TL.Tool == scoreToolDraw {
			tip = "Draw tool: drag to draw the lane freehand."
		}
		if hit >= 0 {
			p := ln.Points[hit]
			tip = fmt.Sprintf("%s at %s", t.Format(p.Value), s.FormatPos(p.At))
		}
		imgui.SetTooltip(tip)
	}
}

// sortVec2ByX orders points left to right (stable: a step's two points at
// one x keep their order).
func sortVec2ByX(p []imgui.Vec2) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].X < p[j-1].X; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}

// ---------- the piano roll ----------

// scoreRollToolbar is the bar over the roll: its line, the tools, the
// grid, the voices it uses.
func scoreRollToolbar(dl imgui.DrawList, g scoreTLGeom) {
	e := scEd
	top := g.rollBarTop
	dl.AddRectFilled(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.right, Y: top + scoreRollBarH}, scoreColRuler)
	li := e.rollLine()
	title := "Piano roll"
	if e.line(li) != nil {
		title += " · " + e.lineName(li)
	}
	dl.AddText(imgui.Vec2{X: g.pos.X + 12, Y: top + 8}, scoreColText, title)
	imgui.SetCursorScreenPos(imgui.Vec2{X: g.gridX + 8, Y: top + 4})
	if editorActiveToggleButtonColor("Select##sctlsel", e.TL.Tool == scoreToolSelect, scoreColActive) {
		e.TL.Tool = scoreToolSelect
	}
	editorMenuTooltip("Select: click and drag notes, drag their ends, box-select; double-click to draw one")
	imgui.SameLine()
	if editorActiveToggleButtonColor("Draw##sctldraw", e.TL.Tool == scoreToolDraw, scoreColActive) {
		e.TL.Tool = scoreToolDraw
	}
	editorMenuTooltip("Draw: drag out a note (or a lane, freehand); right-click a note to delete it")
	imgui.SameLine()
	editorTextMuted("Grid")
	imgui.SameLine()
	imgui.SetNextItemWidth(110)
	if imguibackend.BeginCombo("##sctlgrid", e.TL.Grid.Label()) {
		for i, gr := range scoreGrids {
			if imguibackend.SelectableV(gr.Label()+fmt.Sprintf("##sctlg%d", i), gr == e.TL.Grid, 0, imgui.Vec2{}) {
				e.TL.Grid = gr
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("What notes and lane points snap to (a triplet grid writes triplets)")
	imgui.SameLine()
	editorTextMuted("Voice")
	for v := 0; v < score.MaxVoices; v++ {
		imgui.SameLine()
		imgui.PushStyleColor(imgui.StyleColorText, scoreVoiceUICol[v])
		if editorActiveToggleButtonColor(fmt.Sprintf("%d##sctlv%d", v+1, v), e.Voice == v, scoreColActive) {
			e.setVoice(v)
		}
		imgui.PopStyleColor()
		editorMenuTooltip(fmt.Sprintf("Draw into voice %d (a note that would cut one there goes into the next free voice)", v+1))
	}
	if ns := e.rollNotes(); len(ns) > 0 {
		imgui.SameLine()
		editorTextMuted(fmt.Sprintf("  %d notes · voices %s", len(ns), rollVoiceSummary(ns)))
	}
}

// scoreRollNoteAt is the note under a point and whether the point is on
// its stretchable end; -1 for none.
func scoreRollNoteAt(g scoreTLGeom, ns []edit.RollNote, p imgui.Vec2) (int, bool) {
	for i := len(ns) - 1; i >= 0; i-- {
		n := ns[i]
		a, b := g.x(n.Start), g.x(n.End)
		y := g.y(n.MIDI())
		if p.X >= a && p.X <= max(b, a+3) && p.Y >= y && p.Y < y+g.keyH {
			return i, p.X >= max(b-scoreRollGrab, a+(b-a)/2)
		}
	}
	return -1, false
}

func scoreRollPane(dl imgui.DrawList, g scoreTLGeom) {
	e := scEd
	s := e.Score
	li := e.rollLine()
	top, bottom := g.rollTop, g.rollBottom
	if e.line(li) == nil {
		return
	}
	ns := e.rollNotes()

	// The keys, and the rows behind the notes.
	dl.AddRectFilled(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.gridX, Y: bottom}, scoreColTLLeft)
	dl.PushClipRect(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.right, Y: bottom})
	kx := g.gridX - scoreRollKeysW
	for m := g.midi(bottom); m <= g.topMIDI; m++ {
		y := g.y(m)
		black := scoreBlackKey(m)
		key, row := scoreColKeyWhite, scoreColRollWhite
		if black {
			key, row = scoreColKeyBlack, scoreColRollBlack
		}
		dl.AddRectFilled(imgui.Vec2{X: kx, Y: y}, imgui.Vec2{X: g.gridX, Y: y + g.keyH}, key)
		dl.AddRect(imgui.Vec2{X: kx, Y: y}, imgui.Vec2{X: g.gridX, Y: y + g.keyH + 0.5}, scoreColKeyEdge)
		dl.AddRectFilled(imgui.Vec2{X: g.gridX, Y: y}, imgui.Vec2{X: g.right, Y: y + g.keyH}, row)
		if m%12 == 0 {
			lbl := fmt.Sprintf("C%d", m/12-1)
			tw := imgui.CalcTextSize(lbl, false, 0)
			dl.AddText(imgui.Vec2{X: kx - tw.X - 6, Y: y + g.keyH/2 - tw.Y/2}, scoreColDim, lbl)
			dl.AddLine(imgui.Vec2{X: g.gridX, Y: y + g.keyH}, imgui.Vec2{X: g.right, Y: y + g.keyH}, scoreColTLBar)
		}
	}
	dl.PopClipRect()
	dl.PushClipRect(imgui.Vec2{X: g.gridX, Y: top}, imgui.Vec2{X: g.right, Y: bottom})
	scoreTLBars(dl, g, top, bottom, true)
	scoreTLLoopShade(dl, g, top, bottom)

	// The notes.
	d := &scoreRollDrag
	selEv := e.Sel
	starts := s.BarStarts()
	for _, n := range ns {
		a, b := g.x(n.Start), g.x(n.End)
		if b < g.gridX || a > g.right {
			continue
		}
		y := g.y(n.MIDI())
		col := scoreRollVoiceCols[n.Voice%score.MaxVoices]
		dl.AddRectFilledV(imgui.Vec2{X: a + 0.5, Y: y + 0.5}, imgui.Vec2{X: max(b-1, a+2), Y: y + g.keyH - 0.5}, col, 2, imgui.DrawFlagsRoundCornersAll)
		// Its tied parts' joins, faintly.
		for _, p := range n.Parts[1:] {
			on, _ := edit.Onset(s, p.Loc)
			x := g.x(starts[p.Loc.Bar-1].Add(on))
			dl.AddLine(imgui.Vec2{X: x, Y: y + 2}, imgui.Vec2{X: x, Y: y + g.keyH - 2}, scoreWithAlpha(scoreColEdge, 0.5))
		}
		sel := e.rollSelected(n)
		if !sel && selEv.Kind == scoreSelEvent {
			for _, p := range n.Parts {
				if p.Loc == selEv.Loc && (selEv.Note < 0 || selEv.Note == p.Note) {
					sel = true
				}
			}
		}
		if sel {
			dl.AddRectV(imgui.Vec2{X: a, Y: y}, imgui.Vec2{X: max(b-0.5, a+2.5), Y: y + g.keyH}, scoreColRollSel, 2, imgui.DrawFlagsRoundCornersAll, 1.5)
		}
	}

	// A drag's ghost.
	m := imgui.MousePos()
	switch d.kind {
	case scoreRollMoving, scoreRollStretching:
		dt, dm, dEnd := scoreRollDragDelta(g)
		moving := e.rollSelNow()
		if !e.rollSelected(d.note) {
			moving = []edit.RollNote{d.note}
		}
		for _, n := range moving {
			a, b := g.x(n.Start.Add(dt)), g.x(n.End.Add(dt).Add(dEnd))
			y := g.y(n.MIDI() + dm)
			dl.AddRectFilledV(imgui.Vec2{X: a, Y: y}, imgui.Vec2{X: max(b, a+3), Y: y + g.keyH}, scoreColRollGhost, 2, imgui.DrawFlagsRoundCornersAll)
		}
	case scoreRollDrawing:
		a, b := scoreRollDrawSpan(g)
		y := g.y(d.midi0)
		dl.AddRectFilledV(imgui.Vec2{X: g.x(a), Y: y}, imgui.Vec2{X: g.x(b), Y: y + g.keyH}, scoreColRollGhost, 2, imgui.DrawFlagsRoundCornersAll)
	case scoreRollBoxing:
		dl.AddRectFilled(d.press, m, scoreColRollBox)
		dl.AddRect(d.press, m, scoreColAccent)
	}
	dl.PopClipRect()

	// The keys sound when clicked.
	imgui.SetCursorScreenPos(imgui.Vec2{X: kx, Y: top})
	imgui.InvisibleButton("##sctlkeys", imgui.Vec2{X: scoreRollKeysW, Y: max(bottom-top, 1)})
	if imgui.IsItemActivated() {
		mk := g.midi(m.Y)
		e.auditions = append(e.auditions, scoreAudition{Line: li, MIDI: []int{mk}})
		e.Status = fmt.Sprintf("%s (MIDI %d)", edit.PitchName(score.SpellMIDI(mk, e.fifths(1), 0)), mk)
	}

	// The roll's presses.
	imgui.SetCursorScreenPos(imgui.Vec2{X: g.gridX, Y: top})
	imgui.InvisibleButton("##sctlroll", imgui.Vec2{X: max(g.right-g.gridX, 1), Y: max(bottom-top, 1)})
	hover := imgui.IsItemHovered()
	io := imgui.CurrentIO()
	hit, edge := scoreRollNoteAt(g, ns, m)
	switch {
	case imgui.IsItemActivated() && imgui.IsMouseDown(0):
		*d = scoreRollPress{press: m, t0: g.t(m.X), midi0: g.midi(m.Y), extend: io.KeyShiftPressed() || io.KeyCtrlPressed()}
		switch {
		case hit >= 0:
			d.note = ns[hit]
			if !e.rollSelected(d.note) {
				e.rollSelect(li, []edit.RollNote{d.note}, d.extend)
			}
			d.kind = scoreRollMoving
			if edge {
				d.kind = scoreRollStretching
			}
		case e.TL.Tool == scoreToolDraw:
			d.kind = scoreRollDrawing
		default:
			d.kind = scoreRollBoxing
		}
	case imgui.IsItemActive() && d.kind != scoreRollNone:
		if dx, dy := m.X-d.press.X, m.Y-d.press.Y; dx*dx+dy*dy > 9 {
			d.moved = true
		}
	}
	if imgui.IsItemDeactivated() && d.kind != scoreRollNone {
		scoreRollRelease(g, li)
	}
	if hover && imgui.IsMouseDoubleClicked(0) && hit < 0 && e.TL.Tool == scoreToolSelect {
		// The second press drew the note: it is no box to select with.
		d.kind = scoreRollNone
		t := scoreGridSnap(s, g.t(m.X), e.TL.Grid, true)
		e.rollDraw(li, t, t.Add(e.TL.Grid.Unit()), g.midi(m.Y))
	}
	if (imgui.IsItemHovered() && imgui.IsMouseClicked(1)) && hit >= 0 {
		n := ns[hit]
		if !e.rollSelected(n) {
			e.rollSelect(li, []edit.RollNote{n}, false)
		}
		e.rollDelete(li)
	}
	if hover && d.kind == scoreRollNone {
		switch {
		case hit >= 0 && edge:
			imgui.SetMouseCursor(imgui.MouseCursorResizeEW)
			imgui.SetTooltip(e.rollDescribe(li, ns[hit]) + "\nDrag: stretch it")
		case hit >= 0:
			imgui.SetMouseCursor(imgui.MouseCursorHand)
			imgui.SetTooltip(e.rollDescribe(li, ns[hit]) + "\nDrag: move it. Right-click: delete it.")
		}
	}
}

// scoreBlackKey reports a black key.
func scoreBlackKey(m int) bool {
	switch ((m % 12) + 12) % 12 {
	case 1, 3, 6, 8, 10:
		return true
	}
	return false
}

// scoreRollDragDelta is a move or stretch drag's change: in time (the
// note's start onto the grid), in pitch, and of the end.
func scoreRollDragDelta(g scoreTLGeom) (dt score.Frac, dm int, dEnd score.Frac) {
	e := scEd
	d := &scoreRollDrag
	m := imgui.MousePos()
	n := d.note
	snap := !imgui.CurrentIO().KeyAltPressed()
	switch d.kind {
	case scoreRollMoving:
		if !d.moved {
			return
		}
		to := n.Start.Add(g.t(m.X)).Sub(d.t0)
		if to.Sign() < 0 {
			to = score.Frac{}
		}
		if snap {
			to = scoreGridSnap(e.Score, to, e.TL.Grid, false)
		}
		dt = to.Sub(n.Start)
		dm = g.midi(m.Y) - d.midi0
	case scoreRollStretching:
		end := g.t(m.X)
		if snap {
			end = scoreGridSnap(e.Score, end, e.TL.Grid, false)
		}
		minEnd := n.Start.Add(e.TL.Grid.Unit())
		if end.Less(minEnd) {
			end = minEnd
		}
		dEnd = end.Sub(n.End)
	}
	return
}

// scoreRollDrawSpan is a draw drag's note: from the grid line at or before
// the press to the grid line nearest the pointer, one step at least.
func scoreRollDrawSpan(g scoreTLGeom) (a, b score.Frac) {
	e := scEd
	d := &scoreRollDrag
	a = scoreGridSnap(e.Score, d.t0, e.TL.Grid, true)
	b = scoreGridSnap(e.Score, g.t(imgui.MousePos().X), e.TL.Grid, false)
	if !a.Add(e.TL.Grid.Unit()).Less(b) {
		b = a.Add(e.TL.Grid.Unit())
	}
	return a, b
}

// scoreRollRelease ends a press in the roll: the move, stretch, note or
// box it made.
func scoreRollRelease(g scoreTLGeom, li int) {
	e := scEd
	d := &scoreRollDrag
	defer func() { d.kind = scoreRollNone }()
	switch d.kind {
	case scoreRollMoving, scoreRollStretching:
		if !d.moved {
			// A click on a note: just that note (Shift kept the others).
			if !d.extend {
				e.rollSelect(li, []edit.RollNote{d.note}, false)
			}
			return
		}
		dt, dm, dEnd := scoreRollDragDelta(g)
		e.rollMove(li, d.note, dt, dm, dEnd)
	case scoreRollDrawing:
		a, b := scoreRollDrawSpan(g)
		e.rollDraw(li, a, b, d.midi0)
	case scoreRollBoxing:
		if !d.moved {
			if !d.extend {
				e.TL.RollSel = nil
				e.Sel = scoreSel{}
				e.TL.RollLine = li
				e.Status = "Nothing selected"
			}
			return
		}
		m := imgui.MousePos()
		e.rollSelectInBox(li, d.t0, g.t(m.X), d.midi0, g.midi(m.Y), d.extend)
	}
}

// scoreRollShownVel is the velocity a note's stalk shows: its own, or the
// dynamics' level where it starts.
func scoreRollShownVel(li int, n edit.RollNote) (int, bool) {
	if n.Vel > 0 {
		return n.Vel, true
	}
	if c := scEd.laneCurve(li); c != nil {
		return int(math.Round(float64(c.Effective.At(n.Start.TicksFloat()) * 127))), false
	}
	return 80, false
}

// scoreRollVelocityLane is the velocity lane under the roll.
func scoreRollVelocityLane(dl imgui.DrawList, g scoreTLGeom) {
	e := scEd
	li := e.rollLine()
	top, bottom := g.velTop, g.velBottom
	dl.AddRectFilled(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.gridX, Y: bottom}, scoreColTLLeft)
	dl.AddRectFilled(imgui.Vec2{X: g.gridX, Y: top}, imgui.Vec2{X: g.right, Y: bottom}, scoreRGB(0x16, 0x18, 0x1c, 1))
	dl.AddLine(imgui.Vec2{X: g.pos.X, Y: top + 0.5}, imgui.Vec2{X: g.right, Y: top + 0.5}, scoreColEdge)
	dl.AddText(imgui.Vec2{X: g.pos.X + 12, Y: top + 8}, scoreColDim, "VELOCITY")
	dl.AddText(imgui.Vec2{X: g.pos.X + 12, Y: top + 26}, scoreColDim, "drag a stalk")
	if e.line(li) == nil {
		return
	}
	ns := e.rollNotes()
	yOf := func(v int) float32 { return bottom - 4 - float32(v)/127*(scoreRollVelH-10) }
	d := &scoreRollDrag
	dl.PushClipRect(imgui.Vec2{X: g.gridX, Y: top}, imgui.Vec2{X: g.right, Y: bottom})
	scoreTLBars(dl, g, top, bottom, false)
	for _, n := range ns {
		x := g.x(n.Start) + 1
		if x < g.gridX-2 || x > g.right+2 {
			continue
		}
		v, own := scoreRollShownVel(li, n)
		if d.kind == scoreRollVelocity && (n.Staff == d.note.Staff && n.Voice == d.note.Voice && n.Start.Eq(d.note.Start) || e.rollSelected(d.note) && e.rollSelected(n)) {
			v, own = d.vel, true
		}
		col := scoreColVelDynamic
		if own {
			col = scoreRollVoiceCols[n.Voice%score.MaxVoices]
		}
		dl.AddLineV(imgui.Vec2{X: x, Y: bottom - 4}, imgui.Vec2{X: x, Y: yOf(v)}, col, 2)
		dl.AddCircleFilled(imgui.Vec2{X: x, Y: yOf(v)}, 3, col)
		if e.rollSelected(n) {
			dl.AddCircleV(imgui.Vec2{X: x, Y: yOf(v)}, 5, scoreColRollSel, 10, 1)
		}
	}
	dl.PopClipRect()

	imgui.SetCursorScreenPos(imgui.Vec2{X: g.gridX, Y: top})
	imgui.InvisibleButton("##sctlvel", imgui.Vec2{X: max(g.right-g.gridX, 1), Y: scoreRollVelH})
	m := imgui.MousePos()
	velAt := func() int { return clampI(int(math.Round(float64((bottom-4-m.Y)/(scoreRollVelH-10)*127))), 1, 127) }
	stalk := -1
	best := float32(5)
	for i, n := range ns {
		if dx := float32(math.Abs(float64(g.x(n.Start) + 1 - m.X))); dx <= best {
			stalk, best = i, dx
		}
	}
	switch {
	case imgui.IsItemActivated() && imgui.IsMouseDown(0) && stalk >= 0:
		d.kind, d.note, d.vel, d.moved = scoreRollVelocity, ns[stalk], velAt(), false
	case imgui.IsItemActive() && d.kind == scoreRollVelocity:
		if v := velAt(); v != d.vel {
			d.vel, d.moved = v, true
		}
	}
	if imgui.IsItemDeactivated() && d.kind == scoreRollVelocity {
		n, v := d.note, d.vel
		d.kind = scoreRollNone
		e.rollVelocity(li, n, v)
	}
	if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) && stalk >= 0 {
		// Back to the dynamics; the press is no drag.
		d.kind = scoreRollNone
		e.rollVelocity(li, ns[stalk], 0)
	}
	if imgui.IsItemHovered() && stalk >= 0 && d.kind == scoreRollNone {
		v, own := scoreRollShownVel(li, ns[stalk])
		from := "set on the note"
		if !own {
			from = "from the dynamics"
		}
		imgui.SetTooltip(fmt.Sprintf("Velocity %d (%s)\nDrag: set it. Double-click: back to the dynamics.", v, from))
	}
}

// scoreTLScrollbar is the time axis's scroll bar along the bottom.
func scoreTLScrollbar(dl imgui.DrawList, g scoreTLGeom) {
	end := scoreTimeEnd(scEd.Score).Float() + 1
	top := g.scrollTop
	dl.AddRectFilled(imgui.Vec2{X: g.pos.X, Y: top}, imgui.Vec2{X: g.right, Y: top + scoreTLScrollH}, scoreColRuler)
	width := g.right - g.gridX
	visible := float64(width) / float64(g.zoom)
	frac := math.Min(visible/end, 1)
	tw := max(float32(frac)*width, 24)
	tx := g.gridX + float32(g.scroll/end)*width
	imgui.SetCursorScreenPos(imgui.Vec2{X: g.gridX, Y: top})
	imgui.InvisibleButton("##sctlhscroll", imgui.Vec2{X: max(width, 1), Y: scoreTLScrollH})
	m := imgui.MousePos()
	switch {
	case imgui.IsItemActivated():
		if m.X < tx || m.X > tx+tw {
			// A click off the thumb puts the thumb's middle there.
			scoreTLScroll = math.Max(0, float64(m.X-g.gridX-tw/2)/float64(width)*end)
			tx = g.gridX + float32(scoreTLScroll/end)*width
		}
		scoreTLHScroll.on, scoreTLHScroll.x0, scoreTLHScroll.scroll = true, m.X, scoreTLScroll
	case imgui.IsItemActive() && scoreTLHScroll.on:
		scoreTLScroll = math.Max(0, scoreTLHScroll.scroll+float64(m.X-scoreTLHScroll.x0)/float64(width)*end)
	}
	if imgui.IsItemDeactivated() {
		scoreTLHScroll.on = false
	}
	col := scoreColFrame
	if imgui.IsItemHovered() || imgui.IsItemActive() {
		col = scoreColFrameHi
	}
	dl.AddRectFilledV(imgui.Vec2{X: tx, Y: top + 3}, imgui.Vec2{X: tx + tw, Y: top + scoreTLScrollH - 3}, col, 4, imgui.DrawFlagsRoundCornersAll)
}

// scoreTLPlayhead is the play head down the tracks and the roll.
func scoreTLPlayhead(dl imgui.DrawList, g scoreTLGeom) {
	t, ok := scoreTLHeadTime()
	if !ok {
		return
	}
	x := g.x(t)
	if x < g.gridX || x > g.right {
		return
	}
	dl.AddLineV(imgui.Vec2{X: x, Y: g.tracksTop}, imgui.Vec2{X: x, Y: g.tracksBottom}, scoreColPlay, 1.5)
	dl.AddLineV(imgui.Vec2{X: x, Y: g.rollTop}, imgui.Vec2{X: x, Y: g.velBottom}, scoreColPlay, 1.5)
}

// scoreTimelinePopups are the add-lane menu and a breakpoint's menu.
func scoreTimelinePopups() {
	e := scEd
	if scoreTLAddOpen {
		scoreTLAddOpen = false
		imgui.SetNextWindowPos(scoreTLMenuPos)
		imgui.OpenPopup("##sctladdmenu")
	}
	if scoreTLPointOpen {
		scoreTLPointOpen = false
		imgui.SetNextWindowPos(scoreTLMenuPos)
		imgui.OpenPopup("##sctlptmenu")
	}
	if imgui.BeginPopup("##sctladdmenu") {
		li := scoreTLMenuLine
		editorTextMuted("Automation lane for " + e.ownerName(li))
		ch := e.Score.ChainOf(li)
		insert := -1
		for _, t := range e.autoTargets(li) {
			if t.Kind == score.LaneFX {
				if slot := score.ChainSlot(*ch, t); slot != insert {
					insert = slot
					imgui.Separator()
				}
			}
			has := e.Score.LaneOf(li, t.Name()) != nil
			if imgui.MenuItemV(t.Label()+"##sctlt"+t.Name(), "", has, !has) {
				e.autoAdd(li, t)
			}
		}
		if ch != nil && len(*ch) == 0 {
			imgui.Separator()
			what := "the line has an insert"
			if li < 0 {
				what = "it has an effect"
			}
			editorTextMuted("An effect's settings appear here\nonce " + what + " (Mixer tab)")
		}
		imgui.EndPopup()
	}
	if imgui.BeginPopup("##sctlptmenu") {
		li, name, i := scoreTLMenuLine, scoreTLMenuLane, scoreTLMenuPoint
		ln := e.Score.LaneOf(li, name)
		var p *score.LanePoint
		if ln != nil && i < len(ln.Points) {
			p = &ln.Points[i]
		}
		if p == nil {
			imgui.CloseCurrentPopup()
			imgui.EndPopup()
			return
		}
		t, _ := scoreLaneTarget(li, name)
		editorTextMuted(fmt.Sprintf("%s at %s: %s", t.Label(), e.Score.FormatPos(p.At), t.Format(p.Value)))
		editorTextMuted("The segment after it:")
		if imgui.MenuItemV("Straight##sctlsh0", "", p.Shape == score.LaneLinear, !t.Stepped()) {
			e.autoShape(li, name, i, score.LaneLinear, 0)
		}
		if imgui.MenuItemV("Step (hold, then jump)##sctlsh1", "", p.Shape == score.LaneStep, true) {
			e.autoShape(li, name, i, score.LaneStep, 0)
		}
		if imgui.MenuItemV("Curve##sctlsh2", "", p.Shape == score.LaneCurve, !t.Stepped()) {
			e.autoShape(li, name, i, score.LaneCurve, 0.5)
		}
		if p.Shape == score.LaneCurve {
			c := p.Curve
			imgui.SetNextItemWidth(160)
			if imgui.SliderFloatV("Bend##sctlbend", &c, -1, 1, "%.2f", 0) {
				// One step per drag of the slider.
				e.mixChange(fmt.Sprintf("bend%d%s%d", li, name, i), func() {
					edit.SetLaneShape(e.Score, li, name, i, score.LaneCurve, c)
				})
				e.touch()
			}
			if imgui.IsItemDeactivatedAfterEdit() {
				e.endMixGesture()
			}
		}
		imgui.Separator()
		if imgui.MenuItemV("Delete Point##sctldel", "Delete", false, len(ln.Points) > 1) {
			e.TL.AutoSel = scoreAutoSel{Line: li, Lane: name, Index: i}
			e.autoDelete()
		}
		imgui.EndPopup()
	}
}
