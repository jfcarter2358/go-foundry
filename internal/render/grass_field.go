package render

// grass_field.go — the Poly Haven grass instancer: drawing the grass fields
// a generator's grass_field stage wrote (internal/grassfield; one level
// volume per terrain tile, world/volume_grass.go; docs/regions/PRESETS_2B.md).
//
// # What is resident
//
// A field (one tile's .fgrass, ~12 bytes a clump) is read and its instances
// uploaded as one vertex buffer when the camera comes within the grass draw
// range of the tile's box (plus a margin), and released once it has been out
// of reach for a few seconds — so a world of hundreds of tiles holds only the
// few round the camera. Loads are budgeted a frame (grassLoadsPerFrame), but
// a level seen for the first time loads everything in reach at once, so a
// level opens (and a headless render shoots) with its grass. Its kit (.fgk:
// every clump's two or three LOD meshes) is one vertex and index buffer,
// shared by every field naming it. A field streams with its chunk's switch
// (VolumeGrass.Chunk): a switched-off chunk's grass is neither drawn nor cast.
//
// # Culling and thinning, per cell, on the CPU
//
// A field is cut into cells (16 m by default) and its instances stored by
// (cell, kind) in a fixed hashed order. Each frame, each cell whose box is in
// the frustum and within range draws, per kind, the prefix of its run its
// NEAREST point admits (grassfield.Falloff.DrawCount), with each LOD mesh
// whose distance window its span meets — one DrawElementsIndirect command
// each, gathered per (field, texture group, tier) and issued with one
// MultiDrawElementsIndirect. The vertex stage (grassfield_common.glsl) then
// fades each drawn instance by its own distance and rank and grows the
// survivors to keep the cover; an instance never shown is moved off screen
// before any fragment work. The same thinning rules are Go in
// grassfield/falloff.go and GLSL in grassfield_common.glsl, kept in step by
// tests.
//
// # Shading
//
// Per vertex through biome_light.glsl (the terrain foliage's reasoning: a
// blade is narrower than a shadow texel), the albedo per pixel from the
// clump's `_cov` cutout, alpha-to-coverage under MSAA or TAA's dither,
// fogged like everything else. Drawn in the opaque pass after the terrain
// foliage, in the same place on the forward and visibility-buffer frame
// paths: after the vis buffer's material pass has resolved the opaque world
// into the scene target, so grass depth-tests against it either way.
//
// # Shadows
//
// grass_field_shadow.go: the cells within gfx.GrassShadowDistance cast, with
// a kind's mid mesh, into the near cascade (or the virtual shadow map's
// pages there, as movers: the grass sways).
//
// # Ground cover
//
// A kind's flags (grassfield.Kind.Flags: oriented, still, card) are part of
// its draw group and reach the vertex stage as `kindFlags`, so litter, twigs
// and pebbles draw in the same passes as the grass (docs/terrain-paint/
// PHASE_P3.md). An ORIENTED field (version 2) carries a fourth instance
// word, the up axis, fetched as attribute 5 only while such a field is
// bound; for every other field the attribute's array is off and the stride
// is the old 12 bytes.

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"unsafe"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/grassfield"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// grassLightDebug is FOUNDRY_GRASS_LIGHT, an A/B of the colour pass's
// per-vertex lighting cost (docs/regions/PRESETS_2B.md), not a setting:
// "flat" lights nothing (the tint alone), "noshadow" skips the shadow
// lookups.
var grassLightDebug = os.Getenv("FOUNDRY_GRASS_LIGHT")

// grassLoadsPerFrame is how many fields may load in one frame once a level
// is running (decode and upload: ~1 ms each for a dense 128 m tile).
const grassLoadsPerFrame = 4

// grassEvictFrames is how long a field out of reach stays resident.
const grassEvictFrames = 240

// grassReachMargin is how far beyond the draw range a field is loaded, so it
// is resident before its first cell can draw.
const grassReachMargin = 24

// grassMeshRef is one LOD mesh's slice of its kit's buffers.
type grassMeshRef struct {
	base, first, count int32
}

// grassGroup is the kinds that draw together: one texture, one share of the
// range, one shadow flag, one count of LOD meshes (which fixes the tiers'
// distance windows).
type grassGroup struct {
	texture string
	rng     float32
	casts   bool
	lods    int
	// flags are the kinds' grassfield flags less KindCasts (casts says
	// that): the vertex stage's kindFlags.
	flags uint32
}

