package editor

// editor_generator_rocks_test.go — the Generator editor over the rocks
// stage: its parameter panel draws (preset menu, colours, the preview-only
// variant picker), the preview lands as one turntable mesh with its
// colours, the 3D overlay offers Spin, and the turntable matrix turns the
// variant about its own vertical axis while Spin is on.

import (
	"testing"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

func rocksEditorDoc() *procgen.Doc {
	return &procgen.Doc{Name: "stones", Seed: 3, Stages: []procgen.Stage{{Kind: stages.RocksKind, ID: "boulders", Params: procgen.Params{
		"preset": procgen.Enum("rubble"), "variants": procgen.Int(3), "preview_variant": procgen.Int(1),
		"preview_triangles": procgen.Int(1500),
	}}}}
}

func TestGeneratorDrawsTheRocksStage(t *testing.T) {
	withGeneratorWindow(t)
	genSetDoc(rocksEditorDoc(), "")
	genForceTreesOpen = true
	t.Cleanup(func() { genForceTreesOpen = false })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		genSelected = 0
		genViewMode = genView3D
		genFrame(imgui.Vec2{X: -100, Y: -100}, false, false)
		genLoadPreview(t)
		p := genPreview.data
		if p == nil || len(p.Meshes) != 1 || !p.Meshes[0].Turntable || len(p.Meshes[0].Colors) == 0 {
			t.Fatalf("the rocks preview is not one coloured turntable mesh: %+v", p)
		}
		if p.Meshes[0].Name != "rubble_01" {
			t.Errorf("the preview shows %s, want the chosen variant rubble_01", p.Meshes[0].Name)
		}
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		genViewMode = genView2D
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		genV3.turntable = true
		drawUIOnlyFrame(func() { genView3DOverlay(230, 54) })
	})
}

func TestGeneratorTurntableTurnsAboutTheVariant(t *testing.T) {
	saved := genV3
	t.Cleanup(func() { genV3 = saved })
	genV3.turntable, genV3.spin, genV3.angle = true, true, 0
	genV3.centre = mgl32.Vec3{2, 0.5, -1}
	genV3.last = time.Now().Add(-50 * time.Millisecond)
	m := genTurntableMatrix()
	if genV3.angle <= 0 {
		t.Fatal("the turntable did not turn")
	}
	// The axis through the centre stays put; a point off it moves.
	c := m.Mul4x1(mgl32.Vec4{2, 3, -1, 1})
	if c.Vec3().Sub(mgl32.Vec3{2, 3, -1}).Len() > 1e-5 {
		t.Errorf("the axis moved: %v", c)
	}
	if q := m.Mul4x1(mgl32.Vec4{3, 0.5, -1, 1}); q.Vec3().Sub(mgl32.Vec3{3, 0.5, -1}).Len() < 1e-4 {
		t.Error("a point off the axis did not move")
	}
	genV3.spin = false
	a := genV3.angle
	genV3.last = time.Now().Add(-50 * time.Millisecond)
	genTurntableMatrix()
	if genV3.angle != a {
		t.Error("the turntable turned with Spin off")
	}
	genV3.turntable = false
	if genTurntableMatrix() != mgl32.Ident4() {
		t.Error("a landscape preview was turned")
	}
}
