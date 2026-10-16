package editor

// editor_score_fx_test.go — effects through scoreState, through the
// window's player sync with a real scheduler rendering offline, and
// through the Mixer tab's real ImGui frame.

import (
	"math"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/tracker/fx"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func TestScoreEffectsEditing(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	rev, mix := e.Rev, e.MixRev
	if !e.addFX(0, fx.KindEQ) || !e.addFX(0, fx.KindDelay) {
		t.Fatal(e.Status)
	}
	l := e.Score.Lines[0]
	if len(l.Inserts) != 2 || l.Inserts[0].Kind != "eq" || e.Rev != rev || e.MixRev == mix {
		t.Fatalf("add: %+v rev %d mix %d", l.Inserts, e.Rev, e.MixRev)
	}
	// Every setting is written out at its default, so the file says what plays.
	if v := l.Inserts[1].Param("mix", -1); v != 0.3 {
		t.Errorf("a new delay insert's mix: %g", v)
	}
	// A knob dragged: one undo step, clamped to the range.
	undo := e.undo.len()
	for _, v := range []float64{-3, -6, -40} {
		e.setFXParam(0, 0, "low", v)
	}
	if e.undo.len() != undo+1 || l.Inserts[0].Param("low", 0) != -24 {
		t.Errorf("drag: %d steps, low %g", e.undo.len()-undo, l.Inserts[0].Param("low", 0))
	}
	e.endMixGesture()
	e.toggleFXBypass(0, 1)
	if !l.Inserts[1].Bypass {
		t.Error("bypass")
	}
	if !e.moveFX(0, 1, -1) || l.Inserts[0].Kind != "delay" || e.moveFX(0, 0, -1) {
		t.Errorf("move: %+v", l.Inserts)
	}
	e.resetFX(0, 1)
	if l.Inserts[1].Param("low", 1) != 0 {
		t.Error("reset")
	}
	if !e.removeFX(0, 0) || len(l.Inserts) != 1 {
		t.Error("remove")
	}
	// The buses and the master take effects too, with bus defaults (wet).
	e.addFX(scoreFXBusA, fx.KindChorus)
	if f := e.Score.Buses.A.FX[len(e.Score.Buses.A.FX)-1]; f.Kind != "chorus" || f.Param("mix", 0) != 1 {
		t.Errorf("bus A chorus: %+v", f)
	}
	e.addFX(scoreFXMaster, fx.KindLimiter)
	if n := len(e.Score.Master); n != 1 || e.Score.Master[0].Kind != "limiter" {
		t.Errorf("master: %+v", e.Score.Master)
	}
	e.togglePre(0, 1)
	e.setBusPan(1, -0.5)
	e.toggleBusMute(1)
	if !l.PreB || e.Score.Buses.B.Pan != -0.5 || !e.Score.Buses.B.Mute {
		t.Errorf("pre %v, bus b %+v", l.PreB, e.Score.Buses.B)
	}
	for len(l.Inserts) < edit.MaxEffects {
		e.addFX(0, fx.KindDrive)
	}
	if e.addFX(0, fx.KindDrive) {
		t.Error("a full chain took another effect")
	}
	if e.Rev != rev {
		t.Error("effects counted as changes to the music")
	}
	// Undo walks it all back.
	for e.canUndo() {
		e.undoStep()
	}
	if l := e.Score.Lines[0]; len(l.Inserts) != 0 || l.PreB || e.Score.Buses.B.Mute || len(e.Score.Master) != 0 {
		t.Errorf("after undo: %+v %+v", l.Inserts, e.Score.Buses.B)
	}

	// A line whose lanes move an insert: adding an EQ in front renames
	// them, and it is a change to the performance.
	e.addFX(1, fx.KindEQ)
	b := e.Score.Lines[1]
	b.Lanes = []score.Lane{{Name: "eq.low", Points: []score.LanePoint{{At: score.Pos{Bar: 1}, Value: -6}}}}
	rev = e.Rev
	e.addFX(1, fx.KindEQ)
	e.moveFX(1, 1, -1)
	if e.Rev == rev || b.Lanes[0].Name != "eq#2.low" {
		t.Errorf("lanes after the new EQ moved first: %s, rev %d -> %d", e.Score.Lines[1].Lanes[0].Name, rev, e.Rev)
	}
	e.removeFX(1, 1)
	if len(e.Score.Lines[1].Lanes) != 0 {
		t.Errorf("removing the EQ kept its lane: %+v", e.Score.Lines[1].Lanes)
	}
}

