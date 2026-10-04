package render

// foliage_gpu.go — the GPU cull for terrain foliage.
//
// The CPU path (foliage.go) draws a unit's batch as a rank prefix and lets
// the vertex stage drop every instance its own distance no longer admits.
// Those dropped instances still run every vertex of their mesh, which is why
// choosing a mesh per instance lost to choosing it per unit there. Here the
// choosing moves ahead of the draw: a compute pass (foliage_cull_comp.glsl)
// tests every instance the CPU would have submitted — thinning at its own
// distance, the frustum, and occlusion against a Hi-Z pyramid of the depth
// drawn so far this frame — and writes each survivor's reference into the
// list of every tier it shows in. One indirect draw per (mesh, tier), a
// handful of multi-draws per terrain, and a culled instance costs one
// thread, not a mesh.
//
// The occlusion is this frame's: the terrain, brushes and entities are drawn
// before the foliage, so the depth is copied (a multisample resolve, as the
// volumetrics take theirs) and reduced to a pyramid of furthest depths just
// before the cull. A tree behind a ridge is never drawn, and nothing pops in
// a frame late as a previous frame's depth would make it.
//
// It needs compute shaders, storage buffers and indirect draws, all core in
// the GL 4.3 the engine requires. Where FOUNDRY_FOLIAGE_GPU=0 or a shader
// will not build, the CPU path draws instead. The shadow pass is
// always the CPU path's: it reaches only foliageShadowRange.

import (
	"fmt"
	"log"
	"os"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// FoliageGPUCull switches the GPU cull on where the driver supports it; off
// draws with the CPU path. For A/B timing and tests.
var FoliageGPUCull = true

// foliageCullTile is the most instances one cull workgroup takes.
const foliageCullTile = 1024

// Texture units, clear of the lighting's and the impostors'.
const (
	foliageInstanceUnit = 18
	foliageHiZUnit      = 19
)

// Command groups, by the program that draws them.
const (
	gpuGroupFull = iota
	gpuGroupCheap
	gpuGroupImp
	gpuGroups
)

type gpuCmd struct {
	bucket, tier, group int
	tmpl                foliageTemplate
}

type foliageGPU struct {
	ok       bool
	progs    [foliagePrograms]progUniforms
	paramsAt [foliagePrograms]uint64
	cull     progUniforms
	vao      uint32
	tiles    uint32
	cmdBuf   uint32
	refs     uint32
	refsCap  int
	cmds     []gpuCmd
	// bucketCmd is, per bucket and tier, its command (-1: none).
	bucketCmd [][3]int32
	// groups are each group's first command and count.
	groups [gpuGroups][2]int
	hiz    hiZPyramid

	// Scratch, kept between frames.
	tileData []uint32
	cmdData  []uint32
}

func (g *foliageGPU) active() bool { return g.ok && FoliageGPUCull }

// init brings the cull up if the driver can run it.
func (g *foliageGPU) init() {
	g.ok = false
	if os.Getenv("FOUNDRY_FOLIAGE_GPU") == "0" {
		return
	}
	if err := g.build(); err != nil {
		log.Printf("foliage: GPU cull: %v — foliage is culled on the CPU", err)
		return
	}
	g.ok = true
	log.Printf("foliage: GPU cull on")
}

func (g *foliageGPU) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	g.progs = buildFoliagePrograms(true)
	src := "#version 430\n" + fmt.Sprintf("#define FOLIAGE_SHAPES %d\n#define FOLIAGE_BUCKETS %d\n", world.FoliageShapeCount, foliage.buckets) +
		mustReadShader(biomeShaderPath("foliage_params.glsl")) + "\n" + mustReadShader(biomeShaderPath("foliage_cull_comp.glsl"))
	cs, cerr := gfx.CompileShader(src, gl.COMPUTE_SHADER)
	if cerr != nil {
		return fmt.Errorf("cull shader: %v", cerr)
	}
	prog, lerr := linkStages(cs)
	if lerr != nil {
		return fmt.Errorf("cull program: %v", lerr)
	}
	g.cull = progUniforms{prog: prog, loc: map[string]int32{}}
	if err := g.hiz.init("foliage", foliageHiZUnit); err != nil {
		return err
	}

	// The commands, grouped by program: each bucket's two meshes, and its
	// far tier where it has one.
	g.bucketCmd = make([][3]int32, foliage.buckets)
	for i := range g.bucketCmd {
		g.bucketCmd[i] = [3]int32{-1, -1, -1}
	}
	for grp := 0; grp < gpuGroups; grp++ {
		g.groups[grp][0] = len(g.cmds)
		for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
			info := foliageShapes[s]
			meshGroup := gpuGroupFull
			if info.cheap {
				meshGroup = gpuGroupCheap
			}
			for v := 0; v < world.FoliageVariants[s]; v++ {
				b := foliage.bucket(s, v)
				for tier := 0; tier < 3; tier++ {
					tg, t := meshGroup, foliage.templates[s][v][min(tier, foliageLODs-1)]
					if tier == 2 {
						if info.far == 0 {
							continue
						}
						if info.impostor {
							if foliage.imp.layer[b] < 0 {
								continue
							}
							tg, t = gpuGroupImp, foliage.quad
						}
					}
					if tg != grp {
						continue
					}
					g.bucketCmd[b][tier] = int32(len(g.cmds))
					g.cmds = append(g.cmds, gpuCmd{bucket: b, tier: tier, group: grp, tmpl: t})
				}
			}
		}
		g.groups[grp][1] = len(g.cmds) - g.groups[grp][0]
	}

	gl.GenBuffers(1, &g.tiles)
	gl.GenBuffers(1, &g.cmdBuf)
	gl.GenBuffers(1, &g.refs)
	g.refsCap = 1 << 16
	gl.BindBuffer(gl.ARRAY_BUFFER, g.refs)
	gl.BufferData(gl.ARRAY_BUFFER, g.refsCap*8, nil, gl.STREAM_DRAW)
	gl.GenVertexArrays(1, &g.vao)
	gl.BindVertexArray(g.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, foliage.vbo)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, foliage.ebo)
	bindTemplateAttributes()
	g.bindRefs()
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	return nil
}

