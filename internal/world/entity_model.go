package world

// entity_model.go — an entity's model attachments.
//
// An entity holds a *list* of attachments rather than one model, because the
// things a game wants to place are rarely one rigid mesh: a tank is a hull and
// a turret that turn independently, a character is a body and whatever it is
// holding. Each attachment carries its own offset, rotation and scale relative
// to the entity, so moving the entity moves all of them together while each
// keeps its own placement on the entity.
//
// Each attachment also carries its own set of *named* animations. A .fmdl holds
// clips (model_anim.go); an attachment's animation list is what names them for
// the game — "walk" can mean the clip `walk_cycle_02` on one model and
// `locomotion.walk` on another, and a script says `walk` either way. That
// indirection is the whole point: the animation set is per-model because the
// clips are per-model, but the names scripts use are not.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/model"
	"log"
	"path/filepath"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// EntityAnimation is one named, playable animation on an attachment.
type EntityAnimation struct {
	// Name is what scripts and the editor call this animation.
	Name string
	// Clip is the clip in the model document it plays. Empty means "the clip
	// with the same name as this animation", which is what makes the common
	// case — a model whose clips are already called what the game calls them —
	// need no mapping at all.
	Clip string
	// Speed multiplies playback rate; 0 means 1, so an unmentioned speed plays
	// at the authored rate.
	Speed float32
	// Loop overrides the clip's own loop flag. Nil follows the clip, which is
	// where looping is authored — an override exists for the case where one
	// game wants a one-shot out of a clip authored to cycle.
	Loop *bool
	// BlendTime is how many seconds this animation crossfades in from whatever
	// pose is showing when it starts. Nil follows DefaultAnimBlend; an explicit
	// 0 forces a hard cut, for the rare transition — a ragdoll drop, a teleport
	// — that should not soften.
	BlendTime *float32
	// Autoplay starts this animation when the entity loads. At most one per
	// attachment is honoured (the first), since a model can only be in one pose.
	Autoplay bool
}

// DefaultAnimBlend is the crossfade an animation gets when its own BlendTime
// does not say otherwise — long enough to hide the pop of a hard cut, short
// enough not to read as its own transition animation.
const DefaultAnimBlend = float32(0.15)

// effectiveClip returns the clip name this animation plays.
func (a EntityAnimation) EffectiveClip() string {
	if a.Clip != "" {
		return a.Clip
	}
	return a.Name
}

// effectiveSpeed treats 0 as 1 — see Speed.
func (a EntityAnimation) EffectiveSpeed() float32 {
	if a.Speed == 0 {
		return 1
	}
	return a.Speed
}

// EffectiveBlend returns the crossfade this animation starts with — see
// BlendTime.
func (a EntityAnimation) EffectiveBlend() float32 {
	if a.BlendTime == nil {
		return DefaultAnimBlend
	}
	return *a.BlendTime
}

// EntityModel is one model attached to an entity.
type EntityModel struct {
	// Name identifies the attachment within its entity — what a script names to
	// play an animation on this model rather than another, and what an instance
	// names to override this attachment's transform. Unique per entity.
	Name string
	// Path is the model file, stored relative to the install root (see
	// RelAssetPath). Both .fmdl and .obj are accepted.
	Path string
	// Texture overrides the model document's own texture. Empty keeps whatever
	// the model was authored with, which is the usual case — the override is
	// for reskinning one shared mesh per entity type.
	Texture string

	// The attachment's transform *relative to the entity*. Composed under the
	// entity's own matrix, so the entity's rotation and scale carry these with
	// them.
	Offset   mgl32.Vec3
	Rotation mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	Scale    mgl32.Vec3 // per-axis; zero value treated as {1,1,1}

	// Alpha is the attachment's opacity; 0 means "unset" and loads as 1, so an
	// attachment that never mentioned it is opaque rather than invisible.
	Alpha float32
	// Visible hides this attachment without removing it.
	Visible bool

	// Animations are the named clips this attachment can play.
	Animations []EntityAnimation

	// ---------- runtime, never authored and never saved ----------

	// Submeshes is the uploaded mesh. A document compiles to one submesh today
	// (model_doc.go), but the slice is kept so a model that later splits by
	// material needs no change here.
	Submeshes []gfx.Geometry
	// Anim is live playback state, nil on a model with no motion to play.
	Anim *EntityAnim
	// Doc is kept past Load only when there is an armature to pose against;
	// re-reading the file every frame is not an option. Nil for static models,
	// which is what keeps a level of props from holding every mesh twice.
	Doc *model.ModelDoc
	// AnimBlocked is why Anim is nil, in words, or "" when it is not nil.
	// Recorded at Load because that is the only moment the document is in hand:
	// afterwards a static model has dropped it, and "no armature" and "no skin
	// weights" are indistinguishable from an EntityModel alone.
	AnimBlocked string
}

// effectiveScale treats the zero value as identity.
func (m *EntityModel) EffectiveScale() mgl32.Vec3 {
	if m.Scale == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return m.Scale
}

