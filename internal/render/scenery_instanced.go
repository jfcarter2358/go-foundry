package render

// scenery_instanced.go — drawing placed scenery as instances.
//
// A level stamps the same few models hundreds of times — the canyon has 2,616
// placements of a few dozen rocks, trees and ruins — and drawing each one alone
// cost a uniform upload, a VAO bind, a texture bind and a draw call per
// submesh, in the colour pass and again in every shadow cascade: about 7.7 ms
// of CPU a frame for the canyon on its own.
//
// So placements are GROUPED by what they draw: the same model's same submesh,
// with the same surface (alpha, texture, material, unlit, smooth). One member's
// Geometry stands for the group — every placement of a model loads the same
// mesh, so its VAO serves them all — and is given a per-instance model-matrix
// attribute (gfx.AttachInstanceMatrices). Each pass culls the placements on
// the CPU, as before, writes the survivors' matrices into one buffer, and draws
// each group with a single glDrawArraysInstancedBaseInstance.
//
// A transparent group is NOT instanced: its placements still go through the
// frame's sorted transparent queue one by one, because blending has to be
// drawn back to front and a batch is drawn in whatever order it holds.
//
// The grouping is rebuilt whenever the scenery's signature changes — a
// placement added, removed, reloaded (its VAO changes) or re-alpha'd. Moving
// one only changes its matrix, which is cached per placement and recomputed
// when its position, rotation or scale differ from what built it.

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

type sceneryGroupKey struct {
	model       string
	sub         int
	alpha       float32
	texture     string
	material    string
	unlit       bool
	smooth      bool
	transparent bool
}

type sceneryGroup struct {
	key sceneryGroupKey
	// rep is the placement whose submesh the group draws through.
	rep, sub int
	members  []int32
	// first and count are this pass's slice of the instance buffer.
	first, count int
}

type sceneryXform struct {
	pos, rot, scale mgl32.Vec3
}

type sceneryBox struct {
	lo, hi mgl32.Vec3
	ok     bool
}

type sceneryInstancer struct {
	lvl    *world.Level
	sig    uint64
	groups []sceneryGroup
	// mats caches each placement's ModelMatrix, valid while xforms[i] matches
	// the placement.
	mats   []mgl32.Mat4
	xforms []sceneryXform
	// boxes caches each placement's world bounds for culling, on the same
	// terms; ok false is a placement with none, which is never culled.
	boxes []sceneryBox
	buf   uint32
	data  []float32
}

var scenery sceneryInstancer

// sceneryInstancing can be switched off (FOUNDRY_SCENERY_INSTANCING=0 in
// init) to A/B against the one-draw-per-placement path.
var sceneryInstancing = true

// signature hashes what the grouping depends on.
func sceneryGroupsSignature(lvl *world.Level) uint64 {
	h := fnv.New64a()
	var b [8]byte
	put := func(v uint32) {
		binary.LittleEndian.PutUint32(b[:4], v)
		h.Write(b[:4])
	}
	if len(lvl.Scenery) > 0 {
		binary.LittleEndian.PutUint64(b[:], uint64(uintptr(unsafe.Pointer(&lvl.Scenery[0]))))
		h.Write(b[:])
	}
	put(uint32(len(lvl.Scenery)))
	for i := range lvl.Scenery {
		subs := lvl.Scenery[i].Submeshes
		if lvl.Scenery[i].NaniteGPU {
			put(0xffffffff)
		}
		put(uint32(len(subs)))
		for si := range subs {
			put(subs[si].Vao)
			put(math.Float32bits(subs[si].Alpha))
		}
	}
	return h.Sum64()
}

// prepare rebuilds the groups if the scenery changed shape.
func (in *sceneryInstancer) prepare(lvl *world.Level) {
	if in.buf == 0 {
		gl.GenBuffers(1, &in.buf)
	}
	sig := sceneryGroupsSignature(lvl)
	if lvl == in.lvl && sig == in.sig {
		return
	}
	in.lvl, in.sig = lvl, sig
	in.groups = in.groups[:0]
	index := map[sceneryGroupKey]int{}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if s.NaniteGPU {
			continue // the GPU-driven cull's (nanite_scenery.go)
		}
		for si := range s.Submeshes {
			g := &s.Submeshes[si]
			if len(g.Vertices) == 0 || g.Vao == 0 {
				continue
			}
			k := sceneryGroupKey{
				model: s.Model, sub: si, alpha: g.Alpha, texture: g.Texture,
				material: g.MaterialName, unlit: g.Unlit, smooth: g.Smooth,
				transparent: g.IsTransparent(),
			}
			gi, ok := index[k]
			if !ok {
				gi = len(in.groups)
				index[k] = gi
				in.groups = append(in.groups, sceneryGroup{key: k, rep: i, sub: si})
				// Transparent groups too: their colour pass is per placement,
				// but their shadow is instanced like the rest.
				gfx.AttachInstanceMatrices(g.Vao, in.buf)
			}
			in.groups[gi].members = append(in.groups[gi].members, int32(i))
		}
	}
	in.mats = make([]mgl32.Mat4, len(lvl.Scenery))
	in.xforms = make([]sceneryXform, len(lvl.Scenery))
	in.boxes = make([]sceneryBox, len(lvl.Scenery))
	for i := range in.xforms {
		in.xforms[i].scale = mgl32.Vec3{float32(math.NaN()), 0, 0} // never matches: computed on first use
	}
}

