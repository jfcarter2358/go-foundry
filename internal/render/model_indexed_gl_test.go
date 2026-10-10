package render

// model_indexed_gl_test.go — a model drawn indexed (gfx.IndexedModels) is the
// same picture as the model drawn de-indexed, pixel for pixel and depth sample
// for depth sample: through the world program one placement at a time, through
// the instanced scenery path, through the cascade shadow program, and with a
// skinned attachment posed off its rest. Phase 0 of the Nanite plan.
//
//	FOUNDRY_GL_TEST=1 go test -run IndexedModel ./internal/render/

import (
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/model"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// indexedProbeLevel is three placements of the repo's dropship, scaled into
// the probe camera's view, loaded with IndexedModels set to indexed. The
// texture is cleared so the surface paints its own (smoothed) normal: a
// normal welded wrongly shows up as a colour change, which a texture would
// hide.
func indexedProbeLevel(t *testing.T, indexed bool) *world.Level {
	t.Helper()
	prev := gfx.IndexedModels
	gfx.IndexedModels = indexed
	defer func() { gfx.IndexedModels = prev }()

	path := filepath.Join(testenv.RepoRoot(t), "resources", "models", "dropship.fmdl")
	lvl := &world.Level{}
	for i, p := range []mgl32.Vec3{{-0.9, 0.5, 0}, {0.3, -0.4, -0.6}, {0.9, 0.6, 0.3}} {
		s := world.NewScenery("ship", path, mgl32.Vec3{})
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		for si := range s.Submeshes {
			s.Submeshes[si].Texture = ""
			if s.Submeshes[si].IsIndexed() != indexed {
				t.Fatalf("submesh indexed=%v, want %v", s.Submeshes[si].IsIndexed(), indexed)
			}
		}
		ext := s.LocalMax.Sub(s.LocalMin)
		k := 1.4 / max(ext[0], ext[1], ext[2])
		centre := s.LocalMin.Add(s.LocalMax).Mul(0.5 * k)
		s.Position = p.Sub(centre)
		s.Rotation = mgl32.Vec3{0.4 + 0.3*float32(i), 0.7 * float32(i), 0.1}
		s.Scale = mgl32.Vec3{k, k, k}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	t.Cleanup(func() { world.DeleteScenery(lvl) })
	return lvl
}

func diffPixels(a, b []uint8) (diff, drawn int) {
	for i := 0; i < len(a); i += 4 {
		if a[i] != b[i] || a[i+1] != b[i+1] || a[i+2] != b[i+2] {
			diff++
		}
		if !(a[i] == 0 && a[i+1] == 255 && a[i+2] == 0) {
			drawn++
		}
	}
	return diff, drawn
}

func TestIndexedModelDrawsTheDeindexedPicture(t *testing.T) {
	newNormalProbe(t)
	gfx.InstancedUniform = world.ProgUniformLoc(gfx.Program, "instanced\x00")
	gl.Enable(gl.DEPTH_TEST)
	defer gl.Disable(gl.DEPTH_TEST)

	soup, ind := indexedProbeLevel(t, false), indexedProbeLevel(t, true)
	for _, instanced := range []bool{false, true} {
		scenery = sceneryInstancer{}
		a := drawSceneryFrame(t, soup, instanced)
		scenery = sceneryInstancer{}
		b := drawSceneryFrame(t, ind, instanced)
		diff, drawn := diffPixels(a, b)
		if drawn < 200 {
			t.Fatalf("instanced=%v: only %d pixels drawn; the probe is not looking at the model", instanced, drawn)
		}
		if diff > 0 {
			t.Errorf("instanced=%v: %d of %d drawn pixels differ between the indexed and de-indexed model", instanced, diff, drawn)
		}
	}
}

func TestIndexedModelShadowMatchesDeindexed(t *testing.T) {
	newNormalProbe(t)
	lsm := gfx.Projection.Mul4(gfx.Cam.Matrix)
	identity := mgl32.Ident4()
	gl.Enable(gl.DEPTH_TEST)
	defer gl.Disable(gl.DEPTH_TEST)

	depth := func(lvl *world.Level, instanced bool) []float32 {
		prev := sceneryInstancing
		sceneryInstancing = instanced
		defer func() { sceneryInstancing = prev }()
		scenery = sceneryInstancer{}
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Disable(gl.CULL_FACE)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, nil)
		out := make([]float32, skyTestSize*skyTestSize)
		glext.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(&out[0]))
		return out
	}
	soup, ind := indexedProbeLevel(t, false), indexedProbeLevel(t, true)
	for _, instanced := range []bool{false, true} {
		a, b := depth(soup, instanced), depth(ind, instanced)
		diff, drawn := 0, 0
		for i := range a {
			if a[i] != b[i] {
				diff++
			}
			if a[i] < 1 {
				drawn++
			}
		}
		if drawn == 0 {
			t.Fatalf("instanced=%v: no depth was written", instanced)
		}
		if diff > 0 {
			t.Errorf("instanced=%v: %d of %d depth samples differ between the indexed and de-indexed shadow", instanced, diff, drawn)
		}
	}
}

