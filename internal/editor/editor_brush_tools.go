package editor

// editor_brush_tools.go — making and reshaping brushes.
//
// A brush is a convex polyhedron authored as a vertex + face list, so every
// tool here is a mesh operation, not a parametric one: the stamps below emit
// real corners and real face loops, and extrude/inset/hollow rewrite them. That
// is the whole point of the format (see world/brush.go) — the level is edited
// with the same vocabulary a model is.
//
// Convexity is the one invariant the editor owes the rest of the engine.
// Collision reads a brush's verts as a hull directly, and the compile pipeline
// clips faces as half-spaces; both are wrong on a concave brush rather than
// merely slow. Every operation here either preserves convexity by construction
// (the stamps, extrude along a face normal, inset) or is checked afterwards and
// reported — see warnIfSelectionBrokeConvexity, which a free vertex drag runs
// because a free vertex drag genuinely can break it.
//
// Nothing here rebuilds the whole level. A brush owns its own GPU buffers, so
// an edit reloads that brush and recomputes its chunk's bounds; the chunk's
// other brushes are untouched.

import (
	"math"
	"strings"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	// defaultBrushWallThickness is how deep a hollowed room's shell is, and
	// what a new chunk's starter room uses. Thick enough to be visible from
	// outside and to give the floor something to stand on, thin enough that a
	// small room is still mostly room.
	defaultBrushWallThickness float32 = 0.5

	// defaultBrushHalf is the half-extent a stamped primitive starts at, so a
	// fresh box is 2x2x2 world units — one comfortable step, and a round number
	// on every sensible grid.
	defaultBrushHalf float32 = 1.0

	// brushCylinderSides is how many sides a stamped cylinder has. Twelve is
	// the smallest count that reads as round at arm's length while staying
	// cheap: a cylinder brush is 12 side faces plus a cap at each end, and
	// every one of them is a hull plane collision has to test.
	brushCylinderSides = 12

	// defaultBrushTexture is what a freshly stamped brush's faces are painted
	// with: nothing. A blocked-out solid has not been given a material yet, and
	// naming one for it is a lie the author then has to notice and undo — the
	// level ends up full of "concrete" that nobody chose. An untextured face is
	// drawn from its own normal instead (NormalColorFlag in gfx/geometry.go),
	// so a fresh brush reads as shape, with a different colour per plane,
	// rather than as a placeholder material or a flat white slab.
	defaultBrushTexture = ""
)

// ---------- placing a brush ----------

// editorAddBrushAt stamps a primitive into the active chunk, centred on pos
// (world space) and snapped to the grid when snapping is on.
//
// The brush lands *selected*, unlike the scenery brush's stamps: a solid is
// blocked out and then shaped, so the next thing the author wants is a handle
// on what they just made. That costs the armed stamp below nothing, because an
// armed tool suppresses the gizmo — see gizmoSuppressed — so the selection's
// arms cannot swallow the click that places the next brush.
func editorAddBrushAt(kind string, pos mgl32.Vec3) {
	lvl := ws().level
	ch := ws().activeChunk()
	if lvl == nil || ch == nil {
		return
	}
	pos = brushStampAnchor(pos)
	// pos is where the author clicked, in world space; a brush's verts are
	// chunk-local, so the chunk's own placement is divided out here rather
	// than being baked into the geometry.
	local := brushStampLocalCenter(ch, kind, pos)
	half := mgl32.Vec3{defaultBrushHalf, defaultBrushHalf, defaultBrushHalf}

	ws().pushUndo()

	id := world.UniqueBrushID(ch, kind)
	var b world.Brush
	switch kind {
	case brushKindWedge:
		b = newWedgeBrush(id, local, half, defaultBrushTexture)
	case brushKindCylinder:
		b = newCylinderBrush(id, local, half, brushCylinderSides, defaultBrushTexture)
	default:
		b = world.NewBoxBrush(id, local, half, defaultBrushTexture)
	}
	appendBrush(ch, b)
	selectNewestBrush(ch)
	ws().setStatus("Added %s brush %s", kind, id)
}

