package world

// entity_volume.go — the volumes an entity carries.
//
// A guard's field of view, the pickup radius around a crate, the "someone is
// standing on the pressure plate" region under a trap: regions that belong to a
// *thing* rather than to a level, and so have to go wherever it goes and exist
// wherever it is placed. Authoring one per placement, by hand, and attaching it
// every time, is exactly the chore a definition exists to remove.
//
// It is the fourth instance of one arrangement, and deliberately the same shape
// as entity_emitter.go: a definition holds templates with no runtime state, and
// a level materialises one live object per placement. Here the live object is a
// level Volume whose Attach names the entity — so `enter` and `exit`, `interact`,
// raycasts, the editor's cage and `@guard.sight` in a script all work with no
// further change, because a materialised volume is an ordinary one.
//
// # What a carried volume can be
//
// A trigger: a shape, a switch, and the tags, attributes and script every
// scriptable object has. Not a level exit, a doorway, fog or scatter. A level
// exit is a place in a level, a doorway belongs to a frame's chunks rather than
// to an entity, and fog and scatter are scenery that nobody has asked a type to
// carry. Each of those is still one checkbox away on a level's own volume.
//
// The one piece of scenery a type does carry is ClearsClouds, because the
// thing that asked for it was a type: a dropship's hold has to stay clear of
// the cloud bank it is parked in wherever the ship is placed and wherever it
// flies, and that is a region riding the ship or it is nothing.
//
// # Scripts
//
// The volume's script receives the volume's events, with `@self` the volume —
// the rule for every volume. Its `carrier` property is the entity it rides, so
// a type's sight-cone script reaches the guard it belongs to without having to
// know which placement it is running for.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
)

// EntityVolume is one region a type carries, in the entity's local space.
//
// The zero value is not a usable volume — Enabled wants to be on and a region
// needs extents — so construct one with NewEntityVolume or through
// entityVolumeFromRaw, both of which apply them.
type EntityVolume struct {
	// Name identifies the volume within the type: what an instance override
	// matches on, and what the materialised volume's level ID is built from.
	Name string

	// ScriptData is the tags, attributes and script the materialised volume
	// carries.
	ScriptData

	// Model and Bone name a socket inside one of the entity's model
	// attachments. Both empty — the ordinary case — hangs the volume off the
	// entity's own origin instead.
	Model string
	Bone  string

	// Offset is the centre in the entity's local space, or in the bone's when
	// one is named. Rotation is Euler XYZ in radians.
	Offset   mgl32.Vec3
	Rotation mgl32.Vec3

	Shape VolumeShape
	Half  mgl32.Vec3

	// Enabled is carried onto the placement. A type whose region only arms
	// when its script says so ships with it off.
	Enabled bool

	// ClearsClouds is carried onto the placement: see Volume.ClearsClouds.
	ClearsClouds bool
}

// NewEntityVolume returns a volume with the defaults a freshly added one should
// have: a one-unit box, armed.
func NewEntityVolume(name string) EntityVolume {
	return EntityVolume{
		Name:    name,
		Shape:   VolumeBox,
		Half:    mgl32.Vec3{1, 1, 1},
		Enabled: true,
	}
}

// EntityVolumeID is the level-unique ID one of an entity's volumes carries once
// materialised: the entity's ID and the volume's name, joined — the same rule
// EntityEmitterID follows, for the same reason.
func EntityVolumeID(entityID, name string) string {
	if entityID == "" {
		return name
	}
	return entityID + "." + name
}

// LocalMatrix is the volume's own transform, before whatever carries it.
func (m *EntityVolume) LocalMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(m.Offset.X(), m.Offset.Y(), m.Offset.Z())
	return T.Mul4(EulerMatrixYXZ(m.Rotation))
}

// ---------- what an entity's volumes are ----------

// HasVolumes reports whether the entity carries any volume at all.
func (e *Entity) HasVolumes() bool { return e != nil && len(e.Volumes) > 0 }

// EntityVolume looks up one of the entity's volume templates by name, or nil.
// Named for the template rather than as a bare `Volume` for the reason
// Entity.EntityEmitter is: the live one is in the level, under a different ID.
func (e *Entity) EntityVolume(name string) *EntityVolume {
	if e == nil {
		return nil
	}
	for i := range e.Volumes {
		if e.Volumes[i].Name == name {
			return &e.Volumes[i]
		}
	}
	return nil
}

