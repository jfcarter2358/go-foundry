package editor

// editor_score_fx.go — the Score editor's effects on the editing model, with
// no ImGui in it: the insert chains of the lines, the two send buses' chains
// and the master chain (add, remove, reorder, bypass, a parameter, reset),
// pre-fader sends, the buses' pan and mute; and how the window tells the
// player about them.
//
// Effects are mixer settings, so they go through mixChange (MixRev, one
// undo step, a knob's drag one step) and reach the player live: a
// parameter or a bypass as SetFX / SetBypass, a chain whose effects
// changed as SetEffects (the program copied with the new mixer; chains
// that kept their effects keep their tails). The one exception is a chain
// whose owner's automation lanes move one of its effects (a line's, a
// bus's, the master's): adding, removing or moving an effect there renames
// or drops those lanes (edit.AddOwnedEffect …), which changes the
// performance, so it goes through change.

import (
	"fmt"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
	"foundry/internal/tracker/fx"
)

// Chain targets beyond the lines (a line's target is its index): the
// lane owners of the same names, so a chain and its lanes are one number.
const (
	scoreFXBusA   = score.OwnerBusA
	scoreFXBusB   = score.OwnerBusB
	scoreFXMaster = score.OwnerMaster
)

// fxChain is the chain a target names, nil for none.
func (e *scoreState) fxChain(target int) *[]score.FX {
	switch target {
	case scoreFXBusA:
		return &e.Score.Buses.A.FX
	case scoreFXBusB:
		return &e.Score.Buses.B.FX
	case scoreFXMaster:
		return &e.Score.Master
	}
	if l := e.line(target); l != nil {
		return &l.Inserts
	}
	return nil
}

// fxTargetName is a chain's owner as the status bar says it.
func (e *scoreState) fxTargetName(target int) string {
	switch target {
	case scoreFXBusA:
		return scoreBusName(0) + " bus"
	case scoreFXBusB:
		return scoreBusName(1) + " bus"
	case scoreFXMaster:
		return "Master"
	}
	return e.lineName(target)
}

// fxPlace is where a target's chain sits (what "mix" defaults to).
func fxPlace(target int) fx.Placement {
	if target == scoreFXBusA || target == scoreFXBusB {
		return fx.Bus
	}
	return fx.Insert
}

// fxAt is effect slot of target's chain, nil when there is none.
func (e *scoreState) fxAt(target, slot int) *score.FX {
	c := e.fxChain(target)
	if c == nil || slot < 0 || slot >= len(*c) {
		return nil
	}
	return &(*c)[slot]
}

// fxLabel is an effect as a slot shows it.
func fxLabel(f score.FX) string {
	if k, ok := fx.Lookup(f.Kind); ok {
		return k.Spec().Label
	}
	return f.Kind + " (unknown)"
}

// fxSilent reports whether an effect passes the signal untouched: bypassed,
// or a compressor switched off (the score's default master compressor).
func fxSilent(f score.FX) bool {
	return f.Bypass || f.Kind == "compressor" && f.Param("enabled", 1) == 0
}

// fxStructural runs a change to a chain's effects: through change when
// target's lanes follow its effects (they are renamed), else as a mixer
// step.
func (e *scoreState) fxStructural(target int, f func() error) bool {
	if edit.OwnerHasEffectLanes(e.Score, target) {
		return e.change(f)
	}
	var err error
	e.mixChange("", func() { err = f() })
	if err != nil {
		// Nothing changed; the step is empty but harmless. Say why.
		e.Status = scoreErrText(err)
		return false
	}
	return true
}

// addFX adds an effect of kind k at the end of target's chain.
func (e *scoreState) addFX(target int, k fx.Kind) bool {
	c := e.fxChain(target)
	if c == nil {
		return false
	}
	if len(*c) >= edit.MaxEffects {
		e.Status = fmt.Sprintf("%s has %d effects, the most a chain holds", e.fxTargetName(target), edit.MaxEffects)
		return false
	}
	f := score.NewFX(k, fxPlace(target))
	if !e.fxStructural(target, func() error {
		_, err := edit.AddOwnedEffect(e.Score, target, -1, f)
		return err
	}) {
		return false
	}
	e.Status = fmt.Sprintf("Added %s to %s", k.Spec().Label, e.fxTargetName(target))
	return true
}

