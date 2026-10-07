package world

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
	"foundry/internal/model"

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

	Doc     *model.ModelDoc
	Clip    int // index into Doc.Animations; -1 when nothing is playing
	Time    float32
	Speed   float32
	Playing bool
	Loop    bool

	// pose is reused across frames rather than allocated per frame: a level of
	// animated entities would otherwise churn one slice each, every frame.
	pose model.ModelPose

	// blendFrom, blendElapsed and blendDuration are a crossfade in progress:
	// the pose the previous clip was showing at the instant this one started,
	// how long that fade has been running, and how long it lasts. blending is
	// false the rest of the time, which is what lets update skip the blend
	// arithmetic on the common case of nothing mid-transition. blended is the
	// buffer BlendPoses writes the mix into, kept persistent for the same
	// reason pose is: reused rather than allocated every tick.
	blending      bool
	blendFrom     model.ModelPose
	blendElapsed  float32
	blendDuration float32
	blended       model.ModelPose

	// posed is PoseDeltasInto's model-space scratch, kept so posing a body
	// allocates nothing per frame.
	posed []mgl32.Mat4
	// deltas is the palette-length scratch a pose is written into before
	// setPalette compares it with Palette.
	deltas []mgl32.Mat4

	// Palette is the bone palette every submesh points at. Its backing array is
	// never reallocated after NewEntityAnim, which is what makes those shared
	// slice headers safe.
	Palette []mgl32.Mat4

	// PoseGen counts the times Palette's contents have changed: a reader that
	// remembers it can tell a new pose from the one it last saw without
	// comparing every bone — the point-shadow cache (render/
	// point_shadow_cache.go), which used to hash every matrix of every pose in
	// reach of every light, every frame. A clip that re-poses to the same
	// matrices (one finished on its last frame) leaves it alone.
	PoseGen uint64
}

// setPalette writes deltas into Palette, stepping PoseGen only when that
// changes what the palette holds.
func (a *EntityAnim) setPalette(deltas []mgl32.Mat4) {
	n := min(len(a.Palette), len(deltas))
	for i := 0; i < n; i++ {
		if a.Palette[i] != deltas[i] {
			copy(a.Palette[i:n], deltas[i:n])
			a.PoseGen++
			return
		}
	}
}

// AnimUnavailable explains why doc cannot be animated, or "" when it can.
//
// The three conditions are separate on purpose. "This will not animate" is a
// useless thing to be told when a rig is half-built, and the three ways to
// half-build one — bones but no weights, weights but no keys, keys but no bones
// — are indistinguishable from the outside and have completely different fixes.
// NewEntityAnim used to fold them into one boolean; the reason is now carried
// through to the log window and the script editor, so the message names the step
// that is missing rather than the outcome.
func AnimUnavailable(doc *model.ModelDoc) string {
	switch {
	case doc == nil:
		return "the model file did not load"
	case len(doc.Bones) == 0:
		return "the model has no armature, so there are no bones to pose"
	case !doc.HasSkin():
		return "no vertex in the model is weighted to a bone, so a pose would move nothing"
	case !doc.HasAnimation():
		return "the model has an armature but no clip with any keyframes"
	}
	return ""
}

// NewEntityAnim prepares playback state for a rigged, animated document.
// Returns nil for a model that has no motion to play, so the per-frame tick can
// skip it with a single nil check. AnimUnavailable says why.
func NewEntityAnim(doc *model.ModelDoc) *EntityAnim {
	if AnimUnavailable(doc) != "" {
		return nil
	}
	a := &EntityAnim{
		Doc:     doc,
		Clip:    -1,
		Speed:   1,
		pose:    model.NewModelPose(len(doc.Bones)),
		Palette: make([]mgl32.Mat4, len(doc.Bones)),
	}
	a.restPalette()
	return a
}

// restPalette fills the palette with identities — the rest pose, and what an
// entity shows before any clip is selected.
func (a *EntityAnim) restPalette() {
	ident := mgl32.Ident4()
	for i := range a.Palette {
		if a.Palette[i] != ident {
			a.Palette[i] = ident
			a.PoseGen++ // once per changed bone: it only has to move
		}
	}
}

// clip returns the playing clip, or nil.
func (a *EntityAnim) clip() *model.ModelClip {
	if a == nil || a.Doc == nil {
		return nil
	}
	return a.Doc.Clip(a.Clip)
}

