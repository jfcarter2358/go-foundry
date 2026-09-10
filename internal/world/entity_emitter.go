package world

// entity_emitter.go — the particle effects an entity carries.
//
// A model attachment (entity_model.go) gives an entity a mesh, and its brushes
// (entity_brush.go) give it solid. This gives it *effects*: the thruster plume
// on a ship, the flame on a torch, the smoke off a wrecked crate — authored on
// the type, in the type's own local space, and carried wherever the thing goes.
//
// It is the third instance of one arrangement, and deliberately the same shape
// as the other two: a definition holds templates with no runtime state, and a
// level materialises one live object per placement. For brushes the live object
// is a frame-owned Chunk; here it is a level Emitter with an Attach naming the
// entity — see the materialising section at the bottom of this file, and
// levelio/entity_emitters.go for why it is here and its twin is there.
// Everything downstream — the simulation
// in EmitterUpdate, the instanced sprite pass, culling, `@ship.thruster` in a
// script — then works with no further change, because a materialised emitter is
// an ordinary one.
//
// # What is here and what is in the .fpar
//
// The same split an emitter already makes. What the effect IS lives in
// particles/<name>.fpar; this says where one hangs off the entity and carries
// the handful of knobs worth varying between two placements of it. A type
// therefore never restates a curve or a texture, and retuning a plume is one
// document rather than every ship that has one.
//
// # Riding a bone
//
// Model and Bone are the reason this is not simply "an emitter with an offset".
// A flame on a torch a character is holding has to follow the hand, and Attach
// already resolves entity-plus-model-plus-bone for a level's own emitters. All
// a definition needs to do is name the two, and the materialised emitter takes
// the existing path.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
)

// EntityEmitter is one particle effect a type carries, in the entity's local
// space.
//
// The zero value is not a usable emitter — Enabled, Emitting and RateScale all
// want non-zero defaults — so construct one with NewEntityEmitter or through
// entityEmitterFromRaw, both of which apply them.
type EntityEmitter struct {
	// Name identifies the attachment within the type, the way a model
	// attachment's does: it is what an instance override matches on, and what
	// the materialised emitter's level ID is built from.
	Name string

	// Effect names a document under particles/, without the extension. Empty
	// is legitimate and produces nothing — the same "added it, have not picked
	// one yet" state a model attachment with no mesh is in.
	Effect string

	// Model and Bone name a socket inside one of the entity's model
	// attachments. Both empty — the ordinary case — hangs the emitter off the
	// entity's own origin instead.
	Model string
	Bone  string

	// Offset is the position in the entity's local space, or in the bone's when
	// one is named. Rotation is Euler XYZ in radians and aims the emission
	// shape, which is what points a thruster's cone backwards.
	Offset   mgl32.Vec3
	Rotation mgl32.Vec3

	// Enabled and Emitting are the emitter's own two switches, carried onto the
	// placement — see Emitter's fields for what each one stops. A type whose
	// effect only runs when a script says so ships with Emitting off.
	Enabled  bool
	Emitting bool
	// RateScale multiplies every layer's rate, and Tint every layer's tint, so
	// two types can share one document and still look different.
	RateScale float32
	Tint      mgl32.Vec3

	// Tags are the classification the materialised emitter carries, so `#tag`
	// reaches every ship's thruster at once. Authored on the type rather than
	// per placement, which is the level an effect is usually addressed at.
	Tags []string
}

// NewEntityEmitter returns an attachment with the defaults a freshly added one
// should have: running, unscaled, untinted.
func NewEntityEmitter(name string) EntityEmitter {
	return EntityEmitter{
		Name:      name,
		Enabled:   true,
		Emitting:  true,
		RateScale: 1,
		Tint:      mgl32.Vec3{1, 1, 1},
	}
}

