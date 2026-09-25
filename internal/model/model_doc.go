package model

// model_doc.go — ModelDoc, the model editor's mesh document.
//
// Geometry (geometry.go) is a GPU buffer, not an editing representation: it is
// de-indexed, triangles-only, and carries exactly one texture. None of that
// survives an edit — moving a vertex would mean finding every copy of it in the
// flat buffer, and giving one face its own texture would mean splitting the
// buffer in half.
//
// ModelDoc is the editable side of that divide: a shared vertex pool plus n-gon
// faces that index into it, each face owning its own UVs, texture and smoothing
// flag. It compiles *down* to a set of Geometry (one per texture) whenever the
// editor needs to draw, and that compilation is the only place triangles exist.
//
// Faces stay n-gons rather than being triangulated on load because the whole
// point of the editor is that "the face you clicked" is a thing you can extrude,
// subdivide and texture as a unit.

import (
	"foundry/internal/geom"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// ModelFace is one CCW polygon of the mesh. Verts indexes ModelDoc.Verts; UVs
// is index-parallel to Verts, so UVs[i] belongs to Verts[i].
//
// A face carries no texture of its own: the whole document wears one texture
// (ModelDoc.Texture), and a face's share of it is decided by where its UVs
// land. See the note on ModelDoc.Texture for why.
type ModelFace struct {
	Verts  []int
	UVs    []mgl32.Vec2
	Smooth bool
}

// clone returns a deep copy; the slices must not be shared with the original or
// an edit to one face would silently rewrite another.
func (f ModelFace) Clone() ModelFace {
	c := f
	c.Verts = append([]int(nil), f.Verts...)
	c.UVs = append([]mgl32.Vec2(nil), f.UVs...)
	return c
}

// ModelDoc is the mesh being edited, together with the armature that deforms
// it. Bones and Weights are described in model_bone.go and model_skin.go.
//
// The armature is *always* here in memory, but it does not always live in the
// .fmdl: a document with Skeleton set reads its bones and clips from a shared
// .fskl and writes them back there (model_skeleton.go). Everything downstream —
// posing, skinning, the runtime player — sees the same fields either way.
type ModelDoc struct {
	ID string
	// Skeleton is the .fskl this model's armature and clips belong to, by bare
	// name ("human"), or "" when the model owns them itself. Several models
	// naming one skeleton share a rig: fixing a walk cycle fixes it on all of
	// them, which is the whole point of the split.
	Skeleton string
	// Texture is the one image the whole model is painted with — the same
	// logical name a brush face's Texture holds. Empty means the model wears
	// none, and is drawn from its surface normals instead (NormalColorFlag in
	// gfx/geometry.go); it is the state a new document starts in.
	//
	// One texture per model, rather than one per face, because the two ways of
	// texturing a mesh answer different questions and only one of them scales.
	// Per-face textures suit a mesh assembled out of tiling materials, where
	// each face repeats a brick or a plank; they cannot express a model that is
	// *painted*, because a painted model needs every face to own a distinct
	// region of one image. That is what an atlas UV unwrap produces
	// (model_uv_atlas.go), and it is the workflow the texture editor is built
	// around: unwrap, paint the atlas, watch the model update. Keeping a
	// per-face override alongside it would mean the painted atlas silently did
	// nothing on any face that had been pointed elsewhere.
	//
	// It also makes the mesh one draw call, which the per-texture submesh split
	// below no longer has to guess at.
	Texture string
	Verts   []mgl32.Vec3
	Faces   []ModelFace
	Bones   []ModelBone
	// Weights is index-parallel to Verts: Weights[i] is what moves Verts[i].
	// A nil or empty entry is an unbound vertex, which no bone moves.
	Weights [][]ModelVertexWeight
	// Animations are the keyframed clips that drive Bones (model_anim.go). They
	// live here rather than in a sidecar file for the same reason the armature
	// does: a .fmdl is the one thing the level places, and motion kept elsewhere
	// would be a second file to keep in step.
	Animations  []ModelClip
	SmoothAngle float32 // 0 = DefaultSmoothAngle (smooth.go)
	Path        string  // on-disk .fmdl path; empty until first Save As

	adj   *modelAdjacency // derived; nil when stale
	dirty bool            // geometry needs recompiling for the GPU
	// rev counts edits. Derived data that is expensive to rebuild — the
	// editor's visibility pass — compares it to know whether the mesh it was
	// computed from is still the mesh on screen.
	rev uint64
}

// SetTexture repoints the whole model at tex. The empty string is a real
// choice — "painted with nothing", which the renderer draws from the surface
// normals — and not a missing value to be filled in.
func (m *ModelDoc) SetTexture(tex string) {
	if m.Texture == tex {
		return
	}
	m.Texture = tex
	m.Touch()
}

// Rev returns the document's edit counter.
func (m *ModelDoc) Rev() uint64 { return m.rev }

// Clone returns a deep copy of the document. Used by the undo snapshot path and
// by any operation that wants to try an edit before committing it.
func (m *ModelDoc) Clone() *ModelDoc {
	c := &ModelDoc{
		ID:          m.ID,
		Skeleton:    m.Skeleton,
		Texture:     m.Texture,
		Verts:       append([]mgl32.Vec3(nil), m.Verts...),
		Faces:       make([]ModelFace, len(m.Faces)),
		Bones:       CloneBones(m.Bones),
		Weights:     cloneWeights(m.Weights),
		Animations:  cloneClips(m.Animations),
		SmoothAngle: m.SmoothAngle,
		Path:        m.Path,
	}
	for i := range m.Faces {
		c.Faces[i] = m.Faces[i].Clone()
	}
	return c
}

// touch marks the document as needing a GPU rebuild. Topology edits must use
// invalidate instead, which also drops the cached adjacency.
func (m *ModelDoc) Touch() {
	m.dirty = true
	m.rev++
}

// invalidate marks the document dirty *and* discards the derived adjacency
// index. Anything that adds, removes or rewires a face or vertex must call this
// — a stale adjacency map would hand out indices that no longer exist.
func (m *ModelDoc) Invalidate() {
	m.Touch()
	m.adj = nil
}

// Dirty reports whether the GPU submeshes are out of date.
func (m *ModelDoc) Dirty() bool { return m.dirty }

// ClearDirty is called by the renderer once it has rebuilt its Geometry set.
func (m *ModelDoc) ClearDirty() { m.dirty = false }

// ---------- the starting document ----------

// NewCubeDoc returns the model editor's starting document: a 1x1x1 cube centred
// on the origin, six quad faces over eight shared vertices, wound CCW when seen
// from outside.
//
// The editor is never without a document — opening the window with nothing
// loaded, and File > New, both land here, the same way the app boots into
// NewBlankLevel (level_loader.go) rather than a file dialog. There is nothing
// special about the result: it is an ordinary mesh, immediately extrudable and
// subdividable.
//
// The faces are quads rather than triangle pairs on purpose. Extrude and
// midpoint-subdivide both operate per face, so a cube of six quads behaves the
// way someone expects ("pull the top face up"), while a cube of twelve
// triangles would extrude half a face at a time.
func NewCubeDoc() *ModelDoc {
	const h = 0.5
	m := &ModelDoc{
		ID: "untitled",
		Verts: []mgl32.Vec3{
			{-h, -h, -h}, // 0
			{h, -h, -h},  // 1
			{h, -h, h},   // 2
			{-h, -h, h},  // 3
			{-h, h, -h},  // 4
			{h, h, -h},   // 5
			{h, h, h},    // 6
			{-h, h, h},   // 7
		},
	}
	// Each loop is CCW viewed from outside the cube, so faceNormal points away
	// from the centre.
	quads := [6][4]int{
		{3, 2, 6, 7}, // +Z front
		{1, 0, 4, 5}, // -Z back
		{2, 1, 5, 6}, // +X right
		{0, 3, 7, 4}, // -X left
		{4, 7, 6, 5}, // +Y top
		{3, 0, 1, 2}, // -Y bottom
	}
	unitUV := []mgl32.Vec2{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	for _, q := range quads {
		m.Faces = append(m.Faces, ModelFace{
			Verts: []int{q[0], q[1], q[2], q[3]},
			UVs:   append([]mgl32.Vec2(nil), unitUV...),
		})
	}
	m.Invalidate()
	return m
}

// ---------- topology ----------

// EdgeKey identifies an undirected edge. A is always the smaller index, so the
// same edge reached from either of its two faces produces the same key.
type EdgeKey struct{ A, B int }

func MakeEdgeKey(a, b int) EdgeKey {
	if a > b {
		a, b = b, a
	}
	return EdgeKey{a, b}
}

// modelAdjacency is the derived "what touches what" index. A half-edge
// structure would answer the same questions, but at editor mesh sizes rebuilding
// two maps after each edit is cheaper than maintaining one incrementally — and
// far harder to get subtly wrong.
type modelAdjacency struct {
	vertFaces map[int][]int     // vertex index -> face indices touching it
	edgeFaces map[EdgeKey][]int // edge -> face indices; len 1 = boundary, 2 = interior
	edges     []EdgeKey         // deterministic order, for edge selection/picking
}

// adjacency returns the topology index, rebuilding it if a topology edit has
// invalidated the cache.
func (m *ModelDoc) adjacency() *modelAdjacency {
	if m.adj != nil {
		return m.adj
	}
	a := &modelAdjacency{
		vertFaces: make(map[int][]int, len(m.Verts)),
		edgeFaces: make(map[EdgeKey][]int, len(m.Faces)*4),
	}
	for fi := range m.Faces {
		f := &m.Faces[fi]
		n := len(f.Verts)
		for i, v := range f.Verts {
			a.vertFaces[v] = append(a.vertFaces[v], fi)
			e := MakeEdgeKey(v, f.Verts[(i+1)%n])
			a.edgeFaces[e] = append(a.edgeFaces[e], fi)
		}
	}
	a.edges = make([]EdgeKey, 0, len(a.edgeFaces))
	for e := range a.edgeFaces {
		a.edges = append(a.edges, e)
	}
	// Map iteration order is random; sort so edge indices are stable between
	// rebuilds. A selected edge must not become a different edge because an
	// unrelated face was touched.
	sort.Slice(a.edges, func(i, j int) bool {
		if a.edges[i].A != a.edges[j].A {
			return a.edges[i].A < a.edges[j].A
		}
		return a.edges[i].B < a.edges[j].B
	})
	m.adj = a
	return a
}

// Edges returns the mesh's unique edges in a stable order.
func (m *ModelDoc) Edges() []EdgeKey { return m.adjacency().edges }

// FacesAtVertex returns the indices of the faces touching vertex vi.
func (m *ModelDoc) FacesAtVertex(vi int) []int { return m.adjacency().vertFaces[vi] }

// FacesAtEdge returns the indices of the faces sharing edge e — one for a
// boundary edge, two for an interior one.
func (m *ModelDoc) FacesAtEdge(e EdgeKey) []int { return m.adjacency().edgeFaces[e] }

// ---------- geometry queries ----------

// faceNormal returns the face's unit normal using Newell's method.
//
// Newell rather than a single cross product of the first three vertices: an
// n-gon that has been dragged out of plane (which the editor allows) has no one
// true normal, and the cross product would pick whichever one the first corner
// happened to imply. Newell's area-weighted sum gives the best-fit plane
// instead, which is both stable under vertex reordering and what the
// triangulation below wants to project onto.
func (m *ModelDoc) faceNormal(f *ModelFace) mgl32.Vec3 {
	pts := make([]mgl32.Vec3, len(f.Verts))
	for i, vi := range f.Verts {
		pts[i] = m.Verts[vi]
	}
	return geom.PolygonNormal3D(pts)
}

// FaceNormal returns the unit normal of face fi.
func (m *ModelDoc) FaceNormal(fi int) mgl32.Vec3 {
	if fi < 0 || fi >= len(m.Faces) {
		return mgl32.Vec3{}
	}
	return m.faceNormal(&m.Faces[fi])
}

// FaceCenter returns the centroid of face fi's vertices.
func (m *ModelDoc) FaceCenter(fi int) mgl32.Vec3 {
	if fi < 0 || fi >= len(m.Faces) {
		return mgl32.Vec3{}
	}
	return m.faceCentroid(&m.Faces[fi])
}

func (m *ModelDoc) faceCentroid(f *ModelFace) mgl32.Vec3 {
	var c mgl32.Vec3
	if len(f.Verts) == 0 {
		return c
	}
	for _, v := range f.Verts {
		c = c.Add(m.Verts[v])
	}
	return c.Mul(1 / float32(len(f.Verts)))
}

func faceUVCentroid(f *ModelFace) mgl32.Vec2 {
	var c mgl32.Vec2
	if len(f.UVs) == 0 {
		return c
	}
	for _, uv := range f.UVs {
		c = c.Add(uv)
	}
	return c.Mul(1 / float32(len(f.UVs)))
}

// faceTriangles triangulates face fi, returning triples of *face-local* indices
// (0..len(f.Verts)-1), so callers can look up both the position and the UV of
// each corner.
//
// The flattening and clipping is geom.TriangulatePolygon3D, shared with a
// level surface's SurfaceMesh so the two mesh editors cut an n-gon the same
// way; this wrapper is only the lookup from face-local indices into the
// document's vertex pool.
func (m *ModelDoc) FaceTriangles(fi int) [][3]int {
	if fi < 0 || fi >= len(m.Faces) {
		return nil
	}
	f := &m.Faces[fi]
	pts := make([]mgl32.Vec3, len(f.Verts))
	for i, vi := range f.Verts {
		pts[i] = m.Verts[vi]
	}
	return geom.TriangulatePolygon3D(pts)
}

// ---------- compiling to GPU buffers ----------

// modelSubmesh is the mesh in the [][]float32 + []int shape Geometry.Load
// expects. A document compiles to exactly one of these — see modelSubmeshes —
// but the slice-returning shape is kept so callers stay indifferent to that.
type modelSubmesh struct {
	Texture  string
	Vertices [][]float32 // each [X, Y, Z, U, V]
	Indices  []int
	Smooth   bool // true when any face asked for smoothing
	// Skinning attributes, index-parallel to Vertices: four bone indices and
	// four weights per record, padded with zero weight. Populated only when the
	// document has an armature, so a static model uploads nothing extra.
	SkinIdx, SkinWt [][]float32
	// SrcVerts maps each record back to the document vertex it came from — the
	// fan-out below, made available to callers instead of re-derived.
	SrcVerts []int
}

// modelSubmeshes triangulates every face into a single submesh.
//
// It used to group faces by their per-face texture, because Geometry binds one
// texture per draw call and a mesh with four materials therefore needed four
// Geometry. A document now wears one texture (ModelDoc.Texture), so there is
// one group and one draw call, and an empty mesh compiles to nothing at all.
//
// Normals are not computed here: Smooth rides on the submesh and Geometry.Load
// runs ComputeSmoothNormals itself, welding across the whole submesh, which is
// what makes a subdivided sphere shade as one surface.
func (m *ModelDoc) ModelSubmeshes() []modelSubmesh {
	return m.ModelSubmeshesFrom(m.Verts)
}

// modelSubmeshesFrom compiles the mesh using a caller-supplied vertex array
// rather than the document's own.
//
// The model editor's pose mode passes its CPU-skinned positions here: the
// document goes on holding the rest mesh — so save, undo and every mesh op stay
// untouched by posing — while what reaches the GPU is the deformed shape.
func (m *ModelDoc) ModelSubmeshesFrom(verts []mgl32.Vec3) []modelSubmesh {
	g := &modelSubmesh{Texture: m.Texture}
	skinned := len(m.Bones) > 0 && m.HasSkin()

	for fi := range m.Faces {
		f := &m.Faces[fi]
		tris := m.FaceTriangles(fi)
		if len(tris) == 0 {
			continue
		}
		if f.Smooth {
			g.Smooth = true
		}

		// Each face contributes its own vertex records. Sharing them between
		// faces would merge UV seams that the user set deliberately; the
		// position-welding in ComputeSmoothNormals re-joins them for shading.
		base := len(g.Vertices)
		for i, vi := range f.Verts {
			var p mgl32.Vec3
			if vi >= 0 && vi < len(verts) {
				p = verts[vi]
			}
			var uv mgl32.Vec2
			if i < len(f.UVs) {
				uv = f.UVs[i]
			}
			g.Vertices = append(g.Vertices, []float32{p.X(), p.Y(), p.Z(), uv.X(), uv.Y()})
			g.SrcVerts = append(g.SrcVerts, vi)
			if skinned {
				idx, wt := SkinAttrsFor(m.WeightsFor(vi))
				g.SkinIdx = append(g.SkinIdx, idx)
				g.SkinWt = append(g.SkinWt, wt)
			}
		}
		for _, t := range tris {
			g.Indices = append(g.Indices, base+t[0], base+t[1], base+t[2])
		}
	}

	if len(g.Indices) == 0 {
		return nil
	}
	return []modelSubmesh{*g}
}

// SkinAttrsFor packs one vertex's influences into the fixed vec4 pair the
// shader reads, padding with zero weight. An unbound vertex gets all-zero
// weights, which skinMatrix (skin_common.glsl) resolves to the identity — so
// the unrigged parts of a partly-bound model stay where they are, exactly as
// they do on the CPU path.
func SkinAttrsFor(ws []ModelVertexWeight) (idx, wt []float32) {
	idx = make([]float32, ModelMaxInfluences)
	wt = make([]float32, ModelMaxInfluences)
	for i, w := range ws {
		if i >= ModelMaxInfluences {
			break
		}
		idx[i] = float32(w.Bone)
		wt[i] = w.Weight
	}
	return idx, wt
}

// Bounds returns the mesh's axis-aligned bounds. Returns zero vectors for an
// empty mesh.
func (m *ModelDoc) Bounds() (min, max mgl32.Vec3) {
	if len(m.Verts) == 0 {
		return
	}
	min, max = m.Verts[0], m.Verts[0]
	for _, p := range m.Verts[1:] {
		for i := 0; i < 3; i++ {
			if p[i] < min[i] {
				min[i] = p[i]
			}
			if p[i] > max[i] {
				max[i] = p[i]
			}
		}
	}
	return
}