// brushStampAnchor is where a click puts a brush: the point itself, snapped to
// the grid when snapping is on. Its own function because the ghost has to agree
// with the stamp exactly — a preview drawn at the unsnapped point and geometry
// that lands half a unit away is worse than no preview at all.
func brushStampAnchor(pos mgl32.Vec3) mgl32.Vec3 {
	if ws().SnapToGrid && ws().StepSize > 0 {
		return snapVec3(pos, ws().StepSize)
	}
	return pos
}

// brushStampLocalCenter is where the stamped primitive's centre ends up in
// chunk-local space, given the (already snapped) world point clicked.
//
// The box's lift lives here rather than beside the stamps themselves, so the
// ghost and the geometry cannot disagree about whether a box sits on the click
// or straddles it.
func brushStampLocalCenter(ch *world.Chunk, kind string, pos mgl32.Vec3) mgl32.Vec3 {
	local := ch.WorldToLocal(pos)
	if kind != brushKindWedge && kind != brushKindCylinder {
		// A box sits on the click rather than straddling it: the click landed
		// on a surface, and a block half-buried in the floor is never what was
		// meant.
		local[1] += defaultBrushHalf
	}
	return local
}

// ---------- the stamp, armed ----------

// The stamp is armed rather than one-shot, unlike every other Add menu entry
// that drops something into the level. Blocking out a room is a dozen boxes in
// a row, and a round trip through the menu for each of them is not a workflow —
// the same argument the scenery brush makes at length in editor_scenery.go, and
// this is the same arrangement: it stays armed until Escape puts it away, a
// ghost of what will land follows the cursor, and every click places another.
//
// Undo is per stamp, matching the click: one Ctrl+Z takes back one brush.

// brushPlacePos is where the ghost was last drawn — the point the next stamp
// lands on. Recorded during the draw, which is the one place per frame the ray
// is cast, and read by the click rather than cast again: a second ray is a
// second answer to the same question.
var brushPlacePos mgl32.Vec3

// brushPlaceValid says whether brushPlacePos is from this frame. False means
// the cursor was over the panel, or nothing is armed, and a click must not
// stamp anything.
var brushPlaceValid bool

// brushPlaceGhostColor is the preview's outline — cyan, which nothing else in
// the viewport uses for geometry, so the ghost is never mistaken for a
// selection (yellow).
var brushPlaceGhostColor = mgl32.Vec3{0.2, 0.9, 1}

// editorArmBrushPlacement points the stamp at a primitive and switches it on.
func editorArmBrushPlacement(kind string) {
	if ws().activeChunk() == nil {
		ws().setStatus("No chunk to place a brush in")
		return
	}
	if editorInDetailMode() {
		// Detail mode shapes ONE brush's surface and nothing else is clickable
		// in it, so an armed stamp would drop solids into a context that cannot
		// select them. Arming it is the author saying they are placing again.
		editorExitDetailMode()
	}
	ws().BrushPlaceKind = kind
	ws().BrushPlaceActive = true
	// The one-shot modes, the scenery brush and this one are mutually
	// exclusive: they all answer the next left-click, and two armed at once
	// would make which one wins a question about the order of the checks in
	// Editor3DHandleClick.
	ws().disarmOtherPlacements()
	ws().setStatus("%s brush — click to place, Escape to stop", brushKindLabel(kind))
}

// editorDisarmBrushPlacement puts the stamp away.
func editorDisarmBrushPlacement() {
	ws().BrushPlaceActive = false
	brushPlaceValid = false
}

// editorBrushPlaceHandleClick stamps at the cursor when the stamp is armed, and
// reports whether it consumed the click.
//
// It uses the point the ghost was drawn at rather than casting a fresh ray, so
// what lands is exactly what was being previewed. A frame where the ghost was
// not drawn — the cursor over the side panel — places nothing rather than
// guessing, but still consumes the click: a click while stamping is never a
// selection.
func editorBrushPlaceHandleClick() bool {
	if !ws().BrushPlaceActive {
		return false
	}
	if brushPlaceValid {
		editorAddBrushAt(ws().BrushPlaceKind, brushPlacePos)
	}
	return true
}