// EntityEmitterID is the level-unique ID one of an entity's emitters carries
// once materialised: the entity's ID and the attachment's name, joined.
//
// Qualified by the placement for the reason EntityBrushID is — every shuttle
// stamped from one definition calls its plume "thruster", and an emitter ID has
// to name exactly one thing for `@id` to mean anything.
func EntityEmitterID(entityID, name string) string {
	if entityID == "" {
		return name
	}
	return entityID + "." + name
}

// ---------- what an entity's effects are ----------

// HasEmitters reports whether the entity carries any effect at all — the test
// every path that would otherwise walk an empty list takes first.
func (e *Entity) HasEmitters() bool { return e != nil && len(e.Emitters) > 0 }

// EntityEmitter looks up one of the entity's effects by its authored name, or
// returns nil.
//
// Named for the type rather than as a bare Emitter method, because Entity does
// not have an `Emitter` field to be confused with and a plain `e.Emitter(name)`
// would read as "the live emitter", which this is not — it is the template the
// level's live one was built from.
func (e *Entity) EntityEmitter(name string) *EntityEmitter {
	if e == nil {
		return nil
	}
	for i := range e.Emitters {
		if e.Emitters[i].Name == name {
			return &e.Emitters[i]
		}
	}
	return nil
}

// EmitterNamed returns the definition's effect attachment with that name, or
// nil. The uniqueness test the entity editor's rename runs.
func (d *EntityDef) EmitterNamed(name string) *EntityEmitter {
	if d == nil {
		return nil
	}
	for i := range d.Emitters {
		if d.Emitters[i].Name == name {
			return &d.Emitters[i]
		}
	}
	return nil
}

// EffectiveRateScale is the multiplier, never negative — the same clamp
// Emitter.EffectiveRateScale applies, and for the same reason: a negative rate
// emits nothing and reads as a bug rather than as a setting.
func (m *EntityEmitter) EffectiveRateScale() float32 {
	if m.RateScale < 0 {
		return 0
	}
	return m.RateScale
}

// EffectiveTint is the tint, with the zero value read as white. A struct
// written field by field — a test, or a definition predating the field — would
// otherwise multiply every layer by black and emit invisible particles.
func (m *EntityEmitter) EffectiveTint() mgl32.Vec3 {
	if m.Tint == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return m.Tint
}

// LocalMatrix is the attachment's own transform, before whatever carries it.
func (m *EntityEmitter) LocalMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(m.Offset.X(), m.Offset.Y(), m.Offset.Z())
	return T.Mul4(EulerMatrixYXZ(m.Rotation))
}

