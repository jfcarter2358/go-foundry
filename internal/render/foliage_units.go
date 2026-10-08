package render

// foliage_units.go — what foliage is built in, and where it is kept.
//
// # Units: sections, and clusters of them
//
// Foliage is placed per terrain section (world.TerrainSection, 16 blocks a
// side). Near the camera that is the unit it is built and drawn in. Further
// out a section's worth is too little to spend a draw on — at a kilometre a
// section holds a tree or two — so sections are grouped: a level-1 cluster is
// 4x4x4 of them, a level-2 cluster 4x4x4 of those. Past foliageClusterDist[k]
// a level-k cluster is built and drawn as one unit instead of its members,
// which is what keeps the draw count flat as the range grows: the area of a
// ring goes up with its distance, and so does the size of what it is drawn
// in.
//
// # Built for a distance
//
// A unit is built for the nearest distance it will be drawn at (builtFor),
// and holds only the ranks the thinning still draws there (world.
// FoliageRankCut): a cluster half a kilometre out places its trees, a few of
// its shrubs and nothing that stops short of it, and a candidate the cut
// rejects costs a hash. As the camera closes in and a unit comes nearer than
// it was built for, it is rebuilt with more. Because a cut build is exactly
// the full build with ranks missing, the plants a closer build adds appear
// among the ones already standing; none moves.
//
// When the camera crosses into a cluster's near side, its members are built
// first and the cluster keeps drawing until every one is ready, so the ground
// never goes bare while the finer units are placed. A merged build is spread
// over frames, member by member, under the same budget as everything else.
//
// # One pool
//
// Every unit's instances live in one GL buffer (foliagePool), a span each.
// The CPU path binds a batch's span as instanced attributes; the GPU cull
// (foliage_gpu.go) reads the whole pool as a buffer texture. A unit that goes
// unused for a while gives its span back.

