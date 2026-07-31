package main

import "github.com/go-gl/mathgl/mgl32"

// BoundingType controls which bounding volume shape is used for collision.
type BoundingType int

const (
	BoundingAABB     BoundingType = iota
	BoundingSphere
	BoundingCylinder
)

// Entity is a dynamic game object that can move between chunks and sectors.
type Entity struct {
	ID            string
	Position      mgl32.Vec3
	Velocity      mgl32.Vec3
	Rotation      mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	Scale         mgl32.Vec3 // per-axis scale; zero value treated as {1,1,1}
	ModelPath     string
	TexturePath   string
	BoundingKind  BoundingType
	BoundHalfX    float32
	BoundHalfY    float32
	BoundHalfZ    float32
	Alpha         float32
	Geometry      Geometry
	Visible       bool
	CurrentSector string
	CurrentChunk  string
}

// ModelMatrix builds the TRS model matrix for this entity.
// Scale zero-value is treated as identity {1,1,1}.
func (e *Entity) ModelMatrix() mgl32.Mat4 {
	scale := e.Scale
	if scale == (mgl32.Vec3{}) {
		scale = mgl32.Vec3{1, 1, 1}
	}
	T := mgl32.Translate3D(e.Position.X(), e.Position.Y(), e.Position.Z())
	Rx := mgl32.HomogRotate3DX(e.Rotation.X())
	Ry := mgl32.HomogRotate3DY(e.Rotation.Y())
	Rz := mgl32.HomogRotate3DZ(e.Rotation.Z())
	S := mgl32.Scale3D(scale.X(), scale.Y(), scale.Z())
	return T.Mul4(Ry).Mul4(Rx).Mul4(Rz).Mul4(S)
}

// WorldAABB returns the entity's world-space bounds from its bounding
// half-extents, scaled and (conservatively) rotated. ok is false when no
// extents are authored, in which case the caller must not cull the entity — an
// unbounded entity is not a zero-sized one. A rotated entity uses the extents'
// length as a uniform radius so the box stays valid at any orientation.
func (e *Entity) WorldAABB() (min, max mgl32.Vec3, ok bool) {
	h := mgl32.Vec3{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ}
	if h.X() <= 0 || h.Y() <= 0 || h.Z() <= 0 {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	if s := e.Scale; s != (mgl32.Vec3{}) {
		h = mgl32.Vec3{h.X() * abs32(s.X()), h.Y() * abs32(s.Y()), h.Z() * abs32(s.Z())}
	}
	if e.Rotation != (mgl32.Vec3{}) {
		r := h.Len()
		h = mgl32.Vec3{r, r, r}
	}
	return e.Position.Sub(h), e.Position.Add(h), true
}

// Load uploads the entity's OBJ model to the GPU.
// Skipped when ModelPath is empty (e.g. invisible first-person player).
func (e *Entity) Load() error {
	if e.ModelPath == "" {
		return nil
	}
	verts, err := LoadOBJ(e.ModelPath)
	if err != nil {
		return err
	}
	// Convert flat []float32 to [][]float32 for Geometry.Load
	vertices := make([][]float32, len(verts)/5)
	for i := range vertices {
		vertices[i] = verts[i*5 : i*5+5]
	}
	indices := make([]int, len(vertices))
	for i := range indices {
		indices[i] = i
	}
	if err := e.Geometry.Load(vertices, indices, e.TexturePath, e.Position); err != nil {
		return err
	}
	e.Geometry.Alpha = e.Alpha
	return nil
}