// effectiveAlpha treats 0 as fully opaque — see Alpha.
func (m *EntityModel) EffectiveAlpha() float32 {
	if m.Alpha == 0 {
		return 1
	}
	return m.Alpha
}

// LocalMatrix is the attachment's transform relative to its entity.
func (m *EntityModel) LocalMatrix() mgl32.Mat4 {
	s := m.EffectiveScale()
	T := mgl32.Translate3D(m.Offset.X(), m.Offset.Y(), m.Offset.Z())
	S := mgl32.Scale3D(s.X(), s.Y(), s.Z())
	return T.Mul4(EulerMatrixYXZ(m.Rotation)).Mul4(S)
}

// WorldMatrix is the attachment's full transform: the entity's matrix with the
// attachment's own composed under it.
func (m *EntityModel) WorldMatrix(e *Entity) mgl32.Mat4 {
	if e == nil {
		return m.LocalMatrix()
	}
	return e.ModelMatrix().Mul4(m.LocalMatrix())
}

// ---------- loading ----------

// Load uploads the attachment's mesh to the GPU. ownerID only appears in log
// messages, naming the entity a texture failure belongs to.
//
// Safe to call repeatedly: the previous upload is released first, which is what
// makes it usable as "rebuild after the model path changed in the editor".
func (m *EntityModel) Load(ownerID string) error {
	m.Delete()
	if m.Path == "" {
		return nil
	}
	doc, err := loadModelDocument(fileutil.ResolveAssetPath(m.Path))
	if err != nil {
		return fmt.Errorf("entity %q model %q: %w", ownerID, m.Name, err)
	}

	// A texture override replaces the document's, so the mesh compiles already
	// wearing it rather than being patched per-submesh afterwards.
	if m.Texture != "" {
		doc.Texture = m.Texture
	}

	m.initAnimation(doc)

	for _, sub := range doc.ModelSubmeshes() {
		if len(sub.Indices) == 0 {
			continue
		}
		// Every texture the model reaches for is ensured here: nothing else
		// loads it. The level loader only walks chunk geometry, so a model
		// placed with a texture no wall happens to use would otherwise draw
		// untextured.
		if err := gfx.EnsureTextureLoaded(sub.Texture); err != nil {
			log.Printf("entity %s: model %s: texture %q: %v", ownerID, m.Name, sub.Texture, err)
		}
		g := gfx.Geometry{Smooth: sub.Smooth, SmoothAngle: doc.SmoothAngle}
		if m.Anim != nil {
			// Set before Load, which de-indexes these alongside the positions.
			g.SkinIdx, g.SkinWt = sub.SkinIdx, sub.SkinWt
		}
		// Position is the geometry's own origin for the transparent sort; the
		// mesh is model-space, so that origin is the attachment's offset.
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, m.Offset); err != nil {
			return fmt.Errorf("entity %q model %q: %w", ownerID, m.Name, err)
		}
		g.Alpha = m.EffectiveAlpha()
		m.Submeshes = append(m.Submeshes, g)
	}

	m.bindPalette()
	m.startAutoplay()
	return nil
}

// SetTexture changes what the attachment draws with, both for the next reload
// (the override) and right now (the uploaded submeshes).
//
// Patching the submeshes is the whole of the live change: every draw call binds
// Textures[g.Texture] afresh, so a name swap needs no re-upload of the mesh and
// no reload of the model — see the note above pendingUpload in go.
func (m *EntityModel) SetTexture(texture string) {
	m.Texture = texture
	for i := range m.Submeshes {
		m.Submeshes[i].Texture = texture
	}
}

// Delete releases the attachment's GPU buffers and drops its playback state.
// Reloading without this would leak the previous upload's VAO/VBO.
func (m *EntityModel) Delete() {
	for i := range m.Submeshes {
		m.Submeshes[i].Delete()
	}
	m.Submeshes = nil
	m.Anim = nil
	m.Doc = nil
	m.AnimBlocked = ""
}

// HasGeometry reports whether this attachment has anything uploaded to draw.
func (m *EntityModel) HasGeometry() bool {
	for i := range m.Submeshes {
		if len(m.Submeshes[i].Vertices) > 0 {
			return true
		}
	}
	return false
}

// SetAlpha applies a to every submesh: transparency is a per-Geometry property,
// so it has to reach all of them or half a model would stop blending.
func (m *EntityModel) SetAlpha(a float32) {
	m.Alpha = a
	for i := range m.Submeshes {
		m.Submeshes[i].Alpha = m.EffectiveAlpha()
	}
}

// initAnimation prepares playback state, and keeps the document only when there
// is something to pose. A static model's document is dropped here.
func (m *EntityModel) initAnimation(doc *model.ModelDoc) {
	m.Anim = NewEntityAnim(doc)
	m.AnimBlocked = AnimUnavailable(doc)
	if m.Anim != nil {
		m.Doc = doc
	}
}

