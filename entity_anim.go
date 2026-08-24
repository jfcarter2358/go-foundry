package main

// entity_anim.go — clip playback for one entity model attachment.
//
// The player is deliberately thin: advance a clock, sample the clip into a
// pose, turn the pose into a bone palette, and leave. Everything below that is
// shared with the model editor — PoseDeltas (model_pose.go) is the same call the
// editor's pose mode makes, so a clip cannot look one way in the editor and
// another in the level.
//
// It speaks in *clips* — the names inside a .fmdl — rather than in the names a
// game uses. Mapping one to the other is EntityModel.Play's job (entity_model.go),
// which keeps this file indifferent to how an entity chose to name its motion.
//
// Deformation itself happens on the GPU (model_skin_gpu.go). Nothing here
// touches vertex data after Load: it owns one []mgl32.Mat4 that every submesh
// holds a slice header on, rewrites it in place each frame, and the colour pass
// and both shadow passes read it from there.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// EntityAnim is one attachment's playback state.
//
// Doc is kept alive past Load — the only thing in the runtime that holds a
// ModelDoc — because the bones, the weights and the rest vertices are what a
// pose is evaluated against, and re-reading the file every frame is not an
// option.
type EntityAnim struct {
	Doc     *ModelDoc
	Clip    int // index into Doc.Animations; -1 when nothing is playing
	Time    float32
	Speed   float32
	Playing bool
	Loop    bool

	// pose is reused across frames rather than allocated per frame: a level of
	// animated entities would otherwise churn one slice each, every frame.
	pose ModelPose
	// Palette is the bone palette every submesh points at. Its backing array is
	// never reallocated after newEntityAnim, which is what makes those shared
	// slice headers safe.
	Palette []mgl32.Mat4
}

// newEntityAnim prepares playback state for a rigged, animated document.
// Returns nil for a model that has no motion to play, so the per-frame tick can
// skip it with a single nil check.
func newEntityAnim(doc *ModelDoc) *EntityAnim {
	if doc == nil || len(doc.Bones) == 0 || !doc.hasSkin() || !doc.hasAnimation() {
		return nil
	}
	a := &EntityAnim{
		Doc:     doc,
		Clip:    -1,
		Speed:   1,
		pose:    newModelPose(len(doc.Bones)),
		Palette: make([]mgl32.Mat4, len(doc.Bones)),
	}
	a.restPalette()
	return a
}

// restPalette fills the palette with identities — the rest pose, and what an
// entity shows before any clip is selected.
func (a *EntityAnim) restPalette() {
	for i := range a.Palette {
		a.Palette[i] = mgl32.Ident4()
	}
}

// clip returns the playing clip, or nil.
func (a *EntityAnim) clip() *ModelClip {
	if a == nil || a.Doc == nil {
		return nil
	}
	return a.Doc.Clip(a.Clip)
}

// SetClip starts the named clip from the top. An empty name stops playback and
// returns the entity to its rest pose. Reports whether the name resolved.
func (a *EntityAnim) SetClip(name string) bool {
	if a == nil {
		return false
	}
	if name == "" {
		a.Clip, a.Playing, a.Time = -1, false, 0
		a.restPalette()
		return true
	}
	ci := a.Doc.ClipIndexByName(name)
	if ci < 0 {
		return false
	}
	a.Clip = ci
	a.Time = 0
	a.Playing = true
	// Loop follows the clip's own flag unless the level overrode it, which
	// SetLoop does after this.
	a.Loop = a.Doc.Animations[ci].Loop
	a.update(0)
	return true
}

// Update advances the clock and rebuilds the palette.
func (a *EntityAnim) Update(dt float32) {
	if a == nil || !a.Playing {
		return
	}
	a.update(dt * a.Speed)
}

func (a *EntityAnim) update(dt float32) {
	c := a.clip()
	if c == nil {
		a.restPalette()
		return
	}
	a.Time += dt
	d := c.duration()
	switch {
	case a.Loop:
		a.Time = c.wrapClipTime(a.Time)
	case a.Time >= d:
		// A one-shot settles on its final pose and stops, rather than holding a
		// clock that grows without bound.
		a.Time = d
		a.Playing = false
	}

	c.SampleInto(&a.pose, len(a.Doc.Bones), a.Time)
	deltas := a.Doc.PoseDeltas(a.pose)
	copy(a.Palette, deltas)
}

// ClipNames lists the clips the underlying model holds, for the editor's clip
// picker. These are the .fmdl's own names — what an EntityAnimation's Clip
// field points at, not what the game calls the animation.
func (a *EntityAnim) ClipNames() []string {
	if a == nil || a.Doc == nil {
		return nil
	}
	return a.Doc.ClipNames()
}
