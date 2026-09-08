package editor

// editor_face_gizmo.go — the gizmo a selected face gets.
//
// Points and edges have had one since they became selections in their own
// right: an edge selection *is* the selection of its two endpoints
// (editor_3d_edge_pick.go), so the move, rotate and scale tools drag an edge
// without ever knowing edges exist. Faces were the one unit left out. Picking a
// wall in face mode opened its panel and drew its outline, the toolbar still
// showed G/R/C as the tool in your hand, and nothing appeared in the viewport
// to grab — the face could only be moved by switching to point mode and
// hunting down its corners one at a time.
//
// A face gets the same deal the edge got, and for the same reason: the corners
// it is built from *are* the thing that moves. This file is the translation —
// a face selection to the vertex handles underneath it — and everything that
// already knew how to drag a set of handles is reused untouched.
//
// # Which handles a face owns
//
//   - A floor is every corner's floor handle; a ceiling, every corner's ceiling
//     handle. Dragging one in Y is the same edit as raising each of its corners.
//   - A wall or a portal is the four handles of its two corners — floor and
//     ceiling at each end — because a wall spans the two, and moving only one
//     pair would tilt it rather than move it.
//
// The handles are not a selection: Editor.SelectedVertices stays empty, so the
// right-click menu still offers the face's actions rather than a vertex's, and
// the vertex panel stays shut. They are derived on demand, which also means a
// rebuild between two motion events cannot leave them stale.
//
// # Shared corners are written once
//
// A wall's two handles at one corner are two selections of a single chunk
// point, and two faces of the same sector share corners outright. The write
// paths already reconcile that for world-space transforms (they average the XZ
// per point — see editor_sel_transform.go), and setSelectedVertexPoints now
// skips a point it has already written, so the biome layer riding those points
// is carried along once per drag rather than once per handle that named it.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// gizmoVertexSels is the vertex handles the gizmo's drags act on.
//
// Every write path below the gizmos (the two setSelectedVertex* functions and
// writeSelectedVertexWorld) goes through here rather than reading
// Editor.SelectedVertices directly, which is the whole of what makes a face
// draggable: with anything but a face selected it is that list, unchanged.
//
// The 2D map is deliberately not included — gizmoEffectiveKind is 3D-only, so
// the top-down view keeps working in the unit it has always worked in.
func gizmoVertexSels() []vertexSel {
	if gizmoEffectiveKind() == gizmoKindFace {
		return selectedFaceVertices()
	}
	return Editor.SelectedVertices
}

// selectedFaceVertices is every vertex handle the selected faces are built
// from, in sector-ring order and without duplicates — two faces of one sector
// share corners, and a handle written twice would move a shared chunk point
// twice.
func selectedFaceVertices() []vertexSel {
	var out []vertexSel
	seen := map[vertexSel]bool{}
	for _, f := range Editor.SelectedFaces {
		for _, sel := range faceVertexSels(f) {
			if seen[sel] {
				continue
			}
			seen[sel] = true
			out = append(out, sel)
		}
	}
	return out
}

// faceVertexSels is one face's handles. Empty when the face no longer names
// live geometry, which prunes a selection left behind by an edit rather than
// dragging something that is not there.
func faceVertexSels(f faceSel) []vertexSel {
	ch, s := faceSelSector(f)
	if ch == nil || s == nil {
		return nil
	}
	n := len(s.PointIndices)
	if n == 0 {
		return nil
	}
	at := func(vi int, isFloor bool) (vertexSel, bool) {
		if vi < 0 || vi >= n {
			return vertexSel{}, false
		}
		pIdx := s.PointIndices[vi]
		if pIdx < 0 || pIdx >= len(ch.Points) {
			return vertexSel{}, false
		}
		return vertexSel{
			chunkIdx:  f.chunkIdx,
			sectorIdx: f.sectorIdx,
			vertIdx:   vi,
			pointIdx:  pIdx,
			isFloor:   isFloor,
		}, true
	}

	var out []vertexSel
	add := func(vi int, isFloor bool) {
		if sel, ok := at(vi, isFloor); ok {
			out = append(out, sel)
		}
	}

	switch f.kind {
	// A slab face resolves to the handles of the surface it hangs off, so
	// dragging the underside of a floor moves the floor and takes the slab with
	// it — which is what grabbing a slab and pulling should do. How thick it is
	// is a number in the panel, not a corner in the viewport: a slab has no
	// corners of its own, its far face being an offset copy of the surface's.
	case "floor", "ceiling", "floor_slab", "ceiling_slab":
		isFloor := f.kind == "floor" || f.kind == "floor_slab"
		for vi := 0; vi < n; vi++ {
			add(vi, isFloor)
		}
	case "wall", "portal":
		ei := faceEdgeRingIndex(s, f.kind, f.idx)
		if ei < 0 {
			return nil
		}
		// Both ends, both heights: the quad the outline draws.
		for _, vi := range [2]int{ei, (ei + 1) % n} {
			add(vi, true)
			add(vi, false)
		}
	}
	return out
}

// faceEdgeRingIndex maps a wall or portal index to its position on the
// sector's polygon ring.
//
// Walls and Portals are two separately-compacted slices whose entries run in
// ring order (rebuildSectorEdgePoints fills them by walking EdgeKinds), so the
// n-th wall is the n-th "wall" entry in EdgeKinds. Counting them is the same
// walk portalEdgePosition already does for portals; this is both kinds, and it
// is used rather than matching an edge's stored StartPoint against the sector's
// points because a drag moves those points between events and two corners that
// pass through each other would match the wrong one.
func faceEdgeRingIndex(s *world.Sector, kind string, idx int) int {
	if idx < 0 {
		return -1
	}
	count := 0
	for ei := range s.PointIndices {
		// A ring position past the end of EdgeKinds is a wall, which is the
		// same reading rebuildSectorEdgePoints takes of a short slice.
		ek := "wall"
		if ei < len(s.EdgeKinds) && s.EdgeKinds[ei] == "portal" {
			ek = "portal"
		}
		if ek != kind {
			continue
		}
		if count == idx {
			return ei
		}
		count++
	}
	return -1
}

// selectedFacesCenter is where a face gizmo sits: the average of the handles
// the selection owns, which for a wall is the middle of its quad and for a
// floor the middle of its polygon.
func selectedFacesCenter() (mgl32.Vec3, bool) {
	var sum mgl32.Vec3
	n := 0
	for _, sel := range selectedFaceVertices() {
		if pos, ok := vertexSelPos(sel); ok {
			sum = sum.Add(pos)
			n++
		}
	}
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	return sum.Mul(1 / float32(n)), true
}
