package render

// terrain.go — drawing chunks' block terrain (world/terrain.go).
//
// # One buffer per section
//
// A terrain's surface is built in sections (world/terrain_mesh.go); each
// uploads as its own vertex and index buffer, re-uploaded only when its
// generation moves, so mining a block re-uploads the one to eight sections
// around it rather than the ground. Each section is frustum-culled on its own
// box and drawn with one DrawElements.
//
// # Shading
//
// Its own program (terrain_vert/terrain_frag.glsl), lit by the same block the
// scatter and sprites use — uploadBiomeLighting feeds it every uniform the
// world shader is fed — so the ground takes the level's lights, cascade and
// cube shadows, cloud shadows and fog exactly as the brushes beside it do.
// Textures are projected three ways and blended by the normal (triplanar),
// since a block has sides as well as a top, and the biome painted on a face
// tints it toward the colour of what grows there — more with distance, so a
// meadow still reads as a meadow past the range its grass is drawn to.
//
// # Shadows
//
// The ground casts into the cascades and the point-light cubes through the
// shadow programs themselves: its buffer puts position at location 0, which
// is all shadow_vert.glsl / shadow_cube_vert.glsl read. See
// shadowChunkTerrain, called from the shadow pass's chunk walk.

import (
	"log"
	"math"
	"os"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// terrainVertexWords is one vertex's size in the vertex buffer, in 4-byte
// words: position (3 floats), normal (3 floats), layer weights (4 bytes),
// cover (4 bytes), ground channel weights (8 bytes).
const terrainVertexWords = 10

// Texture units for the four layers. Unit 0 is the albedo unit every program
// uses; the other three are units biome-lit programs leave free (the world
// program's blend, blend-mask and normal-map units — see shadow.go's unit map).
var terrainLayerUnits = [world.TerrainLayerCount]int32{0, gfx.BlendTexUnit, gfx.BlendMaskUnit, gfx.NormalMapUnit}

type terrainRenderer struct {
	initialized bool
	prog        progUniforms
	cache       map[*world.Terrain]*terrainMesh
}

var terrains terrainRenderer

// terrainMesh is one terrain's GPU state.
type terrainMesh struct {
	sections []terrainSectionGPU
	seen     bool
	// scratch buffers, kept between re-uploads.
	verts []uint32
	idx   []uint32
}

type terrainSectionGPU struct {
	vao, vbo, ebo uint32
	gen           uint64
	count         int32
	lo, hi        mgl32.Vec3
}

// TerrainInit compiles the terrain program. Called once with the GL context
// current, from InitWorldPasses.
//
// A shader directory without the terrain stages — a project carrying its own
// copy of resources/shaders from before terrain existed — is not a broken
// install the way a missing world shader is: every level that has no terrain
// still runs. So it is logged and the pass stays off, rather than panicking
// at startup; terrain is then solid and walkable but not drawn.
func TerrainInit() {
	vert, frag := biomeShaderPath("terrain_vert.glsl"), biomeShaderPath("terrain_frag.glsl")
	for _, path := range []string{vert, frag} {
		if _, err := os.Stat(path); err != nil {
			log.Printf("terrain: %v — terrain will not be drawn until the shader directory has terrain_vert.glsl and terrain_frag.glsl", err)
			return
		}
	}
	// The vertex stage only needs biomeViewDepth from the lighting block, so
	// it takes the cheapest variant; the fragment stage takes the full one
	// (5x5 PCF, point shadows, cascade blend) because it is floor, beside
	// brush floors, and a cheaper filter would show as a seam where they meet.
	p := mustBiomeProgram(vert, frag,
		biomeLightSnippet(0, false, false), biomeLightSnippet(2, true, true))
	terrains.prog = progUniforms{prog: p, loc: map[string]int32{}}
	terrains.cache = map[*world.Terrain]*terrainMesh{}
	groundArrayInit()
	terrains.initialized = true
}

// ReleaseTerrainMeshes frees every cached terrain mesh — for a GL context
// about to go away, or a test that wants to start clean.
func ReleaseTerrainMeshes() {
	for t, m := range terrains.cache {
		m.release()
		delete(terrains.cache, t)
	}
}

func (m *terrainMesh) release() {
	for i := range m.sections {
		m.sections[i].release()
	}
	m.sections = nil
}

func (s *terrainSectionGPU) release() {
	if s.vao != 0 {
		gl.DeleteVertexArrays(1, &s.vao)
		gl.DeleteBuffers(1, &s.vbo)
		gl.DeleteBuffers(1, &s.ebo)
	}
	*s = terrainSectionGPU{}
}

// SyncTerrainMeshes brings every terrain's GPU mesh up to date and frees the
// meshes of terrains that are no longer in the level — deleted, or replaced
// wholesale by an undo. Called once a frame, before any pass draws.
func SyncTerrainMeshes(lvl *world.Level) {
	if !terrains.initialized || lvl == nil {
		return
	}
	for _, m := range terrains.cache {
		m.seen = false
	}
	for ci := range lvl.Chunks {
		t := lvl.Chunks[ci].Terrain
		if t == nil {
			continue
		}
		m := terrains.cache[t]
		if m == nil {
			m = &terrainMesh{}
			terrains.cache[t] = m
		}
		m.seen = true
		m.sync(t)
	}
	for t, m := range terrains.cache {
		if !m.seen {
			m.release()
			delete(terrains.cache, t)
		}
	}
	syncGroundArray(lvl)
	syncFoliage(lvl)
}

// sync re-uploads every section whose faces changed since the last sync.
func (m *terrainMesh) sync(t *world.Terrain) {
	secs := t.Sections()
	if len(m.sections) != len(secs) {
		m.release()
		m.sections = make([]terrainSectionGPU, len(secs))
	}
	for i, s := range secs {
		g := &m.sections[i]
		if g.gen == s.Gen && (g.vao != 0 || s.Empty()) {
			continue
		}
		g.gen = s.Gen
		g.lo, g.hi = s.Lo, s.Hi
		if s.Empty() {
			g.count = 0
			continue
		}
		m.upload(g, s)
	}
}

func (m *terrainMesh) upload(g *terrainSectionGPU, s *world.TerrainSection) {
	nv := len(s.Faces) * 4
	if cap(m.verts) < nv*terrainVertexWords {
		m.verts = make([]uint32, nv*terrainVertexWords)
	}
	if cap(m.idx) < len(s.Faces)*6 {
		m.idx = make([]uint32, len(s.Faces)*6)
	}
	v := m.verts[:nv*terrainVertexWords]
	idx := m.idx[:0]
	k := 0
	f32 := math.Float32bits
	for fi := range s.Faces {
		f := &s.Faces[fi]
		base := uint32(fi * 4)
		for c := 0; c < 4; c++ {
			p, n := f.Corners[c], f.Normals[c]
			v[k+0], v[k+1], v[k+2] = f32(p[0]), f32(p[1]), f32(p[2])
			v[k+3], v[k+4], v[k+5] = f32(n[0]), f32(n[1]), f32(n[2])
			v[k+6] = f.Weights[c]
			v[k+7] = f.Cover[c]
			v[k+8] = uint32(f.Ground[c])
			v[k+9] = uint32(f.Ground[c] >> 32)
			k += terrainVertexWords
		}
		for t := 0; t < 2; t++ {
			for _, c := range f.Tri(t) {
				idx = append(idx, base+uint32(c))
			}
		}
	}
	if g.vao == 0 {
		gl.GenVertexArrays(1, &g.vao)
		gl.GenBuffers(1, &g.vbo)
		gl.GenBuffers(1, &g.ebo)
		gl.BindVertexArray(g.vao)
		gl.BindBuffer(gl.ARRAY_BUFFER, g.vbo)
		stride := int32(terrainVertexWords * 4)
		gl.EnableVertexAttribArray(0)
		gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
		gl.EnableVertexAttribArray(1)
		gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
		gl.EnableVertexAttribArray(2)
		gl.VertexAttribPointerWithOffset(2, 4, gl.UNSIGNED_BYTE, true, stride, 6*4)
		gl.EnableVertexAttribArray(3)
		gl.VertexAttribPointerWithOffset(3, 4, gl.UNSIGNED_BYTE, true, stride, 7*4)
		gl.EnableVertexAttribArray(4)
		gl.VertexAttribPointerWithOffset(4, 4, gl.UNSIGNED_BYTE, true, stride, 8*4)
		gl.EnableVertexAttribArray(5)
		gl.VertexAttribPointerWithOffset(5, 4, gl.UNSIGNED_BYTE, true, stride, 9*4)
		// Anything else a shadow program might read stays off: the skin
		// attributes are gated by the `skinned` uniform, which the shadow walk
		// sets to 0 before the chunks.
		gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, g.ebo)
	} else {
		gl.BindVertexArray(g.vao)
		gl.BindBuffer(gl.ARRAY_BUFFER, g.vbo)
	}
	gl.BufferData(gl.ARRAY_BUFFER, len(v)*4, gl.Ptr(v), gl.STATIC_DRAW)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(idx)*4, gl.Ptr(idx), gl.STATIC_DRAW)
	gl.BindVertexArray(0)
	g.count = int32(len(idx))
}

