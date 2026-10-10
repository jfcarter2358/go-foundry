package editor

// editor_mesh_topology.go — join, dissolve and merge, on a brush's meshes.
//
//	J        join the selected faces into one
//	X        dissolve the selected points — each one that only bends a line is
//	         taken out of it, so the two segments either side become one
//	M        merge the selected points into one, at their centre
//	Shift+M  the same, at the first point selected
//
// Like subdivide, these are the model editor's operations run through the
// bridge (editor_detail_mesh.go), not a second implementation: the level
// editor, the entity editor and the model editor all end in model.ModelDoc's
// JoinFaces, DissolveVerts and MergeVerts. And like subdivide they are offered
// in edit mode and detail mode alike, because none of them adds a surface a
// solid cannot have:
//
//   - A join is refused unless the outline is a brush face — flat and convex,
//     which BrushFace promises and BuildBrushFaceMesh's fan relies on. On a
//     hull, faces that pass lie in one plane of the solid already, so the solid
//     is exactly what it was and only its face list is shorter.
//   - A dissolve takes a point out of a line. On a hull every such point lies
//     on the edge between two of its planes, so the solid does not change.
//   - A merge can change the solid — pulling two corners of a box together is
//     a wedge — and it is warned about rather than refused, on the same terms
//     as dragging a corner (warnIfSelectionBrokeConvexity).
//
// # One undo entry, only when something happens
//
// Each operation is worked out on bridged copies of every mesh it touches
// first, and committed only if at least one of them changed. The detail tools
// push and then pop, but popping is the level editor's stack whichever window
// is asking; planning first means the entity editor never needs a pop.

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	// brushOutlineFlatness is how far, as a fraction of its size, a corner of a
	// joined outline may sit off the outline's own plane and still be a flat
	// face. Loose enough for float drift across a subdivide and a drag.
	brushOutlineFlatness float32 = 1e-3
	// brushOutlineBend is the sine of the smallest inward turn that makes an
	// outline concave. Straight runs — the midpoints a join leaves on its edges
	// — turn by nothing and pass.
	brushOutlineBend float32 = 1e-3
)

// ---------- the keys ----------

// meshTopologyHandleKey runs join, dissolve and merge. Asked ahead of the detail
// and hull tools, and claims a key only when there is something of the right
// kind selected: X with no points selected is still detail mode's delete.
func meshTopologyHandleKey(sc sdl.Scancode, shift bool) bool {
	if !ws().editing() || !editorInMeshContext() {
		return false
	}
	switch sc {
	case sdl.SCANCODE_J:
		if len(ws().SelectedFaces) == 0 {
			return false
		}
		meshJoinSelectedFaces()
		return true
	case sdl.SCANCODE_M:
		if len(ws().SelectedVertices) == 0 {
			return false
		}
		meshMergeSelectedPoints(shift)
		return true
	case sdl.SCANCODE_X:
		if len(ws().SelectedVertices) == 0 {
			return false
		}
		meshDissolveSelectedPoints()
		return true
	}
	return false
}

// ---------- planning and committing ----------

// meshKey names one of a brush's two meshes.
type meshKey struct {
	chunkIdx, brushIdx int
	detail             bool
}

// meshPlan is one mesh's edit, worked out on a bridge and not yet written back.
type meshPlan struct {
	key  meshKey
	ch   *world.Chunk
	b    *world.Brush
	mesh *world.BrushMesh
	br   *meshBridge
	// sel is what to select once it is written: face indices for a join,
	// vertex indices for a dissolve or a merge, all into the edited mesh.
	sel []int
}

// resolveMeshKey is faceSelBrush and selMesh in one step.
func resolveMeshKey(k meshKey) (*world.Chunk, *world.Brush, *world.BrushMesh) {
	ch, b := faceSelBrush(faceSel{chunkIdx: k.chunkIdx, brushIdx: k.brushIdx})
	m := selMesh(b, k.detail)
	if ch == nil || m == nil {
		return nil, nil, nil
	}
	return ch, b, m
}

// facesByMesh groups a face selection by the mesh each face belongs to, in the
// order the meshes first appear, keeping each mesh's faces in selection order.
func facesByMesh(sels []faceSel) ([]meshKey, map[meshKey][]int) {
	var keys []meshKey
	groups := map[meshKey][]int{}
	for _, s := range sels {
		k := meshKey{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx, detail: s.detail}
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], s.faceIdx)
	}
	return keys, groups
}

// vertsByMesh is facesByMesh for a point selection.
func vertsByMesh(sels []vertexSel) ([]meshKey, map[meshKey][]int) {
	var keys []meshKey
	groups := map[meshKey][]int{}
	for _, s := range sels {
		k := meshKey{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx, detail: s.detail}
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], s.vertIdx)
	}
	return keys, groups
}

