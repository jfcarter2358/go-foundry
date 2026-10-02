//go:build !nogl

package render

// GL tests for Phase 2's LOD selection (nanite_cull.go, "Level of detail";
// docs/nanite/PHASE_2.md): the cut the GPU draws has no cracks, stays close
// to the full detail at the default threshold, costs by the mesh's size on
// screen, is the same with and without the tree walk, and is the cut the
// shadows draw too.

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// lodProbe is naniteProbe with LOD selection on and the frame's LOD
// parameters taken for the test target's height, at threshold tau pixels.
func lodProbe(t *testing.T) {
	t.Helper()
	naniteProbe(t)
	NaniteLOD = true
	setLODThreshold(t, 1)
}

// setLODThreshold fixes the LOD parameters for the test target and the
// camera as it stands, at tau pixels.
func setLODThreshold(t *testing.T, tau float32) {
	t.Helper()
	prev := gfx.NaniteLODError
	gfx.NaniteLODError = tau
	gfx.UpdateCamMatrix()
	naniteLODFrame.view = naniteLODView{eye: gfx.Cam.Position, k: naniteTestSize * gfx.Projection[5] / 2 / tau}
	naniteLODFrame.set = true
	t.Cleanup(func() {
		gfx.NaniteLODError = prev
		naniteLODFrame.set = false
	})
}

// cookRock cooks the sample rock at subdivision level into a temp dir.
func cookRock(t *testing.T, level int) string {
	t.Helper()
	dir := t.TempDir()
	glb, err := model.EncodeGLB(nanitecook.SampleRock(level, 0.4))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "rock.glb")
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := nanitecook.CookFile(src, "", true, nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mesh.Levels < 3 {
		t.Fatalf("the cooked rock has only %d LOD levels", res.Mesh.Levels)
	}
	return res.Out
}

// drawNanite draws the level's cooked placements, the GPU cull only, and
// returns the picture and the triangles drawn.
func drawNanite(t *testing.T, lvl *world.Level, twoPass bool) ([]uint8, int) {
	t.Helper()
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.Disable(gl.CULL_FACE)
	gl.UseProgram(gfx.Program)
	gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &gfx.Projection[0])
	gfx.UpdateCamMatrix()
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	frustum := ExtractFrustum(projView)
	naniteTwoPass = twoPass
	n := primitivesDrawn(t, func() {
		RenderNaniteScenery(lvl, frustum, projView)
		naniteOcclusionPass(lvl)
	})
	return readColour(), n
}

func isBackground(px []uint8, i int) bool {
	return px[4*i] == 0 && px[4*i+1] == 255 && px[4*i+2] == 0
}

// lodCompare measures got against the full-detail want, as a person would
// see a pop: both pictures box-filtered to blocks of lodBlock pixels (so a
// one-pixel shimmer of an aliased silhouette or a sub-pixel bump does not
// count, as it would not between two frames of the full detail either), and
// a block is OFF when a channel differs by more than 24/255. covered counts
// the blocks with any of the mesh in them.
func lodCompare(want, got []uint8) (off, covered int) {
	n := naniteTestSize
	for by := 0; by+lodBlock <= n; by += lodBlock {
		for bx := 0; bx+lodBlock <= n; bx += lodBlock {
			var a, b [3]int
			any := false
			for y := by; y < by+lodBlock; y++ {
				for x := bx; x < bx+lodBlock; x++ {
					i := y*n + x
					if !isBackground(want, i) || !isBackground(got, i) {
						any = true
					}
					for c := 0; c < 3; c++ {
						a[c] += int(want[4*i+c])
						b[c] += int(got[4*i+c])
					}
				}
			}
			if !any {
				continue
			}
			covered++
			for c := 0; c < 3; c++ {
				if d := (a[c] - b[c]) / (lodBlock * lodBlock); d > 24 || d < -24 {
					off++
					break
				}
			}
		}
	}
	return
}

const lodBlock = 4

