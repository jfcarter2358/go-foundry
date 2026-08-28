package editor

// editor_surface.go — shaping a level surface in the 3D view.
//
// This replaces two things: the "place an outer polygon, then an inner one,
// then type a depth" extrusion flow (editor_extrusion.go), and the flat 2D
// window you had to leave the 3D view for to place terrain points
// (editor_surface2d.go). Both existed because the data model underneath was a
// list of prisms and a list of loose points, neither of which you could work on
// in place.
//
// What is here instead is the model editor's interaction, pointed at a level
// face: pick a surface, and the selection modes retarget from the sector's
// topology to that surface's own mesh. 1, 2 and 3 mean its points, its edges
// and its faces; E extrudes; I insets; C connects two points into a new edge.
// Nothing is placed by typing coordinates, and nothing needs a second window.
//
// # Why this is a sub-mode and not a mode
//
// The camera, the level, the lighting and the rest of the viewport stay exactly
// as they are — you are still looking at the room, just editing one of its
// faces. Making it a mode would mean leaving the room to shape its wall, which
// is the thing about the old 2D surface editor that made it not get used: the
// wall you are cutting a window into only makes sense next to the window
// opposite it.
//
// # Depth is the axis that matters
//
// A surface point moves along two quite different axes. Sliding it in (U, V)
// moves it *across* the face and changes the shape's outline; pushing it in
// depth moves it *out of* the face and is what makes a bump a bump. The drag
// gizmo therefore offers depth as its own arm, along the surface normal, rather
// than making you find it among three world axes that are all oblique to the
// wall you are working on.

