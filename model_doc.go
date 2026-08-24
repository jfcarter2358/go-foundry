package main

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
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// ModelFace is one CCW polygon of the mesh. Verts indexes ModelDoc.Verts; UVs
// is index-parallel to Verts, so UVs[i] belongs to Verts[i].
type ModelFace struct {
	Verts   []int
	UVs     []mgl32.Vec2
	Texture string // logical texture name — same namespace as Wall.Texture
	Smooth  bool
}

// clone returns a deep copy; the slices must not be shared with the original or
// an edit to one face would silently rewrite another.
func (f ModelFace) clone() ModelFace {
	c := f
	c.Verts = append([]int(nil), f.Verts...)
	c.UVs = append([]mgl32.Vec2(nil), f.UVs...)
	return c
}

// ModelDoc is the mesh being edited, together with the armature that deforms
// it. Bones and Weights are described in model_bone.go and model_skin.go; they
// live on the document rather than in a file of their own because a .fmdl is
// the one thing the level places, and a skeleton kept somewhere else would be a
// second file the user has to remember to keep in step.
type ModelDoc struct {
	ID    string
	Verts []mgl32.Vec3
	Faces []ModelFace
	Bones []ModelBone
	// Weights is index-parallel to Verts: Weights[i] is what moves Verts[i].
	// A nil or empty entry is an unbound vertex, which no bone moves.
	Weights [][]ModelVertexWeight
	// Animations are the keyframed clips that drive Bones (model_anim.go). They
	// live here rather than in a sidecar file for the same reason the armature
	// does: a .fmdl is the one thing the level places, and motion kept elsewhere
	// would be a second file to keep in step.
	Animations  []ModelClip
	SmoothAngle float32 // 0 = defaultSmoothAngle (smooth.go)
	Path        string  // on-disk .fmdl path; empty until first Save As

	adj   *modelAdjacency // derived; nil when stale
	dirty bool            // geometry needs recompiling for the GPU
	// rev counts edits. Derived data that is expensive to rebuild — the
	// editor's visibility pass — compares it to know whether the mesh it was
	// computed from is still the mesh on screen.
	rev uint64
}

// Rev returns the document's edit counter.
func (m *ModelDoc) Rev() uint64 { return m.rev }

// Clone returns a deep copy of the document. Used by the undo snapshot path and
// by any operation that wants to try an edit before committing it.
func (m *ModelDoc) Clone() *ModelDoc {
	c := &ModelDoc{
		ID:          m.ID,
		Verts:       append([]mgl32.Vec3(nil), m.Verts...),
		Faces:       make([]ModelFace, len(m.Faces)),
		Bones:       cloneBones(m.Bones),
		Weights:     cloneWeights(m.Weights),
		Animations:  cloneClips(m.Animations),
		SmoothAngle: m.SmoothAngle,
		Path:        m.Path,
	}
	for i := range m.Faces {
		c.Faces[i] = m.Faces[i].clone()
	}
	return c
}

// touch marks the document as needing a GPU rebuild. Topology edits must use
// invalidate instead, which also drops the cached adjacency.
func (m *ModelDoc) touch() {
	m.dirty = true
	m.rev++
}

// invalidate marks the document dirty *and* discards the derived adjacency
// index. Anything that adds, removes or rewires a face or vertex must call this
// — a stale adjacency map would hand out indices that no longer exist.
func (m *ModelDoc) invalidate() {
	m.touch()
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
			Verts:   []int{q[0], q[1], q[2], q[3]},
			UVs:     append([]mgl32.Vec2(nil), unitUV...),
			Texture: "default",
		})
	}
	m.invalidate()
	return m
}

// ---------- topology ----------

// edgeKey identifies an undirected edge. A is always the smaller index, so the
// same edge reached from either of its two faces produces the same key.
type edgeKey struct{ A, B int }

func makeEdgeKey(a, b int) edgeKey {
	if a > b {
		a, b = b, a
	}
	return edgeKey{a, b}
}

