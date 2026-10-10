package preview

// model_test.go — the two silent mistakes a .fmdl rasteriser makes.
//
// Both of these were made once already, in the Python tool this replaces, and
// neither announces itself: the picture comes out, it just has less in it than
// the model does. A quad drawn as one triangle looks like a wedge, and a texture
// sampled once per face looks like a flat colour.

import (
	"image"
	"image/color"
	"testing"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// quadDoc is one flat square facing the front view, as a single four-corner
// face — the shape that exposes an unfanned n-gon.
func quadDoc() *model.ModelDoc {
	return &model.ModelDoc{
		ID: "quad",
		Verts: []mgl32.Vec3{
			{-1, -1, 0}, {1, -1, 0}, {1, 1, 0}, {-1, 1, 0},
		},
		Faces: []model.ModelFace{{
			Verts: []int{0, 1, 2, 3},
			UVs:   []mgl32.Vec2{{0, 1}, {1, 1}, {1, 0}, {0, 0}},
		}},
	}
}

func TestAQuadDrawsAsAQuad(t *testing.T) {
	img, err := Model(quadDoc(), Options{W: 64, H: 64, Untextured: true})
	if err != nil {
		t.Fatal(err)
	}
	// Every corner of the square is covered. Drawing only v[0..2] leaves one of
	// the two triangles empty, and it is always the same corner.
	for _, p := range []image.Point{{X: 8, Y: 8}, {X: 55, Y: 8}, {X: 8, Y: 55}, {X: 55, Y: 55}} {
		if isBackground(img.NRGBAAt(p.X, p.Y)) {
			t.Errorf("pixel %v is background: the quad was not fanned, so half of it is missing", p)
		}
	}
}

func TestATextureIsSampledPerFragment(t *testing.T) {
	// Two halves, hard split: red on the left, blue on the right. One sample per
	// face can only produce one of them.
	tex := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			c := color.NRGBA{R: 0xff, A: 0xff}
			if x >= 8 {
				c = color.NRGBA{B: 0xff, A: 0xff}
			}
			tex.SetNRGBA(x, y, c)
		}
	}

	// The image is handed in, which is the same sampler path a file reaches
	// once it is decoded.
	img := drawWith(t, quadDoc(), tex)

	left, right := img.NRGBAAt(16, 32), img.NRGBAAt(48, 32)
	if left.R <= left.B {
		t.Errorf("the left half is %v, want mostly red", left)
	}
	if right.B <= right.R {
		t.Errorf("the right half is %v, want mostly blue — one sample per face paints it all one colour", right)
	}
}

// A pose is the engine's pose: the same SampleInto, PoseDeltas and SkinVerts the
// renderer uses, so a picture and a measurement cannot disagree.
func TestAPoseMovesTheMesh(t *testing.T) {
	m, bone := skinnedQuad(t)
	m.Animations = []model.ModelClip{{
		Name: "slide", Duration: 1, FPS: 30,
		Tracks: []model.ModelBoneTrack{{
			Bone: bone,
			Keys: []model.ModelBoneKey{
				{Time: 0, ModelBonePose: model.IdentBonePose()},
				{Time: 1, ModelBonePose: model.ModelBonePose{Trans: mgl32.Vec3{0, 4, 0}, Rot: mgl32.QuatIdent(), Scale: mgl32.Vec3{1, 1, 1}}},
			},
		}},
	}}

	rest, err := posedVerts(m, "")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := posedVerts(m, "slide:1")
	if err != nil {
		t.Fatal(err)
	}
	if rest[0].ApproxEqual(moved[0]) {
		t.Error("the pose left the mesh where it was; the clip is not being sampled")
	}
	if got := moved[0].Y() - rest[0].Y(); got < 3.5 || got > 4.5 {
		t.Errorf("the bound vertex moved %v, want about 4", got)
	}
}

// An unknown clip is a mistake worth a message naming what there is, because the
// answer is almost always a typo.
func TestAnUnknownClipSaysWhatThereIs(t *testing.T) {
	m := quadDoc()
	m.Animations = []model.ModelClip{{Name: "walk", Duration: 1}}
	if _, err := Model(m, Options{Pose: "wlak:0.5"}); err == nil {
		t.Fatal("an unknown clip drew without complaint")
	} else if got := err.Error(); !contains(got, "walk") {
		t.Errorf("the error does not name the clips there are: %q", got)
	}
}

// ---------------------------------------------------------------- helpers

func isBackground(c color.NRGBA) bool {
	return c.R == 0x20 && c.G == 0x22 && c.B == 0x28
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// drawWith draws the model against an in-memory texture, which is the sampler
// path a file reaches by once it is decoded.
func drawWith(t *testing.T, m *model.ModelDoc, tex image.Image) *image.NRGBA {
	t.Helper()
	img, err := Model(m, Options{W: 64, H: 64, Image: tex})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// skinnedQuad is one quad bound entirely to one bone.
func skinnedQuad(t *testing.T) (*model.ModelDoc, int) {
	t.Helper()
	m := quadDoc()
	m.Bones = []model.ModelBone{{Name: "root", Head: mgl32.Vec3{0, -1, 0}, Tail: mgl32.Vec3{0, 1, 0}, Parent: model.ModelBoneNoParent}}
	m.Weights = make([][]model.ModelVertexWeight, len(m.Verts))
	for i := range m.Weights {
		m.Weights[i] = []model.ModelVertexWeight{{Bone: 0, Weight: 1}}
	}
	return m, 0
}
