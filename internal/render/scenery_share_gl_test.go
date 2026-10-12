package render

// scenery_share_gl_test.go — placements of one cooked mesh on the classic
// path (world/scenery_share.go) draw from one set of buffers, each through a
// VAO of its own and at its own transform, and deleting one leaves the other
// drawing — one by one and instanced alike.
//
//	FOUNDRY_GL_TEST=1 go test -run ScenerySharedClassic ./internal/render/

import (
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// pixelAt is the RGB of the pixel p projects to in a skyTestSize frame.
func pixelAt(px []uint8, p mgl32.Vec3) [3]uint8 {
	c := gfx.Projection.Mul4(gfx.Cam.Matrix).Mul4x1(p.Vec4(1))
	x := int((c[0]/c[3]*0.5 + 0.5) * skyTestSize)
	y := int((c[1]/c[3]*0.5 + 0.5) * skyTestSize)
	o := 4 * (y*skyTestSize + x)
	return [3]uint8{px[o], px[o+1], px[o+2]}
}

func isProbeBackground(c [3]uint8) bool { return c == [3]uint8{0, 255, 0} }

func TestScenerySharedClassicPlacementsDrawAtTheirOwnTransforms(t *testing.T) {
	newNormalProbe(t)
	gfx.InstancedUniform = world.ProgUniformLoc(gfx.Program, "instanced\x00")
	scenery = sceneryInstancer{}

	rock := filepath.Join(t.TempDir(), "rock.fnm")
	if _, err := nanitecook.CookMeshFile(nanitecook.SampleRock(4, 0.3), rock, true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	at := []mgl32.Vec3{{-0.9, 0, 0}, {0.9, 0, 0}}
	lvl := &world.Level{}
	for i, p := range at {
		s := world.NewScenery("rock", rock, p)
		s.NoNanite = true // the classic path, which is what shares
		s.Scale = mgl32.Vec3{0.25, 0.25, 0.25}
		s.Rotation = mgl32.Vec3{0, 0.7 * float32(i), 0}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	t.Cleanup(func() { world.DeleteScenery(lvl) })

	a, b := &lvl.Scenery[0].Submeshes[0], &lvl.Scenery[1].Submeshes[0]
	if a.Vbo == 0 || a.Vbo != b.Vbo || a.Ebo != b.Ebo || a.Nbo != b.Nbo {
		t.Fatalf("buffers not shared: vbo %d/%d ebo %d/%d nbo %d/%d", a.Vbo, b.Vbo, a.Ebo, b.Ebo, a.Nbo, b.Nbo)
	}
	if a.Vao == 0 || a.Vao == b.Vao {
		t.Fatalf("VAOs %d and %d: each placement needs its own", a.Vao, b.Vao)
	}
	if a.SharedWith() != 2 {
		t.Fatalf("SharedWith = %d, want 2", a.SharedWith())
	}
	if a.ShadowLOD == nil || a.ShadowLOD.Vbo == 0 || a.ShadowLOD.Vbo != b.ShadowLOD.Vbo || a.ShadowLOD.Vao == b.ShadowLOD.Vao {
		t.Fatal("the shadow stand-ins do not share their buffers through VAOs of their own")
	}

	check := func(what string, want []bool) {
		t.Helper()
		for _, instanced := range []bool{false, true} {
			px := drawSceneryFrame(t, lvl, instanced)
			for i, p := range at {
				if drawn := !isProbeBackground(pixelAt(px, p)); drawn != want[i] {
					t.Errorf("%s (instanced %v): placement at %v drawn %v, want %v", what, instanced, p, drawn, want[i])
				}
			}
			if !isProbeBackground(pixelAt(px, mgl32.Vec3{})) {
				t.Errorf("%s (instanced %v): something drawn between the placements", what, instanced)
			}
		}
	}
	check("both", []bool{true, true})

	// One deleted, as the editor's delete does: the other draws on, from
	// the buffers it now holds alone.
	vbo := b.Vbo
	gone := lvl.Scenery[0]
	gone.Delete()
	lvl.Scenery = lvl.Scenery[1:]
	if !gl.IsBuffer(vbo) || b.SharedWith() != 1 {
		t.Fatalf("the buffers went with the first placement (shared with %d)", b.SharedWith())
	}
	check("after a delete", []bool{false, true})

	// The last one gone takes the buffers with it.
	lvl.Scenery[0].Delete()
	if gl.IsBuffer(vbo) {
		t.Error("the buffers outlived the last placement")
	}
}
