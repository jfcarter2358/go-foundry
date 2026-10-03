package render

// scenery_shadow_count_gl_test.go — the scenery shadow passes draw the
// triangles the scene has, no more: counted by the GPU itself
// (GL_PRIMITIVES_GENERATED) around RenderSceneryShadow and the Nanite
// scenery shadow, for a known scene.
//
// The pixel tests beside it (model_indexed_gl_test.go, nanite_gl_test.go)
// compare depth maps, and a depth map cannot tell one copy of a mesh from
// ten copies drawn over each other, or an index count fed in as a vertex
// count that happens to land inside the buffer. A count can. This is the
// test for "the shadow pass draws a huge number of vertices": every way the
// scenery shadow is drawn — placement by placement, instanced, indexed or
// de-indexed, and through the GPU cluster cull — is pinned to the exact
// triangle count of the placements it should draw.
//
//	FOUNDRY_GL_TEST=1 go test -run SceneryShadowDraws ./internal/render/

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

// primitivesDrawn runs fn inside a GL_PRIMITIVES_GENERATED query and returns
// the count: every triangle the vertex stage emitted, across every draw and
// every instance, before clipping. A culled draw adds nothing.
func primitivesDrawn(t *testing.T, fn func()) int {
	t.Helper()
	var q uint32
	gl.GenQueries(1, &q)
	defer gl.DeleteQueries(1, &q)
	gl.BeginQuery(gl.PRIMITIVES_GENERATED, q)
	fn()
	gl.EndQuery(gl.PRIMITIVES_GENERATED)
	// Finished first, for the readback reason glext/readback.go gives.
	gl.Finish()
	var n uint32
	gl.GetQueryObjectuiv(q, gl.QUERY_RESULT, &n)
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("GL error 0x%x around the counted draw", e)
	}
	return int(n)
}

// sceneryTriangles is how many triangles placement s holds, over every
// submesh, whichever shape (pool + indices, or corner soup) they loaded as.
func sceneryTriangles(s *world.Scenery) int {
	n := 0
	for si := range s.Submeshes {
		n += s.Submeshes[si].CornerCount() / 3
	}
	return n
}

// bindSceneryShadow readies the cascade shadow program as the walker leaves
// it: the light's matrix, the identity model matrix.
func bindSceneryShadow() {
	lsm := gfx.Projection.Mul4(gfx.Cam.Matrix)
	identity := mgl32.Ident4()
	gl.UseProgram(Shadows.Program)
	gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
	gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
	gl.Disable(gl.CULL_FACE)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
}

// The classic scenery shadow — one placement at a time and instanced, from
// the indexed model and from the de-indexed soup — draws each placement's
// triangles exactly once, and a frustum that holds one placement draws that
// one's alone.
func TestSceneryShadowDrawsEachPlacementsTrianglesOnce(t *testing.T) {
	newNormalProbe(t)
	gl.Enable(gl.DEPTH_TEST)
	defer gl.Disable(gl.DEPTH_TEST)

	for _, indexed := range []bool{false, true} {
		lvl := indexedProbeLevel(t, indexed)
		per := sceneryTriangles(&lvl.Scenery[0])
		if per == 0 {
			t.Fatal("the dropship loaded no triangles")
		}
		// The probe level's placements overlap; ten metres apart, an
		// orthographic box round the first holds it alone. Only counted, not
		// looked at, so where they stand does not matter otherwise.
		for i := range lvl.Scenery {
			lvl.Scenery[i].Position = lvl.Scenery[i].Position.Add(mgl32.Vec3{10 * float32(i), 0, 0})
		}
		lo, hi, _ := lvl.Scenery[0].WorldAABB()
		c0 := lo.Add(hi).Mul(0.5)
		r := hi.Sub(lo).Len()*0.5 + 0.01
		eye := c0.Add(mgl32.Vec3{0, 0, 2 * r})
		one := ExtractFrustum(mgl32.Ortho(-r, r, -r, r, 0.01, 4*r).Mul4(mgl32.LookAtV(eye, c0, mgl32.Vec3{0, 1, 0})))
		inOne := 0
		for i := range lvl.Scenery {
			if SceneryInFrustum(&lvl.Scenery[i], &one) {
				inOne++
			}
		}
		if inOne != 1 {
			t.Fatalf("indexed=%v: the narrow frustum holds %d placements, want 1", indexed, inOne)
		}

		for _, instanced := range []bool{false, true} {
			prev := sceneryInstancing
			sceneryInstancing = instanced
			for _, c := range []struct {
				name string
				cull *Frustum
				want int
			}{
				{"no cull", nil, per * len(lvl.Scenery)},
				{"one placement in view", &one, per},
			} {
				scenery = sceneryInstancer{}
				bindSceneryShadow()
				got := primitivesDrawn(t, func() {
					RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, c.cull)
				})
				if got != c.want {
					t.Errorf("indexed=%v instanced=%v, %s: the shadow drew %d triangles, want %d (%d per placement)",
						indexed, instanced, c.name, got, c.want, per)
				}
			}
			sceneryInstancing = prev
		}
	}
}