// UniqueEntityEmitterName returns base, or base with a numeric suffix no
// attachment in the list is using. What the entity editor's "Add Emitter"
// names a new one.
func UniqueEntityEmitterName(emitters []EntityEmitter, base string) string {
	if base == "" {
		base = "emitter"
	}
	taken := func(name string) bool {
		for i := range emitters {
			if emitters[i].Name == name {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if !taken(name) {
			return name
		}
	}
}

// CloneEntityEmitters deep-copies an attachment list, so a definition and every
// instance stamped from it own their effects outright.
//
// Only Tags is a slice, but it is enough: two entities sharing one backing
// array would mean tagging one ship's thruster tagged every ship's.
func CloneEntityEmitters(emitters []EntityEmitter) []EntityEmitter {
	if len(emitters) == 0 {
		return nil
	}
	out := make([]EntityEmitter, len(emitters))
	copy(out, emitters)
	for i := range out {
		out[i].Tags = append([]string(nil), emitters[i].Tags...)
	}
	return out
}

// ---------- serialization ----------

// RawEntityEmitter is the YAML form, on a definition and as an instance
// override alike.
//
// Pointers throughout for the fields with non-zero defaults, for the reason
// RawEntityModel's vectors are: an absent key has to mean "whatever the
// definition said" rather than "zero", and `enabled: false` is a thing an
// author writes on purpose.
type RawEntityEmitter struct {
	Name      string    `yaml:"name"`
	Effect    string    `yaml:"effect,omitempty"`
	Model     string    `yaml:"model,omitempty"` // model attachment holding Bone
	Bone      string    `yaml:"bone,omitempty"`
	Offset    *RawVec3  `yaml:"offset,omitempty"`
	Rotation  *RawVec3  `yaml:"rotation,omitempty"`
	Enabled   *bool     `yaml:"enabled,omitempty"`  // default true when absent
	Emitting  *bool     `yaml:"emitting,omitempty"` // default true when absent
	RateScale *float32  `yaml:"rate_scale,omitempty"`
	Tint      *RawColor `yaml:"tint,omitempty"`
	Tags      []string  `yaml:"tags,omitempty"`
}

// entityEmitterFromRaw converts one attachment, applying the defaults every
// absent knob means.
func entityEmitterFromRaw(r RawEntityEmitter) EntityEmitter {
	return EntityEmitter{
		Name:      r.Name,
		Effect:    r.Effect,
		Model:     r.Model,
		Bone:      r.Bone,
		Offset:    vec3FromRawPtr(r.Offset, mgl32.Vec3{}),
		Rotation:  vec3FromRawPtr(r.Rotation, mgl32.Vec3{}),
		Enabled:   BoolDefault(r.Enabled, true),
		Emitting:  BoolDefault(r.Emitting, true),
		RateScale: float32Default(r.RateScale, 1),
		Tint:      colorDefault(r.Tint),
		Tags:      NormalizeTags(r.Tags),
	}
}

// entityEmitterToRaw is the inverse: an attachment written out in full, for a
// definition file and for the instances that own theirs outright.
func entityEmitterToRaw(m *EntityEmitter) RawEntityEmitter {
	return RawEntityEmitter{
		Name:      m.Name,
		Effect:    m.Effect,
		Model:     m.Model,
		Bone:      m.Bone,
		Offset:    vec3ToRawPtr(m.Offset),
		Rotation:  vec3ToRawPtr(m.Rotation),
		Enabled:   BoolPtrIfFalse(m.Enabled),
		Emitting:  BoolPtrIfFalse(m.Emitting),
		RateScale: float32PtrIfNot(m.RateScale, 1),
		Tint:      colorPtrIfNotWhite(m.EffectiveTint()),
		Tags:      m.Tags,
	}
}

// entityEmittersFromRaw converts a whole list, naming anything that arrived
// without one — an unnamed attachment could be neither addressed nor
// overridden, and dropping it would lose an effect the author placed.
func entityEmittersFromRaw(raw []RawEntityEmitter, owner string) ([]EntityEmitter, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]EntityEmitter, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		m := entityEmitterFromRaw(r)
		if m.Name == "" {
			m.Name = fmt.Sprintf("emitter_%d", i+1)
		}
		if seen[m.Name] {
			return nil, fmt.Errorf("%s: duplicate emitter %q", owner, m.Name)
		}
		seen[m.Name] = true
		out = append(out, m)
	}
	return out, nil
}

// entityEmittersToRaw is entityEmittersFromRaw's inverse.
func entityEmittersToRaw(emitters []EntityEmitter) []RawEntityEmitter {
	if len(emitters) == 0 {
		return nil
	}
	out := make([]RawEntityEmitter, 0, len(emitters))
	for i := range emitters {
		out = append(out, entityEmitterToRaw(&emitters[i]))
	}
	return out
}

