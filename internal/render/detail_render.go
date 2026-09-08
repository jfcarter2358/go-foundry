package render

// detail_render.go — GPU rendering for per-face procedural detail (see go).
//
// Two self-contained pipelines, mirroring the grass.go biome pattern (own GL
// programs, restore the main Program on exit):
//
//   Displacement — the face's existing triangles are drawn as GL_PATCHES through
//   a tessellation program that subdivides + noise-displaces them. It reuses the
//   main fragment shader (color) / shadow fragment shaders (depth) so lighting
//   and shadows behave exactly like normal geometry.
//
//   Scatter — a rock template mesh is instanced across the face plane; the vertex
//   shader places each instance deterministically from the face seed. Also reuses
//   the main/shadow fragment shaders.
//
// Both are drawn in the color pass and in the directional + point shadow depth
// passes, so detail casts and receives accurate shadows.

import (
	"fmt"
	"foundry/internal/detail"
	"foundry/internal/fileutil"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// progUniforms wraps a GL program with a lazy uniform-location cache.
type progUniforms struct {
	prog uint32
	loc  map[string]int32
}

func (p *progUniforms) U(name string) int32 {
	if l, ok := p.loc[name]; ok {
		return l
	}
	l := gl.GetUniformLocation(p.prog, gl.Str(name+"\x00"))
	p.loc[name] = l
	return l
}

// detailRenderer holds the detail GL programs and the shared rock template mesh.
type detailRenderer struct {
	dispColor  progUniforms // tess: color pass
	dispShadow progUniforms // tess: directional shadow depth
	dispCube   progUniforms // tess: point-light cubemap shadow depth
	scatColor  progUniforms // instanced: color pass
	scatShadow progUniforms // instanced: directional shadow depth
	scatCube   progUniforms // instanced: point-light cubemap shadow depth

	rockVAO, rockVBO uint32
	rockVerts        int32

	initialized bool
}

var detailRender detailRenderer

// DetailInit compiles the detail programs and builds the rock template mesh.
// Must be called once with the GL context current (see EditorInit).
func DetailInit() {
	shaderDir := filepath.Join(fileutil.ExecutablePath, "shaders")
	sp := func(name string) string { return filepath.Join(shaderDir, name) }

	dispShared := mustReadShader(sp("detail_common.glsl"))
	scatShared := mustReadShader(sp("detail_scatter_common.glsl"))

	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }

	// --- Displacement (tessellation) programs ---
	detailRender.dispColor = mk(mustTessProgram(
		sp("detail_vert.glsl"), sp("detail.tesc"), sp("detail.tese"),
		sp("fragment.glsl"), dispShared, true))
	detailRender.dispShadow = mk(mustTessProgram(
		sp("detail_vert.glsl"), sp("detail.tesc"), sp("detail_shadow.tese"),
		sp("shadow_frag.glsl"), dispShared, false))
	detailRender.dispCube = mk(mustTessProgram(
		sp("detail_vert.glsl"), sp("detail.tesc"), sp("detail_shadow_cube.tese"),
		sp("shadow_cube_frag.glsl"), dispShared, false))

	// --- Scatter (instanced) programs ---
	detailRender.scatColor = mk(mustScatterProgram(
		sp("detail_scatter_vert.glsl"), sp("fragment.glsl"), scatShared, true))
	detailRender.scatShadow = mk(mustScatterProgram(
		sp("detail_scatter_shadow_vert.glsl"), sp("shadow_frag.glsl"), scatShared, false))
	detailRender.scatCube = mk(mustScatterProgram(
		sp("detail_scatter_cube_vert.glsl"), sp("shadow_cube_frag.glsl"), scatShared, false))

	// Rock template mesh (reuse the biome rock builder); attribute layout matches
	// the explicit locations in the scatter vertex shaders (0=aLocal, 1=aNormal,
	// 2=aParam).
	mesh := buildRockMesh(7)
	detailRender.rockVerts = int32(len(mesh) / BiomeVertexStride)
	gl.GenVertexArrays(1, &detailRender.rockVAO)
	gl.GenBuffers(1, &detailRender.rockVBO)
	gl.BindVertexArray(detailRender.rockVAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, detailRender.rockVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(mesh)*4, gl.Ptr(mesh), gl.STATIC_DRAW)
	stride := int32(BiomeVertexStride * 4)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 1, gl.FLOAT, false, stride, 6*4)
	gl.BindVertexArray(0)

	detailRender.initialized = true
	gl.UseProgram(gfx.Program)
}

