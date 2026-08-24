package main

// model_primitives.go — primitive shapes as editable meshes, and merging one
// document into another.
//
// primitive.go already generates boxes, spheres and cylinders, but it emits the
// de-indexed triangle soup Geometry wants: 36 loose vertices for a cube, with
// no record of which six of them were a face. Importing that into the model
// editor would give you a cube you cannot extrude, because there is no "top
// face" left to select.
//
// These generators build ModelDoc directly, so a cylinder arrives as n side
// quads and two n-gon caps — the faces someone would actually want to grab.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ModelPrimitiveKind names a shape the editor can add.
type ModelPrimitiveKind int

const (
	ModelPrimBox ModelPrimitiveKind = iota
	ModelPrimPlane
	ModelPrimCylinder
	ModelPrimCone
	ModelPrimSphere
)

func (k ModelPrimitiveKind) String() string {
	switch k {
	case ModelPrimBox:
		return "Box"
	case ModelPrimPlane:
		return "Plane"
	case ModelPrimCylinder:
		return "Cylinder"
	case ModelPrimCone:
		return "Cone"
	default:
		return "Sphere"
	}
}

// modelPrimitiveKinds is the palette order shown in the panel.
var modelPrimitiveKinds = []ModelPrimitiveKind{
	ModelPrimBox, ModelPrimPlane, ModelPrimCylinder, ModelPrimCone, ModelPrimSphere,
}

// NewPrimitiveDoc builds a standalone document for one primitive, sized to fit
// a unit cube so shapes arrive at comparable scale. segments controls the
// radial resolution of the round shapes and is ignored by box and plane.
func NewPrimitiveDoc(kind ModelPrimitiveKind, segments int) *ModelDoc {
	if segments < 3 {
		segments = 3
	}
	if segments > 128 {
		segments = 128
	}
	switch kind {
	case ModelPrimPlane:
		return newPlaneDoc()
	case ModelPrimCylinder:
		return newCylinderDoc(segments)
	case ModelPrimCone:
		return newConeDoc(segments)
	case ModelPrimSphere:
		return newSphereDoc(segments, segments/2)
	default:
		return NewCubeDoc()
	}
}

func newPlaneDoc() *ModelDoc {
	const h = 0.5
	m := &ModelDoc{
		ID:    "plane",
		Verts: []mgl32.Vec3{{-h, 0, -h}, {-h, 0, h}, {h, 0, h}, {h, 0, -h}},
		Faces: []ModelFace{{
			Verts: []int{0, 1, 2, 3},
			UVs:   []mgl32.Vec2{{0, 0}, {0, 1}, {1, 1}, {1, 0}},
		}},
	}
	m.invalidate()
	return m
}

// newCylinderDoc builds a capped cylinder: n side quads plus a top and bottom
// n-gon. The caps stay single faces rather than triangle fans so they can be
// selected and extruded as one.
func newCylinderDoc(sides int) *ModelDoc {
	const r, h = 0.5, 0.5
	m := &ModelDoc{ID: "cylinder"}

	for i := 0; i < sides; i++ {
		a := float64(i) / float64(sides) * 2 * math.Pi
		x, z := float32(math.Cos(a))*r, float32(math.Sin(a))*r
		m.Verts = append(m.Verts, mgl32.Vec3{x, -h, z}) // bottom ring: 0..sides-1
	}
	for i := 0; i < sides; i++ {
		a := float64(i) / float64(sides) * 2 * math.Pi
		x, z := float32(math.Cos(a))*r, float32(math.Sin(a))*r
		m.Verts = append(m.Verts, mgl32.Vec3{x, h, z}) // top ring: sides..2*sides-1
	}

	for i := 0; i < sides; i++ {
		j := (i + 1) % sides
		u0 := float32(i) / float32(sides)
		u1 := float32(i+1) / float32(sides)
		m.Faces = append(m.Faces, ModelFace{
			Verts: []int{i, j, sides + j, sides + i},
			UVs:   []mgl32.Vec2{{u0, 0}, {u1, 0}, {u1, 1}, {u0, 1}},
		})
	}

	// Both caps are built in ring order; orientFacesOutward below settles which
	// way each ends up facing.
	m.Faces = append(m.Faces,
		ringCapFace(sides, sides), // top
		ringCapFace(0, sides),     // bottom
	)
	m.orientFacesOutward()
	return m
}

