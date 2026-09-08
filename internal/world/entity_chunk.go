package world

// entity_chunk.go — the chunk attachments on an entity.
//
// A model attachment (entity_model.go) gives an entity a *mesh*. A chunk
// attachment gives it a *place*: sectors with floors, ceilings, walls and
// portals — the same geometry a level is built from — authored around the
// entity's own origin and carried wherever the entity goes.
//
// Nothing new had to be invented underneath it. A chunk already knew how to be
// owned by an entity (Chunk.FrameOwner, frame.go), because that is how a
// vehicle's walkable interior works; what was missing was a way for that
// binding to live on a *type* rather than on one level's hand-wiring. A
// definition with chunk attachments is a ship you can place twice.
//
// The geometry itself is not copied into the definition. An attachment names a
// file under chunks/ — the standalone chunk format the editor has always been
// able to export — so the same hull can back several types, and editing it is
// editing one file rather than every definition that mentions it. What the
// attachment owns is where that chunk sits *within* the entity.
//
// Materialising an attachment into a level is levelio's job
// (levelio/entity_chunks.go): this package holds the description and must not
// reach for a file reader.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
)

// RawEntityChunk is the YAML form of one chunk attachment.
//
// Offset is a pointer for the reason RawEntityModel's vectors are: an instance
// override has to be able to leave the definition's placement alone, and a
// plain RawVec3 cannot tell that apart from a move to the origin.
type RawEntityChunk struct {
	Name     string   `yaml:"name"`
	File     string   `yaml:"file"`
	Offset   *RawVec3 `yaml:"offset,omitempty"`
	Rotation float32  `yaml:"rotation,omitempty"` // radians about Y
	Scale    float32  `yaml:"scale,omitempty"`    // 0 = 1
	Disabled bool     `yaml:"disabled,omitempty"`
}

// EntityChunk is one piece of world geometry attached to an entity.
type EntityChunk struct {
	// Name identifies the attachment within its entity, and is what the
	// materialised chunk's ID is built from — so a script addressing a deck
	// says `@shuttle.hold`, not a number. Unique per entity.
	Name string
	// File is the chunk under chunks/, without the extension. Empty is an
	// attachment with nothing in it yet, which is what one starts as.
	File string

	// Offset, Rotation and Scale place the chunk *within* the entity. They map
	// one-to-one onto Chunk.Origin/Rotation/Scale, which is why the rotation is
	// a single Y angle rather than a full Euler triple: a chunk's own placement
	// has only ever had one, and inventing two more here would mean the
	// materialised chunk could not hold what the attachment said.
	Offset   mgl32.Vec3
	Rotation float32 // radians about Y
	Scale    float32 // 0 treated as 1

	// Disabled materialises the chunk switched off — not drawn, not collided
	// with, its buffers never uploaded (see Chunk.Disabled). A deck a script
	// switches on when the player boards costs nothing until then.
	Disabled bool
}

// EffectiveScale treats 0 as 1, so an attachment that never mentioned a scale
// is full size rather than a point.
func (c *EntityChunk) EffectiveScale() float32 {
	if c.Scale == 0 {
		return 1
	}
	return c.Scale
}

// entityChunkFromRaw converts one attachment.
func entityChunkFromRaw(r RawEntityChunk) EntityChunk {
	return EntityChunk{
		Name:     r.Name,
		File:     r.File,
		Offset:   vec3FromRawPtr(r.Offset, mgl32.Vec3{}),
		Rotation: r.Rotation,
		Scale:    r.Scale,
		Disabled: r.Disabled,
	}
}

// entityChunkToRaw is its inverse.
func entityChunkToRaw(c *EntityChunk) RawEntityChunk {
	return RawEntityChunk{
		Name:     c.Name,
		File:     c.File,
		Offset:   vec3ToRawPtr(c.Offset),
		Rotation: c.Rotation,
		Scale:    c.Scale,
		Disabled: c.Disabled,
	}
}

// entityChunksFromRaw converts a whole list, naming anything that arrived
// without one — an unnamed attachment could be neither addressed nor
// overridden, and dropping it would lose geometry the author placed.
func entityChunksFromRaw(raw []RawEntityChunk) ([]EntityChunk, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]EntityChunk, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		c := entityChunkFromRaw(r)
		if c.Name == "" {
			c.Name = fmt.Sprintf("chunk_%d", i+1)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("duplicate chunk attachment %q", c.Name)
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out, nil
}