// The Nanite scenery shadow — a field of cooked rocks through the GPU
// cluster cull — draws every placement's triangles exactly once with no
// frustum, fewer with a frustum that takes some away, and nothing at all
// through the classic walker (which must skip what the cull draws, or the
// rocks would cast twice).
func TestSceneryShadowDrawsNaniteRocksOnce(t *testing.T) {
	naniteProbe(t)
	dir := t.TempDir()
	glb, err := model.EncodeGLB(nanitecook.SampleRock(4, 0.4)) // 5,120 triangles
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
	var poses []mgl32.Vec3
	for i := 0; i < 12; i++ {
		a := float64(i) * 0.52
		poses = append(poses, mgl32.Vec3{float32(math.Cos(a)) * 1.3, float32(math.Sin(a)) * 1.1, -float32(i) * 0.4})
	}
	lvl := naniteSceneryLevel(t, res.Out, true, poses, 0.45)
	per := lvl.Scenery[0].Nanite.TriangleCount()
	total := per * len(lvl.Scenery)

	bindSceneryShadow()
	if got := primitivesDrawn(t, func() {
		RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, nil)
	}); got != 0 {
		t.Errorf("the classic scenery shadow drew %d triangles of placements the GPU cull draws", got)
	}

	for frame := 0; frame < 2; frame++ {
		bindSceneryShadow()
		got := primitivesDrawn(t, func() { renderNaniteSceneryShadow(lvl, Shadows.Program, nil) })
		if got != total {
			t.Errorf("frame %d, no cull: the Nanite shadow drew %d triangles, want %d (%d placements x %d)",
				frame, got, total, len(lvl.Scenery), per)
		}
	}

	// A narrow frustum: some clusters out, none drawn twice.
	tight := ExtractFrustum(mgl32.Perspective(mgl32.DegToRad(20), 1, 0.1, 100).Mul4(gfx.Cam.Matrix))
	bindSceneryShadow()
	got := primitivesDrawn(t, func() { renderNaniteSceneryShadow(lvl, Shadows.Program, &tight) })
	if got == 0 || got >= total {
		t.Errorf("tight frustum: the Nanite shadow drew %d triangles, want some but fewer than %d", got, total)
	}
}

