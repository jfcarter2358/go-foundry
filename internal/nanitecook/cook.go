// Package nanitecook cooks Nanite meshes: it turns a glTF, OBJ or .fmdl source
// into the cooked .fnm file internal/nanite defines and the runtime loads.
// It is its own package, above internal/nanite, because a cook reaches
// internal/model and internal/gfx and the file's readers must not (see
// internal/nanite's package comment). `foundry nanite` (cmd.go) and the
// editor's Assets panel both cook through CookFile.
package nanitecook

// cook.go — turning a source mesh into a cooked one.
//
// The steps, in order:
//
//  1. Import. glTF (model.LoadGLTF) keeps its authored normals and material
//     slots; OBJ and .fmdl come through the model document, and are built
//     exactly as a scenery placement of them builds its mesh — the same
//     triangulation, the same smoothing at the document's crease angle — so a
//     cooked .fmdl draws the picture its uncooked self does.
//  2. Weld (gfx.WeldCorners): corners whose whole record — position, UV,
//     normal — is bit-for-bit the same become one vertex of the pool. Then
//     QUANTISE it (nanite.Quant, Phase 3): every position, UV and normal is
//     snapped to the integers the pages store, so everything below is made
//     from exactly what a load will decode.
//  3. Group the triangles by slot and build each slot's cluster DAG
//     (cluster.BuildDAG: cut, then group, simplify and re-cut a level at a
//     time down to a root), laying the index list out cluster by cluster,
//     every level of it. Where two slots meet is locked, so neither slot's
//     simplification opens a crack against the other. The simplification
//     weighs the normals and UVs (NormalWeight, UVWeight). A double-sided
//     slot's clusters lose their normal cone.
//  4. Build the tree the GPU's LOD selection walks (cluster.BuildLODTree),
//     and the fallback mesh: one cut of the DAG at a middle level's error.
//  5. Bound it, and bake its distance grid for GI from the fallback mesh at
//     a resolution sized to the mesh (sdfResolution).
//  6. Its collision (internal/meshcollide): the DAG cut at a fixed
//     world-space error (Options.Collision.Error, a few centimetres), cleaned,
//     quantised and put under a tree for the mesh mode, and split into convex
//     hulls for the hulls mode. The settings are folded into the source hash,
//     so a project that changes them (game.yaml's nanite block) re-cooks.
//  7. PAGE it (nanite.Mesh.BuildPages, Phase 3): the clusters packed into
//     fixed-size pages by the group that consumes them, each cluster's
//     vertices bit-packed on the quantisation grid. The pool the mesh keeps
//     in memory becomes the pages decoded — what a load sees, bit for bit.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/cluster"
	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/meshcollide"
	"foundry/internal/meshsdf"
	"foundry/internal/model"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// Options are a cook's settings.
type Options struct {
	// Cluster is the cut's limits; zero takes cluster's defaults.
	Cluster cluster.Options
	// SDFCell is the distance grid's target cell size in metres; 0 takes
	// DefaultSDFCell.
	SDFCell float32
	// NormalWeight and UVWeight weigh the normals and UVs in the DAG's
	// simplification, as a fraction of the mesh's radius per unit of the
	// attribute: with 0.2, a normal turned by 0.1 (about 6°) costs as much
	// as moving the surface a fiftieth of the radius. 0 takes the defaults;
	// < 0 ignores the attribute.
	NormalWeight, UVWeight float32
	// GroupSize is the DAG's clusters per group; 0 takes the default.
	GroupSize int
	// PositionBits, UVBits and NormalBits are the quantisation's
	// (nanite.NewQuant); 0 takes nanite's defaults.
	PositionBits, UVBits, NormalBits int
	// Collision is the collision cook's settings; zero fields take
	// meshcollide's defaults. ProjectOptions fills them from game.yaml.
	Collision meshcollide.Settings

	// serial cooks on the calling goroutine alone: the slots one at a
	// time, each DAG unfanned (cluster.DAGOptions.Serial). Set by a caller
	// already cooking several files side by side (cookAll). It changes how
	// long a cook takes, never what it makes, so it is not in the hash.
	serial bool
}

// parallelCook cooks side by side what can be: a mesh's slots (cookSource)
// and the files of one `foundry nanite cook` (cookAll). Off, everything is
// one at a time, in order, as it always was; a test switches it off to show
// both cook the same bytes.
var parallelCook = true

