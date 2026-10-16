package editor

// editor_score_feel.go — swing and humanise: the score's feel and a line's
// own (Play > Swing and Humanise…).
//
// Both change when the notes play, so they are changes to the music (Rev:
// the player performs the score again), but a slider dragged is one undo
// step, like a fader's.

import (
	"fmt"

	"foundry/internal/imguibackend"
	"foundry/internal/score"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// feelChange runs a swing or humanise edit: a change to the music, one
// undo step per drag of one control (key).
func (e *scoreState) feelChange(key string, f func()) {
	if key == "" || key != e.mixKey {
		e.undo.push(scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel})
		e.redo.clear()
	}
	f()
	e.touch()
	e.mixKey = key
}

// feelSwing is the swing line li (-1: the score) has of its own, nil for
// none (a line then plays the score's).
func (e *scoreState) feelSwing(li int) *score.Swing {
	if li < 0 {
		return &e.Score.Playback.Swing
	}
	if l := e.line(li); l != nil {
		return l.Swing
	}
	return nil
}

func (e *scoreState) feelHumanize(li int) *score.Humanize {
	if li < 0 {
		return &e.Score.Playback.Humanize
	}
	if l := e.line(li); l != nil {
		return l.Humanize
	}
	return nil
}

func (e *scoreState) feelName(li int) string {
	if li < 0 {
		return "The score"
	}
	return e.lineName(li)
}

// setSwing sets the swing of the score (li -1) or a line: amount 0.5
// (straight) to 0.75, unit 8 or 16.
func (e *scoreState) setSwing(li int, w score.Swing) {
	w.Amount = clampF32(w.Amount, 0.5, 0.75)
	if w.Unit != 16 {
		w.Unit = 8
	}
	if li >= 0 && e.line(li) == nil {
		return
	}
	if cur := e.feelSwing(li); cur != nil && *cur == w {
		return
	}
	e.feelChange(fmt.Sprintf("swing%d", li), func() {
		if li < 0 {
			e.Score.Playback.Swing = w
		} else {
			e.line(li).Swing = &w
		}
	})
	e.Status = fmt.Sprintf("%s swings %.0f%% on %dths", e.feelName(li), w.Amount*100, w.Unit)
	if !w.On() {
		e.Status = e.feelName(li) + " plays straight"
	}
}

// setHumanize sets the humanise of the score (li -1) or a line.
func (e *scoreState) setHumanize(li int, h score.Humanize) {
	h.Timing = clampF32(h.Timing, 0, 50)
	h.Velocity = clampF32(h.Velocity, 0, 0.5)
	if li >= 0 && e.line(li) == nil {
		return
	}
	if cur := e.feelHumanize(li); cur != nil && *cur == h {
		return
	}
	e.feelChange(fmt.Sprintf("humanize%d", li), func() {
		if li < 0 {
			e.Score.Playback.Humanize = h
		} else {
			e.line(li).Humanize = &h
		}
	})
	e.Status = fmt.Sprintf("%s: humanise ±%.0f ms, ±%.0f%% velocity", e.feelName(li), h.Timing, h.Velocity*100)
}

// clearOwnFeel takes a line's own swing or humanise away: it plays the
// score's.
func (e *scoreState) clearOwnFeel(li int, swing bool) {
	l := e.line(li)
	if l == nil || swing && l.Swing == nil || !swing && l.Humanize == nil {
		return
	}
	e.feelChange("", func() {
		if swing {
			l.Swing = nil
		} else {
			l.Humanize = nil
		}
	})
	e.Status = fmt.Sprintf("%s plays the score's %s", e.lineName(li), scoreOnOff(swing, "swing", "humanise"))
}

// ---------- the popup ----------

var (
	scoreFeelOpen bool
	// scoreFeelLine is the popup's target: -1 the score, else a line.
	scoreFeelLine = -1
)

// scoreOpenFeel opens Swing and Humanise for the score, or the selected
// line when bars of one line are selected.
func scoreOpenFeel() {
	scoreFeelOpen, scoreFeelLine = true, -1
	if q := scEd.Sel; q.Kind == scoreSelBars && q.LineA == q.LineB {
		scoreFeelLine = q.LineA
	}
}

