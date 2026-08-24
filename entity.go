package main

import (
	"log"
	"path/filepath"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// BoundingType controls which bounding volume shape is used for collision.
type BoundingType int

const (
	BoundingAABB BoundingType = iota
	BoundingSphere
	BoundingCylinder
)

// Entity is a dynamic game object that can move between chunks and sectors.
type Entity struct {
	ID           string
	Position     mgl32.Vec3
	Velocity     mgl32.Vec3
	Rotation     mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	Scale        mgl32.Vec3 // per-axis scale; zero value treated as {1,1,1}
	ModelPath    string
	TexturePath  string
	BoundingKind BoundingType
	BoundHalfX   float32
	BoundHalfY   float32
	BoundHalfZ   float32
	Alpha        float32
	// Submeshes is the entity's model, split one Geometry per material: a
	// Geometry binds a single texture per draw call, so a model authored with
	// per-face textures cannot be one buffer. A model with no materials — every
	// .obj the engine shipped before the model editor — yields exactly one
	// submesh textured with TexturePath, so nothing about those changes.
	Submeshes []Geometry
	// Animation names the clip this entity plays, addressed by name in the level
	// file so a model can gain and lose clips without renumbering anything.
	// Anim is the live playback state, nil on a model with no motion to play.
	Animation      string
	AnimationSpeed float32
	AnimationLoop  *bool // nil = follow the clip's own loop flag
	Anim           *EntityAnim
	Visible        bool
	CurrentSector  string
	CurrentChunk   string
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

// Load uploads the entity's model to the GPU, one Geometry per material.
// Skipped when ModelPath is empty (e.g. invisible first-person player).
//
// Both .obj and the model editor's .fmdl are accepted; the two share a document
// representation (ModelDoc), so the only difference is which reader produces it.
func (e *Entity) Load() error {
	if e.ModelPath == "" {
		return nil
	}
	doc, err := loadModelDocument(e.ModelPath)
	if err != nil {
		return err
	}

	e.DeleteGeometry()
	// The document is kept — rather than discarded once the buffers are up — when
	// there is motion to play: a pose is evaluated against the bones and weights,
	// and re-reading the file every frame is not an option.
	e.InitAnimation(doc)
	for _, sub := range doc.modelSubmeshes() {
		if len(sub.Indices) == 0 {
			continue
		}
		// A face that named no material inherits the entity's own texture,
		// which is what keeps every pre-existing entity YAML working unchanged.
		tex := sub.Texture
		if tex == "" || tex == "default" {
			tex = e.TexturePath
		}
		// Every texture the model reaches for is ensured, the entity's own
		// included: nothing else loads it. The level loader only walks chunk
		// geometry, so a model placed with a texture no wall happens to use
		// would otherwise draw untextured.
		if err := ensureTextureLoaded(tex); err != nil {
			log.Printf("entity %s: texture %q: %v", e.ID, tex, err)
		}
		g := Geometry{Smooth: sub.Smooth, SmoothAngle: doc.SmoothAngle}
		if e.Anim != nil {
			// Set before Load, which de-indexes these alongside the positions.
			g.SkinIdx, g.SkinWt = sub.SkinIdx, sub.SkinWt
		}
		if err := g.Load(sub.Vertices, sub.Indices, tex, e.Position); err != nil {
			return err
		}
		g.Alpha = e.Alpha
		e.Submeshes = append(e.Submeshes, g)
	}
	e.bindPalette()
	return nil
}

// DeleteGeometry releases the entity's GPU buffers. Reloading a model without
// this would leak the previous upload's VAO/VBO.
func (e *Entity) DeleteGeometry() {
	for i := range e.Submeshes {
		e.Submeshes[i].Delete()
	}
	e.Submeshes = nil
}

// HasAnimation reports whether this entity has clips it can play.
func (e *Entity) HasAnimation() bool { return e.Anim != nil }

// SetAlpha applies a: transparency is a per-Geometry property, so it has to
// reach every submesh or half a model would stop blending.
func (e *Entity) SetAlpha(a float32) {
	e.Alpha = a
	for i := range e.Submeshes {
		e.Submeshes[i].Alpha = a
	}
}

// HasGeometry reports whether the entity has any uploaded mesh.
func (e *Entity) HasGeometry() bool {
	for i := range e.Submeshes {
		if len(e.Submeshes[i].Vertices) > 0 {
			return true
		}
	}
	return false
}

// loadModelDocument reads a model file into the editable document form, picking
// the reader by extension.
func loadModelDocument(path string) (*ModelDoc, error) {
	if strings.EqualFold(filepath.Ext(path), modelExt) {
		return LoadModel(path)
	}
	return LoadOBJMesh(path)
}
