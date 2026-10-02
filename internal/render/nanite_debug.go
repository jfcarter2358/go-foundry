package render

// nanite_debug.go — the Nanite debug views: what the GPU-driven cull drew,
// coloured by CLUSTER, by LOD LEVEL, or added up as OVERDRAW. Phase 2 of the
// Nanite plan (docs/nanite/PHASE_2.md). Asked for by name like every other
// debug view (view.go: SetView "clusters", "lod", "overdraw"; play's `view`,
// `foundry play -view`, the editor's viewport tool).
//
// Everything the cull draws — cooked meshes, compiled surfaces, chunk brushes
// — is drawn through one small program (nanite_debug_vert/frag.glsl) in place
// of its material, and everything else in the frame as usual, so the views
// show the cull's work in the frame it sits in:
//
//	clusters  each cluster its own colour (instance and cluster hashed), shaded
//	          by its facing so the shape still reads
//	lod       each cluster by the DAG level it comes from: level 0 (the full
//	          detail) white, then blue, cyan, green, yellow, orange, red and
//	          magenta for each coarser level
//	overdraw  every fragment the cull's draws rasterise, added up with no
//	          depth test: dark where a pixel is covered once, bright orange
//	          to white where many clusters pile up behind one another
//
// How the program knows which cluster a triangle is in: in a debug view the
// cluster stage gives every command its own SLOT as its base instance (the
// instance number is read back from a per-slot table instead) and the merge
// pass is skipped, so every command is exactly one cluster. The per-slot
// table — instance and cluster index — is an instanced vertex attribute at
// location 11 (free in every VAO the cull draws from), enabled only for the
// debug draw; the program reads the instance's matrix and the cluster's level
// from the cull's own buffers. Off the views, nothing here runs.

import (
	"fmt"
	"log"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// The Nanite debug views, as nanite_debug_frag.glsl numbers them.
const (
	naniteDebugOff = iota
	naniteDebugClusters
	naniteDebugLOD
	naniteDebugOverdraw
)

// NaniteDebug is the Nanite debug view the frame is in (SetView).
var NaniteDebug = naniteDebugOff

// naniteDebugNames are the views' names, by mode.
var naniteDebugNames = map[int]string{
	naniteDebugClusters: "clusters",
	naniteDebugLOD:      "lod",
	naniteDebugOverdraw: "overdraw",
}

// parseNaniteDebug is the mode named name.
func parseNaniteDebug(name string) (int, bool) {
	for m, n := range naniteDebugNames {
		if n == name {
			return m, true
		}
	}
	return naniteDebugOff, false
}

// naniteSlotAttrib is the slot table's vertex attribute location.
const naniteSlotAttrib = 11

type naniteDebugProgram struct {
	tried, ok bool
	p         progUniforms
}

var naniteDebugProg naniteDebugProgram

func (d *naniteDebugProgram) ready() bool {
	if d.tried {
		return d.ok
	}
	d.tried = true
	if err := d.build(); err != nil {
		log.Printf("nanite: debug views unavailable: %v", err)
		return false
	}
	d.ok = true
	return true
}

func (d *naniteDebugProgram) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	vs, err := gfx.CompileShader(mustReadShader(biomeShaderPath("nanite_debug_vert.glsl")), gl.VERTEX_SHADER)
	if err != nil {
		return fmt.Errorf("nanite_debug_vert.glsl: %v", err)
	}
	fs, err := gfx.CompileShader(mustReadShader(biomeShaderPath("nanite_debug_frag.glsl")), gl.FRAGMENT_SHADER)
	if err != nil {
		return fmt.Errorf("nanite_debug_frag.glsl: %v", err)
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		return err
	}
	d.p = progUniforms{prog: prog, loc: map[string]int32{}}
	return nil
}

// naniteDebugActive reports whether the frame is in a Nanite debug view the
// program for exists.
func naniteDebugActive() bool {
	return NaniteDebug != naniteDebugOff && naniteDebugProg.ready()
}