// ProjectOptions is the options a cook in the current project uses: the
// defaults, with game.yaml's nanite block over them. A manifest that cannot
// be read is the defaults — a cook should not fail for a typo in the window
// block.
func ProjectOptions() Options {
	n, err := game.ReadNanite(game.Path())
	if err != nil {
		return Options{}
	}
	return Options{Collision: meshcollide.Settings{
		Error:        n.CollisionError,
		MaxHulls:     n.CollisionMaxHulls,
		MaxHullVerts: n.CollisionMaxHullVerts,
		AutoSize:     n.CollisionAutoSize,
	}}
}

// The default attribute weights (Options.NormalWeight, UVWeight). Chosen
// on the bumpy sample rock drawn in its normal colours, which shows a turned
// normal more plainly than any lit texture: at 0.2 a cut at the default 1 px
// threshold is within 24/255 of the full detail over 98% of the picture
// (render/nanite_lod_gl_test.go); at 0.1 the first simplification of a
// metre-sized rock cost half as much error, and visibly shifted the shading
// where the cut changed. docs/nanite/PHASE_2.md has the numbers.
const (
	DefaultNormalWeight = 0.2
	DefaultUVWeight     = 0.1
)

// DefaultSDFCell is the distance grid's target cell: a little coarser than
// GI's finest clipmap level, which is all a trace can resolve anyway.
const DefaultSDFCell = 0.15

// The distance grid's resolution bounds: never coarser than a small model's
// 24 cells a side, never more than 64 (a quarter of a million voxels, a
// megabyte of floats) — past that a mesh is big enough that GI's clipmap
// resolves it no better.
const (
	sdfMinRes = 24
	sdfMaxRes = 64
)

// sdfResolution is the cells a side for a mesh whose largest extent is ext.
func sdfResolution(ext, cell float32) int {
	if cell <= 0 {
		cell = DefaultSDFCell
	}
	n := int(math.Ceil(float64(ext/cell))) + 5 // the bake keeps two cells of air a side
	return min(max(n, sdfMinRes), sdfMaxRes)
}

// source is a mesh ready to cook: a welded pool, its triangles' slots.
type source struct {
	pool    gfx.IndexedMesh
	triSlot []int32
	slots   []nanite.Slot
	files   []string
}