// matrix is placement i's model matrix, from the cache when it has not moved.
func (in *sceneryInstancer) matrix(i int) mgl32.Mat4 {
	s := &in.lvl.Scenery[i]
	x := sceneryXform{s.Position, s.Rotation, s.Scale}
	if in.xforms[i] != x {
		in.xforms[i] = x
		in.mats[i] = s.ModelMatrix()
		lo, hi, ok := s.WorldAABB()
		in.boxes[i] = sceneryBox{lo, hi, ok}
	}
	return in.mats[i]
}

// visible is SceneryInFrustum from the cached bounds.
func (in *sceneryInstancer) visible(i int, cull *Frustum) bool {
	in.matrix(i)
	b := &in.boxes[i]
	return cull == nil || !b.ok || AABBInFrustum(*cull, b.lo, b.hi)
}

// fill culls every opaque group's members against cull, writes the survivors'
// matrices into the instance buffer and records each group's slice of it. It
// returns how many placements survived in at least one group.
func (in *sceneryInstancer) fill(cull *Frustum, shadows bool) int {
	in.data = in.data[:0]
	visible := 0
	n := 0
	for gi := range in.groups {
		g := &in.groups[gi]
		g.first, g.count = n, 0
		if g.key.transparent && !shadows {
			continue
		}
		for _, m := range g.members {
			s := &in.lvl.Scenery[m]
			if !in.visible(int(m), cull) {
				continue
			}
			mat := in.mats[m]
			in.data = append(in.data, mat[:]...)
			g.count++
			n++
			if g.sub == firstDrawnSub(s) {
				visible++
			}
		}
	}
	if len(in.data) > 0 {
		gl.BindBuffer(gl.ARRAY_BUFFER, in.buf)
		gl.BufferData(gl.ARRAY_BUFFER, len(in.data)*4, gl.Ptr(in.data), gl.STREAM_DRAW)
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	}
	return visible
}

// firstDrawnSub is the first submesh of s with anything in it, so a placement
// is counted once however many submeshes it has.
func firstDrawnSub(s *world.Scenery) int {
	for si := range s.Submeshes {
		if len(s.Submeshes[si].Vertices) > 0 {
			return si
		}
	}
	return -1
}

// renderSceneryInstanced is RenderScenery's instanced path: opaque groups in
// one draw each, transparent ones queued one placement at a time.
func renderSceneryInstanced(lvl *world.Level, cull *Frustum, draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) int {
	in := &scenery
	in.prepare(lvl)
	visible := in.fill(cull, false)
	for gi := range in.groups {
		g := &in.groups[gi]
		if g.key.transparent {
			for _, m := range g.members {
				s := &lvl.Scenery[m]
				if !in.visible(int(m), cull) {
					continue
				}
				draw(&s.Submeshes[g.sub], in.matrix(int(m)), false)
				if g.sub == firstDrawnSub(s) {
					visible++
				}
			}
			continue
		}
		if g.count == 0 {
			continue
		}
		lvl.Scenery[g.rep].Submeshes[g.sub].DrawInstanced(g.first, g.count)
	}
	return visible
}

// renderSceneryShadowInstanced is RenderSceneryShadow's: every group, in one
// draw each, into the bound shadow map. prog's `instanced` is set for the
// draws and cleared after.
func renderSceneryShadowInstanced(lvl *world.Level, prog uint32, skin *gfx.SkinUniforms, cull *Frustum) {
	in := &scenery
	in.prepare(lvl)
	if in.fill(cull, true) == 0 {
		return
	}
	// Unskinned, whatever the entity drawn before this left set.
	gfx.ApplySkinUniforms(skin, prog, nil)
	loc := gfx.UniformLoc(prog, "instanced\x00")
	gl.Uniform1i(loc, 1)
	for gi := range in.groups {
		g := &in.groups[gi]
		if g.count == 0 {
			continue
		}
		geo := &lvl.Scenery[g.rep].Submeshes[g.sub]
		if geo.Alpha < 0.05 {
			continue
		}
		gl.BindVertexArray(geo.Vao)
		geo.DrawTrianglesInstanced(g.first, g.count)
	}
	gl.Uniform1i(loc, 0)
}
