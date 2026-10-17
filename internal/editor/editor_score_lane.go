package editor

// editor_score_lane.go — the dynamics lane on the paper: drawn under a
// line's staves in the room the engraver leaves there (engrave.Options.Below,
// List.Lanes), and dragged.
//
// The lane is mockup screen 1's Violin I lane. Its height is the marks'
// scale (silence at the bottom, the loudest dynamic at the top). The dashed
// grey curve is what the printed marks and hairpins give (the line's
// Written curve, interpret.WrittenDynamics); the solid blue one is what
// plays (Effective: the overrides laid over it). Each override is a blue
// level line with a handle, shaded down to the lane's floor and labelled
// with its level.
//
// Under the pointer: a press on an override's level line takes it (a drag
// up or down moves its level); a press anywhere else in the lane starts a
// new override there, and the drag stretches it over the beats it crosses
// at the pointer's level. The editing is scoreState's (lanePress, laneMove,
// laneRelease in editor_score_marks.go), which changes the score as the
// drag goes so the player hears it, and makes one undo step at the end.
// Delete removes the selected override.

import (
	"fmt"
	"math"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/interpret"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// The lane's look, after the mockup.
var (
	scoreColLane        = scoreRGB(0xee, 0xf3, 0xfb, 1)
	scoreColLaneEdge    = scoreRGB(0xc9, 0xd6, 0xea, 1)
	scoreColLaneWritten = scoreRGB(0x7c, 0x8a, 0xa3, 1)
	scoreColLaneCurve   = scoreRGB(0x2c, 0x5f, 0xae, 1)
	scoreColLaneFill    = scoreRGB(0x1f, 0x6f, 0xe0, 0.16)
	scoreColLaneFillSel = scoreRGB(0x1f, 0x6f, 0xe0, 0.26)
	scoreColLaneLabel   = scoreRGB(0x3d, 0x5d, 0x93, 1)
)

// scoreLanePad is the room above and below the curve inside a lane, in
// staff spaces: a level of 1 sits that far under the lane's top.
const scoreLanePad = 0.4

// scoreLaneGrab is how near an override's level line a press takes it, in
// staff spaces.
const scoreLaneGrab = 0.7

// scoreLaneY is a level's y in a lane.
func scoreLaneY(ln engrave.LaneBox, v float32) float64 {
	return ln.Top + scoreLanePad + float64(1-v)*(ln.Height-2*scoreLanePad)
}

// scoreLaneLevel is the level a y in a lane means, clamped to what an
// override holds.
func scoreLaneLevel(ln engrave.LaneBox, y float64) float32 {
	return laneLevel(float32(1 - (y-ln.Top-scoreLanePad)/(ln.Height-2*scoreLanePad)))
}

// scoreLaneAt is the lane under a point of the layout.
func scoreLaneAt(l *engrave.List, p engrave.Point) (engrave.LaneBox, bool) {
	if len(l.Bars) == 0 {
		return engrave.LaneBox{}, false
	}
	x0, x1 := l.Bars[0].X0, l.Bars[len(l.Bars)-1].X1
	for _, ln := range l.Lanes {
		if p.Y >= ln.Top && p.Y < ln.Top+ln.Height && p.X >= x0 && p.X <= x1 {
			return ln, true
		}
	}
	return engrave.LaneBox{}, false
}

// scoreNormPos writes a bar's end as the next bar's start, the form the
// overrides keep.
func scoreNormPos(s *score.Score, p score.Pos) score.Pos {
	if p.Bar >= 1 && !p.Offset.Less(s.BarLength(p.Bar)) {
		return score.Pos{Bar: p.Bar + 1}
	}
	return p
}

// scoreLanePosAtX is the beat an x in a lane means.
func scoreLanePosAtX(l *engrave.List, s *score.Score, x float64) (score.Pos, bool) {
	p, ok := scorePosAtX(l, s, x)
	return scoreNormPos(s, p), ok
}

// scoreLaneOverrideX is an override's span on the layout.
func scoreLaneOverrideX(l *engrave.List, s *score.Score, o score.DynOverride) (xa, xb float64, ok bool) {
	xa, ok = scorePosX(l, s, o.From)
	if !ok {
		return 0, 0, false
	}
	to := o.To
	if to.Bar > s.Measures() && to.Offset.IsZero() {
		to = score.Pos{Bar: s.Measures(), Offset: s.BarLength(s.Measures())}
	}
	xb, ok = scorePosX(l, s, to)
	return xa, xb, ok
}

// scoreLaneHit is the override of a line whose level line is under a
// point, -1 for none.
func scoreLaneHit(l *engrave.List, s *score.Score, ln engrave.LaneBox, p engrave.Point) int {
	line := s.Lines[ln.Line]
	best, bd := -1, scoreLaneGrab
	for i, o := range line.Overrides {
		xa, xb, ok := scoreLaneOverrideX(l, s, o)
		if !ok || p.X < xa || p.X > xb {
			continue
		}
		if d := math.Abs(p.Y - scoreLaneY(ln, o.Level)); d <= bd {
			best, bd = i, d
		}
	}
	return best
}

// scoreLanePointer takes the pointer for the lanes: a drag going on, or a
// press in a lane. inLane reports the pointer over a lane (no ghost note
// there, and its click is the lane's).
func scoreLanePointer(l *engrave.List, g scoreViewGeom, hovered, clicked bool) (inLane bool) {
	e := scEd
	p := g.toLayout(imgui.MousePos())
	if e.laneDrag.On {
		if !imgui.IsMouseDown(0) {
			e.laneRelease()
			return true
		}
		ln := engrave.LaneBox{Line: -1}
		for _, b := range l.Lanes {
			if b.Line == e.laneDrag.Line {
				ln = b
			}
		}
		if ln.Line < 0 || e.laneDrag.Line >= len(e.Score.Lines) {
			e.laneRelease()
			return true
		}
		if e.laneDrag.Create {
			at, ok := scoreLanePosAtX(l, e.Score, p.X)
			if ok {
				e.laneMove(at, scoreLaneLevel(ln, p.Y))
			}
		} else {
			e.laneMove(e.laneDrag.At, scoreLaneLevel(ln, p.Y))
			imgui.SetMouseCursor(imgui.MouseCursorResizeNS)
		}
		return true
	}
	if !hovered {
		return false
	}
	ln, ok := scoreLaneAt(l, p)
	if !ok || ln.Line >= len(e.Score.Lines) {
		return false
	}
	hit := scoreLaneHit(l, e.Score, ln, p)
	if hit >= 0 {
		imgui.SetMouseCursor(imgui.MouseCursorResizeNS)
	}
	if clicked {
		at, ok := scoreLanePosAtX(l, e.Score, p.X)
		if ok {
			e.lanePress(ln.Line, at, scoreLaneLevel(ln, p.Y), hit)
			if hit >= 0 {
				// The override is the selection now: Delete removes it.
				e.Sel = scoreSel{}
				o := e.Score.Lines[ln.Line].Overrides[hit]
				e.Status = fmt.Sprintf("%s held at %.2f (%s) from %s to %s: drag up or down, Delete removes it",
					e.lineName(ln.Line), o.Level, scoreDynName(o.Level), e.Score.FormatPos(o.From), e.Score.FormatPos(o.To))
			}
		}
	}
	return true
}

// scoreDrawLanes draws every open lane in sight.
func scoreDrawLanes(dl imgui.DrawList, l *engrave.List, g scoreViewGeom) {
	e := scEd
	if len(l.Lanes) == 0 || len(l.Bars) == 0 {
		return
	}
	for _, ln := range l.Lanes {
		if ln.Line >= len(e.Score.Lines) {
			continue
		}
		top := g.toScreen(engrave.Point{Y: ln.Top}).Y
		bot := g.toScreen(engrave.Point{Y: ln.Top + ln.Height}).Y
		if bot < g.visMin.Y || top > g.visMax.Y {
			continue
		}
		scoreDrawLane(dl, l, g, ln)
	}
}

// scoreDrawLane draws one lane: its box and barlines, the written curve
// dashed, the overrides, the effective curve, the labels.
func scoreDrawLane(dl imgui.DrawList, l *engrave.List, g scoreViewGeom, ln engrave.LaneBox) {
	e := scEd
	s := e.Score
	x0, x1 := l.Bars[0].X0, l.Bars[len(l.Bars)-1].X1
	a := g.toScreen(engrave.Point{X: x0, Y: ln.Top})
	b := g.toScreen(engrave.Point{X: x1, Y: ln.Top + ln.Height})
	dl.AddRectFilled(a, b, scoreColLane)
	dl.AddRectV(a, b, scoreColLaneEdge, 0, 0, 1)
	for _, bb := range l.Bars[1:] {
		x := g.toScreen(engrave.Point{X: bb.X0}).X
		if x >= g.visMin.X && x <= g.visMax.X {
			dl.AddLineV(imgui.Vec2{X: x, Y: a.Y}, imgui.Vec2{X: x, Y: b.Y}, scoreColLaneEdge, 0.8)
		}
	}
	// The overrides under the curves, shaded down to the floor.
	sel := e.laneSelected()
	for i := range s.Lines[ln.Line].Overrides {
		o := s.Lines[ln.Line].Overrides[i]
		xa, xb, ok := scoreLaneOverrideX(l, s, o)
		if !ok {
			continue
		}
		pa := g.toScreen(engrave.Point{X: xa, Y: scoreLaneY(ln, o.Level)})
		pb := g.toScreen(engrave.Point{X: xb, Y: ln.Top + ln.Height})
		fill := scoreColLaneFill
		if sel != nil && e.LaneSel.Line == ln.Line && e.LaneSel.Index == i {
			fill = scoreColLaneFillSel
		}
		dl.AddRectFilled(pa, pb, fill)
		dl.AddLineV(pa, imgui.Vec2{X: pb.X, Y: pa.Y}, scoreColSel, 2.6)
		mid := (pa.X + pb.X) / 2
		dl.AddRectFilledV(imgui.Vec2{X: mid - 9, Y: pa.Y - 4}, imgui.Vec2{X: mid + 9, Y: pa.Y + 4}, scoreColSel, 3, imgui.DrawFlagsRoundCornersAll)
		label := fmt.Sprintf("override %.2f", o.Level)
		ly := max(a.Y+1, pa.Y-17)
		if w := imgui.CalcTextSize(label, false, 0).X; w < pb.X-pa.X-6 {
			dl.AddText(imgui.Vec2{X: pa.X + 4, Y: ly}, scoreColLaneLabel, label)
		}
	}
	// The curves, across the bars in sight.
	ds := e.laneCurve(ln.Line)
	if ds != nil {
		written := scoreLaneCurvePoints(l, g, s, ln, &ds.Written)
		scoreDashedPolyline(dl, written, scoreColLaneWritten, 1.2, 3, 3)
		eff := scoreLaneCurvePoints(l, g, s, ln, &ds.Effective)
		for i := 1; i < len(eff); i++ {
			dl.AddLineV(eff[i-1], eff[i], scoreColLaneCurve, 1.6)
		}
	}
	label := "dashed: from the marks"
	w := imgui.CalcTextSize(label, false, 0).X
	right := min(b.X, g.visMax.X) - 4
	dl.AddText(imgui.Vec2{X: right - w, Y: a.Y + 1}, scoreColLaneLabel, label)
}

// scoreLaneCurvePoints samples a curve (written ticks) across the bars in
// sight: at every column and every few pixels between, so ramps and steps
// both read.
func scoreLaneCurvePoints(l *engrave.List, g scoreViewGeom, s *score.Score, ln engrave.LaneBox, c *interpret.Curve) []imgui.Vec2 {
	starts := s.BarStarts()
	var pts []imgui.Vec2
	step := 3 / float64(g.zoom) // about 3 px
	clip := g.clip()
	for _, bb := range l.Bars {
		if bb.X1 < clip.Min.X || bb.X0 > clip.Max.X {
			continue
		}
		if bb.Bar < 1 || bb.Bar > len(starts) {
			continue
		}
		t0 := s.PosFrac(score.Pos{Bar: bb.Bar}, starts).TicksFloat()
		put := func(x float64) {
			off := 0.0
			if x > bb.Content {
				off = bb.OffsetAt(x)
			}
			tick := t0 + off*score.TicksPerWhole
			// The bar's last instant belongs to it, not the next bar's
			// first mark.
			if x >= bb.X1 {
				tick -= 1e-6
			}
			pts = append(pts, g.toScreen(engrave.Point{X: x, Y: scoreLaneY(ln, c.At(tick))}))
		}
		for x := bb.X0; x < bb.X1; x += step {
			put(x)
		}
		put(bb.X1)
	}
	return pts
}

// scoreDashedPolyline draws a polyline dashed, the dash pattern carried
// across its corners.
func scoreDashedPolyline(dl imgui.DrawList, pts []imgui.Vec2, col imgui.PackedColor, thick, dash, gap float32) {
	on, left := true, dash
	for i := 1; i < len(pts); i++ {
		p, q := pts[i-1], pts[i]
		dx, dy := q.X-p.X, q.Y-p.Y
		segLen := float32(math.Hypot(float64(dx), float64(dy)))
		if segLen == 0 {
			continue
		}
		at := float32(0)
		for at < segLen {
			n := min(left, segLen-at)
			if on {
				a := imgui.Vec2{X: p.X + dx*at/segLen, Y: p.Y + dy*at/segLen}
				z := imgui.Vec2{X: p.X + dx*(at+n)/segLen, Y: p.Y + dy*(at+n)/segLen}
				dl.AddLineV(a, z, col, thick)
			}
			at += n
			left -= n
			if left <= 0 {
				on = !on
				left = dash
				if !on {
					left = gap
				}
			}
		}
	}
}

// scoreLaneHeaderLabels label the open lanes in the header column.
func scoreLaneHeaderLabels(dl imgui.DrawList, l *engrave.List, g scoreViewGeom, wp imgui.Vec2) {
	for _, ln := range l.Lanes {
		if ln.Line >= len(scEd.Score.Lines) {
			continue
		}
		y := g.toScreen(engrave.Point{Y: ln.Top}).Y
		dl.AddText(imgui.Vec2{X: wp.X + 12, Y: y + 2}, scoreColDim, "Dynamics lane")
		dl.AddText(imgui.Vec2{X: wp.X + 12, Y: y + 16}, scoreColDim, scoreClipText(scEd.lineName(ln.Line)+" volume", scoreHeaderW-30))
	}
}