import (
	"fmt"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// surfaceSelMode is what a click selects inside surface mode — the same three
// the model editor offers, and bound to the same keys.
type surfaceSelMode int

const (
	surfaceSelPoint surfaceSelMode = iota
	surfaceSelEdge
	surfaceSelFace
)

func (m surfaceSelMode) String() string {
	switch m {
	case surfaceSelPoint:
		return "point"
	case surfaceSelEdge:
		return "edge"
	default:
		return "face"
	}
}

// surfaceEdgeSel names one edge of the mesh by its two point indices, ordered
// so the two directions compare equal.
type surfaceEdgeSel struct{ A, B int }

func makeSurfaceEdgeSel(a, b int) surfaceEdgeSel {
	if a > b {
		a, b = b, a
	}
	return surfaceEdgeSel{a, b}
}

// surfacePendingExtrude remembers the extrude a drag is still setting the depth
// of, so pressing E twice in a row stacks a second segment on the first rather
// than starting over — the same continuation the model editor's
// modelExtrudePending tracks.
type surfacePendingExtrude struct {
	Caps  []int
	Sides []int
}

// SurfaceEditState is everything surface mode needs. It hangs off EditorState
// rather than being a package global so undo can snapshot and restore it with
// the rest of the editor's selection.
type SurfaceEditState struct {
	SurfaceEditing bool
	SurfaceFace    faceSel // the level face being shaped
	SurfaceMode    surfaceSelMode
	SurfaceVerts   []int
	SurfaceEdges   []surfaceEdgeSel
	SurfaceFaces   []int
	SurfacePending surfacePendingExtrude
	SurfaceStatus  string

	// SurfaceDragDepth is set while a drag is moving the selection along the
	// surface normal; SurfaceDragStart holds the depths it started from, so a
	// long drag stays exact instead of accumulating per-event error.
	SurfaceDragDepth bool
	SurfaceDragAxis  int // 0 = depth, 1 = U, 2 = V
	SurfaceDragStart []float32
	SurfaceDragParam float32
	SurfaceDragPts   []int
}

// surfaceEditActive reports whether surface mode is on and still pointing at
// live geometry. Everything below is gated on it, so a face deleted out from
// under the mode cannot be edited by a stale index.
func surfaceEditActive() bool {
	return Editor.SurfaceEditing && faceSelValid(Editor.SurfaceFace)
}

// surfaceEditTarget resolves the mesh and frame currently being shaped.
func surfaceEditTarget() (*world.Chunk, *world.Sector, *world.SurfaceMesh, world.SurfaceFrame, bool) {
	if !surfaceEditActive() {
		return nil, nil, nil, nil, false
	}
	sel := Editor.SurfaceFace
	ch, s := faceSelSector(sel)
	if ch == nil || s == nil {
		return nil, nil, nil, nil, false
	}
	pts := world.SectorPoints(ch, s)
	frame, ok := s.SurfaceFrameFor(pts, sel.kind, sel.idx)
	if !ok {
		return nil, nil, nil, nil, false
	}
	mesh := s.SurfaceMeshFor(sel.kind, sel.idx)
	if mesh == nil {
		return nil, nil, nil, nil, false
	}
	return ch, s, mesh, frame, true
}

// EnterSurfaceEdit begins shaping the given face.
//
// A face that has never been shaped has an empty mesh — that is how a flat
// surface is stored — so it is seeded with its outline here. Seeding on entry
// rather than on load is what keeps a level of flat rooms from carrying a mesh
// per surface that says nothing.
func EnterSurfaceEdit(sel faceSel) {
	if !faceSelValid(sel) || sel.kind == "portal" {
		return
	}
	ch, s := faceSelSector(sel)
	if ch == nil || s == nil {
		return
	}
	pts := world.SectorPoints(ch, s)
	frame, ok := s.SurfaceFrameFor(pts, sel.kind, sel.idx)
	if !ok {
		return
	}
	mesh := s.SurfaceMeshFor(sel.kind, sel.idx)
	if mesh == nil {
		return
	}
	if mesh.IsEmpty() {
		EditorPushUndo()
		*mesh = world.SeedSurfaceMesh(frame)
		_ = world.ReloadSector(ch, s)
	}

	Editor.SurfaceEditing = true
	Editor.SurfaceFace = sel
	Editor.SurfaceMode = surfaceSelFace
	clearSurfaceSelection()
	setFaceSelection([]faceSel{sel})
	surfaceSetStatus("Shaping %s — [1] points [2] edges [3] faces, E extrude, I inset, B subdivide, C connect, Esc done", sel.kind)
}

// ExitSurfaceEdit leaves surface mode, keeping the level face selected so the
// properties panel stays where it was.
func ExitSurfaceEdit() {
	Editor.SurfaceEditing = false
	Editor.SurfacePending = surfacePendingExtrude{}
	Editor.SurfaceDragDepth = false
	clearSurfaceSelection()
	Editor.SurfaceStatus = ""
}

// surfaceEditResumeAfterSnapshot re-establishes surface shaping across an
// undo/redo snapshot, instead of dropping out of it.
//
// The mode used to be abandoned outright, because everything it holds indexes
// into a mesh the restored level may have rebuilt with a different number of
// points and faces. That is true of the *selection*, and only of the selection:
// which face is being shaped is a chunk/sector/face name, and a snapshot
// restores the same chunks and sectors in the same order, so it survives. So
// the selection is what goes, and shaping carries on — an undo is a correction
// to the shape you are working on, not a reason to stop working on it.
//
// Called by editorResetSelectionState, which owns the rest of the same job.
func surfaceEditResumeAfterSnapshot() {
	if !Editor.SurfaceEditing {
		return
	}
	sel := Editor.SurfaceFace

	// Everything naming a point, an edge or a face inside the mesh, plus the
	// half-finished operations that name several at once.
	Editor.SurfacePending = surfacePendingExtrude{}
	Editor.SurfaceDragDepth = false
	Editor.SurfaceDragStart = nil
	Editor.SurfaceDragPts = nil
	clearSurfaceSelection()

	_, s := faceSelSector(sel)
	if !faceSelValid(sel) || s == nil {
		ExitSurfaceEdit()
		return
	}
	mesh := s.SurfaceMeshFor(sel.kind, sel.idx)
	if mesh == nil || mesh.IsEmpty() {
		// Undone back past the seed EnterSurfaceEdit laid down, which is to say
		// past entering the mode at all. A surface with no mesh has nothing to
		// shape, so leaving is the honest answer rather than sitting in a mode
		// with an empty viewport overlay.
		ExitSurfaceEdit()
		return
	}
	// Put the level face back in the selection, so the properties panel keeps
	// showing the surface being shaped — EnterSurfaceEdit's own last step.
	setFaceSelection([]faceSel{sel})
	surfaceSetStatus("Shaping %s — selection cleared by the undo", sel.kind)
}

func clearSurfaceSelection() {
	Editor.SurfaceVerts = nil
	Editor.SurfaceEdges = nil
	Editor.SurfaceFaces = nil
}

func surfaceSetStatus(format string, args ...any) {
	Editor.SurfaceStatus = fmt.Sprintf(format, args...)
}

// surfaceSetSelMode switches which topology a click picks, carrying the current
// selection over so switching modes does not throw work away: the points of the
// selected faces become the point selection, and the faces touching the
// selected points become the face selection.
func surfaceSetSelMode(mode surfaceSelMode) {
	if mode == Editor.SurfaceMode {
		return
	}
	_, _, mesh, _, ok := surfaceEditTarget()
	if !ok {
		Editor.SurfaceMode = mode
		return
	}
	pts := surfaceSelectedPoints(mesh)
	Editor.SurfaceMode = mode
	clearSurfaceSelection()
	switch mode {
	case surfaceSelPoint:
		Editor.SurfaceVerts = pts
	case surfaceSelEdge:
		Editor.SurfaceEdges = surfaceEdgesWithin(mesh, pts)
	case surfaceSelFace:
		Editor.SurfaceFaces = surfaceFacesWithin(mesh, pts)
	}
	surfaceSetStatus("%s select", mode)
}

// surfaceSelectedPoints is every point the current selection touches, whatever
// mode it is in — the common currency the three modes convert through.
func surfaceSelectedPoints(mesh *world.SurfaceMesh) []int {
	seen := map[int]bool{}
	add := func(i int) {
		if i >= 0 && i < len(mesh.Points) {
			seen[i] = true
		}
	}
	for _, v := range Editor.SurfaceVerts {
		add(v)
	}
	for _, e := range Editor.SurfaceEdges {
		add(e.A)
		add(e.B)
	}
	for _, fi := range Editor.SurfaceFaces {
		if fi < 0 || fi >= len(mesh.Faces) {
			continue
		}
		for _, v := range mesh.Faces[fi].Verts {
			add(v)
		}
	}
	out := make([]int, 0, len(seen))
	for i := range mesh.Points {
		if seen[i] {
			out = append(out, i)
		}
	}
	return out
}

// surfaceEdgesWithin is every mesh edge with both ends in pts.
func surfaceEdgesWithin(mesh *world.SurfaceMesh, pts []int) []surfaceEdgeSel {
	in := map[int]bool{}
	for _, p := range pts {
		in[p] = true
	}
	seen := map[surfaceEdgeSel]bool{}
	var out []surfaceEdgeSel
	for fi := range mesh.Faces {
		ring := mesh.Faces[fi].Verts
		for i := range ring {
			a, b := ring[i], ring[(i+1)%len(ring)]
			if !in[a] || !in[b] {
				continue
			}
			e := makeSurfaceEdgeSel(a, b)
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out
}

// surfaceFacesWithin is every face all of whose corners are in pts. All rather
// than any, because "the faces my selected points touch" would grow the
// selection every time you switched modes back and forth.
func surfaceFacesWithin(mesh *world.SurfaceMesh, pts []int) []int {
	in := map[int]bool{}
	for _, p := range pts {
		in[p] = true
	}
	var out []int
	for fi := range mesh.Faces {
		ring := mesh.Faces[fi].Verts
		if len(ring) == 0 {
			continue
		}
		all := true
		for _, v := range ring {
			if !in[v] {
				all = false
				break
			}
		}
		if all {
			out = append(out, fi)
		}
	}
	return out
}

// ---------- operations ----------

// surfaceRebuild recompiles the sector after a mesh edit. Every op ends here,
// so none of them can leave the GPU showing the mesh as it was.
func surfaceRebuild(ch *world.Chunk, s *world.Sector) {
	if ch == nil || s == nil {
		return
	}
	_ = world.ReloadSector(ch, s)
}

// surfaceExtrude is what E does: grow the selected faces, or continue the
// extrude already in progress.
//
// The topology is created at zero depth and the drag that follows sets it,
// which is the model editor's flow (modelStartExtrude) and the reason
// RefreshSurfaceSideUVs has to be re-derivable rather than baked once.
func surfaceExtrude() {
	ch, s, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}
	if Editor.SurfaceMode != surfaceSelFace || len(Editor.SurfaceFaces) == 0 {
		// Silence is what "E does nothing" feels like from the outside, and the
		// usual cause is being in point or edge mode, which the viewport does
		// not otherwise announce.
		surfaceSetStatus("Nothing to extrude — switch to face select [3] and click a face")
		return
	}
	EditorPushUndo()
	caps, sides := mesh.SurfaceExtrudeFaces(Editor.SurfaceFaces, 0)
	if len(caps) == 0 {
		EditorPopUndo()
		surfaceSetStatus("Cannot extrude that selection")
		return
	}
	Editor.SurfaceFaces = caps
	Editor.SurfacePending = surfacePendingExtrude{Caps: caps, Sides: sides}
	surfaceRebuild(ch, s)
	surfaceSetStatus("Extruded %d faces — drag the depth arm to set how far", len(caps))
}

// surfaceInset shrinks the selected faces towards their own centres, leaving a
// ring of new faces around them. It is the other half of cutting a doorway:
// extrude can only push a whole face, so a hole in a wall needs a smaller face
// to push first.
func surfaceInset() {
	ch, s, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}
	if Editor.SurfaceMode != surfaceSelFace || len(Editor.SurfaceFaces) == 0 {
		surfaceSetStatus("Nothing to inset — switch to face select [3] and click a face")
		return
	}
	EditorPushUndo()
	inset := mesh.SurfaceInsetFaces(Editor.SurfaceFaces, 0.35)
	if len(inset) == 0 {
		EditorPopUndo()
		surfaceSetStatus("Cannot inset that selection")
		return
	}
	Editor.SurfaceFaces = inset
	Editor.SurfacePending = surfacePendingExtrude{}
	surfaceRebuild(ch, s)
	surfaceSetStatus("Inset %d faces — E to push them in or out", len(inset))
}

// surfaceSubdivide splits the selected faces at their edge midpoints, which is
// how you get somewhere to put a bump on an otherwise featureless face.
func surfaceSubdivide() {
	ch, s, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}
	if Editor.SurfaceMode != surfaceSelFace || len(Editor.SurfaceFaces) == 0 {
		surfaceSetStatus("Nothing to subdivide — switch to face select [3] and click a face")
		return
	}
	EditorPushUndo()
	out := mesh.SurfaceSubdivideFaces(Editor.SurfaceFaces)
	if len(out) == 0 {
		EditorPopUndo()
		surfaceSetStatus("Cannot subdivide that selection")
		return
	}
	Editor.SurfaceFaces = out
	Editor.SurfacePending = surfacePendingExtrude{}
	surfaceRebuild(ch, s)
	surfaceSetStatus("Subdivided into %d faces", len(out))
}

