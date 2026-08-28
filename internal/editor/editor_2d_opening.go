package editor

// editor_2d_opening.go — drawing holes through a floor or ceiling.
//
// An opening is authored the way a biome polygon is: arm the tool, click the
// corners on the map, Enter to close it, Escape to abandon it. That gesture is
// reused deliberately rather than invented — it is already the thing in this
// editor that means "draw a polygon on the ground", and an opening is one.
//
// The hole attaches to whichever sector on the active layer its centroid lands
// in. That is the whole of the binding: an opening records no far-side sector,
// because what lies under a hole is a geometric question the engine re-answers
// every time it is asked (world.OpeningTarget). Nothing here can go stale.
//
// Which surface it cuts — floor or ceiling — follows the map's existing
// floor/ceiling switch (Editor.EditFloorVerts), the same one that decides which
// corner handles are lit and which heights a drag moves. A stairwell down is a
// floor opening; the hole it arrives through in the storey above is a ceiling
// one, and an author usually wants only the first.

import (
	"foundry/internal/geom"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// Colours the map draws holes in: warm for a floor opening, cool for a ceiling
// one, matching the red/blue the corner crosses already use for the two
// surfaces.
var (
	openingFloorColor   = mgl32.Vec3{1, 0.55, 0.15}
	openingCeilColor    = mgl32.Vec3{0.5, 0.6, 1}
	openingPendingColor = mgl32.Vec3{0.2, 0.9, 0.2}
)

// openingSurface returns the surface a new opening would be cut into, and the
// list it would join, for the sector at index si.
func openingSurface(s *world.Sector) (openings *[]world.Opening, isFloor bool) {
	if Editor.EditFloorVerts {
		return &s.Floor.Openings, true
	}
	return &s.Ceiling.Openings, false
}

// EditorBeginOpening arms the draw tool. The next clicks on the map place the
// hole's corners.
func EditorBeginOpening() {
	if activeChunk() == nil {
		return
	}
	Editor.OpeningDrawing = true
	Editor.OpeningDrawPoints = nil
	// Two polygon tools at once would each take the same click.
	Editor.BiomeDrawing = false
	Editor.BiomeDrawPoints = nil
	surface := "floor"
	if !Editor.EditFloorVerts {
		surface = "ceiling"
	}
	editorSetStatus("drawing a %s opening — click corners, Enter to close, Esc to cancel", surface)
}

// openingHandleClick places one corner while the tool is armed. Returns true
// when it consumed the event.
//
// Only the drawing half lives here, and it runs early — a click while a polygon
// is being placed means that corner and nothing else. Grabbing an EXISTING
// hole's corner is openingHandleGrab, called further down Editor2DHandleClick's
// ladder so it cannot steal a click aimed at a gizmo handle.
func openingHandleClick(t sdl.MouseButtonEvent) bool {
	if !Editor.OpeningDrawing || t.Button != sdl.ButtonLeft {
		return false
	}
	ch := activeChunk()
	if ch == nil {
		return false
	}
	lx, lz := screenToChunkLocal(ch, t.X, t.Y)
	Editor.OpeningDrawPoints = append(Editor.OpeningDrawPoints, mgl32.Vec2{lx, lz})
	return true
}

// openingHandleGrab arms a drag on an existing hole's corner under the cursor.
// Returns true when it consumed the event.
//
// Placed after the gizmo in the click ladder — a gizmo handle is drawn on top
// of everything and is the more explicit target — but before the map's own
// picking, because an opening's corner is a smaller and more specific thing
// than the sector it is cut into, the same reason an edge outranks a sector
// interior in face mode. Missing one falls through untouched.
func openingHandleGrab(t sdl.MouseButtonEvent) bool {
	if t.Button != sdl.ButtonLeft || Editor.OpeningDrawing {
		return false
	}
	// The biome layer owns the map's clicks outright while it is active.
	if Editor.BiomeLayerActive {
		return false
	}
	ch := activeChunk()
	if ch == nil {
		return false
	}
	lx, lz := screenToChunkLocal(ch, t.X, t.Y)
	threshold := chunkLocalSize(ch, screenToWorldSize(editor2DSelectRadius, editor2DOrtho()))
	return openingGrabVertex(ch, lx, lz, threshold)
}

// openingGrabVertex arms a drag on the opening corner nearest (lx,lz) within
// threshold, on the active layer and the surface the map is editing. Returns
// false when the click reached no corner.
func openingGrabVertex(ch *world.Chunk, lx, lz, threshold float32) bool {
	bestDist := threshold
	bestSI, bestOI, bestVI := -1, -1, -1
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if !layerEditable(s.Layer) {
			continue
		}
		list, _ := openingSurface(s)
		for oi := range *list {
			for vi, p := range (*list)[oi].Points {
				if d := dist2D(lx, lz, p.X(), p.Y()); d < bestDist {
					bestDist, bestSI, bestOI, bestVI = d, si, oi, vi
				}
			}
		}
	}
	if bestSI < 0 {
		return false
	}
	EditorPushUndo() // snapshot at drag start, not per-frame during the drag
	Editor.OpeningDragSector = bestSI
	Editor.OpeningDragIdx = bestOI
	Editor.OpeningDragVertex = bestVI
	Editor.OpeningDragFloor = Editor.EditFloorVerts
	return true
}