// importSource reads path, whatever kind of mesh it is.
func importSource(path string) (*source, error) {
	switch {
	case model.IsGLTFPath(path):
		m, err := model.LoadGLTF(path)
		if err != nil {
			return nil, err
		}
		return fromImport(m), nil
	case strings.EqualFold(filepath.Ext(path), model.ModelExt), strings.EqualFold(filepath.Ext(path), model.ObjExt):
		var doc *model.ModelDoc
		var err error
		if strings.EqualFold(filepath.Ext(path), model.ModelExt) {
			doc, err = model.LoadModel(path)
		} else {
			doc, err = model.LoadOBJMesh(path)
		}
		if err != nil {
			return nil, err
		}
		src := fromDoc(doc)
		src.files = []string{path}
		if strings.EqualFold(filepath.Ext(path), model.ObjExt) {
			// The .mtl beside it names the texture.
			if mtl := strings.TrimSuffix(path, filepath.Ext(path)) + ".mtl"; fileExists(mtl) {
				src.files = append(src.files, mtl)
			}
		}
		return src, nil
	}
	return nil, fmt.Errorf("%s: not a mesh a Nanite cook reads (.gltf, .glb, .obj, .fmdl)", filepath.Base(path))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// fromImport is a glTF import as a cook source. Normals the file did not
// give (none at all, or a zero one) are derived the way the engine derives a
// model's: smoothed by position at the default crease angle.
func fromImport(m *model.ImportMesh) *source {
	n := len(m.Positions)
	rows := make([][]float32, n)
	for v := range rows {
		var uv mgl32.Vec2
		if m.UVs != nil {
			uv = m.UVs[v]
		}
		p := m.Positions[v]
		rows[v] = []float32{p[0], p[1], p[2], uv[0], uv[1]}
	}
	idx := make([]int, len(m.Indices))
	for i, x := range m.Indices {
		idx[i] = int(x)
	}
	// The soup, one record a corner.
	soup := gfx.IndexedMesh{Vertices: make([]float32, 0, len(idx)*5), Normals: make([]float32, 0, len(idx)*3)}
	var derived []float32
	for k, v := range idx {
		soup.Vertices = append(soup.Vertices, rows[v]...)
		var nn mgl32.Vec3
		if m.Normals != nil {
			nn = m.Normals[v]
		}
		if nn == (mgl32.Vec3{}) {
			if derived == nil {
				derived = geom.ComputeSmoothNormals(rows, idx, 0)
			}
			nn = mgl32.Vec3{derived[3*k], derived[3*k+1], derived[3*k+2]}
		}
		soup.Normals = append(soup.Normals, nn[0], nn[1], nn[2])
	}
	src := &source{pool: gfx.WeldCorners(soup), triSlot: append([]int32(nil), m.TriSlot...), files: m.Files}
	for _, s := range m.Slots {
		src.slots = append(src.slots, nanite.Slot{Name: s.Name, Texture: s.Texture, Smooth: true,
			DoubleSided: s.DoubleSided, BaseColor: s.BaseColor})
	}
	return src
}

// fromDoc is a model document as a cook source: its one submesh, built as a
// scenery placement builds it (world.Scenery.Load), with no skin — a cooked
// mesh is static.
func fromDoc(doc *model.ModelDoc) *source {
	src := &source{}
	for _, sub := range doc.ModelSubmeshes() {
		if len(sub.Indices) == 0 {
			continue
		}
		g := gfx.Geometry{Smooth: sub.Smooth, SmoothAngle: doc.SmoothAngle, AutoSmooth: true, CPUOnly: true}
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, mgl32.Vec3{}); err != nil || len(g.Vertices) == 0 {
			continue
		}
		part := gfx.WeldCorners(gfx.IndexedMesh{Vertices: g.Vertices, Normals: g.Normals})
		slot := int32(len(src.slots))
		name := sub.Texture
		if name == "" {
			name = "default"
		}
		src.slots = append(src.slots, nanite.Slot{Name: name, Texture: sub.Texture, Smooth: sub.Smooth, DoubleSided: true,
			BaseColor: [4]float32{1, 1, 1, 1}})
		base := uint32(src.pool.VertexCount())
		src.pool.Vertices = append(src.pool.Vertices, part.Vertices...)
		src.pool.Normals = append(src.pool.Normals, part.Normals...)
		for _, ix := range part.Indices {
			src.pool.Indices = append(src.pool.Indices, base+ix)
		}
		for t := 0; t < len(part.Indices)/3; t++ {
			src.triSlot = append(src.triSlot, slot)
		}
	}
	if len(src.pool.Normals) != src.pool.VertexCount()*3 {
		src.pool.Normals = make([]float32, src.pool.VertexCount()*3)
	}
	return src
}

// HashSource is the hash a cook of the given files with opt would carry:
// CookVersion, the collision settings (resolved, so the defaults spelled out
// and left implicit hash alike), then each file's name and bytes.
func HashSource(files []string, opt Options) ([32]byte, error) {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], nanite.CookVersion)
	h.Write(b[:])
	cs := opt.Collision.Resolved()
	for _, f := range []float32{cs.Error, float32(cs.MaxHulls), float32(cs.MaxHullVerts), cs.Concavity, cs.ConcavityRel, cs.AutoSize, float32(cs.Mode)} {
		binary.LittleEndian.PutUint32(b[:4], math.Float32bits(f))
		h.Write(b[:4])
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return [32]byte{}, err
		}
		h.Write([]byte(filepath.Base(f)))
		binary.LittleEndian.PutUint64(b[:], uint64(len(data)))
		h.Write(b[:])
		h.Write(data)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// Cook imports path and cooks it.
