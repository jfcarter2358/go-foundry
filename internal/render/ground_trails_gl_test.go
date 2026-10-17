package render

// ground_trails_gl_test.go — the trail map's update (ground_trails.go,
// ground_trail_comp.glsl): a stamp presses a print with a rim, the two
// channels fill back in at their own rates, and the toroidal window clears
// the ground it scrolls over.
//
//	FOUNDRY_GL_TEST=1 go test -run GroundTrail ./internal/render/

import (
	"math"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// trailAt reads the trail map's two channels at world (x, z).
func trailAt(t *testing.T, x, z float32) (fast, slow float32) {
	t.Helper()
	pix := make([]float32, 2*TrailSize*TrailSize)
	gl.BindTexture(gl.TEXTURE_2D, groundTrail.tex)
	gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RG, gl.FLOAT, gl.Ptr(pix))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	cx := int(math.Floor(float64(x / trailTexel)))
	cz := int(math.Floor(float64(z / trailTexel)))
	tx := ((cx % TrailSize) + TrailSize) % TrailSize
	tz := ((cz % TrailSize) + TrailSize) % TrailSize
	o := 2 * (tz*TrailSize + tx)
	return pix[o], pix[o+1]
}

func TestGroundTrailStampsDecayAndScroll(t *testing.T) {
	gltest.Context(t)
	prevRes := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prevRes })
	groundTrail = groundTrailPass{}
	world.DrainTrailStamps(nil)
	t.Cleanup(func() {
		if groundTrail.tex != 0 {
			gl.DeleteTextures(1, &groundTrail.tex)
		}
		groundTrail = groundTrailPass{}
		gfx.GroundTrail.On = false
	})
	lvl := &world.Level{}
	world.TrailFocus = mgl32.Vec3{10, 0, 10}

	// A boot print at (10, 10), along +Z.
	world.PushTrailStamp(world.TrailStamp{A: mgl32.Vec2{10, 9.925}, B: mgl32.Vec2{10, 10.075},
		Radius: 0.06, Strength: 1, Rim: 0.35})
	now := 1.0
	groundTrailsUpdate(lvl, now)
	if !groundTrail.ok {
		t.Fatal("the trail update did not build")
	}
	if !gfx.GroundTrail.On || gfx.GroundTrail.Span != TrailSpan {
		t.Fatalf("the trail map is not published: %+v", gfx.GroundTrail)
	}
	if m := gfx.GroundTrail.Min; 10 < m[0] || 10 > m[0]+TrailSpan || 10 < m[1] || 10 > m[1]+TrailSpan {
		t.Fatalf("the window %v does not hold the focus", m)
	}

	if f, s := trailAt(t, 10, 10); f < 0.95 || s < 0.95 {
		t.Errorf("the print's middle is pressed %v, %v; want the stamp's full strength", f, s)
	}
	if f, _ := trailAt(t, 10.1, 10); f >= 0 {
		t.Errorf("beside the print (on its rim) the ground is %v, want raised (negative)", f)
	}
	if f, s := trailAt(t, 12, 10); f != 0 || s != 0 {
		t.Errorf("two metres away the ground is %v, %v; want untouched", f, s)
	}

	// A frame that only stamps updates only the cells its stamps reach:
	// the second print lands, the first is left as it was.
	world.PushTrailStamp(world.TrailStamp{A: mgl32.Vec2{11, 9.925}, B: mgl32.Vec2{11, 10.075},
		Radius: 0.06, Strength: 0.5, Rim: 0.35})
	now += 0.01
	groundTrailsUpdate(lvl, now)
	if f, _ := trailAt(t, 11, 10); f < 0.45 || f > 0.55 {
		t.Errorf("a print stamped on a frame with nothing else to do is %v deep, want its 0.5", f)
	}
	if f, _ := trailAt(t, 10, 10); f < 0.95 {
		t.Errorf("the first print changed to %v when only the second was stamped", f)
	}

	// Forty-five seconds: the fast channel to about e^-1, the slow one
	// barely moved.
	for i := 0; i < 180; i++ {
		now += 0.25
		groundTrailsUpdate(lvl, now)
	}
	f, s := trailAt(t, 10, 10)
	if f < 0.3 || f > 0.45 {
		t.Errorf("after 45 s the fast channel is %v, want about %v", f, math.Exp(-1))
	}
	if s < 0.95 {
		t.Errorf("after 45 s the slow channel is %v, want nearly all of it", s)
	}

	// Walk 30 m east: (10, 10) leaves the window and its texels are reused
	// for the ground coming in. Walk back: the print is gone, not wrapped
	// round to reappear.
	world.TrailFocus = mgl32.Vec3{40, 0, 10}
	now += 0.1
	groundTrailsUpdate(lvl, now)
	world.TrailFocus = mgl32.Vec3{10, 0, 10}
	now += 0.1
	groundTrailsUpdate(lvl, now)
	if f, s := trailAt(t, 10, 10); f != 0 || s != 0 {
		t.Errorf("a print scrolled out of the window and back is %v, %v; want cleared", f, s)
	}
}
