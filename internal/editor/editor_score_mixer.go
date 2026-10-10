package editor

// editor_score_mixer.go — the Score window's Mixer tab (mockup screen 3): a
// strip per line — its name and instrument, the reverb and delay sends and
// pan as knobs, a fader with a live meter, the level in dB, mute and solo —
// then the two send buses' returns and the master fader with its meter.
//
// The faders are the same values as the staff headers' (Line.Volume and so
// on, in the file); moving one goes through scoreState's mixer methods, so it
// is heard at once while the score plays (editor_score_lines.go) and a drag is
// one undo step. A line's insert effects are listed as the file has them;
// the effects rack that plays them is phase 4's (docs/score/SPEC.md).

import (
	"fmt"
	"math"

	"foundry/internal/score"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	scoreStripW   = float32(112)
	scoreStripGap = float32(6)
)

var (
	scoreColStrip       = scoreRGB(0x1e, 0x21, 0x27, 1)
	scoreColStripEdge   = scoreRGB(0x2b, 0x30, 0x39, 1)
	scoreColBusStrip    = scoreRGB(0x1f, 0x23, 0x2b, 1)
	scoreColMasterStrip = scoreRGB(0x20, 0x27, 0x33, 1)
	scoreColMasterEdge  = scoreRGB(0x34, 0x50, 0x7a, 1)
	scoreColMixerBg     = imgui.Vec4{X: 0.102, Y: 0.110, Z: 0.129, W: 1}
	scoreColKnobFace    = scoreRGB(0x14, 0x16, 0x1a, 1)
	scoreColKnobRing    = scoreRGB(0x3a, 0x3f, 0x4a, 1)
	scoreColKnobLine    = scoreRGB(0x7f, 0xb3, 0xff, 1)
	scoreColSlot        = scoreRGB(0x14, 0x16, 0x1a, 1)
)

// scoreMasterMeterVal is the master meter as shown, falling like the lines'.
var scoreMasterMeterVal float32

// scoreMasterLevel is the mix's recent peak, from the player's scope.
func scoreMasterLevel() float32 {
	lvl := float32(0)
	if scorePlayer != nil {
		var buf [512]float32
		scorePlayer.Scope(buf[:])
		for _, v := range buf {
			lvl = max(lvl, float32(math.Abs(float64(v))))
		}
	}
	scoreMasterMeterVal = max(lvl, scoreMasterMeterVal*0.9)
	return scoreMasterMeterVal
}

// scoreMixerPane draws the mixer in the main area.
func scoreMixerPane(pos, size imgui.Vec2) {
	e := scEd
	n := len(e.Score.Lines) + 3
	contentW := 2*10 + float32(n)*(scoreStripW+scoreStripGap)
	imgui.SetNextWindowPos(pos)
	imgui.SetNextWindowSize(size)
	imgui.SetNextWindowContentSize(imgui.Vec2{X: contentW, Y: 0})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{X: 10, Y: 10})
	imgui.PushStyleColor(imgui.StyleColorWindowBg, scoreColMixerBg)
	imgui.BeginV("##scoremixer", nil, trkPaneFlags|imgui.WindowFlagsHorizontalScrollbar)
	imgui.PopStyleColor()
	imgui.PopStyleVar()
	wp := imgui.WindowPos()
	x0 := wp.X + 10 - imgui.ScrollX()
	// Tall enough for a long fader throw, not the whole window.
	stripH := min(max(size.Y-20-scoreScrollW, 280), 440)
	for li := range e.Score.Lines {
		scoreLineStrip(li, imgui.Vec2{X: x0 + float32(li)*(scoreStripW+scoreStripGap), Y: wp.Y + 10}, stripH)
	}
	bx := x0 + float32(len(e.Score.Lines))*(scoreStripW+scoreStripGap) + 8
	scoreBusStrip(0, imgui.Vec2{X: bx, Y: wp.Y + 10}, stripH)
	scoreBusStrip(1, imgui.Vec2{X: bx + scoreStripW + scoreStripGap, Y: wp.Y + 10}, stripH)
	scoreMasterStrip(imgui.Vec2{X: bx + 2*(scoreStripW+scoreStripGap), Y: wp.Y + 10}, stripH)
	imgui.End()
}

// scoreStripFrame draws a strip's box and its title lines, and returns the
// y below them.
func scoreStripFrame(at imgui.Vec2, h float32, fill, edge imgui.PackedColor, title, sub string) float32 {
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(at, imgui.Vec2{X: at.X + scoreStripW, Y: at.Y + h}, fill, 6, imgui.DrawFlagsRoundCornersAll)
	dl.AddRectV(at, imgui.Vec2{X: at.X + scoreStripW, Y: at.Y + h}, edge, 6, imgui.DrawFlagsRoundCornersAll, 1)
	dl.AddText(imgui.Vec2{X: at.X + 7, Y: at.Y + 7}, scoreColText, scoreClipText(title, scoreStripW-14))
	dl.AddText(imgui.Vec2{X: at.X + 7, Y: at.Y + 23}, scoreColDim, scoreClipText(sub, scoreStripW-14))
	return at.Y + 42
}