func Cook(path string, opt Options) (*nanite.Mesh, error) {
	src, err := importSource(path)
	if err != nil {
		return nil, err
	}
	m, err := cookSource(src, opt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if m.SourceHash, err = HashSource(src.files, opt); err != nil {
		return nil, err
	}
	m.Source = fileutil.RelAssetPath(path)
	return m, nil
}

func cookSource(src *source, opt Options) (*nanite.Mesh, error) {
	pool := &src.pool
	nt := len(pool.Indices) / 3
	if nt == 0 || len(src.triSlot) != nt {
		return nil, fmt.Errorf("no triangles to cook")
	}
	// Quantise the pool before anything is made from it.
	var qlo, qhi mgl32.Vec3
	maxUV := float32(0)
	for v := 0; v < pool.VertexCount(); v++ {
		p := mgl32.Vec3{pool.Vertices[5*v], pool.Vertices[5*v+1], pool.Vertices[5*v+2]}
		if v == 0 {
			qlo, qhi = p, p
		}
		qlo = mgl32.Vec3{min(qlo[0], p[0]), min(qlo[1], p[1]), min(qlo[2], p[2])}
		qhi = mgl32.Vec3{max(qhi[0], p[0]), max(qhi[1], p[1]), max(qhi[2], p[2])}
		maxUV = max(maxUV, float32(math.Abs(float64(pool.Vertices[5*v+3]))), float32(math.Abs(float64(pool.Vertices[5*v+4]))))
	}
	if len(pool.Normals) != pool.VertexCount()*3 {
		pool.Normals = make([]float32, pool.VertexCount()*3)
	}
	quant := nanite.NewQuant(qlo, qhi, maxUV, opt.PositionBits, opt.UVBits, opt.NormalBits)
	qverts := quant.Quantize(pool.Vertices, pool.Normals)

	m := &nanite.Mesh{Vertices: pool.Vertices, Normals: pool.Normals, Triangles: nt, Quant: quant}

	// Triangles by slot, in source order within each.
	order := make([]int, nt)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return src.triSlot[order[a]] < src.triSlot[order[b]] })

	// The simplifier's view of the pool: position, UV and normal per vertex.
	nv := pool.VertexCount()
	attrs := make([]float32, 0, nv*8)
	for v := 0; v < nv; v++ {
		attrs = append(attrs, pool.Vertices[5*v:5*v+5]...)
		attrs = append(attrs, pool.Normals[3*v:3*v+3]...)
	}
	weights := attrWeights(pool, opt)

	// A position two slots share is locked: each slot simplifies alone, and
	// they must still meet.
	slotAt := map[[3]uint32]int32{}
	sharedPos := map[[3]uint32]bool{}
	key := func(v uint32) [3]uint32 {
		return [3]uint32{math.Float32bits(pool.Vertices[5*v]), math.Float32bits(pool.Vertices[5*v+1]), math.Float32bits(pool.Vertices[5*v+2])}
	}
	for t := 0; t < nt; t++ {
		for k := 0; k < 3; k++ {
			p := key(pool.Indices[3*t+k])
			if s, ok := slotAt[p]; !ok {
				slotAt[p] = src.triSlot[t]
			} else if s != src.triSlot[t] {
				sharedPos[p] = true
			}
		}
	}
	locked := func(v uint32) bool { return sharedPos[key(v)] }

	// Each slot's DAG. The slots simplify alone (that is what the locks
	// above are for) and read only the pool, so they are built side by side
	// (cluster.BuildJobs) and laid out below in slot order, which is all the
	// file sees of them — the same bytes as one slot at a time.
	idxs := make([][]uint32, len(src.slots))
	for _, t := range order {
		si := int(src.triSlot[t])
		if si < 0 || si >= len(idxs) {
			continue // in no slot: never cooked, as when each slot gathered its own
		}
		idxs[si] = append(idxs[si], pool.Indices[3*t:3*t+3]...)
	}
	sizes := make([]int, len(src.slots))
	for si := range idxs {
		sizes[si] = len(idxs[si]) / 3
	}
	dags := make([]cluster.DAG, len(src.slots))
	cluster.BuildJobs(sizes, parallelCook && !opt.serial, func(si int, inner bool) {
		if len(idxs[si]) == 0 {
			return
		}
		dags[si] = cluster.BuildDAG(cluster.Mesh{Positions: attrs, Stride: 8, Indices: idxs[si]}, cluster.DAGOptions{
			Cluster: opt.Cluster, GroupSize: opt.GroupSize, AttrWeights: weights, Locked: locked, Serial: opt.serial || !inner})
	})

	m.Indices = make([]uint32, 0, nt*3*2)
	var ranges [][2]int32
	for si := range src.slots {
		slot := src.slots[si]
		idx := idxs[si]
		slot.First = int32(len(m.Clusters))
		if len(idx) > 0 {
			d := &dags[si]
			base := uint32(len(m.Indices) / 3)
			m.Indices = append(m.Indices, d.Indices...)
			for _, c := range d.Clusters {
				c.TriFirst += base
				if slot.DoubleSided {
					c.ConeCutoff = cluster.NoCone
				}
				// Group numbers are the slot's own; they only name runs.
				if c.Level == 0 {
					slot.BaseCount++
				}
				m.Clusters = append(m.Clusters, c)
			}
			m.Levels = max(m.Levels, int32(d.Levels))
		}
		slot.Count = int32(len(m.Clusters)) - slot.First
		ranges = append(ranges, [2]int32{slot.First, slot.Count})
		m.Slots = append(m.Slots, slot)
	}
	m.Nodes, m.ClusterLeaf = cluster.BuildLODTree(m.Clusters, ranges)
	for _, n := range m.Nodes {
		m.Leaves = max(m.Leaves, n.Leaf+1)
	}

	// The fallback: every slot cut at the error of the middle level, over a
	// small pool of its own (so a placement has it without the pages).
	if m.Levels > 1 {
		mid := (m.Levels - 1) / 2
		for i := range m.Clusters {
			if m.Clusters[i].Level == mid {
				m.FallbackError = max(m.FallbackError, m.Clusters[i].Error)
			}
		}
	}
	for si := range m.Slots {
		s := &m.Slots[si]
		s.FallbackFirst = uint32(len(m.Fallback) / 3)
		for ci := s.First; ci < s.First+s.Count; ci++ {
			c := &m.Clusters[ci]
			if c.Selected(m.FallbackError) {
				m.Fallback = append(m.Fallback, m.Indices[3*c.TriFirst:3*(c.TriFirst+c.TriCount)]...)
			}
		}
		s.FallbackCount = uint32(len(m.Fallback)/3) - s.FallbackFirst
	}
	remap := map[uint32]uint32{}
	for i, ix := range m.Fallback {
		nix, ok := remap[ix]
		if !ok {
			nix = uint32(len(m.FallbackVertices) / 5)
			remap[ix] = nix
			m.FallbackVertices = append(m.FallbackVertices, pool.Vertices[5*ix:5*ix+5]...)
			m.FallbackNormals = append(m.FallbackNormals, pool.Normals[3*ix:3*ix+3]...)
		}
		m.Fallback[i] = nix
	}

	// Bounds over the full detail (every level's corners are some of its
	// corners, so they bound the rest too).
	first := true
	for _, ix := range m.BaseIndices() {
		p := mgl32.Vec3{pool.Vertices[5*ix], pool.Vertices[5*ix+1], pool.Vertices[5*ix+2]}
		if first {
			m.Lo, m.Hi, first = p, p, false
			continue
		}
		m.Lo = mgl32.Vec3{min(m.Lo[0], p[0]), min(m.Lo[1], p[1]), min(m.Lo[2], p[2])}
		m.Hi = mgl32.Vec3{max(m.Hi[0], p[0]), max(m.Hi[1], p[1]), max(m.Hi[2], p[2])}
	}
	m.Center = m.Lo.Add(m.Hi).Mul(0.5)
	for _, ix := range m.BaseIndices() {
		p := mgl32.Vec3{pool.Vertices[5*ix], pool.Vertices[5*ix+1], pool.Vertices[5*ix+2]}
		m.Radius = max(m.Radius, p.Sub(m.Center).Len())
	}
	m.Radius = m.Radius*1.0001 + 1e-6

	// The distance grid, from the fallback: GI's clipmap resolves nothing
	// finer than a few centimetres, and the fallback is within its error of
	// the full detail.
	var tris []mgl32.Vec3
	for _, ix := range m.Fallback {
		fv := m.FallbackVertices
		tris = append(tris, mgl32.Vec3{fv[5*ix], fv[5*ix+1], fv[5*ix+2]})
	}
	ext := m.Hi.Sub(m.Lo)
	m.SDF = meshsdf.Bake(tris, sdfResolution(max(ext[0], ext[1], ext[2]), opt.SDFCell))

	coll, err := cookCollision(m, opt.Collision)
	if err != nil {
		return nil, fmt.Errorf("collision: %w", err)
	}
	m.Collision = coll
	m.ClosedSolid = m.Closed()

	// Pages last: they replace the pool with its decoded self.
	if err := m.BuildPages(qverts); err != nil {
		return nil, err
	}
	return m, m.Validate()
}