func TestScoreEffectsHeardLive(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	p := scoreWithPlayer(t, e)
	t.Cleanup(func() { scoreSentFX = nil })
	e.togglePlay(false, 0)
	scoreApplyOps()
	left := scoreLevelAt(scoreRender(p, 0.2))
	perf := p.Performance()
	// An EQ on A at -6 dB: a new chain, the same performance.
	e.addFX(0, fx.KindEQ)
	e.setFXParam(0, 0, "output", -6.0206)
	e.endMixGesture()
	scoreApplyOps()
	out := scoreRender(p, 0.1)
	if l := scoreLevelAt(out); math.Abs(float64(l/left)-0.5) > 1e-3 {
		t.Errorf("A through an EQ at -6 dB: %g of %g", l, left)
	}
	chain := p.Engine().Chain(0)
	if chain == nil || p.Performance() != perf {
		t.Fatal("the insert was not heard live, or the score was prepared again")
	}
	// A knob turned: no new chain, heard.
	e.setFXParam(0, 0, "output", -12.0412)
	e.endMixGesture()
	scoreApplyOps()
	out = scoreRender(p, 0.1)
	if l := scoreLevelAt(out); math.Abs(float64(l/left)-0.25) > 1e-3 || p.Engine().Chain(0) != chain {
		t.Errorf("the knob at -12 dB: %g of the level, chain kept %v", l/left, p.Engine().Chain(0) == chain)
	}
	// Bypassed and back.
	e.toggleFXBypass(0, 0)
	scoreApplyOps()
	if l := scoreLevelAt(scoreRender(p, 0.1)); math.Abs(float64(l-left)) > 1e-4 {
		t.Errorf("bypassed: %g, want %g", l, left)
	}
	// A pre-fader send to bus A (an empty bus returns what it is sent),
	// the fader down: A is heard only through the bus.
	e.toggleFXBypass(0, 0)
	e.setVolume(0, 0)
	e.endMixGesture()
	e.setSend(0, 0, 1)
	e.endMixGesture()
	e.togglePre(0, 0)
	e.Score.Buses.A.FX = nil // (as a file might have it)
	e.setReturn(0, 1)
	e.endMixGesture()
	scoreApplyOps()
	if l := scoreLevelAt(scoreRender(p, 0.1)); math.Abs(float64(l/left)-0.25) > 1e-3 {
		t.Errorf("A pre-fader through bus A: %g of the level", l/left)
	}
	e.toggleBusMute(0)
	scoreApplyOps()
	if l := scoreLevelAt(scoreRender(p, 0.1)); l != 0 {
		t.Errorf("bus A muted: %g", l)
	}
	if p.Performance() != perf {
		t.Error("an effects change prepared the score again")
	}
}

// The Mixer tab: "+ Insert" opens the menu, an effect's name its settings,
// the dot bypasses; every popup draws.
func TestScoreMixerEffectsThroughTheView(t *testing.T) {
	scoreDrawTest(t)
	prevView := scoreView
	t.Cleanup(func() { scoreView = prevView })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		scoreShowKeypad = false
		scoreView = scoreViewMixer
		away := imgui.Vec2{X: 700, Y: 895}
		scoreFrame(away, false)
		top := scoreViewTop + 10
		slot := imgui.Vec2{X: 10 + 7 + 20, Y: top + 42 + 9}
		// The first line's "+ Insert".
		scoreClickAt(slot)
		scoreFrame(away, false)
		if scoreFXTarget != 0 {
			t.Fatalf("the add menu is for %d", scoreFXTarget)
		}
		scEd.addFX(0, fx.KindFilter) // what picking "Filter" in it does
		scoreClickAt(away)
		// The slot's name opens its settings; its dot bypasses it.
		scoreClickAt(slot)
		scoreFrame(away, false)
		if scoreFXTarget != 0 || scoreFXSlot != 0 {
			t.Errorf("settings for %d/%d", scoreFXTarget, scoreFXSlot)
		}
		scoreClickAt(away)
		scoreClickAt(imgui.Vec2{X: 10 + scoreStripW - 7 - 9, Y: top + 42 + 9})
		if f := scEd.fxAt(0, 0); f == nil || !f.Bypass {
			t.Errorf("the dot did not bypass the filter (%s)", scEd.Status)
		}
		// Every popup draws, for a line, a bus and the master.
		scEd.addFX(scoreFXBusB, fx.KindPhaser)
		scEd.addFX(scoreFXMaster, fx.KindCompressor)
		for _, target := range []int{0, scoreFXBusB, scoreFXMaster} {
			for _, open := range []func(){
				func() { scoreFXAddOpen, scoreFXTarget = true, target },
				func() { scoreFXSlotOpen, scoreFXTarget, scoreFXSlot = true, target, 0 },
				func() { scoreOpenFXEdit(target, 0, imgui.Vec2{X: 200, Y: 200}) },
			} {
				open()
				scoreFrame(away, false)
				scoreFrame(away, false)
				imgui.CloseCurrentPopup()
			}
		}
		// Many effects: the strip says how many it cannot show.
		for len(scEd.Score.Lines[0].Inserts) < edit.MaxEffects {
			scEd.addFX(0, fx.KindEQ)
		}
		scoreFrame(away, false)
		scoreView = scoreViewScore
		scoreFrame(away, false)
	})
}

