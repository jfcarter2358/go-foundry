package world

// prefab.go — a reusable group of level content, live-linked by file.
//
// A prefab is a piece of a level someone built once and wants twenty of: a
// street lamp that is a post brush, a light and a trigger; a guard tower that
// is six brushes, a ladder volume and two entities; a modular corridor
// section. What makes it a prefab rather than a copy-paste is the link — the
// instance stores a *file reference*, and editing that file changes every
// placement of it, the way editing an `.fmdl` changes every Scenery that names
// it.
//
// # Why not a chunk
//
// A chunk is a streaming and frame-ownership container: it is what a script
// switches off to unload a region, and what a vehicle owns to have an interior.
// Those are properties of *where content is*, not of *what content is*, and a
// level wants both — twenty lamp prefabs spread across four streaming chunks.
// Overloading Chunk to mean "reusable group" as well would make the two
// inseparable: switching off a chunk would switch off every instance in it, and
// a prefab spanning a chunk boundary would be unexpressible.
//
// # Why the content is expanded rather than kept behind an indirection
//
// A materialised instance becomes ordinary level objects — a chunk of brushes,
// entities, volumes, point lights — tagged with the instance they came from.
// Every downstream pass (render, collision, scripting, the shadow walk, the
// spatial hash) then works on them with no knowledge that prefabs exist, which
// is the same trade entity chunk attachments already make (levelio/
// entity_chunks.go) and for the same reason: an extra transform threaded
// through five subsystems is five places to get it wrong.
//
// What that costs is that a level file must not write the expanded content back
// out, or the link is lost and the copy stops tracking the file. FromPrefab is
// what the saver checks, exactly as Chunk.FromEntity already is.
//
// Materialising an instance is levelio's job (levelio/prefabs.go): this package
// holds the description and must not reach for a file reader.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// PrefabInstance is one placement of a prefab file in a level.
type PrefabInstance struct {
	// ID is unique within the level. It is what the materialised content's own
	// IDs are prefixed with, so a script addressing the third lamp's light says
	// `@lamp3.bulb` rather than a number — and so two instances of one prefab
	// do not collide.
	ID string
	// File is the prefab under prefabs/, without the extension. Empty is an
	// instance with nothing in it yet, which is what one starts as.
	File string

	// Position, Rotation and Scale place the prefab's content in the world.
	// They map one-to-one onto Chunk.Origin/Rotation/Scale, which is why the
	// rotation is a single Y angle rather than a full Euler triple: a chunk's
	// own placement has only ever had one, and inventing two more here would
	// mean the materialised chunk could not hold what the instance said.
	Position mgl32.Vec3
	Rotation float32 // radians about Y
	Scale    float32 // 0 treated as 1

	// Frame is the vehicle the instance rides, or "" for the world — the same
	// field, and the same meaning, a volume's has. A prefab placed inside a
	// ship is content that ship carries.
	Frame string
}

// EffectiveScale is Scale with the zero value read as 1, the way every other
// scale in this package treats it.
func (p *PrefabInstance) EffectiveScale() float32 {
	if p.Scale == 0 {
		return 1
	}
	return p.Scale
}

// Matrix is the instance's placement, in the same composition order a chunk's
// own is built with — so a materialised chunk given these three fields sits
// exactly where this says.
func (p *PrefabInstance) Matrix() mgl32.Mat4 {
	s := p.EffectiveScale()
	return mgl32.Translate3D(p.Position.X(), p.Position.Y(), p.Position.Z()).
		Mul4(mgl32.HomogRotate3DY(p.Rotation)).
		Mul4(mgl32.Scale3D(s, s, s))
}

// Prefab is a prefab file's contents, as loaded.
//
// It deliberately holds RAW forms rather than runtime ones. A prefab is
// stamped, not run: materialising an instance means renaming every ID, moving
// every position and handing the result to the ordinary FromRaw path, and doing
// that to a runtime object would mean undoing work the loader had just done
// (uploading geometry that is about to be moved, resolving scripts against IDs
// that are about to change).
type Prefab struct {
	// Brushes become one chunk per instance, placed by the instance's own
	// transform. One chunk rather than several because a prefab's brushes are
	// authored in a single space — the prefab's own origin — and a chunk is
	// precisely "brushes sharing a placement".
	Brushes []RawBrush
	// Entities, Volumes and Lights are level-scoped, so they are stamped
	// straight onto the level with their positions carried through the
	// instance's matrix.
	Entities []RawEntity
	Volumes  []RawVolume
	Lights   []RawPointLight
}

// IsEmpty reports whether the prefab holds nothing at all — a file someone
// created and has not filled in yet, which materialises to nothing rather than
// to an error.
func (p *Prefab) IsEmpty() bool {
	return len(p.Brushes) == 0 && len(p.Entities) == 0 &&
		len(p.Volumes) == 0 && len(p.Lights) == 0
}

// PrefabChunkID is the ID the chunk materialised for an instance carries. One
// per instance, named after it, so the Explorer and any script that reaches for
// it see a name rather than an index.
func PrefabChunkID(instanceID string) string { return instanceID + "_geometry" }

// PrefabMemberID is the ID one piece of a prefab's content carries once
// stamped: the instance's own name, then the name the prefab gave it.
//
// Prefixing rather than suffixing so that everything one instance produced
// sorts together in the Explorer, and so `#tag`-style prefix reading of an ID
// answers "which instance" first.
func PrefabMemberID(instanceID, memberID string) string {
	if memberID == "" {
		return instanceID
	}
	return instanceID + "_" + memberID
}

// ---------- level lookup ----------

// FindPrefabInstance returns the instance with the given ID, or nil.
func FindPrefabInstance(lvl *Level, id string) *PrefabInstance {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Prefabs {
		if lvl.Prefabs[i].ID == id {
			return &lvl.Prefabs[i]
		}
	}
	return nil
}

// UniquePrefabInstanceID returns base, or base with a numeric suffix that no
// instance in lvl is using — the same guarantee UniqueVolumeID gives, and for
// the same reason: two instances sharing an ID would give their contents the
// same names.
func UniquePrefabInstanceID(lvl *Level, base string) string {
	if base == "" {
		base = "prefab"
	}
	return uniqueLevelID(lvl, base)
}

// ---------- serialization ----------

// PrefabInstanceFromRaw converts the YAML form.
func PrefabInstanceFromRaw(r RawPrefabInstance) PrefabInstance {
	return PrefabInstance{
		ID:       r.ID,
		File:     r.File,
		Position: mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation: r.Rotation,
		Scale:    r.Scale,
		Frame:    r.Frame,
	}
}

// PrefabInstanceToRaw is the inverse.
func PrefabInstanceToRaw(p *PrefabInstance) RawPrefabInstance {
	return RawPrefabInstance{
		ID:       p.ID,
		File:     p.File,
		Position: RawVec3{X: p.Position.X(), Y: p.Position.Y(), Z: p.Position.Z()},
		Rotation: p.Rotation,
		Scale:    p.Scale,
		Frame:    p.Frame,
	}
}
