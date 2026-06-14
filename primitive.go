package main

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// PrimitiveType identifies the kind of procedural geometry.
type PrimitiveType int

const (
	PrimitiveRectPrism PrimitiveType = iota
	PrimitiveSphere
	PrimitiveCylinder
	PrimitivePlane
)

// Primitive is a procedurally-generated 3D shape placed in the world.
type Primitive struct {
	ID           string
	Kind         PrimitiveType
	Position     mgl32.Vec3
	Rotation     mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	Scale        mgl32.Vec3 // per-axis scale; zero value treated as {1,1,1}
	Texture      string
	Sides        int          // cylinder only; default 16
	Collision    bool
	UVOffset     mgl32.Vec2  // UV translation
	UVScale      mgl32.Vec2  // UV scale; zero treated as {1,1}
	UVRotation   float32     // UV rotation in radians
	Alpha        float32     // 0.0 = fully transparent, 1.0 = fully opaque
	BoundingKind BoundingType
	BoundHalfX   float32
	BoundHalfY   float32
	BoundHalfZ   float32
	Geometry     Geometry
	SectorID     string
	ChunkID      string
}

// ModelMatrix builds the TRS model matrix for this primitive.
func (p *Primitive) ModelMatrix() mgl32.Mat4 {
	scale := p.Scale
	if scale == (mgl32.Vec3{}) {
		scale = mgl32.Vec3{1, 1, 1}
	}
	T := mgl32.Translate3D(p.Position.X(), p.Position.Y(), p.Position.Z())
	Rx := mgl32.HomogRotate3DX(p.Rotation.X())
	Ry := mgl32.HomogRotate3DY(p.Rotation.Y())
	Rz := mgl32.HomogRotate3DZ(p.Rotation.Z())
	S := mgl32.Scale3D(scale.X(), scale.Y(), scale.Z())
	return T.Mul4(Ry).Mul4(Rx).Mul4(Rz).Mul4(S)
}

// Load generates the procedural geometry and uploads it to the GPU.
func (p *Primitive) Load() error {
	var vertices [][]float32
	var indices []int

	switch p.Kind {
	case PrimitiveRectPrism:
		vertices, indices = generateRectPrism()
	case PrimitiveSphere:
		vertices, indices = generateSphere(16, 32)
	case PrimitiveCylinder:
		sides := p.Sides
		if sides < 3 {
			sides = 16
		}
		vertices, indices = generateCylinder(sides)
	case PrimitivePlane:
		vertices, indices = generatePlane()
	}

	// Apply UV offset, scale, and rotation to all vertices
	uvScale := p.UVScale
	if uvScale == (mgl32.Vec2{}) {
		uvScale = mgl32.Vec2{1, 1}
	}
	cosR := float32(math.Cos(float64(p.UVRotation)))
	sinR := float32(math.Sin(float64(p.UVRotation)))
	for i := range vertices {
		u := vertices[i][3]
		v := vertices[i][4]
		// Scale, then rotate around (0.5, 0.5), then offset
		u = (u - 0.5) * uvScale.X()
		v = (v - 0.5) * uvScale.Y()
		ru := u*cosR - v*sinR + 0.5
		rv := u*sinR + v*cosR + 0.5
		vertices[i][3] = ru + p.UVOffset.X()
		vertices[i][4] = rv + p.UVOffset.Y()
	}

	if err := p.Geometry.Load(vertices, indices, p.Texture, p.Position); err != nil {
		return err
	}
	p.Geometry.Alpha = p.Alpha
	return nil
}

// primAsEntity creates a lightweight Entity view for collision reuse.
func primAsEntity(prim *Primitive) *Entity {
	return &Entity{
		Position:     prim.Position,
		BoundingKind: prim.BoundingKind,
		BoundHalfX:   prim.BoundHalfX,
		BoundHalfY:   prim.BoundHalfY,
		BoundHalfZ:   prim.BoundHalfZ,
	}
}

// generateRectPrism creates a unit cube from (-0.5,-0.5,-0.5) to (0.5,0.5,0.5).
// Each face is UV-mapped independently over [0,1]x[0,1].
func generateRectPrism() ([][]float32, []int) {
	// 4 vertices per face, 6 faces = 24 vertices
	vertices := [][]float32{
		// +Y (top face) — normal (0,1,0)
		{-0.5, 0.5, -0.5, 0, 0},
		{0.5, 0.5, -0.5, 1, 0},
		{0.5, 0.5, 0.5, 1, 1},
		{-0.5, 0.5, 0.5, 0, 1},
		// -Y (bottom face) — normal (0,-1,0)
		{-0.5, -0.5, 0.5, 0, 0},
		{0.5, -0.5, 0.5, 1, 0},
		{0.5, -0.5, -0.5, 1, 1},
		{-0.5, -0.5, -0.5, 0, 1},
		// +Z (front face) — normal (0,0,1)
		{-0.5, -0.5, 0.5, 0, 0},
		{0.5, -0.5, 0.5, 1, 0},
		{0.5, 0.5, 0.5, 1, 1},
		{-0.5, 0.5, 0.5, 0, 1},
		// -Z (back face) — normal (0,0,-1)
		{0.5, -0.5, -0.5, 0, 0},
		{-0.5, -0.5, -0.5, 1, 0},
		{-0.5, 0.5, -0.5, 1, 1},
		{0.5, 0.5, -0.5, 0, 1},
		// +X (right face) — normal (1,0,0)
		{0.5, -0.5, 0.5, 0, 0},
		{0.5, -0.5, -0.5, 1, 0},
		{0.5, 0.5, -0.5, 1, 1},
		{0.5, 0.5, 0.5, 0, 1},
		// -X (left face) — normal (-1,0,0)
		{-0.5, -0.5, -0.5, 0, 0},
		{-0.5, -0.5, 0.5, 1, 0},
		{-0.5, 0.5, 0.5, 1, 1},
		{-0.5, 0.5, -0.5, 0, 1},
	}

	indices := []int{
		0, 1, 2, 0, 2, 3, // +Y
		4, 5, 6, 4, 6, 7, // -Y
		8, 10, 9, 8, 11, 10, // +Z
		12, 14, 13, 12, 15, 14, // -Z
		16, 18, 17, 16, 19, 18, // +X
		20, 22, 21, 20, 23, 22, // -X
	}

	return vertices, indices
}

