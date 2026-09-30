package render

// terrain.go — drawing chunks' heightfield ground (world/terrain.go).
//
// # One grid, many patches
//
// A terrain uploads as one vertex buffer holding every grid vertex (position,
// smooth normal, four layer weights) and one index buffer cut into fixed
// slots, one per terrainPatchCells x terrainPatchCells patch of cells. Each
// patch is frustum-culled on its own box and drawn with one DrawElements over
// its slot, holes left out of the indices. Fixed slots are what make editing
// cheap: a brush stroke re-uploads the vertex rows it touched and the index
// slots of the patches around them (Terrain.TakeDirty), not the grid.
//
// # Shading
//
// Its own program (terrain_vert/terrain_frag.glsl), lit by the same block the
// scatter and sprites use — uploadBiomeLighting feeds it every uniform the
// world shader is fed — so the ground takes the level's lights, cascade and
// cube shadows, cloud shadows and fog exactly as the brushes beside it do.
// The ground is lit live, like everything that is not a brush face: a
// compiled level's baked light (and its bounce) belongs to the faces the
// compiler saw, and the compiler never sees a terrain.
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

// terrainPatchCells is a patch's edge, in cells: the unit of culling and of
// index re-upload.
const terrainPatchCells = 32

// terrainVertexFloats is one vertex's size in the vertex buffer, in 4-byte
// words: position (3 floats), normal (3 floats), weights (4 bytes, one word).
const terrainVertexFloats = 7

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
	vao, vbo, ebo  uint32
	cellsX, cellsZ int
	gen            uint64
	patchesX       int
	patches        []terrainPatch
	// scratch buffers, kept between re-uploads.
	verts []float32
	idx   []uint32
	seen  bool
}

type terrainPatch struct {
	// count is how many indices of the patch's slot are live.
	count int32
	// lo, hi are the patch's chunk-local box.
	lo, hi mgl32.Vec3
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
	gl.DeleteVertexArrays(1, &m.vao)
	gl.DeleteBuffers(1, &m.vbo)
	gl.DeleteBuffers(1, &m.ebo)
	m.vao, m.vbo, m.ebo = 0, 0, 0
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
}

// sync re-uploads whatever the terrain's edits since the last sync touched.
func (m *terrainMesh) sync(t *world.Terrain) {
	if m.vao != 0 && m.gen == t.Generation {
		return
	}
	x0, z0, x1, z1, all := t.TakeDirty()
	if m.vao == 0 || m.cellsX != t.CellsX || m.cellsZ != t.CellsZ {
		all = true
	}
	m.gen = t.Generation
	if all {
		m.build(t)
		return
	}
	// A vertex's normal reads its neighbours' heights, so the rows either side
	// of an edit change too; and a cell's split reads all four corners.
	x0, z0 = max(x0-1, 0), max(z0-1, 0)
	x1, z1 = min(x1+1, t.CellsX), min(z1+1, t.CellsZ)
	m.uploadVertexRows(t, z0, z1)
	px0, pz0 := max(x0-1, 0)/terrainPatchCells, max(z0-1, 0)/terrainPatchCells
	px1, pz1 := min(x1, t.CellsX-1)/terrainPatchCells, min(z1, t.CellsZ-1)/terrainPatchCells
	gl.BindVertexArray(m.vao)
	for pz := pz0; pz <= pz1; pz++ {
		for px := px0; px <= px1; px++ {
			m.uploadPatch(t, px, pz)
		}
	}
	gl.BindVertexArray(0)
}

// build (re)creates the buffers for the whole grid.
func (m *terrainMesh) build(t *world.Terrain) {
	if m.vao == 0 {
		gl.GenVertexArrays(1, &m.vao)
		gl.GenBuffers(1, &m.vbo)
		gl.GenBuffers(1, &m.ebo)
	}
	m.cellsX, m.cellsZ = t.CellsX, t.CellsZ
	m.patchesX = (t.CellsX + terrainPatchCells - 1) / terrainPatchCells
	patchesZ := (t.CellsZ + terrainPatchCells - 1) / terrainPatchCells
	m.patches = make([]terrainPatch, m.patchesX*patchesZ)

	gl.BindVertexArray(m.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, m.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, t.VertexCount()*terrainVertexFloats*4, nil, gl.DYNAMIC_DRAW)
	stride := int32(terrainVertexFloats * 4)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 4, gl.UNSIGNED_BYTE, true, stride, 6*4)
	// Anything else a shadow program might read stays off: the skin
	// attributes are gated by the `skinned` uniform, which the shadow walk
	// sets to 0 before the chunks.
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, m.ebo)
	slot := terrainPatchCells * terrainPatchCells * 6
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(m.patches)*slot*4, nil, gl.DYNAMIC_DRAW)
	m.uploadVertexRows(t, 0, t.CellsZ)
	for pz := 0; pz < patchesZ; pz++ {
		for px := 0; px < m.patchesX; px++ {
			m.uploadPatch(t, px, pz)
		}
	}
	gl.BindVertexArray(0)
}

