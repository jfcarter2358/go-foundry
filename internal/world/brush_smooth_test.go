package world

import (
	"math"
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/geom"
	"foundry/internal/gfx"
)

// prismBrush is an n-sided prism of radius 1 standing on y = 0, two units
// tall, every face wound outward.
func prismBrush(n int) Brush {
	var verts []mgl32.Vec3
	for _, y := range []float32{0, 2} {
		for i := 0; i < n; i++ {
			a := 2 * math.Pi * float64(i) / float64(n)
			verts = append(verts, mgl32.Vec3{float32(math.Cos(a)), y, float32(math.Sin(a))})
		}
	}
	centre := mgl32.Vec3{0, 1, 0}
	outward := func(idx []int) []int {
		p := func(k int) mgl32.Vec3 { return verts[idx[k]] }
		nrm := p(1).Sub(p(0)).Cross(p(2).Sub(p(0)))
		if nrm.Dot(p(0).Sub(centre)) < 0 {
			slices.Reverse(idx)
		}
		return idx
	}
	var faces []BrushFace
	var bottom, top []int
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		faces = append(faces, NewBrushFace(outward([]int{i, j, n + j, n + i}), ""))
		bottom, top = append(bottom, i), append(top, n+i)
	}
	faces = append(faces, NewBrushFace(outward(bottom), ""), NewBrushFace(outward(top), ""))
	return Brush{ID: "prism", BrushMesh: BrushMesh{Verts: verts, Faces: faces}, Solid: true}
}

func faceNormal(m *BrushMesh, f *BrushFace) mgl32.Vec3 {
	pts := m.FacePoints(f)
	return geom.TriangleNormal(pts[0], pts[1], pts[2]).Normalize()
}

// The point of welding across the brush: a side of a faceted cylinder takes
// its corners' normals from its neighbours too, so the corners lean round the
// curve instead of repeating the flat side's own plane.
func TestAPrismsSidesSmoothIntoEachOther(t *testing.T) {
	b := prismBrush(8)
	corners := b.CornerNormals()
	side := &b.Faces[0]
	own := faceNormal(&b.BrushMesh, side)
	for i, n := range corners[0] {
		if d := n.Dot(own); d > 0.999 || d < 0.9 {
			t.Errorf("corner %d of a side: normal %v is %.4f along the side's own %v; want it leaning by half a side (cos 22.5° ≈ 0.924)",
				i, n, d, own)
		}
		if math.Abs(float64(n.Y())) > 1e-4 {
			t.Errorf("corner %d of a side took %v — the caps are past the crease and must not tilt it", i, n)
		}
	}
	// The caps are a crease against every side (90°) and stay flat.
	for _, fi := range []int{8, 9} {
		own := faceNormal(&b.BrushMesh, &b.Faces[fi])
		for i, n := range corners[fi] {
			if n.Dot(own) < 0.9999 {
				t.Errorf("cap %d corner %d: %v, want its own plane %v", fi, i, n, own)
			}
		}
	}
}

// A box's edges are all 90° creases, so smoothing it changes nothing — which
// is what makes turning Smooth Lighting on for every surface safe.
func TestABoxStaysFlat(t *testing.T) {
	b := NewBoxBrush("box", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	corners := b.CornerNormals()
	for fi := range b.Faces {
		own := faceNormal(&b.BrushMesh, &b.Faces[fi])
		for i, n := range corners[fi] {
			if n.Dot(own) < 0.9999 {
				t.Errorf("box face %d corner %d: %v, want its own plane %v", fi, i, n, own)
			}
		}
	}
}

// A face's crease angle is its own: a side with a tight one keeps its facet
// even though its neighbours would have blended into it.
func TestEachFaceKeepsItsOwnCreaseAngle(t *testing.T) {
	b := prismBrush(8)
	b.Faces[0].SmoothAngle = 10 // tighter than the 45° between sides
	corners := b.CornerNormals()
	own := faceNormal(&b.BrushMesh, &b.Faces[0])
	for i, n := range corners[0] {
		if n.Dot(own) < 0.9999 {
			t.Errorf("corner %d of the 10° side smoothed to %v, want its own plane %v", i, n, own)
		}
	}
}

// Load hands the welded normals to every drawn face's geometry, whether or not
// the face is marked Smooth: the player's setting decides whether they are
// used, and it can only use what was built.
func TestLoadGivesEveryFaceItsWeldedNormals(t *testing.T) {
	was := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	t.Cleanup(func() { gfx.GeometryUploadsEnabled = was })

	b := prismBrush(8)
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	g := &b.Faces[0].Geometry
	if len(g.Normals) != len(g.Vertices)/5*3 {
		t.Fatalf("side has %d normal floats for %d vertices", len(g.Normals), len(g.Vertices)/5)
	}
	own := faceNormal(&b.BrushMesh, &b.Faces[0])
	n := mgl32.Vec3{g.Normals[0], g.Normals[1], g.Normals[2]}
	if n.Dot(own) > 0.999 {
		t.Errorf("the loaded side's first normal is its own plane %v; Load did not weld across the brush", n)
	}

	gfx.SmoothLighting = true
	t.Cleanup(func() { gfx.SmoothLighting = true })
	if !g.ShadesSmooth() {
		t.Error("an unticked face does not shade smooth with Smooth Lighting on")
	}
	gfx.SmoothLighting = false
	if g.ShadesSmooth() {
		t.Error("an unticked face still shades smooth with Smooth Lighting off")
	}
	b.Faces[0].Smooth = true
	_ = b.Load()
	if !b.Faces[0].Geometry.ShadesSmooth() {
		t.Error("a face marked Smooth stopped shading smooth with Smooth Lighting off")
	}
}
