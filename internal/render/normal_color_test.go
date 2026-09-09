package render

// normal_color_test.go — the shader half of the untextured surface.
//
// A face that names no texture is painted with its own normal instead of with
// a sample from tex (NormalColorFlag, gfx/geometry.go). Both halves of that are
// looked up by name at runtime — GetUniformLocation returns -1 rather than
// failing if the shader drops the uniform — so a regression here is silent: an
// untextured brush would go back to binding texture object 0 and shading
// black, with nothing to point at.

import (
	"strings"
	"testing"
)

func TestWorldShaderPaintsUntexturedSurfacesWithTheirNormal(t *testing.T) {
	src := readShader(t, "fragment.glsl")
	if !strings.Contains(src, "uniform int normalColor;") {
		t.Error("fragment.glsl does not declare `uniform int normalColor` — NormalColorUniform in app.go has nothing to bind to")
	}
	if !strings.Contains(src, "if (normalColor != 0) {") {
		t.Error("fragment.glsl does not gate its albedo on normalColor — an untextured face would sample whatever texture is bound")
	}
	if !strings.Contains(src, "albedoNormal * 0.5 + 0.5") {
		t.Error("fragment.glsl does not encode the normal as the albedo — the untextured look is gone")
	}
	// The colour has to come from the normal as the polygon declares it, not
	// from the copy the back-face branch flips: a face seen from inside a
	// hollowed room would otherwise be the negative of its outside.
	if strings.Index(src, "vec3 albedoNormal = normal;") > strings.Index(src, "if (!gl_FrontFacing) {") {
		t.Error("fragment.glsl takes albedoNormal after the back-face flip — an untextured face would change colour with the viewing side")
	}
}

func TestModelShaderPaintsUntexturedMeshesWithTheirNormal(t *testing.T) {
	src := readShader(t, "model_frag.glsl")
	if !strings.Contains(src, "uniform int  normalColor;") {
		t.Error("model_frag.glsl does not declare `uniform int normalColor` — modelUniforms.normalColor has nothing to bind to")
	}
	if !strings.Contains(src, "if (normalColor != 0) {") {
		t.Error("model_frag.glsl does not gate its albedo on normalColor — an untextured model would look different in the editor than in the level")
	}
}
