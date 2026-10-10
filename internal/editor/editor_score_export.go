package editor

// editor_score_export.go — File > Export Audio (WAV): the whole score, the
// loop region or the selected bars, rendered offline through the same
// scheduler the game plays it with (play.Render) and written as a 24-bit
// stereo WAV. Mutes and solos are the mixer's; the tempo slider is used when
// asked. The render runs off the frame, like the Tracker's export: a long
// score takes seconds.
//
// The other formats SPEC.md lists (OGG, MP3, FLAC, stems, MIDI, MusicXML,
// PDF, …) and the export dialog of mockup screen 6 are phase 5's.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/play"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreExportKind is what an export covers.
type scoreExportKind int

const (
	scoreExportWhole scoreExportKind = iota
	scoreExportLoop
	scoreExportSelection
)

// Export dialog state.
var (
	scoreShowExport   bool
	scoreExportPath   string
	scoreExportWhat   scoreExportKind
	scoreExportScaled bool
	scoreExportTail   = float32(2)
	scoreExportResult chan string
	// scoreExportPathFor is the range the path was last defaulted for, so
	// choosing another range renames the file until it is typed over.
	scoreExportPathFor scoreExportKind
	scoreExportTyped   bool
)

// scoreExportSpan is the ticks an export covers and a name for it ("bars
// 5-8"), or why it cannot be had. The whole score runs to its end and stops
// there: a score that loops (`loop: true`) would otherwise start again in
// the tail.
func (e *scoreState) exportSpan(k scoreExportKind) (from, to int64, name string, err error) {
	perf := e.perf()
	if perf == nil {
		return 0, 0, "", fmt.Errorf("the score cannot be performed")
	}
	switch k {
	case scoreExportLoop:
		a, b, ok := scoreLoopTicks(perf, e.Transport.Loop)
		if !ok {
			return 0, 0, "", fmt.Errorf("there is no loop region (drag along the ruler, or select bars and press L)")
		}
		return a, b, "loop", nil
	case scoreExportSelection:
		var a, z int
		switch e.Sel.Kind {
		case scoreSelBars:
			a, z = e.Sel.BarA, e.Sel.BarB
		case scoreSelEvent, scoreSelCaret:
			a, z = e.Sel.Loc.Bar, e.Sel.Loc.Bar
		default:
			return 0, 0, "", fmt.Errorf("nothing is selected")
		}
		lp := scoreLoopBars(e.Score, a, z)
		from, to, ok := scoreLoopTicks(perf, lp)
		if !ok {
			return 0, 0, "", fmt.Errorf("bars %d-%d do not play", a, z)
		}
		if a == z {
			return from, to, fmt.Sprintf("bar%d", a), nil
		}
		return from, to, fmt.Sprintf("bars%d-%d", a, z), nil
	}
	return 0, perf.Length, "", nil
}

// scoreExportOptions are the render's options for an export.
func (e *scoreState) exportOptions(k scoreExportKind, scaled bool, tail float32) (play.RenderOptions, error) {
	from, to, _, err := e.exportSpan(k)
	if err != nil {
		return play.RenderOptions{}, err
	}
	o := play.RenderOptions{From: from, To: to, Tail: float64(max(tail, 0)), TempoScale: 1}
	if scaled {
		o.TempoScale = e.Transport.Scale
	}
	return o, nil
}

// scoreExportSeconds is how long an export plays, tail aside.
func (e *scoreState) exportSeconds(k scoreExportKind, scaled bool) float64 {
	perf := e.perf()
	from, to, _, err := e.exportSpan(k)
	if perf == nil || err != nil {
		return 0
	}
	end := perf.Seconds
	if to > 0 {
		end = perf.Tempo.Seconds(float64(to))
	}
	sec := end - perf.Tempo.Seconds(float64(from))
	if scaled && e.Transport.Scale > 0 {
		sec /= e.Transport.Scale
	}
	return sec
}

// scoreRenderWAV renders a score with the options and writes it: the
// export's work, off the frame. Lines whose instruments did not load are
// silent and counted in the message.
func scoreRenderWAV(s *score.Score, o play.RenderOptions, path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	data, errs, err := play.RenderScore(s, o)
	if err != nil {
		return "", err
	}
	rate := o.Rate
	if rate <= 0 {
		rate = 48000
	}
	if err := play.WriteWAV(path, data, rate); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Exported %s (%s)", path, scoreClock(float64(len(data)/2)/float64(rate)))
	if len(errs) > 0 {
		msg += fmt.Sprintf("; %d line(s) silent: %v", len(errs), errs[0])
	}
	return msg, nil
}