// ---------- shader compilation helpers ----------

func mustReadShader(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("detail shader read %s: %v", path, err))
	}
	return strings.ReplaceAll(strings.TrimRight(string(b), "\n"), "\r\n", "\n")
}

func linkStages(shaders ...uint32) (uint32, error) {
	prog := gl.CreateProgram()
	for _, s := range shaders {
		gl.AttachShader(prog, s)
	}
	gl.LinkProgram(prog)
	for _, s := range shaders {
		gl.DeleteShader(s)
	}
	var status int32
	gl.GetProgramiv(prog, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var n int32
		gl.GetProgramiv(prog, gl.INFO_LOG_LENGTH, &n)
		logMsg := strings.Repeat("\x00", int(n+1))
		gl.GetProgramInfoLog(prog, n, nil, gl.Str(logMsg))
		return 0, fmt.Errorf("link failed: %s", logMsg)
	}
	return prog, nil
}

// mustTessProgram and mustScatterProgram take `fogged` rather than deciding for
// themselves, because the same helper links both the colour programs (whose
// fragment stage IS fragment.glsl, and which therefore call applyFog) and the
// two depth-only ones (shadow_frag.glsl / shadow_cube_frag.glsl, which write a
// depth and nothing else). Splicing fog into a depth stage would compile and
// cost nothing, but it would also be a lie about what that stage does.
func mustTessProgram(vertPath, tescPath, tesePath, fragPath, shared string, fogged bool) uint32 {
	vs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(vertPath), shared), gl.VERTEX_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail vert %s: %v", vertPath, err))
	}
	tcs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(tescPath), shared), gl.TESS_CONTROL_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail tesc %s: %v", tescPath, err))
	}
	tes, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(tesePath), shared), gl.TESS_EVALUATION_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail tese %s: %v", tesePath, err))
	}
	fs, err := gfx.CompileShader(detailFragSource(fragPath, fogged), gl.FRAGMENT_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail frag %s: %v", fragPath, err))
	}
	prog, err := linkStages(vs, tcs, tes, fs)
	if err != nil {
		panic(fmt.Sprintf("detail tess program (%s): %v", tesePath, err))
	}
	return prog
}

func mustScatterProgram(vertPath, fragPath, shared string, fogged bool) uint32 {
	vs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(vertPath), shared), gl.VERTEX_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail scatter vert %s: %v", vertPath, err))
	}
	fs, err := gfx.CompileShader(detailFragSource(fragPath, fogged), gl.FRAGMENT_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail scatter frag %s: %v", fragPath, err))
	}

	prog, err := linkStages(vs, fs)
	if err != nil {
		panic(fmt.Sprintf("detail scatter program (%s): %v", vertPath, err))
	}
	return prog
}

// detailFragSource reads a fragment stage and splices fog.glsl in when that
// stage shades. See mustTessProgram's note.
func detailFragSource(fragPath string, fogged bool) string {
	src := mustReadShader(fragPath)
	if !fogged {
		return src
	}
	return gfx.MustInjectFog(src)
}

// ---------- face iteration ----------

// detailFace is a resolved detail-bearing face for rendering. model is the
// owning chunk's ModelMatrix, carried along so the render loop can upload it
// without recomputing it once per face (and can skip the upload entirely
// when it hasn't changed since the last face — see the `current`-tracking
// pattern in RenderDetailColor/DirShadow/CubeShadow, mirroring
// renderSceneShadow's).
type detailFace struct {
	model mgl32.Mat4
	spec  *world.DetailSpec
	geo   *gfx.Geometry
	tex   string
}

// detailPad returns a conservative distance the spec can push geometry beyond
// the face's own bounds: every enabled wave amplitude, the noise amplitude, and
// the height of a scattered instance. Culling uses the sector AABB grown by this
// so a displaced ridge or a rock sitting on the boundary is never culled while
// still on screen.
func detailPad(d *world.DetailSpec) float32 {
	var pad float32
	if d.HasDisplace() {
		for i := 0; i < world.DetailMaxWaves; i++ {
			if w := d.Waves[i]; w.Enabled {
				pad += geom.Abs32(w.Amplitude)
			}
		}
		if d.Noise {
			pad += geom.Abs32(d.NoiseAmplitude)
		}
	}
	if d.HasScatter() {
		pad += geom.Abs32(d.ScatterScale)
	}
	return pad
}