import (
	"hash/fnv"
	"math"
	"sort"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// foliageClusterLevels is how many levels of cluster there are above the
// sections.
const foliageClusterLevels = 2

// foliageClusterShift is how many sections (as a power of two) a cluster
// spans per level on each axis: 4x4x4.
const foliageClusterShift = 2

// foliageClusterDist is, per level, the distance past which a cluster of that
// level is drawn whole, before FoliageDistance. Index 0 is unused.
var foliageClusterDist = [foliageClusterLevels + 1]float32{0, 150, 520}

// foliageFullDetail is how near a section is built with everything, however
// thin the thinning would already make its far side.
const foliageFullDetail = 24

// foliageBoundsPad is how far past its instances' own box a unit's box is
// taken to reach: a plant grows (foliageComp) to cover for the ones thinned
// out around it, and its box is measured before it grew.
const foliageBoundsPad = 3.5

// foliageJumpDist is how far the camera must move in one frame to count as
// having jumped, and foliageJumpBuild how far round where it lands is then
// built at once.
const (
	foliageJumpDist  = 40
	foliageJumpBuild = 120
)

// foliageUnitIdle is how many frames a built unit may go unused before it
// gives its instances back.
const foliageUnitIdle = 240

// foliageUnit is one section or cluster.
type foliageUnit struct {
	level int
	// secs are the sections it covers (level 0: just its own); kids its
	// members one level down.
	secs []int
	kids []int
	// meshLo, meshHi bound its sections' faces; empty when none has any.
	meshLo, meshHi mgl32.Vec3
	empty          bool

	built    bool
	gen      uint64
	builtFor float32
	first, n int
	batches  []foliageBatchGPU
	// trees are its tall plants' placements, kept on the CPU for the GI
	// field (nfoliage_gi.go); treesGen counts their rebuilds.
	trees    []nfGITree
	treesGen uint64
	// lo, hi bound its instances, chunk space.
	lo, hi mgl32.Vec3
	count  int
	used   uint64

	// merge is a cluster build under way.
	merge *foliageMerge
}

type foliageBatchGPU struct {
	shape        world.FoliageShape
	variant      int
	first, count int32
}

// foliageMerge is a cluster being built member by member.
type foliageMerge struct {
	gen      uint64
	builtFor float32
	next     int
	byKey    map[[2]int]*world.FoliageBatch
	lo, hi   mgl32.Vec3
}

type foliageTerrainGPU struct {
	// sections are the level-0 units, one per world.TerrainSection, in its
	// order; clusters[k-1] are level k.
	sections []foliageUnit
	clusters [foliageClusterLevels][]foliageUnit
	// tops are the top-level clusters.
	tops  []int
	seen  bool
	fresh bool
	// model is its chunk's placement this frame.
	model mgl32.Mat4
}

func (g *foliageTerrainGPU) level(k int) []foliageUnit {
	if k == 0 {
		return g.sections
	}
	return g.clusters[k-1]
}

// rebuildTree lays the units out over secs.
func (g *foliageTerrainGPU) rebuildTree(secs []*world.TerrainSection) {
	g.release()
	g.sections = make([]foliageUnit, len(secs))
	for i, s := range secs {
		u := &g.sections[i]
		u.secs = []int{i}
		u.meshLo, u.meshHi = s.Lo, s.Hi
		u.empty = s.Empty()
	}
	childKey := func(i int) [3]int { s := secs[i]; return [3]int{s.X, s.Y, s.Z} }
	prev := make([]int, len(secs))
	for i := range prev {
		prev[i] = i
	}
	prevLevel := g.sections
	for k := 1; k <= foliageClusterLevels; k++ {
		shift := uint(foliageClusterShift * k)
		index := map[[3]int]int{}
		var units []foliageUnit
		var keys [][3]int
		for _, c := range prev {
			sk := childKey(prevLevel[c].secs[0])
			key := [3]int{sk[0] >> shift, sk[1] >> shift, sk[2] >> shift}
			j, ok := index[key]
			if !ok {
				j = len(units)
				index[key] = j
				units = append(units, foliageUnit{level: k, empty: true})
				keys = append(keys, key)
			}
			u := &units[j]
			cu := &prevLevel[c]
			u.kids = append(u.kids, c)
			u.secs = append(u.secs, cu.secs...)
			if !cu.empty {
				if u.empty {
					u.meshLo, u.meshHi = cu.meshLo, cu.meshHi
				} else {
					u.meshLo, u.meshHi = boxUnion(u.meshLo, u.meshHi, cu.meshLo, cu.meshHi)
				}
				u.empty = false
			}
		}
		g.clusters[k-1] = units
		prevLevel = units
		prev = prev[:0]
		for j := range units {
			prev = append(prev, j)
		}
	}
	g.tops = append([]int(nil), prev...)
}

// refreshBoxes follows an edit that moved sections' faces: their boxes, and
// every cluster's above them, are taken again. What is built stays built —
// the edit's FoliageGen rebuilds only the units it reached, and until then
// they keep drawing what they had rather than going bare.
func (g *foliageTerrainGPU) refreshBoxes(secs []*world.TerrainSection) {
	changed := false
	for i, s := range secs {
		u := &g.sections[i]
		if u.meshLo != s.Lo || u.meshHi != s.Hi || u.empty != s.Empty() {
			u.meshLo, u.meshHi, u.empty = s.Lo, s.Hi, s.Empty()
			changed = true
		}
	}
	if !changed {
		return
	}
	for k := 1; k <= foliageClusterLevels; k++ {
		kids := g.level(k - 1)
		us := g.level(k)
		for i := range us {
			u := &us[i]
			u.empty = true
			for _, c := range u.kids {
				cu := &kids[c]
				if cu.empty {
					continue
				}
				if u.empty {
					u.meshLo, u.meshHi = cu.meshLo, cu.meshHi
				} else {
					u.meshLo, u.meshHi = boxUnion(u.meshLo, u.meshHi, cu.meshLo, cu.meshHi)
				}
				u.empty = false
			}
		}
	}
}

func boxUnion(alo, ahi, blo, bhi mgl32.Vec3) (mgl32.Vec3, mgl32.Vec3) {
	for k := 0; k < 3; k++ {
		alo[k] = min(alo[k], blo[k])
		ahi[k] = max(ahi[k], bhi[k])
	}
	return alo, ahi
}

func (g *foliageTerrainGPU) release() {
	for k := 0; k <= foliageClusterLevels; k++ {
		us := g.level(k)
		for i := range us {
			us[i].release()
		}
	}
	g.sections = nil
	g.clusters = [foliageClusterLevels][]foliageUnit{}
	g.tops = nil
}

func (u *foliageUnit) release() {
	if u.n > 0 {
		foliage.pool.free(u.first, u.n)
	}
	u.first, u.n = 0, 0
	u.built, u.gen, u.builtFor, u.count = false, 0, 0, 0
	u.batches = u.batches[:0]
	u.trees = u.trees[:0]
	u.treesGen++
	u.merge = nil
}

// ---------- the pool ----------

// foliagePool is every unit's instances in one buffer.
type foliagePool struct {
	vbo uint32
	// tex is vbo as an RGBA32F buffer texture, for the GPU cull; 0 without it.
	tex   uint32
	cap   int
	spans []poolSpan // free, sorted by first
	// gen changes whenever vbo is replaced.
	gen uint64
}

type poolSpan struct{ first, n int }

const foliagePoolInitial = 1 << 16

func (p *foliagePool) init(withTexture bool) {
	p.cap = foliagePoolInitial
	gl.GenBuffers(1, &p.vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, p.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, p.cap*foliageInstanceFloats*4, nil, gl.STATIC_DRAW)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	p.spans = []poolSpan{{0, p.cap}}
	if withTexture {
		gl.GenTextures(1, &p.tex)
		p.attachTexture()
	}
}

func (p *foliagePool) attachTexture() {
	if p.tex == 0 {
		return
	}
	gl.BindTexture(gl.TEXTURE_BUFFER, p.tex)
	gl.TexBuffer(gl.TEXTURE_BUFFER, gl.RGBA32F, p.vbo)
	gl.BindTexture(gl.TEXTURE_BUFFER, 0)
}

func (p *foliagePool) delete() {
	if p.vbo != 0 {
		gl.DeleteBuffers(1, &p.vbo)
	}
	if p.tex != 0 {
		gl.DeleteTextures(1, &p.tex)
	}
	*p = foliagePool{}
}

// alloc returns the first instance of a free span of n, growing the buffer
// when none is big enough.
func (p *foliagePool) alloc(n int) int {
	for {
		for i, s := range p.spans {
			if s.n >= n {
				first := s.first
				if s.n == n {
					p.spans = append(p.spans[:i], p.spans[i+1:]...)
				} else {
					p.spans[i] = poolSpan{s.first + n, s.n - n}
				}
				return first
			}
		}
		p.grow(max(p.cap*2, p.cap+n))
	}
}

// free gives a span back, merging it with its free neighbours.
func (p *foliagePool) free(first, n int) {
	i := sort.Search(len(p.spans), func(i int) bool { return p.spans[i].first > first })
	p.spans = append(p.spans, poolSpan{})
	copy(p.spans[i+1:], p.spans[i:])
	p.spans[i] = poolSpan{first, n}
	if i+1 < len(p.spans) && p.spans[i].first+p.spans[i].n == p.spans[i+1].first {
		p.spans[i].n += p.spans[i+1].n
		p.spans = append(p.spans[:i+1], p.spans[i+2:]...)
	}
	if i > 0 && p.spans[i-1].first+p.spans[i-1].n == p.spans[i].first {
		p.spans[i-1].n += p.spans[i].n
		p.spans = append(p.spans[:i], p.spans[i+1:]...)
	}
}

// grow moves the pool into a buffer of capacity c, spans kept in place.
func (p *foliagePool) grow(c int) {
	var nb uint32
	gl.GenBuffers(1, &nb)
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, nb)
	gl.BufferData(gl.COPY_WRITE_BUFFER, c*foliageInstanceFloats*4, nil, gl.STATIC_DRAW)
	gl.BindBuffer(gl.COPY_READ_BUFFER, p.vbo)
	gl.CopyBufferSubData(gl.COPY_READ_BUFFER, gl.COPY_WRITE_BUFFER, 0, 0, p.cap*foliageInstanceFloats*4)
	gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
	gl.DeleteBuffers(1, &p.vbo)
	p.vbo = nb
	p.free(p.cap, c-p.cap)
	p.cap = c
	p.gen++
	p.attachTexture()
}

