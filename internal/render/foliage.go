package render

// foliage.go — drawing what grows on painted terrain (world/foliage.go).
//
// # Instances, built once
//
// Placement is decided on the CPU, per terrain section, when the section's
// foliage is first needed and again only when something it depends on
// changes (TerrainSection.FoliageGen) — never per frame. A section's
// instances go into one static buffer, 64 bytes each, grouped by mesh and
// sorted within each group by a random rank. Building is budgeted: nearest
// sections first, a few milliseconds a frame, so painting a hillside or
// streaming a forest in never stalls a frame; a terrain seen for the first
// time is built whole, so a level opens (and a headless render shoots) with
// its foliage in place.
//
// # Drawing less of it, further away
//
// Every shape has a draw range (scaled by the player's gfx.FoliageDistance).
// Over the outer part of that range a batch thins out: because its
// instances are in random-rank order, drawing only the first n of them is an
// even thinning, so the CPU draws a prefix sized for the section's nearest
// point and the vertex shader drops each instance whose rank its own
// distance no longer admits, fading it out over a narrow band (and shrinking
// grass into the ground as it goes) rather than popping. Past a
// shape's LOD distance its far mesh — a fraction of the triangles — is drawn
// instead. And past grass range the ground itself carries the meadow
// (terrain_frag.glsl's cover tint), which is what lets grass stop at a few
// dozen units without the landscape going bald.
//
// # Shading
//
// Lit per vertex through biome_light.glsl, like the scatter's foliage: a
// blade is too thin for per-pixel lighting to show, and one shadow tap per
// vertex costs a fraction of one per pixel. Leaves and blades are lit from
// both sides and let light through toward the viewer (sun behind a canopy
// glows). Tree crowns shade as round masses — their cards carry the crown's
// normal (foliage_mesh.go) — and grass shades half as the ground it stands
// on, so a meadow reads as one surface, not a haze of blades. Cutout cards
// antialias through alpha-to-coverage, on in the opaque pass.
//
// # Shadows
//
// Trees, shrubs, rocks, ferns and deadwood cast into the directional
// cascades (not point lights), within foliageShadowRange of the camera, when
// gfx.FoliageShadows is on. Grass does not: a meadow's self-shadowing is
// what the cover tint and the blades' own base darkening already paint.

