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
// from, without duplicates — two faces of one brush share corners, and a
// handle written twice would move the same vert twice.
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

// faceVertexSels is one face's handles: its own corners, which for a brush
// face is simply the verts its index loop names. Empty when the face no longer
// names live geometry, which prunes a selection left behind by an edit rather
// than dragging something that is not there.
//
// The sector version had to work out which corners a face touched from its
// kind — every corner of the polygon for a floor or ceiling, two corners at
// two heights for a wall, the surface's own for a slab — and map a wall index
// back onto its ring position to do it. A brush face names its corners
// outright.
func faceVertexSels(f faceSel) []vertexSel {
	face := faceSelFace(f)
	if face == nil {
		return nil
	}
	out := make([]vertexSel, 0, len(face.Indices))
	for _, vi := range face.Indices {
		out = append(out, vertexSel{chunkIdx: f.chunkIdx, brushIdx: f.brushIdx, vertIdx: vi})
	}
	return out
}
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