// used is how many instances are allocated.
func (p *foliagePool) used() int {
	n := p.cap
	for _, s := range p.spans {
		n -= s.n
	}
	return n
}

// ---------- distances and cuts ----------

// foliageKeep is foliage_params.glsl's foliageKeep: the fraction of shape
// p's instances drawn at distance d.
func foliageKeep(p foliageShapeParams, d float32) float32 {
	thin := float32(1)
	if p.thin > 0 && d > p.start {
		thin = float32(math.Pow(float64(p.start/d), float64(p.thin)))
	}
	return thin * (1 - world.SmoothStep(p.rng*0.82, p.rng, d))
}

// foliageRankBand is FOLIAGE_RANK_BAND.
const foliageRankBand = 0.06

// foliageKeepRank is the highest rank anything at distance d or further can
// still show: every instance whose rank the fade band has not wholly passed.
func foliageKeepRank(p foliageShapeParams, d float32) float32 {
	return min(foliageKeep(p, d)*(1+foliageRankBand), 1)
}

// cutFor is the rank cut of a unit built for distance b; nil keeps all.
func cutFor(b float32) world.FoliageRankCut {
	if b <= 0 {
		return nil
	}
	params := foliage.params
	return func(s world.FoliageShape) float32 {
		p := params[s]
		if b >= p.rng {
			return 0
		}
		return foliageKeepRank(p, b)
	}
}