// surfaceConnect is what C does: join two selected points with a new edge,
// splitting the face they both belong to. This is the "place and connect
// points" half of shaping a face by hand.
func surfaceConnect() {
	ch, s, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}
	if Editor.SurfaceMode != surfaceSelPoint || len(Editor.SurfaceVerts) != 2 {
		surfaceSetStatus("Connect needs exactly two selected points — switch to point select [1]")
		return
	}
	a, b := Editor.SurfaceVerts[0], Editor.SurfaceVerts[1]
	// Find a face both points belong to. There may be two — an interior edge's
	// worth — and either is a legitimate place to cut, so the first wins.
	target := -1
	for fi := range mesh.Faces {
		ring := mesh.Faces[fi].Verts
		var hasA, hasB bool
		for _, v := range ring {
			if v == a {
				hasA = true
			}
			if v == b {
				hasB = true
			}
		}
		if hasA && hasB {
			target = fi
			break
		}
	}
	if target < 0 {
		surfaceSetStatus("Those two points share no face — nothing to cut across")
		return
	}
	EditorPushUndo()
	if mesh.SurfaceSplitFace(target, a, b) < 0 {
		EditorPopUndo()
		surfaceSetStatus("Cannot connect those two: they are already adjacent, or the face has holes")
		return
	}
	surfaceRebuild(ch, s)
	surfaceSetStatus("Connected — the face is now two")
}