// remapAttrs carries each face's attributes to wherever a topology edit put
// that face — a joined face wears the first face listed for it.
func (br *meshBridge) remapAttrs(faceSrc []int) {
	attrs := make([]world.BrushFace, len(faceSrc))
	for i, s := range faceSrc {
		attrs[i] = br.attrs[s]
	}
	br.attrs = attrs
}

// commitMeshPlans writes every plan back under one undo entry, reloads the
// brushes, and re-buckets the chunks whose bounds may have moved.
func commitMeshPlans(plans []*meshPlan) {
	ws().pushUndo()
	chunks := map[*world.Chunk]bool{}
	for _, p := range plans {
		if !p.br.writeBack(p.mesh) {
			continue
		}
		if err := p.b.Load(); err != nil {
			ws().setStatus("%s: %v", p.b.ID, err)
		}
		chunks[p.ch] = true
	}
	for ch := range chunks {
		ch.RecomputeBounds()
	}
	world.RebuildSpatialHash(ws().level)
}

// planned drops the plans whose bridge no longer agrees with itself about how
// many faces there are — writeBack would refuse them, and a refusal after the
// undo entry was pushed would leave an entry that undoes nothing.
func planned(p *meshPlan) bool { return len(p.br.attrs) == len(p.br.doc.Faces) }

// ---------- join ----------

// meshJoinSelectedFaces joins the selected faces of each brush into as few
// faces as their outlines allow, and selects what came out.
func meshJoinSelectedFaces() {
	keys, groups := facesByMesh(ws().SelectedFaces)
	var plans []*meshPlan
	refused, single := 0, 0
	for _, k := range keys {
		ch, b, mesh := resolveMeshKey(k)
		if mesh == nil {
			continue
		}
		if len(groups[k]) < 2 {
			single++
			continue
		}
		br := bridgeMesh(mesh)
		joined, r, edit := br.doc.JoinFaces(groups[k], brushFaceOutline)
		refused += r
		if !edit.Changed() {
			continue
		}
		br.remapAttrs(edit.FaceSrc)
		if p := (&meshPlan{key: k, ch: ch, b: b, mesh: mesh, br: br, sel: joined}); planned(p) {
			plans = append(plans, p)
		}
	}

	if len(plans) == 0 {
		switch {
		case refused > 0:
			ws().setStatus("Cannot join: the faces must share edges, make one outline with no hole in it, " +
				"and together be a flat, convex face")
		case single > 1:
			ws().setStatus("Cannot join faces of different brushes — each brush is a solid of its own")
		default:
			ws().setStatus("Select two or more neighbouring faces to join")
		}
		return
	}

	commitMeshPlans(plans)
	var sels []faceSel
	for _, p := range plans {
		for _, fi := range p.sel {
			sels = append(sels, faceSel{chunkIdx: p.key.chunkIdx, brushIdx: p.key.brushIdx,
				faceIdx: fi, detail: p.key.detail})
		}
	}
	setFaceSelection(sels)
	ws().PanelFocus = panelFocusFace
	if refused > 0 {
		ws().setStatus("Joined into %d face(s); %d group(s) could not be — not one flat, convex outline",
			len(sels), refused)
		return
	}
	ws().setStatus("Joined into %d face(s)", len(sels))
}

// brushFaceOutline reports whether a loop of points can be a brush face: flat,
// and convex once straight runs are allowed for. It is the accept test a join
// on a brush passes to JoinFaces.
func brushFaceOutline(pts []mgl32.Vec3) bool {
	n := geom.PolygonNormal3D(pts)
	if n == (mgl32.Vec3{}) {
		return false
	}
	var c mgl32.Vec3
	for _, p := range pts {
		c = c.Add(p)
	}
	c = c.Mul(1 / float32(len(pts)))
	size := float32(1)
	for _, p := range pts {
		if d := p.Sub(c).Len(); d > size {
			size = d
		}
	}
	tol := brushOutlineFlatness * size
	for _, p := range pts {
		if float32(math.Abs(float64(n.Dot(p.Sub(c))))) > tol {
			return false
		}
	}
	for i := range pts {
		a, b, d := pts[(i+len(pts)-1)%len(pts)], pts[i], pts[(i+1)%len(pts)]
		in, out := b.Sub(a), d.Sub(b)
		li, lo := in.Len(), out.Len()
		if li < tol || lo < tol {
			continue // a corner on top of its neighbour turns nothing
		}
		if n.Dot(in.Cross(out))/(li*lo) < -brushOutlineBend {
			return false
		}
	}
	return true
}

// ---------- dissolve ----------