// A skinned attachment welds its bone indices and weights with it: posed off
// its rest, the indexed and de-indexed meshes bend into the same picture.
func TestIndexedModelSkinnedPoseMatches(t *testing.T) {
	newNormalProbe(t)
	gl.Enable(gl.DEPTH_TEST)
	defer gl.Disable(gl.DEPTH_TEST)

	doc := model.NewCubeDoc()
	lo := doc.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0, 0})
	hi := doc.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0.5, 0})
	doc.RenameBone(lo, "lo")
	doc.RenameBone(hi, "hi")
	var top, bottom []int
	for i, v := range doc.Verts {
		if v.Y() > 0 {
			top = append(top, i)
		} else {
			bottom = append(bottom, i)
		}
	}
	doc.AssignVertsToBone(bottom, lo, 1)
	doc.AssignVertsToBone(top, hi, 1)
	ci := doc.AddClip("idle")
	doc.Clip(ci).Duration = 1
	doc.Clip(ci).SetKey(lo, 0, model.IdentBonePose())
	for fi := range doc.Faces {
		doc.Faces[fi].Smooth = true
	}
	doc.Texture = ""
	path := filepath.Join(t.TempDir(), "rig"+model.ModelExt)
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}

	// The top bone twisted and lifted: every top corner moves, so a corner
	// welded onto the wrong bone would move with the wrong half.
	pose := []mgl32.Mat4{
		mgl32.Ident4(),
		mgl32.Translate3D(0.1, 0.3, 0).Mul4(mgl32.HomogRotate3DY(0.8)),
	}
	draw := func(indexed bool) []uint8 {
		prev := gfx.IndexedModels
		gfx.IndexedModels = indexed
		m := world.EntityModel{Name: "rig", Path: path, Visible: true}
		err := m.Load("probe")
		gfx.IndexedModels = prev
		if err != nil {
			t.Fatal(err)
		}
		defer m.Delete()
		if len(m.Submeshes) == 0 || !m.Submeshes[0].IsSkinned() {
			t.Fatal("the rigged cube did not upload skin attributes")
		}
		if m.Submeshes[0].IsIndexed() != indexed {
			t.Fatalf("indexed=%v, want %v", m.Submeshes[0].IsIndexed(), indexed)
		}
		gl.Disable(gl.CULL_FACE)
		gl.ClearColor(0, 1, 0, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		model := mgl32.HomogRotate3DX(0.5).Mul4(mgl32.HomogRotate3DY(0.6)).Mul4(mgl32.Scale3D(1.4, 1.4, 1.4))
		for si := range m.Submeshes {
			m.Submeshes[si].BonePalette = pose
			m.Submeshes[si].DrawWithTransform(model)
		}
		px := make([]uint8, skyTestSize*skyTestSize*4)
		glext.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		return px
	}
	diff, drawn := diffPixels(draw(false), draw(true))
	if drawn < 200 {
		t.Fatalf("only %d pixels drawn", drawn)
	}
	if diff > 0 {
		t.Errorf("%d of %d drawn pixels differ between the indexed and de-indexed skinned cube", diff, drawn)
	}
}