// surfaceToggleHole marks the selected faces open, or closes them again.
//
// A hole rather than a deletion, because the ring is worth keeping: it is the
// shape of the opening, and it stays draggable. Deleting the face throws that
// away and leaves nothing to adjust.
func surfaceToggleHole() {
	ch, s, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}
	if Editor.SurfaceMode != surfaceSelFace || len(Editor.SurfaceFaces) == 0 {
		surfaceSetStatus("Nothing to open — switch to face select [3] and click a face")
		return
	}
	EditorPushUndo()
	// The first face's current state decides the direction, so a mixed
	// selection resolves one way rather than inverting face by face.
	open := !mesh.Faces[Editor.SurfaceFaces[0]].Hole
	for _, fi := range Editor.SurfaceFaces {
		if fi >= 0 && fi < len(mesh.Faces) {
			mesh.Faces[fi].Hole = open
		}
	}
	surfaceRebuild(ch, s)
	if open {
		surfaceSetStatus("Opened %d faces — they are a hole through the surface now", len(Editor.SurfaceFaces))
	} else {
		surfaceSetStatus("Closed %d faces", len(Editor.SurfaceFaces))
	}
}

// surfaceDeleteFaces removes the selected faces outright.
func surfaceDeleteFaces() {
	ch, s, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}
	if Editor.SurfaceMode != surfaceSelFace || len(Editor.SurfaceFaces) == 0 {
		return
	}
	if len(Editor.SurfaceFaces) >= len(mesh.Faces) {
		surfaceSetStatus("Refusing to delete every face — mark them a hole instead [H]")
		return
	}
	EditorPushUndo()
	mesh.SurfaceDeleteFaces(Editor.SurfaceFaces)
	clearSurfaceSelection()
	Editor.SurfacePending = surfacePendingExtrude{}
	surfaceRebuild(ch, s)
	surfaceSetStatus("Deleted")
}