// scoreLineStrip is line li's strip.
func scoreLineStrip(li int, at imgui.Vec2, h float32) {
	e := scEd
	l := e.line(li)
	imgui.PushID(fmt.Sprintf("scmix%d", li))
	defer imgui.PopID()
	y := scoreStripFrame(at, h, scoreColStrip, scoreColStripEdge, e.lineName(li), scoreInstrumentLabel(l.Instrument))
	// The title takes clicks: the name selects the line, the instrument
	// opens the picker, a right-click the line's commands.
	imgui.SetCursorScreenPos(imgui.Vec2{X: at.X + 4, Y: at.Y + 5})
	if imgui.InvisibleButton("##scmixname", imgui.Vec2{X: scoreStripW - 8, Y: 16}) {
		e.selectLine(li)
	}
	editorMenuTooltip(e.lineName(li) + "\nClick: select the line. Right-click: line commands.")
	if imgui.IsItemHovered() && imgui.IsMouseClicked(1) {
		scoreLineOpen, scoreMenuLine = true, li
	}
	imgui.SetCursorScreenPos(imgui.Vec2{X: at.X + 4, Y: at.Y + 21})
	if imgui.InvisibleButton("##scmixinst", imgui.Vec2{X: scoreStripW - 8, Y: 16}) {
		scoreOpenPicker(li, imgui.Vec2{X: at.X, Y: at.Y + 40})
	}
	editorMenuTooltip(l.Instrument + scoreInstrumentNote(li) + "\nClick to play the line on another instrument.")
	// Inserts, as stored.
	dl := imgui.WindowDrawList()
	for _, fx := range l.Inserts {
		dl.AddRectFilledV(imgui.Vec2{X: at.X + 7, Y: y}, imgui.Vec2{X: at.X + scoreStripW - 7, Y: y + 18}, scoreColSlot, 3, imgui.DrawFlagsRoundCornersAll)
		dl.AddText(imgui.Vec2{X: at.X + 12, Y: y + 2}, scoreColDim, scoreClipText(fx.Kind, scoreStripW-24))
		imgui.SetCursorScreenPos(imgui.Vec2{X: at.X + 7, Y: y})
		imgui.Dummy(imgui.Vec2{X: scoreStripW - 14, Y: 18})
		editorMenuTooltip(fx.Kind + " insert: kept in the file; the effects rack that plays it comes in phase 4.")
		y += 22
	}
	// Knobs: the two sends and pan.
	ky := y + 4
	kw := (scoreStripW - 14) / 3
	knob := func(k int, id, label string, v, lo, hi, def float32, set func(float32), tip string) {
		c := imgui.Vec2{X: at.X + 7 + kw*float32(k) + kw/2, Y: ky + 13}
		if nv, changed, released := scoreKnob(id, c, 11, v, lo, hi, def, label); changed {
			set(nv)
		} else if released {
			e.endMixGesture()
		}
		editorMenuTooltip(tip)
	}
	knob(0, "##scka", "Rev", l.SendA, 0, 1, 0, func(v float32) { e.setSend(li, 0, v) }, fmt.Sprintf("Reverb send (bus A) %.0f%%; double-click: 0", l.SendA*100))
	knob(1, "##sckb", "Dly", l.SendB, 0, 1, 0, func(v float32) { e.setSend(li, 1, v) }, fmt.Sprintf("Delay send (bus B) %.0f%%; double-click: 0", l.SendB*100))
	knob(2, "##sckp", "Pan", l.Pan, -1, 1, 0, func(v float32) { e.setPan(li, v) }, "Pan "+scorePanText(l.Pan)+"; double-click: centre")
	y = ky + 46
	// Fader and meter, then the level and M/S at the bottom.
	fh := at.Y + h - 50 - y
	if nv, changed, released := scoreVFader("##scfader", imgui.Vec2{X: at.X + 30, Y: y}, imgui.Vec2{X: 26, Y: fh}, l.Volume, 0, scoreFaderMax, 1); changed {
		e.setVolume(li, nv)
	} else if released {
		e.endMixGesture()
	}
	editorMenuTooltip(fmt.Sprintf("%s volume %s (double-click: 0 dB). The same fader as the staff header's.", e.lineName(li), scoreDB(l.Volume)))
	ma := imgui.Vec2{X: at.X + 66, Y: y}
	mb := imgui.Vec2{X: at.X + 73, Y: y + fh}
	dl.AddRectFilledV(ma, mb, scoreColTrack, 2, imgui.DrawFlagsRoundCornersAll)
	scoreDrawMeterBar(dl, ma, mb, scoreMeter(li))
	db := scoreDB(l.Volume)
	tw := imgui.CalcTextSize(db, false, 0).X
	dl.AddText(imgui.Vec2{X: at.X + (scoreStripW-tw)/2, Y: at.Y + h - 44}, scoreColText, db)
	if scoreSmallButton("M##scmm", imgui.Vec2{X: at.X + scoreStripW/2 - 23, Y: at.Y + h - 24}, l.Mute, scoreColMute) {
		e.toggleMute(li)
	}
	editorMenuTooltip("Mute")
	if scoreSmallButton("S##scms", imgui.Vec2{X: at.X + scoreStripW/2 + 3, Y: at.Y + h - 24}, l.Solo, scoreColSolo) {
		e.toggleSolo(li)
	}
	editorMenuTooltip("Solo")
}

