package render

// foliage.go — drawing what grows on painted terrain (world/foliage.go).
//
// # Instances, built once
//
// Placement is decided on the CPU, per terrain section or cluster of them
// (foliage_units.go), when it is first needed and again only when something it
// depends on changes (TerrainSection.FoliageGen) or the camera comes nearer
// than it was built for — never per frame. Instances go into one shared pool,
// 64 bytes each, grouped by mesh and sorted within each group by a random
// rank. Building is budgeted: nearest first, a few milliseconds a frame, so
// painting a hillside or streaming a forest in never stalls a frame; a
// terrain seen for the first time is built whole, so a level opens (and a
// headless render shoots) with its foliage in place.
//
// # Drawing less of it, further away — without the ground going bald
//
// Every shape has a draw range and a distance its thinning starts (both
// scaled by the player's gfx.FoliageDistance). Past that start the fraction
// of a shape still drawn falls as (start/d)^thin; its instances are in
// random-rank order, so drawing only the first n is an even thinning, and the
// vertex stage dissolves each instance whose rank its own distance no longer
// admits over a narrow band rather than popping it. The survivors GROW to
// cover for the ones thinned out around them (foliageComp): with thin = 2 the
// fraction drawn falls as 1/d², each survivor grows as d, and the ground
// covered stays constant — the same number of plants per pixel of screen at
// any distance, which is what lets a meadow reach a hundred metres for about
// what forty used to cost. Trees do not thin (thin = 0): every one stands
// until the end of its range.
//
// # Tiers
//
// A shape draws in up to three tiers: its near mesh, its far mesh (a fraction
// of the triangles) and a far tier — clump cards for ground cover (a few
// crossed cards showing a whole tuft, foliage_mesh.go) or, for trees, shrubs
// and rocks, an octahedral impostor: one quad showing the plant rendered from
// the nearest of 64 directions (foliage_impostor.go). Near to far mesh is
// chosen per unit by its nearest point — measured: choosing per instance cost
// more vertices than it saved. Into the far tier each instance hands over on
// its own, dissolving into the next tier through a complementary dither, so
// the switch is a soft band rather than a line across the field.
//
// Past grass range the ground itself carries the meadow (terrain_frag.glsl's
// cover tint), so the landscape does not go bald where even the cards stop.
//
// # The GPU cull
//
// Where the driver has compute shaders and indirect draws (foliage_gpu.go),
// the choosing is done on the GPU instead: every candidate instance is tested
// against the frustum, the depth of what is already drawn (a Hi-Z pyramid)
// and its own distance, and the survivors are compacted into per-mesh,
// per-tier lists drawn with a handful of indirect draws. An instance that is
// culled then costs nothing in the vertex stage, which is what lets every
// instance pick its own tier. This file's CPU path is the fallback, and the
// shadow pass always.
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
// antialias through alpha-to-coverage, on in the opaque pass. Impostors are
// lit per pixel from the normals baked into them.
//
// # Shadows
//
// Trees, shrubs, rocks, ferns and deadwood cast into the directional
// cascades (not point lights), within foliageShadowRange of the camera, when
// gfx.FoliageShadows is on, with their far mesh whatever tier they show.
// Grass does not: a meadow's self-shadowing is what the cover tint and the
// blades' own base darkening already paint.

