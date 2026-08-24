package main

// volume.go — Volume, a region of space a script can react to.
//
// A volume is a shape, an ID, and a script. It is never drawn outside the
// editor, never collides, and never moves on its own. All it does is answer
// "is this point inside", which is what the `enter`, `exit` and `interact`
// events resolve through — a doorway that opens, a room that dims the lights, a
// pit that kills.
//
// A model-less entity could serve, and for a while the plan was that it should:
// it already has an ID, tags, attributes and a bounding volume. But an entity
// also carries velocity, gravity, four collision flags, model attachments and a
// definition it was instanced from, none of which a trigger has any use for.
// Telling an author "make a box entity, then turn off the three collision flags
// and give it no models" is a rule to remember rather than a thing to place, and
// it puts triggers in the same list as the crates and the barrels. A volume is
// the smaller idea, so it gets the smaller type.
//
// Volumes live on the level rather than in a chunk, for the same reason entities
// do: a trigger region can span a doorway between two chunks, and its authored
// position is world-space.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// Volume is a scriptable region of space.
type Volume struct {
	// ID is unique within a level; a script addresses a volume by it (`@id`).
	ID string
	// ScriptData carries the tags, attributes and attached script — the same
	// three a sector or a wall has, so `#tag` reaches volumes on equal terms.
	ScriptData

	Position mgl32.Vec3
	Rotation mgl32.Vec3 // Euler angles (X, Y, Z) in radians

	// Shape and half-extents describe the region, using the same three kinds and
	// the same "box the shape is inscribed in" convention as an entity's bounds,
	// so one containment implementation serves both.
	Shape VolumeShape
	HalfX float32
	HalfY float32
	HalfZ float32

	// Enabled is what a script switches to arm or disarm a trigger without
	// having to move or delete it. A disabled volume contains nothing.
	Enabled bool

	// Geometry is the editor's wireframe box. Runtime-only, never serialized —
	// a volume is invisible during play.
	Geometry gfx.Geometry
}

// VolumeShape is the region's shape. The values line up with BoundingType so the
// two can be converted without a lookup, which is what lets shapeContainsPoint
// serve both.
type VolumeShape int

const (
	VolumeBox VolumeShape = iota
	VolumeSphere
	VolumeCylinder
)

// volumeShapeNames is the YAML/UI name of each shape, indexed by the shape
// itself. Adding one means adding its name here and nowhere else — the same
// arrangement boundingTypeNames uses, and for the same reason: a per-file switch
// is what lets an unknown name quietly load as the wrong shape.
var volumeShapeNames = []string{
	"box",
	"sphere",
	"cylinder",
}

// volumeShapeName returns a shape's YAML/UI name, falling back to the default
// for anything out of range.
func volumeShapeName(k VolumeShape) string {
	if int(k) < 0 || int(k) >= len(volumeShapeNames) {
		return volumeShapeNames[VolumeBox]
	}
	return volumeShapeNames[k]
}

// volumeShapeFromName is the inverse. An unknown name loads as a box rather than
// failing, so a level written by a newer build still opens.
func volumeShapeFromName(name string) VolumeShape {
	for i, n := range volumeShapeNames {
		if n == name {
			return VolumeShape(i)
		}
	}
	return VolumeBox
}

// boundingKind maps a volume's shape onto the entity bounding kind describing
// the same solid, so shapeContainsPoint can be shared.
func (v *Volume) boundingKind() BoundingType {
	switch v.Shape {
	case VolumeSphere:
		return BoundingSphere
	case VolumeCylinder:
		return BoundingCylinder
	default:
		return BoundingAABB
	}
}

// HalfExtents returns the volume's half-extents as a vector.
func (v *Volume) HalfExtents() mgl32.Vec3 {
	return mgl32.Vec3{v.HalfX, v.HalfY, v.HalfZ}
}

// HasBounds reports whether the volume describes a usable region. All three
// extents must be positive: a zero on any axis is an unauthored volume rather
// than a flat one.
func (v *Volume) HasBounds() bool {
	return v.HalfX > 0 && v.HalfY > 0 && v.HalfZ > 0
}