// modelAdjacency is the derived "what touches what" index. A half-edge
// structure would answer the same questions, but at editor mesh sizes rebuilding
// two maps after each edit is cheaper than maintaining one incrementally — and
// far harder to get subtly wrong.
type modelAdjacency struct {
	vertFaces map[int][]int     // vertex index -> face indices touching it
	edgeFaces map[edgeKey][]int // edge -> face indices; len 1 = boundary, 2 = interior
	edges     []edgeKey         // deterministic order, for edge selection/picking
}

// adjacency returns the topology index, rebuilding it if a topology edit has
// invalidated the cache.
func (m *ModelDoc) adjacency() *modelAdjacency {
	if m.adj != nil {
		return m.adj
	}
	a := &modelAdjacency{
		vertFaces: make(map[int][]int, len(m.Verts)),
		edgeFaces: make(map[edgeKey][]int, len(m.Faces)*4),
	}
	for fi := range m.Faces {
		f := &m.Faces[fi]
		n := len(f.Verts)
		for i, v := range f.Verts {
			a.vertFaces[v] = append(a.vertFaces[v], fi)
			e := makeEdgeKey(v, f.Verts[(i+1)%n])
			a.edgeFaces[e] = append(a.edgeFaces[e], fi)
		}
	}
	a.edges = make([]edgeKey, 0, len(a.edgeFaces))
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
func (m *ModelDoc) Edges() []edgeKey { return m.adjacency().edges }

// FacesAtVertex returns the indices of the faces touching vertex vi.
func (m *ModelDoc) FacesAtVertex(vi int) []int { return m.adjacency().vertFaces[vi] }

// FacesAtEdge returns the indices of the faces sharing edge e — one for a
// boundary edge, two for an interior one.
func (m *ModelDoc) FacesAtEdge(e edgeKey) []int { return m.adjacency().edgeFaces[e] }

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
	var n mgl32.Vec3
	cnt := len(f.Verts)
	if cnt < 3 {
		return n
	}
	for i := 0; i < cnt; i++ {
		a := m.Verts[f.Verts[i]]
		b := m.Verts[f.Verts[(i+1)%cnt]]
		n = n.Add(mgl32.Vec3{
			(a.Y() - b.Y()) * (a.Z() + b.Z()),
			(a.Z() - b.Z()) * (a.X() + b.X()),
			(a.X() - b.X()) * (a.Y() + b.Y()),
		})
	}
	if n.Len() < 1e-12 {
		return mgl32.Vec3{}
	}
	return n.Normalize()
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
// triangulate.go's earClip is a 2D routine, so the face is projected onto its
// own best-fit plane first: a basis (u, v) perpendicular to the Newell normal,
// with perpAxis (detail_render.go) picking a stable first axis. This is the
// same trick computeFaceBasis uses for procedural detail, but on indexed data
// rather than a flat vertex buffer.
func (m *ModelDoc) faceTriangles(fi int) [][3]int {
	if fi < 0 || fi >= len(m.Faces) {
		return nil
	}
	f := &m.Faces[fi]
	n := len(f.Verts)
	if n < 3 {
		return nil
	}
	if n == 3 {
		return [][3]int{{0, 1, 2}}
	}

	nrm := m.faceNormal(f)
	if nrm.Len() < 0.5 {
		// Fully degenerate face (all vertices collinear or coincident). A fan
		// keeps the triangle count right without inventing a plane.
		return fanTriangles(n)
	}
	u := perpAxis(nrm)
	if u.Len() < 1e-6 {
		return fanTriangles(n)
	}
	u = u.Normalize()
	v := nrm.Cross(u).Normalize()

	origin := m.Verts[f.Verts[0]]
	poly := make([]mgl32.Vec2, n)
	for i, vi := range f.Verts {
		d := m.Verts[vi].Sub(origin)
		poly[i] = mgl32.Vec2{d.Dot(u), d.Dot(v)}
	}

	flat := earClip(poly)
	if len(flat) < 3 {
		return fanTriangles(n)
	}
	tris := make([][3]int, 0, len(flat)/3)
	for i := 0; i+2 < len(flat); i += 3 {
		tris = append(tris, [3]int{flat[i], flat[i+1], flat[i+2]})
	}
	return tris
}

// fanTriangles is the fallback triangulation for a face whose plane could not be
// determined. It never fails, and a degenerate face has zero area anyway.
func fanTriangles(n int) [][3]int {
	tris := make([][3]int, 0, n-2)
	for i := 1; i < n-1; i++ {
		tris = append(tris, [3]int{0, i, i + 1})
	}
	return tris
}

// ---------- compiling to GPU buffers ----------

// modelSubmesh is one texture's worth of the mesh, in the [][]float32 +
// []int shape Geometry.Load expects.
type modelSubmesh struct {
	Texture  string
	Vertices [][]float32 // each [X, Y, Z, U, V]
	Indices  []int
	Smooth   bool // true when any face in the group asked for smoothing
	// Skinning attributes, index-parallel to Vertices: four bone indices and
	// four weights per record, padded with zero weight. Populated only when the
	// document has an armature, so a static model uploads nothing extra.
	SkinIdx, SkinWt [][]float32
	// SrcVerts maps each record back to the document vertex it came from — the
	// fan-out below, made available to callers instead of re-derived.
	SrcVerts []int
}

// modelSubmeshes triangulates every face and groups the result by texture.
//
// One Geometry per texture is forced by Geometry itself, which binds a single
// texture per draw call. The repo already does exactly this wherever a surface
// needs a second texture — see ExtrusionBackFace and loadExtrusionGeometries
// (sector.go) — so a model with four materials is four Geometry, no differently
// from a wall with a custom-textured extrusion cap.
//
// Normals are not computed here: Smooth rides on the submesh and Geometry.Load
// runs computeSmoothNormals itself, welding across the whole submesh, which is
// what makes a subdivided sphere shade as one surface.
func (m *ModelDoc) modelSubmeshes() []modelSubmesh {
	return m.modelSubmeshesFrom(m.Verts)
}

// modelSubmeshesFrom compiles the mesh using a caller-supplied vertex array
// rather than the document's own.
//
// The model editor's pose mode passes its CPU-skinned positions here: the
// document goes on holding the rest mesh — so save, undo and every mesh op stay
// untouched by posing — while what reaches the GPU is the deformed shape.
func (m *ModelDoc) modelSubmeshesFrom(verts []mgl32.Vec3) []modelSubmesh {
	groups := map[string]*modelSubmesh{}
	var order []string
	skinned := len(m.Bones) > 0 && m.hasSkin()

	for fi := range m.Faces {
		f := &m.Faces[fi]
		tris := m.faceTriangles(fi)
		if len(tris) == 0 {
			continue
		}
		tex := f.Texture
		if tex == "" {
			tex = "default"
		}
		g, ok := groups[tex]
		if !ok {
			g = &modelSubmesh{Texture: tex}
			groups[tex] = g
			order = append(order, tex)
		}
		if f.Smooth {
			g.Smooth = true
		}

		// Each face contributes its own vertex records. Sharing them between
		// faces would merge UV seams that the user set deliberately; the
		// position-welding in computeSmoothNormals re-joins them for shading.
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
				idx, wt := skinAttrsFor(m.weightsFor(vi))
				g.SkinIdx = append(g.SkinIdx, idx)
				g.SkinWt = append(g.SkinWt, wt)
			}
		}
		for _, t := range tris {
			g.Indices = append(g.Indices, base+t[0], base+t[1], base+t[2])
		}
	}

	out := make([]modelSubmesh, 0, len(order))
	for _, tex := range order {
		out = append(out, *groups[tex])
	}
	return out
}

// skinAttrsFor packs one vertex's influences into the fixed vec4 pair the
// shader reads, padding with zero weight. An unbound vertex gets all-zero
// weights, which skinMatrix (skin_common.glsl) resolves to the identity — so
// the unrigged parts of a partly-bound model stay where they are, exactly as
// they do on the CPU path.
func skinAttrsFor(ws []ModelVertexWeight) (idx, wt []float32) {
	idx = make([]float32, modelMaxInfluences)
	wt = make([]float32, modelMaxInfluences)
	for i, w := range ws {
		if i >= modelMaxInfluences {
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