import (
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// foliageInstanceFloats is one instance's size in the instance buffer.
const foliageInstanceFloats = 16

// foliageShadowRange is how far from the camera foliage casts, in units.
const foliageShadowRange = 110

// foliageBuildBudget is how long one frame may spend placing foliage.
const foliageBuildBudget = 3 * time.Millisecond

// foliageShapeInfo is how the renderer treats one shape. Distances are in
// units, before FoliageDistance.
type foliageShapeInfo struct {
	// rng is its draw range.
	rng float32
	// start is where its thinning begins, and thin how steeply it thins
	// (foliage_params.glsl); thin 0 keeps every instance to the end.
	start, thin float32
	// comp is how much of the thinned coverage the survivors make up by
	// growing (0..1), and compUp the power of that growth that goes into
	// height — a distant tuft grows wide faster than it grows tall.
	comp, compUp float32
	// lod is the distance past which its far mesh is drawn, and far the one
	// past which its far tier is (0: none). cpuLod is lod for the CPU path,
	// which picks the mesh per unit by its nearest point, so a unit whose
	// near edge is inside it draws its whole batch with the near mesh:
	// shorter, for the same look up close at a fraction of the vertices.
	lod, cpuLod, far float32
	// impostor: its far tier is an impostor, not clump cards.
	impostor bool
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
	world.FoliageGrass:       {rng: 130, start: 16, thin: 2, comp: 1, compUp: 0.35, lod: 22, cpuLod: 9, far: 38, shrink: true, cheap: true},
	world.FoliageTallGrass:   {rng: 140, start: 20, thin: 1.8, comp: 0.55, compUp: 0.3, lod: 28, cpuLod: 14, far: 44, shrink: true, cheap: true},
	world.FoliageFlower:      {rng: 130, start: 16, thin: 2, comp: 1, compUp: 0.35, lod: 28, cpuLod: 12, far: 40, shrink: true, cheap: true},
	world.FoliageFern:        {rng: 160, start: 22, thin: 1.6, comp: 0.9, compUp: 0.5, lod: 30, cpuLod: 18, far: 42, casts: true},
	world.FoliageShrub:       {rng: 420, start: 60, thin: 1, comp: 0.6, compUp: 0.6, lod: 40, cpuLod: 30, far: 70, impostor: true, casts: true},
	world.FoliageBroadleaf:   {rng: 1500, lod: 70, cpuLod: 55, far: 115, impostor: true, casts: true},
	world.FoliageConifer:     {rng: 1500, lod: 70, cpuLod: 55, far: 115, impostor: true, casts: true},
	world.FoliageRock:        {rng: 520, start: 80, thin: 1, comp: 0.5, compUp: 0.8, lod: 40, cpuLod: 30, far: 80, impostor: true, casts: true},
	world.FoliagePebbles:     {rng: 70, start: 14, thin: 2, comp: 0.8, compUp: 0.6, lod: 20, cpuLod: 12, cheap: true},
	world.FoliageMoss:        {rng: 90, start: 18, thin: 2, comp: 0.8, compUp: 0.5, lod: 22, cpuLod: 12, cheap: true},
	world.FoliageVine:        {rng: 170, start: 40, thin: 1, comp: 0.5, compUp: 0.7, lod: 32, cpuLod: 22},
	world.FoliageReed:        {rng: 160, start: 24, thin: 1.8, comp: 1, compUp: 0.4, lod: 30, cpuLod: 22, far: 40, cheap: true},
	world.FoliageMushroom:    {rng: 45, start: 12, thin: 2, comp: 0.5, compUp: 0.5, lod: 18, cpuLod: 10, cheap: true},
	world.FoliageLitter:      {rng: 65, start: 14, thin: 2, comp: 1, compUp: 0.3, lod: 18, cpuLod: 10, shrink: true, cheap: true},
	world.FoliageDeadwood:    {rng: 260, start: 50, thin: 1, comp: 0.4, compUp: 0.7, lod: 36, cpuLod: 28, far: 65, impostor: true, casts: true},
	world.FoliagePalm:        {rng: 1500, lod: 70, cpuLod: 55, far: 115, impostor: true, casts: true},
	world.FoliageCactus:      {rng: 900, lod: 50, cpuLod: 40, far: 90, impostor: true, casts: true},
	world.FoliageAcacia:      {rng: 1500, lod: 75, cpuLod: 60, far: 120, impostor: true, casts: true},
	world.FoliageJunglePlant: {rng: 260, start: 40, thin: 1, comp: 0.5, compUp: 0.6, lod: 32, cpuLod: 24, far: 55, impostor: true, casts: true},
	world.FoliageDeadBush:    {rng: 360, start: 55, thin: 1, comp: 0.5, compUp: 0.6, lod: 36, cpuLod: 28, far: 60, impostor: true, casts: true},
	world.FoliageIce:         {rng: 800, lod: 50, cpuLod: 40, far: 90, impostor: true, casts: true},
}

// foliageShapeParams is a shape's info with FoliageDistance applied: what
// foliage_params.glsl is uploaded.
type foliageShapeParams struct {
	rng, start, thin, comp, compUp float32
	shrink                         float32
	lod, cpuLod, far               float32
}

// foliageMaxRange is the longest any shape is drawn, before
// FoliageDistance: units further than this are neither built nor drawn.
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

// Programs, by what they draw.
const (
	foliageProgFull   = iota // the full lighting
	foliageProgCheap         // ground cover's one-tap lighting
	foliageProgImp           // impostors
	foliageProgShadow        // cascade depth
	foliagePrograms
)

type foliageRenderer struct {
	initialized bool
	progs       [foliagePrograms]progUniforms
	// paramsAt is the frame each program last had the parameter table.
	paramsAt  [foliagePrograms]uint64
	vao, vbo  uint32
	ebo       uint32
	atlas     uint32
	templates [world.FoliageShapeCount][][foliageLODs]foliageTemplate
	// quad is the impostors' one card.
	quad foliageTemplate
	// bucketBase is each shape's first bucket; a bucket is one variant.
	bucketBase [world.FoliageShapeCount]int
	buckets    int
	imp        foliageImpostors
	params     [world.FoliageShapeCount]foliageShapeParams
	pool       foliagePool
	cache      map[*world.Terrain]*foliageTerrainGPU
	frame      uint64
	lastCam    mgl32.Vec3
	lastCamSet bool
	draws      []foliageDraw
	jobs       []foliageJob
	items      [foliagePrograms][]foliageItem
	scratch    []float32
	gpu        foliageGPU
}

var foliage foliageRenderer

func (r *foliageRenderer) bucket(s world.FoliageShape, variant int) int {
	return r.bucketBase[s] + variant
}

// foliageSnippet is the parameter table and the placement, for a vertex
// stage; gpu reads instances the way the GPU cull hands them over.
func foliageSnippet(gpu bool) string {
	src := fmt.Sprintf("#define FOLIAGE_SHAPES %d\n#define FOLIAGE_BUCKETS %d\n", world.FoliageShapeCount, foliage.buckets)
	if gpu {
		src += "#define FOLIAGE_GPU 1\n"
	}
	return src + mustReadShader(biomeShaderPath("foliage_params.glsl")) + "\n" +
		mustReadShader(biomeShaderPath("foliage_common.glsl"))
}

// foliagePrograms builds the four programs, the GPU cull's flavour when gpu.
func buildFoliagePrograms(gpu bool) [foliagePrograms]progUniforms {
	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }
	common := foliageSnippet(gpu)
	var ps [foliagePrograms]progUniforms
	// One lighting evaluation per vertex, a 3x3-averaged cascade tap — the
	// scatter foliage's own choice, for its reason (see GrassInit).
	ps[foliageProgFull] = mk(mustBiomeProgram(biomeShaderPath("foliage_vert.glsl"), biomeShaderPath("foliage_frag.glsl"),
		biomeLightSnippet(1, false, false)+"\n"+common, biomeLightSnippet(0, false, false)))
	// Ground cover's: one tap (foliageShapeInfo.cheap).
	ps[foliageProgCheap] = mk(mustBiomeProgram(biomeShaderPath("foliage_vert.glsl"), biomeShaderPath("foliage_frag.glsl"),
		biomeLightSnippet(0, false, false)+"\n"+common, biomeLightSnippet(0, false, false)))
	// Impostors light per pixel, from the normals baked into them.
	ps[foliageProgImp] = mk(mustBiomeProgram(biomeShaderPath("foliage_imp_vert.glsl"), biomeShaderPath("foliage_imp_frag.glsl"),
		biomeLightSnippet(0, false, false)+"\n"+common, biomeLightSnippet(0, false, false)))
	ps[foliageProgShadow] = mk(mustBiomeProgram(biomeShaderPath("foliage_shadow_vert.glsl"), biomeShaderPath("foliage_shadow_frag.glsl"),
		common, ""))
	return ps
}

