package uidraw

// uidraw_video.go — the join between a video widget and a decoded picture.
//
// internal/ui describes a video node and has no idea how to open a file;
// internal/video opens files and has no idea what a widget is. This is the
// twenty lines in the middle, and it lives here because it is the half that
// touches the GPU — one more upload queued into the same drain the glyph pages
// go through, under a texture name nothing else can collide with.
//
// The upload is skipped whenever the player's Rev has not moved. That matters
// more than it sounds: a twelve-frames-a-second animation on a 144 Hz display
// would otherwise re-upload the same eight megabytes a hundred and forty-four
// times a second, and a *paused* one would too.

import (
	"foundry/internal/gfx"
	"foundry/internal/ui"
	"foundry/internal/video"
)

// videoTexPrefix namespaces a player's texture in the shared registry.
//
// The at-sign is not legal in a filename this engine will scan, so a video's
// texture can never be shadowed by — or shadow — an actual image under
// textures/, and an author who types it into a texture field gets a missing
// asset rather than somebody's cutscene.
const videoTexPrefix = "@video/"

// VideoTexName is the name a widget's current frame is registered under, for
// the build below and for a test that wants to check the upload happened.
func VideoTexName(key string) string { return videoTexPrefix + key }

// uploaded is the player Rev each video texture last saw. Keyed like the
// registry, and swept alongside it.
var uploaded = map[string]uint64{}

// InstallVideoDriver points the screen stack at this package's driver. Called
// from Init, so anything that can draw can play.
func InstallVideoDriver() { ui.VideoDriver = driveVideo }

// driveVideo makes one player match one widget, advances it, and gets its frame
// onto a texture.
//
// The order matters: the transport is applied *before* the advance, so a seek
// and a play issued by a script in the same tick land on the same frame rather
// than one tick apart.
func driveVideo(key string, n *ui.Node, dt float32) {
	t := n.TakeVideoTransport()
	if t.Source == "" {
		// A widget with no clip chosen yet. Releasing rather than holding an
		// empty player means clearing the source in the editor closes the file
		// immediately, which is what makes replacing an asset on disk possible
		// without quitting.
		video.Release(key)
		delete(uploaded, VideoTexName(key))
		n.NoteVideoTexture("")
		n.NoteVideo(0, 0, false, false)
		return
	}

	p := video.Acquire(key, t.Source)
	p.SetLoop(t.Loop)
	p.SetVolume(t.Volume)
	p.SetMuted(t.Muted)
	if t.Seeking {
		p.Seek(t.Seek)
	}
	switch {
	case t.Playing && !p.Playing():
		p.Play()
	case !t.Playing && p.Playing():
		p.Pause()
	}

	p.Advance(dt)
	uploadFrame(key, p)
	n.NoteVideoTexture(VideoTexName(key))
	n.NoteVideo(p.Time(), p.Duration(), p.Playing(), p.Ended())
}

func uploadFrame(key string, p *video.Player) {
	img := p.Frame()
	if img == nil {
		return
	}
	name := VideoTexName(key)
	if uploaded[name] == p.Rev() {
		return
	}
	// Through the UI queue, not the world's: a video frame wants linear
	// filtering and clamped edges, and a mip chain built from a picture that is
	// replaced thirty times a second would be a downsample per frame for a
	// texture nothing ever minifies.
	gfx.QueueUITextureUpload(name, img)
	uploaded[name] = p.Rev()
}

// SweepVideos closes every player that no screen asked for this frame.
//
// Called once per frame from the frame loop, after every stack has ticked. Not
// from inside the tick, because two stacks tick in the same frame in the editor
// and a sweep between them would close the players the other one is using.
func SweepVideos() {
	video.Sweep()
	for name := range uploaded {
		if video.Held(name[len(videoTexPrefix):]) {
			continue
		}
		// The player is gone, so the texture it was feeding is dead. Deleting
		// it here rather than leaving it registered is what keeps a session
		// that has played six cutscenes from holding six frame buffers.
		gfx.ReleaseTexture(name)
		delete(uploaded, name)
	}
}