// scoreBusStrip is send bus 0 (A, the reverb) or 1 (B, the delay): its
// effects as stored and its return fader.
func scoreBusStrip(bus int, at imgui.Vec2, h float32) {
	e := scEd
	b := e.Score.Buses.A
	if bus == 1 {
		b = e.Score.Buses.B
	}
	imgui.PushID(fmt.Sprintf("scbus%d", bus))
	defer imgui.PopID()
	y := scoreStripFrame(at, h, scoreColBusStrip, scoreColStripEdge, scoreBusName(bus), "send bus "+string(rune('A'+bus)))
	dl := imgui.WindowDrawList()
	for _, fx := range b.FX {
		dl.AddRectFilledV(imgui.Vec2{X: at.X + 7, Y: y}, imgui.Vec2{X: at.X + scoreStripW - 7, Y: y + 18}, scoreColSlot, 3, imgui.DrawFlagsRoundCornersAll)
		dl.AddText(imgui.Vec2{X: at.X + 12, Y: y + 2}, scoreColDim, scoreClipText(scoreFXWords(fx.Kind, fx.Params), scoreStripW-24))
		y += 22
	}
	y += 50
	fh := at.Y + h - 50 - y
	if nv, changed, released := scoreVFader("##scbusfader", imgui.Vec2{X: at.X + (scoreStripW-26)/2, Y: y}, imgui.Vec2{X: 26, Y: fh}, b.Return, 0, 1, 1); changed {
		e.setReturn(bus, nv)
	} else if released {
		e.endMixGesture()
	}
	editorMenuTooltip(fmt.Sprintf("%s return %s: how much of the bus comes back into the mix", scoreBusName(bus), scoreDB(b.Return)))
	db := scoreDB(b.Return)
	tw := imgui.CalcTextSize(db, false, 0).X
	dl.AddText(imgui.Vec2{X: at.X + (scoreStripW-tw)/2, Y: at.Y + h - 44}, scoreColText, db)
}

// scoreMasterStrip is the master fader and the mix's meter.
func scoreMasterStrip(at imgui.Vec2, h float32) {
	e := scEd
	imgui.PushID("scmaster")
	defer imgui.PopID()
	y := scoreStripFrame(at, h, scoreColMasterStrip, scoreColMasterEdge, "Master", "whole file")
	dl := imgui.WindowDrawList()
	for _, fx := range e.Score.Master {
		dl.AddRectFilledV(imgui.Vec2{X: at.X + 7, Y: y}, imgui.Vec2{X: at.X + scoreStripW - 7, Y: y + 18}, scoreColSlot, 3, imgui.DrawFlagsRoundCornersAll)
		dl.AddText(imgui.Vec2{X: at.X + 12, Y: y + 2}, scoreColDim, scoreClipText(scoreFXWords(fx.Kind, fx.Params), scoreStripW-24))
		y += 22
	}
	y += 50
	fh := at.Y + h - 50 - y
	g := e.Score.Gain
	if nv, changed, released := scoreVFader("##scmasterfader", imgui.Vec2{X: at.X + 30, Y: y}, imgui.Vec2{X: 26, Y: fh}, g, scoreMasterMin, scoreFaderMax, 1); changed {
		e.setGain(nv)
	} else if released {
		e.endMixGesture()
	}
	editorMenuTooltip("Master level " + scoreDB(g) + " (double-click: 0 dB): the score's gain, in the game too")
	ma := imgui.Vec2{X: at.X + 66, Y: y}
	mb := imgui.Vec2{X: at.X + 73, Y: y + fh}
	dl.AddRectFilledV(ma, mb, scoreColTrack, 2, imgui.DrawFlagsRoundCornersAll)
	scoreDrawMeterBar(dl, ma, mb, scoreMasterLevel())
	db := scoreDB(g)
	tw := imgui.CalcTextSize(db, false, 0).X
	dl.AddText(imgui.Vec2{X: at.X + (scoreStripW-tw)/2, Y: at.Y + h - 44}, scoreColText, db)
}