// ---------- building ----------

// foliageDraw is one unit to draw this frame.
type foliageDraw struct {
	model    *mgl32.Mat4
	u        *foliageUnit
	wlo, whi mgl32.Vec3
	d, dmax  float32
}

type foliageJob struct {
	t    *world.Terrain
	g    *foliageTerrainGPU
	u    *foliageUnit
	d    float32
	must bool
}

// foliageVisit is one frame's walk over a terrain's units.
type foliageVisit struct {
	t     *world.Terrain
	g     *foliageTerrainGPU
	secs  []*world.TerrainSection
	model *mgl32.Mat4
	cam   mgl32.Vec3
	reach float32
	dist  [foliageClusterLevels + 1]float32
	frame uint64
	jobs  *[]foliageJob
	draws *[]foliageDraw
}

// box is the chunk-space box a unit is measured by: its instances once
// built, its faces (with room for a tree on top) before.
func (u *foliageUnit) box() (mgl32.Vec3, mgl32.Vec3) {
	if u.built && u.count > 0 {
		pad := mgl32.Vec3{foliageBoundsPad, foliageBoundsPad, foliageBoundsPad}
		return u.lo.Sub(pad), u.hi.Add(pad)
	}
	return u.meshLo.Sub(mgl32.Vec3{2, 2, 2}), u.meshHi.Add(mgl32.Vec3{2, 14, 2})
}

func (v *foliageVisit) measure(u *foliageUnit) (wlo, whi mgl32.Vec3, d, dmax float32) {
	lo, hi := u.box()
	wlo, whi = transformBox(*v.model, lo, hi)
	d = boxDistance(v.cam, wlo, whi)
	for k := 0; k < 3; k++ {
		e := max(math.Abs(float64(v.cam[k]-wlo[k])), math.Abs(float64(whi[k]-v.cam[k])))
		dmax += float32(e * e)
	}
	dmax = float32(math.Sqrt(float64(dmax)))
	return
}

