package render

// viewmodel_gl_test.go — the lens the first-person viewmodel is drawn through.
//
// Two things about that lens are load-bearing and neither is visible from Go
// alone, because both are about what the rasteriser does with a vertex:
//
//   - its near plane is ten times nearer than the world's, so a hand held at
//     arm's length is not sliced open by it;
//   - it is the viewmodel's OWN field of view, so aiming down a scope — which
//     narrows gfx.FieldOfView to a third of normal — does not magnify the arms
//     along with the target.
//
// And one thing about the pass: it must put the world's projection back, or
// every pass after it in the frame draws through a lens meant for a pair of
// hands.
//
//	FOUNDRY_GL_TEST=1 go test -run TestViewmodel ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// viewmodelProbe stands the player in front of the camera holding one quad,
// and draws it exactly the way a frame does.
type viewmodelProbe struct {
	t    *testing.T
	quad gfx.Geometry
}

func newViewmodelProbe(t *testing.T) *viewmodelProbe {
	t.Helper()
	p := &viewmodelProbe{t: t}
	newNormalProbe(t) // gltest context, the world program, its uniforms, lights and shadow samplers

	prevWin, prevHigh := platform.WinWidth, platform.WinHeight
	prevFOV, prevVM := gfx.FieldOfView, gfx.ViewmodelFieldOfView
	prevPlayer := world.PlayerEntity
	t.Cleanup(func() {
		platform.WinWidth, platform.WinHeight = prevWin, prevHigh
		gfx.FieldOfView, gfx.ViewmodelFieldOfView = prevFOV, prevVM
		world.PlayerEntity = prevPlayer
		p.quad.Delete()
	})
	platform.WinWidth, platform.WinHeight = skyTestSize, skyTestSize
	gfx.FieldOfView, gfx.ViewmodelFieldOfView = gfx.DefaultFieldOfView, gfx.DefaultViewmodelFieldOfView

	// The camera at the origin looking down −Z, and its matrix uploaded: the
	// viewmodel's base transform is that matrix inverted.
	gfx.Cam.Position = mgl32.Vec3{0, 0, 0}
	gfx.Cam.Direction = mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()
	gfx.RebuildProjection()

	// A small quad, standing in for a gun: an attachment with no parent, so
	// its offset is measured straight from the eye.
	const h = 0.02
	verts := [][]float32{
		{-h, -h, 0, 0, 0}, {h, -h, 0, 1, 0}, {h, h, 0, 1, 1}, {-h, h, 0, 0, 1},
	}
	p.quad.Alpha = 1
	if err := p.quad.Load(verts, []int{0, 1, 2, 0, 2, 3}, "", mgl32.Vec3{}); err != nil {
		t.Fatalf("upload viewmodel quad: %v", err)
	}

	e := &world.Entity{ID: "player", Visible: true, Scale: mgl32.Vec3{1, 1, 1}}
	e.Models = []world.EntityModel{{
		Name: "gun", Visible: true, Alpha: 1, Scale: mgl32.Vec3{1, 1, 1},
		Submeshes: []gfx.Geometry{p.quad},
	}}
	world.PlayerEntity = e
	return p
}

// draw puts the viewmodel quad at that offset from the eye, renders the pass,
// and reports the centre pixel and how wide the quad came out in pixels.
func (p *viewmodelProbe) draw(offset mgl32.Vec3) (px [4]uint8, widthPx int) {
	p.t.Helper()
	world.PlayerEntity.Models[0].Offset = offset

	gl.Disable(gl.CULL_FACE)
	gl.ClearColor(0, 1, 0, 1) // a colour the shader cannot produce
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	RenderViewmodel()

	row := make([]uint8, 4*skyTestSize)
	glext.ReadPixels(0, skyTestSize/2, skyTestSize, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&row[0]))
	for i := 0; i < skyTestSize; i++ {
		// Anything that is not the clear colour is the quad.
		if !(row[4*i] == 0 && row[4*i+1] == 255 && row[4*i+2] == 0) {
			widthPx++
		}
	}
	copy(px[:], row[4*(skyTestSize/2):])
	return px, widthPx
}

func drawn(px [4]uint8) bool { return !(px[0] == 0 && px[1] == 255 && px[2] == 0) }

// A viewmodel five centimetres from the eye is drawn. The world's own near
// plane is at ten centimetres, so through the world's lens this quad does not
// exist — which is the artifact this pass's own near plane is here to stop.
func TestViewmodelDrawsInsideTheWorldsNearPlane(t *testing.T) {
	p := newViewmodelProbe(t)

	near, _ := p.draw(mgl32.Vec3{0, 0, -0.05})
	if !drawn(near) {
		t.Errorf("a viewmodel 5cm from the eye was not drawn (centre pixel %v): "+
			"the pass is using the world's near plane, and hands held close to the "+
			"camera will be sliced open by it", near)
	}

	// …and the world's lens really would have clipped it, so the test above is
	// about the viewmodel projection rather than about nothing.
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gfx.UploadProjection(gfx.Projection)
	p.quad.DrawWithTransform(gfx.Cam.Matrix.Inv().Mul4(mgl32.Translate3D(0, 0, -0.05)))
	var px [4]uint8
	glext.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	if drawn(px) {
		t.Errorf("the world's own projection drew a quad 5cm from the eye (%v), so this "+
			"test is no longer measuring the viewmodel's near plane", px)
	}
}

// Aiming zooms the world and leaves the hands where they were. Without a lens
// of its own the gun would swell by exactly the zoom factor — which at a
// sniper's field of view is the arms across the whole screen.
func TestViewmodelIgnoresTheWorldsFieldOfView(t *testing.T) {
	p := newViewmodelProbe(t)
	at := mgl32.Vec3{0, 0, -0.3}

	_, hip := p.draw(at)
	if hip == 0 {
		t.Fatal("the viewmodel quad was not drawn at all at the hip")
	}

	// A scope: the world's field of view down to a quarter, which through one
	// shared lens would make everything on screen four times wider.
	gfx.FieldOfView = 12
	gfx.RebuildProjection()
	_, aimed := p.draw(at)
	if aimed != hip {
		t.Errorf("the viewmodel was %d pixels wide at a 45-degree world FOV and %d at 12 degrees: "+
			"it is being drawn through the camera's lens rather than its own", hip, aimed)
	}

	// Its own field of view still moves it, or the knob does nothing.
	gfx.ViewmodelFieldOfView = gfx.DefaultViewmodelFieldOfView / 2
	_, closer := p.draw(at)
	if closer <= hip {
		t.Errorf("halving viewmodel_fov left the viewmodel %d pixels wide, no wider than the %d "+
			"it was: the pass is not reading ViewmodelFieldOfView", closer, hip)
	}
}

// The pass borrows the projection uniform and must give it back: everything
// drawn after it in the frame — the UI's world-space overlays, and the whole
// next frame until something rebuilds — reads that same uniform.
func TestViewmodelRestoresTheWorldProjection(t *testing.T) {
	p := newViewmodelProbe(t)
	gfx.FieldOfView = 70 // something the viewmodel's own 55 cannot be mistaken for
	gfx.RebuildProjection()
	p.draw(mgl32.Vec3{0, 0, -0.3})

	var got [16]float32
	gl.GetUniformfv(gfx.Program, gfx.ProjectionUniform, &got[0])
	for i := range got {
		if d := got[i] - gfx.Projection[i]; math.Abs(float64(d)) > 1e-5 {
			t.Fatalf("after the viewmodel pass the projection uniform is %v, want the world's %v",
				got, gfx.Projection)
		}
	}
}
