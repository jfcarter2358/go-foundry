package render

// gi_debug.go — seeing what GI sees (`gi_debug` in the developer console).
//
//   - lighting: every surface drawn white, so the light is all that is left —
//     set in fragment.glsl through the giDebug uniform (uploadGITo).
//   - probes: each probe a dot, coloured by the light it has facing the
//     camera; a switched-off one (buried in a wall) red.
//   - field: the distance field sphere-traced from the camera
//     (gi_debug_comp.glsl), so its shape can be laid against the level's.

import (
	"log"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
)

const giProbeVert = `#version 430
uniform mat4 projView;
uniform int cascade;
uniform float pointScale;
out vec3 vColor;
void main() {
    int i = gl_VertexID;
    ivec3 slot = ivec3(i % giDims.x, (i / giDims.x) % giDims.y, i / (giDims.x * giDims.y));
    ivec3 lo = ivec3(giCascade[cascade].xyz);
    float s = giCascade[cascade].w;
    ivec3 wi = lo + giSlot(slot - lo);
    ivec2 tile = giTile(slot, cascade);
    vec4 pd = texelFetch(giProbeData, tile, 0);
    vec3 p = vec3(wi) * s + pd.xyz;
    vec3 toCam = normalize(giCamPos - p);
    vec2 size = vec2(textureSize(giIrradianceAtlas, 0));
    vColor = mod(pd.w, 2.0) < 0.5 ? vec3(1.0, 0.1, 0.1)
        : textureLod(giIrradianceAtlas, giAtlasUV(tile, toCam, GI_IRR_TILE, size), 0.0).rgb;
    gl_Position = projView * vec4(p, 1.0);
    gl_PointSize = clamp(pointScale / max(gl_Position.w, 0.1), 2.0, 12.0);
}
`

const giProbeFrag = `#version 430
in vec3 vColor;
out vec4 outColor;
void main() {
    vec2 d = gl_PointCoord * 2.0 - 1.0;
    if (dot(d, d) > 1.0) discard;
    outColor = vec4(vColor, 1.0);
}
`

const giBlitVert = `#version 430
out vec2 vUV;
void main() {
    vec2 p = vec2((gl_VertexID << 1) & 2, gl_VertexID & 2);
    vUV = p;
    gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}
`

const giBlitFrag = `#version 430
uniform sampler2D img;
in vec2 vUV;
out vec4 outColor;
void main() { outColor = vec4(texture(img, vUV).rgb, 1.0); }
`

type giDebugState struct {
	built  bool
	failed bool
	probes progUniforms
	blit   progUniforms
	vao    uint32
	img    uint32
	imgW   int32
	imgH   int32
}

var giDbg giDebugState

func (d *giDebugState) ensure() bool {
	if d.built || d.failed {
		return d.built
	}
	lightModel, err := gfx.LightModelSource()
	if err != nil {
		d.failed = true
		return false
	}
	link := func(vs, fs string) (progUniforms, error) {
		v, err := gfx.CompileShader(vs, gl.VERTEX_SHADER)
		if err != nil {
			return progUniforms{}, err
		}
		f, err := gfx.CompileShader(fs, gl.FRAGMENT_SHADER)
		if err != nil {
			return progUniforms{}, err
		}
		p, err := linkStages(v, f)
		return progUniforms{prog: p, loc: map[string]int32{}}, err
	}
	var e1, e2 error
	d.probes, e1 = link(gfx.InjectAfterVersion(giProbeVert, lightModel), giProbeFrag)
	d.blit, e2 = link(giBlitVert, giBlitFrag)
	if e1 != nil || e2 != nil {
		logGIDebug(e1, e2)
		d.failed = true
		return false
	}
	gl.GenVertexArrays(1, &d.vao)
	d.built = true
	return true
}

func logGIDebug(errs ...error) {
	for _, e := range errs {
		if e != nil {
			log.Printf("gi debug: %v", e)
		}
	}
}

// drawGIDebug draws the probes or the field over the frame, for the modes
// that are drawn rather than shaded.
func drawGIDebug() {
	if !gi.active || (GIDebug != GIDebugProbes && GIDebug != GIDebugField) || !giDbg.ensure() {
		return
	}
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	switch GIDebug {
	case GIDebugProbes:
		p := &giDbg.probes
		gi.uploadProbeUniformsTo(p)
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.U("projView"), 1, false, &projView[0])
		gl.Uniform1f(p.U("pointScale"), 60)
		gl.Enable(gl.PROGRAM_POINT_SIZE)
		gl.BindVertexArray(giDbg.vao)
		for c := 0; c < gi.t.cascades; c++ {
			gl.Uniform1i(p.U("cascade"), int32(c))
			gl.DrawArrays(gl.POINTS, 0, giDimX*giDimY*giDimZ)
		}
		gl.BindVertexArray(0)
		gl.Disable(gl.PROGRAM_POINT_SIZE)
	case GIDebugField:
		var vp [4]int32
		gl.GetIntegerv(gl.VIEWPORT, &vp[0])
		w, h := max(vp[2]/2, 1), max(vp[3]/2, 1)
		if giDbg.img == 0 || giDbg.imgW != w || giDbg.imgH != h {
			if giDbg.img != 0 {
				gl.DeleteTextures(1, &giDbg.img)
			}
			giDbg.img = newGITexture2D(gl.RGBA8, int(w), int(h), gl.LINEAR)
			giDbg.imgW, giDbg.imgH = w, h
		}
		p := &gi.debugProg
		gi.field.uploadSdfUniformsTo(p, giSdfUnitBase)
		gl.UseProgram(p.prog)
		inv := projView.Inv()
		gl.UniformMatrix4fv(p.U("invProjView"), 1, false, &inv[0])
		eye := gfx.Cam.Position
		gl.Uniform3f(p.U("eye"), eye[0], eye[1], eye[2])
		gl.BindImageTexture(0, giDbg.img, 0, false, 0, gl.WRITE_ONLY, gl.RGBA8)
		gl.DispatchCompute(uint32((w+7)/8), uint32((h+7)/8), 1)
		gl.MemoryBarrier(gl.TEXTURE_FETCH_BARRIER_BIT)

		b := &giDbg.blit
		gl.UseProgram(b.prog)
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, giDbg.img)
		gl.Uniform1i(b.U("img"), 0)
		gl.Disable(gl.DEPTH_TEST)
		gl.BindVertexArray(giDbg.vao)
		gl.DrawArrays(gl.TRIANGLES, 0, 3)
		gl.BindVertexArray(0)
		gl.Enable(gl.DEPTH_TEST)
	}
	gl.UseProgram(gfx.Program)
}
