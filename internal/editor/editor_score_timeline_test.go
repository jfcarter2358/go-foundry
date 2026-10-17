package editor

import (
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/tracker/fx"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func fr(n, d int64) score.Frac { return score.F(n, d) }

func TestScoreGridSnap(t *testing.T) {
	s := score.New("t")
	s.Bars = 2
	g16 := scoreGrid{Div: 16}
	for _, c := range []struct {
		t     score.Frac
		g     scoreGrid
		floor bool
		want  score.Frac
	}{
		{fr(7, 64), g16, false, fr(1, 8)},
		{fr(7, 64), g16, true, fr(1, 16)},
		{fr(1, 10), scoreGrid{Div: 8, Triplet: true}, false, fr(1, 12)},
		{fr(7, 4), scoreGrid{Div: 4}, false, fr(7, 4)},
		{fr(9, 4), g16, false, fr(2, 1)}, // past the end: the end
		{fr(-1, 4), g16, false, fr(0, 1)},
	} {
		if got := scoreGridSnap(s, c.t, c.g, c.floor); !got.Eq(c.want) {
			t.Errorf("%s on %s (floor %v): %s, want %s", c.t, c.g.Label(), c.floor, got, c.want)
		}
	}
	// A pickup counts its grid from where its full bar would start.
	s.Pickup = fr(3, 8)
	if got := scoreGridSnap(s, fr(2, 10), scoreGrid{Div: 4}, true); !got.Eq(fr(1, 8)) {
		t.Errorf("pickup grid: %s", got)
	}
	if u := (scoreGrid{Div: 8, Triplet: true}).Unit(); !u.Eq(fr(1, 12)) {
		t.Errorf("eighth triplet %s", u)
	}
}

// The piano roll's commands write notation and keep both views' selections
// in step; each is one undo step.
func TestScoreRollEditing(t *testing.T) {
	e := scoreTestState(t)
	// G major: a drawn F is an F#.
	if !e.rollDraw(0, fr(0, 1), fr(3, 8), 78) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1); got != "q.F#5 er hr" {
		t.Errorf("drawn: %q", got)
	}
	if e.Sel.Kind != scoreSelEvent || e.Sel.Loc.Bar != 1 || len(e.TL.RollSel) != 1 {
		t.Errorf("the drawn note is selected in both views: %+v %d", e.Sel, len(e.TL.RollSel))
	}
	if !strings.HasPrefix(e.Status, "Wrote F#5, bar 1 beat 1, dotted quarter") {
		t.Errorf("status %q", e.Status)
	}
	if as := e.takeAuditions(); len(as) != 1 || as[0].MIDI[0] != 78 {
		t.Errorf("auditions %+v", as)
	}
	// A note over the first one goes into voice 2.
	if !e.rollDraw(0, fr(1, 4), fr(1, 2), 74) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 1, 1); got != "qr qD5 hr" {
		t.Errorf("voice 2: %q", got)
	}
	// Move the F# a beat later and a tone up; it keeps its voice.
	n := e.rollNotes()[0]
	if n.MIDI() != 78 {
		t.Fatalf("first note %+v", n)
	}
	e.rollSelect(0, []edit.RollNote{n}, false)
	if !e.rollMove(0, n, fr(1, 2), 2, score.Frac{}) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1); got != "hr q.G#5 er" {
		t.Errorf("moved: %q", got)
	}
	// Stretch it over the barline.
	n = e.TL.RollSel[0]
	if !e.rollMove(0, n, score.Frac{}, 0, fr(1, 4)) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1) + " | " + barOf(e, 0, 0, 2); got != "hr hG#5~ | eG#5 er qr hr" {
		t.Errorf("stretched: %q", got)
	}
	// Its velocity, then back to the dynamics.
	n = e.TL.RollSel[0]
	if !e.rollVelocity(0, n, 101) || e.TL.RollSel[0].Vel != 101 {
		t.Fatalf("velocity: %s %+v", e.Status, e.TL.RollSel)
	}
	if !strings.Contains(barOf(e, 0, 0, 1), "vel=101") {
		t.Errorf("velocity mark: %q", barOf(e, 0, 0, 1))
	}
	steps := e.undo.len()
	if !e.rollDelete(0) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1); got != "R" {
		t.Errorf("deleted: %q", got)
	}
	if e.undo.len() != steps+1 {
		t.Errorf("a delete is one step: %d -> %d", steps, e.undo.len())
	}
	scoreUndoOn(e)
	if got := barOf(e, 0, 0, 1); !strings.Contains(got, "G#5") {
		t.Errorf("undo: %q", got)
	}
	// A box selects what it holds; a click on the staff selects in the roll.
	e.rollSelectInBox(0, fr(0, 1), fr(1, 1), 70, 80, false)
	if len(e.TL.RollSel) != 2 {
		t.Errorf("box: %d notes", len(e.TL.RollSel))
	}
}

