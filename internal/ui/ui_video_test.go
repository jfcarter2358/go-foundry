package ui

// ui_video_test.go — the video widget as a document and as a transport.
//
// Nothing here decodes anything. That is the point of the driver being a
// function variable: the whole of what the document layer promises about a
// video can be checked against a stub that counts calls, with no file, no
// codec and no GL context.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/hooks"
)

func TestVideoNodeRoundTrips(t *testing.T) {
	old := fileutil.ResourcesPath
	root := t.TempDir()
	fileutil.ResourcesPath = root
	t.Cleanup(func() { fileutil.ResourcesPath = old })
	if err := os.MkdirAll(filepath.Join(root, UIDirName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s := NewScreen("cutscene")
	n := NewNode("film", KindVideo)
	n.Video.Source = "intro"
	n.Video.AutoPlay = true
	n.Video.Loop = true
	n.Video.Muted = true
	n.Video.Volume = 0.4
	n.Style.Fill = FillFill
	s.Root.Children = append(s.Root.Children, n)

	path := UIPath("cutscene")
	if err := SaveScreen(s, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := LoadScreen(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got := back.Find("film")
	if got == nil {
		t.Fatal("the video node did not survive the round trip")
	}
	if got.Kind != KindVideo {
		t.Fatalf("kind = %s, want video", KindName(got.Kind))
	}
	if got.Video != (VideoSpec{Source: "intro", AutoPlay: true, Loop: true, Muted: true, Volume: 0.4}) {
		t.Errorf("video spec came back as %+v", got.Video)
	}
	if got.Style.Fill != FillFill {
		t.Errorf("fill = %s, want fill", FillModeNames[got.Style.Fill])
	}
}

func TestAVideoDefaultsToLetterboxOnBlack(t *testing.T) {
	// Both halves of "a video looks right before anyone styles it": bars that
	// are black rather than white, and a picture that keeps its aspect.
	n := NewNode("film", KindVideo)
	if n.Style.Fill != FillFit {
		t.Errorf("fill = %s, want fit", FillModeNames[n.Style.Fill])
	}
	if n.Style.Color != DefaultBackground(KindVideo) || n.Style.Color[3] != 1 {
		t.Errorf("background = %v, want opaque black", n.Style.Color)
	}
}

func TestADefaultVideoNodeWritesNoVideoBlock(t *testing.T) {
	// The pointer-vs-zero rule, applied to the new block: a widget nobody has
	// pointed at a file should not put four defaulted keys in the document.
	raw := nodeToRaw(ptr(NewNode("film", KindVideo)))
	if raw.Video != nil {
		t.Errorf("an unconfigured video node wrote %+v", *raw.Video)
	}
	// …and a fill of "fit" is this kind's default, so it is not written either.
	if raw.Fill != "" {
		t.Errorf("fill was written as %q; fit is the default for a video", raw.Fill)
	}
}

func ptr(n Node) *Node { return &n }

// stubDriver is a VideoDriver that plays an imaginary clip of a fixed length,
// recording what it was asked to do.
type stubDriver struct {
	duration float32
	keys     []string
	last     VideoTransport
	time     float32
}

func (d *stubDriver) drive(key string, n *Node, dt float32) {
	d.keys = append(d.keys, key)
	t := n.TakeVideoTransport()
	d.last = t
	if t.Seeking {
		d.time = t.Seek
	}
	if t.Playing {
		d.time += dt
	}
	ended := false
	if d.time >= d.duration {
		d.time = d.duration
		ended = true
	}
	n.NoteVideo(d.time, d.duration, t.Playing && !ended, ended)
}

func withDriver(t *testing.T, d *stubDriver) {
	t.Helper()
	old := VideoDriver
	VideoDriver = d.drive
	t.Cleanup(func() { VideoDriver = old })
}

func videoStack(t *testing.T, autoplay bool) (*Stack, *Screen, *Node) {
	t.Helper()
	s := NewScreen("cutscene")
	n := NewNode("film", KindVideo)
	n.Video.Source = "intro"
	n.Video.AutoPlay = autoplay
	s.Root.Children = append(s.Root.Children, n)

	var st Stack
	st.Push(s)
	hooks.TakeUIEvents() // drop the :shown:
	return &st, s, s.Find("film")
}

func TestAutoPlayStartsOnTheFirstTickAndOnlyThen(t *testing.T) {
	d := &stubDriver{duration: 10}
	withDriver(t, d)
	st, _, n := videoStack(t, true)

	st.Tick(0.1, 1920, 1080)
	if !d.last.Playing {
		t.Fatal("an autoplay video was not started on its first tick")
	}

	// Pausing it must stick: re-applying AutoPlay every tick would make the
	// widget impossible to stop.
	n.SetVideoPlaying(false)
	st.Tick(0.1, 1920, 1080)
	if d.last.Playing {
		t.Error("a paused autoplay video started itself again on the next tick")
	}
}

func TestAVideoWithoutAutoPlayWaitsToBeTold(t *testing.T) {
	d := &stubDriver{duration: 10}
	withDriver(t, d)
	st, _, n := videoStack(t, false)

	st.Tick(0.1, 1920, 1080)
	if d.last.Playing {
		t.Fatal("a video with autoplay off started by itself")
	}

	n.SetVideoPlaying(true)
	st.Tick(0.5, 1920, 1080)
	if !d.last.Playing {
		t.Fatal("writing playing did not reach the driver")
	}
	if n.VideoDuration() != 10 {
		t.Errorf("duration read back as %v, want the driver's 10", n.VideoDuration())
	}
}

func TestASeekReachesTheDriverExactlyOnce(t *testing.T) {
	d := &stubDriver{duration: 10}
	withDriver(t, d)
	st, _, n := videoStack(t, true)

	st.Tick(0.1, 1920, 1080)
	n.SeekVideo(4)
	st.Tick(0.1, 1920, 1080)
	if !d.last.Seeking || d.last.Seek != 4 {
		t.Fatalf("the seek did not arrive: %+v", d.last)
	}
	st.Tick(0.1, 1920, 1080)
	if d.last.Seeking {
		// A seek repeated every frame would pin a playing clip in place.
		t.Error("the seek arrived a second time")
	}
}

func TestReachingTheEndPostsFinishedOnce(t *testing.T) {
	d := &stubDriver{duration: 0.2}
	withDriver(t, d)
	st, _, n := videoStack(t, true)

	st.Tick(0.5, 1920, 1080) // straight past the end
	evs := hooks.TakeUIEvents()
	if len(evs) != 1 || evs[0].Atom != "finished" || evs[0].Widget != "film" {
		t.Fatalf("events after the clip ended: %+v", evs)
	}
	if !n.VideoEnded() {
		t.Error("the node does not report the clip as ended")
	}

	st.Tick(0.5, 1920, 1080)
	if evs := hooks.TakeUIEvents(); len(evs) != 0 {
		t.Errorf("finished fired again on a clip that was already over: %+v", evs)
	}
}

func TestTwoStacksShowingOneScreenGetDifferentPlayers(t *testing.T) {
	d := &stubDriver{duration: 10}
	withDriver(t, d)

	game, _, _ := videoStack(t, true)
	preview, _, _ := videoStack(t, true)
	game.Tick(0.1, 1920, 1080)
	preview.Tick(0.1, 1920, 1080)

	if len(d.keys) != 2 {
		t.Fatalf("driver called %d times, want 2", len(d.keys))
	}
	if d.keys[0] == d.keys[1] {
		// One player serving the game and the editor's preview would have them
		// fighting over one playhead.
		t.Errorf("both stacks asked for the same player key %q", d.keys[0])
	}
}