// meshDissolveSelectedPoints dissolves every selected point that lies on a
// line, and leaves whatever else was selected still selected.
//
// In edge mode the selection is a run of edges, and what is dissolved is the
// points the run passes THROUGH — both of whose edges are selected — so the
// run's two ends stay put and it becomes one edge between them.
func meshDissolveSelectedPoints() {
	edges := ws().SelMode == editorSelEdge
	keys, groups := vertsByMesh(ws().SelectedVertices)
	var plans []*meshPlan
	asked, done := 0, 0
	for _, k := range keys {
		ch, b, mesh := resolveMeshKey(k)
		if mesh == nil {
			continue
		}
		br := bridgeMesh(mesh)
		verts := groups[k]
		if edges {
			verts = chainInterior(br.doc, verts)
		}
		asked += len(verts)
		dissolved, edit := br.doc.DissolveVerts(verts)
		if !edit.Changed() {
			continue
		}
		br.remapAttrs(edit.FaceSrc)
		var keep []int
		for _, v := range groups[k] {
			if nv := edit.Vert(v); nv >= 0 {
				keep = append(keep, nv)
			}
		}
		if p := (&meshPlan{key: k, ch: ch, b: b, mesh: mesh, br: br, sel: keep}); planned(p) {
			plans = append(plans, p)
			done += len(dissolved)
		}
	}

	if len(plans) == 0 {
		if edges {
			ws().setStatus("Nothing to dissolve: select a run of edges, and the points along it " +
				"where only two edges meet are taken out")
			return
		}
		ws().setStatus("Nothing to dissolve: a point can be dissolved only where two edges meet — " +
			"a point along a line, not a corner")
		return
	}

	commitMeshPlans(plans)
	var sels []vertexSel
	var hulls []*world.Brush
	for _, p := range plans {
		for _, v := range p.sel {
			sels = append(sels, vertexSel{chunkIdx: p.key.chunkIdx, brushIdx: p.key.brushIdx,
				vertIdx: v, detail: p.key.detail})
		}
		if !p.key.detail {
			hulls = append(hulls, p.b)
		}
	}
	if len(sels) > 0 {
		setVertexSelection(sels)
	} else {
		clearVertexSelection()
	}
	if skipped := asked - done; skipped > 0 {
		ws().setStatus("Dissolved %d point(s); %d are corners, where more than two edges meet, and stay", done, skipped)
	} else {
		ws().setStatus("Dissolved %d point(s)", done)
	}
	warnIfBrushesNotConvex(hulls)
}

// chainInterior keeps the points a selected run of edges passes through: the
// ones on a line whose neighbours on both sides are selected too.
func chainInterior(doc *model.ModelDoc, verts []int) []int {
	in := make(map[int]bool, len(verts))
	for _, v := range verts {
		in[v] = true
	}
	var out []int
	for _, v := range verts {
		if a, b, ok := doc.LineNeighbours(v); ok && in[a] && in[b] {
			out = append(out, v)
		}
	}
	return out
}

// ---------- merge ----------

// meshMergeSelectedPoints collapses the selected points into one, at their
// centre or at the first one selected.
//
// Points on different brushes cannot become one point — each brush owns its
// own — so each brush's share of the selection becomes one point of its own,
// and all of them land on the same spot. Selecting a corner on each of two
// brushes and merging is how two solids are made to meet exactly.
func meshMergeSelectedPoints(atFirst bool) {
	sels := ws().SelectedVertices
	if len(sels) < 2 {
		ws().setStatus("Select two or more points to merge")
		return
	}
	target, ok := selectedVerticesCenter()
	if atFirst {
		target, ok = vertexSelPos(sels[0])
	}
	if !ok {
		return
	}

	keys, groups := vertsByMesh(sels)
	var plans []*meshPlan
	merged := 0
	for _, k := range keys {
		ch, b, mesh := resolveMeshKey(k)
		if mesh == nil {
			continue
		}
		br := bridgeMesh(mesh)
		at := ch.WorldToLocal(target)
		verts := groups[k]
		kept := verts[0]
		if len(verts) == 1 {
			// One point of this brush: nothing to collapse, only somewhere to be.
			if kept < 0 || kept >= len(br.doc.Verts) {
				continue
			}
			br.doc.Verts[kept] = at
		} else {
			var edit model.TopologyEdit
			kept, edit = br.doc.MergeVerts(verts, at)
			if kept < 0 {
				continue
			}
			br.remapAttrs(edit.FaceSrc)
			merged += len(verts)
		}
		if p := (&meshPlan{key: k, ch: ch, b: b, mesh: mesh, br: br, sel: []int{kept}}); planned(p) {
			plans = append(plans, p)
		}
	}
	if len(plans) == 0 {
		ws().setStatus("Nothing to merge")
		return
	}

	commitMeshPlans(plans)
	out := make([]vertexSel, 0, len(plans))
	for _, p := range plans {
		out = append(out, vertexSel{chunkIdx: p.key.chunkIdx, brushIdx: p.key.brushIdx,
			vertIdx: p.sel[0], detail: p.key.detail})
	}
	setVertexSelection(out)
	switch {
	case merged == 0:
		ws().setStatus("Moved %d point(s) on different brushes together", len(out))
	case len(out) == 1:
		ws().setStatus("Merged %d points into one", merged)
	default:
		ws().setStatus("Merged %d points into %d, one per brush, in the same place", len(sels), len(out))
	}
	warnIfSelectionBrokeConvexity()
}
