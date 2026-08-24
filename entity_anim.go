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
	// wrapped and finished record the transitions the last Update crossed, for
	// the scripting engine's :anim_start: and :anim_end: events. Set here rather
	// than derived by a watcher because this is the only place that sees the
	// clock cross a boundary; EntityModel.Update reads and clears them.
	wrapped  bool
	finished bool

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

// animUnavailable explains why doc cannot be animated, or "" when it can.
//
// The three conditions are separate on purpose. "This will not animate" is a
// useless thing to be told when a rig is half-built, and the three ways to
// half-build one — bones but no weights, weights but no keys, keys but no bones
// — are indistinguishable from the outside and have completely different fixes.
// newEntityAnim used to fold them into one boolean; the reason is now carried
// through to the log window and the script editor, so the message names the step
// that is missing rather than the outcome.
func animUnavailable(doc *ModelDoc) string {
	switch {
	case doc == nil:
		return "the model file did not load"
	case len(doc.Bones) == 0:
		return "the model has no armature, so there are no bones to pose"
	case !doc.hasSkin():
		return "no vertex in the model is weighted to a bone, so a pose would move nothing"
	case !doc.hasAnimation():
		return "the model has an armature but no clip with any keyframes"
	}
	return ""
}

// newEntityAnim prepares playback state for a rigged, animated document.
// Returns nil for a model that has no motion to play, so the per-frame tick can
// skip it with a single nil check. animUnavailable says why.
func newEntityAnim(doc *ModelDoc) *EntityAnim {
	if animUnavailable(doc) != "" {
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
		// A wrap is a cycle boundary: the clip ended and began again, which is
		// what makes footstep and cycle-counting scripts writable.
		if d > 0 && (a.Time >= d || a.Time < 0) {
			a.wrapped = true
		}
		a.Time = c.wrapClipTime(a.Time)
	case a.Time >= d:
		// A one-shot settles on its final pose and stops, rather than holding a
		// clock that grows without bound.
		if a.Playing {
			a.finished = true
		}
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
