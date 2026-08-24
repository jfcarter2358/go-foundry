package main

// entity_anim.go — playing a model's clips on a level entity.
//
// The player is deliberately thin: advance a clock, sample the clip into a
// pose, turn the pose into a bone palette, and leave. Everything below that is
// shared with the model editor — PoseDeltas (model_pose.go) is the same call the
// editor's pose mode makes, so a clip cannot look one way in the editor and
// another in the level.
//
// Deformation itself happens on the GPU (model_skin_gpu.go). The entity never
// touches vertex data after Load: it owns one []mgl32.Mat4 that every submesh
// holds a slice header on, rewrites it in place each frame, and the colour pass
// and both shadow passes read it from there.

import (
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// EntityAnim is one entity's playback state.
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

// ---------- the entity's side ----------

// InitAnimation attaches playback state, if the loaded document has any. Called
// from Entity.Load, which is the only place a ModelDoc is in hand.
func (e *Entity) InitAnimation(doc *ModelDoc) {
	e.Anim = newEntityAnim(doc)
	if e.Anim == nil {
		return
	}
	if e.AnimationSpeed > 0 {
		e.Anim.Speed = e.AnimationSpeed
	}
	if e.Animation != "" && !e.Anim.SetClip(e.Animation) {
		// A level naming a clip the model does not have is a content error worth
		// seeing, but not worth refusing to load the entity over.
		log.Printf("entity %s: model %s has no animation %q", e.ID, e.ModelPath, e.Animation)
	}
	if e.AnimationLoop != nil {
		e.Anim.Loop = *e.AnimationLoop
	}
}

// bindPalette points every submesh at the entity's palette, so the draw paths
// find it without the entity being involved in the draw call.
func (e *Entity) bindPalette() {
	if e.Anim == nil {
		return
	}
	for i := range e.Submeshes {
		e.Submeshes[i].BonePalette = e.Anim.Palette
	}
}

// UpdateAnimation advances this entity's clip by dt seconds.
func (e *Entity) UpdateAnimation(dt float32) {
	if e.Anim != nil {
		e.Anim.Update(dt)
	}
}

// SetClip switches the entity to a named clip. Reports whether it resolved.
func (e *Entity) SetClip(name string) bool {
	e.Animation = name
	if e.Anim == nil {
		return name == ""
	}
	return e.Anim.SetClip(name)
}

// ClipNames lists what this entity can play, for the 3D editor's picker.
func (e *Entity) ClipNames() []string {
	if e.Anim == nil || e.Anim.Doc == nil {
		return nil
	}
	return e.Anim.Doc.ClipNames()
}

// UpdateEntityAnimations advances every entity in the level. Called once per
// frame from the main loop, outside the editor/gameplay branch so a clip plays
// in the 3D editor view too — an animation you cannot see while placing the
// entity is most of the way to useless.
func UpdateEntityAnimations(level *Level, dt float32) {
	if level == nil {
		return
	}
	for i := range level.Entities {
		level.Entities[i].UpdateAnimation(dt)
	}
}