// scoreStartExport renders the export in the background.
func scoreStartExport() {
	if scoreExportResult != nil {
		return
	}
	e := scEd
	o, err := e.exportOptions(scoreExportWhat, scoreExportScaled, scoreExportTail)
	if err != nil {
		e.Status = "Export: " + scoreErrText(err)
		return
	}
	s, path := e.Score.Clone(), scoreExportPath
	ch := make(chan string, 1)
	scoreExportResult = ch
	e.Status = "Exporting..."
	go func() {
		msg, err := scoreRenderWAV(s, o, path)
		if err != nil {
			msg = "Export failed: " + err.Error()
		}
		ch <- msg
	}()
}

// scorePollExport shows a finished export's message.
func scorePollExport() {
	if scoreExportResult == nil {
		return
	}
	select {
	case msg := <-scoreExportResult:
		scEd.Status = msg
		scoreExportResult = nil
	default:
	}
}

// scoreDefaultExportPath is exports/<name>[_<range>].wav beside resources/
// — outside sounds/, so a render is never mistaken for the score.
func scoreDefaultExportPath(k scoreExportKind) string {
	name := scEd.Name
	if name == "" {
		name = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(scEd.Score.Title), " ", "_"))
	}
	if name == "" {
		name = "score"
	}
	if _, _, part, err := scEd.exportSpan(k); err == nil && part != "" {
		name += "_" + part
	}
	return filepath.Join(filepath.Dir(filepath.Clean(fileutil.ResourcesPath)), "exports", filepath.FromSlash(name)+".wav")
}

// scoreOpenExport opens the dialog: the loop when one is on, else the
// selected bars when there are some, else the whole score.
func scoreOpenExport() {
	e := scEd
	scoreExportWhat = scoreExportWhole
	switch {
	case e.Transport.Loop.On:
		scoreExportWhat = scoreExportLoop
	case e.Sel.Kind == scoreSelBars:
		scoreExportWhat = scoreExportSelection
	}
	scoreExportScaled = e.Transport.Scale != 1
	scoreExportPath, scoreExportPathFor, scoreExportTyped = scoreDefaultExportPath(scoreExportWhat), scoreExportWhat, false
	scoreShowExport = true
}

// scoreExportDialog is the dialog.
func scoreExportDialog() {
	imguibackend.WithModalPopup("Export Audio##scexport", &scoreShowExport, func() {
		e := scEd
		for i, label := range []string{"Whole score", "Loop region", "Selection"} {
			k := scoreExportKind(i)
			_, _, _, err := e.exportSpan(k)
			if i > 0 {
				imgui.SameLine()
			}
			if imgui.RadioButton(label+"##scexr", scoreExportWhat == k) && err == nil {
				scoreExportWhat = k
			}
			if err != nil {
				editorMenuTooltip(scoreErrText(err))
			}
		}
		if scoreExportWhat != scoreExportPathFor && !scoreExportTyped {
			scoreExportPath, scoreExportPathFor = scoreDefaultExportPath(scoreExportWhat), scoreExportWhat
		}
		if e.Transport.Scale != 1 {
			imgui.Checkbox(fmt.Sprintf("At the tempo slider's %.0f%%##scexscale", e.Transport.Scale*100), &scoreExportScaled)
		}
		imgui.SetNextItemWidth(120)
		imgui.SliderFloatV("Tail##scextail", &scoreExportTail, 0, 8, "%.1f s", 0)
		editorMenuTooltip("Time after the end for the reverb and the last notes to ring out")
		imgui.SetNextItemWidth(520)
		if imgui.InputTextV("Path##scexpath", &scoreExportPath, 0, nil) {
			scoreExportTyped = true
		}
		_, _, _, err := e.exportSpan(scoreExportWhat)
		if err != nil {
			imgui.PushStyleColor(imgui.StyleColorText, saveChipUnsaved)
			imgui.Text(scoreErrText(err))
			imgui.PopStyleColor()
		} else {
			editorTextMuted(fmt.Sprintf("%s plus %.1f s of tail, 48 kHz 24-bit stereo WAV. Mutes and solos are the mixer's.",
				scoreClock(e.exportSeconds(scoreExportWhat, scoreExportScaled)), scoreExportTail))
		}
		if imgui.Button("Export##scexgo") && err == nil && strings.TrimSpace(scoreExportPath) != "" {
			scoreStartExport()
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scexcancel") {
			imgui.CloseCurrentPopup()
		}
	})
}
