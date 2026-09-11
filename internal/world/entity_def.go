package world

// entity_def.go — entity definitions, and the YAML for both them and the
// instances a level places.
//
// An entity has two halves. What it *is* — its models, its bounds, its default
// attributes, the tags that classify it — lives in a definition file under
// resources/entities/. Where it *is* — position, rotation, velocity, and
// whatever this particular one overrides — lives in the level that places it.
//
// The split is what makes a type editable: giving every barrel in the game a
// second animation is one file, not a search across every level that ever
// placed one. It also keeps level files small, since a level says "a barrel,
// here" rather than restating the mesh, the rig and the bounding box each time.
//
// Overriding is per-field and shallow by design:
//
//	tags        union with the definition's — a definition's tags are what the
//	            thing is, and an instance adding one should not have to restate
//	            them
//	attributes  merged by name over the definition's, so an instance changes the
//	            one value it cares about
//	models      matched by attachment name; an override adjusts that
//	            attachment's mesh, texture, transform and visibility. An
//	            attachment the definition does not have is one the instance
//	            added, and is written out in full
//	brushes     replaced wholesale, or inherited untouched — see
//	            RawEntity.Brushes
//	emitters    matched by attachment name, exactly as models are; an override
//	            adjusts that effect's document, socket, transform and knobs
//
// A level may also place an entity with no type at all. That is what the
// editor's "blank entity" is: a thing authored where it stands, carrying its
// whole description in the level rather than referencing a definition. It is
// the quick path — place something, give it a mesh, move on — and a definition
// is what it becomes once a second level wants the same thing.
//
// Everything else on an instance (position, rotation, scale, velocity, angular
// velocity) is the instance's alone: a definition has no position, because a
// definition is not anywhere.

