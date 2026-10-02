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
//     normal — is bit-for-bit the same become one vertex of the pool.
//  3. Group the triangles by slot and cut each slot into clusters
//     (cluster.Build), laying the index list out cluster by cluster. A
//     double-sided slot's clusters lose their normal cone.
//  4. Bound it, and bake its distance grid for GI at a resolution sized to
//     the mesh (sdfResolution).

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
	"foundry/internal/geom"
	"foundry/internal/gfx"
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
}

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

// HashSource is the hash a cook of the given files would carry: CookVersion,
// then each file's name and bytes.
func HashSource(files []string) ([32]byte, error) {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], nanite.CookVersion)
	h.Write(b[:])
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
	if m.SourceHash, err = HashSource(src.files); err != nil {
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
	m := &nanite.Mesh{Vertices: pool.Vertices, Normals: pool.Normals, Triangles: nt}

	// Triangles by slot, in source order within each.
	order := make([]int, nt)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return src.triSlot[order[a]] < src.triSlot[order[b]] })

	m.Indices = make([]uint32, 0, nt*3)
	for si := range src.slots {
		slot := src.slots[si]
		var idx []uint32
		for _, t := range order {
			if int(src.triSlot[t]) == si {
				idx = append(idx, pool.Indices[3*t:3*t+3]...)
			}
		}
		slot.First = int32(len(m.Clusters))
		if len(idx) > 0 {
			r := cluster.Build(cluster.Mesh{Positions: pool.Vertices, Stride: 5, Indices: idx}, opt.Cluster)
			base := uint32(len(m.Indices) / 3)
			m.Indices = append(m.Indices, r.Reorder(idx)...)
			for _, c := range r.Clusters {
				c.TriFirst += base
				if slot.DoubleSided {
					c.ConeCutoff = cluster.NoCone
				}
				m.Clusters = append(m.Clusters, c)
			}
		}
		slot.Count = int32(len(m.Clusters)) - slot.First
		m.Slots = append(m.Slots, slot)
	}

	// Bounds over what is drawn.
	first := true
	var tris []mgl32.Vec3
	for _, ix := range m.Indices {
		p := mgl32.Vec3{pool.Vertices[5*ix], pool.Vertices[5*ix+1], pool.Vertices[5*ix+2]}
		tris = append(tris, p)
		if first {
			m.Lo, m.Hi, first = p, p, false
			continue
		}
		m.Lo = mgl32.Vec3{min(m.Lo[0], p[0]), min(m.Lo[1], p[1]), min(m.Lo[2], p[2])}
		m.Hi = mgl32.Vec3{max(m.Hi[0], p[0]), max(m.Hi[1], p[1]), max(m.Hi[2], p[2])}
	}
	m.Center = m.Lo.Add(m.Hi).Mul(0.5)
	for _, p := range tris {
		m.Radius = max(m.Radius, p.Sub(m.Center).Len())
	}
	m.Radius = m.Radius*1.0001 + 1e-6

	ext := m.Hi.Sub(m.Lo)
	m.SDF = meshsdf.Bake(tris, sdfResolution(max(ext[0], ext[1], ext[2]), opt.SDFCell))
	return m, m.Validate()
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
		if fresh, _ := UpToDate(source, out); fresh {
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

// UpToDate reports whether out is a cook of source as it is now: the same
// format version, and the hash of the source's files matches.
func UpToDate(source, out string) (bool, error) {
	v, have, err := nanite.ReadHeader(out)
	if err != nil || v != nanite.FileVersion {
		return false, err
	}
	files, err := sourceFiles(source)
	if err != nil {
		return false, err
	}
	want, err := HashSource(files)
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
