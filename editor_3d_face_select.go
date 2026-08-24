package main

// editor_3d_face_select.go — what a selected face looks like.
//
// Face mode's click used to leave no mark: the side panel opened on the wall or
// floor it landed on and the viewport said nothing, so which of two coincident
// surfaces you actually got — the floor of this sector or the ceiling of the
// one below — was a guess until you dragged something.
//
// This is the model editor's answer (drawModelSelection, editor_model_pick.go)
// drawn against the level editor's geometry: the face's boundary outlined, plus
// a short stub along its normal, because which way "out" is decides which way
// an extrusion goes.
//
// The colour is modelFaceSelColor itself rather than a copy of its value. The
// same feedback in two viewports should not be two slightly different oranges
// — the same rule the translate gizmo already follows by reusing the 3D view's
// arm colours instead of defining its own.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// editorFaceNormalStubPx is the on-screen length of the normal stub.
//
// Pixel-scaled where the model editor's is a fixed 0.25 world units, because
// the two editors work at different scales: a model spans a few units and a
// level spans hundreds, so one fixed length is either invisible across a room
// or enormous with your nose against a wall.
const editorFaceNormalStubPx = float32(34)

// editorFaceSelSecondaryColor is what a selected face that is not the primary
// is outlined in: the same hue, dimmed.
//
// The distinction is worth drawing because the primary is not merely first in a
// list — it is the face the side panel edits, and the one a toggle takes its
// "before" value from. A dozen identically lit walls would say nothing about
// which of them the panel in the corner is describing.
var editorFaceSelSecondaryColor = modelFaceSelColor.Mul(0.55)

// drawSelectedFaceOutline queues the outline of every selected face.
//
// Caller has already turned depth testing off, so the outline shows through the
// geometry in front of it — the same terms the vertex and edge handles are
// drawn on, and for the same reason: it is feedback about what an edit will
// touch, not something that has to be clickable where it appears.
func drawSelectedFaceOutline() {
	if Editor.level == nil {
		return
	}
	for i, sel := range Editor.SelectedFaces {
		col := modelFaceSelColor
		if i > 0 {
			col = editorFaceSelSecondaryColor
		}
		editorOutlineFace(sel, col)
	}

	// A picked surface wins over a selected biome sector: clicking either one
	// in the 3D view clears the other, but the 2D biome layer can select a
	// sector on its own while a face is still up over here.
	if len(Editor.SelectedFaces) > 0 {
		return
	}
	if ch := activeChunk(); ch != nil {
		editorOutlineWaterSurface(ch)
	}
}

// editorOutlineFace outlines one selected face, whatever kind it is.
func editorOutlineFace(sel faceSel, col mgl32.Vec3) {
	ch, s := faceSelSector(sel)
	if ch == nil || s == nil {
		return
	}
	pts := SectorPoints(ch, s)
	switch sel.kind {
	case "floor":
		editorOutlineSurface(pts, s.Floor.VertexHeight, gfx.WorldUp, col)
	case "ceiling":
		editorOutlineSurface(pts, s.Ceiling.VertexHeight, gfx.WorldUp.Mul(-1), col)
	case "wall":
		if sel.idx >= 0 && sel.idx < len(s.Walls) {
			w := &s.Walls[sel.idx]
			editorOutlineUprightQuad(pts, w.StartPoint, w.EndPoint,
				s.Floor.VertexHeight, s.WallTopHeight, col)
		}
	case "portal":
		// Not reachable from a 3D click — pick3D lets clicks pass through an
		// opening — but the 2D map selects portal edges, and switching back
		// to 3D should show which one.
		if sel.idx >= 0 && sel.idx < len(s.Portals) {
			p := &s.Portals[sel.idx]
			editorOutlineUprightQuad(pts, p.StartPoint, p.EndPoint,
				s.Floor.VertexHeight, s.WallTopHeight, col)
		}
	}
}

// editorOutlineSurface outlines a sector's floor or ceiling: its polygon at the
// per-vertex heights of that surface, closed, with a stub along the way it
// faces.
func editorOutlineSurface(pts []mgl32.Vec2, heightAt func(int) float32, normal, col mgl32.Vec3) {
	n := len(pts)
	if n < 2 {
		return
	}
	at := func(i int) mgl32.Vec3 {
		return mgl32.Vec3{pts[i].X(), heightAt(i), pts[i].Y()}
	}
	var sum mgl32.Vec3
	for i := 0; i < n; i++ {
		a := at(i)
		GizmoLine(a, at((i+1)%n), col)
		sum = sum.Add(a)
	}
	editorFaceNormalStub(sum.Mul(1/float32(n)), normal, col)
}

// editorOutlineUprightQuad outlines the vertical quad spanning p1 → p2 between
// two height functions — the shape a wall occupies, and a portal's gap fill.
//
// The endpoints are looked up in the sector's own points to find which vertex
// heights apply, exactly as the wall mesh is built. findVertexIdx returns -1
// for a point that is not one of them, and VertexHeight reads that as "no
// offset" — the same fallback the mesh itself lands on.
func editorOutlineUprightQuad(pts []mgl32.Vec2, p1, p2 mgl32.Vec2, bottom, top func(int) float32, col mgl32.Vec3) {
	i1, i2 := findVertexIdx(pts, p1), findVertexIdx(pts, p2)
	a := mgl32.Vec3{p1.X(), bottom(i1), p1.Y()}
	b := mgl32.Vec3{p2.X(), bottom(i2), p2.Y()}
	c := mgl32.Vec3{p2.X(), top(i2), p2.Y()}
	d := mgl32.Vec3{p1.X(), top(i1), p1.Y()}
	for _, e := range [4][2]mgl32.Vec3{{a, b}, {b, c}, {c, d}, {d, a}} {
		GizmoLine(e[0], e[1], col)
	}

	// Outward normal: the left-hand normal of the run, which is the convention
	// WallFacingCamera (culling.go) already reads walls with.
	dx, dz := p2.X()-p1.X(), p2.Y()-p1.Y()
	center := a.Add(b).Add(c).Add(d).Mul(0.25)
	editorFaceNormalStub(center, mgl32.Vec3{-dz, 0, dx}, col)
}

// editorOutlineWaterSurface outlines the water plane of the selected Water-kind
// biome sector: the polygon that was painted, drawn at the height the plane
// derived from it actually sits at.
func editorOutlineWaterSurface(ch *Chunk) {
	if Editor.ActiveBiomeSectorIdx < 0 || Editor.ActiveBiomeSectorIdx >= len(ch.BiomeSectors) {
		return
	}
	bs := &ch.BiomeSectors[Editor.ActiveBiomeSectorIdx]
	if bs.Brush.Kind != BiomeWater || len(bs.Points) < 2 {
		return
	}
	y, ok := float32(0), false
	for wi := range ch.WaterSurfaces {
		if ch.WaterSurfaces[wi].ID == bs.ID {
			y, ok = ch.WaterSurfaces[wi].Position.Y(), true
			break
		}
	}
	if !ok {
		// Painted but not synced into a plane yet, so there is no surface to
		// outline and no height to guess at.
		return
	}
	editorOutlineSurface(bs.Points, func(int) float32 { return y }, gfx.WorldUp, modelFaceSelColor)
}

// editorFaceNormalStub draws the short line out of a face's middle that says
// which side is the front.
func editorFaceNormalStub(center, normal, col mgl32.Vec3) {
	if normal.Len() < 1e-6 {
		return
	}
	length := worldSizeForPixels(center, editorFaceNormalStubPx)
	GizmoLine(center, center.Add(normal.Normalize().Mul(length)), col)
}