// Swing and humanise: the score's and a line's own, one undo step a drag,
// heard (the player performs the score again); the popup draws for both.
func TestScoreSwingAndHumanise(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	p := scoreWithPlayer(t, e)
	perf0 := p.Performance()
	rev, undo := e.Rev, e.undo.len()
	for _, a := range []float32{0.55, 0.6, 0.66} {
		e.setSwing(-1, score.Swing{Amount: a})
	}
	e.endMixGesture()
	if e.undo.len() != undo+1 || e.Rev == rev || e.Score.Playback.Swing != (score.Swing{Amount: 0.66, Unit: 8}) {
		t.Errorf("swing drag: %d steps, %+v", e.undo.len()-undo, e.Score.Playback.Swing)
	}
	// Line B plays straight under it; its own humanise takes the score's seed.
	e.setSwing(1, score.Swing{Amount: 0.5})
	e.setHumanize(-1, score.Humanize{Seed: 9})
	e.endMixGesture()
	e.setHumanize(1, score.Humanize{Timing: 80, Velocity: 0.1})
	if h := e.Score.Lines[1].Humanize; h == nil || h.Timing != 50 {
		t.Errorf("a line's humanise: %+v (timing clamped to 50 ms)", h)
	}
	scoreApplyOps()
	scoreSyncPlayer(false)
	perf := p.Performance()
	if perf == perf0 {
		t.Fatal("the player did not perform the score again")
	}
	var a, b []int64
	for _, n := range perf.Notes {
		if n.Bar == 1 && n.Line == 0 {
			a = append(a, n.Tick)
		}
		if n.Bar == 1 && n.Line == 1 {
			b = append(b, n.Tick)
		}
	}
	if len(a) != 4 || len(b) != 4 || a[1] != 960 || b[1] != 960 {
		// Quarters are whole pairs: swing leaves them where they are.
		t.Errorf("quarters under swing: %v %v", a, b)
	}
	e.clearOwnFeel(1, true)
	if e.Score.Lines[1].Swing != nil {
		t.Error("the line kept its own swing")
	}
	for e.undo.len() > undo {
		e.undoStep()
	}
	if e.Score.Playback.Swing != (score.Swing{}) || e.Score.Lines[1].Humanize != nil {
		t.Errorf("undo: %+v", e.Score.Playback)
	}
}

func TestScoreFeelPopupDraws(t *testing.T) {
	scoreDrawTest(t)
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		away := imgui.Vec2{X: 700, Y: 895}
		for _, li := range []int{-1, 0} {
			scoreOpenFeel()
			scoreFeelLine = li
			scoreFrame(away, false)
			scoreFrame(away, false)
			imgui.CloseCurrentPopup()
		}
		scEd.setSwing(0, score.Swing{Amount: 0.6, Unit: 16})
		scEd.setHumanize(0, score.Humanize{Timing: 5})
		scoreOpenFeel()
		scoreFeelLine = 0
		scoreFrame(away, false)
		scoreFrame(away, false)
	})
}