// forEachDetailFace invokes fn for every enabled face carrying a DetailSpec.
// When cull is non-nil, faces whose sector AABB (grown by detailPad) falls
// outside it are skipped.
func forEachDetailFace(level *world.Level, cull *Frustum, fn func(f detailFace)) {
	visible := func(secMin, secMax mgl32.Vec3, d *world.DetailSpec) bool {
		if cull == nil {
			return true
		}
		pad := detailPad(d)
		p := mgl32.Vec3{pad, pad, pad}
		return AABBInFrustum(*cull, secMin.Sub(p), secMax.Add(p))
	}

	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		if !ch.IsEnabled() {
			continue // switched off; see world/chunk_enable.go
		}
		model := ch.ModelMatrix()
		if cull != nil {
			chunkMin, chunkMax := ch.WorldBounds()
			if !AABBInFrustum(*cull, chunkMin, chunkMax) {
				continue
			}
		}
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			var secMin, secMax mgl32.Vec3
			if cull != nil {
				lo, hi := s.LocalAABB()
				secMin, secMax = TransformAABB(model, lo, hi)
			}
			if s.Floor.Enabled && !s.Floor.Hidden && s.Floor.Detail.Enabled && visible(secMin, secMax, &s.Floor.Detail) {
				fn(detailFace{model, &s.Floor.Detail, &s.Floor.Geometry, s.Floor.Texture})
			}
			if s.Ceiling.Enabled && s.Ceiling.Detail.Enabled && visible(secMin, secMax, &s.Ceiling.Detail) {
				fn(detailFace{model, &s.Ceiling.Detail, &s.Ceiling.Geometry, s.Ceiling.Texture})
			}
			for wi := range s.Walls {
				w := &s.Walls[wi]
				if w.Visible && w.Detail.Enabled && visible(secMin, secMax, &w.Detail) {
					fn(detailFace{model, &w.Detail, &w.Geometry, w.Texture})
				}
			}
		}
	}
}

// DetailAnyEnabled reports whether any face wants displacement or scatter, so the
// render loop can skip the whole detail pass cheaply.
func DetailAnyEnabled(level *world.Level) (displace, scatter bool) {
	forEachDetailFace(level, nil, func(f detailFace) {
		if f.spec.HasDisplace() {
			displace = true
		}
		if f.spec.HasScatter() {
			scatter = true
		}
	})
	return
}

// ---------- face plane basis (for scatter) ----------

// ---------- uniform setters ----------

func setDispUniforms(p *progUniforms, d *world.DetailSpec, geo *gfx.Geometry) {
	gl.Uniform1f(p.U("dispSeed"), float32(d.Seed))

	// One normal for the whole face — the same average plane the scatter pass
	// uses. It must NOT be the per-patch geometric normal: a floor/ceiling quad
	// with independent corner heights is non-planar, so its two triangles would
	// displace along different directions and tear apart along their shared
	// diagonal. See detail_common.glsl.
	n := detail.DispNormalFor(geo)
	gl.Uniform3f(p.U("dispNormal"), n.X(), n.Y(), n.Z())

	// Disabled wave slots upload amplitude 0 (rather than being packed out) so a
	// wave's seeded phase stays tied to its slot index.
	for i := 0; i < world.DetailMaxWaves; i++ {
		amp, freq, rot := float32(0), float32(0), float32(0)
		if w := d.Waves[i]; w.Enabled {
			amp, freq, rot = w.Amplitude, w.Frequency, w.Rotation*math.Pi/180
		}
		gl.Uniform1f(p.U(fmt.Sprintf("waveAmp[%d]", i)), amp)
		gl.Uniform1f(p.U(fmt.Sprintf("waveFreq[%d]", i)), freq)
		gl.Uniform1f(p.U(fmt.Sprintf("waveRot[%d]", i)), rot)
	}

	noiseOn := int32(0)
	if d.Noise {
		noiseOn = 1
	}
	gl.Uniform1i(p.U("noiseOn"), noiseOn)
	gl.Uniform1f(p.U("noiseAmp"), d.NoiseAmplitude)
	gl.Uniform1f(p.U("noiseFreq"), d.NoiseFrequency)
	gl.Uniform1i(p.U("noiseOct"), int32(d.NoiseOctaves))

	tess := d.TessLevelFor()
	if tess < 1 {
		tess = 1
	}
	gl.Uniform1f(p.U("tessLevel"), tess)

	// Displaced detail follows the surface's own smoothing setting: one toggle
	// covers the flat face and the detail generated from it. Harmless on the two
	// depth programs, whose shaders drop the unused uniform.
	gl.Uniform1i(p.U("smoothShading"), geo.SmoothFlag())
	gl.Uniform1f(p.U("dispNormalEps"), detail.DispNormalEps(d))

	setDispBoundary(p, d.EdgeTaper, geo)
}