import (
	"log"
	"math"
	"os"
	"sort"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// foliageInstanceFloats is one instance's size in the instance buffer.
const foliageInstanceFloats = 16

// foliageShadowRange is how far from the camera foliage casts, in units.
const foliageShadowRange = 110

// foliageBuildBudget is how long one frame may spend placing foliage.
const foliageBuildBudget = 3 * time.Millisecond

// foliageShapeInfo is how the renderer treats one shape.
type foliageShapeInfo struct {
	// rng is its draw range, in units, before FoliageDistance.
	rng float32
	// lod is the distance past which its far mesh is drawn.
	lod float32
	// casts is whether it casts shadows.
	casts bool
	// shrink sinks it into the ground as it fades out, rather than only
	// dissolving it — for the low, dense things whose fade band is visible
	// as a line across a field.
	shrink bool
	// cheap lights it with one shadow tap per vertex rather than a 3x3
	// average: the low, dense ground cover, which is most of the vertices
	// drawn and where one blade's flicker is lost among thousands.
	cheap bool
}

var foliageShapes = [world.FoliageShapeCount]foliageShapeInfo{
	world.FoliageGrass:       {rng: 42, lod: 10, shrink: true, cheap: true},
	world.FoliageTallGrass:   {rng: 60, lod: 16, shrink: true, cheap: true},
	world.FoliageFlower:      {rng: 55, lod: 14, shrink: true, cheap: true},
	world.FoliageFern:        {rng: 65, lod: 20, casts: true},
	world.FoliageShrub:       {rng: 120, lod: 30, casts: true},
	world.FoliageBroadleaf:   {rng: 380, lod: 55, casts: true},
	world.FoliageConifer:     {rng: 380, lod: 55, casts: true},
	world.FoliageRock:        {rng: 160, lod: 30, casts: true},
	world.FoliagePebbles:     {rng: 35, lod: 12, cheap: true},
	world.FoliageMoss:        {rng: 48, lod: 12, cheap: true},
	world.FoliageVine:        {rng: 80, lod: 22},
	world.FoliageReed:        {rng: 75, lod: 22, cheap: true},
	world.FoliageMushroom:    {rng: 30, lod: 10, cheap: true},
	world.FoliageLitter:      {rng: 32, lod: 10, shrink: true, cheap: true},
	world.FoliageDeadwood:    {rng: 90, lod: 28, casts: true},
	world.FoliagePalm:        {rng: 380, lod: 55, casts: true},
	world.FoliageCactus:      {rng: 220, lod: 40, casts: true},
	world.FoliageAcacia:      {rng: 420, lod: 60, casts: true},
	world.FoliageJunglePlant: {rng: 90, lod: 24, casts: true},
	world.FoliageDeadBush:    {rng: 110, lod: 28, casts: true},
	world.FoliageIce:         {rng: 220, lod: 40, casts: true},
}

// foliageMaxRange is the longest any shape is drawn, before
// FoliageDistance: sections further than this are neither built nor drawn.
var foliageMaxRange = func() float32 {
	m := float32(0)
	for _, s := range foliageShapes {
		m = max(m, s.rng)
	}
	return m
}()

// FoliageDistanceScale is the player's foliage distance multiplier.
func FoliageDistanceScale() float32 {
	return min(max(gfx.FoliageDistance, gfx.FoliageDistanceMin), gfx.FoliageDistanceMax)
}

// foliageTemplate is one mesh's slice of the shared buffers: its vertices
// start at base, its indices at first.
type foliageTemplate struct{ base, first, count int32 }

type foliageRenderer struct {
	initialized bool
	color       progUniforms
	colorCheap  progUniforms
	shadow      progUniforms
	vao, vbo    uint32
	ebo         uint32
	atlas       uint32
	templates   [world.FoliageShapeCount][][foliageLODs]foliageTemplate
	cache       map[*world.Terrain]*foliageTerrainGPU
	// warm is set for a terrain seen for the first time: its foliage is
	// built whole that frame rather than on the budget.
	scratch []float32
}

var foliage foliageRenderer

type foliageTerrainGPU struct {
	sections []foliageSectionGPU
	seen     bool
	fresh    bool
}

type foliageSectionGPU struct {
	built   bool
	gen     uint64
	vbo     uint32
	batches []foliageBatchGPU
	lo, hi  mgl32.Vec3
	count   int
}

type foliageBatchGPU struct {
	shape        world.FoliageShape
	variant      int
	first, count int32
}

// FoliageInit compiles the foliage programs, builds every template mesh and
// draws the atlas. Called once with the GL context current, from
// InitWorldPasses. Like TerrainInit, a shader directory without the foliage
// stages is logged and the pass stays off.
func FoliageInit() {
	paths := []string{"foliage_common.glsl", "foliage_vert.glsl", "foliage_frag.glsl", "foliage_shadow_vert.glsl", "foliage_shadow_frag.glsl"}
	for _, p := range paths {
		if _, err := os.Stat(biomeShaderPath(p)); err != nil {
			log.Printf("foliage: %v — terrain foliage will not be drawn until the shader directory has the foliage_*.glsl stages", err)
			return
		}
	}
	common := mustReadShader(biomeShaderPath("foliage_common.glsl"))
	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }
	// One lighting evaluation per vertex, a 3x3-averaged cascade tap — the
	// scatter foliage's own choice, for its reason (see GrassInit).
	foliage.color = mk(mustBiomeProgram(biomeShaderPath("foliage_vert.glsl"), biomeShaderPath("foliage_frag.glsl"),
		biomeLightSnippet(1, false, false)+"\n"+common, biomeLightSnippet(0, false, false)))
	// Ground cover's: one tap (foliageShapeInfo.cheap).
	foliage.colorCheap = mk(mustBiomeProgram(biomeShaderPath("foliage_vert.glsl"), biomeShaderPath("foliage_frag.glsl"),
		biomeLightSnippet(0, false, false)+"\n"+common, biomeLightSnippet(0, false, false)))
	foliage.shadow = mk(mustBiomeProgram(biomeShaderPath("foliage_shadow_vert.glsl"), biomeShaderPath("foliage_shadow_frag.glsl"),
		common, ""))

	// Every template, into one indexed buffer: a blade's segments and a
	// card's corners share their vertices, so each is shaded once rather
	// than once per triangle touching it — the vertex stage, not the pixels,
	// is where foliage spends its time.
	var verts []float32
	var idx []uint32
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		foliage.templates[s] = make([][foliageLODs]foliageTemplate, world.FoliageVariants[s])
		for v := 0; v < world.FoliageVariants[s]; v++ {
			for lod := 0; lod < foliageLODs; lod++ {
				uv, ui := indexFoliageMesh(buildFoliageTemplate(s, v, lod))
				foliage.templates[s][v][lod] = foliageTemplate{
					base:  int32(len(verts) / foliageVertexFloats),
					first: int32(len(idx)),
					count: int32(len(ui)),
				}
				verts = append(verts, uv...)
				idx = append(idx, ui...)
			}
		}
	}
	gl.GenVertexArrays(1, &foliage.vao)
	gl.GenBuffers(1, &foliage.vbo)
	gl.GenBuffers(1, &foliage.ebo)
	gl.BindVertexArray(foliage.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, foliage.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, foliage.ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(idx)*4, gl.Ptr(idx), gl.STATIC_DRAW)
	stride := int32(foliageVertexFloats * 4)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, stride, 6*4)
	gl.EnableVertexAttribArray(3)
	gl.VertexAttribPointerWithOffset(3, 3, gl.FLOAT, false, stride, 8*4)
	for i := uint32(4); i < 8; i++ {
		gl.EnableVertexAttribArray(i)
		gl.VertexAttribDivisor(i, 1)
	}
	gl.BindVertexArray(0)

	foliage.atlas = uploadFoliageAtlas(buildFoliageAtlas())
	foliage.cache = map[*world.Terrain]*foliageTerrainGPU{}
	foliage.initialized = true
}

