package script

// script_video_test.go — driving a video widget from flang.
//
// Headless, and with no decoder at all: ui.VideoDriver is a stub that reports a
// clip of a known length. What is being asserted is the property table and the
// :finished: routing, both of which are easy to get half-right — a getter that
// works and a setter that silently does nothing reads as a broken video rather
// than as a missing line here.

import (
	"testing"

	"foundry/internal/ui"
)

// stubVideo answers every widget with the same imaginary ten-second clip.
func stubVideo(t *testing.T, duration float32) {
	t.Helper()
	prev := ui.VideoDriver
	at := map[string]float32{}
	ui.VideoDriver = func(key string, n *ui.Node, dt float32) {
		tr := n.TakeVideoTransport()
		if tr.Source == "" {
			n.NoteVideo(0, 0, false, false)
			return
		}
		if tr.Seeking {
			at[key] = tr.Seek
		}
		if tr.Playing {
			at[key] += dt
		}
		ended := at[key] >= duration
		if ended {
			at[key] = duration
		}
		n.NoteVideo(at[key], duration, tr.Playing && !ended, ended)
	}
	t.Cleanup(func() { ui.VideoDriver = prev })
}

func screenWithVideo(id, scriptPath string) *ui.Screen {
	s := ui.NewScreen(id)
	s.Canvas = ui.Canvas{Mode: ui.ScaleConstantPixel}
	film := pinnedWidget("film", ui.KindVideo, 0, 0, 320, 180)
	film.Video.Source = "intro"
	film.Script = scriptPath
	s.Root.Children = []ui.Node{film}
	return s
}

func TestAScriptStartsAndReadsAVideo(t *testing.T) {
	root := withTempScriptDir(t)
	stubVideo(t, 10)
	st := withUIStack(t)
	path := writeScript(t, root, "v.flang", `def on_spawn(:spawn:) -> null
    @self.prop.playing = true
    @self.prop.loop = true
end`)

	st.Push(screenWithVideo("cutscene", path))
	st.Tick(0, 800, 600)

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 3)

	film := st.Find("cutscene").Find("film")
	if !film.VideoPlaying() {
		t.Error("writing .prop.playing did not start the clip")
	}
	if !film.Video.Loop {
		t.Error("writing .prop.loop did not reach the widget")
	}

	st.Tick(2, 800, 600)
	if got := film.VideoTime(); got < 1.9 || got > 2.1 {
		t.Errorf("time reads %v after two seconds of ticking, want about 2", got)
	}
	if film.VideoDuration() != 10 {
		t.Errorf("duration reads %v, want the driver's 10", film.VideoDuration())
	}
}

func TestAScriptSeeksAVideo(t *testing.T) {
	root := withTempScriptDir(t)
	stubVideo(t, 10)
	st := withUIStack(t)
	path := writeScript(t, root, "v.flang", `def on_spawn(:spawn:) -> null
    @self.prop.time = 6.5
end`)

	st.Push(screenWithVideo("cutscene", path))
	st.Tick(0, 800, 600)
	lvl := tickTestLevel(t)
	runTicks(t, lvl, 3)

	film := st.Find("cutscene").Find("film")
	if got := film.VideoTime(); got != 6.5 {
		t.Errorf("time is %v after writing 6.5", got)
	}
	st.Tick(0, 800, 600)
	if got := film.VideoTime(); got != 6.5 {
		t.Errorf("the seek did not survive the next tick: %v", got)
	}
}

func TestFinishedReachesTheScript(t *testing.T) {
	root := withTempScriptDir(t)
	stubVideo(t, 0.5)
	st := withUIStack(t)
	path := writeScript(t, root, "v.flang", `def on_finished(:finished:, src) -> null
    @self.attr.over = 1
end`)

	s := screenWithVideo("cutscene", path)
	s.Find("film").Video.AutoPlay = true
	st.Push(s)
	st.Tick(1, 800, 600) // straight past the end

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 2)

	film := st.Find("cutscene").Find("film")
	if v, ok := film.Attr("over"); !ok {
		t.Fatal(":finished: never reached a handler")
	} else if n, _ := v.Int(); n != 1 {
		t.Errorf("over is %d, want 1", n)
	}
}

func TestRepointingAVideoRewindsIt(t *testing.T) {
	root := withTempScriptDir(t)
	stubVideo(t, 10)
	st := withUIStack(t)
	path := writeScript(t, root, "v.flang", `def on_spawn(:spawn:) -> null
    @self.prop.video = "outro"
end`)

	// The widget is already four seconds into its first clip when the script
	// repoints it, which is the only way the rewind is visible at all.
	s := screenWithVideo("cutscene", path)
	s.Find("film").SeekVideo(4)
	st.Push(s)
	st.Tick(0, 800, 600)

	film := st.Find("cutscene").Find("film")
	if film.VideoTime() != 4 {
		t.Fatalf("the clip did not start at 4: %v", film.VideoTime())
	}

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 3)
	st.Tick(0, 800, 600)

	if film.Video.Source != "outro" {
		t.Fatalf("source is %q, want outro", film.Video.Source)
	}
	if film.VideoTime() != 0 {
		t.Errorf("time is %v after a change of clip; a new film starts at its start", film.VideoTime())
	}
}