// The shadow cone (NaniteShadowCone): on a closed cooked mesh, a sun cascade
// and a point light's face each drop the clusters turned wholly away from the
// light — fewer triangles than with the cone off — and the shadow map does not
// change by one sample against the classic path, which draws every triangle.
func TestSceneryShadowConeDropsBackClustersWithoutChangingTheMap(t *testing.T) {
	naniteProbe(t)
	dir := t.TempDir()
	glb, err := model.EncodeGLB(nanitecook.SampleRock(4, 0.4))
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
	var poses []mgl32.Vec3
	for i := 0; i < 12; i++ {
		a := float64(i) * 0.52
		poses = append(poses, mgl32.Vec3{float32(math.Cos(a)) * 1.3, float32(math.Sin(a)) * 1.1, -float32(i) * 0.4})
	}
	classic := naniteSceneryLevel(t, res.Out, false, poses, 0.45)
	gpu := naniteSceneryLevel(t, res.Out, true, poses, 0.45)
	if !gpu.Scenery[0].Nanite.Closed() {
		t.Fatal("the sample rock does not read as closed; the shadow cone would never apply")
	}
	total := gpu.Scenery[0].Nanite.TriangleCount() * len(poses)

	centre := mgl32.Vec3{0, 0, -2.2}
	lights := []struct {
		name string
		lsm  mgl32.Mat4
	}{
		// A sun cascade: orthographic, every rock inside its box.
		{"sun", mgl32.Ortho(-4, 4, -4, 4, 0.1, 30).Mul4(
			mgl32.LookAtV(centre.Add(mgl32.Vec3{3, 5, 4}.Normalize().Mul(12)), centre, mgl32.Vec3{0, 1, 0}))},
		// A point light's face: a 90-degree perspective from close by.
		{"point", mgl32.Perspective(mgl32.DegToRad(90), 1, 0.1, 30).Mul4(
			mgl32.LookAtV(centre.Add(mgl32.Vec3{0.5, 0.8, 4.5}), centre, mgl32.Vec3{0, 1, 0}))},
	}
	shadow := func(lvl *world.Level, lsm mgl32.Mat4, cone bool) ([]float32, int) {
		prev := NaniteShadowCone
		NaniteShadowCone = cone
		defer func() { NaniteShadowCone = prev }()
		identity := mgl32.Ident4()
		cull := ExtractFrustum(lsm)
		scenery = sceneryInstancer{}
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Disable(gl.CULL_FACE)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		n := primitivesDrawn(t, func() {
			RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, &cull)
			renderNaniteSceneryShadow(lvl, Shadows.Program, &cull)
		})
		return readDepth(), n
	}
	for _, l := range lights {
		ref, _ := shadow(classic, l.lsm, false)
		off, nOff := shadow(gpu, l.lsm, false)
		on, nOn := shadow(gpu, l.lsm, true)
		drawn := 0
		for i := range ref {
			if ref[i] < 1 {
				drawn++
			}
		}
		if drawn < 1000 {
			t.Fatalf("%s: only %d shadow samples written; the light is not looking at the rocks", l.name, drawn)
		}
		for _, c := range []struct {
			what string
			got  []float32
		}{{"cone off", off}, {"cone on", on}} {
			diff := 0
			for i := range ref {
				if ref[i] != c.got[i] {
					diff++
				}
			}
			if diff > 0 {
				t.Errorf("%s, %s: %d of %d shadow samples differ from the classic path", l.name, c.what, diff, drawn)
			}
		}
		if nOff == 0 || nOff > total {
			t.Errorf("%s: cone off drew %d triangles, want 1..%d", l.name, nOff, total)
		}
		// The sample rock is bumpy — five octaves of displacement — so most
		// of its 128-triangle clusters face too many ways to have a cone, and
		// the cone drops 7-8% of them from either light. Three per cent is
		// the floor: a cone test that had stopped working drops none.
		if nOn >= nOff-nOff*3/100 {
			t.Errorf("%s: the shadow cone drew %d triangles against %d without it; it dropped too few", l.name, nOn, nOff)
		}
		t.Logf("%s: %d triangles with the shadow cone, %d without (%.0f%%), of %d", l.name, nOn, nOff, 100*float64(nOn)/float64(nOff), total)
	}
	// The sun's box holds every rock: with the cone off it draws them all,
	// once.
	if _, n := shadow(gpu, lights[0].lsm, false); n != total {
		t.Errorf("sun, cone off: drew %d triangles, want every rock's %d", n, total)
	}
}