// indexFoliageMesh turns a triangle list into unique vertices and indices.
func indexFoliageMesh(tris []float32) ([]float32, []uint32) {
	seen := map[[foliageVertexFloats]float32]uint32{}
	var verts []float32
	idx := make([]uint32, 0, len(tris)/foliageVertexFloats)
	for i := 0; i+foliageVertexFloats <= len(tris); i += foliageVertexFloats {
		var k [foliageVertexFloats]float32
		copy(k[:], tris[i:i+foliageVertexFloats])
		n, ok := seen[k]
		if !ok {
			n = uint32(len(verts) / foliageVertexFloats)
			seen[k] = n
			verts = append(verts, k[:]...)
		}
		idx = append(idx, n)
	}
	return verts, idx
}

// drawTemplate draws count instances of t.
func drawTemplate(t foliageTemplate, count int32) {
	gl.DrawElementsInstancedBaseVertex(gl.TRIANGLES, t.count, gl.UNSIGNED_INT, gl.PtrOffset(int(t.first)*4), count, t.base)
}

func (g *foliageTerrainGPU) release() {
	for i := range g.sections {
		g.sections[i].release()
	}
	g.sections = nil
}

func (s *foliageSectionGPU) release() {
	if s.vbo != 0 {
		gl.DeleteBuffers(1, &s.vbo)
	}
	*s = foliageSectionGPU{}
}