// detailMaxEdges must match DETAIL_MAX_EDGES in detail_common.glsl.
const detailMaxEdges = 32

// setDispBoundary uploads the taper band width plus the face's boundary edges so
// the shader can fade displacement to 0 at edges shared with adjacent walls.
func setDispBoundary(p *progUniforms, taper float32, geo *gfx.Geometry) {
	if taper <= 0 {
		gl.Uniform1f(p.U("edgeTaper"), 0)
		gl.Uniform1i(p.U("numEdges"), 0)
		return
	}
	edges := detail.BoundaryEdgesFor(geo)
	n := len(edges)
	if n > detailMaxEdges {
		n = detailMaxEdges
	}
	gl.Uniform1f(p.U("edgeTaper"), taper)
	gl.Uniform1i(p.U("numEdges"), int32(n))
	for i := 0; i < n; i++ {
		a, b := edges[i][0], edges[i][1]
		gl.Uniform3f(p.U(fmt.Sprintf("edgeA[%d]", i)), a.X(), a.Y(), a.Z())
		gl.Uniform3f(p.U(fmt.Sprintf("edgeB[%d]", i)), b.X(), b.Y(), b.Z())
	}
}

// ---------- boundary-edge detection (with per-Geometry cache) ----------

// setScatterUniforms sets the per-face scatter uniforms and returns the instance
// count (gridNU*gridNV). Returns 0 when nothing should be drawn.
func setScatterUniforms(p *progUniforms, d *world.DetailSpec, b detail.FaceBasis) int32 {
	spacing := d.ScatterSpacing
	if spacing < 0.05 {
		spacing = 0.05
	}
	const maxSide = 256
	gridNU := int(b.UMax/spacing) + 1
	gridNV := int(b.VMax/spacing) + 1
	if gridNU < 1 {
		gridNU = 1
	}
	if gridNV < 1 {
		gridNV = 1
	}
	if gridNU > maxSide {
		gridNU = maxSide
	}
	if gridNV > maxSide {
		gridNV = maxSide
	}

	gl.Uniform3f(p.U("faceOrigin"), b.Origin.X(), b.Origin.Y(), b.Origin.Z())
	gl.Uniform3f(p.U("faceU"), b.U.X(), b.U.Y(), b.U.Z())
	gl.Uniform3f(p.U("faceV"), b.V.X(), b.V.Y(), b.V.Z())
	gl.Uniform3f(p.U("faceNrm"), b.N.X(), b.N.Y(), b.N.Z())
	gl.Uniform1f(p.U("faceUMax"), b.UMax)
	gl.Uniform1f(p.U("faceVMax"), b.VMax)
	gl.Uniform1f(p.U("sSpacing"), spacing)
	gl.Uniform1f(p.U("sDensity"), geom.Clamp01(d.ScatterDensity))
	gl.Uniform1f(p.U("sScale"), d.ScatterScale)
	gl.Uniform1f(p.U("sJitter"), geom.Clamp01(d.ScatterJitter))
	gl.Uniform1f(p.U("sSeed"), float32(d.Seed))
	gl.Uniform1i(p.U("gridNU"), int32(gridNU))
	return int32(gridNU * gridNV)
}

// uploadDetailModel uploads model to p's "model" uniform only when it differs
// from *current, mirroring renderSceneShadow's model-tracking so a level of
// unmoved/unrotated/unscaled chunks doesn't pay for a uniform upload per face.
func uploadDetailModel(p *progUniforms, current *mgl32.Mat4, model mgl32.Mat4) {
	if model == *current {
		return
	}
	*current = model
	gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
}