// cookCollision builds a mesh's collision: the DAG cut at the collision
// error over every slot (each triangle tagged with its slot), handed to
// meshcollide to clean, tree and decompose.
func cookCollision(m *nanite.Mesh, s meshcollide.Settings) (*meshcollide.Data, error) {
	s = s.Resolved()
	var idx []uint32
	var tags []uint8
	names := make([]string, 0, len(m.Slots))
	for si := range m.Slots {
		sl := &m.Slots[si]
		names = append(names, sl.Name)
		for ci := sl.First; ci < sl.First+sl.Count; ci++ {
			c := &m.Clusters[ci]
			if !c.Selected(s.Error) {
				continue
			}
			idx = append(idx, m.Indices[3*c.TriFirst:3*(c.TriFirst+c.TriCount)]...)
			for t := uint32(0); t < c.TriCount; t++ {
				tags = append(tags, uint8(min(si, 255)))
			}
		}
	}
	return meshcollide.Build(meshcollide.Input{Positions: m.Vertices, Stride: 5, Indices: idx, TriTags: tags, TagNames: names}, s)
}

// attrWeights is the simplification's weights for the pool's U, V and the
// normal's three floats, from opt and the mesh's size.
func attrWeights(pool *gfx.IndexedMesh, opt Options) []float32 {
	nw, uw := opt.NormalWeight, opt.UVWeight
	if nw == 0 {
		nw = DefaultNormalWeight
	}
	if uw == 0 {
		uw = DefaultUVWeight
	}
	nw, uw = max(nw, 0), max(uw, 0)
	// The radius, roughly: half the box's diagonal.
	var lo, hi mgl32.Vec3
	for v := 0; v < pool.VertexCount(); v++ {
		p := mgl32.Vec3{pool.Vertices[5*v], pool.Vertices[5*v+1], pool.Vertices[5*v+2]}
		if v == 0 {
			lo, hi = p, p
			continue
		}
		lo = mgl32.Vec3{min(lo[0], p[0]), min(lo[1], p[1]), min(lo[2], p[2])}
		hi = mgl32.Vec3{max(hi[0], p[0]), max(hi[1], p[1]), max(hi[2], p[2])}
	}
	r := hi.Sub(lo).Len() / 2
	return []float32{uw * r, uw * r, nw * r, nw * r, nw * r}
}