// VolumeNamed returns the definition's volume with that name, or nil. The
// uniqueness test the entity editor's rename runs.
func (d *EntityDef) VolumeNamed(name string) *EntityVolume {
	if d == nil {
		return nil
	}
	for i := range d.Volumes {
		if d.Volumes[i].Name == name {
			return &d.Volumes[i]
		}
	}
	return nil
}

// UniqueEntityVolumeName returns base, or base with a numeric suffix no volume in
// the list is using. What the entity editor's "Add Volume" names a new one.
func UniqueEntityVolumeName(vols []EntityVolume, base string) string {
	if base == "" {
		base = "volume"
	}
	taken := func(name string) bool {
		for i := range vols {
			if vols[i].Name == name {
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

// CloneEntityVolumes deep-copies a volume list, so a definition and every
// instance stamped from it own their volumes outright — tagging one guard's
// sight cone must not tag every guard's.
func CloneEntityVolumes(vols []EntityVolume) []EntityVolume {
	if len(vols) == 0 {
		return nil
	}
	out := make([]EntityVolume, len(vols))
	copy(out, vols)
	for i := range out {
		out[i].ScriptData = CloneScriptData(vols[i].ScriptData)
	}
	return out
}

// ---------- serialization ----------

// RawEntityVolume is the YAML form, on a definition and as an instance override
// alike. Pointers for the fields whose absence has to mean "whatever the
// definition said", on RawEntityEmitter's rule.
type RawEntityVolume struct {
	Name          string `yaml:"name"`
	RawScriptData `yaml:",inline"`
	Model         string   `yaml:"model,omitempty"` // model attachment holding Bone
	Bone          string   `yaml:"bone,omitempty"`
	Shape         string   `yaml:"shape,omitempty"` // box (default) | sphere | cylinder
	Offset        *RawVec3 `yaml:"offset,omitempty"`
	Rotation      *RawVec3 `yaml:"rotation,omitempty"`
	Half          *RawVec3 `yaml:"half,omitempty"`    // default 1, 1, 1 when absent
	Enabled       *bool    `yaml:"enabled,omitempty"` // default true when absent
	// ClearsClouds is a pointer for Enabled's reason: an instance override has
	// to be able to say "false" to a type that said true.
	ClearsClouds *bool `yaml:"clears_clouds,omitempty"`
}

// entityVolumeFromRaw converts one volume, applying the defaults every absent key
// means.
func entityVolumeFromRaw(r RawEntityVolume, owner string) (EntityVolume, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, fmt.Sprintf("%s volume %q", owner, r.Name))
	if err != nil {
		return EntityVolume{}, err
	}
	return EntityVolume{
		Name:         r.Name,
		ScriptData:   data,
		Model:        r.Model,
		Bone:         r.Bone,
		Offset:       vec3FromRawPtr(r.Offset, mgl32.Vec3{}),
		Rotation:     vec3FromRawPtr(r.Rotation, mgl32.Vec3{}),
		Shape:        VolumeShapeFromName(r.Shape),
		Half:         vec3FromRawPtr(r.Half, mgl32.Vec3{1, 1, 1}),
		Enabled:      BoolDefault(r.Enabled, true),
		ClearsClouds: BoolDefault(r.ClearsClouds, false),
	}, nil
}

// entityVolumeToRaw is the inverse: a volume written out in full, with every
// default left unsaid.
func entityVolumeToRaw(m *EntityVolume) RawEntityVolume {
	r := RawEntityVolume{
		Name:          m.Name,
		RawScriptData: ScriptDataToRaw(m.ScriptData),
		Model:         m.Model,
		Bone:          m.Bone,
		Offset:        vec3ToRawPtr(m.Offset),
		Rotation:      vec3ToRawPtr(m.Rotation),
		Enabled:       BoolPtrIfFalse(m.Enabled),
	}
	if m.ClearsClouds {
		on := true
		r.ClearsClouds = &on
	}
	if m.Shape != VolumeBox {
		r.Shape = VolumeShapeName(m.Shape)
	}
	if m.Half != (mgl32.Vec3{1, 1, 1}) {
		r.Half = &RawVec3{X: m.Half.X(), Y: m.Half.Y(), Z: m.Half.Z()}
	}
	return r
}

// entityVolumesFromRaw converts a whole list, naming anything that arrived
// without one — an unnamed volume could be neither addressed nor overridden.
func entityVolumesFromRaw(raw []RawEntityVolume, owner string) ([]EntityVolume, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]EntityVolume, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		m, err := entityVolumeFromRaw(r, owner)
		if err != nil {
			return nil, err
		}
		if m.Name == "" {
			m.Name = fmt.Sprintf("volume_%d", i+1)
		}
		if seen[m.Name] {
			return nil, fmt.Errorf("%s: duplicate volume %q", owner, m.Name)
		}
		seen[m.Name] = true
		out = append(out, m)
	}
	return out, nil
}

// entityVolumesToRaw is entityVolumesFromRaw's inverse.
func entityVolumesToRaw(vols []EntityVolume) []RawEntityVolume {
	if len(vols) == 0 {
		return nil
	}
	out := make([]RawEntityVolume, 0, len(vols))
	for i := range vols {
		out = append(out, entityVolumeToRaw(&vols[i]))
	}
	return out
}

// applyVolumeOverride applies one instance-level override, touching only what
// the override mentions.
//
// Strings follow applyEmitterOverride's rule — set by being non-empty, so an
// instance cannot clear a bone or a script the type named — and the scripting
// half follows an entity's own: tags are unioned with the type's, and
// attributes and parameters overlaid one name at a time.
func applyVolumeOverride(m *EntityVolume, r RawEntityVolume, owner string) error {
	over, err := ScriptDataFromRaw(r.RawScriptData, fmt.Sprintf("%s volume %q", owner, r.Name))
	if err != nil {
		return err
	}
	if over.Script != "" {
		m.Script = over.Script
	}
	m.Tags = mergeTags(m.Tags, r.Tags)
	m.Attributes = mergeAttrs(m.Attributes, over.Attributes)
	m.ScriptParams = mergeAttrs(m.ScriptParams, over.ScriptParams)
	if r.Model != "" {
		m.Model = r.Model
	}
	if r.Bone != "" {
		m.Bone = r.Bone
	}
	if r.Shape != "" {
		m.Shape = VolumeShapeFromName(r.Shape)
	}
	if r.Offset != nil {
		m.Offset = mgl32.Vec3{r.Offset.X, r.Offset.Y, r.Offset.Z}
	}
	if r.Rotation != nil {
		m.Rotation = mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z}
	}
	if r.Half != nil {
		m.Half = mgl32.Vec3{r.Half.X, r.Half.Y, r.Half.Z}
	}
	if r.Enabled != nil {
		m.Enabled = *r.Enabled
	}
	if r.ClearsClouds != nil {
		m.ClearsClouds = *r.ClearsClouds
	}
	return nil
}