// bindRefs points attribute 4 at the reference list; the VAO must be bound.
func (g *foliageGPU) bindRefs() {
	gl.BindBuffer(gl.ARRAY_BUFFER, g.refs)
	gl.EnableVertexAttribArray(4)
	gl.VertexAttribIPointerWithOffset(4, 2, gl.UNSIGNED_INT, 8, 0)
	gl.VertexAttribDivisor(4, 1)
}

// gpuTerrain is one terrain's share of a frame's cull.
type gpuTerrain struct {
	model            *mgl32.Mat4
	tileFirst, tiles int
	cmdFirst         int
	caps             []int32
	submitted        int
}

// render culls and draws this frame's foliage. Returns how many instances
// were submitted to the cull.
func (g *foliageGPU) render(lvl *world.Level, projView mgl32.Mat4, frustum *Frustum, frame ShadowFrame) int {
	r := &foliage
	g.tileData = g.tileData[:0]
	g.cmdData = g.cmdData[:0]
	var terrains []gpuTerrain
	byModel := map[*mgl32.Mat4]int{}
	refs := 0
	for di := range r.draws {
		dr := &r.draws[di]
		if frustum != nil && !AABBInFrustum(*frustum, dr.wlo, dr.whi) {
			continue
		}
		ti, ok := byModel[dr.model]
		if !ok {
			ti = len(terrains)
			byModel[dr.model] = ti
			terrains = append(terrains, gpuTerrain{model: dr.model, caps: make([]int32, r.buckets)})
		}
		t := &terrains[ti]
		for bi := range dr.u.batches {
			b := &dr.u.batches[bi]
			if r.nan.takes(b.shape) {
				continue
			}
			p := r.params[b.shape]
			if dr.d > p.rng {
				continue
			}
			n := min(int32(float32(b.count)*foliageKeepRank(p, dr.d)+0.999), b.count)
			bucket := r.bucket(b.shape, b.variant)
			for off := int32(0); off < n; off += foliageCullTile {
				cnt := min(n-off, foliageCullTile)
				// Tiles are gathered per terrain below, so park them by
				// terrain index for now.
				g.tileData = append(g.tileData, uint32(b.first+off), uint32(cnt), uint32(bucket), uint32(ti))
			}
			t.caps[bucket] += n
			t.submitted += int(n)
		}
	}
	if len(terrains) == 0 {
		return 0
	}
	// Tiles in terrain order, so each terrain's are one run.
	sorted := make([]uint32, 0, len(g.tileData))
	for ti := range terrains {
		terrains[ti].tileFirst = len(sorted) / 4
		for i := 0; i < len(g.tileData); i += 4 {
			if int(g.tileData[i+3]) == ti {
				sorted = append(sorted, g.tileData[i:i+4]...)
			}
		}
		terrains[ti].tiles = len(sorted)/4 - terrains[ti].tileFirst
	}
	// Commands: each region of the reference list as big as everything its
	// bucket submitted.
	for ti := range terrains {
		t := &terrains[ti]
		t.cmdFirst = len(g.cmdData) / 5
		for _, c := range g.cmds {
			cp := int(t.caps[c.bucket])
			g.cmdData = append(g.cmdData, uint32(c.tmpl.count), 0, uint32(c.tmpl.first), uint32(c.tmpl.base), uint32(refs))
			refs += cp
		}
	}
	submitted := 0
	for _, t := range terrains {
		submitted += t.submitted
	}
	if len(sorted) == 0 {
		// Every batch in view is the Nanite path's (nfoliage.go).
		return 0
	}

	useHiz := g.hiz.build()

	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, g.tiles)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(sorted)*4, gl.Ptr(sorted), gl.STREAM_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, g.cmdBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(g.cmdData)*4, gl.Ptr(g.cmdData), gl.STREAM_DRAW)
	if refs > g.refsCap {
		for g.refsCap < refs {
			g.refsCap *= 2
		}
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, g.refs)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, g.refsCap*8, nil, gl.STREAM_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, g.tiles)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, g.cmdBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, g.refs)

	// The cull.
	c := &g.cull
	gl.UseProgram(c.prog)
	if g.paramsAt[0] != r.frame {
		uploadFoliageParams(c.U("shapeRange"), c.U("shapeMore"), c.U("bucketInfo"), c.U("bucketImp"))
		ext := make([]float32, world.FoliageShapeCount)
		for s := range ext {
			e := world.FoliageExtent[s]
			ext[s] = max(e[0], e[1])
		}
		gl.Uniform1fv(c.U("shapeExtent"), int32(len(ext)), &ext[0])
	}
	cam := gfx.Cam.Position
	gl.Uniform3f(c.U("camPos"), cam[0], cam[1], cam[2])
	var planes [6]mgl32.Vec4
	if frustum != nil {
		planes = frustum.Planes
	}
	gl.Uniform4fv(c.U("planes"), 6, &planes[0][0])
	gl.UniformMatrix4fv(c.U("projView"), 1, false, &projView[0])
	gl.Uniform1i(c.U("useHiz"), boolToInt32(useHiz))
	gl.Uniform1i(c.U("hiz"), foliageHiZUnit)
	gl.Uniform2i(c.U("hizSize"), g.hiz.size[0], g.hiz.size[1])
	gl.Uniform1i(c.U("hizLevels"), g.hiz.levels)
	gl.Uniform1i(c.U("instances"), foliageInstanceUnit)
	gl.ActiveTexture(gl.TEXTURE0 + foliageHiZUnit)
	gl.BindTexture(gl.TEXTURE_2D, g.hiz.tex)
	gl.ActiveTexture(gl.TEXTURE0 + foliageInstanceUnit)
	gl.BindTexture(gl.TEXTURE_BUFFER, r.pool.tex)
	gl.ActiveTexture(gl.TEXTURE0)
	bc := make([]int32, 4*r.buckets)
	for _, t := range terrains {
		for b := 0; b < r.buckets; b++ {
			bc[4*b], bc[4*b+1], bc[4*b+2] = g.bucketCmd[b][0], g.bucketCmd[b][1], g.bucketCmd[b][2]
			bc[4*b+3] = t.caps[b]
		}
		gl.Uniform4iv(c.U("bucketCmd"), int32(r.buckets), &bc[0])
		gl.UniformMatrix4fv(c.U("model"), 1, false, &t.model[0])
		gl.Uniform1ui(c.U("cmdBase"), uint32(t.cmdFirst))
		for done := 0; done < t.tiles; done += 65535 {
			n := min(t.tiles-done, 65535)
			gl.Uniform1ui(c.U("tileBase"), uint32(t.tileFirst+done))
			gl.DispatchCompute(uint32(n), 1, 1)
		}
	}
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.VERTEX_ATTRIB_ARRAY_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)

	// The draws.
	gl.BindVertexArray(g.vao)
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, g.cmdBuf)
	for grp := 0; grp < gpuGroups; grp++ {
		if g.groups[grp][1] == 0 {
			continue
		}
		p := g.colorUniforms(grp, lvl, projView, frame)
		for _, t := range terrains {
			setFoliageModel(p, *t.model)
			off := (t.cmdFirst + g.groups[grp][0]) * 5 * 4
			gl.MultiDrawElementsIndirect(gl.TRIANGLES, gl.UNSIGNED_INT, gl.PtrOffset(off), int32(g.groups[grp][1]), 0)
		}
	}
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.UseProgram(gfx.Program)
	g.paramsAt[0] = r.frame
	return submitted
}