// cracksOf counts the holes in the cooked placements' surface as drawn: the
// pixels where a back face shows with no front face in front of it. Every
// ray into a closed solid meets a front face first, so on the sample rock a
// back face drawn where no front face is drawn is a hole — whatever the
// silhouette does as the cut coarsens, which moves both alike.
func cracksOf(t *testing.T, lvl *world.Level) int {
	t.Helper()
	prevCone := NaniteCone
	NaniteCone = false // the cone would drop the back faces
	defer func() { NaniteCone = prevCone }()
	gl.Enable(gl.CULL_FACE)
	defer gl.Disable(gl.CULL_FACE)
	coverage := func(face uint32) []bool {
		gl.CullFace(face)
		gl.ClearColor(0, 1, 0, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		gl.UseProgram(gfx.Program)
		projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
		frustum := ExtractFrustum(projView)
		naniteTwoPass = false
		RenderNaniteScenery(lvl, frustum, projView)
		px := readColour()
		out := make([]bool, naniteTestSize*naniteTestSize)
		for i := range out {
			out[i] = !isBackground(px, i)
		}
		return out
	}
	front := coverage(gl.BACK)
	back := coverage(gl.FRONT)
	gl.CullFace(gl.BACK)
	holes := 0
	for i := range front {
		if back[i] && !front[i] {
			holes++
		}
	}
	return holes
}

// A field of rocks from near to far, at the default threshold and at the
// coarsest the setting allows: the cut is never cracked, at 1 px it barely
// differs from the full detail, and it draws far fewer triangles.
func TestNaniteLODCutHasNoCracksAndStaysCloseToFullDetail(t *testing.T) {
	lodProbe(t)
	rock := cookRock(t, 5) // 20,480 triangles
	var poses []mgl32.Vec3
	for i := 0; i < 10; i++ {
		a := float64(i) * 0.9
		z := -float32(i*i) * 0.6
		r := 0.4 + float32(i)*0.35
		poses = append(poses, mgl32.Vec3{float32(math.Cos(a)) * r, float32(math.Sin(a)) * r * 0.8, z})
	}
	lvl := naniteSceneryLevel(t, rock, true, poses, 0.5)

	NaniteLOD = false
	ref, full := drawNanite(t, lvl, false)
	baseHoles := cracksOf(t, lvl)
	NaniteLOD = true
	for _, tau := range []float32{1, 4, gfx.NaniteLODErrorMax} {
		setLODThreshold(t, tau)
		if holes := cracksOf(t, lvl); holes > baseHoles {
			t.Errorf("tau %g: %d pixels show a back face through a hole (the full detail: %d)", tau, holes, baseHoles)
		}
		for _, twoPass := range []bool{false, true} {
			for frame := 0; frame < 2; frame++ {
				got, n := drawNanite(t, lvl, twoPass)
				off, covered := lodCompare(ref, got)
				if covered < 100 {
					t.Fatalf("only %d blocks of rock drawn", covered)
				}
				if n >= full {
					t.Errorf("tau %g: the cut drew %d triangles, the full detail %d", tau, n, full)
				}
				if tau == 1 && off > 1+covered/50 {
					t.Errorf("tau 1: %d of %d blocks are off by more than 24/255", off, covered)
				}
				if frame == 0 && !twoPass {
					t.Logf("tau %g: %d of %d triangles; %d of %d blocks off by > 24/255", tau, n, full, off, covered)
				}
			}
		}
	}
}

// One rock, walked away from: the triangles drawn fall with its size on
// screen. The tree walk changes none of it.
func TestNaniteLODCostsByScreenSize(t *testing.T) {
	lodProbe(t)
	rock := cookRock(t, 6) // 81,920 triangles
	lvl := naniteSceneryLevel(t, rock, true, []mgl32.Vec3{{0, 0, 0}}, 1)
	base := lvl.Scenery[0].Nanite.TriangleCount()
	eye := gfx.Cam.Position
	defer func() { gfx.Cam.Position = eye }()
	var counts []int
	for _, d := range []float32{2.5, 10, 40, 90} {
		gfx.Cam.Position = mgl32.Vec3{0, 0, d}
		setLODThreshold(t, 1)
		pic, n := drawNanite(t, lvl, false)
		naniteLODWalk = false
		picFlat, nFlat := drawNanite(t, lvl, false)
		naniteLODWalk = true
		if n != nFlat {
			t.Errorf("at %g m the tree walk drew %d triangles, the flat test %d", d, n, nFlat)
		}
		for i := range pic {
			if pic[i] != picFlat[i] {
				t.Errorf("at %g m the tree walk's picture differs from the flat test's", d)
				break
			}
		}
		t.Logf("at %g m: %d of %d triangles", d, n, base)
		counts = append(counts, n)
	}
	for i := 1; i < len(counts); i++ {
		if counts[i] >= counts[i-1] {
			t.Errorf("cost does not fall with distance: %v", counts)
		}
	}
	// Four times as far is a sixteenth of the screen area; the cut should
	// fall by well over four times between the middle distances, and the
	// farthest — the rock a few pixels tall — should be a sliver of the full
	// mesh. (Not less: the sample rock has two material slots, and where
	// they meet is locked at every level, so its root is a few hundred
	// triangles.)
	if counts[1] < 4*counts[2] {
		t.Errorf("10 m to 40 m only took the cut from %d to %d triangles", counts[1], counts[2])
	}
	if counts[3]*25 > base {
		t.Errorf("at 90 m the rock still draws %d of %d triangles", counts[3], base)
	}
}

// The shadow passes draw the camera's cut: with the camera far off, a
// cascade holding the rocks draws the coarse cut, not the full detail.
func TestNaniteLODShadowDrawsTheCamerasCut(t *testing.T) {
	lodProbe(t)
	rock := cookRock(t, 5)
	var poses []mgl32.Vec3
	for i := 0; i < 6; i++ {
		poses = append(poses, mgl32.Vec3{float32(i-3) * 0.8, 0, -1})
	}
	lvl := naniteSceneryLevel(t, rock, true, poses, 0.4)
	total := lvl.Scenery[0].Nanite.TriangleCount() * len(poses)
	centre := mgl32.Vec3{0, 0, -1}
	lsm := mgl32.Ortho(-4, 4, -4, 4, 0.1, 30).Mul4(
		mgl32.LookAtV(centre.Add(mgl32.Vec3{3, 5, 4}.Normalize().Mul(12)), centre, mgl32.Vec3{0, 1, 0}))
	shadow := func() int {
		cull := ExtractFrustum(lsm)
		identity := mgl32.Ident4()
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		return primitivesDrawn(t, func() { renderNaniteSceneryShadow(lvl, Shadows.Program, &cull) })
	}
	NaniteShadowCone = false
	eye := gfx.Cam.Position
	defer func() { gfx.Cam.Position = eye }()

	NaniteLOD = false
	if n := shadow(); n != total {
		t.Fatalf("LOD off: the shadow drew %d triangles, want every rock's %d", n, total)
	}
	NaniteLOD = true
	gfx.Cam.Position = mgl32.Vec3{0, 0, 3}
	setLODThreshold(t, 1)
	near := shadow()
	gfx.Cam.Position = mgl32.Vec3{0, 0, 80}
	setLODThreshold(t, 1)
	far := shadow()
	t.Logf("shadow triangles: camera near %d, far %d, full detail %d", near, far, total)
	if !(far < near && near <= total) {
		t.Errorf("the shadow did not follow the camera's cut: near %d, far %d, full %d", near, far, total)
	}
	if far*10 > total {
		t.Errorf("with the camera 80 m off the shadow still drew %d of %d triangles", far, total)
	}
}

// Popping: walking the camera from near to far in small steps, every frame's
// cut stays within the threshold of the full detail at that same spot — so no
// step between two frames can jump by more than that either.
func TestNaniteLODDoesNotPopAsTheCameraMoves(t *testing.T) {
	lodProbe(t)
	rock := cookRock(t, 5)
	lvl := naniteSceneryLevel(t, rock, true, []mgl32.Vec3{{0, 0, 0}, {0.9, 0.3, -1.5}}, 0.6)
	eye := gfx.Cam.Position
	defer func() { gfx.Cam.Position = eye }()
	worst := 0.0
	for step := 0; step < 24; step++ {
		gfx.Cam.Position = mgl32.Vec3{0.1, 0.05, 2.2 * float32(math.Pow(1.18, float64(step)))}
		setLODThreshold(t, 1)
		NaniteLOD = false
		ref, _ := drawNanite(t, lvl, false)
		NaniteLOD = true
		got, _ := drawNanite(t, lvl, false)
		off, covered := lodCompare(ref, got)
		if holes := cracksOf(t, lvl); holes > 0 {
			t.Errorf("step %d: %d pixels show a back face through a hole", step, holes)
		}
		if covered < 6 {
			break
		}
		frac := float64(off) / float64(covered)
		worst = max(worst, frac)
		// Two per cent, and one block whatever the size: at a few blocks
		// across, one block on the silhouette is the aliasing of the full
		// detail itself.
		if off > 1+covered/50 {
			t.Errorf("step %d (%.1f m): %d of %d blocks off by > 24/255", step, gfx.Cam.Position[2], off, covered)
		}
	}
	t.Logf("worst frame: %.2f%% of covered blocks off by > 24/255", 100*worst)
}

// The debug views: lod paints the full detail white and coarser levels in
// colour, and a far rock in more colour than a near one; clusters gives the
// clusters many colours; overdraw adds up.
func TestNaniteDebugViews(t *testing.T) {
	lodProbe(t)
	rock := cookRock(t, 5)
	lvl := naniteSceneryLevel(t, rock, true, []mgl32.Vec3{{0, 0, 0}}, 1)
	defer SetView("normal")
	eye := gfx.Cam.Position
	defer func() { gfx.Cam.Position = eye }()

	colours := func(px []uint8) (white, coloured, distinct int) {
		seen := map[[3]uint8]bool{}
		for i := 0; i < len(px)/4; i++ {
			if isBackground(px, i) {
				continue
			}
			c := [3]uint8{px[4*i], px[4*i+1], px[4*i+2]}
			seen[[3]uint8{c[0] &^ 15, c[1] &^ 15, c[2] &^ 15}] = true
			lo, hi := min(c[0], c[1], c[2]), max(c[0], c[1], c[2])
			if hi-lo < 12 {
				white++
			} else {
				coloured++
			}
		}
		return white, coloured, len(seen)
	}
	if _, err := SetView("lod"); err != nil {
		t.Fatal(err)
	}
	NaniteLOD = false
	gfx.Cam.Position = mgl32.Vec3{0, 0, 4}
	setLODThreshold(t, 1)
	px, _ := drawNanite(t, lvl, false)
	w, c, _ := colours(px)
	if w < 500 || c > w/50 {
		t.Errorf("lod view, LOD off: %d white (level 0) and %d coloured pixels; want all level 0", w, c)
	}
	NaniteLOD = true
	gfx.Cam.Position = mgl32.Vec3{0, 0, 12}
	setLODThreshold(t, 1)
	px, _ = drawNanite(t, lvl, false)
	w, c, _ = colours(px)
	if c < 50 || c < w {
		t.Errorf("lod view at 12 m: %d white and %d coloured pixels; want mostly coarser levels", w, c)
	}

	if _, err := SetView("clusters"); err != nil {
		t.Fatal(err)
	}
	gfx.Cam.Position = mgl32.Vec3{0, 0, 4}
	setLODThreshold(t, 1)
	px, _ = drawNanite(t, lvl, true)
	if _, _, d := colours(px); d < 20 {
		t.Errorf("clusters view: only %d distinct colours", d)
	}

	if _, err := SetView("overdraw"); err != nil {
		t.Fatal(err)
	}
	px, _ = drawNanite(t, lvl, false)
	maxR := 0
	for i := 0; i < len(px)/4; i++ {
		if !isBackground(px, i) {
			maxR = max(maxR, int(px[4*i]))
		}
	}
	// The clear is green; a rock's front and back each add 0.08 red.
	if maxR < 30 {
		t.Errorf("overdraw view: the brightest pixel has red %d; want two layers or more added up", maxR)
	}
	SetView("normal")
	if NaniteDebug != naniteDebugOff {
		t.Error("the normal view left a Nanite debug view on")
	}
}

// litGridWall is a compiled wall the way the compiler leaves one: a grid of
// n x n faces, each its own surface with its own corners, at z = 0 spanning
// ±half, facing +Z, with UVs on the world grid and light baked per corner —
// a smooth swell of light and dark, and a hard-edged shadow across its lower
// left — so corners at one point carry the same values and weld.
func litGridWall(t *testing.T, n int, half float32) *world.Level {
	t.Helper()
	light := func(x, y float32) []float32 {
		l := 0.45 + 0.3*float32(math.Sin(float64(1.3*x))*math.Cos(float64(0.9*y)))
		if x < -half/3 && y < 0 {
			l *= 0.3 // the shadow
		}
		return []float32{l, l * 0.9, l * 0.8}
	}
	s := &world.StaticGeometry{}
	step := 2 * half / float32(n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			x0, y0 := -half+float32(i)*step, -half+float32(j)*step
			x1, y1 := -half+float32(i+1)*step, -half+float32(j+1)*step
			verts := [][]float32{{x0, y0, 0, x0, y0}, {x1, y0, 0, x1, y0}, {x1, y1, 0, x1, y1}, {x0, y1, 0, x0, y1}}
			var g gfx.Geometry
			g.CPUOnly = true
			g.VertexLight = [][]float32{light(x0, y0), light(x1, y0), light(x1, y1), light(x0, y1)}
			if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, "concrete", mgl32.Vec3{}); err != nil {
				t.Fatal(err)
			}
			g.Alpha = 1
			s.Surfaces = append(s.Surfaces, world.StaticSurface{Geometry: g, Chunk: "wall", Normal: mgl32.Vec3{0, 0, 1},
				Lo: mgl32.Vec3{x0, y0, 0}, Hi: mgl32.Vec3{x1, y1, 0}})
		}
	}
	// As the compiler stores it: the DAG built ahead of the load — whatever
	// FOUNDRY_NANITE_LOD_STATIC says.
	prev := world.StaticLOD
	world.StaticLOD = true
	s.SetClusters(world.BuildStaticClusters(s))
	world.StaticLOD = prev
	if cg := s.Clusters; len(cg) != 1 || cg[0].DAG == nil || cg[0].DAG.Levels < 3 {
		t.Fatalf("the wall's group got no DAG worth the name")
	}
	lvl := &world.Level{Static: s}
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	return lvl
}