// genOf is what a unit's foliage depends on: its sections' FoliageGens.
func (v *foliageVisit) genOf(u *foliageUnit) uint64 {
	if u.level == 0 {
		return v.secs[u.secs[0]].FoliageGen
	}
	h := fnv.New64a()
	var b [8]byte
	for _, s := range u.secs {
		g := v.secs[s].FoliageGen
		for k := 0; k < 8; k++ {
			b[k] = byte(g >> (8 * k))
		}
		h.Write(b[:])
	}
	return h.Sum64() | 1
}

// target is the distance a unit wanted at d should be built for.
func (v *foliageVisit) target(u *foliageUnit, d float32) float32 {
	if u.level == 0 {
		if d < foliageFullDetail*FoliageDistanceScale() {
			return 0
		}
		return d * 0.7
	}
	return min(max(v.dist[u.level], d*0.7), d)
}

// want asks for u to be valid at d: built, current, and built for no
// further than d.
func (v *foliageVisit) want(u *foliageUnit, d float32) {
	u.used = v.frame
	gen := v.genOf(u)
	if u.built && u.gen == gen && u.builtFor <= d {
		return
	}
	*v.jobs = append(*v.jobs, foliageJob{t: v.t, g: v.g, u: u, d: d, must: v.g.fresh})
}

func (v *foliageVisit) draw(u *foliageUnit) {
	u.used = v.frame
	if u.count == 0 {
		return
	}
	wlo, whi, d, dmax := v.measure(u)
	*v.draws = append(*v.draws, foliageDraw{model: v.model, u: u, wlo: wlo, whi: whi, d: d, dmax: dmax})
}

// ready reports whether u can be drawn in place of its parent: every unit
// it would draw is built.
func (v *foliageVisit) ready(u *foliageUnit) bool {
	if u.empty {
		return true
	}
	if u.level == 0 {
		return u.built
	}
	_, _, d, _ := v.measure(u)
	if d >= v.dist[u.level] {
		return u.built
	}
	for _, k := range u.kids {
		if !v.ready(&v.g.level(u.level - 1)[k]) {
			return false
		}
	}
	return true
}

// visit walks u: draws what should show of it (when drawing) and asks for
// whatever is not yet built for where the camera is.
func (v *foliageVisit) visit(u *foliageUnit, drawing bool) {
	if u.empty {
		return
	}
	_, _, d, _ := v.measure(u)
	if d > v.reach {
		return
	}
	if u.level == 0 {
		v.want(u, d)
		if drawing && u.built {
			v.draw(u)
		}
		return
	}
	kids := v.g.level(u.level - 1)
	if d >= v.dist[u.level] {
		v.want(u, d)
		if !drawing {
			return
		}
		if u.built {
			v.draw(u)
		} else {
			// Not placed yet: show whatever finer units are left over.
			for _, k := range u.kids {
				v.drawBuilt(&kids[k])
			}
		}
		return
	}
	ready := true
	for _, k := range u.kids {
		if !v.ready(&kids[k]) {
			ready = false
			break
		}
	}
	if ready || !u.built {
		for _, k := range u.kids {
			v.visit(&kids[k], drawing)
		}
		return
	}
	// The finer units are still being placed: they are asked for, and the
	// cluster keeps the ground covered meanwhile.
	for _, k := range u.kids {
		v.visit(&kids[k], false)
	}
	if drawing {
		v.draw(u)
	}
}

// drawBuilt draws the coarsest built units under u.
func (v *foliageVisit) drawBuilt(u *foliageUnit) {
	if u.empty {
		return
	}
	if u.built {
		v.draw(u)
		return
	}
	if u.level == 0 {
		return
	}
	kids := v.g.level(u.level - 1)
	for _, k := range u.kids {
		v.drawBuilt(&kids[k])
	}
}