// ringCapFace builds an n-gon over a contiguous run of count vertex indices
// starting at base, in ring order. Winding is settled afterwards by
// orientFacesOutward.
func ringCapFace(base, count int) ModelFace {
	verts := make([]int, count)
	uvs := make([]mgl32.Vec2, count)
	for i := 0; i < count; i++ {
		verts[i] = base + i
		a := float64(i) / float64(count) * 2 * math.Pi
		// Cap UVs are the unit circle mapped into 0..1, so a texture lands on
		// the cap round rather than stretched along one axis.
		uvs[i] = mgl32.Vec2{
			float32(math.Cos(a))*0.5 + 0.5,
			float32(math.Sin(a))*0.5 + 0.5,
		}
	}
	return ModelFace{Verts: verts, UVs: uvs}
}

// newConeDoc builds a cone: n triangles up to an apex, over one n-gon base.
func newConeDoc(sides int) *ModelDoc {
	const r, h = 0.5, 0.5
	m := &ModelDoc{ID: "cone"}
	for i := 0; i < sides; i++ {
		a := float64(i) / float64(sides) * 2 * math.Pi
		m.Verts = append(m.Verts, mgl32.Vec3{
			float32(math.Cos(a)) * r, -h, float32(math.Sin(a)) * r,
		})
	}
	apex := len(m.Verts)
	m.Verts = append(m.Verts, mgl32.Vec3{0, h, 0})

	for i := 0; i < sides; i++ {
		j := (i + 1) % sides
		u0 := float32(i) / float32(sides)
		u1 := float32(i+1) / float32(sides)
		m.Faces = append(m.Faces, ModelFace{
			Verts: []int{i, j, apex},
			UVs:   []mgl32.Vec2{{u0, 0}, {u1, 0}, {(u0 + u1) / 2, 1}},
		})
	}
	m.Faces = append(m.Faces, ringCapFace(0, sides))
	m.orientFacesOutward()
	return m
}

// newSphereDoc builds a UV sphere: quads through the body, triangles at the two
// poles where the quad would degenerate.
func newSphereDoc(slices, stacks int) *ModelDoc {
	if stacks < 2 {
		stacks = 2
	}
	const r = 0.5
	m := &ModelDoc{ID: "sphere"}

	// Poles are single vertices; every intermediate stack is a full ring.
	north := 0
	m.Verts = append(m.Verts, mgl32.Vec3{0, r, 0})
	ringStart := make([]int, stacks-1)
	for s := 1; s < stacks; s++ {
		phi := float64(s) / float64(stacks) * math.Pi
		y := float32(math.Cos(phi)) * r
		rad := float32(math.Sin(phi)) * r
		ringStart[s-1] = len(m.Verts)
		for i := 0; i < slices; i++ {
			th := float64(i) / float64(slices) * 2 * math.Pi
			m.Verts = append(m.Verts, mgl32.Vec3{
				float32(math.Cos(th)) * rad, y, float32(math.Sin(th)) * rad,
			})
		}
	}
	south := len(m.Verts)
	m.Verts = append(m.Verts, mgl32.Vec3{0, -r, 0})

	uvAt := func(i, s int) mgl32.Vec2 {
		return mgl32.Vec2{float32(i) / float32(slices), 1 - float32(s)/float32(stacks)}
	}

	for i := 0; i < slices; i++ {
		j := (i + 1) % slices
		// North cap.
		m.Faces = append(m.Faces, ModelFace{
			Verts: []int{north, ringStart[0] + i, ringStart[0] + j},
			UVs:   []mgl32.Vec2{uvAt(i, 0), uvAt(i, 1), uvAt(i+1, 1)},
		})
		// Body.
		for s := 0; s < stacks-2; s++ {
			a, b := ringStart[s], ringStart[s+1]
			m.Faces = append(m.Faces, ModelFace{
				Verts: []int{a + i, b + i, b + j, a + j},
				UVs:   []mgl32.Vec2{uvAt(i, s+1), uvAt(i, s+2), uvAt(i+1, s+2), uvAt(i+1, s+1)},
			})
		}
		// South cap.
		last := ringStart[stacks-2]
		m.Faces = append(m.Faces, ModelFace{
			Verts: []int{south, last + j, last + i},
			UVs:   []mgl32.Vec2{uvAt(i, stacks), uvAt(i+1, stacks-1), uvAt(i, stacks-1)},
		})
	}
	m.orientFacesOutward()
	return m
}