// FoliageInit compiles the foliage programs, builds every template mesh,
// draws the atlas and bakes the impostors. Called once with the GL context
// current, from InitWorldPasses. Like TerrainInit, a shader directory without
// the foliage stages is logged and the pass stays off.
func FoliageInit() {
	paths := []string{"foliage_params.glsl", "foliage_common.glsl", "foliage_vert.glsl", "foliage_frag.glsl",
		"foliage_shadow_vert.glsl", "foliage_shadow_frag.glsl", "foliage_imp_vert.glsl", "foliage_imp_frag.glsl",
		"foliage_imp_bake_vert.glsl", "foliage_imp_bake_frag.glsl"}
	for _, p := range paths {
		if _, err := os.Stat(biomeShaderPath(p)); err != nil {
			log.Printf("foliage: %v — terrain foliage will not be drawn until the shader directory has the foliage_*.glsl stages", err)
			return
		}
	}
	foliage.buckets = 0
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		foliage.bucketBase[s] = foliage.buckets
		foliage.buckets += world.FoliageVariants[s]
	}
	foliage.progs = buildFoliagePrograms(false)
	foliage.paramsAt = [foliagePrograms]uint64{}

	// Every template, into one indexed buffer: a blade's segments and a
	// card's corners share their vertices, so each is shaded once rather
	// than once per triangle touching it — the vertex stage, not the pixels,
	// is where foliage spends its time.
	var verts []float32
	var idx []uint32
	add := func(tris []float32) foliageTemplate {
		uv, ui := indexFoliageMesh(tris)
		t := foliageTemplate{base: int32(len(verts) / foliageVertexFloats), first: int32(len(idx)), count: int32(len(ui))}
		verts = append(verts, uv...)
		idx = append(idx, ui...)
		return t
	}
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		foliage.templates[s] = make([][foliageLODs]foliageTemplate, world.FoliageVariants[s])
		for v := 0; v < world.FoliageVariants[s]; v++ {
			for lod := 0; lod < foliageLODs; lod++ {
				foliage.templates[s][v][lod] = add(buildFoliageTemplate(s, v, lod))
			}
		}
	}
	foliage.quad = add(impostorQuad())
	gl.GenVertexArrays(1, &foliage.vao)
	gl.GenBuffers(1, &foliage.vbo)
	gl.GenBuffers(1, &foliage.ebo)
	gl.BindVertexArray(foliage.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, foliage.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, foliage.ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(idx)*4, gl.Ptr(idx), gl.STATIC_DRAW)
	bindTemplateAttributes()
	for i := uint32(4); i < 8; i++ {
		gl.EnableVertexAttribArray(i)
		gl.VertexAttribDivisor(i, 1)
	}
	gl.BindVertexArray(0)

	foliage.atlas = uploadFoliageAtlas(buildFoliageAtlas())
	foliage.imp = bakeFoliageImpostors()
	foliage.cache = map[*world.Terrain]*foliageTerrainGPU{}
	foliage.pool.init(true)
	foliage.initialized = true
	foliage.gpu.init()
}

