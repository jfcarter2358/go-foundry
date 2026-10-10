package render

// nanite_vis_source.go — the visibility buffer's programs, made from the
// world program's own two stages. Phase 4 of the Nanite plan
// (docs/nanite/PHASE_4.md; the pass itself is nanite_vis.go).
//
// The visibility buffer draws a triangle's ID where the forward path drew its
// colour, and shades each pixel afterwards from that ID. For the picture to be
// the forward path's, both halves have to compute what the world program
// computes, and the world program is two files that change (fragment.glsl had
// three other sessions' edits in it while this was written). So rather than a
// second copy of either, the sources are taken as they are and transformed:
//
//   - visVertexSource: vertex.glsl with its per-instance matrix read from the
//     cull's instance buffer, picked by the command record the draw's base
//     instance points at, and the command's number passed to the fragment
//     stage. Everything else — the position, gl_Position invariant — is the
//     file's own, so the ID pass writes exactly the depth the forward pass
//     would have.
//   - materialFragmentSource: the material pass's fragment stage, which is
//     fragment.glsl with its main renamed and its varyings made globals,
//     preceded by vertex.glsl as a function (vx_main) the pass runs once for
//     each corner of the pixel's triangle, and followed by nanite_material_
//     frag.glsl, which fetches the triangle, runs the corners, interpolates
//     what the rasteriser would have — perspective-correct, from the 2D
//     homogeneous barycentrics — and calls fragment.glsl's main.
//
// Screen-space derivatives are the one thing a full-screen pass cannot take:
// neighbouring pixels belong to other triangles, often of other objects, so
// dFdx of a reconstructed value is a difference between unrelated surfaces.
// fragment.glsl takes four (position and texture coordinate, x and y) and
// samples four material textures; those are rewritten to read analytic
// derivatives the material pass computes from the barycentrics (nvDPdx,
// nvDUVdx, …) and textureGrad. gl_FrontFacing becomes the sign of the
// triangle's homogeneous determinant.
//
// Every rewrite is checked: a pattern fragment.glsl or vertex.glsl no longer
// contains is an error, and the visibility buffer is then not used — never a
// pass that compiles and shades something else (nanite_vis_source_test.go
// runs them against the files in the repository).

import (
	"fmt"
	"regexp"
	"strings"
)

// visVaryings are vertex.glsl's outputs, which are fragment.glsl's inputs.
var visVaryings = []string{"fragTexCoord", "fragWorldPos", "fragNormal", "fragViewDepth", "fragBakedLight", "fragBakedIndirect"}

var (
	reVersion      = regexp.MustCompile(`(?m)^\s*#version[^\n]*\n`)
	reAttribute    = regexp.MustCompile(`(?m)^(\s*)layout\s*\(\s*location\s*=\s*\d+\s*\)\s*in\s+(\w+)\s+(\w+)\s*;`)
	reOut          = regexp.MustCompile(`(?m)^(\s*)out\s+(\w+)\s+(\w+)\s*;`)
	reIn           = regexp.MustCompile(`(?m)^(\s*)in\s+(\w+)\s+(\w+)\s*;`)
	reUniform      = regexp.MustCompile(`(?m)^\s*uniform\s+\w+\s+(\w+)\s*(\[[^\]]*\])?\s*;[^\n]*\n`)
	reFunctionDef  = regexp.MustCompile(`(?m)^\s*\w+\s+(\w+)\s*\([^;{)]*\)\s*\{`)
	reInvariant    = regexp.MustCompile(`(?m)^\s*invariant\s+gl_Position\s*;[^\n]*\n`)
	reMain         = regexp.MustCompile(`\bvoid\s+main\s*\(\s*\)`)
	reInstanceAttr = regexp.MustCompile(`(?m)^\s*layout\s*\(\s*location\s*=\s*7\s*\)\s*in\s+mat4\s+instanceModel\s*;`)
	reSphereAttr   = regexp.MustCompile(`(?m)^\s*layout\s*\(\s*location\s*=\s*13\s*\)\s*in\s+vec4\s+instanceSphere\s*;`)
	reWindAttr     = regexp.MustCompile(`(?m)^\s*layout\s*\(\s*location\s*=\s*14\s*\)\s*in\s+vec4\s+instanceWind\s*;`)
)