import (
	"fmt"
	"foundry/internal/fileutil"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// EntitiesDirName is the directory under resources/ that holds definition
// files, and entityDefExt the extension they carry.
const (
	EntitiesDirName = "entities"
	entityDefExt    = ".yaml"
)

func entityDefsDir() string { return filepath.Join(fileutil.ResourcesPath, EntitiesDirName) }

func entityDefPath(name string) string {
	return filepath.Join(entityDefsDir(), name+entityDefExt)
}

// ---------- YAML ----------

// RawEntityAnimation is the YAML form of one named animation on an attachment.
type RawEntityAnimation struct {
	Name      string   `yaml:"name"`
	Clip      string   `yaml:"clip,omitempty"`  // empty = same name as the animation
	Speed     float32  `yaml:"speed,omitempty"` // 0 = 1
	Loop      *bool    `yaml:"loop,omitempty"`  // nil = follow the clip's own flag
	BlendTime *float32 `yaml:"blend,omitempty"` // nil = DefaultAnimBlend, 0 = hard cut
	Autoplay  bool     `yaml:"autoplay,omitempty"`
}

// RawEntityModel is the YAML form of one model attachment.
//
// The vector fields are pointers so an *override* can distinguish "leave the
// definition's rotation alone" from "rotate this one to zero" — a plain
// RawVec3 makes those two identical. In a definition file, where there is
// nothing to leave alone, absent simply means the zero vector.
type RawEntityModel struct {
	Name       string               `yaml:"name"`
	Path       string               `yaml:"path,omitempty"`
	Texture    string               `yaml:"texture,omitempty"`
	Offset     *RawVec3             `yaml:"offset,omitempty"`
	Rotation   *RawVec3             `yaml:"rotation,omitempty"`
	Scale      *RawVec3             `yaml:"scale,omitempty"`
	Alpha      float32              `yaml:"alpha,omitempty"`   // 0 = opaque
	Visible    *bool                `yaml:"visible,omitempty"` // default true when absent
	Animations []RawEntityAnimation `yaml:"animations,omitempty"`
}

// RawEntityCollision is the YAML form of an entity's collision flags.
//
// Every field is a pointer so an instance can override one without restating
// the rest, and so "absent" is distinguishable from "false" — which matters for
// Movable, whose default is true.
type RawEntityCollision struct {
	World    *bool `yaml:"world,omitempty"`
	Entities *bool `yaml:"entities,omitempty"`
	Gravity  *bool `yaml:"gravity,omitempty"`
	Movable  *bool `yaml:"movable,omitempty"` // default true when absent
}

// entityCollisionFromRaw folds a YAML block onto a starting set, leaving any
// field the block does not mention alone. Called twice: once with the defaults
// for a definition, once with the definition's values for an instance.
func entityCollisionFromRaw(base EntityCollision, r *RawEntityCollision) EntityCollision {
	if r == nil {
		return base
	}
	if r.World != nil {
		base.World = *r.World
	}
	if r.Entities != nil {
		base.Entities = *r.Entities
	}
	if r.Gravity != nil {
		base.Gravity = *r.Gravity
	}
	if r.Movable != nil {
		base.Movable = *r.Movable
	}
	return base
}

// entityCollisionToRaw writes only what differs from base, and returns nil when
// nothing does — so an untouched instance grows no `collision:` key.
func entityCollisionToRaw(c, base EntityCollision) *RawEntityCollision {
	out := RawEntityCollision{}
	changed := false
	if c.World != base.World {
		v := c.World
		out.World = &v
		changed = true
	}
	if c.Entities != base.Entities {
		v := c.Entities
		out.Entities = &v
		changed = true
	}
	if c.Gravity != base.Gravity {
		v := c.Gravity
		out.Gravity = &v
		changed = true
	}
	if c.Movable != base.Movable {
		v := c.Movable
		out.Movable = &v
		changed = true
	}
	if !changed {
		return nil
	}
	return &out
}

// RawEntityDef is a definition file: resources/entities/<name>.yaml.
type RawEntityDef struct {
	ID                 string                  `yaml:"id,omitempty"` // defaults to the filename
	Tags               []string                `yaml:"tags,omitempty"`
	Bounding           string                  `yaml:"bounding,omitempty"` // aabb (default), sphere, cylinder
	BoundHalfX         float32                 `yaml:"bound_half_x,omitempty"`
	BoundHalfY         float32                 `yaml:"bound_half_y,omitempty"`
	BoundHalfZ         float32                 `yaml:"bound_half_z,omitempty"`
	BoundOffset        *RawVec3                `yaml:"bound_offset,omitempty"`
	Visible            *bool                   `yaml:"visible,omitempty"` // default true when absent
	Collision          *RawEntityCollision     `yaml:"collision,omitempty"`
	Models             []RawEntityModel        `yaml:"models,omitempty"`
	Brushes            []RawBrush              `yaml:"brushes,omitempty"`             // see entity_brush.go
	Emitters           []RawEntityEmitter      `yaml:"emitters,omitempty"`            // see entity_emitter.go
	Volumes            []RawEntityVolume       `yaml:"volumes,omitempty"`             // see entity_volume.go
	WaypointAnimations []RawWaypointAnimation  `yaml:"waypoint_animations,omitempty"` // see entity_waypoints.go
	Attributes         map[string]RawAttribute `yaml:"attributes,omitempty"`
	Script             string                  `yaml:"script,omitempty"`      // .flang file driving this type's behaviour
	Params             map[string]RawAttribute `yaml:"params,omitempty"`      // defaults for that script's `param` declarations
	Description        string                  `yaml:"description,omitempty"` // author's note; shown in the editor's picker
	Vehicle            *RawVehicle             `yaml:"vehicle,omitempty"`     // see vehicle_def.go
}

// RawEntity is the YAML form of one entity *instance* in a level file.
//
// Type names the definition this instance is stamped from, and is optional: an
// entity with no type is authored entirely in the level, and every field below
// is then its own rather than an override. With a type, only the fields that
// differ from the definition are written — see EntityToRaw.
type RawEntity struct {
	Type            string   `yaml:"type,omitempty"`
	ID              string   `yaml:"id"`
	Position        RawVec3  `yaml:"position"`
	Rotation        RawVec3  `yaml:"rotation,omitempty"`
	Scale           *RawVec3 `yaml:"scale,omitempty"`
	Velocity        RawVec3  `yaml:"velocity,omitempty"`
	AngularVelocity RawVec3  `yaml:"angular_velocity,omitempty"`
	// AttachedChunk is which chunk this instance rides — see
	// Entity.AttachedChunk. Instance-only: a definition cannot name a level's
	// chunk, since it does not know which level it will be placed in.
	AttachedChunk string                  `yaml:"attached_chunk,omitempty"`
	Tags          []string                `yaml:"tags,omitempty"`    // unioned with the definition's
	Visible       *bool                   `yaml:"visible,omitempty"` // nil = follow the definition
	Attributes    map[string]RawAttribute `yaml:"attributes,omitempty"`
	Params        map[string]RawAttribute `yaml:"params,omitempty"`    // script params, overlaid on the definition's
	Collision     *RawEntityCollision     `yaml:"collision,omitempty"` // per-flag overrides
	Models        []RawEntityModel        `yaml:"models,omitempty"`    // per-attachment overrides, matched by name
	// Brushes is this instance's own geometry. Written whole or not at all, the
	// way the vehicle block is and for the same reason: a per-field diff would
	// have to invent a way to say "this brush was removed", and an instance
	// that has diverged from its type's shape at all has usually diverged
	// wholesale.
	Brushes []RawBrush `yaml:"brushes,omitempty"`
	// Emitters are per-attachment overrides, matched by name exactly as Models
	// are. Per-name rather than wholesale like Brushes, because an effect IS a
	// named attachment with a handful of knobs — "this one ship's plume is
	// twice the rate" is the ordinary edit, and it should not drag the other
	// five emitters into the level file with it.
	Emitters []RawEntityEmitter `yaml:"emitters,omitempty"`
	// Volumes are per-volume overrides, matched by name, on the emitters' rule.
	Volumes []RawEntityVolume `yaml:"volumes,omitempty"`
	// WaypointAnimations are written whole or not at all, on the brushes' rule —
	// and in practice only by a typeless instance, since a type's paths have no
	// per-instance override (entity_waypoints.go).
	WaypointAnimations []RawWaypointAnimation `yaml:"waypoint_animations,omitempty"`
	// Script overrides the definition's. A pointer so the three cases stay
	// distinct: absent follows the type, "" detaches the script this instance
	// would otherwise have inherited, and a name replaces it.
	Script *string `yaml:"script,omitempty"`

	// Vehicle overlays the definition's vehicle block — seats matched by name,
	// drive tuning merged field by field. See MergeVehicle.
	Vehicle *RawVehicle `yaml:"vehicle,omitempty"`

	// Bounds overrides. Pointers throughout, so an absent field means "follow
	// the definition" rather than "zero" — an extent of zero is a *disabled*
	// volume, which is a thing an author might legitimately want and which a
	// plain float could not distinguish from silence.
	Bounding    string   `yaml:"bounding,omitempty"`
	BoundHalfX  *float32 `yaml:"bound_half_x,omitempty"`
	BoundHalfY  *float32 `yaml:"bound_half_y,omitempty"`
	BoundHalfZ  *float32 `yaml:"bound_half_z,omitempty"`
	BoundOffset *RawVec3 `yaml:"bound_offset,omitempty"`
}

// ---------- parsed definition ----------

// EntityDef is a parsed definition: the template every instance of a type is
// stamped from.
type EntityDef struct {
	Name        string
	Description string
	Tags        []string
	// Script is the .flang file driving this type's behaviour, relative to the
	// scripts root. An instance may override it — see RawEntity.Script.
	Script string

	BoundingKind BoundingType
	BoundHalfX   float32
	BoundHalfY   float32
	BoundHalfZ   float32
	BoundOffset  mgl32.Vec3

	Visible bool
	// Collision is what instances of this type take part in physically.
	Collision EntityCollision
	// Models are attachment templates. They hold no GPU state — Instantiate
	// copies them and the copy is what gets loaded — so one definition can back
	// any number of placed entities.
	Models []EntityModel
	// Brushes are the type's own solid geometry, in its local space. Like
	// Models they hold no GPU state — the level materialises one frame-owned
	// chunk per instance from them (levelio/entity_geometry.go) — so one
	// definition can back any number of placed ships.
	Brushes []Brush
	// Emitters are the particle effects the type carries, in its local space
	// (entity_emitter.go). Templates like Models and Brushes: the level
	// materialises one live Emitter per placement from each of them.
	Emitters []EntityEmitter
	// Volumes are the regions the type carries, in its local space
	// (entity_volume.go). Templates again: one live Volume per placement.
	Volumes []EntityVolume
	// WaypointAnimations are the paths the type carries, written in its local
	// space (entity_waypoints.go): one live level animation per placement.
	WaypointAnimations []WaypointAnimation
	Attributes         AttrSet
	// Params are the type's defaults for its script's `param` declarations. An
	// instance overrides them one name at a time.
	Params AttrSet

	// Vehicle is the authored vehicle block, nil for anything that is not one.
	// Shared with every instance rather than copied: it is authored data that
	// nothing mutates, and an instance override replaces the pointer wholesale.
	Vehicle *VehicleSpec
}

// vec3FromRawPtr reads an optional vector, falling back to def when absent.
func vec3FromRawPtr(p *RawVec3, def mgl32.Vec3) mgl32.Vec3 {
	if p == nil {
		return def
	}
	return mgl32.Vec3{p.X, p.Y, p.Z}
}

// vec3ToRawPtr returns the YAML form of a vector, or nil when it is the zero
// vector so the key is omitted rather than written as three zeros.
func vec3ToRawPtr(v mgl32.Vec3) *RawVec3 {
	if v == (mgl32.Vec3{}) {
		return nil
	}
	return &RawVec3{X: v.X(), Y: v.Y(), Z: v.Z()}
}

// entityModelFromRaw converts one attachment. Used for definition attachments,
// where every absent field means its zero value.
func entityModelFromRaw(r RawEntityModel) EntityModel {
	return EntityModel{
		Name:       r.Name,
		Path:       r.Path,
		Texture:    r.Texture,
		Offset:     vec3FromRawPtr(r.Offset, mgl32.Vec3{}),
		Rotation:   vec3FromRawPtr(r.Rotation, mgl32.Vec3{}),
		Scale:      vec3FromRawPtr(r.Scale, mgl32.Vec3{}),
		Alpha:      r.Alpha,
		Visible:    BoolDefault(r.Visible, true),
		Animations: entityAnimationsFromRaw(r.Animations),
	}
}

// entityAnimationsFromRaw converts an attachment's animation list, dropping
// unnamed entries — an animation with no name cannot be played, since the name
// is the only way to ask for it.
func entityAnimationsFromRaw(raw []RawEntityAnimation) []EntityAnimation {
	if len(raw) == 0 {
		return nil
	}
	out := make([]EntityAnimation, 0, len(raw))
	for _, r := range raw {
		if r.Name == "" {
			continue
		}
		out = append(out, EntityAnimation{
			Name:      r.Name,
			Clip:      r.Clip,
			Speed:     r.Speed,
			Loop:      r.Loop,
			BlendTime: r.BlendTime,
			Autoplay:  r.Autoplay,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// entityModelToRaw is entityModelFromRaw's inverse: an attachment written out
// in full, for a definition file and for the instances that own their
// attachments outright rather than overriding a definition's.
func entityModelToRaw(m *EntityModel) RawEntityModel {
	return RawEntityModel{
		Name:       m.Name,
		Path:       fileutil.RelAssetPath(m.Path),
		Texture:    m.Texture,
		Offset:     vec3ToRawPtr(m.Offset),
		Rotation:   vec3ToRawPtr(m.Rotation),
		Scale:      vec3ToRawPtr(m.Scale),
		Alpha:      m.Alpha,
		Visible:    BoolPtrIfFalse(m.Visible),
		Animations: entityAnimationsToRaw(m.Animations),
	}
}

// entityAnimationsToRaw is entityAnimationsFromRaw's inverse.
func entityAnimationsToRaw(anims []EntityAnimation) []RawEntityAnimation {
	if len(anims) == 0 {
		return nil
	}
	out := make([]RawEntityAnimation, 0, len(anims))
	for _, a := range anims {
		out = append(out, RawEntityAnimation{
			Name:      a.Name,
			Clip:      a.Clip,
			Speed:     a.Speed,
			Loop:      a.Loop,
			BlendTime: a.BlendTime,
			Autoplay:  a.Autoplay,
		})
	}
	return out
}

// entityDefFromRaw converts a parsed definition file. name is the fallback ID,
// taken from the filename when the file does not give one.
func entityDefFromRaw(raw RawEntityDef, name string) (*EntityDef, error) {
	id := raw.ID
	if id == "" {
		id = name
	}
	if id == "" {
		return nil, fmt.Errorf("entity definition has no id and no filename to take one from")
	}

	attrs, err := attrsFromRaw(raw.Attributes)
	if err != nil {
		return nil, fmt.Errorf("entity definition %q: %w", id, err)
	}
	params, err := attrsFromRaw(raw.Params)
	if err != nil {
		return nil, fmt.Errorf("entity definition %q: %w", id, err)
	}

	def := &EntityDef{
		Name:         id,
		Description:  raw.Description,
		Script:       raw.Script,
		Tags:         NormalizeTags(raw.Tags),
		BoundingKind: BoundingKindFromName(raw.Bounding),
		BoundHalfX:   raw.BoundHalfX,
		BoundHalfY:   raw.BoundHalfY,
		BoundHalfZ:   raw.BoundHalfZ,
		BoundOffset:  vec3FromRawPtr(raw.BoundOffset, mgl32.Vec3{}),
		Visible:      BoolDefault(raw.Visible, true),
		Collision:    entityCollisionFromRaw(DefaultEntityCollision(), raw.Collision),
		Attributes:   attrs,
		Params:       params,
		Vehicle:      VehicleFromRaw(raw.Vehicle),
	}

	seen := map[string]bool{}
	for i, rm := range raw.Models {
		m := entityModelFromRaw(rm)
		if m.Name == "" {
			// An unnamed attachment cannot be addressed or overridden, so give
			// it a positional name rather than refusing the whole definition.
			m.Name = fmt.Sprintf("model_%d", i+1)
		}
		if seen[m.Name] {
			return nil, fmt.Errorf("entity definition %q: duplicate model name %q", id, m.Name)
		}
		seen[m.Name] = true
		def.Models = append(def.Models, m)
	}

	brushes, err := entityBrushesFromRaw(raw.Brushes, fmt.Sprintf("entity definition %q", id))
	if err != nil {
		return nil, err
	}
	def.Brushes = brushes

	emitters, err := entityEmittersFromRaw(raw.Emitters, fmt.Sprintf("entity definition %q", id))
	if err != nil {
		return nil, err
	}
	def.Emitters = emitters

	volumes, err := entityVolumesFromRaw(raw.Volumes, fmt.Sprintf("entity definition %q", id))
	if err != nil {
		return nil, err
	}
	def.Volumes = volumes

	anims, err := entityWaypointAnimationsFromRaw(raw.WaypointAnimations, fmt.Sprintf("entity definition %q", id))
	if err != nil {
		return nil, err
	}
	def.WaypointAnimations = anims
	return def, nil
}

// entityDefToRaw is entityDefFromRaw's inverse, for the editor's definition
// writer.
func entityDefToRaw(def *EntityDef) RawEntityDef {
	raw := RawEntityDef{
		ID:          def.Name,
		Description: def.Description,
		Script:      def.Script,
		Tags:        def.Tags,
		Bounding:    BoundingTypeName(def.BoundingKind),
		BoundHalfX:  def.BoundHalfX,
		BoundHalfY:  def.BoundHalfY,
		BoundHalfZ:  def.BoundHalfZ,
		BoundOffset: vec3ToRawPtr(def.BoundOffset),
		Visible:     BoolPtrIfFalse(def.Visible),
		Collision:   entityCollisionToRaw(def.Collision, DefaultEntityCollision()),
		Attributes:  attrsToRaw(def.Attributes),
		Params:      attrsToRaw(def.Params),
		Vehicle:     VehicleToRaw(def.Vehicle),
	}
	for i := range def.Models {
		raw.Models = append(raw.Models, entityModelToRaw(&def.Models[i]))
	}
	raw.Brushes = entityBrushesToRaw(def.Brushes)
	raw.Emitters = entityEmittersToRaw(def.Emitters)
	raw.Volumes = entityVolumesToRaw(def.Volumes)
	raw.WaypointAnimations = entityWaypointAnimationsToRaw(def.WaypointAnimations)
	return raw
}

// ---------- instantiation ----------

// Instantiate stamps out a runtime entity from the definition. The result
// shares nothing with the definition, so an entity edited in the level editor
// cannot rewrite the type every other level places.
//
// The returned entity has no GPU state yet — call Load once a GL context is
// current.
func (d *EntityDef) Instantiate(id string) Entity {
	e := Entity{
		ID:           id,
		Type:         d.Name,
		Tags:         append([]string(nil), d.Tags...),
		Scale:        mgl32.Vec3{1, 1, 1},
		BoundingKind: d.BoundingKind,
		BoundHalfX:   d.BoundHalfX,
		BoundHalfY:   d.BoundHalfY,
		BoundHalfZ:   d.BoundHalfZ,
		BoundOffset:  d.BoundOffset,
		Visible:      d.Visible,
		Collision:    d.Collision,
		Script:       d.Script,
		Attributes:   d.Attributes.Clone(),
		ScriptParams: d.Params.Clone(),
		Vehicle:      d.Vehicle,
		Brushes:      CloneEntityBrushes(d.Brushes),
		Emitters:     CloneEntityEmitters(d.Emitters),
		Volumes:      CloneEntityVolumes(d.Volumes),

		WaypointAnimations: CloneWaypointAnimations(d.WaypointAnimations),
	}
	for i := range d.Models {
		e.Models = append(e.Models, d.Models[i].TemplateCopy())
	}
	return e
}

// templateCopy returns an attachment with its authored fields copied and its
// runtime fields left empty — the slices deep-copied so an instance's animation
// list is its own.
func (m *EntityModel) TemplateCopy() EntityModel {
	c := EntityModel{
		Name:     m.Name,
		Path:     m.Path,
		Texture:  m.Texture,
		Offset:   m.Offset,
		Rotation: m.Rotation,
		Scale:    m.Scale,
		Alpha:    m.Alpha,
		Visible:  m.Visible,
	}
	if len(m.Animations) > 0 {
		c.Animations = make([]EntityAnimation, len(m.Animations))
		copy(c.Animations, m.Animations)
	}
	return c
}

// applyModelOverride applies one instance-level attachment override. Only the
// fields the override actually mentions are touched — see RawEntityModel's note
// on why the vectors are pointers.
//
// Path is overridable like everything else. It used to not be, on the grounds
// that a different mesh is a different type; in practice the first thing an
// author does with a placed thing is decide what it looks like, and making that
// a trip through a definition file cost more than the purity was worth. An
// instance that swaps its mesh is one the level writes the path for, and the
// definition it came from is unchanged for everyone else.
func applyModelOverride(m *EntityModel, r RawEntityModel) {
	if r.Path != "" {
		m.Path = r.Path
	}
	if r.Texture != "" {
		m.Texture = r.Texture
	}
	if r.Offset != nil {
		m.Offset = mgl32.Vec3{r.Offset.X, r.Offset.Y, r.Offset.Z}
	}
	if r.Rotation != nil {
		m.Rotation = mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z}
	}
	if r.Scale != nil {
		m.Scale = mgl32.Vec3{r.Scale.X, r.Scale.Y, r.Scale.Z}
	}
	if r.Alpha != 0 {
		m.Alpha = r.Alpha
	}
	if r.Visible != nil {
		m.Visible = *r.Visible
	}
	if len(r.Animations) > 0 {
		// Animations are replaced wholesale rather than merged: the list is one
		// coherent naming of a model's motion, and half of one plus half of
		// another is not a thing an author ever means.
		m.Animations = entityAnimationsFromRaw(r.Animations)
	}
}

// EntityFromRaw builds a runtime entity from a level instance, resolving its
// type through lookup. No GPU state is created — the caller loads it.
//
// lookup is passed in rather than reached for so tests can supply definitions
// without touching the filesystem, and so the level loader can resolve against
// a preloaded set.
func EntityFromRaw(r RawEntity, lookup func(string) (*EntityDef, error)) (Entity, error) {
	id := r.ID
	if id == "" {
		// An entity with no ID cannot be addressed by a script, but it is still
		// a perfectly good piece of scenery — name it after its type rather
		// than refusing the level. The loader de-duplicates afterwards.
		id = r.Type
		if id == "" {
			id = BlankEntityID
		}
	}

	// No type is not an error: the entity is authored in the level, and every
	// field below is its own rather than an override of a definition's.
	var def *EntityDef
	if r.Type != "" {
		d, err := lookup(r.Type)
		if err != nil {
			return Entity{}, fmt.Errorf("entity %q: %w", id, err)
		}
		def = d
	}

	e := NewBlankEntity(id)
	if def != nil {
		e = def.Instantiate(id)
	}
	e.Type = r.Type

	e.Position = mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z}
	e.Rotation = mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z}
	e.Scale = vec3FromRawPtr(r.Scale, mgl32.Vec3{1, 1, 1})
	e.Velocity = mgl32.Vec3{r.Velocity.X, r.Velocity.Y, r.Velocity.Z}
	e.AngularVelocity = mgl32.Vec3{r.AngularVelocity.X, r.AngularVelocity.Y, r.AngularVelocity.Z}
	e.AttachedChunk = r.AttachedChunk
	e.Tags = mergeTags(e.Tags, r.Tags)
	if r.Visible != nil {
		e.Visible = *r.Visible
	}
	// A nil Script means "whatever the definition said"; a non-nil one replaces
	// it, including with "" to detach the type's script from this instance.
	if r.Script != nil {
		e.Script = *r.Script
	}

	e.Collision = entityCollisionFromRaw(e.Collision, r.Collision)
	e.Vehicle = MergeVehicle(e.Vehicle, r.Vehicle)

	if r.Bounding != "" {
		e.BoundingKind = BoundingKindFromName(r.Bounding)
	}
	if r.BoundHalfX != nil {
		e.BoundHalfX = *r.BoundHalfX
	}
	if r.BoundHalfY != nil {
		e.BoundHalfY = *r.BoundHalfY
	}
	if r.BoundHalfZ != nil {
		e.BoundHalfZ = *r.BoundHalfZ
	}
	if r.BoundOffset != nil {
		e.BoundOffset = mgl32.Vec3{r.BoundOffset.X, r.BoundOffset.Y, r.BoundOffset.Z}
	}

	over, err := attrsFromRaw(r.Attributes)
	if err != nil {
		return Entity{}, fmt.Errorf("entity %q: %w", id, err)
	}
	e.Attributes = mergeAttrs(e.Attributes, over)

	paramOver, err := attrsFromRaw(r.Params)
	if err != nil {
		return Entity{}, fmt.Errorf("entity %q: %w", id, err)
	}
	e.ScriptParams = mergeAttrs(e.ScriptParams, paramOver)

	for i, rm := range r.Models {
		if m := e.Model(rm.Name); m != nil && rm.Name != "" {
			applyModelOverride(m, rm)
			continue
		}
		// An attachment the definition has no counterpart for. With a mesh it
		// is one the instance added — every attachment on a typeless entity
		// arrives this way — so it is appended rather than refused. Without
		// one there is nothing to attach and nothing to draw, so the entry is
		// an override of something that is not there and the level says so.
		if rm.Path == "" {
			return Entity{}, fmt.Errorf("entity %q: override for model %q, which type %q does not have",
				id, rm.Name, r.Type)
		}
		m := entityModelFromRaw(rm)
		if m.Name == "" {
			m.Name = fmt.Sprintf("model_%d", i+1)
		}
		e.Models = append(e.Models, m)
	}

	// Brushes replace the definition's wholesale rather than merging per ID —
	// see RawEntity.Brushes. An instance that mentions none keeps the type's,
	// which is the case that matters: a level places a shuttle and gets the
	// shuttle's hull without restating it.
	if len(r.Brushes) > 0 {
		brushes, err := entityBrushesFromRaw(r.Brushes, fmt.Sprintf("entity %q", id))
		if err != nil {
			return Entity{}, err
		}
		e.Brushes = brushes
	}

	// Emitters override by name, the way models do. An entry naming an effect
	// the type does not have is one the instance added — every emitter on a
	// typeless entity arrives this way — and an entry naming nothing at all is
	// an override of something that is not there, which the level says.
	for i, re := range r.Emitters {
		if m := e.EntityEmitter(re.Name); m != nil && re.Name != "" {
			applyEmitterOverride(m, re)
			continue
		}
		m := entityEmitterFromRaw(re)
		if m.Name == "" {
			m.Name = fmt.Sprintf("emitter_%d", i+1)
		}
		if e.EntityEmitter(m.Name) != nil {
			return Entity{}, fmt.Errorf("entity %q: duplicate emitter %q", id, m.Name)
		}
		e.Emitters = append(e.Emitters, m)
	}

	// Volumes override by name as well, under the same two rules.
	owner := fmt.Sprintf("entity %q", id)
	for i, rv := range r.Volumes {
		if m := e.EntityVolume(rv.Name); m != nil && rv.Name != "" {
			if err := applyVolumeOverride(m, rv, owner); err != nil {
				return Entity{}, err
			}
			continue
		}
		m, err := entityVolumeFromRaw(rv, owner)
		if err != nil {
			return Entity{}, err
		}
		if m.Name == "" {
			m.Name = fmt.Sprintf("volume_%d", i+1)
		}
		if e.EntityVolume(m.Name) != nil {
			return Entity{}, fmt.Errorf("entity %q: duplicate volume %q", id, m.Name)
		}
		e.Volumes = append(e.Volumes, m)
	}

	// Paths replace the type's wholesale — see RawEntity.WaypointAnimations.
	if len(r.WaypointAnimations) > 0 {
		anims, err := entityWaypointAnimationsFromRaw(r.WaypointAnimations, owner)
		if err != nil {
			return Entity{}, err
		}
		e.WaypointAnimations = anims
	}
	return e, nil
}

// RestampEntity rebuilds a placed entity from a changed definition, keeping what
// the placement made its own.
//
// prev is the definition the entity was stamped from, next the one to stamp it
// from now. The instance's difference from prev — its overrides, exactly what a
// level file stores for it — is laid over next, so a field the instance left
// alone follows the new definition and one it changed keeps its value: a type
// that switches script takes every placement with it, and the parameters a
// placement bound for that script stay bound. It is the level's save-and-reload
// round trip done in memory, which is what makes an edited type land on an open
// level the same way it would on one reopened from disk.
//
// The fields a level file does not store but a live placement has — the frame
// and seat it occupies, the prefab that put it there — are carried across as
// they were. A nil prev has no difference to take, so only the placement is
// kept.
//
// An error means the overrides no longer fit next — one names a model the type
// has since dropped, say — and the caller should leave the entity as it was
// rather than discard what it carried.
func RestampEntity(e *Entity, prev, next *EntityDef) (Entity, error) {
	if e == nil || next == nil {
		return Entity{}, fmt.Errorf("restamp: no entity or no definition")
	}
	var fresh Entity
	if prev == nil {
		fresh = next.Instantiate(e.ID)
		fresh.Type = e.Type
		fresh.Position, fresh.Rotation, fresh.Scale = e.Position, e.Rotation, e.Scale
		fresh.Velocity, fresh.AngularVelocity = e.Velocity, e.AngularVelocity
		fresh.AttachedChunk = e.AttachedChunk
	} else {
		var err error
		fresh, err = EntityFromRaw(EntityToRaw(e, prev), func(string) (*EntityDef, error) { return next, nil })
		if err != nil {
			return Entity{}, err
		}
	}
	fresh.Frame, fresh.Seat, fresh.FromPrefab = e.Frame, e.Seat, e.FromPrefab
	return fresh, nil
}

// EntityToRaw is EntityFromRaw's inverse: the instance's own state, plus only
// those fields that differ from its definition.
//
// Writing the difference rather than everything is what keeps the definition
// authoritative. A level that restated its definition's bounds would silently
// stop tracking them the moment the definition changed, which is exactly the
// coupling definitions exist to remove.
func EntityToRaw(e *Entity, def *EntityDef) RawEntity {
	r := RawEntity{
		Type:            e.Type,
		ID:              e.ID,
		Position:        RawVec3{X: e.Position.X(), Y: e.Position.Y(), Z: e.Position.Z()},
		Rotation:        RawVec3{X: e.Rotation.X(), Y: e.Rotation.Y(), Z: e.Rotation.Z()},
		Velocity:        RawVec3{X: e.Velocity.X(), Y: e.Velocity.Y(), Z: e.Velocity.Z()},
		AngularVelocity: RawVec3{X: e.AngularVelocity.X(), Y: e.AngularVelocity.Y(), Z: e.AngularVelocity.Z()},
		AttachedChunk:   e.AttachedChunk,
	}
	if s := e.Scale; s != (mgl32.Vec3{1, 1, 1}) && s != (mgl32.Vec3{}) {
		r.Scale = &RawVec3{X: s.X(), Y: s.Y(), Z: s.Z()}
	}

	if def == nil {
		// No definition in hand (it failed to load, or the caller had none):
		// write everything the instance holds rather than silently dropping the
		// parts that would otherwise have been inherited.
		r.Tags = e.Tags
		r.Visible = BoolPtrIfFalse(e.Visible)
		r.Attributes = attrsToRaw(e.Attributes)
		r.Params = attrsToRaw(e.ScriptParams)
		r.Bounding = BoundingTypeName(e.BoundingKind)
		r.BoundHalfX, r.BoundHalfY, r.BoundHalfZ = &e.BoundHalfX, &e.BoundHalfY, &e.BoundHalfZ
		r.BoundOffset = vec3ToRawPtr(e.BoundOffset)
		r.Collision = entityCollisionToRaw(e.Collision, DefaultEntityCollision())
		if e.Script != "" {
			s := e.Script
			r.Script = &s
		}
		r.Vehicle = VehicleToRaw(e.Vehicle)
		for i := range e.Models {
			r.Models = append(r.Models, entityModelToRaw(&e.Models[i]))
		}
		r.Brushes = entityBrushesToRaw(e.Brushes)
		r.Emitters = entityEmittersToRaw(e.Emitters)
		r.Volumes = entityVolumesToRaw(e.Volumes)
		r.WaypointAnimations = entityWaypointAnimationsToRaw(e.WaypointAnimations)
		return r
	}

	// Only write the script when it differs from the type's, so an instance that
	// never overrode one keeps tracking the definition.
	if e.Script != def.Script {
		s := e.Script
		r.Script = &s
	}

	r.Tags = tagsBeyond(def.Tags, e.Tags)
	if e.Visible != def.Visible {
		v := e.Visible
		r.Visible = &v
	}
	r.Collision = entityCollisionToRaw(e.Collision, def.Collision)
	writeBoundsOverride(&r, e, def)
	r.Attributes = attrsToRaw(attrsBeyond(def.Attributes, e.Attributes))
	r.Params = attrsToRaw(attrsBeyond(def.Params, e.ScriptParams))
	// The vehicle block is written whole or not at all. A per-field diff would
	// have to invent a way to say "this seat was removed", and a seating plan is
	// small enough that restating it costs less than that machinery would.
	if !sameVehicle(def.Vehicle, e.Vehicle) {
		r.Vehicle = VehicleToRaw(e.Vehicle)
	}
	for i := range e.Models {
		if o, ok := modelOverride(def, &e.Models[i]); ok {
			r.Models = append(r.Models, o)
		}
	}
	// Whole list or nothing, exactly as the vehicle block above.
	if !EntityBrushesEqual(def.Brushes, e.Brushes) {
		r.Brushes = entityBrushesToRaw(e.Brushes)
	}
	for i := range e.Emitters {
		if o, ok := emitterOverride(def, &e.Emitters[i]); ok {
			r.Emitters = append(r.Emitters, o)
		}
	}
	for i := range e.Volumes {
		if o, ok := volumeOverride(def, &e.Volumes[i]); ok {
			r.Volumes = append(r.Volumes, o)
		}
	}
	// Whole list or nothing, as the brushes above.
	if !WaypointAnimationsEqual(def.WaypointAnimations, e.WaypointAnimations) {
		r.WaypointAnimations = entityWaypointAnimationsToRaw(e.WaypointAnimations)
	}
	return r
}

// writeBoundsOverride records only the bounds fields that differ from the
// definition's, each on its own — an entity whose box was widened on one axis
// should not have the other two frozen into the level with it.
func writeBoundsOverride(r *RawEntity, e *Entity, def *EntityDef) {
	if e.BoundingKind != def.BoundingKind {
		r.Bounding = BoundingTypeName(e.BoundingKind)
	}
	if e.BoundHalfX != def.BoundHalfX {
		v := e.BoundHalfX
		r.BoundHalfX = &v
	}
	if e.BoundHalfY != def.BoundHalfY {
		v := e.BoundHalfY
		r.BoundHalfY = &v
	}
	if e.BoundHalfZ != def.BoundHalfZ {
		v := e.BoundHalfZ
		r.BoundHalfZ = &v
	}
	if e.BoundOffset != def.BoundOffset {
		r.BoundOffset = &RawVec3{
			X: e.BoundOffset.X(), Y: e.BoundOffset.Y(), Z: e.BoundOffset.Z(),
		}
	}
}

// tagsBeyond returns the tags an entity carries that its definition does not.
func tagsBeyond(defTags, entityTags []string) []string {
	if len(entityTags) == 0 {
		return nil
	}
	has := make(map[string]bool, len(defTags))
	for _, t := range defTags {
		has[t] = true
	}
	var out []string
	for _, t := range entityTags {
		if !has[t] {
			out = append(out, t)
		}
	}
	return out
}

// attrsBeyond returns the attributes whose value differs from the definition's,
// plus any the definition does not have at all.
func attrsBeyond(defAttrs, entityAttrs AttrSet) AttrSet {
	if len(entityAttrs) == 0 {
		return nil
	}
	var out AttrSet
	for _, name := range entityAttrs.AttrNames() {
		v := entityAttrs[name]
		if d, ok := defAttrs[name]; ok && d == v {
			continue
		}
		if out == nil {
			out = AttrSet{}
		}
		out[name] = v
	}
	return out
}

// modelOverride returns the YAML override for one attachment, and whether it
// differs from the definition at all. An attachment matching its definition
// contributes nothing, so an untouched entity writes no `models:` block.
func modelOverride(def *EntityDef, m *EntityModel) (RawEntityModel, bool) {
	var base *EntityModel
	for i := range def.Models {
		if def.Models[i].Name == m.Name {
			base = &def.Models[i]
			break
		}
	}
	if base == nil {
		// An attachment the definition does not have: one this instance added,
		// or one a definition since dropped. Either way nothing in the
		// definition would restore it, so it is written out in full and loads
		// back as an attachment of its own.
		return entityModelToRaw(m), true
	}

	o := RawEntityModel{Name: m.Name}
	changed := false
	if m.Path != base.Path {
		o.Path = fileutil.RelAssetPath(m.Path)
		changed = true
	}
	if m.Texture != base.Texture {
		o.Texture = m.Texture
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
	if m.Scale != base.Scale {
		s := m.EffectiveScale()
		o.Scale = &RawVec3{X: s.X(), Y: s.Y(), Z: s.Z()}
		changed = true
	}
	if m.Alpha != base.Alpha {
		o.Alpha = m.Alpha
		changed = true
	}
	if m.Visible != base.Visible {
		v := m.Visible
		o.Visible = &v
		changed = true
	}
	if !animationsEqual(m.Animations, base.Animations) {
		o.Animations = entityAnimationsToRaw(m.Animations)
		changed = true
	}
	return o, changed
}

// animationsEqual compares two animation lists, Loop and BlendTime pointers
// included by value so two lists that mean the same thing compare equal.
func animationsEqual(a, b []EntityAnimation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Clip != b[i].Clip ||
			a[i].Speed != b[i].Speed || a[i].Autoplay != b[i].Autoplay {
			return false
		}
		switch {
		case a[i].Loop == nil && b[i].Loop == nil:
		case a[i].Loop == nil || b[i].Loop == nil:
			return false
		case *a[i].Loop != *b[i].Loop:
			return false
		}
		switch {
		case a[i].BlendTime == nil && b[i].BlendTime == nil:
		case a[i].BlendTime == nil || b[i].BlendTime == nil:
			return false
		case *a[i].BlendTime != *b[i].BlendTime:
			return false
		}
	}
	return true
}

// ---------- the definition registry ----------

// Definitions are read from disk once and cached, because a level places many
// instances of a few types and re-reading the file per instance would be one
// open() per barrel. The cache is keyed by name and holds failures too, so a
// level referencing a missing type reports it once rather than once per
// instance.
var (
	entityDefMu    sync.Mutex
	entityDefCache map[string]entityDefEntry
)

type entityDefEntry struct {
	def *EntityDef
	err error
}

// LoadEntityDef returns the named definition, reading it from disk on first
// use. The returned *EntityDef is shared and must not be mutated — Instantiate
// is what makes a copy to edit.
func LoadEntityDef(name string) (*EntityDef, error) {
	if name == "" {
		return nil, fmt.Errorf("no entity type given")
	}
	entityDefMu.Lock()
	defer entityDefMu.Unlock()

	if e, ok := entityDefCache[name]; ok {
		return e.def, e.err
	}
	def, err := readEntityDef(entityDefPath(name), name)
	if entityDefCache == nil {
		entityDefCache = map[string]entityDefEntry{}
	}
	entityDefCache[name] = entityDefEntry{def: def, err: err}
	return def, err
}

// ReloadEntityDefs drops the cache so the next lookup re-reads from disk —
// what the editor's "reload definitions" does after a definition file has been
// edited outside the running engine.
func ReloadEntityDefs() {
	entityDefMu.Lock()
	entityDefCache = nil
	entityDefMu.Unlock()
}

// readEntityDef parses one definition file.
func readEntityDef(path, name string) (*EntityDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("unknown entity type %q — expected %s", name, path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw RawEntityDef
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return entityDefFromRaw(raw, name)
}

// SaveEntityDef writes a definition back to resources/entities/<name>.yaml.
func SaveEntityDef(def *EntityDef) error {
	if def == nil || def.Name == "" {
		return fmt.Errorf("entity definition has no name")
	}
	data, err := yaml.Marshal(entityDefToRaw(def))
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(entityDefPath(def.Name), data); err != nil {
		return err
	}
	// The file on disk is now what was just written, so the cache entry for it
	// is stale by definition.
	entityDefMu.Lock()
	delete(entityDefCache, def.Name)
	entityDefMu.Unlock()
	return nil
}

// ScanEntityDefs lists the definition names available to place, sorted. Used by
// the editor's entity catalog.
func ScanEntityDefs() []string {
	files := fileutil.ScanFilesByExt(entityDefsDir(), entityDefExt)
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, strings.TrimSuffix(f, entityDefExt))
	}
	sort.Strings(names)
	return names
}

// sameVehicle reports whether an instance's vehicle block is unchanged from its
// definition's, so an untouched instance writes none.
func sameVehicle(a, b *VehicleSpec) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return true
	}
	if a.Mode != b.Mode || a.Drive != b.Drive || !slices.Equal(a.Interiors, b.Interiors) {
		return false
	}
	if len(a.Seats) != len(b.Seats) {
		return false
	}
	for i := range a.Seats {
		if a.Seats[i] != b.Seats[i] {
			return false
		}
	}
	return true
}