// A compiled grid wall with a DAG: the LOD cut keeps every pixel of the wall
// covered, keeps the light — gradient and shadow edge — close to the full
// detail, and draws fewer triangles the further off it is.
func TestNaniteStaticLODKeepsTheWallWholeAndItsLight(t *testing.T) {
	lodProbe(t)
	lvl := litGridWall(t, 48, 4) // 4,608 triangles
	eye := gfx.Cam.Position
	defer func() { gfx.Cam.Position = eye }()
	draw := func() ([]uint8, int) {
		var px []uint8
		n := primitivesDrawn(t, func() { px = drawStaticFrame(lvl, true) })
		return px, n
	}
	prev := 1 << 30
	for _, d := range []float32{6, 14, 30} {
		gfx.Cam.Position = mgl32.Vec3{0.3, 0.2, d}
		setLODThreshold(t, 1)
		NaniteLOD = false
		ref, full := draw()
		NaniteLOD = true
		got, n := draw()
		holes := 0
		for i := 0; i < naniteTestSize*naniteTestSize; i++ {
			if !isBackground(ref, i) && isBackground(got, i) {
				holes++
			}
		}
		off, covered := lodCompare(ref, got)
		t.Logf("at %g m: %d of %d triangles; %d holes; %d of %d blocks off by > 24/255", d, n, full, holes, off, covered)
		if holes > 0 {
			t.Errorf("at %g m: %d pixels of the wall opened up", d, holes)
		}
		if off > 1+covered/50 {
			t.Errorf("at %g m: %d of %d blocks off by more than 24/255", d, off, covered)
		}
		if n >= full || n > prev {
			t.Errorf("at %g m: %d triangles (full detail %d, nearer %d)", d, n, full, prev)
		}
		prev = n
	}
}

