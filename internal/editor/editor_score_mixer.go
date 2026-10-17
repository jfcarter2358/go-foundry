package editor

// editor_score_mixer.go — the Score window's Mixer tab (mockup screen 3): a
// strip per line — its name and instrument, its insert effects, the reverb
// and delay sends and pan as knobs, a fader with a live meter, the level in
// dB, mute and solo — then the two send buses (their effects, pan, return
// fader, meter, mute) and the master (its chain, fader and meter).
//
// The faders are the same values as the staff headers' (Line.Volume and so
// on, in the file); moving one goes through scoreState's mixer methods, so it
// is heard at once while the score plays (editor_score_lines.go) and a drag is
// one undo step. An effect slot: click its name for its settings (a popup of
// sliders, heard as they move), its dot to bypass it, right-click to move or
// remove it; "+ Insert" adds one (editor_score_fx.go has what each does). A
// send knob's right-click makes it pre-fader, marked "pre" under it.

import (
	"fmt"
	"math"

	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/tracker/fx"

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
	scoreColSlotEdge    = scoreRGB(0x2c, 0x31, 0x3a, 1)
	scoreColSlotEmpty   = scoreRGB(0x5d, 0x64, 0x70, 1)
	scoreColFXOn        = scoreRGB(0x6c, 0xd4, 0x9a, 1)
	scoreColFXOff       = scoreRGB(0x4a, 0x50, 0x5a, 1)
)

// The effect popups: which chain and slot they are for, and where they open.
var (
	scoreFXAddOpen, scoreFXSlotOpen, scoreFXEditOpen bool
	scoreFXTarget, scoreFXSlot                       int
	scoreFXPos                                       imgui.Vec2
)

// scoreBusMeterVals are the bus meters as shown, falling like the lines'.
var scoreBusMeterVals [2]float32

func scoreBusMeter(b int) float32 {
	lvl := float32(0)
	if scorePlayer != nil {
		lvl = scorePlayer.BusLevel(b)
	}
	scoreBusMeterVals[b] = max(lvl, scoreBusMeterVals[b]*0.9)
	return scoreBusMeterVals[b]
}

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
	// Inserts, and room under them for the knobs and a fader.
	dl := imgui.WindowDrawList()
	y = scoreFXSlots(li, l.Inserts, at.X, y, at.Y+h-200)
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
	sendTip := func(bus int, v float32, pre bool) string {
		return fmt.Sprintf("%s send (bus %c) %.0f%%, %s; double-click: 0; right-click: %s", scoreBusName(bus), 'A'+bus, v*100,
			scoreOnOff(pre, "pre-fader (the fader does not change what the bus hears)", "post-fader"), scoreOnOff(pre, "post-fader", "pre-fader"))
	}
	knob(0, "##scka", "Rev", l.SendA, 0, 1, 0, func(v float32) { e.setSend(li, 0, v) }, sendTip(0, l.SendA, l.PreA))
	if imgui.IsItemHovered() && imgui.IsMouseClicked(1) {
		e.togglePre(li, 0)
	}
	knob(1, "##sckb", "Dly", l.SendB, 0, 1, 0, func(v float32) { e.setSend(li, 1, v) }, sendTip(1, l.SendB, l.PreB))
	if imgui.IsItemHovered() && imgui.IsMouseClicked(1) {
		e.togglePre(li, 1)
	}
	knob(2, "##sckp", "Pan", l.Pan, -1, 1, 0, func(v float32) { e.setPan(li, v) }, "Pan "+scorePanText(l.Pan)+"; double-click: centre")
	for k, pre := range []bool{l.PreA, l.PreB} {
		if pre {
			tw := imgui.CalcTextSize("pre", false, 0).X
			dl.AddText(imgui.Vec2{X: at.X + 7 + kw*float32(k) + (kw-tw)/2, Y: ky + 41}, scoreColAccent, "pre")
		}
	}
	y = ky + 56
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
// effects, the return's pan, its fader and meter, and its mute.
func scoreBusStrip(bus int, at imgui.Vec2, h float32) {
	e := scEd
	b := e.bus(bus)
	imgui.PushID(fmt.Sprintf("scbus%d", bus))
	defer imgui.PopID()
	y := scoreStripFrame(at, h, scoreColBusStrip, scoreColStripEdge, scoreBusName(bus), "send bus "+string(rune('A'+bus)))
	target := scoreFXBusA
	if bus == 1 {
		target = scoreFXBusB
	}
	y = scoreFXSlots(target, b.FX, at.X, y, at.Y+h-200)
	ky := y + 4
	c := imgui.Vec2{X: at.X + scoreStripW/2, Y: ky + 13}
	if nv, changed, released := scoreKnob("##scbuspan", c, 11, b.Pan, -1, 1, 0, "Pan"); changed {
		e.setBusPan(bus, nv)
	} else if released {
		e.endMixGesture()
	}
	editorMenuTooltip(scoreBusName(bus) + " return pan " + scorePanText(b.Pan) + "; double-click: centre")
	y = ky + 52
	fh := at.Y + h - 50 - y
	if nv, changed, released := scoreVFader("##scbusfader", imgui.Vec2{X: at.X + 30, Y: y}, imgui.Vec2{X: 26, Y: fh}, b.Return, 0, 1, 1); changed {
		e.setReturn(bus, nv)
	} else if released {
		e.endMixGesture()
	}
	editorMenuTooltip(fmt.Sprintf("%s return %s: how much of the bus comes back into the mix", scoreBusName(bus), scoreDB(b.Return)))
	dl := imgui.WindowDrawList()
	ma := imgui.Vec2{X: at.X + 66, Y: y}
	mb := imgui.Vec2{X: at.X + 73, Y: y + fh}
	dl.AddRectFilledV(ma, mb, scoreColTrack, 2, imgui.DrawFlagsRoundCornersAll)
	scoreDrawMeterBar(dl, ma, mb, scoreBusMeter(bus))
	db := scoreDB(b.Return)
	tw := imgui.CalcTextSize(db, false, 0).X
	dl.AddText(imgui.Vec2{X: at.X + (scoreStripW-tw)/2, Y: at.Y + h - 44}, scoreColText, db)
	if scoreSmallButton("M##scbm", imgui.Vec2{X: at.X + scoreStripW/2 - 10, Y: at.Y + h - 24}, b.Mute, scoreColMute) {
		e.toggleBusMute(bus)
	}
	editorMenuTooltip("Mute the bus's return")
}