// grassKitGPU is one kit, uploaded.
type grassKitGPU struct {
	kit           *grassfield.Kit
	err           error
	vao, vbo, ebo uint32
	lods          [][]grassMeshRef
	group         []int
	groups        []grassGroup
	// tallest is the tallest kind's height at scale 1.
	tallest float32
}

// grassFieldGPU is one field, resident or not.
type grassFieldGPU struct {
	path  string
	field *grassfield.Field
	kit   *grassKitGPU
	err   error
	vbo   uint32
	// seen is the last frame the field was within reach.
	seen uint64
	// The chunk it streams with, resolved once per level and chunk count.
	chunkLvl *world.Level
	chunkN   int
	chunk    *world.Chunk
	// top is the cells' box top above their highest ground.
	top float32
}

// grassCmd is DrawElementsIndirectCommand.
type grassCmd struct {
	count, instances, first uint32
	base                    int32
	baseInstance            uint32
}

// grassBatch is one MultiDrawElementsIndirect: a field's commands of one
// group and tier.
type grassBatch struct {
	f           *grassFieldGPU
	group, tier int
	off, n      int
	window      grassfield.Tier
}

// GrassFieldStats is what the last colour pass drew.
type GrassFieldStats struct {
	Resident, Drawn, Cells, Commands, Instances int
	// Triangles is what the draws submitted (instances × their mesh's
	// triangles, every tier counted): the vertex stage's load, which is
	// what grass costs on the GTX 1070 (docs/regions/PRESETS_2B.md).
	Triangles int
}

type grassFieldRenderer struct {
	initialized   bool
	color, shadow progUniforms
	kits          map[string]*grassKitGPU
	fields        map[string]*grassFieldGPU
	frame         uint64
	lvl           *world.Level
	cmdBuf        uint32
	cmds          []grassCmd
	batches       []grassBatch
	// scratch per (group, tier) while one field is gathered.
	scratch [][]grassCmd
	// visible is this frame's fields in reach (colour pass), for the shadow
	// passes after it.
	visible []*grassFieldGPU
	stats   GrassFieldStats
	// loadAll lifts the per-frame load budget (tests, first frames).
	loadAll bool
	// tris counts the colour pass's submitted triangles while it gathers.
	tris int
}

var grassFields grassFieldRenderer

// grassFieldShaders are the files the pass needs.
var grassFieldShaders = []string{"grassfield_common.glsl", "grassfield_vert.glsl", "grassfield_frag.glsl",
	"grassfield_shadow_vert.glsl", "grassfield_shadow_frag.glsl", "wind_common.glsl"}

// GrassFieldInit compiles the grass instancer's programs. Called once with
// the GL context current, from InitWorldPasses. A shader directory without
// the grassfield_*.glsl stages is logged and the pass stays off.
func GrassFieldInit() {
	for _, p := range grassFieldShaders {
		if _, err := os.Stat(biomeShaderPath(p)); err != nil {
			log.Printf("grass fields: %v — Poly Haven grass will not be drawn until the shader directory has the grassfield_*.glsl stages", err)
			return
		}
	}
	wind := mustReadShader(biomeShaderPath("wind_common.glsl"))
	if grassLightDebug == "flat" {
		wind = "#define GRASS_FLAT_LIGHT 1\n" + wind
	}
	common := mustReadShader(biomeShaderPath("grassfield_common.glsl"))
	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }
	// The vertex stage lights per vertex with ONE virtual-shadow-map tap
	// (vsm.glsl's vsmShadow1): the nine-tap filter cost the grass ~8 ms of
	// its 12 on the GTX 1070, for softness a blade cannot show.
	grassFields.color = mk(mustBiomeProgram(biomeShaderPath("grassfield_vert.glsl"), biomeShaderPath("grassfield_frag.glsl"),
		"#define BIOME_LIGHT_VSM_ONE_TAP 1\n"+biomeLightSnippet(0, false, false)+"\n"+wind+"\n"+common, biomeLightSnippet(0, false, false)))
	grassFields.shadow = mk(mustBiomeProgram(biomeShaderPath("grassfield_shadow_vert.glsl"), biomeShaderPath("grassfield_shadow_frag.glsl"),
		wind+"\n"+common, ""))
	grassFields.kits = map[string]*grassKitGPU{}
	grassFields.fields = map[string]*grassFieldGPU{}
	gl.GenBuffers(1, &grassFields.cmdBuf)
	grassFields.initialized = true
}