// detailAlbedoName resolves the texture a detail draw should sample, falling
// back to a known-loaded one so an unloaded scatter texture never binds unit 0
// to 0. Resolution stops at the name rather than the handle because the normal
// map is keyed off the albedo's name and has to follow the same fallback.
func detailAlbedoName(name, fallback string) string {
	if h, ok := gfx.Textures[name]; ok && h != 0 {
		return name
	}
	if h, ok := gfx.Textures[fallback]; ok && h != 0 {
		return fallback
	}
	return "default"
}

// bindDetailTexture binds a detail draw's albedo to unit 0 and its normal map,
// if the art has one, to gfx.NormalMapUnit — so a displaced or scattered
// surface picks up relief on exactly the same terms as an ordinary wall.
func bindDetailTexture(p *progUniforms, name, fallback string) {
	resolved := detailAlbedoName(name, fallback)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[resolved])
	gfx.BindNormalMap(resolved, p.U("hasNormalMap"), p.U("normalMap"))
}

// ---------- render entry points ----------

// RenderDetailColor draws displaced + scattered detail in the main color pass.
// frame carries the directional cascade setup from RenderShadowPass, which the
// detail programs need in order to receive shadows the same way world geometry
// does. Restores the main Program on exit.
func RenderDetailColor(level *world.Level, frame ShadowFrame) {
	if !detailRender.initialized {
		return
	}
	// The color pass is not culled here — it is drawn with the same coverage as
	// before this call gained a cull parameter.
	var cull *Frustum
	haveD, haveS := DetailAnyEnabled(level)
	if !haveD && !haveS {
		return
	}
	proj := gfx.Projection
	cam := gfx.Cam.Matrix

	if haveD {
		p := &detailRender.dispColor
		world.UploadLightsTo(p.prog, level.Lights, false)
		world.UploadFogTo(p.prog, level.Fog, level.Volumes)
		UploadShadowUniformsTo(p.prog, level.Lights, frame)
		UploadPointShadowUniformsTo(p.prog, level.Lights)
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.U("projection"), 1, false, &proj[0])
		gl.UniformMatrix4fv(p.U("camera"), 1, false, &cam[0])
		gl.Uniform1i(p.U("tex"), 0)
		gl.Uniform1f(p.U("alpha"), 1.0)
		gl.PatchParameteri(gl.PATCH_VERTICES, 3)
		model := mgl32.Ident4()
		gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
		forEachDetailFace(level, cull, func(f detailFace) {
			if !f.spec.HasDisplace() || len(f.geo.Vertices) == 0 {
				return
			}
			uploadDetailModel(p, &model, f.model)
			setDispUniforms(p, f.spec, f.geo)
			bindDetailTexture(p, f.tex, "default")
			gl.BindVertexArray(f.geo.Vao)
			gl.DrawArrays(gl.PATCHES, 0, int32(len(f.geo.Vertices)/5))
		})
	}

	if haveS {
		p := &detailRender.scatColor
		world.UploadLightsTo(p.prog, level.Lights, false)
		world.UploadFogTo(p.prog, level.Fog, level.Volumes)
		UploadShadowUniformsTo(p.prog, level.Lights, frame)
		UploadPointShadowUniformsTo(p.prog, level.Lights)
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.U("projection"), 1, false, &proj[0])
		gl.UniformMatrix4fv(p.U("camera"), 1, false, &cam[0])
		gl.Uniform1i(p.U("tex"), 0)
		gl.Uniform1f(p.U("alpha"), 1.0)
		gl.BindVertexArray(detailRender.rockVAO)
		model := mgl32.Ident4()
		gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
		forEachDetailFace(level, cull, func(f detailFace) {
			if !f.spec.HasScatter() || len(f.geo.Vertices) == 0 {
				return
			}
			b := detail.FaceBasisFor(f.geo)
			if !b.Ok {
				return
			}
			uploadDetailModel(p, &model, f.model)
			inst := setScatterUniforms(p, f.spec, b)
			if inst <= 0 {
				return
			}
			// Scattered instances follow the same per-surface toggle; the rock
			// template's normals are per-facet, so this shades them exactly as
			// the derivative path does, just without the derivative.
			gl.Uniform1i(p.U("smoothShading"), f.geo.SmoothFlag())
			bindDetailTexture(p, f.spec.ScatterTex, f.tex)
			gl.DrawArraysInstanced(gl.TRIANGLES, 0, detailRender.rockVerts, inst)
		})
	}

	gl.BindVertexArray(0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(gfx.Program)
}