// scoreFXWords is an effect's slot text: "reverb", "compressor (off)".
func scoreFXWords(kind string, params []score.FXParam) string {
	for _, p := range params {
		if p.Name == "enabled" && p.Value == 0 {
			return kind + " (off)"
		}
	}
	return kind
}

// ---------- knob and fader ----------

// scoreKnob is a rotary control centred at c: drag up or down to turn it
// (150 px from end to end), double-click for def. A label under it.
func scoreKnob(id string, c imgui.Vec2, r, v, lo, hi, def float32, label string) (nv float32, changed, released bool) {
	imgui.SetCursorScreenPos(imgui.Vec2{X: c.X - r - 2, Y: c.Y - r - 2})
	imgui.InvisibleButton(id, imgui.Vec2{X: 2*r + 4, Y: 2*r + 4})
	active, hov := imgui.IsItemActive(), imgui.IsItemHovered()
	released = imgui.IsItemDeactivated()
	nv = v
	switch {
	case hov && imgui.IsMouseDoubleClicked(0):
		nv, changed = def, v != def
	case active:
		if dy := imgui.CurrentIO().MouseDelta().Y; dy != 0 {
			nv = clampF32(v-dy*(hi-lo)/150, lo, hi)
			changed = nv != v
		}
	}
	dl := imgui.WindowDrawList()
	ring := scoreColKnobRing
	if hov || active {
		ring = scoreColAccent
	}
	dl.AddCircleFilled(c, r, scoreColKnobFace)
	dl.AddCircleV(c, r, ring, 0, 1)
	f := (nv - lo) / (hi - lo)
	ang := float64(-135+f*270) * math.Pi / 180
	tip := imgui.Vec2{X: c.X + float32(math.Sin(ang))*(r-2), Y: c.Y - float32(math.Cos(ang))*(r-2)}
	dl.AddLineV(c, tip, scoreColKnobLine, 2)
	tw := imgui.CalcTextSize(label, false, 0).X
	dl.AddText(imgui.Vec2{X: c.X - tw/2, Y: c.Y + r + 3}, scoreColDim, label)
	return nv, changed, released
}

// scoreVFader is a vertical fader: a slot and a cap, drag or click to set,
// double-click for def (with a tick at def).
func scoreVFader(id string, at, size imgui.Vec2, v, lo, hi, def float32) (nv float32, changed, released bool) {
	imgui.SetCursorScreenPos(at)
	imgui.InvisibleButton(id, size)
	active, hov := imgui.IsItemActive(), imgui.IsItemHovered()
	released = imgui.IsItemDeactivated()
	capH := float32(14)
	y0, y1 := at.Y+capH/2, at.Y+size.Y-capH/2
	nv = v
	switch {
	case hov && imgui.IsMouseDoubleClicked(0):
		nv, changed = def, v != def
	case active:
		f := clampF32((y1-imgui.MousePos().Y)/max(y1-y0, 1), 0, 1)
		nv = lo + f*(hi-lo)
		changed = nv != v
	}
	dl := imgui.WindowDrawList()
	cx := at.X + size.X/2
	dl.AddRectFilledV(imgui.Vec2{X: cx - 2, Y: y0}, imgui.Vec2{X: cx + 2, Y: y1}, scoreColTrack, 2, imgui.DrawFlagsRoundCornersAll)
	if def > lo && def < hi {
		dy := y1 - (y1-y0)*(def-lo)/(hi-lo)
		dl.AddLine(imgui.Vec2{X: at.X + 2, Y: dy}, imgui.Vec2{X: at.X + size.X - 2, Y: dy}, scoreColDim)
	}
	f := clampF32((nv-lo)/(hi-lo), 0, 1)
	cy := y1 - (y1-y0)*f
	col := scoreColKnob
	if hov || active {
		col = scoreRGB(0xff, 0xff, 0xff, 1)
	}
	dl.AddRectFilledV(imgui.Vec2{X: at.X, Y: cy - capH/2}, imgui.Vec2{X: at.X + size.X, Y: cy + capH/2}, col, 3, imgui.DrawFlagsRoundCornersAll)
	dl.AddLine(imgui.Vec2{X: at.X + 3, Y: cy}, imgui.Vec2{X: at.X + size.X - 3, Y: cy}, scoreColTrack)
	return nv, changed, released
}