// bindTemplateAttributes points attributes 0-3 at the template buffer, which
// must be bound to ARRAY_BUFFER.
func bindTemplateAttributes() {
	stride := int32(foliageVertexFloats * 4)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, stride, 6*4)
	gl.EnableVertexAttribArray(3)
	gl.VertexAttribPointerWithOffset(3, 3, gl.FLOAT, false, stride, 8*4)
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

// ReleaseFoliage frees every unit's instances — for a GL context about to go
// away, or a test that wants to start clean.
func ReleaseFoliage() {
	for t, g := range foliage.cache {
		g.release()
		delete(foliage.cache, t)
	}
	foliage.draws = foliage.draws[:0]
}

// updateParams applies FoliageDistance to the shape table.
func (r *foliageRenderer) updateParams() {
	sc := FoliageDistanceScale()
	for s, in := range foliageShapes {
		p := foliageShapeParams{rng: in.rng * sc, start: in.start * sc, thin: in.thin, comp: in.comp, compUp: in.compUp,
			lod: in.lod * sc, cpuLod: in.cpuLod * sc, far: in.far * sc}
		if in.shrink {
			p.shrink = 1
		}
		r.params[s] = p
	}
}

// uploadParams gives program i the parameter table, once a frame.
func (r *foliageRenderer) uploadParams(i int) {
	if r.paramsAt[i] == r.frame && r.frame != 0 {
		return
	}
	r.paramsAt[i] = r.frame
	p := &r.progs[i]
	uploadFoliageParams(p.U("shapeRange"), p.U("shapeMore"), p.U("bucketInfo"), p.U("bucketImp"))
}