// surfaceResetMesh throws the shape away and puts the surface back to flat.
func surfaceResetMesh() {
	ch, s, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		return
	}
	EditorPushUndo()
	*mesh = world.SeedSurfaceMesh(frame)
	clearSurfaceSelection()
	Editor.SurfacePending = surfacePendingExtrude{}
	surfaceRebuild(ch, s)
	surfaceSetStatus("Surface reset to flat")
}

// ---------- keyboard ----------

// surfaceEditHandleKey processes surface mode's keys, returning true when the
// event was consumed. It runs before the 3D view's own bindings, so the digits
// address the mesh rather than the sector while a surface is being shaped.
func surfaceEditHandleKey(t sdl.KeyboardEvent) bool {
	if !surfaceEditActive() || t.Type != sdl.KEYDOWN {
		return false
	}
	ctrl := sdl.GetModState()&sdl.KMOD_CTRL != 0

	// Ctrl+A selects everything in the current mode. On the Ctrl rather than on
	// a bare letter because every free bare letter is worth more than a
	// shortcut that already has a universal chord, and because A on its own is
	// strafe-left; see the note on the bare-key switch below.
	if ctrl {
		if t.Keysym.Scancode == sdl.SCANCODE_A {
			surfaceSelectAll()
			return true
		}
		// Every other chord belongs to somebody else — Ctrl+Z and Ctrl+Y are
		// undo and redo, and shaping a surface must not swallow them.
		return false
	}

	// The bare-letter operations deliberately avoid W, A, S and D.
	//
	// Those four fly the camera, and the camera reads them by *polling* the
	// keyboard rather than from the event queue, so consuming the event here
	// would not stop the movement — the two would simply both happen. Worse,
	// key repeat fires KEYDOWN over and over while a key is held, so holding S
	// to fly backwards across a room would have subdivided the selected face
	// once per repeat.
	//
	// Shaping a surface is something you do while flying around it: you push a
	// face in, fly round to see what it did, and push it again. So the movement
	// keys win and the operations work around them — the same trade the model
	// editor made when it put its scale tool on C rather than the conventional
	// S (editor_model_window.go).
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_ESCAPE:
		ExitSurfaceEdit()
		return true
	case sdl.SCANCODE_1:
		surfaceSetSelMode(surfaceSelPoint)
		return true
	case sdl.SCANCODE_2:
		surfaceSetSelMode(surfaceSelEdge)
		return true
	case sdl.SCANCODE_3:
		surfaceSetSelMode(surfaceSelFace)
		return true
	case sdl.SCANCODE_E:
		surfaceExtrude()
		return true
	case sdl.SCANCODE_I:
		surfaceInset()
		return true
	case sdl.SCANCODE_C:
		surfaceConnect()
		return true
	case sdl.SCANCODE_H:
		surfaceToggleHole()
		return true
	case sdl.SCANCODE_B:
		// B, not the S this started on: S flies the camera backwards.
		surfaceSubdivide()
		return true
	case sdl.SCANCODE_DELETE, sdl.SCANCODE_BACKSPACE:
		surfaceDeleteFaces()
		return true
	}
	return false
}