// ReleaseGrassFields frees every resident field and kit — for a GL context
// about to go away, or a test that wants to start clean.
func ReleaseGrassFields() {
	r := &grassFields
	for _, f := range r.fields {
		f.release()
	}
	for _, k := range r.kits {
		k.release()
	}
	r.fields = map[string]*grassFieldGPU{}
	r.kits = map[string]*grassKitGPU{}
	r.visible = r.visible[:0]
	r.lvl = nil
}

// GrassFieldsLastStats is what the last colour pass drew.
func GrassFieldsLastStats() GrassFieldStats { return grassFields.stats }

// grassFalloff is the distance rules with the player's settings applied.
func grassFalloff() grassfield.Falloff {
	return grassfield.DefaultFalloff.Scaled(gfx.ClampGrassDistance(gfx.GrassDistance), gfx.ClampGrassDensity(gfx.GrassDensity))
}

func resourcePath(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(fileutil.ResourcesPath, filepath.FromSlash(rel))
}

// kitFor is the kit at path, loaded and uploaded on first use (an error is
// kept, so a missing kit is logged once).
func (r *grassFieldRenderer) kitFor(path string) *grassKitGPU {
	if k, ok := r.kits[path]; ok {
		return k
	}
	k := &grassKitGPU{}
	r.kits[path] = k
	data, err := os.ReadFile(resourcePath(path))
	if err == nil {
		k.kit, err = grassfield.DecodeKit(data)
	}
	if err != nil {
		k.err = err
		log.Printf("grass fields: kit %s: %v", path, err)
		return k
	}
	k.upload()
	return k
}

// upload puts the kit's meshes into one vertex and index buffer and works
// out its groups.
func (k *grassKitGPU) upload() {
	var verts []float32
	var idx []uint32
	k.lods = make([][]grassMeshRef, len(k.kit.Kinds))
	k.group = make([]int, len(k.kit.Kinds))
	for i := range k.kit.Kinds {
		kd := &k.kit.Kinds[i]
		k.tallest = max(k.tallest, kd.Height)
		for _, m := range kd.LODs {
			k.lods[i] = append(k.lods[i], grassMeshRef{base: int32(len(verts) / grassfield.VertexFloats),
				first: int32(len(idx)), count: int32(len(m.Indices))})
			verts = append(verts, m.Vertices...)
			idx = append(idx, m.Indices...)
		}
		g := grassGroup{texture: kd.Texture, rng: kd.Range, casts: kd.Casts(), lods: len(kd.LODs),
			flags: kd.Flags &^ grassfield.KindCasts}
		k.group[i] = -1
		for gi, o := range k.groups {
			if o == g {
				k.group[i] = gi
			}
		}
		if k.group[i] < 0 {
			k.group[i] = len(k.groups)
			k.groups = append(k.groups, g)
		}
		if err := gfx.EnsureTextureLoaded(kd.Texture); err != nil {
			log.Printf("grass fields: kind %s: %v", kd.Name, err)
		}
	}
	gl.GenVertexArrays(1, &k.vao)
	gl.GenBuffers(1, &k.vbo)
	gl.GenBuffers(1, &k.ebo)
	gl.BindVertexArray(k.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, k.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, k.ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(idx)*4, gl.Ptr(idx), gl.STATIC_DRAW)
	stride := int32(grassfield.VertexFloats * 4)
	gl.BindVertexBuffer(0, k.vbo, 0, stride)
	for _, a := range []struct {
		loc  uint32
		size int32
		off  uint32
	}{{0, 3, 0}, {1, 3, 12}, {2, 2, 24}, {3, 4, 32}} {
		gl.EnableVertexAttribArray(a.loc)
		gl.VertexAttribFormat(a.loc, a.size, gl.FLOAT, false, a.off)
		gl.VertexAttribBinding(a.loc, 0)
	}
	gl.EnableVertexAttribArray(4)
	gl.VertexAttribIFormat(4, 3, gl.UNSIGNED_INT, 0)
	gl.VertexAttribBinding(4, 1)
	// The up axis of an oriented field (enabled per field, drawBatches).
	gl.VertexAttribIFormat(5, 1, gl.UNSIGNED_INT, 12)
	gl.VertexAttribBinding(5, 1)
	gl.DisableVertexAttribArray(5)
	gl.VertexAttribI4ui(5, 0, 0, 0, 0)
	gl.VertexBindingDivisor(1, 1)
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
}