// removeFX takes slot out of target's chain (with any lanes of it).
func (e *scoreState) removeFX(target, slot int) bool {
	f := e.fxAt(target, slot)
	if f == nil {
		return false
	}
	label := fxLabel(*f)
	gone := 0
	if !e.fxStructural(target, func() error {
		n, err := edit.RemoveOwnedEffect(e.Score, target, slot)
		gone = n
		return err
	}) {
		return false
	}
	e.Status = fmt.Sprintf("Removed %s from %s", label, e.fxTargetName(target))
	if gone > 0 {
		e.Status += fmt.Sprintf(" and its %d lane(s)", gone)
	}
	return true
}

// moveFX moves slot up (dir < 0) or down one place in its chain.
func (e *scoreState) moveFX(target, slot, dir int) bool {
	c := e.fxChain(target)
	to := slot + dir
	if c == nil || slot < 0 || slot >= len(*c) || to < 0 || to >= len(*c) {
		return false
	}
	label := fxLabel((*c)[slot])
	if !e.fxStructural(target, func() error { return edit.MoveOwnedEffect(e.Score, target, slot, to) }) {
		return false
	}
	e.Status = fmt.Sprintf("Moved %s %s in %s", label, scoreOnOff(dir < 0, "up", "down"), e.fxTargetName(target))
	return true
}

// toggleFXBypass takes an effect out of its chain's signal or back in.
func (e *scoreState) toggleFXBypass(target, slot int) {
	f := e.fxAt(target, slot)
	if f == nil {
		return
	}
	e.mixChange("", func() { f.Bypass = !f.Bypass })
	e.Status = fmt.Sprintf("%s on %s %s", fxLabel(*f), e.fxTargetName(target), scoreOnOff(f.Bypass, "bypassed", "back in"))
}

// setFXParam sets one parameter (by name), clamped to its range: one undo
// step per drag of one control.
func (e *scoreState) setFXParam(target, slot int, name string, v float64) {
	f := e.fxAt(target, slot)
	if f == nil {
		return
	}
	k, ok := fx.Lookup(f.Kind)
	if !ok {
		return
	}
	pi := k.Spec().Param(name)
	if pi < 0 {
		return
	}
	ps := &k.Spec().Params[pi]
	v = score.Float32Value(ps.Clamp(float32(v)))
	if f.Param(name, float64(k.Spec().Default(pi, fxPlace(target)))) == v {
		return
	}
	e.mixChange(fmt.Sprintf("fx%d.%d.%s", target, slot, name), func() { f.SetParam(name, v) })
	e.Status = fmt.Sprintf("%s, %s %s: %s", e.fxTargetName(target), fxLabel(*f), ps.Label, ps.Format(float32(v)))
}

// resetFX puts every parameter of an effect back to its default.
func (e *scoreState) resetFX(target, slot int) {
	f := e.fxAt(target, slot)
	if f == nil {
		return
	}
	k, ok := fx.Lookup(f.Kind)
	if !ok {
		return
	}
	def := score.NewFX(k, fxPlace(target))
	e.mixChange("", func() { f.Params = def.Params })
	e.Status = fmt.Sprintf("%s on %s back to its defaults", fxLabel(*f), e.fxTargetName(target))
}

// togglePre makes line i's send to bus pre-fader or post-fader.
func (e *scoreState) togglePre(i, bus int) {
	l := e.line(i)
	if l == nil {
		return
	}
	p := &l.PreA
	if bus == 1 {
		p = &l.PreB
	}
	e.mixChange("", func() { *p = !*p })
	e.Status = fmt.Sprintf("%s: %s send %s", e.lineName(i), scoreBusName(bus), scoreOnOff(*p, "pre-fader (the fader does not change it)", "post-fader"))
}

// bus is send bus 0 (A) or 1 (B).
func (e *scoreState) bus(b int) *score.Bus {
	if b == 1 {
		return &e.Score.Buses.B
	}
	return &e.Score.Buses.A
}

// setBusPan is a bus return's pan.
func (e *scoreState) setBusPan(b int, v float32) {
	v = clampF32(v, -1, 1)
	bs := e.bus(b)
	if v == bs.Pan {
		return
	}
	e.mixChange(fmt.Sprintf("buspan%d", b), func() { bs.Pan = v })
	e.Status = fmt.Sprintf("%s return: pan %s", scoreBusName(b), scorePanText(v))
}

// toggleBusMute is a bus return's M.
func (e *scoreState) toggleBusMute(b int) {
	bs := e.bus(b)
	e.mixChange("", func() { bs.Mute = !bs.Mute })
	e.Status = scoreBusName(b) + " return" + scoreOnOff(bs.Mute, " muted", " unmuted")
}

// ---------- telling the player ----------

// What the player was last told of the effects and the buses.
var (
	scoreSentFX    [][]score.FX
	scoreSentBuses [2]score.Bus
)