// scoreFeelPopup draws Swing and Humanise.
func scoreFeelPopup() {
	e := scEd
	if scoreFeelOpen {
		scoreFeelOpen = false
		imgui.SetNextWindowPos(imgui.Vec2{X: 300, Y: editorTopBarH + scoreTransportH + 20})
		imgui.OpenPopup("##scfeel")
	}
	if !imgui.BeginPopup("##scfeel") {
		return
	}
	defer imgui.EndPopup()
	if scoreFeelLine >= len(e.Score.Lines) {
		scoreFeelLine = -1
	}
	imgui.Text("Swing and humanise")
	names := []string{"The whole score"}
	for i := range e.Score.Lines {
		names = append(names, e.lineName(i))
	}
	idx := int32(scoreFeelLine + 1)
	imgui.PushItemWidth(220)
	defer imgui.PopItemWidth()
	if imguibackend.Combo("For##scfeelfor", &idx, names) {
		scoreFeelLine = int(idx) - 1
	}
	li := scoreFeelLine
	imgui.Separator()

	// Swing.
	sw := e.feelSwing(li)
	if li >= 0 {
		own := sw != nil
		if imgui.Checkbox("Own swing##scfeelsw", &own) {
			if own {
				e.setSwing(li, e.Score.Playback.Swing.OrStraight())
			} else {
				e.clearOwnFeel(li, true)
			}
			e.endMixGesture()
		}
		editorMenuTooltip("Off: the line swings as the score does")
		sw = e.feelSwing(li)
	}
	if sw != nil {
		w := sw.OrStraight()
		amt := w.Amount * 100
		if imgui.SliderFloatV("Swing##scfeelamt", &amt, 50, 75, "%.0f%%", imgui.SliderFlagsAlwaysClamp) {
			w.Amount = amt / 100
			e.setSwing(li, w)
		}
		if imgui.IsItemDeactivated() {
			e.endMixGesture()
		}
		editorMenuTooltip("How much of each pair the first note takes: 50% straight, 67% triplets, 75% dotted")
		unit := int32(0)
		if w.Unit == 16 {
			unit = 1
		}
		if imguibackend.Combo("On##scfeelunit", &unit, []string{"eighths", "sixteenths"}) {
			w.Unit = 8
			if unit == 1 {
				w.Unit = 16
			}
			e.setSwing(li, w)
			e.endMixGesture()
		}
	}
	imgui.Separator()

	// Humanise.
	hz := e.feelHumanize(li)
	if li >= 0 {
		own := hz != nil
		if imgui.Checkbox("Own humanise##scfeelhu", &own) {
			if own {
				e.setHumanize(li, e.Score.Playback.Humanize)
			} else {
				e.clearOwnFeel(li, false)
			}
			e.endMixGesture()
		}
		editorMenuTooltip("Off: the line is humanised as the score is")
		hz = e.feelHumanize(li)
	}
	if hz != nil {
		h := *hz
		if imgui.SliderFloatV("Timing##scfeeltime", &h.Timing, 0, 50, "±%.0f ms", imgui.SliderFlagsAlwaysClamp) {
			e.setHumanize(li, h)
		}
		if imgui.IsItemDeactivated() {
			e.endMixGesture()
		}
		editorMenuTooltip("The most a note moves early or late")
		vel := h.Velocity * 100
		if imgui.SliderFloatV("Velocity##scfeelvel", &vel, 0, 50, "±%.0f%%", imgui.SliderFlagsAlwaysClamp) {
			h.Velocity = vel / 100
			e.setHumanize(li, h)
		}
		if imgui.IsItemDeactivated() {
			e.endMixGesture()
		}
		editorMenuTooltip("The most a note's velocity moves, as a share of itself")
		seed := int32(h.Seed)
		if imgui.InputInt("Seed##scfeelseed", &seed) {
			h.Seed = int64(seed)
			e.setHumanize(li, h)
			e.endMixGesture()
		}
		editorMenuTooltip("The same seed plays the same nudges every time; another seed, other nudges")
	}
}