func (k *grassKitGPU) release() {
	if k.vao != 0 {
		gl.DeleteVertexArrays(1, &k.vao)
		gl.DeleteBuffers(1, &k.vbo)
		gl.DeleteBuffers(1, &k.ebo)
		k.vao, k.vbo, k.ebo = 0, 0, 0
	}
}

// load reads and uploads the field.
func (f *grassFieldGPU) load(r *grassFieldRenderer) {
	data, err := os.ReadFile(resourcePath(f.path))
	if err == nil {
		f.field, err = grassfield.DecodeField(data)
	}
	if err == nil {
		f.kit = r.kitFor(f.field.Kit)
		if f.kit.err != nil {
			err = fmt.Errorf("its kit: %w", f.kit.err)
		} else if f.field.Kinds != len(f.kit.kit.Kinds) {
			err = fmt.Errorf("laid out for %d kinds, its kit has %d", f.field.Kinds, len(f.kit.kit.Kinds))
		}
	}
	if err != nil {
		f.err = err
		f.field = nil
		log.Printf("grass fields: %s: %v", f.path, err)
		return
	}
	f.top = f.kit.tallest*f.field.ScaleMax*1.5 + 1
	gl.GenBuffers(1, &f.vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, f.vbo)
	if n := len(f.field.Data); n > 0 {
		gl.BufferData(gl.ARRAY_BUFFER, n*4, gl.Ptr(f.field.Data), gl.STATIC_DRAW)
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
}

func (f *grassFieldGPU) release() {
	if f.vbo != 0 {
		gl.DeleteBuffers(1, &f.vbo)
		f.vbo = 0
	}
	f.field, f.kit = nil, nil
}

// inWorld reports whether the field's chunk (if any) is switched on.
func (f *grassFieldGPU) inWorld(lvl *world.Level, v *world.Volume) bool {
	if f.chunkLvl != lvl || f.chunkN != len(lvl.Chunks) {
		f.chunkLvl, f.chunkN = lvl, len(lvl.Chunks)
		f.chunk = v.GrassChunk(lvl)
	}
	return f.chunk == nil || f.chunk.IsEnabled()
}

// grassBoxDistance is the distance from p to the box lo..hi (0 inside), and to
// its farthest corner.
func grassBoxDistance(p, lo, hi mgl32.Vec3) (near, far float32) {
	var dn, df float32
	for i := 0; i < 3; i++ {
		d := max(lo[i]-p[i], 0, p[i]-hi[i])
		dn += d * d
		e := max(p[i]-lo[i], hi[i]-p[i])
		df += e * e
	}
	return float32(math.Sqrt(float64(dn))), float32(math.Sqrt(float64(df)))
}

// sync finds the level's fields in reach, loads what they need within the
// frame's budget and evicts what has been out of reach a while. Called once
// a frame by the colour pass; fills r.visible.
func (r *grassFieldRenderer) sync(lvl *world.Level, cam mgl32.Vec3, fo grassfield.Falloff) {
	r.frame++
	r.visible = r.visible[:0]
	loads := grassLoadsPerFrame
	if r.lvl != lvl || r.loadAll {
		loads = math.MaxInt32
		r.lvl = lvl
	}
	reach := fo.Range + grassReachMargin
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if !v.IsGrass() {
			continue
		}
		lo, hi, ok := v.WorldAABB(lvl)
		if !ok {
			continue
		}
		if d, _ := grassBoxDistance(cam, lo, hi); d > reach {
			continue
		}
		f := r.fields[v.Grass.File]
		if f == nil {
			f = &grassFieldGPU{path: v.Grass.File}
			r.fields[v.Grass.File] = f
		}
		if !f.inWorld(lvl, v) {
			continue
		}
		f.seen = r.frame
		if f.field == nil && f.err == nil {
			if loads <= 0 {
				continue
			}
			loads--
			f.load(r)
		}
		if f.field != nil {
			r.visible = append(r.visible, f)
		}
	}
	for path, f := range r.fields {
		if r.frame-f.seen > grassEvictFrames {
			f.release()
			delete(r.fields, path)
		}
	}
	r.stats.Resident = 0
	for _, f := range r.fields {
		if f.field != nil {
			r.stats.Resident++
		}
	}
}