// openingHandleMouseMove moves the corner being dragged, if any.
//
// The sector is retriangulated on every move rather than on release, so the
// hole in the mesh follows the corner as it is dragged — which is the only way
// to see what the hole will actually look like while shaping it.
func openingHandleMouseMove(t sdl.MouseMotionEvent) {
	if Editor.OpeningDragSector < 0 {
		return
	}
	ch := activeChunk()
	if ch == nil || Editor.OpeningDragSector >= len(ch.Sectors) {
		return
	}
	s := &ch.Sectors[Editor.OpeningDragSector]
	// The surface is the one latched at mouse-down, not whatever the map's
	// switch says now: flipping Floor/Ceiling Verts mid-drag must not move the
	// grabbed corner onto the other surface's hole.
	list := &s.Floor.Openings
	if !Editor.OpeningDragFloor {
		list = &s.Ceiling.Openings
	}
	if Editor.OpeningDragIdx >= len(*list) {
		openingEndDrag()
		return
	}
	op := &(*list)[Editor.OpeningDragIdx]
	if Editor.OpeningDragVertex >= len(op.Points) {
		openingEndDrag()
		return
	}

	lx, lz := screenToChunkLocal(ch, t.X, t.Y)
	if Editor.SnapToGrid && Editor.StepSize > 0 {
		lx = snapToStep(lx)
		lz = snapToStep(lz)
	}
	op.Points[Editor.OpeningDragVertex] = mgl32.Vec2{lx, lz}

	// The slab's other face moves with it. The twin was cut from the same
	// polygon and its corners are index-aligned, so the same vertex moves — a
	// pair that drifted would leave a lip of floor over the hole from one side.
	farSector, twin, paired := world.OpeningPartner(ch, s, op, Editor.OpeningDragFloor)
	if paired && Editor.OpeningDragVertex < len(twin.Points) {
		twin.Points[Editor.OpeningDragVertex] = mgl32.Vec2{lx, lz}
	}

	if err := world.ReloadSector(ch, s); err != nil {
		editorSetStatus("opening: %v", err)
		return
	}
	if paired {
		if err := world.ReloadSector(ch, farSector); err != nil {
			editorSetStatus("opening: %v", err)
		}
	}
}

// openingHandleRelease ends a corner drag.
func openingHandleRelease(t sdl.MouseButtonEvent) {
	if t.Button == sdl.ButtonLeft {
		openingEndDrag()
	}
}

func openingEndDrag() {
	Editor.OpeningDragSector = -1
	Editor.OpeningDragIdx = -1
	Editor.OpeningDragVertex = -1
}

// openingDraggingVertex reports whether a corner drag is in progress, so the
// map's other drag paths leave the mouse alone.
func openingDraggingVertex() bool { return Editor.OpeningDragSector >= 0 }

// snapToStep rounds a coordinate to the map's drag quantum.
func snapToStep(v float32) float32 {
	step := Editor.StepSize
	return float32(int(v/step+copySign(0.5, v))) * step
}

func copySign(mag, sign float32) float32 {
	if sign < 0 {
		return -mag
	}
	return mag
}

// openingHandleKey processes Enter/Escape while the tool is armed. Returns true
// when it consumed the event.
func openingHandleKey(t sdl.KeyboardEvent) bool {
	if !Editor.OpeningDrawing || t.Type != sdl.KEYDOWN {
		return false
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_ESCAPE:
		Editor.OpeningDrawing = false
		Editor.OpeningDrawPoints = nil
		editorSetStatus("opening cancelled")
		return true
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		if ch := activeChunk(); ch != nil {
			finishOpeningDraw(ch)
		} else {
			Editor.OpeningDrawing = false
			Editor.OpeningDrawPoints = nil
		}
		return true
	}
	return false
}