// syncFoliage brings the foliage of every terrain up to date, within the
// frame's budget, and frees what departed terrains left. Called from
// SyncTerrainMeshes.
func syncFoliage(lvl *world.Level) {
	if !foliage.initialized {
		return
	}
	for _, g := range foliage.cache {
		g.seen = false
	}
	type job struct {
		t    *world.Terrain
		g    *foliageTerrainGPU
		i    int
		s    *world.TerrainSection
		dist float32
		must bool
	}
	var jobs []job
	cam := gfx.Cam.Position
	reach := foliageMaxRange * FoliageDistanceScale()
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		t := ch.Terrain
		if t == nil {
			continue
		}
		g := foliage.cache[t]
		if g == nil {
			g = &foliageTerrainGPU{fresh: true}
			foliage.cache[t] = g
		}
		g.seen = true
		secs := t.Sections()
		if len(g.sections) != len(secs) {
			g.release()
			g.sections = make([]foliageSectionGPU, len(secs))
		}
		if len(t.Paint) == 0 {
			// Nothing grows; drop whatever used to.
			for i := range g.sections {
				if g.sections[i].built {
					g.sections[i].release()
				}
			}
			g.fresh = false
			continue
		}
		model := ch.ModelMatrix()
		for i, s := range secs {
			gs := &g.sections[i]
			if gs.built && gs.gen == s.FoliageGen {
				continue
			}
			if s.Empty() {
				if gs.built {
					gs.release()
				}
				gs.built, gs.gen = true, s.FoliageGen
				continue
			}
			lo, hi := transformBox(model, s.Lo, s.Hi)
			d := boxDistance(cam, lo, hi)
			if d > reach+20 && !gs.built {
				continue
			}
			jobs = append(jobs, job{t: t, g: g, i: i, s: s, dist: d, must: g.fresh})
		}
		g.fresh = false
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].dist < jobs[b].dist })
	start := time.Now()
	for n, j := range jobs {
		if n > 0 && !j.must && time.Since(start) > foliageBuildBudget {
			break
		}
		fs := j.t.BuildFoliage(j.s)
		foliage.upload(&j.g.sections[j.i], fs)
		j.g.sections[j.i].gen = j.s.FoliageGen
	}
	for t, g := range foliage.cache {
		if !g.seen {
			g.release()
			delete(foliage.cache, t)
		}
	}
}

// boxDistance is how far p is from the box [lo, hi]; 0 inside it.
func boxDistance(p, lo, hi mgl32.Vec3) float32 {
	var d2 float32
	for k := 0; k < 3; k++ {
		v := max(lo[k]-p[k], 0, p[k]-hi[k])
		d2 += v * v
	}
	return float32(math.Sqrt(float64(d2)))
}