// gather builds f's draws into r.cmds/r.batches: every cell in cull (nil:
// any) within maxDist of cam, each of its kinds' runs' admitted prefix, with
// each tier the cell's span meets. shadow keeps only casting groups and
// draws one tier (the coarsest mesh). It returns the instances
// submitted and cells drawn.
func (r *grassFieldRenderer) gather(f *grassFieldGPU, cam mgl32.Vec3, cull *Frustum, fo grassfield.Falloff, maxDist float32, shadow bool) (inst, cells int) {
	fd, k := f.field, f.kit
	ng := len(k.groups)
	nt := grassfield.MaxLODs
	if cap(r.scratch) < ng*nt {
		r.scratch = make([][]grassCmd, ng*nt)
	}
	r.scratch = r.scratch[:ng*nt]
	for i := range r.scratch {
		r.scratch[i] = r.scratch[i][:0]
	}
	var tiersOf [grassfield.MaxLODs + 1][]grassfield.Tier
	for l := 1; l <= grassfield.MaxLODs; l++ {
		tiersOf[l] = fo.Tiers(l)
	}
	for c := 0; c < fd.CellsX*fd.CellsZ; c++ {
		box := fd.CellBox(c)
		cy := fd.CellY[c]
		lo := mgl32.Vec3{box[0] - 1, cy[0] - 0.5, box[1] - 1}
		hi := mgl32.Vec3{box[2] + 1, cy[1] + f.top, box[3] + 1}
		dmin, dmax := grassBoxDistance(cam, lo, hi)
		if dmin > maxDist {
			continue
		}
		if cull != nil && !AABBInFrustum(*cull, lo, hi) {
			continue
		}
		drew := false
		for kd := 0; kd < fd.Kinds; kd++ {
			run := fd.Run(c, kd)
			if run.Count == 0 {
				continue
			}
			g := k.group[kd]
			grp := &k.groups[g]
			if shadow && !grp.casts {
				continue
			}
			n := fo.DrawCount(int(run.Count), dmin, grp.rng)
			if n <= 0 {
				continue
			}
			lods := k.lods[kd]
			if shadow {
				m := lods[len(lods)-1]
				r.scratch[g*nt] = append(r.scratch[g*nt], grassCmd{count: uint32(m.count), instances: uint32(n),
					first: uint32(m.first), base: m.base, baseInstance: run.Offset})
				inst += n
				drew = true
				continue
			}
			for t, tr := range tiersOf[len(lods)] {
				if !tr.Meets(dmin, dmax) {
					continue
				}
				m := lods[t]
				r.scratch[g*nt+t] = append(r.scratch[g*nt+t], grassCmd{count: uint32(m.count), instances: uint32(n),
					first: uint32(m.first), base: m.base, baseInstance: run.Offset})
				inst += n
				r.tris += n * int(m.count) / 3
				drew = true
			}
		}
		if drew {
			cells++
		}
	}
	for g := 0; g < ng; g++ {
		for t := 0; t < nt; t++ {
			s := r.scratch[g*nt+t]
			if len(s) == 0 {
				continue
			}
			b := grassBatch{f: f, group: g, tier: t, off: len(r.cmds), n: len(s)}
			if !shadow {
				b.window = fo.Tiers(k.groups[g].lods)[t]
			}
			r.cmds = append(r.cmds, s...)
			r.batches = append(r.batches, b)
		}
	}
	return inst, cells
}

// upload sends this pass's commands to the indirect buffer.
func (r *grassFieldRenderer) upload() {
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, r.cmdBuf)
	size := len(r.cmds) * int(unsafe.Sizeof(grassCmd{}))
	gl.BufferData(gl.DRAW_INDIRECT_BUFFER, size, unsafe.Pointer(&r.cmds[0]), gl.STREAM_DRAW)
}