// finishOpeningDraw closes the polygon and cuts it into the sector under it.
func finishOpeningDraw(ch *world.Chunk) {
	pts := Editor.OpeningDrawPoints
	Editor.OpeningDrawing = false
	Editor.OpeningDrawPoints = nil

	if len(pts) < 3 {
		editorSetStatus("an opening needs at least three corners")
		return
	}
	pts = append([]mgl32.Vec2(nil), pts...)
	// CCW seen from above, the same winding a sector outline uses. The
	// triangulator flips holes to the winding it needs; keeping the stored form
	// consistent is what lets everything else read them the same way.
	if world.SignedArea2D(pts) < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}

	si := openingHostSector(ch, pts)
	if si < 0 {
		editorSetStatus("an opening has to be drawn inside a sector on this layer")
		return
	}

	EditorPushUndo()
	s := &ch.Sectors[si]
	list, isFloor := openingSurface(s)
	surface := "floor"
	if !isFloor {
		surface = "ceiling"
	}
	op := world.Opening{ID: world.NextOpeningID(*list, "opening"), Points: pts}

	// Cut the slab's other face too. A hole between two storeys goes through a
	// slab with two faces — the floor above and the ceiling below — and cutting
	// only the one drawn on leaves the other still covering it. Opaque geometry
	// draws with face culling off, so the room below's ceiling fills the
	// stairwell seen from above just as solidly as from below.
	//
	// Done here rather than derived at render time because whether a surface
	// belongs to this slab is a judgement (see world.SlabFace), and an author
	// who wants a hole in only one face can delete the twin.
	far, cutFar := world.SlabFace(ch, s, &op, isFloor)
	var farList *[]world.Opening
	if cutFar {
		if isFloor {
			farList = &far.Ceiling.Openings
		} else {
			farList = &far.Floor.Openings
		}
		twin := world.Opening{
			ID:     world.NextOpeningID(*farList, "opening"),
			Points: append([]mgl32.Vec2(nil), pts...),
			Link:   op.ID,
		}
		op.Link = twin.ID
		*farList = append(*farList, twin)
	}
	*list = append(*list, op)

	if err := world.ReloadSector(ch, s); err != nil {
		editorSetStatus("opening: %v", err)
		return
	}
	if cutFar {
		if err := world.ReloadSector(ch, far); err != nil {
			editorSetStatus("opening: %v", err)
			return
		}
	}
	ch.RebuildGaps()
	Editor.ActiveSectorIdx = si
	if cutFar {
		editorSetStatus("cut %s through the slab between %s and %s", op.ID, s.ID, far.ID)
		return
	}
	editorSetStatus("cut %s in %s's %s", op.ID, s.ID, surface)
}

// openingHostSector returns the index of the sector on the active layer that a
// polygon belongs to — the one containing its centroid — or -1.
//
// The centroid rather than every corner, so a hole drawn a little sloppily over
// a sector's edge still lands somewhere sensible instead of being refused.
func openingHostSector(ch *world.Chunk, pts []mgl32.Vec2) int {
	var c mgl32.Vec2
	for _, p := range pts {
		c = c.Add(p)
	}
	c = c.Mul(1 / float32(len(pts)))

	for si := range ch.Sectors {
		if !layerEditable(ch.Sectors[si].Layer) {
			continue
		}
		if world.PointInSector(ch, &ch.Sectors[si], c) {
			return si
		}
	}
	return -1
}

// EditorDeleteOpeningAt removes the opening under chunk-local (lx,lz) on the
// active layer, on whichever surface the map is currently editing. Returns true
// if one was removed.
func EditorDeleteOpeningAt(ch *world.Chunk, lx, lz float32) bool {
	if ch == nil {
		return false
	}
	xz := mgl32.Vec2{lx, lz}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if !layerEditable(s.Layer) {
			continue
		}
		list, isFloor := openingSurface(s)
		for oi := range *list {
			if !(*list)[oi].Contains(xz) {
				continue
			}
			EditorPushUndo()
			id := (*list)[oi].ID
			// Both faces of the slab go: they are one hole, and leaving the far
			// one behind would fill the shaft back in from the other side while
			// looking as though the hole was removed.
			farSector, twin, paired := world.OpeningPartner(ch, s, &(*list)[oi], isFloor)
			*list = append((*list)[:oi], (*list)[oi+1:]...)
			if paired {
				removeOpeningByID(farSector, !isFloor, twin.ID)
			}
			if err := world.ReloadSector(ch, s); err != nil {
				editorSetStatus("opening: %v", err)
				return true
			}
			if paired {
				if err := world.ReloadSector(ch, farSector); err != nil {
					editorSetStatus("opening: %v", err)
					return true
				}
			}
			ch.RebuildGaps()
			editorSetStatus("removed %s from %s", id, s.ID)
			return true
		}
	}
	return false
}

// removeOpeningByID deletes the named opening from one of a sector's surfaces.
func removeOpeningByID(s *world.Sector, isFloor bool, id string) {
	list := &s.Floor.Openings
	if !isFloor {
		list = &s.Ceiling.Openings
	}
	for i := range *list {
		if (*list)[i].ID == id {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return
		}
	}
}