// drawSections issues one DrawElements per section that survives the cull,
// cull being in world space and model the chunk's placement. It returns how
// many sections it drew.
func (m *terrainMesh) drawSections(model mgl32.Mat4, cull *Frustum) int {
	drawn := 0
	for i := range m.sections {
		s := &m.sections[i]
		if s.count == 0 || s.vao == 0 {
			continue
		}
		if cull != nil {
			wlo, whi := transformBox(model, s.lo, s.hi)
			if !AABBInFrustum(*cull, wlo, whi) {
				continue
			}
		}
		gl.BindVertexArray(s.vao)
		gl.DrawElementsWithOffset(gl.TRIANGLES, s.count, gl.UNSIGNED_INT, 0)
		drawn++
	}
	return drawn
}

// transformBox is the world box around a local box under model.
func transformBox(model mgl32.Mat4, lo, hi mgl32.Vec3) (mgl32.Vec3, mgl32.Vec3) {
	wlo := mgl32.Vec3{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
	whi := wlo.Mul(-1)
	for i := 0; i < 8; i++ {
		c := mgl32.Vec3{lo.X(), lo.Y(), lo.Z()}
		if i&1 != 0 {
			c[0] = hi.X()
		}
		if i&2 != 0 {
			c[1] = hi.Y()
		}
		if i&4 != 0 {
			c[2] = hi.Z()
		}
		r := model.Mul4x1(c.Vec4(1))
		for k := 0; k < 3; k++ {
			wlo[k] = min(wlo[k], r[k])
			whi[k] = max(whi[k], r[k])
		}
	}
	return wlo, whi
}

// terrainDrawable reports whether chunk ch has ground to draw this frame.
func terrainDrawable(ch *world.Chunk) bool {
	return ch.Terrain != nil && ch.IsEnabled()
}

// TerrainCoverFar is the distance at which a painted biome's ground tint
// reaches its full strength — about where its grass has thinned out.
const TerrainCoverFar = 55

// RenderTerrain draws every enabled chunk's terrain in the opaque pass. It
// uses its own program and puts the world program back when done. Returns the
// number of sections drawn.
func RenderTerrain(lvl *world.Level, projView mgl32.Mat4, frustum *Frustum, frame ShadowFrame) int {
	if !terrains.initialized || lvl == nil {
		return 0
	}
	started := false
	drawn := 0
	p := &terrains.prog
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !terrainDrawable(ch) {
			continue
		}
		m := terrains.cache[ch.Terrain]
		if m == nil {
			continue
		}
		if frustum != nil {
			lo, hi := ch.WorldBounds()
			if !AABBInFrustum(*frustum, lo, hi) {
				continue
			}
		}
		if !started {
			uploadBiomeLighting(p.prog, lvl, frame)
			pv := projView
			gl.UniformMatrix4fv(p.U("projView"), 1, false, &pv[0])
			// The ground always receives: BiomeShadows.Receive is the
			// scatter's own switch, and a floor that stopped taking shadows
			// with the grass would be a floor a brush does not match.
			gl.Uniform1i(p.U("receiveShadows"), 1)
			gl.Uniform1i(p.U("smoothShading"), boolToInt32(gfx.SmoothLighting))
			gl.Uniform3f(p.U("camPos"), gfx.Cam.Position.X(), gfx.Cam.Position.Y(), gfx.Cam.Position.Z())
			gl.Uniform1f(p.U("coverFar"), TerrainCoverFar*FoliageDistanceScale())
			for l, unit := range terrainLayerUnits {
				gl.Uniform1i(p.U("layer"+string(rune('0'+l))), unit)
			}
			gl.Uniform1i(p.U("groundTex"), GroundTexUnit)
			gl.ActiveTexture(gl.TEXTURE0 + GroundTexUnit)
			gl.BindTexture(gl.TEXTURE_2D_ARRAY, ground.tex)
			started = true
		}
		t := ch.Terrain
		model := ch.ModelMatrix()
		gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
		gl.Uniform1f(p.U("textureScale"), t.EffectiveTextureScale())
		var on [4]float32
		for l, name := range t.Layers {
			if name == "" {
				continue
			}
			_ = gfx.EnsureTextureLoaded(name)
			on[l] = 1
			gl.ActiveTexture(gl.TEXTURE0 + uint32(terrainLayerUnits[l]))
			gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[name])
		}
		gl.Uniform4f(p.U("layerOn"), on[0], on[1], on[2], on[3])
		gl.Uniform1i(p.U("normalColor"), boolToInt32(!t.HasTexture()))
		bindGroundUniforms(p, t)
		drawn += m.drawSections(model, frustum)
	}
	if started {
		gl.BindVertexArray(0)
		gl.ActiveTexture(gl.TEXTURE0 + GroundTexUnit)
		gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
		gl.ActiveTexture(gl.TEXTURE0)
		gl.UseProgram(gfx.Program)
	}
	return drawn
}

// shadowChunkTerrain draws ch's terrain into whichever shadow map the caller
// has bound, with the caller's shadow program: position at location 0 is
// all that program reads. current tracks the model uniform the way
// shadowChunkBrushes does.
func shadowChunkTerrain(ch *world.Chunk, cull *Frustum, modelUniform int32, current *mgl32.Mat4) {
	if !terrains.initialized || !terrainDrawable(ch) {
		return
	}
	m := terrains.cache[ch.Terrain]
	if m == nil {
		return
	}
	model := ch.ModelMatrix()
	if model != *current {
		*current = model
		gl.UniformMatrix4fv(modelUniform, 1, false, &current[0])
	}
	m.drawSections(model, cull)
	gl.BindVertexArray(0)
}
