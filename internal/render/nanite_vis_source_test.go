package render

// nanite_vis_source_test.go — the visibility buffer's programs are made by
// rewriting vertex.glsl and fragment.glsl (nanite_vis_source.go), and a
// rewrite that silently stops matching would shade something other than
// the forward path. These run the rewrites on the files in the repository:
// every pattern found, nothing left that the material pass cannot
// reconstruct. (Whether the result compiles and draws the forward path's
// picture is nanite_vis_gl_test.go's.)

import (
	"strings"
	"testing"

	"foundry/internal/gfx"
)

func worldSourcesForTest(t *testing.T) (string, string) {
	t.Helper()
	withRepoResources(t)
	vert, frag, err := gfx.WorldProgramSources(repoShader(t, "vertex.glsl"), repoShader(t, "fragment.glsl"))
	if err != nil {
		t.Fatal(err)
	}
	return vert, frag
}

func TestVisVertexSourceReadsTheInstanceFromItsRecord(t *testing.T) {
	vert, _ := worldSourcesForTest(t)
	src, err := visVertexSource(vert)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"void nvVertexMain()", "instanceModel = visInstances[visRecA.z].model;",
		"layout(location = 11) in uvec4 visRecA;", "flat out uint visCmd;", "invariant gl_Position;"} {
		if !strings.Contains(src, want) {
			t.Errorf("the ID pass's vertex stage has no %q", want)
		}
	}
	if strings.Contains(src, "in mat4 instanceModel") {
		t.Error("the per-instance matrix is still an attribute")
	}
	if strings.Count(src, "void main()") != 1 {
		t.Error("want exactly one main")
	}
	if _, err := visVertexSource(strings.Replace(vert, "layout(location = 7) in mat4 instanceModel;", "", 1)); err == nil {
		t.Error("a vertex stage without the instance attribute was accepted")
	}
}

func TestMaterialFragmentSourceRewritesEveryDerivative(t *testing.T) {
	vert, frag := worldSourcesForTest(t)
	main := mustReadShader(repoShader(t, "nanite_material_frag.glsl"))
	src, err := materialFragmentSource(vert, frag, main)
	if err != nil {
		t.Fatal(err)
	}
	part, err := materialFragmentPart(frag)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(part, "discard;") {
		t.Error("fragment.glsl's discards survive: the material pass would punch holes the ID pass filled")
	}
	for _, gone := range []string{"dFdx(", "dFdy(", "gl_FrontFacing", "texture(tex,", "texture(normalMap,",
		"in vec2 fragTexCoord;", "invariant gl_Position", "gl_Position"} {
		if strings.Contains(src, gone) {
			t.Errorf("the material pass still has %q", gone)
		}
	}
	for _, want := range []string{"void nvShadeMain()", "void vx_main()", "vec4 vx_Position;", "vec2 vx_fragTexCoord;",
		"nvTexture(tex,", "vx_skinPosition(", "vx_waterHeight(", "float waterHeight("} {
		if !strings.Contains(src, want) {
			t.Errorf("the material pass has no %q", want)
		}
	}
	if n := len(reVersion.FindAllString(src, -1)); n != 1 {
		t.Errorf("%d #version lines", n)
	}
	if n := strings.Count(src, "void main()"); n != 1 {
		t.Errorf("%d mains", n)
	}
	// Uniforms both stages declare are declared once.
	for _, u := range []string{"uniform int   isWater;", "uniform float time;", "uniform int smoothShading;"} {
		if n := strings.Count(src, u); n > 1 {
			t.Errorf("%q declared %d times", u, n)
		}
	}
	// A fragment stage that took a derivative the pass cannot rebuild, or
	// lost one it rewrites, is refused rather than mis-shaded.
	if _, err := materialFragmentPart(strings.Replace(frag, "void main() {", "void main() {\n    float q = dFdx(fragViewDepth);", 1)); err == nil {
		t.Error("an unreconstructed derivative was accepted")
	}
	if _, err := materialFragmentPart(strings.ReplaceAll(frag, "dFdx(fragTexCoord)", "vec2(0.0)")); err == nil {
		t.Error("a fragment stage without its UV derivative was accepted")
	}
}

// The jitter is a Halton(2,3) sequence inside the pixel, centred: eight
// distinct offsets, each within half a pixel, averaging near zero.
func TestTAAJitterCoversThePixel(t *testing.T) {
	const w, h = 640, 360
	seen := map[[2]float32]bool{}
	var sx, sy float32
	for n := uint32(0); n < taaJitterSamples; n++ {
		j := taaJitter(n, w, h)
		px, py := j[0]*w/2, j[1]*h/2
		if px < -0.5 || px > 0.5 || py < -0.5 || py > 0.5 {
			t.Errorf("frame %d: offset %v px is outside the pixel", n, [2]float32{px, py})
		}
		seen[[2]float32{px, py}] = true
		sx += px
		sy += py
	}
	if len(seen) != taaJitterSamples {
		t.Errorf("%d distinct offsets in %d frames", len(seen), taaJitterSamples)
	}
	if sx, sy = sx/taaJitterSamples, sy/taaJitterSamples; sx*sx+sy*sy > 0.1*0.1 {
		t.Errorf("the offsets average (%g, %g) px, not near the centre", sx, sy)
	}
	if taaJitter(3, w, h) != taaJitter(3+taaJitterSamples, w, h) {
		t.Error("the sequence does not repeat")
	}
}
