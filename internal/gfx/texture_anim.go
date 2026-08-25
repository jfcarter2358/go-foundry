package gfx

// texture_anim.go — animated texture playback.
//
// The texture editor authors an "animation stack": several frames, each its
// own layered document, each flattened down to one RGBA image (see the
// texture package's TexEditorState.SyncAnimatedFrames). What lands here is
// just that ordered stack of already-flattened frames per texture name —
// this package has no layered-document knowledge of its own, matching the
// split that already keeps texdoc (the pure model) and gfx (GPU upload)
// apart everywhere else.
//
// Playback re-specifies the same GL texture object every consumer already
// binds by name (see the note atop the GPU-sync section of go),
// through the existing upload queue — an animated texture needs no changes
// anywhere else in the renderer: a wall, floor, or biome patch showing it
// just sees the pixels change under it, the same way a texture edited live
// in the texture editor already does.
//
// A texture with N frames plays at N frames per second, so a full cycle
// through the stack always takes exactly one second no matter how large the
// stack is — the frame count *is* the fps.

import "image"

type animatedTexture struct {
	frames  []*image.RGBA
	current int // index last uploaded; -1 forces the first tick to upload
}

var animatedTextures = map[string]*animatedTexture{}

// animTick counts AdvanceAnimatedTextures calls — roughly one per main-loop
// frame — and is what TouchAnimatedTexture's grace period is measured in.
var animTick int

// touchedAnimatedTexture/At record the one texture name, if any, whose pixels
// a live edit (SyncLiveTexture, or the texture editor's own per-frame preview
// refresh) most recently drove directly, and when. See TouchAnimatedTexture.
var (
	touchedAnimatedTexture   string
	touchedAnimatedTextureAt int
)

// animTouchGraceTicks is how many AdvanceAnimatedTextures calls a texture
// stays exempt from cycling after its last live-edit touch. It only needs to
// bridge one frame: a touch's QueueTextureUpload call and the drain that
// applies it can straddle a single AdvanceAnimatedTextures call in between,
// and that call would otherwise be free to overwrite the pending edit with
// whatever frame the playback clock landed on. A few frames of margin costs
// nothing — the texture resumes cycling within a fraction of a second of the
// user actually stopping, animation stack left open or not.
const animTouchGraceTicks = 4

// TouchAnimatedTexture marks name as having just been driven by a live edit,
// so AdvanceAnimatedTextures leaves it alone for a few frames rather than
// racing it — see animTouchGraceTicks. Unlike a plain pause, this expires on
// its own shortly after the edits stop, so simply having the texture editor
// open on an already-saved animated texture (to go check it in the 3D view,
// say) doesn't leave it stuck on one frame.
func TouchAnimatedTexture(name string) {
	touchedAnimatedTexture = name
	touchedAnimatedTextureAt = animTick
}

// RegisterAnimatedTexture installs frames as name's animation stack,
// replacing whatever was registered before it. A single frame is not an
// animation: it demotes name back to an ordinary static texture rather than
// leaving a one-frame entry that would trivially cycle through itself.
func RegisterAnimatedTexture(name string, frames []*image.RGBA) {
	if len(frames) <= 1 {
		delete(animatedTextures, name)
		return
	}
	animatedTextures[name] = &animatedTexture{frames: frames, current: -1}
}

// UnregisterAnimatedTexture removes name from the animated set, if present —
// used when a texture is re-saved back down to a single frame.
func UnregisterAnimatedTexture(name string) {
	delete(animatedTextures, name)
}

// AdvanceAnimatedTextures re-uploads whichever frame each registered
// animated texture's clock currently selects. nowSeconds is a free-running
// clock shared with the water/biome-wind animation uniforms (see app.go), so
// every animated texture in the world stays in phase with them and with each
// other — the same way every water surface shares one clock rather than
// starting its ripple from the moment it was first drawn.
func AdvanceAnimatedTextures(nowSeconds float64) {
	animTick++
	for name, a := range animatedTextures {
		if name == touchedAnimatedTexture && animTick-touchedAnimatedTextureAt <= animTouchGraceTicks {
			continue
		}
		n := len(a.frames)
		idx := int(nowSeconds*float64(n)) % n
		if idx == a.current {
			continue
		}
		a.current = idx
		QueueTextureUpload(name, a.frames[idx])
	}
}