// generateSphere creates a UV sphere of radius 0.5 centered at origin.
func generateSphere(stacks, slices int) ([][]float32, []int) {
	vertices := make([][]float32, 0, (stacks+1)*(slices+1))

	for i := 0; i <= stacks; i++ {
		theta := float64(i) * math.Pi / float64(stacks)
		sinT := math.Sin(theta)
		cosT := math.Cos(theta)

		for j := 0; j <= slices; j++ {
			phi := float64(j) * 2.0 * math.Pi / float64(slices)
			sinP := math.Sin(phi)
			cosP := math.Cos(phi)

			x := float32(sinT * cosP * 0.5)
			y := float32(cosT * 0.5)
			z := float32(sinT * sinP * 0.5)
			u := float32(j) / float32(slices)
			v := float32(i) / float32(stacks)

			vertices = append(vertices, []float32{x, y, z, u, v})
		}
	}

	indices := make([]int, 0, stacks*slices*6)
	for i := 0; i < stacks; i++ {
		for j := 0; j < slices; j++ {
			a := i*(slices+1) + j
			b := a + slices + 1

			// Skip degenerate triangles at poles
			if i != 0 {
				indices = append(indices, a, b, a+1)
			}
			if i != stacks-1 {
				indices = append(indices, a+1, b, b+1)
			}
		}
	}

	return vertices, indices
}

// generateCylinder creates a cylinder of radius 0.5 and height 1.0
// centered at origin, with the given number of sides.
func generateCylinder(sides int) ([][]float32, []int) {
	vertices := make([][]float32, 0)
	indices := make([]int, 0)

	// --- Side surface ---
	for i := 0; i <= sides; i++ {
		angle := float64(i) * 2.0 * math.Pi / float64(sides)
		cos := float32(math.Cos(angle))
		sin := float32(math.Sin(angle))
		u := float32(i) / float32(sides)

		// Top vertex
		vertices = append(vertices, []float32{cos * 0.5, 0.5, sin * 0.5, u, 0})
		// Bottom vertex
		vertices = append(vertices, []float32{cos * 0.5, -0.5, sin * 0.5, u, 1})
	}

	for i := 0; i < sides; i++ {
		top := i * 2
		bot := top + 1
		nextTop := top + 2
		nextBot := top + 3
		indices = append(indices, top, nextTop, bot)
		indices = append(indices, bot, nextTop, nextBot)
	}

	// --- Top cap ---
	topCenter := len(vertices)
	vertices = append(vertices, []float32{0, 0.5, 0, 0.5, 0.5})
	for i := 0; i <= sides; i++ {
		angle := float64(i) * 2.0 * math.Pi / float64(sides)
		cos := float32(math.Cos(angle))
		sin := float32(math.Sin(angle))
		vertices = append(vertices, []float32{cos * 0.5, 0.5, sin * 0.5, cos*0.5 + 0.5, sin*0.5 + 0.5})
	}
	for i := 0; i < sides; i++ {
		indices = append(indices, topCenter, topCenter+1+i, topCenter+2+i)
	}

	// --- Bottom cap ---
	botCenter := len(vertices)
	vertices = append(vertices, []float32{0, -0.5, 0, 0.5, 0.5})
	for i := 0; i <= sides; i++ {
		angle := float64(i) * 2.0 * math.Pi / float64(sides)
		cos := float32(math.Cos(angle))
		sin := float32(math.Sin(angle))
		vertices = append(vertices, []float32{cos * 0.5, -0.5, sin * 0.5, cos*0.5 + 0.5, sin*0.5 + 0.5})
	}
	for i := 0; i < sides; i++ {
		indices = append(indices, botCenter, botCenter+2+i, botCenter+1+i)
	}

	return vertices, indices
}

// generatePlane creates a double-sided XZ quad at y=0, from (-0.5,0,-0.5) to (0.5,0,0.5).
func generatePlane() ([][]float32, []int) {
	vertices := [][]float32{
		{-0.5, 0, -0.5, 0, 0},
		{0.5, 0, -0.5, 1, 0},
		{0.5, 0, 0.5, 1, 1},
		{-0.5, 0, 0.5, 0, 1},
	}

	indices := []int{
		0, 2, 1, 0, 3, 2, // top face
		0, 1, 2, 0, 2, 3, // bottom face
	}

	return vertices, indices
}