// syncFoliage brings the foliage of every terrain up to date, within the
// frame's budget, picks what this frame draws, and frees what departed
// terrains and idle units held. Called from SyncTerrainMeshes.
func syncFoliage(lvl *world.Level) {
	if !foliage.initialized {
		return
	}
	foliage.frame++
	foliage.updateParams()
	for _, g := range foliage.cache {
		g.seen = false
	}
	jobs := foliage.jobs[:0]
	scale := FoliageDistanceScale()
	var visits []foliageVisit
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
			g.rebuildTree(secs)
		} else {
			g.refreshBoxes(secs)
		}
		if len(t.Paint) == 0 {
			// Nothing grows; drop whatever used to.
			for k := 0; k <= foliageClusterLevels; k++ {
				us := g.level(k)
				for i := range us {
					if us[i].built {
						us[i].release()
					}
				}
			}
			g.fresh = false
			continue
		}
		if !terrainDrawable(ch) {
			continue
		}
		g.model = ch.ModelMatrix()
		v := foliageVisit{
			t: t, g: g, secs: secs, model: &g.model,
			cam: gfx.Cam.Position, reach: foliageMaxRange * scale, frame: foliage.frame,
		}
		for k := 1; k <= foliageClusterLevels; k++ {
			v.dist[k] = foliageClusterDist[k] * scale
		}
		visits = append(visits, v)
	}
	walk := func(jobs *[]foliageJob) {
		foliage.draws = foliage.draws[:0]
		for i := range visits {
			v := &visits[i]
			v.jobs, v.draws = jobs, &foliage.draws
			top := v.g.level(foliageClusterLevels)
			for _, i := range v.g.tops {
				v.visit(&top[i], true)
			}
		}
	}
	walk(&jobs)
	for i := range visits {
		visits[i].g.fresh = false
	}

	// A camera that jumped — a respawn, a checkpoint, a teleport — would
	// otherwise stand on bare ground while the budget catches up: what is
	// near it is built now, as a fresh terrain's is.
	cam := gfx.Cam.Position
	if foliage.lastCamSet && cam.Sub(foliage.lastCam).Len() > foliageJumpDist*scale {
		for i := range jobs {
			if jobs[i].d < foliageJumpBuild*scale {
				jobs[i].must = true
			}
		}
	}
	foliage.lastCam, foliage.lastCamSet = cam, true

	sort.Slice(jobs, func(a, b int) bool { return jobs[a].d < jobs[b].d })
	start := time.Now()
	built := map[*foliageUnit]bool{}
	for n, j := range jobs {
		if n > 0 && !j.must && time.Since(start) > foliageBuildBudget {
			break
		}
		if built[j.u] {
			continue
		}
		built[j.u] = true
		foliage.build(j, start)
	}
	foliage.jobs = jobs[:0]
	if len(built) > 0 {
		// What was just built draws this frame, not next: a terrain's first
		// frame (and so a headless shot) has its foliage in place.
		var again []foliageJob
		walk(&again)
	}
	// The placed plants go on the list after the terrains' (each walk
	// starts it afresh).
	foliage.placed.sync(lvl, foliage.frame, foliageMaxRange*scale)
	foliage.volumes.sync(lvl, foliage.frame, foliageMaxRange*scale)

	for t, g := range foliage.cache {
		if !g.seen {
			g.release()
			delete(foliage.cache, t)
			continue
		}
		for k := 0; k <= foliageClusterLevels; k++ {
			us := g.level(k)
			for i := range us {
				if (us[i].built || us[i].merge != nil) && foliage.frame-us[i].used > foliageUnitIdle {
					us[i].release()
				}
			}
		}
	}
}