// uploadVertexRows writes grid rows z0..z1 into the vertex buffer. Rows are
// contiguous, so it is one BufferSubData.
func (m *terrainMesh) uploadVertexRows(t *world.Terrain, z0, z1 int) {
	vx := t.VertsX()
	n := (z1 - z0 + 1) * vx * terrainVertexFloats
	if cap(m.verts) < n {
		m.verts = make([]float32, n)
	}
	v := m.verts[:n]
	k := 0
	for z := z0; z <= z1; z++ {
		for x := 0; x < vx; x++ {
			p := t.Vertex(x, z)
			nm := t.SmoothNormal(x, z)
			var w [4]uint8
			for l := 0; l < world.TerrainLayerCount; l++ {
				w[l] = t.Weight(x, z, l)
			}
			packed := uint32(w[0]) | uint32(w[1])<<8 | uint32(w[2])<<16 | uint32(w[3])<<24
			v[k+0], v[k+1], v[k+2] = p.X(), p.Y(), p.Z()
			v[k+3], v[k+4], v[k+5] = nm.X(), nm.Y(), nm.Z()
			v[k+6] = math.Float32frombits(packed)
			k += terrainVertexFloats
		}
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, m.vbo)
	gl.BufferSubData(gl.ARRAY_BUFFER, z0*vx*terrainVertexFloats*4, n*4, gl.Ptr(v))
}

// uploadPatch rebuilds patch (px, pz)'s index slot and box. The VAO must be
// bound (the element buffer binding is VAO state).
func (m *terrainMesh) uploadPatch(t *world.Terrain, px, pz int) {
	cx0, cz0 := px*terrainPatchCells, pz*terrainPatchCells
	cx1, cz1 := min(cx0+terrainPatchCells, t.CellsX), min(cz0+terrainPatchCells, t.CellsZ)
	slot := terrainPatchCells * terrainPatchCells * 6
	if cap(m.idx) < slot {
		m.idx = make([]uint32, slot)
	}
	idx := m.idx[:0]
	vx := t.VertsX()
	lo := mgl32.Vec3{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
	hi := lo.Mul(-1)
	for z := cz0; z < cz1; z++ {
		for x := cx0; x < cx1; x++ {
			if t.IsHole(x, z) {
				continue
			}
			for _, tri := range t.CellCorners(x, z) {
				for _, c := range tri {
					idx = append(idx, uint32(c[1]*vx+c[0]))
					p := t.Vertex(c[0], c[1])
					for i := 0; i < 3; i++ {
						lo[i] = min(lo[i], p[i])
						hi[i] = max(hi[i], p[i])
					}
				}
			}
		}
	}
	pi := pz*m.patchesX + px
	m.patches[pi] = terrainPatch{count: int32(len(idx)), lo: lo, hi: hi}
	if len(idx) > 0 {
		gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, m.ebo)
		gl.BufferSubData(gl.ELEMENT_ARRAY_BUFFER, pi*slot*4, len(idx)*4, gl.Ptr(idx))
	}
}

// drawPatches issues one DrawElements per patch that survives the cull, cull
// being in world space and model the chunk's placement. It returns how many
// patches it drew.
func (m *terrainMesh) drawPatches(model mgl32.Mat4, cull *Frustum) int {
	slot := terrainPatchCells * terrainPatchCells * 6
	drawn := 0
	gl.BindVertexArray(m.vao)
	for pi := range m.patches {
		p := &m.patches[pi]
		if p.count == 0 {
			continue
		}
		if cull != nil {
			wlo, whi := transformBox(model, p.lo, p.hi)
			if !AABBInFrustum(*cull, wlo, whi) {
				continue
			}
		}
		gl.DrawElementsWithOffset(gl.TRIANGLES, p.count, gl.UNSIGNED_INT, uintptr(pi*slot*4))
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

// RenderTerrain draws every enabled chunk's terrain in the opaque pass. It
// uses its own program and puts the world program back when done. Returns the
// number of patches drawn.
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
		if m == nil || m.vao == 0 {
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
			for l, unit := range terrainLayerUnits {
				gl.Uniform1i(p.U("layer"+string(rune('0'+l))), unit)
			}
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
		drawn += m.drawPatches(model, frustum)
	}
	if started {
		gl.BindVertexArray(0)
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
	if m == nil || m.vao == 0 {
		return
	}
	model := ch.ModelMatrix()
	if model != *current {
		*current = model
		gl.UniformMatrix4fv(modelUniform, 1, false, &current[0])
	}
	m.drawPatches(model, cull)
	gl.BindVertexArray(0)
}
