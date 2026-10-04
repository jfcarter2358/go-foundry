package render

// nanite_swraster_gl_test.go — Phase 5 (docs/nanite/PHASE_5.md): the compute
// rasteriser, through DrawWorld on Phase 4's parity scene (visTestLevel: a
// compiled level, a live chunk, cooked rocks, a classic rock in front of
// them, a shadowing sun and point light).
//
//   - The picture with the compute rasteriser drawing the small clusters is
//     the hardware's: the same pixels covered (no holes along shared edges,
//     none showing through what the hardware drew in front), shaded alike.
//   - It did draw them: hundreds of the rocks' clusters a pose.
//   - Its 64-bit form draws the same picture (that test runs only where the
//     driver has 64-bit atomics: natively on NVIDIA, not on Mesa D3D12).
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'SWRaster|Oversized' ./internal/render/

import (
	"fmt"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// swPoses look at the rocks: from the parity test's front, from below the
// classic rock (which then covers some of them), and from further back,
// where every cluster is small. Each is drawn at full detail (pose n) and
// with the LOD cut (pose 10+n).
var swPoses = []struct{ pos, dir mgl32.Vec3 }{
	{mgl32.Vec3{0.2, 0.3, 4.5}, mgl32.Vec3{-0.03, -0.08, -1}},
	{mgl32.Vec3{-1.5, -0.6, 3.5}, mgl32.Vec3{0.35, -0.05, -1}},
	{mgl32.Vec3{0.1, 0.4, 7.5}, mgl32.Vec3{0, -0.05, -1}},
}

// readVisIDs reads the scene target's visibility IDs.
func readVisIDs() []uint32 {
	ids := make([]uint32, int(scene.w)*int(scene.h))
	gl.BindTexture(gl.TEXTURE_2D, scene.visID)
	glext.GetTexImage(gl.TEXTURE_2D, 0, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&ids[0]))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return ids
}

// swShot draws lvl at the pose twice (the occlusion history's first frame,
// then the one kept) with the rasteriser switched as mode, and returns the
// picture, the IDs and how many clusters the rasteriser drew.
func swShot(t *testing.T, w *visWorld, lvl *world.Level, mode string) ([]uint8, []uint32, int) {
	t.Helper()
	NaniteSWRaster = mode
	naniteSW.reset()
	w.frame(lvl)
	px := w.frame(lvl)
	return px, readVisIDs(), DebugStats.NaniteSWClusters
}

func TestSWRasterDrawsTheHardwarePicture(t *testing.T) {
	w := newVisWorld(t)
	lvl := visTestLevel(t, visTestRock(t))
	setSWRaster(t, "0")
	NaniteStats = true
	// Full detail (cluster tiles, a command a cluster before the merge) and
	// the LOD cut (range tiles over the DAG's leaves).
	for k := 0; k < 2*len(swPoses); k++ {
		pi, p := k%len(swPoses), swPoses[k%len(swPoses)]
		NaniteLOD = k >= len(swPoses)
		if NaniteLOD {
			pi += 10
		}
		gfx.Cam.Position, gfx.Cam.Direction = p.pos, p.dir.Normalize()
		gfx.UpdateCamMatrix()
		hw, hwIDs, none := swShot(t, w, lvl, "0")
		if none != 0 {
			t.Fatalf("pose %d: the compute rasteriser drew %d clusters while off", pi, none)
		}
		sw, swIDs, drawn := swShot(t, w, lvl, "32")
		if naniteSW.mode != swMode32 {
			t.Fatalf("pose %d: the 32-bit form did not build", pi)
		}
		dumpPNG(fmt.Sprintf("sw%d_hw", pi), hw, int(w.size))
		dumpPNG(fmt.Sprintf("sw%d_sw", pi), sw, int(w.size))
		// Coverage: which pixels hold an ID at all.
		hwCov, swCov, onlyHW, onlySW := 0, 0, 0, 0
		for i := range hwIDs {
			a, b := hwIDs[i] != 0, swIDs[i] != 0
			if a {
				hwCov++
			}
			if b {
				swCov++
			}
			if a && !b {
				onlyHW++
			}
			if b && !a {
				onlySW++
			}
		}
		over8, over32, worst := pixelDiff(hw, sw)
		total := int(w.size * w.size)
		t.Logf("pose %d: %d clusters rasterised in compute; IDs cover %d (hardware) / %d (compute) pixels, %d only the hardware's, %d only the compute's; %d pixels differ by more than 8 levels, %d by more than 32, worst %d",
			pi, drawn, hwCov, swCov, onlyHW, onlySW, over8, over32, worst)
		if drawn == 0 {
			t.Errorf("pose %d: the compute rasteriser drew no cluster", pi)
		}
		// The silhouettes may differ by a pixel where a centre lies exactly
		// on an edge (the hardware's top-left rule, the compute
		// rasteriser's inclusive one); a hole or a show-through is many.
		if onlyHW+onlySW > hwCov/200+4 {
			t.Errorf("pose %d: coverage differs on %d pixels of %d", pi, onlyHW+onlySW, hwCov)
		}
		// The shading is the same triangles' (the material pass cannot tell
		// who rasterised them), so what differs is coverage alone: a crack
		// between two triangles shows whatever is behind (a back face, the
		// wall) and is tens of levels off. Measured: no pixel past 8 on
		// Mesa D3D12; an exclusive edge rule in the rasteriser leaves 1-3
		// cracks a pose, past 100 levels.
		if over8 > total/2000 || over32 > 0 {
			t.Errorf("pose %d: %d pixels differ by more than 8 levels (allowed %d), %d by more than 32 (allowed none; worst %d)",
				pi, over8, total/2000, over32, worst)
		}
	}
}