// scoreMasterStrip is the master chain, the master fader and the mix's
// meter.
func scoreMasterStrip(at imgui.Vec2, h float32) {
	e := scEd
	imgui.PushID("scmaster")
	defer imgui.PopID()
	y := scoreStripFrame(at, h, scoreColMasterStrip, scoreColMasterEdge, "Master", "whole file")
	y = scoreFXSlots(scoreFXMaster, e.Score.Master, at.X, y, at.Y+h-150)
	y += 8
	dl := imgui.WindowDrawList()
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

// ---------- effect slots ----------

// scoreFXSlots draws target's chain from y — a slot per effect, then a
// dashed "+ Insert" — stopping above maxY, and returns the y under them.
func scoreFXSlots(target int, chain []score.FX, x, y, maxY float32) float32 {
	e := scEd
	dl := imgui.WindowDrawList()
	w := scoreStripW - 14
	for k, f := range chain {
		if y+18 > maxY {
			dl.AddText(imgui.Vec2{X: x + 10, Y: y + 1}, scoreColDim, fmt.Sprintf("+%d more", len(chain)-k))
			imgui.SetCursorScreenPos(imgui.Vec2{X: x + 7, Y: y})
			if imgui.InvisibleButton(fmt.Sprintf("##fxmore%d", k), imgui.Vec2{X: w, Y: 18}) {
				scoreOpenFXEdit(target, k, imgui.Vec2{X: x, Y: y + 20})
			}
			editorMenuTooltip("More effects than the strip shows: click to open the next one's settings")
			return y + 22
		}
		a, b := imgui.Vec2{X: x + 7, Y: y}, imgui.Vec2{X: x + 7 + w, Y: y + 18}
		imgui.SetCursorScreenPos(a)
		if imgui.InvisibleButton(fmt.Sprintf("##fxs%d", k), imgui.Vec2{X: w - 18, Y: 18}) {
			scoreOpenFXEdit(target, k, imgui.Vec2{X: x, Y: y + 20})
		}
		hov := imgui.IsItemHovered()
		if hov && imgui.IsMouseClicked(1) {
			scoreFXSlotOpen, scoreFXTarget, scoreFXSlot = true, target, k
		}
		editorMenuTooltip(fxLabel(f) + scoreOnOff(f.Bypass, " (bypassed)", "") + "\nClick: its settings. Right-click: move, bypass, remove.")
		edge := scoreColSlotEdge
		if hov {
			edge = scoreColAccent
		}
		dl.AddRectFilledV(a, b, scoreColSlot, 3, imgui.DrawFlagsRoundCornersAll)
		dl.AddRectV(a, b, edge, 3, imgui.DrawFlagsRoundCornersAll, 1)
		col, label := scoreColText, fxLabel(f)
		if fxSilent(f) {
			col = scoreColDim
		}
		if !f.Bypass && fxSilent(f) {
			label += " (off)"
		}
		dl.AddText(imgui.Vec2{X: a.X + 5, Y: a.Y + 2}, col, scoreClipText(label, w-26))
		// The bypass dot: lit while the effect is in the signal.
		imgui.SetCursorScreenPos(imgui.Vec2{X: b.X - 18, Y: a.Y})
		if imgui.InvisibleButton(fmt.Sprintf("##fxb%d", k), imgui.Vec2{X: 18, Y: 18}) {
			e.toggleFXBypass(target, k)
		}
		editorMenuTooltip(scoreOnOff(f.Bypass, "Bypassed: click to put it back in", "In the signal: click to bypass it"))
		dot := scoreColFXOn
		if fxSilent(f) {
			dot = scoreColFXOff
		}
		dl.AddCircleFilled(imgui.Vec2{X: b.X - 9, Y: a.Y + 9}, 3.5, dot)
		y += 22
	}
	if len(chain) < edit.MaxEffects && y+18 <= maxY {
		a, b := imgui.Vec2{X: x + 7, Y: y}, imgui.Vec2{X: x + 7 + w, Y: y + 18}
		imgui.SetCursorScreenPos(a)
		if imgui.InvisibleButton("##fxadd", imgui.Vec2{X: w, Y: 18}) {
			scoreFXAddOpen, scoreFXTarget, scoreFXPos = true, target, imgui.Vec2{X: x, Y: y + 20}
		}
		editorMenuTooltip("Add an effect to " + e.fxTargetName(target))
		edge := scoreColSlotEmpty
		if imgui.IsItemHovered() {
			edge = scoreColAccent
		}
		scoreDashedRect(dl, a, b, edge)
		dl.AddText(imgui.Vec2{X: a.X + 5, Y: a.Y + 2}, scoreColSlotEmpty, "+ Insert")
		y += 22
	}
	return y
}

// scoreDashedRect outlines a rectangle in dashes.
func scoreDashedRect(dl imgui.DrawList, a, b imgui.Vec2, col imgui.PackedColor) {
	const dash, gap = float32(4), float32(3)
	for x := a.X; x < b.X; x += dash + gap {
		x2 := min(x+dash, b.X)
		dl.AddLine(imgui.Vec2{X: x, Y: a.Y}, imgui.Vec2{X: x2, Y: a.Y}, col)
		dl.AddLine(imgui.Vec2{X: x, Y: b.Y}, imgui.Vec2{X: x2, Y: b.Y}, col)
	}
	for y := a.Y; y < b.Y; y += dash + gap {
		y2 := min(y+dash, b.Y)
		dl.AddLine(imgui.Vec2{X: a.X, Y: y}, imgui.Vec2{X: a.X, Y: y2}, col)
		dl.AddLine(imgui.Vec2{X: b.X, Y: y}, imgui.Vec2{X: b.X, Y: y2}, col)
	}
}

// scoreOpenFXEdit opens slot's settings popup at pos.
func scoreOpenFXEdit(target, slot int, pos imgui.Vec2) {
	scoreFXEditOpen, scoreFXTarget, scoreFXSlot, scoreFXPos = true, target, slot, pos
}

// scoreFXPopups draws the effect popups: the add menu, a slot's menu, and
// a slot's settings.
func scoreFXPopups() {
	e := scEd
	// Each popup is opened and begun together: a BeginPopup of one that is
	// not open clears the position set for the next window.
	if scoreFXAddOpen {
		scoreFXAddOpen = false
		imgui.SetNextWindowPos(scoreFXPos)
		imgui.OpenPopup("##scfxadd")
	}
	if imgui.BeginPopup("##scfxadd") {
		imgui.Text("Add to " + e.fxTargetName(scoreFXTarget))
		imgui.Separator()
		for i := range fx.Specs {
			sp := &fx.Specs[i]
			if imgui.MenuItem(sp.Label) {
				if e.addFX(scoreFXTarget, sp.Kind) {
					// Open the new effect's settings where the slot went.
					if c := e.fxChain(scoreFXTarget); c != nil {
						scoreOpenFXEdit(scoreFXTarget, len(*c)-1, scoreFXPos)
					}
				}
			}
			editorMenuTooltip(sp.Desc)
		}
		imgui.EndPopup()
	}
	if scoreFXSlotOpen {
		scoreFXSlotOpen = false
		imgui.OpenPopup("##scfxslot")
	}
	if imgui.BeginPopup("##scfxslot") {
		t, k := scoreFXTarget, scoreFXSlot
		if f := e.fxAt(t, k); f != nil {
			imgui.Text(fxLabel(*f) + " on " + e.fxTargetName(t))
			imgui.Separator()
			if imgui.MenuItem("Settings...") {
				scoreOpenFXEdit(t, k, imgui.MousePos())
			}
			if imgui.MenuItemV("Bypass", "", f.Bypass, true) {
				e.toggleFXBypass(t, k)
			}
			if imgui.MenuItemV("Move Up", "", false, k > 0) {
				e.moveFX(t, k, -1)
			}
			if imgui.MenuItemV("Move Down", "", false, k < len(*e.fxChain(t))-1) {
				e.moveFX(t, k, 1)
			}
			if imgui.MenuItem("Reset to Defaults") {
				e.resetFX(t, k)
			}
			if imgui.MenuItem("Remove") {
				e.removeFX(t, k)
			}
		}
		imgui.EndPopup()
	}
	if scoreFXEditOpen {
		scoreFXEditOpen = false
		imgui.SetNextWindowPos(scoreFXPos)
		imgui.OpenPopup("##scfxedit")
	}
	if imgui.BeginPopup("##scfxedit") {
		scoreFXEditor(scoreFXTarget, scoreFXSlot)
		imgui.EndPopup()
	}
}

// scoreFXEditor is a slot's settings: what it is, bypass, every
// parameter as a slider (a switch as a list), heard while it moves.
func scoreFXEditor(target, slot int) {
	e := scEd
	f := e.fxAt(target, slot)
	if f == nil {
		imgui.CloseCurrentPopup()
		return
	}
	k, ok := fx.Lookup(f.Kind)
	if !ok {
		imgui.Text(fmt.Sprintf("%s: this build has no %q effect; it plays nothing.", e.fxTargetName(target), f.Kind))
		if imgui.Button("Remove") {
			e.removeFX(target, slot)
			imgui.CloseCurrentPopup()
		}
		return
	}
	sp := k.Spec()
	imgui.Text(fmt.Sprintf("%s · %s (%d of %d)", e.fxTargetName(target), sp.Label, slot+1, len(*e.fxChain(target))))
	imgui.PushTextWrapPosV(imgui.CursorPosX() + 320)
	imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0.55, Y: 0.58, Z: 0.63, W: 1})
	imgui.Text(sp.Desc)
	imgui.PopStyleColor()
	imgui.PopTextWrapPos()
	bypass := f.Bypass
	if imgui.Checkbox("Bypass##scfxbyp", &bypass) {
		e.toggleFXBypass(target, slot)
	}
	imgui.SameLine()
	if imgui.Button("Reset##scfxreset") {
		e.resetFX(target, slot)
	}
	editorMenuTooltip("Every setting back to its default")
	imgui.SameLine()
	if imgui.Button("Remove##scfxrm") {
		e.removeFX(target, slot)
		imgui.CloseCurrentPopup()
		return
	}
	imgui.Separator()
	imgui.PushItemWidth(200)
	place := fxPlace(target)
	for pi := range sp.Params {
		ps := &sp.Params[pi]
		v := float32(f.Param(ps.Name, float64(sp.Default(pi, place))))
		id := ps.Label + "##scfxp" + ps.Name
		switch {
		case len(ps.Choices) > 0:
			idx := int32(math.Round(float64(v)))
			if imguibackend.Combo(id, &idx, ps.Choices) {
				e.setFXParam(target, slot, ps.Name, float64(idx))
				e.endMixGesture()
			}
		case ps.Int:
			iv := int32(math.Round(float64(v)))
			if imgui.SliderIntV(id, &iv, int32(ps.Min), int32(ps.Max), ps.Format(float32(iv)), imgui.SliderFlagsAlwaysClamp) {
				e.setFXParam(target, slot, ps.Name, float64(iv))
			}
		default:
			flags := imgui.SliderFlagsAlwaysClamp
			if ps.Log {
				flags |= imgui.SliderFlagsLogarithmic
			}
			nv := v
			if imgui.SliderFloatV(id, &nv, ps.Min, ps.Max, strings.ReplaceAll(ps.Format(v), "%", "%%"), flags) {
				e.setFXParam(target, slot, ps.Name, float64(nv))
			}
		}
		if imgui.IsItemDeactivated() {
			e.endMixGesture()
		}
		if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
			e.setFXParam(target, slot, ps.Name, float64(sp.Default(pi, place)))
			e.endMixGesture()
		}
	}
	imgui.PopItemWidth()
	if ls, ch := e.Score.LanesOf(target), e.Score.ChainOf(target); ls != nil && ch != nil {
		var lanes []string
		for _, ln := range *ls {
			if t, err := score.ParseLaneTargetIn(score.OwnerScope(target), ln.Name); err == nil && t.Kind == score.LaneFX && score.ChainSlot(*ch, t) == slot {
				lanes = append(lanes, sp.Params[t.Param].Label)
			}
		}
		if len(lanes) > 0 {
			imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0.55, Y: 0.58, Z: 0.63, W: 1})
			imgui.Text("Automated: " + strings.Join(lanes, ", ") + " (the lane wins while it plays)")
			imgui.PopStyleColor()
		}
	}
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