// The light is what keeps the wall's triangles: built with the baked light
// weighed at nothing, the flat wall simplifies to a handful of triangles at
// no geometric error, and the swell and the shadow smear across it.
func TestNaniteStaticLODWeighsTheBakedLight(t *testing.T) {
	lodProbe(t)
	eye := gfx.Cam.Position
	defer func() { gfx.Cam.Position = eye }()
	gfx.Cam.Position = mgl32.Vec3{0.3, 0.2, 9}
	setLODThreshold(t, 1)
	measure := func(lightWeight float32) (int, int, int) {
		prev := world.StaticLODWeights.Light
		world.StaticLODWeights.Light = lightWeight
		defer func() { world.StaticLODWeights.Light = prev }()
		lvl := litGridWall(t, 48, 4)
		NaniteLOD = false
		ref := drawStaticFrame(lvl, false)
		NaniteLOD = true
		var got []uint8
		n := primitivesDrawn(t, func() { got = drawStaticFrame(lvl, false) })
		off, covered := lodCompare(ref, got)
		return n, off, covered
	}
	nW, offW, covered := measure(world.StaticLODWeights.Light)
	nZ, offZ, _ := measure(0)
	t.Logf("light weighed: %d triangles, %d of %d blocks off; light ignored: %d triangles, %d blocks off", nW, offW, covered, nZ, offZ)
	if offW > 1+covered/50 {
		t.Errorf("with the light weighed, %d of %d blocks are off", offW, covered)
	}
	if offZ <= offW+covered/20 || nZ >= nW {
		t.Errorf("ignoring the light changed too little (%d triangles, %d blocks off): the test proves nothing", nZ, offZ)
	}
}
