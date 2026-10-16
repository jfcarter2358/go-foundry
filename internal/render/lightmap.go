package render

// lightmap.go — a compiled level's lightmaps on the GPU (compile/lightmap.go;
// docs/lighting/PLAN.md, phase 5): its pages as one RGBA16F texture array,
// bound to every world program's `lightmaps` on LightmapTexUnit. A lightmapped
// surface's baked-light rows say which texel of which page it reads
// (levelio.lightmapRows), and fragment.glsl reads it there.
//
// Uploaded once a level — when the static world in the level changes — and
// released when a level without lightmaps replaces it.

import (
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// LightmapTexUnit is the unit the lightmap array binds to in a world program:
// another of the units the point shadows' cubes gave back.
const LightmapTexUnit = 6

type lightmapState struct {
	tex   uint32
	for_  *world.StaticGeometry
	dummy uint32
}

var lightmapGPU lightmapState

// syncLightmaps uploads lvl's lightmap pages if they are not the ones on the
// GPU. Called once a frame from DrawWorld; a frame whose level has not
// changed costs a pointer comparison.
func syncLightmaps(lvl *world.Level) {
	l := &lightmapGPU
	var static *world.StaticGeometry
	if lvl != nil {
		static = lvl.Static
	}
	if static == l.for_ {
		return
	}
	l.for_ = static
	if l.tex != 0 {
		gl.DeleteTextures(1, &l.tex)
		l.tex = 0
	}
	if static == nil || len(static.Lightmaps) == 0 || !gfx.TextureUploadsEnabled {
		return
	}
	pages := static.Lightmaps
	size := pages[0].Size
	gl.GenTextures(1, &l.tex)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, l.tex)
	gl.TexStorage3D(gl.TEXTURE_2D_ARRAY, 1, gl.RGBA16F, size, size, int32(len(pages)))
	for i, p := range pages {
		if len(p.Texels) != int(size*size*4) {
			continue
		}
		gl.TexSubImage3D(gl.TEXTURE_2D_ARRAY, 0, 0, 0, int32(i), size, size, 1, gl.RGBA, gl.HALF_FLOAT,
			unsafe.Pointer(&p.Texels[0]))
	}
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
}

// lightmapDummy is a 1×1 one-layer array for `lightmaps` with none loaded.
func lightmapDummy() uint32 {
	l := &lightmapGPU
	if l.dummy == 0 {
		gl.GenTextures(1, &l.dummy)
		gl.BindTexture(gl.TEXTURE_2D_ARRAY, l.dummy)
		px := [4]float32{0, 0, 0, 1}
		gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.RGBA16F, 1, 1, 1, 0, gl.RGBA, gl.FLOAT, gl.Ptr(&px[0]))
		gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
	}
	return l.dummy
}

// uploadLightmapsTo binds the lightmap array (or the stand-in) to prog's
// `lightmaps`.
func uploadLightmapsTo(prog uint32) {
	tex := lightmapGPU.tex
	if tex == 0 {
		tex = lightmapDummy()
	}
	gl.UseProgram(prog)
	gl.ActiveTexture(gl.TEXTURE0 + LightmapTexUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, tex)
	gl.Uniform1i(gfx.UniformLocName(prog, "lightmaps"), LightmapTexUnit)
	gl.ActiveTexture(gl.TEXTURE0)
}