// visVertexSource is the ID pass's vertex stage: vertSrc (vertex.glsl with
// the skinning already spliced in) with its instance matrix read from the
// cull's instance buffer and the command record's number passed on.
func visVertexSource(vertSrc string) (string, error) {
	if !reInstanceAttr.MatchString(vertSrc) {
		return "", fmt.Errorf("vertex.glsl: no per-instance matrix at location 7 to read from the instance buffer")
	}
	if !reMain.MatchString(vertSrc) {
		return "", fmt.Errorf("vertex.glsl: no main")
	}
	// The placement's wind (wind_common.glsl), read from the instance record
	// with its matrix: the ID pass must bend a tree as the forward pass would.
	if !reSphereAttr.MatchString(vertSrc) || !reWindAttr.MatchString(vertSrc) {
		return "", fmt.Errorf("vertex.glsl: no per-instance sphere and wind at locations 13 and 14 to read from the instance buffer")
	}
	vertSrc = reSphereAttr.ReplaceAllString(vertSrc, "vec4 instanceSphere;")
	vertSrc = reWindAttr.ReplaceAllString(vertSrc, "vec4 instanceWind;")
	src := reInstanceAttr.ReplaceAllString(vertSrc, `mat4 instanceModel;
// The visibility buffer's command record (render/nanite_vis.go): a = first
// index, base vertex, instance, material bin; b = the record's number + 1.
layout(location = 11) in uvec4 visRecA;
layout(location = 12) in uvec4 visRecB;
struct VisInstance {
    mat4 model;
    vec4 sphere;
    uvec4 flags;
    vec4 wind;
};
layout(std430, binding = 0) readonly buffer VisInstances {
    VisInstance visInstances[];
};
flat out uint visCmd;`)
	src = reMain.ReplaceAllString(src, "void nvVertexMain()")
	return src + `

void main() {
    instanceModel = visInstances[visRecA.z].model;
    instanceSphere = visInstances[visRecA.z].sphere;
    instanceWind = visInstances[visRecA.z].wind;
    nvVertexMain();
    visCmd = visRecB.x;
}
`, nil
}

// materialFragmentSource assembles the material pass's fragment stage from
// the world program's fragment source (fragment.glsl with its shared code
// spliced in), its vertex source and the pass's own main.
func materialFragmentSource(vertSrc, fragSrc, mainSrc string) (string, error) {
	frag, err := materialFragmentPart(fragSrc)
	if err != nil {
		return "", err
	}
	vert, err := materialVertexPart(vertSrc, fragSrc)
	if err != nil {
		return "", err
	}
	main := reVersion.ReplaceAllString(mainSrc, "")
	return frag + "\n// ---- vertex.glsl, as a function (render/nanite_vis_source.go) ----\n" + vert +
		"\n// ---- the material pass (nanite_material_frag.glsl) ----\n" + main, nil
}

