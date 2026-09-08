package uidraw

// uidraw_video_test.go — the video widget's half of the build, and the rule
// that keeps it from re-uploading a frame nobody changed.
//
// No GL and no decoder: gfx.Textures is a plain map, so registering a name in
// it is all "this frame is resident" means to the builder.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/ui"
)

func videoNode(t *testing.T, key string, w, h float32) ui.Node {
	t.Helper()
	n := pinned("film", ui.KindVideo, 0, 0, w, h)
	n.Video.Source = "intro"
	n.NoteVideoTexture(VideoTexName(key))
	return n
}

// residentFrame registers a picture of a size under a key's texture name, as an
// upload would, and removes it again afterwards.
func residentFrame(t *testing.T, key string, w, h float32) {
	t.Helper()
	name := VideoTexName(key)
	gfx.Textures[name] = 4242
	gfx.TextureSizes[name] = [2]float32{w, h}
	t.Cleanup(func() {
		delete(gfx.Textures, name)
		delete(gfx.TextureSizes, name)
	})
}

func TestAVideoWithNoFrameYetDrawsOnlyItsBackground(t *testing.T) {
	n := videoNode(t, "1/s/film", 320, 180)
	l := built(t, screenWith(n), nil)

	// One quad, not two: an untextured picture quad would be a white rectangle
	// flashing at the start of every clip.
	if got := quads(l); got != 1 {
		t.Fatalf("%d quads before the first frame is resident, want 1 (the background)", got)
	}
}

func TestAResidentFrameIsDrawnOverTheBackground(t *testing.T) {
	residentFrame(t, "1/s/film", 320, 180)
	n := videoNode(t, "1/s/film", 320, 180)
	l := built(t, screenWith(n), nil)

	if got := quads(l); got != 2 {
		t.Fatalf("%d quads, want 2 — a background and the picture over it", got)
	}
	if l.Cmds[len(l.Cmds)-1].Tex != 4242 {
		t.Errorf("the last command binds %d, want the frame texture", l.Cmds[len(l.Cmds)-1].Tex)
	}
}

func TestTheDefaultFitLetterboxesRatherThanStretching(t *testing.T) {
	// A sixteen-by-nine film in a square widget: the picture keeps its aspect
	// and the background shows through above and below it.
	residentFrame(t, "1/s/film", 320, 180)
	n := videoNode(t, "1/s/film", 200, 200)
	l := built(t, screenWith(n), nil)

	if quads(l) != 2 {
		t.Fatalf("%d quads, want 2", quads(l))
	}
	// The picture's four vertices come after the background's four.
	pic := l.Verts[4:8]
	top, bottom := pic[0].Y, pic[2].Y
	if top <= 0 || bottom >= 200 {
		t.Errorf("picture spans y %v..%v inside a 200-tall widget; it was stretched, not letterboxed",
			top, bottom)
	}
	if h := bottom - top; h < 111 || h > 114 {
		t.Errorf("picture is %v tall, want about 112.5 (200 * 9/16)", h)
	}
	if pic[0].X != 0 || pic[1].X != 200 {
		t.Errorf("picture spans x %v..%v, want the full width", pic[0].X, pic[1].X)
	}
}

func TestAHiddenVideoDrawsNothing(t *testing.T) {
	residentFrame(t, "1/s/film", 320, 180)
	n := videoNode(t, "1/s/film", 320, 180)
	n.Visible = false
	l := built(t, screenWith(n), nil)

	if got := quads(l); got != 0 {
		t.Errorf("%d quads for a hidden video, want 0", got)
	}
}

func TestDrivingAVideoWithNoSourceReleasesItsTexture(t *testing.T) {
	// A widget whose clip is cleared in the editor must let go of the player
	// and stop claiming a texture, or the file stays open and the last frame
	// keeps being drawn.
	n := videoNode(t, "1/s/film", 320, 180)
	n.Video.Source = ""

	driveVideo("1/s/film", &n, 0.016)

	if got := n.VideoTexture(); got != "" {
		t.Errorf("texture name is %q after the source was cleared, want empty", got)
	}
	if n.VideoDuration() != 0 || n.VideoPlaying() {
		t.Errorf("a sourceless widget reports duration %v playing %v",
			n.VideoDuration(), n.VideoPlaying())
	}
}