// editorBrushPlaceHandleKey owns Escape while the stamp is armed, and reports
// whether it consumed the key.
func editorBrushPlaceHandleKey(scancode sdl.Scancode) bool {
	if !ws().BrushPlaceActive || scancode != sdl.SCANCODE_ESCAPE {
		return false
	}
	editorDisarmBrushPlacement()
	ws().setStatus("Brush stamp off")
	return true
}

// editorBrushStampMenuItem is the 3D context menu's entry for the stamp: arm
// one of the primitives, or put an armed one away.
//
// In the menu as well as the panel because arming from the viewport is the
// point of the tool — an author blocking out a room is already looking at where
// the next box goes, and a trip to the side panel is the round trip the stamp
// exists to remove.
func editorBrushStampMenuItem() ctxMenuItem {
	if ws().BrushPlaceActive {
		return ctxMenuItem{
			Label:   "Stop stamping " + brushKindLabel(ws().BrushPlaceKind) + " brushes",
			Do:      editorDisarmBrushPlacement,
			Tooltip: "Put the brush stamp away. Escape does the same.",
		}
	}
	if ws().activeChunk() == nil {
		return ctxMenuItem{
			Label:    "Stamp brushes",
			Disabled: true,
			Tooltip:  "Make a chunk for them to live in first.",
		}
	}
	kinds := make([]ctxMenuItem, 0, len(brushKinds))
	for _, kind := range brushKinds {
		k := kind
		kinds = append(kinds, ctxMenuItem{
			Label: brushKindLabel(k),
			Do:    func() { editorArmBrushPlacement(k) },
		})
	}
	return ctxMenuItem{
		Label:   "Stamp brushes",
		Sub:     kinds,
		Tooltip: "A box follows the cursor and every click places another\nsolid. Stays on until Escape.",
	}
}

// DrawBrushPlaceGhost outlines where the next stamp will land, and records that
// point for the click to use.
//
// Drawn as the primitive's own bounding box carried out through the chunk's
// placement, so a rotated or scaled chunk gets a box where its brush will
// actually be rather than an axis-aligned guess. Called from the overlay pass
// with depth testing off, so the outline is visible against the surface it is
// resting on instead of z-fighting with it.
func DrawBrushPlaceGhost() {
	brushPlaceValid = false
	if !ws().editing() || !ws().BrushPlaceActive || ws().level == nil {
		return
	}
	ch := ws().activeChunk()
	if ch == nil {
		return
	}
	// Where the cursor is, asked of SDL rather than remembered from an event —
	// the same source the gizmo's hover test uses. Over the side panel the ray
	// would be cast through a pixel the viewport does not own, so nothing is
	// drawn, which is also the signal the click reads.
	mx, my, _ := sdl.GetMouseState()
	if imgui.CurrentIO().WantCaptureMouse() || ws().cursorOverPanel(mx) {
		return
	}

	brushPlacePos = brushStampAnchor(workspacePointAt(mx, my))
	brushPlaceValid = true

	c := brushStampLocalCenter(ch, ws().BrushPlaceKind, brushPlacePos)
	h := defaultBrushHalf
	corner := func(sx, sy, sz float32) mgl32.Vec3 {
		return ch.LocalToWorld(mgl32.Vec3{c.X() + sx*h, c.Y() + sy*h, c.Z() + sz*h})
	}
	lo := [4]mgl32.Vec3{corner(-1, -1, -1), corner(1, -1, -1), corner(1, -1, 1), corner(-1, -1, 1)}
	hi := [4]mgl32.Vec3{corner(-1, 1, -1), corner(1, 1, -1), corner(1, 1, 1), corner(-1, 1, 1)}
	for i := 0; i < 4; i++ {
		gizmo.GizmoLine(lo[i], lo[(i+1)%4], brushPlaceGhostColor)
		gizmo.GizmoLine(hi[i], hi[(i+1)%4], brushPlaceGhostColor)
		gizmo.GizmoLine(lo[i], hi[i], brushPlaceGhostColor)
	}
}