// orientFacesOutward flips any face whose normal points back towards the
// origin.
//
// Every generator here builds an origin-centred convex solid, which makes
// "normal points away from the origin" an exact test for correct winding — and
// a far more reliable one than deriving the vertex order by hand for each ring
// and cap, where a single reversed loop produces a shape that looks right until
// it is lit or extruded.
//
// A face whose centroid sits on the origin has no outward direction to test
// against (the plane primitive's single face), so it is left alone.
func (m *ModelDoc) orientFacesOutward() {
	for fi := range m.Faces {
		f := &m.Faces[fi]
		c := m.faceCentroid(f)
		if c.Len() < 1e-6 {
			continue
		}
		n := m.faceNormal(f)
		if n.Len() < 0.5 || n.Dot(c) >= 0 {
			continue
		}
		reverseFaceLoop(f)
	}
	m.invalidate()
}

// reverseFaceLoop flips a face's winding, keeping each corner's UV with its
// vertex.
func reverseFaceLoop(f *ModelFace) {
	for i, j := 0, len(f.Verts)-1; i < j; i, j = i+1, j-1 {
		f.Verts[i], f.Verts[j] = f.Verts[j], f.Verts[i]
	}
	for i, j := 0, len(f.UVs)-1; i < j; i, j = i+1, j-1 {
		f.UVs[i], f.UVs[j] = f.UVs[j], f.UVs[i]
	}
}

// ---------- merging ----------

// Merge appends another document's geometry into this one, offset by the given
// translation, and returns the indices of the added faces so the caller can
// leave them selected.
//
// The two meshes stay separate islands — no vertices are welded across the
// join. Welding would silently fuse a box dropped on top of another into one
// surface, which is almost never what "add a primitive" means; the user can
// snap and merge deliberately afterwards.
//
// The other document's armature comes across too, keeping its own hierarchy,
// its own vertex bindings and its own animation clips. Dropping any of them
// would be a silent data loss the moment anyone merges two rigged models — and
// a primitive, which is what this is usually called with, simply has no bones to
// bring.
//
// Its texture does not come across, because a document wears exactly one and
// the merged result is one document. The merged geometry therefore arrives
// wearing whatever m already wears, and its UVs — authored against a different
// atlas — will land wherever those coordinates fall on it. Re-unwrapping
// (model_uv_atlas.go) is what puts the combined mesh back on one layout.
func (m *ModelDoc) Merge(other *ModelDoc, offset mgl32.Vec3) []int {
	if other == nil || len(other.Verts) == 0 {
		return nil
	}
	m.ensureWeights()
	other.ensureWeights()
	base := len(m.Verts)
	boneBase := len(m.Bones)
	for i, p := range other.Verts {
		ws := append([]ModelVertexWeight(nil), other.Weights[i]...)
		for j := range ws {
			ws[j].Bone += boneBase
		}
		m.addVertex(p.Add(offset), ws)
	}
	for _, b := range other.Bones {
		b.Name = m.uniqueBoneName(b.Name)
		if b.Parent >= 0 {
			b.Parent += boneBase
		}
		b.Head = b.Head.Add(offset)
		b.Tail = b.Tail.Add(offset)
		m.Bones = append(m.Bones, b)
	}
	// The clips come across with the bones they drive. Their tracks index the
	// bone array, so they need the same boneBase shift the weights and parents
	// just got — see the invariants in model_anim.go's header.
	for _, c := range other.Animations {
		c = c.clone()
		c.Name = m.uniqueClipName(c.Name)
		for ti := range c.Tracks {
			c.Tracks[ti].Bone += boneBase
		}
		m.Animations = append(m.Animations, c)
	}
	added := make([]int, 0, len(other.Faces))
	for i := range other.Faces {
		f := other.Faces[i].clone()
		for j := range f.Verts {
			f.Verts[j] += base
		}
		added = append(added, len(m.Faces))
		m.Faces = append(m.Faces, f)
	}
	m.invalidate()
	return added
}

// ModelPrimitiveSegments is the radial resolution the panel's primitive palette
// uses for round shapes.
var ModelPrimitiveSegments = 16