// slotTable is the batch's per-slot (instance, cluster) table, rebuilt when
// its tiles change.
func (b *cullBatch) slotTable() uint32 {
	if b.slotBuf != 0 && !b.slotsDirty {
		return b.slotBuf
	}
	b.slotsDirty = false
	table := make([]uint32, 2*max(b.slots, 1))
	for _, t := range b.tiles {
		if t.B[2] == tileRange {
			// Not a debug view's tiles: its slots as its first cluster.
			for k := uint32(0); k < rangeSlots; k++ {
				table[2*(t.A[3]+k)], table[2*(t.A[3]+k)+1] = t.A[2], t.A[0]
			}
			continue
		}
		for k := uint32(0); k < t.A[1]; k++ {
			s := t.A[3] + k
			table[2*s], table[2*s+1] = t.A[2], t.A[0]+k
		}
	}
	if b.slotBuf == 0 {
		gl.GenBuffers(1, &b.slotBuf)
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, b.slotBuf)
	gl.BufferData(gl.ARRAY_BUFFER, len(table)*4, gl.Ptr(table), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	return b.slotBuf
}

// drawDebug draws the batch's groups — this pass's commands, culled in a
// debug view (one cluster a command, its slot its base instance) — through
// the debug program. vao is each group's VAO; instanced says it carries the
// cull's instance matrices at 7-10 (the cooked-mesh pool), which the debug
// draw must not fetch past the end of.
func (b *cullBatch) drawDebug(n int, vao func(i int) uint32, instanced bool) {
	d := &naniteDebugProg
	ProfileBegin("nanite: debug view")
	defer ProfileEnd()
	projView := cameraProjView()
	gl.UseProgram(d.p.prog)
	gl.UniformMatrix4fv(d.p.U("projView"), 1, false, &projView[0])
	gl.Uniform1i(d.p.U("debugMode"), int32(NaniteDebug))
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, b.instBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, b.clusters)
	if NaniteDebug == naniteDebugOverdraw {
		gl.Disable(gl.DEPTH_TEST)
		gl.DepthMask(false)
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.ONE, gl.ONE)
	}
	table := b.slotTable()
	for i := 0; i < n; i++ {
		if !b.groupLive(i) {
			continue
		}
		gl.BindVertexArray(vao(i))
		if instanced {
			for k := uint32(0); k < 4; k++ {
				gl.DisableVertexAttribArray(gfx.AttribInstanceModel + k)
			}
		}
		gl.BindBuffer(gl.ARRAY_BUFFER, table)
		gl.EnableVertexAttribArray(naniteSlotAttrib)
		gl.VertexAttribIPointerWithOffset(naniteSlotAttrib, 2, gl.UNSIGNED_INT, 8, 0)
		gl.VertexAttribDivisor(naniteSlotAttrib, 1)
		b.drawGroup(i)
		gl.DisableVertexAttribArray(naniteSlotAttrib)
		gl.VertexAttribDivisor(naniteSlotAttrib, 0)
		if instanced {
			for k := uint32(0); k < 4; k++ {
				gl.EnableVertexAttribArray(gfx.AttribInstanceModel + k)
			}
		}
	}
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, 0)
	if NaniteDebug == naniteDebugOverdraw {
		gl.Enable(gl.DEPTH_TEST)
		gl.DepthMask(true)
		gl.Disable(gl.BLEND)
	}
	gl.UseProgram(gfx.Program)
}

// naniteDebugLevelColour is the LOD view's colour for level l, as the
// fragment stage paints it (for the tests and the legend).
func naniteDebugLevelColour(l int) mgl32.Vec3 {
	palette := []mgl32.Vec3{
		{1, 1, 1}, {0.2, 0.4, 1}, {0, 0.9, 0.9}, {0.2, 0.9, 0.2},
		{1, 0.95, 0.1}, {1, 0.55, 0}, {1, 0.1, 0.1}, {0.9, 0.1, 0.9},
	}
	return palette[min(l, len(palette)-1)]
}
