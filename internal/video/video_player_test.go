package video

// video_player_test.go — the clock, the transport and the GIF composite.
//
// Every test here drives Advance with an explicit dt, which is the whole point
// of the player taking one rather than reading a clock: a four-minute clip is a
// handful of calls, and "what is on screen at 3.75 seconds" is a question with
// one answer rather than a race.

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
)

// withVideoDir points asset resolution at a temporary video/ folder and returns
// it, restoring the real one afterwards.
func withVideoDir(t *testing.T) string {
	t.Helper()
	old := fileutil.ResourcesPath
	root := t.TempDir()
	fileutil.ResourcesPath = root
	t.Cleanup(func() { fileutil.ResourcesPath = old })

	dir := filepath.Join(root, VideoDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

// installClip writes a four-frame, one-second AVI under video/ and returns its
// bare name.
func installClip(t *testing.T, dir, name string) string {
	t.Helper()
	b := &aviBuilder{width: 16, height: 16, fps: 4}
	for i := 0; i < 4; i++ {
		b.frames = append(b.frames, jpegFrame(t, 16, 16, color.RGBA{uint8(40 + i*60), 20, 20, 255}))
	}
	if err := os.WriteFile(filepath.Join(dir, name+".avi"), b.bytes(), 0o644); err != nil {
		t.Fatalf("write clip: %v", err)
	}
	return name
}

func TestPlayerStartsPausedOnTheFirstFrame(t *testing.T) {
	dir := withVideoDir(t)
	p := Open(installClip(t, dir, "intro"))
	defer p.Close()

	if p.Err() != nil {
		t.Fatalf("open: %v", p.Err())
	}
	if p.Playing() {
		t.Error("a freshly opened player is playing; nothing asked it to")
	}
	if p.Frame() == nil {
		t.Error("no first frame — a paused video would draw nothing at all")
	}
	if p.Duration() != 1 {
		t.Errorf("duration = %v, want 1", p.Duration())
	}
}

func TestPlayerAdvancesThroughItsFrames(t *testing.T) {
	dir := withVideoDir(t)
	p := Open(installClip(t, dir, "intro"))
	defer p.Close()
	p.Play()

	first := p.Rev()
	p.Advance(0.3) // into frame 1 of a 4 fps clip
	if p.Rev() == first {
		t.Fatal("crossing a frame boundary did not produce a new picture")
	}
	if got := p.Time(); got < 0.29 || got > 0.31 {
		t.Errorf("time = %v, want 0.3", got)
	}

	// Half of a frame's worth is not a new frame, and must not re-upload one.
	rev := p.Rev()
	p.Advance(0.1)
	if p.Rev() != rev {
		t.Error("a sub-frame step produced a new picture; that is an upload per display frame")
	}
}

func TestPlayerStopsAtTheEndAndSaysSo(t *testing.T) {
	dir := withVideoDir(t)
	p := Open(installClip(t, dir, "intro"))
	defer p.Close()
	p.Play()

	p.Advance(5)
	if p.Playing() {
		t.Error("a one-shot clip is still playing past its end")
	}
	if !p.Ended() {
		t.Error("Ended is false past the end")
	}
	if p.Time() != p.Duration() {
		t.Errorf("time = %v, want it parked at the duration %v", p.Time(), p.Duration())
	}

	// Play on a finished clip means play it, not sit at the end.
	p.Play()
	if !p.Playing() || p.Ended() || p.Time() != 0 {
		t.Errorf("Play on a finished clip left it at %v, ended=%v, playing=%v",
			p.Time(), p.Ended(), p.Playing())
	}
}

func TestPlayerLoopsWithoutDrift(t *testing.T) {
	dir := withVideoDir(t)
	p := Open(installClip(t, dir, "intro"))
	defer p.Close()
	p.SetLoop(true)
	p.Play()

	// Two and a quarter times round a one-second clip.
	p.Advance(2.25)
	if !p.Playing() || p.Ended() {
		t.Fatal("a looping clip stopped at its end")
	}
	if got := p.Time(); got < 0.24 || got > 0.26 {
		t.Errorf("time = %v after 2.25s of a 1s loop, want 0.25", got)
	}
}

func TestPlayerSeeks(t *testing.T) {
	dir := withVideoDir(t)
	p := Open(installClip(t, dir, "intro"))
	defer p.Close()

	p.Seek(0.6)
	if got := p.Time(); got != 0.6 {
		t.Errorf("time = %v after seeking to 0.6", got)
	}
	// Clamped rather than refused: a script asking for ten seconds of a
	// one-second clip means "the end".
	p.Seek(99)
	if p.Time() != p.Duration() {
		t.Errorf("seek past the end left time at %v, want %v", p.Time(), p.Duration())
	}
	p.Seek(-5)
	if p.Time() != 0 {
		t.Errorf("seek before the start left time at %v, want 0", p.Time())
	}
}

func TestMissingVideoIsAPlayerThatDrawsNothing(t *testing.T) {
	withVideoDir(t)
	p := Open("not-a-clip")
	defer p.Close()

	if p.Err() == nil {
		t.Fatal("a missing video opened cleanly")
	}
	// Every one of these has to be safe, because a widget pointed at a typo is
	// ticked every frame for as long as its screen is up.
	p.Play()
	p.Advance(1)
	p.Seek(2)
	p.SetLoop(true)
	p.SetVolume(0.5)
	if p.Frame() != nil || p.Duration() != 0 {
		t.Error("a failed player produced a frame or a duration")
	}
}

// ---------- GIF ----------

func TestGIFComposesPatchesRatherThanDrawingThem(t *testing.T) {
	dir := withVideoDir(t)

	// Frame 0 fills the canvas red. Frame 1 patches a small square blue and
	// leaves the rest alone, which is what "do not dispose" means. A decoder
	// that drew the patches raw would show frame 1 as a blue square on nothing.
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	full := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	patch := image.NewPaletted(image.Rect(2, 2, 4, 4), pal)
	for i := range patch.Pix {
		patch.Pix[i] = 1
	}

	g := &gif.GIF{
		Image:    []*image.Paletted{full, patch},
		Delay:    []int{50, 50},
		Disposal: []byte{gif.DisposalNone, gif.DisposalNone},
		Config:   image.Config{ColorModel: pal, Width: 8, Height: 8},
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anim.gif"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p := Open("anim")
	defer p.Close()
	if p.Err() != nil {
		t.Fatalf("open: %v", p.Err())
	}
	if got := p.Duration(); got != 1 {
		t.Errorf("duration = %v, want 1s for two frames held 50 hundredths each", got)
	}

	p.Seek(0.75) // into the second frame
	img := p.Frame()
	if img == nil {
		t.Fatal("no frame")
	}
	if got := img.RGBAAt(3, 3); got != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("patched pixel is %v, want blue", got)
	}
	if got := img.RGBAAt(7, 7); got != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("unpatched pixel is %v, want the red left over from frame 0 — "+
			"the patch was drawn without the frame under it", got)
	}
}

// ---------- the track ----------

func TestFrameAtPicksTheFrameInForce(t *testing.T) {
	trk := Track{Starts: []float64{0, 0.5, 1.5, 2}, Duration: 3}
	for _, c := range []struct {
		at   float64
		want int
	}{
		{-1, 0}, {0, 0}, {0.49, 0}, {0.5, 1}, {1.4, 1}, {1.5, 2}, {2, 3}, {99, 3},
	} {
		if got := trk.FrameAt(c.at); got != c.want {
			t.Errorf("FrameAt(%v) = %d, want %d", c.at, got, c.want)
		}
	}
}

// ---------- the registry ----------

func TestRegistrySweepClosesWhatNobodyAskedFor(t *testing.T) {
	dir := withVideoDir(t)
	installClip(t, dir, "intro")
	t.Cleanup(CloseAll)
	CloseAll()

	a := Acquire("hud/clip", "intro")
	if Acquire("hud/clip", "intro") != a {
		t.Error("acquiring the same key twice opened the file again")
	}

	Sweep() // acquired this generation, so it survives
	if OpenCount() != 1 {
		t.Fatalf("player count = %d after a sweep that should have kept it", OpenCount())
	}

	Sweep() // a whole generation with nobody asking, so it goes
	if OpenCount() != 0 {
		t.Errorf("player count = %d, want 0 after a generation with no acquire", OpenCount())
	}
}

func TestRepointingAKeyReopensTheFile(t *testing.T) {
	dir := withVideoDir(t)
	installClip(t, dir, "intro")
	installClip(t, dir, "outro")
	t.Cleanup(CloseAll)
	CloseAll()

	first := Acquire("hud/clip", "intro")
	second := Acquire("hud/clip", "outro")
	if first == second {
		t.Fatal("pointing a key at a different clip kept the old player")
	}
	if second.Source != "outro" {
		t.Errorf("player source = %q, want outro", second.Source)
	}
	if OpenCount() != 1 {
		t.Errorf("player count = %d, want 1 — the old one was not closed", OpenCount())
	}
}