// brushKind* are the primitives the stamp offers.
const (
	brushKindBox      = "box"
	brushKindWedge    = "wedge"
	brushKindCylinder = "cylinder"
)

// brushKinds is the stamp menu's order, which is also roughly how often each
// is reached for.
var brushKinds = []string{brushKindBox, brushKindWedge, brushKindCylinder}

// brushKindLabel is a kind as it is shown to an author — the constant with its
// first letter raised, so the menu and the status line say Box, not box.
func brushKindLabel(kind string) string {
	if kind == "" {
		return ""
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// editorHollowSelectedBrush replaces the brush the selection names with the six
// brushes of a hollow room filling its bounds — the fast way to turn a blocked-
// out solid into somewhere to stand.
//
// It works from the brush's axis-aligned bounds rather than its actual shape,
// because that is what NewHollowRoom can express. Hollowing a wedge therefore
// gives a box-shaped room, which is honest: there is no six-brush shell of an
// arbitrary polyhedron, and pretending otherwise would silently produce
// something other than what was asked for.
func editorHollowSelectedBrush() {
	ci, bi := activeBrushIndices()
	ch, b := editor3DActiveBrush()
	if ch == nil || b == nil || bi < 0 {
		return
	}
	lo, hi := b.LocalAABB()
	if hi.X()-lo.X() <= 2*defaultBrushWallThickness ||
		hi.Y()-lo.Y() <= 2*defaultBrushWallThickness ||
		hi.Z()-lo.Z() <= 2*defaultBrushWallThickness {
		ws().setStatus("%s is too small to hollow", b.ID)
		return
	}
	// The shell goes *inside* the original bounds, so hollowing does not grow
	// the solid the author placed: the outer surface stays where they put it.
	t := defaultBrushWallThickness
	inner := [2]mgl32.Vec3{
		{lo.X() + t, lo.Y() + t, lo.Z() + t},
		{hi.X() - t, hi.Y() - t, hi.Z() - t},
	}
	// The shell inherits whatever the solid wore, including nothing.
	texture := defaultBrushTexture
	if len(b.Faces) > 0 {
		texture = b.Faces[0].Texture
	}
	id := b.ID

	ws().pushUndo()
	room := world.NewHollowRoom(id, inner[0], inner[1], t, texture)
	b.Release()
	ch.Brushes = append(ch.Brushes[:bi], ch.Brushes[bi+1:]...)
	for i := range room {
		room[i].ID = world.UniqueBrushID(ch, room[i].ID)
		appendBrush(ch, room[i])
	}
	// Every selection, because all three could be naming the brush that has
	// just been removed — and in object mode it certainly was.
	clearBrushSelection()
	clearFaceSelection()
	clearVertexSelection()
	ws().ActiveChunkIdx = ci
	ws().setStatus("Hollowed %s into %d brushes", id, len(room))
}

// editorDuplicateSelectedBrush copies the selected brush in place. The copy
// lands selected and exactly on top of the original, ready to be dragged off
// it — the same convention duplicating a chunk follows.
func editorDuplicateSelectedBrush() {
	ch, b := editor3DActiveBrush()
	if ch == nil || b == nil {
		return
	}
	ws().pushUndo()
	clone := world.CloneBrush(b, "_copy")
	clone.ID = world.UniqueBrushID(ch, clone.ID)
	appendBrush(ch, clone)
	selectNewestBrush(ch)
	ws().setStatus("Duplicated %s as %s", b.ID, clone.ID)
}

// appendBrush adds b to ch, uploads its geometry and rolls the chunk's bounds
// up around it. Every path that creates a brush goes through here so none of
// them can forget the upload — an un-Loaded brush is invisible and unclickable,
// which reads as "the tool did nothing".
func appendBrush(ch *world.Chunk, b world.Brush) {
	ch.Brushes = append(ch.Brushes, b)
	_ = ch.Brushes[len(ch.Brushes)-1].Load()
	editorChunkGeometryChanged(ch)
}

// selectNewestBrush selects the last brush of ch so the properties panel opens
// on something and the gizmo has a handle.
//
// In the unit of whichever context is current: the whole brush in object mode,
// its first face in edit mode. Placing a brush is an object-mode act and a
// freshly stamped box wants dragging into place, so the common path gets a
// gizmo on the solid rather than on one arbitrary side of it.
func selectNewestBrush(ch *world.Chunk) {
	bi := len(ch.Brushes) - 1
	if bi < 0 || len(ch.Brushes[bi].Faces) == 0 {
		return
	}
	if !editorInEditMode() {
		editorSelectBrushObject(brushSel{chunkIdx: ws().ActiveChunkIdx, brushIdx: bi}, false)
		return
	}
	setFaceSelection([]faceSel{brushFaceSel(ws().ActiveChunkIdx, bi, 0)})
	ws().PanelFocus = panelFocusFace
}

// ---------- the primitives ----------

// newWedgeBrush is a box with its +Y+Z edge collapsed onto +Y: a right prism
// with a triangular cross-section, sloping up from -Z to +Z. The ramp, and the
// roof pitch.
//
// Six corners rather than a box's eight, so the two end faces are triangles and
// the slope is a single quad. Winding is CCW seen from outside, as every brush
// face must be (world/brush.go).
func newWedgeBrush(id string, center, half mgl32.Vec3, texture string) world.Brush {
	cx, cy, cz := center.X(), center.Y(), center.Z()
	hx, hy, hz := half.X(), half.Y(), half.Z()
	// Bottom rectangle 0..3, top edge 4..5 along +Z.
	verts := []mgl32.Vec3{
		{cx - hx, cy - hy, cz - hz}, // 0
		{cx + hx, cy - hy, cz - hz}, // 1
		{cx + hx, cy - hy, cz + hz}, // 2
		{cx - hx, cy - hy, cz + hz}, // 3
		{cx - hx, cy + hy, cz + hz}, // 4
		{cx + hx, cy + hy, cz + hz}, // 5
	}
	faces := []world.BrushFace{
		world.NewBrushFace([]int{0, 1, 2, 3}, texture), // -Y, the base
		world.NewBrushFace([]int{3, 2, 5, 4}, texture), // +Z, the tall back
		world.NewBrushFace([]int{0, 4, 5, 1}, texture), // the slope
		world.NewBrushFace([]int{0, 3, 4}, texture),    // -X end
		world.NewBrushFace([]int{1, 5, 2}, texture),    // +X end
	}
	return world.Brush{ID: id, BrushMesh: world.BrushMesh{Verts: verts, Faces: faces}, Solid: true}
}

// newCylinderBrush is a right prism on a regular n-gon in the XZ plane: a
// column, a pillar, a round tower.
//
// It is a prism rather than a true cylinder because a brush is a polyhedron —
// there is no curved face to author. sides is what trades roundness against
// the number of hull planes collision and the compiler have to carry.
func newCylinderBrush(id string, center, half mgl32.Vec3, sides int, texture string) world.Brush {
	if sides < 3 {
		sides = 3
	}
	cx, cy, cz := center.X(), center.Y(), center.Z()
	rx, hy, rz := half.X(), half.Y(), half.Z()

	// Bottom ring 0..n-1, top ring n..2n-1, both wound CCW seen from above.
	verts := make([]mgl32.Vec3, 0, sides*2)
	for i := 0; i < sides; i++ {
		a := 2 * math.Pi * float64(i) / float64(sides)
		x := cx + rx*float32(math.Cos(a))
		z := cz + rz*float32(math.Sin(a))
		verts = append(verts, mgl32.Vec3{x, cy - hy, z})
	}
	for i := 0; i < sides; i++ {
		v := verts[i]
		verts = append(verts, mgl32.Vec3{v.X(), cy + hy, v.Z()})
	}

	faces := make([]world.BrushFace, 0, sides+2)
	// The caps. The bottom ring is CCW from above, so seen from *below* — from
	// outside the solid — it has to be reversed; the top ring is already right.
	bottom := make([]int, sides)
	top := make([]int, sides)
	for i := 0; i < sides; i++ {
		bottom[i] = sides - 1 - i
		top[i] = sides + i
	}
	faces = append(faces, world.NewBrushFace(bottom, texture))
	faces = append(faces, world.NewBrushFace(top, texture))
	for i := 0; i < sides; i++ {
		j := (i + 1) % sides
		faces = append(faces, world.NewBrushFace([]int{i, j, sides + j, sides + i}, texture))
	}
	return world.Brush{ID: id, BrushMesh: world.BrushMesh{Verts: verts, Faces: faces}, Solid: true}
}

// ---------- convexity ----------

// warnIfSelectionBrokeConvexity checks every brush the current selection can
// have moved and says so in the status line when one is no longer convex.
//
// It reports rather than refuses. Refusing would mean rejecting the *first*
// step of a two-step edit that ends up convex — pulling one corner out and then
// its neighbour after it — and an editor that will not let you pass through an
// invalid intermediate state is an editor you cannot shape anything in.
func warnIfSelectionBrokeConvexity() { warnIfBrushesNotConvex(selectionBrushes()) }

// warnIfBrushesNotConvex is the same warning for a stated list of brushes, for
// an edit that has already changed the selection it would otherwise read — a
// dissolve can leave nothing selected on the brush it reshaped.
//
// A concave STATIC brush is FYI only: the compiler splits it into convex
// pieces automatically (compile/validate.go, world.DecomposeConvex), so this
// no longer blocks a build — it just costs a few extra collision/CSG checks
// against the generated pieces. A concave DYNAMIC brush is still a real
// problem worth a harder warning: a script moves it as one rigid convex hull,
// and there is nothing to split it INTO that a script could still address as
// one thing (see brush_dynamic.go).
func warnIfBrushesNotConvex(brushes []*world.Brush) {
	var badDynamic, badStatic []string
	for _, b := range brushes {
		if b.IsConvex() {
			continue
		}
		if b.Dynamic {
			badDynamic = append(badDynamic, b.ID)
		} else {
			badStatic = append(badStatic, b.ID)
		}
	}
	switch {
	case len(badDynamic) == 1:
		ws().setStatus("Warning: dynamic brush %s is concave; a moving brush must stay convex to collide correctly", badDynamic[0])
	case len(badDynamic) > 1:
		ws().setStatus("Warning: dynamic brush %s and %d other(s) are concave; a moving brush must stay convex to collide correctly",
			badDynamic[0], len(badDynamic)-1)
	case len(badStatic) == 1:
		ws().setStatus("%s is concave — it will still compile fine (split into convex pieces automatically)", badStatic[0])
	case len(badStatic) > 1:
		ws().setStatus("%s and %d other brush(es) are concave — they will still compile fine (split into convex pieces automatically)",
			badStatic[0], len(badStatic)-1)
	}
}

// selectionBrushes lists the distinct brushes the current face and vertex
// selections name, in a stable order.
func selectionBrushes() []*world.Brush {
	seen := map[[2]int]struct{}{}
	var out []*world.Brush
	add := func(key [2]int, b *world.Brush) {
		if b == nil {
			return
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, b)
	}
	// Detail selections are skipped outright: a detail mesh has no convexity
	// rule to break — it is not the solid — so checking one would either warn
	// about nothing or, worse, warn about the hull the author has not touched.
	for _, sel := range ws().SelectedFaces {
		if sel.detail {
			continue
		}
		_, b := faceSelBrush(sel)
		add([2]int{sel.chunkIdx, sel.brushIdx}, b)
	}
	for _, sel := range ws().SelectedVertices {
		if sel.detail {
			continue
		}
		_, b := vertexSelBrush(sel)
		add([2]int{sel.chunkIdx, sel.brushIdx}, b)
	}
	return out
}