func (r *foliageRenderer) upload(gs *foliageSectionGPU, fs *world.FoliageSection) {
	gs.built = true
	gs.batches = gs.batches[:0]
	gs.lo, gs.hi, gs.count = fs.Lo, fs.Hi, fs.Count
	if fs.Count == 0 {
		if gs.vbo != 0 {
			gl.DeleteBuffers(1, &gs.vbo)
			gs.vbo = 0
		}
		return
	}
	need := fs.Count * foliageInstanceFloats
	if cap(r.scratch) < need {
		r.scratch = make([]float32, need)
	}
	buf := r.scratch[:0]
	for _, b := range fs.Batches {
		gs.batches = append(gs.batches, foliageBatchGPU{shape: b.Shape, variant: b.Variant,
			first: int32(len(buf) / foliageInstanceFloats), count: int32(len(b.Instances))})
		for _, in := range b.Instances {
			seed := float32(math.Mod(float64(in.Rank)*7919.13, 1))
			buf = append(buf,
				in.Pos[0], in.Pos[1], in.Pos[2], in.Scale,
				in.Up[0], in.Up[1], in.Up[2], in.Yaw,
				in.Color[0], in.Color[1], in.Color[2], in.Rank,
				in.Accent[0], in.Accent[1], in.Accent[2], seed)
		}
	}
	if gs.vbo == 0 {
		gl.GenBuffers(1, &gs.vbo)
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, gs.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(buf)*4, gl.Ptr(buf), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
}

// ReleaseFoliage frees every section's instances — for a GL context about
// to go away, or a test that wants to start clean.
func ReleaseFoliage() {
	for t, g := range foliage.cache {
		g.release()
		delete(foliage.cache, t)
	}
}

// bindInstances points the instance attributes at batch b of buffer vbo.
func bindInstances(vbo uint32, first int32) {
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	stride := int32(foliageInstanceFloats * 4)
	off := uintptr(first) * uintptr(stride)
	for i := uint32(0); i < 4; i++ {
		gl.VertexAttribPointerWithOffset(4+i, 4, gl.FLOAT, false, stride, off+uintptr(i*16))
	}
}

// foliageWind is the wind every plant bends in: direction (xz) and strength.
var foliageWind = struct {
	dir      mgl32.Vec2
	strength float32
}{mgl32.Vec2{0.8, 0.6}, 1}

func (r *foliageRenderer) commonUniforms(p *progUniforms, camPos mgl32.Vec3) {
	gl.Uniform3f(p.U("camPos"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1f(p.U("time"), BiomeWindTime)
	gl.Uniform2f(p.U("windDir"), foliageWind.dir[0], foliageWind.dir[1])
	gl.Uniform1f(p.U("windStrength"), foliageWind.strength)
	gl.Uniform1i(p.U("atlas"), 0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, r.atlas)
}

// eachFoliageBatch walks every drawable batch: the chunk's model matrix, the
// section, the batch, its range and the count and LOD to draw it with.
//
// The LOD is chosen per section, by its nearest point. Choosing it per
// instance — drawing a section the LOD distance cuts through once with each
// mesh, each draw dropping the instances on the other side in the vertex
// stage — was measured and lost: the dropped instances' vertices still cost
// more than the near mesh on the few extra instances it saves.
func eachFoliageBatch(lvl *world.Level, camPos mgl32.Vec3, cull *Frustum, maxDist float32, only func(world.FoliageShape) bool,
	fn func(model mgl32.Mat4, gs *foliageSectionGPU, b *foliageBatchGPU, rng float32, count int32, lod int)) {
	scale := FoliageDistanceScale()
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !terrainDrawable(ch) {
			continue
		}
		g := foliage.cache[ch.Terrain]
		if g == nil {
			continue
		}
		model := ch.ModelMatrix()
		for si := range g.sections {
			gs := &g.sections[si]
			if gs.count == 0 || gs.vbo == 0 {
				continue
			}
			wlo, whi := transformBox(model, gs.lo, gs.hi)
			d := boxDistance(camPos, wlo, whi)
			if d > maxDist {
				continue
			}
			if cull != nil && !AABBInFrustum(*cull, wlo, whi) {
				continue
			}
			for bi := range gs.batches {
				b := &gs.batches[bi]
				if only != nil && !only(b.shape) {
					continue
				}
				info := foliageShapes[b.shape]
				rng := info.rng * scale
				if d > rng {
					continue
				}
				// The prefix every instance the section's nearest point could
				// still admit: foliage_common.glsl keeps rank < keep * 1.06.
				keep := 1 - world.SmoothStep(rng*0.35, rng, d)
				n := int32(math.Ceil(float64(float32(b.count) * min(keep*1.06, 1))))
				if n <= 0 {
					continue
				}
				lod := 0
				if d > info.lod*scale {
					lod = 1
				}
				fn(model, gs, b, rng, min(n, b.count), lod)
			}
		}
	}
}

// RenderFoliage draws every terrain's foliage in the opaque pass, after the
// ground. Returns how many instances it submitted.
func RenderFoliage(lvl *world.Level, projView mgl32.Mat4, frustum *Frustum, frame ShadowFrame) int {
	if !foliage.initialized || lvl == nil || len(foliage.cache) == 0 {
		return 0
	}
	drawn := 0
	cam := gfx.Cam.Position
	// Two programs — the full lighting and ground cover's cheap one — so
	// two walks, each with its program bound once.
	for _, cheap := range [2]bool{false, true} {
		p := &foliage.color
		if cheap {
			p = &foliage.colorCheap
		}
		started := false
		var current mgl32.Mat4
		eachFoliageBatch(lvl, cam, frustum, foliageMaxRange*FoliageDistanceScale(),
			func(s world.FoliageShape) bool { return foliageShapes[s].cheap == cheap },
			func(model mgl32.Mat4, gs *foliageSectionGPU, b *foliageBatchGPU, rng float32, count int32, lod int) {
				if !started {
					uploadBiomeLighting(p.prog, lvl, frame)
					pv := projView
					gl.UniformMatrix4fv(p.U("projView"), 1, false, &pv[0])
					foliage.commonUniforms(p, cam)
					gl.Uniform3f(p.U("barkColor"), 0.30, 0.24, 0.18)
					gl.Uniform1i(p.U("alphaToCoverage"), boolToInt32(gl.IsEnabled(gl.SAMPLE_ALPHA_TO_COVERAGE)))
					gl.BindVertexArray(foliage.vao)
					started = true
					current = mgl32.Mat4{}
				}
				if model != current {
					current = model
					setFoliageModel(p, current)
				}
				info := foliageShapes[b.shape]
				gl.Uniform1f(p.U("drawRange"), rng)
				gl.Uniform1i(p.U("shrinkFade"), boolToInt32(info.shrink))
				bindInstances(gs.vbo, b.first)
				drawTemplate(foliage.templates[b.shape][b.variant][lod], count)
				drawn += int(count)
			})
	}
	if drawn > 0 {
		gl.BindVertexArray(0)
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)
		gl.UseProgram(gfx.Program)
	}
	return drawn
}

// setFoliageModel uploads a chunk's placement and its normal matrix, which
// the vertex stage would otherwise invert per vertex.
func setFoliageModel(p *progUniforms, m mgl32.Mat4) {
	gl.UniformMatrix4fv(p.U("model"), 1, false, &m[0])
	nm := m.Mat3().Inv().Transpose()
	gl.UniformMatrix3fv(p.U("normalMatrix"), 1, false, &nm[0])
}

// RenderFoliageDirShadow draws the casting foliage into the bound cascade
// layer with light-space matrix lsm. Restores Shadows.Program on exit.
func RenderFoliageDirShadow(lvl *world.Level, lsm mgl32.Mat4, cascade int, cull *Frustum) {
	if !foliage.initialized || lvl == nil || !gfx.FoliageShadows || len(foliage.cache) == 0 || cascade > 2 {
		return
	}
	p := &foliage.shadow
	started := false
	cam := gfx.Cam.Position
	var current mgl32.Mat4
	eachFoliageBatch(lvl, cam, cull, foliageShadowRange*FoliageDistanceScale(),
		func(s world.FoliageShape) bool { return foliageShapes[s].casts },
		func(model mgl32.Mat4, gs *foliageSectionGPU, b *foliageBatchGPU, rng float32, count int32, lod int) {
			if !started {
				gl.UseProgram(p.prog)
				m := lsm
				gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &m[0])
				foliage.commonUniforms(p, cam)
				gl.BindVertexArray(foliage.vao)
				started = true
			}
			if model != current {
				current = model
				setFoliageModel(p, current)
			}
			gl.Uniform1f(p.U("drawRange"), rng)
			gl.Uniform1i(p.U("shrinkFade"), 0)
			bindInstances(gs.vbo, b.first)
			// The far mesh casts: a shadow is soft enough that nobody counts
			// its leaves, and a cascade redraws everything in it.
			drawTemplate(foliage.templates[b.shape][b.variant][1], count)
		})
	if started {
		gl.BindVertexArray(0)
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)
		gl.UseProgram(Shadows.Program)
	}
}