// A cooked mesh drawn the classic way (no GPU cull for it) casts its
// FALLBACK cut, not its full detail: every placement's shadow, per placement
// and instanced, in a sun cascade and a point light's face, is exactly the
// fallback's triangles — a bounded fraction of the full detail the colour
// pass still draws — and with world.NaniteClassicShadowLOD off it is the full
// detail again. The shadow it casts is the same shape: the samples it writes
// are within a few per cent of the full detail's.
func TestClassicCookedSceneryCastsItsFallbackCut(t *testing.T) {
	naniteProbe(t)
	dir := t.TempDir()
	glb, err := model.EncodeGLB(nanitecook.SampleRock(5, 0.4)) // 20,480 triangles
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
	var poses []mgl32.Vec3
	for i := 0; i < 8; i++ {
		a := float64(i) * 0.7
		poses = append(poses, mgl32.Vec3{float32(math.Cos(a)) * 1.4, float32(math.Sin(a)) * 1.1, -float32(i) * 0.5})
	}
	load := func(lod bool) *world.Level {
		prev := world.NaniteClassicShadowLOD
		world.NaniteClassicShadowLOD = lod
		defer func() { world.NaniteClassicShadowLOD = prev }()
		return naniteSceneryLevel(t, res.Out, false, poses, 0.45)
	}
	full, cut := load(false), load(true)
	m := cut.Scenery[0].Nanite
	per := m.TriangleCount()
	fallback := len(m.Fallback) / 3
	if fallback == 0 || fallback*4 > per {
		t.Fatalf("the sample rock's fallback is %d of %d triangles; want a cut under a quarter", fallback, per)
	}
	for i := range cut.Scenery {
		if got := sceneryTriangles(&cut.Scenery[i]); got != per {
			t.Fatalf("placement %d: the colour pass's mesh is %d triangles, want the full %d", i, got, per)
		}
	}

	centre := mgl32.Vec3{0, 0, -1.8}
	views := []struct {
		name string
		lsm  mgl32.Mat4
	}{
		{"sun", mgl32.Ortho(-4, 4, -4, 4, 0.1, 30).Mul4(
			mgl32.LookAtV(centre.Add(mgl32.Vec3{3, 5, 4}.Normalize().Mul(12)), centre, mgl32.Vec3{0, 1, 0}))},
		{"point", mgl32.Perspective(mgl32.DegToRad(90), 1, 0.1, 30).Mul4(
			mgl32.LookAtV(centre.Add(mgl32.Vec3{0.5, 0.8, 4.5}), centre, mgl32.Vec3{0, 1, 0}))},
	}
	cast := func(lvl *world.Level, lsm mgl32.Mat4) ([]float32, int) {
		identity := mgl32.Ident4()
		scenery = sceneryInstancer{}
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Disable(gl.CULL_FACE)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		n := primitivesDrawn(t, func() {
			RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, nil)
		})
		return readDepth(), n
	}
	written := func(d []float32) int {
		n := 0
		for _, v := range d {
			if v < 1 {
				n++
			}
		}
		return n
	}
	for _, instanced := range []bool{false, true} {
		prev := sceneryInstancing
		sceneryInstancing = instanced
		for _, v := range views {
			ref, nFull := cast(full, v.lsm)
			got, nCut := cast(cut, v.lsm)
			if want := per * len(poses); nFull != want {
				t.Errorf("instanced=%v %s, switch off: drew %d triangles, want the full %d", instanced, v.name, nFull, want)
			}
			if want := fallback * len(poses); nCut != want {
				t.Errorf("instanced=%v %s: drew %d triangles, want the fallback's %d (%d a placement)",
					instanced, v.name, nCut, want, fallback)
			}
			a, b := written(ref), written(got)
			if a < 1000 {
				t.Fatalf("%s: only %d samples written; the light is not looking at the rocks", v.name, a)
			}
			if d := math.Abs(float64(a-b)) / float64(a); d > 0.03 {
				t.Errorf("instanced=%v %s: the fallback wrote %d samples against the full detail's %d (%.1f%% apart)",
					instanced, v.name, b, a, 100*d)
			}
			t.Logf("instanced=%v %s: %d triangles cast against %d; %d samples against %d", instanced, v.name, nCut, nFull, b, a)
		}
		sceneryInstancing = prev
	}
}