// materialFragmentPart is fragment.glsl with its varyings as globals, its
// main renamed nvShadeMain, and its derivatives, material samples and facing
// read from what the material pass reconstructs.
func materialFragmentPart(fragSrc string) (string, error) {
	src := fragSrc
	// The varyings, as globals the pass assigns.
	found := map[string]bool{}
	src = reIn.ReplaceAllStringFunc(src, func(m string) string {
		g := reIn.FindStringSubmatch(m)
		found[g[3]] = true
		return g[1] + g[2] + " " + g[3] + ";"
	})
	for _, v := range visVaryings {
		if !found[v] {
			return "", fmt.Errorf("fragment.glsl: no input %s", v)
		}
	}
	type rewrite struct{ from, to string }
	for _, r := range []rewrite{
		{"dFdx(fragWorldPos)", "nvDPdx"},
		{"dFdy(fragWorldPos)", "nvDPdy"},
		{"dFdx(fragTexCoord)", "nvDUVdx"},
		{"dFdy(fragTexCoord)", "nvDUVdy"},
		{"gl_FrontFacing", "nvFrontFacing"},
		{"texture(tex,", "nvTexture(tex,"},
		{"texture(normalMap,", "nvTexture(normalMap,"},
	} {
		if !strings.Contains(src, r.from) {
			return "", fmt.Errorf("fragment.glsl: %q is gone; the material pass's rewrite of it needs updating", r.from)
		}
		src = strings.ReplaceAll(src, r.from, r.to)
	}
	// The blend pair is optional (a file without blending is still fine).
	src = strings.ReplaceAll(src, "texture(blendTex,", "nvTexture(blendTex,")
	src = strings.ReplaceAll(src, "texture(blendMask,", "nvTexture(blendMask,")
	if strings.Contains(src, "dFdx(") || strings.Contains(src, "dFdy(") {
		return "", fmt.Errorf("fragment.glsl: a screen-space derivative the material pass does not reconstruct")
	}
	// A visibility-buffer pixel is opaque and already decided: the ID pass
	// made the cutout's call, so the shading never discards.
	src = strings.ReplaceAll(src, "discard;", ";")
	loc := reMain.FindStringIndex(src)
	if loc == nil {
		return "", fmt.Errorf("fragment.glsl: no main")
	}
	prelude := `
// ---- the material pass's reconstruction (render/nanite_vis_source.go) ----
vec3 nvDPdx, nvDPdy;
vec2 nvDUVdx, nvDUVdy;
bool nvFrontFacing;
vec4 nvTexture(sampler2D s, vec2 uv) {
    return textureGrad(s, uv, nvDUVdx, nvDUVdy);
}
`
	src = src[:loc[0]] + prelude + "void nvShadeMain()" + src[loc[1]:]
	return src, nil
}

// materialVertexPart is vertex.glsl as a function the material pass calls:
// its attributes globals the pass assigns, its outputs and gl_Position
// globals renamed vx_*, its functions and main renamed vx_*, and the uniforms
// fragment.glsl declares too dropped (the one declaration serves both).
func materialVertexPart(vertSrc, fragSrc string) (string, error) {
	src := reVersion.ReplaceAllString(vertSrc, "")
	src = reInvariant.ReplaceAllString(src, "")
	if !reMain.MatchString(src) {
		return "", fmt.Errorf("vertex.glsl: no main")
	}
	src = reAttribute.ReplaceAllString(src, "${1}${2} ${3};")
	outs := map[string]bool{}
	src = reOut.ReplaceAllStringFunc(src, func(m string) string {
		g := reOut.FindStringSubmatch(m)
		outs[g[3]] = true
		return g[1] + g[2] + " vx_" + g[3] + ";"
	})
	for _, v := range visVaryings {
		if !outs[v] {
			return "", fmt.Errorf("vertex.glsl: no output %s", v)
		}
		src = regexp.MustCompile(`\b`+v+`\b`).ReplaceAllString(src, "vx_"+v)
	}
	// Uniforms both stages declare: the fragment stage's declaration serves.
	fragUniforms := map[string]bool{}
	for _, m := range reUniform.FindAllStringSubmatch(fragSrc, -1) {
		fragUniforms[m[1]] = true
	}
	src = reUniform.ReplaceAllStringFunc(src, func(m string) string {
		if fragUniforms[reUniform.FindStringSubmatch(m)[1]] {
			return ""
		}
		return m
	})
	// Functions, main included, renamed: fragment.glsl defines some of the
	// same names (waterHeight is in both, on purpose).
	for _, m := range reFunctionDef.FindAllStringSubmatch(src, -1) {
		name := m[1]
		if name == "if" || name == "for" || name == "while" || name == "switch" {
			continue
		}
		src = regexp.MustCompile(`\b`+name+`\s*\(`).ReplaceAllString(src, "vx_"+name+"(")
	}
	if !strings.Contains(src, "gl_Position") {
		return "", fmt.Errorf("vertex.glsl: no gl_Position")
	}
	src = strings.ReplaceAll(src, "gl_Position", "vx_Position")
	return "vec4 vx_Position;\n" + src, nil
}