// colorUniforms readies the GPU flavour of the group's program.
func (g *foliageGPU) colorUniforms(grp int, lvl *world.Level, projView mgl32.Mat4, frame ShadowFrame) *progUniforms {
	i := [gpuGroups]int{foliageProgFull, foliageProgCheap, foliageProgImp}[grp]
	p := &g.progs[i]
	uploadBiomeLighting(p.prog, lvl, frame)
	if g.paramsAt[1+i] != foliage.frame {
		g.paramsAt[1+i] = foliage.frame
		uploadFoliageParams(p.U("shapeRange"), p.U("shapeMore"), p.U("bucketInfo"), p.U("bucketImp"))
	}
	gl.UniformMatrix4fv(p.U("projView"), 1, false, &projView[0])
	foliage.commonUniforms(p, gfx.Cam.Position)
	gl.Uniform3f(p.U("barkColor"), 0.30, 0.24, 0.18)
	gl.Uniform1i(p.U("alphaToCoverage"), boolToInt32(alphaToCoverage()))
	// In a TAA frame, its dither instead (taa.go).
	gl.Uniform1i(p.U("coverageDither"), coverageDither())
	gl.Uniform1i(p.U("instances"), foliageInstanceUnit)
	gl.ActiveTexture(gl.TEXTURE0 + foliageInstanceUnit)
	gl.BindTexture(gl.TEXTURE_BUFFER, foliage.pool.tex)
	gl.ActiveTexture(gl.TEXTURE0)
	if i == foliageProgImp {
		foliage.imp.bind(p)
	}
	return p
}