// ContainsPoint reports whether p is inside the volume. A disabled volume
// contains nothing, which is the whole point of the flag — a script can turn a
// trigger off without the engine having to remember it used to be somewhere.
func (v *Volume) ContainsPoint(p mgl32.Vec3) bool {
	if !v.Enabled || !v.HasBounds() {
		return false
	}
	return shapeContainsPoint(p, v.Position, v.Rotation, v.HalfExtents(), v.boundingKind())
}

// WorldAABB returns the volume's world-space bounds, for culling the editor's
// wireframe and for broad-phase overlap tests. ok is false when no extents are
// authored — an unbounded volume is not a zero-sized one.
//
// A rotated volume uses the extents' length as a uniform radius so the box stays
// valid at any orientation, matching Entity.WorldAABB.
func (v *Volume) WorldAABB() (min, max mgl32.Vec3, ok bool) {
	if !v.HasBounds() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	h := v.HalfExtents()
	if v.Rotation != (mgl32.Vec3{}) {
		r := h.Len()
		h = mgl32.Vec3{r, r, r}
	}
	return v.Position.Sub(h), v.Position.Add(h), true
}

// ---------- level lookup ----------

// FindVolume returns the level volume with the given ID, or nil. This is what a
// script's `@id` resolves through for volumes.
func FindVolume(lvl *Level, id string) *Volume {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].ID == id {
			return &lvl.Volumes[i]
		}
	}
	return nil
}

// FindVolumesByTag returns every level volume carrying tag, in level order.
func FindVolumesByTag(lvl *Level, tag string) []*Volume {
	if lvl == nil || tag == "" {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		if lvl.Volumes[i].HasTag(tag) {
			out = append(out, &lvl.Volumes[i])
		}
	}
	return out
}

// VolumesContaining returns every enabled volume whose region holds p, in level
// order. This is the query behind `enter`, `exit` and `interact`.
func VolumesContaining(lvl *Level, p mgl32.Vec3) []*Volume {
	if lvl == nil {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		if lvl.Volumes[i].ContainsPoint(p) {
			out = append(out, &lvl.Volumes[i])
		}
	}
	return out
}

// uniqueVolumeID returns base, or base with a numeric suffix that no volume in
// lvl is already using — the same guarantee uniqueEntityID gives, and for the
// same reason: two volumes sharing an ID would make `@id` ambiguous.
func uniqueVolumeID(lvl *Level, base string) string {
	if base == "" {
		base = "volume"
	}
	return uniqueLevelID(lvl, base)
}

// NewVolume returns a volume with the defaults a freshly placed one should have:
// a one-unit box, armed, sitting where it was put.
func NewVolume(id string, pos mgl32.Vec3) Volume {
	return Volume{
		ID:       id,
		Position: pos,
		Shape:    VolumeBox,
		HalfX:    1,
		HalfY:    1,
		HalfZ:    1,
		Enabled:  true,
	}
}

// ---------- serialization ----------

// volumeFromRaw converts the YAML form. An unknown shape name loads as a box and
// a missing `enabled` arms the volume, so the smallest useful entry in a level
// file is an ID, a position and a size.
func volumeFromRaw(r RawVolume) (Volume, error) {
	data, err := scriptDataFromRaw(r.RawScriptData, "volume "+r.ID)
	if err != nil {
		return Volume{}, err
	}
	return Volume{
		ID:         r.ID,
		ScriptData: data,
		Position:   mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:   mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Shape:      volumeShapeFromName(r.Shape),
		HalfX:      r.Half.X,
		HalfY:      r.Half.Y,
		HalfZ:      r.Half.Z,
		Enabled:    boolDefault(r.Enabled, true),
	}, nil
}

// volumeToRaw is the inverse.
func volumeToRaw(v *Volume) RawVolume {
	return RawVolume{
		ID:            v.ID,
		RawScriptData: scriptDataToRaw(v.ScriptData),
		Shape:         volumeShapeName(v.Shape),
		Position:      RawVec3{X: v.Position.X(), Y: v.Position.Y(), Z: v.Position.Z()},
		Rotation:      RawVec3{X: v.Rotation.X(), Y: v.Rotation.Y(), Z: v.Rotation.Z()},
		Half:          RawVec3{X: v.HalfX, Y: v.HalfY, Z: v.HalfZ},
		Enabled:       boolPtrIfFalse(v.Enabled),
	}
}