// openingAt reports whether any opening on the active layer covers the point,
// on either surface — what the context menu asks to decide whether to offer a
// delete row.
func openingAt(ch *world.Chunk, lx, lz float32) bool {
	if ch == nil {
		return false
	}
	xz := mgl32.Vec2{lx, lz}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if !layerEditable(s.Layer) {
			continue
		}
		list, _ := openingSurface(s)
		if world.SurfaceIsOpenAt(*list, xz) {
			return true
		}
	}
	return false
}

// Editor2DRenderOpenings draws the holes cut in the layer's surfaces, and the
// polygon currently being drawn.
//
// Drawn at the surface's own height, like every other line on the map, so an
// opening sits visibly on the floor it belongs to rather than floating on the
// map's ground plane.
func Editor2DRenderOpenings(ch *world.Chunk) {
	wp3 := func(p mgl32.Vec2, y float32) mgl32.Vec3 {
		return ch.LocalToWorld(mgl32.Vec3{p.X(), y, p.Y()})
	}
	ring := func(pts []mgl32.Vec2, y float32, col mgl32.Vec3) {
		for i := range pts {
			gizmo.GizmoLine(wp3(pts[i], y), wp3(pts[(i+1)%len(pts)], y), col)
		}
	}

	crossSz := screenToWorldSize(4, editor2DOrtho())
	// Corner handles, drawn only on the openings a click can actually grab —
	// the active layer's, on the surface the map is editing. A handle on a hole
	// that cannot be dragged is a promise the map does not keep.
	handles := func(pts []mgl32.Vec2, y float32, col mgl32.Vec3, oi int, si int, isFloor bool) {
		for vi, p := range pts {
			c := col
			if si == Editor.OpeningDragSector && oi == Editor.OpeningDragIdx &&
				vi == Editor.OpeningDragVertex && isFloor == Editor.OpeningDragFloor {
				c = mgl32.Vec3{1, 1, 0}
			}
			gp := wp3(p, y)
			gizmo.GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), c)
			gizmo.GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), c)
		}
	}

	// Ghosts first, the active layer last, for the reason layerDrawOrder gives:
	// a hole on the layer being edited is what the corner handles belong to.
	for _, si := range layerDrawOrder(ch) {
		s := &ch.Sectors[si]
		if !layerDrawn(s.Layer) {
			continue
		}
		grabbable := layerEditable(s.Layer)
		for i := range s.Floor.Openings {
			col := layerTint(s.Layer, openingFloorColor)
			ring(s.Floor.Openings[i].Points, s.Floor.Height, col)
			if grabbable && Editor.EditFloorVerts {
				handles(s.Floor.Openings[i].Points, s.Floor.Height, col, i, si, true)
			}
		}
		for i := range s.Ceiling.Openings {
			col := layerTint(s.Layer, openingCeilColor)
			ring(s.Ceiling.Openings[i].Points, s.Ceiling.Height, col)
			if grabbable && !Editor.EditFloorVerts {
				handles(s.Ceiling.Openings[i].Points, s.Ceiling.Height, col, i, si, false)
			}
		}
	}

	// The in-progress outline: an open chain, plus the closing edge dashed in
	// by drawing it back to the first point, so what Enter would commit is
	// visible before committing to it.
	if Editor.OpeningDrawing && len(Editor.OpeningDrawPoints) > 0 {
		pts := Editor.OpeningDrawPoints
		y := openingDrawHeight(ch, pts[0])
		for i := 0; i+1 < len(pts); i++ {
			gizmo.GizmoLine(wp3(pts[i], y), wp3(pts[i+1], y), openingPendingColor)
		}
		if len(pts) >= 3 {
			gizmo.GizmoLine(wp3(pts[len(pts)-1], y), wp3(pts[0], y), openingPendingColor)
		}
		crossSz := screenToWorldSize(4, editor2DOrtho())
		for _, p := range pts {
			gp := wp3(p, y)
			gizmo.GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), openingPendingColor)
			gizmo.GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), openingPendingColor)
		}
	}
}

// openingDrawHeight is the height to draw an in-progress outline at: the
// surface under its first corner, so it lies on the floor being cut rather than
// at y=0 under everything.
func openingDrawHeight(ch *world.Chunk, first mgl32.Vec2) float32 {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if !layerEditable(s.Layer) {
			continue
		}
		if !geom.PointInPolygon(first, world.SectorPoints(ch, s)) {
			continue
		}
		if Editor.EditFloorVerts {
			return s.Floor.Height
		}
		return s.Ceiling.Height
	}
	return ch.LayerAt(activeLayer()).BaseY
}