// scoreChains are every chain of a score in the order scorePlayerTarget
// numbers them: the lines', then bus A, bus B, the master.
func scoreChains(s *score.Score) [][]score.FX {
	out := make([][]score.FX, 0, len(s.Lines)+3)
	for _, l := range s.Lines {
		out = append(out, l.Inserts)
	}
	return append(out, s.Buses.A.FX, s.Buses.B.FX, s.Master)
}

// scorePlayerTarget is chain i of scoreChains as the player names it.
func scorePlayerTarget(s *score.Score, i int) int {
	switch i - len(s.Lines) {
	case 0:
		return tracker.FXBusA
	case 1:
		return tracker.FXBusB
	case 2:
		return tracker.FXMaster
	}
	return i
}

func scoreCopyChains(s *score.Score) [][]score.FX {
	var out [][]score.FX
	for _, c := range scoreChains(s) {
		cp := make([]score.FX, len(c))
		for i, f := range c {
			f.Params = append([]score.FXParam(nil), f.Params...)
			cp[i] = f
		}
		out = append(out, cp)
	}
	return out
}

// scoreRecordFX notes the effects and buses as the player now has them.
func scoreRecordFX(s *score.Score) {
	scoreSentFX = scoreCopyChains(s)
	scoreSentBuses = [2]score.Bus{{Return: s.Buses.A.Return, Pan: s.Buses.A.Pan, Mute: s.Buses.A.Mute},
		{Return: s.Buses.B.Return, Pan: s.Buses.B.Pan, Mute: s.Buses.B.Mute}}
}

// scoreSameKinds reports whether two chains have the same effects in order.
func scoreSameKinds(a, b []score.FX) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind {
			return false
		}
	}
	return true
}

// scorePlanSlot is where the file's effect i sits among the effects the
// player plays (it leaves out kinds this build does not have).
func scorePlanSlot(chain []score.FX, i int) int {
	n := 0
	for k := 0; k < i; k++ {
		if _, ok := fx.Lookup(chain[k].Kind); ok {
			n++
		}
	}
	return n
}

// fxPlayer is what scoreSyncFX talks to (the player; tests record).
type fxPlayer interface {
	SetEffects(*score.Score) ([]error, error)
	SetFX(target, slot, param int, v float32) bool
	SetBypass(target, slot int, on bool) bool
	SetBus(bus int, ret, pan float32, mute bool) bool
}

var _ fxPlayer = (*play.Player)(nil)

// scoreSyncFX sends the player what changed in the effects and the buses
// since it was last told: a chain whose effects changed as one SetEffects,
// otherwise each moved parameter and bypass.
func scoreSyncFX(p fxPlayer, s *score.Score) {
	chains := scoreChains(s)
	structural := len(chains) != len(scoreSentFX)
	for i := 0; !structural && i < len(chains); i++ {
		structural = !scoreSameKinds(chains[i], scoreSentFX[i])
	}
	if structural {
		if _, err := p.SetEffects(s.Clone()); err != nil {
			scEd.Status = "Playback: " + err.Error()
		}
	} else {
		for i, c := range chains {
			target := scorePlayerTarget(s, i)
			for k, f := range c {
				kind, ok := fx.Lookup(f.Kind)
				if !ok {
					continue
				}
				sent := scoreSentFX[i][k]
				slot := scorePlanSlot(c, k)
				if f.Bypass != sent.Bypass {
					p.SetBypass(target, slot, f.Bypass)
				}
				sp := kind.Spec()
				for pi := range sp.Params {
					def := float64(sp.Default(pi, fxPlace(scoreEditorTarget(s, i))))
					name := sp.Params[pi].Name
					if v := f.Param(name, def); v != sent.Param(name, def) {
						p.SetFX(target, slot, pi, float32(v))
					}
				}
			}
		}
	}
	for b, bs := range []score.Bus{s.Buses.A, s.Buses.B} {
		if sent := scoreSentBuses[b]; bs.Return != sent.Return || bs.Pan != sent.Pan || bs.Mute != sent.Mute {
			p.SetBus(b, bs.Return, bs.Pan, bs.Mute)
		}
	}
	scoreRecordFX(s)
}

// scoreEditorTarget is chain i of scoreChains as the editor's targets
// name it.
func scoreEditorTarget(s *score.Score, i int) int {
	switch i - len(s.Lines) {
	case 0:
		return scoreFXBusA
	case 1:
		return scoreFXBusB
	case 2:
		return scoreFXMaster
	}
	return i
}
