package main

// detail_render.go — GPU rendering for per-face procedural detail (see detail.go).
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

func (p *progUniforms) u(name string) int32 {
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

var detail detailRenderer

// DetailInit compiles the detail programs and builds the rock template mesh.
// Must be called once with the GL context current (see EditorInit).
func DetailInit() {
	shaderDir := filepath.Join(executablePath, "shaders")
	sp := func(name string) string { return filepath.Join(shaderDir, name) }

	dispShared := mustReadShader(sp("detail_common.glsl"))
	scatShared := mustReadShader(sp("detail_scatter_common.glsl"))

	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }

	// --- Displacement (tessellation) programs ---
	detail.dispColor = mk(mustTessProgram(
		sp("detail_vert.glsl"), sp("detail.tesc"), sp("detail.tese"),
		sp("fragment.glsl"), dispShared))
	detail.dispShadow = mk(mustTessProgram(
		sp("detail_vert.glsl"), sp("detail.tesc"), sp("detail_shadow.tese"),
		sp("shadow_frag.glsl"), dispShared))
	detail.dispCube = mk(mustTessProgram(
		sp("detail_vert.glsl"), sp("detail.tesc"), sp("detail_shadow_cube.tese"),
		sp("shadow_cube_frag.glsl"), dispShared))

	// --- Scatter (instanced) programs ---
	detail.scatColor = mk(mustScatterProgram(
		sp("detail_scatter_vert.glsl"), sp("fragment.glsl"), scatShared))
	detail.scatShadow = mk(mustScatterProgram(
		sp("detail_scatter_shadow_vert.glsl"), sp("shadow_frag.glsl"), scatShared))
	detail.scatCube = mk(mustScatterProgram(
		sp("detail_scatter_cube_vert.glsl"), sp("shadow_cube_frag.glsl"), scatShared))

	// Rock template mesh (reuse the biome rock builder); attribute layout matches
	// the explicit locations in the scatter vertex shaders (0=aLocal, 1=aNormal,
	// 2=aParam).
	mesh := buildRockMesh(7)
	detail.rockVerts = int32(len(mesh) / biomeVertexStride)
	gl.GenVertexArrays(1, &detail.rockVAO)
	gl.GenBuffers(1, &detail.rockVBO)
	gl.BindVertexArray(detail.rockVAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, detail.rockVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(mesh)*4, gl.Ptr(mesh), gl.STATIC_DRAW)
	stride := int32(biomeVertexStride * 4)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 1, gl.FLOAT, false, stride, 6*4)
	gl.BindVertexArray(0)

	detail.initialized = true
	gl.UseProgram(Program)
}

// ---------- shader compilation helpers ----------

func mustReadShader(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("detail shader read %s: %v", path, err))
	}
	return strings.ReplaceAll(strings.TrimRight(string(b), "\n"), "\r\n", "\n")
}

// injectAfterVersion inserts snippet immediately after the shader's first line
// (the #version directive), so shared uniforms/functions are declared before use.
func injectAfterVersion(src, snippet string) string {
	idx := strings.Index(src, "\n")
	if idx < 0 {
		return src + "\n" + snippet
	}
	return src[:idx+1] + "\n" + snippet + "\n" + src[idx+1:]
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

func mustTessProgram(vertPath, tescPath, tesePath, fragPath, shared string) uint32 {
	vs, err := CompileShader(injectAfterVersion(mustReadShader(vertPath), shared), gl.VERTEX_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail vert %s: %v", vertPath, err))
	}
	tcs, err := CompileShader(injectAfterVersion(mustReadShader(tescPath), shared), gl.TESS_CONTROL_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail tesc %s: %v", tescPath, err))
	}
	tes, err := CompileShader(injectAfterVersion(mustReadShader(tesePath), shared), gl.TESS_EVALUATION_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail tese %s: %v", tesePath, err))
	}
	fs, err := CompileShader(mustReadShader(fragPath), gl.FRAGMENT_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail frag %s: %v", fragPath, err))
	}
	prog, err := linkStages(vs, tcs, tes, fs)
	if err != nil {
		panic(fmt.Sprintf("detail tess program (%s): %v", tesePath, err))
	}
	return prog
}

func mustScatterProgram(vertPath, fragPath, shared string) uint32 {
	vs, err := CompileShader(injectAfterVersion(mustReadShader(vertPath), shared), gl.VERTEX_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail scatter vert %s: %v", vertPath, err))
	}
	fs, err := CompileShader(mustReadShader(fragPath), gl.FRAGMENT_SHADER)
	if err != nil {
		panic(fmt.Sprintf("detail scatter frag %s: %v", fragPath, err))
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		panic(fmt.Sprintf("detail scatter program (%s): %v", vertPath, err))
	}
	return prog
}

// ---------- face iteration ----------

// detailFace is a resolved detail-bearing face for rendering.
type detailFace struct {
	spec *DetailSpec
	geo  *Geometry
	tex  string
}

// forEachDetailFace invokes fn for every enabled face carrying a DetailSpec.
func forEachDetailFace(level *Level, fn func(f detailFace)) {
	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			if s.Floor.Enabled && s.Floor.Detail.Enabled {
				fn(detailFace{&s.Floor.Detail, &s.Floor.Geometry, s.Floor.Texture})
			}
			if s.Ceiling.Enabled && s.Ceiling.Detail.Enabled {
				fn(detailFace{&s.Ceiling.Detail, &s.Ceiling.Geometry, s.Ceiling.Texture})
			}
			for wi := range s.Walls {
				w := &s.Walls[wi]
				if w.Visible && w.Detail.Enabled {
					fn(detailFace{&w.Detail, &w.Geometry, w.Texture})
				}
			}
		}
	}
}

