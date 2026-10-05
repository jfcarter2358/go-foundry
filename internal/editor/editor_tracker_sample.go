package editor

// editor_tracker_sample.go — the Sample tab: the current instrument's sample
// as a waveform, and the edits a tracker makes to one.
//
// Samples are immutable once loaded (the mixer goroutine reads them with no
// lock), so the editor never touches the library's copy. It works on a deep
// copy (trkSampleWork); Apply hands that copy to the library cache with
// tracker.PutSample, which is how every song — this one and any a level is
// playing — hears the edit, and Save also writes it to disk.
//
// The DSP below the drawing (normalise, fades, reverse, trim, zero-crossing
// snap, pitch detection) is plain functions over a *tracker.Sample so the test
// can check them without a window.

import (
	"fmt"
	"math"

	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// The sample editor's working state.
var (
	trkSampleWork  *tracker.Sample      // the copy being edited; nil reloads it
	trkSampleSlot  int                  // which of the instrument's samples
	trkSampleDirty bool                 // differs from what the library holds
	trkWaveStart   float64              // first frame on screen
	trkWaveZoom    float64         = 0  // frames per pixel; 0 fits the whole sample
	trkWaveSelA                    = -1 // selection in frames; -1 none
	trkWaveSelB                    = -1
	trkWaveDrag    int             // 0 none, 1 selecting, 2 loop start, 3 loop end
	trkBaseNoteTxt string
)

// ---------- DSP ----------

// trkCopySample deep-copies a sample's audio and metadata.
func trkCopySample(s *tracker.Sample) *tracker.Sample {
	c := *s
	c.Data = append([]float32(nil), s.Data...)
	c.Tags = append([]string(nil), s.Tags...)
	return &c
}

// trkRange normalises a frame range against a sample; an empty range is the
// whole sample.
func trkRange(s *tracker.Sample, a, b int) (int, int) {
	n := s.Frames()
	if a > b {
		a, b = b, a
	}
	if a < 0 || b <= a {
		return 0, n
	}
	return clampI(a, 0, n), clampI(b, 0, n)
}

// trkNormalize scales a range so its peak is at full scale.
func trkNormalize(s *tracker.Sample, a, b int) {
	a, b = trkRange(s, a, b)
	ch := s.Channels
	var peak float32
	for i := a * ch; i < b*ch; i++ {
		peak = max32(peak, absF32(s.Data[i]))
	}
	if peak <= 0 {
		return
	}
	g := 1 / peak
	for i := a * ch; i < b*ch; i++ {
		s.Data[i] *= g
	}
}

// trkFade ramps a range in from silence (in) or out to it.
func trkFade(s *tracker.Sample, a, b int, in bool) {
	a, b = trkRange(s, a, b)
	n := b - a
	if n <= 1 {
		return
	}
	for f := a; f < b; f++ {
		g := float32(f-a) / float32(n-1)
		if !in {
			g = 1 - g
		}
		for c := 0; c < s.Channels; c++ {
			s.Data[f*s.Channels+c] *= g
		}
	}
}

// trkReverse reverses a range in place.
func trkReverse(s *tracker.Sample, a, b int) {
	a, b = trkRange(s, a, b)
	ch := s.Channels
	for i, j := a, b-1; i < j; i, j = i+1, j-1 {
		for c := 0; c < ch; c++ {
			s.Data[i*ch+c], s.Data[j*ch+c] = s.Data[j*ch+c], s.Data[i*ch+c]
		}
	}
}

// trkTrim keeps only a range, moving the loop points with it.
func trkTrim(s *tracker.Sample, a, b int) {
	a, b = trkRange(s, a, b)
	s.Data = append([]float32(nil), s.Data[a*s.Channels:b*s.Channels]...)
	n := b - a
	s.LoopStart = clampI(s.LoopStart-a, 0, n)
	s.LoopEnd = clampI(s.LoopEnd-a, 0, n)
	if s.LoopEnd <= s.LoopStart+1 {
		s.Loop, s.LoopStart, s.LoopEnd = tracker.LoopNone, 0, 0
	}
}

// trkMono is frame f's mono value.
func trkMono(s *tracker.Sample, f int) float32 {
	var v float32
	for c := 0; c < s.Channels; c++ {
		v += s.Data[f*s.Channels+c]
	}
	return v / float32(s.Channels)
}

// trkZeroCrossing is the frame nearest f (within radius) where the mono
// signal crosses zero going up — the place a loop point does not click.
// With none in reach, f itself.
func trkZeroCrossing(s *tracker.Sample, f, radius int) int {
	n := s.Frames()
	if n < 2 {
		return f
	}
	f = clampI(f, 1, n-1)
	for d := 0; d <= radius; d++ {
		for _, g := range []int{f - d, f + d} {
			if g >= 1 && g < n && trkMono(s, g-1) <= 0 && trkMono(s, g) > 0 {
				return g
			}
		}
	}
	return f
}

// trkDetectPitch estimates a sample's fundamental by autocorrelation over a
// window from a little way in (past the attack), searching 40 Hz..2 kHz.
// Returns 0 when nothing periodic is found.
func trkDetectPitch(s *tracker.Sample) float64 {
	n := s.Frames()
	if n < 512 || s.Rate <= 0 {
		return 0
	}
	start := n / 10
	win := min(4096, n-start)
	minLag := s.Rate / 2000
	maxLag := min(s.Rate/40, win/2)
	if maxLag <= minLag+2 {
		return 0
	}
	x := make([]float64, win)
	for i := range x {
		x[i] = float64(trkMono(s, start+i))
	}
	corr := func(lag int) float64 {
		var sum, ea, eb float64
		for i := 0; i+lag < win; i++ {
			sum += x[i] * x[i+lag]
			ea += x[i] * x[i]
			eb += x[i+lag] * x[i+lag]
		}
		if ea == 0 || eb == 0 {
			return 0
		}
		return sum / math.Sqrt(ea*eb)
	}
	cs := make([]float64, maxLag+2)
	best, bestLag := 0.0, 0
	for lag := minLag; lag <= maxLag+1; lag++ {
		cs[lag] = corr(lag)
	}
	// The first strong peak, not the global one: the global one is often an
	// octave below (two periods correlate as well as one).
	for lag := minLag + 1; lag <= maxLag; lag++ {
		if cs[lag] > cs[lag-1] && cs[lag] >= cs[lag+1] && cs[lag] > 0.6 {
			best, bestLag = cs[lag], lag
			break
		}
		if cs[lag] > best {
			best, bestLag = cs[lag], lag
		}
	}
	if bestLag == 0 || best < 0.3 {
		return 0
	}
	// Parabolic interpolation for a sub-sample lag.
	l := float64(bestLag)
	if bestLag > minLag && bestLag <= maxLag {
		a, b, c := cs[bestLag-1], cs[bestLag], cs[bestLag+1]
		if d := a - 2*b + c; d != 0 {
			l += 0.5 * (a - c) / d
		}
	}
	return float64(s.Rate) / l
}

// trkHzToNote is the nearest tracker note to a frequency and the cents off
// it. C-4 is middle C (MIDI 60), so a tracker note is MIDI - 11.
func trkHzToNote(hz float64) (uint8, float32) {
	if hz <= 0 {
		return 0, 0
	}
	midi := 69 + 12*math.Log2(hz/440)
	n := math.Round(midi)
	note := int(n) - 11
	if note < int(tracker.NoteMin) || note > int(tracker.NoteMax) {
		return 0, 0
	}
	return uint8(note), float32((midi - n) * 100)
}

// trkLoopSeamSample is the loop played four times over, capped at a few
// seconds — the loop seam, auditioned on its own.
func trkLoopSeamSample(s *tracker.Sample) *tracker.Sample {
	if !s.Looped() {
		return nil
	}
	c := *s
	seg := s.Data[s.LoopStart*s.Channels : s.LoopEnd*s.Channels]
	reps := 4
	for reps > 1 && len(seg)*reps/s.Channels > s.Rate*4 {
		reps--
	}
	c.Data = nil
	for i := 0; i < reps; i++ {
		c.Data = append(c.Data, seg...)
	}
	c.Loop, c.LoopStart, c.LoopEnd = tracker.LoopNone, 0, 0
	c.Name = s.Name + "#seam"
	return &c
}

// ---------- the tab ----------

// trkSampleSource is the instrument's selected sample slot and its library
// sample, or nil.
func trkSampleSource() (*tracker.Instrument, *tracker.Sample) {
	in := trkInstrument()
	if in == nil || len(in.Samples) == 0 {
		return in, nil
	}
	trkSampleSlot = clampI(trkSampleSlot, 0, len(in.Samples)-1)
	return in, in.Loaded(trkSampleSlot)
}

// trkSampleEdit runs one edit on the working copy.
func trkSampleEdit(fn func(s *tracker.Sample)) {
	if trkSampleWork == nil {
		return
	}
	fn(trkSampleWork)
	trkSampleDirty = true
}

// trkSampleApply makes the working copy what the library holds, so songs
// hear it; save also writes it.
func trkSampleApply(save bool) {
	in, _ := trkSampleSource()
	if trkSampleWork == nil || in == nil {
		return
	}
	s := trkCopySample(trkSampleWork)
	tracker.PutSample(s)
	in.Resolve(tracker.SampleFor)
	trkSampleDirty = false
	if save {
		if err := tracker.SaveSample(s); err != nil {
			trkEd.Status = "Sample save failed: " + err.Error()
			return
		}
		trkSampleNames = nil
		trkEd.Status = "Saved sample " + s.Name
	} else {
		trkEd.Status = "Applied " + s.Name
	}
	trkSyncPlayer(true)
}

// trkPreviewPlay auditions a sample that is not (yet) what the library holds.
func trkPreviewPlay(s *tracker.Sample, secs float32) {
	if s == nil {
		return
	}
	trkEnsurePlayer()
	trkPreviewSample = s
	trkSyncPlayer(true)
	note := s.BaseNote
	if note == 0 {
		note = tracker.NoteC4
	}
	trkPlayer.NoteFor(trkPreviewInstr(), note, 1, secs)
}

func trkSampleTab() {
	in, src := trkSampleSource()
	if in == nil {
		editorTextMuted("No instrument.")
		return
	}
	if len(in.Samples) > 1 {
		imgui.SetNextItemWidth(220)
		if imguibackend.BeginCombo("##trksampslot", fmt.Sprintf("%d  %s", trkSampleSlot+1, in.Samples[trkSampleSlot])) {
			for i, n := range in.Samples {
				if imguibackend.SelectableV(fmt.Sprintf("%d  %s", i+1, n), i == trkSampleSlot, 0, imgui.Vec2{}) {
					trkSampleSlot, trkSampleWork = i, nil
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SameLine()
	}
	if src == nil {
		name := ""
		if len(in.Samples) > 0 {
			name = in.Samples[trkSampleSlot]
		}
		if name == "" {
			editorTextMuted("This instrument has no sample.")
		} else {
			editorTextMuted(name + " is not in the library.")
		}
		imgui.SameLine()
		trkGenerateButton()
		return
	}
	if trkSampleWork == nil || trkSampleWork.Name != src.Name {
		trkSampleWork = trkCopySample(src)
		trkSampleDirty = false
		trkWaveStart, trkWaveZoom = 0, 0
		trkWaveSelA, trkWaveSelB = -1, -1
		trkBaseNoteTxt = tracker.NoteName(trkSampleWork.BaseNote)
	}
	s := trkSampleWork
	imgui.Text(s.Name)
	imgui.SameLine()
	editorTextMuted(fmt.Sprintf("%d frames  %.2fs  %d Hz  %s", s.Frames(), float64(s.Frames())/float64(max(1, s.Rate)), s.Rate,
		map[int]string{1: "mono", 2: "stereo"}[s.Channels]))
	if trkSampleDirty {
		imgui.SameLine()
		editorTextColored(imgui.Vec4{X: 1, Y: 0.62, Z: 0.16, W: 1}, "edited")
	}

	// Toolbar.
	a, b := trkWaveSelA, trkWaveSelB
	if imgui.Button("Play##trksplay") {
		trkPreviewPlay(trkCopySample(s), float32(s.Frames())/float32(max(1, s.Rate))+0.2)
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(!s.Looped())
	if imgui.Button("Loop seam##trksseam") {
		trkPreviewPlay(trkLoopSeamSample(s), 4)
	}
	imguibackend.EditorPopDisabled()
	editorMenuTooltip("Hear the loop repeated, to check the seam")
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	if imgui.Button("Normalize##trksnorm") {
		trkSampleEdit(func(s *tracker.Sample) { trkNormalize(s, a, b) })
	}
	imgui.SameLine()
	if imgui.Button("Fade in##trksfi") {
		trkSampleEdit(func(s *tracker.Sample) { trkFade(s, a, b, true) })
	}
	imgui.SameLine()
	if imgui.Button("Fade out##trksfo") {
		trkSampleEdit(func(s *tracker.Sample) { trkFade(s, a, b, false) })
	}
	imgui.SameLine()
	if imgui.Button("Reverse##trksrev") {
		trkSampleEdit(func(s *tracker.Sample) { trkReverse(s, a, b) })
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(a < 0 || a == b)
	if imgui.Button("Trim##trkstrim") {
		trkSampleEdit(func(s *tracker.Sample) { trkTrim(s, a, b) })
		trkWaveSelA, trkWaveSelB, trkWaveStart, trkWaveZoom = -1, -1, 0, 0
	}
	imguibackend.EditorPopDisabled()
	editorMenuTooltip("Keep only the selection")
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(!trkSampleDirty)
	if imgui.Button("Apply##trksapply") {
		trkSampleApply(false)
	}
	imgui.SameLine()
	if imgui.Button("Revert##trksrevert") {
		trkSampleWork = nil
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("Save##trkssave") {
		trkSampleApply(true)
	}
	editorMenuTooltip("Write the sample back to the library (WAV + .fsmp)")
	imgui.SameLine()
	trkGenerateButton()
	if trkSampleWork == nil {
		return
	}

	trkWaveform(s)

	// Loop and tuning.
	imgui.PushItemWidth(110)
	modes := []string{"No loop", "Forward", "Ping-pong"}
	lm := int32(s.Loop)
	if imguibackend.Combo("Loop##trksloop", &lm, modes) {
		trkSampleEdit(func(s *tracker.Sample) {
			s.Loop = tracker.LoopMode(lm)
			if s.Loop != tracker.LoopNone && s.LoopEnd <= s.LoopStart+1 {
				s.LoopStart, s.LoopEnd = 0, s.Frames()
			}
		})
	}
	imgui.SameLine()
	if imgui.InputTextV("Base note##trksbase", &trkBaseNoteTxt, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		if n, err := tracker.ParseNote(trkBaseNoteTxt); err == nil && n >= tracker.NoteMin && n <= tracker.NoteMax {
			trkSampleEdit(func(s *tracker.Sample) { s.BaseNote = n })
		}
		trkBaseNoteTxt = tracker.NoteName(s.BaseNote)
	}
	editorMenuTooltip("The note at which the sample plays at its own pitch")
	imgui.SameLine()
	ft := s.FineTune
	if imgui.SliderFloatV("Fine##trksft", &ft, -100, 100, "%+.0f c", 0) {
		trkSampleEdit(func(s *tracker.Sample) { s.FineTune = ft })
	}
	imgui.SameLine()
	if imgui.Button("Detect pitch##trksdetect") {
		hz := trkDetectPitch(s)
		if n, cents := trkHzToNote(hz); n != 0 {
			trkSampleEdit(func(s *tracker.Sample) { s.BaseNote, s.FineTune = n, -cents })
			trkBaseNoteTxt = tracker.NoteName(n)
			trkEd.Status = fmt.Sprintf("Detected %.1f Hz: %s %+.0f cents", hz, tracker.NoteName(n), cents)
		} else {
			trkEd.Status = "No clear pitch found"
		}
	}
	vol := s.Volume
	imgui.SameLine()
	if imgui.SliderFloatV("Gain##trksvol", &vol, 0, 2, "%.2f", 0) {
		trkSampleEdit(func(s *tracker.Sample) { s.Volume = vol })
	}
	imgui.PopItemWidth()
}

// trkWaveform draws the working sample with its selection and loop points;
// wheel zooms about the mouse, drag selects, the loop markers drag (snapped
// to zero crossings; hold Alt for exact).
func trkWaveform(s *tracker.Sample) {
	width := imgui.ContentRegionAvail().X
	height := max32(60, imgui.ContentRegionAvail().Y-imgui.FrameHeightWithSpacing()-4)
	p := imgui.CursorScreenPos()
	imgui.InvisibleButtonV("##trkwave", imgui.Vec2{X: width, Y: height}, imgui.ButtonFlagsMouseButtonLeft|imgui.ButtonFlagsMouseButtonRight)
	hovered := imgui.IsItemHovered()
	dl := imgui.WindowDrawList()
	end := imgui.Vec2{X: p.X + width, Y: p.Y + height}
	dl.AddRectFilledV(p, end, trkColBg, 3, imgui.DrawFlagsRoundCornersAll)
	n := s.Frames()
	if n == 0 {
		return
	}
	fit := float64(n) / float64(width)
	if trkWaveZoom <= 0 || trkWaveZoom > fit {
		trkWaveZoom = fit
	}
	trkWaveStart = math.Max(0, math.Min(trkWaveStart, float64(n)-trkWaveZoom*float64(width)))
	frameX := func(f int) float32 { return p.X + float32((float64(f)-trkWaveStart)/trkWaveZoom) }
	xFrame := func(x float32) int {
		return clampI(int(trkWaveStart+float64(x-p.X)*trkWaveZoom), 0, n)
	}
	mouse := imgui.MousePos()
	if hovered {
		if _, wheel := imgui.CurrentIO().MouseWheel(); wheel != 0 {
			at := float64(xFrame(mouse.X))
			trkWaveZoom = math.Max(0.05, math.Min(fit, trkWaveZoom*math.Pow(0.8, float64(wheel))))
			trkWaveStart = at - float64(mouse.X-p.X)*trkWaveZoom
		}
		if imgui.IsMouseDragging(2, 1) {
			trkWaveStart -= float64(imgui.CurrentIO().MouseDelta().X) * trkWaveZoom
		}
	}
	// Selection.
	if trkWaveSelA >= 0 && trkWaveSelA != trkWaveSelB {
		a, b := trkWaveSelA, trkWaveSelB
		if a > b {
			a, b = b, a
		}
		dl.AddRectFilled(imgui.Vec2{X: max32(p.X, frameX(a)), Y: p.Y}, imgui.Vec2{X: min(end.X, frameX(b)), Y: end.Y}, trkColSel)
	}
	// Loop region.
	if s.Loop != tracker.LoopNone {
		la, lb := frameX(s.LoopStart), frameX(s.LoopEnd)
		dl.AddRectFilled(imgui.Vec2{X: max32(p.X, la), Y: p.Y}, imgui.Vec2{X: min(end.X, lb), Y: end.Y}, trkCol(0.45, 0.65, 1, 0.10))
	}
	// Wave: min/max per pixel column, one band per channel.
	dl.PushClipRect(p, end)
	bandH := height / float32(s.Channels)
	for c := 0; c < s.Channels; c++ {
		mid := p.Y + bandH*(float32(c)+0.5)
		dl.AddLine(imgui.Vec2{X: p.X, Y: mid}, imgui.Vec2{X: end.X, Y: mid}, trkCol(1, 1, 1, 0.08))
		for x := 0; x < int(width); x++ {
			f0 := int(trkWaveStart + float64(x)*trkWaveZoom)
			f1 := int(trkWaveStart + float64(x+1)*trkWaveZoom)
			if f0 >= n {
				break
			}
			if f1 <= f0 {
				f1 = f0 + 1
			}
			lo, hi := float32(1), float32(-1)
			for f := f0; f < f1 && f < n; f++ {
				v := s.Data[f*s.Channels+c]
				lo, hi = min(lo, v), max32(hi, v)
			}
			px := p.X + float32(x)
			dl.AddLine(imgui.Vec2{X: px, Y: mid - hi*bandH*0.45}, imgui.Vec2{X: px, Y: mid - lo*bandH*0.45 + 1}, trkColVol)
		}
	}
	// Loop markers.
	var la, lb float32
	if s.Loop != tracker.LoopNone {
		la, lb = frameX(s.LoopStart), frameX(s.LoopEnd)
		dl.AddLineV(imgui.Vec2{X: la, Y: p.Y}, imgui.Vec2{X: la, Y: end.Y}, trkColInstr, 2)
		dl.AddLineV(imgui.Vec2{X: lb, Y: p.Y}, imgui.Vec2{X: lb, Y: end.Y}, trkColNoteOff, 2)
		dl.AddText(imgui.Vec2{X: la + 3, Y: p.Y + 2}, trkColInstr, "S")
		dl.AddText(imgui.Vec2{X: lb - 10, Y: p.Y + 2}, trkColNoteOff, "E")
	}
	dl.PopClipRect()

	snap := func(f int) int {
		if imgui.CurrentIO().KeyAltPressed() {
			return f
		}
		return trkZeroCrossing(s, f, int(math.Max(32, trkWaveZoom*8)))
	}
	if hovered && imgui.IsMouseClicked(0) {
		switch {
		case s.Loop != tracker.LoopNone && absF32(mouse.X-la) < 5:
			trkWaveDrag = 2
		case s.Loop != tracker.LoopNone && absF32(mouse.X-lb) < 5:
			trkWaveDrag = 3
		default:
			trkWaveDrag = 1
			trkWaveSelA = xFrame(mouse.X)
			trkWaveSelB = trkWaveSelA
		}
	}
	if hovered && imgui.IsMouseClicked(1) {
		trkWaveSelA, trkWaveSelB = -1, -1
	}
	if trkWaveDrag != 0 {
		if !imgui.IsMouseDown(0) {
			if trkWaveDrag == 1 && trkWaveSelA == trkWaveSelB {
				trkWaveSelA, trkWaveSelB = -1, -1
			}
			trkWaveDrag = 0
		} else {
			f := xFrame(mouse.X)
			switch trkWaveDrag {
			case 1:
				trkWaveSelB = f
			case 2:
				trkSampleEdit(func(s *tracker.Sample) { s.LoopStart = clampI(snap(f), 0, s.LoopEnd-2) })
			case 3:
				trkSampleEdit(func(s *tracker.Sample) { s.LoopEnd = clampI(snap(f), s.LoopStart+2, n) })
			}
		}
	}
	if hovered && trkWaveDrag == 0 && s.Loop != tracker.LoopNone && (absF32(mouse.X-la) < 5 || absF32(mouse.X-lb) < 5) {
		imgui.SetMouseCursor(imgui.MouseCursorResizeEW)
	}
}
