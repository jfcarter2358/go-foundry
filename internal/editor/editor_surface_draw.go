package editor

// editor_surface_draw.go — what a surface being shaped looks like.
//
// The mesh is drawn over the geometry it belongs to: every edge as a line, the
// selection picked out, and the drag arms at the selection's centre. Depth
// testing is off for all of it, the same terms the vertex and edge handles are
// already drawn on, and for the same reason — it is feedback about what an edit
// will touch, not something that has to be occluded by the wall it is lying on.
//
// Without the wireframe there is nothing to aim at. A surface's topology is
// invisible in the shaded render: a face that has been subdivided four ways
// looks exactly like one that has not until you try to extrude part of it.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

var (
	// surfaceWireColor is the unselected mesh: dim, because most of it is
	// context rather than the thing being worked on.
	surfaceWireColor = mgl32.Vec3{0.35, 0.45, 0.55}
	// surfaceSelColor is the model editor's own selection orange, reused rather
	// than copied so the same feedback in two viewports is not two slightly
	// different oranges.
	surfaceSelColor = modelFaceSelColor
	// surfaceHoleColor marks a face that has been opened. Distinct from both,
	// because an opening you cannot see is one you will cut twice.
	surfaceHoleColor = mgl32.Vec3{1, 0.35, 0.75}
	// surfaceBoundaryColor marks the points the sector owns, which cannot be
	// dragged across the surface. Showing them differently is what stops the
	// U/V arm looking broken when it refuses to move one.
	surfaceBoundaryColor = mgl32.Vec3{0.55, 0.55, 0.6}
)

// drawSurfaceEditOverlay queues the surface mesh's wireframe, handles and drag
// arms. Called from the 3D view's gizmo pass, with depth testing already off.
func drawSurfaceEditOverlay() {
	ch, _, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		return
	}

	pos := func(i int) mgl32.Vec3 {
		p := mesh.Points[i]
		return ch.LocalToWorld(frame.ToWorld(p.U, p.V, p.Depth))
	}

	selFaces := map[int]bool{}
	for _, fi := range Editor.SurfaceFaces {
		selFaces[fi] = true
	}
	selEdges := map[surfaceEdgeSel]bool{}
	for _, e := range Editor.SurfaceEdges {
		selEdges[e] = true
	}
	selPts := map[int]bool{}
	for _, v := range Editor.SurfaceVerts {
		selPts[v] = true
	}

	// Edges. A face's own selection wins over an edge's, so an extruded cap
	// reads as one shape rather than as four separately-lit edges.
	drawn := map[surfaceEdgeSel]bool{}
	for fi := range mesh.Faces {
		f := &mesh.Faces[fi]
		rings := append([][]int{f.Verts}, f.Holes...)
		for _, ring := range rings {
			for i := range ring {
				a, b := ring[i], ring[(i+1)%len(ring)]
				if a < 0 || a >= len(mesh.Points) || b < 0 || b >= len(mesh.Points) {
					continue
				}
				e := makeSurfaceEdgeSel(a, b)
				col := surfaceWireColor
				switch {
				case selFaces[fi] || selEdges[e]:
					col = surfaceSelColor
				case f.Hole:
					col = surfaceHoleColor
				case drawn[e]:
					continue
				}
				drawn[e] = true
				gizmo.GizmoLine(pos(a), pos(b), col)
			}
		}
	}

	// Point handles, only in point mode: a box at every point of a dense mesh
	// would bury the wireframe the other two modes are aiming at.
	if Editor.SurfaceMode == surfaceSelPoint {
		for i := range mesh.Points {
			col := surfaceBoundaryColor
			if mesh.Points[i].Boundary < 0 {
				col = surfaceWireColor
			}
			if selPts[i] {
				col = surfaceSelColor
			}
			gizmoBox(pos(i), 4, col)
		}
	}

	drawSurfaceDragArms(ch, frame)
}

// drawSurfaceDragArms draws the three arms at the selection's centre: depth
// along the surface normal, U and V across it.
func drawSurfaceDragArms(ch *world.Chunk, frame world.SurfaceFrame) {
	origin, u, v, d, ok := surfaceDragOrigin()
	if !ok {
		return
	}
	armLen := gizmoArmLength(origin)
	for axis := 0; axis < 3; axis++ {
		dir := surfaceAxisDir(ch, frame, u, v, d, axis)
		col := surfaceDragAxes[axis].Color
		if Editor.SurfaceDragDepth && Editor.SurfaceDragAxis == axis {
			col = mgl32.Vec3{1, 1, 0.4}
		}
		gizmo.GizmoLine(origin, origin.Add(dir.Mul(armLen)), col)
	}
	// A box on the depth arm's tip, so the one that matters is the one that is
	// easiest to grab.
	depthDir := surfaceAxisDir(ch, frame, u, v, d, 0)
	gizmoBox(origin.Add(depthDir.Mul(armLen)), 5, surfaceDragAxes[0].Color)
	_ = gfx.Projection
}
