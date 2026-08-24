package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// cubeFarthestCorner returns the corner furthest from the eye.
//
// On a cube seen from a general direction that corner, and only that corner, is
// hidden: three faces are turned towards the viewer, their outline is a hexagon
// of six corners, and the seventh is the near corner in the middle of it. So
// "hidden" is one vertex out of eight — not the four furthest away, which is
// the tempting and wrong guess, because two of the four sit on the silhouette.
func cubeFarthestCorner(m *ModelDoc) int {
	eye := modelCam.position()
	far, farDist := -1, float32(-1)
	for vi, p := range m.Verts {
		if d := p.Sub(eye).Len(); d > farDist {
			far, farDist = vi, d
		}
	}
	return far
}

func TestSolidShadingHidesTheFarCorner(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelWireframe = false
	modelInvalidateVisibility()

	modelUpdateVisibility()
	if !modelVis.exact {
		t.Fatal("a cube should be well inside the exact-test budget")
	}

	far := cubeFarthestCorner(m)
	hidden := 0
	for vi := range m.Verts {
		if !modelVertexVisible(vi) {
			hidden++
			if vi != far {
				t.Errorf("corner %d is hidden, but only the far corner %d should be", vi, far)
			}
		}
	}
	if hidden != 1 {
		t.Errorf("%d corners hidden, want exactly 1", hidden)
	}
}

func TestWireframeMakesEverythingVisible(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelSetWireframe(true)

	modelUpdateVisibility()
	for vi := range m.Verts {
		if !modelVertexVisible(vi) {
			t.Errorf("vertex %d is hidden in wireframe", vi)
		}
	}
	for _, e := range m.Edges() {
		if !modelEdgeVisible(e) {
			t.Errorf("edge %v is hidden in wireframe", e)
		}
	}
}

// cornerScreenPos is where a vertex lands in the viewport.
func cornerScreenPos(t *testing.T, m *ModelDoc, vi int) (int32, int32) {
	t.Helper()
	w, h, _ := modelViewport()
	sp, ok := projectToScreenIn(modelProjView(), m.Verts[vi], w, h)
	if !ok {
		t.Fatalf("corner %d is off screen", vi)
	}
	return int32(sp.X()), int32(sp.Y())
}

func TestSolidShadingRefusesToPickTheHiddenCorner(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelWireframe = false
	modelInvalidateVisibility()
	modelUpdateVisibility()

	far := cubeFarthestCorner(m)
	mx, my := cornerScreenPos(t, m, far)

	if got, hit := modelPickVertex(mx, my); hit && got == far {
		t.Errorf("clicking straight at hidden corner %d selected it", far)
	}
	// Every corner that is visible must still be reachable, or the filter has
	// simply broken selection.
	for vi := range m.Verts {
		if vi == far {
			continue
		}
		vx, vy := cornerScreenPos(t, m, vi)
		if got, hit := modelPickVertex(vx, vy); !hit || got != vi {
			t.Errorf("visible corner %d picked as (%d, %v)", vi, got, hit)
		}
	}
}

func TestWireframeReachesTheHiddenCorner(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelWireframe = false
	modelInvalidateVisibility()
	modelUpdateVisibility()

	far := cubeFarthestCorner(m)
	mx, my := cornerScreenPos(t, m, far)

	if _, hit := modelPickVertex(mx, my); hit {
		t.Fatalf("corner %d was pickable in solid shading", far)
	}
	modelSetWireframe(true)
	if got, hit := modelPickVertex(mx, my); !hit || got != far {
		t.Errorf("wireframe pick gave (%d, %v), want corner %d", got, hit, far)
	}
}

func TestEdgeIsVisibleWhenEitherEndIs(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelWireframe = false
	modelInvalidateVisibility()
	modelUpdateVisibility()

	// An edge running behind an occluder but emerging at one end is still
	// something the user can see and aim at, so either endpoint being visible
	// is enough. On a cube that means every edge stays reachable: the one
	// hidden corner's edges all run out to the silhouette.
	for _, e := range m.Edges() {
		want := modelVertexVisible(e.A) || modelVertexVisible(e.B)
		if got := modelEdgeVisible(e); got != want {
			t.Errorf("edge %v visible=%v, want %v", e, got, want)
		}
	}
}

func TestVisibilityCacheTracksTheEyeAndTheMesh(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelWireframe = false
	modelInvalidateVisibility()
	modelUpdateVisibility()

	first := append([]bool(nil), modelVis.verts...)

	// Orbiting to the far side must flip which corners are visible.
	modelCam.Yaw += mgl32.DegToRad(180)
	modelUpdateVisibility()
	same := true
	for i := range first {
		if first[i] != modelVis.verts[i] {
			same = false
		}
	}
	if same {
		t.Error("visibility did not change after orbiting to the opposite side")
	}

	// Editing the mesh must be noticed even with the eye where it is.
	before := modelDoc.Rev()
	m.MoveVerts([]int{0}, mgl32.Vec3{0, 0.1, 0})
	if m.Rev() == before {
		t.Error("moving a vertex did not bump the document revision")
	}
	eye := modelCam.position()
	modelUpdateVisibility()
	if modelVis.rev != m.Rev() || modelVis.eye != eye {
		t.Error("the cache did not refresh for the edited mesh")
	}
}

func TestVisibilityFallsBackOnDenseMeshes(t *testing.T) {
	m := NewCubeDoc()
	for i := 0; i < 3; i++ {
		m.CatmullClark()
	}
	withModelDoc(t, m)
	withModelViewport(t)
	modelWireframe = false
	modelInvalidateVisibility()

	modelUpdateVisibility()

	if modelVis.exact {
		t.Fatalf("%d verts should have exceeded the exact-test budget", len(m.Verts))
	}
	// The approximation still has to hide the far side of a closed solid.
	eye := modelCam.position()
	hidden := 0
	for vi := range m.Verts {
		if !modelVertexVisible(vi) {
			hidden++
			if m.Verts[vi].Sub(eye).Len() < 1 {
				t.Errorf("vertex %d right next to the camera was hidden", vi)
			}
		}
	}
	if hidden == 0 {
		t.Error("the fallback reported every vertex on a closed solid visible")
	}
}