// build places job j's unit, or — for a cluster — as much of it as the
// frame's budget allows.
func (r *foliageRenderer) build(j foliageJob, start time.Time) {
	v := foliageVisit{secs: j.t.Sections(), reach: foliageMaxRange * FoliageDistanceScale()}
	for k := 1; k <= foliageClusterLevels; k++ {
		v.dist[k] = foliageClusterDist[k] * FoliageDistanceScale()
	}
	u := j.u
	gen := v.genOf(u)
	b := v.target(u, j.d)
	if u.level == 0 {
		fs := j.t.BuildFoliageFor(v.secs[u.secs[0]], cutFor(b))
		r.upload(u, fs.Batches, fs.Count, fs.Lo, fs.Hi)
		u.gen, u.builtFor = gen, b
		return
	}
	m := u.merge
	if m == nil || m.gen != gen || m.builtFor > j.d {
		inf := float32(math.Inf(1))
		m = &foliageMerge{gen: gen, builtFor: b, byKey: map[[2]int]*world.FoliageBatch{},
			lo: mgl32.Vec3{inf, inf, inf}, hi: mgl32.Vec3{-inf, -inf, -inf}}
		u.merge = m
	}
	cut := cutFor(m.builtFor)
	for m.next < len(u.secs) {
		if !j.must && time.Since(start) > foliageBuildBudget && m.next > 0 {
			return
		}
		s := v.secs[u.secs[m.next]]
		m.next++
		if s.Empty() {
			continue
		}
		fs := j.t.BuildFoliageFor(s, cut)
		for _, bt := range fs.Batches {
			key := [2]int{int(bt.Shape), bt.Variant}
			mb := m.byKey[key]
			if mb == nil {
				mb = &world.FoliageBatch{Shape: bt.Shape, Variant: bt.Variant}
				m.byKey[key] = mb
			}
			mb.Instances = append(mb.Instances, bt.Instances...)
		}
		if fs.Count > 0 {
			m.lo, m.hi = boxUnion(m.lo, m.hi, fs.Lo, fs.Hi)
		}
	}
	keys := make([][2]int, 0, len(m.byKey))
	for k := range m.byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	batches := make([]world.FoliageBatch, 0, len(keys))
	count := 0
	for _, k := range keys {
		bt := m.byKey[k]
		sort.Slice(bt.Instances, func(i, j int) bool { return bt.Instances[i].Rank < bt.Instances[j].Rank })
		batches = append(batches, *bt)
		count += len(bt.Instances)
	}
	r.upload(u, batches, count, m.lo, m.hi)
	u.gen, u.builtFor = m.gen, m.builtFor
	u.merge = nil
}

// upload replaces u's instances with batches.
func (r *foliageRenderer) upload(u *foliageUnit, batches []world.FoliageBatch, count int, lo, hi mgl32.Vec3) {
	u.built = true
	u.batches = u.batches[:0]
	u.trees = nfKeepTrees(u.trees[:0], batches)
	u.treesGen++
	u.lo, u.hi, u.count = lo, hi, count
	if u.n > 0 && u.n != count {
		r.pool.free(u.first, u.n)
		u.first, u.n = 0, 0
	}
	if count == 0 {
		return
	}
	if u.n == 0 {
		u.first, u.n = r.pool.alloc(count), count
	}
	need := count * foliageInstanceFloats
	if cap(r.scratch) < need {
		r.scratch = make([]float32, need)
	}
	buf := r.scratch[:0]
	for _, b := range batches {
		u.batches = append(u.batches, foliageBatchGPU{shape: b.Shape, variant: b.Variant,
			first: int32(u.first + len(buf)/foliageInstanceFloats), count: int32(len(b.Instances))})
		for _, in := range b.Instances {
			seed := float32(math.Mod(float64(in.Rank)*7919.13, 1))
			buf = append(buf,
				in.Pos[0], in.Pos[1], in.Pos[2], in.Scale,
				in.Up[0], in.Up[1], in.Up[2], in.Yaw,
				in.Color[0], in.Color[1], in.Color[2], in.Rank,
				in.Accent[0], in.Accent[1], in.Accent[2], seed)
		}
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, r.pool.vbo)
	gl.BufferSubData(gl.ARRAY_BUFFER, u.first*foliageInstanceFloats*4, len(buf)*4, gl.Ptr(buf))
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
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
