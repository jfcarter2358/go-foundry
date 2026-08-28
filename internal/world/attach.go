package world

// attach.go — Attach, the "ride this" binding shared by sprites and emitters.
//
// A sprite or an emitter is usually placed and left alone, but the interesting
// ones move: smoke off a ship's engine, a flame on the torch an NPC is holding,
// dust off a wheel. Each of those is the same question — whose coordinates is
// this thing's position expressed in — asked of a different kind of carrier.
//
// So there is one binding rather than three, resolved cheapest-first:
//
//	nothing set   the world; Position is world-space
//	Frame         a moving coordinate system (a vehicle), exactly as
//	              SpawnPoint.Frame works — Position is that frame's local space
//	Entity        an entity's own transform
//	Entity+Model+Bone   a bone inside one of that entity's model attachments
//
// The four fields are names rather than pointers or indices for the reason the
// whole level format is: undo rebuilds the level wholesale, and a pointer into
// the old one would outlive it. An unresolvable name reads as "not attached"
// rather than as an error — deleting the entity a torch was riding should leave
// the torch somewhere, not fail the level load.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// Attach names the thing a sprite or emitter rides. The zero value is "world
// space", which is what an unattached object wants.
type Attach struct {
	// Frame is the ID of a moving coordinate system — in practice a vehicle.
	// Set, it means Position and Rotation are that frame's local coordinates.
	Frame string
	// Entity is the ID of an entity to ride. Set, Position is an offset from
	// that entity's origin, in its rotated space.
	Entity string
	// Model names which of the entity's model attachments Bone belongs to.
	// Only meaningful with Entity and Bone both set.
	Model string
	// Bone names a bone within Model. Only meaningful with Model set.
	Bone string
}

// IsZero reports whether nothing is attached, so callers can skip the whole
// resolution path in the common case.
func (a Attach) IsZero() bool {
	return a.Frame == "" && a.Entity == "" && a.Model == "" && a.Bone == ""
}

// Describe names the binding for the editor's panel and for log messages.
func (a Attach) Describe() string {
	switch {
	case a.Entity != "" && a.Bone != "":
		return a.Entity + "." + a.Model + "." + a.Bone
	case a.Entity != "":
		return a.Entity
	case a.Frame != "":
		return "frame " + a.Frame
	default:
		return ""
	}
}

// Matrix resolves the binding to the transform that carries a locally-authored
// position into world space. It is the identity for an unattached object and
// for one whose carrier has gone away.
//
// Called once per frame per attached object, after RefreshFrames has run — a
// frame's World matrix is only current for this frame once it has.
func (a Attach) Matrix(lvl *Level) mgl32.Mat4 {
	if a.IsZero() || lvl == nil {
		return mgl32.Ident4()
	}
	if a.Entity != "" {
		e := FindEntity(lvl, a.Entity)
		if e == nil {
			return mgl32.Ident4()
		}
		// An entity's own Position is expressed in whatever frame it rides, so
		// the frame has to be composed on the outside — otherwise a torch on an
		// NPC aboard a ship lands at the NPC's ship-local coordinates
		// reinterpreted as world ones.
		m := lvl.Frames.FrameFor(e).WorldMatrix().Mul4(e.ModelMatrix())
		if a.Bone != "" {
			if bm, ok := a.boneMatrix(e); ok {
				m = m.Mul4(bm)
			}
		}
		return m
	}
	if a.Frame != "" {
		return lvl.Frames.Frame(a.Frame).WorldMatrix()
	}
	return mgl32.Ident4()
}

// boneMatrix returns the posed placement of the named bone, relative to the
// entity. ok is false when the attachment, the bone or the pose is missing.
//
// The palette holds skinning *deltas* — the transform taking a vertex from rest
// to posed — so applying one to the bone's own rest placement is what gives the
// socket. That is the same array the colour pass and both shadow passes read,
// which is what keeps a flame, the hand holding it, and their shadows in
// agreement.
func (a Attach) boneMatrix(e *Entity) (mgl32.Mat4, bool) {
	m := e.Model(a.Model)
	if m == nil || m.Doc == nil || m.Anim == nil {
		return mgl32.Ident4(), false
	}
	bi := m.Doc.BoneIndexByName(a.Bone)
	if bi < 0 || bi >= len(m.Anim.Palette) {
		return mgl32.Ident4(), false
	}
	// The attachment's own offset/rotation/scale sits between the entity and
	// the bone, since the bone is expressed in the model's space.
	return m.LocalMatrix().Mul4(m.Anim.Palette[bi].Mul4(boneRestMatrix(m, bi))), true
}

// boneRestMatrix places a socket at the bone's head, oriented along it. A bone
// with no length falls back to a translation, which is the right answer for a
// marker bone authored as a single point.
func boneRestMatrix(m *EntityModel, bi int) mgl32.Mat4 {
	b := &m.Doc.Bones[bi]
	head := b.Head
	T := mgl32.Translate3D(head.X(), head.Y(), head.Z())
	axis := b.Tail.Sub(head)
	if axis.Len() < 1e-6 {
		return T
	}
	y := axis.Normalize()
	// Any vector not parallel to the bone will do for the reference; world up
	// unless the bone is already vertical.
	ref := mgl32.Vec3{0, 1, 0}
	if absF(y.Y()) > 0.999 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	x := ref.Cross(y).Normalize()
	z := y.Cross(x)
	R := mgl32.Mat4{
		x.X(), x.Y(), x.Z(), 0,
		y.X(), y.Y(), y.Z(), 0,
		z.X(), z.Y(), z.Z(), 0,
		0, 0, 0, 1,
	}
	return T.Mul4(R)
}

func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// ---------- serialization ----------

// RawAttach is the YAML form. Every field is omitempty, so an unattached object
// writes no `attach:` block at all.
type RawAttach struct {
	Frame  string `yaml:"frame,omitempty"`
	Entity string `yaml:"entity,omitempty"`
	Model  string `yaml:"model,omitempty"`
	Bone   string `yaml:"bone,omitempty"`
}

// AttachFromRaw converts the YAML form.
func AttachFromRaw(r *RawAttach) Attach {
	if r == nil {
		return Attach{}
	}
	return Attach{Frame: r.Frame, Entity: r.Entity, Model: r.Model, Bone: r.Bone}
}

// AttachToRaw is the inverse, returning nil for an unattached object so the key
// is omitted entirely rather than written as an empty map.
func AttachToRaw(a Attach) *RawAttach {
	if a.IsZero() {
		return nil
	}
	return &RawAttach{Frame: a.Frame, Entity: a.Entity, Model: a.Model, Bone: a.Bone}
}