// The 64-bit form draws the hardware's picture as the 32-bit one does. (Not
// the same IDs: a cluster's record is its place in the cull's list, which
// the GPU appends to in whatever order its threads finish — the same
// triangle under another number.) Where the driver has no 64-bit atomics
// this is skipped; the native run covers it (docs/nanite/PHASE_5.md).
func TestSWRaster64BitFormDrawsTheHardwarePicture(t *testing.T) {
	w := newVisWorld(t)
	glext.Init()
	if !glext.Int64AtomicsAvailable() {
		t.Skip("no 64-bit atomics (GL_NV_shader_atomic_int64) on this driver")
	}
	lvl := visTestLevel(t, visTestRock(t))
	setSWRaster(t, "0")
	NaniteStats = true
	for k := 0; k < 2*len(swPoses); k++ {
		pi, p := k%len(swPoses), swPoses[k%len(swPoses)]
		NaniteLOD = k >= len(swPoses)
		gfx.Cam.Position, gfx.Cam.Direction = p.pos, p.dir.Normalize()
		gfx.UpdateCamMatrix()
		hw, hwIDs, _ := swShot(t, w, lvl, "0")
		sw32, _, n32 := swShot(t, w, lvl, "32")
		sw64, ids64, n64 := swShot(t, w, lvl, "64")
		if naniteSW.mode != swMode64 {
			t.Fatalf("the 64-bit form did not build")
		}
		cov := 0
		for i := range hwIDs {
			if (hwIDs[i] != 0) != (ids64[i] != 0) {
				cov++
			}
		}
		o8, o32, worst := pixelDiff(hw, sw64)
		f8, f32, fworst := pixelDiff(sw32, sw64)
		t.Logf("pose %d (LOD %v): %d / %d clusters (32 / 64); 64-bit vs hardware: coverage differs on %d, %d past 8, %d past 32, worst %d; vs 32-bit: %d past 8, %d past 32, worst %d",
			pi, NaniteLOD, n32, n64, cov, o8, o32, worst, f8, f32, fworst)
		total := int(w.size * w.size)
		if n64 == 0 {
			t.Errorf("pose %d: the 64-bit form drew no cluster", pi)
		}
		if cov > 4 || o8 > total/2000 || o32 > 0 {
			t.Errorf("pose %d: the 64-bit form's picture is not the hardware's", pi)
		}
	}
}

// A cluster the IDs cannot number turns the visibility buffer off.
func TestOversizedClustersRefuseTheVisibilityBuffer(t *testing.T) {
	prev := naniteClusterTrisMax
	defer func() { naniteClusterTrisMax = prev }()
	noteClusterTris(naniteVisClusterTris)
	if !naniteClustersFitVis() {
		t.Fatalf("a %d-triangle cluster refused", naniteVisClusterTris)
	}
	noteClusterTris(naniteVisClusterTris + 1)
	if naniteClustersFitVis() {
		t.Fatalf("a %d-triangle cluster accepted", naniteVisClusterTris+1)
	}
}