// scoreUndoOn undoes one step on a state that is not scEd.
func scoreUndoOn(e *scoreState) {
	if e.undoStep() {
		e.dropStaleSel()
	}
}

// A grand staff puts a drawn note by its pitch; a transposing staff spells
// it in its own key.
func TestScoreRollStavesAndSpelling(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Lines = append(e.Score.Lines, scoreGrandStaffLine())
	if !e.rollDraw(2, fr(0, 1), fr(1, 4), 48) || !e.rollDraw(2, fr(0, 1), fr(1, 4), 64) {
		t.Fatal(e.Status)
	}
	bass := score.FormatMeasure(edit.Voice(e.Score, 2, 1, 0).Measure(1))
	treble := score.FormatMeasure(edit.Voice(e.Score, 2, 0, 0).Measure(1))
	if bass != "qC3 qr hr" || treble != "qE4 qr hr" {
		t.Errorf("grand staff: treble %q bass %q", treble, bass)
	}
	// A B-flat clarinet in G major is written in A: a sounding F (MIDI 65)
	// is a written G, stored as a sounding F, not E#.
	cl := score.NewLine("Clarinet", "woodwinds/clarinet", "treble")
	cl.Staves[0].Transpose = score.Interval{Diatonic: -1, Chromatic: -2}
	e.Score.Lines = append(e.Score.Lines, cl)
	if !e.rollDraw(3, fr(0, 1), fr(1, 4), 65) {
		t.Fatal(e.Status)
	}
	if got := score.FormatMeasure(edit.Voice(e.Score, 3, 0, 0).Measure(1)); got != "qF4 qr hr" {
		t.Errorf("clarinet: %q", got)
	}
}

// Lane editing: a lane added, a point added by a press and dragged (one
// step, heard as it moves), its shape set, a stroke drawn, a point deleted.
func TestScoreAutoLaneEditing(t *testing.T) {
	e := scoreTestState(t)
	if !e.autoAdd(0, score.LaneTarget{Kind: score.LanePan}) {
		t.Fatal(e.Status)
	}
	if !e.tlOpen(0) {
		t.Error("adding a lane shows the line's lanes")
	}
	steps := e.undo.len()
	live := e.laneLive
	e.autoPress(0, "pan", score.Pos{Bar: 2}, 0.5, -1)
	e.autoMove(score.Pos{Bar: 2, Offset: fr(1, 4)}, -0.5)
	e.autoMove(score.Pos{Bar: 3}, -0.75)
	if e.laneLive == live {
		t.Error("a drag is heard as it goes")
	}
	e.autoRelease()
	if e.undo.len() != steps+1 {
		t.Errorf("a drag is one step: %d -> %d", steps, e.undo.len())
	}
	ln := e.Score.Lines[0].Lane("pan")
	if len(ln.Points) != 2 || ln.Points[1].At.Bar != 3 || ln.Points[1].Value != -0.75 {
		t.Fatalf("points %+v", ln.Points)
	}
	if !strings.Contains(e.Status, "L75") {
		t.Errorf("status %q", e.Status)
	}
	if !e.autoShape(0, "pan", 0, score.LaneStep, 0) || ln.Points[0].Shape != score.LaneStep {
		t.Errorf("shape: %s", e.Status)
	}
	// A press on a point takes it; without a move nothing is a step.
	steps = e.undo.len()
	e.autoPress(0, "pan", score.Pos{Bar: 3}, -0.75, 1)
	e.autoRelease()
	if e.undo.len() != steps || e.TL.AutoSel.Index != 1 {
		t.Errorf("a click on a point: %d steps, selection %+v", e.undo.len()-steps, e.TL.AutoSel)
	}
	if !e.autoDelete() || len(ln.Points) != 1 {
		t.Errorf("delete: %s %+v", e.Status, ln.Points)
	}
	// The draw tool: a stroke across bars 2-3.
	e.TL.Tool = scoreToolDraw
	e.autoPress(0, "pan", score.Pos{Bar: 2}, 0, -1)
	for k := int64(1); k <= 8; k++ {
		e.autoMove(score.Pos{Bar: 2, Offset: fr(k, 8)}, float32(k)/8)
	}
	e.autoRelease()
	ln = e.Score.Lines[0].Lane("pan")
	if last := ln.Points[len(ln.Points)-1]; len(ln.Points) < 3 || last.Value != 1 || last.At.Bar != 3 {
		t.Errorf("stroke: %+v (%s)", ln.Points, e.Status)
	}
	if !e.autoRemove(0, "pan") || e.Score.Lines[0].Lanes != nil {
		t.Errorf("remove: %s", e.Status)
	}
	// An effect's settings are offered once the line has the effect.
	if n := len(e.autoTargets(0)); n != 5 {
		t.Errorf("%d targets without inserts", n)
	}
}