// volumeOverride returns the difference between one of an instance's volumes
// and its definition's, and whether there was any — emitterOverride's
// counterpart, so an untouched guard writes no `volumes:` block.
func volumeOverride(def *EntityDef, m *EntityVolume) (RawEntityVolume, bool) {
	base := def.VolumeNamed(m.Name)
	if base == nil {
		// One the instance added, or one the definition since dropped: nothing
		// in the definition would restore it, so it is written in full.
		return entityVolumeToRaw(m), true
	}

	o := RawEntityVolume{Name: m.Name}
	changed := false
	if m.Script != base.Script && m.Script != "" {
		o.Script = m.Script
		changed = true
	}
	if t := tagsBeyond(base.Tags, m.Tags); len(t) > 0 {
		o.Tags = t
		changed = true
	}
	if a := attrsBeyond(base.Attributes, m.Attributes); len(a) > 0 {
		o.Attributes = attrsToRaw(a)
		changed = true
	}
	if a := attrsBeyond(base.ScriptParams, m.ScriptParams); len(a) > 0 {
		o.ScriptParams = attrsToRaw(a)
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
	if m.Shape != base.Shape {
		o.Shape = VolumeShapeName(m.Shape)
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
	if m.Half != base.Half {
		o.Half = &RawVec3{X: m.Half.X(), Y: m.Half.Y(), Z: m.Half.Z()}
		changed = true
	}
	if m.Enabled != base.Enabled {
		v := m.Enabled
		o.Enabled = &v
		changed = true
	}
	if m.ClearsClouds != base.ClearsClouds {
		v := m.ClearsClouds
		o.ClearsClouds = &v
		changed = true
	}
	return o, changed
}

// ---------- materialising into a level ----------
//
// Beside entity_emitter.go's, and for its reason: nothing here uploads, so the
// script runtime can build and drop an entity's volumes around `spawn` and
// `destroy` without importing the loader. The one piece of GPU work a volume
// has — the editor's wireframe cage — is left to the callers that run on the GL
// thread, through LoadEntityVolumeCages.

// EntityVolumeInstance converts one template into a level volume riding the
// named entity. entityID may be empty, which is what a preview passes: the
// volume is then unattached and unqualified.
func EntityVolumeInstance(entityID string, m *EntityVolume) Volume {
	v := Volume{
		ID:           EntityVolumeID(entityID, m.Name),
		ScriptData:   CloneScriptData(m.ScriptData),
		Position:     m.Offset,
		Rotation:     m.Rotation,
		Shape:        m.Shape,
		HalfX:        m.Half.X(),
		HalfY:        m.Half.Y(),
		HalfZ:        m.Half.Z(),
		Enabled:      m.Enabled,
		ClearsClouds: m.ClearsClouds,
		FromEntity:   entityID,
	}
	if entityID != "" {
		// Model and Bone travel with the entity or not at all, as an emitter's
		// do — Attach reads a bone with no entity as no attachment.
		v.Attach = Attach{Entity: entityID, Model: m.Model, Bone: m.Bone}
	}
	return v
}

// MaterializeEntityVolumes builds e's volumes into level volumes riding it and
// appends them to lvl, reporting how many it added. No cage is uploaded; see
// LoadEntityVolumeCages.
func MaterializeEntityVolumes(lvl *Level, e *Entity) int {
	if lvl == nil || e == nil || !e.HasVolumes() {
		return 0
	}
	for i := range e.Volumes {
		v := EntityVolumeInstance(e.ID, &e.Volumes[i])
		// Qualified by the entity already, so this only fires when something
		// else in the level happens to have that exact name.
		v.ID = UniqueVolumeID(lvl, v.ID)
		lvl.Volumes = append(lvl.Volumes, v)
	}
	return len(e.Volumes)
}

// CarriedVolumeTemplates returns e's volume templates with the scripting half —
// script, tags, attributes and params — taken from the level volumes they
// materialised into, as those stand now.
//
// A placement's wiring is bound on the live volume: the level editor's panel
// writes a `ref` param onto ship.trigger, and only the placement can say which
// ship. The live volume is skipped on save, so whatever writes an entity out
// (a level save, an undo snapshot, a compile, a re-stamp) has to read the
// binding back through here, or it never reaches the instance's override.
//
// Matched by order, which is the order MaterializeEntityVolumes appended them
// in and which DropEntityVolumes keeps. A count that disagrees means the level
// is mid-change, and the templates come back untouched rather than paired wrong.
func CarriedVolumeTemplates(lvl *Level, e *Entity) []EntityVolume {
	if lvl == nil || e == nil || e.ID == "" || !e.HasVolumes() {
		if e == nil {
			return nil
		}
		return e.Volumes
	}
	live := make([]*Volume, 0, len(e.Volumes))
	for i := range lvl.Volumes {
		if lvl.Volumes[i].FromEntity == e.ID {
			live = append(live, &lvl.Volumes[i])
		}
	}
	if len(live) != len(e.Volumes) {
		return e.Volumes
	}
	out := CloneEntityVolumes(e.Volumes)
	for i := range out {
		out[i].ScriptData = CloneScriptData(live[i].ScriptData)
	}
	return out
}

// LoadEntityVolumeCages uploads the editor cage for every volume materialised
// from the named entity that does not have one yet. GL work, so only for
// callers on the GL thread; a failed cage is a missing authoring aid, and the
// first error is returned for reporting while the rest still load.
func LoadEntityVolumeCages(lvl *Level, entityID string) error {
	if lvl == nil || entityID == "" {
		return nil
	}
	var firstErr error
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.FromEntity != entityID || len(v.Geometry.Vertices) > 0 {
			continue
		}
		if err := v.Load(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("entity %q volume %q: marker load: %w", entityID, v.ID, err)
		}
	}
	return firstErr
}

// DropEntityVolumes removes every volume materialised from the named entity,
// releasing their cages, and reports how many went.
//
// A sweep rather than a lookup, for DropEntityEmitters' reason: a rename leaves
// the old ID's volumes behind, and this is what has to find them.
func DropEntityVolumes(lvl *Level, entityID string) int {
	if lvl == nil || entityID == "" {
		return 0
	}
	kept := lvl.Volumes[:0]
	dropped := 0
	for i := range lvl.Volumes {
		if lvl.Volumes[i].FromEntity == entityID {
			lvl.Volumes[i].Geometry.Delete()
			dropped++
			continue
		}
		kept = append(kept, lvl.Volumes[i])
	}
	lvl.Volumes = kept
	return dropped
}