// surfaceSelectAll selects everything in the current mode.
func surfaceSelectAll() {
	_, _, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}
	clearSurfaceSelection()
	switch Editor.SurfaceMode {
	case surfaceSelPoint:
		for i := range mesh.Points {
			Editor.SurfaceVerts = append(Editor.SurfaceVerts, i)
		}
	case surfaceSelEdge:
		all := make([]int, len(mesh.Points))
		for i := range all {
			all[i] = i
		}
		Editor.SurfaceEdges = surfaceEdgesWithin(mesh, all)
	case surfaceSelFace:
		for i := range mesh.Faces {
			Editor.SurfaceFaces = append(Editor.SurfaceFaces, i)
		}
	}
	surfaceSetStatus("Selected all %ss", Editor.SurfaceMode)
}

// surfaceSelectionCenter is where the drag gizmo sits: the centroid of whatever
// is selected, in chunk-local space carried out to the world.
func surfaceSelectionCenter() (mgl32.Vec3, bool) {
	ch, _, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		return mgl32.Vec3{}, false
	}
	pts := surfaceSelectedPoints(mesh)
	if len(pts) == 0 {
		return mgl32.Vec3{}, false
	}
	var sum mgl32.Vec3
	for _, i := range pts {
		p := mesh.Points[i]
		sum = sum.Add(frame.ToWorld(p.U, p.V, p.Depth))
	}
	local := sum.Mul(1 / float32(len(pts)))
	return ch.LocalToWorld(local), true
}