func TestScoreTrackSwitches(t *testing.T) {
	e := scoreTestState(t)
	e.toggleArm(1)
	if !e.armed(1) || e.rollLine() != 1 {
		t.Errorf("armed %v, roll line %d", e.armed(1), e.rollLine())
	}
	e.toggleAutoLanes(0)
	e.renameLine(0, "Piccolo")
	if !e.tlOpen(0) {
		t.Error("the lanes switch follows a rename")
	}
	e.toggleArm(1)
	if e.armed(1) {
		t.Error("disarmed")
	}
}

// The Timeline through the real frame: the tabs, a note drawn with the Draw
// tool by a drag on the roll, a click on a track's region, a lane point
// added and dragged, the velocity lane, and every popup — ImGui's stacks
// even throughout.
func TestScoreTimelineThroughTheView(t *testing.T) {
	scoreDrawTest(t)
	prevView, prevZoom, prevScroll, prevTop := scoreView, scoreTLZoom, scoreTLScroll, scoreRollTop
	t.Cleanup(func() {
		scoreView, scoreTLZoom, scoreTLScroll, scoreRollTop, scoreRollTopFor = prevView, prevZoom, prevScroll, prevTop, -1
		scoreRollDrag = scoreRollPress{}
	})
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		away := imgui.Vec2{X: 5, Y: 5}
		scoreView = scoreViewTimeline
		scoreRollTopFor = -1
		scoreFrame(away, false)
		area := imgui.Vec2{X: 1400, Y: 900 - scoreViewTop - scoreStatusH}
		g := scoreTimelineGeom(imgui.Vec2{X: 0, Y: scoreViewTop}, area)
		click := func(a, b imgui.Vec2) {
			scoreFrame(a, false)
			scoreFrame(a, true)
			scoreFrame(b, true)
			scoreFrame(b, true)
			scoreFrame(b, false)
		}
		// Draw a G5 from beat 2 to beat 3 of bar 1 in the Flute's roll.
		scEd.TL.Tool = scoreToolDraw
		row := func(m int) float32 { return g.y(m) + g.keyH/2 }
		click(imgui.Vec2{X: g.x(fr(1, 4)) + 2, Y: row(79)}, imgui.Vec2{X: g.x(fr(1, 2)) + 1, Y: row(79)})
		if got := barOf(scEd, 0, 0, 1); got != "qr qG5 hr" {
			t.Fatalf("drawn through the roll: %q (status %q)", got, scEd.Status)
		}
		// Drag it a beat later with the Select tool.
		scEd.TL.Tool = scoreToolSelect
		click(imgui.Vec2{X: g.x(fr(1, 4)) + 8, Y: row(79)}, imgui.Vec2{X: g.x(fr(1, 2)) + 8, Y: row(79)})
		if got := barOf(scEd, 0, 0, 1); got != "hr qG5 qr" {
			t.Errorf("moved through the roll: %q (status %q)", got, scEd.Status)
		}
		// The velocity lane: drag the note's stalk to the top.
		x := g.x(fr(1, 2)) + 1
		click(imgui.Vec2{X: x, Y: g.velBottom - 10}, imgui.Vec2{X: x, Y: g.velTop + 2})
		if got := barOf(scEd, 0, 0, 1); got != "hr qG5(vel=127) qr" {
			t.Errorf("velocity through the lane: %q (status %q)", got, scEd.Status)
		}
		// A double-click on the stalk gives it back to the dynamics.
		vp := imgui.Vec2{X: x, Y: g.velBottom - 20}
		scoreFrame(vp, false)
		scoreFrame(vp, true)
		scoreFrame(vp, false)
		scoreFrame(vp, true)
		scoreFrame(vp, false)
		if got := barOf(scEd, 0, 0, 1); got != "hr qG5 qr" {
			t.Errorf("velocity cleared: %q (status %q)", got, scEd.Status)
		}
		// A double-click on empty roll draws a 16th there.
		dp := imgui.Vec2{X: g.x(fr(1, 1)) + 3, Y: row(76)}
		scoreFrame(dp, false)
		scoreFrame(dp, true)
		scoreFrame(dp, false)
		scoreFrame(dp, true)
		scoreFrame(dp, false)
		if got := barOf(scEd, 0, 0, 2); got != "sE5 e.r qr hr" || !strings.HasPrefix(scEd.Status, "Wrote E5") {
			t.Errorf("double-click draw: %q (status %q)", got, scEd.Status)
		}
		// A click on the Cello's region selects its bar 2.
		y := g.tracksTop + scoreTLRowH + scoreTLRowH/2
		bx := g.x(fr(5, 4))
		scoreFrame(imgui.Vec2{X: bx, Y: y}, false)
		scoreFrame(imgui.Vec2{X: bx, Y: y}, true)
		scoreFrame(imgui.Vec2{X: bx, Y: y}, false)
		if q := scEd.Sel; q.Kind != scoreSelBars || q.LineA != 1 || q.BarA != 2 {
			t.Errorf("region click: %+v", q)
		}
		if scEd.rollLine() != 1 {
			t.Errorf("the roll follows: line %d", scEd.rollLine())
		}
		// A volume lane on the Flute, a point added by a click in it.
		scEd.Sel = scoreSel{}
		scEd.autoAdd(0, score.LaneTarget{Kind: score.LaneVolume})
		scoreFrame(away, false)
		g = scoreTimelineGeom(imgui.Vec2{X: 0, Y: scoreViewTop}, area)
		ly := g.tracksTop + scoreTLRowH
		px := g.x(fr(2, 1))
		click(imgui.Vec2{X: px, Y: ly + scoreTLLaneH/2}, imgui.Vec2{X: px, Y: ly + 8})
		ln := scEd.Score.Lines[0].Lane("volume")
		if len(ln.Points) != 2 || ln.Points[1].At.Bar != 3 || ln.Points[1].Value <= 1 {
			t.Errorf("lane through the view: %+v (status %q)", ln.Points, scEd.Status)
		}
		// The master's track: its + offers its lanes; a volume lane, and a
		// point added by a click in it.
		rowOf := func(owner int, lane string) (scoreTLRow, bool) {
			for _, r := range scoreTLRows(scEd) {
				if r.line == owner && r.lane == lane {
					return r, true
				}
			}
			return scoreTLRow{}, false
		}
		mr, ok := rowOf(score.OwnerMaster, "")
		if !ok {
			t.Fatal("no master track")
		}
		plus := imgui.Vec2{X: g.pos.X + 125 + 10, Y: g.tracksTop + mr.y + 7 + 8}
		click(plus, plus)
		if scoreTLMenuLine != score.OwnerMaster {
			t.Errorf("the master's + opened nothing (menu for %d)", scoreTLMenuLine)
		}
		// A click on nothing (the velocity lane's label) closes the menu.
		nothing := imgui.Vec2{X: g.pos.X + 10, Y: g.velTop + 40}
		click(nothing, nothing)
		if !scEd.autoAdd(score.OwnerMaster, score.LaneTarget{Kind: score.LaneVolume}) || !scEd.tlOpen(score.OwnerMaster) {
			t.Fatalf("master volume lane: %s", scEd.Status)
		}
		scoreFrame(away, false)
		lr, ok := rowOf(score.OwnerMaster, "volume")
		if !ok {
			t.Fatal("the master's volume lane has no row")
		}
		mly := g.tracksTop + lr.y
		click(imgui.Vec2{X: px, Y: mly + scoreTLLaneH/2}, imgui.Vec2{X: px, Y: mly + scoreTLLaneH - 10})
		if mln := scEd.Score.MasterLanes; len(mln) != 1 || len(mln[0].Points) != 2 || mln[0].Points[1].At.Bar != 3 || mln[0].Points[1].Value >= 1 {
			t.Errorf("master lane through the view: %+v (status %q)", mln, scEd.Status)
		}
		// Its A hides the lanes again.
		ab := imgui.Vec2{X: g.pos.X + 104 + 8, Y: g.tracksTop + mr.y + 7 + 8}
		click(ab, ab)
		if scEd.tlOpen(score.OwnerMaster) {
			t.Errorf("the master's A did not hide its lanes (status %q)", scEd.Status)
		}
		// Every popup and a drag's ghost draw.
		scoreTLAddOpen, scoreTLMenuLine = true, score.OwnerBusA
		scoreFrame(away, false)
		scoreFrame(away, false)
		imgui.CloseCurrentPopup()
		scoreTLAddOpen, scoreTLMenuLine = true, 0
		scoreFrame(away, false)
		scoreFrame(away, false)
		imgui.CloseCurrentPopup()
		scoreTLPointOpen, scoreTLMenuLine, scoreTLMenuLane, scoreTLMenuPoint = true, 0, "volume", 1
		scoreFrame(away, false)
		scoreFrame(away, false)
		scoreRollDrag = scoreRollPress{kind: scoreRollBoxing, press: imgui.Vec2{X: g.gridX + 10, Y: g.rollTop + 10}}
		scoreFrame(imgui.Vec2{X: g.gridX + 200, Y: g.rollTop + 80}, false)
		scoreRollDrag = scoreRollPress{}
		// The tabs: Score, then back.
		scoreRunCmd(scTimeline)
		if scoreView != scoreViewScore {
			t.Errorf("Timeline command from the timeline: view %d", scoreView)
		}
		scoreRunCmd(scTimeline)
		scoreFrame(away, false)
	})
}