// applyEmitterOverride applies one instance-level override, touching only the
// fields the override actually mentions.
//
// Model and Bone follow Texture's rule rather than Offset's: a name is cleared
// by not having been set, not by an empty string, which means an instance
// cannot detach an effect from a bone the type hung it on. That is the same
// limitation every string override in RawEntityModel has and it costs the same
// thing — an instance that wants a different arrangement writes its own
// attachment under a different name.
func applyEmitterOverride(m *EntityEmitter, r RawEntityEmitter) {
	if r.Effect != "" {
		m.Effect = r.Effect
	}
	if r.Model != "" {
		m.Model = r.Model
	}
	if r.Bone != "" {
		m.Bone = r.Bone
	}
	if r.Offset != nil {
		m.Offset = mgl32.Vec3{r.Offset.X, r.Offset.Y, r.Offset.Z}
	}
	if r.Rotation != nil {
		m.Rotation = mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z}
	}
	if r.Enabled != nil {
		m.Enabled = *r.Enabled
	}
	if r.Emitting != nil {
		m.Emitting = *r.Emitting
	}
	if r.RateScale != nil {
		m.RateScale = *r.RateScale
	}
	if r.Tint != nil {
		m.Tint = mgl32.Vec3{r.Tint.R, r.Tint.G, r.Tint.B}
	}
	if len(r.Tags) > 0 {
		// Replaced rather than unioned with the type's, unlike an entity's own
		// tags: an entity's are what the thing IS and an instance only ever
		// adds to them, while an effect's are a classification the instance may
		// legitimately want to be the whole of.
		m.Tags = NormalizeTags(r.Tags)
	}
}

// emitterOverride returns the difference between one of an instance's effects
// and its definition's, and whether there was any.
//
// The counterpart of modelOverride, and it exists for the same reason: a level
// that restated its type's emitters would silently stop tracking them the
// moment the type changed.
func emitterOverride(def *EntityDef, m *EntityEmitter) (RawEntityEmitter, bool) {
	var base *EntityEmitter
	for i := range def.Emitters {
		if def.Emitters[i].Name == m.Name {
			base = &def.Emitters[i]
			break
		}
	}
	if base == nil {
		// An effect the definition does not have: one this instance added, or
		// one a definition since dropped. Nothing in the definition would
		// restore it, so it is written out in full.
		return entityEmitterToRaw(m), true
	}

	o := RawEntityEmitter{Name: m.Name}
	changed := false
	if m.Effect != base.Effect {
		o.Effect = m.Effect
		changed = true
	}
	if m.Model != base.Model {
		o.Model = m.Model
		changed = true
	}
	if m.Bone != base.Bone {
		o.Bone = m.Bone
		changed = true
	}
	if m.Offset != base.Offset {
		o.Offset = &RawVec3{X: m.Offset.X(), Y: m.Offset.Y(), Z: m.Offset.Z()}
		changed = true
	}
	if m.Rotation != base.Rotation {
		o.Rotation = &RawVec3{X: m.Rotation.X(), Y: m.Rotation.Y(), Z: m.Rotation.Z()}
		changed = true
	}
	if m.Enabled != base.Enabled {
		v := m.Enabled
		o.Enabled = &v
		changed = true
	}
	if m.Emitting != base.Emitting {
		v := m.Emitting
		o.Emitting = &v
		changed = true
	}
	if m.RateScale != base.RateScale {
		v := m.RateScale
		o.RateScale = &v
		changed = true
	}
	if m.EffectiveTint() != base.EffectiveTint() {
		t := m.EffectiveTint()
		o.Tint = &RawColor{R: t.X(), G: t.Y(), B: t.Z()}
		changed = true
	}
	if !stringsEqual(m.Tags, base.Tags) {
		o.Tags = m.Tags
		changed = true
	}
	return o, changed
}

// stringsEqual compares two tag lists element by element. Both are normalised
// on the way in, so equal sets are equal slices.
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------- materialising into a level ----------
//
// This lives in world, while its geometry twin lives in levelio
// (entity_geometry.go), and the reason is GPU work: materialising brushes
// uploads vertex buffers and resolves textures, which is the loader's half of
// the split. Materialising an effect uploads nothing — it reads a .fpar, which
// world already does for every placed emitter — so keeping it here lets the
// script runtime clean up after `destroy` without importing the loader.