// SetClip starts the named clip from the top, crossfading in from whatever
// pose is currently on screen over blend seconds — 0 for the instant cut that
// used to be the only option. An empty name stops playback and returns the
// entity to its rest pose immediately; there is no clip left to keep sampling
// once nothing is playing, so a fade-to-rest is not offered. Reports whether
// the name resolved.
func (a *EntityAnim) SetClip(name string, blend float32) bool {
	if a == nil {
		return false
	}
	if name == "" {
		a.Clip, a.Playing, a.Time = -1, false, 0
		a.blending = false
		a.restPalette()
		return true
	}
	ci := a.Doc.ClipIndexByName(name)
	if ci < 0 {
		return false
	}
	if blend > 0 && a.Clip >= 0 {
		// Depart from whatever is actually being shown, mid-blend included: a
		// second switch before the first crossfade finishes should bend the fade
		// toward its new target, not snap to wherever the interrupted one had
		// gotten to. currentPose is copied because it returns an in-place buffer
		// (pose or blended) that the very next update would otherwise overwrite
		// out from under blendFrom — into blendFrom's own backing array, which a
		// soldier switching clips several times a second reuses rather than
		// allocating a fresh pose per switch.
		cur := a.currentPose()
		a.blendFrom.Bones = append(a.blendFrom.Bones[:0], cur.Bones...)
		a.blendElapsed = 0
		a.blendDuration = blend
		a.blending = true
	} else {
		a.blending = false
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

// currentPose is what the last update actually put on screen: the blend in
// progress if one is running, or the active clip's own sample otherwise.
func (a *EntityAnim) currentPose() model.ModelPose {
	if a.blending {
		return a.blended
	}
	return a.pose
}

// Update advances the clock and rebuilds the palette.
func (a *EntityAnim) Update(dt float32) {
	a.UpdatePose(dt, true)
}

// UpdatePose advances the clock, and rebuilds the palette only when pose is
// set. Everything that depends on time alone — a loop's wrap, a one-shot's end,
// a blend's progress — moves either way, so events and timing are the same
// whether or not this frame was posed; skipping the pose only leaves the
// palette a frame or two behind, which is what a body a hundred metres off can
// afford (see AnimPoseDue).
func (a *EntityAnim) UpdatePose(dt float32, pose bool) {
	if a == nil || !a.Playing {
		return
	}
	a.advance(dt*a.Speed, pose)
}

func (a *EntityAnim) update(dt float32) {
	a.advance(dt, true)
}

func (a *EntityAnim) advance(dt float32, pose bool) {
	c := a.clip()
	if c == nil {
		a.blending = false
		a.restPalette()
		return
	}
	a.Time += dt
	d := c.EffectiveDuration()
	switch {
	case a.Loop:
		// A wrap is a cycle boundary: the clip ended and began again, which is
		// what makes footstep and cycle-counting scripts writable.
		if d > 0 && (a.Time >= d || a.Time < 0) {
			a.wrapped = true
		}
		a.Time = c.WrapClipTime(a.Time)
	case a.Time >= d:
		// A one-shot settles on its final pose and stops, rather than holding a
		// clock that grows without bound.
		if a.Playing {
			a.finished = true
		}
		a.Time = d
		a.Playing = false
	}

	if !pose {
		if a.blending {
			if a.blendElapsed += dt; a.blendElapsed >= a.blendDuration {
				a.blending = false
			}
		}
		return
	}

	c.SampleInto(&a.pose, len(a.Doc.Bones), a.Time)

	shown := &a.pose
	if a.blending {
		a.blendElapsed += dt
		if u := a.blendElapsed / a.blendDuration; u < 1 {
			model.BlendPoses(&a.blended, a.blendFrom, a.pose, u)
			shown = &a.blended
		} else {
			a.blending = false
		}
	}

	// Posed into a scratch the length of the palette, then handed to
	// setPalette, so PoseGen steps only when a bone actually moved (the point
	// shadows' cache reads it). The scratch starts as a copy of the palette
	// because PoseDeltasInto writes only the overlap of the palette and the
	// armature, and the rest must compare equal.
	if cap(a.deltas) < len(a.Palette) {
		a.deltas = make([]mgl32.Mat4, len(a.Palette))
	}
	a.deltas = a.deltas[:len(a.Palette)]
	copy(a.deltas, a.Palette)
	a.deltas, a.posed = a.Doc.PoseDeltasInto(a.deltas, a.posed, *shown)
	a.setPalette(a.deltas)
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