// bindPalette hands every submesh a slice header on the shared bone palette.
// The backing array is never reallocated after NewEntityAnim, which is what
// makes sharing it safe: the animation rewrites it in place each frame and the
// colour pass and both shadow passes read it from there.
func (m *EntityModel) bindPalette() {
	if m.Anim == nil {
		return
	}
	for i := range m.Submeshes {
		m.Submeshes[i].BonePalette = m.Anim.Palette
	}
}

// startAutoplay starts the first animation marked Autoplay, if any.
func (m *EntityModel) startAutoplay() {
	for _, a := range m.Animations {
		if a.Autoplay {
			m.Play(a.Name)
			return
		}
	}
}

// ---------- animation control ----------

// HasAnimation reports whether this attachment has motion it can play — both a
// rigged document and at least one animation naming a clip in it.
func (m *EntityModel) HasAnimation() bool {
	return m.Anim != nil && len(m.Animations) > 0
}

// Animation returns the named animation, or nil.
func (m *EntityModel) Animation(name string) *EntityAnimation {
	for i := range m.Animations {
		if m.Animations[i].Name == name {
			return &m.Animations[i]
		}
	}
	return nil
}

// Play starts the named animation from the top and reports whether it resolved.
// An empty name stops playback and returns the model to its rest pose.
//
// It fails rather than falling back to a default when the name is unknown: a
// script asking for an animation that is not there is a bug worth seeing, and
// silently playing "idle" instead would hide it.
func (m *EntityModel) Play(name string) bool {
	if m.Anim == nil {
		return false
	}
	if name == "" {
		m.Anim.SetClip("", 0)
		return true
	}
	a := m.Animation(name)
	if a == nil {
		return false
	}
	if !m.Anim.SetClip(a.EffectiveClip(), a.EffectiveBlend()) {
		return false
	}
	m.Anim.Speed = a.EffectiveSpeed()
	if a.Loop != nil {
		m.Anim.Loop = *a.Loop
	}
	return true
}

// Stop halts playback and returns the model to its rest pose.
func (m *EntityModel) Stop() {
	if m.Anim != nil {
		m.Anim.SetClip("", 0)
	}
}

// Playing returns the name of the animation currently playing, or "".
//
// It maps the live clip back through the animation list rather than remembering
// what Play was told, so a clip started any other way still reports under the
// name the game knows it by.
func (m *EntityModel) Playing() string {
	if m.Anim == nil || !m.Anim.Playing {
		return ""
	}
	c := m.Anim.clip()
	if c == nil {
		return ""
	}
	for _, a := range m.Animations {
		if a.EffectiveClip() == c.Name {
			return a.Name
		}
	}
	return ""
}

// Update advances this attachment's animation clock and reports any clip
// transition it crossed, named the way the game knows the animation rather than
// the way the .fmdl does.
//
// entityID is what the event is addressed to. It is threaded down rather than
// looked up because an attachment has no way back to its entity, and adding one
// would be a cycle for the sake of one string.
func (m *EntityModel) Update(dt float32, entityID string) {
	if m.Anim == nil {
		return
	}
	name := m.Playing()
	m.Anim.Update(dt)

	if m.Anim.wrapped {
		m.Anim.wrapped = false
		// A looping clip emits end-then-start per cycle, in that order: the
		// cycle that just closed, then the one that opened.
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: entityID, Model: m.Name, Animation: name, Ended: true})
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: entityID, Model: m.Name, Animation: name})
	}
	if m.Anim.finished {
		m.Anim.finished = false
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: entityID, Model: m.Name, Animation: name, Ended: true})
	}
}

// ---------- helpers ----------

// loadModelDocument reads a model file into the editable document form, picking
// the reader by extension.
func loadModelDocument(path string) (*model.ModelDoc, error) {
	if strings.EqualFold(filepath.Ext(path), model.ModelExt) {
		return model.LoadModel(path)
	}
	return model.LoadOBJMesh(path)
}

// UniqueModelName returns base, or base with a numeric suffix no attachment on
// the entity is using. Attachment names address a model from a script, so two
// sharing one would make that address ambiguous.
func UniqueModelName(e *Entity, base string) string {
	if base == "" {
		base = "model"
	}
	if e == nil || e.Model(base) == nil {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if e.Model(name) == nil {
			return name
		}
	}
}

// ModelNameForPath is the attachment name a newly picked mesh suggests: the
// file's own name, so an entity wearing models/barrel.fmdl has an attachment
// called "barrel" rather than "model_2".
func ModelNameForPath(path string) string {
	base := filepath.Base(filepath.ToSlash(path))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "." {
		return "model"
	}
	return base
}

// UniqueAnimationName is UniqueModelName for an attachment's animation list.
func UniqueAnimationName(m *EntityModel, base string) string {
	if base == "" {
		base = "animation"
	}
	if m == nil || m.Animation(base) == nil {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if m.Animation(name) == nil {
			return name
		}
	}
}