// EntityEmitterInstances builds the live emitters an entity's attachments
// describe, ready to append to a level, and reports the textures their effects
// need.
//
// entityID may be empty, which is what a preview passes: the emitters are then
// unattached and unqualified, since there is no level for a name to collide in
// and no entity to ride.
//
// A document that will not load costs that emitter its particles, not its
// existence: the emitter is returned anyway and the error is reported. That is
// the rule the level loader already follows for a placed emitter, and for the
// same reason — the author needs a marker to fix rather than a silent absence.
func EntityEmitterInstances(entityID string, e *Entity) ([]Emitter, map[string]bool, error) {
	if e == nil || !e.HasEmitters() {
		return nil, nil, nil
	}
	out := make([]Emitter, 0, len(e.Emitters))
	tex := map[string]bool{}
	var firstErr error
	for i := range e.Emitters {
		em := EntityEmitterInstance(entityID, &e.Emitters[i])
		if err := em.Load(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("entity %q emitter %q: effect %q: %w",
				e.ID, e.Emitters[i].Name, e.Emitters[i].Effect, err)
		}
		if em.Doc != nil {
			for li := range em.Doc.Layers {
				tex[em.Doc.Layers[li].Texture] = true
			}
		}
		out = append(out, em)
	}
	return out, tex, firstErr
}

// EntityEmitterInstance converts one attachment into a level emitter, with no
// document loaded yet — the caller loads it, because that reads a file and this
// is also what a test wants to build without one.
func EntityEmitterInstance(entityID string, m *EntityEmitter) Emitter {
	em := Emitter{
		ID:         EntityEmitterID(entityID, m.Name),
		Effect:     m.Effect,
		Position:   m.Offset,
		Rotation:   m.Rotation,
		Enabled:    m.Enabled,
		Emitting:   m.Emitting,
		RateScale:  m.EffectiveRateScale(),
		Tint:       m.EffectiveTint(),
		FromEntity: entityID,
	}
	em.Tags = append([]string(nil), m.Tags...)
	if entityID != "" {
		// Model and Bone are only meaningful with an entity to resolve them
		// against, and Attach reads a bone with no entity as no attachment at
		// all — so they travel together or not at all.
		em.Attach = Attach{Entity: entityID, Model: m.Model, Bone: m.Bone}
	}
	return em
}

// MaterializeEntityEmitters builds e's attachments into level emitters and
// appends them to lvl, adding whatever textures they need to texNeeded.
//
// An effect that will not load costs the level that plume, not the level: the
// error is returned for the caller to report and the load carries on, exactly
// as MaterializeEntityGeometry does for a brush that will not build.
func MaterializeEntityEmitters(lvl *Level, e *Entity, texNeeded map[string]bool) error {
	if lvl == nil || e == nil || !e.HasEmitters() {
		return nil
	}
	emitters, tex, err := EntityEmitterInstances(e.ID, e)
	for t := range tex {
		if texNeeded != nil {
			texNeeded[t] = true
		}
	}
	for i := range emitters {
		// Qualified by the entity already, so this only fires when a level file
		// happens to author an emitter of the same name — the rule
		// uniqueChunkID follows for a materialised chunk.
		emitters[i].ID = UniqueEmitterID(lvl, emitters[i].ID)
		lvl.Emitters = append(lvl.Emitters, emitters[i])
	}
	return err
}

// DropEntityEmitters removes every emitter materialised from the named entity,
// and reports how many went.
//
// Called when the entity is deleted, renamed, or re-stamped from a definition
// whose effects have changed. Written as a sweep rather than a lookup because a
// rename leaves the old ID's emitters behind and this is what has to find them.
//
// Nothing is released: unlike a chunk an emitter holds no GL buffers, only its
// document and its live particles, both of which are ordinary memory.
func DropEntityEmitters(lvl *Level, entityID string) int {
	if lvl == nil || entityID == "" {
		return 0
	}
	kept := lvl.Emitters[:0]
	dropped := 0
	for i := range lvl.Emitters {
		if lvl.Emitters[i].FromEntity == entityID {
			dropped++
			continue
		}
		kept = append(kept, lvl.Emitters[i])
	}
	lvl.Emitters = kept
	return dropped
}