// The buses' and the master's lanes on the editing model: their targets,
// a lane added, dragged and removed, and a bus effect's lanes following it
// when the Mixer moves or removes the effect.
func TestScoreMixOwnerLanes(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Buses.A.FX = []score.FX{score.NewFX(fx.KindReverb, fx.Bus)}
	got := map[string]bool{}
	for _, tg := range e.autoTargets(score.OwnerBusA) {
		got[tg.Name()] = true
	}
	if !got["return"] || !got["pan"] || !got["reverb.size"] || got["volume"] || got["cutoff"] {
		t.Errorf("bus A's targets: %v", got)
	}
	if ts := e.autoTargets(score.OwnerMaster); len(ts) == 0 || ts[0].Kind != score.LaneVolume {
		t.Errorf("the master's targets: %+v", ts)
	}
	if !e.autoAdd(score.OwnerBusA, score.LaneTarget{Kind: score.LaneReturn}) {
		t.Fatal(e.Status)
	}
	steps := e.undo.len()
	e.autoPress(score.OwnerBusA, "return", score.Pos{Bar: 2}, 0.1, -1)
	e.autoMove(score.Pos{Bar: 3}, 0.2)
	e.autoRelease()
	if ln := e.Score.LaneOf(score.OwnerBusA, "return"); e.undo.len() != steps+1 || ln == nil || len(ln.Points) != 2 || ln.Points[1].Value != 0.2 {
		t.Errorf("a bus lane drag: %+v, %d steps", ln, e.undo.len()-steps)
	}
	if !strings.HasPrefix(e.Status, "Bus A · Return:") {
		t.Errorf("status %q", e.Status)
	}
	// A reverb lane; then a second reverb added in front through the
	// Mixer renames it to follow its effect, as one undo step.
	reverb, _ := score.ChainTargetForSlot(e.Score.Buses.A.FX, 0, fx.KindReverb.Spec().Param("size"))
	if !e.autoAdd(score.OwnerBusA, reverb) {
		t.Fatal(e.Status)
	}
	if !e.addFX(scoreFXBusA, fx.KindEQ) || !e.moveFX(scoreFXBusA, 1, -1) {
		t.Fatal(e.Status)
	}
	if e.Score.LaneOf(score.OwnerBusA, "reverb.size") == nil {
		t.Errorf("the reverb lane after moving an EQ in front: %+v", e.Score.Buses.A.Lanes)
	}
	if !e.removeFX(scoreFXBusA, 1) || e.Score.LaneOf(score.OwnerBusA, "reverb.size") != nil || !strings.Contains(e.Status, "1 lane") {
		t.Errorf("removing the reverb: %q, lanes %+v", e.Status, e.Score.Buses.A.Lanes)
	}
	if !e.autoRemove(score.OwnerBusA, "return") || e.Score.Buses.A.Lanes != nil {
		t.Errorf("remove: %s", e.Status)
	}
}