// CookedPath is where a source's cooked mesh goes by default: beside it,
// under the same name.
func CookedPath(source string) string {
	return strings.TrimSuffix(source, filepath.Ext(source)) + nanite.Ext
}

// CookResult says what CookFile did.
type CookResult struct {
	Out     string
	Skipped bool // the file on disk already matched the source
	Mesh    *nanite.Mesh
}

// CookFile cooks source into out (CookedPath when empty), unless out already
// holds a cook of exactly this source and force is off.
func CookFile(source, out string, force bool, opt Options) (CookResult, error) {
	if out == "" {
		out = CookedPath(source)
	}
	res := CookResult{Out: out}
	if !force {
		if fresh, _ := UpToDateWith(source, out, opt); fresh {
			res.Skipped = true
			return res, nil
		}
	}
	m, err := Cook(source, opt)
	if err != nil {
		return res, err
	}
	if err := m.Write(out); err != nil {
		return res, err
	}
	res.Mesh = m
	return res, nil
}

// UpToDate reports whether out is a cook of source as it is now, with the
// project's options (ProjectOptions): the same format version, and the hash
// of the source's files and the settings matches.
func UpToDate(source, out string) (bool, error) {
	return UpToDateWith(source, out, ProjectOptions())
}

// UpToDateWith is UpToDate for a cook with opt.
func UpToDateWith(source, out string, opt Options) (bool, error) {
	v, have, err := nanite.ReadHeader(out)
	if err != nil || v != nanite.FileVersion {
		return false, err
	}
	files, err := sourceFiles(source)
	if err != nil {
		return false, err
	}
	want, err := HashSource(files, opt)
	if err != nil {
		return false, err
	}
	return have == want, nil
}

// sourceFiles is every file a cook of source reads, without cooking it.
func sourceFiles(source string) ([]string, error) {
	if model.IsGLTFPath(source) {
		m, err := model.LoadGLTF(source)
		if err != nil {
			return nil, err
		}
		return m.Files, nil
	}
	files := []string{source}
	if strings.EqualFold(filepath.Ext(source), model.ObjExt) {
		if mtl := strings.TrimSuffix(source, filepath.Ext(source)) + ".mtl"; fileExists(mtl) {
			files = append(files, mtl)
		}
	}
	return files, nil
}

// IsSourcePath reports whether path is a mesh a cook reads.
func IsSourcePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == model.GLTFExt || ext == model.GLBExt || ext == strings.ToLower(model.ObjExt) || ext == strings.ToLower(model.ModelExt)
}