// entityChunksToRaw is entityChunksFromRaw's inverse.
func entityChunksToRaw(chunks []EntityChunk) []RawEntityChunk {
	if len(chunks) == 0 {
		return nil
	}
	out := make([]RawEntityChunk, 0, len(chunks))
	for i := range chunks {
		out = append(out, entityChunkToRaw(&chunks[i]))
	}
	return out
}

// cloneEntityChunks copies a list so a definition and the instances stamped
// from it never share one.
func cloneEntityChunks(chunks []EntityChunk) []EntityChunk {
	if len(chunks) == 0 {
		return nil
	}
	out := make([]EntityChunk, len(chunks))
	copy(out, chunks)
	return out
}

// entityChunksEqual reports whether two lists say the same thing, so an
// instance that never touched its type's geometry writes no `chunks:` block.
func entityChunksEqual(a, b []EntityChunk) bool {
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

// ChunkAttachment returns the named chunk attachment, or nil.
func (e *Entity) ChunkAttachment(name string) *EntityChunk {
	if e == nil {
		return nil
	}
	for i := range e.Chunks {
		if e.Chunks[i].Name == name {
			return &e.Chunks[i]
		}
	}
	return nil
}

// EntityChunkID is the ID the chunk materialised from one attachment carries in
// a level: the entity's ID and the attachment's, joined.
//
// Built from both halves rather than from the attachment alone because two
// shuttles placed from the same definition would otherwise both call their hull
// "hull", and a chunk ID has to name exactly one thing for `@id` to mean
// anything.
func EntityChunkID(entityID, attachment string) string {
	if entityID == "" {
		return attachment
	}
	return entityID + "." + attachment
}

// UniqueEntityChunkName returns base, or base with a numeric suffix no
// attachment on the entity is using.
func UniqueEntityChunkName(e *Entity, base string) string {
	if base == "" {
		base = "chunk"
	}
	if e == nil || e.ChunkAttachment(base) == nil {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if e.ChunkAttachment(name) == nil {
			return name
		}
	}
}

// UniqueDefChunkName is UniqueEntityChunkName for a definition being authored,
// which has no Entity to ask.
func UniqueDefChunkName(chunks []EntityChunk, base string) string {
	if base == "" {
		base = "chunk"
	}
	taken := func(n string) bool {
		for i := range chunks {
			if chunks[i].Name == n {
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

// ---------- cloning a definition ----------

// Clone returns a fully independent copy of a definition — every slice and map
// with a fresh backing array, and the vehicle block copied rather than shared.
//
// It exists for the entity editor's undo, which snapshots whole documents the
// way the model and particle editors do. The rule that makes that safe is that
// nothing in an EntityDef holds GPU state: the models are templates, and the
// chunks are file names. A snapshot is therefore plain data, and restoring one
// is an assignment plus a preview rebuild.
//
// The shared *EntityDef handed out by LoadEntityDef must not be mutated (see
// its doc comment), so the editor edits a clone from the moment it opens one.
func (d *EntityDef) Clone() *EntityDef {
	if d == nil {
		return nil
	}
	c := *d
	c.Tags = append([]string(nil), d.Tags...)
	c.Attributes = d.Attributes.Clone()
	c.Params = d.Params.Clone()
	c.Chunks = cloneEntityChunks(d.Chunks)
	c.Models = nil
	for i := range d.Models {
		c.Models = append(c.Models, d.Models[i].TemplateCopy())
	}
	c.Vehicle = CloneVehicleSpec(d.Vehicle)
	return &c
}

// CloneVehicleSpec deep-copies a vehicle block. A definition normally *shares*
// its spec with every instance, because nothing at runtime mutates it — but an
// editor does, so the copy it edits has to be its own.
func CloneVehicleSpec(v *VehicleSpec) *VehicleSpec {
	if v == nil {
		return nil
	}
	c := *v
	c.Interiors = append([]string(nil), v.Interiors...)
	c.Seats = append([]VehicleSeat(nil), v.Seats...)
	return &c
}