// uploadFoliageParams sends the table to the bound program's four arrays.
func uploadFoliageParams(rangeLoc, moreLoc, infoLoc, impLoc int32) {
	n := int(world.FoliageShapeCount)
	rng := make([]float32, 0, 4*n)
	more := make([]float32, 0, 4*n)
	for _, p := range foliage.params {
		rng = append(rng, p.rng, p.start, p.thin, p.comp)
		more = append(more, p.compUp, p.shrink, p.lod, p.far)
	}
	gl.Uniform4fv(rangeLoc, int32(n), &rng[0])
	gl.Uniform4fv(moreLoc, int32(n), &more[0])
	info := make([]int32, 0, 2*foliage.buckets)
	imp := make([]float32, 0, 4*foliage.buckets)
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		for v := 0; v < world.FoliageVariants[s]; v++ {
			b := foliage.bucket(s, v)
			info = append(info, int32(s), foliage.imp.layer[b])
			c := foliage.imp.centre[b]
			imp = append(imp, c[0], c[1], c[2], c[3])
		}
	}
	gl.Uniform2iv(infoLoc, int32(foliage.buckets), &info[0])
	gl.Uniform4fv(impLoc, int32(foliage.buckets), &imp[0])
}

// bindInstances points the instance attributes at instance first of the
// pool.
func bindInstances(first int32) {
	gl.BindBuffer(gl.ARRAY_BUFFER, foliage.pool.vbo)
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

// colorUniforms readies colour program i for this frame's draws.
func (r *foliageRenderer) colorUniforms(i int, lvl *world.Level, projView mgl32.Mat4, frame ShadowFrame) *progUniforms {
	p := &r.progs[i]
	uploadBiomeLighting(p.prog, lvl, frame)
	r.uploadParams(i)
	gl.UniformMatrix4fv(p.U("projView"), 1, false, &projView[0])
	r.commonUniforms(p, gfx.Cam.Position)
	gl.Uniform3f(p.U("barkColor"), 0.30, 0.24, 0.18)
	gl.Uniform1i(p.U("alphaToCoverage"), boolToInt32(alphaToCoverage()))
	if i == foliageProgImp {
		r.imp.bind(p)
	}
	return p
}

// foliageItem is one CPU-path draw: a batch of a unit, in one tier.
type foliageItem struct {
	dr    *foliageDraw
	b     *foliageBatchGPU
	tier  int
	win   [2]float32
	count int32
}

// foliageItems sorts this frame's visible batches into draws by program.
func (r *foliageRenderer) foliageItems(frustum *Frustum) {
	for i := range r.items {
		r.items[i] = r.items[i][:0]
	}
	for di := range r.draws {
		dr := &r.draws[di]
		if frustum != nil && !AABBInFrustum(*frustum, dr.wlo, dr.whi) {
			continue
		}
		for bi := range dr.u.batches {
			b := &dr.u.batches[bi]
			info := foliageShapes[b.shape]
			p := r.params[b.shape]
			if dr.d > p.rng {
				continue
			}
			// The prefix every instance the unit's nearest point could
			// still admit.
			n := int32(math.Ceil(float64(float32(b.count) * foliageKeepRank(p, dr.d))))
			if n <= 0 {
				continue
			}
			n = min(n, b.count)
			prog := foliageProgFull
			if info.cheap {
				prog = foliageProgCheap
			}
			if p.far == 0 || dr.d < p.far {
				tier := 0
				if dr.d > p.cpuLod {
					tier = 1
				}
				r.items[prog] = append(r.items[prog], foliageItem{dr: dr, b: b, tier: tier, win: [2]float32{0, p.far}, count: n})
			}
			if p.far > 0 && dr.dmax > p.far-0.12*p.far {
				fp := prog
				if info.impostor {
					fp = foliageProgImp
				}
				r.items[fp] = append(r.items[fp], foliageItem{dr: dr, b: b, tier: 2, win: [2]float32{p.far, 0}, count: n})
			}
		}
	}
}

// RenderFoliage draws every terrain's foliage in the opaque pass, after the
// ground. Returns how many instances it submitted.
func RenderFoliage(lvl *world.Level, projView mgl32.Mat4, frustum *Frustum, frame ShadowFrame) int {
	if !foliage.initialized || lvl == nil || len(foliage.draws) == 0 {
		return 0
	}
	if foliage.gpu.active() {
		return foliage.gpu.render(lvl, projView, frustum, frame)
	}
	r := &foliage
	r.foliageItems(frustum)
	drawn := 0
	for i := foliageProgFull; i <= foliageProgImp; i++ {
		items := r.items[i]
		if len(items) == 0 {
			continue
		}
		p := r.colorUniforms(i, lvl, projView, frame)
		gl.BindVertexArray(r.vao)
		var current *mgl32.Mat4
		for _, it := range items {
			if it.dr.model != current {
				current = it.dr.model
				setFoliageModel(p, *current)
			}
			gl.Uniform1i(p.U("drawBucket"), int32(r.bucket(it.b.shape, it.b.variant)))
			gl.Uniform2f(p.U("drawWindow"), it.win[0], it.win[1])
			bindInstances(it.b.first)
			t := r.quad
			if i != foliageProgImp {
				t = r.templates[it.b.shape][it.b.variant][it.tier]
			}
			drawTemplate(t, it.count)
			drawn += int(it.count)
		}
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
	if !foliage.initialized || lvl == nil || !gfx.FoliageShadows || len(foliage.draws) == 0 || cascade > 2 {
		return
	}
	r := &foliage
	p := &r.progs[foliageProgShadow]
	started := false
	reach := foliageShadowRange * FoliageDistanceScale()
	var current *mgl32.Mat4
	for di := range r.draws {
		dr := &r.draws[di]
		if dr.d > reach || (cull != nil && !AABBInFrustum(*cull, dr.wlo, dr.whi)) {
			continue
		}
		for bi := range dr.u.batches {
			b := &dr.u.batches[bi]
			if !foliageShapes[b.shape].casts {
				continue
			}
			pr := r.params[b.shape]
			if dr.d > pr.rng {
				continue
			}
			n := min(int32(math.Ceil(float64(float32(b.count)*foliageKeepRank(pr, dr.d)))), b.count)
			if n <= 0 {
				continue
			}
			if !started {
				gl.UseProgram(p.prog)
				m := lsm
				gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &m[0])
				r.uploadParams(foliageProgShadow)
				r.commonUniforms(p, gfx.Cam.Position)
				gl.Uniform2f(p.U("drawWindow"), 0, 0)
				gl.BindVertexArray(r.vao)
				started = true
			}
			if dr.model != current {
				current = dr.model
				setFoliageModel(p, *current)
			}
			gl.Uniform1i(p.U("drawBucket"), int32(r.bucket(b.shape, b.variant)))
			bindInstances(b.first)
			// The far mesh casts: a shadow is soft enough that nobody counts
			// its leaves, and a cascade redraws everything in it.
			drawTemplate(r.templates[b.shape][b.variant][1], n)
		}
	}
	if started {
		gl.BindVertexArray(0)
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)
		gl.UseProgram(Shadows.Program)
	}
}