// DetailAnyEnabled reports whether any face wants displacement or scatter, so the
// render loop can skip the whole detail pass cheaply.
func DetailAnyEnabled(level *Level) (displace, scatter bool) {
	forEachDetailFace(level, func(f detailFace) {
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

type faceBasis struct {
	origin     mgl32.Vec3
	u, v, n    mgl32.Vec3
	uMax, vMax float32
	ok         bool
}

func vtx(verts []float32, i int) mgl32.Vec3 {
	return mgl32.Vec3{verts[i], verts[i+1], verts[i+2]}
}

// computeFaceBasis derives a plane basis + in-plane bounding rectangle from a
// face's world-space triangle list (5 floats/vertex, 15/triangle).
func computeFaceBasis(verts []float32) faceBasis {
	if len(verts) < 15 {
		return faceBasis{}
	}
	var nrm mgl32.Vec3
	for ti := 0; ti+14 < len(verts); ti += 15 {
		a := vtx(verts, ti)
		b := vtx(verts, ti+5)
		c := vtx(verts, ti+10)
		nrm = nrm.Add(b.Sub(a).Cross(c.Sub(a)))
	}
	if nrm.Len() < 1e-8 {
		return faceBasis{}
	}
	n := nrm.Normalize()

	p0 := vtx(verts, 0)
	tan := vtx(verts, 5).Sub(p0)
	tan = tan.Sub(n.Mul(tan.Dot(n))) // project into plane
	if tan.Len() < 1e-6 {
		tan = perpAxis(n)
	}
	u := tan.Normalize()
	v := n.Cross(u).Normalize()

	uMin, uMax := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	vMin, vMax := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for i := 0; i+4 < len(verts); i += 5 {
		d := vtx(verts, i).Sub(p0)
		du, dv := d.Dot(u), d.Dot(v)
		uMin, uMax = min32(uMin, du), max32(uMax, du)
		vMin, vMax = min32(vMin, dv), max32(vMax, dv)
	}
	origin := p0.Add(u.Mul(uMin)).Add(v.Mul(vMin))
	return faceBasis{origin: origin, u: u, v: v, n: n, uMax: uMax - uMin, vMax: vMax - vMin, ok: true}
}

func perpAxis(n mgl32.Vec3) mgl32.Vec3 {
	if math.Abs(float64(n.Y())) < 0.9 {
		return mgl32.Vec3{0, 1, 0}.Cross(n)
	}
	return mgl32.Vec3{1, 0, 0}.Cross(n)
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// clamp01 clamps v to [0,1].
func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ---------- uniform setters ----------

func setDispUniforms(p *progUniforms, d *DetailSpec, geo *Geometry) {
	gl.Uniform1f(p.u("dispSeed"), float32(d.Seed))
	gl.Uniform1f(p.u("dispAmp"), d.Amplitude)
	gl.Uniform1f(p.u("dispFreq"), d.Frequency)
	gl.Uniform1i(p.u("dispOct"), int32(d.Octaves))
	tess := d.TessLevel
	if tess < 1 {
		tess = 1
	}
	gl.Uniform1f(p.u("tessLevel"), tess)
	setDispBoundary(p, d.EdgeTaper, geo)
}

// detailMaxEdges must match DETAIL_MAX_EDGES in detail_common.glsl.
const detailMaxEdges = 32

// setDispBoundary uploads the taper band width plus the face's boundary edges so
// the shader can fade displacement to 0 at edges shared with adjacent walls.
func setDispBoundary(p *progUniforms, taper float32, geo *Geometry) {
	if taper <= 0 {
		gl.Uniform1f(p.u("edgeTaper"), 0)
		gl.Uniform1i(p.u("numEdges"), 0)
		return
	}
	edges := boundaryEdgesFor(geo)
	n := len(edges)
	if n > detailMaxEdges {
		n = detailMaxEdges
	}
	gl.Uniform1f(p.u("edgeTaper"), taper)
	gl.Uniform1i(p.u("numEdges"), int32(n))
	for i := 0; i < n; i++ {
		a, b := edges[i][0], edges[i][1]
		gl.Uniform3f(p.u(fmt.Sprintf("edgeA[%d]", i)), a.X(), a.Y(), a.Z())
		gl.Uniform3f(p.u(fmt.Sprintf("edgeB[%d]", i)), b.X(), b.Y(), b.Z())
	}
}

// ---------- boundary-edge detection (with per-Geometry cache) ----------

type boundaryEntry struct {
	fp    uint64
	edges [][2]mgl32.Vec3
}

var detailBoundaryCache = map[*Geometry]boundaryEntry{}

// boundaryEdgesFor returns the face's outer boundary edges (world space),
// caching by a fingerprint of the vertex data so it only recomputes when the
// geometry actually changes.
func boundaryEdgesFor(g *Geometry) [][2]mgl32.Vec3 {
	fp := geomFingerprint(g.Vertices)
	if e, ok := detailBoundaryCache[g]; ok && e.fp == fp {
		return e.edges
	}
	edges := computeBoundaryEdges(g.Vertices)
	detailBoundaryCache[g] = boundaryEntry{fp: fp, edges: edges}
	return edges
}

func geomFingerprint(v []float32) uint64 {
	h := uint64(14695981039346656037)
	mix := func(x uint32) { h ^= uint64(x); h *= 1099511628211 }
	mix(uint32(len(v)))
	step := len(v)/17 + 1
	for i := 0; i < len(v); i += step {
		mix(math.Float32bits(v[i]))
	}
	return h
}

// computeBoundaryEdges returns the mesh's boundary edges — those used by exactly
// one triangle. For a triangle-fan floor this is the sector polygon perimeter;
// for a wall quad it is the four quad edges; interior/shared edges (fan diagonals,
// the quad diagonal) are excluded. Vertices are matched by quantized position so
// the de-indexed triangle soup still shares edges.
func computeBoundaryEdges(verts []float32) [][2]mgl32.Vec3 {
	type rec struct {
		a, b  mgl32.Vec3
		count int
	}
	m := map[[6]int64]*rec{}
	q := func(v mgl32.Vec3) [3]int64 {
		return [3]int64{
			int64(math.Round(float64(v.X()) * 1e4)),
			int64(math.Round(float64(v.Y()) * 1e4)),
			int64(math.Round(float64(v.Z()) * 1e4)),
		}
	}
	less := func(a, b [3]int64) bool {
		for i := 0; i < 3; i++ {
			if a[i] != b[i] {
				return a[i] < b[i]
			}
		}
		return false
	}
	add := func(a, b mgl32.Vec3) {
		qa, qb := q(a), q(b)
		if qa == qb {
			return // degenerate edge
		}
		if !less(qa, qb) {
			qa, qb = qb, qa
		}
		k := [6]int64{qa[0], qa[1], qa[2], qb[0], qb[1], qb[2]}
		if r, ok := m[k]; ok {
			r.count++
		} else {
			m[k] = &rec{a: a, b: b, count: 1}
		}
	}
	for ti := 0; ti+14 < len(verts); ti += 15 {
		v0 := vtx(verts, ti)
		v1 := vtx(verts, ti+5)
		v2 := vtx(verts, ti+10)
		add(v0, v1)
		add(v1, v2)
		add(v2, v0)
	}
	var out [][2]mgl32.Vec3
	for _, r := range m {
		if r.count == 1 {
			out = append(out, [2]mgl32.Vec3{r.a, r.b})
		}
	}
	return out
}

// setScatterUniforms sets the per-face scatter uniforms and returns the instance
// count (gridNU*gridNV). Returns 0 when nothing should be drawn.
func setScatterUniforms(p *progUniforms, d *DetailSpec, b faceBasis) int32 {
	spacing := d.ScatterSpacing
	if spacing < 0.05 {
		spacing = 0.05
	}
	const maxSide = 256
	gridNU := int(b.uMax/spacing) + 1
	gridNV := int(b.vMax/spacing) + 1
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

	gl.Uniform3f(p.u("faceOrigin"), b.origin.X(), b.origin.Y(), b.origin.Z())
	gl.Uniform3f(p.u("faceU"), b.u.X(), b.u.Y(), b.u.Z())
	gl.Uniform3f(p.u("faceV"), b.v.X(), b.v.Y(), b.v.Z())
	gl.Uniform3f(p.u("faceNrm"), b.n.X(), b.n.Y(), b.n.Z())
	gl.Uniform1f(p.u("faceUMax"), b.uMax)
	gl.Uniform1f(p.u("faceVMax"), b.vMax)
	gl.Uniform1f(p.u("sSpacing"), spacing)
	gl.Uniform1f(p.u("sDensity"), clamp01(d.ScatterDensity))
	gl.Uniform1f(p.u("sScale"), d.ScatterScale)
	gl.Uniform1f(p.u("sJitter"), clamp01(d.ScatterJitter))
	gl.Uniform1f(p.u("sSeed"), float32(d.Seed))
	gl.Uniform1i(p.u("gridNU"), int32(gridNU))
	return int32(gridNU * gridNV)
}

// detailAlbedo returns a valid GL texture handle for name, falling back to a
// known-loaded texture so an unloaded scatter texture never binds unit 0 to 0.
func detailAlbedo(name, fallback string) uint32 {
	if h, ok := Textures[name]; ok && h != 0 {
		return h
	}
	if h, ok := Textures[fallback]; ok && h != 0 {
		return h
	}
	return Textures["default"]
}

// ---------- render entry points ----------

// RenderDetailColor draws displaced + scattered detail in the main color pass.
// shadowMatrices are the directional light-space matrices from RenderShadowPass.
// Restores the main Program on exit.
func RenderDetailColor(level *Level, shadowMatrices []mgl32.Mat4) {
	if !detail.initialized {
		return
	}
	haveD, haveS := DetailAnyEnabled(level)
	if !haveD && !haveS {
		return
	}
	proj := Projection
	cam := Cam.Matrix

	if haveD {
		p := &detail.dispColor
		UploadLightsTo(p.prog, level.Lights, false)
		UploadShadowUniformsTo(p.prog, level.Lights, shadowMatrices)
		UploadPointShadowUniformsTo(p.prog, level.Lights)
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.u("projection"), 1, false, &proj[0])
		gl.UniformMatrix4fv(p.u("camera"), 1, false, &cam[0])
		gl.Uniform1i(p.u("tex"), 0)
		gl.Uniform1f(p.u("alpha"), 1.0)
		gl.PatchParameteri(gl.PATCH_VERTICES, 3)
		forEachDetailFace(level, func(f detailFace) {
			if !f.spec.HasDisplace() || len(f.geo.Vertices) == 0 {
				return
			}
			setDispUniforms(p, f.spec, f.geo)
			gl.ActiveTexture(gl.TEXTURE0)
			gl.BindTexture(gl.TEXTURE_2D, detailAlbedo(f.tex, "default"))
			gl.BindVertexArray(f.geo.Vao)
			gl.DrawArrays(gl.PATCHES, 0, int32(len(f.geo.Vertices)/5))
		})
	}

	if haveS {
		p := &detail.scatColor
		UploadLightsTo(p.prog, level.Lights, false)
		UploadShadowUniformsTo(p.prog, level.Lights, shadowMatrices)
		UploadPointShadowUniformsTo(p.prog, level.Lights)
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.u("projection"), 1, false, &proj[0])
		gl.UniformMatrix4fv(p.u("camera"), 1, false, &cam[0])
		gl.Uniform1i(p.u("tex"), 0)
		gl.Uniform1f(p.u("alpha"), 1.0)
		gl.BindVertexArray(detail.rockVAO)
		forEachDetailFace(level, func(f detailFace) {
			if !f.spec.HasScatter() || len(f.geo.Vertices) == 0 {
				return
			}
			b := computeFaceBasis(f.geo.Vertices)
			if !b.ok {
				return
			}
			inst := setScatterUniforms(p, f.spec, b)
			if inst <= 0 {
				return
			}
			gl.ActiveTexture(gl.TEXTURE0)
			gl.BindTexture(gl.TEXTURE_2D, detailAlbedo(f.spec.ScatterTex, f.tex))
			gl.DrawArraysInstanced(gl.TRIANGLES, 0, detail.rockVerts, inst)
		})
	}

	gl.BindVertexArray(0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(Program)
}

// RenderDetailDirShadow draws detail into the currently-bound directional shadow
// FBO using light-space matrix lsm. Restores Shadows.Program on exit.
func RenderDetailDirShadow(level *Level, lsm mgl32.Mat4) {
	if !detail.initialized {
		return
	}
	haveD, haveS := DetailAnyEnabled(level)
	if haveD {
		p := &detail.dispShadow
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.u("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.PatchParameteri(gl.PATCH_VERTICES, 3)
		forEachDetailFace(level, func(f detailFace) {
			if !f.spec.HasDisplace() || len(f.geo.Vertices) == 0 {
				return
			}
			setDispUniforms(p, f.spec, f.geo)
			gl.BindVertexArray(f.geo.Vao)
			gl.DrawArrays(gl.PATCHES, 0, int32(len(f.geo.Vertices)/5))
		})
	}
	if haveS {
		p := &detail.scatShadow
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.u("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.BindVertexArray(detail.rockVAO)
		forEachDetailFace(level, func(f detailFace) {
			if !f.spec.HasScatter() || len(f.geo.Vertices) == 0 {
				return
			}
			b := computeFaceBasis(f.geo.Vertices)
			if !b.ok {
				return
			}
			inst := setScatterUniforms(p, f.spec, b)
			if inst > 0 {
				gl.DrawArraysInstanced(gl.TRIANGLES, 0, detail.rockVerts, inst)
			}
		})
	}
	gl.BindVertexArray(0)
	gl.UseProgram(Shadows.Program)
}

// RenderDetailCubeShadow draws detail into the currently-bound point-light cube
// FBO face using lsm, the light position, and its far plane. Restores
// Shadows.CubeProgram on exit.
func RenderDetailCubeShadow(level *Level, lsm mgl32.Mat4, lightPos mgl32.Vec3, farPlane float32) {
	if !detail.initialized {
		return
	}
	haveD, haveS := DetailAnyEnabled(level)
	if haveD {
		p := &detail.dispCube
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.u("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.Uniform3f(p.u("lightPos"), lightPos.X(), lightPos.Y(), lightPos.Z())
		gl.Uniform1f(p.u("farPlane"), farPlane)
		gl.PatchParameteri(gl.PATCH_VERTICES, 3)
		forEachDetailFace(level, func(f detailFace) {
			if !f.spec.HasDisplace() || len(f.geo.Vertices) == 0 {
				return
			}
			setDispUniforms(p, f.spec, f.geo)
			gl.BindVertexArray(f.geo.Vao)
			gl.DrawArrays(gl.PATCHES, 0, int32(len(f.geo.Vertices)/5))
		})
	}
	if haveS {
		p := &detail.scatCube
		gl.UseProgram(p.prog)
		gl.UniformMatrix4fv(p.u("lightSpaceMatrix"), 1, false, &lsm[0])
		gl.Uniform3f(p.u("lightPos"), lightPos.X(), lightPos.Y(), lightPos.Z())
		gl.Uniform1f(p.u("farPlane"), farPlane)
		gl.BindVertexArray(detail.rockVAO)
		forEachDetailFace(level, func(f detailFace) {
			if !f.spec.HasScatter() || len(f.geo.Vertices) == 0 {
				return
			}
			b := computeFaceBasis(f.geo.Vertices)
			if !b.ok {
				return
			}
			inst := setScatterUniforms(p, f.spec, b)
			if inst > 0 {
				gl.DrawArraysInstanced(gl.TRIANGLES, 0, detail.rockVerts, inst)
			}
		})
	}
	gl.BindVertexArray(0)
	gl.UseProgram(Shadows.CubeProgram)
}
