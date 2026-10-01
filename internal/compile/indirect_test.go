package compile

// indirect_test.go — the bake's indirect part (Face.Indirect), which live GI
// takes back off a baked surface before adding its own.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func near(a, b mgl32.Vec3) bool {
	d := a.Sub(b)
	return d.Len() < 1e-3
}

// With no bounce, the indirect part is the ambient alone: the whole bake less
// the sun is exactly what AmbientAt gives the face's normal.
func TestIndirectIsTheAmbientWithoutBounce(t *testing.T) {
	withAlbedos(t, map[string]mgl32.Vec3{"red": {0.8, 0.1, 0.1}, "white": {0.9, 0.9, 0.9}})
	lvl := floorAndWall(0, "ground", nil, 0.3)
	tr := bake(t, lvl)
	checked := 0
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if f.Light == nil {
			continue
		}
		if len(f.Indirect) != len(f.Light) {
			t.Fatalf("face %d: %d indirect values for %d lit points", fi, len(f.Indirect), len(f.Light))
		}
		want := lvl.Lights.Global.AmbientAt(f.Plane.N)
		for i := range f.Indirect {
			if !near(f.Indirect[i], want) {
				t.Fatalf("face %d point %d: indirect %v, want the ambient %v", fi, i, f.Indirect[i], want)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("nothing was baked")
	}
}

// With bounce, the bake less its indirect part is the same direct light a
// bake with no bounce and no ambient gives: the bounce and the ambient are
// the indirect part, all of it, and nothing of the lights' own.
func TestTheBakeLessItsIndirectIsTheDirectLight(t *testing.T) {
	withAlbedos(t, map[string]mgl32.Vec3{"red": {0.8, 0.1, 0.1}, "white": {0.9, 0.9, 0.9}})
	bounced := bake(t, floorAndWall(2, "ground", nil, 0.3))
	direct := bake(t, floorAndWall(0, "ground", nil, 0))
	if len(bounced.Faces) != len(direct.Faces) {
		t.Fatalf("the two bakes cut the level differently: %d faces against %d", len(bounced.Faces), len(direct.Faces))
	}
	sawBounce := false
	for fi := range bounced.Faces {
		b, d := &bounced.Faces[fi], &direct.Faces[fi]
		for i := range b.Light {
			got := b.Light[i].Sub(b.Indirect[i])
			if !near(got, d.Light[i]) {
				t.Fatalf("face %d point %d: bake less indirect is %v, the direct bake %v", fi, i, got, d.Light[i])
			}
			// The ambient is 0.3 from every side (no hemisphere), so more
			// than that is bounce.
			if b.Indirect[i].X() > 0.31 {
				sawBounce = true
			}
		}
	}
	if !sawBounce {
		t.Error("no face's indirect part carried more than the ambient: the bounce is missing from it")
	}
}