// drawBatches issues the gathered batches with program p bound.
func (r *grassFieldRenderer) drawBatches(p *progUniforms) {
	var cur *grassFieldGPU
	var curKit *grassKitGPU
	stride := int(unsafe.Sizeof(grassCmd{}))
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Uniform1i(p.U("grassTex"), 0)
	for _, b := range r.batches {
		if b.f != cur {
			cur = b.f
			if cur.kit != curKit {
				curKit = cur.kit
				gl.BindVertexArray(curKit.vao)
			}
			fd := cur.field
			gl.BindVertexBuffer(1, cur.vbo, 0, int32(fd.Words()*4))
			if fd.Oriented {
				gl.EnableVertexAttribArray(5)
			} else {
				gl.DisableVertexAttribArray(5)
			}
			gl.Uniform3f(p.U("fieldOrigin"), fd.Origin[0], fd.Origin[1], fd.Origin[2])
			gl.Uniform3f(p.U("fieldSize"), fd.Size[0], fd.Size[1], fd.Size[2])
			gl.Uniform1f(p.U("fieldScale"), fd.ScaleMax)
		}
		grp := &curKit.groups[b.group]
		gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[grp.texture])
		gl.Uniform1f(p.U("kindRange"), grp.rng)
		gl.Uniform1i(p.U("kindFlags"), int32(grp.flags))
		gl.Uniform2f(p.U("tierWindow"), b.window.From, b.window.To)
		gl.MultiDrawElementsIndirect(gl.TRIANGLES, gl.UNSIGNED_INT, gl.PtrOffset(b.off*stride), int32(b.n), 0)
	}
}

// commonGrassUniforms are what both programs read every frame.
func commonGrassUniforms(p *progUniforms, cam mgl32.Vec3, fo grassfield.Falloff) {
	gl.Uniform3f(p.U("camPos"), cam[0], cam[1], cam[2])
	gl.Uniform4f(p.U("grassRangeA"), fo.Range, fo.Start, fo.Thin, fo.Comp)
	gl.Uniform4f(p.U("grassRangeB"), fo.CompUp, fo.Density, 0, 0)
}

// RenderGrassFields draws the level's grass fields in the opaque pass, after
// the terrain foliage. Returns the instances submitted.
func RenderGrassFields(lvl *world.Level, projView mgl32.Mat4, frustum *Frustum, frame ShadowFrame) int {
	r := &grassFields
	if !r.initialized || lvl == nil {
		return 0
	}
	r.stats = GrassFieldStats{Resident: r.stats.Resident}
	if !anyGrassVolume(lvl) {
		if len(r.fields) > 0 {
			ReleaseGrassFields()
		}
		return 0
	}
	cam := gfx.Cam.Position
	fo := grassFalloff()
	r.sync(lvl, cam, fo)
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	r.tris = 0
	for _, f := range r.visible {
		inst, cells := r.gather(f, cam, frustum, fo, fo.Range, false)
		r.stats.Instances += inst
		r.stats.Cells += cells
		if cells > 0 {
			r.stats.Drawn++
		}
	}
	r.stats.Commands = len(r.cmds)
	r.stats.Triangles = r.tris
	if len(r.cmds) == 0 {
		return 0
	}
	p := &r.color
	uploadBiomeLighting(p.prog, lvl, frame)
	if grassLightDebug == "noshadow" {
		gl.Uniform1i(p.U("receiveShadows"), 0)
	}
	gl.UniformMatrix4fv(p.U("projView"), 1, false, &projView[0])
	commonGrassUniforms(p, cam, fo)
	gl.Uniform1i(p.U("alphaToCoverage"), boolToInt32(alphaToCoverage()))
	gl.Uniform1i(p.U("coverageDither"), coverageDither())
	r.upload()
	r.drawBatches(p)
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
	gl.BindVertexArray(0)
	gl.UseProgram(gfx.Program)
	return r.stats.Instances
}

// anyGrassVolume reports whether the level has a grass field at all: the
// whole pass is skipped (and nothing resident kept) for every level without.
func anyGrassVolume(lvl *world.Level) bool {
	for i := range lvl.Volumes {
		if lvl.Volumes[i].IsGrass() {
			return true
		}
	}
	return false
}