// RenderDetailDirShadow draws detail into the currently-bound directional shadow
// cascade layer using light-space matrix lsm. cull, when non-nil, restricts the
// faces drawn to those reaching into that cascade's box. Restores
// Shadows.Program on exit.
func RenderDetailDirShadow(level *world.Level, lsm mgl32.Mat4, cull *Frustum) {
	if !detailRender.initialized {
		return
	}
	haveD, haveS := DetailAnyEnabled(level)
	if haveD {
		p := &detailRender.dispShadow
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.PatchParameteri(gl.PATCH_VERTICES, 3)
		model := mgl32.Ident4()
		gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
		forEachDetailFace(level, cull, func(f detailFace) {
			if !f.spec.HasDisplace() || len(f.geo.Vertices) == 0 {
				return
			}
			uploadDetailModel(p, &model, f.model)
			setDispUniforms(p, f.spec, f.geo)
			gl.BindVertexArray(f.geo.Vao)
			gl.DrawArrays(gl.PATCHES, 0, int32(len(f.geo.Vertices)/5))
		})
	}
	if haveS {
		p := &detailRender.scatShadow
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.BindVertexArray(detailRender.rockVAO)
		model := mgl32.Ident4()
		gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
		forEachDetailFace(level, cull, func(f detailFace) {
			if !f.spec.HasScatter() || len(f.geo.Vertices) == 0 {
				return
			}
			b := detail.FaceBasisFor(f.geo)
			if !b.Ok {
				return
			}
			uploadDetailModel(p, &model, f.model)
			inst := setScatterUniforms(p, f.spec, b)
			if inst > 0 {
				gl.DrawArraysInstanced(gl.TRIANGLES, 0, detailRender.rockVerts, inst)
			}
		})
	}
	gl.BindVertexArray(0)
	gl.UseProgram(Shadows.Program)
}

// RenderDetailCubeShadow draws detail into the currently-bound point-light cube
// FBO face using lsm, the light position, and its far plane. cull, when non-nil,
// restricts the faces drawn to those inside that face's frustum. Restores
// Shadows.CubeProgram on exit.
func RenderDetailCubeShadow(level *world.Level, lsm mgl32.Mat4, lightPos mgl32.Vec3, farPlane float32, cull *Frustum) {
	if !detailRender.initialized {
		return
	}
	haveD, haveS := DetailAnyEnabled(level)
	if haveD {
		p := &detailRender.dispCube
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.Uniform3f(p.U("lightPos"), lightPos.X(), lightPos.Y(), lightPos.Z())
		gl.Uniform1f(p.U("farPlane"), farPlane)
		gl.PatchParameteri(gl.PATCH_VERTICES, 3)
		model := mgl32.Ident4()
		gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
		forEachDetailFace(level, cull, func(f detailFace) {
			if !f.spec.HasDisplace() || len(f.geo.Vertices) == 0 {
				return
			}
			uploadDetailModel(p, &model, f.model)
			setDispUniforms(p, f.spec, f.geo)
			gl.BindVertexArray(f.geo.Vao)
			gl.DrawArrays(gl.PATCHES, 0, int32(len(f.geo.Vertices)/5))
		})
	}
	if haveS {
		p := &detailRender.scatCube
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.Uniform3f(p.U("lightPos"), lightPos.X(), lightPos.Y(), lightPos.Z())
		gl.Uniform1f(p.U("farPlane"), farPlane)
		gl.BindVertexArray(detailRender.rockVAO)
		model := mgl32.Ident4()
		gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
		forEachDetailFace(level, cull, func(f detailFace) {
			if !f.spec.HasScatter() || len(f.geo.Vertices) == 0 {
				return
			}
			b := detail.FaceBasisFor(f.geo)
			if !b.Ok {
				return
			}
			uploadDetailModel(p, &model, f.model)
			inst := setScatterUniforms(p, f.spec, b)
			if inst > 0 {
				gl.DrawArraysInstanced(gl.TRIANGLES, 0, detailRender.rockVerts, inst)
			}
		})
	}
	gl.BindVertexArray(0)
	gl.UseProgram(Shadows.CubeProgram)
}
